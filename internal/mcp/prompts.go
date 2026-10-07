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
)

// promptHash validates the required hash argument of a prompt and returns it
// normalized, matching how the tools treat info hashes.
func promptHash(req mcp.GetPromptRequest) (string, error) {
	hash := req.Params.Arguments["hash"]
	if strings.TrimSpace(hash) == "" {
		return "", errors.New("hash argument is required")
	}

	h, err := parseAndValidateHash(hash)
	if err != nil {
		return "", err
	}
	return h.HexString(), nil
}

func registerPrompts(s *server.MCPServer, client *Client) {
	s.AddPrompt(
		mcp.NewPrompt(
			"find_playable_file",
			mcp.WithPromptDescription("Inspects a torrent's files to find and recommend the primary video file to stream."),
			mcp.WithArgument("hash",
				mcp.RequiredArgument(),
				mcp.ArgumentDescription("40-character hex SHA-1 info hash of the torrent."),
			),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			h, err := promptHash(req)
			if err != nil {
				return nil, err
			}

			torrent, err := client.GetTorrent(ctx, h, "")
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
			mcp.WithArgument("hash",
				mcp.RequiredArgument(),
				mcp.ArgumentDescription("40-character hex SHA-1 info hash of the torrent."),
			),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			h, err := promptHash(req)
			if err != nil {
				return nil, err
			}

			stats, err := client.GetTorrentStats(ctx, h)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch torrent stats: %w", err)
			}

			memStats, memErr := client.GetMemoryStats(ctx)
			if memErr != nil {
				logger.Printf("warning: failed to fetch memory stats for stream_diagnostics: %v", memErr)
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
