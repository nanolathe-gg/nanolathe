package formats

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/vfs"
)

type FNT struct {
	Height  uint16
	Unknown uint16
	Glyphs  [256]*FNTGlyph
}

type FNTGlyph struct {
	Width  uint8
	Height uint16
	Bits   []byte
}

const maxFNTGlyphBits = 16 << 20

func LoadFNT(data []byte) (*FNT, error) {
	if len(data) < 516 {
		return nil, fmt.Errorf("fnt: file is too small")
	}
	fnt := &FNT{Height: binary.LittleEndian.Uint16(data[0:2]), Unknown: binary.LittleEndian.Uint16(data[2:4])}
	if fnt.Height == 0 {
		return nil, fmt.Errorf("fnt: zero glyph height")
	}
	var totalBits uint64
	for code := 0; code < 256; code++ {
		offset := uint64(binary.LittleEndian.Uint16(data[4+code*2 : 6+code*2]))
		if offset == 0 {
			continue
		}
		if offset >= uint64(len(data)) {
			return nil, fmt.Errorf("fnt: glyph 0x%02x offset outside file", code)
		}
		width := data[offset]
		bitCount := uint64(width) * uint64(fnt.Height)
		if bitCount > maxFNTGlyphBits || totalBits > maxFNTGlyphBits-bitCount {
			return nil, fmt.Errorf("fnt: glyph data exceeds loader limits")
		}
		byteCount := (bitCount + 7) / 8
		if byteCount > uint64(math.MaxInt) || uint64(len(data))-offset-1 < byteCount {
			return nil, fmt.Errorf("fnt: glyph 0x%02x bitmap is truncated", code)
		}
		bits := make([]byte, int(byteCount))
		copy(bits, data[offset+1:offset+1+byteCount])
		fnt.Glyphs[code] = &FNTGlyph{Width: width, Height: fnt.Height, Bits: bits}
		totalBits += bitCount
	}
	return fnt, nil
}

func LoadFNTFile(fs vfs.FSOps, name string) (*FNT, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadFNT(data)
}

func (g *FNTGlyph) On(x, y int) bool {
	if x < 0 || y < 0 || x >= int(g.Width) || y >= int(g.Height) {
		return false
	}
	bit := y*int(g.Width) + x
	return g.Bits[bit/8]&(0x80>>uint(bit%8)) != 0
}
