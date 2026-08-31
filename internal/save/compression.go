package save

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// decodeSQSH decodes the single-chunk SQSH framing used by HAPIBANK pools and
// account bodies.  Save chunks use the archive LZ77 variant (method 1); the
// zlib method is accepted as a format-level variant, but is not selected by
// the retail save writer [fmt hpi] [08 R-ENTRY-02 §3].
func decodeSQSH(src []byte) ([]byte, error) {
	if len(src) < 19 || string(src[:4]) != "SQSH" {
		return nil, fmt.Errorf("sqsh: short or missing header")
	}
	method := src[5]
	encoded := src[6]
	compressed := binary.LittleEndian.Uint32(src[7:])
	decompressed := binary.LittleEndian.Uint32(src[11:])
	checksum := binary.LittleEndian.Uint32(src[15:])
	if uint64(decompressed) > uint64(math.MaxInt) {
		return nil, fmt.Errorf("sqsh: output size exceeds host limit")
	}
	if compressed > uint32(len(src)-19) {
		return nil, fmt.Errorf("sqsh: payload exceeds framing")
	}
	payload := append([]byte(nil), src[19:19+int(compressed)]...)
	if sum := sumBytes(payload); sum != checksum {
		return nil, fmt.Errorf("sqsh: checksum mismatch")
	}
	if encoded != 0 {
		for i := range payload {
			payload[i] = byte((uint16(payload[i]) - uint16(i)) ^ uint16(i))
		}
	}
	var out []byte
	var err error
	switch method {
	case 1:
		out, err = decodeSQSHLZ(payload, int(decompressed))
	case 2:
		out, err = decodeSQSHZlib(payload)
	default:
		err = fmt.Errorf("sqsh: unsupported method %d", method)
	}
	if err != nil {
		return nil, err
	}
	if len(out) != int(decompressed) {
		return nil, fmt.Errorf("sqsh: decoded length %d, want %d", len(out), decompressed)
	}
	return out, nil
}

func sumBytes(b []byte) uint32 {
	var sum uint32
	for _, v := range b {
		sum += uint32(v)
	}
	return sum
}

func decodeSQSHZlib(payload []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// decodeSQSHLZ is the byte-oriented 4096-byte ring LZSS described by the
// HPI/SQSH format. Position zero is a terminator and the write cursor starts
// at one, allowing overlapping matches to encode runs [fmt hpi].
func decodeSQSHLZ(payload []byte, want int) ([]byte, error) {
	if want < 0 {
		return nil, fmt.Errorf("sqsh: negative output size")
	}
	window := make([]byte, 4096)
	write := 1
	out := make([]byte, 0, want)
	pos := 0
	terminated := false
	for !terminated {
		if pos >= len(payload) {
			return nil, fmt.Errorf("sqsh: truncated tag")
		}
		tag := payload[pos]
		pos++
		for bit := 0; bit < 8; bit++ {
			if tag&(1<<bit) == 0 {
				if pos >= len(payload) {
					return nil, fmt.Errorf("sqsh: truncated literal")
				}
				if len(out) >= want {
					return nil, fmt.Errorf("sqsh: output exceeds declared size")
				}
				v := payload[pos]
				pos++
				out = append(out, v)
				window[write] = v
				write = (write + 1) & 0xfff
				continue
			}
			if pos+2 > len(payload) {
				return nil, fmt.Errorf("sqsh: truncated match")
			}
			word := binary.LittleEndian.Uint16(payload[pos:])
			pos += 2
			match := int(word >> 4)
			if match == 0 {
				terminated = true
				break
			}
			length := int(word&0xf) + 2
			for i := 0; i < length; i++ {
				if len(out) >= want {
					return nil, fmt.Errorf("sqsh: output exceeds declared size")
				}
				v := window[match]
				match = (match + 1) & 0xfff
				out = append(out, v)
				window[write] = v
				write = (write + 1) & 0xfff
			}
		}
	}
	if len(out) != want {
		return nil, fmt.Errorf("sqsh: terminator before output is complete")
	}
	return out, nil
}
