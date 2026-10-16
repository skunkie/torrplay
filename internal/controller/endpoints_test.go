// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestSavedOpt(t *testing.T) {
	createdAt := time.Now()
	saved := &api.Torrent{CreatedAt: &createdAt, Name: "saved"}
	loaded := &api.Torrent{Name: "loaded"}
	ts := []*api.Torrent{saved, loaded}

	assert.Equal(t, []*api.Torrent{saved}, savedOpt(true)(ts))
	assert.Equal(t, []*api.Torrent{loaded}, savedOpt(false)(ts))
}

func TestSearchOpt(t *testing.T) {
	ts := []*api.Torrent{
		{Name: "Tears.of.Steel.2012", Title: utils.Ptr("Стальные слёзы")},
		{Name: "Sintel.2010"},
	}

	tests := []struct {
		name  string
		query string
		fuzzy bool
		want  []*api.Torrent
	}{
		{name: "matches the title", query: "стальные", want: []*api.Torrent{ts[0]}},
		{name: "matches the name", query: "sintel", want: []*api.Torrent{ts[1]}},
		{name: "passes fuzzy matching through", query: "sintl", fuzzy: true, want: []*api.Torrent{ts[1]}},
		{name: "leaves fuzzy matching off", query: "sintl", want: []*api.Torrent{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, searchOpt(tt.query, tt.fuzzy)(ts))
		})
	}
}

func TestListTorrentsSearchAndSavedFilters(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
	loaded, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(newMetadataFixture("Big Buck Bunnyland Extras")))
	require.NoError(t, err)
	<-loaded.GotInfo()

	list := func(t *testing.T, query url.Values) api.ListTorrents {
		t.Helper()
		rr := doGet(t, ctrl.router, "/api/v1/torrents?"+query.Encode())
		require.Equal(t, http.StatusOK, rr.Code)
		var result api.ListTorrents
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))

		return result
	}
	names := func(result api.ListTorrents) []string {
		out := make([]string, 0, len(result.Torrents))
		for _, t := range result.Torrents {
			out = append(out, t.Name)
		}

		return out
	}

	t.Run("filters by the search query", func(t *testing.T) {
		result := list(t, url.Values{"search": {"buck bunny"}})
		assert.ElementsMatch(t, []string{"Big Buck Bunny", "Big Buck Bunnyland Extras"}, names(result))
		assert.Equal(t, 2, result.Total)
	})

	t.Run("applies fuzzy matching on request", func(t *testing.T) {
		assert.Empty(t, list(t, url.Values{"search": {"laundromt"}}).Torrents)
		assert.Equal(t, []string{"Cosmos Laundromat"}, names(list(t, url.Values{"search": {"laundromt"}, "fuzzy": {"true"}})))
	})

	t.Run("restricts results to saved torrents", func(t *testing.T) {
		result := list(t, url.Values{"saved": {"true"}, "search": {"bunny"}})
		assert.Equal(t, []string{"Big Buck Bunny"}, names(result))
		assert.Equal(t, 1, result.Total)
	})

	t.Run("restricts results to loaded torrents", func(t *testing.T) {
		assert.Equal(t, []string{"Big Buck Bunnyland Extras"}, names(list(t, url.Values{"saved": {"false"}})))
	})

	t.Run("ranks results before paginating them", func(t *testing.T) {
		// The loaded torrent is listed first but matches "bunny" only as a word start.
		result := list(t, url.Values{"limit": {"1"}, "search": {"bunny"}})
		assert.Equal(t, []string{"Big Buck Bunny"}, names(result))
		assert.Equal(t, 2, result.Total)

		result = list(t, url.Values{"limit": {"1"}, "offset": {"1"}, "search": {"bunny"}})
		assert.Equal(t, []string{"Big Buck Bunnyland Extras"}, names(result))
	})
}

