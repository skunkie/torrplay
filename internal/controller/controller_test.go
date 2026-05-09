// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/metrics"
	"github.com/torrplay/torrplay/internal/utils"
)

// Taken from https://webtorrent.io/free-torrents.
var samples = map[metainfo.Hash]string{
	metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"): "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fbig-buck-bunny.torrent",
	metainfo.NewHashFromHex("c9e15763f722f23e98a29decdfae341b98d53056"): "magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056&dn=Cosmos+Laundromat&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fcosmos-laundromat.torrent",
	metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10"): "magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fsintel.torrent",
}

type testControllerOpt func(*Controller)

func newTestController(t *testing.T, opts ...testControllerOpt) (*Controller, func()) {
	t.Helper()

	dbPath := tempfile()
	dbClient, err := database.NewBBoltDB(dbPath)
	require.NoError(t, err)

	postersDBPath := tempfile()
	imagesSvc, err := images.NewBBoltDBService(postersDBPath)
	require.NoError(t, err)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	metricsSvc := metrics.New()
	ctrl, err := NewController(".", "127.0.0.1", port, dbClient, imagesSvc, metricsSvc)
	require.NoError(t, err)

	// Disable auth.
	ctrl.settings.Auth.Enabled = utils.Ptr(false)
	err = ctrl.db.UpdateSettings(ctrl.settings)
	require.NoError(t, err)

	for _, opt := range opts {
		opt(ctrl)
	}

	ctrl.SetupRouter()
	ctrl.Start()

	cleanup := func() {
		ctrl.Shutdown()
		dbClient.Close()
		imagesSvc.Close()
		os.Remove(dbPath)
		os.Remove(postersDBPath)
	}

	return ctrl, cleanup
}

func doGet(t *testing.T, router http.Handler, url string) *httptest.ResponseRecorder {
	response := testutil.NewRequest().Get(url).WithAcceptJson().GoWithHTTPHandler(t, router)
	return response.Recorder
}

func addAllSampleTorrents(t *testing.T, router http.Handler) {
	t.Helper()
	for ih, magnet := range samples {
		req := api.TorrentAdd{
			Magnet: &magnet,
		}
		rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, router).Recorder
		require.Equal(t, http.StatusCreated, rr.Code, "failed to add sample torrent %s", ih.HexString())
	}
}

func TestAddTorrentFromFile(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelTorrentPath := "testdata/sintel.torrent"
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	file, err := os.Open(sintelTorrentPath)
	require.NoError(t, err)
	defer file.Close()

	part, err := writer.CreateFormFile("file", "sintel.torrent")
	require.NoError(t, err)

	_, err = io.Copy(part, file)
	require.NoError(t, err)
	writer.Close()

	req, err := http.NewRequest("POST", "/api/v1/torrents", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code)

	var result api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10"), result.Hash)
}

func TestAddInvalidTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	magnet := "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"
	body, err := json.Marshal(api.TorrentAdd{Magnet: &magnet})
	require.NoError(t, err)

	req, err := http.NewRequest("POST", "/api/v1/torrents", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAddNonExistentTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	gotInfoTimeout = 2 * time.Second
	defer func() { gotInfoTimeout = 30 * time.Second }()

	magnet := "magnet:?xt=urn:btih:0000000000000000000000000000000000000001"
	body, err := json.Marshal(api.TorrentAdd{Magnet: &magnet})
	require.NoError(t, err)

	req, err := http.NewRequest("POST", "/api/v1/torrents", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusGatewayTimeout, rr.Code)
	assert.Contains(t, rr.Body.String(), gotInfoTimeoutMsg)
}

func TestAddDuplicateTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	rr = testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	assert.Equal(t, http.StatusConflict, rr.Code)
}

func TestGetTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)

	var result api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.NotNil(t, result.CreatedAt)
	assert.Equal(t, ih, result.Hash)
	assert.NotNil(t, result.Files)
	assert.NotNil(t, result.Magnet)
	assert.NotNil(t, result.Name)
	assert.NotNil(t, result.PieceCount)
	assert.NotNil(t, result.TotalSize)
}

func TestStreamFileFromTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	streamURL := fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih)
	rr := testutil.NewRequest().Get(streamURL).WithHeader("Range", "bytes=0-1023").GoWithHTTPHandler(t, ctrl.router).Recorder

	require.Equal(t, http.StatusPartialContent, rr.Code)
	assert.NotEmpty(t, rr.Body.String())
}

func TestStreamFileFromTorrentByIndex(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	streamURL := fmt.Sprintf("/api/v1/stream/%s?index=0", ih)
	rr := testutil.NewRequest().Get(streamURL).WithHeader("Range", "bytes=0-1023").GoWithHTTPHandler(t, ctrl.router).Recorder

	require.Equal(t, http.StatusPartialContent, rr.Code)
	assert.NotEmpty(t, rr.Body.String())
}

func TestStreamFileWithBothIdentifiers(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	streamURL := fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4&index=0", ih)
	rr := testutil.NewRequest().Get(streamURL).GoWithHTTPHandler(t, ctrl.router).Recorder

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestStreamFileWithNoIdentifier(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	streamURL := fmt.Sprintf("/api/v1/stream/%s", ih)
	rr := testutil.NewRequest().Get(streamURL).GoWithHTTPHandler(t, ctrl.router).Recorder

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetPlaylist(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	rr := doGet(t, ctrl.router, "/api/v1/playlist")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/x-mpegURL", rr.Header().Get("Content-Type"))
	assert.True(t, strings.HasPrefix(rr.Body.String(), "#EXTM3U"))
}

func TestGetTorrentStatistics(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	var result api.TorrentStats
	rr := doGet(t, ctrl.router, fmt.Sprintf("/api/stats/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)

	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))

	assert.NotNil(t, result.Pieces)
	assert.NotNil(t, result.MemoryStats)
	assert.Greater(t, result.MemoryStats.MaxMemory, int64(0))
	assert.GreaterOrEqual(t, result.MemoryStats.UsedMemory, int64(0))
	assert.GreaterOrEqual(t, result.TotalPeers, 0)
	assert.GreaterOrEqual(t, result.ActivePeers, 0)
	assert.GreaterOrEqual(t, result.BytesRead, int64(0))
	assert.GreaterOrEqual(t, result.BytesWritten, int64(0))
	assert.GreaterOrEqual(t, result.PiecesComplete, 0)
}

func TestQBittorrentAddTorrentFromURL(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormField("urls")
	require.NoError(t, err)
	_, err = part.Write([]byte(samples[metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")]))
	require.NoError(t, err)
	writer.Close()

	req, err := http.NewRequest("POST", "/api/v2/torrents/add", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Ok.", rr.Body.String())
}

func TestQBittorrentAddTorrentFromFile(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	file, err := os.Open("testdata/sintel.torrent")
	require.NoError(t, err)
	defer file.Close()

	part, err := writer.CreateFormFile("torrents", "sintel.torrent")
	require.NoError(t, err)

	_, err = io.Copy(part, file)
	require.NoError(t, err)
	writer.Close()

	req, err := http.NewRequest("POST", "/api/v2/torrents/add", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Ok.", rr.Body.String())
}

func TestTorrentMetadataFetchTimesOut(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	gotInfoTimeout = 2 * time.Second
	defer func() { gotInfoTimeout = 30 * time.Second }()

	rr := doGet(t, ctrl.router, "/api/v1/torrents/0000000000000000000000000000000000000001")
	require.Equal(t, http.StatusGatewayTimeout, rr.Code)

	var apiError api.Error
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&apiError))
	assert.Equal(t, http.StatusGatewayTimeout, apiError.Code)
}

func TestInvalidHash(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	rr := doGet(t, ctrl.router, "/api/v1/torrents/invalid")
	require.Equal(t, http.StatusBadRequest, rr.Code)

	var apiError api.Error
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&apiError))
	assert.Equal(t, http.StatusBadRequest, apiError.Code)
}

func TestTSStreamWithFilenameInPath(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	streamURL := fmt.Sprintf("/stream/Sintel.mp4?link=%s&play&index=6", ih.HexString())
	rr := testutil.NewRequest().Get(streamURL).WithHeader("Range", "bytes=0-1023").GoWithHTTPHandler(t, ctrl.router).Recorder
	assert.Equal(t, http.StatusPartialContent, rr.Code)
}

func TestDeleteTorrentWhileStreamingConcurrently(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	streamErrChan := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		streamURL := fmt.Sprintf("%s/api/v1/stream/%s?path=Sintel/Sintel.mp4", server.URL, ih)
		req, _ := http.NewRequest("GET", streamURL, nil)
		req.Header.Set("Range", "bytes=0-")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			streamErrChan <- err
			return
		}
		defer resp.Body.Close()

		_, readErr := io.Copy(io.Discard, resp.Body)
		streamErrChan <- readErr
	}()

	time.Sleep(5 * time.Second)

	deleteURL := fmt.Sprintf("%s/api/v1/torrents/%s", server.URL, ih)
	req, _ := http.NewRequest("DELETE", deleteURL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	wg.Wait()

	err = <-streamErrChan
	if err != nil {
		require.True(t, err == io.EOF || err == io.ErrUnexpectedEOF, "unexpected error: %v", err)
	}
}

func TestStreamAndConcurrentlyDelete(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	ihSintel := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	ihBunny := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	ihCosmos := metainfo.NewHashFromHex("c9e15763f722f23e98a29decdfae341b98d53056")

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	var wg sync.WaitGroup
	wg.Add(3)

	streamErrChan := make(chan error, 1)

	go func() {
		defer wg.Done()
		streamURL := fmt.Sprintf("%s/api/v1/stream/%s?path=Sintel/Sintel.mp4", server.URL, ihSintel)
		req, _ := http.NewRequest("GET", streamURL, nil)
		req.Header.Set("Range", "bytes=0-")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			streamErrChan <- err
			return
		}
		defer resp.Body.Close()

		_, readErr := io.CopyN(io.Discard, resp.Body, 1024)
		streamErrChan <- readErr
	}()

	go func() {
		defer wg.Done()
		deleteURL := fmt.Sprintf("%s/api/v1/torrents/%s", server.URL, ihBunny)
		req, _ := http.NewRequest("DELETE", deleteURL, nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	}()

	go func() {
		defer wg.Done()
		deleteURL := fmt.Sprintf("%s/api/v1/torrents/%s", server.URL, ihCosmos)
		req, _ := http.NewRequest("DELETE", deleteURL, nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	}()

	wg.Wait()

	err := <-streamErrChan
	assert.NoError(t, err)

	listURL := fmt.Sprintf("%s/api/v1/torrents?hashes=%s", server.URL, ihSintel.HexString())
	resp, err := http.Get(listURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listSintel api.ListTorrents
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listSintel))
	assert.Equal(t, 1, len(listSintel.Torrents))
	resp.Body.Close()

	listURL = fmt.Sprintf("%s/api/v1/torrents?hashes=%s,%s", server.URL, ihBunny.HexString(), ihCosmos.HexString())
	resp, err = http.Get(listURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var torrentsList api.ListTorrents
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&torrentsList))
	assert.Equal(t, 0, len(torrentsList.Torrents))
	resp.Body.Close()
}

func TestDeleteTorrents(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl.router)
	rr := testutil.NewRequest().Delete("/api/v1/torrents/0000000000000000000000000000000000000000").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNotFound, rr.Code)

	var apiError api.Error
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&apiError), "error unmarshaling error response")
	assert.Equal(t, http.StatusNotFound, apiError.Code)

	var result api.ListTorrents
	rr = doGet(t, ctrl.router, "/api/v1/torrents")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	require.Greater(t, len(result.Torrents), 0)

	for _, torrent := range result.Torrents {
		rr = testutil.NewRequest().Delete("/api/v1/torrents/"+torrent.Hash.HexString()).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusNoContent, rr.Code)
	}

	rr = doGet(t, ctrl.router, "/api/v1/torrents")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, 0, len(result.Torrents))
	assert.Equal(t, 0, result.Total)
}

