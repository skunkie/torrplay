// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// ActiveRangeRegistry is the stream pool's interface to the storage layer.
// It is intentionally minimal so that any storage implementation can
// satisfy it without importing the stream package.
type ActiveRangeRegistry interface {
	// SetActiveRange registers or refreshes a piece-index window [start, end]
	// (inclusive) that a reader is actively consuming. Pieces inside this
	// window are protected from eviction.
	SetActiveRange(hash metainfo.Hash, readerID uint64, start, end int64)

	// ClearActiveRange removes the active range for a specific reader.
	ClearActiveRange(hash metainfo.Hash, readerID uint64)
}

// ReaderInfo describes the current position window of a single reader.
type ReaderInfo struct {
	End      int // piece index of the readahead end.
	Position int // piece index of the current read head.
	Start    int // piece index of the trailing buffer start.
}

// Config configures the stream pool.
type Config struct {
	FileStorageReadahead int64         // default: 50MB, used only for file storage readers
	IdleTimeout          time.Duration // default: 30s, used when no pressure func is configured or memory usage is below 50%
	Logger               *slog.Logger
	// MaxIdleTime is the maximum time a reader may remain parked before it is
	// closed and removed from the pool. Zero means no limit.
	MaxIdleTime time.Duration
	// MemoryPressureFunc returns the current memory usage ratio (0.0–1.0).
	// When set, idle readers are parked faster under memory pressure to
	// prevent pieces from being downloaded and immediately evicted.
	// When nil, a fixed IdleTimeout is used.
	MemoryPressureFunc func() float64
	// Registry tracks active read ranges for piece eviction protection.
	Registry ActiveRangeRegistry // optional, nil = no range tracking
}

// readerKey uniquely identifies a stream reader within the pool.
// readerID makes it unique even when multiple readers exist for the same torrent/file.
type readerKey struct {
	hash     metainfo.Hash
	filePath string
	readerID uint64
}

// readAtWrapper adapts a torrent.Reader (io.ReadSeekCloser) to io.ReaderAt.
// It saves the current position, seeks to the requested offset, reads data,
// then restores the original position. This allows Range requests without
// blocking on sequential reads.
//
// Lock ordering: wrapper.mu is never held while acquiring pool.mu, and
// pool.mu is never held while acquiring wrapper.mu. The callback
// (onOffsetChange) fires AFTER releasing wrapper.mu.
type readAtWrapper struct {
	mu     sync.Mutex
	reader io.ReadSeekCloser
	offset int64 // tracks the read position in bytes

	// onOffsetChange is called when the read position moves, allowing the
	// caller to update the active eviction-protection range. The parameter
	// is the new byte offset. May be nil. Must be called WITHOUT rw.mu held.
	onOffsetChange func(newOffset int64)
}

func (rw *readAtWrapper) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.offset = 0
	return rw.reader.Close()
}

// ReadAt seeks to the requested offset, reads data, restores the original
// position, then notifies the pool of the new offset. The callback fires
// WITHOUT the wrapper lock held to maintain a consistent lock order.
func (rw *readAtWrapper) ReadAt(p []byte, off int64) (int, error) {
	rw.mu.Lock()

	pos, err := rw.reader.Seek(0, io.SeekCurrent)
	if err != nil {
		rw.mu.Unlock()
		return 0, err
	}

	if _, err := rw.reader.Seek(off, io.SeekStart); err != nil {
		_, _ = rw.reader.Seek(pos, io.SeekStart)
		rw.mu.Unlock()
		return 0, err
	}

	n := 0
	for n < len(p) {
		nn, e := rw.reader.Read(p[n:])
		n += nn
		if e != nil {
			_, _ = rw.reader.Seek(pos, io.SeekStart)
			rw.offset = off + int64(n)
			cb := rw.onOffsetChange
			offset := rw.offset
			rw.mu.Unlock()
			if cb != nil {
				cb(offset)
			}
			if n >= len(p) {
				return n, nil
			}
			return n, e
		}
	}

	_, _ = rw.reader.Seek(pos, io.SeekStart)
	rw.offset = off + int64(n)
	cb := rw.onOffsetChange
	offset := rw.offset
	rw.mu.Unlock()
	if cb != nil {
		cb(offset)
	}
	return n, nil
}

