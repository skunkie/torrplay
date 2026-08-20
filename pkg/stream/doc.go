// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package stream provides a pooled reader manager for torrent file streaming.
//
// It multiplexes multiple concurrent readers per torrent file, manages idle
// reader parking (readahead = 0), and coordinates with the storage layer via
// the ActiveRangeRegistry interface to protect actively-read pieces from eviction.
//
// # Overview
//
// The Pool type is the central manager. Each torrent file can have multiple
// readers acquired simultaneously. On Acquire the pool either reactivates an
// idle reader or creates a new one. The caller receives an io.ReadSeeker
// backed by an io.SectionReader so that http.ServeContent can serve Range
// requests without blocking on sequential reads.
//
// # Idle Reader Reuse
//
// When an HTTP request ends, the reader is released to idle state. A
// subsequent Acquire for the same (info hash, file path) pair reuses the
// idle reader instead of creating a new one, avoiding duplicate download
// progress from the torrent client.
//
// # Active Range Protection
//
// Each active reader registers a forward-weighted readahead window
// (1/4 behind, full readahead ahead) through the ActiveRangeRegistry interface. Pieces
// inside this window are protected from LRU eviction. When the reader is
// released, the active range is cleared immediately so those pieces become
// eviction candidates again.
//
// # Piece-Priority Bumping
//
// When PriorityWindowFraction > 0, reading a new offset triggers an
// asynchronous piece-priority bump in the torrent client. Of the selected
// pieces, the nearest PriorityNowFraction (default 30%) receive
// PiecePriorityNow and the remainder receive PiecePriorityHigh. Stale priority updates
// are discarded when a reader moves or is released. Pool-level claim
// aggregation preserves the highest priority requested by overlapping readers.
//
// # Reader Cap and Eviction
//
// MaxReadersPerFile limits the total number of
// active and idle readers per (info hash, file path) pair. Each file has an
// independent soft cap: active readers are never terminated, so bursts of
// concurrent requests may temporarily exceed the limit. Excess idle readers
// are removed as requests finish.
//
// # Readahead Rebalancing
//
// When a reader is acquired, released, or parked, the pool redistributes the total
// memory readahead budget among active memory-storage readers. File-storage
// readers retain their configured FileReadaheadBytes. This ensures no single
// reader monopolizes the torrent client's download capacity while others starve.
//
// # Idle GC
//
// Readers that remain idle longer than the effective timeout are parked
// by having their readahead set to zero. This allows the torrent client
// to reclaim piece memory. Readers that remain idle for CloseTimeout are
// closed and removed from the pool; a negative IdleCloseTimeout disables removal.
//
// When MemoryUsage is configured, the idle timeout scales down
// automatically under memory pressure (e.g., 1 s at ≥90 %, 5 s at ≥75 %,
// 10 s at ≥50 %). This prevents pieces from being downloaded and
// immediately evicted. At high pressure, the close deadline may also be
// shortened. Without MemoryUsage the fixed IdleParkTimeout (default 30 s)
// and IdleCloseTimeout (default 5 minutes) are used.
//
// # Thread Safety
//
// All public methods are safe for concurrent use.
//
// # Usage Example
//
//	package main
//
//	import (
//		"log/slog"
//		"time"
//
//		"github.com/torrplay/torrplay/pkg/stream"
//	)
//
//	func main() {
//		pool := stream.New(stream.Config{
//			FileReadaheadBytes: 50 * 1024 * 1024, // 50 MiB
//			IdleParkTimeout:    30 * time.Second,
//			Logger:             slog.Default(),
//			MaxReadersPerFile:  10,
//			Registry:           nil, // pass a storage.Client here to enable eviction protection
//		})
//		defer pool.Close()
//		pool.SetReadaheadBudget(256 << 20)
//
//		// Acquire returns the bounded io.ReadSeeker expected by http.ServeContent.
//		// reader, release, err := pool.Acquire(file, stream.MemoryStorage)
//		// if err != nil { panic(err) }
//		// defer release()
//		// http.ServeContent(w, r, file.Path(), time.Time{}, reader)
//	}
package stream
