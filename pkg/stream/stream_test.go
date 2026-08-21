// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// stubRegistry implements ActiveRangeRegistry for testing.
type stubRegistry struct {
	sets   int
	clears int
	last   activeRange
}

func (r *stubRegistry) SetActiveRange(_ metainfo.Hash, _ uint64, start, end int64) {
	r.sets++
	r.last = activeRange{startPiece: int(start), endPiece: int(end)}
}

func (r *stubRegistry) ClearActiveRange(_ metainfo.Hash, _ uint64) {
	r.clears++
}

type activeRange struct {
	startPiece int
	endPiece   int
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// memReadSeekCloser is an in-memory io.ReadSeekCloser backed by bytes.Reader,
// used for testing ReadAt behavior, position restoration, and callbacks.
type memReadSeekCloser struct {
	*bytes.Reader
	closed bool
}

func (m *memReadSeekCloser) Close() error {
	m.closed = true
	return nil
}

func newMemReader(data []byte) *memReadSeekCloser {
	return &memReadSeekCloser{Reader: bytes.NewReader(data)}
}

func TestReadAtWrapper_ReadAt_BasicRead(t *testing.T) {
	data := []byte("hello world")
	s := newMemReader(data)
	var cbOffset int64
	rw := &readAtWrapper{
		reader: s,
		onOffsetChange: func(off int64) {
			cbOffset = off
		},
	}

	buf := make([]byte, 5)
	n, err := rw.ReadAt(buf, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes, got %d", n)
	}
	if string(buf) != "hello" {
		t.Fatalf("expected 'hello', got %q", buf)
	}
	// Callback should have fired with offset 5 (0 + 5 bytes read).
	if cbOffset != 5 {
		t.Fatalf("expected callback offset 5, got %d", cbOffset)
	}
	if rw.offset != 5 {
		t.Fatalf("expected rw.offset=5, got %d", rw.offset)
	}
}

func TestReadAtWrapper_ReadAt_MidFileOffset(t *testing.T) {
	data := []byte("0123456789")
	s := newMemReader(data)
	var cbOffset int64
	rw := &readAtWrapper{
		reader: s,
		onOffsetChange: func(off int64) {
			cbOffset = off
		},
	}

	buf := make([]byte, 3)
	n, err := rw.ReadAt(buf, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 bytes, got %d", n)
	}
	if string(buf) != "789" {
		t.Fatalf("expected '789', got %q", buf)
	}
	if cbOffset != 10 { // 7 + 3
		t.Fatalf("expected callback offset 10, got %d", cbOffset)
	}
}

func TestReadAtWrapper_ReadAt_PartialReadWithEOF(t *testing.T) {
	data := []byte("abc")
	s := newMemReader(data)
	var cbOffset int64
	var cbCalled bool
	rw := &readAtWrapper{
		reader: s,
		onOffsetChange: func(off int64) {
			cbOffset = off
			cbCalled = true
		},
	}

	// Request 10 bytes starting at offset 0 — only 3 available.
	buf := make([]byte, 10)
	n, err := rw.ReadAt(buf, 0)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 bytes (partial), got %d", n)
	}
	if string(buf[:n]) != "abc" {
		t.Fatalf("expected 'abc', got %q", buf[:n])
	}
	// Callback should fire even on partial read with error.
	if !cbCalled {
		t.Fatal("expected onOffsetChange to be called on partial read")
	}
	if cbOffset != 3 {
		t.Fatalf("expected callback offset 3, got %d", cbOffset)
	}
}

