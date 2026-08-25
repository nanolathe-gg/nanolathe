// Package units — typed per-unit state [04 §2][04 §4][04 §5][06][GAP T15].
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
	VM *cob.VM
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
	TargetUnit                     // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	TargetGround                   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
)

// Target is the decoded slot target [06 §1.2] (I13).
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// for unit latch, otherwise ground X/Z words <<16 [06 §1.2] P0-10.
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type Slot struct {
	Weapon       *content.WeaponDef // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Reload       int32              // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Flags        uint8              // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	DesiredYaw   uint16             // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	DesiredPitch uint16             // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Ammo         int32              // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	MuzzlePiece  int32              // muzzle piece queried synchronously [06 §4.1] C3

	// Aim is the asynchronous Aim handshake [GAP T15] C16 [06 §3.3] [04 §5.3].
	// IssueBit mirrors Flags&0x01 latch; Ready granted only on nonzero return [GAP T15] C16.
	Aim cob.AimSlot

	// Target is the decoded current target [06 §1.2].
	Target Target
}

// IsPopulated reports whether the slot has a resolved weapon [06 §1.2] C1.
func (s *Slot) IsPopulated() bool { return s != nil && s.Weapon != nil }

// IsArmed reports the armed/hasTarget flag 0x02 [06 §1.2] P0-10.
func (s *Slot) IsArmed() bool { return s != nil && s.Flags&0x02 != 0 }

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
// TODO(question): exact layout of mover mode bits and velocity domain remains
// open; kept minimal for P0-I02/P0-I15 wiring.
type MoveState struct {
	Mode    uint8         // low two bits runtime mover mode: 0 none, 1 stopped/parked, 2 active locomotion [04 §9.1]
	Heading uint16        // 0..65535 per circle [04 §5.1] C25 (I2) [03 §2.4] C24 bank→Z heading→Y pitch→X
	Pitch   uint16        // pitch per [03 §2.4] C24 [03 §5.2] (flight lean pitch)
	Bank    uint16        // bank per [03 §2.4] C24 [03 §2.4] C21
	Speed   numeric.Fixed // current scalar speed, 16.16 [04 §8.1]
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

// CallbackQueue holds engine→COB callback state between windows [GAP T15].
// Deferred callbacks queued before the normal drain run same visit; immediate
// wake-flag starts do an all-slot delta-0 drain [GAP T15] C17-C18.
// Backed by cob.DeferredQueue for engine→COB handshaking [04 §5].
type CallbackQueue struct {
	Deferred cob.DeferredQueue
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
	u.ScriptState = &ScriptState{VM: vm}
	u.Script = vm // typed field [P1-I01]
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

// InstallWeapon binds a weapon definition to a slot [06 §1.2] C1.
// The definition must be compiled via content [02 "Weapon record"].
func (u *Unit) InstallWeapon(idx int, w *content.WeaponDef) {
	if u == nil || idx < 0 || idx >= NumSlots {
		return
	}
	u.Slots[idx].Weapon = w
	if w != nil {
		u.Slots[idx].Flags |= 0x02 // armed/hasTarget when populated [06 §1.2] P0-10
	}
}
