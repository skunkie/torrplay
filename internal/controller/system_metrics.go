// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/torrplay/torrplay/internal/api"
)

func (c *Controller) GetSystemMetrics(w http.ResponseWriter, r *http.Request) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()

	torrents := c.client.Torrents()

	var totalDownloadBytes int64
	var totalUploadBytes int64

	for _, t := range torrents {
		stats := t.Stats()
		totalDownloadBytes += stats.BytesReadData.Int64()
		totalUploadBytes += stats.BytesWrittenData.Int64()
	}

	now := time.Now()
	downloadSpeed := int64(0)
	uploadSpeed := int64(0)

	if !c.lastMetricsTime.IsZero() {
		elapsed := now.Sub(c.lastMetricsTime).Seconds()
		if elapsed > 0.05 {
			downloadSpeed = int64(float64(totalDownloadBytes-c.lastTotalDownload) / elapsed)
			uploadSpeed = int64(float64(totalUploadBytes-c.lastTotalUpload) / elapsed)
		}
	}

	c.lastMetricsTime = now
	c.lastTotalDownload = totalDownloadBytes
	c.lastTotalUpload = totalUploadBytes

	metrics := api.SystemMetrics{
		ActiveTorrents: len(torrents),
		DownloadSpeed:  downloadSpeed,
		UploadSpeed:    uploadSpeed,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}
