// Typed per-unit state [04 §2][04 §4][04 §5][06][GAP T15].
//
// This file defines the typed fields and side tables that the real per-unit
// pipeline owns. Construction Remaining is owned exclusively by
// construction.Service [05 "Construction target state"]; no other package
// mutates Remaining. Weapon failure and Aim scheduling delegate to combat slots
// when wired, otherwise stub via this package's local Slot which carries the
// same Aim latch without importing combat (which would cycle via
// combat→economy→units) [06 §1.2][06 §3.3][GAP T15]. COB state uses cob.VM
// directly (no cycle) [04 §4.2][04 §4.6]. Movement status is shared with
// movement.System via the MoveState field that movement reads (movement imports
// units) [04 §8.1][04 §9.1]; attachments/cargo live here for future
// transport wiring [04 §4.4]. Orders remain opaque any holding *orders.Queue to
// avoid the orders→units import cycle [04 §3.2][04 §3.3].

package units

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// ScriptState is the per-unit COB VM and piece state placeholder [04 §4.1][04 §4.2].
// The VM holds eight thread slots and piece animation lanes [01 §6.1][04 §4.2] C13.
// The Pieces slice aliases VM.Pieces for snapshot presentation [03 §2.4] C21.
// Stored as *ScriptState on Unit; nil means no script bound (e.g., fixture or
// not yet wired in session composition) [04 §4.1]. Tick drains with delta 1 via
// VM.Drain(1) [04 §4.6][GAP T15] and never synthesizes completion.
// Typed Script field on Unit is *cob.VM (not any) per P1-I01 acceptance; ScriptState
// remains as typed wrapper for snapshot convenience.
type ScriptState struct {
	VM      *cob.VM
	Binding *cob.Binding // strict production binding; nil for synthetic fixtures [04 §4.1]
	// Bridge is the unit's one callback bridge, built with the VM and retained
	// for its life. It is retained rather than constructed per call because a
	// fresh bridge carries none of the sinks a caller may have installed — SFX,
	// presentation, lifecycle, the simulation RNG — so a script that emitted
	// during a callback run on a throwaway bridge would have its output
	// silently dropped [04 §4.1][R-COB-01 §1].
	Bridge *cob.CallbackBridge
}

// Pieces returns the current piece transforms for snapshot or nil [03 §2.4] C21.
func (s *ScriptState) Pieces() []model.PieceState {
	if s == nil || s.VM == nil {
		return nil
	}
	return s.VM.Pieces
}

// TargetKind names the encoded slot target form [06 §1.2].
type TargetKind uint8

const (
	TargetNone   TargetKind = iota // no target
	TargetUnit                     // unit latch sentinel [06 §1.2]
	TargetGround                   // ground point, not a unit latch [06 §1.2]
)

// Target is the decoded slot target [06 §1.2] (I13).
// A signed sentinel selects a unit latch; otherwise the target carries ground
// X/Z words converted to 16.16 fixed point [06 §1.2] [P0-10].
type Target struct {
	Kind TargetKind
	Unit pool.Handle   // valid when Kind==TargetUnit
	X    numeric.Fixed // valid when Kind==TargetGround
	Z    numeric.Fixed // valid when Kind==TargetGround
}

