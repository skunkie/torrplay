// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowSeedStorage delays every piece read so the leecher's in-progress pieces
// stay partially written long enough to be evicted.
type slowSeedStorage struct {
	inner storage.ClientImpl
	delay time.Duration
}

type slowSeedPiece struct {
	storage.PieceImpl
	delay time.Duration
}

func (p slowSeedPiece) ReadAt(b []byte, off int64) (int, error) {
	time.Sleep(p.delay)
	return p.PieceImpl.ReadAt(b, off)
}

func (s slowSeedStorage) OpenTorrent(ctx context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	impl, err := s.inner.OpenTorrent(ctx, info, infoHash)
	if err != nil {
		return impl, err
	}
	if piece := impl.Piece; piece != nil {
		impl.Piece = func(p metainfo.Piece) storage.PieceImpl { return slowSeedPiece{piece(p), s.delay} }
	}
	if pieceWithHash := impl.PieceWithHash; pieceWithHash != nil {
		impl.PieceWithHash = func(p metainfo.Piece, hash g.Option[[]byte]) storage.PieceImpl {
			return slowSeedPiece{pieceWithHash(p, hash), s.delay}
		}
	}
	// Route every read through the delayed ReadAt.
	impl.NewReader = nil
	impl.NewPieceReader = nil
	return impl, nil
}

// incompleteHashCounter counts self-hashes rejected with ErrPieceIncomplete,
// proving that pieces were evicted mid-download.
type incompleteHashCounter struct {
	inner *Client
	count atomic.Int64
}

type countingPiece struct {
	storage.PieceImpl
	counter *incompleteHashCounter
}

func (p countingPiece) SelfHash() (metainfo.Hash, error) {
	hash, err := p.PieceImpl.(storage.SelfHashing).SelfHash()
	if errors.Is(err, ErrPieceIncomplete) {
		p.counter.count.Add(1)
	}
	return hash, err
}

func (c *incompleteHashCounter) OpenTorrent(ctx context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	impl, err := c.inner.OpenTorrent(ctx, info, infoHash)
	if err != nil {
		return impl, err
	}
	piece := impl.Piece
	impl.Piece = func(p metainfo.Piece) storage.PieceImpl { return countingPiece{piece(p), c} }
	return impl, nil
}

func newQuietTorrentClient(t *testing.T, configure func(*torrent.ClientConfig)) *torrent.Client {
	t.Helper()
	cfg := torrent.NewDefaultClientConfig()
	cfg.DisablePEX = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	cfg.Slogger = slog.New(slog.DiscardHandler)
	configure(cfg)
	client, err := torrent.NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func writeRandomSeedTorrent(t *testing.T, dir, name string, size, pieceLength int64) *metainfo.MetaInfo {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(t, err)
	_, err = io.CopyN(f, rand.Reader, size)
	require.NoError(t, f.Close())
	require.NoError(t, err)

	info := metainfo.Info{PieceLength: pieceLength}
	require.NoError(t, info.BuildFromFilePath(path))
	infoBytes, err := bencode.Marshal(info)
	require.NoError(t, err)
	return &metainfo.MetaInfo{InfoBytes: infoBytes}
}

// TestEvictedInProgressPiecesDoNotBanPeers downloads from an untrusted
// peer into memory too small for the in-progress pieces. Pieces evicted
// mid-download must be re-requested as storage failures; hashing their
// zero-filled gaps as peer data made anacrolix ban the only peer that sent
// them, for every torrent, until restart.
func TestEvictedInProgressPiecesDoNotBanPeers(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads from a local seeder")
	}
	const (
		fileSize    = 24 << 20
		maxMemory   = 8 << 20
		pieceLength = 2 << 20
		torrents    = 2
	)
	seedDir := t.TempDir()
	seeder := newQuietTorrentClient(t, func(cfg *torrent.ClientConfig) {
		cfg.DataDir = seedDir
		cfg.DefaultStorage = slowSeedStorage{inner: storage.NewFile(seedDir), delay: 500 * time.Microsecond}
		cfg.Seed = true
	})
	counter := &incompleteHashCounter{inner: New(maxMemory, slog.New(slog.DiscardHandler))}
	leecher := newQuietTorrentClient(t, func(cfg *torrent.ClientConfig) {
		cfg.DefaultStorage = counter
	})
	// Added by address rather than AddClientPeer, which marks the peer trusted
	// and exempts it from banning.
	seederAddr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: seeder.LocalPort()}

	leeched := make([]*torrent.Torrent, 0, torrents)
	for i := range torrents {
		mi := writeRandomSeedTorrent(t, seedDir, fmt.Sprintf("seed%d.bin", i), fileSize, pieceLength)
		seeded, _, err := seeder.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(mi))
		require.NoError(t, err)
		<-seeded.GotInfo()
		require.NoError(t, seeded.VerifyDataContext(t.Context()))

		to, _, err := leecher.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(mi))
		require.NoError(t, err)
		<-to.GotInfo()
		to.AddPeers([]torrent.PeerInfo{{Addr: seederAddr}})
		to.DownloadAll()
		leeched = append(leeched, to)
	}

	require.Eventually(t, func() bool {
		return counter.count.Load() >= 3 || len(leecher.BadPeerIPs()) > 0
	}, 30*time.Second, 50*time.Millisecond, "pieces were never evicted mid-download")

	assert.Positive(t, counter.count.Load(), "the scenario must evict in-progress pieces")
	assert.Empty(t, leecher.BadPeerIPs(), "evicted pieces must not ban the peer that sent them")
	for _, to := range leeched {
		stats := to.Stats()
		assert.Zero(t, stats.PiecesDirtiedBad.Int64(), "evicted pieces must not count as bad peer data")
	}
}

