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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

	ctrl.preloadSnapshots.Store(ih, &preloadStatusSnapshot{
		completedBytes: 256,
		progress:       0.25,
		status:         api.Superseded,
		targetBytes:    1024,
	})
	interruptedStatus := decodeStatus(doRequest(http.MethodGet, preloadURL, ""))
	assert.Equal(t, api.Superseded, interruptedStatus.Status)
	assert.Equal(t, int64(256), interruptedStatus.CompletedBytes)
	assert.Equal(t, int64(1024), interruptedStatus.TargetBytes)
	assert.Equal(t, float32(0.25), interruptedStatus.Progress)

	pathBody, err := json.Marshal(api.PreloadRequest{FilePath: utils.Ptr(to.Files()[0].Path())})
	require.NoError(t, err)
	byPath := doRequest(http.MethodPut, preloadURL, string(pathBody))
	require.Equal(t, http.StatusOK, byPath.Code)
	assert.Equal(t, 0, decodeStatus(byPath).FileIndex)
	_, snapshotExists := ctrl.preloadSnapshots.Load(ih)
	assert.False(t, snapshotExists, "a new preload must replace historical status")
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

func TestPreloadTask_ProgressBytes(t *testing.T) {
	t.Run("never decreases", func(t *testing.T) {
		task := &preloadTask{targetBytes: 1000}

		task.bytesRead.Store(600)
		assert.Equal(t, int64(600), task.progressBytes())
		task.bytesRead.Store(800)
		assert.Equal(t, int64(800), task.progressBytes())
		task.bytesRead.Store(1200)
		assert.Equal(t, int64(1000), task.progressBytes())
	})

	t.Run("counts completed pieces", func(t *testing.T) {
		completed := int64(0)
		task := &preloadTask{targetBytes: 1000, completedBytes: func() int64 { return completed }}

		task.bytesRead.Store(100)
		assert.Equal(t, int64(100), task.progressBytes(), "no piece complete yet")
		completed = 700
		assert.Equal(t, int64(700), task.progressBytes(), "complete pieces count before the reader reaches them")
		completed = 1200
		assert.Equal(t, int64(1000), task.progressBytes())
	})

	t.Run("holds high-water mark", func(t *testing.T) {
		completed := int64(700)
		task := &preloadTask{targetBytes: 1000, completedBytes: func() int64 { return completed }}

		assert.Equal(t, int64(700), task.progressBytes())
		completed = 200
		assert.Equal(t, int64(700), task.progressBytes(), "evicted pieces must not lower reported progress")
		task.bytesRead.Store(900)
		assert.Equal(t, int64(900), task.progressBytes())
	})
}

func TestCompletedRangeBytes(t *testing.T) {
	// A file at torrent offset 8 over 16-byte pieces; pieces 1 and 3 are complete.
	complete := map[int]bool{1: true, 3: true}
	pieceComplete := func(index int) bool { return complete[index] }
	tests := []struct {
		name       string
		start, end int64
		expected   int64
	}{
		{name: "empty range", start: 10, end: 10, expected: 0},
		{name: "incomplete first piece only", start: 0, end: 8, expected: 0},
		{name: "partial first and complete second piece", start: 0, end: 24, expected: 16},
		{name: "range ends inside complete piece", start: 0, end: 12, expected: 4},
		{name: "range starts inside complete piece", start: 20, end: 40, expected: 4},
		{name: "spans complete and incomplete pieces", start: 0, end: 64, expected: 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, completedRangeBytes(pieceComplete, 16, 8, tt.start, tt.end))
		})
	}
	assert.Zero(t, completedRangeBytes(pieceComplete, 0, 0, 0, 64), "invalid piece length")
}

func TestPreloadMemoryLimit(t *testing.T) {
	const mib = int64(1 << 20)
	tests := []struct {
		name     string
		capacity int64
		expected int64
	}{
		{name: "no capacity", capacity: 0, expected: 0},
		{name: "negative capacity", capacity: -1, expected: 0},
		// The default 64 MiB memory limit leaves 16 MiB of preload capacity.
		// Halving it would drop below a head and tail boundary, so one preload
		// takes it all.
		{name: "default memory runs one preload", capacity: 16 * mib, expected: 16 * mib},
		{name: "share below boundary runs one preload", capacity: 30 * mib, expected: 30 * mib},
		{name: "share at boundary runs two preloads", capacity: 32 * mib, expected: 16 * mib},
		// 128 MiB of memory leaves about 60.8 MiB, enough for two preloads.
		{name: "128 MiB memory runs two preloads", capacity: 63753420, expected: 31876710},
		{name: "ample capacity is capped by latency ceiling", capacity: 1 << 30, expected: maxPreloadBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit := preloadMemoryLimit(tt.capacity)
			assert.Equal(t, tt.expected, limit)
			assert.LessOrEqual(t, limit, max(tt.capacity, 0), "a preload must fit the pool capacity")
		})
	}
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

func TestStartPreloadFitsRoundedReservationInDefaultMemory(t *testing.T) {
	const mib = int64(1 << 20)
	tests := []struct {
		name        string
		length      int64
		pieceLength int64
	}{
		// The final piece holds a single byte, so the tail range touches an
		// extra piece that byte-sized ranges did not account for.
		{name: "partial final piece", length: 100*mib + 1, pieceLength: 4 * mib},
		{name: "piece larger than boundary", length: 1<<30 + 12345, pieceLength: 16 * mib},
		{name: "small pieces", length: 700*mib + 3, pieceLength: 512 << 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, cleanup := newTestController(t)
			defer cleanup()
			ctrl.mu.RLock()
			maxMemory := utils.Val(ctrl.settings.Load().MaxMemory)
			ctrl.mu.RUnlock()
			require.Equal(t, 64*mib, maxMemory, "test assumes the default memory limit")

			to := addSyntheticTorrent(t, ctrl, tt.length, tt.pieceLength)
			ih := to.InfoHash()
			defer ctrl.cancelPreload(ih)

			preload := ctrl.startPreload(to, to.Files()[0], 0)
			require.NotNil(t, preload)
			current, registered := ctrl.preloads.Load(ih)
			require.True(t, registered, "preload was dropped because its reservation did not fit")
			assert.Same(t, preload, current)
			ctrl.preloadsMu.Lock()
			active := preload.active
			ctrl.preloadsMu.Unlock()
			assert.True(t, active, "preload must be admitted")

			limit := preloadMemoryLimit(ctrl.streamPool.Load().PreloadCapacity())
			assert.LessOrEqual(t, preload.reserveBytes, limit)
			assert.Positive(t, preload.targetBytes)
			assert.LessOrEqual(t, preload.targetBytes, preload.reserveBytes)
		})
	}
}

