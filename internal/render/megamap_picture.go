package render

import "github.com/nanolathe-gg/nanolathe/internal/world"

// BuildMegamapPicture renders the terrain background of the megamap: the map's
// own tile art over the megamap extent, point-sampled into a w×h picture that
// keeps the tiles' palette indices unchanged. There is no averaging, colour
// matching or dithering; the picture is drawn through the battle palette like
// the game view. The caller builds it once per battle and image size
// ([draw-engine-interface "Terrain picture"](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap)).
//
// The step in each axis is `extent / dimension` in single precision, and
// output column c samples source column `trunc(c × stepX)` (rows likewise),
// both from zero. Each sample reads the tile index from the tile grid at
// `(column / 32, row / 32)` and copies byte `(column mod 32, row mod 32)` of
// that tile's 32×32 graphic.
//
// Host choice (DESIGN_INTERFACE_HUD_INPUT §3.15): the shipped build never lets
// the step fall below one source pixel, so a picture larger than the play area
// runs on past it into the excluded edge tiles and then off the tile grid.
// Nanolathe keeps the fractional step, so the picture always covers exactly
// the extent it shares with every other layer and never reads past the grid.
func BuildMegamapPicture(t *world.Terrain, extentW, extentH int32, w, h int) []byte {
	if t == nil || w <= 0 || h <= 0 || extentW <= 0 || extentH <= 0 || len(t.TileSet) == 0 {
		return nil
	}
	tilesW, tilesH := int(t.CellW/2), int(t.CellH/2)
	if tilesW <= 0 || tilesH <= 0 || len(t.TileIndices) < tilesW*tilesH {
		return nil
	}
	cols := MegamapSampleSteps(extentW, w)
	rows := MegamapSampleSteps(extentH, h)
	out := make([]byte, w*h)
	for y, sz := range rows {
		tz := min(sz>>5, tilesH-1)
		line := out[y*w : y*w+w]
		for x, sx := range cols {
			tx := min(sx>>5, tilesW-1)
			idx := int(t.TileIndices[tz*tilesW+tx])
			if idx >= len(t.TileSet) {
				idx = 0
			}
			line[x] = t.TileSet[idx][(sz&31)*32+(sx&31)]
		}
	}
	return out
}

// MegamapSampleSteps is one axis of the point sample: for each of n output
// pixels, the source pixel `trunc(i × (extent / n))` in single precision,
// clamped inside the extent.
func MegamapSampleSteps(extent int32, n int) []int {
	if n <= 0 || extent <= 0 {
		return nil
	}
	step := float32(extent) / float32(n)
	out := make([]int, n)
	for i := range out {
		out[i] = min(int(float32(i)*step), int(extent)-1)
	}
	return out
}
