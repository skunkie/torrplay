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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
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
			req := httptest.NewRequest(tc.method, tc.url, nil)
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
		for i := 0; i < goroutines; i++ {
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
	addReq := map[string]interface{}{
		"action":     "add",
		"link":       magnet,
		"title":      "Sintel Test",
		"save_to_db": true,
	}

	rr := testutil.NewRequest().Post("/torrents").
		WithJsonBody(addReq).
		GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	getRR := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih.HexString()))
	require.Equal(t, http.StatusOK, getRR.Code)

	var fetchedTorrent api.Torrent
	require.NoError(t, json.NewDecoder(getRR.Body).Decode(&fetchedTorrent))
	assert.NotEmpty(t, fetchedTorrent.Magnet)
	assert.Contains(t, fetchedTorrent.Magnet, ih.HexString())

	// 2. Add via /torrents with hash field
	ih2 := metainfo.NewHashFromHex("dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c")
	addHashReq := map[string]interface{}{
		"action":     "add",
		"hash":       ih2.HexString(),
		"title":      "Bunny Test",
		"save_to_db": true,
	}

	rr2 := testutil.NewRequest().Post("/torrents").
		WithJsonBody(addHashReq).
		GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr2.Code)

	getRR2 := doGet(t, ctrl.router, fmt.Sprintf("/api/v1/torrents/%s", ih2.HexString()))
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

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	// 1. Stream the torrent before adding to database.
	streamURL := fmt.Sprintf("%s/stream/Sintel.mp4?link=%s&play&index=6", server.URL, ih.HexString())
	req, err := http.NewRequest(http.MethodGet, streamURL, nil)
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
		Title:    utils.Ptr("Sintel Test"),
		SaveToDB: utils.Ptr(true),
	}
	addBody, err := json.Marshal(addReq)
	require.NoError(t, err)
	addHTTPReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/torrents", server.URL), bytes.NewBuffer(addBody))
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
