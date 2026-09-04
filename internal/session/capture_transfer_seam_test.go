package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// captureSeamSession builds a minimal two-player battle carrying one mobile
// captor definition (`cancapture` set) and one finished, capturable victim
// definition (`cancapture` clear — the same bit gates both ends
// [05 R-WORK-01 §6]).
func captureSeamSession(t *testing.T) *Session {
	t.Helper()
	cat := minimalCatalogForStrict()
	mk := func(name string, f func(*content.UnitDef)) {
		d := &content.UnitDef{
			UnitName: name, ObjectName: name, MaxDamage: 100, Limit: -1,
			SightDistance: 64, MovementClass: "testmove",
			FootprintX: 1, FootprintZ: 1, BMCode: true, CanMove: true,
			MaxVelocity: 1 << 16, TurnRate: 100, Acceleration: 1 << 10, BrakeRate: 1 << 10,
		}
		f(d)
		d.CanonicalKey = content.CanonicalKey(name)
		cat.Units[d.CanonicalKey] = d
	}
	mk("captureseamcaptor", func(d *content.UnitDef) {
		d.CanCapture = true
		d.BuildDistance = 400 << 16
	})
	mk("captureseamvictim", func(d *content.UnitDef) {
		// CanCapture stays false: predicate 4 of the phase-0 ladder rejects a
		// target whose OWN definition can also capture [05 R-WORK-01 §6].
		d.BuildCostEnergy = 1
		d.BuildCostMetal = 1
	})
	installFixtureCOB(cat)

	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), LocalOwner: 0}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = economyForTest()
	for i := range s.Econ.Players {
		s.Econ.Players[i].Exists = true
		s.Econ.Players[i].ControllerState = 1
		s.Econ.Players[i].Stock[0] = 1e6
		s.Econ.Players[i].Stock[1] = 1e6
		s.Econ.Players[i].Capacity[0] = 2e6
		s.Econ.Players[i].Capacity[1] = 2e6
	}
	s.Econ.SeedDeadlines(0)
	s.Clock = &clock.State{}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	return s
}

// TestCommanderCapturesEnemyUnit is WU-19-177's regression: a completed
// `Capture` order must change the target's owner.
//
// Before this WU the session bound no implementation for the work adapter's
// Capture port — internal/orders/work.go's captureHandler reached phase 5,
// called the seam, and got nothing back, so the transfer never happened and
// a captured unit kept its owner in a live battle [05 R-WORK-01 §6]
// [05 R-WORK-01 §11]. This drives a real `Capture` order end to end through
// the authoritative phases (approach, build stance, the progress timer) and
// asserts that a live replacement ends up owned by the captor, at the
// victim's position, while the old record is destroyed rather than mutated
// in place — the transfer allocates a fresh record and kills the old one
// with a cause-4 packet [05 R-WORK-01 §11], it does not flip `Owner` on the
// same handle.
func TestCommanderCapturesEnemyUnit(t *testing.T) {
	s := captureSeamSession(t)
	w := s.Units
	captorDef := s.Catalog.Units["captureseamcaptor"]
	victimDef := s.Catalog.Units["captureseamvictim"]

	// The captor starts several cells from the victim so the approach phase
	// exercises a genuine path search and a genuine follower arrival, rather
	// than the path search's "already satisfied" shortcut a zero-distance
	// request would take at setup — that shortcut alone never raises the
	// gate's arrival bit `0x20`, only the per-tick follower service does
	// [04 R-PATH-01 §4 step 6][04 R-MOV-03 §1 "The follower's per-tick
	// service"], so a captor spawned already inside the goal would need the
	// same follower step this test wants to exercise regardless.
	hCaptor, err := w.Create(captorDef, 0, world.CellToWorld(4), 0, world.CellToWorld(4))
	if err != nil {
		t.Fatalf("create captor: %v", err)
	}
	hVictim, err := w.Create(victimDef, 1, world.CellToWorld(12), 0, world.CellToWorld(12))
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	captor, victim := w.Unit(hCaptor), w.Unit(hVictim)
	victim.Remaining = 0 // finished — not "a cloud of vapor" [05 R-WORK-01 §6].
	victimX, victimZ := victim.X, victim.Z
	s.Movement.EnsureUnit(captor)
	s.Movement.EnsureUnit(victim)

	// The fixture script never runs the deferred StartBuilding body, so the
	// build-stance byte is set here — the same fixture accommodation
	// TestFrameFinishedByAHelperJoinsTheWorld makes for HelpBuild's identical
	// INBUILDSTANCE wait [04 R-ORD-01 §5].
	captor.InBuildStance = true
	// A hand-built Node needs the target's position in GoalX/Y/Z: that is what
	// the real order-issue path writes (session/commands.go resolves a
	// targeted order's goal from the target's current position), and the
	// per-tick mover step treats a targeted, non-move order with a zero goal
	// triple as goal-less — it never runs the follower's arrival check for it
	// at all [04 R-MOV-03 §1].
	captureID := orders.Lookup("Capture")
	orders.QueueForUnit(captor).Push(captureID, orders.NewNodeForOrder(captureID, hVictim, victim.X, victim.Y, victim.Z, 1, hCaptor, false))

	var repl *units.Unit
	for tick := uint32(1); tick <= 600 && repl == nil; tick++ {
		s.Clock.GlobalTick = tick
		s.stepAuthoritativePhases(tick)
		if victim.Owner == captor.Owner {
			t.Fatalf("the victim's own record changed owner in place at tick %d; retail creates a fresh replacement and kills the old record instead [05 R-WORK-01 §11]", tick)
		}
		for _, u := range w.IterSliced() {
			if u == nil || u == victim {
				continue
			}
			if u.Def == victimDef && u.Owner == captor.Owner {
				repl = u
				break
			}
		}
	}
	if repl == nil {
		t.Fatalf("no captor-owned replacement of %q appeared within 400 ticks; the captured unit never changed owner", victimDef.UnitName)
	}
	if repl.X != victimX || repl.Z != victimZ {
		t.Fatalf("replacement position = (%v,%v), want the victim's original position (%v,%v) [05 R-WORK-01 §11]", repl.X, repl.Z, victimX, victimZ)
	}
	if victim.Alive && !victim.Dying {
		t.Fatal("the old record is still alive and not even marked dying after the transfer")
	}
}
