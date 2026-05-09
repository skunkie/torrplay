// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package piececompletion

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "piececompletion-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test successful creation.
	pc, err := New(tempDir, logger)
	require.NoError(t, err)
	require.NotNil(t, pc)
	assert.FileExists(t, filepath.Join(tempDir, ".torrent.db"))

	err = pc.Close()
	require.NoError(t, err)

	// Test error on invalid directory.
	_, err = New("/nonexistent/path", logger)
	assert.Error(t, err)
}

func TestPieceCompletion_GetAndSet(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "piececompletion-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	pc, err := New(tempDir, logger)
	require.NoError(t, err)
	defer pc.Close()

	pk := metainfo.PieceKey{
		InfoHash: metainfo.NewHashFromHex("0123456789abcdef0123456789abcdef01234567"),
		Index:    42,
	}

	// 1. Get a non-existent piece.
	comp, err := pc.Get(pk)
	require.NoError(t, err)
	assert.Equal(t, storage.Completion{Complete: false, Ok: true}, comp)

	// 2. Set the piece as complete.
	err = pc.Set(pk, true)
	require.NoError(t, err)

	// 3. Get the completed piece.
	comp, err = pc.Get(pk)
	require.NoError(t, err)
	assert.Equal(t, storage.Completion{Complete: true, Ok: true}, comp)

	// 4. Set the piece as not complete.
	err = pc.Set(pk, false)
	require.NoError(t, err)

	// 5. Get the not-completed piece.
	comp, err = pc.Get(pk)
	require.NoError(t, err)
	assert.Equal(t, storage.Completion{Complete: false, Ok: true}, comp)

	// 6. Test idempotency of Set(true).
	err = pc.Set(pk, true)
	require.NoError(t, err)
	err = pc.Set(pk, true)
	require.NoError(t, err)
	comp, err = pc.Get(pk)
	require.NoError(t, err)
	assert.Equal(t, storage.Completion{Complete: true, Ok: true}, comp)

	// 7. Test idempotency of Set(false).
	err = pc.Set(pk, false)
	require.NoError(t, err)
	err = pc.Set(pk, false)
	require.NoError(t, err)
	comp, err = pc.Get(pk)
	require.NoError(t, err)
	assert.Equal(t, storage.Completion{Complete: false, Ok: true}, comp)
}
