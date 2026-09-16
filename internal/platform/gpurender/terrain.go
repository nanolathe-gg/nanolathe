package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Terrain draw for the modern executor — the orthographic tile pass in palette-
// index space (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It reproduces the classic
// tile blitter (internal/client blitTerrain) exactly at either view scale: for
// each visible tile it copies the same source block plus intra-tile remainder to
// the same clipped destination rectangle, drawing an axis-aligned integer quad
// that samples the per-(tile set, scale) atlas with nearest filtering. Axis-
// aligned integer quads rasterize to exactly the classic rect's pixels, so the
// two executors write the same indices [03 §2.2][03 §2.5].
//
// The detail view is contract D4 (§14.5): the atlas holds one 32·s square per
// tile, so the source-to-destination map stays 1:1 at every scale and the intra-
// tile remainder is the classic blitter's own. At s = 2 that square is the
// record's detail tile when it has one and the 32×32 tile magnified by s
// otherwise, which is the per-tile choice the classic blitter makes (§14.2,
// §14.3).
//
// Palette lookups stay deferred: this pass writes indices only, and the expansion
// pass turns them to colour once at the end (C-G8).

const (
	// terrainTileSize is the tile side in pixels; the tile map is a grid of these
	// [03 §2.1][03 §2.2]. It mirrors internal/client's terrainTileSize; the value
	// is a file-format constant, not shared code.
	terrainTileSize   = 32
	terrainTilePixels = terrainTileSize * terrainTileSize
	// terrainDetailSize is the side of one detail tile, the 2× tile set of
	// DESIGN_GPU_RENDERER §14.3. drawlist.DetailTilePixels is its pixel count.
	terrainDetailSize = 64
	// terrainAtlasFallbackSide is the atlas side used before the graphics device
	// reports its own maximum. ebiten.MaxImageSize answers 0 until the game has
	// started, and every backend Ebitengine supports allows at least this, so a
	// pre-device build is packed to a size no device rejects rather than to a
	// guess that might.
	terrainAtlasFallbackSide = 4096
)

// The detail tile the record carries is exactly terrainDetailSize squared; this
// fails to compile if the two ever disagree.
const _ = uint(terrainDetailSize*terrainDetailSize - drawlist.DetailTilePixels)

// tileAtlasKey identifies one built atlas. The terrain pointer is the tile set's
// identity, the scale selects the tile square, and the detail slice's backing
// array distinguishes "no detail tiles yet" from a detail set installed later:
// the load-time remaster (§14.4) can install tiles after the first frame, and
// the atlas built before it must not be served afterwards.
type tileAtlasKey struct {
	terrain *world.Terrain
	detail  *[drawlist.DetailTilePixels]byte
	scale   camera.ViewScale
}

// tileAtlas is one map's tile set uploaded at one view scale as index textures
// whose red channel holds tile bytes (C-G4). Tile i occupies the side×side block
// at grid position (i%cols, (i/cols)%rowsPer) of page i/perPage; a tile pixel's
// atlas coordinate is derived in atlasSrc.
//
// The atlas is paged because the square grid of a large tile set can exceed the
// device's maximum image size at the detail scale. Measured over the 276 map
// tile sets of a retail install, the largest is Lava & Two Hills at 11,561
// tiles: 108×108 squares, which is 3,456 px at 32 and 6,912 px at 64. That is
// one page on a device reporting 8,192 or more and three pages at 4,096; a
// device whose maximum is below the grid gets one page per band of rows, and the
// draw walks the pages in turn. Tile rectangles are pairwise disjoint, so
// splitting the pass by page changes no pixel and no order (C-G3).
//
// Every cell is surrounded by a one-texel border holding a copy of the tile's
// own edge, so the cell's stride is side + 2·tileAtlasPad. See tileAtlasPad.
type tileAtlas struct {
	pages   []*ebiten.Image
	side    int // atlas square per tile, the scale's Px(32)
	stride  int // side + 2*tileAtlasPad, the pitch between cell origins
	cols    int // tiles per atlas row
	rowsPer int // tile rows per page
	perPage int // cols*rowsPer
	count   int // number of tiles (len(TileSet))
}

// tileAtlasPad is the border of duplicated edge texels around every cell of the
// tile atlas (docs/DESIGN_GPU_RENDERER.md §16.3 "Sampling").
//
// Without it a tile's cell touches its neighbour's, and a sample one texel past
// the cell reads a different tile. That happens: under the world transform the
// quad's source coordinate is interpolated across a destination span shorter
// than the source span, so the fragment nearest the quad's far edge maps to a
// source coordinate within a fraction of a texel of the cell's far edge, and the
// interpolator's own rounding is enough to floor it one texel over. Whether it
// does depends on where the tile's screen edge falls relative to the pixel
// centre, so it appears for a periodic subset of tile columns at some factors
// and none at others — the intermittent tile seams the play test reported. With
// the border, a read one texel past the cell returns the tile's own edge texel,
// which is what a clamp would have returned, and the seam cannot form. It is
// also what makes the filtered sampling below safe to write as four unclamped
// taps.
//
// The cost is (side+2)²/side² of the atlas: 13% at the native tile and 6% at the
// detail one.
const tileAtlasPad = 1

