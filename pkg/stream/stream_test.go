// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/pkg/storage"
)

type dummyTorrentReader struct {
	torrent.Reader
	closed    bool
	data      []byte
	mu        sync.Mutex
	pos       int64
	readahead int64
}

func (d *dummyTorrentReader) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

func (d *dummyTorrentReader) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pos >= int64(len(d.data)) {
		return 0, io.EOF
	}
	n := copy(p, d.data[d.pos:])
	d.pos += int64(n)
	return n, nil
}

func (d *dummyTorrentReader) ReadContext(_ context.Context, p []byte) (int, error) {
	return d.Read(p)
}

func (d *dummyTorrentReader) Seek(offset int64, whence int) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch whence {
	case io.SeekStart:
		d.pos = offset
	case io.SeekCurrent:
		d.pos += offset
	case io.SeekEnd:
		d.pos = int64(len(d.data)) + offset
	}
	return d.pos, nil
}

func (d *dummyTorrentReader) SetContext(_ context.Context) {}

func (d *dummyTorrentReader) SetReadahead(a int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.readahead = a
}

type errTorrentReader struct {
	dummyTorrentReader
}

func (e *errTorrentReader) Seek(_ int64, _ int) (int64, error) {
	return 0, io.ErrUnexpectedEOF
}

type dummyReadSeeker struct {
	data []byte
	pos  int64
}

func (s *dummyReadSeeker) Read(p []byte) (int, error) {
	if s.pos >= int64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.pos:])
	s.pos += int64(n)
	return n, nil
}

func (s *dummyReadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		s.pos = offset
	case io.SeekCurrent:
		s.pos += offset
	case io.SeekEnd:
		s.pos = int64(len(s.data)) + offset
	}
	return s.pos, nil
}

type errReadSeeker struct{}

func (e *errReadSeeker) Read(_ []byte) (int, error) { return 0, io.EOF }
func (e *errReadSeeker) Seek(_ int64, _ int) (int64, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestReadAtReader_Operations(t *testing.T) {
	mockR := &dummyTorrentReader{data: []byte("hello world 1234567890")}
	rr := newReadAtReader(mockR)

	buf := make([]byte, 5)
	n, err := rr.ReadAt(buf, 6)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "world", string(buf))

	rr.SetReadahead(1024)
	assert.Equal(t, int64(1024), mockR.readahead)

	err = rr.Close()
	require.NoError(t, err)
	assert.True(t, mockR.closed)
}

func TestReadAtReader_SeekError(t *testing.T) {
	rr := newReadAtReader(&errTorrentReader{})
	buf := make([]byte, 10)
	_, err := rr.ReadAt(buf, 10)
	assert.Error(t, err)
}

func TestStreamReader_Release(t *testing.T) {
	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	sr := &streamReader{
		lastUsed: time.Now().Add(-10 * time.Minute),
		refs:     2,
	}
	pool.releaseReader(sr, 50*1024*1024)
	assert.Equal(t, 1, sr.refs)
	assert.True(t, time.Since(sr.lastUsed) < 1*time.Second)
}

func TestStreamReader_UpdateRange(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := storage.NewClient(10*1024*1024, logger)
	defer func() { _ = store.Close() }()

	var ih metainfo.Hash
	ih[0] = 0xAA

	info := &metainfo.Info{
		PieceLength: 1024 * 1024, // 1MB
		Pieces:      make([]byte, 20*10),
	}
	_, err := store.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	sr := &streamReader{
		fileLen:     10 * 1024 * 1024,
		fileOffset:  0,
		filePath:    "video.mp4",
		hash:        ih,
		lastUsed:    time.Now(),
		pieceLength: 1024 * 1024,
		positions:   &sync.Map{},
		readahead:   3 * 1024 * 1024, // 3MB readahead
		readerID:    1,
		refs:        1,
		storage:     store,
	}

	// Update range at byte 2MB (piece 2)
	sr.updateRange(2 * 1024 * 1024)

	val, ok := sr.positions.Load(uint64(1))
	require.True(t, ok)
	rInfo, ok := val.(ReaderInfo)
	require.True(t, ok)

	assert.Equal(t, 2, rInfo.Reader)
	// Window starts with trailing buffer (2 pieces behind): piece 0 (2MB - 2MB = 0MB), ends at piece 5 (2MB + 3MB = 5MB)
	assert.Equal(t, 0, rInfo.Start)
	assert.Equal(t, 5, rInfo.End)
}

