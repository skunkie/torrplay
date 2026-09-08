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
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/go-chi/chi/v5"
	"github.com/oapi-codegen/testutil"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/httpclient"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/metrics"
	tputil "github.com/torrplay/torrplay/internal/testutil"
	"github.com/torrplay/torrplay/internal/utils"
	"github.com/torrplay/torrplay/pkg/stream"
)

func TestMain(m *testing.M) {
	tputil.VerifyTestMain(m)
}

var (
	sintelHash = metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	bunnyMeta  = newMetadataFixture("Big Buck Bunny")
	bunnyHash  = bunnyMeta.HashInfoBytes()
	cosmosMeta = newMetadataFixture("Cosmos Laundromat")
	cosmosHash = cosmosMeta.HashInfoBytes()
	samples    = map[metainfo.Hash]string{
		sintelHash: utils.MagnetURIFromHash(sintelHash),
		bunnyHash:  utils.MagnetURIFromHash(bunnyHash),
		cosmosHash: utils.MagnetURIFromHash(cosmosHash),
	}
)

const sintelTorrentFile = "testdata/sintel.torrent"

func newMetadataFixture(name string) *metainfo.MetaInfo {
	info := metainfo.Info{
		Name:        name,
		PieceLength: 64,
		Pieces:      make([]byte, 20),
		Files: []metainfo.FileInfo{{
			Length: 64,
			Path:   []string{name + ".mp4"},
		}},
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	return &metainfo.MetaInfo{InfoBytes: infoBytes}
}

func sampleMetaInfo(t *testing.T, ih metainfo.Hash) *metainfo.MetaInfo {
	t.Helper()
	switch ih {
	case bunnyHash:
		return bunnyMeta
	case cosmosHash:
		return cosmosMeta
	case sintelHash:
		file, err := os.Open(sintelTorrentFile)
		require.NoError(t, err)
		defer file.Close()
		meta, err := metainfo.Load(file)
		require.NoError(t, err)
		return meta
	default:
		t.Fatalf("unknown sample torrent %s", ih.HexString())
		return nil
	}
}

func primeSampleMetadata(t *testing.T, ctrl *Controller, ih metainfo.Hash) {
	t.Helper()
	_, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(sampleMetaInfo(t, ih)))
	require.NoError(t, err)
}

type testControllerOpt func(*Controller)

func testControllerRuntimeConfig() controllerRuntimeConfig {
	return controllerRuntimeConfig{
		fetchTrackers: func(context.Context, *httpclient.Client) ([][]string, error) {
			return nil, nil
		},
		configureClient: func(config *torrent.ClientConfig) {
			config.NoDHT = true
			config.DisablePEX = true
			config.DisableTrackers = true
			config.DisableWebtorrent = true
			config.DisableWebseeds = true
			config.NoDefaultPortForwarding = true
			config.DisableTCP = true
			config.DisableUTP = true
		},
		gotInfoTimeout:   100 * time.Millisecond,
		clientCloseDelay: 0,
	}
}

func newTestController(t *testing.T, opts ...testControllerOpt) (*Controller, func()) {
	t.Helper()
	return newTestControllerWithRuntimeConfig(t, testControllerRuntimeConfig(), opts...)
}

func newTestControllerWithRuntimeConfig(t *testing.T, runtimeConfig controllerRuntimeConfig, opts ...testControllerOpt) (*Controller, func()) {
	t.Helper()

	dbPath := tempfile()
	dbClient, err := database.NewBBoltDB(dbPath)
	require.NoError(t, err)

	postersDBPath := tempfile()
	imagesSvc, err := images.NewBBoltDBService(postersDBPath)
	require.NoError(t, err)

	metricsSvc := metrics.New()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	ctrl, err := newController(".", "127.0.0.1", port, dbClient, imagesSvc, metricsSvc, runtimeConfig)
	require.NoError(t, err)

	// Disable auth.
	ctrl.settings.Auth.Enabled = new(false)
	err = ctrl.db.UpdateSettings(database.FromAPISettings(ctrl.settings))
	require.NoError(t, err)

	for _, opt := range opts {
		opt(ctrl)
	}

	ctrl.SetupRouter()

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			ctrl.Shutdown()
			dbClient.Close()
			imagesSvc.Close()
			os.Remove(dbPath)
			os.Remove(postersDBPath)
		})
	}
	t.Cleanup(cleanup)

	return ctrl, cleanup
}

