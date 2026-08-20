// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/httpclient"
)

// AddTrackersToSpec adds default trackers to a torrent spec.
// defaultTrackers is expected to already be organized into tiers
// (e.g. by FetchTrackers), and is appended as-is, tier by tier.
func AddTrackersToSpec(spec *torrent.TorrentSpec, defaultTrackers [][]string) {
	if len(defaultTrackers) == 0 {
		return
	}

	allTrackers := make(map[string]bool)
	for _, tier := range spec.Trackers {
		for _, t := range tier {
			allTrackers[strings.ToLower(t)] = true
		}
	}

	for _, tier := range defaultTrackers {
		var newTier []string
		for _, t := range tier {
			if !allTrackers[strings.ToLower(t)] {
				newTier = append(newTier, t)
				allTrackers[strings.ToLower(t)] = true
			}
		}
		if len(newTier) > 0 {
			spec.Trackers = append(spec.Trackers, newTier)
		}
	}
}

// FetchTrackers fetches a list of trackers from a remote source,
// returning each tracker in its own tier.
func FetchTrackers(ctx context.Context, client *httpclient.Client) ([][]string, error) {
	url := "https://raw.githubusercontent.com/ngosang/trackerslist/master/trackers_best_ip.txt"

	resp, err := client.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Convert to string and split by lines.
	content := string(body)
	lines := strings.Split(content, "\n")

	// Filter out empty lines, put each tracker in its own tier.
	var tiers [][]string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			tiers = append(tiers, []string{line})
		}
	}

	return tiers, nil
}

// MagnetURIFromHash returns a magnet URI string for the specified info hash.
func MagnetURIFromHash(ih metainfo.Hash) string {
	return "magnet:?xt=urn:btih:" + ih.HexString()
}
