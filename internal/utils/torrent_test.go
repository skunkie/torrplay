// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
)

func TestAddTrackersToSpec(t *testing.T) {
	defaultTrackers := [][]string{
		{"http://tracker.test/announce"},
	}

	tests := []struct {
		name             string
		initialTrackers  [][]string
		defaultTrackers  [][]string
		expectedTrackers [][]string
		expectedNumTiers int
		expectedNumTotal int
	}{
		{
			name:             "no default trackers",
			initialTrackers:  [][]string{{"http://tracker.existing/announce"}},
			defaultTrackers:  [][]string{},
			expectedTrackers: [][]string{{"http://tracker.existing/announce"}},
			expectedNumTiers: 1,
			expectedNumTotal: 1,
		},
		{
			name:             "no trackers in spec",
			initialTrackers:  [][]string{},
			defaultTrackers:  defaultTrackers,
			expectedTrackers: [][]string{{"http://tracker.test/announce"}},
			expectedNumTiers: 1,
			expectedNumTotal: 1,
		},
		{
			name:             "no duplicates",
			initialTrackers:  [][]string{{"http://tracker.test/announce"}},
			defaultTrackers:  defaultTrackers,
			expectedTrackers: [][]string{{"http://tracker.test/announce"}},
			expectedNumTiers: 1,
			expectedNumTotal: 1,
		},
		{
			name:             "no duplicates case insensitive",
			initialTrackers:  [][]string{{"HTTP://tracker.test/announce"}},
			defaultTrackers:  defaultTrackers,
			expectedTrackers: [][]string{{"HTTP://tracker.test/announce"}},
			expectedNumTiers: 1,
			expectedNumTotal: 1,
		},
		{
			name:             "merge trackers",
			initialTrackers:  [][]string{{"http://tracker.existing/announce"}},
			defaultTrackers:  defaultTrackers,
			expectedTrackers: [][]string{{"http://tracker.existing/announce"}, {"http://tracker.test/announce"}},
			expectedNumTiers: 2,
			expectedNumTotal: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &torrent.TorrentSpec{
				Trackers: tt.initialTrackers,
			}

			AddTrackersToSpec(spec, tt.defaultTrackers)

			assert.Len(t, spec.Trackers, tt.expectedNumTiers, "number of tiers should match")

			// Since the order of trackers is not guaranteed, we check for presence and total count.
			var totalTrackers int
			for _, tier := range spec.Trackers {
				totalTrackers += len(tier)
			}
			assert.Equal(t, tt.expectedNumTotal, totalTrackers, "total number of trackers should match")

			// Create a map of expected trackers for efficient lookup.
			expectedTrackersMap := make(map[string]bool)
			for _, tier := range tt.expectedTrackers {
				for _, tr := range tier {
					expectedTrackersMap[tr] = true
				}
			}

			// Check if all trackers in the spec are expected.
			for _, tier := range spec.Trackers {
				for _, tr := range tier {
					assert.True(t, expectedTrackersMap[tr], "tracker %s should be in the expected list", tr)
				}
			}
		})
	}
}

func TestMagnetURIFromHash(t *testing.T) {
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	expected := "magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10"
	assert.Equal(t, expected, MagnetURIFromHash(ih))
}
