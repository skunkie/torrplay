// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

func parseAndValidateHash(hash string) (metainfo.Hash, error) {
	clean := strings.TrimSpace(hash)
	h, err := utils.HashFromHexString(clean)
	if err != nil {
		return metainfo.Hash{}, fmt.Errorf("invalid info hash %q: must be 40-character hex string", clean)
	}
	return h, nil
}

// isMagnet reports whether uri carries the (case-insensitive) magnet scheme.
func isMagnet(uri string) bool {
	return strings.HasPrefix(strings.ToLower(uri), "magnet:")
}

// validateMagnet normalizes an optional magnet URI argument.
func validateMagnet(magnet string) (string, error) {
	clean := strings.TrimSpace(magnet)
	if clean != "" && !isMagnet(clean) {
		return "", fmt.Errorf("invalid magnet %q: must start with the magnet scheme", clean)
	}
	return clean, nil
}

// unsetInt is the sentinel returned by optionalIndex when an integer argument is absent.
const unsetInt = math.MinInt

// optionalIndex reads an optional non-negative integer argument. It returns unsetInt
// when the argument is omitted, and an error when an explicit negative value is given.
func optionalIndex(req mcp.CallToolRequest, name string) (int, error) {
	value := req.GetInt(name, unsetInt)
	if value != unsetInt && value < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}
	return value, nil
}

