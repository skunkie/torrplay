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
	"strings"
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

func TestTorrentPreloadEndpoints(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	file, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(file)
	require.NoError(t, file.Close())
	require.NoError(t, err)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()
	ih := to.InfoHash()
	preloadURL := "/api/v1/torrents/" + ih.HexString() + "/preload"

	doRequest := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		ctrl.router.ServeHTTP(recorder, request)
		return recorder
	}
	decodeStatus := func(recorder *httptest.ResponseRecorder) api.PreloadResponse {
		t.Helper()
		var response api.PreloadResponse
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
		return response
	}

	idle := doRequest(http.MethodGet, preloadURL, "")
	require.Equal(t, http.StatusOK, idle.Code)
	idleStatus := decodeStatus(idle)
	assert.Equal(t, api.Idle, idleStatus.Status)
	assert.Equal(t, preloadNoFileIndex, idleStatus.FileIndex)
	assert.Zero(t, idleStatus.TargetBytes)
	assert.Zero(t, idleStatus.Progress)

	unknownURL := "/api/v1/torrents/" + (metainfo.Hash{1, 2, 3}).HexString() + "/preload"
	assert.Equal(t, http.StatusNotFound, doRequest(http.MethodGet, unknownURL, "").Code)
	assert.Equal(t, http.StatusNotFound, doRequest(http.MethodDelete, unknownURL, "").Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(http.MethodPut, preloadURL, `{"file_index":9999}`).Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(http.MethodPut, preloadURL, `{"file_path":"missing.mkv"}`).Code)

	started := doRequest(http.MethodPut, preloadURL, `{"file_index":0,"file_path":"missing.mkv"}`)
	require.Equal(t, http.StatusOK, started.Code)
	startedStatus := decodeStatus(started)
	assert.Equal(t, api.Preloading, startedStatus.Status)
	assert.Equal(t, 0, startedStatus.FileIndex)
	require.NotNil(t, startedStatus.FilePath)
	assert.Equal(t, to.Files()[0].Path(), *startedStatus.FilePath)
	assert.Positive(t, startedStatus.TargetBytes)

	repeated := doRequest(http.MethodPut, preloadURL, `{"file_index":0}`)
	require.Equal(t, http.StatusOK, repeated.Code)
	assert.Equal(t, startedStatus.TargetBytes, decodeStatus(repeated).TargetBytes)

	require.Equal(t, http.StatusNoContent, doRequest(http.MethodDelete, preloadURL, "").Code)
	afterCancel := decodeStatus(doRequest(http.MethodGet, preloadURL, ""))
	assert.Equal(t, api.Idle, afterCancel.Status)
	assert.Equal(t, preloadNoFileIndex, afterCancel.FileIndex)
	assert.Equal(t, http.StatusNoContent, doRequest(http.MethodDelete, preloadURL, "").Code)

	pathBody, err := json.Marshal(api.PreloadRequest{FilePath: utils.Ptr(to.Files()[0].Path())})
	require.NoError(t, err)
	byPath := doRequest(http.MethodPut, preloadURL, string(pathBody))
	require.Equal(t, http.StatusOK, byPath.Code)
	assert.Equal(t, 0, decodeStatus(byPath).FileIndex)
	ctrl.cancelPreload(ih)
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

func TestPreloadTaskProgressBytesNeverDecreases(t *testing.T) {
	task := &preloadTask{targetBytes: 1000}

	task.bytesRead.Store(600)
	assert.Equal(t, int64(600), task.progressBytes())
	task.bytesRead.Store(800)
	assert.Equal(t, int64(800), task.progressBytes())
	task.bytesRead.Store(1200)
	assert.Equal(t, int64(1000), task.progressBytes())
}

func TestFinishPreload(t *testing.T) {
	t.Run("failure releases task without deadlock", func(t *testing.T) {
		ctrl := &Controller{preloadReadyTTL: time.Hour}
		ih := metainfo.Hash{1}
		ctx, cancel := context.WithCancel(context.Background())
		cleared := make(chan struct{})
		task := &preloadTask{
			cancel:          cancel,
			clearProtection: func() { close(cleared) },
			done:            make(chan struct{}),
		}
		ctrl.preloads.Store(ih, task)

		returned := make(chan struct{})
		go func() {
			ctrl.finishPreload(ih, task, false)
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
			cancel: cancel,
			done:   make(chan struct{}),
		}
		ctrl.preloads.Store(ih, task)

		ctrl.finishPreload(ih, task, true)

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