// Slot is one weapon slot per unit [06 §1.2] C1 (I13).
// This is the units-owned slot record; combat.Slot is the combat-owned
// definition that will be wired via side table or conversion later [06 §1.2].
// Kept local to avoid the units→combat→economy→units import cycle.
// Fields mirror the retail weapon-slot semantics [06 §1.2] [P0-10].
type Slot struct {
	Weapon *content.WeaponDef // resolved weapon definition [06 §1.2] [P0-10]
	Reload int32              // signed reload countdown [06 §1.2] [P0-10]
	// Flags is THE slot control byte; see the SlotFlag* constants below.
	//
	// Corrected 2026-09-02 [06 R-WPN-05 §3]: this build carried an
	// `OrderControl` byte beside it, holding the order verbs' bit 4 while
	// `Flags` held doc 06's "tracking flag". Reading every access to the byte
	// settles that these are ONE byte, so the pair modelled one retail byte as
	// two — equivalent only for as long as nothing wrote one without the other.
	Flags          uint8
	DesiredYaw     uint16 // commanded yaw [06 §1.2] [P0-10]
	DesiredPitch   uint16 // commanded pitch [06 §1.2] [P0-10]
	Ammo           int32  // remaining stockpile [06 §1.2] [P0-10]
	MuzzlePiece    int32  // Query* result retained as the weapon muzzle identity [06 §4.1] C3
	AimOriginPiece int32  // AimFrom*/second-Query result retained for the later aim-origin consumer [R-CB-01 §4]
	// DistanceWord is the slot's distance word — the divisor the ballistic
	// creator's `T0` reads [06 §6.4]. It is written ONCE, by the slot
	// initializer at unit construction, as
	// `trunc(1.25 × (queryPoint.z − aimFromPoint.z))` over the two composed
	// piece points in 16.16 world units, and it has no other writer
	// [06 R-WPN-05 §3] (RWU-19-39). It is NOT a per-shot flight distance:
	// every ballistic shot the unit ever fires divides by this one word.
	DistanceWord int32

	// Aim is the asynchronous Aim handshake [GAP T15] C16 [06 §3.3] [04 §5.3].
	// IssueBit mirrors Flags&0x01 latch; Ready granted only on nonzero return [GAP T15] C16.
	Aim cob.AimSlot

	// Target is the decoded current target [06 §1.2].
	Target Target
	// SavedTargetLow/High preserve the complete on-disk target pair, including
	// the 0x8000 unit-mode sentinel. The resolved Target is populated only
	// after all forced unit slots exist [08 R-SAVE-WEAPON-01].
	SavedTargetLow  uint16
	SavedTargetHigh uint16
	// SavedActiveByte and payload words are copied from the fixed record. The
	// active byte gates the resolved definition; it is never a weapon identity,
	// while the payload words remain intentionally unnamed [08 R-SAVE-WEAPON-01].
	SavedActiveByte   uint8
	SavedPayloadWord0 uint32
	SavedPayloadWord1 uint32
}

// The slot control byte, bit by bit [06 R-WPN-05 §3]. One byte, five live
// bits; the initializer preserves 5-7 and nothing else writes them.
//
//	0    Aim-request latch: set by the slot pipeline when it dispatches Aim*;
//	     cleared on target loss, on no-solution, on drift-gate failure and on
//	     a successful shot.
//	1    slot enabled — the slot's weapon definition is active. Its ONE writer
//	     is the slot initializer that unit construction runs, so a slot is
//	     never disabled during play; save load restores it wholesale. This
//	     closes [04 R-ORD-01 §7]'s "no runtime writer of bit 1 was found".
//	2-3  the slot's own INDEX (0, 1, 2). The record is self-describing, which
//	     is why the creators and the muzzle queries take a slot pointer alone.
//	4    autonomy — doc 06's "tracking flag" and [04 R-ORD-01 §7]'s "inhibit
//	     latch" are the same bit. The initializer SETS it, so every slot
//	     starts autonomous; thereafter only the two order verbs write it
//	     (*inhibit slot k* sets it, *release slot k* clears it), and the
//	     autonomous scan, the retaliation offer, the guards and the
//	     fire-stance handler read it.
//
// Bit 4 is NOT a firing gate. A comment here once said it "is read by the
// normal combat slot pipeline before acquisition or firing"; the removal
// cleanup walk sets the bit on every assigned slot on every order-record
// removal [04 R-ORDER-02 §2] — the purge a player's own non-queued order
// performs included — so reading it as a *suppression* gate silenced each
// unit's weapons permanently from its owner's first order.
const (
	SlotFlagAimLatch   uint8 = 1 << 0
	SlotFlagEnabled    uint8 = 1 << 1
	SlotFlagIndexMask  uint8 = 3 << 2
	SlotFlagIndexShift uint8 = 2
	SlotFlagAutonomous uint8 = 1 << 4
	// SlotFlagPersisted is the span the save writer serializes and the reader
	// restores; bits 5-7 are discarded on both sides [08 R-SAVE-WEAPON-01].
	SlotFlagPersisted uint8 = 0x1F
)

// IsPopulated reports whether the slot has a resolved weapon [06 §1.2] C1.
func (s *Slot) IsPopulated() bool { return s != nil && s.Weapon != nil }

// IsEnabled reports the control byte's bit 1 [06 R-WPN-05 §3]. It is set by the
// slot initializer for exactly the slots whose weapon link resolved, so for a
// unit built in this process it agrees with IsPopulated; after a save load the
// byte is authoritative on its own.
func (s *Slot) IsEnabled() bool { return s != nil && s.Flags&SlotFlagEnabled != 0 }

// SlotIndex reads the slot's own index out of bits 2-3 [06 R-WPN-05 §3].
func (s *Slot) SlotIndex() int {
	if s == nil {
		return 0
	}
	return int((s.Flags & SlotFlagIndexMask) >> SlotFlagIndexShift)
}

// IsAutonomous reports the control byte's bit 4 [06 R-WPN-05 §3].
func (s *Slot) IsAutonomous() bool { return s != nil && s.Flags&SlotFlagAutonomous != 0 }

