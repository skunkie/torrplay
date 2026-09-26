// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package stream provides a pooled reader manager for torrent file streaming.
//
// It multiplexes multiple concurrent readers per torrent file, keeps a released
// reader reading ahead briefly for the player's next request, and coordinates
// with the storage layer via the ActiveRangeRegistry interface to protect
// actively-read pieces from eviction.
//
// # Overview
//
// The Pool type is the central manager. Each torrent file can have multiple
// readers acquired simultaneously, and every Acquire creates a new reader. The
// caller receives an io.ReadSeeker backed by an io.SectionReader so that
// http.ServeContent can serve Range requests without blocking on sequential
// reads.
//
// # Lingering Readers
//
// When an HTTP request ends, its playback reader lingers: it stays open with
// its readahead, so the torrent client keeps fetching the pieces just past
// where the player stopped. A player that fetches a file in consecutive range
// requests, or reconnects after a pause, then finds those pieces cached by the
// time its next request's reader reaches them. A reader lingers only while no
// other playback or preload reader is active; new work closes it after taking
// over its own readahead window, so released requests never download outside
// the shared budget. Readers still lingering after LingerTimeout are closed.
// Preload readers and readers of a dropped torrent close on release.
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
// client orders by rarity. Preload readers claim no priorities: their
// readahead already spans their bounded range, and preloads do not run
// alongside playback. Stale priority updates are discarded when a reader moves
// or is released. Pool-level claim aggregation preserves the highest priority
// requested by overlapping readers.
//
// # Readahead Rebalancing
//
// When a reader is acquired or released, the pool redistributes the total
// memory readahead budget among active memory-storage readers. Storage protects
// whole pieces, so the division is made in pieces: head and tail boundaries use
// at most half of the budget and shrink, or are dropped, until their pieces fit,
// and each reader's readahead is the largest whole number of pieces whose active
// range fits its share. With large pieces and a small budget, a reader may protect
// only the piece it is reading. A file whose held preload reservation covers
// both its head and tail gets no reader boundaries, because the preload already
// protects them within that reservation. File-storage
// readers retain their configured FileReadaheadBytes while active. Lingering
// readers hold no share of the budget.
//
// # Preload Reservations
//
// Preloads share the readahead budget with playback. ReservePreload admits a
// preload of one file per torrent when the whole pieces of its head and tail
// fit the preload share of the budget, and protects those pieces from eviction
// until ReleasePreload. PreloadCapacity reports the preload share, and the rest
// is always kept for playback readers so a new stream is never starved by held
// preloads. ReleasePreload returns the reservation to active readers.
// AcquirePreloadContext acquires a reader whose readahead is bounded to a byte
// range. SetReadaheadBudget refuses a new budget whose preload share cannot
// hold the existing reservations.
//
// # Memory Pressure
//
// When MemoryUsage is configured, the linger timeout shortens under memory
// pressure (1 s at ≥90 %, 5 s at ≥75 %, 10 s at ≥50 %), so pieces are not
// downloaded only to be evicted. Without MemoryUsage the fixed LingerTimeout
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
//			FileReadaheadBytes: 50 * 1024 * 1024, // 50 MiB
//			LingerTimeout:      30 * time.Second,
//			Logger:             slog.Default(),
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
