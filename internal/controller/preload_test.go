// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestTorrentPreload_Endpoints(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)

	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	ih := to.InfoHash().HexString()

	t.Run("GET when idle returns status idle", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var pResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
		assert.Equal(t, api.Idle, pResp.Status)
		assert.Equal(t, preloadNoFileIndex, pResp.FileIndex)
		assert.Zero(t, pResp.TargetBytes)
		assert.Zero(t, pResp.Progress)
	})

	t.Run("GET with unknown hash returns 404", func(t *testing.T) {
		unknown := metainfo.Hash{1, 2, 3}.HexString()
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, unknown))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("PUT starts preload by file index", func(t *testing.T) {
		reqBody := `{"file_index":0}`
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewBufferString(reqBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var pResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
		assert.Equal(t, api.Preloading, pResp.Status)
		assert.Equal(t, 0, pResp.FileIndex)
		assert.Positive(t, pResp.TargetBytes)

		// Verify GET returns preloading state
		getResp, err := http.Get(fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih))
		require.NoError(t, err)
		defer getResp.Body.Close()

		assert.Equal(t, http.StatusOK, getResp.StatusCode)
		var getPResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(getResp.Body).Decode(&getPResp))
		assert.Equal(t, api.Preloading, getPResp.Status)
		assert.Equal(t, pResp.TargetBytes, getPResp.TargetBytes)
	})

	t.Run("PUT same file index is idempotent", func(t *testing.T) {
		reqBody := `{"file_index":0}`
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewBufferString(reqBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var pResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
		assert.Equal(t, api.Preloading, pResp.Status)
	})

	t.Run("PUT index takes precedence over path", func(t *testing.T) {
		reqBody := `{"file_index":0,"file_path":"non_existent_movie.mkv"}`
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewBufferString(reqBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var pResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
		assert.Equal(t, 0, pResp.FileIndex)
		require.NotNil(t, pResp.FilePath)
		assert.Equal(t, to.Files()[0].Path(), *pResp.FilePath)
	})

	t.Run("PUT by path resolves correct file", func(t *testing.T) {
		file := to.Files()[0]
		reqBody, err := json.Marshal(api.PreloadRequest{FilePath: utils.Ptr(file.Path())})
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewReader(reqBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var pResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
		assert.Equal(t, api.Preloading, pResp.Status)
		assert.Equal(t, 0, pResp.FileIndex)
		require.NotNil(t, pResp.FilePath)
		assert.Equal(t, file.Path(), *pResp.FilePath)
	})

	t.Run("PUT with invalid index returns 400", func(t *testing.T) {
		reqBody := `{"file_index":9999}`
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewBufferString(reqBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("PUT with invalid path returns 400", func(t *testing.T) {
		reqBody := `{"file_path":"non_existent_movie.mkv"}`
		req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewBufferString(reqBody))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("DELETE cancels preload", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), http.NoBody)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		// After cancellation, status returns idle
		getResp, err := http.Get(fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih))
		require.NoError(t, err)
		defer getResp.Body.Close()

		assert.Equal(t, http.StatusOK, getResp.StatusCode)
		var pResp api.PreloadResponse
		require.NoError(t, json.NewDecoder(getResp.Body).Decode(&pResp))
		assert.Equal(t, api.Idle, pResp.Status)
		assert.Equal(t, preloadNoFileIndex, pResp.FileIndex)
	})

	t.Run("DELETE when already idle is 204", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), http.NoBody)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("DELETE on unknown hash returns 404", func(t *testing.T) {
		unknown := metainfo.Hash{9, 9, 9}.HexString()
		req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, unknown), http.NoBody)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestTorrentPreload_DbTorrentActivation(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)

	ih := metaInfo.HashInfoBytes()

	info, err := metaInfo.UnmarshalInfo()
	require.NoError(t, err)

	// Insert into DB as inactive
	err = ctrl.db.CreateTorrent(&database.Torrent{
		Torrent: api.Torrent{
			Hash:      ih,
			Name:      info.Name,
			Storage:   utils.Ptr(api.Memory),
			TotalSize: info.TotalLength(),
		},
		InfoBytes: metaInfo.InfoBytes,
	})
	require.NoError(t, err)

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	// PUT preload should automatically activate the torrent and start preloading
	reqBody := `{"file_index":0}`
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih.HexString()), bytes.NewBufferString(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var pResp api.PreloadResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
	assert.Equal(t, api.Preloading, pResp.Status)

	ctrl.cancelPreload(ih)
}

func TestTorrentPreload_ReadyState(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)

	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	ih := to.InfoHash()

	task := &preloadTask{
		targetBytes: 1048576,
		fileIndex:   0,
		filePath:    to.Files()[0].Path(),
	}
	task.bytesRead.Store(128)
	ctrl.preloads.Store(ih, task)

	storageTorrent, err := ctrl.storageClient.OpenTorrent(context.Background(), to.Info(), ih)
	require.NoError(t, err)
	_, err = storageTorrent.Piece(to.Piece(0).Info()).WriteAt(make([]byte, 1024), 0)
	require.NoError(t, err)
	preloadingResp := ctrl.getPreloadStatus(ih)
	assert.Equal(t, api.Preloading, preloadingResp.Status)
	assert.Equal(t, int64(128), preloadingResp.CompletedBytes, "unrelated torrent writes must not advance this preload")

	task.ready.Store(true)
	task.bytesRead.Store(1048576)

	// GET should return ready status with 100% progress
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih.HexString()))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var pResp api.PreloadResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pResp))
	assert.Equal(t, api.Ready, pResp.Status)
	assert.Equal(t, float32(1.0), pResp.Progress)
	assert.Equal(t, int64(1048576), pResp.TargetBytes)
	assert.Equal(t, int64(1048576), pResp.CompletedBytes)

	// Playback must retire even an unfinished same-file preload before it
	// acquires a competing stream reader.
	task.ready.Store(false)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/stream/0", http.NoBody)
	ctrl.streamFile(recorder, request, ih, 0)
	_, preloading := ctrl.preloads.Load(ih)
	assert.False(t, preloading)
}

