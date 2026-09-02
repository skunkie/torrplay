// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			if l := req.GetInt("limit", 0); l > 0 {
				limit = &l
			}
			if o := req.GetInt("offset", -1); o >= 0 {
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

			torrent, err := client.GetTorrent(ctx, h.HexString())
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
			case strings.HasPrefix(rawURI, "magnet:"):
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
			mcp.WithDescription("Get direct HTTP streaming, TorrServer play, and M3U playlist URLs for a torrent file."),
			mcp.WithString("hash", mcp.Required(), mcp.Description("40-character hex info hash of the torrent.")),
			mcp.WithInteger("file_index", mcp.Description("Index of the file within the torrent (default 0).")),
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
			fileIndex := req.GetInt("file_index", 0)
			if fileIndex < 0 {
				return nil, errors.New("file_index must not be negative")
			}

			torrent, err := client.GetTorrent(ctx, cleanHash)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to get torrent: %v", err)), nil
			}
			if fileIndex >= len(torrent.Files) {
				return nil, fmt.Errorf("file_index %d is out of range for torrent with %d files", fileIndex, len(torrent.Files))
			}

			info := map[string]any{
				"hash":         cleanHash,
				"file_index":   fileIndex,
				"stream_url":   client.StreamURL(cleanHash, fileIndex),
				"play_url":     client.PlayURL(cleanHash, fileIndex),
				"playlist_url": client.PlaylistURL(torrent.Name),
			}

			data, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to encode stream URLs: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)

	// 7. get_memory_stats
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

	// 8. get_torrent_stats
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

	// 9. get_system_info
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
}
