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
	"sync"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a new client for testing with a specified memory limit.
func newTestClient(maxMemory int64) *Client {
	return NewClient(maxMemory, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// newTestInfo creates a dummy torrent info and hash for testing.
func newTestInfo(pieceLength int64, numPieces int) (*metainfo.Info, metainfo.Hash) {
	info := &metainfo.Info{
		PieceLength: pieceLength,
		Pieces:      make([]byte, 20*numPieces),
		Name:        "test_torrent",
		Length:      pieceLength * int64(numPieces),
	}
	for i := 0; i < numPieces; i++ {
		hash := sha1.Sum([]byte(fmt.Sprintf("piece_%d", i)))
		copy(info.Pieces[i*20:(i+1)*20], hash[:])
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	infoHash := metainfo.Hash(sha1.Sum(b))
	return info, infoHash
}

// TestClient_OpenTorrent verifies that a new torrent is correctly initialized and tracked.
func TestClient_OpenTorrent(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	client.mu.RLock()
	defer client.mu.RUnlock()

	assert.NotNil(t, client.torrents[infoHash])
	assert.Len(t, client.torrents[infoHash].pieceHashes, 4)
}

// TestClient_CloseTorrent ensures that closing a torrent removes its data and state.
func TestClient_CloseTorrent(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Add a piece to the client
	key := pieceKey{hash: infoHash, index: 0}
	client.pieces[key] = &pieceData{data: make([]byte, 256)}

	err = client.closeTorrent(infoHash)
	require.NoError(t, err)

	client.mu.RLock()
	defer client.mu.RUnlock()

	assert.Nil(t, client.torrents[infoHash])
	assert.Empty(t, client.pieces)
}

// TestClient_GetTorrentMemoryStats checks that torrent-specific memory statistics are accurate.
func TestClient_GetTorrentMemoryStats(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Add some pieces
	client.pieces[pieceKey{hash: infoHash, index: 0}] = &pieceData{data: make([]byte, 256), complete: true, pieceSize: 256}
	client.pieces[pieceKey{hash: infoHash, index: 1}] = &pieceData{data: make([]byte, 256), complete: false, pieceSize: 256}

	stats, err := client.GetTorrentMemoryStats(infoHash)
	require.NoError(t, err)

	assert.Equal(t, 4, stats.TotalPieces)
	assert.Equal(t, int64(512), stats.TotalSize)
	assert.Equal(t, int64(256), stats.CompletedSize)
	assert.Equal(t, 2, stats.InMemory)
}

// TestPieceImpl_ReadWrite tests basic read and write operations on a piece.
func TestPieceImpl_ReadWrite(t *testing.T) {
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

// TestPieceImpl_MarkCompletion verifies the logic for marking pieces as complete or not complete.
func TestPieceImpl_MarkCompletion(t *testing.T) {
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
}

// TestMemoryEviction simulates memory pressure to ensure the LRU eviction policy works.
func TestMemoryEviction(t *testing.T) {
	client := newTestClient(512) // Max memory for 2 pieces of 256 bytes
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Write 3 pieces, causing eviction of the first one.
	for i := 0; i < 3; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
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
}

// TestClient_GetCompletedProgress checks the calculation of completion percentage.
func TestClient_GetCompletedProgress(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Complete 2 out of 4 pieces
	for i := 0; i < 2; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
		err = p.MarkComplete()
		require.NoError(t, err)
	}

	progress := client.GetCompletedProgress(infoHash)
	assert.InDelta(t, 50.0, progress*100, 0.1)
}

// TestClient_GetMemoryUsageProgress verifies the calculation of memory usage percentage.
func TestClient_GetMemoryUsageProgress(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Write 2 pieces
	for i := 0; i < 2; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
	}

	progress := client.GetMemoryUsageProgress(infoHash)
	assert.InDelta(t, 50.0, progress*100, 0.1)
}

// TestClient_SetMaxMemory ensures that dynamically changing the memory limit triggers eviction.
func TestClient_SetMaxMemory(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Write 3 pieces
	for i := 0; i < 3; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
	}

	// Reduce memory, triggering eviction
	client.SetMaxMemory(512)

	stats, err := client.GetTorrentMemoryStats(infoHash)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.InMemory)
}