func createMultipartForm(t *testing.T, fields map[string]string, fileFieldName ...string) (*bytes.Buffer, *multipart.Writer) {
	t.Helper()

	fieldName := "file"
	if len(fileFieldName) > 0 {
		fieldName = fileFieldName[0]
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	file, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	defer file.Close()

	part, err := writer.CreateFormFile(fieldName, filepath.Base(sintelTorrentFile))
	require.NoError(t, err)

	_, err = io.Copy(part, file)
	require.NoError(t, err)

	for key, val := range fields {
		err = writer.WriteField(key, val)
		require.NoError(t, err)
	}

	writer.Close()
	return &body, writer
}

func doGet(t *testing.T, router http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	response := testutil.NewRequest().Get(url).WithAcceptJson().GoWithHTTPHandler(t, router)
	return response.Recorder
}

func TestDemoStaticAssetRoute(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	found := false
	err := chi.Walk(ctrl.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && route == "/demo/*" {
			found = true
		}
		return nil
	})
	require.NoError(t, err)
	assert.True(t, found, "nested demo assets must be served by the static handler")
}

func addAllSampleTorrents(t *testing.T, ctrl *Controller) {
	t.Helper()
	for ih, magnet := range samples {
		primeSampleMetadata(t, ctrl, ih)
		req := api.TorrentAdd{
			Magnet: &magnet,
		}
		rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusCreated, rr.Code, "failed to add sample torrent %s", ih.HexString())
	}
	for ih := range samples {
		primeSampleMetadata(t, ctrl, ih)
	}
}

func TestAddTorrentFromFile(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	body, writer := createMultipartForm(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code)

	var result api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Equal(t, metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10"), result.Hash)

	_, ok := ctrl.client.Torrent(result.Hash)
	assert.False(t, ok, "torrent should be dropped from client when not streaming")
}

func TestAddInvalidTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	magnet := "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"
	body, err := json.Marshal(api.TorrentAdd{Magnet: &magnet})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAddNonExistentTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ctrl.runtimeConfig.gotInfoTimeout = 10 * time.Millisecond

	magnet := "magnet:?xt=urn:btih:0000000000000000000000000000000000000001"
	body, err := json.Marshal(api.TorrentAdd{Magnet: &magnet})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusGatewayTimeout, rr.Code)
	assert.Contains(t, rr.Body.String(), gotInfoTimeoutMsg)
}

func TestAddDuplicateTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := bunnyHash
	primeSampleMetadata(t, ctrl, ih)
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	primeSampleMetadata(t, ctrl, ih)
	rr = testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	assert.Equal(t, http.StatusConflict, rr.Code)
}

func TestGetTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
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
	assert.NotNil(t, result.Active)
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

	addAllSampleTorrents(t, ctrl)

	t.Run("master playlist", func(t *testing.T) {
		rr := doGet(t, ctrl.router, "/api/v1/playlist")

		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/x-mpegURL", rr.Header().Get("Content-Type"))
		playlist := rr.Body.String()
		assert.True(t, strings.HasPrefix(playlist, "#EXTM3U"))
		assert.Contains(t, playlist, "#EXTINF:-1,Big Buck Bunny")
		assert.Contains(t, playlist, "/api/v1/playlist?name=Big+Buck+Bunny.m3u")
		assert.Contains(t, playlist, "#EXTINF:-1,Cosmos Laundromat")
		assert.Contains(t, playlist, "/api/v1/playlist?name=Cosmos+Laundromat.m3u")
		assert.Contains(t, playlist, "#EXTINF:-1,Sintel")
		assert.Contains(t, playlist, "/api/v1/playlist?name=Sintel.m3u")
	})

	t.Run("torrent specific playlist", func(t *testing.T) {
		rr := doGet(t, ctrl.router, "/api/v1/playlist?name=Sintel.m3u&token=test-token")

		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/x-mpegURL", rr.Header().Get("Content-Type"))

		playlist := rr.Body.String()
		assert.True(t, strings.HasPrefix(playlist, "#EXTM3U"))
		assert.Contains(t, playlist, "#EXTINF:-1,Sintel.mp4")
		lines := strings.Split(playlist, "\n")
		var streamURL string
		for i, line := range lines {
			if strings.Contains(line, "Sintel.mp4") && i+1 < len(lines) {
				streamURL = lines[i+1]
				break
			}
		}
		require.NotEmpty(t, streamURL, "stream URL not found in playlist")
		ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
		expectedURLPart := fmt.Sprintf("/api/v1/stream/%s?path=", ih.HexString())
		assert.Contains(t, streamURL, expectedURLPart)
		assert.Contains(t, streamURL, "&token=test-token")
	})
}

