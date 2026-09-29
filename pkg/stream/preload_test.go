// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addHashedTorrent adds a single-file torrent named name of ten 64-byte pieces
// that carry real hashes of deterministic data, so pieces written to storage
// can be verified. It returns the torrent, its file, and the data.
func addHashedTorrent(t *testing.T, c *torrent.Client, name string) (*torrent.Torrent, *torrent.File, []byte) {
	t.Helper()
	const pieceLength, length = 64, 640
	data := make([]byte, length)
	for i := range data {
		data[i] = byte(i*31 + len(name))
	}
	var pieces []byte
	for start := int64(0); start < length; start += pieceLength {
		sum := sha1.Sum(data[start:min(start+pieceLength, length)])
		pieces = append(pieces, sum[:]...)
	}
	infoBytes, err := bencode.Marshal(metainfo.Info{Name: name, PieceLength: pieceLength, Length: length, Pieces: pieces})
	require.NoError(t, err)
	to, file := addTestTorrentFromMetaInfo(t, c, &metainfo.MetaInfo{InfoBytes: infoBytes})
	return to, file, data
}

// addSizedTorrent adds a single-file torrent without data, for tests that only
// plan or schedule preloads.
func addSizedTorrent(t *testing.T, c *torrent.Client, name string, pieceLength, length int64) (*torrent.Torrent, *torrent.File) {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name:        name,
		PieceLength: pieceLength,
		Length:      length,
		Pieces:      make([]byte, (length+pieceLength-1)/pieceLength*sha1.Size),
	})
	require.NoError(t, err)
	return addTestTorrentFromMetaInfo(t, c, &metainfo.MetaInfo{InfoBytes: infoBytes})
}

// writePieces writes data into the given pieces' storage and verifies them,
// so the torrent client marks them complete, or incomplete when data does not
// match their hashes.
func writePieces(t *testing.T, to *torrent.Torrent, data []byte, indexes ...int) {
	t.Helper()
	pieceLength := to.Info().PieceLength
	for _, index := range indexes {
		start := int64(index) * pieceLength
		_, err := to.Piece(index).Storage().WriteAt(data[start:min(start+pieceLength, int64(len(data)))], 0)
		require.NoError(t, err)
		require.NoError(t, to.Piece(index).VerifyData())
	}
}

// allPieces returns the indexes of every piece of to.
func allPieces(to *torrent.Torrent) []int {
	indexes := make([]int, to.NumPieces())
	for i := range indexes {
		indexes[i] = i
	}
	return indexes
}

// preloadState returns the state of a torrent's preload, or zero when it has
// none.
func preloadState(p *Pool, infoHash metainfo.Hash) PreloadState {
	status, _ := p.PreloadStatus(infoHash)
	return status.State
}

// waitForPreloadState waits until a torrent's preload reaches state.
func waitForPreloadState(t *testing.T, p *Pool, infoHash metainfo.Hash, state PreloadState) {
	t.Helper()
	require.Eventually(t, func() bool { return preloadState(p, infoHash) == state }, 5*time.Second, time.Millisecond,
		"preload did not become %s", state)
}

// preloadClaims returns the priority each piece of to is claimed at by the
// torrent's preload.
func preloadClaims(p *Pool, to *torrent.Torrent) map[int]torrent.PiecePriority {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl := p.preloads[to.InfoHash()]
	claims := make(map[int]torrent.PiecePriority)
	for key, claim := range p.priorityClaims {
		if priority, ok := claim[pl]; ok && key.torrent == to {
			claims[key.index] = priority
		}
	}
	return claims
}

// reservedPreloadBytes returns the bytes all preloads of p reserve.
func reservedPreloadBytes(p *Pool) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reservedPreloadBytesLocked()
}

// holdPreload registers a running memory preload of file, or of a file
// without data when file is nil, that holds reservation, for tests of the
// pool's budget accounting. It reports whether the reservation fit.
func holdPreload(p *Pool, infoHash metainfo.Hash, file *torrent.File, reservation preloadReservation) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if file == nil {
		file = &torrent.File{}
	}
	pl := &preload{file: file, infoHash: infoHash, mode: MemoryStorage, reservation: reservation, state: PreloadRunning}
	// Register the preload first, as Preload does, so the rebalance after the
	// reservation counts it.
	p.preloads[infoHash] = pl
	if !p.reservePreloadLocked(pl) {
		delete(p.preloads, infoHash)
		return false
	}
	return true
}

func TestPreloadState_String(t *testing.T) {
	for state, want := range map[PreloadState]string{
		PreloadQueued:   "queued",
		PreloadRunning:  "running",
		PreloadReady:    "ready",
		PreloadFailed:   "failed",
		PreloadEvicted:  "evicted",
		PreloadState(0): "unknown",
	} {
		assert.Equal(t, want, state.String())
	}
}

