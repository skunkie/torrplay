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
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/ethulhu/helix/upnpav"
	"github.com/ethulhu/helix/upnpav/contentdirectory"
	"github.com/ethulhu/helix/upnpav/contentdirectory/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
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

	ctx := context.Background()

	t.Run("root metadata", func(t *testing.T) {
		didl, err := service.contentDirectory.BrowseMetadata(ctx, rootID, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(rootID), didl.Containers[0].ID)
		assert.Equal(t, "TorrPlay", didl.Containers[0].Title)
		assert.Equal(t, 3, didl.Containers[0].ChildCount)
	})

	t.Run("categorized containers metadata", func(t *testing.T) {
		for _, containerID := range []string{allTorrentsContainerID, recentlyAddedContainerID, recentlyViewedContainerID} {
			didl, err := service.contentDirectory.BrowseMetadata(ctx, upnpav.ObjectID(containerID), nil)
			require.NoError(t, err)
			require.NotNil(t, didl)
			require.Len(t, didl.Containers, 1)
			assert.Equal(t, upnpav.ObjectID(containerID), didl.Containers[0].ID)
		}
	})

	t.Run("torrent container metadata", func(t *testing.T) {
		didl, err := service.contentDirectory.BrowseMetadata(ctx, upnpav.ObjectID(testTorrentHash.HexString()), nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, "Test Torrent", didl.Containers[0].Title)
		assert.Equal(t, upnpav.ObjectID(allTorrentsContainerID), didl.Containers[0].Parent)
	})

	t.Run("non-existent container metadata", func(t *testing.T) {
		_, err := service.contentDirectory.BrowseMetadata(ctx, "nonexistent-container", nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("item metadata", func(t *testing.T) {
		itemID := upnpav.ObjectID(fmt.Sprintf("%s/0", testTorrentHash.HexString()))
		didl, err := service.contentDirectory.BrowseMetadata(ctx, itemID, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Items, 1)
		assert.Equal(t, testTorrentFileName, didl.Items[0].Title)
		assert.Equal(t, itemID, didl.Items[0].ID)
	})

	t.Run("non-existent item metadata", func(t *testing.T) {
		_, err := service.contentDirectory.BrowseMetadata(ctx, "0000000000000000000000000000000000000000/0", nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)

		_, err = service.contentDirectory.BrowseMetadata(ctx, upnpav.ObjectID(fmt.Sprintf("%s/99", testTorrentHash.HexString())), nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})
}

func TestContentDirectory_BrowseChildren(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	didl, totalMatches, err := service.contentDirectory.BrowseChildren(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), 0, 0, nil)
	if err != nil {
		t.Fatalf("BrowseChildren returned an error: %v", err)
	}

	if didl == nil {
		t.Fatal("BrowseChildren returned nil DIDLLite")
	}

	if totalMatches != 1 {
		t.Errorf("expected totalMatches 1, got %d", totalMatches)
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

	t.Run("search by title", func(t *testing.T) {
		criteria, err := search.Parse(`(dc:title contains "Test")`)
		require.NoError(t, err)

		didl, totalMatches, err := service.contentDirectory.Search(context.Background(), "0", criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(2), totalMatches) // 1 container + 1 item
		assert.Len(t, didl.Containers, 1)
		assert.Len(t, didl.Items, 1)
		assert.Equal(t, "Test Torrent", didl.Containers[0].Title)
		assert.Equal(t, testTorrentFileName, didl.Items[0].Title)
	})

	t.Run("search video items by upnp:class (Vimu query)", func(t *testing.T) {
		criteria, err := search.Parse(`upnp:class derivedfrom "object.item.videoItem"`)
		require.NoError(t, err)

		didl, totalMatches, err := service.contentDirectory.Search(context.Background(), "0", criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(1), totalMatches)
		assert.Empty(t, didl.Containers)
		require.Len(t, didl.Items, 1)
		assert.Equal(t, testTorrentFileName, didl.Items[0].Title)
		assert.Equal(t, upnpav.VideoItem, didl.Items[0].Class)
	})

	t.Run("search scoped to torrent container", func(t *testing.T) {
		criteria, err := search.Parse(`upnp:class derivedfrom "object.item.videoItem"`)
		require.NoError(t, err)

		didl, totalMatches, err := service.contentDirectory.Search(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(1), totalMatches)
		assert.Empty(t, didl.Containers)
		require.Len(t, didl.Items, 1)
		assert.Equal(t, testTorrentFileName, didl.Items[0].Title)
	})

	t.Run("search pagination offset beyond count", func(t *testing.T) {
		criteria, err := search.Parse(`upnp:class derivedfrom "object.item.videoItem"`)
		require.NoError(t, err)

		didlEmpty, totalMatchesEmpty, err := service.contentDirectory.Search(context.Background(), "0", criteria, 1, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(1), totalMatchesEmpty)
		assert.Empty(t, didlEmpty.Items)
	})
}

func TestContentDirectory_fileURI(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	didl, _, err := service.contentDirectory.BrowseChildren(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), 0, 0, nil)
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

func TestBrowseTorrent_ItemProperties(t *testing.T) {
	db := &mockDB{}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")

	didl, totalMatches, err := cd.browseTorrent(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), 0, 0)
	require.NoError(t, err)
	require.Equal(t, uint(1), totalMatches)
	require.Len(t, didl.Items, 1)

	item := didl.Items[0]
	assert.Equal(t, testTorrentFileName, item.Title)
	expectedIconURL := fmt.Sprintf("%s/icons/media/videofile-128x128.png", baseURL)
	require.NotNil(t, item.Icon)
	assert.Equal(t, expectedIconURL, item.Icon.String())
	assert.Contains(t, item.AlbumArtURIs, expectedIconURL)
}

type mockDBWithTorrent struct {
	database.Unimplemented
	torrent *database.Torrent
}

func (m *mockDBWithTorrent) GetTorrent(ih metainfo.Hash) (*database.Torrent, error) {
	return m.torrent, nil
}

func TestBrowseTorrent_WithPoster(t *testing.T) {
	poster := "test-poster.jpg"
	torrentWithPoster := &database.Torrent{
		Torrent: api.Torrent{
			Hash:   testTorrentHash,
			Name:   "Poster Torrent",
			Poster: &poster,
			Files: []api.TorrentFile{
				{Path: "movie.mp4", Name: "movie.mp4", Length: 2048},
			},
		},
	}
	db := &mockDBWithTorrent{
		torrent: torrentWithPoster,
	}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")

	didl, totalMatches, err := cd.browseTorrent(context.Background(), upnpav.ObjectID(testTorrentHash.HexString()), 0, 0)
	require.NoError(t, err)
	require.Equal(t, uint(1), totalMatches)
	require.Len(t, didl.Items, 1)

	item := didl.Items[0]
	expectedPosterURL := fmt.Sprintf("%s/posters/test-poster.jpg", baseURL)
	require.NotNil(t, item.Icon)
	assert.Equal(t, expectedPosterURL, item.Icon.String())
	assert.Contains(t, item.AlbumArtURIs, expectedPosterURL)
}

func TestContentDirectory_BrowseChildren_Pagination(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	ctx := context.Background()

	t.Run("root pagination - first page", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, rootID, 0, 2, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(3), totalMatches)
		require.Len(t, didl.Containers, 2)
		assert.Equal(t, upnpav.ObjectID(allTorrentsContainerID), didl.Containers[0].ID)
		assert.Equal(t, upnpav.ObjectID(recentlyAddedContainerID), didl.Containers[1].ID)
	})

	t.Run("root pagination - second page", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, rootID, 2, 2, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(3), totalMatches)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(recentlyViewedContainerID), didl.Containers[0].ID)
	})

	t.Run("root pagination - offset beyond count", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, rootID, 3, 2, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(3), totalMatches)
		require.Empty(t, didl.Containers)
	})

	t.Run("all torrents container pagination", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, allTorrentsContainerID, 0, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(1), totalMatches)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(testTorrentHash.HexString()), didl.Containers[0].ID)

		didlEmpty, totalMatchesEmpty, err := service.contentDirectory.BrowseChildren(ctx, allTorrentsContainerID, 1, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didlEmpty)
		require.Equal(t, uint(1), totalMatchesEmpty)
		require.Empty(t, didlEmpty.Containers)
	})

	t.Run("recently added container pagination", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, recentlyAddedContainerID, 0, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(1), totalMatches)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(testTorrentHash.HexString()), didl.Containers[0].ID)
	})

	t.Run("torrent files pagination", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, upnpav.ObjectID(testTorrentHash.HexString()), 0, 1, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(1), totalMatches)
		require.Len(t, didl.Items, 1)
		assert.Equal(t, testTorrentFileName, didl.Items[0].Title)
		assert.Equal(t, upnpav.ObjectID(fmt.Sprintf("%s/0", testTorrentHash.HexString())), didl.Items[0].ID)

		didlEmpty, totalMatchesEmpty, err := service.contentDirectory.BrowseChildren(ctx, upnpav.ObjectID(testTorrentHash.HexString()), 1, 1, nil)
		require.NoError(t, err)
		require.NotNil(t, didlEmpty)
		require.Equal(t, uint(1), totalMatchesEmpty)
		require.Empty(t, didlEmpty.Items)
	})
}

