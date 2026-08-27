// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package settings

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestDefault(t *testing.T) {
	d := Default()

	require.NotNil(t, d.Auth)
	assert.False(t, *d.Auth.Enabled)

	require.NotNil(t, d.EnableDlna)
	assert.False(t, *d.EnableDlna)

	require.NotNil(t, d.EnableDownloader)
	assert.False(t, *d.EnableDownloader)

	require.NotNil(t, d.FileStoragePath)
	assert.Equal(t, "", *d.FileStoragePath)

	require.NotNil(t, d.FriendlyName)
	assert.Equal(t, "TorrPlay", *d.FriendlyName)

	require.NotNil(t, d.HTTPServerPort)
	assert.Equal(t, 8090, *d.HTTPServerPort)

	require.NotNil(t, d.LogFormat)
	assert.Equal(t, api.Text, *d.LogFormat)

	require.NotNil(t, d.LogLevel)
	assert.Equal(t, slog.LevelInfo, *d.LogLevel)

	require.NotNil(t, d.LogStoreSize)
	assert.Equal(t, 100, *d.LogStoreSize)

	require.NotNil(t, d.MaxMemory)
	assert.Equal(t, int64(64*1024*1024), *d.MaxMemory)

	require.NotNil(t, d.TorrentClient)
	assert.False(t, *d.TorrentClient.DisableDHT)
	assert.True(t, *d.TorrentClient.DisableIPv6)
	assert.False(t, *d.TorrentClient.DisablePEX)
	assert.False(t, *d.TorrentClient.DisableTCP)
	assert.False(t, *d.TorrentClient.DisableUTP)
	assert.Equal(t, 0, *d.TorrentClient.DownloadRateLimit)
	assert.Equal(t, 50, *d.TorrentClient.EstablishedConnsPerTorrent)
	assert.Equal(t, 25, *d.TorrentClient.HalfOpenConnsPerTorrent)
	assert.Equal(t, 1048576, *d.TorrentClient.MaxAllocPeerRequestDataPerConn)
	assert.False(t, *d.TorrentClient.PreferHeaderObfuscation)
	assert.False(t, *d.TorrentClient.Seed)
	assert.Equal(t, 500, *d.TorrentClient.TorrentPeersHighWater)
	assert.Equal(t, 50, *d.TorrentClient.TorrentPeersLowWater)
	assert.Equal(t, 100, *d.TorrentClient.TotalHalfOpenConns)
	assert.Equal(t, 0, *d.TorrentClient.UploadRateLimit)

	require.NotNil(t, d.TorrentTrackers)
	assert.Empty(t, *d.TorrentTrackers)
}

func TestMerge(t *testing.T) {
	t.Run("nil target", func(t *testing.T) {
		assert.False(t, Merge(nil, Default()))
	})

	t.Run("empty target gets all defaults", func(t *testing.T) {
		target := &api.Settings{}
		changed := Merge(target, Default())
		assert.True(t, changed)

		assert.Equal(t, "TorrPlay", *target.FriendlyName)
		assert.Equal(t, 8090, *target.HTTPServerPort)
		assert.NotNil(t, target.TorrentClient)
		assert.Equal(t, 50, *target.TorrentClient.EstablishedConnsPerTorrent)
	})

	t.Run("existing values are preserved", func(t *testing.T) {
		customPort := 9999
		target := &api.Settings{
			HTTPServerPort: &customPort,
			FriendlyName:   utils.Ptr("MyCustomTorrPlay"),
			TorrentClient: &api.TorrentClient{
				DownloadRateLimit: utils.Ptr(1024),
			},
		}

		changed := Merge(target, Default())
		assert.True(t, changed)

		assert.Equal(t, 9999, *target.HTTPServerPort)
		assert.Equal(t, "MyCustomTorrPlay", *target.FriendlyName)
		assert.Equal(t, 1024, *target.TorrentClient.DownloadRateLimit)
		// Missing sub-fields filled from defaults
		assert.Equal(t, 50, *target.TorrentClient.EstablishedConnsPerTorrent)
	})

	t.Run("fully populated target remains unchanged", func(t *testing.T) {
		target := Default()
		changed := Merge(&target, Default())
		assert.False(t, changed)
	})

	t.Run("target with non-nil empty sub-structs", func(t *testing.T) {
		target := &api.Settings{
			Auth:          &api.Auth{},
			TorrentClient: &api.TorrentClient{},
		}
		changed := Merge(target, Default())
		assert.True(t, changed)
		require.NotNil(t, target.Auth.Enabled)
		assert.False(t, *target.Auth.Enabled)
		assert.False(t, *target.TorrentClient.DisableDHT)
		assert.True(t, *target.TorrentClient.DisableIPv6)
		assert.False(t, *target.TorrentClient.DisablePEX)
		assert.False(t, *target.TorrentClient.DisableTCP)
		assert.False(t, *target.TorrentClient.DisableUTP)
		assert.Equal(t, 0, *target.TorrentClient.DownloadRateLimit)
		assert.Equal(t, 50, *target.TorrentClient.EstablishedConnsPerTorrent)
		assert.Equal(t, 25, *target.TorrentClient.HalfOpenConnsPerTorrent)
		assert.Equal(t, 1048576, *target.TorrentClient.MaxAllocPeerRequestDataPerConn)
		assert.False(t, *target.TorrentClient.PreferHeaderObfuscation)
		assert.False(t, *target.TorrentClient.Seed)
		assert.Equal(t, 500, *target.TorrentClient.TorrentPeersHighWater)
		assert.Equal(t, 50, *target.TorrentClient.TorrentPeersLowWater)
		assert.Equal(t, 100, *target.TorrentClient.TotalHalfOpenConns)
		assert.Equal(t, 0, *target.TorrentClient.UploadRateLimit)
	})
}
