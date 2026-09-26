// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
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
	// window are protected from eviction on a best-effort basis; an
	// implementation may still evict them under severe memory pressure
	// rather than fail an incoming write outright.
	SetActiveRange(infoHash metainfo.Hash, readerID uint64, start, end int)

	// ClearActiveRange removes the active range for a specific reader.
	ClearActiveRange(infoHash metainfo.Hash, readerID uint64)

	// SetFileBoundaries registers or updates the head and tail piece-index ranges
	// for a file being streamed, protecting container metadata from standard LRU eviction.
	SetFileBoundaries(infoHash metainfo.Hash, readerID uint64, headStart, headEnd, tailStart, tailEnd int)

	// ClearFileBoundaries removes the protected file boundaries for a specific reader.
	ClearFileBoundaries(infoHash metainfo.Hash, readerID uint64)
}

// ReaderPosition describes a reader's piece-index position and readahead window.
// Start and End are inclusive absolute piece indices within the torrent.
type ReaderPosition struct {
	// End is the inclusive end of the reader's readahead window.
	End int
	// Position is the reader's current piece index, or its last position while lingering.
	Position int
	// Start is the inclusive start of the reader's trailing window.
	Start int
}

// StorageMode selects the storage behavior used by an acquired reader.
type StorageMode uint8

const (
	// MemoryStorage shares the pool's global readahead budget with other
	// memory-storage readers.
	MemoryStorage StorageMode = iota
	// FileStorage uses Config.FileReadaheadBytes instead of the shared budget.
	FileStorage
)

// String returns "memory" or "file", or "unknown" for an invalid mode.
func (m StorageMode) String() string {
	switch m {
	case MemoryStorage:
		return "memory"
	case FileStorage:
		return "file"
	default:
		return "unknown"
	}
}

// ReleaseFunc returns an acquired reader to the pool. It is safe to call more
// than once.
type ReleaseFunc func()

// ErrPoolClosed is returned when a reader is acquired from a closed pool.
var ErrPoolClosed = errors.New("stream pool is closed")

// ErrInvalidFile is returned when Acquire receives a nil or detached file.
var ErrInvalidFile = errors.New("invalid torrent file")

// ErrInvalidStorageMode is returned when Acquire receives an unknown mode.
var ErrInvalidStorageMode = errors.New("invalid storage mode")

// Config configures the stream pool.
type Config struct {
	// FileReadaheadBytes is the fixed readahead in bytes for file-storage readers.
	// Zero defaults to 50 MiB.
	FileReadaheadBytes int64
	// Logger receives pool lifecycle and diagnostic messages. Nil uses slog.Default.
	Logger *slog.Logger
	// LingerTimeout is how long a released playback reader stays open, still
	// reading ahead, while no other work is active. Zero defaults to 30
	// seconds. Memory pressure may shorten it.
	LingerTimeout time.Duration
	// MemoryUsage returns the current memory usage ratio (0.0–1.0).
	// When set, lingering readers close sooner under memory pressure to
	// prevent pieces from being downloaded and immediately evicted.
	// When nil, a fixed LingerTimeout is used.
	MemoryUsage func() float64
	// PriorityWindowFraction, when > 0, raises the pieces just ahead of a
	// playback reader to PiecePriorityNow. The fraction applies to the
	// readahead window size in pieces, and at least one piece is claimed. The
	// torrent client already gives the whole readahead window
	// PiecePriorityReadahead but orders those pieces by rarity, so the claim
	// makes the nearest pieces download first. Values above 1 are clamped to 1.
	// A non-positive value disables prioritization.
	PriorityWindowFraction float64
	// ReadObserver, when set, is called after every Read of a playback reader
	// with the reader's storage mode and how long the Read took, including
	// any wait for torrent data. Preload reads are not reported. It runs on
	// the reading goroutine, so it must return quickly.
	ReadObserver func(mode StorageMode, duration time.Duration)
	// Registry tracks active read ranges for piece eviction protection.
	// Nil disables active-range tracking.
	Registry ActiveRangeRegistry
}

// readerKey uniquely identifies a stream reader within the pool.
// readerID makes it unique even when multiple readers exist for the same torrent/file.
type readerKey struct {
	infoHash metainfo.Hash
	filePath string
	readerID uint64
}

type prioritizedPiece struct {
	index    int
	priority torrent.PiecePriority
}

type priorityPieceKey struct {
	index   int
	torrent *torrent.Torrent
}

type priorityClaim struct {
	owners map[*streamReader]torrent.PiecePriority
}

const defaultWrapperBufSize = 256 * 1024

// readAtWrapper adapts a torrent.Reader (io.ReadSeekCloser) to io.ReaderAt.
// It seeks to the requested offset and leaves the dedicated underlying reader
// at the end of the read so its torrent readahead window remains active.
//
// Lock ordering: wrapper.mu is never held while acquiring pool.mu. Pool
// lifecycle paths may hold pool.mu while acquiring wrapper.mu to replace or
// close a wrapper. This is safe because ReadAt and notifyOffsetChange always
// release wrapper.mu before invoking the pool callback.
type readAtWrapper struct {
	closed bool
	mu     sync.Mutex
	reader io.ReadSeekCloser
	offset int64 // tracks the read position in bytes

	cacheBuf   []byte
	cacheStart int64 // start byte offset of cached window
	cacheLen   int   // valid byte count in cacheBuf
	// fillLimit, when positive, is the exclusive byte offset past which cache
	// refills do not read ahead. Bounded readers such as preloads set it so a
	// refill never blocks on, or downloads, data outside their range.
	fillLimit int64

	// onOffsetChange is called when the read position moves, allowing the
	// caller to update the active eviction-protection range. The parameter
	// is the new byte offset. May be nil. Must be called WITHOUT rw.mu held.
	onOffsetChange func(newOffset int64)
}

// retire detaches a released wrapper from its torrent reader, so a stale
// caller can neither read nor move a lingering reader, and frees its buffer.
// The pool closes the torrent reader itself. It is safe to call more than
// once.
func (rw *readAtWrapper) retire() {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.closed = true
	rw.onOffsetChange = nil
	rw.cacheBuf = nil
	rw.cacheLen = 0
	rw.cacheStart = 0
	rw.offset = 0
}

// notifyOffsetChange invokes the current callback without holding rw.mu. Seek
// notifications use this path so the pool can protect the destination before
// the next potentially blocking ReadAt cache refill.
func (rw *readAtWrapper) notifyOffsetChange(newOffset int64) {
	rw.mu.Lock()
	if rw.closed {
		rw.mu.Unlock()
		return
	}
	cb := rw.onOffsetChange
	rw.mu.Unlock()
	if cb != nil {
		cb(newOffset)
	}
}

