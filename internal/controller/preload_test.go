// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
	"github.com/torrplay/torrplay/pkg/stream"
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
	assert.Zero(t, idleStatus.ActivePeers)
	assert.Zero(t, idleStatus.DownloadRate)
	assert.Zero(t, idleStatus.TotalPeers)

	unknownURL := "/api/v1/torrents/" + (metainfo.Hash{1, 2, 3}).HexString() + "/preload"
	assert.Equal(t, http.StatusNotFound, doRequest(http.MethodGet, unknownURL, "").Code)
	assert.Equal(t, http.StatusNotFound, doRequest(http.MethodDelete, unknownURL, "").Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(http.MethodPut, preloadURL, `{"file_index":9999}`).Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(http.MethodPut, preloadURL, `{"file_path":"missing.mkv"}`).Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(http.MethodPut, preloadURL, `{"magnet":"invalid-magnet"}`).Code)
	assert.Equal(t, http.StatusBadRequest, doRequest(http.MethodPut, preloadURL, `{"magnet":"magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}`).Code)

	validMagnetBody := fmt.Sprintf(`{"file_index":0,"magnet":"magnet:?xt=urn:btih:%s&dn=Sintel&tr=http%%3A%%2F%%2Ftracker.example.com%%2Fannounce&tr=http%%3A%%2F%%2Ftracker2.example.com%%2Fannounce"}`, ih.HexString())
	magnetPreload := doRequest(http.MethodPut, preloadURL, validMagnetBody)
	require.Equal(t, http.StatusOK, magnetPreload.Code)
	assert.Equal(t, api.Preloading, decodeStatus(magnetPreload).Status)

	announceList := to.Metainfo().AnnounceList
	distinctTrackers := announceList.DistinctValues()
	assert.Contains(t, distinctTrackers, "http://tracker.example.com/announce")
	assert.Contains(t, distinctTrackers, "http://tracker2.example.com/announce")
	totalTrackerEntries := 0
	for _, tier := range announceList {
		totalTrackerEntries += len(tier)
	}
	assert.Equal(t, len(distinctTrackers), totalTrackerEntries, "magnet trackers must not be duplicated across announce tiers")

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

	// A preload evicted by a smaller budget reports that it was evicted.
	pathBody, err := json.Marshal(api.PreloadRequest{FilePath: utils.Ptr(to.Files()[0].Path())})
	require.NoError(t, err)
	byPath := doRequest(http.MethodPut, preloadURL, string(pathBody))
	require.Equal(t, http.StatusOK, byPath.Code)
	assert.Equal(t, 0, decodeStatus(byPath).FileIndex)
	pool := ctrl.streamPool.Load()
	budget := readaheadBudget(utils.Val(ctrl.settings.Load().MaxMemory))
	pool.SetReadaheadBudget(0)
	evicted := decodeStatus(doRequest(http.MethodGet, preloadURL, ""))
	assert.Equal(t, api.Evicted, evicted.Status)
	assert.Equal(t, preloadNoFileIndex, evicted.FileIndex)
	assert.Nil(t, evicted.FilePath)
	assert.Equal(t, startedStatus.TargetBytes, evicted.TargetBytes)

	// A new preload replaces the reported final state.
	pool.SetReadaheadBudget(budget)
	restarted := decodeStatus(doRequest(http.MethodPut, preloadURL, string(pathBody)))
	assert.Equal(t, api.Preloading, restarted.Status)
	assert.Equal(t, 0, restarted.FileIndex)
	ctrl.cancelPreload(ih)
}

