package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func makeKilledProg(variant int32) *cob.Program {
	code := []uint32{
		0x10021001, uint32(variant),
		0x10023002, 1,
		0x10021001, uint32(variant),
		0x10065000,
	}
	return &cob.Program{
		Code:        code,
		Scripts:     map[string]int{"Killed": 0},
		Pieces:      []string{"base"},
		Statics:     0,
		ScriptsByID: []int{0},
	}
}

func TestDeathKilledCorpseDepth(t *testing.T) {
	cat := strictMinimalCatalog()
	corpse1 := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Damage: 100}
	corpse1.CanonicalKey = content.CanonicalKey("heap1")
	corpse2 := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Damage: 100}
	corpse2.CanonicalKey = content.CanonicalKey("heap2")
	corpse3 := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Damage: 100}
	corpse3.CanonicalKey = content.CanonicalKey("heap3")
	corpse1.FeatureDead = "heap2"
	corpse1.FeatureDeadDef = corpse2
	corpse2.FeatureDead = "heap3"
	corpse2.FeatureDeadDef = corpse3
	cat.Features["heap1"] = corpse1
	cat.Features["heap2"] = corpse2
	cat.Features["heap3"] = corpse3

	terrain := strictMinimalTerrain()
	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.IsObserver = false
	}
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	unitDef := cat.Units["armcom"]
	unitDef.Corpse = "heap1"
	unitDef.MaxDamage = 100
	unitDef.CanMove = true

	h, _ := s.Units.Create(unitDef, 0, numeric.Fixed(10*16*65536), 0, numeric.Fixed(10*16*65536))
	u := s.Units.Unit(h)
	u.Health = -10
	u.MaxHealth = 100
	u.PriorSample = 80
	u.Remaining = 0
	prog := makeKilledProg(3)
	vm := cob.NewVM(prog)
	u.SetScript(vm)

	s.Units.Destroy(h, units.DeathKilled)
	s.Units.FinalizeDeath(h, 1)
	found := false
	for _, inst := range s.Features.Instances() {
		if inst.Def != nil && inst.Def.CanonicalKey == "heap3" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("corpse depth 3 expected heap3, got %v", s.Features.Instances())
	}
	h2, _ := s.Units.Create(unitDef, 0, numeric.Fixed(20*16*65536), 0, numeric.Fixed(20*16*65536))
	u2 := s.Units.Unit(h2)
	u2.Health = -10
	u2.MaxHealth = 100
	u2.PriorSample = 80
	u2.Remaining = 0.5
	vm2 := cob.NewVM(makeKilledProg(2))
	u2.SetScript(vm2)
	prevCount := len(s.Features.Instances())
	s.Units.Destroy(h2, units.DeathKilled)
	s.Units.FinalizeDeath(h2, 1)
	if len(s.Features.Instances()) != prevCount {
		t.Fatalf("remaining non-zero should force variant 0 no corpse [06 §12.1] C24, got %d new instances", len(s.Features.Instances())-prevCount)
	}
}

