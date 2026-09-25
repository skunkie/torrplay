// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/pkg/stream"
)

// preloadCapacityFor returns the preload capacity a stream pool reports for
// the readahead budget of maxMemory.
func preloadCapacityFor(t *testing.T, maxMemory int64) int64 {
	t.Helper()
	pool := stream.New(stream.Config{})
	defer pool.Close()
	require.True(t, pool.SetReadaheadBudget(readaheadBudget(maxMemory)))
	return pool.PreloadCapacity()
}

func patchMaxMemory(t *testing.T, ctrl *Controller, maxMemory int64) *testutil.CompletedRequest {
	t.Helper()
	return testutil.NewRequest().Patch("/api/v1/settings").
		WithJsonBody(map[string]int64{"max_memory": maxMemory}).
		GoWithHTTPHandler(t, http.HandlerFunc(ctrl.UpdateSettings))
}

func TestUpdateSettingsResizesMemoryInPlace(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	client := ctrl.currentClient()
	storageClient := ctrl.storageClient.Load()
	pool := ctrl.streamPool.Load()
	generation := ctrl.torrentGeneration.Load()

	for _, maxMemory := range []int64{512 << 20, 32 << 20} {
		t.Run(fmt.Sprintf("%d MiB", maxMemory>>20), func(t *testing.T) {
			rr := patchMaxMemory(t, ctrl, maxMemory).Recorder
			require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

			assert.Same(t, client, ctrl.currentClient(), "the torrent client must not be replaced")
			assert.Same(t, storageClient, ctrl.storageClient.Load())
			assert.Same(t, pool, ctrl.streamPool.Load())
			assert.Equal(t, generation, ctrl.torrentGeneration.Load(), "streams must stay valid")
			assert.False(t, ctrl.torrentClientUnavailable.Load())

			assert.Equal(t, maxMemory, storageClient.MemoryStats().LimitBytes)
			assert.Equal(t, preloadCapacityFor(t, maxMemory), pool.PreloadCapacity())
			assert.Equal(t, maxMemory, *ctrl.settings.Load().MaxMemory)
		})
	}
}

func TestUpdateSettingsMemoryShrinkCancelsPreloadsThatNoLongerFit(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	require.Equal(t, http.StatusNoContent, patchMaxMemory(t, ctrl, 512<<20).Recorder.Code)
	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	preload := ctrl.startPreload(to, to.Files()[0], 0)
	require.NotNil(t, preload)
	require.Eventually(t, func() bool {
		ctrl.preloadsMu.Lock()
		defer ctrl.preloadsMu.Unlock()
		return preload.active
	}, 5*time.Second, 10*time.Millisecond, "the preload must be dispatched and hold its reservation")
	const smaller = 32 << 20
	require.Greater(t, preload.reserveBytes, preloadCapacityFor(t, smaller), "the reservation must not fit the smaller budget")

	rr := patchMaxMemory(t, ctrl, smaller).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	_, ok := ctrl.preloads.Load(to.InfoHash())
	assert.False(t, ok, "a preload that no longer fits must be cancelled")
	assert.Equal(t, int64(smaller), ctrl.storageClient.Load().MemoryStats().LimitBytes)
	assert.Equal(t, preloadCapacityFor(t, smaller), ctrl.streamPool.Load().PreloadCapacity())
}

func TestUpdateSettingsRollsBackFailedMemoryResize(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	oldMaxMemory := *ctrl.settings.Load().MaxMemory
	pool := ctrl.streamPool.Load()
	oldCapacity := pool.PreloadCapacity()
	// A reservation the controller does not track cannot be cancelled, so the
	// smaller budget cannot be applied.
	infoHash := metainfo.Hash{7}
	require.Equal(t, oldCapacity, pool.ReservePreloadBudget(infoHash, "", false, oldCapacity))
	defer pool.ReleasePreloadBudget(infoHash)

	rr := patchMaxMemory(t, ctrl, 32<<20).Recorder
	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "failed to resize memory storage")

	assert.Equal(t, oldMaxMemory, *ctrl.settings.Load().MaxMemory)
	stored, err := ctrl.db.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, oldMaxMemory, *stored.MaxMemory)
	assert.Equal(t, oldMaxMemory, ctrl.storageClient.Load().MemoryStats().LimitBytes)
	assert.Equal(t, oldCapacity, pool.PreloadCapacity())
}

