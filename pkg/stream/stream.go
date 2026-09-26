// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
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
	// LingerTimeout is how long a released reader stays open, still reading
	// ahead, for its player's next request. Zero defaults to 30 seconds.
	// Memory pressure may shorten it.
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
	// PreloadReadyTTL is how long a ready preload whose file has not been read
	// stays cached, and how long a failed or evicted preload keeps reporting
	// its final state. A preload whose file was read is released once the
	// file has no reader left, including a lingering one, whatever the TTL.
	// Zero defaults to 5 minutes. Negative values keep unread and finished
	// preloads until they are replaced, cancelled, or evicted.
	PreloadReadyTTL time.Duration
	// PreloadStallTimeout is how long a running preload may go without
	// completing a piece before it fails, freeing its download slot and
	// memory, as on a torrent without peers. Zero defaults to 2 minutes.
	// Negative values let preloads run until they complete or are removed.
	PreloadStallTimeout time.Duration
	// ReadObserver, when set, is called after every Read of a reader with the
	// reader's storage mode and how long the Read took, including any wait
	// for torrent data. It runs on the reading goroutine, so it must return
	// quickly.
	ReadObserver func(mode StorageMode, duration time.Duration)
	// Registry tracks active read ranges for piece eviction protection.
	// Nil disables active-range tracking.
	Registry ActiveRangeRegistry
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
	// owners maps each claim owner, a *streamReader or a *preload, to the
	// priority it requests.
	owners map[any]torrent.PiecePriority
}

// readBufferSize is the size of a stream's read buffer. It lets the small
// reads of an HTTP response copy take fewer turns through the torrent reader.
const readBufferSize = 256 << 10

// streamReadSeeker is the io.ReadSeeker handed to one caller of Acquire. It
// buffers reads from its dedicated torrent reader and reports the caller's
// position to the pool, which moves the reader's eviction protection and piece
// priorities with it.
//
// A seek only records its destination. The torrent reader moves, and the
// destination is reported, when the next read starts, before that read can
// block on torrent data, so the torrent reader never follows positions that
// are not read, such as the end-of-file seek that http.ServeContent uses to
// learn the content size.
//
// Like other io.ReadSeekers, it is not safe for concurrent use. The pool may
// retire it concurrently, which waits for a read in progress.
//
// Lock ordering: mu is never held while acquiring pool.mu, so onPosition runs
// without it. The pool may hold pool.mu while retiring the stream.
type streamReadSeeker struct {
	buf    *bufio.Reader
	closed bool
	length int64
	mu     sync.Mutex
	// moved reports that the caller's position no longer matches the torrent
	// reader's, which moves on the next read.
	moved bool
	// observeRead, when set, receives the duration of each Read.
	observeRead func(time.Duration)
	// onPosition receives the caller's position after a seek and after each
	// read. The pool ignores reports for a released reader.
	onPosition func(int64)
	position   int64
	reader     io.ReadSeeker
	// seekPending reports a seek not yet followed by a read.
	seekPending bool
}

func newStreamReadSeeker(reader io.ReadSeeker, length int64, onPosition func(int64)) *streamReadSeeker {
	return &streamReadSeeker{
		buf:        bufio.NewReaderSize(reader, readBufferSize),
		length:     length,
		onPosition: onPosition,
		reader:     reader,
	}
}

// Read reads from the caller's position, first moving the torrent reader there
// after a seek.
func (s *streamReadSeeker) Read(p []byte) (int, error) {
	if s.observeRead != nil {
		start := time.Now()
		defer func() { s.observeRead(time.Since(start)) }()
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if s.seekPending {
		s.seekPending = false
		position := s.position
		s.mu.Unlock()
		s.reportPosition(position)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
	}
	if s.position >= s.length {
		s.mu.Unlock()
		return 0, io.EOF
	}
	if s.moved {
		if _, err := s.reader.Seek(s.position, io.SeekStart); err != nil {
			s.mu.Unlock()
			return 0, err
		}
		s.buf.Reset(s.reader)
		s.moved = false
	}
	n, err := s.buf.Read(p[:min(int64(len(p)), s.length-s.position)])
	s.position += int64(n)
	position := s.position
	s.mu.Unlock()

	if n > 0 {
		s.reportPosition(position)
	}
	return n, err
}

// Seek records the caller's new position, reported by the next read. A
// position past the end is allowed and reads as the end of the file.
func (s *streamReadSeeker) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += s.position
	case io.SeekEnd:
		offset += s.length
	default:
		return 0, errors.New("stream: invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("stream: negative position")
	}
	if offset != s.position {
		s.position = offset
		s.moved = true
	}
	s.seekPending = true
	return offset, nil
}

