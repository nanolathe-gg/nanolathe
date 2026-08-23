package formats

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
)

const maxBMPPixels = 16 << 20

// LoadBMP decodes the uncompressed Windows BMP variants used by the retail
// installation. It intentionally accepts only bounded 4/8/24/32-bit BI_RGB
// images; compressed or exotic BMP payloads remain explicit parse errors.
func LoadBMP(data []byte) (image.Image, error) {
	if len(data) < 54 || string(data[:2]) != "BM" {
		return nil, fmt.Errorf("bmp: invalid file header")
	}
	pixelOffset := int64(binary.LittleEndian.Uint32(data[10:14]))
	dibSize := binary.LittleEndian.Uint32(data[14:18])
	if dibSize < 40 || uint64(14)+uint64(dibSize) > uint64(len(data)) {
		return nil, fmt.Errorf("bmp: unsupported DIB header")
	}
	width := int64(int32(binary.LittleEndian.Uint32(data[18:22])))
	heightValue := int64(int32(binary.LittleEndian.Uint32(data[22:26])))
	if width <= 0 || heightValue == 0 {
		return nil, fmt.Errorf("bmp: invalid dimensions")
	}
	topDown := heightValue < 0
	height := heightValue
	if topDown {
		height = -height
	}
	if width > 8192 || height > 8192 || width > int64(^uint(0)>>1)/height || width*height > maxBMPPixels {
		return nil, fmt.Errorf("bmp: dimensions exceed viewer limit")
	}
	if binary.LittleEndian.Uint16(data[26:28]) != 1 {
		return nil, fmt.Errorf("bmp: invalid plane count")
	}
	bpp := binary.LittleEndian.Uint16(data[28:30])
	if bpp != 4 && bpp != 8 && bpp != 24 && bpp != 32 {
		return nil, fmt.Errorf("bmp: unsupported bit depth %d", bpp)
	}
	if binary.LittleEndian.Uint32(data[30:34]) != 0 {
		return nil, fmt.Errorf("bmp: compressed images are unsupported")
	}
	if pixelOffset < 14+int64(dibSize) || pixelOffset >= int64(len(data)) {
		return nil, fmt.Errorf("bmp: invalid pixel offset")
	}

	var palette []color.RGBA
	if bpp == 4 || bpp == 8 {
		paletteOffset := int64(14) + int64(dibSize)
		colors := binary.LittleEndian.Uint32(data[46:50])
		if colors == 0 {
			colors = 1 << bpp
		}
		maxColors := uint32(1 << bpp)
		if colors > maxColors || paletteOffset+int64(colors)*4 > int64(len(data)) || paletteOffset+int64(colors)*4 > pixelOffset {
			return nil, fmt.Errorf("bmp: invalid color table")
		}
		palette = make([]color.RGBA, colors)
		for n := range palette {
			offset := paletteOffset + int64(n)*4
			palette[n] = color.RGBA{data[offset+2], data[offset+1], data[offset], 255}
		}
	}
	rowBytes := ((int64(width)*int64(bpp) + 31) / 32) * 4
	if pixelOffset+rowBytes*height > int64(len(data)) {
		return nil, fmt.Errorf("bmp: pixel data is truncated")
	}
	im := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := int64(0); y < height; y++ {
		row := y
		if !topDown {
			row = height - 1 - y
		}
		base := pixelOffset + row*rowBytes
		for x := int64(0); x < width; x++ {
			var pixel color.RGBA
			switch bpp {
			case 4:
				value := data[base+x/2]
				index := value & 0x0f
				if x&1 == 0 {
					index = value >> 4
				}
				if int(index) >= len(palette) {
					return nil, fmt.Errorf("bmp: palette index %d is out of range", index)
				}
				pixel = palette[index]
			case 8:
				index := data[base+x]
				if int(index) >= len(palette) {
					return nil, fmt.Errorf("bmp: palette index %d is out of range", index)
				}
				pixel = palette[index]
			case 24:
				offset := base + x*3
				pixel = color.RGBA{data[offset+2], data[offset+1], data[offset], 255}
			case 32:
				offset := base + x*4
				alpha := data[offset+3]
				if alpha == 0 {
					alpha = 255
				}
				pixel = color.RGBA{data[offset+2], data[offset+1], data[offset], alpha}
			}
			im.SetRGBA(int(x), int(y), pixel)
		}
	}
	return im, nil
}
