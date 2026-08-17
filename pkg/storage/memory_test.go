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
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

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
		hash := sha1.Sum(fmt.Appendf(nil, "piece_%d", i))
		copy(info.Pieces[i*20:(i+1)*20], hash[:])
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	infoHash := metainfo.Hash(sha1.Sum(b))
	return info, infoHash
}

// newNamedTestInfo creates a dummy torrent info and unique hash for testing multiple torrents.
func newNamedTestInfo(name string, pieceLength int64, numPieces int) (*metainfo.Info, metainfo.Hash) {
	info := &metainfo.Info{
		PieceLength: pieceLength,
		Pieces:      make([]byte, 20*numPieces),
		Name:        name,
		Length:      pieceLength * int64(numPieces),
	}
	for i := 0; i < numPieces; i++ {
		hash := sha1.Sum(fmt.Appendf(nil, "%s_piece_%d", name, i))
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

	assert.Nil(t, client.torrents[infoHash], "torrent state should be removed")

	// Piece entry is fully deleted — Completion() will return Ok=false, which
	// tells the torrent library the piece is unmanaged and must be re-downloaded.
	_, exists := client.pieces[key]
	assert.False(t, exists, "piece entry must be deleted from map after closeTorrent")
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
		_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
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
		_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
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
		_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
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
		_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
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
		_, err := p.WriteAt(fmt.Appendf(nil, "piece_%d", i), 0)
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

// TestConcurrentChunkWrites_NoMemoryLeak verifies that concurrent chunk writes
// to the SAME piece allocate memory exactly once without leaking c.used.
func TestConcurrentChunkWrites_NoMemoryLeak(t *testing.T) {
	const pieceSize = int64(256)
	client := newTestClient(pieceSize * 2)
	info, infoHash := newTestInfo(pieceSize, 2)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))

	var wg sync.WaitGroup
	const numWriters = 50
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(offset int64) {
			defer wg.Done()
			chunk := make([]byte, 4)
			_, _ = p.WriteAt(chunk, offset%250)
		}(int64(i * 4))
	}

	wg.Wait()

	stats := client.GetMemoryStats()
	assert.Equal(t, pieceSize, stats.UsedMemory, "used memory must equal exactly one piece size, no leak")
	assert.Equal(t, 1, stats.TotalPieces)
}

// TestConcurrentChunkWrites_UnderMemoryPressureAndEviction stresses concurrent chunk
// writes across many pieces under tight memory limits, verifying that continuous
// evictions never corrupt memory accounting or leak phantom memory.
func TestConcurrentChunkWrites_UnderMemoryPressureAndEviction(t *testing.T) {
	const pieceSize = int64(256)
	const maxPieces = 4
	const totalPieces = 20
	client := newTestClient(pieceSize * maxPieces)
	info, infoHash := newTestInfo(pieceSize, totalPieces)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	var wg sync.WaitGroup
	const numWriters = 200
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(workerID int) {
			defer wg.Done()
			pieceIndex := workerID % totalPieces
			p := torrentImpl.Piece(info.Piece(pieceIndex))
			chunk := []byte{byte(workerID), 1, 2, 3}
			offset := int64((workerID * 16) % int(pieceSize-4))
			_, _ = p.WriteAt(chunk, offset)
		}(i)
	}

	wg.Wait()

	client.mu.RLock()
	defer client.mu.RUnlock()

	var actualBytesInPieces int64
	for _, pd := range client.pieces {
		pd.mu.RLock()
		if pd.data != nil {
			actualBytesInPieces += int64(len(pd.data))
		}
		pd.mu.RUnlock()
	}

	assert.LessOrEqual(t, client.used, pieceSize*maxPieces, "used memory must never exceed max memory limit")
	assert.Equal(t, actualBytesInPieces, client.used, "client.used must strictly equal real memory allocated in pieces")
}

