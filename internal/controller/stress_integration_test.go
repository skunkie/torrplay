//go:build integration && stress

// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
	"golang.org/x/time/rate"
)

const (
	stressFileSize    = 64 << 20
	stressPieceLength = 256 << 10
	stressMemoryLimit = 128 << 20
	// stressSeedRate is each webseed's upload rate, standing in for a swarm.
	stressSeedRate = 16 << 20
)

// throttledReadSeeker limits reads to rate bytes per second.
type throttledReadSeeker struct {
	*bytes.Reader
	rate int64
}

func (r *throttledReadSeeker) Read(p []byte) (int, error) {
	p = p[:min(len(p), 64<<10)]
	n, err := r.Reader.Read(p)
	time.Sleep(time.Duration(int64(n) * int64(time.Second) / r.rate))
	return n, err
}

// stressTorrent is a torrent served by its own throttled webseed.
type stressTorrent struct {
	hash  metainfo.Hash
	name  string
	files [][]byte
	// payload is the first file's data.
	payload []byte
}

// stressData returns size bytes of reproducible pseudorandom data.
func stressData(seed uint64, size int) []byte {
	data := make([]byte, size)
	rng := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // Seeded, reproducible test data.
	for i := 0; i < len(data); i += 8 {
		v := rng.Uint64()
		for j := 0; j < 8 && i+j < len(data); j++ {
			data[i+j] = byte(v >> (8 * j))
		}
	}
	return data
}

func addStressTorrent(t *testing.T, ctrl *Controller, name string, seed uint64) stressTorrent {
	t.Helper()
	return addStressTorrentFiles(t, ctrl, name, seed, stressFileSize)
}

// addStressTorrentFiles adds a torrent of one file per size, served by its own
// throttled webseed, and saves it with memory storage.
func addStressTorrentFiles(t *testing.T, ctrl *Controller, name string, seed uint64, sizes ...int) stressTorrent {
	t.Helper()
	files, infoBytes := stressInfo(t, name, seed, sizes...)
	var info metainfo.Info
	require.NoError(t, bencode.Unmarshal(infoBytes, &info))
	paths := make(map[string][]byte)
	for i := range info.Files {
		paths["/"+name+"/"+info.Files[i].Path[0]] = files[i]
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, &throttledReadSeeker{Reader: bytes.NewReader(data), rate: stressSeedRate})
	}))
	t.Cleanup(server.Close)

	meta := &metainfo.MetaInfo{InfoBytes: infoBytes, UrlList: metainfo.UrlList{server.URL + "/"}}
	_, _, err := ctrl.currentClient().AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(meta))
	require.NoError(t, err)
	st := stressTorrent{hash: meta.HashInfoBytes(), name: name, files: files, payload: files[0]}
	saveStressTorrent(t, ctrl, st, nil, api.Memory)
	return st
}

// stressInfo builds the info of a torrent of one file per size, returning the
// files' data and the bencoded info.
func stressInfo(t *testing.T, name string, seed uint64, sizes ...int) ([][]byte, []byte) {
	t.Helper()
	files := make([][]byte, 0, len(sizes))
	infoFiles := make([]metainfo.FileInfo, 0, len(sizes))
	var all []byte
	for i, size := range sizes {
		data := stressData(seed*16+uint64(i), size)
		files = append(files, data)
		infoFiles = append(infoFiles, metainfo.FileInfo{Length: int64(size), Path: []string{fmt.Sprintf("%s-%d.mkv", name, i)}})
		all = append(all, data...)
	}
	pieces := make([]byte, 0, (len(all)+stressPieceLength-1)/stressPieceLength*sha1.Size)
	for offset := 0; offset < len(all); offset += stressPieceLength {
		sum := sha1.Sum(all[offset:min(offset+stressPieceLength, len(all))])
		pieces = append(pieces, sum[:]...)
	}
	infoBytes, err := bencode.Marshal(metainfo.Info{Name: name, PieceLength: stressPieceLength, Pieces: pieces, Files: infoFiles})
	require.NoError(t, err)
	return files, infoBytes
}

