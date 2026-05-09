// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package web

import (
	"embed"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// ServeStatic returns a http.HandlerFunc that serves static files from the embedded staticFS.
func ServeStatic() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		r2 := r.Clone(r.Context())
		r2.URL.Path = "static" + r2.URL.Path

		http.FileServerFS(staticFS).ServeHTTP(w, r2)
	}
}
