package units

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestRetailUnitImageRoundTripEstablishedFieldsAndStableIDs(t *testing.T) {
	u := &Unit{
		Handle: 41, Def: &content.UnitDef{UnitName: "armwriter"}, Owner: 2, Alive: true,
		X: 3 << 16, Y: -4 << 16, Z: 5 << 16, Health: -7, Kills: 12,
		Flags: 0x0255e321, InBuildStance: true, Busy: true, YardOpen: true,
		Activated: true, Armored: true, IsCloaked: true, BuildingState: true,
		HasMover: true, RestoredAIGroup: -1, LastDamageSide: 10,
		CachedOccupancyX: -3, CachedOccupancyZ: 4, SightCellX: 5, SightCellZ: -6,
		FootprintSizeX: 2, FootprintSizeZ: 3, RevealDeadline: 77,
		Remaining: .5, LastDamageCause: byte(DeathReclaimed), CurrentSample: 8,
		PriorSample: 9, Pending: 0x1234, LOSByte: 6, BlinkSuppress: -16,
		Attachment: AttachmentState{Carrier: 42, AttachPiece: 7}, EngagementTarget: 43,
	}
	u.Move.Bank, u.Move.Heading, u.Move.Pitch = 11, 12, 13
	u.Slots[0] = Slot{Target: Target{Kind: TargetUnit, Unit: 44}, Reload: -1, DesiredYaw: 14, DesiredPitch: 15, Ammo: 9, Flags: 0xff, SavedActiveByte: 1, SavedPayloadWord0: 0x11223344, SavedPayloadWord1: 0x55667788}
	u.Slots[1] = Slot{Target: Target{Kind: TargetGround, X: numeric.Fixed(-2 << 16), Z: numeric.Fixed(3 << 16)}}
	ids := map[pool.Handle]uint16{41: 9, 42: 10, 43: 11, 44: 12}
	resolve := func(h pool.Handle) (uint16, bool) { id, ok := ids[h]; return id, ok }
	image, err := RetailUnitImage(u, 4, resolve, RetailUnitWriterScratch{PackedStatusBits17To19: 0xa0000})
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(image[0x21:]) != 9 || binary.LittleEndian.Uint16(image[0x89:]) != 10 || binary.LittleEndian.Uint16(image[0x8b:]) != 11 || binary.LittleEndian.Uint16(image[0x41:]) != 12 {
		t.Fatalf("writer used handles rather than stable IDs: %x", image)
	}
	if binary.LittleEndian.Uint32(image[0xb4:])&0xe0000 != 0xa0000 {
		t.Fatal("explicit packed-word scratch was not retained")
	}
	v := &Unit{Handle: 90, Alive: true, Flags: u.Flags & (1<<12 | 0xfc000000)}
	if err := RetailUnitBase(v, image); err != nil {
		t.Fatal(err)
	}
	if err := RetailUnitWeaponTargets(v, map[uint16]pool.Handle{12: 94}); err != nil {
		t.Fatal(err)
	}
	if v.X != u.X || v.Y != u.Y || v.Z != u.Z || v.Health != u.Health || v.Move.Heading != u.Move.Heading || v.Slots[0].Target.Unit != 94 || v.Slots[1].Target.Kind != TargetGround || v.Slots[1].Target.X != -2<<16 || v.Pending != uint32(uint16(u.Pending)) {
		t.Fatalf("round trip mismatch: %#v", v)
	}
	// 0xAC/0xAD carry the two health samples and 0xB1 the damage-flash byte
	// [08 R-SAVE-02 §14]; the flash byte's 240 crosses the codec as the raw
	// byte, so the signed carrier reads back as it was written.
	if image[0xac] != 8 || image[0xad] != 9 || image[0xb1] != 0xf0 {
		t.Fatalf("named bytes: ac=%#x ad=%#x b1=%#x, want 8 9 0xf0 [08 R-SAVE-02 §14]", image[0xac], image[0xad], image[0xb1])
	}
	if v.CurrentSample != 8 || v.PriorSample != 9 || v.BlinkSuppress != -16 {
		t.Fatalf("restored samples/flash = %d %d %d, want 8 9 -16 [08 R-SAVE-02 §14]", v.CurrentSample, v.PriorSample, v.BlinkSuppress)
	}
}

// targetPairFixture builds the minimum image a base restore accepts, carrying
// the given slot-0 target pair at record bytes 0x41..0x44
// [08 R-SAVE-WEAPON-01].
func targetPairFixture(low, high uint16) []byte {
	data := make([]byte, retailUnitRecordSize)
	copy(data, []byte("pairwire"))
	// Slots 1 and 2 carry the empty encoding so the fixture has exactly one
	// interesting slot.
	for i := 1; i < NumSlots; i++ {
		putSlotPair(data, i, 0, 0x8000)
	}
	putSlotPair(data, 0, low, high)
	return data
}

// slotRecordAt is the position of weapon-slot record n in the unit image: the
// three records are fixed 24-byte payloads starting at record byte 0x41
// [08 R-SAVE-WEAPON-01].
func slotRecordAt(i int) int { return 0x41 + i*0x18 }

func putSlotPair(data []byte, i int, low, high uint16) {
	off := slotRecordAt(i)
	binary.LittleEndian.PutUint16(data[off:], low)
	binary.LittleEndian.PutUint16(data[off+2:], high)
}

func pairFixtureUnit() *Unit {
	return &Unit{
		Handle: 1, Def: &content.UnitDef{UnitName: "pairwire"}, Owner: 1, Alive: true,
		RestoredAIGroup: -1, LastDamageSide: NeutralAttackerSide,
	}
}

func slotPair(image []byte, i int) (uint16, uint16) {
	off := slotRecordAt(i)
	return binary.LittleEndian.Uint16(image[off:]), binary.LittleEndian.Uint16(image[off+2:])
}

