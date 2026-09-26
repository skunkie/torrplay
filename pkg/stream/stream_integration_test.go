// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"crypto/sha1"
	"errors"
	"fmt"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func createMisalignedFileTestMetaInfo(t *testing.T) *metainfo.MetaInfo {
	t.Helper()
	info := &metainfo.Info{
		Name:        "split-torrent",
		PieceLength: 64,
		Pieces:      make([]byte, 180), // 9 pieces of 64 bytes.
		Files: []metainfo.FileInfo{
			{Length: 32, Path: []string{"header.bin"}},
			{Length: 512, Path: []string{"video.bin"}},
		},
	}
	infoBytes, err := bencode.Marshal(info)
	require.NoError(t, err)
	return &metainfo.MetaInfo{InfoBytes: infoBytes}
}

// addTestTorrent creates a proper torrent with a single 64-byte file.
func addTestTorrent(t *testing.T, c *torrent.Client) (*torrent.Torrent, *torrent.File) {
	t.Helper()
	return addTestTorrentFromMetaInfo(t, c, createTestMetaInfo(t))
}

func addTestTorrentFromMetaInfo(t *testing.T, c *torrent.Client, mi *metainfo.MetaInfo) (*torrent.Torrent, *torrent.File) {
	t.Helper()

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

func TestPoolReaderAcquisitionCycle(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	infoHash := to.InfoHash()
	totalBudget := int64(1024 * 1024)

	pool := New(Config{
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		LingerTimeout: 30 * time.Second,
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

func TestPool_Acquire(t *testing.T) {
	t.Run("replaces a lingering reader", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addTestTorrent(t, c)
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
		})
		defer pool.Close()

		reader1, release1 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
		release1()

		pool.mu.Lock()
		require.Len(t, pool.readers, 1)
		var lingeringKey uint64
		var lingering *streamReader
		for key, sr := range pool.readers {
			lingeringKey, lingering = key, sr
		}
		assert.False(t, lingering.active)
		assert.Positive(t, lingering.readahead, "a lingering reader keeps reading ahead")
		pool.mu.Unlock()

		buf := make([]byte, 1)
		if _, err := reader1.Read(buf); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("expected released wrapper to be retired, got %v", err)
		}

		// The next request gets a new reader, and the lingering one closes.
		_, release2 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
		defer release2()
		pool.mu.Lock()
		defer pool.mu.Unlock()
		assert.Len(t, pool.readers, 1)
		_, stillLingering := pool.readers[lingeringKey]
		assert.False(t, stillLingering, "new work must close the lingering reader")
		assert.Nil(t, lingering.reader)
		for _, sr := range pool.readers {
			assert.True(t, sr.active)
			assert.Equal(t, to.InfoHash(), sr.infoHash)
		}
	})

	t.Run("closes a released reader while other work is active", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrent(t, c)
		pool := New(Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))})
		defer pool.Close()
		pool.SetReadaheadBudget(1024 * 1024)

		_, releaseFirst, err := pool.Acquire(f, MemoryStorage)
		require.NoError(t, err)
		_, releaseSecond, err := pool.Acquire(f, MemoryStorage)
		require.NoError(t, err)

		releaseFirst()
		pool.mu.Lock()
		assert.Len(t, pool.readers, 1, "a reader released during other work must not linger")
		pool.mu.Unlock()

		releaseSecond()
		pool.mu.Lock()
		assert.Len(t, pool.readers, 1, "the last released reader lingers")
		pool.mu.Unlock()
	})

	t.Run("keeps a lingering reader while another file streams", func(t *testing.T) {
		c := newTestTorrentClient(t)
		first, firstFile := addSizedTorrent(t, c, "first", 64, 640)
		_, secondFile := addSizedTorrent(t, c, "second", 64, 640)
		pool := New(Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))})
		defer pool.Close()
		pool.SetReadaheadBudget(1024 * 1024)

		_, releaseFirst, err := pool.Acquire(firstFile, MemoryStorage)
		require.NoError(t, err)
		releaseFirst()
		_, releaseSecond, err := pool.Acquire(secondFile, MemoryStorage)
		require.NoError(t, err)
		defer releaseSecond()

		assert.True(t, pool.HasReaders(first.InfoHash()), "the engine is shared, so another viewer must not close the reader")
		assert.False(t, pool.HasActiveReaders(first.InfoHash()))
	})

	t.Run("closes a reader of a dropped torrent on release", func(t *testing.T) {
		c := newTestTorrentClient(t)
		oldTorrent, oldFile := addTestTorrentFromMetaInfo(t, c, createTestMetaInfo(t))
		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
		})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1024 * 1024)

		_, releaseOld, err := pool.Acquire(oldFile, MemoryStorage)
		require.NoError(t, err)
		oldTorrent.Drop()
		<-oldTorrent.Closed()

		releaseOld()
		assert.False(t, pool.HasReaders(oldTorrent.InfoHash()), "a dropped torrent's reader must not linger")
	})

	t.Run("sets active range", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrent(t, c)
		totalBudget := int64(1024 * 1024)

		reg := &testRegistry{}

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
			Registry:      reg,
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
	})

	t.Run("file storage", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrent(t, c)
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout:      30 * time.Second,
			FileReadaheadBytes: 50 * 1024 * 1024,
		})

		reader, release := acquireTestReader(t, pool, f, FileStorage, totalBudget)
		if reader == nil {
			t.Fatal("expected non-nil reader for file storage")
		}

		release()
		pool.Close()
	})

	t.Run("file storage readahead", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addTestTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)
		wantReadahead := int64(75 * 1024 * 1024)

		pool := New(Config{
			Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout:      30 * time.Second,
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
	})

	t.Run("while closed", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrent(t, c)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
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
	})

	t.Run("rejects invalid storage mode", func(t *testing.T) {
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
	})

	t.Run("rejects invalid file", func(t *testing.T) {
		pool := New(Config{})
		t.Cleanup(pool.Close)

		reader, release, err := pool.Acquire(nil, MemoryStorage)
		if !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("expected ErrInvalidFile, got %v", err)
		}
		if reader != nil || release != nil {
			t.Fatal("expected no reader or release function")
		}
	})

	t.Run("concurrent acquires", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addMultiPieceTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)
		iterations := 50

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
		})

		var wg sync.WaitGroup
		var successCount atomic.Int32
		for range iterations {
			wg.Go(func() {
				reader, release := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
				if reader == nil {
					t.Error("expected non-nil reader")
					return
				}
				successCount.Add(1)
				release()
			})
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
	})
}