func TestPool_Preload(t *testing.T) {
	newPool := func(t *testing.T, reg ProtectionRegistry) *Pool {
		t.Helper()
		pool := New(Config{Logger: testLogger(), Registry: reg})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		return pool
	}

	t.Run("claims its pieces at high priority until they complete", func(t *testing.T) {
		c := newTestTorrentClient(t)
		// Ten 64-byte pieces.
		to, file, data := addHashedTorrent(t, c, "movie")
		reg := newProtectionRegistry()
		pool := newPool(t, reg)

		status, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		assert.Equal(t, PreloadStatus{FileIndex: 0, FilePath: file.Path(), State: PreloadRunning, TargetBytes: 640}, status)
		claims := preloadClaims(pool, to)
		assert.Len(t, claims, 10)
		for index, priority := range claims {
			assert.Equal(t, torrent.PiecePriorityHigh, priority, "piece %d", index)
		}
		assert.Len(t, reg.protectedPieces(), 10, "a memory preload protects its pieces while downloading")

		writePieces(t, to, data, 0, 1, 2)
		require.Eventually(t, func() bool {
			status, _ := pool.PreloadStatus(to.InfoHash())
			return status.CompletedBytes == 192
		}, 5*time.Second, time.Millisecond, "progress must follow completed pieces")
		assert.Equal(t, PreloadRunning, preloadState(pool, to.InfoHash()))

		writePieces(t, to, data, allPieces(to)...)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
		status, _ = pool.PreloadStatus(to.InfoHash())
		assert.Equal(t, int64(640), status.CompletedBytes)
		assert.Empty(t, preloadClaims(pool, to), "a ready preload needs no priority")
		assert.Len(t, reg.protectedPieces(), 10, "a ready preload keeps its cache protected")
	})

	t.Run("is ready at once when its pieces are already complete", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "cached")
		writePieces(t, to, data, allPieces(to)...)
		pool := newPool(t, nil)

		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
	})

	t.Run("runs again when it loses a piece", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "evicted")
		writePieces(t, to, data, allPieces(to)...)
		pool := newPool(t, nil)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)

		// Corrupt data fails verification, which marks the piece incomplete
		// as an eviction does.
		corrupt := slices.Clone(data)
		corrupt[3*64] ^= 0xff
		writePieces(t, to, corrupt, 3)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadRunning)
		assert.Len(t, preloadClaims(pool, to), 10, "a preload that runs again claims its pieces again")

		writePieces(t, to, data, 3)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
	})

	t.Run("is removed rather than run again when it loses a piece past its ready TTL", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "expired")
		writePieces(t, to, data, allPieces(to)...)
		pool := newPool(t, nil)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
		// Expire the preload and report the lost piece under one lock, so
		// the pool's expiry loop cannot remove it first.
		pool.mu.Lock()
		pl := pool.preloads[to.InfoHash()]
		pl.readyAt = time.Now().Add(-2 * defaultPreloadReadyTTL)
		pool.updatePreloadLocked(pl, pl.completedBytes-64, false)
		pool.mu.Unlock()

		assert.Zero(t, preloadState(pool, to.InfoHash()), "an expired unread preload must be removed")
		assert.Empty(t, preloadClaims(pool, to), "it must not claim its pieces again")
	})

	t.Run("keeps its ready TTL when it downloads a lost piece again", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "again")
		writePieces(t, to, data, allPieces(to)...)
		pool := newPool(t, nil)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
		readyAt := time.Now().Add(-time.Minute)
		pool.mu.Lock()
		pool.preloads[to.InfoHash()].readyAt = readyAt
		pool.mu.Unlock()

		corrupt := slices.Clone(data)
		corrupt[3*64] ^= 0xff
		writePieces(t, to, corrupt, 3)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadRunning)
		writePieces(t, to, data, 3)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)

		pool.mu.Lock()
		defer pool.mu.Unlock()
		assert.True(t, pool.preloads[to.InfoHash()].readyAt.Equal(readyAt), "the ready TTL must run from when it first became ready")
	})

	t.Run("waits for a slot when it loses a piece while every slot is taken", func(t *testing.T) {
		c := newTestTorrentClient(t)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		reg := newProtectionRegistry()
		pool := newPool(t, reg)
		owners := func() int {
			reg.mu.Lock()
			defer reg.mu.Unlock()
			return len(reg.ranges)
		}
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		running := make([]*torrent.Torrent, 0, maxConcurrentPreloads)
		runningData := make([][]byte, 0, maxConcurrentPreloads)
		for i := range maxConcurrentPreloads {
			to, file, data := addHashedTorrent(t, c, fmt.Sprintf("running%d", i))
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
			running = append(running, to)
			runningData = append(runningData, data)
		}
		reserved := reservedPreloadBytes(pool)
		require.Equal(t, 1+maxConcurrentPreloads, owners(), "each preload protects its pieces under one owner")

		corrupt := slices.Clone(readyData)
		corrupt[3*64] ^= 0xff
		writePieces(t, ready, corrupt, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadQueued)
		assert.Empty(t, preloadClaims(pool, ready), "a preload waiting for a slot claims nothing")
		assert.Equal(t, reserved, reservedPreloadBytes(pool), "it keeps its reservation")

		// A finished preload frees a slot for it without a second reservation.
		writePieces(t, running[0], runningData[0], allPieces(running[0])...)
		waitForPreloadState(t, pool, running[0].InfoHash(), PreloadReady)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadRunning)
		assert.Len(t, preloadClaims(pool, ready), 10)
		assert.Equal(t, reserved, reservedPreloadBytes(pool))
		assert.Equal(t, 1+maxConcurrentPreloads, owners(), "running again must not protect its pieces a second time")

		writePieces(t, ready, readyData, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
	})

	t.Run("releases a read preload waiting for a slot once playback ends", func(t *testing.T) {
		c := newTestTorrentClient(t)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		pool := New(Config{Logger: testLogger(), LingerTimeout: time.Nanosecond})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		_, release, err := pool.Acquire(context.Background(), readyFile, MemoryStorage)
		require.NoError(t, err)
		for i := range maxConcurrentPreloads {
			_, file, _ := addHashedTorrent(t, c, fmt.Sprintf("running%d", i))
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		corrupt := slices.Clone(readyData)
		corrupt[3*64] ^= 0xff
		writePieces(t, ready, corrupt, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadQueued)
		reserved := reservedPreloadBytes(pool)

		release()
		pool.closeExpiredLingeringReaders()
		assert.Zero(t, preloadState(pool, ready.InfoHash()), "a read preload must be released once its file has no reader")
		assert.Less(t, reservedPreloadBytes(pool), reserved, "its reservation must return to playback")
	})

	t.Run("expires while waiting for a slot once its ready TTL runs out", func(t *testing.T) {
		c := newTestTorrentClient(t)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		pool := newPool(t, nil)
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		for i := range maxConcurrentPreloads {
			_, file, _ := addHashedTorrent(t, c, fmt.Sprintf("running%d", i))
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		corrupt := slices.Clone(readyData)
		corrupt[3*64] ^= 0xff
		writePieces(t, ready, corrupt, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadQueued)
		reserved := reservedPreloadBytes(pool)

		pool.expirePreloads()
		require.Equal(t, PreloadQueued, preloadState(pool, ready.InfoHash()), "it keeps waiting within its ready TTL")
		pool.mu.Lock()
		pool.preloads[ready.InfoHash()].readyAt = time.Now().Add(-2 * defaultPreloadReadyTTL)
		pool.mu.Unlock()
		pool.expirePreloads()
		assert.Zero(t, preloadState(pool, ready.InfoHash()), "an unread preload past its ready TTL must not wait to download again")
		assert.Less(t, reservedPreloadBytes(pool), reserved, "its reservation must return to playback")
	})

	t.Run("is evicted for a new preload while waiting for a slot", func(t *testing.T) {
		c := newTestTorrentClient(t)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		pool := newPool(t, nil)
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		for i := range maxConcurrentPreloads {
			_, file, _ := addHashedTorrent(t, c, fmt.Sprintf("running%d", i))
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		corrupt := slices.Clone(readyData)
		corrupt[3*64] ^= 0xff
		writePieces(t, ready, corrupt, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadQueued)

		pool.mu.Lock()
		evicted := pool.evictIdlePreloadLocked()
		pool.mu.Unlock()
		require.True(t, evicted, "a waiting cache is idle")
		assert.Equal(t, PreloadEvicted, preloadState(pool, ready.InfoHash()))
	})

	t.Run("becomes ready again while waiting when its piece returns", func(t *testing.T) {
		c := newTestTorrentClient(t)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		pool := newPool(t, nil)
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		for i := range maxConcurrentPreloads {
			_, file, _ := addHashedTorrent(t, c, fmt.Sprintf("running%d", i))
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}

		corrupt := slices.Clone(readyData)
		corrupt[3*64] ^= 0xff
		writePieces(t, ready, corrupt, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadQueued)
		writePieces(t, ready, readyData, 3)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		assert.Empty(t, preloadClaims(pool, ready))
	})

	t.Run("returns the preload of the same file unchanged", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		pool := newPool(t, nil)

		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		pool.mu.Lock()
		first := pool.preloads[to.InfoHash()]
		pool.mu.Unlock()
		_, err = pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		pool.mu.Lock()
		assert.Same(t, first, pool.preloads[to.InfoHash()])
		pool.mu.Unlock()
	})

	t.Run("replaces the preload of another file of the torrent", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, _ := addTestTorrentFromMetaInfo(t, c, createMisalignedFileTestMetaInfo(t))
		files := to.Files()
		reg := newProtectionRegistry()
		pool := newPool(t, reg)

		_, err := pool.Preload(files[0], MemoryStorage)
		require.NoError(t, err)
		status, err := pool.Preload(files[1], MemoryStorage)
		require.NoError(t, err)
		assert.Equal(t, 1, status.FileIndex)
		assert.Equal(t, files[1].Path(), status.FilePath)
		// video.bin spans torrent pieces 0 through 8; header.bin only piece 0.
		assert.Len(t, preloadClaims(pool, to), 9)
		pool.mu.Lock()
		claimedPieces := len(pool.priorityClaims)
		pool.mu.Unlock()
		assert.Equal(t, 9, claimedPieces, "the replaced preload must release its claims")
		assert.Len(t, reg.protectedPieces(), 9)
	})

	t.Run("reserves nothing for file storage", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		reg := newProtectionRegistry()
		pool := newPool(t, reg)
		pool.SetReadaheadBudget(0)

		status, err := pool.Preload(file, FileStorage)
		require.NoError(t, err)
		assert.Equal(t, PreloadRunning, status.State, "file storage needs no memory")
		assert.Len(t, preloadClaims(pool, to), 10)
		assert.Empty(t, reg.protectedPieces())
	})

	t.Run("does not fit an empty budget", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		pool := newPool(t, nil)
		pool.SetReadaheadBudget(0)

		_, err := pool.Preload(file, MemoryStorage)
		require.ErrorIs(t, err, ErrPreloadDoesNotFit)
		_, ok := pool.PreloadStatus(to.InfoHash())
		assert.False(t, ok)
	})

	t.Run("rejects invalid requests", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, file := addSizedTorrent(t, c, "movie", 64, 640)
		pool := newPool(t, nil)

		_, err := pool.Preload(nil, MemoryStorage)
		require.ErrorIs(t, err, ErrInvalidFile)
		_, err = pool.Preload(&torrent.File{}, MemoryStorage)
		require.ErrorIs(t, err, ErrInvalidFile)
		_, err = pool.Preload(file, StorageMode(9))
		require.ErrorIs(t, err, ErrInvalidStorageMode)
		pool.Close()
		_, err = pool.Preload(file, MemoryStorage)
		require.ErrorIs(t, err, ErrPoolClosed)
	})

	t.Run("cancel releases its claims and protection", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		reg := newProtectionRegistry()
		pool := newPool(t, reg)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)

		pool.CancelPreload(to.InfoHash())
		pool.CancelPreload(to.InfoHash())
		_, ok := pool.PreloadStatus(to.InfoHash())
		assert.False(t, ok)
		pool.mu.Lock()
		assert.Empty(t, pool.priorityClaims)
		pool.mu.Unlock()
		assert.Empty(t, reg.protectedPieces())
		assert.Zero(t, reservedPreloadBytes(pool))
	})

	t.Run("is removed when its torrent closes", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		reg := newProtectionRegistry()
		pool := newPool(t, reg)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)

		to.Drop()
		<-to.Closed()
		require.Eventually(t, func() bool {
			_, ok := pool.PreloadStatus(to.InfoHash())
			return !ok
		}, 5*time.Second, time.Millisecond)
		assert.Empty(t, reg.protectedPieces())
	})

	t.Run("close stops every preload", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, file := addSizedTorrent(t, c, "movie", 64, 640)
		reg := newProtectionRegistry()
		pool := newPool(t, reg)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)

		pool.Close()
		pool.mu.Lock()
		assert.Empty(t, pool.preloads)
		pool.mu.Unlock()
		assert.Empty(t, reg.protectedPieces())
	})
}

