// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/metrics"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
)

// engineTotals holds the final counters of torrent clients and memory storage
// replaced by reconfiguration, so exported counters never go backwards.
// Guarded by Controller.mu.
type engineTotals struct {
	bytesDownloaded  int64
	bytesUploaded    int64
	piecesHashedBad  int64
	piecesHashedGood int64
	storage          memstorage.Counters
}

// engineTotalsOf returns the cumulative counters of a torrent client and its
// memory storage. Either may be nil.
func engineTotalsOf(client *torrent.Client, storageClient *memstorage.Client) engineTotals {
	var totals engineTotals
	if client != nil {
		stats := client.Stats()
		totals.bytesDownloaded = stats.BytesReadUsefulData.Int64()
		totals.bytesUploaded = stats.BytesWrittenData.Int64()
		totals.piecesHashedBad = stats.PiecesDirtiedBad.Int64()
		totals.piecesHashedGood = stats.PiecesDirtiedGood.Int64()
	}
	if storageClient != nil {
		totals.storage = storageClient.Counters()
	}
	return totals
}

func (t *engineTotals) add(other engineTotals) {
	t.bytesDownloaded += other.bytesDownloaded
	t.bytesUploaded += other.bytesUploaded
	t.piecesHashedBad += other.piecesHashedBad
	t.piecesHashedGood += other.piecesHashedGood
	t.storage = t.storage.Add(other.storage)
}

// engineStats reports the streaming engine for the metrics endpoint. The
// retired totals and the current components are read together under c.mu, so
// a concurrent reconfiguration cannot count a replaced client twice.
func (c *Controller) engineStats() metrics.EngineStats {
	c.mu.RLock()
	totals := c.engineTotals
	client := c.client
	storageClient := c.storageClient.Load()
	c.mu.RUnlock()

	current := engineTotalsOf(client, storageClient)
	totals.add(current)
	stats := metrics.EngineStats{
		BytesDownloaded:  totals.bytesDownloaded,
		BytesUploaded:    totals.bytesUploaded,
		PiecesHashedBad:  totals.piecesHashedBad,
		PiecesHashedGood: totals.piecesHashedGood,
		Storage:          totals.storage,
	}
	if client != nil {
		clientStats := client.Stats()
		stats.BannedPeers = len(client.BadPeerIPs())
		stats.LoadedTorrentsBackground, stats.LoadedTorrentsOnDemand = c.loadedTorrentCounts(client)
		stats.PeersActive = clientStats.ActivePeers
		stats.PeersHalfOpen = clientStats.HalfOpenPeers
		stats.PeersPending = clientStats.PendingPeers
	}
	if storageClient != nil {
		memory := storageClient.MemoryStats()
		stats.MemoryLimitBytes = memory.LimitBytes
		stats.MemoryUsedBytes = memory.UsedBytes
	}
	stats.StreamingTorrents = c.streamingTorrentCount()
	return stats
}

// loadedTorrentCounts splits the torrents loaded in client by why they are
// loaded. Requests load torrents through loadTorrentSpec, which records them
// for idle expiry; the background downloader adds torrents directly.
func (c *Controller) loadedTorrentCounts(client *torrent.Client) (background, onDemand int) {
	torrents := client.Torrents()
	c.torrentTracker.mu.RLock()
	defer c.torrentTracker.mu.RUnlock()
	for _, to := range torrents {
		if _, tracked := c.torrentTracker.torrents[to.InfoHash()]; tracked {
			onDemand++
		} else {
			background++
		}
	}
	return background, onDemand
}

// streamingTorrentCount returns the number of torrents with an open playback
// session, counting a torrent once however many of its files are playing.
func (c *Controller) streamingTorrentCount() int {
	c.preloadsMu.Lock()
	defer c.preloadsMu.Unlock()
	torrents := make(map[metainfo.Hash]struct{}, len(c.playbackSessions))
	for key := range c.playbackSessions {
		torrents[key.infoHash] = struct{}{}
	}
	return len(torrents)
}
