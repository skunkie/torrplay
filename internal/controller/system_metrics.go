// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"net/http"

	"github.com/torrplay/torrplay/internal/api"
)

func (c *Controller) GetSystemMetrics(w http.ResponseWriter, _ *http.Request) {
	var downloadSpeed, uploadSpeed int64
	if c.speedMonitor != nil {
		downloadSpeed, uploadSpeed = c.speedMonitor.Speeds()
	}

	var activeTorrents int
	if client := c.currentClient(); client != nil {
		activeTorrents = len(client.Torrents())
	}

	metrics := api.SystemMetrics{
		ActiveTorrents: activeTorrents,
		DownloadSpeed:  downloadSpeed,
		UploadSpeed:    uploadSpeed,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}
