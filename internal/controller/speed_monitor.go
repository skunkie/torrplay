// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"sync"
	"time"
)

const (
	speedSampleInterval = 500 * time.Millisecond
	speedEMAFactor      = 0.5
)

// speedMonitor tracks network transfer rates over time using an Exponential Moving Average (EMA).
type speedMonitor struct {
	done              chan struct{}
	downloadSpeed     int64
	lastMetricsTime   time.Time
	lastTotalDownload int64
	lastTotalUpload   int64
	mu                sync.RWMutex
	stopOnce          sync.Once
	uploadSpeed       int64
}

func newSpeedMonitor() *speedMonitor {
	return &speedMonitor{
		done: make(chan struct{}),
	}
}

// Speeds returns the current smoothed download and upload speeds in bytes per second.
func (s *speedMonitor) Speeds() (downloadSpeed, uploadSpeed int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.downloadSpeed, s.uploadSpeed
}

func (s *speedMonitor) update(totalDownloadBytes, totalUploadBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.lastMetricsTime.IsZero() {
		s.lastMetricsTime = now
		s.lastTotalDownload = totalDownloadBytes
		s.lastTotalUpload = totalUploadBytes
		return
	}

	elapsed := now.Sub(s.lastMetricsTime).Seconds()
	if elapsed <= 0 {
		return
	}

	deltaDownload := max(0, totalDownloadBytes-s.lastTotalDownload)
	deltaUpload := max(0, totalUploadBytes-s.lastTotalUpload)

	instantDownloadSpeed := float64(deltaDownload) / elapsed
	instantUploadSpeed := float64(deltaUpload) / elapsed

	if s.downloadSpeed == 0 {
		s.downloadSpeed = int64(instantDownloadSpeed)
	} else {
		s.downloadSpeed = int64(speedEMAFactor*instantDownloadSpeed + (1-speedEMAFactor)*float64(s.downloadSpeed))
	}

	if s.uploadSpeed == 0 {
		s.uploadSpeed = int64(instantUploadSpeed)
	} else {
		s.uploadSpeed = int64(speedEMAFactor*instantUploadSpeed + (1-speedEMAFactor)*float64(s.uploadSpeed))
	}

	if deltaDownload == 0 && s.downloadSpeed < 10 {
		s.downloadSpeed = 0
	}
	if deltaUpload == 0 && s.uploadSpeed < 10 {
		s.uploadSpeed = 0
	}

	s.lastMetricsTime = now
	s.lastTotalDownload = totalDownloadBytes
	s.lastTotalUpload = totalUploadBytes
}

// Start runs a periodic sampling loop in a background goroutine until Stop is called.
func (s *speedMonitor) Start(getTotals func() (downloadBytes, uploadBytes int64)) {
	ticker := time.NewTicker(speedSampleInterval)
	defer ticker.Stop()

	// Initial baseline sample
	s.update(getTotals())

	for {
		select {
		case <-ticker.C:
			s.update(getTotals())
		case <-s.done:
			return
		}
	}
}

// Stop terminates the periodic sampling loop.
func (s *speedMonitor) Stop() {
	if s.done != nil {
		s.stopOnce.Do(func() {
			close(s.done)
		})
	}
}
