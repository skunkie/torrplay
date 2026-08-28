// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oapi-codegen/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

func TestProfilerRunsOnSeparateLoopbackListener(t *testing.T) {
	ctrl, _ := newTestController(t, func(c *Controller) {
		c.settings.LogLevel = utils.Ptr(slog.LevelDebug)
		c.profilerAddr = "127.0.0.1:0"
	})

	publicResponse := httptest.NewRecorder()
	ctrl.SetupRouter().ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	assert.Equal(t, http.StatusNotFound, publicResponse.Code)

	ctrl.profilerMu.Lock()
	listener := ctrl.profilerListener
	ctrl.profilerMu.Unlock()
	require.NotNil(t, listener)

	profilerAddr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	assert.True(t, profilerAddr.IP.IsLoopback())

	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get("http://" + listener.Addr().String() + "/debug/pprof/")
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestProfilerFollowsLogLevelUpdates(t *testing.T) {
	ctrl, _ := newTestController(t, func(c *Controller) {
		c.profilerAddr = "127.0.0.1:0"
	})
	publicRouter := ctrl.SetupRouter()
	assert.Nil(t, currentProfilerListener(ctrl))

	response := testutil.NewRequest().
		Patch("/api/v1/settings").
		WithJsonBody(api.Settings{LogLevel: utils.Ptr(slog.LevelDebug)}).
		GoWithHTTPHandler(t, publicRouter).
		Recorder
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.Same(t, publicRouter, ctrl.SetupRouter(), "a log-level change should not restart the public server")

	listener := currentProfilerListener(ctrl)
	require.NotNil(t, listener)
	profilerURL := "http://" + listener.Addr().String() + "/debug/pprof/"
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	profilerResponse, err := client.Get(profilerURL)
	require.NoError(t, err)
	require.NoError(t, profilerResponse.Body.Close())
	assert.Equal(t, http.StatusOK, profilerResponse.StatusCode)

	response = testutil.NewRequest().
		Patch("/api/v1/settings").
		WithJsonBody(api.Settings{LogLevel: utils.Ptr(slog.LevelInfo)}).
		GoWithHTTPHandler(t, publicRouter).
		Recorder
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.Nil(t, currentProfilerListener(ctrl))

	stoppedResponse, err := client.Get(profilerURL)
	if stoppedResponse != nil {
		_ = stoppedResponse.Body.Close()
	}
	assert.Error(t, err)
}

func currentProfilerListener(c *Controller) net.Listener {
	c.profilerMu.Lock()
	defer c.profilerMu.Unlock()
	return c.profilerListener
}
