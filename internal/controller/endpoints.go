// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	gotorrentfs "github.com/ajnavarro/go-torrent-fs"
	"github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/auth"
	"github.com/torrplay/torrplay/internal/buildinfo"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/dlna"
	"github.com/torrplay/torrplay/internal/downloader"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/logging"
	"github.com/torrplay/torrplay/internal/piececompletion"
	"github.com/torrplay/torrplay/internal/utils"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
)

func (c *Controller) AddTorrent(w http.ResponseWriter, r *http.Request) {
	var req api.TorrentAdd

	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")

	switch {
	case strings.HasPrefix(contentType, "application/json"):
		// Handle JSON request (magnet or info hash).
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to decode JSON request: %v", err), http.StatusBadRequest)
			return
		}

		// Validate that we have either magnet or info hash.
		if req.Magnet == nil && req.Hash == nil {
			api.HTTPError(w, "either magnet or hash must be provided", http.StatusBadRequest)
			return
		}

	case strings.HasPrefix(contentType, "multipart/form-data"):
		err := r.ParseMultipartForm(multipartFormMaxMemory)
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to parse multipart form: %v", err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to get file: %v", err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		if !strings.HasSuffix(strings.ToLower(header.Filename), ".torrent") {
			api.HTTPError(w, "file must be a .torrent file", http.StatusBadRequest)
			return
		}

		meta, err := metainfo.Load(file)
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("invalid torrent file: %v", err), http.StatusBadRequest)
			return
		}

		magnetV2, err := meta.MagnetV2()
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to create magnet link: %v", err), http.StatusInternalServerError)
			return
		}

		category := r.FormValue("category")
		title := r.FormValue("title")
		poster := r.FormValue("poster")
		storage := r.FormValue("storage")

		// If no title provided, try to get it from the torrent.
		if title == "" {
			info, err := meta.UnmarshalInfo()
			if err == nil && info.Name != "" {
				title = info.Name
			}
		}

		if storage == "" {
			storage = string(api.Memory)
		}

		req = api.TorrentAdd{
			Category: &category,
			Magnet:   utils.Ptr(magnetV2.String()),
			Poster:   &poster,
			Storage:  (*api.TorrentStorage)(utils.Ptr(storage)),
			Title:    &title,
		}

	case contentType == "application/octet-stream":
		contentDisposition := r.Header.Get("Content-Disposition")
		var filename string
		if contentDisposition != "" {
			// Parse filename from Content-Disposition header.
			// Format: attachment; filename="file.torrent"
			re := regexp.MustCompile(`filename="([^"]+)"`)
			matches := re.FindStringSubmatch(contentDisposition)
			if len(matches) > 1 {
				filename = matches[1]
			}
		}

		if filename != "" && !strings.HasSuffix(strings.ToLower(filename), ".torrent") {
			api.HTTPError(w, "filename must end with .torrent", http.StatusBadRequest)
			return
		}

		meta, err := metainfo.Load(r.Body)
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("invalid torrent file: %v", err), http.StatusBadRequest)
			return
		}

		magnetV2, err := meta.MagnetV2()
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to create magnet link: %v", err), http.StatusInternalServerError)
			return
		}

		var title string
		info, err := meta.UnmarshalInfo()
		if err == nil && info.Name != "" {
			title = info.Name
		}

		// There is no poster for octet-stream.
		req = api.TorrentAdd{
			Magnet:  utils.Ptr(magnetV2.String()),
			Storage: utils.Ptr(api.Memory),
			Title:   &title,
		}

	default:
		api.HTTPError(w,
			fmt.Sprintf("unsupported content type: %s. Use application/json, multipart/form-data, or application/octet-stream", contentType),
			http.StatusUnsupportedMediaType,
		)
		return
	}

	switch {
	case req.Hash != nil:
		req.Magnet = utils.Ptr(magnetURIfromHash(*req.Hash))
		fallthrough
	case req.Magnet != nil:
	default:
		api.HTTPError(w, "invalid hash or magnet link", http.StatusBadRequest)
		return
	}

	if utils.Val(req.Storage) == api.File && (c.settings.FileStoragePath == nil || *c.settings.FileStoragePath == "") {
		api.HTTPError(w, "file storage is not configured", http.StatusBadRequest)
		return
	}

	to, err := c.addTorrentByMagnet(*req.Magnet, utils.Val(req.Storage))
	if err != nil {
		api.HandleError(w, err)
		return
	}

	select {
	case <-to.GotInfo():
	case <-time.After(gotInfoTimeout):
		api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
		return
	}

	c.mu.Lock()
	resp, err := c.createTorrentInDBLocked(to, req)
	c.mu.Unlock()
	if err != nil {
		api.HandleError(w, err)
		return
	}

	to.Drop()
	<-to.Closed()
	c.logger.Debug("dropped torrent after adding to database", "hash", to.InfoHash())

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", fmt.Sprintf("/api/v1/torrents/%s", resp.Hash))
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) DeleteTorrent(w http.ResponseWriter, _ *http.Request, ih metainfo.Hash) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.deleteTorrentLocked(ih); err != nil {
		api.HandleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *Controller) GetLogs(w http.ResponseWriter, _ *http.Request) {
	entries := logging.DefaultStore.Entries()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) GetMemoryStats(w http.ResponseWriter, _ *http.Request) {
	stats := c.storageClient.GetMemoryStats()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(api.MemoryStats(stats)); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) GetPlaylist(w http.ResponseWriter, r *http.Request, params api.GetPlaylistParams) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	const suffix = ".m3u"
	audioExtensions := []string{
		".aac",
		".ac3",
		".aiff",
		".alac",
		".amr",
		".dff",
		".dsf",
		".dts",
		".flac",
		".it",
		".m3u",
		".m3u8",
		".m4a",
		".mid",
		".midi",
		".mod",
		".mp3",
		".oga",
		".ogg",
		".opus",
		".pcm",
		".ra",
		".wav",
		".wma",
		".wv",
		".xm",
	}
	videoExtensions := []string{
		".3gp",
		".asf",
		".avi",
		".flv",
		".m2ts",
		".m4v",
		".mkv",
		".mov",
		".mp4",
		".mpg",
		".mpeg",
		".mts",
		".mxf",
		".ogv",
		".rm",
		".ts",
		".vob",
		".webm",
		".wmv",
	}
	mediaExts := append(audioExtensions, videoExtensions...)

	var opts []torrentsOpt
	if params.Name != nil {
		name := strings.TrimSuffix(*params.Name, suffix)
		opts = append(opts, nameOpt(name))
	}

	ts, err := c.listTorrentsRLocked(r, opts...)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)
	masterPlaylist := !(params.Name != nil && strings.HasSuffix(*params.Name, suffix))
	m3u := "#EXTM3U\n"

	for _, t := range ts {
		var hasMedia bool
		for _, f := range t.Files {
			ext := strings.ToLower(path.Ext(f.Name))
			if slices.Contains(mediaExts, ext) {
				if !masterPlaylist {
					m3u += fmt.Sprintf("#EXTINF:-1,%s\n", f.Path)
					m3u += fmt.Sprintf("%s/objects/%s\n", baseURL, f.Path)
				}
				hasMedia = true
				break
			}
		}
		if masterPlaylist && hasMedia {
			m3u += fmt.Sprintf("#EXTINF:-1,%s\n", t.Name)
			m3u += fmt.Sprintf("%s%s?name=%s%s\n", baseURL, r.URL.Path, t.Name, suffix)
		}
	}

	w.Header().Set("Content-Disposition", "attachment; filename=playlist.m3u")
	w.Header().Set("Content-Type", "application/x-mpegURL")
	_, _ = w.Write([]byte(m3u))
}

