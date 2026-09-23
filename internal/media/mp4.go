// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package media

import (
	"encoding/binary"
	"io"
	"math"
)

const maxMP4TableEntries = 16 << 20

type mp4Box struct {
	dataEnd   int64
	dataStart int64
	typ       string
}

type mp4Edit struct {
	duration  uint64
	mediaTime int64
}

type mp4SampleTable struct {
	chunkOffsets []uint64
	edits        []mp4Edit
	movieScale   uint32
	sampleSizes  []uint32
	stsc         []mp4SampleToChunk
	stss         []uint32
	stts         []mp4TimeToSample
	timescale    uint32
	uniformSize  uint32
}

type mp4SampleToChunk struct {
	firstChunk      uint32
	samplesPerChunk uint32
}

type mp4TimeToSample struct {
	count uint32
	delta uint32
}

func resolveMP4Offset(reader io.ReaderAt, size int64, positionSeconds float64) (int64, bool, error) {
	moov, found, err := findMP4Box(reader, 0, size, "moov")
	if err != nil || !found {
		return 0, false, err
	}

	movieScale, err := readMP4MovieTimescale(reader, moov)
	if err != nil {
		return 0, false, err
	}

	children, err := listMP4Boxes(reader, moov.dataStart, moov.dataEnd)
	if err != nil {
		return 0, false, err
	}
	for _, child := range children {
		if child.typ != "trak" {
			continue
		}
		table, isVideo, parseErr := readMP4Track(reader, child, movieScale)
		if parseErr != nil {
			return 0, false, parseErr
		}
		if !isVideo {
			continue
		}
		offset, resolved := table.resolveOffset(positionSeconds)
		if !resolved || offset < 0 || offset >= size {
			return 0, false, nil
		}
		return offset, true, nil
	}

	return 0, false, nil
}

func readMP4MovieTimescale(reader io.ReaderAt, moov mp4Box) (uint32, error) {
	mvhd, found, err := findMP4Box(reader, moov.dataStart, moov.dataEnd, "mvhd")
	if err != nil || !found {
		return 0, err
	}
	buffer := make([]byte, 24)
	if err := readAtFull(reader, buffer, mvhd.dataStart); err != nil {
		return 0, err
	}
	if buffer[0] == 1 {
		return binary.BigEndian.Uint32(buffer[20:24]), nil
	}
	return binary.BigEndian.Uint32(buffer[12:16]), nil
}

func readMP4Track(reader io.ReaderAt, trak mp4Box, movieScale uint32) (mp4SampleTable, bool, error) {
	var table mp4SampleTable
	table.movieScale = movieScale

	mdia, found, err := findMP4Box(reader, trak.dataStart, trak.dataEnd, "mdia")
	if err != nil || !found {
		return table, false, err
	}
	hdlr, found, err := findMP4Box(reader, mdia.dataStart, mdia.dataEnd, "hdlr")
	if err != nil || !found {
		return table, false, err
	}
	handler := make([]byte, 12)
	if err := readAtFull(reader, handler, hdlr.dataStart); err != nil {
		return table, false, err
	}
	if string(handler[8:12]) != "vide" {
		return table, false, nil
	}

	mdhd, found, err := findMP4Box(reader, mdia.dataStart, mdia.dataEnd, "mdhd")
	if err != nil || !found {
		return table, true, err
	}
	mdhdHeader := make([]byte, 24)
	if err := readAtFull(reader, mdhdHeader, mdhd.dataStart); err != nil {
		return table, true, err
	}
	if mdhdHeader[0] == 1 {
		table.timescale = binary.BigEndian.Uint32(mdhdHeader[20:24])
	} else {
		table.timescale = binary.BigEndian.Uint32(mdhdHeader[12:16])
	}
	if table.timescale == 0 {
		return table, true, errInvalidContainer
	}

	if edts, hasEdits, findErr := findMP4Box(reader, trak.dataStart, trak.dataEnd, "edts"); findErr != nil {
		return table, true, findErr
	} else if hasEdits {
		if elst, hasList, listErr := findMP4Box(reader, edts.dataStart, edts.dataEnd, "elst"); listErr != nil {
			return table, true, listErr
		} else if hasList {
			table.edits, err = readMP4EditList(reader, elst)
			if err != nil {
				return table, true, err
			}
		}
	}

	minf, found, err := findMP4Box(reader, mdia.dataStart, mdia.dataEnd, "minf")
	if err != nil || !found {
		return table, true, err
	}
	stbl, found, err := findMP4Box(reader, minf.dataStart, minf.dataEnd, "stbl")
	if err != nil || !found {
		return table, true, err
	}
	boxes, err := listMP4Boxes(reader, stbl.dataStart, stbl.dataEnd)
	if err != nil {
		return table, true, err
	}
	for _, box := range boxes {
		switch box.typ {
		case "co64":
			table.chunkOffsets, err = readMP4Offsets(reader, box, true)
		case "stco":
			table.chunkOffsets, err = readMP4Offsets(reader, box, false)
		case "stsc":
			table.stsc, err = readMP4SampleToChunk(reader, box)
		case "stss":
			table.stss, err = readMP4Uint32Table(reader, box)
		case "stsz":
			table.uniformSize, table.sampleSizes, err = readMP4SampleSizes(reader, box)
		case "stts":
			table.stts, err = readMP4TimeToSample(reader, box)
		}
		if err != nil {
			return table, true, err
		}
	}

	return table, true, nil
}