// IsAimReady reports whether the slot is aim-ready per [GAP T15] C16 [06 §3.3].
// Aim-ready is granted only on a nonzero Aim* return via cob.AimSlot [GAP T15] C16.
func (s *Slot) IsAimReady() bool {
	if s == nil {
		return false
	}
	return s.Aim.CanFire() // [GAP T15] C16/C9: Ready only on nonzero, no timeout P0-10
}

// CanFire reports whether the slot would be allowed to fire this tick
// ignoring range/medium gates. A turret family would also need the Aim latch
// and nonzero result [06 §3.3]; spent reload and an outstanding Aim request
// block firing. This is the hook that proves Aim can block firing [06 §3.3][GAP T15].
func (s *Slot) CanFire() bool {
	if s == nil || !s.IsPopulated() {
		return false
	}
	if s.Reload > 0 {
		return false // [06 §1.2][06 §4.1] reload countdown before admission
	}
	// If an Aim request is outstanding (IssueBit set and not yet Ready), block.
	// Retail ORs 0x01 immediately after Aim dispatch and Ready grants only on
	// nonzero return [GAP T15] C16 [04 §5.3]. Issue alone authorizes nothing [04 §5.3].
	if s.Aim.IssueBit && !s.Aim.Ready {
		return false // [GAP T15] C16: Aim blocks firing
	}
	// For turret-like weapons the Ready bit is also required; the generic gate
	// here treats any missing Ready when IssueBit was ever set as blocking.
	// Stockpile and other families that don't use Aim have IssueBit==false and pass.
	return true
}

// NumSlots is the retail slot count [06 §1.2].
const NumSlots = 3 // [06 §1.2] primary, secondary, tertiary

// MoveState is the mover status shared with movement.System [04 §8.1][04 §9.1][GAP T15].
// The mover reads this field after the unit-phase preserves it for the movement
// window; integration via movement.System.Tick runs in phase 5 [01 §4.4] [GAP T15].
// Stored directly on Unit; movement imports units so it can read/write this.
//
// Both halves of this record's former open question are settled. The mode is
// the low two bits of the flags-word mode mirror, and its values are grounded
// (1) and airborne (2), not stopped and moving: the mover constructor writes 1
// for every unit, the only runtime writer is the two-valued setter the air
// executors call with 2 on takeoff and 1 on landing, and 0 and 3 reach a unit
// only through a save file [04 R-MOV-01 §8]. The velocity domain is the world's:
// coordinates and velocities are signed 16.16 fixed point, one world unit =
// 65536, with angles unsigned 16-bit at 65536 per circle [04 §8.1]
// [04 R-MOV-01 §4] (I2).
type MoveState struct {
	Mode    uint8         // low two bits of the flags-word mode mirror: 1 grounded/surface (every structure too), 2 airborne, 0 attached/parked, 3 save-installed [04 R-MOV-01 §8]; seeded to 1 at creation. The older "0 none, 1 stopped/parked, 2 active locomotion" reading is retracted [03 R-RAST-01 §7].
	Heading uint16        // 0..65535 per circle [04 §5.1] C25 (I2) [03 §2.4] C24 bank→Z heading→Y pitch→X
	Pitch   uint16        // authoritative ground-conform or flight-lean pitch [03 §2.4] C24 [04 R-MOV-01 §5a][04 R-AIR-01 §2]
	Bank    uint16        // authoritative ground-conform or flight-lean bank [03 §2.4] C24 [04 R-MOV-01 §5a][04 R-AIR-01 §2]
	Speed   numeric.Fixed // current scalar speed, 16.16 [04 §8.1]
	// VelX, VelY and VelZ are the mover's VELOCITY TRIPLE, one 16.16 word per
	// axis [04 R-MOV-01 §1]. It is a different quantity from the scalar speed
	// word above it: the scalar is a magnitude, the triple is the signed
	// per-axis displacement the commit step adds to the position each tick
	// (`proposed = position + velocity`, [04 R-COLL-01 §1]).
	//
	// Producers, all in internal/movement: the ground speed update writes
	// `(-sin(heading, speed), 0, -cos(heading, speed))` — a ground mover never
	// has vertical velocity and there is no gravity term on that path
	// [04 R-MOV-01 §4]; the flight integrator writes all three
	// [04 §10.1][04 R-AIR-01 §1]; the commit's blocked branch rewrites the
	// horizontal pair at the halved speed [04 R-COLL-01 §1]; and the carried
	// branch copies the carrier's triple, zeroing when the carrier has no
	// mover [04 R-COLL-01 §1][04 R-FAC-02 §2].
	//
	// Its one simulation reader outside internal/movement is the pre-fire lead
	// of [06 §3.3], which multiplies the TARGET's triple by the scaled flight
	// time. Presentation must not read it as an interpolation term [I6].
	VelX numeric.Fixed // [04 R-MOV-01 §1] (I2)
	VelY numeric.Fixed // [04 R-MOV-01 §1] (I2); always zero on the ground path [04 R-MOV-01 §4]
	VelZ numeric.Fixed // [04 R-MOV-01 §1] (I2)
	// Pending callbacks for movement window [GAP T15] C18.
	PendingHeading uint16 // desired heading queued before movement window
	PendingSpeed   int32  // signed dword before shift left 4 for SetSpeed [04 §5.3]
}