// ReadAt seeks to the requested offset, reads data, and notifies the pool of the
// new offset. It utilizes an internal read buffer to satisfy small sequential reads
// without seeking or invoking the underlying reader repeatedly. The callback fires
// WITHOUT the wrapper lock held to maintain a consistent lock order.
// Note: ReadAt serializes concurrent I/O per wrapper under rw.mu to protect the
// underlying reader's shared Seek and Read positions.
func (rw *readAtWrapper) ReadAt(p []byte, off int64) (int, error) {
	rw.mu.Lock()
	if rw.closed {
		rw.mu.Unlock()
		return 0, io.ErrClosedPipe
	}

	// 1. Try to fulfill the read request from the internal buffer.
	if rw.cacheLen > 0 && off >= rw.cacheStart && off < rw.cacheStart+int64(rw.cacheLen) {
		rel := off - rw.cacheStart
		n := copy(p, rw.cacheBuf[rel:rw.cacheLen])
		rw.offset = off + int64(n)
		offset := rw.offset
		cb := rw.onOffsetChange
		rw.mu.Unlock()

		if cb != nil {
			cb(offset)
		}
		if n < len(p) {
			nn, err := rw.ReadAt(p[n:], off+int64(n))
			return n + nn, err
		}
		return n, nil
	}

	// 2. Buffer miss: check current position, seek if necessary, and refill buffer.
	pos, err := rw.reader.Seek(0, io.SeekCurrent)
	if err != nil {
		rw.mu.Unlock()
		return 0, err
	}

	if pos != off {
		if _, err := rw.reader.Seek(off, io.SeekStart); err != nil {
			_, _ = rw.reader.Seek(pos, io.SeekStart)
			rw.mu.Unlock()
			return 0, err
		}
	}

	// If the requested read is large enough, bypass cacheBuf to avoid
	// redundant buffer allocations and memory copies.
	if len(p) >= defaultWrapperBufSize {
		rw.cacheLen = 0
		readBytes := 0
		var readErr error
		for readBytes < len(p) {
			nn, e := rw.reader.Read(p[readBytes:])
			readBytes += nn
			if e != nil {
				readErr = e
				break
			}
		}
		rw.offset = off + int64(readBytes)
		offset := rw.offset
		cb := rw.onOffsetChange
		rw.mu.Unlock()

		if cb != nil {
			cb(offset)
		}
		if readBytes < len(p) && readErr != nil {
			return readBytes, readErr
		}
		return readBytes, nil
	}

	if rw.cacheBuf == nil {
		rw.cacheBuf = make([]byte, defaultWrapperBufSize)
	}

	fillLen := int64(len(rw.cacheBuf))
	if rw.fillLimit > 0 {
		// Always satisfy the caller's request, but do not read ahead past the limit.
		fillLen = min(fillLen, max(rw.fillLimit-off, int64(len(p))))
	}
	fill := rw.cacheBuf[:fillLen]
	readBytes := 0
	var readErr error
	for readBytes < len(fill) {
		nn, e := rw.reader.Read(fill[readBytes:])
		readBytes += nn
		if e != nil {
			readErr = e
			break
		}
	}

	rw.cacheStart = off
	rw.cacheLen = readBytes

	if readBytes == 0 && readErr != nil {
		rw.mu.Unlock()
		return 0, readErr
	}

	n := copy(p, rw.cacheBuf[:rw.cacheLen])
	rw.offset = off + int64(n)
	offset := rw.offset
	cb := rw.onOffsetChange
	rw.mu.Unlock()

	if cb != nil {
		cb(offset)
	}

	if n < len(p) {
		if readErr != nil {
			return n, readErr
		}
		nn, err := rw.ReadAt(p[n:], off+int64(n))
		return n + nn, err
	}
	return n, nil
}

// seekNotifyingReader reports the destination of a seek when the next read
// starts, before that read can block on torrent data. Deferring the report
// skips positions that are never read, such as the end-of-file seek that
// http.ServeContent uses to learn the content size. io.SectionReader still
// provides the bounded file view. Like other io.ReadSeekers, it is not safe
// for concurrent use.
type seekNotifyingReader struct {
	io.ReadSeeker
	// observeRead, when set, receives the duration of each Read.
	observeRead func(time.Duration)
	onSeek      func(int64)
	seekPending bool
	seekTarget  int64
}

func (r *seekNotifyingReader) Read(p []byte) (int, error) {
	if r.observeRead != nil {
		start := time.Now()
		defer func() { r.observeRead(time.Since(start)) }()
	}
	if r.seekPending {
		r.seekPending = false
		if r.onSeek != nil {
			r.onSeek(r.seekTarget)
		}
	}
	return r.ReadSeeker.Read(p)
}

func (r *seekNotifyingReader) Seek(offset int64, whence int) (int64, error) {
	position, err := r.ReadSeeker.Seek(offset, whence)
	if err == nil {
		r.seekPending = true
		r.seekTarget = position
	}
	return position, err
}

// streamReader wraps a torrent.Reader with lifecycle management.
type streamReader struct {
	active        bool
	cancel        context.CancelFunc
	file          *torrent.File
	infoHash      metainfo.Hash
	isFileStorage bool
	isPreload     bool
	// lingerSince is when a released reader started lingering.
	lingerSince time.Time
	preloadEnd  int64
	// lastOffset is the last byte offset reported by the onOffsetChange
	// callback. Updated under pool.mu only, so code holding pool.mu can
	// read it without acquiring wrapper.mu.
	lastOffset   int64
	lastPieceIdx int64
	// prioritizedPieces tracks the piece claims currently owned by this reader.
	// Pool-level ownership keeps overlapping readers from lowering each other's
	// priorities when one moves or releases.
	prioritizedPieces []int
	// priorityMu guards prioritizedPieces so that concurrent offset
	// updates from different goroutines serialize their piece-priority
	// writes without contending on pool.mu.
	priorityMu sync.Mutex
	// prioritySeq is monotonically incremented on each priority change or reader release.
	// Out-of-order workers safely bail by comparing their captured seq against the live
	// counter (sr.prioritySeq.Load() != seq) under priorityMu, rather than tracking a
	// high-water mark — any newer dispatch invalidates all older ones.
	// Atomic — no mutex needed for this field.
	prioritySeq atomic.Uint64
	readahead   int64 // current readahead in bytes (updated by refreshReadaheadLocked and Acquire)
	reader      torrent.Reader
	readerID    uint64
	wrapper     *readAtWrapper
}

// Pool manages torrent readers with dynamic readahead management.
// Callers must call Close() when the pool is no longer needed to stop the background
// lingering-reader goroutine and release reader resources.
type Pool struct {
	closeCh         chan struct{}
	closed          bool
	cfg             Config
	logger          *slog.Logger
	mu              sync.Mutex
	nextID          uint64
	priorityClaims  map[priorityPieceKey]*priorityClaim
	priorityMu      sync.Mutex
	preloadBudgets  map[metainfo.Hash]preloadReservation
	readaheadBudget int64 // current total readahead budget, updated by SetReadaheadBudget
	readers         map[readerKey]*streamReader
}

// New creates a new stream pool and starts a background goroutine that closes
// expired lingering readers.
// Callers must call Close() when the pool is no longer needed to terminate
// the background goroutine and avoid resource leakage.
func New(cfg Config) *Pool {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.FileReadaheadBytes == 0 {
		cfg.FileReadaheadBytes = 50 * 1024 * 1024
	}
	if cfg.LingerTimeout == 0 {
		cfg.LingerTimeout = 30 * time.Second
	}

	p := &Pool{
		closeCh:        make(chan struct{}),
		cfg:            cfg,
		logger:         logger,
		priorityClaims: make(map[priorityPieceKey]*priorityClaim),
		preloadBudgets: make(map[metainfo.Hash]preloadReservation),
		readers:        make(map[readerKey]*streamReader),
	}

	go p.idleGC()

	return p
}

// Acquire returns an io.ReadSeeker for reading the given file within a torrent.
// It is equivalent to AcquireContext with context.Background().
func (p *Pool) Acquire(file *torrent.File, mode StorageMode) (io.ReadSeeker, ReleaseFunc, error) {
	return p.AcquireContext(context.Background(), file, mode)
}

