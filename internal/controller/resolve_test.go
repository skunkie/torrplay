// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestAddResolvedTorrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = bunnyMeta.Write(w) }))
	defer server.Close()
	for _, saveStorage := range []api.TorrentStorage{api.Memory, api.File} {
		t.Run("save_to_"+string(saveStorage), func(t *testing.T) {
			ctrl, cleanup := newTestController(t, func(c *Controller) { c.settings.Load().FileStoragePath = new(t.TempDir()) })
			defer cleanup()
			rr := testutil.NewRequest().Post("/api/v1/torrent-resolutions").WithJsonBody(api.TorrentResolutionRequest{URL: server.URL}).GoWithHTTPHandler(t, ctrl.router).Recorder
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			var resolved api.Torrent
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resolved))
			require.Equal(t, api.Memory, utils.Val(resolved.Storage))
			_, err := ctrl.db.GetTorrent(resolved.Hash)
			require.ErrorIs(t, err, database.ErrTorrentNotFound)
			rr = testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(api.TorrentAdd{Magnet: &resolved.Magnet, Storage: &saveStorage}).GoWithHTTPHandler(t, ctrl.router).Recorder
			require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
			saved, err := ctrl.db.GetTorrent(resolved.Hash)
			require.NoError(t, err)
			assert.Equal(t, resolved.Hash, saved.Hash)
			assert.Equal(t, saveStorage, utils.Val(saved.Storage))
		})
	}
}

func TestResolveTorrent(t *testing.T) {
	var fixture bytes.Buffer
	require.NoError(t, bunnyMeta.Write(&fixture))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fixture.Bytes()) }))
	defer server.Close()
	storageDir := t.TempDir()
	ctrl, cleanup := newTestController(t, func(c *Controller) { c.settings.Load().FileStoragePath = &storageDir })
	defer cleanup()
	request := api.TorrentResolutionRequest{URL: server.URL}
	var first *torrent.Torrent
	var result api.Torrent
	for range 2 {
		rr := testutil.NewRequest().Post("/api/v1/torrent-resolutions").WithJsonBody(request).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
		assert.Equal(t, bunnyHash, result.Hash)
		require.NotNil(t, result.Files)
		require.Len(t, result.Files, 1)
		assert.Equal(t, "Big Buck Bunny.mp4", result.Files[0].Name)
		require.NotEmpty(t, result.Magnet)
		_, err := ctrl.db.GetTorrent(bunnyHash)
		require.ErrorIs(t, err, database.ErrTorrentNotFound)
		to, ok := ctrl.client.Torrent(bunnyHash)
		require.True(t, ok)
		if first != nil {
			assert.Same(t, first, to)
		}
		first = to
		select {
		case <-to.GotInfo():
		default:
			t.Fatal("metadata must be immediately available")
		}
		ctrl.torrentTracker.mu.RLock()
		tracked, ok := ctrl.torrentTracker.torrents[bunnyHash]
		ctrl.torrentTracker.mu.RUnlock()
		require.True(t, ok)
		assert.Equal(t, api.Memory, tracked.storageType)
		assert.Equal(t, api.Memory, utils.Val(result.Storage))
		assert.False(t, tracked.lastUsedAt.IsZero())
		entries, err := os.ReadDir(storageDir)
		require.NoError(t, err)
		assert.Empty(t, entries, "resolution must not create torrent files")
	}
	for _, magnet := range []*string{nil, &result.Magnet} {
		to, err := ctrl.resolveTorrentForRequest(magnet, bunnyHash)
		require.NoError(t, err)
		assert.Same(t, first, to)
	}
}

func TestResolveStoredTorrent(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	primeSampleMetadata(t, ctrl, bunnyHash)
	to, ok := ctrl.client.Torrent(bunnyHash)
	require.True(t, ok)
	stored := database.FromAPITorrent(torrentToMetadata(to))
	stored.Title = new("Custom title")
	stored.Storage = utils.Ptr(api.File)
	require.NoError(t, ctrl.db.CreateTorrent(stored))
	before, err := ctrl.db.GetTorrent(bunnyHash)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = bunnyMeta.Write(w) }))
	defer server.Close()
	rr := testutil.NewRequest().Post("/api/v1/torrent-resolutions").WithJsonBody(api.TorrentResolutionRequest{URL: server.URL}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)
	var result api.Torrent
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
	assert.Equal(t, database.ToAPITorrent(before), &result)
	after, err := ctrl.db.GetTorrent(bunnyHash)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	current, ok := ctrl.client.Torrent(bunnyHash)
	require.True(t, ok)
	assert.Same(t, to, current)
}

