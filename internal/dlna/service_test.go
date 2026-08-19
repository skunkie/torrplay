// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/ethulhu/helix/upnpav"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

const testTorrentFileName = "test file.mp4"

var (
	testTorrentHash = metainfo.NewHashFromHex("1234567890123456789012345678901234567890")
	testTorrents    = []*api.Torrent{
		{
			Hash: testTorrentHash,
			Name: "Test Torrent",
			Files: []api.TorrentFile{
				{Path: testTorrentFileName, Name: testTorrentFileName, Length: 1024},
			},
		},
	}
)

// mockDB is a mock implementation of the DatabaseInterface for testing purposes.
// It embeds the Unimplemented struct to satisfy the interface while allowing
// specific methods to be overridden for tests.
type mockDB struct {
	database.Unimplemented
}

// GetSettings returns mock settings for the test environment.
func (m *mockDB) GetSettings() (*database.Settings, error) {
	return &database.Settings{
		Settings: api.Settings{
			EnableDlna:          utils.Ptr(true),
			FriendlyName:        utils.Ptr("test-server"),
			HTTPServerPort:      utils.Ptr(8080),
			LogLevel:            utils.Ptr(slog.LevelInfo),
			MaxMemory:           utils.Ptr(int64(1024 * 1024 * 1024)),
			ReadaheadPercentage: utils.Ptr(20),
			TorrentClient: &api.TorrentClient{
				DisableIPv6:                utils.Ptr(false),
				EstablishedConnsPerTorrent: utils.Ptr(50),
				TorrentPeersHighWater:      utils.Ptr(100),
			},
		},
	}, nil
}

func (m *mockDB) GetDLNAUDN() (string, error) {
	return "uuid:12345678-1234-5678-1234-567812345678", nil
}

func (m *mockDB) GetTorrents() ([]*database.Torrent, error) {
	return database.FromAPITorrents(testTorrents), nil
}

func (m *mockDB) GetTorrent(ih metainfo.Hash) (*database.Torrent, error) {
	return database.FromAPITorrent(testTorrents[0]), nil
}

// newTestService creates a Service, starts it on 127.0.0.1 with a fixed
// test address, and registers a cleanup that stops the service when the test
// finishes. Using t.Cleanup guarantees the UPnP broadcast goroutine launched
// by Start is cancelled before the next test (or the next -count iteration of
// the same test) starts a new one.
func newTestService(t *testing.T) (*Service, func()) {
	t.Helper()

	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	require.NoError(t, service.Start("test-server", "127.0.0.1", 8080))

	return service, func() { _ = service.Stop() }
}

func TestNewService(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if service == nil {
		t.Fatal("NewService returned nil")
	}

	if service.db != db {
		t.Error("service.db was not set correctly")
	}

	if service.logger != logger {
		t.Error("service.logger was not set correctly")
	}
}

func TestService_Start(t *testing.T) {
	service, cleanup := newTestService(t)
	defer cleanup()

	if service.cancel == nil {
		t.Error("service.cancel was not set")
	}

	if service.device == nil {
		t.Error("service.device was not set")
	}

	if service.handler == nil {
		t.Error("service.handler was not set")
	}
}

func TestService_Start_NoErrorWithUnspecifiedIP(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "0.0.0.0", 8080); err != nil {
		t.Fatalf("service.Start() returned an error for an unspecified IP: %v", err)
	}
}

func TestService_Stop(t *testing.T) {
	service, cleanup := newTestService(t)
	defer cleanup()

	if err := service.Stop(); err != nil {
		t.Fatalf("service.Stop() returned an error: %v", err)
	}

	if service.cancel != nil {
		t.Error("service.cancel was not cleared")
	}
}

func TestService_Reconfigure(t *testing.T) {
	service, cleanup := newTestService(t)
	defer cleanup()

	if err := service.Reconfigure("new-test-server", "127.0.0.1", 8081); err != nil {
		t.Fatalf("service.Reconfigure() returned an error: %v", err)
	}
}

func TestService_ServeHTTP(t *testing.T) {
	service, cleanup := newTestService(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/upnp/", nil)
	rw := httptest.NewRecorder()

	service.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("ServeHTTP returned status %d, expected %d", rw.Code, http.StatusOK)
	}
}

