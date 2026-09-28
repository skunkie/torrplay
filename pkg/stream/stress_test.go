//go:build stress

// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"math/rand/v2"
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
	"github.com/torrplay/torrplay/pkg/storage"
)

const (
	enginePieceLength = 64 << 10
	enginePieces      = 512 // 32 MiB files
	// engineSwarmTick and engineSwarmPieces make the simulated swarm deliver
	// 4 pieces every 10 ms, 25 MiB/s shared by every torrent.
	engineSwarmTick   = 10 * time.Millisecond
	engineSwarmPieces = 4
)

// engine is a torrent client whose pieces come from a simulated swarm instead
// of peers. The swarm has a fixed bandwidth shared by every torrent and, like
// the peers the piece picker drives, downloads the highest-priority missing
// pieces first, so what the pool claims decides what arrives when.
type engine struct {
	client   *torrent.Client
	storage  *storage.Client
	registry *ownerRegistry

	mu       sync.Mutex
	torrents []*engineTorrent
	written  atomic.Int64
}

type engineTorrent struct {
	to   *torrent.Torrent
	file *torrent.File
	data []byte
}

func newEngine(t *testing.T, memoryLimit int64) *engine {
	t.Helper()
	storageClient := storage.New(memoryLimit, testLogger())
	cfg := torrent.NewDefaultClientConfig()
	cfg.DefaultStorage = storageClient
	cfg.DisablePEX = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.DisableTrackers = true
	cfg.DisableWebseeds = true
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = false
	client, err := torrent.NewClient(cfg)
	require.NoError(t, err)
	storageClient.SetEvictionHandler(storage.ClientEvictionHandler(client))
	e := &engine{client: client, storage: storageClient, registry: &ownerRegistry{inner: storageClient, owners: make(map[uint64]struct{})}}

	stop := make(chan struct{})
	var swarm sync.WaitGroup
	swarm.Go(func() { e.runSwarm(stop) })
	t.Cleanup(func() {
		close(stop)
		swarm.Wait()
		for _, err := range client.Close() {
			assert.NoError(t, err)
		}
		assert.NoError(t, storageClient.Close())
	})
	return e
}

func (e *engine) addTorrent(t *testing.T, name string, seed uint64) *engineTorrent {
	t.Helper()
	data := make([]byte, enginePieces*enginePieceLength)
	rng := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // Seeded, reproducible test data.
	for i := 0; i < len(data); i += 8 {
		v := rng.Uint64()
		for j := range 8 {
			data[i+j] = byte(v >> (8 * j))
		}
	}
	pieces := make([]byte, 0, enginePieces*sha1.Size)
	for offset := 0; offset < len(data); offset += enginePieceLength {
		sum := sha1.Sum(data[offset : offset+enginePieceLength])
		pieces = append(pieces, sum[:]...)
	}
	infoBytes, err := bencode.Marshal(metainfo.Info{Name: name, PieceLength: enginePieceLength, Length: int64(len(data)), Pieces: pieces})
	require.NoError(t, err)
	to, file := addTestTorrentFromMetaInfo(t, e.client, &metainfo.MetaInfo{InfoBytes: infoBytes})
	et := &engineTorrent{to: to, file: file, data: data}
	e.mu.Lock()
	e.torrents = append(e.torrents, et)
	e.mu.Unlock()
	return et
}

// runSwarm delivers the highest-priority missing pieces of every torrent,
// engineSwarmPieces per tick, until stop closes.
func (e *engine) runSwarm(stop <-chan struct{}) {
	ticker := time.NewTicker(engineSwarmTick)
	defer ticker.Stop()
	type wanted struct {
		et       *engineTorrent
		index    int
		priority torrent.PiecePriority
	}
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		e.mu.Lock()
		torrents := slices.Clone(e.torrents)
		e.mu.Unlock()
		var candidates []wanted
		for _, et := range torrents {
			select {
			case <-et.to.Closed():
				continue
			default:
			}
			for index := range enginePieces {
				state := et.to.PieceState(index)
				if !state.Complete && state.Priority > torrent.PiecePriorityNone {
					candidates = append(candidates, wanted{et: et, index: index, priority: state.Priority})
				}
			}
		}
		slices.SortFunc(candidates, func(a, b wanted) int {
			return cmp.Or(cmp.Compare(b.priority, a.priority), cmp.Compare(a.index, b.index))
		})
		for _, c := range candidates[:min(len(candidates), engineSwarmPieces)] {
			start := c.index * enginePieceLength
			piece := c.et.to.Piece(c.index)
			if _, err := piece.Storage().WriteAt(c.et.data[start:start+enginePieceLength], 0); err != nil {
				continue
			}
			_ = piece.VerifyData()
			e.written.Add(1)
		}
	}
}

