// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package storage

import (
	"container/list"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// ErrPieceNotAvailable is returned when reading or hashing a piece that has
// never been written or has been evicted.
var ErrPieceNotAvailable = errors.New("piece not available in memory")

// ErrPieceIncomplete is returned when reading or hashing piece bytes that have
// not been written, such as when an in-progress piece was evicted and only its
// later chunks were written to a fresh buffer. Reporting it as a storage error
// keeps the torrent client from blaming, and banning, the peers that sent the
// data.
var ErrPieceIncomplete = errors.New("piece data is incomplete in memory")

// ErrInsufficientMemory is returned when memory allocation fails even after attempting
// to evict existing pieces to free up space.
var ErrInsufficientMemory = errors.New("insufficient memory after eviction")

// ErrClientClosed is returned when an operation is attempted on a closed Client.
var ErrClientClosed = errors.New("storage client is closed")

// ErrTorrentClosed is returned when an operation is attempted through a
// TorrentImpl that has already been closed.
var ErrTorrentClosed = errors.New("storage torrent is closed")

// ErrTorrentNotManaged is returned when statistics are requested for a torrent
// that is not currently managed by the client.
var ErrTorrentNotManaged = errors.New("torrent is not managed by storage")

// ErrEvictionTargetNotReached is returned when protected pieces prevent a
// requested eviction target from being reached.
var ErrEvictionTargetNotReached = errors.New("eviction target not reached")

// Client implements the storage.Client interface from anacrolix/torrent.
type Client struct {
	// allocationCond wakes allocations that are temporarily blocked behind
	// unpublished buffers. It uses mu as its locker.
	allocationCond *sync.Cond
	// allocations tracks reservations until their buffers are published or refunded.
	allocations sync.WaitGroup
	// activeRanges tracks piece-index windows that readers are actively consuming.
	// Pieces inside these ranges are protected from standard LRU eviction, except
	// as a last resort under severe memory pressure (see emergency eviction).
	activeRanges map[activeRangeKey]activeRange
	// closeCh is closed when the client is fully shut down.
	closeCh chan struct{}
	// closed is protected by mu and rejects new work before closeCh is signaled.
	closed bool
	// evictedPending holds pieces evicted, or found missing when marked
	// complete, since the eviction handler last ran. It is protected by mu
	// and nil until a handler is registered.
	evictedPending map[pieceKey]struct{}
	// evictedSignal wakes the eviction notifier. It is nil until a handler is
	// registered.
	evictedSignal chan struct{}
	// fileBoundaries tracks head and tail piece ranges for media files being streamed
	// or preloaded, protecting container metadata from standard LRU eviction.
	fileBoundaries map[activeRangeKey]fileBoundary
	// lru is the global least-recently-used list for all pieces.
	lru       *list.List
	logger    *slog.Logger
	maxMemory int64
	mu        sync.RWMutex
	// notifier tracks the goroutine that runs the eviction handler.
	notifier sync.WaitGroup
	// pieces stores the metadata and data for each piece across all torrents.
	pieces map[pieceKey]*pieceData
	// pendingAllocations counts reservations whose buffers have not yet been
	// published or refunded. It is protected by mu.
	pendingAllocations int
	// torrents tracks the state for each torrent being managed.
	torrents map[metainfo.Hash]*torrentState
	// used is the total memory currently consumed by piece data.
	used int64
}

// MemoryStats contains global storage memory statistics.
type MemoryStats struct {
	// LimitBytes is the configured global memory limit in bytes.
	LimitBytes int64
	// TorrentsUsingMemory is the number of torrents currently consuming piece-buffer memory.
	TorrentsUsingMemory int
	// TrackedPieces is the number of piece records currently tracked across all torrents.
	// A record may temporarily represent an in-flight allocation whose data is not yet published.
	TrackedPieces int
	// UsedBytes is the number of bytes currently reserved for piece data.
	UsedBytes int64
}

// PieceStats describes a torrent piece currently tracked by the storage client.
type PieceStats struct {
	// Complete reports whether the piece is marked complete.
	Complete bool
	// Index is the absolute piece index within the torrent.
	Index int
	// Resident reports whether the piece data is resident in memory.
	Resident bool
	// SizeBytes is the expected piece size in bytes.
	SizeBytes int64
	// WrittenBytes is the number of bytes written into the resident piece buffer.
	WrittenBytes int64
}

// TorrentStats contains storage statistics for a managed torrent.
type TorrentStats struct {
	// CompletedBytes is the total expected size of tracked pieces marked complete.
	CompletedBytes int64
	// Global is the global memory snapshot captured with these torrent statistics.
	Global MemoryStats
	// Pieces contains all piece records currently tracked for the torrent.
	Pieces []PieceStats
	// ResidentBytes is the total resident piece-data size in bytes.
	ResidentBytes int64
	// ResidentPieces is the number of tracked pieces whose data is resident in memory.
	ResidentPieces int
	// TrackedBytes is the total expected size of all tracked pieces.
	TrackedBytes int64
	// TotalPieces is the torrent's total piece count from metadata.
	TotalPieces int
	// WrittenBytes is the total number of bytes written into resident piece buffers.
	WrittenBytes int64
}

// pieceKey is a unique identifier for a piece within a specific torrent.
type pieceKey struct {
	infoHash metainfo.Hash
	index    int
}

// pieceData holds the data and state for a single torrent piece.
type pieceData struct {
	allocDone     chan struct{} // Closed when the single in-flight allocation finishes.
	allocating    bool
	data          []byte        // The actual piece data, nil if not in memory.
	complete      bool          // True if the piece has been successfully downloaded and verified.
	evicted       bool          // True if the piece was evicted or unlinked from tracking.
	lruElem       *list.Element // Pointer to the piece's element in the global LRU list.
	lastTouchNano atomic.Int64  // Unix timestamp in nanoseconds of last LRU move.
	mu            sync.RWMutex
	pieceSize     int64 // The expected size of the piece.
	torrent       *torrentState
	writtenBytes  int64 // Unique bytes covered by writtenRanges.
	writtenRanges []byteRange
}

type byteRange struct {
	start int64
	end   int64
}

type evictionProtection uint8

// downloadingPieceGrace is how long after its last write an incomplete piece
// counts as still downloading, and so is spared while other pieces can be
// evicted instead. Writes refresh it at most once per touchMinInterval.
const downloadingPieceGrace = 30 * time.Second

const (
	protectActiveAndBoundaries evictionProtection = iota
	protectActiveOnly
	protectNone
)

// activeRangeKey uniquely identifies a reader's active range within a torrent.
type activeRangeKey struct {
	infoHash metainfo.Hash
	readerID uint64
}

// activeRange stores the piece-index window [startPiece, endPiece] (inclusive)
// that a reader is actively consuming, protecting those pieces from standard
// LRU eviction (see emergency eviction for the last-resort exception).
type activeRange struct {
	endPiece   int
	startPiece int
}

// fileBoundary stores the head and tail piece-index ranges [headStart, headEnd]
// and [tailStart, tailEnd] (inclusive) for a media file, protecting container
// metadata and seek indexes from standard LRU eviction throughout playback.
type fileBoundary struct {
	headEnd   int
	headStart int
	tailEnd   int
	tailStart int
}

// torrentState holds the state specific to a single torrent.
type torrentState struct {
	mu          sync.RWMutex
	openHandles int   // Number of live TorrentImpl handles; protected by Client.mu.
	pieceMemory int64 // Memory used by this torrent.
	totalPieces int   // Total number of pieces from torrent metadata.
}

type torrentHandle struct {
	closed    atomic.Bool
	closeErr  error
	closeOnce sync.Once
}

// New creates a storage client with the given memory limit in bytes.
// Negative limits are treated as zero. A nil logger uses slog.Default.
func New(maxMemory int64, logger *slog.Logger) *Client {
	if maxMemory < 0 {
		maxMemory = 0
	}
	if logger == nil {
		logger = slog.Default()
	}

	c := &Client{
		maxMemory:      maxMemory,
		pieces:         make(map[pieceKey]*pieceData),
		torrents:       make(map[metainfo.Hash]*torrentState),
		activeRanges:   make(map[activeRangeKey]activeRange),
		fileBoundaries: make(map[activeRangeKey]fileBoundary),
		lru:            list.New(),
		closeCh:        make(chan struct{}),
		logger:         logger,
	}
	c.allocationCond = sync.NewCond(&c.mu)
	return c
}

// Close stops the client and evicts all pieces from memory.
// It is safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.closeCh
		c.notifier.Wait()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// No new reservations can start after closed is set. Let existing owners
	// finish and refund their reservations before signaling shutdown.
	c.allocations.Wait()

	c.mu.Lock()

	// Clear all pieces.
	for key, pd := range c.pieces {
		c.evictPieceLocked(key, pd)
	}

	c.pieces = make(map[pieceKey]*pieceData)
	c.torrents = make(map[metainfo.Hash]*torrentState)
	c.activeRanges = make(map[activeRangeKey]activeRange)
	c.fileBoundaries = make(map[activeRangeKey]fileBoundary)
	c.lru.Init()
	c.used = 0
	close(c.closeCh)
	c.mu.Unlock()

	// The notifier may be running the handler, which can call back into the
	// client, so wait for it only after releasing mu.
	c.notifier.Wait()
	return nil
}