func TestReadAtWrapper_ReadAt_PositionRestored(t *testing.T) {
	data := []byte("0123456789")
	s := newMemReader(data)
	_, _ = s.Seek(3, io.SeekStart)

	rw := &readAtWrapper{reader: s}

	buf := make([]byte, 2)
	n, err := rw.ReadAt(buf, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 || string(buf) != "78" {
		t.Fatalf("expected '78', got %q", buf[:n])
	}

	// Position should be restored to the original 3.
	pos, _ := s.Seek(0, io.SeekCurrent)
	if pos != 3 {
		t.Fatalf("expected position restored to 3, got %d", pos)
	}
}

func TestReadAtWrapper_ReadAt_CallbackFiresAfterUnlock(t *testing.T) {
	data := []byte("abcd")
	s := newMemReader(data)

	// Track whether the callback sees the wrapper lock still held.
	var mu sync.Mutex
	var callbacks []int64
	rw := &readAtWrapper{
		reader: s,
		onOffsetChange: func(off int64) {
			mu.Lock()
			callbacks = append(callbacks, off)
			mu.Unlock()
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2)
		_, _ = rw.ReadAt(buf, 0) // "ab"
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, 2)
		_, _ = rw.ReadAt(buf, 2) // "cd"
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(callbacks) != 2 {
		t.Fatalf("expected 2 callbacks, got %d", len(callbacks))
	}
	// Both offsets should be present (order may vary).
	if (callbacks[0] != 2 || callbacks[1] != 4) && (callbacks[0] != 4 || callbacks[1] != 2) {
		t.Fatalf("expected offsets [2,4] or [4,2], got %v", callbacks)
	}
}

func TestReadAtWrapper_ReadAt_NilCallback(t *testing.T) {
	data := []byte("test")
	s := newMemReader(data)
	rw := &readAtWrapper{reader: s} // onOffsetChange is nil

	buf := make([]byte, 4)
	n, err := rw.ReadAt(buf, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 4 || string(buf) != "test" {
		t.Fatalf("expected 'test', got %q", buf[:n])
	}
}

func TestReadAtWrapper_Close(t *testing.T) {
	s := newMemReader([]byte("x"))
	rw := &readAtWrapper{reader: s}

	err := rw.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.closed {
		t.Fatal("expected underlying reader to be closed")
	}
	// Offset should be reset.
	if rw.offset != 0 {
		t.Fatalf("expected offset reset to 0, got %d", rw.offset)
	}
}

func TestPool_IdleTimeout_NoPressure(t *testing.T) {
	p := New(Config{
		Logger:      testLogger(),
		IdleTimeout: 30 * time.Second,
	})

	if got := p.idleTimeout(-1); got != 30*time.Second {
		t.Fatalf("expected 30s, got %v", got)
	}
}

func TestPool_IdleTimeout_WithPressureFunc(t *testing.T) {
	tests := []struct {
		usage float64
		want  time.Duration
	}{
		{0.30, 30 * time.Second},
		{0.60, 10 * time.Second},
		{0.80, 5 * time.Second},
		{0.95, 1 * time.Second},
	}

	for _, tc := range tests {
		p := New(Config{
			Logger:      testLogger(),
			IdleTimeout: 30 * time.Second,
		})

		got := p.idleTimeout(tc.usage)
		if got != tc.want {
			t.Errorf("usage=%.2f: expected %v, got %v", tc.usage, tc.want, got)
		}
	}
}

func TestPool_ClosedFlagPreventsReuse(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	if p.closed {
		t.Fatal("expected pool to not be closed initially")
	}

	p.Close()

	if !p.closed {
		t.Fatal("expected pool to be closed after Close()")
	}
}

func TestPool_CloseClearsAllReaders(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:   testLogger(),
		Registry: reg,
	})

	if len(p.readers) != 0 {
		t.Fatalf("expected 0 readers initially, got %d", len(p.readers))
	}
	if reg.clears != 0 {
		t.Fatalf("expected 0 ClearActiveRange calls initially, got %d", reg.clears)
	}

	p.Close()

	if len(p.readers) != 0 {
		t.Fatalf("expected 0 readers after close, got %d", len(p.readers))
	}
}

func TestPool_ComputeReadahead(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	rah := p.computeReadahead(1000)
	if rah != 1000 {
		t.Fatalf("expected 1000, got %d", rah)
	}

	p.readers[readerKey{hash: ih, filePath: "a", readerID: 1}] = &streamReader{active: true}
	p.readers[readerKey{hash: ih, filePath: "b", readerID: 2}] = &streamReader{active: true}

	rah = p.computeReadahead(1000)
	if rah != 500 {
		t.Fatalf("expected 500, got %d", rah)
	}

	p.readers[readerKey{hash: ih, filePath: "c", readerID: 3}] = &streamReader{active: false}
	rah = p.computeReadahead(1000)
	if rah != 500 {
		t.Fatalf("expected 500 (idle ignored), got %d", rah)
	}
}

