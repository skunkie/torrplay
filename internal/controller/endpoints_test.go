// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
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
	allFacets := api.TorrentFacets{
		Categories: []api.TorrentCategoryCount{{Count: 2, Name: "Animation"}, {Count: 1, Name: "Fantasy"}},
		Total:      4,
	}

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
		assert.Equal(t, api.TorrentFacets{Categories: []api.TorrentCategoryCount{}, Total: 1}, result.Facets)

		result = list(t, url.Values{"hashes": {bunnyHash.HexString() + "," + sintelHash.HexString()}})
		assert.Equal(t, api.TorrentFacets{
			Categories: []api.TorrentCategoryCount{{Count: 1, Name: "Animation"}, {Count: 1, Name: "Fantasy"}},
			Total:      2,
		}, result.Facets)
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
			assert.Equal(t, tt.want, torrentFacets(tt.ts))
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
