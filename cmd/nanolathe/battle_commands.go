package main

// Dispatch of commands from the HUD and the keyboard: build pages, order buttons,
// stance, on/off, stockpile and self-destruct [07 §6] [07 §9].

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/session"
)

// switchBuildPage handles digit 1..9 build page switching [07 §9] C10.
// Page number lives in flag bits 23-25 with bit 22 paged indicator [07 §9].
func (b *battleSession) switchBuildPage(digit int) {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount == 0 || digit < 1 || digit > 9 {
		return
	}
	target := hud.ClampPage(hud.DigitToPage(digit), int(frame.CommandPage.PageCount))
	_ = b.dispatchBuildPageCued(target)
}

// nextBuildPage advances one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) nextBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		return
	}
	target := hud.ClampPage(int(frame.CommandPage.Page)+1, int(frame.CommandPage.PageCount))
	_ = b.dispatchBuildPageCued(target)
}

// prevBuildPage goes back one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) prevBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		return
	}
	target := hud.ClampPage(int(frame.CommandPage.Page)-1, int(frame.CommandPage.PageCount))
	_ = b.dispatchBuildPageCued(target)
}

// handleHudOrderButton binds named order buttons to the session command path
// [R-P0-03][07 §9]. It is used by retail HUD consumeClick.
func (b *battleSession) handleHudOrderButton(name string) {
	latch := hud.ParseButtonLatch(name, 1)
	// STOP is a distinct immediate command. It must never dispatch contextual
	// code 1 at the map origin before the Stop descriptor [04 §3.4][07 §9].
	// STOP is the one arm that ignores the gate and always writes the idle
	// latch, but it is still a matched arm: it plays `immediateorders` just
	// like the other nine arms of that family [07 §9 "Corrected and
	// completed"].
	if latch == input.LatchNormal && containsStop(name) {
		_ = b.dispatchStopCommand()
		b.battleState().Input.Latch = input.LatchNormal
		b.playUICue(nil, cueImmediateOrders)
		return
	}
	if latch.IsValid() {
		b.battleState().Input.Latch = latch
		// "Each armed write … plays the `immediateorders` cue … or the
		// `specialorders` cue" [07 §9]; the cue follows the armed write, not the
		// hit test, so a button whose parse yields no valid latch is silent.
		b.playUICue(nil, orderButtonCue(latch))
	}
	// A name matching none of the chain's tests is not handled: no latch
	// write, no cue [07 §9].
}

func containsStop(s string) bool {
	upper := strings.ToUpper(s)
	return strings.Contains(upper, "STOP")
}

// cancelSelectedProduction cancels the tail-most matching factory/mobile build for selected units [04 §3.3][P1-14].
// It walks each selected factory/builder's primary queue tail-most and decrements or frees via
// construction.CancelTailMost / CancelMobileTailMost. Tombstone bit ensures weapon-target-clear skip [04 §3.3].
func (b *battleSession) cancelSelectedProduction() {
	for _, u := range b.selectedCommandUnits() {
		if u != nil {
			_ = b.DispatchCancelProduction(u.Handle)
		}
	}
}

// toggleOnOffSelected issues Activate/Deactivate for OnOffable units [02 "Unit record"].
// OnOffable is data-driven; the command is Activate/Deactivate via orders.NewNodeForOrder [P0-I14].
func (b *battleSession) toggleOnOffSelected(queued bool) {
	for _, u := range b.selectedCommandUnits() {
		if u == nil || u.Def == nil || !u.Def.OnOffable {
			continue
		}
		// Activation state is typed unit state, not the unrelated order flag
		// word. The queued command is consumed by the ordinary order/COB edge
		// machinery [05 "Unit instance economy state"].
		_ = b.DispatchActivation(session.HumanActivationCommand{
			Unit: u.Handle, Activate: !u.Activated, Queued: queued,
		})
	}
}

// cycleStance is one press of the side panel's MOVEORD or FIREORD gadget, in
// the order [04 R-STANCE-01 §2] gives it: read the published three-bit panel
// field, compute the next value with that section's cycle, transmit the
// standing order through the selection broadcast, and play the cue. Step 3 —
// the local write-back into the panel word — is display only and has no place
// here: this build recomputes the aggregate from the selection at every
// publication boundary [I6], which is the same refresh retail's step 3 is
// overwritten by.
//
// A field reading 4 is not applicable to this selection; the gadget is greyed
// and matches no arm of the cycle, so the press does nothing at all.
func (b *battleSession) cycleStance(fire bool) {
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	current := f.CommandPage.MoveStance
	cue := "setmoveorders"
	if fire {
		current = f.CommandPage.FireStance
		cue = "setfireorders"
	}
	var next int32
	switch current {
	case 0:
		next = 1
	case 1:
		next = 2
	case 2, 3: // the mixed sentinel cycles to hold [04 R-STANCE-01 §2]
		next = 0
	default:
		return // 4: not applicable, the gadget is grey
	}
	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanStance, Stance: session.HumanStanceCommand{Fire: fire, Value: next},
	}); err != nil {
		return
	}
	b.playUICue(nil, cue)
}

