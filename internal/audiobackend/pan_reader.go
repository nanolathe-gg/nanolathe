package audiobackend

import (
	"encoding/binary"
	"io"
	"math"
)

// panReader transforms canonical stereo float32 frames while they are read.
// It keeps one frame of state so arbitrary device read lengths, including
// unaligned short reads, produce the same byte stream as whole-frame reads.
type panReader struct {
	data     []byte
	pan      float64
	offset   int
	frame    [8]byte
	framePos int
}

func newPanReader(data []byte, pan float64) *panReader {
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	return &panReader{data: data, pan: pan, framePos: 8}
}

func (r *panReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if r.framePos == len(r.frame) {
			if r.offset >= len(r.data) {
				if n == 0 {
					return 0, io.EOF
				}
				return n, io.EOF
			}
			r.loadFrame()
		}
		copied := copy(p[n:], r.frame[r.framePos:])
		n += copied
		r.framePos += copied
		if r.framePos == len(r.frame) {
			r.offset += len(r.frame)
		}
	}
	return n, nil
}

func (r *panReader) loadFrame() {
	leftGain, rightGain := panGains(r.pan)
	left := math.Float32frombits(binary.LittleEndian.Uint32(r.data[r.offset:]))
	right := math.Float32frombits(binary.LittleEndian.Uint32(r.data[r.offset+4:]))
	binary.LittleEndian.PutUint32(r.frame[:], math.Float32bits(scaleChannel(left, leftGain)))
	binary.LittleEndian.PutUint32(r.frame[4:], math.Float32bits(scaleChannel(right, rightGain)))
	r.framePos = 0
}

func panGains(pan float64) (left, right float64) {
	if pan < 0 {
		return 1, 1 + pan
	}
	if pan > 0 {
		return 1 - pan, 1
	}
	return 1, 1
}

func scaleChannel(value float32, gain float64) float32 {
	value = float32(float64(value) * gain)
	if value < -1 {
		return -1
	}
	if value > 1 {
		return 1
	}
	return value
}