func TestListTorrentsFacets(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
	loaded, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(newMetadataFixture("Big Buck Bunnyland Extras")))
	require.NoError(t, err)
	<-loaded.GotInfo()
	for ih, category := range map[metainfo.Hash]string{bunnyHash: "Animation", cosmosHash: "Animation", sintelHash: "Fantasy"} {
		body := strings.NewReader(`{"category":"` + category + `"}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/torrents/"+ih.HexString(), body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		ctrl.router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	}

	list := func(t *testing.T, query url.Values) api.ListTorrents {
		t.Helper()
		rr := doGet(t, ctrl.router, "/api/v1/torrents?"+query.Encode())
		require.Equal(t, http.StatusOK, rr.Code)
		var result api.ListTorrents
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))

		return result
	}
	allFacets := list(t, url.Values{}).Facets
	require.NotEmpty(t, allFacets.Version)
	assert.Equal(t, []api.TorrentCategoryCount{{Count: 2, Name: "Animation"}, {Count: 1, Name: "Fantasy"}}, allFacets.Categories)
	assert.Equal(t, 4, allFacets.Total)

	t.Run("describes every torrent without filters", func(t *testing.T) {
		result := list(t, url.Values{})
		assert.Equal(t, allFacets, result.Facets)
		assert.Equal(t, 4, result.Total)
	})

	t.Run("ignores the search query", func(t *testing.T) {
		result := list(t, url.Values{"fuzzy": {"true"}, "search": {"bunny"}})
		assert.Equal(t, allFacets, result.Facets)
		assert.Equal(t, 2, result.Total)
	})

	t.Run("ignores the category filter", func(t *testing.T) {
		result := list(t, url.Values{"categories": {"Fantasy"}, "search": {"bunny"}})
		assert.Equal(t, allFacets, result.Facets)
		assert.Empty(t, result.Torrents)
		assert.Equal(t, 0, result.Total)
	})

	t.Run("ignores pagination", func(t *testing.T) {
		result := list(t, url.Values{"limit": {"1"}})
		assert.Equal(t, allFacets, result.Facets)
		assert.Len(t, result.Torrents, 1)
	})

	t.Run("decorates the listed torrents", func(t *testing.T) {
		result := list(t, url.Values{"limit": {"1"}, "search": {"bunny"}})
		require.Len(t, result.Torrents, 1)
		assert.NotNil(t, result.Torrents[0].Active)
	})

	t.Run("follows the other filters", func(t *testing.T) {
		result := list(t, url.Values{"saved": {"false"}, "search": {"bunny"}})
		assert.Equal(t, []api.TorrentCategoryCount{}, result.Facets.Categories)
		assert.Equal(t, 1, result.Facets.Total)
		assert.NotEqual(t, allFacets.Version, result.Facets.Version)

		result = list(t, url.Values{"hashes": {bunnyHash.HexString() + "," + sintelHash.HexString()}})
		assert.Equal(t, []api.TorrentCategoryCount{{Count: 1, Name: "Animation"}, {Count: 1, Name: "Fantasy"}}, result.Facets.Categories)
		assert.Equal(t, 2, result.Facets.Total)
	})
}

func TestListTorrentsVersion(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
	get := func(t *testing.T, query url.Values, ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/torrents?"+query.Encode(), http.NoBody)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rr := httptest.NewRecorder()
		ctrl.router.ServeHTTP(rr, req)
		return rr
	}
	version := func(t *testing.T, query url.Values) string {
		t.Helper()
		rr := get(t, query, "")
		require.Equal(t, http.StatusOK, rr.Code)
		var result api.ListTorrents
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
		assert.True(t, strings.HasPrefix(rr.Header().Get("ETag"), `W/"`+result.Facets.Version+`.`), rr.Header().Get("ETag"))
		assert.Equal(t, "private, no-cache", rr.Header().Get("Cache-Control"))
		return result.Facets.Version
	}
	etag := func(t *testing.T, query url.Values) string {
		t.Helper()
		rr := get(t, query, "")
		require.Equal(t, http.StatusOK, rr.Code)
		return rr.Header().Get("ETag")
	}
	patch := func(t *testing.T, ih metainfo.Hash, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/torrents/"+ih.HexString(), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		ctrl.router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	}

	initial := version(t, url.Values{})

	t.Run("stays the same for an unchanged listing", func(t *testing.T) {
		assert.Equal(t, initial, version(t, url.Values{}))
	})

	t.Run("matches between a search and the full list", func(t *testing.T) {
		assert.Equal(t, initial, version(t, url.Values{"fuzzy": {"true"}, "search": {"bunny"}}))
		assert.Equal(t, initial, version(t, url.Values{"categories": {"Fantasy"}, "limit": {"1"}}))
	})

	t.Run("answers a matching etag without a body", func(t *testing.T) {
		tag := etag(t, url.Values{})
		rr := get(t, url.Values{}, `"other", `+tag)
		assert.Equal(t, http.StatusNotModified, rr.Code)
		assert.Empty(t, rr.Body.String())
		assert.Equal(t, tag, rr.Header().Get("ETag"))
	})

	t.Run("tags each query apart", func(t *testing.T) {
		full := etag(t, url.Values{})
		search := url.Values{"fuzzy": {"true"}, "search": {"bunny"}}
		searchTag := etag(t, search)
		assert.Equal(t, searchTag, etag(t, search))
		assert.NotEqual(t, full, searchTag)
		assert.NotEqual(t, full, etag(t, url.Values{"limit": {"1"}}))

		rr := get(t, url.Values{"limit": {"1"}}, full)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.NotEmpty(t, rr.Body.String())
	})

	t.Run("answers a stale etag in full", func(t *testing.T) {
		rr := get(t, url.Values{}, `W/"stale"`)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.NotEmpty(t, rr.Body.String())
	})

	t.Run("changes when a torrent changes", func(t *testing.T) {
		patch(t, bunnyHash, `{"category":"Animation"}`)
		edited := version(t, url.Values{})
		assert.NotEqual(t, initial, edited)

		patch(t, bunnyHash, `{"category":""}`)
		assert.NotEqual(t, edited, version(t, url.Values{}))
	})

	t.Run("changes when a referenced poster image is stored", func(t *testing.T) {
		poster := []byte("\x89PNG\r\n\x1a\nposter")
		sum := sha256.Sum256(poster)
		row, err := ctrl.db.GetTorrent(sintelHash)
		require.NoError(t, err)
		row.Poster = utils.Ptr(hex.EncodeToString(sum[:]))
		require.NoError(t, ctrl.db.UpdateTorrent(row))
		missing := version(t, url.Values{})

		_, err = ctrl.images.SaveData(poster)
		require.NoError(t, err)

		rr := get(t, url.Values{}, `W/"`+missing+`"`)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("changes when a torrent is removed", func(t *testing.T) {
		before := version(t, url.Values{})
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/torrents/"+cosmosHash.HexString(), http.NoBody)
		rr := httptest.NewRecorder()
		ctrl.router.ServeHTTP(rr, req)
		require.Less(t, rr.Code, 300, rr.Body.String())

		assert.NotEqual(t, before, version(t, url.Values{}))
	})
}

// unversionableDB lists, in place of the saved torrents, one without a creation
// time, which is versioned by its encoding, and which cannot be encoded.
type unversionableDB struct {
	database.DatabaseInterface
}

func (unversionableDB) GetTorrentsAtRevision() ([]*database.Torrent, int, error) {
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	return []*database.Torrent{{Torrent: api.Torrent{Hash: sintelHash, Name: "Sintel", UpdatedAt: &invalid}}}, 1, nil
}

func TestListTorrentsFailsForAListingThatCannotBeVersioned(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	ctrl.db = unversionableDB{ctrl.db}

	rr := doGet(t, ctrl.router, "/api/v1/torrents")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Contains(t, rr.Body.String(), "version torrent "+sintelHash.HexString())
	assert.Empty(t, rr.Header().Get("ETag"))
}

func TestListTorrentsOrdersSameNamedLoadedTorrentsByHash(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	hashes := make([]metainfo.Hash, 0, 6)
	for i := range 6 {
		info := metainfo.Info{
			Name:        "Spring",
			PieceLength: 64,
			Pieces:      bytes.Repeat([]byte{byte(i + 1)}, 20),
			Files:       []metainfo.FileInfo{{Length: 64, Path: []string{"Spring.mp4"}}},
		}
		infoBytes, err := bencode.Marshal(info)
		require.NoError(t, err)
		loaded, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(&metainfo.MetaInfo{InfoBytes: infoBytes}))
		require.NoError(t, err)
		<-loaded.GotInfo()
		hashes = append(hashes, loaded.InfoHash())
	}
	slices.SortFunc(hashes, func(a, b metainfo.Hash) int { return bytes.Compare(a[:], b[:]) })

	versions := make([]string, 0, 10)
	for range 10 {
		rr := doGet(t, ctrl.router, "/api/v1/torrents")
		require.Equal(t, http.StatusOK, rr.Code)
		var result api.ListTorrents
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
		listed := make([]metainfo.Hash, 0, len(result.Torrents))
		for _, listedTorrent := range result.Torrents {
			listed = append(listed, listedTorrent.Hash)
		}
		require.Equal(t, hashes, listed)
		versions = append(versions, result.Facets.Version)
	}
	assert.Len(t, slices.Compact(versions), 1)
}

func TestListingETag(t *testing.T) {
	request := func(target, host string, tls bool) *http.Request {
		r := httptest.NewRequest(http.MethodGet, target, http.NoBody)
		r.Host = host
		if !tls {
			r.TLS = nil
		}
		return r
	}
	base := listingETag("v1", request("http://a/api/v1/torrents?search=x&limit=1", "a", false))

	assert.True(t, strings.HasPrefix(base, `W/"v1.`))
	assert.True(t, strings.HasSuffix(base, `"`))
	assert.Equal(t, base, listingETag("v1", request("http://a/api/v1/torrents?limit=1&search=x", "a", false)), "parameter order")
	assert.NotEqual(t, base, listingETag("v2", request("http://a/api/v1/torrents?search=x&limit=1", "a", false)))
	assert.NotEqual(t, base, listingETag("v1", request("http://a/api/v1/torrents?search=y&limit=1", "a", false)))
	assert.NotEqual(t, base, listingETag("v1", request("http://b/api/v1/torrents?search=x&limit=1", "b", false)))
	assert.NotEqual(t, base, listingETag("v1", request("https://a/api/v1/torrents?search=x&limit=1", "a", true)))
}

func TestMatchesETag(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "matches the same weak tag", header: `W/"v1"`, want: true},
		{name: "matches the strong form weakly", header: `"v1"`, want: true},
		{name: "matches one of a list", header: `"v0", W/"v1"`, want: true},
		{name: "matches any tag", header: "*", want: true},
		{name: "rejects another tag", header: `W/"v2"`},
		{name: "rejects an empty header", header: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchesETag(tt.header, `W/"v1"`))
		})
	}
}

