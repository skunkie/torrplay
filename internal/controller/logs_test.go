// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/logging"
)

func TestFilterLogEntries(t *testing.T) {
	entries := []logging.LogEntry{
		{Time: time.Unix(1, 0), Level: slog.LevelInfo, Message: "server started"},
		{Time: time.Unix(2, 0), Level: slog.LevelError, Message: "metadata failed", Data: map[string]any{"hash": "ABC123", "error": "timeout"}},
		{Time: time.Unix(3, 0), Level: slog.LevelWarn, Message: "tracker slow", Data: map[string]any{"tracker": "UDP://EXAMPLE"}},
	}

	assert.Equal(t, []string{"tracker slow", "metadata failed", "server started"}, logMessages(filterLogEntries(entries, "", "")))
	assert.Equal(t, []string{"metadata failed"}, logMessages(filterLogEntries(entries, "abc123", "ERROR")))
	assert.Equal(t, []string{"tracker slow"}, logMessages(filterLogEntries(entries, "example", "WARN")))
	assert.Empty(t, filterLogEntries(entries, "timeout", "INFO"))
}

func TestGetLogsRejectsInvalidLevel(t *testing.T) {
	controller := &Controller{}
	recorder := httptest.NewRecorder()
	level := api.GetLogsParamsLevel("TRACE")
	controller.GetLogs(recorder, httptest.NewRequest(http.MethodGet, "/api/system/logs", http.NoBody), api.GetLogsParams{Level: &level})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestLogEntryJSONContract(t *testing.T) {
	entry := logging.LogEntry{Time: time.Unix(2, 0), Level: slog.LevelError, Message: "metadata failed"}
	encoded, err := json.Marshal(entry)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	assert.Equal(t, "metadata failed", result["message"])
	assert.Equal(t, "ERROR", result["level"])
}

func logMessages(entries []logging.LogEntry) []string {
	result := make([]string, len(entries))
	for i, entry := range entries {
		result[i] = entry.Message
	}
	return result
}