// saveStressTorrent saves a torrent in the database with the given storage,
// and with its info when infoBytes is set, so the controller can load it from
// the database.
func saveStressTorrent(t *testing.T, ctrl *Controller, st stressTorrent, infoBytes []byte, storage api.TorrentStorage) {
	t.Helper()
	var total int64
	for _, file := range st.files {
		total += int64(len(file))
	}
	require.NoError(t, ctrl.db.CreateTorrent(&database.Torrent{
		Torrent: api.Torrent{
			Hash:      st.hash,
			Magnet:    utils.MagnetURIFromHash(st.hash),
			Name:      st.name,
			Title:     &st.name,
			Storage:   &storage,
			TotalSize: total,
		},
		InfoBytes: infoBytes,
	}))
}

// stressClient drives the controller's HTTP API.
type stressClient struct {
	t      *testing.T
	server *httptest.Server
}

func (c stressClient) do(ctx context.Context, method, path, body string, header http.Header) *http.Response {
	c.t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, c.server.URL+path, bytes.NewBufferString(body))
	require.NoError(c.t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	maps.Copy(req.Header, header)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	return resp
}

// expect sends a request and checks its status, discarding the body.
func (c stressClient) expect(method, path, body string, status int) {
	c.t.Helper()
	resp := c.do(context.Background(), method, path, body, nil)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(c.t, resp.Body.Close())
	require.Equal(c.t, status, resp.StatusCode, "%s %s", method, path)
}

func (c stressClient) preload(st stressTorrent, fileIndex int) {
	c.t.Helper()
	c.expect(http.MethodPut, fmt.Sprintf("/api/v1/torrents/%s/preload", st.hash), fmt.Sprintf(`{"file_index":%d}`, fileIndex), http.StatusOK)
}

func (c stressClient) preloadStatus(st stressTorrent) api.PreloadResponse {
	c.t.Helper()
	resp := c.do(context.Background(), http.MethodGet, fmt.Sprintf("/api/v1/torrents/%s/preload", st.hash), "", nil)
	defer resp.Body.Close()
	var status api.PreloadResponse
	require.NoError(c.t, json.NewDecoder(resp.Body).Decode(&status))
	return status
}