func TestReleaseOnePreloadReservationLockedReleasesCheapestFirst(t *testing.T) {
	ctrl := &Controller{preloadReadyTTL: time.Hour}
	var released []string
	newTask := func(name string, hash metainfo.Hash) *preloadTask {
		task := &preloadTask{
			cancel:        func() {},
			done:          make(chan struct{}),
			infoHash:      hash,
			protected:     true,
			releaseBudget: func() { released = append(released, name) },
		}
		task.doneOnce.Do(func() { close(task.done) })
		return task
	}

	ready := newTask("ready", metainfo.Hash{1})
	ready.ready.Store(true)
	ctrl.preloads.Store(ready.infoHash, ready)
	running := newTask("running", metainfo.Hash{2})
	running.active = true
	ctrl.preloadActiveTasks = 1
	ctrl.preloads.Store(running.infoHash, running)
	lease := newTask("lease", metainfo.Hash{3})
	lease.ready.Store(true)
	session := &playbackSession{key: playbackKey{infoHash: lease.infoHash}, lease: lease}
	ctrl.playbackSessions = map[playbackKey]*playbackSession{session.key: session}

	ctrl.preloadsMu.Lock()
	defer ctrl.preloadsMu.Unlock()
	for range 3 {
		require.True(t, ctrl.releaseOnePreloadReservationLocked())
	}
	assert.False(t, ctrl.releaseOnePreloadReservationLocked(), "nothing is left to release")
	assert.Equal(t, []string{"ready", "running", "lease"}, released)
	assert.Nil(t, session.lease)
	assert.Zero(t, ctrl.preloadActiveTasks)
}

func TestUpdateSettingsMemoryShrinkReleasesOnlyPreloadsThatDoNotFit(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	require.Equal(t, http.StatusNoContent, patchMaxMemory(t, ctrl, 512<<20).Recorder.Code)
	lengths := []int64{1 << 30, 1<<30 + 1<<20}
	preloads := make([]*preloadTask, 0, len(lengths))
	for _, length := range lengths {
		to := addSyntheticTorrent(t, ctrl, length, 1<<20)
		preload := ctrl.startPreload(to, to.Files()[0], 0)
		require.NotNil(t, preload)
		preloads = append(preloads, preload)
	}
	require.Eventually(t, func() bool {
		ctrl.preloadsMu.Lock()
		defer ctrl.preloadsMu.Unlock()
		return preloads[0].active && preloads[1].active
	}, 5*time.Second, 10*time.Millisecond, "both preloads must hold reservations")

	// Pick a limit whose preload share fits one reservation but not both.
	largest := max(preloads[0].reserveBytes, preloads[1].reserveBytes)
	var maxMemory int64
	for candidate := int64(32 << 20); candidate < 512<<20; candidate += 1 << 20 {
		if capacity := preloadCapacityFor(t, candidate); capacity >= largest && capacity < preloads[0].reserveBytes+preloads[1].reserveBytes {
			maxMemory = candidate
			break
		}
	}
	require.NotZero(t, maxMemory, "no memory limit fits exactly one reservation")

	rr := patchMaxMemory(t, ctrl, maxMemory).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	kept := 0
	for _, preload := range preloads {
		if current, ok := ctrl.preloads.Load(preload.infoHash); ok && current == preload {
			kept++
		}
	}
	assert.Equal(t, 1, kept, "only the preloads that do not fit may be released")
}
