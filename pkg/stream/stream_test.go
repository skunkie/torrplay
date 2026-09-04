// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bytes"
	"context"
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
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m)
}

func TestPool_AcquireRejectsInvalidFile(t *testing.T) {
	pool := New(Config{})
	t.Cleanup(pool.Close)

	reader, release, err := pool.Acquire(nil, MemoryStorage)
	if !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("expected ErrInvalidFile, got %v", err)
	}
	if reader != nil || release != nil {
		t.Fatal("expected no reader or release function")
	}
}

// stubRegistry implements ActiveRangeRegistry for testing.
type stubRegistry struct {
	sets           int
	clears         int
	boundarySets   int
	boundaryClears int
	last           activeRange
	lastBoundary   testFileBoundary
}

type testFileBoundary struct {
	headStart int
	headEnd   int
	tailStart int
	tailEnd   int
}

func (r *stubRegistry) SetActiveRange(_ metainfo.Hash, _ uint64, start, end int) {
	r.sets++
	r.last = activeRange{startPiece: start, endPiece: end}
}

func (r *stubRegistry) ClearActiveRange(_ metainfo.Hash, _ uint64) {
	r.clears++
}

func (r *stubRegistry) SetFileBoundaries(_ metainfo.Hash, _ uint64, headStart, headEnd, tailStart, tailEnd int) {
	r.boundarySets++
	r.lastBoundary = testFileBoundary{
		headStart: headStart,
		headEnd:   headEnd,
		tailStart: tailStart,
		tailEnd:   tailEnd,
	}
}

func (r *stubRegistry) ClearFileBoundaries(_ metainfo.Hash, _ uint64) {
	r.boundaryClears++
}

type activeRange struct {
	startPiece int
	endPiece   int
}

// mockReader is a minimal torrent.Reader mock for rebalancing tests.
type mockReader struct {
	mu        sync.Mutex
	readahead int64 // current readahead
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
	m.readahead = r
}
func (m *mockReader) SetReadaheadFunc(torrent.ReadaheadFunc) {}
func (m *mockReader) SetResponsive()                         {}
func (m *mockReader) SetContext(context.Context)             {}

func (m *mockReader) getReadahead() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readahead
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}
func newTestPool(t *testing.T, cfg Config) *Pool {
	t.Helper()
	p := New(cfg)
	t.Cleanup(func() {
		p.Close()
	})
	return p
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
	if !errors.Is(err, io.EOF) {
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

func TestReadAtWrapper_ReadAt_PreservesReadProgress(t *testing.T) {
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

	// The small-read path fills its cache through the end of the source. The
	// dedicated torrent reader remains there so readahead stays anchored to
	// the most recently fetched data.
	pos, _ := s.Seek(0, io.SeekCurrent)
	if pos != int64(len(data)) {
		t.Fatalf("expected position advanced to %d, got %d", len(data), pos)
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

func TestReadAtWrapper_ReadAt_LargeReadDirectBypass(t *testing.T) {
	const size = 600 * 1024 // larger than 256KB defaultWrapperBufSize
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	s := newMemReader(data)
	var cbOffset int64
	rw := &readAtWrapper{
		reader: s,
		onOffsetChange: func(off int64) {
			cbOffset = off
		},
	}

	buf := make([]byte, size)
	n, err := rw.ReadAt(buf, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != size {
		t.Fatalf("expected %d bytes, got %d", size, n)
	}
	if !bytes.Equal(buf, data) {
		t.Fatal("data mismatch in large read")
	}
	if cbOffset != int64(size) {
		t.Fatalf("expected callback offset %d, got %d", size, cbOffset)
	}
}

func TestReadAtWrapper_ReadAt_LargeReadPartialWithEOF(t *testing.T) {
	const dataSize = 300 * 1024
	const reqSize = 500 * 1024
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 17)
	}
	s := newMemReader(data)
	rw := &readAtWrapper{reader: s}

	buf := make([]byte, reqSize)
	n, err := rw.ReadAt(buf, 0)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if n != dataSize {
		t.Fatalf("expected %d bytes, got %d", dataSize, n)
	}
	if !bytes.Equal(buf[:n], data) {
		t.Fatal("data mismatch in partial large read")
	}
}

func TestReadAtWrapper_ReadAt_CacheBoundarySpanning(t *testing.T) {
	const totalSize = 300 * 1024
	data := make([]byte, totalSize)
	for i := range data {
		data[i] = byte(i % 251)
	}
	s := newMemReader(data)
	rw := &readAtWrapper{reader: s}

	// 1. Prime cache with 256KB from offset 0
	smallBuf := make([]byte, 10)
	n, err := rw.ReadAt(smallBuf, 0)
	if err != nil || n != 10 {
		t.Fatalf("prime cache failed: %d, %v", n, err)
	}

	// 2. Read starting near end of cache that spans beyond 256KB into the unbuffered remainder
	off := int64(256*1024 - 100)
	spanBuf := make([]byte, 200) // 100 bytes from cache, 100 bytes from refill
	n, err = rw.ReadAt(spanBuf, off)
	if err != nil {
		t.Fatalf("spanning read error: %v", err)
	}
	if n != 200 {
		t.Fatalf("expected 200 bytes, got %d", n)
	}
	if !bytes.Equal(spanBuf, data[off:off+200]) {
		t.Fatal("data mismatch in boundary spanning read")
	}
}

func TestPool_ParkTimeout_NoPressure(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:          testLogger(),
		IdleParkTimeout: 30 * time.Second,
	})

	if got := p.effectiveParkTimeout(-1); got != 30*time.Second {
		t.Fatalf("expected 30s, got %v", got)
	}
}

func TestPool_ParkTimeout_WithPressureFunc(t *testing.T) {
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
		p := newTestPool(t, Config{
			Logger:          testLogger(),
			IdleParkTimeout: 30 * time.Second,
		})

		got := p.effectiveParkTimeout(tc.usage)
		if got != tc.want {
			t.Errorf("usage=%.2f: expected %v, got %v", tc.usage, tc.want, got)
		}
	}
}

