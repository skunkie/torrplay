// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package downloader

import (
	"log/slog"
	"sync"
	"time"

	"github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/metrics"
	"github.com/torrplay/torrplay/internal/utils"
)

const checkInterval = 1 * time.Minute

var gotInfoTimeout = 30 * time.Second

// Downloader is responsible for downloading torrents in the background.
type Downloader struct {
	client          *torrent.Client
	db              database.DatabaseInterface
	downloading     map[metainfo.Hash]struct{}
	fileStoragePath string
	logger          *slog.Logger
	metrics         *metrics.Metrics
	mu              sync.Mutex
	pieceCompletion storage.PieceCompletion
	streamings      map[metainfo.Hash]int
	stop            chan struct{}
	trackers        [][]string
}

// New creates a new Downloader.
func New(client *torrent.Client, db database.DatabaseInterface, logger *slog.Logger, m *metrics.Metrics, pc storage.PieceCompletion, fsp string, trackers [][]string) *Downloader {
	return &Downloader{
		client:          client,
		db:              db,
		downloading:     make(map[metainfo.Hash]struct{}),
		fileStoragePath: fsp,
		logger:          logger,
		metrics:         m,
		pieceCompletion: pc,
		streamings:      make(map[metainfo.Hash]int),
		trackers:        trackers,
	}
}

// AddStreaming increases the count of streaming sessions for a torrent.
func (d *Downloader) AddStreaming(hash metainfo.Hash) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.streamings[hash]++
}

// RemoveStreaming decreases the count of streaming sessions for a torrent.
func (d *Downloader) RemoveStreaming(hash metainfo.Hash) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.streamings[hash]--
	if d.streamings[hash] <= 0 {
		delete(d.streamings, hash)
	}
}

// hasStreamings returns true if there are any active streaming sessions.
func (d *Downloader) hasStreamings() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.streamings) > 0
}

// IsActive returns true if the torrent is currently being downloaded or streamed.
func (d *Downloader) IsActive(hash metainfo.Hash) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, isDownloading := d.downloading[hash]
	return isDownloading || d.streamings[hash] > 0
}

// Start starts the background downloader.
func (d *Downloader) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.stop != nil {
		d.logger.Info("background downloader already running")
		return
	}

	d.logger.Info("starting background downloader")
	stop := make(chan struct{})
	d.stop = stop
	go d.run(stop)
}

// Stop stops the background downloader.
func (d *Downloader) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.stop == nil {
		d.logger.Info("background downloader not running")
		return
	}

	d.logger.Info("stopping background downloader")
	close(d.stop)
	d.stop = nil

	// Pause all torrents that this downloader was managing.
	for hash := range d.downloading {
		if to, ok := d.client.Torrent(hash); ok {
			if to.Info() == nil {
				d.logger.Warn("torrent in downloader has no info on stop", "hash", hash)
				continue
			}
			d.logger.Debug("pausing background download for torrent on stop", "hash", hash)
			for _, f := range to.Files() {
				f.SetPriority(torrent.PiecePriorityNone)
			}
		}
	}

	// Clear the state.
	d.downloading = make(map[metainfo.Hash]struct{})
	d.metrics.SetDownloadingTorrents(0)
}

func (d *Downloader) run(stop <-chan struct{}) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	d.logger.Info("background downloader started")

	// Run once on start
	d.processTorrents()

	for {
		select {
		case <-ticker.C:
			d.processTorrents()
		case <-stop:
			d.logger.Info("background downloader stopped")
			return
		}
	}
}

func (d *Downloader) processTorrents() {
	d.logger.Debug("checking for torrents to download in the background")

	settings, err := d.db.GetSettings()
	if err != nil {
		d.logger.Error("failed to get settings from db", "error", err)
		return
	}

	downloaderEnabled := utils.Val(settings.EnableDownloader)
	isStreaming := d.hasStreamings()

	if !downloaderEnabled {
		d.logger.Debug("background downloader is disabled, stopping all background downloads")
	}
	if isStreaming {
		d.logger.Debug("streaming is active, pausing background downloader")
	}

	allTorrents, err := d.db.GetTorrents()
	if err != nil {
		d.logger.Error("failed to get torrents from database", "error", err)
		return
	}

	var fileTorrents []*database.Torrent
	currentTorrents := make(map[metainfo.Hash]struct{})
	for _, t := range allTorrents {
		if t.Storage != nil && *t.Storage == api.File {
			fileTorrents = append(fileTorrents, t)
			currentTorrents[t.Hash] = struct{}{}
		}
	}

	d.mu.Lock()
	for hash := range d.downloading {
		if _, ok := currentTorrents[hash]; !ok {
			delete(d.downloading, hash)
		}
	}
	d.mu.Unlock()

	for _, t := range fileTorrents {
		to, ok := d.client.Torrent(t.Hash)
		if !ok {
			spec, err := torrent.TorrentSpecFromMagnetUri(t.Magnet)
			if err != nil {
				d.logger.Error("failed to create torrent spec from magnet", "hash", t.Hash, "error", err)
				continue
			}

			if len(t.InfoBytes) > 0 {
				spec.InfoBytes = t.InfoBytes
			}

			if len(d.trackers) > 0 {
				spec.Trackers = d.trackers
			}

			if d.fileStoragePath != "" && d.pieceCompletion != nil {
				opts := storage.NewFileClientOpts{
					ClientBaseDir:   d.fileStoragePath,
					PieceCompletion: d.pieceCompletion,
					UsePartFiles:    generics.Option[bool]{Value: false, Ok: true},
					Logger:          d.logger,
				}
				spec.Storage = storage.NewFileOpts(opts)
			} else {
				d.logger.Warn("file storage path or piece completion not configured, cannot background download", "hash", t.Hash)
				continue
			}

			to, _, err = d.client.AddTorrentSpec(spec)
			if err != nil {
				d.logger.Error("failed to add torrent to client for background download", "hash", t.Hash, "error", err)
				continue
			}
		}

		select {
		case <-to.GotInfo():
		case <-time.After(gotInfoTimeout):
			d.logger.Warn("timeout getting info for torrent", "hash", t.Hash)
			continue
		}

		if to.Length() == 0 {
			continue
		}

		d.mu.Lock()
		_, isDownloading := d.downloading[t.Hash]
		d.mu.Unlock()

		if to.BytesCompleted() == to.Length() {
			if isDownloading {
				// If it was downloading, remove it from our tracking.
				d.mu.Lock()
				delete(d.downloading, t.Hash)
				d.mu.Unlock()
			}
			continue
		}

		shouldDownload := downloaderEnabled && !isStreaming

		if shouldDownload {
			if !isDownloading {
				d.logger.Debug("starting background download for torrent", "hash", t.Hash)
				to.DownloadAll()
				d.mu.Lock()
				d.downloading[t.Hash] = struct{}{}
				d.mu.Unlock()
			}
		} else { // should pause
			if isDownloading {
				d.logger.Debug("pausing background download for torrent", "hash", t.Hash)
				for _, f := range to.Files() {
					f.SetPriority(torrent.PiecePriorityNone)
				}
				d.mu.Lock()
				delete(d.downloading, t.Hash)
				d.mu.Unlock()
			}
		}
	}

	d.mu.Lock()
	downloadingCount := float64(len(d.downloading))
	d.mu.Unlock()
	d.metrics.SetDownloadingTorrents(downloadingCount)
}
