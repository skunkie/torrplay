// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package buildinfo

// These variables are populated at build time via -ldflags.
var (
	BuildDate = "2006-01-02"
	Commit    = "unknown"
	Version   = "unknown"
)