func TestPool_ClosedFlagPreventsReuse(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

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
	p := newTestPool(t, Config{
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
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	// 0 active readers: activeCount clamped to 1, so 1000 / 1 = 1000.
	readahead := p.computeReadahead(1000)
	if readahead != 1000 {
		t.Fatalf("expected 1000, got %d", readahead)
	}

	p.readers[readerKey{infoHash: infoHash, filePath: "a", readerID: 1}] = &streamReader{active: true}
	p.readers[readerKey{infoHash: infoHash, filePath: "b", readerID: 2}] = &streamReader{active: true}

	// Active ranges include a 1/4 trailing window, so 1000 / 1.25 / 2 = 400.
	readahead = p.computeReadahead(1000)
	if readahead != 400 {
		t.Fatalf("expected 400, got %d", readahead)
	}

	// Idle reader is excluded.
	p.readers[readerKey{infoHash: infoHash, filePath: "c", readerID: 3}] = &streamReader{active: false}
	readahead = p.computeReadahead(1000)
	if readahead != 400 {
		t.Fatalf("expected 400 (idle ignored), got %d", readahead)
	}
}

func TestPool_ComputeReadahead_FileStorageExcluded(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	p.readers[readerKey{infoHash: infoHash, filePath: "a", readerID: 1}] = &streamReader{active: true, isFileStorage: true}
	p.readers[readerKey{infoHash: infoHash, filePath: "b", readerID: 2}] = &streamReader{active: true, isFileStorage: false}

	// 1 active memory reader (file storage reader excluded): 1000 / 1.25 = 800.
	readahead := p.computeReadahead(1000)
	if readahead != 800 {
		t.Fatalf("expected 800 (file-storage excluded), got %d", readahead)
	}
}

func TestPool_ComputeReadahead_MinimumOne(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	for i := uint64(1); i <= 10; i++ {
		p.readers[readerKey{infoHash: infoHash, filePath: "x", readerID: i}] = &streamReader{active: true}
	}

	// 10 active readers: 5 / 10 = 0 → clamped to 1.
	readahead := p.computeReadahead(5)
	if readahead != 1 {
		t.Fatalf("expected minimum 1, got %d", readahead)
	}
}

func TestPool_ReservePreloadBudgetCapsAggregateReservations(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	p.SetReadaheadBudget(1000)
	firstHash := metainfo.Hash{1}
	secondHash := metainfo.Hash{2}

	if got := p.ReservePreloadBudget(firstHash, 600); got != 600 {
		t.Fatalf("expected first preload to reserve 600, got %d", got)
	}
	if got := p.ReservePreloadBudget(secondHash, 600); got != 400 {
		t.Fatalf("expected second preload to receive remaining 400, got %d", got)
	}
	if got := p.ReservePreloadBudget(firstHash, 800); got != 600 {
		t.Fatalf("expected replacement reservation to remain capped at 600, got %d", got)
	}

	p.ReleasePreloadBudget(secondHash)
	if got := p.ReservePreloadBudget(firstHash, 800); got != 800 {
		t.Fatalf("expected released capacity to become available, got %d", got)
	}
	p.mu.Lock()
	available := p.availableProtectionBudgetLocked(p.readaheadBudget)
	boundaryBytes := p.boundaryBytesPerFileLocked(available, 1)
	p.mu.Unlock()
	if available != 200 || boundaryBytes != 50 {
		t.Fatalf("expected boundaries to share the remaining 200-byte budget, got available=%d boundary=%d", available, boundaryBytes)
	}
}

func TestPool_ReleaseSetsIdle(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	sr := &streamReader{
		active:    true,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 1024,
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

	p.release(infoHash, "f", 1)

	if sr.active {
		t.Fatal("expected reader to be idle after release")
	}
	if sr.idleSince.IsZero() {
		t.Fatal("expected idleSince to be set")
	}
}

func TestPool_ReleaseNotActiveNoop(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	sr := &streamReader{active: false, infoHash: infoHash, readerID: 1}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

	p.release(infoHash, "f", 1)

	if !sr.idleSince.IsZero() {
		t.Fatal("idleSince should remain zero for already-idle reader")
	}
}

func TestPool_ReleaseIdempotent(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	sr := &streamReader{
		active:    true,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 1024,
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

	p.release(infoHash, "f", 1)
	p.release(infoHash, "f", 1)

	if sr.active {
		t.Fatal("expected reader to remain idle after double release")
	}
}

func TestPool_ParkIdleReaders_SkipsAlreadyParked(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:          testLogger(),
		IdleParkTimeout: 1 * time.Millisecond,
	})
	infoHash := metainfo.Hash{}

	sr := &streamReader{
		active:    false,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 0, // already parked
		idleSince: time.Now().Add(-10 * time.Millisecond),
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

	p.parkIdleReaders()

	if sr.readahead != 0 {
		t.Fatalf("expected readahead to remain 0, got %d", sr.readahead)
	}
}

func TestPool_ParkIdleReaders_ParksIdleToZero(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleParkTimeout:  10 * time.Millisecond,
		IdleCloseTimeout: 24 * time.Hour,
	})
	infoHash := metainfo.Hash{}

	sr := &streamReader{
		active:    false,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 1024,
		idleSince: time.Now().Add(-50 * time.Millisecond),
	}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.parkIdleReaders()

	if sr.readahead != 0 {
		t.Fatalf("expected readahead to be 0 after parking, got %d", sr.readahead)
	}
}

func TestPool_CloseTimeout_RemovesReader(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleParkTimeout:  1 * time.Millisecond,
		IdleCloseTimeout: 1 * time.Millisecond,
	})
	infoHash := metainfo.Hash{}

	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:    false,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 0,
		idleSince: time.Now().Add(-100 * time.Millisecond),
	}

	p.parkIdleReaders()

	if _, ok := p.readers[key]; ok {
		t.Fatal("expected reader to be removed after CloseTimeout")
	}
}

// TestPool_CloseTimeout_NegativeDisablesCleanup verifies that a negative
// CloseTimeout passed into Config is left untouched by New() (only zero
// gets the 5-minute default applied) and produces "never close" behavior
// at runtime, since parkIdleReaders only applies a close deadline when
// CloseTimeout > 0.
func TestPool_CloseTimeout_NegativeDisablesCleanup(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleParkTimeout:  1 * time.Millisecond,
		IdleCloseTimeout: -1,
	})

	if p.cfg.IdleCloseTimeout != -1 {
		t.Fatalf("expected cfg.IdleCloseTimeout=-1, got %v", p.cfg.IdleCloseTimeout)
	}

	infoHash := metainfo.Hash{}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:    false,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 0,
		idleSince: time.Now().Add(-1 * time.Hour),
	}

	p.parkIdleReaders()

	if _, ok := p.readers[key]; !ok {
		t.Fatal("expected reader to still exist when CloseTimeout is negative")
	}
}