// Closed returns a receive-only channel that is closed when the client
// has completed all cleanup operations and is fully shut down.
func (c *Client) Closed() <-chan struct{} {
	return c.closeCh
}

// isClosed reports whether the client has been closed. c.mu must be held.
func (c *Client) isClosed() bool {
	return c.closed
}

// SetEvictionHandler registers handler to be told about every piece that
// eviction removes, and about every piece that is already gone when the
// torrent engine marks it complete. The engine caches piece completion, and
// marks a hashed piece complete even when storing that fails, so without this
// it keeps treating such pieces as downloaded: it skips them when reading
// ahead, and once every piece of a torrent has been downloaded it considers
// the torrent complete and may drop its peers. Pieces removed because their
// torrent closed are not reported. The handler runs on a background goroutine
// without any client lock held, so it may call back into the client.
// Notifications for the same piece are coalesced. Only the first handler is
// kept, and none is started after Close. Close stops the goroutine.
func (c *Client) SetEvictionHandler(handler func(infoHash metainfo.Hash, index int)) {
	if handler == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.evictedSignal != nil {
		return
	}
	c.evictedPending = make(map[pieceKey]struct{})
	c.evictedSignal = make(chan struct{}, 1)
	c.notifier.Go(func() { c.runEvictionNotifier(handler) })
}

// ClientEvictionHandler returns an eviction handler that makes client re-read
// the completion of each evicted piece, so it downloads the piece again when
// it is next needed.
func ClientEvictionHandler(client *torrent.Client) func(infoHash metainfo.Hash, index int) {
	return func(infoHash metainfo.Hash, index int) {
		to, ok := client.Torrent(infoHash)
		if !ok || to.Info() == nil || index < 0 || index >= to.NumPieces() {
			return
		}
		select {
		case <-to.Closed():
			return
		default:
		}
		to.Piece(index).UpdateCompletion()
	}
}

// runEvictionNotifier delivers pending eviction notifications to handler until
// the client closes.
func (c *Client) runEvictionNotifier(handler func(infoHash metainfo.Hash, index int)) {
	for {
		select {
		case <-c.closeCh:
			return
		case <-c.evictedSignal:
		}
		c.mu.Lock()
		pending := c.evictedPending
		c.evictedPending = make(map[pieceKey]struct{})
		c.mu.Unlock()
		for key := range pending {
			select {
			case <-c.closeCh:
				return
			default:
			}
			handler(key.infoHash, key.index)
		}
	}
}

// queueEvictionLocked records a piece for the eviction handler. c.mu must be
// held.
func (c *Client) queueEvictionLocked(key pieceKey) {
	if c.evictedSignal == nil {
		return
	}
	c.evictedPending[key] = struct{}{}
	select {
	case c.evictedSignal <- struct{}{}:
	default:
	}
}

// EvictTo evicts unprotected pieces until memory usage is at most targetBytes.
// It returns the number of bytes reclaimed. If active-range or file-boundary
// protection prevents reaching the target, it returns ErrEvictionTargetNotReached with the reclaimed
// byte count. Negative targets are treated as zero.
func (c *Client) EvictTo(targetBytes int64) (int64, error) {
	if targetBytes < 0 {
		targetBytes = 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClosed() {
		return 0, ErrClientClosed
	}

	before := c.used
	c.evictDownToLocked(targetBytes)
	reclaimed := before - c.used
	if c.used > targetBytes {
		return reclaimed, fmt.Errorf("%w: target=%d used=%d", ErrEvictionTargetNotReached, targetBytes, c.used)
	}
	return reclaimed, nil
}

// MemoryStats returns current global memory usage statistics.
func (c *Client) MemoryStats() MemoryStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.memoryStatsLocked()
}

