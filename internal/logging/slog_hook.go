// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package logging

import (
	"context"
	"log/slog"

	"github.com/sirupsen/logrus"
)

// SlogHook is a Logrus hook that redirects log entries to an slog.Logger.
// It captures structured log data and forwards it to slog, ensuring consistent
// logging across the application. It also stores the most recent log entries
// in an in-memory store for retrieval via an API endpoint.
type SlogHook struct {
	logger *slog.Logger
}

func NewSlogHook(logger *slog.Logger) *SlogHook {
	return &SlogHook{
		logger: logger,
	}
}

func (h *SlogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *SlogHook) Fire(entry *logrus.Entry) error {
	// Create a slice of slog.Attr for structured logging.
	attrs := make([]slog.Attr, 0, len(entry.Data))
	for k, v := range entry.Data {
		attrs = append(attrs, slog.Any(k, v))
	}

	// Convert Logrus level to slog level.
	slogLevel := h.toSlogLevel(entry.Level)

	// Add the log entry to the in-memory store.
	logData := make(map[string]interface{}, len(entry.Data))
	for k, v := range entry.Data {
		logData[k] = v
	}
	DefaultStore.Add(LogEntry{
		Time:    entry.Time,
		Level:   slogLevel,
		Message: entry.Message,
		Data:    logData,
	})

	// Log the message using the slog logger.
	ctx := entry.Context
	if ctx == nil {
		ctx = context.Background()
	}
	h.logger.LogAttrs(ctx, slogLevel, entry.Message, attrs...)

	return nil
}

// toSlogLevel converts a Logrus log level to the corresponding slog level.
func (h *SlogHook) toSlogLevel(level logrus.Level) slog.Level {
	switch level {
	case logrus.TraceLevel, logrus.DebugLevel:
		return slog.LevelDebug
	case logrus.InfoLevel:
		return slog.LevelInfo
	case logrus.WarnLevel:
		return slog.LevelWarn
	case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
		return slog.LevelError
	default:
		return slog.LevelInfo // Default to Info level.
	}
}
