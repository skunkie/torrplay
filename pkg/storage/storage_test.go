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
	return New(maxMemory, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

func requirePieceBuffer(t testing.TB, piece storage.PieceImpl) []byte {
	t.Helper()
	impl, ok := piece.(*pieceImpl)
	if !ok {
		t.Fatalf("unexpected piece implementation %T", piece)
	}
	pd, err := impl.getPieceData()
	if err != nil {
		t.Fatal(err)
	}
	pd.mu.RLock()
	defer pd.mu.RUnlock()
	return pd.data
}

func requireTorrentStats(t testing.TB, client *Client, infoHash metainfo.Hash) TorrentStats {
	t.Helper()
	stats, err := client.TorrentStats(infoHash)
	require.NoError(t, err)
	return stats
}

func residentPieceIndexes(t testing.TB, client *Client, infoHash metainfo.Hash) []int {
	t.Helper()
	stats := requireTorrentStats(t, client, infoHash)
	indexes := make([]int, 0, stats.ResidentPieces)
	for _, piece := range stats.Pieces {
		if piece.Resident {
			indexes = append(indexes, piece.Index)
		}
	}
	return indexes
}

func piecesByCompletion(t testing.TB, client *Client, infoHash metainfo.Hash, complete bool) []int {
	t.Helper()
	stats := requireTorrentStats(t, client, infoHash)
	indexes := make([]int, 0, len(stats.Pieces))
	for _, piece := range stats.Pieces {
		if piece.Complete == complete {
			indexes = append(indexes, piece.Index)
		}
	}
	return indexes
}

func requirePieceStats(t testing.TB, client *Client, infoHash metainfo.Hash, index int) PieceStats {
	t.Helper()
	for _, piece := range requireTorrentStats(t, client, infoHash).Pieces {
		if piece.Index == index {
			return piece
		}
	}
	t.Fatalf("piece %d is not tracked", index)
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
	for i := 0; i < numPieces; i++ {
		infoHash := sha1.Sum([]byte(fmt.Sprintf("piece_%d", i)))
		copy(info.Pieces[i*20:(i+1)*20], infoHash[:])
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
	assert.Equal(t, 4, client.torrents[infoHash].totalPieces)
}

func TestClient_OpenTorrent_PureV2(t *testing.T) {
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
}

// TestClient_CloseTorrent ensures that closing a torrent removes its data and state.
func TestClient_CloseTorrent(t *testing.T) {
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
}

// TestClient_TorrentStats checks that torrent-specific memory statistics are accurate.
func TestClient_TorrentStats(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Add some pieces
	client.pieces[pieceKey{infoHash: infoHash, index: 0}] = &pieceData{data: make([]byte, 256), complete: true, pieceSize: 256}
	client.pieces[pieceKey{infoHash: infoHash, index: 1}] = &pieceData{data: make([]byte, 256), complete: false, pieceSize: 256}

	stats, err := client.TorrentStats(infoHash)
	require.NoError(t, err)

	assert.Equal(t, 4, stats.TotalPieces)
	assert.Equal(t, int64(512), stats.TrackedBytes)
	assert.Equal(t, int64(256), stats.CompletedBytes)
	assert.Equal(t, 2, stats.ResidentPieces)
}

func TestClient_TorrentStats_IncludesGlobalStats(t *testing.T) {
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
}

func TestClient_TorrentStats_UnmanagedTorrent(t *testing.T) {
	client := newTestClient(1024)

	_, err := client.TorrentStats(metainfo.Hash{1})
	assert.ErrorIs(t, err, ErrTorrentNotManaged)
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

// TestClient_MemoryEviction simulates memory pressure to ensure the LRU eviction policy works.
func TestClient_MemoryEviction(t *testing.T) {
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

func TestClient_PieceBufferReuse_StandardEvictionClearsStaleData(t *testing.T) {
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
}

func TestClient_PieceBufferReuse_DifferentSizeFallsBackToAllocation(t *testing.T) {
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
}

func TestClient_PieceBufferReuse_TransfersAccountingAcrossTorrents(t *testing.T) {
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
}

func TestClient_PieceBufferReuse_EmergencyEviction(t *testing.T) {
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
	client.SetActiveRange(infoHash, 1, 0, 0)

	_, err = p1.WriteAt([]byte{1}, 0)
	require.NoError(t, err)
	newBuffer := requirePieceBuffer(t, p1)
	if oldPointer != &newBuffer[0] {
		t.Fatal("emergency eviction should hand off an exact-size buffer")
	}
	assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
}

func TestClient_PieceBufferReuse_ConcurrentFirstWrite(t *testing.T) {
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
	for i := 0; i < writers; i++ {
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
	for i := 0; i < writers; i++ {
		assert.Equal(t, byte(i+1), newBuffer[i])
	}
	for i, value := range newBuffer[writers:] {
		if value != 0 {
			t.Fatalf("reused buffer retained stale byte at offset %d: %d", i+writers, value)
		}
	}
	assert.Equal(t, int64(256), client.MemoryStats().UsedBytes)
}

// TestClient_TorrentStats_CompletedFraction checks the calculation of completion fraction.
func TestClient_TorrentStats_CompletedFraction(t *testing.T) {
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

	progress := requireTorrentStats(t, client, infoHash).CompletedFraction()
	assert.InDelta(t, 0.5, progress, 0.001)
}

// TestClient_TorrentStats_MemoryUsageFraction verifies the calculation of memory usage fraction.
func TestClient_TorrentStats_MemoryUsageFraction(t *testing.T) {
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

	progress := requireTorrentStats(t, client, infoHash).MemoryUsageFraction()
	assert.InDelta(t, 0.5, progress, 0.001)
}

func TestClient_TorrentStats_MemoryUsageFraction_ZeroLimit(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 1)
	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	require.NoError(t, client.SetMaxMemory(0))
	assert.Equal(t, float64(0), requireTorrentStats(t, client, infoHash).MemoryUsageFraction())
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
	require.NoError(t, client.SetMaxMemory(512))

	stats, err := client.TorrentStats(infoHash)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.ResidentPieces)
}

func TestClient_SetMaxMemory_EnforcesLimitAcrossActiveRanges(t *testing.T) {
	client := newTestClient(512)
	info, infoHash := newTestInfo(256, 2)
	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err = torrentImpl.Piece(info.Piece(i)).WriteAt([]byte("data"), 0)
		require.NoError(t, err)
	}
	client.SetActiveRange(infoHash, 1, 0, 1)

	require.NoError(t, client.SetMaxMemory(0))
	stats := client.MemoryStats()
	assert.Equal(t, int64(0), stats.LimitBytes)
	assert.Equal(t, int64(0), stats.UsedBytes)
	assert.Equal(t, 0, stats.TrackedPieces)
}

// TestClient_EvictTo checks manual eviction down to a specific target.
func TestClient_EvictTo(t *testing.T) {
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
	reclaimed, err := client.EvictTo(256)
	require.NoError(t, err)
	assert.Equal(t, int64(512), reclaimed)

	stats, err := client.TorrentStats(infoHash)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.ResidentPieces)
}

// TestClient_TorrentStats_ResidentPieces verifies that the list of in-memory pieces is correct.
func TestClient_TorrentStats_ResidentPieces(t *testing.T) {
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

	inMemory := residentPieceIndexes(t, client, infoHash)
	assert.ElementsMatch(t, []int{0, 1}, inMemory)
}

// TestClient_TorrentStats_IncompletePieces confirms that the list of incomplete pieces is accurate.
func TestClient_TorrentStats_IncompletePieces(t *testing.T) {
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
}

// TestClient_TorrentStats_CompletedPieces ensures the list of completed pieces is correct.
func TestClient_TorrentStats_CompletedPieces(t *testing.T) {
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
}

// TestClient_TorrentStats_Piece checks that the status of an individual piece is reported correctly.
func TestClient_TorrentStats_Piece(t *testing.T) {
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
}

// TestPieceImpl_SelfHash verifies that the piece can hash its own data correctly.
func TestPieceImpl_SelfHash(t *testing.T) {
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

// TestClient_MemoryAllocationFailure ensures that writes fail when not enough memory is available.
func TestClient_MemoryAllocationFailure(t *testing.T) {
	client := newTestClient(128) // Very small memory
	info, infoHash := newTestInfo(256, 1)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))

	_, err = p.WriteAt([]byte("data"), 0)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientMemory)
}

