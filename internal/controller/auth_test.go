// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/auth"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/metrics"
	"github.com/torrplay/torrplay/internal/stremio"
	"github.com/torrplay/torrplay/internal/utils"
)

func newAuthTestController(t *testing.T, updateSettings func(*api.Settings)) (*Controller, func()) {
	t.Helper()

	dbPath := tempfile()
	dbClient, err := database.NewBBoltDB(dbPath)
	require.NoError(t, err)

	metricsSvc := metrics.New()
	c, err := NewController(".", "127.0.0.1", 8080, dbClient, nil, metricsSvc)
	require.NoError(t, err)

	if updateSettings != nil {
		updateSettings(c.settings)
		err = dbClient.UpdateSettings(database.FromAPISettings(c.settings))
		require.NoError(t, err)
	}

	cleanup := func() {
		c.Shutdown()
		dbClient.Close()
		os.Remove(dbPath)
	}

	return c, cleanup
}

func TestNewAuthenticator(t *testing.T) {
	testCases := []struct {
		name          string
		settings      *api.Settings
		requestPath   string
		username      string
		password      string
		token         string
		schemeName    string
		expectedError string
	}{
		{
			name: "Auth Disabled",
			settings: &api.Settings{
				Auth: &api.Auth{Enabled: new(false)},
			},
			requestPath:   "/api/v1/torrents",
			expectedError: "",
		},
		{
			name: "Basic Auth - Success",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Basic),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			username:      "admin",
			password:      "password",
			schemeName:    "basicAuth",
			expectedError: "",
		},
		{
			name: "Basic Auth - Invalid Credentials",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Basic),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			username:      "admin",
			password:      "wrongpassword",
			schemeName:    "basicAuth",
			expectedError: "invalid credentials",
		},
		{
			name: "Basic Auth - Not Enabled",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Bearer),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			username:      "admin",
			password:      "password",
			schemeName:    "basicAuth",
			expectedError: "basic authentication is not enabled",
		},
		{
			name: "Bearer Auth - Success",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Bearer),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			schemeName:    "bearerAuth",
			expectedError: "",
		},
		{
			name: "Bearer Auth - Invalid Token",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Bearer),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			token:         "invalid-token",
			schemeName:    "bearerAuth",
			expectedError: "invalid token",
		},
		{
			name: "Bearer Auth - Not Enabled",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Basic),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			schemeName:    "bearerAuth",
			expectedError: "bearer authentication is not enabled",
		},
		{
			name: "Config Error - Missing Username",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Basic),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			schemeName:    "basicAuth",
			expectedError: "authentication not configured correctly",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
				if tc.settings.Auth != nil {
					s.Auth = tc.settings.Auth
				}
			})
			defer cleanup()

			authenticator := controller.NewAuthenticator()

			req := httptest.NewRequest(http.MethodGet, tc.requestPath, http.NoBody)

			if tc.username != "" && tc.password != "" {
				req.SetBasicAuth(tc.username, tc.password)
			}

			if tc.name == "Bearer Auth - Success" {
				secret, err := controller.db.GetJWTSecret()
				require.NoError(t, err)
				token, err := auth.GenerateToken("testuser", []byte(secret))
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+token)
			} else if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}

			input := &openapi3filter.AuthenticationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{
					Request: req,
					Route: &routers.Route{
						Path: tc.requestPath,
					},
				},
				SecuritySchemeName: tc.schemeName,
				SecurityScheme:     &openapi3.SecurityScheme{},
			}

			err := authenticator(context.Background(), input)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestQueryTokenAuthenticator(t *testing.T) {
	controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
		s.Auth = &api.Auth{
			Enabled:  new(true),
			Type:     utils.Ptr(api.Bearer),
			Username: new("admin"),
			Password: new("password"),
		}
	})
	defer cleanup()

	secret, err := controller.db.GetJWTSecret()
	require.NoError(t, err)
	token, err := auth.GenerateToken("testuser", []byte(secret))
	require.NoError(t, err)
	playbackToken, _, err := auth.GeneratePlaybackToken([]byte(secret))
	require.NoError(t, err)

	authenticator := controller.NewAuthenticator()
	for _, tc := range []struct {
		name      string
		token     string
		wantError bool
	}{
		{name: "playback token", token: playbackToken},
		{name: "full JWT rejected", token: token, wantError: true},
		{name: "missing", wantError: true},
		{name: "invalid", token: "invalid", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/hash", http.NoBody)
			if tc.token != "" {
				query := req.URL.Query()
				query.Set("token", tc.token)
				req.URL.RawQuery = query.Encode()
			}
			input := &openapi3filter.AuthenticationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{
					Request: req,
					Route:   &routers.Route{Path: "/api/v1/stream/{hash}"},
				},
				SecuritySchemeName: "queryTokenAuth",
				SecurityScheme:     &openapi3.SecurityScheme{},
			}

			err := authenticator(context.Background(), input)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("playback token cannot authenticate API requests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+playbackToken)
		input := &openapi3filter.AuthenticationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req},
			SecuritySchemeName:     "bearerAuth",
			SecurityScheme:         &openapi3.SecurityScheme{},
		}
		require.Error(t, authenticator(context.Background(), input))
	})

	t.Run("validates through HTTP middleware", func(t *testing.T) {
		controller.SetupRouter()
		rr := doGet(t, controller.router, "/api/v1/playlist?token="+playbackToken)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

		rr = doGet(t, controller.router, "/api/v1/playlist?token="+token)
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		rr = doGet(t, controller.router, "/api/v1/playlist")
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
	})
}