// streamReader wraps a torrent.Reader with lifecycle management.
type streamReader struct {
	active        bool
	file          *torrent.File
	hash          metainfo.Hash
	idleSince     time.Time
	isFileStorage bool
	// lastOffset is the last byte offset reported by the onOffsetChange
	// callback. Updated under pool.mu only, so code holding pool.mu can
	// read it without acquiring wrapper.mu.
	lastOffset int64
	rah        int64 // current readahead in bytes (updated by RefreshReadahead)
	reader     torrent.Reader
	readerID   uint64
	wrapper    *readAtWrapper
}

// Pool manages a pool of stream readers keyed by (infohash, filePath, readerID).
type Pool struct {
	closeCh chan struct{}
	closed  bool
	cfg     Config
	logger  *slog.Logger
	mu      sync.Mutex
	nextID  uint64
	readers map[readerKey]*streamReader
}

// New creates a new stream pool.
func New(cfg Config) *Pool {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	if cfg.FileStorageReadahead == 0 {
		cfg.FileStorageReadahead = 50 * 1024 * 1024
	}

	p := &Pool{
		closeCh: make(chan struct{}),
		cfg:     cfg,
		logger:  logger,
		readers: make(map[readerKey]*streamReader),
	}

	go p.idleGC()

	return p
}

// Acquire returns an io.ReadSeeker for reading the given file within a torrent.
// The caller MUST call the returned release function (typically via defer) when
// done reading. The release function is safe to call multiple times.
//
// totalReadaheadPool is the total readahead budget in bytes that should be
// divided among all active readers.
func (p *Pool) Acquire(ctx context.Context, ih metainfo.Hash, file *torrent.File, isFileStorage bool, totalReadaheadPool int64) (io.ReadSeeker, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		reader := file.NewReader()
		reader.SetContext(ctx)
		if isFileStorage {
			reader.SetReadahead(p.cfg.FileStorageReadahead)
		}
		return reader, func() { _ = reader.Close() }
	}

	// Try to reuse an idle reader for the same torrent file.
	for key, sr := range p.readers {
		if key.hash == ih && key.filePath == file.Path() && !sr.active {
			sr.active = true
			sr.idleSince = time.Time{}
			sr.isFileStorage = isFileStorage

			sr.reader.SetContext(ctx)
			rah := p.computeReadahead(totalReadaheadPool)
			if isFileStorage {
				rah = p.cfg.FileStorageReadahead
			}
			sr.reader.SetReadahead(rah)
			sr.rah = rah

			// Reset lastOffset so RefreshReadahead/ReaderPositions don't
			// read a stale offset from a previous activation.
			_, _ = sr.reader.Seek(0, io.SeekStart)
			sr.lastOffset = 0
			sr.wrapper = &readAtWrapper{
				reader: sr.reader,
				offset: 0,
				onOffsetChange: func(newOffset int64) {
					p.updateActiveRange(ih, key, file, newOffset)
				},
			}

			// byteOffset is 0 here; the real position will be set by the
			// first onOffsetChange callback after the caller issues a ReadAt.
			p.registerRangeLocked(ih, key, file, rah, 0)

			p.logger.Debug("reused idle reader",
				slog.String("hash", ih.HexString()),
				slog.String("file", file.Path()),
				slog.Uint64("readerID", sr.readerID),
				slog.Int64("readahead", rah))

			var once sync.Once
			release := func() {
				once.Do(func() { p.release(ih, file.Path(), sr.readerID) })
			}

			sectionReader := io.NewSectionReader(sr.wrapper, 0, file.Length())
			return sectionReader, release
		}
	}

	// No idle reader found.
	p.nextID++
	readerID := p.nextID
	key := readerKey{hash: ih, filePath: file.Path(), readerID: readerID}

	reader := file.NewReader()
	reader.SetContext(ctx)
	rah := p.computeReadahead(totalReadaheadPool)
	if isFileStorage {
		rah = p.cfg.FileStorageReadahead
	}
	reader.SetReadahead(rah)

	wrapper := &readAtWrapper{
		reader: reader,
		onOffsetChange: func(newOffset int64) {
			p.updateActiveRange(ih, key, file, newOffset)
		},
	}

	sr := &streamReader{
		active:        true,
		file:          file,
		hash:          ih,
		isFileStorage: isFileStorage,
		rah:           rah,
		reader:        reader,
		readerID:      readerID,
		wrapper:       wrapper,
	}
	p.readers[key] = sr

	// byteOffset is 0 because the reader was just created and re-seeked.
	p.registerRangeLocked(ih, key, file, rah, 0)

	p.logger.Debug("created new reader",
		slog.String("hash", ih.HexString()),
		slog.String("file", file.Path()),
		slog.Uint64("readerID", readerID),
		slog.Int64("readahead", rah))

	var once sync.Once
	release := func() {
		once.Do(func() { p.release(ih, file.Path(), readerID) })
	}

	sectionReader := io.NewSectionReader(wrapper, 0, file.Length())
	return sectionReader, release
}

