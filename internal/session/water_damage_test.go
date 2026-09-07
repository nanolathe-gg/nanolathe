package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// waterDamageFixture composes the smallest session the phase-2 visit needs: a
// unit pool, one seated human row so the sweep's control-byte gate passes, and
// a terrain carrying the sea-level byte and the two mission words.
func waterDamageFixture(t *testing.T, waterDoesDamage, waterDamage int32) (*Session, *content.UnitDef) {
	t.Helper()
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "watertest"},
		UnitName:         "watertest",
		MaxDamage:        100,
		DamageModifier:   65536, // 1.0 [02 "Unit record"]
		Script:           fixtureCOBProgram(),
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	ter := &world.Terrain{
		CellW:           4,
		CellH:           4,
		Plot:            make([]world.PlotCell, 16),
		SeaLevel:        20, // header byte [03 §2.2] C9
		WaterDoesDamage: waterDoesDamage,
		WaterDamage:     waterDamage,
	}
	s := &Session{
		Units:   newSessionFixtureWorld(4, cat),
		Catalog: cat,
		World:   ter,
		Econ:    &economy.Service{},
	}
	s.Combat = &combat.Service{ControlByte: func(owner uint8) uint8 {
		if int(owner) >= len(s.Econ.Players) || !s.Econ.Players[owner].Exists {
			return combat.ControlByteAbsent
		}
		return s.Econ.Players[owner].ControllerState
	}}
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	return s, def
}

// TestWaterDamageIsAppliedByThePhase2Visit locks step 9 of [04 R-MOV-03 §1] at
// its call site: on a map authoring both `waterdoesdamage` and `waterdamage`,
// a unit whose height word is at or below the sea-level byte loses exactly the
// authored amount, and only on the step's own `tick mod 30 == 0` cadence
// [04 §9.2]. A unit above sea level, and a `canhover` unit below it, take
// nothing.
func TestWaterDamageIsAppliedByThePhase2Visit(t *testing.T) {
	s, def := waterDamageFixture(t, 1, 10)

	hover := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "watertesthover"},
		UnitName:         "watertesthover",
		MaxDamage:        100,
		DamageModifier:   65536,
		CanHover:         true, // bit 12 excludes the unit from water damage [04 §9.2]
		Script:           fixtureCOBProgram(),
	}
	s.Catalog.Units[hover.CanonicalKey] = hover

	// The height operand is the signed high word of the 16.16 Y against the
	// zero-extended sea-level byte, inclusive [04 §9.2].
	drowning, err := s.Units.Create(def, 0, 0, numeric.FixedFromInt(5), 0)
	if err != nil {
		t.Fatalf("create drowning unit: %v", err)
	}
	dry, err := s.Units.Create(def, 0, 0, numeric.FixedFromInt(21), 0)
	if err != nil {
		t.Fatalf("create dry unit: %v", err)
	}
	floating, err := s.Units.Create(hover, 0, 0, numeric.FixedFromInt(5), 0)
	if err != nil {
		t.Fatalf("create hover unit: %v", err)
	}

	// Not a water-damage tick: nothing moves.
	s.stepUnitPhase(29)
	if got := s.Units.Unit(drowning).Health; got != 100 {
		t.Fatalf("tick 29 health = %d, want 100: the step's cadence is tick mod 30 == 0 [04 §9.2]", got)
	}

	// A water-damage tick. With no credited kills and no armored posture the
	// funnel's veteran factor is ((25-0)*10*4)/100 = 10, the authored amount.
	s.stepUnitPhase(30)
	if got := s.Units.Unit(drowning).Health; got != 90 {
		t.Fatalf("tick 30 health = %d, want 90 (the authored waterdamage of 10) [04 §9.2]", got)
	}
	if got := s.Units.Unit(drowning).LastDamageCause; got != uint8(combat.CauseWaterDamage) {
		t.Fatalf("last damage cause = %d, want %d (mission water damage) [06 §12.1]", got, combat.CauseWaterDamage)
	}
	if got := s.Units.Unit(dry).Health; got != 100 {
		t.Fatalf("above sea level health = %d, want 100 [04 §9.2]", got)
	}
	if got := s.Units.Unit(floating).Health; got != 100 {
		t.Fatalf("canhover health = %d, want 100: canhover is exempt [04 §9.2]", got)
	}

	// The cadence repeats, not the tick.
	s.stepUnitPhase(31)
	if got := s.Units.Unit(drowning).Health; got != 90 {
		t.Fatalf("tick 31 health = %d, want 90 [04 §9.2]", got)
	}
	s.stepUnitPhase(60)
	if got := s.Units.Unit(drowning).Health; got != 80 {
		t.Fatalf("tick 60 health = %d, want 80 [04 §9.2]", got)
	}
}

// TestWaterDamageInertWhenTheMissionAuthorsZero locks the other half of the
// gate: both mission words must be nonzero [04 §9.2]. Most stock OTAs author a
// `waterdamage` amount with `waterdoesdamage=0`, which damages nothing.
func TestWaterDamageInertWhenTheMissionAuthorsZero(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		waterDoesDamage, waterDamage int32
	}{
		{"flag zero, amount authored", 0, 100},
		{"flag authored, amount zero", 1, 0},
		{"neither authored", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, def := waterDamageFixture(t, tc.waterDoesDamage, tc.waterDamage)
			h, err := s.Units.Create(def, 0, 0, numeric.FixedFromInt(5), 0)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			for tick := uint32(1); tick <= 90; tick++ {
				s.stepUnitPhase(tick)
			}
			if got := s.Units.Unit(h).Health; got != 100 {
				t.Fatalf("health = %d, want 100: water damage needs BOTH words nonzero [04 §9.2]", got)
			}
		})
	}
}

// TestWaterDamageDrownsAndFinalizesInTheSameVisit locks the lethal arm: a
// lethal kind-0xB packet sets the ordinary death-pending state, and step 10 of
// the same visit finalizes it [04 R-MOV-03 §1][04 §9.2].
func TestWaterDamageDrownsAndFinalizesInTheSameVisit(t *testing.T) {
	s, def := waterDamageFixture(t, 1, 100)
	h, err := s.Units.Create(def, 0, 0, numeric.FixedFromInt(-1), 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var died []units.DeathCause
	s.Units.OnDeath = func(_ pool.Handle, cause units.DeathCause, _ *units.Unit) {
		died = append(died, cause)
	}
	s.stepUnitPhase(30)
	if len(died) != 1 || died[0] != units.DeathKilled {
		t.Fatalf("death finalization = %v, want exactly one DeathKilled in the same visit [04 R-MOV-03 §1] step 10", died)
	}
	if s.Units.Unit(h) != nil && s.Units.Unit(h).Alive {
		t.Fatalf("unit survived a lethal water-damage packet [04 §9.2]")
	}
}
