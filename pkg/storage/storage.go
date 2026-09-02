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
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// ErrPieceNotAvailable is returned when reading or hashing a piece that has
// never been written or has been evicted.
var ErrPieceNotAvailable = errors.New("piece not available in memory")

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
	// activeRanges tracks piece-index windows that readers are actively consuming.
	// Pieces inside these ranges are protected from standard LRU eviction, except
	// as a last resort under severe memory pressure (see emergency eviction).
	activeRanges map[activeRangeKey]activeRange
	// closeCh is closed when the client is fully shut down.
	closeCh chan struct{}
	// lru is the global least-recently-used list for all pieces.
	lru       *list.List
	logger    *slog.Logger
	maxMemory int64
	mu        sync.RWMutex
	// pieces stores the metadata and data for each piece across all torrents.
	pieces map[pieceKey]*pieceData
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
}

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

// torrentState holds the state specific to a single torrent.
type torrentState struct {
	mu          sync.RWMutex
	pieceMemory int64 // Memory used by this torrent.
	totalPieces int   // Total number of pieces from torrent metadata.
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
		maxMemory:    maxMemory,
		pieces:       make(map[pieceKey]*pieceData),
		torrents:     make(map[metainfo.Hash]*torrentState),
		activeRanges: make(map[activeRangeKey]activeRange),
		lru:          list.New(),
		closeCh:      make(chan struct{}),
		logger:       logger,
	}
	return c
}

// Close stops the client and evicts all pieces from memory.
// It is safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.closeCh:
		return nil
	default:
		close(c.closeCh)
	}

	// Clear all pieces.
	for key, pd := range c.pieces {
		c.evictPieceLocked(key, pd)
	}

	c.pieces = make(map[pieceKey]*pieceData)
	c.torrents = make(map[metainfo.Hash]*torrentState)
	c.activeRanges = make(map[activeRangeKey]activeRange)
	c.lru.Init()
	c.used = 0

	return nil
}

// Closed returns a receive-only channel that is closed when the client
// has completed all cleanup operations and is fully shut down.
func (c *Client) Closed() <-chan struct{} {
	return c.closeCh
}

// isClosed reports whether the client has been closed.
func (c *Client) isClosed() bool {
	select {
	case <-c.closeCh:
		return true
	default:
		return false
	}
}

// EvictTo evicts unprotected pieces until memory usage is at most targetBytes.
// It returns the number of bytes reclaimed. If active-range protection prevents
// reaching the target, it returns ErrEvictionTargetNotReached with the reclaimed
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
			Index:     key.index,
			SizeBytes: pd.pieceSize,
			Complete:  pd.complete,
			Resident:  pd.data != nil,
		}

		stats.Pieces = append(stats.Pieces, pieceStats)
		stats.TrackedBytes += pd.pieceSize

		if pd.complete {
			stats.CompletedBytes += pd.pieceSize
		}

		if pd.data != nil {
			stats.ResidentPieces++
			stats.ResidentBytes += int64(len(pd.data))
		}

		pd.mu.RUnlock()
	}

	// Sort pieces by index for consistent output.
	sort.Slice(stats.Pieces, func(i, j int) bool {
		return stats.Pieces[i].Index < stats.Pieces[j].Index
	})

	return stats, nil
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
		state = &torrentState{totalPieces: pieceCount}
		c.torrents[infoHash] = state
	} else {
		state.mu.Lock()
		state.totalPieces = pieceCount
		state.mu.Unlock()
	}

	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &pieceImpl{
				client:    c,
				infoHash:  infoHash,
				index:     p.Index(),
				pieceSize: p.Length(),
				torrent:   state,
			}
		},
		Close: func() error {
			return c.closeTorrent(infoHash, state)
		},
	}, nil
}

// SetMaxMemory updates the maximum memory limit for the storage client.
// If the new limit is lower than current usage, an eviction will be triggered
// to bring memory usage within the new limit. Negative values are clamped to 0.
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		return nil, ErrClientClosed
	}
	if current, exists := c.torrents[infoHash]; !exists || current != state {
		return nil, ErrTorrentClosed
	}

	// Check if piece itself is larger than the total maxMemory limit.
	if size > c.maxMemory {
		return nil, ErrInsufficientMemory
	}

	var reusable []byte

	// Check if we need to evict.
	if c.used+size > c.maxMemory {
		target := max(c.maxMemory-size, 0)

		beforeEvict := c.used

		// Standard eviction: evict pieces skipping active reader ranges.
		reusable = c.evictDownToInternalLocked(target, true, size)

		// Emergency fallback: if memory is still above target because active ranges
		// protected too many pieces, evict oldest LRU pieces regardless of active range
		// so an incoming piece write does not fail and permanently kill data download.
		if c.used+size > c.maxMemory {
			reuseSize := size
			if reusable != nil {
				reuseSize = 0
			}
			if emergencyReusable := c.evictDownToInternalLocked(target, false, reuseSize); reusable == nil {
				reusable = emergencyReusable
			}
		}

		if c.logger.Enabled(context.Background(), slog.LevelDebug) {
			c.logger.Debug("memory allocation eviction",
				slog.Int64("needed", size),
				slog.Int64("before", beforeEvict),
				slog.Int64("after", c.used))
		}

		// If still not enough memory, return error.
		if c.used+size > c.maxMemory {
			return nil, ErrInsufficientMemory
		}
	}

	// Allocate the memory.
	c.used += size
	state.mu.Lock()
	state.pieceMemory += size
	state.mu.Unlock()

	return reusable, nil
}