func TestResolveTorrentErrors(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("x", maxTorrentResolveBytes+1)))
		case "/timeout":
			<-r.Context().Done()
		case "/metainfo":
			_, _ = w.Write([]byte("d4:infod4:namei1eee"))
		case "/v2-only":
			_, _ = w.Write([]byte("d4:infod12:meta versioni2e4:name4:testee"))
		case "/unsupported-version":
			_, _ = w.Write([]byte("d4:infod12:meta versioni3e4:name4:testee"))
		case "/valid":
			_ = bunnyMeta.Write(w)
		default:
			_, _ = w.Write([]byte("not a torrent"))
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"missing", `{"url":"` + server.URL + `/missing"}`, 502},
		{"large", `{"url":"` + server.URL + `/large"}`, 502},
		{"timeout", `{"url":"` + server.URL + `/timeout"}`, 504},
		{"invalid torrent", `{"url":"` + server.URL + `"}`, 400},
		{"invalid metainfo", `{"url":"` + server.URL + `/metainfo"}`, 400},
		{"v2-only metainfo", `{"url":"` + server.URL + `/v2-only"}`, 400},
		{"unsupported metainfo version", `{"url":"` + server.URL + `/unsupported-version"}`, 400},
		{"file scheme", `{"url":"file:///etc/passwd"}`, 400},
		{"empty", `{}`, 400},
		{"json", `{`, 400},
		{"storage", `{"url":"` + server.URL + `","storage":"invalid"}`, 400},
		{"file storage", `{"url":"` + server.URL + `/valid","storage":"file"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/torrent-resolutions", strings.NewReader(tc.body))
			if tc.name == "timeout" {
				ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
				defer cancel()
				req = req.WithContext(ctx)
			}
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			ctrl.router.ServeHTTP(rr, req)
			assert.Equal(t, tc.status, rr.Code, rr.Body.String())
			if tc.name == "v2-only metainfo" || tc.name == "unsupported metainfo version" {
				var response api.Error
				require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
				assert.Equal(t, http.StatusBadRequest, response.Code)
				assert.Equal(t, "torrent metainfo must include a v1 info hash", response.Message)
				assert.Empty(t, ctrl.client.Torrents())
				stored, err := ctrl.db.GetTorrents()
				require.NoError(t, err)
				assert.Empty(t, stored)
			}
		})
	}
}

func TestResolveTorrentMergesExistingMetadata(t *testing.T) {
	runtimeConfig := testControllerRuntimeConfig()
	configureClient := runtimeConfig.configureClient
	runtimeConfig.configureClient = func(config *torrent.ClientConfig) {
		configureClient(config)
		config.DisableWebseeds = false
	}
	ctrl, cleanup := newTestControllerWithRuntimeConfig(t, runtimeConfig)
	defer cleanup()
	meta := *bunnyMeta
	meta.Announce = "http://tracker.local/announce?passkey=old"
	to, err := ctrl.loadTorrentSpec(torrent.TorrentSpecFromMetaInfo(&meta), api.Memory)
	require.NoError(t, err)
	require.NotNil(t, to.Info())
	meta.Announce = "http://tracker.local/announce?passkey=new"
	meta.UrlList = []string{"http://webseed.local/video"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = meta.Write(w) }))
	defer server.Close()
	for range 2 {
		rr := testutil.NewRequest().Post("/api/v1/torrent-resolutions").WithJsonBody(api.TorrentResolutionRequest{URL: server.URL}).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var result api.Torrent
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
		assert.Contains(t, result.Magnet, "passkey=old")
		assert.Contains(t, result.Magnet, "passkey=new")
		assert.Contains(t, to.Metainfo().UrlList, meta.UrlList[0])
		current, ok := ctrl.client.Torrent(bunnyHash)
		require.True(t, ok)
		assert.Same(t, to, current)
		_, err = ctrl.db.GetTorrent(bunnyHash)
		require.ErrorIs(t, err, database.ErrTorrentNotFound)
	}
}

func TestResolveStoredTorrentPoster(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	primeSampleMetadata(t, ctrl, bunnyHash)
	to, ok := ctrl.client.Torrent(bunnyHash)
	require.True(t, ok)
	posterID, err := ctrl.images.SaveData([]byte("poster"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = bunnyMeta.Write(w) }))
	defer server.Close()
	for _, tc := range []struct {
		name, id string
	}{
		{"existing", posterID},
		{"missing", "missing-poster"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := database.FromAPITorrent(torrentToMetadata(to))
			stored.Poster = &tc.id
			require.NoError(t, ctrl.db.CreateTorrent(stored))
			t.Cleanup(func() { require.NoError(t, ctrl.db.DeleteTorrent(bunnyHash)) })
			before, err := ctrl.db.GetTorrent(bunnyHash)
			require.NoError(t, err)
			rr := testutil.NewRequest().Post("/api/v1/torrent-resolutions").WithJsonBody(api.TorrentResolutionRequest{URL: server.URL}).GoWithHTTPHandler(t, ctrl.router).Recorder
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			var result api.Torrent
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
			get := testutil.NewRequest().Get("/api/v1/torrents/"+bunnyHash.HexString()).GoWithHTTPHandler(t, ctrl.router).Recorder
			require.Equal(t, http.StatusOK, get.Code)
			var expected api.Torrent
			require.NoError(t, json.Unmarshal(get.Body.Bytes(), &expected))
			assert.Equal(t, expected.Poster, result.Poster)
			if tc.name == "existing" {
				require.NotNil(t, result.Poster)
				assert.Contains(t, *result.Poster, "/posters/"+posterID)
			} else {
				assert.Nil(t, result.Poster)
			}
			after, err := ctrl.db.GetTorrent(bunnyHash)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestResolveTorrentSuppliesMissingMetadata(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	to, err := ctrl.loadTorrent(samples[bunnyHash], api.Memory)
	require.NoError(t, err)
	require.Nil(t, to.Info())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = bunnyMeta.Write(w) }))
	defer server.Close()
	rr := testutil.NewRequest().Post("/api/v1/torrent-resolutions").WithJsonBody(api.TorrentResolutionRequest{URL: server.URL}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, to.Info())
	var result api.Torrent
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
	require.Len(t, result.Files, 1)
}
