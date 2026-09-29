// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/golang-jwt/jwt/v5"
	"github.com/oapi-codegen/testutil"
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
	c, err := newController(".", "127.0.0.1", 8080, dbClient, nil, metricsSvc, testControllerRuntimeConfig())
	require.NoError(t, err)

	if updateSettings != nil {
		updateSettings(c.settings.Load())
		err = dbClient.UpdateSettings(database.FromAPISettings(c.settings.Load()))
		require.NoError(t, err)
	}

	c.SetupRouter()

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
		tokenUsername string
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
			tokenUsername: "admin",
			schemeName:    "bearerAuth",
			expectedError: "",
		},
		{
			name: "bearer auth rejects a token issued to a different user",
			settings: &api.Settings{
				Auth: &api.Auth{
					Enabled:  new(true),
					Type:     utils.Ptr(api.Bearer),
					Username: new("admin"),
					Password: new("password"),
				},
			},
			requestPath:   "/api/v1/torrents",
			tokenUsername: "former-admin",
			schemeName:    "bearerAuth",
			expectedError: "token was issued to a different user",
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

			if tc.tokenUsername != "" {
				secret, err := controller.db.GetJWTSecret()
				require.NoError(t, err)
				token, err := auth.GenerateToken(tc.tokenUsername, []byte(secret))
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

func TestDLNAPlaybackToken(t *testing.T) {
	controller, cleanup := newAuthTestController(t, nil)
	defer cleanup()

	token, err := controller.dlnaPlaybackToken()
	require.NoError(t, err)
	assert.Empty(t, token)

	for _, authType := range []api.AuthType{api.Bearer, api.Basic} {
		t.Run(string(authType), func(t *testing.T) {
			controller.mu.Lock()
			controller.settings.Load().Auth = &api.Auth{
				Enabled:  new(true),
				Type:     utils.Ptr(authType),
				Username: new("admin"),
				Password: new("password"),
			}
			controller.mu.Unlock()

			token, err := controller.dlnaPlaybackToken()
			require.NoError(t, err)
			require.NotEmpty(t, token)

			secret, err := controller.db.GetJWTSecret()
			require.NoError(t, err)
			claims, err := auth.ValidateToken(token, []byte(secret))
			require.NoError(t, err)
			assert.Equal(t, auth.PlaybackTokenScope, claims.Scope)
		})
	}

	controller.mu.Lock()
	controller.settings.Load().Auth.Enabled = new(false)
	controller.mu.Unlock()
	token, err = controller.dlnaPlaybackToken()
	require.NoError(t, err)
	assert.Empty(t, token)
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
	token, err := auth.GenerateToken("admin", []byte(secret))
	require.NoError(t, err)
	playbackToken, _, err := auth.GeneratePlaybackToken([]byte(secret))
	require.NoError(t, err)
	expiredPlaybackToken := signedPlaybackToken(t, secret, time.Now().Add(-time.Hour))

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
		{name: "expired", token: expiredPlaybackToken, wantError: true},
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

	t.Run("compatibility routes still require tokens with bearer auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/play/hash/0", http.NoBody)
		input := &openapi3filter.AuthenticationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req},
			SecuritySchemeName:     "compatQueryTokenAuth",
			SecurityScheme:         &openapi3.SecurityScheme{},
		}
		require.Error(t, authenticator(context.Background(), input))

		query := req.URL.Query()
		query.Set("token", playbackToken)
		req.URL.RawQuery = query.Encode()
		require.NoError(t, authenticator(context.Background(), input))
	})

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

		for _, requestPath := range []string{
			"/play/0000000000000000000000000000000000000000/0",
			"/stream/video.mp4?link=invalid",
		} {
			rr = doGet(t, controller.router, requestPath)
			require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
		}
	})
}