func TestPreloadScheduling(t *testing.T) {
	// A 1500-byte budget leaves a 750-byte preload share, which holds one
	// 640-byte reservation but not two.
	const oneReservationBudget = 1500

	t.Run("runs at most two preloads at a time", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		first, firstFile, firstData := addHashedTorrent(t, c, "first")
		second, secondFile := addSizedTorrent(t, c, "second", 64, 640)
		third, thirdFile := addSizedTorrent(t, c, "third", 64, 640)

		for _, file := range []*torrent.File{firstFile, secondFile, thirdFile} {
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		assert.Equal(t, PreloadRunning, preloadState(pool, first.InfoHash()))
		assert.Equal(t, PreloadRunning, preloadState(pool, second.InfoHash()))
		assert.Equal(t, PreloadQueued, preloadState(pool, third.InfoHash()))

		writePieces(t, first, firstData, allPieces(first)...)
		waitForPreloadState(t, pool, first.InfoHash(), PreloadReady)
		waitForPreloadState(t, pool, third.InfoHash(), PreloadRunning)
	})

	t.Run("evicts an idle ready preload to admit a queued one", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(oneReservationBudget)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)

		queued, queuedFile := addSizedTorrent(t, c, "queued", 64, 640)
		status, err := pool.Preload(queuedFile, MemoryStorage)
		require.NoError(t, err)
		assert.Equal(t, PreloadRunning, status.State)
		assert.Equal(t, PreloadEvicted, preloadState(pool, ready.InfoHash()))
		assert.Equal(t, PreloadRunning, preloadState(pool, queued.InfoHash()))
	})

	t.Run("keeps a ready preload pinned while its file is read", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(oneReservationBudget)
		ready, readyFile, readyData := addHashedTorrent(t, c, "ready")
		writePieces(t, ready, readyData, allPieces(ready)...)
		_, err := pool.Preload(readyFile, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, ready.InfoHash(), PreloadReady)
		_, release, err := pool.Acquire(context.Background(), readyFile, MemoryStorage)
		require.NoError(t, err)
		defer release()

		// Nothing is running and the only ready preload is in use, so the new
		// preload has nothing to wait for.
		failed, failedFile := addSizedTorrent(t, c, "failed", 64, 640)
		status, err := pool.Preload(failedFile, MemoryStorage)
		require.NoError(t, err)
		assert.Equal(t, PreloadFailed, status.State)
		assert.Equal(t, PreloadReady, preloadState(pool, ready.InfoHash()))
		assert.Equal(t, PreloadFailed, preloadState(pool, failed.InfoHash()))
	})

	t.Run("waits for a running preload to make room", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(oneReservationBudget)
		running, runningFile, runningData := addHashedTorrent(t, c, "running")
		_, err := pool.Preload(runningFile, MemoryStorage)
		require.NoError(t, err)
		waiting, waitingFile := addSizedTorrent(t, c, "waiting", 64, 640)
		_, err = pool.Preload(waitingFile, MemoryStorage)
		require.NoError(t, err)
		assert.Equal(t, PreloadQueued, preloadState(pool, waiting.InfoHash()))

		// Once ready and unread, the running preload yields its memory.
		writePieces(t, running, runningData, allPieces(running)...)
		waitForPreloadState(t, pool, waiting.InfoHash(), PreloadRunning)
		assert.Equal(t, PreloadEvicted, preloadState(pool, running.InfoHash()))
	})

	t.Run("does not pause for playback", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		played, playedFile := addSizedTorrent(t, c, "played", 64, 640)
		_, release, err := pool.Acquire(context.Background(), playedFile, MemoryStorage)
		require.NoError(t, err)
		defer release()
		release()
		require.True(t, pool.HasReaders(played.InfoHash()), "the released reader lingers")

		preloaded, preloadedFile := addSizedTorrent(t, c, "preloaded", 64, 640)
		status, err := pool.Preload(preloadedFile, MemoryStorage)
		require.NoError(t, err)
		assert.Equal(t, PreloadRunning, status.State, "preloads run alongside playback")
		assert.True(t, pool.HasReaders(played.InfoHash()), "a preload must not close a lingering reader")
		assert.Equal(t, PreloadRunning, preloadState(pool, preloaded.InfoHash()))
	})
}

