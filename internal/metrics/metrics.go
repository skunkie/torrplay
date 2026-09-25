// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metrics

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/torrplay/torrplay/pkg/storage"
)

// Metrics holds the Prometheus metrics.
type Metrics struct {
	DownloadingTorrents    prometheus.Gauge
	StreamRequestsInFlight prometheus.Gauge
	reg                    *prometheus.Registry
	engine                 *engineCollector
	HTTPRequestsTotal      *prometheus.CounterVec
	HTTPRequestDuration    *prometheus.HistogramVec
	HTTPRequestSizeBytes   *prometheus.SummaryVec
	HTTPResponseSizeBytes  *prometheus.SummaryVec
	StreamReadDuration     *prometheus.HistogramVec
}

// EngineStats is a snapshot of the streaming engine, read on every scrape.
// Counters must be cumulative for the life of the process, including torrent
// clients and storage that have since been replaced.
type EngineStats struct {
	// BannedPeers is the number of peer IPs the torrent client has banned.
	BannedPeers int
	// BytesDownloaded is the useful piece data received from peers.
	BytesDownloaded int64
	// BytesUploaded is the piece data sent to peers.
	BytesUploaded int64
	// LoadedTorrentsBackground is the number of loaded torrents the
	// background downloader keeps loaded. They do not expire.
	LoadedTorrentsBackground int
	// LoadedTorrentsOnDemand is the number of loaded torrents loaded by
	// requests. They are dropped once unused for the idle lifetime.
	LoadedTorrentsOnDemand int
	// MemoryLimitBytes is the memory storage limit.
	MemoryLimitBytes int64
	// MemoryUsedBytes is the memory reserved for piece data.
	MemoryUsedBytes int64
	// PeersActive is the number of connected peers.
	PeersActive int
	// PeersHalfOpen is the number of peer connections being established.
	PeersHalfOpen int
	// PeersPending is the number of known peers not yet connected.
	PeersPending int
	// PiecesHashedBad is the number of downloaded pieces that failed their
	// hash with data from peers.
	PiecesHashedBad int64
	// PiecesHashedGood is the number of downloaded pieces that passed their
	// hash.
	PiecesHashedGood int64
	// Storage holds the memory storage event counters.
	Storage storage.Counters
	// StreamingTorrents is the number of torrents with an open playback
	// session. A session spans a player's separate range requests.
	StreamingTorrents int
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
		StreamRequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "torrplay_stream_requests_in_flight",
			Help: "Number of streaming HTTP requests currently being served.",
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
		StreamReadDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "torrplay_stream_read_duration_seconds",
				Help:    "Duration of playback stream reads, including waits for torrent data. Slow reads are stalls.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{"storage"},
		),
		engine: newEngineCollector(),
		reg:    reg,
	}

	reg.MustRegister(m.DownloadingTorrents)
	reg.MustRegister(m.StreamRequestsInFlight)
	reg.MustRegister(m.HTTPRequestsTotal)
	reg.MustRegister(m.HTTPRequestDuration)
	reg.MustRegister(m.HTTPRequestSizeBytes)
	reg.MustRegister(m.HTTPResponseSizeBytes)
	reg.MustRegister(m.StreamReadDuration)
	reg.MustRegister(m.engine)

	return m
}

// Handler returns an HTTP handler for the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// IncStreamRequests records the start of a streaming HTTP request.
func (m *Metrics) IncStreamRequests() {
	m.StreamRequestsInFlight.Inc()
}

// DecStreamRequests records the end of a streaming HTTP request.
func (m *Metrics) DecStreamRequests() {
	m.StreamRequestsInFlight.Dec()
}

// SetDownloadingTorrents sets the downloading torrents gauge.
func (m *Metrics) SetDownloadingTorrents(count float64) {
	m.DownloadingTorrents.Set(count)
}

// SetEngineStatsSource sets the function that reports streaming engine stats
// on each scrape. Engine metrics are omitted until it is set.
func (m *Metrics) SetEngineStatsSource(source func() EngineStats) {
	m.engine.source.Store(&source)
}

// StreamReadObserver returns a function that records how long playback stream
// reads took for a storage mode such as "memory" or "file". The series is
// resolved once, keeping the per-read cost to the observation itself.
func (m *Metrics) StreamReadObserver(storageMode string) func(time.Duration) {
	observer := m.StreamReadDuration.WithLabelValues(storageMode)
	return func(duration time.Duration) {
		observer.Observe(duration.Seconds())
	}
}

// engineCollector exports EngineStats as metrics read at scrape time, so the
// engine keeps its own counters and needs no Prometheus dependency.
type engineCollector struct {
	source atomic.Pointer[func() EngineStats]

	bannedPeers            *prometheus.Desc
	completionMisses       *prometheus.Desc
	dataBytes              *prometheus.Desc
	evictedIncompleteBytes *prometheus.Desc
	evictedPieces          *prometheus.Desc
	incompleteHashes       *prometheus.Desc
	loadedTorrents         *prometheus.Desc
	memoryLimitBytes       *prometheus.Desc
	memoryUsedBytes        *prometheus.Desc
	peers                  *prometheus.Desc
	piecesHashed           *prometheus.Desc
	protectedEvictions     *prometheus.Desc
	storageReadFailures    *prometheus.Desc
	streamingTorrents      *prometheus.Desc
}

