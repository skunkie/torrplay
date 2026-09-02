// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stremio

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/testutil"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m)
}

func TestAccessToken(t *testing.T) {
	token := AccessToken("server-secret")
	assert.NotEmpty(t, token)
	assert.True(t, ValidateAccessToken(token, "server-secret"))
	assert.False(t, ValidateAccessToken(token, "different-secret"))
	assert.False(t, ValidateAccessToken("", "server-secret"))
}

func TestBuildStreamURLDoesNotDoubleEscapeFilename(t *testing.T) {
	svc := NewService(nil, nil, "", slog.Default(), nil, nil)
	req := httptest.NewRequest(http.MethodGet, "https://example.com/stremio/stream", http.NoBody)
	ih := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")

	streamURL := svc.buildStreamURL(req, "", ih, 0, "My Movie #1.mkv")
	assert.Contains(t, streamURL, "/My%20Movie%20%231.mkv")
	assert.NotContains(t, streamURL, "%2520")
}

type mockDB struct {
	database.Unimplemented
	torrents []*database.Torrent
}

func (m *mockDB) GetTorrents() ([]*database.Torrent, error) {
	return m.torrents, nil
}

func (m *mockDB) GetTorrent(h metainfo.Hash) (*database.Torrent, error) {
	for _, t := range m.torrents {
		if t.Hash == h {
			return t, nil
		}
	}
	return nil, database.ErrTorrentNotFound
}

type mockImages struct {
	images.Unimplemented
}

func createTestTorrents() []*database.Torrent {
	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	hash2 := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")
	hash3 := metainfo.NewHashFromHex("3333333333333333333333333333333333333333")

	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)

	return []*database.Torrent{
		{
			Torrent: api.Torrent{
				Hash:      hash1,
				Name:      "Big.Buck.Bunny.1080p.mkv",
				Title:     utils.Ptr("Big Buck Bunny"),
				Category:  utils.Ptr("Movies"),
				Poster:    utils.Ptr("poster1"),
				CreatedAt: &t1,
				Files: []api.TorrentFile{
					{
						Name:   "Big.Buck.Bunny.1080p.mkv",
						Path:   "Big.Buck.Bunny.1080p.mkv",
						Length: 1000000,
					},
					{
						Name:   "readme.txt",
						Path:   "readme.txt",
						Length: 100,
					},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:      hash2,
				Name:      "Cosmos.Laundromat.S01",
				Title:     utils.Ptr("Cosmos Laundromat"),
				Category:  utils.Ptr("Series"),
				Poster:    utils.Ptr("poster2"),
				CreatedAt: &t2,
				Files: []api.TorrentFile{
					{
						Name:   "Cosmos.Laundromat.S01E01.mkv",
						Path:   "Season 1/Cosmos.Laundromat.S01E01.mkv",
						Length: 2000000,
					},
					{
						Name:   "Cosmos.Laundromat.S01E02.mkv",
						Path:   "Season 1/Cosmos.Laundromat.S01E02.mkv",
						Length: 2200000,
					},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:      hash3,
				Name:      "Documentary.NoMedia.zip",
				Title:     utils.Ptr("Documentary"),
				CreatedAt: &t3,
				Files: []api.TorrentFile{
					{
						Name:   "data.bin",
						Path:   "data.bin",
						Length: 500000,
					},
				},
			},
		},
	}
}

func setupTestService(t *testing.T, streamHandler StreamHandlerFunc, authValidator AuthValidatorFunc) *Service {
	t.Helper()

	db := &mockDB{torrents: createTestTorrents()}
	img := &mockImages{}
	logger := slog.New(slog.DiscardHandler)

	return NewService(db, img, "/posters/", logger, streamHandler, authValidator)
}

func TestManifest(t *testing.T) {
	service := setupTestService(t, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/stremio/manifest.json", http.NoBody)
	w := httptest.NewRecorder()

	service.ServeHTTP(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var manifest Manifest
	err := json.NewDecoder(resp.Body).Decode(&manifest)
	require.NoError(t, err)

	assert.Equal(t, "org.torrplay.stremio", manifest.ID)
	assert.Equal(t, "TorrPlay", manifest.Name)
	assert.Contains(t, manifest.Resources, "catalog")
	assert.Contains(t, manifest.Resources, "meta")
	assert.Contains(t, manifest.Resources, "stream")
	assert.Contains(t, manifest.Types, "movie")
	assert.Contains(t, manifest.Types, "series")
	assert.Contains(t, manifest.Types, "other")
	assert.NotEmpty(t, manifest.Catalogs)
	assert.Contains(t, manifest.IDPrefixes, "torrplay:")
}

func TestCatalog(t *testing.T) {
	service := setupTestService(t, nil, nil)

	t.Run("all media catalog", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/catalog/other/torrplay_all.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cat CatalogResponse
		err := json.NewDecoder(resp.Body).Decode(&cat)
		require.NoError(t, err)

		// Non-media torrent (hash3) should be filtered out.
		assert.Len(t, cat.Metas, 2)
		// Sorted newest first: Cosmos Laundromat (t2) before Big Buck Bunny (t1).
		assert.Equal(t, "torrplay:2222222222222222222222222222222222222222", cat.Metas[0].ID)
		assert.Equal(t, "series", cat.Metas[0].Type)
		assert.Equal(t, "Cosmos Laundromat", cat.Metas[0].Name)
		assert.Contains(t, cat.Metas[0].Poster, "/posters/poster2.jpg")

		assert.Equal(t, "torrplay:1111111111111111111111111111111111111111", cat.Metas[1].ID)
		assert.Equal(t, "movie", cat.Metas[1].Type)
	})

	t.Run("movies only catalog", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/catalog/movie/torrplay_movies.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cat CatalogResponse
		err := json.NewDecoder(resp.Body).Decode(&cat)
		require.NoError(t, err)

		assert.Len(t, cat.Metas, 1)
		assert.Equal(t, "torrplay:1111111111111111111111111111111111111111", cat.Metas[0].ID)
	})

	t.Run("series only catalog", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/catalog/series/torrplay_series.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cat CatalogResponse
		err := json.NewDecoder(resp.Body).Decode(&cat)
		require.NoError(t, err)

		assert.Len(t, cat.Metas, 1)
		assert.Equal(t, "torrplay:2222222222222222222222222222222222222222", cat.Metas[0].ID)
	})

	t.Run("search filter in path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/catalog/other/torrplay_all/search=bunny.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cat CatalogResponse
		err := json.NewDecoder(resp.Body).Decode(&cat)
		require.NoError(t, err)

		assert.Len(t, cat.Metas, 1)
		assert.Equal(t, "Big Buck Bunny", cat.Metas[0].Name)
	})

	t.Run("search filter in query string", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/catalog/other/torrplay_all.json?search=cosmos", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cat CatalogResponse
		err := json.NewDecoder(resp.Body).Decode(&cat)
		require.NoError(t, err)

		assert.Len(t, cat.Metas, 1)
		assert.Equal(t, "Cosmos Laundromat", cat.Metas[0].Name)
	})

	t.Run("skip pagination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/catalog/other/torrplay_all/skip=1.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var cat CatalogResponse
		err := json.NewDecoder(resp.Body).Decode(&cat)
		require.NoError(t, err)

		assert.Len(t, cat.Metas, 1)
		assert.Equal(t, "torrplay:1111111111111111111111111111111111111111", cat.Metas[0].ID)
	})
}

