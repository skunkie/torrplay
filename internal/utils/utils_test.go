// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"testing"
)

func TestDiffer(t *testing.T) {
	isPositive := func(n int) bool {
		return n > 0
	}

	isNegative := func(n int) bool {
		return n < 0
	}

	testCases := []struct {
		name       string
		oldValue   *int
		newValue   *int
		validators []func(int) bool
		want       bool
	}{
		{
			name:     "new value is nil",
			oldValue: Ptr(1),
			newValue: nil,
			want:     false,
		},
		{
			name:     "old and new values are same",
			oldValue: Ptr(10),
			newValue: Ptr(10),
			want:     false,
		},
		{
			name:     "old is nil, new has value",
			oldValue: nil,
			newValue: Ptr(10),
			want:     true,
		},
		{
			name:     "old and new values are different",
			oldValue: Ptr(5),
			newValue: Ptr(10),
			want:     true,
		},
		{
			name:       "value changed, validator passes",
			oldValue:   Ptr(5),
			newValue:   Ptr(10),
			validators: []func(int) bool{isPositive},
			want:       true,
		},
		{
			name:       "value changed, one validator fails",
			oldValue:   Ptr(5),
			newValue:   Ptr(10),
			validators: []func(int) bool{isPositive, isNegative},
			want:       false,
		},
		{
			name:       "value changed, validator fails",
			oldValue:   Ptr(5),
			newValue:   Ptr(-10),
			validators: []func(int) bool{isPositive},
			want:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := Differ(tc.oldValue, tc.newValue, tc.validators...)
			if got != tc.want {
				t.Errorf("Differ() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPtr(t *testing.T) {
	t.Run("int", func(t *testing.T) {
		v := 42
		p := Ptr(v)
		if p == nil {
			t.Fatal("Ptr() returned nil")
		}
		if *p != v {
			t.Errorf("Ptr() got = %v, want %v", *p, v)
		}
	})

	t.Run("string", func(t *testing.T) {
		v := "hello"
		p := Ptr(v)
		if p == nil {
			t.Fatal("Ptr() returned nil")
		}
		if *p != v {
			t.Errorf("Ptr() got = %v, want %v", *p, v)
		}
	})
}

func TestVal(t *testing.T) {
	t.Run("non-nil pointer", func(t *testing.T) {
		v := 42
		p := &v
		got := Val(p)
		if got != v {
			t.Errorf("Val() = %v, want %v", got, v)
		}
	})

	t.Run("nil pointer", func(t *testing.T) {
		var p *int
		got := Val(p)
		var want int // zero value
		if got != want {
			t.Errorf("Val() = %v, want %v", got, want)
		}
	})

	t.Run("nil string pointer", func(t *testing.T) {
		var p *string
		got := Val(p)
		var want string // zero value
		if got != want {
			t.Errorf("Val() = %v, want %v", got, want)
		}
	})
}
