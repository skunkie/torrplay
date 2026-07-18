// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"github.com/anacrolix/torrent/metainfo"
)

type DatabaseInterface interface {
	CreateTorrent(*Torrent) error
	GetTorrents() ([]*Torrent, error)
	GetTorrent(metainfo.Hash) (*Torrent, error)
	UpdateTorrent(*Torrent) error
	DeleteTorrent(metainfo.Hash) error
	IsPosterUsed(string) (bool, error)

	GetSettings() (*Settings, error)
	UpdateSettings(*Settings) error

	GetDLNAUDN() (string, error)
	GetJWTSecret() (string, error)
}
