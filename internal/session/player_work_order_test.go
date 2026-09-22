package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestPlayerMaintenanceUsesPreviousRegistryAfterWeaponPhase(t *testing.T) {
	s := visibilityFixture(t, true)
	weapon := &content.WeaponDef{ID: 1, Range: 500, LineOfSight: true, Tolerance: 32767}
	s.Catalog.Weapons["fixture"] = weapon
	s.Catalog.RebuildWeaponIndex()
	def := s.Catalog.Units["armcom"]
	def.Weapon1Def = weapon
	def.StandingFireOrder = 2
	// Check 2 of the picked-candidate order [06 §3.2]: a HUMAN-owned shooter
	// acquires only candidates whose definition authors `shootme`, and the
	// shooter below is human. The stock commander authors it — the stock
	// definitions that omit it are the non-combat buildings — so the fixture
	// definition authors it too and this test stays about phase order.
	def.ShootMe = true
	shooter, err := s.Units.Create(def, 0, numeric.FixedFromInt(128), numeric.FixedFromInt(10), numeric.FixedFromInt(128))
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.Units.Create(def, 1, numeric.FixedFromInt(160), numeric.FixedFromInt(10), numeric.FixedFromInt(128))
	if err != nil {
		t.Fatal(err)
	}
	s.Econ.Players[0].ControllerState = 1
	s.Combat = &combat.Service{ControlByte: func(uint8) uint8 { return combat.ControlByteHuman }}
	s.AI[0] = &ai.Manager{Player: 0, Catalog: s.Catalog}
	publishVisibilityForAll(s)
	u := s.Units.Unit(shooter)
	s.tickPlayers(30)
	if u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatal("maintenance used registry rebuilt later in the same phase")
	}
	for tick := uint32(31); tick < 38; tick++ {
		s.tickPlayers(tick)
	}
	// The eight-record slice returns to the shooter's record on this visit.
	s.Combat.StepWeaponsForUnit(u, 38, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
	if u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatal("weapon phase acquired autonomously")
	}
	s.tickPlayers(38)
	if got := u.SlotAt(0).Target; got.Kind != units.TargetUnit || got.Unit != target {
		t.Fatalf("phase-5 acquisition=%+v, want target %d", got, target)
	}
}

func TestPlayerLOSPublicationPrecedesSettlement(t *testing.T) {
	s := visibilityFixture(t, true)
	def := s.Catalog.Units["armcom"]
	h, err := s.Units.Create(def, 1, numeric.FixedFromInt(96), 0, numeric.FixedFromInt(96))
	if err != nil {
		t.Fatal(err)
	}
	publishVisibilityForAll(s)
	old := s.visStamps[int(h)]
	u := s.Units.Unit(h)
	u.X = numeric.FixedFromInt(400)
	observed := false
	s.Econ.EndCondition = func(player int, tick uint32) {
		if player == 1 {
			observed = true
			if s.visStamps[int(h)] == old {
				t.Fatal("settlement reached before the current LOS stamp")
			}
		}
	}
	s.tickPlayers(1)
	if !observed {
		t.Fatal("local deadline block not visited")
	}
}

// A builder already on the feature's grown border still waits for the real
// follower's next service before its height draw [04 R-PATH-01 §12].
func TestStationaryFeatureWorkArrivalThroughFollower(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		for _, name := range []string{"Reclaim", "Resurrect"} {
			t.Run(string(mode)+"/"+name, func(t *testing.T) {
				s := wreckOrientationSession(t)
				s.SetGameplay(mode)
				def := s.Catalog.Units["wreckcon"]
				def.CanReclamate, def.CanResurrect = true, true
				def.BuildDistance = 200
				corpse := s.Catalog.Features["wreckvictim_dead"]
				corpse.Height = 20
				if s.Features.PlaceAt(12, 12, corpse) == nil {
					t.Fatal("place corpse")
				}
				h, err := s.Units.Create(def, 0, world.CellToWorld(11), 0, world.CellToWorld(12))
				if err != nil {
					t.Fatal(err)
				}
				u := s.Units.Unit(h)
				s.Movement.BindWorld(s.Units)
				s.Movement.EnsureUnit(u)
				s.bindOrderQueue(u)
				q := orders.QueueForUnit(u)
				q.CancelAll()
				q.Push(orders.Lookup(name), orders.Node{Owner: h, GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(12), GoalSupplied: true})
				head := q.Head()
				sim := *s.SimRNG()
				q.Pump(u, 1)
				if head.Phase != 1 || head.DynamicGate != 0xe0 || *s.SimRNG() != sim {
					t.Fatalf("stationary approach did not wait: phase=%d gate=%#x draws=%d", head.Phase, head.DynamicGate, s.SimRNG().Draws()-sim.Draws())
				}
				x, z := u.X, u.Z
				s.Movement.ActivateMove(u, head)
				s.Movement.Scheduler.Tick(2)
				s.Movement.BeginTick(2)
				s.Movement.StepUnit(h, 2)
				s.Movement.EndTick(2)
				if head.Satisfied&0x20 == 0 || head.Satisfied&0x40 != 0 || u.X != x || u.Z != z {
					t.Fatalf("stationary follower outcome=%#x position=(%d,%d), want arrival without displacement", head.Satisfied, u.X, u.Z)
				}
				q.Pump(u, 2)
				if head.Phase != 2 || s.SimRNG().Draws() != sim.Draws()+1 {
					t.Fatalf("arrival did not enter stance wait with one height draw: phase=%d draws=%d", head.Phase, s.SimRNG().Draws()-sim.Draws())
				}
			})
		}
	}
}
