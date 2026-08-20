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
//     hashing mechanisms.
//
//  6. Active Range Protection: Satisfies the stream.ActiveRangeRegistry interface so that
//     actively-read pieces are protected from standard LRU eviction. The stream package calls
//     SetActiveRange to register a reader's readahead window and ClearActiveRange when the
//     reader is released. Under severe memory pressure where active ranges alone exceed
//     available memory, emergency eviction evicts the oldest LRU piece to prevent download stalls.
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
// TorrentStats also provides derived completion and memory-usage fractions.
//
// # Active Range Tracking
//
// The storage client maintains a map of active ranges keyed by (info hash, reader ID).
// When SetActiveRange is called, the given piece-index window is marked as protected from
// standard LRU eviction. ClearActiveRange removes the protection. This interface is consumed
// by the stream pool to ensure pieces within the current readahead window stay in memory
// during playback. If memory pressure prevents allocating a new incoming piece because
// protected ranges consume all available RAM, emergency eviction evicts the oldest LRU piece
// as a last resort to keep the torrent engine from disabling data downloads.
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
//   - ErrEvictionTargetNotReached indicates that protected pieces prevented manual eviction.
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
