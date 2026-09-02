// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

func (c *Controller) QBittorrentAddTorrent(w http.ResponseWriter, r *http.Request) {
	if err := parseMultipartForm(w, r); err != nil {
		api.HTTPError(w, fmt.Sprintf("failed to parse multipart form: %v", err), http.StatusBadRequest)
		return
	}

	category := r.FormValue("category")

	// Handle torrent URLs.
	if urls := r.FormValue("urls"); urls != "" {
		magnets := strings.SplitSeq(urls, "\n")
		for magnet := range magnets {
			magnet = strings.TrimSpace(magnet)
			if magnet == "" {
				continue
			}

			to, err := c.addTorrentByMagnet(magnet)
			if err != nil {
				api.HandleError(w, err)
				return
			}

			select {
			case <-to.GotInfo():
			case <-time.After(gotInfoTimeout):
				to.Drop()
				<-to.Closed()
				api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
				return
			}

			req := api.TorrentAdd{
				Magnet:  &magnet,
				Storage: utils.Ptr(api.File),
			}
			if category != "" {
				req.Category = &category
			}

			c.mu.Lock()
			_, err = c.createTorrentInDBLocked(to, req)
			c.mu.Unlock()

			if !c.hasTorrentReaders(to.InfoHash()) {
				to.Drop()
				<-to.Closed()
			}

			if err != nil {
				var apiErr api.Error
				if !errors.As(err, &apiErr) || apiErr.Code != http.StatusConflict {
					api.HandleError(w, err)
					return
				}
			}
		}

		_, _ = w.Write([]byte("Ok."))
		return
	}

	// Handle torrent files.
	if r.MultipartForm != nil && r.MultipartForm.File != nil {
		if files, ok := r.MultipartForm.File["torrents"]; ok {
			for _, fileHeader := range files {
				file, err := fileHeader.Open()
				if err != nil {
					api.HTTPError(w, fmt.Sprintf("failed to open torrent file: %v", err), http.StatusInternalServerError)
					return
				}

				meta, err := metainfo.Load(file)
				file.Close()
				if err != nil {
					api.HTTPError(w, fmt.Sprintf("invalid torrent file: %v", err), http.StatusUnsupportedMediaType)
					return
				}

				magnet, err := meta.MagnetV2()
				if err != nil {
					api.HTTPError(w, fmt.Sprintf("failed to create magnet link: %v", err), http.StatusInternalServerError)
					return
				}
				magnetStr := magnet.String()

				to, err := c.addTorrentByMagnet(magnetStr)
				if err != nil {
					api.HandleError(w, err)
					return
				}

				select {
				case <-to.GotInfo():
				case <-time.After(gotInfoTimeout):
					to.Drop()
					<-to.Closed()
					api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
					return
				}

				req := api.TorrentAdd{
					Magnet:  &magnetStr,
					Storage: utils.Ptr(api.File),
				}
				if category != "" {
					req.Category = &category
				}

				c.mu.Lock()
				_, err = c.createTorrentInDBLocked(to, req)
				c.mu.Unlock()

				if !c.hasTorrentReaders(to.InfoHash()) {
					to.Drop()
					<-to.Closed()
				}

				if err != nil {
					var apiErr api.Error
					if !errors.As(err, &apiErr) || apiErr.Code != http.StatusConflict {
						api.HandleError(w, err)
						return
					}
				}
			}

			_, _ = w.Write([]byte("Ok."))
			return
		}
	}

	api.HTTPError(w, "no torrents or urls provided", http.StatusBadRequest)
}
