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
		HasMover: true, RestoredAIGroup: -1, RelationDomainByte: 10,
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
