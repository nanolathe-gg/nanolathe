package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func mkWaterWorldDef(name string, canHover bool, armored bool, dmgMod int32) *content.UnitDef {
	if dmgMod == 0 {
		dmgMod = 65536
	}
	return &content.UnitDef{
		UnitName:       name,
		MaxDamage:      100,
		DamageModifier: dmgMod,
		ArmoredState:   armored,
		CanHover:       canHover,
		FootprintX:     1,
		FootprintZ:     1,
	}
}

func mkWaterWorld() (*units.World, *world.Terrain) {
	w := newCombatFixtureWorld(4, nil)
	ter := &world.Terrain{CellW: 10, CellH: 10, SeaLevel: 10}
	return w, ter
}

func TestWaterDamage_TickGate(t *testing.T) {
	w, ter := mkWaterWorld()
	def := mkWaterWorldDef("testland", false, false, 65536)
	h, err := w.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	// Tick not multiple of 30 should do nothing even though in water [04 §9.2]
	if got := TickWaterDamage(31, w, ter, 1, 10, nil); got != 0 {
		t.Fatalf("tick 31 should do nothing [04 §9.2] cadence, got %d", got)
	}
	if u.Health != 100 {
		t.Fatalf("health changed on non-damage tick [04 §9.2] got %d want 100", u.Health)
	}
	// Tick 30 should apply
	if got := TickWaterDamage(30, w, ter, 1, 10, nil); got != 1 {
		t.Fatalf("tick 30 should apply to one unit [04 §9.2], got %d", got)
	}
	if u.Health == 100 {
		t.Fatalf("health should have taken water damage on tick 30 [04 §9.2]")
	}
}

func TestWaterDamage_LandTakesNothing(t *testing.T) {
	w, ter := mkWaterWorld()
	def := mkWaterWorldDef("landunit", false, false, 65536)
	h, _ := w.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(20), numeric.FixedFromInt(0))
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	// Y=20 > sea 10 => land [04 §9.2] at or below sea-level
	if got := TickWaterDamage(30, w, ter, 1, 10, nil); got != 0 {
		t.Fatalf("land unit should take nothing [04 §9.2] isInWater, got applied %d", got)
	}
	if u.Health != 100 {
		t.Fatalf("land unit health changed [04 §9.2] got %d want 100", u.Health)
	}
	// also check eligibility directly
	if IsWaterDamageEligible(u, ter) {
		t.Fatalf("land eligibility should be false [04 §9.2]")
	}
}

func TestWaterDamage_NonHoverInWaterTakesDamage(t *testing.T) {
	w, ter := mkWaterWorld()
	def := mkWaterWorldDef("ground", false, false, 65536)
	h, _ := w.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	u.Kills = 0
	// IsInWater check [04 §9.2] signed integer height
	if !IsInWaterForDamage(u.Y, ter.SeaLevel) {
		t.Fatalf("unit at 5 should be in water for sea 10 [04 §9.2]")
	}
	if !IsWaterDamageEligible(u, ter) {
		t.Fatalf("non-hover in water should be eligible [04 §9.2]")
	}
	// packet kind should be 0xB with null attacker [04 §9.2][06 §9.1]
	pkt := WaterDamagePacketForTest(u.Handle, 10, u.Kills, false, 65536)
	if pkt.Kind != KindNoReaction {
		t.Fatalf("water damage kind should be 0xB (KindNoReaction 11) [04 §9.2][06 §9.1] got %d", pkt.Kind)
	}
	if pkt.Attacker != 0 {
		t.Fatalf("water damage attacker should be null [04 §9.2][06 §12.1] got %d", pkt.Attacker)
	}
	// falloff multiplier 1.0 for non-AOE [04 §9.2]
	expected := ComputeWaterDamageScaledAmount(10, 0, false, 65536)
	if pkt.Amount != expected {
		t.Fatalf("packet amount mismatch [04 §9.2][06 §9.2] got %d want %d", pkt.Amount, expected)
	}
	if got := TickWaterDamage(30, w, ter, 1, 10, nil); got != 1 {
		t.Fatalf("non-hover in water should take damage [04 §9.2] got %d", got)
	}
	if u.Health == 100 {
		t.Fatalf("non-hover in water health unchanged, expected damage [04 §9.2]")
	}
	// health should be 100 - expected (with defender vet tier 0 => factor 1.0, so 100-10=90)
	if u.Health != 90 {
		// veteran factor for 0 kills: ((25-0)*4)/100 =1.0 => 10
		// armor not applied => 10
		t.Fatalf("health after water damage [04 §9.2][06 §9.2] got %d want 90", u.Health)
	}
}

func TestWaterDamage_CanHoverInWaterTakesNothing(t *testing.T) {
	w, ter := mkWaterWorld()
	def := mkWaterWorldDef("hover", true, false, 65536) // canhover true [04 §9.2] bit 12
	h, _ := w.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	if IsWaterDamageEligible(u, ter) {
		t.Fatalf("canhover unit should be excluded [04 §9.2] canhover is bit 12 excludes")
	}
	if got := TickWaterDamage(30, w, ter, 1, 10, nil); got != 0 {
		t.Fatalf("canhover in water should take nothing [04 §9.2] got %d", got)
	}
	if u.Health != 100 {
		t.Fatalf("canhover health changed [04 §9.2] got %d want 100", u.Health)
	}
}

