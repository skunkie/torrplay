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
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	torrentstorage "github.com/anacrolix/torrent/storage"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestTSCorrectionMiddleware(t *testing.T) {
	testCases := []struct {
		name                string
		method              string
		url                 string
		contentType         string
		expectedPath        string
		expectedQuery       string
		expectedContentType string
	}{
		{
			name:          "URL encoding unescape",
			method:        "GET",
			url:           "/stream/file%26name.mp4?link=some_link",
			expectedPath:  "/stream/file&name.mp4",
			expectedQuery: "link=some_link",
		},
		{
			name:          "play parameter normalization",
			method:        "GET",
			url:           "/stream/file.mp4?link=some_link&play",
			expectedPath:  "/stream/file.mp4",
			expectedQuery: "link=some_link&play=true",
		},
		{
			name:          "preload parameter normalization",
			method:        "GET",
			url:           "/stream/file.mp4?link=some_link&preload",
			expectedPath:  "/stream/file.mp4",
			expectedQuery: "link=some_link&preload=true",
		},
		{
			name:          "stat parameter normalization",
			method:        "GET",
			url:           "/stream/file.mp4?link=some_link&stat",
			expectedPath:  "/stream/file.mp4",
			expectedQuery: "link=some_link&stat=true",
		},
		{
			name:          "multiple empty params normalized",
			method:        "GET",
			url:           "/stream/file.mp4?play&preload&stat",
			expectedPath:  "/stream/file.mp4",
			expectedQuery: "play=true&preload=true&stat=true",
		},
		{
			name:         "non-stream path - no change",
			method:       "GET",
			url:          "/other/path?foo=bar",
			expectedPath: "/other/path",
		},
		{
			name:                "header correction - cache path",
			method:              "POST",
			url:                 "/cache",
			contentType:         "text/plain",
			expectedContentType: "application/json",
		},
		{
			name:                "header correction - torrents path",
			method:              "POST",
			url:                 "/torrents",
			contentType:         "",
			expectedContentType: "application/json",
		},
		{
			name:                "header correction - settings path",
			method:              "POST",
			url:                 "/settings",
			contentType:         "application/xml",
			expectedContentType: "application/json",
		},
		{
			name:                "header correction - viewed path",
			method:              "POST",
			url:                 "/viewed",
			contentType:         "application/json",
			expectedContentType: "application/json",
		},
		{
			name:                "non-correction path - header unchanged",
			method:              "POST",
			url:                 "/other",
			contentType:         "text/plain",
			expectedContentType: "text/plain",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.url, http.NoBody)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			rr := httptest.NewRecorder()

			mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.expectedContentType != "" {
					assert.Equal(t, tc.expectedContentType, r.Header.Get("Content-Type"))
					return
				}

				assert.Equal(t, tc.expectedPath, r.URL.Path)

				if tc.expectedQuery != "" {
					expected, _ := url.ParseQuery(tc.expectedQuery)
					actual, _ := url.ParseQuery(r.URL.RawQuery)
					assert.Equal(t, expected, actual)
				}
			})

			middleware := tSCorrectionMiddleware(mockHandler)
			middleware.ServeHTTP(rr, req)
		})
	}
}