// ownerRegistry forwards to storage and tracks the protection owners the pool
// holds, so a test can check that none leak.
type ownerRegistry struct {
	inner  ProtectionRegistry
	mu     sync.Mutex
	owners map[uint64]struct{}
}

func (r *ownerRegistry) SetProtection(infoHash metainfo.Hash, ownerID uint64, protection storage.Protection) {
	r.mu.Lock()
	r.owners[ownerID] = struct{}{}
	r.mu.Unlock()
	r.inner.SetProtection(infoHash, ownerID, protection)
}

func (r *ownerRegistry) ClearProtection(infoHash metainfo.Hash, ownerID uint64) {
	r.mu.Lock()
	delete(r.owners, ownerID)
	r.mu.Unlock()
	r.inner.ClearProtection(infoHash, ownerID)
}

func (r *ownerRegistry) ownerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.owners)
}

// readAt reads length bytes at offset through a new reader of et and checks
// them against the file, returning the time to the first byte.
func readAt(t *testing.T, pool *Pool, et *engineTorrent, offset, length int64) time.Duration {
	t.Helper()
	reader, release, err := pool.Acquire(context.Background(), et.file, MemoryStorage)
	require.NoError(t, err)
	defer release()
	_, err = reader.Seek(offset, io.SeekStart)
	require.NoError(t, err)
	buf := make([]byte, length)
	start := time.Now()
	_, err = io.ReadFull(reader, buf[:1])
	require.NoError(t, err)
	ttfb := time.Since(start)
	_, err = io.ReadFull(reader, buf[1:])
	require.NoError(t, err)
	require.True(t, bytes.Equal(et.data[offset:offset+length], buf), "bytes at %d must match the file", offset)
	return ttfb
}

// TestEngineSeekLatency seeks through a file while two other torrents
// preload, and checks that playback's piece priorities keep the time to the
// first byte after a seek within a few swarm ticks, whatever the preloads
// ask for.
func TestEngineSeekLatency(t *testing.T) {
	e := newEngine(t, 128<<20)
	pool := New(Config{Logger: testLogger(), PriorityWindowFraction: 0.15, Registry: e.registry})
	t.Cleanup(pool.Close)
	// Enough for both preloads to download at once.
	pool.SetReadaheadBudget(64 << 20)

	played := e.addTorrent(t, "played", 1)
	preloaded := make([]*engineTorrent, 0, 2)
	for i, name := range []string{"preload-a", "preload-b"} {
		et := e.addTorrent(t, name, uint64(i+2))
		_, err := pool.Preload(et.file, MemoryStorage)
		require.NoError(t, err)
		preloaded = append(preloaded, et)
	}

	// The swarm now spends every tick on preload pieces. Each seek lands on
	// pieces nobody has asked for yet.
	const bound = 25 * engineSwarmTick
	var worst time.Duration
	for _, fraction := range []float64{0.5, 0.1, 0.8, 0.3, 0.95, 0.6} {
		offset := int64(fraction*enginePieces) * enginePieceLength
		ttfb := readAt(t, pool, played, offset, 256<<10)
		t.Logf("seek to %2.0f%%: first byte after %v", 100*fraction, ttfb.Round(time.Millisecond))
		worst = max(worst, ttfb)
	}
	assert.Less(t, worst, bound, "playback must outrank preloads after a seek")

	for _, et := range preloaded {
		waitForPreloadState(t, pool, et.to.InfoHash(), PreloadReady)
	}
}