// memoryStatsLocked returns a consistent global snapshot. c.mu must be held
// for reading or writing by the caller.
func (c *Client) memoryStatsLocked() MemoryStats {
	var activeTorrents int
	for _, state := range c.torrents {
		state.mu.RLock()
		if state.pieceMemory > 0 {
			activeTorrents++
		}
		state.mu.RUnlock()
	}

	return MemoryStats{
		LimitBytes:          c.maxMemory,
		TorrentsUsingMemory: activeTorrents,
		TrackedPieces:       len(c.pieces),
		UsedBytes:           c.used,
	}
}

// CompletedFraction returns the fraction of the torrent's total metadata pieces
// represented by tracked pieces marked complete, in [0, 1].
func (s TorrentStats) CompletedFraction() float64 {
	if s.TotalPieces <= 0 {
		return 0
	}
	completed := 0
	for _, piece := range s.Pieces {
		if piece.Complete {
			completed++
		}
	}
	return float64(completed) / float64(s.TotalPieces)
}

// MemoryUsageFraction returns the fraction of the global memory limit used by
// this torrent, in [0, 1].
func (s TorrentStats) MemoryUsageFraction() float64 {
	if s.Global.LimitBytes <= 0 {
		return 0
	}
	return float64(s.ResidentBytes) / float64(s.Global.LimitBytes)
}

// TorrentStats returns statistics for a managed torrent.
// The piece statistics include records created by WriteAt that remain tracked.
//
// The returned stats are eventually consistent rather than atomic: the global snapshot and
// piece-level fields are captured under separate lock acquisitions
// to avoid holding c.mu.RLock() for the duration of piece iteration (which could starve writers
// on torrents with thousands of pieces). Callers should treat the fields as a rough snapshot
// suitable for progress bars and UI displays, not for assertions like ResidentBytes <= UsedBytes.
//
// It returns an error if the torrent is not managed.
func (c *Client) TorrentStats(infoHash metainfo.Hash) (TorrentStats, error) {
	c.mu.RLock()

	// Check if torrent exists.
	state, exists := c.torrents[infoHash]
	if !exists {
		c.mu.RUnlock()
		return TorrentStats{}, fmt.Errorf("%w: %s", ErrTorrentNotManaged, infoHash)
	}

	// Read torrent state fields under its own lock for consistent discipline.
	state.mu.RLock()
	totalPieces := state.totalPieces
	state.mu.RUnlock()

	memoryStats := c.memoryStatsLocked()

	// Collect piece keys belonging to this torrent, then release the global lock
	// before reading per-piece data. This prevents write starvation from
	// allocateMemory, evictDownTo, touchPiece, and freeMemory.
	pieceKeys := make([]pieceKey, 0, totalPieces)
	for key := range c.pieces {
		if key.infoHash == infoHash {
			pieceKeys = append(pieceKeys, key)
		}
	}
	c.mu.RUnlock()

	stats := TorrentStats{
		Global:      memoryStats,
		Pieces:      make([]PieceStats, 0, len(pieceKeys)),
		TotalPieces: totalPieces,
	}

	// Acquire c.mu.RLock and pd.mu.RLock per-piece to read piece fields.
	// The pd pointer remains valid even if the piece was concurrently evicted
	// (Go's GC keeps the struct alive), and pd.mu.RLock() prevents a torn
	// read of that piece's fields — though the piece may already be evicted
	// by the time we read it, which is consistent with the eventually-consistent
	// semantics documented on this function.
	for _, key := range pieceKeys {
		c.mu.RLock()
		pd, ok := c.pieces[key]
		if !ok {
			c.mu.RUnlock()
			continue
		}
		c.mu.RUnlock()

		pd.mu.RLock()

		pieceStats := PieceStats{
			Index:        key.index,
			SizeBytes:    pd.pieceSize,
			Complete:     pd.complete,
			Resident:     pd.data != nil,
			WrittenBytes: pd.writtenBytes,
		}

		stats.Pieces = append(stats.Pieces, pieceStats)
		stats.TrackedBytes += pd.pieceSize

		if pd.complete {
			stats.CompletedBytes += pd.pieceSize
		}

		if pd.data != nil {
			stats.ResidentPieces++
			stats.ResidentBytes += int64(len(pd.data))
			stats.WrittenBytes += pd.writtenBytes
		}

		pd.mu.RUnlock()
	}

	// Sort pieces by index for consistent output.
	sort.Slice(stats.Pieces, func(i, j int) bool {
		return stats.Pieces[i].Index < stats.Pieces[j].Index
	})

	return stats, nil
}

// PiecesCached reports whether every listed piece of a managed torrent is
// resident in memory and marked complete. Unlike TorrentStats, it inspects only
// the listed pieces. It returns ErrTorrentNotManaged if the torrent is not
// managed.
func (c *Client) PiecesCached(infoHash metainfo.Hash, indexes []int) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state, exists := c.torrents[infoHash]
	if !exists {
		return false, fmt.Errorf("%w: %s", ErrTorrentNotManaged, infoHash)
	}
	for _, index := range indexes {
		pd, ok := c.pieces[pieceKey{infoHash: infoHash, index: index}]
		if !ok || pd.torrent != state {
			return false, nil
		}
		pd.mu.RLock()
		cached := pd.data != nil && pd.complete
		pd.mu.RUnlock()
		if !cached {
			return false, nil
		}
	}
	return true, nil
}

// OpenTorrent implements the storage.Client interface. It is called when a new
// torrent is added to the torrent client.
func (c *Client) OpenTorrent(_ context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		return storage.TorrentImpl{}, ErrClientClosed
	}

	pieceCount := info.NumPieces()

	// Initialize or update torrent state. Piece hashes are deliberately not
	// copied here: this backend indexes pieces by torrent and piece index, and
	// pure v2 torrents can legitimately omit the v1-only Info.Pieces field.
	state, exists := c.torrents[infoHash]
	if !exists {
		state = &torrentState{openHandles: 1, totalPieces: pieceCount}
		c.torrents[infoHash] = state
	} else {
		state.openHandles++
		state.mu.Lock()
		state.totalPieces = pieceCount
		state.mu.Unlock()
	}
	handle := &torrentHandle{}

	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &pieceImpl{
				client:    c,
				infoHash:  infoHash,
				index:     p.Index(),
				pieceSize: p.Length(),
				torrent:   state,
				handle:    handle,
			}
		},
		Close: func() error {
			handle.closeOnce.Do(func() {
				handle.closed.Store(true)
				handle.closeErr = c.closeTorrent(infoHash, state)
			})
			return handle.closeErr
		},
	}, nil
}

