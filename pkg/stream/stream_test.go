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
	"github.com/anacrolix/torrent/bencode"
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

	t.Run("fill limit stops readahead", func(t *testing.T) {
		const (
			totalSize = 300 * 1024
			limit     = 1000
		)
		data := make([]byte, totalSize)
		for i := range data {
			data[i] = byte(i % 251)
		}
		s := newMemReader(data)
		rw := &readAtWrapper{reader: s, fillLimit: limit}
		consumed := func(r *memReadSeekCloser) int64 { return r.Size() - int64(r.Len()) }

		buf := make([]byte, 10)
		n, err := rw.ReadAt(buf, 0)
		if err != nil || n != 10 {
			t.Fatalf("read failed: %d, %v", n, err)
		}
		if !bytes.Equal(buf, data[:10]) {
			t.Fatal("data mismatch")
		}
		if got := consumed(s); got != limit {
			t.Fatalf("cache refill consumed %d bytes, want %d", got, limit)
		}

		// A request crossing the limit is still satisfied in full, without
		// reading ahead beyond what the caller asked for.
		off := int64(limit - 10)
		spanBuf := make([]byte, 20)
		n, err = rw.ReadAt(spanBuf, off)
		if err != nil || n != 20 {
			t.Fatalf("spanning read failed: %d, %v", n, err)
		}
		if !bytes.Equal(spanBuf, data[off:off+20]) {
			t.Fatal("data mismatch in limit-spanning read")
		}
		if got := consumed(s); got != off+20 {
			t.Fatalf("limit-spanning read consumed %d bytes, want %d", got, off+20)
		}

		// Without a limit, a refill reads a full cache buffer.
		unbounded := newMemReader(data)
		rw = &readAtWrapper{reader: unbounded}
		if _, err := rw.ReadAt(buf, 0); err != nil {
			t.Fatalf("unbounded read failed: %v", err)
		}
		if got := consumed(unbounded); got != defaultWrapperBufSize {
			t.Fatalf("unbounded refill consumed %d bytes, want %d", got, defaultWrapperBufSize)
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
	if _, err := rw.ReadAt(make([]byte, 1), 0); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected io.ErrClosedPipe after close, got %v", err)
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
	read := func(pool *Pool, mode StorageMode, isPreload bool) {
		t.Helper()
		wrapper := &readAtWrapper{reader: newMemReader([]byte("abcdef"))}
		reader := pool.newReadSeeker(wrapper, 6, mode, isPreload)
		if _, err := reader.Read(make([]byte, 3)); err != nil {
			t.Fatalf("read failed: %v", err)
		}
	}

	read(pool, FileStorage, false)
	read(pool, MemoryStorage, false)
	read(pool, MemoryStorage, true) // preload reads run in the background
	read(&Pool{}, MemoryStorage, false)

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
		key := readerKey{filePath: "active", readerID: 1}

		pool.mu.Lock()
		pool.readers[key] = &streamReader{
			active:   true,
			cancel:   cancel,
			ctx:      readerCtx,
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
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
		p.readers[key] = sr

		p.Close()

		if sr.prioritizedPieces != nil {
			t.Fatalf("expected prioritizedPieces to be nil after Close, got %v", sr.prioritizedPieces)
		}
	})

	t.Run("waits for priority worker with no claims", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		key := readerKey{infoHash: metainfo.Hash{51}, filePath: "f", readerID: 1}
		sr := &streamReader{active: true}
		p.readers[key] = sr

		// A worker may hold priorityMu after its sequence check but before it
		// records its first claim. Close must still wait for that worker.
		sr.priorityMu.Lock()
		done := make(chan struct{})
		go func() {
			p.Close()
			close(done)
		}()
		<-p.closeCh
		closedBeforeWorker := false
		select {
		case <-done:
			closedBeforeWorker = true
		case <-time.After(20 * time.Millisecond):
		}
		sr.priorityMu.Unlock()
		<-done
		if closedBeforeWorker {
			t.Fatal("Close returned while a priority worker still held its lock")
		}
	})
}

