// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	errBucketNotFound   = errors.New("bucket not found")
	errSettingsNotFound = errors.New("settings not found")

	defaultSettings = internalSettings{
		Settings: api.Settings{
			Auth:                &api.Auth{Enabled: utils.Ptr(false)},
			DisableIpv6:         utils.Ptr(false),
			EnableDlna:          utils.Ptr(false),
			EnableDownloader:    utils.Ptr(false),
			FileStoragePath:     utils.Ptr(""),
			FriendlyName:        utils.Ptr("TorrPlay"),
			HTTPServerPort:      utils.Ptr(8090),
			LogFormat:           utils.Ptr(api.Text),
			LogLevel:            utils.Ptr(slog.LevelInfo),
			LogStoreSize:        utils.Ptr(100),
			MaxMemory:           utils.Ptr(int64(64 * 1024 * 1024)),
			ReadaheadPercentage: utils.Ptr(90),
		},
	}
)

type BBoltDB struct {
	db *bbolt.DB
}

type internalSettings struct {
	api.Settings
	DLNAUDN   string `json:"dlna_udn,omitempty"`
	JWTSecret string `json:"jwt_secret,omitempty"`
}

func NewBBoltDB(path string) (*BBoltDB, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
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

func (b *BBoltDB) CreateTorrent(t *api.Torrent) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		if v := bucket.Get(t.Hash.Bytes()); v != nil {
			return ErrTorrentExists
		}

		if t.CreatedAt == nil {
			t.CreatedAt = utils.Ptr(time.Now())
		}

		encoded, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("failed to marshal torrent: %w", err)
		}

		return bucket.Put(t.Hash.Bytes(), encoded)
	})
}

func (b *BBoltDB) GetTorrents() ([]*api.Torrent, error) {
	var ts []*api.Torrent

	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(torrentsBucket))
		if bucket == nil {
			return errBucketNotFound
		}

		return bucket.ForEach(func(_, v []byte) error {
			var t api.Torrent
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("failed to unmarshal torrent: %w", err)
			}
			ts = append(ts, &t)
			return nil
		})
	})

	slices.SortFunc(ts, func(a, b *api.Torrent) int {
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

func (b *BBoltDB) GetTorrent(ih metainfo.Hash) (*api.Torrent, error) {
	var t api.Torrent

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

func (b *BBoltDB) UpdateTorrent(t *api.Torrent) error {
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

func (b *BBoltDB) getInternalSettings(tx *bbolt.Tx) (*internalSettings, error) {
	var s internalSettings

	bucket := tx.Bucket([]byte(settingsBucket))
	if bucket == nil {
		return nil, errBucketNotFound
	}

	v := bucket.Get([]byte("settings"))
	if v == nil {
		return nil, errSettingsNotFound
	}

	if err := json.Unmarshal(v, &s); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}
	return &s, nil
}

func (b *BBoltDB) GetSettings() (*api.Settings, error) {
	var s *internalSettings
	var needsUpdate bool

	err := b.db.Update(func(tx *bbolt.Tx) error {
		var err error
		s, err = b.getInternalSettings(tx)
		if err != nil {
			if errors.Is(err, errSettingsNotFound) {
				s = &defaultSettings
				needsUpdate = true
				return nil
			}
			return err
		}

		// Merge with default settings to ensure no nil pointers on missing values.
		if s.Settings.Auth == nil {
			needsUpdate = true
			s.Settings.Auth = defaultSettings.Auth
		}
		if s.Settings.DisableIpv6 == nil {
			needsUpdate = true
			s.Settings.DisableIpv6 = defaultSettings.Settings.DisableIpv6
		}
		if s.Settings.EnableDlna == nil {
			needsUpdate = true
			s.Settings.EnableDlna = defaultSettings.Settings.EnableDlna
		}
		if s.Settings.EnableDownloader == nil {
			needsUpdate = true
			s.Settings.EnableDownloader = defaultSettings.Settings.EnableDownloader
		}
		if s.Settings.FileStoragePath == nil {
			needsUpdate = true
			s.Settings.FileStoragePath = defaultSettings.Settings.FileStoragePath
		}
		if s.Settings.FriendlyName == nil {
			needsUpdate = true
			s.Settings.FriendlyName = defaultSettings.Settings.FriendlyName
		}
		if s.Settings.HTTPServerPort == nil {
			needsUpdate = true
			s.Settings.HTTPServerPort = defaultSettings.Settings.HTTPServerPort
		}
		if s.Settings.LogFormat == nil {
			needsUpdate = true
			s.Settings.LogFormat = defaultSettings.Settings.LogFormat
		}
		if s.Settings.LogLevel == nil {
			needsUpdate = true
			s.Settings.LogLevel = defaultSettings.Settings.LogLevel
		}
		if s.Settings.LogStoreSize == nil {
			needsUpdate = true
			s.Settings.LogStoreSize = defaultSettings.Settings.LogStoreSize
		}
		if s.Settings.MaxMemory == nil {
			needsUpdate = true
			s.Settings.MaxMemory = defaultSettings.Settings.MaxMemory
		}
		if s.Settings.ReadaheadPercentage == nil {
			needsUpdate = true
			s.Settings.ReadaheadPercentage = defaultSettings.Settings.ReadaheadPercentage
		}

		if needsUpdate {
			return b.updateSettings(tx, &s.Settings)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &s.Settings, nil
}

func (b *BBoltDB) UpdateSettings(s *api.Settings) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return b.updateSettings(tx, s)
	})
}

func (b *BBoltDB) updateSettings(tx *bbolt.Tx, s *api.Settings) error {
	bucket := tx.Bucket([]byte(settingsBucket))
	if bucket == nil {
		return errBucketNotFound
	}

	is, err := b.getInternalSettings(tx)
	if err != nil && !errors.Is(err, errSettingsNotFound) {
		return err
	}

	if is == nil {
		is = &internalSettings{}
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
		is, err := b.getInternalSettings(tx)
		if err != nil && !errors.Is(err, errSettingsNotFound) {
			return err
		}

		if is == nil {
			is = &defaultSettings
		}

		if is.DLNAUDN != "" {
			udn = is.DLNAUDN
			return nil
		}

		newUDN, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		udn = "uuid:" + newUDN.String()
		is.DLNAUDN = udn

		encoded, err := json.Marshal(is)
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
		is, err := b.getInternalSettings(tx)
		if err != nil && !errors.Is(err, errSettingsNotFound) {
			return err
		}

		if is == nil {
			is = &defaultSettings
		}

		if is.JWTSecret != "" {
			secret = is.JWTSecret
			return nil
		}

		newSecret, err := auth.GenerateJWTSecret()
		if err != nil {
			return err
		}
		secret = newSecret
		is.JWTSecret = newSecret

		encoded, err := json.Marshal(is)
		if err != nil {
			return fmt.Errorf("failed to marshal settings: %w", err)
		}
		return tx.Bucket([]byte(settingsBucket)).Put([]byte("settings"), encoded)
	})

	return secret, err
}
