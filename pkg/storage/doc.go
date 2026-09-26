// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package storage provides a memory-limited, piece-level storage client for torrent downloads.
// It implements the storage.Client interface from the anacrolix/torrent library with efficient
// memory management and LRU-based eviction policies.
//
// # Overview
//
// The Client type manages torrent data storage with configurable memory limits. Unlike traditional
// file-based storage, this implementation keeps downloaded pieces in memory, making it suitable for
// scenarios where:
//
//   - Disk I/O should be minimized.
//   - Data needs to be served quickly to peers.
//   - Memory use must remain bounded.
//   - Downloaded data is temporary.
//
// # Key Features
//
//  1. Memory Management: Enforces a global memory limit across all torrents with automatic eviction
//     of least-recently-used pieces when limits are exceeded.
//
//  2. Piece Tracking: Maintains detailed information about each piece including completion status,
//     memory residency, and LRU position.
//
//  3. Multi-Torrent Support: Tracks memory usage per torrent while maintaining global limits.
//
//  4. Statistics: Provides comprehensive memory usage statistics at both global and per-torrent levels.
//
//  5. Self-Hashing: Implements the SelfHashing interface to verify piece integrity without external
//     hashing mechanisms. A piece whose buffer is not fully written, for example because it was
//     evicted mid-download, fails with ErrPieceIncomplete rather than a wrong hash, so the torrent
//     client re-downloads it instead of banning the peers that sent it.
//
//  6. Eviction Protection: Satisfies the stream.ProtectionRegistry interface so that
//     actively-read pieces and file boundary pieces are protected from standard LRU eviction.
//     Under memory pressure, boundary protection yields first; active ranges are evicted only
//     as a last resort to prevent download stalls.
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
//		// Create a storage client with a 1 GiB memory limit.
//		storageClient := storage.New(1<<30, slog.Default())
//
//		// Configure torrent client to use our storage.
//		config := torrent.NewDefaultClientConfig()
//		config.DefaultStorage = storageClient
//
//		client, err := torrent.NewClient(config)
//		if err != nil {
//			panic(err)
//		}
//		defer client.Close()
//
//		// Add and download torrents...
//	}
//
// # Memory Eviction
//
// When an allocation would exceed the configured limit, the client automatically evicts
// least-recently-used pieces. Eviction removes piece data and tracking from memory, causing
// evicted pieces to be reported as incomplete so the torrent engine can download them again on demand.
// Pieces still downloading, meaning incomplete and written within the last 30 seconds, are spared
// while other unprotected pieces can be evicted instead, because an evicted partial piece must be
// downloaded again in full. They are still evicted, oldest first, when nothing else frees enough memory.
// The torrent engine caches piece completion and is not told when storage drops a piece on its own.
// Register ClientEvictionHandler with SetEvictionHandler so every evicted piece, and every piece
// already gone when the engine marks it complete, is reported back to it from a background
// goroutine; otherwise it keeps treating evicted pieces as downloaded, skips them when reading
// ahead, and considers a torrent that was read in full complete, dropping its peers.
// When an incoming piece has the same size as a buffer detached during allocation-triggered
// eviction, the buffer is cleared and handed directly to the incoming reservation. This reduces
// allocation and garbage-collection churn without retaining an unaccounted free-buffer pool.
//
// # Thread Safety
//
// All public methods are thread-safe and can be called concurrently from multiple goroutines.
// The implementation uses fine-grained locking to minimize contention.
//
// # Limitations
//
//  1. Data Persistence: All data is stored in memory and not persisted to disk. Application
//     restarts will lose all downloaded data.
//
//  2. Memory Pressure: Large torrents or many concurrent torrents may exceed available memory,
//     causing frequent evictions and reduced performance.
//
//  3. Re-downloading on Eviction: When piece memory is evicted, the piece must be re-downloaded
//     from the peer swarm to access its data again.
//
// # Statistics and Monitoring
//
// The package provides two snapshot methods for monitoring storage usage:
//
//   - Client.MemoryStats provides global memory usage statistics.
//   - Client.TorrentStats provides detailed per-torrent and per-piece statistics.
//
// TorrentStats also provides the torrent's fraction of the memory limit.
//
// # Eviction Protection
//
// SetProtection replaces the piece ranges one owner, keyed by (info hash, owner
// ID), protects, and ClearProtection removes them. A Protection holds two kinds
// of ranges:
//
//   - Active ranges keep the pieces around a reader's playback position, or a
//     preload's pieces, in memory.
//   - File boundaries keep the head and tail of a streamed file, and with them
//     container metadata such as MP4 moov atoms or Matroska cues, resident while
//     a reader seeks.
//
// When an incoming piece needs space, eviction walks the LRU list in up to three passes,
// each stopping as soon as the allocation fits:
//
//  1. Evict pieces that are in neither an active range nor a file boundary.
//  2. Evict file boundary pieces, while still preserving active ranges.
//  3. Evict active range pieces, oldest first. This pass runs only when no allocation is
//     pending; otherwise the allocation waits for the pending reservation to publish or
//     refund its memory. It prevents ErrInsufficientMemory from making the torrent engine
//     disable data downloads.
//
// SetMaxMemory enforces a new limit with all three passes, waiting for pending
// allocations when necessary.
//
// # Error Handling
//
// The package defines several error conditions:
//
//   - ErrPieceNotAvailable indicates that a piece has never been written or has been evicted.
//   - ErrInsufficientMemory indicates that an allocation cannot fit even after eviction.
//   - ErrClientClosed indicates that the storage client has been closed.
//   - ErrTorrentClosed indicates that an operation used a closed torrent implementation.
//   - ErrTorrentNotManaged indicates that statistics were requested for an unmanaged torrent.
//
// # Implementation Details
//
// Internally, the client maintains:
//
//   - A global LRU list for eviction decisions.
//   - Synchronized per-piece metadata and data buffers.
//   - Per-torrent memory usage accounting.
//   - On-demand SHA-1 self-hashing of resident piece data.
//
// The implementation is designed to be efficient for the common case of sequential piece
// downloading while supporting random access patterns.
package storage
