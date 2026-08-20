//go:build integration

// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/utils"
)

type localWebseedFixture struct {
	meta      *metainfo.MetaInfo
	payload   []byte
	requested <-chan struct{}
	release   func()
}

func newIntegrationTestController(t *testing.T) *Controller {
	t.Helper()
	runtimeConfig := testControllerRuntimeConfig()
	runtimeConfig.configureClient = func(config *torrent.ClientConfig) {
		config.NoDHT = true
		config.DisablePEX = true
		config.DisableTrackers = true
		config.DisableWebtorrent = true
		config.DisableWebseeds = false
		config.NoDefaultPortForwarding = true
		config.DisableTCP = true
		config.DisableUTP = true
	}
	ctrl, cleanup := newTestControllerWithRuntimeConfig(t, runtimeConfig)
	t.Cleanup(cleanup)
	return ctrl
}

func newLocalWebseedFixture(t *testing.T, block bool) localWebseedFixture {
	t.Helper()
	payload := bytes.Repeat([]byte("torrplay-local-webseed\n"), 4096)
	requested := make(chan struct{})
	release := make(chan struct{})
	var requestOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestOnce.Do(func() { close(requested) })
		if block {
			<-release
		}
		http.ServeContent(w, r, "video.mp4", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(server.Close)

	const pieceLength = 16 * 1024
	pieces := make([]byte, 0, ((len(payload)+pieceLength-1)/pieceLength)*sha1.Size)
	for offset := 0; offset < len(payload); offset += pieceLength {
		end := min(offset+pieceLength, len(payload))
		sum := sha1.Sum(payload[offset:end])
		pieces = append(pieces, sum[:]...)
	}
	info := metainfo.Info{
		Name:        "Sintel",
		PieceLength: pieceLength,
		Pieces:      pieces,
		Files: []metainfo.FileInfo{{
			Length: int64(len(payload)),
			Path:   []string{"Sintel.mp4"},
		}},
	}
	infoBytes, err := bencode.Marshal(info)
	require.NoError(t, err)
	meta := &metainfo.MetaInfo{
		InfoBytes: infoBytes,
		UrlList:   metainfo.UrlList{server.URL + "/"},
	}

	var releaseOnce sync.Once
	return localWebseedFixture{
		meta:      meta,
		payload:   payload,
		requested: requested,
		release:   func() { releaseOnce.Do(func() { close(release) }) },
	}
}

func addLocalWebseedTorrent(t *testing.T, ctrl *Controller, fixture localWebseedFixture) metainfo.Hash {
	t.Helper()
	spec := torrent.TorrentSpecFromMetaInfo(fixture.meta)
	_, _, err := ctrl.client.AddTorrentSpec(spec)
	require.NoError(t, err)

	ih := fixture.meta.HashInfoBytes()
	storageType := api.Memory
	title := "Sintel"
	err = ctrl.db.CreateTorrent(&database.Torrent{Torrent: api.Torrent{
		Hash:      ih,
		Magnet:    utils.MagnetURIFromHash(ih),
		Name:      "Sintel",
		Title:     &title,
		Storage:   &storageType,
		TotalSize: int64(len(fixture.payload)),
	}})
	require.NoError(t, err)
	return ih
}

func startBlockedIntegrationStream(t *testing.T, ctrl *Controller, fixture localWebseedFixture, endpoint string) (<-chan error, *torrent.Torrent) {
	t.Helper()
	server := httptest.NewServer(ctrl.router)
	t.Cleanup(server.Close)
	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, server.URL+endpoint, http.NoBody)
		if err != nil {
			done <- err
			return
		}
		req.Header.Set("Range", "bytes=0-")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusPartialContent {
			done <- fmt.Errorf("unexpected stream status: %s", resp.Status)
			return
		}
		_, err = io.Copy(io.Discard, resp.Body)
		done <- err
	}()

	select {
	case <-fixture.requested:
	case <-time.After(10 * time.Second):
		t.Fatal("local webseed was not requested")
	}
	ih := fixture.meta.HashInfoBytes()
	to, ok := ctrl.client.Torrent(ih)
	require.True(t, ok)
	return done, to
}

