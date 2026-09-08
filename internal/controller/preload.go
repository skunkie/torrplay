// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
	"github.com/torrplay/torrplay/pkg/stream"
)

const (
	preloadNoFileIndex     = -1
	defaultPreloadReadyTTL = 5 * time.Minute
	// maxConcurrentPreloads caps preloads that are actively fetching. The
	// binding constraint is bandwidth and piece-priority contention rather than
	// memory: a preload claims its whole range at PiecePriorityNow, the same
	// priority a blocked playback reader uses, so every extra concurrent one
	// competes with the stream the user is watching. Raising this does not
	// shrink an individual preload on a machine with memory to spare.
	maxConcurrentPreloads = 2

	// maxPreloadBytes bounds a single preload. It is a startup-latency limit,
	// not a memory one: no amount of configured memory makes anyone willing to
	// wait longer for playback to begin, so it deliberately does not scale with
	// MaxMemory. Aggregate preload memory is capped by the stream pool at
	// reservation time, not here.
	maxPreloadBytes = 32 << 20

	// Preload protection is registered directly with storage rather than by a
	// Pool reader. Keep these sentinels far from Pool.nextID's zero-based reader
	// IDs; changes to either allocation scheme must preserve that separation.
	preloadHeadReaderID uint64 = 1<<63 - 1
	preloadTailReaderID uint64 = preloadHeadReaderID - 1
)

type preloadTask struct {
	ctx             context.Context
	infoHash        metainfo.Hash
	cancel          context.CancelFunc
	clearProtection func()
	doneOnce        sync.Once
	done            chan struct{}
	expiryTimer     *time.Timer
	releaseBudget   func()
	reserveBudget   func() bool
	setProtection   func()
	start           func()
	targetBytes     int64
	fileIndex       int
	filePath        string
	active          bool
	protected       bool
	queued          bool
	bytesRead       atomic.Int64
	ready           atomic.Bool
}

func (p *preloadTask) progressBytes() int64 {
	return min(p.bytesRead.Load(), p.targetBytes)
}

