// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// To run the tests for this package, it is highly recommended to use the -race flag
// to detect potential race conditions:
// CGO_ENABLED=1 go test -race -v ./pkg/storage/...

package storage

import (
	"context"
	"crypto/sha1"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m)
}

// newTestClient creates a new client for testing with a specified memory limit.
func newTestClient(maxMemory int64) *Client {
	return New(maxMemory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// newBenchmarkClient creates a new client for benchmarking with discarded logs to avoid I/O overhead.
func newBenchmarkClient(maxMemory int64) *Client {
	return New(maxMemory, slog.New(slog.DiscardHandler))
}

func requirePieceBuffer(tb testing.TB, piece storage.PieceImpl) []byte {
	tb.Helper()
	impl, ok := piece.(*pieceImpl)
	if !ok {
		tb.Fatalf("unexpected piece implementation %T", piece)
	}
	pd, err := impl.getPieceData()
	if err != nil {
		tb.Fatal(err)
	}
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	return pd.data
}

func requireTorrentStats(tb testing.TB, client *Client, infoHash metainfo.Hash) TorrentStats {
	tb.Helper()
	stats, err := client.TorrentStats(infoHash)
	require.NoError(tb, err)
	return stats
}

func residentPieceIndexes(tb testing.TB, client *Client, infoHash metainfo.Hash) []int {
	tb.Helper()
	stats := requireTorrentStats(tb, client, infoHash)
	indexes := make([]int, 0, stats.ResidentPieces)
	for _, piece := range stats.Pieces {
		if piece.Resident {
			indexes = append(indexes, piece.Index)
		}
	}
	return indexes
}

func piecesByCompletion(tb testing.TB, client *Client, infoHash metainfo.Hash, complete bool) []int {
	tb.Helper()
	stats := requireTorrentStats(tb, client, infoHash)
	indexes := make([]int, 0, len(stats.Pieces))
	for _, piece := range stats.Pieces {
		if piece.Complete == complete {
			indexes = append(indexes, piece.Index)
		}
	}
	return indexes
}

func requirePieceStats(tb testing.TB, client *Client, infoHash metainfo.Hash, index int) PieceStats {
	tb.Helper()
	for _, piece := range requireTorrentStats(tb, client, infoHash).Pieces {
		if piece.Index == index {
			return piece
		}
	}
	tb.Fatalf("piece %d is not tracked", index)
	return PieceStats{}
}

// newTestInfo creates a dummy torrent info and hash for testing.
func newTestInfo(pieceLength int64, numPieces int) (*metainfo.Info, metainfo.Hash) {
	info := &metainfo.Info{
		PieceLength: pieceLength,
		Pieces:      make([]byte, 20*numPieces),
		Name:        "test_torrent",
		Length:      pieceLength * int64(numPieces),
	}
	for i := range numPieces {
		infoHash := sha1.Sum(fmt.Appendf(nil, "piece_%d", i))
		copy(info.Pieces[i*20:(i+1)*20], infoHash[:])
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	infoHash := metainfo.Hash(sha1.Sum(b))
	return info, infoHash
}

func TestClient_OpenTorrent(t *testing.T) {
	// Verifies that a new torrent is correctly initialized and tracked.
	t.Run("initializes and tracks torrent", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		_, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		client.mu.RLock()
		defer client.mu.RUnlock()

		assert.NotNil(t, client.torrents[infoHash])
		assert.Equal(t, 4, client.torrents[infoHash].totalPieces)
	})

	t.Run("pure v2 torrent", func(t *testing.T) {
		client := newTestClient(1024)
		info := &metainfo.Info{
			MetaVersion: 2,
			PieceLength: 256,
			Name:        "v2_torrent",
			FileTree: metainfo.FileTree{Dir: map[string]metainfo.FileTree{
				"video.mp4": {File: metainfo.FileTreeFile{Length: 512}},
			}},
		}
		infoHash := metainfo.Hash(sha1.Sum([]byte("pure-v2")))

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		require.NotNil(t, torrentImpl.Piece)

		stats, err := client.TorrentStats(infoHash)
		require.NoError(t, err)
		assert.Equal(t, 2, stats.TotalPieces)
	})

	t.Run("preserves piece memory on reopen", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 2)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		_, err = torrentImpl.Piece(info.Piece(0)).WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		memBefore := requireTorrentStats(t, client, infoHash).ResidentBytes
		assert.Equal(t, int64(256), memBefore)

		// Re-open the same torrent.
		_, err = client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		memAfter := requireTorrentStats(t, client, infoHash).ResidentBytes
		assert.Equal(t, int64(256), memAfter, "pieceMemory should be preserved on reopen")
	})

	t.Run("shared state survives one handle close", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 1)
		first, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		second, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		firstPiece := first.Piece(info.Piece(0))
		secondPiece := second.Piece(info.Piece(0))
		_, err = firstPiece.WriteAt([]byte("first"), 0)
		require.NoError(t, err)

		require.NoError(t, first.Close())
		require.NoError(t, first.Close(), "closing a handle twice must not release another handle")
		_, err = firstPiece.WriteAt([]byte("stale"), 0)
		assert.ErrorIs(t, err, ErrTorrentClosed)

		buf := make([]byte, 5)
		n, err := secondPiece.ReadAt(buf, 0)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "first", string(buf))
		_, err = secondPiece.WriteAt([]byte("alive"), 0)
		require.NoError(t, err)
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)

		require.NoError(t, second.Close())
		assert.Equal(t, int64(0), client.MemoryStats().UsedBytes)
		_, err = client.TorrentStats(infoHash)
		assert.ErrorIs(t, err, ErrTorrentNotManaged)
		_, err = secondPiece.WriteAt([]byte("closed"), 0)
		assert.ErrorIs(t, err, ErrTorrentClosed)
	})
}

func TestClient_CloseTorrent(t *testing.T) {
	// Ensures that closing a torrent removes its data and state.
	t.Run("removes data and state", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		_, err = torrentImpl.Piece(info.Piece(0)).WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		err = torrentImpl.Close()
		require.NoError(t, err)

		client.mu.RLock()
		defer client.mu.RUnlock()

		assert.Nil(t, client.torrents[infoHash])
		assert.Empty(t, client.pieces)
	})

	// Verifies that closing a torrent
	// removes all associated active ranges.
	t.Run("clears active ranges", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		client.SetProtection(infoHash, 1, activeProtection(0, 2))
		client.SetProtection(infoHash, 2, activeProtection(1, 3))

		err = torrentImpl.Close()
		require.NoError(t, err)

		client.mu.RLock()
		defer client.mu.RUnlock()
		for key := range client.protections {
			assert.NotEqual(t, infoHash, key.infoHash, "no active ranges should remain for closed torrent")
		}
	})

	// Verifies that closing a torrent
	// removes its registered file boundaries.
	t.Run("clears file boundaries", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 8)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		client.SetProtection(infoHash, 1, boundaryProtection(0, 1, 6, 7))

		client.mu.RLock()
		assert.True(t, inFileBoundary(client, pieceKey{infoHash: infoHash, index: 0}))
		assert.True(t, inFileBoundary(client, pieceKey{infoHash: infoHash, index: 7}))
		assert.False(t, inFileBoundary(client, pieceKey{infoHash: infoHash, index: 3}))
		client.mu.RUnlock()

		require.NoError(t, torrentImpl.Close())

		client.mu.RLock()
		assert.False(t, inFileBoundary(client, pieceKey{infoHash: infoHash, index: 0}))
		assert.False(t, inFileBoundary(client, pieceKey{infoHash: infoHash, index: 7}))
		client.mu.RUnlock()
	})
}