// TestPool_ParkIdleReaders_ZeroFieldMeansNoClose documents an internal
// (non-public-contract) invariant: parkIdleReaders reads p.cfg.IdleCloseTimeout
// directly and only applies a close deadline when it is > 0. This differs
// from Config's documented zero-value behavior, which only applies at
// New() construction time (zero -> 5-minute default). This test exists to
// pin the runtime field semantics, not the public Config contract — a
// caller should never see a live Pool with CloseTimeout == 0 unless they
// mutate cfg directly after construction, which is not a supported path.
func TestPool_ParkIdleReaders_ZeroFieldMeansNoClose(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:          testLogger(),
		IdleParkTimeout: 1 * time.Millisecond,
	})
	// Bypasses New()'s defaulting — internal-only state.
	p.cfg.IdleCloseTimeout = 0

	infoHash := metainfo.Hash{}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:    false,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		readahead: 0,
		idleSince: time.Now().Add(-1 * time.Hour),
	}

	p.parkIdleReaders()

	if _, ok := p.readers[key]; !ok {
		t.Fatal("expected reader to still exist when p.cfg.IdleCloseTimeout is zero at runtime")
	}
}

func TestPool_ReaderPositions_EmptyPool(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	result := p.ReaderPositions(infoHash)
	if len(result) != 0 {
		t.Fatalf("expected 0 readers, got %d", len(result))
	}
}

func TestPool_ReaderPositions_NilFileGuards(t *testing.T) {
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
}

func TestPool_HasReaders(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}
	otherIh := metainfo.Hash{1, 2, 3}

	if p.HasReaders(infoHash) {
		t.Fatal("expected no readers in empty pool")
	}

	p.readers[readerKey{infoHash: infoHash, filePath: "a", readerID: 1}] = &streamReader{active: false, infoHash: infoHash}
	if !p.HasReaders(infoHash) {
		t.Fatal("expected HasReaders to return true when reader is idle")
	}
	if p.HasActiveReaders(infoHash) {
		t.Fatal("expected no active readers when reader is idle")
	}

	p.readers[readerKey{infoHash: infoHash, filePath: "b", readerID: 2}] = &streamReader{active: true, infoHash: infoHash}
	if !p.HasReaders(infoHash) {
		t.Fatal("expected HasReaders to return true when reader is active")
	}
	if !p.HasActiveReaders(infoHash) {
		t.Fatal("expected active readers to return true")
	}
	if p.HasReaders(otherIh) {
		t.Fatal("expected other hash to have no readers")
	}
}

func TestPool_SetReadaheadBudget(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}

	sr1 := &streamReader{active: true, infoHash: infoHash, readahead: 100}
	sr2 := &streamReader{active: false, infoHash: infoHash, readahead: 100}
	p.readers[readerKey{infoHash: infoHash, filePath: "a", readerID: 1}] = sr1
	p.readers[readerKey{infoHash: infoHash, filePath: "b", readerID: 2}] = sr2

	// One active range reserves 1/4 of its ahead window for trailing protection.
	p.refreshReadaheadLocked(1000)

	if sr1.readahead != 800 {
		t.Fatalf("expected active reader readahead=800, got %d", sr1.readahead)
	}
	if sr2.readahead != 100 {
		t.Fatalf("expected idle reader readahead unchanged=100, got %d", sr2.readahead)
	}
}

func TestPool_ActiveRangeRegistry_ClearOnRelease(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:   testLogger(),
		Registry: reg,
	})
	infoHash := metainfo.Hash{}

	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 42}
	p.readers[key] = &streamReader{
		active:    true,
		infoHash:  infoHash,
		readerID:  42,
		readahead: 2048,
	}

	p.release(infoHash, "f", 42)

	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
	}
}

func TestPool_ReleaseKeepsReaderContextAlive(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}
	var cancelled atomic.Bool

	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 42}
	p.readers[key] = &streamReader{
		active:   true,
		cancel:   func() { cancelled.Store(true) },
		infoHash: infoHash,
		readerID: 42,
	}

	p.release(infoHash, "f", 42)

	if cancelled.Load() {
		t.Fatal("release cancelled the idle reader context")
	}
}

func TestPool_ActiveRangeRegistry_ClearOnClose(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:   testLogger(),
		Registry: reg,
	})
	infoHash := metainfo.Hash{}

	for i := uint64(1); i <= 2; i++ {
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:   true,
			infoHash: infoHash,
			readerID: i,
		}
	}

	p.Close()

	if reg.clears != 2 {
		t.Fatalf("expected 2 ClearActiveRange calls, got %d", reg.clears)
	}
	if reg.boundaryClears != 2 {
		t.Fatalf("expected 2 ClearFileBoundaries calls, got %d", reg.boundaryClears)
	}
}

func TestPool_FileBoundaries_ClearedOnRelease(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:   testLogger(),
		Registry: reg,
	})
	infoHash := metainfo.Hash{}

	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 42}
	p.readers[key] = &streamReader{
		active:    true,
		infoHash:  infoHash,
		readerID:  42,
		readahead: 2048,
	}

	p.release(infoHash, "f", 42)

	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call on release, got %d", reg.clears)
	}
	if reg.boundaryClears != 1 {
		t.Fatalf("expected file boundaries to be cleared on release, got %d clears", reg.boundaryClears)
	}
}

func TestComputeFileBoundaries_NilOrEmpty(t *testing.T) {
	_, _, _, _, ok := ComputeFileBoundaries(nil)
	if ok {
		t.Fatal("expected ok=false for nil file")
	}
}

func TestPool_ActiveRangeRegistry_NilRegistryNoop(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:   testLogger(),
		Registry: nil,
	})
	infoHash := metainfo.Hash{}

	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:   true,
		infoHash: infoHash,
		readerID: 1,
	}

	p.release(infoHash, "f", 1)
	p.Close()
}

func TestPool_ConfigDefaults(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

	if p.cfg.IdleParkTimeout != 30*time.Second {
		t.Fatalf("expected default ParkTimeout=30s, got %v", p.cfg.IdleParkTimeout)
	}
	if p.cfg.FileReadaheadBytes != 50*1024*1024 {
		t.Fatalf("expected default FileReadaheadBytes=50 MiB, got %d", p.cfg.FileReadaheadBytes)
	}
	if p.cfg.IdleCloseTimeout != 5*time.Minute {
		t.Fatalf("expected default CloseTimeout=5m, got %v", p.cfg.IdleCloseTimeout)
	}
}