func TestService_ConnectionManagerAndRegistrar(t *testing.T) {
	service, cleanup := newTestService(t)
	defer cleanup()

	// Test ConnectionManager service handler is registered
	cmHandler, ok := service.device.SOAPInterface("urn:schemas-upnp-org:service:ConnectionManager:1")
	if !ok || cmHandler == nil {
		t.Fatal("ConnectionManager SOAPInterface is not registered")
	}

	// Test MediaReceiverRegistrar service handler is registered
	mrrHandler, ok := service.device.SOAPInterface("urn:microsoft.com:service:X_MS_MediaReceiverRegistrar:1")
	if !ok || mrrHandler == nil {
		t.Fatal("MediaReceiverRegistrar SOAPInterface is not registered")
	}
}

func TestAddHeader(t *testing.T) {
	t.Run("no DLNA headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		AddHeader(rec, req)
		assert.Empty(t, rec.Header().Get("contentFeatures.dlna.org"))
		assert.Empty(t, rec.Header().Get("transferMode.dlna.org"))
	})

	t.Run("getContentFeatures requested", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("getContentFeatures.dlna.org", "1")
		rec := httptest.NewRecorder()
		AddHeader(rec, req)
		assert.Equal(t, upnpav.ContentFeatures, rec.Header().Get("contentFeatures.dlna.org"))
		assert.Equal(t, "Streaming", rec.Header().Get("transferMode.dlna.org"))
	})

	t.Run("custom transferMode provided", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("transferMode.dlna.org", "Background")
		rec := httptest.NewRecorder()
		AddHeader(rec, req)
		assert.Empty(t, rec.Header().Get("contentFeatures.dlna.org"))
		assert.Equal(t, "Background", rec.Header().Get("transferMode.dlna.org"))
	})

	t.Run("both headers provided", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("getContentFeatures.dlna.org", "1")
		req.Header.Set("transferMode.dlna.org", "Interactive")
		rec := httptest.NewRecorder()
		AddHeader(rec, req)
		assert.Equal(t, upnpav.ContentFeatures, rec.Header().Get("contentFeatures.dlna.org"))
		assert.Equal(t, "Interactive", rec.Header().Get("transferMode.dlna.org"))
	})
}

func TestService_SetLogger(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	newLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	service.SetLogger(newLogger)
	assert.Equal(t, newLogger, service.logger)
}

func TestService_IncrementSystemUpdateID(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	// When content directory is nil (not started)
	service.IncrementSystemUpdateID()

	// When service is running
	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}
	defer service.Stop()

	initialID, err := service.contentDirectory.SystemUpdateID(t.Context())
	assert.NoError(t, err)

	service.IncrementSystemUpdateID()
	newID, err := service.contentDirectory.SystemUpdateID(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, initialID+1, newID)
}

func TestService_SendUpdateNotification(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	// When stopped (no-op)
	service.SendUpdateNotification()

	// When running
	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}
	defer service.Stop()

	service.SendUpdateNotification()
}

func TestService_ServeHTTP_IconsAndNotFound(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	t.Run("handler is nil when stopped", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/upnp/test", nil)
		rec := httptest.NewRecorder()
		service.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("serve embedded icon", func(t *testing.T) {
		if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
			t.Fatalf("service.Start() returned an error: %v", err)
		}
		defer service.Stop()

		req := httptest.NewRequest(http.MethodGet, "/upnp/icons/device/icon-128x128.png", nil)
		rec := httptest.NewRecorder()
		service.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "max-age=600", rec.Header().Get("Cache-Control"))
	})
}

func TestService_StartAlreadyRunning(t *testing.T) {
	service, cleanup := newTestService(t)
	defer cleanup()

	err := service.Start("test-server", "127.0.0.1", 8080)
	assert.ErrorContains(t, err, "already running")
}

func TestDeviceIcons_Error(t *testing.T) {
	baseURL, _ := url.Parse("http://127.0.0.1:8080")
	_, err := deviceIcons(iconsFS, "nonexistent-dir", baseURL)
	assert.Error(t, err)
}