// toggleCloakSelected is one press of the side panel's CLOAK gadget — the cloak
// arm of the same battle-panel handler as the two stance gadgets, in the order
// [04 R-STANCE-01 §2] gives it: read the published two-bit cloak pair, resolve
// the direction from it, transmit the descriptor through the selection
// broadcast, then play `specialorders`.
//
// The direction test is the pair against zero, not a comparison with 1: only a
// pair of 0 (every cloak-capable selected unit is visible) sends `Cloak_On`, and
// 1 (cloaked) and 2 (mixed) both send `Cloak_Off` [04 R-STANCE-01 §2]. The
// not-applicable value 3 greys the gadget [07 R-HUD-03 §6], so a press never
// reaches here carrying it; it would take the same off arm if it did.
//
// The local write-back into the panel word is display only and has no place
// here — this build recomputes the aggregate from the selection at every
// publication boundary [I6], the same refresh that overwrites retail's.
func (b *battleSession) toggleCloakSelected() {
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	on := f.CommandPage.CloakState == 0
	if err := b.enqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanCloak, Cloak: session.HumanCloakCommand{Cloak: on},
	}); err != nil {
		return
	}
	b.playUICue(nil, cueSpecialOrders)
}

// stockpileSelected queues one BuildWeapon round for stockpile weapons [06 §11.1].
// Stockpile launch requires BuildWeapon descriptor (rear segment 0x40000) with count.
func (b *battleSession) stockpileSelected(queued bool) {
	for _, u := range b.selectedCommandUnits() {
		if u != nil {
			_ = b.DispatchStockpile(u.Handle, queued)
		}
	}
}

// enqueueSelectionCommand is every selection-changing human command this shell
// issues, and the one place the command-panel page close hangs off.
//
// Retail calls that close from **every** selection change: it zeroes the
// current-page word and then, while a window is open, closes the top window
// through the top-object close and repeats until the command window is on top
// [07 R-HUD-04 §3][07 §3]. The unit-information screen is opened later than the
// command window and therefore sits above it, so a selection change closes it.
// That chain — and not a key matrix — is why Escape closes the screen: Escape
// with an idle latch deselects everything [07 R-CAM-01 §2], and the deselect
// runs the close.
//
// The close's deferral gate ([07 R-HUD-04 §3] step 1, which defers while a
// menu or one of a named set of interface bits is up and re-runs the close
// when they clear) reads battle-interface flag bits this shell does not
// publish, so the close here is unconditional. With no menu open the two
// agree.
func (b *battleSession) enqueueSelectionCommand(c session.HumanCommand) error {
	closeUnitInfo()
	return b.enqueueHumanCommand(c)
}

// playUICue plays a non-positional interface sound by its authored alias
// [07 §9][03 §8.3]. Retail's placement path plays `oktobuild` on a placed site
// and `notoktobuild` on a refused one; both are ordinary sound aliases, not a
// separate UI audio path.
func (b *battleSession) playUICue(cl *client.Client, alias string) {
	if b == nil || b.sess == nil || b.sess.Audio == nil {
		return
	}
	_ = b.sess.Audio.PlayUICue(alias)
}

// orderSelected resolves code at the clicked world position and submits one
// typed order command. Descriptor selection remains solely in orders.Resolve;
// this integration layer does not guess an attack descriptor for ground clicks
// [04 §3.4][07 §9].
func (b *battleSession) orderSelected(code int, sx, sy int32, queued bool) {
	targetHandle, _, pos := b.pickTarget(sx, sy)
	if pos == nil {
		return
	}
	// The HUD latch table is the single semantic mapping between an armed
	// order and the session order code. Validate the caller's code by running
	// it through that table; do not maintain a second switch here [07 §9].
	latch := input.Latch(code)
	if hud.LatchToCode(latch) != code {
		return
	}
	_ = b.DispatchOrderCommand(session.HumanOrderCommand{
		Code: code, Target: targetHandle,
		Position: *pos, Queued: queued,
	})
}

// selfDestructSelection is Ctrl+D. Retail resolves the SELFDESTRUCT order
// descriptor, fires the button's script path for every selected unit that
// carries that button, and otherwise issues the order through the order
// dispatcher for the selection [07 R-CAM-01 §2]. The palette's button is not
// wired here, so the order path runs for the whole selection; the descriptor
// is the front-segment `SelfDestructFG`, which [04 R-ORD-01 §2] names as the
// button's own.
func (b *battleSession) selfDestructSelection() {
	handles := b.selectedHandlesInSlotOrder()
	if len(handles) == 0 {
		return
	}
	_ = b.enqueueHumanCommand(session.HumanCommand{
		Kind:         session.HumanSelfDestruct,
		SelfDestruct: session.HumanSelfDestructCommand{Handles: handles},
	})
}
