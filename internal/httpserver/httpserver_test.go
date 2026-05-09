// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Lifecycle(t *testing.T) {
	// Setup a simple router for testing.
	router := chi.NewRouter()
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	addr := "127.0.0.1:8081"
	s := NewServer(router, addr, logger)

	// Start the server in a goroutine.
	go func() {
		// A clean shutdown is expected to return a nil error.
		assert.NoError(t, s.Run(), "server.Run() should be nil on clean shutdown")
	}()

	// Give the server a moment to start.
	<-time.After(100 * time.Millisecond)

	// Make a request to verify it's running.
	resp, err := http.Get("http://" + addr)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "hello", string(body))

	// Test Restart.
	newRouter := chi.NewRouter()
	newRouter.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("restarted"))
	})

	newAddr := "127.0.0.1:8082"
	s.SetRouter(newRouter)
	s.SetAddr(newAddr)

	err = s.Restart()
	require.NoError(t, err)

	// Give the server a moment to restart.
	<-time.After(100 * time.Millisecond)

	// Make a request to the new address to verify restart.
	resp, err = http.Get("http://" + newAddr)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ = io.ReadAll(resp.Body)
	assert.Equal(t, "restarted", string(body))

	// Verify the old address is no longer responding.
	_, err = http.Get("http://" + addr)
	assert.Error(t, err, "request to old server address should fail after restart")

	// Test Shutdown.
	err = s.Shutdown()
	require.NoError(t, err)

	// Give the server a moment to shut down.
	<-time.After(100 * time.Millisecond)

	// Verify the server is no longer running.
	_, err = http.Get("http://" + newAddr)
	assert.Error(t, err, "request to shutdown server should fail")
}

func TestServer_ImmediateShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(nil, "127.0.0.1:8083", logger)

	// Start and immediately shut down.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// A clean shutdown is expected to return a nil error.
		assert.NoError(t, s.Run(), "server.Run() should return nil on immediate shutdown")
	}()

	// Immediately shut down the server.
	err := s.Shutdown()
	require.NoError(t, err)
	wg.Wait()
}

func TestServer_ShutdownWithoutStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(nil, "127.0.0.1:8084", logger)

	// Shutdown should be a no-op if the server hasn't started.
	err := s.Shutdown()
	assert.NoError(t, err)
}

func TestServer_RestartFailedShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test that requires a timeout")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	addr := "127.0.0.1:8085"
	s := NewServer(chi.NewRouter(), addr, logger)

	// Manually create a server instance that will fail to shut down gracefully.
	mockServer := &http.Server{
		Addr: s.addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// This handler will block, preventing a graceful shutdown.
			<-time.After(10 * time.Second)
		}),
	}

	s.mu.Lock()
	s.server = mockServer
	s.mu.Unlock()

	go func() {
		// This can return an error "address already in use" if the OS hasn't
		// freed the port from a previous test. That's fine.
		// The main thing is that it's listening.
		_ = s.server.ListenAndServe()
	}()
	<-time.After(50 * time.Millisecond)

	// Make a request that will hang.
	go func() {
		_, _ = http.Get("http://" + addr)
	}()
	// Give the request time to be accepted by the server.
	<-time.After(50 * time.Millisecond)

	// Restart should fail because shutdown will time out.
	err := s.Restart()
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// Test that Run() returns an error for an invalid address.
func TestServer_RunFails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	addr := "127.0.0.1:8086"

	// Start a listener on the address to simulate a port conflict.
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	defer listener.Close()

	s := NewServer(nil, addr, logger)

	// Run the server in a goroutine and check for an error.
	errChan := make(chan error)
	go func() {
		errChan <- s.Run()
	}()

	select {
	case err := <-errChan:
		assert.Error(t, err, "server.Run() should return an error for an invalid address")
	case <-time.After(1 * time.Second):
		t.Fatal("server.Run() did not return an error within the time limit")
	}
}

// Test that calling Start() on an already running server is a no-op.
func TestServer_DoubleStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(nil, "127.0.0.1:8087", logger)

	err := s.Start()
	require.NoError(t, err)

	// Calling Start() again should not cause an error.
	err = s.Start()
	assert.NoError(t, err, "calling Start() on an already running server should be a no-op")

	err = s.Shutdown()
	require.NoError(t, err)
}

// Test that calling Shutdown() multiple times is a no-op.
func TestServer_DoubleShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(nil, "127.0.0.1:8088", logger)

	err := s.Start()
	require.NoError(t, err)

	err = s.Shutdown()
	require.NoError(t, err)

	// Calling Shutdown() again should be a no-op.
	err = s.Shutdown()
	assert.NoError(t, err, "calling Shutdown() multiple times should be a no-op")
}

// Test that SetRouter updates the handler on a running server without a restart.
func TestServer_SetRouterOnRunningServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	addr := "127.0.0.1:8089"

	// Initial router.
	router1 := chi.NewRouter()
	router1.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("router1"))
	})

	s := NewServer(router1, addr, logger)
	go func() {
		// We expect a nil error on clean shutdown.
		assert.NoError(t, s.Run())
	}()
	<-time.After(50 * time.Millisecond) // Wait for server to start.

	// Verify initial router.
	resp, err := http.Get("http://" + addr)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "router1", string(body))

	// New router.
	router2 := chi.NewRouter()
	router2.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("router2"))
	})

	// Update router on the fly.
	s.SetRouter(router2)
	<-time.After(50 * time.Millisecond)

	// Verify new router is active.
	resp, err = http.Get("http://" + addr)
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "router2", string(body))

	err = s.Shutdown()
	require.NoError(t, err)
}

// TestConnStateHandling verifies that active connections are correctly tracked
// and closed upon shutdown.
func TestConnStateHandling(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	addr := "127.0.0.1:8090"

	// This handler will block, keeping the connection active.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // Wait until the connection is closed by the client or server.
	})

	s := NewServer(handler, addr, logger)
	go func() {
		assert.NoError(t, s.Run())
	}()
	<-time.After(50 * time.Millisecond) // Wait for server to start.

	// Make a request that will hang.
	go func() {
		_, _ = http.Get("http://" + addr)
	}()
	<-time.After(50 * time.Millisecond) // Give the request time to be accepted.

	s.connMu.RLock()
	assert.Equal(t, 1, len(s.activeConns), "should have one active connection")
	s.connMu.RUnlock()

	// Shutdown should close the active connection.
	err := s.Shutdown()
	require.NoError(t, err)

	s.connMu.RLock()
	assert.Equal(t, 0, len(s.activeConns), "should have no active connections after shutdown")
	s.connMu.RUnlock()
}
