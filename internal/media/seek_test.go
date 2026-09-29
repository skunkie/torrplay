// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePlaybackOffset(t *testing.T) {
	t.Run("mp4", func(t *testing.T) {
		file := buildIndexedMP4(t, nil)
		for _, tc := range []struct {
			position float64
			want     int64
		}{
			{position: 4, want: 1000},
			{position: 7.2, want: 2000},
			{position: math.MaxFloat64, want: 2000},
		} {
			offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mp4", tc.position)
			require.NoError(t, err)
			require.True(t, ok, "position %v", tc.position)
			assert.Equal(t, tc.want, offset, "position %v", tc.position)
		}
	})

	t.Run("mp4 edit list", func(t *testing.T) {
		// Edit durations are in the movie timescale of 1000 and media times
		// in the media timescale of 90000: one second empty, two seconds
		// from media time 4 s, one second empty, and one second from media
		// time 1 s.
		edits := []struct {
			duration  uint64
			mediaTime int64
		}{{1000, -1}, {2000, 4 * 90000}, {1000, -1}, {1000, 90000}}
		for _, version := range []byte{0, 1} {
			t.Run(fmt.Sprintf("version %d", version), func(t *testing.T) {
				payload := slices.Concat([]byte{version, 0, 0, 0}, uint32Bytes(uint32(len(edits))))
				for _, e := range edits {
					if version == 1 {
						payload = slices.Concat(payload, uint64Bytes(e.duration), uint64Bytes(uint64(e.mediaTime)), make([]byte, 4))
					} else {
						payload = slices.Concat(payload, uint32Bytes(uint32(e.duration)), uint32Bytes(uint32(e.mediaTime)), make([]byte, 4))
					}
				}
				file := buildIndexedMP4(t, mp4TestBox("edts", mp4TestBox("elst", payload)))
				for _, position := range []struct {
					seconds float64
					want    int64
				}{
					// The first empty edit shows the media's start.
					{seconds: 0.5, want: 1000},
					// 1.5 s into the second edit is media time 5.5 s, at sync
					// sample 6.
					{seconds: 2.5, want: 2000},
					// The empty edit after it shows where it ended, media
					// time 6 s.
					{seconds: 3.5, want: 2000},
					// Past the edits, the last edit continues to media time
					// 3 s, before sync sample 6.
					{seconds: 6, want: 1000},
				} {
					offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mp4", position.seconds)
					require.NoError(t, err)
					require.True(t, ok, "position %v", position.seconds)
					assert.Equal(t, position.want, offset, "position %v", position.seconds)
				}
			})
		}
	})

	t.Run("matroska", func(t *testing.T) {
		file, secondClusterOffset, _ := buildIndexedMatroska(t, 1)
		for _, position := range []float64{12, math.MaxFloat64} {
			offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mkv", position)
			require.NoError(t, err)
			require.True(t, ok, "position %v", position)
			assert.Equal(t, secondClusterOffset, offset, "position %v", position)
		}
	})

	t.Run("matroska cluster at the position is not read", func(t *testing.T) {
		file, secondClusterOffset, _ := buildIndexedMatroska(t, 1)
		reader := &offsetsReader{ReaderAt: bytes.NewReader(file)}

		_, ok, err := ResolvePlaybackOffset(reader, int64(len(file)), "movie.mkv", 12)
		require.NoError(t, err)
		require.True(t, ok)
		assert.NotContains(t, reader.offsets, secondClusterOffset, "resolution must not wait for the cluster's data")
	})

	t.Run("matroska cues past clusters without a seekhead", func(t *testing.T) {
		file, _, firstClusterOffset := buildIndexedMatroska(t, 0)
		reader := &furthestReader{ReaderAt: bytes.NewReader(file)}

		_, ok, err := ResolvePlaybackOffset(reader, int64(len(file)), "movie.mkv", 12)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.LessOrEqual(t, reader.furthest, firstClusterOffset+16, "the clusters must not be walked")
	})

	t.Run("matroska cues listed by a second seekhead", func(t *testing.T) {
		file, secondClusterOffset, _ := buildIndexedMatroska(t, 2)

		offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(file), int64(len(file)), "movie.mkv", 12)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, secondClusterOffset, offset)
	})

	t.Run("container mislabeled by its extension", func(t *testing.T) {
		mkv, secondClusterOffset, _ := buildIndexedMatroska(t, 1)
		offset, ok, err := ResolvePlaybackOffset(bytes.NewReader(mkv), int64(len(mkv)), "movie.mp4", 12)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, secondClusterOffset, offset)

		mp4 := buildIndexedMP4(t, nil)
		offset, ok, err = ResolvePlaybackOffset(bytes.NewReader(mp4), int64(len(mp4)), "movie.mkv", 4)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, int64(1000), offset)
	})

	t.Run("fragmented mp4", func(t *testing.T) {
		mvhd := mp4TestBox("mvhd", append(make([]byte, 12), uint32Bytes(1000)...))
		file := slices.Concat(mp4TestBox("ftyp", []byte("isom0000")), mp4TestBox("moov", mvhd))
		firstFragment := int64(len(file))
		for range 16 {
			file = slices.Concat(file, mp4TestBox("moof", nil), mp4TestBox("mdat", make([]byte, 1024)))
		}
		reader := &furthestReader{ReaderAt: bytes.NewReader(file)}

		_, ok, err := ResolvePlaybackOffset(reader, int64(len(file)), "movie.mp4", 4)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.LessOrEqual(t, reader.furthest, firstFragment+16, "the fragments must not be walked")
	})

	t.Run("mp4 table too large", func(t *testing.T) {
		// An stsz box that really holds 8M sample sizes is refused before its
		// table is allocated or read.
		const count = 8 << 20
		header := slices.Concat(uint32Bytes(0), uint32Bytes(0), uint32Bytes(count))
		reader := &furthestReader{ReaderAt: zeroPaddedReader(header)}
		box := mp4Box{dataStart: 0, dataEnd: int64(len(header)) + count*4, typ: "stsz"}

		_, _, err := readMP4SampleSizes(reader, box)
		assert.ErrorIs(t, err, errInvalidContainer)
		assert.Equal(t, int64(len(header)), reader.furthest, "the table must not be read")
	})

	t.Run("unsupported container", func(t *testing.T) {
		offset, ok, err := ResolvePlaybackOffset(bytes.NewReader([]byte("not media data")), 14, "movie.avi", 30)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, offset)
	})
}

