// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"testing"
)

func TestIsPrivateIPAddr(t *testing.T) {
	testCases := []struct {
		name    string
		ip      string
		want    bool
		wantErr bool
	}{
		{
			name:    "private IPv4",
			ip:      "192.168.1.1",
			want:    true,
			wantErr: false,
		},
		{
			name:    "public IPv4",
			ip:      "8.8.8.8",
			want:    false,
			wantErr: false,
		},
		{
			name:    "private IPv6",
			ip:      "fd00::1",
			want:    true,
			wantErr: false,
		},
		{
			name:    "public IPv6",
			ip:      "2001:4860:4860::8888",
			want:    false,
			wantErr: false,
		},
		{
			name:    "loopback IPv4",
			ip:      "127.0.0.1",
			want:    true,
			wantErr: false,
		},
		{
			name:    "loopback IPv6",
			ip:      "::1",
			want:    true,
			wantErr: false,
		},
		{
			name:    "invalid IP",
			ip:      "not an ip",
			want:    false,
			wantErr: true,
		},
		{
			name:    "empty string",
			ip:      "",
			want:    false,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IsPrivateIPAddr(tc.ip)
			if (err != nil) != tc.wantErr {
				t.Errorf("IsPrivateIPAddr() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if got != tc.want {
				t.Errorf("IsPrivateIPAddr() = %v, want %v", got, tc.want)
			}
		})
	}
}
