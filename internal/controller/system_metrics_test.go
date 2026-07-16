// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
)

func TestGetSystemMetrics(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	rr := doGet(t, ctrl.router, "/api/system/metrics")
	require.Equal(t, http.StatusOK, rr.Code)

	var metrics api.SystemMetrics
	err := json.NewDecoder(rr.Body).Decode(&metrics)
	require.NoError(t, err)

	assert.Equal(t, 0, metrics.ActiveTorrents)
	assert.Equal(t, int64(0), metrics.DownloadSpeed)
	assert.Equal(t, int64(0), metrics.UploadSpeed)

	for _, magnet := range samples {
		torrent, err := ctrl.client.AddMagnet(magnet)
		require.NoError(t, err)

		select {
		case <-torrent.GotInfo():
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for GotInfo on %s", magnet)
		}

		torrent.DownloadAll()
	}

	time.Sleep(1 * time.Second)

	rr = doGet(t, ctrl.router, "/api/system/metrics")
	require.Equal(t, http.StatusOK, rr.Code)
	err = json.NewDecoder(rr.Body).Decode(&metrics)
	require.NoError(t, err)

	assert.Equal(t, len(samples), metrics.ActiveTorrents)
	assert.GreaterOrEqual(t, metrics.DownloadSpeed, int64(0))
	assert.GreaterOrEqual(t, metrics.UploadSpeed, int64(0))

	time.Sleep(3 * time.Second)

	rr = doGet(t, ctrl.router, "/api/system/metrics")
	require.Equal(t, http.StatusOK, rr.Code)
	err = json.NewDecoder(rr.Body).Decode(&metrics)
	require.NoError(t, err)

	assert.Equal(t, len(samples), metrics.ActiveTorrents)
	assert.GreaterOrEqual(t, metrics.DownloadSpeed, int64(0))
	assert.GreaterOrEqual(t, metrics.UploadSpeed, int64(0))
}