func TestQueryTokenAuthenticatorWithBasicAuth(t *testing.T) {
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
	fullToken, err := auth.GenerateToken("admin", []byte(secret))
	require.NoError(t, err)
	playbackToken, _, err := auth.GeneratePlaybackToken([]byte(secret))
	require.NoError(t, err)
	expiredPlaybackToken := signedPlaybackToken(t, secret, time.Now().Add(-time.Hour))

	authenticator := controller.NewAuthenticator()
	for _, tc := range []struct {
		name      string
		token     string
		wantError bool
	}{
		{name: "playback token", token: playbackToken},
		{name: "full JWT rejected", token: fullToken, wantError: true},
		{name: "missing", wantError: true},
		{name: "invalid", token: "invalid", wantError: true},
		{name: "expired", token: expiredPlaybackToken, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/hash", http.NoBody)
			if tc.token != "" {
				query := req.URL.Query()
				query.Set("token", tc.token)
				req.URL.RawQuery = query.Encode()
			}
			input := &openapi3filter.AuthenticationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req},
				SecuritySchemeName:     "queryTokenAuth",
				SecurityScheme:         &openapi3.SecurityScheme{},
			}

			err := authenticator(context.Background(), input)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("compatibility routes remain token-free", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/play/hash/0", http.NoBody)
		input := &openapi3filter.AuthenticationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req},
			SecuritySchemeName:     "compatQueryTokenAuth",
			SecurityScheme:         &openapi3.SecurityScheme{},
		}
		require.NoError(t, authenticator(context.Background(), input))
	})

	t.Run("validates through HTTP middleware", func(t *testing.T) {
		rr := doGet(t, controller.router, "/api/v1/playlist")
		require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())

		rr = doGet(t, controller.router, "/api/v1/playlist?token="+playbackToken)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

		for _, requestPath := range []string{
			"/play/0000000000000000000000000000000000000000/0",
			"/stream/video.mp4?link=invalid",
		} {
			rr = doGet(t, controller.router, requestPath)
			require.NotEqual(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
		}
	})
}

func signedPlaybackToken(t *testing.T, secret string, expiresAt time.Time) string {
	t.Helper()
	now := time.Now()
	claims := &auth.Claims{
		Scope: auth.PlaybackTokenScope,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-2 * time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return token
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
	controller.settings.Load().Auth.Enabled = new(false)
	controller.mu.Unlock()
	assert.True(t, controller.validateStremioToken(""))

	controller.mu.Lock()
	controller.settings.Load().Auth.Enabled = new(true)
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

func TestBasicAuthWWWAuthenticateSuppression(t *testing.T) {
	controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
		s.Auth = &api.Auth{
			Enabled:  utils.Ptr(true),
			Type:     utils.Ptr(api.Basic),
			Username: utils.Ptr("admin"),
			Password: utils.Ptr("password"),
		}
	})
	defer cleanup()

	t.Run("omitted X-Requested-With header sends standard Basic challenge", func(t *testing.T) {
		rr := testutil.NewRequest().Get("/api/v1/torrents").GoWithHTTPHandler(t, controller.router).Recorder
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, `Basic realm="TorrPlay"`, rr.Header().Get("WWW-Authenticate"))
	})

	t.Run("X-Requested-With: XMLHttpRequest sends x-Basic challenge", func(t *testing.T) {
		rr := testutil.NewRequest().
			Get("/api/v1/torrents").
			WithHeader("X-Requested-With", "XMLHttpRequest").
			GoWithHTTPHandler(t, controller.router).Recorder
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, `x-Basic realm="TorrPlay"`, rr.Header().Get("WWW-Authenticate"))
	})

	t.Run("Bearer auth retains Bearer challenge even with XMLHttpRequest", func(t *testing.T) {
		controller.mu.Lock()
		controller.settings.Load().Auth.Type = utils.Ptr(api.Bearer)
		controller.mu.Unlock()

		rr := testutil.NewRequest().
			Get("/api/v1/torrents").
			WithHeader("X-Requested-With", "XMLHttpRequest").
			GoWithHTTPHandler(t, controller.router).Recorder
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, `Bearer realm="TorrPlay"`, rr.Header().Get("WWW-Authenticate"))
	})
}