func TestPreloadTaskProgressBytesNeverDecreases(t *testing.T) {
	task := &preloadTask{targetBytes: 1000}

	task.bytesRead.Store(600)
	assert.Equal(t, int64(600), task.progressBytes())
	task.bytesRead.Store(800)
	assert.Equal(t, int64(800), task.progressBytes())
	task.bytesRead.Store(1200)
	assert.Equal(t, int64(1000), task.progressBytes())
}

func TestCalculatePreloadBudget(t *testing.T) {
	const mib = int64(1 << 20)
	tests := []struct {
		name              string
		fileSize          int64
		maxMemory         int64
		expectedPreloaded int64
	}{
		{name: "small file", fileSize: 20 * mib, maxMemory: 128 * mib, expectedPreloaded: 20 * mib},
		{name: "memory below cap", fileSize: 1 << 30, maxMemory: 64 * mib, expectedPreloaded: 16 * mib},
		{name: "memory at cap", fileSize: 1 << 30, maxMemory: 128 * mib, expectedPreloaded: 32 * mib},
		{name: "memory above cap", fileSize: 1 << 30, maxMemory: 512 * mib, expectedPreloaded: 32 * mib},
		{name: "one gibibyte memory", fileSize: 1 << 30, maxMemory: 1 << 30, expectedPreloaded: 32 * mib},
		// The latency ceiling, not the concurrency count, binds once memory is
		// plentiful: raising maxConcurrentPreloads must not shrink these.
		{name: "ample memory is capped by latency ceiling", fileSize: 1 << 30, maxMemory: 8 << 30, expectedPreloaded: 32 * mib},
		{name: "zero memory", fileSize: 1 << 30, maxMemory: 0, expectedPreloaded: 0},
		{name: "negative memory", fileSize: 1 << 30, maxMemory: -1, expectedPreloaded: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedPreloaded, calculatePreloadBudget(tt.fileSize, tt.maxMemory))
		})
	}
}

