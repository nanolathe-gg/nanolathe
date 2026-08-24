// Package world provides authoritative world geometry and coordinate helpers.
// Coordinate conversions are centralized here per [03 §2.1] and INVARIANTS I3.
package world

import "github.com/nanolathe/nanolathe/internal/sim/numeric"

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