func finishBlockedIntegrationStream(t *testing.T, fixture localWebseedFixture, done <-chan error) {
	t.Helper()
	fixture.release()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish cleanly")
	}
}

func TestIntegrationAddTorrentWhileStreaming(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	to, _, err := ctrl.client.AddTorrentSpec(torrent.TorrentSpecFromMetaInfo(fixture.meta))
	require.NoError(t, err)
	ih := fixture.meta.HashInfoBytes()
	done, activeTorrent := startBlockedIntegrationStream(t, ctrl, fixture, fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih))
	require.Same(t, to, activeTorrent)

	magnet := utils.MagnetURIFromHash(ih)
	rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(api.TorrentAdd{Magnet: &magnet}).GoWithHTTPHandler(t, ctrl.router).Recorder
	require.Equal(t, http.StatusCreated, rr.Code)
	loadedTorrent, ok := ctrl.client.Torrent(ih)
	require.True(t, ok)
	assert.Same(t, activeTorrent, loadedTorrent)
	select {
	case <-loadedTorrent.Closed():
		t.Fatal("adding an active torrent closed the stream")
	default:
	}

	finishBlockedIntegrationStream(t, fixture, done)
}

func TestIntegrationStreamingFromLocalWebseed(t *testing.T) {
	fixture := newLocalWebseedFixture(t, false)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)

	server := httptest.NewServer(ctrl.router)
	defer server.Close()

	for _, endpoint := range []string{
		fmt.Sprintf("/api/v1/stream/%s?path=Sintel/Sintel.mp4", ih),
		fmt.Sprintf("/api/v1/stream/%s?index=0", ih),
		fmt.Sprintf("/stream/Sintel.mp4?link=%s&play&index=0", ih),
		fmt.Sprintf("/stream/Sintel.mp4?link=%s&play", ih),
	} {
		req, err := http.NewRequest(http.MethodGet, server.URL+endpoint, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Range", "bytes=0-1023")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, readErr)
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Equal(t, fixture.payload[:1024], body)
	}
}

func TestIntegrationDeleteWhileStreamingUsesStateSynchronization(t *testing.T) {
	fixture := newLocalWebseedFixture(t, true)
	defer fixture.release()
	ctrl := newIntegrationTestController(t)
	ih := addLocalWebseedTorrent(t, ctrl, fixture)

	server := httptest.NewServer(ctrl.router)
	defer server.Close()
	streamDone := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/stream/%s?path=Sintel/Sintel.mp4", server.URL, ih), http.NoBody)
		if err == nil {
			req.Header.Set("Range", "bytes=0-")
			var resp *http.Response
			resp, err = http.DefaultClient.Do(req)
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
		streamDone <- err
	}()

	select {
	case <-fixture.requested:
	case <-time.After(5 * time.Second):
		t.Fatal("local webseed was not requested")
	}

	otherHashes := []metainfo.Hash{bunnyHash, cosmosHash}
	for _, otherHash := range otherHashes {
		primeSampleMetadata(t, ctrl, otherHash)
		magnet := samples[otherHash]
		rr := testutil.NewRequest().Post("/api/v1/torrents").WithJsonBody(api.TorrentAdd{Magnet: &magnet}).GoWithHTTPHandler(t, ctrl.router).Recorder
		require.Equal(t, http.StatusCreated, rr.Code)
	}
	otherDeletes := make(chan int, len(otherHashes))
	for _, otherHash := range otherHashes {
		go func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodDelete, "/api/v1/torrents/"+otherHash.HexString(), http.NoBody)
			ctrl.router.ServeHTTP(recorder, request)
			otherDeletes <- recorder.Code
		}()
	}
	for range otherHashes {
		assert.Equal(t, http.StatusNoContent, <-otherDeletes)
	}

	deleteDone := make(chan int, 1)
	go func() {
		rr := testutil.NewRequest().Delete("/api/v1/torrents/"+ih.HexString()).GoWithHTTPHandler(t, ctrl.router).Recorder
		deleteDone <- rr.Code
	}()
	fixture.release()

	select {
	case code := <-deleteDone:
		assert.Equal(t, http.StatusNoContent, code)
	case <-time.After(5 * time.Second):
		t.Fatal("delete deadlocked with active stream")
	}
	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not exit after torrent deletion")
	}
}
