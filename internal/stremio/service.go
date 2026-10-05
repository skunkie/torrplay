// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stremio

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/buildinfo"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

var (
	seasonEpisodeRegex = regexp.MustCompile(`(?i)[sS](\d{1,2})[eE](\d{1,3})`)
	xEpisodeRegex      = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])(\d{1,2})x(\d{1,3})(?:[^a-zA-Z0-9]|$)`)
	epOnlyRegex        = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])(?:ep|episode)[. _-]*(\d{1,3})(?:[^a-zA-Z0-9]|$)`)
	eNumRegex          = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])e(\d{2,3})(?:[^a-zA-Z0-9]|$)`)
	seasonOnlyRegex    = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])(?:season|s)[. _-]?(\d{1,2})(?:[^a-zA-Z0-9]|$)`)
	standaloneNumRegex = regexp.MustCompile(`^(\d{1,3})[.\s_-]`)
	cdPartRegex        = regexp.MustCompile(`(?i)(?:cd|disc|disk|part)[. _-]?(\d+)`)

	mediaExtensions = map[string]bool{
		".mp4":  true,
		".mkv":  true,
		".avi":  true,
		".webm": true,
		".mov":  true,
		".wmv":  true,
		".m4v":  true,
		".flv":  true,
		".ts":   true,
		".m2ts": true,
		".vob":  true,
		".ogv":  true,
		".3gp":  true,
		".mp3":  true,
		".flac": true,
		".aac":  true,
		".m4a":  true,
		".opus": true,
		".ogg":  true,
	}
)

const (
	defaultCatalogLimit = 100
	idPrefix            = "torrplay:"
	// fallbackManifestVersion is used when the build was not stamped with a
	// semver version; Stremio rejects manifests whose version is not semver.
	fallbackManifestVersion = "1.0.0"
)

var semverRegex = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?` +
		`(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
)

// StreamHandlerFunc defines the signature for streaming a torrent file.
type StreamHandlerFunc func(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, fileIdx int)

// AuthValidatorFunc validates a token string when authentication is enabled.
type AuthValidatorFunc func(token string) bool

type torrentReader interface {
	GetTorrents() ([]*database.Torrent, error)
	GetTorrent(ih metainfo.Hash) (*database.Torrent, error)
}

// Service implements the Stremio Addon Protocol v3.
type Service struct {
	db            torrentReader
	postersPath   string
	logger        *slog.Logger
	streamHandler StreamHandlerFunc
	authValidator AuthValidatorFunc
}

// NewService creates a new Stremio addon service.
func NewService(
	db torrentReader,
	postersPath string,
	logger *slog.Logger,
	streamHandler StreamHandlerFunc,
	authValidator AuthValidatorFunc,
) *Service {
	return &Service{
		db:            db,
		postersPath:   postersPath,
		logger:        logger,
		streamHandler: streamHandler,
		authValidator: authValidator,
	}
}

// ServeHTTP routes Stremio protocol requests.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The addon protocol is read-only: reject anything but safe methods.
	// Preflight requests are answered by the CORS middleware before reaching here.
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions:
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Support both mounted router (prefix stripped) and direct dispatch.
	reqPath := strings.TrimPrefix(r.URL.Path, "/stremio")
	cleanPath := strings.Trim(reqPath, "/")

	if cleanPath == "" {
		http.Redirect(w, r, "/stremio/manifest.json", http.StatusTemporaryRedirect)
		return
	}

	parts := strings.Split(cleanPath, "/")

	var token string
	if len(parts) > 0 && !isStremioResource(parts[0]) {
		token = parts[0]
		parts = parts[1:]
	}

	// Also check query param for token if not in path.
	if token == "" {
		token = r.URL.Query().Get("token")
	}

	// Validate authentication if an auth validator is provided.
	if s.authValidator != nil && !s.authValidator(token) {
		s.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	switch parts[0] {
	case "manifest.json":
		s.handleManifest(w, r)

	case "catalog":
		// Format: /catalog/{type}/{id}.json or /catalog/{type}/{id}/{extra}.json
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		catalogType := parts[1]
		catalogID := strings.TrimSuffix(parts[2], ".json")
		var extra string
		if len(parts) >= 4 {
			extra = strings.TrimSuffix(parts[3], ".json")
		}
		s.handleCatalog(w, r, catalogType, catalogID, extra)

	case "meta":
		// Format: /meta/{type}/{id}.json
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		mediaType := parts[1]
		metaID := strings.TrimSuffix(parts[2], ".json")
		s.handleMeta(w, r, mediaType, metaID)

	case "stream":
		// Format: /stream/{type}/{id}.json
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		mediaType := parts[1]
		streamID := strings.TrimSuffix(parts[2], ".json")
		s.handleStream(w, r, mediaType, streamID, token)

	case "play":
		// Format: /play/{hash}/{fileIdx} or /play/{hash}/{fileIdx}/{filename}
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		s.handlePlay(w, r, parts[1], parts[2])

	default:
		http.NotFound(w, r)
	}
}

