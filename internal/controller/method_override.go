// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"net/http"
	"strings"
)

// methodOverrideHeader is the canonical Go MIME header key for the
// X-HTTP-Method-Override convention. It lets a client that can only issue
// GET/POST requests (e.g. a WebView bridge with no native PUT/PATCH/DELETE
// support) POST with this header set to the verb it actually means, so
// routing still dispatches to the correct handler.
const methodOverrideHeader = "X-Http-Method-Override"

var overridableMethods = map[string]bool{
	http.MethodDelete: true,
	http.MethodPatch:  true,
	http.MethodPut:    true,
}

// methodOverrideMiddleware rewrites a POST request's method when it carries
// an X-Http-Method-Override header naming a supported verb, before the
// router dispatches on r.Method.
func methodOverrideMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			override := strings.ToUpper(strings.TrimSpace(r.Header.Get(methodOverrideHeader)))
			if overridableMethods[override] {
				r.Method = override
			}
		}
		next.ServeHTTP(w, r)
	})
}