func TestStreamReader_UpdateRange_PieceLengthZero(t *testing.T) {
	sr := &streamReader{
		pieceLength: 0,
		positions:   &sync.Map{},
		readerID:    55,
	}
	sr.updateRange(100)
	_, ok := sr.positions.Load(uint64(55))
	assert.False(t, ok)
}

func TestStreamReader_FastForwardAndRewindRanges(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := storage.NewClient(100*1024*1024, logger)
	defer func() { _ = store.Close() }()

	var ih metainfo.Hash
	ih[0] = 0xFE

	info := &metainfo.Info{
		PieceLength: 1024 * 1024, // 1MB pieces
		Pieces:      make([]byte, 20*100),
	}
	_, err := store.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	sr := &streamReader{
		fileLen:     100 * 1024 * 1024,
		fileOffset:  0,
		filePath:    "bunny.mp4",
		hash:        ih,
		lastUsed:    time.Now(),
		pieceLength: 1024 * 1024,
		positions:   &sync.Map{},
		readahead:   20 * 1024 * 1024, // 20MB readahead (trailingBuffer = 5MB = 5 pieces)
		readerID:    1,
		refs:        1,
		storage:     store,
	}

	// 1. Initial play at 0MB
	sr.updateRange(0)
	val, ok := sr.positions.Load(uint64(1))
	require.True(t, ok)
	rInfo := val.(ReaderInfo)
	assert.Equal(t, 0, rInfo.Reader)
	assert.Equal(t, 0, rInfo.Start)
	assert.Equal(t, 20, rInfo.End)

	// 2. Sequential play to 10MB (piece 10) -> trailing buffer covers 5 pieces behind (piece 5)
	sr.updateRange(10 * 1024 * 1024)
	val, _ = sr.positions.Load(uint64(1))
	rInfo = val.(ReaderInfo)
	assert.Equal(t, 10, rInfo.Reader)
	assert.Equal(t, 5, rInfo.Start) // 10MB - 5MB trailing = 5MB
	assert.Equal(t, 30, rInfo.End)  // 10MB + 20MB readahead = 30MB

	// 3. Fast-forward seek to 60MB (piece 60) -> trailing covers piece 55
	sr.updateRange(60 * 1024 * 1024)
	val, _ = sr.positions.Load(uint64(1))
	rInfo = val.(ReaderInfo)
	assert.Equal(t, 60, rInfo.Reader)
	assert.Equal(t, 55, rInfo.Start) // 60MB - 5MB = 55MB
	assert.Equal(t, 80, rInfo.End)   // 60MB + 20MB = 80MB

	// 4. Rewind seek back to 20MB (piece 20) -> trailing covers piece 15
	sr.updateRange(20 * 1024 * 1024)
	val, _ = sr.positions.Load(uint64(1))
	rInfo = val.(ReaderInfo)
	assert.Equal(t, 20, rInfo.Reader)
	assert.Equal(t, 15, rInfo.Start) // 20MB - 5MB = 15MB
	assert.Equal(t, 40, rInfo.End)   // 20MB + 20MB = 40MB
}

func TestPool_ReaderPositions(t *testing.T) {
	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	var ih metainfo.Hash
	ih[0] = 0xBB

	sr := &streamReader{
		hash:      ih,
		positions: &sync.Map{},
		readerID:  42,
	}
	sr.positions.Store(uint64(42), ReaderInfo{
		End:    10,
		Reader: 5,
		Start:  4,
	})

	key := readerKey(ih, "movie.mkv")
	pool.readers[key] = []*streamReader{sr}

	positions := pool.ReaderPositions(ih)
	require.Len(t, positions, 1)
	assert.Equal(t, 5, positions[0].Reader)
	assert.Equal(t, 4, positions[0].Start)
	assert.Equal(t, 10, positions[0].End)

	// Other hash should return empty
	var otherHash metainfo.Hash
	otherHash[0] = 0xCC
	assert.Empty(t, pool.ReaderPositions(otherHash))
}

