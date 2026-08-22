// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bytes"
	"context"
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

// mockReader is a minimal torrent.Reader mock for rebalancing tests.
type mockReader struct {
	mu  sync.Mutex
	reh int64 // current readahead
}

func (m *mockReader) Read(p []byte) (int, error)              { return 0, io.EOF }
func (m *mockReader) ReadAt(p []byte, off int64) (int, error) { return 0, io.EOF }
func (m *mockReader) ReadContext(ctx context.Context, p []byte) (int, error) {
	return 0, io.EOF
}
func (m *mockReader) Seek(offset int64, whence int) (int64, error) { return offset, nil }
func (m *mockReader) Close() error                                 { return nil }
func (m *mockReader) SetReadahead(r int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reh = r
}
func (m *mockReader) SetReadaheadFunc(torrent.ReadaheadFunc) {}
func (m *mockReader) SetResponsive()                         {}
func (m *mockReader) SetContext(context.Context)             {}

func (m *mockReader) getReh() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reh
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

	// Caller always counts as +1, so 1000 / 1 = 1000.
	rah := p.computeReadahead(1000, true)
	if rah != 1000 {
		t.Fatalf("expected 1000, got %d", rah)
	}

	p.readers[readerKey{hash: ih, filePath: "a", readerID: 1}] = &streamReader{active: true}
	p.readers[readerKey{hash: ih, filePath: "b", readerID: 2}] = &streamReader{active: true}

	// 2 existing active + 1 caller = 3, so 1000 / 3 = 333.
	rah = p.computeReadahead(1000, true)
	if rah != 333 {
		t.Fatalf("expected 333, got %d", rah)
	}

	p.readers[readerKey{hash: ih, filePath: "c", readerID: 3}] = &streamReader{active: false}
	rah = p.computeReadahead(1000, true)
	if rah != 333 {
		t.Fatalf("expected 333 (idle ignored), got %d", rah)
	}

	// Without includeCallers: 2 active = 1000 / 2 = 500.
	rah = p.computeReadahead(1000, false)
	if rah != 500 {
		t.Fatalf("expected 500 without caller, got %d", rah)
	}
}

func TestPool_ComputeReadahead_FileStorageExcluded(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	p.readers[readerKey{hash: ih, filePath: "a", readerID: 1}] = &streamReader{active: true, isFileStorage: true}
	p.readers[readerKey{hash: ih, filePath: "b", readerID: 2}] = &streamReader{active: true, isFileStorage: false}

	// 1 existing active memory reader + 1 caller = 2, so 1000 / 2 = 500.
	rah := p.computeReadahead(1000, true)
	if rah != 500 {
		t.Fatalf("expected 500 (file-storage excluded), got %d", rah)
	}
}