func TestPool_ExpirePreloads(t *testing.T) {
	newReadyPreload := func(t *testing.T, ttl time.Duration) (*Pool, *torrent.Torrent, *torrent.File) {
		t.Helper()
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger(), PreloadReadyTTL: ttl})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		to, file, data := addHashedTorrent(t, c, "movie")
		writePieces(t, to, data, allPieces(to)...)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
		return pool, to, file
	}
	idleFor := func(p *Pool, infoHash metainfo.Hash, idle time.Duration) {
		p.mu.Lock()
		defer p.mu.Unlock()
		pl := p.preloads[infoHash]
		pl.readyAt = time.Now().Add(-idle)
		pl.finishedAt = time.Now().Add(-idle)
	}

	t.Run("removes a ready preload unread for the TTL", func(t *testing.T) {
		pool, to, _ := newReadyPreload(t, time.Minute)
		pool.expirePreloads()
		assert.Equal(t, PreloadReady, preloadState(pool, to.InfoHash()))

		idleFor(pool, to.InfoHash(), time.Minute)
		pool.expirePreloads()
		_, ok := pool.PreloadStatus(to.InfoHash())
		assert.False(t, ok)
		assert.Zero(t, reservedPreloadBytes(pool), "an expired preload releases its memory")
	})

	t.Run("keeps a preload while its file is read", func(t *testing.T) {
		pool, to, file := newReadyPreload(t, time.Minute)
		_, release, err := pool.Acquire(context.Background(), file, MemoryStorage)
		require.NoError(t, err)
		defer release()
		idleFor(pool, to.InfoHash(), time.Hour)
		pool.expirePreloads()
		assert.Equal(t, PreloadReady, preloadState(pool, to.InfoHash()), "a preload being read must not expire")
	})

	t.Run("releases a read preload once its lingering reader closes", func(t *testing.T) {
		pool, to, file := newReadyPreload(t, time.Hour)
		_, release, err := pool.Acquire(context.Background(), file, MemoryStorage)
		require.NoError(t, err)
		release()
		pool.expirePreloads()
		assert.Equal(t, PreloadReady, preloadState(pool, to.InfoHash()), "the lingering reader bridges the player's next request")

		pool.mu.Lock()
		for _, sr := range pool.readers {
			sr.lingerSince = time.Now().Add(-time.Hour)
		}
		pool.mu.Unlock()
		pool.closeExpiredLingeringReaders()
		_, ok := pool.PreloadStatus(to.InfoHash())
		assert.False(t, ok, "playback of the file has ended, so its cache is released")
		assert.Zero(t, reservedPreloadBytes(pool))
	})

	t.Run("keeps a read preload while another viewer streams another file", func(t *testing.T) {
		pool, to, file := newReadyPreload(t, time.Hour)
		_, release, err := pool.Acquire(context.Background(), file, MemoryStorage)
		require.NoError(t, err)
		release()

		c := newTestTorrentClient(t)
		_, otherFile := addSizedTorrent(t, c, "other", 64, 640)
		_, releaseOther, err := pool.Acquire(context.Background(), otherFile, MemoryStorage)
		require.NoError(t, err)
		defer releaseOther()
		assert.True(t, pool.HasReaders(to.InfoHash()), "another viewer must not close the lingering reader")
		assert.Equal(t, PreloadReady, preloadState(pool, to.InfoHash()))
	})

	t.Run("releases on ready when playback ended while it ran", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger(), PreloadReadyTTL: time.Hour})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		to, file, data := addHashedTorrent(t, c, "movie")
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		_, release, err := pool.Acquire(context.Background(), file, MemoryStorage)
		require.NoError(t, err)
		release()
		pool.mu.Lock()
		for _, sr := range pool.readers {
			pool.removeReaderLocked(sr)
		}
		pool.mu.Unlock()
		require.Equal(t, PreloadRunning, preloadState(pool, to.InfoHash()), "a running preload is not released")

		writePieces(t, to, data, allPieces(to)...)
		require.Eventually(t, func() bool {
			_, ok := pool.PreloadStatus(to.InfoHash())
			return !ok
		}, 5*time.Second, time.Millisecond, "a preload whose playback already ended must not stay cached")
	})

	t.Run("a preload requested during playback counts as read", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		_, release, err := pool.Acquire(context.Background(), file, MemoryStorage)
		require.NoError(t, err)
		defer release()
		_, err = pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		pool.mu.Lock()
		read := pool.preloads[to.InfoHash()].fileRead
		pool.mu.Unlock()
		assert.True(t, read)
	})

	t.Run("keeps a final state for the TTL", func(t *testing.T) {
		pool, to, _ := newReadyPreload(t, time.Minute)
		pool.SetReadaheadBudget(0)
		require.Equal(t, PreloadEvicted, preloadState(pool, to.InfoHash()))
		pool.expirePreloads()
		assert.Equal(t, PreloadEvicted, preloadState(pool, to.InfoHash()))

		idleFor(pool, to.InfoHash(), time.Minute)
		pool.expirePreloads()
		_, ok := pool.PreloadStatus(to.InfoHash())
		assert.False(t, ok)
	})

	t.Run("a negative TTL keeps ready preloads", func(t *testing.T) {
		pool, to, _ := newReadyPreload(t, -1)
		idleFor(pool, to.InfoHash(), 24*time.Hour)
		pool.expirePreloads()
		assert.Equal(t, PreloadReady, preloadState(pool, to.InfoHash()))
	})
}

