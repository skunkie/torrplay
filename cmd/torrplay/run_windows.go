// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const serviceName = "TorrPlay"

type windowsService struct{}

func (s *windowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Set the environment variable to indicate the service context.
	if err := os.Setenv("TORRPLAY_RUNNING_AS_SERVICE", "true"); err != nil {
		return false, 101 // Specific error code for env var failure
	}

	elog, err := eventlog.Open(serviceName)
	if err != nil {
		// If we can't open the event log, it's a critical failure for a service.
		return false, 102 // Specific error code for event log failure
	}
	defer elog.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runApp(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
	elog.Info(1, fmt.Sprintf("%s service started", serviceName))

	for {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				elog.Error(3, fmt.Sprintf("service error: %v", err))
			} else {
				elog.Info(2, fmt.Sprintf("%s service stopped", serviceName))
			}
			changes <- svc.Status{State: svc.Stopped}
			return

		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
			default:
				elog.Error(3, fmt.Sprintf("unexpected control request #%d", c))
			}
		}
	}
}

func run() error {
	isWindowsService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("failed to determine if running as a service: %w", err)
	}

	if !isWindowsService {
		// Handle command-line arguments for non-service mode
		if len(os.Args) > 1 && strings.ToLower(os.Args[1]) == "metadata" {
			return runMetadataTool()
		}
		// Run as a console app otherwise
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runApp(ctx)
	}

	// Run as a service
	return svc.Run(serviceName, &windowsService{})
}
