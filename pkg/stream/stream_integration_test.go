// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
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

func createMultiPieceTestMetaInfo(t *testing.T) *metainfo.MetaInfo {
	t.Helper()
	info := &metainfo.Info{
		Name:        "test-torrent",
		PieceLength: 64,
		Length:      640,
		Pieces:      make([]byte, 200), // 10 pieces * 20-byte SHA1 hashes
	}
	infoBytes, err := bencode.Marshal(info)
	require.NoError(t, err)
	return &metainfo.MetaInfo{InfoBytes: infoBytes}
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

func TestPool_Acquire(t *testing.T) {
	t.Run("reuses idle reader", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addTestTorrent(t, c)

		infoHash := to.InfoHash()
		totalBudget := int64(1024 * 1024)

		pool := New(Config{
			Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			IdleParkTimeout: 30 * time.Second,
		})

		reader1, release1 := acquireTestReader(t, pool, f, MemoryStorage, totalBudget)
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

		buf := make([]byte, 1)
		if _, err := reader1.Read(buf); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("expected released wrapper to be closed after reuse, got %v", err)
		}

		release2()
		pool.Close()
	})

	t.Run("does not reuse reader from replaced torrent", func(t *testing.T) {
		c := newTestTorrentClient(t)
		metaInfo := createTestMetaInfo(t)
		oldTorrent, oldFile := addTestTorrentFromMetaInfo(t, c, metaInfo)
		pool := New(Config{
			Logger:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			IdleCloseTimeout:  -1,
			IdleParkTimeout:   30 * time.Second,
			MaxReadersPerFile: -1,
		})
		t.Cleanup(pool.Close)
		require.True(t, pool.SetReadaheadBudget(1024*1024))

		_, releaseOld, err := pool.Acquire(oldFile, MemoryStorage)
		require.NoError(t, err)

		pool.mu.Lock()
		var oldReaderID uint64
		for _, sr := range pool.readers {
			oldReaderID = sr.readerID
		}
		pool.mu.Unlock()
		require.NotZero(t, oldReaderID)

		oldTorrent.Drop()
		<-oldTorrent.Closed()
		newTorrent, newFile := addTestTorrentFromMetaInfo(t, c, metaInfo)
		require.NotSame(t, oldTorrent, newTorrent)

		// The old reader remains active while the replacement gets its own reader.
		// Releasing it afterward must close it instead of admitting it to the idle
		// pool, without requiring another acquisition to trigger cleanup.
		_, releaseNew, err := pool.Acquire(newFile, MemoryStorage)
		require.NoError(t, err)
		releaseNew()
		releaseOld()

		pool.mu.Lock()
		defer pool.mu.Unlock()
		require.Len(t, pool.readers, 1)
		for _, sr := range pool.readers {
			assert.NotEqual(t, oldReaderID, sr.readerID)
			assert.Same(t, newTorrent, sr.file.Torrent())
		}
	})

	t.Run("sets active range", func(t *testing.T) {
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
	})

	t.Run("file storage", func(t *testing.T) {
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
	})

	t.Run("file storage readahead", func(t *testing.T) {
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
	})

	t.Run("while closed", func(t *testing.T) {
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
			Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			IdleParkTimeout: 30 * time.Second,
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

func TestPoolStaleReaderCannotMoveReusedReader(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrent(t, c)

	pool := New(Config{
		Logger:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleParkTimeout: 30 * time.Second,
	})
	t.Cleanup(pool.Close)

	stale, releaseStale := acquireTestReader(t, pool, f, MemoryStorage, 1024*1024)
	releaseStale()
	_, releaseCurrent := acquireTestReader(t, pool, f, MemoryStorage, 1024*1024)
	defer releaseCurrent()

	pool.mu.Lock()
	var sr *streamReader
	for _, reader := range pool.readers {
		sr = reader
	}
	pool.mu.Unlock()
	require.NotNil(t, sr)

	// A caller that keeps its reader after release must neither read nor move
	// the torrent reader that the new lease now owns.
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

func TestPool_AcquirePreloadContext(t *testing.T) {
	t.Run("purges reader from replaced torrent", func(t *testing.T) {
		c := newTestTorrentClient(t)
		metaInfo := createTestMetaInfo(t)
		oldTorrent, oldFile := addTestTorrentFromMetaInfo(t, c, metaInfo)
		pool := New(Config{
			Logger:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			IdleCloseTimeout:  -1,
			IdleParkTimeout:   30 * time.Second,
			MaxReadersPerFile: -1,
		})
		t.Cleanup(pool.Close)
		require.True(t, pool.SetReadaheadBudget(1024*1024))

		_, releaseOld, err := pool.Acquire(oldFile, MemoryStorage)
		require.NoError(t, err)
		releaseOld()

		oldTorrent.Drop()
		<-oldTorrent.Closed()
		newTorrent, newFile := addTestTorrentFromMetaInfo(t, c, metaInfo)
		require.NotSame(t, oldTorrent, newTorrent)

		_, releasePreload, err := pool.AcquirePreloadContext(
			context.Background(), newFile, MemoryStorage, 0, newFile.Length(),
		)
		require.NoError(t, err)

		pool.mu.Lock()
		require.Len(t, pool.readers, 1)
		for _, sr := range pool.readers {
			assert.True(t, sr.isPreload)
			assert.Same(t, newTorrent, sr.file.Torrent())
		}
		pool.mu.Unlock()

		releasePreload()
		assert.False(t, pool.HasReaders(newTorrent.InfoHash()))
	})

	t.Run("uses bounded readahead without speculative protection", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrentFromMetaInfo(t, c, createMultiPieceTestMetaInfo(t))
		reg := &testRegistry{}
		pool := New(Config{
			Logger:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			Registry: reg,
		})
		pool.SetReadaheadBudget(1024 * 1024)

		const preloadRange = int64(256)
		_, release, err := pool.AcquirePreloadContext(context.Background(), f, MemoryStorage, 0, preloadRange)
		require.NoError(t, err)
		defer release()
		defer pool.Close()

		pool.mu.Lock()
		var key readerKey
		var preloadReader *streamReader
		for candidateKey, sr := range pool.readers {
			key = candidateKey
			preloadReader = sr
			assert.True(t, sr.isPreload)
			assert.Equal(t, preloadRange, sr.readahead)
			assert.Equal(t, preloadRange, sr.wrapper.fillLimit)
		}
		pool.mu.Unlock()
		require.NotNil(t, preloadReader)

		// Rebalancing playback readers must not erase the preload's reserved,
		// range-sized scheduling window.
		pool.SetReadaheadBudget(2 * 1024 * 1024)
		pool.mu.Lock()
		assert.Equal(t, preloadRange, preloadReader.readahead)
		pool.mu.Unlock()

		// Every piece in the preload range is claimed at Now immediately, independent
		// of playback's moving priority-window fractions.
		preloadReader.priorityMu.Lock()
		assert.Equal(t, []int{0, 1, 2, 3}, preloadReader.prioritizedPieces)
		pool.priorityMu.Lock()
		for _, index := range preloadReader.prioritizedPieces {
			claim := pool.priorityClaims[priorityPieceKey{torrent: f.Torrent(), index: index}]
			require.NotNil(t, claim)
			assert.Equal(t, torrent.PiecePriorityNow, claim.owners[preloadReader])
		}
		pool.priorityMu.Unlock()
		preloadReader.priorityMu.Unlock()

		// Reader movement shrinks readahead but keeps the complete static priority
		// claim until release. Cache protection remains controller-owned.
		pool.updateActiveRange(f.Torrent().InfoHash(), key, f, nil, 64)
		pool.mu.Lock()
		assert.Equal(t, preloadRange-64, preloadReader.readahead)
		pool.mu.Unlock()
		pool.updateActiveRange(f.Torrent().InfoHash(), key, f, nil, preloadRange)
		preloadReader.priorityMu.Lock()
		assert.Equal(t, []int{0, 1, 2, 3}, preloadReader.prioritizedPieces)
		preloadReader.priorityMu.Unlock()
		assert.Zero(t, reg.setCalls.Load(), "controller owns preload range protection")
		assert.Zero(t, reg.boundarySetCalls.Load(), "controller owns preload range protection")

		release()
		pool.priorityMu.Lock()
		assert.Empty(t, pool.priorityClaims)
		pool.priorityMu.Unlock()
	})

	t.Run("does not consume idle playback reader", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrentFromMetaInfo(t, c, createMultiPieceTestMetaInfo(t))
		pool := New(Config{
			Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		})
		defer pool.Close()
		pool.SetReadaheadBudget(1024 * 1024)

		_, playbackRelease, err := pool.AcquireContext(context.Background(), f, MemoryStorage)
		require.NoError(t, err)
		playbackRelease()

		pool.mu.Lock()
		require.Len(t, pool.readers, 1)
		var playbackKey readerKey
		for key := range pool.readers {
			playbackKey = key
		}
		pool.mu.Unlock()

		_, preloadRelease, err := pool.AcquirePreloadContext(context.Background(), f, MemoryStorage, 0, 256)
		require.NoError(t, err)

		pool.mu.Lock()
		assert.Len(t, pool.readers, 2, "preload must not take over the parked playback reader")
		parked, ok := pool.readers[playbackKey]
		require.True(t, ok)
		assert.False(t, parked.isPreload)
		assert.False(t, parked.active)
		pool.mu.Unlock()

		preloadRelease()

		// The preload reader is closed and removed; the parked playback reader
		// survives so resumed playback reuses a warm reader.
		pool.mu.Lock()
		defer pool.mu.Unlock()
		assert.Len(t, pool.readers, 1)
		survivor, ok := pool.readers[playbackKey]
		require.True(t, ok)
		assert.False(t, survivor.isPreload)
		assert.NotNil(t, survivor.reader)
	})

	t.Run("file storage uses requested readahead", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, f := addTestTorrentFromMetaInfo(t, c, createMultiPieceTestMetaInfo(t))
		pool := New(Config{
			Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			FileReadaheadBytes: 50 * 1024 * 1024,
		})
		defer pool.Close()

		const preloadRange = int64(192)
		_, release, err := pool.AcquirePreloadContext(context.Background(), f, FileStorage, 64, 64+preloadRange)
		require.NoError(t, err)
		defer release()

		pool.mu.Lock()
		defer pool.mu.Unlock()
		for _, sr := range pool.readers {
			assert.True(t, sr.isPreload)
			assert.Equal(t, preloadRange, sr.readahead)
		}
	})
}

