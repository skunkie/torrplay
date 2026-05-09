// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package logging

import (
	"log/slog"
	"sync"
	"time"
)

const defaultLogStoreSize = 100

// DefaultStore is the global, in-memory store for log entries.
// It is used to collect logs that can be later retrieved via an API endpoint.
var DefaultStore = NewStore(defaultLogStoreSize)

// LogEntry represents a single log message with its associated metadata.
// It is designed to be a serializable representation of a log record.
type LogEntry struct {
	Time    time.Time              `json:"time"`
	Level   slog.Level             `json:"level"`
	Message string                 `json:"msg"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// Store is a thread-safe, in-memory store for log entries.
// It uses a circular buffer to keep a fixed number of recent log entries.
type Store struct {
	mu      sync.RWMutex
	entries []LogEntry
	size    int
	head    int
	tail    int
	full    bool
}

// NewStore creates a new Store with the specified size.
func NewStore(size int) *Store {
	if size < 0 {
		size = 0
	}
	return &Store{
		entries: make([]LogEntry, size),
		size:    size,
	}
}

// Add adds a new log entry to the store.
// If the store is full, the oldest entry is overwritten.
func (s *Store) Add(entry LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(entry)
}

// add is an unlocked version of Add for internal use.
func (s *Store) add(entry LogEntry) {
	if s.size == 0 {
		return // Do not store logs if size is 0
	}

	s.entries[s.tail] = entry
	s.tail = (s.tail + 1) % s.size
	if s.full {
		s.head = s.tail
	} else if s.tail == s.head {
		s.full = true
	}
}

// Entries returns a copy of all log entries in the store.
func (s *Store) Entries() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.entriesInOrder()
}

// Resize changes the capacity of the log store.
// It preserves the most recent log entries up to the new size.
func (s *Store) Resize(newSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if newSize < 0 {
		newSize = 0
	}

	if newSize == s.size {
		return
	}

	existing := s.entriesInOrder()

	newStore := NewStore(newSize)

	// Take the last 'newSize' entries from existing.
	if len(existing) > newSize {
		existing = existing[len(existing)-newSize:]
	}

	for _, entry := range existing {
		newStore.add(entry)
	}

	s.entries = newStore.entries
	s.size = newStore.size
	s.head = newStore.head
	s.tail = newStore.tail
	s.full = newStore.full
}

// entriesInOrder is a helper function to get entries in chronological order.
func (s *Store) entriesInOrder() []LogEntry {
	if s.size == 0 {
		return []LogEntry{}
	}

	if !s.full {
		// Simple case: from head to tail
		// Need to return a copy
		c := make([]LogEntry, s.tail-s.head)
		copy(c, s.entries[s.head:s.tail])
		return c
	}

	// When the buffer is full, the order is from head to end, then from 0 to tail.
	entries := make([]LogEntry, 0, s.size)
	entries = append(entries, s.entries[s.head:]...)
	entries = append(entries, s.entries[:s.tail]...)

	return entries
}
