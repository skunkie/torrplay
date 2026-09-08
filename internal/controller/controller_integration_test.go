//go:build integration

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
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

type localWebseedFixture struct {
	meta      *metainfo.MetaInfo
	payload   []byte
	requested <-chan struct{}
	release   func()
}

func newIntegrationTestController(t *testing.T) *Controller {
	t.Helper()
	runtimeConfig := testControllerRuntimeConfig()
	runtimeConfig.configureClient = func(config *torrent.ClientConfig) {
		config.NoDHT = true
		config.DisablePEX = true
		config.DisableTrackers = true
		config.DisableWebtorrent = true
		config.DisableWebseeds = false
		config.NoDefaultPortForwarding = true
		config.DisableTCP = true
		config.DisableUTP = true
	}
	ctrl, _ := newTestControllerWithRuntimeConfig(t, runtimeConfig)
	ctrl.Start()
	return ctrl
}

func newLocalWebseedFixture(t *testing.T, block bool) localWebseedFixture {
	t.Helper()
	payload := bytes.Repeat([]byte("torrplay-local-webseed\n"), 4096)
	requested := make(chan struct{})
	release := make(chan struct{})
	var requestOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestOnce.Do(func() { close(requested) })
		if block {
			<-release
		}
		http.ServeContent(w, r, "video.mp4", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(server.Close)

	const pieceLength = 16 * 1024
	pieces := make([]byte, 0, ((len(payload)+pieceLength-1)/pieceLength)*sha1.Size)
	for offset := 0; offset < len(payload); offset += pieceLength {
		end := min(offset+pieceLength, len(payload))
		sum := sha1.Sum(payload[offset:end])
		pieces = append(pieces, sum[:]...)
	}
	info := metainfo.Info{
		Name:        "Sintel",
		PieceLength: pieceLength,
		Pieces:      pieces,
		Files: []metainfo.FileInfo{{
			Length: int64(len(payload)),
			Path:   []string{"Sintel.mp4"},
		}},
	}
	infoBytes, err := bencode.Marshal(info)
	require.NoError(t, err)
	meta := &metainfo.MetaInfo{
		InfoBytes: infoBytes,
		UrlList:   metainfo.UrlList{server.URL + "/"},
	}

	var releaseOnce sync.Once
	return localWebseedFixture{
		meta:      meta,
		payload:   payload,
		requested: requested,
		release:   func() { releaseOnce.Do(func() { close(release) }) },
	}
}

func addLocalWebseedTorrent(t *testing.T, ctrl *Controller, fixture localWebseedFixture) metainfo.Hash {
	t.Helper()
	spec := torrent.TorrentSpecFromMetaInfo(fixture.meta)
	_, _, err := ctrl.client.AddTorrentSpec(spec)
	require.NoError(t, err)

	ih := fixture.meta.HashInfoBytes()
	storageType := api.Memory
	title := "Sintel"
	err = ctrl.db.CreateTorrent(&database.Torrent{Torrent: api.Torrent{
		Hash:      ih,
		Magnet:    utils.MagnetURIFromHash(ih),
		Name:      "Sintel",
		Title:     &title,
		Storage:   &storageType,
		TotalSize: int64(len(fixture.payload)),
	}})
	require.NoError(t, err)
	return ih
}

func startBlockedIntegrationStream(t *testing.T, ctrl *Controller, fixture localWebseedFixture, endpoint string) (<-chan error, *torrent.Torrent) {
	t.Helper()
	server := httptest.NewServer(ctrl.router)
	t.Cleanup(server.Close)
	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, server.URL+endpoint, http.NoBody)
		if err != nil {
			done <- err
			return
		}
		req.Header.Set("Range", "bytes=0-")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusPartialContent {
			done <- fmt.Errorf("unexpected stream status: %s", resp.Status)
			return
		}
		_, err = io.Copy(io.Discard, resp.Body)
		done <- err
	}()

	select {
	case <-fixture.requested:
	case <-time.After(10 * time.Second):
		t.Fatal("local webseed was not requested")
	}
	ih := fixture.meta.HashInfoBytes()
	require.Eventually(t, func() bool { return ctrl.hasTorrentReaders(ih) }, time.Second, time.Millisecond)
	to, ok := ctrl.client.Torrent(ih)
	require.True(t, ok)
	return done, to
}

func finishBlockedIntegrationStream(t *testing.T, fixture localWebseedFixture, done <-chan error) {
	t.Helper()
	fixture.release()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish after releasing the local webseed")
	}
}

func TestIntegrationAddTorrentWhileStreaming(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(fixture.meta))
	require.NoError(t, err)
	ih := fixture.meta.HashInfoBytes()
	done, activeTorrent := startBlockedIntegrationStream(t, ctrl, fixture, fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih))
	require.Same(t, to, activeTorrent)

	magnet := utils.MagnetURIFromHash(ih)
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(api.TorrentAdd{Magnet: &magnet}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)
	loadedTorrent, ok := ctrl.client.Torrent(ih)
	require.True(t, ok)
	assert.Same(t, activeTorrent, loadedTorrent)
	select {
	case <-loadedTorrent.Closed():
		t.Fatal("adding an active torrent closed the stream")
	default:
	}

	finishBlockedIntegrationStream(t, fixture, done)
}

