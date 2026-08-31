package units

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/pool"
)

// RetailStableID resolves a live handle to the logical unit identifier used by
// retail save records. Callers must supply the mapping; a pool handle is not
// assumed to be the persisted identifier [08 R-SAVE-02 §6].
type RetailStableID func(pool.Handle) (uint16, bool)

// RetailUnitWriterScratch carries the non-state bits that retail copies from
// its writer stack into the packed status word. They cannot be derived from a
// Unit and therefore must be supplied explicitly rather than silently zeroed.
type RetailUnitWriterScratch struct {
	PackedStatusBits17To19 uint32
}

// RetailUnitImage returns a detached 0xB8 base record. orderCount is supplied
// by the queue writer so the base record and the emitted order boxes cannot
// acquire separate ownership of queue traversal [08 R-SAVE-02 §6].
func RetailUnitImage(u *Unit, orderCount uint32, stableID RetailStableID, scratch RetailUnitWriterScratch) ([]byte, error) {
	if u == nil || !u.Alive || u.Def == nil {
		return nil, fmt.Errorf("units: retail save: unit is not live or has no definition")
	}
	id, err := resolveRetailStableID(stableID, u.Handle, "unit")
	if err != nil {
		return nil, err
	}
	if len(u.Def.UnitName) > 32 || strings.IndexByte(u.Def.UnitName, 0) >= 0 {
		return nil, fmt.Errorf("units: retail save: definition name %q is not a 32-byte save name", u.Def.UnitName)
	}
	if !fitsInt32(int64(u.X)) || !fitsInt32(int64(u.Y)) || !fitsInt32(int64(u.Z)) {
		return nil, fmt.Errorf("units: retail save: unit %d position is outside signed 32-bit 16.16", id)
	}
	if u.Health < math.MinInt16 || u.Health > math.MaxInt16 {
		return nil, fmt.Errorf("units: retail save: unit %d health %d is outside signed 16-bit", id, u.Health)
	}
	if scratch.PackedStatusBits17To19&^uint32(0xe0000) != 0 {
		return nil, fmt.Errorf("units: retail save: packed status scratch %#x exceeds bits 17..19", scratch.PackedStatusBits17To19)
	}

	data := make([]byte, retailUnitRecordSize)
	copy(data[:32], u.Def.UnitName)
	data[0x20] = u.Owner
	binary.LittleEndian.PutUint16(data[0x21:], id)
	binary.LittleEndian.PutUint32(data[0x23:], orderCount)
	if u.HasMover {
		binary.LittleEndian.PutUint32(data[0x27:], 1)
	}
	putI32(data[0x2b:], int32(u.X))
	putI32(data[0x2f:], int32(u.Y))
	putI32(data[0x33:], int32(u.Z))
	binary.LittleEndian.PutUint16(data[0x37:], u.Move.Bank)
	binary.LittleEndian.PutUint16(data[0x39:], u.Move.Heading)
	binary.LittleEndian.PutUint16(data[0x3b:], u.Move.Pitch)
	binary.LittleEndian.PutUint16(data[0x3d:], uint16(int16(u.Health)))
	binary.LittleEndian.PutUint16(data[0x3f:], uint16(u.Kills))

	for i := range u.Slots {
		if err := writeRetailWeaponSlot(data[0x41+i*0x18:], &u.Slots[i], stableID); err != nil {
			return nil, fmt.Errorf("units: retail save: unit %d weapon slot %d: %w", id, i, err)
		}
	}
	carrier, err := optionalRetailStableID(stableID, u.Attachment.Carrier, "carrier")
	if err != nil {
		return nil, err
	}
	engagement, err := optionalRetailStableID(stableID, u.EngagementTarget, "engagement target")
	if err != nil {
		return nil, err
	}
	binary.LittleEndian.PutUint16(data[0x89:], carrier)
	binary.LittleEndian.PutUint16(data[0x8b:], engagement)
	data[0x8d] = 0xff
	if carrier != 0 {
		if u.Attachment.AttachPiece < 0 || u.Attachment.AttachPiece > math.MaxUint8 {
			return nil, fmt.Errorf("units: retail save: unit %d attach piece %d is outside byte range", id, u.Attachment.AttachPiece)
		}
		data[0x8d] = byte(u.Attachment.AttachPiece)
	}
	data[0x8e] = u.RelationDomainByte
	binary.LittleEndian.PutUint32(data[0x8f:], math.Float32bits(u.SpotMetal))
	putI16(data[0x93:], u.CachedOccupancyX)
	putI16(data[0x95:], u.CachedOccupancyZ)
	putI16(data[0x97:], u.SightCellX)
	putI16(data[0x99:], u.SightCellZ)
	putI16(data[0x9b:], u.FootprintSizeX)
	putI16(data[0x9d:], u.FootprintSizeZ)
	binary.LittleEndian.PutUint32(data[0x9f:], uint32(u.RestoredAIGroup))
	binary.LittleEndian.PutUint32(data[0xa3:], u.RevealDeadline)
	binary.LittleEndian.PutUint32(data[0xa7:], math.Float32bits(u.Remaining))
	data[0xab] = u.LastDamageCause
	data[0xac], data[0xad] = u.UnknownByteAC, u.UnknownByteAD
	binary.LittleEndian.PutUint16(data[0xae:], uint16(u.Pending))
	data[0xb0], data[0xb1] = u.LOSByte, u.UnknownCountdownByte
	var state uint16
	if u.Activated {
		state |= 1
	}
	if u.Armored {
		state |= 2
	}
	if u.IsCloaked {
		state |= 4
	}
	if u.BuildingState {
		state |= 8
	}
	binary.LittleEndian.PutUint16(data[0xb2:], state)
	var stance uint32
	if u.InBuildStance {
		stance |= 1
	}
	if u.Busy {
		stance |= 2
	}
	if u.YardOpen {
		stance |= 4
	}
	if u.BuggerOff {
		stance |= 8
	}
	// Bits 17..19 are writer stack residue rather than unit state. Requiring
	// them from the caller keeps this projection byte-exact without inventing a
	// clearing rule [08 R-SAVE-02 §6].
	packed := stance | (u.Flags&0x0fff)<<4 | (u.Flags&0x2000)<<3 | (u.Flags&0x03ffc000)<<6 | scratch.PackedStatusBits17To19
	binary.LittleEndian.PutUint32(data[0xb4:], packed)
	return data, nil
}