func (table mp4SampleTable) resolveOffset(positionSeconds float64) (int64, bool) {
	if table.timescale == 0 || len(table.stts) == 0 || len(table.stsc) == 0 || len(table.chunkOffsets) == 0 {
		return 0, false
	}
	targetTime := table.mediaTime(positionSeconds)
	sample := table.sampleAtTime(targetTime)
	if len(table.stss) > 0 {
		targetNumber := sample + 1
		syncSample := table.stss[0]
		for _, candidate := range table.stss {
			if uint64(candidate) > targetNumber {
				break
			}
			syncSample = candidate
		}
		if syncSample == 0 {
			return 0, false
		}
		sample = uint64(syncSample - 1)
	}

	chunk, firstSample, ok := table.chunkForSample(sample)
	if !ok || chunk >= uint64(len(table.chunkOffsets)) {
		return 0, false
	}
	offset := table.chunkOffsets[chunk]
	for current := firstSample; current < sample; current++ {
		size, hasSize := table.sampleSize(current)
		if !hasSize || math.MaxUint64-offset < uint64(size) {
			return 0, false
		}
		offset += uint64(size)
	}
	if offset > math.MaxInt64 {
		return 0, false
	}
	return int64(offset), true
}

func (table mp4SampleTable) mediaTime(positionSeconds float64) uint64 {
	if len(table.edits) == 0 || table.movieScale == 0 {
		return scaleSeconds(positionSeconds, float64(table.timescale))
	}
	presentation := positionSeconds * float64(table.movieScale)
	var elapsed float64
	for _, edit := range table.edits {
		duration := float64(edit.duration)
		if presentation < elapsed+duration {
			if edit.mediaTime < 0 {
				return 0
			}
			within := presentation - elapsed
			mediaTime := uint64(edit.mediaTime)
			offset := scaleSeconds(within, float64(table.timescale)/float64(table.movieScale))
			if math.MaxUint64-mediaTime < offset {
				return math.MaxUint64
			}
			return mediaTime + offset
		}
		elapsed += duration
	}
	return scaleSeconds(positionSeconds, float64(table.timescale))
}

func (table mp4SampleTable) sampleAtTime(target uint64) uint64 {
	var elapsed, sample uint64
	for _, entry := range table.stts {
		entryDuration := uint64(entry.count) * uint64(entry.delta)
		if entry.delta > 0 && target < elapsed+entryDuration {
			return sample + (target-elapsed)/uint64(entry.delta)
		}
		elapsed += entryDuration
		sample += uint64(entry.count)
	}
	if sample == 0 {
		return 0
	}
	return sample - 1
}