func TestTorrentsVersion(t *testing.T) {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sintel := func() *api.Torrent {
		return &api.Torrent{Active: utils.Ptr(false), CreatedAt: &createdAt, Hash: sintelHash, Name: "Sintel", Poster: utils.Ptr("sintel-poster")}
	}
	bunny := func() *api.Torrent { return &api.Torrent{Hash: bunnyHash, Name: "Big Buck Bunny"} }
	stored := func(string) bool { return true }
	versionOf := func(t *testing.T, revision int, ts []*api.Torrent, posterStored func(string) bool) string {
		t.Helper()
		version, err := torrentsVersion("instance", revision, ts, posterStored)
		require.NoError(t, err)
		return version
	}
	base := versionOf(t, 1, []*api.Torrent{sintel(), bunny()}, stored)

	t.Run("is stable for equal torrents", func(t *testing.T) {
		assert.Equal(t, base, versionOf(t, 1, []*api.Torrent{sintel(), bunny()}, stored))
	})

	t.Run("leaves saved fields to the revision", func(t *testing.T) {
		edited := sintel()
		edited.Title = utils.Ptr("Sintel 2010")
		assert.Equal(t, base, versionOf(t, 1, []*api.Torrent{edited, bunny()}, stored))
		assert.NotEqual(t, base, versionOf(t, 2, []*api.Torrent{edited, bunny()}, stored))
	})

	t.Run("changes with a field of a loaded torrent", func(t *testing.T) {
		edited := bunny()
		edited.Title = utils.Ptr("Big Buck Bunny 2008")
		assert.NotEqual(t, base, versionOf(t, 1, []*api.Torrent{sintel(), edited}, stored))
	})

	t.Run("changes with activity", func(t *testing.T) {
		active := sintel()
		active.Active = utils.Ptr(true)
		assert.NotEqual(t, base, versionOf(t, 1, []*api.Torrent{active, bunny()}, stored))
	})

	t.Run("changes with whether the poster image is stored", func(t *testing.T) {
		missing := func(string) bool { return false }
		assert.NotEqual(t, base, versionOf(t, 1, []*api.Torrent{sintel(), bunny()}, missing))
	})

	t.Run("changes with whether the poster image of a torrent without a creation time is stored", func(t *testing.T) {
		restored := bunny()
		restored.Poster = utils.Ptr("bunny-poster")
		missing := func(id string) bool { return id != "bunny-poster" }
		assert.NotEqual(t,
			versionOf(t, 1, []*api.Torrent{sintel(), restored}, stored),
			versionOf(t, 1, []*api.Torrent{sintel(), restored}, missing))
	})

	t.Run("changes with the instance", func(t *testing.T) {
		restarted, err := torrentsVersion("restarted", 1, []*api.Torrent{sintel(), bunny()}, stored)
		require.NoError(t, err)
		assert.NotEqual(t, base, restarted)
	})

	t.Run("fails for a loaded torrent that cannot be encoded", func(t *testing.T) {
		invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		_, err := torrentsVersion("instance", 1, []*api.Torrent{{UpdatedAt: &invalid}}, stored)
		assert.Error(t, err)
	})

	t.Run("changes with membership and order", func(t *testing.T) {
		assert.NotEqual(t, base, versionOf(t, 1, []*api.Torrent{sintel()}, stored))
		assert.NotEqual(t, base, versionOf(t, 1, []*api.Torrent{bunny(), sintel()}, stored))
	})
}