// AcquireContext returns an io.ReadSeeker for reading the given file within a torrent
// with cancellation tied to ctx. The caller MUST call the returned release function
// (typically via defer) when done reading. The release function is safe to call
// multiple times. MemoryStorage readers share the budget configured by SetReadaheadBudget;
// FileStorage readers use Config.FileReadaheadBytes. AcquireContext returns an error
// for an invalid file or mode, or after the pool has been closed.
func (p *Pool) AcquireContext(ctx context.Context, file *torrent.File, mode StorageMode) (io.ReadSeeker, ReleaseFunc, error) {
	return p.acquireContext(ctx, file, mode, false, 0, 0)
}

// AcquirePreloadContext acquires a reader with dynamic readahead bounded to
// [start, end). The torrent client prioritizes that readahead window, and the
// preload controller owns eviction protection for the range.
func (p *Pool) AcquirePreloadContext(ctx context.Context, file *torrent.File, mode StorageMode, start, end int64) (io.ReadSeeker, ReleaseFunc, error) {
	if file != nil {
		start = min(max(start, 0), file.Length())
		end = min(max(end, start), file.Length())
	}
	return p.acquireContext(ctx, file, mode, true, start, end)
}

func (p *Pool) acquireContext(ctx context.Context, file *torrent.File, mode StorageMode, isPreload bool, preloadStart, preloadEnd int64) (io.ReadSeeker, ReleaseFunc, error) {
	if file == nil || file.Torrent() == nil {
		return nil, nil, ErrInvalidFile
	}
	if mode != MemoryStorage && mode != FileStorage {
		return nil, nil, fmt.Errorf("%w: %d", ErrInvalidStorageMode, mode)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	infoHash := file.Torrent().InfoHash()
	isFileStorage := mode == FileStorage

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, nil, ErrPoolClosed
	}

	p.nextID++
	readerID := p.nextID
	key := readerKey{infoHash: infoHash, filePath: file.Path(), readerID: readerID}

	readerCtx, cancel := context.WithCancel(ctx)
	reader := file.NewReader()
	reader.SetContext(readerCtx)

	wrapper := p.newReaderWrapper(reader, key, file)
	if isPreload {
		wrapper.fillLimit = preloadEnd
	}

	sr := &streamReader{
		active:        true,
		cancel:        cancel,
		file:          file,
		infoHash:      infoHash,
		isFileStorage: isFileStorage,
		isPreload:     isPreload,
		preloadEnd:    preloadEnd,
		lastPieceIdx:  -1,
		reader:        reader,
		readerID:      readerID,
		wrapper:       wrapper,
	}
	p.readers[key] = sr

	switch {
	case isPreload:
		sr.readahead = preloadEnd - preloadStart
		reader.SetReadaheadFunc(preloadReadaheadFunc(preloadEnd))
		p.closeLingeringReadersLocked()
	case isFileStorage:
		sr.readahead = p.cfg.FileReadaheadBytes
		reader.SetReadahead(p.cfg.FileReadaheadBytes)
		p.registerActiveRangeLocked(infoHash, key, file, p.cfg.FileReadaheadBytes, 0, DefaultFileBoundaryBytes)
		p.closeLingeringReadersLocked()
	default:
		p.refreshReadaheadLocked(p.readaheadBudget)
	}

	p.logger.Debug("created new reader",
		slog.String("hash", infoHash.HexString()),
		slog.String("file", file.Path()),
		slog.Uint64("readerID", readerID),
		slog.Int64("readahead", sr.readahead))

	return p.newReadSeeker(wrapper, file.Length(), mode, isPreload), p.releaseFunc(key), nil
}

// newReaderWrapper returns a wrapper for reader whose position changes update
// the eviction-protection range and piece priorities of the reader at key.
func (p *Pool) newReaderWrapper(reader io.ReadSeekCloser, key readerKey, file *torrent.File) *readAtWrapper {
	wrapper := &readAtWrapper{reader: reader}
	wrapper.onOffsetChange = func(newOffset int64) {
		p.updateActiveRange(key.infoHash, key, file, newOffset)
	}
	return wrapper
}

// releaseFunc returns an idempotent ReleaseFunc for the reader at key.
func (p *Pool) releaseFunc(key readerKey) ReleaseFunc {
	var once sync.Once
	return func() {
		once.Do(func() { p.release(key.infoHash, key.filePath, key.readerID) })
	}
}

// newReadSeeker returns the bounded view of wrapper handed to callers. Reads
// of playback readers are timed for Config.ReadObserver.
func (p *Pool) newReadSeeker(wrapper *readAtWrapper, length int64, mode StorageMode, isPreload bool) io.ReadSeeker {
	r := &seekNotifyingReader{ReadSeeker: io.NewSectionReader(wrapper, 0, length), onSeek: wrapper.notifyOffsetChange}
	if observe := p.cfg.ReadObserver; observe != nil && !isPreload {
		r.observeRead = func(d time.Duration) { observe(mode, d) }
	}
	return r
}

func preloadReadaheadFunc(end int64) torrent.ReadaheadFunc {
	return func(ctx torrent.ReadaheadContext) int64 {
		return max(end-ctx.CurrentPos, 0)
	}
}

// closeStreamReaderLocked cancels and closes a reader. Retiring the wrapper
// first serializes with any in-flight ReadAt operation. The caller must hold
// Pool.mu so no new pool-owned use can begin while closure is in progress.
func closeStreamReaderLocked(sr *streamReader) {
	if sr.cancel != nil {
		sr.cancel()
		sr.cancel = nil
	}
	if sr.wrapper != nil {
		sr.wrapper.retire()
	}
	if sr.reader != nil {
		_ = sr.reader.Close()
	}
	sr.reader = nil
}

// release ends a reader's lease. Called immediately after the HTTP request
// ends (via defer in streamFile).
//
// A released playback reader lingers: it stays open with its readahead, so
// the torrent client keeps fetching the pieces just past where the player
// stopped for the player's next range request. It lingers only while no other
// playback or preload reader is active, so it cannot download outside the
// shared budget, and at most LingerTimeout. The next request creates a new
// reader, which finds those pieces cached. Preload readers and readers of a
// closed torrent close immediately.
//
// A lingering reader holds no eviction protection or priority claims, so its
// prefetched pieces compete with other cached pieces under memory pressure.
func (p *Pool) release(infoHash metainfo.Hash, filePath string, readerID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := readerKey{infoHash: infoHash, filePath: filePath, readerID: readerID}
	sr, ok := p.readers[key]
	if !ok || !sr.active {
		return
	}

	sr.active = false
	if sr.isPreload || torrentClosed(sr.file) {
		p.removeReaderLocked(key, sr)
		p.rebalanceLocked()
		return
	}

	p.clearReaderClaimsLocked(sr)
	// The caller is done with its reader, so a stale call cannot move the
	// lingering reader's position.
	if sr.wrapper != nil {
		sr.wrapper.retire()
	}
	sr.lingerSince = time.Now()
	p.logger.Debug("reader lingering",
		slog.String("hash", infoHash.HexString()),
		slog.String("file", filePath),
		slog.Uint64("readerID", readerID))

	// Closes this reader too when other work is active.
	p.rebalanceLocked()
}

// torrentClosed reports whether file's torrent has been dropped.
func torrentClosed(file *torrent.File) bool {
	if file == nil || file.Torrent() == nil {
		return false
	}
	select {
	case <-file.Torrent().Closed():
		return true
	default:
		return false
	}
}