func TestContentDirectory_Capabilities(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	ctx := context.Background()

	searchCaps, err := service.contentDirectory.SearchCapabilities(ctx)
	require.NoError(t, err)
	assert.Contains(t, searchCaps, "dc:title")
	assert.Contains(t, searchCaps, "upnp:class")

	sortCaps, err := service.contentDirectory.SortCapabilities(ctx)
	require.NoError(t, err)
	assert.Contains(t, sortCaps, "dc:title")

	updateID, err := service.contentDirectory.SystemUpdateID(ctx)
	require.NoError(t, err)
	assert.NotZero(t, updateID)

	service.contentDirectory.IncrementSystemUpdateID()
	newUpdateID, err := service.contentDirectory.SystemUpdateID(ctx)
	require.NoError(t, err)
	assert.Equal(t, updateID+1, newUpdateID)

	features, err := service.contentDirectory.XGetFeatureList(ctx)
	require.NoError(t, err)
	require.Len(t, features, 1)
	assert.Contains(t, features[0], "samsung.com_BASICVIEW")
}

func TestRecentlyAddedAndRecentlyViewedTorrents(t *testing.T) {
	now := time.Now()
	var allTorrents []*database.Torrent

	for i := 0; i < 15; i++ {
		createdAt := now.Add(time.Duration(i) * time.Minute)
		viewedAt := now.Add(time.Duration(i) * time.Hour)
		torrent := &database.Torrent{
			Torrent: api.Torrent{
				Hash:      metainfo.NewHashFromHex(fmt.Sprintf("%040x", i+1)),
				Name:      fmt.Sprintf("Torrent %d", i),
				CreatedAt: &createdAt,
				Files: []api.TorrentFile{
					{
						Path:     fmt.Sprintf("file%d.mp4", i),
						Name:     fmt.Sprintf("file%d.mp4", i),
						Length:   1000,
						ViewedAt: &viewedAt,
					},
				},
			},
		}
		allTorrents = append(allTorrents, torrent)
	}

	t.Run("recently added limits to 10 sorted descending", func(t *testing.T) {
		recent := getRecentlyAddedTorrents(allTorrents)
		require.Len(t, recent, recentlyItemsCount)
		assert.Equal(t, "Torrent 14", recent[0].Name)
		assert.Equal(t, "Torrent 5", recent[9].Name)
	})

	t.Run("recently viewed limits to 10 sorted descending", func(t *testing.T) {
		recent := getRecentlyViewedTorrents(allTorrents)
		require.Len(t, recent, recentlyItemsCount)
		assert.Equal(t, "Torrent 14", recent[0].Name)
		assert.Equal(t, "Torrent 5", recent[9].Name)
	})

	t.Run("recently viewed with no viewed files", func(t *testing.T) {
		unviewed := []*database.Torrent{
			{
				Torrent: api.Torrent{
					Hash: testTorrentHash,
					Name: "Unviewed",
					Files: []api.TorrentFile{
						{
							Path:   "file.mp4",
							Name:   "file.mp4",
							Length: 1000,
						},
					},
				},
			},
		}
		recent := getRecentlyViewedTorrents(unviewed)
		assert.Empty(t, recent)
	})
}

