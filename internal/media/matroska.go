// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"math"
)

const (
	matroskaEBMLID               = 0x1A45DFA3
	matroskaSegmentID            = 0x18538067
	matroskaSeekHeadID           = 0x114D9B74
	matroskaSeekEntryID          = 0x4DBB
	matroskaSeekIdentifierID     = 0x53AB
	matroskaSeekPositionID       = 0x53AC
	matroskaInfoID               = 0x1549A966
	matroskaTimestampScaleID     = 0x2AD7B1
	matroskaTracksID             = 0x1654AE6B
	matroskaTrackEntryID         = 0xAE
	matroskaTrackNumberID        = 0xD7
	matroskaTrackTypeID          = 0x83
	matroskaCuesID               = 0x1C53BB6B
	matroskaCuePointID           = 0xBB
	matroskaCueTimeID            = 0xB3
	matroskaCueTrackPositionsID  = 0xB7
	matroskaCueTrackID           = 0xF7
	matroskaCueClusterPositionID = 0xF1
	matroskaClusterID            = 0x1F43B675
)

// maxMatroskaCuesBytes bounds the Cues element read into memory. Cues take a
// few megabytes even for long films.
const maxMatroskaCuesBytes = 32 << 20

// maxMatroskaSeekHeads bounds the chain of SeekHeads followed from the first.
const maxMatroskaSeekHeads = 4

// matroskaElement is an EBML element: its ID, the file offset of its header,
// and the file range [dataStart, dataEnd) of its data. An element of unknown
// size ends at the end of its parent.
type matroskaElement struct {
	dataEnd   int64
	dataStart int64
	id        uint64
	offset    int64
}

// matroskaCue is a CuePoint's time, in timestamp-scale units, and the track
// and segment-relative position of the Cluster it points to.
type matroskaCue struct {
	clusterPosition uint64
	time            uint64
	track           uint64
}

// resolveMatroskaOffset resolves positionSeconds to the file offset of the
// Cluster whose Cue for the first video track is the last at or before it.
// It finds the segment's Info, Tracks, and Cues before its first Cluster or
// through its SeekHead, and reports false when Tracks, a video track, or Cues
// are missing. Without Info, the default timestamp scale applies.
func resolveMatroskaOffset(reader io.ReaderAt, size int64, positionSeconds float64) (int64, bool, error) {
	segment, found, err := findMatroskaElement(reader, 0, size, matroskaSegmentID)
	if err != nil || !found {
		return 0, false, err
	}

	elements, seekTargets, err := scanMatroskaSegment(reader, segment)
	if err != nil {
		return 0, false, err
	}
	lookup := func(id uint64) (matroskaElement, bool, error) {
		if element, ok := elements[id]; ok {
			return element, true, nil
		}
		position, ok := seekTargets[id]
		if !ok || position > math.MaxInt64 {
			return matroskaElement{}, false, nil
		}
		return readMatroskaElement(reader, segment.dataStart+int64(position), segment.dataEnd)
	}

	timestampScale := uint64(1_000_000)
	if info, ok, lookupErr := lookup(matroskaInfoID); lookupErr != nil {
		return 0, false, lookupErr
	} else if ok {
		if scaleElement, scaleFound, findErr := findMatroskaChild(reader, info, matroskaTimestampScaleID); findErr != nil {
			return 0, false, findErr
		} else if scaleFound {
			timestampScale, err = readMatroskaUint(reader, scaleElement)
			if err != nil || timestampScale == 0 {
				return 0, false, err
			}
		}
	}

	tracks, ok, err := lookup(matroskaTracksID)
	if err != nil || !ok {
		return 0, false, err
	}
	videoTrack, found, err := findMatroskaVideoTrack(reader, tracks)
	if err != nil || !found {
		return 0, false, err
	}

	cues, ok, err := lookup(matroskaCuesID)
	if err != nil || !ok {
		return 0, false, err
	}
	// Cue points are parsed from many small elements, so the Cues are read
	// in one piece rather than element by element through reader.
	cuesSize := cues.dataEnd - cues.dataStart
	if cuesSize > maxMatroskaCuesBytes {
		return 0, false, nil
	}
	cuesData := make([]byte, cuesSize)
	if err := readAtFull(reader, cuesData, cues.dataStart); err != nil {
		return 0, false, err
	}
	target := scaleToUnits(positionSeconds, 1_000_000_000/float64(timestampScale))
	cue, found, err := findMatroskaCue(bytes.NewReader(cuesData), matroskaElement{dataEnd: cuesSize, id: cues.id}, videoTrack, target)
	if err != nil || !found {
		return 0, false, err
	}
	if cue.clusterPosition > math.MaxInt64 {
		return 0, false, nil
	}
	// The cluster is not read to check it: it lies away from the head and
	// tail, so reading it would wait for its data to download.
	resolved := segment.dataStart + int64(cue.clusterPosition)
	if resolved < segment.dataStart || resolved >= min(size, segment.dataEnd) {
		return 0, false, nil
	}
	return resolved, true, nil
}