func TestUpdateTorrentPosterRepeatedly(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet, Poster: &posterURL}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var createdTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&createdTorrent))

	// Wait for the poster to be fetched
	require.Eventually(t, func() bool {
		rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
		if rr.Code != http.StatusOK {
			return false
		}
		var torrent api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&torrent); err != nil {
			return false
		}
		return torrent.Poster != nil
	}, 2*time.Second, 100*time.Millisecond, "poster should be fetched")

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&createdTorrent))
	require.NotNil(t, createdTorrent.Poster)

	for i := 0; i < 3; i++ {
		updateReq := api.TorrentUpdate{Poster: &posterURL}
		rr := testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ih)).WithJsonBody(updateReq).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusNoContent, rr.Code)
	}

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	var updatedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))
	require.NotNil(t, updatedTorrent.Poster)
	assert.Equal(t, *updatedTorrent.Poster, *createdTorrent.Poster)
}

func TestUpdateTorrentPosterRepeatedlyWithDifferentPosters(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	posterA := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	posterB := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	magnet := samples[ih]

	req := api.TorrentAdd{Magnet: &magnet, Poster: &posterA}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var torrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrent))

	// Wait for the poster to be fetched
	require.Eventually(t, func() bool {
		rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
		if rr.Code != http.StatusOK {
			return false
		}
		var t api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&t); err != nil {
			return false
		}
		return t.Poster != nil
	}, 2*time.Second, 100*time.Millisecond, "poster should be fetched")

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrent))
	require.NotNil(t, torrent.Poster)
	posterA_URL := *torrent.Poster

	updateReqB := api.TorrentUpdate{Poster: &posterB}
	rr = testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ih)).WithJsonBody(updateReqB).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Wait for the poster to be updated to B
	require.Eventually(t, func() bool {
		rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
		if rr.Code != http.StatusOK {
			return false
		}
		var t api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&t); err != nil {
			return false
		}
		return t.Poster != nil && *t.Poster != posterA_URL
	}, 2*time.Second, 100*time.Millisecond, "poster should be updated to B")

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrent))
	assert.NotEqual(t, posterA_URL, *torrent.Poster)
	posterB_URL := *torrent.Poster

	updateReqA := api.TorrentUpdate{Poster: &posterA}
	rr = testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ih)).WithJsonBody(updateReqA).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Wait for the poster to be updated to A
	require.Eventually(t, func() bool {
		rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
		if rr.Code != http.StatusOK {
			return false
		}
		var t api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&t); err != nil {
			return false
		}
		return t.Poster != nil && *t.Poster != posterB_URL
	}, 2*time.Second, 100*time.Millisecond, "poster should be updated to A")

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrent))
	assert.Equal(t, posterA_URL, *torrent.Poster)
}

