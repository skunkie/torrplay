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
	"github.com/torrplay/torrplay/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.VerifyTestMain(m, "github.com/ethulhu/helix", "github.com/skunkie/helix")
}

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

// mockDB provides the database reads used by the DLNA service in tests.
type mockDB struct{}

func (m *mockDB) GetDLNAUDN() (string, error) {
	return "uuid:12345678-1234-5678-1234-567812345678", nil
}

func (m *mockDB) GetTorrents() ([]*database.Torrent, error) {
	return database.FromAPITorrents(testTorrents), nil
}

func (m *mockDB) GetTorrent(ih metainfo.Hash) (*database.Torrent, error) {
	return database.FromAPITorrent(testTorrents[0]), nil
}

func TestNewService(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, "/upnp/", "/posters/", logger)

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
	service := NewService(db, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}
	defer func() { _ = service.Stop() }()

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
	service := NewService(db, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "0.0.0.0", 8080); err != nil {
		t.Fatalf("service.Start() returned an error for an unspecified IP: %v", err)
	}
}

func TestService_Stop(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, "/upnp/", "/posters/", logger)

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
	service := NewService(db, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}
	defer func() { _ = service.Stop() }()

	if err := service.Reconfigure("new-test-server", "127.0.0.1", 8081); err != nil {
		t.Fatalf("service.Reconfigure() returned an error: %v", err)
	}
}

func TestService_ServeHTTP(t *testing.T) {
	db := &mockDB{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	service := NewService(db, "/upnp/", "/posters/", logger)

	if err := service.Start("test-server", "127.0.0.1", 8080); err != nil {
		t.Fatalf("service.Start() returned an error: %v", err)
	}
	defer func() { _ = service.Stop() }()

	req := httptest.NewRequest(http.MethodGet, "/upnp/", http.NoBody)
	rw := httptest.NewRecorder()

	service.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("ServeHTTP returned status %d, expected %d", rw.Code, http.StatusOK)
	}
}