func TestPreloadStallTimeout(t *testing.T) {
	stalledFor := func(p *Pool, infoHash metainfo.Hash, stalled time.Duration) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.preloads[infoHash].progressAt = time.Now().Add(-stalled)
	}

	t.Run("fails a stalled preload and frees its slot", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger(), PreloadStallTimeout: time.Minute})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1500)
		stalled, stalledFile := addSizedTorrent(t, c, "stalled", 64, 640)
		waiting, waitingFile := addSizedTorrent(t, c, "waiting", 64, 640)
		for _, file := range []*torrent.File{stalledFile, waitingFile} {
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		require.Equal(t, PreloadQueued, preloadState(pool, waiting.InfoHash()), "only one reservation fits")

		pool.expirePreloads()
		assert.Equal(t, PreloadRunning, preloadState(pool, stalled.InfoHash()))
		stalledFor(pool, stalled.InfoHash(), time.Minute)
		pool.expirePreloads()
		assert.Equal(t, PreloadFailed, preloadState(pool, stalled.InfoHash()))
		assert.Empty(t, preloadClaims(pool, stalled))
		assert.Equal(t, PreloadRunning, preloadState(pool, waiting.InfoHash()), "the freed memory admits the queued preload")
	})

	t.Run("progress restarts the timer", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger(), PreloadStallTimeout: time.Minute})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		to, file, data := addHashedTorrent(t, c, "movie")
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		// Just short of the timeout, so the pool's own expiry tick cannot
		// fail the preload while the piece arrives.
		stalledFor(pool, to.InfoHash(), time.Minute-10*time.Second)

		writePieces(t, to, data, 0)
		require.Eventually(t, func() bool {
			status, _ := pool.PreloadStatus(to.InfoHash())
			return status.CompletedBytes == 64
		}, 5*time.Second, time.Millisecond)
		pool.mu.Lock()
		progressAt := pool.preloads[to.InfoHash()].progressAt
		pool.mu.Unlock()
		assert.WithinDuration(t, time.Now(), progressAt, 5*time.Second, "a completed piece must restart the timer")
		pool.expirePreloads()
		assert.Equal(t, PreloadRunning, preloadState(pool, to.InfoHash()))
	})

	t.Run("progress of other pieces of its torrent restarts the timer", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger(), PreloadStallTimeout: time.Minute})
		t.Cleanup(pool.Close)
		// The preload share holds only the first three 64-byte pieces.
		pool.SetReadaheadBudget(400)
		to, file, data := addHashedTorrent(t, c, "movie")
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		pool.mu.Lock()
		require.Equal(t, []int{0, 1, 2}, pool.preloads[to.InfoHash()].pieces)
		pool.mu.Unlock()

		// Another viewer's playback completes a piece the preload waits
		// behind, after the preload last saw progress an hour ago. The
		// timer is rewound together with the progress it last saw, so an
		// expiry tick of the pool itself cannot fail the preload first.
		writePieces(t, to, data, 8)
		pool.mu.Lock()
		pl := pool.preloads[to.InfoHash()]
		pl.progressAt = time.Now().Add(-time.Hour)
		pl.torrentCompleted = 0
		pool.mu.Unlock()
		pool.expirePreloads()
		assert.Equal(t, PreloadRunning, preloadState(pool, to.InfoHash()), "a torrent still downloading is not stalled")

		stalledFor(pool, to.InfoHash(), time.Hour)
		pool.expirePreloads()
		assert.Equal(t, PreloadFailed, preloadState(pool, to.InfoHash()), "without further progress the preload stalls")
	})

	t.Run("a negative timeout disables it", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger(), PreloadStallTimeout: -1})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		stalledFor(pool, to.InfoHash(), 24*time.Hour)
		pool.expirePreloads()
		assert.Equal(t, PreloadRunning, preloadState(pool, to.InfoHash()))
	})
}

func TestPool_PlanPreloadLocked(t *testing.T) {
	const mib = int64(1 << 20)

	t.Run("fits whole pieces within the memory limit", func(t *testing.T) {
		for _, tt := range []struct {
			name        string
			length      int64
			pieceLength int64
		}{
			// The final piece holds a single byte, so the tail range touches an
			// extra piece that byte-sized ranges would not account for.
			{name: "partial final piece", length: 100*mib + 1, pieceLength: 4 * mib},
			{name: "piece larger than boundary", length: 1<<30 + 12345, pieceLength: 16 * mib},
			{name: "small pieces", length: 700*mib + 3, pieceLength: 512 << 10},
		} {
			t.Run(tt.name, func(t *testing.T) {
				c := newTestTorrentClient(t)
				_, file := addSizedTorrent(t, c, tt.name, tt.pieceLength, tt.length)
				pool := newTestPool(t, Config{Logger: testLogger()})
				pool.SetReadaheadBudget(48 * mib)

				pool.mu.Lock()
				pl, ok := pool.planPreloadLocked(file, MemoryStorage, 0)
				pool.mu.Unlock()
				require.True(t, ok)
				limit := preloadMemoryLimit(pool.PreloadCapacity())
				assert.LessOrEqual(t, pl.reservation.bytes, limit)
				assert.Positive(t, pl.targetBytes)
				assert.LessOrEqual(t, pl.targetBytes, pl.reservation.bytes)
			})
		}
	})

	t.Run("file storage ignores the memory limit", func(t *testing.T) {
		c := newTestTorrentClient(t)
		_, file := addSizedTorrent(t, c, "movie", 16*mib, 1<<30)
		pool := newTestPool(t, Config{Logger: testLogger()})
		pool.SetReadaheadBudget(16 * mib)

		pool.mu.Lock()
		_, fits := pool.planPreloadLocked(file, MemoryStorage, 0)
		pl, ok := pool.planPreloadLocked(file, FileStorage, 0)
		pool.mu.Unlock()
		assert.False(t, fits, "one 16 MiB piece exceeds the memory preload share")
		require.True(t, ok)
		assert.Equal(t, int64(maxPreloadBytes), pl.targetBytes)
	})

	t.Run("plans a resume window at a playback position", func(t *testing.T) {
		for _, tt := range []struct {
			name         string
			length       int64
			pieceLength  int64
			wantHeadEnd  int64
			wantPosition time.Duration
			wantTarget   int64
			windowPieces int
		}{
			{name: "one-piece boundaries", length: 1 << 30, pieceLength: mib, wantHeadEnd: mib, wantPosition: time.Minute, wantTarget: 32 * mib, windowPieces: 30},
			// A one-piece head and tail take the whole budget, so the preload
			// falls back to its ordinary head and tail.
			{name: "no room for a window", length: 1 << 30, pieceLength: 16 * mib, wantHeadEnd: 16 * mib, wantTarget: 32 * mib},
			{name: "file covered whole", length: 10 * mib, pieceLength: mib, wantHeadEnd: 2 * mib, wantTarget: 10 * mib},
		} {
			t.Run(tt.name, func(t *testing.T) {
				c := newTestTorrentClient(t)
				_, file := addSizedTorrent(t, c, tt.name, tt.pieceLength, tt.length)
				pool := newTestPool(t, Config{Logger: testLogger()})

				pool.mu.Lock()
				pl, ok := pool.planPreloadLocked(file, FileStorage, time.Minute)
				pool.mu.Unlock()
				require.True(t, ok)
				assert.Equal(t, tt.wantPosition, pl.position)
				assert.Equal(t, tt.windowPieces, pl.windowPieces)
				assert.Equal(t, tt.wantHeadEnd, pl.headEnd)
				assert.Equal(t, tt.wantTarget, pl.targetBytes)
			})
		}
	})

	t.Run("shares capacity between concurrent preloads", func(t *testing.T) {
		for _, tt := range []struct {
			budget       int64
			secondActive bool
		}{
			{budget: 40 * mib, secondActive: false},
			{budget: 80 * mib, secondActive: true},
		} {
			t.Run(fmt.Sprintf("%d MiB", tt.budget/mib), func(t *testing.T) {
				c := newTestTorrentClient(t)
				pool := New(Config{Logger: testLogger()})
				t.Cleanup(pool.Close)
				pool.SetReadaheadBudget(tt.budget)
				_, firstFile := addSizedTorrent(t, c, "first", mib, 2<<30)
				second, secondFile := addSizedTorrent(t, c, "second", mib, 2<<30+1)
				for _, file := range []*torrent.File{firstFile, secondFile} {
					_, err := pool.Preload(file, MemoryStorage)
					require.NoError(t, err)
				}
				want := PreloadQueued
				if tt.secondActive {
					want = PreloadRunning
				}
				assert.Equal(t, want, preloadState(pool, second.InfoHash()), "a preload that does not fit must wait, not be dropped")
			})
		}
	})
}