func TestStartPreloadSkipsWhenOnePieceExceedsBudget(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	// A 32 MiB limit leaves 8 MiB of preload capacity, less than one 16 MiB
	// piece.
	setTestMemoryLimit(t, ctrl, 32<<20)

	to := addSyntheticTorrent(t, ctrl, 1<<30, 16<<20)
	assert.Nil(t, ctrl.startPreload(to, to.Files()[0], 0))
	_, registered := ctrl.preloads.Load(to.InfoHash())
	assert.False(t, registered)
}

func TestStartPreloadSkipsWhileClientReconfigures(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ctrl.torrentClientUnavailable.Store(true)
	defer ctrl.torrentClientUnavailable.Store(false)

	assert.Nil(t, ctrl.startPreload(to, to.Files()[0], 0))
	_, registered := ctrl.preloads.Load(to.InfoHash())
	assert.False(t, registered)
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
	assert.Nil(t, ctrl.startPreload(to, to.Files()[0], 0))
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
	assert.Nil(t, ctrl.startPreload(to, file, 0))
	_, registered := ctrl.preloads.Load(to.InfoHash())
	assert.False(t, registered)
}

func TestStartPreloadWithoutClientReturnsNil(t *testing.T) {
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

	assert.Nil(t, ctrl.startPreload(to, to.Files()[0], 0))
	_, ok := ctrl.clientTorrent(to.InfoHash())
	assert.False(t, ok)
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

// TestStreamWaitsForCancelledPreloadWorkersWithoutPreloadLock verifies that
// playback lets preload workers it cancelled exit before acquiring its
// reader, and waits without holding preloadsMu, which exiting workers take.
func TestStreamWaitsForCancelledPreloadWorkersWithoutPreloadLock(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	ih := to.InfoHash()
	pool := ctrl.streamPool.Load()

	// A cancelled preload worker that has not exited yet.
	exiting := &preloadTask{active: true, cancel: func() {}, done: make(chan struct{}), infoHash: metainfo.Hash{9}}
	// Let the worker exit even when an assertion fails, or controller
	// cleanup would wait for it forever.
	defer exiting.doneOnce.Do(func() { close(exiting.done) })
	ctrl.preloadsMu.Lock()
	ctrl.preloadWorkers = append(ctrl.preloadWorkers, exiting)
	ctrl.preloadsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	go func() {
		defer close(served)
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/stream/%s?index=0", ih), http.NoBody).WithContext(ctx)
		ctrl.router.ServeHTTP(httptest.NewRecorder(), req)
	}()

	// The request opens its playback session, then waits with preloadsMu
	// free and without a reader.
	require.Eventually(t, func() bool {
		ctrl.preloadsMu.Lock()
		defer ctrl.preloadsMu.Unlock()
		return ctrl.preloadPlaybackCount == 1
	}, 5*time.Second, time.Millisecond, "playback session was not opened")
	require.Never(t, func() bool { return pool.HasReaders(ih) }, 100*time.Millisecond, time.Millisecond,
		"playback acquired its reader before the cancelled worker exited")

	// Once the worker exits, playback acquires its reader.
	exiting.doneOnce.Do(func() { close(exiting.done) })
	ctrl.finishPreload(exiting, false, false)
	require.Eventually(t, func() bool { return pool.HasReaders(ih) }, 5*time.Second, time.Millisecond,
		"playback did not acquire its reader after the worker exited")

	cancel()
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("stream request did not end after its context was cancelled")
	}
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

func TestStartPreloadConcurrencyFollowsPoolCapacity(t *testing.T) {
	tests := []struct {
		name         string
		memory       int64
		secondActive bool
	}{
		{name: "default memory runs one at a time", memory: 64 << 20, secondActive: false},
		{name: "128 MiB runs two concurrently", memory: 128 << 20, secondActive: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl, cleanup := newTestController(t)
			defer cleanup()
			setTestMemoryLimit(t, ctrl, tt.memory)

			first := addSyntheticTorrent(t, ctrl, 2<<30, 1<<20)
			second := addSyntheticTorrent(t, ctrl, 2<<30+1, 1<<20)
			defer ctrl.cancelAllPreloads()

			firstTask := ctrl.startPreload(first, first.Files()[0], 0)
			secondTask := ctrl.startPreload(second, second.Files()[0], 0)
			require.NotNil(t, firstTask)
			require.NotNil(t, secondTask)

			ctrl.preloadsMu.Lock()
			firstActive, secondActive, secondQueued := firstTask.active, secondTask.active, secondTask.queued
			ctrl.preloadsMu.Unlock()
			assert.True(t, firstActive)
			assert.Equal(t, tt.secondActive, secondActive)
			assert.Equal(t, !tt.secondActive, secondQueued, "a preload that does not fit must wait, not be dropped")
			_, registered := ctrl.preloads.Load(second.InfoHash())
			assert.True(t, registered)
		})
	}
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

	preload := ctrl.startPreload(to, to.Files()[0], 0)
	require.NotNil(t, preload)
	assert.Equal(t, int64(maxPreloadBytes), preload.targetBytes)
}

func TestPreloadTaskProgressFloor(t *testing.T) {
	task := &preloadTask{targetBytes: 1000, progressFloor: 400}
	assert.Equal(t, int64(400), task.progressBytes(), "floor applies before the resumed run catches up")
	task.bytesRead.Store(700)
	assert.Equal(t, int64(700), task.progressBytes())
	task.bytesRead.Store(1200)
	assert.Equal(t, int64(1000), task.progressBytes())
}

func TestStartPreloadReservesWholeProtectedPieces(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	to := addSintelTorrent(t, ctrl)
	file := to.Files()[0]

	ctrl.preloadsMu.Lock()
	ctrl.preloadPlaybackCount = 1
	ctrl.preloadsMu.Unlock()
	defer func() {
		ctrl.cancelPreload(to.InfoHash())
		ctrl.preloadsMu.Lock()
		ctrl.preloadPlaybackCount = 0
		ctrl.preloadsMu.Unlock()
	}()

	preload := ctrl.startPreload(to, file, 0)
	require.NotNil(t, preload)
	pieceLength := to.Info().PieceLength
	assert.GreaterOrEqual(t, preload.reserveBytes, preload.targetBytes)
	assert.Zero(t, preload.reserveBytes%pieceLength, "reservation must cover whole pieces")
}