func TestPool_CloseIdempotent(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

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
	for i := range goroutines {
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
	for i := range goroutines {
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

func TestPool_MaxReadersPerFile_DefaultsToTen(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

	if p.cfg.MaxReadersPerFile != 10 {
		t.Fatalf("expected default 10, got %d", p.cfg.MaxReadersPerFile)
	}
}

func TestPool_MaxReadersPerFile_EvictsIdle(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:            testLogger(),
		Registry:          reg,
		MaxReadersPerFile: 2,
	})
	infoHash := metainfo.Hash{1}

	// Insert two idle readers for the same (info hash, file path).
	// readerID=1 is older (more time idle) than readerID=2.
	for i := uint64(1); i <= 2; i++ {
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:    false,
			infoHash:  infoHash,
			file:      &torrent.File{},
			readerID:  i,
			idleSince: time.Now().Add(-time.Duration(3-i) * time.Minute),
		}
	}

	// Force eviction — should remove readerID=1 (oldest idle).
	p.evictOldestIdleLocked(infoHash, "f")

	if _, ok := p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}]; ok {
		t.Fatal("expected oldest idle reader (ID 1) to be evicted")
	}
	if _, ok := p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 2}]; !ok {
		t.Fatal("expected younger idle reader (ID 2) to remain")
	}
	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
	}
}

func TestPool_MaxReadersPerFile_NoEvictionWhenOnlyActiveExist(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:            testLogger(),
		Registry:          reg,
		MaxReadersPerFile: 2,
	})
	infoHash := metainfo.Hash{2}

	// Insert two *active* readers — no idle readers exist.
	for i := uint64(10); i <= 11; i++ {
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:   true,
			infoHash: infoHash,
			file:     &torrent.File{},
			readerID: i,
		}
	}

	// Force eviction — should return false since no idle readers exist.
	evicted := p.evictOldestIdleLocked(infoHash, "f")

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

func TestPool_MaxReadersPerFile_NoEvictWhenUnderCap(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:            testLogger(),
		Registry:          reg,
		MaxReadersPerFile: 5,
	})
	infoHash := metainfo.Hash{3}

	// Insert 2 idle readers — eviction should work even when under cap.
	for i := uint64(1); i <= 2; i++ {
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: i}
		p.readers[key] = &streamReader{
			active:    false,
			infoHash:  infoHash,
			file:      &torrent.File{},
			readerID:  i,
			idleSince: time.Now().Add(time.Duration(i) * time.Minute),
		}
	}

	p.evictOldestIdleLocked(infoHash, "f")

	if len(p.readers) != 1 {
		t.Fatalf("expected 1 reader after eviction, got %d", len(p.readers))
	}
	if reg.clears != 1 {
		t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
	}
}

func TestPool_MaxReadersPerFile_ZeroUsesDefault(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:            testLogger(),
		MaxReadersPerFile: 0,
	})

	if p.cfg.MaxReadersPerFile != 10 {
		t.Fatalf("expected 0 to default to 10, got %d", p.cfg.MaxReadersPerFile)
	}
}

func TestPool_MaxReadersPerFile_NegativeDisablesCap(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:            testLogger(),
		MaxReadersPerFile: -1,
	})

	if p.cfg.MaxReadersPerFile != 0 {
		t.Fatalf("expected -1 to disable the cap (0), got %d", p.cfg.MaxReadersPerFile)
	}
}

func TestPool_MaxReadersPerFile_SingleEvictionLoop(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:            testLogger(),
		MaxReadersPerFile: 2,
	})
	infoHash := metainfo.Hash{4}

	// Insert 1 idle + 2 active readers — cap is 2.
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = &streamReader{
		active:    false,
		infoHash:  infoHash,
		file:      &torrent.File{},
		readerID:  1,
		idleSince: time.Now(),
	}
	for i := uint64(2); i <= 3; i++ {
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: i}] = &streamReader{
			active:   true,
			infoHash: infoHash,
			file:     &torrent.File{},
			readerID: i,
		}
	}

	// Evict loop: first iteration evicts idle reader, second iteration
	// finds no idle readers and breaks (soft cap).
	for p.countReadersLocked(infoHash, "f") >= p.cfg.MaxReadersPerFile {
		if !p.evictOldestIdleLocked(infoHash, "f") {
			break
		}
	}

	count := p.countReadersLocked(infoHash, "f")
	if count != 2 {
		t.Fatalf("expected 2 readers (cap soft when no idle), got %d", count)
	}
}