func TestPool_PreloadCapacity(t *testing.T) {
	const mib = int64(1 << 20)
	for _, tt := range []struct {
		budget, want int64
	}{
		{budget: 0, want: 0},
		{budget: 32 * mib, want: 16 * mib},   // half kept for playback
		{budget: 256 * mib, want: 240 * mib}, // reserve capped at two boundaries
	} {
		p := newTestPool(t, Config{Logger: testLogger()})
		p.SetReadaheadBudget(tt.budget)
		assert.Equal(t, tt.want, p.PreloadCapacity(), "budget %d", tt.budget)
		// The whole capacity must be reservable by a single preload.
		assert.True(t, holdPreload(p, metainfo.Hash{1}, nil, preloadReservation{bytes: tt.want}), "budget %d", tt.budget)
	}
}

func TestPool_ReservePreloadLocked(t *testing.T) {
	t.Run("caps aggregate reservations", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		p.SetReadaheadBudget(1000)

		require.True(t, holdPreload(p, metainfo.Hash{1}, nil, preloadReservation{bytes: 300}))
		assert.False(t, holdPreload(p, metainfo.Hash{2}, nil, preloadReservation{bytes: 300}), "only 200 bytes of the preload share are left")
		require.True(t, holdPreload(p, metainfo.Hash{2}, nil, preloadReservation{bytes: 200}))
		p.mu.Lock()
		available := p.planReadaheadLocked().share
		p.mu.Unlock()
		assert.Equal(t, int64(500), available, "playback keeps the rest of the budget")
	})

	t.Run("returns released capacity to readers", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		p.SetReadaheadBudget(1200)
		for i := uint64(1); i <= 3; i++ {
			p.readers[i] = &streamReader{
				active:   true,
				infoHash: metainfo.Hash{byte(i)},
				readerID: i,
				reader:   &mockReader{},
			}
		}

		preloadHash := metainfo.Hash{9}
		require.True(t, holdPreload(p, preloadHash, nil, preloadReservation{bytes: 600}))
		for _, sr := range p.readers {
			assert.Equal(t, int64(160), sr.readahead, "readers share the budget left by the preload")
		}
		p.CancelPreload(preloadHash)
		for _, sr := range p.readers {
			assert.Equal(t, int64(320), sr.readahead, "readers get the released capacity back")
		}
	})

	t.Run("skips reader boundaries a preload covers", func(t *testing.T) {
		const (
			budget      = int64(32 << 20)
			pieceLength = int64(1 << 20)
			length      = int64(8 << 30)
		)
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie.mkv", pieceLength, length)
		reg := newProtectionRegistry()
		pool := New(Config{Registry: reg, Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(budget)
		_, release, err := pool.Acquire(context.Background(), file, MemoryStorage)
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

		// A preload of another file of the torrent protects nothing this reader
		// needs, so the reader keeps its boundaries and pays for the reservation.
		require.True(t, holdPreload(pool, to.InfoHash(), &torrent.File{}, preloadReservation{bytes: 16 << 20, coversBoundaries: true}))
		assert.Equal(t, 1, boundaries())
		withOtherPreload := readahead()
		pool.CancelPreload(to.InfoHash())

		// A head-only preload of the playing file leaves its tail unprotected.
		require.True(t, holdPreload(pool, to.InfoHash(), file, preloadReservation{bytes: 16 << 20}))
		assert.Equal(t, 1, boundaries(), "a head-only preload must not suppress the tail boundary")
		pool.CancelPreload(to.InfoHash())

		// A preload of the playing file that reaches both ends protects them.
		status, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		require.Equal(t, PreloadRunning, status.State)
		assert.Zero(t, boundaries(), "the reservation replaces the reader's boundaries")
		assert.Greater(t, readahead(), withOtherPreload, "boundaries must not be charged twice")

		pool.CancelPreload(to.InfoHash())
		assert.Equal(t, 1, boundaries(), "releasing the reservation restores the reader's boundaries")
	})
}

func TestPreloadCoversBoundaries(t *testing.T) {
	tests := []struct {
		name                        string
		headEnd, tailStart, tailEnd int64
		want                        bool
	}{
		{name: "head and tail", headEnd: 10, tailStart: 90, tailEnd: 100, want: true},
		{name: "head only", headEnd: 10, want: false},
		{name: "head spans whole file", headEnd: 100, want: true},
		{name: "tail short of file end", headEnd: 10, tailStart: 80, tailEnd: 90, want: false},
		{name: "nothing protected", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, preloadCoversBoundaries(100, tt.headEnd, tt.tailStart, tt.tailEnd))
		})
	}
}

func TestCompletedPieces(t *testing.T) {
	c := newTestTorrentClient(t)
	to, _, data := addHashedTorrent(t, c, "completed")
	writePieces(t, to, data, 0, 1, 4, 9)

	assert.Equal(t, map[int]bool{0: true, 2: false, 3: false, 4: true, 9: true}, completedPieces(to, []int{0, 2, 3, 4, 9}))
	assert.Empty(t, completedPieces(to, nil))
}