func TestPreloadRemovedWhenTorrentCloses(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	to := addSintelTorrent(t, ctrl)
	ih := to.InfoHash()

	ctrl.preloadsMu.Lock()
	ctrl.preloadPlaybackCount = 1
	ctrl.preloadsMu.Unlock()
	defer func() {
		ctrl.preloadsMu.Lock()
		ctrl.preloadPlaybackCount = 0
		ctrl.preloadsMu.Unlock()
	}()

	stale := ctrl.startPreload(to, to.Files()[0], 0)
	require.NotNil(t, stale)
	// Model a completed preload whose cache is lost when the torrent is dropped
	// by a path that does not cancel preloads, such as a storage migration.
	stale.ready.Store(true)

	to.Drop()
	<-to.Closed()
	assert.Equal(t, api.Idle, ctrl.getPreloadStatus(ih).Status, "closed torrent reported a ready preload")
	// A preload is deleted from the registry before it is retired, so wait for
	// the retirement itself.
	select {
	case <-stale.retired:
	case <-time.After(time.Second):
		t.Fatal("removed preload was not retired")
	}
	_, exists := ctrl.preloads.Load(ih)
	assert.False(t, exists)

	// Re-adding the torrent creates a new instance, which must get a new task.
	readded := addSintelTorrent(t, ctrl)
	require.NotSame(t, to, readded)
	fresh := ctrl.startPreload(readded, readded.Files()[0], 0)
	require.NotNil(t, fresh)
	assert.NotSame(t, stale, fresh)
	assert.False(t, fresh.ready.Load())
	ctrl.cancelPreload(ih)
}

func TestPreloadSchedulerLimitsConcurrencyAndReleasesCapacity(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: time.Hour}
	type scheduledTask struct {
		preload        *preloadTask
		started        chan struct{}
		complete       chan struct{}
		budgetReleases atomic.Int32
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
			ctrl.finishPreload(preload, completed, false)
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
	assert.Len(t, ctrl.preloadWorkers, maxConcurrentPreloads)
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
	assert.Zero(t, first.budgetReleases.Load(), "ready preload must keep the reservation and protection covering its pinned cache")
	current, ok := ctrl.preloads.Load(first.preload.infoHash)
	require.True(t, ok)
	assert.Same(t, first.preload, current)

	ctrl.cancelPreload(first.preload.infoHash)
	assert.Equal(t, int32(1), first.budgetReleases.Load(), "cancelling must return the reservation and its protection")
	ctrl.cancelAllPreloads()
	ctrl.preloadsMu.Lock()
	assert.Empty(t, ctrl.preloadWorkers)
	ctrl.preloadsMu.Unlock()
}