func TestPreloadSchedulerLimitsConcurrencyAndReleasesCapacity(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: time.Hour}
	type scheduledTask struct {
		preload          *preloadTask
		started          chan struct{}
		complete         chan struct{}
		budgetReleases   atomic.Int32
		protectionClears atomic.Int32
	}
	newTask := func(id byte) *scheduledTask {
		ctx, cancel := context.WithCancel(context.Background())
		task := &scheduledTask{
			started:  make(chan struct{}),
			complete: make(chan struct{}),
		}
		preload := &preloadTask{
			ctx:         ctx,
			infoHash:    metainfo.Hash{id},
			cancel:      cancel,
			done:        make(chan struct{}),
			targetBytes: 32 << 20,
			queued:      true,
			reserveBudget: func() bool {
				return true
			},
		}
		preload.releaseBudget = func() { task.budgetReleases.Add(1) }
		preload.setProtection = func() {}
		preload.clearProtection = func() { task.protectionClears.Add(1) }
		preload.start = func() {
			close(task.started)
			completed := false
			select {
			case <-task.complete:
				preload.ready.Store(true)
				completed = true
			case <-ctx.Done():
			}
			preload.doneOnce.Do(func() { close(preload.done) })
			ctrl.finishPreload(preload, completed)
		}
		task.preload = preload
		return task
	}

	first := newTask(1)
	second := newTask(2)
	third := newTask(3)
	ctrl.preloadsMu.Lock()
	for _, task := range []*scheduledTask{first, second, third} {
		ctrl.preloads.Store(task.preload.infoHash, task.preload)
		ctrl.preloadQueue = append(ctrl.preloadQueue, task.preload)
	}
	ctrl.dispatchPreloadsLocked()
	ctrl.preloadsMu.Unlock()

	require.Eventually(t, func() bool {
		select {
		case <-first.started:
		default:
			return false
		}
		select {
		case <-second.started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	select {
	case <-third.started:
		t.Fatal("third preload started before an active slot was released")
	default:
	}
	ctrl.preloadsMu.Lock()
	assert.Equal(t, maxConcurrentPreloads, ctrl.preloadActiveTasks)
	ctrl.preloadsMu.Unlock()

	close(first.complete)
	require.Eventually(t, func() bool {
		select {
		case <-third.started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	assert.Zero(t, first.budgetReleases.Load(), "ready preload must keep the reservation covering its pinned cache")
	assert.Zero(t, first.protectionClears.Load(), "ready preload must retain cache protection")
	current, ok := ctrl.preloads.Load(first.preload.infoHash)
	require.True(t, ok)
	assert.Same(t, first.preload, current)

	ctrl.cancelPreload(first.preload.infoHash)
	assert.Equal(t, int32(1), first.protectionClears.Load())
	assert.Equal(t, int32(1), first.budgetReleases.Load(), "clearing protection must return the reservation")
	ctrl.cancelAllPreloads()
	ctrl.preloadsMu.Lock()
	assert.Zero(t, ctrl.preloadActiveTasks)
	ctrl.preloadsMu.Unlock()
}

func TestPreloadSchedulerSkipsCancelledQueuedTask(t *testing.T) {
	ctrl := &Controller{}
	ctx, cancel := context.WithCancel(context.Background())
	queued := &preloadTask{
		ctx:         ctx,
		infoHash:    metainfo.Hash{1},
		cancel:      cancel,
		done:        make(chan struct{}),
		targetBytes: 1,
		queued:      true,
		start:       func() { t.Error("cancelled queued preload started") },
	}
	ctrl.preloads.Store(queued.infoHash, queued)
	ctrl.preloadQueue = append(ctrl.preloadQueue, queued)

	ctrl.cancelPreload(queued.infoHash)
	ctrl.preloadsMu.Lock()
	ctrl.dispatchPreloadsLocked()
	assert.Zero(t, ctrl.preloadActiveTasks)
	assert.Empty(t, ctrl.preloadQueue)
	ctrl.preloadsMu.Unlock()
}

func TestPreloadSchedulerEvictsReadyPreloadToReserveBudget(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: time.Hour}
	readyCtx, readyCancel := context.WithCancel(context.Background())
	defer readyCancel()
	var protectionClears, budgetReleases atomic.Int32
	ready := &preloadTask{
		ctx:             readyCtx,
		infoHash:        metainfo.Hash{1},
		cancel:          readyCancel,
		done:            make(chan struct{}),
		targetBytes:     32 << 20,
		protected:       true,
		releaseBudget:   func() { budgetReleases.Add(1) },
		clearProtection: func() { protectionClears.Add(1) },
	}
	ready.ready.Store(true)
	ready.doneOnce.Do(func() { close(ready.done) })
	ctrl.preloads.Store(ready.infoHash, ready)

	pendingCtx, pendingCancel := context.WithCancel(context.Background())
	defer pendingCancel()
	started := make(chan struct{})
	pending := &preloadTask{
		ctx:         pendingCtx,
		infoHash:    metainfo.Hash{2},
		cancel:      pendingCancel,
		done:        make(chan struct{}),
		targetBytes: 32 << 20,
		queued:      true,
	}
	// Budget is only available once the ready preload unpins its cache.
	pending.reserveBudget = func() bool { return budgetReleases.Load() > 0 }
	pending.start = func() {
		close(started)
		pending.doneOnce.Do(func() { close(pending.done) })
		ctrl.finishPreload(pending, false)
	}
	ctrl.preloads.Store(pending.infoHash, pending)

	ctrl.preloadsMu.Lock()
	ctrl.preloadQueue = append(ctrl.preloadQueue, pending)
	ctrl.dispatchPreloadsLocked()
	ctrl.preloadsMu.Unlock()

	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond, "pending preload must run once a ready one is evicted")
	assert.Equal(t, int32(1), budgetReleases.Load())
	assert.Equal(t, int32(1), protectionClears.Load())
	_, ok := ctrl.preloads.Load(ready.infoHash)
	assert.False(t, ok, "evicted preload must leave the registry")

	ctrl.cancelAllPreloads()
}

func TestPreloadSchedulerDropsUnreservableTask(t *testing.T) {
	ctrl := &Controller{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starved := &preloadTask{
		ctx:           ctx,
		infoHash:      metainfo.Hash{1},
		cancel:        cancel,
		done:          make(chan struct{}),
		targetBytes:   32 << 20,
		queued:        true,
		reserveBudget: func() bool { return false },
		start:         func() { t.Error("preload started without a budget reservation") },
	}
	followerCtx, followerCancel := context.WithCancel(context.Background())
	defer followerCancel()
	started := make(chan struct{})
	follower := &preloadTask{
		ctx:           followerCtx,
		infoHash:      metainfo.Hash{2},
		cancel:        followerCancel,
		done:          make(chan struct{}),
		targetBytes:   1,
		queued:        true,
		reserveBudget: func() bool { return true },
	}
	follower.start = func() {
		close(started)
		follower.doneOnce.Do(func() { close(follower.done) })
		ctrl.finishPreload(follower, false)
	}

	ctrl.preloadsMu.Lock()
	for _, preload := range []*preloadTask{starved, follower} {
		ctrl.preloads.Store(preload.infoHash, preload)
		ctrl.preloadQueue = append(ctrl.preloadQueue, preload)
	}
	ctrl.dispatchPreloadsLocked()
	assert.Empty(t, ctrl.preloadQueue, "an unreservable task must not block the queue")
	assert.Equal(t, 1, ctrl.preloadActiveTasks)
	ctrl.preloadsMu.Unlock()

	select {
	case <-starved.done:
	default:
		t.Fatal("dropped preload must not leave waiters blocked forever")
	}
	_, ok := ctrl.preloads.Load(starved.infoHash)
	assert.False(t, ok, "dropped preload must be removed from the registry")
	assert.Error(t, starved.ctx.Err())
	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond, "task queued behind a dropped one must still run")

	ctrl.cancelAllPreloads()
}

func TestFinishPreload(t *testing.T) {
	t.Run("failure releases task without deadlock", func(t *testing.T) {
		ctrl := &Controller{preloadReadyTTL: time.Hour}
		ih := metainfo.Hash{1}
		ctx, cancel := context.WithCancel(context.Background())
		cleared := make(chan struct{})
		task := &preloadTask{
			ctx:             ctx,
			infoHash:        ih,
			cancel:          cancel,
			clearProtection: func() { close(cleared) },
			done:            make(chan struct{}),
			protected:       true,
		}
		ctrl.preloads.Store(ih, task)

		returned := make(chan struct{})
		go func() {
			ctrl.finishPreload(task, false)
			close(returned)
		}()

		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("failed preload cleanup deadlocked")
		}
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
		_, exists := ctrl.preloads.Load(ih)
		assert.False(t, exists)
		select {
		case <-cleared:
		default:
			t.Fatal("failed preload did not release cache protection")
		}
	})

	t.Run("success remains ready until expiry", func(t *testing.T) {
		ctrl := &Controller{preloadReadyTTL: time.Hour}
		ih := metainfo.Hash{2}
		ctx, cancel := context.WithCancel(context.Background())
		task := &preloadTask{
			ctx:      ctx,
			infoHash: ih,
			cancel:   cancel,
			done:     make(chan struct{}),
		}
		ctrl.preloads.Store(ih, task)

		ctrl.finishPreload(task, true)

		assert.ErrorIs(t, ctx.Err(), context.Canceled)
		current, exists := ctrl.preloads.Load(ih)
		require.True(t, exists)
		assert.Same(t, task, current)
		require.NotNil(t, task.expiryTimer)
		ctrl.cancelPreload(ih)
	})
}

func TestReadPreloadRangeRequiresEntireRange(t *testing.T) {
	var bytesRead atomic.Int64
	err := readPreloadRange(context.Background(), bytes.NewReader([]byte("data")), 0, 8, &bytesRead)

	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, int64(4), bytesRead.Load())
}

func TestStartPreloadConcurrentSameFileIsIdempotent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)

	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()
	file := to.Files()[0]

	const callers = 8
	start := make(chan struct{})
	tasks := make(chan *preloadTask, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			tasks <- ctrl.startPreload(to, file, 0)
		})
	}
	close(start)
	wg.Wait()
	close(tasks)

	var first *preloadTask
	for task := range tasks {
		require.NotNil(t, task)
		if first == nil {
			first = task
		}
		assert.Same(t, first, task)
	}
	ctrl.cancelPreload(to.InfoHash())
}

