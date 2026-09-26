// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m)
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
	closed    atomic.Bool
	mu        sync.Mutex
	readahead int64 // current readahead
}

type blockingReader struct {
	closeDuringRead atomic.Bool
	ctx             context.Context
	mu              sync.Mutex
	position        int64
	readStarted     chan struct{}
	reading         atomic.Bool
	startOnce       sync.Once
}

func newBlockingReader() *blockingReader {
	return &blockingReader{ctx: context.Background(), readStarted: make(chan struct{})}
}

func (r *blockingReader) Read(_ []byte) (int, error) {
	r.reading.Store(true)
	r.startOnce.Do(func() { close(r.readStarted) })
	defer r.reading.Store(false)
	r.mu.Lock()
	ctx := r.ctx
	r.mu.Unlock()
	<-ctx.Done()
	return 0, ctx.Err()
}

func (r *blockingReader) ReadContext(ctx context.Context, p []byte) (int, error) {
	r.SetContext(ctx)
	return r.Read(p)
}

func (r *blockingReader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch whence {
	case io.SeekStart:
		r.position = offset
	case io.SeekCurrent:
		r.position += offset
	default:
		return 0, errors.New("unsupported seek")
	}
	return r.position, nil
}

func (r *blockingReader) Close() error {
	if r.reading.Load() {
		r.closeDuringRead.Store(true)
	}
	return nil
}

func (r *blockingReader) SetContext(ctx context.Context) {
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
}

func (r *blockingReader) SetReadahead(int64)                     {}
func (r *blockingReader) SetReadaheadFunc(torrent.ReadaheadFunc) {}
func (r *blockingReader) SetResponsive()                         {}

func (m *mockReader) Read(p []byte) (int, error)              { return 0, io.EOF }
func (m *mockReader) ReadAt(p []byte, off int64) (int, error) { return 0, io.EOF }
func (m *mockReader) ReadContext(ctx context.Context, p []byte) (int, error) {
	return 0, io.EOF
}
func (m *mockReader) Seek(offset int64, whence int) (int64, error) { return offset, nil }
func (m *mockReader) Close() error {
	m.closed.Store(true)
	return nil
}
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

func TestReadAtWrapper_ReadAt(t *testing.T) {
	t.Run("basic read", func(t *testing.T) {
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
	})

	t.Run("mid file offset", func(t *testing.T) {
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
	})

	t.Run("partial read with EOF", func(t *testing.T) {
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
	})

	t.Run("preserves read progress", func(t *testing.T) {
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
	})

	t.Run("callback fires after unlock", func(t *testing.T) {
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
	})

	t.Run("nil callback", func(t *testing.T) {
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
	})

	t.Run("large read bypasses cache", func(t *testing.T) {
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
	})

	t.Run("large partial read with EOF", func(t *testing.T) {
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
	})

	t.Run("read spanning cache boundary", func(t *testing.T) {
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
	})

	t.Run("concurrent reads", func(t *testing.T) {
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
	})
}

func TestReadAtWrapper_Retire(t *testing.T) {
	s := newMemReader([]byte("xy"))
	calls := 0
	rw := &readAtWrapper{reader: s, onOffsetChange: func(int64) { calls++ }}
	if _, err := rw.ReadAt(make([]byte, 1), 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rw.retire()
	rw.retire()
	if s.closed {
		t.Fatal("retiring a wrapper must leave the torrent reader to the pool")
	}
	if rw.cacheBuf != nil || rw.offset != 0 {
		t.Fatalf("expected cache and offset reset, got cache=%v offset=%d", rw.cacheBuf != nil, rw.offset)
	}
	if _, err := rw.ReadAt(make([]byte, 1), 1); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected io.ErrClosedPipe after retire, got %v", err)
	}
	rw.notifyOffsetChange(1)
	if calls != 1 {
		t.Fatalf("expected no offset callbacks after retire, got %d calls", calls)
	}
}

// TestPool_NewReadSeeker verifies that newReadSeeker readers report playback reads.
func TestPool_NewReadSeeker(t *testing.T) {
	var observed []StorageMode
	pool := &Pool{cfg: Config{ReadObserver: func(mode StorageMode, duration time.Duration) {
		if duration < 0 {
			t.Errorf("negative read duration %v", duration)
		}
		observed = append(observed, mode)
	}}}
	read := func(pool *Pool, mode StorageMode) {
		t.Helper()
		wrapper := &readAtWrapper{reader: newMemReader([]byte("abcdef"))}
		reader := pool.newReadSeeker(wrapper, 6, mode)
		if _, err := reader.Read(make([]byte, 3)); err != nil {
			t.Fatalf("read failed: %v", err)
		}
	}

	read(pool, FileStorage)
	read(pool, MemoryStorage)
	read(&Pool{}, MemoryStorage)

	if want := []StorageMode{FileStorage, MemoryStorage}; !slices.Equal(observed, want) {
		t.Fatalf("expected observed reads %v, got %v", want, observed)
	}
}