// readRange reads [start, end) of a torrent's file through the stream
// endpoint and checks the bytes.
func (c stressClient) readRange(ctx context.Context, st stressTorrent, fileIndex int, start, end int64) error {
	resp := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/stream/%s?index=%d", st.hash, fileIndex), "",
		http.Header{"Range": {fmt.Sprintf("bytes=%d-%d", start, end-1)}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("range %d-%d: status %d", start, end, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("range %d-%d: %w", start, end, err)
	}
	if !bytes.Equal(st.files[fileIndex][start:end], body) {
		return fmt.Errorf("range %d-%d: wrong bytes", start, end)
	}
	return nil
}

// waitForPreloadEnd waits until a preload is no longer queued or downloading
// and returns its final status.
func (c stressClient) waitForPreloadEnd(st stressTorrent, timeout time.Duration) api.PreloadResponse {
	c.t.Helper()
	var status api.PreloadResponse
	require.Eventually(c.t, func() bool {
		status = c.preloadStatus(st)
		return status.Status != api.Queued && status.Status != api.Preloading
	}, timeout, 50*time.Millisecond, "preload of %s must end", st.name)
	return status
}

// logStorage reports the storage counters and checks the memory limit.
func logStorage(t *testing.T, ctrl *Controller, limit int64) {
	t.Helper()
	storage := ctrl.storageClient.Load()
	counters := storage.Counters()
	t.Logf("memory: %d MiB of %d MiB used; evicted %d complete and %d incomplete pieces, %d from active ranges, %d from boundaries; %d read misses, %d incomplete reads",
		storage.MemoryStats().UsedBytes>>20, limit>>20, counters.EvictedCompletePieces, counters.EvictedIncompletePieces,
		counters.ActiveRangeEvictions, counters.BoundaryEvictions, counters.ReadMisses, counters.IncompleteReads)
	assert.LessOrEqual(t, storage.MemoryStats().UsedBytes, limit, "storage must stay within the memory limit")
}

// TestStreamingEngineUnderLoad streams a file while two other torrents
// preload, all competing for one memory limit smaller than their files, and
// compares the stream with one that runs alone.
func TestStreamingEngineUnderLoad(t *testing.T) {
	alone := runStressScenario(t, "streaming alone", false)
	loaded := runStressScenario(t, "streaming beside two preloads", true)
	t.Logf("preloads slowed the stream by %.0f%% (%v vs %v)",
		100*(loaded.Seconds()/alone.Seconds()-1), loaded.Round(time.Millisecond), alone.Round(time.Millisecond))
}

// runStressScenario streams a file like a player, with or without two other
// torrents preloading meanwhile, verifies every byte read, the preloads, and
// the memory limit, and returns how long the stream took.
func runStressScenario(t *testing.T, name string, withPreloads bool) time.Duration {
	t.Helper()
	var streamTime time.Duration
	t.Run(name, func(t *testing.T) {
		ctrl := newIntegrationTestController(t)
		setTestMemoryLimit(t, ctrl, stressMemoryLimit)
		server := httptest.NewServer(ctrl.router)
		defer server.Close()

		var preloaded []stressTorrent
		if withPreloads {
			preloaded = []stressTorrent{
				addStressTorrent(t, ctrl, "preloaded-a", 1),
				addStressTorrent(t, ctrl, "preloaded-b", 2),
			}
		}
		streamed := addStressTorrent(t, ctrl, "streamed", 3)
		start := time.Now()

		// Sample memory use and preload states throughout the run.
		var peakUsed atomic.Int64
		var mu sync.Mutex
		readyAt := make(map[metainfo.Hash]time.Duration)
		final := make(map[metainfo.Hash]api.PreloadResponse)
		sampling := make(chan struct{})
		var sampler sync.WaitGroup
		sampler.Go(func() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-sampling:
					return
				case <-ticker.C:
				}
				used := ctrl.storageClient.Load().MemoryStats().UsedBytes
				for {
					peak := peakUsed.Load()
					if used <= peak || peakUsed.CompareAndSwap(peak, used) {
						break
					}
				}
				for _, st := range preloaded {
					status := ctrl.preloadResponse(st.hash)
					mu.Lock()
					final[st.hash] = status
					if _, seen := readyAt[st.hash]; !seen && status.Status == api.Ready {
						readyAt[st.hash] = time.Since(start)
					}
					mu.Unlock()
				}
			}
		})

		for _, st := range preloaded {
			req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/torrents/%s/preload", server.URL, st.hash), bytes.NewBufferString(`{"file_index":0}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		// The player reads the file in consecutive ranges, seeks near the end
		// for the container index, and back, as players do on open and on
		// seeking.
		type rangeRead struct{ start, end int64 }
		reads := []rangeRead{{0, 1 << 20}, {stressFileSize - 1<<20, stressFileSize}}
		for offset := int64(0); offset < stressFileSize; offset += 4 << 20 {
			reads = append(reads, rangeRead{offset, min(offset+4<<20, stressFileSize)})
			if offset == 16<<20 {
				reads = append(reads, rangeRead{48 << 20, 52 << 20}, rangeRead{20 << 20, 24 << 20})
			}
		}

		var firstRange, slowest time.Duration
		var slowestAt int64
		streamStart := time.Now()
		for i, rr := range reads {
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/stream/%s?index=0", server.URL, streamed.hash), http.NoBody)
			require.NoError(t, err)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rr.start, rr.end-1))
			readStart := time.Now()
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusPartialContent, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			require.NoError(t, err)
			latency := time.Since(readStart)
			if testing.Verbose() {
				t.Logf("range %d-%d MiB: %v", rr.start>>20, rr.end>>20, latency.Round(time.Millisecond))
			}
			if i == 0 {
				firstRange = latency
			}
			if latency > slowest {
				slowest, slowestAt = latency, rr.start
			}
			require.True(t, bytes.Equal(streamed.payload[rr.start:rr.end], body), "range %d-%d must match the file", rr.start, rr.end)
		}
		streamTime = time.Since(streamStart)

		for _, st := range preloaded {
			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				status := final[st.hash].Status
				return status != api.Preloading && status != api.Queued && status != ""
			}, 60*time.Second, 50*time.Millisecond, "preload %s must finish", st.name)
		}
		close(sampling)
		sampler.Wait()

		counters := ctrl.storageClient.Load().Counters()
		t.Logf("stream: %d ranges, %d MiB in %v (%.1f MiB/s), first range %v, slowest range at %d MiB %v",
			len(reads), stressFileSize>>20, streamTime.Round(time.Millisecond),
			float64(stressFileSize)/float64(1<<20)/streamTime.Seconds(), firstRange.Round(time.Millisecond),
			slowestAt>>20, slowest.Round(time.Millisecond))
		for _, st := range preloaded {
			status := final[st.hash]
			t.Logf("preload %s: %s, %d of %d MiB, ready %v after start",
				st.name, status.Status, status.CompletedBytes>>20, status.TargetBytes>>20, readyAt[st.hash].Round(time.Millisecond))
			assert.Equal(t, api.Ready, status.Status, "preload %s must become ready, not give up", st.name)
		}
		t.Logf("memory: peak %d MiB of %d MiB; evicted %d complete and %d incomplete pieces, %d from active ranges, %d from boundaries; %d read misses",
			peakUsed.Load()>>20, stressMemoryLimit>>20, counters.EvictedCompletePieces, counters.EvictedIncompletePieces,
			counters.ActiveRangeEvictions, counters.BoundaryEvictions, counters.ReadMisses)
		assert.LessOrEqual(t, peakUsed.Load(), int64(stressMemoryLimit), "storage must stay within the memory limit")
	})
	return streamTime
}

