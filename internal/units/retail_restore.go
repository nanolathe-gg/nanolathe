package units

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

const retailUnitRecordSize = 0xB8

// RetailUnitBase restores the fixed fields of one standard 0xB8 unit image
// into a unit allocated by CreateWithForcedSlot. The adapter copies only
// fields represented by Unit; neutral fields retain the established
// persisted values whose consumers run in later phases [08 R-SAVE-02 §6].
func RetailUnitBase(u *Unit, data []byte) error {
	if u == nil {
		return fmt.Errorf("units: retail restore: nil unit")
	}
	if len(data) != retailUnitRecordSize {
		return fmt.Errorf("units: retail restore: unit image size %d, want 0xB8", len(data))
	}
	x := int32(binary.LittleEndian.Uint32(data[0x2B:]))
	y := int32(binary.LittleEndian.Uint32(data[0x2F:]))
	z := int32(binary.LittleEndian.Uint32(data[0x33:]))
	u.X, u.Y, u.Z = numeric.Fixed(x), numeric.Fixed(y), numeric.Fixed(z)
	u.Move.Bank = binary.LittleEndian.Uint16(data[0x37:])
	u.Move.Heading = binary.LittleEndian.Uint16(data[0x39:])
	u.Move.Pitch = binary.LittleEndian.Uint16(data[0x3B:])
	u.Health = int32(int16(binary.LittleEndian.Uint16(data[0x3D:])))
	u.Kills = int32(binary.LittleEndian.Uint16(data[0x3F:]))
	u.SpotMetal = math.Float32frombits(binary.LittleEndian.Uint32(data[0x8F:]))
	u.RestoredAIGroup = int32(binary.LittleEndian.Uint32(data[0x9F:]))
	u.Pending = uint32(binary.LittleEndian.Uint16(data[0xAE:]))
	u.Remaining = math.Float32frombits(binary.LittleEndian.Uint32(data[0xA7:]))
	u.HasMover = binary.LittleEndian.Uint32(data[0x27:]) != 0
	u.LastDamageCause = data[0xAB]
	u.DeathCause = DeathCause(data[0xAB])
	u.RelationDomainByte = data[0x8E]
	u.CachedOccupancyX = int16(binary.LittleEndian.Uint16(data[0x93:]))
	u.CachedOccupancyZ = int16(binary.LittleEndian.Uint16(data[0x95:]))
	u.SightCellX = int16(binary.LittleEndian.Uint16(data[0x97:]))
	u.SightCellZ = int16(binary.LittleEndian.Uint16(data[0x99:]))
	u.FootprintSizeX = int16(binary.LittleEndian.Uint16(data[0x9B:]))
	u.FootprintSizeZ = int16(binary.LittleEndian.Uint16(data[0x9D:]))
	u.RevealDeadline = binary.LittleEndian.Uint32(data[0xA3:])
	u.UnknownByteAC, u.UnknownByteAD = data[0xAC], data[0xAD]
	u.LOSByte, u.UnknownCountdownByte = data[0xB0], data[0xB1]
	// The allocator supplies identity and Alive. Invert only the established
	// persisted portions of the packed status word. Flags 12 and 26..31 are
	// not serialized and retain allocator state [08 R-SAVE-02 §6].
	packed := binary.LittleEndian.Uint32(data[0xB4:])
	flags := (packed >> 4) & 0x0fff
	flags |= (packed >> 3) & 0x2000
	flags |= (packed >> 6) & 0x03ffc000
	const serializedFlags = 0x03ffeFFF // bits 0..11, 13, and 14..25; bit 12 is allocator-owned
	u.Flags = (u.Flags &^ serializedFlags) | flags
	u.Move.Mode = uint8(flags & 3)
	u.RestoredMoveMode = true
	u.InBuildStance = packed&1 != 0
	u.Busy = packed&2 != 0
	u.YardOpen = packed&4 != 0
	u.BuggerOff = packed&8 != 0
	state := binary.LittleEndian.Uint16(data[0xB2:])
	u.Activated = state&1 != 0
	u.Armored = state&2 != 0
	u.IsCloaked = state&4 != 0
	u.BuildingState = state&8 != 0
	for i := 0; i < NumSlots; i++ {
		off := 0x41 + i*0x18
		s := &u.Slots[i]
		s.Reload = int32(int16(binary.LittleEndian.Uint16(data[off+0x10:])))
		s.DesiredYaw = binary.LittleEndian.Uint16(data[off+0x12:])
		s.DesiredPitch = binary.LittleEndian.Uint16(data[off+0x14:])
		s.Ammo = int32(data[off+0x16])
		s.Flags = (s.Flags &^ 0x1f) | (data[off+0x17] & 0x1f)
		s.SavedTargetLow = binary.LittleEndian.Uint16(data[off:])
		s.SavedTargetHigh = binary.LittleEndian.Uint16(data[off+2:])
		s.SavedActiveByte = data[off+0x08]
		s.SavedPayloadWord0 = binary.LittleEndian.Uint32(data[off+0x04:])
		s.SavedPayloadWord1 = binary.LittleEndian.Uint32(data[off+0x0C:])
		// Target identity is fixed up from the saved pair only after every
		// forced slot exists; do not treat the serialized low word as a live
		// pool handle in this scalar pass [08 R-SAVE-WEAPON-01].
		s.Target = Target{}
	}
	return nil
}

// RetailUnitWeaponTargets resolves the saved unit-mode target words after all
// stable slots have been forced-allocated. Ground pairs remain signed 16-bit
// coordinates widened to 16.16 [08 R-SAVE-WEAPON-01].
func RetailUnitWeaponTargets(u *Unit, stable map[uint16]pool.Handle) error {
	if u == nil {
		return fmt.Errorf("units: retail restore: nil unit")
	}
	for i := range u.Slots {
		s := &u.Slots[i]
		if s.SavedTargetHigh == 0x8000 {
			if s.SavedTargetLow != 0 {
				h, ok := stable[s.SavedTargetLow]
				if !ok || h == 0 {
					return fmt.Errorf("units: retail restore: unresolved weapon target %d", s.SavedTargetLow)
				}
				s.Target.Unit = h
			}
			s.Target.Kind = TargetUnit
			continue
		}
		s.Target = Target{Kind: TargetGround, X: numeric.Fixed(int64(int16(s.SavedTargetLow)) << 16), Z: numeric.Fixed(int64(int16(s.SavedTargetHigh)) << 16)}
	}
	return nil
}

// RetailUnitReferences installs the two logical cross-unit links after every
// fixed slot has been allocated. The first is the carrier relationship; the
// second is a plain engagement link and has no attachment side effect [08
// R-SAVE-02 §6].
func RetailUnitReferences(u *Unit, carrier, engagement pool.Handle, attachPiece uint8) error {
	if u == nil {
		return fmt.Errorf("units: retail restore: nil unit")
	}
	if (carrier != 0 && carrier == u.Handle) || (engagement != 0 && engagement == u.Handle) {
		return fmt.Errorf("units: retail restore: self reference %d", u.Handle)
	}
	u.Attachment.Carrier = carrier
	u.EngagementTarget = engagement
	u.Attachment.AttachPiece = -1
	if carrier != 0 {
		u.Attachment.AttachPiece = int(attachPiece)
	}
	return nil
}
