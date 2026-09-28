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

type databaseReader interface {
	GetSettings() (*database.Settings, error)
	GetTorrents() ([]*database.Torrent, error)
}

// Downloader is responsible for downloading torrents in the background.
type Downloader struct {
	client          *torrent.Client
	db              databaseReader
	downloading     map[metainfo.Hash]struct{}
	fileStoragePath string
	logger          *slog.Logger
	metrics         *metrics.Metrics
	mu              sync.Mutex
	pieceCompletion storage.PieceCompletion
	stop            chan struct{}
	// streaming reports whether any file is being streamed. Background
	// downloads pause while it does, so playback keeps the bandwidth.
	streaming func() bool
	trackers  [][]string
	// wake requests a pass before the next interval.
	wake chan struct{}
	// writeFailed holds the torrents whose file storage writes failed. Their
	// background downloads stay stopped until the downloader stops.
	writeFailed map[metainfo.Hash]struct{}
}

// New creates a new Downloader. streaming reports whether any file is being
// streamed, which pauses background downloads; nil means never.
func New(client *torrent.Client, db databaseReader, logger *slog.Logger, m *metrics.Metrics, pc storage.PieceCompletion, fsp string, trackers [][]string, streaming func() bool) *Downloader {
	return &Downloader{
		client:          client,
		db:              db,
		downloading:     make(map[metainfo.Hash]struct{}),
		fileStoragePath: fsp,
		logger:          logger,
		metrics:         m,
		pieceCompletion: pc,
		streaming:       streaming,
		trackers:        trackers,
		wake:            make(chan struct{}, 1),
		writeFailed:     make(map[metainfo.Hash]struct{}),
	}
}

// IsDownloading reports whether the torrent is being downloaded in the
// background.
func (d *Downloader) IsDownloading(hash metainfo.Hash) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, isDownloading := d.downloading[hash]
	return isDownloading
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

	// Clear the state. A restart tries torrents whose writes failed again.
	d.downloading = make(map[metainfo.Hash]struct{})
	clear(d.writeFailed)
	d.metrics.SetDownloadingTorrents(0)
}

// Wake makes the downloader run a pass at once instead of at its next
// interval, such as when streaming starts or stops. It never blocks.
func (d *Downloader) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
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
		case <-d.wake:
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
	isStreaming := d.streaming != nil && d.streaming()

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
			d.WatchStorageWrites(to)
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
		_, writeFailed := d.writeFailed[t.Hash]
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

		shouldDownload := downloaderEnabled && !isStreaming && !writeFailed

		if shouldDownload {
			if !isDownloading {
				d.logger.Debug("starting background download for torrent", "hash", t.Hash)
				// Download through file priorities, which pausing sets back to
				// none. Piece priorities belong to the stream pool's claims,
				// and a piece's effective priority is the highest of the two,
				// so raising piece priorities here could not be undone.
				for _, f := range to.Files() {
					f.SetPriority(torrent.PiecePriorityNormal)
				}
				// A failed storage write stops the torrent's downloads
				// until they are allowed again.
				to.AllowDataDownload()
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

// WatchStorageWrites stops downloading a file-storage torrent when writing its
// data fails, such as on a full disk, instead of requesting the data again
// forever. The torrent's background download stays stopped until the
// downloader stops, and a stream of the torrent allows its downloads again.
func (d *Downloader) WatchStorageWrites(to *torrent.Torrent) {
	to.SetOnWriteChunkError(func(err error) { d.storageWriteFailed(to, err) })
}

// storageWriteFailed stops downloading to after a failed storage write and
// stops its background download.
func (d *Downloader) storageWriteFailed(to *torrent.Torrent, err error) {
	to.DisallowDataDownload()
	hash := to.InfoHash()
	d.mu.Lock()
	_, alreadyFailed := d.writeFailed[hash]
	d.writeFailed[hash] = struct{}{}
	_, wasDownloading := d.downloading[hash]
	delete(d.downloading, hash)
	downloadingCount := float64(len(d.downloading))
	d.mu.Unlock()
	if wasDownloading {
		for _, f := range to.Files() {
			f.SetPriority(torrent.PiecePriorityNone)
		}
		d.metrics.SetDownloadingTorrents(downloadingCount)
	}
	if !alreadyFailed {
		d.logger.Error("stopped downloading torrent after a storage write failed", "hash", hash, "error", err)
	}
}