// rebalanceLocked redistributes the readahead budget after a reader stops
// being active, or only closes lingering readers while no budget is set.
// Must be called with p.mu held.
func (p *Pool) rebalanceLocked() {
	if p.readaheadBudget > 0 {
		p.refreshReadaheadLocked(p.readaheadBudget)
	} else {
		p.closeLingeringReadersLocked()
	}
}

// clearReaderClaimsLocked releases every piece priority and eviction
// protection a reader holds. Bumping prioritySeq invalidates in-flight
// prioritizeAsync goroutines, and priorityMu serializes with one that is
// already applying priorities. Must be called with p.mu held.
func (p *Pool) clearReaderClaimsLocked(sr *streamReader) {
	sr.prioritySeq.Add(1)
	sr.priorityMu.Lock()
	p.clearReaderPrioritiesLocked(sr)
	sr.priorityMu.Unlock()
	if p.cfg.Registry != nil {
		p.cfg.Registry.ClearActiveRange(sr.infoHash, sr.readerID)
		p.cfg.Registry.ClearFileBoundaries(sr.infoHash, sr.readerID)
	}
}

// removeReaderLocked releases a reader's claims, closes it, and removes it
// from the pool. Must be called with p.mu held.
func (p *Pool) removeReaderLocked(key readerKey, sr *streamReader) {
	p.clearReaderClaimsLocked(sr)
	closeStreamReaderLocked(sr)
	delete(p.readers, key)
}

// refreshReadaheadLocked recalculates and applies readahead for all active readers.
// File-storage readers keep their fixed readahead and are never divided.
// Must be called with p.mu held.
func (p *Pool) refreshReadaheadLocked(totalReadaheadBudget int64) {
	plan := p.planReadaheadLocked(totalReadaheadBudget)

	for key, sr := range p.readers {
		if !sr.active {
			continue
		}
		// File-storage readers use the fixed value; do not overwrite.
		if sr.isFileStorage {
			continue
		}
		// Preload readers have a dynamic, range-bounded readahead backed by
		// an explicit reservation. Do not redistribute their budget here.
		if sr.isPreload {
			continue
		}
		readahead := readaheadForShare(plan.share, filePieceLength(sr.file))
		sr.readahead = readahead
		if sr.reader != nil {
			sr.reader.SetReadahead(readahead)
		}
		var boundaryBytes int64
		if sr.file != nil {
			boundaryBytes = plan.boundaryBytes[readerFileKey{infoHash: sr.infoHash, filePath: sr.file.Path()}]
		}
		p.registerActiveRangeLocked(sr.infoHash, key, sr.file, readahead, sr.lastOffset, boundaryBytes)
	}

	// Close lingering readers only after active readers have their windows,
	// so pieces both want keep a reader's priority throughout.
	p.closeLingeringReadersLocked()

	p.logger.Debug("refreshed readahead",
		slog.Int64("totalPool", totalReadaheadBudget),
		slog.Int64("perReaderShare", plan.share))
}

// closeLingeringReadersLocked closes every lingering reader whenever playback
// or preload work is active. A reader lingers only while it is the pool's sole
// work, so released HTTP range requests cannot accumulate unbudgeted
// downloads. A running preload is active work through its own readers; a
// preload reservation alone is not, because a completed preload that still
// holds one downloads nothing. Must be called with p.mu held.
func (p *Pool) closeLingeringReadersLocked() {
	hasActiveWork := false
	for _, sr := range p.readers {
		if sr.active {
			hasActiveWork = true
			break
		}
	}
	if !hasActiveWork {
		return
	}

	for key, sr := range p.readers {
		if sr.active {
			continue
		}
		p.removeReaderLocked(key, sr)
		p.logger.Debug("closed lingering reader for active work",
			slog.String("hash", sr.infoHash.HexString()),
			slog.Uint64("readerID", sr.readerID))
	}
}

// SetReadaheadBudget recalculates readahead for all active memory-storage
// readers using the provided total budget. Negative budgets are treated as
// zero. File-storage readers keep their fixed readahead. It returns false and
// leaves the previous budget unchanged if existing preload reservations would
// exceed the new preload share.
func (p *Pool) SetReadaheadBudget(budgetBytes int64) bool {
	if budgetBytes < 0 {
		budgetBytes = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	reserved := int64(0)
	for _, reservation := range p.preloadBudgets {
		reserved += reservation.bytes
	}
	if reserved > preloadProtectionCapacity(budgetBytes) {
		return false
	}
	p.readaheadBudget = budgetBytes
	p.refreshReadaheadLocked(budgetBytes)
	return true
}

// ReservePreload admits a preload of file into the same global protection
// budget used by streaming readahead, and protects the whole pieces holding the
// file-relative byte ranges [0, headEnd) and [tailStart, tailEnd) from eviction
// until ReleasePreload. An empty tail range protects only the head. Replacing a
// reservation for the same torrent is atomic. When the ranges reach both ends
// of the file, playback readers of that file skip their own boundary
// protection while the reservation is held instead of paying for those pieces
// twice. A preload that protects only the head, such as one trimmed to a small
// budget, leaves reader boundaries in place so the tail stays protected.
// Preloads may use otherwise-idle capacity but always leave a bounded playback
// reserve, so a newly acquired stream cannot be reduced to the one-byte
// minimum by completed preload leases. It returns false, and holds no
// reservation for the torrent, when the pieces do not fit the preload capacity
// left by other reservations or the file has no piece metadata.
func (p *Pool) ReservePreload(file *torrent.File, headEnd, tailStart, tailEnd int64) bool {
	headStart, headEndPiece, tailStartPiece, tailEndPiece, ok := FilePieceRanges(file, headEnd, tailStart, tailEnd)
	if !ok {
		return false
	}
	reservation := preloadReservation{
		bytes:            BoundaryPieceBytes(file.Torrent().Info(), headStart, headEndPiece, tailStartPiece, tailEndPiece),
		coversBoundaries: preloadCoversBoundaries(file.Length(), headEnd, tailStart, tailEnd),
		filePath:         file.Path(),
		headStart:        headStart,
		headEnd:          headEndPiece,
		tailStart:        tailStartPiece,
		tailEnd:          tailEndPiece,
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reservePreloadLocked(file.Torrent().InfoHash(), reservation)
}

// preloadCoversBoundaries reports whether the head range [0, headEnd) and the
// tail range [tailStart, tailEnd) reach both ends of a file of fileLength
// bytes. A preload trimmed to part of its head leaves the tail to playback
// boundary protection; one whose head spans the whole file covers the tail as
// well.
func preloadCoversBoundaries(fileLength, headEnd, tailStart, tailEnd int64) bool {
	return headEnd > 0 && (headEnd >= fileLength || (tailEnd > tailStart && tailEnd >= fileLength))
}

// reservePreloadLocked admits reservation for infoHash when it fits the
// preload capacity left by other torrents' reservations, and protects its
// pieces. A replaced reservation keeps its protection IDs, so its pieces stay
// protected throughout. Must be called with p.mu held.
func (p *Pool) reservePreloadLocked(infoHash metainfo.Hash, reservation preloadReservation) bool {
	reserved := int64(0)
	for hash, other := range p.preloadBudgets {
		if hash != infoHash {
			reserved += other.bytes
		}
	}
	if reservation.bytes > preloadProtectionCapacity(p.readaheadBudget)-reserved {
		p.releasePreloadLocked(infoHash)
		return false
	}
	if previous, ok := p.preloadBudgets[infoHash]; ok {
		reservation.headID, reservation.tailID = previous.headID, previous.tailID
	} else {
		p.nextID++
		reservation.headID = p.nextID
		p.nextID++
		reservation.tailID = p.nextID
	}
	p.preloadBudgets[infoHash] = reservation
	if p.cfg.Registry != nil {
		p.cfg.Registry.SetActiveRange(infoHash, reservation.headID, reservation.headStart, reservation.headEnd)
		p.cfg.Registry.SetActiveRange(infoHash, reservation.tailID, reservation.tailStart, reservation.tailEnd)
	}
	p.refreshReadaheadLocked(p.readaheadBudget)
	return true
}

// PreloadCapacity returns the total bytes that preload reservations may hold
// under the current readahead budget. The remainder of the budget is kept for
// playback readers.
func (p *Pool) PreloadCapacity() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return preloadProtectionCapacity(p.readaheadBudget)
}

// ReleasePreload removes a torrent's preload reservation, ends the eviction
// protection of its pieces, and restores the freed capacity to active stream
// readers.
func (p *Pool) ReleasePreload(infoHash metainfo.Hash) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releasePreloadLocked(infoHash)
}