func TestTSUploadTorrentMiddleware(t *testing.T) {
	const testBoundary = "testboundary123"

	tests := []struct {
		name           string
		path           string
		contentType    string
		formValues     map[string][]string
		files          map[string]string
		expectRewrite  bool
		expectedFields map[string]string
		expectedFiles  int
	}{
		{
			name:          "non-upload path - passes through",
			path:          "/other",
			contentType:   "multipart/form-data; boundary=" + testBoundary,
			expectRewrite: false,
		},
		{
			name:          "wrong content type - passes through",
			path:          "/torrent/upload",
			contentType:   "application/json",
			expectRewrite: false,
		},
		{
			name:          "no multipart form - passes through",
			path:          "/torrent/upload",
			contentType:   "multipart/form-data; boundary=" + testBoundary,
			expectRewrite: false,
		},
		{
			name:        "upload with files - rewrites multipart, skips data/save",
			path:        "/torrent/upload",
			contentType: "multipart/form-data; boundary=" + testBoundary,
			formValues: map[string][]string{
				"save":     {"1"},
				"data":     {`{"key":"value"}`},
				"category": {"movies"},
				"tags":     {"action", "hd"},
			},
			files: map[string]string{
				"torrent": "movie.torrent",
			},
			expectRewrite: true,
			expectedFields: map[string]string{
				"category": "movies",
			},
			expectedFiles: 1,
		},
		{
			name:        "multiple files - all copied as 'file' fields",
			path:        "/torrent/upload",
			contentType: "multipart/form-data; boundary=" + testBoundary,
			files: map[string]string{
				"file1": "a.torrent",
				"file2": "b.torrent",
			},
			expectRewrite: true,
			expectedFiles: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			_ = writer.SetBoundary(testBoundary)

			for key, values := range tt.formValues {
				for _, val := range values {
					_ = writer.WriteField(key, val)
				}
			}

			for _, filename := range tt.files {
				part, _ := writer.CreateFormFile("torrent", filename)
				_, _ = io.WriteString(part, "fake content")
			}

			_ = writer.Close()

			req := httptest.NewRequest(http.MethodPost, tt.path, &body)
			req.Header.Set("Content-Type", tt.contentType)

			rr := httptest.NewRecorder()

			called := false
			mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if !tt.expectRewrite {
					return
				}

				require.NoError(t, r.ParseMultipartForm(32<<20))

				assert.Empty(t, r.MultipartForm.Value["data"])
				assert.Empty(t, r.MultipartForm.Value["save"])

				for k, v := range tt.expectedFields {
					assert.Contains(t, r.MultipartForm.Value[k], v)
				}

				total := 0
				for _, fileHeaders := range r.MultipartForm.File {
					total += len(fileHeaders)
					for _, fh := range fileHeaders {
						assert.NotEmpty(t, fh.Filename)
					}
				}
				assert.Equal(t, tt.expectedFiles, total)
			})

			middleware := tSUploadTorrentMiddleware(mockHandler)
			middleware.ServeHTTP(rr, req)

			assert.True(t, called)
		})
	}
}

func TestTSUploadTorrentMiddleware_ParseError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/torrent/upload", strings.NewReader("bad data"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=bad")

	rr := httptest.NewRecorder()

	called := false
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	middleware := tSUploadTorrentMiddleware(mockHandler)
	middleware.ServeHTTP(rr, req)

	assert.False(t, called, "next handler should not be called on parse error")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestTSViewed_Deadlock(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	magnet := samples[ih]

	rr := testutil.NewRequest().Post("/api/v1/torrents").
		WithJsonBody(api.TorrentAdd{Magnet: &magnet}).
		GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	var createdTorrent api.Torrent
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&createdTorrent))
	require.NotEmpty(t, createdTorrent.Files)

	const goroutines = 10
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(goroutines * 2)
		for i := range goroutines {
			_ = i
			// Concurrent set-viewed requests.
			go func() {
				defer wg.Done()
				body := api.TSViewedRequest{
					Hash:      ih.HexString(),
					FileIndex: 1,
					Action:    "set",
				}
				testutil.NewRequest().Post("/viewed").
					WithJsonBody(body).
					GoWithHTTPHandler(t, ctrl.router)
			}()
			// Concurrent list-viewed requests interleaved.
			go func() {
				defer wg.Done()
				body := api.TSViewedRequest{Action: "list"}
				testutil.NewRequest().Post("/viewed").
					WithJsonBody(body).
					GoWithHTTPHandler(t, ctrl.router)
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
		// All goroutines completed — no deadlock.
	case <-time.After(10 * time.Second):
		t.Fatal("TSViewed deadlock detected: goroutines did not complete within 10 seconds")
	}
}

