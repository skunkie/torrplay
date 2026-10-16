// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	appsettings "github.com/torrplay/torrplay/internal/settings"
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
				Poster: new("poster123.jpg"),
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
				CreatedAt: new(now.Add(-1 * time.Hour)),
				UpdatedAt: new(now.Add(1 * time.Hour)),
			}
			err := db.CreateTorrent(FromAPITorrent(torrent2))
			require.NoError(t, err)

			torrent3 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("2222222222222222222222222222222222222222"),
				Name:      "Cosmos",
				CreatedAt: new(now.Add(2 * time.Hour)),
			}
			err = db.CreateTorrent(FromAPITorrent(torrent3))
			require.NoError(t, err)

			torrent4 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("3333333333333333333333333333333333333333"),
				Name:      "Equal Time",
				CreatedAt: new(now.Add(2 * time.Hour)),
			}
			err = db.CreateTorrent(FromAPITorrent(torrent4))
			require.NoError(t, err)

			torrent5 := &api.Torrent{
				Hash:      metainfo.NewHashFromHex("4444444444444444444444444444444444444444"),
				Name:      "Past Movie",
				CreatedAt: new(now.Add(-5 * time.Hour)),
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

		t.Run("reads an existing secret without the write lock", func(t *testing.T) {
			secret, err := db.GetJWTSecret()
			require.NoError(t, err)

			// A write transaction in progress blocks another, but not a read.
			writer, err := db.db.Begin(true)
			require.NoError(t, err)
			defer func() { _ = writer.Rollback() }()
			read := make(chan string, 1)
			go func() {
				got, err := db.GetJWTSecret()
				assert.NoError(t, err)
				read <- got
			}()
			select {
			case got := <-read:
				assert.Equal(t, secret, got)
			case <-time.After(5 * time.Second):
				require.NoError(t, writer.Rollback())
				<-read
				t.Fatal("reading the secret waited for the write transaction")
			}
		})

		t.Run("rotate secret", func(t *testing.T) {
			secret, err := db.GetJWTSecret()
			require.NoError(t, err)
			udn, err := db.GetDLNAUDN()
			require.NoError(t, err)
			settings, err := db.GetSettings()
			require.NoError(t, err)

			require.NoError(t, db.RotateJWTSecret())

			rotated, err := db.GetJWTSecret()
			require.NoError(t, err)
			assert.NotEmpty(t, rotated)
			assert.NotEqual(t, secret, rotated)

			udnAfter, err := db.GetDLNAUDN()
			require.NoError(t, err)
			assert.Equal(t, udn, udnAfter)

			settingsAfter, err := db.GetSettings()
			require.NoError(t, err)
			assert.Equal(t, settings.Settings, settingsAfter.Settings)
		})
	})

	t.Run("empty database", func(t *testing.T) {
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
	})

	t.Run("corrupted data", func(t *testing.T) {
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

		_, err = db.IsPosterUsed("poster-id")
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

		assert.Error(t, db.RotateJWTSecret())
	})

	t.Run("missing buckets", func(t *testing.T) {
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
	})

	t.Run("rejects nil torrent", func(t *testing.T) {
		dbPath := tempfile(t)
		db, err := NewBBoltDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		assert.Error(t, db.CreateTorrent(nil))
		assert.Error(t, db.UpdateTorrent(nil))
	})
}

// TestNewBBoltDB verifies that NewBBoltDB rejects an unusable path.
func TestNewBBoltDB(t *testing.T) {
	_, err := NewBBoltDB("/non/existent/path/db.bolt")
	assert.Error(t, err)
}

// TestBBoltDB_IsPosterUsed verifies that IsPosterUsed matches poster IDs exactly.
func TestBBoltDB_IsPosterUsed(t *testing.T) {
	dbPath := tempfile(t)
	db, err := NewBBoltDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	posterID := "0123456789abcdef"
	require.NoError(t, db.CreateTorrent(&Torrent{Torrent: api.Torrent{
		Hash: metainfo.NewHashFromHex("1111111111111111111111111111111111111111"),
		Name: "title containing " + posterID,
	}}))

	used, err := db.IsPosterUsed(posterID)
	require.NoError(t, err)
	assert.False(t, used)

	require.NoError(t, db.CreateTorrent(&Torrent{Torrent: api.Torrent{
		Hash:   metainfo.NewHashFromHex("2222222222222222222222222222222222222222"),
		Poster: new(posterID),
	}}))

	used, err = db.IsPosterUsed(posterID)
	require.NoError(t, err)
	assert.True(t, used)

	used, err = db.IsPosterUsed("01234567")
	require.NoError(t, err)
	assert.False(t, used)
}

