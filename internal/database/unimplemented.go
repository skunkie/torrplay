// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"errors"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/torrplay/torrplay/internal/api"
)

var _ DatabaseInterface = (*Unimplemented)(nil)

var ErrUnimplemented = errors.New("unimplemented")

// Unimplemented is a Database implementation that returns ErrUnimplemented for all methods.
// It is used for embedding in other implementations to ensure forward compatibility.
type Unimplemented struct{}

func (Unimplemented) CreateTorrent(*api.Torrent) error               { return ErrUnimplemented }
func (Unimplemented) GetTorrents() ([]*api.Torrent, error)           { return nil, ErrUnimplemented }
func (Unimplemented) GetTorrent(metainfo.Hash) (*api.Torrent, error) { return nil, ErrUnimplemented }
func (Unimplemented) UpdateTorrent(*api.Torrent) error               { return ErrUnimplemented }
func (Unimplemented) DeleteTorrent(metainfo.Hash) error              { return ErrUnimplemented }
func (Unimplemented) IsPosterUsed(string) (bool, error)              { return false, ErrUnimplemented }

func (Unimplemented) GetSettings() (*api.Settings, error) { return nil, ErrUnimplemented }
func (Unimplemented) UpdateSettings(*api.Settings) error  { return ErrUnimplemented }

func (Unimplemented) GetDLNAUDN() (string, error)   { return "", ErrUnimplemented }
func (Unimplemented) GetJWTSecret() (string, error) { return "", ErrUnimplemented }