// TestClient_ConcurrentAccess stresses concurrent reads and writes for race conditions.
func TestClient_ConcurrentAccess(t *testing.T) {
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

	stats, err := client.TorrentStats(infoHash)
	require.NoError(t, err)

	assert.Equal(t, 8, stats.TotalPieces)
	assert.Equal(t, numGoroutines, stats.ResidentPieces)
}

// TestClient_ConcurrentMapIteration exercises concurrent map access and iteration.
func TestClient_ConcurrentMapIteration(t *testing.T) {
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
			_, _ = client.TorrentStats(infoHash)
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

// TestClient_SetActiveRange_PieceProtected verifies that pieces inside an active range
// survive eviction even when they are the least-recently-used.
func TestClient_SetActiveRange_PieceProtected(t *testing.T) {
	client := newTestClient(512)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
	}

	client.SetActiveRange(infoHash, 1, 1, 1)

	// Write another piece to trigger eviction. Piece 0 is LRU and unprotected.
	p2 := torrentImpl.Piece(info.Piece(2))
	_, err = p2.WriteAt([]byte("piece_2"), 0)
	require.NoError(t, err)

	inMemory := residentPieceIndexes(t, client, infoHash)
	assert.Contains(t, inMemory, 1, "protected piece should still be in memory")
	assert.Contains(t, inMemory, 2, "newly written piece should be in memory")
	assert.NotContains(t, inMemory, 0, "unprotected LRU piece should have been evicted")
}

