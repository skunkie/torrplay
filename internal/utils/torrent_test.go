// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestParseAndValidateMagnet(t *testing.T) {
	ih := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	t.Run("valid magnet matching the expected hash", func(t *testing.T) {
		magnetV2, err := ParseAndValidateMagnet(
			"magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel&tr=http%3A%2F%2Ftracker.example.com%2Fannounce",
			ih,
		)
		require.NoError(t, err)
		assert.Equal(t, ih, magnetV2.InfoHash.Value)
		assert.Equal(t, []string{"http://tracker.example.com/announce"}, magnetV2.Trackers)
	})

	t.Run("not a magnet URI", func(t *testing.T) {
		_, err := ParseAndValidateMagnet("not-a-magnet", ih)
		assert.Error(t, err)
	})

	t.Run("malformed magnet URI", func(t *testing.T) {
		_, err := ParseAndValidateMagnet("magnet:?dn=Sintel", ih)
		assert.Error(t, err)
	})

	t.Run("zero info hash", func(t *testing.T) {
		_, err := ParseAndValidateMagnet("magnet:?xt=urn:btih:0000000000000000000000000000000000000000", ih)
		assert.Error(t, err)
	})

	t.Run("info hash does not match expected hash", func(t *testing.T) {
		other := metainfo.NewHashFromHex("1111111111111111111111111111111111111111")
		_, err := ParseAndValidateMagnet("magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10", other)
		assert.Error(t, err)
	})
}