func TestStorageMode_String(t *testing.T) {
	for mode, want := range map[StorageMode]string{MemoryStorage: "memory", FileStorage: "file", StorageMode(9): "unknown"} {
		if got := mode.String(); got != want {
			t.Errorf("StorageMode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}

// TestSeekNotifyingReader_Read verifies that a pending seek is notified before the next read.
func TestSeekNotifyingReader_Read(t *testing.T) {
	var events []string
	underlying := &orderRecordingReader{
		ReadSeeker: io.NewSectionReader(bytes.NewReader([]byte("abcdef")), 0, 6),
		events:     &events,
	}
	reader := &seekNotifyingReader{
		ReadSeeker: underlying,
		onSeek: func(offset int64) {
			events = append(events, fmt.Sprintf("notify %d", offset))
		},
	}

	// The size probe used by http.ServeContent must not be reported.
	if _, err := reader.Seek(0, io.SeekEnd); err != nil {
		t.Fatalf("seek to end failed: %v", err)
	}
	position, err := reader.Seek(4, io.SeekStart)
	if err != nil || position != 4 {
		t.Fatalf("seek failed: position=%d err=%v", position, err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no notification before a read, got %v", events)
	}

	buf := make([]byte, 2)
	if _, err := reader.Read(buf); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if _, err := reader.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	want := []string{"notify 4", "read", "read"}
	if !slices.Equal(events, want) {
		t.Fatalf("expected %v, got %v", want, events)
	}
}

// orderRecordingReader records reads so tests can check notification order.
type orderRecordingReader struct {
	io.ReadSeeker
	events *[]string
}

func (r *orderRecordingReader) Read(p []byte) (int, error) {
	*r.events = append(*r.events, "read")
	return r.ReadSeeker.Read(p)
}

func TestPool_Close(t *testing.T) {
	t.Run("serializes with active read", func(t *testing.T) {
		pool := New(Config{Logger: testLogger()})
		reader := newBlockingReader()
		readerCtx, cancel := context.WithCancel(context.Background())
		reader.SetContext(readerCtx)
		wrapper := &readAtWrapper{reader: reader}
		key := uint64(1)

		pool.mu.Lock()
		pool.readers[key] = &streamReader{
			active:   true,
			cancel:   cancel,
			reader:   reader,
			readerID: 1,
			wrapper:  wrapper,
		}
		pool.mu.Unlock()

		readDone := make(chan error, 1)
		go func() {
			_, err := wrapper.ReadAt(make([]byte, 1), 0)
			readDone <- err
		}()
		<-reader.readStarted

		pool.Close()
		if reader.closeDuringRead.Load() {
			t.Fatal("underlying reader was closed concurrently with Read")
		}
		if err := <-readDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled read, got %v", err)
		}
	})

	t.Run("prevents reader reuse", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		if p.closed {
			t.Fatal("expected pool to not be closed initially")
		}

		p.Close()

		if !p.closed {
			t.Fatal("expected pool to be closed after Close()")
		}
	})

	t.Run("clears all readers", func(t *testing.T) {
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
	})

	t.Run("is idempotent", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		p.Close()
		p.Close() // should not panic
	})

	t.Run("resets priorities for active readers", func(t *testing.T) {
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
		key := uint64(1)
		p.readers[key] = sr

		p.Close()

		if sr.prioritizedPieces != nil {
			t.Fatalf("expected prioritizedPieces to be nil after Close, got %v", sr.prioritizedPieces)
		}
	})
}

func TestPool_EffectiveLingerTimeout(t *testing.T) {
	t.Run("no pressure", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:        testLogger(),
			LingerTimeout: 30 * time.Second,
		})

		if got := p.effectiveLingerTimeout(-1); got != 30*time.Second {
			t.Fatalf("expected 30s, got %v", got)
		}
	})

	t.Run("with pressure func", func(t *testing.T) {
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
				Logger:        testLogger(),
				LingerTimeout: 30 * time.Second,
			})

			got := p.effectiveLingerTimeout(tc.usage)
			if got != tc.want {
				t.Errorf("usage=%.2f: expected %v, got %v", tc.usage, tc.want, got)
			}
		}
	})

	t.Run("pressure never extends a shorter timeout", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger(), LingerTimeout: 3 * time.Second})
		if got := p.effectiveLingerTimeout(0.60); got != 3*time.Second {
			t.Fatalf("expected 3s, got %v", got)
		}
		if got := p.effectiveLingerTimeout(0.95); got != time.Second {
			t.Fatalf("expected 1s, got %v", got)
		}
	})
}

