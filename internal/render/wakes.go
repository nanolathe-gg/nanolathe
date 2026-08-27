package render

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

// WakeInput is the complete authored/authoritative input for one mover wake.
// Bounds are supplied by the mover; this renderer does not infer a water mesh
// or invent dimensions [03 §5.7][I6].
type WakeInput struct {
	MinX, MinZ numeric.Fixed
	MaxX, MaxZ numeric.Fixed
	Water      bool
	Visible    bool
	Graphic    string
	Palette    uint8
}

// WakeRect is a world-space rectangle. Empty Graphic means authored wake art
// was not resolved and must remain a no-op in compatibility drawing [03 §5.7][I9].
type WakeRect struct {
	MinX, MinZ numeric.Fixed
	MaxX, MaxZ numeric.Fixed
	Graphic    string
	Palette    uint8
}

// BuildWakeRect admits only visible, water-covered mover bounds. The caller
// supplies the bounds already established by the unit's footprint/collision
// state; no synthetic rectangle is created for a missing asset [03 §5.7].
func BuildWakeRect(in WakeInput) (WakeRect, bool) {
	if !in.Water || !in.Visible || in.MaxX <= in.MinX || in.MaxZ <= in.MinZ {
		return WakeRect{}, false
	}
	return WakeRect{
		MinX: in.MinX, MinZ: in.MinZ, MaxX: in.MaxX, MaxZ: in.MaxZ,
		Graphic: in.Graphic, Palette: in.Palette,
	}, true
}

// ClipWakeRect clips a wake against inclusive world bounds. It returns false
// when no covered pixels remain. Clipping is integer/fixed-point and does not
// alter the source wake [03 §5.7][I2].
func ClipWakeRect(w WakeRect, minX, minZ, maxX, maxZ numeric.Fixed) (WakeRect, bool) {
	if maxX <= minX || maxZ <= minZ {
		return WakeRect{}, false
	}
	if w.MinX < minX {
		w.MinX = minX
	}
	if w.MinZ < minZ {
		w.MinZ = minZ
	}
	if w.MaxX > maxX {
		w.MaxX = maxX
	}
	if w.MaxZ > maxZ {
		w.MaxZ = maxZ
	}
	if w.MaxX <= w.MinX || w.MaxZ <= w.MinZ {
		return WakeRect{}, false
	}
	return w, true
}

// WakeRasterizer is deliberately a callback over already clipped integer
// cells. It can feed strip 9 without introducing a water-surface simulation.
type WakeRasterizer func(x, z int32, palette uint8)

// RasterizeWake emits every covered map-pixel cell in row-major order. The
// fixed-point bounds are narrowed toward zero at the presentation edge; an
// empty/missing-art wake emits nothing [03 §5.7][I3][I9].
func RasterizeWake(w WakeRect, draw WakeRasterizer) {
	if draw == nil || w.Graphic == "" || w.MaxX <= w.MinX || w.MaxZ <= w.MinZ {
		return
	}
	minX, minZ := int32(w.MinX>>16), int32(w.MinZ>>16)
	maxX, maxZ := int32(w.MaxX>>16), int32(w.MaxZ>>16)
	for z := minZ; z < maxZ; z++ {
		for x := minX; x < maxX; x++ {
			draw(x, z, w.Palette)
		}
	}
}
