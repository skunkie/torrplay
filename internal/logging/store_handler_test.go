// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockHandler is a mock slog.Handler to test if the underlying handler is called.
// It wraps a real JSONHandler to verify the final output while also tracking
// that key handler methods were invoked.
type mockHandler struct {
	buf         *bytes.Buffer
	jsonHandler slog.Handler
	enabled     bool
	handled     bool
}

func newMockHandler() *mockHandler {
	buf := &bytes.Buffer{}
	return &mockHandler{
		buf:         buf,
		jsonHandler: slog.NewJSONHandler(buf, &slog.HandlerOptions{}),
	}
}

func (h *mockHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.enabled = true
	return h.jsonHandler.Enabled(ctx, level)
}

func (h *mockHandler) Handle(ctx context.Context, r slog.Record) error {
	h.handled = true
	return h.jsonHandler.Handle(ctx, r)
}

func (h *mockHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Return a new mock handler that wraps a new underlying handler with the attributes.
	// This is critical to respecting the slog.Handler contract.
	return &mockHandler{
		buf:         h.buf,
		jsonHandler: h.jsonHandler.WithAttrs(attrs),
	}
}

func (h *mockHandler) WithGroup(name string) slog.Handler {
	return &mockHandler{
		buf:         h.buf,
		jsonHandler: h.jsonHandler.WithGroup(name),
	}
}

func TestStoreHandler_Enabled(t *testing.T) {
	store := NewStore(10)
	mock := newMockHandler()
	handler := NewStoreHandler(mock, store)

	assert.True(t, handler.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, mock.enabled, "underlying handler's Enabled should have been called")
}

func TestStoreHandler_Handle(t *testing.T) {
	store := NewStore(10)
	mock := newMockHandler()
	handler := NewStoreHandler(mock, store)
	logger := slog.New(handler)

	logger.Info("test message", "key", "value")

	// Check if log was stored.
	entries := store.Entries()
	assert.Len(t, entries, 1)
	assert.Equal(t, "test message", entries[0].Message)
	assert.Equal(t, slog.LevelInfo, entries[0].Level)
	assert.Equal(t, "value", entries[0].Data["key"])

	// Check if underlying handler was called.
	assert.True(t, mock.handled, "underlying handler's Handle should have been called")
	assert.Contains(t, mock.buf.String(), "test message")
}

func TestStoreHandler_WithAttrs(t *testing.T) {
	store := NewStore(10)
	mock := newMockHandler()
	handler := NewStoreHandler(mock, store)
	logger := slog.New(handler)

	logger = logger.With("pid", 123)
	logger.Info("another message")

	// Check if log was stored with the attribute.
	entries := store.Entries()
	assert.Len(t, entries, 1)
	assert.Equal(t, "another message", entries[0].Message)
	assert.Equal(t, int64(123), entries[0].Data["pid"], "slog converts numeric literals to int64")

	// Check if underlying handler has the attribute.
	assert.Contains(t, mock.buf.String(), `"pid":123`)
}