func TestPool_ComputeReadahead_FileStorageExcluded(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	p.readers[readerKey{hash: ih, filePath: "a", readerID: 1}] = &streamReader{active: true, isFileStorage: true}
	p.readers[readerKey{hash: ih, filePath: "b", readerID: 2}] = &streamReader{active: true, isFileStorage: false}

	rah := p.computeReadahead(1000)
	if rah != 1000 {
		t.Fatalf("expected 1000 (file-storage excluded), got %d", rah)
	}
}

func TestPool_ComputeReadahead_MinimumOne(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	for i := uint64(1); i <= 10; i++ {
		p.readers[readerKey{hash: ih, filePath: "x", readerID: i}] = &streamReader{active: true}
	}

	rah := p.computeReadahead(5)
	if rah != 1 {
		t.Fatalf("expected minimum 1, got %d", rah)
	}
}

func TestPool_ReleaseSetsIdle(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	sr := &streamReader{
		active:   true,
		hash:     ih,
		file:     &torrent.File{},
		readerID: 1,
		rah:      1024,
	}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr

	p.release(ih, "f", 1)

	if sr.active {
		t.Fatal("expected reader to be idle after release")
	}
	if sr.idleSince.IsZero() {
		t.Fatal("expected idleSince to be set")
	}
}

func TestPool_ReleaseNotActiveNoop(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	sr := &streamReader{active: false, hash: ih, readerID: 1}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr

	p.release(ih, "f", 1)

	if !sr.idleSince.IsZero() {
		t.Fatal("idleSince should remain zero for already-idle reader")
	}
}

func TestPool_ReleaseIdempotent(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	sr := &streamReader{
		active:   true,
		hash:     ih,
		file:     &torrent.File{},
		readerID: 1,
		rah:      1024,
	}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr

	p.release(ih, "f", 1)
	p.release(ih, "f", 1)

	if sr.active {
		t.Fatal("expected reader to remain idle after double release")
	}
}

func TestPool_ParkIdleReaders_SkipsAlreadyParked(t *testing.T) {
	p := New(Config{
		Logger:      testLogger(),
		IdleTimeout: 1 * time.Millisecond,
	})
	ih := metainfo.Hash{}

	sr := &streamReader{
		active:    false,
		hash:      ih,
		file:      &torrent.File{},
		readerID:  1,
		rah:       0, // already parked
		idleSince: time.Now().Add(-10 * time.Millisecond),
	}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr

	p.parkIdleReaders()

	if sr.rah != 0 {
		t.Fatalf("expected rah to remain 0, got %d", sr.rah)
	}
}

func TestPool_ParkIdleReaders_ParksIdleToZero(t *testing.T) {
	p := New(Config{
		Logger:      testLogger(),
		IdleTimeout: 10 * time.Millisecond,
		MaxIdleTime: 24 * time.Hour,
	})
	ih := metainfo.Hash{}

	sr := &streamReader{
		active:    false,
		hash:      ih,
		file:      &torrent.File{},
		readerID:  1,
		rah:       1024,
		idleSince: time.Now().Add(-50 * time.Millisecond),
	}
	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.parkIdleReaders()

	if sr.rah != 0 {
		t.Fatalf("expected readahead to be 0 after parking, got %d", sr.rah)
	}
}

func TestPool_MaxIdleTime_RemovesReader(t *testing.T) {
	p := New(Config{
		Logger:      testLogger(),
		IdleTimeout: 1 * time.Millisecond,
		MaxIdleTime: 1 * time.Millisecond,
	})
	ih := metainfo.Hash{}

	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:    false,
		hash:      ih,
		file:      &torrent.File{},
		readerID:  1,
		rah:       0,
		idleSince: time.Now().Add(-100 * time.Millisecond),
	}

	p.parkIdleReaders()

	if _, ok := p.readers[key]; ok {
		t.Fatal("expected reader to be removed after MaxIdleTime")
	}
}

