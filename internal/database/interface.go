// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"github.com/anacrolix/torrent/metainfo"
)

type DatabaseInterface interface {
	CreateTorrent(t *Torrent) error
	GetTorrents() ([]*Torrent, error)
	GetTorrent(ih metainfo.Hash) (*Torrent, error)
	UpdateTorrent(t *Torrent) error
	DeleteTorrent(ih metainfo.Hash) error
	IsPosterUsed(posterID string) (bool, error)

	GetSettings() (*Settings, error)
	UpdateSettings(s *Settings) error

	GetDLNAUDN() (string, error)
	GetJWTSecret() (string, error)
}