// TestPieceImpl_WriteAt_ConcurrentEvictionNoShortWrite verifies that concurrent evictions
// racing with chunk writes never cause WriteAt to return io.ErrShortWrite.
func TestPieceImpl_WriteAt_ConcurrentEvictionNoShortWrite(t *testing.T) {
	const pieceSize = int64(256)
	client := newTestClient(pieceSize * 2)
	info, infoHash := newTestInfo(pieceSize, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p0 := torrentImpl.Piece(info.Piece(0))

	var wg sync.WaitGroup
	const numWriters = 30
	const numEvictors = 10
	stopCh := make(chan struct{})

	// Writers: continuously write chunks to piece 0
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			chunk := []byte{byte(workerID), 1, 2, 3}
			for {
				select {
				case <-stopCh:
					return
				default:
					n, writeErr := p0.WriteAt(chunk, 0)
					assert.NoError(t, writeErr, "WriteAt must never fail with ErrShortWrite or eviction race")
					assert.Equal(t, len(chunk), n)
				}
			}
		}(i)
	}

	// Evictors: repeatedly force eviction
	for i := 0; i < numEvictors; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					client.ForceEvict(0)
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(stopCh)
	wg.Wait()
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

func TestSetActiveRange_BasicRegistration(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 512)

	c.mu.RLock()
	ts := c.torrents[ih]
	c.mu.RUnlock()

	ts.mu.RLock()
	ar, ok := ts.activeRanges[1]
	ts.mu.RUnlock()

	require.True(t, ok)
	assert.Equal(t, 0, ar.begin)
	assert.Equal(t, 2, ar.end) // ceil(512/256)
}

func TestSetActiveRange_InvalidInputsIgnored(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	// Non-existent torrent: should not panic.
	c.SetActiveRange(metainfo.Hash{}, 1, 0, 100)

	// Negative begin clamped to 0.
	c.SetActiveRange(ih, 1, -100, 100)
	ts := c.torrents[ih]
	ts.mu.RLock()
	ar := ts.activeRanges[1]
	ts.mu.RUnlock()
	assert.Equal(t, 0, ar.begin)

	// end <= begin: no-op.
	c.SetActiveRange(ih, 2, 500, 100)
	ts.mu.RLock()
	_, exists := ts.activeRanges[2]
	ts.mu.RUnlock()
	assert.False(t, exists)
}

func TestClearActiveRange_RemovesRegistration(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 512)
	c.ClearActiveRange(ih, 1)

	ts := c.torrents[ih]
	ts.mu.RLock()
	_, exists := ts.activeRanges[1]
	ts.mu.RUnlock()
	assert.False(t, exists)
}

func TestClearActiveRange_NonExistentTorrentNoPanic(t *testing.T) {
	c := newTestClient(1024)
	c.ClearActiveRange(metainfo.Hash{}, 1) // should not panic
}

func TestPieceWeight_SingleRange(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 768) // covers pieces 0,1,2

	c.mu.RLock()
	defer c.mu.RUnlock()

	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 0})) // in window, head
	assert.Equal(t, 999999, c.pieceWeight(pieceKey{hash: ih, index: 1}))  // in window
	assert.Equal(t, 999998, c.pieceWeight(pieceKey{hash: ih, index: 2}))  // in window
	// window end=768 → pieces 0,1,2; piece 3 is 1 ahead of window edge → weight 10000
	assert.Equal(t, 10000, c.pieceWeight(pieceKey{hash: ih, index: 3}))
}

func TestPieceWeight_MultipleRanges(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 256)    // piece 0
	c.SetActiveRange(ih, 2, 512, 1024) // pieces 2,3

	c.mu.RLock()
	defer c.mu.RUnlock()

	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 0})) // in window1
	// window1 end=256 (piece 0); piece 1 is 1 ahead of window1 edge → weight 10000
	assert.Equal(t, 10000, c.pieceWeight(pieceKey{hash: ih, index: 1}))
	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 2})) // in window2, head
	assert.Equal(t, 999999, c.pieceWeight(pieceKey{hash: ih, index: 3}))  // in window2
}

func TestPieceWeight_NoTorrent(t *testing.T) {
	c := newTestClient(1024)
	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.Equal(t, 0, c.pieceWeight(pieceKey{hash: metainfo.Hash{}, index: 0}))
}

