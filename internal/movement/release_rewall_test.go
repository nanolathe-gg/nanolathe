package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestParkedRequesterIsRewalledAtRelease locks the request release's re-wall
// [04 R-PATH-01 §14 correction]: "the request release ... compares the
// requester's mover stamp tick against the class record's watermark and, when
// the tick is below it, reclassifies the requester's own footprint rectangle in
// the class layer ...; it writes nothing to the tick."
//
// The three states the test walks are the ones the correction names. (1) A
// parked mover whose commit tick the watermark has passed is BLOCKED in the
// layer — that is the occupant-age gate doing its job for everyone else's
// searches. (2) Its own request init makes the same rectangle PASSABLE, by
// classifying it under a temporarily current tick, so the requester cannot
// block its own start cell. (3) Its release restores the wall.
//
// Without (3) the revision window [old, new) never revisits that old stamp tick
// again, so a parked unit's every re-armed request would leave its cells
// passable for the rest of the battle and a mover routed into them would be
// stopped only by the commit validator — "the difference between a follower
// that idles and one that circles" [04 R-ORDER-02 §1 item 1].
func TestParkedRequesterIsRewalledAtRelease(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	sys.BeginTick(1)
	pk := world.CellToWorld(9)
	parked, err := w.Create(wiringDef(), 0, pk, terrain.HeightAt(pk, pk), pk)
	if err != nil {
		t.Fatalf("create parked mover: %v", err)
	}
	sys.EnsureUnit(w.Unit(parked))
	ot := world.CellToWorld(2)
	other, err := w.Create(wiringDef(), 0, ot, terrain.HeightAt(ot, ot), ot)
	if err != nil {
		t.Fatalf("create other mover: %v", err)
	}
	sys.EnsureUnit(w.Unit(other))

	reg := sys.ensureLayerRegistry()
	layer := reg.For("", wiringProfile)
	anchor := sys.Collisions[parked].CachedAnchor

	// (1) Another unit's request at tick 200 arms the watermark past the parked
	// mover's creation stamp, so the revision pass restamps its rectangle and
	// the occupant-age gate blocks it [04 R-MOV-03 §3][04 R-PATH-01 §14].
	reg.ReviseFor("", wiringProfile, other, 200)
	if got := layer.Value(anchor.X, anchor.Z); got != LayerBlocked {
		t.Fatalf("parked mover's anchor = %d, want blocked(0) — fixture did not arm the gate", got)
	}

	// (2) The parked mover's own request init classifies that rectangle under a
	// temporarily current tick and then restores the real one.
	reg.ReviseFor("", wiringProfile, parked, 260)
	if got := layer.Value(anchor.X, anchor.Z); got == LayerBlocked {
		t.Fatalf("requester's own rectangle still blocked after its request init; "+
			"the revision pass owes it self-transparency [04 R-PATH-01 §2] (value %d)", got)
	}
	// An absent entry is the unit record's zero-initialized tick, which is the
	// value this fixture's parked mover carries.
	commitBefore, _ := layer.CommitTick(parked)

	// (3) The release — reached here through the production publication
	// callback, which is where every request of this build ends.
	sys.publishFunc(path.Request{Unit: parked}, nil, path.StatusRejected)

	if got := layer.Value(anchor.X, anchor.Z); got != LayerBlocked {
		t.Errorf("parked requester's anchor = %d after release, want blocked(0): the release "+
			"reclassifies the requester's own rectangle [04 R-PATH-01 §14 correction]", got)
	}
	if got, _ := layer.CommitTick(parked); got != commitBefore {
		t.Errorf("release wrote the commit tick %d (was %d); the release path reclassifies "+
			"and writes no tick [04 R-PATH-01 §14 correction]", got, commitBefore)
	}
}

// TestFreshRequesterIsNotRewalledAtRelease is the other arm of the same
// comparison: a requester whose commit tick is at or above the watermark was
// never walled by the occupant-age gate, so its release has nothing to
// reclassify and must not invent a wall [04 R-PATH-01 §14 correction].
func TestFreshRequesterIsNotRewalledAtRelease(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	sys.BeginTick(1)
	at := world.CellToWorld(9)
	mover, err := w.Create(wiringDef(), 0, at, terrain.HeightAt(at, at), at)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	sys.EnsureUnit(w.Unit(mover))

	reg := sys.ensureLayerRegistry()
	layer := reg.For("", wiringProfile)
	anchor := sys.Collisions[mover].CachedAnchor

	reg.ReviseFor("", wiringProfile, mover, 200)
	// The mover commits this tick, so its stamp tick is ahead of the watermark
	// the revision just wrote.
	sys.noteOccupancyCommit(mover, 200)

	sys.publishFunc(path.Request{Unit: mover}, nil, path.StatusRejected)
	if got := layer.Value(anchor.X, anchor.Z); got == LayerBlocked {
		t.Errorf("fresh requester's anchor = blocked after release; the re-wall runs only " +
			"when the commit tick is BELOW the watermark [04 R-PATH-01 §14 correction]")
	}
}
