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

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// ErrPieceNotAvailable is returned when trying to read a piece that is not in memory.
var ErrPieceNotAvailable = errors.New("piece not available in memory")

// ErrInsufficientMemory is returned when memory allocation fails even after attempting
// to evict existing pieces to free up space.
var ErrInsufficientMemory = errors.New("insufficient memory after eviction")

// Client implements the storage.Client interface from anacrolix/torrent.
type Client struct {
	// activeRanges tracks byte-range windows that readers are actively consuming.
	// Pieces inside these ranges are protected from eviction.
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

// MemoryStats represents global memory usage statistics.
type MemoryStats struct {
	ActiveTorrents int   // Number of active torrents using memory.
	MaxMemory      int64 // Maximum memory allowed in bytes.
	TotalPieces    int   // Total number of pieces in memory.
	UsedMemory     int64 // Current memory used in bytes.
}

// PieceInfo represents information about a torrent piece that we are actively managing.
type PieceInfo struct {
	Complete bool  // Whether the piece is marked as complete.
	InMemory bool  // Whether the piece data is currently in memory.
	Index    int   // Piece index.
	Size     int64 // Piece size in bytes.
}

// TorrentMemoryStats contains statistics about a torrent that the storage is actively managing.
type TorrentMemoryStats struct {
	CompletedSize         int64       // Total size of completed pieces.
	InMemory              int         // Number of pieces currently in memory.
	InMemorySize          int64       // Total size of pieces currently in memory.
	MemoryStats           MemoryStats // Global memory usage statistics.
	MemoryUsagePercentage float64     // Percentage of maximum memory used by this torrent (0-100)
	Pieces                []PieceInfo // All pieces we are managing (both completed and not completed).
	TotalPieces           int         // Total number of pieces in the torrent, from metadata.
	TotalSize             int64       // Total size of all managed pieces in bytes.
}

// pieceKey is a unique identifier for a piece within a specific torrent.
type pieceKey struct {
	hash  metainfo.Hash
	index int
}

// pieceData holds the data and state for a single torrent piece.
type pieceData struct {
	data      []byte        // The actual piece data, nil if not in memory.
	complete  bool          // True if the piece has been successfully downloaded and verified.
	lruElem   *list.Element // Pointer to the piece's element in the global LRU list.
	mu        sync.RWMutex
	pieceSize int64 // The expected size of the piece.
}

// activeRangeKey uniquely identifies a reader's active range within a torrent.
type activeRangeKey struct {
	hash     metainfo.Hash
	readerID uint64
}

// activeRange stores the piece-index window [startPiece, endPiece] (inclusive)
// that a reader is actively consuming, protecting those pieces from eviction.
type activeRange struct {
	endPiece   int
	startPiece int
}

// torrentState holds the state specific to a single torrent.
type torrentState struct {
	mu          sync.RWMutex
	pieceHashes []metainfo.Hash // The SHA1 hashes of all pieces in the torrent.
	pieceMemory int64           // Memory used by this torrent.
}

// NewClient creates a new memory-limited storage client.
func NewClient(maxMemory int64, logger *slog.Logger) *Client {
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

// Close stops the client and cleans up, evicting all pieces from memory.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clear all pieces.
	for key, pd := range c.pieces {
		c.evictPieceLocked(key, pd)
	}

	c.pieces = make(map[pieceKey]*pieceData)
	c.torrents = make(map[metainfo.Hash]*torrentState)
	c.activeRanges = make(map[activeRangeKey]activeRange)
	c.lru.Init()
	c.used = 0

	close(c.closeCh)

	return nil
}

// Closed returns a receive-only channel that is closed when the client
// has completed all cleanup operations and is fully shut down.
func (c *Client) Closed() <-chan struct{} {
	return c.closeCh
}

// ForceEvict can be used to manually trigger eviction down to a target memory usage.
func (c *Client) ForceEvict(target int64) {
	c.evictDownTo(target)
}

// GetCompletedPieces returns a slice of piece indices that are marked as complete.
// Returns nil if the torrent doesn't exist.
func (c *Client) GetCompletedPieces(hash metainfo.Hash) []int {
	info, err := c.GetTorrentMemoryStats(hash)
	if err != nil {
		return nil
	}

	completed := make([]int, 0, len(info.Pieces))
	for _, piece := range info.Pieces {
		if piece.Complete {
			completed = append(completed, piece.Index)
		}
	}

	return completed
}

// GetCompletedProgress returns the percentage of managed pieces that are complete.
// Returns 0 if torrent doesn't exist or has no managed pieces.
func (c *Client) GetCompletedProgress(hash metainfo.Hash) float64 {
	c.mu.RLock()
	torrentState, ok := c.torrents[hash]
	if !ok {
		c.mu.RUnlock()
		return 0
	}
	c.mu.RUnlock()

	torrentState.mu.RLock()
	totalPieces := len(torrentState.pieceHashes)
	torrentState.mu.RUnlock()

	if totalPieces == 0 {
		return 0
	}

	info, err := c.GetTorrentMemoryStats(hash)
	if err != nil {
		return 0
	}

	// Count completed pieces.
	completedCount := 0
	for _, piece := range info.Pieces {
		if piece.Complete {
			completedCount++
		}
	}

	return float64(completedCount) / float64(totalPieces)
}

// GetIncompletePieces returns a slice of piece indices that are not marked as complete.
// Returns nil if the torrent doesn't exist.
func (c *Client) GetIncompletePieces(hash metainfo.Hash) []int {
	info, err := c.GetTorrentMemoryStats(hash)
	if err != nil {
		return nil
	}

	incomplete := make([]int, 0, len(info.Pieces))
	for _, piece := range info.Pieces {
		if !piece.Complete {
			incomplete = append(incomplete, piece.Index)
		}
	}

	return incomplete
}

// GetMemoryStats returns current global memory usage statistics.
func (c *Client) GetMemoryStats() MemoryStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var activeTorrents int
	for _, ts := range c.torrents {
		ts.mu.RLock()
		if ts.pieceMemory > 0 {
			activeTorrents++
		}
		ts.mu.RUnlock()
	}

	return MemoryStats{
		ActiveTorrents: activeTorrents,
		MaxMemory:      c.maxMemory,
		TotalPieces:    len(c.pieces),
		UsedMemory:     c.used,
	}
}