// releasePreloadLocked removes a torrent's preload reservation and its piece
// protection, if it holds one. Must be called with p.mu held.
func (p *Pool) releasePreloadLocked(infoHash metainfo.Hash) {
	reservation, ok := p.preloadBudgets[infoHash]
	if !ok {
		return
	}
	delete(p.preloadBudgets, infoHash)
	p.clearPreloadProtectionLocked(infoHash, reservation)
	p.refreshReadaheadLocked(p.readaheadBudget)
}

// clearPreloadProtectionLocked ends the eviction protection of a reservation's
// pieces. Must be called with p.mu held.
func (p *Pool) clearPreloadProtectionLocked(infoHash metainfo.Hash, reservation preloadReservation) {
	if p.cfg.Registry != nil {
		p.cfg.Registry.ClearActiveRange(infoHash, reservation.headID)
		p.cfg.Registry.ClearActiveRange(infoHash, reservation.tailID)
	}
}

// readaheadPlan divides the protection budget among active memory-storage
// playback readers. Storage protects and evicts whole pieces, so the plan is
// sized in pieces rather than bytes.
type readaheadPlan struct {
	// boundaryBytes holds the head and tail size for each file that keeps
	// boundary protection. Files whose boundary pieces do not fit are absent.
	boundaryBytes map[readerFileKey]int64
	// share is each reader's protection budget in bytes after boundaries.
	share int64
}

// planReadaheadLocked divides the budget left after preload reservations. The
// budget is global, not per info hash. Boundary protection may use at most half
// of it; each file's boundaries shrink until their whole pieces fit, and are
// dropped when even one piece per boundary does not. A file whose held preload
// reservation covers both its head and tail gets no boundaries, because the
// preload already protects them within that reservation. The remainder is shared
// evenly by the readers. File-storage and preload readers are excluded because
// they have their own readahead. Must be called with p.mu held.
func (p *Pool) planReadaheadLocked(totalBudget int64) readaheadPlan {
	available := p.availableProtectionBudgetLocked(totalBudget)
	activeCount := 0
	activeFiles := make(map[readerFileKey]*torrent.File)
	for _, sr := range p.readers {
		if sr.active && !sr.isFileStorage && !sr.isPreload {
			activeCount++
			if sr.file != nil && !p.preloadCoversFileLocked(sr.infoHash, sr.file.Path()) {
				activeFiles[readerFileKey{infoHash: sr.infoHash, filePath: sr.file.Path()}] = sr.file
			}
		}
	}
	plan := readaheadPlan{
		boundaryBytes: make(map[readerFileKey]int64, len(activeFiles)),
		share:         available,
	}
	if activeCount < 1 {
		return plan
	}

	allowance := 2 * p.boundaryBytesPerFileLocked(available, len(activeFiles))
	var boundaryCost int64
	for key, file := range activeFiles {
		if boundaryBytes, cost, ok := fitFileBoundaries(file, allowance); ok {
			plan.boundaryBytes[key] = boundaryBytes
			boundaryCost += cost
		}
	}
	plan.share = max(available-boundaryCost, 0) / int64(activeCount)
	return plan
}

// preloadCoversFileLocked reports whether a held preload reservation protects
// both the given file's head and tail. Must be called with p.mu held.
func (p *Pool) preloadCoversFileLocked(infoHash metainfo.Hash, filePath string) bool {
	reservation, ok := p.preloadBudgets[infoHash]
	return ok && reservation.coversBoundaries && reservation.filePath == filePath
}

// fitFileBoundaries returns the largest per-boundary size, at most half of
// allowance, whose whole head and tail pieces fit within allowance, together
// with the bytes those pieces occupy. It returns false when a single piece per
// boundary does not fit. Files without piece metadata use a byte estimate.
func fitFileBoundaries(file *torrent.File, allowance int64) (boundaryBytes, cost int64, ok bool) {
	if allowance <= 0 || file == nil || file.Length() == 0 {
		return 0, 0, false
	}
	pieceLength := filePieceLength(file)
	if pieceLength <= 0 {
		return allowance / 2, min(file.Length(), allowance), true
	}
	info := file.Torrent().Info()
	for boundaryBytes = allowance / 2; ; boundaryBytes = max(boundaryBytes-pieceLength, pieceLength) {
		headStart, headEnd, tailStart, tailEnd, ok := computeFileBoundaries(file, boundaryBytes)
		if !ok {
			return 0, 0, false
		}
		if cost = BoundaryPieceBytes(info, headStart, headEnd, tailStart, tailEnd); cost <= allowance {
			return boundaryBytes, cost, true
		}
		if boundaryBytes <= pieceLength {
			return 0, 0, false
		}
	}
}

// readaheadForShare returns the readahead whose protected range fits within
// share bytes. computeRange protects the position piece, the readahead pieces
// ahead of it, and a trailing quarter of the readahead behind it, so the
// readahead is a whole number of pieces and may be zero when the share only
// covers the piece being read. Without piece metadata the share is split in
// bytes with a minimum of one.
func readaheadForShare(share, pieceLength int64) int64 {
	if pieceLength <= 0 {
		return max(share*trailingReadaheadDivisor/(trailingReadaheadDivisor+1), 1)
	}
	spare := share/pieceLength - 1 // pieces left after the position piece
	if spare <= 0 {
		return 0
	}
	protected := func(ahead int64) int64 { return ahead + ahead/trailingReadaheadDivisor }
	ahead := spare * trailingReadaheadDivisor / (trailingReadaheadDivisor + 1)
	for protected(ahead+1) <= spare {
		ahead++
	}
	return ahead * pieceLength
}

// filePieceLength returns the file's torrent piece length, or zero when the
// metadata is unavailable.
func filePieceLength(file *torrent.File) int64 {
	if file == nil || file.Torrent() == nil || file.Torrent().Info() == nil {
		return 0
	}
	return max(file.Torrent().Info().PieceLength, 0)
}

// availableProtectionBudgetLocked returns the protection capacity not already
// reserved by preload ranges. Must be called with p.mu held.
func (p *Pool) availableProtectionBudgetLocked(totalBudget int64) int64 {
	for _, reservation := range p.preloadBudgets {
		totalBudget -= reservation.bytes
	}
	return max(totalBudget, 0)
}