func TestTorrentPreloadActivatesStoredTorrent(t *testing.T) {
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

func addSintelTorrent(t *testing.T, ctrl *Controller) *torrent.Torrent {
	t.Helper()
	metaInfo, err := metainfo.LoadFromFile(sintelTorrentFile)
	require.NoError(t, err)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()
	return to
}

// addSyntheticTorrent adds a single-file torrent with the given size and piece
// length. The torrent has no peers, so its data is never downloaded.
func addSyntheticTorrent(t *testing.T, ctrl *Controller, length, pieceLength int64) *torrent.Torrent {
	t.Helper()
	info := metainfo.Info{
		Name:        "movie.mkv",
		PieceLength: pieceLength,
		Length:      length,
		Pieces:      make([]byte, (length+pieceLength-1)/pieceLength*sha1.Size),
	}
	infoBytes, err := bencode.Marshal(info)
	require.NoError(t, err)
	to, _, err := ctrl.client.AddTorrentSpec(&torrent.TorrentSpec{
		AddTorrentOpts: torrent.AddTorrentOpts{
			InfoHash:  metainfo.HashBytes(infoBytes),
			InfoBytes: infoBytes,
		},
	})
	require.NoError(t, err)
	<-to.GotInfo()
	return to
}

func TestStartPreloadSkipsWhenOnePieceExceedsBudget(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	// A 32 MiB limit leaves 8 MiB of preload capacity, less than one 16 MiB
	// piece.
	setTestMemoryLimit(t, ctrl, 32<<20)

	to := addSyntheticTorrent(t, ctrl, 1<<30, 16<<20)
	assert.False(t, ctrl.startPreload(to, to.Files()[0]))
	_, preloading := ctrl.preloadStatus(to.InfoHash())
	assert.False(t, preloading)
}

func TestStartPreloadSkipsWhileClientReconfigures(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ctrl.torrentClientUnavailable.Store(true)
	defer ctrl.torrentClientUnavailable.Store(false)

	assert.False(t, ctrl.startPreload(to, to.Files()[0]))
	_, preloading := ctrl.preloadStatus(to.InfoHash())
	assert.False(t, preloading)
}

func TestFailedClientReconfigureKeepsTorrentClientUnavailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	busyPort := listener.Addr().(*net.TCPAddr).Port

	var failNext atomic.Bool
	runtimeConfig := testControllerRuntimeConfig()
	configureClient := runtimeConfig.configureClient
	runtimeConfig.configureClient = func(config *torrent.ClientConfig) {
		configureClient(config)
		if failNext.Load() {
			// Listening on a port that is already bound makes NewClient fail.
			config.DisableTCP = false
			config.ListenHost = func(string) string { return "127.0.0.1" }
			config.ListenPort = busyPort
		}
	}
	ctrl, cleanup := newTestControllerWithRuntimeConfig(t, runtimeConfig)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	failNext.Store(true)
	// A torrent client setting change rebuilds the torrent client.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"torrent_client":{"seed":true}}`))
	req.Header.Set("Content-Type", "application/json")
	ctrl.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())

	assert.True(t, ctrl.torrentClientUnavailable.Load())
	assert.False(t, ctrl.startPreload(to, to.Files()[0]))
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/stream/%s?index=0", to.InfoHash()), http.NoBody)
	ctrl.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestStartPreloadRejectsTorrentFromPreviousClientGeneration(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	file := to.Files()[0]
	previousGeneration := ctrl.torrentGeneration.Load()
	setTestMemoryLimit(t, ctrl, 128<<20)

	assert.Greater(t, ctrl.torrentGeneration.Load(), previousGeneration)
	assert.False(t, ctrl.startPreload(to, file))
	_, preloading := ctrl.preloadStatus(to.InfoHash())
	assert.False(t, preloading)
}

func TestStartPreloadWithoutClient(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ctrl.mu.Lock()
	client := ctrl.client
	ctrl.client = nil
	ctrl.mu.Unlock()
	defer func() {
		ctrl.mu.Lock()
		ctrl.client = client
		ctrl.mu.Unlock()
	}()

	assert.False(t, ctrl.startPreload(to, to.Files()[0]))
	_, preloading := ctrl.preloadStatus(to.InfoHash())
	assert.False(t, preloading)
}

func TestStreamPoolRejectsReplacedTorrentInSameGeneration(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	previous := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	generation := ctrl.torrentGeneration.Load()
	previous.Drop()
	<-previous.Closed()
	replacement := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	require.NotSame(t, previous, replacement)

	pool, ok := ctrl.streamPoolForGeneration(previous, generation)
	assert.False(t, ok)
	assert.Nil(t, pool)

	pool, ok = ctrl.streamPoolForGeneration(replacement, generation)
	assert.True(t, ok)
	assert.NotNil(t, pool)
}