// GetMemoryUsageProgress returns the percentage of memory currently used by a torrent.
// Returns 0 if torrent doesn't exist or has no managed pieces.
func (c *Client) GetMemoryUsageProgress(hash metainfo.Hash) float64 {
	info, err := c.GetTorrentMemoryStats(hash)
	if err != nil || info.TotalPieces == 0 {
		return 0
	}

	return float64(info.InMemorySize) / float64(info.MemoryStats.MaxMemory)
}

// GetPieceStatus returns detailed information about a specific piece that we are managing.
// Returns nil if the piece is not being managed by the storage.
func (c *Client) GetPieceStatus(hash metainfo.Hash, index int) *PieceInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := pieceKey{hash: hash, index: index}
	pd, exists := c.pieces[key]
	if !exists {
		return nil
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	return &PieceInfo{
		Complete: pd.complete,
		InMemory: pd.data != nil,
		Index:    index,
		Size:     pd.pieceSize,
	}
}

// GetPiecesInMemory returns a slice of piece indices that are currently in memory.
// Returns nil if the torrent doesn't exist.
func (c *Client) GetPiecesInMemory(hash metainfo.Hash) []int {
	info, err := c.GetTorrentMemoryStats(hash)
	if err != nil {
		return nil
	}

	inMemory := make([]int, 0, len(info.Pieces))
	for _, piece := range info.Pieces {
		if piece.InMemory {
			inMemory = append(inMemory, piece.Index)
		}
	}

	return inMemory
}

