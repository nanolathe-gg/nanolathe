package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func airReclaimFixture(t *testing.T, mode uint8) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	s, b, target, _ := reclaimFixture(t, 100, 60)
	b.Def.CanFly, b.Def.CanMove = true, true
	b.Def.FootprintX, b.Def.FootprintZ = 1, 1
	b.Def.CruiseAlt, b.Def.MaxVelocity, b.Def.Acceleration, b.Def.BrakeRate, b.Def.TurnRate = 60, 4*65536, 65536/4, 65536/8, 500
	b.Def.BankScale = 65536
	b.InBuildStance, b.Activated = false, false
	b.Move.Mode = mode
	b.X, b.Z = world.CellToWorld(8), world.CellToWorld(8)
	target.X, target.Z = world.CellToWorld(26), b.Z
	terrain := &world.Terrain{CellW: 32, CellH: 32, Gravity: 0x1fdb, Plot: make([]world.PlotCell, 1024)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	s.Terrain = terrain
	s.Movement = movement.NewSystem(terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 12, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}, movement.NewOccupancyGrid())
	s.Movement.BindWorld(s.World)
	s.Movement.EnsureUnit(b)
	b.Move.Mode = mode
	q := orders.QueueForUnit(b)
	q.RemoveHead()
	sim := rng.NewSimulation(1)
	q.SetBinding(&orders.QueueBinding{Lookup: s.World.Unit, SimRNG: &sim, Movement: &orders.MovementGoalAdapter{
		InstallAir: s.Movement.InstallAirGoal, Release: s.Movement.ReleaseGoalPayload,
	}})
	q.Push(orders.Lookup("VTOL_ReclaimUnit"), orders.Node{Owner: b.Handle, Target: target.Handle, Deadline: -1})
	s.RegisterOrderHandlers(q)
	return s, b, target, q.Head()
}

// The air preamble has a takeoff arm only for grounded starts. A carried
// aircraft is released into airborne mode before that decision [04 R-ORD-01 §7].
func TestAirUnitReclaimPreambleAndMarker(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    uint8
		carried bool
	}{{"grounded", 1, false}, {"airborne", 2, false}, {"carried", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, b, target, n := airReclaimFixture(t, tc.mode)
			var carrier *units.Unit
			if tc.carried {
				h, err := s.World.Create(target.Def, 0, b.X, b.Y, b.Z)
				if err != nil {
					t.Fatal(err)
				}
				carrier = s.World.Unit(h)
				carrier.Attachment.Cargo = []pool.Handle{b.Handle}
				b.Attachment.Carrier = h
			}
			for i := 0; i < 3; i++ {
				b.SlotAt(i).Flags |= units.SlotFlagEnabled | units.SlotFlagAutonomous
			}
			vm := bindScriptBridge(t, b, "StartBuilding", "StopBuilding")
			b.RevealDeadline = 123
			s.StepUnit(TickContext{Tick: 1}, b.Handle)
			if !b.Activated || b.Move.Mode&3 != 2 {
				t.Fatal("preamble did not activate and take off/release")
			}
			for i := 0; i < 3; i++ {
				if b.SlotAt(i).Flags&units.SlotFlagAutonomous != 0 {
					t.Fatal("weapon slot was not released")
				}
			}
			if tc.carried && (b.Attachment.Carrier != 0 || len(carrier.Attachment.Cargo) != 0) {
				t.Fatal("carrier links survived preamble")
			}
			marker := s.Movement.AirMarkerState(b.Handle)
			if !marker.IsMarker || marker.ArrivalRadius != 0 || marker.AltOffset != 0 {
				t.Fatalf("unexpected air marker: %+v", marker)
			}
			if tc.mode == 1 {
				if n.Phase != 1 || n.DynamicGate != 0xe0 || marker.Goal.X != b.X || marker.Goal.Y != b.Y+numeric.Fixed(int64(b.Def.CruiseAlt/2)<<16) {
					t.Fatalf("grounded takeoff phase/gate/marker: %d/%#x/%+v", n.Phase, n.DynamicGate, marker)
				}
				n.Satisfied |= 0x20
				s.StepUnit(TickContext{Tick: 2}, b.Handle)
				marker = s.Movement.AirMarkerState(b.Handle)
			}
			if n.Phase != 2 || n.DynamicGate != 0x100e8 || marker.Goal.X != target.X || marker.Goal.Y != target.Y || marker.Goal.Z != target.Z {
				t.Fatalf("target approach phase/gate/marker: %d/%#x/%+v", n.Phase, n.DynamicGate, marker)
			}
			if n.Param1 == 0 || n.Param2 != 0 {
				t.Fatal("air approach did not seed pulse and zero cadence")
			}
			if liveThreads(vm) != 0 || n.Flags&orders.FlagStopBuildingPending != 0 || b.RevealDeadline != 123 {
				t.Fatal("air reclaim emitted a ground callback or reveal stamp")
			}
		})
	}
}

