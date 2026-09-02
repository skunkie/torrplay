// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/mcp"
	"github.com/torrplay/torrplay/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m)
}

func TestRunMCP_FlagParsing(t *testing.T) {
	t.Run("invalid transport returns error", func(t *testing.T) {
		err := runMCP([]string{"--transport", "websocket"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported transport")
	})

	t.Run("invalid flag returns error", func(t *testing.T) {
		err := runMCP([]string{"--non-existent-flag"})
		require.Error(t, err)
	})

	t.Run("sse server starts and stops on context cancel", func(t *testing.T) {
		client := mcp.NewClient("http://127.0.0.1:8090", "", nil)
		s := mcp.NewServer(client)

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- mcp.ServeSSE(ctx, s, "127.0.0.1:0")
		}()

		// Allow server to spin up briefly, then cancel
		time.Sleep(50 * time.Millisecond)
		cancel()

		err := <-errCh
		assert.NoError(t, err)
	})

	t.Run("sse server rejects non-loopback listeners", func(t *testing.T) {
		client := mcp.NewClient("http://127.0.0.1:8090", "", nil)
		s := mcp.NewServer(client)

		err := mcp.ServeSSE(context.Background(), s, "0.0.0.0:8091")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must use a loopback IP")
	})
}
