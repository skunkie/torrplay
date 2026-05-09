// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"errors"

	"github.com/anacrolix/torrent/metainfo"
)

// Differ checks if a value has changed and returns a boolean.
func Differ[T comparable](oldValue, newValue *T, validators ...func(T) bool) bool {
	if newValue == nil {
		return false
	}

	oldVal := Val(oldValue)
	newVal := Val(newValue)

	if oldVal == newVal {
		return false
	}

	for _, v := range validators {
		if !v(newVal) {
			return false
		}
	}

	return true
}

func HashFromHexString(s string) (metainfo.Hash, error) {
	var h metainfo.Hash
	err := h.FromHexString(s)
	if err != nil || h.IsZero() {
		return h, errors.New("invalid hash")
	}

	return h, err
}

// Ptr returns a pointer to the given value.
// This is useful for obtaining a pointer to literals, constants, or any non-pointer value.
//
// Example:
//
//	Ptr(42)      // returns *int pointing to 42
//	Ptr("hello") // returns *string pointing to "hello"
//
// Note: The returned pointer points to a copy of the input value.
// Modifying the pointed-to value does not affect the original input.
func Ptr[T any](v T) *T {
	return &v
}

// Val dereferences a pointer and returns its underlying value.
// If the pointer is nil, it returns the zero value of type T.
// This function is the inverse of Ptr[T], converting a pointer back to its value.
//
// Example:
//
//	p := Ptr(42)     // *int pointing to 42
//	v := Val(p)      // 42
//
//	var nilPtr *int
//	v2 := Val(nilPtr) // 0 (zero value for int)
func Val[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
