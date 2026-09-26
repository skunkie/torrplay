// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package storage

import (
	"cmp"
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

// Client implements the storage.Client interface from anacrolix/torrent.
type Client struct {
	// allocationCond wakes allocations that are temporarily blocked behind
	// unpublished buffers. It uses mu as its locker.
	allocationCond *sync.Cond
	// allocations tracks reservations until their buffers are published or refunded.
	allocations sync.WaitGroup
	// closeCh is closed when the client is fully shut down.
	closeCh chan struct{}
	// counters accumulates storage events for Counters.
	counters clientCounters
	// closed is protected by mu and rejects new work before closeCh is signaled.
	closed bool
	// evictedPending holds pieces evicted, or found missing when marked
	// complete, since the eviction handler last ran. It is protected by mu
	// and nil until a handler is registered.
	evictedPending map[pieceKey]struct{}
	// evictedSignal wakes the eviction notifier. It is nil until a handler is
	// registered.
	evictedSignal chan struct{}
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
	// protections holds the piece ranges each owner, such as a stream reader
	// or a preload, protects from eviction.
	protections map[protectionKey]Protection
	// torrents tracks the state for each torrent being managed.
	torrents map[metainfo.Hash]*torrentState
	// used is the total memory currently consumed by piece data.
	used int64
}

// Counters holds cumulative storage event counts since the client was
// created. Pieces removed because their torrent or the client closed are not
// counted as evictions.
type Counters struct {
	// ActiveRangeEvictions counts pieces evicted despite active-range
	// protection, as a last resort under memory pressure.
	ActiveRangeEvictions int64
	// BoundaryEvictions counts pieces evicted despite file-boundary
	// protection.
	BoundaryEvictions int64
	// CompletionMisses counts pieces already gone when the torrent engine
	// marked them complete.
	CompletionMisses int64
	// EvictedCompletePieces counts evicted pieces that were marked complete.
	EvictedCompletePieces int64
	// EvictedIncompleteBytes counts bytes already written into incomplete
	// pieces when they were evicted, which must be downloaded again.
	EvictedIncompleteBytes int64
	// EvictedIncompletePieces counts evicted pieces not yet marked complete,
	// including fully written pieces still awaiting their hash check.
	EvictedIncompletePieces int64
	// IncompleteHashes counts self-hashes refused because chunks were lost to
	// eviction.
	IncompleteHashes int64
	// IncompleteReads counts reads refused because they covered bytes that
	// were never written.
	IncompleteReads int64
	// ReadMisses counts reads of pieces that were no longer in memory.
	ReadMisses int64
}

// Add returns the field-wise sum of c and other.
func (c Counters) Add(other Counters) Counters {
	return Counters{
		ActiveRangeEvictions:    c.ActiveRangeEvictions + other.ActiveRangeEvictions,
		BoundaryEvictions:       c.BoundaryEvictions + other.BoundaryEvictions,
		CompletionMisses:        c.CompletionMisses + other.CompletionMisses,
		EvictedCompletePieces:   c.EvictedCompletePieces + other.EvictedCompletePieces,
		EvictedIncompleteBytes:  c.EvictedIncompleteBytes + other.EvictedIncompleteBytes,
		EvictedIncompletePieces: c.EvictedIncompletePieces + other.EvictedIncompletePieces,
		IncompleteHashes:        c.IncompleteHashes + other.IncompleteHashes,
		IncompleteReads:         c.IncompleteReads + other.IncompleteReads,
		ReadMisses:              c.ReadMisses + other.ReadMisses,
	}
}

// clientCounters is the lock-free backing store for Counters.
type clientCounters struct {
	activeRangeEvictions    atomic.Int64
	boundaryEvictions       atomic.Int64
	completionMisses        atomic.Int64
	evictedCompletePieces   atomic.Int64
	evictedIncompleteBytes  atomic.Int64
	evictedIncompletePieces atomic.Int64
	incompleteHashes        atomic.Int64
	incompleteReads         atomic.Int64
	readMisses              atomic.Int64
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

// protectionKey identifies one owner's protection within a torrent.
type protectionKey struct {
	infoHash metainfo.Hash
	ownerID  uint64
}

// PieceRange is an inclusive range of torrent piece indexes.
type PieceRange struct {
	End   int
	Start int
}

// contains reports whether the range holds piece index.
func (r PieceRange) contains(index int) bool {
	return index >= r.Start && index <= r.End
}

// Protection holds the piece ranges one owner protects from standard LRU
// eviction.
type Protection struct {
	// Active holds ranges being read or preloaded. Their pieces are evicted
	// only as a last resort under severe memory pressure.
	Active []PieceRange
	// Boundaries holds the head and tail ranges of a streamed file, which keep
	// container metadata such as MP4 moov atoms or Matroska cues resident.
	// Their pieces are evicted before those of active ranges.
	Boundaries []PieceRange
}

// torrentState holds the state specific to a single torrent. Client.mu
// guards its fields.
type torrentState struct {
	openHandles int   // Number of live TorrentImpl handles.
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
		maxMemory:   maxMemory,
		pieces:      make(map[pieceKey]*pieceData),
		torrents:    make(map[metainfo.Hash]*torrentState),
		protections: make(map[protectionKey]Protection),
		lru:         list.New(),
		closeCh:     make(chan struct{}),
		logger:      logger,
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
	c.protections = make(map[protectionKey]Protection)
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

// Counters returns a snapshot of the cumulative storage event counts.
func (c *Client) Counters() Counters {
	return Counters{
		ActiveRangeEvictions:    c.counters.activeRangeEvictions.Load(),
		BoundaryEvictions:       c.counters.boundaryEvictions.Load(),
		CompletionMisses:        c.counters.completionMisses.Load(),
		EvictedCompletePieces:   c.counters.evictedCompletePieces.Load(),
		EvictedIncompleteBytes:  c.counters.evictedIncompleteBytes.Load(),
		EvictedIncompletePieces: c.counters.evictedIncompletePieces.Load(),
		IncompleteHashes:        c.counters.incompleteHashes.Load(),
		IncompleteReads:         c.counters.incompleteReads.Load(),
		ReadMisses:              c.counters.readMisses.Load(),
	}
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
		if state.pieceMemory > 0 {
			activeTorrents++
		}
	}

	return MemoryStats{
		LimitBytes:          c.maxMemory,
		TorrentsUsingMemory: activeTorrents,
		TrackedPieces:       len(c.pieces),
		UsedBytes:           c.used,
	}
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

	totalPieces := state.totalPieces

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
	slices.SortFunc(stats.Pieces, func(a, b PieceStats) int {
		return cmp.Compare(a.Index, b.Index)
	})

	return stats, nil
}

// OpenTorrent implements the storage.Client interface. It is called when a new
// torrent is added to the torrent client.
func (c *Client) OpenTorrent(_ context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
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
		state.totalPieces = pieceCount
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
// The limit is an absolute ceiling: protected pieces are evicted too if that
// is needed to enforce it.
//
// It returns ErrClientClosed after Close and ErrInsufficientMemory if the new
// limit cannot be enforced. This operation is thread-safe.
func (c *Client) SetMaxMemory(limitBytes int64) error {
	if limitBytes < 0 {
		limitBytes = 0
	}

	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return ErrClientClosed
		}
		c.maxMemory = limitBytes

		// Trigger eviction if current usage exceeds the new limit.
		c.evictLocked(limitBytes, protectNone, 0)

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
	c.used = max(c.used-size, 0)
	if state != nil {
		state.pieceMemory = max(state.pieceMemory-size, 0)
	}
}

// torrentOpenErrLocked returns ErrClientClosed after Close, ErrTorrentClosed
// when state is no longer the managed state of infoHash, and nil otherwise.
// c.mu must be held.
func (c *Client) torrentOpenErrLocked(infoHash metainfo.Hash, state *torrentState) error {
	if c.closed {
		return ErrClientClosed
	}
	if current, exists := c.torrents[infoHash]; !exists || current != state {
		return ErrTorrentClosed
	}
	return nil
}

// allocateMemory reserves a given amount of memory for a piece. If the allocation
// would exceed the memory limit, it attempts to evict least-recently-used pieces
// to free up space. When eviction detaches an exact-size piece buffer, it returns
// that buffer directly to the incoming allocation for reuse. The returned buffer
// remains covered by the reservation added before this function returns.
func (c *Client) allocateMemory(size int64, infoHash metainfo.Hash, state *torrentState) ([]byte, error) {
	for {
		c.mu.Lock()

		if err := c.torrentOpenErrLocked(infoHash, state); err != nil {
			c.mu.Unlock()
			return nil, err
		}

		// Check if piece itself is larger than the total maxMemory limit.
		if size > c.maxMemory {
			c.mu.Unlock()
			return nil, ErrInsufficientMemory
		}

		var reusable []byte
		if c.used+size > c.maxMemory {
			// Only an absolute lack of other capacity may evict an active
			// range. An unpublished reservation will soon publish or refund
			// its space, so wait for it below rather than discard pieces a
			// reader is consuming.
			evictUpTo := protectActiveOnly
			if c.pendingAllocations == 0 {
				evictUpTo = protectNone
			}
			reusable = c.evictLocked(max(c.maxMemory-size, 0), evictUpTo, size)

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
		state.pieceMemory += size
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

	// Remove the protections for this torrent.
	for key := range c.protections {
		if key.infoHash == infoHash {
			delete(c.protections, key)
		}
	}

	// Remove torrent state.
	delete(c.torrents, infoHash)

	c.logger.Debug("closed torrent",
		slog.String("hash", infoHash.HexString()),
		slog.Int64("evicted", totalEvicted))

	return nil
}

// evictLocked evicts pieces, least recently used first, until memory usage
// is at most target. It gives up unprotected pieces first, then file boundary
// pieces, and last active range pieces, stopping after the upTo level. Within
// each level, pieces still downloading are spared until nothing else is left,
// because an evicted partial piece must be downloaded again in full. When
// reuseSize is positive, the first detached buffer of exactly that length is
// returned for immediate handoff to an incoming allocation. c.mu must be held.
func (c *Client) evictLocked(target int64, upTo evictionProtection, reuseSize int64) []byte {
	if c.used <= target {
		return nil
	}

	before := c.used
	downloadingSince := time.Now().Add(-downloadingPieceGrace).UnixNano()
	var reusable []byte
	for protection := protectActiveAndBoundaries; protection <= upTo && c.used > target; protection++ {
		for _, since := range []int64{downloadingSince, 0} {
			if c.used <= target {
				break
			}
			if detached := c.evictPassLocked(target, protection, reuseSize, since); reusable == nil && detached != nil {
				reusable, reuseSize = detached, 0
			}
		}
	}

	if c.logger.Enabled(context.Background(), slog.LevelDebug) {
		c.logger.Debug("eviction completed",
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

			inActiveRange, inFileBoundary := c.pieceProtectionLocked(key)
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
			c.countEvictionLocked(pd, protection, inActiveRange, inFileBoundary)
			detached := c.evictPieceLocked(key, pd)
			if reusable == nil && int64(dataLen) == reuseSize {
				reusable = detached
			}
		}

		e = next
	}
	return reusable
}

// countEvictionLocked records an eviction in the storage counters. c.mu must
// be held; it acquires pd.mu.
func (c *Client) countEvictionLocked(pd *pieceData, protection evictionProtection, inActiveRange, inFileBoundary bool) {
	pd.mu.RLock()
	complete, written := pd.complete, pd.writtenBytes
	pd.mu.RUnlock()
	if complete {
		c.counters.evictedCompletePieces.Add(1)
	} else {
		c.counters.evictedIncompletePieces.Add(1)
		c.counters.evictedIncompleteBytes.Add(written)
	}
	switch {
	case inActiveRange && protection == protectNone:
		c.counters.activeRangeEvictions.Add(1)
	case inFileBoundary && protection != protectActiveAndBoundaries:
		c.counters.boundaryEvictions.Add(1)
	}
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

// pieceProtectionLocked reports whether a piece falls inside any registered
// active range or file boundary of its torrent. Must be called with c.mu held.
func (c *Client) pieceProtectionLocked(key pieceKey) (inActiveRange, inFileBoundary bool) {
	for k, protection := range c.protections {
		if k.infoHash != key.infoHash {
			continue
		}
		if !inActiveRange && slices.ContainsFunc(protection.Active, func(r PieceRange) bool { return r.contains(key.index) }) {
			inActiveRange = true
		}
		if !inFileBoundary && slices.ContainsFunc(protection.Boundaries, func(r PieceRange) bool { return r.contains(key.index) }) {
			inFileBoundary = true
		}
		if inActiveRange && inFileBoundary {
			return true, true
		}
	}
	return inActiveRange, inFileBoundary
}

// SetProtection replaces the piece ranges that the owner with ownerID, a
// caller-chosen identifier unique within the torrent, protects from standard
// LRU eviction. Ranges hold absolute piece indexes within the torrent. An
// empty protection clears the owner's. Protection for a torrent the client no
// longer manages is ignored.
func (c *Client) SetProtection(infoHash metainfo.Hash, ownerID uint64, protection Protection) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := protectionKey{infoHash: infoHash, ownerID: ownerID}
	// A late call from the stream pool can arrive after closeTorrent has
	// already deleted the torrent state; drop it to prevent orphaned entries.
	if _, exists := c.torrents[infoHash]; !exists || (len(protection.Active) == 0 && len(protection.Boundaries) == 0) {
		delete(c.protections, key)
		return
	}
	c.protections[key] = Protection{
		Active:     slices.Clone(protection.Active),
		Boundaries: slices.Clone(protection.Boundaries),
	}
}

// ClearProtection removes the owner's protection, allowing its pieces to
// become eviction candidates again.
func (c *Client) ClearProtection(infoHash metainfo.Hash, ownerID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.protections, protectionKey{infoHash: infoHash, ownerID: ownerID})

	c.logger.Debug("cleared protection",
		slog.String("hash", infoHash.HexString()),
		slog.Uint64("ownerID", ownerID))
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
		c.releaseMemoryLocked(int64(len(pd.data)), pd.torrent)
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
	c.counters.completionMisses.Add(1)
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
		if errors.Is(err, ErrPieceNotAvailable) {
			p.client.counters.readMisses.Add(1)
		}
		return 0, ErrPieceNotAvailable
	}

	pd.mu.RLock()

	// Check if piece data is available in memory.
	if pd.data == nil {
		pd.mu.RUnlock()
		p.client.counters.readMisses.Add(1)
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
		p.client.counters.incompleteReads.Add(1)
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
		p.client.counters.incompleteHashes.Add(1)
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

	err := p.openErrLocked()
	if err == nil {
		c.evictLocked(c.maxMemory, protectNone, 0)
		if c.used > c.maxMemory {
			err = ErrInsufficientMemory
		}
	}
	if err != nil {
		c.releaseMemoryLocked(pd.pieceSize, p.torrent)
		p.finishFailedAllocationLocked(pd)
		return err
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

	if err := p.openErrLocked(); err != nil {
		return nil, err
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
	if err := p.openErrLocked(); err != nil {
		return nil, err
	}

	key := p.key()

	if pd, ok := p.client.pieces[key]; ok && pd.torrent == p.torrent {
		return pd, nil
	}

	return nil, ErrPieceNotAvailable
}

// openErrLocked returns ErrClientClosed after Close, ErrTorrentClosed once
// the piece's torrent handle or torrent closed, and nil otherwise. c.mu must
// be held.
func (p *pieceImpl) openErrLocked() error {
	if err := p.client.torrentOpenErrLocked(p.infoHash, p.torrent); err != nil {
		return err
	}
	if p.handle.closed.Load() {
		return ErrTorrentClosed
	}
	return nil
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