// preloadProtectionCapacity returns the portion of the shared protection
// budget that speculative preload leases may reserve. The remainder is kept
// available for playback readers.
func preloadProtectionCapacity(totalBudget int64) int64 {
	playbackReserve := min(totalBudget/2, 2*int64(DefaultFileBoundaryBytes))
	return max(totalBudget-playbackReserve, 0)
}

// preloadReservation is the protection budget held by a torrent's preload,
// the file and pieces it protects, and whether it protects both that file's
// head and tail.
type preloadReservation struct {
	bytes            int64
	coversBoundaries bool
	filePath         string
	// headStart, headEnd, tailStart, and tailEnd are the inclusive piece
	// ranges protected from eviction while the reservation is held.
	headStart, headEnd, tailStart, tailEnd int
	// headID and tailID identify the reservation's protected ranges in the
	// registry. They are drawn from the reader ID sequence, so they never
	// collide with a reader's own range.
	headID, tailID uint64
}

type readerFileKey struct {
	infoHash metainfo.Hash
	filePath string
}

// boundaryBytesPerFileLocked bounds the aggregate head-and-tail protection to
// at most half of the stream protection budget. Must be called with p.mu held.
func (p *Pool) boundaryBytesPerFileLocked(totalBudget int64, activeFiles int) int64 {
	if activeFiles <= 0 || totalBudget <= 0 {
		return 0
	}
	return min(int64(DefaultFileBoundaryBytes), totalBudget/(4*int64(activeFiles)))
}

const trailingReadaheadDivisor = 4 // trailing range is 1/4th of readahead pieces

// computeRange returns the (start, end) piece indices for a given file position,
// readahead, and read offset. All callers share the same clamping logic.
// The ahead range reaches the full readahead piece count to protect all pieces actively prefetched
// by the torrent client, matching reader.SetReadahead(readahead). The position
// piece is always included; readaheadForShare sizes readahead so that the
// complete range fits the reader's share of the protection budget.
func computeRange(file *torrent.File, pieceLength, readahead, byteOffset int64) (start, end int) {
	if pieceLength <= 0 {
		pieceLength = 1
	}
	readaheadPieces := max(readahead/pieceLength, 0)
	beginPiece := int64(file.BeginPieceIndex())
	// EndPieceIndex is exclusive; ActiveRangeRegistry endpoints are inclusive.
	endPieceMax := int64(file.EndPieceIndex()) - 1
	if endPieceMax < beginPiece {
		return int(beginPiece), int(beginPiece)
	}

	positionPiece := min(max((file.Offset()+byteOffset)/pieceLength, beginPiece), endPieceMax)
	trailing := readaheadPieces / trailingReadaheadDivisor

	startPiece := max(positionPiece-trailing, beginPiece)
	endPiece := min(positionPiece+readaheadPieces, endPieceMax)
	return int(startPiece), int(endPiece)
}

// DefaultFileBoundaryBytes is the minimum amount of head and tail bytes (8 MiB)
// protected from eviction to preserve container metadata and seek tables.
const DefaultFileBoundaryBytes = 8 << 20

// computeFileBoundaries calculates the inclusive head and tail piece ranges of
// boundaryBytes each, at least one piece, that protect a file's container
// metadata (e.g. EBML headers, SeekHead, Cues, moov atom) from eviction
// throughout streaming.
func computeFileBoundaries(file *torrent.File, boundaryBytes int64) (headStart, headEnd, tailStart, tailEnd int, ok bool) {
	if file == nil || file.Length() == 0 {
		return 0, 0, 0, 0, false
	}
	length := file.Length()
	startend := max(boundaryBytes, filePieceLength(file))
	return FilePieceRanges(file, min(length, startend), max(length-startend, 0), length)
}

// FilePieceRanges returns the inclusive torrent piece ranges holding the
// file-relative byte ranges [0, headEnd) and [tailStart, tailEnd). An empty
// tail range repeats the head range. It returns false when the file has no
// piece metadata or the head range is empty.
func FilePieceRanges(file *torrent.File, headEnd, tailStart, tailEnd int64) (int, int, int, int, bool) {
	if file == nil || file.Torrent() == nil || file.Torrent().Info() == nil || headEnd <= 0 {
		return 0, 0, 0, 0, false
	}
	pieceLength := max(file.Torrent().Info().PieceLength, 1)
	fileOffset := file.Offset()
	headStartPiece := int(fileOffset / pieceLength)
	headEndPiece := int((fileOffset + headEnd - 1) / pieceLength)
	if tailEnd <= tailStart {
		return headStartPiece, headEndPiece, headStartPiece, headEndPiece, true
	}
	return headStartPiece, headEndPiece,
		int((fileOffset + tailStart) / pieceLength),
		int((fileOffset + tailEnd - 1) / pieceLength), true
}

// BoundaryPieces returns the pieces of the inclusive head and tail piece
// ranges in ascending order. The tail may repeat or overlap the head; pieces
// shared by both are listed once.
func BoundaryPieces(headStart, headEnd, tailStart, tailEnd int) []int {
	pieces := make([]int, 0, max(headEnd-headStart+1, 0)+max(tailEnd-tailStart+1, 0))
	return slices.AppendSeq(pieces, boundaryPieces(headStart, headEnd, tailStart, tailEnd))
}

// BoundaryPieceBytes returns the storage size of the pieces BoundaryPieces
// lists. Storage protects and evicts whole pieces, so this is the memory the
// head and tail ranges occupy, including a shorter final piece at its real size.
func BoundaryPieceBytes(info *metainfo.Info, headStart, headEnd, tailStart, tailEnd int) int64 {
	var total int64
	for index := range boundaryPieces(headStart, headEnd, tailStart, tailEnd) {
		total += info.Piece(index).Length()
	}
	return total
}

func boundaryPieces(headStart, headEnd, tailStart, tailEnd int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for index := headStart; index <= headEnd; index++ {
			if !yield(index) {
				return
			}
		}
		for index := max(tailStart, headEnd+1); index <= tailEnd; index++ {
			if !yield(index) {
				return
			}
		}
	}
}

// registerActiveRangeLocked computes and registers the eviction-protection window
// for a reader at its current position with the given readahead, and its file
// boundaries of boundaryBytes each. A non-positive boundaryBytes clears any
// boundaries the reader registered before.
// byteOffset is passed explicitly to avoid acquiring wrapper.mu under pool.mu,
// consistent with the lock-ordering invariant documented on readAtWrapper.
// Must be called with p.mu held.
func (p *Pool) registerActiveRangeLocked(infoHash metainfo.Hash, key readerKey, file *torrent.File, readahead, byteOffset, boundaryBytes int64) {
	if p.cfg.Registry == nil || file == nil || file.Torrent() == nil || file.Torrent().Info() == nil {
		return
	}
	if file.EndPieceIndex() <= file.BeginPieceIndex() {
		return
	}
	if sr := p.readers[key]; sr != nil && sr.isPreload {
		return
	}
	pieceLength := file.Torrent().Info().PieceLength
	start, end := computeRange(file, pieceLength, readahead, byteOffset)
	p.cfg.Registry.SetActiveRange(infoHash, key.readerID, start, end)
	if boundaryBytes <= 0 {
		p.cfg.Registry.ClearFileBoundaries(infoHash, key.readerID)
		return
	}
	if hs, he, ts, te, ok := computeFileBoundaries(file, boundaryBytes); ok {
		p.cfg.Registry.SetFileBoundaries(infoHash, key.readerID, hs, he, ts, te)
	}
}

