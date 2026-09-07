package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"

	"github.com/nanolathe/nanolathe/internal/world"
)

// TestGroundCommitPublishesVelocityTriple locks the mover's VELOCITY TRIPLE on
// the unit-side mirror [04 R-MOV-01 §1]. The triple is not the scalar speed
// word beside it: it is the signed per-axis displacement the commit step adds
// to the position, `proposed = position + velocity` [04 R-COLL-01 §1], and it
// is what the pre-fire lead of [06 §3.3] multiplies by the scaled flight time.
//
// Two contracts are asserted, and both are easy to regress silently:
//
//   - the published triple is exactly the displacement the commit applied, so
//     `position(after) - position(before)` reproduces it;
//   - the Y component is a literal zero on the ground path — the speed update
//     writes `vy = 0` and there is no gravity term anywhere in the ground
//     mover [04 R-MOV-01 §4].
func TestGroundCommitPublishesVelocityTriple(t *testing.T) {
	def := &content.UnitDef{
		UnitName: "velocity-triple-test", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 3, Acceleration: 3, BrakeRate: 3, TurnRate: 65535, BMCode: 1,
	}
	system, w, h := proposalFixture(t, def, true)
	u := w.Unit(h)
	if u.Move.VelX != 0 || u.Move.VelY != 0 || u.Move.VelZ != 0 {
		t.Fatalf("a fresh mover already carries a velocity triple (%d,%d,%d)",
			u.Move.VelX, u.Move.VelY, u.Move.VelZ)
	}
	// Three ticks of unobstructed travel. The identity holds only on a commit
	// the validator ACCEPTED: the blocked branch clamps the position inside
	// the old footprint while leaving the triple at the steering step's value
	// (or rewriting it at the halved speed), so a rejected proposal is
	// deliberately allowed to disagree [04 R-COLL-01 §1].
	for tick := uint32(1); tick <= 3; tick++ {
		beforeX, beforeZ := u.X, u.Z
		stepOnce(system, h, tick)
		if u.Move.VelY != 0 {
			t.Fatalf("tick %d: ground vertical velocity = %d, want a literal zero [04 R-MOV-01 §4]", tick, u.Move.VelY)
		}
		if system.Collisions[h].Blocked {
			t.Fatalf("tick %d: the fixture mover was rejected; it must travel freely for this assertion", tick)
		}
		if got, want := u.X.Sub(beforeX), u.Move.VelX; got != want {
			t.Fatalf("tick %d: X moved by %d but the published velocity is %d [04 R-COLL-01 §1]", tick, got, want)
		}
		if got, want := u.Z.Sub(beforeZ), u.Move.VelZ; got != want {
			t.Fatalf("tick %d: Z moved by %d but the published velocity is %d [04 R-COLL-01 §1]", tick, got, want)
		}
	}
	if u.Move.VelX == 0 && u.Move.VelZ == 0 {
		t.Fatal("the mover never published a nonzero velocity triple; the fixture did not move")
	}
}

// TestCarriedBranchCopiesCarrierVelocityTriple locks the carried arm of the
// commit step [04 R-COLL-01 §1]: it "copies the carrier's velocity triple and
// scalar speed into this mover", and zeroes when the carrier has no mover
// [04 R-FAC-02 §2]. A cargo that kept its own pre-attach triple would be led
// by the pre-fire lead of [06 §3.3] along a course it is no longer taking.
func TestCarriedBranchCopiesCarrierVelocityTriple(t *testing.T) {
	system := NewSystem(syntheticTerrainForIntegrate(), Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255,
	}, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	carrierDef := &content.UnitDef{UnitName: "carrier", FootprintX: 1, FootprintZ: 1, BMCode: 1, MaxVelocity: 32}
	cargoDef := &content.UnitDef{UnitName: "cargo", FootprintX: 1, FootprintZ: 1, BMCode: 1, MaxVelocity: 32}
	carrier, err := w.Create(carrierDef, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create carrier: %v", err)
	}
	cargo, err := w.Create(cargoDef, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create cargo: %v", err)
	}
	system.BindWorld(w)
	system.EnsureUnit(w.Unit(carrier))
	system.EnsureUnit(w.Unit(cargo))
	if !AttachCargo(w, carrier, cargo, -1) {
		t.Fatal("attach cargo")
	}
	// A carrier whose mover carries a horizontal velocity.
	coll := system.Collisions[carrier]
	coll.VX, coll.VZ, coll.Speed = 12345, -6789, 20000
	system.SyncCarriedMotion(w)
	cu := w.Unit(cargo)
	if cu.Move.VelX != numeric.Fixed(12345) || cu.Move.VelY != 0 || cu.Move.VelZ != numeric.Fixed(-6789) {
		t.Fatalf("cargo triple = (%d,%d,%d), want the carrier's (12345,0,-6789) [04 R-COLL-01 §1]",
			cu.Move.VelX, cu.Move.VelY, cu.Move.VelZ)
	}
	// The zeroing arm: with no mover record for the carrier the copy is an
	// exact zero triple, not a retention of what the cargo last held.
	delete(system.Collisions, carrier)
	delete(system.Flights, carrier)
	system.SyncCarriedMotion(w)
	if cu.Move.VelX != 0 || cu.Move.VelY != 0 || cu.Move.VelZ != 0 {
		t.Fatalf("mover-less carrier: cargo triple = (%d,%d,%d), want zeroes [04 R-COLL-01 §1]",
			cu.Move.VelX, cu.Move.VelY, cu.Move.VelZ)
	}
}
