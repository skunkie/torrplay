// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package logging

import (
	"context"
	"log/slog"
)

// StoreHandler is a slog.Handler that writes log records to a Store
// and passes them to a nested handler.
type StoreHandler struct {
	handler slog.Handler
	store   *Store
	attrs   []slog.Attr
}

// NewStoreHandler creates a new StoreHandler.
func NewStoreHandler(handler slog.Handler, store *Store) *StoreHandler {
	return &StoreHandler{
		handler: handler,
		store:   store,
	}
}

// Enabled implements slog.Handler.
func (h *StoreHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *StoreHandler) Handle(ctx context.Context, r slog.Record) error {
	logEntry := LogEntry{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Data:    make(map[string]interface{}),
	}
	// Add attributes from the handler itself.
	for _, a := range h.attrs {
		logEntry.Data[a.Key] = a.Value.Any()
	}
	// Add attributes from the record.
	r.Attrs(func(a slog.Attr) bool {
		logEntry.Data[a.Key] = a.Value.Any()
		return true
	})

	// Add to the store.
	h.store.Add(logEntry)

	// Pass to the next handler in the chain.
	return h.handler.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.
func (h *StoreHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &StoreHandler{
		handler: h.handler.WithAttrs(attrs),
		store:   h.store,
		attrs:   append(h.attrs, attrs...),
	}
}

// WithGroup implements slog.Handler.
func (h *StoreHandler) WithGroup(name string) slog.Handler {
	return &StoreHandler{
		handler: h.handler.WithGroup(name),
		store:   h.store,
		attrs:   h.attrs,
	}
}
