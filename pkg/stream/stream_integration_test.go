// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/pkg/storage"
)

// newTestTorrentClient creates a minimal in-memory torrent client for integration tests.
func newTestTorrentClient(t *testing.T) *torrent.Client {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	maxMemory := int64(16 * 1024 * 1024) // 16 MB
	storageClient := storage.NewClient(maxMemory, logger)

	cfg := torrent.NewDefaultClientConfig()
	cfg.DefaultStorage = storageClient
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.DisablePEX = true

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

func TestAcquireAndRelease(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
	})

	reader, release := pool.Acquire(context.Background(), ih, f, false, totalPool)
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	release()

	// Verify reader moved to idle state.
	pool.mu.Lock()
	found := false
	for _, sr := range pool.readers {
		if sr.hash == ih && sr.file == f && !sr.active {
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

func TestAcquireReuseIdleReader(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
	})

	_, release1 := pool.Acquire(context.Background(), ih, f, false, totalPool)
	release1()

	// Second acquire should reuse the idle reader.
	_, release2 := pool.Acquire(context.Background(), ih, f, false, totalPool)

	// Verify only one reader exists.
	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.hash == ih {
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

func TestReaderPositionsIntegration(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
	})

	result := pool.ReaderPositions(ih)
	if len(result) != 0 {
		t.Fatalf("expected 0 readers, got %d", len(result))
	}

	_, release := pool.Acquire(context.Background(), ih, f, false, totalPool)

	result = pool.ReaderPositions(ih)
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

func TestReaderPositionsMultipleReaders(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
	})

	_, release1 := pool.Acquire(context.Background(), ih, f, false, totalPool)
	_, release2 := pool.Acquire(context.Background(), ih, f, false, totalPool)

	result := pool.ReaderPositions(ih)
	if len(result) != 2 {
		t.Fatalf("expected 2 reader positions, got %d", len(result))
	}

	release1()
	release2()
	pool.Close()
}

func TestReaderPositionsWrongHash(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
	})

	_, release := pool.Acquire(context.Background(), ih, f, false, totalPool)

	// Query with a different hash.
	wrongHash := metainfo.Hash{}
	result := pool.ReaderPositions(wrongHash)
	if len(result) != 0 {
		t.Fatalf("expected 0 readers for wrong hash, got %d", len(result))
	}

	release()
	pool.Close()
}

func TestActiveRangeSetOnAcquire(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	var registrySetCalls int
	var registryClearCalls int

	reg := &testRegistry{
		setCalls:   &registrySetCalls,
		clearCalls: &registryClearCalls,
	}

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
		Registry:    reg,
	})

	_, release := pool.Acquire(context.Background(), ih, f, false, totalPool)
	if registrySetCalls != 1 {
		t.Fatalf("expected 1 SetActiveRange call, got %d", registrySetCalls)
	}

	release()
	if registryClearCalls != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", registryClearCalls)
	}

	pool.Close()
}

type testRegistry struct {
	setCalls   *int
	clearCalls *int
}

func (r *testRegistry) SetActiveRange(_ metainfo.Hash, _ uint64, _, _ int64) {
	*r.setCalls++
}

func (r *testRegistry) ClearActiveRange(_ metainfo.Hash, _ uint64) {
	*r.clearCalls++
}

func TestParkIdleReadersRemovesAfterMaxIdleTime(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 1 * time.Millisecond,
		MaxIdleTime: 10 * time.Millisecond,
	})

	_, release := pool.Acquire(context.Background(), ih, f, false, totalPool)
	release()

	// Wait for MaxIdleTime to pass.
	time.Sleep(20 * time.Millisecond)

	// Trigger the GC manually.
	pool.parkIdleReaders()

	pool.mu.Lock()
	count := 0
	for _, sr := range pool.readers {
		if sr.hash == ih {
			count++
		}
	}
	pool.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 readers after MaxIdleTime, got %d", count)
	}

	pool.Close()
}

func TestAcquireWithFileStorage(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()
	totalPool := int64(1024 * 1024)

	pool := New(Config{
		Logger:               slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout:          30 * time.Second,
		FileStorageReadahead: 50 * 1024 * 1024,
	})

	reader, release := pool.Acquire(context.Background(), ih, f, true, totalPool)
	if reader == nil {
		t.Fatal("expected non-nil reader for file storage")
	}

	release()
	pool.Close()
}

func TestAcquireWhileClosed(t *testing.T) {
	c := newTestTorrentClient(t)
	to, f := addTestTorrent(t, c)

	ih := to.InfoHash()

	pool := New(Config{
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		IdleTimeout: 30 * time.Second,
	})

	pool.Close()

	reader, release := pool.Acquire(context.Background(), ih, f, false, 1024*1024)
	defer release()

	if reader == nil {
		t.Fatal("expected non-nil reader from closed pool")
	}

	pool.mu.Lock()
	count := len(pool.readers)
	pool.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 tracked readers, got %d", count)
	}
}
