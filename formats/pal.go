package formats

import (
	"fmt"
	"image/color"

	"github.com/nanolathe/nanolathe/vfs"
)

type Palette struct {
	Colors [256]color.RGBA
}

func LoadPAL(data []byte) (*Palette, error) {
	if len(data) != 768 && len(data) != 1024 {
		return nil, fmt.Errorf("pal: expected 768 or 1024 bytes, got %d", len(data))
	}
	palette := &Palette{}
	stride := 3
	if len(data) == 1024 {
		stride = 4
	}
	for i := 0; i < 256; i++ {
		palette.Colors[i] = color.RGBA{R: data[i*stride], G: data[i*stride+1], B: data[i*stride+2], A: 255}
	}
	return palette, nil
}

func LoadPALFile(fs vfs.FSOps, name string) (*Palette, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadPAL(data)
}

type PaletteTable struct {
	Rows, Columns int
	Data          []byte
}

func LoadPaletteTable(data []byte, rows, columns int) (*PaletteTable, error) {
	if rows <= 0 || columns <= 0 || len(data) != rows*columns {
		return nil, fmt.Errorf("pal: expected %d bytes, got %d", rows*columns, len(data))
	}
	return &PaletteTable{Rows: rows, Columns: columns, Data: append([]byte(nil), data...)}, nil
}

func LoadPaletteTableFile(fs vfs.FSOps, name string, rows, columns int) (*PaletteTable, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadPaletteTable(data, rows, columns)
}

// Shade returns the palette that results from viewing every entry through one
// row of a shade table. PALETTE.SHD is authored so its middle row reproduces
// the palette unchanged, lower rows darken, and higher rows brighten.
func (t *PaletteTable) Shade(palette *Palette, row int) *Palette {
	shaded := &Palette{}
	for index := range palette.Colors {
		shaded.Colors[index] = palette.Colors[t.At(row, index)]
	}
	return shaded
}

func (t *PaletteTable) At(row, column int) byte {
	if row < 0 || row >= t.Rows || column < 0 || column >= t.Columns {
		return 0
	}
	return t.Data[row*t.Columns+column]
}