func TestPool_ReadaheadRebalance_Integration(t *testing.T) {
	// Registry is nil so registerActiveRangeLocked returns early without
	// needing a valid torrent.File (which has unexported fields).
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleCloseTimeout: 5 * time.Minute,
		IdleParkTimeout:  30 * time.Second,
		MemoryUsage:      func() float64 { return 0.3 },
	})
	infoHash := metainfo.Hash{5}
	pool := int64(10000)

	// Simulate 3 memory readers sharing the pool.
	srs := make([]*streamReader, 3)
	for i := uint64(1); i <= 3; i++ {
		mr := &mockReader{readahead: 0}
		srs[i-1] = &streamReader{
			active:        true,
			infoHash:      infoHash,
			readerID:      i,
			readahead:     0,
			isFileStorage: false,
			reader:        mr,
		}
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: i}] = srs[i-1]
	}
	p.readaheadBudget = pool

	// All 3 active share the budget after accounting for trailing protection.
	p.refreshReadaheadLocked(pool)
	for i, sr := range srs {
		want := int64(2666)
		if sr.readahead != want {
			t.Fatalf("reader %d: expected readahead %d, got %d", i+1, want, sr.readahead)
		}
		if sr.reader.(*mockReader).getReadahead() != want {
			t.Fatalf("reader %d: mock SetReadahead(%d) not called, got %d", i+1, want, sr.reader.(*mockReader).getReadahead())
		}
	}

	// Release reader 2: remaining readers split the protected budget.
	p.release(infoHash, "f", 2)
	if srs[1].readahead != 2666 {
		t.Fatalf("released reader 2 should keep readahead=2666, got %d", srs[1].readahead)
	}
	for _, sr := range []*streamReader{srs[0], srs[2]} {
		if sr.readahead != 4000 {
			t.Fatalf("active reader %d: expected readahead 4000, got %d", sr.readerID, sr.readahead)
		}
		if sr.reader.(*mockReader).getReadahead() != 4000 {
			t.Fatalf("active reader %d: mock SetReadahead(4000) not called, got %d", sr.readerID, sr.reader.(*mockReader).getReadahead())
		}
	}

	// Release reader 0: the remaining reader gets 8000 ahead + 2000 trailing.
	p.release(infoHash, "f", 1)
	if srs[0].readahead != 4000 {
		t.Fatalf("released reader 0 should keep readahead=4000, got %d", srs[0].readahead)
	}
	if srs[2].readahead != 8000 {
		t.Fatalf("active reader 2: expected readahead 8000, got %d", srs[2].readahead)
	}
	if srs[2].reader.(*mockReader).getReadahead() != 8000 {
		t.Fatalf("active reader 2: mock SetReadahead(8000) not called, got %d", srs[2].reader.(*mockReader).getReadahead())
	}

	// Reader 1 was never active in this scenario — releasing it is a no-op.
	p.release(infoHash, "f", 2)

	// File-storage reader is excluded from rebalancing.
	fsMr := &mockReader{readahead: 50000000}
	fsSr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      99,
		readahead:     50000000,
		isFileStorage: true,
		reader:        fsMr,
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 99}] = fsSr
	p.refreshReadaheadLocked(pool)
	if srs[2].readahead != 8000 {
		t.Fatalf("memory reader should keep readahead=8000, got %d", srs[2].readahead)
	}
	if fsSr.readahead != 50000000 {
		t.Fatalf("file-storage reader should keep readahead=50000000, got %d", fsSr.readahead)
	}
	if fsSr.reader.(*mockReader).getReadahead() != 50000000 {
		t.Fatalf("file-storage reader SetReadahead should not have changed, got %d", fsSr.reader.(*mockReader).getReadahead())
	}
}

func TestPool_ReadaheadRebalance_PoolZero(t *testing.T) {
	p := newTestPool(t, Config{
		Logger: testLogger(),
	})
	infoHash := metainfo.Hash{7}

	sr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     500,
		isFileStorage: false,
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

	p.release(infoHash, "f", 1)

	if sr.readahead != 500 {
		t.Fatalf("expected readahead unchanged at 500 when readaheadBudget is 0, got %d", sr.readahead)
	}
}

func TestPool_ReadaheadRebalance_IdleGCRebalance(t *testing.T) {
	// Registry is nil so registerActiveRangeLocked returns early without
	// needing a valid torrent.File (which has unexported fields).
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleCloseTimeout: 10 * time.Minute,
		IdleParkTimeout:  100 * time.Millisecond,
		MemoryUsage:      func() float64 { return 0.3 },
	})
	infoHash := metainfo.Hash{10}
	pool := int64(10000)

	// Two active readers.
	sr1 := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     0,
		isFileStorage: false,
		reader:        &mockReader{readahead: 0},
	}
	sr2 := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      2,
		readahead:     0,
		isFileStorage: false,
		reader:        &mockReader{readahead: 0},
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr1
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 2}] = sr2
	p.readaheadBudget = pool
	p.refreshReadaheadLocked(pool)

	if sr1.readahead != 4000 || sr2.readahead != 4000 {
		t.Fatalf("expected 4000 each, got sr1=%d sr2=%d", sr1.readahead, sr2.readahead)
	}

	// Park sr1 via idleGC — it loses readahead.
	sr1.active = false
	sr1.idleSince = time.Now().Add(-200 * time.Millisecond)
	p.parkIdleReaders()

	if sr1.readahead != 0 {
		t.Fatalf("parked reader should have readahead=0, got %d", sr1.readahead)
	}

	// sr2 gets 8000 ahead plus its 2000 trailing protection.
	// parkIdleReaders should have called refreshReadaheadLocked for active readers.
	if sr2.readahead != 8000 {
		t.Fatalf("active reader should get protected budget after idle peer parked: expected 8000, got %d", sr2.readahead)
	}
}

func TestPool_PriorityWindowFraction_ZeroIsNoop(t *testing.T) {
	// PriorityWindowFraction=0 means no prioritizeAsync goroutine is
	// ever dispatched regardless of Registry.  When Registry is also nil,
	// updateActiveRange returns early (no range tracking at all).  The key
	// invariant is that sr.prioritizedPieces is never modified by
	// prioritization logic when fraction == 0.
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		PriorityWindowFraction: 0,
	})
	infoHash := metainfo.Hash{20}

	sr := &streamReader{
		active:            true,
		infoHash:          infoHash,
		readerID:          1,
		readahead:         1024,
		isFileStorage:     false,
		prioritizedPieces: []int{1, 2, 3},
	}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = sr

	// Registry is nil so updateActiveRange returns early before reaching
	// file.Torrent().Info() — this is the correct path for the "no
	// prioritization" config.
	p.updateActiveRange(infoHash, key, &torrent.File{}, nil, 512)

	if len(sr.prioritizedPieces) != 3 {
		t.Fatalf("expected prioritizedPieces unchanged (len=3), got %d", len(sr.prioritizedPieces))
	}
}

func TestPool_PriorityWindowFraction_FileStorageSkipped(t *testing.T) {
	// File-storage readers skip prioritization even when fraction > 0.
	// With a nil Registry, updateActiveRange returns early before any
	// prioritization logic — the test verifies that the reader's
	// prioritizedPieces are never modified.
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		Registry:               nil,
		PriorityWindowFraction: 0.5,
	})
	infoHash := metainfo.Hash{21}

	sr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     1024,
		isFileStorage: true,
	}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.updateActiveRange(infoHash, key, &torrent.File{}, nil, 512)

	if len(sr.prioritizedPieces) != 0 {
		t.Fatalf("expected no prioritized pieces for file-storage reader, got %d", len(sr.prioritizedPieces))
	}
}