// countingImages lists ids and counts how often it does.
type countingImages struct {
	images.Unimplemented
	ids   []string
	err   error
	lists int
}

func (s *countingImages) ListIDs() ([]string, error) {
	s.lists++
	return s.ids, s.err
}

func TestController_StoredPosters(t *testing.T) {
	t.Run("lists no images until asked", func(t *testing.T) {
		store := &countingImages{ids: []string{"sintel-poster"}}
		c := &Controller{images: store}

		stored := c.storedPosters()
		assert.Zero(t, store.lists)

		assert.True(t, stored("sintel-poster"))
		assert.False(t, stored("bunny-poster"))
		assert.Equal(t, 1, store.lists)
	})

	t.Run("reports none stored when the images cannot be listed", func(t *testing.T) {
		c := &Controller{images: &countingImages{ids: []string{"sintel-poster"}, err: errors.New("closed")}}
		assert.False(t, c.storedPosters()("sintel-poster"))
	})

	t.Run("reports none stored without an image store", func(t *testing.T) {
		assert.False(t, (&Controller{}).storedPosters()("sintel-poster"))
	})
}

func TestTorrentFacets(t *testing.T) {
	tests := []struct {
		name string
		ts   []*api.Torrent
		want api.TorrentFacets
	}{
		{
			name: "returns empty categories for no torrents",
			want: api.TorrentFacets{Categories: []api.TorrentCategoryCount{}},
		},
		{
			name: "counts torrents per category ordered by name",
			ts: []*api.Torrent{
				{Category: utils.Ptr("Shows")},
				{Category: utils.Ptr("Movies")},
				{Category: utils.Ptr("Shows")},
			},
			want: api.TorrentFacets{
				Categories: []api.TorrentCategoryCount{{Count: 1, Name: "Movies"}, {Count: 2, Name: "Shows"}},
				Total:      3,
			},
		},
		{
			name: "counts uncategorized torrents only in the total",
			ts:   []*api.Torrent{{}, {Category: utils.Ptr("")}, {Category: utils.Ptr("Movies")}},
			want: api.TorrentFacets{Categories: []api.TorrentCategoryCount{{Count: 1, Name: "Movies"}}, Total: 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want.Version = "v1"
			assert.Equal(t, tt.want, torrentFacets(tt.ts, "v1"))
		})
	}
}