// scanMatroskaSegment returns the segment's top-level elements before its
// first Cluster, keyed by ID, and the segment-relative positions its
// SeekHeads list, following a SeekHead listed by another up to
// maxMatroskaSeekHeads times. It never reads past the first Cluster, so the
// file's clusters are not walked.
func scanMatroskaSegment(reader io.ReaderAt, segment matroskaElement) (map[uint64]matroskaElement, map[uint64]uint64, error) {
	elements := make(map[uint64]matroskaElement)
	seekTargets := make(map[uint64]uint64)
	err := walkMatroskaElements(reader, segment.dataStart, segment.dataEnd, func(element matroskaElement) (bool, error) {
		if _, exists := elements[element.id]; !exists {
			elements[element.id] = element
		}
		if element.id == matroskaSeekHeadID {
			targets, err := readMatroskaSeekHead(reader, element)
			if err != nil {
				return false, err
			}
			maps.Copy(seekTargets, targets)
		}
		// Media data follows the first Cluster. Elements past it are found
		// through the SeekHead, because walking the clusters would read the
		// whole file.
		return element.id != matroskaClusterID, nil
	})
	if err != nil {
		return nil, nil, err
	}
	// A SeekHead may list another SeekHead, typically placed after the
	// clusters, which lists the elements the first one does not.
	for range maxMatroskaSeekHeads {
		position, ok := seekTargets[matroskaSeekHeadID]
		if !ok {
			break
		}
		delete(seekTargets, matroskaSeekHeadID)
		if position > math.MaxInt64 {
			break
		}
		seekHead, found, err := readMatroskaElement(reader, segment.dataStart+int64(position), segment.dataEnd)
		if err != nil {
			return nil, nil, err
		}
		if !found || seekHead.id != matroskaSeekHeadID {
			break
		}
		targets, err := readMatroskaSeekHead(reader, seekHead)
		if err != nil {
			return nil, nil, err
		}
		for id, target := range targets {
			if _, exists := seekTargets[id]; !exists {
				seekTargets[id] = target
			}
		}
	}
	return elements, seekTargets, nil
}

// readMatroskaSeekHead returns the segment-relative position of each element
// a SeekHead lists, keyed by element ID.
func readMatroskaSeekHead(reader io.ReaderAt, seekHead matroskaElement) (map[uint64]uint64, error) {
	targets := make(map[uint64]uint64)
	err := walkMatroskaElements(reader, seekHead.dataStart, seekHead.dataEnd, func(entry matroskaElement) (bool, error) {
		if entry.id != matroskaSeekEntryID {
			return true, nil
		}
		identifier, hasIdentifier, err := findMatroskaChild(reader, entry, matroskaSeekIdentifierID)
		if err != nil {
			return false, err
		}
		position, hasPosition, err := findMatroskaChild(reader, entry, matroskaSeekPositionID)
		if err != nil || !hasIdentifier || !hasPosition {
			return err == nil, err
		}
		id, err := readMatroskaUint(reader, identifier)
		if err != nil {
			return false, err
		}
		targets[id], err = readMatroskaUint(reader, position)
		return err == nil, err
	})
	if err != nil {
		return nil, err
	}
	return targets, nil
}