func TestIntegrationUpdateTorrentWhileStreaming(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)
	done, _ := startBlockedIntegrationStream(t, ctrl, fixture, fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih))

	newTitle := "updated while streaming"
	rr := testutil.NewRequest().Patch("/api/v1/torrents/"+ih.HexString()).WithJsonBody(api.TorrentUpdate{Title: &newTitle}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)
	stored, err := ctrl.db.GetTorrent(ih)
	require.NoError(t, err)
	require.NotNil(t, stored.Title)
	assert.Equal(t, newTitle, *stored.Title)

	finishBlockedIntegrationStream(t, fixture, done)
}

func TestIntegrationTSTorrentsAddWhileStreaming(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(fixture.meta))
	require.NoError(t, err)
	ih := fixture.meta.HashInfoBytes()
	done, activeTorrent := startBlockedIntegrationStream(t, ctrl, fixture, fmt.Sprintf("/stream/Sintel.mp4?link=%s&play&index=0", ih))
	require.Same(t, to, activeTorrent)

	magnet := utils.MagnetURIFromHash(ih)
	saveToDB := true
	rr := testutil.NewRequest().Post("/torrents").WithJsonBody(api.TSTorrentRequest{
		Action:   "add",
		Link:     &magnet,
		Title:    new("Sintel Test"),
		SaveToDB: &saveToDB,
	}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)
	loadedTorrent, ok := ctrl.client.Torrent(ih)
	require.True(t, ok)
	assert.Same(t, activeTorrent, loadedTorrent)
	select {
	case <-loadedTorrent.Closed():
		t.Fatal("TorrServer add closed the active stream")
	default:
	}
	finishBlockedIntegrationStream(t, fixture, done)
}

func TestIntegrationStreamingFromLocalWebseed(t *testing.T) {
	fixture := newLocalWebseedFixture(t, false)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	for _, endpoint := range []string{
		fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih),
		fmt.Sprintf("/api/v1/stream/%s?index=0", ih),
		fmt.Sprintf("/stream/Sintel.mp4?link=%s&play&index=0", ih),
		fmt.Sprintf("/stream/Sintel.mp4?link=%s&play", ih),
	} {
		req, err := http.NewRequest(http.MethodGet, server.URL+endpoint, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Range", "bytes=0-1023")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, readErr)
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Equal(t, fixture.payload[:1024], body)
	}
}

func TestIntegrationActiveMemoryCacheResponse(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)
	done, to := startBlockedIntegrationStream(t, ctrl, fixture, fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih))

	storageTorrent, err := ctrl.storageClient.OpenTorrent(context.Background(), to.Info(), ih)
	require.NoError(t, err)
	_, err = storageTorrent.Piece(to.Piece(0).Info()).WriteAt(make([]byte, 1024), 0)
	require.NoError(t, err)

	recorder := testutil.NewRequest().Post("/cache").WithJsonBody(api.TSCacheRequest{
		Action: new("get"),
		Hash:   ih.HexString(),
	}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, recorder.Code)

	bodyBytes := recorder.Body.Bytes()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bodyBytes, &raw))
	for _, key := range []string{"Capacity", "Filled", "Hash", "Pieces", "PiecesCount", "PiecesLength", "Readers", "Torrent"} {
		assert.Contains(t, raw, key)
	}

	var response api.TSCacheResponse
	require.NoError(t, json.Unmarshal(bodyBytes, &response))
	assert.Equal(t, ih.HexString(), response.Hash)
	assert.Positive(t, response.Capacity)
	assert.Equal(t, to.NumPieces(), response.PiecesCount)
	assert.NotEmpty(t, response.Readers)
	require.NotNil(t, response.Torrent)

	stats, err := ctrl.storageClient.TorrentStats(ih)
	require.NoError(t, err)
	require.NotEmpty(t, stats.Pieces)
	first := stats.Pieces[0]
	piece, ok := response.Pieces[strconv.Itoa(first.Index)]
	require.True(t, ok)
	assert.Equal(t, first.Index, piece.ID)
	assert.Equal(t, first.SizeBytes, piece.Length)
	assert.LessOrEqual(t, piece.Size, piece.Length)

	finishBlockedIntegrationStream(t, fixture, done)
}

