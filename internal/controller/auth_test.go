// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

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
	secret, err := controller.db.GetJWTSecret()
	require.NoError(t, err)
	assert.Equal(t, stremio.AccessToken(secret), *got.StremioToken)
	assert.NotEqual(t, secret, *got.StremioToken)
}
>>>>>>> 1ee093a (feat(stremio): add native stremio addon protocol support)
