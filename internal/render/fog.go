// Package render implements fog presentation [03 §1][03 §3.3].
//
// Fog is presentation-only: it reads the published LOS mask via the snapshot
// / FogCache (I6) and never writes sim state. The ten-strip composer stages fog
// after world drawing but before selection/interface [03 §1] step 10 C1 C2.
// Visibility culling is binary and hard-edged on 32-pixel tiles [03 §3.3]; there
// is no ALP blend at the LOS edge and no intermediate opacity. Palette lookup
// uses the 256-byte logical→physical table at present time (C7) [03 §4.3]; SHD
// darkening is noted as TODO(question) where the row selection is not
// established.
package render

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// FogTilePixels is the hard fog tile size in map pixels [03 §3.3][03 §2.1].
// One visibility cell covers 32 world pixels and edges are hard [03 §3.3].
const FogTilePixels = 32

// FogTileWorld is the fog tile size in fixed world units [03 §2.1] (I2).
// One pixel is 65536 wu [03 §2.1], so 32 pixels is 2097152 wu.
const FogTileWorld = FogTilePixels * 65536 // 32*65536 [03 §2.1][03 §3.3]

// FogDarkPaletteIndex is the palette index for the unexplored solid fill
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// mapping of logical index 0 — black in stock PALETTE.PAL [rr-16 §8]
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// The fogged-but-explored fill (hi==15) is NOT a solid color: retail remaps the
// existing screen pixels through the 256-byte "GRAY TABLE" LUT via
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const FogDarkPaletteIndex byte = 0

// FogKind describes the draw kind for one fog cell operation [03 §3.3].
type FogKind uint8

const (
	FogKindNone      FogKind = iota // visible, no fog draw
	FogKindSolidDark                // lo==15 short-circuit solid black [rr-16 §8]
	FogKindGrayRemap                // hi==15: remap existing pixels through GRAY TABLE [rr-16 §8]
	FogKindPatterned                // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	FogKindGAFCh1                   // hi 1..14 Gray family GAF [rr-16 §8]
	FogKindGAFCh0                   // lo 1..14 Black family GAF [rr-16 §8]
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// cell is therefore centred on the corner where tiles (gx,gy), (gx-1,gy),
// (gx,gy-1), (gx-1,gy-1) meet — exactly the four tiles the 4-bit nibble
// accumulates [rr-16 §6.1].
func FogScreenRect(cam *camera.Camera, gx, gy int32) (x0, y0, x1, y1 int32) {
	var camX, camZ int32
	if cam != nil {
		camX = cam.X
		camZ = cam.Z
	}
	x0 = gx*FogTilePixels + FogTilePixels/2 - camX + camera.OriginX // [03 §2.5][03 §3.3][rr-16 §7]
	y0 = gy*FogTilePixels + FogTilePixels/2 - camZ + camera.OriginY
	x1 = x0 + FogTilePixels // hard 32 [03 §3.3]
	y1 = y0 + FogTilePixels
	return x0, y0, x1, y1
}

// FogVariant returns the four-way variant selector for a cell [rr-16 §6.2].
//
// Retail computes variant = (col + row + camPhase) & 3 over cache-relative
// col/row with camPhase = floorDiv(camX+16,32)+floorDiv(camZ+16,32)
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// startX = floorDiv(camX-16,32) [rr-16 §6.1], and the two floorDiv arguments
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

// FogSHDDarkRGBA is a placeholder for SHD-based darkening.
// Research notes SHD 32 rows for shading/darkening [03 §4.3] and the 32-row
// table but states the exact SHD row selection formula is not established
// [PLAN_13 Explicit unknowns] TODO(question).
// This helper returns the same as FogDarkRGBA using the identity mid row
// until the row selection is traced.
func FogSHDDarkRGBA(tables *palette.Tables) (r, g, b, a uint8) {
	// TODO(question): SHD row selection not established [03 §4.3]; use mid row 16 placeholder.
	_ = tables
	return FogDarkRGBA(tables)
}

// cellOps builds the fog ops for a single cell (gx,gy) with raw channels c0,c1
// [03 §3.3]. It never mutates the cache (I6). Ordering is channel one BEFORE
// channel zero [03 §3.3]. Channel zero ==15 short-circuits the cell [03 §3.3].
func cellOps(gx, gy int32, c0, c1 uint8, cam *camera.Camera, tables *palette.Tables, dither bool) []FogOp {
	x0, y0, x1, y1 := FogScreenRect(cam, gx, gy) // hard 32 [03 §3.3]
	dr, dg, db, da := FogDarkRGBA(tables)        // palette [03 §4.3] C7

	// Channel zero ==15: fill whole 32x32 with default dark; nothing else [03 §3.3].
	if c0 == 15 {
		return []FogOp{{
			GridX: gx, GridY: gy,
			ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1,
			Channel0: c0, Channel1: c1,
			Kind:    FogKindSolidDark,
			Variant: -1, Frame: -1,
			R: dr, G: dg, B: db, A: da,
		}}
	}

	var ops []FogOp
	// Channel one handling before channel zero [03 §3.3].
	if c1 == 15 {
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		})
	} else if c1 >= 1 && c1 <= 14 {
		// GAF frame value-1 from four-way variant family selected by (col+row+camPhase)&3 [rr-16 §6.2].
		variant := FogVariant(gx, gy, cam)
		frame := int(c1) - 1
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		patterned := dither
		ops = append(ops, FogOp{
			GridX: gx, GridY: gy,
			ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1,
			Channel0: c0, Channel1: c1,
			Kind: FogKindGAFCh1, Variant: variant, Frame: frame,
			Patterned: patterned,
			R:         dr, G: dg, B: db, A: da,
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
		})
	}

	if len(ops) == 0 {
		return nil // visible [03 §3.3]
	}
	return ops
}

