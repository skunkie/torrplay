// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/torrplay/torrplay/internal/api"
)

// Client interacts with a running TorrPlay HTTP API instance.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// NewClient creates a new Client for communicating with TorrPlay.
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	trimmedURL := strings.TrimRight(baseURL, "/")
	if trimmedURL == "" {
		trimmedURL = "http://127.0.0.1:8090"
	}

	return &Client{
		baseURL:    trimmedURL,
		httpClient: httpClient,
		token:      token,
	}
}

// BaseURL returns the configured base URL of the TorrPlay instance.
func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) doRequest(ctx context.Context, method, endpoint string, body any, out any) error {
	relPath := strings.TrimPrefix(endpoint, "/")
	targetURL := fmt.Sprintf("%s/%s", c.baseURL, relPath)

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("User-Agent", "TorrPlay-MCP/1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBytes, _ := io.ReadAll(resp.Body)
		var apiErr api.Error
		if jsonErr := json.Unmarshal(respBytes, &apiErr); jsonErr == nil && apiErr.Message != "" {
			return fmt.Errorf("API error (status %d): %s", resp.StatusCode, apiErr.Message)
		}
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("failed to decode response JSON: %w", err)
		}
	}

	return nil
}

// ListTorrents retrieves a list of torrents from TorrPlay.
func (c *Client) ListTorrents(ctx context.Context, limit, offset *int, category *string) (*api.ListTorrents, error) {
	values := url.Values{}
	if limit != nil && *limit > 0 {
		values.Set("limit", strconv.Itoa(*limit))
	}
	if offset != nil && *offset >= 0 {
		values.Set("offset", strconv.Itoa(*offset))
	}
	if category != nil && *category != "" {
		values.Set("categories", *category)
	}

	endpoint := "/api/v1/torrents"
	if q := values.Encode(); q != "" {
		endpoint += "?" + q
	}

	var res api.ListTorrents
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetTorrent retrieves metadata and file listing for a specific torrent.
func (c *Client) GetTorrent(ctx context.Context, hash string) (*api.Torrent, error) {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash)
	var res api.Torrent
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// AddTorrent adds a new torrent by magnet URI or info hash.
func (c *Client) AddTorrent(ctx context.Context, req api.TorrentAdd) (*api.Torrent, error) {
	endpoint := "/api/v1/torrents"
	var res api.Torrent
	if err := c.doRequest(ctx, http.MethodPost, endpoint, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// DeleteTorrent removes a torrent by its info hash.
func (c *Client) DeleteTorrent(ctx context.Context, hash string) error {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash)
	return c.doRequest(ctx, http.MethodDelete, endpoint, nil, nil)
}

// UpdateTorrent updates metadata (title, category, poster) for an existing torrent.
func (c *Client) UpdateTorrent(ctx context.Context, hash string, req api.TorrentUpdate) (*api.Torrent, error) {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash)
	var res api.Torrent
	if err := c.doRequest(ctx, http.MethodPatch, endpoint, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetMemoryStats returns global memory storage metrics.
func (c *Client) GetMemoryStats(ctx context.Context) (*api.MemoryStats, error) {
	endpoint := "/api/stats/memory"
	var res api.MemoryStats
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetTorrentStats returns real-time statistics for a single torrent.
func (c *Client) GetTorrentStats(ctx context.Context, hash string) (*api.TorrentStats, error) {
	endpoint := "/api/stats/torrents/" + url.PathEscape(hash)
	var res api.TorrentStats
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetSystemInfo returns system information including version and health.
func (c *Client) GetSystemInfo(ctx context.Context) (*api.SystemInfo, error) {
	endpoint := "/api/system/info"
	var res api.SystemInfo
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// CreatePlaybackToken exchanges the configured API credential for a playback-only token.
func (c *Client) CreatePlaybackToken(ctx context.Context) (*api.ScopedToken, error) {
	var res api.ScopedToken
	req := api.CreateTokenRequest{Scope: api.Playback}
	if err := c.doRequest(ctx, http.MethodPost, "/api/v1/tokens", req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// StreamURL returns the streaming URL for a given torrent and file index, optionally appending a playback token.
func (c *Client) StreamURL(hash string, fileIndex int, playbackToken ...string) string {
	values := url.Values{"index": {strconv.Itoa(fileIndex)}}
	addPlaybackToken(values, playbackToken)
	return fmt.Sprintf("%s/api/v1/stream/%s?%s", c.baseURL, url.PathEscape(hash), values.Encode())
}

// PlayURL returns the TorrServer-compatible direct play URL, optionally appending a playback token.
func (c *Client) PlayURL(hash string, fileIndex int, playbackToken ...string) string {
	values := url.Values{}
	addPlaybackToken(values, playbackToken)
	target := fmt.Sprintf("%s/play/%s/%d", c.baseURL, url.PathEscape(hash), fileIndex+1)
	if query := values.Encode(); query != "" {
		target += "?" + query
	}
	return target
}

// PlaylistURL returns the M3U playlist URL for a torrent name, optionally appending a playback token.
func (c *Client) PlaylistURL(name string, playbackToken ...string) string {
	values := url.Values{}
	if name != "" {
		values.Set("name", name+".m3u")
	}
	addPlaybackToken(values, playbackToken)
	if query := values.Encode(); query != "" {
		return c.baseURL + "/api/v1/playlist?" + query
	}
	return c.baseURL + "/api/v1/playlist"
}

func addPlaybackToken(values url.Values, tokens []string) {
	if len(tokens) > 0 && tokens[0] != "" {
		values.Set("token", tokens[0])
	}
}