func TestMetricsEndpointRequiresAuthentication(t *testing.T) {
	scrape := func(t *testing.T, controller *Controller, setAuth func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
		if setAuth != nil {
			setAuth(req)
		}
		rr := httptest.NewRecorder()
		controller.router.ServeHTTP(rr, req)
		return rr
	}
	bearer := func(token string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	}

	t.Run("auth disabled", func(t *testing.T) {
		controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
			s.Auth = &api.Auth{Enabled: new(false)}
		})
		defer cleanup()

		rr := scrape(t, controller, nil)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Contains(t, rr.Body.String(), "torrplay_storage_memory_limit_bytes")
	})

	t.Run("basic auth", func(t *testing.T) {
		controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
			s.Auth = &api.Auth{Enabled: new(true), Type: utils.Ptr(api.Basic), Username: new("admin"), Password: new("password")}
		})
		defer cleanup()

		rr := scrape(t, controller, nil)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, `Basic realm="TorrPlay"`, rr.Header().Get("WWW-Authenticate"))
		assert.NotContains(t, rr.Body.String(), "torrplay_")

		rr = scrape(t, controller, func(r *http.Request) { r.SetBasicAuth("admin", "wrong") })
		assert.Equal(t, http.StatusUnauthorized, rr.Code)

		rr = scrape(t, controller, func(r *http.Request) { r.SetBasicAuth("admin", "password") })
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Contains(t, rr.Body.String(), "torrplay_storage_memory_limit_bytes")
	})

	t.Run("bearer auth", func(t *testing.T) {
		controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
			s.Auth = &api.Auth{Enabled: new(true), Type: utils.Ptr(api.Bearer), Username: new("admin"), Password: new("password")}
		})
		defer cleanup()
		secret, err := controller.db.GetJWTSecret()
		require.NoError(t, err)
		token, err := auth.GenerateToken("admin", []byte(secret))
		require.NoError(t, err)
		playbackToken, _, err := auth.GeneratePlaybackToken([]byte(secret))
		require.NoError(t, err)

		assert.Equal(t, http.StatusUnauthorized, scrape(t, controller, nil).Code)
		assert.Equal(t, http.StatusUnauthorized, scrape(t, controller, bearer(playbackToken)).Code,
			"playback-scoped tokens must not grant metrics access")

		rr := scrape(t, controller, bearer(token))
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Contains(t, rr.Body.String(), "torrplay_storage_memory_limit_bytes")
	})
}

func TestUpdateSettingsRotatesJWTSecretOnAuthChange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		patch  api.Settings
		rotate bool
	}{
		{name: "password change", patch: api.Settings{Auth: &api.Auth{Password: new("new-password")}}, rotate: true},
		{name: "username change", patch: api.Settings{Auth: &api.Auth{Username: new("new-admin")}}, rotate: true},
		{name: "type change", patch: api.Settings{Auth: &api.Auth{Type: utils.Ptr(api.Basic)}}, rotate: true},
		{name: "auth disabled", patch: api.Settings{Auth: &api.Auth{Enabled: new(false)}}, rotate: true},
		{name: "unchanged credentials", patch: api.Settings{Auth: &api.Auth{
			Enabled:  new(true),
			Type:     utils.Ptr(api.Bearer),
			Username: new("admin"),
			Password: new("password"),
		}}},
		{name: "unrelated setting", patch: api.Settings{FriendlyName: new("Renamed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
				s.Auth = &api.Auth{Enabled: new(true), Type: utils.Ptr(api.Bearer), Username: new("admin"), Password: new("password")}
			})
			defer cleanup()

			secretBefore, err := controller.db.GetJWTSecret()
			require.NoError(t, err)
			token, err := auth.GenerateToken("admin", []byte(secretBefore))
			require.NoError(t, err)
			playbackToken, _, err := auth.GeneratePlaybackToken([]byte(secretBefore))
			require.NoError(t, err)

			rr := testutil.NewRequest().Patch("/api/v1/settings").
				WithHeader("Authorization", "Bearer "+token).
				WithJsonBody(tc.patch).
				GoWithHTTPHandler(t, controller.router).Recorder
			require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

			secretAfter, err := controller.db.GetJWTSecret()
			require.NoError(t, err)
			require.NotEmpty(t, secretAfter)
			if !tc.rotate {
				assert.Equal(t, secretBefore, secretAfter)
				rr = testutil.NewRequest().Get("/api/v1/torrents").
					WithHeader("Authorization", "Bearer "+token).
					GoWithHTTPHandler(t, controller.router).Recorder
				assert.Equal(t, http.StatusOK, rr.Code, "tokens issued before the update must stay valid")
				return
			}

			assert.NotEqual(t, secretBefore, secretAfter)
			_, err = auth.ValidateToken(token, []byte(secretAfter))
			assert.Error(t, err, "API tokens issued before the update must be revoked")
			_, err = auth.ValidateToken(playbackToken, []byte(secretAfter))
			assert.Error(t, err, "playback tokens issued before the update must be revoked")
			assert.False(t, stremio.ValidateAccessToken(stremio.AccessToken(secretBefore), secretAfter),
				"Stremio tokens issued before the update must be revoked")

			current := controller.settings.Load().Auth
			if !utils.Val(current.Enabled) {
				return
			}
			if utils.Val(current.Type) == api.Bearer {
				rr = testutil.NewRequest().Get("/api/v1/torrents").
					WithHeader("Authorization", "Bearer "+token).
					GoWithHTTPHandler(t, controller.router).Recorder
				assert.Equal(t, http.StatusUnauthorized, rr.Code, "the API must reject tokens issued before the update")
			}

			rr = httptest.NewRecorder()
			controller.GetSettings(rr, httptest.NewRequest(http.MethodGet, "/api/v1/settings", http.NoBody))
			require.Equal(t, http.StatusOK, rr.Code)
			var res api.Settings
			require.NoError(t, json.NewDecoder(rr.Body).Decode(&res))
			assert.Equal(t, stremio.AccessToken(secretAfter), utils.Val(res.StremioToken),
				"settings must expose the Stremio token for the new secret")
		})
	}
}

