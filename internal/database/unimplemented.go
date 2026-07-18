// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"errors"

	"github.com/anacrolix/torrent/metainfo"
)

var _ DatabaseInterface = (*Unimplemented)(nil)

var ErrUnimplemented = errors.New("unimplemented")

// Unimplemented is a Database implementation that returns ErrUnimplemented for all methods.
// It is used for embedding in other implementations to ensure forward compatibility.
type Unimplemented struct{}

func (Unimplemented) CreateTorrent(*Torrent) error               { return ErrUnimplemented }
func (Unimplemented) GetTorrents() ([]*Torrent, error)           { return nil, ErrUnimplemented }
func (Unimplemented) GetTorrent(metainfo.Hash) (*Torrent, error) { return nil, ErrUnimplemented }
func (Unimplemented) UpdateTorrent(*Torrent) error               { return ErrUnimplemented }
func (Unimplemented) DeleteTorrent(metainfo.Hash) error          { return ErrUnimplemented }
func (Unimplemented) IsPosterUsed(string) (bool, error)          { return false, ErrUnimplemented }

func (Unimplemented) GetSettings() (*Settings, error) { return nil, ErrUnimplemented }
func (Unimplemented) UpdateSettings(*Settings) error  { return ErrUnimplemented }

func (Unimplemented) GetDLNAUDN() (string, error)   { return "", ErrUnimplemented }
func (Unimplemented) GetJWTSecret() (string, error) { return "", ErrUnimplemented }
