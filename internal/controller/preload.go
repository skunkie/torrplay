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
	"math"
	"net/http"
	"path/filepath"
	"slices"
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
	// defaultPlaybackGracePeriod keeps a playback session open between the
	// separate HTTP range requests a player issues, such as the tail read for
	// an MP4 index or the reconnect after a seek.
	defaultPlaybackGracePeriod = 10 * time.Second
	defaultPreloadReadyTTL     = 5 * time.Minute
	preloadNoFileIndex         = -1
	// maxConcurrentPreloads caps preloads that are actively fetching. The
	// binding constraint is bandwidth rather than memory: each preload's
	// readahead spans its whole range, so every extra concurrent one splits
	// the download capacity the others need to become ready. Raising this does
	// not shrink an individual preload on a machine with memory to spare.
	maxConcurrentPreloads = 2

	// maxPreloadBytes bounds a single preload. It is a startup-latency limit,
	// not a memory one: no amount of configured memory makes anyone willing to
	// wait longer for playback to begin, so it deliberately does not scale with
	// MaxMemory. Aggregate preload memory is capped by the stream pool at
	// reservation time, not here.
	maxPreloadBytes = 32 << 20

	// minSharedPreloadBytes is the smallest per-preload share worth running
	// preloads concurrently for: a default head and tail boundary. Below it, a
	// single preload takes the whole capacity instead.
	minSharedPreloadBytes = 2 * int64(stream.DefaultFileBoundaryBytes)

	// preloadWorkerExitTimeout bounds how long playback waits for the preload
	// workers it cancelled to exit before its reader starts competing with
	// them for pieces. Cancelled workers normally exit within milliseconds.
	preloadWorkerExitTimeout = 5 * time.Second
)

type preloadTask struct {
	active        bool
	bytesRead     atomic.Int64
	cacheResident func() bool
	cancel        context.CancelFunc
	// completedBytes reports the bytes of the preload range held in verified
	// pieces. Pieces of the range share one priority and download in rarity
	// order, so a sequential reader can sit at zero while most of the range
	// is already complete.
	completedBytes func() int64
	ctx            context.Context
	done           chan struct{}
	doneOnce       sync.Once
	expiryTimer    *time.Timer
	fileIndex      int
	filePath       string
	infoHash       metainfo.Hash
	// progressFloor is the progress an interrupted predecessor had reached.
	// A re-queued task reports at least this much, so status progress does
	// not fall back while the resumed run re-reads cached pieces. It is set
	// before the task is published and never changes afterwards.
	progressFloor int64
	// progressHigh is the highest progress reported so far. Completed pieces
	// of a queued memory preload are not yet protected and can be evicted,
	// so reported progress holds this high-water mark instead of falling.
	progressHigh atomic.Int64
	queued       bool
	ready        atomic.Bool
	// readyAt orders completed memory preloads for eviction. It is written and
	// read while preloadsMu is held.
	readyAt time.Time
	// releaseBudget returns a memory preload's stream pool reservation, which
	// also ends the eviction protection of its cached pieces. It is nil when
	// no reservation is held.
	releaseBudget func()
	// requeue builds a fresh queued task for the same request. Playback uses
	// it to stop a running worker without abandoning the preload.
	requeue func() *preloadTask
	// reserveBudget reserves the preload's memory in the stream pool and
	// protects its pieces from eviction. It reports whether the reservation
	// fits.
	reserveBudget func() bool
	// reserveBytes is the memory reserved for the preload. It covers the whole
	// pieces that storage protects, which may exceed targetBytes when a range
	// boundary falls inside a piece.
	reserveBytes int64
	retired      chan struct{}
	retireOnce   sync.Once
	start        func()
	targetBytes  int64
	// torrent is the torrent instance the preload caches. A dropped or
	// re-added torrent with the same info hash is a different instance.
	torrent *torrent.Torrent
}

type preloadStatusSnapshot struct {
	completedBytes int64
	expiryTimer    *time.Timer
	progress       float32
	status         api.PreloadResponseStatus
	targetBytes    int64
}

// playbackKey identifies one file of a torrent being played.
type playbackKey struct {
	filePath string
	infoHash metainfo.Hash
}

// playbackSession tracks playback of one file across the separate HTTP range
// requests a player issues. It stays open for a grace period after its last
// request ends, so the gap between two ranges neither restarts paused preloads
// nor releases the cache that a ready preload handed to playback. It is
// guarded by preloadsMu.
type playbackSession struct {
	// closeTimer ends the session once the grace period passes with no
	// active request.
	closeTimer *time.Timer
	key        playbackKey
	// lease is the ready preload of this file whose protection and memory
	// reservation playback took over, or nil.
	lease *preloadTask
	// preloadRequests is Controller.preloadRequests when the session opened.
	preloadRequests uint64
	requests        int
}

// progressBytes returns the preload's progress, counting pieces of the range
// that completed ahead of the reader. It queries the torrent's piece states
// under the torrent client lock, so callers must not hold preloadsMu.
func (p *preloadTask) progressBytes() int64 {
	current := p.bytesRead.Load()
	if p.completedBytes != nil {
		current = max(current, p.completedBytes())
	}
	current = max(min(current, p.targetBytes), p.progressFloor)
	for {
		high := p.progressHigh.Load()
		if current <= high {
			return high
		}
		if p.progressHigh.CompareAndSwap(high, current) {
			return current
		}
	}
}