func TestStreamPoolRejectsTorrentFromPreviousClientGeneration(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	previousGeneration := ctrl.torrentGeneration.Load()
	setTestMemoryLimit(t, ctrl, 128<<20)

	pool, ok := ctrl.streamPoolForGeneration(to, previousGeneration)
	assert.False(t, ok)
	assert.Nil(t, pool)
}

func TestStreamRejectedWhileTorrentClientUnavailable(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ctrl.torrentClientUnavailable.Store(true)
	defer ctrl.torrentClientUnavailable.Store(false)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/stream/%s?index=0", to.InfoHash()), http.NoBody)
	ctrl.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// TestStreamDoesNotPausePreloads verifies that a preload keeps running while
// another torrent streams, because the engine is shared by every viewer.
func TestStreamDoesNotPausePreloads(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	preloaded := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	played := addSyntheticTorrent(t, ctrl, 1<<30+1, 1<<20)
	require.True(t, ctrl.startPreload(preloaded, preloaded.Files()[0]))
	defer ctrl.cancelPreload(preloaded.InfoHash())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	go func() {
		defer close(served)
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/stream/%s?index=0", played.InfoHash()), http.NoBody).WithContext(ctx)
		ctrl.router.ServeHTTP(httptest.NewRecorder(), req)
	}()

	pool := ctrl.streamPool.Load()
	require.Eventually(t, func() bool { return pool.HasActiveReaders(played.InfoHash()) }, 5*time.Second, time.Millisecond)
	status, ok := ctrl.preloadStatus(preloaded.InfoHash())
	require.True(t, ok)
	assert.Equal(t, stream.PreloadRunning, status.State, "playback must not pause another torrent's preload")

	cancel()
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("stream request did not end after its context was cancelled")
	}
}

// TestGetPreloadStatusReportsQueuedPreload verifies that a preload waiting for
// a preload slot reports that it is queued rather than downloading.
func TestGetPreloadStatusReportsQueuedPreload(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	setTestMemoryLimit(t, ctrl, 512<<20)

	// At most two preloads download at a time.
	hashes := make([]metainfo.Hash, 0, 3)
	for i := range 3 {
		to := addSyntheticTorrent(t, ctrl, 1<<30+int64(i), 1<<20)
		require.True(t, ctrl.startPreload(to, to.Files()[0]))
		hashes = append(hashes, to.InfoHash())
	}
	defer func() {
		for _, ih := range hashes {
			ctrl.cancelPreload(ih)
		}
	}()

	assert.Equal(t, api.Preloading, ctrl.getPreloadStatus(hashes[0]).Status)
	assert.Equal(t, api.Preloading, ctrl.getPreloadStatus(hashes[1]).Status)
	queued := ctrl.getPreloadStatus(hashes[2])
	assert.Equal(t, api.Queued, queued.Status)
	assert.Equal(t, 0, queued.FileIndex)
	assert.Positive(t, queued.TargetBytes)
}

// TestPreloadKeepsTorrentActive verifies that a running preload counts as
// activity, so torrent expiry does not drop a torrent mid-preload.
func TestPreloadKeepsTorrentActive(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ih := to.InfoHash()
	assert.False(t, ctrl.hasTorrentReaders(ih))
	require.True(t, ctrl.startPreload(to, to.Files()[0]))
	assert.True(t, ctrl.hasTorrentReaders(ih))
	ctrl.cancelPreload(ih)
	assert.False(t, ctrl.hasTorrentReaders(ih))
}

func TestTorrentActivityReadsDoNotRaceClientReconfigure(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ih := to.InfoHash()
	done := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			// These run without c.mu from request handlers and must observe
			// either the old or the new component, never a torn read.
			ctrl.hasTorrentReaders(ih)
			ctrl.getPreloadStatus(ih)
			_, _ = ctrl.clientTorrent(ih)
		}
	})

	setTestMemoryLimit(t, ctrl, 128<<20)
	setTestMemoryLimit(t, ctrl, 64<<20)
	close(done)
	readers.Wait()
}