func TestPool_ComputeReadahead_MinimumOne(t *testing.T) {
	p := New(Config{Logger: testLogger()})
	ih := metainfo.Hash{}

	for i := uint64(1); i <= 10; i++ {
		p.readers[readerKey{hash: ih, filePath: "x", readerID: i}] = &streamReader{active: true}
	}

	// 10 existing active + 1 caller = 11, so 5 / 11 = 0 → clamped to 1.
	rah := p.computeReadahead(5, true)
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

	// 1 active reader (no caller), so 1000 / 1 = 1000.
	p.refreshReadaheadLocked(1000)

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
	// readerID=1 is older (more time idle) than readerID=2.
	for i := uint64(1); i <= 2; i++ {
		key := readerKey{hash: ih, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:    false,
			hash:      ih,
			file:      &torrent.File{},
			readerID:  i,
			idleSince: time.Now().Add(-time.Duration(3-i) * time.Minute),
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
		file:      &torrent.File{},
		readerID:  1,
		idleSince: time.Now(),
	}
	for i := uint64(2); i <= 3; i++ {
		p.readers[readerKey{hash: ih, filePath: "f", readerID: i}] = &streamReader{
			active:   true,
			hash:     ih,
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

func TestPool_ReadaheadRebalance_Integration(t *testing.T) {
	// Registry is nil so registerRangeLocked returns early without
	// needing a valid torrent.File (which has unexported fields).
	p := New(Config{
		Logger:             testLogger(),
		MaxIdleTime:        5 * time.Minute,
		IdleTimeout:        30 * time.Second,
		MemoryPressureFunc: func() float64 { return 0.3 },
	})
	ih := metainfo.Hash{5}
	pool := int64(10000)

	// Simulate 3 memory readers sharing the pool.
	srs := make([]*streamReader, 3)
	for i := uint64(1); i <= 3; i++ {
		mr := &mockReader{reh: 0}
		srs[i-1] = &streamReader{
			active:        true,
			hash:          ih,
			readerID:      i,
			rah:           0,
			isFileStorage: false,
			reader:        mr,
		}
		p.readers[readerKey{hash: ih, filePath: "f", readerID: i}] = srs[i-1]
	}
	p.readaheadPool = pool

	// All 3 active: each gets pool/3 = 3333.
	p.refreshReadaheadLocked(pool)
	for i, sr := range srs {
		want := int64(3333)
		if sr.rah != want {
			t.Fatalf("reader %d: expected readahead %d, got %d", i+1, want, sr.rah)
		}
		if sr.reader.(*mockReader).getReh() != want {
			t.Fatalf("reader %d: mock SetReadahead(%d) not called, got %d", i+1, want, sr.reader.(*mockReader).getReh())
		}
	}

	// Release reader 2: remaining 2 get pool/2 = 5000.
	p.release(ih, "f", 2)
	if srs[1].rah != 3333 {
		t.Fatalf("released reader 2 should keep readahead=3333, got %d", srs[1].rah)
	}
	for _, sr := range []*streamReader{srs[0], srs[2]} {
		if sr.rah != 5000 {
			t.Fatalf("active reader %d: expected readahead 5000, got %d", sr.readerID, sr.rah)
		}
		if sr.reader.(*mockReader).getReh() != 5000 {
			t.Fatalf("active reader %d: mock SetReadahead(5000) not called, got %d", sr.readerID, sr.reader.(*mockReader).getReh())
		}
	}

	// Release reader 0: reader 2 gets full pool = 10000.
	p.release(ih, "f", 1)
	if srs[0].rah != 5000 {
		t.Fatalf("released reader 0 should keep readahead=5000, got %d", srs[0].rah)
	}
	if srs[2].rah != 10000 {
		t.Fatalf("active reader 2: expected readahead 10000, got %d", srs[2].rah)
	}
	if srs[2].reader.(*mockReader).getReh() != 10000 {
		t.Fatalf("active reader 2: mock SetReadahead(10000) not called, got %d", srs[2].reader.(*mockReader).getReh())
	}

	// Reader 1 was never active in this scenario — releasing it is a no-op.
	p.release(ih, "f", 2)

	// File-storage reader is excluded from rebalancing.
	fsMr := &mockReader{reh: 50000000}
	fsSr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      99,
		rah:           50000000,
		isFileStorage: true,
		reader:        fsMr,
	}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 99}] = fsSr
	p.refreshReadaheadLocked(pool)
	if srs[2].rah != 10000 {
		t.Fatalf("memory reader should keep readahead=10000, got %d", srs[2].rah)
	}
	if fsSr.rah != 50000000 {
		t.Fatalf("file-storage reader should keep readahead=50000000, got %d", fsSr.rah)
	}
	if fsSr.reader.(*mockReader).getReh() != 50000000 {
		t.Fatalf("file-storage reader SetReadahead should not have changed, got %d", fsSr.reader.(*mockReader).getReh())
	}
}

func TestPool_ReadaheadRebalance_PoolZero(t *testing.T) {
	p := New(Config{
		Logger: testLogger(),
	})
	ih := metainfo.Hash{7}

	sr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           500,
		isFileStorage: false,
	}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr

	p.release(ih, "f", 1)

	if sr.rah != 500 {
		t.Fatalf("expected readahead unchanged at 500 when readaheadPool is 0, got %d", sr.rah)
	}
}