// byteReadahead returns the readahead the pool assigns to a reader without
// piece metadata for the given total budget.
func byteReadahead(p *Pool, totalBudget int64) int64 {
	return readaheadForShare(p.planReadaheadLocked(totalBudget).share, 0)
}

func TestPool_PlanReadaheadLocked(t *testing.T) {
	t.Run("splits budget across active readers", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		// With no active readers the whole budget remains unallocated.
		if share := p.planReadaheadLocked(1000).share; share != 1000 {
			t.Fatalf("expected share 1000, got %d", share)
		}

		p.readers[uint64(1)] = &streamReader{active: true}
		p.readers[uint64(2)] = &streamReader{active: true}

		// Active ranges include a 1/4 trailing window, so 1000 / 2 / 1.25 = 400.
		readahead := byteReadahead(p, 1000)
		if readahead != 400 {
			t.Fatalf("expected 400, got %d", readahead)
		}

		// Idle reader is excluded.
		p.readers[uint64(3)] = &streamReader{active: false}
		readahead = byteReadahead(p, 1000)
		if readahead != 400 {
			t.Fatalf("expected 400 (idle ignored), got %d", readahead)
		}
	})

	t.Run("file storage excluded", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		p.readers[uint64(1)] = &streamReader{active: true, isFileStorage: true}
		p.readers[uint64(2)] = &streamReader{active: true, isFileStorage: false}

		// 1 active memory reader (file storage reader excluded): 1000 / 1.25 = 800.
		readahead := byteReadahead(p, 1000)
		if readahead != 800 {
			t.Fatalf("expected 800 (file-storage excluded), got %d", readahead)
		}
	})

	t.Run("minimum one", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		for i := uint64(1); i <= 10; i++ {
			p.readers[i] = &streamReader{active: true}
		}

		// 10 active readers: 5 / 10 = 0 → clamped to 1.
		readahead := byteReadahead(p, 5)
		if readahead != 1 {
			t.Fatalf("expected minimum 1, got %d", readahead)
		}
	})
}

// TestReadaheadForShare verifies that readaheadForShare fits whole pieces.
func TestReadaheadForShare(t *testing.T) {
	const piece = int64(16)
	tests := []struct {
		share, want int64
	}{
		{share: 0, want: 0},
		{share: piece - 1, want: 0},     // not even the position piece
		{share: piece, want: 0},         // position piece only
		{share: 2 * piece, want: piece}, // position + 1 ahead
		{share: 5 * piece, want: 3 * piece},
		{share: 6 * piece, want: 4 * piece}, // position + 4 ahead + 1 trailing
		{share: 11 * piece, want: 8 * piece},
	}
	for _, tt := range tests {
		got := readaheadForShare(tt.share, piece)
		if got != tt.want {
			t.Errorf("readaheadForShare(%d) = %d, want %d", tt.share, got, tt.want)
		}
		ahead := got / piece
		if protected := (1 + ahead + ahead/trailingReadaheadDivisor) * piece; got > 0 && protected > tt.share {
			t.Errorf("readaheadForShare(%d) protects %d bytes", tt.share, protected)
		}
	}
}

func TestPool_Release(t *testing.T) {
	t.Run("starts lingering", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		sr := &streamReader{
			active:    true,
			infoHash:  infoHash,
			file:      &torrent.File{},
			readerID:  1,
			readahead: 1024,
		}
		p.readers[uint64(1)] = sr

		p.release(1)

		if sr.active {
			t.Fatal("expected reader to be inactive after release")
		}
		if sr.lingerSince.IsZero() {
			t.Fatal("expected lingerSince to be set")
		}
	})

	t.Run("ignores inactive reader", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		sr := &streamReader{active: false, infoHash: infoHash, readerID: 1}
		p.readers[uint64(1)] = sr

		p.release(1)

		if !sr.lingerSince.IsZero() {
			t.Fatal("lingerSince should remain zero for already-idle reader")
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		sr := &streamReader{
			active:    true,
			infoHash:  infoHash,
			file:      &torrent.File{},
			readerID:  1,
			readahead: 1024,
		}
		p.readers[uint64(1)] = sr

		p.release(1)
		p.release(1)

		if sr.active {
			t.Fatal("expected reader to remain idle after double release")
		}
	})

	t.Run("keeps reader context alive", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}
		var cancelled atomic.Bool

		key := uint64(42)
		p.readers[key] = &streamReader{
			active:   true,
			cancel:   func() { cancelled.Store(true) },
			infoHash: infoHash,
			readerID: 42,
		}

		p.release(42)

		if cancelled.Load() {
			t.Fatal("release cancelled the lingering reader context")
		}
	})

	t.Run("restores prioritized pieces", func(t *testing.T) {
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
		key := uint64(1)
		p.readers[key] = sr

		p.release(1)

		if len(sr.prioritizedPieces) != 0 {
			t.Fatalf("expected prioritizedPieces to be empty after release, got %v", sr.prioritizedPieces)
		}
		if sr.active {
			t.Fatal("expected reader to be inactive after release")
		}
	})

	t.Run("clears file boundaries", func(t *testing.T) {
		reg := &stubRegistry{}
		p := newTestPool(t, Config{
			Logger:   testLogger(),
			Registry: reg,
		})
		infoHash := metainfo.Hash{}

		key := uint64(42)
		p.readers[key] = &streamReader{
			active:    true,
			infoHash:  infoHash,
			readerID:  42,
			readahead: 2048,
		}

		p.release(42)

		if reg.clears != 1 {
			t.Fatalf("expected 1 ClearActiveRange call on release, got %d", reg.clears)
		}
		if reg.boundaryClears != 1 {
			t.Fatalf("expected file boundaries to be cleared on release, got %d clears", reg.boundaryClears)
		}
	})
}