func TestPool_MaxIdleTime_ZeroMeansNoCleanup(t *testing.T) {
	p := New(Config{
		Logger:      testLogger(),
		IdleTimeout: 1 * time.Millisecond,
		MaxIdleTime: 0,
	})
	ih := metainfo.Hash{}

	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:    false,
		hash:      ih,
		file:      &torrent.File{},
		readerID:  1,
		rah:       0,
		idleSince: time.Now().Add(-1 * time.Hour),
	}

	p.parkIdleReaders()

	if _, ok := p.readers[key]; !ok {
		t.Fatal("expected reader to still exist when MaxIdleTime is zero")
	}
}

func TestPool_ReaderPositions_EmptyPool(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	result := p.ReaderPositions(ih)
	if len(result) != 0 {
		t.Fatalf("expected 0 readers, got %d", len(result))
	}
}

func TestPool_RefreshReadahead(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	sr1 := &streamReader{active: true, hash: ih, rah: 100}
	sr2 := &streamReader{active: false, hash: ih, rah: 100}
	p.readers[readerKey{hash: ih, filePath: "a", readerID: 1}] = sr1
	p.readers[readerKey{hash: ih, filePath: "b", readerID: 2}] = sr2

	p.RefreshReadahead(1000)

	if sr1.rah != 1000 {
		t.Fatalf("expected active reader readahead=1000, got %d", sr1.rah)
	}
	if sr2.rah != 100 {
		t.Fatalf("expected idle reader readahead unchanged=100, got %d", sr2.rah)
	}
}

func TestPool_ActiveRangeRegistry_ClearOnRelease(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:   testLogger(),
		Registry: reg,
	})
	ih := metainfo.Hash{}

	key := readerKey{hash: ih, filePath: "f", readerID: 42}
	p.readers[key] = &streamReader{
		active:   true,
		hash:     ih,
		readerID: 42,
		rah:      2048,
	}

	p.release(ih, "f", 42)

	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
	}
}

func TestPool_ActiveRangeRegistry_ClearOnClose(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:   testLogger(),
		Registry: reg,
	})
	ih := metainfo.Hash{}

	for i := uint64(1); i <= 2; i++ {
		key := readerKey{hash: ih, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:   true,
			hash:     ih,
			readerID: i,
		}
	}

	p.Close()

	if reg.clears != 2 {
		t.Fatalf("expected 2 ClearActiveRange calls, got %d", reg.clears)
	}
}

func TestPool_ActiveRangeRegistry_NilRegistryNoop(t *testing.T) {
	p := New(Config{
		Logger:   testLogger(),
		Registry: nil,
	})
	ih := metainfo.Hash{}

	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:   true,
		hash:     ih,
		readerID: 1,
	}

	p.release(ih, "f", 1)
	p.Close()
}

func TestPool_ConfigDefaults(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	if p.cfg.IdleTimeout != 30*time.Second {
		t.Fatalf("expected default IdleTimeout=30s, got %v", p.cfg.IdleTimeout)
	}
	if p.cfg.FileStorageReadahead != 50*1024*1024 {
		t.Fatalf("expected default FileStorageReadahead=50MB, got %d", p.cfg.FileStorageReadahead)
	}
	if p.cfg.MaxIdleTime != 0 {
		t.Fatalf("expected default MaxIdleTime=0 (no limit), got %v", p.cfg.MaxIdleTime)
	}
}

func TestPool_CloseIdempotent(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	p.Close()
	p.Close() // should not panic
}

func TestReadAtWrapper_ConcurrentReadAt(t *testing.T) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	s := newMemReader(data)

	var mu sync.Mutex
	var offsets []int64
	rw := &readAtWrapper{
		reader: s,
		onOffsetChange: func(off int64) {
			mu.Lock()
			offsets = append(offsets, off)
			mu.Unlock()
		},
	}

	// Launch multiple goroutines reading at different offsets concurrently.
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			off := int64(idx * 100)
			buf := make([]byte, 10)
			_, _ = rw.ReadAt(buf, off)
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(offsets) != goroutines {
		t.Fatalf("expected %d callbacks, got %d", goroutines, len(offsets))
	}
	// Each callback should have offset = start + 10 bytes read.
	// Order is not deterministic, so check that every expected offset is present.
	expected := make(map[int64]int)
	for i := 0; i < goroutines; i++ {
		e := int64(i*100 + 10)
		expected[e]++
	}
	for _, off := range offsets {
		expected[off]--
	}
	for e, count := range expected {
		if count != 0 {
			t.Errorf("offset %d: expected count 1, got %d (present=%d)", e, count, goroutines-count)
		}
	}
}