// atlasSrc returns the page and the atlas pixel coordinate of intra-tile pixel
// (sx, sy) of tile id. The caller has already validated id against count.
func (a *tileAtlas) atlasSrc(id, sx, sy int) (page, ax, ay int) {
	page = id / a.perPage
	local := id % a.perPage
	gx := (local%a.cols)*a.stride + tileAtlasPad
	gy := (local/a.cols)*a.stride + tileAtlasPad
	return page, gx + sx, gy + sy
}

// terrainAtlasMaxSide is the largest atlas dimension this device accepts.
// ebiten.MaxImageSize answers 0 before the game starts, which is when the
// renderer's own tests build an atlas without a device.
func terrainAtlasMaxSide() int {
	if n := ebiten.MaxImageSize(); n > 0 {
		return n
	}
	return terrainAtlasFallbackSide
}

// terrainAtlasLayout picks the page grid for n tiles of the given cell stride
// (the tile side plus its two borders): a near-square grid, narrowed to what one
// page can hold, and banded into pages when the rows do not fit one page either.
func terrainAtlasLayout(n, side, maxSide int) (cols, rowsPer, pages int) {
	// Near-square grid: cols = ceil(sqrt(n)). Integer sqrt by search keeps this
	// free of float rounding at the grid boundary.
	cols = 1
	for cols*cols < n {
		cols++
	}
	perSide := maxSide / side
	if perSide < 1 {
		// A single tile is wider than the device allows; one tile per page is
		// the most this can do, and the draw still addresses it.
		perSide = 1
	}
	if cols > perSide {
		cols = perSide
	}
	rows := (n + cols - 1) / cols
	rowsPer = rows
	if rowsPer > perSide {
		rowsPer = perSide
	}
	pages = (rows + rowsPer - 1) / rowsPer
	return cols, rowsPer, pages
}

// buildTileAtlas packs every tile of t.TileSet into index textures at one view
// scale (C-G4, §14.5). The square reserved for a tile is the scale's Px(32),
// and it is filled the way the classic blitter fills the tile's screen
// rectangle: from the detail tile — already at that side — when the record has
// one for that tile at a scale above native, and otherwise from the 32×32 tile
// resampled through the scale's inverse, the blitter's own nearest sampling. A
// source pixel the blitter's guard rejects leaves the atlas texel at index
// zero, which is the destination the blitter leaves untouched.
//
// The red channel carries the tile byte; alpha is opaque so the stored red
// survives premultiplied sampling and decodes back exactly.
func buildTileAtlas(t *world.Terrain, detail [][drawlist.DetailTilePixels]byte, scale camera.ViewScale) *tileAtlas {
	n := len(t.TileSet)
	if n <= 0 {
		return nil
	}
	scale = scale.Norm()
	side := int(scale.Px(terrainTileSize))
	stride := side + 2*tileAtlasPad
	cols, rowsPer, pageCount := terrainAtlasLayout(n, stride, terrainAtlasMaxSide())
	a := &tileAtlas{
		side:    side,
		stride:  stride,
		cols:    cols,
		rowsPer: rowsPer,
		perPage: cols * rowsPer,
		count:   n,
		pages:   make([]*ebiten.Image, pageCount),
	}
	atlasW := cols * stride
	for page := 0; page < pageCount; page++ {
		first := page * a.perPage
		last := first + a.perPage
		if last > n {
			last = n
		}
		rows := (last - first + cols - 1) / cols
		atlasH := rows * stride
		buf := make([]byte, atlasW*atlasH*4)
		for i := first; i < last; i++ {
			// The blitter's per-tile source choice: the detail tile at a scale
			// above native when the record carries one for this tile, and the
			// 32×32 tile otherwise (§14.2).
			srcSide, src := terrainTileSize, t.TileSet[i][:]
			direct := scale.Native()
			if !scale.Native() && i < len(detail) {
				srcSide, src, direct = side, detail[i][:], true
			}
			if len(src) < srcSide*srcSide {
				continue
			}
			local := i - first
			gx := (local%cols)*stride + tileAtlasPad
			gy := (local/cols)*stride + tileAtlasPad
			for ty := 0; ty < side; ty++ {
				sy := ty
				if !direct {
					sy = int(scale.Inverse(int32(ty)))
				}
				if sy >= srcSide {
					continue
				}
				dstRow := ((gy+ty)*atlasW + gx) * 4
				srcRow := sy * srcSide
				for tx := 0; tx < side; tx++ {
					sx := tx
					if !direct {
						sx = int(scale.Inverse(int32(tx)))
					}
					if sx >= srcSide {
						continue
					}
					p := dstRow + tx*4
					buf[p] = src[srcRow+sx]
					buf[p+3] = 255
				}
			}
			padTileCell(buf, atlasW, gx, gy, side)
		}
		img := ebiten.NewImage(atlasW, atlasH)
		img.WritePixels(buf)
		a.pages[page] = img
	}
	return a
}