func (s *Service) handleManifest(w http.ResponseWriter, r *http.Request) {
	baseURL := getBaseURL(r)
	logoURL := baseURL.ResolveReference(&url.URL{Path: "/favicon.ico"}).String()

	manifest := Manifest{
		ID:          "org.torrplay.stremio",
		Name:        "TorrPlay",
		Version:     manifestVersion(),
		Description: "Stream torrents from your TorrPlay library",
		Logo:        logoURL,
		Resources:   []string{"catalog", "meta", "stream"},
		Types:       []string{"movie", "series", "other"},
		Catalogs: []Catalog{
			{
				Type: "other",
				ID:   "torrplay_all",
				Name: "TorrPlay Library",
				Extra: []ExtraProp{
					{Name: "search", IsRequired: false},
					{Name: "skip", IsRequired: false},
				},
			},
			{
				Type: "movie",
				ID:   "torrplay_movies",
				Name: "TorrPlay Movies",
				Extra: []ExtraProp{
					{Name: "search", IsRequired: false},
					{Name: "skip", IsRequired: false},
				},
			},
			{
				Type: "series",
				ID:   "torrplay_series",
				Name: "TorrPlay Series",
				Extra: []ExtraProp{
					{Name: "search", IsRequired: false},
					{Name: "skip", IsRequired: false},
				},
			},
		},
		IDPrefixes: []string{idPrefix},
		BehaviorHints: &BehaviorHints{
			Configurable:          false,
			ConfigurationRequired: false,
		},
	}

	s.writeJSON(w, http.StatusOK, manifest)
}

func (s *Service) handleCatalog(w http.ResponseWriter, r *http.Request, catalogType, catalogID, extra string) {
	torrents, err := s.db.GetTorrents()
	if err != nil {
		s.logger.Error("failed to get torrents for Stremio catalog", "error", err)
		s.writeJSON(w, http.StatusOK, CatalogResponse{Metas: []MetaPreview{}})
		return
	}

	search, skip := parseCatalogExtra(extra, r.URL.Query())

	// Filter torrents having media files and matching catalog type.
	var filtered []*database.Torrent
	for _, t := range torrents {
		if !hasMediaFiles(t) {
			continue
		}

		itemType := classifyTorrent(t)
		switch catalogID {
		case "torrplay_movies":
			if itemType != "movie" {
				continue
			}
		case "torrplay_series":
			if itemType != "series" {
				continue
			}
		case "torrplay_all":
			// matches all media torrents
		default:
			if catalogType != "" && catalogType != "other" && itemType != catalogType {
				continue
			}
		}

		if search != "" {
			query := strings.ToLower(search)
			name := strings.ToLower(t.Name)
			var title string
			if t.Title != nil {
				title = strings.ToLower(*t.Title)
			}
			if !strings.Contains(name, query) && !strings.Contains(title, query) {
				continue
			}
		}

		filtered = append(filtered, t)
	}

	// Sort newest first.
	sort.SliceStable(filtered, func(i, j int) bool {
		iAt, jAt := utils.Val(filtered[i].CreatedAt), utils.Val(filtered[j].CreatedAt)
		if iAt.Equal(jAt) {
			// Tie-break on the infohash so pagination stays deterministic.
			return filtered[i].Hash.HexString() < filtered[j].Hash.HexString()
		}
		return iAt.After(jAt)
	})

	// Apply skip pagination.
	if skip > 0 {
		if skip < len(filtered) {
			filtered = filtered[skip:]
		} else {
			filtered = nil
		}
	}

	if len(filtered) > defaultCatalogLimit {
		filtered = filtered[:defaultCatalogLimit]
	}

	metas := make([]MetaPreview, 0, len(filtered))
	for _, t := range filtered {
		itemType := classifyTorrent(t)
		name := t.Name
		if t.Title != nil && *t.Title != "" {
			name = *t.Title
		}

		var posterURL string
		if t.Poster != nil && *t.Poster != "" {
			posterURL = buildPosterURL(r, s.postersPath, *t.Poster)
		}

		var genres []string
		if t.Category != nil && strings.TrimSpace(*t.Category) != "" {
			genres = append(genres, strings.TrimSpace(*t.Category))
		}

		metas = append(metas, MetaPreview{
			ID:     idPrefix + t.Hash.HexString(),
			Type:   itemType,
			Name:   name,
			Poster: posterURL,
			Genres: genres,
		})
	}

	s.writeJSON(w, http.StatusOK, CatalogResponse{Metas: metas})
}