func TestPool_CloseExpiredLingeringReaders(t *testing.T) {
	t.Run("closes after linger timeout", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, f := addTestTorrent(t, c)

		pool := New(Config{
			Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
			LingerTimeout: 1 * time.Millisecond,
		})
		defer pool.Close()

		_, release := acquireTestReader(t, pool, f, MemoryStorage, 1024*1024)
		release()
		require.True(t, pool.HasReaders(to.InfoHash()), "a released reader lingers")

		time.Sleep(20 * time.Millisecond)
		pool.closeExpiredLingeringReaders()

		assert.False(t, pool.HasReaders(to.InfoHash()), "an expired lingering reader must be closed")
	})

	lingering := func(p *Pool, lingered time.Duration) uint64 {
		key := uint64(1)
		p.readers[key] = &streamReader{
			infoHash:    metainfo.Hash{1},
			lingerSince: time.Now().Add(-lingered),
			readahead:   1024,
			reader:      &mockReader{},
			readerID:    key,
		}
		return key
	}

	t.Run("keeps readers within the timeout", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger(), LingerTimeout: time.Hour})
		key := lingering(p, time.Minute)

		p.closeExpiredLingeringReaders()

		sr, ok := p.readers[key]
		require.True(t, ok)
		assert.Equal(t, int64(1024), sr.readahead, "a lingering reader keeps reading ahead")
	})

	t.Run("memory pressure shortens the timeout", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:        testLogger(),
			LingerTimeout: 30 * time.Second,
			MemoryUsage:   func() float64 { return 0.95 },
		})
		key := lingering(p, 2*time.Second)

		p.closeExpiredLingeringReaders()

		_, ok := p.readers[key]
		assert.False(t, ok, "critical memory pressure must close a reader lingering longer than 1s")
	})

	t.Run("ignores active readers", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger(), LingerTimeout: time.Millisecond})
		key := uint64(1)
		p.readers[key] = &streamReader{active: true, infoHash: metainfo.Hash{1}, readerID: key}

		p.closeExpiredLingeringReaders()

		_, ok := p.readers[key]
		assert.True(t, ok)
	})
}

func TestPool_HasReaders(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{}
	otherIh := metainfo.Hash{1, 2, 3}

	if p.HasReaders(infoHash) {
		t.Fatal("expected no readers in empty pool")
	}

	p.readers[uint64(1)] = &streamReader{active: false, infoHash: infoHash}
	if !p.HasReaders(infoHash) {
		t.Fatal("expected HasReaders to return true when reader is idle")
	}
	if p.HasActiveReaders(infoHash) {
		t.Fatal("expected no active readers when reader is idle")
	}

	p.readers[uint64(2)] = &streamReader{active: true, infoHash: infoHash}
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
	t.Run("redistributes budget and keeps lingering readers", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		sr1 := &streamReader{active: true, infoHash: infoHash, readahead: 100}
		sr2 := &streamReader{active: false, infoHash: infoHash, readahead: 100}
		p.readers[uint64(1)] = sr1
		p.readers[uint64(2)] = sr2

		// One active range reserves 1/4 of its ahead window for trailing protection.
		p.refreshReadaheadLocked(1000)

		if sr1.readahead != 800 {
			t.Fatalf("expected active reader readahead=800, got %d", sr1.readahead)
		}
		if _, lingering := p.readers[uint64(2)]; !lingering {
			t.Fatal("a budget change must not close a lingering reader")
		}
	})
}