func TestWaitForPieceChange(t *testing.T) {
	c := newTestTorrentClient(t)
	to, _, _ := addHashedTorrent(t, c, "changes")
	change := func(index int, complete bool) torrent.PieceStateChange {
		var state torrent.PieceState
		state.Complete = complete
		return torrent.PieceStateChange{Index: index, PieceState: state}
	}

	t.Run("records a burst of watched changes", func(t *testing.T) {
		changes := make(chan torrent.PieceStateChange, 4)
		changes <- change(7, true) // not watched
		changes <- change(1, true)
		changes <- change(2, true)
		changes <- change(1, false)
		complete := map[int]bool{1: false, 2: false}

		require.True(t, waitForPieceChange(context.Background(), to, changes, complete, nil))
		assert.Equal(t, map[int]bool{1: false, 2: true}, complete, "later changes must win and unwatched pieces stay untracked")
		assert.Empty(t, changes, "the burst must be drained")
	})

	t.Run("ends when the subscription closes", func(t *testing.T) {
		changes := make(chan torrent.PieceStateChange)
		close(changes)
		assert.False(t, waitForPieceChange(context.Background(), to, changes, map[int]bool{}, nil))
	})

	t.Run("ends with its context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.False(t, waitForPieceChange(ctx, to, make(chan torrent.PieceStateChange), map[int]bool{}, nil))
	})
}

func TestCompletedRangeBytes(t *testing.T) {
	complete := map[int]bool{0: true, 2: true}
	pieceComplete := func(index int) bool { return complete[index] }
	tests := []struct {
		name                   string
		fileOffset, start, end int64
		want                   int64
	}{
		{name: "empty range", start: 10, end: 10, want: 0},
		{name: "whole complete piece", start: 0, end: 100, want: 100},
		{name: "spans a missing piece", start: 50, end: 250, want: 50 + 50},
		{name: "file offset shifts pieces", fileOffset: 100, start: 0, end: 200, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, completedRangeBytes(pieceComplete, 100, tt.fileOffset, tt.start, tt.end))
		})
	}
}

func TestPreloadMemoryLimit(t *testing.T) {
	const mib = int64(1 << 20)
	for _, tt := range []struct {
		capacity, want int64
	}{
		{capacity: 0, want: 0},
		{capacity: 8 * mib, want: 8 * mib},    // too small to share
		{capacity: 40 * mib, want: 20 * mib},  // shared by two preloads
		{capacity: 256 * mib, want: 32 * mib}, // capped at the startup limit
	} {
		assert.Equal(t, tt.want, preloadMemoryLimit(tt.capacity), "capacity %d", tt.capacity)
	}
}

