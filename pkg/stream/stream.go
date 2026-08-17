// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/pkg/storage"
)

const (
	defaultIdleTimeout          = 30 * time.Second
	defaultFileStorageReadahead = 10 * 1024 * 1024 // 10MB
)

// streamReaderIDCounter generates unique IDs for stream reader
// instances so the storage layer can track concurrent readers independently.
var streamReaderIDCounter atomic.Uint64

// ReaderInfo represents an active stream reader's position and window in piece indices.
type ReaderInfo struct {
	End    int `json:"end"`    // Piece index end of active window (exclusive).
	Reader int `json:"reader"` // Current reader piece index.
	Start  int `json:"start"`  // Piece index start of trailing demuxer buffer (inclusive).
}

// Config defines the configuration for a stream reader Pool.
type Config struct {
	// Storage is the optional in-memory storage client for active range registration.
	Storage *storage.Client

	// Logger for debug and error logging. Defaults to slog.Default().
	Logger *slog.Logger

	// IdleTimeout is how long an unreferenced stream reader stays warm before being closed.
	// Defaults to 30 seconds.
	IdleTimeout time.Duration

	// FileStorageReadahead is the fixed readahead in bytes for disk-based torrents.
	// Defaults to 10MB.
	FileStorageReadahead int64
}

// Pool manages shared, refcounted torrent file stream readers with dynamic
// fair-share readahead distribution and active range tracking for storage eviction.
type Pool struct {
	closeOnce            sync.Once
	fileStorageReadahead int64
	idleTimeout          time.Duration
	logger               *slog.Logger
	mu                   sync.Mutex
	readers              map[string][]*streamReader
	shutdownCh           chan struct{}
	storage              *storage.Client
}

// New creates a new stream Pool.
func New(cfg Config) *Pool {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}

	fileStorageReadahead := cfg.FileStorageReadahead
	if fileStorageReadahead <= 0 {
		fileStorageReadahead = defaultFileStorageReadahead
	}

	p := &Pool{
		fileStorageReadahead: fileStorageReadahead,
		idleTimeout:          idleTimeout,
		logger:               logger,
		readers:              make(map[string][]*streamReader),
		shutdownCh:           make(chan struct{}),
		storage:              cfg.Storage,
	}

	go p.startCleaner()

	return p
}

// Acquire returns an io.ReadSeeker for the given torrent file along with a release
// function that the caller must invoke when the streaming session completes.
func (p *Pool) Acquire(
	ih metainfo.Hash,
	file *torrent.File,
	pieceLength int64,
	isFileStorage bool,
	totalReadaheadPool int64,
) (io.ReadSeeker, func()) {
	key := readerKey(ih, file.Path())

	p.mu.Lock()
	var sr *streamReader
	for _, existing := range p.readers[key] {
		existing.mu.Lock()
		if existing.refs == 0 {
			sr = existing
			sr.refs++
			sr.lastUsed = time.Now()
			sr.pieceLength = pieceLength
			sr.isFileStorage = isFileStorage
			existing.mu.Unlock()
			break
		}
		existing.mu.Unlock()
	}

	if sr == nil {
		sr = &streamReader{
			fileLen:       file.Length(),
			fileOffset:    file.Offset(),
			filePath:      file.Path(),
			hash:          ih,
			isFileStorage: isFileStorage,
			lastUsed:      time.Now(),
			pieceLength:   pieceLength,
			positions:     &sync.Map{},
			reader:        newReadAtReader(file.NewReader()),
			readerID:      streamReaderIDCounter.Add(1),
			refs:          1,
			storage:       p.storage,
		}
		p.readers[key] = append(p.readers[key], sr)
	}
	p.mu.Unlock()

	if isFileStorage {
		sr.mu.Lock()
		sr.readahead = p.fileStorageReadahead
		sr.mu.Unlock()
		if sr.reader != nil {
			sr.reader.SetReadahead(p.fileStorageReadahead)
		}
	} else {
		p.RefreshReadahead(totalReadaheadPool)
	}

	secReader := io.NewSectionReader(sr.reader, 0, sr.fileLen)
	tracker := newActiveRangeTracker(secReader, sr)

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			p.releaseReader(sr, totalReadaheadPool)
		})
	}

	return tracker, release
}

