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
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/media"
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
	preloadReaderIDBase uint64 = 1<<63 - 1
)

type preloadByteRange struct {
	end   int64
	start int64
}

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
	ranges          []preloadByteRange
	active          bool
	protected       bool
	queued          bool
	bytesRead       atomic.Int64
	ready           atomic.Bool
}

type preloadStatusSnapshot struct {
	completedBytes int64
	expiryTimer    *time.Timer
	progress       float32
	targetBytes    int64
}

type preloadSeekReaderAt struct {
	mu     sync.Mutex
	reader io.ReadSeeker
}

func (reader *preloadSeekReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if _, err := reader.reader.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(reader.reader, buffer)
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
		to, ok = c.client.Torrent(ih)
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

	playbackPositionSeconds, err := validatePreloadPosition(req)
	if err != nil {
		api.HTTPError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var playbackOffset *int64
	if playbackPositionSeconds > 0 {
		if offset, ok := c.resolvePreloadOffset(r.Context(), files[targetIdx], playbackPositionSeconds); ok {
			playbackOffset = &offset
		}
	}

	c.startPreload(to, files[targetIdx], targetIdx, playbackOffset)

	resp := c.getPreloadStatus(ih)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func validatePreloadPosition(req api.PreloadRequest) (float64, error) {
	positionSeconds := 0.0
	if req.PlaybackPositionSeconds != nil {
		positionSeconds = *req.PlaybackPositionSeconds
		if math.IsNaN(positionSeconds) || math.IsInf(positionSeconds, 0) || positionSeconds < 0 {
			return 0, errors.New("playback_position_seconds must be a finite number greater than or equal to 0")
		}
	}
	return positionSeconds, nil
}

func (c *Controller) resolvePreloadOffset(ctx context.Context, file *torrent.File, positionSeconds float64) (int64, bool) {
	if file == nil || positionSeconds <= 0 {
		return 0, false
	}
	reader := file.NewReader()
	reader.SetContext(ctx)
	reader.SetReadahead(1 << 20)
	reader.SetResponsive()
	defer reader.Close()

	offset, ok, err := media.ResolvePlaybackOffset(
		&preloadSeekReaderAt{reader: reader},
		file.Length(),
		file.Path(),
		positionSeconds,
	)
	if err != nil {
		c.logger.Debug("failed to resolve preload playback position",
			"error", err,
			"file", file.Path(),
			"positionSeconds", positionSeconds)
		return 0, false
	}
	return offset, ok
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
	defer c.preloadsMu.Unlock()

	if val, preloading := c.preloads.Load(ih); preloading {
		if p, ok := val.(*preloadTask); ok && p != nil {
			if p.ready.Load() {
				resp := base
				resp.CompletedBytes = p.targetBytes
				resp.FileIndex = p.fileIndex
				resp.FilePath = &p.filePath
				resp.Progress = 1.0
				resp.Status = api.Ready
				resp.TargetBytes = p.targetBytes
				return resp
			}

			currentBytes := p.progressBytes()

			progress := float32(0)
			if p.targetBytes > 0 {
				progress = min(1.0, float32(currentBytes)/float32(p.targetBytes))
			}
			resp := base
			resp.CompletedBytes = currentBytes
			resp.FileIndex = p.fileIndex
			resp.FilePath = &p.filePath
			resp.Progress = progress
			resp.Status = api.Preloading
			resp.TargetBytes = p.targetBytes
			return resp
		}
	}
	if val, ok := c.preloadSnapshots.Load(ih); ok {
		if snapshot, isSnapshot := val.(*preloadStatusSnapshot); isSnapshot && snapshot != nil {
			if fullyComplete {
				resp := base
				resp.CompletedBytes = completedLength
				resp.Progress = 1
				resp.Status = api.Ready
				resp.TargetBytes = completedLength
				return resp
			}
			resp := base
			resp.CompletedBytes = snapshot.completedBytes
			resp.Progress = snapshot.progress
			resp.Status = api.Superseded
			resp.TargetBytes = snapshot.targetBytes
			return resp
		}
	}

	if fullyComplete {
		resp := base
		resp.CompletedBytes = completedLength
		resp.Progress = 1.0
		resp.Status = api.Ready
		resp.TargetBytes = completedLength
		return resp
	}

	return base
}

func (c *Controller) startPreload(
	to *torrent.Torrent,
	file *torrent.File,
	fileIndex int,
	playbackOffset *int64,
) *preloadTask {
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
	c.clearPreloadSnapshotLocked(ih)
	preloadBudget := calculatePreloadBudget(file.Length(), maxMem)
	startend := max(int64(stream.DefaultFileBoundaryBytes), to.Info().PieceLength)
	ranges := planPreloadRanges(
		file.Length(),
		preloadBudget,
		startend,
		playbackOffset,
	)
	if current, ok := c.preloads.Load(ih); ok {
		if p, ok := current.(*preloadTask); ok && p != nil && p.fileIndex == fileIndex && preloadRangesEqual(p.ranges, ranges) {
			c.preloadsMu.Unlock()
			return p
		}
	}
	if len(ranges) == 0 {
		if old, ok := c.preloads.LoadAndDelete(ih); ok {
			if p, ok := old.(*preloadTask); ok {
				c.releasePreloadLocked(p, true)
			}
		}
		c.preloadsMu.Unlock()
		return nil
	}
	targetBytes := preloadRangesSize(ranges)

	if old, ok := c.preloads.LoadAndDelete(ih); ok {
		if p, ok := old.(*preloadTask); ok {
			c.releasePreloadLocked(p, true)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	clearProtection := func() {
		if storageClient != nil {
			readerID := preloadReaderIDBase
			for range ranges {
				storageClient.ClearActiveRange(ih, readerID)
				readerID--
			}
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
		ranges:          ranges,
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
			readerID := preloadReaderIDBase
			for _, byteRange := range ranges {
				startPiece, endPiece, ok := preloadPieceRange(file, byteRange)
				if ok {
					storageClient.SetActiveRange(ih, readerID, startPiece, endPiece)
				}
				readerID--
			}
		}
	}
	preload.start = func() {
		c.runPreload(preload, to, file, pool, mode, ranges)
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
	// Preload range readers claim their complete range at PiecePriorityNow, so
	// keep queued work registered but do not let it compete with live playback.
	// The final playback release resumes this queue.
	if c.preloadPlaybackCount > 0 {
		return
	}
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

func (c *Controller) runPreload(
	preload *preloadTask,
	to *torrent.Torrent,
	file *torrent.File,
	pool *stream.Pool,
	mode stream.StorageMode,
	ranges []preloadByteRange,
) {
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

	results := make(chan error, len(ranges))
	for _, byteRange := range ranges {
		go func(target preloadByteRange) {
			results <- preloadRange(preload.ctx, pool, file, mode, target.start, target.end, &preload.bytesRead)
		}(byteRange)
	}

	var preloadErr error
	for range ranges {
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

func planPreloadRanges(
	fileSize int64,
	preloadBudget int64,
	boundaryBytes int64,
	playbackOffset *int64,
) []preloadByteRange {
	preloadBudget = min(max(preloadBudget, 0), max(fileSize, 0))
	if preloadBudget == 0 {
		return nil
	}
	if preloadBudget == fileSize {
		return []preloadByteRange{{end: fileSize}}
	}

	if playbackOffset == nil {
		boundaryBytes = min(max(boundaryBytes, 0), fileSize)
		if preloadBudget <= boundaryBytes {
			return []preloadByteRange{{end: preloadBudget}}
		}

		tailStart := fileSize - boundaryBytes
		headEnd := min(preloadBudget-boundaryBytes, tailStart)
		ranges := make([]preloadByteRange, 0, 2)
		if headEnd > 0 {
			ranges = append(ranges, preloadByteRange{end: headEnd})
		}
		if tailStart < fileSize {
			ranges = append(ranges, preloadByteRange{end: fileSize, start: tailStart})
		}
		return ranges
	}

	metadataBytes := min(max(boundaryBytes, 0), preloadBudget/4)
	headEnd := metadataBytes
	tailStart := fileSize - metadataBytes
	positionBudget := min(preloadBudget-2*metadataBytes, tailStart-headEnd)
	positionByte := *playbackOffset
	positionByte = min(max(positionByte, headEnd), tailStart)
	positionStart := positionByte - positionBudget/8
	positionStart = min(max(positionStart, headEnd), tailStart-positionBudget)

	ranges := make([]preloadByteRange, 0, 3)
	if headEnd > 0 {
		ranges = append(ranges, preloadByteRange{end: headEnd})
	}
	if positionBudget > 0 {
		ranges = append(ranges, preloadByteRange{
			end:   positionStart + positionBudget,
			start: positionStart,
		})
	}
	if tailStart < fileSize {
		ranges = append(ranges, preloadByteRange{end: fileSize, start: tailStart})
	}
	return normalizePreloadRanges(ranges)
}

func normalizePreloadRanges(ranges []preloadByteRange) []preloadByteRange {
	filtered := ranges[:0]
	for _, byteRange := range ranges {
		if byteRange.end > byteRange.start {
			filtered = append(filtered, byteRange)
		}
	}
	sort.Slice(filtered, func(left, right int) bool {
		return filtered[left].start < filtered[right].start
	})
	merged := make([]preloadByteRange, 0, len(filtered))
	for _, byteRange := range filtered {
		if len(merged) == 0 || byteRange.start > merged[len(merged)-1].end {
			merged = append(merged, byteRange)
			continue
		}
		merged[len(merged)-1].end = max(merged[len(merged)-1].end, byteRange.end)
	}
	return merged
}

func preloadRangesEqual(left, right []preloadByteRange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func preloadRangesSize(ranges []preloadByteRange) int64 {
	var size int64
	for _, byteRange := range ranges {
		size += max(byteRange.end-byteRange.start, 0)
	}
	return size
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

func preloadPieceRange(file *torrent.File, byteRange preloadByteRange) (int, int, bool) {
	if file == nil || file.Torrent() == nil || file.Torrent().Info() == nil || byteRange.end <= byteRange.start {
		return 0, 0, false
	}
	pieceLength := max(file.Torrent().Info().PieceLength, 1)
	fileOffset := file.Offset()
	return int((fileOffset + byteRange.start) / pieceLength),
		int((fileOffset + byteRange.end - 1) / pieceLength), true
}

func (c *Controller) cancelPreload(ih metainfo.Hash) {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	c.clearPreloadSnapshotLocked(ih)
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

func (c *Controller) snapshotPreloadLocked(preload *preloadTask) {
	if preload == nil {
		return
	}
	c.clearPreloadSnapshotLocked(preload.infoHash)
	completedBytes := preload.progressBytes()
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

// preparePreloadsForPlaybackLocked retires the preload for the file that is
// about to play and stops unrelated workers that are still downloading. Each
// retired task leaves an immutable, short-lived status snapshot, while all of
// its bandwidth, cache protection, and memory reservations are released. Ready
// file-storage preloads unrelated to this playback do not reserve memory and
// may remain cached; queued requests remain registered until playback ends. The
// caller must hold preloadsMu and increment preloadPlaybackCount before calling
// this method.
func (c *Controller) preparePreloadsForPlaybackLocked(infoHash metainfo.Hash, filePath string) {
	var matchedPreload *preloadTask
	if current, ok := c.preloads.Load(infoHash); ok {
		preload, isPreload := current.(*preloadTask)
		if isPreload && preload != nil && preload.filePath == filePath &&
			c.preloads.CompareAndDelete(infoHash, preload) {
			matchedPreload = preload
			c.releasePreloadLocked(preload, false)
			c.snapshotPreloadLocked(preload)
		}
	}

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
		c.preloads.Delete(key)
		c.releasePreloadLocked(preload, false)
		c.snapshotPreloadLocked(preload)
		return true
	})
}

func (c *Controller) cancelAllPreloads() {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	c.cancelAllPreloadsLocked()
}

// cancelAllPreloadsLocked releases every speculative cache lease and priority
// claim. The caller must hold preloadsMu.
func (c *Controller) cancelAllPreloadsLocked() {
	c.preloadQueue = nil
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