func TestPool_PreloadAt(t *testing.T) {
	// A 512-byte budget leaves a 256-byte preload limit, four of the ten
	// 64-byte pieces: a 64-byte head and tail, and two pieces for the window.
	const budget, position = 512, 30 * time.Second
	type seekCall struct {
		header   []byte
		position time.Duration
	}
	newPool := func(t *testing.T, reg ProtectionRegistry, offset int64, ok bool) (*Pool, chan seekCall) {
		t.Helper()
		calls := make(chan seekCall, 1)
		pool := New(Config{
			Logger:   testLogger(),
			Registry: reg,
			SeekIndex: func(r io.ReaderAt, _ *torrent.File, position time.Duration) (int64, bool, error) {
				header := make([]byte, 8)
				if _, err := r.ReadAt(header, 0); err != nil {
					return 0, false, err
				}
				calls <- seekCall{header: header, position: position}
				return offset, ok, nil
			},
		})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(budget)
		return pool, calls
	}
	protected := func(reg *protectionRegistry) []int {
		return slices.Sorted(maps.Keys(reg.protectedPieces()))
	}
	claimed := func(pool *Pool, to *torrent.Torrent) []int {
		return slices.Sorted(maps.Keys(preloadClaims(pool, to)))
	}

	t.Run("places its window at the resolved offset before the head and tail are cached", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "movie")
		reg := newProtectionRegistry()
		// Offset 320 starts the 128-byte window 16 bytes earlier, in piece 4.
		pool, calls := newPool(t, reg, 320, true)

		status, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		assert.Equal(t, PreloadStatus{FilePath: file.Path(), Position: position, State: PreloadRunning, TargetBytes: 256}, status)
		assert.Equal(t, []int{0, 9}, claimed(pool, to), "the window waits for its seek index")
		assert.Equal(t, []int{0, 9}, protected(reg))

		// The seek index reads only the first piece, so the window is placed
		// while the tail still downloads.
		writePieces(t, to, data, 0)
		var call seekCall
		select {
		case call = <-calls:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the playback position was not resolved")
		}
		assert.Equal(t, position, call.position)
		assert.Equal(t, data[:8], call.header, "the seek index must read the file")
		require.Eventually(t, func() bool { return slices.Equal(claimed(pool, to), []int{0, 4, 5, 9}) }, 5*time.Second, time.Millisecond)
		assert.Equal(t, []int{0, 4, 5, 9}, protected(reg))

		writePieces(t, to, data, 4, 5)
		assert.Never(t, func() bool { return preloadState(pool, to.InfoHash()) == PreloadReady }, 50*time.Millisecond, time.Millisecond,
			"the tail must complete before the preload is ready")
		writePieces(t, to, data, 9)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
		status, _ = pool.PreloadStatus(to.InfoHash())
		assert.Equal(t, int64(256), status.TargetBytes)
		assert.Equal(t, []int{0, 4, 5, 9}, protected(reg), "a ready preload keeps its window protected")
	})

	t.Run("reports progress while it resolves the position", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "slow index")
		release := make(chan struct{})
		pool := New(Config{
			Logger: testLogger(),
			SeekIndex: func(io.ReaderAt, *torrent.File, time.Duration) (int64, bool, error) {
				<-release
				return 320, true, nil
			},
		})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(budget)

		_, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		writePieces(t, to, data, 0, 9)
		require.Eventually(t, func() bool {
			status, _ := pool.PreloadStatus(to.InfoHash())
			return status.CompletedBytes == 128
		}, 5*time.Second, time.Millisecond, "the head and tail must count while the index is read")
		assert.Equal(t, PreloadRunning, preloadState(pool, to.InfoHash()), "the window must be placed before the preload is ready")

		close(release)
		require.Eventually(t, func() bool { return slices.Equal(claimed(pool, to), []int{0, 4, 5, 9}) }, 5*time.Second, time.Millisecond)
		writePieces(t, to, data, 4, 5)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
	})

	t.Run("keeps the index pieces it read outside the head and tail", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "large index")
		reg := newProtectionRegistry()
		pool := New(Config{
			Logger:   testLogger(),
			Registry: reg,
			SeekIndex: func(r io.ReaderAt, _ *torrent.File, _ time.Duration) (int64, bool, error) {
				// An index that spans the first five pieces.
				if _, err := r.ReadAt(make([]byte, 300), 0); err != nil {
					return 0, false, err
				}
				return 320, true, nil
			},
		})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(budget)

		_, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		writePieces(t, to, data, 0, 1, 2, 3, 4)
		// Pieces 1 and 2 take both window pieces, so the window is gone, and
		// the reservation holds no more of the index.
		require.Eventually(t, func() bool { return slices.Equal(protected(reg), []int{0, 1, 2, 9}) }, 5*time.Second, time.Millisecond)
		assert.Equal(t, []int{0, 1, 2, 9}, claimed(pool, to))
		writePieces(t, to, data, 9)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
	})

	t.Run("keeps the piece of the offset in a one-piece window", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "one piece window")
		reg := newProtectionRegistry()
		pool := New(Config{
			Logger:   testLogger(),
			Registry: reg,
			SeekIndex: func(r io.ReaderAt, _ *torrent.File, _ time.Duration) (int64, bool, error) {
				// An index that spans the first two pieces leaves one window
				// piece. Offset 320 starts piece 5, and a window starting 8
				// bytes earlier would begin in piece 4.
				if _, err := r.ReadAt(make([]byte, 70), 0); err != nil {
					return 0, false, err
				}
				return 320, true, nil
			},
		})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(budget)

		_, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		writePieces(t, to, data, 0, 1)
		require.Eventually(t, func() bool { return slices.Equal(protected(reg), []int{0, 1, 5, 9}) }, 5*time.Second, time.Millisecond)
	})

	t.Run("keeps its window between the head and the tail", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file, data := addHashedTorrent(t, c, "late")
		reg := newProtectionRegistry()
		pool, _ := newPool(t, reg, 630, true)

		_, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		writePieces(t, to, data, 0, 9)
		require.Eventually(t, func() bool { return slices.Equal(claimed(pool, to), []int{0, 7, 8, 9}) }, 5*time.Second, time.Millisecond)
	})

	t.Run("places its window in its own file's pieces in a multi-file torrent", func(t *testing.T) {
		// The movie follows a 100-byte file, so its byte offsets are 100 bytes
		// short of the torrent's.
		const pieceLength, firstLength, movieLength = 64, 100, 640
		data := make([]byte, firstLength+movieLength)
		for i := range data {
			data[i] = byte(i * 7)
		}
		var pieces []byte
		for start := 0; start < len(data); start += pieceLength {
			sum := sha1.Sum(data[start:min(start+pieceLength, len(data))])
			pieces = append(pieces, sum[:]...)
		}
		infoBytes, err := bencode.Marshal(metainfo.Info{
			Name:        "season",
			PieceLength: pieceLength,
			Pieces:      pieces,
			Files: []metainfo.FileInfo{
				{Length: firstLength, Path: []string{"extras.srt"}},
				{Length: movieLength, Path: []string{"movie.mkv"}},
			},
		})
		require.NoError(t, err)
		c := newTestTorrentClient(t)
		to, _ := addTestTorrentFromMetaInfo(t, c, &metainfo.MetaInfo{InfoBytes: infoBytes})
		file := to.Files()[1]
		reg := newProtectionRegistry()
		// A 1 KiB budget leaves a 512-byte preload: the movie's first piece 1,
		// which it shares with the other file, its short last piece 11, and
		// six window pieces. File offset 320 would start the window 48 bytes
		// earlier, but the window must end where the tail starts, at file
		// offset 604, so it starts at file offset 220, in piece 5.
		pool, _ := newPool(t, reg, 320, true)
		pool.SetReadaheadBudget(1 << 10)

		_, err = pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 11}, claimed(pool, to))

		writePieces(t, to, data, 1)
		require.Eventually(t, func() bool {
			return slices.Equal(claimed(pool, to), []int{1, 5, 6, 7, 8, 9, 10, 11})
		}, 5*time.Second, time.Millisecond)
		assert.Equal(t, []int{1, 5, 6, 7, 8, 9, 10, 11}, protected(reg))

		writePieces(t, to, data, 5, 6, 7, 8, 9, 10, 11)
		waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
	})

	t.Run("is ready with its head and tail when the position cannot be resolved", func(t *testing.T) {
		for _, tt := range []struct {
			name      string
			seekIndex bool
		}{
			{name: "unresolved", seekIndex: true},
			{name: "no seek index", seekIndex: false},
		} {
			t.Run(tt.name, func(t *testing.T) {
				c := newTestTorrentClient(t)
				to, file, data := addHashedTorrent(t, c, tt.name)
				reg := newProtectionRegistry()
				pool, _ := newPool(t, reg, 0, false)
				if !tt.seekIndex {
					pool.cfg.SeekIndex = nil
				}

				_, err := pool.PreloadAt(file, MemoryStorage, position)
				require.NoError(t, err)
				writePieces(t, to, data, 0, 9)
				waitForPreloadState(t, pool, to.InfoHash(), PreloadReady)
				status, _ := pool.PreloadStatus(to.InfoHash())
				assert.Equal(t, int64(128), status.TargetBytes)
				assert.Equal(t, int64(128), status.CompletedBytes)
				assert.Equal(t, []int{0, 9}, protected(reg))
			})
		}
	})

	t.Run("keeps a preload that a position without a window would plan the same", func(t *testing.T) {
		c := newTestTorrentClient(t)
		// A 200-byte file fits the 256-byte preload whole.
		to, file := addSizedTorrent(t, c, "small", 64, 200)
		pool, _ := newPool(t, nil, 0, false)

		_, err := pool.Preload(file, MemoryStorage)
		require.NoError(t, err)
		pool.mu.Lock()
		first := pool.preloads[to.InfoHash()]
		pool.mu.Unlock()

		status, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		assert.Zero(t, status.Position)
		pool.mu.Lock()
		assert.Same(t, first, pool.preloads[to.InfoHash()])
		pool.mu.Unlock()
	})

	t.Run("replaces a preload at another position", func(t *testing.T) {
		c := newTestTorrentClient(t)
		to, file := addSizedTorrent(t, c, "movie", 64, 640)
		pool, _ := newPool(t, nil, 0, false)

		_, err := pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		pool.mu.Lock()
		first := pool.preloads[to.InfoHash()]
		pool.mu.Unlock()

		_, err = pool.PreloadAt(file, MemoryStorage, position)
		require.NoError(t, err)
		pool.mu.Lock()
		assert.Same(t, first, pool.preloads[to.InfoHash()], "a request at the same position keeps the preload")
		pool.mu.Unlock()

		_, err = pool.PreloadAt(file, MemoryStorage, 2*position)
		require.NoError(t, err)
		pool.mu.Lock()
		assert.NotSame(t, first, pool.preloads[to.InfoHash()])
		pool.mu.Unlock()
	})
}
