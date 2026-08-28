package units

import (
	"testing"

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
	w := NewSliced(10, nil)
	// Use installWeapons directly via Create path (which calls installWeapons)
	h, err := w.Create(def, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	u := w.Unit(h)
	if u == nil {
		t.Fatalf("unit nil")
	}
	// [04 §5.3] [06 §4.1] C3: Query-less COBs must fall back to root, so default must be -1
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