func TestPreloadSchedulerResumesAfterPlayback(t *testing.T) {
	ctrl := &Controller{preloadPlaybackCount: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	preload := &preloadTask{
		ctx:           ctx,
		infoHash:      metainfo.Hash{1},
		cancel:        cancel,
		done:          make(chan struct{}),
		queued:        true,
		reserveBudget: func() bool { return true },
	}
	preload.start = func() {
		close(started)
		preload.doneOnce.Do(func() { close(preload.done) })
		ctrl.finishPreload(preload, false, false)
	}
	ctrl.preloads.Store(preload.infoHash, preload)
	ctrl.preloadQueue = append(ctrl.preloadQueue, preload)

	ctrl.preloadsMu.Lock()
	ctrl.dispatchPreloadsLocked()
	assert.True(t, preload.queued)
	assert.False(t, preload.active)
	ctrl.preloadPlaybackCount--
	ctrl.dispatchPreloadsLocked()
	ctrl.preloadsMu.Unlock()

	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestPreparePreloadsForPlaybackCancelsWorkersAndReleasesReadyMemory(t *testing.T) {
	ctrl := &Controller{preloadPlaybackCount: 1}

	readyCtx, readyCancel := context.WithCancel(context.Background())
	defer readyCancel()
	var readyBudgetReleases atomic.Int32
	ready := &preloadTask{
		ctx:         readyCtx,
		infoHash:    metainfo.Hash{1},
		cancel:      readyCancel,
		done:        make(chan struct{}),
		fileIndex:   1,
		filePath:    "ready.mkv",
		targetBytes: 2048,
		releaseBudget: func() {
			readyBudgetReleases.Add(1)
		},
	}
	ready.bytesRead.Store(ready.targetBytes)
	ready.ready.Store(true)
	ready.doneOnce.Do(func() { close(ready.done) })
	ctrl.preloads.Store(ready.infoHash, ready)

	activeCtx, activeCancel := context.WithCancel(context.Background())
	active := &preloadTask{
		ctx:         activeCtx,
		infoHash:    metainfo.Hash{2},
		cancel:      activeCancel,
		done:        make(chan struct{}),
		fileIndex:   2,
		filePath:    "active.mkv",
		targetBytes: 4096,
		active:      true,
	}
	active.bytesRead.Store(1024)
	ctrl.preloadWorkers = []*preloadTask{active}
	ctrl.preloads.Store(active.infoHash, active)
	go func() {
		<-activeCtx.Done()
		active.doneOnce.Do(func() { close(active.done) })
	}()

	ctrl.preloadsMu.Lock()
	ctrl.preparePreloadsForPlaybackLocked(metainfo.Hash{3}, "playing.mkv")
	ctrl.preloadsMu.Unlock()

	_, ok := ctrl.preloads.Load(ready.infoHash)
	assert.False(t, ok)
	_, ok = ctrl.preloads.Load(active.infoHash)
	assert.False(t, ok)
	assert.Len(t, ctrl.preloadWorkers, 1, "the cancelled worker keeps its slot until it exits")
	assert.Error(t, activeCtx.Err())
	assert.Equal(t, int32(1), readyBudgetReleases.Load())
	readyValue, ok := ctrl.preloadSnapshots.Load(ready.infoHash)
	require.True(t, ok)
	readySnapshot := readyValue.(*preloadStatusSnapshot)
	assert.Equal(t, ready.targetBytes, readySnapshot.completedBytes)
	assert.Equal(t, ready.targetBytes, readySnapshot.targetBytes)
	assert.Equal(t, float32(1), readySnapshot.progress)
	activeValue, ok := ctrl.preloadSnapshots.Load(active.infoHash)
	require.True(t, ok)
	activeSnapshot := activeValue.(*preloadStatusSnapshot)
	assert.Equal(t, int64(1024), activeSnapshot.completedBytes)
	assert.Equal(t, active.targetBytes, activeSnapshot.targetBytes)
	assert.Equal(t, float32(0.25), activeSnapshot.progress)

	// The worker frees its slot when it exits.
	<-active.done
	ctrl.finishPreload(active, false, false)
	assert.Empty(t, ctrl.preloadWorkers)
}

func TestPreparePreloadsForPlaybackRequeuesInterruptedWorker(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	to := addSintelTorrent(t, ctrl)
	ih := to.InfoHash()
	defer ctrl.cancelPreload(ih)

	// The torrent has no peers, so the dispatched worker stays active.
	interrupted := ctrl.startPreload(to, to.Files()[0], 0)
	require.NotNil(t, interrupted)
	ctrl.preloadsMu.Lock()
	dispatched := interrupted.active
	ctrl.preloadsMu.Unlock()
	require.True(t, dispatched, "preload must be dispatched while idle")

	// Model progress the worker had made before playback interrupts it.
	interrupted.bytesRead.Store(1234)

	// Playback of another torrent starts between a player's range requests.
	// Assertions run after unlocking so a failure cannot deadlock cleanup.
	ctrl.preloadsMu.Lock()
	ctrl.preloadPlaybackCount++
	ctrl.preparePreloadsForPlaybackLocked(metainfo.Hash{9}, "other.mkv")
	current, registered := ctrl.preloads.Load(ih)
	replacement, _ := current.(*preloadTask)
	var queued, active bool
	if replacement != nil {
		queued, active = replacement.queued, replacement.active
	}
	var queueHead *preloadTask
	if len(ctrl.preloadQueue) > 0 {
		queueHead = ctrl.preloadQueue[0]
	}
	activeTasks := len(ctrl.preloadWorkers)
	ctrl.preloadsMu.Unlock()
	defer func() {
		ctrl.preloadsMu.Lock()
		if ctrl.preloadPlaybackCount > 0 {
			ctrl.preloadPlaybackCount--
		}
		ctrl.preloadsMu.Unlock()
	}()

	require.True(t, registered, "interrupted preload must stay registered")
	require.NotNil(t, replacement)
	assert.NotSame(t, interrupted, replacement)
	assert.True(t, queued)
	assert.False(t, active)
	assert.Same(t, replacement, queueHead)
	assert.Equal(t, 1, activeTasks, "the cancelled worker keeps its slot until it exits")
	assert.Equal(t, int64(1234), replacement.progressBytes(), "re-queued progress must not fall back")

	// Playback cancels the worker without waiting; it exits on its own and
	// frees its slot.
	assert.ErrorIs(t, interrupted.ctx.Err(), context.Canceled)
	require.Eventually(t, func() bool {
		ctrl.preloadsMu.Lock()
		defer ctrl.preloadsMu.Unlock()
		return len(ctrl.preloadWorkers) == 0
	}, 10*time.Second, time.Millisecond, "interrupted worker did not exit")
	select {
	case <-interrupted.done:
	default:
		t.Fatal("interrupted worker exited without closing done")
	}
	_, snapshotted := ctrl.preloadSnapshots.Load(ih)
	assert.False(t, snapshotted, "re-queued preload must not report Superseded")
	assert.Equal(t, api.Preloading, ctrl.getPreloadStatus(ih).Status)

	// The interrupted worker's completion must not remove its replacement.
	require.Never(t, func() bool {
		current, ok := ctrl.preloads.Load(ih)
		return !ok || current != replacement
	}, 50*time.Millisecond, time.Millisecond)

	// Ending playback resumes the re-queued preload.
	ctrl.preloadsMu.Lock()
	ctrl.preloadPlaybackCount--
	ctrl.dispatchPreloadsLocked()
	resumed := replacement.active
	ctrl.preloadsMu.Unlock()
	assert.True(t, resumed, "re-queued preload must resume when playback ends")
}

func TestPreparePreloadsForPlaybackReleasesReadyMemoryWithinTorrent(t *testing.T) {
	ctrl := &Controller{preloadPlaybackCount: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var budgetReleases atomic.Int32
	preload := &preloadTask{
		ctx:      ctx,
		infoHash: metainfo.Hash{1},
		cancel:   cancel,
		done:     make(chan struct{}),
		filePath: "next-episode.mkv",
		releaseBudget: func() {
			budgetReleases.Add(1)
		},
	}
	preload.ready.Store(true)
	preload.doneOnce.Do(func() { close(preload.done) })
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	ctrl.preparePreloadsForPlaybackLocked(preload.infoHash, "current-episode.mkv")
	ctrl.preloadsMu.Unlock()

	_, ok := ctrl.preloads.Load(preload.infoHash)
	assert.False(t, ok, "playback of another file must release the memory preload lease")
	assert.Equal(t, int32(1), budgetReleases.Load())
}

// newLeasablePreload returns a ready memory preload whose reservation releases
// are counted.
func newLeasablePreload(ih metainfo.Hash, filePath string, budgetReleases *atomic.Int32) *preloadTask {
	ctx, cancel := context.WithCancel(context.Background())
	preload := &preloadTask{
		ctx:         ctx,
		infoHash:    ih,
		cancel:      cancel,
		done:        make(chan struct{}),
		fileIndex:   2,
		filePath:    filePath,
		targetBytes: 1024,
		releaseBudget: func() {
			budgetReleases.Add(1)
		},
	}
	preload.bytesRead.Store(preload.targetBytes)
	preload.ready.Store(true)
	preload.doneOnce.Do(func() { close(preload.done) })
	return preload
}

func TestCancelPreloadReleasesPlaybackLease(t *testing.T) {
	ctrl := &Controller{}
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	var budgetReleases atomic.Int32
	preload := newLeasablePreload(metainfo.Hash{1}, "playing.mkv", &budgetReleases)
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	session := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	ctrl.preloadsMu.Unlock()

	ctrl.cancelPreload(preload.infoHash)
	assert.Nil(t, session.lease)
	assert.Equal(t, int32(1), budgetReleases.Load())
	assert.Equal(t, 1, ctrl.preloadPlaybackCount, "cancelling the preload must not end the playback session")

	ctrl.preloadsMu.Lock()
	ctrl.endPlaybackRequestLocked(session)
	ctrl.closePlaybackSessionLocked(session)
	ctrl.preloadsMu.Unlock()
	assert.Equal(t, int32(1), budgetReleases.Load(), "a released lease must not be released twice")
}

// TestController_CancelPreload verifies that cancelling a running preload
// releases its memory at once without waiting for the worker, which exits on
// its own and then frees its slot.
func TestController_CancelPreload(t *testing.T) {
	ctrl := &Controller{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var budgetReleases atomic.Int32
	running := &preloadTask{
		active:        true,
		cancel:        cancel,
		ctx:           ctx,
		done:          make(chan struct{}),
		infoHash:      metainfo.Hash{1},
		releaseBudget: func() { budgetReleases.Add(1) },
	}
	ctrl.preloadWorkers = []*preloadTask{running}
	ctrl.preloads.Store(running.infoHash, running)

	returned := make(chan struct{})
	go func() {
		ctrl.cancelPreload(running.infoHash)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelPreload waited for the running worker")
	}

	require.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Equal(t, int32(1), budgetReleases.Load())
	assert.Len(t, ctrl.preloadWorkers, 1, "the worker keeps its slot until it exits")

	running.doneOnce.Do(func() { close(running.done) })
	ctrl.finishPreload(running, false, false)
	assert.Empty(t, ctrl.preloadWorkers)
	assert.Equal(t, int32(1), budgetReleases.Load(), "the worker's exit must not release the budget twice")
}

// TestController_CancelAllPreloads verifies that cancelAllPreloads waits for
// every running worker to exit, including one cancelled earlier, and does so
// without holding preloadsMu, which the exiting workers take.
func TestController_CancelAllPreloads(t *testing.T) {
	ctrl := &Controller{}
	newWorker := func(hash metainfo.Hash) *preloadTask {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return &preloadTask{active: true, cancel: cancel, ctx: ctx, done: make(chan struct{}), infoHash: hash}
	}
	// exit ends a worker the way runPreload does: it closes done, then takes
	// preloadsMu in finishPreload.
	exit := func(worker *preloadTask) {
		worker.doneOnce.Do(func() { close(worker.done) })
		ctrl.finishPreload(worker, false, false)
	}
	registered := newWorker(metainfo.Hash{1})
	cancelled := newWorker(metainfo.Hash{2})
	ctrl.preloadWorkers = []*preloadTask{registered, cancelled}
	ctrl.preloads.Store(registered.infoHash, registered)
	ctrl.preloads.Store(cancelled.infoHash, cancelled)
	ctrl.cancelPreload(cancelled.infoHash)

	returned := make(chan struct{})
	go func() {
		ctrl.cancelAllPreloads()
		close(returned)
	}()
	require.Eventually(t, func() bool { return registered.ctx.Err() != nil }, 5*time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		if !ctrl.preloadsMu.TryLock() {
			return false
		}
		ctrl.preloadsMu.Unlock()
		return true
	}, 5*time.Second, time.Millisecond, "cancelAllPreloads held preloadsMu while waiting")

	exit(registered)
	require.Never(t, func() bool {
		select {
		case <-returned:
			return true
		default:
			return false
		}
	}, 50*time.Millisecond, time.Millisecond, "cancelAllPreloads returned before the earlier cancelled worker exited")

	exit(cancelled)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelAllPreloads did not return after every worker exited")
	}
	assert.Empty(t, ctrl.preloadWorkers)
}

func TestWaitForPreloadWorkers(t *testing.T) {
	t.Run("returns once every worker exits", func(t *testing.T) {
		first, second := make(chan struct{}), make(chan struct{})
		returned := make(chan struct{})
		go func() {
			waitForPreloadWorkers(context.Background(), []<-chan struct{}{first, second})
			close(returned)
		}()

		close(first)
		require.Never(t, func() bool {
			select {
			case <-returned:
				return true
			default:
				return false
			}
		}, 50*time.Millisecond, time.Millisecond, "returned before every worker exited")
		close(second)
		require.Eventually(t, func() bool {
			select {
			case <-returned:
				return true
			default:
				return false
			}
		}, 5*time.Second, time.Millisecond)
	})

	t.Run("returns when the context ends", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		returned := make(chan struct{})
		go func() {
			waitForPreloadWorkers(ctx, []<-chan struct{}{make(chan struct{})})
			close(returned)
		}()

		select {
		case <-returned:
		case <-time.After(time.Second):
			t.Fatal("waited past the end of the context")
		}
	})
}

// TestController_GetPreloadStatus verifies how the status of a running
// preload is reported. Its progress reads piece states under the torrent
// client lock, so it must be computed without holding preloadsMu.
func TestController_GetPreloadStatus(t *testing.T) {
	newRunning := func(completedBytes func() int64) *preloadTask {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return &preloadTask{
			cancel:         cancel,
			completedBytes: completedBytes,
			ctx:            ctx,
			fileIndex:      2,
			filePath:       "movie.mkv",
			infoHash:       metainfo.Hash{1},
			targetBytes:    2048,
		}
	}

	t.Run("computes progress without holding preloadsMu", func(t *testing.T) {
		ctrl := &Controller{}
		var lockHeld atomic.Bool
		running := newRunning(func() int64 {
			if ctrl.preloadsMu.TryLock() {
				ctrl.preloadsMu.Unlock()
			} else {
				lockHeld.Store(true)
			}
			return 512
		})
		ctrl.preloads.Store(running.infoHash, running)

		status := ctrl.getPreloadStatus(running.infoHash)

		assert.False(t, lockHeld.Load(), "progress was computed while holding preloadsMu")
		assert.Equal(t, api.Preloading, status.Status)
		assert.Equal(t, int64(512), status.CompletedBytes)
		assert.Equal(t, int64(2048), status.TargetBytes)
		assert.InDelta(t, 0.25, status.Progress, 0.001)
		assert.Equal(t, 2, status.FileIndex)
	})

	t.Run("reports the current state when the preload ends during the poll", func(t *testing.T) {
		ctrl := &Controller{}
		var running *preloadTask
		running = newRunning(func() int64 {
			ctrl.cancelPreload(running.infoHash)
			return 512
		})
		ctrl.preloads.Store(running.infoHash, running)

		status := ctrl.getPreloadStatus(running.infoHash)

		assert.Equal(t, api.Idle, status.Status)
		assert.Zero(t, status.CompletedBytes)
	})
}

func TestDroppedTorrentDoesNotReportFailedPreload(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	setTestMemoryLimit(t, ctrl, 128<<20)
	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	task := ctrl.startPreload(to, to.Files()[0], 0)
	require.NotNil(t, task)
	require.True(t, task.active)
	// Stop the torrent watcher so the worker, which is blocked on a read
	// because no peers serve the synthetic torrent, is what retires the task.
	task.retire()

	to.Drop()
	<-to.Closed()
	<-task.done
	require.Eventually(t, func() bool {
		_, registered := ctrl.preloads.Load(to.InfoHash())
		return !registered
	}, time.Second, 5*time.Millisecond)
	assert.NotEqual(t, api.Failed, ctrl.getPreloadStatus(to.InfoHash()).Status, "a dropped torrent is not a failed preload")
}

func TestPlaybackSessionClosesImmediatelyWithoutGracePeriod(t *testing.T) {
	ctrl := &Controller{}
	var budgetReleases atomic.Int32
	preload := newLeasablePreload(metainfo.Hash{1}, "playing.mkv", &budgetReleases)
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	session := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	ctrl.endPlaybackRequestLocked(session)
	ctrl.preloadsMu.Unlock()

	assert.Zero(t, ctrl.preloadPlaybackCount)
	assert.Empty(t, ctrl.playbackSessions)
	assert.Equal(t, int32(1), budgetReleases.Load())
}

func TestPlaybackSessionForAnotherFileClosesIdleSession(t *testing.T) {
	ctrl := &Controller{}
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	var budgetReleases atomic.Int32
	preload := newLeasablePreload(metainfo.Hash{1}, "episode-1.mkv", &budgetReleases)
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	idle := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	ctrl.endPlaybackRequestLocked(idle)
	next := ctrl.beginPlaybackLocked(preload.infoHash, "episode-2.mkv")
	ctrl.preloadsMu.Unlock()

	assert.NotSame(t, idle, next)
	assert.Nil(t, idle.closeTimer, "closing the idle session must stop its timer")
	assert.Equal(t, 1, ctrl.preloadPlaybackCount, "only the new session remains open")
	assert.Equal(t, int32(1), budgetReleases.Load(), "the idle session's lease must return its reservation")

	ctrl.preloadsMu.Lock()
	ctrl.endPlaybackRequestLocked(next)
	ctrl.closePlaybackSessionLocked(next)
	ctrl.preloadsMu.Unlock()
	assert.Zero(t, ctrl.preloadPlaybackCount)
}

func TestPlaybackSessionHoldsReadyPreloadAcrossRequests(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: time.Hour}
	ctrl.runtimeConfig.playbackGracePeriod = 50 * time.Millisecond
	var budgetReleases atomic.Int32
	preload := newLeasablePreload(metainfo.Hash{1}, "playing.mkv", &budgetReleases)
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	first := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	ctrl.preloadsMu.Unlock()
	require.Same(t, preload, first.lease, "playback must take over the ready preload of its file")
	_, ok := ctrl.preloads.Load(preload.infoHash)
	assert.False(t, ok, "playing file must release its scheduler task")
	status := ctrl.getPreloadStatus(preload.infoHash)
	assert.Equal(t, api.Ready, status.Status, "a preload held by playback is still ready")
	assert.Equal(t, preload.fileIndex, status.FileIndex)
	assert.Equal(t, preload.targetBytes, status.CompletedBytes)

	// A player's next range request arrives after the first one ended but
	// within the grace period, so it joins the session and keeps the lease.
	ctrl.preloadsMu.Lock()
	ctrl.endPlaybackRequestLocked(first)
	second := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	ctrl.preloadsMu.Unlock()
	assert.Same(t, first, second)
	assert.Same(t, preload, second.lease)
	assert.Equal(t, 1, ctrl.preloadPlaybackCount)
	assert.Zero(t, budgetReleases.Load(), "the gap between range requests must keep the reservation")
	assert.Zero(t, budgetReleases.Load(), "the gap between range requests must keep the protection")

	ctrl.preloadsMu.Lock()
	ctrl.endPlaybackRequestLocked(second)
	ctrl.preloadsMu.Unlock()
	require.Eventually(t, func() bool {
		ctrl.preloadsMu.Lock()
		defer ctrl.preloadsMu.Unlock()
		return ctrl.preloadPlaybackCount == 0
	}, time.Second, 5*time.Millisecond, "the session must close after the grace period")
	assert.Equal(t, int32(1), budgetReleases.Load(), "closing the session must return the reservation")
	assert.Equal(t, int32(1), budgetReleases.Load(), "closing the session must clear the protection")
	assert.Equal(t, api.Idle, ctrl.getPreloadStatus(preload.infoHash).Status)
}

