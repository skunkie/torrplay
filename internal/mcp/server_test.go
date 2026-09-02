// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m)
}

func setupMockTorrPlay(t *testing.T) *Client {
	t.Helper()

	testHashStr := "08ada5a7a6183aae1e09d831df6748d566095a10"
	testHash := metainfo.NewHashFromHex(testHashStr)

	dummyTorrent := api.Torrent{
		Hash:       testHash,
		Name:       "Sintel",
		TotalSize:  12345678,
		PieceCount: 100,
		Files: []api.TorrentFile{
			{
				Name:   "sintel.mp4",
				Length: 12345678,
				Path:   "sintel.mp4",
			},
			{
				Name:   "sintel-extras.mp4",
				Length: 1024,
				Path:   "sintel-extras.mp4",
			},
		},
	}

	mux := http.NewServeMux()

	// GET /api/v1/torrents
	mux.HandleFunc("/api/v1/torrents", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			res := api.ListTorrents{
				Limit:    50,
				Offset:   0,
				Total:    1,
				Torrents: []api.Torrent{dummyTorrent},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
		case http.MethodPost:
			var addReq api.TorrentAdd
			if err := json.NewDecoder(r.Body).Decode(&addReq); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(dummyTorrent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// /api/v1/torrents/{hash}
	mux.HandleFunc("/api/v1/torrents/", func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Path[len("/api/v1/torrents/"):]
		if hash != testHashStr {
			http.Error(w, `{"message":"torrent not found"}`, http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dummyTorrent)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			var updateReq api.TorrentUpdate
			_ = json.NewDecoder(r.Body).Decode(&updateReq)
			updated := dummyTorrent
			if updateReq.Title != nil {
				updated.Title = updateReq.Title
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(updated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// GET /api/stats/memory
	mux.HandleFunc("/api/stats/memory", func(w http.ResponseWriter, _ *http.Request) {
		res := api.MemoryStats{
			UsedMemory: 1024 * 1024 * 50,
			MaxMemory:  1024 * 1024 * 200,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	// GET /api/stats/torrents/{hash}
	mux.HandleFunc("/api/stats/torrents/", func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Path[len("/api/stats/torrents/"):]
		if hash != testHashStr {
			http.Error(w, `{"message":"torrent not found"}`, http.StatusNotFound)
			return
		}
		res := api.TorrentStats{
			BytesRead: 12345678,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	// GET /api/system/info
	mux.HandleFunc("/api/system/info", func(w http.ResponseWriter, _ *http.Request) {
		res := api.SystemInfo{
			Version: "1.0.0",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return NewClient(ts.URL, "test-token", ts.Client())
}

func TestTools(t *testing.T) {
	client := setupMockTorrPlay(t)
	s := NewServer(client)

	ctx := context.Background()
	testHash := "08ada5a7a6183aae1e09d831df6748d566095a10"

	t.Run("list_torrents", func(t *testing.T) {
		tool := s.GetTool("list_torrents")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "list_torrents",
				Arguments: map[string]any{
					"limit": 10,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Sintel")
	})

	t.Run("get_torrent", func(t *testing.T) {
		tool := s.GetTool("get_torrent")
		require.NotNil(t, tool)

		// Missing hash
		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "get_torrent"},
		})
		require.Error(t, err)
		assert.Nil(t, res)

		// Valid hash
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_torrent",
				Arguments: map[string]any{
					"hash": testHash,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "sintel.mp4")

		// Not found hash (valid hex, non-zero, but nonexistent on server)
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_torrent",
				Arguments: map[string]any{
					"hash": "1111111111111111111111111111111111111111",
				},
			},
		})
		require.NoError(t, err)
		assert.True(t, res.IsError)

		// Malformed hash
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_torrent",
				Arguments: map[string]any{
					"hash": "too-short",
				},
			},
		})
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "invalid info hash")
	})

	t.Run("add_torrent", func(t *testing.T) {
		tool := s.GetTool("add_torrent")
		require.NotNil(t, tool)

		// Missing uri
		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "add_torrent"},
		})
		require.Error(t, err)
		assert.Nil(t, res)

		// Malformed uri (neither magnet nor 40-char hex)
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "add_torrent",
				Arguments: map[string]any{
					"uri": "invalid-torrent-uri",
				},
			},
		})
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "valid magnet link")

		// Valid magnet
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "add_torrent",
				Arguments: map[string]any{
					"uri":      "magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10",
					"title":    "Sintel Movie",
					"category": "Movies",
					"storage":  "memory",
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Sintel")

		// Valid 40-char hash
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "add_torrent",
				Arguments: map[string]any{
					"uri": testHash,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
	})

	t.Run("delete_torrent", func(t *testing.T) {
		tool := s.GetTool("delete_torrent")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "delete_torrent",
				Arguments: map[string]any{
					"hash": testHash,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "successfully deleted")
	})

	t.Run("update_torrent", func(t *testing.T) {
		tool := s.GetTool("update_torrent")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "update_torrent",
				Arguments: map[string]any{
					"hash":  testHash,
					"title": "New Sintel Title",
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "New Sintel Title")
	})

	t.Run("get_stream_url", func(t *testing.T) {
		tool := s.GetTool("get_stream_url")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_stream_url",
				Arguments: map[string]any{
					"hash":       testHash,
					"file_index": 1,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "/api/v1/stream/")
		assert.Contains(t, textContent.Text, "/play/")
		assert.Contains(t, textContent.Text, "/api/v1/playlist")
	})

	t.Run("get_memory_stats", func(t *testing.T) {
		tool := s.GetTool("get_memory_stats")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "get_memory_stats"},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
	})

	t.Run("get_torrent_stats", func(t *testing.T) {
		tool := s.GetTool("get_torrent_stats")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_torrent_stats",
				Arguments: map[string]any{
					"hash": testHash,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
	})

	t.Run("get_system_info", func(t *testing.T) {
		tool := s.GetTool("get_system_info")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "get_system_info"},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "1.0.0")
	})
}

