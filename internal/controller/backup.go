// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
)

type backup struct {
	Posters  map[string][]byte `json:"posters"`
	Torrents []*api.Torrent    `json:"torrents"`
}

func (c *Controller) BackupTorrents(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ts, err := c.db.GetTorrents()
	if err != nil {
		api.HandleError(w, err)
		return
	}

	postersData := make(map[string][]byte)
	for _, t := range ts {
		if t.Poster != nil {
			p, err := c.images.Get(*t.Poster)
			if err != nil {
				c.logger.Error("failed to get poster for backup", "err", err, "posterID", *t.Poster)
				continue
			}
			postersData[*t.Poster] = p
		}
	}

	backupData := backup{
		Posters:  postersData,
		Torrents: ts,
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\"torrplay.backup\"")
	if err := json.NewEncoder(w).Encode(backupData); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) RestoreTorrents(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		api.HTTPError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var backupData backup

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusBadRequest)
			return
		}

		if p.FormName() == "file" {
			if err := json.NewDecoder(p).Decode(&backupData); err != nil {
				api.HTTPError(w, "invalid backup file format", http.StatusBadRequest)
				return
			}
			break
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, posterBytes := range backupData.Posters {
		if _, err := c.images.SaveData(posterBytes); err != nil {
			c.logger.Error("failed to restore poster", "err", err)
		}
	}

	for _, t := range backupData.Torrents {
		if err := c.db.CreateTorrent(t); err != nil {
			if err == database.ErrTorrentExists {
				if err := c.db.UpdateTorrent(t); err != nil {
					c.logger.Error("failed to update torrent on restore", "err", err, "hash", t.Hash.HexString())
				}
			} else {
				c.logger.Error("failed to restore torrent", "err", err, "hash", t.Hash.HexString())
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
