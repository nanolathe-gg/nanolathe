package main

// Dispatch of commands from the HUD and the keyboard: build pages, order buttons,
// stance, on/off, stockpile and self-destruct [07 §6] [07 §9].

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// resetOrderLatch keeps the semantic idle state and the retained order-radio
// group together [07 R-HUD-04 §3]. Cached pages retain presentation state, so
// clear their groups too before a later selection exposes them again.
func (b *battleSession) resetOrderLatch() {
	b.battleState().SetLatch(input.LatchNormal)
	b.battleState().Input.ShiftLatchSticky = false
	if b.hud == nil {
		return
	}
	// These panels are presentation-only; their iteration order has no effect
	// on simulation or on another panel's state.
	for window, panel := range b.hud.palettePanels {
		for i, gad := range window.Gadgets {
			if gad.Kind == gui.KindButton && commandButtonName(gad.Name) == "STOP" {
				panel.ClearButtonGroup(i)
				break
			}
		}
	}
}

// switchBuildPage handles digit 1..9 build page switching [07 §9] C10.
// Page number lives in flag bits 23-25 with bit 22 paged indicator [07 §9].
func (b *battleSession) switchBuildPage(digit int) {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount == 0 || digit < 1 || digit > 9 {
		return
	}
	_, count := b.buildPageNavigationState(frame)
	target, valid := hud.DigitPage(digit, count)
	if !valid {
		return
	}
	_ = b.dispatchBuildPageCued(target)
}

// nextBuildPage advances one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) nextBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		// Retail requests the cue even without a live page owner. Keep
		// adaptive sidebar navigation's change-only policy separate
		// [07 R-HUD-03 §6][DESIGN_INTERFACE_HUD_INPUT §3.3].
		if ok && b.hud != nil {
			if _, active := b.hud.expandedSidebarPaging(b, frame); active {
				return
			}
		}
		b.playUICue(nil, cueNextBuildMenu)
		return
	}
	page, count := b.buildPageNavigationState(frame)
	target := hud.NextPageKey(page, count)
	if state, authored := b.authoredPageState(frame); authored {
		target = state.NextKey(count)
	}
	_ = b.dispatchBuildPageCued(target)
}

// prevBuildPage goes back one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) prevBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		// Retail requests the cue even without a live page owner. Keep
		// adaptive sidebar navigation's change-only policy separate
		// [07 R-HUD-03 §6][DESIGN_INTERFACE_HUD_INPUT §3.3].
		if ok && b.hud != nil {
			if _, active := b.hud.expandedSidebarPaging(b, frame); active {
				return
			}
		}
		b.playUICue(nil, cueNextBuildMenu)
		return
	}
	page, count := b.buildPageNavigationState(frame)
	target := hud.PrevPageKey(page, count)
	if state, authored := b.authoredPageState(frame); authored {
		target = state.PrevKey(count)
	}
	_ = b.dispatchBuildPageCued(target)
}

// handleHudOrderButton binds named order buttons to the session command path
// [R-P0-03][07 §9]. It is used by the retail HUD click pass.
func (b *battleSession) handleHudOrderButton(name string) {
	latch := hud.ParseButtonLatch(name, b.orderButtonGate(name))
	// STOP is a distinct immediate command. It must never dispatch contextual
	// code 1 at the map origin before the Stop descriptor [04 §3.4][07 §9].
	// STOP is the one arm that ignores the gate and always writes the idle
	// latch, but it is still a matched arm: it plays `immediateorders` just
	// like the other nine arms of that family [07 §9 "Corrected and
	// completed"].
	if latch == input.LatchNormal && containsStop(name) {
		_ = b.dispatchStopCommand()
		b.resetOrderLatch()
		b.playUICue(nil, cueImmediateOrders)
		return
	}
	if latch.IsValid() {
		b.battleState().SetLatch(latch)
		// "Each armed write … plays the `immediateorders` cue … or the
		// `specialorders` cue" [07 §9]; the cue follows the armed write, not the
		// hit test, so a button whose parse yields no valid latch is silent.
		b.playUICue(nil, orderButtonCue(latch))
	}
	// A name matching none of the chain's tests is not handled: no latch
	// write, no cue [07 §9].
}

