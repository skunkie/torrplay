// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMethodOverrideRewritesPostToRealVerb(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	file, err := os.Open(sintelTorrentFile)
	require.NoError(t, err)
	metaInfo, err := metainfo.Load(file)
	require.NoError(t, file.Close())
	require.NoError(t, err)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(metaInfo))
	require.NoError(t, err)
	<-to.GotInfo()
	ih := to.InfoHash()
	preloadURL := "/api/v1/torrents/" + ih.HexString() + "/preload"

	doRequest := func(method, target, body, overrideHeader string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if overrideHeader != "" {
			request.Header.Set(methodOverrideHeader, overrideHeader)
		}
		ctrl.router.ServeHTTP(recorder, request)
		return recorder
	}

	// A plain POST to a PUT-only route (what a native bridge that can't send
	// PUT actually sends) must fail to route, exactly like the real bug.
	plainPost := doRequest(http.MethodPost, preloadURL, `{"file_index":0}`, "")
	assert.Equal(t, http.StatusNotFound, plainPost.Code)

	// The same POST with the override header must be routed as a real PUT.
	overriddenPost := doRequest(http.MethodPost, preloadURL, `{"file_index":0}`, "PUT")
	assert.Equal(t, http.StatusOK, overriddenPost.Code)

	// A genuine POST (the common case, no override header) must still route normally.
	magnet := samples[bunnyHash]
	primeSampleMetadata(t, ctrl, bunnyHash)
	genuinePost := doRequest(http.MethodPost, "/api/v1/torrents", `{"magnet":"`+magnet+`"}`, "")
	assert.Equal(t, http.StatusCreated, genuinePost.Code)

	// An unsupported or malformed override value is ignored, not routed.
	assert.Equal(t, http.StatusNotFound, doRequest(http.MethodPost, preloadURL, `{"file_index":0}`, "TRACE").Code)
}
