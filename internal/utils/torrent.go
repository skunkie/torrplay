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
	"github.com/torrplay/torrplay/internal/httpclient"
)

// AddTrackersToSpec adds default trackers to a torrent spec.
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
		for _, t := range tier {
			if !allTrackers[strings.ToLower(t)] {
				// Add the new tracker to its own tier.
				spec.Trackers = append(spec.Trackers, []string{t})
				allTrackers[strings.ToLower(t)] = true
			}
		}
	}
}

// FetchTrackers fetches a list of trackers from a remote source.
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

	// Convert to string and and split by lines.
	content := string(body)
	lines := strings.Split(content, "\n")

	// Filter out empty lines and create a slice.
	var trackers []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			trackers = append(trackers, line)
		}
	}

	return [][]string{trackers}, nil
}