// PutTorrentPreload starts or updates preloading for a specific torrent file.
func (c *Controller) PutTorrentPreload(w http.ResponseWriter, r *http.Request, hash api.Hash) {
	ih := hash
	if ih.IsZero() {
		api.HTTPError(w, "invalid hash", http.StatusBadRequest)
		return
	}

	var req api.PreloadRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.HTTPError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	to, ok := c.client.Torrent(ih)
	if !ok {
		t, err := c.db.GetTorrent(ih)
		if err != nil {
			api.HTTPError(w, "torrent not found", http.StatusNotFound)
			return
		}
		storageMode := utils.Val(t.Storage)
		if storageMode == "" {
			storageMode = api.Memory
		}
		to, err = c.loadTorrentSpec(&torrent.TorrentSpec{
			AddTorrentOpts: torrent.AddTorrentOpts{
				InfoHash:  ih,
				InfoBytes: t.InfoBytes,
			},
		}, storageMode)
		if err != nil {
			api.HandleError(w, err)
			return
		}
	}

	select {
	case <-to.GotInfo():
	case <-time.After(c.runtimeConfig.gotInfoTimeout):
		api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
		return
	}

	files := to.Files()
	if len(files) == 0 {
		api.HTTPError(w, "torrent has no files", http.StatusBadRequest)
		return
	}

	targetIdx := 0
	if req.FileIndex != nil {
		if *req.FileIndex < 0 || *req.FileIndex >= len(files) {
			api.HTTPError(w, fmt.Sprintf("file_index %d out of range (0..%d)", *req.FileIndex, len(files)-1), http.StatusBadRequest)
			return
		}
		targetIdx = *req.FileIndex
	} else if req.FilePath != nil && *req.FilePath != "" {
		found := false
		targetPath := *req.FilePath
		for i, f := range files {
			if f.Path() == targetPath || filepath.Clean(f.Path()) == filepath.Clean(targetPath) {
				targetIdx = i
				found = true
				break
			}
		}
		if !found {
			api.HTTPError(w, fmt.Sprintf("file path %q not found in torrent", targetPath), http.StatusBadRequest)
			return
		}
	}

	c.startPreload(to, files[targetIdx], targetIdx)

	resp := c.getPreloadStatus(ih)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetTorrentPreload returns the current preload status and progress for a torrent.
func (c *Controller) GetTorrentPreload(w http.ResponseWriter, _ *http.Request, hash api.Hash) {
	ih := hash
	if ih.IsZero() {
		api.HTTPError(w, "invalid hash", http.StatusBadRequest)
		return
	}

	if _, ok := c.client.Torrent(ih); !ok {
		if _, err := c.db.GetTorrent(ih); err != nil {
			api.HTTPError(w, "torrent not found", http.StatusNotFound)
			return
		}
	}

	resp := c.getPreloadStatus(ih)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

// DeleteTorrentPreload cancels active preloading and clears boundary protection.
func (c *Controller) DeleteTorrentPreload(w http.ResponseWriter, _ *http.Request, hash api.Hash) {
	ih := hash
	if ih.IsZero() {
		api.HTTPError(w, "invalid hash", http.StatusBadRequest)
		return
	}

	if _, ok := c.client.Torrent(ih); !ok {
		if _, err := c.db.GetTorrent(ih); err != nil {
			api.HTTPError(w, "torrent not found", http.StatusNotFound)
			return
		}
	}

	c.cancelPreload(ih)
	w.WriteHeader(http.StatusNoContent)
}

func (c *Controller) getPreloadStatus(ih metainfo.Hash) api.PreloadResponse {
	to, hasTorrent := c.client.Torrent(ih)

	if val, preloading := c.preloads.Load(ih); preloading {
		if p, ok := val.(*preloadTask); ok && p != nil {
			if p.ready.Load() {
				return api.PreloadResponse{
					FileIndex:      p.fileIndex,
					FilePath:       &p.filePath,
					TargetBytes:    p.targetBytes,
					CompletedBytes: p.targetBytes,
					Progress:       1.0,
					Status:         api.Ready,
				}
			}

			currentBytes := p.progressBytes()

			progress := float32(0)
			if p.targetBytes > 0 {
				progress = min(1.0, float32(currentBytes)/float32(p.targetBytes))
			}
			return api.PreloadResponse{
				FileIndex:      p.fileIndex,
				FilePath:       &p.filePath,
				TargetBytes:    p.targetBytes,
				CompletedBytes: currentBytes,
				Progress:       progress,
				Status:         api.Preloading,
			}
		}
	}

	if hasTorrent && to.Info() != nil && to.BytesCompleted() == to.Length() {
		return api.PreloadResponse{
			FileIndex:      preloadNoFileIndex,
			TargetBytes:    to.Length(),
			CompletedBytes: to.Length(),
			Progress:       1.0,
			Status:         api.Ready,
		}
	}

	return api.PreloadResponse{
		FileIndex:      preloadNoFileIndex,
		TargetBytes:    0,
		CompletedBytes: 0,
		Progress:       0.0,
		Status:         api.Idle,
	}
}

func (c *Controller) startPreload(to *torrent.Torrent, file *torrent.File, fileIndex int) *preloadTask {
	if to == nil || to.Info() == nil || file == nil {
		return nil
	}

	to.AllowDataDownload()

	c.mu.RLock()
	pool := c.streamPool
	maxMem := utils.Val(c.settings.MaxMemory)
	storageClient := c.storageClient
	c.mu.RUnlock()
	if pool == nil {
		return nil
	}

	ih := to.InfoHash()
	mode := stream.MemoryStorage
	if t, err := c.db.GetTorrent(ih); err == nil && utils.Val(t.Storage) == api.File {
		mode = stream.FileStorage
	} else {
		c.torrentTracker.mu.RLock()
		if info, ok := c.torrentTracker.torrents[ih]; ok && info.storageType == api.File {
			mode = stream.FileStorage
		}
		c.torrentTracker.mu.RUnlock()
	}

	c.preloadsMu.Lock()
	if current, ok := c.preloads.Load(ih); ok {
		if p, ok := current.(*preloadTask); ok && p != nil && p.fileIndex == fileIndex {
			c.preloadsMu.Unlock()
			return p
		}
	}
	preloadBudget := calculatePreloadBudget(file.Length(), maxMem)
	if preloadBudget <= 0 {
		if old, ok := c.preloads.LoadAndDelete(ih); ok {
			if p, ok := old.(*preloadTask); ok {
				c.releasePreloadLocked(p, true)
			}
		}
		c.preloadsMu.Unlock()
		return nil
	}

	startend := max(int64(stream.DefaultFileBoundaryBytes), to.Info().PieceLength)
	var readerStartEnd, readerEndStart, readerEndEnd, targetBytes int64
	switch {
	case file.Length() <= startend:
		readerStartEnd = min(preloadBudget, file.Length())
		targetBytes = readerStartEnd
	case preloadBudget <= startend:
		readerStartEnd = preloadBudget
		targetBytes = readerStartEnd
	default:
		readerEndStart = file.Length() - startend
		readerEndEnd = file.Length()
		readerStartEnd = min(preloadBudget-startend, readerEndStart)
		targetBytes = readerStartEnd + (readerEndEnd - readerEndStart)
	}

	if old, ok := c.preloads.LoadAndDelete(ih); ok {
		if p, ok := old.(*preloadTask); ok {
			c.releasePreloadLocked(p, true)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	clearProtection := func() {
		if storageClient != nil {
			storageClient.ClearActiveRange(ih, preloadHeadReaderID)
			storageClient.ClearActiveRange(ih, preloadTailReaderID)
		}
	}
	preload := &preloadTask{
		ctx:             ctx,
		infoHash:        ih,
		cancel:          cancel,
		clearProtection: clearProtection,
		done:            make(chan struct{}),
		targetBytes:     targetBytes,
		fileIndex:       fileIndex,
		filePath:        file.Path(),
		queued:          true,
	}
	preload.reserveBudget = func() bool {
		if mode != stream.MemoryStorage {
			return true
		}
		granted := pool.ReservePreloadBudget(ih, preload.targetBytes)
		if granted == preload.targetBytes {
			return true
		}
		if granted > 0 {
			pool.ReleasePreloadBudget(ih)
		}
		return false
	}
	if mode == stream.MemoryStorage {
		preload.releaseBudget = func() { pool.ReleasePreloadBudget(ih) }
	}
	preload.setProtection = func() {
		if storageClient != nil {
			if hs, he, ts, te, ok := preloadPieceBoundaries(file, readerStartEnd, readerEndStart, readerEndEnd); ok {
				storageClient.SetActiveRange(ih, preloadHeadReaderID, hs, he)
				storageClient.SetActiveRange(ih, preloadTailReaderID, ts, te)
			}
		}
	}
	preload.start = func() {
		c.runPreload(preload, to, file, pool, mode, readerStartEnd, readerEndStart, readerEndEnd)
	}
	c.preloads.Store(ih, preload)
	c.preloadQueue = append(c.preloadQueue, preload)
	c.dispatchPreloadsLocked()
	c.preloadsMu.Unlock()
	return preload
}

// dispatchPreloadsLocked starts queued preloads in FIFO order. Concurrency is
// capped by maxConcurrentPreloads; memory is accounted solely by the stream
// pool, whose ReservePreloadBudget caps aggregate preload reservations against
// the same budget streaming readahead draws from. Keeping a second byte
// accounting here would let a task pass one gate and fail the other. Completed
// preloads still hold a reservation for the cache they pin, so admission may
// have to evict one of them first.
func (c *Controller) dispatchPreloadsLocked() {
	for c.preloadActiveTasks < maxConcurrentPreloads && len(c.preloadQueue) > 0 {
		preload := c.preloadQueue[0]
		current, ok := c.preloads.Load(preload.infoHash)
		if !ok || current != preload || preload.ctx.Err() != nil {
			c.preloadQueue = c.preloadQueue[1:]
			preload.queued = false
			preload.doneOnce.Do(func() { close(preload.done) })
			continue
		}
		c.preloadQueue = c.preloadQueue[1:]
		reserved := preload.reserveBudget == nil || preload.reserveBudget()
		for !reserved && c.evictReadyPreloadLocked(preload) {
			reserved = preload.reserveBudget()
		}
		if !reserved {
			if c.preloadActiveTasks > 0 {
				// A running preload holds the pool reservation; retry in FIFO
				// order once it releases.
				c.preloadQueue = append([]*preloadTask{preload}, c.preloadQueue...)
				break
			}
			// Nothing active will release a reservation, so no later dispatch
			// is coming and the task (with everything queued behind it) would
			// wait forever.
			c.dropUndispatchablePreloadLocked(preload, "preload cannot reserve stream memory budget")
			continue
		}

		preload.queued = false
		preload.active = true
		c.preloadActiveTasks++
		if preload.setProtection != nil {
			preload.setProtection()
			preload.protected = true
		}
		go preload.start()
	}
}

// dropUndispatchablePreloadLocked abandons a queued preload that can never be
// admitted, so it does not block the rest of the FIFO queue indefinitely. The
// task must already have been removed from c.preloadQueue.
func (c *Controller) dropUndispatchablePreloadLocked(preload *preloadTask, reason string) {
	if c.logger != nil {
		c.logger.Warn("torrent preload dropped", "hash", preload.infoHash, "reason", reason, "bytes", preload.targetBytes)
	}
	c.preloads.CompareAndDelete(preload.infoHash, preload)
	c.releasePreloadBudgetLocked(preload)
	preload.queued = false
	preload.doneOnce.Do(func() { close(preload.done) })
	preload.cancel()
}

func (c *Controller) runPreload(preload *preloadTask, to *torrent.Torrent, file *torrent.File, pool *stream.Pool, mode stream.StorageMode, readerStartEnd, readerEndStart, readerEndEnd int64) {
	completed := false
	defer func() {
		preload.doneOnce.Do(func() { close(preload.done) })
		c.finishPreload(preload, completed && preload.ctx.Err() == nil)
	}()

	if c.downloader != nil {
		c.downloader.AddStreaming(preload.infoHash)
		defer c.downloader.RemoveStreaming(preload.infoHash)
	}

	go func() {
		select {
		case <-to.Closed():
			preload.cancel()
		case <-preload.ctx.Done():
		}
	}()

	results := make(chan error, 2)
	workers := 1
	go func() {
		results <- preloadRange(preload.ctx, pool, file, mode, 0, readerStartEnd, &preload.bytesRead)
	}()
	if readerEndEnd > readerEndStart {
		workers++
		go func() {
			results <- preloadRange(preload.ctx, pool, file, mode, readerEndStart, readerEndEnd, &preload.bytesRead)
		}()
	}

	var preloadErr error
	for range workers {
		if err := <-results; err != nil && !errors.Is(err, context.Canceled) && preloadErr == nil {
			preloadErr = err
			preload.cancel()
		}
	}
	if preloadErr != nil {
		c.logger.Warn("torrent preload failed", "hash", preload.infoHash, "error", preloadErr)
		return
	}
	if preload.ctx.Err() != nil {
		return
	}

	preload.bytesRead.Store(preload.targetBytes)
	preload.ready.Store(true)
	completed = true
}

// calculatePreloadBudget sizes a single preload: never more than the file,
// never more than an even share of the half of memory preload may collectively
// use, and never more than the startup-latency ceiling. The memory term scales
// with maxConcurrentPreloads because a tight memory limit genuinely has to be
// divided; maxPreloadBytes does not, so on a machine with memory to spare the
// per-preload size is independent of how many run at once.
//
// This only bounds what the task asks for. Whether that request is affordable
// right now is decided by the stream pool at reservation time.
func calculatePreloadBudget(fileSize, maxMemory int64) int64 {
	return max(min(fileSize, maxMemory/2/maxConcurrentPreloads, maxPreloadBytes), 0)
}

func (c *Controller) finishPreload(preload *preloadTask, completed bool) {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()

	c.releasePreloadCapacityLocked(preload)
	current, ok := c.preloads.Load(preload.infoHash)
	if !ok || current != preload {
		c.clearPreloadProtectionLocked(preload)
		c.dispatchPreloadsLocked()
		preload.cancel()
		return
	}
	if completed {
		c.schedulePreloadExpiryLocked(preload)
	} else {
		c.preloads.Delete(preload.infoHash)
		c.clearPreloadProtectionLocked(preload)
	}
	c.dispatchPreloadsLocked()
	preload.cancel()
}

func preloadRange(ctx context.Context, pool *stream.Pool, file *torrent.File, mode stream.StorageMode, start, end int64, bytesRead *atomic.Int64) error {
	if end <= start {
		return nil
	}
	reader, release, err := pool.AcquirePreloadContext(ctx, file, mode, start, end)
	if err != nil {
		return fmt.Errorf("acquire reader: %w", err)
	}
	defer release()
	return readPreloadRange(ctx, reader, start, end, bytesRead)
}

func readPreloadRange(ctx context.Context, reader io.ReadSeeker, start, end int64, bytesRead *atomic.Int64) error {
	if _, err := reader.Seek(start, io.SeekStart); err != nil {
		return fmt.Errorf("seek to %d: %w", start, err)
	}

	buf := make([]byte, 32768)
	offset := start
	for offset < end {
		if err := ctx.Err(); err != nil {
			return err
		}
		limit := min(int64(len(buf)), end-offset)
		n, readErr := reader.Read(buf[:limit])
		if n > 0 {
			offset += int64(n)
			bytesRead.Add(int64(n))
		}
		if offset >= end {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read at %d: %w", offset, readErr)
		}
		if n == 0 {
			return fmt.Errorf("read at %d: %w", offset, io.ErrNoProgress)
		}
	}
	return nil
}

func preloadPieceBoundaries(file *torrent.File, headEnd, tailStart, tailEnd int64) (int, int, int, int, bool) {
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

func (c *Controller) cancelPreload(ih metainfo.Hash) {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	if val, ok := c.preloads.LoadAndDelete(ih); ok {
		if p, ok := val.(*preloadTask); ok {
			c.releasePreloadLocked(p, true)
		}
	}
}

func (c *Controller) removePreload(ih metainfo.Hash, preload *preloadTask) bool {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	if !c.preloads.CompareAndDelete(ih, preload) {
		return false
	}
	c.releasePreloadLocked(preload, true)
	return true
}

func (c *Controller) schedulePreloadExpiryLocked(preload *preloadTask) {
	if c.preloadReadyTTL <= 0 {
		return
	}
	current, ok := c.preloads.Load(preload.infoHash)
	if !ok || current != preload {
		return
	}
	preload.expiryTimer = time.AfterFunc(c.preloadReadyTTL, func() {
		c.removePreload(preload.infoHash, preload)
	})
}

func (c *Controller) releasePreloadLocked(preload *preloadTask, dispatch bool) {
	if preload.expiryTimer != nil {
		preload.expiryTimer.Stop()
		preload.expiryTimer = nil
	}
	if preload.cancel != nil {
		preload.cancel()
	}
	if preload.queued {
		c.removeQueuedPreloadLocked(preload)
	}
	if preload.active && preload.done != nil {
		<-preload.done
	} else {
		preload.doneOnce.Do(func() {
			if preload.done != nil {
				close(preload.done)
			}
		})
	}
	preload.queued = false
	c.releasePreloadCapacityLocked(preload)
	c.clearPreloadProtectionLocked(preload)
	if dispatch {
		c.dispatchPreloadsLocked()
	}
}

func (c *Controller) removeQueuedPreloadLocked(preload *preloadTask) {
	for i, queued := range c.preloadQueue {
		if queued != preload {
			continue
		}
		copy(c.preloadQueue[i:], c.preloadQueue[i+1:])
		c.preloadQueue[len(c.preloadQueue)-1] = nil
		c.preloadQueue = c.preloadQueue[:len(c.preloadQueue)-1]
		return
	}
}

// releasePreloadCapacityLocked frees the scheduling slot so the next queued
// preload can start. The memory reservation is not freed here: a completed
// preload keeps pinning its cache, and releasePreloadBudgetLocked accounts for
// that until the protection is cleared.
func (c *Controller) releasePreloadCapacityLocked(preload *preloadTask) {
	if !preload.active {
		return
	}
	preload.active = false
	c.preloadActiveTasks--
}

// releasePreloadBudgetLocked returns the task's reservation to the stream pool.
// Idempotent: the first call clears the hook.
func (c *Controller) releasePreloadBudgetLocked(preload *preloadTask) {
	if preload.releaseBudget == nil {
		return
	}
	preload.releaseBudget()
	preload.releaseBudget = nil
}

// clearPreloadProtectionLocked unpins the preloaded cache and, with it, the
// memory reservation covering that cache.
func (c *Controller) clearPreloadProtectionLocked(preload *preloadTask) {
	c.releasePreloadBudgetLocked(preload)
	if !preload.protected {
		return
	}
	preload.protected = false
	if preload.clearProtection != nil {
		preload.clearProtection()
	}
}

// evictReadyPreloadLocked unpins one completed preload so a pending one can
// reserve its budget. A ready preload is opportunistic cache for a playback
// that may never start, so it yields to a preload that is actually waiting.
// Returns false when there is nothing left to give up.
func (c *Controller) evictReadyPreloadLocked(except *preloadTask) bool {
	var victim *preloadTask
	c.preloads.Range(func(_, val any) bool {
		p, ok := val.(*preloadTask)
		if !ok || p == nil || p == except || p.active || p.queued || !p.protected {
			return true
		}
		victim = p
		return false
	})
	if victim == nil {
		return false
	}
	c.preloads.Delete(victim.infoHash)
	c.releasePreloadLocked(victim, false)
	return true
}

func (c *Controller) cancelAllPreloads() {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	c.preloadQueue = nil
	c.preloads.Range(func(key, val any) bool {
		c.preloads.Delete(key)
		if p, ok := val.(*preloadTask); ok {
			c.releasePreloadLocked(p, false)
		}
		return true
	})
}
