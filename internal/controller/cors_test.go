// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/torrplay/torrplay/internal/api"
)

func TestRouteScopedCORS(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		origin         string
		allowed        []string
		private        bool
		expectedOrigin string
	}{
		{name: "Stremio allows every origin", path: "/stremio/manifest.json", origin: "https://web.stremio.com", private: true, expectedOrigin: "*"},
		{name: "loopback UI is trusted", path: "/api/v1/settings", origin: "http://localhost:3000", expectedOrigin: "http://localhost:3000"},
		{name: "Tauri UI is trusted", path: "/api/v1/settings", origin: "tauri://localhost", expectedOrigin: "tauri://localhost"},
		{name: "configured UI is trusted", path: "/api/v1/settings", origin: "https://torrplay.example.com", allowed: []string{"https://torrplay.example.com"}, expectedOrigin: "https://torrplay.example.com"},
		{name: "untrusted website is rejected", path: "/api/v1/settings", origin: "https://evil.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &Controller{settings: &api.Settings{CorsAllowedOrigins: &tc.allowed}}
			handler := ctrl.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodOptions, tc.path, http.NoBody)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", http.MethodGet)
			if tc.private {
				req.Header.Set("Access-Control-Request-Private-Network", "true")
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tc.expectedOrigin, rr.Header().Get("Access-Control-Allow-Origin"))
			assert.Empty(t, rr.Header().Get("Access-Control-Allow-Credentials"))
			if tc.private {
				assert.Equal(t, "true", rr.Header().Get("Access-Control-Allow-Private-Network"))
				assert.Contains(t, rr.Header().Values("Vary"), "Access-Control-Request-Private-Network")
			}
		})
	}
}

func TestNormalizeOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin   string
		expected string
		valid    bool
	}{
		{origin: "HTTPS://UI.Example.COM/", expected: "https://ui.example.com", valid: true},
		{origin: "http://127.0.0.1:3000", expected: "http://127.0.0.1:3000", valid: true},
		{origin: "https://ui.example.com/path"},
		{origin: "https://user@ui.example.com"},
		{origin: "*"},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			actual, valid := normalizeOrigin(tc.origin)
			assert.Equal(t, tc.valid, valid)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestCORSAllowedHeaders(t *testing.T) {
	ctrl := &Controller{settings: &api.Settings{}}
	handler := ctrl.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/settings", http.NoBody)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "X-Requested-With")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Access-Control-Allow-Headers"), "X-Requested-With")
}
