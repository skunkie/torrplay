// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
)

func TestGetSystemMetrics(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	// Initial metrics with 0 torrents.
	rr := doGet(t, ctrl.router, "/api/system/metrics")
	require.Equal(t, http.StatusOK, rr.Code)

	var metrics api.SystemMetrics
	err := json.NewDecoder(rr.Body).Decode(&metrics)
	require.NoError(t, err)

	assert.Equal(t, 0, metrics.ActiveTorrents)
	assert.Equal(t, int64(0), metrics.DownloadSpeed)
	assert.Equal(t, int64(0), metrics.UploadSpeed)

	// Add sample torrents without blocking on external network DHT resolution.
	for _, magnet := range samples {
		_, err := ctrl.client.AddMagnet(magnet)
		require.NoError(t, err)
	}

	// Verify active torrent count updates immediately.
	rr = doGet(t, ctrl.router, "/api/system/metrics")
	require.Equal(t, http.StatusOK, rr.Code)
	err = json.NewDecoder(rr.Body).Decode(&metrics)
	require.NoError(t, err)

	assert.Equal(t, len(samples), metrics.ActiveTorrents)
	assert.GreaterOrEqual(t, metrics.DownloadSpeed, int64(0))
	assert.GreaterOrEqual(t, metrics.UploadSpeed, int64(0))

	// Rapid consecutive calls return consistent values without mutating state.
	for range 3 {
		rr = doGet(t, ctrl.router, "/api/system/metrics")
		require.Equal(t, http.StatusOK, rr.Code)
		var m api.SystemMetrics
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&m))
		assert.Equal(t, len(samples), m.ActiveTorrents)
		assert.GreaterOrEqual(t, m.DownloadSpeed, int64(0))
		assert.GreaterOrEqual(t, m.UploadSpeed, int64(0))
	}
}
