package render

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// BuildMegamapPicture renders the terrain background of the megamap: the
// map's tile art over the megamap extent, reduced to a w×h indexed picture.
//
// The shipped ProTA 4.8 picture algorithm is not recorded, so Nanolathe takes
// the pinned current source's documented reduction as its host choice
// (DESIGN_INTERFACE_HUD_INPUT §3.15): each output pixel averages the
// gamma-encoded RGB of the tile texels it covers with integer division, and
// takes the nearest palette index in OKLab among the indices the map's tiles
// use, keeping the lower index on a tie
// [community-patch-rendering "Terrain raster and palette subset"]. Error
// diffusion is not applied. The average is looked up at five bits per channel,
// which bounds the search to 32,768 colours per map.
func BuildMegamapPicture(t *world.Terrain, extentW, extentH int32, w, h int, pal *palette.Tables) []byte {
	if t == nil || pal == nil || w <= 0 || h <= 0 || extentW <= 0 || extentH <= 0 || len(t.TileSet) == 0 {
		return nil
	}
	tilesW, tilesH := int(t.CellW/2), int(t.CellH/2)
	if tilesW <= 0 || tilesH <= 0 || len(t.TileIndices) < tilesW*tilesH {
		return nil
	}
	// The palette subset: every index present in the map's tile art.
	var used [256]bool
	for _, tile := range t.TileSet {
		for _, p := range tile {
			used[p] = true
		}
	}
	var candidates []int
	var labs [][3]float64
	for i, u := range used {
		if u {
			candidates = append(candidates, i)
			c := pal.Base[i]
			labs = append(labs, okLab(c[0], c[1], c[2]))
		}
	}
	nearest := make([]int16, 1<<15)
	for i := range nearest {
		nearest[i] = -1
	}
	lookup := func(r, g, b int) byte {
		key := r>>3<<10 | g>>3<<5 | b>>3
		if v := nearest[key]; v >= 0 {
			return byte(v)
		}
		lab := okLab(byte(r>>3<<3|r>>3>>2), byte(g>>3<<3|g>>3>>2), byte(b>>3<<3|b>>3>>2))
		best, bestD := 0, math.Inf(1)
		for i, c := range labs {
			d := (c[0]-lab[0])*(c[0]-lab[0]) + (c[1]-lab[1])*(c[1]-lab[1]) + (c[2]-lab[2])*(c[2]-lab[2])
			if d < bestD {
				best, bestD = i, d
			}
		}
		nearest[key] = int16(candidates[best])
		return byte(candidates[best])
	}
	texel := func(x, z int) [4]byte {
		tx, tz := x>>5, z>>5
		if tx >= tilesW {
			tx = tilesW - 1
		}
		if tz >= tilesH {
			tz = tilesH - 1
		}
		idx := int(t.TileIndices[tz*tilesW+tx])
		if idx >= len(t.TileSet) {
			idx = 0
		}
		return pal.Base[t.TileSet[idx][(z&31)*32+(x&31)]]
	}
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		z0, z1 := int(int64(y)*int64(extentH)/int64(h)), int(int64(y+1)*int64(extentH)/int64(h))
		if z1 <= z0 {
			z1 = z0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := int(int64(x)*int64(extentW)/int64(w)), int(int64(x+1)*int64(extentW)/int64(w))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, n int
			for z := z0; z < z1; z++ {
				for xx := x0; xx < x1; xx++ {
					c := texel(xx, z)
					r, g, b, n = r+int(c[0]), g+int(c[1]), b+int(c[2]), n+1
				}
			}
			out[y*w+x] = lookup(r/n, g/n, b/n)
		}
	}
	return out
}

// okLab converts a gamma-encoded sRGB colour to OKLab.
func okLab(r8, g8, b8 byte) [3]float64 {
	lin := func(v byte) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	r, g, b := lin(r8), lin(g8), lin(b8)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return [3]float64{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}
