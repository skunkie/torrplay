// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/auth"
	"github.com/torrplay/torrplay/internal/settings"
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
		_ = db.Close()
		return nil, err
	}

	return &BBoltDB{db: db}, nil
}

func (b *BBoltDB) Close() error {
	return b.db.Close()
}

func (b *BBoltDB) CreateTorrent(t *Torrent) error {
	if t == nil {
		return errors.New("torrent cannot be nil")
	}

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
	ts, _, err := b.GetTorrentsAtRevision()
	return ts, err
}

// GetTorrentsAtRevision returns the torrents with the revision of the
// database they were read at. The revision grows with every committed write,
// so equal revisions read equal torrents.
func (b *BBoltDB) GetTorrentsAtRevision() ([]*Torrent, int, error) {
	var (
		ts       []*Torrent
		revision int
	)

	err := b.db.View(func(tx *bbolt.Tx) error {
		revision = tx.ID()
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

	return ts, revision, err
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
	var used bool
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
			if t.Poster != nil && *t.Poster == posterID {
				used = true
			}
			return nil
		})
	})

	if err != nil {
		return false, err
	}

	return used, nil
}

func (b *BBoltDB) UpdateTorrent(t *Torrent) error {
	if t == nil {
		return errors.New("torrent cannot be nil")
	}

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

// UpdateSettings replaces the api.Settings part of the stored settings with
// that of s, keeping database-specific fields like DLNAUDN and JWTSecret. The
// record holds a copy, decoded from its encoding as storage decodes it, so
// merging the default settings into the record leaves s and the nested values
// it shares with its caller unchanged.
func (b *BBoltDB) UpdateSettings(s *Settings) error {
	if s == nil {
		return errors.New("settings cannot be nil")
	}
	encoded, err := json.Marshal(&s.Settings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	var replacement api.Settings
	if err := json.Unmarshal(encoded, &replacement); err != nil {
		return fmt.Errorf("failed to copy settings: %w", err)
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return b.writeSettings(tx, func(stored *Settings) error {
			stored.Settings = replacement
			return nil
		})
	})
}

// GetDLNAUDN returns the DLNA UDN, creating it on first use.
func (b *BBoltDB) GetDLNAUDN() (string, error) {
	return b.getOrCreateSetting(
		func(s *Settings) string { return s.DLNAUDN },
		func(s *Settings) (string, error) {
			udn, err := uuid.NewRandom()
			if err != nil {
				return "", err
			}
			s.DLNAUDN = "uuid:" + udn.String()
			return s.DLNAUDN, nil
		},
	)
}

// GetJWTSecret returns the JWT secret, creating it on first use. It runs for
// every authenticated request, which getOrCreateSetting answers with a
// read-only transaction once the secret exists.
func (b *BBoltDB) GetJWTSecret() (string, error) {
	return b.getOrCreateSetting(
		func(s *Settings) string { return s.JWTSecret },
		func(s *Settings) (string, error) {
			secret, err := auth.GenerateJWTSecret()
			if err != nil {
				return "", err
			}
			s.JWTSecret = secret
			return secret, nil
		},
	)
}

// errSettingExists stops the write transaction of getOrCreateSetting when
// another caller has created the setting since its read.
var errSettingExists = errors.New("setting exists")

// getOrCreateSetting returns the setting that read reads from the stored
// settings, storing the one create sets on first use. An existing setting is
// read in a read-only transaction, so reading it commits no write. The write
// transaction reads again, since another caller may have created the setting
// since the read, and then commits nothing.
func (b *BBoltDB) getOrCreateSetting(read func(*Settings) string, create func(*Settings) (string, error)) (string, error) {
	var value string
	err := b.db.View(func(tx *bbolt.Tx) error {
		s, err := b.getSettings(tx)
		if err != nil && !errors.Is(err, ErrSettingsNotFound) {
			return err
		}
		if s != nil {
			value = read(s)
		}
		return nil
	})
	if err != nil || value != "" {
		return value, err
	}

	err = b.db.Update(func(tx *bbolt.Tx) error {
		return b.writeSettings(tx, func(s *Settings) error {
			if value = read(s); value != "" {
				return errSettingExists
			}
			created, err := create(s)
			if err != nil {
				return err
			}
			value = created
			return nil
		})
	})
	switch {
	case errors.Is(err, errSettingExists):
		return value, nil
	case err != nil:
		return "", err
	}
	return value, nil
}

// RotateJWTSecret replaces the JWT secret, revoking every token signed with
// the previous one.
func (b *BBoltDB) RotateJWTSecret() error {
	secret, err := auth.GenerateJWTSecret()
	if err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return b.writeSettings(tx, func(s *Settings) error {
			s.JWTSecret = secret
			return nil
		})
	})
}

// writeSettings stores the settings record after change modifies it, reading
// the stored record or starting an empty one, and merges the default settings
// into it, so a record written before the settings are bootstrapped loses no
// default. It must be called within a write transaction.
func (b *BBoltDB) writeSettings(tx *bbolt.Tx, change func(*Settings) error) error {
	bucket := tx.Bucket([]byte(settingsBucket))
	if bucket == nil {
		return errBucketNotFound
	}
	s, err := b.getSettings(tx)
	if err != nil && !errors.Is(err, ErrSettingsNotFound) {
		return err
	}
	if s == nil {
		s = &Settings{}
	}
	if err := change(s); err != nil {
		return err
	}
	settings.Merge(&s.Settings, settings.Default())

	encoded, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	return bucket.Put([]byte("settings"), encoded)
}