func (table mp4SampleTable) chunkForSample(target uint64) (uint64, uint64, bool) {
	var firstSample uint64
	for index, entry := range table.stsc {
		if entry.firstChunk == 0 || entry.samplesPerChunk == 0 {
			return 0, 0, false
		}
		firstChunk := uint64(entry.firstChunk - 1)
		nextChunk := uint64(len(table.chunkOffsets))
		if index+1 < len(table.stsc) {
			nextChunk = uint64(table.stsc[index+1].firstChunk - 1)
		}
		if nextChunk < firstChunk {
			return 0, 0, false
		}
		spanSamples := (nextChunk - firstChunk) * uint64(entry.samplesPerChunk)
		if target < firstSample+spanSamples {
			relative := target - firstSample
			chunkDelta := relative / uint64(entry.samplesPerChunk)
			return firstChunk + chunkDelta, firstSample + chunkDelta*uint64(entry.samplesPerChunk), true
		}
		firstSample += spanSamples
	}
	return 0, 0, false
}

func (table mp4SampleTable) sampleSize(sample uint64) (uint32, bool) {
	if table.uniformSize > 0 {
		return table.uniformSize, true
	}
	if sample >= uint64(len(table.sampleSizes)) {
		return 0, false
	}
	return table.sampleSizes[sample], true
}

func findMP4Box(reader io.ReaderAt, start, end int64, typ string) (mp4Box, bool, error) {
	boxes, err := listMP4Boxes(reader, start, end)
	if err != nil {
		return mp4Box{}, false, err
	}
	for _, box := range boxes {
		if box.typ == typ {
			return box, true, nil
		}
	}
	return mp4Box{}, false, nil
}

func listMP4Boxes(reader io.ReaderAt, start, end int64) ([]mp4Box, error) {
	boxes := make([]mp4Box, 0, 8)
	for offset := start; offset+8 <= end; {
		header := make([]byte, 16)
		if err := readAtFull(reader, header[:8], offset); err != nil {
			return nil, err
		}
		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		switch boxSize {
		case 1:
			if err := readAtFull(reader, header[8:16], offset+8); err != nil {
				return nil, err
			}
			largeSize := binary.BigEndian.Uint64(header[8:16])
			if largeSize > math.MaxInt64 {
				return nil, errInvalidContainer
			}
			boxSize = int64(largeSize)
			headerSize = 16
		case 0:
			boxSize = end - offset
		}
		boxEnd, valid := checkedEnd(offset, boxSize, end)
		if !valid || boxSize < headerSize {
			return nil, errInvalidContainer
		}
		boxes = append(boxes, mp4Box{
			dataEnd:   boxEnd,
			dataStart: offset + headerSize,
			typ:       string(header[4:8]),
		})
		offset = boxEnd
	}
	return boxes, nil
}

func readMP4EntryCount(reader io.ReaderAt, box mp4Box, entrySize int64) (uint32, int64, error) {
	header := make([]byte, 8)
	if err := readAtFull(reader, header, box.dataStart); err != nil {
		return 0, 0, err
	}
	count := binary.BigEndian.Uint32(header[4:8])
	if count > maxMP4TableEntries {
		return 0, 0, errInvalidContainer
	}
	bytes := int64(count) * entrySize
	if _, valid := checkedEnd(box.dataStart+8, bytes, box.dataEnd); !valid {
		return 0, 0, errInvalidContainer
	}
	return count, box.dataStart + 8, nil
}

func readMP4Uint32Table(reader io.ReaderAt, box mp4Box) ([]uint32, error) {
	count, offset, err := readMP4EntryCount(reader, box, 4)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, int(count)*4)
	if err := readAtFull(reader, buffer, offset); err != nil {
		return nil, err
	}
	values := make([]uint32, count)
	for index := range values {
		values[index] = binary.BigEndian.Uint32(buffer[index*4 : index*4+4])
	}
	return values, nil
}

func readMP4Offsets(reader io.ReaderAt, box mp4Box, is64Bit bool) ([]uint64, error) {
	entrySize := int64(4)
	if is64Bit {
		entrySize = 8
	}
	count, offset, err := readMP4EntryCount(reader, box, entrySize)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, int64(count)*entrySize)
	if err := readAtFull(reader, buffer, offset); err != nil {
		return nil, err
	}
	values := make([]uint64, count)
	for index := range values {
		if is64Bit {
			values[index] = binary.BigEndian.Uint64(buffer[index*8 : index*8+8])
		} else {
			values[index] = uint64(binary.BigEndian.Uint32(buffer[index*4 : index*4+4]))
		}
	}
	return values, nil
}

