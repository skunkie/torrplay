// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
	"github.com/torrplay/torrplay/pkg/stream"
)

func TestEngineStatsKeepCountersAcrossClientReconfiguration(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	info := &metainfo.Info{Name: "counters", PieceLength: 256, Length: 256, Pieces: make([]byte, 20)}
	infoHash := metainfo.Hash{1}
	readMissingPiece := func() {
		t.Helper()
		impl, err := ctrl.storageClient.Load().OpenTorrent(t.Context(), info, infoHash)
		require.NoError(t, err)
		defer func() { require.NoError(t, impl.Close()) }()
		_, err = impl.Piece(info.Piece(0)).ReadAt(make([]byte, 16), 0)
		require.ErrorIs(t, err, memstorage.ErrPieceNotAvailable)
	}

	readMissingPiece()
	require.Equal(t, int64(1), ctrl.engineStats().Storage.ReadMisses)

	// Reconfiguration replaces the storage, whose own counters start over.
	require.NoError(t, ctrl.configureTorrentClient())
	readMissingPiece()
	stats := ctrl.engineStats()
	assert.Equal(t, int64(2), stats.Storage.ReadMisses, "counters of the replaced storage must be kept")
	assert.Equal(t, *ctrl.settings.Load().MaxMemory, stats.MemoryLimitBytes)

	rr := httptest.NewRecorder()
	ctrl.metrics.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rr.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `torrplay_storage_read_failures_total{reason="evicted"} 2`)
	assert.Contains(t, string(body), "torrplay_torrent_banned_peers 0")
}

func TestEngineStatsCountTorrentsWithStreamReaders(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	assert.Zero(t, ctrl.engineStats().StreamingTorrents)

	series := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	movie := addSyntheticTorrent(t, ctrl, 1<<30+1, 1<<20)
	pool := ctrl.streamPool.Load()
	releases := make([]func(), 0, 3)
	for _, file := range []*torrent.File{series.Files()[0], series.Files()[0], movie.Files()[0]} {
		_, release, err := pool.Acquire(context.Background(), file, stream.MemoryStorage)
		require.NoError(t, err)
		releases = append(releases, release)
	}
	assert.Equal(t, 2, ctrl.engineStats().StreamingTorrents, "each torrent counts once")
	assert.True(t, ctrl.isStreaming())

	for _, release := range releases {
		release()
	}
	assert.True(t, ctrl.isStreaming(), "a lingering reader still pauses background downloads")
	pool.Close()
	assert.Zero(t, ctrl.engineStats().StreamingTorrents)
	assert.False(t, ctrl.isStreaming())
}

func TestEngineStatsSplitLoadedTorrentsByReason(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()
	stats := ctrl.engineStats()
	require.Zero(t, stats.LoadedTorrentsBackground)
	require.Zero(t, stats.LoadedTorrentsOnDemand)

	spec := func(name string) *torrent.TorrentSpec {
		infoBytes, err := bencode.Marshal(metainfo.Info{Name: name, PieceLength: 256, Length: 256, Pieces: make([]byte, 20)})
		require.NoError(t, err)
		return &torrent.TorrentSpec{AddTorrentOpts: torrent.AddTorrentOpts{InfoHash: metainfo.HashBytes(infoBytes), InfoBytes: infoBytes}}
	}
	// Requests load torrents through loadTorrentSpec, which tracks them for expiry.
	_, err := ctrl.loadTorrentSpec(spec("requested.mkv"), api.Memory)
	require.NoError(t, err)
	// The background downloader adds torrents to the client directly.
	_, _, err = ctrl.currentClient().AddTorrentSpec(spec("downloaded.mkv"))
	require.NoError(t, err)

	stats = ctrl.engineStats()
	assert.Equal(t, 1, stats.LoadedTorrentsOnDemand)
	assert.Equal(t, 1, stats.LoadedTorrentsBackground)
}