func TestDeathExplosionDamagesNeighbor(t *testing.T) {
	cat := strictMinimalCatalog()
	wdef := &content.WeaponDef{ID: 99, DamageDefault: 500, AreaOfEffect: 64, EdgeEffectiveness: 0}
	wdef.CanonicalKey = content.CanonicalKey("explodegun")
	cat.Weapons = map[string]*content.WeaponDef{"explodegun": wdef}
	cat.RebuildWeaponIndex()

	terrain := strictMinimalTerrain()
	terrain.CellW = 32
	terrain.CellH = 32

	explDef := cat.Units["armcom"]
	explDef.Corpse = "heap1"
	explDef.MaxDamage = 200
	explDef.ExplodeAs = "explodegun"
	explDef.ExplodeAsDef = wdef
	explDef.CanMove = true

	nbrDef := cat.Units["corcom"]
	nbrDef.MaxDamage = 200
	nbrDef.CanMove = true
	nbrDef.Corpse = ""

	corpse := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Damage: 100}
	corpse.CanonicalKey = content.CanonicalKey("heap1")
	cat.Features["heap1"] = corpse

	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.IsObserver = false
	}
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	s.Clock.GlobalTick = 10

	hExpl, _ := s.Units.Create(explDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	uExpl := s.Units.Unit(hExpl)
	uExpl.Health = -20
	uExpl.MaxHealth = 200
	uExpl.PriorSample = 80
	uExpl.Remaining = 0
	vmExpl := cob.NewVM(makeKilledProg(1))
	uExpl.SetScript(vmExpl)

	hNbr, _ := s.Units.Create(nbrDef, 1, world.CellToWorld(11), 0, world.CellToWorld(10))
	uNbr := s.Units.Unit(hNbr)
	uNbr.Health = 100
	uNbr.MaxHealth = 200
	uNbr.PriorSample = 0
	vmNbr := cob.NewVM(&cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}, Pieces: []string{"base"}})
	uNbr.SetScript(vmNbr)

	healthBefore := uNbr.Health
	s.Units.Destroy(hExpl, units.DeathKilled)
	s.Units.FinalizeDeath(hExpl, 10)
	if uNbr.Health >= healthBefore {
		t.Fatalf("exploding death should damage neighbor health %d -> %d [06 §12.1] DoExplosion", healthBefore, uNbr.Health)
	}
	found := false
	for _, inst := range s.Features.Instances() {
		if inst.Def != nil && inst.Def.CanonicalKey == "heap1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("exploder corpse not placed")
	}
	hNbr2, _ := s.Units.Create(nbrDef, 1, world.CellToWorld(12), 0, world.CellToWorld(10))
	uNbr2 := s.Units.Unit(hNbr2)
	uNbr2.Health = 10
	uNbr2.MaxHealth = 200
	uNbr2.SetScript(vmNbr)
	hExpl2, _ := s.Units.Create(explDef, 0, world.CellToWorld(13), 0, world.CellToWorld(10))
	uExpl2 := s.Units.Unit(hExpl2)
	uExpl2.Health = -20
	uExpl2.MaxHealth = 200
	uExpl2.PriorSample = 80
	uExpl2.Remaining = 0
	uExpl2.SetScript(cob.NewVM(makeKilledProg(1)))
	uExpl2.X = world.CellToWorld(12)
	uExpl2.Z = world.CellToWorld(10)
	s.Units.Destroy(hExpl2, units.DeathKilled)
	s.Units.FinalizeDeath(hExpl2, 10)
	if uNbr2.Alive && !uNbr2.Dying {
		if uNbr2.Health >= 10 {
			t.Fatalf("lethal explosion should kill or damage neighbor2 health %d", uNbr2.Health)
		}
	}
}