func TestMeta(t *testing.T) {
	service := setupTestService(t, nil, nil)

	t.Run("movie metadata", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/meta/movie/torrplay:1111111111111111111111111111111111111111.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var metaResp MetaResponse
		err := json.NewDecoder(resp.Body).Decode(&metaResp)
		require.NoError(t, err)
		require.NotNil(t, metaResp.Meta)

		assert.Equal(t, "torrplay:1111111111111111111111111111111111111111", metaResp.Meta.ID)
		assert.Equal(t, "Big Buck Bunny", metaResp.Meta.Name)
		assert.Equal(t, "movie", metaResp.Meta.Type)
		require.Len(t, metaResp.Meta.Videos, 1)
		assert.Equal(t, "torrplay:1111111111111111111111111111111111111111:0", metaResp.Meta.Videos[0].ID)
		assert.Equal(t, 1, metaResp.Meta.Videos[0].Season)
		assert.Equal(t, 1, metaResp.Meta.Videos[0].Episode)
		require.NotNil(t, metaResp.Meta.BehaviorHints)
		assert.Equal(t, metaResp.Meta.Videos[0].ID, metaResp.Meta.BehaviorHints.DefaultVideoID)
	})

	t.Run("series metadata", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/meta/series/torrplay:2222222222222222222222222222222222222222.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var metaResp MetaResponse
		err := json.NewDecoder(resp.Body).Decode(&metaResp)
		require.NoError(t, err)
		require.NotNil(t, metaResp.Meta)

		assert.Equal(t, "series", metaResp.Meta.Type)
		require.Len(t, metaResp.Meta.Videos, 2)
		assert.Equal(t, "torrplay:2222222222222222222222222222222222222222:0", metaResp.Meta.Videos[0].ID)
		assert.Equal(t, 1, metaResp.Meta.Videos[0].Season)
		assert.Equal(t, 1, metaResp.Meta.Videos[0].Episode)

		assert.Equal(t, "torrplay:2222222222222222222222222222222222222222:1", metaResp.Meta.Videos[1].ID)
		assert.Equal(t, 1, metaResp.Meta.Videos[1].Season)
		assert.Equal(t, 2, metaResp.Meta.Videos[1].Episode)
	})

	t.Run("non-existent metadata", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/meta/movie/torrplay:9999999999999999999999999999999999999999.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var metaResp MetaResponse
		err := json.NewDecoder(resp.Body).Decode(&metaResp)
		require.NoError(t, err)
		assert.Nil(t, metaResp.Meta)
	})
}

