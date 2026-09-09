package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

// tables holds the palette lookup textures the modern executor samples, uploaded
// once from the immutable-after-load palette tables (docs/DESIGN_GPU_RENDERER.md
// §2.3, C-G4, §11.2 "One table atlas"). Two storage conventions live here, and
// the difference is the whole point of C-G4/C-G8:
//
//   - PAL is the colour table: each texel the actual RGB of that index with alpha
//     forced opaque. The expansion pass reads a colour from it (C-G8).
//   - ALP, LHT, SHD, Gray and Blue are index→index remap tables. Their values are
//     palette indices, so each is stored with the index byte in the red channel
//     and G/B/A carrying no meaning (C-G4).
//
// # The table atlas
//
// Every table is packed into ONE 256-wide RGBA8 image at fixed row offsets, so a
// shader that needs any of them binds a single source image and leaves the other
// three Ebitengine image slots to the scene atlas, the phase snapshot and the
// model slot atlas (§11.2 "One table atlas"). The layout is:
//
//	rows   0..255  ALP    texel (col=dst, row=src) = Alpha[src*256 + dst]
//	row      256   PAL    texel (col=index)        = the index's RGB, alpha 255
//	rows 257..288  SHD    texel (col=index, row=257+shadeRow)
//	rows 289..320  LHT    texel (col=index, row=289+lightRow)
//	row      321   Gray   texel (col=index)        = GRAY TABLE[index]
//	row      322   Blue   texel (col=index)        = BLUE TABLE[index]
//
// A shader therefore addresses one entry as (index, tableRowX + row); the row
// base rides the vertex lanes, so no draw needs a uniform (§11.2 "Allocation
// policy"). The per-table images below are kept beside the atlas because the
// model rasterization passes and the fog pass bind single tables directly.
type tables struct {
	// atlas is the packed table image described above (§11.2). It is the only
	// image the expansion and the two scene passes bind; the single-table images
	// below serve the model rasterization passes and the fog pass, which bind one
	// table directly.
	atlas *ebiten.Image
	alpha *ebiten.Image // PALETTE.ALP, 256×256, index in red (C-G4)
	light *ebiten.Image // PALETTE.LHT, 256×32, index in red (C-G4)
	shade *ebiten.Image // PALETTE.SHD, 256×32, index in red (C-G4)
	gray  *ebiten.Image // GRAY TABLE, 256×1, index in red (C-G4)
	blue  *ebiten.Image // BLUE TABLE, 256×1, index in red (C-G4)
}

// The table atlas row offsets. They are shader constants as well as Go
// constants: the shader sources are built from these values so the two can
// never drift (§11.2 "One table atlas").
const (
	tableAtlasW = 256
	// tableRowALP is row 0 because ALP is addressed as (dst, src) with src
	// spanning the whole 0..255 range; every other table sits above it.
	tableRowALP  = 0
	tableRowPAL  = 256
	tableRowSHD  = 257
	tableRowLHT  = 289
	tableRowGray = 321
	tableRowBlue = 322
	tableAtlasH  = 323
)

// uploadTables builds every palette texture from pal. It is called once per
// renderer. A nil pal yields nil handles; the caller guards on tables.atlas
// before expanding, so a renderer built before the palette is installed simply
// cannot expand rather than panicking.
func uploadTables(pal *palette.Tables) tables {
	if pal == nil {
		return tables{}
	}
	return tables{
		atlas: uploadTableAtlas(pal),
		alpha: uploadAlpha(pal),
		light: uploadLight(pal),
		shade: uploadShade(pal),
		gray:  uploadRedTable256x1(&pal.Gray),
		blue:  uploadRedTable256x1(&pal.Blue),
	}
}

// uploadTableAtlas packs ALP, PAL, SHD, LHT, GRAY and BLUE into one image at the
// row offsets documented above (§11.2 "One table atlas"). Index tables store the
// remapped index in red; PAL stores its RGB. Alpha is forced opaque throughout so
// premultiplied sampling recovers the stored bytes exactly (C-G4, C-G8).
func uploadTableAtlas(pal *palette.Tables) *ebiten.Image {
	buf := make([]byte, tableAtlasW*tableAtlasH*4)
	put := func(col, row int, r, g, b byte) {
		p := (row*tableAtlasW + col) * 4
		buf[p+0], buf[p+1], buf[p+2], buf[p+3] = r, g, b, 255
	}
	for src := 0; src < 256; src++ {
		for dst := 0; dst < 256; dst++ {
			put(dst, tableRowALP+src, pal.Alpha[src*256+dst], 0, 0)
		}
	}
	for i := 0; i < 256; i++ {
		e := pal.Base[i]
		put(i, tableRowPAL, e[0], e[1], e[2])
		put(i, tableRowGray, pal.Gray[i], 0, 0)
		put(i, tableRowBlue, pal.Blue[i], 0, 0)
	}
	for row := 0; row < 32; row++ {
		for col := 0; col < 256; col++ {
			put(col, tableRowSHD+row, pal.Shade[row][col], 0, 0)
			put(col, tableRowLHT+row, pal.Light[row*256+col], 0, 0)
		}
	}
	img := ebiten.NewImage(tableAtlasW, tableAtlasH)
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