// reportedProgressBytes returns the progress already known without querying
// the torrent: the bytes read and the highest progress reported so far. It is
// safe to call while holding preloadsMu.
func (p *preloadTask) reportedProgressBytes() int64 {
	current := max(min(p.bytesRead.Load(), p.targetBytes), p.progressFloor)
	return max(current, p.progressHigh.Load())
}

func (p *preloadTask) isCacheResident() bool {
	return p.cacheResident == nil || p.cacheResident()
}

// retire signals that the task has left the preload registry, stopping its
// torrent watcher. It is safe to call more than once.
func (p *preloadTask) retire() {
	p.retireOnce.Do(func() {
		if p.retired != nil {
			close(p.retired)
		}
	})
}

// torrentClosed reports whether the torrent instance the task caches has
// closed, which leaves nothing behind for a ready preload to report.
func (p *preloadTask) torrentClosed() bool {
	if p.torrent == nil {
		return false
	}
	select {
	case <-p.torrent.Closed():
		return true
	default:
		return false
	}
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

	var (
		to  *torrent.Torrent
		err error
	)

	if req.Magnet != nil && *req.Magnet != "" {
		to, err = c.torrentFromMagnetParam(*req.Magnet, ih)
		if err != nil {
			api.HandleError(w, err)
			return
		}
	} else {
		var ok bool
		to, ok = c.clientTorrent(ih)
		if !ok {
			t, getErr := c.db.GetTorrent(ih)
			if getErr != nil {
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

	if _, ok := c.clientTorrent(ih); !ok {
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

	if _, ok := c.clientTorrent(ih); !ok {
		if _, err := c.db.GetTorrent(ih); err != nil {
			api.HTTPError(w, "torrent not found", http.StatusNotFound)
			return
		}
	}

	c.cancelPreload(ih)
	w.WriteHeader(http.StatusNoContent)
}

func (c *Controller) getPreloadStatus(ih metainfo.Hash) api.PreloadResponse {
	to, hasTorrent := c.clientTorrent(ih)
	var completedLength int64
	fullyComplete := false
	if hasTorrent && to != nil && to.Info() != nil {
		completedLength = to.Length()
		fullyComplete = to.BytesCompleted() == completedLength
	}

	base := api.PreloadResponse{
		FileIndex: preloadNoFileIndex,
		Status:    api.Idle,
	}
	if hasTorrent && to != nil {
		rate, _ := peerTransferRates(to)
		if !math.IsNaN(rate) && !math.IsInf(rate, 0) {
			base.DownloadRate = int64(rate)
		}
		stats := to.Stats()
		base.ActivePeers = stats.ActivePeers
		base.TotalPeers = stats.TotalPeers
	}

	c.preloadsMu.Lock()
	resp, running := c.preloadStatusLocked(ih, base, completedLength, fullyComplete)
	c.preloadsMu.Unlock()
	if running == nil {
		return resp
	}

	// Progress reads piece states under the torrent client lock, so it is
	// computed after preloadsMu is released: a busy client must not stall
	// playback and other status requests waiting on preloadsMu.
	currentBytes := running.progressBytes()
	if running.ctx != nil && running.ctx.Err() != nil {
		// The task finished or was cancelled meanwhile, so it may no longer
		// be the current preload. Report the current state instead, taking
		// any running task's progress from what is already known.
		c.preloadsMu.Lock()
		resp, running = c.preloadStatusLocked(ih, base, completedLength, fullyComplete)
		c.preloadsMu.Unlock()
		if running == nil {
			return resp
		}
		currentBytes = running.reportedProgressBytes()
	}
	resp.CompletedBytes = currentBytes
	if running.targetBytes > 0 {
		resp.Progress = min(1.0, float32(currentBytes)/float32(running.targetBytes))
	}
	return resp
}

// preloadStatusLocked builds the preload status from base. For a preload
// still running, it returns the task with the response lacking progress,
// which the caller computes without holding preloadsMu.
func (c *Controller) preloadStatusLocked(ih metainfo.Hash, base api.PreloadResponse, completedLength int64, fullyComplete bool) (api.PreloadResponse, *preloadTask) {
	if p := c.currentPreloadLocked(ih); p != nil {
		if p.ready.Load() {
			return readyPreloadResponse(base, p), nil
		}

		resp := base
		resp.FileIndex = p.fileIndex
		resp.FilePath = &p.filePath
		resp.Status = api.Preloading
		resp.TargetBytes = p.targetBytes
		return resp, p
	}
	if lease := c.playbackLeaseLocked(ih); lease != nil {
		return readyPreloadResponse(base, lease), nil
	}
	if val, ok := c.preloadSnapshots.Load(ih); ok {
		if snapshot, isSnapshot := val.(*preloadStatusSnapshot); isSnapshot && snapshot != nil {
			if fullyComplete {
				resp := base
				resp.CompletedBytes = completedLength
				resp.Progress = 1
				resp.Status = api.Ready
				resp.TargetBytes = completedLength
				return resp, nil
			}
			resp := base
			resp.CompletedBytes = snapshot.completedBytes
			resp.Progress = snapshot.progress
			resp.Status = snapshot.status
			resp.TargetBytes = snapshot.targetBytes
			return resp, nil
		}
	}

	if fullyComplete {
		resp := base
		resp.CompletedBytes = completedLength
		resp.Progress = 1.0
		resp.Status = api.Ready
		resp.TargetBytes = completedLength
		return resp, nil
	}

	return base, nil
}

// readyPreloadResponse reports a completed preload, including one that playback
// currently holds.
func readyPreloadResponse(base api.PreloadResponse, p *preloadTask) api.PreloadResponse {
	resp := base
	resp.CompletedBytes = p.targetBytes
	resp.FileIndex = p.fileIndex
	resp.FilePath = &p.filePath
	resp.Progress = 1.0
	resp.Status = api.Ready
	resp.TargetBytes = p.targetBytes
	return resp
}

func (c *Controller) startPreload(to *torrent.Torrent, file *torrent.File, fileIndex int) *preloadTask {
	if to == nil || to.Info() == nil || file == nil {
		return nil
	}
	if c.torrentClientUnavailable.Load() {
		return nil
	}

	ih := to.InfoHash()
	c.mu.RLock()
	pool := c.streamPool.Load()
	storageClient := c.storageClient.Load()
	generation := c.torrentGeneration.Load()
	var currentTorrent *torrent.Torrent
	current := false
	if c.client != nil {
		currentTorrent, current = c.client.Torrent(ih)
	}
	c.mu.RUnlock()
	if pool == nil || !current || currentTorrent != to {
		return nil
	}

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
	if c.torrentClientUnavailable.Load() || generation != c.torrentGeneration.Load() {
		c.preloadsMu.Unlock()
		return nil
	}
	if session := c.playbackSessions[playbackKey{infoHash: ih, filePath: file.Path()}]; session != nil {
		// A player reopening the file it just played finds the preload still
		// held by that playback session, so there is nothing to fetch again.
		if lease := c.validPlaybackLeaseLocked(session); lease != nil && lease.torrent == to {
			c.preloadsMu.Unlock()
			return lease
		}
		// The file is playing, so playback is already fetching it. A preload
		// would only compete with it and, as a new request, end the session.
		// Keep the status snapshot, which still describes the last preload.
		if session.requests > 0 {
			c.preloadsMu.Unlock()
			return nil
		}
	}
	c.clearPreloadSnapshotLocked(ih)
	if current, ok := c.preloads.Load(ih); ok {
		if p, ok := current.(*preloadTask); ok && p != nil && p.torrent == to && p.fileIndex == fileIndex && !p.torrentClosed() {
			c.preloadsMu.Unlock()
			return p
		}
	}
	// File-storage preloads are written to disk and reserve no memory, so only
	// the startup-latency ceiling bounds them.
	limit := int64(maxPreloadBytes)
	if mode == stream.MemoryStorage {
		limit = preloadMemoryLimit(pool.PreloadCapacity())
	}
	preloadBudget := min(file.Length(), limit)
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
	if mode == stream.MemoryStorage {
		// Fit against the memory limit rather than preloadBudget, which is
		// also capped by the file length: a file smaller than one piece still
		// occupies the whole piece.
		readerStartEnd, readerEndStart, readerEndEnd = fitPreloadRanges(file, limit, readerStartEnd, readerEndStart, readerEndEnd)
		targetBytes = readerStartEnd + (readerEndEnd - readerEndStart)
	}
	headStart, headEnd, tailStart, tailEnd, hasProtection := stream.FilePieceRanges(file, readerStartEnd, readerEndStart, readerEndEnd)
	if !hasProtection {
		// Not even one piece fits the memory limit.
		if old, ok := c.preloads.LoadAndDelete(ih); ok {
			if p, ok := old.(*preloadTask); ok {
				c.releasePreloadLocked(p, true)
			}
		}
		c.preloadsMu.Unlock()
		return nil
	}
	reserveBytes := stream.BoundaryPieceBytes(to.Info(), headStart, headEnd, tailStart, tailEnd)
	protectedPieces := stream.BoundaryPieces(headStart, headEnd, tailStart, tailEnd)

	if old, ok := c.preloads.LoadAndDelete(ih); ok {
		if p, ok := old.(*preloadTask); ok {
			c.releasePreloadLocked(p, true)
		}
	}

	var newTask func() *preloadTask
	newTask = func() *preloadTask {
		ctx, cancel := context.WithCancel(context.Background())
		preload := &preloadTask{
			cacheResident: func() bool {
				if mode != stream.MemoryStorage || storageClient == nil {
					return true
				}
				cached, err := storageClient.PiecesCached(ih, protectedPieces)
				return err == nil && cached
			},
			cancel: cancel,
			completedBytes: func() int64 {
				return preloadCompletedBytes(file, readerStartEnd, readerEndStart, readerEndEnd)
			},
			ctx:          ctx,
			done:         make(chan struct{}),
			fileIndex:    fileIndex,
			filePath:     file.Path(),
			infoHash:     ih,
			queued:       true,
			requeue:      newTask,
			reserveBytes: reserveBytes,
			retired:      make(chan struct{}),
			targetBytes:  targetBytes,
			torrent:      to,
		}
		preload.reserveBudget = func() bool {
			if mode != stream.MemoryStorage {
				return true
			}
			return pool.ReservePreload(file, readerStartEnd, readerEndStart, readerEndEnd)
		}
		if mode == stream.MemoryStorage {
			preload.releaseBudget = func() { pool.ReleasePreload(ih) }
		}
		preload.start = func() {
			c.runPreload(preload, file, pool, mode, readerStartEnd, readerEndStart, readerEndEnd)
		}
		return preload
	}
	preload := newTask()
	to.AllowDataDownload()
	c.registerPreloadLocked(preload, false)
	// An explicit preload request means the viewer has moved on from any
	// playback that is only waiting out its grace period. Close those sessions
	// now, or the player waiting on this preload would wait for them too.
	c.preloadRequests++
	c.closeIdlePlaybackSessionsLocked()
	c.dispatchPreloadsLocked()
	c.preloadsMu.Unlock()
	return preload
}

// registerPreloadLocked records a queued task as the torrent's current preload
// and watches its torrent. A task placed at the front runs before tasks that
// were requested after it. The caller must hold preloadsMu.
func (c *Controller) registerPreloadLocked(preload *preloadTask, front bool) {
	c.preloads.Store(preload.infoHash, preload)
	if front {
		c.preloadQueue = append([]*preloadTask{preload}, c.preloadQueue...)
	} else {
		c.preloadQueue = append(c.preloadQueue, preload)
	}
	go c.watchPreloadTorrent(preload)
}

// watchPreloadTorrent removes a preload when its torrent instance closes.
// Torrents are dropped by paths that do not know about preloads, such as
// storage migration and client reconfiguration; without this, a ready task
// would keep reporting Ready and holding its reservation for an empty cache.
func (c *Controller) watchPreloadTorrent(preload *preloadTask) {
	select {
	case <-preload.torrent.Closed():
		c.removePreload(preload.infoHash, preload)
	case <-preload.retired:
	}
}

// dispatchPreloadsLocked starts queued preloads in FIFO order. Concurrency is
// capped by maxConcurrentPreloads; memory is accounted solely by the stream
// pool, whose ReservePreload caps aggregate preload reservations against
// the same budget streaming readahead draws from. Keeping a second byte
// accounting here would let a task pass one gate and fail the other. Completed
// preloads still hold a reservation for the cache they pin, so admission may
// have to evict one of them first.
func (c *Controller) dispatchPreloadsLocked() {
	// Preload range readers read ahead across their complete range, so keep
	// queued work registered but do not let it compete with live playback for
	// bandwidth. The final playback release resumes this queue.
	if c.preloadPlaybackCount > 0 {
		return
	}
	for len(c.preloadWorkers) < maxConcurrentPreloads && len(c.preloadQueue) > 0 {
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
			if len(c.preloadWorkers) > 0 {
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
		c.preloadWorkers = append(c.preloadWorkers, preload)
		go preload.start()
	}
}

// dropUndispatchablePreloadLocked abandons a queued preload that can never be
// admitted, so it does not block the rest of the FIFO queue indefinitely. The
// task must already have been removed from c.preloadQueue.
func (c *Controller) dropUndispatchablePreloadLocked(preload *preloadTask, reason string) {
	if logger := c.logger.Load(); logger != nil {
		logger.Warn("torrent preload dropped", "hash", preload.infoHash, "reason", reason, "bytes", preload.reserveBytes)
	}
	c.preloads.CompareAndDelete(preload.infoHash, preload)
	preload.retire()
	c.releasePreloadBudgetLocked(preload)
	preload.queued = false
	preload.doneOnce.Do(func() { close(preload.done) })
	preload.cancel()
}

func (c *Controller) runPreload(preload *preloadTask, file *torrent.File, pool *stream.Pool, mode stream.StorageMode, readerStartEnd, readerEndStart, readerEndEnd int64) {
	completed := false
	var preloadErr error
	defer func() {
		preload.doneOnce.Do(func() { close(preload.done) })
		c.finishPreload(preload, completed && preload.ctx.Err() == nil, preloadErr != nil)
	}()

	activeDownloader := c.downloader.Load()
	if activeDownloader != nil {
		activeDownloader.AddStreaming(preload.infoHash)
		defer activeDownloader.RemoveStreaming(preload.infoHash)
	}

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

	for range workers {
		if err := <-results; err != nil && !errors.Is(err, context.Canceled) && preloadErr == nil {
			preloadErr = err
			preload.cancel()
		}
	}
	if preloadErr != nil && preload.torrentClosed() {
		// Readers of a dropped torrent fail with a closed-torrent error. The
		// torrent watcher retires the task; it is not a preload failure.
		preloadErr = nil
		return
	}
	if preloadErr != nil {
		c.logger.Load().Warn("torrent preload failed", "hash", preload.infoHash, "error", preloadErr)
		return
	}
	if preload.ctx.Err() != nil {
		return
	}

	preload.bytesRead.Store(preload.targetBytes)
	preload.ready.Store(true)
	completed = true
}

// preloadMemoryLimit returns the bytes one memory-storage preload may protect,
// given the stream pool's total preload capacity. Preloads run concurrently
// with an even share each when that share still covers a default head and tail
// boundary; otherwise one preload uses the whole capacity and later requests
// wait for it, because splitting would leave each too small to be useful. The
// startup-latency ceiling applies either way.
func preloadMemoryLimit(capacity int64) int64 {
	limit := capacity / maxConcurrentPreloads
	if limit < minSharedPreloadBytes {
		limit = capacity
	}
	return max(min(limit, maxPreloadBytes), 0)
}

// finishPreload retires a worker. A current task that completed stays
// registered as ready; one that failed leaves a Failed status snapshot.
func (c *Controller) finishPreload(preload *preloadTask, completed, failed bool) {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()

	c.releasePreloadCapacityLocked(preload)
	current, ok := c.preloads.Load(preload.infoHash)
	if !ok || current != preload {
		c.releasePreloadBudgetLocked(preload)
		c.dispatchPreloadsLocked()
		preload.cancel()
		return
	}
	if completed {
		preload.readyAt = time.Now()
		c.schedulePreloadExpiryLocked(preload)
	} else {
		c.preloads.Delete(preload.infoHash)
		preload.retire()
		c.releasePreloadBudgetLocked(preload)
		if failed {
			c.snapshotPreloadLocked(preload, api.Failed)
		}
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

// preloadCompletedBytes returns the bytes of the head range [0, headEnd) and
// the tail range [tailStart, tailEnd) that lie in complete pieces.
func preloadCompletedBytes(file *torrent.File, headEnd, tailStart, tailEnd int64) int64 {
	if file == nil || file.Torrent() == nil || file.Torrent().Info() == nil {
		return 0
	}
	tor := file.Torrent()
	pieceLength := tor.Info().PieceLength
	pieceComplete := func(index int) bool { return tor.PieceState(index).Complete }
	return completedRangeBytes(pieceComplete, pieceLength, file.Offset(), 0, headEnd) +
		completedRangeBytes(pieceComplete, pieceLength, file.Offset(), tailStart, tailEnd)
}

// completedRangeBytes returns the bytes of the file-relative range
// [start, end) that lie in complete pieces of a file beginning at fileOffset.
func completedRangeBytes(pieceComplete func(int) bool, pieceLength, fileOffset, start, end int64) int64 {
	if end <= start || pieceLength <= 0 {
		return 0
	}
	begin := fileOffset + start
	finish := fileOffset + end
	var total int64
	for index := begin / pieceLength; index*pieceLength < finish; index++ {
		if pieceComplete(int(index)) {
			total += min(finish, (index+1)*pieceLength) - max(begin, index*pieceLength)
		}
	}
	return total
}

// fitPreloadRanges trims the head range [0, headEnd) and the tail range
// [tailStart, tailEnd) until the whole pieces they touch fit within budget.
// Storage protects and evicts complete pieces, so a range ending one byte into
// a piece costs that entire piece, and byte-sized ranges can exceed the
// preload's admission capacity. Each step drops the last piece of the head or
// the first piece of the tail, whichever range spans more pieces, keeping at
// least one head piece while a tail remains. A returned headEnd of zero means
// that not even one piece fits.
func fitPreloadRanges(file *torrent.File, budget, headEnd, tailStart, tailEnd int64) (int64, int64, int64) {
	info := file.Torrent().Info()
	pieceLength := max(info.PieceLength, 1)
	fileOffset := file.Offset()
	for headEnd > 0 {
		headStartPiece, headEndPiece, tailStartPiece, tailEndPiece, ok := stream.FilePieceRanges(file, headEnd, tailStart, tailEnd)
		if !ok || stream.BoundaryPieceBytes(info, headStartPiece, headEndPiece, tailStartPiece, tailEndPiece) <= budget {
			break
		}
		headPieces := headEndPiece - headStartPiece + 1
		tailPieces := tailEndPiece - tailStartPiece + 1
		if tailEnd > tailStart && (tailPieces > headPieces || headPieces == 1) {
			// Start the tail at the next piece boundary, dropping it when
			// nothing is left.
			tailStart = int64(tailStartPiece+1)*pieceLength - fileOffset
			if tailStart >= tailEnd {
				tailStart, tailEnd = 0, 0
			}
			continue
		}
		// End the head where its last piece begins; an empty head means that
		// the budget cannot hold a single piece.
		headEnd = max(int64(headEndPiece)*pieceLength-fileOffset, 0)
	}
	if headEnd <= 0 {
		return 0, 0, 0
	}
	return headEnd, tailStart, tailEnd
}

// currentPreloadLocked returns the registered preload after discarding tasks
// whose torrent or ready memory cache no longer exists. The caller must hold
// preloadsMu.
func (c *Controller) currentPreloadLocked(ih metainfo.Hash) *preloadTask {
	value, ok := c.preloads.Load(ih)
	if !ok {
		return nil
	}
	preload, ok := value.(*preloadTask)
	if !ok || preload == nil {
		return nil
	}
	if !preload.torrentClosed() && (!preload.ready.Load() || preload.isCacheResident()) {
		return preload
	}
	if c.preloads.CompareAndDelete(ih, preload) {
		c.releasePreloadLocked(preload, true)
	}
	return nil
}

func (c *Controller) cancelPreload(ih metainfo.Hash) {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	c.clearPreloadSnapshotLocked(ih)
	for key, session := range c.playbackSessions {
		if key.infoHash == ih {
			c.releasePlaybackLeaseLocked(session)
		}
	}
	if val, ok := c.preloads.LoadAndDelete(ih); ok {
		if p, ok := val.(*preloadTask); ok {
			c.releasePreloadLocked(p, true)
		}
	}
}

func (c *Controller) clearPreloadSnapshotLocked(ih metainfo.Hash) {
	val, ok := c.preloadSnapshots.LoadAndDelete(ih)
	if !ok {
		return
	}
	if snapshot, isSnapshot := val.(*preloadStatusSnapshot); isSnapshot && snapshot != nil && snapshot.expiryTimer != nil {
		snapshot.expiryTimer.Stop()
	}
}

func (c *Controller) snapshotPreloadLocked(preload *preloadTask, status api.PreloadResponseStatus) {
	if preload == nil {
		return
	}
	c.clearPreloadSnapshotLocked(preload.infoHash)
	completedBytes := preload.reportedProgressBytes()
	progress := float32(0)
	if preload.targetBytes > 0 {
		progress = min(1, float32(completedBytes)/float32(preload.targetBytes))
	}
	if preload.ready.Load() {
		completedBytes = preload.targetBytes
		progress = 1
	}
	snapshot := &preloadStatusSnapshot{
		completedBytes: completedBytes,
		progress:       progress,
		status:         status,
		targetBytes:    preload.targetBytes,
	}
	c.preloadSnapshots.Store(preload.infoHash, snapshot)
	if c.preloadReadyTTL > 0 {
		snapshot.expiryTimer = time.AfterFunc(c.preloadReadyTTL, func() {
			c.preloadsMu.Lock()
			defer c.preloadsMu.Unlock()
			c.preloadSnapshots.CompareAndDelete(preload.infoHash, snapshot)
		})
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

// releasePreloadLocked retires a task that the caller has already removed
// from c.preloads. Its memory reservation and cache protection are released
// immediately. A running worker is only cancelled, not awaited: it exits
// through finishPreload, which frees its scheduling slot and dispatches the
// next task. Waiting here would hold preloadsMu while the worker's reader
// closes under the torrent client lock, stalling status requests and
// playback. The caller must hold preloadsMu.
func (c *Controller) releasePreloadLocked(preload *preloadTask, dispatch bool) {
	if preload.expiryTimer != nil {
		preload.expiryTimer.Stop()
		preload.expiryTimer = nil
	}
	if preload.cancel != nil {
		preload.cancel()
	}
	preload.retire()
	if preload.queued {
		c.removeQueuedPreloadLocked(preload)
	}
	if !preload.active {
		preload.doneOnce.Do(func() {
			if preload.done != nil {
				close(preload.done)
			}
		})
	}
	preload.queued = false
	c.releasePreloadBudgetLocked(preload)
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
	c.preloadWorkers = slices.DeleteFunc(c.preloadWorkers, func(worker *preloadTask) bool { return worker == preload })
}

// releasePreloadBudgetLocked returns the task's reservation to the stream pool,
// which unpins its cached pieces. Idempotent: the first call clears the hook.
func (c *Controller) releasePreloadBudgetLocked(preload *preloadTask) {
	if preload.releaseBudget == nil {
		return
	}
	preload.releaseBudget()
	preload.releaseBudget = nil
}

// evictReadyPreloadLocked unpins one completed preload so a pending one can
// reserve its budget. A ready preload is opportunistic cache for a playback
// that may never start, so it yields to a preload that is actually waiting.
// The evicted task leaves a Superseded status snapshot. Returns false when
// there is nothing left to give up.
func (c *Controller) evictReadyPreloadLocked(except *preloadTask) bool {
	var victim *preloadTask
	c.preloads.Range(func(_, val any) bool {
		p, ok := val.(*preloadTask)
		if !ok || p == nil || p == except || p.active || p.queued || !p.ready.Load() || p.releaseBudget == nil {
			return true
		}
		if victim == nil || p.readyAt.Before(victim.readyAt) {
			victim = p
		}
		return true
	})
	if victim == nil {
		return false
	}
	c.preloads.Delete(victim.infoHash)
	c.releasePreloadLocked(victim, false)
	c.snapshotPreloadLocked(victim, api.Superseded)
	return true
}

// releaseOnePreloadReservationLocked gives up one preload memory reservation
// so the stream readahead budget can shrink. It releases the cheapest first:
// a ready preload nobody is playing, then a running preload, and last the
// lease a playback session holds. It returns false when no reservation is
// left. The caller must hold preloadsMu.
func (c *Controller) releaseOnePreloadReservationLocked() bool {
	if c.evictReadyPreloadLocked(nil) {
		return true
	}
	var running *preloadTask
	c.preloads.Range(func(_, val any) bool {
		if p, ok := val.(*preloadTask); ok && p != nil && p.active && p.releaseBudget != nil {
			running = p
			return false
		}
		return true
	})
	if running != nil {
		c.preloads.CompareAndDelete(running.infoHash, running)
		c.releasePreloadLocked(running, false)
		c.snapshotPreloadLocked(running, api.Superseded)
		return true
	}
	for _, session := range c.playbackSessions {
		if session.lease != nil {
			c.releasePlaybackLeaseLocked(session)
			return true
		}
	}
	return false
}

// preparePreloadsForPlaybackLocked retires the preload for the file that is
// about to play, cancels unrelated workers that are still downloading, and
// releases the cache held by ready memory preloads. Cancelled workers are
// re-queued ahead of later requests and resume when the playback session
// ends. A ready memory preload for the file being played is returned so the
// session can hold its protection and memory reservation; every other retired
// task releases them and leaves an immutable, short-lived Superseded status
// snapshot. Ready file-storage preloads unrelated to this playback do not
// reserve memory and may remain cached; queued requests remain registered
// until playback ends. The caller must hold preloadsMu and increment
// preloadPlaybackCount before calling this method.
func (c *Controller) preparePreloadsForPlaybackLocked(infoHash metainfo.Hash, filePath string) *preloadTask {
	var matchedPreload *preloadTask
	var retainedProtection *preloadTask
	if current, ok := c.preloads.Load(infoHash); ok {
		preload, isPreload := current.(*preloadTask)
		if isPreload && preload != nil && preload.filePath == filePath &&
			c.preloads.CompareAndDelete(infoHash, preload) {
			matchedPreload = preload
			ready := preload.ready.Load()
			cacheResident := !ready || preload.isCacheResident()
			if ready && preload.releaseBudget != nil && cacheResident {
				if preload.expiryTimer != nil {
					preload.expiryTimer.Stop()
					preload.expiryTimer = nil
				}
				preload.retire()
				retainedProtection = preload
			} else {
				c.releasePreloadLocked(preload, false)
				if cacheResident {
					c.snapshotPreloadLocked(preload, api.Superseded)
				}
			}
		}
	}

	var interrupted []*preloadTask
	c.preloads.Range(func(key, value any) bool {
		preload, ok := value.(*preloadTask)
		if !ok || preload == nil || preload == matchedPreload || preload.queued {
			return true
		}
		// Active workers always yield bandwidth. A non-active task with a
		// releaseBudget hook is a completed memory preload that still reserves
		// and protects cache capacity, so it must yield that capacity as well.
		if !preload.active && preload.releaseBudget == nil {
			return true
		}
		if preload.active && preload.requeue != nil {
			interrupted = append(interrupted, preload)
			return true
		}
		c.preloads.Delete(key)
		c.releasePreloadLocked(preload, false)
		c.snapshotPreloadLocked(preload, api.Superseded)
		return true
	})

	// Register each replacement before cancelling the worker so the worker's
	// completion sees that it was superseded and leaves the replacement alone.
	// Iterate in reverse so front insertion preserves the order found above.
	for _, preload := range slices.Backward(interrupted) {
		replacement := preload.requeue()
		replacement.progressFloor = preload.reportedProgressBytes()
		c.registerPreloadLocked(replacement, true)
		c.releasePreloadLocked(preload, false)
	}
	return retainedProtection
}

// beginPlaybackLocked registers an HTTP playback request for a file and
// returns its session. The first request of a session pauses preload dispatch
// and takes over a ready preload of the file; later requests within the grace
// period join the open session. Starting a new session closes idle sessions,
// whose leases hold budget the new playback needs. The caller must hold
// preloadsMu and pass the session to endPlaybackRequestLocked when the request
// ends.
func (c *Controller) beginPlaybackLocked(ih metainfo.Hash, filePath string) *playbackSession {
	key := playbackKey{infoHash: ih, filePath: filePath}
	if session := c.playbackSessions[key]; session != nil {
		session.requests++
		c.stopPlaybackCloseTimerLocked(session)
		// The torrent may have been replaced during the grace period, which
		// clears the lease's storage protection while its reservation would
		// still suppress the new reader's own boundaries.
		c.validPlaybackLeaseLocked(session)
		return session
	}
	// Count the new session first so closing idle ones cannot resume dispatch.
	c.preloadPlaybackCount++
	c.closeIdlePlaybackSessionsLocked()
	if c.playbackSessions == nil {
		c.playbackSessions = make(map[playbackKey]*playbackSession)
	}
	session := &playbackSession{key: key, preloadRequests: c.preloadRequests, requests: 1}
	session.lease = c.preparePreloadsForPlaybackLocked(ih, filePath)
	c.playbackSessions[key] = session
	return session
}

// endPlaybackRequestLocked ends one request of a playback session. The session
// closes once the grace period passes without a new request for its file. The
// caller must hold preloadsMu.
func (c *Controller) endPlaybackRequestLocked(session *playbackSession) {
	session.requests--
	if session.requests > 0 {
		return
	}
	// A preload requested during the session means the viewer moved on, and
	// that preload is waiting for this session to end.
	grace := c.runtimeConfig.playbackGracePeriod
	if grace <= 0 || c.preloadRequests != session.preloadRequests {
		c.closePlaybackSessionLocked(session)
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(grace, func() {
		c.preloadsMu.Lock()
		defer c.preloadsMu.Unlock()
		// A request that joined in the meantime replaced or cleared the timer.
		if session.closeTimer == timer {
			c.closePlaybackSessionLocked(session)
		}
	})
	session.closeTimer = timer
}

// closeIdlePlaybackSessionsLocked closes sessions that are only waiting out
// their grace period. The caller must hold preloadsMu.
func (c *Controller) closeIdlePlaybackSessionsLocked() {
	for _, session := range c.playbackSessions {
		if session.requests == 0 {
			c.closePlaybackSessionLocked(session)
		}
	}
}

// closePlaybackSessionLocked releases a session's lease and resumes preload
// dispatch once no session remains. It is a no-op for a closed session. The
// caller must hold preloadsMu.
func (c *Controller) closePlaybackSessionLocked(session *playbackSession) {
	if c.playbackSessions[session.key] != session {
		return
	}
	delete(c.playbackSessions, session.key)
	c.stopPlaybackCloseTimerLocked(session)
	c.releasePlaybackLeaseLocked(session)
	c.preloadPlaybackCount--
	c.dispatchPreloadsLocked()
}

// playbackLeaseLocked returns the ready preload a playback session holds for
// the torrent. The caller must hold preloadsMu.
func (c *Controller) playbackLeaseLocked(ih metainfo.Hash) *preloadTask {
	for key, session := range c.playbackSessions {
		if key.infoHash != ih {
			continue
		}
		if lease := c.validPlaybackLeaseLocked(session); lease != nil {
			return lease
		}
	}
	return nil
}

// releasePlaybackLeaseLocked returns a session's leased preload cache to the
// pool. The caller must hold preloadsMu.
func (c *Controller) releasePlaybackLeaseLocked(session *playbackSession) {
	if session.lease == nil {
		return
	}
	c.releasePreloadLocked(session.lease, false)
	session.lease = nil
}

func (c *Controller) stopPlaybackCloseTimerLocked(session *playbackSession) {
	if session.closeTimer != nil {
		session.closeTimer.Stop()
		session.closeTimer = nil
	}
}

// validPlaybackLeaseLocked returns the session's lease, first releasing it if
// its torrent closed or its cache was evicted. The caller must hold
// preloadsMu.
func (c *Controller) validPlaybackLeaseLocked(session *playbackSession) *preloadTask {
	if session.lease == nil {
		return nil
	}
	if session.lease.torrentClosed() || !session.lease.isCacheResident() {
		c.releasePlaybackLeaseLocked(session)
		return nil
	}
	return session.lease
}

// cancelAllPreloads cancels every preload and waits for running workers to
// exit, so none still reads from the client, pool, or storage its caller is
// about to close. It waits after releasing preloadsMu, which the exiting
// workers take in finishPreload.
func (c *Controller) cancelAllPreloads() {
	c.preloadsMu.Lock()
	c.cancelAllPreloadsLocked()
	running := c.preloadWorkerDonesLocked()
	c.preloadsMu.Unlock()

	for _, done := range running {
		<-done
	}
}

// preloadWorkerDonesLocked returns the done channels of every running
// preload worker, including cancelled workers still exiting. The caller must
// hold preloadsMu, and must release it before waiting on them.
func (c *Controller) preloadWorkerDonesLocked() []<-chan struct{} {
	dones := make([]<-chan struct{}, 0, len(c.preloadWorkers))
	for _, worker := range c.preloadWorkers {
		if worker.done != nil {
			dones = append(dones, worker.done)
		}
	}
	return dones
}

// waitForPreloadWorkers waits until the given workers exit, the context ends,
// or preloadWorkerExitTimeout passes. It bounds the wait so a worker stuck in
// a read cannot hold up playback.
func waitForPreloadWorkers(ctx context.Context, dones []<-chan struct{}) {
	if len(dones) == 0 {
		return
	}
	timeout := time.NewTimer(preloadWorkerExitTimeout)
	defer timeout.Stop()
	for _, done := range dones {
		select {
		case <-done:
		case <-ctx.Done():
			return
		case <-timeout.C:
			return
		}
	}
}

// cancelAllPreloadsLocked releases every speculative cache lease and priority
// claim, including leases held by playback, and closes idle playback sessions.
// Sessions with requests in flight stay open so dispatch remains paused until
// those requests end. The caller must hold preloadsMu.
func (c *Controller) cancelAllPreloadsLocked() {
	c.preloadQueue = nil
	for _, session := range c.playbackSessions {
		c.releasePlaybackLeaseLocked(session)
	}
	c.closeIdlePlaybackSessionsLocked()
	c.preloadSnapshots.Range(func(key, val any) bool {
		c.preloadSnapshots.Delete(key)
		if snapshot, ok := val.(*preloadStatusSnapshot); ok && snapshot != nil && snapshot.expiryTimer != nil {
			snapshot.expiryTimer.Stop()
		}
		return true
	})
	c.preloads.Range(func(key, val any) bool {
		c.preloads.Delete(key)
		if p, ok := val.(*preloadTask); ok {
			c.releasePreloadLocked(p, false)
		}
		return true
	})
}