func TestPlaybackSessionJoinReleasesStaleLease(t *testing.T) {
	ctrl := &Controller{}
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	var budgetReleases atomic.Int32
	preload := newLeasablePreload(metainfo.Hash{1}, "playing.mkv", &budgetReleases)
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	defer ctrl.preloadsMu.Unlock()
	session := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	ctrl.endPlaybackRequestLocked(session)
	require.Same(t, preload, session.lease)

	// The torrent was replaced during the grace period, taking the cache with it.
	preload.cacheResident = func() bool { return false }
	joined := ctrl.beginPlaybackLocked(preload.infoHash, preload.filePath)
	assert.Same(t, session, joined)
	assert.Nil(t, joined.lease, "joining must drop a lease whose cache is gone")
	assert.Equal(t, int32(1), budgetReleases.Load(), "the stale reservation must stop suppressing reader boundaries")

	ctrl.endPlaybackRequestLocked(joined)
	ctrl.closePlaybackSessionLocked(joined)
}

func TestPreloadRequestClosesIdlePlaybackSession(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	setTestMemoryLimit(t, ctrl, 128<<20)
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	played := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	next := addSyntheticTorrent(t, ctrl, 1<<30+1, 1<<20)
	defer ctrl.cancelAllPreloads()

	ctrl.preloadsMu.Lock()
	session := ctrl.beginPlaybackLocked(played.InfoHash(), played.Files()[0].Path())
	ctrl.endPlaybackRequestLocked(session)
	ctrl.preloadsMu.Unlock()

	// The player closed; the session is only waiting out its grace period, so
	// the preload the player now waits on must start at once.
	task := ctrl.startPreload(next, next.Files()[0], 0)
	require.NotNil(t, task)
	ctrl.preloadsMu.Lock()
	defer ctrl.preloadsMu.Unlock()
	assert.Empty(t, ctrl.playbackSessions)
	assert.Zero(t, ctrl.preloadPlaybackCount)
	assert.True(t, task.active, "the requested preload must not wait for the grace period")
}