// TestStreamingUnderTightMemory streams a file while two other torrents
// preload under a memory limit so small that the playing window, its file
// boundaries, and the preloads compete for it. Only one preload fits at a
// time, so the second evicts the first once that is ready.
func TestStreamingUnderTightMemory(t *testing.T) {
	const limit = 40 << 20
	ctrl := newIntegrationTestController(t)
	setTestMemoryLimit(t, ctrl, limit)
	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	client := stressClient{t: t, server: server}

	preloaded := []stressTorrent{addStressTorrent(t, ctrl, "tight-a", 21), addStressTorrent(t, ctrl, "tight-b", 22)}
	streamed := addStressTorrent(t, ctrl, "tight-streamed", 23)
	for _, st := range preloaded {
		client.preload(st, 0)
	}

	start := time.Now()
	for offset := int64(0); offset < stressFileSize; offset += 2 << 20 {
		require.NoError(t, client.readRange(context.Background(), streamed, 0, offset, min(offset+2<<20, stressFileSize)))
		if offset == 20<<20 {
			// Seek to the end for the container index, and back.
			require.NoError(t, client.readRange(context.Background(), streamed, 0, stressFileSize-1<<20, stressFileSize))
		}
	}
	t.Logf("streamed %d MiB in %v", stressFileSize>>20, time.Since(start).Round(time.Millisecond))

	for _, st := range preloaded {
		status := client.waitForPreloadEnd(st, 90*time.Second)
		t.Logf("preload %s: %s, %d of %d MiB", st.name, status.Status, status.CompletedBytes>>20, status.TargetBytes>>20)
		assert.Contains(t, []api.PreloadResponseStatus{api.Ready, api.Evicted}, status.Status,
			"a preload may only end ready, or evicted by the other preload")
	}
	logStorage(t, ctrl, limit)
}