// TestDeathExplosionCreditsNoOwner locks [06 R-WPN-02 §5] and [06 R-DMG-01 §9]
// at the session's death-explosion call site: a unit killed outright by
// another unit's death explosion must not credit the dying unit's owner (or
// any owner) with a kill, because the record carries no shooter. Before this
// fix, session.go passed the dying unit's own still-resolvable handle as
// ExplodeWeaponAt's shooter (Destroy sets Dying but Alive is not cleared
// until FinalizeDeath, which runs later), so the neighbor's provenance was
// stamped with the dying unit's owner and its death credited that owner a
// kill it never earned.
func TestDeathExplosionCreditsNoOwner(t *testing.T) {
	cat := strictMinimalCatalog()
	wdef := &content.WeaponDef{ID: 99, DamageDefault: 5000, AreaOfEffect: 64, EdgeEffectiveness: 0}
	wdef.CanonicalKey = content.CanonicalKey("explodegun")
	cat.Weapons = map[string]*content.WeaponDef{"explodegun": wdef}
	cat.RebuildWeaponIndex()

	terrain := strictMinimalTerrain()
	terrain.CellW = 32
	terrain.CellH = 32

	explDef := cat.Units["armcom"]
	explDef.Corpse = ""
	explDef.MaxDamage = 200
	explDef.ExplodeAs = "explodegun"
	explDef.ExplodeAsDef = wdef
	explDef.CanMove = true

	nbrDef := cat.Units["corcom"]
	nbrDef.MaxDamage = 200
	nbrDef.CanMove = true
	nbrDef.Corpse = ""

	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.IsObserver = false
	}
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	s.Clock.GlobalTick = 10

	// hExpl (owner 0) dies and explodes; hNbr (owner 1) sits well within the
	// blast and takes lethal damage from it, so the credit path actually runs.
	hExpl, _ := s.Units.Create(explDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	uExpl := s.Units.Unit(hExpl)
	uExpl.Health = -20
	uExpl.MaxHealth = 200
	uExpl.PriorSample = 80
	uExpl.Remaining = 0
	uExpl.SetScript(cob.NewVM(makeKilledProg(1)))

	hNbr, _ := s.Units.Create(nbrDef, 1, world.CellToWorld(11), 0, world.CellToWorld(10))
	uNbr := s.Units.Unit(hNbr)
	uNbr.Health = 1
	uNbr.MaxHealth = 200
	uNbr.PriorSample = 0
	uNbr.SetScript(cob.NewVM(&cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}, Pieces: []string{"base"}}))

	killsBefore0 := s.Econ.Players[0].Kills
	killsBefore1 := s.Econ.Players[1].Kills

	s.Units.Destroy(hExpl, units.DeathKilled)
	s.Units.FinalizeDeath(hExpl, 10)

	if uNbr.Health > 0 && !uNbr.Dying {
		t.Fatalf("neighbor should take lethal damage from the death explosion, health=%d dying=%v", uNbr.Health, uNbr.Dying)
	}
	// Drain the neighbor's own death finalization, exactly as the session's
	// phase-2 sweep would, so recordFinalizedDeathStatistics runs for it too.
	if s.Units.NeedsDeathFinalization(hNbr) {
		s.Units.FinalizeDeath(hNbr, 10)
	}

	if got := s.Econ.Players[0].Kills; got != killsBefore0 {
		t.Fatalf("death explosion must not credit the dying unit's own owner (side 10, not owner 0) [06 R-WPN-02 §5][06 R-DMG-01 §9]: owner 0 kills %d -> %d", killsBefore0, got)
	}
	if got := s.Econ.Players[1].Kills; got != killsBefore1 {
		t.Fatalf("death explosion must not credit the victim's own owner either: owner 1 kills %d -> %d", killsBefore1, got)
	}
}

func TestKilledDedupAcrossHandleReuse(t *testing.T) {
	cat := strictMinimalCatalog()
	terrain := strictMinimalTerrain()
	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.IsObserver = false
	}
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	def := cat.Units["armcom"]
	def.MaxDamage = 100
	def.Corpse = ""
	h1, _ := s.Units.Create(def, 0, numeric.Fixed(10*16*65536), 0, numeric.Fixed(10*16*65536))
	count := 0
	origOnDeath := s.Units.OnDeath
	s.Units.OnDeath = func(h pool.Handle, c units.DeathCause, u *units.Unit) {
		count++
		if origOnDeath != nil {
			origOnDeath(h, c, u)
		}
	}
	s.Units.Destroy(h1, units.DeathKilled)
	if count != 0 {
		t.Fatalf("first destroy should defer hook until finalization, got %d", count)
	}
	s.Units.Destroy(h1, units.DeathKilled)
	if count != 0 {
		t.Fatalf("second destroy on already dying should not fire hook again got %d", count)
	}
	s.Units.FinalizeDeath(h1, 1)
	if count != 1 {
		t.Fatalf("first finalization should fire hook once got %d", count)
	}
	h2, _ := s.Units.Create(def, 0, numeric.Fixed(20*16*65536), 0, numeric.Fixed(20*16*65536))
	if h2 == h1 {
		s.Units.Destroy(h2, units.DeathKilled)
		s.Units.FinalizeDeath(h2, 1)
		if count != 2 {
			t.Fatalf("reused handle destroy should fire hook again got %d want 2 [01 §4.4] dedup", count)
		}
	} else {
		t.Logf("handle not reused h1=%d h2=%d, dedup still per handle", h1, h2)
		s.Units.Destroy(h2, units.DeathKilled)
		s.Units.FinalizeDeath(h2, 1)
		if count != 2 {
			t.Fatalf("second occupant finalization should fire hook got %d", count)
		}
	}
	if s.Combat != nil {
		_ = combat.ShouldDispatchReplayKilled
	}
}

func init() {
	_ = rng.NewSimulation
	_ = world.CellToWorld
	_ = combat.BlastRadius
}
