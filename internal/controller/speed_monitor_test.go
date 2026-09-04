// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSpeedMonitor_Calculations(t *testing.T) {
	sm := newSpeedMonitor()

	// Initial baseline sample sets reference totals with 0 speed.
	sm.update(1000, 500)
	down, up := sm.Speeds()
	assert.Equal(t, int64(0), down)
	assert.Equal(t, int64(0), up)

	// Simulate 1 second elapsed with 10,000 bytes downloaded and 5,000 bytes uploaded.
	sm.mu.Lock()
	sm.lastMetricsTime = time.Now().Add(-1 * time.Second)
	sm.mu.Unlock()

	sm.update(11000, 5500)
	down, up = sm.Speeds()
	// Delta: 10,000 bytes / 1s = 10,000 B/s; 5,000 bytes / 1s = 5,000 B/s
	assert.InDelta(t, 10000, down, 200)
	assert.InDelta(t, 5000, up, 100)

	// Second sample with EMA smoothing (alpha = 0.5):
	// New instant speed: 20,000 B/s down, 10,000 B/s up.
	// Expected: 0.5 * 20000 + 0.5 * 10000 = 15000.
	sm.mu.Lock()
	sm.lastMetricsTime = time.Now().Add(-1 * time.Second)
	sm.mu.Unlock()

	sm.update(31000, 15500)
	down, up = sm.Speeds()
	assert.InDelta(t, 15000, down, 300)
	assert.InDelta(t, 7500, up, 150)

	// Protection against negative deltas (e.g. counter reset or lower reading).
	sm.mu.Lock()
	sm.lastMetricsTime = time.Now().Add(-1 * time.Second)
	sm.mu.Unlock()

	sm.update(0, 0)
	down, up = sm.Speeds()
	assert.GreaterOrEqual(t, down, int64(0), "download speed must never be negative")
	assert.GreaterOrEqual(t, up, int64(0), "upload speed must never be negative")

	// Decay to 0 when delta is zero and smoothed speed drops below 10.
	sm.mu.Lock()
	sm.downloadSpeed = 5
	sm.uploadSpeed = 5
	sm.lastTotalDownload = 100
	sm.lastTotalUpload = 100
	sm.lastMetricsTime = time.Now().Add(-1 * time.Second)
	sm.mu.Unlock()

	sm.update(100, 100)
	down, up = sm.Speeds()
	assert.Equal(t, int64(0), down)
	assert.Equal(t, int64(0), up)
}

func TestSpeedMonitor_ElapsedNonPositive(t *testing.T) {
	sm := newSpeedMonitor()
	sm.update(1000, 500)

	// When timestamp does not advance (elapsed <= 0), values remain unchanged.
	sm.mu.Lock()
	sm.lastMetricsTime = time.Now().Add(1 * time.Second) // future
	sm.mu.Unlock()

	sm.update(2000, 1000)
	down, up := sm.Speeds()
	assert.Equal(t, int64(0), down)
	assert.Equal(t, int64(0), up)
}

func TestSpeedMonitor_Lifecycle(t *testing.T) {
	sm := newSpeedMonitor()

	var callCount atomic.Int32
	go sm.Start(func() (int64, int64) {
		callCount.Add(1)
		return 5000, 2000
	})

	assert.Eventually(t, func() bool {
		return callCount.Load() > 0
	}, 1*time.Second, 50*time.Millisecond)

	// Ensure Stop terminates cleanly and is idempotent.
	sm.Stop()
	sm.Stop()
}