func (s *Service) handleMeta(w http.ResponseWriter, r *http.Request, _, metaID string) {
	hashHex := strings.TrimPrefix(metaID, idPrefix)
	ih, err := utils.HashFromHexString(hashHex)
	if err != nil {
		s.writeJSON(w, http.StatusOK, MetaResponse{Meta: nil})
		return
	}

	t, err := s.db.GetTorrent(ih)
	if err != nil {
		s.writeJSON(w, http.StatusOK, MetaResponse{Meta: nil})
		return
	}

	// The item type always comes from classification: catalogs advertise that
	// same type, so trusting the request path would just let a client relabel.
	itemType := classifyTorrent(t)

	name := t.Name
	if t.Title != nil && *t.Title != "" {
		name = *t.Title
	}

	var posterURL string
	if t.Poster != nil && *t.Poster != "" {
		posterURL = buildPosterURL(r, s.postersPath, *t.Poster)
	}

	var genres []string
	if t.Category != nil && strings.TrimSpace(*t.Category) != "" {
		genres = append(genres, strings.TrimSpace(*t.Category))
	}

	var videos []Video
	mediaIndex := 0
	for idx, f := range t.Files {
		if !isMediaFile(f.Path) {
			continue
		}

		videoID := fmt.Sprintf("%s%s:%d", idPrefix, ih.HexString(), idx)
		title := f.Name
		if title == "" {
			title = path.Base(f.Path)
		}

		// Season/episode numbering is only meaningful for series; filling it in
		// for a movie would turn extras into fabricated episodes.
		var season, episode int
		if itemType == "series" {
			season, episode, _ = parseSeasonEpisode(f.Path, f.Name, mediaIndex)
		}

		var released string
		if t.CreatedAt != nil {
			released = t.CreatedAt.UTC().Format(time.RFC3339)
		}

		videos = append(videos, Video{
			ID:       videoID,
			Title:    title,
			Season:   season,
			Episode:  episode,
			Released: released,
		})
		mediaIndex++
	}

	meta := &MetaDetail{
		ID:          idPrefix + ih.HexString(),
		Type:        itemType,
		Name:        name,
		Poster:      posterURL,
		Genres:      genres,
		Videos:      videos,
		Description: t.Name,
	}

	if len(videos) == 1 {
		meta.BehaviorHints = &MetaBehaviorHints{
			DefaultVideoID: videos[0].ID,
		}
	}

	s.writeJSON(w, http.StatusOK, MetaResponse{Meta: meta})
}

func (s *Service) handleStream(w http.ResponseWriter, r *http.Request, _ string, streamID, token string) {
	cleanID := strings.TrimPrefix(streamID, idPrefix)
	parts := strings.Split(cleanID, ":")

	if len(parts) == 0 || parts[0] == "" {
		s.writeJSON(w, http.StatusOK, StreamResponse{Streams: []Stream{}})
		return
	}

	ih, err := utils.HashFromHexString(parts[0])
	if err != nil {
		s.writeJSON(w, http.StatusOK, StreamResponse{Streams: []Stream{}})
		return
	}

	t, err := s.db.GetTorrent(ih)
	if err != nil {
		s.writeJSON(w, http.StatusOK, StreamResponse{Streams: []Stream{}})
		return
	}

	fileIdx := -1
	if len(parts) >= 2 {
		if parsedIdx, err := strconv.Atoi(parts[1]); err == nil {
			fileIdx = parsedIdx
		}
	}

	var streams []Stream

	if fileIdx >= 0 {
		// The ID names a specific file: it must exist and be playable, since
		// every ID we hand out points at a media file.
		if fileIdx >= len(t.Files) || !isMediaFile(t.Files[fileIdx].Path) {
			s.writeJSON(w, http.StatusOK, StreamResponse{Streams: []Stream{}})
			return
		}

		f := t.Files[fileIdx]
		streamURL := s.buildStreamURL(r, token, ih, fileIdx, f.Name)
		title := f.Name
		if t.Title != nil && *t.Title != "" {
			title = fmt.Sprintf("%s - %s", *t.Title, f.Name)
		}

		streams = append(streams, Stream{
			Name:  "TorrPlay",
			Title: title,
			URL:   streamURL,
			BehaviorHints: &StreamBehaviorHints{
				BingeGroup: "torrplay-" + ih.HexString(),
			},
		})
	} else {
		// If file index was not specified in the ID, present all media files.
		for idx, f := range t.Files {
			if !isMediaFile(f.Path) {
				continue
			}

			streamURL := s.buildStreamURL(r, token, ih, idx, f.Name)
			streams = append(streams, Stream{
				Name:  "TorrPlay",
				Title: f.Name,
				URL:   streamURL,
				BehaviorHints: &StreamBehaviorHints{
					BingeGroup: "torrplay-" + ih.HexString(),
				},
			})
		}
	}

	s.writeJSON(w, http.StatusOK, StreamResponse{Streams: streams})
}