// addStalledTorrents loads count torrents the client can never fetch metadata
// for, since the test client has no peers, trackers, or DHT.
func addStalledTorrents(t *testing.T, ctrl *Controller, count int) {
	t.Helper()
	for i := range count {
		var ih metainfo.Hash
		ih[0], ih[1] = 0xee, byte(i)
		_, _ = ctrl.client.AddTorrentInfoHash(ih)
	}
}

// tryLockWithin reports whether mu could be locked for writing within timeout,
// releasing it again if so.
func tryLockWithin(mu *sync.RWMutex, timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if mu.TryLock() {
			mu.Unlock()
			return true
		}
	}

	return false
}

func TestGotInfoBy(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	primeSampleMetadata(t, ctrl, bunnyHash)
	resolved, ok := ctrl.client.Torrent(bunnyHash)
	require.True(t, ok)
	<-resolved.GotInfo()
	stalled, _ := ctrl.client.AddTorrentInfoHash(metainfo.Hash{0xee})

	expired, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("prefers resolved metadata over an expired context", func(t *testing.T) {
		assert.True(t, gotInfoBy(expired, resolved))
	})

	t.Run("gives up on unresolved metadata once the context is done", func(t *testing.T) {
		assert.False(t, gotInfoBy(expired, stalled))
	})
}