func TestActiveRangeTracker_ReadAndSeek(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := storage.NewClient(10*1024*1024, logger)
	defer func() { _ = store.Close() }()

	var ih metainfo.Hash
	ih[0] = 0xDD

	info := &metainfo.Info{
		PieceLength: 1024 * 1024,
		Pieces:      make([]byte, 20*10),
	}
	_, err := store.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	sr := &streamReader{
		fileLen:     10 * 1024 * 1024,
		fileOffset:  0,
		filePath:    "test.mp4",
		hash:        ih,
		lastUsed:    time.Now(),
		pieceLength: 1024 * 1024,
		positions:   &sync.Map{},
		readahead:   2 * 1024 * 1024,
		readerID:    10,
		refs:        1,
		storage:     store,
	}

	mockRs := &dummyReadSeeker{data: make([]byte, 10*1024*1024)}
	tracker := newActiveRangeTracker(mockRs, sr)

	// Seek to 3MB
	_, err = tracker.Seek(3*1024*1024, io.SeekStart)
	require.NoError(t, err)

	val, ok := sr.positions.Load(uint64(10))
	require.True(t, ok)
	rInfo := val.(ReaderInfo)
	assert.Equal(t, 3, rInfo.Reader)
	// Window starts with trailing buffer (2 pieces behind): piece 1 (3MB - 2MB = 1MB), ends at piece 5 (3MB + 2MB = 5MB)
	assert.Equal(t, 1, rInfo.Start)
	assert.Equal(t, 5, rInfo.End)

	// Read 1MB
	buf := make([]byte, 1024*1024)
	n, err := tracker.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, len(buf), n)

	val, ok = sr.positions.Load(uint64(10))
	require.True(t, ok)
	rInfo = val.(ReaderInfo)
	assert.Equal(t, 4, rInfo.Reader)
}

func TestActiveRangeTracker_SeekError(t *testing.T) {
	sr := &streamReader{
		positions: &sync.Map{},
		readerID:  88,
	}
	tracker := newActiveRangeTracker(&errReadSeeker{}, sr)
	_, err := tracker.Seek(10, io.SeekStart)
	assert.Error(t, err)
}

func TestPool_CleanupIdle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := storage.NewClient(10*1024*1024, logger)
	defer func() { _ = store.Close() }()

	var ih metainfo.Hash
	ih[0] = 0xEE

	info := &metainfo.Info{
		PieceLength: 1024 * 1024,
		Pieces:      make([]byte, 20*10),
	}
	_, err := store.OpenTorrent(context.Background(), info, ih)
	require.NoError(t, err)

	mockR := &dummyTorrentReader{data: make([]byte, 1024)}
	srIdle := &streamReader{
		hash:      ih,
		lastUsed:  time.Now().Add(-1 * time.Hour), // Expired
		positions: &sync.Map{},
		reader:    newReadAtReader(mockR),
		readerID:  100,
		refs:      0,
	}
	srIdle.positions.Store(uint64(100), ReaderInfo{Reader: 1})
	store.SetActiveRange(ih, 100, 0, 1024)

	srActive := &streamReader{
		hash:      ih,
		lastUsed:  time.Now().Add(-1 * time.Hour),
		positions: &sync.Map{},
		readerID:  200,
		refs:      1, // Still active
	}

	pool := New(Config{
		Logger:  logger,
		Storage: store,
	})
	defer func() { _ = pool.Close() }()

	key := readerKey(ih, "test.mp4")
	pool.readers[key] = []*streamReader{srIdle, srActive}

	pool.cleanupIdle(time.Now())

	pool.mu.Lock()
	remaining := pool.readers[key]
	pool.mu.Unlock()

	require.Len(t, remaining, 1)
	assert.Equal(t, uint64(200), remaining[0].readerID)
	assert.True(t, mockR.closed)

	_, ok := srIdle.positions.Load(uint64(100))
	assert.False(t, ok)
}

func TestPool_TwoReaders_SameTorrent_Positions(t *testing.T) {
	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	var ih metainfo.Hash
	ih[0] = 0xEE

	sr1 := &streamReader{
		hash:      ih,
		positions: &sync.Map{},
		readerID:  101,
	}
	sr1.positions.Store(uint64(101), ReaderInfo{
		End:    25,
		Reader: 5,
		Start:  2,
	})

	sr2 := &streamReader{
		hash:      ih,
		positions: &sync.Map{},
		readerID:  102,
	}
	sr2.positions.Store(uint64(102), ReaderInfo{
		End:    70,
		Reader: 50,
		Start:  45,
	})

	key := readerKey(ih, "movie.mp4")
	pool.readers[key] = []*streamReader{sr1, sr2}

	positions := pool.ReaderPositions(ih)
	require.Len(t, positions, 2)

	readersFound := map[int]bool{}
	for _, p := range positions {
		readersFound[p.Reader] = true
	}
	assert.True(t, readersFound[5], "Reader 1 position reported")
	assert.True(t, readersFound[50], "Reader 2 position reported")
}

