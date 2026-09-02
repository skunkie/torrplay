// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerPrompts(s *server.MCPServer, client *Client) {
	s.AddPrompt(
		mcp.NewPrompt(
			"find_playable_file",
			mcp.WithPromptDescription("Inspects a torrent's files to find and recommend the primary video file to stream."),
			mcp.WithArgument("hash", mcp.ArgumentDescription("SHA-1 info hash of the torrent.")),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			hash := req.Params.Arguments["hash"]
			if hash == "" {
				return nil, errors.New("hash argument is required")
			}

			torrent, err := client.GetTorrent(ctx, hash)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch torrent: %w", err)
			}

			filesJSON, _ := json.MarshalIndent(torrent.Files, "", "  ")

			promptText := fmt.Sprintf(
				"Here are the files for torrent '%s' (hash: %s):\n```json\n%s\n```\n\n"+
					"Please inspect these files and determine the primary video stream file (ignore sample clips, extras, subtitles, or small files). "+
					"Report its file index and provide the direct stream URL using `get_stream_url`.",
				torrent.Name,
				torrent.Hash.HexString(),
				string(filesJSON),
			)

			return &mcp.GetPromptResult{
				Description: "Select primary video file for streaming",
				Messages: []mcp.PromptMessage{
					mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(promptText)),
				},
			}, nil
		},
	)

	s.AddPrompt(
		mcp.NewPrompt(
			"stream_diagnostics",
			mcp.WithPromptDescription("Examines buffer, piece availability, and peer stats to diagnose streaming performance."),
			mcp.WithArgument("hash", mcp.ArgumentDescription("SHA-1 info hash of the torrent.")),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			hash := req.Params.Arguments["hash"]
			if hash == "" {
				return nil, errors.New("hash argument is required")
			}

			stats, err := client.GetTorrentStats(ctx, hash)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch torrent stats: %w", err)
			}

			memStats, memErr := client.GetMemoryStats(ctx)
			if memErr != nil {
				fmt.Fprintf(os.Stderr, "[mcp] warning: failed to fetch memory stats for stream_diagnostics: %v\n", memErr)
			}

			statsJSON, _ := json.MarshalIndent(stats, "", "  ")
			memJSON, _ := json.MarshalIndent(memStats, "", "  ")

			promptText := fmt.Sprintf(
				"Torrent stats:\n```json\n%s\n```\n\nMemory stats:\n```json\n%s\n```\n\n"+
					"Evaluate if this torrent has sufficient seeds/peers and piece progress for continuous, uninterrupted streaming without buffering.",
				string(statsJSON),
				string(memJSON),
			)

			return &mcp.GetPromptResult{
				Description: "Diagnose streaming performance and buffer health",
				Messages: []mcp.PromptMessage{
					mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(promptText)),
				},
			}, nil
		},
	)
}