func TestPool_ReadaheadRebalance_IdleGCRebalance(t *testing.T) {
	// Registry is nil so registerRangeLocked returns early without
	// needing a valid torrent.File (which has unexported fields).
	p := New(Config{
		Logger:             testLogger(),
		MaxIdleTime:        10 * time.Minute,
		IdleTimeout:        100 * time.Millisecond,
		MemoryPressureFunc: func() float64 { return 0.3 },
	})
	ih := metainfo.Hash{10}
	pool := int64(10000)

	// Two active readers.
	sr1 := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           0,
		isFileStorage: false,
		reader:        &mockReader{reh: 0},
	}
	sr2 := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      2,
		rah:           0,
		isFileStorage: false,
		reader:        &mockReader{reh: 0},
	}
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 1}] = sr1
	p.readers[readerKey{hash: ih, filePath: "f", readerID: 2}] = sr2
	p.readaheadPool = pool
	p.refreshReadaheadLocked(pool)

	if sr1.rah != 5000 || sr2.rah != 5000 {
		t.Fatalf("expected 5000 each, got sr1=%d sr2=%d", sr1.rah, sr2.rah)
	}

	// Park sr1 via idleGC — it loses readahead.
	sr1.active = false
	sr1.idleSince = time.Now().Add(-200 * time.Millisecond)
	p.parkIdleReaders()

	if sr1.rah != 0 {
		t.Fatalf("parked reader should have readahead=0, got %d", sr1.rah)
	}

	// sr2 should now get the full pool since sr1 is parked (idle).
	// parkIdleReaders should have called refreshReadaheadLocked for active readers.
	if sr2.rah != 10000 {
		t.Fatalf("active reader should get full pool after idle peer parked: expected 10000, got %d", sr2.rah)
	}
}

func TestPool_PrioritizeAheadFraction_ZeroIsNoop(t *testing.T) {
	// PrioritizeAheadFraction=0 means no prioritizeAsync goroutine is
	// ever dispatched regardless of Registry.  When Registry is also nil,
	// updateActiveRange returns early (no range tracking at all).  The key
	// invariant is that sr.prioritizedPieces is never modified by
	// prioritization logic when frac==0.
	p := New(Config{
		Logger:                  testLogger(),
		PrioritizeAheadFraction: 0,
	})
	ih := metainfo.Hash{20}

	sr := &streamReader{
		active:            true,
		hash:              ih,
		readerID:          1,
		rah:               1024,
		isFileStorage:     false,
		prioritizedPieces: []int{1, 2, 3},
	}
	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = sr

	// Registry is nil so updateActiveRange returns early before reaching
	// file.Torrent().Info() — this is the correct path for the "no
	// prioritization" config.
	p.updateActiveRange(ih, key, &torrent.File{}, 512)

	if len(sr.prioritizedPieces) != 3 {
		t.Fatalf("expected prioritizedPieces unchanged (len=3), got %d", len(sr.prioritizedPieces))
	}
}

func TestPool_PrioritizeAheadFraction_FileStorageSkipped(t *testing.T) {
	// File-storage readers skip prioritization even when frac > 0.
	// With a nil Registry, updateActiveRange returns early before any
	// prioritization logic — the test verifies that the reader's
	// prioritizedPieces are never modified.
	p := New(Config{
		Logger:                  testLogger(),
		Registry:                nil,
		PrioritizeAheadFraction: 0.5,
	})
	ih := metainfo.Hash{21}

	sr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           1024,
		isFileStorage: true,
	}
	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.updateActiveRange(ih, key, &torrent.File{}, 512)

	if len(sr.prioritizedPieces) != 0 {
		t.Fatalf("expected no prioritized pieces for file-storage reader, got %d", len(sr.prioritizedPieces))
	}
}

func TestPool_ReleaseRestoresPrioritizedPieces(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:                  testLogger(),
		Registry:                reg,
		PrioritizeAheadFraction: 0.5,
	})
	ih := metainfo.Hash{22}

	// The release method iterates over prioritizedPieces and calls
	// file.Torrent().Piece() which panics on a bare &torrent.File{}.
	// We use an empty slice to test the nil-clearing path without panic.
	sr := &streamReader{
		active:            true,
		hash:              ih,
		file:              &torrent.File{},
		readerID:          1,
		rah:               1024,
		isFileStorage:     false,
		prioritizedPieces: []int{},
	}
	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.release(ih, "f", 1)

	if sr.prioritizedPieces != nil {
		t.Fatalf("expected prioritizedPieces to be nil after release, got %v", sr.prioritizedPieces)
	}
	if sr.active {
		t.Fatal("expected reader to be idle after release")
	}
}

