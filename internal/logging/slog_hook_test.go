// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogHook_Levels(t *testing.T) {
	hook := NewSlogHook(nil) // No logger needed for this test
	assert.Equal(t, logrus.AllLevels, hook.Levels())
}

func TestSlogHook_Fire(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	hook := NewSlogHook(logger)

	tests := []struct {
		name              string
		level             logrus.Level
		message           string
		data              logrus.Fields
		expectedSlogLevel slog.Level
	}{
		{"info", logrus.InfoLevel, "test info", logrus.Fields{"key": "value"}, slog.LevelInfo},
		{"debug", logrus.DebugLevel, "test debug", logrus.Fields{"num": 123}, slog.LevelDebug},
		{"warn", logrus.WarnLevel, "test warn", nil, slog.LevelWarn},
		{"error", logrus.ErrorLevel, "test error", logrus.Fields{"err": "something went wrong"}, slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()

			entry := &logrus.Entry{
				Level:   tt.level,
				Message: tt.message,
				Data:    tt.data,
			}

			err := hook.Fire(entry)
			require.NoError(t, err)

			var logOutput map[string]interface{}
			err = json.Unmarshal(buf.Bytes(), &logOutput)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedSlogLevel.String(), logOutput["level"])
			assert.Equal(t, tt.message, logOutput["msg"])

			// Check that data fields are present
			if tt.data != nil {
				for k, v := range tt.data {
					assert.EqualValues(t, v, logOutput[k])
				}
			}
		})
	}
}

func TestSlogHook_toSlogLevel(t *testing.T) {
	hook := NewSlogHook(nil) // No logger needed for this test

	assert.Equal(t, slog.LevelDebug, hook.toSlogLevel(logrus.TraceLevel))
	assert.Equal(t, slog.LevelDebug, hook.toSlogLevel(logrus.DebugLevel))
	assert.Equal(t, slog.LevelInfo, hook.toSlogLevel(logrus.InfoLevel))
	assert.Equal(t, slog.LevelWarn, hook.toSlogLevel(logrus.WarnLevel))
	assert.Equal(t, slog.LevelError, hook.toSlogLevel(logrus.ErrorLevel))
	assert.Equal(t, slog.LevelError, hook.toSlogLevel(logrus.FatalLevel))
	assert.Equal(t, slog.LevelError, hook.toSlogLevel(logrus.PanicLevel))
}
