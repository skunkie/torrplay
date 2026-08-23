// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package downloader

import (
	"bytes"
	"crypto/rand"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/metrics"
	tputil "github.com/torrplay/torrplay/internal/testutil"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestMain(m *testing.M) {
	tputil.VerifyTestMain(m)
}

// MockDB is a mock implementation of the DatabaseInterface for testing.
type MockDB struct {
	database.DatabaseInterface
	err      error
	settings *database.Settings
	torrents []*database.Torrent
}

func (m *MockDB) GetSettings() (*database.Settings, error) {
	return m.settings, m.err
}

func (m *MockDB) GetTorrents() ([]*database.Torrent, error) {
	return m.torrents, m.err
}

func newTestTorrent(t *testing.T, name string, totalSize int64) *metainfo.MetaInfo {
	t.Helper()
	pieceLength := int64(16 * 1024)
	numPieces := (totalSize + pieceLength - 1) / pieceLength
	info := metainfo.Info{
		Name:        name,
		Length:      totalSize,
		PieceLength: pieceLength,
		Pieces:      make([]byte, 20*numPieces),
	}
	// Use random data for piece hashes to make it a valid torrent structure.
	_, err := rand.Read(info.Pieces)
	require.NoError(t, err)
	mi := &metainfo.MetaInfo{
		InfoBytes: mustEncodeInfo(t, &info),
	}
	return mi
}

func mustEncodeInfo(t *testing.T, info *metainfo.Info) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := bencode.NewEncoder(&buf).Encode(info)
	require.NoError(t, err)
	return buf.Bytes()
}

func newTestTorrentClient(t *testing.T, dataDir string) *torrent.Client {
	t.Helper()
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.NoDHT = true
	cfg.DisablePEX = true
	cfg.DisableTrackers = true
	cfg.DisableWebtorrent = true
	cfg.DisableWebseeds = true
	cfg.NoDefaultPortForwarding = true
	cfg.DisableUTP = true
	cfg.DisableTCP = true
	cfg.ListenPort = 0
	c, err := torrent.NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDownloader_ProcessTorrents_Metrics(t *testing.T) {
	testMetaInfo := newTestTorrent(t, "test-torrent", 1024)
	testHash := testMetaInfo.HashInfoBytes()
	storageType := api.File
	testApiTorrent := &database.Torrent{
		Torrent: api.Torrent{
			Hash:    testHash,
			Magnet:  testMetaInfo.Magnet(nil, nil).String(),
			Storage: &storageType,
		},
	}

	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()

	db := &MockDB{
		settings: &database.Settings{Settings: api.Settings{EnableDownloader: utils.Ptr(true)}},
		torrents: []*database.Torrent{testApiTorrent},
	}
	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Use a real torrent client, but configured to not touch the network.
	client := newTestTorrentClient(t, td)

	// Create the downloader instance.
	downloader := New(client, db, logger, m, pc, td, nil)
	originalGotInfoTimeout := gotInfoTimeout
	gotInfoTimeout = 1 * time.Millisecond
	defer func() {
		gotInfoTimeout = originalGotInfoTimeout
	}()

	downloader.processTorrents()

	// Check that the metric was updated to 0, since we have one torrent that will fail to get info.
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.DownloadingTorrents) == 0
	}, time.Second, 10*time.Millisecond, "DownloadingTorrents metric should be 0")

	// Verify that if we run it again, it's still 0 (not incremented).
	downloader.processTorrents()
	assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents), "DownloadingTorrents metric should remain 0")

	// Now, let's simulate the torrent completing by having the DB return no torrents.
	db.torrents = []*database.Torrent{}
	downloader.processTorrents()
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.DownloadingTorrents) == 0
	}, time.Second, 10*time.Millisecond, "DownloadingTorrents metric should be 0 after torrent is removed")
}

func TestDownloader_AddAndRemoveStreaming(t *testing.T) {
	d := &Downloader{
		streamings: make(map[metainfo.Hash]int),
	}

	hash := newTestTorrent(t, "test-torrent", 1).HashInfoBytes()

	d.AddStreaming(hash)
	assert.Equal(t, 1, d.streamings[hash])

	d.AddStreaming(hash)
	assert.Equal(t, 2, d.streamings[hash])

	d.RemoveStreaming(hash)
	assert.Equal(t, 1, d.streamings[hash])

	d.RemoveStreaming(hash)
	_, exists := d.streamings[hash]
	assert.False(t, exists)
}

