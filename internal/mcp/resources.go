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

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/torrplay/torrplay/internal/utils"
)

func registerResources(s *server.MCPServer, client *Client) {
	// 1. torrplay://torrents
	s.AddResource(
		mcp.NewResource(
			"torrplay://torrents",
			"Active Torrents",
			mcp.WithResourceDescription("List of all torrents currently tracked by TorrPlay in JSON format."),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			list, err := client.ListTorrents(ctx, nil, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch torrents: %w", err)
			}

			data, err := json.MarshalIndent(list, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to serialize torrents: %w", err)
			}

			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "application/json",
					Text:     string(data),
				},
			}, nil
		},
	)

	// 2. torrplay://torrents/{hash}
	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"torrplay://torrents/{hash}",
			"Torrent Details",
			mcp.WithTemplateDescription("Detailed metadata, file hierarchy, and piece info for a single torrent."),
			mcp.WithTemplateMIMEType("application/json"),
		),
		torrentDetailHandler(client),
	)

	// 3. torrplay://system/memory
	s.AddResource(
		mcp.NewResource(
			"torrplay://system/memory",
			"System Memory Statistics",
			mcp.WithResourceDescription("Real-time memory buffer utilization, cache limits, and allocations."),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			stats, err := client.GetMemoryStats(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch memory stats: %w", err)
			}

			data, err := json.MarshalIndent(stats, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to serialize memory stats: %w", err)
			}

			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "application/json",
					Text:     string(data),
				},
			}, nil
		},
	)

	// 4. torrplay://system/info
	s.AddResource(
		mcp.NewResource(
			"torrplay://system/info",
			"System Information",
			mcp.WithResourceDescription("TorrPlay application version, build commit, uptime, and system status."),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			info, err := client.GetSystemInfo(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch system info: %w", err)
			}

			data, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to serialize system info: %w", err)
			}

			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "application/json",
					Text:     string(data),
				},
			}, nil
		},
	)
}

// torrentDetailHandler returns the handler for the torrplay://torrents/{hash} resource
// template. It is a named function so tests can invoke it directly.
func torrentDetailHandler(client *Client) server.ResourceTemplateHandlerFunc {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		hash, ok := req.Params.Arguments["hash"].(string)
		if !ok || hash == "" {
			const prefix = "torrplay://torrents/"
			if after, found := strings.CutPrefix(req.Params.URI, prefix); found {
				hash = strings.Trim(after, "/")
			}
		}
		cleanHash := strings.TrimSpace(hash)
		if cleanHash == "" {
			return nil, errors.New("missing hash in resource URI")
		}

		h, err := utils.HashFromHexString(cleanHash)
		if err != nil {
			return nil, fmt.Errorf("invalid info hash in resource URI %q: %w", req.Params.URI, err)
		}

		torrent, err := client.GetTorrent(ctx, h.HexString())
		if err != nil {
			return nil, fmt.Errorf("failed to fetch torrent %s: %w", h.HexString(), err)
		}

		data, err := json.MarshalIndent(torrent, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize torrent: %w", err)
		}

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}
