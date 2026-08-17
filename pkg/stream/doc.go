// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package stream provides thread-safe, refcounted stream readers for torrent files,
// featuring dynamic fair-share readahead distribution, active range tracking for
// storage eviction protection, and idle reader connection pooling.
//
// # Overview
//
// Streaming video and large media files directly from torrents requires efficient
// byte-range seekability, forward prefetching (readahead), backward trailing demuxer
// protection, and fair memory allocation across multiple concurrent clients (e.g. multiple
// browser tabs, DLNA media renderers, or external media players).
//
// Package stream solves these challenges by providing:
//
//   - Connection Pooling: Reuses warmed-up readahead readers across consecutive HTTP range requests
//     from the same playback session, avoiding cold-start readahead penalties.
//
//   - Dynamic Fair-Share Readahead: Fairly divides available memory readahead pool across active
//     concurrent streams with safety minimum floors (at least 2 full pieces or 10MB).
//
//   - Active Range Tracking: Continuously reports current byte positions and windows [start, end)
//     to the storage layer (e.g. pkg/storage) to prevent active stream data and trailing demuxer
//     buffers from being evicted during high memory pressure.
//
//   - Deadlock-Free Concurrency: Fine-grained locking ensures reader operations, range updates,
//     and readahead adjustments execute concurrently without blocking HTTP streaming I/O.
//
// # Usage Example
//
//	// Create a stream pool attached to the memory storage client.
//	pool := stream.New(stream.Config{
//		Storage:     storageClient,
//		IdleTimeout: 30 * time.Second,
//	})
//	defer pool.Close()
//
//	// Acquire a stream reader for an HTTP range request.
//	reader, release := pool.Acquire(infoHash, torrentFile, pieceLength, false, totalReadaheadBytes)
//	defer release()
//
//	// Serve content via standard library HTTP file server.
//	http.ServeContent(w, r, torrentFile.Path(), time.Time{}, reader)
//
// # Thread Safety
//
// All methods on Pool and acquired stream readers are fully thread-safe and designed
// for concurrent access by multiple goroutines.
package stream
