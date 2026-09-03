// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/cors"
	"github.com/torrplay/torrplay/internal/utils"
)

func corsOptions() cors.Options {
	return cors.Options{
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "SUBSCRIBE", "UNSUBSCRIBE", "NOTIFY"},
		AllowedHeaders: []string{
			"Accept", "Accept-Ranges", "Accept-Language", "Access-Control-Request-Private-Network",
			"Authorization", "Content-Language", "Content-Type", "Content-Length", "Origin", "Range",
		},
		ExposedHeaders: []string{"Content-Range"},
		MaxAge:         600,
	}
}

func normalizeOrigin(origin string) (string, bool) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func (c *Controller) isTrustedOrigin(origin string) bool {
	normalized, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	u, _ := url.Parse(normalized)
	hostname := u.Hostname()
	switch u.Scheme {
	case "http", "https", "tauri", "capacitor":
		if strings.EqualFold(hostname, "localhost") || strings.HasSuffix(strings.ToLower(hostname), ".localhost") {
			return true
		}
		if ip := net.ParseIP(hostname); ip != nil && ip.IsLoopback() {
			return true
		}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, allowed := range utils.Val(c.settings.CorsAllowedOrigins) {
		if configured, valid := normalizeOrigin(allowed); valid && configured == normalized {
			return true
		}
	}
	return false
}

func privateNetworkAccess(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Add("Vary", "Access-Control-Request-Private-Network")
		}
		handler.ServeHTTP(w, r)
	})
}

func (c *Controller) corsMiddleware(next http.Handler) http.Handler {
	stremioOptions := corsOptions()
	stremioOptions.AllowedOrigins = []string{"*"}
	stremioOptions.AllowedMethods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	stremioHandler := privateNetworkAccess(cors.New(stremioOptions).Handler(next))

	applicationOptions := corsOptions()
	applicationOptions.AllowOriginFunc = func(_ *http.Request, origin string) bool {
		return c.isTrustedOrigin(origin)
	}
	applicationHandler := privateNetworkAccess(cors.New(applicationOptions).Handler(next))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stremio" || strings.HasPrefix(r.URL.Path, "/stremio/") {
			stremioHandler.ServeHTTP(w, r)
			return
		}
		applicationHandler.ServeHTTP(w, r)
	})
}