// TestAggressivePlayers has two viewers read one file at once, each with two
// range requests in flight and random seeks, while a third viewer disconnects
// in the middle of its reads.
func TestAggressivePlayers(t *testing.T) {
	const limit = 128 << 20
	ctrl := newIntegrationTestController(t)
	setTestMemoryLimit(t, ctrl, limit)
	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	client := stressClient{t: t, server: server}
	st := addStressTorrent(t, ctrl, "aggressive", 31)

	var viewers sync.WaitGroup
	errs := make(chan error, 64)
	for viewer := range 2 {
		viewers.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(viewer), 5)) //nolint:gosec // Seeded, reproducible test workload.
			offset := int64(0)
			for range 12 {
				if rng.IntN(4) == 0 {
					offset = rng.Int64N(stressFileSize - 4<<20)
				}
				// Two ranges in flight, as players prefetch the next one.
				var inFlight sync.WaitGroup
				for part := range 2 {
					start := offset + int64(part)*(1<<20)
					inFlight.Go(func() { errs <- client.readRange(context.Background(), st, 0, start, start+1<<20) })
				}
				inFlight.Wait()
				offset = min(offset+2<<20, stressFileSize-2<<20)
			}
		})
	}
	// A viewer that closes its connection mid-read, again and again.
	viewers.Go(func() {
		for i := range 6 {
			ctx, cancel := context.WithCancel(context.Background())
			resp := client.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/stream/%s?index=0", st.hash), "",
				http.Header{"Range": {fmt.Sprintf("bytes=%d-", int64(i)*(8<<20))}})
			_, _ = io.CopyN(io.Discard, resp.Body, 64<<10)
			cancel()
			_ = resp.Body.Close()
		}
	})
	viewers.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}

	pool := ctrl.streamPool.Load()
	require.Eventually(t, func() bool { return !pool.HasActiveReaders(st.hash) }, 10*time.Second, 10*time.Millisecond,
		"every reader, including a disconnected viewer's, must be released")
	logStorage(t, ctrl, limit)
}