func TestPoolStaleReaderCannotMoveLingeringReader(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrent(t, c)

	pool := New(Config{
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		LingerTimeout: 30 * time.Second,
	})
	t.Cleanup(pool.Close)

	stale, releaseStale := acquireTestReader(t, pool, f, MemoryStorage, 1024*1024)
	releaseStale()

	pool.mu.Lock()
	var sr *streamReader
	for _, reader := range pool.readers {
		sr = reader
	}
	pool.mu.Unlock()
	require.NotNil(t, sr, "the released reader lingers")

	// A caller that keeps its reader after release must neither read nor move
	// the lingering torrent reader.
	_, err := stale.Seek(f.Length()/2, io.SeekStart)
	require.NoError(t, err)
	_, err = stale.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.ErrClosedPipe)

	pos, err := sr.reader.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	assert.Zero(t, pos)
	pool.mu.Lock()
	assert.Zero(t, sr.lastOffset)
	pool.mu.Unlock()
}

func TestPool_ReaderPositions(t *testing.T) {
	t.Run("single reader", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addTestTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
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
	})

	t.Run("multiple readers", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addTestTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
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
	})

	t.Run("wrong info hash", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrent(t, c)
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
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
	})

	t.Run("empty pool", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		result := p.ReaderPositions(infoHash)
		if len(result) != 0 {
			t.Fatalf("expected 0 readers, got %d", len(result))
		}
	})

	t.Run("nil file guards", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{1}

		// Add reader with nil file.
		p.readers[uint64(1)] = &streamReader{
			file: nil,
		}
		// Add reader with file that has nil torrent.
		p.readers[uint64(2)] = &streamReader{
			file: &torrent.File{},
		}

		// Should not panic, and returns empty slice.
		result := p.ReaderPositions(infoHash)
		if len(result) != 0 {
			t.Fatalf("expected 0 valid readers, got %d", len(result))
		}
	})
}

