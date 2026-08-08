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
// Each rewrite function is called in order with the request (whose URL.Path has already been
// prefixed with "static/"). Use it to map directory-style URLs to their .html files,
// e.g. "static/demo" → "static/demo.html".
func ServeStatic(rewrites ...func(r *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "static" + r2.URL.Path

		for _, rewrite := range rewrites {
			if rewrite != nil {
				rewrite(r2)
			}
		}

		http.FileServerFS(staticFS).ServeHTTP(w, r2)
	}
}
