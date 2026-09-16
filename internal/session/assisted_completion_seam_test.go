package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// assistSeamSession builds a minimal battle carrying one mobile builder and one
// mobile product definition.
func assistSeamSession(t *testing.T) *Session {
	t.Helper()
	cat := minimalCatalogForStrict()
	mk := func(name string, f func(*content.UnitDef)) {
		d := &content.UnitDef{
			UnitName: name, ObjectName: name, MaxDamage: 100, Limit: -1,
			SightDistance: 64, MovementClass: "testmove",
			FootprintX: 1, FootprintZ: 1, BMCode: 1, CanMove: true,
			MaxVelocity: 1 << 16, TurnRate: 100, Acceleration: 1 << 10, BrakeRate: 1 << 10,
		}
		f(d)
		d.CanonicalKey = content.CanonicalKey(name)
		cat.Units[d.CanonicalKey] = d
	}
	mk("assistseamcon", func(d *content.UnitDef) {
		d.Builder = true
		d.WorkerTime = 300
		d.BuildDistance = 400 << 16
	})
	mk("assistseamprod", func(d *content.UnitDef) {
		d.BuildTime = 100
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

// TestFrameFinishedByAHelperJoinsTheWorld is WU-19-153's regression, and the
// second half of WU-19-132's.
//
// [05 R-WORK-01 §1] runs the completion transition from inside the shared
// construction step, so the builder that stores the zero is the builder that
// completes the frame — a `HelpBuild` helper or an assisting guard as much as
// the frame's own builder. WU-19-132 taught the FACTORY's node to notice a zero
// it did not store; this locks the other direction, the helper's own step.
//
// This build defers a product's mover state and its visibility publication to
// the session's `CompleteUnit`, and the only construction caller of that hook
// was the `WorkResult` a builder's own `StepUnit` window returns. So a frame
// with no live builder record — its builder killed, or given another order,
// which is the ordinary way a half-built solar ends up finished by a second con
// — took the whole completion posture from the helper's step and still got no
// mover record and no publish: finished, at full health, standing on the map
// unable to answer a Move and holding no occupancy.
func TestFrameFinishedByAHelperJoinsTheWorld(t *testing.T) {
	s := assistSeamSession(t)
	w := s.Units
	conDef := s.Catalog.Units["assistseamcon"]
	prodDef := s.Catalog.Units["assistseamprod"]

	hHelper, err := w.Create(conDef, 0, world.CellToWorld(10), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create helper: %v", err)
	}
	hFrame, err := w.CreateNanoframe(prodDef, 0, world.CellToWorld(9), 0, world.CellToWorld(9))
	if err != nil {
		t.Fatalf("create nanoframe: %v", err)
	}
	helper, frame := w.Unit(hHelper), w.Unit(hFrame)
	frame.Remaining = 1
	frame.Health = 0
	frame.MaxHealth = int32(prodDef.MaxDamage)
	frame.SetActivationEdge(false)

	// No builder holds a build record for this frame: the helper's own step is
	// the only thing that can store the zero.
	q := orders.QueueForUnit(helper)
	// The fixture script never runs the deferred StartBuilding body, so the
	// stance byte is set here; it is the only thing `HelpBuild` phase 2 waits on
	// [04 R-ORD-01 §5].
	helper.InBuildStance = true
	q.Push(orders.Lookup("HelpBuild"), orders.Node{Owner: hHelper, Target: hFrame})

	for tick := uint32(1); tick <= 60 && frame.Remaining > 0; tick++ {
		s.Clock.GlobalTick = tick
		s.stepAuthoritativePhases(tick)
	}
	if frame.Remaining != 0 {
		t.Fatalf("the helper never finished the frame: remaining %v", frame.Remaining)
	}
	if frame.Flags&construction.FlagCompleted == 0 || frame.Health != frame.MaxHealth {
		t.Fatalf("the helper's step skipped the completion posture: flags %#x health %d/%d [05 R-WORK-01 §1]",
			frame.Flags, frame.Health, frame.MaxHealth)
	}
	if s.Movement == nil || s.Movement.Routes == nil {
		t.Fatal("no movement service in the fixture")
	}
	if s.Movement.Routes[hFrame] == nil {
		t.Fatal("a frame finished by a helper never reached the session's completion hook, so it has no mover " +
			"state: it cannot answer a Move and holds no occupancy [05 R-WORK-01 §1][01 §6.1][03 §3]")
	}
	cell := movement.Cell{X: int32(frame.X.Raw() >> 20), Z: int32(frame.Z.Raw() >> 20)}
	if occ, ok := s.Movement.Grid.OccupantAt(cell); !ok || occ != int(hFrame) {
		t.Fatalf("the finished unit holds no occupancy at its own cell: occupant %d present %v, want %d [04 §8.2]", occ, ok, hFrame)
	}

	// The play-visible half: it walks when told to.
	startX, startZ := frame.X, frame.Z
	orders.QueueForUnit(frame).Push(orders.Lookup("Move_Ground"), orders.Node{
		Owner: hFrame,
		GoalX: world.CellToWorld(20), GoalZ: world.CellToWorld(20),
		GoalSupplied: true,
	})
	for tick := uint32(61); tick <= 130; tick++ {
		s.Clock.GlobalTick = tick
		s.stepAuthoritativePhases(tick)
	}
	if frame.X == startX && frame.Z == startZ {
		t.Fatal("the finished unit did not move off its build site when ordered to")
	}
}