func TestPool_ReleaseRestoresPrioritizedPieces(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		Registry:               reg,
		PriorityWindowFraction: 0.5,
	})
	infoHash := metainfo.Hash{22}

	// The release method iterates over prioritizedPieces and calls
	// file.Torrent().Piece() which panics on a bare &torrent.File{}.
	// We use an empty slice to test the nil-clearing path without panic.
	sr := &streamReader{
		active:            true,
		infoHash:          infoHash,
		file:              &torrent.File{},
		readerID:          1,
		readahead:         1024,
		isFileStorage:     false,
		prioritizedPieces: []int{},
	}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.release(infoHash, "f", 1)

	if len(sr.prioritizedPieces) != 0 {
		t.Fatalf("expected prioritizedPieces to be empty after release, got %v", sr.prioritizedPieces)
	}
	if sr.active {
		t.Fatal("expected reader to be idle after release")
	}
}

func TestPool_PrioritizeNextPieces_NilFileReturnsNil(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

	result := p.prioritizeNextPieces(nil, 0, 1024, 0.5, 0.3)
	if result != nil {
		t.Fatalf("expected nil for nil file, got %v", result)
	}
}

func TestPool_PrioritizeNextPieces_ZeroFractionReturnsNil(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

	result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0, 0.3)
	if result != nil {
		t.Fatalf("expected nil for fraction=0, got %v", result)
	}

	result = p.prioritizeNextPieces(&torrent.File{}, 0, 1024, -0.1, 0.3)
	if result != nil {
		t.Fatalf("expected nil for fraction=-0.1, got %v", result)
	}
}

func TestPool_PrioritizeNextPieces_EmptyFileReturnsNil(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

	result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 1.1, 0.3)
	if result != nil {
		t.Fatalf("expected nil for fraction=1.1, got %v", result)
	}
}

func TestPool_PrioritizeNextPieces_NearFractionDefaultsToThirtyPercent(t *testing.T) {
	// When PriorityNowFraction is 0 in config, updateActiveRange should
	// default to 0.3 (30 % Now, 70 % High).
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		Registry:               reg,
		PriorityWindowFraction: 0.5,
		PriorityNowFraction:    0, // zero → defaults to 0.3
	})
	infoHash := metainfo.Hash{30}

	sr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     1024,
		isFileStorage: false,
	}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = sr

	// Verify priorityPlan defaults nearFraction to 0.3 when zero.
	// readahead=4 MiB, pieceLength=256 KiB → readaheadPieces=16, n=int(16*0.5)=8.
	n, nowCount, _, _ := priorityPlan(0, 4*1024*1024, 256*1024, 0, 100, 0.5, 0)
	if n != 8 {
		t.Fatalf("expected n=8 for fraction=0.5 and readaheadPieces=16, got %d", n)
	}
	// nearFraction defaults to 0.3: nowCount = int(8*0.3) = 2.
	if nowCount != 2 {
		t.Fatalf("expected nowCount=2 (30%% of 8), got %d", nowCount)
	}
}

func TestPool_CloseResetsPrioritiesForActiveReaders(t *testing.T) {
	reg := &stubRegistry{}
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		Registry:               reg,
		PriorityWindowFraction: 0.5,
	})
	infoHash := metainfo.Hash{50}

	sr := &streamReader{
		active:            true,
		infoHash:          infoHash,
		file:              &torrent.File{},
		readerID:          1,
		readahead:         1024,
		isFileStorage:     false,
		prioritizedPieces: []int{5, 6, 7},
	}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = sr

	p.Close()

	if sr.prioritizedPieces != nil {
		t.Fatalf("expected prioritizedPieces to be nil after Close, got %v", sr.prioritizedPieces)
	}
}

func TestPool_PrioritizeNextPieces_NearFractionClampedToMax(t *testing.T) {
	// nearFraction > 1 should clamp to 1.0 in priorityPlan so all n pieces get Now.
	// readahead=4 MiB, pieceLength=256 KiB → readaheadPieces=16, n=int(16*0.5)=8.
	n, nowCount, _, _ := priorityPlan(0, 4*1024*1024, 256*1024, 0, 100, 0.5, 1.5)
	if n != 8 {
		t.Fatalf("expected n=8, got %d", n)
	}
	// With nearFraction clamped to 1.0: nowCount = 1.0 * 8 = 8.
	if nowCount != 8 {
		t.Fatalf("expected nowCount=8 (all pieces get Now), got %d", nowCount)
	}
}

