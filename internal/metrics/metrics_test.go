// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	m := New()
	assert.NotNil(t, m)
	assert.NotNil(t, m.reg)
	assert.NotNil(t, m.StreamingTorrents)
	assert.NotNil(t, m.HTTPRequestsTotal)
	assert.NotNil(t, m.HTTPRequestDuration)
	assert.NotNil(t, m.HTTPRequestSizeBytes)
	assert.NotNil(t, m.HTTPResponseSizeBytes)
}

func TestMetrics_Handler(t *testing.T) {
	m := New()
	require.NotNil(t, m)

	// Increment a gauge to have a value to check for.
	m.StreamingTorrents.Inc()

	// Observe some values for other metrics.
	m.HTTPRequestsTotal.WithLabelValues("200", "GET", "/metrics").Inc()
	m.HTTPRequestDuration.WithLabelValues("200", "GET", "/metrics").Observe(0.5)
	m.HTTPRequestSizeBytes.WithLabelValues("200", "GET", "/metrics").Observe(123)
	m.HTTPResponseSizeBytes.WithLabelValues("200", "GET", "/metrics").Observe(456)

	req := httptest.NewRequest("GET", "/metrics", nil)
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
	assert.Contains(t, bodyStr, "torrplay_streaming_torrents 1")
	assert.Contains(t, bodyStr, `http_requests_total{code="200",method="GET",path="/metrics"} 1`)
	assert.Contains(t, bodyStr, `http_request_duration_seconds_bucket{code="200",method="GET",path="/metrics",le="0.5"} 1`)
	assert.Contains(t, bodyStr, `http_request_size_bytes_sum{code="200",method="GET",path="/metrics"} 123`)
	assert.Contains(t, bodyStr, `http_response_size_bytes_sum{code="200",method="GET",path="/metrics"} 456`)
}
