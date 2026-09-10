package content

import "encoding/binary"

// testGAFEntry describes an authored GAF entry for content fixtures.
type testGAFEntry struct {
	name     string
	frames   int
	duration uint32
}

func testGAF(name string, frames int, duration uint32) []byte {
	return testGAFEntries(testGAFEntry{name: name, frames: frames, duration: duration})
}

func testGAFEntries(entries ...testGAFEntry) []byte {
	const entryTableOffset = 12
	const entrySize = 40
	const frameRefSize = 8
	const frameSize = 24
	entryOffsets := make([]int, len(entries))
	cursor := entryTableOffset + len(entries)*4
	totalFrames := 0
	for i, entry := range entries {
		entryOffsets[i] = cursor
		cursor += entrySize + entry.frames*frameRefSize
		totalFrames += entry.frames
	}
	frameDataOffset := cursor + totalFrames*frameSize
	out := make([]byte, frameDataOffset+totalFrames)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(entries)))
	for i, entry := range entries {
		entryOffset := entryOffsets[i]
		refOffset := entryOffset + entrySize
		frameOffset := cursor
		binary.LittleEndian.PutUint32(out[entryTableOffset+i*4:], uint32(entryOffset))
		binary.LittleEndian.PutUint16(out[entryOffset:], uint16(entry.frames))
		copy(out[entryOffset+8:], entry.name)
		for frameIndex := 0; frameIndex < entry.frames; frameIndex++ {
			ref := refOffset + frameIndex*frameRefSize
			frame := frameOffset + frameIndex*frameSize
			binary.LittleEndian.PutUint32(out[ref:], uint32(frame))
			binary.LittleEndian.PutUint32(out[ref+4:], entry.duration)
			binary.LittleEndian.PutUint16(out[frame:], 1)
			binary.LittleEndian.PutUint16(out[frame+2:], 1)
			binary.LittleEndian.PutUint16(out[frame+4:], 3)
			binary.LittleEndian.PutUint16(out[frame+6:], 0xfffe)
			out[frame+8] = 9
			binary.LittleEndian.PutUint32(out[frame+16:], uint32(frameDataOffset))
			out[frameDataOffset] = 7
			frameDataOffset++
		}
		cursor += entry.frames * frameSize
	}
	return out
}
