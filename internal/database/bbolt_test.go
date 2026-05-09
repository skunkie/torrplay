// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"os"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
)

func tempfile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "bolt-")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestBBoltDB(t *testing.T) {
	dbPath := tempfile(t)
	db, err := NewBBoltDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	t.Run("Torrents", func(t *testing.T) {
		t.Run("Create and Get", func(t *testing.T) {
			torrent := &api.Torrent{
				Hash: metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10"),
				Name: "Sintel",
			}

			err := db.CreateTorrent(torrent)
			require.NoError(t, err)

			retrieved, err := db.GetTorrent(torrent.Hash)
			require.NoError(t, err)
			assert.Equal(t, torrent.Name, retrieved.Name)

			// Try to create the same torrent again.
			err = db.CreateTorrent(torrent)
			assert.ErrorIs(t, err, ErrTorrentExists)
		})

		t.Run("Update", func(t *testing.T) {
			torrent, err := db.GetTorrent(metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10"))
			require.NoError(t, err)

			newName := "Sintel Updated"
			torrent.Name = newName

			err = db.UpdateTorrent(torrent)
			require.NoError(t, err)

			updated, err := db.GetTorrent(torrent.Hash)
			require.NoError(t, err)
			assert.Equal(t, newName, updated.Name)
		})

		t.Run("Delete", func(t *testing.T) {
			hash := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
			err := db.DeleteTorrent(hash)
			require.NoError(t, err)

			_, err = db.GetTorrent(hash)
			assert.ErrorIs(t, err, ErrTorrentNotFound)
		})
	})

	t.Run("Settings", func(t *testing.T) {
		t.Run("Get and Update", func(t *testing.T) {
			settings, err := db.GetSettings()
			require.NoError(t, err)
			assert.NotNil(t, settings)

			newPort := 9091
			settings.HTTPServerPort = &newPort

			err = db.UpdateSettings(settings)
			require.NoError(t, err)

			updated, err := db.GetSettings()
			require.NoError(t, err)
			assert.Equal(t, newPort, *updated.HTTPServerPort)
		})
	})

	t.Run("DLNA", func(t *testing.T) {
		t.Run("Get UDN", func(t *testing.T) {
			udn, err := db.GetDLNAUDN()
			require.NoError(t, err)
			assert.NotEmpty(t, udn)

			// Getting it again should return the same one.
			udn2, err := db.GetDLNAUDN()
			require.NoError(t, err)
			assert.Equal(t, udn, udn2)
		})
	})
}