// GetTorrentMemoryStats returns statistics only about a torrent that the storage is actively managing.
// This includes pieces that have been created (via ReadAt or WriteAt) and are being tracked.
//
// The returned stats are eventually consistent rather than atomic: maxMemory, usedMemory, and
// piece-level fields (InMemorySize, TotalSize, etc.) are captured under separate lock acquisitions
// to avoid holding c.mu.RLock() for the duration of piece iteration (which could starve writers
// on torrents with thousands of pieces). Callers should treat the fields as a rough snapshot
// suitable for progress bars and UI displays, not for assertions like InMemorySize <= UsedMemory.
//
// Returns error if the torrent doesn't exist.
func (c *Client) GetTorrentMemoryStats(hash metainfo.Hash) (*TorrentMemoryStats, error) {
	c.mu.RLock()

	// Check if torrent exists.
	torrentState, exists := c.torrents[hash]
	if !exists {
		c.mu.RUnlock()
		return nil, fmt.Errorf("torrent %s is not managed by storage", hash)
	}

	// Read torrentState fields under its own lock for consistent discipline.
	// pieceHashes is write-once at OpenTorrent time and never mutated after,
	// but we lock torrentState.mu here to match the pattern used by
	// GetCompletedProgress and other callers, preventing future bugs if the
	// invariant changes.
	torrentState.mu.RLock()
	totalPieces := len(torrentState.pieceHashes)
	torrentState.mu.RUnlock()

	maxMemory := c.maxMemory
	usedMemory := c.used

	// Collect piece keys belonging to this torrent, then release the global lock
	// before reading per-piece data. This prevents write starvation from
	// allocateMemory, evictDownTo, touchPiece, and freeMemory.
	pieceKeys := make([]pieceKey, 0, totalPieces)
	for key := range c.pieces {
		if key.hash == hash {
			pieceKeys = append(pieceKeys, key)
		}
	}
	c.mu.RUnlock()

	info := &TorrentMemoryStats{
		Pieces:      make([]PieceInfo, 0, len(pieceKeys)),
		TotalPieces: totalPieces,
	}
	info.MemoryStats.MaxMemory = maxMemory
	info.MemoryStats.UsedMemory = usedMemory

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

		pieceInfo := PieceInfo{
			Index:    key.index,
			Size:     pd.pieceSize,
			Complete: pd.complete,
			InMemory: pd.data != nil,
		}

		info.Pieces = append(info.Pieces, pieceInfo)
		info.TotalSize += pd.pieceSize

		if pd.complete {
			info.CompletedSize += pd.pieceSize
		}

		if pd.data != nil {
			info.InMemory++
			info.InMemorySize += int64(len(pd.data))
		}

		pd.mu.RUnlock()
	}

	// Sort pieces by index for consistent output.
	sort.Slice(info.Pieces, func(i, j int) bool {
		return info.Pieces[i].Index < info.Pieces[j].Index
	})

	// Compute percentage after iterating pieces (InMemorySize is now populated).
	if info.MemoryStats.MaxMemory > 0 {
		info.MemoryUsagePercentage = float64(info.InMemorySize) / float64(info.MemoryStats.MaxMemory) * 100
	}

	return info, nil
}

// GetTorrentMemoryUsage returns memory usage for a specific torrent.
func (c *Client) GetTorrentMemoryUsage(hash metainfo.Hash) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if torrentState, exists := c.torrents[hash]; exists {
		torrentState.mu.RLock()
		defer torrentState.mu.RUnlock()
		return torrentState.pieceMemory
	}

	return 0
}

// OpenTorrent implements the storage.Client interface. It is called when a new
// torrent is added to the torrent client.
func (c *Client) OpenTorrent(_ context.Context, info *metainfo.Info, hash metainfo.Hash) (storage.TorrentImpl, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Extract piece hashes from torrent info.
	// info.Pieces is a []byte containing concatenated SHA1 hashes (20 bytes each).
	pieceCount := info.NumPieces()
	pieceHashes := make([]metainfo.Hash, pieceCount)

	for i := 0; i < pieceCount; i++ {
		start := i * 20 // SHA1 is 20 bytes.
		end := start + 20
		if end > len(info.Pieces) {
			// This shouldn't happen with a valid torrent file, but we check for safety.
			c.logger.Error("invalid pieces length in torrent info",
				slog.String("hash", hash.HexString()),
				slog.Int("pieceIndex", i),
				slog.Int("piecesLength", len(info.Pieces)))
			return storage.TorrentImpl{}, errors.New("invalid pieces length in torrent info")
		}

		var h metainfo.Hash
		copy(h[:], info.Pieces[start:end])
		pieceHashes[i] = h
	}

	// Initialize torrent state with piece hashes.
	c.torrents[hash] = &torrentState{
		pieceHashes: pieceHashes,
	}

	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &pieceImpl{
				client: c,
				hash:   hash,
				index:  p.Index(),
				length: p.Length(),
			}
		},
		Close: func() error {
			return c.closeTorrent(hash)
		},
	}, nil
}