func TestStream(t *testing.T) {
	service := setupTestService(t, nil, nil)

	t.Run("stream by video id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/stream/movie/torrplay:1111111111111111111111111111111111111111:0.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var streamResp StreamResponse
		err := json.NewDecoder(resp.Body).Decode(&streamResp)
		require.NoError(t, err)
		require.Len(t, streamResp.Streams, 1)

		assert.Equal(t, "TorrPlay", streamResp.Streams[0].Name)
		assert.Contains(t, streamResp.Streams[0].URL, "/stremio/play/1111111111111111111111111111111111111111/0/Big.Buck.Bunny.1080p.mkv")
		require.NotNil(t, streamResp.Streams[0].BehaviorHints)
		assert.Equal(t, "torrplay-1111111111111111111111111111111111111111", streamResp.Streams[0].BehaviorHints.BingeGroup)
	})

	t.Run("stream by whole torrent id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/stream/series/torrplay:2222222222222222222222222222222222222222.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var streamResp StreamResponse
		err := json.NewDecoder(resp.Body).Decode(&streamResp)
		require.NoError(t, err)
		require.Len(t, streamResp.Streams, 2)
	})

	t.Run("non-existent stream", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/stream/movie/torrplay:9999999999999999999999999999999999999999.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var streamResp StreamResponse
		err := json.NewDecoder(resp.Body).Decode(&streamResp)
		require.NoError(t, err)
		assert.Empty(t, streamResp.Streams)
	})
}

func TestPlay(t *testing.T) {
	var calledHash metainfo.Hash
	var calledIdx int

	mockStreamHandler := func(w http.ResponseWriter, r *http.Request, ih metainfo.Hash, fileIdx int) {
		calledHash = ih
		calledIdx = fileIdx
		w.WriteHeader(http.StatusPartialContent)
	}

	service := setupTestService(t, mockStreamHandler, nil)

	t.Run("valid play dispatch", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/play/1111111111111111111111111111111111111111/0/Big.Buck.Bunny.1080p.mkv", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Equal(t, "1111111111111111111111111111111111111111", calledHash.HexString())
		assert.Equal(t, 0, calledIdx)
	})

	t.Run("invalid hash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/play/invalid-hash/0/video.mp4", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestAuth(t *testing.T) {
	authValidator := func(token string) bool {
		return token == "valid-secret-token"
	}

	service := setupTestService(t, nil, authValidator)

	t.Run("unauthorized when no token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/manifest.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("unauthorized with invalid token in path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/bad-token/manifest.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("authorized with valid token in path prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/valid-secret-token/manifest.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("authorized with valid token in query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/manifest.json?token=valid-secret-token", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("stream URL includes token when authenticated in path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/stremio/valid-secret-token/stream/movie/torrplay:1111111111111111111111111111111111111111:0.json", http.NoBody)
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		resp := w.Result()
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var streamResp StreamResponse
		err := json.NewDecoder(resp.Body).Decode(&streamResp)
		require.NoError(t, err)
		require.Len(t, streamResp.Streams, 1)

		assert.Contains(t, streamResp.Streams[0].URL, "/stremio/valid-secret-token/play/1111111111111111111111111111111111111111/0/")
	})
}

func TestParseSeasonEpisode(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		fileName    string
		mediaIndex  int
		wantSeason  int
		wantEpisode int
		wantMatch   matchKind
	}{
		{
			name:        "standard S01E02",
			path:        "Show.S01E02.1080p.mkv",
			fileName:    "Show.S01E02.1080p.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 2,
			wantMatch:   strongMatch,
		},
		{
			name:        "1x05 format",
			path:        "Show.1x05.mkv",
			fileName:    "Show.1x05.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 5,
			wantMatch:   strongMatch,
		},
		{
			name:        "Ep 03 format",
			path:        "Show - Ep. 03.mkv",
			fileName:    "Show - Ep. 03.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 3,
			wantMatch:   strongMatch,
		},
		{
			name:        "e04 format",
			path:        "Show.e04.mkv",
			fileName:    "Show.e04.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 4,
			wantMatch:   strongMatch,
		},
		{
			name:        "parent directory season with bare number",
			path:        "Show/Season 3/07 - Title.mkv",
			fileName:    "07 - Title.mkv",
			mediaIndex:  0,
			wantSeason:  3,
			wantEpisode: 7,
			wantMatch:   strongMatch,
		},
		{
			name:        "parent directory season 1 with bare number is strong match",
			path:        "Show/Season 1/01.mkv",
			fileName:    "01.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 1,
			wantMatch:   strongMatch,
		},
		{
			name:        "bare numeral title in root is weak match",
			path:        "300.mkv",
			fileName:    "300.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 300,
			wantMatch:   weakMatch,
		},
		{
			name:        "Se7en movie title must not match as episode 7",
			path:        "Se7en.1995.1080p.mkv",
			fileName:    "Se7en.1995.1080p.mkv",
			mediaIndex:  0,
			wantSeason:  1,
			wantEpisode: 1,
			wantMatch:   noMatch,
		},
		{
			name:        "regular movie without episode pattern",
			path:        "Inception.2010.mkv",
			fileName:    "Inception.2010.mkv",
			mediaIndex:  3,
			wantSeason:  1,
			wantEpisode: 4,
			wantMatch:   noMatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, e, match := parseSeasonEpisode(tt.path, tt.fileName, tt.mediaIndex)
			assert.Equal(t, tt.wantSeason, s, "season")
			assert.Equal(t, tt.wantEpisode, e, "episode")
			assert.Equal(t, tt.wantMatch, match, "match")
		})
	}
}