func TestGetTorrentStatistics(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	var result api.TorrentStats
	rr := doGet(t, ctrl.router, fmt.Sprintf("/api/stats/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)

	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))

	assert.NotNil(t, result.Pieces)
	assert.NotNil(t, result.MemoryStats)
	assert.Positive(t, result.MemoryStats.MaxMemory)
	assert.GreaterOrEqual(t, result.MemoryStats.UsedMemory, int64(0))
	assert.GreaterOrEqual(t, result.TotalPeers, 0)
	assert.GreaterOrEqual(t, result.ActivePeers, 0)
	assert.GreaterOrEqual(t, result.BytesRead, int64(0))
	assert.GreaterOrEqual(t, result.BytesWritten, int64(0))
	assert.GreaterOrEqual(t, result.WrittenBytes, int64(0))
	assert.GreaterOrEqual(t, result.PiecesComplete, 0)
}

func TestQBittorrentAddTorrentFromURL(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	primeSampleMetadata(t, ctrl, sintelHash)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormField("urls")
	require.NoError(t, err)
	_, err = part.Write([]byte(samples[metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")]))
	require.NoError(t, err)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/torrents/add", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Ok.", rr.Body.String())
}

func TestQBittorrentAddTorrentFromFile(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	primeSampleMetadata(t, ctrl, sintelHash)

	body, writer := createMultipartForm(t, nil, "torrents")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/torrents/add", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Ok.", rr.Body.String())
}

func TestTorrentMetadataFetchTimesOut(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ctrl.runtimeConfig.gotInfoTimeout = 10 * time.Millisecond

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

func TestDeleteTorrents(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
	rr := testutil.NewRequest().Delete("/api/v1/torrents/0000000000000000000000000000000000000000").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNotFound, rr.Code)

	var apiError api.Error
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&apiError), "error unmarshaling error response")
	assert.Equal(t, http.StatusNotFound, apiError.Code)

	var result api.ListTorrents
	rr = doGet(t, ctrl.router, "/api/v1/torrents")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	require.NotEmpty(t, result.Torrents)

	for _, torrent := range result.Torrents {
		rr = testutil.NewRequest().Delete("/api/v1/torrents/"+torrent.Hash.HexString()).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusNoContent, rr.Code)
	}

	rr = doGet(t, ctrl.router, "/api/v1/torrents")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.Empty(t, result.Torrents)
	assert.Equal(t, 0, result.Total)
}

func TestUpdateTorrentPosterRepeatedly(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	primeSampleMetadata(t, ctrl, ih)
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
		var torrentResp api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&torrentResp); err != nil {
			return false
		}
		return torrentResp.Poster != nil
	}, 2*time.Second, 100*time.Millisecond, "poster should be fetched")

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&createdTorrent))
	require.NotNil(t, createdTorrent.Poster)

	for i := range 3 {
		_ = i
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
	primeSampleMetadata(t, ctrl, ih)
	magnet := samples[ih]

	req := api.TorrentAdd{Magnet: &magnet, Poster: &posterA}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var torrentResp api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrentResp))

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
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrentResp))
	require.NotNil(t, torrentResp.Poster)
	posterA_URL := *torrentResp.Poster

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
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrentResp))
	assert.NotEqual(t, posterA_URL, *torrentResp.Poster)
	posterB_URL := *torrentResp.Poster

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
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrentResp))
	assert.Equal(t, posterA_URL, *torrentResp.Poster)
}

