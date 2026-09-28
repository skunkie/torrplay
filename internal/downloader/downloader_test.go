// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package downloader

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"path/filepath"
	"sync/atomic"
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

// MockDB provides the database reads used by Downloader in tests.
type MockDB struct {
	err      error
	settings *database.Settings
	// settingsReads counts GetSettings calls, one per processing pass.
	settingsReads atomic.Int32
	torrents      []*database.Torrent
}

func (m *MockDB) GetSettings() (*database.Settings, error) {
	m.settingsReads.Add(1)
	return m.settings, m.err
}

func (m *MockDB) GetTorrents() ([]*database.Torrent, error) {
	return m.torrents, m.err
}

func newTestTorrent(t *testing.T, totalSize int64) *metainfo.MetaInfo {
	t.Helper()
	pieceLength := int64(16 * 1024)
	numPieces := (totalSize + pieceLength - 1) / pieceLength
	info := metainfo.Info{
		Name:        "test-torrent",
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

func TestDownloader_ProcessTorrents(t *testing.T) {
	t.Run("updates downloading metric", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		testApiTorrent := &database.Torrent{
			Torrent: api.Torrent{
				Hash:    testHash,
				Magnet:  utils.MagnetURIFromHash(testHash),
				Storage: &storageType,
			},
		}

		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()

		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
			torrents: []*database.Torrent{testApiTorrent},
		}
		m := metrics.New()
		logger := slog.New(slog.DiscardHandler)

		// Use a real torrent client, but configured to not touch the network.
		client := newTestTorrentClient(t, td)

		// Create the downloader instance.
		downloader := New(client, db, logger, m, pc, td, nil, Hooks{})
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
	})

	t.Run("stops a download it started once streaming begins", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()
		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
			torrents: []*database.Torrent{{Torrent: api.Torrent{Hash: testHash, Magnet: utils.MagnetURIFromHash(testHash), Storage: &storageType}}},
		}
		client := newTestTorrentClient(t, td)
		streaming := false
		downloader := New(client, db, slog.New(slog.DiscardHandler), metrics.New(), pc, td, nil, Hooks{Streaming: func() bool { return streaming }})
		to, err := client.AddTorrent(testMetaInfo)
		require.NoError(t, err)
		verifyData(t, to)

		downloader.processTorrents()
		require.True(t, downloader.IsDownloading(testHash))
		for index := range to.NumPieces() {
			require.Equal(t, torrent.PiecePriorityNormal, to.PieceState(index).Priority, "piece %d must be wanted", index)
		}

		streaming = true
		downloader.processTorrents()
		assert.False(t, downloader.IsDownloading(testHash))
		assertNothingWanted(t, to)
	})

	t.Run("waits for metadata through its waiter", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()
		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
			torrents: []*database.Torrent{{Torrent: api.Torrent{Hash: testHash, Magnet: utils.MagnetURIFromHash(testHash), Storage: &storageType}}},
		}
		client := newTestTorrentClient(t, td)
		var waited []metainfo.Hash
		waitForInfo := func(to *torrent.Torrent) error {
			waited = append(waited, to.InfoHash())
			return errors.New("no metadata")
		}
		downloader := New(client, db, slog.New(slog.DiscardHandler), metrics.New(), pc, td, nil, Hooks{WaitForInfo: waitForInfo})
		_, err = client.AddTorrent(testMetaInfo)
		require.NoError(t, err)

		downloader.processTorrents()
		assert.Equal(t, []metainfo.Hash{testHash}, waited, "the caller must count the wait")
		assert.False(t, downloader.IsDownloading(testHash), "a torrent without metadata must not start")
	})

	t.Run("leaves a torrent held in memory storage alone", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()
		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
			torrents: []*database.Torrent{{Torrent: api.Torrent{Hash: testHash, Magnet: utils.MagnetURIFromHash(testHash), Storage: &storageType}}},
		}
		client := newTestTorrentClient(t, td)
		inMemory := func(hash metainfo.Hash) bool { return hash == testHash }
		downloader := New(client, db, slog.New(slog.DiscardHandler), metrics.New(), pc, td, nil, Hooks{InMemoryStorage: inMemory})
		to, err := client.AddTorrent(testMetaInfo)
		require.NoError(t, err)
		verifyData(t, to)

		downloader.processTorrents()
		assert.False(t, downloader.IsDownloading(testHash), "a background download would only churn memory storage")
		assertNothingWanted(t, to)
	})

	t.Run("stops a download whose storage write failed", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()
		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
			torrents: []*database.Torrent{{Torrent: api.Torrent{Hash: testHash, Magnet: utils.MagnetURIFromHash(testHash), Storage: &storageType}}},
		}
		client := newTestTorrentClient(t, td)
		m := metrics.New()
		downloader := New(client, db, slog.New(slog.DiscardHandler), m, pc, td, nil, Hooks{})
		to, err := client.AddTorrent(testMetaInfo)
		require.NoError(t, err)
		verifyData(t, to)
		downloader.processTorrents()
		require.True(t, downloader.IsDownloading(testHash))

		downloader.storageWriteFailed(to, errors.New("no space left on device"))
		assert.False(t, downloader.IsDownloading(testHash))
		assertNothingWanted(t, to)
		assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
		assert.ErrorContains(t, readFirstByte(t, to), "downloading disabled", "the torrent must stop downloading")

		downloader.processTorrents()
		assert.False(t, downloader.IsDownloading(testHash), "a later pass must not resume the download")

		// Starting the downloader, here for the first time, tries the torrent
		// again.
		downloader.Start()
		defer downloader.Stop()
		require.Eventually(t, func() bool { return downloader.IsDownloading(testHash) }, time.Second, time.Millisecond)
		assert.ErrorIs(t, readFirstByte(t, to), context.DeadlineExceeded, "the torrent must download again")
	})

	t.Run("clears priorities when disabled", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		testApiTorrent := &database.Torrent{
			Torrent: api.Torrent{
				Hash:    testHash,
				Magnet:  utils.MagnetURIFromHash(testHash),
				Storage: &storageType,
			},
		}

		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()

		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(false)}},
			torrents: []*database.Torrent{testApiTorrent},
		}

		m := metrics.New()
		logger := slog.New(slog.DiscardHandler)

		client := newTestTorrentClient(t, td)

		downloader := New(client, db, logger, m, pc, td, nil, Hooks{})
		downloader.downloading[testHash] = struct{}{}

		to, err := client.AddTorrent(testMetaInfo)
		require.NoError(t, err)
		for _, f := range to.Files() {
			f.SetPriority(torrent.PiecePriorityNormal)
		}

		downloader.processTorrents()

		assertNothingWanted(t, to)
		assert.Empty(t, downloader.downloading)
		assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
	})

	t.Run("clears priorities while streaming", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()
		storageType := api.File
		testApiTorrent := &database.Torrent{
			Torrent: api.Torrent{
				Hash:    testHash,
				Magnet:  utils.MagnetURIFromHash(testHash),
				Storage: &storageType,
			},
		}

		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()

		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
			torrents: []*database.Torrent{testApiTorrent},
		}

		m := metrics.New()
		logger := slog.New(slog.DiscardHandler)

		client := newTestTorrentClient(t, td)

		downloader := New(client, db, logger, m, pc, td, nil, Hooks{Streaming: func() bool { return true }})
		downloader.downloading[testHash] = struct{}{}

		to, err := client.AddTorrent(testMetaInfo)
		require.NoError(t, err)
		for _, f := range to.Files() {
			f.SetPriority(torrent.PiecePriorityNormal)
		}

		downloader.processTorrents()

		assertNothingWanted(t, to)
		assert.Empty(t, downloader.downloading)
		assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
	})
}

