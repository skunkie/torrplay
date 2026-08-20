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
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

func (c *Controller) TSCache(w http.ResponseWriter, r *http.Request) {
	var req api.TSCacheRequest

	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.HTTPError(w, fmt.Sprintf("failed to read request, %v", err), http.StatusBadRequest)
		return
	}

	ih, err := utils.HashFromHexString(req.Hash)
	if err != nil {
		api.HTTPError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if t, err := c.db.GetTorrent(ih); err != nil {
		if !errors.Is(err, database.ErrTorrentNotFound) {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if *t.Storage == api.File {
		api.HTTPError(w, "torrent uses file storage", http.StatusBadRequest)
		return
	}

	info, err := c.storageClient.TorrentStats(ih)
	if err != nil {
		api.HTTPError(w, "torrent memory stats unavailable", http.StatusBadRequest)
		return
	}
	if info.TotalPieces == 0 || len(info.Pieces) == 0 {
		api.HTTPError(w, "torrent has no pieces cached yet", http.StatusBadRequest)
		return
	}
	resp := api.TSCacheResponse{
		Capacity:     info.TrackedBytes,
		Pieces:       make(map[string]api.TSPieceInfo, len(info.Pieces)),
		PiecesCount:  info.TotalPieces,
		PiecesLength: info.Pieces[0].SizeBytes,
		Readers: []api.TSReaderInfo{
			{
				Reader: info.Pieces[0].Index,
				Start:  info.Pieces[0].Index,
				End:    info.Pieces[len(info.Pieces)-1].Index,
			},
		},
	}

	for _, piece := range info.Pieces {
		resp.Pieces[fmt.Sprint(piece.Index)] = api.TSPieceInfo{
			Completed: piece.Complete,
			ID:        piece.Index,
			Length:    piece.SizeBytes,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (_ *Controller) TSEcho(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("MatriX.TorrPlay"))
}

func (c *Controller) TSPlay(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, index int) {
	if index > 0 {
		index-- // Adjust for 0-based index
	} else {
		index = 0
	}

	c.streamFile(w, r, ih, index)
}

func (c *Controller) TSSettings(w http.ResponseWriter, _ *http.Request) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	resp := api.TSSettings{CacheSize: c.settings.MaxMemory}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) TSStream(w http.ResponseWriter, r *http.Request, _ api.TSFileName, params api.TSStreamParams) {
	var (
		ih        metainfo.Hash
		magnetStr string
	)

	if strings.HasPrefix(params.Link, "magnet") {
		magnetStr = params.Link
		m, err := metainfo.ParseMagnetV2Uri(magnetStr)
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusBadRequest)
			return
		}
		ih = m.InfoHash.Value
	} else {
		var err error
		ih, err = utils.HashFromHexString(params.Link)
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusBadRequest)
			return
		}
		magnetStr = magnetURIfromHash(ih)
	}

	if ih.IsZero() {
		api.HTTPError(w, fmt.Sprintf("invalid hash %s", ih.HexString()), http.StatusBadRequest)
		return
	}

	if utils.Val(params.Play) {
		c.TSPlay(w, r, ih, *params.Index)
		return
	}

	to, err := c.addTorrentByMagnet(magnetStr)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	if utils.Val(params.Preload) {
		select {
		case <-to.GotInfo():
		case <-time.After(gotInfoTimeout):
			return
		}

		// If torrent is already fully downloaded, no need to download pieces.
		if to.BytesCompleted() == to.Length() {
			return
		}

		to.DownloadPieces(0, 2)
		return
	}

	if utils.Val(params.Stat) {
		select {
		case <-to.GotInfo():
		case <-time.After(gotInfoTimeout):
			api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
			return
		}
		t := torrentToMetadata(to)
		resp := c.buildTSTorrentResponse(t, to)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	api.HTTPError(w, "invalid query params", http.StatusBadRequest)
}