func TestPreloadRequestEndsPlaybackSessionWithoutGrace(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	setTestMemoryLimit(t, ctrl, 128<<20)
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	played := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	next := addSyntheticTorrent(t, ctrl, 1<<30+1, 1<<20)
	defer ctrl.cancelAllPreloads()

	ctrl.preloadsMu.Lock()
	session := ctrl.beginPlaybackLocked(played.InfoHash(), played.Files()[0].Path())
	ctrl.preloadsMu.Unlock()

	// The next episode's preload can arrive before the browser aborts the
	// previous stream, so it queues behind the active request.
	task := ctrl.startPreload(next, next.Files()[0], 0)
	require.NotNil(t, task)
	ctrl.preloadsMu.Lock()
	defer ctrl.preloadsMu.Unlock()
	assert.True(t, task.queued, "live playback still pauses preloading")

	ctrl.endPlaybackRequestLocked(session)
	assert.Empty(t, ctrl.playbackSessions, "the session must end with its last request")
	assert.True(t, task.active, "the waiting preload must start without a grace period")
}

func TestStartPreloadReturnsPlaybackLeaseForSameFile(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	file := to.Files()[0]
	var budgetReleases atomic.Int32
	lease := newLeasablePreload(to.InfoHash(), file.Path(), &budgetReleases)
	lease.torrent = to
	ctrl.preloads.Store(lease.infoHash, lease)

	ctrl.preloadsMu.Lock()
	session := ctrl.beginPlaybackLocked(lease.infoHash, lease.filePath)
	ctrl.endPlaybackRequestLocked(session)
	ctrl.preloadsMu.Unlock()
	require.Same(t, lease, session.lease)

	// Reopening the same file within the grace period finds its cache still
	// held, so it reports ready instead of queueing a fresh preload.
	assert.Same(t, lease, ctrl.startPreload(to, file, 0))
	assert.Equal(t, api.Ready, ctrl.getPreloadStatus(to.InfoHash()).Status)
	_, registered := ctrl.preloads.Load(to.InfoHash())
	assert.False(t, registered)

	ctrl.preloadsMu.Lock()
	rejoined := ctrl.beginPlaybackLocked(lease.infoHash, lease.filePath)
	ctrl.endPlaybackRequestLocked(rejoined)
	ctrl.closePlaybackSessionLocked(rejoined)
	ctrl.preloadsMu.Unlock()
	assert.Same(t, session, rejoined, "the reopened player joins the held session")
	assert.Equal(t, int32(1), budgetReleases.Load(), "the lease is released once, when the session closes")
}