func TestTSTorrentsAddMagnetField(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	magnet := samples[ih]

	// 1. Add via /torrents with magnet link
	addReq := map[string]any{
		"action":     "add",
		"link":       magnet,
		"title":      "Sintel Test",
		"save_to_db": true,
	}

	rr := testutil.NewRequest().Post("/torrents").
		WithJsonBody(addReq).
		GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	getRR := doGet(t, ctrl.router, "/api/v1/torrents/"+ih.HexString())
	require.Equal(t, http.StatusOK, getRR.Code)

	var fetchedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(getRR.Body).Decode(&fetchedTorrent))
	assert.NotEmpty(t, fetchedTorrent.Magnet)
	assert.Contains(t, fetchedTorrent.Magnet, ih.HexString())

	// 2. Add via /torrents with hash field
	ih2 := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	addHashReq := map[string]any{
		"action":     "add",
		"hash":       ih2.HexString(),
		"title":      "Bunny Test",
		"save_to_db": true,
	}

	rr2 := testutil.NewRequest().Post("/torrents").
		WithJsonBody(addHashReq).
		GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr2.Code)

	getRR2 := doGet(t, ctrl.router, "/api/v1/torrents/"+ih2.HexString())
	require.Equal(t, http.StatusOK, getRR2.Code)

	var fetchedTorrent2 api.Torrent
	require.NoError(t, json.NewDecoder(getRR2.Body).Decode(&fetchedTorrent2))
	assert.NotEmpty(t, fetchedTorrent2.Magnet)
	assert.Contains(t, fetchedTorrent2.Magnet, ih2.HexString())
}

func TestTSTorrentsAddWhileStreaming(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	magnet := samples[ih]

	sintelFile, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(sintelFile)
	_ = sintelFile.Close()
	require.NoError(t, err)

	specTorrent, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-specTorrent.GotInfo()

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	// 1. Stream the torrent before adding to database.
	streamURL := fmt.Sprintf("%s/stream/Sintel.mp4?link=%s&play&index=6", server.URL, ih.HexString())
	req, err := http.NewRequest(http.MethodGet, streamURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=0-")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)

	buf := make([]byte, 1024)
	n, err := io.ReadFull(resp.Body, buf)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)

	// Verify torrent is active.
	require.True(t, ctrl.hasTorrentReaders(ih))

	to, ok := ctrl.client.Torrent(ih)
	require.True(t, ok)
	select {
	case <-to.Closed():
		t.Fatal("torrent should not be closed while streaming")
	default:
	}

	// 2. Add via /torrents with save_to_db: true while stream is active.
	addReq := api.TSTorrentRequest{
		Action:   "add",
		Link:     &magnet,
		Title:    new("Sintel Test"),
		SaveToDB: new(true),
	}
	addBody, err := json.Marshal(addReq)
	require.NoError(t, err)
	addHTTPReq, err := http.NewRequest(http.MethodPost, server.URL+"/torrents", bytes.NewBuffer(addBody))
	require.NoError(t, err)
	addHTTPReq.Header.Set("Content-Type", "application/json")

	addResp, err := http.DefaultClient.Do(addHTTPReq)
	require.NoError(t, err)
	defer addResp.Body.Close()
	require.Equal(t, http.StatusOK, addResp.StatusCode)

	// 3. Verify torrent is still loaded and not closed.
	toAfter, ok := ctrl.client.Torrent(ih)
	require.True(t, ok, "torrent should remain in client while streaming")
	assert.Equal(t, to, toAfter)
	select {
	case <-toAfter.Closed():
		t.Fatal("torrent should not be closed after adding to database")
	default:
	}

	// 4. Verify streaming continues without error.
	n, err = io.ReadFull(resp.Body, buf)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
}