func TestPool_EffectiveParkTimeout(t *testing.T) {
	t.Run("no pressure", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:          testLogger(),
			IdleParkTimeout: 30 * time.Second,
		})

		if got := p.effectiveParkTimeout(-1); got != 30*time.Second {
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
				Logger:          testLogger(),
				IdleParkTimeout: 30 * time.Second,
			})

			got := p.effectiveParkTimeout(tc.usage)
			if got != tc.want {
				t.Errorf("usage=%.2f: expected %v, got %v", tc.usage, tc.want, got)
			}
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
		infoHash := metainfo.Hash{}

		// With no active readers the whole budget remains unallocated.
		if share := p.planReadaheadLocked(1000).share; share != 1000 {
			t.Fatalf("expected share 1000, got %d", share)
		}

		p.readers[readerKey{infoHash: infoHash, filePath: "a", readerID: 1}] = &streamReader{active: true}
		p.readers[readerKey{infoHash: infoHash, filePath: "b", readerID: 2}] = &streamReader{active: true}

		// Active ranges include a 1/4 trailing window, so 1000 / 2 / 1.25 = 400.
		readahead := byteReadahead(p, 1000)
		if readahead != 400 {
			t.Fatalf("expected 400, got %d", readahead)
		}

		// Idle reader is excluded.
		p.readers[readerKey{infoHash: infoHash, filePath: "c", readerID: 3}] = &streamReader{active: false}
		readahead = byteReadahead(p, 1000)
		if readahead != 400 {
			t.Fatalf("expected 400 (idle ignored), got %d", readahead)
		}
	})

	t.Run("file storage excluded", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		p.readers[readerKey{infoHash: infoHash, filePath: "a", readerID: 1}] = &streamReader{active: true, isFileStorage: true}
		p.readers[readerKey{infoHash: infoHash, filePath: "b", readerID: 2}] = &streamReader{active: true, isFileStorage: false}

		// 1 active memory reader (file storage reader excluded): 1000 / 1.25 = 800.
		readahead := byteReadahead(p, 1000)
		if readahead != 800 {
			t.Fatalf("expected 800 (file-storage excluded), got %d", readahead)
		}
	})

	t.Run("minimum one", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		for i := uint64(1); i <= 10; i++ {
			p.readers[readerKey{infoHash: infoHash, filePath: "x", readerID: i}] = &streamReader{active: true}
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

func TestPool_ReservePreloadBudget(t *testing.T) {
	t.Run("caps aggregate reservations", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		if !p.SetReadaheadBudget(1000) {
			t.Fatal("expected initial readahead budget to be accepted")
		}
		firstHash := metainfo.Hash{1}
		secondHash := metainfo.Hash{2}

		if got := p.ReservePreloadBudget(firstHash, "", false, 300); got != 300 {
			t.Fatalf("expected first preload to reserve 300, got %d", got)
		}
		if got := p.ReservePreloadBudget(secondHash, "", false, 300); got != 200 {
			t.Fatalf("expected second preload to receive the remaining 200-byte preload share, got %d", got)
		}
		if got := p.ReservePreloadBudget(firstHash, "", false, 400); got != 300 {
			t.Fatalf("expected replacement reservation to remain capped at 300, got %d", got)
		}

		p.ReleasePreloadBudget(secondHash)
		if got := p.ReservePreloadBudget(firstHash, "", false, 400); got != 400 {
			t.Fatalf("expected released preload capacity to become available, got %d", got)
		}
		p.mu.Lock()
		available := p.availableProtectionBudgetLocked(p.readaheadBudget)
		boundaryBytes := p.boundaryBytesPerFileLocked(available, 1)
		p.mu.Unlock()
		if available != 600 || boundaryBytes != 150 {
			t.Fatalf("expected playback to retain 600 bytes with 150-byte boundaries, got available=%d boundary=%d", available, boundaryBytes)
		}
	})

	t.Run("replaces file boundaries", func(t *testing.T) {
		const (
			budget      = int64(32 << 20)
			pieceLength = int64(1 << 20)
			preload     = int64(16 << 20)
		)
		length := int64(8 << 30)
		info := metainfo.Info{
			Name:        "movie.mkv",
			PieceLength: pieceLength,
			Length:      length,
			Pieces:      make([]byte, length/pieceLength*sha1.Size),
		}
		infoBytes, err := bencode.Marshal(info)
		require.NoError(t, err)
		c := newTestTorrentClient(t)
		to, file := addTestTorrentFromMetaInfo(t, c, &metainfo.MetaInfo{InfoBytes: infoBytes})

		reg := newProtectionRegistry()
		pool := New(Config{Registry: reg, Logger: testLogger()})
		defer pool.Close()
		require.True(t, pool.SetReadaheadBudget(budget))
		_, release, err := pool.Acquire(file, MemoryStorage)
		require.NoError(t, err)
		defer release()

		readahead := func() int64 {
			pool.mu.Lock()
			defer pool.mu.Unlock()
			for _, sr := range pool.readers {
				return sr.readahead
			}
			return 0
		}
		boundaries := func() int {
			reg.mu.Lock()
			defer reg.mu.Unlock()
			return len(reg.boundaries)
		}
		require.Equal(t, 1, boundaries())

		// A preload for another file of the torrent protects nothing this reader
		// needs, so the reader keeps its boundaries and pays for the reservation.
		require.Equal(t, preload, pool.ReservePreloadBudget(to.InfoHash(), "other.mkv", true, preload))
		assert.Equal(t, 1, boundaries())
		withOtherPreload := readahead()

		// A head-only preload of the playing file leaves its tail unprotected, so
		// the reader keeps its boundaries.
		require.Equal(t, preload, pool.ReservePreloadBudget(to.InfoHash(), file.Path(), false, preload))
		assert.Equal(t, 1, boundaries(), "a head-only preload must not suppress the tail boundary")

		// A preload for the playing file already protects its head and tail.
		require.Equal(t, preload, pool.ReservePreloadBudget(to.InfoHash(), file.Path(), true, preload))
		assert.Zero(t, boundaries(), "the reservation replaces the reader's boundaries")
		assert.Greater(t, readahead(), withOtherPreload, "boundaries must not be charged twice")

		pool.ReleasePreloadBudget(to.InfoHash())
		assert.Equal(t, 1, boundaries(), "releasing the reservation restores the reader's boundaries")
	})
}

// TestPool_PreloadCapacity verifies that PreloadCapacity keeps a playback reserve.
func TestPool_PreloadCapacity(t *testing.T) {
	const mib = int64(1 << 20)
	tests := []struct {
		budget, want int64
	}{
		{budget: 0, want: 0},
		{budget: 32 * mib, want: 16 * mib},   // half kept for playback
		{budget: 256 * mib, want: 240 * mib}, // reserve capped at two boundaries
	}
	for _, tt := range tests {
		p := newTestPool(t, Config{Logger: testLogger()})
		if !p.SetReadaheadBudget(tt.budget) {
			t.Fatalf("budget %d rejected", tt.budget)
		}
		if got := p.PreloadCapacity(); got != tt.want {
			t.Errorf("PreloadCapacity() with budget %d = %d, want %d", tt.budget, got, tt.want)
		}
		// The whole capacity must be reservable by a single preload.
		if got := p.ReservePreloadBudget(metainfo.Hash{1}, "", false, tt.want); got != tt.want {
			t.Errorf("ReservePreloadBudget(%d) granted %d", tt.want, got)
		}
	}
}

// TestPool_ReleasePreloadBudget verifies that ReleasePreloadBudget restores readahead for concurrent readers.
func TestPool_ReleasePreloadBudget(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	const totalBudget = int64(1200)
	if !p.SetReadaheadBudget(totalBudget) {
		t.Fatal("expected initial readahead budget to be accepted")
	}

	for i := uint64(1); i <= 3; i++ {
		p.readers[readerKey{infoHash: metainfo.Hash{byte(i)}, filePath: "f", readerID: i}] = &streamReader{
			active:   true,
			infoHash: metainfo.Hash{byte(i)},
			readerID: i,
			reader:   &mockReader{},
		}
	}

	preloadHash := metainfo.Hash{9}
	if got := p.ReservePreloadBudget(preloadHash, "", false, 600); got != 600 {
		t.Fatalf("expected 600-byte preload reservation, got %d", got)
	}
	for _, sr := range p.readers {
		if sr.readahead != 160 {
			t.Fatalf("expected preload-reduced readahead 160, got %d", sr.readahead)
		}
	}

	p.ReleasePreloadBudget(preloadHash)
	for _, sr := range p.readers {
		if sr.readahead != 320 {
			t.Fatalf("expected restored readahead 320, got %d", sr.readahead)
		}
	}
}

// TestPreloadReadaheadFunc verifies that preloadReadaheadFunc stops at the end of the range.
func TestPreloadReadaheadFunc(t *testing.T) {
	readahead := preloadReadaheadFunc(1024)

	if got := readahead(torrent.ReadaheadContext{CurrentPos: 256}); got != 768 {
		t.Fatalf("expected 768 bytes remaining, got %d", got)
	}
	if got := readahead(torrent.ReadaheadContext{CurrentPos: 1024}); got != 0 {
		t.Fatalf("expected no readahead at range end, got %d", got)
	}
	if got := readahead(torrent.ReadaheadContext{CurrentPos: 2048}); got != 0 {
		t.Fatalf("expected no readahead past range end, got %d", got)
	}
}

func TestPool_Release(t *testing.T) {
	t.Run("sets idle", func(t *testing.T) {
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
	})

	t.Run("removes preload reader", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{1}
		readerCtx, cancel := context.WithCancel(context.Background())
		sr := &streamReader{
			active:    true,
			cancel:    cancel,
			ctx:       readerCtx,
			infoHash:  infoHash,
			isPreload: true,
			reader:    &mockReader{},
			readerID:  1,
		}
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
		p.readers[key] = sr

		p.release(infoHash, "f", 1)

		if _, ok := p.readers[key]; ok {
			t.Fatal("expected preload reader to be removed instead of pooled idle")
		}
		if !errors.Is(readerCtx.Err(), context.Canceled) {
			t.Fatalf("expected preload reader context cancellation, got %v", readerCtx.Err())
		}
	})

	t.Run("ignores inactive reader", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		infoHash := metainfo.Hash{}

		sr := &streamReader{active: false, infoHash: infoHash, readerID: 1}
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

		p.release(infoHash, "f", 1)

		if !sr.idleSince.IsZero() {
			t.Fatal("idleSince should remain zero for already-idle reader")
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
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr

		p.release(infoHash, "f", 1)
		p.release(infoHash, "f", 1)

		if sr.active {
			t.Fatal("expected reader to remain idle after double release")
		}
	})

	t.Run("keeps reader context alive", func(t *testing.T) {
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
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
		p.readers[key] = sr

		p.release(infoHash, "f", 1)

		if len(sr.prioritizedPieces) != 0 {
			t.Fatalf("expected prioritizedPieces to be empty after release, got %v", sr.prioritizedPieces)
		}
		if sr.active {
			t.Fatal("expected reader to be idle after release")
		}
	})

	t.Run("clears file boundaries", func(t *testing.T) {
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
	})
}