func TestContentDirectory_NilBaseURL(t *testing.T) {
	db := &mockDB{}
	cd := NewContentDirectory(db, &mockImages{}, nil, "/posters/")

	uri := cd.fileURI("0123456789012345678901234567890123456789", "test.mp4")
	assert.Empty(t, uri)

	assert.Nil(t, cd.posterURI("poster.jpg"))
	assert.Nil(t, cd.posterURI(""))
	assert.Nil(t, cd.iconURI("test.mp4"))
}

func TestContentDirectory_Search_CategoriesAndErrors(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	ctx := context.Background()
	criteria, err := search.Parse(`(dc:title contains "Test")`)
	require.NoError(t, err)

	t.Run("search in recently added container", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.Search(ctx, recentlyAddedContainerID, criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(2), totalMatches)
		assert.Len(t, didl.Containers, 1)
	})

	t.Run("search in recently viewed container", func(t *testing.T) {
		didl, totalMatches, err := service.contentDirectory.Search(ctx, recentlyViewedContainerID, criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(0), totalMatches)
		assert.Empty(t, didl.Containers)
	})

	t.Run("search in non-existent container", func(t *testing.T) {
		_, _, err := service.contentDirectory.Search(ctx, "nonexistent-container", criteria, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})
}

func TestContentDirectory_BrowseChildren_RecentlyViewed(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	ctx := context.Background()
	didl, totalMatches, err := service.contentDirectory.BrowseChildren(ctx, recentlyViewedContainerID, 0, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, uint(0), totalMatches)
	assert.Empty(t, didl.Containers)
}