func TestEvictDownToLocked_SkipsProtected(t *testing.T) {
	c := newTestClient(1024) // room for 4 pieces
	info, ih := newTestInfo(256, 4)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	// Write all 4 pieces, touch oldest first.
	for i := 0; i < 4; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	// Protect piece 0.
	c.SetActiveRange(ih, 1, 0, 256)

	c.mu.RLock()
	assert.Equal(t, int64(1024), c.used)
	c.mu.RUnlock()

	// Evict down 256 bytes — piece 3 is furthest from reader (distance 3),
	// so it gets evicted. Pieces 0,1,2 are inside or closest to reader.
	c.ForceEvict(768)

	c.mu.RLock()
	assert.Equal(t, int64(768), c.used)
	pd0, exists0 := c.pieces[pieceKey{hash: ih, index: 0}]
	assert.True(t, exists0, "piece 0 must survive eviction")
	assert.NotNil(t, pd0.data, "piece 0 must retain data")
	pd1, exists1 := c.pieces[pieceKey{hash: ih, index: 1}]
	assert.True(t, exists1, "piece 1 entry retained")
	assert.NotNil(t, pd1.data, "piece 1 must retain data")
	pd2, exists2 := c.pieces[pieceKey{hash: ih, index: 2}]
	assert.True(t, exists2, "piece 2 entry retained")
	assert.NotNil(t, pd2.data, "piece 2 must retain data")
	_, exists3 := c.pieces[pieceKey{hash: ih, index: 3}]
	assert.False(t, exists3, "piece 3 fully deleted from map after eviction")
	c.mu.RUnlock()
}

func TestEvictDownToLocked_AllProtected_EmergencyEviction(t *testing.T) {
	c := newTestClient(512) // room for 2 pieces
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	// Active range covering piece 0 and piece 1.
	c.SetActiveRange(ih, 1, 0, 512)

	// Evict 1 piece under extreme pressure.
	c.ForceEvict(256)

	c.mu.RLock()
	assert.Equal(t, int64(256), c.used)
	// Piece 0 (at reader head) has higher weight (1,000,000) than Piece 1 (999,999), so Piece 0 survives.
	pd0 := c.pieces[pieceKey{hash: ih, index: 0}]
	assert.NotNil(t, pd0.data, "piece 0 at reader head survives")
	_, exists1 := c.pieces[pieceKey{hash: ih, index: 1}]
	assert.False(t, exists1, "piece 1 further in window is evicted under emergency pressure")
	c.mu.RUnlock()
}

func TestEvictDownToLocked_ClearRangeEnablesEviction(t *testing.T) {
	c := newTestClient(512)
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	c.SetActiveRange(ih, 1, 0, 512)
	c.ClearActiveRange(ih, 1)

	c.ForceEvict(0)

	c.mu.RLock()
	assert.Equal(t, int64(0), c.used)
	c.mu.RUnlock()
}

// TestPieceWeight_TwoReaders_GapProtectsCloserPiece verifies that when two readers
// have separated windows, pieces in the gap are protected by whichever reader is closer.
func TestPieceWeight_TwoReaders_GapProtectsCloserPiece(t *testing.T) {
	c := newTestClient(2560)
	info, ih := newTestInfo(256, 10)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 512)     // reader1 covers pieces 0,1
	c.SetActiveRange(ih, 2, 1792, 2560) // reader2 covers pieces 7,8,9

	c.mu.RLock()
	defer c.mu.RUnlock()

	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 0})) // in reader1, head
	assert.Equal(t, 999999, c.pieceWeight(pieceKey{hash: ih, index: 1}))  // in reader1
	// reader1 window ends at piece 1; piece 2 is 1 ahead of window edge → weight 10000
	assert.Equal(t, 10000, c.pieceWeight(pieceKey{hash: ih, index: 2}))
	// piece 4 is 3 ahead of reader1 window edge → weight 9998
	assert.Equal(t, 9998, c.pieceWeight(pieceKey{hash: ih, index: 4}))
	// piece 5 is 4 ahead of reader1 window edge → weight 9997
	assert.Equal(t, 9997, c.pieceWeight(pieceKey{hash: ih, index: 5}))
	// piece 6 is 5 ahead of reader1 window edge → weight 9996
	assert.Equal(t, 9996, c.pieceWeight(pieceKey{hash: ih, index: 6}))
	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 7})) // in reader2, head
}