// SetMaxMemory updates the maximum memory limit for the storage client.
// If the new limit is lower than current usage, an eviction will be triggered
// to bring memory usage within the new limit. Negative values are clamped to 0.
// This operation is thread-safe.
func (c *Client) SetMaxMemory(maxMemory int64) {
	if maxMemory < 0 {
		maxMemory = 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.maxMemory = maxMemory

	// Trigger eviction if current usage exceeds the new limit.
	if c.used > maxMemory {
		c.evictDownToLocked(maxMemory)
	}

	c.logger.Debug("updated memory limit",
		slog.Int64("newLimit", maxMemory),
		slog.Int64("currentUsed", c.used))
}

// freeMemory releases a previously reserved amount of memory for a piece.
// It must only be called when the memory was reserved but never actually used
// (e.g. a redundant allocation from ensureDataAllocated's concurrent-writer race).
func (c *Client) freeMemory(size int64, hash metainfo.Hash) {
	c.mu.Lock()
	c.used -= size
	if c.used < 0 {
		c.used = 0
	}
	torrentState, exists := c.torrents[hash]
	c.mu.Unlock()

	if exists {
		torrentState.mu.Lock()
		torrentState.pieceMemory -= size
		if torrentState.pieceMemory < 0 {
			torrentState.pieceMemory = 0
		}
		torrentState.mu.Unlock()
	}
}

// allocateMemory reserves a given amount of memory for a piece. If the allocation
// would exceed the memory limit, it attempts to evict least-recently-used pieces
// to free up space. It returns ErrInsufficientMemory if enough space cannot be freed.
func (c *Client) allocateMemory(size int64, hash metainfo.Hash) error {
	c.mu.Lock()

	// Check if we need to evict.
	if c.used+size > c.maxMemory {
		target := c.maxMemory - size
		if target < 0 {
			target = 0
		}

		beforeEvict := c.used

		// Evict pieces until we have enough space.
		c.evictDownToLocked(target)

		c.logger.Debug("memory allocation eviction",
			slog.Int64("needed", size),
			slog.Int64("before", beforeEvict),
			slog.Int64("after", c.used))

		// If still not enough memory, return error.
		if c.used+size > c.maxMemory {
			c.mu.Unlock()
			return ErrInsufficientMemory
		}
	}

	// Allocate the memory.
	c.used += size

	// Get torrent state before releasing the global lock.
	torrentState, exists := c.torrents[hash]
	c.mu.Unlock()

	// Update torrent-specific memory usage.
	if exists {
		torrentState.mu.Lock()
		torrentState.pieceMemory += size
		torrentState.mu.Unlock()
	}

	return nil
}

// closeTorrent removes all pieces associated with a specific torrent from memory and
// cleans up the torrent's state.
func (c *Client) closeTorrent(hash metainfo.Hash) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove all pieces for this torrent.
	var totalEvicted int64
	for key, pd := range c.pieces {
		if key.hash == hash {
			size := int64(len(pd.data))
			c.evictPieceLocked(key, pd)
			totalEvicted += size
		}
	}

	// Remove active ranges for this torrent.
	for key := range c.activeRanges {
		if key.hash == hash {
			delete(c.activeRanges, key)
		}
	}

	// Remove torrent state.
	delete(c.torrents, hash)

	c.logger.Debug("closed torrent",
		slog.String("hash", hash.HexString()),
		slog.Int64("evicted", totalEvicted))

	return nil
}

// evictDownTo is a wrapper for evictDownToLocked that acquires the necessary lock.
func (c *Client) evictDownTo(target int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictDownToLocked(target)
}