// findMatroskaVideoTrack returns the track number of the first video track
// in Tracks, or false when there is none.
func findMatroskaVideoTrack(reader io.ReaderAt, tracks matroskaElement) (uint64, bool, error) {
	var number uint64
	found := false
	err := walkMatroskaElements(reader, tracks.dataStart, tracks.dataEnd, func(entry matroskaElement) (bool, error) {
		if entry.id != matroskaTrackEntryID {
			return true, nil
		}
		numberElement, hasNumber, err := findMatroskaChild(reader, entry, matroskaTrackNumberID)
		if err != nil {
			return false, err
		}
		typeElement, hasType, err := findMatroskaChild(reader, entry, matroskaTrackTypeID)
		if err != nil || !hasNumber || !hasType {
			return err == nil, err
		}
		trackType, err := readMatroskaUint(reader, typeElement)
		if err != nil || trackType != 1 {
			return err == nil, err
		}
		number, err = readMatroskaUint(reader, numberElement)
		found = err == nil
		return false, err
	})
	return number, found, err
}

// findMatroskaCue returns the last cue for videoTrack at or before target, in
// timestamp-scale units, or the first cue when all are after it. It reports
// false when no cue is for the track.
func findMatroskaCue(reader io.ReaderAt, cues matroskaElement, videoTrack, target uint64) (matroskaCue, bool, error) {
	var selected matroskaCue
	found := false
	err := walkMatroskaElements(reader, cues.dataStart, cues.dataEnd, func(point matroskaElement) (bool, error) {
		if point.id != matroskaCuePointID {
			return true, nil
		}
		cue, ok, err := readMatroskaCuePoint(reader, point, videoTrack)
		if err != nil || !ok {
			return err == nil, err
		}
		if !found || (cue.time <= target && cue.time >= selected.time) {
			selected, found = cue, true
		}
		// Cues are in time order, so the first cue past target ends the search.
		return cue.time <= target || selected.time > target, nil
	})
	if err != nil {
		return matroskaCue{}, false, err
	}
	return selected, found, nil
}

// readMatroskaCuePoint returns a CuePoint's time and its position for
// videoTrack, or false when the point has no position for the track.
func readMatroskaCuePoint(reader io.ReaderAt, point matroskaElement, videoTrack uint64) (matroskaCue, bool, error) {
	var cue matroskaCue
	timeElement, hasTime, err := findMatroskaChild(reader, point, matroskaCueTimeID)
	if err != nil || !hasTime {
		return cue, false, err
	}
	cue.time, err = readMatroskaUint(reader, timeElement)
	if err != nil {
		return cue, false, err
	}

	found := false
	err = walkMatroskaElements(reader, point.dataStart, point.dataEnd, func(positions matroskaElement) (bool, error) {
		if positions.id != matroskaCueTrackPositionsID {
			return true, nil
		}
		trackElement, hasTrack, err := findMatroskaChild(reader, positions, matroskaCueTrackID)
		if err != nil {
			return false, err
		}
		clusterElement, hasCluster, err := findMatroskaChild(reader, positions, matroskaCueClusterPositionID)
		if err != nil || !hasTrack || !hasCluster {
			return err == nil, err
		}
		cue.track, err = readMatroskaUint(reader, trackElement)
		if err != nil || cue.track != videoTrack {
			return err == nil, err
		}
		cue.clusterPosition, err = readMatroskaUint(reader, clusterElement)
		found = err == nil
		return false, err
	})
	return cue, found, err
}

// findMatroskaElement returns the first element with id among the elements
// in [start, end), reading only the headers before it.
func findMatroskaElement(reader io.ReaderAt, start, end int64, id uint64) (matroskaElement, bool, error) {
	var found matroskaElement
	ok := false
	err := walkMatroskaElements(reader, start, end, func(element matroskaElement) (bool, error) {
		if element.id == id {
			found, ok = element, true
		}
		return !ok, nil
	})
	return found, ok, err
}

