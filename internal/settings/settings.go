// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package settings

import (
	"log/slog"

	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

// Default returns a new api.Settings populated with default application values.
func Default() api.Settings {
	return api.Settings{
		Auth:               &api.Auth{Enabled: new(false)},
		CorsAllowedOrigins: new([]string{}),
		EnableDlna:         new(false),
		EnableDownloader:   new(false),
		EnableStremio:      new(false),
		FileStoragePath:    new(""),
		FriendlyName:       new("TorrPlay"),
		HTTPServerPort:     new(8090),
		LogFormat:          utils.Ptr(api.Text),
		LogLevel:           utils.Ptr(slog.LevelInfo),
		LogStoreSize:       new(100),
		MaxMemory:          new(int64(64 * 1024 * 1024)),
		TorrentClient: &api.TorrentClient{
			DisableDHT:                     new(false),
			DisableIPv6:                    new(true),
			DisablePEX:                     new(false),
			DisableTCP:                     new(false),
			DisableUTP:                     new(false),
			DownloadRateLimit:              new(0),
			EstablishedConnsPerTorrent:     new(50),
			HalfOpenConnsPerTorrent:        new(25),
			MaxAllocPeerRequestDataPerConn: new(1048576),
			PreferHeaderObfuscation:        new(false),
			Seed:                           new(false),
			TorrentPeersHighWater:          new(500),
			TorrentPeersLowWater:           new(50),
			TotalHalfOpenConns:             new(100),
			UploadRateLimit:                new(0),
		},
		TorrentTrackers: new([]string{}),
	}
}

// Merge merges missing (nil) fields from defaults into target.
// It returns true if any field was updated from defaults.
func Merge(target *api.Settings, defaults api.Settings) bool {
	if target == nil {
		return false
	}

	var changed bool

	if target.Auth == nil {
		target.Auth = defaults.Auth
		changed = true
	} else if defaults.Auth != nil {
		if target.Auth.Enabled == nil {
			target.Auth.Enabled = defaults.Auth.Enabled
			changed = true
		}
	}
	if target.CorsAllowedOrigins == nil {
		target.CorsAllowedOrigins = defaults.CorsAllowedOrigins
		changed = true
	}
	if target.EnableDlna == nil {
		target.EnableDlna = defaults.EnableDlna
		changed = true
	}
	if target.EnableDownloader == nil {
		target.EnableDownloader = defaults.EnableDownloader
		changed = true
	}
	if target.EnableStremio == nil {
		target.EnableStremio = defaults.EnableStremio
		changed = true
	}
	if target.FileStoragePath == nil {
		target.FileStoragePath = defaults.FileStoragePath
		changed = true
	}
	if target.FriendlyName == nil {
		target.FriendlyName = defaults.FriendlyName
		changed = true
	}
	if target.HTTPServerPort == nil {
		target.HTTPServerPort = defaults.HTTPServerPort
		changed = true
	}
	if target.LogFormat == nil {
		target.LogFormat = defaults.LogFormat
		changed = true
	}
	if target.LogLevel == nil {
		target.LogLevel = defaults.LogLevel
		changed = true
	}
	if target.LogStoreSize == nil {
		target.LogStoreSize = defaults.LogStoreSize
		changed = true
	}
	if target.MaxMemory == nil {
		target.MaxMemory = defaults.MaxMemory
		changed = true
	}
	if target.TorrentTrackers == nil {
		target.TorrentTrackers = defaults.TorrentTrackers
		changed = true
	}

	if target.TorrentClient == nil {
		target.TorrentClient = defaults.TorrentClient
		changed = true
	} else if defaults.TorrentClient != nil {
		tc := target.TorrentClient
		dtc := defaults.TorrentClient

		if tc.DisableDHT == nil {
			tc.DisableDHT = dtc.DisableDHT
			changed = true
		}
		if tc.DisableIPv6 == nil {
			tc.DisableIPv6 = dtc.DisableIPv6
			changed = true
		}
		if tc.DisablePEX == nil {
			tc.DisablePEX = dtc.DisablePEX
			changed = true
		}
		if tc.DisableTCP == nil {
			tc.DisableTCP = dtc.DisableTCP
			changed = true
		}
		if tc.DisableUTP == nil {
			tc.DisableUTP = dtc.DisableUTP
			changed = true
		}
		if tc.DownloadRateLimit == nil {
			tc.DownloadRateLimit = dtc.DownloadRateLimit
			changed = true
		}
		if tc.EstablishedConnsPerTorrent == nil {
			tc.EstablishedConnsPerTorrent = dtc.EstablishedConnsPerTorrent
			changed = true
		}
		if tc.HalfOpenConnsPerTorrent == nil {
			tc.HalfOpenConnsPerTorrent = dtc.HalfOpenConnsPerTorrent
			changed = true
		}
		if tc.MaxAllocPeerRequestDataPerConn == nil {
			tc.MaxAllocPeerRequestDataPerConn = dtc.MaxAllocPeerRequestDataPerConn
			changed = true
		}
		if tc.PreferHeaderObfuscation == nil {
			tc.PreferHeaderObfuscation = dtc.PreferHeaderObfuscation
			changed = true
		}
		if tc.Seed == nil {
			tc.Seed = dtc.Seed
			changed = true
		}
		if tc.TorrentPeersHighWater == nil {
			tc.TorrentPeersHighWater = dtc.TorrentPeersHighWater
			changed = true
		}
		if tc.TorrentPeersLowWater == nil {
			tc.TorrentPeersLowWater = dtc.TorrentPeersLowWater
			changed = true
		}
		if tc.TotalHalfOpenConns == nil {
			tc.TotalHalfOpenConns = dtc.TotalHalfOpenConns
			changed = true
		}
		if tc.UploadRateLimit == nil {
			tc.UploadRateLimit = dtc.UploadRateLimit
			changed = true
		}
	}

	return changed
}
