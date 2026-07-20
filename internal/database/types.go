// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"github.com/torrplay/torrplay/internal/api"
)

// Torrent represents a torrent in the database.
// It embeds the api.Torrent struct and adds an InfoBytes field.
type Torrent struct {
	api.Torrent
	InfoBytes []byte `json:"info_bytes,omitempty"`
}

// Settings represents the application settings in the database.
// It embeds the api.Settings struct and adds database-specific fields.
type Settings struct {
	api.Settings
	DLNAUDN   string `json:"dlna_udn,omitempty"`
	JWTSecret string `json:"jwt_secret,omitempty"`
}

// FromAPITorrent converts an api.Torrent to a database.Torrent.
func FromAPITorrent(t *api.Torrent) *Torrent {
	if t == nil {
		return nil
	}
	return &Torrent{
		Torrent: *t,
	}
}

// FromAPITorrents converts a slice of api.Torrent to a slice of database.Torrent.
func FromAPITorrents(ts []*api.Torrent) []*Torrent {
	torrents := make([]*Torrent, 0, len(ts))
	for _, t := range ts {
		torrents = append(torrents, FromAPITorrent(t))
	}
	return torrents
}

// ToAPITorrent converts a database.Torrent to an api.Torrent.
func ToAPITorrent(t *Torrent) *api.Torrent {
	if t == nil {
		return nil
	}
	return &t.Torrent
}

// ToAPITorrents converts a slice of database.Torrent to a slice of api.Torrent.
func ToAPITorrents(ts []*Torrent) []*api.Torrent {
	torrents := make([]*api.Torrent, 0, len(ts))
	for _, t := range ts {
		torrents = append(torrents, ToAPITorrent(t))
	}
	return torrents
}

// FromAPISettings converts an api.Settings to a database.Settings.
func FromAPISettings(s *api.Settings) *Settings {
	if s == nil {
		return nil
	}
	return &Settings{
		Settings: *s,
	}
}

// ToAPISettings converts a database.Settings to an api.Settings.
func ToAPISettings(s *Settings) *api.Settings {
	if s == nil {
		return nil
	}
	return &s.Settings
}
