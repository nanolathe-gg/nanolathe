package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/palette"
)

// tables holds the palette lookup textures the modern executor samples, uploaded
// once from the immutable-after-load palette tables (docs/DESIGN_GPU_RENDERER.md
// §2.3, C-G4). Two storage conventions live here, and the difference is the
// whole point of C-G4/C-G8:
//
//   - pal is the colour table: PALETTE.PAL as 256×1 RGBA, each texel the actual
//     RGB of that index with alpha forced opaque. The expansion pass reads a
//     colour from it. It is the only table this unit uses (C-G8).
//   - alpha, light, shade, gray, blue are index→index remap tables. Their values
//     are palette indices, so each is stored with the index byte in the red
//     channel and G/B/A carrying no meaning (C-G4). Later units sample these; the
//     handles are uploaded now so the palette is read exactly once.
type tables struct {
	pal   *ebiten.Image // PALETTE.PAL colours, 256×1 RGBA (C-G8)
	alpha *ebiten.Image // PALETTE.ALP, 256×256, index in red (C-G4)
	light *ebiten.Image // PALETTE.LHT, 256×32, index in red (C-G4)
	shade *ebiten.Image // PALETTE.SHD, 256×32, index in red (C-G4)
	gray  *ebiten.Image // GRAY TABLE, 256×1, index in red (C-G4)
	blue  *ebiten.Image // BLUE TABLE, 256×1, index in red (C-G4)
}

// uploadTables builds every palette texture from pal. It is called once per
// renderer. A nil pal yields nil handles; the caller guards on tables.pal before
// expanding, so a renderer built before the palette is installed simply cannot
// expand rather than panicking.
func uploadTables(pal *palette.Tables) tables {
	if pal == nil {
		return tables{}
	}
	return tables{
		pal:   uploadPAL(pal),
		alpha: uploadAlpha(pal),
		light: uploadLight(pal),
		shade: uploadShade(pal),
		gray:  uploadRedTable256x1(&pal.Gray),
		blue:  uploadRedTable256x1(&pal.Blue),
	}
}

// uploadPAL stores PALETTE.PAL as a 256×1 RGBA colour table (C-G8). Alpha is
// forced opaque exactly as the software expansion does: PALETTE.PAL's fourth byte
// is a reserved zero, so a stored zero alpha would multiply the colour away under
// premultiplied sampling; forcing 255 keeps the colour and matches
// convertIndexedToRGBA's opaque rule.
func uploadPAL(pal *palette.Tables) *ebiten.Image {
	buf := make([]byte, 256*4)
	for i := 0; i < 256; i++ {
		e := pal.Base[i]
		buf[i*4+0] = e[0]
		buf[i*4+1] = e[1]
		buf[i*4+2] = e[2]
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(256, 1)
	img.WritePixels(buf)
	return img
}

// uploadAlpha stores PALETTE.ALP as a 256×256 texture with the remapped index in
// the red channel: texel (x, y) carries Alpha[y*256 + x] (C-G4).
func uploadAlpha(pal *palette.Tables) *ebiten.Image {
	buf := make([]byte, 256*256*4)
	for i := 0; i < 256*256; i++ {
		buf[i*4+0] = pal.Alpha[i]
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(256, 256)
	img.WritePixels(buf)
	return img
}

// uploadLight stores PALETTE.LHT as a 256×32 texture (256 indices wide, 32 rows
// tall) with the remapped index in the red channel: texel (col, row) carries
// Light[row*256 + col] (C-G4). Light is the brighten-only halo table [03 §4.3.1].
func uploadLight(pal *palette.Tables) *ebiten.Image {
	buf := make([]byte, 256*32*4)
	for i := 0; i < 256*32; i++ {
		buf[i*4+0] = pal.Light[i]
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(256, 32)
	img.WritePixels(buf)
	return img
}

// uploadShade stores PALETTE.SHD as a 256×32 texture (256 indices wide, 32 rows
// tall) with the remapped index in the red channel: texel (col, row) carries
// Shade[row][col] (C-G4). Shade is the full signed ramp [03 §4.3.2].
func uploadShade(pal *palette.Tables) *ebiten.Image {
	buf := make([]byte, 256*32*4)
	for row := 0; row < 32; row++ {
		for col := 0; col < 256; col++ {
			buf[(row*256+col)*4+0] = pal.Shade[row][col]
			buf[(row*256+col)*4+3] = 255
		}
	}
	img := ebiten.NewImage(256, 32)
	img.WritePixels(buf)
	return img
}

// uploadRedTable256x1 stores a 256-entry index→index table (GRAY or BLUE) as a
// 256×1 texture with the remapped index in the red channel (C-G4).
func uploadRedTable256x1(table *[256]byte) *ebiten.Image {
	buf := make([]byte, 256*4)
	for i := 0; i < 256; i++ {
		buf[i*4+0] = table[i]
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(256, 1)
	img.WritePixels(buf)
	return img
}
