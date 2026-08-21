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
// subsequent Acquire for the same (infohash, filePath) pair reuses the
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
// # Idle GC
//
// Readers that remain idle longer than the effective timeout are parked
// by having their readahead set to zero. This allows the torrent client
// to reclaim piece memory.
//
// When a MemoryPressureFunc is configured, the idle timeout scales down
// automatically under memory pressure (e.g., 1 s at ≥90 %, 5 s at ≥75 %,
// 10 s at ≥50 %). This prevents pieces from being downloaded and
// immediately evicted. Without MemoryPressureFunc the fixed IdleTimeout
// (default 30 s) is used.
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
//			FileStorageReadahead: 50 * 1024 * 1024, // 50 MB
//			IdleTimeout:          30 * time.Second,
//			Logger:               slog.Default(),
//			MaxReadersPerTorrent: 10,
//			Registry:             nil, // pass a storage.Client here to enable eviction protection
//		})
//		defer pool.Close()
//
//		// Acquire a reader for a torrent file.
//		// reader, release := pool.Acquire(ctx, hash, file, false, 256<<20)
//		// defer release()
//		//
//		// // Use http.ServeContent with an io.SectionReader backed by the reader.
//		// http.ServeContent(w, r, file.Path(), time.Time{}, io.NewSectionReader(reader, 0, file.Length()))
//	}
package stream