// SetMaxMemory updates the maximum memory limit for the storage client.
// If the new limit is lower than current usage, an eviction will be triggered
// to bring memory usage within the new limit. Negative values are clamped to 0.
//
// Unlike EvictTo (which respects active-range and file-boundary protections and returns
// ErrEvictionTargetNotReached if protected pieces prevent reaching the target),
// SetMaxMemory enforces an absolute process memory ceiling and will perform
// emergency eviction of protected pieces if necessary to bring memory usage
// within the configured limit.
//
// It returns ErrClientClosed after Close and ErrInsufficientMemory if the new
// limit cannot be enforced. This operation is thread-safe.
func (c *Client) SetMaxMemory(limitBytes int64) error {
	if limitBytes < 0 {
		limitBytes = 0
	}

	for {
		c.mu.Lock()
		if c.isClosed() {
			c.mu.Unlock()
			return ErrClientClosed
		}
		c.maxMemory = limitBytes

		// Trigger eviction if current usage exceeds the new limit.
		if c.used > limitBytes {
			c.evictDownToLocked(limitBytes)
			if c.used > limitBytes {
				c.emergencyEvictDownToLocked(limitBytes)
			}
		}

		used := c.used
		if used <= limitBytes {
			c.mu.Unlock()
			c.logger.Debug("updated memory limit",
				slog.Int64("newLimit", limitBytes),
				slog.Int64("currentUsed", used))
			return nil
		}

		// The only entries emergency eviction cannot release are in-flight
		// reservations whose data has not been published yet. Wait without c.mu
		// so their owners can commit or refund, then enforce the limit again.
		waiters := make([]<-chan struct{}, 0)
		for _, pd := range c.pieces {
			pd.mu.RLock()
			if pd.allocating {
				waiters = append(waiters, pd.allocDone)
			}
			pd.mu.RUnlock()
		}
		c.mu.Unlock()

		if len(waiters) == 0 {
			c.logger.Warn("unable to enforce updated memory limit",
				slog.Int64("newLimit", limitBytes),
				slog.Int64("currentUsed", used))
			return fmt.Errorf("%w: limit=%d used=%d", ErrInsufficientMemory, limitBytes, used)
		}
		for _, done := range waiters {
			<-done
		}
	}
}

// releaseMemoryLocked releases a reservation. c.mu must be held by the caller.
func (c *Client) releaseMemoryLocked(size int64, state *torrentState) {
	c.used -= size
	if c.used < 0 {
		c.used = 0
	}
	if state != nil {
		state.mu.Lock()
		state.pieceMemory -= size
		if state.pieceMemory < 0 {
			state.pieceMemory = 0
		}
		state.mu.Unlock()
	}
}

// allocateMemory reserves a given amount of memory for a piece. If the allocation
// would exceed the memory limit, it attempts to evict least-recently-used pieces
// to free up space. When eviction detaches an exact-size piece buffer, it returns
// that buffer directly to the incoming allocation for reuse. The returned buffer
// remains covered by the reservation added before this function returns.
func (c *Client) allocateMemory(size int64, infoHash metainfo.Hash, state *torrentState) ([]byte, error) {
	for {
		c.mu.Lock()

		if c.isClosed() {
			c.mu.Unlock()
			return nil, ErrClientClosed
		}
		if current, exists := c.torrents[infoHash]; !exists || current != state {
			c.mu.Unlock()
			return nil, ErrTorrentClosed
		}

		// Check if piece itself is larger than the total maxMemory limit.
		if size > c.maxMemory {
			c.mu.Unlock()
			return nil, ErrInsufficientMemory
		}

		var reusable []byte

		// Check if we need to evict.
		if c.used+size > c.maxMemory {
			target := max(c.maxMemory-size, 0)

			beforeEvict := c.used

			// Standard eviction preserves active playback ranges and file boundaries.
			reusable = c.evictDownToInternalLocked(target, protectActiveAndBoundaries, size)

			// Boundary metadata yields before active playback ranges. This keeps current
			// playback data protected when the combined leases exceed the cache budget.
			if c.used+size > c.maxMemory {
				reuseSize := size
				if reusable != nil {
					reuseSize = 0
				}
				if boundaryReusable := c.evictDownToInternalLocked(target, protectActiveOnly, reuseSize); reusable == nil {
					reusable = boundaryReusable
				}
			}

			// Only an absolute lack of non-active capacity may evict an active range.
			// An unpublished reservation will soon publish or refund its space, so
			// wait for it below rather than discard pieces a reader is consuming.
			if c.used+size > c.maxMemory && c.pendingAllocations == 0 {
				reuseSize := size
				if reusable != nil {
					reuseSize = 0
				}
				if emergencyReusable := c.evictDownToInternalLocked(target, protectNone, reuseSize); reusable == nil {
					reusable = emergencyReusable
				}
			}

			if c.logger.Enabled(context.Background(), slog.LevelDebug) {
				c.logger.Debug("memory allocation eviction",
					slog.Int64("needed", size),
					slog.Int64("before", beforeEvict),
					slog.Int64("after", c.used))
			}

			if c.used+size > c.maxMemory {
				// Unprotected pieces have already been exhausted, so an unpublished
				// reservation is the only memory that may be reclaimed without
				// evicting an active range. Wait for one to publish or refund instead
				// of surfacing a transient WriteAt error, which the torrent engine
				// treats as fatal.
				if c.pendingAllocations > 0 {
					c.allocationCond.Wait()
					c.mu.Unlock()
					continue
				}
				c.mu.Unlock()
				return nil, ErrInsufficientMemory
			}
		}

		// Reserve the memory. The reservation remains pending until its buffer
		// is published or refunded by completeAllocation.
		c.used += size
		state.mu.Lock()
		state.pieceMemory += size
		state.mu.Unlock()
		c.pendingAllocations++
		c.allocations.Add(1)
		c.mu.Unlock()

		return reusable, nil
	}
}

