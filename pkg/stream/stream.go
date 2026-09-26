// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	// Position is the reader's current piece index, or its last position while idle.
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
	// IdleCloseTimeout is the maximum time a reader may remain parked before it is
	// closed and removed from the pool. Zero defaults to 5 minutes. Negative
	// values disable the idle-close limit entirely.
	IdleCloseTimeout time.Duration
	// FileReadaheadBytes is the fixed readahead in bytes for file-storage readers.
	// Zero defaults to 50 MiB.
	FileReadaheadBytes int64
	// Logger receives pool lifecycle and diagnostic messages. Nil uses slog.Default.
	Logger *slog.Logger
	// MaxReadersPerFile limits the total number of torrent readers
	// (active + idle) allowed per (info hash, file path) pair.
	// A value of 0 uses the default of 10. Set a negative value to disable the
	// cap entirely.
	// The cap is best-effort and soft: active readers are never terminated mid-stream,
	// allowing bursts of concurrent active readers to temporarily exceed the limit.
	// When readers are released to idle (or upon reuse in Acquire), excess idle readers
	// are evicted to reconcile the pool back down to the configured cap.
	MaxReadersPerFile int
	// MemoryUsage returns the current memory usage ratio (0.0–1.0).
	// When set, idle readers are parked faster under memory pressure to
	// prevent pieces from being downloaded and immediately evicted.
	// When nil, a fixed IdleParkTimeout is used.
	MemoryUsage func() float64
	// IdleParkTimeout is the idle duration after which a reader's readahead is set
	// to zero. Zero defaults to 30 seconds. Memory pressure may shorten it.
	IdleParkTimeout time.Duration
	// PriorityWindowFraction, when > 0, bumps the download priority of pieces
	// ahead of the reader to PiecePriorityNow for the closest
	// PriorityNowFraction of them and PiecePriorityHigh for the rest. The
	// fraction applies to the readahead window size in pieces (end − position).
	// Values above 1 are clamped to 1. A non-positive value disables prioritization.
	PriorityWindowFraction float64
	// PriorityNowFraction, when > 0, defines the fraction of the
	// prioritized pieces that receive PiecePriorityNow (closest to the reader);
	// the remaining fraction receives PiecePriorityHigh. Values above 1 are clamped to 1.
	// Defaults to 0.3 (30 % get Now, 70 % get High).
	PriorityNowFraction float64
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

func (rw *readAtWrapper) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return nil
	}
	rw.closed = true
	rw.onOffsetChange = nil
	rw.cacheBuf = nil
	rw.cacheLen = 0
	rw.cacheStart = 0
	rw.offset = 0
	if rw.reader == nil {
		return nil
	}
	return rw.reader.Close()
}

// retire prevents a released wrapper from accessing a torrent reader that has
// been reassigned to a new pool lease. The shared reader remains open for the
// replacement wrapper.
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
	active         bool
	cancel         context.CancelFunc
	closeOnRelease bool
	ctx            context.Context
	file           *torrent.File
	infoHash       metainfo.Hash
	idleSince      time.Time
	isFileStorage  bool
	isPreload      bool
	preloadEnd     int64
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
	readahead   int64 // current readahead in bytes (updated by refreshReadaheadLocked, Acquire, and parkIdleReaders)
	reader      torrent.Reader
	readerID    uint64
	wrapper     *readAtWrapper
}

// Pool manages a bounded set of torrent readers with dynamic readahead management.
// Callers must call Close() when the pool is no longer needed to stop the background
// idle-GC goroutine and release reader resources.
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