func TestIntegrationDeleteWhileStreamingUsesStateSynchronization(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)

	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	streamDone := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/stream/%s?path=Sintel/Sintel.mp4", server.URL, ih), http.NoBody)
		if err == nil {
			req.Header.Set("Range", "bytes=0-")
			var resp *http.Response
			resp, err = http.DefaultClient.Do(req)
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
		streamDone <- err
	}()

	select {
	case <-fixture.requested:
	case <-time.After(5 * time.Second):
		t.Fatal("local webseed was not requested")
	}
	require.Eventually(t, func() bool { return ctrl.hasTorrentReaders(ih) }, time.Second, time.Millisecond)

	otherHashes := []metainfo.Hash{bunnyHash, cosmosHash}
	for _, otherHash := range otherHashes {
		primeSampleMetadata(t, ctrl, otherHash)
		magnet := samples[otherHash]
		rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(api.TorrentAdd{Magnet: &magnet}).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusCreated, rr.Code)
	}
	otherDeletes := make(chan int, len(otherHashes))
	for _, otherHash := range otherHashes {
		go func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodDelete, "/api/v1/torrents/"+otherHash.HexString(), http.NoBody)
			ctrl.router.ServeHTTP(recorder, request)
			otherDeletes <- recorder.Code
		}()
	}
	for range otherHashes {
		assert.Equal(t, http.StatusNoContent, <-otherDeletes)
	}
	assert.True(t, ctrl.hasTorrentReaders(ih), "deleting unrelated torrents interrupted the active stream")

	deleteDone := make(chan int, 1)
	go func() {
		rr := testutil.NewRequest().Delete("/api/v1/torrents/"+ih.HexString()).GoWithHTTPHandler(t, ctrl.router).Recorder
		deleteDone <- rr.Code
	}()
	fixture.release()

	select {
	case code := <-deleteDone:
		assert.Equal(t, http.StatusNoContent, code)
	case <-time.After(5 * time.Second):
		t.Fatal("delete deadlocked with active stream")
	}
	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not exit after torrent deletion")
	}
}

func TestIntegrationPreloadFromLocalWebseed(t *testing.T) {
	fixture := newLocalWebseedFixture(t, false)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)

	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	tsPreloadResponse, err := http.Get(fmt.Sprintf("%s/stream/Sintel.mp4?link=%s&preload&stat&index=0", server.URL, ih))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, tsPreloadResponse.StatusCode)
	var tsStatus api.TSTorrentResponse
	require.NoError(t, json.NewDecoder(tsPreloadResponse.Body).Decode(&tsStatus))
	require.NoError(t, tsPreloadResponse.Body.Close())
	assert.Equal(t, tsStatPreload, tsStatus.Stat)
	assert.Equal(t, "Torrent preload", tsStatus.StatString)
	assert.Positive(t, tsStatus.PreloadSize)

	invalidPlayback := httptest.NewRecorder()
	ctrl.streamFile(invalidPlayback, httptest.NewRequest(http.MethodGet, "/stream/invalid", http.NoBody), ih, 99)
	assert.Equal(t, http.StatusBadRequest, invalidPlayback.Code)
	_, stillPreloading := ctrl.preloads.Load(ih)
	assert.True(t, stillPreloading, "invalid playback should not cancel the active preload")
	ctrl.cancelPreload(ih)

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih), bytes.NewBufferString(`{"file_index":0}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var readyState api.PreloadResponse
	require.Eventually(t, func() bool {
		getResp, getErr := http.Get(fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, ih))
		if getErr != nil {
			return false
		}
		defer getResp.Body.Close()
		var state api.PreloadResponse
		if json.NewDecoder(getResp.Body).Decode(&state) != nil || state.Status != api.Ready {
			return false
		}
		readyState = state
		return true
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, float32(1), readyState.Progress)
	assert.Equal(t, int64(len(fixture.payload)), readyState.TargetBytes)
	assert.Equal(t, readyState.TargetBytes, readyState.CompletedBytes)

	done := make(chan struct{})
	close(done)
	activePreload := &preloadTask{
		cancel:      func() {},
		done:        done,
		targetBytes: int64(len(fixture.payload)),
		fileIndex:   0,
		filePath:    "Sintel/Sintel.mp4",
	}
	ctrl.preloads.Store(ih, activePreload)
	streamRequest, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/stream/%s?index=0", server.URL, ih), http.NoBody)
	require.NoError(t, err)
	streamRequest.Header.Set("Range", "bytes=0-1023")
	streamResponse, err := http.DefaultClient.Do(streamRequest)
	require.NoError(t, err)
	require.NoError(t, streamResponse.Body.Close())
	assert.Equal(t, http.StatusPartialContent, streamResponse.StatusCode)
	_, stillPreloading = ctrl.preloads.Load(ih)
	assert.False(t, stillPreloading, "playback should retire the active preload")
}

func TestIntegrationControllerLifecycle(t *testing.T) {
	ctrl := newIntegrationTestController(t)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	settingsURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/settings", ctrl.port)

	require.Eventually(t, func() bool {
		resp, err := client.Get(settingsURL)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 10*time.Millisecond, "controller listener did not become ready")

	ctrl.Shutdown()
	require.Eventually(t, func() bool {
		resp, err := client.Get(settingsURL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		return err != nil
	}, time.Second, 10*time.Millisecond, "controller listener remained reachable after shutdown")
}
