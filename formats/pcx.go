package formats

import (
	"encoding/binary"
	"fmt"
	"image/color"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// PCX is a decoded PCX image: indexed pixels and the palette they index
// [fmt pcx].
type PCX struct {
	Width, Height          uint16
	XMin, YMin, XMax, YMax uint16
	BytesPerLine           uint16
	Pixels                 []byte
	Palette                [256]color.RGBA
}

const maxPCXPixels = 16 << 20

// LoadPCX decodes a PCX image from its bytes [fmt pcx].
func LoadPCX(data []byte) (*PCX, error) {
	if len(data) < 128+768 {
		return nil, fmt.Errorf("pcx: file is too small")
	}
	// Retail validates ONLY the manufacturer byte and the version byte;
	// encoding, pixel depth and plane count are unchecked because the RLE logic
	// implies them [02 §7 "PCX"]. Checking them here would reject files retail
	// accepts.
	if data[0] != 0x0a || data[1] != 5 {
		return nil, fmt.Errorf("pcx: unsupported header (manufacturer %#02x version %d, need 0x0a/5)", data[0], data[1])
	}
	xMin := binary.LittleEndian.Uint16(data[4:6])
	yMin := binary.LittleEndian.Uint16(data[6:8])
	xMax := binary.LittleEndian.Uint16(data[8:10])
	yMax := binary.LittleEndian.Uint16(data[10:12])
	if xMax < xMin || yMax < yMin {
		return nil, fmt.Errorf("pcx: invalid image bounds")
	}
	width := uint64(xMax-xMin) + 1
	height := uint64(yMax-yMin) + 1
	if width > 65535 || height > 65535 {
		return nil, fmt.Errorf("pcx: image dimensions exceed loader limits")
	}
	stride := binary.LittleEndian.Uint16(data[66:68])
	if width*height > uint64(^uint(0)>>1) || width*height > maxPCXPixels {
		return nil, fmt.Errorf("pcx: image is too large")
	}
	// The palette is read from the final 768 bytes, without any marker-byte
	// check [02 R-MALF-01 §9].
	trailer := len(data) - 768
	pcx := &PCX{Width: uint16(width), Height: uint16(height), XMin: xMin, YMin: yMin, XMax: xMax, YMax: yMax, BytesPerLine: stride, Pixels: make([]byte, int(width*height))}
	for i := 0; i < 256; i++ {
		base := trailer + i*3
		pcx.Palette[i] = color.RGBA{R: data[base], G: data[base+1], B: data[base+2], A: 255}
	}
	position := 128
	for row := 0; row < int(height); row++ {
		decoded := 0
		for decoded < int(width) {
			if position >= trailer {
				return nil, fmt.Errorf("pcx: scanline data is truncated")
			}
			value := data[position]
			position++
			run := 1
			if value&0xc0 == 0xc0 {
				run = int(value & 0x3f)
				if run == 0 || position >= trailer {
					return nil, fmt.Errorf("pcx: invalid RLE run")
				}
				value = data[position]
				position++
			}
			// Every run is clamped to the remaining visible width and the
			// excess discarded, so malformed files cannot overflow a scanline
			// [02 §7 "PCX"]. Retail does not fail here.
			if decoded+run > int(width) {
				run = int(width) - decoded
			}
			for i := 0; i < run; i++ {
				pcx.Pixels[row*int(width)+decoded+i] = value
			}
			decoded += run
		}
	}
	return pcx, nil
}

// LoadPCXFile reads and decodes a PCX image from the VFS.
func LoadPCXFile(fs vfs.FSOps, name string) (*PCX, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadPCX(data)
}

// At returns the palette index and resolved color at (x, y), and whether the
// coordinate lies inside the image.
func (p *PCX) At(x, y int) (byte, color.RGBA, bool) {
	if x < 0 || y < 0 || x >= int(p.Width) || y >= int(p.Height) {
		return 0, color.RGBA{}, false
	}
	index := p.Pixels[y*int(p.Width)+x]
	return index, p.Palette[index], true
}