func TestListTorrentsWaitsForStalledMetadataOutsideTheLock(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	addAllSampleTorrents(t, ctrl)
	addStalledTorrents(t, ctrl, 10)

	t.Run("shares one wait across stalled torrents", func(t *testing.T) {
		start := time.Now()
		rr := doGet(t, ctrl.router, "/api/v1/torrents")
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Less(t, time.Since(start), 5*loadedInfoWait)

		var result api.ListTorrents
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
		assert.Equal(t, len(samples), result.Total)
	})

	t.Run("stops waiting once the request is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/torrents", http.NoBody)

		start := time.Now()
		ts, err := ctrl.listTorrents(req)
		require.NoError(t, err)
		assert.Less(t, time.Since(start), loadedInfoWait/2)
		assert.Len(t, ts, len(samples))
	})

	for name, newRequest := range map[string]func() *http.Request{
		"lets writers take the lock while listing": func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/api/v1/torrents", http.NoBody)
		},
		"lets writers take the lock while building a playlist": func() *http.Request {
			return httptest.NewRequest(http.MethodGet, "/api/v1/playlist", http.NoBody)
		},
		"lets writers take the lock while listing for TorrServer clients": func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/torrents", strings.NewReader(`{"action":"list"}`))
			req.Header.Set("Content-Type", "application/json")
			return req
		},
	} {
		t.Run(name, func(t *testing.T) {
			listed := make(chan int)
			go func() {
				rec := httptest.NewRecorder()
				ctrl.router.ServeHTTP(rec, newRequest())
				listed <- rec.Code
			}()
			time.Sleep(loadedInfoWait / 5)

			// Polling rather than blocking keeps a writer that is never let in
			// from deadlocking a handler that takes the read lock twice.
			assert.True(t, tryLockWithin(&ctrl.mu, loadedInfoWait/2))
			assert.Equal(t, http.StatusOK, <-listed)
		})
	}
}