// reportPosition reports position to the pool. It must be called without
// s.mu held.
func (s *streamReadSeeker) reportPosition(position int64) {
	if s.onPosition != nil {
		s.onPosition(position)
	}
}

// retire detaches a released stream from its torrent reader, so a stale
// caller can neither read nor move a lingering reader, and frees its buffer.
// It waits for a read in progress. The pool closes the torrent reader itself.
// It is safe to call more than once.
func (s *streamReadSeeker) retire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.buf = nil
}

// streamReader wraps a torrent.Reader with lifecycle management.
type streamReader struct {
	active        bool
	cancel        context.CancelFunc
	file          *torrent.File
	infoHash      metainfo.Hash
	isFileStorage bool
	// lingerSince is when a released reader started lingering.
	lingerSince time.Time
	// lastOffset is the last byte offset the reader's stream reported.
	// Updated under pool.mu only, so code holding pool.mu can read it without
	// acquiring the stream's lock.
	lastOffset   int64
	lastPieceIdx int64
	// prioritizedPieces tracks the piece claims currently owned by this reader.
	// Pool-level ownership keeps overlapping readers from lowering each other's
	// priorities when one moves or releases.
	prioritizedPieces []int
	readahead         int64 // current readahead in bytes (updated by refreshReadaheadLocked and Acquire)
	reader            torrent.Reader
	readerID          uint64
	// stream is the caller's view of reader.
	stream *streamReadSeeker
}

// Pool manages torrent readers with dynamic readahead management.
// Callers must call Close() when the pool is no longer needed to stop the background
// lingering-reader goroutine and release reader resources.
type Pool struct {
	closeCh chan struct{}
	closed  bool
	cfg     Config
	logger  *slog.Logger
	mu      sync.Mutex
	nextID  uint64
	// priorityClaims holds every piece priority claimed by readers and
	// preloads, keyed by piece.
	priorityClaims map[priorityPieceKey]*priorityClaim
	// preloadQueue holds queued preloads in request order. Entries that are
	// no longer queued are skipped on dispatch.
	preloadQueue []*preload
	// preloadWatchers tracks the goroutines that follow running and ready
	// preloads, so Close can wait for them.
	preloadWatchers sync.WaitGroup
	// preloads holds each torrent's preload, including a failed or evicted
	// one that still reports its final state.
	preloads        map[metainfo.Hash]*preload
	readaheadBudget int64 // current total readahead budget, updated by SetReadaheadBudget
	readers         map[uint64]*streamReader
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
	if cfg.PreloadReadyTTL == 0 {
		cfg.PreloadReadyTTL = defaultPreloadReadyTTL
	}
	if cfg.PreloadStallTimeout == 0 {
		cfg.PreloadStallTimeout = defaultPreloadStallTimeout
	}

	p := &Pool{
		closeCh:        make(chan struct{}),
		cfg:            cfg,
		logger:         logger,
		priorityClaims: make(map[priorityPieceKey]*priorityClaim),
		preloads:       make(map[metainfo.Hash]*preload),
		readers:        make(map[uint64]*streamReader),
	}

	go p.idleGC()

	return p
}

