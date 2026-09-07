package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Terrain draw for the modern executor — the orthographic tile pass in palette-
// index space (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It reproduces the classic
// tile blitter (internal/client BlitTerrainOrigin) exactly at native scale: for
// each visible tile it copies the same source block plus intra-tile remainder to
// the same clipped destination rectangle, drawing an axis-aligned integer quad
// that samples the per-map tile atlas with nearest filtering. Axis-aligned
// integer quads rasterize to exactly the classic rect's pixels, so the two
// executors write the same indices [03 §2.2][03 §2.5].
//
// Palette lookups stay deferred: this pass writes indices only, and the expansion
// pass turns them to colour once at the end (C-G8).

const (
	// terrainTileSize is the tile side in pixels; the tile map is a grid of these
	// [03 §2.1][03 §2.2]. It mirrors internal/client's terrainTileSize; the value
	// is a file-format constant, not shared code.
	terrainTileSize   = 32
	terrainTilePixels = terrainTileSize * terrainTileSize
)

// tileAtlas is one map's tile set uploaded as a single texture whose red channel
// holds tile indices (C-G4). Tile i occupies the 32×32 block at grid position
// (i%cols, i/cols); a tile pixel's atlas coordinate is derived in atlasSrc.
type tileAtlas struct {
	img   *ebiten.Image
	cols  int // tiles per atlas row
	count int // number of tiles (len(TileSet))
}

// atlasSrc returns the atlas pixel coordinate of intra-tile pixel (sx, sy) of
// tile id. The caller has already validated id against count.
func (a *tileAtlas) atlasSrc(id, sx, sy int) (int, int) {
	gx := (id % a.cols) * terrainTileSize
	gy := (id / a.cols) * terrainTileSize
	return gx + sx, gy + sy
}

// buildTileAtlas packs every tile of t.TileSet into one index texture (C-G4).
// Tiles are laid out in a near-square grid so neither atlas dimension grows
// without bound. The red channel carries the tile byte; alpha is opaque so the
// stored red survives premultiplied sampling and decodes back exactly.
func buildTileAtlas(t *world.Terrain) *tileAtlas {
	n := len(t.TileSet)
	if n <= 0 {
		return nil
	}
	// Near-square grid: cols = ceil(sqrt(n)). Integer sqrt by search keeps this
	// free of float rounding at the grid boundary.
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols
	atlasW := cols * terrainTileSize
	atlasH := rows * terrainTileSize
	buf := make([]byte, atlasW*atlasH*4)
	for i := 0; i < n; i++ {
		tile := &t.TileSet[i]
		gx := (i % cols) * terrainTileSize
		gy := (i / cols) * terrainTileSize
		for ty := 0; ty < terrainTileSize; ty++ {
			dstRow := ((gy+ty)*atlasW + gx) * 4
			srcRow := ty * terrainTileSize
			for tx := 0; tx < terrainTileSize; tx++ {
				p := dstRow + tx*4
				buf[p] = tile[srcRow+tx]
				buf[p+3] = 255
			}
		}
	}
	img := ebiten.NewImage(atlasW, atlasH)
	img.WritePixels(buf)
	return &tileAtlas{img: img, cols: cols, count: n}
}

// atlasFor returns the cached tile atlas for t, building it on first use and
// caching it by pointer identity (docs/DESIGN_GPU_RENDERER.md §2.3). A nil or
// empty terrain has no atlas.
func (r *Renderer) atlasFor(t *world.Terrain) *tileAtlas {
	if t == nil || t.TileIndices == nil || len(t.TileSet) == 0 {
		return nil
	}
	if a, ok := r.tileAtlases[t]; ok {
		return a
	}
	a := buildTileAtlas(t)
	r.tileAtlases[t] = a
	return a
}