type testRegistry struct {
	setCalls         atomic.Int32
	clearCalls       atomic.Int32
	boundarySetCalls atomic.Int32
	mu               sync.Mutex
	lastRange        struct {
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

func (r *testRegistry) SetFileBoundaries(_ metainfo.Hash, _ uint64, _, _, _, _ int) {
	r.boundarySetCalls.Add(1)
}

func (r *testRegistry) ClearFileBoundaries(_ metainfo.Hash, _ uint64) {}

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

func TestComputeRange(t *testing.T) {
	t.Run("file start", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addMultiPieceTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
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
		wantEnd := f.EndPieceIndex() - 1
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
	})

	t.Run("mid file", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addMultiPieceTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 30 * time.Second,
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
		wantEnd := f.EndPieceIndex() - 1
		if ri.End != wantEnd {
			t.Fatalf("expected End=%d (last inclusive piece), got %d", wantEnd, ri.End)
		}

		// Position must reflect piece 5 (offset 320 / 64 bytes per piece).
		if ri.Position != 5 {
			t.Fatalf("expected Position=5 (piece for offset 320), got %d", ri.Position)
		}

		release()
		pool.Close()
	})
}

// protectionRegistry records the ranges each reader currently protects.
type protectionRegistry struct {
	mu         sync.Mutex
	ranges     map[uint64]activeRange
	boundaries map[uint64]testFileBoundary
}

func newProtectionRegistry() *protectionRegistry {
	return &protectionRegistry{ranges: make(map[uint64]activeRange), boundaries: make(map[uint64]testFileBoundary)}
}

func (r *protectionRegistry) SetActiveRange(_ metainfo.Hash, readerID uint64, start, end int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranges[readerID] = activeRange{startPiece: start, endPiece: end}
}

func (r *protectionRegistry) ClearActiveRange(_ metainfo.Hash, readerID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ranges, readerID)
}

func (r *protectionRegistry) SetFileBoundaries(_ metainfo.Hash, readerID uint64, headStart, headEnd, tailStart, tailEnd int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.boundaries[readerID] = testFileBoundary{headStart: headStart, headEnd: headEnd, tailStart: tailStart, tailEnd: tailEnd}
}

func (r *protectionRegistry) ClearFileBoundaries(_ metainfo.Hash, readerID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.boundaries, readerID)
}

// protectedPieces returns the distinct pieces protected by any reader.
func (r *protectionRegistry) protectedPieces() map[int]struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	pieces := make(map[int]struct{})
	add := func(start, end int) {
		for index := start; index <= end; index++ {
			pieces[index] = struct{}{}
		}
	}
	for _, protected := range r.ranges {
		add(protected.startPiece, protected.endPiece)
	}
	for _, boundary := range r.boundaries {
		add(boundary.headStart, boundary.headEnd)
		add(boundary.tailStart, boundary.tailEnd)
	}
	return pieces
}

func TestPoolProtectedPiecesFitReadaheadBudget(t *testing.T) {
	const budget = int64(32 << 20) // default 64 MiB memory limit at 50 %
	for _, pieceLength := range []int64{1 << 20, 4 << 20, 16 << 20} {
		for _, readers := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dMiB pieces %d readers", pieceLength>>20, readers), func(t *testing.T) {
				length := int64(8<<30) + 12345
				info := metainfo.Info{
					Name:        "movie.mkv",
					PieceLength: pieceLength,
					Length:      length,
					Pieces:      make([]byte, (length+pieceLength-1)/pieceLength*sha1.Size),
				}
				infoBytes, err := bencode.Marshal(info)
				require.NoError(t, err)
				c := newTestTorrentClient(t)
				_, file := addTestTorrentFromMetaInfo(t, c, &metainfo.MetaInfo{InfoBytes: infoBytes})

				reg := newProtectionRegistry()
				pool := New(Config{Registry: reg, Logger: testLogger()})
				defer pool.Close()
				pool.SetReadaheadBudget(budget)

				for range readers {
					_, release, err := pool.Acquire(file, MemoryStorage)
					require.NoError(t, err)
					t.Cleanup(release)
				}
				// Place the readers apart so their ranges do not overlap, then
				// re-register protection at those positions.
				pool.mu.Lock()
				offset := int64(1 << 30)
				for _, sr := range pool.readers {
					sr.lastOffset = offset
					offset += 1 << 30
				}
				pool.mu.Unlock()
				pool.SetReadaheadBudget(budget)

				pieces := reg.protectedPieces()
				reg.mu.Lock()
				registered := len(reg.ranges)
				reg.mu.Unlock()
				require.Equal(t, readers, registered)
				protected := int64(len(pieces)) * pieceLength
				assert.LessOrEqual(t, protected, budget, "protected %d pieces of %d MiB", len(pieces), pieceLength>>20)

				pool.mu.Lock()
				for _, sr := range pool.readers {
					assert.Zero(t, sr.readahead%pieceLength, "readahead must be whole pieces")
				}
				pool.mu.Unlock()
			})
		}
	}
}

