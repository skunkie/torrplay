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
	"strconv"
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
	if utils.Val(req.Action) != "get" {
		api.HTTPError(w, "invalid action", http.StatusBadRequest)
		return
	}

	ih, err := utils.HashFromHexString(req.Hash)
	if err != nil {
		api.HTTPError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var dbTorrent *database.Torrent
	if t, err := c.db.GetTorrent(ih); err == nil {
		dbTorrent = t
	} else if !errors.Is(err, database.ErrTorrentNotFound) {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	c.mu.RLock()
	to, hasClientTorrent := c.client.Torrent(ih)
	c.mu.RUnlock()

	if dbTorrent == nil && !hasClientTorrent {
		api.HTTPError(w, "torrent not found", http.StatusNotFound)
		return
	}

	if dbTorrent != nil && *dbTorrent.Storage == api.File {
		// File storage: pieces are written directly to disk, not tracked by
		// the in-memory storage client. Build piece state from the torrent
		// engine when the torrent is active and its info is available;
		// otherwise return an empty cache response compatible with TorrServer.
		w.Header().Set("Content-Type", "application/json")
		if !hasClientTorrent || to == nil || to.Info() == nil {
			if err := json.NewEncoder(w).Encode(struct{}{}); err != nil {
				api.HTTPError(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		c.mu.RLock()
		pool := c.streamPool
		c.mu.RUnlock()

		var fileReaders []api.TSReaderInfo
		if pool != nil {
			rps := pool.ReaderPositions(ih)
			fileReaders = make([]api.TSReaderInfo, len(rps))
			for i, ri := range rps {
				fileReaders[i] = api.TSReaderInfo{Start: ri.Start, Reader: ri.Position, End: ri.End}
			}
		}

		numPieces := to.NumPieces()
		pieceMap := make(map[string]api.TSPieceInfo, numPieces)
		var fileFilled int64
		for i := range numPieces {
			ps := to.PieceState(i)
			pieceLen := to.Info().Piece(i).Length()
			pieceInfo := tsPieceInfoFromState(i, pieceLen, ps)
			fileFilled += pieceInfo.Size
			pieceMap[strconv.Itoa(i)] = pieceInfo
		}

		fileResp := api.TSCacheResponse{
			Capacity:     to.Length(),
			Filled:       fileFilled,
			Hash:         ih.HexString(),
			Pieces:       pieceMap,
			PiecesCount:  numPieces,
			PiecesLength: to.Info().PieceLength,
			Readers:      fileReaders,
		}

		tsResp := c.buildTSTorrentResponse(database.ToAPITorrent(dbTorrent), to)
		fileResp.Torrent = &tsResp

		if err := json.NewEncoder(w).Encode(fileResp); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	info, err := c.storageClient.TorrentStats(ih)
	if err != nil || info.TotalPieces == 0 || len(info.Pieces) == 0 {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct{}{}); err != nil {
			api.HTTPError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	c.mu.RLock()
	pool := c.streamPool
	capacity := utils.Val(c.settings.MaxMemory)
	c.mu.RUnlock()

	var apiReaders []api.TSReaderInfo
	if pool != nil {
		readers := pool.ReaderPositions(ih)
		apiReaders = make([]api.TSReaderInfo, len(readers))
		for i, ri := range readers {
			apiReaders[i] = api.TSReaderInfo{Start: ri.Start, Reader: ri.Position, End: ri.End}
		}
	}
	if len(apiReaders) == 0 {
		// Fallback: if no active readers, use the full piece range.
		minPiece := info.Pieces[0].Index
		maxPiece := info.Pieces[0].Index
		for _, p := range info.Pieces[1:] {
			if p.Index < minPiece {
				minPiece = p.Index
			}
			if p.Index > maxPiece {
				maxPiece = p.Index
			}
		}
		apiReaders = []api.TSReaderInfo{
			{
				Reader: minPiece,
				Start:  minPiece,
				End:    maxPiece,
			},
		}
	}

	piecesLength := info.Pieces[0].SizeBytes
	if hasClientTorrent && to != nil && to.Info() != nil {
		piecesLength = to.Info().PieceLength
	}

	resp := api.TSCacheResponse{
		Capacity:     capacity,
		Filled:       info.WrittenBytes,
		Hash:         ih.HexString(),
		Pieces:       make(map[string]api.TSPieceInfo, len(info.Pieces)),
		PiecesCount:  info.TotalPieces,
		PiecesLength: piecesLength,
		Readers:      apiReaders,
	}

	for _, piece := range info.Pieces {
		var pieceSize int64
		if piece.Resident {
			pieceSize = piece.WrittenBytes
		}
		var priority int
		if hasClientTorrent && to != nil {
			priority = int(to.PieceState(piece.Index).Priority)
		}
		resp.Pieces[strconv.Itoa(piece.Index)] = api.TSPieceInfo{
			Completed: piece.Complete,
			ID:        piece.Index,
			Length:    piece.SizeBytes,
			Priority:  priority,
			Size:      pieceSize,
		}
	}

	if hasClientTorrent && to != nil {
		var t *api.Torrent
		if dbTorrent != nil {
			t = database.ToAPITorrent(dbTorrent)
		} else {
			t = torrentToMetadata(to)
		}
		tsResp := c.buildTSTorrentResponse(t, to)
		resp.Torrent = &tsResp
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

func tsPieceInfoFromState(id int, length int64, state torrent.PieceState) api.TSPieceInfo {
	var size int64
	if state.Complete {
		size = length
	}
	return api.TSPieceInfo{
		Completed: state.Complete,
		ID:        id,
		Length:    length,
		Priority:  int(state.Priority),
		Size:      size,
	}
}

func (*Controller) TSEcho(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("MatriX.TorrPlay"))
}

func (c *Controller) TSPlay(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, index int, _ api.TSPlayParams) {
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
		magnetStr = utils.MagnetURIFromHash(ih)
	}

	if ih.IsZero() {
		api.HTTPError(w, "invalid hash "+ih.HexString(), http.StatusBadRequest)
		return
	}

	if utils.Val(params.Play) {
		c.TSPlay(w, r, ih, utils.Val(params.Index), api.TSPlayParams{})
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
			to.Drop()
			<-to.Closed()
			return
		}

		// If torrent is already fully downloaded, no need to download pieces.
		if to.BytesCompleted() < to.Length() {
			c.startPreloadByFileIndex(to, params.Index)
		}

		if utils.Val(params.Stat) {
			t := torrentToMetadata(to)
			resp := c.buildTSTorrentResponse(t, to)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				api.HTTPError(w, err.Error(), http.StatusInternalServerError)
			}
		}
		return
	}

	if utils.Val(params.Stat) {
		select {
		case <-to.GotInfo():
		case <-time.After(gotInfoTimeout):
			to.Drop()
			<-to.Closed()
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
			magnet = new(utils.MagnetURIFromHash(ih))
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
		if utils.Val(req.Link) != "" {
			var code int
			magnet, code, err = c.parseLink(r.Context(), req.Link)
			if err != nil {
				api.HTTPError(w, err.Error(), code)
				return
			}
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
			to.Drop()
			<-to.Closed()
			api.HTTPError(w, gotInfoTimeoutMsg, http.StatusGatewayTimeout)
			return
		}

		if req.Action == api.TSTorrentRequestActionAdd && utils.Val(req.SaveToDB) {
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

		if req.Action == api.TSTorrentRequestActionAdd && utils.Val(req.SaveToDB) {
			if !c.hasTorrentReaders(to.InfoHash()) {
				to.Drop()
				<-to.Closed()
				c.logger.Debug("dropped torrent after adding to database", "hash", to.InfoHash())
			}
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
	err := parseMultipartForm(w, r)
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
		to.Drop()
		<-to.Closed()
		api.HandleError(w, api.NewError(gotInfoTimeoutMsg, http.StatusGatewayTimeout))
		return
	}

	req := api.TorrentAdd{
		Category: &category,
		Magnet:   new(magnetV2.String()),
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

		if err := c.updateTorrent(ih, api.TorrentUpdate{
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

const (
	tsStatGettingInfo = 1
	tsStatPreload     = 2
	tsStatWorking     = 3
	tsStatClosed      = 4
	tsStatInDB        = 5
)

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

	title := utils.Val(t.Title)
	if title == "" {
		title = t.Name
	}

	resp := api.TSTorrentResponse{
		Category:    t.Category,
		FileStats:   &fileStats,
		Hash:        t.Hash.HexString(),
		Name:        t.Name,
		Poster:      t.Poster,
		Title:       title,
		Timestamp:   utils.Val(t.CreatedAt).Unix(),
		TorrentSize: t.TotalSize,
		Stat:        tsStatInDB,
		StatString:  "Torrent in db",
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
		resp.WrittenBytes = stats.WrittenBytes

		peers := to.PeerConns()
		var totalDownloadRate float64
		var totalUploadRate float64
		for _, peer := range peers {
			pStats := peer.Stats()
			totalDownloadRate += pStats.DownloadRate
			totalUploadRate += pStats.LastWriteUploadRate
		}
		resp.DownloadSpeed = totalDownloadRate
		resp.UploadSpeed = totalUploadRate
		resp.LoadedSize = stats.CompletedSize
		resp.PreloadSize = stats.CompletedSize
		resp.PreloadedBytes = stats.CompletedSize

		if to.Info() == nil {
			resp.Stat = tsStatGettingInfo
			resp.StatString = "Torrent getting info"
		} else if val, preloading := c.preloads.Load(to.InfoHash()); preloading {
			resp.TorrentSize = to.Length()
			resp.Stat = tsStatPreload
			resp.StatString = "Torrent preload"
			if p, ok := val.(*preloadTask); ok && p != nil {
				resp.PreloadSize = p.targetBytes
				resp.PreloadedBytes = p.progressBytes()
			}
		} else {
			resp.TorrentSize = to.Length()
			resp.Stat = tsStatWorking
			resp.StatString = "Torrent working"
		}
	}

	return resp
}

func (c *Controller) startPreloadByFileIndex(to *torrent.Torrent, fileIndex *int) {
	if to == nil || to.Info() == nil {
		return
	}

	files := to.Files()
	if len(files) == 0 {
		return
	}

	idx := 0
	if fileIndex != nil && *fileIndex > 0 {
		idx = *fileIndex - 1
	}
	if idx < 0 || idx >= len(files) {
		idx = 0
	}

	c.startPreload(to, files[idx], idx)
}

func (c *Controller) parseLink(ctx context.Context, link *string) (*string, int, error) {
	err := errors.New("invalid link")
	if utils.Val(link) == "" {
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
		magnet = utils.MagnetURIFromHash(ih)
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

		err := parseMultipartForm(w, r)
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

				h := make(textproto.MIMEHeader)
				disposition := fmt.Sprintf(`form-data; name="file"; filename=%q`, fileHeader.Filename)
				h.Set("Content-Disposition", disposition)
				h.Set("Content-Type", fileHeader.Header.Get("Content-Type"))

				part, err := writer.CreatePart(h)
				if err != nil {
					_ = file.Close()
					continue
				}

				_, _ = io.Copy(part, file)
				_ = file.Close()
			}
		}

		_ = writer.Close()

		newReq, _ := http.NewRequest(r.Method, r.URL.String(), &buf) //nolint:gosec // This request is dispatched internally and never sent over the network.
		newReq.Header = r.Header.Clone()
		newReq.Header.Set("Content-Type", writer.FormDataContentType())
		newReq = newReq.WithContext(r.Context())

		*r = *newReq

		next.ServeHTTP(w, r)
	})
}