func TestDeleteTorrentClearsCompletedPreload(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)

	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()
	ih := to.InfoHash()

	cleared := false
	task := &preloadTask{
		infoHash:        ih,
		cancel:          func() {},
		clearProtection: func() { cleared = true },
		fileIndex:       0,
		filePath:        to.Files()[0].Path(),
		protected:       true,
	}
	task.ready.Store(true)
	ctrl.preloads.Store(ih, task)

	ctrl.mu.Lock()
	err = ctrl.deleteTorrentLocked(ih)
	ctrl.mu.Unlock()
	require.NoError(t, err)
	assert.True(t, cleared)
	_, exists := ctrl.preloads.Load(ih)
	assert.False(t, exists)
}

func TestReadyPreloadExpires(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: 10 * time.Millisecond}
	ih := metainfo.Hash{1}
	cleared := make(chan struct{})
	task := &preloadTask{
		infoHash:        ih,
		cancel:          func() {},
		clearProtection: func() { close(cleared) },
		protected:       true,
	}
	task.ready.Store(true)
	ctrl.preloads.Store(ih, task)

	ctrl.preloadsMu.Lock()
	ctrl.schedulePreloadExpiryLocked(task)
	ctrl.preloadsMu.Unlock()

	require.Eventually(t, func() bool {
		_, exists := ctrl.preloads.Load(ih)
		return !exists
	}, time.Second, time.Millisecond)
	select {
	case <-cleared:
	default:
		t.Fatal("preload expiry did not release cache protection")
	}
}

func TestPreloadExpiryDoesNotRemoveReplacement(t *testing.T) {
	ctrl := &Controller{}
	ih := metainfo.Hash{1}
	var oldCleared atomic.Bool
	oldTask := &preloadTask{
		infoHash:        ih,
		cancel:          func() {},
		clearProtection: func() { oldCleared.Store(true) },
		protected:       true,
	}
	newTask := &preloadTask{}
	ctrl.preloads.Store(ih, newTask)

	assert.False(t, ctrl.removePreload(ih, oldTask))
	assert.False(t, oldCleared.Load())
	current, exists := ctrl.preloads.Load(ih)
	require.True(t, exists)
	assert.Same(t, newTask, current)
}