// TestClient_ClearActiveRange_AllowsEviction verifies that clearing an active range
// allows previously protected pieces to be evicted.
func TestClient_ClearActiveRange_AllowsEviction(t *testing.T) {
	client := newTestClient(512)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("piece_%d", i)), 0)
		require.NoError(t, err)
	}

	client.SetActiveRange(infoHash, 1, 1, 1)
	client.ClearActiveRange(infoHash, 1)

	// Trigger eviction; piece 1 is no longer protected.
	p2 := torrentImpl.Piece(info.Piece(2))
	_, err = p2.WriteAt([]byte("piece_2"), 0)
	require.NoError(t, err)

	inMemory := residentPieceIndexes(t, client, infoHash)
	assert.Contains(t, inMemory, 2, "newly written piece should be in memory")
	assert.LessOrEqual(t, len(inMemory), 2, "at most 2 pieces should be in memory")
}

// TestClient_SetActiveRange_MultipleReaders verifies that multiple active ranges
// from different readers are tracked independently.
func TestClient_SetActiveRange_MultipleReaders(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 8)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	client.SetActiveRange(infoHash, 10, 0, 2)
	client.SetActiveRange(infoHash, 20, 5, 7)

	client.mu.RLock()
	count := len(client.activeRanges)
	client.mu.RUnlock()
	assert.Equal(t, 2, count)

	client.ClearActiveRange(infoHash, 10)

	client.mu.RLock()
	count = len(client.activeRanges)
	client.mu.RUnlock()
	assert.Equal(t, 1, count)

	client.ClearActiveRange(infoHash, 20)

	client.mu.RLock()
	count = len(client.activeRanges)
	client.mu.RUnlock()
	assert.Equal(t, 0, count)
}

// TestClient_CloseTorrent_ClearsActiveRanges verifies that closing a torrent
// removes all associated active ranges.
func TestClient_CloseTorrent_ClearsActiveRanges(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	client.SetActiveRange(infoHash, 1, 0, 2)
	client.SetActiveRange(infoHash, 2, 1, 3)

	err = torrentImpl.Close()
	require.NoError(t, err)

	client.mu.RLock()
	defer client.mu.RUnlock()
	for key := range client.activeRanges {
		assert.NotEqual(t, infoHash, key.infoHash, "no active ranges should remain for closed torrent")
	}
}

