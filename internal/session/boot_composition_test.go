package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestSessionBootPopulatesCommanders verifies that a configured battle starts
// with authored commander definitions, ownership, placement, COB bindings,
// and weapon slots [08 "Skirmish configuration"][02 §6.2].
func TestSessionBootPopulatesCommanders(t *testing.T) {
	rng.SeedGlobal(1, 2)
	cat := strictMinimalCatalog()
	wdef := &content.WeaponDef{
		ID: 1, Name: "testgun", WeaponVelocity: 65536 * 5,
		Range: 5000, ReloadTime: 2, Damage: map[string]int32{"default": 100},
	}
	wdef.CanonicalKey = content.CanonicalKey(wdef.Name)
	cat.Weapons = map[string]*content.WeaponDef{wdef.CanonicalKey: wdef}
	cat.RebuildWeaponIndex()
	for _, def := range cat.Units {
		def.Weapon1 = wdef.CanonicalKey
		def.Weapon1Def = wdef
	}
	s := &Session{Catalog: cat, World: strictMinimalTerrain(), Mission: strictSyntheticMission()}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("new unit world: %v", err)
	}
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.SetSettlementStatusPair(1, 0)
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind services: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	for i, key := range []string{"armcom", "corcom"} {
		def := cat.Units[key]
		x := strictCellToWorld(int32(i*10 + 5))
		z := strictCellToWorld(int32(i*10 + 5))
		y := s.World.HeightAt(x, z)
		if y == numeric.Fixed(-1) {
			y = 0
		}
		h, err := s.Units.Create(def, uint8(i), x, y, z)
		if err != nil {
			t.Fatalf("create commander %d: %v", i, err)
		}
		u := s.Units.Unit(h)
		if u == nil {
			t.Fatalf("commander %d missing", i)
		}
		// Creation succeeds only after attaching a loadable program
		// [R-COB-04 §8]. The fixture catalog supplies an authored no-op COB so
		// this composition test exercises the same mandatory binding boundary.
		if u.GetScript() == nil {
			t.Fatalf("commander %d did not bind its fixture COB VM", i)
		}
		ensureMovementForAll(s)
		publishVisibilityForAll(s)
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("validate composition: %v", err)
	}
	count := 0
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		count++
		if u.Owner > 1 || u.Def == nil || (u.Def.UnitName != "armcom" && u.Def.UnitName != "corcom") {
			t.Fatalf("unexpected commander record: owner=%d def=%v", u.Owner, u.Def)
		}
		if u.X.Raw() == 0 && u.Z.Raw() == 0 {
			t.Fatalf("commander %d was not placed", u.Handle)
		}
		hasWeapon := false
		for slot := 0; slot < 3; slot++ {
			if s := u.SlotAt(slot); s != nil && s.IsPopulated() {
				hasWeapon = true
			}
		}
		if !hasWeapon {
			t.Fatalf("commander %d lacks authored weapon slot", u.Handle)
		}
	}
	if count != 2 {
		t.Fatalf("commander count=%d, want 2", count)
	}
	s.Clock.ScaledAnchor = 0
	s.Step(1)
	if s.State != StateBattle {
		t.Fatalf("session left battle after first tick: %v", s.State)
	}
}

func strictCellToWorld(c int32) numeric.Fixed {
	return numeric.Fixed(int64(c) * 16 * 65536)
}