// walkMatroskaElements calls visit with each element in [start, end) in order,
// reading only their headers, until visit returns false or an error.
func walkMatroskaElements(reader io.ReaderAt, start, end int64, visit func(matroskaElement) (bool, error)) error {
	for offset := start; offset < end; {
		element, ok, err := readMatroskaElement(reader, offset, end)
		if err != nil || !ok {
			return err
		}
		more, err := visit(element)
		if err != nil || !more {
			return err
		}
		offset = element.dataEnd
	}
	return nil
}

// findMatroskaChild returns parent's first child element with id.
func findMatroskaChild(reader io.ReaderAt, parent matroskaElement, id uint64) (matroskaElement, bool, error) {
	return findMatroskaElement(reader, parent.dataStart, parent.dataEnd, id)
}

// readMatroskaElement reads the header of the element at offset, which must
// end by limit. It reports false at or past limit and errInvalidContainer for
// a malformed header or an element that overruns limit.
func readMatroskaElement(reader io.ReaderAt, offset, limit int64) (matroskaElement, bool, error) {
	if offset < 0 || offset >= limit {
		return matroskaElement{}, false, nil
	}
	header := make([]byte, 16)
	available := min(int64(len(header)), limit-offset)
	if available < 2 {
		return matroskaElement{}, false, errInvalidContainer
	}
	if err := readAtFull(reader, header[:available], offset); err != nil && !errors.Is(err, io.EOF) {
		return matroskaElement{}, false, err
	}
	id, idLength, _, ok := readMatroskaVint(header[:available], false)
	if !ok {
		return matroskaElement{}, false, errInvalidContainer
	}
	size, sizeLength, unknown, ok := readMatroskaVint(header[idLength:available], true)
	if !ok {
		return matroskaElement{}, false, errInvalidContainer
	}
	dataStart := offset + int64(idLength+sizeLength)
	dataEnd := limit
	if !unknown {
		if size > math.MaxInt64 {
			return matroskaElement{}, false, errInvalidContainer
		}
		var valid bool
		dataEnd, valid = checkedEnd(dataStart, int64(size), limit)
		if !valid {
			return matroskaElement{}, false, errInvalidContainer
		}
	}
	return matroskaElement{dataEnd: dataEnd, dataStart: dataStart, id: id, offset: offset}, true, nil
}

// readMatroskaVint decodes the variable-length integer at the start of
// buffer: an element ID, or with isSize a data size without its length
// marker. It returns the value, its length in bytes, whether a size is the
// reserved unknown size, and false when buffer holds no valid integer.
func readMatroskaVint(buffer []byte, isSize bool) (uint64, int, bool, bool) {
	if len(buffer) == 0 || buffer[0] == 0 {
		return 0, 0, false, false
	}
	mask := byte(0x80)
	length := 1
	for length <= 8 && buffer[0]&mask == 0 {
		mask >>= 1
		length++
	}
	if length > 8 || len(buffer) < length {
		return 0, 0, false, false
	}
	value := uint64(buffer[0])
	if isSize {
		value &= uint64(mask - 1)
	}
	for index := 1; index < length; index++ {
		value = value<<8 | uint64(buffer[index])
	}
	unknown := false
	if isSize {
		unknownValue := uint64(1)<<(7*length) - 1
		unknown = value == unknownValue
	}
	return value, length, unknown, true
}

// readMatroskaUint reads an unsigned integer element of up to eight bytes.
func readMatroskaUint(reader io.ReaderAt, element matroskaElement) (uint64, error) {
	size := element.dataEnd - element.dataStart
	if size <= 0 || size > 8 {
		return 0, errInvalidContainer
	}
	buffer := make([]byte, 8)
	if err := readAtFull(reader, buffer[8-size:], element.dataStart); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(buffer), nil
}