// evictDownToLocked evicts pieces from the LRU list until the total memory usage
// is at or below the target. It must be called with the client's mutex held.
// Pieces inside registered active ranges are skipped and protected from eviction.
func (c *Client) evictDownToLocked(target int64) {
	if c.used <= target {
		return
	}

	evicted := int64(0)
	targetEvict := c.used - target

	// Iterate from the back of the LRU list (least recently used).
	for e := c.lru.Back(); e != nil && evicted < targetEvict; {
		key := e.Value.(pieceKey)
		next := e.Prev() // Save the next element before potential removal.

		if pd, ok := c.pieces[key]; ok {
			// Skip pieces that are inside any active reader range for this torrent.
			if c.isPieceInActiveRangeLocked(key) {
				e = next
				continue
			}

			size := int64(len(pd.data))
			c.evictPieceLocked(key, pd)
			evicted += size

			// Update torrent-specific memory usage.
			if torrentState, exists := c.torrents[key.hash]; exists {
				torrentState.mu.Lock()
				torrentState.pieceMemory -= size
				torrentState.mu.Unlock()
			}
		}

		e = next
	}

	c.logger.Debug("eviction completed",
		slog.Int64("target", target),
		slog.Int64("evicted", evicted),
		slog.Int64("newUsed", c.used))
}

// isPieceInActiveRangeLocked checks whether a piece falls inside any registered
// active range for its torrent. Must be called with c.mu held.
func (c *Client) isPieceInActiveRangeLocked(key pieceKey) bool {
	for rk, ar := range c.activeRanges {
		if rk.hash != key.hash {
			continue
		}
		if key.index >= ar.startPiece && key.index <= ar.endPiece {
			return true
		}
	}
	return false
}

// SetActiveRange registers or refreshes a piece-index range [startPiece, endPiece]
// (inclusive) that a reader is actively consuming. Pieces inside this range are
// protected from eviction. readerID is a unique, caller-chosen identifier.
// startPiece and endPiece are absolute piece indices within the torrent.
func (c *Client) SetActiveRange(hash metainfo.Hash, readerID uint64, start, end int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activeRanges[activeRangeKey{hash: hash, readerID: readerID}] = activeRange{
		endPiece:   int(end),
		startPiece: int(start),
	}
}

// ClearActiveRange removes the active range for a specific reader, allowing its
// pieces to become eviction candidates again.
func (c *Client) ClearActiveRange(hash metainfo.Hash, readerID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.activeRanges, activeRangeKey{hash: hash, readerID: readerID})

	c.logger.Debug("cleared active range",
		slog.String("hash", hash.HexString()),
		slog.Uint64("readerID", readerID))
}

// evictPieceLocked removes a piece's data from memory and the LRU list.
// It must be called with the client's mutex held. Acquires pd.mu to safely
// nil out the data slice, preventing a data race with concurrent ReadAt calls.
func (c *Client) evictPieceLocked(key pieceKey, pd *pieceData) {
	pd.mu.Lock()
	if pd.data != nil {
		size := int64(len(pd.data))
		c.used -= size
		pd.data = nil
	}
	pd.mu.Unlock()

	if pd.lruElem != nil {
		c.lru.Remove(pd.lruElem)
		pd.lruElem = nil
	}
	delete(c.pieces, key)

	c.logger.Debug("evicted piece",
		slog.String("hash", key.hash.HexString()),
		slog.Int("piece", key.index))
}

// pieceImpl implements the storage.PieceImpl interface.
type pieceImpl struct {
	client *Client
	hash   metainfo.Hash
	index  int
	length int64
}

