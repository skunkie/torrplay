// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/pkg/storage"
)

func TestNew(t *testing.T) {
	m := New()
	assert.NotNil(t, m)
	assert.NotNil(t, m.reg)
	assert.NotNil(t, m.StreamRequestsInFlight)
	assert.NotNil(t, m.HTTPRequestsTotal)
	assert.NotNil(t, m.HTTPRequestDuration)
	assert.NotNil(t, m.HTTPRequestSizeBytes)
	assert.NotNil(t, m.HTTPResponseSizeBytes)
}

func TestMetrics_Handler(t *testing.T) {
	m := New()
	require.NotNil(t, m)

	// Increment a gauge to have a value to check for.
	m.IncStreamRequests()
	m.IncStreamRequests()
	m.DecStreamRequests()

	// Observe some values for other metrics.
	m.HTTPRequestsTotal.WithLabelValues("200", "GET", "/metrics").Inc()
	m.HTTPRequestDuration.WithLabelValues("200", "GET", "/metrics").Observe(0.5)
	m.HTTPRequestSizeBytes.WithLabelValues("200", "GET", "/metrics").Observe(123)
	m.HTTPResponseSizeBytes.WithLabelValues("200", "GET", "/metrics").Observe(456)

	req := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
	rr := httptest.NewRecorder()

	m.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	body, err := io.ReadAll(rr.Body)
	require.NoError(t, err)

	bodyStr := string(body)

	// Check for Go metrics (from collectors.NewGoCollector()).
	assert.Contains(t, bodyStr, "go_goroutines")

	// Check for process metrics (from collectors.NewProcessCollector()).
	assert.Contains(t, bodyStr, "process_cpu_seconds_total")

	// Check for our custom metrics.
	assert.Contains(t, bodyStr, "torrplay_stream_requests_in_flight 1")
	assert.Contains(t, bodyStr, `http_requests_total{code="200",method="GET",path="/metrics"} 1`)
	assert.Contains(t, bodyStr, `http_request_duration_seconds_bucket{code="200",method="GET",path="/metrics",le="0.5"} 1`)
	assert.Contains(t, bodyStr, `http_request_size_bytes_sum{code="200",method="GET",path="/metrics"} 123`)
	assert.Contains(t, bodyStr, `http_response_size_bytes_sum{code="200",method="GET",path="/metrics"} 456`)
}

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	require.Equal(t, http.StatusOK, rr.Code)
	body, err := io.ReadAll(rr.Body)
	require.NoError(t, err)
	return string(body)
}

func TestMetrics_SetEngineStatsSource(t *testing.T) {
	m := New()
	assert.NotContains(t, scrape(t, m), "torrplay_storage_", "engine metrics are omitted until a source is set")

	m.SetEngineStatsSource(func() EngineStats {
		return EngineStats{
			BannedPeers:              2,
			BytesDownloaded:          1000,
			BytesUploaded:            200,
			LoadedTorrentsBackground: 19,
			LoadedTorrentsOnDemand:   3,
			MemoryLimitBytes:         512,
			MemoryUsedBytes:          256,
			PeersActive:              4,
			PeersHalfOpen:            5,
			PeersPending:             6,
			PiecesHashedBad:          7,
			PiecesHashedGood:         8,
			Storage: storage.Counters{
				ActiveRangeEvictions:    9,
				BoundaryEvictions:       10,
				CompletionMisses:        11,
				EvictedCompletePieces:   12,
				EvictedIncompleteBytes:  13,
				EvictedIncompletePieces: 14,
				IncompleteHashes:        15,
				IncompleteReads:         16,
				ReadMisses:              17,
			},
			StreamingTorrents: 18,
		}
	})
	body := scrape(t, m)
	for _, line := range []string{
		"torrplay_torrent_banned_peers 2",
		`torrplay_torrent_data_bytes_total{direction="download"} 1000`,
		`torrplay_torrent_data_bytes_total{direction="upload"} 200`,
		`torrplay_torrents_loaded{reason="background"} 19`,
		`torrplay_torrents_loaded{reason="on_demand"} 3`,
		`torrplay_torrent_peers{state="active"} 4`,
		`torrplay_torrent_peers{state="half_open"} 5`,
		`torrplay_torrent_peers{state="pending"} 6`,
		`torrplay_torrent_pieces_hashed_total{result="bad"} 7`,
		`torrplay_torrent_pieces_hashed_total{result="good"} 8`,
		"torrplay_storage_memory_limit_bytes 512",
		"torrplay_storage_memory_used_bytes 256",
		`torrplay_storage_protected_evictions_total{protection="active_range"} 9`,
		`torrplay_storage_protected_evictions_total{protection="boundary"} 10`,
		"torrplay_storage_completion_misses_total 11",
		`torrplay_storage_evicted_pieces_total{state="complete"} 12`,
		"torrplay_storage_evicted_incomplete_bytes_total 13",
		`torrplay_storage_evicted_pieces_total{state="incomplete"} 14`,
		"torrplay_storage_incomplete_hashes_total 15",
		`torrplay_storage_read_failures_total{reason="incomplete"} 16`,
		`torrplay_storage_read_failures_total{reason="evicted"} 17`,
		"torrplay_streaming_torrents 18",
	} {
		assert.Contains(t, body, line)
	}
}

func TestMetrics_StreamReadObserver(t *testing.T) {
	m := New()
	observeMemoryRead := m.StreamReadObserver("memory")
	observeFileRead := m.StreamReadObserver("file")
	observeMemoryRead(2 * time.Millisecond)
	observeMemoryRead(3 * time.Second)
	observeFileRead(time.Millisecond)

	body := scrape(t, m)
	assert.Contains(t, body, `torrplay_stream_read_duration_seconds_bucket{storage="memory",le="0.005"} 1`)
	assert.Contains(t, body, `torrplay_stream_read_duration_seconds_bucket{storage="memory",le="5"} 2`)
	assert.Contains(t, body, `torrplay_stream_read_duration_seconds_count{storage="memory"} 2`)
	assert.Contains(t, body, `torrplay_stream_read_duration_seconds_count{storage="file"} 1`)
}