// TestPreloadChurn changes the engine's load while a player streams: more
// preloads than slots, switching a preload to another file, cancelling one
// mid-download, shrinking and restoring the memory limit, and deleting
// torrents while they are queued, streaming, or lingering.
func TestPreloadChurn(t *testing.T) {
	const limit = 128 << 20
	ctrl := newIntegrationTestController(t)
	setTestMemoryLimit(t, ctrl, limit)
	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	client := stressClient{t: t, server: server}

	a := addStressTorrent(t, ctrl, "churn-a", 41)
	b := addStressTorrent(t, ctrl, "churn-b", 42)
	c := addStressTorrent(t, ctrl, "churn-c", 43)
	multi := addStressTorrentFiles(t, ctrl, "churn-multi", 44, 24<<20, 24<<20)
	streamed := addStressTorrent(t, ctrl, "churn-streamed", 45)
	deleted := addStressTorrent(t, ctrl, "churn-deleted", 46)

	// Three preloads and two slots: one queues.
	client.preload(a, 0)
	client.preload(b, 0)
	client.preload(c, 0)
	assert.Equal(t, api.Queued, client.preloadStatus(c).Status, "a third preload waits for a slot")
	// Switch a preload to another file of its torrent.
	client.preload(multi, 0)
	client.preload(multi, 1)
	assert.Equal(t, 1, client.preloadStatus(multi).FileIndex, "a request for another file replaces the preload")

	ctx := t.Context()
	streaming := make(chan error, 1)
	go func() {
		for offset := int64(0); offset < stressFileSize; offset += 4 << 20 {
			if err := client.readRange(ctx, streamed, 0, offset, offset+4<<20); err != nil {
				streaming <- err
				return
			}
		}
		streaming <- nil
	}()

	// Cancel a preload mid-download, delete the queued one, and resize memory.
	client.expect(http.MethodDelete, fmt.Sprintf("/api/v1/torrents/%s/preload", b.hash), "", http.StatusNoContent)
	assert.Equal(t, api.Idle, client.preloadStatus(b).Status)
	client.expect(http.MethodDelete, fmt.Sprintf("/api/v1/torrents/%s", c.hash), "", http.StatusNoContent)
	client.expect(http.MethodPatch, "/api/v1/settings", `{"max_memory":67108864}`, http.StatusNoContent)
	time.Sleep(500 * time.Millisecond)
	client.expect(http.MethodPatch, "/api/v1/settings", fmt.Sprintf(`{"max_memory":%d}`, limit), http.StatusNoContent)

	// Delete a torrent while a player streams it.
	deletedStream := make(chan error, 1)
	go func() {
		deletedStream <- client.readRange(ctx, deleted, 0, 0, stressFileSize)
	}()
	require.Eventually(t, func() bool { return ctrl.streamPool.Load().HasActiveReaders(deleted.hash) }, 10*time.Second, 10*time.Millisecond)
	client.expect(http.MethodDelete, fmt.Sprintf("/api/v1/torrents/%s", deleted.hash), "", http.StatusNoContent)
	select {
	case err := <-deletedStream:
		t.Logf("stream of the deleted torrent ended: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("a stream of a deleted torrent must end")
	}

	require.NoError(t, <-streaming, "the unrelated stream must complete correctly")
	for _, st := range []stressTorrent{a, multi} {
		status := client.waitForPreloadEnd(st, 90*time.Second)
		t.Logf("preload %s: %s, %d of %d MiB", st.name, status.Status, status.CompletedBytes>>20, status.TargetBytes>>20)
		assert.Contains(t, []api.PreloadResponseStatus{api.Ready, api.Evicted}, status.Status)
	}
	// The switched preload cached the second file, which plays correctly.
	require.NoError(t, client.readRange(ctx, multi, 1, 0, 1<<20))

	// Delete the streamed torrent while its reader lingers.
	pool := ctrl.streamPool.Load()
	require.True(t, pool.HasReaders(streamed.hash), "the finished stream's reader lingers")
	client.expect(http.MethodDelete, fmt.Sprintf("/api/v1/torrents/%s", streamed.hash), "", http.StatusNoContent)
	require.Eventually(t, func() bool { return !pool.HasReaders(streamed.hash) }, 5*time.Second, 10*time.Millisecond,
		"a lingering reader of a deleted torrent must close")
	for _, st := range []stressTorrent{c, deleted, streamed} {
		assert.False(t, pool.HasReaders(st.hash), "%s keeps a reader", st.name)
		_, preloading := ctrl.preloadStatus(st.hash)
		assert.False(t, preloading, "%s keeps a preload", st.name)
	}
	logStorage(t, ctrl, limit)
}

// stressSeeder is an in-process BitTorrent client that seeds complete
// torrents to the controller's client over loopback TCP, with a shared upload
// rate like a single remote peer.
type stressSeeder struct {
	client *torrent.Client
	addr   net.Addr
}

func newStressSeeder(t *testing.T, uploadRate int) *stressSeeder {
	t.Helper()
	storageClient := memstorage.New(1<<30, nil)
	cfg := torrent.NewDefaultClientConfig()
	cfg.DefaultStorage = storageClient
	cfg.DataDir = t.TempDir()
	cfg.DisablePEX = true
	cfg.DisableTrackers = true
	cfg.DisableIPv6 = true
	cfg.DisableUTP = true
	cfg.DisableWebseeds = true
	cfg.DisableWebtorrent = true
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = true
	cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(uploadRate), 256<<10)
	client, err := torrent.NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
		_ = storageClient.Close()
	})
	addrs := client.ListenAddrs()
	require.NotEmpty(t, addrs)
	return &stressSeeder{client: client, addr: addrs[0]}
}