// completeAllocation finishes an unpublished reservation and wakes allocators
// that may now be able to evict its published buffer or use its refunded space.
func (c *Client) completeAllocation() {
	c.mu.Lock()
	c.pendingAllocations--
	c.allocations.Done()
	c.allocationCond.Broadcast()
	c.mu.Unlock()
}

// closeTorrent releases one TorrentImpl handle. The final handle removes all
// pieces associated with the torrent and cleans up its state.
func (c *Client) closeTorrent(infoHash metainfo.Hash, state *torrentState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if current, exists := c.torrents[infoHash]; !exists || current != state {
		return nil
	}
	state.openHandles--
	if state.openHandles > 0 {
		return nil
	}

	// Remove all pieces for this torrent.
	var totalEvicted int64
	for key, pd := range c.pieces {
		if key.infoHash != infoHash || pd.torrent != state {
			continue
		}
		// Read pd.data under the piece lock so len() is consistent
		// with any concurrent piece mutations (avoid races with
		// SetPiece/evict operations).
		pd.mu.RLock()
		size := int64(len(pd.data))
		pd.mu.RUnlock()
		c.evictPieceLocked(key, pd)
		totalEvicted += size
	}

	// Remove active ranges and file boundaries for this torrent.
	for key := range c.activeRanges {
		if key.infoHash == infoHash {
			delete(c.activeRanges, key)
		}
	}
	for key := range c.fileBoundaries {
		if key.infoHash == infoHash {
			delete(c.fileBoundaries, key)
		}
	}

	// Remove torrent state.
	delete(c.torrents, infoHash)

	c.logger.Debug("closed torrent",
		slog.String("hash", infoHash.HexString()),
		slog.Int64("evicted", totalEvicted))

	return nil
}

// evictDownToLocked evicts pieces from the LRU list until the total memory usage
// is at or below the target. It must be called with the client's mutex held.
// Pieces inside registered active ranges or file boundaries are skipped.
func (c *Client) evictDownToLocked(target int64) {
	c.evictDownToInternalLocked(target, protectActiveAndBoundaries, 0)
}

// emergencyEvictDownToLocked is called during allocateMemory when standard eviction
// could not free enough space because too many pieces are protected. It first
// evicts file boundary pieces, then the oldest LRU pieces regardless of active
// range, to prevent ErrInsufficientMemory
// from causing anacrolix/torrent to permanently disable downloading.
func (c *Client) emergencyEvictDownToLocked(target int64) {
	c.evictDownToInternalLocked(target, protectActiveOnly, 0)
	if c.used > target {
		c.evictDownToInternalLocked(target, protectNone, 0)
	}
}

// evictDownToInternalLocked evicts pieces until target is reached. If reuseSize
// is positive, at most one detached buffer of exactly that length is returned
// for immediate handoff to an incoming allocation. Pieces still downloading are
// spared on a first pass and evicted only if the target is otherwise out of
// reach, because an evicted partial piece must be downloaded again in full.
// c.mu must be held.
func (c *Client) evictDownToInternalLocked(target int64, protection evictionProtection, reuseSize int64) []byte {
	if c.used <= target {
		return nil
	}

	before := c.used
	downloadingSince := time.Now().Add(-downloadingPieceGrace).UnixNano()
	reusable := c.evictPassLocked(target, protection, reuseSize, downloadingSince)
	if c.used > target {
		if reusable != nil {
			reuseSize = 0
		}
		if downloadingReusable := c.evictPassLocked(target, protection, reuseSize, 0); reusable == nil {
			reusable = downloadingReusable
		}
	}

	if c.logger.Enabled(context.Background(), slog.LevelDebug) {
		msg := "eviction completed"
		if protection != protectActiveAndBoundaries {
			msg = "emergency eviction completed"
		}
		c.logger.Debug(msg,
			slog.Int64("target", target),
			slog.Int64("evicted", before-c.used),
			slog.Int64("newUsed", c.used))
	}

	return reusable
}

// evictPassLocked walks the LRU list from its least recently used end and
// evicts pieces allowed by protection until target is reached. Incomplete
// pieces touched after downloadingSince, in Unix nanoseconds, are skipped as
// still downloading; zero spares none. c.mu must be held.
func (c *Client) evictPassLocked(target int64, protection evictionProtection, reuseSize, downloadingSince int64) []byte {
	var reusable []byte
	for e := c.lru.Back(); e != nil && c.used > target; {
		key := e.Value.(pieceKey)
		next := e.Prev() // Save the next element before potential removal.

		if pd, ok := c.pieces[key]; ok {
			dataLen := len(pd.data)
			if dataLen == 0 {
				e = next
				continue
			}

			inActiveRange := c.isPieceInActiveRangeLocked(key)
			inFileBoundary := c.isPieceInFileBoundaryLocked(key)
			switch protection {
			case protectActiveAndBoundaries:
				if inActiveRange || inFileBoundary {
					e = next
					continue
				}
			case protectActiveOnly:
				if inActiveRange {
					e = next
					continue
				}
			case protectNone:
			}
			if downloadingSince > 0 && pd.lastTouchNano.Load() > downloadingSince {
				pd.mu.RLock()
				downloading := !pd.complete
				pd.mu.RUnlock()
				if downloading {
					e = next
					continue
				}
			}
			if protection == protectNone && c.logger.Enabled(context.Background(), slog.LevelWarn) && inActiveRange {
				c.logger.Warn("emergency eviction of active range piece under memory pressure",
					slog.String("hash", key.infoHash.HexString()),
					slog.Int("piece", key.index),
					slog.Int64("size", int64(dataLen)))
			}

			c.queueEvictionLocked(key)
			size := int64(dataLen)
			detached := c.evictPieceLocked(key, pd)
			if reusable == nil && size == reuseSize {
				reusable = detached
			}

			// Update torrent-specific memory usage.
			if state, exists := c.torrents[key.infoHash]; exists {
				state.mu.Lock()
				state.pieceMemory -= size
				state.mu.Unlock()
			}
		}

		e = next
	}
	return reusable
}

