package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/world"
)

// TestAirborneRestampAdvancesTheOccupantClock locks the stamp's first action
// [04 R-COLL-01 §4 "stamp, in order"]: "The mover's last-stamp tick (the
// occupant-age clock of [R-DOC04-B]) is set to the current tick" — before the
// bounds test, without reference to the plane being written, guarded only on
// the mover existing [04 R-PATH-01 §14 "writers of the mover's commit tick"].
//
// The build wrote it only when the GROUND plane was touched, so an aircraft
// restamping in the air kept a clock frozen at takeoff and its cells could read
// stale to the first classification after touchdown.
func TestAirborneRestampAdvancesTheOccupantClock(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	sys.BeginTick(1)
	at := world.CellToWorld(6)
	h, err := w.Create(wiringDef(), 0, at, terrain.HeightAt(at, at), at)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	layer := sys.ensureLayerRegistry().For("", wiringProfile)

	// Leave the ground plane for the air plane; this transition already wrote
	// the clock before the change, because it clears a ground rectangle.
	u.Move.Mode = 2
	sys.syncMoverStamp(u)

	// A purely airborne move: clear the air word at the old pair, stamp it at
	// the new one. No ground cell is touched.
	sys.BeginTick(300)
	coll := sys.Collisions[h]
	coll.CachedAnchor = Cell{X: coll.CachedAnchor.X + 2, Z: coll.CachedAnchor.Z}
	sys.syncMoverStamp(u)

	if got, _ := layer.CommitTick(h); got != 300 {
		t.Errorf("last-stamp tick after an airborne restamp = %d, want 300: the stamp writes "+
			"the mover's clock unconditionally [04 R-COLL-01 §4]", got)
	}
}
