// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/pkg/storage"
)

func acquireTestReader(t *testing.T, pool *Pool, file *torrent.File, mode StorageMode, budgetBytes int64) (io.ReadSeeker, ReleaseFunc) {
	t.Helper()
	pool.SetReadaheadBudget(budgetBytes)
	reader, release, err := pool.Acquire(file, mode)
	if err != nil {
		t.Fatalf("acquire reader: %v", err)
	}
	return reader, release
}

// newTestTorrentClient creates a minimal in-memory torrent client for integration tests.
func newTestTorrentClient(t *testing.T) *torrent.Client {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	maxMemory := int64(16 * 1024 * 1024) // 16 MB
	storageClient := storage.New(maxMemory, logger)

	cfg := torrent.NewDefaultClientConfig()
	cfg.DefaultStorage = storageClient
	cfg.DisablePEX = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = false

	c, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create torrent client: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	return c
}

// createTestMetaInfo builds a metainfo with a single 64-byte file "test.bin".
func createTestMetaInfo(t *testing.T) *metainfo.MetaInfo {
	t.Helper()

	// Build a minimal info dictionary.
	info := &metainfo.Info{
		Name:        "test-torrent",
		PieceLength: 64,
		Length:      64,
		Pieces:      make([]byte, 20), // 1 piece = 20 bytes SHA1
	}

	// Bencode the info dictionary to get InfoBytes for MetaInfo.
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("failed to bencode info: %v", err)
	}

	mi := &metainfo.MetaInfo{
		InfoBytes: infoBytes,
	}
	return mi
}

// addTestTorrent creates a proper torrent with a single 64-byte file.
func addTestTorrent(t *testing.T, c *torrent.Client) (*torrent.Torrent, *torrent.File) {
	t.Helper()

	mi := createTestMetaInfo(t)
	spec := torrent.TorrentSpecFromMetaInfo(mi)

	to, _, err := c.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("failed to add torrent: %v", err)
	}

	// Wait for info.
	select {
	case <-to.GotInfo():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for torrent info")
	}

	files := to.Files()
	if len(files) == 0 {
		t.Fatal("expected at least one file")
	}

	return to, files[0]
}

func TestPool_AcquireAndRelease(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	reader, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	release()

	// Verify reader moved to idle state.
	pool.mu.Lock()
	found := false
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash && sr.file == f && !sr.active {
			found = true
			break
		}
	}
	pool.mu.Unlock()

	if !found {
		t.Fatal("expected reader to be in idle state after release")
	}

	pool.Close()
}

func TestPool_AcquireReuseIdleReader(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	_, release1 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	release1()

	// Second acquire should reuse the idle reader.
	_, release2 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)

	// Verify only one reader exists.
	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash {
			count++
		}
	}
	pool.mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 reader (reused), got %d", count)
	}

	release2()
	pool.Close()
}

func TestPool_ReaderPositions_Integration(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	result := pool.ReaderPositions(infoHash)
	if len(result) != 0 {
		t.Fatalf("expected 0 readers, got %d", len(result))
	}

	_, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)

	result = pool.ReaderPositions(infoHash)
	if len(result) != 1 {
		t.Fatalf("expected 1 reader position, got %d", len(result))
	}

	// Position should be within file bounds.
	if result[0].Start > result[0].End {
		t.Fatalf("expected Start <= End, got %d > %d", result[0].Start, result[0].End)
	}

	release()
	pool.Close()
}

func TestPool_ReaderPositions_MultipleReaders(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	_, release1 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	_, release2 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)

	result := pool.ReaderPositions(infoHash)
	if len(result) != 2 {
		t.Fatalf("expected 2 reader positions, got %d", len(result))
	}

	release1()
	release2()
	pool.Close()
}

func TestPool_ReaderPositions_WrongInfoHash(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrent(t, c)
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	_, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)

	// Query with a different hash.
	wrongHash := metainfo.Hash{}
	result := pool.ReaderPositions(wrongHash)
	if len(result) != 0 {
		t.Fatalf("expected 0 readers for wrong hash, got %d", len(result))
	}

	release()
	pool.Close()
}

