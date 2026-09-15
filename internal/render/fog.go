package render

// Fog presentation [03 §1][03 §3.3].
//
// Fog is presentation-only: it reads the published LOS mask via the snapshot
// / FogCache (I6) and never writes sim state. The ten-strip composer stages fog
// after world drawing but before selection/interface [03 §1] step 10 C1 C2.
// Visibility culling is binary and hard-edged on 32-pixel tiles [03 §3.3]; there
// is no ALP blend at the LOS edge and no intermediate opacity. Palette lookup
// uses the 256-byte logical→physical table at present time (C7) [03 §4.3]. The
// SHD row selection this file once flagged is established: the default is the
// DONT_SHADE pin, row 15 [03 R-RAST-01 §5], and no marker remains here.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// FogFrames is the resolved, immutable fog art supplied by the presentation
// asset catalog. A nil entry is an ordinary optional-resource miss.
type FogFrames struct {
	Gray  [4][]*formats.GAFFrame
	Black [4][]*formats.GAFFrame
}

// FogTilePixels is the hard fog tile size in map pixels [03 §3.3][03 §2.1].
// One visibility cell covers 32 world pixels and edges are hard [03 §3.3].
const FogTilePixels = 32

// FogTileWorld is the fog tile size in fixed world units [03 §2.1] (I2).
// One pixel is 65536 wu [03 §2.1], so 32 pixels is 2097152 wu.
const FogTileWorld = FogTilePixels * 65536 // 32*65536 [03 §2.1][03 §3.3]

// FogDarkPaletteIndex is the palette index for the unexplored solid fill
// (lo==15): retail uses the logical-to-physical mapping of logical index 0,
// which is black in stock PALETTE.PAL [03 §3.3].
// The fogged-but-explored fill (hi==15) is NOT a solid color: retail remaps the
// existing screen pixels through the 256-byte gray table, preserving terrain
// texture [03 §3.3]. See palette.Tables.Gray.
const FogDarkPaletteIndex byte = 0

// FogKind describes the draw kind for one fog cell operation [03 §3.3].
type FogKind uint8

const (
	FogKindNone      FogKind = iota // visible, no fog draw
	FogKindSolidDark                // lo==15 short-circuit solid black [03 §3.3]
	FogKindGrayRemap                // hi==15: remap existing pixels through GRAY TABLE [03 §3.3]
	FogKindPatterned                // hi==15 dithered: black checker dots [03 §3.3]
	FogKindGAFCh1                   // hi 1..14 Gray family GAF [03 §3.3]
	FogKindGAFCh0                   // lo 1..14 Black family GAF [03 §3.3]
)

// FogOp is one deterministic fog draw operation for a cell [03 §3.3] (I6).
// Presentation-only: constructing ops never mutates the visibility cache.
type FogOp struct {
	GridX, GridY       int32
	ScreenX0, ScreenY0 int32 // inclusive top-left in screen pixels [03 §2.5][03 §3.3]
	ScreenX1, ScreenY1 int32 // exclusive bottom-right (X0+32, Y0+32) hard edge [03 §3.3]
	Channel0, Channel1 uint8 // raw 0..15 values [03 §3.3]
	Kind               FogKind
	Variant            int   // 0..3 when Kind is GAF, else -1 [03 §3.3]
	Frame              int   // value-1 when GAF, else -1 [03 §3.3]
	Patterned          bool  // dither checker when applicable [03 §3.3]
	R, G, B, A         uint8 // palette/SHD dark color or GAF-modulated (presentation) [03 §4.3]
	// Scale is the presentation view scale the rectangle was projected at, in
	// the camera's native and detail record scales (DESIGN_GPU_RENDERER §14.2). It is an additive
	// field, zero meaning native, and it is what tells an executor which
	// variant of the fog frame covers the scaled cell. The fills need only the
	// rectangle, which already carries the scale.
	Scale camera.ViewScale
}

// ViewScale is the op's view scale with its zero value read as the native
// scale: the cell rectangle is ViewScale.Px(32) on a side and the fog frame an
// executor draws into it is the frame's variant at that scale
// (DESIGN_GPU_RENDERER §14.2).
func (op FogOp) ViewScale() camera.ViewScale {
	return op.Scale.Norm()
}

