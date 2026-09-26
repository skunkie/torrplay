// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
	"github.com/torrplay/torrplay/pkg/stream"
)

// preloadCapacityFor returns the preload capacity a stream pool reports for
// the readahead budget of maxMemory.
func preloadCapacityFor(t *testing.T, maxMemory int64) int64 {
	t.Helper()
	pool := stream.New(stream.Config{})
	defer pool.Close()
	pool.SetReadaheadBudget(readaheadBudget(maxMemory))
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

func TestUpdateSettingsMemoryShrinkEvictsPreloadsThatNoLongerFit(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	require.Equal(t, http.StatusNoContent, patchMaxMemory(t, ctrl, 512<<20).Recorder.Code)
	to := addSyntheticTorrent(t, ctrl, 1<<30, 1<<20)
	require.True(t, ctrl.startPreload(to, to.Files()[0]))
	preload, ok := ctrl.preloadStatus(to.InfoHash())
	require.True(t, ok)
	require.Equal(t, stream.PreloadRunning, preload.State, "the preload must run and hold its reservation")
	const smaller = 32 << 20
	// Its whole 1 MiB pieces make the reservation equal to the target.
	require.Greater(t, preload.TargetBytes, preloadCapacityFor(t, smaller), "the reservation must not fit the smaller budget")

	rr := patchMaxMemory(t, ctrl, smaller).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	preload, ok = ctrl.preloadStatus(to.InfoHash())
	require.True(t, ok)
	assert.Equal(t, stream.PreloadEvicted, preload.State, "a preload that no longer fits must be evicted")
	assert.Equal(t, api.Evicted, ctrl.preloadResponse(to.InfoHash()).Status)
	assert.Equal(t, int64(smaller), ctrl.storageClient.Load().MemoryStats().LimitBytes)
	assert.Equal(t, preloadCapacityFor(t, smaller), ctrl.streamPool.Load().PreloadCapacity())
}

func TestUpdateSettingsRollsBackFailedMemoryResize(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	oldMaxMemory := *ctrl.settings.Load().MaxMemory
	pool := ctrl.streamPool.Load()
	oldCapacity := pool.PreloadCapacity()
	// A closed memory storage cannot be resized, so the update fails after
	// the stream budget was already shrunk and must be rolled back.
	closedStorage := memstorage.New(oldMaxMemory, nil)
	require.NoError(t, closedStorage.Close())
	storageClient := ctrl.storageClient.Swap(closedStorage)

	rr := patchMaxMemory(t, ctrl, 32<<20).Recorder
	ctrl.storageClient.Store(storageClient)
	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "failed to resize memory storage")

	assert.Equal(t, oldMaxMemory, *ctrl.settings.Load().MaxMemory)
	stored, err := ctrl.db.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, oldMaxMemory, *stored.MaxMemory)
	assert.Equal(t, oldMaxMemory, ctrl.storageClient.Load().MemoryStats().LimitBytes)
	assert.Equal(t, oldCapacity, pool.PreloadCapacity())
}

func TestUpdateSettingsMemoryShrinkEvictsOnlyPreloadsThatDoNotFit(t *testing.T) {
	ctrl, cleanup := newTestController(t)
	defer cleanup()

	require.Equal(t, http.StatusNoContent, patchMaxMemory(t, ctrl, 512<<20).Recorder.Code)
	lengths := []int64{1 << 30, 1<<30 + 1<<20}
	hashes := make([]metainfo.Hash, 0, len(lengths))
	reserved := make([]int64, 0, len(lengths))
	for _, length := range lengths {
		to := addSyntheticTorrent(t, ctrl, length, 1<<20)
		require.True(t, ctrl.startPreload(to, to.Files()[0]))
		preload, ok := ctrl.preloadStatus(to.InfoHash())
		require.True(t, ok)
		require.Equal(t, stream.PreloadRunning, preload.State, "both preloads must hold reservations")
		hashes = append(hashes, to.InfoHash())
		// Their whole 1 MiB pieces make each reservation equal to its target.
		reserved = append(reserved, preload.TargetBytes)
	}

	// Pick a limit whose preload share fits one reservation but not both.
	largest := max(reserved[0], reserved[1])
	var maxMemory int64
	for candidate := int64(32 << 20); candidate < 512<<20; candidate += 1 << 20 {
		if capacity := preloadCapacityFor(t, candidate); capacity >= largest && capacity < reserved[0]+reserved[1] {
			maxMemory = candidate
			break
		}
	}
	require.NotZero(t, maxMemory, "no memory limit fits exactly one reservation")

	rr := patchMaxMemory(t, ctrl, maxMemory).Recorder
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	kept := 0
	for _, ih := range hashes {
		if preload, ok := ctrl.preloadStatus(ih); ok && preload.State == stream.PreloadRunning {
			kept++
		}
	}
	assert.Equal(t, 1, kept, "only the preloads that do not fit may be evicted")
}