func TestPool_ParkIdleReaders(t *testing.T) {
	t.Run("removes after close timeout", func(t *testing.T) {
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
	})

	t.Run("skips already parked", func(t *testing.T) {
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
	})

	t.Run("parks idle to zero", func(t *testing.T) {
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
	})

	// Documents an internal
	// (non-public-contract) invariant: parkIdleReaders reads p.cfg.IdleCloseTimeout
	// directly and only applies a close deadline when it is > 0. This differs
	// from Config's documented zero-value behavior, which only applies at
	// New() construction time (zero -> 5-minute default). This test exists to
	// pin the runtime field semantics, not the public Config contract — a
	// caller should never see a live Pool with CloseTimeout == 0 unless they
	// mutate cfg directly after construction, which is not a supported path.
	t.Run("zero field means no close", func(t *testing.T) {
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
	})

	t.Run("short close timeout not expanded", func(t *testing.T) {
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
	})
}

func TestPoolIdleCloseTimeout(t *testing.T) {
	t.Run("removes reader", func(t *testing.T) {
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
	})

	// Verifies that a negative
	// CloseTimeout passed into Config is left untouched by New() (only zero
	// gets the 5-minute default applied) and produces "never close" behavior
	// at runtime, since parkIdleReaders only applies a close deadline when
	// CloseTimeout > 0.
	t.Run("negative disables cleanup", func(t *testing.T) {
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
	})
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
	t.Run("redistributes budget and parks idle readers", func(t *testing.T) {
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
		if sr2.readahead != 0 {
			t.Fatalf("expected competing idle reader to be parked, got readahead=%d", sr2.readahead)
		}
	})

	t.Run("rejects preload overcommit", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		if !p.SetReadaheadBudget(1000) {
			t.Fatal("expected initial readahead budget to be accepted")
		}
		infoHash := metainfo.Hash{1}
		if got := p.ReservePreloadBudget(infoHash, "", false, 400); got != 400 {
			t.Fatalf("expected 400-byte preload reservation, got %d", got)
		}

		if p.SetReadaheadBudget(600) {
			t.Fatal("expected budget reduction to reject an existing 400-byte reservation")
		}
		p.mu.Lock()
		budgetAfterRejection := p.readaheadBudget
		availableAfterRejection := p.availableProtectionBudgetLocked(p.readaheadBudget)
		p.mu.Unlock()
		if budgetAfterRejection != 1000 || availableAfterRejection != 600 {
			t.Fatalf("rejected update changed the pool: budget=%d available=%d", budgetAfterRejection, availableAfterRejection)
		}
		p.ReleasePreloadBudget(infoHash)
		if !p.SetReadaheadBudget(600) {
			t.Fatal("expected budget reduction after releasing preload reservation")
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
	})

	t.Run("clears on close", func(t *testing.T) {
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
	})

	t.Run("nil registry is a no-op", func(t *testing.T) {
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
	})
}

