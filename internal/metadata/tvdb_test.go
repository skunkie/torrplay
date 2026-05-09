// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metadata

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/torrplay/torrplay/internal/api"
)

func TestTVDBUpdateMetadata(t *testing.T) {
	var server *httptest.Server
	var lastRequestURL *url.URL
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			lastRequestURL = r.URL
		}
		switch r.URL.Path {
		case "/login":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"data": {"token": "test_token"}}`)
		case "/search":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"data": [{"tvdb_id": "12345", "name": "New Test Movie Title", "image_url": "`+server.URL+`/poster.jpg"}]}`)
		case "/poster.jpg":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "poster data")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	thetvdbBaseURL = server.URL

	client, err := NewTVDBClient("test_api_key")
	if err != nil {
		t.Fatalf("NewTVDBClient returned an error: %v", err)
	}

	backup := api.Backup{
		Torrents: []api.Torrent{
			{
				Name: "Test Movie (2023)",
			},
		},
		Posters: make(map[string]openapi_types.File),
	}

	opts := Options{
		Category: true,
		Language: "eng",
		Poster:   true,
		Title:    true,
	}

	updatedBackup, err := client.UpdateMetadata(backup, opts)
	if err != nil {
		t.Fatalf("UpdateMetadata returned an error: %v", err)
	}

	if len(updatedBackup.Torrents) != 1 {
		t.Fatalf("expected 1 torrent in backup, got %d", len(updatedBackup.Torrents))
	}

	if updatedBackup.Torrents[0].Poster == nil {
		t.Fatal("expected torrent to have a poster, but it was nil")
	}

	if *updatedBackup.Torrents[0].Title != "New Test Movie Title" {
		t.Fatalf("expected torrent title to be 'New Test Movie Title', got '%s'", *updatedBackup.Torrents[0].Title)
	}

	if len(updatedBackup.Posters) != 1 {
		t.Fatalf("expected 1 poster in backup, got %d", len(updatedBackup.Posters))
	}

	if *updatedBackup.Torrents[0].Category != "Movies" {
		t.Fatalf("expected torrent category to be 'Movies', got '%s'", *updatedBackup.Torrents[0].Category)
	}

	if lastRequestURL == nil {
		t.Fatal("no search request was made to the server")
	}

	if lang := lastRequestURL.Query().Get("language"); lang != "eng" {
		t.Fatalf("expected language to be 'eng', but got '%s'", lang)
	}
}