// TestPieceWeight_TwoReaders_BehindBothReaders verifies that pieces behind
// readers get soft backward protection (decaying from 5000), protecting recent history for rewinds.
func TestPieceWeight_TwoReaders_BehindBothReaders(t *testing.T) {
	c := newTestClient(2560)
	info, ih := newTestInfo(256, 10)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 768, 1280)  // reader1 covers pieces 3,4
	c.SetActiveRange(ih, 2, 2048, 2560) // reader2 covers pieces 8,9

	c.mu.RLock()
	defer c.mu.RUnlock()

	// pieces 0-2 are behind reader1 (begin=3): piece 2 is 1 behind (5000), piece 1 is 2 behind (4999), piece 0 is 3 behind (4998)
	assert.Equal(t, 4998, c.pieceWeight(pieceKey{hash: ih, index: 0}))
	assert.Equal(t, 4999, c.pieceWeight(pieceKey{hash: ih, index: 1}))
	assert.Equal(t, 5000, c.pieceWeight(pieceKey{hash: ih, index: 2}))
	// reader1 window ends at piece 5 (ceil); piece 5 is 1 ahead of window edge → weight 10000
	assert.Equal(t, 10000, c.pieceWeight(pieceKey{hash: ih, index: 5}))
}

// TestEvict_TwoReaders_EvictsFarthestPiece verifies that a piece farthest behind
// readers is evicted before pieces nearer either reader.
func TestEvict_TwoReaders_EvictsFarthestPiece(t *testing.T) {
	c := newTestClient(2560) // room for 10 pieces
	info, ih := newTestInfo(256, 10)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	c.SetActiveRange(ih, 1, 512, 768)   // reader1 covers piece 2
	c.SetActiveRange(ih, 2, 2048, 2560) // reader2 covers pieces 8,9

	c.mu.RLock()
	assert.Equal(t, int64(2560), c.used)

	// piece 0 is 2 behind reader1 (4999); piece 1 is 1 behind reader1 (5000)
	// reader1 window ends at piece 3; pieces 3-7 are 1-5 ahead of window edge → weights 10000,9999,9998,9997,9996
	assert.Equal(t, 4999, c.pieceWeight(pieceKey{hash: ih, index: 0}))
	assert.Equal(t, 5000, c.pieceWeight(pieceKey{hash: ih, index: 1}))
	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 2})) // in reader1
	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 8})) // in reader2
	c.mu.RUnlock()

	c.ForceEvict(2304) // free 256 bytes → 1 piece

	c.mu.RLock()
	defer c.mu.RUnlock()

	assert.Equal(t, int64(2304), c.used)

	// piece 0 (weight 4999, lowest weight) should be fully evicted
	_, exists0 := c.pieces[pieceKey{hash: ih, index: 0}]
	assert.False(t, exists0, "piece 0 farthest behind readers deleted from map")

	// piece 2 (inside reader1) and piece 9 (inside reader2) must survive
	pd2 := c.pieces[pieceKey{hash: ih, index: 2}]
	assert.NotNil(t, pd2.data, "piece 2 inside reader1 must survive")
	pd9 := c.pieces[pieceKey{hash: ih, index: 9}]
	assert.NotNil(t, pd9.data, "piece 9 inside reader2 must survive")
}

// TestEvict_TwoReaders_PieceBetweenReadersSurvives verifies that a piece
// positioned between two readers (protected by the closer one) is not evicted
// when a different piece is the farthest.
func TestEvict_TwoReaders_PieceBetweenReadersSurvives(t *testing.T) {
	c := newTestClient(2560)
	info, ih := newTestInfo(256, 10)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	c.SetActiveRange(ih, 1, 0, 256)     // reader1 covers piece 0
	c.SetActiveRange(ih, 2, 2048, 2560) // reader2 covers pieces 8,9

	c.mu.RLock()
	assert.Equal(t, int64(2560), c.used)

	// reader1 window ends at piece 0; piece 7 is 7 ahead of reader1 (weight 9994), and 1 behind reader2 (5000) -> max is 9994
	assert.Equal(t, 9994, c.pieceWeight(pieceKey{hash: ih, index: 7}))
	c.mu.RUnlock()

	c.ForceEvict(2304) // free 256 bytes → 1 piece

	c.mu.RLock()
	defer c.mu.RUnlock()

	// piece 7 (weight 9994, lowest positive among 1-7) is evicted
	_, exists7 := c.pieces[pieceKey{hash: ih, index: 7}]
	assert.False(t, exists7, "piece 7 has lowest weight, deleted from map")

	// piece 5 (weight 9996) and piece 2 (weight 9999) survive
	pd5 := c.pieces[pieceKey{hash: ih, index: 5}]
	assert.NotNil(t, pd5.data, "piece 5 protected by proximity to reader1")
	pd2 := c.pieces[pieceKey{hash: ih, index: 2}]
	assert.NotNil(t, pd2.data, "piece 2 protected by proximity to reader1")
}

