// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus metrics.
type Metrics struct {
	DownloadingTorrents   prometheus.Gauge
	StreamingTorrents     prometheus.Gauge
	reg                   *prometheus.Registry
	HTTPRequestsTotal     *prometheus.CounterVec
	HTTPRequestDuration   *prometheus.HistogramVec
	HTTPRequestSizeBytes  *prometheus.SummaryVec
	HTTPResponseSizeBytes *prometheus.SummaryVec
}

// New creates a new Metrics instance.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &Metrics{
		DownloadingTorrents: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "torrplay_downloading_torrents",
			Help: "Number of torrents currently being downloaded in the background.",
		}),
		StreamingTorrents: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "torrplay_streaming_torrents",
			Help: "Number of torrents currently being streamed.",
		}),
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests.",
			},
			[]string{"code", "method", "path"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "Histogram of request durations.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"code", "method", "path"},
		),
		HTTPRequestSizeBytes: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name: "http_request_size_bytes",
				Help: "Summary of request sizes.",
			},
			[]string{"code", "method", "path"},
		),
		HTTPResponseSizeBytes: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name: "http_response_size_bytes",
				Help: "Summary of response sizes.",
			},
			[]string{"code", "method", "path"},
		),
		reg: reg,
	}

	reg.MustRegister(m.DownloadingTorrents)
	reg.MustRegister(m.StreamingTorrents)
	reg.MustRegister(m.HTTPRequestsTotal)
	reg.MustRegister(m.HTTPRequestDuration)
	reg.MustRegister(m.HTTPRequestSizeBytes)
	reg.MustRegister(m.HTTPResponseSizeBytes)

	return m
}

// Handler returns an HTTP handler for the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// IncStreamingTorrents increments the streaming torrents gauge.
func (m *Metrics) IncStreamingTorrents() {
	m.StreamingTorrents.Inc()
}

// DecStreamingTorrents decrements the streaming torrents gauge.
func (m *Metrics) DecStreamingTorrents() {
	m.StreamingTorrents.Dec()
}

// SetDownloadingTorrents sets the downloading torrents gauge.
func (m *Metrics) SetDownloadingTorrents(count float64) {
	m.DownloadingTorrents.Set(count)
}
