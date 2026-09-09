// Coordinate conversions, centralized here per [03 §2.1] and INVARIANTS I3.

package world

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// World units per map pixel and derived cell/tile sizes [03 §2.1].
//
//   - 1 map pixel = 65,536 world units (1 << 16, i.e. numeric.FractionOne)
//   - 1 cell      = 16 map pixels = 1,048,576 world units (1 << 20)
//   - 1 tile      = 32 map pixels = 2,097,152 world units (1 << 21), covering 2×2 cells
const (
	worldUnitsPerPixel = int64(numeric.FractionOne) // 65536 [03 §2.1]
	worldUnitsPerCell  = 16 * worldUnitsPerPixel    // 1048576 [03 §2.1]
	worldUnitsPerTile  = 32 * worldUnitsPerPixel    // 2097152 [03 §2.1]
)

// floorDiv returns floor(a/b) for signed integers with sign correction [INVARIANTS I3].
// Go's integer division truncates toward zero; this adjusts for negative dividends
// so that e.g. -1/1048576 yields -1 rather than 0, matching retail's arithmetic
// shift with sign correction [03 §2.1].
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// WorldToCell converts a world coordinate to a cell index with floor semantics
// and sign correction [03 §2.1]. Every world→cell conversion in the codebase
// must go through this helper (I3); no caller may inline x>>20 or x/65536.
func WorldToCell(x numeric.Fixed) int32 {
	return int32(floorDiv(int64(x), worldUnitsPerCell))
}

// WorldToTile converts a world coordinate to a tile index with floor semantics
// and sign correction [03 §2.1]. A tile is 32 map pixels covering four cells.
func WorldToTile(x numeric.Fixed) int32 {
	return int32(floorDiv(int64(x), worldUnitsPerTile))
}

// CellToWorld returns the world coordinate of the origin (minimum corner) of
// cell c [03 §2.1].
func CellToWorld(c int32) numeric.Fixed {
	return numeric.Fixed(int64(c) * worldUnitsPerCell)
}

// TileToWorld returns the world coordinate of the origin of tile t [03 §2.1].
func TileToWorld(t int32) numeric.Fixed {
	return numeric.Fixed(int64(t) * worldUnitsPerTile)
}

// PlacementAnchor returns the north-west footprint cell retail snaps a build
// site to, given the ground point under the cursor [07 §9].
//
// Retail's ghost updater and its order issuer compute the same pair, with the
// footprint's half-extent subtracted in world units before the shift:
//
//	cell = (picked - (foot << 19) + (1 << 19)) >> 20
//
// The `+ (1 << 19)` is a round-to-nearest-cell, not a floor: an even footprint
// straddles a cell boundary, and the site follows whichever cell the cursor is
// nearer to rather than always taking the lower one. Odd footprints are
// centered on a cell and the rounding term is absorbed by the half-extent, so
// they behave like a plain floor.
func PlacementAnchor(px, pz numeric.Fixed, footX, footZ int32) (cellX, cellZ int32) {
	extent, err := NewFootprintExtent(footX, footZ)
	if err != nil {
		return legacyPlacementAnchor(px, pz, footX, footZ)
	}
	anchor, err := SnapFootprintAnchor(px, pz, extent)
	if err != nil {
		return legacyPlacementAnchor(px, pz, footX, footZ)
	}
	return anchor.cellX, anchor.cellZ
}

// legacyPlacementAnchor preserves the original no-error helper's behavior for
// callers that have not migrated to checked typed placement APIs. New code
// should use SnapFootprintAnchor and handle its explicit errors.
func legacyPlacementAnchor(px, pz numeric.Fixed, footX, footZ int32) (cellX, cellZ int32) {
	half := func(p numeric.Fixed, foot int32) int32 {
		v := int64(p) - int64(foot)*(worldUnitsPerCell/2) + worldUnitsPerCell/2
		return int32(floorDiv(v, worldUnitsPerCell))
	}
	return half(px, footX), half(pz, footZ)
}

// PlacementCenter returns the world point at the center of a footprint anchored
// at (cellX, cellZ). This is the position retail stores on the MOBILEBUILD
// order, not the raw cursor point [07 §9]:
//
//	center = ((foot + 2*cell) << 19)
func PlacementCenter(cellX, cellZ, footX, footZ int32) (x, z numeric.Fixed) {
	extent, err := NewFootprintExtent(footX, footZ)
	if err != nil {
		return legacyPlacementCenter(cellX, cellZ, footX, footZ)
	}
	center, err := CenterForFootprint(NewFootprintAnchor(cellX, cellZ), extent)
	if err != nil {
		return legacyPlacementCenter(cellX, cellZ, footX, footZ)
	}
	return center.x, center.z
}

// legacyPlacementCenter preserves the original no-error helper's behavior;
// checked callers should use CenterForFootprint.
func legacyPlacementCenter(cellX, cellZ, footX, footZ int32) (x, z numeric.Fixed) {
	c := func(cell, foot int32) numeric.Fixed {
		return numeric.Fixed(int64(foot+2*cell) * (worldUnitsPerCell / 2))
	}
	return c(cellX, footX), c(cellZ, footZ)
}