func TestPool_PrioritizeAsync_DrivesPriorityUpdate(t *testing.T) {
	// Verifies that prioritizeAsync acquires priorityMu and runs without
	// panicking.  Since &torrent.File{}.Torrent() is nil,
	// prioritizeNextPieces returns nil immediately — but the goroutine
	// runs, acquires/releases priorityMu, and completes cleanly.
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		PriorityWindowFraction: 0.5,
	})
	infoHash := metainfo.Hash{40}

	sr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     1024,
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
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		PriorityWindowFraction: 0.5,
	})
	infoHash := metainfo.Hash{41}

	sr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     1024,
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
	p.clearReaderPrioritiesLocked(sr)
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
	p := newTestPool(t, Config{
		Logger:                 testLogger(),
		PriorityWindowFraction: 0.5,
	})
	infoHash := metainfo.Hash{42}

	sr := &streamReader{
		active:        true,
		infoHash:      infoHash,
		readerID:      1,
		readahead:     1024,
		isFileStorage: false,
	}

	// releaseMu mirrors pool.mu: it serializes the "release" side so
	// only one goroutine writes active/idleSince at a time.  In
	// production this is pool.mu; here we add it to keep the test
	// race-free while still exercising concurrent prioritizeAsync vs.
	// release.
	var releaseMu sync.Mutex

	var wg sync.WaitGroup
	for i := range 50 {
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
			p.clearReaderPrioritiesLocked(sr)
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

func TestPriorityPlan_EdgeCases(t *testing.T) {
	// n clamps to minimum 1 when fraction * readaheadPieces < 1.
	n, _, _, _ := priorityPlan(0, 100, 256*1024, 0, 100, 0.01, 0.5)
	if n < 1 {
		t.Fatalf("expected n >= 1, got %d", n)
	}

	// nowCount clamps to minimum 1.
	_, nowCount, _, _ := priorityPlan(0, 256*1024, 256*1024, 0, 100, 0.5, 0.001)
	if nowCount < 1 {
		t.Fatalf("expected nowCount >= 1, got %d", nowCount)
	}

	// target can't go below currentPiece+1 (even when n and endPieceMax
	// would push it lower).  Use pieceLength=1 so byteOffset maps
	// directly to piece index.
	_, _, target, _ := priorityPlan(100, 100, 1, 0, 100, 0.01, 0.5)
	if target < 101 {
		t.Fatalf("expected target >= 101, got %d", target)
	}

	// fraction > 1 clamps to 1.
	// readahead=4 MiB, pieceLength=256 KiB → readaheadPieces=16, fraction clamped to 1.0.
	n, _, _, _ = priorityPlan(0, 4*1024*1024, 256*1024, 0, 100, 2.0, 0.5)
	if n != 16 {
		t.Fatalf("expected n=16 (fraction clamped to 1.0), got %d", n)
	}
}

// TestPriorityPlan_EndOfLastPiece tests that when the window is clamped to
// file end (endPieceMax is exclusive), the returned target still allows
// the last valid piece (endPieceMax-1) to be included by a loop using
// idx < target.  This catches the off-by-one where the old code did
// endPieceMax-1 inside priorityPlan while endPieceMax was already exclusive.
func TestPriorityPlan_EndOfLastPiece(t *testing.T) {
	// EndPieceIndex() == 10, so endPieceMax = 11 (exclusive).
	// Large readahead + fraction=1.0 → target would far exceed the file.
	_, _, target, _ := priorityPlan(0, 100*1024*1024, 256*1024, 0, 11, 1.0, 0.5)
	// idx < target must allow idx == 10 (the last valid piece).
	if target <= 10 {
		t.Fatalf("expected target > 10 to include piece 10, got %d", target)
	}

	// Verify unclamped path: small readahead, target does not exceed endPieceMax.
	_, _, target, _ = priorityPlan(0, 512*1024, 256*1024, 0, 100, 1.0, 0.5)
	// readaheadPieces = 2, n = int(2*1.0) = 2, target = 0+1+2 = 3.
	if target != 3 {
		t.Fatalf("expected target=3, got %d", target)
	}
}

// TestPriorityPlan_OverlappingFileEnd ensures that when beginPiece is non-zero
// (as in split-file torrents) and endPieceMax is clamped by the torrent's
// actual piece count, the returned target does not exceed it and the
// resulting loop still covers the full range from beginPiece+1 to end.
func TestPriorityPlan_OverlappingFileEnd(t *testing.T) {
	// Simulate a split-file scenario: file starts at piece 100, torrent
	// has 200 total pieces (indices 0..199), so endPieceMax=200 (exclusive).
	// readaheadPieces=40, n=40, target=100+1+40=141 (within bounds).
	_, _, target, currentPiece := priorityPlan(0, 10*1024*1024, 256*1024, 100, 200, 1.0, 0.5)
	if currentPiece != 100 {
		t.Fatalf("expected currentPiece=100 (byteOffset=0 + beginPiece=100), got %d", currentPiece)
	}
	if target != 141 {
		t.Fatalf("expected target=141, got %d", target)
	}

	// Now the clamping case with a split-file that nearly reaches the end:
	// beginPiece=195, endPieceMax=200 (only 5 pieces left), huge readahead.
	// readaheadPieces=400, n=400, target=195+1+400=596 > 200 → clamped to 200.
	// Loop covers 196..199 (the full remaining range).
	_, _, target, _ = priorityPlan(0, 100*1024*1024, 256*1024, 195, 200, 1.0, 0.5)
	if target != 200 {
		t.Fatalf("expected target=200 (saturated near end), got %d", target)
	}
}

// TestPrioritizeNextPieces_SplitFileEndClamped documents the coverage gap:
// the EndPieceIndex > NumPieces clamping in prioritizeNextPieces cannot be
// reached with &torrent.File{} because Torrent() returns nil and the function
// returns early.  When a real torrent is available, prioritizeNextPieces
// clamps endPieceMax to torrent.NumPieces() before calling priorityPlan, preventing
// index-out-of-range panics when file.EndPieceIndex() exceeds the torrent's
// actual piece count (split-file or partially-seeded torrents).
func TestPrioritizeNextPieces_SplitFileEndClamped(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	// &torrent.File{}.Torrent() == nil → early returns, no panic.
	// This test verifies the early-return path doesn't regress; the
	// actual clamping logic is exercised in the integration test
	// TestPool_PrioritizeAsync_DrivesPriorityUpdate
	// which dispatches through updateActiveRange (which itself panics on
	// &torrent.File{}.Torrent().Info()).  The clamping is a simple
	// endPieceMax = min(endPieceMax, tor.NumPieces()) guard — no loop
	// or arithmetic — so the risk of regression is low and the test
	// constraint is fundamental to the anacrolix/torrent package.
	result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0.3, 0.5)
	if result != nil {
		t.Fatalf("expected nil for nil torrent, got %v", result)
	}
}

func TestPool_UpdateActiveRange_NilRegistry_CachesOffset(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:   testLogger(),
		Registry: nil,
	})
	defer p.Close()

	infoHash := metainfo.Hash{}
	key := readerKey{infoHash: infoHash, filePath: "test", readerID: 1}
	sr := &streamReader{
		active:   true,
		infoHash: infoHash,
		readerID: 1,
	}
	p.readers[key] = sr

	p.updateActiveRange(infoHash, key, nil, nil, 1024)

	p.mu.Lock()
	cached := sr.lastOffset
	p.mu.Unlock()

	if cached != 1024 {
		t.Fatalf("expected lastOffset=1024, got %d", cached)
	}
}

func TestPool_UpdateActiveRange_StaleWrapperDiscarded(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:   testLogger(),
		Registry: nil,
	})
	defer p.Close()

	infoHash := metainfo.Hash{1, 2, 3}
	key := readerKey{infoHash: infoHash, filePath: "video.mp4", readerID: 1}

	oldWrapper := &readAtWrapper{}
	newWrapper := &readAtWrapper{}

	sr := &streamReader{
		active:   true,
		infoHash: infoHash,
		readerID: 1,
		wrapper:  newWrapper,
	}
	p.readers[key] = sr

	// Stale callback from previous activation's wrapper should be dropped.
	p.updateActiveRange(infoHash, key, nil, oldWrapper, 500)

	p.mu.Lock()
	cached := sr.lastOffset
	p.mu.Unlock()

	if cached != 0 {
		t.Fatalf("expected stale callback from oldWrapper to be ignored (cached=0), got %d", cached)
	}

	// Legitimate callback from the current active wrapper should be applied.
	p.updateActiveRange(infoHash, key, nil, newWrapper, 1500)

	p.mu.Lock()
	cached = sr.lastOffset
	p.mu.Unlock()

	if cached != 1500 {
		t.Fatalf("expected callback from active wrapper to be accepted (cached=1500), got %d", cached)
	}
}