func TestPool_ActiveRangeSetOnAcquire(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrent(t, c)
	totalBudget := int64(1024 * 1024)

	reg := &testRegistry{}

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
		Registry:        reg,
	})

	_, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if reg.setCalls.Load() != 1 {
		t.Fatalf("expected 1 SetActiveRange call, got %d", reg.setCalls.Load())
	}

	// Verify the registry captured the last range values.
	snap := reg.lastRangeSnapshot()
	if snap.start > snap.end {
		t.Fatalf("expected start <= end, got start=%d end=%d", snap.start, snap.end)
	}

	release()
	if reg.clearCalls.Load() != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clearCalls.Load())
	}

	pool.Close()
}

type testRegistry struct {
	setCalls   atomic.Int32
	clearCalls atomic.Int32
	mu         sync.Mutex
	lastRange  struct {
		infoHash metainfo.Hash
		readerID uint64
		start    int
		end      int
	}
}

func (r *testRegistry) SetActiveRange(infoHash metainfo.Hash, readerID uint64, start, end int) {
	r.setCalls.Add(1)
	r.mu.Lock()
	r.lastRange = struct {
		infoHash metainfo.Hash
		readerID uint64
		start    int
		end      int
	}{infoHash: infoHash, readerID: readerID, start: start, end: end}
	r.mu.Unlock()
}

func (r *testRegistry) ClearActiveRange(_ metainfo.Hash, _ uint64) {
	r.clearCalls.Add(1)
}

func (r *testRegistry) lastRangeSnapshot() struct {
	infoHash metainfo.Hash
	readerID uint64
	start    int
	end      int
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastRange
}

func TestPool_ParkIdleReaders_RemovesAfterCloseTimeout(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:           slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout:  1 * time.Millisecond,
		IdleCloseTimeout: 10 * time.Millisecond,
	})

	_, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	release()

	// Wait for CloseTimeout to pass.
	time.Sleep(20 * time.Millisecond)

	// Trigger the GC manually.
	pool.parkIdleReaders()

	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash {
			count++
		}
	}
	pool.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 readers after CloseTimeout, got %d", count)
	}

	pool.Close()
}

func TestPool_AcquireWithFileStorage(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrent(t, c)
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout:    30 * time.Second,
		FileReadaheadBytes: 50 * 1024 * 1024,
	})

	reader, release := acquireTestReader(t, pool, f, FileStorage, totalBudget)
	if reader == nil {
		t.Fatal("expected non-nil reader for file storage")
	}

	release()
	pool.Close()
}

func TestPool_AcquireWhileClosed(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrent(t, c)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	pool.Close()

	reader, release, err := pool.Acquire(f, MemoryStorage)
	if !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("expected ErrPoolClosed, got %v", err)
	}
	if reader != nil || release != nil {
		t.Fatal("expected no reader or release function from closed pool")
	}

	pool.mu.Lock()
	count := len(pool.readers)
	pool.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 tracked readers, got %d", count)
	}
}

func TestPool_AcquireRejectsInvalidStorageMode(t *testing.T) {
	c := newTestTorrentClient(t)
	_, file := addTestTorrent(t, c)
	pool := New(Config{})
	t.Cleanup(pool.Close)

	reader, release, err := pool.Acquire(file, StorageMode(255))
	if !errors.Is(err, ErrInvalidStorageMode) {
		t.Fatalf("expected ErrInvalidStorageMode, got %v", err)
	}
	if reader != nil || release != nil {
		t.Fatal("expected no reader or release function")
	}
}

// createMultiPieceMetaInfo builds a metainfo with 10 pieces of 64 bytes each.
func createMultiPieceMetaInfo(t *testing.T) *metainfo.MetaInfo {
	t.Helper()
	const pieceLen = int64(64)
	const numPieces = 10
	totalSize := pieceLen * numPieces

	info := &metainfo.Info{
		Name:        "multi-torrent",
		PieceLength: pieceLen,
		Length:      totalSize,
		Pieces:      make([]byte, numPieces*20), // 10 pieces x 20 bytes SHA1
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("failed to bencode info: %v", err)
	}

	return &metainfo.MetaInfo{InfoBytes: infoBytes}
}