func TestClient_TorrentStats(t *testing.T) {
	// Checks that torrent-specific memory statistics are accurate.
	t.Run("reports memory statistics", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		_, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Add some pieces
		client.pieces[pieceKey{infoHash: infoHash, index: 0}] = &pieceData{data: make([]byte, 256), complete: true, writtenBytes: 256, pieceSize: 256}
		client.pieces[pieceKey{infoHash: infoHash, index: 1}] = &pieceData{data: make([]byte, 256), complete: false, writtenBytes: 64, pieceSize: 256}

		stats, err := client.TorrentStats(infoHash)
		require.NoError(t, err)

		assert.Equal(t, 4, stats.TotalPieces)
		assert.Equal(t, int64(512), stats.TrackedBytes)
		assert.Equal(t, int64(256), stats.CompletedBytes)
		assert.Equal(t, int64(320), stats.WrittenBytes)
		assert.Equal(t, 2, stats.ResidentPieces)
	})

	t.Run("tracks written bytes", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 1)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		piece := torrentImpl.Piece(info.Piece(0))

		_, err = piece.WriteAt(make([]byte, 32), 0)
		require.NoError(t, err)
		stats := requireTorrentStats(t, client, infoHash)
		require.Len(t, stats.Pieces, 1)
		assert.Equal(t, int64(32), stats.WrittenBytes)
		assert.Equal(t, int64(32), stats.Pieces[0].WrittenBytes)
		assert.Equal(t, int64(256), stats.ResidentBytes)

		// Repeated writes to the same span do not inflate the resident byte count.
		for range 10 {
			_, err = piece.WriteAt(make([]byte, 32), 0)
			require.NoError(t, err)
		}
		stats = requireTorrentStats(t, client, infoHash)
		assert.Equal(t, int64(32), stats.WrittenBytes)
		assert.Equal(t, int64(32), stats.Pieces[0].WrittenBytes)

		_, err = piece.WriteAt(make([]byte, 32), 16)
		require.NoError(t, err)
		_, err = piece.WriteAt(make([]byte, 32), 96)
		require.NoError(t, err)
		stats = requireTorrentStats(t, client, infoHash)
		assert.Equal(t, int64(80), stats.WrittenBytes)
		assert.Equal(t, int64(80), stats.Pieces[0].WrittenBytes)
	})

	t.Run("includes global stats", func(t *testing.T) {
		client := newTestClient(2048)
		info1, infoHash1 := newTestInfo(256, 2)
		info2, infoHash2 := newTestInfo(256, 3)

		torrent1, err := client.OpenTorrent(context.Background(), info1, infoHash1)
		require.NoError(t, err)
		torrent2, err := client.OpenTorrent(context.Background(), info2, infoHash2)
		require.NoError(t, err)

		_, err = torrent1.Piece(info1.Piece(0)).WriteAt([]byte("one"), 0)
		require.NoError(t, err)
		_, err = torrent2.Piece(info2.Piece(0)).WriteAt([]byte("two"), 0)
		require.NoError(t, err)

		stats, err := client.TorrentStats(infoHash1)
		require.NoError(t, err)
		assert.Equal(t, 2, stats.Global.TorrentsUsingMemory)
		assert.Equal(t, 2, stats.Global.TrackedPieces)
		assert.Equal(t, int64(512), stats.Global.UsedBytes)
	})

	t.Run("unmanaged torrent", func(t *testing.T) {
		client := newTestClient(1024)

		_, err := client.TorrentStats(metainfo.Hash{1})
		assert.ErrorIs(t, err, ErrTorrentNotManaged)
	})

	// Verifies the calculation of memory usage fraction.
	t.Run("memory usage fraction", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 2 pieces
		for i := range 2 {
			p := torrentImpl.Piece(info.Piece(i))
			_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
			require.NoError(t, err)
		}

		progress := requireTorrentStats(t, client, infoHash).MemoryUsageFraction()
		assert.InDelta(t, 0.5, progress, 0.001)
	})

	t.Run("memory usage fraction with zero limit", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 1)
		_, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		require.NoError(t, client.SetMaxMemory(0))
		assert.Equal(t, float64(0), requireTorrentStats(t, client, infoHash).MemoryUsageFraction())
	})

	// Verifies that the list of in-memory pieces is correct.
	t.Run("resident pieces", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 2 pieces
		for i := range 2 {
			p := torrentImpl.Piece(info.Piece(i))
			_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
			require.NoError(t, err)
		}

		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.ElementsMatch(t, []int{0, 1}, inMemory)
	})

	// Confirms that the list of incomplete pieces is accurate.
	t.Run("incomplete pieces", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 2 pieces, complete 1
		p0 := torrentImpl.Piece(info.Piece(0))
		_, err = p0.WriteAt([]byte("p0"), 0)
		require.NoError(t, err)
		err = p0.MarkComplete()
		require.NoError(t, err)

		p1 := torrentImpl.Piece(info.Piece(1))
		_, err = p1.WriteAt([]byte("p1"), 0)
		require.NoError(t, err)

		incomplete := piecesByCompletion(t, client, infoHash, false)
		assert.ElementsMatch(t, []int{1}, incomplete)
	})

	// Ensures the list of completed pieces is correct.
	t.Run("completed pieces", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 2 pieces, complete 1
		p0 := torrentImpl.Piece(info.Piece(0))
		_, err = p0.WriteAt([]byte("p0"), 0)
		require.NoError(t, err)
		err = p0.MarkComplete()
		require.NoError(t, err)

		p1 := torrentImpl.Piece(info.Piece(1))
		_, err = p1.WriteAt([]byte("p1"), 0)
		require.NoError(t, err)

		completed := piecesByCompletion(t, client, infoHash, true)
		assert.ElementsMatch(t, []int{0}, completed)
	})

	// Checks that the status of an individual piece is reported correctly.
	t.Run("reports piece status", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0))
		_, err = p.WriteAt([]byte("data"), 0)
		require.NoError(t, err)
		err = p.MarkComplete()
		require.NoError(t, err)

		status := requirePieceStats(t, client, infoHash, 0)

		assert.True(t, status.Complete)
		assert.True(t, status.Resident)
		assert.Equal(t, 0, status.Index)
		assert.Equal(t, int64(256), status.SizeBytes)
	})
}

// TestPieceImplReadWrite tests basic read and write operations on a piece.
func TestPieceImplReadWrite(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))

	// Write data
	data := []byte("hello world")
	n, err := p.WriteAt(data, 0)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)

	// Read data
	readBuf := make([]byte, len(data))
	n, err = p.ReadAt(readBuf, 0)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, readBuf)
}

func TestPieceImpl_MarkComplete(t *testing.T) {
	// Verifies the logic for marking pieces as complete or not complete.
	t.Run("marks piece complete and not complete", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0))

		// Cannot mark complete without data
		err = p.MarkComplete()
		assert.Error(t, err)

		// Write data, then mark complete
		_, err = p.WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		err = p.MarkComplete()
		require.NoError(t, err)

		completion := p.Completion()
		assert.True(t, completion.Complete)

		// Mark not complete
		err = p.MarkNotComplete()
		require.NoError(t, err)

		completion = p.Completion()
		assert.False(t, completion.Complete)
	})

	t.Run("releases piece lock before reporting", func(t *testing.T) {
		client := newTestClient(512)
		defer client.Close()
		info, infoHash := newTestInfo(256, 1)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		client.SetEvictionHandler(func(metainfo.Hash, int) {})
		p := torrentImpl.Piece(info.Piece(0))

		// A tracked piece without data, as while a re-download is allocating.
		key := pieceKey{infoHash: infoHash, index: 0}
		pd := &pieceData{pieceSize: 256}
		client.mu.Lock()
		pd.torrent = client.torrents[infoHash]
		client.pieces[key] = pd
		pd.lruElem = client.lru.PushFront(key)
		client.mu.Unlock()

		// A held read lock lets MarkComplete find the piece but blocks its
		// report, which needs the write lock.
		client.mu.RLock()
		done := make(chan error, 1)
		go func() { done <- p.MarkComplete() }()

		// Once the report waits for the write lock, new read locks are refused.
		require.Eventually(t, func() bool {
			if client.mu.TryRLock() {
				client.mu.RUnlock()
				return false
			}
			return true
		}, 5*time.Second, time.Millisecond)

		// Eviction takes c.mu before pd.mu, so MarkComplete must not wait for
		// c.mu while holding pd.mu.
		released := pd.mu.TryLock()
		if released {
			pd.mu.Unlock()
		}
		client.mu.RUnlock()
		require.Error(t, <-done)
		assert.True(t, released, "MarkComplete holds the piece lock while waiting for the client lock")
	})
}

func TestClientMemoryEviction(t *testing.T) {
	// Simulates memory pressure to ensure the LRU eviction policy works.
	t.Run("evicts least recently used pieces", func(t *testing.T) {
		client := newTestClient(512) // Max memory for 2 pieces of 256 bytes
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 3 pieces, causing eviction of the first one.
		for i := range 3 {
			p := torrentImpl.Piece(info.Piece(i))
			_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
			require.NoError(t, err)
		}

		client.mu.RLock()
		defer client.mu.RUnlock()

		// Check that only 2 pieces are in memory
		inMemoryCount := 0
		for _, pd := range client.pieces {
			if pd.data != nil {
				inMemoryCount++
			}
		}
		assert.Equal(t, 2, inMemoryCount)
	})

	t.Run("spares downloading pieces", func(t *testing.T) {
		tests := []struct {
			name         string
			stale        bool
			completeB    bool
			wantResident []int
		}{
			// The older, still-downloading piece 0 outlives the newer complete piece 1.
			{name: "complete piece evicted before downloading one", completeB: true, wantResident: []int{0, 2}},
			// A piece not written within the grace period is no longer downloading.
			{name: "stale incomplete piece evicted in LRU order", stale: true, completeB: true, wantResident: []int{1, 2}},
			// With only downloading pieces left, the oldest is still evicted.
			{name: "downloading pieces yield when nothing else fits", wantResident: []int{1, 2}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				client := newTestClient(512) // room for 2 pieces of 256 bytes
				info, infoHash := newTestInfo(256, 3)
				torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
				require.NoError(t, err)

				write := func(i int) storage.PieceImpl {
					p := torrentImpl.Piece(info.Piece(i))
					_, err := p.WriteAt(make([]byte, 256), 0)
					require.NoError(t, err)
					return p
				}
				write(0)
				if tt.stale {
					client.mu.RLock()
					pd := client.pieces[pieceKey{infoHash: infoHash, index: 0}]
					client.mu.RUnlock()
					pd.lastTouchNano.Store(time.Now().Add(-2 * downloadingPieceGrace).UnixNano())
				}
				p1 := write(1)
				if tt.completeB {
					require.NoError(t, p1.MarkComplete())
				}
				write(2)

				stats, err := client.TorrentStats(infoHash)
				require.NoError(t, err)
				resident := make([]int, 0, len(stats.Pieces))
				for _, piece := range stats.Pieces {
					if piece.Resident {
						resident = append(resident, piece.Index)
					}
				}
				slices.Sort(resident)
				assert.Equal(t, tt.wantResident, resident)
			})
		}
	})
}