func TestSetActiveRange_UpdatesExistingReader(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 256)    // piece 0
	c.SetActiveRange(ih, 1, 512, 1024) // pieces 2,3 — should replace

	ts := c.torrents[ih]
	ts.mu.RLock()
	ar := ts.activeRanges[1]
	ts.mu.RUnlock()

	assert.Equal(t, 2, ar.begin)
	assert.Equal(t, 4, ar.end)

	c.mu.RLock()
	assert.Equal(t, 4999, c.pieceWeight(pieceKey{hash: ih, index: 0}))    // 2 behind readhead (begin=2) → 4999
	assert.Equal(t, 5000, c.pieceWeight(pieceKey{hash: ih, index: 1}))    // 1 behind readhead (begin=2) → 5000
	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 2})) // in window, head
	assert.Equal(t, 999999, c.pieceWeight(pieceKey{hash: ih, index: 3}))  // in window
	c.mu.RUnlock()
}

func TestClient_CloseAndClosed(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p := tImpl.Piece(info.Piece(0))
	_, err = p.WriteAt([]byte("test"), 0)
	require.NoError(t, err)

	err = c.Close()
	require.NoError(t, err)

	select {
	case <-c.Closed():
	case <-time.After(1 * time.Second):
		t.Fatal("closed channel was not closed")
	}

	assert.Equal(t, int64(0), c.used)
	assert.Empty(t, c.pieces)
	assert.Empty(t, c.torrents)
}

func TestClient_GetMemoryStats(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p := tImpl.Piece(info.Piece(0))
	_, err = p.WriteAt([]byte("test"), 0)
	require.NoError(t, err)

	stats := c.GetMemoryStats()
	assert.Equal(t, int64(1024), stats.MaxMemory)
	assert.Equal(t, int64(256), stats.UsedMemory)
	assert.Equal(t, 1, stats.ActiveTorrents)
	assert.Equal(t, 1, stats.TotalPieces)

	usage := c.GetTorrentMemoryUsage(ih)
	assert.Equal(t, int64(256), usage)

	// Non-existent torrent returns 0 usage
	assert.Equal(t, int64(0), c.GetTorrentMemoryUsage(metainfo.Hash{}))
}

func TestClient_NewClient_NilLogger(t *testing.T) {
	c := NewClient(1024, nil)
	assert.NotNil(t, c)
	assert.NotNil(t, c.logger)
}

func TestClient_QueryNonExistentTorrent(t *testing.T) {
	c := newTestClient(1024)
	var dummyHash metainfo.Hash
	dummyHash[0] = 0xEE

	assert.Nil(t, c.GetCompletedPieces(dummyHash))
	assert.Equal(t, 0.0, c.GetCompletedProgress(dummyHash))
	assert.Nil(t, c.GetIncompletePieces(dummyHash))
	assert.Nil(t, c.GetPiecesInMemory(dummyHash))
	assert.Nil(t, c.GetPieceStatus(dummyHash, 0))
	assert.Equal(t, 0.0, c.GetMemoryUsageProgress(dummyHash))

	_, err := c.GetTorrentMemoryStats(dummyHash)
	assert.Error(t, err)

	// Torrent with 0 pieces
	c.torrents[dummyHash] = &torrentState{
		pieceHashes: nil,
	}
	assert.Equal(t, 0.0, c.GetCompletedProgress(dummyHash))
	assert.Equal(t, 0.0, c.GetMemoryUsageProgress(dummyHash))
}

func TestClient_OpenTorrent_InvalidPieces(t *testing.T) {
	c := newTestClient(1024)
	info := &metainfo.Info{
		Length:      1000,
		PieceLength: 100,
		Pieces:      make([]byte, 25), // not divisible by 20
	}
	_, err := c.OpenTorrent(context.Background(), info, metainfo.Hash{})
	assert.Error(t, err)
}

func TestClient_SetActiveRange_PieceLengthZero(t *testing.T) {
	c := newTestClient(1024)
	var ih metainfo.Hash
	ih[0] = 0xAA
	c.torrents[ih] = &torrentState{
		pieceLength: 0,
	}
	// Should return early without panic
	c.SetActiveRange(ih, 1, 0, 100)
}

