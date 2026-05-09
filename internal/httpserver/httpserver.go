// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const defaultTimeout = 5 * time.Second

// Server manages the lifecycle of an HTTP server.
type Server struct {
	activeConns map[net.Conn]struct{}
	addr        string
	connMu      sync.RWMutex
	handler     http.Handler
	logger      *slog.Logger
	mu          sync.Mutex
	server      *http.Server
}

// NewServer creates a new instance of an HTTP server.
func NewServer(router http.Handler, addr string, logger *slog.Logger) *Server {
	s := &Server{
		activeConns: make(map[net.Conn]struct{}),
		addr:        addr,
		handler:     router,
		logger:      logger,
	}
	s.server = &http.Server{
		Addr:      addr,
		Handler:   s.handler,
		ConnState: s.connStateHandler,
	}
	return s
}

func (s *Server) connStateHandler(c net.Conn, cs http.ConnState) {
	s.connMu.Lock()
	defer s.connMu.Unlock()

	switch cs {
	case http.StateNew, http.StateActive:
		s.activeConns[c] = struct{}{}
	case http.StateIdle, http.StateClosed:
		delete(s.activeConns, c)
	}
}

// Run starts the HTTP server and blocks until it's stopped.
func (s *Server) Run() error {
	s.mu.Lock()
	server := s.server
	addr := s.addr
	s.mu.Unlock()

	if server == nil {
		// This can happen if Shutdown() is called right after the goroutine for Run() is created.
		// In this case, we can consider it a clean shutdown.
		return nil
	}

	s.logger.Info("starting HTTP server", "address", addr)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Start creates a new http.Server instance and starts it.
// This is used to start the server after it has been shut down.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		return nil // Already running.
	}

	s.server = &http.Server{
		Addr:      s.addr,
		Handler:   s.handler, // Re-use the existing handler.
		ConnState: s.connStateHandler,
	}

	go func() {
		if err := s.Run(); err != nil {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	return nil
}

// Restart gracefully restarts the server.
func (s *Server) Restart() error {
	s.logger.Info("restarting HTTP server")
	shutdownErr := s.Shutdown()
	startErr := s.Start()

	if shutdownErr != nil {
		return shutdownErr
	}

	return startErr
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server == nil {
		return nil
	}

	s.logger.Info("shutting down HTTP server")

	s.connMu.RLock()
	s.logger.Debug("closing active connections", "count", len(s.activeConns))
	for c := range s.activeConns {
		c.Close()
	}
	s.connMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	err := s.server.Shutdown(ctx)
	s.server = nil
	if err == nil {
		s.logger.Info("HTTP server stopped")
	}
	return err
}

// SetAddr updates the server's address.
func (s *Server) SetAddr(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addr = addr
}

// SetRouter updates the server's router.
func (s *Server) SetRouter(router http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = router
	if s.server != nil {
		s.server.Handler = router
	}
}