func TestUpdateTorrentWithSharedPoster(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

	ihA := bunnyHash
	primeSampleMetadata(t, ctrl, ihA)
	magnetA := samples[ihA]
	reqA := api.TorrentAdd{Magnet: &magnetA, Poster: &posterURL}
	rrA := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(reqA).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rrA.Code)
	var torrentA api.Torrent
	require.NoError(t, json.NewDecoder(rrA.Body).Decode(&torrentA))

	ihB := cosmosHash
	primeSampleMetadata(t, ctrl, ihB)
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

	ih := bunnyHash
	primeSampleMetadata(t, ctrl, ih)
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var torrentResp api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&torrentResp))

	targetPath := torrentResp.Files[0].Path
	updateReq := api.TorrentUpdate{
		Files: &[]api.TorrentFileUpdate{
			{
				Path:   targetPath,
				Viewed: true,
			},
		},
	}

	err := ctrl.updateTorrent(ih, updateReq)
	require.NoError(t, err)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	var updatedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))

	var viewedFile api.TorrentFile
	for _, f := range updatedTorrent.Files {
		if f.Path == targetPath {
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
			Path:   targetPath,
			Viewed: true,
		},
	}

	err = ctrl.updateTorrent(ih, updateReq)
	require.NoError(t, err)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))

	for _, f := range updatedTorrent.Files {
		if f.Path == targetPath {
			viewedFile = f
			break
		}
	}
	require.NotNil(t, viewedFile, "file not found in torrent")
	assert.Equal(t, firstUpdate, *viewedFile.ViewedAt)

	updateReq.Files = &[]api.TorrentFileUpdate{
		{
			Path:   targetPath,
			Viewed: false,
		},
	}

	err = ctrl.updateTorrent(ih, updateReq)
	require.NoError(t, err)

	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&updatedTorrent))

	for _, f := range updatedTorrent.Files {
		if f.Path == targetPath {
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
		EnableStremio:  new(true),
		FriendlyName:   new("My New TorrPlay"),
		HTTPServerPort: new(9090),
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
	require.NotNil(t, updatedSettings.EnableStremio)
	assert.True(t, *updatedSettings.EnableStremio)
}

func TestUpdateSettingsWithTrackers(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	trackers := ctrl.trackers

	newTrackers := []string{"udp://tracker.opentrackr.org:1337", "udp://explodie.org:6969"}
	newSettings := api.Settings{
		TorrentTrackers: &newTrackers,
	}

	rr := testutil.NewRequest().Patch("/api/v1/settings").WithJsonBody(newSettings).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	require.Eventually(t, func() bool {
		ctrl.mu.RLock()
		defer ctrl.mu.RUnlock()

		return len(ctrl.trackers) > len(trackers)
	}, 5*time.Second, 100*time.Millisecond, "trackers should be updated")

	rr = testutil.NewRequest().Get("/api/v1/settings").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var updatedSettings api.Settings
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &updatedSettings))
	assert.Equal(t, newTrackers, *updatedSettings.TorrentTrackers)
}

func TestUpdateSettingsWithTrackersValidation(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	testCases := []struct {
		name          string
		trackers      []string
		expectedCode  int
		expectedError string
	}{
		{
			name:         "valid single tracker",
			trackers:     []string{"udp://tracker.opentrackr.org:1337/announce"},
			expectedCode: http.StatusNoContent,
		},
		{
			name:         "valid multiple trackers",
			trackers:     []string{"udp://tracker.opentrackr.org:1337/announce", "wss://tracker.openwebtorrent.com"},
			expectedCode: http.StatusNoContent,
		},
		{
			name:         "valid multiple trackers in one tier",
			trackers:     []string{"udp://tracker.opentrackr.org:1337/announce,wss://tracker.openwebtorrent.com"},
			expectedCode: http.StatusNoContent,
		},
		{
			name:         "valid ipv6",
			trackers:     []string{"https://[::1]:8080/announce"},
			expectedCode: http.StatusNoContent,
		},
		{
			name:          "invalid protocol",
			trackers:      []string{"zzz://tracker.opentrackr.org:1337/announce"},
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid torrent tracker format",
		},
		{
			name:          "invalid multiple trackers in one tier",
			trackers:      []string{"udp://tracker.opentrackr.org:1337/announce,zzz://tracker.opentrackr.org:1337/announce"},
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid torrent tracker format",
		},
		{
			name:          "leading comma",
			trackers:      []string{",udp://tracker.opentrackr.org:1337/announce"},
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid torrent tracker format",
		},
		{
			name:          "trailing comma",
			trackers:      []string{"udp://tracker.opentrackr.org:1337/announce,"},
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid torrent tracker format",
		},
		{
			name:          "double comma",
			trackers:      []string{"udp://tracker.opentrackr.org:1337/announce,,wss://tracker.openwebtorrent.com"},
			expectedCode:  http.StatusBadRequest,
			expectedError: "invalid torrent tracker format",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			newSettings := api.Settings{
				TorrentTrackers: &tc.trackers,
			}
			rr := testutil.NewRequest().Patch("/api/v1/settings").WithJsonBody(newSettings).GoWithHTTPHandler(t, ctrl.router).Recorder
			assert.Equal(t, tc.expectedCode, rr.Code)
			if tc.expectedError != "" {
				assert.Contains(t, rr.Body.String(), tc.expectedError)
			}
		})
	}
}