func TestClient_SetEvictionHandler(t *testing.T) {
	t.Run("reports evicted pieces", func(t *testing.T) {
		client := newTestClient(512) // room for 2 pieces of 256 bytes
		defer client.Close()
		info, infoHash := newTestInfo(256, 4)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		pieces := make([]storage.PieceImpl, 4)
		for i := range pieces {
			pieces[i] = torrentImpl.Piece(info.Piece(i))
		}

		type eviction struct {
			infoHash metainfo.Hash
			index    int
			complete bool
		}
		evictions := make(chan eviction, 8)
		client.SetEvictionHandler(func(ih metainfo.Hash, index int) {
			// The handler may call back into the client without deadlocking.
			evictions <- eviction{infoHash: ih, index: index, complete: pieces[index].Completion().Complete}
		})
		// Only the first handler is kept.
		client.SetEvictionHandler(func(metainfo.Hash, int) { t.Error("replacement handler must not run") })

		write := func(i int) {
			_, err := pieces[i].WriteAt(make([]byte, 256), 0)
			require.NoError(t, err)
		}
		expect := func(index int) {
			t.Helper()
			select {
			case got := <-evictions:
				assert.Equal(t, eviction{infoHash: infoHash, index: index, complete: false}, got)
			case <-time.After(5 * time.Second):
				t.Fatalf("eviction of piece %d was not reported", index)
			}
		}
		write(0)
		require.NoError(t, pieces[0].MarkComplete())
		write(1)
		write(2) // evicts complete piece 0
		expect(0)
		write(3) // evicts incomplete piece 1
		expect(1)
	})

	t.Run("reports piece missing when marked complete", func(t *testing.T) {
		client := newTestClient(512)
		defer client.Close()
		info, infoHash := newTestInfo(256, 2)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		p := torrentImpl.Piece(info.Piece(0))

		// The piece hashes and is evicted before the engine marks it complete.
		_, err = p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
		evictUnprotected(client, 0)

		reported := make(chan int, 4)
		client.SetEvictionHandler(func(_ metainfo.Hash, index int) { reported <- index })
		require.ErrorIs(t, p.MarkComplete(), ErrPieceNotAvailable)
		select {
		case index := <-reported:
			assert.Equal(t, 0, index)
		case <-time.After(5 * time.Second):
			t.Fatal("piece missing when marked complete was not reported")
		}
	})

	t.Run("ignores closed torrents", func(t *testing.T) {
		client := newTestClient(512)
		defer client.Close()
		info, infoHash := newTestInfo(256, 2)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		p := torrentImpl.Piece(info.Piece(0))
		_, err = p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)

		reported := make(chan int, 4)
		client.SetEvictionHandler(func(_ metainfo.Hash, index int) { reported <- index })
		require.NoError(t, torrentImpl.Close())
		require.Error(t, p.MarkComplete())
		select {
		case index := <-reported:
			t.Fatalf("piece %d of a closed torrent was reported", index)
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("stops on close", func(t *testing.T) {
		client := newTestClient(256)
		client.SetEvictionHandler(func(metainfo.Hash, int) {})
		require.NoError(t, client.Close())
		require.NoError(t, client.Close())

		// A handler registered after Close never starts a goroutine; the package
		// leak check fails otherwise.
		closed := newTestClient(256)
		require.NoError(t, closed.Close())
		closed.SetEvictionHandler(func(metainfo.Hash, int) { t.Error("handler must not run after Close") })
	})
}

func TestClient_Counters(t *testing.T) {
	full := make([]byte, 256)
	tests := []struct {
		name      string
		maxMemory int64
		run       func(t *testing.T, client *Client, infoHash metainfo.Hash, pieces []storage.PieceImpl)
		want      Counters
	}{
		{
			name:      "complete piece evicted",
			maxMemory: 256,
			run: func(t *testing.T, _ *Client, _ metainfo.Hash, pieces []storage.PieceImpl) {
				t.Helper()
				_, err := pieces[0].WriteAt(full, 0)
				require.NoError(t, err)
				require.NoError(t, pieces[0].MarkComplete())
				_, err = pieces[1].WriteAt(full, 0)
				require.NoError(t, err)
			},
			want: Counters{EvictedCompletePieces: 1},
		},
		{
			name:      "partial piece evicted",
			maxMemory: 256,
			run: func(t *testing.T, _ *Client, _ metainfo.Hash, pieces []storage.PieceImpl) {
				t.Helper()
				_, err := pieces[0].WriteAt(full[:128], 0)
				require.NoError(t, err)
				_, err = pieces[1].WriteAt(full, 0)
				require.NoError(t, err)
			},
			want: Counters{EvictedIncompleteBytes: 128, EvictedIncompletePieces: 1},
		},
		{
			name:      "evicted piece read and marked complete",
			maxMemory: 512,
			run: func(t *testing.T, client *Client, _ metainfo.Hash, pieces []storage.PieceImpl) {
				t.Helper()
				_, err := pieces[0].WriteAt(full, 0)
				require.NoError(t, err)
				require.NoError(t, pieces[0].MarkComplete())
				evictUnprotected(client, 0)
				_, err = pieces[0].ReadAt(make([]byte, 16), 0)
				require.ErrorIs(t, err, ErrPieceNotAvailable)
				require.Error(t, pieces[0].MarkComplete())
			},
			want: Counters{CompletionMisses: 1, EvictedCompletePieces: 1, ReadMisses: 1},
		},
		{
			name:      "unwritten bytes read and hashed",
			maxMemory: 512,
			run: func(t *testing.T, _ *Client, _ metainfo.Hash, pieces []storage.PieceImpl) {
				t.Helper()
				_, err := pieces[0].WriteAt(full[128:], 128)
				require.NoError(t, err)
				_, err = pieces[0].ReadAt(make([]byte, 64), 0)
				require.ErrorIs(t, err, ErrPieceIncomplete)
				_, err = pieces[0].(storage.SelfHashing).SelfHash()
				require.ErrorIs(t, err, ErrPieceIncomplete)
			},
			want: Counters{IncompleteHashes: 1, IncompleteReads: 1},
		},
		{
			name:      "boundary piece evicted",
			maxMemory: 256,
			run: func(t *testing.T, client *Client, infoHash metainfo.Hash, pieces []storage.PieceImpl) {
				t.Helper()
				_, err := pieces[0].WriteAt(full, 0)
				require.NoError(t, err)
				require.NoError(t, pieces[0].MarkComplete())
				client.SetProtection(infoHash, 1, boundaryProtection(0, 0, 0, 0))
				_, err = pieces[1].WriteAt(full, 0)
				require.NoError(t, err)
			},
			want: Counters{BoundaryEvictions: 1, EvictedCompletePieces: 1},
		},
		{
			name:      "active range piece evicted",
			maxMemory: 256,
			run: func(t *testing.T, client *Client, infoHash metainfo.Hash, pieces []storage.PieceImpl) {
				t.Helper()
				_, err := pieces[0].WriteAt(full, 0)
				require.NoError(t, err)
				require.NoError(t, pieces[0].MarkComplete())
				client.SetProtection(infoHash, 1, activeProtection(0, 0))
				_, err = pieces[1].WriteAt(full, 0)
				require.NoError(t, err)
			},
			want: Counters{ActiveRangeEvictions: 1, EvictedCompletePieces: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(tt.maxMemory)
			defer client.Close()
			info, infoHash := newTestInfo(256, 2)
			torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
			require.NoError(t, err)
			pieces := []storage.PieceImpl{torrentImpl.Piece(info.Piece(0)), torrentImpl.Piece(info.Piece(1))}

			tt.run(t, client, infoHash, pieces)
			assert.Equal(t, tt.want, client.Counters())
		})
	}
}

func TestCounters_Add(t *testing.T) {
	a := Counters{ActiveRangeEvictions: 1, BoundaryEvictions: 2, CompletionMisses: 3, EvictedCompletePieces: 4, EvictedIncompleteBytes: 5, EvictedIncompletePieces: 6, IncompleteHashes: 7, IncompleteReads: 8, ReadMisses: 9}
	b := Counters{ActiveRangeEvictions: 10, BoundaryEvictions: 20, CompletionMisses: 30, EvictedCompletePieces: 40, EvictedIncompleteBytes: 50, EvictedIncompletePieces: 60, IncompleteHashes: 70, IncompleteReads: 80, ReadMisses: 90}
	assert.Equal(t, Counters{ActiveRangeEvictions: 11, BoundaryEvictions: 22, CompletionMisses: 33, EvictedCompletePieces: 44, EvictedIncompleteBytes: 55, EvictedIncompletePieces: 66, IncompleteHashes: 77, IncompleteReads: 88, ReadMisses: 99}, a.Add(b))
}

func TestClientPieceBufferReuse(t *testing.T) {
	t.Run("standard eviction clears stale data", func(t *testing.T) {
		client := newTestClient(256)
		info, infoHash := newTestInfo(256, 2)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p0 := torrentImpl.Piece(info.Piece(0))
		p1 := torrentImpl.Piece(info.Piece(1))
		oldData := make([]byte, 256)
		for i := range oldData {
			oldData[i] = 0xff
		}
		_, err = p0.WriteAt(oldData, 0)
		require.NoError(t, err)
		oldBuffer := requirePieceBuffer(t, p0)
		oldPointer := &oldBuffer[0]

		newPrefix := []byte{1, 2, 3, 4}
		_, err = p1.WriteAt(newPrefix, 0)
		require.NoError(t, err)
		newBuffer := requirePieceBuffer(t, p1)
		if oldPointer != &newBuffer[0] {
			t.Fatal("expected exact-size evicted buffer to be reused")
		}
		assert.Equal(t, newPrefix, newBuffer[:len(newPrefix)])
		for i, value := range newBuffer[len(newPrefix):] {
			if value != 0 {
				t.Fatalf("reused buffer retained stale byte at offset %d: %d", i+len(newPrefix), value)
			}
		}
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
	})

	t.Run("different size falls back to allocation", func(t *testing.T) {
		client := newTestClient(512)
		oldInfo, oldHash := newTestInfo(512, 1)
		oldTorrent, err := client.OpenTorrent(context.Background(), oldInfo, oldHash)
		require.NoError(t, err)
		oldPiece := oldTorrent.Piece(oldInfo.Piece(0))
		_, err = oldPiece.WriteAt(make([]byte, 512), 0)
		require.NoError(t, err)
		oldBuffer := requirePieceBuffer(t, oldPiece)
		oldPointer := &oldBuffer[0]

		newInfo, newHash := newTestInfo(256, 1)
		newTorrent, err := client.OpenTorrent(context.Background(), newInfo, newHash)
		require.NoError(t, err)
		newPiece := newTorrent.Piece(newInfo.Piece(0))
		_, err = newPiece.WriteAt([]byte{1}, 0)
		require.NoError(t, err)
		newBuffer := requirePieceBuffer(t, newPiece)
		if oldPointer == &newBuffer[0] {
			t.Fatal("different-size buffer must not be reused")
		}
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
	})

	t.Run("transfers accounting across torrents", func(t *testing.T) {
		client := newTestClient(256)
		info, _ := newTestInfo(256, 1)
		oldHash := metainfo.Hash{1}
		newHash := metainfo.Hash{2}
		oldTorrent, err := client.OpenTorrent(context.Background(), info, oldHash)
		require.NoError(t, err)
		newTorrent, err := client.OpenTorrent(context.Background(), info, newHash)
		require.NoError(t, err)

		oldPiece := oldTorrent.Piece(info.Piece(0))
		newPiece := newTorrent.Piece(info.Piece(0))
		_, err = oldPiece.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
		oldBuffer := requirePieceBuffer(t, oldPiece)
		oldPointer := &oldBuffer[0]

		_, err = newPiece.WriteAt([]byte{1}, 0)
		require.NoError(t, err)
		newBuffer := requirePieceBuffer(t, newPiece)
		if oldPointer != &newBuffer[0] {
			t.Fatal("expected buffer handoff across torrents")
		}
		assert.Equal(t, int64(0), requireTorrentStats(t, client, oldHash).ResidentBytes)
		assert.Equal(t, int64(256), requireTorrentStats(t, client, newHash).ResidentBytes)
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
	})

	t.Run("emergency eviction", func(t *testing.T) {
		client := newTestClient(256)
		info, infoHash := newTestInfo(256, 2)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p0 := torrentImpl.Piece(info.Piece(0))
		p1 := torrentImpl.Piece(info.Piece(1))
		_, err = p0.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
		oldBuffer := requirePieceBuffer(t, p0)
		oldPointer := &oldBuffer[0]
		client.SetProtection(infoHash, 1, activeProtection(0, 0))

		_, err = p1.WriteAt([]byte{1}, 0)
		require.NoError(t, err)
		newBuffer := requirePieceBuffer(t, p1)
		if oldPointer != &newBuffer[0] {
			t.Fatal("emergency eviction should hand off an exact-size buffer")
		}
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
	})

	t.Run("concurrent first write", func(t *testing.T) {
		client := newTestClient(256)
		info, infoHash := newTestInfo(256, 2)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p0 := torrentImpl.Piece(info.Piece(0))
		p1 := torrentImpl.Piece(info.Piece(1))
		oldData := make([]byte, 256)
		for i := range oldData {
			oldData[i] = 0xff
		}
		_, err = p0.WriteAt(oldData, 0)
		require.NoError(t, err)

		const writers = 16
		errs := make(chan error, writers)
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := p1.WriteAt([]byte{byte(i + 1)}, int64(i))
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}

		newBuffer := requirePieceBuffer(t, p1)
		for i := range writers {
			assert.Equal(t, byte(i+1), newBuffer[i])
		}
		for i, value := range newBuffer[writers:] {
			if value != 0 {
				t.Fatalf("reused buffer retained stale byte at offset %d: %d", i+writers, value)
			}
		}
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
	})
}

func TestClient_SetMaxMemory(t *testing.T) {
	// Ensures that dynamically changing the memory limit triggers eviction.
	t.Run("shrinking triggers eviction", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 3 pieces
		for i := range 3 {
			p := torrentImpl.Piece(info.Piece(i))
			_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
			require.NoError(t, err)
		}

		// Reduce memory, triggering eviction
		require.NoError(t, client.SetMaxMemory(512))

		stats, err := client.TorrentStats(infoHash)
		require.NoError(t, err)
		assert.Equal(t, 2, stats.ResidentPieces)
		client.mu.RLock()
		assert.Equal(t, int64(512), client.torrents[infoHash].pieceMemory, "eviction must release the torrent's memory")
		client.mu.RUnlock()

		require.NoError(t, client.SetMaxMemory(0))
		assert.Zero(t, client.MemoryStats().TorrentsUsingMemory, "a torrent without resident pieces uses no memory")
	})

	t.Run("enforces limit across active ranges", func(t *testing.T) {
		client := newTestClient(512)
		info, infoHash := newTestInfo(256, 2)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		for i := range 2 {
			_, err = torrentImpl.Piece(info.Piece(i)).WriteAt([]byte("data"), 0)
			require.NoError(t, err)
		}
		client.SetProtection(infoHash, 1, activeProtection(0, 1))

		require.NoError(t, client.SetMaxMemory(0))
		stats := client.MemoryStats()
		assert.Equal(t, int64(0), stats.LimitBytes)
		assert.Equal(t, int64(0), stats.UsedBytes)
		assert.Equal(t, 0, stats.TrackedPieces)
	})

	// Verifies that SetMaxMemory(-100)
	// clamps to 0 and triggers eviction.
	t.Run("negative clamps to zero", func(t *testing.T) {
		client := newTestClient(512)
		info, infoHash := newTestInfo(256, 2)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		_, err = torrentImpl.Piece(info.Piece(0)).WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		require.NoError(t, client.SetMaxMemory(-100))

		client.mu.RLock()
		maxMem := client.maxMemory
		used := client.used
		client.mu.RUnlock()

		assert.Equal(t, int64(0), maxMem, "negative value should be clamped to 0")
		// With maxMemory=0, eviction deterministically removes all unprotected pieces.
		assert.Equal(t, int64(0), used, "used should be 0 after eviction to maxMemory=0")
	})
}