func addMultiPieceTorrent(t *testing.T, c *torrent.Client) (*torrent.Torrent, *torrent.File) {
	t.Helper()

	mi := createMultiPieceMetaInfo(t)
	spec := torrent.TorrentSpecFromMetaInfo(mi)

	to, _, err := c.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("failed to add torrent: %v", err)
	}

	select {
	case <-to.GotInfo():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for torrent info")
	}

	files := to.Files()
	if len(files) == 0 {
		t.Fatal("expected at least one file")
	}

	return to, files[0]
}

// TestPool_MaxReadersPerFile_ViaAcquire verifies the reuse-and-cap behavior when
// Acquire is called multiple times for the same (info hash, file path).
//
// NOTE: This test does NOT exercise the eviction branch (evictOldestIdleLocked)
// because the reuse loop at the top of Acquire always returns an idle reader
// before the cap-check/eviction block is reached. For a single (info hash, file path),
// evictOldestIdleLocked is effectively unreachable through the public Acquire API —
// any idle reader is always reusable, so the loop intercepts early. The eviction
// path is currently exercised only by direct unit tests of evictOldestIdleLocked
// itself and by cross-key contention scenarios (not shown here). If that is
// intentional design, document it in Pool.Acquire; otherwise the reuse loop may
// need to consider the cap before returning an idle reader.
func TestPool_MaxReadersPerFile_ViaAcquire(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout:   30 * time.Second,
		MaxReadersPerFile: 2,
	})

	// Acquire reader 1 — it becomes active.
	r1, rel1 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if r1 == nil {
		t.Fatal("expected non-nil reader 1")
	}

	// Acquire reader 2 — since r1 is active (not idle), reuse finds no idle reader,
	// so a new reader is created. r1 stays active, r2 becomes active.
	r2, rel2 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if r2 == nil {
		t.Fatal("expected non-nil reader 2")
	}

	// Both are still active. Now release both — both go idle.
	rel1()
	rel2()

	// Pool should have exactly 2 idle readers.
	pool.mu.Lock()
	idleCount := 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash && !sr.active {
			idleCount++
		}
	}
	pool.mu.Unlock()

	if idleCount != 2 {
		t.Fatalf("expected 2 idle readers after releasing both, got %d", idleCount)
	}

	// Acquire reader 3 — reuse loop finds an idle reader and returns it as active.
	// No eviction because an idle reader was available for reuse.
	r3, rel3 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if r3 == nil {
		t.Fatal("expected non-nil reader 3")
	}

	// 2 readers still tracked in the pool (reuse returned one idle reader as active).
	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash {
			count++
		}
	}
	pool.mu.Unlock()

	if count != 2 {
		t.Fatalf("expected 2 readers after reuse, got %d", count)
	}

	// 1 active (r3, which reused one of the idle readers), 1 idle remaining.
	pool.mu.Lock()
	activeCount := 0
	idleCount = 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash {
			if sr.active {
				activeCount++
			} else {
				idleCount++
			}
		}
	}
	pool.mu.Unlock()

	if activeCount != 1 {
		t.Fatalf("expected 1 active reader, got %d", activeCount)
	}

	rel3()
	pool.Close()
}

func TestPool_MaxReadersPerFile_NoEvictionWhenAllActive(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout:   30 * time.Second,
		MaxReadersPerFile: 2,
	})

	// Acquire 2 readers without releasing — both are active.
	r1, rel1 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if r1 == nil {
		t.Fatal("expected non-nil reader 1")
	}
	r2, rel2 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if r2 == nil {
		t.Fatal("expected non-nil reader 2")
	}

	// Acquire a third reader — no idle readers to evict,
	// so cap is soft and third reader is still created.
	r3, rel3 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if r3 == nil {
		t.Fatal("expected non-nil reader 3 (soft cap)")
	}

	// All 3 should still be tracked (soft cap).
	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash {
			count++
		}
	}
	pool.mu.Unlock()

	if count != 3 {
		t.Fatalf("expected 3 readers (soft cap), got %d", count)
	}

	rel1()
	rel2()
	rel3()
	pool.Close()
}