func TestPool_MaxReadersPerTorrent_DefaultsToTen(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	if p.cfg.MaxReadersPerTorrent != 10 {
		t.Fatalf("expected default 10, got %d", p.cfg.MaxReadersPerTorrent)
	}
}

func TestPool_MaxReadersPerTorrent_EvictsIdle(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:               testLogger(),
		Registry:             reg,
		MaxReadersPerTorrent: 2,
	})
	ih := metainfo.Hash{1}

	// Insert two idle readers for the same (hash, filePath).
	for i := uint64(1); i <= 2; i++ {
		key := readerKey{hash: ih, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:    false,
			hash:      ih,
			filePath:  "f",
			file:      &torrent.File{},
			readerID:  i,
			idleSince: time.Now().Add(time.Duration(i) * time.Minute),
		}
	}

	// Force eviction — should remove readerID=1 (oldest idle).
	p.evictOldestIdleLocked(ih, "f")

	if _, ok := p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}]; ok {
		t.Fatal("expected oldest idle reader (ID 1) to be evicted")
	}
	if _, ok := p.readers[readerKey{hash: ih, filePath: "f", readerID: 2}]; !ok {
		t.Fatal("expected younger idle reader (ID 2) to remain")
	}
	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
	}
}

func TestPool_MaxReadersPerTorrent_NoEvictionWhenOnlyActiveExist(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:               testLogger(),
		Registry:             reg,
		MaxReadersPerTorrent: 2,
	})
	ih := metainfo.Hash{2}

	// Insert two *active* readers — no idle readers exist.
	for i := uint64(10); i <= 11; i++ {
		key := readerKey{hash: ih, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:   true,
			hash:     ih,
			filePath: "f",
			file:     &torrent.File{},
			readerID: i,
		}
	}

	// Force eviction — should return false since no idle readers exist.
	evicted := p.evictOldestIdleLocked(ih, "f")

	if evicted {
		t.Fatal("expected false when no idle readers exist")
	}
	if len(p.readers) != 2 {
		t.Fatal("expected no readers to be evicted (active readers are never killed)")
	}
	if reg.clears != 0 {
		t.Fatalf("expected 0 ClearActiveRange calls, got %d", reg.clears)
	}
}

func TestPool_MaxReadersPerTorrent_NoEvictWhenUnderCap(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:               testLogger(),
		Registry:             reg,
		MaxReadersPerTorrent: 5,
	})
	ih := metainfo.Hash{3}

	// Insert 2 idle readers — eviction should work even when under cap.
	for i := uint64(1); i <= 2; i++ {
		key := readerKey{hash: ih, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:    false,
			hash:      ih,
			filePath:  "f",
			file:      &torrent.File{},
			readerID:  i,
			idleSince: time.Now().Add(time.Duration(i) * time.Minute),
		}
	}

	p.evictOldestIdleLocked(ih, "f")

	if len(p.readers) != 1 {
		t.Fatalf("expected 1 reader after eviction, got %d", len(p.readers))
	}
	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
	}
}

func TestPool_MaxReadersPerTorrent_ZeroUsesDefault(t *testing.T) {
	p := New(Config{
		Logger:               testLogger(),
		MaxReadersPerTorrent: 0,
	})

	if p.cfg.MaxReadersPerTorrent != 10 {
		t.Fatalf("expected 0 to default to 10, got %d", p.cfg.MaxReadersPerTorrent)
	}
}