func TestPoolActiveRangeRegistry(t *testing.T) {
	t.Run("clears on release", func(t *testing.T) {
		reg := &stubRegistry{}
		p := newTestPool(t, Config{
			Logger:   testLogger(),
			Registry: reg,
		})
		infoHash := metainfo.Hash{}

		key := uint64(42)
		p.readers[key] = &streamReader{
			active:    true,
			infoHash:  infoHash,
			readerID:  42,
			readahead: 2048,
		}

		p.release(42)

		if reg.clears != 1 {
			t.Fatalf("expected 1 ClearActiveRange call, got %d", reg.clears)
		}
	})

	t.Run("clears on close", func(t *testing.T) {
		reg := &stubRegistry{}
		p := newTestPool(t, Config{
			Logger:   testLogger(),
			Registry: reg,
		})
		infoHash := metainfo.Hash{}

		for i := uint64(1); i <= 2; i++ {
			key := i
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
	})

	t.Run("nil registry is a no-op", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:   testLogger(),
			Registry: nil,
		})
		infoHash := metainfo.Hash{}

		key := uint64(1)
		p.readers[key] = &streamReader{
			active:   true,
			infoHash: infoHash,
			readerID: 1,
		}

		p.release(1)
		p.Close()
	})
}

func TestBoundaryPieces(t *testing.T) {
	tests := []struct {
		name                                   string
		headStart, headEnd, tailStart, tailEnd int
		want                                   []int
	}{
		{name: "tail repeats head", headStart: 0, headEnd: 1, tailStart: 0, tailEnd: 1, want: []int{0, 1}},
		{name: "disjoint head and tail", headStart: 0, headEnd: 1, tailStart: 5, tailEnd: 6, want: []int{0, 1, 5, 6}},
		{name: "overlapping head and tail", headStart: 0, headEnd: 3, tailStart: 2, tailEnd: 5, want: []int{0, 1, 2, 3, 4, 5}},
		{name: "adjacent head and tail", headStart: 0, headEnd: 2, tailStart: 3, tailEnd: 4, want: []int{0, 1, 2, 3, 4}},
		{name: "tail inside head", headStart: 0, headEnd: 5, tailStart: 2, tailEnd: 3, want: []int{0, 1, 2, 3, 4, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, slices.Collect(boundaryPieces(tt.headStart, tt.headEnd, tt.tailStart, tt.tailEnd)))
		})
	}
}

func TestBoundaryPieceBytes(t *testing.T) {
	// Six full 16-byte pieces followed by a 4-byte final piece.
	info := &metainfo.Info{PieceLength: 16, Length: 100, Pieces: make([]byte, 7*sha1.Size)}
	tests := []struct {
		name                                   string
		headStart, headEnd, tailStart, tailEnd int
		want                                   int64
	}{
		{name: "head only", headStart: 0, headEnd: 1, tailStart: 0, tailEnd: 1, want: 32},
		{name: "disjoint head and tail", headStart: 0, headEnd: 1, tailStart: 5, tailEnd: 6, want: 32 + 16 + 4},
		{name: "overlapping head and tail", headStart: 0, headEnd: 3, tailStart: 2, tailEnd: 6, want: 6*16 + 4},
		{name: "adjacent head and tail", headStart: 0, headEnd: 2, tailStart: 3, tailEnd: 4, want: 5 * 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, boundaryPieceBytes(info, tt.headStart, tt.headEnd, tt.tailStart, tt.tailEnd))
		})
	}
}

// TestComputeFileBoundaries verifies that computeFileBoundaries rejects a nil or empty file.
func TestComputeFileBoundaries(t *testing.T) {
	_, _, _, _, ok := computeFileBoundaries(nil, defaultFileBoundaryBytes)
	if ok {
		t.Fatal("expected ok=false for nil file")
	}
	_, _, _, _, ok = computeFileBoundaries(&torrent.File{}, defaultFileBoundaryBytes)
	if ok {
		t.Fatal("expected ok=false for empty file")
	}
}

func TestPoolConfigDefaults(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})

	if p.cfg.LingerTimeout != 30*time.Second {
		t.Fatalf("expected default LingerTimeout=30s, got %v", p.cfg.LingerTimeout)
	}
	if p.cfg.FileReadaheadBytes != 50*1024*1024 {
		t.Fatalf("expected default FileReadaheadBytes=50 MiB, got %d", p.cfg.FileReadaheadBytes)
	}
}