func TestClient_EvictLocked(t *testing.T) {
	// Pieces 0 and 1 are unprotected, 2 is a file boundary, and 3 is in an
	// active range; each level gives up its pieces only after the one before.
	t.Run("gives up protection levels in order", func(t *testing.T) {
		tests := []struct {
			name         string
			upTo         evictionProtection
			wantResident []int
		}{
			{name: "unprotected only", upTo: protectActiveAndBoundaries, wantResident: []int{2, 3}},
			{name: "boundaries before active ranges", upTo: protectActiveOnly, wantResident: []int{3}},
			{name: "active ranges last", upTo: protectNone},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				client := newTestClient(1024)
				info, infoHash := newTestInfo(256, 4)
				torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
				require.NoError(t, err)
				for i := range 4 {
					_, err := torrentImpl.Piece(info.Piece(i)).WriteAt(make([]byte, 256), 0)
					require.NoError(t, err)
				}
				client.SetProtection(infoHash, 1, Protection{
					Active:     []PieceRange{{Start: 3, End: 3}},
					Boundaries: []PieceRange{{Start: 2, End: 2}},
				})

				client.mu.Lock()
				client.evictLocked(0, tt.upTo, 0)
				client.mu.Unlock()

				assert.ElementsMatch(t, tt.wantResident, residentPieceIndexes(t, client, infoHash))
			})
		}
	})

	t.Run("returns one buffer of the reuse size", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		for i := range 3 {
			_, err := torrentImpl.Piece(info.Piece(i)).WriteAt(make([]byte, 256), 0)
			require.NoError(t, err)
		}

		client.mu.Lock()
		reusable := client.evictLocked(0, protectNone, 256)
		missed := client.evictLocked(0, protectNone, 128)
		client.mu.Unlock()
		assert.Len(t, reusable, 256)
		assert.Nil(t, missed, "nothing is left to reuse")
	})

	// Checks manual eviction down to a specific target.
	t.Run("evicts down to target", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write 3 pieces
		for i := range 3 {
			p := torrentImpl.Piece(info.Piece(i))
			_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
			require.NoError(t, err)
		}

		// Force evict down to 1 piece
		assert.Equal(t, int64(512), evictUnprotected(client, 256))

		stats, err := client.TorrentStats(infoHash)
		require.NoError(t, err)
		assert.Equal(t, 1, stats.ResidentPieces)
	})

	// Verifies that eviction does not
	// loop forever when the target is below what's achievable due to active ranges.
	t.Run("stops gracefully with protection", func(t *testing.T) {
		client := newTestClient(512) // 2 pieces
		info, infoHash := newTestInfo(256, 4)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Fill memory with pieces 0 and 1.
		for i := range 2 {
			p := torrentImpl.Piece(info.Piece(i))
			_, err := p.WriteAt(fmt.Appendf(nil, "p%d", i), 0)
			require.NoError(t, err)
		}

		client.SetProtection(infoHash, 1, activeProtection(0, 1))

		// Eviction must not loop when all pieces are protected.
		done := make(chan int64, 1)
		go func() { done <- evictUnprotected(client, 0) }()

		select {
		case reclaimed := <-done:
			assert.Zero(t, reclaimed)
		case <-time.After(2 * time.Second):
			t.Fatal("eviction hung with protected pieces exceeding target")
		}

		client.mu.RLock()
		used := client.used
		client.mu.RUnlock()
		assert.Equal(t, int64(512), used, "protected pieces should survive eviction")
	})
}

func TestPieceImpl_SelfHash(t *testing.T) {
	// Verifies that the piece can hash its own data correctly.
	t.Run("hashes piece data", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 1)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0))

		// Write data to the piece
		data := make([]byte, 256)
		copy(data, "some data")
		_, err = p.WriteAt(data, 0)
		require.NoError(t, err)

		selfHasher, ok := p.(storage.SelfHashing)
		require.True(t, ok)

		// Compute hash
		h, err := selfHasher.SelfHash()
		require.NoError(t, err)

		// Verify hash
		expectedHash := sha1.Sum(data)
		assert.Equal(t, expectedHash[:], h[:])
	})

	t.Run("missing piece returns error", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 1)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		selfHasher, ok := torrentImpl.Piece(info.Piece(0)).(storage.SelfHashing)
		require.True(t, ok)

		_, err = selfHasher.SelfHash()
		assert.ErrorIs(t, err, ErrPieceNotAvailable, "missing piece should return ErrPieceNotAvailable")
	})
}

