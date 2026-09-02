// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/auth"
	"github.com/torrplay/torrplay/internal/utils"
	"go.etcd.io/bbolt"
)

const (
	torrentsBucket = "torrents"
	settingsBucket = "settings"
)

var _ DatabaseInterface = (*BBoltDB)(nil)

var (
	ErrTorrentExists    = errors.New("torrent already exists")
	ErrTorrentNotFound  = errors.New("torrent not found")
	ErrSettingsNotFound = errors.New("settings not found")
	errBucketNotFound   = errors.New("bucket not found")
)

type BBoltDB struct {
	db *bbolt.DB
}

func NewBBoltDB(path string) (*BBoltDB, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(torrentsBucket)); err != nil {
			return fmt.Errorf("failed to create torrents bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(settingsBucket)); err != nil {
			return fmt.Errorf("failed to create settings bucket: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &BBoltDB{db: db}, nil
}

func (b *BBoltDB) Close() error {
	return b.db.Close()
}

func (b *BBoltDB) CreateTorrent(t *Torrent) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		if v := bucket.Get(t.Hash.Bytes()); v != nil {
			return ErrTorrentExists
		}

		if t.CreatedAt == nil {
			t.CreatedAt = new(time.Now())
		}

		encoded, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("failed to marshal torrent: %w", err)
		}

		return bucket.Put(t.Hash.Bytes(), encoded)
	})
}

func (b *BBoltDB) GetTorrents() ([]*Torrent, error) {
	var ts []*Torrent

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		return bucket.ForEach(func(_, v []byte) error {
			var t Torrent
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("failed to unmarshal torrent: %w", err)
			}
			ts = append(ts, &t)
			return nil
		})
	})

	slices.SortFunc(ts, func(a, b *Torrent) int {
		timeA := utils.Val(a.CreatedAt)
		if a.UpdatedAt != nil {
			timeA = *a.UpdatedAt
		}
		timeB := utils.Val(b.CreatedAt)
		if b.UpdatedAt != nil {
			timeB = *b.UpdatedAt
		}

		if timeA.Before(timeB) {
			return 1
		} else if timeA.After(timeB) {
			return -1
		}

		return 0
	})

	return ts, err
}

func (b *BBoltDB) GetTorrent(ih metainfo.Hash) (*Torrent, error) {
	var t Torrent

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		v := bucket.Get(ih.Bytes())
		if v == nil {
			return ErrTorrentNotFound
		}

		if err := json.Unmarshal(v, &t); err != nil {
			return fmt.Errorf("failed to unmarshal torrent: %w", err)
		}
		return nil
	})

	return &t, err
}

func (b *BBoltDB) IsPosterUsed(posterID string) (bool, error) {
	var count int
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		return bucket.ForEach(func(k, v []byte) error {
			if strings.Contains(string(v), posterID) {
				count++
			}
			return nil
		})
	})

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (b *BBoltDB) UpdateTorrent(t *Torrent) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		encoded, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("failed to marshal torrent: %w", err)
		}

		return bucket.Put(t.Hash.Bytes(), encoded)
	})
}

func (b *BBoltDB) DeleteTorrent(ih metainfo.Hash) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}
		return bucket.Delete(ih.Bytes())
	})
}

func (b *BBoltDB) getSettings(tx *bbolt.Tx) (*Settings, error) {
	var s Settings

	bucket := tx.Bucket([]byte(settingsBucket))
	if bucket == nil {
		return nil, errBucketNotFound
	}

	v := bucket.Get([]byte("settings"))
	if len(v) == 0 {
		return nil, ErrSettingsNotFound
	}

	if err := json.Unmarshal(v, &s); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}
	return &s, nil
}

func (b *BBoltDB) GetSettings() (*Settings, error) {
	var s *Settings
	err := b.db.View(func(tx *bbolt.Tx) error {
		var err error
		s, err = b.getSettings(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (b *BBoltDB) UpdateSettings(s *Settings) error {
	if s == nil {
		return errors.New("settings cannot be nil")
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return b.updateSettings(tx, &s.Settings)
	})
}

// updateSettings updates the settings in the database.
// This function must be called within a database transaction.
// It performs a partial update by first reading the existing settings
// and then overwriting the api.Settings part with the new values.
// This preserves database-specific fields like DLNAUDN and JWTSecret.
func (b *BBoltDB) updateSettings(tx *bbolt.Tx, s *api.Settings) error {
	if s == nil {
		return errors.New("settings cannot be nil")
	}
	bucket := tx.Bucket([]byte(settingsBucket))
	if bucket == nil {
		return errBucketNotFound
	}

	is, err := b.getSettings(tx)
	if err != nil && !errors.Is(err, ErrSettingsNotFound) {
		return err
	}

	if is == nil {
		is = &Settings{}
	}

	is.Settings = *s

	encoded, err := json.Marshal(is)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	return bucket.Put([]byte("settings"), encoded)
}

func (b *BBoltDB) GetDLNAUDN() (string, error) {
	var udn string
	err := b.db.Update(func(tx *bbolt.Tx) error {
		s, err := b.getSettings(tx)
		if err != nil && !errors.Is(err, ErrSettingsNotFound) {
			return err
		}

		if s == nil {
			s = &Settings{}
		}

		if s.DLNAUDN != "" {
			udn = s.DLNAUDN
			return nil
		}

		newUDN, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		udn = "uuid:" + newUDN.String()
		s.DLNAUDN = udn

		encoded, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("failed to marshal settings: %w", err)
		}
		return tx.Bucket([]byte(settingsBucket)).Put([]byte("settings"), encoded)
	})

	return udn, err
}

func (b *BBoltDB) GetJWTSecret() (string, error) {
	var secret string
	err := b.db.Update(func(tx *bbolt.Tx) error {
		s, err := b.getSettings(tx)
		if err != nil && !errors.Is(err, ErrSettingsNotFound) {
			return err
		}

		if s == nil {
			s = &Settings{}
		}

		if s.JWTSecret != "" {
			secret = s.JWTSecret
			return nil
		}

		newSecret, err := auth.GenerateJWTSecret()
		if err != nil {
			return err
		}
		secret = newSecret
		s.JWTSecret = newSecret

		encoded, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("failed to marshal settings: %w", err)
		}
		return tx.Bucket([]byte(settingsBucket)).Put([]byte("settings"), encoded)
	})

	return secret, err
}
