package units

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// RetailStableID resolves a handle to the logical unit identifier used by
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
// Live object references and held weapon target slots have separate resolvers.
// targetSlot admits every valid pool slot, including a currently free slot;
// stableID admits live subjects, carriers and engagement links
// [08 R-SAVE-WEAPON-01].
func RetailUnitImage(u *Unit, orderCount uint32, stableID, targetSlot RetailStableID, scratch RetailUnitWriterScratch) ([]byte, error) {
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
		if err := writeRetailWeaponSlot(data[0x41+i*0x18:], &u.Slots[i], retailWeaponDefinition(u, i), targetSlot); err != nil {
			return nil, fmt.Errorf("units: retail save: unit %d weapon slot %d: %w", id, i, err)
		}
	}
	// The carrier link and the engagement-target link are the two words the
	// record may legitimately lack: each is "the stable slot, or 0 when absent
	// or dead" [08 R-SAVE-02 §6]. A save taken on the tick a shooter kills its
	// target therefore writes 0, not a refusal.
	carrier := optionalRetailStableID(stableID, u.Attachment.Carrier)
	binary.LittleEndian.PutUint16(data[0x89:], carrier)
	binary.LittleEndian.PutUint16(data[0x8b:], optionalRetailStableID(stableID, u.EngagementTarget))
	// The attach slot byte follows the written link: 0xFF when the unit has
	// no live carrier [08 R-SAVE-02 §6].
	data[0x8d] = 0xff
	if carrier != 0 {
		if u.Attachment.AttachPiece < 0 || u.Attachment.AttachPiece > math.MaxUint8 {
			return nil, fmt.Errorf("units: retail save: unit %d attach piece %d is outside byte range", id, u.Attachment.AttachPiece)
		}
		data[0x8d] = byte(u.Attachment.AttachPiece)
	}
	// 0x8E is the attacker-side snapshot: the owner byte of the last unit that
	// damaged this one, 10 meaning no attacker [08 R-SAVE-02 §6]
	// [06 R-WPN-04 §2]. This used to write an obsolete opaque byte that no
	// producer ever set, so a save discarded the snapshot the `Under Attack`
	// notice compares against the victim's owner.
	data[0x8e] = u.LastDamageSide
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
	// 0xAC/0xAD are the current/previous 30-tick-window health samples and
	// 0xB1 the damage-flash byte [08 R-SAVE-02 §14].
	data[0xac], data[0xad] = u.CurrentSample, u.PriorSample
	binary.LittleEndian.PutUint16(data[0xae:], uint16(u.Pending))
	data[0xb0], data[0xb1] = u.LOSByte, uint8(u.BlinkSuppress)
	var state uint16
	if u.Activated {
		state |= 1
	}
	if u.Armored {
		state |= 2
	}
	// Operational byte bit 2 is the INSTANCE cloaked bit; the cloak REQUEST
	// rides in the status word below [05 R-ECO-01 §8][05 R-ECO-01 §9].
	if u.Hidden {
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
	// The cloak-REQUESTED bit is status-word bit 11, and the runtime authority
	// for it is the bool, so project it back over the flag word's copy before
	// packing [05 R-ECO-01 §9]. A unit restored and re-saved untouched packs
	// exactly what it loaded. Pending death is also bool-owned: project Dying
	// independently of health or the last damage kind [08 R-SAVE-02 §6].
	// The unit mode mirror and cached movement-rate tier have named runtime
	// owners; pack their current values rather than an earlier loaded copy
	// left in Flags [08 R-SAVE-02 §6][04 R-MOV-01 §6].
	statusFlags := u.Flags &^ (CloakRequestedStatus | DeathPendingStatus | 0xf)
	statusFlags |= uint32(u.Move.ModeMirror&3) | uint32(u.MoveTier&3)<<2
	if u.Dying {
		statusFlags |= DeathPendingStatus
	}
	if u.IsCloaked {
		statusFlags |= CloakRequestedStatus
	}
	packed := stance | (statusFlags&0x0fff)<<4 | (statusFlags&0x2000)<<3 | (statusFlags&0x03ffc000)<<6 | scratch.PackedStatusBits17To19
	binary.LittleEndian.PutUint32(data[0xb4:], packed)
	return data, nil
}