func TestClient_AllocateMemory(t *testing.T) {
	// Ensures that writes fail when not enough memory is available.
	t.Run("fails when memory is exhausted", func(t *testing.T) {
		client := newTestClient(128) // Very small memory
		info, infoHash := newTestInfo(256, 1)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0))

		_, err = p.WriteAt([]byte("data"), 0)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInsufficientMemory)
	})

	// Ensures that a failed allocation
	// leaves c.used unchanged (no partial state leaked).
	t.Run("failure does not drift used memory", func(t *testing.T) {
		client := newTestClient(128)
		info, infoHash := newTestInfo(256, 2)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// maxMemory (128) is smaller than a single piece (256), so the very first
		// allocation attempt must fail — verifying it leaves no partial c.used state.
		p := torrentImpl.Piece(info.Piece(0))
		_, err = p.WriteAt([]byte("data"), 0)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInsufficientMemory)

		// c.used must not have increased.
		client.mu.RLock()
		used := client.used
		client.mu.RUnlock()
		assert.Equal(t, int64(0), used, "c.used should be 0 after failed allocation")
	})

	t.Run("waits for unpublished reservation", func(t *testing.T) {
		client := newTestClient(256)
		info, infoHash := newTestInfo(256, 2)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		piece := torrentImpl.Piece(info.Piece(1))

		// Hold the entire cache as an unpublished reservation. A concurrent piece
		// write cannot evict it yet, but it must wait rather than return the fatal
		// storage error used for genuinely impossible allocations.
		_, err = client.allocateMemory(256, infoHash, piece.(*pieceImpl).torrent)
		require.NoError(t, err)
		releaseReservation := sync.OnceFunc(func() {
			client.mu.Lock()
			client.releaseMemoryLocked(256, piece.(*pieceImpl).torrent)
			client.mu.Unlock()
			client.completeAllocation()
		})
		defer releaseReservation()

		writeDone := make(chan error, 1)
		go func() {
			_, writeErr := piece.WriteAt([]byte("data"), 0)
			writeDone <- writeErr
		}()

		deadline := time.After(time.Second)
		for {
			client.mu.RLock()
			pd := client.pieces[pieceKey{infoHash: infoHash, index: 1}]
			client.mu.RUnlock()
			waiting := false
			if pd != nil {
				pd.mu.RLock()
				waiting = pd.allocating
				pd.mu.RUnlock()
			}
			if waiting {
				break
			}
			select {
			case err := <-writeDone:
				t.Fatalf("write returned before reservation completed: %v", err)
			case <-deadline:
				t.Fatal("write did not wait for the unpublished reservation")
			default:
				runtime.Gosched()
			}
		}

		select {
		case err := <-writeDone:
			t.Fatalf("write returned while reservation was unpublished: %v", err)
		default:
		}

		releaseReservation()
		require.NoError(t, <-writeDone)
		assert.Equal(t, []int{1}, residentPieceIndexes(t, client, infoHash))
	})

	t.Run("evicts boundary before active range", func(t *testing.T) {
		client := newTestClient(512)
		info, infoHash := newTestInfo(256, 3)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		for i := range 2 {
			_, err = torrentImpl.Piece(info.Piece(i)).WriteAt(make([]byte, 256), 0)
			require.NoError(t, err)
		}
		client.SetProtection(infoHash, 1, activeProtection(0, 0))
		client.SetProtection(infoHash, 2, boundaryProtection(1, 1, 1, 1))

		_, err = torrentImpl.Piece(info.Piece(2)).WriteAt([]byte{1}, 0)
		require.NoError(t, err)

		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.Contains(t, inMemory, 0, "active piece should survive boundary pressure")
		assert.NotContains(t, inMemory, 1, "boundary-only piece should yield before active playback")
		assert.Contains(t, inMemory, 2)
	})

	t.Run("waits for reservation before evicting active range", func(t *testing.T) {
		client := newTestClient(512) // room for exactly 2 pieces
		info, infoHash := newTestInfo(256, 3)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p0 := torrentImpl.Piece(info.Piece(0))
		_, err = p0.WriteAt([]byte("data"), 0)
		require.NoError(t, err)
		client.SetProtection(infoHash, 1, activeProtection(0, 0))

		// Hold the remaining capacity as an unpublished reservation.
		state := p0.(*pieceImpl).torrent
		_, err = client.allocateMemory(256, infoHash, state)
		require.NoError(t, err)
		refundReservation := sync.OnceFunc(func() {
			client.mu.Lock()
			client.releaseMemoryLocked(256, state)
			client.mu.Unlock()
			client.completeAllocation()
		})
		defer refundReservation()

		writeDone := make(chan error, 1)
		go func() {
			_, writeErr := torrentImpl.Piece(info.Piece(2)).WriteAt([]byte("data"), 0)
			writeDone <- writeErr
		}()

		// The write must wait for the reservation instead of evicting the active piece.
		require.Eventually(t, func() bool {
			client.mu.RLock()
			pd := client.pieces[pieceKey{infoHash: infoHash, index: 2}]
			client.mu.RUnlock()
			if pd == nil {
				return false
			}
			pd.mu.RLock()
			defer pd.mu.RUnlock()
			return pd.allocating
		}, time.Second, time.Millisecond)
		select {
		case err := <-writeDone:
			t.Fatalf("write returned while reservation was unpublished: %v", err)
		default:
		}
		assert.Contains(t, residentPieceIndexes(t, client, infoHash), 0)

		refundReservation()
		select {
		case err := <-writeDone:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("write did not resume after the reservation was refunded")
		}
		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.Contains(t, inMemory, 0, "active piece was evicted despite refunded capacity")
		assert.Contains(t, inMemory, 2)
	})

	// Verifies that when protected pieces fill maxMemory, the allocator
	// emergency-evicts the oldest piece to allow incoming pieces to be written
	// without failing and halting torrent downloads.
	t.Run("active range emergency eviction", func(t *testing.T) {
		client := newTestClient(256) // room for exactly 1 piece
		info, infoHash := newTestInfo(256, 2)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		// Write piece 0 — uses all 256 bytes.
		p0 := torrentImpl.Piece(info.Piece(0))
		_, err = p0.WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		client.SetProtection(infoHash, 1, activeProtection(0, 0))

		// Write piece 1 — under memory pressure, piece 0 should be emergency-evicted.
		p1 := torrentImpl.Piece(info.Piece(1))
		_, err = p1.WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.Contains(t, inMemory, 1)
		assert.NotContains(t, inMemory, 0)
	})

	// Verifies that a piece exceeding
	// the total maxMemory limit returns ErrInsufficientMemory and leaves no ghost entries behind.
	t.Run("piece larger than max memory", func(t *testing.T) {
		client := newTestClient(128) // maxMemory is 128 bytes
		info, infoHash := newTestInfo(256, 2)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p0 := torrentImpl.Piece(info.Piece(0))
		_, err = p0.WriteAt([]byte("data"), 0)
		assert.ErrorIs(t, err, ErrInsufficientMemory)

		// Assert no ghost piece was left in client.pieces or client.lru.
		client.mu.RLock()
		defer client.mu.RUnlock()
		assert.Empty(t, client.pieces, "no ghost piece should remain in c.pieces after failed WriteAt")
		assert.Equal(t, 0, client.lru.Len(), "no ghost piece should remain in LRU after failed WriteAt")
	})
}

// TestClientConcurrentAccess stresses concurrent reads and writes for race conditions.
func TestClientConcurrentAccess(t *testing.T) {
	client := newTestClient(2048)
	info, infoHash := newTestInfo(256, 8)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 4
	pcs := make([]storage.PieceImpl, numGoroutines)
	for i := range numGoroutines {
		pcs[i] = torrentImpl.Piece(info.Piece(i))
	}

	wg.Add(numGoroutines)
	for i := range numGoroutines {
		go func(p storage.PieceImpl) {
			defer wg.Done()
			_, _ = p.WriteAt([]byte("data"), 0)
			_ = p.MarkComplete()
			buf := make([]byte, 4)
			_, _ = p.ReadAt(buf, 0)
		}(pcs[i])
	}

	wg.Wait()

	stats, err := client.TorrentStats(infoHash)
	require.NoError(t, err)

	assert.Equal(t, 8, stats.TotalPieces)
	assert.Equal(t, numGoroutines, stats.ResidentPieces)
}

// TestClientConcurrentMapIteration exercises concurrent map access and iteration.
func TestClientConcurrentMapIteration(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 10)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Continuously get torrent memory stats
	go func() {
		defer wg.Done()
		for range 100 {
			_, _ = client.TorrentStats(infoHash)
		}
	}()

	// Goroutine 2: Continuously add and remove pieces
	go func() {
		defer wg.Done()
		torrentImpl, _ := client.OpenTorrent(context.Background(), info, infoHash)
		for i := range 10 {
			p := torrentImpl.Piece(info.Piece(i))
			_, _ = p.WriteAt(make([]byte, 256), 0)
		}
	}()

	wg.Wait()
}

