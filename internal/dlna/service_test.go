// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
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
func (m *mockDB) GetSettings() (*api.Settings, error) {
	return &api.Settings{
		EnableDlna:          utils.Ptr(true),
		FriendlyName:        utils.Ptr("test-server"),
		HTTPServerPort:      utils.Ptr(8080),
		LogLevel:            utils.Ptr(slog.LevelInfo),
		MaxMemory:           utils.Ptr(int64(1024 * 1024 * 1024)),
		DisableIpv6:         utils.Ptr(false),
		ReadaheadPercentage: utils.Ptr(20),
	}, nil
}

func (m *mockDB) GetDLNAUDN() (string, error) {
	return "uuid:12345678-1234-5678-1234-567812345678", nil
}

func (m *mockDB) GetTorrents() ([]*api.Torrent, error) {
	return testTorrents, nil
}

func (m *mockDB) GetTorrent(ih metainfo.Hash) (*api.Torrent, error) {
	return testTorrents[0], nil
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
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

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
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	if err := service.Stop(); err != nil {
		t.Fatalf("service.Stop() returned an error: %v", err)
	}

	if service.cancel != nil {
		t.Error("service.cancel was not cleared")
	}
}

func TestService_Reconfigure(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	if err := service.Reconfigure("new-test-server", "127.0.0.1", 8081); err != nil {
		t.Fatalf("service.Reconfigure() returned an error: %v", err)
	}
}

func TestService_ServeHTTP(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, &mockImages{}, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}

	req := httptest.NewRequest("GET", "/upnp/", nil)
	rw := httptest.NewRecorder()

	service.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("ServeHTTP returned status %d, expected %d", rw.Code, http.StatusOK)
	}
}