func TestDownloader_IsDownloading(t *testing.T) {
	hash := newTestTorrent(t, 1).HashInfoBytes()
	d := &Downloader{downloading: make(map[metainfo.Hash]struct{})}
	assert.False(t, d.IsDownloading(hash))
	d.downloading[hash] = struct{}{}
	assert.True(t, d.IsDownloading(hash))
}

func TestDownloader_Wake(t *testing.T) {
	testMetaInfo := newTestTorrent(t, 1024)
	testHash := testMetaInfo.HashInfoBytes()
	storageType := api.File
	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()
	db := &MockDB{
		settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
		torrents: []*database.Torrent{{Torrent: api.Torrent{Hash: testHash, Magnet: utils.MagnetURIFromHash(testHash), Storage: &storageType}}},
	}
	client := newTestTorrentClient(t, td)
	var streaming atomic.Bool
	downloader := New(client, db, slog.New(slog.DiscardHandler), metrics.New(), pc, td, nil, Hooks{Streaming: streaming.Load})
	to, err := client.AddTorrent(testMetaInfo)
	require.NoError(t, err)
	verifyData(t, to)
	downloading := func() bool { return downloader.IsDownloading(testHash) }

	downloader.Start()
	defer downloader.Stop()
	require.Eventually(t, downloading, time.Second, time.Millisecond)

	// Each change must show well within the one-minute interval.
	streaming.Store(true)
	downloader.Wake()
	require.Eventually(t, func() bool { return !downloading() }, time.Second, time.Millisecond, "waking must pause for streaming at once")
	streaming.Store(false)
	downloader.Wake()
	require.Eventually(t, downloading, time.Second, time.Millisecond, "waking must resume after streaming at once")
}

