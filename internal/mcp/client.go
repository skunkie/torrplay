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

// requestTimeout bounds a single API call. It must stay above the server's own
// metadata wait (gotInfoTimeout, 30s) so that magnet-bootstrapped requests surface
// the server's "timeout waiting for torrent metadata" error instead of an opaque
// client-side deadline.
const requestTimeout = 60 * time.Second

// maxErrorBody caps how much of an error response body is buffered for reporting.
const maxErrorBody = 64 << 10

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
			Timeout: requestTimeout,
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
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
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

// GetTorrent retrieves metadata and file listing for a specific torrent. An optional
// magnet URI bootstraps discovery for torrents that are not registered in the database
// and appends its trackers to torrents that are.
func (c *Client) GetTorrent(ctx context.Context, hash, magnet string) (*api.Torrent, error) {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash)
	if magnet != "" {
		endpoint += "?" + url.Values{"magnet": {magnet}}.Encode()
	}
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

// PreloadTorrent starts or updates preloading of a torrent file and returns its progress.
func (c *Client) PreloadTorrent(ctx context.Context, hash string, req api.PreloadRequest) (*api.PreloadResponse, error) {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash) + "/preload"
	var res api.PreloadResponse
	if err := c.doRequest(ctx, http.MethodPut, endpoint, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetPreloadStatus returns the current preload state and progress for a torrent.
func (c *Client) GetPreloadStatus(ctx context.Context, hash string) (*api.PreloadResponse, error) {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash) + "/preload"
	var res api.PreloadResponse
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// CancelPreload cancels an active preload and releases its buffers.
func (c *Client) CancelPreload(ctx context.Context, hash string) error {
	endpoint := "/api/v1/torrents/" + url.PathEscape(hash) + "/preload"
	return c.doRequest(ctx, http.MethodDelete, endpoint, nil, nil)
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

// GetSystemLogs returns the most recent application log entries.
func (c *Client) GetSystemLogs(ctx context.Context) ([]api.LogEntry, error) {
	return c.SearchSystemLogs(ctx, "", "")
}

// SearchSystemLogs filters the application's retained log entries.
func (c *Client) SearchSystemLogs(ctx context.Context, query, level string) ([]api.LogEntry, error) {
	params := url.Values{}
	if query != "" {
		params.Set("q", query)
	}
	if level != "" {
		params.Set("level", level)
	}
	endpoint := "/api/system/logs"
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	var res []api.LogEntry
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// GetSystemMetrics returns real-time torrent activity and network metrics.
func (c *Client) GetSystemMetrics(ctx context.Context) (*api.SystemMetrics, error) {
	var res api.SystemMetrics
	if err := c.doRequest(ctx, http.MethodGet, "/api/system/metrics", nil, &res); err != nil {
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

// StreamURL returns the streaming URL for a given torrent and file index. An optional
// magnet URI lets the server stream torrents that are not registered in the database,
// and an optional playback token authorizes the request.
func (c *Client) StreamURL(hash string, fileIndex int, magnet, playbackToken string) string {
	values := url.Values{"index": {strconv.Itoa(fileIndex)}}
	setIfNotEmpty(values, "magnet", magnet)
	setIfNotEmpty(values, "token", playbackToken)
	return fmt.Sprintf("%s/api/v1/stream/%s?%s", c.baseURL, url.PathEscape(hash), values.Encode())
}

// PlaylistURL returns the M3U playlist URL for a single torrent, optionally appending
// a playback token. An empty name yields the master playlist covering every torrent.
func (c *Client) PlaylistURL(name, playbackToken string) string {
	values := url.Values{}
	if name != "" {
		values.Set("name", name+".m3u")
	}
	setIfNotEmpty(values, "token", playbackToken)
	if query := values.Encode(); query != "" {
		return c.baseURL + "/api/v1/playlist?" + query
	}
	return c.baseURL + "/api/v1/playlist"
}

func setIfNotEmpty(values url.Values, key, value string) {
	if value != "" {
		values.Set(key, value)
	}
}