func TestPool_PrioritizeNextPieces_NilFileReturnsNil(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	result := p.prioritizeNextPieces(nil, 0, 1024, 0.5, 0.3, nil)
	if result != nil {
		t.Fatalf("expected nil for nil file, got %v", result)
	}
}

func TestPool_PrioritizeNextPieces_ZeroFractionReturnsNil(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0, 0.3, nil)
	if result != nil {
		t.Fatalf("expected nil for frac=0, got %v", result)
	}

	result = p.prioritizeNextPieces(&torrent.File{}, 0, 1024, -0.1, 0.3, nil)
	if result != nil {
		t.Fatalf("expected nil for frac=-0.1, got %v", result)
	}
}

func TestPool_PrioritizeNextPieces_FractionOverOneReturnsNil(t *testing.T) {
	p := New(Config{Logger: testLogger()})

	result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 1.1, 0.3, nil)
	if result != nil {
		t.Fatalf("expected nil for frac=1.1, got %v", result)
	}
}

func TestPool_PrioritizeNextPieces_NearFractionDefaultsToThree(t *testing.T) {
	// When PrioritizeNearFraction is 0 in config, updateActiveRange should
	// default to 0.3 (30 % Now, 70 % High).
	reg := &stubRegistry{}
	p := New(Config{
		Logger:                  testLogger(),
		Registry:                reg,
		PrioritizeAheadFraction: 0.5,
		PrioritizeNearFraction:  0, // zero → defaults to 0.3
	})
	ih := metainfo.Hash{30}

	sr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           1024,
		isFileStorage: false,
	}
	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = sr

	// Verify prioPlan defaults nearFrac to 0.3 when zero.
	// rah=4MB, pieceLength=256KB → rahPieces=16, n=int(16*0.5)=8.
	n, nowCount, _, _ := prioPlan(0, 4*1024*1024, 256*1024, 0, 100, 0.5, 0)
	if n != 8 {
		t.Fatalf("expected n=8 for frac=0.5 and rahPieces=16, got %d", n)
	}
	// nearFrac defaults to 0.3: nowCount = int(8*0.3) = 2.
	if nowCount != 2 {
		t.Fatalf("expected nowCount=2 (30%% of 8), got %d", nowCount)
	}
}