func writeRetailWeaponSlot(data []byte, s *Slot, definition *content.WeaponDef, stableID RetailStableID) error {
	// The LIVE target is the authority for the pair. The restore scratch
	// (SavedTargetLow/High) is written only by the reader and is never read
	// here: it carries the on-disk words from the scalar pass to
	// RetailUnitWeaponTargets, and nothing keeps it in step with the ordinary
	// order-side clear, which sets Kind to TargetNone and leaves the scratch
	// naming whatever unit the slot used to hold.
	var low, high uint16
	switch s.Target.Kind {
	case TargetNone:
		// The empty target encoding is index zero carrying the unit sentinel:
		// the target-point resolver rewrites a freed target's slot words to
		// "the empty encoding (index zero with the unit sentinel)" and reads a
		// unit-mode pair with slot index zero as no target [06 R-WPN-04 §1].
		// A zero pair is NOT empty — it is the real ground point (0,0)
		// [08 R-SAVE-WEAPON-01].
		low, high = 0, 0x8000
	case TargetUnit:
		var err error
		low, err = resolveRetailStableID(stableID, s.Target.Unit, "weapon target")
		if err != nil {
			return err
		}
		high = 0x8000
	case TargetGround:
		if int64(s.Target.X)&0xffff != 0 || int64(s.Target.Z)&0xffff != 0 {
			return fmt.Errorf("units: ground target is not integral in save coordinates")
		}
		x, z := int64(s.Target.X)>>16, int64(s.Target.Z)>>16
		if x < math.MinInt16 || x > math.MaxInt16 || z < math.MinInt16 || z > math.MaxInt16 {
			return fmt.Errorf("units: ground target (%d,%d) is outside signed 16-bit", x, z)
		}
		low, high = uint16(int16(x)), uint16(int16(z))
	default:
		return fmt.Errorf("units: unknown target kind %d", s.Target.Kind)
	}
	binary.LittleEndian.PutUint16(data, low)
	binary.LittleEndian.PutUint16(data[2:], high)
	binary.LittleEndian.PutUint32(data[4:], s.Aim.ReadyWord())
	data[8] = definition.ActiveByte()
	binary.LittleEndian.PutUint32(data[0x0c:], uint32(s.DistanceWord))
	if s.Reload < math.MinInt16 || s.Reload > math.MaxInt16 {
		return fmt.Errorf("units: reload %d is outside signed 16-bit", s.Reload)
	}
	binary.LittleEndian.PutUint16(data[0x10:], uint16(int16(s.Reload)))
	binary.LittleEndian.PutUint16(data[0x12:], s.DesiredYaw)
	binary.LittleEndian.PutUint16(data[0x14:], s.DesiredPitch)
	if s.Ammo < 0 || s.Ammo > math.MaxUint8 {
		return fmt.Errorf("units: stockpile %d is outside byte range", s.Ammo)
	}
	data[0x16] = byte(s.Ammo)
	// The persisted slot-flag byte is the slot control byte's bits 0-4: aim
	// latch, enabled, the slot's own index, and autonomy [08 R-SAVE-WEAPON-01]
	// [06 R-WPN-05 §3]. Bits 5-7 are inert and the writer discards them.
	//
	// This used to fold in a separate OrderControl byte for bit 4. With the two
	// bytes collapsed [06 R-WPN-05 §3] the whole span comes off one field.
	data[0x17] = s.Flags & SlotFlagPersisted
	return nil
}

// resolveRetailStableID requires a nonzero identity admitted by the supplied
// resolver. Weapon targets use pool bounds; object links use live membership
// [08 R-SAVE-WEAPON-01] [08 R-SAVE-02 §6].
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

// optionalRetailStableID is the lenient form for the carrier and
// engagement-target links: the stable slot when the handle names a live unit,
// otherwise 0 [08 R-SAVE-02 §6]. The resolver knows only live units, so an
// unresolved handle is exactly the "absent or dead" case — the linked unit
// died this tick and its slot was freed, or has already been reused.
func optionalRetailStableID(resolve RetailStableID, h pool.Handle) uint16 {
	if h == 0 || resolve == nil {
		return 0
	}
	id, ok := resolve(h)
	if !ok {
		return 0
	}
	return id
}

func fitsInt32(v int64) bool     { return v >= math.MinInt32 && v <= math.MaxInt32 }
func putI32(dst []byte, v int32) { binary.LittleEndian.PutUint32(dst, uint32(v)) }
func putI16(dst []byte, v int16) { binary.LittleEndian.PutUint16(dst, uint16(v)) }

// retailWeaponDefinition retains the inactive record-0 identity even when the
// runtime slot uses nil for an inactive link [08 R-SAVE-WEAPON-01].
func retailWeaponDefinition(u *Unit, index int) *content.WeaponDef {
	if u.Slots[index].Weapon != nil {
		return u.Slots[index].Weapon
	}
	if u.Def == nil {
		return nil
	}
	return [NumSlots]*content.WeaponDef{u.Def.Weapon1Def, u.Def.Weapon2Def, u.Def.Weapon3Def}[index]
}
