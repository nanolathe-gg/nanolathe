package world

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

// CursorToWorld converts a cursor position already expressed in map pixels
// (camera origin plus the viewport-relative pointer) into the ground point the
// pointer is over [07 §8].
//
// Terrain tiles are presented flat — the tile blitter never reads a height —
// but every world object standing on the ground is drawn with the half-height
// shear `screenRow = worldZ - (height >> 1)` [03 §2.5]. A naive inverse that
// assumes height zero therefore lands a move order north of the pixel the
// player clicked, by half the terrain height there. Retail resolves this with a
// short search along Z instead of an algebraic inverse:
//
//  1. Clamp the pixel pair into the map rectangle, 0 .. pixels-1 on each axis.
//  2. Round the row down to a whole cell and start eight cells south of it.
//  3. Walk north one cell (16 map pixels) at a time, at most nine probes. For
//     each candidate row compute `max(height, sea level)` and its projected
//     screen row `Z - (h >> 1)`; stop at the first candidate that projects at or
//     above the clicked row.
//  4. Probe once more one cell further south and interpolate Z linearly between
//     the two bracketing projections, then sample the height at the interpolated
//     row for the returned Y.
//
// Exhausting the nine probes returns the last candidate unrefined. The returned
// triple is 16.16 world (X, Y, Z), Y being the ground height under the result.
//
// The one deliberate divergence: retail divides by the difference of the two
// projected rows without guarding it, so a degenerate pair faults. Here a zero
// difference keeps the unrefined first candidate.
func (t *Terrain) CursorToWorld(px, pz int32) (x, y, z numeric.Fixed) {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return numeric.Fixed(int64(px) * worldUnitsPerPixel), 0, numeric.Fixed(int64(pz) * worldUnitsPerPixel)
	}
	// Map rectangle in pixels; retail holds these as Width<<4 / Height<<4.
	pixW := t.CellW * 16
	pixH := t.CellH * 16
	if px < 0 {
		px = 0
	}
	if px >= pixW {
		px = pixW - 1
	}
	if pz < 0 {
		pz = 0
	}
	if pz >= pixH {
		pz = pixH - 1
	}
	sea := int32(t.SeaLevel)
	xf := numeric.Fixed(int64(px) * worldUnitsPerPixel)

	// Start eight cells south of the clicked row, cell-aligned.
	zf := numeric.Fixed(int64((pz&^0xF)+0x80) * worldUnitsPerPixel)
	var hf numeric.Fixed
	var projNorth int32
	found := false
	// Nine probes: retail's budget starts at 128 and steps down by 16 while it
	// stays non-negative. The row is decremented between probes, never after
	// the last one, so an exhausted search reports the ninth candidate.
	for probe := 0; probe < 9; probe++ {
		h := t.groundLevel(xf, zf, sea)
		hf = numeric.Fixed(int64(h) * worldUnitsPerPixel)
		projNorth = int32(int16(zf>>16)) - int32(int16(h))>>1
		if projNorth <= pz {
			found = true
			break
		}
		if probe == 8 {
			break
		}
		zf -= numeric.Fixed(worldUnitsPerCell)
	}
	if !found {
		return xf, hf, zf
	}

	// Bracket with the candidate one cell further south.
	z2 := zf + numeric.Fixed(worldUnitsPerCell)
	h2 := t.groundLevel(xf, z2, sea)
	projSouth := int32(int16(z2>>16)) - int32(int16(h2))>>1

	// Retail's two guards: a non-increasing pair, or a click south of the far
	// bracket, both keep the unrefined candidate.
	if projNorth >= projSouth || pz > projSouth {
		return xf, hf, zf
	}
	span := projSouth - projNorth
	if span == 0 {
		return xf, hf, zf
	}
	zi := zf + numeric.Fixed(int64(pz-projNorth)*worldUnitsPerCell/int64(span))
	hi := t.groundLevel(xf, zi, sea)
	return xf, numeric.Fixed(int64(hi) * worldUnitsPerPixel), zi
}

// CursorToWorldMapPixels is the named map-pixel entry point for the terrain
// inverse. CursorToWorld is retained for existing callers; both names route
// through the same retail search so an order target cannot accidentally use a
// height-zero algebraic inverse [07 §8].
func (t *Terrain) CursorToWorldMapPixels(px, pz int32) (x, y, z numeric.Fixed) {
	return t.CursorToWorld(px, pz)
}

// groundLevel is the height query the cursor search uses: the bilinear terrain
// height, floored at sea level so the water surface picks like ground [07 §8].
// The out-of-bounds −1 sentinel loses to sea level for free.
func (t *Terrain) groundLevel(x, z numeric.Fixed, sea int32) int32 {
	h := int32(t.HeightAt(x, z) >> 16)
	if h > sea {
		return h
	}
	return sea
}
