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
// eviction candidates again. A seek is reported when the next read starts, so
// the destination is protected before the read can block, while positions that
// are never read, such as the size probe of http.ServeContent, are skipped.
//
// # Piece-Priority Bumping
//
// When PriorityWindowFraction > 0, reading a new piece triggers an
// asynchronous piece-priority bump in the torrent client: the nearest
// PriorityWindowFraction of the readahead pieces receive PiecePriorityNow, so
// they download before the rest of the readahead window, which the torrent
// client orders by rarity. Preload readers instead claim every piece in their
// bounded range at PiecePriorityNow until release. Stale priority updates are
// discarded when a reader moves or is released. Pool-level claim aggregation
// preserves the highest priority requested by overlapping readers.
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
// memory readahead budget among active memory-storage readers. Storage protects
// whole pieces, so the division is made in pieces: head and tail boundaries use
// at most half of the budget and shrink, or are dropped, until their pieces fit,
// and each reader's readahead is the largest whole number of pieces whose active
// range fits its share. With large pieces and a small budget, a reader may protect
// only the piece it is reading. A file whose held preload reservation covers
// both its head and tail gets no reader boundaries, because the preload already
// protects them within that reservation. File-storage
// readers retain their configured FileReadaheadBytes while active. Competing idle
// readers are parked immediately whenever playback or preload work is active, so
// released HTTP range requests cannot keep downloading outside the shared budget.
// The last idle reader may remain warm until its normal park timeout. This ensures
// no stale reader monopolizes the torrent client's download capacity while active
// work starves.
//
// # Preload Reservations
//
// Preloads share the readahead budget with playback. ReservePreloadBudget
// admits a preload of one file per torrent and may grant less than requested;
// PreloadCapacity reports the preload share of the budget, and the rest is
// always kept for playback readers so a new stream is never starved by held
// preloads. ReleasePreloadBudget returns the reservation to active readers.
// AcquirePreloadContext acquires a reader bounded to a byte range whose pieces
// are claimed at PiecePriorityNow until release. SetReadaheadBudget refuses a
// new budget whose preload share cannot hold the existing reservations.
//
// # Idle GC
//
// Readers that remain idle longer than the effective timeout are parked
// by having their readahead set to zero. This allows the torrent client
// to reclaim piece memory. Readers that remain idle for CloseTimeout are
// closed and removed from the pool; a negative IdleCloseTimeout disables removal.
//
// When MemoryUsage is configured, the idle park timeout scales down
// automatically under memory pressure (e.g., 1 s at ≥90 %, 5 s at ≥75 %,
// 10 s at ≥50 %). This prevents pieces from being downloaded and
// immediately evicted. When IdleCloseTimeout is positive, the close deadline
// is also capped at 30 s at ≥90 % and 60 s at ≥75 %. Without MemoryUsage the fixed IdleParkTimeout (default 30 s)
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
//		if !pool.SetReadaheadBudget(256 << 20) {
//			panic("readahead budget conflicts with active preload reservations")
//		}
//
//		// Acquire returns the bounded io.ReadSeeker expected by http.ServeContent.
//		// reader, release, err := pool.Acquire(file, stream.MemoryStorage)
//		// if err != nil { panic(err) }
//		// defer release()
//		// http.ServeContent(w, r, file.Path(), time.Time{}, reader)
//	}
package stream