// TestRetailWeaponTargetPairWireEncoding locks the four cases of the target
// pair at record bytes 0x00..0x03 of each weapon slot [08 R-SAVE-WEAPON-01]
// ("The target encoding is complete in the first four bytes"). The empty form
// is index zero carrying the unit sentinel — the pair the target-point
// resolver writes when its target is freed, and the pair it reads back as no
// target [06 R-WPN-04 §1]. A zero pair is a real ground point (0,0), never
// empty.
//
// The writer used to start from the restore scratch and emit it unchanged for
// a TargetNone slot. A slot cleared the ordinary way — orders' clearSlotTarget
// sets Kind without touching the scratch — therefore re-saved the stale ID of
// the unit it no longer aims at, and a freshly built unit with zero scratch
// saved the ground point (0,0).
func TestRetailWeaponTargetPairWireEncoding(t *testing.T) {
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h) + 100, true }

	t.Run("empty pair round trips", func(t *testing.T) {
		u := pairFixtureUnit()
		if err := RetailUnitBase(u, targetPairFixture(0, 0x8000)); err != nil {
			t.Fatal(err)
		}
		if err := RetailUnitWeaponTargets(u, nil); err != nil {
			t.Fatal(err)
		}
		if u.Slots[0].Target.Kind != TargetNone {
			t.Fatalf("(0,0x8000) decoded as kind %d, want TargetNone [06 R-WPN-04 §1]", u.Slots[0].Target.Kind)
		}
		image, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range u.Slots {
			if low, high := slotPair(image, i); low != 0 || high != 0x8000 {
				t.Fatalf("slot %d re-saved as (%#x,%#x), want the empty encoding (0,0x8000)", i, low, high)
			}
		}
	})

	t.Run("ordinary clear writes the empty encoding", func(t *testing.T) {
		u := pairFixtureUnit()
		if err := RetailUnitBase(u, targetPairFixture(5, 0x8000)); err != nil {
			t.Fatal(err)
		}
		if err := RetailUnitWeaponTargets(u, map[uint16]pool.Handle{5: 44}); err != nil {
			t.Fatal(err)
		}
		if u.Slots[0].Target.Kind != TargetUnit || u.Slots[0].Target.Unit != 44 {
			t.Fatalf("unit-mode pair decoded as %+v", u.Slots[0].Target)
		}
		// Exactly what orders' clearSlotTarget does: the kind goes to TargetNone
		// and nothing touches the restore scratch.
		u.Slots[0].Target = Target{Kind: TargetNone}
		image, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		if low, high := slotPair(image, 0); low != 0 || high != 0x8000 {
			t.Fatalf("a cleared slot saved as (%#x,%#x), want (0,0x8000); the stale scratch resurrected the target", low, high)
		}
	})

	t.Run("cleared target that has since died reloads", func(t *testing.T) {
		u := pairFixtureUnit()
		if err := RetailUnitBase(u, targetPairFixture(5, 0x8000)); err != nil {
			t.Fatal(err)
		}
		if err := RetailUnitWeaponTargets(u, map[uint16]pool.Handle{5: 44}); err != nil {
			t.Fatal(err)
		}
		u.Slots[0].Target = Target{Kind: TargetNone}
		image, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		v := pairFixtureUnit()
		if err := RetailUnitBase(v, image); err != nil {
			t.Fatal(err)
		}
		// Stable ID 5 is gone from the staged image: the unit was removed
		// between the clear and the save. The reload must still succeed.
		if err := RetailUnitWeaponTargets(v, map[uint16]pool.Handle{}); err != nil {
			t.Fatalf("reload after the target died: %v", err)
		}
		if v.Slots[0].Target.Kind != TargetNone {
			t.Fatalf("reloaded kind %d, want TargetNone", v.Slots[0].Target.Kind)
		}
	})

	t.Run("ground origin stays a point", func(t *testing.T) {
		u := pairFixtureUnit()
		if err := RetailUnitBase(u, targetPairFixture(0, 0)); err != nil {
			t.Fatal(err)
		}
		if err := RetailUnitWeaponTargets(u, nil); err != nil {
			t.Fatal(err)
		}
		if u.Slots[0].Target != (Target{Kind: TargetGround}) {
			t.Fatalf("(0,0) decoded as %+v, want the ground point (0,0) [08 R-SAVE-WEAPON-01]", u.Slots[0].Target)
		}
		image, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		if low, high := slotPair(image, 0); low != 0 || high != 0 {
			t.Fatalf("ground origin re-saved as (%#x,%#x), want (0,0)", low, high)
		}
	})

	t.Run("unit target keeps the sentinel", func(t *testing.T) {
		u := pairFixtureUnit()
		u.Slots[0].Target = Target{Kind: TargetUnit, Unit: 7}
		image, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		if low, high := slotPair(image, 0); low != 107 || high != 0x8000 {
			t.Fatalf("unit target saved as (%d,%#x), want (107,0x8000)", low, high)
		}
	})
}

func TestRetailUnitImageRequiresExplicitValidScratchAndResolver(t *testing.T) {
	u := &Unit{Handle: 20, Def: &content.UnitDef{UnitName: "u"}, Alive: true}
	if _, err := RetailUnitImage(u, 0, nil, RetailUnitWriterScratch{}); err == nil {
		t.Fatal("missing stable-ID resolver accepted")
	}
	resolve := func(pool.Handle) (uint16, bool) { return 1, true }
	if _, err := RetailUnitImage(u, 0, resolve, RetailUnitWriterScratch{PackedStatusBits17To19: 1}); err == nil {
		t.Fatal("out-of-mask writer scratch accepted")
	}
}