func TestDownloader_Start(t *testing.T) {
	t.Run("serves a wake requested while stopped with its first pass", func(t *testing.T) {
		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()
		db := &MockDB{settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}}}
		downloader := New(newTestTorrentClient(t, td), db, slog.New(slog.DiscardHandler), metrics.New(), pc, td, nil, Hooks{})

		downloader.Wake()
		downloader.Start()
		defer downloader.Stop()
		require.Eventually(t, func() bool { return db.settingsReads.Load() == 1 }, time.Second, time.Millisecond)
		assert.Never(t, func() bool { return db.settingsReads.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond,
			"a wake requested while stopped must not add a second pass")
	})
}

func TestDownloader_Stop(t *testing.T) {
	t.Run("resets file priorities", func(t *testing.T) {
		testMetaInfo := newTestTorrent(t, 1024)
		testHash := testMetaInfo.HashInfoBytes()

		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()

		m := metrics.New()
		logger := slog.New(slog.DiscardHandler)

		client := newTestTorrentClient(t, td)

		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(true)}},
		}
		downloader := New(client, db, logger, m, pc, td, nil, Hooks{})
		downloader.downloading[testHash] = struct{}{}

		to, err := client.AddTorrent(testMetaInfo)
		require.NoError(t, err)
		for _, f := range to.Files() {
			f.SetPriority(torrent.PiecePriorityNormal)
		}

		downloader.Start()
		downloader.Stop()

		assertNothingWanted(t, to)
		assert.Empty(t, downloader.downloading)
		assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents))
	})

	t.Run("exits cleanly after start", func(t *testing.T) {
		td := t.TempDir()
		pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
		require.NoError(t, err)
		defer pc.Close()

		db := &MockDB{
			settings: &database.Settings{Settings: api.Settings{EnableDownloader: new(false)}},
		}

		m := metrics.New()
		logger := slog.New(slog.DiscardHandler)

		client := newTestTorrentClient(t, td)

		downloader := New(client, db, logger, m, pc, td, nil, Hooks{})

		// Start and stop multiple times in quick succession
		for range 5 {
			downloader.Start()
			downloader.Stop()
		}
	})
}

// assertNothingWanted checks that no piece of to is wanted, so pausing
// actually stops the download rather than only lowering file priorities.
func assertNothingWanted(t *testing.T, to *torrent.Torrent) {
	t.Helper()
	for _, f := range to.Files() {
		assert.Equal(t, torrent.PiecePriorityNone, f.Priority())
	}
	for index := range to.NumPieces() {
		assert.Equal(t, torrent.PiecePriorityNone, to.PieceState(index).Priority, "piece %d is still wanted", index)
	}
}

// readFirstByte reads the first byte of to, waiting briefly for its data.
func readFirstByte(t *testing.T, to *torrent.Torrent) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	r := to.NewReader()
	defer r.Close()
	r.SetContext(ctx)
	_, err := r.Read(make([]byte, 1))
	return err
}

// verifyData checks the pieces of to and waits until their completion is
// recorded, which the torrent client finishes after the check returns. Piece
// priorities count only once it is.
func verifyData(t *testing.T, to *torrent.Torrent) {
	t.Helper()
	require.NoError(t, to.VerifyDataContext(t.Context()))
	require.Eventually(t, func() bool {
		for index := range to.NumPieces() {
			if to.PieceState(index).Marking {
				return false
			}
		}
		return true
	}, time.Second, time.Millisecond)
}
