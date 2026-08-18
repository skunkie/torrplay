// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"os"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
	"go.etcd.io/bbolt"
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
				Hash:   metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10"),
				Name:   "Sintel",
				Poster: utils.Ptr("poster123.jpg"),
			}

			err := db.CreateTorrent(FromAPITorrent(torrent))
			require.NoError(t, err)

			retrieved, err := db.GetTorrent(torrent.Hash)
			require.NoError(t, err)
			assert.Equal(t, torrent.Name, retrieved.Name)
			assert.Equal(t, "poster123.jpg", *retrieved.Poster)

			// Try to create the same torrent again.
			err = db.CreateTorrent(FromAPITorrent(torrent))
			assert.ErrorIs(t, err, ErrTorrentExists)
		})

		t.Run("GetTorrents and Sort", func(t *testing.T) {
			now := time.Now()
			torrent2 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("1111111111111111111111111111111111111111"),
				Name:      "Tears of Steel",
				CreatedAt: utils.Ptr(now.Add(-1 * time.Hour)),
				UpdatedAt: utils.Ptr(now.Add(1 * time.Hour)),
			}
			err := db.CreateTorrent(FromAPITorrent(torrent2))
			require.NoError(t, err)

			torrent3 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("2222222222222222222222222222222222222222"),
				Name:      "Cosmos",
				CreatedAt: utils.Ptr(now.Add(2 * time.Hour)),
			}
			err = db.CreateTorrent(FromAPITorrent(torrent3))
			require.NoError(t, err)

			torrent4 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("3333333333333333333333333333333333333333"),
				Name:      "Equal Time",
				CreatedAt: utils.Ptr(now.Add(2 * time.Hour)),
			}
			err = db.CreateTorrent(FromAPITorrent(torrent4))
			require.NoError(t, err)

			torrent5 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("4444444444444444444444444444444444444444"),
				Name:      "Past Movie",
				CreatedAt: utils.Ptr(now.Add(-5 * time.Hour)),
			}
			err = db.CreateTorrent(FromAPITorrent(torrent5))
			require.NoError(t, err)

			torrents, err := db.GetTorrents()
			require.NoError(t, err)
			require.Len(t, torrents, 5)
		})

		t.Run("IsPosterUsed", func(t *testing.T) {
			used, err := db.IsPosterUsed("poster123.jpg")
			require.NoError(t, err)
			assert.True(t, used)

			used, err = db.IsPosterUsed("nonexistent.jpg")
			require.NoError(t, err)
			assert.False(t, used)
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
			_, err := db.GetSettings()
			assert.ErrorIs(t, err, ErrSettingsNotFound)

			newPort := 9091
			apiSettings := &api.Settings{
				HTTPServerPort: &newPort,
			}

			err = db.UpdateSettings(FromAPISettings(apiSettings))
			require.NoError(t, err)

			updatedDbSettings, err := db.GetSettings()
			require.NoError(t, err)

			updatedApiSettings := ToAPISettings(updatedDbSettings)
			assert.Equal(t, newPort, *updatedApiSettings.HTTPServerPort)
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

	t.Run("JWT Secret", func(t *testing.T) {
		t.Run("Get Secret", func(t *testing.T) {
			secret, err := db.GetJWTSecret()
			require.NoError(t, err)
			assert.NotEmpty(t, secret)

			// Getting it again should return the same one.
			secret2, err := db.GetJWTSecret()
			require.NoError(t, err)
			assert.Equal(t, secret, secret2)
		})
	})
}

func TestBBoltDB_EmptyDB_DirectCalls(t *testing.T) {
	t.Run("GetJWTSecret on empty DB", func(t *testing.T) {
		dbPath := tempfile(t)
		db, err := NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		secret, err := db.GetJWTSecret()
		require.NoError(t, err)
		assert.NotEmpty(t, secret)
	})

	t.Run("GetDLNAUDN on empty DB", func(t *testing.T) {
		dbPath := tempfile(t)
		db, err := NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		udn, err := db.GetDLNAUDN()
		require.NoError(t, err)
		assert.NotEmpty(t, udn)
	})

	t.Run("UpdateSettings on empty DB", func(t *testing.T) {
		dbPath := tempfile(t)
		db, err := NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		assert.Error(t, db.UpdateSettings(nil))

		newPort := 8081
		err = db.UpdateSettings(&Settings{Settings: api.Settings{HTTPServerPort: &newPort}})
		require.NoError(t, err)

		s, err := db.GetSettings()
		require.NoError(t, err)
		assert.Equal(t, newPort, *s.HTTPServerPort)
	})

	t.Run("GetJWTSecret and GetDLNAUDN on empty DB followed by UpdateSettings preserves both", func(t *testing.T) {
		dbPath := tempfile(t)
		db, err := NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		secret, err := db.GetJWTSecret()
		require.NoError(t, err)
		assert.NotEmpty(t, secret)

		udn, err := db.GetDLNAUDN()
		require.NoError(t, err)
		assert.NotEmpty(t, udn)

		newPort := 8084
		err = db.UpdateSettings(&Settings{Settings: api.Settings{HTTPServerPort: &newPort}})
		require.NoError(t, err)

		s, err := db.GetSettings()
		require.NoError(t, err)
		assert.Equal(t, newPort, *s.HTTPServerPort)

		secretAfter, err := db.GetJWTSecret()
		require.NoError(t, err)
		assert.Equal(t, secret, secretAfter)

		udnAfter, err := db.GetDLNAUDN()
		require.NoError(t, err)
		assert.Equal(t, udn, udnAfter)
	})

	t.Run("UpdateSettings on empty DB followed by GetJWTSecret and GetDLNAUDN preserves settings", func(t *testing.T) {
		dbPath := tempfile(t)
		db, err := NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		newPort := 8085
		err = db.UpdateSettings(&Settings{Settings: api.Settings{HTTPServerPort: &newPort}})
		require.NoError(t, err)

		secret, err := db.GetJWTSecret()
		require.NoError(t, err)
		assert.NotEmpty(t, secret)

		udn, err := db.GetDLNAUDN()
		require.NoError(t, err)
		assert.NotEmpty(t, udn)

		s, err := db.GetSettings()
		require.NoError(t, err)
		assert.Equal(t, newPort, *s.HTTPServerPort)
	})
}

func TestBBoltDB_CorruptedData(t *testing.T) {
	dbPath := tempfile(t)
	db, err := NewBBoltDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	hash := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")

	// Insert corrupted JSON into torrentsBucket
	err = db.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(torrentsBucket)).Put(hash.Bytes(), []byte("{invalid-json"))
	})
	require.NoError(t, err)

	_, err = db.GetTorrent(hash)
	assert.Error(t, err)

	_, err = db.GetTorrents()
	assert.Error(t, err)

	// Insert corrupted JSON into settingsBucket
	err = db.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(settingsBucket)).Put([]byte("settings"), []byte("{invalid-json"))
	})
	require.NoError(t, err)

	_, err = db.GetSettings()
	assert.Error(t, err)

	_, err = db.GetDLNAUDN()
	assert.Error(t, err)

	_, err = db.GetJWTSecret()
	assert.Error(t, err)
}