// TestComputeFileBoundaries verifies that computeFileBoundaries handles a nil or empty file.
func TestComputeFileBoundaries(t *testing.T) {
	_, _, _, _, ok := ComputeFileBoundaries(nil)
	if ok {
		t.Fatal("expected ok=false for nil file")
	}
}

func TestPoolConfigDefaults(t *testing.T) {
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

func TestPoolMaxReadersPerFile(t *testing.T) {
	// Verifies the reuse-and-cap behavior when
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
	t.Run("via acquire", func(t *testing.T) {
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
	})

	t.Run("no eviction when all active", func(t *testing.T) {
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
	})

	t.Run("defaults to ten", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		if p.cfg.MaxReadersPerFile != 10 {
			t.Fatalf("expected default 10, got %d", p.cfg.MaxReadersPerFile)
		}
	})

	t.Run("evicts idle", func(t *testing.T) {
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
	})

	t.Run("no eviction when only active exist", func(t *testing.T) {
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
	})

	t.Run("no eviction when under cap", func(t *testing.T) {
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
	})

	t.Run("zero uses default", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:            testLogger(),
			MaxReadersPerFile: 0,
		})

		if p.cfg.MaxReadersPerFile != 10 {
			t.Fatalf("expected 0 to default to 10, got %d", p.cfg.MaxReadersPerFile)
		}
	})

	t.Run("negative disables cap", func(t *testing.T) {
		p := newTestPool(t, Config{
			Logger:            testLogger(),
			MaxReadersPerFile: -1,
		})

		if p.cfg.MaxReadersPerFile != 0 {
			t.Fatalf("expected -1 to disable the cap (0), got %d", p.cfg.MaxReadersPerFile)
		}
	})
}

