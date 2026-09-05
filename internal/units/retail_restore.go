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
	// The saved byte is retail's death-cause byte, "the cause code recorded by
	// the last damage packet" [08 R-SAVE-02 §6] — a damage-kind value in the
	// sixteen-value enumeration of [06 §12.1]. It restores verbatim into the
	// field that holds that enumeration; the coarse label is DERIVED from it,
	// because the two do not share a numbering. Casting the byte straight into
	// DeathCause (as this did) filed a restored reclaim victim, kind 5, as an
	// enum value with no member at all, and made retail's paralyze kind 2 read
	// back as DeathReclaimed.
	u.LastDamageCause = data[0xAB]
	u.DeathCause = DeathCauseFromKind(data[0xAB])
	u.RelationDomainByte = data[0x8E]
	u.CachedOccupancyX = int16(binary.LittleEndian.Uint16(data[0x93:]))
	u.CachedOccupancyZ = int16(binary.LittleEndian.Uint16(data[0x95:]))
	u.SightCellX = int16(binary.LittleEndian.Uint16(data[0x97:]))
	u.SightCellZ = int16(binary.LittleEndian.Uint16(data[0x99:]))
	u.FootprintSizeX = int16(binary.LittleEndian.Uint16(data[0x9B:]))
	u.FootprintSizeZ = int16(binary.LittleEndian.Uint16(data[0x9D:]))
	u.RevealDeadline = binary.LittleEndian.Uint32(data[0xA3:])
	// 0xAC/0xAD are the current/previous health samples the tick-30 roll
	// rotates and 0xB1 the damage-flash byte [08 R-SAVE-02 §14].
	u.CurrentSample, u.PriorSample = data[0xAC], data[0xAD]
	u.LOSByte, u.BlinkSuppress = data[0xB0], int8(data[0xB1])
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
	// The cloak-REQUESTED bit is restored from the persisted STATUS word, bit
	// 11 [05 R-ECO-01 §9] — the same masked field the constructor seeds from
	// `init_cloaked` — while the operational byte below carries the INSTANCE
	// cloaked bit. Two bits, two sources; the bool is the runtime authority and
	// the flag word keeps its restored copy so the save writer round-trips.
	u.IsCloaked = flags&CloakRequestedStatus != 0
	state := binary.LittleEndian.Uint16(data[0xB2:])
	u.Activated = state&1 != 0
	u.Armored = state&2 != 0
	// Operational byte bit 2: the INSTANCE cloaked bit [05 R-ECO-01 §8].
	u.Hidden = state&4 != 0
	u.BuildingState = state&8 != 0
	for i := 0; i < NumSlots; i++ {
		off := 0x41 + i*0x18
		s := &u.Slots[i]
		s.Reload = int32(int16(binary.LittleEndian.Uint16(data[off+0x10:])))
		s.DesiredYaw = binary.LittleEndian.Uint16(data[off+0x12:])
		s.DesiredPitch = binary.LittleEndian.Uint16(data[off+0x14:])
		s.Ammo = int32(data[off+0x16])
		// The persisted byte restores the whole control byte, bits 0-4 — aim
		// latch, enabled, the slot's index, autonomy — over one field
		// [08 R-SAVE-WEAPON-01] [06 R-WPN-05 §3]. Save load is the only thing
		// that can change the enabled bit after construction. Bits 5-7 are
		// inert and the reader discards them, as the writer does.
		s.Flags = (s.Flags &^ SlotFlagPersisted) | (data[off+0x17] & SlotFlagPersisted)
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