func TestPieceImpl_ReadAt_EdgeCases(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 1)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p := tImpl.Piece(info.Piece(0))
	_, err = p.WriteAt([]byte("hello world"), 0)
	require.NoError(t, err)

	// Read with negative offset
	buf := make([]byte, 10)
	n, err := p.ReadAt(buf, -1)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	// Read with offset >= length
	n, err = p.ReadAt(buf, 300)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	// Read asking for more than remaining piece length
	largeBuf := make([]byte, 500)
	n, err = p.ReadAt(largeBuf, 0)
	assert.Equal(t, 256, n)
	assert.Equal(t, io.EOF, err)
}

func TestPieceImpl_WriteAt_EdgeCases(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 1)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p := tImpl.Piece(info.Piece(0))

	// Write negative offset
	_, err = p.WriteAt([]byte("data"), -5)
	assert.Error(t, err)

	// Write beyond piece length
	_, err = p.WriteAt([]byte("data"), 300)
	assert.Error(t, err)

	// Write too large slice
	largeData := make([]byte, 300)
	_, err = p.WriteAt(largeData, 0)
	assert.ErrorIs(t, err, io.ErrShortWrite)
}

func TestPieceImpl_MarkComplete_And_NotComplete(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p0 := tImpl.Piece(info.Piece(0))

	// Mark complete when data is nil
	pd := p0.(*pieceImpl).getOrCreatePieceData()
	pd.data = nil
	err = p0.MarkComplete()
	assert.Error(t, err)

	// Mark not complete on non-existent piece
	p1 := tImpl.Piece(info.Piece(1))
	err = p1.MarkNotComplete()
	assert.NoError(t, err)

	// Mark not complete on piece with data
	_, err = p1.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	err = p1.MarkComplete()
	require.NoError(t, err)
	assert.True(t, p1.Completion().Complete)
	err = p1.MarkNotComplete()
	require.NoError(t, err)
	assert.False(t, p1.Completion().Complete)
}

func TestPieceImpl_SelfHash_EdgeCases(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p := tImpl.Piece(info.Piece(0))

	// Non-existent piece in map
	h, err := p.(storage.SelfHashing).SelfHash()
	assert.NoError(t, err)
	assert.Equal(t, metainfo.Hash{}, h)

	// Piece with nil data
	_ = p.(*pieceImpl).getOrCreatePieceData()
	_, err = p.(storage.SelfHashing).SelfHash()
	assert.Error(t, err)
}

func TestPieceImpl_CloseTorrent(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 1)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p := tImpl.Piece(info.Piece(0))
	_, err = p.WriteAt([]byte("test"), 0)
	require.NoError(t, err)

	err = tImpl.Close()
	require.NoError(t, err)
	assert.Empty(t, c.torrents)
}

func TestPieceWeight_FarDistance(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 20000)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	c.SetActiveRange(ih, 1, 0, 256) // piece 0

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Very far piece (> 10000)
	w := c.pieceWeight(pieceKey{hash: ih, index: 15000})
	assert.Equal(t, 1, w)
}

func TestPieceImpl_ReadAt_EvictedAndShortBuffer(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 2)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p0 := tImpl.Piece(info.Piece(0))

	// Read when data is nil (evicted)
	pd := p0.(*pieceImpl).getOrCreatePieceData()
	pd.data = nil
	buf := make([]byte, 10)
	_, err = p0.ReadAt(buf, 0)
	assert.ErrorIs(t, err, ErrPieceNotAvailable)

	// Read when data buffer is smaller than piece length
	pd.data = []byte("short")
	n, err := p0.ReadAt(buf, 0)
	assert.Equal(t, 5, n)
	assert.Equal(t, io.EOF, err)
}

func TestPieceImpl_WriteAt_ShortDataBuffer(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 1)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p0 := tImpl.Piece(info.Piece(0))
	pd := p0.(*pieceImpl).getOrCreatePieceData()
	_ = p0.(*pieceImpl).ensureDataAllocated(pd)

	// Artificially shrink pd.data to test off + len(b) > len(pd.data)
	pd.mu.Lock()
	pd.data = make([]byte, 50)
	pd.mu.Unlock()

	data := make([]byte, 100)
	_, err = p0.WriteAt(data, 0)
	assert.ErrorIs(t, err, io.ErrShortWrite)
}

func TestClient_SetActiveRange_InvalidBounds(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	_, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	// begin >= end
	c.SetActiveRange(ih, 1, 500, 500)
	c.SetActiveRange(ih, 1, 600, 500)

	ts := c.torrents[ih]
	ts.mu.RLock()
	_, exists := ts.activeRanges[1]
	ts.mu.RUnlock()
	assert.False(t, exists)
}