// seed makes the seeder hold every piece of a torrent.
func (s *stressSeeder) seed(t *testing.T, infoBytes []byte, files [][]byte) {
	t.Helper()
	to, _, err := s.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(&metainfo.MetaInfo{InfoBytes: infoBytes}))
	require.NoError(t, err)
	<-to.GotInfo()
	all := bytes.Join(files, nil)
	for index := range to.NumPieces() {
		start := index * stressPieceLength
		piece := to.Piece(index)
		_, err := piece.Storage().WriteAt(all[start:min(start+stressPieceLength, len(all))], 0)
		require.NoError(t, err)
		require.NoError(t, piece.VerifyData())
	}
	require.Equal(t, to.Length(), to.BytesCompleted(), "the seeder must hold the whole torrent")
}

// newPeerController returns a controller whose torrent client connects to
// peers over loopback TCP.
func newPeerController(t *testing.T) *Controller {
	t.Helper()
	runtimeConfig := testControllerRuntimeConfig()
	runtimeConfig.configureClient = func(config *torrent.ClientConfig) {
		config.NoDHT = true
		config.DisablePEX = true
		config.DisableTrackers = true
		config.DisableWebtorrent = true
		config.DisableWebseeds = true
		config.NoDefaultPortForwarding = true
		config.DisableIPv6 = true
		config.DisableUTP = true
		config.ListenHost = func(string) string { return "127.0.0.1" }
	}
	ctrl, _ := newTestControllerWithRuntimeConfig(t, runtimeConfig)
	ctrl.Start()
	return ctrl
}

// addPeerTorrent saves a torrent the seeder seeds, loads it the way the
// controller loads saved torrents, and connects it to the seeder.
func addPeerTorrent(t *testing.T, ctrl *Controller, seeder *stressSeeder, name string, seed uint64, storage api.TorrentStorage) stressTorrent {
	t.Helper()
	files, infoBytes := stressInfo(t, name, seed, stressFileSize)
	seeder.seed(t, infoBytes, files)
	st := stressTorrent{hash: metainfo.HashBytes(infoBytes), name: name, files: files, payload: files[0]}
	saveStressTorrent(t, ctrl, st, infoBytes, storage)
	to, err := ctrl.addTorrentByHash(st.hash)
	require.NoError(t, err)
	to.AddPeers([]torrent.PeerInfo{{Addr: seeder.addr, Trusted: true}})
	return st
}

// TestStreamingFromPeer streams a file from a BitTorrent peer, seeking as a
// player does, while two other torrents from the same peer preload, so
// playback and preloads share one peer's upload. Without the webseed request
// timer, it bounds the time to the first byte of each seek.
func TestStreamingFromPeer(t *testing.T) {
	const limit = 128 << 20
	ctrl := newPeerController(t)
	setTestMemoryLimit(t, ctrl, limit)
	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	client := stressClient{t: t, server: server}
	seeder := newStressSeeder(t, stressSeedRate)

	preloaded := []stressTorrent{
		addPeerTorrent(t, ctrl, seeder, "peer-a", 51, api.Memory),
		addPeerTorrent(t, ctrl, seeder, "peer-b", 52, api.Memory),
	}
	streamed := addPeerTorrent(t, ctrl, seeder, "peer-streamed", 53, api.Memory)
	for _, st := range preloaded {
		client.preload(st, 0)
	}

	var worst time.Duration
	for _, fraction := range []float64{0, 0.5, 0.1, 0.9, 0.3, 0.7} {
		start := int64(fraction*stressFileSize) &^ (1<<20 - 1)
		began := time.Now()
		require.NoError(t, client.readRange(context.Background(), streamed, 0, start, start+1<<20))
		latency := time.Since(began)
		t.Logf("1 MiB at %2.0f%%: %v", 100*fraction, latency.Round(time.Millisecond))
		worst = max(worst, latency)
	}
	// A 1 MiB range takes 64 ms at the peer's rate. The bound leaves room for
	// connection setup and the peer's queued preload pieces, but not for
	// playback waiting behind the preloads.
	assert.Less(t, worst, 3*time.Second, "playback must not wait behind preloads from the same peer")

	for _, st := range preloaded {
		status := client.waitForPreloadEnd(st, 60*time.Second)
		t.Logf("preload %s: %s, %d of %d MiB", st.name, status.Status, status.CompletedBytes>>20, status.TargetBytes>>20)
		assert.Equal(t, api.Ready, status.Status)
	}
	logStorage(t, ctrl, limit)
}