func TestResources(t *testing.T) {
	client := setupMockTorrPlay(t)
	s := NewServer(client)

	ctx := context.Background()
	testHash := "08ada5a7a6183aae1e09d831df6748d566095a10"

	t.Run("torrplay://torrents", func(t *testing.T) {
		res := s.ListResources()["torrplay://torrents"]
		require.NotNil(t, res)

		contents, err := res.Handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{URI: "torrplay://torrents"},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		textContent, ok := mcp.AsTextResourceContents(contents[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Sintel")
	})

	t.Run("torrplay://system/memory", func(t *testing.T) {
		res := s.ListResources()["torrplay://system/memory"]
		require.NotNil(t, res)

		contents, err := res.Handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{URI: "torrplay://system/memory"},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
	})

	t.Run("torrplay://system/info", func(t *testing.T) {
		res := s.ListResources()["torrplay://system/info"]
		require.NotNil(t, res)

		contents, err := res.Handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{URI: "torrplay://system/info"},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		textContent, ok := mcp.AsTextResourceContents(contents[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "1.0.0")
	})

	t.Run("torrplay://torrents/{hash}", func(t *testing.T) {
		handler := torrentDetailHandler(client)

		// Via Arguments map (normal path)
		contents, err := handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{
				URI: "torrplay://torrents/" + testHash,
				Arguments: map[string]any{
					"hash": testHash,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		textContent, ok := mcp.AsTextResourceContents(contents[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Sintel")

		// Via URI-fallback path (Arguments map absent / no "hash" key)
		contents, err = handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{
				URI: "torrplay://torrents/" + testHash,
			},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		textContent, ok = mcp.AsTextResourceContents(contents[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Sintel")

		// Invalid hash returns error
		_, err = handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{
				URI: "torrplay://torrents/not-a-hash",
				Arguments: map[string]any{
					"hash": "not-a-hash",
				},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid info hash")
	})
}

func TestPrompts(t *testing.T) {
	client := setupMockTorrPlay(t)
	s := NewServer(client)

	ctx := context.Background()
	testHash := "08ada5a7a6183aae1e09d831df6748d566095a10"

	t.Run("find_playable_file", func(t *testing.T) {
		prompt := s.ListPrompts()["find_playable_file"]
		require.NotNil(t, prompt)

		res, err := prompt.Handler(ctx, mcp.GetPromptRequest{
			Params: mcp.GetPromptParams{
				Arguments: map[string]string{
					"hash": testHash,
				},
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, res.Messages)
		textContent, ok := mcp.AsTextContent(res.Messages[0].Content)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "sintel.mp4")
	})

	t.Run("stream_diagnostics", func(t *testing.T) {
		prompt := s.ListPrompts()["stream_diagnostics"]
		require.NotNil(t, prompt)

		res, err := prompt.Handler(ctx, mcp.GetPromptRequest{
			Params: mcp.GetPromptParams{
				Arguments: map[string]string{
					"hash": testHash,
				},
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, res.Messages)
	})
}

func TestClient_Errors(t *testing.T) {
	client := NewClient("http://127.0.0.1:59999", "", nil)
	ctx := context.Background()

	_, err := client.ListTorrents(ctx, nil, nil, nil)
	assert.Error(t, err)

	_, err = client.GetTorrent(ctx, "abc")
	assert.Error(t, err)

	err = client.DeleteTorrent(ctx, "abc")
	assert.Error(t, err)
}

func TestClientRequestParameters(t *testing.T) {
	requests := make(chan *http.Request, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ListTorrents{})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", ts.Client())
	category := "Movies"
	_, err := client.ListTorrents(context.Background(), nil, nil, &category)
	require.NoError(t, err)
	assert.Equal(t, "Movies", (<-requests).URL.Query().Get("categories"))

	err = client.DeleteTorrent(context.Background(), "08ada5a7a6183aae1e09d831df6748d566095a10")
	require.NoError(t, err)
	assert.Empty(t, (<-requests).URL.RawQuery)
}

func TestStreamingURLs(t *testing.T) {
	client := NewClient("http://127.0.0.1:8090", "secret token", nil)
	hash := "08ada5a7a6183aae1e09d831df6748d566095a10"

	streamURL, err := url.Parse(client.StreamURL(hash, 1))
	require.NoError(t, err)
	assert.Equal(t, "1", streamURL.Query().Get("index"))
	assert.Equal(t, "secret token", streamURL.Query().Get("token"))

	playURL, err := url.Parse(client.PlayURL(hash, 1))
	require.NoError(t, err)
	assert.Equal(t, "/play/"+hash+"/2", playURL.Path)
	assert.Equal(t, "secret token", playURL.Query().Get("token"))

	playlistURL, err := url.Parse(client.PlaylistURL("Sintel"))
	require.NoError(t, err)
	assert.Equal(t, "Sintel.m3u", playlistURL.Query().Get("name"))
	assert.Equal(t, "secret token", playlistURL.Query().Get("token"))
}