// zeroPaddedReader reads prefix followed by zeros without end.
type zeroPaddedReader []byte

func (r zeroPaddedReader) ReadAt(b []byte, off int64) (int, error) {
	clear(b)
	if off < int64(len(r)) {
		copy(b, r[off:])
	}
	return len(b), nil
}

// offsetsReader records the offset of each read through it.
type offsetsReader struct {
	io.ReaderAt
	offsets []int64
}

func (r *offsetsReader) ReadAt(b []byte, off int64) (int, error) {
	r.offsets = append(r.offsets, off)
	return r.ReaderAt.ReadAt(b, off)
}

// furthestReader records the furthest offset read through it.
type furthestReader struct {
	io.ReaderAt
	furthest int64
}

func (r *furthestReader) ReadAt(b []byte, off int64) (int, error) {
	r.furthest = max(r.furthest, off+int64(len(b)))
	return r.ReaderAt.ReadAt(b, off)
}

// buildIndexedMP4 returns an MP4 file with a movie timescale of 1000 and a
// video track, in a media timescale of 90000, of ten one-second samples, sync
// samples 1 and 6, and five samples each in chunks at offsets 1000 and 2000.
// A non-nil edts is the track's edit box.
func buildIndexedMP4(t *testing.T, edts []byte) []byte {
	t.Helper()
	mvhd := mp4TestBox("mvhd", append([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uint32Bytes(1000)...))
	mdhd := mp4TestBox("mdhd", append([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uint32Bytes(90000)...))
	hdlr := mp4TestBox("hdlr", append([]byte{0, 0, 0, 0, 0, 0, 0, 0}, []byte("vide")...))
	stts := mp4FullTableBox("stts", [][]byte{append(uint32Bytes(10), uint32Bytes(90000)...)}...)
	stss := mp4FullTableBox("stss", uint32Bytes(1), uint32Bytes(6))
	stsc := mp4FullTableBox("stsc", append(append(uint32Bytes(1), uint32Bytes(5)...), uint32Bytes(1)...))
	stszPayload := append([]byte{0, 0, 0, 0}, uint32Bytes(100)...)
	stszPayload = append(stszPayload, uint32Bytes(10)...)
	stsz := mp4TestBox("stsz", stszPayload)
	stco := mp4FullTableBox("stco", uint32Bytes(1000), uint32Bytes(2000))
	stbl := mp4TestBox("stbl", append(append(append(append(stts, stss...), stsc...), stsz...), stco...))
	minf := mp4TestBox("minf", stbl)
	mdia := mp4TestBox("mdia", append(append(mdhd, hdlr...), minf...))
	trak := mp4TestBox("trak", append(slices.Clone(edts), mdia...))
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

func uint64Bytes(value uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, value)
}

func uint32Bytes(value uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, value)
}

// buildIndexedMatroska returns a Matroska file with two clusters followed by
// Cues, and the file offsets of its second and first clusters. With one
// SeekHead, a SeekHead before the clusters locates the Cues; with two, it
// locates a second SeekHead after the clusters, which locates the Cues.
func buildIndexedMatroska(t *testing.T, seekHeads int) (file []byte, secondCluster, firstCluster int64) {
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
	// The SeekPosition is fixed-width, so a SeekHead's size does not depend
	// on the position it holds.
	seekHeadFor := func(id []byte, position uint64) []byte {
		fixed := make([]byte, 8)
		binary.BigEndian.PutUint64(fixed, position)
		seek := matroskaTestElement([]byte{0x4D, 0xBB}, append(
			matroskaTestElement([]byte{0x53, 0xAB}, id),
			matroskaTestElement([]byte{0x53, 0xAC}, fixed)...,
		))
		return matroskaTestElement([]byte{0x11, 0x4D, 0x9B, 0x74}, seek)
	}
	cuesID := []byte{0x1C, 0x53, 0xBB, 0x6B}
	seekHeadID := []byte{0x11, 0x4D, 0x9B, 0x74}
	var firstSeekHeadLength, secondSeekHeadLength uint64
	if seekHeads > 0 {
		firstSeekHeadLength = uint64(len(seekHeadFor(cuesID, 0)))
	}
	if seekHeads > 1 {
		secondSeekHeadLength = firstSeekHeadLength
	}
	clusterOnePosition := firstSeekHeadLength + uint64(len(info)+len(tracks))
	clusterTwoPosition := clusterOnePosition + uint64(len(clusterOne))
	secondSeekHeadPosition := clusterTwoPosition + uint64(len(clusterTwo))
	cuesPosition := secondSeekHeadPosition + secondSeekHeadLength
	cues := matroskaTestElement(cuesID, append(
		matroskaCuePoint(0, clusterOnePosition),
		matroskaCuePoint(10_000, clusterTwoPosition)...,
	))
	var firstSeekHead, secondSeekHead []byte
	switch seekHeads {
	case 1:
		firstSeekHead = seekHeadFor(cuesID, cuesPosition)
	case 2:
		firstSeekHead = seekHeadFor(seekHeadID, secondSeekHeadPosition)
		secondSeekHead = seekHeadFor(cuesID, cuesPosition)
	}
	var segmentPayload []byte
	for _, element := range [][]byte{firstSeekHead, info, tracks, clusterOne, clusterTwo, secondSeekHead, cues} {
		segmentPayload = append(segmentPayload, element...)
	}
	segment := matroskaTestElement([]byte{0x18, 0x53, 0x80, 0x67}, segmentPayload)
	ebml := matroskaTestElement([]byte{0x1A, 0x45, 0xDF, 0xA3}, nil)
	file = make([]byte, 0, len(ebml)+len(segment))
	file = append(file, ebml...)
	file = append(file, segment...)
	segmentElement, found, err := findMatroskaTopLevel(bytes.NewReader(file), 0, int64(len(file)), matroskaSegmentID)
	require.NoError(t, err)
	require.True(t, found)
	return file, segmentElement.dataStart + int64(clusterTwoPosition), segmentElement.dataStart + int64(clusterOnePosition)
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