func TestClient_GetCompletedProgress_MultiplePieces(t *testing.T) {
	c := newTestClient(1024)
	info, ih := newTestInfo(256, 4)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	p0 := tImpl.Piece(info.Piece(0))
	_, err = p0.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	err = p0.MarkComplete()
	require.NoError(t, err)

	p1 := tImpl.Piece(info.Piece(1))
	_, err = p1.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	err = p1.MarkComplete()
	require.NoError(t, err)

	progress := c.GetCompletedProgress(ih)
	assert.Equal(t, 0.5, progress)
}

// TestEvict_FastForwardAndRewind_SingleTorrent verifies eviction behavior during
// sequential play, fast-forward (FF) seek, and rewind (RW) seek on a single torrent.
func TestEvict_FastForwardAndRewind_SingleTorrent(t *testing.T) {
	c := newTestClient(2560) // room for 10 pieces (256 bytes each)
	info, ih := newTestInfo(256, 20)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	// Step 1: Initial sequential playback (pieces 0-3)
	for i := 0; i < 4; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}
	c.SetActiveRange(ih, 1, 0, 1024) // window covering pieces 0-3

	c.mu.RLock()
	assert.Equal(t, int64(1024), c.used)
	assert.Equal(t, 1000000, c.pieceWeight(pieceKey{hash: ih, index: 0}))
	c.mu.RUnlock()

	// Step 2: Fast-forward seek -> piece 12 (pieces 10-15 written)
	for i := 10; i < 16; i++ {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}
	// Active window moved to [10, 16) with trailing margin
	c.SetActiveRange(ih, 1, 2560, 4096) // pieces 10 to 15

	c.mu.RLock()
	assert.Equal(t, int64(2560), c.used) // memory is 100% full (10 pieces)
	// Piece 0 (far behind, dist=10) has lower backward score than piece 9 (dist=1)
	assert.True(t, c.pieceWeight(pieceKey{hash: ih, index: 0}) < c.pieceWeight(pieceKey{hash: ih, index: 1}))
	c.mu.RUnlock()

	// Step 3: Evict 2 pieces under pressure -> ancient piece 0 and 1 evicted first
	c.ForceEvict(2048)

	c.mu.RLock()
	assert.Equal(t, int64(2048), c.used)
	_, exists0 := c.pieces[pieceKey{hash: ih, index: 0}]
	assert.False(t, exists0, "piece 0 far behind reader was evicted")
	// In-window piece 12 survived
	pd12 := c.pieces[pieceKey{hash: ih, index: 12}]
	assert.NotNil(t, pd12.data, "piece 12 inside FF window survived")
	c.mu.RUnlock()

	// Step 4: Rewind seek <- back to piece 3 (pieces 2-5)
	c.SetActiveRange(ih, 1, 512, 1536) // window covering pieces 2-5

	c.mu.RLock()
	// Pieces 2 and 3 now receive Tier 3 in-window maximum protection
	assert.True(t, c.pieceWeight(pieceKey{hash: ih, index: 2}) >= 100000)
	assert.True(t, c.pieceWeight(pieceKey{hash: ih, index: 3}) >= 100000)
	c.mu.RUnlock()
}

