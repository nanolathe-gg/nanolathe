package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestPlayerMaintenanceUsesPreviousRegistryAfterWeaponPhase(t *testing.T) {
	s := visibilityFixture(t, true)
	weapon := &content.WeaponDef{ID: 1, Range: 500, LineOfSight: true, Tolerance: 32767}
	s.Catalog.Weapons["fixture"] = weapon
	s.Catalog.RebuildWeaponIndex()
	def := s.Catalog.Units["armcom"]
	def.Weapon1Def = weapon
	def.StandingFireOrder = 2
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