// recordWrittenRange adds [start, end) to the piece's sorted, disjoint written
// ranges, merging ranges it overlaps or touches. Ranges are updated in place,
// so sequential writes extend one range without allocating.
func (pd *pieceData) recordWrittenRange(start, end int64) {
	if start >= end {
		return
	}

	ranges := pd.writtenRanges
	// first is the first range that overlaps or touches the new one, and last
	// is one past the final such range.
	first := sort.Search(len(ranges), func(i int) bool { return ranges[i].end >= start })
	last := first
	for last < len(ranges) && ranges[last].start <= end {
		last++
	}
	if first == last {
		ranges = slices.Insert(ranges, first, byteRange{start: start, end: end})
	} else {
		ranges[first] = byteRange{
			start: min(start, ranges[first].start),
			end:   max(end, ranges[last-1].end),
		}
		ranges = slices.Delete(ranges, first+1, last)
	}

	pd.writtenRanges = ranges
	pd.writtenBytes = 0
	for _, current := range ranges {
		pd.writtenBytes += current.end - current.start
	}
}

// rangeWritten reports whether [start, end) lies within a single written
// range. Written ranges are merged when they touch, so a fully written span
// is always covered by one range. pd.mu must be held.
func (pd *pieceData) rangeWritten(start, end int64) bool {
	if start >= end {
		return true
	}
	i := sort.Search(len(pd.writtenRanges), func(i int) bool { return pd.writtenRanges[i].end > start })
	return i < len(pd.writtenRanges) && pd.writtenRanges[i].start <= start && pd.writtenRanges[i].end >= end
}

// isPieceInActiveRangeLocked checks whether a piece falls inside any registered
// active reader range for the given torrent hash. Must be called with c.mu held.
func (c *Client) isPieceInActiveRangeLocked(key pieceKey) bool {
	for k, r := range c.activeRanges {
		if k.infoHash == key.infoHash && key.index >= r.startPiece && key.index <= r.endPiece {
			return true
		}
	}
	return false
}

// isPieceInFileBoundaryLocked checks whether a piece falls inside any registered
// file boundary (head or tail range) for the given torrent hash. Must be called with c.mu held.
func (c *Client) isPieceInFileBoundaryLocked(key pieceKey) bool {
	for k, b := range c.fileBoundaries {
		if k.infoHash == key.infoHash {
			if (key.index >= b.headStart && key.index <= b.headEnd) ||
				(key.index >= b.tailStart && key.index <= b.tailEnd) {
				return true
			}
		}
	}
	return false
}

// SetActiveRange registers or refreshes an inclusive piece-index range [start, end]
// that a reader is actively consuming. Pieces inside this range are
// protected from standard LRU eviction, except as a last resort during emergency
// eviction under severe memory pressure (to prevent halting torrent downloads).
// readerID is a unique, caller-chosen identifier.
// start and end are absolute piece indices within the torrent.
func (c *Client) SetActiveRange(infoHash metainfo.Hash, readerID uint64, start, end int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only register ranges for torrents that are still managed. A late
	// callback from the stream pool can arrive after closeTorrent has
	// already deleted the torrent state; silently drop it to prevent
	// orphaned entries in the activeRanges map.
	if _, exists := c.torrents[infoHash]; !exists {
		return
	}

	c.activeRanges[activeRangeKey{infoHash: infoHash, readerID: readerID}] = activeRange{
		endPiece:   end,
		startPiece: start,
	}
}

// ClearActiveRange removes the active range for a specific reader, allowing its
// pieces to become eviction candidates again.
func (c *Client) ClearActiveRange(infoHash metainfo.Hash, readerID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.activeRanges, activeRangeKey{infoHash: infoHash, readerID: readerID})

	c.logger.Debug("cleared active range",
		slog.String("hash", infoHash.HexString()),
		slog.Uint64("readerID", readerID))
}

// SetFileBoundaries registers or updates the head and tail piece-index ranges
// for a file being streamed or preloaded, protecting those boundary pieces from standard
// LRU eviction throughout playback.
func (c *Client) SetFileBoundaries(infoHash metainfo.Hash, readerID uint64, headStart, headEnd, tailStart, tailEnd int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.torrents[infoHash]; !exists {
		return
	}

	c.fileBoundaries[activeRangeKey{infoHash: infoHash, readerID: readerID}] = fileBoundary{
		headEnd:   headEnd,
		headStart: headStart,
		tailEnd:   tailEnd,
		tailStart: tailStart,
	}
}

// ClearFileBoundaries removes the protected file boundaries for a specific reader.
func (c *Client) ClearFileBoundaries(infoHash metainfo.Hash, readerID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.fileBoundaries, activeRangeKey{infoHash: infoHash, readerID: readerID})

	c.logger.Debug("cleared file boundaries",
		slog.String("hash", infoHash.HexString()),
		slog.Uint64("readerID", readerID))
}

// evictPieceLocked removes a piece's data from memory and the LRU list and
// returns the detached data buffer. Callers may discard it or immediately hand
// it to another fully-accounted allocation.
// It must be called with the client's mutex held. Acquires pd.mu to safely
// nil out the data slice, preventing a data race with concurrent ReadAt calls.
func (c *Client) evictPieceLocked(key pieceKey, pd *pieceData) []byte {
	pd.mu.Lock()
	pd.evicted = true
	data := pd.data
	if pd.data != nil {
		size := int64(len(pd.data))
		c.used -= size
		if c.used < 0 {
			c.used = 0
		}
		pd.data = nil
	}
	pd.mu.Unlock()

	if pd.lruElem != nil {
		c.lru.Remove(pd.lruElem)
		pd.lruElem = nil
	}
	delete(c.pieces, key)

	if c.logger.Enabled(context.Background(), slog.LevelDebug) {
		c.logger.Debug("evicted piece",
			slog.String("hash", key.infoHash.HexString()),
			slog.Int("piece", key.index))
	}

	return data
}

// pieceImpl implements the storage.PieceImpl interface.
type pieceImpl struct {
	client    *Client
	handle    *torrentHandle
	infoHash  metainfo.Hash
	index     int
	pieceSize int64
	torrent   *torrentState
}

