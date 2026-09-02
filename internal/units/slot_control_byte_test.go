package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// TestSlotInitializerWritesTheWholeControlByte locks [06 R-WPN-05 §3]: the
// weapon-slot initializer unit construction runs writes bit 1 from the resolved
// definition's active byte, bits 2-3 the slot's own index, bit 4 set, bit 0
// clear — and it is the only writer of the enabled bit, so a slot is never
// disabled during play.
func TestSlotInitializerWritesTheWholeControlByte(t *testing.T) {
	armed := &content.WeaponDef{ID: 1}
	u := &Unit{}
	// Slot 1 is deliberately left unlinked: an unlinked slot still carries its
	// own index, but neither enabled nor autonomy.
	installWeapons(u, &content.UnitDef{Weapon1Def: armed, Weapon3Def: &content.WeaponDef{ID: 3}})

	for idx, wantEnabled := range [NumSlots]bool{true, false, true} {
		s := u.SlotAt(idx)
		if s.SlotIndex() != idx {
			t.Fatalf("slot %d: index bits read %d — the record must describe itself", idx, s.SlotIndex())
		}
		if s.IsEnabled() != wantEnabled {
			t.Fatalf("slot %d: enabled %v, want %v", idx, s.IsEnabled(), wantEnabled)
		}
		if s.IsAutonomous() != wantEnabled {
			t.Fatalf("slot %d: autonomy %v, want %v — the initializer sets it so every linked slot starts autonomous", idx, s.IsAutonomous(), wantEnabled)
		}
		if s.Flags&SlotFlagAimLatch != 0 {
			t.Fatalf("slot %d: the initializer leaves the Aim latch clear", idx)
		}
	}
}

// TestSlotInitializerPreservesInertBits locks the byte's bits 5-7: the
// initializer preserves whatever the record held and nothing else writes them
// [06 R-WPN-05 §3].
func TestSlotInitializerPreservesInertBits(t *testing.T) {
	u := &Unit{}
	u.Slots[0].Flags = 0xE0
	installWeapons(u, &content.UnitDef{Weapon1Def: &content.WeaponDef{ID: 1}})
	if got := u.Slots[0].Flags & 0xE0; got != 0xE0 {
		t.Fatalf("inert bits 5-7 = %#02x, want 0xe0 preserved", got)
	}
}

// TestSlotControlByteRoundTripsThroughTheSave locks that all five live bits
// survive a save/load, which is the only thing that can change the enabled bit
// after construction [08 R-SAVE-WEAPON-01] [06 R-WPN-05 §3]. The two bytes this
// build used to keep — Flags plus an order-side OrderControl — needed the writer
// to fold bit 4 in by hand and the reader to split it back out.
func TestSlotControlByteRoundTripsThroughTheSave(t *testing.T) {
	u := &Unit{Handle: 7, Def: &content.UnitDef{UnitName: "armwriter"}, Alive: true, RestoredAIGroup: -1}
	// Every live bit set on one slot, only the index on another, and a byte
	// whose inert bits 5-7 must be discarded on both sides.
	u.Slots[0].Flags = SlotFlagAimLatch | SlotFlagEnabled | (2 << SlotFlagIndexShift) | SlotFlagAutonomous
	u.Slots[1].Flags = 1 << SlotFlagIndexShift
	u.Slots[2].Flags = 0xE0 | SlotFlagEnabled

	ids := map[pool.Handle]uint16{7: 9}
	image, err := RetailUnitImage(u, 0, func(h pool.Handle) (uint16, bool) { id, ok := ids[h]; return id, ok }, RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	v := &Unit{Handle: 8, Alive: true}
	if err := RetailUnitBase(v, image); err != nil {
		t.Fatal(err)
	}
	for idx, want := range [NumSlots]uint8{
		u.Slots[0].Flags,
		u.Slots[1].Flags,
		u.Slots[2].Flags & SlotFlagPersisted, // bits 5-7 are discarded by both sides
	} {
		if got := v.Slots[idx].Flags; got != want {
			t.Fatalf("slot %d control byte round-tripped as %#02x, want %#02x", idx, got, want)
		}
	}
}
