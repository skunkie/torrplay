// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/ethulhu/helix/upnpav"
	"github.com/ethulhu/helix/upnpav/contentdirectory/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/images"
)

type mockImages struct {
	images.Unimplemented
}

func (m *mockImages) SaveData(data []byte) (*string, error) {
	s := "test"
	return &s, nil
}

func (m *mockImages) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

func TestContentDirectory_BrowseMetadata(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	didl, err := service.contentDirectory.BrowseMetadata(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), nil)
	if err != nil {
		t.Fatalf("BrowseMetadata returned an error: %v", err)
	}

	if didl == nil {
		t.Fatal("BrowseMetadata returned nil DIDLLite")
	}

	if len(didl.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(didl.Containers))
	}

	if didl.Containers[0].Title != "Test Torrent" {
		t.Errorf("expected title 'Test Torrent', got '%s'", didl.Containers[0].Title)
	}
}

func TestContentDirectory_BrowseChildren(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	didl, err := service.contentDirectory.BrowseChildren(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), nil)
	if err != nil {
		t.Fatalf("BrowseChildren returned an error: %v", err)
	}

	if didl == nil {
		t.Fatal("BrowseChildren returned nil DIDLLite")
	}

	if len(didl.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(didl.Items))
	}

	if didl.Items[0].Title != testTorrentFileName {
		t.Errorf("expected title '%s', got '%s'", testTorrentFileName, didl.Items[0].Title)
	}
}

func TestContentDirectory_Search(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	criteria, err := search.Parse(`(dc:title contains "Test")`)
	if err != nil {
		t.Fatalf("failed to create search criteria: %v", err)
	}

	didl, err := service.contentDirectory.Search(context.Background(), "0", criteria)
	if err != nil {
		t.Fatalf("Search returned an error: %v", err)
	}

	if len(didl.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(didl.Containers))
	}

	if didl.Containers[0].Title != "Test Torrent" {
		t.Errorf("expected title 'Test Torrent', got '%s'", didl.Containers[0].Title)
	}
}

func TestContentDirectory_fileURI(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	didl, err := service.contentDirectory.BrowseChildren(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), nil)
	if err != nil {
		t.Fatalf("BrowseChildren returned an error: %v", err)
	}

	if len(didl.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(didl.Items))
	}

	uriString := didl.Items[0].Resources[0].URI
	uri, err := url.Parse(uriString)
	if err != nil {
		t.Fatalf("failed to parse URI: %v", err)
	}

	expectedPath := "/api/v1/stream/" + testTorrentHash.HexString()
	if uri.Path != expectedPath {
		t.Errorf("URI path is incorrect, got: %s, want: %s", uri.Path, expectedPath)
	}

	filepath := uri.Query().Get("path")
	if filepath != testTorrentFileName {
		t.Errorf("file path query parameter is incorrect, got: %s, want: %s", filepath, testTorrentFileName)
	}
}

func TestBrowseTorrentWithIcon(t *testing.T) {
	db := &mockDB{}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")

	didl, err := cd.browseTorrent(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()))
	require.NoError(t, err)
	require.Len(t, didl.Items, 1)

	item := didl.Items[0]
	expectedIconURL := fmt.Sprintf("%s/icons/media/videofile-128x128.png", baseURL)
	assert.Equal(t, expectedIconURL, item.Icon.String())

	foundIconResource := false
	for _, res := range item.Resources {
		if res.URI == expectedIconURL {
			foundIconResource = true
			assert.Equal(t, "image/png", res.ProtocolInfo.ContentFormat)
			break
		}
	}
	assert.True(t, foundIconResource, "icon resource not found in item")
}
