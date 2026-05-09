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
)

// MockDB is a mock implementation of the DatabaseInterface for testing.
type MockDB struct {
	database.DatabaseInterface
	torrents []*api.Torrent
	err      error
}

func (m *MockDB) GetTorrents() ([]*api.Torrent, error) {
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

func TestDownloader_ProcessTorrents_Metrics(t *testing.T) {
	// 1. Setup a test torrent.
	testMetaInfo := newTestTorrent(t, "test-torrent", 1024)
	testHash := testMetaInfo.HashInfoBytes()
	storageType := api.File
	testApiTorrent := &api.Torrent{
		Hash:    testHash,
		Magnet:  testMetaInfo.Magnet(nil, nil).String(),
		Storage: &storageType,
	}

	// 2. Setup mocks and test dependencies.
	td := t.TempDir()
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(td, "pieces.db"))
	require.NoError(t, err)
	defer pc.Close()

	db := &MockDB{
		torrents: []*api.Torrent{testApiTorrent},
	}
	m := metrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Use a real torrent client, but configured to not touch the network.
	clientCfg := torrent.NewDefaultClientConfig()
	clientCfg.DataDir = td
	clientCfg.NoDHT = true
	clientCfg.DisablePEX = true
	clientCfg.DisableTrackers = true
	clientCfg.DisableWebtorrent = true
	clientCfg.DisableWebseeds = true
	client, err := torrent.NewClient(clientCfg)
	require.NoError(t, err)
	defer client.Close()

	// Create the downloader instance.
	downloader := New(client, db, logger, m, pc, td, nil)
	originalGotInfoTimeout := gotInfoTimeout
	gotInfoTimeout = 1 * time.Millisecond // Set a very short timeout for testing.
	defer func() {
		gotInfoTimeout = originalGotInfoTimeout
	}()

	// 3. Run the function to be tested.
	downloader.processTorrents()

	// 4. Assert the results.
	// Check that the metric was updated to 0, since we have one torrent that will fail to get info.
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.DownloadingTorrents) == 0
	}, time.Second, 10*time.Millisecond, "DownloadingTorrents metric should be 0")

	// Verify that if we run it again, it's still 0 (not incremented).
	downloader.processTorrents()
	assert.Equal(t, float64(0), testutil.ToFloat64(m.DownloadingTorrents), "DownloadingTorrents metric should remain 0")

	// Now, let's simulate the torrent completing by having the DB return no torrents.
	db.torrents = []*api.Torrent{}
	downloader.processTorrents()
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.DownloadingTorrents) == 0
	}, time.Second, 10*time.Millisecond, "DownloadingTorrents metric should be 0 after torrent is removed")
}
