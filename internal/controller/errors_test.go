// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"errors"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestGetInnerErrorMessage(t *testing.T) {
	t.Run("unwraps schema error", func(t *testing.T) {
		err := &openapi3.SchemaError{
			Reason: "field is required",
		}
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "field is required", msg)
		require.True(t, isSchemaErr)
	})

	t.Run("unwraps nested schema error", func(t *testing.T) {
		err := &openapi3.SchemaError{
			SchemaField: "allOf",
			Origin: &openapi3.SchemaError{
				Reason: "nested error",
			},
		}
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "nested error", msg)
		require.True(t, isSchemaErr)
	})

	t.Run("unwraps schema error with multi-error inside", func(t *testing.T) {
		err := &openapi3.SchemaError{
			SchemaField: "allOf",
			Origin: openapi3.MultiError{
				errors.New("generic inner error"),
				&openapi3.SchemaError{Reason: "specific inner error"},
			},
		}
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "specific inner error", msg)
		require.True(t, isSchemaErr)
	})

	t.Run("unwraps schema error with generic multi-error inside", func(t *testing.T) {
		err := &openapi3.SchemaError{
			SchemaField: "allOf",
			Origin: openapi3.MultiError{
				errors.New("first generic inner error"),
				errors.New("second generic inner error"),
			},
		}
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "first generic inner error", msg)
		require.False(t, isSchemaErr)
	})

	t.Run("unwraps multi error", func(t *testing.T) {
		err := openapi3.MultiError{
			errors.New("some other error"),
			&openapi3.SchemaError{
				Reason: "the actual error",
			},
		}
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "the actual error", msg)
		require.True(t, isSchemaErr)
	})

	t.Run("unwraps multi error without schema error", func(t *testing.T) {
		err := openapi3.MultiError{
			errors.New("first error"),
			errors.New("second error"),
		}
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "first error", msg)
		require.False(t, isSchemaErr)
	})

	t.Run("returns original error message if no inner error", func(t *testing.T) {
		err := errors.New("simple error")
		msg, isSchemaErr := getInnerErrorMessage(err)
		require.Equal(t, "simple error", msg)
		require.False(t, isSchemaErr)
	})
}