func TestStremioAuthenticationFollowsCurrentSettings(t *testing.T) {
	controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
		s.Auth = &api.Auth{
			Enabled:  new(true),
			Type:     utils.Ptr(api.Basic),
			Username: new("admin"),
			Password: new("password"),
		}
	})
	defer cleanup()

	secret, err := controller.db.GetJWTSecret()
	require.NoError(t, err)
	token := stremio.AccessToken(secret)

	assert.True(t, controller.validateStremioToken(token))
	assert.False(t, controller.validateStremioToken("invalid"))

	controller.mu.Lock()
	controller.settings.Auth.Enabled = new(false)
	controller.mu.Unlock()
	assert.True(t, controller.validateStremioToken(""))

	controller.mu.Lock()
	controller.settings.Auth.Enabled = new(true)
	controller.mu.Unlock()
	assert.False(t, controller.validateStremioToken(""))
}

func TestGetSettingsIncludesScopedStremioToken(t *testing.T) {
	controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
		s.Auth = &api.Auth{
			Enabled:  new(true),
			Type:     utils.Ptr(api.Basic),
			Username: new("admin"),
			Password: new("password"),
		}
	})
	defer cleanup()

	rr := httptest.NewRecorder()
	controller.GetSettings(rr, httptest.NewRequest(http.MethodGet, "/api/v1/settings", http.NoBody))
	require.Equal(t, http.StatusOK, rr.Code)

	var got api.Settings
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.NotNil(t, got.StremioToken)
	require.NotNil(t, got.PlaybackToken)
	secret, err := controller.db.GetJWTSecret()
	require.NoError(t, err)
	assert.Equal(t, stremio.AccessToken(secret), *got.StremioToken)
	assert.NotEqual(t, secret, *got.StremioToken)
	claims, err := auth.ValidateToken(*got.PlaybackToken, []byte(secret))
	require.NoError(t, err)
	assert.Equal(t, auth.PlaybackTokenScope, claims.Scope)
}

func TestCreateToken(t *testing.T) {
	controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
		s.Auth = &api.Auth{
			Enabled:  new(true),
			Type:     utils.Ptr(api.Bearer),
			Username: new("admin"),
			Password: new("password"),
		}
	})
	defer cleanup()

	t.Run("valid playback scope", func(t *testing.T) {
		body, err := json.Marshal(api.CreateTokenRequest{Scope: api.Playback})
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		controller.CreateToken(rr, httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, rr.Code)

		var response api.ScopedToken
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
		assert.Equal(t, string(api.Playback), response.Scope)

		secret, err := controller.db.GetJWTSecret()
		require.NoError(t, err)
		claims, err := auth.ValidateToken(response.Token, []byte(secret))
		require.NoError(t, err)
		assert.Equal(t, auth.PlaybackTokenScope, claims.Scope)
		assert.WithinDuration(t, claims.ExpiresAt.Time, response.ExpiresAt, time.Second)
	})

	t.Run("unsupported scope", func(t *testing.T) {
		body, err := json.Marshal(map[string]string{"scope": "invalid"})
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		controller.CreateToken(rr, httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid body", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.CreateToken(rr, httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader([]byte("invalid json"))))
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