func TestBBoltDB_GetTorrentsAtRevision(t *testing.T) {
	db, err := NewBBoltDB(tempfile(t))
	require.NoError(t, err)
	defer db.Close()

	sintel := &Torrent{Torrent: api.Torrent{Hash: metainfo.NewHashFromHex("1111111111111111111111111111111111111111"), Name: "Sintel"}}
	require.NoError(t, db.CreateTorrent(sintel))
	read := func(t *testing.T) ([]*Torrent, int) {
		t.Helper()
		ts, revision, err := db.GetTorrentsAtRevision()
		require.NoError(t, err)
		return ts, revision
	}
	ts, created := read(t)
	require.Len(t, ts, 1)

	t.Run("stays the same without writes", func(t *testing.T) {
		_, revision := read(t)
		assert.Equal(t, created, revision)
	})

	t.Run("grows with an update", func(t *testing.T) {
		sintel.Title = new("Sintel 2010")
		require.NoError(t, db.UpdateTorrent(sintel))
		ts, revision := read(t)
		assert.Greater(t, revision, created)
		assert.Equal(t, "Sintel 2010", *ts[0].Title)
	})

	t.Run("grows with a deletion", func(t *testing.T) {
		_, before := read(t)
		require.NoError(t, db.DeleteTorrent(sintel.Hash))
		ts, revision := read(t)
		assert.Greater(t, revision, before)
		assert.Empty(t, ts)
	})
}

