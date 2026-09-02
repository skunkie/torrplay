// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stremio

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

const hmacDomain = "torrplay:stremio-access:v1"

// AccessToken derives a stable token scoped to Stremio access from the server secret.
func AccessToken(secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(hmacDomain))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ValidateAccessToken compares a presented Stremio token in constant time.
func ValidateAccessToken(token, secret string) bool {
	if token == "" || secret == "" {
		return false
	}
	return hmac.Equal([]byte(token), []byte(AccessToken(secret)))
}