// TestClient_ForceEvict checks manual eviction down to a specific target.
func TestClient_ForceEvict(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Write 3 pieces
	for i := 0; i < 3; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
	}

	// Force evict down to 1 piece
	client.ForceEvict(256)

	stats, err := client.GetTorrentMemoryStats(infoHash)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.InMemory)
}

// TestClient_GetPiecesInMemory verifies that the list of in-memory pieces is correct.
func TestClient_GetPiecesInMemory(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Write 2 pieces
	for i := 0; i < 2; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
	}

	inMemory := client.GetPiecesInMemory(infoHash)
	assert.ElementsMatch(t, []int{0, 1}, inMemory)
}

// TestClient_GetIncompletePieces confirms that the list of incomplete pieces is accurate.
func TestClient_GetIncompletePieces(t *testing.T) {
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

	incomplete := client.GetIncompletePieces(infoHash)
	assert.ElementsMatch(t, []int{1}, incomplete)
}

// TestClient_GetCompletedPieces ensures the list of completed pieces is correct.
func TestClient_GetCompletedPieces(t *testing.T) {
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

	completed := client.GetCompletedPieces(infoHash)
	assert.ElementsMatch(t, []int{0}, completed)
}

// TestClient_GetPieceStatus checks that the status of an individual piece is reported correctly.
func TestClient_GetPieceStatus(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))
	_, err = p.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	err = p.MarkComplete()
	require.NoError(t, err)

	status := client.GetPieceStatus(infoHash, 0)
	require.NotNil(t, status)

	assert.True(t, status.Complete)
	assert.True(t, status.InMemory)
	assert.Equal(t, 0, status.Index)
	assert.Equal(t, int64(256), status.Size)
}

// TestSelfHash verifies that the piece can hash its own data correctly.
func TestSelfHash(t *testing.T) {
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
}

// TestMemoryAllocationFailure ensures that writes fail when not enough memory is available.
func TestMemoryAllocationFailure(t *testing.T) {
	client := newTestClient(128) // Very small memory
	info, infoHash := newTestInfo(256, 1)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))

	_, err = p.WriteAt([]byte("data"), 0)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientMemory)
}

// TestConcurrentAccess stresses the client with concurrent reads and writes to check for race conditions.
func TestConcurrentAccess(t *testing.T) {
	client := newTestClient(2048)
	info, infoHash := newTestInfo(256, 8)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 4
	pcs := make([]storage.PieceImpl, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		pcs[i] = torrentImpl.Piece(info.Piece(i))
	}

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(p storage.PieceImpl) {
			defer wg.Done()
			_, _ = p.WriteAt([]byte("data"), 0)
			_ = p.MarkComplete()
			buf := make([]byte, 4)
			_, _ = p.ReadAt(buf, 0)
		}(pcs[i])
	}

	wg.Wait()

	stats, err := client.GetTorrentMemoryStats(infoHash)
	require.NoError(t, err)

	assert.Equal(t, 8, stats.TotalPieces)
	assert.Equal(t, numGoroutines, stats.InMemory)
}

// TestRaceWithMapIteration exposes race conditions with map iteration.
func TestRaceWithMapIteration(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 10)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: Continuously get torrent memory stats
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _ = client.GetTorrentMemoryStats(infoHash)
		}
	}()

	// Goroutine 2: Continuously add and remove pieces
	go func() {
		defer wg.Done()
		torrentImpl, _ := client.OpenTorrent(context.Background(), info, infoHash)
		for i := 0; i < 10; i++ {
			p := torrentImpl.Piece(info.Piece(i))
			_, _ = p.WriteAt(make([]byte, 256), 0)
		}
	}()

	wg.Wait()
}