// release marks a reader as idle and clears active ranges.
// Called immediately after the HTTP request ends (via defer in streamFile).
func (p *Pool) release(ih metainfo.Hash, filePath string, readerID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := readerKey{hash: ih, filePath: filePath, readerID: readerID}
	sr, ok := p.readers[key]
	if !ok || !sr.active {
		return
	}

	if p.cfg.Registry != nil {
		p.cfg.Registry.ClearActiveRange(sr.hash, sr.readerID)
	}

	sr.active = false
	sr.idleSince = time.Now()

	p.logger.Debug("reader released to idle",
		slog.String("hash", ih.HexString()),
		slog.String("file", filePath),
		slog.Uint64("readerID", readerID))
}

// RefreshReadahead recalculates and applies readahead for all active readers.
// File-storage readers keep their fixed readahead and are never divided.
func (p *Pool) RefreshReadahead(totalReadaheadPool int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rah := p.computeReadahead(totalReadaheadPool)

	for key, sr := range p.readers {
		if sr.active {
			// File-storage readers use the fixed value; do not overwrite.
			if sr.isFileStorage {
				continue
			}
			sr.rah = rah
			if sr.reader != nil {
				sr.reader.SetReadahead(rah)
			}
			p.registerRangeLocked(sr.hash, key, sr.file, rah, sr.lastOffset)
		}
	}

	p.logger.Debug("refreshed readahead",
		slog.Int64("totalPool", totalReadaheadPool),
		slog.Int64("perReader", rah))
}

// computeReadahead divides the total pool by the number of active memory
// readers. File-storage readers are excluded because they use a fixed
// readahead and never compete for the pool budget.
// Must be called with p.mu held.
func (p *Pool) computeReadahead(totalPool int64) int64 {
	activeCount := 0
	for _, sr := range p.readers {
		if sr.active && !sr.isFileStorage {
			activeCount++
		}
	}
	if activeCount == 0 {
		return totalPool
	}
	rah := totalPool / int64(activeCount)
	if rah < 1 {
		rah = 1
	}
	return rah
}

