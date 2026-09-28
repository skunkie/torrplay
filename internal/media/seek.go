// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// Package media resolves playback positions of media files to byte offsets
// through their containers' own seek indexes.
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

	// The extension picks the likely container, and the header identifies the
	// real one when that container's parser finds nothing, as in a Matroska
	// file named .mp4.
	tried := containerByExtension(filePath)
	if tried != unknownContainer {
		offset, ok, err := tried.resolve(reader, size, positionSeconds)
		if ok || (err != nil && !errors.Is(err, errInvalidContainer)) {
			return offset, ok, err
		}
	}

	header := make([]byte, 12)
	if _, err := reader.ReadAt(header, 0); err != nil && !errors.Is(err, io.EOF) {
		return 0, false, err
	}
	sniffed := containerByHeader(header)
	if sniffed == unknownContainer || sniffed == tried {
		return 0, false, nil
	}
	return sniffed.resolve(reader, size, positionSeconds)
}

// container is a media container format with a seek index the package reads.
type container int

const (
	unknownContainer container = iota
	matroskaContainer
	mp4Container
)

// containerByExtension returns the container a file name's extension names.
func containerByExtension(filePath string) container {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".m4v", ".mov", ".mp4":
		return mp4Container
	case ".mkv", ".webm":
		return matroskaContainer
	default:
		return unknownContainer
	}
}

// containerByHeader returns the container a file's first 12 bytes identify.
func containerByHeader(header []byte) container {
	switch {
	case binary.BigEndian.Uint32(header[:4]) == matroskaEBMLID:
		return matroskaContainer
	case string(header[4:8]) == "ftyp":
		return mp4Container
	default:
		return unknownContainer
	}
}

// resolve resolves a playback position through the container's seek index.
func (c container) resolve(reader io.ReaderAt, size int64, positionSeconds float64) (int64, bool, error) {
	switch c {
	case matroskaContainer:
		return resolveMatroskaOffset(reader, size, positionSeconds)
	case mp4Container:
		return resolveMP4Offset(reader, size, positionSeconds)
	default:
		return 0, false, nil
	}
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