func (s *Service) handlePlay(w http.ResponseWriter, r *http.Request, hashStr, fileIdxStr string) {
	ih, err := utils.HashFromHexString(hashStr)
	if err != nil {
		http.Error(w, "invalid torrent hash", http.StatusBadRequest)
		return
	}

	fileIdx, err := strconv.Atoi(fileIdxStr)
	if err != nil || fileIdx < 0 {
		http.Error(w, "invalid file index", http.StatusBadRequest)
		return
	}

	if s.streamHandler == nil {
		http.Error(w, "streaming handler not configured", http.StatusServiceUnavailable)
		return
	}

	s.streamHandler(w, r, ih, fileIdx)
}

func (s *Service) buildStreamURL(r *http.Request, token string, ih metainfo.Hash, fileIdx int, fileName string) string {
	baseURL := getBaseURL(r)
	var playPath string
	fileName = path.Base(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "" || fileName == "." {
		fileName = "video.mp4"
	}

	if token != "" {
		playPath = fmt.Sprintf("/stremio/%s/play/%s/%d/%s", token, ih.HexString(), fileIdx, fileName)
	} else {
		playPath = fmt.Sprintf("/stremio/play/%s/%d/%s", ih.HexString(), fileIdx, fileName)
	}

	return baseURL.ResolveReference(&url.URL{Path: playPath}).String()
}

func parseCatalogExtra(extraPath string, query url.Values) (search string, skip int) {
	search = query.Get("search")
	if skipStr := query.Get("skip"); skipStr != "" {
		if s, err := strconv.Atoi(skipStr); err == nil {
			skip = s
		}
	}

	if extraPath != "" {
		extraPath = strings.TrimSuffix(extraPath, ".json")
		for part := range strings.SplitSeq(extraPath, "&") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				k := strings.ToLower(strings.TrimSpace(kv[0]))
				v, err := url.QueryUnescape(strings.TrimSpace(kv[1]))
				if err != nil {
					v = strings.TrimSpace(kv[1])
				}
				if k == "search" && search == "" {
					search = v
				} else if k == "skip" && skip == 0 {
					if s, err := strconv.Atoi(v); err == nil {
						skip = s
					}
				}
			}
		}
	}

	return search, skip
}

type matchKind int

const (
	noMatch matchKind = iota
	weakMatch
	strongMatch
)

