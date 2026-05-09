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
	if len(os.Args) > 1 && os.Args[1] == "metadata" {
		return runMetadataTool()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runApp(ctx)
}