// Acquire returns an io.ReadSeeker for reading the given file within a torrent
// with cancellation tied to ctx. The caller MUST call the returned release function
// (typically via defer) when done reading. The release function is safe to call
// multiple times. MemoryStorage readers share the budget configured by SetReadaheadBudget;
// FileStorage readers use Config.FileReadaheadBytes. Acquire returns an error
// for an invalid file or mode, or after the pool has been closed.
func (p *Pool) Acquire(ctx context.Context, file *torrent.File, mode StorageMode) (io.ReadSeeker, ReleaseFunc, error) {
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

	readerCtx, cancel := context.WithCancel(ctx)
	reader := file.NewReader()
	reader.SetContext(readerCtx)

	stream := newStreamReadSeeker(reader, file.Length(), func(position int64) {
		p.updateActiveRange(readerID, position)
	})
	if observe := p.cfg.ReadObserver; observe != nil {
		stream.observeRead = func(d time.Duration) { observe(mode, d) }
	}

	sr := &streamReader{
		active:        true,
		cancel:        cancel,
		file:          file,
		infoHash:      infoHash,
		isFileStorage: isFileStorage,
		lastPieceIdx:  -1,
		reader:        reader,
		readerID:      readerID,
		stream:        stream,
	}
	p.readers[readerID] = sr
	if pl := p.preloads[infoHash]; pl != nil && pl.file == file {
		pl.read = true
	}

	if isFileStorage {
		// File-storage pieces live on disk, so they need neither a share of
		// the budget nor eviction protection.
		sr.readahead = p.cfg.FileReadaheadBytes
		reader.SetReadahead(p.cfg.FileReadaheadBytes)
	} else {
		p.refreshReadaheadLocked()
	}
	// The new reader takes over from the file's lingering reader. It closes
	// only after the new reader has its window, so pieces both want keep a
	// reader's priority throughout.
	p.closeLingeringReadersLocked(file)

	p.logger.Debug("created new reader",
		slog.String("hash", infoHash.HexString()),
		slog.String("file", file.Path()),
		slog.Uint64("readerID", readerID),
		slog.Int64("readahead", sr.readahead))

	return stream, p.releaseFunc(readerID), nil
}

// releaseFunc returns an idempotent ReleaseFunc for the reader with readerID.
func (p *Pool) releaseFunc(readerID uint64) ReleaseFunc {
	var once sync.Once
	return func() {
		once.Do(func() { p.release(readerID) })
	}
}

// closeStreamReaderLocked cancels and closes a reader. Canceling first ends a
// read in progress, and retiring its stream then waits for that read. The caller must hold
// Pool.mu so no new pool-owned use can begin while closure is in progress.
func closeStreamReaderLocked(sr *streamReader) {
	if sr.cancel != nil {
		sr.cancel()
		sr.cancel = nil
	}
	if sr.stream != nil {
		sr.stream.retire()
	}
	if sr.reader != nil {
		_ = sr.reader.Close()
	}
	sr.reader = nil
}

// release ends a reader's lease. Called immediately after the HTTP request
// ends (via defer in streamFile).
//
// A released reader lingers: it stays open with its readahead, so the torrent
// client keeps fetching the pieces just past where the player stopped for the
// player's next range request, which creates a new reader of the file and
// closes the lingering one. A reader released while another reader of its file
// is active closes at once, so a file has at most one lingering reader.
// Other viewers' readers do not close it, because the engine is shared; it
// closes at the latest after LingerTimeout. Readers of a closed torrent close
// immediately.
//
// A lingering reader holds no eviction protection or priority claims, so its
// prefetched pieces compete with other cached pieces under memory pressure.
func (p *Pool) release(readerID uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sr, ok := p.readers[readerID]
	if !ok || !sr.active {
		return
	}

	sr.active = false
	if torrentClosed(sr.file) || p.fileHasActiveReaderLocked(sr.file) {
		p.removeReaderLocked(sr)
		p.rebalanceLocked()
		return
	}

	p.clearReaderClaimsLocked(sr)
	// The caller is done with its reader, so a stale call cannot move the
	// lingering reader's position.
	if sr.stream != nil {
		sr.stream.retire()
	}
	sr.lingerSince = time.Now()
	p.logger.Debug("reader lingering",
		slog.String("hash", sr.infoHash.HexString()),
		slog.Uint64("readerID", readerID))
	p.rebalanceLocked()
}

// rebalanceLocked redistributes the readahead budget after a reader stops
// being active. Without a budget there is nothing to redistribute, so active
// readers keep their readahead. Must be called with p.mu held.
func (p *Pool) rebalanceLocked() {
	if p.readaheadBudget > 0 {
		p.refreshReadaheadLocked()
	}
}

