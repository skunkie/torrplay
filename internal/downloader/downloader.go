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
	"github.com/anacrolix/torrent/storage"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/metrics"
)

const checkInterval = 1 * time.Minute

var gotInfoTimeout = 30 * time.Second

// Downloader is responsible for downloading torrents in the background.
type Downloader struct {
	client          *torrent.Client
	db              database.DatabaseInterface
	fileStoragePath string
	logger          *slog.Logger
	metrics         *metrics.Metrics
	mu              sync.Mutex
	pieceCompletion storage.PieceCompletion
	stop            chan struct{}
	trackers        [][]string
}

// New creates a new Downloader.
func New(client *torrent.Client, db database.DatabaseInterface, logger *slog.Logger, m *metrics.Metrics, pc storage.PieceCompletion, fsp string, trackers [][]string) *Downloader {
	return &Downloader{
		client:          client,
		db:              db,
		fileStoragePath: fsp,
		logger:          logger,
		metrics:         m,
		pieceCompletion: pc,
		trackers:        trackers,
	}
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
	d.stop = make(chan struct{})
	go d.run()
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
}

func (d *Downloader) run() {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	d.logger.Info("background downloader started")

	// Run once on start
	d.processTorrents()

	for {
		select {
		case <-ticker.C:
			d.processTorrents()
		case <-d.stop:
			d.logger.Info("background downloader stopped")
			return
		}
	}
}

func (d *Downloader) processTorrents() {
	d.logger.Debug("checking for torrents to download in the background")

	torrents, err := d.db.GetTorrents()
	if err != nil {
		d.logger.Error("failed to get torrents from database", "error", err)
		return
	}

	var downloadingTorrents float64
	for _, t := range torrents {
		// Only process torrents with file storage.
		if t.Storage == nil || *t.Storage != api.File {
			continue
		}

		to, ok := d.client.Torrent(t.Hash)
		if !ok {
			spec, err := torrent.TorrentSpecFromMagnetUri(t.Magnet)
			if err != nil {
				d.logger.Error("failed to create torrent spec from magnet", "hash", t.Hash, "error", err)
				continue
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

		// If torrent is complete, we don't need to do anything.
		if to.BytesCompleted() == to.Length() {
			d.logger.Debug("torrent is already complete", "hash", t.Hash)
			continue
		}

		downloadingTorrents++
		d.logger.Debug("starting background download for torrent", "hash", t.Hash)
		to.DownloadAll()
	}
	d.metrics.SetDownloadingTorrents(downloadingTorrents)
}
