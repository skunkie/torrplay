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
		Auth:                &api.Auth{Enabled: utils.Ptr(false)},
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
		TorrentClient: &api.TorrentClient{
			DisableDHT:                 utils.Ptr(false),
			DisableIPv6:                utils.Ptr(true),
			DisablePEX:                 utils.Ptr(false),
			DisableTCP:                 utils.Ptr(false),
			DisableUTP:                 utils.Ptr(false),
			DownloadRateLimit:          utils.Ptr(0),
			EstablishedConnsPerTorrent: utils.Ptr(50),
			PreferHeaderObfuscation:    utils.Ptr(false),
			Seed:                       utils.Ptr(false),
			TorrentPeersHighWater:      utils.Ptr(500),
			UploadRateLimit:            utils.Ptr(0),
		},
		TorrentTrackers: utils.Ptr([]string{}),
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
	if target.EnableDlna == nil {
		target.EnableDlna = defaults.EnableDlna
		changed = true
	}
	if target.EnableDownloader == nil {
		target.EnableDownloader = defaults.EnableDownloader
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
	if target.ReadaheadPercentage == nil {
		target.ReadaheadPercentage = defaults.ReadaheadPercentage
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
		if tc.UploadRateLimit == nil {
			tc.UploadRateLimit = dtc.UploadRateLimit
			changed = true
		}
	}

	return changed
}
