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
// - Disk I/O should be minimized.
// - Data needs to be served quickly to peers.
// - Memory is plentiful but limited.
// - Temporary storage of downloaded content is required.
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
// # Usage Example
//
//	package main
//
//	import (
//		"context"
//		"log/slog"
//
//		"github.com/anacrolix/torrent"
//		"github.com/torrplay/torrplay/pkg/storage"
//	)
//
//	func main() {
//		// Create a storage client with 1GB memory limit.
//		storageClient := storage.NewClient(1<<30, slog.Default())
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
// When the total memory usage exceeds the configured limit, the client automatically evicts
// least-recently-used pieces. Eviction only removes piece data from memory; metadata about
// piece completion status is preserved. Re-downloading evicted pieces is required to access
// their data again.
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
//  3. Completion Tracking: While piece completion status is tracked, the actual piece data
//     may be evicted. This means a piece can be marked as "complete" but not have its data
//     in memory.
//
// # Statistics and Monitoring
//
// The package provides several methods for monitoring storage usage:
// - GetMemoryStats(): Global memory usage statistics.
// - GetTorrentMemoryStats(): Per-torrent detailed statistics.
// - GetPieceStatus(): Individual piece information.
// - Various helper methods for tracking completion progress and memory usage.
//
// # Error Handling
//
// The package defines several error conditions:
// - ErrPieceNotAvailable: Returned when reading a piece that has been evicted from memory.
// - ErrInsufficientMemory: When memory limits cannot be satisfied even after eviction.
//
// # Implementation Details
//
// Internally, the client maintains:
// - A global LRU list for eviction decisions.
// - Per-piece metadata with synchronization.
// - Per-torrent memory usage tracking.
// - Piece hashes for integrity verification.
//
// The implementation is designed to be efficient for the common case of sequential piece
// downloading while supporting random access patterns.
package storage
