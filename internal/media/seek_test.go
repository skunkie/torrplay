// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package media

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveMP4PlaybackOffset(t *testing.T) {
	file := buildIndexedMP4(t)

	offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mp4", 7.2)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(2000), offset)

	offset, ok, err = ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mp4", 4)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(1000), offset)

	offset, ok, err = ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mp4", math.MaxFloat64)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(2000), offset)
}

func TestResolveMatroskaPlaybackOffset(t *testing.T) {
	file, secondClusterOffset := buildIndexedMatroska(t)

	offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mkv", 12)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, secondClusterOffset, offset)

	offset, ok, err = ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mkv", math.MaxFloat64)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, secondClusterOffset, offset)
}

func TestResolvePlaybackOffsetUnsupportedContainer(t *testing.T) {
	offset, ok, err := ResolvePlaybackOffset(bytes.NewReader([]byte("not media data")), 14, "movie.avi", 30)

	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, offset)
}

func buildIndexedMP4(t *testing.T) []byte {
	t.Helper()
	mvhd := mp4TestBox("mvhd", append([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uint32Bytes(1000)...))
	mdhd := mp4TestBox("mdhd", append([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uint32Bytes(1000)...))
	hdlr := mp4TestBox("hdlr", append([]byte{0, 0, 0, 0, 0, 0, 0, 0}, []byte("vide")...))
	stts := mp4FullTableBox("stts", [][]byte{append(uint32Bytes(10), uint32Bytes(1000)...)}...)
	stss := mp4FullTableBox("stss", uint32Bytes(1), uint32Bytes(6))
	stsc := mp4FullTableBox("stsc", append(append(uint32Bytes(1), uint32Bytes(5)...), uint32Bytes(1)...))
	stszPayload := append([]byte{0, 0, 0, 0}, uint32Bytes(100)...)
	stszPayload = append(stszPayload, uint32Bytes(10)...)
	stsz := mp4TestBox("stsz", stszPayload)
	stco := mp4FullTableBox("stco", uint32Bytes(1000), uint32Bytes(2000))
	stbl := mp4TestBox("stbl", append(append(append(append(stts, stss...), stsc...), stsz...), stco...))
	minf := mp4TestBox("minf", stbl)
	mdia := mp4TestBox("mdia", append(append(mdhd, hdlr...), minf...))
	trak := mp4TestBox("trak", mdia)
	moov := mp4TestBox("moov", append(mvhd, trak...))
	ftyp := mp4TestBox("ftyp", []byte("isom0000"))
	file := make([]byte, 0, len(ftyp)+len(moov))
	file = append(file, ftyp...)
	file = append(file, moov...)
	if len(file) < 4096 {
		file = append(file, make([]byte, 4096-len(file))...)
	}
	return file
}

func mp4FullTableBox(typ string, entries ...[]byte) []byte {
	payload := []byte{0, 0, 0, 0}
	payload = append(payload, uint32Bytes(uint32(len(entries)))...)
	for _, entry := range entries {
		payload = append(payload, entry...)
	}
	return mp4TestBox(typ, payload)
}

func mp4TestBox(typ string, payload []byte) []byte {
	box := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(8+len(payload)))
	copy(box[4:8], typ)
	return append(box, payload...)
}

func uint32Bytes(value uint32) []byte {
	buffer := make([]byte, 4)
	binary.BigEndian.PutUint32(buffer, value)
	return buffer
}

func buildIndexedMatroska(t *testing.T) ([]byte, int64) {
	t.Helper()
	info := matroskaTestElement([]byte{0x15, 0x49, 0xA9, 0x66},
		matroskaTestElement([]byte{0x2A, 0xD7, 0xB1}, uintBytes(1_000_000)))
	trackEntry := matroskaTestElement([]byte{0xAE}, append(
		matroskaTestElement([]byte{0xD7}, uintBytes(1)),
		matroskaTestElement([]byte{0x83}, uintBytes(1))...,
	))
	tracks := matroskaTestElement([]byte{0x16, 0x54, 0xAE, 0x6B}, trackEntry)
	clusterOne := matroskaTestElement([]byte{0x1F, 0x43, 0xB6, 0x75}, []byte{0})
	clusterTwo := matroskaTestElement([]byte{0x1F, 0x43, 0xB6, 0x75}, []byte{0})
	clusterOnePosition := uint64(len(info) + len(tracks))
	clusterTwoPosition := clusterOnePosition + uint64(len(clusterOne))
	cues := matroskaTestElement([]byte{0x1C, 0x53, 0xBB, 0x6B}, append(
		matroskaCuePoint(0, clusterOnePosition),
		matroskaCuePoint(10_000, clusterTwoPosition)...,
	))
	segmentPayload := append(append(append(append(info, tracks...), clusterOne...), clusterTwo...), cues...)
	segment := matroskaTestElement([]byte{0x18, 0x53, 0x80, 0x67}, segmentPayload)
	ebml := matroskaTestElement([]byte{0x1A, 0x45, 0xDF, 0xA3}, nil)
	file := make([]byte, 0, len(ebml)+len(segment))
	file = append(file, ebml...)
	file = append(file, segment...)
	segmentElement, found, err := findMatroskaTopLevel(bytes.NewReader(file), 0, int64(len(file)), matroskaSegmentID)
	require.NoError(t, err)
	require.True(t, found)
	return file, segmentElement.dataStart + int64(clusterTwoPosition)
}

func matroskaCuePoint(timestamp, clusterPosition uint64) []byte {
	positions := matroskaTestElement([]byte{0xB7}, append(
		matroskaTestElement([]byte{0xF7}, uintBytes(1)),
		matroskaTestElement([]byte{0xF1}, uintBytes(clusterPosition))...,
	))
	payload := append(matroskaTestElement([]byte{0xB3}, uintBytes(timestamp)), positions...)
	return matroskaTestElement([]byte{0xBB}, payload)
}

func matroskaTestElement(id, payload []byte) []byte {
	element := append([]byte{}, id...)
	element = append(element, matroskaSize(uint64(len(payload)))...)
	return append(element, payload...)
}

func matroskaSize(value uint64) []byte {
	if value < 0x7f {
		return []byte{0x80 | byte(value)}
	}
	return []byte{0x40 | byte(value>>8), byte(value)}
}

func uintBytes(value uint64) []byte {
	buffer := make([]byte, 8)
	binary.BigEndian.PutUint64(buffer, value)
	first := 0
	for first < len(buffer)-1 && buffer[first] == 0 {
		first++
	}
	return buffer[first:]
}
