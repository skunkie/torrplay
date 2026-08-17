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

		_, err := service.contentDirectory.BrowseMetadata(ctx, categoriesContainerID, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
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

type mockDBWithCategories struct {
	database.Unimplemented
	torrents []*database.Torrent
}

func (m *mockDBWithCategories) GetTorrents() ([]*database.Torrent, error) {
	return m.torrents, nil
}

func (m *mockDBWithCategories) GetTorrent(ih metainfo.Hash) (*database.Torrent, error) {
	for _, t := range m.torrents {
		if t.Hash == ih {
			return t, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func TestContentDirectory_Categories(t *testing.T) {
	moviesCat := "Movies"
	animeCat := "Anime"
	slashCat := "TV/Shows"
	emptyCat := "   "
	docsCat := "Documents"

	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	hash2 := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")
	hash3 := metainfo.NewHashFromHex("3333333333333333333333333333333333333333")
	hash4 := metainfo.NewHashFromHex("4444444444444444444444444444444444444444")
	hash5 := metainfo.NewHashFromHex("5555555555555555555555555555555555555555")
	hash6 := metainfo.NewHashFromHex("6666666666666666666666666666666666666666")
	hash7 := metainfo.NewHashFromHex("7777777777777777777777777777777777777777")

	testTorrents := []*database.Torrent{
		{
			Torrent: api.Torrent{
				Hash:     hash1,
				Name:     "Movie B",
				Category: &moviesCat,
				Files: []api.TorrentFile{
					{Path: "movieB.mp4", Name: "movieB.mp4", Length: 1000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash2,
				Name:     "Movie A",
				Category: &moviesCat,
				Files: []api.TorrentFile{
					{Path: "movieA.mp4", Name: "movieA.mp4", Length: 2000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash3,
				Name:     "Anime 1",
				Category: &animeCat,
				Files: []api.TorrentFile{
					{Path: "anime1.mkv", Name: "anime1.mkv", Length: 3000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash4,
				Name:     "TV Episode 1",
				Category: &slashCat,
				Files: []api.TorrentFile{
					{Path: "tv1.mp4", Name: "tv1.mp4", Length: 4000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash5,
				Name:     "Uncategorized Nil",
				Category: nil,
				Files: []api.TorrentFile{
					{Path: "uncat.mp4", Name: "uncat.mp4", Length: 5000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash6,
				Name:     "Uncategorized Empty",
				Category: &emptyCat,
				Files: []api.TorrentFile{
					{Path: "empty.mp4", Name: "empty.mp4", Length: 6000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash7,
				Name:     "Non-Media Doc",
				Category: &docsCat,
				Files: []api.TorrentFile{
					{Path: "readme.txt", Name: "readme.txt", Length: 100},
				},
			},
		},
	}

	db := &mockDBWithCategories{torrents: testTorrents}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	t.Run("categories container metadata", func(t *testing.T) {
		didl, err := cd.BrowseMetadata(ctx, categoriesContainerID, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(categoriesContainerID), didl.Containers[0].ID)
		assert.Equal(t, upnpav.ObjectID(rootID), didl.Containers[0].Parent)
		assert.Equal(t, "Categories", didl.Containers[0].Title)
		assert.Equal(t, 4, didl.Containers[0].ChildCount) // Anime, Movies, TV/Shows, Uncategorized
	})

	t.Run("browse categories container list", func(t *testing.T) {
		didl, totalMatches, err := cd.BrowseChildren(ctx, categoriesContainerID, 0, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(4), totalMatches)
		require.Len(t, didl.Containers, 4)

		assert.Equal(t, upnpav.ObjectID("category:Anime"), didl.Containers[0].ID)
		assert.Equal(t, "Anime", didl.Containers[0].Title)
		assert.Equal(t, upnpav.ObjectID(categoriesContainerID), didl.Containers[0].Parent)
		assert.Equal(t, 1, didl.Containers[0].ChildCount)

		assert.Equal(t, upnpav.ObjectID("category:Movies"), didl.Containers[1].ID)
		assert.Equal(t, "Movies", didl.Containers[1].Title)
		assert.Equal(t, 2, didl.Containers[1].ChildCount)

		assert.Equal(t, upnpav.ObjectID("category:TV/Shows"), didl.Containers[2].ID)
		assert.Equal(t, "TV/Shows", didl.Containers[2].Title)
		assert.Equal(t, 1, didl.Containers[2].ChildCount)

		assert.Equal(t, upnpav.ObjectID("category:Uncategorized"), didl.Containers[3].ID)
		assert.Equal(t, "Uncategorized", didl.Containers[3].Title)
		assert.Equal(t, 2, didl.Containers[3].ChildCount)
	})

	t.Run("browse specific category metadata", func(t *testing.T) {
		didl, err := cd.BrowseMetadata(ctx, "category:Movies", nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID("category:Movies"), didl.Containers[0].ID)
		assert.Equal(t, "Movies", didl.Containers[0].Title)
		assert.Equal(t, upnpav.ObjectID(categoriesContainerID), didl.Containers[0].Parent)
		assert.Equal(t, 2, didl.Containers[0].ChildCount)
	})

	t.Run("browse non-existent category metadata", func(t *testing.T) {
		_, err := cd.BrowseMetadata(ctx, "category:NonExistent", nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("browse non-existent category children", func(t *testing.T) {
		_, _, err := cd.BrowseChildren(ctx, "category:NonExistent", 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("browse category children sorted alphabetically", func(t *testing.T) {
		didl, totalMatches, err := cd.BrowseChildren(ctx, "category:Movies", 0, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(2), totalMatches)
		require.Len(t, didl.Containers, 2)

		assert.Equal(t, "Movie A", didl.Containers[0].Title)
		assert.Equal(t, upnpav.ObjectID(hash2.HexString()), didl.Containers[0].ID)
		assert.Equal(t, upnpav.ObjectID("category:Movies"), didl.Containers[0].Parent)

		assert.Equal(t, "Movie B", didl.Containers[1].Title)
		assert.Equal(t, upnpav.ObjectID(hash1.HexString()), didl.Containers[1].ID)
		assert.Equal(t, upnpav.ObjectID("category:Movies"), didl.Containers[1].Parent)
	})

	t.Run("browse category pagination", func(t *testing.T) {
		didl1, totalMatches1, err := cd.BrowseChildren(ctx, categoriesContainerID, 0, 2, nil)
		require.NoError(t, err)
		require.Equal(t, uint(4), totalMatches1)
		require.Len(t, didl1.Containers, 2)
		assert.Equal(t, "Anime", didl1.Containers[0].Title)
		assert.Equal(t, "Movies", didl1.Containers[1].Title)

		didl2, totalMatches2, err := cd.BrowseChildren(ctx, categoriesContainerID, 2, 2, nil)
		require.NoError(t, err)
		require.Equal(t, uint(4), totalMatches2)
		require.Len(t, didl2.Containers, 2)
		assert.Equal(t, "TV/Shows", didl2.Containers[0].Title)
		assert.Equal(t, "Uncategorized", didl2.Containers[1].Title)

		didl3, totalMatches3, err := cd.BrowseChildren(ctx, categoriesContainerID, 4, 2, nil)
		require.NoError(t, err)
		require.Equal(t, uint(4), totalMatches3)
		require.Empty(t, didl3.Containers)
	})

	t.Run("browse item metadata when category has slash", func(t *testing.T) {
		itemID := upnpav.ObjectID(fmt.Sprintf("%s/0", hash4.HexString()))
		didl, err := cd.BrowseMetadata(ctx, itemID, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Items, 1)
		assert.Equal(t, "tv1.mp4", didl.Items[0].Title)
	})

	t.Run("search in categories container", func(t *testing.T) {
		criteria, err := search.Parse(`(dc:title contains "Movie")`)
		require.NoError(t, err)

		didl, totalMatches, err := cd.Search(ctx, categoriesContainerID, criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(4), totalMatches) // 2 containers (Movie A, Movie B) + 2 items (movieA.mp4, movieB.mp4)
		assert.Len(t, didl.Containers, 2)
		assert.Len(t, didl.Items, 2)
	})

	t.Run("search scoped to category", func(t *testing.T) {
		criteria, err := search.Parse(`(dc:title contains "Movie")`)
		require.NoError(t, err)

		didl, totalMatches, err := cd.Search(ctx, "category:Movies", criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(4), totalMatches)
		assert.Len(t, didl.Containers, 2)
		assert.Equal(t, upnpav.ObjectID("category:Movies"), didl.Containers[0].Parent)
	})

	t.Run("search in non-existent category", func(t *testing.T) {
		criteria, err := search.Parse(`(dc:title contains "Test")`)
		require.NoError(t, err)

		_, _, err = cd.Search(ctx, "category:NonExistent", criteria, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})
}

func TestContentDirectory_NoTorrents(t *testing.T) {
	db := &mockDBWithCategories{torrents: []*database.Torrent{}}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	t.Run("root metadata with no torrents", func(t *testing.T) {
		didl, err := cd.BrowseMetadata(ctx, rootID, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(rootID), didl.Containers[0].ID)
		assert.Equal(t, 3, didl.Containers[0].ChildCount)
	})

	t.Run("container metadata with no torrents", func(t *testing.T) {
		for _, containerID := range []string{allTorrentsContainerID, recentlyAddedContainerID, recentlyViewedContainerID} {
			didl, err := cd.BrowseMetadata(ctx, upnpav.ObjectID(containerID), nil)
			require.NoError(t, err)
			require.NotNil(t, didl)
			require.Len(t, didl.Containers, 1)
			assert.Equal(t, 0, didl.Containers[0].ChildCount)
		}

		_, err := cd.BrowseMetadata(ctx, categoriesContainerID, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("browse root with no torrents", func(t *testing.T) {
		didl, totalMatches, err := cd.BrowseChildren(ctx, rootID, 0, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(3), totalMatches)
		require.Len(t, didl.Containers, 3)
		for _, c := range didl.Containers {
			assert.Equal(t, 0, c.ChildCount)
		}
	})

	t.Run("browse all containers children with no torrents", func(t *testing.T) {
		for _, containerID := range []string{allTorrentsContainerID, recentlyAddedContainerID, recentlyViewedContainerID} {
			didl, totalMatches, err := cd.BrowseChildren(ctx, upnpav.ObjectID(containerID), 0, 10, nil)
			require.NoError(t, err)
			require.NotNil(t, didl)
			assert.Equal(t, uint(0), totalMatches)
			assert.Empty(t, didl.Containers)
			assert.Empty(t, didl.Items)
		}

		_, _, err := cd.BrowseChildren(ctx, categoriesContainerID, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("browse category metadata with no torrents", func(t *testing.T) {
		_, err := cd.BrowseMetadata(ctx, "category:Movies", nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("search with no torrents", func(t *testing.T) {
		criteria, err := search.Parse(`(dc:title contains "Movie")`)
		require.NoError(t, err)

		didl, totalMatches, err := cd.Search(ctx, rootID, criteria, 0, 10, nil)
		require.NoError(t, err)
		assert.Equal(t, uint(0), totalMatches)
		assert.Empty(t, didl.Containers)
		assert.Empty(t, didl.Items)

		_, _, err = cd.Search(ctx, categoriesContainerID, criteria, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)

		_, _, err = cd.Search(ctx, "category:Movies", criteria, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})
}

func TestContentDirectory_TorrentsWithoutCategories(t *testing.T) {
	emptyCat := "   "
	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	hash2 := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")

	testTorrents := []*database.Torrent{
		{
			Torrent: api.Torrent{
				Hash:     hash1,
				Name:     "Torrent Nil Cat",
				Category: nil,
				Files: []api.TorrentFile{
					{Path: "t1.mp4", Name: "t1.mp4", Length: 1000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash2,
				Name:     "Torrent Empty Cat",
				Category: &emptyCat,
				Files: []api.TorrentFile{
					{Path: "t2.mp4", Name: "t2.mp4", Length: 2000},
				},
			},
		},
	}

	db := &mockDBWithCategories{torrents: testTorrents}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	t.Run("root metadata when no categories set", func(t *testing.T) {
		didl, err := cd.BrowseMetadata(ctx, rootID, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Len(t, didl.Containers, 1)
		assert.Equal(t, upnpav.ObjectID(rootID), didl.Containers[0].ID)
		assert.Equal(t, 3, didl.Containers[0].ChildCount)
	})

	t.Run("browse root when no categories set", func(t *testing.T) {
		didl, totalMatches, err := cd.BrowseChildren(ctx, rootID, 0, 10, nil)
		require.NoError(t, err)
		require.NotNil(t, didl)
		require.Equal(t, uint(3), totalMatches)
		require.Len(t, didl.Containers, 3)
		assert.Equal(t, upnpav.ObjectID(allTorrentsContainerID), didl.Containers[0].ID)
		assert.Equal(t, upnpav.ObjectID(recentlyAddedContainerID), didl.Containers[1].ID)
		assert.Equal(t, upnpav.ObjectID(recentlyViewedContainerID), didl.Containers[2].ID)
	})

	t.Run("categories container metadata returns ErrNoSuchObject when no categories set", func(t *testing.T) {
		_, err := cd.BrowseMetadata(ctx, categoriesContainerID, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("categories container browse returns ErrNoSuchObject when no categories set", func(t *testing.T) {
		_, _, err := cd.BrowseChildren(ctx, categoriesContainerID, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})

	t.Run("categories container search returns ErrNoSuchObject when no categories set", func(t *testing.T) {
		criteria, err := search.Parse(`(dc:title contains "Torrent")`)
		require.NoError(t, err)

		_, _, err = cd.Search(ctx, categoriesContainerID, criteria, 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})
}

func TestContentDirectory_OnlyExplicitCategories_NoUncategorized(t *testing.T) {
	moviesCat := "Movies"
	seriesCat := "Series"
	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	hash2 := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")

	testTorrents := []*database.Torrent{
		{
			Torrent: api.Torrent{
				Hash:     hash1,
				Name:     "Movie 1",
				Category: &moviesCat,
				Files: []api.TorrentFile{
					{Path: "m1.mp4", Name: "m1.mp4", Length: 1000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash2,
				Name:     "Series 1",
				Category: &seriesCat,
				Files: []api.TorrentFile{
					{Path: "s1.mp4", Name: "s1.mp4", Length: 2000},
				},
			},
		},
	}

	db := &mockDBWithCategories{torrents: testTorrents}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	t.Run("categories list only has explicit categories and no Uncategorized", func(t *testing.T) {
		didl, totalMatches, err := cd.BrowseChildren(ctx, categoriesContainerID, 0, 10, nil)
		require.NoError(t, err)
		require.Equal(t, uint(2), totalMatches)
		require.Len(t, didl.Containers, 2)
		assert.Equal(t, "Movies", didl.Containers[0].Title)
		assert.Equal(t, "Series", didl.Containers[1].Title)
	})

	t.Run("browse Uncategorized returns ErrNoSuchObject when all torrents are categorized", func(t *testing.T) {
		_, err := cd.BrowseMetadata(ctx, "category:Uncategorized", nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)

		_, _, err = cd.BrowseChildren(ctx, "category:Uncategorized", 0, 10, nil)
		assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
	})
}

func TestContentDirectory_DynamicCategoryTransition(t *testing.T) {
	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	torrent := &database.Torrent{
		Torrent: api.Torrent{
			Hash:     hash1,
			Name:     "Dynamic Torrent",
			Category: nil,
			Files: []api.TorrentFile{
				{Path: "dyn.mp4", Name: "dyn.mp4", Length: 1000},
			},
		},
	}

	db := &mockDBWithCategories{torrents: []*database.Torrent{torrent}}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	// State 1: Torrent has no category
	didl, err := cd.BrowseMetadata(ctx, rootID, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, didl.Containers[0].ChildCount)

	rootChildren, totalMatches, err := cd.BrowseChildren(ctx, rootID, 0, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, uint(3), totalMatches)
	assert.Len(t, rootChildren.Containers, 3)

	_, err = cd.BrowseMetadata(ctx, categoriesContainerID, nil)
	assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)

	// State 2: Assign a category
	cat := "Documentaries"
	torrent.Category = &cat

	didl, err = cd.BrowseMetadata(ctx, rootID, nil)
	require.NoError(t, err)
	assert.Equal(t, 4, didl.Containers[0].ChildCount)

	rootChildren, totalMatches, err = cd.BrowseChildren(ctx, rootID, 0, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, uint(4), totalMatches)
	require.Len(t, rootChildren.Containers, 4)
	assert.Equal(t, upnpav.ObjectID(categoriesContainerID), rootChildren.Containers[3].ID)

	catMetadata, err := cd.BrowseMetadata(ctx, categoriesContainerID, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, catMetadata.Containers[0].ChildCount)

	// State 3: Remove category
	torrent.Category = nil

	didl, err = cd.BrowseMetadata(ctx, rootID, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, didl.Containers[0].ChildCount)

	rootChildren, totalMatches, err = cd.BrowseChildren(ctx, rootID, 0, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, uint(3), totalMatches)
	assert.Len(t, rootChildren.Containers, 3)

	_, err = cd.BrowseMetadata(ctx, categoriesContainerID, nil)
	assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
}

func TestContentDirectory_Categories_NonMediaTorrentsIgnored(t *testing.T) {
	docsCat := "Documents"
	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	torrent := &database.Torrent{
		Torrent: api.Torrent{
			Hash:     hash1,
			Name:     "Non-Media Torrent",
			Category: &docsCat,
			Files: []api.TorrentFile{
				{Path: "manual.pdf", Name: "manual.pdf", Length: 1000},
			},
		},
	}

	db := &mockDBWithCategories{torrents: []*database.Torrent{torrent}}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	// Since torrent has no media files, it is ignored by DLNA, so no categories exist
	didl, err := cd.BrowseMetadata(ctx, rootID, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, didl.Containers[0].ChildCount)

	_, err = cd.BrowseMetadata(ctx, categoriesContainerID, nil)
	assert.ErrorIs(t, err, contentdirectory.ErrNoSuchObject)
}

func TestContentDirectory_Categories_WhitespaceTrimming(t *testing.T) {
	catWithSpaces := "   Movies   "
	normalCat := "Movies"
	hash1 := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
	hash2 := metainfo.NewHashFromHex("2222222222222222222222222222222222222222")

	testTorrents := []*database.Torrent{
		{
			Torrent: api.Torrent{
				Hash:     hash1,
				Name:     "Movie Spaced",
				Category: &catWithSpaces,
				Files: []api.TorrentFile{
					{Path: "m1.mp4", Name: "m1.mp4", Length: 1000},
				},
			},
		},
		{
			Torrent: api.Torrent{
				Hash:     hash2,
				Name:     "Movie Normal",
				Category: &normalCat,
				Files: []api.TorrentFile{
					{Path: "m2.mp4", Name: "m2.mp4", Length: 2000},
				},
			},
		},
	}

	db := &mockDBWithCategories{torrents: testTorrents}
	baseURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	cd := NewContentDirectory(db, &mockImages{}, baseURL, "/posters/")
	ctx := context.Background()

	// Both should be grouped into a single "Movies" category
	didl, totalMatches, err := cd.BrowseChildren(ctx, categoriesContainerID, 0, 10, nil)
	require.NoError(t, err)
	require.Equal(t, uint(1), totalMatches)
	require.Len(t, didl.Containers, 1)
	assert.Equal(t, "Movies", didl.Containers[0].Title)
	assert.Equal(t, 2, didl.Containers[0].ChildCount)

	// Browsing "Movies" category returns both torrents
	moviesChildren, count, err := cd.BrowseChildren(ctx, "category:Movies", 0, 10, nil)
	require.NoError(t, err)
	assert.Equal(t, uint(2), count)
	assert.Len(t, moviesChildren.Containers, 2)
}