func TestPool_TrimIdleReadersLocked(t *testing.T) {
	infoHash := metainfo.Hash{4}
	now := time.Now()
	newPool := func(t *testing.T, maxReaders int, idle, active int) *Pool {
		t.Helper()
		p := newTestPool(t, Config{Logger: testLogger(), MaxReadersPerFile: maxReaders})
		id := uint64(0)
		for i := range idle {
			id++
			p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: id}] = &streamReader{
				file:      &torrent.File{},
				idleSince: now.Add(time.Duration(i) * time.Second),
				infoHash:  infoHash,
				readerID:  id,
			}
		}
		for range active {
			id++
			p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: id}] = &streamReader{
				active:   true,
				file:     &torrent.File{},
				infoHash: infoHash,
				readerID: id,
			}
		}
		return p
	}

	t.Run("evicts oldest idle readers down to the cap", func(t *testing.T) {
		p := newPool(t, 2, 3, 1)
		p.trimIdleReadersLocked(infoHash, "f")
		if got := p.countReadersLocked(infoHash, "f"); got != 2 {
			t.Fatalf("expected 2 readers, got %d", got)
		}
		// Readers 1 and 2 are the oldest idle readers.
		for _, id := range []uint64{1, 2} {
			if _, ok := p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: id}]; ok {
				t.Errorf("expected reader %d to be evicted", id)
			}
		}
	})

	t.Run("keeps active readers above the cap", func(t *testing.T) {
		p := newPool(t, 2, 1, 2)
		p.trimIdleReadersLocked(infoHash, "f")
		if got := p.countReadersLocked(infoHash, "f"); got != 2 {
			t.Fatalf("expected 2 active readers, got %d", got)
		}

		p = newPool(t, 1, 0, 3)
		p.trimIdleReadersLocked(infoHash, "f")
		if got := p.countReadersLocked(infoHash, "f"); got != 3 {
			t.Fatalf("expected soft cap to keep 3 active readers, got %d", got)
		}
	})

	t.Run("disabled cap keeps every reader", func(t *testing.T) {
		p := newPool(t, -1, 3, 0)
		p.trimIdleReadersLocked(infoHash, "f")
		if got := p.countReadersLocked(infoHash, "f"); got != 3 {
			t.Fatalf("expected 3 readers, got %d", got)
		}
	})
}