func TestClassifyTorrent(t *testing.T) {
	t.Run("single video movie", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "The.Matrix.1999.mkv", Path: "The.Matrix.1999.mkv", Length: 4000000000},
				},
			},
		}
		assert.Equal(t, "movie", classifyTorrent(tor))
	})

	t.Run("numeral movie titles should not become series", func(t *testing.T) {
		for _, name := range []string{"300.mkv", "9.mkv", "21.mkv", "42.mkv", "127.Hours.mkv"} {
			tor := &database.Torrent{
				Torrent: api.Torrent{
					Files: []api.TorrentFile{
						{Name: name, Path: name, Length: 4000000000},
					},
				},
			}
			assert.Equal(t, "movie", classifyTorrent(tor), name)
		}
	})

	t.Run("movie with sample clip", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "The.Matrix.1999.mkv", Path: "The.Matrix.1999.mkv", Length: 4000000000},
					{Name: "sample.mkv", Path: "sample/sample.mkv", Length: 50000000},
				},
			},
		}
		assert.Equal(t, "movie", classifyTorrent(tor))
	})

	t.Run("movie with multi-CD split", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "Fellowship.CD1.avi", Path: "Fellowship.CD1.avi", Length: 700000000},
					{Name: "Fellowship.CD2.avi", Path: "Fellowship.CD2.avi", Length: 700000000},
				},
			},
		}
		assert.Equal(t, "movie", classifyTorrent(tor))
	})

	t.Run("movie with small extras", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "Movie.mkv", Path: "Movie.mkv", Length: 8000000000},
					{Name: "Interview.mp4", Path: "Interview.mp4", Length: 300000000},
					{Name: "Trailer.mp4", Path: "Trailer.mp4", Length: 100000000},
				},
			},
		}
		assert.Equal(t, "movie", classifyTorrent(tor))
	})

	t.Run("Se7en title does not become series", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "Se7en.1995.1080p.mkv", Path: "Se7en.1995.1080p.mkv", Length: 5000000000},
				},
			},
		}
		assert.Equal(t, "movie", classifyTorrent(tor))
	})

	t.Run("TV series with SxxExx", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "Show.S01E01.mkv", Path: "Show.S01E01.mkv", Length: 1000000000},
					{Name: "Show.S01E02.mkv", Path: "Show.S01E02.mkv", Length: 1000000000},
				},
			},
		}
		assert.Equal(t, "series", classifyTorrent(tor))
	})

	t.Run("TV series with sequential numbers", func(t *testing.T) {
		tor := &database.Torrent{
			Torrent: api.Torrent{
				Files: []api.TorrentFile{
					{Name: "01 - Pilot.mkv", Path: "01 - Pilot.mkv", Length: 500000000},
					{Name: "02 - Next.mkv", Path: "02 - Next.mkv", Length: 500000000},
				},
			},
		}
		assert.Equal(t, "series", classifyTorrent(tor))
	})

	t.Run("category override", func(t *testing.T) {
		torMovie := &database.Torrent{
			Torrent: api.Torrent{
				Category: utils.Ptr("Movies"),
				Files: []api.TorrentFile{
					{Name: "File.S01E01.mkv", Path: "File.S01E01.mkv", Length: 1000000000},
				},
			},
		}
		assert.Equal(t, "movie", classifyTorrent(torMovie))

		torSeries := &database.Torrent{
			Torrent: api.Torrent{
				Category: utils.Ptr("Series"),
				Files: []api.TorrentFile{
					{Name: "SingleMovie.mkv", Path: "SingleMovie.mkv", Length: 1000000000},
				},
			},
		}
		assert.Equal(t, "series", classifyTorrent(torSeries))
	})
}