type failingRotationDB struct {
	database.DatabaseInterface
}

func (failingRotationDB) RotateJWTSecret() error {
	return errors.New("rotation failed")
}

func TestUpdateSettingsRollsBackFailedJWTSecretRotation(t *testing.T) {
	controller, cleanup := newAuthTestController(t, func(s *api.Settings) {
		s.Auth = &api.Auth{Enabled: new(true), Type: utils.Ptr(api.Bearer), Username: new("admin"), Password: new("password")}
	})
	defer cleanup()
	controller.db = failingRotationDB{controller.db}

	secretBefore, err := controller.db.GetJWTSecret()
	require.NoError(t, err)
	token, err := auth.GenerateToken("admin", []byte(secretBefore))
	require.NoError(t, err)

	rr := testutil.NewRequest().Patch("/api/v1/settings").
		WithHeader("Authorization", "Bearer "+token).
		WithJsonBody(api.Settings{Auth: &api.Auth{Password: new("new-password")}}).
		GoWithHTTPHandler(t, controller.router).Recorder
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	assert.Equal(t, "password", utils.Val(controller.settings.Load().Auth.Password))
	stored, err := controller.db.GetSettings()
	require.NoError(t, err)
	assert.Equal(t, "password", utils.Val(stored.Auth.Password))

	secretAfter, err := controller.db.GetJWTSecret()
	require.NoError(t, err)
	assert.Equal(t, secretBefore, secretAfter)
}

// credentialsChangingDB changes the credentials while a token request reads
// the JWT secret, as a concurrent settings update would.
type credentialsChangingDB struct {
	database.DatabaseInterface
	controller *Controller
}

func (d credentialsChangingDB) GetJWTSecret() (string, error) {
	settings := *d.controller.settings.Load()
	settings.Auth = &api.Auth{Enabled: new(true), Type: utils.Ptr(api.Bearer), Username: new("admin"), Password: new("new-password")}
	d.controller.settings.Store(&settings)
	return d.DatabaseInterface.GetJWTSecret()
}

func TestController_GetToken(t *testing.T) {
	requestToken := func(t *testing.T, controller *Controller, password string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"grant_type": {"password"}, "username": {"admin"}, "password": {password}}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		controller.GetToken(rr, req)
		return rr
	}
	enableBearer := func(s *api.Settings) {
		s.Auth = &api.Auth{Enabled: new(true), Type: utils.Ptr(api.Bearer), Username: new("admin"), Password: new("password")}
	}

	t.Run("valid credentials", func(t *testing.T) {
		controller, cleanup := newAuthTestController(t, enableBearer)
		defer cleanup()

		rr := requestToken(t, controller, "password")
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var res api.TokenResponse
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&res))

		secret, err := controller.db.GetJWTSecret()
		require.NoError(t, err)
		claims, err := auth.ValidateToken(res.AccessToken, []byte(secret))
		require.NoError(t, err)
		assert.Equal(t, "admin", claims.Username)
	})

	t.Run("invalid credentials", func(t *testing.T) {
		controller, cleanup := newAuthTestController(t, enableBearer)
		defer cleanup()

		assert.Equal(t, http.StatusUnauthorized, requestToken(t, controller, "wrong").Code)
	})

	t.Run("credentials changed while signing", func(t *testing.T) {
		controller, cleanup := newAuthTestController(t, enableBearer)
		defer cleanup()
		controller.db = credentialsChangingDB{DatabaseInterface: controller.db, controller: controller}

		rr := requestToken(t, controller, "password")
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "no token may be signed for replaced credentials")
	})
}