// Terrain replays one terrain blit into the indexed offscreen (C-G4). It matches
// the classic BlitTerrainOrigin at native scale: a tile at world pixel (px, pz)
// lands at (px-OriginX, pz-OriginY), the same rebasing the record's Origin fields
// fold in [03 §2.5], and each tile's clipped destination rectangle and intra-tile
// source offset are computed exactly as the byte blitter does.
//
// The GPU executor derives projection from the record's OriginX/OriginY and the
// DstW/DstH window rather than from the camera, so this path is native-scale
// only. TODO(question): world-space zoom for modern terrain is a Phase-3 design
// item (docs/DESIGN_GPU_RENDERER.md §5, world-space zoom); the parity matrix
// keeps zoom at 1, so the scale term is not carried on the record and not applied
// here. What would settle it: the design's world-space-zoom entry, which replaces
// per-tile scaling with a single scaled compose.
func (r *Renderer) Terrain(c drawlist.Terrain) {
	if r == nil || r.offscreen == nil || r.atlas == nil {
		return
	}
	t := c.Terrain
	atlas := r.atlasFor(t)
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
	if dstW > r.w {
		dstW = r.w
	}
	if dstH > r.h {
		dstH = r.h
	}
	if dstW <= 0 || dstH <= 0 {
		return
	}
	originX := int(c.OriginX)
	originY := int(c.OriginY)

	// Visible tile range. The bounds only bound the loop; the per-tile clip below
	// decides the covered pixels, so a one-tile pad on each side (harmless, its
	// clipped rect is empty) guards against any off-by-one in the range itself.
	startTX := floorDivInt(originX, terrainTileSize) - 1
	startTY := floorDivInt(originY, terrainTileSize) - 1
	endTX := floorDivInt(originX+dstW-1, terrainTileSize) + 1
	endTY := floorDivInt(originY+dstH-1, terrainTileSize) + 1
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

	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	flush := func() {
		if len(r.verts) == 0 {
			return
		}
		r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.atlas, &ebiten.DrawTrianglesShaderOptions{
			Blend:  ebiten.BlendCopy,
			Images: [4]*ebiten.Image{atlas.img, nil, nil, nil},
		})
	}
	for ty := startTY; ty <= endTY; ty++ {
		for tx := startTX; tx <= endTX; tx++ {
			tileID := int(t.TileIndices[ty*tileMapW+tx])
			if tileID < 0 || tileID >= atlas.count {
				continue
			}
			// Tile screen origin at native scale (shear term is zero for terrain at
			// ground height) [03 §2.5].
			sx := tx*terrainTileSize - originX
			sy := ty*terrainTileSize - originY
			dstX0, dstY0 := sx, sy
			dstX1, dstY1 := sx+terrainTileSize, sy+terrainTileSize
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
			// Intra-tile source offset (remainder), matching BlitTerrainOrigin's
			// scale-1 path and its guard [03 §2.2].
			srcX0 := dstX0 - sx
			srcY0 := dstY0 - sy
			w := dstX1 - dstX0
			h := dstY1 - dstY0
			if srcX0 < 0 || srcY0 < 0 || srcX0+w > terrainTileSize || srcY0+h > terrainTileSize {
				continue
			}
			ax0, ay0 := atlas.atlasSrc(tileID, srcX0, srcY0)
			if !r.quadBatchHasRoom() {
				flush()
				r.resetGeometry()
			}
			r.appendTexQuad(
				float32(dstX0), float32(dstY0), float32(dstX1), float32(dstY1),
				float32(ax0), float32(ay0), float32(ax0+w), float32(ay0+h),
			)
		}
	}
	flush()
}

// appendTexQuad appends one axis-aligned quad mapping destination rect
// [dx0,dx1)×[dy0,dy1) to source-atlas rect [sx0,sx1)×[sy0,sy1) as two triangles,
// into the reusable geometry scratch. Vertices sit on integer pixel corners with
// no colour operand (the atlas shader ignores vertex colour).
func (r *Renderer) appendTexQuad(dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1 float32) {
	base := uint16(len(r.verts))
	r.verts = append(r.verts,
		ebiten.Vertex{DstX: dx0, DstY: dy0, SrcX: sx0, SrcY: sy0},
		ebiten.Vertex{DstX: dx1, DstY: dy0, SrcX: sx1, SrcY: sy0},
		ebiten.Vertex{DstX: dx0, DstY: dy1, SrcX: sx0, SrcY: sy1},
		ebiten.Vertex{DstX: dx1, DstY: dy1, SrcX: sx1, SrcY: sy1},
	)
	r.idx = append(r.idx, base, base+1, base+2, base+1, base+2, base+3)
}

// floorDivInt is floor division for int, correct for negative numerators
// [INVARIANTS I3]. Terrain projection uses it for the visible-tile range.
func floorDivInt(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