// fileHasActiveReaderLocked reports whether file has an active reader. Must
// be called with p.mu held.
func (p *Pool) fileHasActiveReaderLocked(file *torrent.File) bool {
	for _, sr := range p.readers {
		if sr.active && sr.file == file {
			return true
		}
	}
	return false
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

// clearReaderClaimsLocked releases every piece priority and eviction
// protection a reader holds. A priority update still in flight for the reader
// finds it inactive or removed and does nothing. Must be called with p.mu held.
func (p *Pool) clearReaderClaimsLocked(sr *streamReader) {
	p.unclaimLocked(sr, fileTorrent(sr.file), sr.prioritizedPieces)
	sr.prioritizedPieces = nil
	if p.cfg.Registry != nil {
		p.cfg.Registry.ClearActiveRange(sr.infoHash, sr.readerID)
		p.cfg.Registry.ClearFileBoundaries(sr.infoHash, sr.readerID)
	}
}

// removeReaderLocked releases a reader's claims, closes it, and removes it
// from the pool. Must be called with p.mu held.
func (p *Pool) removeReaderLocked(sr *streamReader) {
	p.clearReaderClaimsLocked(sr)
	closeStreamReaderLocked(sr)
	delete(p.readers, sr.readerID)
}

// refreshReadaheadLocked recalculates and applies readahead for all active readers.
// File-storage readers keep their fixed readahead and are never divided.
// Must be called with p.mu held.
func (p *Pool) refreshReadaheadLocked() {
	plan := p.planReadaheadLocked()

	for _, sr := range p.readers {
		if !sr.active {
			continue
		}
		// File-storage readers use the fixed value; do not overwrite.
		if sr.isFileStorage {
			continue
		}
		readahead := readaheadForShare(plan.share, filePieceLength(sr.file))
		sr.readahead = readahead
		if sr.reader != nil {
			sr.reader.SetReadahead(readahead)
		}
		p.registerActiveRangeLocked(sr, readahead, plan.boundaryBytes[sr.file])
	}

	p.logger.Debug("refreshed readahead",
		slog.Int64("totalPool", p.readaheadBudget),
		slog.Int64("perReaderShare", plan.share))
}

// closeLingeringReadersLocked closes file's lingering reader. A ready preload
// of the file stays while the file has another reader. Must be called with
// p.mu held.
func (p *Pool) closeLingeringReadersLocked(file *torrent.File) {
	closed := false
	for _, sr := range p.readers {
		if sr.active || sr.file != file {
			continue
		}
		p.removeReaderLocked(sr)
		closed = true
		p.logger.Debug("closed lingering reader for a newer reader of its file",
			slog.String("hash", sr.infoHash.HexString()),
			slog.Uint64("readerID", sr.readerID))
	}
	if closed {
		p.releaseReadPreloadsLocked()
	}
}

// SetReadaheadBudget recalculates readahead for all active memory-storage
// readers using the provided total budget. Negative budgets are treated as
// zero. File-storage readers keep their fixed readahead. When preload
// reservations exceed the preload share of the new budget, preloads are
// evicted until they fit, and a larger budget lets queued preloads start.
func (p *Pool) SetReadaheadBudget(budgetBytes int64) {
	if budgetBytes < 0 {
		budgetBytes = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readaheadBudget = budgetBytes
	p.shrinkPreloadsLocked()
	p.refreshReadaheadLocked()
	p.dispatchPreloadsLocked()
}

// readaheadPlan divides the protection budget among active memory-storage
// playback readers. Storage protects and evicts whole pieces, so the plan is
// sized in pieces rather than bytes.
type readaheadPlan struct {
	// boundaryBytes holds the head and tail size for each file that keeps
	// boundary protection. Files whose boundary pieces do not fit are absent.
	boundaryBytes map[*torrent.File]int64
	// share is each reader's protection budget in bytes after boundaries.
	share int64
}

// planReadaheadLocked divides the budget left after preload reservations. The
// budget is global, not per info hash. Boundary protection may use at most half
// of it; each file's boundaries shrink until their whole pieces fit, and are
// dropped when even one piece per boundary does not. A file whose held preload
// reservation covers both its head and tail gets no boundaries, because the
// preload already protects them within that reservation. The remainder is shared
// evenly by the readers. File-storage readers are excluded because they have
// their own readahead. Must be called with p.mu held.
func (p *Pool) planReadaheadLocked() readaheadPlan {
	available := max(p.readaheadBudget-p.reservedPreloadBytesLocked(), 0)
	activeCount := 0
	activeFiles := make(map[*torrent.File]struct{})
	for _, sr := range p.readers {
		if sr.active && !sr.isFileStorage {
			activeCount++
			if sr.file != nil && !p.preloadCoversFileLocked(sr.infoHash, sr.file) {
				activeFiles[sr.file] = struct{}{}
			}
		}
	}
	plan := readaheadPlan{
		boundaryBytes: make(map[*torrent.File]int64, len(activeFiles)),
		share:         available,
	}
	if activeCount < 1 {
		return plan
	}

	// Each file's head and tail get an even share of at most half the
	// available budget, and never more than a default boundary each.
	var allowance int64
	if len(activeFiles) > 0 {
		allowance = 2 * min(int64(defaultFileBoundaryBytes), available/(4*int64(len(activeFiles))))
	}
	var boundaryCost int64
	for file := range activeFiles {
		if boundaryBytes, cost, ok := fitFileBoundaries(file, allowance); ok {
			plan.boundaryBytes[file] = boundaryBytes
			boundaryCost += cost
		}
	}
	plan.share = max(available-boundaryCost, 0) / int64(activeCount)
	return plan
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
		if cost = boundaryPieceBytes(info, headStart, headEnd, tailStart, tailEnd); cost <= allowance {
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

const trailingReadaheadDivisor = 4 // trailing range is 1/4th of readahead pieces

// readerWindow returns the inclusive piece range a reader of file at byteOffset
// with the given readahead protects, and the piece it reads, all clamped to the
// file's pieces. The ahead range reaches the full readahead piece count to
// protect all pieces actively prefetched by the torrent client, matching
// reader.SetReadahead(readahead), and a trailing quarter of it stays behind.
// readaheadForShare sizes readahead so that the complete range fits the
// reader's share of the protection budget. It returns false without piece
// metadata or for a file that spans no piece.
func readerWindow(file *torrent.File, readahead, byteOffset int64) (start, position, end int, ok bool) {
	pieceLength := filePieceLength(file)
	if pieceLength <= 0 || file.EndPieceIndex() <= file.BeginPieceIndex() {
		return 0, 0, 0, false
	}
	readaheadPieces := max(readahead/pieceLength, 0)
	beginPiece := int64(file.BeginPieceIndex())
	// EndPieceIndex is exclusive; ActiveRangeRegistry endpoints are inclusive.
	endPieceMax := int64(file.EndPieceIndex()) - 1

	positionPiece := min(max(filePiece(file, byteOffset), beginPiece), endPieceMax)
	trailing := readaheadPieces / trailingReadaheadDivisor
	return int(max(positionPiece-trailing, beginPiece)), int(positionPiece), int(min(positionPiece+readaheadPieces, endPieceMax)), true
}

// filePiece returns the torrent piece holding the file-relative byteOffset.
// Without piece metadata it returns the byte offset itself, so a change of
// position still reads as a change of piece.
func filePiece(file *torrent.File, byteOffset int64) int64 {
	if file == nil {
		return byteOffset
	}
	return (file.Offset() + byteOffset) / max(filePieceLength(file), 1)
}

// defaultFileBoundaryBytes is the minimum amount of head and tail bytes (8 MiB)
// protected from eviction to preserve container metadata and seek tables.
const defaultFileBoundaryBytes = 8 << 20

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
	return filePieceRanges(file, min(length, startend), max(length-startend, 0), length)
}

// filePieceRanges returns the inclusive torrent piece ranges holding the
// file-relative byte ranges [0, headEnd) and [tailStart, tailEnd). An empty
// tail range repeats the head range. It returns false when the file has no
// piece metadata or the head range is empty.
func filePieceRanges(file *torrent.File, headEnd, tailStart, tailEnd int64) (int, int, int, int, bool) {
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

// boundaryPieceBytes returns the storage size of the pieces boundaryPieces
// yields. Storage protects and evicts whole pieces, so this is the memory the
// head and tail ranges occupy, including a shorter final piece at its real size.
func boundaryPieceBytes(info *metainfo.Info, headStart, headEnd, tailStart, tailEnd int) int64 {
	var total int64
	for index := range boundaryPieces(headStart, headEnd, tailStart, tailEnd) {
		total += info.Piece(index).Length()
	}
	return total
}

// boundaryPieces yields the pieces of the inclusive head and tail piece ranges
// in ascending order. The tail may repeat or overlap the head; pieces shared by
// both are yielded once.
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
// for a reader at its last reported position with the given readahead, and its
// file boundaries of boundaryBytes each. A non-positive boundaryBytes clears any
// boundaries the reader registered before. Must be called with p.mu held.
func (p *Pool) registerActiveRangeLocked(sr *streamReader, readahead, boundaryBytes int64) {
	file, infoHash, readerID := sr.file, sr.infoHash, sr.readerID
	if p.cfg.Registry == nil {
		return
	}
	start, _, end, ok := readerWindow(file, readahead, sr.lastOffset)
	if !ok {
		return
	}
	p.cfg.Registry.SetActiveRange(infoHash, readerID, start, end)
	if boundaryBytes <= 0 {
		p.cfg.Registry.ClearFileBoundaries(infoHash, readerID)
		return
	}
	if hs, he, ts, te, ok := computeFileBoundaries(file, boundaryBytes); ok {
		p.cfg.Registry.SetFileBoundaries(infoHash, readerID, hs, he, ts, te)
	}
}

// prioritizeNextPieces plans the PiecePriorityNow claims for the pieces just
// past the one a reader of file at byteOffset reads: the fraction of its
// readahead pieces, at least one, clamped to the file's last piece. Applying
// the plan is separate so priorities shared by overlapping readers are only
// lowered after their final owner releases them. It returns nil without piece
// metadata or with no readahead or fraction.
func prioritizeNextPieces(file *torrent.File, byteOffset, readahead int64, fraction float64) []prioritizedPiece {
	pieceLength := filePieceLength(file)
	if fraction <= 0 || readahead <= 0 || pieceLength <= 0 {
		return nil
	}
	count := max(int64(float64(max(readahead/pieceLength, 1))*min(fraction, 1)), 1)
	current := filePiece(file, byteOffset)
	end := min(current+1+count, int64(file.EndPieceIndex()))
	planned := make([]prioritizedPiece, 0, max(end-current-1, 0))
	for index := current + 1; index < end; index++ {
		planned = append(planned, prioritizedPiece{index: int(index), priority: torrent.PiecePriorityNow})
	}
	return planned
}

// fileTorrent returns file's torrent, or nil for a nil file.
func fileTorrent(file *torrent.File) *torrent.Torrent {
	if file == nil {
		return nil
	}
	return file.Torrent()
}

// claimLocked replaces the claims owner, a reader or a preload, holds on the
// owned pieces of tor with the planned claims, applies the highest priority
// still requested for every affected piece, and returns the owner's new
// claimed pieces, reusing owned. Must be called with p.mu held.
func (p *Pool) claimLocked(owner any, tor *torrent.Torrent, owned []int, planned []prioritizedPiece) []int {
	if tor == nil {
		return owned[:0]
	}
	touched := make(map[priorityPieceKey]struct{}, len(owned)+len(planned))
	for _, index := range owned {
		key := priorityPieceKey{torrent: tor, index: index}
		if claim := p.priorityClaims[key]; claim != nil {
			delete(claim.owners, owner)
		}
		touched[key] = struct{}{}
	}

	owned = owned[:0]
	for _, piece := range planned {
		key := priorityPieceKey{torrent: tor, index: piece.index}
		claim := p.priorityClaims[key]
		if claim == nil {
			claim = &priorityClaim{owners: make(map[any]torrent.PiecePriority)}
			p.priorityClaims[key] = claim
		}
		claim.owners[owner] = piece.priority
		owned = append(owned, piece.index)
		touched[key] = struct{}{}
	}

	for key := range touched {
		p.applyPriorityClaimLocked(key)
	}
	return owned
}

// unclaimLocked removes owner from every owned piece claim on tor and
// immediately reapplies the highest remaining owner priority. Keeping this
// separate from claimLocked avoids allocating a temporary touched-piece map
// on release and eviction paths. Must be called with p.mu held.
func (p *Pool) unclaimLocked(owner any, tor *torrent.Torrent, owned []int) {
	for _, index := range owned {
		key := priorityPieceKey{torrent: tor, index: index}
		if claim := p.priorityClaims[key]; claim != nil {
			delete(claim.owners, owner)
		}
		p.applyPriorityClaimLocked(key)
	}
}

// applyPriorityClaimLocked applies the highest remaining owner claim for a
// piece. Must be called with p.mu held.
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

// updateActiveRange recalculates and refreshes the eviction-protection
// window for a reader based on its new read offset. Called by the reader's
// stream when its position moves. Takes pool.mu to protect
// against concurrent release, Close, and lingering-reader closure.
//
// Piece-priority claims are applied by a goroutine, so the reading goroutine
// never waits on the torrent client's lock for them.
func (p *Pool) updateActiveRange(readerID uint64, newOffset int64) {
	p.mu.Lock()

	// Skip a reader that was already released or closed.
	sr := p.readers[readerID]
	if sr == nil || !sr.active {
		p.mu.Unlock()
		return
	}
	file := sr.file

	// Cache the offset on the streamReader so other pool.mu holders
	// (refreshReadaheadLocked, ReaderPositions) can read it without touching
	// the stream's lock, preserving the lock order.
	sr.lastOffset = newOffset
	currentPiece := filePiece(file, newOffset)
	pieceChanged := (currentPiece != sr.lastPieceIdx) || (sr.lastPieceIdx < 0)
	sr.lastPieceIdx = currentPiece

	if !pieceChanged {
		p.mu.Unlock()
		return
	}

	readahead := sr.readahead
	prioEnabled := p.cfg.PriorityWindowFraction > 0

	if !sr.isFileStorage && p.cfg.Registry != nil {
		if start, _, end, ok := readerWindow(file, readahead, newOffset); ok {
			p.cfg.Registry.SetActiveRange(sr.infoHash, readerID, start, end)
		}
	}
	p.mu.Unlock()

	if prioEnabled {
		go p.prioritizeAsync(readerID, currentPiece, file, newOffset, readahead)
	}
}

// prioritizeAsync claims the pieces just ahead of the reader with readerID after
// it reached piece. It does nothing when the reader was released or has since
// moved to another piece, whose own update then applies, so out-of-order
// goroutines cannot leave stale claims.
func (p *Pool) prioritizeAsync(readerID uint64, piece int64, file *torrent.File, newOffset, readahead int64) {
	planned := prioritizeNextPieces(file, newOffset, readahead, p.cfg.PriorityWindowFraction)

	p.mu.Lock()
	defer p.mu.Unlock()
	sr := p.readers[readerID]
	if sr == nil || !sr.active || sr.lastPieceIdx != piece {
		return
	}
	sr.prioritizedPieces = p.claimLocked(sr, fileTorrent(file), sr.prioritizedPieces, planned)
}

// ReaderPositions returns positions for all active and lingering readers belonging
// to the given info hash. The result order is unspecified.
func (p *Pool) ReaderPositions(infoHash metainfo.Hash) []ReaderPosition {
	p.mu.Lock()
	defer p.mu.Unlock()

	var result []ReaderPosition
	for _, sr := range p.readers {
		if sr.infoHash != infoHash {
			continue
		}
		start, position, end, ok := readerWindow(sr.file, sr.readahead, sr.lastOffset)
		if !ok {
			continue
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

// StreamingTorrentCount returns the number of torrents with an active or
// lingering reader, counting a torrent once however many of its files are
// being read.
func (p *Pool) StreamingTorrentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	torrents := make(map[metainfo.Hash]struct{})
	for _, sr := range p.readers {
		torrents[sr.infoHash] = struct{}{}
	}
	return len(torrents)
}

// Close shuts down the pool, closes all readers, and clears active ranges.
// It is safe to call multiple times.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.closeCh)

	for _, sr := range p.readers {
		p.removeReaderLocked(sr)
	}
	for infoHash := range p.preloads {
		p.removePreloadLocked(infoHash)
	}
	p.preloadQueue = nil
	p.mu.Unlock()

	// Watchers take p.mu to record progress, so wait for them after
	// releasing it.
	p.preloadWatchers.Wait()
	p.logger.Debug("stream pool closed")
}

// idleGC periodically closes readers that have lingered past the effective
// linger timeout and expires preloads.
func (p *Pool) idleGC() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.closeCh:
			return
		case <-ticker.C:
			p.closeExpiredLingeringReaders()
			p.expirePreloads()
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
	closed := false
	for _, sr := range p.readers {
		if sr.active {
			continue
		}
		lingered := now.Sub(sr.lingerSince)
		if lingered < timeout {
			continue
		}
		p.removeReaderLocked(sr)
		closed = true
		p.logger.Debug("closed lingering reader",
			slog.String("hash", sr.infoHash.HexString()),
			slog.Uint64("readerID", sr.readerID),
			slog.Duration("lingered", lingered))
	}
	if closed {
		p.releaseReadPreloadsLocked()
	}
}
