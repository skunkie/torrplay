// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package testutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// VerifyTestMain runs m.Run() and verifies that no goroutines from torrplay packages leak.
// It retries briefly to allow asynchronous teardown and background workers to exit cleanly,
// and filters out goroutines from third-party libraries (e.g. anacrolix/torrent, pion, helix).
func VerifyTestMain(m *testing.M, excludedSubstrings ...string) {
	exitCode := m.Run()
	if exitCode != 0 {
		os.Exit(exitCode)
	}

	// Retry briefly to allow standard goroutines to terminate cleanly.
	var lastLeaks []string
	for range 20 {
		err := goleak.Find()
		if err == nil {
			os.Exit(0)
		}

		lastLeaks = nil
		goroutines := strings.SplitSeq(err.Error(), "Goroutine ")
		for gr := range goroutines {
			if !strings.Contains(gr, "github.com/torrplay/torrplay") {
				continue
			}

			excluded := false
			for _, excl := range excludedSubstrings {
				if excl != "" && strings.Contains(gr, excl) {
					excluded = true
					break
				}
			}

			if !excluded {
				lastLeaks = append(lastLeaks, "Goroutine "+gr)
			}
		}

		if len(lastLeaks) == 0 {
			// All remaining goroutines belong to 3rd-party engines or excluded components.
			os.Exit(0)
		}
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "goleak: found unexpected goroutines in torrplay packages:\n%s\n", strings.Join(lastLeaks, "\n"))
	os.Exit(1)
}
