package client

// Terrain draw — the tile blitter with its source block plus intra-tile remainder.
//
// Terrain draw is the orthographic tile pass [03 §2.2] [03 §2.5] C1. The tile map
// is row-major CellW/2 × CellH/2 16-bit indices into a 32×32 indexed tile set
// (1,024 bytes per tile). The blitter computes source block plus intra-tile pixel
// remainder, clips at map bounds, and handles partial edge rectangles — no depth
// buffer, no water mesh [03 §2.2]. Projection is integer orthographic with half-
// height shear: screenX = (worldX>>16)-cameraX+originX,
// screenY = (worldZ>>16)-((worldY>>16)>>1)-cameraZ+originY [03 §2.5] C1. For
// terrain at ground height the shear term is zero; the general form is kept so
// height-aware consumers share the same helper (camera.WorldToScreen). Palette
// lookups are logical→physical at present time only (C7) and happen in the
// indexed→RGBA conversion, not in this blitter — this file writes palette
// indices only, so palette animation stays possible.

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

const (
	terrainTileSize   = 32   // pixels per tile side [03 §2.1] C1
	terrainTilePixels = 1024 // 32×32 [03 §2.2] C5
	// detailTileSize and detailTilePixels are the 2x tile of the detail view
	// (DESIGN_GPU_RENDERER §14.3): one 64x64 index tile per TileSet entry.
	// detailTilePixels is drawlist.DetailTilePixels, so the two tile types are
	// the same Go type and a tile set passes across the record unconverted.
	detailTileSize   = terrainTileSize * 2
	detailTilePixels = drawlist.DetailTilePixels
)

// floorDiv returns floor(a/b) with sign correction [INVARIANTS I3] [03 §2.1].
func terrainFloorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// DrawTerrain draws terrain into the client's indexed framebuffer [03 §2.2] C1.
//
// It blits the world Terrain's TileIndices/TileSet through the camera pan,
// clipping at map bounds and handling partial edge rectangles. The destination
// is c.indexed at c.width×c.height. A nil terrain or camera clears the buffer
// to 0 (void). The write is palette indices; conversion through palette.Tables
// happens at present time in convertIndexedToRGBA (C7).
//
// Viewport origin for the shell window is 0,0 (full-window terrain). The
// underlying projection still uses camera.WorldToScreen [03 §2.5] C1 with its
// 128,32 beam offsets; the blitter subtracts those offsets to obtain the
// shell origin so the scale/shear stay general and only the offset is
// viewport-specific (PLAN_04A C1).
func (c *Client) DrawTerrain(t *world.Terrain, cam *camera.Camera) {
	if c == nil {
		return
	}
	BlitTerrain(c.indexed, c.width, c.height, t, cam)
}

// BlitTerrain blits terrain into dst (row-major dstW×dstH indexed pixels) [03 §2.2].
//
// dst is filled with 0 (void) where no map tile covers the viewport. Visible
// tiles are copied with source block plus intra-tile remainder: for each tile
// intersecting the viewport the blitter computes its screen rectangle via
// camera.WorldToScreen [03 §2.5], clips that rectangle at viewport and map
// bounds, derives the intra-tile source offset (remainder), and copies the
// partial edge rectangle row by row with nearest-neighbour sampling (the
// texture upload already uses TextureFilterNearest).
//
// cam is in map pixels [camera.Camera]. A nil cam is treated as at 0,0.
// t may be nil; the function then only clears dst.
func BlitTerrain(dst []uint8, dstW, dstH int, t *world.Terrain, cam *camera.Camera) {
	BlitTerrainOrigin(dst, dstW, dstH, t, cam, 0, 0)
}

// BlitTerrainDetail is BlitTerrain with the detail tile set of
// DESIGN_GPU_RENDERER §14.3. At the detail scale a non-nil detail slice
// supplies one 64x64 tile per TileSet entry, in the same order, and the
// blitter copies it one-to-one; a nil slice, a short slice or a nil entry
// falls back to doubling the 32x32 tile by nearest sampling. At the native
// scale the detail tiles are unused and this is exactly BlitTerrain.
func BlitTerrainDetail(dst []uint8, dstW, dstH int, t *world.Terrain, cam *camera.Camera, detail [][detailTilePixels]byte) {
	blitTerrain(dst, dstW, dstH, t, cam, 0, 0, detail)
}