func TestStartPreloadSkipsFileBeingPlayed(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	ctrl.runtimeConfig.playbackGracePeriod = time.Hour
	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	file := to.Files()[0]
	defer ctrl.cancelAllPreloads()

	ctrl.preloadsMu.Lock()
	session := ctrl.beginPlaybackLocked(to.InfoHash(), file.Path())
	ctrl.preloadSnapshots.Store(to.InfoHash(), &preloadStatusSnapshot{status: api.Superseded, targetBytes: 512})
	ctrl.preloadsMu.Unlock()

	// A TorrServer client polling preload for the file it is playing must not
	// register a competing preload or cut the session's grace period short.
	assert.Nil(t, ctrl.startPreload(to, file, 0))
	_, registered := ctrl.preloads.Load(to.InfoHash())
	assert.False(t, registered)
	assert.Equal(t, api.Superseded, ctrl.getPreloadStatus(to.InfoHash()).Status, "the last preload's status must remain")

	ctrl.preloadsMu.Lock()
	defer ctrl.preloadsMu.Unlock()
	ctrl.endPlaybackRequestLocked(session)
	assert.NotNil(t, session.closeTimer, "the session must still wait out its grace period")
	assert.Equal(t, 1, ctrl.preloadPlaybackCount)
	ctrl.closePlaybackSessionLocked(session)
}
func TestPreparePreloadsForPlaybackPreservesReadyFileStorage(t *testing.T) {
	ctrl := &Controller{preloadPlaybackCount: 1}
	preload := &preloadTask{
		infoHash: metainfo.Hash{1},
		cancel:   func() {},
		done:     make(chan struct{}),
		filePath: "cached.mkv",
	}
	preload.ready.Store(true)
	preload.doneOnce.Do(func() { close(preload.done) })
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	ctrl.preparePreloadsForPlaybackLocked(metainfo.Hash{2}, "playing.mkv")
	ctrl.preloadsMu.Unlock()

	current, ok := ctrl.preloads.Load(preload.infoHash)
	require.True(t, ok)
	assert.Same(t, preload, current)
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
	assert.Empty(t, ctrl.preloadWorkers)
	assert.Empty(t, ctrl.preloadQueue)
	ctrl.preloadsMu.Unlock()
}

func TestPreloadSchedulerEvictsReadyPreloadToReserveBudget(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: time.Hour}
	readyCtx, readyCancel := context.WithCancel(context.Background())
	defer readyCancel()
	var budgetReleases atomic.Int32
	ready := &preloadTask{
		ctx:           readyCtx,
		infoHash:      metainfo.Hash{1},
		cancel:        readyCancel,
		done:          make(chan struct{}),
		targetBytes:   32 << 20,
		releaseBudget: func() { budgetReleases.Add(1) },
	}
	ready.ready.Store(true)
	ready.readyAt = time.Now().Add(-time.Minute)
	ready.doneOnce.Do(func() { close(ready.done) })
	ctrl.preloads.Store(ready.infoHash, ready)
	var newerBudgetReleases atomic.Int32
	newerReady := &preloadTask{
		infoHash:      metainfo.Hash{4},
		cancel:        func() {},
		done:          make(chan struct{}),
		releaseBudget: func() { newerBudgetReleases.Add(1) },
		readyAt:       time.Now(),
	}
	newerReady.ready.Store(true)
	newerReady.doneOnce.Do(func() { close(newerReady.done) })
	ctrl.preloads.Store(newerReady.infoHash, newerReady)
	fileReady := &preloadTask{
		infoHash: metainfo.Hash{3},
		cancel:   func() {},
		done:     make(chan struct{}),
	}
	fileReady.ready.Store(true)
	fileReady.doneOnce.Do(func() { close(fileReady.done) })
	ctrl.preloads.Store(fileReady.infoHash, fileReady)

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
		ctrl.finishPreload(pending, false, false)
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
	_, ok := ctrl.preloads.Load(ready.infoHash)
	assert.False(t, ok, "evicted preload must leave the registry")
	currentNewerReady, ok := ctrl.preloads.Load(newerReady.infoHash)
	require.True(t, ok, "newer ready preload must remain registered")
	assert.Same(t, newerReady, currentNewerReady)
	assert.Zero(t, newerBudgetReleases.Load())
	currentFileReady, ok := ctrl.preloads.Load(fileReady.infoHash)
	require.True(t, ok, "file-storage preload must not be evicted for a memory reservation")
	assert.Same(t, fileReady, currentFileReady)
	snapshotValue, ok := ctrl.preloadSnapshots.Load(ready.infoHash)
	require.True(t, ok, "evicted preload must leave a status snapshot")
	snapshot := snapshotValue.(*preloadStatusSnapshot)
	assert.Equal(t, ready.targetBytes, snapshot.completedBytes)
	assert.Equal(t, float32(1), snapshot.progress)

	ctrl.cancelAllPreloads()
}