// New creates a new stream pool and starts a background idle-GC goroutine.
// Callers must call Close() when the pool is no longer needed to terminate
// the background goroutine and avoid resource leakage.
func New(cfg Config) *Pool {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.IdleCloseTimeout == 0 {
		cfg.IdleCloseTimeout = 5 * time.Minute
	}
	if cfg.FileReadaheadBytes == 0 {
		cfg.FileReadaheadBytes = 50 * 1024 * 1024
	}
	if cfg.MaxReadersPerFile == 0 {
		cfg.MaxReadersPerFile = 10
	} else if cfg.MaxReadersPerFile < 0 {
		cfg.MaxReadersPerFile = 0
	}
	if cfg.IdleParkTimeout == 0 {
		cfg.IdleParkTimeout = 30 * time.Second
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
// [start, end). The preload controller owns eviction protection for that range,
// while every intersecting torrent piece is held at PiecePriorityNow until the
// reader is released.
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

	// Try to reuse an idle reader for the same torrent file. Preload readers
	// never reuse one: they are one-shot range workers that close on release,
	// so taking a parked playback reader would both retarget it away from the
	// playback position and destroy it once the preload finishes.
	var reusableKey readerKey
	var reusableSR *streamReader
	for key, sr := range p.readers {
		if key.infoHash != infoHash || key.filePath != file.Path() {
			continue
		}

		// A torrent can be explicitly dropped and re-added with the same
		// info hash and file path. Its old readers remain bound to the closed
		// torrent instance and must never be reused for the replacement. Remove
		// idle readers immediately and retire active readers when their caller
		// releases them. This reconciliation applies to preload acquisitions too,
		// even though preload readers themselves are never reused.
		if sr.file == nil || sr.file.Torrent() != file.Torrent() {
			if sr.active {
				sr.closeOnRelease = true
				continue
			}
			p.removeReaderLocked(key, sr)
			continue
		}

		if !isPreload && !sr.active && reusableSR == nil {
			reusableKey = key
			reusableSR = sr
		}
	}

	if reusableSR != nil {
		sr := reusableSR
		key := reusableKey
		// Retire the old wrapper before touching the shared torrent reader so a
		// stale caller cannot race the new lease's context or position reset.
		if sr.wrapper != nil {
			sr.wrapper.retire()
		}
		readerCtx, cancel := context.WithCancel(ctx)
		sr.active = true
		sr.cancel = cancel
		sr.ctx = readerCtx
		sr.reader.SetContext(readerCtx)
		sr.idleSince = time.Time{}
		sr.isFileStorage = isFileStorage
		sr.isPreload = isPreload
		sr.preloadEnd = preloadEnd
		sr.file = file

		// Reset lastOffset so refreshReadaheadLocked/ReaderPositions don't
		// read a stale offset from a previous activation.
		_, _ = sr.reader.Seek(0, io.SeekStart)
		sr.lastOffset = 0
		sr.lastPieceIdx = -1
		// Reader reuse does not need to bump prioritySeq or clear old claims here
		// because readers only transition active -> idle -> active through release(),
		// which already bumped prioritySeq and cleared claims under priorityMu.
		sr.wrapper = p.newReaderWrapper(sr.reader, key, file)

		// No preload arm here: the reuse search above is skipped entirely for
		// preload readers, so isPreload is always false on this path.
		if isFileStorage {
			sr.readahead = p.cfg.FileReadaheadBytes
			sr.reader.SetReadahead(p.cfg.FileReadaheadBytes)
			p.registerActiveRangeLocked(infoHash, key, file, p.cfg.FileReadaheadBytes, 0, DefaultFileBoundaryBytes)
			p.parkCompetingIdleReadersLocked()
		} else {
			p.refreshReadaheadLocked(p.readaheadBudget)
		}

		// Readers left over from earlier concurrent bursts may exceed the cap.
		p.trimIdleReadersLocked(infoHash, file.Path())

		p.logger.Debug("reused idle reader",
			slog.String("hash", infoHash.HexString()),
			slog.String("file", file.Path()),
			slog.Uint64("readerID", sr.readerID),
			slog.Int64("readahead", sr.readahead))

		return p.newReadSeeker(sr.wrapper, file.Length(), mode, isPreload), p.releaseFunc(key), nil
	}

	// No idle reader found for reuse. All existing readers for this file are active.
	// The MaxReadersPerFile cap is enforced on the reuse and release paths only;
	// active readers are never interrupted to satisfy the cap.
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
		ctx:           readerCtx,
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
		p.parkCompetingIdleReadersLocked()
		// Claimed synchronously under p.mu, unlike playback's prioritizeAsync
		// (see updateActiveRange). The claim has to be atomic with reader
		// creation or a concurrent release could race it, and preload pays the
		// cost once per reader rather than on every piece boundary, so keeping
		// it off the lock is not worth restructuring acquireContext for.
		// Lock order p.mu -> sr.priorityMu -> p.priorityMu matches
		// prioritizeAsync's sr.priorityMu -> p.priorityMu.
		sr.priorityMu.Lock()
		p.replaceReaderPrioritiesLocked(sr, file, preloadPriorityPlan(file, preloadStart, preloadEnd))
		sr.priorityMu.Unlock()
	case isFileStorage:
		sr.readahead = p.cfg.FileReadaheadBytes
		reader.SetReadahead(p.cfg.FileReadaheadBytes)
		p.registerActiveRangeLocked(infoHash, key, file, p.cfg.FileReadaheadBytes, 0, DefaultFileBoundaryBytes)
		p.parkCompetingIdleReadersLocked()
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
		p.updateActiveRange(key.infoHash, key, file, wrapper, newOffset)
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

// preloadPriorityPlan returns every torrent piece intersecting the file-relative
// byte range [start, end). Unlike playback's moving priority window, preload
// keeps the complete bounded range at Now priority until its reader is released.
func preloadPriorityPlan(file *torrent.File, start, end int64) []prioritizedPiece {
	if file == nil || end <= start {
		return nil
	}
	tor := file.Torrent()
	if tor == nil || tor.Info() == nil {
		return nil
	}
	pieceLength := tor.Info().PieceLength
	if pieceLength <= 0 {
		return nil
	}

	start = min(max(start, 0), file.Length())
	end = min(max(end, start), file.Length())
	if end <= start {
		return nil
	}

	// Clamping start and end to the file bounds above already keeps these
	// within [file.BeginPieceIndex(), file.EndPieceIndex()]: both are derived
	// from the same piece length, as floor(offset/pieceLength) and
	// ceil((offset+length)/pieceLength) respectively.
	first := (file.Offset() + start) / pieceLength
	last := (file.Offset() + end) / pieceLength
	if (file.Offset()+end)%pieceLength != 0 {
		last++
	}
	if last <= first {
		return nil
	}

	planned := make([]prioritizedPiece, 0, last-first)
	for index := first; index < last; index++ {
		planned = append(planned, prioritizedPiece{
			index:    int(index),
			priority: torrent.PiecePriorityNow,
		})
	}
	return planned
}

// closeStreamReaderLocked cancels and closes a reader while serializing with
// any in-flight ReadAt operation through its wrapper. The caller must hold
// Pool.mu so no new pool-owned use can begin while closure is in progress.
func closeStreamReaderLocked(sr *streamReader) {
	if sr.cancel != nil {
		sr.cancel()
		sr.cancel = nil
	}
	if sr.wrapper != nil {
		_ = sr.wrapper.Close()
	} else if sr.reader != nil {
		_ = sr.reader.Close()
	}
	sr.reader = nil
}

// release marks a reader as idle and clears active ranges.
// Called immediately after the HTTP request ends (via defer in streamFile).
//
// The reader's context is intentionally NOT cancelled here. The final reader
// may continue warming its read window while no other work is active. As soon
// as another playback or preload reader is active, competing idle readers are
// parked so stale windows cannot consume bandwidth outside the shared budget.
// The context is cancelled only when the reader is evicted by idleGC or when
// the pool is closed.
//
// Note: ClearActiveRange is called unconditionally here, which tears down
// eviction protection the moment the reader becomes idle. During the gap
// between release and parkIdleReaders (when the reader is still prefetching),
// pieces being fetched are no longer protected from eviction under memory
// pressure. This is a best-effort tradeoff — under normal conditions the
// reader's readahead window still keeps recently-fetched pieces alive, and
// the window is only briefly unprotected.
func (p *Pool) release(infoHash metainfo.Hash, filePath string, readerID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := readerKey{infoHash: infoHash, filePath: filePath, readerID: readerID}
	sr, ok := p.readers[key]
	if !ok || !sr.active {
		return
	}

	sr.active = false
	if sr.isPreload || sr.closeOnRelease {
		// Preload readers are one-shot range workers, and readers belonging to a
		// replaced torrent generation must not enter the idle reuse pool. Closing
		// either immediately clears anacrolix's current-piece claim and prevents
		// stale readers from keeping the current torrent alive through HasReaders.
		p.removeReaderLocked(key, sr)
		p.rebalanceLocked()
		return
	}

	// Restore piece priorities to None so they no longer compete with
	// other readers' readahead windows.
	p.clearReaderClaimsLocked(sr)
	sr.idleSince = time.Now()

	// Clear cacheBuf to reclaim buffer memory while the reader is idle in the pool.
	if sr.wrapper != nil {
		sr.wrapper.mu.Lock()
		sr.wrapper.cacheBuf = nil
		sr.wrapper.cacheLen = 0
		sr.wrapper.cacheStart = 0
		sr.wrapper.mu.Unlock()
	}

	p.logger.Debug("reader released to idle",
		slog.String("hash", infoHash.HexString()),
		slog.String("file", filePath),
		slog.Uint64("readerID", readerID))

	// A burst of concurrent active readers may have exceeded the cap. If this
	// reader is the only idle one when the count is over the limit, it may be
	// evicted immediately.
	p.trimIdleReadersLocked(infoHash, filePath)
	p.rebalanceLocked()
}

// rebalanceLocked redistributes the readahead budget after a reader stops
// being active, or only parks competing idle readers while no budget is set.
// Must be called with p.mu held.
func (p *Pool) rebalanceLocked() {
	if p.readaheadBudget > 0 {
		p.refreshReadaheadLocked(p.readaheadBudget)
	} else {
		p.parkCompetingIdleReadersLocked()
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

// trimIdleReadersLocked evicts the oldest idle readers of a file until its
// reader count is within MaxReadersPerFile. Active readers are never evicted,
// so the count may remain above the cap. Must be called with p.mu held.
func (p *Pool) trimIdleReadersLocked(infoHash metainfo.Hash, filePath string) {
	if p.cfg.MaxReadersPerFile <= 0 {
		return
	}
	for count := p.countReadersLocked(infoHash, filePath); count > p.cfg.MaxReadersPerFile; count-- {
		if !p.evictOldestIdleLocked(infoHash, filePath) {
			return
		}
	}
}

// evictOldestIdleLocked removes the idle reader with the oldest idleSince
// for the given (info hash, file path). It returns true if a reader was evicted.
// Only idle readers are evicted — active readers are never killed mid-stream.
// This means the MaxReadersPerFile cap is soft when no idle readers exist.
// Must be called with p.mu held.
func (p *Pool) evictOldestIdleLocked(infoHash metainfo.Hash, filePath string) bool {
	var oldest *streamReader
	var oldestKey readerKey
	for key, sr := range p.readers {
		if key.infoHash == infoHash && key.filePath == filePath && !sr.active {
			if oldest == nil || sr.idleSince.Before(oldest.idleSince) {
				oldest = sr
				oldestKey = key
			}
		}
	}
	if oldest != nil {
		p.removeReaderLocked(oldestKey, oldest)
		p.logger.Debug("evicted idle reader to make room",
			slog.String("hash", infoHash.HexString()),
			slog.String("file", filePath),
			slog.Uint64("readerID", oldest.readerID))
		return true
	}
	return false
}

// countReadersLocked returns the number of active and idle readers for the
// given (info hash, file path).
// Must be called with p.mu held.
func (p *Pool) countReadersLocked(infoHash metainfo.Hash, filePath string) int {
	count := 0
	for key := range p.readers {
		if key.infoHash == infoHash && key.filePath == filePath {
			count++
		}
	}
	return count
}

// refreshReadaheadLocked recalculates and applies readahead for all active readers.
// File-storage readers keep their fixed readahead and are never divided.
// Must be called with p.mu held.
func (p *Pool) refreshReadaheadLocked(totalReadaheadBudget int64) {
	p.parkCompetingIdleReadersLocked()
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

	p.logger.Debug("refreshed readahead",
		slog.Int64("totalPool", totalReadaheadBudget),
		slog.Int64("perReaderShare", plan.share))
}

// parkCompetingIdleReadersLocked stops stale read windows whenever playback
// or preload work is active. An idle reader can remain warm only while it is
// the pool's sole work, preserving fast reuse without allowing released HTTP
// range requests to accumulate unbudgeted downloads. A running preload is
// active work through its own readers; a preload reservation alone is not,
// because a completed preload that still holds one downloads nothing. Must be
// called with p.mu held.
func (p *Pool) parkCompetingIdleReadersLocked() {
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

	for _, sr := range p.readers {
		if sr.active || sr.readahead <= 0 {
			continue
		}
		sr.readahead = 0
		if sr.reader != nil {
			sr.reader.SetReadahead(0)
		}
		p.logger.Debug("parked competing idle reader",
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

// ReservePreloadBudget admits a preload of filePath into the same global
// protection budget used by streaming readahead. Replacing a reservation for
// the same torrent is atomic. When coversBoundaries reports that the preload
// protects both the file's head and tail itself, playback readers of that file
// skip their own boundary protection while the reservation is held instead of
// paying for those pieces twice. A preload that protects only the head, such
// as one trimmed to a small budget, leaves reader boundaries in place so the
// tail stays protected. Preloads may use otherwise-idle capacity but always leave a bounded
// playback reserve, so a newly acquired stream cannot be reduced to the
// one-byte minimum by completed preload leases. The returned byte count may be
// smaller than requested when other preloads already consume the preload share.
func (p *Pool) ReservePreloadBudget(infoHash metainfo.Hash, filePath string, coversBoundaries bool, requested int64) int64 {
	if requested < 0 {
		requested = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	reserved := int64(0)
	for hash, reservation := range p.preloadBudgets {
		if hash != infoHash {
			reserved += reservation.bytes
		}
	}
	preloadCapacity := preloadProtectionCapacity(p.readaheadBudget)
	granted := min(requested, max(preloadCapacity-reserved, 0))
	if granted > 0 {
		p.preloadBudgets[infoHash] = preloadReservation{bytes: granted, coversBoundaries: coversBoundaries, filePath: filePath}
	} else {
		delete(p.preloadBudgets, infoHash)
	}
	p.refreshReadaheadLocked(p.readaheadBudget)
	return granted
}

// PreloadCapacity returns the total bytes that preload reservations may hold
// under the current readahead budget. The remainder of the budget is kept for
// playback readers.
func (p *Pool) PreloadCapacity() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return preloadProtectionCapacity(p.readaheadBudget)
}

// ReleasePreloadBudget removes a torrent's preload reservation and restores
// the freed capacity to active stream readers.
func (p *Pool) ReleasePreloadBudget(infoHash metainfo.Hash) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.preloadBudgets[infoHash]; !ok {
		return
	}
	delete(p.preloadBudgets, infoHash)
	p.refreshReadaheadLocked(p.readaheadBudget)
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
	for boundaryBytes = allowance / 2; ; boundaryBytes = max(boundaryBytes-pieceLength, pieceLength) {
		headStart, headEnd, tailStart, tailEnd, ok := computeFileBoundaries(file, boundaryBytes)
		if !ok {
			return 0, 0, false
		}
		// The tail may repeat or overlap the head; count shared pieces once.
		pieces := int64(headEnd-headStart+1) + int64(max(tailEnd-max(tailStart, headEnd+1)+1, 0))
		if cost = pieces * pieceLength; cost <= allowance {
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
// the file it protects, and whether it protects both that file's head and
// tail.
type preloadReservation struct {
	bytes            int64
	coversBoundaries bool
	filePath         string
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

// ComputeFileBoundaries calculates the head and tail piece index ranges (inclusive)
// for a file to protect container metadata (e.g. EBML headers, SeekHead, Cues, moov atom)
// from eviction throughout streaming.
func ComputeFileBoundaries(file *torrent.File) (headStart, headEnd, tailStart, tailEnd int, ok bool) {
	return computeFileBoundaries(file, DefaultFileBoundaryBytes)
}

func computeFileBoundaries(file *torrent.File, boundaryBytes int64) (headStart, headEnd, tailStart, tailEnd int, ok bool) {
	if file == nil || file.Length() == 0 {
		return 0, 0, 0, 0, false
	}
	tor := file.Torrent()
	if tor == nil || tor.Info() == nil {
		return 0, 0, 0, 0, false
	}
	pieceLength := tor.Info().PieceLength
	if pieceLength <= 0 {
		pieceLength = 1
	}

	startend := max(boundaryBytes, pieceLength)
	fileOffset := file.Offset()
	fileEndOffset := fileOffset + file.Length()

	headPieceStart := int(fileOffset / pieceLength)
	headPieceEnd := int(min(fileEndOffset-1, fileOffset+startend-1) / pieceLength)

	tailPieceStart := int(max(fileOffset, fileEndOffset-startend) / pieceLength)
	tailPieceEnd := int((fileEndOffset - 1) / pieceLength)

	return headPieceStart, headPieceEnd, tailPieceStart, tailPieceEnd, true
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

// prioritizeNextPieces plans the priorities ahead of a reader. The nearest
// The nearFraction proportion of selected pieces gets PiecePriorityNow; the rest
// get PiecePriorityHigh.
// Applying the plan is separate so priorities shared by overlapping readers
// are only lowered after their final owner releases them.
func (p *Pool) prioritizeNextPieces(file *torrent.File, byteOffset, readahead int64, fraction, nearFraction float64) []prioritizedPiece {
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
	return buildPriorityPlan(byteOffset, readahead, pieceLength, int64(file.BeginPieceIndex()), endPieceMax, fraction, nearFraction)
}

// buildPriorityPlan constructs the bounded per-piece priority plan after the
// caller has resolved and validated the file and torrent bounds.
func buildPriorityPlan(byteOffset, readahead, pieceLength, beginPiece, endPieceMax int64, fraction, nearFraction float64) []prioritizedPiece {
	n, nowCount, target, currentPiece := priorityPlan(byteOffset, readahead, pieceLength, beginPiece, endPieceMax, fraction, nearFraction)

	planCapacity := max(int(target-(currentPiece+1)), 0)
	planned := make([]prioritizedPiece, 0, planCapacity)
	count := 0
	for idx := currentPiece + 1; idx < target; idx++ {
		priority := torrent.PiecePriorityHigh
		if count < nowCount {
			priority = torrent.PiecePriorityNow
		}
		planned = append(planned, prioritizedPiece{index: int(idx), priority: priority})
		count++
		if count >= n {
			break
		}
	}
	return planned
}

// replaceReaderPrioritiesLocked replaces one reader's claims and applies the
// highest priority still requested for every affected piece. sr.priorityMu
// must be held by the caller.
//
// Note: priorityPieceKey captures file.Torrent() at claim time, and
// clearReaderPrioritiesLocked recomputes sr.file.Torrent() upon release.
// This remains consistent because sr.file is only modified during reader
// acquisition/reuse under p.mu, never while active.
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

// priorityPlan computes how many pieces to prioritize and how to split them
// between Now and High. It is a pure function of the input parameters so it
// can be unit-tested without a live torrent. It returns n, nowCount, target,
// and currentPiece.
func priorityPlan(byteOffset, readahead, pieceLength, beginPiece, endPieceMax int64, fraction, nearFraction float64) (n int, nowCount int, target int64, currentPiece int64) {
	if fraction > 1 {
		fraction = 1
	}
	if pieceLength <= 0 {
		pieceLength = 1
	}
	readaheadPieces := max(readahead/pieceLength, 1)
	n = max(int(float64(readaheadPieces)*fraction), 1)
	if nearFraction <= 0 {
		nearFraction = 0.3
	}
	nearFraction = min(nearFraction, 1)
	nowCount = max(int(float64(n)*nearFraction), 1)
	currentPiece = byteOffset/pieceLength + beginPiece
	target = max(min(currentPiece+1+int64(n), endPieceMax), currentPiece+1)
	return n, nowCount, target, currentPiece
}

// findReaderLocked returns the streamReader for the given key, or nil.
// Must be called with p.mu held.
func (p *Pool) findReaderLocked(key readerKey) *streamReader {
	return p.readers[key]
}

// updateActiveRange recalculates and refreshes the eviction-protection
// window for a reader based on its new read offset. Called asynchronously
// from readAtWrapper when the position moves. origin identifies the specific
// wrapper that issued the callback to discard stale notifications across reader reuse.
// Takes pool.mu to protect against concurrent release / Close / parkIdleReaders.
//
// The active-range registration stays under p.mu to preserve atomicity
// with release/Close.  Piece-priority bumping uses the reader's own
// priorityMu so it does not block other pool operations (Acquire,
// release, parkIdleReaders).  A per-reader sequence counter ensures
// that out-of-order priority goroutines drop stale results.
func (p *Pool) updateActiveRange(infoHash metainfo.Hash, key readerKey, file *torrent.File, origin *readAtWrapper, newOffset int64) {
	p.mu.Lock()

	// If the reader was already released, deleted, or reassigned to a new wrapper on reuse, skip.
	// Note: origin check verifies wrapper identity; origin != nil allows test stubs lacking wrappers.
	sr := p.findReaderLocked(key)
	if sr == nil || !sr.active || (origin != nil && sr.wrapper != origin) {
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
	near := p.cfg.PriorityNowFraction
	fraction := p.cfg.PriorityWindowFraction

	sr.priorityMu.Lock()
	if sr.prioritySeq.Load() != seq {
		sr.priorityMu.Unlock()
		return
	}
	planned := p.prioritizeNextPieces(file, newOffset, readahead, fraction, near)
	p.replaceReaderPrioritiesLocked(sr, file, planned)
	sr.priorityMu.Unlock()
}

// ReaderPositions returns positions for all active and idle readers belonging
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

// HasReaders returns true if there is at least one reader (active or idle)
// for the given info hash. This intentionally includes idle readers
// because pooled readers are still valid resources that should prevent
// torrent expiration. Callers that need to distinguish active playback
// from idle pool members should use HasActiveReaders instead.
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
	clear(p.preloadBudgets)

	p.logger.Debug("stream pool closed")
}

// idleGC periodically runs parkIdleReaders, which parks (readahead → 0) readers
// idle longer than the configured timeout and closes those idle longer than
// IdleCloseTimeout.
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

// effectiveParkTimeout returns the park timeout for the given memory usage.
// usage < 0 means "no memory-usage callback configured" and returns
// p.cfg.IdleParkTimeout.
func (p *Pool) effectiveParkTimeout(usage float64) time.Duration {
	switch {
	case usage < 0:
		return p.cfg.IdleParkTimeout
	case usage >= 0.90:
		return 1 * time.Second
	case usage >= 0.75:
		return 5 * time.Second
	case usage >= 0.50:
		return 10 * time.Second
	default:
		return p.cfg.IdleParkTimeout
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

// parkIdleReaders sets readahead to 0 for readers that have been idle
// longer than the effective timeout, and closes readers that have been
// idle longer than IdleCloseTimeout (the close check runs before the park check
// in the same loop iteration, so a reader can be closed without ever being
// parked — this matters when IdleParkTimeout >= IdleCloseTimeout under unusual configs).
// Under memory pressure the close timeout is also shortened proportionally.
//
// It collects the actions to take first, then applies them while still
// holding the lock, to keep the critical section minimal.
func (p *Pool) parkIdleReaders() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Sample memory pressure once per tick for consistent policy decisions.
	usage := p.sampleMemoryPressure()

	timeout := p.effectiveParkTimeout(usage)
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
		// memory faster. A negative IdleCloseTimeout means "never close" even
		// under pressure; the pressure path only activates when a deadline is
		// set (IdleCloseTimeout > 0).
		closeDeadline := p.cfg.IdleCloseTimeout
		if p.cfg.IdleCloseTimeout > 0 && usage >= 0 {
			if usage >= 0.90 {
				closeDeadline = min(closeDeadline, 30*time.Second)
			} else if usage >= 0.75 {
				closeDeadline = min(closeDeadline, 60*time.Second)
			}
		}

		if closeDeadline > 0 && idle >= closeDeadline {
			toClose = append(toClose, closeAction{key: key, sr: sr, idle: idle})
			continue
		}

		if sr.readahead > 0 && idle >= timeout {
			toPark = append(toPark, sr)
		}
	}

	// Apply close actions.
	for _, c := range toClose {
		p.removeReaderLocked(c.key, c.sr)
		p.logger.Debug("closed idle reader",
			slog.String("hash", c.sr.infoHash.HexString()),
			slog.Uint64("readerID", c.sr.readerID),
			slog.Duration("idle", c.idle))
	}

	for _, sr := range toPark {
		sr.readahead = 0
		if sr.reader != nil {
			sr.reader.SetReadahead(0)
		}
		p.logger.Debug("parked idle reader",
			slog.String("hash", sr.infoHash.HexString()),
			slog.Uint64("readerID", sr.readerID),
			slog.Duration("effectiveParkTimeout", timeout))
	}

	// Rebalance readahead among active readers now that idle readers
	// have been parked (their readahead was set to 0).
	if (len(toPark) > 0 || len(toClose) > 0) && p.readaheadBudget > 0 {
		p.refreshReadaheadLocked(p.readaheadBudget)
	}
}