func TestPool_ConcurrentAcquire(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addMultiPieceTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)
	iterations := 50

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	var wg sync.WaitGroup
	var successCount atomic.Int32
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reader, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
			if reader == nil {
				t.Error("expected non-nil reader")
				return
			}
			successCount.Add(1)
			release()
		}()
	}
	wg.Wait()

	// All 50 Acquire calls should succeed (return non-nil reader).
	if successCount.Load() != int32(iterations) {
		t.Fatalf("expected %d successful Acquire calls, got %d", iterations, successCount.Load())
	}

	// After all goroutines finish, readers should be idle (reused or separate).
	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash && !sr.active {
			count++
		}
	}
	pool.mu.Unlock()

	if count < 1 {
		t.Fatalf("expected at least 1 idle reader, got %d", count)
	}

	pool.Close()
}

func TestComputeRange_Integration(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addMultiPieceTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	reader, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	positions := pool.ReaderPositions(infoHash)
	if len(positions) != 1 {
		t.Fatalf("expected 1 reader position, got %d", len(positions))
	}

	ri := positions[0]
	if ri.Start > ri.End {
		t.Fatalf("expected Start <= End, got Start=%d End=%d", ri.Start, ri.End)
	}
	if ri.Position < 0 {
		t.Fatalf("expected Position >= 0, got %d", ri.Position)
	}

	fileInfo := to.Info()
	if fileInfo == nil {
		t.Fatal("expected torrent info")
	}

	// At offset 0, positionPiece = 0, trailing = readaheadPieces/4,
	// start = max(0, 0-trailing), and end = readaheadPieces.
	// readaheadPieces = readahead / pieceLength, and the 1 MiB budget has one active reader.
	// readaheadPieces = 1048576 / 64 = 16384, trailing = 4096.
	// start = max(0, 0 - 4096) = 0, end = 0 + 16384 = 16384, but end clamped to the
	// final inclusive piece index (EndPieceIndex-1 = 8)
	// position = 0 + beginPiece = 0
	wantEnd := int(f.EndPieceIndex() - 1)
	if ri.End != wantEnd {
		t.Fatalf("expected End=%d (last inclusive piece), got %d", wantEnd, ri.End)
	}
	if ri.Start != 0 {
		t.Fatalf("expected Start=0, got %d", ri.Start)
	}
	if ri.Position != 0 {
		t.Fatalf("expected Position=0, got %d", ri.Position)
	}

	release()
	pool.Close()
}

func TestComputeRange_Integration_MidFile(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addMultiPieceTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})

	reader, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	// Manually set lastOffset to 320 (middle of file = 5 pieces × 64 bytes).
	// lastOffset is only updated by the onOffsetChange callback from ReadAt,
	// but since the torrent has no peer data, we set it directly to exercise
	// computeRange at a non-zero offset.
	pool.mu.Lock()
	var found *streamReader
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash && sr.file == f {
			found = sr
			break
		}
	}
	if found == nil {
		t.Fatal("expected to find reader in pool")
	}
	found.lastOffset = 320
	pool.mu.Unlock()

	// After lastOffset = 320:
	//   pieceIndex = 320 / 64 = 5, beginPiece = 5
	//   readahead = 1 MiB / 1 active reader, so readaheadPieces = 1048576 / 64 = 16384.
	//   trailing = readaheadPieces / 4 = 4096.
	//   start = max(0, 5 - 4096) = 0
	//   end is clamped to the final inclusive file piece (EndPieceIndex-1)
	//   position = beginPiece = 5
	positions := pool.ReaderPositions(infoHash)
	if len(positions) != 1 {
		t.Fatalf("expected 1 reader position, got %d", len(positions))
	}

	ri := positions[0]
	if ri.Start > ri.End {
		t.Fatalf("expected Start <= End, got Start=%d End=%d", ri.Start, ri.End)
	}

	// Start must be 0 (trailing extends past piece 0).
	if ri.Start != 0 {
		t.Fatalf("expected Start=0, got %d", ri.Start)
	}

	// End must be clamped to the final inclusive file piece.
	wantEnd := int(f.EndPieceIndex() - 1)
	if ri.End != wantEnd {
		t.Fatalf("expected End=%d (last inclusive piece), got %d", wantEnd, ri.End)
	}

	// Position must reflect piece 5 (offset 320 / 64 bytes per piece).
	if ri.Position != 5 {
		t.Fatalf("expected Position=5 (piece for offset 320), got %d", ri.Position)
	}

	release()
	pool.Close()
}

