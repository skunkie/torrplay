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
		cancel:          func() {},
		clearProtection: func() { cleared = true },
		fileIndex:       0,
		filePath:        to.Files()[0].Path(),
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
		cancel:          func() {},
		clearProtection: func() { close(cleared) },
	}
	task.ready.Store(true)
	ctrl.preloads.Store(ih, task)

	ctrl.schedulePreloadExpiry(ih, task)

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
		cancel:          func() {},
		clearProtection: func() { oldCleared.Store(true) },
	}
	newTask := &preloadTask{}
	ctrl.preloads.Store(ih, newTask)

	assert.False(t, ctrl.removePreload(ih, oldTask))
	assert.False(t, oldCleared.Load())
	current, exists := ctrl.preloads.Load(ih)
	require.True(t, exists)
	assert.Same(t, newTask, current)
}