func TestBuildTSTorrentResponse(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	t.Run("torrent in database only", func(t *testing.T) {
		meta := &api.Torrent{
			Hash:      ih,
			Name:      "Sintel",
			Title:     new("Sintel"),
			TotalSize: 12345678,
		}

		resp := ctrl.buildTSTorrentResponse(meta, nil)
		assert.Equal(t, tsStatInDB, resp.Stat)
		assert.Equal(t, "Torrent in db", resp.StatString)
		assert.Equal(t, int64(12345678), resp.TorrentSize)
		assert.Equal(t, float64(0), resp.DownloadSpeed)
		assert.Equal(t, float64(0), resp.UploadSpeed)
	})

	t.Run("active torrent getting info", func(t *testing.T) {
		fakeMagnet := "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=Unknown"
		to, err := ctrl.client.AddMagnet(fakeMagnet)
		require.NoError(t, err)

		meta := &api.Torrent{
			Hash:  to.InfoHash(),
			Name:  "Unknown",
			Title: new("Unknown"),
		}
		resp := ctrl.buildTSTorrentResponse(meta, to)

		assert.Equal(t, tsStatGettingInfo, resp.Stat)
		assert.Equal(t, "Torrent getting info", resp.StatString)
	})

	t.Run("active torrent with metadata", func(t *testing.T) {
		sintelFile, err := os.Open(sintelTorrentFile)
		require.NoError(t, err)
		metaInfo, err := metainfo.Load(sintelFile)
		_ = sintelFile.Close()
		require.NoError(t, err)

		to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
		require.NoError(t, err)
		<-to.GotInfo()

		meta := torrentToMetadata(to)
		resp := ctrl.buildTSTorrentResponse(meta, to)

		assert.Equal(t, tsStatWorking, resp.Stat)
		assert.Equal(t, "Torrent working", resp.StatString)
		assert.Equal(t, to.Length(), resp.TorrentSize)
		assert.Equal(t, to.BytesCompleted(), resp.LoadedSize)
		assert.GreaterOrEqual(t, resp.DownloadSpeed, float64(0))
		assert.GreaterOrEqual(t, resp.UploadSpeed, float64(0))
	})

	t.Run("active torrent preloading", func(t *testing.T) {
		sintelFile, err := os.Open(sintelTorrentFile)
		require.NoError(t, err)
		metaInfo, err := metainfo.Load(sintelFile)
		_ = sintelFile.Close()
		require.NoError(t, err)

		to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
		require.NoError(t, err)
		<-to.GotInfo()

		storageTorrent, err := ctrl.storageClient.OpenTorrent(context.Background(), to.Info(), to.InfoHash())
		require.NoError(t, err)
		piece := storageTorrent.Piece(to.Piece(0).Info())
		_, err = piece.WriteAt(make([]byte, 1024), 0)
		require.NoError(t, err)
		storageStats, err := ctrl.storageClient.TorrentStats(to.InfoHash())
		require.NoError(t, err)

		preload := &preloadTask{targetBytes: 1048576}
		preload.bytesRead.Store(storageStats.WrittenBytes + 512)
		ctrl.preloads.Store(to.InfoHash(), preload)
		defer ctrl.preloads.Delete(to.InfoHash())

		resp := ctrl.buildTSTorrentResponse(torrentToMetadata(to), to)
		assert.Equal(t, tsStatPreload, resp.Stat)
		assert.Equal(t, "Torrent preload", resp.StatString)
		assert.Equal(t, int64(1048576), resp.PreloadSize)
		assert.Positive(t, storageStats.WrittenBytes)
		assert.Equal(t, storageStats.WrittenBytes+512, resp.PreloadedBytes)
	})
}