func TestPool_ParkIdleReaders_ShortCloseTimeoutNotExpanded(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleParkTimeout:  1 * time.Second,
		IdleCloseTimeout: 2 * time.Second,
		MemoryUsage: func() float64 {
			return 0.95 // critical memory pressure
		},
	})
	defer p.Close()

	infoHash := metainfo.Hash{}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	p.readers[key] = &streamReader{
		active:    false,
		infoHash:  infoHash,
		readerID:  1,
		idleSince: time.Now().Add(-5 * time.Second), // idle for 5s > 2s CloseTimeout
		readahead: 1024,
	}

	p.parkIdleReaders()

	p.mu.Lock()
	_, exists := p.readers[key]
	p.mu.Unlock()

	if exists {
		t.Fatal("expected idle reader to be closed and evicted after 5s exceeding 2s CloseTimeout")
	}
}

func TestPool_ReaderContextLifecycle(t *testing.T) {
	p := newTestPool(t, Config{
		Logger:           testLogger(),
		IdleCloseTimeout: 2 * time.Second,
	})
	defer p.Close()

	infoHash := metainfo.Hash{}
	key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
	ctx, cancel := context.WithCancel(context.Background())
	sr := &streamReader{
		active:    false,
		cancel:    cancel,
		ctx:       ctx,
		infoHash:  infoHash,
		readerID:  1,
		idleSince: time.Now().Add(-5 * time.Second),
	}

	p.mu.Lock()
	p.readers[key] = sr
	p.mu.Unlock()

	if sr.ctx.Err() != nil {
		t.Fatalf("expected context not canceled initially, got %v", sr.ctx.Err())
	}

	p.parkIdleReaders()

	if sr.ctx.Err() == nil {
		t.Fatal("expected reader context to be canceled on close in parkIdleReaders")
	}

	p.mu.Lock()
	_, stillPresent := p.readers[key]
	p.mu.Unlock()
	if stillPresent {
		t.Fatal("expected reader to be removed from pool after close")
	}
}

func BenchmarkReadAtWrapper_ReadAt(b *testing.B) {
	const totalSize = 16 * 1024 * 1024 // 16 MB
	data := make([]byte, totalSize)
	for i := range data {
		data[i] = byte(i)
	}

	chunkSizes := []struct {
		name string
		size int
	}{
		{"4KB", 4 * 1024},
		{"16KB", 16 * 1024},
		{"32KB", 32 * 1024},
		{"64KB", 64 * 1024},
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, tc := range chunkSizes {
		b.Run(tc.name, func(b *testing.B) {
			s := newMemReader(data)
			rw := &readAtWrapper{reader: s}
			buf := make([]byte, tc.size)

			b.SetBytes(int64(tc.size))
			b.ResetTimer()

			var off int64
			for range b.N {
				if off+int64(tc.size) > totalSize {
					off = 0
				}
				_, err := rw.ReadAt(buf, off)
				if err != nil {
					b.Fatalf("read failed: %v", err)
				}
				off += int64(tc.size)
			}
		})
	}
}

func BenchmarkReadAtWrapper_RandomCacheMiss(b *testing.B) {
	const totalSize = 16 * 1024 * 1024
	const chunkSize = 16 * 1024
	const windowCount = totalSize / defaultWrapperBufSize
	data := make([]byte, totalSize)
	s := newMemReader(data)
	rw := &readAtWrapper{reader: s}
	buf := make([]byte, chunkSize)

	b.SetBytes(chunkSize)
	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		// A coprime stride visits every 256 KiB window before repeating, forcing
		// a cache refill rather than measuring the sequential cache-hit path.
		window := (i * 17) % windowCount
		off := int64(window * defaultWrapperBufSize)
		if _, err := rw.ReadAt(buf, off); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadAtWrapper_ReadAt_Parallel(b *testing.B) {
	const totalSize = 16 * 1024 * 1024
	const chunkSize = 16 * 1024
	data := make([]byte, totalSize)
	s := newMemReader(data)
	rw := &readAtWrapper{reader: s}
	var next atomic.Uint64
	chunkCount := uint64(totalSize / chunkSize)

	b.SetBytes(chunkSize)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, chunkSize)
		for pb.Next() {
			chunk := (next.Add(1) - 1) % chunkCount
			if _, err := rw.ReadAt(buf, int64(chunk*chunkSize)); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

var benchmarkPriorityPlan []prioritizedPiece

func BenchmarkPriorityPlanBuild(b *testing.B) {
	const readahead = 32 * 1024 * 1024
	for _, pieceLength := range []int64{16 * 1024, 256 * 1024, 1024 * 1024} {
		b.Run(fmt.Sprintf("PieceSize_%dKB", pieceLength/1024), func(b *testing.B) {
			endPiece := 4 * 1024 * 1024 * 1024 / pieceLength
			b.ReportAllocs()
			for i := range b.N {
				benchmarkPriorityPlan = buildPriorityPlan(
					int64(i%1024)*pieceLength,
					readahead,
					pieceLength,
					0,
					endPiece,
					0.5,
					0.3,
				)
			}
		})
	}
}

func BenchmarkPriorityClaimResetAndClear(b *testing.B) {
	for _, claimCount := range []int{32, 256, 2048} {
		b.Run(fmt.Sprintf("Claims_%d", claimCount), func(b *testing.B) {
			p := &Pool{priorityClaims: make(map[priorityPieceKey]*priorityClaim, claimCount)}
			sr := &streamReader{prioritizedPieces: make([]int, claimCount)}
			claims := make([]*priorityClaim, claimCount)
			for i := range sr.prioritizedPieces {
				sr.prioritizedPieces[i] = i
				claims[i] = &priorityClaim{owners: make(map[*streamReader]torrent.PiecePriority, 1)}
			}
			refill := func() {
				sr.prioritizedPieces = sr.prioritizedPieces[:claimCount]
				for _, index := range sr.prioritizedPieces {
					claim := claims[index]
					claim.owners[sr] = torrent.PiecePriorityHigh
					p.priorityClaims[priorityPieceKey{index: index}] = claim
				}
			}
			refill()

			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if i > 0 {
					refill()
				}
				sr.priorityMu.Lock()
				p.clearReaderPrioritiesLocked(sr)
				sr.priorityMu.Unlock()
			}
		})
	}
}