// TestStreamingFileStorageBesideDownloader streams a file-storage torrent
// from a peer while the background downloader has another file-storage
// torrent, and checks that the downloader pauses for the stream, including
// while its reader lingers, and resumes after it.
func TestStreamingFileStorageBesideDownloader(t *testing.T) {
	const limit = 128 << 20
	ctrl := newPeerController(t)
	ctrl.mu.Lock()
	settings := ctrl.settings.Load()
	settings.EnableDownloader = utils.Ptr(true)
	settings.FileStoragePath = utils.Ptr(t.TempDir())
	ctrl.mu.Unlock()
	require.NoError(t, ctrl.db.UpdateSettings(database.FromAPISettings(settings)))
	setTestMemoryLimit(t, ctrl, limit)
	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	client := stressClient{t: t, server: server}
	// A slow peer, so the background download is still running when the
	// stream starts.
	seeder := newStressSeeder(t, 4<<20)

	background := addPeerTorrent(t, ctrl, seeder, "fs-background", 61, api.File)
	streamed := addPeerTorrent(t, ctrl, seeder, "fs-streamed", 62, api.File)
	downloader := ctrl.downloader.Load()
	// Start runs a pass at once, on its own goroutine, instead of after the
	// one-minute interval. Stop clears what is downloading, so a pass that
	// wrongly resumes shows within moments.
	cycle := func() {
		downloader.Stop()
		downloader.Start()
	}
	downloading := func() bool { return downloader.IsDownloading(background.hash) }

	cycle()
	require.Eventually(t, downloading, 5*time.Second, 10*time.Millisecond, "the downloader must download the saved file-storage torrent")

	ctx := t.Context()
	streaming := make(chan error, 1)
	go func() {
		for offset := int64(0); offset < 16<<20; offset += 4 << 20 {
			if err := client.readRange(ctx, streamed, 0, offset, offset+4<<20); err != nil {
				streaming <- err
				return
			}
		}
		streaming <- nil
	}()
	pool := ctrl.streamPool.Load()
	require.Eventually(t, func() bool { return pool.HasReaders(streamed.hash) }, 10*time.Second, 10*time.Millisecond)
	cycle()
	assert.Never(t, downloading, time.Second, 10*time.Millisecond, "the downloader must pause while a file streams")
	require.NoError(t, <-streaming, "the file-storage stream must read correctly")

	require.True(t, pool.HasReaders(streamed.hash), "the finished stream's reader lingers")
	cycle()
	assert.Never(t, downloading, time.Second, 10*time.Millisecond, "a lingering reader keeps the downloader paused")

	require.Eventually(t, func() bool { return pool.StreamingTorrentCount() == 0 }, 45*time.Second, 50*time.Millisecond,
		"the lingering reader must close")
	cycle()
	assert.Eventually(t, downloading, 5*time.Second, 10*time.Millisecond, "the downloader must resume after the stream")
	logStorage(t, ctrl, limit)
}
