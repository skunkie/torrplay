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
	"strings"
	"testing"
	"time"

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
	mux.HandleFunc("/api/v1/tokens", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ScopedToken{Token: "playback-token", Scope: string(api.Playback), ExpiresAt: time.Now().Add(time.Hour)})
	})

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
		if preloadHash, ok := strings.CutSuffix(hash, "/preload"); ok {
			if preloadHash != testHashStr {
				http.Error(w, `{"message":"torrent not found"}`, http.StatusNotFound)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.PreloadResponse{
				Status:    api.Preloading,
				FileIndex: 0,
				Progress:  0.25,
			})
			return
		}
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
			Deployment: api.SystemInfoDeploymentNative,
			Version:    "1.0.0",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	// GET /api/system/logs
	mux.HandleFunc("/api/system/logs", func(w http.ResponseWriter, _ *http.Request) {
		res := []api.LogEntry{{Level: "info", Message: "torrplay started"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	// GET /api/system/metrics
	mux.HandleFunc("/api/system/metrics", func(w http.ResponseWriter, _ *http.Request) {
		res := api.SystemMetrics{ActiveTorrents: 1}
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

		// Uppercase magnet scheme
		res, err = tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "add_torrent",
				Arguments: map[string]any{
					"uri": "MAGNET:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10",
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)

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
		assert.NotContains(t, textContent.Text, "/play/")
		assert.Contains(t, textContent.Text, "/api/v1/playlist")
	})

	t.Run("preload_torrent", func(t *testing.T) {
		tool := s.GetTool("preload_torrent")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "preload_torrent",
				Arguments: map[string]any{
					"hash":       testHash,
					"file_index": 0,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "preloading")
	})

	t.Run("preload_torrent_negative_file_index", func(t *testing.T) {
		tool := s.GetTool("preload_torrent")
		require.NotNil(t, tool)

		_, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "preload_torrent",
				Arguments: map[string]any{
					"hash":       testHash,
					"file_index": -1,
				},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "file_index must not be negative")
	})

	t.Run("preload_torrent_uppercase_magnet", func(t *testing.T) {
		tool := s.GetTool("preload_torrent")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "preload_torrent",
				Arguments: map[string]any{
					"hash":   testHash,
					"magnet": "MAGNET:?xt=urn:btih:" + testHash,
				},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
	})

	t.Run("get_preload_status", func(t *testing.T) {
		tool := s.GetTool("get_preload_status")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "get_preload_status",
				Arguments: map[string]any{"hash": testHash},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "preloading")
	})

	t.Run("cancel_preload", func(t *testing.T) {
		tool := s.GetTool("cancel_preload")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "cancel_preload",
				Arguments: map[string]any{"hash": testHash},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
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

	t.Run("get_system_logs", func(t *testing.T) {
		tool := s.GetTool("get_system_logs")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "get_system_logs"},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		textContent, ok := mcp.AsTextContent(res.Content[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "torrplay started")
	})

	t.Run("get_system_metrics", func(t *testing.T) {
		tool := s.GetTool("get_system_metrics")
		require.NotNil(t, tool)

		res, err := tool.Handler(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "get_system_metrics"},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
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

	t.Run("torrplay://system/logs", func(t *testing.T) {
		res := s.ListResources()["torrplay://system/logs"]
		require.NotNil(t, res)

		contents, err := res.Handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{URI: "torrplay://system/logs"},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		textContent, ok := mcp.AsTextResourceContents(contents[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "torrplay started")
	})

	t.Run("torrplay://system/metrics", func(t *testing.T) {
		res := s.ListResources()["torrplay://system/metrics"]
		require.NotNil(t, res)

		contents, err := res.Handler(ctx, mcp.ReadResourceRequest{
			Params: mcp.ReadResourceParams{URI: "torrplay://system/metrics"},
		})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		textContent, ok := mcp.AsTextResourceContents(contents[0])
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "active_torrents")
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

	_, err = client.GetTorrent(ctx, "abc", "")
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

	hash := "08ada5a7a6183aae1e09d831df6748d566095a10"
	magnet := "magnet:?xt=urn:btih:" + hash
	_, err = client.GetTorrent(context.Background(), hash, magnet)
	require.NoError(t, err)
	assert.Equal(t, magnet, (<-requests).URL.Query().Get("magnet"))
}

func TestClientPreloadTorrent(t *testing.T) {
	hash := "08ada5a7a6183aae1e09d831df6748d566095a10"
	magnet := "magnet:?xt=urn:btih:" + hash

	var (
		gotMethod string
		gotPath   string
		gotBody   api.PreloadRequest
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_ = json.NewEncoder(w).Encode(api.PreloadResponse{Status: api.Preloading, FileIndex: 2})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "", ts.Client())
	fileIndex := 2
	res, err := client.PreloadTorrent(context.Background(), hash, api.PreloadRequest{
		FileIndex: &fileIndex,
		Magnet:    &magnet,
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/api/v1/torrents/"+hash+"/preload", gotPath)
	require.NotNil(t, gotBody.FileIndex)
	assert.Equal(t, 2, *gotBody.FileIndex)
	require.NotNil(t, gotBody.Magnet)
	assert.Equal(t, magnet, *gotBody.Magnet)
	assert.Equal(t, api.Preloading, res.Status)
}

func TestStreamingURLs(t *testing.T) {
	client := NewClient("http://127.0.0.1:8090", "secret token", nil)
	hash := "08ada5a7a6183aae1e09d831df6748d566095a10"

	streamURL, err := url.Parse(client.StreamURL(hash, 1, "", ""))
	require.NoError(t, err)
	assert.Equal(t, "1", streamURL.Query().Get("index"))
	assert.Empty(t, streamURL.Query().Get("token"))

	playlistURL, err := url.Parse(client.PlaylistURL("Sintel", ""))
	require.NoError(t, err)
	assert.Equal(t, "Sintel.m3u", playlistURL.Query().Get("name"))
	assert.Empty(t, playlistURL.Query().Get("token"))

	streamURL, err = url.Parse(client.StreamURL(hash, 1, "", "playback token"))
	require.NoError(t, err)
	assert.Equal(t, "playback token", streamURL.Query().Get("token"))

	magnet := "magnet:?xt=urn:btih:" + hash
	streamURL, err = url.Parse(client.StreamURL(hash, 1, magnet, "playback token"))
	require.NoError(t, err)
	assert.Equal(t, magnet, streamURL.Query().Get("magnet"))
	assert.Equal(t, "playback token", streamURL.Query().Get("token"))
}

func TestValidateLoopbackAddress(t *testing.T) {
	for _, tc := range []struct {
		addr    string
		wantErr bool
	}{
		{addr: "127.0.0.1:8091"},
		{addr: "localhost:8091"},
		{addr: "LOCALHOST:8091"},
		{addr: "[::1]:8091"},
		{addr: "0.0.0.0:8091", wantErr: true},
		{addr: ":8091", wantErr: true},
		{addr: "192.168.1.10:8091", wantErr: true},
		{addr: "example.com:8091", wantErr: true},
		{addr: "127.0.0.1", wantErr: true},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			err := validateLoopbackAddress(tc.addr)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestServeSSEStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeSSE(ctx, NewServer(setupMockTorrPlay(t)), "127.0.0.1:0")
	}()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ServeSSE did not return after context cancellation")
	}
}

func TestOptionalIndex(t *testing.T) {
	newReq := func(args map[string]any) mcp.CallToolRequest {
		return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
	}

	value, err := optionalIndex(newReq(map[string]any{}), "file_index")
	require.NoError(t, err)
	assert.Equal(t, unsetInt, value)

	value, err = optionalIndex(newReq(map[string]any{"file_index": 0}), "file_index")
	require.NoError(t, err)
	assert.Equal(t, 0, value)

	_, err = optionalIndex(newReq(map[string]any{"file_index": -1}), "file_index")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file_index must not be negative")
}

func TestIsMagnet(t *testing.T) {
	assert.True(t, isMagnet("magnet:?xt=urn:btih:abc"))
	assert.True(t, isMagnet("MAGNET:?xt=urn:btih:abc"))
	assert.False(t, isMagnet("http://example.com/x.torrent"))
}

func TestPromptHashValidation(t *testing.T) {
	_, err := promptHash(mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Arguments: map[string]string{}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash argument is required")

	_, err = promptHash(mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Arguments: map[string]string{"hash": "not-a-hash"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid info hash")

	hash, err := promptHash(mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Arguments: map[string]string{"hash": " 08ADA5A7A6183AAE1E09D831DF6748D566095A10 "},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "08ada5a7a6183aae1e09d831df6748d566095a10", hash)
}
