package formats

import (
	"encoding/binary"
	"fmt"
	"image/color"

	"github.com/nanolathe/nanolathe/vfs"
)

type PCX struct {
	Width, Height          uint16
	XMin, YMin, XMax, YMax uint16
	BytesPerLine           uint16
	Pixels                 []byte
	Palette                [256]color.RGBA
}

const maxPCXPixels = 16 << 20

func LoadPCX(data []byte) (*PCX, error) {
	if len(data) < 128+769 {
		return nil, fmt.Errorf("pcx: file is too small")
	}
	if data[0] != 0x0a || data[1] != 5 || data[2] != 1 || data[3] != 8 || data[65] != 1 {
		return nil, fmt.Errorf("pcx: unsupported header (need version 5, 8bpp, one plane, RLE)")
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
	stride := uint64(binary.LittleEndian.Uint16(data[66:68]))
	if stride < width || stride == 0 {
		return nil, fmt.Errorf("pcx: invalid scanline stride")
	}
	if width*height > uint64(^uint(0)>>1) || width*height > maxPCXPixels {
		return nil, fmt.Errorf("pcx: image is too large")
	}
	trailer := len(data) - 769
	if data[trailer] != 0x0c {
		return nil, fmt.Errorf("pcx: missing palette marker")
	}
	pcx := &PCX{Width: uint16(width), Height: uint16(height), XMin: xMin, YMin: yMin, XMax: xMax, YMax: yMax, BytesPerLine: uint16(stride), Pixels: make([]byte, int(width*height))}
	for i := 0; i < 256; i++ {
		base := trailer + 1 + i*3
		pcx.Palette[i] = color.RGBA{R: data[base], G: data[base+1], B: data[base+2], A: 255}
	}
	position := 128
	for row := 0; row < int(height); row++ {
		decoded := 0
		for decoded < int(stride) {
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
			if decoded+run > int(stride) {
				return nil, fmt.Errorf("pcx: RLE run exceeds scanline")
			}
			for i := 0; i < run; i++ {
				if decoded+i < int(width) {
					pcx.Pixels[row*int(width)+decoded+i] = value
				}
			}
			decoded += run
		}
	}
	return pcx, nil
}

func LoadPCXFile(fs vfs.FSOps, name string) (*PCX, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadPCX(data)
}

func (p *PCX) At(x, y int) (byte, color.RGBA, bool) {
	if x < 0 || y < 0 || x >= int(p.Width) || y >= int(p.Height) {
		return 0, color.RGBA{}, false
	}
	index := p.Pixels[y*int(p.Width)+x]
	return index, p.Palette[index], true
}
