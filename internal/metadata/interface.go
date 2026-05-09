// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metadata

import "github.com/torrplay/torrplay/internal/api"

// Options represents the options for fetching metadata.
type Options struct {
	Category bool
	Language string
	Poster   bool
	Title    bool
}

// Provider is an interface for a metadata provider.
type Provider interface {
	UpdateMetadata(backup api.Backup, opts Options) (api.Backup, error)
}