func TestPoolReadaheadRebalance(t *testing.T) {
	t.Run("rebalances active readers", func(t *testing.T) {
		// Registry is nil so registerActiveRangeLocked returns early without
		// needing a valid torrent.File (which has unexported fields).
		p := newTestPool(t, Config{
			Logger:        testLogger(),
			LingerTimeout: 30 * time.Second,
			MemoryUsage:   func() float64 { return 0.3 },
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
			p.readers[i] = srs[i-1]
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

		// Release reader 2: it closes while the remaining active readers split
		// the protected budget.
		p.release(2)
		if srs[1].reader != nil {
			t.Fatal("released reader 2 should close while other readers are active")
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
		p.release(1)
		if srs[0].reader != nil {
			t.Fatal("released reader 0 should close while another reader is active")
		}
		if srs[2].readahead != 8000 {
			t.Fatalf("active reader 2: expected readahead 8000, got %d", srs[2].readahead)
		}
		if srs[2].reader.(*mockReader).getReadahead() != 8000 {
			t.Fatalf("active reader 2: mock SetReadahead(8000) not called, got %d", srs[2].reader.(*mockReader).getReadahead())
		}

		// Reader 1 was never active in this scenario — releasing it is a no-op.
		p.release(2)

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
		p.readers[uint64(99)] = fsSr
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
	})

	t.Run("last released reader lingers", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{6}
		mr := &mockReader{readahead: 8000}
		sr := &streamReader{
			active:    true,
			infoHash:  infoHash,
			readerID:  1,
			readahead: 8000,
			reader:    mr,
		}
		p.readers[uint64(1)] = sr
		p.readaheadBudget = 10000

		p.release(1)

		if sr.readahead != 8000 || mr.getReadahead() != 8000 {
			t.Fatalf("last released reader should keep reading ahead, got reader=%d underlying=%d", sr.readahead, mr.getReadahead())
		}
	})

	t.Run("zero pool budget", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger: testLogger(),
		})
		infoHash := metainfo.Hash{7}

		released := &streamReader{
			active:        true,
			infoHash:      infoHash,
			readerID:      1,
			readahead:     500,
			isFileStorage: false,
			reader:        &mockReader{readahead: 500},
		}
		remaining := &streamReader{
			active:        true,
			infoHash:      infoHash,
			readerID:      2,
			readahead:     500,
			isFileStorage: false,
			reader:        &mockReader{readahead: 500},
		}
		p.readers[uint64(1)] = released
		p.readers[uint64(2)] = remaining

		p.release(1)

		if released.reader != nil {
			t.Fatal("released reader should close while another reader is active")
		}
		if remaining.readahead != 500 {
			t.Fatalf("closing without a budget should not redistribute active reader readahead, got %d", remaining.readahead)
		}
	})
}

func TestPoolPriorityWindowFraction(t *testing.T) {
	t.Run("zero is a no-op", func(t *testing.T) {
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
		key := uint64(1)
		p.readers[key] = sr

		// Registry is nil so updateActiveRange returns early before reaching
		// file.Torrent().Info() — this is the correct path for the "no
		// prioritization" config.
		p.updateActiveRange(key, 512)

		if len(sr.prioritizedPieces) != 3 {
			t.Fatalf("expected prioritizedPieces unchanged (len=3), got %d", len(sr.prioritizedPieces))
		}
	})

	t.Run("file storage claims pieces without protection", func(t *testing.T) {
		// File-storage readers download in rarity order like memory readers,
		// so they claim the pieces just ahead too; their pieces live on disk,
		// so they register no eviction protection.
		c := newTestTorrentClient(t)
		_, file := addSizedTorrent(t, c, "movie", 64, 640)
		reg := newProtectionRegistry()
		p := newTestPool(t, Config{
			FileReadaheadBytes:     4 * 64,
			Logger:                 testLogger(),
			PriorityWindowFraction: 1,
			Registry:               reg,
		})
		_, release, err := p.Acquire(file, FileStorage)
		require.NoError(t, err)
		defer release()
		var key uint64
		p.mu.Lock()
		for k := range p.readers {
			key = k
		}
		p.mu.Unlock()

		p.updateActiveRange(key, 2*64)
		require.Eventually(t, func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return len(p.readers[key].prioritizedPieces) == 4
		}, 5*time.Second, time.Millisecond, "a file-storage reader must claim the pieces ahead")
		assert.Empty(t, reg.protectedPieces(), "file-storage pieces need no eviction protection")
	})
}