func TestWaterDamage_VeteranReduction(t *testing.T) {
	// veteran victim takes REDUCED water damage [04 §9.2] tier = min(kills/5,5), factor ((25-tier)*4)/100
	// Novice vs veteran
	w1, ter := mkWaterWorld()
	def := mkWaterWorldDef("vettest", false, false, 65536)

	// novice kills 0 => tier 0 => factor 100% => damage 20
	h1, _ := w1.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u1 := w1.Unit(h1)
	u1.Health = 100
	u1.MaxHealth = 100
	u1.Kills = 0
	TickWaterDamage(30, w1, ter, 1, 20, nil)
	dmgNovice := 100 - int(u1.Health)

	w2, _ := mkWaterWorld()
	def2 := mkWaterWorldDef("vettest2", false, false, 65536)
	h2, _ := w2.Create(def2, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u2 := w2.Unit(h2)
	u2.Health = 100
	u2.MaxHealth = 100
	u2.Kills = 10 // tier 2 => factor (25-2)*4/100=92% => damage 18 (trunc)
	TickWaterDamage(30, w2, ter, 1, 20, nil)
	dmgVet := 100 - int(u2.Health)

	if dmgVet >= dmgNovice {
		t.Fatalf("veteran should take REDUCED water damage [04 §9.2] novice %d veteran %d", dmgNovice, dmgVet)
	}
	// Direct amount check via ComputeWaterDamageScaledAmount
	amtNovice := ComputeWaterDamageScaledAmount(20, 0, false, 65536)
	amtVet := ComputeWaterDamageScaledAmount(20, 10, false, 65536)
	if amtVet >= amtNovice {
		t.Fatalf("scaled amount veteran %d should be < novice %d [04 §9.2][06 §9.2] tier reduction", amtVet, amtNovice)
	}
	// Tier 5 cap: 25 kills => tier 5 => factor 80% => damage 16
	amtVet5 := ComputeWaterDamageScaledAmount(20, 25, false, 65536)
	if amtVet5 != 16 {
		t.Fatalf("tier 5 amount [04 §9.2] (25-5)*4/100=0.8*20=16, got %d", amtVet5)
	}
	amtVet10 := ComputeWaterDamageScaledAmount(20, 30, false, 65536)
	if amtVet10 != 16 {
		t.Fatalf("tier cap at 5 [04 §9.2] kills 30 should equal kills 25, got %d want 16", amtVet10)
	}
}

func TestWaterDamage_PlayerClassGate(t *testing.T) {
	w, ter := mkWaterWorld()
	def := mkWaterWorldDef("classgate", false, false, 65536)
	h, _ := w.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	// accessor returns class 3 (computer/AI) => should be excluded [04 §9.2] only 1 or 2 eligible
	getClass := func(owner uint8) uint8 { return 3 }
	if got := TickWaterDamage(30, w, ter, 1, 10, getClass); got != 0 {
		t.Fatalf("player class 3 should exclude [04 §9.2] got %d", got)
	}
	if u.Health != 100 {
		t.Fatalf("class 3 unit should not take damage [04 §9.2] got %d", u.Health)
	}
	// class 1 should be eligible
	u.Health = 100
	getClass1 := func(owner uint8) uint8 { return 1 }
	if got := TickWaterDamage(60, w, ter, 1, 10, getClass1); got != 1 {
		t.Fatalf("player class 1 should be eligible [04 §9.2] got %d", got)
	}
	if u.Health == 100 {
		t.Fatalf("class 1 unit should take damage [04 §9.2]")
	}
	// nil accessor placeholder assumes eligible (TODO) — ensures existing callers without class store still damage
	w3, ter3 := mkWaterWorld()
	h3, _ := w3.Create(mkWaterWorldDef("nilgate", false, false, 65536), 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u3 := w3.Unit(h3)
	u3.Health = 100
	u3.MaxHealth = 100
	if got := TickWaterDamage(30, w3, ter3, 1, 10, nil); got != 1 {
		t.Fatalf("nil class accessor should be permissive placeholder [04 §9.2] TODO got %d", got)
	}
}

func TestWaterDamage_MissionGates(t *testing.T) {
	w, ter := mkWaterWorld()
	def := mkWaterWorldDef("mission", false, false, 65536)
	h, _ := w.Create(def, 0, numeric.FixedFromInt(0), numeric.FixedFromInt(5), numeric.FixedFromInt(0))
	u := w.Unit(h)
	u.Health = 100
	u.MaxHealth = 100
	// waterdoesdamage 0 => no damage
	if got := TickWaterDamage(30, w, ter, 0, 10, nil); got != 0 {
		t.Fatalf("waterdoesdamage 0 should block [04 §9.2] got %d", got)
	}
	if u.Health != 100 {
		t.Fatalf("should not damage when waterdoesdamage 0 [04 §9.2]")
	}
	// waterdamage 0 => no damage
	if got := TickWaterDamage(60, w, ter, 1, 0, nil); got != 0 {
		t.Fatalf("waterdamage 0 should block [04 §9.2] got %d", got)
	}
	if u.Health != 100 {
		t.Fatalf("should not damage when waterdamage 0 [04 §9.2]")
	}
}

func TestWaterDamage_IsInWaterIntegerHeight(t *testing.T) {
	// signed integer height at or below sea-level byte [04 §9.2]
	// fraction .5 should not matter for integer check
	if !IsInWaterForDamage(numeric.FixedFromInt(10), 10) {
		t.Fatalf("10 <=10 should be in water [04 §9.2]")
	}
	if IsInWaterForDamage(numeric.FixedFromInt(11), 10) {
		t.Fatalf("11 >10 should not be in water [04 §9.2]")
	}
	// fractional: 10.9 truncs to 10 => still in water if sea 10
	if !IsInWaterForDamage(numeric.Fixed(10*65536+32768), 10) {
		t.Fatalf("10.5 trunc 10 <=10 should be in water [04 §9.2] integer height")
	}
}