func TestBBoltDB_MissingBuckets(t *testing.T) {
	dbPath := tempfile(t)
	db, err := NewBBoltDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = db.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.DeleteBucket([]byte(torrentsBucket)); err != nil {
			return err
		}
		return tx.DeleteBucket([]byte(settingsBucket))
	})
	require.NoError(t, err)

	hash := metainfo.NewHashFromHex("08ada5a7a6183aae1e09d831df6748d566095a10")
	torrent := &Torrent{Torrent: api.Torrent{Hash: hash}}

	assert.ErrorIs(t, db.CreateTorrent(torrent), errBucketNotFound)
	_, err = db.GetTorrents()
	assert.ErrorIs(t, err, errBucketNotFound)
	_, err = db.GetTorrent(hash)
	assert.ErrorIs(t, err, errBucketNotFound)
	_, err = db.IsPosterUsed("test")
	assert.ErrorIs(t, err, errBucketNotFound)
	assert.ErrorIs(t, db.UpdateTorrent(torrent), errBucketNotFound)
	assert.ErrorIs(t, db.DeleteTorrent(hash), errBucketNotFound)
	_, err = db.GetSettings()
	assert.ErrorIs(t, err, errBucketNotFound)
	assert.ErrorIs(t, db.UpdateSettings(&Settings{}), errBucketNotFound)
}

func TestNewBBoltDB_Error(t *testing.T) {
	_, err := NewBBoltDB("/non/existent/path/db.bolt")
	assert.Error(t, err)
}

func TestTypesConversions(t *testing.T) {
	assert.Nil(t, FromAPITorrent(nil))
	assert.Nil(t, ToAPITorrent(nil))
	assert.Nil(t, FromAPISettings(nil))
	assert.Nil(t, ToAPISettings(nil))

	t1 := &api.Torrent{Name: "T1"}
	t2 := &api.Torrent{Name: "T2"}
	dbTorrents := FromAPITorrents([]*api.Torrent{t1, t2})
	require.Len(t, dbTorrents, 2)
	assert.Equal(t, "T1", dbTorrents[0].Name)

	apiTorrents := ToAPITorrents(dbTorrents)
	require.Len(t, apiTorrents, 2)
	assert.Equal(t, "T2", apiTorrents[1].Name)

	port := 8080
	s := &api.Settings{HTTPServerPort: &port}
	dbSettings := FromAPISettings(s)
	require.NotNil(t, dbSettings)
	assert.Equal(t, port, *dbSettings.HTTPServerPort)

	convertedBack := ToAPISettings(dbSettings)
	require.NotNil(t, convertedBack)
	assert.Equal(t, port, *convertedBack.HTTPServerPort)
}
