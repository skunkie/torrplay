// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
)

type DatabaseInterface interface {
	CreateTorrent(*api.Torrent) error
	GetTorrents() ([]*api.Torrent, error)
	GetTorrent(metainfo.Hash) (*api.Torrent, error)
	UpdateTorrent(*api.Torrent) error
	DeleteTorrent(metainfo.Hash) error
	IsPosterUsed(string) (bool, error)

	GetSettings() (*api.Settings, error)
	UpdateSettings(*api.Settings) error

	GetDLNAUDN() (string, error)
	GetJWTSecret() (string, error)
}