// floorDiv returns floor(a/b) for b>0 with sign correction [03 §2.1][I3].
// Go's / truncates toward zero; fog alignment needs floor for negative camera
// residues including signed residues [03 §3.3].
func floorDiv(a, b int32) int32 {
	q := a / b
	r := a % b
	if r != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// FogTileForPixel returns the fog grid coordinate for a map pixel [03 §2.1][03 §3.3].
// One tile is 32 map pixels; alignment uses floor division for negative
// coordinates including signed residues [03 §2.1][I3].
func FogTileForPixel(px int32) int32 {
	return floorDiv(px, FogTilePixels)
}

// FogTileForWorld returns the fog grid coordinate for a world Fixed coordinate
// [03 §2.1][03 §3.3] (I2) (I3). The world value is narrowed to its signed high
// word (map pixel) before the shift by five [03 §3.2] "pixel components".
func FogTileForWorld(world numeric.Fixed) int32 {
	px := int32(int64(world) >> 16) // high word [03 §3.2]
	return FogTileForPixel(px)
}

// FogScreenRect returns the hard-edged 32x32 screen rectangle for grid cell
// (gx,gy) at camera cam [03 §3.3][03 §2.5]. The rectangle is camera-aligned
// including signed residues [03 §3.3] and has hard 32-pixel edges with no blend.
// Screen origin uses the orthographic beam projection offsets [03 §2.5].
//
// Fog cells straddle visibility-tile corners: retail draws cell gx at
// sx = vpLeft + offX + (gx-startX)*32 with startX=floorDiv(camX-16,32) and
// offX=(res<16?-16:+16)-res, which algebraically reduces to map pixel
// gx*32+16 for every camera residue [03 §3.3]. Each
// cell is therefore centred on the corner where tiles (gx,gy), (gx-1,gy),
// (gx,gy-1), (gx-1,gy-1) meet — exactly the four tiles the 4-bit nibble
// accumulates [03 §3.3].
//
// The rectangle is the cell's map-pixel origin through the same projection every
// other world layer uses, so at the presentation view scale its edge is
// Px(32) — 32, 48 or 64 — and its corner is the projected corner [F-P1-008]
// (DESIGN_GPU_RENDERER §14.2). At the native scale the expression is the
// original one, unchanged.
func FogScreenRect(cam *camera.Camera, gx, gy int32) (x0, y0, x1, y1 int32) {
	var camX, camZ int32
	scale := camera.ViewScaleNative
	if cam != nil {
		camX = cam.X
		camZ = cam.Z
		scale = cam.EffectiveScale()
	}
	x0 = scale.Project(gx*FogTilePixels+FogTilePixels/2-camX) + camera.OriginX // [03 §2.5][03 §3.3]
	y0 = scale.Project(gy*FogTilePixels+FogTilePixels/2-camZ) + camera.OriginY
	x1 = x0 + scale.Px(FogTilePixels) // hard 32 world pixels [03 §3.3]
	y1 = y0 + scale.Px(FogTilePixels)
	return x0, y0, x1, y1
}

// FogVariant returns the four-way variant selector for a cell [03 §3.3].
//
// Retail computes variant = (col + row + camPhase) & 3 over cache-relative
// col/row with camPhase = floorDiv(camX+16,32)+floorDiv(camZ+16,32)
// [03 §3.3]. The producer's cache origin is `floorDiv(camX-16,32)`, and the two
// floorDiv arguments
// differ by exactly 32, so floorDiv(camX+16,32) − floorDiv(camX−16,32) = 1 for
// every camera position. Substituting col = gx − startX:
//
//	variant = (gx + gy + 2) & 3   (map-global coordinates, camera-independent)
//
// Nanolathe's cache is map-sized (col = gx), so the camera phase cancels
// exactly; the four-way cloud tiling is anchored to the world grid and must
// NOT rotate as the camera pans.
func FogVariant(gx, gy int32, cam *camera.Camera) int {
	_ = cam // camera phase cancels for map-global cells; see doc comment
	return int((gx + gy + 2) & 3)
}

// FogDarkRGBA returns the RGBA for the default dark fog fill via the palette
// logical→physical lookup at present time [03 §4.3] C7 (I6).
// It never mutates the palette; it is presentation-only.
func FogDarkRGBA(tables *palette.Tables) (r, g, b, a uint8) {
	if tables == nil {
		return 0, 0, 0, 255
	}
	return tables.RGBA(FogDarkPaletteIndex) // C7 logical→physical [03 §4.3]
}

// cellOpsInto builds the fog ops for a single cell (gx,gy) with raw channels
// c0,c1 [03 §3.3]. It never mutates the cache (I6). Ordering is channel one
// BEFORE channel zero [03 §3.3]. Channel zero ==15 short-circuits the cell
// [03 §3.3].
func cellOpsInto(ops []FogOp, gx, gy int32, c0, c1 uint8, cam *camera.Camera, tables *palette.Tables, dither bool) []FogOp {
	x0, y0, x1, y1 := FogScreenRect(cam, gx, gy) // hard 32 world pixels [03 §3.3]
	dr, dg, db, da := FogDarkRGBA(tables)        // palette [03 §4.3] C7
	// Every op of this cell carries the scale it was projected at, so an
	// executor tiles the native fog frame across the scaled rectangle
	// (DESIGN_GPU_RENDERER §14.2).
	scale := camera.ViewScaleNative
	if cam != nil {
		scale = cam.EffectiveScale()
	}

	// Channel zero ==15: fill whole 32x32 with default dark; nothing else [03 §3.3].
	if c0 == 15 {
		return append(ops, FogOp{
			GridX: gx, GridY: gy,
			ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1,
			Channel0: c0, Channel1: c1,
			Kind:    FogKindSolidDark,
			Variant: -1, Frame: -1,
			R: dr, G: dg, B: db, A: da,
			Scale: scale,
		})
	}

	// Channel one handling before channel zero [03 §3.3].
	if c1 == 15 {
		// The DitheredFog option selects black checker pixels instead of a gray
		// table remap; camera parity is
		// only the checker phase, never the selector [03 §3.3].
		kind := FogKindGrayRemap
		patterned := dither
		if patterned {
			kind = FogKindPatterned
		}
		ops = append(ops, FogOp{
			GridX: gx, GridY: gy,
			ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1,
			Channel0: c0, Channel1: c1,
			Kind: kind, Variant: -1, Frame: -1,
			Patterned: patterned,
			R:         dr, G: dg, B: db, A: da,
			Scale: scale,
		})
	} else if c1 >= 1 && c1 <= 14 {
		// GAF frame value-1 from four-way variant family selected by (col+row+camPhase)&3 [03 §3.3].
		variant := FogVariant(gx, gy, cam)
		frame := int(c1) - 1
		// The same option selects checker-masked rather than plain GAF drawing;
		// parity is the checker phase [03 §3.3].
		patterned := dither
		ops = append(ops, FogOp{
			GridX: gx, GridY: gy,
			ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1,
			Channel0: c0, Channel1: c1,
			Kind: FogKindGAFCh1, Variant: variant, Frame: frame,
			Patterned: patterned,
			R:         dr, G: dg, B: db, A: da,
			Scale: scale,
		})
	}

	// Then channel zero in 1..14: frame value-1 from second four-way family via plain blitter [03 §3.3].
	// Channel one renders BEFORE channel zero, so this op comes after [03 §3.3].
	if c0 >= 1 && c0 <= 14 {
		variant := FogVariant(gx, gy, cam) // second family same selector, distinct Kind
		frame := int(c0) - 1
		ops = append(ops, FogOp{
			GridX: gx, GridY: gy,
			ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1,
			Channel0: c0, Channel1: c1,
			Kind: FogKindGAFCh0, Variant: variant, Frame: frame,
			R: dr, G: dg, B: db, A: da,
			Scale: scale,
		})
	}

	if len(ops) == 0 {
		return ops[:0] // visible [03 §3.3]
	}
	return ops
}

// BuildFogOpsInto is the reusable-scratch variant for the live client frame
// path. It preserves row-major operation order while avoiding an operation
// slice allocation after warmup [03 §3.3][I1].
func BuildFogOpsInto(out []FogOp, cache *visibility.FogCache, cam *camera.Camera, viewW, viewH int32, gridW, gridH int32, tables *palette.Tables, dither bool) []FogOp {
	if cache == nil {
		return out[:0]
	}
	// FogCache is the sole producer of viewport nibbles. This function only
	// translates the already-aligned cache to ordered blits; a zero origin is
	// valid and is not a sentinel for a map-sized cache. [03 §3.3]
	_ = viewW
	_ = viewH
	_ = gridW
	_ = gridH
	ox, oz := cache.Origin()
	w, h := cache.Dimensions()
	if w <= 0 || h <= 0 {
		return out[:0]
	}
	out = out[:0]
	if cap(out) < int(w*h) {
		out = make([]FogOp, 0, int(w*h))
	}
	for row := int32(0); row < h; row++ {
		for col := int32(0); col < w; col++ {
			c0, c1 := cache.Channel(col, row)
			if c0 == 0 && c1 == 0 {
				continue
			}
			out = cellOpsInto(out, ox+col, oz+row, c0, c1, cam, tables, dither)
		}
	}
	return out
}

// BuildFogOpsWindowInto is BuildFogOpsInto restricted to the cells that can
// land on a surface of surfW x surfH pixels.
//
// The fog cache is map-sized, so BuildFogOpsInto emits one operation for every
// fogged cell of the whole map -- better than seventeen thousand of them on a
// stock map -- and the composer then clips all but the few hundred that touch
// the viewport. Building and discarding the rest is pure waste: the composer's
// clip already reduces an off-surface operation to nothing, so not emitting it
// paints exactly the same pixels.
//
// The surface rectangle is the composer's, not the camera's: the composer
// rebases every operation off the retail viewport origin before clipping, and
// FogScreenRect adds that same origin, so the two cancel and a cell's rebased
// rect is [gx*32 + 16 - camX, +32) by [gy*32 + 16 - camZ, +32). A cell survives
// exactly when that rect overlaps [0, surfW) x [0, surfH) -- the same test the
// composer's clip applies -- and the survivors keep their row-major order, so
// the paint sequence is unchanged [03 §3.3][I1].
//
// BuildFogOpsInto is left alone: it is the unwindowed contract the render tests
// drive, several of which pass a zero viewport.
func BuildFogOpsWindowInto(out []FogOp, cache *visibility.FogCache, cam *camera.Camera, surfW, surfH int32, tables *palette.Tables, dither bool) []FogOp {
	out = out[:0]
	if cache == nil || surfW <= 0 || surfH <= 0 {
		return out
	}
	ox, oz := cache.Origin()
	w, h := cache.Dimensions()
	if w <= 0 || h <= 0 {
		return out
	}
	var camX, camZ int32
	scale := camera.ViewScaleNative
	if cam != nil {
		camX, camZ = cam.X, cam.Z
		scale = cam.EffectiveScale()
	}
	// The window test is in SCREEN pixels: the composer's rebase and
	// FogScreenRect's origin still cancel, but the cell's rebased rect is
	// [Project(gx*32 + 16 - camX), +Px(32)) at the view scale, so both the
	// offset and the edge carry it (DESIGN_GPU_RENDERER §14.2).
	edge := scale.Px(FogTilePixels)
	for row := int32(0); row < h; row++ {
		// The row test is hoisted out of the column walk: a fog grid is far
		// taller than a viewport, so most rows are rejected by one comparison
		// instead of by one per cell.
		y0 := scale.Project((oz+row)*FogTilePixels + FogTilePixels/2 - camZ)
		if y0+edge <= 0 || y0 >= surfH {
			continue
		}
		for col := int32(0); col < w; col++ {
			x0 := scale.Project((ox+col)*FogTilePixels + FogTilePixels/2 - camX)
			if x0+edge <= 0 || x0 >= surfW {
				continue
			}
			c0, c1 := cache.Channel(col, row)
			if c0 == 0 && c1 == 0 {
				continue
			}
			out = cellOpsInto(out, ox+col, oz+row, c0, c1, cam, tables, dither)
		}
	}
	return out
}
