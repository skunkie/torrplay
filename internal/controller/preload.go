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

	// Preload protection is registered directly with storage rather than by a
	// Pool reader. Keep these sentinels far from Pool.nextID's zero-based reader
	// IDs; changes to either allocation scheme must preserve that separation.
	preloadHeadReaderID uint64 = 1<<63 - 1
	preloadTailReaderID uint64 = preloadHeadReaderID - 1
)

type preloadTask struct {
	cancel          context.CancelFunc
	clearProtection func()
	expiryTimer     *time.Timer
	targetBytes     int64
	fileIndex       int
	filePath        string
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
	case <-time.After(gotInfoTimeout):
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
	preloadBudget := min(file.Length(), maxMem*50/100)
	if mode == stream.MemoryStorage {
		preloadBudget = pool.ReservePreloadBudget(ih, preloadBudget)
	}
	if preloadBudget <= 0 {
		if old, ok := c.preloads.LoadAndDelete(ih); ok {
			if p, ok := old.(*preloadTask); ok {
				releasePreload(p)
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

	if old, ok := c.preloads.Load(ih); ok {
		if p, ok := old.(*preloadTask); ok {
			if p.expiryTimer != nil {
				p.expiryTimer.Stop()
				p.expiryTimer = nil
			}
			p.cancel()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	clearProtection := func() {
		if storageClient != nil {
			storageClient.ClearActiveRange(ih, preloadHeadReaderID)
			storageClient.ClearActiveRange(ih, preloadTailReaderID)
		}
		if mode == stream.MemoryStorage {
			pool.ReleasePreloadBudget(ih)
		}
	}
	preload := &preloadTask{
		cancel:          cancel,
		clearProtection: clearProtection,
		targetBytes:     targetBytes,
		fileIndex:       fileIndex,
		filePath:        file.Path(),
	}
	c.preloads.Store(ih, preload)
	if storageClient != nil {
		if hs, he, ts, te, ok := preloadPieceBoundaries(file, readerStartEnd, readerEndStart, readerEndEnd); ok {
			storageClient.SetActiveRange(ih, preloadHeadReaderID, hs, he)
			storageClient.SetActiveRange(ih, preloadTailReaderID, ts, te)
		}
	}
	c.preloadsMu.Unlock()
	removePreload := func() bool {
		return c.removePreload(ih, preload)
	}

	go func() {
		defer func() {
			if ctx.Err() != nil {
				removePreload()
			}
			cancel()
		}()

		if c.downloader != nil {
			c.downloader.AddStreaming(ih)
			defer c.downloader.RemoveStreaming(ih)
		}

		go func() {
			select {
			case <-to.Closed():
				cancel()
			case <-ctx.Done():
			}
		}()

		results := make(chan error, 2)
		workers := 1
		go func() {
			results <- preloadRange(ctx, pool, file, mode, 0, readerStartEnd, &preload.bytesRead)
		}()
		if readerEndEnd > readerEndStart {
			workers++
			go func() {
				results <- preloadRange(ctx, pool, file, mode, readerEndStart, readerEndEnd, &preload.bytesRead)
			}()
		}

		var preloadErr error
		for range workers {
			if err := <-results; err != nil && !errors.Is(err, context.Canceled) && preloadErr == nil {
				preloadErr = err
				cancel()
			}
		}
		if preloadErr != nil {
			removePreload()
			c.logger.Warn("torrent preload failed", "hash", ih, "error", preloadErr)
			return
		}
		if ctx.Err() != nil {
			return
		}

		preload.bytesRead.Store(preload.targetBytes)
		preload.ready.Store(true)
		c.schedulePreloadExpiry(ih, preload)
	}()
	return preload
}

func preloadRange(ctx context.Context, pool *stream.Pool, file *torrent.File, mode stream.StorageMode, start, end int64, bytesRead *atomic.Int64) error {
	if end <= start {
		return nil
	}
	reader, release, err := pool.AcquirePreloadContext(ctx, file, mode)
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
			releasePreload(p)
		}
	}
}

func (c *Controller) removePreload(ih metainfo.Hash, preload *preloadTask) bool {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	if !c.preloads.CompareAndDelete(ih, preload) {
		return false
	}
	releasePreload(preload)
	return true
}

func (c *Controller) schedulePreloadExpiry(ih metainfo.Hash, preload *preloadTask) {
	if c.preloadReadyTTL <= 0 {
		return
	}
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	current, ok := c.preloads.Load(ih)
	if !ok || current != preload {
		return
	}
	preload.expiryTimer = time.AfterFunc(c.preloadReadyTTL, func() {
		c.removePreload(ih, preload)
	})
}

func releasePreload(preload *preloadTask) {
	if preload.expiryTimer != nil {
		preload.expiryTimer.Stop()
		preload.expiryTimer = nil
	}
	if preload.cancel != nil {
		preload.cancel()
	}
	if preload.clearProtection != nil {
		preload.clearProtection()
	}
}

func (c *Controller) cancelAllPreloads() {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	c.preloads.Range(func(key, val any) bool {
		c.preloads.Delete(key)
		if p, ok := val.(*preloadTask); ok {
			releasePreload(p)
		}
		return true
	})
}
