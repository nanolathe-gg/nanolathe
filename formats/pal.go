package formats

import (
	"fmt"
	"image/color"

	"github.com/nanolathe/nanolathe/vfs"
)

// Palette is a 256-entry indexed palette [fmt pal].
type Palette struct {
	Colors [256]color.RGBA
	Raw    []byte // owned source bytes, including reserved fourth bytes [fmt pal]
}

// LoadPAL decodes a palette from its bytes. Both the 768-byte RGB and the
// 1024-byte RGB/reserved layouts are accepted. The 768-byte form is an
// intentional import extension; retail reads four-byte entries [fmt pal].
func LoadPAL(data []byte) (*Palette, error) {
	if len(data) != 768 && len(data) != 1024 {
		return nil, fmt.Errorf("pal: expected 768 or 1024 bytes, got %d", len(data))
	}
	palette := &Palette{Raw: append([]byte(nil), data...)}
	stride := 3
	if len(data) == 1024 {
		stride = 4
	}
	for i := 0; i < 256; i++ {
		palette.Colors[i] = color.RGBA{R: data[i*stride], G: data[i*stride+1], B: data[i*stride+2], A: 255}
	}
	return palette, nil
}

// LoadPALFile reads and decodes a palette from the VFS.
func LoadPALFile(fs vfs.FSOps, name string) (*Palette, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadPAL(data)
}