func TestTorrServerPreload(t *testing.T) {
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

	t.Run("starts preload on specific file and updates status", func(t *testing.T) {
		fileIdx := 1
		ctrl.startPreloadByFileIndex(to, &fileIdx)

		val, preloading := ctrl.preloads.Load(to.InfoHash())
		require.True(t, preloading)
		preload, ok := val.(*preloadTask)
		require.True(t, ok)
		assert.Positive(t, preload.targetBytes)

		resp := ctrl.buildTSTorrentResponse(torrentToMetadata(to), to)
		assert.Equal(t, tsStatPreload, resp.Stat)
		assert.Equal(t, "Torrent preload", resp.StatString)
		assert.Equal(t, preload.targetBytes, resp.PreloadSize)

		ctrl.cancelPreload(to.InfoHash())
		_, preloading = ctrl.preloads.Load(to.InfoHash())
		assert.False(t, preloading)
	})

	t.Run("playback cancels active preload", func(t *testing.T) {
		fileIdx := 1
		ctrl.startPreloadByFileIndex(to, &fileIdx)
		_, preloading := ctrl.preloads.Load(to.InfoHash())
		require.True(t, preloading)

		ctrl.cancelPreload(to.InfoHash())
		_, preloading = ctrl.preloads.Load(to.InfoHash())
		assert.False(t, preloading)
	})

	t.Run("invalid playback keeps active preload", func(t *testing.T) {
		fileIdx := 1
		ctrl.startPreloadByFileIndex(to, &fileIdx)
		_, preloading := ctrl.preloads.Load(to.InfoHash())
		require.True(t, preloading)

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/stream/invalid", http.NoBody)
		ctrl.streamFile(recorder, request, to.InfoHash(), len(to.Files()))

		assert.Equal(t, http.StatusBadRequest, recorder.Code)
		_, preloading = ctrl.preloads.Load(to.InfoHash())
		assert.True(t, preloading)
		ctrl.cancelPreload(to.InfoHash())
	})

	t.Run("stream endpoint with preload and stat returns preloading response", func(t *testing.T) {
		server := httptest.NewServer(ctrl.router)
		defer server.Close()

		streamURL := fmt.Sprintf("%s/stream/Sintel.mp4?link=%s&preload&stat&index=1", server.URL, to.InfoHash().HexString())
		resp, err := http.Get(streamURL)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var tsResp api.TSTorrentResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&tsResp))
		assert.Equal(t, tsStatPreload, tsResp.Stat)
		assert.Equal(t, "Torrent preload", tsResp.StatString)
		assert.Positive(t, tsResp.PreloadSize)

		ctrl.cancelPreload(to.InfoHash())
	})

	t.Run("stream endpoint with play and nil index does not panic", func(t *testing.T) {
		server := httptest.NewServer(ctrl.router)
		defer server.Close()

		streamURL := fmt.Sprintf("%s/stream/Sintel.mp4?link=%s&play", server.URL, to.InfoHash().HexString())
		req, err := http.NewRequest(http.MethodGet, streamURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Range", "bytes=0-10")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Contains(t, []int{http.StatusOK, http.StatusPartialContent}, resp.StatusCode)
	})

	t.Run("preload on file where budget exceeds length preloads entire file", func(t *testing.T) {
		// Temporarily increase MaxMemory so preload budget covers the whole file.
		origMem := ctrl.settings.MaxMemory
		ctrl.settings.MaxMemory = utils.Ptr(int64(500 << 20))
		defer func() { ctrl.settings.MaxMemory = origMem }()

		fileIdx := 1
		file := to.Files()[0]
		ctrl.startPreloadByFileIndex(to, &fileIdx)

		val, preloading := ctrl.preloads.Load(to.InfoHash())
		require.True(t, preloading)
		preload, ok := val.(*preloadTask)
		require.True(t, ok)
		assert.Equal(t, file.Length(), preload.targetBytes)

		ctrl.cancelPreload(to.InfoHash())
	})
}

func TestTSPieceInfoFromStateUsesCompletionValue(t *testing.T) {
	state := torrent.PieceState{
		Completion: torrentstorage.Completion{Ok: true, Complete: false},
	}

	piece := tsPieceInfoFromState(3, 1024, state)
	assert.False(t, piece.Completed)
	assert.Zero(t, piece.Size)
}

