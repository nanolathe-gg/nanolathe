package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestPointTargetHeightIsTheGroundNotTheZeroPlane locks the point-target half
// of the per-slot target-point resolver [06 R-WPN-04 §1]: the Y a slot aims at
// is `max(bilinearTerrainHeight(X, Z), seaLevelByte) << 16`, never zero.
//
// The zero it replaced was not neutral. A `Suppress` record binds its slots to
// a ground point [04 R-ORD-01 §3], and with Y left at zero the pipeline solved
// its pitch toward the map's zero plane — tens of world units under the point
// the player clicked — so the shot dived into the ground almost at the muzzle
// and the shot-time range gate measured to the wrong point as well.
func TestPointTargetHeightIsTheGroundNotTheZeroPlane(t *testing.T) {
	const cells = 8
	ter := &world.Terrain{CellW: cells, CellH: cells, SeaLevel: 30, Plot: make([]world.PlotCell, cells*cells)}
	for i := range ter.Plot {
		ter.Plot[i].SetHeight(90)
	}
	// A point inside the guarded interior, so the bilinear query answers a real
	// height rather than its out-of-bounds sentinel [03 §2.3].
	x := world.CellToWorld(2)
	z := world.CellToWorld(2)
	if got := PointTargetHeight(ter, x, z); got != numeric.Fixed(90<<16) {
		t.Fatalf("point height over height-90 ground = %v, want 90 world units [06 R-WPN-04 §1]", got)
	}

	// Below the water plane the aim point is the SURFACE, not the sea bed.
	for i := range ter.Plot {
		ter.Plot[i].SetHeight(5)
	}
	if got := PointTargetHeight(ter, x, z); got != numeric.Fixed(30<<16) {
		t.Fatalf("point height over submerged ground = %v, want the sea-level byte 30 [06 R-WPN-04 §1]", got)
	}

	// The bilinear query's raw −1 sentinel on the map's last row and column
	// loses the maximum to sea level rather than reading as a height.
	edge := world.CellToWorld(cells - 1)
	if got := PointTargetHeight(ter, edge, edge); got != numeric.Fixed(30<<16) {
		t.Fatalf("point height at the map edge = %v, want the sea-level byte [03 §2.3][06 R-WPN-04 §1]", got)
	}
}