func (c *Controller) GetSettings(w http.ResponseWriter, _ *http.Request) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	redactedSettings := *c.settings
	if c.settings.Auth != nil {
		redactedAuth := *c.settings.Auth
		if redactedAuth.Password != nil {
			redactedAuth.Password = utils.Ptr("********")
		}
		redactedSettings.Auth = &redactedAuth
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(redactedSettings); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) GetStream(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, params api.GetStreamParams) {
	if params.Path != nil && params.Index != nil {
		api.HTTPError(w, "only one of 'path' or 'index' is allowed", http.StatusBadRequest)
		return
	}
	if params.Path != nil {
		c.streamFile(w, r, ih, *params.Path)
		return
	}
	if params.Index != nil {
		c.streamFile(w, r, ih, *params.Index)
		return
	}
	api.HTTPError(w, "one of 'path' or 'index' is required", http.StatusBadRequest)
}

func (c *Controller) GetSystemInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(api.SystemInfo{
		BuildDate: buildinfo.BuildDate,
		Commit:    buildinfo.Commit,
		Uptime:    int64(time.Since(c.startedAt).Seconds()),
		Version:   buildinfo.Version,
	}); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) GetToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		api.HTTPError(w, "invalid form data", http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")
	username := r.FormValue("username")
	password := r.FormValue("password")

	if grantType != "password" {
		api.HTTPError(w, "unsupported grant_type", http.StatusBadRequest)
		return
	}

	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()

	if settings.Auth == nil || !utils.Val(settings.Auth.Enabled) {
		api.HTTPError(w, "authentication not enabled", http.StatusServiceUnavailable)
		return
	}

	if utils.Val(settings.Auth.Username) != username || utils.Val(settings.Auth.Password) != password {
		api.HTTPError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if utils.Val(settings.Auth.Type) != "bearer" {
		api.HTTPError(w, "token endpoint not supported for basic auth", http.StatusBadRequest)
		return
	}

	jwtSecret, err := c.db.GetJWTSecret()
	if err != nil {
		api.HTTPError(w, "failed to get JWT secret", http.StatusInternalServerError)
		return
	}

	token, err := auth.GenerateToken(username, []byte(jwtSecret))
	if err != nil {
		api.HTTPError(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	cookie := http.Cookie{
		Name:     "session",
		Value:    token,
		Expires:  time.Now().Add(auth.TokenExpiry),
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, &cookie)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(api.TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   utils.Ptr(int(auth.TokenExpiry / time.Second)),
	}); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) GetTorrent(w http.ResponseWriter, r *http.Request, ih metainfo.Hash) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	t, err := c.db.GetTorrent(ih)
	if err == nil {
		if t.Poster != nil {
			t.Poster = c.buildPosterUrl(r, *t.Poster)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(t); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if !errors.Is(err, database.ErrTorrentNotFound) {
		api.HandleError(w, api.NewError(err.Error(), http.StatusInternalServerError))
		return
	}

	// If torrent is not in the database, fetch metadata from network.
	to, err := c.addTorrentByHash(ih)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	select {
	case <-to.GotInfo():
		metadata := torrentToMetadata(to)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(metadata); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}
	case <-time.After(gotInfoTimeout):
		api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
	}
}

func (c *Controller) GetTorrentStats(w http.ResponseWriter, _ *http.Request, ih metainfo.Hash) {
	to, err := c.addTorrentByHash(ih)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	select {
	case <-to.GotInfo():
	case <-time.After(gotInfoTimeout):
		api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
		return
	}

	stats, err := c.buildTorrentStats(to)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) HeadStream(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, params api.HeadStreamParams) {
	if params.Path != nil && params.Index != nil {
		api.HTTPError(w, "only one of 'path' or 'index' is allowed", http.StatusBadRequest)
		return
	}
	if params.Path != nil {
		c.streamFile(w, r, ih, *params.Path)
		return
	}
	if params.Index != nil {
		c.streamFile(w, r, ih, *params.Index)
		return
	}
	api.HTTPError(w, "one of 'path' or 'index' is required", http.StatusBadRequest)
}

func (c *Controller) ListTorrents(w http.ResponseWriter, r *http.Request, params api.ListTorrentsParams) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var opts []torrentsOpt
	if params.Categories != nil {
		opts = append(opts, categoryOpt(*params.Categories...))
	}
	if params.Hashes != nil {
		opts = append(opts, hashOpt(*params.Hashes...))
	}
	if params.Names != nil {
		opts = append(opts, nameOpt(*params.Names...))
	}

	ts, err := c.listTorrentsRLocked(r, opts...)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	total := len(ts)

	limit := 0
	if params.Limit != nil && *params.Limit >= 1 {
		limit = *params.Limit
	}
	offset := 0
	if params.Offset != nil && *params.Offset >= 1 {
		offset = *params.Offset
	}

	if offset > 0 {
		if offset < len(ts) {
			ts = ts[offset:]
		} else {
			ts = []*api.Torrent{}
		}
	}

	if limit > 0 {
		if limit < len(ts) {
			ts = ts[:limit]
		}
	}

	out := make([]api.Torrent, 0, len(ts))
	for _, t := range ts {
		out = append(out, *t)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(api.ListTorrents{
		Limit:    limit,
		Offset:   offset,
		Torrents: out,
		Total:    total,
	}); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var reqSettings api.Settings

	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&reqSettings); err != nil {
		api.HTTPError(w, fmt.Sprintf("invalid format for settings, %v", err), http.StatusBadRequest)
		return
	}

	var reconfigureDLNA, reconfigureLogger, reconfigureTorrentClient, restartHTTPServer, saveSettings bool

	c.mu.Lock()
	oldSettings, err := c.db.GetSettings()
	if err != nil {
		c.mu.Unlock()
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	newSettings := *oldSettings

	c.mu.Unlock()

	// Defer error handling to roll back settings.
	defer func() {
		if err != nil {
			c.mu.Lock()
			c.settings = oldSettings
			c.mu.Unlock()
		}
	}()

	if reqSettings.Auth != nil {
		var needsReplace bool
		authCopy := api.Auth{}
		if newSettings.Auth != nil {
			authCopy = *newSettings.Auth
		}

		if reqSettings.Auth.Enabled != nil {
			needsReplace = true
			authCopy.Enabled = reqSettings.Auth.Enabled
		}
		if reqSettings.Auth.Type != nil {
			needsReplace = true
			authCopy.Type = reqSettings.Auth.Type
		}
		if reqSettings.Auth.Username != nil {
			needsReplace = true
			authCopy.Username = reqSettings.Auth.Username
		}
		if reqSettings.Auth.Password != nil {
			needsReplace = true
			authCopy.Password = reqSettings.Auth.Password
		}
		if needsReplace {
			saveSettings = true
			newSettings.Auth = &authCopy
		}
	}

	if reqSettings.LogLevel != nil {
		newSettings.LogLevel = reqSettings.LogLevel
	}
	if reqSettings.LogFormat != nil {
		newSettings.LogFormat = reqSettings.LogFormat
	}
	if reqSettings.LogStoreSize != nil {
		newSettings.LogStoreSize = reqSettings.LogStoreSize
	}
	if reqSettings.MaxMemory != nil {
		newSettings.MaxMemory = reqSettings.MaxMemory
	}
	if reqSettings.DisableIpv6 != nil {
		newSettings.DisableIpv6 = reqSettings.DisableIpv6
	}
	if reqSettings.EnableDlna != nil {
		newSettings.EnableDlna = reqSettings.EnableDlna
	}
	if reqSettings.EnableDownloader != nil {
		newSettings.EnableDownloader = reqSettings.EnableDownloader
	}
	if reqSettings.FriendlyName != nil {
		newSettings.FriendlyName = reqSettings.FriendlyName
	}
	if reqSettings.HTTPServerPort != nil {
		newSettings.HTTPServerPort = reqSettings.HTTPServerPort
	}
	if reqSettings.FileStoragePath != nil {
		newSettings.FileStoragePath = reqSettings.FileStoragePath
	}
	if reqSettings.ReadaheadPercentage != nil {
		newSettings.ReadaheadPercentage = reqSettings.ReadaheadPercentage
	}

	// Perform validation on the merged settings.
	if newSettings.Auth != nil && utils.Val(newSettings.Auth.Enabled) {
		if utils.Val(newSettings.Auth.Type) == "" || utils.Val(newSettings.Auth.Username) == "" || utils.Val(newSettings.Auth.Password) == "" {
			api.HTTPError(w, "authentication type, username and password are required to enable authentication", http.StatusBadRequest)
			return
		}
		if utils.Val(newSettings.Auth.Type) == "bearer" {
			// Ensure JWT secret exists if bearer is enabled.
			if _, err := c.db.GetJWTSecret(); err != nil {
				api.HTTPError(w, "failed to get or create JWT secret", http.StatusInternalServerError)
				return
			}
		}
	}

	// Determine which components need reconfiguration.
	if utils.Differ(oldSettings.LogLevel, newSettings.LogLevel) {
		reconfigureLogger = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.LogFormat, newSettings.LogFormat) {
		reconfigureLogger = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.LogStoreSize, newSettings.LogStoreSize) {
		saveSettings = true
		logging.DefaultStore.Resize(*newSettings.LogStoreSize)
	}
	if utils.Differ(oldSettings.MaxMemory, newSettings.MaxMemory) {
		reconfigureTorrentClient = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.DisableIpv6, newSettings.DisableIpv6) {
		reconfigureTorrentClient = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.EnableDlna, newSettings.EnableDlna) {
		reconfigureDLNA = true
		saveSettings = true
		restartHTTPServer = true
	}
	if utils.Differ(oldSettings.EnableDownloader, newSettings.EnableDownloader) {
		saveSettings = true
	}
	if utils.Differ(oldSettings.FriendlyName, newSettings.FriendlyName) {
		reconfigureDLNA = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.HTTPServerPort, newSettings.HTTPServerPort) {
		reconfigureDLNA = true
		restartHTTPServer = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.FileStoragePath, newSettings.FileStoragePath) {
		c.mu.Lock()
		if c.pieceCompletion != nil {
			c.pieceCompletion.Close()
			c.pieceCompletion = nil
		}

		if newSettings.FileStoragePath != nil && *newSettings.FileStoragePath != "" {
			p := *newSettings.FileStoragePath
			// Create the directory if it does not exist.
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if err := os.MkdirAll(p, 0755); err != nil {
					c.mu.Unlock()
					api.HTTPError(w, fmt.Sprintf("failed to create file storage directory: %v", err), http.StatusBadRequest)
					return
				}
			}
			// Check for write permissions by creating a temporary file.
			tempFile, err := os.CreateTemp(p, "writable-test-")
			if err != nil {
				c.mu.Unlock()
				api.HTTPError(w, fmt.Sprintf("file storage path is not writable: %v", err), http.StatusBadRequest)
				return
			}
			_ = tempFile.Close()
			_ = os.Remove(tempFile.Name())

			// Create the new piece completion database.
			pc, err := piececompletion.New(p, c.logger)
			if err != nil {
				c.mu.Unlock()
				api.HTTPError(w, fmt.Sprintf("failed to create piece completion database: %v", err), http.StatusInternalServerError)
				return
			}
			c.pieceCompletion = pc
		}
		c.mu.Unlock()

		reconfigureTorrentClient = true
		restartHTTPServer = true
		saveSettings = true
	}
	if utils.Differ(oldSettings.ReadaheadPercentage, newSettings.ReadaheadPercentage) {
		saveSettings = true
	}

	if saveSettings {
		c.mu.Lock()
		c.settings = &newSettings
		c.mu.Unlock()
		err = c.db.UpdateSettings(&newSettings)
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to update settings, %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Reconfigure components.
	if reconfigureLogger {
		reconfigureTorrentClient = true
		restartHTTPServer = true
		newLogger := c.configureLogger(*c.settings.LogLevel)
		c.mu.Lock()
		c.logger = newLogger
		c.mu.Unlock()
		c.dlna.SetLogger(newLogger)
	}

	if reconfigureTorrentClient {
		err = c.configureTorrentClient(slog.LevelError)
		if err != nil {
			api.HTTPError(w, fmt.Sprintf("failed to reconfigure torrent client, %v", err), http.StatusInternalServerError)
			return
		}
		c.mu.Lock()
		if c.downloader != nil {
			c.downloader.Stop()
		}
		c.downloader = downloader.New(c.client, c.db, c.logger, c.metrics, c.pieceCompletion, utils.Val(newSettings.FileStoragePath), c.trackers)
		if *c.settings.EnableDownloader && *c.settings.FileStoragePath != "" {
			c.downloader.Start()
		}
		c.mu.Unlock()
	} else if utils.Differ(oldSettings.EnableDownloader, newSettings.EnableDownloader) {
		c.mu.Lock()
		if *c.settings.EnableDownloader && *c.settings.FileStoragePath != "" {
			c.downloader.Start()
		} else {
			c.downloader.Stop()
		}
		c.mu.Unlock()
	}

	if reconfigureDLNA {
		if *c.settings.EnableDlna {
			err = c.dlna.Reconfigure(*c.settings.FriendlyName, c.httpAddr, c.resolveHTTPPort())
		} else {
			err = c.dlna.Stop()
		}
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if restartHTTPServer {
		go func() {
			// Build the new router outside of any locks.
			newRouter := c.buildRouter()

			c.mu.Lock()
			// Atomically swap the router and update the server address.
			c.router = newRouter
			addr := net.JoinHostPort(c.httpAddr, strconv.Itoa(c.resolveHTTPPort()))
			c.httpServer.SetAddr(addr)
			c.mu.Unlock()

			// Set the new router on the HTTP server and restart it.
			c.httpServer.SetRouter(newRouter)

			if err := c.httpServer.Restart(); err != nil {
				c.logger.Debug(fmt.Sprintf("failed to update settings, %v", err))
			}
		}()
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *Controller) UpdateTorrent(w http.ResponseWriter, r *http.Request, ih metainfo.Hash) {
	var req api.TorrentUpdate

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.HTTPError(w, fmt.Sprintf("failed to read request, %v", err), http.StatusBadRequest)
		return
	}

	var posterNeedsUpdate bool
	if req.Poster != nil && *req.Poster != "" {
		go c.handlePosterUpdate(ih, *req.Poster)
		posterNeedsUpdate = true
	}

	// If there are no other fields to update, and it's not a poster removal action,
	// we can return early.
	if req.Category == nil && req.Storage == nil && req.Title == nil && posterNeedsUpdate {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := c.updateTorrent(r, ih, req); err != nil {
		api.HandleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *Controller) addTorrentByHash(ih metainfo.Hash) (*torrent.Torrent, error) {
	if ih.IsZero() {
		return nil, fmt.Errorf("invalid hash %s", ih.HexString())
	}

	t, err := c.db.GetTorrent(ih)
	if err != nil {
		if !errors.Is(err, database.ErrTorrentNotFound) {
			return nil, err
		}
		return c.loadTorrent(magnetURIfromHash(ih), api.Memory)
	}

	return c.loadTorrent(t.Magnet, utils.Val(t.Storage))
}

func (c *Controller) addTorrentByMagnet(uri string, storageType api.TorrentStorage) (*torrent.Torrent, error) {
	magnetV2, err := metainfo.ParseMagnetV2Uri(uri)
	if err != nil || magnetV2.InfoHash.Value.IsZero() {
		return nil, api.NewError(fmt.Sprintf("invalid magnet URI: %v", err), http.StatusBadRequest)
	}

	return c.loadTorrent(uri, storageType)
}

func (c *Controller) buildPosterUrl(r *http.Request, id string) *string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	posterURL := url.URL{
		Scheme: scheme,
		Host:   r.Host,
	}

	data, err := c.images.Get(id)
	if err != nil {
		return nil
	}
	contentType := http.DetectContentType(data)
	ext, ok := images.ImageTypes[contentType]
	if ok {
		posterURL.Path = path.Join(c.postersPath, id+ext)
	} else {
		posterURL.Path = path.Join(c.postersPath, id)
	}
	s := posterURL.String()

	return &s
}

func (c *Controller) buildTorrentStats(to *torrent.Torrent) (*api.TorrentStats, error) {
	if to == nil {
		return nil, errors.New("cannot build stats from a nil torrent")
	}

	t, err := c.db.GetTorrent(to.InfoHash())
	isFileStorage := err == nil && utils.Val(t.Storage) == api.File

	stats := to.Stats()
	resp := &api.TorrentStats{
		ActivePeers:                 stats.ActivePeers,
		BytesHashed:                 stats.BytesHashed.Int64(),
		BytesRead:                   stats.BytesRead.Int64(),
		BytesReadData:               stats.BytesReadData.Int64(),
		BytesReadUsefulData:         stats.BytesReadUsefulData.Int64(),
		BytesReadUsefulIntendedData: stats.BytesReadUsefulIntendedData.Int64(),
		BytesWritten:                stats.BytesWritten.Int64(),
		BytesWrittenData:            stats.BytesWrittenData.Int64(),
		ChunksRead:                  stats.ChunksRead.Int64(),
		ChunksReadUseful:            stats.ChunksReadUseful.Int64(),
		ChunksReadWasted:            stats.ChunksReadWasted.Int64(),
		ChunksWritten:               stats.ChunksWritten.Int64(),
		ConnectedSeeders:            stats.ConnectedSeeders,
		HalfOpenPeers:               stats.HalfOpenPeers,
		MetadataChunksRead:          stats.MetadataChunksRead.Int64(),
		PendingPeers:                stats.PendingPeers,
		PiecesComplete:              stats.PiecesComplete,
		PiecesDirtiedBad:            stats.PiecesDirtiedBad.Int64(),
		PiecesDirtiedGood:           stats.PiecesDirtiedGood.Int64(),
		TotalPeers:                  stats.TotalPeers,
	}

	if isFileStorage {
		resp.CompletedSize = to.BytesCompleted()
		resp.InMemory = 0
		resp.InMemorySize = 0
		resp.MemoryStats = api.MemoryStats(c.storageClient.GetMemoryStats())
		resp.MemoryUsagePercentage = 0

		pieces := make([]api.PieceInfo, to.NumPieces())
		for i := 0; i < to.NumPieces(); i++ {
			p := to.Piece(i)
			pieces[i] = api.PieceInfo{
				Complete: p.State().Complete,
				InMemory: false,
				Index:    p.Info().Index(),
				Size:     p.Info().Length(),
			}
		}
		resp.Pieces = pieces
		resp.TotalPieces = to.NumPieces()
		resp.TotalSize = to.Length()
	} else {
		storageStats, err := c.storageClient.GetTorrentMemoryStats(to.InfoHash())
		if err != nil {
			storageStats = &memstorage.TorrentMemoryStats{}
		}

		var pieces []api.PieceInfo
		if storageStats.Pieces != nil {
			pieces = make([]api.PieceInfo, 0, len(storageStats.Pieces))
			for _, p := range storageStats.Pieces {
				pieces = append(pieces, api.PieceInfo(p))
			}
		}

		resp.Pieces = pieces
		resp.CompletedSize = storageStats.CompletedSize
		resp.InMemory = storageStats.InMemory
		resp.InMemorySize = storageStats.InMemorySize
		resp.MemoryStats = api.MemoryStats(storageStats.MemoryStats)
		resp.MemoryUsagePercentage = storageStats.MemoryUsagePercentage
		resp.TotalPieces = storageStats.TotalPieces
		resp.TotalSize = storageStats.TotalSize
	}

	return resp, nil
}

func (c *Controller) createTorrentInDBLocked(to *torrent.Torrent, req api.TorrentAdd) (*api.Torrent, error) {
	if _, err := c.db.GetTorrent(to.InfoHash()); err == nil {
		return nil, api.NewError(database.ErrTorrentExists.Error(), http.StatusConflict)
	}

	t := torrentToMetadata(to)
	t.Magnet = *req.Magnet
	t.Storage = req.Storage

	if req.Category != nil {
		t.Category = req.Category
	}

	if req.Title != nil {
		t.Title = req.Title
	}

	if err := c.db.CreateTorrent(t); err != nil {
		if errors.Is(err, database.ErrTorrentExists) {
			return nil, api.NewError(err.Error(), http.StatusConflict)
		}
		return nil, api.NewError(err.Error(), http.StatusInternalServerError)
	}

	if req.Poster != nil && *req.Poster != "" {
		go c.handlePosterUpdate(t.Hash, *req.Poster)
	} else if *c.settings.EnableDlna {
		// If there's no poster, we can notify DLNA clients immediately.
		c.dlna.IncrementSystemUpdateID()
		c.dlna.SendUpdateNotification()
	}

	return t, nil
}

func (c *Controller) deleteTorrentLocked(ih metainfo.Hash) error {
	t, dbErr := c.db.GetTorrent(ih)
	isDbTorrent := dbErr == nil

	to, isClientTorrent := c.client.Torrent(ih)

	if !isDbTorrent && !isClientTorrent {
		st := http.StatusInternalServerError
		if errors.Is(dbErr, database.ErrTorrentNotFound) {
			st = http.StatusNotFound
		}
		return api.NewError(dbErr.Error(), st)
	}

	if isClientTorrent {
		to.Drop()
		<-to.Closed()
	}

	if isDbTorrent {
		if utils.Val(t.Storage) == api.File {
			if runtime.GOOS != "windows" && c.settings.FileStoragePath != nil && *c.settings.FileStoragePath != "" {
				torrentStoragePath := filepath.Join(*c.settings.FileStoragePath, t.Name)
				if err := os.RemoveAll(torrentStoragePath); err != nil {
					c.logger.Error("failed to delete torrent file storage", "path", torrentStoragePath, "error", err)
				}
			}
			if c.pieceCompletion != nil {
				if err := c.pieceCompletion.Delete(ih); err != nil {
					c.logger.Error("failed to delete piece completion data", "hash", ih, "error", err)
				}
			}
		}

		if err := c.db.DeleteTorrent(t.Hash); err != nil {
			return api.NewError(fmt.Sprintf("failed to delete torrent with hash %s", ih), http.StatusInternalServerError)
		}

		if *c.settings.EnableDlna {
			c.dlna.IncrementSystemUpdateID()
			c.dlna.SendUpdateNotification()
		}
	}

	return nil
}

func (c *Controller) handlePosterUpdate(ih metainfo.Hash, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), imageDownloadTimeout)
	defer cancel()

	data, err := c.images.DownloadImageData(ctx, url)
	if err != nil {
		c.logger.Debug("error downloading poster image", "error", err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.posterOpMu.Lock()
	defer c.posterOpMu.Unlock()

	imageID, err := c.images.SaveData(data)
	if err != nil {
		c.logger.Debug("error saving poster image", "error", err)
		return
	}

	// Get the torrent to avoid overwriting recent user changes.
	t, err := c.db.GetTorrent(ih)
	if err != nil {
		c.logger.Error("failed to get torrent", "hash", ih, "error", err)
		return
	}

	// If the poster is the same, do nothing.
	if t.Poster != nil && imageID != nil && *t.Poster == *imageID {
		return
	}

	t.Poster = imageID
	t.UpdatedAt = utils.Ptr(time.Now())

	if err := c.db.UpdateTorrent(t); err != nil {
		c.logger.Error("failed to update torrent poster", "hash", ih, "error", err)
		return
	}

	if *c.settings.EnableDlna {
		c.dlna.IncrementSystemUpdateID()
		c.dlna.SendUpdateNotification()
	}

	c.logger.Debug("successfully updated torrent poster", "hash", ih)
}

func (c *Controller) listTorrentsRLocked(r *http.Request, opts ...torrentsOpt) ([]*api.Torrent, error) {
	ts, err := c.db.GetTorrents()
	if err != nil {
		return nil, api.NewError(err.Error(), http.StatusInternalServerError)
	}

	tsMap := make(map[metainfo.Hash]*api.Torrent, len(ts))
	for _, t := range ts {
		tsMap[t.Hash] = t
	}

	for _, to := range c.client.Torrents() {
		if _, ok := tsMap[to.InfoHash()]; !ok {
			select {
			case <-to.GotInfo():
				ts = append([]*api.Torrent{torrentToMetadata(to)}, ts...)
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
	}

	for _, opt := range opts {
		ts = opt(ts)
	}

	for _, t := range ts {
		if t.Poster != nil {
			t.Poster = c.buildPosterUrl(r, *t.Poster)
		}
	}

	return ts, nil
}

// loadTorrent is the single entry point for adding a torrent to the client.
// It handles adding trackers and managing torrents' lifetime in the torrent client.
func (c *Controller) loadTorrent(uri string, storageType api.TorrentStorage) (*torrent.Torrent, error) {
	spec, err := torrent.TorrentSpecFromMagnetUri(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse magnet URI: %w", err)
	}

	if len(c.trackers) > 0 {
		spec.Trackers = c.trackers
	}

	if storageType == api.File && c.settings.FileStoragePath != nil && *c.settings.FileStoragePath != "" {
		fileStoragePath := *c.settings.FileStoragePath
		if _, err := os.Stat(fileStoragePath); os.IsNotExist(err) {
			if err := os.MkdirAll(fileStoragePath, 0755); err != nil {
				c.logger.Warn("failed to create file storage directory, falling back to memory storage", "error", err)
			}
		}

		if _, err := os.Stat(fileStoragePath); err == nil {
			opts := storage.NewFileClientOpts{
				ClientBaseDir:   fileStoragePath,
				PieceCompletion: c.pieceCompletion,
				UsePartFiles:    generics.Option[bool]{Value: false, Ok: true},
				Logger:          c.logger,
			}
			spec.Storage = storage.NewFileOpts(opts)
		}
	}

	to, _, err := c.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to add torrent spec to client: %w", err)
	}

	c.torrentTracker.mu.Lock()
	c.torrentTracker.torrents[to.InfoHash()] = time.Now()
	c.torrentTracker.mu.Unlock()

	return to, nil
}

// streamFile is the internal implementation for streaming a torrent file.
// It can identify the file to stream by either a file path (string) or a file index (int).
func (c *Controller) streamFile(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, fileIdentifier any) {
	to, err := c.addTorrentByHash(ih)
	if err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	select {
	case <-to.GotInfo():
	case <-time.After(gotInfoTimeout):
		api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
		return
	}

	var file *torrent.File

	switch v := fileIdentifier.(type) {
	case string: // File path
		idx := slices.IndexFunc(to.Files(), func(f *torrent.File) bool {
			return f.Path() == v
		})
		if idx < 0 {
			api.HTTPError(w, fmt.Sprintf("invalid file path %s", v), http.StatusBadRequest)
			return
		}
		file = to.Files()[idx]
	case int: // File index
		if v < 0 || v >= len(to.Files()) {
			api.HTTPError(w, "file index out of range", http.StatusBadRequest)
			return
		}
		file = to.Files()[v]
	default:
		api.HTTPError(w, "invalid file identifier", http.StatusInternalServerError)
		return
	}

	t, err := c.db.GetTorrent(ih)
	if err == nil {
		go func() {
			err := c.updateTorrent(r, ih, api.TorrentUpdate{
				Files: &[]api.TorrentFileUpdate{
					{
						Path:   file.Path(),
						Viewed: true,
					},
				},
			})
			if err != nil {
				c.logger.Error("failed to update torrent", "err", err, "hash", ih)
			}
		}()
	}

	reader := file.NewReader()
	defer reader.Close()

	isFileStorage := err == nil && utils.Val(t.Storage) == api.File

	if isFileStorage {
		reader.SetReadahead(fileStorageReadahead)
	} else {
		c.mu.RLock()
		readahead := int64(*c.settings.MaxMemory * int64(*c.settings.ReadaheadPercentage) / 100)
		c.mu.RUnlock()
		reader.SetReadahead(readahead)
	}

	fs := gotorrentfs.New(to)

	r2 := r.Clone(r.Context())
	r2.URL.Path = file.Path()

	dlna.AddHeader(w, r)

	var once sync.Once
	decrement := func() {
		once.Do(func() {
			c.metrics.DecStreamingTorrents()
		})
	}
	defer decrement()

	go func() {
		<-r.Context().Done()
		decrement()
	}()

	c.metrics.IncStreamingTorrents()

	// For streaming endpoints, it is important to disable the read and write timeouts.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	http.FileServerFS(fs).ServeHTTP(w, r2)
}

func (c *Controller) updateTorrent(_ *http.Request, ih metainfo.Hash, req api.TorrentUpdate) error {
	c.mu.Lock()
	c.posterOpMu.Lock()

	t, err := c.db.GetTorrent(ih)
	if err != nil {
		c.posterOpMu.Unlock()
		c.mu.Unlock()
		st := http.StatusInternalServerError
		if errors.Is(err, database.ErrTorrentNotFound) {
			st = http.StatusNotFound
		}
		return api.NewError(err.Error(), st)
	}

	var needsUpdate, needsDrop bool
	if utils.Differ(t.Category, req.Category) {
		needsUpdate = true
		t.Category = req.Category
	}

	if req.Files != nil {
		for _, fileUpdate := range *req.Files {
			for i := range t.Files {
				if t.Files[i].Path == fileUpdate.Path {
					if fileUpdate.Viewed {
						now := time.Now()
						if t.Files[i].ViewedAt == nil || t.Files[i].ViewedAt.Before(now.Add(-fileViewedUpdateThreshold)) {
							t.Files[i].ViewedAt = &now
							needsUpdate = true
						}
					} else if t.Files[i].ViewedAt != nil {
						t.Files[i].ViewedAt = nil
						needsUpdate = true
					}
					break
				}
			}
		}
	}

	if req.Poster != nil && *req.Poster == "" {
		if t.Poster != nil {
			needsUpdate = true
			t.Poster = nil
		}
	}

	oldStorage := t.Storage
	if utils.Differ(t.Storage, req.Storage) {
		if utils.Val(req.Storage) == api.File && (c.settings.FileStoragePath == nil || *c.settings.FileStoragePath == "") {
			c.posterOpMu.Unlock()
			c.mu.Unlock()
			return api.NewError("file storage is not configured", http.StatusBadRequest)
		}
		needsUpdate = true
		needsDrop = true
		t.Storage = req.Storage
	}

	if utils.Differ(t.Title, req.Title) {
		needsUpdate = true
		t.Title = req.Title
	}

	if !needsUpdate {
		c.posterOpMu.Unlock()
		c.mu.Unlock()
		return nil
	}

	t.UpdatedAt = utils.Ptr(time.Now())
	if err := c.db.UpdateTorrent(t); err != nil {
		c.posterOpMu.Unlock()
		c.mu.Unlock()
		return api.NewError(fmt.Sprintf("failed to update torrent with hash %s", ih.HexString()), http.StatusInternalServerError)
	}

	dlnaEnabled := *c.settings.EnableDlna

	c.posterOpMu.Unlock()
	c.mu.Unlock()

	if needsDrop {
		if to, ok := c.client.Torrent(ih); ok {
			to.Drop()
			<-to.Closed()
		}
	}

	if utils.Val(oldStorage) == api.File && utils.Val(t.Storage) == api.Memory {
		if runtime.GOOS != "windows" && c.settings.FileStoragePath != nil && *c.settings.FileStoragePath != "" {
			torrentStoragePath := filepath.Join(*c.settings.FileStoragePath, t.Name)
			if err := os.RemoveAll(torrentStoragePath); err != nil {
				c.logger.Error("failed to delete torrent file storage", "path", torrentStoragePath, "error", err)
			}
		}
		if c.pieceCompletion != nil {
			if err := c.pieceCompletion.Delete(ih); err != nil {
				c.logger.Error("failed to delete piece completion data", "hash", ih, "error", err)
			}
		}
	}

	if dlnaEnabled {
		c.dlna.IncrementSystemUpdateID()
		c.dlna.SendUpdateNotification()
	}

	return nil
}

type torrentsOpt func([]*api.Torrent) []*api.Torrent

func categoryOpt(categories ...string) func([]*api.Torrent) []*api.Torrent {
	return func(ts []*api.Torrent) []*api.Torrent {
		opted := []*api.Torrent{}
		for _, t := range ts {
			if t.Category != nil {
				if slices.Contains(categories, *t.Category) {
					opted = append(opted, t)
				}
			}
		}

		return opted
	}
}

func hashOpt(ihs ...metainfo.Hash) func([]*api.Torrent) []*api.Torrent {
	return func(ts []*api.Torrent) []*api.Torrent {
		opted := []*api.Torrent{}
		for _, t := range ts {
			if slices.ContainsFunc(ihs, func(ih metainfo.Hash) bool {
				return bytes.EqualFold(ih.Bytes(), t.Hash.Bytes())
			}) {
				opted = append(opted, t)
			}
		}

		return opted
	}
}

// nameOpt returns a filter function that selects torrents matching any of the provided names.
// The filter performs case-insensitive matching against torrent names.
func nameOpt(names ...string) func([]*api.Torrent) []*api.Torrent {
	lowerNames := make([]string, 0, len(names))
	for _, n := range names {
		lowerNames = append(lowerNames, strings.ToLower(n))
	}

	return func(ts []*api.Torrent) []*api.Torrent {
		opted := make([]*api.Torrent, 0, len(ts))

		for _, t := range ts {
			if slices.Contains(lowerNames, strings.ToLower(t.Name)) {
				opted = append(opted, t)
			}
		}

		return opted
	}
}

func magnetURIfromHash(ih metainfo.Hash) string {
	return "magnet:?xt=urn:btih:" + ih.HexString()
}

func torrentToMetadata(to *torrent.Torrent) *api.Torrent {
	ih := to.InfoHash()
	name := to.Name()
	files := make([]api.TorrentFile, 0, len(to.Files()))
	for _, f := range to.Files() {
		files = append(files, api.TorrentFile{
			Length: f.Length(),
			Name:   path.Base(f.Path()),
			Path:   f.Path(),
		})
	}

	meta := to.Metainfo()
	magnetV2, err := meta.MagnetV2()
	if err != nil {
		magnetV2 = metainfo.MagnetV2{
			InfoHash:    generics.Some(meta.HashInfoBytes()),
			DisplayName: name,
		}
	}

	magnet := magnetV2.String()
	if unescaped, err := url.QueryUnescape(magnet); err == nil {
		magnet = unescaped
	}

	return &api.Torrent{
		Files:      files,
		Hash:       ih,
		Name:       name,
		Magnet:     magnet,
		PieceCount: to.NumPieces(),
		Storage:    utils.Ptr(api.Memory),
		Title:      &name,
		TotalSize:  to.Length(),
	}
}
