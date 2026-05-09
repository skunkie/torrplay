// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type AuthError struct {
	Message string
	Type    AuthType
}

func (e AuthError) Error() string {
	return e.Message
}

func NewError(error string, code int) Error {
	return Error{
		Message: error,
		Code:    code,
	}
}

// Error implements the error interface for the Error type.
func (e Error) Error() string {
	return e.Message
}

func HandleError(w http.ResponseWriter, err error) {
	var e Error
	if ok := errors.As(err, &e); ok {
		HTTPError(w, e.Message, e.Code)
	} else {
		HTTPError(w, err.Error(), http.StatusInternalServerError)
	}
}

// HTTPError sends an error response in JSON format.
// It ensures valid JSON is sent even if marshaling fails, and sets appropriate headers.
//
// Parameters:
//   - w: http.ResponseWriter to write the response
//   - message: Human-readable error message for the client
//   - code: HTTP status code to send (e.g., 400, 404, 500)
func HTTPError(w http.ResponseWriter, message string, code int) {
	e := Error{
		Message: message,
		Code:    code,
	}

	// Marshal first to ensure we can send valid JSON.
	body, err := json.Marshal(e)
	if err != nil {
		// If we can't marshal JSON, send a plain text error.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "Internal Server Error: failed to encode error response")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