func newEngineCollector() *engineCollector {
	return &engineCollector{
		bannedPeers: prometheus.NewDesc("torrplay_torrent_banned_peers",
			"Number of peer IPs banned by the torrent client for sending bad data.", nil, nil),
		completionMisses: prometheus.NewDesc("torrplay_storage_completion_misses_total",
			"Pieces already evicted when the torrent client marked them complete.", nil, nil),
		dataBytes: prometheus.NewDesc("torrplay_torrent_data_bytes_total",
			"Piece data exchanged with peers and web seeds: useful data received for download, excluding duplicates, and data sent for upload.", []string{"direction"}, nil),
		evictedIncompleteBytes: prometheus.NewDesc("torrplay_storage_evicted_incomplete_bytes_total",
			"Bytes already downloaded into incomplete pieces when they were evicted, which must be downloaded again.", nil, nil),
		evictedPieces: prometheus.NewDesc("torrplay_storage_evicted_pieces_total",
			"Pieces evicted from memory storage, either complete or incomplete. Incomplete pieces, including fully downloaded ones awaiting their hash check, must be downloaded again.", []string{"state"}, nil),
		incompleteHashes: prometheus.NewDesc("torrplay_storage_incomplete_hashes_total",
			"Piece hashes refused because chunks were lost to eviction.", nil, nil),
		loadedTorrents: prometheus.NewDesc("torrplay_torrents_loaded",
			"Number of torrents loaded in the torrent client, by reason: on_demand torrents were loaded by requests and are dropped once unused, background torrents are kept loaded by the background downloader.", []string{"reason"}, nil),
		memoryLimitBytes: prometheus.NewDesc("torrplay_storage_memory_limit_bytes",
			"Memory storage limit.", nil, nil),
		memoryUsedBytes: prometheus.NewDesc("torrplay_storage_memory_used_bytes",
			"Memory reserved for piece data.", nil, nil),
		peers: prometheus.NewDesc("torrplay_torrent_peers",
			"Peer connections and addresses summed across loaded torrents: active and half_open are established and connecting connections, pending are known addresses not yet connected. A peer shared by several torrents counts once per torrent.", []string{"state"}, nil),
		piecesHashed: prometheus.NewDesc("torrplay_torrent_pieces_hashed_total",
			"Pieces downloaded from peers and checked against their hash, counted once per piece. Bad results are bad peer data; failures caused by storage are counted by torrplay_storage_incomplete_hashes_total.", []string{"result"}, nil),
		protectedEvictions: prometheus.NewDesc("torrplay_storage_protected_evictions_total",
			"Pieces evicted despite protection, as a last resort under memory pressure.", []string{"protection"}, nil),
		storageReadFailures: prometheus.NewDesc("torrplay_storage_read_failures_total",
			"Piece reads memory storage could not serve, for playback and for uploads to peers.", []string{"reason"}, nil),
		streamingTorrents: prometheus.NewDesc("torrplay_streaming_torrents",
			"Number of torrents currently being streamed, counting each torrent with an open playback session once.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *engineCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{
		c.bannedPeers, c.completionMisses, c.dataBytes, c.evictedIncompleteBytes, c.evictedPieces,
		c.incompleteHashes, c.loadedTorrents, c.memoryLimitBytes, c.memoryUsedBytes, c.peers,
		c.piecesHashed, c.protectedEvictions, c.storageReadFailures, c.streamingTorrents,
	} {
		ch <- desc
	}
}

// Collect implements prometheus.Collector.
func (c *engineCollector) Collect(ch chan<- prometheus.Metric) {
	source := c.source.Load()
	if source == nil {
		return
	}
	stats := (*source)()
	counter := func(desc *prometheus.Desc, value int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(value), labels...)
	}
	gauge := func(desc *prometheus.Desc, value int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(value), labels...)
	}

	gauge(c.bannedPeers, int64(stats.BannedPeers))
	counter(c.dataBytes, stats.BytesDownloaded, "download")
	counter(c.dataBytes, stats.BytesUploaded, "upload")
	gauge(c.loadedTorrents, int64(stats.LoadedTorrentsBackground), "background")
	gauge(c.loadedTorrents, int64(stats.LoadedTorrentsOnDemand), "on_demand")
	gauge(c.peers, int64(stats.PeersActive), "active")
	gauge(c.peers, int64(stats.PeersHalfOpen), "half_open")
	gauge(c.peers, int64(stats.PeersPending), "pending")
	gauge(c.streamingTorrents, int64(stats.StreamingTorrents))
	counter(c.piecesHashed, stats.PiecesHashedGood, "good")
	counter(c.piecesHashed, stats.PiecesHashedBad, "bad")

	gauge(c.memoryLimitBytes, stats.MemoryLimitBytes)
	gauge(c.memoryUsedBytes, stats.MemoryUsedBytes)
	counters := stats.Storage
	counter(c.completionMisses, counters.CompletionMisses)
	counter(c.evictedIncompleteBytes, counters.EvictedIncompleteBytes)
	counter(c.evictedPieces, counters.EvictedCompletePieces, "complete")
	counter(c.evictedPieces, counters.EvictedIncompletePieces, "incomplete")
	counter(c.incompleteHashes, counters.IncompleteHashes)
	counter(c.protectedEvictions, counters.BoundaryEvictions, "boundary")
	counter(c.protectedEvictions, counters.ActiveRangeEvictions, "active_range")
	counter(c.storageReadFailures, counters.ReadMisses, "evicted")
	counter(c.storageReadFailures, counters.IncompleteReads, "incomplete")
}