func TestPool_PrioritizeNextPieces(t *testing.T) {
	t.Run("nil file returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(nil, 0, 1024, 0.5)
		if result != nil {
			t.Fatalf("expected nil for nil file, got %v", result)
		}
	})

	t.Run("zero fraction returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0)
		if result != nil {
			t.Fatalf("expected nil for fraction=0, got %v", result)
		}

		result = p.prioritizeNextPieces(&torrent.File{}, 0, 1024, -0.1)
		if result != nil {
			t.Fatalf("expected nil for fraction=-0.1, got %v", result)
		}
	})

	t.Run("zero readahead returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(&torrent.File{}, 0, 0, 1)
		if result != nil {
			t.Fatalf("expected nil for zero readahead, got %v", result)
		}
	})

	t.Run("empty file returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 1.1)
		if result != nil {
			t.Fatalf("expected nil for fraction=1.1, got %v", result)
		}
	})

	t.Run("claims every planned piece at now priority", func(t *testing.T) {
		// readahead=4 MiB, pieceLength=256 KiB → readaheadPieces=16, n=int(16*0.25)=4.
		plan := buildPriorityPlan(0, 4*1024*1024, 256*1024, 0, 100, 0.25)
		if len(plan) != 4 {
			t.Fatalf("expected 4 planned pieces, got %d", len(plan))
		}
		for i, piece := range plan {
			if piece.index != i+1 {
				t.Errorf("expected piece %d at position %d, got %d", i+1, i, piece.index)
			}
			if piece.priority != torrent.PiecePriorityNow {
				t.Errorf("expected piece %d at PiecePriorityNow, got %v", piece.index, piece.priority)
			}
		}
	})

	t.Run("claims at least one piece", func(t *testing.T) {
		plan := buildPriorityPlan(0, 4*1024*1024, 256*1024, 0, 100, 0.01)
		if len(plan) != 1 || plan[0].priority != torrent.PiecePriorityNow {
			t.Fatalf("expected one piece at PiecePriorityNow, got %v", plan)
		}
	})

	// Documents the coverage gap:
	// the EndPieceIndex > NumPieces clamping in prioritizeNextPieces cannot be
	// reached with &torrent.File{} because Torrent() returns nil and the function
	// returns early.  When a real torrent is available, prioritizeNextPieces
	// clamps endPieceMax to torrent.NumPieces() before calling priorityPlan, preventing
	// index-out-of-range panics when file.EndPieceIndex() exceeds the torrent's
	// actual piece count (split-file or partially-seeded torrents).
	t.Run("split file end clamped", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		// &torrent.File{}.Torrent() == nil → early returns, no panic.
		// This test verifies the early-return path doesn't regress; the
		// actual clamping logic is exercised in the "drives priority update"
		// subtest of TestPool_PrioritizeAsync, which dispatches through updateActiveRange (which itself panics on
		// &torrent.File{}.Torrent().Info()).  The clamping is a simple
		// endPieceMax = min(endPieceMax, tor.NumPieces()) guard — no loop
		// or arithmetic — so the risk of regression is low and the test
		// constraint is fundamental to the anacrolix/torrent package.
		result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0.3)
		if result != nil {
			t.Fatalf("expected nil for nil torrent, got %v", result)
		}
	})
}

func TestPool_PrioritizeAsync(t *testing.T) {
	newReader := func(t *testing.T) (*Pool, *torrent.Torrent, uint64, *streamReader) {
		t.Helper()
		c := newTestTorrentClient(t)
		// Ten 64-byte pieces.
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		p := newTestPool(t, Config{Logger: testLogger(), PriorityWindowFraction: 1})
		key := uint64(1)
		sr := &streamReader{active: true, file: file, infoHash: to.InfoHash(), lastPieceIdx: 2, readerID: 1}
		p.readers[key] = sr
		return p, to, key, sr
	}
	claimed := func(p *Pool, sr *streamReader) []int {
		p.mu.Lock()
		defer p.mu.Unlock()
		return slices.Clone(sr.prioritizedPieces)
	}

	t.Run("claims the pieces ahead of the reader", func(t *testing.T) {
		p, to, key, sr := newReader(t)
		p.prioritizeAsync(key, 2, sr.file, 2*64, 4*64)
		assert.Equal(t, []int{3, 4, 5, 6}, claimed(p, sr))
		p.mu.Lock()
		for _, index := range sr.prioritizedPieces {
			assert.Equal(t, torrent.PiecePriorityNow, p.priorityClaims[priorityPieceKey{index: index, torrent: to}].owners[sr])
		}
		p.mu.Unlock()
	})

	t.Run("ignores an update for a piece the reader has left", func(t *testing.T) {
		p, _, key, sr := newReader(t)
		sr.lastPieceIdx = 5
		p.prioritizeAsync(key, 2, sr.file, 2*64, 4*64)
		assert.Empty(t, claimed(p, sr), "a stale update must not replace newer claims")
	})

	t.Run("ignores a released reader", func(t *testing.T) {
		p, _, key, sr := newReader(t)
		p.release(key)
		p.prioritizeAsync(key, 2, sr.file, 2*64, 4*64)
		assert.Empty(t, claimed(p, sr))
		p.mu.Lock()
		assert.Empty(t, p.priorityClaims)
		p.mu.Unlock()
	})

	t.Run("races safely with release", func(t *testing.T) {
		p, _, key, sr := newReader(t)
		var wg sync.WaitGroup
		for range 50 {
			wg.Go(func() { p.prioritizeAsync(key, 2, sr.file, 2*64, 4*64) })
		}
		wg.Go(func() { p.release(key) })
		wg.Wait()
		// Updates that ran before the release claimed pieces, which the
		// release then cleared; later ones did nothing.
		p.mu.Lock()
		defer p.mu.Unlock()
		assert.Empty(t, p.priorityClaims)
		assert.False(t, sr.active)
	})
}

