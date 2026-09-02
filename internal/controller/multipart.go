// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import "net/http"

func parseMultipartForm(w http.ResponseWriter, r *http.Request) error {
	return parseMultipartFormWithLimit(w, r, multipartFormMaxBody)
}

func parseMultipartFormWithLimit(w http.ResponseWriter, r *http.Request, maxBody int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	return r.ParseMultipartForm(multipartFormMaxMemory) //nolint:gosec // The request body is capped by MaxBytesReader above.
}