// Completion implements the storage.PieceImpl interface.
func (p *pieceImpl) Completion() storage.Completion {
	pd, err := p.getPieceData()
	if err != nil {
		// Piece was evicted or never existed: report as incomplete so
		// the library removes it from the completed-pieces bitmap.
		return storage.Completion{Complete: false, Ok: true}
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

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
	pd := p.getOrCreatePieceData()

	pd.mu.RLock()

	// Check if piece data is available in memory.
	if pd.data == nil {
		pd.mu.RUnlock()
		return 0, ErrPieceNotAvailable
	}

	// Boundary checks.
	if off < 0 || off >= p.length {
		pd.mu.RUnlock()
		p.touchPiece()
		return 0, io.EOF
	}
	remaining := p.length - off
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
	p.touchPiece()

	return n, err
}

// SelfHash implements the storage.SelfHashing interface, computing the SHA1 hash
// of the piece data in memory.
func (p *pieceImpl) SelfHash() (metainfo.Hash, error) {
	p.client.mu.RLock()
	defer p.client.mu.RUnlock()

	key := pieceKey{hash: p.hash, index: p.index}
	pd, exists := p.client.pieces[key]
	if !exists {
		return metainfo.Hash{}, nil // No piece data, no hash.
	}

	pd.mu.RLock()
	defer pd.mu.RUnlock()

	if pd.data == nil {
		return metainfo.Hash{}, errors.New("piece data not in memory")
	}

	// Compute SHA1 hash.
	hash := sha1.Sum(pd.data)
	var result metainfo.Hash
	copy(result[:], hash[:])

	return result, nil
}

// WriteAt implements the storage.PieceImpl interface.
func (p *pieceImpl) WriteAt(b []byte, off int64) (n int, err error) {
	pd := p.getOrCreatePieceData()

	// Ensure data is allocated. This function will handle locking.
	if err := p.ensureDataAllocated(pd); err != nil {
		return 0, err
	}

	pd.mu.Lock()

	// Boundary checks.
	if off < 0 || off > p.length {
		pd.mu.Unlock()
		return 0, errors.New("offset out of piece bounds")
	}
	if int64(len(b)) > p.length-off {
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

	p.touchPiece()

	return n, nil
}

// ensureDataAllocated makes sure that the piece's data slice is allocated.
// Lock ordering: c.mu → pd.mu (never the reverse). Release pd.mu before
// calling allocateMemory (which acquires c.mu), then re-check under pd.mu
// after allocating. If a concurrent goroutine already allocated while
// blocked, the redundant reservation is refunded via freeMemory.
func (p *pieceImpl) ensureDataAllocated(pd *pieceData) error {
	pd.mu.RLock()
	needsAlloc := pd.data == nil
	pd.mu.RUnlock()

	if !needsAlloc {
		return nil
	}

	if err := p.client.allocateMemory(pd.pieceSize, p.hash); err != nil {
		return err
	}

	// Re-check under write lock — another goroutine may have already allocated.
	pd.mu.Lock()
	if pd.data == nil {
		pd.data = make([]byte, pd.pieceSize)
		pd.mu.Unlock()
	} else {
		// Another goroutine already allocated; refund the redundant
		// reservation to keep c.used from drifting.
		pd.mu.Unlock()
		p.client.freeMemory(pd.pieceSize, p.hash)
	}

	return nil
}

// getOrCreatePieceData retrieves the pieceData for a piece, creating it if it doesn't exist.
// This ensures that piece metadata is tracked as soon as it's accessed.
func (p *pieceImpl) getOrCreatePieceData() *pieceData {
	p.client.mu.Lock()
	defer p.client.mu.Unlock()

	key := p.key()

	// Return the piece if it already exists.
	if pd, ok := p.client.pieces[key]; ok {
		return pd
	}

	// Create a new piece, add it to the tracking map, and place it in the LRU list.
	pd := &pieceData{
		pieceSize: p.length,
	}
	p.client.pieces[key] = pd
	pd.lruElem = p.client.lru.PushFront(key)

	return pd
}

// getPieceData retrieves the pieceData for a piece only if it already exists.
func (p *pieceImpl) getPieceData() (*pieceData, error) {
	p.client.mu.Lock()
	defer p.client.mu.Unlock()

	key := p.key()

	if pd, ok := p.client.pieces[key]; ok {
		return pd, nil
	}

	return nil, ErrPieceNotAvailable
}

// key generates the unique pieceKey for the current piece.
func (p *pieceImpl) key() pieceKey {
	return pieceKey{hash: p.hash, index: p.index}
}

// touchPiece moves a piece to the front of the LRU list, marking it as
// recently used.
func (p *pieceImpl) touchPiece() {
	p.client.mu.Lock()
	defer p.client.mu.Unlock()

	if pd, ok := p.client.pieces[p.key()]; ok && pd.lruElem != nil {
		p.client.lru.MoveToFront(pd.lruElem)
	}
}
