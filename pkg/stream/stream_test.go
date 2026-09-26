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

// recordingReader is an in-memory io.ReadSeeker that records the reads and
// seeks reaching it.
type recordingReader struct {
	*bytes.Reader
	events *[]string
}

func newRecordingReader(data []byte, events *[]string) *recordingReader {
	return &recordingReader{Reader: bytes.NewReader(data), events: events}
}

func (r *recordingReader) Read(p []byte) (int, error) {
	*r.events = append(*r.events, "read")
	return r.Reader.Read(p)
}

func (r *recordingReader) Seek(offset int64, whence int) (int64, error) {
	*r.events = append(*r.events, fmt.Sprintf("seek %d", offset))
	return r.Reader.Seek(offset, whence)
}

func TestStreamReadSeeker_Read(t *testing.T) {
	newStream := func(data []byte, length int64) (*streamReadSeeker, *[]string) {
		events := &[]string{}
		return newStreamReadSeeker(newRecordingReader(data, events), length, func(position int64) {
			*events = append(*events, fmt.Sprintf("report %d", position))
		}), events
	}

	t.Run("reads sequentially and reports each position", func(t *testing.T) {
		s, events := newStream([]byte("hello world"), 11)
		buf := make([]byte, 5)
		n, err := s.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(buf[:n]))
		n, err = s.Read(make([]byte, 16))
		require.NoError(t, err)
		assert.Equal(t, 6, n)
		_, err = s.Read(buf)
		require.ErrorIs(t, err, io.EOF)
		assert.Equal(t, []string{"read", "report 5", "report 11"}, *events, "one buffered read serves both")
	})

	t.Run("ends at the file length", func(t *testing.T) {
		s, _ := newStream([]byte("abcdef"), 4)
		data, err := io.ReadAll(s)
		require.NoError(t, err)
		assert.Equal(t, "abcd", string(data))
	})

	t.Run("reports a seek before the read", func(t *testing.T) {
		s, events := newStream([]byte("abcdef"), 6)
		// The size probe used by http.ServeContent must not reach the reader.
		end, err := s.Seek(0, io.SeekEnd)
		require.NoError(t, err)
		assert.Equal(t, int64(6), end)
		position, err := s.Seek(4, io.SeekStart)
		require.NoError(t, err)
		assert.Equal(t, int64(4), position)
		assert.Empty(t, *events, "a seek alone must not move or report the reader")

		buf := make([]byte, 2)
		n, err := s.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "ef", string(buf[:n]))
		assert.Equal(t, []string{"report 4", "seek 4", "read", "report 6"}, *events)
	})

	t.Run("keeps the buffer without a move", func(t *testing.T) {
		s, events := newStream([]byte("abcdef"), 6)
		_, err := s.Read(make([]byte, 2))
		require.NoError(t, err)
		position, err := s.Seek(0, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, int64(2), position)
		buf := make([]byte, 2)
		_, err = s.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "cd", string(buf))
		assert.Equal(t, []string{"read", "report 2", "report 2", "report 4"}, *events)
	})

	t.Run("seeking back rereads", func(t *testing.T) {
		s, _ := newStream([]byte("abcdef"), 6)
		_, err := s.Read(make([]byte, 4))
		require.NoError(t, err)
		_, err = s.Seek(-3, io.SeekCurrent)
		require.NoError(t, err)
		buf := make([]byte, 2)
		_, err = s.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "bc", string(buf))
	})

	t.Run("reads the end past the length", func(t *testing.T) {
		s, _ := newStream([]byte("abcdef"), 6)
		position, err := s.Seek(10, io.SeekStart)
		require.NoError(t, err)
		assert.Equal(t, int64(10), position)
		_, err = s.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("rejects invalid seeks", func(t *testing.T) {
		s, _ := newStream([]byte("abcdef"), 6)
		_, err := s.Seek(-1, io.SeekStart)
		require.Error(t, err)
		_, err = s.Seek(0, 42)
		require.Error(t, err)
	})

	t.Run("reports without its lock held", func(t *testing.T) {
		var s *streamReadSeeker
		reports := 0
		s = newStreamReadSeeker(bytes.NewReader([]byte("abcd")), 4, func(int64) {
			// Reporting takes pool.mu, which must never be acquired under
			// the stream's lock.
			require.True(t, s.mu.TryLock(), "the stream lock is held while reporting")
			s.mu.Unlock()
			reports++
		})
		_, err := s.Seek(1, io.SeekStart)
		require.NoError(t, err)
		_, err = s.Read(make([]byte, 2))
		require.NoError(t, err)
		assert.Equal(t, 2, reports)
	})

	t.Run("observes read durations", func(t *testing.T) {
		s := newStreamReadSeeker(bytes.NewReader([]byte("abcd")), 4, nil)
		var durations []time.Duration
		s.observeRead = func(d time.Duration) { durations = append(durations, d) }
		_, err := s.Read(make([]byte, 2))
		require.NoError(t, err)
		require.Len(t, durations, 1)
		assert.GreaterOrEqual(t, durations[0], time.Duration(0))
	})
}

func TestStreamReadSeeker_Retire(t *testing.T) {
	var events []string
	reports := 0
	s := newStreamReadSeeker(newRecordingReader([]byte("xy"), &events), 2, func(int64) { reports++ })
	_, err := s.Read(make([]byte, 1))
	require.NoError(t, err)

	s.retire()
	s.retire()
	assert.Nil(t, s.buf, "retiring must free the buffer")
	_, err = s.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	_, err = s.Seek(0, io.SeekStart)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	assert.Equal(t, 1, reports, "a retired stream must not report")
	assert.Equal(t, []string{"read"}, events, "a retired stream must not touch its reader")
}

func TestPool_Close(t *testing.T) {
	t.Run("serializes with active read", func(t *testing.T) {
		pool := New(Config{Logger: testLogger()})
		reader := newBlockingReader()
		readerCtx, cancel := context.WithCancel(context.Background())
		reader.SetContext(readerCtx)
		stream := newStreamReadSeeker(reader, 1, nil)
		key := uint64(1)

		pool.mu.Lock()
		pool.readers[key] = &streamReader{
			active:   true,
			cancel:   cancel,
			reader:   reader,
			readerID: 1,
			stream:   stream,
		}
		pool.mu.Unlock()

		readDone := make(chan error, 1)
		go func() {
			_, err := stream.Read(make([]byte, 1))
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
		_, release, err := p.Acquire(context.Background(), file, FileStorage)
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

func TestPrioritizeNextPieces(t *testing.T) {
	const pieceLength = 256 << 10
	c := newTestTorrentClient(t)
	_, file := addSizedTorrent(t, c, "priorities", pieceLength, 100*pieceLength)
	_, misaligned := addTestTorrentFromMetaInfo(t, c, createMisalignedFileTestMetaInfo(t))
	misaligned = misaligned.Torrent().Files()[1]

	pieces := func(planned []prioritizedPiece) []int {
		indexes := make([]int, 0, len(planned))
		for _, piece := range planned {
			assert.Equal(t, torrent.PiecePriorityNow, piece.priority)
			indexes = append(indexes, piece.index)
		}
		return indexes
	}

	tests := []struct {
		name       string
		file       *torrent.File
		byteOffset int64
		readahead  int64
		fraction   float64
		want       []int
	}{
		{name: "fraction of the readahead pieces", file: file, readahead: 16 * pieceLength, fraction: 0.25, want: []int{1, 2, 3, 4}},
		{name: "at least one piece", file: file, readahead: 16 * pieceLength, fraction: 0.01, want: []int{1}},
		{name: "readahead below one piece", file: file, readahead: 100, fraction: 1, want: []int{1}},
		{name: "fraction above one clamps", file: file, readahead: 2 * pieceLength, fraction: 2, want: []int{1, 2}},
		{name: "starts past the current piece", file: file, byteOffset: 10*pieceLength + 1, readahead: 2 * pieceLength, fraction: 1, want: []int{11, 12}},
		{name: "clamps to the last piece", file: file, byteOffset: 97 * pieceLength, readahead: 100 * pieceLength, fraction: 1, want: []int{98, 99}},
		{name: "none past the last piece", file: file, byteOffset: 99 * pieceLength, readahead: 100 * pieceLength, fraction: 1, want: []int{}},
		{name: "counts the file offset", file: misaligned, byteOffset: 32, readahead: 128, fraction: 1, want: []int{2, 3}},
		{name: "no fraction", file: file, readahead: 16 * pieceLength},
		{name: "negative fraction", file: file, readahead: 16 * pieceLength, fraction: -0.1},
		{name: "no readahead", file: file, fraction: 1},
		{name: "nil file", readahead: 16 * pieceLength, fraction: 1},
		{name: "file without torrent", file: &torrent.File{}, readahead: 16 * pieceLength, fraction: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planned := prioritizeNextPieces(tt.file, tt.byteOffset, tt.readahead, tt.fraction)
			if tt.want == nil {
				assert.Nil(t, planned)
				return
			}
			assert.Equal(t, tt.want, pieces(planned))
		})
	}
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

func BenchmarkStreamReadSeeker_Read(b *testing.B) {
	const totalSize = 16 << 20
	data := make([]byte, totalSize)
	for i := range data {
		data[i] = byte(i)
	}

	for _, size := range []int{4 << 10, 32 << 10, 256 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKB", size>>10), func(b *testing.B) {
			s := newStreamReadSeeker(bytes.NewReader(data), totalSize, nil)
			buf := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for range b.N {
				if _, err := io.ReadFull(s, buf); err != nil {
					if _, err := s.Seek(0, io.SeekStart); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkStreamReadSeeker_SeekRead(b *testing.B) {
	const totalSize = 16 << 20
	const chunkSize = 16 << 10
	const windowCount = totalSize / readBufferSize
	s := newStreamReadSeeker(bytes.NewReader(make([]byte, totalSize)), totalSize, nil)
	buf := make([]byte, chunkSize)

	b.SetBytes(chunkSize)
	b.ReportAllocs()
	for i := range b.N {
		// A coprime stride visits every buffer-sized window before repeating,
		// so each read refills the buffer.
		window := (i * 17) % windowCount
		if _, err := s.Seek(int64(window*readBufferSize), io.SeekStart); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(s, buf); err != nil {
			b.Fatal(err)
		}
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