// RefreshReadahead updates the readahead size on all active memory readers.
// Readahead values are calculated and updated in reader structs under Pool.mu, then
// applied to the underlying torrent readers outside the lock to prevent deadlocks with active streaming reads.
func (p *Pool) RefreshReadahead(totalReadaheadPool int64) {
	p.mu.Lock()
	activeCount := 0
	for _, srList := range p.readers {
		for _, sr := range srList {
			sr.mu.Lock()
			if sr.refs > 0 && !sr.isFileStorage {
				activeCount++
			}
			sr.mu.Unlock()
		}
	}

	var updates []readerReadaheadUpdate
	for _, srList := range p.readers {
		for _, sr := range srList {
			sr.mu.Lock()
			if sr.refs > 0 && !sr.isFileStorage {
				newReadahead := ComputeReadahead(sr.pieceLength, totalReadaheadPool, activeCount)
				sr.readahead = newReadahead
				if sr.reader != nil {
					updates = append(updates, readerReadaheadUpdate{
						readahead: newReadahead,
						reader:    sr.reader,
					})
				}
			}
			sr.mu.Unlock()
		}
	}
	p.mu.Unlock()

	for _, u := range updates {
		u.reader.SetReadahead(u.readahead)
	}
}

// ReaderPositions returns current reader piece indexes for all active streams
// of the given torrent. Each entry is the read window [start, end) for that stream.
func (p *Pool) ReaderPositions(ih metainfo.Hash) []ReaderInfo {
	prefix := ih.HexString()
	var out []ReaderInfo

	p.mu.Lock()
	for key, srList := range p.readers {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, sr := range srList {
			sr.positions.Range(func(k, v any) bool {
				w, ok := v.(ReaderInfo)
				if !ok {
					return true
				}
				out = append(out, w)
				return true
			})
		}
	}
	p.mu.Unlock()

	return out
}

// Close shuts down the pool and cleans up all underlying readers.
func (p *Pool) Close() error {
	p.closeOnce.Do(func() {
		close(p.shutdownCh)
		p.cleanupIdle(time.Now().Add(24 * time.Hour))
	})
	return nil
}

// CloseTorrent closes and removes all stream readers associated with the given torrent hash.
func (p *Pool) CloseTorrent(ih metainfo.Hash) {
	prefix := ih.HexString() + ":"

	p.mu.Lock()
	var toClose []*streamReader
	for key, srList := range p.readers {
		if strings.HasPrefix(key, prefix) {
			toClose = append(toClose, srList...)
			delete(p.readers, key)
		}
	}
	p.mu.Unlock()

	for _, sr := range toClose {
		if p.storage != nil {
			p.storage.ClearActiveRange(sr.hash, sr.readerID)
		}
		sr.positions.Delete(sr.readerID)
		if sr.reader != nil {
			if err := sr.reader.Close(); err != nil && p.logger != nil {
				p.logger.Error("failed to close torrent reader on torrent removal", "err", err)
			}
		}
	}
}

// ComputeReadahead calculates the per-reader readahead size given the
// total available pool and active memory streams count.
func ComputeReadahead(pieceLength int64, totalReadahead int64, activeMemoryStreams int) int64 {
	if activeMemoryStreams <= 1 {
		return totalReadahead
	}

	perReader := totalReadahead / int64(activeMemoryStreams)

	// Bounded minimum floor: at least 2 pieces or 10MB
	minFloor := int64(10 * 1024 * 1024)
	if pieceLength > 0 && 2*pieceLength > minFloor {
		minFloor = 2 * pieceLength
	}

	if perReader < minFloor {
		perReader = minFloor
	}
	if perReader > totalReadahead {
		perReader = totalReadahead
	}
	return perReader
}

func (p *Pool) releaseReader(sr *streamReader, totalReadaheadPool int64) {
	p.mu.Lock()
	sr.mu.Lock()
	sr.refs--
	sr.lastUsed = time.Now()
	isFile := sr.isFileStorage
	sr.mu.Unlock()
	p.mu.Unlock()

	if !isFile {
		p.RefreshReadahead(totalReadaheadPool)
	}
}

func (p *Pool) startCleaner() {
	ticker := time.NewTicker(p.idleTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.cleanupIdle(time.Now())
		case <-p.shutdownCh:
			return
		}
	}
}

func (p *Pool) cleanupIdle(now time.Time) {
	p.mu.Lock()
	var toClose []*streamReader
	for key, srList := range p.readers {
		var active []*streamReader
		for _, sr := range srList {
			sr.mu.Lock()
			idle := sr.refs == 0 && now.Sub(sr.lastUsed) > p.idleTimeout
			sr.mu.Unlock()
			if idle {
				toClose = append(toClose, sr)
			} else {
				active = append(active, sr)
			}
		}
		if len(active) == 0 {
			delete(p.readers, key)
		} else {
			p.readers[key] = active
		}
	}
	p.mu.Unlock()

	for _, sr := range toClose {
		if p.storage != nil {
			p.storage.ClearActiveRange(sr.hash, sr.readerID)
		}
		sr.positions.Delete(sr.readerID)
		if sr.reader != nil {
			if err := sr.reader.Close(); err != nil && p.logger != nil {
				p.logger.Error("failed to close idle torrent reader", "err", err)
			}
		}
	}
}