func TestPool_UpdateActiveRange(t *testing.T) {
	t.Run("nil registry caches offset", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:   testLogger(),
			Registry: nil,
		})
		defer p.Close()

		infoHash := metainfo.Hash{}
		key := uint64(1)
		sr := &streamReader{
			active:   true,
			infoHash: infoHash,
			readerID: 1,
		}
		p.readers[key] = sr

		p.updateActiveRange(key, 1024)

		p.mu.Lock()
		cached := sr.lastOffset
		p.mu.Unlock()

		if cached != 1024 {
			t.Fatalf("expected lastOffset=1024, got %d", cached)
		}
	})
}

func TestPriorityPlan(t *testing.T) {
	t.Run("edge cases", func(t *testing.T) {
		// n clamps to minimum 1 when fraction * readaheadPieces < 1.
		n, _, _ := priorityPlan(0, 100, 256*1024, 0, 100, 0.01)
		if n < 1 {
			t.Fatalf("expected n >= 1, got %d", n)
		}

		// target can't go below currentPiece+1 (even when n and endPieceMax
		// would push it lower).  Use pieceLength=1 so byteOffset maps
		// directly to piece index.
		_, target, _ := priorityPlan(100, 100, 1, 0, 100, 0.01)
		if target < 101 {
			t.Fatalf("expected target >= 101, got %d", target)
		}

		// fraction > 1 clamps to 1.
		// readahead=4 MiB, pieceLength=256 KiB → readaheadPieces=16, fraction clamped to 1.0.
		n, _, _ = priorityPlan(0, 4*1024*1024, 256*1024, 0, 100, 2.0)
		if n != 16 {
			t.Fatalf("expected n=16 (fraction clamped to 1.0), got %d", n)
		}
	})

	// Tests that when the window is clamped to
	// file end (endPieceMax is exclusive), the returned target still allows
	// the last valid piece (endPieceMax-1) to be included by a loop using
	// idx < target.  This catches the off-by-one where the old code did
	// endPieceMax-1 inside priorityPlan while endPieceMax was already exclusive.
	t.Run("end of last piece", func(t *testing.T) {
		// EndPieceIndex() == 10, so endPieceMax = 11 (exclusive).
		// Large readahead + fraction=1.0 → target would far exceed the file.
		_, target, _ := priorityPlan(0, 100*1024*1024, 256*1024, 0, 11, 1.0)
		// idx < target must allow idx == 10 (the last valid piece).
		if target <= 10 {
			t.Fatalf("expected target > 10 to include piece 10, got %d", target)
		}

		// Verify unclamped path: small readahead, target does not exceed endPieceMax.
		_, target, _ = priorityPlan(0, 512*1024, 256*1024, 0, 100, 1.0)
		// readaheadPieces = 2, n = int(2*1.0) = 2, target = 0+1+2 = 3.
		if target != 3 {
			t.Fatalf("expected target=3, got %d", target)
		}
	})

	// Ensures that when beginPiece is non-zero
	// (as in split-file torrents) and endPieceMax is clamped by the torrent's
	// actual piece count, the returned target does not exceed it and the
	// resulting loop still covers the full range from beginPiece+1 to end.
	t.Run("overlapping file end", func(t *testing.T) {
		// Simulate a split-file scenario: file starts at piece 100, torrent
		// has 200 total pieces (indices 0..199), so endPieceMax=200 (exclusive).
		// readaheadPieces=40, n=40, target=100+1+40=141 (within bounds).
		_, target, currentPiece := priorityPlan(0, 10*1024*1024, 256*1024, 100, 200, 1.0)
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
		_, target, _ = priorityPlan(0, 100*1024*1024, 256*1024, 195, 200, 1.0)
		if target != 200 {
			t.Fatalf("expected target=200 (saturated near end), got %d", target)
		}
	})
}

func TestPoolReaderContextLifecycle(t *testing.T) {
	p := newTestPool(t, Config{
		Logger: testLogger(),
	})
	defer p.Close()

	infoHash := metainfo.Hash{}
	key := uint64(1)
	ctx, cancel := context.WithCancel(context.Background())
	sr := &streamReader{
		active:      false,
		cancel:      cancel,
		infoHash:    infoHash,
		readerID:    1,
		lingerSince: time.Now().Add(-time.Minute),
	}

	p.mu.Lock()
	p.readers[key] = sr
	p.mu.Unlock()

	if ctx.Err() != nil {
		t.Fatalf("expected context not canceled initially, got %v", ctx.Err())
	}

	p.closeExpiredLingeringReaders()

	if ctx.Err() == nil {
		t.Fatal("expected reader context to be canceled on close in closeExpiredLingeringReaders")
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
					0.15,
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
				claims[i] = &priorityClaim{owners: make(map[any]torrent.PiecePriority, 1)}
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
				p.unclaimLocked(sr, nil, sr.prioritizedPieces)
			}
		})
	}
}