func TestUpdateTorrentWithSharedPoster(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

	ihA := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	magnetA := samples[ihA]
	reqA := api.TorrentAdd{Magnet: &magnetA, Poster: &posterURL}
	rrA := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(reqA).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rrA.Code)
	var torrentA api.Torrent
	require.NoError(t, json.NewDecoder(rrA.Body).Decode(&torrentA))

	ihB := metainfo.NewHashFromHex("c9e15763f722f23e98a29decdfae341b98d53056")
	magnetB := samples[ihB]
	reqB := api.TorrentAdd{Magnet: &magnetB, Poster: &posterURL}
	rrB := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(reqB).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rrB.Code)
	var torrentB api.Torrent
	require.NoError(t, json.NewDecoder(rrB.Body).Decode(&torrentB))

	// Wait for posters to be fetched
	require.Eventually(t, func() bool {
		rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihA))
		if rr.Code != http.StatusOK {
			return false
		}
		var tA api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&tA); err != nil {
			return false
		}

		rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihB))
		if rr.Code != http.StatusOK {
			return false
		}
		var tB api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&tB); err != nil {
			return false
		}

		return tA.Poster != nil && tB.Poster != nil && *tA.Poster == *tB.Poster
	}, 2*time.Second, 100*time.Millisecond, "posters should be fetched and equal")

	rrGetA := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihA))
	require.NoError(t, json.NewDecoder(rrGetA.Body).Decode(&torrentA))
	rrGetB := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihB))
	require.NoError(t, json.NewDecoder(rrGetB.Body).Decode(&torrentB))
	assert.Equal(t, *torrentA.Poster, *torrentB.Poster)

	updateReq := api.TorrentUpdate{Poster: new(string)}
	rrUpdate := testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ihA)).WithJsonBody(updateReq).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rrUpdate.Code)

	// Wait for poster to be removed from torrent A
	require.Eventually(t, func() bool {
		rr := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihA))
		if rr.Code != http.StatusOK {
			return false
		}
		var tA api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&tA); err != nil {
			return false
		}
		return tA.Poster == nil
	}, 2*time.Second, 100*time.Millisecond, "poster should be removed from torrent A")

	rrGetA = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihA))
	var updatedTorrentA api.Torrent
	require.NoError(t, json.NewDecoder(rrGetA.Body).Decode(&updatedTorrentA))
	assert.Nil(t, updatedTorrentA.Poster)

	rrGetB = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ihB))
	var updatedTorrentB api.Torrent
	require.NoError(t, json.NewDecoder(rrGetB.Body).Decode(&updatedTorrentB))
	require.NotNil(t, updatedTorrentB.Poster)
	assert.Equal(t, *updatedTorrentB.Poster, *torrentB.Poster)

	rrImage := testutil.NewRequest().Get(*updatedTorrentB.Poster).GoWithHTTPHandler(t, ctrl.router).Recorder
	assert.Equal(t, http.StatusOK, rrImage.Code)
}