// setTestMemoryLimit rebuilds the torrent client, stream pool, and storage for
// a new memory limit, as a settings update does.
func setTestMemoryLimit(t *testing.T, ctrl *Controller, limit int64) {
	t.Helper()
	ctrl.mu.Lock()
	ctrl.settings.Load().MaxMemory = utils.Ptr(limit)
	ctrl.mu.Unlock()
	require.NoError(t, ctrl.configureTorrentClient())
}

func TestStartPreloadFileStorageIgnoresMemoryLimit(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	// With 32 MiB of memory a 16 MiB piece exceeds the memory preload limit,
	// but a file-storage preload reserves no memory.
	setTestMemoryLimit(t, ctrl, 32<<20)

	to := addSyntheticTorrent(t, ctrl, 1<<30, 16<<20)
	ih := to.InfoHash()
	ctrl.torrentTracker.mu.Lock()
	ctrl.torrentTracker.torrents[ih] = torrentInfo{storageType: api.File, lastUsedAt: time.Now()}
	ctrl.torrentTracker.mu.Unlock()
	defer ctrl.cancelPreload(ih)

	require.True(t, ctrl.startPreload(to, to.Files()[0]))
	status, ok := ctrl.preloadStatus(ih)
	require.True(t, ok)
	assert.Equal(t, int64(32<<20), status.TargetBytes)
}

func TestPreloadRemovedWhenTorrentCloses(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	to := addSintelTorrent(t, ctrl)
	ih := to.InfoHash()

	require.True(t, ctrl.startPreload(to, to.Files()[0]))
	to.Drop()
	<-to.Closed()
	assert.Equal(t, api.Idle, ctrl.getPreloadStatus(ih).Status, "a closed torrent must not report its preload")

	// Re-adding the torrent creates a new instance, which gets a new preload.
	readded := addSintelTorrent(t, ctrl)
	require.NotSame(t, to, readded)
	require.True(t, ctrl.startPreload(readded, readded.Files()[0]))
	assert.Equal(t, api.Preloading, ctrl.getPreloadStatus(ih).Status)
	ctrl.cancelPreload(ih)
}

func TestAddTorrentByHashLoadsTorrentSavedWithoutMagnet(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	ih := metainfo.Hash{5}
	require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{Torrent: api.Torrent{
		Hash:    ih,
		Name:    "saved",
		Storage: utils.Ptr(api.Memory),
	}}))

	to, err := ctrl.addTorrentByHash(ih)
	require.NoError(t, err)
	assert.Equal(t, ih, to.InfoHash())
	ctrl.cancelPreload(ih)
}

func TestController_WaitForInfo(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	ctrl.runtimeConfig.gotInfoTimeout = 10 * time.Millisecond

	t.Run("returns once the metadata is known", func(t *testing.T) {
		to := addSyntheticTorrent(t, ctrl, 1<<20, 1<<20)
		require.NoError(t, ctrl.waitForInfo(to))
		require.NoError(t, ctrl.waitForInfoOrDrop(to))
	})

	// A torrent added by hash alone has no peers here, so its metadata never
	// arrives.
	withoutInfo := func(t *testing.T, ih metainfo.Hash) *torrent.Torrent {
		t.Helper()
		to, err := ctrl.loadTorrent(utils.MagnetURIFromHash(ih), api.Memory)
		require.NoError(t, err)
		return to
	}

	t.Run("times out and keeps the torrent", func(t *testing.T) {
		to := withoutInfo(t, metainfo.Hash{7})
		err := ctrl.waitForInfo(to)
		var apiErr api.Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusGatewayTimeout, apiErr.Code)
		assert.Equal(t, gotInfoTimeoutMsg, apiErr.Message)
		_, loaded := ctrl.clientTorrent(to.InfoHash())
		assert.True(t, loaded)
	})

	t.Run("times out and drops the torrent", func(t *testing.T) {
		to := withoutInfo(t, metainfo.Hash{8})
		require.Error(t, ctrl.waitForInfoOrDrop(to))
		select {
		case <-to.Closed():
		default:
			t.Fatal("the torrent was not dropped")
		}
	})
}