func TestComputeReadahead_SingleAndMultipleReaders(t *testing.T) {
	maxMem := int64(64 * 1024 * 1024) // 64MB
	readaheadPct := 80
	expectedPool := maxMem * int64(readaheadPct) / 100 // 53687091 bytes (~51.2MB)

	// 1. Single memory reader -> gets full pool (~51.2MB)
	r1 := ComputeReadahead(1024*1024, expectedPool, 1)
	assert.Equal(t, expectedPool, r1)

	// 2. Two memory readers -> divides fairly (~25.6MB each)
	r2 := ComputeReadahead(1024*1024, expectedPool, 2)
	assert.Equal(t, expectedPool/2, r2)

	// 3. Six memory readers -> bounded by 10MB floor
	r6 := ComputeReadahead(1024*1024, expectedPool, 6)
	assert.Equal(t, int64(10*1024*1024), r6)

	// 4. Large piece size (8MB) -> bounded by 2 pieces = 16MB floor
	rLargePiece := ComputeReadahead(8*1024*1024, expectedPool, 6)
	assert.Equal(t, int64(16*1024*1024), rLargePiece)
}

func TestDynamicReadahead_RefreshOnAcquireAndRelease(t *testing.T) {
	expectedPool := int64(64 * 1024 * 1024 * 80 / 100)

	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	mockR1 := &dummyTorrentReader{}
	sr1 := &streamReader{
		isFileStorage: false,
		pieceLength:   1024 * 1024,
		positions:     &sync.Map{},
		reader:        newReadAtReader(mockR1),
		readerID:      1,
		refs:          1,
	}
	pool.readers["key1"] = []*streamReader{sr1}

	// 1 active reader: refresh sets full pool
	pool.RefreshReadahead(expectedPool)
	assert.Equal(t, expectedPool, sr1.readahead)
	assert.Equal(t, expectedPool, mockR1.readahead)

	// Add 2nd active reader
	mockR2 := &dummyTorrentReader{}
	sr2 := &streamReader{
		isFileStorage: false,
		pieceLength:   1024 * 1024,
		positions:     &sync.Map{},
		reader:        newReadAtReader(mockR2),
		readerID:      2,
		refs:          1,
	}
	pool.readers["key2"] = []*streamReader{sr2}

	// 2 active readers: both divide to expectedPool / 2
	pool.RefreshReadahead(expectedPool)
	assert.Equal(t, expectedPool/2, sr1.readahead)
	assert.Equal(t, expectedPool/2, mockR1.readahead)
	assert.Equal(t, expectedPool/2, sr2.readahead)
	assert.Equal(t, expectedPool/2, mockR2.readahead)

	// Release reader 2
	pool.releaseReader(sr2, expectedPool)

	// Reader 1 expands back to full pool
	assert.Equal(t, expectedPool, sr1.readahead)
	assert.Equal(t, expectedPool, mockR1.readahead)
}

func TestStreamReader_NoDeadlock_ConcurrentReadAndRefresh(t *testing.T) {
	expectedPool := int64(64 * 1024 * 1024 * 80 / 100)

	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	mockR := &dummyTorrentReader{data: make([]byte, 1024*1024)}
	sr := &streamReader{
		fileLen:       1024 * 1024,
		isFileStorage: false,
		pieceLength:   64 * 1024,
		positions:     &sync.Map{},
		reader:        newReadAtReader(mockR),
		readerID:      1,
		refs:          1,
	}
	pool.readers["key1"] = []*streamReader{sr}

	secReader := io.NewSectionReader(sr.reader, 0, sr.fileLen)
	tracker := newActiveRangeTracker(secReader, sr)

	done := make(chan struct{})

	// Goroutine 1: Continuous streaming reads + range updates
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for i := 0; i < 200; i++ {
			_, _ = tracker.Seek(int64((i*1024)%len(mockR.data)), io.SeekStart)
			_, _ = tracker.Read(buf)
		}
	}()

	// Goroutine 2: Concurrent readahead refreshes and reader acquires/releases
	for i := 0; i < 200; i++ {
		pool.RefreshReadahead(expectedPool)
	}

	select {
	case <-done:
		// Succeeded without deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected between stream read and readahead refresh")
	}
}