func TestTSCache(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	t.Run("missing or invalid action returns 400", func(t *testing.T) {
		for _, reqBody := range []string{
			`{"hash":"1111111111111111111111111111111111111111"}`,
			`{"action":"set","hash":"1111111111111111111111111111111111111111"}`,
		} {
			resp, err := http.Post(server.URL+"/cache", "application/json", strings.NewReader(reqBody))
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}
	})

	t.Run("torrent not found returns 404", func(t *testing.T) {
		reqBody := `{"action":"get","hash":"1111111111111111111111111111111111111111"}`
		resp, err := http.Post(server.URL+"/cache", "application/json", strings.NewReader(reqBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("torrent in database without pieces returns empty 200", func(t *testing.T) {
		ih := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")
		name := "Test Torrent"
		err := ctrl.db.CreateTorrent(&database.Torrent{
			Torrent: api.Torrent{
				Hash:       ih,
				Name:       name,
				Title:      &name,
				Storage:    utils.Ptr(api.Memory),
				TotalSize:  1048576,
				PieceCount: 1,
			},
		})
		require.NoError(t, err)

		reqBody := `{"action":"get","hash":"2222222222222222222222222222222222222222"}`
		resp, err := http.Post(server.URL+"/cache", "application/json", strings.NewReader(reqBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var bodyMap map[string]any
		err = json.NewDecoder(resp.Body).Decode(&bodyMap)
		require.NoError(t, err)
		assert.Empty(t, bodyMap)
	})

	t.Run("active torrent cache response structure", func(t *testing.T) {
		ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

		sintelFile, err := os.Open(sintelTorrentFile)
		require.NoError(t, err)
		metaInfo, err := metainfo.Load(sintelFile)
		_ = sintelFile.Close()
		require.NoError(t, err)

		specTorrent, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
		require.NoError(t, err)
		<-specTorrent.GotInfo()

		// Start streaming to activate cache and readers.
		streamURL := fmt.Sprintf("%s/stream/Sintel.mp4?link=%s&play&index=6", server.URL, ih.HexString())
		streamReq, err := http.NewRequest(http.MethodGet, streamURL, http.NoBody)
		require.NoError(t, err)
		streamReq.Header.Set("Range", "bytes=0-")

		streamResp, err := http.DefaultClient.Do(streamReq)
		require.NoError(t, err)
		defer streamResp.Body.Close()
		require.Equal(t, http.StatusPartialContent, streamResp.StatusCode)

		buf := make([]byte, 1024)
		n, err := io.ReadFull(streamResp.Body, buf)
		require.NoError(t, err)
		assert.Equal(t, 1024, n)

		to, ok := ctrl.client.Torrent(ih)
		require.True(t, ok)

		storageStats, err := ctrl.storageClient.TorrentStats(ih)
		require.NoError(t, err)
		require.NotEmpty(t, storageStats.Pieces)

		reqBody := fmt.Sprintf(`{"action":"get","hash":%q}`, ih.HexString())
		resp, err := http.Post(server.URL+"/cache", "application/json", strings.NewReader(reqBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var rawMap map[string]json.RawMessage
		bodyBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		err = json.Unmarshal(bodyBytes, &rawMap)
		require.NoError(t, err)

		// Verify TorrServer uppercase keys exist in JSON.
		assert.Contains(t, rawMap, "Capacity")
		assert.Contains(t, rawMap, "Filled")
		assert.Contains(t, rawMap, "Hash")
		assert.Contains(t, rawMap, "Pieces")
		assert.Contains(t, rawMap, "PiecesCount")
		assert.Contains(t, rawMap, "PiecesLength")
		assert.Contains(t, rawMap, "Readers")
		assert.Contains(t, rawMap, "Torrent")

		var cacheResp api.TSCacheResponse
		err = json.Unmarshal(bodyBytes, &cacheResp)
		require.NoError(t, err)

		assert.Equal(t, ih.HexString(), cacheResp.Hash)
		assert.Positive(t, cacheResp.Capacity)
		assert.Equal(t, to.NumPieces(), cacheResp.PiecesCount)
		assert.NotEmpty(t, cacheResp.Pieces)
		assert.NotEmpty(t, cacheResp.Readers)

		// Check first piece has Size, Id, Length.
		firstPiece := cacheResp.Pieces[strconv.Itoa(storageStats.Pieces[0].Index)]
		assert.Equal(t, storageStats.Pieces[0].Index, firstPiece.ID)
		assert.Equal(t, storageStats.Pieces[0].SizeBytes, firstPiece.Length)
		assert.LessOrEqual(t, firstPiece.Size, firstPiece.Length)

		var filled int64
		for _, piece := range cacheResp.Pieces {
			filled += piece.Size
		}
		assert.Equal(t, filled, cacheResp.Filled)

		// Check reader has capitalized Reader, Start, End properties.
		var rawReaders []map[string]any
		err = json.Unmarshal(rawMap["Readers"], &rawReaders)
		require.NoError(t, err)
		require.NotEmpty(t, rawReaders)
		assert.Contains(t, rawReaders[0], "Reader")
		assert.Contains(t, rawReaders[0], "Start")
		assert.Contains(t, rawReaders[0], "End")

		// Check embedded Torrent status.
		require.NotNil(t, cacheResp.Torrent)
		assert.Equal(t, tsStatWorking, cacheResp.Torrent.Stat)
		assert.Equal(t, "Torrent working", cacheResp.Torrent.StatString)
	})
}

func TestTSCacheFileStorage(t *testing.T) {
	t.Run("file storage torrent not yet active returns empty 200", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl, cleanup := newTestController(t, func(c *Controller) {
			c.settings.FileStoragePath = &tmpDir
		})
		defer cleanup()

		server := httptest.NewServer(ctrl.router)
		defer server.Close()

		ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
		name := "Sintel"
		err := ctrl.db.CreateTorrent(&database.Torrent{
			Torrent: api.Torrent{
				Hash:    ih,
				Name:    name,
				Storage: utils.Ptr(api.File),
			},
		})
		require.NoError(t, err)

		reqBody := fmt.Sprintf(`{"action":"get","hash":%q}`, ih.HexString())
		resp, err := http.Post(server.URL+"/cache", "application/json", strings.NewReader(reqBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var bodyMap map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&bodyMap))
		assert.Empty(t, bodyMap)
	})

	t.Run("active file storage torrent reports full piece state", func(t *testing.T) {
		tmpDir := t.TempDir()
		ctrl, cleanup := newTestController(t, func(c *Controller) {
			c.settings.FileStoragePath = &tmpDir
		})
		defer cleanup()

		server := httptest.NewServer(ctrl.router)
		defer server.Close()

		ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

		// Upload sintel.torrent with storage=file so InfoBytes are persisted and
		// GotInfo resolves instantly when the stream handler calls loadTorrent.
		uploadBody, uploadWriter := createMultipartForm(t, map[string]string{
			"storage": string(api.File),
		})
		uploadReq := httptest.NewRequest(http.MethodPost, "/api/v1/torrents", uploadBody)
		uploadReq.Header.Set("Content-Type", uploadWriter.FormDataContentType())
		uploadRec := httptest.NewRecorder()
		ctrl.router.ServeHTTP(uploadRec, uploadReq)
		require.Equal(t, http.StatusCreated, uploadRec.Code)

		// Activate the file-backed torrent without starting downloads. Completion
		// state is immediately known from the persisted metainfo.
		dbTorrent, err := ctrl.db.GetTorrent(ih)
		require.NoError(t, err)
		to, err := ctrl.loadTorrentSpec(&torrent.TorrentSpec{
			AddTorrentOpts: torrent.AddTorrentOpts{
				InfoHash:  ih,
				InfoBytes: dbTorrent.InfoBytes,
			},
		}, api.File)
		require.NoError(t, err)
		<-to.GotInfo()

		to, ok := ctrl.client.Torrent(ih)
		require.True(t, ok)
		require.NotNil(t, to.Info(), "torrent info must be available after streaming")

		reqBody := fmt.Sprintf(`{"action":"get","hash":%q}`, ih.HexString())
		resp, err := http.Post(server.URL+"/cache", "application/json", strings.NewReader(reqBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		bodyBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var rawMap map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(bodyBytes, &rawMap))

		// TorrServer-compatible uppercase keys must be present.
		assert.Contains(t, rawMap, "Capacity")
		assert.Contains(t, rawMap, "Filled")
		assert.Contains(t, rawMap, "Hash")
		assert.Contains(t, rawMap, "Pieces")
		assert.Contains(t, rawMap, "PiecesCount")
		assert.Contains(t, rawMap, "PiecesLength")
		assert.Contains(t, rawMap, "Readers")
		assert.Contains(t, rawMap, "Torrent")

		var cacheResp api.TSCacheResponse
		require.NoError(t, json.Unmarshal(bodyBytes, &cacheResp))

		assert.Equal(t, ih.HexString(), cacheResp.Hash)
		// Capacity == total torrent length for file storage.
		assert.Equal(t, to.Length(), cacheResp.Capacity)
		assert.Equal(t, to.NumPieces(), cacheResp.PiecesCount)
		assert.Equal(t, to.Info().PieceLength, cacheResp.PiecesLength)
		assert.Len(t, cacheResp.Pieces, to.NumPieces())

		var filled int64
		// Completion must use PieceState.Complete, not Completion.Ok.
		for i := range to.NumPieces() {
			p, ok := cacheResp.Pieces[strconv.Itoa(i)]
			require.True(t, ok, "piece %d missing from response", i)
			assert.Equal(t, i, p.ID)
			assert.Positive(t, p.Length)
			assert.Equal(t, to.PieceState(i).Complete, p.Completed)
			if p.Completed {
				assert.Equal(t, p.Length, p.Size)
			} else {
				assert.Zero(t, p.Size)
			}
			filled += p.Size
		}
		assert.Equal(t, filled, cacheResp.Filled)

		require.NotNil(t, cacheResp.Torrent)
		assert.Empty(t, cacheResp.Readers)
	})
}