// registerTools adds every TorrPlay tool to the MCP server.
//
// Error convention: invalid arguments are reported as protocol errors (return nil, err)
// so the client can correct the call, while failures reaching TorrPlay or encoding its
// response are reported as tool errors (mcp.NewToolResultError) for the model to read.
func registerTools(s *server.MCPServer, client *Client) {
	// 1. list_torrents
	s.AddTool(
		mcp.NewTool("list_torrents",
			mcp.WithDescription("List torrents currently managed by TorrPlay with optional filtering and pagination."),
			mcp.WithInteger("limit", mcp.Description("Maximum number of torrents to return (default 50).")),
			mcp.WithInteger("offset", mcp.Description("Pagination offset (default 0).")),
			mcp.WithString("category", mcp.Description("Optional category to filter by (e.g. Movies, Series).")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var limit, offset *int
			l, err := optionalIndex(req, "limit")
			if err != nil {
				return nil, err
			}
			if l != unsetInt {
				if l == 0 {
					return nil, errors.New("limit must be greater than zero")
				}
				limit = &l
			}
			o, err := optionalIndex(req, "offset")
			if err != nil {
				return nil, err
			}
			if o != unsetInt {
				offset = &o
			}
			var category *string
			if cat := req.GetString("category", ""); cat != "" {
				category = &cat
			}

			list, err := client.ListTorrents(ctx, limit, offset, category)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to list torrents: %v", err)), nil
			}

			data, err := json.MarshalIndent(list, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode response: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 2. get_torrent
	s.AddTool(
		mcp.NewTool("get_torrent",
			mcp.WithDescription("Get detailed metadata, file list, and status for a specific torrent by its info hash."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex SHA-1 info hash.")),
			mcp.WithString("magnet", mcp.Description("Optional magnet URI used to resolve metadata for a torrent that is not stored in TorrPlay, or to add its trackers to a stored one.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}

			magnet, err := validateMagnet(req.GetString("magnet", ""))
			if err != nil {
				return nil, err
			}

			torrent, err := client.GetTorrent(ctx, h.HexString(), magnet)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get torrent: %v", err)), nil
			}

			data, err := json.MarshalIndent(torrent, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode response: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 3. add_torrent
	s.AddTool(
		mcp.NewTool("add_torrent",
			mcp.WithDescription("Add a torrent to TorrPlay via magnet URI or 40-character hex info hash."),
			mcp.WithString("uri", mcp.Required(), mcp.Description("Magnet link (magnet:?xt=...) or 40-character info hash.")),
			mcp.WithString("title", mcp.Description("Optional display title.")),
			mcp.WithString("category", mcp.Description("Optional category (e.g., Movies, Series).")),
			mcp.WithString("storage", mcp.Description("Optional storage type ('memory' or 'file').")),
			mcp.WithString("poster", mcp.Description("Optional poster image URL or base64 data URI.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			rawURI, err := req.RequireString("uri")
			if err != nil {
				return nil, err
			}
			rawURI = strings.TrimSpace(rawURI)

			var addReq api.TorrentAdd
			switch {
			case isMagnet(rawURI):
				addReq.Magnet = &rawURI
			default:
				parsedHash, hashErr := utils.HashFromHexString(rawURI)
				if hashErr != nil {
					return nil, fmt.Errorf("uri must be a valid magnet link (magnet:?xt=...) or 40-character hex info hash: %w", hashErr)
				}
				addReq.Hash = &parsedHash
			}

			if title := req.GetString("title", ""); title != "" {
				addReq.Title = &title
			}
			if category := req.GetString("category", ""); category != "" {
				addReq.Category = &category
			}
			if poster := req.GetString("poster", ""); poster != "" {
				addReq.Poster = &poster
			}
			if storage := req.GetString("storage", ""); storage != "" {
				st := api.TorrentStorage(storage)
				addReq.Storage = &st
			}

			created, err := client.AddTorrent(ctx, addReq)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to add torrent: %v", err)), nil
			}

			data, err := json.MarshalIndent(created, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode response: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 4. delete_torrent
	s.AddTool(
		mcp.NewTool("delete_torrent",
			mcp.WithDescription("Remove a torrent and its downloaded data from TorrPlay by its info hash."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent to delete.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}
			if err := client.DeleteTorrent(ctx, h.HexString()); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to delete torrent: %v", err)), nil
			}

			return mcp.NewToolResultText(fmt.Sprintf("Torrent %s successfully deleted.", h.HexString())), nil
		},
	)

	// 5. update_torrent
	s.AddTool(
		mcp.NewTool("update_torrent",
			mcp.WithDescription("Update metadata for an existing torrent (title, category, or poster)."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent.")),
			mcp.WithString("title", mcp.Description("Optional new display title.")),
			mcp.WithString("category", mcp.Description("Optional new category.")),
			mcp.WithString("poster", mcp.Description("Optional new poster URL.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}

			var updateReq api.TorrentUpdate
			if title := req.GetString("title", ""); title != "" {
				updateReq.Title = &title
			}
			if category := req.GetString("category", ""); category != "" {
				updateReq.Category = &category
			}
			if poster := req.GetString("poster", ""); poster != "" {
				updateReq.Poster = &poster
			}

			updated, err := client.UpdateTorrent(ctx, h.HexString(), updateReq)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to update torrent: %v", err)), nil
			}

			data, err := json.MarshalIndent(updated, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode response: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 6. get_stream_url
	s.AddTool(
		mcp.NewTool("get_stream_url",
			mcp.WithDescription("Get direct HTTP streaming and M3U playlist URLs for a torrent file."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent.")),
			mcp.WithInteger("file_index", mcp.Description("Index of the file within the torrent (default 0).")),
			mcp.WithString("magnet", mcp.Description("Optional magnet URI enabling playback of a torrent that is not stored in TorrPlay. Only stream_url supports it; playlist_url requires a stored torrent.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}
			cleanHash := h.HexString()
			fileIndex, err := optionalIndex(req, "file_index")
			if err != nil {
				return nil, err
			}
			if fileIndex == unsetInt {
				fileIndex = 0
			}

			magnet, err := validateMagnet(req.GetString("magnet", ""))
			if err != nil {
				return nil, err
			}

			torrent, err := client.GetTorrent(ctx, cleanHash, magnet)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get torrent: %v", err)), nil
			}
			if fileIndex >= len(torrent.Files) {
				return nil, fmt.Errorf("file_index %d is out of range for torrent with %d files", fileIndex, len(torrent.Files))
			}
			playback, err := client.CreatePlaybackToken(ctx)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to create playback token: %v", err)), nil
			}

			info := map[string]any{
				"hash":       cleanHash,
				"file_index": fileIndex,
				"stream_url": client.StreamURL(cleanHash, fileIndex, magnet, playback.Token),
			}
			// PlaylistURL without a name returns the playlist of every torrent, which is
			// not what this tool promises, so it is omitted for an unnamed torrent.
			if torrent.Name != "" {
				info["playlist_url"] = client.PlaylistURL(torrent.Name, playback.Token)
			}

			data, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode stream URLs: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 7. preload_torrent
	s.AddTool(
		mcp.NewTool("preload_torrent",
			mcp.WithDescription("Start or update preloading of a torrent file so playback can begin without buffering, and report the current progress."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent.")),
			mcp.WithInteger("file_index", mcp.Description("Index of the file to preload. Takes precedence over file_path; defaults to file 0 when both are omitted.")),
			mcp.WithString("file_path", mcp.Description("Relative path of the file to preload within the torrent.")),
			mcp.WithString("magnet", mcp.Description("Optional magnet URI used to bootstrap a torrent that is not stored in TorrPlay.")),
			mcp.WithNumber("playback_position_seconds", mcp.Description("Zero-based position on the selected media file's presentation timeline in seconds. The server resolves it through the container index.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}

			magnet, err := validateMagnet(req.GetString("magnet", ""))
			if err != nil {
				return nil, err
			}

			fileIndex, err := optionalIndex(req, "file_index")
			if err != nil {
				return nil, err
			}

			var preloadReq api.PreloadRequest
			if fileIndex != unsetInt {
				preloadReq.FileIndex = &fileIndex
			}
			if filePath := req.GetString("file_path", ""); filePath != "" {
				preloadReq.FilePath = &filePath
			}
			if magnet != "" {
				preloadReq.Magnet = &magnet
			}
			arguments := req.GetArguments()
			if _, ok := arguments["playback_position_seconds"]; ok {
				playbackPositionSeconds, positionErr := req.RequireFloat("playback_position_seconds")
				if positionErr != nil {
					return nil, positionErr
				}
				preloadReq.PlaybackPositionSeconds = &playbackPositionSeconds
			}

			status, err := client.PreloadTorrent(ctx, h.HexString(), preloadReq)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to preload torrent: %v", err)), nil
			}

			data, err := json.MarshalIndent(status, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode preload status: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 8. get_preload_status
	s.AddTool(
		mcp.NewTool("get_preload_status",
			mcp.WithDescription("Get the current preload state, target buffer size, and progress for a torrent."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}

			status, err := client.GetPreloadStatus(ctx, h.HexString())
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get preload status: %v", err)), nil
			}

			data, err := json.MarshalIndent(status, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode preload status: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 9. cancel_preload
	s.AddTool(
		mcp.NewTool("cancel_preload",
			mcp.WithDescription("Cancel an active preload for a torrent and release its reserved buffers."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}

			if err := client.CancelPreload(ctx, h.HexString()); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to cancel preload: %v", err)), nil
			}

			return mcp.NewToolResultText(fmt.Sprintf("Preload for torrent %s canceled.", h.HexString())), nil
		},
	)

	// 10. get_memory_stats
	s.AddTool(
		mcp.NewTool("get_memory_stats",
			mcp.WithDescription("Get current RAM cache usage, limits, and active piece buffer stats."),
		),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			stats, err := client.GetMemoryStats(ctx)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get memory stats: %v", err)), nil
			}

			data, err := json.MarshalIndent(stats, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode memory stats: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 11. get_torrent_stats
	s.AddTool(
		mcp.NewTool("get_torrent_stats",
			mcp.WithDescription("Get real-time downloading speed, connected peers, and piece status for a torrent."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			hash, err := req.RequireString("hash")
			if err != nil {
				return nil, err
			}

			h, err := parseAndValidateHash(hash)
			if err != nil {
				return nil, err
			}

			stats, err := client.GetTorrentStats(ctx, h.HexString())
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get torrent stats: %v", err)), nil
			}

			data, err := json.MarshalIndent(stats, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode torrent stats: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 12. get_system_info
	s.AddTool(
		mcp.NewTool("get_system_info",
			mcp.WithDescription("Get TorrPlay system information, version, uptime, and service health."),
		),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			info, err := client.GetSystemInfo(ctx)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get system info: %v", err)), nil
			}

			data, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode system info: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 13. get_system_logs
	s.AddTool(
		mcp.NewTool("get_system_logs",
			mcp.WithDescription("Get retained TorrPlay application log entries, newest first, with optional search and level filtering."),
			mcp.WithString("q", mcp.Description("Case-insensitive text to find in log messages or structured fields.")),
			mcp.WithString("level", mcp.Description("Exact log level: DEBUG, INFO, WARN, or ERROR.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			level := strings.ToUpper(strings.TrimSpace(req.GetString("level", "")))
			switch level {
			case "", "DEBUG", "INFO", "WARN", "ERROR":
			default:
				return nil, fmt.Errorf("invalid log level %q", level)
			}
			logs, err := client.SearchSystemLogs(ctx, strings.TrimSpace(req.GetString("q", "")), level)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get system logs: %v", err)), nil
			}

			data, err := json.MarshalIndent(logs, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode system logs: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 14. get_system_metrics
	s.AddTool(
		mcp.NewTool("get_system_metrics",
			mcp.WithDescription("Get real-time system metrics such as torrent activity and network transfer speeds."),
		),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			metrics, err := client.GetSystemMetrics(ctx)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get system metrics: %v", err)), nil
			}

			data, err := json.MarshalIndent(metrics, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode system metrics: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)
}
