// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "metadata":
			return runMetadataTool()
		case "mcp":
			return runMCPTool()
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runApp(ctx)
}
