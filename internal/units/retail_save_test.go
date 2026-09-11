package units

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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
	u.Slots[0] = Slot{Target: Target{Kind: TargetUnit, Unit: 44}, Reload: -1, DesiredYaw: 14, DesiredPitch: 15, Ammo: 9, Flags: 0xff, Weapon: &content.WeaponDef{ID: 1}, DistanceWord: -0x11223344}
	u.Slots[0].Aim.RestoreReadyWord(0x11223344)
	u.Slots[1] = Slot{Target: Target{Kind: TargetGround, X: numeric.Fixed(-2 << 16), Z: numeric.Fixed(3 << 16)}}
	ids := map[pool.Handle]uint16{41: 9, 42: 10, 43: 11, 44: 12}
	resolve := func(h pool.Handle) (uint16, bool) { id, ok := ids[h]; return id, ok }
	image, err := RetailUnitImage(u, 4, resolve, resolve, RetailUnitWriterScratch{PackedStatusBits17To19: 0xa0000})
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
	if v.Slots[0].Aim.ReadyWord() != 0x11223344 || !v.Slots[0].Aim.IssueBit || v.Slots[0].DistanceWord != u.Slots[0].DistanceWord {
		t.Fatal("saved readiness, request latch or ballistic distance was not restored")
	}
	if err := RetailUnitWeaponTargets(v, retailTargetSlotMap(map[uint16]pool.Handle{12: 94})); err != nil {
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
		image, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
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
		if err := RetailUnitWeaponTargets(u, retailTargetSlotMap(map[uint16]pool.Handle{5: 44})); err != nil {
			t.Fatal(err)
		}
		if u.Slots[0].Target.Kind != TargetUnit || u.Slots[0].Target.Unit != 44 {
			t.Fatalf("unit-mode pair decoded as %+v", u.Slots[0].Target)
		}
		// Exactly what orders' clearSlotTarget does: the kind goes to TargetNone
		// and nothing touches the restore scratch.
		u.Slots[0].Target = Target{Kind: TargetNone}
		image, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
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
		if err := RetailUnitWeaponTargets(u, retailTargetSlotMap(map[uint16]pool.Handle{5: 44})); err != nil {
			t.Fatal(err)
		}
		u.Slots[0].Target = Target{Kind: TargetNone}
		image, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
		if err != nil {
			t.Fatal(err)
		}
		v := pairFixtureUnit()
		if err := RetailUnitBase(v, image); err != nil {
			t.Fatal(err)
		}
		// Stable ID 5 is gone from the staged image: the unit was removed
		// between the clear and the save. The reload must still succeed.
		if err := RetailUnitWeaponTargets(v, retailTargetSlotMap(map[uint16]pool.Handle{})); err != nil {
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
		image, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
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
		image, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
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
	if _, err := RetailUnitImage(u, 0, nil, nil, RetailUnitWriterScratch{}); err == nil {
		t.Fatal("missing stable-ID resolver accepted")
	}
	resolve := func(pool.Handle) (uint16, bool) { return 1, true }
	if _, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{PackedStatusBits17To19: 1}); err == nil {
		t.Fatal("out-of-mask writer scratch accepted")
	}
}

// A carrier or engagement-target link whose unit is no longer live is written
// as 0, never refused: both words are "the stable slot, or 0 when absent or
// dead" [08 R-SAVE-02 §6]. The weapon-slot unit target has no such clause and
// uses an independent pool-slot resolver [08 R-SAVE-WEAPON-01].
func TestRetailUnitImageDeadOptionalLinksWriteZero(t *testing.T) {
	u := &Unit{
		Handle: 20, Def: &content.UnitDef{UnitName: "u"}, Alive: true,
		Attachment: AttachmentState{Carrier: 30}, EngagementTarget: 31,
	}
	// Only the unit itself is live; 30 and 31 are freed slots.
	resolve := func(h pool.Handle) (uint16, bool) {
		if h == 20 {
			return 1, true
		}
		return 0, false
	}
	image, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{})
	if err != nil {
		t.Fatalf("dead optional links refused the save: %v [08 R-SAVE-02 §6]", err)
	}
	if carrier, engagement := binary.LittleEndian.Uint16(image[0x89:]), binary.LittleEndian.Uint16(image[0x8b:]); carrier != 0 || engagement != 0 {
		t.Fatalf("dead links wrote carrier=%d engagement=%d, want 0 0 [08 R-SAVE-02 §6]", carrier, engagement)
	}
	if image[0x8d] != 0xff {
		t.Fatalf("attach slot byte = %#x with a dead carrier, want 0xff [08 R-SAVE-02 §6]", image[0x8d])
	}
	u.Slots[0] = Slot{Target: Target{Kind: TargetUnit, Unit: 32}}
	if _, err := RetailUnitImage(u, 0, resolve, resolve, RetailUnitWriterScratch{}); err == nil {
		t.Fatal("a weapon slot without an explicit slot identity was accepted [08 R-SAVE-WEAPON-01]")
	}
}

