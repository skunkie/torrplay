// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package logging

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewStore(t *testing.T) {
	t.Run("positive size", func(t *testing.T) {
		store := NewStore(10)
		assert.NotNil(t, store)
		assert.Equal(t, 10, store.size)
		assert.Len(t, store.entries, 10)
	})

	t.Run("zero size", func(t *testing.T) {
		store := NewStore(0)
		assert.NotNil(t, store)
		assert.Equal(t, 0, store.size)
		assert.Len(t, store.entries, 0)
	})

	t.Run("negative size", func(t *testing.T) {
		store := NewStore(-1)
		assert.NotNil(t, store)
		assert.Equal(t, 0, store.size)
		assert.Len(t, store.entries, 0)
	})
}

func TestStore_Add_And_Entries(t *testing.T) {
	t.Run("add to non-full store", func(t *testing.T) {
		store := NewStore(5)
		entry1 := LogEntry{Message: "first"}
		entry2 := LogEntry{Message: "second"}
		store.Add(entry1)
		store.Add(entry2)

		entries := store.Entries()
		assert.Len(t, entries, 2)
		assert.Equal(t, "first", entries[0].Message)
		assert.Equal(t, "second", entries[1].Message)
	})

	t.Run("add to make store full", func(t *testing.T) {
		store := NewStore(3)
		for i := 0; i < 3; i++ {
			store.Add(LogEntry{Message: fmt.Sprintf("message %d", i)})
		}

		entries := store.Entries()
		assert.Len(t, entries, 3)
		assert.Equal(t, "message 0", entries[0].Message)
		assert.Equal(t, "message 1", entries[1].Message)
		assert.Equal(t, "message 2", entries[2].Message)
	})

	t.Run("add to already full store", func(t *testing.T) {
		store := NewStore(3)
		for i := 0; i < 5; i++ {
			store.Add(LogEntry{Message: fmt.Sprintf("message %d", i)})
		}

		entries := store.Entries()
		assert.Len(t, entries, 3)
		assert.Equal(t, "message 2", entries[0].Message)
		assert.Equal(t, "message 3", entries[1].Message)
		assert.Equal(t, "message 4", entries[2].Message)
	})

	t.Run("add to zero-size store", func(t *testing.T) {
		store := NewStore(0)
		store.Add(LogEntry{Message: "test"})
		assert.Empty(t, store.Entries())
	})
}

func TestStore_Resize(t *testing.T) {
	t.Run("resize to larger", func(t *testing.T) {
		store := NewStore(3)
		for i := 0; i < 5; i++ { // Overfill
			store.Add(LogEntry{Message: fmt.Sprintf("message %d", i)})
		}
		// store has ["message 2", "message 3", "message 4"].

		store.Resize(5)
		assert.Equal(t, 5, store.size)
		entries := store.Entries()
		assert.Len(t, entries, 3)
		assert.Equal(t, "message 2", entries[0].Message)
		assert.Equal(t, "message 3", entries[1].Message)
		assert.Equal(t, "message 4", entries[2].Message)

		store.Add(LogEntry{Message: "message 5"})
		entries = store.Entries()
		assert.Len(t, entries, 4)
		assert.Equal(t, "message 5", entries[3].Message)
	})

	t.Run("resize to smaller", func(t *testing.T) {
		store := NewStore(5)
		for i := 0; i < 5; i++ {
			store.Add(LogEntry{Message: fmt.Sprintf("message %d", i)})
		}

		store.Resize(3)
		assert.Equal(t, 3, store.size)
		entries := store.Entries()
		assert.Len(t, entries, 3)
		assert.Equal(t, "message 2", entries[0].Message)
		assert.Equal(t, "message 3", entries[1].Message)
		assert.Equal(t, "message 4", entries[2].Message)
	})

	t.Run("resize to same size", func(t *testing.T) {
		store := NewStore(3)
		store.Add(LogEntry{Message: "a"})
		store.Resize(3)
		assert.Equal(t, 3, store.size)
		assert.Len(t, store.Entries(), 1)
	})

	t.Run("resize to zero", func(t *testing.T) {
		store := NewStore(5)
		store.Add(LogEntry{Message: "a"})
		store.Resize(0)
		assert.Equal(t, 0, store.size)
		assert.Empty(t, store.Entries())
		store.Add(LogEntry{Message: "b"})
		assert.Empty(t, store.Entries())
	})

	t.Run("resize from zero", func(t *testing.T) {
		store := NewStore(0)
		store.Resize(5)
		assert.Equal(t, 5, store.size)
		store.Add(LogEntry{Message: "a"})
		assert.Len(t, store.Entries(), 1)
	})
}

func TestStore_Concurrency(t *testing.T) {
	store := NewStore(1000)
	var wg sync.WaitGroup
	numGoroutines := 20
	numWritesPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numWritesPerGoroutine; j++ {
				store.Add(LogEntry{
					Message: "concurrent log",
					Level:   slog.LevelInfo,
					Time:    time.Now(),
				})
			}
		}()
	}

	var readCount int
	var readWg sync.WaitGroup
	readWg.Add(1)
	go func() {
		defer readWg.Done()
		for i := 0; i < 50; i++ {
			readCount += len(store.Entries())
			time.Sleep(10 * time.Millisecond)
		}
	}()

	wg.Wait()
	readWg.Wait()

	finalEntries := store.Entries()
	assert.True(t, len(finalEntries) <= 1000)
}