func readMP4TimeToSample(reader io.ReaderAt, box mp4Box) ([]mp4TimeToSample, error) {
	count, offset, err := readMP4EntryCount(reader, box, 8)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, int(count)*8)
	if err := readAtFull(reader, buffer, offset); err != nil {
		return nil, err
	}
	entries := make([]mp4TimeToSample, count)
	for index := range entries {
		entries[index] = mp4TimeToSample{
			count: binary.BigEndian.Uint32(buffer[index*8 : index*8+4]),
			delta: binary.BigEndian.Uint32(buffer[index*8+4 : index*8+8]),
		}
	}
	return entries, nil
}

func readMP4SampleToChunk(reader io.ReaderAt, box mp4Box) ([]mp4SampleToChunk, error) {
	count, offset, err := readMP4EntryCount(reader, box, 12)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, int(count)*12)
	if err := readAtFull(reader, buffer, offset); err != nil {
		return nil, err
	}
	entries := make([]mp4SampleToChunk, count)
	for index := range entries {
		entries[index] = mp4SampleToChunk{
			firstChunk:      binary.BigEndian.Uint32(buffer[index*12 : index*12+4]),
			samplesPerChunk: binary.BigEndian.Uint32(buffer[index*12+4 : index*12+8]),
		}
	}
	return entries, nil
}

func readMP4SampleSizes(reader io.ReaderAt, box mp4Box) (uint32, []uint32, error) {
	header := make([]byte, 12)
	if err := readAtFull(reader, header, box.dataStart); err != nil {
		return 0, nil, err
	}
	uniform := binary.BigEndian.Uint32(header[4:8])
	count := binary.BigEndian.Uint32(header[8:12])
	if count > maxMP4TableEntries {
		return 0, nil, errInvalidContainer
	}
	if uniform > 0 {
		return uniform, nil, nil
	}
	if _, valid := checkedEnd(box.dataStart+12, int64(count)*4, box.dataEnd); !valid {
		return 0, nil, errInvalidContainer
	}
	buffer := make([]byte, int(count)*4)
	if err := readAtFull(reader, buffer, box.dataStart+12); err != nil {
		return 0, nil, err
	}
	sizes := make([]uint32, count)
	for index := range sizes {
		sizes[index] = binary.BigEndian.Uint32(buffer[index*4 : index*4+4])
	}
	return 0, sizes, nil
}

func readMP4EditList(reader io.ReaderAt, box mp4Box) ([]mp4Edit, error) {
	header := make([]byte, 8)
	if err := readAtFull(reader, header, box.dataStart); err != nil {
		return nil, err
	}
	version := header[0]
	count := binary.BigEndian.Uint32(header[4:8])
	entrySize := int64(12)
	if version == 1 {
		entrySize = 20
	}
	if count > maxMP4TableEntries {
		return nil, errInvalidContainer
	}
	if _, valid := checkedEnd(box.dataStart+8, int64(count)*entrySize, box.dataEnd); !valid {
		return nil, errInvalidContainer
	}
	buffer := make([]byte, int64(count)*entrySize)
	if err := readAtFull(reader, buffer, box.dataStart+8); err != nil {
		return nil, err
	}
	edits := make([]mp4Edit, count)
	for index := range edits {
		entry := buffer[int64(index)*entrySize:]
		if version == 1 {
			mediaTime := int64(binary.BigEndian.Uint32(entry[8:12])) << 32
			mediaTime |= int64(binary.BigEndian.Uint32(entry[12:16]))
			edits[index] = mp4Edit{
				duration:  binary.BigEndian.Uint64(entry[:8]),
				mediaTime: mediaTime,
			}
		} else {
			mediaTime := int64(binary.BigEndian.Uint32(entry[4:8]))
			if mediaTime >= 1<<31 {
				mediaTime -= 1 << 32
			}
			edits[index] = mp4Edit{
				duration:  uint64(binary.BigEndian.Uint32(entry[:4])),
				mediaTime: mediaTime,
			}
		}
	}
	return edits, nil
}