func TestClient_SetProtection(t *testing.T) {
	// writeTwoPieces fills a 512-byte client with pieces 0 and 1 of a
	// four-piece torrent, so the next write must evict one of them.
	writeTwoPieces := func(t *testing.T) (*Client, *metainfo.Info, metainfo.Hash, storage.TorrentImpl) {
		t.Helper()
		client := newTestClient(512)
		info, infoHash := newTestInfo(256, 4)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		for i := range 2 {
			_, err := torrentImpl.Piece(info.Piece(i)).WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
			require.NoError(t, err)
		}
		return client, info, infoHash, torrentImpl
	}
	writePiece2 := func(t *testing.T, info *metainfo.Info, torrentImpl storage.TorrentImpl) {
		t.Helper()
		_, err := torrentImpl.Piece(info.Piece(2)).WriteAt([]byte("piece_2"), 0)
		require.NoError(t, err)
	}

	// Pieces inside an active range survive eviction even when they are the
	// least recently used.
	t.Run("protects active pieces", func(t *testing.T) {
		client, info, infoHash, torrentImpl := writeTwoPieces(t)
		client.SetProtection(infoHash, 1, activeProtection(1, 1))

		writePiece2(t, info, torrentImpl)

		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.Contains(t, inMemory, 1, "protected piece should still be in memory")
		assert.Contains(t, inMemory, 2, "newly written piece should be in memory")
		assert.NotContains(t, inMemory, 0, "unprotected LRU piece should have been evicted")
	})

	t.Run("protects boundary pieces", func(t *testing.T) {
		client, info, infoHash, torrentImpl := writeTwoPieces(t)
		client.SetProtection(infoHash, 1, boundaryProtection(0, 0, 3, 3))

		writePiece2(t, info, torrentImpl)

		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.Contains(t, inMemory, 0, "head piece should be protected from eviction")
		assert.Contains(t, inMemory, 2, "newly written piece should be in memory")
		assert.NotContains(t, inMemory, 1, "unprotected middle piece should have been evicted")
	})

	t.Run("replaces the owner's ranges", func(t *testing.T) {
		client, info, infoHash, torrentImpl := writeTwoPieces(t)
		client.SetProtection(infoHash, 1, activeProtection(0, 0))
		client.SetProtection(infoHash, 1, activeProtection(1, 1))

		writePiece2(t, info, torrentImpl)

		inMemory := residentPieceIndexes(t, client, infoHash)
		assert.Contains(t, inMemory, 1, "the new range should be protected")
		assert.NotContains(t, inMemory, 0, "the replaced range should no longer be protected")
	})

	t.Run("tracks owners independently", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 8)
		_, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		count := func() int {
			client.mu.RLock()
			defer client.mu.RUnlock()
			return len(client.protections)
		}

		client.SetProtection(infoHash, 10, activeProtection(0, 2))
		client.SetProtection(infoHash, 20, activeProtection(5, 7))
		assert.Equal(t, 2, count())
		client.ClearProtection(infoHash, 10)
		assert.Equal(t, 1, count())
		client.SetProtection(infoHash, 20, Protection{})
		assert.Equal(t, 0, count(), "an empty protection clears the owner's")
	})

	t.Run("ignores a torrent it does not manage", func(t *testing.T) {
		client := newTestClient(1024)
		client.SetProtection(metainfo.Hash{1}, 1, activeProtection(0, 1))
		client.mu.RLock()
		defer client.mu.RUnlock()
		assert.Empty(t, client.protections)
	})

	t.Run("keeps its own copy of the ranges", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 8)
		_, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		protection := activeProtection(2, 3)
		client.SetProtection(infoHash, 1, protection)
		protection.Active[0] = PieceRange{Start: 6, End: 7}

		client.mu.RLock()
		defer client.mu.RUnlock()
		assert.True(t, inActiveRange(client, pieceKey{infoHash: infoHash, index: 2}))
		assert.False(t, inActiveRange(client, pieceKey{infoHash: infoHash, index: 6}))
	})
}

// TestClient_ClearProtection verifies that clearing an owner's protection
// allows its previously protected pieces to be evicted.
func TestClient_ClearProtection(t *testing.T) {
	for name, protection := range map[string]Protection{
		"active range":    activeProtection(1, 1),
		"file boundaries": boundaryProtection(0, 0, 3, 3),
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(512)
			info, infoHash := newTestInfo(256, 4)
			torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
			require.NoError(t, err)
			for i := range 2 {
				_, err := torrentImpl.Piece(info.Piece(i)).WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
				require.NoError(t, err)
			}

			client.SetProtection(infoHash, 1, protection)
			client.ClearProtection(infoHash, 1)

			// Trigger eviction; no piece is protected any more.
			_, err = torrentImpl.Piece(info.Piece(2)).WriteAt([]byte("piece_2"), 0)
			require.NoError(t, err)

			inMemory := residentPieceIndexes(t, client, infoHash)
			assert.Contains(t, inMemory, 2, "newly written piece should be in memory")
			assert.NotContains(t, inMemory, 0, "the LRU piece should have been evicted")
		})
	}
}

// TestClient_PieceProtectionLocked verifies the piece-in-range checks directly.
func TestClient_PieceProtectionLocked(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 8)
	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	client.SetProtection(infoHash, 1, Protection{
		Active:     []PieceRange{{Start: 2, End: 3}},
		Boundaries: []PieceRange{{Start: 0, End: 0}, {Start: 7, End: 7}},
	})
	client.SetProtection(infoHash, 2, activeProtection(5, 5))
	client.SetProtection(infoHash, 3, boundaryProtection(3, 3, 3, 3))

	client.mu.RLock()
	defer client.mu.RUnlock()
	for index, want := range []struct{ active, boundary bool }{
		0: {boundary: true},
		1: {},
		2: {active: true},
		3: {active: true, boundary: true},
		4: {},
		5: {active: true},
		6: {},
		7: {boundary: true},
	} {
		active, boundary := client.pieceProtectionLocked(pieceKey{infoHash: infoHash, index: index})
		assert.Equal(t, want.active, active, "piece %d active", index)
		assert.Equal(t, want.boundary, boundary, "piece %d boundary", index)
	}
	active, boundary := client.pieceProtectionLocked(pieceKey{infoHash: metainfo.Hash{9}, index: 2})
	assert.False(t, active || boundary, "another torrent's ranges must not protect a piece")
}

func TestPieceImpl_WriteAt(t *testing.T) {
	// Ensures that multiple goroutines
	// racing to write the same never-before-allocated piece don't cause c.used
	// to drift from actual allocated memory (regression test for the double
	// allocation race in ensureDataAllocated / freeMemory).
	t.Run("concurrent writes to same piece do not drift memory", func(t *testing.T) {
		client := newTestClient(1024)
		info, infoHash := newTestInfo(256, 1)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0))

		const numGoroutines = 20
		var wg sync.WaitGroup
		wg.Add(numGoroutines)
		for range numGoroutines {
			go func() {
				defer wg.Done()
				_, _ = p.WriteAt([]byte("x"), 0)
			}()
		}
		wg.Wait()

		client.mu.RLock()
		used := client.used
		client.mu.RUnlock()

		// Only one 256-byte piece was ever allocated, regardless of how many
		// goroutines raced to allocate it.
		assert.Equal(t, int64(256), used, "c.used should match actual allocated memory")
	})

	// Verifies that concurrent writes
	// to the same piece don't corrupt the underlying data slice. Each goroutine
	// writes a unique byte value to a distinct offset; after all writes complete,
	// every byte is checked for correctness.
	t.Run("concurrent writes to same piece keep data intact", func(t *testing.T) {
		client := newTestClient(4096)
		info, infoHash := newTestInfo(256, 1)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0))

		const numGoroutines = 20
		var wg sync.WaitGroup
		wg.Add(numGoroutines)
		for i := range numGoroutines {
			go func(idx int) {
				defer wg.Done()
				buf := make([]byte, 1)
				buf[0] = byte(idx)
				_, _ = p.WriteAt(buf, int64(idx%256))
			}(i)
		}
		wg.Wait()

		// Verify every written byte landed at the correct offset.
		buf := make([]byte, numGoroutines)
		n, err := p.ReadAt(buf, 0)
		assert.NoError(t, err)
		assert.Equal(t, numGoroutines, n)
		for idx := range numGoroutines {
			assert.Equal(t, byte(idx), buf[idx],
				"byte written by goroutine %d was lost or corrupted", idx)
		}
	})

	t.Run("concurrent first allocation under tight limit", func(t *testing.T) {
		client := newTestClient(256)
		info, infoHash := newTestInfo(256, 1)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)
		pd, err := p.getOrCreatePieceData()
		require.NoError(t, err)

		const writers = 20
		errs := make(chan error, writers)
		client.mu.Lock() // Hold reservations so all writers converge on one allocator.
		for range writers {
			go func() {
				errs <- p.ensureDataAllocated(pd)
			}()
		}
		time.Sleep(20 * time.Millisecond)
		client.mu.Unlock()

		for range writers {
			assert.NoError(t, <-errs)
		}
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
	})

	t.Run("orphaned piece race", func(t *testing.T) {
		client := newTestClient(1024)
		defer client.Close()

		info, infoHash := newTestInfo(256, 1)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)

		// Simulate G1: accesses the piece, but allocation fails and it cleans up the piece.
		orphanedPd, err := p.getOrCreatePieceData()
		require.NoError(t, err)
		p.cleanupEmptyPiece(orphanedPd)

		// orphanedPd should now be marked evicted and deleted from tracking.
		orphanedPd.mu.RLock()
		assert.True(t, orphanedPd.evicted)
		orphanedPd.mu.RUnlock()

		client.mu.RLock()
		_, exists := client.pieces[p.key()]
		assert.False(t, exists)
		client.mu.RUnlock()

		// Simulate G2: calls WriteAt concurrently.
		// It should detect the evicted/unlinked piece, recover with a new pieceData,
		// and successfully complete the write without memory accounting drift.
		data := []byte("hello world")
		n, err := p.WriteAt(data, 0)
		require.NoError(t, err)
		assert.Equal(t, len(data), n)

		// Memory usage should exactly equal one piece (256 bytes), no leak.
		assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
		assert.Equal(t, int64(256), requireTorrentStats(t, client, infoHash).ResidentBytes)

		// The piece should be accessible from the client map.
		client.mu.RLock()
		activePd, exists := client.pieces[p.key()]
		assert.True(t, exists)
		assert.NotEqual(t, orphanedPd, activePd)
		client.mu.RUnlock()

		// Subsequent ReadAt should succeed.
		buf := make([]byte, len(data))
		rn, err := p.ReadAt(buf, 0)
		require.NoError(t, err)
		assert.Equal(t, len(data), rn)
		assert.Equal(t, data, buf)
	})
}

func TestPieceData_RecordWrittenRange(t *testing.T) {
	tests := []struct {
		name   string
		writes [][2]int64
		want   []byteRange
	}{
		{name: "empty write ignored", writes: [][2]int64{{5, 5}}, want: nil},
		{name: "sequential writes merge", writes: [][2]int64{{0, 4}, {4, 8}, {8, 12}}, want: []byteRange{{0, 12}}},
		{name: "gap kept sorted", writes: [][2]int64{{8, 12}, {0, 4}}, want: []byteRange{{0, 4}, {8, 12}}},
		{name: "insert between", writes: [][2]int64{{0, 2}, {10, 12}, {5, 6}}, want: []byteRange{{0, 2}, {5, 6}, {10, 12}}},
		{name: "overlap extends", writes: [][2]int64{{2, 6}, {4, 10}}, want: []byteRange{{2, 10}}},
		{name: "bridge several", writes: [][2]int64{{0, 2}, {4, 6}, {8, 10}, {1, 9}}, want: []byteRange{{0, 10}}},
		{name: "duplicate write", writes: [][2]int64{{3, 7}, {3, 7}}, want: []byteRange{{3, 7}}},
		{name: "contained write", writes: [][2]int64{{0, 10}, {2, 4}}, want: []byteRange{{0, 10}}},
		{name: "touch previous start", writes: [][2]int64{{4, 8}, {0, 4}}, want: []byteRange{{0, 8}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := &pieceData{}
			for _, write := range tt.writes {
				pd.recordWrittenRange(write[0], write[1])
			}
			assert.Equal(t, tt.want, pd.writtenRanges)
			var wantBytes int64
			for _, r := range tt.want {
				wantBytes += r.end - r.start
			}
			assert.Equal(t, wantBytes, pd.writtenBytes)
		})
	}

	t.Run("sequential writes do not allocate", func(t *testing.T) {
		pd := &pieceData{}
		pd.recordWrittenRange(0, 16)
		offset := int64(16)
		allocs := testing.AllocsPerRun(100, func() {
			pd.recordWrittenRange(offset, offset+16)
			offset += 16
		})
		assert.Zero(t, allocs)
		assert.Len(t, pd.writtenRanges, 1)
	})
}

