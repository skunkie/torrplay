// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package media

import (
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"math"
)

const (
	matroskaEBMLID                = 0x1A45DFA3
	matroskaSegmentID             = 0x18538067
	matroskaSeekHeadID            = 0x114D9B74
	matroskaSeekEntryID           = 0x4DBB
	matroskaSeekIdentifierID      = 0x53AB
	matroskaSeekPositionID        = 0x53AC
	matroskaInfoID                = 0x1549A966
	matroskaTimestampScaleID      = 0x2AD7B1
	matroskaTracksID              = 0x1654AE6B
	matroskaTrackEntryID          = 0xAE
	matroskaTrackNumberID         = 0xD7
	matroskaTrackTypeID           = 0x83
	matroskaCuesID                = 0x1C53BB6B
	matroskaCuePointID            = 0xBB
	matroskaCueTimeID             = 0xB3
	matroskaCueTrackPositionsID   = 0xB7
	matroskaCueTrackID            = 0xF7
	matroskaCueClusterPositionID  = 0xF1
	matroskaCueRelativePositionID = 0xF0
	matroskaClusterID             = 0x1F43B675
)

type matroskaElement struct {
	dataEnd   int64
	dataStart int64
	id        uint64
	offset    int64
}

type matroskaCue struct {
	clusterPosition  uint64
	relativePosition uint64
	time             uint64
	track            uint64
}

func resolveMatroskaOffset(reader io.ReaderAt, size int64, positionSeconds float64) (int64, bool, error) {
	segment, found, err := findMatroskaTopLevel(reader, 0, size, matroskaSegmentID)
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
	target := scaleSeconds(positionSeconds, 1_000_000_000/float64(timestampScale))
	cue, found, err := findMatroskaCue(reader, cues, videoTrack, target)
	if err != nil || !found {
		return 0, false, err
	}
	if cue.clusterPosition > math.MaxInt64 {
		return 0, false, nil
	}
	clusterOffset := segment.dataStart + int64(cue.clusterPosition)
	cluster, clusterFound, err := readMatroskaElement(reader, clusterOffset, segment.dataEnd)
	if err != nil || !clusterFound || cluster.id != matroskaClusterID {
		return 0, false, err
	}
	resolved := cluster.offset
	if cue.relativePosition > 0 {
		if cue.relativePosition > math.MaxInt64 {
			return 0, false, nil
		}
		relativePosition := int64(cue.relativePosition)
		if relativePosition > cluster.dataEnd-cluster.dataStart {
			return 0, false, nil
		}
		resolved = cluster.dataStart + relativePosition
	}
	if resolved < 0 || resolved >= size {
		return 0, false, nil
	}
	return resolved, true, nil
}

func scanMatroskaSegment(reader io.ReaderAt, segment matroskaElement) (map[uint64]matroskaElement, map[uint64]uint64, error) {
	elements := make(map[uint64]matroskaElement)
	seekTargets := make(map[uint64]uint64)
	for offset := segment.dataStart; offset < segment.dataEnd; {
		element, ok, err := readMatroskaElement(reader, offset, segment.dataEnd)
		if err != nil || !ok {
			return nil, nil, err
		}
		if _, exists := elements[element.id]; !exists {
			elements[element.id] = element
		}
		if element.id == matroskaSeekHeadID {
			targets, parseErr := readMatroskaSeekHead(reader, element)
			if parseErr != nil {
				return nil, nil, parseErr
			}
			maps.Copy(seekTargets, targets)
		}
		if element.id == matroskaClusterID && len(seekTargets) > 0 {
			break
		}
		if element.dataEnd <= offset {
			return nil, nil, errInvalidContainer
		}
		offset = element.dataEnd
	}
	return elements, seekTargets, nil
}

func readMatroskaSeekHead(reader io.ReaderAt, seekHead matroskaElement) (map[uint64]uint64, error) {
	targets := make(map[uint64]uint64)
	for offset := seekHead.dataStart; offset < seekHead.dataEnd; {
		entry, ok, err := readMatroskaElement(reader, offset, seekHead.dataEnd)
		if err != nil || !ok {
			return nil, err
		}
		if entry.id == matroskaSeekEntryID {
			identifier, hasIdentifier, findErr := findMatroskaChild(reader, entry, matroskaSeekIdentifierID)
			if findErr != nil {
				return nil, findErr
			}
			position, hasPosition, findErr := findMatroskaChild(reader, entry, matroskaSeekPositionID)
			if findErr != nil {
				return nil, findErr
			}
			if hasIdentifier && hasPosition {
				id, readErr := readMatroskaUint(reader, identifier)
				if readErr != nil {
					return nil, readErr
				}
				value, readErr := readMatroskaUint(reader, position)
				if readErr != nil {
					return nil, readErr
				}
				targets[id] = value
			}
		}
		offset = entry.dataEnd
	}
	return targets, nil
}

