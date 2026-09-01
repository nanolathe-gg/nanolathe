// Play-test PT7 end-to-end lock, the air sequel to pt5 and pt6. Those two
// proved that ORDERED units fire and that ordered ground units close the
// distance. This one is about the units nobody ordered: an aircraft sitting on
// fire-at-will beside a hostile it can see.
package combat_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestPT7_IdleAircraftEngagesOnItsOwn is the play-test report "my plane refused
// to attack, and when it landed the enemy would not attack it either".
//
// The first half was real and total. Every stock aircraft authors
// `defaultmissiontype = VTOL_Standby`, and that row's phase 1 — "asks the
// ordinary autonomous acquisition for a target and, if one is found and
// accepted, clears the gate word, resets the phase to zero" [04 R-AIR-01 §7] —
// stood as a placeholder that always took the no-target arm, because the
// acquisition pair lives in internal/orders and had no exported seam. Phase 2's
// no-cargo arm then does what §7 says it does: an idle unloaded aircraft spawns
// `VTOL_LandIfCan` and parks. So an aircraft with fire-at-will and a hostile in
// plain sight landed next to it and sat there for the rest of the battle.
//
// The lock is the whole loop rather than "a target was acquired", because the
// weapon layer acquires and fires on its own whenever a target is already
// inside weapon range: a build with phase 1 still stubbed still damages a
// target parked under the aircraft's nose. What only the order path can produce
// is an air-attack RECORD on the queue.
func TestPT7_IdleAircraftEngagesOnItsOwn(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSkirmish, Map: "Great Divide", LocalOwner: 0, FS: fs,
	})
	if err != nil {
		t.Skipf("Great Divide unavailable: %v", err)
	}
	sess := composed.Session
	scaled := sess.Clock.ScaledAnchor
	step := func(n int) {
		for i := 0; i < n; i++ {
			scaled += 5
			sess.Step(scaled)
		}
	}
	step(4)

	var anchor *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Owner == 0 {
			anchor = u
			break
		}
	}
	if anchor == nil {
		t.Skip("skirmish composed with no local units")
	}
	gunship, ok := sess.Catalog.Unit("ARMBRAWL")
	target, ok2 := sess.Catalog.Unit("ARMSOLAR")
	if !ok || !ok2 || gunship == nil || target == nil {
		t.Skip("stock gunship or solar collector absent")
	}
	// Inside the aircraft's line of sight. The LOS radius saturates well below
	// the authored `sightdistance` for every long-sighted unit, because retail
	// clamps the ray-table group to the declared `numtables` [03 R-COMP-02 §1];
	// 240 world units is inside the saturated radius for anything.
	base := anchor.X.Add(numeric.FixedFromInt(700))
	air, err := sess.Units.Create(gunship, 0, base, anchor.Y, anchor.Z)
	if err != nil {
		t.Skipf("gunship placement refused: %v", err)
	}
	foe, err := sess.Units.Create(target, 1, base.Add(numeric.FixedFromInt(240)), anchor.Y, anchor.Z)
	if err != nil {
		t.Skipf("hostile placement refused: %v", err)
	}
	sess.CompleteUnit(air)
	sess.CompleteUnit(foe)
	plane := sess.Units.Unit(air)
	if plane.Flags>>units.StandingFireShift&units.StandingFieldMask != 2 {
		t.Fatalf("the stock gunship is not seeded fire-at-will [04 R-STANCE-01 §6]")
	}

	// The aircraft is given no order at all; the only thing that can put a
	// record on its queue is the standby row's own acquisition.
	engaged := ""
	for i := 0; i < 90 && engaged == ""; i++ {
		step(1)
		q := orders.QueueForUnit(plane)
		if q == nil || q.LenPrimary() == 0 {
			continue
		}
		switch name := orders.DescriptorFor(q.Primary()[0].ID).Name; name {
		case "AirStrike", "AirToAir", "AirToGround", "AirToGroundHover":
			engaged = name
		}
	}
	if engaged == "" {
		q := orders.QueueForUnit(plane)
		head := "<empty>"
		if q != nil && q.LenPrimary() > 0 {
			head = orders.DescriptorFor(q.Primary()[0].ID).Name
		}
		t.Fatalf("an idle fire-at-will aircraft issued no air-attack order against a hostile in sight; head is %q [04 R-AIR-01 §7]", head)
	}
}