func TestCurrentPreloadLockedDiscardsReadyTaskAfterCacheEviction(t *testing.T) {
	ctrl := &Controller{}
	var budgetReleases atomic.Int32
	preload := &preloadTask{
		cacheResident: func() bool { return false },
		infoHash:      metainfo.Hash{1},
		cancel:        func() {},
		done:          make(chan struct{}),
		releaseBudget: func() {
			budgetReleases.Add(1)
		},
	}
	preload.ready.Store(true)
	preload.doneOnce.Do(func() { close(preload.done) })
	ctrl.preloads.Store(preload.infoHash, preload)

	ctrl.preloadsMu.Lock()
	current := ctrl.currentPreloadLocked(preload.infoHash)
	ctrl.preloadsMu.Unlock()

	assert.Nil(t, current)
	_, ok := ctrl.preloads.Load(preload.infoHash)
	assert.False(t, ok, "evicted ready cache must leave the preload registry")
	assert.Equal(t, int32(1), budgetReleases.Load())
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
		ctrl.finishPreload(follower, false, false)
	}

	ctrl.preloadsMu.Lock()
	for _, preload := range []*preloadTask{starved, follower} {
		ctrl.preloads.Store(preload.infoHash, preload)
		ctrl.preloadQueue = append(ctrl.preloadQueue, preload)
	}
	ctrl.dispatchPreloadsLocked()
	assert.Empty(t, ctrl.preloadQueue, "an unreservable task must not block the queue")
	assert.Len(t, ctrl.preloadWorkers, 1)
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
			ctx:           ctx,
			infoHash:      ih,
			cancel:        cancel,
			releaseBudget: func() { close(cleared) },
			done:          make(chan struct{}),
		}
		ctrl.preloads.Store(ih, task)

		returned := make(chan struct{})
		go func() {
			ctrl.finishPreload(task, false, true)
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
		status := ctrl.getPreloadStatus(ih)
		assert.Equal(t, api.Failed, status.Status, "a failure must stay visible after the task is removed")
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

		ctrl.finishPreload(task, true, false)

		assert.ErrorIs(t, ctx.Err(), context.Canceled)
		current, exists := ctrl.preloads.Load(ih)
		require.True(t, exists)
		assert.Same(t, task, current)
		require.NotNil(t, task.expiryTimer)
		ctrl.cancelPreload(ih)
	})
}

// TestReadPreloadRange verifies that readPreloadRange requires the entire range.
func TestReadPreloadRange(t *testing.T) {
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

func TestStartPreloadQueuesWhilePlaybackIsActive(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	require.NoError(t, sintelFile.Close())
	require.NoError(t, err)

	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()
	file := to.Files()[0]

	ctrl.preloadsMu.Lock()
	ctrl.preloadPlaybackCount = 1
	ctrl.preloadsMu.Unlock()

	preload := ctrl.startPreload(to, file, 0)
	require.NotNil(t, preload)
	assert.True(t, preload.queued)
	assert.False(t, preload.active)
	current, exists := ctrl.preloads.Load(to.InfoHash())
	require.True(t, exists)
	assert.Same(t, preload, current)

	ctrl.cancelPreload(to.InfoHash())
	ctrl.preloadsMu.Lock()
	ctrl.preloadPlaybackCount = 0
	ctrl.preloadsMu.Unlock()
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
		infoHash:      ih,
		cancel:        func() {},
		releaseBudget: func() { cleared = true },
		fileIndex:     0,
		filePath:      to.Files()[0].Path(),
	}
	task.ready.Store(true)
	ctrl.preloads.Store(ih, task)
	ctrl.preloadSnapshots.Store(ih, &preloadStatusSnapshot{status: api.Superseded, targetBytes: 512})

	ctrl.mu.Lock()
	err = ctrl.deleteTorrentLocked(ih)
	ctrl.mu.Unlock()
	require.NoError(t, err)
	assert.True(t, cleared)
	_, exists := ctrl.preloads.Load(ih)
	assert.False(t, exists)
	_, exists = ctrl.preloadSnapshots.Load(ih)
	assert.False(t, exists)
}

// TestDeleteTorrentWithRunningPreloadRemovesFileStorage verifies that deleting
// a torrent cancels its running preload without waiting for the worker, closes
// the torrent, and removes its file storage.
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

	// The torrent has no peers, so the dispatched worker blocks reading.
	preload := ctrl.startPreload(to, to.Files()[0], 0)
	require.NotNil(t, preload)
	ctrl.preloadsMu.Lock()
	running := slices.Contains(ctrl.preloadWorkers, preload)
	ctrl.preloadsMu.Unlock()
	require.True(t, running, "preload must be running before the delete")

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

	require.ErrorIs(t, preload.ctx.Err(), context.Canceled)
	select {
	case <-to.Closed():
	default:
		t.Fatal("the torrent was not closed")
	}
	assert.NoDirExists(t, torrentDir)
	_, err := ctrl.db.GetTorrent(ih)
	require.ErrorIs(t, err, database.ErrTorrentNotFound)

	// The worker exits on its own, and its closed-torrent read error is not
	// reported as a failed preload.
	require.Eventually(t, func() bool {
		ctrl.preloadsMu.Lock()
		defer ctrl.preloadsMu.Unlock()
		return !slices.Contains(ctrl.preloadWorkers, preload)
	}, 10*time.Second, time.Millisecond, "the cancelled worker did not exit")
	_, snapshotted := ctrl.preloadSnapshots.Load(ih)
	assert.False(t, snapshotted, "the cancelled preload must not report a status")
}

func TestReadyPreloadExpires(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: 10 * time.Millisecond}
	ih := metainfo.Hash{1}
	cleared := make(chan struct{})
	task := &preloadTask{
		infoHash:      ih,
		cancel:        func() {},
		releaseBudget: func() { close(cleared) },
	}
	task.ready.Store(true)
	ctrl.preloads.Store(ih, task)

	ctrl.preloadsMu.Lock()
	ctrl.schedulePreloadExpiryLocked(task)
	ctrl.preloadsMu.Unlock()

	// removePreload deletes the task before releasing it, so wait for the
	// release itself rather than only for the task to leave the registry.
	select {
	case <-cleared:
	case <-time.After(time.Second):
		t.Fatal("preload expiry did not release cache protection")
	}
	_, exists := ctrl.preloads.Load(ih)
	assert.False(t, exists)
}

func TestPreloadStatusSnapshotExpires(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: 10 * time.Millisecond}
	preload := &preloadTask{
		infoHash:    metainfo.Hash{1},
		fileIndex:   3,
		filePath:    "ready.mkv",
		targetBytes: 1024,
	}
	preload.bytesRead.Store(preload.targetBytes)
	preload.ready.Store(true)

	ctrl.preloadsMu.Lock()
	ctrl.snapshotPreloadLocked(preload, api.Superseded)
	ctrl.preloadsMu.Unlock()

	_, exists := ctrl.preloadSnapshots.Load(preload.infoHash)
	require.True(t, exists)
	require.Eventually(t, func() bool {
		_, exists := ctrl.preloadSnapshots.Load(preload.infoHash)
		return !exists
	}, time.Second, time.Millisecond)
}

func TestPreloadExpiryDoesNotRemoveReplacement(t *testing.T) {
	ctrl := &Controller{}
	ih := metainfo.Hash{1}
	var oldCleared atomic.Bool
	oldTask := &preloadTask{
		infoHash:      ih,
		cancel:        func() {},
		releaseBudget: func() { oldCleared.Store(true) },
	}
	newTask := &preloadTask{}
	ctrl.preloads.Store(ih, newTask)

	assert.False(t, ctrl.removePreload(ih, oldTask))
	assert.False(t, oldCleared.Load())
	current, exists := ctrl.preloads.Load(ih)
	require.True(t, exists)
	assert.Same(t, newTask, current)
}