func TestPieceData_RangeWritten(t *testing.T) {
	pd := &pieceData{}
	pd.recordWrittenRange(0, 10)
	pd.recordWrittenRange(20, 30)
	tests := []struct {
		name       string
		start, end int64
		want       bool
	}{
		{name: "empty range", start: 15, end: 15, want: true},
		{name: "whole first range", start: 0, end: 10, want: true},
		{name: "inside second range", start: 22, end: 28, want: true},
		{name: "ends at range end", start: 25, end: 30, want: true},
		{name: "inside gap", start: 12, end: 18, want: false},
		{name: "straddles gap", start: 5, end: 25, want: false},
		{name: "runs past last range", start: 25, end: 31, want: false},
		{name: "starts at gap", start: 10, end: 12, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, pd.rangeWritten(tt.start, tt.end))
		})
	}
}

func TestClient_Close(t *testing.T) {
	t.Run("memory controls after close", func(t *testing.T) {
		client := newTestClient(512)
		require.NoError(t, client.Close())

		assert.ErrorIs(t, client.SetMaxMemory(1), ErrClientClosed)
	})

	t.Run("is idempotent", func(t *testing.T) {
		client := newTestClient(512)
		info, infoHash := newTestInfo(256, 1)

		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)

		_, err = torrentImpl.Piece(info.Piece(0)).WriteAt([]byte("data"), 0)
		require.NoError(t, err)

		// Close first time.
		err = client.Close()
		require.NoError(t, err)

		// Close second time: should not panic on channel close.
		err = client.Close()
		require.NoError(t, err)

		// Post-close operations should fail with ErrClientClosed.
		_, err = client.OpenTorrent(context.Background(), info, infoHash)
		assert.ErrorIs(t, err, ErrClientClosed)

		_, err = torrentImpl.Piece(info.Piece(0)).WriteAt([]byte("data"), 0)
		assert.ErrorIs(t, err, ErrClientClosed)
	})

	t.Run("waits for unpublished reservation", func(t *testing.T) {
		client := newTestClient(512)
		info, infoHash := newTestInfo(256, 1)
		torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
		require.NoError(t, err)
		p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)

		// A reservation can be in use while its buffer is being initialized,
		// even when no piece entry remains in client.pieces.
		_, err = client.allocateMemory(256, infoHash, p.torrent)
		require.NoError(t, err)
		complete := sync.OnceFunc(func() {
			client.mu.Lock()
			client.releaseMemoryLocked(256, p.torrent)
			client.mu.Unlock()
			client.completeAllocation()
		})
		defer complete()

		firstDone := make(chan error, 1)
		go func() { firstDone <- client.Close() }()
		deadline := time.After(time.Second)
		for {
			client.mu.RLock()
			closing := client.closed
			client.mu.RUnlock()
			if closing {
				break
			}
			select {
			case <-deadline:
				t.Fatal("Close did not begin")
			default:
				runtime.Gosched()
			}
		}

		secondDone := make(chan error, 1)
		go func() { secondDone <- client.Close() }()
		select {
		case <-client.Closed():
			t.Fatal("Closed signaled with an unpublished reservation")
		default:
		}
		select {
		case <-firstDone:
			t.Fatal("first Close returned with an unpublished reservation")
		case <-secondDone:
			t.Fatal("second Close returned before cleanup completed")
		default:
		}

		complete()
		require.NoError(t, <-firstDone)
		require.NoError(t, <-secondDone)
		select {
		case <-client.Closed():
		default:
			t.Fatal("Closed was not signaled after cleanup")
		}
		assert.Zero(t, client.MemoryStats().UsedBytes)
	})
}

// TestClient_Closed verifies that Closed signals only after cleanup.
func TestClient_Closed(t *testing.T) {
	client := newTestClient(512)
	info, infoHash := newTestInfo(256, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)
	p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)
	_, err = p.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	pd, err := p.getPieceData()
	require.NoError(t, err)

	// Keep cleanup blocked on the resident piece while Close holds client.mu.
	pd.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	deadline := time.After(time.Second)
	for client.mu.TryLock() {
		client.mu.Unlock()
		select {
		case <-deadline:
			pd.mu.Unlock()
			<-done
			t.Fatal("Close did not start")
		default:
			runtime.Gosched()
		}
	}
	signaledBeforeCleanup := false
	select {
	case <-client.Closed():
		signaledBeforeCleanup = true
	default:
	}
	pd.mu.Unlock()
	require.NoError(t, <-done)
	assert.False(t, signaledBeforeCleanup, "Closed must wait until resident pieces are evicted")
	select {
	case <-client.Closed():
	default:
		t.Fatal("Closed was not signaled after Close returned")
	}
}

func TestStalePieceCannotRecreateDataAfterTorrentClose(t *testing.T) {
	client := newTestClient(512)
	info, infoHash := newTestInfo(256, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)
	p := torrentImpl.Piece(info.Piece(0))

	require.NoError(t, torrentImpl.Close())
	_, err = p.WriteAt([]byte("data"), 0)
	assert.ErrorIs(t, err, ErrTorrentClosed)
	assert.Equal(t, int64(0), client.MemoryStats().UsedBytes)
	assert.Empty(t, client.pieces)
}

// TestPieceImpl_ReadAt verifies that a read miss does not create a ghost piece.
func TestPieceImpl_ReadAt(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 2)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	buf := make([]byte, 10)
	n, err := torrentImpl.Piece(info.Piece(0)).ReadAt(buf, 0)
	assert.ErrorIs(t, err, ErrPieceNotAvailable)
	assert.Equal(t, 0, n)

	// Check that no ghost pieceData was inserted into pieces map or LRU list.
	client.mu.RLock()
	piecesCount := len(client.pieces)
	lruLen := client.lru.Len()
	client.mu.RUnlock()

	assert.Equal(t, 0, piecesCount, "no ghost piece should be added to pieces map")
	assert.Equal(t, 0, lruLen, "no ghost element should be added to LRU list")
}

func TestPieceImplRejectsReadAndHashOfPartiallyWrittenPiece(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 1)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)
	p := torrentImpl.Piece(info.Piece(0))
	selfHasher, ok := p.(storage.SelfHashing)
	require.True(t, ok)

	data := make([]byte, 256)
	copy(data, "first chunk")
	copy(data[128:], "second chunk")

	// The first chunk is lost when the in-progress piece is evicted, and the
	// second chunk lands in a fresh zeroed buffer.
	_, err = p.WriteAt(data[:128], 0)
	require.NoError(t, err)
	evictUnprotected(client, 0)
	_, err = p.WriteAt(data[128:], 128)
	require.NoError(t, err)

	_, err = selfHasher.SelfHash()
	require.ErrorIs(t, err, ErrPieceIncomplete, "a buffer missing evicted chunks must not hash as peer data")
	_, err = p.ReadAt(make([]byte, 256), 0)
	require.ErrorIs(t, err, ErrPieceIncomplete, "reads must not return evicted chunks as zeros")
	_, err = p.ReadAt(make([]byte, 64), 120)
	require.ErrorIs(t, err, ErrPieceIncomplete, "a read straddling the gap must fail")
	got := make([]byte, 128)
	n, err := p.ReadAt(got, 128)
	require.NoError(t, err, "written chunks remain readable")
	assert.Equal(t, data[128:], got[:n])

	// Once the lost chunk is downloaded again the piece hashes correctly.
	_, err = p.WriteAt(data[:128], 0)
	require.NoError(t, err)
	h, err := selfHasher.SelfHash()
	require.NoError(t, err)
	expected := sha1.Sum(data)
	assert.Equal(t, expected[:], h[:])
}

func TestPieceCompletionAndHashAcrossLifecycle(t *testing.T) {
	client := newTestClient(256) // 1 piece capacity
	info, infoHash := newTestInfo(256, 2)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p0 := torrentImpl.Piece(info.Piece(0))
	p1 := torrentImpl.Piece(info.Piece(1))

	h0, ok := p0.(storage.SelfHashing)
	require.True(t, ok)

	// State 1: Untracked (never written)
	c0 := p0.Completion()
	assert.False(t, c0.Complete, "untracked piece must be Complete: false")
	assert.True(t, c0.Ok, "untracked piece must be Ok: true")
	assert.NoError(t, c0.Err, "untracked piece must have Err: nil so downloads aren't disabled")

	_, err = h0.SelfHash()
	assert.ErrorIs(t, err, ErrPieceNotAvailable, "untracked piece must return ErrPieceNotAvailable on SelfHash")

	// State 2: In-flight (written/allocated, but not marked complete)
	data0 := make([]byte, 256)
	copy(data0, "hello piece 0")
	_, err = p0.WriteAt(data0, 0)
	require.NoError(t, err)

	c0 = p0.Completion()
	assert.False(t, c0.Complete, "in-flight piece must be Complete: false")
	assert.True(t, c0.Ok, "in-flight piece must be Ok: true")
	assert.NoError(t, c0.Err)

	hash0, err := h0.SelfHash()
	require.NoError(t, err, "in-flight piece with data must SelfHash successfully")
	expected0 := sha1.Sum(data0)
	assert.Equal(t, expected0[:], hash0[:])

	// State 3: Complete (marked complete)
	err = p0.MarkComplete()
	require.NoError(t, err)

	c0 = p0.Completion()
	assert.True(t, c0.Complete, "completed piece must be Complete: true")
	assert.True(t, c0.Ok, "completed piece must be Ok: true")
	assert.NoError(t, c0.Err)

	// State 4: Evicted (allocating piece 1 forces eviction of piece 0)
	data1 := make([]byte, 256)
	copy(data1, "hello piece 1")
	_, err = p1.WriteAt(data1, 0)
	require.NoError(t, err)

	// Piece 0 was evicted
	c0 = p0.Completion()
	assert.False(t, c0.Complete, "evicted piece must be Complete: false so client re-downloads")
	assert.True(t, c0.Ok, "evicted piece must be Ok: true")
	assert.NoError(t, c0.Err, "evicted piece must have Err: nil")

	_, err = h0.SelfHash()
	assert.ErrorIs(t, err, ErrPieceNotAvailable, "evicted piece must return ErrPieceNotAvailable on SelfHash")
}