// TestClient_IsPieceInActiveRange verifies the piece-in-range check directly.
func TestClient_IsPieceInActiveRange(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 8)

	_, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	client.SetActiveRange(infoHash, 1, 2, 5)

	client.mu.RLock()
	defer client.mu.RUnlock()

	// Inside range.
	assert.True(t, client.isPieceInActiveRangeLocked(pieceKey{infoHash: infoHash, index: 2}))
	assert.True(t, client.isPieceInActiveRangeLocked(pieceKey{infoHash: infoHash, index: 3}))
	assert.True(t, client.isPieceInActiveRangeLocked(pieceKey{infoHash: infoHash, index: 5}))

	// Outside range.
	assert.False(t, client.isPieceInActiveRangeLocked(pieceKey{infoHash: infoHash, index: 1}))
	assert.False(t, client.isPieceInActiveRangeLocked(pieceKey{infoHash: infoHash, index: 6}))
	assert.False(t, client.isPieceInActiveRangeLocked(pieceKey{infoHash: infoHash, index: 0}))
}

// TestPieceImpl_ConcurrentWriteSamePiece_NoMemoryDrift ensures that multiple goroutines
// racing to write the same never-before-allocated piece don't cause c.used
// to drift from actual allocated memory (regression test for the double
// allocation race in ensureDataAllocated / freeMemory).
func TestPieceImpl_ConcurrentWriteSamePiece_NoMemoryDrift(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 1)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
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
}

func TestPieceImpl_ConcurrentFirstAllocation_TightLimit(t *testing.T) {
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
}

// TestPieceImpl_ConcurrentWriteSamePiece_DataIntegrity verifies that concurrent writes
// to the same piece don't corrupt the underlying data slice. Each goroutine
// writes a unique byte value to a distinct offset; after all writes complete,
// every byte is checked for correctness.
func TestPieceImpl_ConcurrentWriteSamePiece_DataIntegrity(t *testing.T) {
	client := newTestClient(4096)
	info, infoHash := newTestInfo(256, 1)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	p := torrentImpl.Piece(info.Piece(0))

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			buf := make([]byte, 1)
			buf[0] = byte(idx)
			_, _ = p.WriteAt(buf, int64(idx%256))
		}(i)
	}
	wg.Wait()

	// Verify every written byte landed at the correct offset.
	buf := make([]byte, 256)
	n, err := p.ReadAt(buf, 0)
	assert.NoError(t, err)
	assert.Equal(t, 256, n)
	for idx := 0; idx < numGoroutines; idx++ {
		assert.Equal(t, byte(idx), buf[idx%256],
			"byte written by goroutine %d was lost or corrupted", idx)
	}
}

// TestClient_MemoryAllocationFailure_NoUsedDrift ensures that a failed allocation
// leaves c.used unchanged (no partial state leaked).
func TestClient_MemoryAllocationFailure_NoUsedDrift(t *testing.T) {
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
}

// TestClient_AllocateMemory_ActiveRangeEmergencyEviction verifies that when protected
// pieces fill maxMemory, the allocator emergency-evicts the oldest piece to allow
// incoming pieces to be written without failing and halting torrent downloads.
func TestClient_AllocateMemory_ActiveRangeEmergencyEviction(t *testing.T) {
	client := newTestClient(256) // room for exactly 1 piece
	info, infoHash := newTestInfo(256, 2)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Write piece 0 — uses all 256 bytes.
	p0 := torrentImpl.Piece(info.Piece(0))
	_, err = p0.WriteAt([]byte("data"), 0)
	require.NoError(t, err)

	client.SetActiveRange(infoHash, 1, 0, 0)

	// Write piece 1 — under memory pressure, piece 0 should be emergency-evicted.
	p1 := torrentImpl.Piece(info.Piece(1))
	_, err = p1.WriteAt([]byte("data"), 0)
	require.NoError(t, err)

	inMemory := residentPieceIndexes(t, client, infoHash)
	assert.Contains(t, inMemory, 1)
	assert.NotContains(t, inMemory, 0)
}

