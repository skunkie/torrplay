// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateJWTSecret(t *testing.T) {
	secret, err := GenerateJWTSecret()
	require.NoError(t, err)
	assert.Len(t, secret, 64)
}

func TestGenerateAndValidateToken(t *testing.T) {
	secret, err := GenerateJWTSecret()
	require.NoError(t, err)
	require.NotEmpty(t, secret)

	tokenString, err := GenerateToken("testuser", []byte(secret))
	require.NoError(t, err)
	assert.NotEmpty(t, tokenString)

	claims, err := ValidateToken(tokenString, []byte(secret))
	require.NoError(t, err)
	assert.NotNil(t, claims)
	assert.Equal(t, "testuser", claims.Username)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), claims.ExpiresAt.Time, time.Second)
}

func TestValidateToken_InvalidToken(t *testing.T) {
	secret, err := GenerateJWTSecret()
	require.NoError(t, err)

	// Test with a completely invalid token string.
	_, err = ValidateToken("invalid-token", []byte(secret))
	assert.Error(t, err)

	// Test with a token signed with a different secret.
	otherSecret, err := GenerateJWTSecret()
	require.NoError(t, err)

	tokenString, err := GenerateToken("testuser", []byte(secret))
	require.NoError(t, err)

	_, err = ValidateToken(tokenString, []byte(otherSecret))
	assert.Error(t, err)

	// Test with an expired token.
	expiredToken, err := createExpiredToken("testuser", []byte(secret))
	require.NoError(t, err)

	_, err = ValidateToken(expiredToken, []byte(secret))
	assert.Error(t, err)
	assert.ErrorIs(t, err, jwt.ErrTokenExpired)
}

func createExpiredToken(username string, secret []byte) (string, error) {
	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}