func findMatroskaVideoTrack(reader io.ReaderAt, tracks matroskaElement) (uint64, bool, error) {
	for offset := tracks.dataStart; offset < tracks.dataEnd; {
		entry, ok, err := readMatroskaElement(reader, offset, tracks.dataEnd)
		if err != nil || !ok {
			return 0, false, err
		}
		if entry.id == matroskaTrackEntryID {
			numberElement, hasNumber, findErr := findMatroskaChild(reader, entry, matroskaTrackNumberID)
			if findErr != nil {
				return 0, false, findErr
			}
			typeElement, hasType, findErr := findMatroskaChild(reader, entry, matroskaTrackTypeID)
			if findErr != nil {
				return 0, false, findErr
			}
			if hasNumber && hasType {
				trackType, readErr := readMatroskaUint(reader, typeElement)
				if readErr != nil {
					return 0, false, readErr
				}
				if trackType == 1 {
					number, readErr := readMatroskaUint(reader, numberElement)
					return number, readErr == nil, readErr
				}
			}
		}
		offset = entry.dataEnd
	}
	return 0, false, nil
}

func findMatroskaCue(reader io.ReaderAt, cues matroskaElement, videoTrack, target uint64) (matroskaCue, bool, error) {
	var selected matroskaCue
	found := false
	for offset := cues.dataStart; offset < cues.dataEnd; {
		point, ok, err := readMatroskaElement(reader, offset, cues.dataEnd)
		if err != nil || !ok {
			return matroskaCue{}, false, err
		}
		if point.id == matroskaCuePointID {
			cue, cueFound, parseErr := readMatroskaCuePoint(reader, point, videoTrack)
			if parseErr != nil {
				return matroskaCue{}, false, parseErr
			}
			if cueFound {
				if !found {
					selected = cue
					found = true
				}
				if cue.time <= target && cue.time >= selected.time {
					selected = cue
				}
				if cue.time > target && selected.time <= target {
					break
				}
			}
		}
		offset = point.dataEnd
	}
	return selected, found, nil
}

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

	for offset := point.dataStart; offset < point.dataEnd; {
		positions, ok, readErr := readMatroskaElement(reader, offset, point.dataEnd)
		if readErr != nil || !ok {
			return cue, false, readErr
		}
		if positions.id == matroskaCueTrackPositionsID {
			trackElement, hasTrack, findErr := findMatroskaChild(reader, positions, matroskaCueTrackID)
			if findErr != nil {
				return cue, false, findErr
			}
			clusterElement, hasCluster, findErr := findMatroskaChild(reader, positions, matroskaCueClusterPositionID)
			if findErr != nil {
				return cue, false, findErr
			}
			if hasTrack && hasCluster {
				cue.track, err = readMatroskaUint(reader, trackElement)
				if err != nil {
					return cue, false, err
				}
				if cue.track == videoTrack {
					cue.clusterPosition, err = readMatroskaUint(reader, clusterElement)
					if err != nil {
						return cue, false, err
					}
					if relative, hasRelative, findErr := findMatroskaChild(reader, positions, matroskaCueRelativePositionID); findErr != nil {
						return cue, false, findErr
					} else if hasRelative {
						cue.relativePosition, err = readMatroskaUint(reader, relative)
						if err != nil {
							return cue, false, err
						}
					}
					return cue, true, nil
				}
			}
		}
		offset = positions.dataEnd
	}
	return cue, false, nil
}

func findMatroskaTopLevel(reader io.ReaderAt, start, end int64, id uint64) (matroskaElement, bool, error) {
	for offset := start; offset < end; {
		element, ok, err := readMatroskaElement(reader, offset, end)
		if err != nil || !ok {
			return matroskaElement{}, false, err
		}
		if element.id == id {
			return element, true, nil
		}
		offset = element.dataEnd
	}
	return matroskaElement{}, false, nil
}

func findMatroskaChild(reader io.ReaderAt, parent matroskaElement, id uint64) (matroskaElement, bool, error) {
	for offset := parent.dataStart; offset < parent.dataEnd; {
		element, ok, err := readMatroskaElement(reader, offset, parent.dataEnd)
		if err != nil || !ok {
			return matroskaElement{}, false, err
		}
		if element.id == id {
			return element, true, nil
		}
		offset = element.dataEnd
	}
	return matroskaElement{}, false, nil
}

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