// computeRange returns the (start, end) piece indices for a given file position,
// readahead, and read offset. All callers share the same clamping logic.
func computeRange(file *torrent.File, pieceLength, rah, byteOffset int64) (start, end int64) {
	if pieceLength <= 0 {
		pieceLength = 1
	}
	rahPieces := rah / pieceLength
	if rahPieces < 1 {
		rahPieces = 1
	}
	beginPiece := int64(file.BeginPieceIndex())
	endPieceMax := int64(file.EndPieceIndex())

	positionPiece := byteOffset/pieceLength + beginPiece
	trailing := rahPieces / 4
	if trailing < 1 {
		trailing = 1
	}
	start = positionPiece - trailing
	end = positionPiece + rahPieces
	if start < beginPiece {
		start = beginPiece
	}
	if end > endPieceMax {
		end = endPieceMax
	}
	return start, end
}

// registerRangeLocked computes and registers the eviction-protection window
// for a reader at its current position with the given readahead.
// byteOffset is passed explicitly to avoid acquiring wrapper.mu under pool.mu
// (lock ordering: never take wrapper.mu under pool.mu).
// Must be called with p.mu held.
func (p *Pool) registerRangeLocked(ih metainfo.Hash, key readerKey, file *torrent.File, rah, byteOffset int64) {
	if p.cfg.Registry == nil {
		return
	}
	pieceLength := int64(1)
	if file.Torrent().Info() != nil {
		pieceLength = file.Torrent().Info().PieceLength
	}
	start, end := computeRange(file, pieceLength, rah, byteOffset)
	p.cfg.Registry.SetActiveRange(ih, key.readerID, start, end)
}

// findReaderLocked returns the streamReader for the given key, or nil.
// Must be called with p.mu held.
func (p *Pool) findReaderLocked(key readerKey) *streamReader {
	return p.readers[key]
}

// updateActiveRange recalculates and refreshes the eviction-protection
// window for a reader based on its new read offset. Called asynchronously
// from readAtWrapper when the position moves. Takes pool.mu to protect
// against concurrent release / Close / parkIdleReaders.
func (p *Pool) updateActiveRange(ih metainfo.Hash, key readerKey, file *torrent.File, newOffset int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cfg.Registry == nil {
		return
	}

	// If the reader was already released or deleted, skip the update.
	sr := p.findReaderLocked(key)
	if sr == nil || !sr.active {
		return
	}

	// Cache the offset on the streamReader so other pool.mu holders
	// (RefreshReadahead, ReaderPositions) can read it without touching
	// wrapper.mu — preserving the lock order.
	sr.lastOffset = newOffset

	pieceLength := int64(1)
	if file.Torrent().Info() != nil {
		pieceLength = file.Torrent().Info().PieceLength
	}

	start, end := computeRange(file, pieceLength, sr.rah, newOffset)
	p.cfg.Registry.SetActiveRange(ih, key.readerID, start, end)
}

// ReaderPositions returns metadata about all readers (active and idle) for the given torrent.
func (p *Pool) ReaderPositions(ih metainfo.Hash) []ReaderInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	var result []ReaderInfo
	for key, sr := range p.readers {
		if key.hash == ih {
			info := sr.file.Torrent().Info()
			if info == nil {
				continue
			}
			byteOffset := sr.lastOffset
			pieceLength := info.PieceLength
			if pieceLength <= 0 {
				pieceLength = 1
			}
			start, end := computeRange(sr.file, pieceLength, sr.rah, byteOffset)
			position := int(byteOffset/pieceLength) + sr.file.BeginPieceIndex()
			if position < sr.file.BeginPieceIndex() {
				position = sr.file.BeginPieceIndex()
			}
			if position > sr.file.EndPieceIndex() {
				position = sr.file.EndPieceIndex()
			}
			result = append(result, ReaderInfo{
				End:      int(end),
				Position: position,
				Start:    int(start),
			})
		}
	}
	return result
}

// Close shuts down the pool, closing all readers and clearing ranges.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}
	p.closed = true
	close(p.closeCh)

	for key, sr := range p.readers {
		if sr.reader != nil {
			_ = sr.reader.Close()
			sr.reader = nil
		}
		if p.cfg.Registry != nil {
			p.cfg.Registry.ClearActiveRange(sr.hash, sr.readerID)
		}
		delete(p.readers, key)
	}

	p.logger.Debug("stream pool closed")
}