// padTileCell copies a cell's edge texels into the one-texel border around it,
// so a sample that lands just outside the cell returns the tile's own edge
// rather than the neighbouring tile's (tileAtlasPad). (gx, gy) is the cell's
// first INNER texel and side its edge; the border it fills is the ring
// immediately outside that square, corners included.
func padTileCell(buf []byte, atlasW, gx, gy, side int) {
	at := func(x, y int) int { return (y*atlasW + x) * 4 }
	copyTexel := func(dx, dy, sx, sy int) {
		d, s := at(dx, dy), at(sx, sy)
		if d < 0 || s < 0 || d+4 > len(buf) || s+4 > len(buf) {
			return
		}
		copy(buf[d:d+4], buf[s:s+4])
	}
	x0, y0 := gx-tileAtlasPad, gy-tileAtlasPad
	x1, y1 := gx+side, gy+side // first texel past the cell on each axis
	for k := 0; k < side; k++ {
		copyTexel(gx+k, y0, gx+k, gy)        // top row
		copyTexel(gx+k, y1, gx+k, gy+side-1) // bottom row
		copyTexel(x0, gy+k, gx, gy+k)        // left column
		copyTexel(x1, gy+k, gx+side-1, gy+k) // right column
	}
	copyTexel(x0, y0, gx, gy) // corners
	copyTexel(x1, y0, gx+side-1, gy)
	copyTexel(x0, y1, gx, gy+side-1)
	copyTexel(x1, y1, gx+side-1, gy+side-1)
}

// atlasFor returns the cached tile atlas for one (tile set, detail tiles, scale)
// identity, building it on first use (docs/DESIGN_GPU_RENDERER.md §2.3, §14.5).
// A nil or empty terrain has no atlas.
func (r *Renderer) atlasFor(t *world.Terrain, detail [][drawlist.DetailTilePixels]byte, scale camera.ViewScale) *tileAtlas {
	if t == nil || t.TileIndices == nil || len(t.TileSet) == 0 {
		return nil
	}
	scale = scale.Norm()
	key := tileAtlasKey{terrain: t, scale: scale}
	if len(detail) != 0 {
		key.detail = &detail[0]
	}
	if a, ok := r.tileAtlases[key]; ok {
		return a
	}
	a := buildTileAtlas(t, detail, scale)
	r.tileAtlases[key] = a
	return a
}