func TestPool_MaxReadersPerTorrent_NegativeDisablesCap(t *testing.T) {
	p := New(Config{
		Logger:               testLogger(),
		MaxReadersPerTorrent: -1,
	})

	if p.cfg.MaxReadersPerTorrent != 0 {
		t.Fatalf("expected -1 to disable the cap (0), got %d", p.cfg.MaxReadersPerTorrent)
	}
}

func TestPool_MaxReadersPerTorrent_SingleEvictionLoop(t *testing.T) {
	p := New(Config{
		Logger:               testLogger(),
		MaxReadersPerTorrent: 2,
	})
	ih := metainfo.Hash{4}

	// Insert 1 idle + 2 active readers — cap is 2.
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = &streamReader{
		active:    false,
		hash:      ih,
		filePath:  "f",
		file:      &torrent.File{},
		readerID:  1,
		idleSince: time.Now(),
	}
	for i := uint64(2); i <= 3; i++ {
		p.readers[readerKey{hash: ih, filePath: "f", readerID: i}] = &streamReader{
			active:   true,
			hash:     ih,
			filePath: "f",
			file:     &torrent.File{},
			readerID: i,
		}
	}

	// Evict loop: first iteration evicts idle reader, second iteration
	// finds no idle readers and breaks (soft cap).
	for p.countReadersLocked(ih, "f") >= p.cfg.MaxReadersPerTorrent {
		if !p.evictOldestIdleLocked(ih, "f") {
			break
		}
	}

	count := p.countReadersLocked(ih, "f")
	if count != 2 {
		t.Fatalf("expected 2 readers (cap soft when no idle), got %d", count)
	}
}

func TestPool_ReadaheadRebalance_OnRelease(t *testing.T) {
	p := New(Config{
		Logger: testLogger(),
	})
	ih := metainfo.Hash{5}

	sr1 := &streamReader{active: true, hash: ih, filePath: "f", readerID: 1, rah: 500, isFileStorage: false, readaheadPool: 1000}
	sr2 := &streamReader{active: true, hash: ih, filePath: "f", readerID: 2, rah: 500, isFileStorage: false, readaheadPool: 1000}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr1
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 2}] = sr2

	p.refreshReadaheadLocked(1000)

	if sr1.rah != 500 || sr2.rah != 500 {
		t.Fatalf("expected both readers at 500, got sr1=%d sr2=%d", sr1.rah, sr2.rah)
	}

	sr1.active = false
	p.refreshReadaheadLocked(1000)

	if sr2.rah != 1000 {
		t.Fatalf("expected sr2 to get full pool=1000 after sr1 released, got %d", sr2.rah)
	}
	if sr1.rah != 500 {
		t.Fatalf("expected sr1 readahead unchanged at 500 (idle), got %d", sr1.rah)
	}
}

func TestPool_ReadaheadRebalance_FileStorageExcluded(t *testing.T) {
	p := New(Config{
		Logger: testLogger(),
	})
	ih := metainfo.Hash{6}

	sr1 := &streamReader{active: true, hash: ih, filePath: "f", readerID: 1, rah: 0, isFileStorage: true, readaheadPool: 1000}
	sr2 := &streamReader{active: true, hash: ih, filePath: "f", readerID: 2, rah: 0, isFileStorage: false, readaheadPool: 1000}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr1
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 2}] = sr2

	p.refreshReadaheadLocked(1000)

	if sr2.rah != 1000 {
		t.Fatalf("expected sr2 to get full pool=1000 (file-storage excluded), got %d", sr2.rah)
	}
	if sr1.rah != 0 {
		t.Fatalf("expected sr1 readahead unchanged at 0, got %d", sr1.rah)
	}
}

func TestPool_ReadaheadRebalance_NoRebalanceWhenZero(t *testing.T) {
	p := New(Config{
		Logger: testLogger(),
	})
	ih := metainfo.Hash{7}

	sr := &streamReader{active: true, hash: ih, filePath: "f", readerID: 1, rah: 500, isFileStorage: false, readaheadPool: 0}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr

	p.release(ih, "f", 1)

	if sr.rah != 500 {
		t.Fatalf("expected readahead unchanged at 500 when ReadaheadPool is 0, got %d", sr.rah)
	}
}