func TestTSTorrentUploadWithPoster(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	primeSampleMetadata(t, ctrl, ih)

	body, writer := createMultipartForm(t, map[string]string{"poster": posterURL})

	req := httptest.NewRequest(http.MethodPost, "/torrent/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var result map[string]any
	err := json.NewDecoder(rr.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, ih.HexString(), result["hash"])

	// Wait for the poster to be fetched asynchronously
	require.Eventually(t, func() bool {
		getRR := doGet(t, ctrl.router, "/api/v1/torrents/"+ih.HexString())
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
	getRR := doGet(t, ctrl.router, "/api/v1/torrents/"+ih.HexString())
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

func TestUpdateTorrentStorage(t *testing.T) {
	ctrl, cleanup := newTestController(t, func(c *Controller) {
		c.settings.FileStoragePath = new(t.TempDir())
	})
	defer cleanup()

	ih := bunnyHash
	primeSampleMetadata(t, ctrl, ih)
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

func TestController_TorrentInfoBytes(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl, cleanup := newTestController(t, func(c *Controller) {
		c.settings.FileStoragePath = new(tmpDir)
		err := c.db.UpdateSettings(database.FromAPISettings(c.settings))
		require.NoError(t, err)
	})
	defer cleanup()

	body, writer := createMultipartForm(t, map[string]string{
		"storage": string(api.File),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ctrl.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "Response body: %s", w.Body.String())

	var resp api.Torrent
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	dbTorrent, err := ctrl.db.GetTorrent(resp.Hash)
	require.NoError(t, err)
	assert.NotEmpty(t, dbTorrent.InfoBytes, "InfoBytes should be saved for new torrent with file storage")

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/torrents/"+resp.Hash.HexString(), http.NoBody)
	delW := httptest.NewRecorder()
	ctrl.router.ServeHTTP(delW, delReq)
	require.Equal(t, http.StatusNoContent, delW.Code)

	body, writer = createMultipartForm(t, map[string]string{
		"storage": string(api.Memory),
	})

	req = httptest.NewRequest(http.MethodPost, "/api/v1/torrents", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()

	ctrl.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	dbTorrent, err = ctrl.db.GetTorrent(resp.Hash)
	require.NoError(t, err)
	assert.Empty(t, dbTorrent.InfoBytes, "InfoBytes should not be saved for new torrent with memory storage")

	// Pre-load the torrent into memory client with metadata so the storage switch can read InfoBytes
	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	meta, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(meta))
	require.NoError(t, err)
	<-to.GotInfo()

	updateReqBody := api.TorrentUpdate{
		Storage: utils.Ptr(api.File),
	}

	rr := testutil.NewRequest().Patch("/api/v1/torrents/"+resp.Hash.HexString()).WithJsonBody(updateReqBody).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	require.Eventually(t, func() bool {
		updatedDbTorrent, getErr := ctrl.db.GetTorrent(resp.Hash)
		return getErr == nil && len(updatedDbTorrent.InfoBytes) != 0
	}, time.Second, time.Millisecond, "InfoBytes should be saved after updating torrent to file storage")
}

func TestNewController_SettingsBootstrapping(t *testing.T) {
	t.Run("with pre-existing secrets on empty DB", func(t *testing.T) {
		dbPath := tempfile()
		defer os.Remove(dbPath)

		dbClient, err := database.NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer dbClient.Close()

		secretBefore, err := dbClient.GetJWTSecret()
		require.NoError(t, err)
		assert.NotEmpty(t, secretBefore)

		udnBefore, err := dbClient.GetDLNAUDN()
		require.NoError(t, err)
		assert.NotEmpty(t, udnBefore)

		metricsSvc := metrics.New()
		ctrl, err := newController(".", "127.0.0.1", 8080, dbClient, nil, metricsSvc, testControllerRuntimeConfig())
		require.NoError(t, err)
		defer ctrl.Shutdown()

		assert.Equal(t, "TorrPlay", *ctrl.settings.FriendlyName)
		assert.Equal(t, 8090, *ctrl.settings.HTTPServerPort)
		assert.Equal(t, 50, *ctrl.settings.TorrentClient.EstablishedConnsPerTorrent)

		secretAfter, err := dbClient.GetJWTSecret()
		require.NoError(t, err)
		assert.Equal(t, secretBefore, secretAfter)

		udnAfter, err := dbClient.GetDLNAUDN()
		require.NoError(t, err)
		assert.Equal(t, udnBefore, udnAfter)
	})

	t.Run("with pre-existing secrets and partial settings", func(t *testing.T) {
		dbPath := tempfile()
		defer os.Remove(dbPath)

		dbClient, err := database.NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer dbClient.Close()

		secretBefore, err := dbClient.GetJWTSecret()
		require.NoError(t, err)
		assert.NotEmpty(t, secretBefore)

		udnBefore, err := dbClient.GetDLNAUDN()
		require.NoError(t, err)
		assert.NotEmpty(t, udnBefore)

		customPort := 9999
		customName := "CustomTorrPlay"
		partialSettings := &api.Settings{
			HTTPServerPort: &customPort,
			FriendlyName:   &customName,
		}
		err = dbClient.UpdateSettings(database.FromAPISettings(partialSettings))
		require.NoError(t, err)

		metricsSvc := metrics.New()
		ctrl, err := newController(".", "127.0.0.1", 8080, dbClient, nil, metricsSvc, testControllerRuntimeConfig())
		require.NoError(t, err)
		defer ctrl.Shutdown()

		assert.Equal(t, "CustomTorrPlay", *ctrl.settings.FriendlyName)
		assert.Equal(t, 9999, *ctrl.settings.HTTPServerPort)
		assert.Equal(t, 50, *ctrl.settings.TorrentClient.EstablishedConnsPerTorrent)

		secretAfter, err := dbClient.GetJWTSecret()
		require.NoError(t, err)
		assert.Equal(t, secretBefore, secretAfter)

		udnAfter, err := dbClient.GetDLNAUDN()
		require.NoError(t, err)
		assert.Equal(t, udnBefore, udnAfter)
	})
}

func TestUpdateSettings_PreservesSecrets(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	secretBefore, err := ctrl.db.GetJWTSecret()
	require.NoError(t, err)
	assert.NotEmpty(t, secretBefore)

	udnBefore, err := ctrl.db.GetDLNAUDN()
	require.NoError(t, err)
	assert.NotEmpty(t, udnBefore)

	newFriendlyName := "PatchedTorrPlay"
	patchBody := api.Settings{
		FriendlyName: &newFriendlyName,
	}
	rr := testutil.NewRequest().Patch("/api/v1/settings").WithJsonBody(patchBody).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code)

	rr = testutil.NewRequest().Get("/api/v1/settings").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var res api.Settings
	err = json.NewDecoder(rr.Body).Decode(&res)
	require.NoError(t, err)
	assert.Equal(t, newFriendlyName, *res.FriendlyName)

	secretAfter, err := ctrl.db.GetJWTSecret()
	require.NoError(t, err)
	assert.Equal(t, secretBefore, secretAfter)

	udnAfter, err := ctrl.db.GetDLNAUDN()
	require.NoError(t, err)
	assert.Equal(t, udnBefore, udnAfter)
}

func TestController_ShutdownIdempotent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	// Calling Shutdown multiple times should not panic
	require.NotPanics(t, func() {
		ctrl.Shutdown()
		ctrl.Shutdown()
	})
}

func TestController_CleanupExpiredTorrents_SkipsActive(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	primeSampleMetadata(t, ctrl, ih)
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	// Mark the torrent expired in the tracker
	ctrl.torrentTracker.mu.Lock()
	ctrl.torrentTracker.torrents[ih] = torrentInfo{
		lastUsedAt:  time.Now().Add(-4 * time.Hour),
		storageType: api.Memory,
	}
	ctrl.torrentTracker.mu.Unlock()

	// Simulate an active streaming reader in streamPool
	primeSampleMetadata(t, ctrl, ih)
	to, err := ctrl.addTorrentByHash(ih)
	require.NoError(t, err)
	<-to.GotInfo()

	file := to.Files()[0]
	ctrl.streamPool.SetReadaheadBudget(1024 * 1024)
	_, release, err := ctrl.streamPool.Acquire(file, stream.MemoryStorage)
	require.NoError(t, err)
	defer release()

	// Trigger cleanup
	ctrl.cleanupExpiredTorrents()

	// The torrent should NOT have been removed because of the active reader
	ctrl.torrentTracker.mu.RLock()
	info, exists := ctrl.torrentTracker.torrents[ih]
	ctrl.torrentTracker.mu.RUnlock()

	assert.True(t, exists, "active torrent should not be dropped during cleanup")
	assert.Less(t, time.Since(info.lastUsedAt), 1*time.Minute, "lastUsedAt should have been refreshed")
}

func TestController_CleanupExpiredTorrents_ReleasesCompletedPreload(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.Hash{1}
	otherHash := metainfo.Hash{2}
	ctrl.torrentTracker.mu.Lock()
	ctrl.torrentTracker.torrents[ih] = torrentInfo{
		lastUsedAt:  time.Now().Add(-4 * time.Hour),
		storageType: api.Memory,
	}
	ctrl.torrentTracker.mu.Unlock()

	ctrl.streamPool.SetReadaheadBudget(1000)
	require.Equal(t, int64(1000), ctrl.streamPool.ReservePreloadBudget(ih, 1000))
	task := &preloadTask{
		cancel: func() {},
		clearProtection: func() {
			ctrl.streamPool.ReleasePreloadBudget(ih)
		},
	}
	task.ready.Store(true)
	ctrl.preloads.Store(ih, task)

	ctrl.cleanupExpiredTorrents()

	_, preloading := ctrl.preloads.Load(ih)
	assert.False(t, preloading)
	ctrl.torrentTracker.mu.RLock()
	_, tracked := ctrl.torrentTracker.torrents[ih]
	ctrl.torrentTracker.mu.RUnlock()
	assert.False(t, tracked)
	assert.Equal(t, int64(1000), ctrl.streamPool.ReservePreloadBudget(otherHash, 1000))
	ctrl.streamPool.ReleasePreloadBudget(otherHash)
}

func TestUpdateTorrent_NoConfiguredFileStorage_Unlocks(t *testing.T) {
	ctrl, cleanup := newTestController(t, func(c *Controller) {
		c.settings.FileStoragePath = nil
	})
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	primeSampleMetadata(t, ctrl, ih)
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	// Attempt to switch to File storage with unconfigured path
	updateReq := api.TorrentUpdate{
		Storage: utils.Ptr(api.File),
	}
	rr = testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", ih)).WithJsonBody(updateReq).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusBadRequest, rr.Code)

	// Verify that controller is not deadlocked and can handle subsequent requests
	rr = testutil.NewRequest().Get("/api/v1/settings").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestUpdateTorrent_StorageSwitch_GotInfoTimeout_Unlocks(t *testing.T) {
	ctrl, cleanup := newTestController(t, func(c *Controller) {
		c.settings.FileStoragePath = new(t.TempDir())
	})
	defer cleanup()
	ctrl.runtimeConfig.gotInfoTimeout = 10 * time.Millisecond

	// Add a dummy torrent directly into the database with an unresolvable magnet link (empty InfoBytes)
	deadHash := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	deadMagnet := "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=DeadTorrent"
	err := ctrl.db.CreateTorrent(&database.Torrent{
		Torrent: api.Torrent{
			Hash:    deadHash,
			Name:    "DeadTorrent",
			Magnet:  deadMagnet,
			Storage: utils.Ptr(api.Memory),
		},
	})
	require.NoError(t, err)

	// Attempt to update storage from Memory to File - this will trigger loadTorrent and time out on <-to.GotInfo()
	updateReq := api.TorrentUpdate{
		Storage: utils.Ptr(api.File),
	}
	rr := testutil.NewRequest().Patch(fmt.Sprintf("/api/v1/torrents/%s", deadHash)).WithJsonBody(updateReq).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusGatewayTimeout, rr.Code)

	// Verify that controller is not deadlocked and can handle subsequent requests
	rr = testutil.NewRequest().Get("/api/v1/settings").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	rr = testutil.NewRequest().Get("/api/v1/torrents").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestTorrentActiveField(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	primeSampleMetadata(t, ctrl, ih)
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	// Initially not active
	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)
	var gotTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&gotTorrent))
	require.NotNil(t, gotTorrent.Active)
	assert.False(t, *gotTorrent.Active)

	var listResult api.ListTorrents
	rr = doGet(t, ctrl.router, "/api/v1/torrents")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&listResult))
	require.Len(t, listResult.Torrents, 1)
	require.NotNil(t, listResult.Torrents[0].Active)
	assert.False(t, *listResult.Torrents[0].Active)

	// Simulate active streaming
	primeSampleMetadata(t, ctrl, ih)
	to, err := ctrl.addTorrentByHash(ih)
	require.NoError(t, err)
	<-to.GotInfo()

	file := to.Files()[0]
	ctrl.streamPool.SetReadaheadBudget(1024 * 1024)
	_, release, err := ctrl.streamPool.Acquire(file, stream.MemoryStorage)
	require.NoError(t, err)

	// Now should be active
	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&gotTorrent))
	require.NotNil(t, gotTorrent.Active)
	assert.True(t, *gotTorrent.Active)

	rr = doGet(t, ctrl.router, "/api/v1/torrents")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&listResult))
	require.Len(t, listResult.Torrents, 1)
	require.NotNil(t, listResult.Torrents[0].Active)
	assert.True(t, *listResult.Torrents[0].Active)

	// Release stream
	release()

	// Reader returns to idle pool — hasTorrentReaders still returns true
	// because idle readers count as active (they can be resumed).
	rr = doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&gotTorrent))
	require.NotNil(t, gotTorrent.Active)
	assert.True(t, *gotTorrent.Active)
}

