package units

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestRetailUnitBaseRestoresEstablishedFields(t *testing.T) {
	def := &content.UnitDef{UnitName: "restore", MaxDamage: 100, Limit: -1}
	// The package fixture world has no strict binder, so install a script on a
	// directly allocated unit only after forcing the production identity path.
	u := &Unit{Handle: pool.Handle(1), Def: def, Alive: true, MaxHealth: 100, Flags: 1<<12 | 0xc0000000}
	data := make([]byte, retailUnitRecordSize)
	copy(data, []byte("restore"))
	data[0x20] = 2
	binary.LittleEndian.PutUint32(data[0x2B:], uint32(int32(3<<16)))
	binary.LittleEndian.PutUint16(data[0x39:], 0x1234)
	binary.LittleEndian.PutUint16(data[0x3D:], 0xfff9)
	binary.LittleEndian.PutUint32(data[0x8F:], math.Float32bits(4.5))
	binary.LittleEndian.PutUint32(data[0x27:], 0xa5a55a5a)
	binary.LittleEndian.PutUint32(data[0x9F:], 0xffffffff)
	data[0xA7] = 0x7f
	data[0xAB] = byte(DeathReclaimed)
	data[0xB2] = 0x07
	binary.LittleEndian.PutUint32(data[0xB4:], 1|(1<<20))
	if err := RetailUnitBase(u, data); err != nil {
		t.Fatal(err)
	}
	if u.X != 3<<16 || u.Move.Heading != 0x1234 || u.Health != -7 || u.SpotMetal != 4.5 || !u.HasMover || u.RestoredAIGroup != -1 || u.Dying || u.DeathCause != DeathReclaimed || !u.InBuildStance || u.Flags&0x4000 == 0 || u.Flags&(1<<12|0xc0000000) != (1<<12|0xc0000000) {
		t.Fatalf("restored fields: x=%v heading=%x health=%d metal=%v dying=%v cause=%v stance=%v", u.X, u.Move.Heading, u.Health, u.SpotMetal, u.Dying, u.DeathCause, u.InBuildStance)
	}
}

func TestRetailUnitBaseRestoresPackedMoverMirror(t *testing.T) {
	u := &Unit{Handle: 1, Alive: true}
	data := make([]byte, retailUnitRecordSize)
	// The established packed-status transform produces unit mirror mode 2.
	binary.LittleEndian.PutUint32(data[0xB4:], 2<<4)
	if err := RetailUnitBase(u, data); err != nil {
		t.Fatal(err)
	}
	if u.Move.Mode != 2 || !u.RestoredMoveMode {
		t.Fatalf("packed mover mirror=%d restored=%v, want mode 2", u.Move.Mode, u.RestoredMoveMode)
	}
}

func TestRetailUnitWeaponTargetAndPayloadFixup(t *testing.T) {
	u := &Unit{Handle: 3, Alive: true}
	data := make([]byte, retailUnitRecordSize)
	off := 0x41
	binary.LittleEndian.PutUint16(data[off:], 7)
	binary.LittleEndian.PutUint16(data[off+2:], 0x8000)
	binary.LittleEndian.PutUint32(data[off+4:], 0x11223344)
	data[off+8] = 1
	binary.LittleEndian.PutUint32(data[off+12:], 0x55667788)
	binary.LittleEndian.PutUint16(data[off+0x10:], 0xffff)
	data[off+0x17] = 0xff
	if err := RetailUnitBase(u, data); err != nil {
		t.Fatal(err)
	}
	if u.Slots[0].SavedActiveByte != 1 || u.Slots[0].SavedPayloadWord0 != 0x11223344 || u.Slots[0].SavedPayloadWord1 != 0x55667788 || u.Slots[0].Target.Kind != TargetNone {
		t.Fatalf("base pass interpreted target/payload early: %#v", u.Slots[0])
	}
	if err := RetailUnitWeaponTargets(u, map[uint16]pool.Handle{7: 11}); err != nil {
		t.Fatal(err)
	}
	if u.Slots[0].Target.Kind != TargetUnit || u.Slots[0].Target.Unit != 11 {
		t.Fatalf("target fixup: %#v", u.Slots[0].Target)
	}
}