func TestDownloader_hasStreamings(t *testing.T) {
	d := &Downloader{
		streamings: make(map[metainfo.Hash]int),
	}

	assert.False(t, d.hasStreamings())

	hash := newTestTorrent(t, "test-torrent", 1).HashInfoBytes()
	d.AddStreaming(hash)
	assert.True(t, d.hasStreamings())

	d.RemoveStreaming(hash)
	assert.False(t, d.hasStreamings())
}

func TestDownloader_Stop(t *testing.T) {
	testMetaInfo := newTestTorrent(t, "test-torrent", 1024)
	testHash := testMetaInfo.HashInfoBytes()

	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()

	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	client := newTestTorrentClient(t, td)

	db := &MockDB{
		settings: &database.Settings{Settings: api.Settings{EnableDownloader: utils.Ptr(true)}},
	}
	downloader := New(client, db, logger, m, pc, td, nil)
	downloader.downloading[testHash] = struct{}{}

	to, err := client.AddTorrent(testMetaInfo)
	require.NoError(t, err)
	to.DownloadAll()

	downloader.Start()
	downloader.Stop()

	for _, f := range to.Files() {
		assert.Equal(t, torrent.PiecePriorityNone, f.Priority())
	}
	assert.Empty(t, downloader.downloading)
	assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
}

func TestDownloader_ProcessTorrents_DownloaderDisabled(t *testing.T) {
	testMetaInfo := newTestTorrent(t, "test-torrent", 1024)
	testHash := testMetaInfo.HashInfoBytes()
	storageType := api.File
	testApiTorrent := &database.Torrent{
		Torrent: api.Torrent{
			Hash:    testHash,
			Magnet:  testMetaInfo.Magnet(nil, nil).String(),
			Storage: &storageType,
		},
	}

	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()

	db := &MockDB{
		settings: &database.Settings{Settings: api.Settings{EnableDownloader: utils.Ptr(false)}},
		torrents: []*database.Torrent{testApiTorrent},
	}
	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	client := newTestTorrentClient(t, td)

	downloader := New(client, db, logger, m, pc, td, nil)
	downloader.downloading[testHash] = struct{}{}

	to, err := client.AddTorrent(testMetaInfo)
	require.NoError(t, err)
	to.DownloadAll()

	downloader.processTorrents()

	for _, f := range to.Files() {
		assert.Equal(t, torrent.PiecePriorityNone, f.Priority())
	}
	assert.Empty(t, downloader.downloading)
	assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
}

func TestDownloader_ProcessTorrents_WithStreaming(t *testing.T) {
	testMetaInfo := newTestTorrent(t, "test-torrent", 1024)
	testHash := testMetaInfo.HashInfoBytes()
	storageType := api.File
	testApiTorrent := &database.Torrent{
		Torrent: api.Torrent{
			Hash:    testHash,
			Magnet:  testMetaInfo.Magnet(nil, nil).String(),
			Storage: &storageType,
		},
	}

	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()

	db := &MockDB{
		settings: &database.Settings{Settings: api.Settings{EnableDownloader: utils.Ptr(true)}},
		torrents: []*database.Torrent{testApiTorrent},
	}
	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	client := newTestTorrentClient(t, td)

	downloader := New(client, db, logger, m, pc, td, nil)
	downloader.downloading[testHash] = struct{}{}
	downloader.AddStreaming(testHash)

	to, err := client.AddTorrent(testMetaInfo)
	require.NoError(t, err)
	to.DownloadAll()

	downloader.processTorrents()

	for _, f := range to.Files() {
		assert.Equal(t, torrent.PiecePriorityNone, f.Priority())
	}
	assert.Empty(t, downloader.downloading)
	assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
}

func TestDownloader_StartStop_CleanExit(t *testing.T) {
	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()

	db := &MockDB{
		settings: &database.Settings{Settings: api.Settings{EnableDownloader: utils.Ptr(false)}},
	}
	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	client := newTestTorrentClient(t, td)

	downloader := New(client, db, logger, m, pc, td, nil)

	// Start and stop multiple times in quick succession
	for i := 0; i < 5; i++ {
		downloader.Start()
		downloader.Stop()
	}
}
