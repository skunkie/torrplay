// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
	"github.com/torrplay/torrplay/pkg/stream"
)

// preloadNoFileIndex is the file index reported when no file is preloaded.
const preloadNoFileIndex = -1

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

	c.startPreload(to, files[targetIdx])

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

	// A fully downloaded torrent is ready as a whole, so a preload that ended
	// without becoming ready no longer matters.
	completeTorrent := func() api.PreloadResponse {
		resp := base
		resp.CompletedBytes = completedLength
		resp.Progress = 1
		resp.Status = api.Ready
		resp.TargetBytes = completedLength
		return resp
	}

	status, ok := c.preloadStatus(ih)
	if !ok {
		if fullyComplete {
			return completeTorrent()
		}
		return base
	}
	resp := base
	resp.CompletedBytes = status.CompletedBytes
	resp.TargetBytes = status.TargetBytes
	if status.TargetBytes > 0 {
		resp.Progress = min(1, float32(status.CompletedBytes)/float32(status.TargetBytes))
	}
	switch status.State {
	case stream.PreloadQueued:
		resp.Status = api.Queued
	case stream.PreloadRunning:
		resp.Status = api.Preloading
	case stream.PreloadReady:
		resp.Status = api.Ready
	case stream.PreloadFailed:
		if fullyComplete {
			return completeTorrent()
		}
		resp.Status = api.Failed
		return resp
	case stream.PreloadEvicted:
		if fullyComplete {
			return completeTorrent()
		}
		resp.Status = api.Evicted
		return resp
	default:
		return base
	}
	resp.FileIndex = status.FileIndex
	resp.FilePath = &status.FilePath
	return resp
}

// preloadStatus returns the status of a torrent's preload in the current
// stream pool.
func (c *Controller) preloadStatus(ih metainfo.Hash) (stream.PreloadStatus, bool) {
	pool := c.streamPool.Load()
	if pool == nil {
		return stream.PreloadStatus{}, false
	}
	return pool.PreloadStatus(ih)
}

// preloadActive reports whether a torrent's preload is queued or downloading.
func (c *Controller) preloadActive(ih metainfo.Hash) bool {
	status, ok := c.preloadStatus(ih)
	return ok && (status.State == stream.PreloadQueued || status.State == stream.PreloadRunning)
}

// startPreload preloads file in the current stream pool. It does nothing while
// the torrent client is reconfiguring or when to is not the current client's
// instance of its torrent. It reports whether the torrent now has a preload.
func (c *Controller) startPreload(to *torrent.Torrent, file *torrent.File) bool {
	if to == nil || to.Info() == nil || file == nil || c.torrentClientUnavailable.Load() {
		return false
	}
	ih := to.InfoHash()
	c.mu.RLock()
	pool := c.streamPool.Load()
	var currentTorrent *torrent.Torrent
	if c.client != nil {
		currentTorrent, _ = c.client.Torrent(ih)
	}
	c.mu.RUnlock()
	if pool == nil || currentTorrent != to {
		return false
	}

	if _, err := pool.Preload(file, c.preloadStorageMode(ih)); err != nil {
		if !errors.Is(err, stream.ErrPreloadDoesNotFit) {
			c.logger.Load().Warn("failed to start preload", "hash", ih, "file", file.Path(), "error", err)
		}
		return false
	}
	return true
}

// preloadStorageMode returns the storage mode a torrent's preload uses: file
// storage only for torrents saved with it, memory storage otherwise.
func (c *Controller) preloadStorageMode(ih metainfo.Hash) stream.StorageMode {
	if t, err := c.db.GetTorrent(ih); err == nil && utils.Val(t.Storage) == api.File {
		return stream.FileStorage
	}
	c.torrentTracker.mu.RLock()
	defer c.torrentTracker.mu.RUnlock()
	if info, ok := c.torrentTracker.torrents[ih]; ok && info.storageType == api.File {
		return stream.FileStorage
	}
	return stream.MemoryStorage
}

// cancelPreload stops a torrent's preload in the current stream pool.
func (c *Controller) cancelPreload(ih metainfo.Hash) {
	if pool := c.streamPool.Load(); pool != nil {
		pool.CancelPreload(ih)
	}
}