// TestEvict_TwoReaders_SingleTorrent_SeekAndRewind tests two concurrent readers
// streaming different parts of the same torrent with FF and RW seeks.
func TestEvict_TwoReaders_SingleTorrent_SeekAndRewind(t *testing.T) {
	c := newTestClient(2560) // room for 10 pieces
	info, ih := newTestInfo(256, 20)
	tImpl, err := c.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	// Reader 1 at piece 1 (window [0, 3)), Reader 2 at piece 7 (window [6, 9))
	c.SetActiveRange(ih, 1, 0, 768)     // Reader 1: pieces 0, 1, 2
	c.SetActiveRange(ih, 2, 1536, 2304) // Reader 2: pieces 6, 7, 8

	// Populate pieces
	for _, i := range []int{0, 1, 2, 4, 6, 7, 8} {
		p := tImpl.Piece(info.Piece(i))
		_, err := p.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	c.mu.RLock()
	// Piece 4 is in the gap outside both windows
	w4 := c.pieceWeight(pieceKey{hash: ih, index: 4})
	w1 := c.pieceWeight(pieceKey{hash: ih, index: 1})
	w7 := c.pieceWeight(pieceKey{hash: ih, index: 7})
	assert.True(t, w1 >= 100000, "piece 1 inside reader1 window has Tier 3 protection")
	assert.True(t, w7 >= 100000, "piece 7 inside reader2 window has Tier 3 protection")
	assert.True(t, w4 < w1 && w4 < w7, "gap piece 4 has lower priority than in-window pieces")
	c.mu.RUnlock()

	// Evict 1 piece -> piece 4 (in gap) is evicted first
	c.ForceEvict(1536) // free 256 bytes

	c.mu.RLock()
	_, exists4 := c.pieces[pieceKey{hash: ih, index: 4}]
	assert.False(t, exists4, "piece 4 in gap evicted")
	assert.NotNil(t, c.pieces[pieceKey{hash: ih, index: 1}].data, "reader1 piece survived")
	assert.NotNil(t, c.pieces[pieceKey{hash: ih, index: 7}].data, "reader2 piece survived")
	c.mu.RUnlock()

	// Reader 2 Fast Forwards to piece 16 (window [14, 18))
	c.SetActiveRange(ih, 2, 3584, 4608)

	// Reader 1 Rewinds to piece 0 (window [0, 2))
	c.SetActiveRange(ih, 1, 0, 512)

	ts := c.torrents[ih]
	ts.mu.RLock()
	assert.Equal(t, 2, len(ts.activeRanges), "both readers remain tracked")
	assert.Equal(t, 0, ts.activeRanges[1].begin)
	assert.Equal(t, 14, ts.activeRanges[2].begin)
	ts.mu.RUnlock()
}

// TestEvict_TwoTorrents_SimultaneousStreaming_SeekAndRewind verifies simultaneous
// streaming across two distinct torrents (e.g. Bunny & Sintel) sharing a memory pool.
func TestEvict_TwoTorrents_SimultaneousStreaming_SeekAndRewind(t *testing.T) {
	c := newTestClient(2560) // room for 10 pieces total across all torrents

	infoA, ihA := newNamedTestInfo("bunny", 256, 10) // Torrent A ("Bunny")
	tA, err := c.OpenTorrent(context.Background(), infoA, ihA)
	require.NoError(t, err)

	infoB, ihB := newNamedTestInfo("sintel", 256, 10) // Torrent B ("Sintel")
	tB, err := c.OpenTorrent(context.Background(), infoB, ihB)
	require.NoError(t, err)

	// Stream Torrent A (Reader 1, pieces 0-3) and Torrent B (Reader 2, pieces 0-3)
	c.SetActiveRange(ihA, 1, 0, 1024)
	c.SetActiveRange(ihB, 2, 0, 1024)

	for i := 0; i < 4; i++ {
		pA := tA.Piece(infoA.Piece(i))
		_, err = pA.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)

		pB := tB.Piece(infoB.Piece(i))
		_, err = pB.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	c.mu.RLock()
	assert.Equal(t, int64(2048), c.used) // 8 pieces used (4 for A, 4 for B)
	c.mu.RUnlock()

	// Torrent A fast-forwards to pieces 7-9 (window [6, 10))
	c.SetActiveRange(ihA, 1, 1536, 2560)
	for i := 7; i < 10; i++ {
		pA := tA.Piece(infoA.Piece(i))
		_, err := pA.WriteAt(make([]byte, 256), 0)
		require.NoError(t, err)
	}

	// Torrent B rewinds to piece 0 (window [0, 2))
	c.SetActiveRange(ihB, 2, 0, 512)

	c.mu.RLock()
	// Active window pieces in both torrents have Tier 3 protection
	assert.True(t, c.pieceWeight(pieceKey{hash: ihA, index: 8}) >= 100000)
	assert.True(t, c.pieceWeight(pieceKey{hash: ihB, index: 1}) >= 100000)
	c.mu.RUnlock()

	// Reclaim memory to 1536 bytes (6 pieces)
	c.ForceEvict(1536)

	c.mu.RLock()
	assert.Equal(t, int64(1536), c.used)
	// Active pieces in both torrents survived
	assert.NotNil(t, c.pieces[pieceKey{hash: ihA, index: 8}].data, "Torrent A in-window piece survived")
	assert.NotNil(t, c.pieces[pieceKey{hash: ihB, index: 1}].data, "Torrent B in-window piece survived")
	c.mu.RUnlock()
}
