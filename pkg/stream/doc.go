// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package stream is the streaming engine for torrent files: a pooled reader
// manager and a preloader sharing one readahead budget and one piece-priority
// system.
//
// It multiplexes multiple concurrent readers per torrent file, keeps a released
// reader reading ahead briefly for the player's next request, caches the head
// and tail of files before they are played, and coordinates with the storage
// layer via the ActiveRangeRegistry interface to protect actively-read and
// preloaded pieces from eviction.
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
// other reader is active; a new reader closes it after taking over its own
// readahead window, so released requests never download outside the shared
// budget. Readers still lingering after LingerTimeout are closed. Readers of a
// dropped torrent close on release.
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
// client orders by rarity. Preloads claim their pieces at PiecePriorityHigh,
// below every playback reader's readahead, so they download with the bandwidth
// playback leaves. Stale priority updates are discarded when a reader moves or
// is released. Pool-level claim aggregation preserves the highest priority
// requested by overlapping readers and preloads.
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
// # Preloads
//
// Preload caches the head and tail of a file, where containers keep their
// metadata and seek indexes, so its playback starts without waiting for them.
// A torrent has at most one preload, and a request for another of its files
// replaces it. Preloads never pause for playback, because the engine is shared
// by every viewer: they have no reader, claim their pieces at
// PiecePriorityHigh, and a watcher marks them ready once every piece is
// complete. At most two preloads download at a time, in request order.
//
// A memory-storage preload reserves its whole pieces in the preload share of
// the readahead budget, reported by PreloadCapacity, and protects them from
// eviction until it is removed. The rest of the budget is always kept for
// playback readers, so a new stream is never starved by held preloads. When
// the share is full, a queued preload evicts the ready preload that has gone
// longest without a reader; ready preloads of files being read stay pinned. A
// preload that cannot fit and has nothing to wait for fails. A smaller
// SetReadaheadBudget evicts preloads until they fit, cheapest first. A ready
// preload that loses a piece to eviction downloads it again.
//
// A ready preload expires PreloadReadyTTL after its file last had a reader,
// and a failed or evicted preload reports its final state for as long.
// File-storage preloads write to disk and reserve nothing.
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
