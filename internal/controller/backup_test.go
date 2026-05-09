// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
)

func TestBackupAndRestore(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	// Add some torrents to backup.
	addAllSampleTorrents(t, ctrl.router)

	// Get the backup.
	rr := testutil.NewRequest().Get("/api/v1/torrents/backup").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var backupBytes bytes.Buffer
	_, err := io.Copy(&backupBytes, rr.Body)
	require.NoError(t, err)

	// Create a new controller to restore the backup to.
	ctrl2, cleanup2 := newTestController(t)
	defer cleanup2()

	// Restore the backup.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "torrplay.backup")
	require.NoError(t, err)
	_, err = part.Write(backupBytes.Bytes())
	require.NoError(t, err)
	writer.Close()

	req, err := http.NewRequest("POST", "/api/v1/torrents/restore", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr = httptest.NewRecorder()
	ctrl2.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Check that the torrents were restored.
	rr = testutil.NewRequest().Get("/api/v1/torrents").GoWithHTTPHandler(t, ctrl2.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var list api.ListTorrents
	err = json.NewDecoder(rr.Body).Decode(&list)
	require.NoError(t, err)

	assert.Equal(t, len(samples), list.Total)
}

func TestRestoreInvalidBackup(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "invalid-backup.json")
	require.NoError(t, err)
	_, err = part.Write([]byte("invalid backup data"))
	require.NoError(t, err)
	writer.Close()

	req, err := http.NewRequest("POST", "/api/v1/torrents/restore", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestBackupAndRestoreWithPosters(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	magnet := samples[ih]
	req := api.TorrentAdd{Magnet: &magnet, Poster: &posterURL}
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(req).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)

	require.Eventually(t, func() bool {
		rr := testutil.NewRequest().Get("/api/v1/torrents/"+ih.HexString()).GoWithHTTPHandler(t, ctrl.router).Recorder

		var torrent api.Torrent
		if err := json.NewDecoder(rr.Body).Decode(&torrent); err != nil {
			return false
		}
		return torrent.Poster != nil
	}, 5*time.Second, 100*time.Millisecond)

	// Get the backup.
	rr = testutil.NewRequest().Get("/api/v1/torrents/backup").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var backupBytes bytes.Buffer
	_, err := io.Copy(&backupBytes, rr.Body)
	require.NoError(t, err)

	// Create a new controller to restore the backup to.
	ctrl2, cleanup2 := newTestController(t)
	defer cleanup2()

	// Restore the backup.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "torrplay.backup")
	require.NoError(t, err)
	_, err = part.Write(backupBytes.Bytes())
	require.NoError(t, err)
	writer.Close()

	restoreReq, err := http.NewRequest("POST", "/api/v1/torrents/restore", &body)
	require.NoError(t, err)
	restoreReq.Header.Set("Content-Type", writer.FormDataContentType())

	rr = httptest.NewRecorder()
	ctrl2.router.ServeHTTP(rr, restoreReq)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Check that the torrent was restored with the poster.
	rr = testutil.NewRequest().Get("/api/v1/torrents/"+ih.HexString()).GoWithHTTPHandler(t, ctrl2.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)

	var torrent api.Torrent
	err = json.NewDecoder(rr.Body).Decode(&torrent)
	require.NoError(t, err)
	assert.NotNil(t, torrent.Poster)
}