type blockingTorrentReader struct {
	dummyTorrentReader
	readEntered chan struct{}
	readUnblock chan struct{}
}

func (b *blockingTorrentReader) Read(p []byte) (int, error) {
	if b.readEntered != nil {
		select {
		case b.readEntered <- struct{}{}:
		default:
		}
	}
	if b.readUnblock != nil {
		<-b.readUnblock
	}
	return b.dummyTorrentReader.Read(p)
}

func TestStreamReader_BlockingRead_ConcurrentReadaheadRefresh(t *testing.T) {
	expectedPool := int64(64 * 1024 * 1024 * 80 / 100)

	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	readEntered := make(chan struct{}, 1)
	readUnblock := make(chan struct{})

	mockR := &blockingTorrentReader{
		dummyTorrentReader: dummyTorrentReader{data: make([]byte, 1024*1024)},
		readEntered:        readEntered,
		readUnblock:        readUnblock,
	}

	sr := &streamReader{
		fileLen:       1024 * 1024,
		isFileStorage: false,
		pieceLength:   64 * 1024,
		positions:     &sync.Map{},
		reader:        newReadAtReader(mockR),
		readerID:      1,
		refs:          1,
	}
	pool.readers["key1"] = []*streamReader{sr}

	readDone := make(chan struct{})

	// Goroutine 1: Initiates a slow/blocking Read (simulating network peer download wait while holding readAtReader.mu)
	go func() {
		defer close(readDone)
		buf := make([]byte, 1024)
		_, _ = sr.reader.ReadAt(buf, 0)
	}()

	// Wait until ReadAt has entered Read() and is actively blocking
	select {
	case <-readEntered:
		// Goroutine 1 is now actively holding readAtReader.mu and blocked on network/unblock
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for read to start")
	}

	// Goroutine 2: While ReadAt is blocked, execute RefreshReadahead()
	// This must NOT block waiting for readAtReader.mu or keep Pool.mu locked.
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		pool.RefreshReadahead(expectedPool)
	}()

	select {
	case <-refreshDone:
		// Success: refresh completed without blocking on the active ReadAt call!
	case <-time.After(500 * time.Millisecond):
		close(readUnblock)
		t.Fatal("DEADLOCK: RefreshReadahead blocked on an active ReadAt call!")
	}

	// Also verify that another request can acquire Pool.mu immediately
	lockAcquired := make(chan struct{})
	go func() {
		defer close(lockAcquired)
		pool.mu.Lock()
		defer pool.mu.Unlock()
	}()

	select {
	case <-lockAcquired:
		// Success: Pool.mu was not left locked
	case <-time.After(500 * time.Millisecond):
		close(readUnblock)
		t.Fatal("DEADLOCK: Pool.mu is blocked by background refresh!")
	}

	// Now unblock the streaming read
	close(readUnblock)

	select {
	case <-readDone:
		// Clean completion
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ReadAt to complete")
	}
}

func createTestTorrent(t *testing.T) (*torrent.Client, *torrent.Torrent) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.NoDHT = true
	cfg.DisableIPv6 = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cfg.DataDir = t.TempDir()
	client, err := torrent.NewClient(cfg)
	require.NoError(t, err)

	info := metainfo.Info{
		PieceLength: 1024 * 1024,
		Pieces:      make([]byte, 20*10),
		Name:        "test_torrent",
		Files: []metainfo.FileInfo{
			{
				Length: 10 * 1024 * 1024,
				Path:   []string{"video.mp4"},
			},
		},
	}
	var buf bytes.Buffer
	err = bencode.NewEncoder(&buf).Encode(&info)
	require.NoError(t, err)
	mi := metainfo.MetaInfo{
		InfoBytes: buf.Bytes(),
	}
	spec := torrent.TorrentSpecFromMetaInfo(&mi)
	to, _, err := client.AddTorrentSpec(spec)
	require.NoError(t, err)
	<-to.GotInfo()
	return client, to
}