func TestPool_PriorityClaims_OverlappingReaders(t *testing.T) {
	c := newTestTorrentClient(t)
	to, file := addMultiPieceTorrent(t, c)
	p := New(Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))})
	defer p.Close()

	sr1 := &streamReader{file: file}
	sr2 := &streamReader{file: file}
	planned := p.prioritizeNextPieces(file, 0, 256, 1, 0.5)
	if len(planned) == 0 {
		t.Fatal("expected a non-empty priority plan")
	}
	for _, piece := range planned {
		if piece.index >= file.EndPieceIndex() {
			t.Fatalf("priority plan crossed exclusive file end: piece=%d end=%d", piece.index, file.EndPieceIndex())
		}
	}
	boundedPlan := p.prioritizeNextPieces(file, 0, 1<<40, 1, 0.5)
	if cap(boundedPlan) > file.EndPieceIndex()-file.BeginPieceIndex() {
		t.Fatalf("priority plan capacity should be bounded by file pieces, got %d", cap(boundedPlan))
	}

	sr1.priorityMu.Lock()
	p.replaceReaderPrioritiesLocked(sr1, file, planned)
	sr1.priorityMu.Unlock()
	sr2.priorityMu.Lock()
	p.replaceReaderPrioritiesLocked(sr2, file, planned)
	sr2.priorityMu.Unlock()

	key := priorityPieceKey{torrent: to, index: planned[0].index}
	p.priorityMu.Lock()
	claim := p.priorityClaims[key]
	ownerCount := 0
	if claim != nil {
		ownerCount = len(claim.owners)
	}
	p.priorityMu.Unlock()
	if ownerCount != 2 {
		t.Fatalf("expected two owners for overlapping piece, got %d", ownerCount)
	}

	sr1.priorityMu.Lock()
	p.clearReaderPrioritiesLocked(sr1)
	sr1.priorityMu.Unlock()

	p.priorityMu.Lock()
	claim = p.priorityClaims[key]
	ownerCount = 0
	secondReaderStillOwns := false
	if claim != nil {
		ownerCount = len(claim.owners)
		_, secondReaderStillOwns = claim.owners[sr2]
	}
	p.priorityMu.Unlock()
	if ownerCount != 1 || !secondReaderStillOwns {
		t.Fatalf("expected the second reader's claim to survive, owners=%d second=%v", ownerCount, secondReaderStillOwns)
	}

	sr2.priorityMu.Lock()
	p.clearReaderPrioritiesLocked(sr2)
	sr2.priorityMu.Unlock()
	p.priorityMu.Lock()
	_, stillClaimed := p.priorityClaims[key]
	p.priorityMu.Unlock()
	if stillClaimed {
		t.Fatal("expected final owner release to remove the piece claim")
	}
}

func TestPool_AcquireWithFileStorage_Readahead(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)
	wantReadahead := int64(75 * 1024 * 1024)

	pool := New(Config{
		Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout:    30 * time.Second,
		FileReadaheadBytes: wantReadahead,
	})

	reader, release := acquireTestReader(t, pool, f, FileStorage, totalBudget)
	if reader == nil {
		t.Fatal("expected non-nil reader for file storage")
	}

	// Verify readahead was set to the file storage value, not divided by pool.
	pool.mu.Lock()
	for _, sr := range pool.readers {
		if sr.infoHash == infoHash && sr.isFileStorage {
			if sr.readahead != wantReadahead {
				t.Fatalf("expected file storage readahead=%d, got %d", wantReadahead, sr.readahead)
			}
		}
	}
	pool.mu.Unlock()

	release()
	pool.Close()
}