// idleGC periodically parks readers that have been idle for longer than
// the configured IdleTimeout.
func (p *Pool) idleGC() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.closeCh:
			return
		case <-ticker.C:
			p.parkIdleReaders()
		}
	}
}

// idleTimeout returns the effective idle timeout for the given memory usage.
// usage < 0 means "no pressure func configured" and returns IdleTimeout.
func (p *Pool) idleTimeout(usage float64) time.Duration {
	switch {
	case usage < 0:
		return p.cfg.IdleTimeout
	case usage >= 0.90:
		return 1 * time.Second
	case usage >= 0.75:
		return 5 * time.Second
	case usage >= 0.50:
		return 10 * time.Second
	default:
		return p.cfg.IdleTimeout
	}
}

// sampleMemoryPressure returns the current memory usage ratio, calling
// MemoryPressureFunc at most once. Returns -1 if no function is set.
func (p *Pool) sampleMemoryPressure() float64 {
	if p.cfg.MemoryPressureFunc == nil {
		return -1
	}
	return p.cfg.MemoryPressureFunc()
}

// parkIdleReaders sets readahead to 0 for readers that have been idle
// longer than the effective timeout, and closes readers that have been
// parked longer than MaxIdleTime. Under memory pressure the close timeout
// is also shortened proportionally.
//
// It collects the actions to take first, then applies them while still
// holding the lock, to keep the critical section minimal.
func (p *Pool) parkIdleReaders() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Sample memory pressure once per tick for consistent policy decisions.
	usage := p.sampleMemoryPressure()

	timeout := p.idleTimeout(usage)
	now := time.Now()

	type closeAction struct {
		key  readerKey
		sr   *streamReader
		idle time.Duration
	}
	var toClose []closeAction
	var toPark []*streamReader

	for key, sr := range p.readers {
		if sr.active || sr.idleSince.IsZero() {
			continue
		}
		idle := now.Sub(sr.idleSince)

		// Under memory pressure, shrink the close deadline to reclaim
		// memory faster. MaxIdleTime == 0 still means "never close" when
		// there is no pressure; the pressure path only activates when a
		// deadline is already set (MaxIdleTime > 0).
		closeDeadline := p.cfg.MaxIdleTime
		if p.cfg.MaxIdleTime > 0 && usage >= 0 {
			if usage >= 0.90 {
				closeDeadline = 30 * time.Second
			} else if usage >= 0.75 {
				closeDeadline = 60 * time.Second
			}
		}

		if closeDeadline > 0 && idle >= closeDeadline {
			toClose = append(toClose, closeAction{key: key, sr: sr, idle: idle})
			continue
		}

		if sr.rah > 0 && idle >= timeout {
			toPark = append(toPark, sr)
		}
	}

	// Apply close actions.
	for _, c := range toClose {
		if c.sr.reader != nil {
			_ = c.sr.reader.Close()
			c.sr.reader = nil
		}
		if p.cfg.Registry != nil {
			p.cfg.Registry.ClearActiveRange(c.sr.hash, c.sr.readerID)
		}
		delete(p.readers, c.key)
		p.logger.Debug("closed idle reader",
			slog.String("hash", c.sr.hash.HexString()),
			slog.Uint64("readerID", c.sr.readerID),
			slog.Duration("idle", c.idle))
	}

	for _, sr := range toPark {
		sr.rah = 0
		if sr.reader != nil {
			sr.reader.SetReadahead(0)
		}
		p.logger.Debug("parked idle reader",
			slog.String("hash", sr.hash.HexString()),
			slog.Uint64("readerID", sr.readerID),
			slog.Duration("idleTimeout", timeout))
	}
}
