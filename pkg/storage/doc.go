// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package storage provides a memory-limited, piece-level storage client for torrent downloads
// and high-performance media streaming. It implements the storage.Client and storage.PieceCompletion
// interfaces from the anacrolix/torrent library with proximity-weighted eviction and active range protection.
//
// # Overview
//
// The Client type manages torrent piece data in memory with configurable memory limits. Unlike traditional
// file-based storage, this implementation keeps active and downloaded pieces resident in RAM, making it
// optimal for media streaming and scenarios where:
//   - Disk I/O, wear, and storage footprint should be eliminated.
//   - Pieces must be served with minimum latency to players and peers.
//   - Memory is bounded and shared across multiple concurrent torrents and streams.
//
// # Key Features
//
//  1. Global Memory Management: Enforces a configurable global memory limit across all active torrents,
//     automatically reclaiming memory when limits are exceeded.
//
//  2. Proximity-Weighted Eviction: Employs a multi-tiered proximity scoring
//     algorithm that prioritizes pieces based on their distance to active stream reader positions.
//
//  3. Active Range & Stream Reader Protection: Supports concurrent streaming sessions with independent
//     reader IDs, protecting both active playback windows and trailing demuxer buffers from eviction.
//
//  4. Seamless Re-downloading on Eviction: When pieces are evicted under memory pressure, the storage
//     layer reports them as incomplete to anacrolix/torrent, allowing players to seek backwards and forwards
//     freely while the engine dynamically re-downloads required pieces.
//
//  5. Self-Hashing Integrity: Implements the storage.SelfHashing interface to verify piece SHA1 checksums
//     directly in memory without external hashing pipelines.
//
//  6. Detailed Metrics & Monitoring: Provides real-time global and per-torrent memory consumption,
//     residency maps, and piece completion statistics.
//
// # Proximity-Weighted Eviction Model
//
// When memory limits are reached, pieces are evaluated and assigned an eviction-protection score
// (higher score = greater protection against eviction):
//
//   - Tier 3 — In-Window & Trailing Buffer (Score: 100,000–1,000,000):
//     Pieces currently inside or immediately behind an active stream reader's window. Pieces closest to
//     the active read head receive the highest protection, preventing playback stutter.
//
//   - Tier 2 — Forward Prefetch / Readahead (Score: 1–10,000):
//     Pieces ahead of the reader head within the readahead window. Protection decays smoothly with distance,
//     prioritizing pieces that will be consumed next.
//
//   - Tier 1.5 — Backward History / Rewind Buffer (Score: 1–5,000):
//     Pieces behind the trailing buffer. Retains moderate protection decaying with distance to accommodate
//     short rewinds and player demuxer lookbacks without triggering immediate re-downloads.
//
//   - Tier 0 — Inactive & Unreferenced Pieces (Score: 0):
//     Pieces belonging to torrents or files without active readers. These are evicted first.
//
//   - LRU Tie-Breaker:
//     Among pieces with identical proximity scores, the piece least recently used (furthest back in the global
//     LRU list) is evicted first.
//
// # Stream Reader Tracking
//
// Readers register and update their streaming byte positions using SetActiveRange:
//
//	storageClient.SetActiveRange(infoHash, readerID, windowStartBytes, windowEndBytes)
//
// When a reader finishes or is closed, its range is unregistered:
//
//	storageClient.ClearActiveRange(infoHash, readerID)
//
// Multiple readers on the same or different torrents operate concurrently, each maintaining its own
// protected window in the shared memory pool.
//
// # Usage Example
//
//	package main
//
//	import (
//		"log/slog"
//
//		"github.com/anacrolix/torrent"
//		"github.com/torrplay/torrplay/pkg/storage"
//	)
//
//	func main() {
//		// Create a storage client with a 64MB memory limit.
//		storageClient := storage.NewClient(64*1024*1024, slog.Default())
//		defer storageClient.Close()
//
//		// Configure anacrolix/torrent to use memory storage.
//		config := torrent.NewDefaultClientConfig()
//		config.DefaultStorage = storageClient
//
//		client, err := torrent.NewClient(config)
//		if err != nil {
//			panic(err)
//		}
//		defer client.Close()
//
//		// Download and stream torrents...
//	}
//
// # Thread Safety
//
// All public methods on Client are fully thread-safe and safe for concurrent use across multiple
// goroutines, HTTP handlers, and streaming readers. Fine-grained mutexes protect individual piece buffers,
// torrent states, and the global LRU list independently to minimize lock contention.
//
// # Statistics and Monitoring
//
// The package exposes several inspection methods:
//   - GetMemoryStats: Returns global statistics (total memory used, max memory, active torrent count).
//   - GetTorrentMemoryStats: Returns detailed per-torrent metrics, including memory usage percentage,
//     in-memory piece counts, and individual piece statuses.
//   - GetPieceStatus: Returns completion and residency status for a specific piece index.
//
// # Error Conditions
//
//   - ErrPieceNotAvailable: Returned when attempting to read a piece whose data is not resident in memory.
//   - ErrInsufficientMemory: Returned when an allocation cannot be fulfilled even after evicting all eligible pieces.
package storage