// AttachmentState holds carrier/cargo linkage [04 §4.4] attach-unit / detach-unit.
type AttachmentState struct {
	Carrier     pool.Handle   // 0 if not attached to a carrier
	AttachPiece int           // piece index on carrier, -1 if none
	Cargo       []pool.Handle // units carried as cargo; ordered by attach time
}

// SetScript binds a COB VM to the unit's script state [04 §4.1][P1-I01].
// Script is typed *cob.VM (not any) per P1-I01 acceptance; ScriptState wrapper is retained for snapshot convenience.
func (u *Unit) SetScript(vm *cob.VM) {
	if u == nil {
		return
	}
	if vm == nil {
		u.ScriptState = nil
		u.Script = nil
		return
	}
	u.ScriptState = &ScriptState{VM: vm, Bridge: cob.NewCallbackBridge(vm)}
	u.Script = vm // typed field [P1-I01]
}

// ScriptBridge returns the unit's retained callback bridge, or nil when no
// script is bound. Callers that need a synchronous script query — the landing
// pad query of [04 R-AIR-01 §6] among them — go through this rather than
// building a bridge of their own.
func (u *Unit) ScriptBridge() *cob.CallbackBridge {
	if u == nil || u.ScriptState == nil {
		return nil
	}
	return u.ScriptState.Bridge
}

// GetScript returns the unit's VM or nil. It prefers ScriptState then falls back to typed Script field.
func (u *Unit) GetScript() *cob.VM {
	if u == nil {
		return nil
	}
	if u.ScriptState != nil && u.ScriptState.VM != nil {
		return u.ScriptState.VM
	}
	return u.Script
}

// SlotAt returns the slot or nil if out of range.
func (u *Unit) SlotAt(idx int) *Slot {
	if u == nil || idx < 0 || idx >= NumSlots {
		return nil
	}
	return &u.Slots[idx]
}

// NanolatheBox is the six-word box the nano-segment submission routine builds
// when a unit is the boxed end of a work segment: this unit's world position
// plus the six signed extents of its definition's bounding record
// [05 R-WORK-01 §8][02 R-CAT-01 §7]. The reversed producers — unit reclaim and
// capture, where the box is the SOURCE and the builder's nano piece is the
// degenerate destination — and the forward producers — build, repair, help-
// build/assist, and a resurrection whose target has already resolved into a
// unit, where the box is the DESTINATION — all read it through here so every
// unit-target spray direction shares one derivation.
//
// The box is presentation geometry: nothing in the emission path is
// authoritative [05 R-WORK-01 §8], so no caller may read it back into
// simulation state (I6).
func (u *Unit) NanolatheBox() (min, max [3]numeric.Fixed) {
	if u == nil {
		return min, max
	}
	lo, hi := u.Def.BoundingExtents()
	pos := [3]numeric.Fixed{u.X, u.Y, u.Z}
	for a := 0; a < 3; a++ {
		min[a] = pos[a] + numeric.Fixed(lo[a])
		max[a] = pos[a] + numeric.Fixed(hi[a])
	}
	return min, max
}

// InstallWeapon binds a weapon definition to a slot [06 §1.2] C1.
// The definition must be compiled via content [02 "Weapon record"].
//
// It writes the same control byte the slot initializer does
// [06 R-WPN-05 §3]: the slot's own index into bits 2-3, and enabled plus
// autonomy when the link resolved.
func (u *Unit) InstallWeapon(idx int, w *content.WeaponDef) {
	if u == nil || idx < 0 || idx >= NumSlots {
		return
	}
	s := &u.Slots[idx]
	s.Weapon = w
	s.Flags = (s.Flags &^ SlotFlagIndexMask) | (uint8(idx)<<SlotFlagIndexShift)&SlotFlagIndexMask
	if w != nil {
		s.Flags |= SlotFlagEnabled | SlotFlagAutonomous
	}
}