func TestPool_Acquire_NewAndReuse(t *testing.T) {
	client, to := createTestTorrent(t)
	defer client.Close()

	store := storage.NewClient(64*1024*1024, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() { _ = store.Close() }()

	pool := New(Config{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Storage: store,
	})
	defer func() { _ = pool.Close() }()

	ih := to.InfoHash()
	file := to.Files()[0]
	pieceLength := to.Info().PieceLength
	totalReadahead := int64(30 * 1024 * 1024)

	// 1. Acquire first reader
	rs1, release1 := pool.Acquire(ih, file, pieceLength, false, totalReadahead)
	require.NotNil(t, rs1)

	// Seek and verify active position is tracked
	_, err := rs1.Seek(2*1024*1024, io.SeekStart)
	require.NoError(t, err)

	positions := pool.ReaderPositions(ih)
	require.Len(t, positions, 1)
	assert.Equal(t, 2, positions[0].Reader)

	// 2. Acquire concurrent reader for same file (creates 2nd instance)
	rs2, release2 := pool.Acquire(ih, file, pieceLength, false, totalReadahead)
	require.NotNil(t, rs2)

	_, err = rs2.Seek(6*1024*1024, io.SeekStart)
	require.NoError(t, err)

	positions = pool.ReaderPositions(ih)
	require.Len(t, positions, 2)

	// Release first reader (now idle)
	release1()

	// 3. Acquire 3rd stream: reuses idle reader 1
	rs3, release3 := pool.Acquire(ih, file, pieceLength, false, totalReadahead)
	require.NotNil(t, rs3)

	release2()
	release3()
}

func TestPool_Acquire_FileStorage(t *testing.T) {
	client, to := createTestTorrent(t)
	defer client.Close()

	pool := New(Config{
		FileStorageReadahead: 20 * 1024 * 1024,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	ih := to.InfoHash()
	file := to.Files()[0]
	pieceLength := to.Info().PieceLength

	rs, release := pool.Acquire(ih, file, pieceLength, true, 0)
	require.NotNil(t, rs)
	release()
}

func TestPool_Close_IdempotentAndConcurrent(t *testing.T) {
	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	// Concurrent close calls should not panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, pool.Close())
		}()
	}
	wg.Wait()

	// Serial repeat close
	assert.NoError(t, pool.Close())
}

func TestPool_Acquire_Release_Idempotent(t *testing.T) {
	client, to := createTestTorrent(t)
	defer client.Close()

	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	ih := to.InfoHash()
	file := to.Files()[0]
	pieceLength := to.Info().PieceLength

	rs, release := pool.Acquire(ih, file, pieceLength, false, 20*1024*1024)
	require.NotNil(t, rs)

	key := readerKey(ih, file.Path())
	pool.mu.Lock()
	srList := pool.readers[key]
	pool.mu.Unlock()
	require.Len(t, srList, 1)

	srList[0].mu.Lock()
	assert.Equal(t, 1, srList[0].refs)
	srList[0].mu.Unlock()

	// Call release multiple times
	release()
	release()
	release()

	srList[0].mu.Lock()
	assert.Equal(t, 0, srList[0].refs)
	srList[0].mu.Unlock()
}

func TestPool_CloseTorrent_RemovesReadersAndClearsActiveRange(t *testing.T) {
	client, to1 := createTestTorrent(t)
	defer client.Close()

	storageClient := storage.NewClient(10*1024*1024, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() { _ = storageClient.Close() }()

	pool := New(Config{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Storage: storageClient,
	})
	defer func() { _ = pool.Close() }()

	ih1 := to1.InfoHash()
	file1 := to1.Files()[0]
	pieceLen1 := to1.Info().PieceLength

	rs1, release1 := pool.Acquire(ih1, file1, pieceLen1, false, 10*1024*1024)
	require.NotNil(t, rs1)
	defer release1()

	// Verify reader position exists
	positions1 := pool.ReaderPositions(ih1)
	require.Len(t, positions1, 1)

	// Close the torrent
	pool.CloseTorrent(ih1)

	// Positions for ih1 should now be empty
	positionsAfter := pool.ReaderPositions(ih1)
	assert.Empty(t, positionsAfter)

	// Readers map in pool should no longer contain keys for ih1
	pool.mu.Lock()
	for key := range pool.readers {
		assert.False(t, strings.HasPrefix(key, ih1.HexString()))
	}
	pool.mu.Unlock()
}

func TestPool_CloseTorrent_NonExistentTorrentNoPanic(t *testing.T) {
	pool := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = pool.Close() }()

	fakeHash := metainfo.NewHashFromHex("0123456789abcdef0123456789abcdef01234567")
	assert.NotPanics(t, func() {
		pool.CloseTorrent(fakeHash)
	})
}
