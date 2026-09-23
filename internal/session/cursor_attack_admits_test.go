package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestCursorAttackAdmitsRebuildsSlotZero locks the immobile attacker's cursor
// range test [07 §8]: a committed-view copy carries no weapon slots, so slot 0
// is rebuilt from the definition's first weapon link, the planar range test is
// inclusive [06 §3.3], and over open ground a `toairweapon` slot refuses even
// in range.
func TestCursorAttackAdmitsRebuildsSlotZero(t *testing.T) {
	s := &Session{World: &world.Terrain{}, Combat: &combat.Service{}}
	at := func(x int32) numeric.Fixed { return numeric.Fixed(x) << 16 }
	gun := &content.WeaponDef{ID: 1, Range: 100}
	tower := &units.Unit{Alive: true, Y: at(10), Def: &content.UnitDef{UnitName: "ARMLLT", CanAttack: true, Weapon1Def: gun}}
	target := func(x int32) *units.Unit {
		return &units.Unit{Alive: true, X: at(x), Y: at(10), Owner: 1, Def: &content.UnitDef{UnitName: "ARMPW"}}
	}
	if !s.CursorAttackAdmits(tower, target(100), 0, 0, 0) {
		t.Errorf("unit at exactly range refused; the planar test is inclusive [06 §3.3]")
	}
	if s.CursorAttackAdmits(tower, target(101), 0, 0, 0) {
		t.Errorf("unit beyond range admitted [07 §8]")
	}
	if !s.CursorAttackAdmits(tower, nil, at(100), at(10), 0) || s.CursorAttackAdmits(tower, nil, at(101), at(10), 0) {
		t.Errorf("ground point range test wrong [07 §8][06 R-WPN-05 §9]")
	}
	if tower.SlotAt(0).Weapon != nil {
		t.Errorf("the query wrote a slot onto the caller's copy [I6]")
	}
	aa := &units.Unit{Alive: true, Y: at(10), Def: &content.UnitDef{UnitName: "ARMRL", CanAttack: true,
		Weapon1Def: &content.WeaponDef{ID: 2, Range: 100, ToAirWeapon: true}}}
	if s.CursorAttackAdmits(aa, nil, at(10), at(10), 0) {
		t.Errorf("toairweapon slot admitted a ground point in range; want cursortoofar [07 §8]")
	}
}