func classifyTorrent(t *database.Torrent) string {
	if t.Category != nil {
		cat := strings.ToLower(strings.TrimSpace(*t.Category))
		if cat == "movies" || cat == "movie" || cat == "film" || cat == "films" {
			return "movie"
		}
		if cat == "series" || cat == "tv" || cat == "shows" || cat == "show" || cat == "anime" {
			return "series"
		}
	}

	var nonSampleFiles []api.TorrentFile
	var strongSeriesMatches int
	var totalLength int64
	var maxFileLength int64

	for idx, f := range t.Files {
		if !isMediaFile(f.Path) {
			continue
		}
		if isSampleFile(f.Path, f.Name) {
			continue
		}
		nonSampleFiles = append(nonSampleFiles, f)
		totalLength += f.Length
		if f.Length > maxFileLength {
			maxFileLength = f.Length
		}

		if _, _, match := parseSeasonEpisode(f.Path, f.Name, idx); match == strongMatch {
			strongSeriesMatches++
		}
	}

	// 1. Explicit strong season/episode pattern detected (SxxExx, 1x02, Ep. 03, E04, or Season folder).
	if strongSeriesMatches > 0 {
		return "series"
	}

	// 2. Only one main media file (or none).
	if len(nonSampleFiles) <= 1 {
		return "movie"
	}

	// 3. Multi-CD split files (e.g. CD1/CD2, Part1/Part2).
	if isMultiPartMovie(nonSampleFiles) {
		return "movie"
	}

	// 4. One dominant main video (e.g. movie + extras/bonus clips).
	if totalLength > 0 && float64(maxFileLength)/float64(totalLength) >= 0.70 {
		return "movie"
	}

	// 5. Sequential episode numbers without series prefixes across multi-file torrent (e.g. "01 - Intro.mkv", "02 - Second.mkv").
	if areSequentialFiles(nonSampleFiles) {
		return "series"
	}

	return "movie"
}

func isSampleFile(filePath, fileName string) bool {
	lowerName := strings.ToLower(fileName)
	if lowerName == "" {
		lowerName = strings.ToLower(path.Base(filePath))
	}
	if strings.Contains(lowerName, "sample") {
		return true
	}
	lowerPath := strings.ToLower(filePath)
	return strings.Contains(lowerPath, "/sample/") || strings.Contains(lowerPath, "\\sample\\")
}

func isMultiPartMovie(files []api.TorrentFile) bool {
	if len(files) < 2 {
		return false
	}
	for _, f := range files {
		name := f.Name
		if name == "" {
			name = path.Base(f.Path)
		}
		if !cdPartRegex.MatchString(name) {
			return false
		}
	}
	return true
}

func areSequentialFiles(files []api.TorrentFile) bool {
	if len(files) < 2 {
		return false
	}

	seen := make(map[int]bool, len(files))
	for _, f := range files {
		name := f.Name
		if name == "" {
			name = path.Base(f.Path)
		}
		match := standaloneNumRegex.FindStringSubmatch(name)
		if len(match) != 2 {
			return false
		}
		n, err := strconv.Atoi(match[1])
		if err != nil || seen[n] {
			// Repeated numbers mean the prefix is not an episode counter.
			return false
		}
		seen[n] = true
	}

	return true
}

func detectSeasonFromPath(filePath string) (int, bool) {
	dir := path.Dir(filePath)
	if match := seasonOnlyRegex.FindStringSubmatch(dir); len(match) == 2 {
		if s, err := strconv.Atoi(match[1]); err == nil && s > 0 {
			return s, true
		}
	}
	return 1, false
}

func parseSeasonEpisode(filePath, fileName string, mediaIndex int) (int, int, matchKind) {
	name := fileName
	if name == "" {
		name = path.Base(filePath)
	}

	if match := seasonEpisodeRegex.FindStringSubmatch(name); len(match) == 3 {
		s, errS := strconv.Atoi(match[1])
		e, errE := strconv.Atoi(match[2])
		if errS == nil && errE == nil {
			return s, e, strongMatch
		}
	}

	if match := xEpisodeRegex.FindStringSubmatch(name); len(match) == 3 {
		s, errS := strconv.Atoi(match[1])
		e, errE := strconv.Atoi(match[2])
		if errS == nil && errE == nil {
			return s, e, strongMatch
		}
	}

	if match := epOnlyRegex.FindStringSubmatch(name); len(match) == 2 {
		e, err := strconv.Atoi(match[1])
		if err == nil {
			season, _ := detectSeasonFromPath(filePath)
			return season, e, strongMatch
		}
	}

	if match := eNumRegex.FindStringSubmatch(name); len(match) == 2 {
		e, err := strconv.Atoi(match[1])
		if err == nil {
			season, _ := detectSeasonFromPath(filePath)
			return season, e, strongMatch
		}
	}

	if match := standaloneNumRegex.FindStringSubmatch(name); len(match) == 2 {
		e, err := strconv.Atoi(match[1])
		if err == nil {
			if season, inSeasonDir := detectSeasonFromPath(filePath); inSeasonDir {
				return season, e, strongMatch
			}
			return 1, e, weakMatch
		}
	}

	return 1, mediaIndex + 1, noMatch
}

func hasMediaFiles(t *database.Torrent) bool {
	for _, f := range t.Files {
		if isMediaFile(f.Path) {
			return true
		}
	}
	return false
}