func TestCalcReadaheadPct(t *testing.T) {
	tests := []struct {
		name      string
		maxMemory int64
		want      int
	}{
		{"below 64MB", 32 * 1024 * 1024, 50},
		{"exactly 64MB", 64 * 1024 * 1024, 50},
		{"midway 64-128MB (96MB)", 96 * 1024 * 1024, 55},
		{"boundary 128MB - 1", 128*1024*1024 - 1, 59},
		{"exactly 128MB", 128 * 1024 * 1024, 60},
		{"midway 128-256MB (192MB)", 192 * 1024 * 1024, 65},
		{"boundary 256MB - 1", 256*1024*1024 - 1, 69},
		{"exactly 256MB", 256 * 1024 * 1024, 70},
		{"midway 256-512MB (384MB)", 384 * 1024 * 1024, 72},
		{"boundary 512MB - 1", 512*1024*1024 - 1, 74},
		{"exactly 512MB", 512 * 1024 * 1024, 75},
		{"1GB", 1024 * 1024 * 1024, 75},
		{"2GB", 2048 * 1024 * 1024, 75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcReadaheadPct(tt.maxMemory)
			assert.Equal(t, tt.want, got, "maxMemory = %d", tt.maxMemory)
		})
	}
}

func TestSlogMiddlewareRedactsTokens(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	c := &Controller{logger: logger}

	tests := []struct {
		name          string
		requestURL    string
		unexpectedStr string
		expectedPath  string
	}{
		{
			name:          "stremio path token is redacted",
			requestURL:    "/stremio/secret-access-token-123/manifest.json",
			unexpectedStr: "secret-access-token-123",
			expectedPath:  "/stremio/[REDACTED]/manifest.json",
		},
		{
			name:          "query token is redacted",
			requestURL:    "/api/v1/stream?token=secret-query-token-456",
			unexpectedStr: "secret-query-token-456",
			expectedPath:  "/api/v1/stream",
		},
		{
			name:          "stremio unauthenticated path is not altered",
			requestURL:    "/stremio/manifest.json",
			unexpectedStr: "[REDACTED]",
			expectedPath:  "/stremio/manifest.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			req := httptest.NewRequest(http.MethodGet, tc.requestURL, http.NoBody)
			rr := httptest.NewRecorder()

			mw := c.SlogMiddleware()
			testHandler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			testHandler.ServeHTTP(rr, req)

			logOutput := buf.String()
			assert.Contains(t, logOutput, tc.expectedPath)
			if tc.unexpectedStr != "" {
				assert.NotContains(t, logOutput, tc.unexpectedStr)
			}
		})
	}
}

