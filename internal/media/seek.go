// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package media

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"path/filepath"
	"strings"
)

var errInvalidContainer = errors.New("invalid media container")

// ResolvePlaybackOffset resolves a presentation timestamp to a byte offset by
// reading the media container's own seek index. Unsupported or unindexed files
// return ok=false so callers can retain their ordinary beginning preload.
func ResolvePlaybackOffset(reader io.ReaderAt, size int64, filePath string, positionSeconds float64) (offset int64, ok bool, err error) {
	if reader == nil || size <= 0 || positionSeconds <= 0 || math.IsNaN(positionSeconds) || math.IsInf(positionSeconds, 0) {
		return 0, false, nil
	}

	extension := strings.ToLower(filepath.Ext(filePath))
	switch extension {
	case ".m4v", ".mov", ".mp4":
		return resolveMP4Offset(reader, size, positionSeconds)
	case ".mkv", ".webm":
		return resolveMatroskaOffset(reader, size, positionSeconds)
	}

	header := make([]byte, 12)
	if _, err := reader.ReadAt(header, 0); err != nil && !errors.Is(err, io.EOF) {
		return 0, false, err
	}
	if binary.BigEndian.Uint32(header[:4]) == matroskaEBMLID {
		return resolveMatroskaOffset(reader, size, positionSeconds)
	}
	if string(header[4:8]) == "ftyp" {
		return resolveMP4Offset(reader, size, positionSeconds)
	}

	return 0, false, nil
}

func scaleSeconds(positionSeconds, unitsPerSecond float64) uint64 {
	scaled := positionSeconds * unitsPerSecond
	if math.IsInf(scaled, 1) || scaled >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	return uint64(scaled)
}

func readAtFull(reader io.ReaderAt, buffer []byte, offset int64) error {
	read := 0
	for read < len(buffer) {
		n, err := reader.ReadAt(buffer[read:], offset+int64(read))
		read += n
		if err != nil {
			if errors.Is(err, io.EOF) && read == len(buffer) {
				return nil
			}
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func checkedEnd(start, size, limit int64) (int64, bool) {
	if start < 0 || size < 0 || start > limit || size > limit-start {
		return 0, false
	}
	return start + size, true
}