func TestBBoltDB_GetOrCreateSetting(t *testing.T) {
	getters := []struct {
		name string
		get  func(*BBoltDB) (string, error)
	}{
		{name: "dlna udn", get: (*BBoltDB).GetDLNAUDN},
		{name: "jwt secret", get: (*BBoltDB).GetJWTSecret},
	}
	open := func(t *testing.T) *BBoltDB {
		t.Helper()
		db, err := NewBBoltDB(tempfile(t))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	// lastWrite returns the ID of the newest committed write transaction.
	lastWrite := func(t *testing.T, db *BBoltDB) int {
		t.Helper()
		var id int
		require.NoError(t, db.db.View(func(tx *bbolt.Tx) error {
			id = tx.ID()
			return nil
		}))
		return id
	}

	for _, getter := range getters {
		t.Run(getter.name, func(t *testing.T) {
			t.Run("returns the created value without another write", func(t *testing.T) {
				db := open(t)
				created, err := getter.get(db)
				require.NoError(t, err)
				require.NotEmpty(t, created)
				written := lastWrite(t, db)

				again, err := getter.get(db)
				require.NoError(t, err)
				assert.Equal(t, created, again)
				assert.Equal(t, written, lastWrite(t, db), "reading an existing value committed a write")
			})

			t.Run("returns one value to concurrent callers", func(t *testing.T) {
				db := open(t)
				const callers = 8
				values := make([]string, callers)
				errs := make([]error, callers)
				var wg sync.WaitGroup
				for i := range callers {
					wg.Go(func() {
						values[i], errs[i] = getter.get(db)
					})
				}
				wg.Wait()

				for i := range callers {
					require.NoError(t, errs[i])
				}
				assert.Len(t, slices.Compact(values), 1)
				stored, err := getter.get(db)
				require.NoError(t, err)
				assert.Equal(t, values[0], stored)
			})

			t.Run("stores the default settings with a created value", func(t *testing.T) {
				db := open(t)
				_, err := getter.get(db)
				require.NoError(t, err)

				stored, err := db.GetSettings()
				require.NoError(t, err)
				assert.False(t, appsettings.Merge(&stored.Settings, appsettings.Default()), "a default setting is missing")
			})

			t.Run("keeps existing settings", func(t *testing.T) {
				db := open(t)
				require.NoError(t, db.UpdateSettings(&Settings{Settings: api.Settings{FriendlyName: new("Llamaland")}}))

				_, err := getter.get(db)
				require.NoError(t, err)

				stored, err := db.GetSettings()
				require.NoError(t, err)
				assert.Equal(t, "Llamaland", *stored.FriendlyName)
			})
		})
	}
}

func TestBBoltDB_RotateJWTSecret(t *testing.T) {
	t.Run("replaces the secret and keeps the other settings", func(t *testing.T) {
		db, err := NewBBoltDB(tempfile(t))
		require.NoError(t, err)
		defer db.Close()
		require.NoError(t, db.UpdateSettings(&Settings{Settings: api.Settings{FriendlyName: new("Llamaland")}}))
		udn, err := db.GetDLNAUDN()
		require.NoError(t, err)
		old, err := db.GetJWTSecret()
		require.NoError(t, err)

		require.NoError(t, db.RotateJWTSecret())

		secret, err := db.GetJWTSecret()
		require.NoError(t, err)
		assert.NotEqual(t, old, secret)
		stored, err := db.GetSettings()
		require.NoError(t, err)
		assert.Equal(t, secret, stored.JWTSecret)
		assert.Equal(t, udn, stored.DLNAUDN)
		assert.Equal(t, "Llamaland", *stored.FriendlyName)
	})

	t.Run("stores the default settings without a record", func(t *testing.T) {
		db, err := NewBBoltDB(tempfile(t))
		require.NoError(t, err)
		defer db.Close()

		require.NoError(t, db.RotateJWTSecret())

		stored, err := db.GetSettings()
		require.NoError(t, err)
		assert.NotEmpty(t, stored.JWTSecret)
		assert.False(t, appsettings.Merge(&stored.Settings, appsettings.Default()), "a default setting is missing")
	})
}

func TestBBoltDB_UpdateSettings(t *testing.T) {
	open := func(t *testing.T) *BBoltDB {
		t.Helper()
		db, err := NewBBoltDB(tempfile(t))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	t.Run("keeps the dlna udn and the jwt secret", func(t *testing.T) {
		db := open(t)
		udn, err := db.GetDLNAUDN()
		require.NoError(t, err)
		secret, err := db.GetJWTSecret()
		require.NoError(t, err)

		require.NoError(t, db.UpdateSettings(&Settings{Settings: api.Settings{FriendlyName: new("Llamaland")}}))

		stored, err := db.GetSettings()
		require.NoError(t, err)
		assert.Equal(t, "Llamaland", *stored.FriendlyName)
		assert.Equal(t, udn, stored.DLNAUDN)
		assert.Equal(t, secret, stored.JWTSecret)
	})

	t.Run("merges the default settings and leaves the caller's unchanged", func(t *testing.T) {
		db := open(t)
		in := &Settings{Settings: api.Settings{
			Auth:          &api.Auth{},
			TorrentClient: &api.TorrentClient{DisableDHT: new(true)},
		}}

		require.NoError(t, db.UpdateSettings(in))

		stored, err := db.GetSettings()
		require.NoError(t, err)
		assert.True(t, *stored.TorrentClient.DisableDHT)
		assert.False(t, appsettings.Merge(&stored.Settings, appsettings.Default()), "a default setting is missing")
		assert.Nil(t, in.Auth.Enabled, "the caller's auth settings changed")
		assert.Nil(t, in.TorrentClient.DisablePEX, "the caller's torrent client settings changed")
		assert.Nil(t, in.FriendlyName, "the caller's settings changed")
	})

	t.Run("rejects nil settings", func(t *testing.T) {
		assert.Error(t, open(t).UpdateSettings(nil))
	})

	t.Run("reports a missing settings bucket", func(t *testing.T) {
		db := open(t)
		require.NoError(t, db.db.Update(func(tx *bbolt.Tx) error {
			return tx.DeleteBucket([]byte(settingsBucket))
		}))

		assert.ErrorIs(t, db.UpdateSettings(&Settings{}), errBucketNotFound)
	})
}

func TestBBoltDB_WriteSettings(t *testing.T) {
	t.Run("reports a missing settings bucket", func(t *testing.T) {
		db, err := NewBBoltDB(tempfile(t))
		require.NoError(t, err)
		defer db.Close()
		require.NoError(t, db.db.Update(func(tx *bbolt.Tx) error {
			return tx.DeleteBucket([]byte(settingsBucket))
		}))

		assert.ErrorIs(t, db.RotateJWTSecret(), errBucketNotFound)
		_, err = db.GetDLNAUDN()
		assert.ErrorIs(t, err, errBucketNotFound)
	})

	t.Run("stores nothing when the change fails", func(t *testing.T) {
		db, err := NewBBoltDB(tempfile(t))
		require.NoError(t, err)
		defer db.Close()
		failed := errors.New("change failed")

		err = db.db.Update(func(tx *bbolt.Tx) error {
			return db.writeSettings(tx, func(s *Settings) error {
				s.DLNAUDN = "uuid:discarded"
				return failed
			})
		})

		assert.ErrorIs(t, err, failed)
		_, err = db.GetSettings()
		assert.ErrorIs(t, err, ErrSettingsNotFound)
	})
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