func TestPoolMisalignedFileOffsets(t *testing.T) {
	c := newTestTorrentClient(t)
	to, _ := addTestTorrentFromMetaInfo(t, c, createMisalignedFileTestMetaInfo(t))
	file := to.Files()[1]
	require.Equal(t, int64(32), file.Offset())
	reg := &stubRegistry{}
	pool := New(Config{Registry: reg, Logger: testLogger()})
	defer pool.Close()
	// Two 64-byte pieces: the position piece and one piece of readahead.
	pool.SetReadaheadBudget(128)

	_, release, err := pool.Acquire(file, MemoryStorage)
	require.NoError(t, err)
	defer release()

	key := uint64(1)
	pool.updateActiveRange(key, 31)
	setsBeforeBoundary := reg.sets
	pool.updateActiveRange(key, 32)
	assert.Equal(t, setsBeforeBoundary+1, reg.sets, "crossing a torrent piece must refresh the active range")
	assert.Equal(t, 2, reg.last.endPiece, "readahead must follow the actual torrent piece")

	positions := pool.ReaderPositions(to.InfoHash())
	require.Len(t, positions, 1)
	assert.Equal(t, 1, positions[0].Position)
	assert.Equal(t, 2, positions[0].End)
}

func TestFilePieceRanges(t *testing.T) {
	c := newTestTorrentClient(t)
	to, _ := addTestTorrentFromMetaInfo(t, c, createMisalignedFileTestMetaInfo(t))
	// video.bin spans torrent bytes [32, 544) across 64-byte pieces 0 through 8.
	video := to.Files()[1]

	tests := []struct {
		name                        string
		headEnd, tailStart, tailEnd int64
		want                        [4]int
		ok                          bool
	}{
		{name: "head and tail", headEnd: 32, tailStart: 480, tailEnd: 512, want: [4]int{0, 0, 8, 8}, ok: true},
		{name: "head crosses a piece boundary", headEnd: 33, tailStart: 479, tailEnd: 512, want: [4]int{0, 1, 7, 8}, ok: true},
		{name: "empty tail repeats head", headEnd: 100, want: [4]int{0, 2, 0, 2}, ok: true},
		{name: "empty head", headEnd: 0, tailStart: 480, tailEnd: 512},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headStart, headEnd, tailStart, tailEnd, ok := filePieceRanges(video, tt.headEnd, tt.tailStart, tt.tailEnd)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.want, [4]int{headStart, headEnd, tailStart, tailEnd})
			}
		})
	}

	t.Run("nil file", func(t *testing.T) {
		_, _, _, _, ok := filePieceRanges(nil, 32, 0, 0)
		assert.False(t, ok)
	})
}