func TestMetricsMiddlewareRedactsTokens(t *testing.T) {
	metricsSvc := metrics.New()
	c := &Controller{metrics: metricsSvc}

	t.Run("unrouted request with stremio token is redacted in metrics label", func(t *testing.T) {
		mw := c.MetricsMiddleware()
		testHandler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/stremio/secret-metrics-token-123/manifest.json", http.NoBody)
		rr := httptest.NewRecorder()
		testHandler.ServeHTTP(rr, req)

		redactedCounter, err := metricsSvc.HTTPRequestsTotal.GetMetricWithLabelValues("200", http.MethodGet, "/stremio/[REDACTED]/manifest.json")
		require.NoError(t, err)
		assert.Equal(t, float64(1), promtestutil.ToFloat64(redactedCounter))

		rawCounter, err := metricsSvc.HTTPRequestsTotal.GetMetricWithLabelValues("200", http.MethodGet, "/stremio/secret-metrics-token-123/manifest.json")
		require.NoError(t, err)
		assert.Equal(t, float64(0), promtestutil.ToFloat64(rawCounter))
	})

	t.Run("mounted stremio handler fallback redacts path token", func(t *testing.T) {
		r := chi.NewRouter()
		r.Use(c.MetricsMiddleware())
		r.Mount("/stremio", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/stremio/secret-metrics-token-456/catalog/movie/all.json", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		redactedCounter, err := metricsSvc.HTTPRequestsTotal.GetMetricWithLabelValues("200", http.MethodGet, "/stremio/[REDACTED]/catalog/movie/all.json")
		require.NoError(t, err)
		assert.Equal(t, float64(1), promtestutil.ToFloat64(redactedCounter))

		rawCounter, err := metricsSvc.HTTPRequestsTotal.GetMetricWithLabelValues("200", http.MethodGet, "/stremio/secret-metrics-token-456/catalog/movie/all.json")
		require.NoError(t, err)
		assert.Equal(t, float64(0), promtestutil.ToFloat64(rawCounter))
	})

	t.Run("standard chi routed pattern is preserved", func(t *testing.T) {
		r := chi.NewRouter()
		r.Use(c.MetricsMiddleware())
		r.Get("/api/v1/torrents", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/torrents", http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		patternCounter, err := metricsSvc.HTTPRequestsTotal.GetMetricWithLabelValues("200", http.MethodGet, "/api/v1/torrents")
		require.NoError(t, err)
		assert.Equal(t, float64(1), promtestutil.ToFloat64(patternCounter))
	})
}
