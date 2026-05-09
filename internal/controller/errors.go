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

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/utils"
)

// ErrorHandler is a custom error handler for OpenAPI request validation middleware.
// It intercepts errors from the oapi-codegen validator and formats them into consistent HTTP JSON responses.
func (c *Controller) ErrorHandler(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
	switch e := err.(type) {
	case *openapi3filter.RequestError:
		message, _ := getInnerErrorMessage(e.Err)
		api.HTTPError(w, message, opts.StatusCode)
		return
	case *openapi3filter.SecurityRequirementsError:
		var authErr *api.AuthError
		if errors.As(err, &authErr) {
			realm := "TorrPlay"
			authHeader := fmt.Sprintf(`%s realm="%s"`, authErr.Type, realm)
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

	var schemaErr *openapi3.SchemaError
	if errors.As(err, &schemaErr) {
		if schemaErr.Origin != nil {
			if msg, isSchema := getInnerErrorMessage(schemaErr.Origin); msg != "" {
				return msg, isSchema
			}
		}
		if schemaErr.Reason != "" {
			return schemaErr.Reason, true
		}
	}

	var multiErr openapi3.MultiError
	if errors.As(err, &multiErr) {
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