// prioritizeNextPieces plans the PiecePriorityNow claims for the fraction of
// the readahead pieces nearest to a reader. Applying the plan is separate so
// priorities shared by overlapping readers are only lowered after their final
// owner releases them.
func (p *Pool) prioritizeNextPieces(file *torrent.File, byteOffset, readahead int64, fraction float64) []prioritizedPiece {
	if fraction <= 0 || readahead <= 0 || file == nil {
		return nil
	}
	tor := file.Torrent()
	if tor == nil {
		return nil
	}
	info := tor.Info()
	if info == nil {
		return nil
	}

	// EndPieceIndex already is the exclusive loop boundary.
	endPieceMax := int64(file.EndPieceIndex())
	pieceLength := info.PieceLength
	if pieceLength <= 0 {
		pieceLength = 1
	}

	// Clamp target to the torrent's actual piece count — EndPieceIndex()
	// may exceed it for partially-seeded or split files.
	torrentPieceCount := int64(tor.NumPieces())
	if torrentPieceCount > 0 && endPieceMax > torrentPieceCount {
		endPieceMax = torrentPieceCount
	}
	// The read offset is file-relative, while priorityPlan works in torrent
	// pieces. Include the file's offset within its first piece.
	byteOffset += file.Offset() % pieceLength
	return buildPriorityPlan(byteOffset, readahead, pieceLength, int64(file.BeginPieceIndex()), endPieceMax, fraction)
}

// buildPriorityPlan constructs the bounded per-piece priority plan after the
// caller has resolved and validated the file and torrent bounds.
func buildPriorityPlan(byteOffset, readahead, pieceLength, beginPiece, endPieceMax int64, fraction float64) []prioritizedPiece {
	_, target, currentPiece := priorityPlan(byteOffset, readahead, pieceLength, beginPiece, endPieceMax, fraction)

	planned := make([]prioritizedPiece, 0, max(int(target-(currentPiece+1)), 0))
	for idx := currentPiece + 1; idx < target; idx++ {
		planned = append(planned, prioritizedPiece{index: int(idx), priority: torrent.PiecePriorityNow})
	}
	return planned
}

// replaceReaderPrioritiesLocked replaces one reader's claims and applies the
// highest priority still requested for every affected piece. sr.priorityMu
// must be held by the caller.
//
// Note: priorityPieceKey captures file.Torrent() at claim time, and
// clearReaderPrioritiesLocked recomputes sr.file.Torrent() upon release.
// This remains consistent because sr.file is set once when the reader is
// created.
func (p *Pool) replaceReaderPrioritiesLocked(sr *streamReader, file *torrent.File, planned []prioritizedPiece) {
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()

	touched := make(map[priorityPieceKey]struct{}, len(sr.prioritizedPieces)+len(planned))
	oldTorrent := (*torrent.Torrent)(nil)
	if sr.file != nil {
		oldTorrent = sr.file.Torrent()
	}
	if oldTorrent != nil {
		for _, index := range sr.prioritizedPieces {
			key := priorityPieceKey{torrent: oldTorrent, index: index}
			if claim := p.priorityClaims[key]; claim != nil {
				delete(claim.owners, sr)
			}
			touched[key] = struct{}{}
		}
	}

	sr.prioritizedPieces = sr.prioritizedPieces[:0]
	newTorrent := (*torrent.Torrent)(nil)
	if file != nil {
		newTorrent = file.Torrent()
	}
	if newTorrent != nil {
		for _, piece := range planned {
			key := priorityPieceKey{torrent: newTorrent, index: piece.index}
			claim := p.priorityClaims[key]
			if claim == nil {
				claim = &priorityClaim{owners: make(map[*streamReader]torrent.PiecePriority)}
				p.priorityClaims[key] = claim
			}
			claim.owners[sr] = piece.priority
			sr.prioritizedPieces = append(sr.prioritizedPieces, piece.index)
			touched[key] = struct{}{}
		}
	}

	for key := range touched {
		p.applyPriorityClaimLocked(key)
	}
}

// clearReaderPrioritiesLocked removes every priority owned by a reader.
// sr.priorityMu must be held by the caller.
func (p *Pool) clearReaderPrioritiesLocked(sr *streamReader) {
	oldTorrent := (*torrent.Torrent)(nil)
	if sr.file != nil {
		oldTorrent = sr.file.Torrent()
	}

	p.priorityMu.Lock()
	p.clearPriorityClaimsLocked(sr, oldTorrent)
	p.priorityMu.Unlock()
	sr.prioritizedPieces = nil
}

// clearPriorityClaimsLocked removes a reader from every listed piece claim and
// immediately reapplies the highest remaining owner priority. p.priorityMu must
// be held by the caller. Keeping this separate from replacement avoids allocating
// a temporary touched-piece map on release and eviction paths.
func (p *Pool) clearPriorityClaimsLocked(sr *streamReader, tor *torrent.Torrent) {
	for _, index := range sr.prioritizedPieces {
		key := priorityPieceKey{torrent: tor, index: index}
		if claim := p.priorityClaims[key]; claim != nil {
			delete(claim.owners, sr)
		}
		p.applyPriorityClaimLocked(key)
	}
}

// applyPriorityClaimLocked applies the highest remaining reader claim for a
// piece. p.priorityMu must be held by the caller.
func (p *Pool) applyPriorityClaimLocked(key priorityPieceKey) {
	claim := p.priorityClaims[key]
	priority := torrent.PiecePriorityNone
	if claim != nil {
		for _, ownerPriority := range claim.owners {
			if ownerPriority > priority {
				priority = ownerPriority
			}
		}
		if len(claim.owners) == 0 {
			delete(p.priorityClaims, key)
		}
	}
	if key.torrent == nil {
		return
	}
	piece := key.torrent.Piece(key.index)
	if piece != nil {
		piece.SetPriority(priority)
	}
}

// priorityPlan computes how many pieces ahead of the reader to prioritize. It
// is a pure function of the input parameters so it can be unit-tested without
// a live torrent. It returns the requested piece count n, the exclusive end
// piece target after clamping to endPieceMax, and currentPiece. The planned
// pieces are [currentPiece+1, target).
func priorityPlan(byteOffset, readahead, pieceLength, beginPiece, endPieceMax int64, fraction float64) (n int, target int64, currentPiece int64) {
	if fraction > 1 {
		fraction = 1
	}
	if pieceLength <= 0 {
		pieceLength = 1
	}
	readaheadPieces := max(readahead/pieceLength, 1)
	n = max(int(float64(readaheadPieces)*fraction), 1)
	currentPiece = byteOffset/pieceLength + beginPiece
	target = max(min(currentPiece+1+int64(n), endPieceMax), currentPiece+1)
	return n, target, currentPiece
}

// findReaderLocked returns the streamReader for the given key, or nil.
// Must be called with p.mu held.
func (p *Pool) findReaderLocked(key readerKey) *streamReader {
	return p.readers[key]
}