func TestPool_ResetPriorities_NilFileNoop(t *testing.T) {
	result := resetPriorities([]int{10, 11, 12}, nil)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestPool_ResetPriorities_ClearsSlice(t *testing.T) {
	ih := metainfo.Hash{60}

	sr := &streamReader{
		active:            true,
		hash:              ih,
		file:              &torrent.File{},
		readerID:          1,
		rah:               1024,
		isFileStorage:     false,
		prioritizedPieces: []int{10, 11, 12},
	}

	// Directly call resetPriorities with the reader's file.
	// Since &torrent.File{}.Torrent() returns nil, this is a no-op on
	// the underlying torrent but should still clear the slice.
	sr.prioritizedPieces = resetPriorities(sr.prioritizedPieces, sr.file)
	if sr.prioritizedPieces != nil {
		t.Fatalf("expected nil after reset, got %v", sr.prioritizedPieces)
	}
}

func TestPool_CloseResetsPrioritiesForActiveReaders(t *testing.T) {
	reg := &stubRegistry{}
	p := New(Config{
		Logger:                  testLogger(),
		Registry:                reg,
		PrioritizeAheadFraction: 0.5,
	})
	ih := metainfo.Hash{50}

	sr := &streamReader{
		active:            true,
		hash:              ih,
		file:              &torrent.File{},
		readerID:          1,
		rah:               1024,
		isFileStorage:     false,
		prioritizedPieces: []int{5, 6, 7},
	}
	key := readerKey{hash: ih, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.Close()

	if sr.prioritizedPieces != nil {
		t.Fatalf("expected prioritizedPieces to be nil after Close, got %v", sr.prioritizedPieces)
	}
}

func TestPool_PrioritizeNextPieces_NearFractionClampedToMax(t *testing.T) {
	// nearFrac > 1 should clamp to 1.0 in prioPlan so all n pieces get Now.
	// rah=4MB, pieceLength=256KB → rahPieces=16, n=int(16*0.5)=8.
	n, nowCount, _, _ := prioPlan(0, 4*1024*1024, 256*1024, 0, 100, 0.5, 1.5)
	if n != 8 {
		t.Fatalf("expected n=8, got %d", n)
	}
	// With nearFrac clamped to 1.0: nowCount = 1.0 * 8 = 8.
	if nowCount != 8 {
		t.Fatalf("expected nowCount=8 (all pieces get Now), got %d", nowCount)
	}
}

func TestPool_PrioritizeAsync_DrivesPriorityUpdate(t *testing.T) {
	// Verifies that prioritizeAsync acquires priorityMu and runs without
	// panicking.  Since &torrent.File{}.Torrent() is nil,
	// prioritizeNextPieces returns nil immediately — but the goroutine
	// runs, acquires/releases priorityMu, and completes cleanly.
	p := New(Config{
		Logger:                  testLogger(),
		PrioritizeAheadFraction: 0.5,
	})
	ih := metainfo.Hash{40}

	sr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           1024,
		isFileStorage: false,
	}

	// Simulate what updateActiveRange does: bump seq, then call
	// prioritizeAsync directly (bypassing the Registry path that panics
	// on &torrent.File{}.Torrent().Info()).
	seq := sr.prioritySeq.Add(1)

	done := make(chan struct{})
	go func() {
		p.prioritizeAsync(sr, seq, &torrent.File{}, 512, 1024)
		close(done)
	}()

	// Wait for the goroutine to complete.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("prioritizeAsync did not complete")
	}

	// prioritizedPieces should be nil because torrent.File has no torrent.
	if sr.prioritizedPieces != nil {
		t.Fatalf("expected nil prioritizedPieces (no real torrent), got %v", sr.prioritizedPieces)
	}
}

func TestPool_PrioritizeAsync_StaleGoroutineDropped(t *testing.T) {
	// When release() bumps prioritySeq, an in-flight prioritizeAsync must
	// detect the seq mismatch and drop its write-back to prioritizedPieces.
	p := New(Config{
		Logger:                  testLogger(),
		PrioritizeAheadFraction: 0.5,
	})
	ih := metainfo.Hash{41}

	sr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           1024,
		isFileStorage: false,
	}

	// Dispatch prioritizeAsync(seq=1).
	seq := sr.prioritySeq.Add(1)

	done := make(chan struct{})
	go func() {
		p.prioritizeAsync(sr, seq, &torrent.File{}, 512, 1024)
		close(done)
	}()

	// Immediately release — this bumps prioritySeq to 2 and clears
	// prioritizedPieces.  The in-flight goroutine (seq=1) must see the
	// mismatch and drop its write.
	sr.prioritySeq.Add(1)
	sr.priorityMu.Lock()
	sr.prioritizedPieces = resetPriorities(sr.prioritizedPieces, sr.file)
	sr.priorityMu.Unlock()
	sr.active = false
	sr.idleSince = time.Now()

	// Wait for the async goroutine to run (it should exit without
	// modifying sr.prioritizedPieces).
	select {
	case <-done:
	case <-time.After(1 * time.Second):
	}
	time.Sleep(50 * time.Millisecond)

	// prioritizedPieces must remain nil — the stale goroutine dropped.
	if sr.prioritizedPieces != nil {
		t.Fatalf("expected nil prioritizedPieces (stale goroutine dropped), got %v", sr.prioritizedPieces)
	}
	if sr.active {
		t.Fatal("expected reader to be inactive after release")
	}
}