// A real movement marker drives the aircraft toward the distant target; the
// work row remains behind the arrival gate with no cadence or damage meanwhile.
func TestAirUnitReclaimDistantTargetIsApproached(t *testing.T) {
	for _, mode := range []uint8{1, 2} {
		s, b, target, n := airReclaimFixture(t, mode)
		q := orders.QueueForUnit(b)
		startX, startY := b.X, b.Y
		for tick := uint32(1); tick <= 80; tick++ {
			q.Pump(b, tick)
			s.StepUnit(TickContext{Tick: tick}, b.Handle)
			s.Movement.BeginTick(tick)
			s.Movement.StepUnit(b.Handle, tick)
			s.Movement.EndTick(tick)
		}
		if b.X <= startX || b.Y <= startY {
			t.Fatalf("start mode %d never climbed and approached: x/y=%d/%d", mode, b.X, b.Y)
		}
		if n.Phase != 2 || n.Param2 != 0 || target.Health != 100 {
			t.Fatalf("distant approach ran work: mode/phase/counter/health=%d/%d/%d/%d", mode, n.Phase, n.Param2, target.Health)
		}
	}
}

// Air work uses builddistance alone and waits thirty ticks on a failed reach;
// the ground model-radius extension and stance/callback phases cannot leak in.
func TestAirUnitReclaimReachAndThirtyTickRestart(t *testing.T) {
	s, b, target, n := airReclaimFixture(t, 2)
	sink := &countingNanoSink{}
	s.Presentation = sink
	vm := bindScriptBridge(t, b, "StartBuilding", "StopBuilding")
	b.RevealDeadline = 77
	target.Def.FootprintX, target.Def.FootprintZ = 2, 2 // a ground-reachable point outside air reach
	target.X, target.Z = b.X+numeric.FixedFromInt(61), b.Z
	s.StepUnit(TickContext{Tick: 0}, b.Handle)
	n.Satisfied |= 0x20
	s.StepUnit(TickContext{Tick: 1}, b.Handle)
	if !reclaimInRange(b, target) {
		t.Fatal("fixture does not discriminate the ground radius")
	}
	if n.Phase != 0 || n.Deadline != 31 || n.Param2 != 0 || sink.count != 0 || target.Health != 100 {
		t.Fatalf("failed air reach: phase/deadline/counter/spray/health=%d/%d/%d/%d/%d", n.Phase, n.Deadline, n.Param2, sink.count, target.Health)
	}
	target.X = b.X + numeric.FixedFromInt(60)
	for tick := uint32(2); tick < 31; tick++ {
		orders.QueueForUnit(b).Pump(b, tick)
		s.StepUnit(TickContext{Tick: tick}, b.Handle)
	}
	if n.Phase != 0 || n.Param2 != 0 {
		t.Fatal("air restart ran before thirty ticks")
	}
	s.StepUnit(TickContext{Tick: 31}, b.Handle)
	if n.Phase != 2 || n.Param2 != 0 {
		t.Fatal("air restart did not reinstall its target marker")
	}
	n.Satisfied |= 0x20
	s.StepUnit(TickContext{Tick: 32}, b.Handle)
	if n.Param2 != 2 || sink.count != 1 {
		t.Fatalf("inclusive air reach refused work: counter/spray=%d/%d", n.Param2, sink.count)
	}
	n.Param2 = 16
	s.StepUnit(TickContext{Tick: 34}, b.Handle)
	if target.Health != 99 || n.Param2 != 2 || n.Deadline != 36 || sink.count != 2 {
		t.Fatal("air pulse or after-pulse tail differs from unit-reclaim cadence")
	}
	if b.RevealDeadline != 77 || liveThreads(vm) != 0 {
		t.Fatal("air work emitted ground reveal/callback effects")
	}
}
