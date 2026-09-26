// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"crypto/sha1"
	"fmt"
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
	pl := p.preloads[to.InfoHash()]
	p.mu.Unlock()
	p.priorityMu.Lock()
	defer p.priorityMu.Unlock()
	claims := make(map[int]torrent.PiecePriority)
	for key, claim := range p.priorityClaims {
		if priority, ok := claim.owners[pl]; ok && key.torrent == to {
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

// holdPreload registers a running memory preload that reserves size bytes
// without a file, for tests of the pool's budget accounting. It reports
// whether the reservation fit.
func holdPreload(p *Pool, infoHash metainfo.Hash, reservation preloadReservation) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl := &preload{file: &torrent.File{}, infoHash: infoHash, mode: MemoryStorage, reservation: reservation, state: PreloadRunning}
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
	newPool := func(t *testing.T, reg ActiveRangeRegistry) *Pool {
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
		pool.priorityMu.Lock()
		claimedPieces := len(pool.priorityClaims)
		pool.priorityMu.Unlock()
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
		pool.priorityMu.Lock()
		assert.Empty(t, pool.priorityClaims)
		pool.priorityMu.Unlock()
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

func TestPool_PreloadScheduling(t *testing.T) {
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
		_, release, err := pool.Acquire(readyFile, MemoryStorage)
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
		_, release, err := pool.Acquire(playedFile, MemoryStorage)
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

func TestPool_SetReadaheadBudgetEvictsPreloads(t *testing.T) {
	t.Run("gives up the cheapest reservations first", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1 << 20)
		read, readFile, readData := addHashedTorrent(t, c, "read")
		idle, idleFile, idleData := addHashedTorrent(t, c, "idle")
		writePieces(t, read, readData, allPieces(read)...)
		writePieces(t, idle, idleData, allPieces(idle)...)
		running, runningFile := addSizedTorrent(t, c, "running", 64, 640)
		for _, file := range []*torrent.File{readFile, idleFile} {
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		waitForPreloadState(t, pool, read.InfoHash(), PreloadReady)
		waitForPreloadState(t, pool, idle.InfoHash(), PreloadReady)
		_, err := pool.Preload(runningFile, MemoryStorage)
		require.NoError(t, err)
		_, release, err := pool.Acquire(readFile, MemoryStorage)
		require.NoError(t, err)
		defer release()

		// Each budget's preload share holds one reservation fewer.
		for _, step := range []struct {
			budget  int64
			evicted metainfo.Hash
		}{
			{budget: 2 * 1300, evicted: idle.InfoHash()},
			{budget: 2 * 700, evicted: running.InfoHash()},
			{budget: 0, evicted: read.InfoHash()},
		} {
			pool.SetReadaheadBudget(step.budget)
			assert.Equal(t, PreloadEvicted, preloadState(pool, step.evicted))
		}
	})

	t.Run("a larger budget starts queued preloads", func(t *testing.T) {
		c := newTestTorrentClient(t)
		pool := New(Config{Logger: testLogger()})
		t.Cleanup(pool.Close)
		pool.SetReadaheadBudget(1500)
		_, firstFile := addSizedTorrent(t, c, "first", 64, 640)
		second, secondFile := addSizedTorrent(t, c, "second", 64, 640)
		for _, file := range []*torrent.File{firstFile, secondFile} {
			_, err := pool.Preload(file, MemoryStorage)
			require.NoError(t, err)
		}
		require.Equal(t, PreloadQueued, preloadState(pool, second.InfoHash()))

		pool.SetReadaheadBudget(1 << 20)
		assert.Equal(t, PreloadRunning, preloadState(pool, second.InfoHash()))
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
		pl.idleSince = time.Now().Add(-idle)
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

	t.Run("restarts the TTL while its file is read", func(t *testing.T) {
		pool, to, file := newReadyPreload(t, time.Minute)
		_, release, err := pool.Acquire(file, MemoryStorage)
		require.NoError(t, err)
		idleFor(pool, to.InfoHash(), time.Hour)
		pool.expirePreloads()
		assert.Equal(t, PreloadReady, preloadState(pool, to.InfoHash()), "a preload being read must not expire")

		// The TTL restarts from when its last reader closes.
		release()
		pool.mu.Lock()
		for key, sr := range pool.readers {
			pool.removeReaderLocked(key, sr)
		}
		idleSince := pool.preloads[to.InfoHash()].idleSince
		pool.mu.Unlock()
		assert.WithinDuration(t, time.Now(), idleSince, time.Second)
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
				pl, ok := pool.planPreloadLocked(file, MemoryStorage)
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
		_, fits := pool.planPreloadLocked(file, MemoryStorage)
		pl, ok := pool.planPreloadLocked(file, FileStorage)
		pool.mu.Unlock()
		assert.False(t, fits, "one 16 MiB piece exceeds the memory preload share")
		require.True(t, ok)
		assert.Equal(t, int64(maxPreloadBytes), pl.targetBytes)
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
		assert.True(t, holdPreload(p, metainfo.Hash{1}, preloadReservation{bytes: tt.want}), "budget %d", tt.budget)
	}
}

func TestPool_ReservePreloadLocked(t *testing.T) {
	t.Run("caps aggregate reservations", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		p.SetReadaheadBudget(1000)

		require.True(t, holdPreload(p, metainfo.Hash{1}, preloadReservation{bytes: 300}))
		assert.False(t, holdPreload(p, metainfo.Hash{2}, preloadReservation{bytes: 300}), "only 200 bytes of the preload share are left")
		require.True(t, holdPreload(p, metainfo.Hash{2}, preloadReservation{bytes: 200}))
		p.mu.Lock()
		available := p.availableProtectionBudgetLocked(p.readaheadBudget)
		p.mu.Unlock()
		assert.Equal(t, int64(500), available, "playback keeps the rest of the budget")
	})

	t.Run("returns released capacity to readers", func(t *testing.T) {
		p := newTestPool(t, Config{Logger: testLogger()})
		p.SetReadaheadBudget(1200)
		for i := uint64(1); i <= 3; i++ {
			p.readers[readerKey{infoHash: metainfo.Hash{byte(i)}, filePath: "f", readerID: i}] = &streamReader{
				active:   true,
				infoHash: metainfo.Hash{byte(i)},
				readerID: i,
				reader:   &mockReader{},
			}
		}

		preloadHash := metainfo.Hash{9}
		require.True(t, holdPreload(p, preloadHash, preloadReservation{bytes: 600}))
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

		// A preload of another file of the torrent protects nothing this reader
		// needs, so the reader keeps its boundaries and pays for the reservation.
		require.True(t, holdPreload(pool, to.InfoHash(), preloadReservation{bytes: 16 << 20, coversBoundaries: true, filePath: "other.mkv"}))
		assert.Equal(t, 1, boundaries())
		withOtherPreload := readahead()
		pool.CancelPreload(to.InfoHash())

		// A head-only preload of the playing file leaves its tail unprotected.
		require.True(t, holdPreload(pool, to.InfoHash(), preloadReservation{bytes: 16 << 20, filePath: file.Path()}))
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