// readAtReader wraps a torrent.Reader into a mutex-guarded io.ReaderAt.
// Each ReadAt call executes an atomic Seek+Read under a mutex.
// SetReadahead is non-blocking and delegates directly to the underlying
// torrent.Reader (which is internally synchronized in anacrolix/torrent).
type readAtReader struct {
	mu sync.Mutex
	r  torrent.Reader
}

func newReadAtReader(r torrent.Reader) *readAtReader {
	return &readAtReader{r: r}
}

func (rr *readAtReader) Close() error {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.r.Close()
}

func (rr *readAtReader) ReadAt(p []byte, off int64) (int, error) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if _, err := rr.r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadAtLeast(rr.r, p, len(p))
}

func (rr *readAtReader) SetReadahead(a int64) {
	rr.r.SetReadahead(a)
}

// streamReader is a refcounted, shared reader for one torrent file stream, kept
// alive past refs hitting zero for IdleTimeout so consecutive requests
// in the same session reuse the warmed-up readahead window.
type streamReader struct {
	fileLen       int64
	fileOffset    int64
	filePath      string
	hash          metainfo.Hash
	isFileStorage bool
	lastPos       int64
	lastUsed      time.Time
	mu            sync.Mutex
	pieceLength   int64
	positions     *sync.Map
	readahead     int64
	reader        *readAtReader
	readerID      uint64
	refs          int
	storage       *storage.Client
}

// updateRange registers or refreshes the active readahead range in the storage client
// and in the active positions map for API reporting.
func (sr *streamReader) updateRange(posInFile int64) {
	sr.mu.Lock()
	sr.lastPos = posInFile
	sr.lastUsed = time.Now()
	fileOffset := sr.fileOffset
	readahead := sr.readahead
	pieceLength := sr.pieceLength
	sr.mu.Unlock()

	readerBytePos := fileOffset + posInFile
	// Reserve a trailing buffer behind the readhead to protect the media player's
	// internal demuxer buffer and allow instant user rewinds.
	trailingBuffer := readahead / 4
	if pieceLength > 0 && trailingBuffer < 2*pieceLength {
		trailingBuffer = 2 * pieceLength
	}
	windowStart := readerBytePos - trailingBuffer
	if windowStart < 0 {
		windowStart = 0
	}
	windowEnd := readerBytePos + readahead
	if windowEnd <= windowStart {
		windowEnd = windowStart + 1
	}

	if sr.storage != nil && !sr.isFileStorage {
		sr.storage.SetActiveRange(sr.hash, sr.readerID, windowStart, windowEnd)
	}

	if pieceLength > 0 {
		sr.positions.Store(sr.readerID, ReaderInfo{
			End:    int((windowEnd + pieceLength - 1) / pieceLength),
			Reader: int(readerBytePos / pieceLength),
			Start:  int(windowStart / pieceLength),
		})
	}
}

// activeRangeTracker wraps an io.ReadSeeker and updates the reader's
// active range on Read and Seek operations.
type activeRangeTracker struct {
	io.ReadSeeker
	sr *streamReader
}

func newActiveRangeTracker(rs io.ReadSeeker, sr *streamReader) *activeRangeTracker {
	t := &activeRangeTracker{
		ReadSeeker: rs,
		sr:         sr,
	}
	t.updateRange()
	return t
}

func (t *activeRangeTracker) Read(p []byte) (int, error) {
	n, err := t.ReadSeeker.Read(p)
	if n > 0 {
		t.updateRange()
	}
	return n, err
}

func (t *activeRangeTracker) Seek(offset int64, whence int) (int64, error) {
	pos, err := t.ReadSeeker.Seek(offset, whence)
	if err == nil {
		t.updateRange()
	}
	return pos, err
}

func (t *activeRangeTracker) updateRange() {
	pos, err := t.ReadSeeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return
	}
	t.sr.updateRange(pos)
}

type readerReadaheadUpdate struct {
	readahead int64
	reader    *readAtReader
}

func readerKey(ih metainfo.Hash, path string) string {
	return ih.HexString() + ":" + path
}