// BuildFogOps constructs the deterministic, hard-edged fog overlay for the
// viewport defined by cam view size and grid dimensions [03 §3.3] (I6) (I1).
//
// It reads the published FogCache only via Channel (I6) and never mutates it.
// Iteration is row-major (y outer, x inner) for determinism [I1]. Each cell's
// rectangle is hard 32x32 aligned including signed residues [03 §3.3]. Palette
// darkening uses the logical→physical lookup at present time [03 §4.3] C7.
//
// gridW, gridH are the visibility grid dimensions (W = CellW/2, H = CellH/2
// [03 §3.1]); when zero they are derived from cam.MapW/MapH when available.
// When cam is nil the full grid is enumerated row-major without viewport culling.
// dither selects the options-storage dither bit for patterned fills [03 §3.3].
func BuildFogOps(cache *visibility.FogCache, cam *camera.Camera, viewW, viewH int32, gridW, gridH int32, tables *palette.Tables, dither bool) []FogOp {
	if cache == nil {
		return nil
	}
	if gridW <= 0 || gridH <= 0 {
		if cam != nil && cam.MapW > 0 && cam.MapH > 0 {
			gridW = cam.MapW / FogTilePixels // MapW = CellW*16 = gridW*32 [03 §2.1][03 §3.1]
			gridH = cam.MapH / FogTilePixels
			if gridW <= 0 {
				gridW = cam.MapW / FogTilePixels
			}
			if gridH <= 0 {
				gridH = cam.MapH / FogTilePixels
			}
		}
		if gridW <= 0 || gridH <= 0 {
			return nil
		}
	}
	if viewW <= 0 && cam != nil {
		viewW = cam.ViewW
	}
	if viewH <= 0 && cam != nil {
		viewH = cam.ViewH
	}

	var startX, endX, startY, endY int32
	useViewport := cam != nil && viewW > 0 && viewH > 0
	if useViewport {
		// Align to camera in 32-pixel cells including signed residues [03 §3.3].
		// screenX0 = gx*32 - camX + OriginX ; visible when intersects [0,viewW)
		// => gx*32 ∈ [camX-OriginX -31, camX-OriginX+viewW)
		// Compute conservative range via floorDiv then clip to grid [I3].
		startX = floorDiv(cam.X-camera.OriginX, FogTilePixels)
		endX = floorDiv(cam.X-camera.OriginX+viewW+FogTilePixels-1, FogTilePixels) // ceil
		startY = floorDiv(cam.Z-camera.OriginY, FogTilePixels)
		endY = floorDiv(cam.Z-camera.OriginY+viewH+FogTilePixels-1, FogTilePixels)
		if startX < 0 {
			startX = 0
		}
		if startY < 0 {
			startY = 0
		}
		if endX > gridW {
			endX = gridW
		}
		if endY > gridH {
			endY = gridH
		}
		if startX > endX {
			startX = endX
		}
		if startY > endY {
			startY = endY
		}
	} else {
		startX = 0
		endX = gridW
		startY = 0
		endY = gridH
	}

	var out []FogOp
	// Deterministic row-major iteration [I1]: y outer, x inner.
	for gy := startY; gy < endY; gy++ {
		for gx := startX; gx < endX; gx++ {
			c0, c1 := cache.Channel(gx, gy) // read-only [I6]; out of bounds returns 0,0
			// Channel is presentation-only 0..15 [03 §3.3] C13.
			if c0 == 0 && c1 == 0 {
				continue // visible, no fog draw [03 §3.3]
			}
			ops := cellOps(gx, gy, c0, c1, cam, tables, dither)
			if len(ops) > 0 {
				out = append(out, ops...)
			}
		}
	}
	return out
}

// FogHook returns a composer hook closure that draws the fog overlay after
// world strips but before selection/interface [03 §1] step 10 C1 C2.
//
// It captures cache, camera, palette and dither bit and builds ops
// deterministically each invocation without mutating sim state (I6).
// The hook is intended for assignment to Composer.Hooks.Fog [03 §1].
func FogHook(cache *visibility.FogCache, cam *camera.Camera, gridW, gridH int32, tables *palette.Tables, dither bool) func() {
	return func() {
		if cache == nil || cam == nil {
			return
		}
		ops := BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, gridW, gridH, tables, dither)
		_ = ops // presentation blit omitted in headless unit; ordering and determinism are locked by BuildFogOps [03 §1][I6]
	}
}

// FogComposerHook is an alias for FogHook for API compatibility with WU-13-1
// seam naming [03 §1]. The composer's fog hook is the seam [PLAN_13 WU-13-5].
func FogComposerHook(cache *visibility.FogCache, cam *camera.Camera, gridW, gridH int32, tables *palette.Tables, dither bool) func() {
	return FogHook(cache, cam, gridW, gridH, tables, dither)
}