// TestPieceImpl_TouchPiece verifies that touchPiece throttles updates and tolerates clock skew.
func TestPieceImpl_TouchPiece(t *testing.T) {
	client := newTestClient(10 * 1024 * 1024)
	defer client.Close()

	info, infoHash := newTestInfo(1024, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)

	data := []byte("testing piece touch")
	_, err = p.WriteAt(data, 0)
	require.NoError(t, err)

	pd, err := p.getPieceData()
	require.NoError(t, err)
	require.NotNil(t, pd)

	// After WriteAt, pd.lastTouchNano is already initialized
	initialTouch := pd.lastTouchNano.Load()
	assert.Positive(t, initialTouch)

	// An immediate touch should be throttled (no CAS update)
	p.touchPiece(pd)
	assert.Equal(t, initialTouch, pd.lastTouchNano.Load())

	// Simulate clock stepping backwards (skew): now < last
	pd.lastTouchNano.Store(time.Now().Add(10 * time.Minute).UnixNano())
	skewTouch := pd.lastTouchNano.Load()
	p.touchPiece(pd)
	// Clock skew guard should detect now < last and update timestamp
	assert.Less(t, pd.lastTouchNano.Load(), skewTouch)

	// Simulate time elapsed > touchMinInterval
	pd.lastTouchNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	elapsedTouch := pd.lastTouchNano.Load()
	p.touchPiece(pd)
	assert.Greater(t, pd.lastTouchNano.Load(), elapsedTouch)
}

func BenchmarkPieceImpl_ReadAt(b *testing.B) {
	client := newBenchmarkClient(64 * 1024 * 1024)
	defer client.Close()

	const pieceSize = 1024 * 1024 // 1 MB piece
	info, infoHash := newTestInfo(pieceSize, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}

	p := torrentImpl.Piece(info.Piece(0))
	data := make([]byte, pieceSize)
	if _, err := p.WriteAt(data, 0); err != nil {
		b.Fatal(err)
	}

	readBuf := make([]byte, 16*1024) // 16 KB read chunks
	b.SetBytes(int64(len(readBuf)))
	b.ResetTimer()

	var off int64
	for range b.N {
		if off+int64(len(readBuf)) > pieceSize {
			off = 0
		}
		_, err := p.ReadAt(readBuf, off)
		if err != nil {
			b.Fatal(err)
		}
		off += int64(len(readBuf))
	}
}

func BenchmarkPieceImpl_ReadAt_Parallel(b *testing.B) {
	client := newBenchmarkClient(64 * 1024 * 1024)
	defer client.Close()

	const pieceSize = 1024 * 1024
	info, infoHash := newTestInfo(pieceSize, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}

	p := torrentImpl.Piece(info.Piece(0))
	data := make([]byte, pieceSize)
	if _, err := p.WriteAt(data, 0); err != nil {
		b.Fatal(err)
	}

	b.SetBytes(16 * 1024)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, 16*1024)
		var off int64
		for pb.Next() {
			if off+int64(len(buf)) > pieceSize {
				off = 0
			}
			_, err := p.ReadAt(buf, off)
			if err != nil {
				b.Fatal(err)
			}
			off += int64(len(buf))
		}
	})
}

func BenchmarkPieceImpl_WriteAt_Existing(b *testing.B) {
	client := newBenchmarkClient(64 * 1024 * 1024)
	defer client.Close()

	const pieceSize = 1024 * 1024
	const writeSize = 16 * 1024
	info, infoHash := newTestInfo(pieceSize, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}

	p := torrentImpl.Piece(info.Piece(0))
	data := make([]byte, pieceSize)
	if _, err := p.WriteAt(data, 0); err != nil {
		b.Fatal(err)
	}
	writeBuf := make([]byte, writeSize)

	b.SetBytes(writeSize)
	b.ReportAllocs()
	b.ResetTimer()

	var off int64
	for range b.N {
		if off+writeSize > pieceSize {
			off = 0
		}
		if _, err := p.WriteAt(writeBuf, off); err != nil {
			b.Fatal(err)
		}
		off += writeSize
	}
}

func BenchmarkPieceImpl_WriteAt_FirstAllocationAndEviction(b *testing.B) {
	const pieceSize = 64 * 1024
	const memoryLimit = 1024 * 1024
	client := newBenchmarkClient(memoryLimit)
	defer client.Close()

	info, infoHash := newTestInfo(pieceSize, max(b.N, 1))
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}
	data := make([]byte, pieceSize)

	b.SetBytes(pieceSize)
	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		if _, err := torrentImpl.Piece(info.Piece(i)).WriteAt(data, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPieceImpl_WriteAt_ContendedFirstAllocation(b *testing.B) {
	const pieceSize = 64 * 1024
	const memoryLimit = 16 * 1024 * 1024
	const writers = 8
	client := newBenchmarkClient(memoryLimit)
	defer client.Close()

	info, infoHash := newTestInfo(pieceSize, max(b.N, 1))
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}
	data := make([]byte, pieceSize)

	b.SetBytes(pieceSize * writers)
	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		p := torrentImpl.Piece(info.Piece(i))
		start := make(chan struct{})
		errs := make([]error, writers)
		var wg sync.WaitGroup
		wg.Add(writers)
		for writer := range writers {
			go func(writer int) {
				defer wg.Done()
				<-start
				_, errs[writer] = p.WriteAt(data, 0)
			}(writer)
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkClient_RefillAndSetMaxMemoryEvict(b *testing.B) {
	const pieceSize = 64 * 1024
	const pieceCount = 16
	const highLimit = pieceSize * pieceCount
	const lowLimit = highLimit / 2
	client := newBenchmarkClient(highLimit)
	defer client.Close()

	info, infoHash := newTestInfo(pieceSize, pieceCount)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}
	data := make([]byte, pieceSize)
	fill := func() {
		for i := range pieceCount {
			if _, err := torrentImpl.Piece(info.Piece(i)).WriteAt(data, 0); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := client.SetMaxMemory(highLimit); err != nil {
			b.Fatal(err)
		}
		fill()
		if err := client.SetMaxMemory(lowLimit); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClient_MemoryStats(b *testing.B) {
	const pieceSize = 64 * 1024
	const pieceCount = 256
	client := newBenchmarkClient(pieceSize * pieceCount)
	defer client.Close()

	info, infoHash := newTestInfo(pieceSize, pieceCount)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}
	data := make([]byte, pieceSize)
	for i := range pieceCount {
		if _, err := torrentImpl.Piece(info.Piece(i)).WriteAt(data, 0); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("Global", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = client.MemoryStats()
		}
	})
	b.Run("TorrentDetailed", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := client.TorrentStats(infoHash); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPieceImpl_TouchPiece(b *testing.B) {
	client := newBenchmarkClient(64 * 1024 * 1024)
	defer client.Close()

	const pieceSize = 1024 * 1024
	info, infoHash := newTestInfo(pieceSize, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}

	p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)
	data := make([]byte, pieceSize)
	if _, err := p.WriteAt(data, 0); err != nil {
		b.Fatal(err)
	}
	pd, err := p.getPieceData()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for range b.N {
		p.touchPiece(pd)
	}
}

func BenchmarkPieceImpl_TouchPiece_Parallel(b *testing.B) {
	client := newBenchmarkClient(64 * 1024 * 1024)
	defer client.Close()

	const pieceSize = 1024 * 1024
	info, infoHash := newTestInfo(pieceSize, 1)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		b.Fatal(err)
	}

	p := torrentImpl.Piece(info.Piece(0)).(*pieceImpl)
	data := make([]byte, pieceSize)
	if _, err := p.WriteAt(data, 0); err != nil {
		b.Fatal(err)
	}
	pd, err := p.getPieceData()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.touchPiece(pd)
		}
	})
}

// activeProtection returns a protection of the active range [start, end].
func activeProtection(start, end int) Protection {
	return Protection{Active: []PieceRange{{Start: start, End: end}}}
}

// boundaryProtection returns a protection of the file boundaries
// [headStart, headEnd] and [tailStart, tailEnd].
func boundaryProtection(headStart, headEnd, tailStart, tailEnd int) Protection {
	return Protection{Boundaries: []PieceRange{{Start: headStart, End: headEnd}, {Start: tailStart, End: tailEnd}}}
}

// inActiveRange reports whether key lies in a registered active range. The
// caller must hold client.mu.
func inActiveRange(client *Client, key pieceKey) bool {
	active, _ := client.pieceProtectionLocked(key)
	return active
}

// inFileBoundary reports whether key lies in a registered file boundary. The
// caller must hold client.mu.
func inFileBoundary(client *Client, key pieceKey) bool {
	_, boundary := client.pieceProtectionLocked(key)
	return boundary
}

// evictUnprotected evicts pieces outside every protected range until memory
// usage is at most target, and returns the bytes reclaimed.
func evictUnprotected(client *Client, target int64) int64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	before := client.used
	client.evictLocked(target, protectActiveAndBoundaries, 0)
	return before - client.used
}
