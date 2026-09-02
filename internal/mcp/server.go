// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/torrplay/torrplay/internal/buildinfo"
)

const sseShutdownTimeout = 5 * time.Second

// NewServer creates a new configured MCP server backed by the given TorrPlay client.
func NewServer(client *Client) *server.MCPServer {
	version := buildinfo.Version
	if version == "" || version == "unknown" {
		version = "1.0.0"
	}

	instructions := "TorrPlay MCP server provides tools to manage torrents, inspect files, generate streaming URLs, and monitor memory and streaming statistics."

	s := server.NewMCPServer(
		"TorrPlay",
		version,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
		server.WithInstructions(instructions),
	)

	registerTools(s, client)
	registerResources(s, client)
	registerPrompts(s, client)

	return s
}

// ServeStdio starts the MCP server using standard input and output.
// Note: All logging must go to stderr, as stdout is strictly reserved for JSON-RPC messages.
func ServeStdio(s *server.MCPServer) error {
	logger := log.New(os.Stderr, "[mcp] ", log.LstdFlags)
	return server.ServeStdio(s, server.WithErrorLogger(logger))
}

// ServeSSE starts an SSE-based HTTP server on a loopback address.
func ServeSSE(ctx context.Context, s *server.MCPServer, addr string) error {
	if err := validateLoopbackAddress(addr); err != nil {
		return err
	}
	sseServer := server.NewSSEServer(s)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           sseServer,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), sseShutdownTimeout)
		defer cancel()
		return httpServer.Shutdown(shutCtx)
	case err := <-errCh:
		return fmt.Errorf("SSE server error: %w", err)
	}
}

func validateLoopbackAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid SSE listen address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("SSE listen address %q must use a loopback IP; expose it through an authenticated reverse proxy for remote access", addr)
	}
	return nil
}