// Completion implements the storage.PieceImpl interface.
// It returns Ok: true so the torrent engine knows the status definitively without
// halting downloads with an Err, and Complete: pd.complete (or false if untracked/evicted).
func (p *pieceImpl) Completion() storage.Completion {
	pd, err := p.getPieceData()
	if err != nil {
		// Piece was evicted or never existed: report as known-incomplete (Complete: false, Ok: true)
		// so anacrolix/torrent removes it from the completed-pieces bitmap and allows downloading
		// without triggering an error or halting the torrent.
		return storage.Completion{Complete: false, Ok: true}
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	// Both untracked pieces (above) and in-flight/incomplete pieces (pd.complete == false)
	// correctly report Complete: false with Ok: true.
	return storage.Completion{
		Complete: pd.complete,
		Ok:       true,
	}
}

// MarkComplete implements the storage.PieceImpl interface.
func (p *pieceImpl) MarkComplete() error {
	pd, err := p.getPieceData()
	if err != nil {
		if errors.Is(err, ErrPieceNotAvailable) {
			p.reportMissingOnComplete()
		}
		return err
	}

	pd.mu.Lock()
	// Only mark as complete if we have the data in memory.
	if pd.data == nil {
		// Queueing needs c.mu, which must never be taken while holding pd.mu.
		pd.mu.Unlock()
		p.reportMissingOnComplete()
		return errors.New("cannot mark incomplete piece as complete without data")
	}
	pd.complete = true
	pd.mu.Unlock()

	return nil
}

// reportMissingOnComplete queues a piece that was evicted between hashing and
// being marked complete. The torrent engine marks it complete regardless, so
// the eviction handler must make it re-read the completion. c.mu and pd.mu
// must not be held.
func (p *pieceImpl) reportMissingOnComplete() {
	c := p.client
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.torrents[p.infoHash]; !exists || current != p.torrent {
		return
	}
	c.queueEvictionLocked(p.key())
}

// MarkNotComplete implements the storage.PieceImpl interface.
func (p *pieceImpl) MarkNotComplete() error {
	pd, err := p.getPieceData()
	if err != nil {
		// If the piece isn't available, it's already effectively not complete.
		if errors.Is(err, ErrPieceNotAvailable) {
			return nil
		}
		return err
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	pd.complete = false

	return nil
}

// ReadAt implements the storage.PieceImpl interface.
func (p *pieceImpl) ReadAt(b []byte, off int64) (n int, err error) {
	pd, err := p.getPieceData()
	if err != nil {
		return 0, ErrPieceNotAvailable
	}

	pd.mu.RLock()

	// Check if piece data is available in memory.
	if pd.data == nil {
		pd.mu.RUnlock()
		return 0, ErrPieceNotAvailable
	}

	// Boundary checks.
	if off < 0 || off >= p.pieceSize {
		pd.mu.RUnlock()
		p.touchPiece(pd)
		return 0, io.EOF
	}
	remaining := p.pieceSize - off
	if int64(len(b)) > remaining {
		b = b[:remaining]
		err = io.EOF
	}

	// Ensure we don't read beyond the actual data buffer.
	end := off + int64(len(b))
	if end > int64(len(pd.data)) {
		end = int64(len(pd.data))
		b = b[:end-off]
		if err == nil { // Don't overwrite a previous io.EOF
			err = io.EOF
		}
	}

	// Bytes never written since the buffer was allocated, such as chunks
	// lost when an in-progress piece was evicted, read as zeros. Refuse them
	// so no caller mistakes them for piece data.
	if !pd.rangeWritten(off, end) {
		pd.mu.RUnlock()
		return 0, ErrPieceIncomplete
	}

	n = copy(b, pd.data[off:end])
	if n < len(b) && err == nil {
		err = io.EOF // Signal that not all requested bytes were returned.
	}

	pd.mu.RUnlock()
	p.touchPiece(pd)

	return n, err
}

// SelfHash implements the storage.SelfHashing interface, computing the SHA-1 hash
// of the piece data in memory. It returns ErrPieceIncomplete unless every byte
// of the current buffer has been written.
func (p *pieceImpl) SelfHash() (metainfo.Hash, error) {
	pd, err := p.getPieceData()
	if err != nil {
		return metainfo.Hash{}, ErrPieceNotAvailable
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	if pd.data == nil {
		return metainfo.Hash{}, ErrPieceNotAvailable
	}
	// Chunks lost to eviction would hash as zeros and look like corrupt peer
	// data. anacrolix bans the sole contributor of a piece that fails its
	// hash without a storage error, so report the gap as one instead.
	if pd.writtenBytes < int64(len(pd.data)) {
		return metainfo.Hash{}, ErrPieceIncomplete
	}

	// Compute the SHA-1 hash.
	digest := sha1.Sum(pd.data)
	var result metainfo.Hash
	copy(result[:], digest[:])

	return result, nil
}

const maxWriteAllocRetries = 3

// WriteAt implements the storage.PieceImpl interface.
func (p *pieceImpl) WriteAt(b []byte, off int64) (n int, err error) {
	var lastErr error
	for range maxWriteAllocRetries {
		pd, getErr := p.getOrCreatePieceData()
		if getErr != nil {
			return 0, getErr
		}

		// Ensure data is allocated. This function will handle locking.
		if err := p.ensureDataAllocated(pd); err != nil {
			lastErr = err
			if errors.Is(err, ErrPieceNotAvailable) {
				// pd was evicted/unlinked while allocation was in flight.
				// Retry with a fresh piece from getOrCreatePieceData.
				continue
			}
			return 0, err
		}

		pd.mu.Lock()

		// Detect if piece data was evicted between ensureDataAllocated and pd.mu.Lock().
		if pd.data == nil || pd.evicted {
			lastErr = ErrPieceNotAvailable
			pd.mu.Unlock()
			continue
		}
		if p.handle.closed.Load() {
			pd.mu.Unlock()
			return 0, ErrTorrentClosed
		}

		// Boundary checks.
		if off < 0 || off > p.pieceSize {
			pd.mu.Unlock()
			return 0, errors.New("offset out of piece bounds")
		}
		if int64(len(b)) > p.pieceSize-off {
			pd.mu.Unlock()
			return 0, io.ErrShortWrite
		}
		if off+int64(len(b)) > int64(len(pd.data)) {
			pd.mu.Unlock()
			return 0, io.ErrShortWrite
		}

		copy(pd.data[off:], b)
		n = len(b)
		pd.recordWrittenRange(off, off+int64(n))
		pd.mu.Unlock()

		p.touchPiece(pd)

		return n, nil
	}

	if lastErr != nil {
		return 0, lastErr
	}
	return 0, ErrInsufficientMemory
}

// ensureDataAllocated makes sure that the piece's data slice is allocated. A
// single goroutine owns the allocation while concurrent writers wait for it.
func (p *pieceImpl) ensureDataAllocated(pd *pieceData) error {
	for {
		pd.mu.Lock()
		switch {
		case pd.evicted:
			pd.mu.Unlock()
			return ErrPieceNotAvailable
		case pd.data != nil:
			pd.mu.Unlock()
			return nil
		case pd.allocating:
			done := pd.allocDone
			pd.mu.Unlock()
			<-done
			continue
		default:
			pd.allocating = true
			pd.allocDone = make(chan struct{})
			pd.mu.Unlock()
		}
		break
	}

	data, err := p.client.allocateMemory(pd.pieceSize, p.infoHash, p.torrent)
	if err != nil {
		p.cleanupEmptyPiece(pd)
		return err
	}
	defer p.client.completeAllocation()

	if data == nil {
		data = make([]byte, pd.pieceSize)
	} else {
		// Writes may cover only part of a piece. Clear recycled bytes before
		// publication so unwritten regions never expose the evicted piece.
		clear(data)
	}
	return p.commitPieceAllocation(pd, data)
}

// commitPieceAllocation publishes allocated data and revalidates the memory
// limit in case SetMaxMemory ran after the reservation was made.
func (p *pieceImpl) commitPieceAllocation(pd *pieceData, data []byte) error {
	c := p.client
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		c.releaseMemoryLocked(pd.pieceSize, p.torrent)
		p.finishFailedAllocationLocked(pd)
		return ErrClientClosed
	}
	if p.handle.closed.Load() {
		c.releaseMemoryLocked(pd.pieceSize, p.torrent)
		p.finishFailedAllocationLocked(pd)
		return ErrTorrentClosed
	}
	if current, exists := c.torrents[p.infoHash]; !exists || current != p.torrent {
		c.releaseMemoryLocked(pd.pieceSize, p.torrent)
		p.finishFailedAllocationLocked(pd)
		return ErrTorrentClosed
	}

	if c.used > c.maxMemory {
		c.evictDownToLocked(c.maxMemory)
		if c.used > c.maxMemory {
			c.emergencyEvictDownToLocked(c.maxMemory)
		}
	}
	if c.used > c.maxMemory {
		c.releaseMemoryLocked(pd.pieceSize, p.torrent)
		p.finishFailedAllocationLocked(pd)
		return ErrInsufficientMemory
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()
	if pd.evicted || c.pieces[p.key()] != pd {
		c.releaseMemoryLocked(pd.pieceSize, p.torrent)
		p.finishAllocationLocked(pd)
		return ErrPieceNotAvailable
	}
	pd.data = data
	p.finishAllocationLocked(pd)
	return nil
}

// finishAllocationLocked wakes writers waiting for the current allocation.
// pd.mu must be held.
func (p *pieceImpl) finishAllocationLocked(pd *pieceData) {
	if pd.allocating {
		pd.allocating = false
		close(pd.allocDone)
		pd.allocDone = nil
	}
}

// finishFailedAllocationLocked unlinks an empty piece and wakes waiters.
// c.mu must be held; this function acquires pd.mu.
func (p *pieceImpl) finishFailedAllocationLocked(pd *pieceData) {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	if existing, ok := p.client.pieces[p.key()]; ok && existing == pd && pd.data == nil {
		pd.evicted = true
		if pd.lruElem != nil {
			p.client.lru.Remove(pd.lruElem)
			pd.lruElem = nil
		}
		delete(p.client.pieces, p.key())
	}
	p.finishAllocationLocked(pd)
}

// cleanupEmptyPiece removes a piece entry from tracking if its data was never
// successfully allocated (e.g. allocateMemory failed). Must be called without locks held.
func (p *pieceImpl) cleanupEmptyPiece(pd *pieceData) {
	p.client.mu.Lock()
	defer p.client.mu.Unlock()
	p.finishFailedAllocationLocked(pd)
}

// getOrCreatePieceData retrieves the pieceData for a piece, creating it if it doesn't exist.
// This ensures that piece metadata is tracked as soon as it's accessed.
func (p *pieceImpl) getOrCreatePieceData() (*pieceData, error) {
	p.client.mu.Lock()
	defer p.client.mu.Unlock()

	if p.client.isClosed() {
		return nil, ErrClientClosed
	}
	if p.handle.closed.Load() {
		return nil, ErrTorrentClosed
	}
	if current, exists := p.client.torrents[p.infoHash]; !exists || current != p.torrent {
		return nil, ErrTorrentClosed
	}

	key := p.key()

	// Return the piece if it already exists.
	if pd, ok := p.client.pieces[key]; ok {
		return pd, nil
	}

	// Create and register new piece data.
	pd := &pieceData{
		pieceSize: p.pieceSize,
		torrent:   p.torrent,
	}
	pd.lastTouchNano.Store(time.Now().UnixNano())

	p.client.pieces[key] = pd
	pd.lruElem = p.client.lru.PushFront(key)

	return pd, nil
}

// getPieceData retrieves the pieceData for a piece, returning ErrPieceNotAvailable
// if it does not exist in memory.
func (p *pieceImpl) getPieceData() (*pieceData, error) {
	p.client.mu.RLock()
	defer p.client.mu.RUnlock()
	if p.handle.closed.Load() {
		return nil, ErrTorrentClosed
	}
	if current, exists := p.client.torrents[p.infoHash]; !exists || current != p.torrent {
		return nil, ErrTorrentClosed
	}

	key := p.key()

	if pd, ok := p.client.pieces[key]; ok && pd.torrent == p.torrent {
		return pd, nil
	}

	return nil, ErrPieceNotAvailable
}

// key generates the unique pieceKey for the current piece.
func (p *pieceImpl) key() pieceKey {
	return pieceKey{infoHash: p.infoHash, index: p.index}
}

const touchMinInterval = int64(time.Second)

// touchPiece moves a piece to the front of the LRU list, marking it as
// recently used.
func (p *pieceImpl) touchPiece(pd *pieceData) {
	if pd == nil {
		return
	}

	pd.mu.RLock()
	if pd.evicted {
		pd.mu.RUnlock()
		return
	}
	pd.mu.RUnlock()

	now := time.Now().UnixNano()
	last := pd.lastTouchNano.Load()
	if now >= last && now-last < touchMinInterval {
		return
	}

	if pd.lastTouchNano.CompareAndSwap(last, now) {
		p.client.mu.Lock()
		if pd.lruElem != nil {
			p.client.lru.MoveToFront(pd.lruElem)
		}
		p.client.mu.Unlock()
	}
}
