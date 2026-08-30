package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestOW0D_MuzzlePieceDefaultIsNegativeOne(t *testing.T) {
	def := &content.UnitDef{
		UnitName:   "testunit",
		MaxDamage:  100,
		FootprintX: 1,
		FootprintZ: 1,
		Weapon1Def: &content.WeaponDef{ID: 1},
		Weapon2Def: nil,
		Weapon3Def: &content.WeaponDef{ID: 3},
	}
	// Every creation-time query is present and returns -1, the authored root
	// fallback value [04 §5.3].
	def.Script = &cob.Program{
		Code: []uint32{0x10021001, ^uint32(0), 0x10023002, 0, 0x10065000},
		Scripts: map[string]int{
			"Create": 0, "QueryPrimary": 0, "QuerySecondary": 0, "QueryTertiary": 0,
			"AimFromPrimary": 0, "AimFromSecondary": 0, "AimFromTertiary": 0,
		},
		ScriptsByID: []int{0, 0, 0, 0, 0, 0, 0},
	}
	w := newFixtureWorld(10, nil)
	// Use installWeapons directly via Create path (which calls installWeapons)
	h, err := w.Create(def, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	u := w.Unit(h)
	if u == nil {
		t.Fatalf("unit nil")
	}
	// [04 §5.3] [06 §4.1] C3: a -1 query result falls back to root.
	for i := 0; i < NumSlots; i++ {
		if u.Slots[i].MuzzlePiece != -1 {
			t.Fatalf("slot %d MuzzlePiece=%d want -1 [04 §5.3] root fallback", i, u.Slots[i].MuzzlePiece)
		}
	}
	// Installed weapons retain -1 initial, will be overwritten by successful Query when COB present
	if u.Slots[0].Weapon == nil {
		t.Fatalf("slot0 weapon not installed")
	}
	if u.Slots[2].Weapon == nil {
		t.Fatalf("slot2 weapon not installed")
	}
}