// Pending death is a saved latch, independently of health or last damage kind
// [08 R-SAVE-02 §6]. Inspect the wire and author the reader input independently.
func TestRetailPendingDeathProjection(t *testing.T) {
	u := pairFixtureUnit()
	u.LastDamageCause = 1
	MarkDeath(u, DeathKilled, 2)
	live := func(h pool.Handle) (uint16, bool) { return uint16(h), h == 1 || h == 2 }
	image, err := RetailUnitImage(u, 0, live, live, RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(image[0xb4:])&(1<<20) == 0 || u.Flags&DeathPendingStatus != 0 {
		t.Fatal("pending death was omitted or saving changed the runtime flag mirror")
	}
	v := pairFixtureUnit()
	data := targetPairFixture(0, 0x8000)
	data[0xab] = 1
	binary.LittleEndian.PutUint32(data[0xb4:], 1<<20)
	if err := RetailUnitBase(v, data); err != nil {
		t.Fatal(err)
	}
	if !v.Dying || v.DeathCause != DeathKilled {
		t.Fatalf("pending death restore: %+v", v)
	}
	binary.LittleEndian.PutUint32(data[0xb4:], 0)
	if err := RetailUnitBase(v, data); err != nil {
		t.Fatal(err)
	}
	if v.Dying {
		t.Fatal("clear pending bit retained death latch from earlier state or nonfatal damage kind")
	}
	u.Dying = false
	u.Flags |= DeathPendingStatus
	image, err = RetailUnitImage(u, 0, live, live, RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(image[0xb4:])&(1<<20) != 0 {
		t.Fatal("stale flag mirror overrode the clear logical latch")
	}
}

func TestRetailWeaponDefinitionByteIsCanonical(t *testing.T) {
	u := pairFixtureUnit()
	defs := [NumSlots]*content.WeaponDef{{ID: 7}, {ID: 193}, {ID: 0}}
	u.Def.Weapon1Def, u.Def.Weapon2Def, u.Def.Weapon3Def = defs[0], defs[1], defs[2]
	installWeapons(u, u.Def)
	// Slot enabled is independent of the saved definition byte.
	u.Slots[0].Flags &^= SlotFlagEnabled
	live := func(h pool.Handle) (uint16, bool) { return uint16(h), h == 1 }
	image, err := RetailUnitImage(u, 0, live, live, RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []uint32{7, 193, 0} {
		if got := binary.LittleEndian.Uint32(image[slotRecordAt(i)+8:]); got != want {
			t.Errorf("slot %d definition byte=%d, want %d", i, got, want)
		}
	}
	for i, value := range []byte{0, 29, 0} {
		image[slotRecordAt(i)+8] = value
	}
	if err := RetailUnitWeaponDefinitions(u, image); err != nil {
		t.Fatal(err)
	}
	if !content.IsWeaponInactive(defs[0]) || defs[1].ActiveByte() != 29 || defs[1].ID != 193 {
		t.Fatal("restored definition byte did not reach the active predicate independently of identity")
	}
}

func TestRetailHeldFreeTargetUsesSlotResolver(t *testing.T) {
	u := pairFixtureUnit()
	u.Slots[0].Target = Target{Kind: TargetUnit, Unit: 5}
	u.EngagementTarget = 5
	live := func(h pool.Handle) (uint16, bool) { return uint16(h), h == 1 }
	slot := func(h pool.Handle) (uint16, bool) { return uint16(h), h > 0 && h < 8 }
	image, err := RetailUnitImage(u, 0, live, slot, RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	if low, high := slotPair(image, 0); low != 5 || high != 0x8000 {
		t.Fatalf("free target pair=%d,%x", low, high)
	}
	if binary.LittleEndian.Uint16(image[0x8b:]) != 0 {
		t.Fatal("optional object reference used pool membership as liveness")
	}
	if err := RetailUnitBase(u, image); err != nil {
		t.Fatal(err)
	}
	if err := RetailUnitWeaponTargets(u, func(id uint16) (pool.Handle, bool) { return pool.Handle(id), id > 0 && id < 8 }); err != nil {
		t.Fatal(err)
	}
	if u.Slots[0].Target.Unit != 5 {
		t.Fatal("reader lost free slot identity")
	}
	u.Slots[0].Target.Unit = 8
	if _, err := RetailUnitImage(u, 0, live, slot, RetailUnitWriterScratch{}); err == nil {
		t.Fatal("out-of-pool target accepted")
	}
}

func retailTargetSlotMap(slots map[uint16]pool.Handle) func(uint16) (pool.Handle, bool) {
	return func(id uint16) (pool.Handle, bool) { h, ok := slots[id]; return h, ok }
}