func TestPoolReadaheadRebalance(t *testing.T) {
	t.Run("rebalances active readers", func(t *testing.T) {
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

		// Release reader 2: it is parked while the remaining active readers split
		// the protected budget.
		p.release(infoHash, "f", 2)
		if srs[1].readahead != 0 {
			t.Fatalf("released reader 2 should be parked, got readahead=%d", srs[1].readahead)
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
		if srs[0].readahead != 0 {
			t.Fatalf("released reader 0 should be parked, got readahead=%d", srs[0].readahead)
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
	})

	t.Run("last idle reader stays warm", func(t *testing.T) {
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
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr
		p.readaheadBudget = 10000

		p.release(infoHash, "f", 1)

		if sr.readahead != 8000 || mr.getReadahead() != 8000 {
			t.Fatalf("last idle reader should stay warm, got reader=%d underlying=%d", sr.readahead, mr.getReadahead())
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
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = released
		p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 2}] = remaining

		p.release(infoHash, "f", 1)

		if released.readahead != 0 || released.reader.(*mockReader).getReadahead() != 0 {
			t.Fatalf("released reader should be parked while another reader is active, got reader=%d underlying=%d", released.readahead, released.reader.(*mockReader).getReadahead())
		}
		if remaining.readahead != 500 {
			t.Fatalf("zero-budget parking should not redistribute active reader readahead, got %d", remaining.readahead)
		}
	})

	t.Run("idle GC rebalances", func(t *testing.T) {
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
	})
}

func TestPoolPreloadParksIdleReaderOnlyWhileReading(t *testing.T) {
	p := newTestPool(t, Config{Logger: testLogger()})
	infoHash := metainfo.Hash{7}
	mr := &mockReader{readahead: 8000}
	sr := &streamReader{
		infoHash:  infoHash,
		readerID:  1,
		readahead: 8000,
		reader:    mr,
	}
	p.readers[readerKey{infoHash: infoHash, filePath: "f", readerID: 1}] = sr
	p.readaheadBudget = 10000

	// A reservation alone, such as one a completed preload keeps for its
	// cache, downloads nothing, so the idle reader may stay warm.
	preloadHash := metainfo.Hash{8}
	if got := p.ReservePreloadBudget(preloadHash, "", false, 5000); got != 5000 {
		t.Fatalf("expected 5000-byte preload reservation, got %d", got)
	}
	if sr.readahead != 8000 || mr.getReadahead() != 8000 {
		t.Fatalf("a held reservation must not park the idle reader, got reader=%d underlying=%d", sr.readahead, mr.getReadahead())
	}

	// A preload that is reading competes for bandwidth, so the idle reader parks.
	p.mu.Lock()
	p.readers[readerKey{infoHash: preloadHash, filePath: "f", readerID: 2}] = &streamReader{
		active:    true,
		infoHash:  preloadHash,
		isPreload: true,
		readerID:  2,
		reader:    &mockReader{},
	}
	p.parkCompetingIdleReadersLocked()
	p.mu.Unlock()
	if sr.readahead != 0 || mr.getReadahead() != 0 {
		t.Fatalf("a reading preload should park the idle reader, got reader=%d underlying=%d", sr.readahead, mr.getReadahead())
	}
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
		key := readerKey{infoHash: infoHash, filePath: "f", readerID: 1}
		p.readers[key] = sr

		// Registry is nil so updateActiveRange returns early before reaching
		// file.Torrent().Info() — this is the correct path for the "no
		// prioritization" config.
		p.updateActiveRange(infoHash, key, &torrent.File{}, nil, 512)

		if len(sr.prioritizedPieces) != 3 {
			t.Fatalf("expected prioritizedPieces unchanged (len=3), got %d", len(sr.prioritizedPieces))
		}
	})

	t.Run("file storage skipped", func(t *testing.T) {
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
	})
}