func TestController_TorrentStorageMode(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	saved := func(ih metainfo.Hash, storage api.TorrentStorage) {
		t.Helper()
		require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{Torrent: api.Torrent{
			Hash:    ih,
			Magnet:  utils.MagnetURIFromHash(ih),
			Name:    ih.HexString(),
			Storage: utils.Ptr(storage),
		}}))
	}
	tracked := func(ih metainfo.Hash, storage api.TorrentStorage) {
		ctrl.torrentTracker.mu.Lock()
		defer ctrl.torrentTracker.mu.Unlock()
		ctrl.torrentTracker.torrents[ih] = torrentInfo{lastUsedAt: time.Now(), storageType: storage}
	}

	savedFile, savedMemory, trackedFile, unknown := metainfo.Hash{1}, metainfo.Hash{2}, metainfo.Hash{3}, metainfo.Hash{4}
	saved(savedFile, api.File)
	saved(savedMemory, api.Memory)
	tracked(savedMemory, api.File)
	tracked(trackedFile, api.File)

	tests := []struct {
		name      string
		hash      metainfo.Hash
		wantMode  stream.StorageMode
		wantSaved bool
	}{
		{name: "saved with file storage", hash: savedFile, wantMode: stream.FileStorage, wantSaved: true},
		{name: "saved storage wins over tracked", hash: savedMemory, wantMode: stream.MemoryStorage, wantSaved: true},
		{name: "tracked with file storage", hash: trackedFile, wantMode: stream.FileStorage},
		{name: "unknown", hash: unknown, wantMode: stream.MemoryStorage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, saved := ctrl.torrentStorageMode(tt.hash)
			assert.Equal(t, tt.wantMode, mode)
			assert.Equal(t, tt.wantSaved, saved)
		})
	}
}

func TestDeleteTorrentClearsPreload(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	to := addSintelTorrent(t, ctrl)
	ih := to.InfoHash()
	require.True(t, ctrl.startPreload(to, to.Files()[0]))

	ctrl.mu.Lock()
	err := ctrl.deleteTorrentLocked(ih)
	ctrl.mu.Unlock()
	require.NoError(t, err)
	_, preloading := ctrl.preloadStatus(ih)
	assert.False(t, preloading)
}

// TestDeleteTorrentWithRunningPreloadRemovesFileStorage verifies that deleting
// a torrent cancels its running preload, closes the torrent, and removes its
// file storage.
func TestDeleteTorrentWithRunningPreloadRemovesFileStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("deleting a torrent keeps its file storage on Windows")
	}
	storageDir := t.TempDir()
	ctrl, cleanup := newTestController(t, func(c *Controller) { c.settings.Load().FileStoragePath = &storageDir })
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ih := to.InfoHash()
	require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{Torrent: api.Torrent{
		Hash:    ih,
		Magnet:  utils.MagnetURIFromHash(ih),
		Name:    to.Name(),
		Storage: utils.Ptr(api.File),
	}}))
	torrentDir := filepath.Join(storageDir, to.Name())
	require.NoError(t, os.MkdirAll(torrentDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(torrentDir, "piece"), []byte("data"), 0o600))

	// The torrent has no peers, so the preload keeps running.
	require.True(t, ctrl.startPreload(to, to.Files()[0]))
	status, ok := ctrl.preloadStatus(ih)
	require.True(t, ok)
	require.Equal(t, stream.PreloadRunning, status.State, "preload must be running before the delete")

	deleted := make(chan error, 1)
	go func() {
		ctrl.mu.Lock()
		defer ctrl.mu.Unlock()
		deleted <- ctrl.deleteTorrentLocked(ih)
	}()
	select {
	case err := <-deleted:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("deleting the torrent did not return")
	}

	_, preloading := ctrl.preloadStatus(ih)
	assert.False(t, preloading, "deleting the torrent must cancel its preload")
	select {
	case <-to.Closed():
	default:
		t.Fatal("the torrent was not closed")
	}
	assert.NoDirExists(t, torrentDir)
	_, err := ctrl.db.GetTorrent(ih)
	require.ErrorIs(t, err, database.ErrTorrentNotFound)
	assert.Equal(t, api.Idle, ctrl.getPreloadStatus(ih).Status, "the cancelled preload must not report a status")
}