func writeRetailWeaponSlot(data []byte, s *Slot, stableID RetailStableID) error {
	low, high := s.SavedTargetLow, s.SavedTargetHigh
	switch s.Target.Kind {
	case TargetNone:
		// The wire has no third target discriminator. Preserve the raw pair held
		// by a restored slot; a fresh zero pair is retail's point (0,0) image.
	case TargetUnit:
		var err error
		low, err = resolveRetailStableID(stableID, s.Target.Unit, "weapon target")
		if err != nil {
			return err
		}
		high = 0x8000
	case TargetGround:
		if int64(s.Target.X)&0xffff != 0 || int64(s.Target.Z)&0xffff != 0 {
			return fmt.Errorf("ground target is not integral in save coordinates")
		}
		x, z := int64(s.Target.X)>>16, int64(s.Target.Z)>>16
		if x < math.MinInt16 || x > math.MaxInt16 || z < math.MinInt16 || z > math.MaxInt16 {
			return fmt.Errorf("ground target (%d,%d) is outside signed 16-bit", x, z)
		}
		low, high = uint16(int16(x)), uint16(int16(z))
	default:
		return fmt.Errorf("unknown target kind %d", s.Target.Kind)
	}
	binary.LittleEndian.PutUint16(data, low)
	binary.LittleEndian.PutUint16(data[2:], high)
	binary.LittleEndian.PutUint32(data[4:], s.SavedPayloadWord0)
	data[8] = s.SavedActiveByte
	binary.LittleEndian.PutUint32(data[0x0c:], s.SavedPayloadWord1)
	if s.Reload < math.MinInt16 || s.Reload > math.MaxInt16 {
		return fmt.Errorf("reload %d is outside signed 16-bit", s.Reload)
	}
	binary.LittleEndian.PutUint16(data[0x10:], uint16(int16(s.Reload)))
	binary.LittleEndian.PutUint16(data[0x12:], s.DesiredYaw)
	binary.LittleEndian.PutUint16(data[0x14:], s.DesiredPitch)
	if s.Ammo < 0 || s.Ammo > math.MaxUint8 {
		return fmt.Errorf("stockpile %d is outside byte range", s.Ammo)
	}
	data[0x16] = byte(s.Ammo)
	data[0x17] = s.Flags & 0x1f
	return nil
}

func resolveRetailStableID(resolve RetailStableID, h pool.Handle, kind string) (uint16, error) {
	if h == 0 || resolve == nil {
		return 0, fmt.Errorf("units: retail save: unresolved %s handle %d", kind, h)
	}
	id, ok := resolve(h)
	if !ok || id == 0 {
		return 0, fmt.Errorf("units: retail save: unresolved %s handle %d", kind, h)
	}
	return id, nil
}

func optionalRetailStableID(resolve RetailStableID, h pool.Handle, kind string) (uint16, error) {
	if h == 0 {
		return 0, nil
	}
	return resolveRetailStableID(resolve, h, kind)
}

func fitsInt32(v int64) bool     { return v >= math.MinInt32 && v <= math.MaxInt32 }
func putI32(dst []byte, v int32) { binary.LittleEndian.PutUint32(dst, uint32(v)) }
func putI16(dst []byte, v int16) { binary.LittleEndian.PutUint16(dst, uint16(v)) }