// updateActiveRange recalculates and refreshes the eviction-protection
// window for a reader based on its new read offset. Called asynchronously
// from readAtWrapper when the position moves. Takes pool.mu to protect
// against concurrent release, Close, and lingering-reader closure.
//
// The active-range registration stays under p.mu to preserve atomicity
// with release/Close.  Piece-priority bumping uses the reader's own
// priorityMu so it does not block other pool operations (Acquire,
// release, lingering-reader closure).  A per-reader sequence counter ensures
// that out-of-order priority goroutines drop stale results.
func (p *Pool) updateActiveRange(infoHash metainfo.Hash, key readerKey, file *torrent.File, newOffset int64) {
	p.mu.Lock()

	// Skip a reader that was already released or closed.
	sr := p.findReaderLocked(key)
	if sr == nil || !sr.active {
		p.mu.Unlock()
		return
	}

	// Cache the offset on the streamReader so other pool.mu holders
	// (refreshReadaheadLocked, ReaderPositions) can read it without touching
	// wrapper.mu — preserving the lock order.
	sr.lastOffset = newOffset
	if sr.isPreload {
		sr.readahead = max(sr.preloadEnd-newOffset, 0)
	}
	pieceLength := int64(1)
	if file != nil && file.Torrent() != nil && file.Torrent().Info() != nil && file.Torrent().Info().PieceLength > 0 {
		pieceLength = file.Torrent().Info().PieceLength
	}
	torrentOffset := newOffset
	if file != nil {
		torrentOffset += file.Offset()
	}
	currentPiece := torrentOffset / pieceLength
	pieceChanged := (currentPiece != sr.lastPieceIdx) || (sr.lastPieceIdx < 0)
	sr.lastPieceIdx = currentPiece

	if !pieceChanged {
		p.mu.Unlock()
		return
	}

	readahead := sr.readahead
	var prioEnabled bool
	var seq uint64
	if p.cfg.PriorityWindowFraction > 0 && !sr.isFileStorage && !sr.isPreload {
		seq = sr.prioritySeq.Add(1)
		prioEnabled = true
	}

	if !sr.isPreload && p.cfg.Registry != nil && file != nil && file.Torrent() != nil && file.Torrent().Info() != nil {
		start, end := computeRange(file, pieceLength, readahead, newOffset)
		p.cfg.Registry.SetActiveRange(infoHash, key.readerID, start, end)
	}
	p.mu.Unlock()

	if prioEnabled {
		go p.prioritizeAsync(sr, seq, file, newOffset, readahead)
	}
}

// prioritizeAsync updates piece priorities for a single reader.  It holds
// sr.priorityMu for the entire body (read-snapshot → compute → conditional
// write-back) so that in-flight goroutines cannot race each other's
// SetPriority calls, and release/Close can invalidate them by bumping
// prioritySeq.
func (p *Pool) prioritizeAsync(sr *streamReader, seq uint64, file *torrent.File, newOffset, readahead int64) {
	fraction := p.cfg.PriorityWindowFraction

	sr.priorityMu.Lock()
	if sr.prioritySeq.Load() != seq {
		sr.priorityMu.Unlock()
		return
	}
	planned := p.prioritizeNextPieces(file, newOffset, readahead, fraction)
	p.replaceReaderPrioritiesLocked(sr, file, planned)
	sr.priorityMu.Unlock()
}

// ReaderPositions returns positions for all active and lingering readers belonging
// to the given info hash. The result order is unspecified.
func (p *Pool) ReaderPositions(infoHash metainfo.Hash) []ReaderPosition {
	p.mu.Lock()
	defer p.mu.Unlock()

	var result []ReaderPosition
	for key, sr := range p.readers {
		if key.infoHash != infoHash {
			continue
		}
		if sr.file == nil || sr.file.Torrent() == nil {
			continue
		}
		if sr.file.EndPieceIndex() <= sr.file.BeginPieceIndex() {
			continue
		}
		info := sr.file.Torrent().Info()
		if info == nil {
			continue
		}
		byteOffset := sr.lastOffset
		pieceLength := info.PieceLength
		if pieceLength <= 0 {
			pieceLength = 1
		}
		start, end := computeRange(sr.file, pieceLength, sr.readahead, byteOffset)
		position := max(int((sr.file.Offset()+byteOffset)/pieceLength), sr.file.BeginPieceIndex())
		lastPiece := sr.file.EndPieceIndex() - 1
		if position > lastPiece {
			position = lastPiece
		}
		result = append(result, ReaderPosition{
			End:      end,
			Position: position,
			Start:    start,
		})
	}
	return result
}

// HasReaders returns true if there is at least one reader (active or
// lingering) for the given info hash. This intentionally includes lingering
// readers, which are still downloading for the player's next request.
// Callers that need to distinguish active playback from lingering readers
// should use HasActiveReaders instead.
func (p *Pool) HasReaders(infoHash metainfo.Hash) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, sr := range p.readers {
		if sr.infoHash == infoHash {
			return true
		}
	}
	return false
}

// HasActiveReaders reports whether the given info hash has an active reader.
func (p *Pool) HasActiveReaders(infoHash metainfo.Hash) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, sr := range p.readers {
		if sr.infoHash == infoHash && sr.active {
			return true
		}
	}
	return false
}

// Close shuts down the pool, closes all readers, and clears active ranges.
// It is safe to call multiple times.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}
	p.closed = true
	close(p.closeCh)

	for key, sr := range p.readers {
		p.removeReaderLocked(key, sr)
	}
	for infoHash, reservation := range p.preloadBudgets {
		p.clearPreloadProtectionLocked(infoHash, reservation)
	}
	clear(p.preloadBudgets)

	p.logger.Debug("stream pool closed")
}

// idleGC periodically closes readers that have lingered past the effective
// linger timeout.
func (p *Pool) idleGC() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.closeCh:
			return
		case <-ticker.C:
			p.closeExpiredLingeringReaders()
		}
	}
}

// effectiveLingerTimeout returns the linger timeout for the given memory
// usage. usage < 0 means "no memory-usage callback configured" and returns
// p.cfg.LingerTimeout. Pressure only ever shortens the configured timeout.
func (p *Pool) effectiveLingerTimeout(usage float64) time.Duration {
	switch {
	case usage >= 0.90:
		return min(p.cfg.LingerTimeout, 1*time.Second)
	case usage >= 0.75:
		return min(p.cfg.LingerTimeout, 5*time.Second)
	case usage >= 0.50:
		return min(p.cfg.LingerTimeout, 10*time.Second)
	default:
		return p.cfg.LingerTimeout
	}
}

// sampleMemoryPressure returns the current memory usage ratio, calling
// MemoryUsage at most once. Returns -1 if no function is set.
func (p *Pool) sampleMemoryPressure() float64 {
	if p.cfg.MemoryUsage == nil {
		return -1
	}
	return p.cfg.MemoryUsage()
}

// closeExpiredLingeringReaders closes readers that have lingered at least the
// effective linger timeout. Lingering readers hold no share of the readahead
// budget, so closing them needs no rebalance.
func (p *Pool) closeExpiredLingeringReaders() {
	p.mu.Lock()
	defer p.mu.Unlock()

	timeout := p.effectiveLingerTimeout(p.sampleMemoryPressure())
	now := time.Now()
	for key, sr := range p.readers {
		if sr.active {
			continue
		}
		lingered := now.Sub(sr.lingerSince)
		if lingered < timeout {
			continue
		}
		p.removeReaderLocked(key, sr)
		p.logger.Debug("closed lingering reader",
			slog.String("hash", sr.infoHash.HexString()),
			slog.Uint64("readerID", sr.readerID),
			slog.Duration("lingered", lingered))
	}
}
