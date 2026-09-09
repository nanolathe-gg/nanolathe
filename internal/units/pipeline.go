// Post-normal-COB status maintenance [04 R-MOV-03 §1 steps 5–8].
// The session owns the visit: weapons, exactly one normal COB drain, this
// refresh, water/orders/movement, then slot-end death finalization.
//
// Construction Remaining is owned exclusively by construction.Service [05
// "Construction target state"]; no stage here reads or writes it.

package units

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
)

// unitPostCOBStatus is the per-unit post-normal-COB status work [04 §2.4][04 §5.1].
//
// [04 R-MOV-03 §1] lists ten numbered acts per unit visit. Four of them are
// this stage, and they run here in the listed order:
//
//	step 5 — the minimap blink byte, decremented as a SIGNED byte while nonzero
//	         [06 R-WPN-04 §2];
//	step 6 — the post-capture grace counter, decremented by one while nonzero;
//	step 7 — selection maintenance: a unit carrying the selected bit loses it
//	         when it stops being ready;
//	step 8 — the tick-mod-30 health-percentage roll [04 §5.1] C22.
//
// Step 6 has no field and needs none. The counter's only writer is the
// ownership transfer's branch whose NEW owner is a remote peer of controller 3
// ([05 R-WORK-01 §15], the ownership transfer's second branch), which is
// multiplayer transport and out of scope, so in a single-player session the
// counter is zero for every unit at every visit and the decrement is inert.
// That is why step 7's third clause below is a constant true rather than a
// field read: writing a field no producer can arm would be inventing state.
//
// Steps 2 (the general unit update carrying the wind-generator notifier of
// [05 R-PROD-01 §3]) and 9 (water damage, self-repair, the order pumps, the
// mover) belong to other windows and other packages; they are not this stage.
func (w *World) unitPostCOBStatus(u *Unit, tick uint32) {
	if u == nil || !u.Alive {
		return
	}
	// Step 5: the damage-flash byte of [06 R-WPN-04 §2]. The damage dispatcher
	// writes 240; the sweep decrements it by ONE per visit while it is nonzero,
	// so it reaches zero after 240 visits [06 R-WPN-04 §4] — a plain byte
	// decrement, signedness immaterial. The "sixteen visits, rising toward
	// zero" reading this step used to implement was inferred from a mis-stated
	// observable and is retracted there. The guard is the "while it is nonzero"
	// clause and is also the floor.
	if u.BlinkSuppress != 0 {
		u.BlinkSuppress--
	}
	// Step 6: the post-capture grace counter. Inert in single-player — see the
	// doc comment. No field, no decrement, no placeholder value.

	// Step 7: selection maintenance [04 R-MOV-03 §1 step 7]. A unit carrying the
	// selected bit (bit 4, 0x10) loses it the moment it stops being READY. Ready
	// is the same four-part predicate the selection, trigger and camera paths
	// share [07 R-WGT-01 §9][08 R-TRIG-01 §3]: the selectable bit 5 is set, the
	// remaining-build fraction is exactly 0.0, the post-capture grace counter is
	// zero, and either the unit has no carrier or its carrier's status word
	// carries bit 30. This is the sweep's own clear, not presentation's: the bit
	// lives on the unit record (internal/session's selection commands write it,
	// publication reads it), so a unit that starts a repair, is picked up by a
	// transport that is not an airbase, or has its selectable bit cleared drops
	// out of the selection on its next visit without the HUD being asked.
	if u.Flags&SelectedStatus != 0 && !w.selectionReady(u) {
		u.Flags &^= SelectedStatus
	}
	// Step 8: the 30-tick health sample used by the death severity input
	// [04 §5.1] C22. Every 30 ticks it recomputes clamp(health*100/maxHealth,
	// 0, 100) and shifts the current sample into PriorSample before storing the
	// new CurrentSample. The two-byte state is required: local Killed consumes
	// the previous window, not the value sampled at the current boundary.
	if tick%30 == 0 && u.MaxHealth > 0 { // [04 §5.1] every 30 ticks clamp(health*100/maxHealth,0,100)
		// The rolled pair uses the CLAMPED form, not engine port 4: §4.4 item 8
		// gives "the same unsigned division and clamps as the `TakeDamage` percent
		// of section 5.1", while the port itself clamps nothing [04 R-COB-03 §2].
		pct := cob.ClampedHealthPercent(u.Health, u.MaxHealth) // [04 §5.1] clamped 0..100
		u.PriorSample = u.CurrentSample                        // [04 §5.1] previous window
		u.CurrentSample = uint8(pct)                           // [04 §5.1] current window
	}
	// No Remaining mutation. No order creation. No RNG draws.
}

// selectionReady is the *ready* predicate of [04 R-MOV-03 §1] step 7, which is
// the shared eligibility predicate of [07 R-WGT-01 §9] read on a unit that is
// already selected: selectable bit 5 set, remaining-build fraction exactly 0.0,
// post-capture grace counter zero (constant in single-player, see
// unitPostCOBStatus), and either no carrier or a carrier whose status word carries
// the cargo-selectable bit 30. Eligible() is the first two clauses; the carrier
// clause is spelled out here because it needs the world to resolve the carrier.
//
// A carrier handle that no longer resolves is not ready: retail reads the
// carrier's status word through the stored reference, and a unit whose carrier
// has gone leaves the selection rather than staying selected on a dead link.
func (w *World) selectionReady(u *Unit) bool {
	if !u.Eligible() {
		return false
	}
	if u.Attachment.Carrier == 0 {
		return true
	}
	carrier := w.Unit(u.Attachment.Carrier)
	return carrier != nil && carrier.Flags&CargoSelectableStatus != 0
}

// SelectionReady exposes the shared sweep and next-ready selector predicate
// [04 R-MOV-03 §1].
func (w *World) SelectionReady(u *Unit) bool { return w.selectionReady(u) }
