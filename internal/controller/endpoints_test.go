// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
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
