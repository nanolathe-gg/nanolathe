package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

// tables holds the palette lookup textures the modern executor samples, uploaded
// once from the immutable-after-load palette tables (docs/DESIGN_GPU_RENDERER.md
// §2.3, C-G4, §11.2 "One table atlas"). Two storage conventions live here, and
// the difference is the whole point of C-G4/C-G8:
//
//   - PAL is the colour table: each texel the actual RGB of that index with alpha
//     forced opaque. Every fragment that writes the composite resolves its index
//     through this row as it writes, which is the retail composite's own final
//     lookup done per fragment instead of once per frame in an expansion pass
//     (C-G8 as amended, §13.3).
//   - ALP, LHT, SHD, Gray and Blue are index→index remap tables. Their values are
//     palette indices, so each is stored with the index byte in the red channel
//     and G/B/A carrying no meaning (C-G4).
//
// # The table atlas
//
// Every table is packed into ONE 256-wide RGBA8 image at fixed row offsets, so a
// shader that needs any of them binds a single source image and leaves the other
// three Ebitengine image slots to the scene atlas, the model lane's atlas pages
// and the terrain tile atlas (§11.2 "One table atlas", §22). The layout is:
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
// policy"). The atlas is the ONLY table image: every pass that once bound a
// single table now addresses it by row, so no per-table texture is uploaded.
type tables struct {
	// atlas is the packed table image described above (§11.2). Every pass that
	// reads a palette table binds it, which is what frees the other three
	// Ebitengine image slots for the scene atlas, the model lane's atlas pages
	// and the terrain tile atlas.
	atlas *ebiten.Image
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

// uploadTables builds the palette table atlas from pal. It is called once per
// renderer. A nil pal yields a nil handle; the caller guards on tables.atlas
// before binding it, so a renderer built before the palette is installed simply
// resolves no index rather than panicking.
func uploadTables(pal *palette.Tables) tables {
	if pal == nil {
		return tables{}
	}
	return tables{atlas: uploadTableAtlas(pal)}
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

// setDisplayPalette replaces only the colour row; the surrounding atlas rows
// remain index-to-index lookup tables [07 R-FE-01 §11].
func (t *tables) setDisplayPalette(p [256][4]byte) {
	var pixels [256 * 4]byte
	for i, entry := range p {
		copy(pixels[i*4:i*4+3], entry[:3])
		pixels[i*4+3] = 255
	}
	t.atlas.SubImage(image.Rect(0, tableRowPAL, 256, tableRowPAL+1)).(*ebiten.Image).WritePixels(pixels[:])
}