func TestUpdateTorrentFileViewedStatus(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var torrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrent))

	filepath := torrent.Files[0].Path
	updateReq := api.TorrentUpdate{
		Files: &[]api.TorrentFileUpdate{
			{
				Path:   filepath,
				Viewed: true,
			},
		},
	}

	dummyReq := httptest.NewRequest(http.MethodPatch, "/", nil)
	err := ctrl.updateTorrent(dummyReq, ih, updateReq)
	require.NoError(t, err)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	var updatedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))

	var viewedFile api.TorrentFile
	for _, f := range updatedTorrent.Files {
		if f.Path == filepath {
			viewedFile = f
			break
		}
	}
	require.NotNil(t, viewedFile, "file not found in torrent")
	assert.NotNil(t, viewedFile.ViewedAt)
	assert.NotNil(t, updatedTorrent.UpdatedAt)

	// Update again to check that the viewed timestamp is not updated too frequently.
	firstUpdate := *viewedFile.ViewedAt
	updateReq.Files = &[]api.TorrentFileUpdate{
		{
			Path:   filepath,
			Viewed: true,
		},
	}

	err = ctrl.updateTorrent(dummyReq, ih, updateReq)
	require.NoError(t, err)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))

	for _, f := range updatedTorrent.Files {
		if f.Path == filepath {
			viewedFile = f
			break
		}
	}
	require.NotNil(t, viewedFile, "file not found in torrent")
	assert.Equal(t, firstUpdate, *viewedFile.ViewedAt)

	updateReq.Files = &[]api.TorrentFileUpdate{
		{
			Path:   filepath,
			Viewed: false,
		},
	}

	err = ctrl.updateTorrent(dummyReq, ih, updateReq)
	require.NoError(t, err)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))

	for _, f := range updatedTorrent.Files {
		if f.Path == filepath {
			viewedFile = f
			break
		}
	}
	require.NotNil(t, viewedFile, "file not found in torrent")
	assert.Nil(t, viewedFile.ViewedAt)
	assert.NotNil(t, updatedTorrent.UpdatedAt)
}