// BlitTerrainOrigin is BlitTerrain with an explicit viewport origin. originX/Y
// is the screen offset added after the camera subtraction — the viewport-
// specific originX/Y of [03 §2.5] C1. The shell window uses 0,0; callers that
// want the observed beam offsets can pass camera.OriginX/Y.
//
// The general projection per tile origin (tx*32, tz*32) at height Y=0 is:
//
//	screenX = (worldX>>16) - cam.X + originX
//	screenY = (worldZ>>16) - ((worldY>>16)>>1) - cam.Z + originY
//
// where worldX = tileMapX*65536 etc [03 §2.5]. The blitter obtains that
// position through cam.WorldToScreen and then re-bases from the 128,32 offsets
// to the requested origin, so call sites do not hard-code literals (PLAN_04A C1).
func BlitTerrainOrigin(dst []uint8, dstW, dstH int, t *world.Terrain, cam *camera.Camera, originX, originY int32) {
	blitTerrain(dst, dstW, dstH, t, cam, originX, originY, nil)
}

func blitTerrain(dst []uint8, dstW, dstH int, t *world.Terrain, cam *camera.Camera, originX, originY int32, detail [][detailTilePixels]byte) {
	if dstW <= 0 || dstH <= 0 || len(dst) < dstW*dstH {
		return
	}
	// Clear to void/background index 0. Out-of-map regions stay 0 (clipped at
	// map bounds [03 §2.2]).
	//
	// The builtin is used rather than a range loop because the loop this
	// replaced ranged over `dst[:dstW*dstH]` while storing through `dst`, and
	// the compiler recognises its zeroing idiom only when the ranged expression
	// and the stored one are the same slice. Written that way it was a
	// bytewise, bounds-checked walk: it cost three times as much as every tile
	// copy in this function put together, 0.141 ms against 0.043 ms at 800x600,
	// and was the largest single item in the whole compose. The bytes zeroed
	// are the same ones.
	clear(dst[:dstW*dstH])
	if t == nil || t.TileIndices == nil || len(t.TileSet) == 0 {
		return
	}
	if t.CellW <= 0 || t.CellH <= 0 {
		return
	}
	// Map pixel dimensions: CellW*16 x CellH*16 [03 §2.1]. Tile map is
	// CellW/2 × CellH/2 tiles of 32×32 [03 §2.2] C5 — both products agree.
	tileMapW := int(t.CellW / 2)
	tileMapH := int(t.CellH / 2)
	if tileMapW <= 0 || tileMapH <= 0 {
		return
	}
	if len(t.TileIndices) < tileMapW*tileMapH {
		// Corrupt terrain; behave as empty.
		return
	}
	var camX, camZ int32
	var scale int32 = 1
	if cam != nil {
		camX = cam.X
		camZ = cam.Z
		scale = cam.EffectiveScale()
	}
	// Visible map rectangle in map pixels for the viewport [originX/Y, originX+dstW).
	// For the half-height shear, terrain is at Y=0 so the shear term is zero
	// and the projection reduces to map-pixel translation; we keep the general
	// WorldToScreen path per tile so the same helper governs all world→screen
	// work [03 §2.5] C1.
	// Derive the visible range from the camera's view scale so rendered tiles and
	// picked pixels agree at either scale [C-1][03 §2.5][F-P1-008]:
	// screenX = (worldX - camX)*scale + originX, so worldX = camX + floorDiv(screenX
	// - originX, scale) — the inverse ScreenToWorld computes. The range is the
	// whole world pixels covering screen columns [0, dstW): its first is the
	// inverse of column 0 and its last the inverse of column dstW-1, both floored
	// so a negative offset lands on the pixel that covers it [I3][03 §2.1].
	mx0 := int64(camX) + terrainFloorDiv(int64(-originX), int64(scale))
	my0 := int64(camZ) + terrainFloorDiv(int64(-originY), int64(scale))
	mx1 := int64(camX) + terrainFloorDiv(int64(dstW-1)-int64(originX), int64(scale))
	my1 := int64(camZ) + terrainFloorDiv(int64(dstH-1)-int64(originY), int64(scale))
	// Inclusive tile indices covering [mx0,mx1] etc.
	startTX := terrainFloorDiv(mx0, terrainTileSize)
	startTY := terrainFloorDiv(my0, terrainTileSize)
	endTX := terrainFloorDiv(mx1, terrainTileSize)
	endTY := terrainFloorDiv(my1, terrainTileSize)

	// Stable iteration over intersecting tiles in row-major order (determinism
	// per I1 is preserved — no map iteration).
	for ty := startTY; ty <= endTY; ty++ {
		for tx := startTX; tx <= endTX; tx++ {
			// Clip at map bounds [03 §2.2]: tiles outside the TileIndices grid
			// have no source block and leave the destination void.
			if tx < 0 || ty < 0 || tx >= int64(tileMapW) || ty >= int64(tileMapH) {
				continue
			}
			tileIndexPos := int(ty)*tileMapW + int(tx)
			if tileIndexPos < 0 || tileIndexPos >= len(t.TileIndices) {
				continue
			}
			tileID := t.TileIndices[tileIndexPos]
			if int(tileID) < 0 || int(tileID) >= len(t.TileSet) {
				continue
			}
			tile := &t.TileSet[tileID]
			// Tile's map-pixel origin.
			tileMapX := int(tx * terrainTileSize)
			tileMapY := int(ty * terrainTileSize)
			// Compute screen position via camera.WorldToScreen [03 §2.5] C1 to
			// share the one orthographic integer projection with all render
			// passes. World coords are map pixels *65536 (16.16 Fixed) [03 §2.1].
			// Height Y=0 so half-height shear ((Y>>16)>>1) is zero; the call
			// still proves the shear path.
			var sx, sy int32
			if cam != nil {
				wx := numeric.Fixed(int64(tileMapX) << 16)
				wz := numeric.Fixed(int64(tileMapY) << 16)
				// WorldToScreen bakes OriginX/Y (128,32) for the beam path.
				// Re-base to the requested viewport origin [PLAN_04A C1].
				bsx, bsy := cam.WorldToScreen(wx, 0, wz)
				sx = bsx - camera.OriginX + originX
				sy = bsy - camera.OriginY + originY
			} else {
				// No camera: treat cam at 0,0.
				sx = int32(tileMapX) + originX
				sy = int32(tileMapY) + originY
			}
			// Destination rectangle for this tile, clipped at viewport bounds
			// — partial edge rectangles [03 §2.2]. The screen size is exactly
			// 32*scale, which is the projection of the tile's opposite corner, so
			// picking via ScreenToWorld and the rendered tiles agree at either
			// scale [C-1][F-P1-008] (DESIGN_GPU_RENDERER §14.2).
			tileScreenW := int(terrainTileSize * scale)
			tileScreenH := tileScreenW
			dstX0 := int(sx)
			dstY0 := int(sy)
			dstX1 := dstX0 + tileScreenW
			dstY1 := dstY0 + tileScreenH
			if dstX0 < 0 {
				dstX0 = 0
			}
			if dstY0 < 0 {
				dstY0 = 0
			}
			if dstX1 > dstW {
				dstX1 = dstW
			}
			if dstY1 > dstH {
				dstY1 = dstH
			}
			if dstX0 >= dstX1 || dstY0 >= dstY1 {
				continue
			}
			// The source is the tile itself at the native scale, and at the detail
			// scale either a 64x64 detail tile copied one-to-one or the same 32x32
			// tile doubled by nearest sampling (DESIGN_GPU_RENDERER §14.2, §14.3).
			// Both are one walk over an exact integer rectangle: the source pixel of
			// destination column dx is (dx - sx)/step, where step is 1 when the
			// source side already equals the screen side and 2 for the doubled tile.
			srcSide := terrainTileSize
			src := tile[:]
			if scale != 1 && int(tileID) < len(detail) {
				srcSide, src = detailTileSize, detail[tileID][:]
			}
			if scale == 1 {
				// Fast 1:1 path: source intra-tile remainder [03 §2.2].
				srcX0 := dstX0 - int(sx)
				srcY0 := dstY0 - int(sy)
				w := dstX1 - dstX0
				h := dstY1 - dstY0
				if srcX0 < 0 || srcY0 < 0 || srcX0+w > terrainTileSize || srcY0+h > terrainTileSize {
					continue
				}
				for row := 0; row < h; row++ {
					srcRow := srcY0 + row
					dstRow := dstY0 + row
					srcOff := srcRow*terrainTileSize + srcX0
					dstOff := dstRow*dstW + dstX0
					if srcOff < 0 || srcOff+w > terrainTilePixels {
						continue
					}
					if dstOff < 0 || dstOff+w > len(dst) {
						continue
					}
					copy(dst[dstOff:dstOff+w], tile[srcOff:srcOff+w])
				}
				continue
			}
			step := tileScreenW / srcSide
			if step <= 0 || len(src) < srcSide*srcSide {
				continue
			}
			for dy := dstY0; dy < dstY1; dy++ {
				srcY := (dy - int(sy)) / step
				if srcY < 0 || srcY >= srcSide {
					continue
				}
				dstOff := dy*dstW + dstX0
				srcRow := srcY * srcSide
				for dx := dstX0; dx < dstX1; dx++ {
					srcX := (dx - int(sx)) / step
					if srcX < 0 || srcX >= srcSide {
						continue
					}
					dstIdx := dstOff + (dx - dstX0)
					if dstIdx < 0 || dstIdx >= len(dst) {
						continue
					}
					dst[dstIdx] = src[srcRow+srcX]
				}
			}
		}
	}
}