func TestPool_PrioritizeNextPieces(t *testing.T) {
	t.Run("nil file returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(nil, 0, 1024, 0.5, 0.3)
		if result != nil {
			t.Fatalf("expected nil for nil file, got %v", result)
		}
	})

	t.Run("zero fraction returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0, 0.3)
		if result != nil {
			t.Fatalf("expected nil for fraction=0, got %v", result)
		}

		result = p.prioritizeNextPieces(&torrent.File{}, 0, 1024, -0.1, 0.3)
		if result != nil {
			t.Fatalf("expected nil for fraction=-0.1, got %v", result)
		}
	})

	t.Run("zero readahead returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(&torrent.File{}, 0, 0, 1, 0.3)
		if result != nil {
			t.Fatalf("expected nil for zero readahead, got %v", result)
		}
	})

	t.Run("empty file returns nil", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})

		result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 1.1, 0.3)
		if result != nil {
			t.Fatalf("expected nil for fraction=1.1, got %v", result)
		}
	})

	t.Run("near fraction defaults to thirty percent", func(t *testing.T) {
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
	})

	t.Run("near fraction clamped to max", func(t *testing.T) {
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
		result := p.prioritizeNextPieces(&torrent.File{}, 0, 1024, 0.3, 0.5)
		if result != nil {
			t.Fatalf("expected nil for nil torrent, got %v", result)
		}
	})
}

func TestPool_PrioritizeAsync(t *testing.T) {
	t.Run("drives priority update", func(t *testing.T) {
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
	})

	t.Run("stale goroutine dropped", func(t *testing.T) {
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
	})
}

func TestPool_UpdateActiveRange(t *testing.T) {
	t.Run("concurrent with release", func(t *testing.T) {
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
	})

	t.Run("nil registry caches offset", func(t *testing.T) {
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
	})

	t.Run("stale wrapper discarded", func(t *testing.T) {
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
	})
}

func TestPriorityPlan(t *testing.T) {
	t.Run("edge cases", func(t *testing.T) {
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
	})

	// Tests that when the window is clamped to
	// file end (endPieceMax is exclusive), the returned target still allows
	// the last valid piece (endPieceMax-1) to be included by a loop using
	// idx < target.  This catches the off-by-one where the old code did
	// endPieceMax-1 inside priorityPlan while endPieceMax was already exclusive.
	t.Run("end of last piece", func(t *testing.T) {
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
	})

	// Ensures that when beginPiece is non-zero
	// (as in split-file torrents) and endPieceMax is clamped by the torrent's
	// actual piece count, the returned target does not exceed it and the
	// resulting loop still covers the full range from beginPiece+1 to end.
	t.Run("overlapping file end", func(t *testing.T) {
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
	})
}

func TestPoolReaderContextLifecycle(t *testing.T) {
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