func TestPool_ReaderPositions(t *testing.T) {
	t.Run("single reader", func(t *testing.T) {
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
	})

	t.Run("multiple readers", func(t *testing.T) {
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
	})

	t.Run("wrong info hash", func(t *testing.T) {
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
		p.readers[readerKey{infoHash: infoHash, filePath: "f1", readerID: 1}] = &streamReader{
			file: nil,
		}
		// Add reader with file that has nil torrent.
		p.readers[readerKey{infoHash: infoHash, filePath: "f2", readerID: 2}] = &streamReader{
			file: &torrent.File{},
		}

		// Should not panic, and returns empty slice.
		result := p.ReaderPositions(infoHash)
		if len(result) != 0 {
			t.Fatalf("expected 0 valid readers, got %d", len(result))
		}
	})
}

// TestPreloadPriorityPlan verifies that preloadPriorityPlan includes every intersecting piece.
func TestPreloadPriorityPlan(t *testing.T) {
	c := newTestTorrentClient(t)
	_, f := addTestTorrentFromMetaInfo(t, c, createMultiPieceTestMetaInfo(t))

	tests := []struct {
		name       string
		start, end int64
		want       []int
	}{
		{name: "empty", start: 64, end: 64, want: []int{}},
		{name: "partial piece", start: 1, end: 64, want: []int{0}},
		{name: "aligned piece", start: 64, end: 128, want: []int{1}},
		{name: "crosses boundary", start: 63, end: 65, want: []int{0, 1}},
		{name: "tail", start: 576, end: 640, want: []int{9}},
		{name: "clamped to file", start: -1, end: 641, want: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := preloadPriorityPlan(f, tt.start, tt.end)
			indexes := make([]int, 0, len(plan))
			for _, piece := range plan {
				indexes = append(indexes, piece.index)
				assert.Equal(t, torrent.PiecePriorityNow, piece.priority)
			}
			assert.Equal(t, tt.want, indexes)
		})
	}
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
				require.True(t, pool.SetReadaheadBudget(budget))

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
				require.True(t, pool.SetReadaheadBudget(budget))

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

	key := readerKey{infoHash: to.InfoHash(), filePath: file.Path(), readerID: 1}
	pool.updateActiveRange(to.InfoHash(), key, file, nil, 31)
	setsBeforeBoundary := reg.sets
	pool.updateActiveRange(to.InfoHash(), key, file, nil, 32)
	assert.Equal(t, setsBeforeBoundary+1, reg.sets, "crossing a torrent piece must refresh the active range")
	assert.Equal(t, 2, reg.last.endPiece, "readahead must follow the actual torrent piece")

	positions := pool.ReaderPositions(to.InfoHash())
	require.Len(t, positions, 1)
	assert.Equal(t, 1, positions[0].Position)
	assert.Equal(t, 2, positions[0].End)

	plan := pool.prioritizeNextPieces(file, 32, 128, 1)
	require.Len(t, plan, 2)
	assert.Equal(t, 2, plan[0].index, "the first priority should be beyond the current torrent piece")
	assert.Equal(t, 3, plan[1].index)
}

func TestPoolPriorityClaimsForOverlappingReaders(t *testing.T) {
	c := newTestTorrentClient(t)
	to, file := addMultiPieceTorrent(t, c)
	p := New(Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))})
	defer p.Close()

	sr1 := &streamReader{file: file}
	sr2 := &streamReader{file: file}
	planned := p.prioritizeNextPieces(file, 0, 256, 1)
	if len(planned) == 0 {
		t.Fatal("expected a non-empty priority plan")
	}
	for _, piece := range planned {
		if piece.index >= file.EndPieceIndex() {
			t.Fatalf("priority plan crossed exclusive file end: piece=%d end=%d", piece.index, file.EndPieceIndex())
		}
	}
	boundedPlan := p.prioritizeNextPieces(file, 0, 1<<40, 1)
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
