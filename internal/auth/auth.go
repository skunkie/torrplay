// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenExpiry         = 24 * time.Hour
	PlaybackTokenExpiry = 24 * time.Hour
	PlaybackTokenScope  = "playback"
)

var ErrInvalidToken = errors.New("invalid token")

// Claims defines the structure of the JWT claims.
type Claims struct {
	Username string `json:"username,omitempty"`
	Scope    string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

// GenerateToken generates a new JWT token for a given username.
func GenerateToken(username string, secret []byte) (string, error) {
	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(TokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// GeneratePlaybackToken generates a short-lived token restricted to media playback routes.
func GeneratePlaybackToken(secret []byte) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(PlaybackTokenExpiry)
	claims := &Claims{
		Scope: PlaybackTokenScope,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	return signed, expiresAt, err
}

// ValidateToken validates a JWT token and returns the claims if the token is valid.
func ValidateToken(tokenString string, secret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

// GenerateJWTSecret generates a new, cryptographically secure JWT secret.
func GenerateJWTSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}
