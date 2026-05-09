// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package torrplay

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/torrplay/torrplay/internal/controller"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/metrics"
)

// App encapsulates the application's state and lifecycle.
type App struct {
	ctrl *controller.Controller
}

// New creates a new TorrPlay application instance.
//
// It initializes all the necessary components for the application to run, but it
// does not start the services. The `Start` method must be called to begin
// serving requests.
//
// `dataDir` specifies the directory where TorrPlay will store its data,
// including configuration files and torrent metadata.
//
// `ipAddr` sets the IP address for the HTTP server to listen on.
//
// `port` sets the port for the HTTP server to listen on. It overrides the
// setting stored in the database if the value is not less than 1 and not
// greater than 65535.
func New(dataDir string, ipAddr string, port int) (*App, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("could not create data directory: %w", err)
	}

	if net.ParseIP(ipAddr) == nil {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddr)
	}

	configDBPath := filepath.Join(dataDir, "config.db")
	configClient, err := database.NewBBoltDB(configDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create config database client: %w", err)
	}

	imagesDBPath := filepath.Join(dataDir, "posters.db")
	imagesSvc, err := images.NewBBoltDBService(imagesDBPath)
	if err != nil {
		_ = configClient.Close()
		return nil, fmt.Errorf("failed to create images service: %w", err)
	}

	metrics := metrics.New()

	c, err := controller.NewController(dataDir, ipAddr, port, configClient, imagesSvc, metrics)
	if err != nil {
		_ = configClient.Close()
		_ = imagesSvc.Close()
		return nil, fmt.Errorf("failed to create controller: %w", err)
	}

	return &App{ctrl: c}, nil
}

// Start sets up the HTTP server and runs it in a non-blocking way.
func (a *App) Start() {
	a.ctrl.Start()
}

// Stop gracefully shuts down the Torrplay service.
func (a *App) Stop() {
	if a != nil && a.ctrl != nil {
		a.ctrl.Shutdown()
	}
}