func isMediaFile(filePath string) bool {
	ext := strings.ToLower(path.Ext(filePath))
	if mediaExtensions[ext] {
		return true
	}
	mimeType := mime.TypeByExtension(ext)
	return strings.HasPrefix(mimeType, "video/") || strings.HasPrefix(mimeType, "audio/")
}

func isStremioResource(part string) bool {
	switch part {
	case "manifest.json", "catalog", "meta", "stream", "play":
		return true
	default:
		return false
	}
}

// IsPath reports whether p is a path under the /stremio mount.
func IsPath(p string) bool {
	return p == "/stremio" || strings.HasPrefix(p, "/stremio/")
}

// MetricsPath collapses a Stremio URL into a bounded metrics label. Stremio
// paths embed tokens, infohashes, file indexes and file names, so using them
// verbatim as a Prometheus label would create one series per streamed file.
func MetricsPath(p string) string {
	parts, ok := stremioPathParts(p)
	if !ok {
		return p
	}

	if len(parts) > 0 && !isStremioResource(parts[0]) {
		parts = parts[1:]
	}
	if len(parts) == 0 || !isStremioResource(parts[0]) {
		return "/stremio"
	}

	return "/stremio/" + parts[0]
}

// RedactPathToken redacts sensitive authentication path tokens from Stremio URLs.
func RedactPathToken(p string) string {
	parts, ok := stremioPathParts(p)
	if !ok {
		return p
	}

	if len(parts) > 0 && !isStremioResource(parts[0]) {
		parts[0] = "[REDACTED]"
		return "/stremio/" + strings.Join(parts, "/")
	}

	return p
}

// stremioPathParts splits a Stremio request path into its segments, reporting
// false for paths that are not served by this addon.
func stremioPathParts(p string) ([]string, bool) {
	if !IsPath(p) {
		return nil, false
	}

	cleanPath := strings.Trim(strings.TrimPrefix(p, "/stremio"), "/")
	if cleanPath == "" {
		return nil, false
	}

	return strings.Split(cleanPath, "/"), true
}

// firstForwardedValue returns the first entry of a comma-separated forwarded
// header, which proxies append to on each hop.
func firstForwardedValue(value string) string {
	first, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(first)
}

// isValidHost reports whether a forwarded host is usable in a generated URL.
// The value is client-controlled, so anything that could smuggle a path, query
// or header break is rejected in favour of the request host.
func isValidHost(host string) bool {
	if host == "" || len(host) > 255 {
		return false
	}
	if strings.ContainsAny(host, " \t\r\n/\\?#@\"<>") {
		return false
	}
	for _, c := range host {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func getBaseURL(r *http.Request) *url.URL {
	scheme := "http"
	if r.TLS != nil || firstForwardedValue(r.Header.Get("X-Forwarded-Proto")) == "https" {
		scheme = "https"
	}

	host := r.Host
	if fwdHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); isValidHost(fwdHost) {
		host = fwdHost
	}

	return &url.URL{
		Scheme: scheme,
		Host:   host,
	}
}

func buildPosterURL(r *http.Request, postersPath, posterID string) string {
	baseURL := getBaseURL(r)
	// Poster IDs are plain file names; strip any directory components so a
	// stored value can never escape the posters directory.
	posterID = path.Base(strings.ReplaceAll(posterID, "\\", "/"))
	if posterID == "." || posterID == "/" || posterID == ".." {
		return ""
	}
	p := path.Join(postersPath, posterID)
	if !strings.HasSuffix(strings.ToLower(p), ".jpg") && !strings.HasSuffix(strings.ToLower(p), ".png") {
		p += ".jpg"
	}
	return baseURL.ResolveReference(&url.URL{Path: p}).String()
}

func (s *Service) writeJSON(w http.ResponseWriter, status int, data any) {
	// Encode before writing the header so an encoding failure can still be
	// reported with a clean status instead of a truncated body.
	body, err := json.Marshal(data)
	if err != nil {
		s.logger.Error("failed to encode Stremio response", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.logger.Debug("failed to write Stremio response", "error", err)
	}
}

// manifestVersion reports the addon version advertised to Stremio, which
// caches manifests per version. Unstamped dev builds fall back to a valid
// semver so the manifest stays parseable.
func manifestVersion() string {
	if semverRegex.MatchString(buildinfo.Version) {
		return buildinfo.Version
	}
	return fallbackManifestVersion
}