func TestUpdateSettings(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	newSettings := api.Settings{
		FriendlyName:   utils.Ptr("My New TorrPlay"),
		HTTPServerPort: utils.Ptr(9090),
	}

	rr := testutil.NewRequest().Patch("/api/v1/settings").WithJsonBody(newSettings).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Wait for the router to be rebuilt
	require.Eventually(t, func() bool {
		return ctrl.router != nil
	}, 2*time.Second, 100*time.Millisecond, "router should be rebuilt")

	rr = testutil.NewRequest().Get("/api/v1/settings").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var updatedSettings api.Settings
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &updatedSettings))
	assert.Equal(t, *newSettings.FriendlyName, *updatedSettings.FriendlyName)
	assert.Equal(t, *newSettings.HTTPServerPort, *updatedSettings.HTTPServerPort)
}

func TestTSTorrentUploadWithPoster(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	sintelTorrentPath := "testdata/sintel.torrent"
	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	file, err := os.Open(sintelTorrentPath)
	require.NoError(t, err)
	defer file.Close()

	part, err := writer.CreateFormFile("file", "sintel.torrent")
	require.NoError(t, err)

	_, err = io.Copy(part, file)
	require.NoError(t, err)

	err = writer.WriteField("poster", posterURL)
	require.NoError(t, err)

	writer.Close()

	req, err := http.NewRequest("POST", "/torrent/upload", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var result map[string]interface{}
	err = json.NewDecoder(rr.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, ih.HexString(), result["hash"])

	// Wait for the poster to be fetched asynchronously
	require.Eventually(t, func() bool {
		getRR := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih.HexString()))
		if getRR.Code != http.StatusOK {
			return false
		}
		var updatedTorrent api.Torrent
		if json.NewDecoder(getRR.Body).Decode(&updatedTorrent) != nil {
			return false
		}
		return updatedTorrent.Poster != nil && *updatedTorrent.Poster != ""
	}, 5*time.Second, 200*time.Millisecond, "poster should be fetched and not be empty")

	// Final check to ensure the poster is still there
	getRR := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih.HexString()))
	require.Equal(t, http.StatusOK, getRR.Code)

	var finalTorrent api.Torrent
	err = json.NewDecoder(getRR.Body).Decode(&finalTorrent)
	require.NoError(t, err)
	require.NotNil(t, finalTorrent.Poster)
	assert.Contains(t, *finalTorrent.Poster, "/posters/")
}

func tempfile() string {
	f, err := os.CreateTemp("", "bolt-")
	if err != nil {
		panic(err)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
	if err := os.Remove(f.Name()); err != nil {
		panic(err)
	}
	return f.Name()
}

func TestUpdateTorrent_Deadlock(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		streamURL := fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih)
		rr := testutil.NewRequest().Get(streamURL).WithHeader("Range", "bytes=0-1023").GoWithHTTPHandler(t, ctrl.router).Recorder
		assert.Equal(t, http.StatusPartialContent, rr.Code)
	}()

	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond) // Give the stream time to start.
		updateReq := api.TorrentUpdate{Title: utils.Ptr("new title")}
		rr := testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ih)).WithJsonBody(updateReq).GoWithHTTPHandler(t, ctrl.router).Recorder
		assert.Equal(t, http.StatusNoContent, rr.Code)
	}()

	wg.Wait()
}

func TestUpdateTorrentStorage(t *testing.T) {
	ctrl, cleanup := newTestController(t, func(c *Controller) {
		c.settings.FileStoragePath = utils.Ptr(t.TempDir())
	})
	defer cleanup()

	ih := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet, Storage: utils.Ptr(api.File)}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var createdTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&createdTorrent))
	require.NotNil(t, createdTorrent.Storage)
	assert.Equal(t, api.File, *createdTorrent.Storage)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)
	var fetchedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&fetchedTorrent))
	require.NotNil(t, fetchedTorrent.Storage)
	assert.Equal(t, api.File, *fetchedTorrent.Storage)

	updateReq := api.TorrentUpdate{
		Storage: utils.Ptr(api.Memory),
	}
	rr = testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ih)).WithJsonBody(updateReq).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)

	var updatedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))
	assert.Equal(t, api.Memory, *updatedTorrent.Storage)
}