// TestEvictionHandlerKeepsFullyReadTorrentIncomplete reads a whole
// file larger than memory, then reads its start again. Without eviction
// notifications the client believed every piece was still downloaded, dropped
// its only peer as mutually complete, and the second read stalled.
func TestEvictionHandlerKeepsFullyReadTorrentIncomplete(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads from a local seeder")
	}
	const (
		fileSize    = 64 << 20
		maxMemory   = 32 << 20
		pieceLength = 1 << 20
	)
	seedDir := t.TempDir()
	seeder := newQuietTorrentClient(t, func(cfg *torrent.ClientConfig) {
		cfg.DataDir = seedDir
		cfg.DefaultStorage = storage.NewFile(seedDir)
		cfg.Seed = true
	})
	mem := New(maxMemory, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = mem.Close() })
	leecher := newQuietTorrentClient(t, func(cfg *torrent.ClientConfig) {
		cfg.DefaultStorage = mem
		cfg.Seed = false
	})
	mem.SetEvictionHandler(ClientEvictionHandler(leecher))

	mi := writeRandomSeedTorrent(t, seedDir, "movie.bin", fileSize, pieceLength)
	seeded, _, err := seeder.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(mi))
	require.NoError(t, err)
	<-seeded.GotInfo()
	require.NoError(t, seeded.VerifyDataContext(t.Context()))
	to, _, err := leecher.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(mi))
	require.NoError(t, err)
	<-to.GotInfo()
	to.AddPeers([]torrent.PeerInfo{{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: seeder.LocalPort()}}})

	readFrom := func(n int64) error {
		r := to.NewReader()
		defer r.Close()
		r.SetReadahead(4 << 20)
		// The local anacrolix seeder occasionally stops answering a queued
		// request until its one-minute keep-alive drops the connection, so
		// allow a read to outlast one reconnect.
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		r.SetContext(ctx)
		_, err := io.CopyN(io.Discard, r, n)
		return err
	}
	require.NoError(t, readFrom(fileSize), "first full read")

	completePieces := func() int {
		n := 0
		for i := range to.NumPieces() {
			if to.PieceState(i).Complete {
				n++
			}
		}
		return n
	}
	residentPieces := int(maxMemory / pieceLength)
	require.Eventually(t, func() bool { return completePieces() <= residentPieces },
		5*time.Second, 20*time.Millisecond, "evicted pieces must stop counting as downloaded")
	assert.NotEmpty(t, to.PeerConns(), "a torrent with evicted pieces must keep its peers")

	require.NoError(t, readFrom(8<<20), "reading evicted data again must download it")
}