// closeTorrent removes all pieces associated with a specific torrent from memory and
// cleans up the torrent's state.
func (c *Client) closeTorrent(infoHash metainfo.Hash, state *torrentState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if current, exists := c.torrents[infoHash]; !exists || current != state {
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

	// Remove active ranges for this torrent.
	for key := range c.activeRanges {
		if key.infoHash == infoHash {
			delete(c.activeRanges, key)
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
// Pieces inside registered active ranges are skipped and protected from eviction.
func (c *Client) evictDownToLocked(target int64) {
	c.evictDownToInternalLocked(target, true, 0)
}

// emergencyEvictDownToLocked is called during allocateMemory when standard eviction
// could not free enough space because too many pieces are in active ranges.
// It evicts the oldest LRU pieces regardless of active range to prevent ErrInsufficientMemory
// from causing anacrolix/torrent to permanently disable downloading.
func (c *Client) emergencyEvictDownToLocked(target int64) {
	c.evictDownToInternalLocked(target, false, 0)
}

// evictDownToInternalLocked evicts pieces until target is reached. If reuseSize
// is positive, at most one detached buffer of exactly that length is returned
// for immediate handoff to an incoming allocation. c.mu must be held.
func (c *Client) evictDownToInternalLocked(target int64, respectActiveRanges bool, reuseSize int64) []byte {
	if c.used <= target {
		return nil
	}

	evicted := int64(0)
	targetEvict := c.used - target
	var reusable []byte

	// Iterate from the back of the LRU list (least recently used).
	for e := c.lru.Back(); e != nil && evicted < targetEvict; {
		key := e.Value.(pieceKey)
		next := e.Prev() // Save the next element before potential removal.

		if pd, ok := c.pieces[key]; ok {
			pd.mu.RLock()
			dataLen := len(pd.data)
			pd.mu.RUnlock()

			if dataLen == 0 {
				e = next
				continue
			}

			if respectActiveRanges {
				// Skip pieces that are inside any active reader range for this torrent.
				if c.isPieceInActiveRangeLocked(key) {
					e = next
					continue
				}
			} else if c.logger.Enabled(context.Background(), slog.LevelWarn) && c.isPieceInActiveRangeLocked(key) {
				c.logger.Warn("emergency eviction of active range piece under memory pressure",
					slog.String("hash", key.infoHash.HexString()),
					slog.Int("piece", key.index),
					slog.Int64("size", int64(dataLen)))
			}

			size := int64(dataLen)
			detached := c.evictPieceLocked(key, pd)
			if reusable == nil && size == reuseSize {
				reusable = detached
			}
			evicted += size

			// Update torrent-specific memory usage.
			if state, exists := c.torrents[key.infoHash]; exists {
				state.mu.Lock()
				state.pieceMemory -= size
				state.mu.Unlock()
			}
		}

		e = next
	}

	if c.logger.Enabled(context.Background(), slog.LevelDebug) {
		if respectActiveRanges {
			c.logger.Debug("eviction completed",
				slog.Int64("target", target),
				slog.Int64("evicted", evicted),
				slog.Int64("newUsed", c.used))
		} else {
			c.logger.Debug("emergency eviction completed",
				slog.Int64("target", target),
				slog.Int64("evicted", evicted),
				slog.Int64("newUsed", c.used))
		}
	}

	return reusable
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
		return err
	}

	pd.mu.Lock()
	defer pd.mu.Unlock()

	// Only mark as complete if we have the data in memory.
	if pd.data == nil {
		return errors.New("cannot mark incomplete piece as complete without data")
	}

	pd.complete = true

	return nil
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

	n = copy(b, pd.data[off:end])
	if n < len(b) && err == nil {
		err = io.EOF // Signal that not all requested bytes were returned.
	}

	pd.mu.RUnlock()
	p.touchPiece(pd)

	return n, err
}

// SelfHash implements the storage.SelfHashing interface, computing the SHA-1 hash
// of the piece data in memory.
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
