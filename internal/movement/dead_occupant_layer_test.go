package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/world"
)

// TestDeadMoverLeavesNoBlockedAnchors locks the class-layer half of the
// footprint clear [04 R-COLL-01 §4]: "for a unit with a mover, each of the
// sixteen class-layer records whose watermark exceeds the mover's last-stamp
// tick reclassifies the rectangle ... for a unit without a mover every active
// layer reclassifies it".
//
// Without that step a mover that stops committing long enough for a request
// revision to bake its rectangle in as blocked keeps blocking after it dies:
// the occupant word is gone, but the layer still holds the blocked value the
// occupant-age gate wrote, and the revision pass never revisits a dead unit
// (it walks live slots only). The result is a permanent phantom wall exactly
// where a unit was killed, which is the "cleared collision record hard-blocks
// like a building" shape [04 R-PATH-01 §14].
//
// The victim has to have STOPPED committing for the window to exist: a mover
// killed mid-stride carries a commit tick no watermark has passed, so no layer
// ever classified its cells blocked and there is nothing stale to leave behind.
// The stale precondition is a unit that halted — the ordinary end of a route —
// and was then killed where it stood.
func TestDeadMoverLeavesNoBlockedAnchors(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	sys.BeginTick(1)
	at := world.CellToWorld(9)
	victim, err := w.Create(wiringDef(), 0, at, terrain.HeightAt(at, at), at)
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	sys.EnsureUnit(w.Unit(victim))
	rq := world.CellToWorld(2)
	requester, err := w.Create(wiringDef(), 0, rq, terrain.HeightAt(rq, rq), rq)
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	sys.EnsureUnit(w.Unit(requester))

	anchor := sys.Collisions[victim].CachedAnchor
	layer := sys.ensureLayerRegistry().For("", wiringProfile)

	// A path request 200 ticks later arms the watermark past the victim's
	// creation-stamp tick, so the revision pass restamps its rectangle and the
	// occupant-age gate blocks it [04 R-MOV-03 §3][04 R-PATH-01 §14].
	sys.ensureLayerRegistry().ReviseFor("", wiringProfile, requester, 200)
	if got := layer.Value(anchor.X, anchor.Z); got != LayerBlocked {
		t.Fatalf("stale occupant anchor = %d, want blocked(0) — fixture did not arm the gate", got)
	}

	// The victim dies mid-route: the session finalizer frees the slot and then
	// runs the movement teardown [01 §4.4][04 §2.4].
	w.Unit(victim).Alive = false
	sys.ForgetUnit(victim)

	if occ, ok := grid.OccupantAt(anchor); ok {
		t.Fatalf("teardown left occupant %d at %+v", occ, anchor)
	}
	if got := layer.Value(anchor.X, anchor.Z); got == LayerBlocked {
		t.Errorf("dead mover's anchor still classifies blocked; the clear owes every "+
			"stale layer a rectangle reclassification [04 R-COLL-01 §4] (value %d)", got)
	}
}
