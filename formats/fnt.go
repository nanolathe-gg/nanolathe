package formats

import (
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/vfs"
)

// FNT is a decoded bitmap font: one glyph per byte code, all of the font's
// own height. A code the file does not author has a nil glyph [fmt fnt].
type FNT struct {
	Height    uint8
	Ignored   uint8
	Baseline  int8
	FirstCode uint8
	Glyphs    [256]*FNTGlyph
}

// FNTGlyph is one glyph's bitmap, Width by Height bits packed row by row.
type FNTGlyph struct {
	Width  uint8
	Height uint8
	Bits   []byte
}

const maxFNTGlyphBits = 16 << 20

// LoadFNT decodes a font from its bytes [fmt fnt].
func LoadFNT(data []byte) (*FNT, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("fnt: file is too small")
	}
	fnt := &FNT{Height: data[0], Ignored: data[1], Baseline: int8(data[2]), FirstCode: data[3]}
	if fnt.Height == 0 {
		return nil, fmt.Errorf("fnt: zero glyph height")
	}
	tableBytes := 2 * (256 - int(fnt.FirstCode))
	if len(data) < 4+tableBytes {
		return nil, fmt.Errorf("fnt: glyph offset table is truncated")
	}
	var totalBits uint64
	for code := int(fnt.FirstCode); code < 256; code++ {
		tableIndex := code - int(fnt.FirstCode)
		offset := uint64(data[4+tableIndex*2]) | uint64(data[5+tableIndex*2])<<8
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

// LoadFNTFile reads and decodes a font from the VFS.
func LoadFNTFile(fs vfs.FSOps, name string) (*FNT, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadFNT(data)
}

// On reports whether the bit at (x, y) of the glyph is set. Coordinates
// outside the glyph read as clear.
func (g *FNTGlyph) On(x, y int) bool {
	if g == nil {
		return false
	}
	if x < 0 || y < 0 || x >= int(g.Width) || y >= int(g.Height) {
		return false
	}
	bit := y*int(g.Width) + x
	if bit/8 >= len(g.Bits) {
		// A manually assembled or partially decoded glyph must fail soft at
		// the raster boundary rather than panic. LoadFNT validates this bound
		// for file-backed glyphs [fmt fnt].
		return false
	}
	return g.Bits[bit/8]&(0x80>>uint(bit%8)) != 0
}