// TestEngineSoak runs viewers, preloads, cancellations, and budget changes
// against three torrents at once for STRESS_DURATION (default 20 s), checks
// every byte read, and then checks that the pool returns to empty: no
// readers, preloads, priority claims, or protection left behind.
func TestEngineSoak(t *testing.T) {
	duration := 20 * time.Second
	if value := os.Getenv("STRESS_DURATION"); value != "" {
		parsed, err := time.ParseDuration(value)
		require.NoError(t, err)
		duration = parsed
	}

	const memoryLimit = 24 << 20
	e := newEngine(t, memoryLimit)
	pool := New(Config{
		Logger:                 testLogger(),
		LingerTimeout:          300 * time.Millisecond,
		MemoryUsage:            func() float64 { return float64(e.storage.MemoryStats().UsedBytes) / memoryLimit },
		PreloadReadyTTL:        2 * time.Second,
		PreloadStallTimeout:    3 * time.Second,
		PriorityWindowFraction: 0.15,
		Registry:               e.registry,
	})
	t.Cleanup(pool.Close)
	pool.SetReadaheadBudget(memoryLimit / 2)
	torrents := []*engineTorrent{e.addTorrent(t, "a", 11), e.addTorrent(t, "b", 12), e.addTorrent(t, "c", 13)}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	var workers sync.WaitGroup
	var reads, preloads, cancels, budgets atomic.Int64
	failures := make(chan error, 16)
	fail := func(err error) {
		select {
		case failures <- err:
		default:
		}
		cancel()
	}

	// Viewers open a file at a random point, read a few ranges, sometimes
	// seek, and stop, like players opening and scrubbing.
	for viewer := range 3 {
		workers.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(viewer), 99)) //nolint:gosec // Seeded, reproducible test workload.
			for ctx.Err() == nil {
				et := torrents[rng.IntN(len(torrents))]
				reader, release, err := pool.Acquire(ctx, et.file, MemoryStorage)
				if err != nil {
					fail(fmt.Errorf("acquire: %w", err))
					return
				}
				offset := rng.Int64N(int64(len(et.data)) - 1<<20)
				for range 1 + rng.IntN(4) {
					if rng.IntN(3) == 0 {
						offset = rng.Int64N(int64(len(et.data)) - 1<<20)
					}
					if _, err := reader.Seek(offset, io.SeekStart); err != nil {
						fail(fmt.Errorf("seek: %w", err))
						break
					}
					buf := make([]byte, 64<<10+rng.IntN(256<<10))
					n, err := io.ReadFull(reader, buf)
					if err != nil {
						if ctx.Err() == nil {
							fail(fmt.Errorf("read at %d: %w", offset, err))
						}
						break
					}
					if !bytes.Equal(et.data[offset:offset+int64(n)], buf[:n]) {
						fail(fmt.Errorf("wrong bytes at %d of %s", offset, et.file.Path()))
						break
					}
					reads.Add(1)
					offset += int64(n)
				}
				release()
				time.Sleep(time.Duration(rng.IntN(200)) * time.Millisecond)
			}
		})
	}
	// Preloads come and go.
	workers.Go(func() {
		rng := rand.New(rand.NewPCG(7, 7)) //nolint:gosec // Seeded, reproducible test workload.
		for ctx.Err() == nil {
			et := torrents[rng.IntN(len(torrents))]
			if rng.IntN(4) == 0 {
				pool.CancelPreload(et.to.InfoHash())
				cancels.Add(1)
			} else if _, err := pool.Preload(et.file, MemoryStorage); err == nil {
				preloads.Add(1)
			}
			time.Sleep(time.Duration(100+rng.IntN(400)) * time.Millisecond)
		}
	})
	// The memory limit setting changes.
	workers.Go(func() {
		rng := rand.New(rand.NewPCG(8, 8)) //nolint:gosec // Seeded, reproducible test workload.
		for ctx.Err() == nil {
			pool.SetReadaheadBudget(int64(4+rng.IntN(12)) << 20)
			budgets.Add(1)
			time.Sleep(time.Duration(500+rng.IntN(1000)) * time.Millisecond)
		}
	})
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}

	for _, et := range torrents {
		pool.CancelPreload(et.to.InfoHash())
	}
	require.Eventually(t, func() bool { return pool.StreamingTorrentCount() == 0 }, 10*time.Second, 10*time.Millisecond,
		"every lingering reader must close")
	pool.mu.Lock()
	readers, preloadCount, claims := len(pool.readers), len(pool.preloads), len(pool.priorityClaims)
	pool.mu.Unlock()
	assert.Zero(t, readers, "no reader may be left")
	assert.Zero(t, preloadCount, "no preload may be left")
	assert.Zero(t, claims, "no piece priority claim may be left")
	assert.Zero(t, e.registry.ownerCount(), "no protection may be left")
	for _, et := range torrents {
		for index := range enginePieces {
			assert.Equal(t, torrent.PiecePriorityNone, et.to.PieceState(index).Priority, "piece %d of %s keeps a priority", index, et.file.Path())
		}
	}
	counters := e.storage.Counters()
	t.Logf("soak %v: %d reads, %d preloads, %d cancels, %d budget changes, %d pieces downloaded; evicted %d complete and %d incomplete pieces, %d from active ranges, %d from boundaries; %d read misses",
		duration, reads.Load(), preloads.Load(), cancels.Load(), budgets.Load(), e.written.Load(),
		counters.EvictedCompletePieces, counters.EvictedIncompletePieces, counters.ActiveRangeEvictions, counters.BoundaryEvictions, counters.ReadMisses)
	assert.LessOrEqual(t, e.storage.MemoryStats().UsedBytes, int64(memoryLimit))
}