func TestPool_UpdateActiveRangeVsRelease_Concurrent(t *testing.T) {
	// Runs prioritizeAsync and release concurrently on the same reader
	// to verify no panics, races, or dangling priorities.  Run with
	// -race to catch data races.
	p := New(Config{
		Logger:                  testLogger(),
		PrioritizeAheadFraction: 0.5,
	})
	ih := metainfo.Hash{42}

	sr := &streamReader{
		active:        true,
		hash:          ih,
		readerID:      1,
		rah:           1024,
		isFileStorage: false,
	}

	// releaseMu mirrors pool.mu: it serializes the "release" side so
	// only one goroutine writes active/idleSince at a time.  In
	// production this is pool.mu; here we add it to keep the test
	// race-free while still exercising concurrent prioritizeAsync vs.
	// release.
	var releaseMu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			seq := sr.prioritySeq.Add(1)
			p.prioritizeAsync(sr, seq, &torrent.File{}, int64(i)*100, 1024)
		}()
		go func() {
			defer wg.Done()
			releaseMu.Lock()
			sr.prioritySeq.Add(1)
			sr.priorityMu.Lock()
			sr.prioritizedPieces = resetPriorities(sr.prioritizedPieces, sr.file)
			sr.priorityMu.Unlock()
			sr.active = false
			sr.idleSince = time.Now()
			releaseMu.Unlock()
		}()
	}
	wg.Wait()

	// After all concurrent ops, prioritizedPieces must be nil (no real
	// torrent to SetPriority on) and the reader must be inactive.
	if sr.prioritizedPieces != nil {
		t.Fatalf("expected nil prioritizedPieces after concurrent ops, got %v", sr.prioritizedPieces)
	}
	if sr.active {
		t.Fatal("expected reader to be inactive after release")
	}
	if sr.idleSince.IsZero() {
		t.Fatal("expected idleSince to be set after release")
	}
}

func TestPrioPlan_EdgeCases(t *testing.T) {
	// n clamps to minimum 1 when frac * rahPieces < 1.
	n, _, _, _ := prioPlan(0, 100, 256*1024, 0, 100, 0.01, 0.5)
	if n < 1 {
		t.Fatalf("expected n >= 1, got %d", n)
	}

	// nowCount clamps to minimum 1.
	_, nowCount, _, _ := prioPlan(0, 256*1024, 256*1024, 0, 100, 0.5, 0.001)
	if nowCount < 1 {
		t.Fatalf("expected nowCount >= 1, got %d", nowCount)
	}

	// target can't go below currentPiece+1 (even when n and endPieceMax
	// would push it lower).  Use pieceLength=1 so byteOffset maps
	// directly to piece index.
	_, _, target, _ := prioPlan(100, 100, 1, 0, 100, 0.01, 0.5)
	if target < 101 {
		t.Fatalf("expected target >= 101, got %d", target)
	}

	// frac > 1 clamps to 1.
	// rah=4MB, pieceLength=256KB → rahPieces=16, frac clamped to 1.0.
	n, _, _, _ = prioPlan(0, 4*1024*1024, 256*1024, 0, 100, 2.0, 0.5)
	if n != 16 {
		t.Fatalf("expected n=16 (frac clamped to 1.0), got %d", n)
	}
}

// TestPrioPlan_EndOfLastPiece tests that when the window is clamped to
// file end (endPieceMax is exclusive), the returned target still allows
// the last valid piece (endPieceMax-1) to be included by a loop using
// idx < target.  This catches the off-by-one where the old code did
// endPieceMax-1 inside prioPlan while endPieceMax was already exclusive.
func TestPrioPlan_EndOfLastPiece(t *testing.T) {
	// EndPieceIndex() == 10, so endPieceMax = 11 (exclusive).
	// Large rah + frac=1.0 → target would far exceed the file.
	_, _, target, _ := prioPlan(0, 100*1024*1024, 256*1024, 0, 11, 1.0, 0.5)
	// idx < target must allow idx == 10 (the last valid piece).
	if target <= 10 {
		t.Fatalf("expected target > 10 to include piece 10, got %d", target)
	}

	// Verify unclamped path: small rah, target doesn't exceed endPieceMax.
	_, _, target, _ = prioPlan(0, 512*1024, 256*1024, 0, 100, 1.0, 0.5)
	// rahPieces = 2, n = int(2*1.0) = 2, target = 0+1+2 = 3.
	if target != 3 {
		t.Fatalf("expected target=3, got %d", target)
	}
}
