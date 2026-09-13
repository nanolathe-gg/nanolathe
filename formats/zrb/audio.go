package zrb

import (
	"encoding/binary"
	"fmt"
)

func audioBuffer(dst []byte, n int) []byte {
	if cap(dst) < n {
		return make([]byte, n)
	}
	return dst[:n]
}

// DPCM predictors restart per packet; deltas add at the complete sample width
// before output normalization, preserving low-byte carries [fmt zrb].
func decodeAudio(data []byte, flags uint32, dst []byte) ([]byte, error) {
	channels := 1
	if flags&(1<<28) != 0 {
		channels = 2
	}
	width := 1
	if flags&(1<<29) != 0 {
		width = 2
	}
	if flags&(1<<31) == 0 {
		if len(data)%(channels*width) != 0 || len(data) > maxAudioPacket {
			return nil, fmt.Errorf("invalid PCM size")
		}
		dst = audioBuffer(dst, len(data)*2/width)
		if width == 2 {
			copy(dst, data)
		} else {
			for i, v := range data {
				binary.LittleEndian.PutUint16(dst[i*2:], uint16((int(v)-128)*256))
			}
		}
		return dst, nil
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("truncated decoded audio length")
	}
	size := int(binary.LittleEndian.Uint32(data))
	data = data[4:]
	if size > maxAudioPacket || size%(channels*width) != 0 {
		return nil, fmt.Errorf("invalid decoded audio length %d", size)
	}
	b := bitReader{data: data}
	if b.read(1) == 0 {
		return dst[:0], b.err
	}
	stereo, wide := b.read(1), b.read(1)
	if int(stereo)+1 != channels || int(wide)+1 != width {
		return nil, fmt.Errorf("audio packet format disagrees with header")
	}
	if size < channels*width {
		return nil, fmt.Errorf("decoded audio lacks initial sample")
	}
	var trees [4]huffTree
	for i := 0; i < channels*width; i++ {
		trees[i] = readByteTree(&b)
	}
	if b.err != nil {
		return nil, b.err
	}
	var sample [2]uint16
	for ch := channels - 1; ch >= 0; ch-- {
		if width == 2 {
			sample[ch] = uint16(b.read(8)) << 8
		}
		sample[ch] |= uint16(b.read(8))
	}
	dst = audioBuffer(dst, size*2/width)
	for frame := 0; frame < size/(channels*width); frame++ {
		for ch := 0; ch < channels; ch++ {
			if frame != 0 {
				delta := trees[ch*width].decode(&b)
				if width == 2 {
					delta |= trees[ch*width+1].decode(&b) << 8
				}
				sample[ch] += delta
			}
			v := sample[ch]
			if width == 1 {
				sample[ch] &= 255
				v = uint16((int(sample[ch]) - 128) * 256)
			}
			binary.LittleEndian.PutUint16(dst[(frame*channels+ch)*2:], v)
		}
		if b.err != nil {
			return nil, b.err
		}
	}
	return dst, nil
}