func TestReaderWindow(t *testing.T) {
	c := newTestTorrentClient(t)
	to, _ := addTestTorrentFromMetaInfo(t, c, createMisalignedFileTestMetaInfo(t))
	// video.bin spans torrent bytes [32, 544) across 64-byte pieces 0 through 8.
	video := to.Files()[1]

	tests := []struct {
		name                 string
		readahead, offset    int64
		start, position, end int
	}{
		{name: "start of file", readahead: 0, offset: 0, start: 0, position: 0, end: 0},
		{name: "inside the file", readahead: 4 * 64, offset: 200, start: 2, position: 3, end: 7},
		{name: "clamped to the last piece", readahead: 4 * 64, offset: 500, start: 7, position: 8, end: 8},
		{name: "offset past the end", readahead: 0, offset: 10_000, start: 8, position: 8, end: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, position, end, ok := readerWindow(video, tt.readahead, tt.offset)
			require.True(t, ok)
			assert.Equal(t, [3]int{tt.start, tt.position, tt.end}, [3]int{start, position, end})
		})
	}

	t.Run("without piece metadata", func(t *testing.T) {
		_, _, _, ok := readerWindow(&torrent.File{}, 64, 0)
		assert.False(t, ok)
		assert.Equal(t, int64(100), filePiece(&torrent.File{}, 100), "a position change still reads as a piece change")
	})
}

func TestFitFileBoundaries(t *testing.T) {
	c := newTestTorrentClient(t)
	to, _ := addTestTorrentFromMetaInfo(t, c, createMisalignedFileTestMetaInfo(t))
	video := to.Files()[1]

	// 64-byte boundaries cover head pieces 0 and 1 and tail pieces 7 and 8,
	// the torrent's 32-byte final piece. Costing that piece at its real size
	// lets them fit within 224 bytes.
	boundaryBytes, cost, ok := fitFileBoundaries(video, 224)
	require.True(t, ok)
	assert.Equal(t, int64(64), boundaryBytes)
	assert.Equal(t, int64(64+64+64+32), cost)

	_, _, ok = fitFileBoundaries(video, 128)
	assert.False(t, ok, "four boundary pieces cannot fit within 128 bytes")
}

func TestPoolPriorityClaimsForOverlappingReaders(t *testing.T) {
	c := newTestTorrentClient(t)
	to, file := addMultiPieceTorrent(t, c)
	p := New(Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))})
	defer p.Close()

	sr1 := &streamReader{file: file}
	sr2 := &streamReader{file: file}
	planned := prioritizeNextPieces(file, 0, 256, 1)
	if len(planned) == 0 {
		t.Fatal("expected a non-empty priority plan")
	}
	for _, piece := range planned {
		if piece.index >= file.EndPieceIndex() {
			t.Fatalf("priority plan crossed exclusive file end: piece=%d end=%d", piece.index, file.EndPieceIndex())
		}
	}
	boundedPlan := prioritizeNextPieces(file, 0, 1<<40, 1)
	if cap(boundedPlan) > file.EndPieceIndex()-file.BeginPieceIndex() {
		t.Fatalf("priority plan capacity should be bounded by file pieces, got %d", cap(boundedPlan))
	}

	p.mu.Lock()
	sr1.prioritizedPieces = p.claimLocked(sr1, to, sr1.prioritizedPieces, planned)
	sr2.prioritizedPieces = p.claimLocked(sr2, to, sr2.prioritizedPieces, planned)
	p.mu.Unlock()

	key := priorityPieceKey{torrent: to, index: planned[0].index}
	p.mu.Lock()
	claim := p.priorityClaims[key]
	ownerCount := 0
	if claim != nil {
		ownerCount = len(claim.owners)
	}
	p.mu.Unlock()
	if ownerCount != 2 {
		t.Fatalf("expected two owners for overlapping piece, got %d", ownerCount)
	}

	p.mu.Lock()
	p.unclaimLocked(sr1, to, sr1.prioritizedPieces)
	p.mu.Unlock()

	p.mu.Lock()
	claim = p.priorityClaims[key]
	ownerCount = 0
	secondReaderStillOwns := false
	if claim != nil {
		ownerCount = len(claim.owners)
		_, secondReaderStillOwns = claim.owners[sr2]
	}
	p.mu.Unlock()
	if ownerCount != 1 || !secondReaderStillOwns {
		t.Fatalf("expected the second reader's claim to survive, owners=%d second=%v", ownerCount, secondReaderStillOwns)
	}

	p.mu.Lock()
	p.unclaimLocked(sr2, to, sr2.prioritizedPieces)
	p.mu.Unlock()
	p.mu.Lock()
	_, stillClaimed := p.priorityClaims[key]
	p.mu.Unlock()
	if stillClaimed {
		t.Fatal("expected final owner release to remove the piece claim")
	}
}
