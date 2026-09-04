// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

// ErrorHandler is a custom error handler for OpenAPI request validation middleware.
// It intercepts errors from the oapi-codegen validator and formats them into consistent HTTP JSON responses.
func (c *Controller) ErrorHandler(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
	var reqErr *openapi3filter.RequestError
	var secErr *openapi3filter.SecurityRequirementsError
	switch {
	case errors.As(err, &reqErr):
		if schemaError, ok := errors.AsType[*openapi3.SchemaError](reqErr.Err); ok {
			if ext, ok := schemaError.Schema.Extensions["x-torrplay-validation-key"]; ok {
				if ext == "torrent_trackers" {
					api.HTTPError(w, "invalid torrent tracker format", http.StatusBadRequest)
					return
				}
			}
		}
		message, _ := getInnerErrorMessage(reqErr.Err)
		api.HTTPError(w, message, opts.StatusCode)
		return
	case errors.As(err, &secErr):
		if authErr, ok := errors.AsType[*api.AuthError](err); ok {
			realm := "TorrPlay"
			authType := string(authErr.Type)
			if strings.EqualFold(authType, "Basic") {
				if r != nil && strings.EqualFold(r.Header.Get("X-Requested-With"), "XMLHttpRequest") {
					authType = "x-Basic"
				} else {
					authType = "Basic"
				}
			} else if strings.EqualFold(authType, "Bearer") {
				authType = "Bearer"
			}
			authHeader := fmt.Sprintf(`%s realm=%q`, authType, realm)
			w.Header().Set("WWW-Authenticate", authHeader)
		}
		if utils.Val(c.settings.LogLevel) == slog.LevelDebug {
			api.HTTPError(w, err.Error(), http.StatusUnauthorized)
			return
		}
		api.HTTPError(w, "authentication failed", http.StatusUnauthorized)
	default:
		api.HTTPError(w, "invalid request", opts.StatusCode)
	}
}

// getInnerErrorMessage recursively unwraps an error to find the most specific
// and user-friendly message. It prioritizes schema validation errors over others.
func getInnerErrorMessage(err error) (string, bool) {
	if err == nil {
		return "", false
	}

	if schemaErr, ok := errors.AsType[*openapi3.SchemaError](err); ok {
		if schemaErr.Origin != nil {
			if msg, isSchema := getInnerErrorMessage(schemaErr.Origin); msg != "" {
				return msg, isSchema
			}
		}
		if schemaErr.Reason != "" {
			return schemaErr.Reason, true
		}
	}

	if multiErr, ok := errors.AsType[openapi3.MultiError](err); ok {
		var firstMessage string
		for _, me := range multiErr {
			msg, isSchema := getInnerErrorMessage(me)
			if msg != "" {
				if isSchema {
					return msg, true
				}
				if firstMessage == "" {
					firstMessage = msg
				}
			}
		}
		if firstMessage != "" {
			return firstMessage, false
		}
	}

	// If we can't unpack a specific type, return the top-level error message.
	return err.Error(), false
}