// TestClient_AllocateMemory_PieceLargerThanMaxMemory verifies that a piece exceeding
// the total maxMemory limit returns ErrInsufficientMemory and leaves no ghost entries behind.
func TestClient_AllocateMemory_PieceLargerThanMaxMemory(t *testing.T) {
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
}

// TestClient_EvictTo_StopsGracefullyWithProtection verifies that eviction does not
// loop forever when the target is below what's achievable due to active ranges.
func TestClient_EvictTo_StopsGracefullyWithProtection(t *testing.T) {
	client := newTestClient(512) // 2 pieces
	info, infoHash := newTestInfo(256, 4)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	// Fill memory with pieces 0 and 1.
	for i := 0; i < 2; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		_, err := p.WriteAt([]byte(fmt.Sprintf("p%d", i)), 0)
		require.NoError(t, err)
	}

	client.SetActiveRange(infoHash, 1, 0, 1)

	// EvictTo must not deadlock when all pieces are protected.
	done := make(chan error, 1)
	go func() {
		_, err := client.EvictTo(0)
		done <- err
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, ErrEvictionTargetNotReached)
	case <-time.After(2 * time.Second):
		t.Fatal("EvictTo hung with protected pieces exceeding target")
	}

	client.mu.RLock()
	used := client.used
	client.mu.RUnlock()
	assert.Equal(t, int64(512), used, "protected pieces should survive eviction")
}

// TestClient_SetMaxMemory_NegativeClampsToZero verifies that SetMaxMemory(-100)
// clamps to 0 and triggers eviction.
func TestClient_SetMaxMemory_NegativeClampsToZero(t *testing.T) {
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
}

func TestClient_MemoryControls_AfterClose(t *testing.T) {
	client := newTestClient(512)
	require.NoError(t, client.Close())

	assert.ErrorIs(t, client.SetMaxMemory(1), ErrClientClosed)
	_, err := client.EvictTo(0)
	assert.ErrorIs(t, err, ErrClientClosed)
}

func TestClient_Close_Idempotent(t *testing.T) {
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
}

func TestPieceImpl_TorrentClose_StalePieceCannotRecreateData(t *testing.T) {
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

func TestClient_OpenTorrent_PreservesPieceMemoryOnReopen(t *testing.T) {
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
}

func TestPieceImpl_ReadAt_MissDoesNotCreateGhostPiece(t *testing.T) {
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

func TestPieceImpl_SelfHash_MissingPieceReturnsError(t *testing.T) {
	client := newTestClient(1024)
	info, infoHash := newTestInfo(256, 1)

	torrentImpl, err := client.OpenTorrent(context.Background(), info, infoHash)
	require.NoError(t, err)

	selfHasher, ok := torrentImpl.Piece(info.Piece(0)).(storage.SelfHashing)
	require.True(t, ok)

	_, err = selfHasher.SelfHash()
	assert.ErrorIs(t, err, ErrPieceNotAvailable, "missing piece should return ErrPieceNotAvailable")
}

func TestPieceImpl_CompletionAndSelfHash_AllLifecycleStates(t *testing.T) {
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

func TestPieceImpl_TouchPiece_ThrottlingAndClockSkew(t *testing.T) {
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

func TestPieceImpl_WriteAt_OrphanedPieceRace(t *testing.T) {
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
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
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

	for i := 0; i < b.N; i++ {
		p := torrentImpl.Piece(info.Piece(i))
		start := make(chan struct{})
		errs := make([]error, writers)
		var wg sync.WaitGroup
		wg.Add(writers)
		for writer := 0; writer < writers; writer++ {
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
		for i := 0; i < pieceCount; i++ {
			if _, err := torrentImpl.Piece(info.Piece(i)).WriteAt(data, 0); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < pieceCount; i++ {
		if _, err := torrentImpl.Piece(info.Piece(i)).WriteAt(data, 0); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("Global", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = client.MemoryStats()
		}
	})
	b.Run("TorrentDetailed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