// orderButtonGate is the fired order button's down-state word, read after the
// widget's own press mutation [07 §9][07 R-WGT-01 §3]. Both stock orders
// windows author their order buttons as toggles (attribute 0x40), so a second press of an armed button
// flips it back up and the dispatcher writes the idle latch instead of the
// button's own value. Passing a constant 1 here kept RECLAIM armed after the
// player toggled E off: the button art went up while the latch, the reclaim
// cursor and the Community wreck-snap preview all stayed armed.
//
// A caller with no retained command-window widget — a direct test call, or a
// window that does not author the named button — has no down-state to read,
// and keeps the armed answer the button name asks for.
func (b *battleSession) orderButtonGate(name string) uint32 {
	if b == nil || b.hud == nil {
		return 1
	}
	ctx, ok := b.hud.paletteContext(b)
	if !ok {
		return 1
	}
	panel := b.hud.palettePanels[ctx.window]
	index := ctx.window.GadgetIndex(name)
	if panel == nil || index <= 0 {
		return 1
	}
	return uint32(uint16(panel.StatusAt(index)))
}

func containsStop(s string) bool {
	upper := strings.ToUpper(s)
	return strings.Contains(upper, "STOP")
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
	if closeUnitInfo() {
		b.developer.quickkeysDisabled = false
	}
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
//
// It is also the front door the world-click handler's shape gate sits in
// [07 R-CAM-01 §14] step 3: with an order armed, the click issues an order
// only when the reduced cursor shape is an ACTION shape — an index below
// `cursorred` — and does nothing at all on `cursorred`, `cursorgrn` or
// `cursornormal`. That gate, not the resolver, is what makes the advertised
// action and the performed action the same action: the resolver's code-12 unit
// arm, for one, accepts any live target and would strip a unit the armed
// RECLAIM row never offered to strip.
//
// The reported bool is whether the click took that issue branch, which is what
// the armed-click callers need to decide the latch: branch 3 not taken means
// *nothing happens*, and a latch that retired would be something happening.
// The branch counts as taken once the command is handed to the session, so a
// command the session boundary then refuses retires the latch exactly as it
// always has.
//
// Once the shape admits branch 3, its tail tests only Shift after dispatch;
// it does not read a descriptor/success result [07 R-CAM-01 §14]. Thus even
// when every actor rejects the order, Shift clear retires the latch and Shift
// held keeps it. A rejected shape never reaches that tail.
func (b *battleSession) orderSelected(code int, sx, sy int32, queued bool) bool {
	// An armed Community wreck-snap preview consumes the reclaim click and
	// re-issues it at the snapped feature (community patch engine CP-CON-6).
	// It is taken here, in the one armed-click producer, so the plain click
	// and the Modern command drag's short release both honour it. Before, only
	// the plain click path did: with the Enhanced client the press became a
	// command drag whose release reclaimed at the raw pointer, which the shape
	// gate below then refused wherever the snap marker sat on a nearby feature
	// rather than under the pointer — the marker promised a reclaim and the
	// click issued nothing. The preview is armed only for the RECLAIM latch
	// under resolved community features with WreckSnap set, which Strict 3.1
	// never resolves, so a Strict code 12 never reaches it. A minimap order keeps its lens point: the minimap
	// click never took the snap, and the extension's snap is a game-view
	// gesture.
	if code == hud.LatchToCode(input.LatchReclaim) && b.classifyPointer(sx, sy) != battlePointerMinimap && b.issueCommunityReclaimSnap(queued) {
		return true
	}
	// The HUD latch table is the single semantic mapping between an armed
	// order and the session order code. Validate the caller's code by running
	// it through that table; do not maintain a second switch here [07 §9].
	latch := input.Latch(code)
	if hud.LatchToCode(latch) != code {
		return false
	}
	targetHandle, target, pos := b.pickTargetFor(latch, sx, sy)
	if pos == nil {
		return false
	}
	if target == nil {
		if h, radarTarget := b.pickRadarAttackTarget(sx, sy, latch); radarTarget != nil {
			targetHandle, target = h, radarTarget
			// Contextual contact clicks are attacks only; concealed unit traits
			// cannot choose repair/reclaim/capture or another contextual action.
			code = hud.LatchToCode(input.LatchAttack)
			latch = input.LatchAttack
		}
	}
	// The shape gate applies to the ARMED latches only. The contextual code
	// the idle latch issues reaches this producer from the click classifier's
	// own branches, which have already consumed the select and deselect
	// answers [07 R-CAM-01 §14] steps 2 and 4.
	//
	// The handler is region-agnostic, so a minimap click with a latch armed is
	// judged by the same gate, on the unit word `pickTarget` resolved for that
	// region — the blip winner over the minimap, the hover winner in the view
	// [07 R-CAM-01 §14][07 R-HUD-03 §1]. The Modern area drag dispatches its
	// own target list and never reaches this producer; its short release does,
	// and is judged like any other armed click (interface design §3.11). Armed
	// placement is branch 1, which the placement paths own.
	if latch != input.LatchNormal && b.cursorShapeForClick(latch, sx, sy, target) >= render.CursorRed {
		return false
	}
	command := session.HumanOrderCommand{
		Code: code, Target: targetHandle,
		Position: *pos, Queued: queued,
	}
	if latch == input.LatchNormal && target == nil && pos.HasFeature {
		b.pickContextualWreckWorkers(sx, sy, &command)
	}
	_ = b.DispatchOrderCommand(command)
	return true
}

// selfDestructSelection is Ctrl+D, a toggle [07 R-CAM-01 §2]: a second press
// cancels the selection's countdowns, and only a selection with none issues
// self-destruct. The session applies the toggle at the input boundary, against
// the queues as they stand then. Shift makes an issue queued.
func (b *battleSession) selfDestructSelection(queued bool) {
	handles := b.selectedHandlesInSlotOrder()
	if len(handles) == 0 {
		return
	}
	_ = b.enqueueHumanCommand(session.HumanCommand{
		Kind:         session.HumanSelfDestruct,
		SelfDestruct: session.HumanSelfDestructCommand{Handles: handles, Queued: queued},
	})
}

// effectiveBuildPage is the command page's page. The page is local interface
// state applied at once (battle_local_interface.go), and the frame's command
// page is composed from it, so no pending page command waits to be projected.
func (b *battleSession) effectiveBuildPage(f *frame.Frame) int {
	return int(f.CommandPage.Page)
}

// Modern row pages have a presentation-local index. Keep every navigation
// producer on the same range while preserving authored paging for Classic
// and unsupported GUI layouts (interface design §3.3).
func (b *battleSession) buildPageNavigationState(f *frame.Frame) (page, count int) {
	if b.hud != nil {
		if state, ok := b.hud.expandedSidebarPaging(b, f); ok {
			return state.Page, state.Count
		}
	}
	return b.effectiveBuildPage(f), int(f.CommandPage.PageCount)
}

// authoredPageState is the selected builder's page-shown bit and page field
// when authored paging is in force. Retail's page producers are operations on
// that pair, not on the displayed page, which is what keeps nine or more pages
// cycling [07 R-HUD-03 §6]. Adaptive sidebar paging keeps its local index walk
// (interface design §3.3), so it reports false there.
func (b *battleSession) authoredPageState(f *frame.Frame) (hud.PageState, bool) {
	if f == nil || f.CommandPage.Builder == 0 {
		return hud.PageState{}, false
	}
	if b.hud != nil {
		if _, active := b.hud.expandedSidebarPaging(b, f); active {
			return hud.PageState{}, false
		}
	}
	builder, found := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !found {
		return hud.PageState{}, false
	}
	return hud.PageStateOf(builder.Flags), true
}