// Terrain replays one terrain blit into the indexed offscreen (C-G4). It matches
// the classic blitTerrain at either view scale: a tile at world pixel (px, pz)
// lands at ((px-OriginX)·s, (pz-OriginY)·s), the rebasing and scaling the
// record's Origin and Scale fields describe [03 §2.5](§14.2), and each tile's
// clipped destination rectangle and intra-tile source offset are computed exactly
// as the byte blitter does.
//
// The GPU executor derives projection from the record's OriginX/OriginY, its
// Scale and the DstW/DstH window rather than from the camera, so it never reads
// client state.
func (r *Renderer) Terrain(c drawlist.Terrain) {
	if r == nil || r.surfaces[0] == nil || r.scene2D == nil {
		return
	}
	r.prepareWater(c)
	t := c.Terrain
	// Zero is the native scale: a recorder that never set the field draws the
	// native view (drawlist.Terrain.Scale).
	scale := c.Scale.Norm()
	atlas := r.atlasFor(t, c.Detail, scale)
	if atlas == nil {
		// A nil/empty terrain draws nothing; the offscreen keeps its cleared void
		// index, exactly as the classic clear-to-0 left it [03 §2.2].
		return
	}
	if t.CellW <= 0 || t.CellH <= 0 {
		return
	}
	tileMapW := int(t.CellW / 2)
	tileMapH := int(t.CellH / 2)
	if tileMapW <= 0 || tileMapH <= 0 || len(t.TileIndices) < tileMapW*tileMapH {
		return
	}
	dstW := int(c.DstW)
	dstH := int(c.DstH)
	if dstW > r.clipW() {
		dstW = r.clipW()
	}
	if dstH > r.clipH() {
		dstH = r.clipH()
	}
	if dstW <= 0 || dstH <= 0 {
		return
	}
	originX := int(c.OriginX)
	originY := int(c.OriginY)
	tileScreen := atlas.side
	// Filtered sampling rides a vertex lane rather than a second shader, so the
	// terrain still merges into the frame's own opaque run
	// (docs/DESIGN_GPU_RENDERER.md §16.3 "Sampling"). It is armed exactly when
	// the world transform is: rest scales with a fractional camera offset also
	// filter. An identity transform keeps the original nearest fetch and parity.
	filter := float32(0)
	if r.sched.worldOn {
		filter = 1
	}
	// TODO(question): below the strategic threshold the 1x tiles are sampled
	// down by the world transform, nearest, one tile quad at a time
	// (DESIGN_GPU_RENDERER §16.10). A half-resolution tile set would sample
	// better and cost a quarter of the atlas; whether the load-time synthesis
	// is worth it is unmeasured, so this draws the 1x set for now.

	// Visible tile range. Screen column c shows world pixel originX +
	// Inverse(c), the inverse the camera's ScreenToWorld computes (§14.2), so
	// the visible world pixels are [originX, originX+Inverse(dstW-1)]. The
	// bounds only bound the loop; the per-tile clip below decides the covered
	// pixels, so a one-tile pad on each side (harmless, its clipped rect is
	// empty) guards against any off-by-one in the range itself.
	startTX := numeric.FloorDiv(originX, terrainTileSize) - 1
	startTY := numeric.FloorDiv(originY, terrainTileSize) - 1
	endTX := numeric.FloorDiv(originX+int(scale.Inverse(int32(dstW-1))), terrainTileSize) + 1
	endTY := numeric.FloorDiv(originY+int(scale.Inverse(int32(dstH-1))), terrainTileSize) + 1
	if startTX < 0 {
		startTX = 0
	}
	if startTY < 0 {
		startTY = 0
	}
	if endTX > tileMapW-1 {
		endTX = tileMapW - 1
	}
	if endTY > tileMapH-1 {
		endTY = tileMapH - 1
	}

	// One command per atlas page: the page rides source slot 3 of the scene
	// shader, so the terrain pass merges into the same batch as the frame's
	// sprites, glyphs and fills (docs/DESIGN_GPU_RENDERER.md §11.2). A tile set
	// small enough for one page — every retail set on a current device — is one
	// command, as it was before the detail view.
	for page := 0; page < len(atlas.pages); page++ {
		img := atlas.pages[page]
		if img == nil {
			continue
		}
		begun := false
		for ty := startTY; ty <= endTY; ty++ {
			for tx := startTX; tx <= endTX; tx++ {
				tileID := int(t.TileIndices[ty*tileMapW+tx])
				if tileID < 0 || tileID >= atlas.count {
					continue
				}
				if tileID/atlas.perPage != page {
					continue
				}
				// Tile screen origin (shear term is zero for terrain at ground
				// height) [03 §2.5](§14.2).
				sx := int(scale.Project(int32(tx*terrainTileSize - originX)))
				sy := int(scale.Project(int32(ty*terrainTileSize - originY)))
				dstX0, dstY0 := sx, sy
				dstX1, dstY1 := sx+tileScreen, sy+tileScreen
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
				// Intra-tile source offset (remainder). The atlas square is the
				// tile's screen square, so this is the blitter's own remainder at
				// either scale [03 §2.2](§14.5).
				srcX0 := dstX0 - sx
				srcY0 := dstY0 - sy
				w := dstX1 - dstX0
				h := dstY1 - dstY0
				if srcX0 < 0 || srcY0 < 0 || srcX0+w > atlas.side || srcY0+h > atlas.side {
					continue
				}
				if !begun {
					if !r.sched.begin(schedOpaque, 0, 0, dstW, dstH, [4]*ebiten.Image{3: img}) {
						return
					}
					begun = true
				}
				_, ax0, ay0 := atlas.atlasSrc(tileID, srcX0, srcY0)
				r.sched.quad(schedOpaque,
					float32(dstX0), float32(dstY0), float32(dstX1), float32(dstY1),
					float32(ax0), float32(ay0), float32(ax0+w), float32(ay0+h),
					[4]float32{}, [4]float32{filter, 0, 0, sceneOpTerrain})
			}
		}
	}
	r.drawWater(c)
	r.drawWaterReflections(c)
	// The battle lights reach the ground last in the terrain pass, so the copy
	// they read carries the resolved water surface too (§31).
	r.drawGroundLighting()
}
