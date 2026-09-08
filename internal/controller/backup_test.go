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
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

func restoreBackup(t *testing.T, ctrl *Controller, data backup) {
	t.Helper()

	var backupBytes bytes.Buffer
	require.NoError(t, json.NewEncoder(&backupBytes).Encode(data))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "torrplay.backup")
	require.NoError(t, err)
	_, err = part.Write(backupBytes.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/restore", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestBackupAndRestore(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	// Add some torrents to backup.
	addAllSampleTorrents(t, ctrl)

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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/restore", &body)
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/restore", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rr := httptest.NewRecorder()
	ctrl.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRestoreTorrentsPreservesTargetStorageChoices(t *testing.T) {
	ctrl, cleanup := newTestController(t, func(c *Controller) {
		c.settings.FileStoragePath = nil
	})
	defer cleanup()

	fileHash := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	memoryHash := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")
	newHash := metainfo.NewHashFromHex("3333333333333333333333333333333333333333")
	fileStorage := api.File
	memoryStorage := api.Memory
	fileInfoBytes := []byte("existing file metadata")

	require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{
		Torrent: api.Torrent{
			Hash: fileHash, Magnet: utils.MagnetURIFromHash(fileHash), Name: "old file name",
			Files: []api.TorrentFile{}, Storage: &fileStorage, TotalSize: 1,
		},
		InfoBytes: fileInfoBytes,
	}))
	require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{
		Torrent: api.Torrent{
			Hash: memoryHash, Magnet: utils.MagnetURIFromHash(memoryHash), Name: "old memory name",
			Files: []api.TorrentFile{}, Storage: &memoryStorage, TotalSize: 2,
		},
	}))

	category := "restored"
	restoreBackup(t, ctrl, backup{
		Posters: map[string][]byte{},
		Torrents: []*api.Torrent{
			{
				Hash: fileHash, Magnet: utils.MagnetURIFromHash(fileHash), Name: "updated file name",
				Category: &category, Files: []api.TorrentFile{}, Storage: &memoryStorage, TotalSize: 10,
			},
			{
				Hash: memoryHash, Magnet: utils.MagnetURIFromHash(memoryHash), Name: "updated memory name",
				Category: &category, Files: []api.TorrentFile{}, Storage: &fileStorage, TotalSize: 20,
			},
			{
				Hash: newHash, Magnet: utils.MagnetURIFromHash(newHash), Name: "new torrent",
				Category: &category, Files: []api.TorrentFile{}, Storage: &fileStorage, TotalSize: 30,
			},
		},
	})

	restoredFile, err := ctrl.db.GetTorrent(fileHash)
	require.NoError(t, err)
	require.Equal(t, api.File, utils.Val(restoredFile.Storage))
	assert.Equal(t, fileInfoBytes, restoredFile.InfoBytes)
	assert.Equal(t, "updated file name", restoredFile.Name)
	assert.Equal(t, int64(10), restoredFile.TotalSize)
	assert.Equal(t, category, utils.Val(restoredFile.Category))

	restoredMemory, err := ctrl.db.GetTorrent(memoryHash)
	require.NoError(t, err)
	require.Equal(t, api.Memory, utils.Val(restoredMemory.Storage))
	assert.Empty(t, restoredMemory.InfoBytes)
	assert.Equal(t, "updated memory name", restoredMemory.Name)
	assert.Equal(t, int64(20), restoredMemory.TotalSize)

	restoredNew, err := ctrl.db.GetTorrent(newHash)
	require.NoError(t, err)
	require.Equal(t, api.Memory, utils.Val(restoredNew.Storage))
	assert.Empty(t, restoredNew.InfoBytes)
	assert.Equal(t, "new torrent", restoredNew.Name)
	assert.Equal(t, int64(30), restoredNew.TotalSize)
}

func TestBackupExcludesInfoBytes(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	ih := metainfo.NewHashFromHex("4444444444444444444444444444444444444444")
	require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{
		Torrent: api.Torrent{
			Hash: ih, Magnet: utils.MagnetURIFromHash(ih), Name: "file torrent",
			Files: []api.TorrentFile{}, Storage: utils.Ptr(api.File),
		},
		InfoBytes: []byte("private metadata"),
	}))

	rr := testutil.NewRequest().Get("/api/v1/torrents/backup").GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "info_bytes")
}

func TestBackupAndRestoreWithPosters(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	posterURL := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	primeSampleMetadata(t, ctrl, ih)
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

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/v1/torrents/restore", &body)
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