func (c *Controller) TSTorrents(w http.ResponseWriter, r *http.Request) {
	var (
		ih     metainfo.Hash
		magnet *string
		req    api.TSTorrentRequest
	)

	defer r.Body.Close()
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		api.HTTPError(w, fmt.Sprintf("failed to read request, %v", err), http.StatusBadRequest)
		return
	}

	if req.Hash != nil {
		if hash, err := req.Hash.AsTSTorrentRequestHash1(); err == nil && hash != "" {
			ih, err = utils.HashFromHexString(hash)
			if err != nil {
				api.HTTPError(w, err.Error(), http.StatusBadRequest)
				return
			}
			magnet = utils.Ptr(magnetURIfromHash(ih))
		}
	}

	if req.Action == api.TSTorrentRequestActionList {
		c.mu.RLock()
		defer c.mu.RUnlock()
		resp := []api.TSTorrentResponse{}
		ts, err := c.listTorrentsRLocked(r)
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, t := range ts {
			to, ok := c.client.Torrent(t.Hash)
			if !ok {
				to = nil
			}
			resp = append(resp, c.buildTSTorrentResponse(t, to))
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if req.Action == api.TSTorrentRequestActionDrop {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if req.Action == api.TSTorrentRequestActionAdd {
		var code int
		magnet, code, err = c.parseLink(r.Context(), req.Link)
		if err != nil {
			api.HTTPError(w, err.Error(), code)
			return
		}
	}

	if magnet == nil {
		api.HTTPError(w, "hash or link is empty", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case api.TSTorrentRequestActionAdd, api.TSTorrentRequestActionGet:
		to, err := c.addTorrentByMagnet(*magnet)
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

		var needsDrop bool
		if req.Action == api.TSTorrentRequestActionAdd && utils.Val(req.SaveToDB) {
			needsDrop = true
			addTorrentReq := api.TorrentAdd{
				Category: req.Category,
				Magnet:   magnet,
				Poster:   req.Poster,
				Storage:  utils.Ptr(api.Memory),
				Title:    req.Title,
			}
			c.mu.Lock()
			_, err := c.createTorrentInDBLocked(to, addTorrentReq)
			c.mu.Unlock()
			if err != nil && !errors.Is(err, database.ErrTorrentExists) {
				api.HandleError(w, err)
				return
			}
		}

		var t *api.Torrent
		dbT, err := c.db.GetTorrent(to.InfoHash())
		if err != nil {
			t = torrentToMetadata(to)
		} else {
			t = database.ToAPITorrent(dbT)
		}

		if t.Poster != nil {
			t.Poster = c.buildPosterUrl(r, *t.Poster)
		}

		resp := c.buildTSTorrentResponse(t, to)

		if needsDrop {
			to.Drop()
			<-to.Closed()
			c.logger.Debug("dropped torrent after adding to database", "hash", to.InfoHash())
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}

	case api.TSTorrentRequestActionRem:
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.deleteTorrentLocked(ih); err != nil {
			api.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		api.HTTPError(w, fmt.Sprintf("unknown action %s", req.Action), http.StatusBadRequest)
	}
}

func (c *Controller) TSTorrentUpload(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(multipartFormMaxMemory)
	if err != nil {
		api.HTTPError(w, fmt.Sprintf("failed to parse multipart form: %v", err), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		api.HTTPError(w, fmt.Sprintf("failed to get file: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

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

	if title == "" {
		info, err := meta.UnmarshalInfo()
		if err == nil && info.Name != "" {
			title = info.Name
		}
	}

	to, err := c.addTorrentByMagnet(magnetV2.String())
	if err != nil {
		api.HandleError(w, err)
		return
	}

	select {
	case <-to.GotInfo():
	case <-time.After(gotInfoTimeout):
		api.HandleError(w, api.NewError(gotInfoTimeoutMsg, http.StatusGatewayTimeout))
		return
	}

	req := api.TorrentAdd{
		Category: &category,
		Magnet:   utils.Ptr(magnetV2.String()),
		Poster:   &poster,
		Storage:  utils.Ptr(api.Memory),
		Title:    &title,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	t, err := c.createTorrentInDBLocked(to, req)
	if err != nil {
		api.HandleError(w, err)
		return
	}

	resp := c.buildTSTorrentResponse(t, to)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", fmt.Sprintf("/api/v1/torrents/%s", t.Hash))
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *Controller) TSViewed(w http.ResponseWriter, r *http.Request) {
	var req api.TSViewedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.HTTPError(w, "failed to decode request", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case api.TSViewedRequestActionSet, api.TSViewedRequestActionRem:
		viewed := req.Action == api.TSViewedRequestActionSet
		ih, err := utils.HashFromHexString(req.Hash)
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusBadRequest)
			return
		}

		t, err := c.db.GetTorrent(ih)
		if err != nil {
			api.HTTPError(w, "torrent not found", http.StatusNotFound)
			return
		}

		index := req.FileIndex
		if index > 0 {
			index--
		}

		if index < 0 || index >= len(t.Files) {
			api.HTTPError(w, "file index out of range", http.StatusBadRequest)
			return
		}

		if err := c.updateTorrent(r, ih, api.TorrentUpdate{
			Files: &[]api.TorrentFileUpdate{
				{
					Path:   t.Files[index].Path,
					Viewed: viewed,
				},
			},
		}); err != nil {
			api.HandleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case api.TSViewedRequestActionList:
		allTorrents, err := c.db.GetTorrents()
		if err != nil {
			api.HTTPError(w, "failed to get torrents", http.StatusInternalServerError)
			return
		}

		var viewedFiles []api.TSViewedResponse
		for _, t := range allTorrents {
			for i, f := range t.Files {
				if f.ViewedAt != nil {
					viewedFiles = append(viewedFiles, api.TSViewedResponse{
						Hash:      t.Hash.HexString(),
						FileIndex: i + 1,
					})
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(viewedFiles); err != nil {
			api.HTTPError(w, "failed to encode response", http.StatusInternalServerError)
		}

	default:
		api.HTTPError(w, "invalid action", http.StatusBadRequest)
	}
}

func (c *Controller) buildTSTorrentResponse(t *api.Torrent, to *torrent.Torrent) api.TSTorrentResponse {
	fileStats := make([]api.TSTorrentFileStat, 0, len(t.Files))
	for idx, f := range t.Files {
		fileStats = append(fileStats, api.TSTorrentFileStat{
			ID:       idx + 1,
			Length:   f.Length,
			Name:     f.Name,
			Path:     f.Path,
			ViewedAt: f.ViewedAt,
		})
	}

	resp := api.TSTorrentResponse{
		Category:  t.Category,
		FileStats: &fileStats,
		Hash:      t.Hash.HexString(),
		Name:      t.Name,
		Poster:    t.Poster,
		Title:     *t.Title,
		Timestamp: utils.Val(t.CreatedAt).Unix(),
	}

	if to != nil {
		stats, err := c.buildTorrentStats(to)
		if err != nil {
			stats = &api.TorrentStats{}
		}

		resp.ActivePeers = stats.ActivePeers
		resp.BytesHashed = stats.BytesHashed
		resp.BytesRead = stats.BytesRead
		resp.BytesReadData = stats.BytesReadData
		resp.BytesReadUsefulData = stats.BytesReadUsefulData
		resp.BytesReadUsefulIntendedData = stats.BytesReadUsefulIntendedData
		resp.BytesWritten = stats.BytesWritten
		resp.BytesWrittenData = stats.BytesWrittenData
		resp.ChunksRead = stats.ChunksRead
		resp.ChunksReadUseful = stats.ChunksReadUseful
		resp.ChunksReadWasted = stats.ChunksReadWasted
		resp.ChunksWritten = stats.ChunksWritten
		resp.ConnectedSeeders = stats.ConnectedSeeders
		resp.HalfOpenPeers = stats.HalfOpenPeers
		resp.MetadataChunksRead = stats.MetadataChunksRead
		resp.PendingPeers = stats.PendingPeers
		resp.PiecesComplete = stats.PiecesComplete
		resp.PiecesDirtiedBad = stats.PiecesDirtiedBad
		resp.PiecesDirtiedGood = stats.PiecesDirtiedGood
		resp.TotalPeers = stats.TotalPeers

		peers := to.PeerConns()
		var totalDownloadRate float64
		for _, peer := range peers {
			totalDownloadRate += peer.Stats().DownloadRate
		}
		resp.DownloadSpeed = totalDownloadRate
		resp.LoadedSize = stats.InMemorySize
		resp.PreloadSize = stats.CompletedSize
		resp.PreloadedBytes = stats.CompletedSize
		resp.TorrentSize = to.Length()
	}

	return resp
}

func (c *Controller) parseLink(ctx context.Context, link *string) (*string, int, error) {
	err := errors.New("invalid link")
	if link == nil || *link == "" {
		return nil, http.StatusBadRequest, err
	}
	var magnet string
	u, err := url.Parse(*link)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	switch u.Scheme {
	case "magnet":
		magnet = *link
	case "":
		ih, err := utils.HashFromHexString(*link)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		magnet = magnetURIfromHash(ih)
	case "http", "https":
		resp, err := c.httpClient.Get(ctx, *link)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, http.StatusInternalServerError, fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}
		meta, err := metainfo.Load(resp.Body)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		magnetV2, err := meta.MagnetV2()
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		magnet = magnetV2.String()
	case "file":
		meta, err := metainfo.LoadFromFile(*link)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		magnetV2, err := meta.MagnetV2()
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		magnet = magnetV2.String()
	default:
		return nil, http.StatusBadRequest, err
	}

	_, err = metainfo.ParseMagnetV2Uri(magnet)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	return &magnet, 0, nil
}

func tSCorrectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Paths that need header correction.
		correctionPaths := map[string]bool{
			"/cache":    true,
			"/torrents": true,
			"/settings": true,
			"/viewed":   true,
		}

		if correctionPaths[r.URL.Path] {
			val := r.Header.Get("Content-Type")
			if val != "application/json" {
				// Remove problematic header.
				r.Header.Del("Content-Type")
				// Add correct header.
				r.Header.Set("Content-Type", "application/json")
			}
		}

		// Correct path and query params.
		if strings.HasPrefix(r.URL.Path, "/stream") {
			unescaped, err := url.PathUnescape(r.URL.Path)
			if err == nil {
				r.URL.Path = unescaped
			}
			q := r.URL.Query()
			for _, param := range []string{"play", "preload", "stat"} {
				if q.Get(param) == "" && q.Has(param) {
					q.Set(param, "true")
					r.URL.RawQuery = q.Encode()
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

func tSUploadTorrentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrent/upload" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			next.ServeHTTP(w, r)
			return
		}

		err := r.ParseMultipartForm(multipartFormMaxMemory)
		if err != nil {
			api.HTTPError(w, err.Error(), http.StatusBadRequest)
			return
		}

		if r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		for key, values := range r.MultipartForm.Value {
			if key == "data" || key == "save" {
				continue
			}
			for _, value := range values {
				if value != "" {
					_ = writer.WriteField(key, value)
				}
			}
		}

		for _, files := range r.MultipartForm.File {
			for _, fileHeader := range files {
				file, err := fileHeader.Open()
				if err != nil {
					continue
				}
				defer file.Close()

				h := make(textproto.MIMEHeader)
				disposition := fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileHeader.Filename)
				h.Set("Content-Disposition", disposition)
				h.Set("Content-Type", fileHeader.Header.Get("Content-Type"))

				part, err := writer.CreatePart(h)
				if err != nil {
					continue
				}

				_, _ = io.Copy(part, file)
			}
		}

		_ = writer.Close()

		newReq, _ := http.NewRequest(r.Method, r.URL.String(), &buf)
		newReq.Header = r.Header.Clone()
		newReq.Header.Set("Content-Type", writer.FormDataContentType())
		newReq = newReq.WithContext(r.Context())

		*r = *newReq

		next.ServeHTTP(w, r)
	})
}
