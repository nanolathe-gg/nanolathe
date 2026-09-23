package main

// The battle keyboard and mouse pass: the per-frame input walk, the digit
// routing gate and the Ctrl+letter category table [07 §9].

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

func (b *battleSession) syncSelectionDrag(cl *client.Client) {
	if cl == nil || b == nil || b.battleState() == nil {
		if cl != nil {
			cl.SetSelectionDrag(client.SelectionDrag{})
		}
		return
	}
	state := b.battleState()
	in := state.Input
	// The drawn box is the same band the release tests: both recorded world
	// endpoints projected with the camera of this frame, in the surface pixels
	// the overlay writer draws in [07 §9 "Drag-rectangle conversion is closed"]
	// (DESIGN_GPU_RENDERER §16.3). Publishing the press-time pointer pixels
	// instead left the box behind while the camera edge-scrolled or zoomed under
	// it, and left it disagreeing with the handles the release admitted.
	box := b.selectionBand(in).Surface
	// The colour is chosen by the armed latch, not by the fact that a drag is
	// running: an ordinary selection drag is white (logical entry 15 outer over
	// entry 0 inner), and only an armed MOBILEBUILD latch takes the validity
	// pair, 10 for a legal site and 4 for an illegal one on both of its frames
	// [07 §9 "Build placement is closed"]. Passing DragActive as the box-mode
	// selector made every drag take entry 4 — a dark red — and left the
	// entry-15 branch unreachable.
	cl.SetSelectionDrag(client.SelectionDrag{
		Active:           in.DragActive,
		StartX:           box.MinX,
		StartY:           box.MinY,
		EndX:             box.MaxX,
		EndY:             box.MaxY,
		MobileBuildLatch: in.Latch == input.LatchMobileBuild,
		// Retail holds this in one interface flags byte, not two: bit 3 of
		// that byte is the button gate every order-button arm clears, and
		// bit 6 is the **site-valid** bit written by the in-view placement
		// preview — which the frame handler runs only when the pointer is
		// over the view and the latch is MOBILEBUILD — and cleared by the
		// world rebuild [07 R-CAM-01 §14]. This build's placement verdict is
		// that bit, so the ghost's colour follows the same byte the placement
		// cursor and the build click read [07 §9].
		SpecialLatchFlag: in.BuildOK,
		VisiblePanel:     state.PanelOffset == ui.PanelVisible,
	})
}

// The battle hotkey census — every row of [07 R-CAM-01 §2]'s token table
// against what this dispatcher does, in table order. Retail folds Ctrl into
// the token itself (`Ctrl+A..Z` -> 0xAA..0xC3, `Ctrl+0..9` -> 0xC4..0xCD,
// `Ctrl+F1..F12` -> 0xCE..0xD9), so a Ctrl-composed token can never reach an
// unmodified key's case. The shortcut decoder keeps that token identity
// separate from the live Ctrl state used by pointer gestures.
//
//	token                  key            state
//	0x09                   Tab            done — opens/closes the options window (viewerStep)
//	0xE3                   F2             done — same window; Shift+F2 pins the restored Unit Builder Probe
//	0x0D                   Enter          done — opens single-player `TALK.GUI`; local text and the bounded `+` command set enter the shared message ring [07 §5 "Chat"]
//	0x1B                   Escape         done — options close, latch cancel, else deselect all
//	0x21 0x23 0x2A         ! # *          done — Shift+1/3/8, the same "label every unit" bit as ` and ~ [07 R-CAM-01 §14]
//	0x60 0x7E              ` ~            done — "label every unit" bit
//	0x2B 0x3D              + =            done — speed up, with the ring announcement
//	0x2D 0x5F              - _            done — speed down, with the ring announcement
//	0x2C                   ,              done — previous build page, `nextbuildmenu`
//	0x2E                   .              done — next build page, `nextbuildmenu`
//	0x31..0x39             1..9           done — SwitchAlt mux; group recall plays `SelectSquad`; reached by an unshifted digit or Alt+digit, so additive recall is Shift+Alt+digit [07 R-CAM-01 §14]
//	0x54 0x74              T t            done — follow camera, previous/next selected unit
//	0x5C                   \              done — replay the supported retained local command with developer access
//	0x68                   h              out of scope — `SHARE.GUI` is multiplayer
//	0x6E                   n              done — next unvisited own unit, camera glide, no selection change; `N` (0x4E) has no case [07 R-CAM-01 §14]
//	0xAA                   Ctrl+A         done — select every own selectable unit, additive
//	0xAC                   Ctrl+C         done — `CTRL_C` category select, then follow the commander
//	0xAD                   Ctrl+D         done — self-destruct the selection
//	0xAB 0xAE..0xBB        Ctrl+B, E..R   done — `CTRL_%c` category select
//	0xBD..0xC2             Ctrl+T..Y      done — `CTRL_%c` category select
//	0xBC                   Ctrl+S         done — select the own selectable units on screen, replacing
//	0xC3                   Ctrl+Z         done — select every own unit sharing a selected definition
//	0xC5..0xCD             Ctrl+1..9      done — group assign, `CreateSquad`; Ctrl+0 has no case
//	0xD2..0xD5             Ctrl+F5..F8    done — store bookmark 0..3, `SelectSquad`
//	0xE6..0xE9             F5..F8         done — recall bookmark 0..3, `SelectSquad`
//	0xD6                   Ctrl+F9        out of scope — screenshot; the battle shell has no in-battle capture writer
//	0xD7                   Ctrl+F10       out of scope — developer mode only
//	0xE2                   F1             done — opens `UNITINFOx.GUI` for the hovered unit or the hovered build button's product; Shift+F1 pins the restored State Probe [07 R-HUD-03 §8]
//	0xE4                   F3             done — message-source glide
//	0xE5                   F4             done — interface-flags bit 0x80
//	0xEC                   F11            done — authorized film toggle; second dispatch owns i/m/P/p
//	0xED                   F12            done — clear the message ring
//	0xF8                   Pause          done — pause toggle
//	0x20, 0xC4, 0xF0..0xF7 Space, arrows… no case — the arrows are the scroll pass's held-key queries, not ring tokens
//
// The order latches below (`m`, `a`, `p`, `r`, `e`, `c`, `g`, `d`, `x`, `o`)
// have no row in that table at all: retail reaches them through the command
// palette's authored gadget quick keys [07 §2 "GUI quick keys"], not through
// the battle dispatcher. The active palette services them before this
// residual battle dispatcher.
//
// handleInput processes selection, orders, and build placement.
// It converts input into complete canonical commands with target/position and
// queue modifiers (shift-queued) via one picking routine that respects fog,
// unit/feature overlap, and command validity [07 §9][03 §3.2] C8 [P0-I14].
// Order buttons enqueue session-owned commands directly [R-P0-03]; build products are
// data-driven from cat.BuildMenus; input-capture latch prevents HUD presses
// from leaking into world drag [F-P0-003][F-P1-008].
func (b *battleSession) handleInput(in *input.State, cl *client.Client) {
	// viewerStep owns the producer queue; this pass receives its one selected
	// residual token alongside independent live input [07 §2].
	shortcutsServiced := false
	defer func() {
		// Captured pointer work still precedes the residual token. Its early
		// return must not drop a queued shortcut [07 R-CAM-01 §1].
		if !shortcutsServiced && in.ShortcutTokenMode && in.ShortcutToken.Kind != input.TokenNone {
			b.handleBattleShortcuts(in, cl)
		}
	}()
	kbd := in.Kbd
	mouse, pointerModifiers := publishedPointer(in)
	mx, my := int32(mouse.X), int32(mouse.Y)
	if b.serviceCommunityIncome(mouse) {
		return
	}
	if b.serviceCommunityPlacementInput(in, cl, mx, my) {
		shortcutsServiced = true
		return
	}
	if b.serviceCommunityOrderDrag(in, cl, b.hostPreferences().QueuedOrderDrag != 0) {
		return
	}
	b.updateResourceQueueFeedback(cl)
	if b.serviceCommandDrag(in, cl, mouse, pointerModifiers) {
		// The modern gesture already applies Escape's cancellation; applying
		// it twice would deselect after clearing the latch.
		shortcutsServiced = battleShortcutKeyboard(in).KeyDown(input.KeyEscape)
		return
	}
	if b.serviceResourceClick(in, cl, mouse, pointerModifiers) {
		return
	}
	b.dragScrollStepped = false
	b.battleState().Input.ShiftHeld = kbd.HasShift()
	b.battleState().Input.PointerX, b.battleState().Input.PointerY = mx, my
	// Any latch held by Shift retires on the live Shift-up, regardless of
	// order family [R-P0-11].
	if !b.battleState().Input.ShiftHeld && b.battleState().Input.ShiftLatchSticky {
		b.resetOrderLatch()
	}

	if !b.palettePointerOwned && b.serviceDragScroll(mx, my, mouse.Held(input.MouseButtonRight), cl) {
		return
	}
	// An admitted minimap camera down edge only sets a presentation capture;
	// its first jump is serviced here on the following host frame. Capture is
	// intentionally serviced before fresh clicks and continues outside the
	// radar until its matching release [07 R-CAM-01 §5][07 R-CAM-01 §11].
	if !b.palettePointerOwned && b.serviceMinimapCameraLatch(mx, my, &mouse) {
		return
	}
	if !b.palettePointerOwned && !b.battleState().Input.DragActive && b.classifyPointer(mx, my) == battlePointerMinimap {
		state := b.battleState().Input
		// An armed order, including placement, always fires on left. Type 1
		// therefore cannot let its idle left-button minimap camera latch steal
		// an armed left click [07 R-CAM-01 §5].
		if state.Latch != input.LatchNormal || state.BuildDef != "" {
			if mouse.Pressed(input.MouseButtonLeft) {
				b.minimapClickOrder(cl, mx, my, pointerModifiers.Shift)
				return
			}
		} else {
			if b.beginMinimapCameraLatch(mx, my, &mouse) {
				return
			}
			if mouse.Pressed(b.minimapOrderButton()) {
				b.minimapClickOrder(cl, mx, my, pointerModifiers.Shift)
				return
			}
		}
	}
	if !b.palettePointerOwned && !b.battleState().Input.DragActive && b.isOverMinimap(mx, my) && mouse.Held(input.MouseButtonLeft) {
		// The whole canvas suppresses a viewport drag; only its fitted radar
		// rectangle admits lens input [07 R-CAM-01 §11].
		return
	}

	shortcutsServiced = true
	if b.handleBattleShortcuts(in, cl) {
		return
	}

	// The earlier active-GUI pass owns its pointer gesture before world input.
	// Reuse that verdict; direct controller samples service the same retained
	// panel here [07 §3][07 R-WGT-01 §3].
	if b.hud != nil && b.hud.servicePalettePointer(b, in) {
		return
	}
	if b.beginCommandDrag(cl, mouse, pointerModifiers) {
		return
	}
	// A right click over the viewport is Type 1's idle contextual-order path.
	// It stays a no-op with no selection; the cursor's relationship colour does
	// not grant an actor or bypass command validation [07 R-CAM-01 §5].
	if mouse.Pressed(input.MouseButtonRight) {
		// A factory product button is the one right-click exception: it
		// subtracts the modifier-selected batch from the matching tail node
		// [R-P0-11]; Alt extends this to twenty (DESIGN_INTERFACE_HUD_INPUT §5).
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) && b.hud.consumeRightClickWithModifiers(b, mx, my, pointerModifiers) {
			return
		}
		state := b.battleState().Input
		if b.interfaceTypeRightClick() && state.BuildDef == "" && state.Latch == input.LatchNormal && b.classifyPointer(mx, my) == battlePointerViewport {
			if b.hasSelection() {
				b.orderSelected(1, mx, my, pointerModifiers.Shift)
			}
			return
		}
		if b.battleState().PlacementArmed() {
			// Cancel armed placement before affecting selection [R-P0-03][F-P0-003][07 §9].
			b.disarmPlacement()
			b.battleState().Input.HUDCaptured = false
			return
		}
		if b.battleState().Input.Latch != input.LatchNormal {
			// Cancel armed order latch to idle [07 §9][07 §8][07 §9].
			b.resetOrderLatch()
			return
		}
		if !b.interfaceTypeRightClick() && pointerModifiers.Ctrl && b.classifyPointer(mx, my) == battlePointerViewport {
			b.beginDragScroll(mx, my, cl)
			return
		}
		if b.hasSelection() {
			// Deselect is a typed command; UI never mutates live flags [I6].
			_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
			return
		}
		return
	}

	// Input-capture latch [F-P0-003][F-P1-008]: press that begins on HUD chrome
	// never starts/completes world drag selection even if released over world.
	// Detect HUD origin on the pressed edge.
	if mouse.Pressed(input.MouseButtonLeft) {
		overHUD := false
		if !b.overWorld(mx, my) {
			overHUD = true
		}
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) {
			overHUD = true
		}
		if overHUD {
			b.battleState().Input.HUDCaptured = true
			b.battleState().Input.HUDPressX = mx
			b.battleState().Input.HUDPressY = my
			b.battleState().Input.DragActive = false
		}
	}
	if b.battleState().Input.HUDCaptured && mouse.Released(input.MouseButtonLeft) {
		// Retail buttons arm while held and activate once on release-inside.
		// Requiring the same authored gadget at both endpoints prevents a drag
		// across the rail from activating a different control [07 §3][07 §4].
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		if b.hud != nil && b.hud.sameButton(b, b.battleState().Input.HUDPressX, b.battleState().Input.HUDPressY, mx, my) && b.hud.consumeClickWithModifiers(b, mx, my, pointerModifiers) {
			return
		}
		return
	}
	if b.battleState().Input.HUDCaptured {
		return
	}
	// The platform has already classified this event with the native
	// timestamp/rectangle policy. The optional host rule consumes that exact
	// record over an own unit and emits a normal selection replacement.
	if b.handleCommunityDoubleClick(in, mx, my) {
		b.disarmPlacement()
		return
	}

	// A left press the placement path already consumed owns that button until
	// it is released. Retail routes one press/release pair through exactly one
	// world path [07 §9]; because placement commits on the press edge and then
	// disarms, the remaining held frames of an ordinary human click used to
	// fall through to drag selection, and its release issued a contextual Move
	// that purged the build order the same click had just queued.
	if b.battleState().Input.PlaceCaptured {
		if !mouse.Held(input.MouseButtonLeft) {
			b.battleState().Input.PlaceCaptured = false
		}
		if b.battleState().PlacementArmed() {
			b.updatePlacement(mx, my)
		}
		return
	}

	// Build placement mode captures left-clicks before selection handling [R-P0-03].
	// Right-click cancellation is handled at the top of handleInput with the
	// latch/selection precedence of [07 §9].
	//
	// A shift-click leaves the mode armed so the next click places another copy;
	// retail records that on a placement-valid-pending bit and drops back to the
	// idle latch as soon as shift is released, whether or not another click
	// arrives. Arming from a build button does not set the bit, so a first
	// placement without shift still gets its click.
	if b.battleState().Input.BuildSticky && !kbd.HasShift() {
		b.disarmPlacement()
	}
	if b.battleState().PlacementArmed() {
		b.updatePlacement(mx, my)
		if mouse.Pressed(input.MouseButtonLeft) {
			// The press belongs to placement whatever it decides below —
			// placed, refused, or rejected by the command boundary.
			b.battleState().Input.PlaceCaptured = true
			if !b.battleState().Input.BuildOK {
				// An illegal site queues nothing and stays armed; the player
				// hears the refusal and can move the ghost [07 §9].
				b.playUICue(cl, "notoktobuild")
				return
			}
			queued := pointerModifiers.Shift
			if !b.commitBuild(queued) {
				// A command rejection is a failed commit, not an armed
				// placement state. All cancellation exits share disarmPlacement.
				b.disarmPlacement()
				return
			}
			b.playUICue(cl, "oktobuild")
			if queued {
				b.battleState().Input.BuildSticky = true
			} else {
				b.disarmPlacement()
			}
		}
		return
	}

	if mouse.Pressed(input.MouseButtonLeft) && b.battleState().Input.Latch != input.LatchNormal {
		issued := false
		code := hud.LatchToCode(b.battleState().Input.Latch)
		if code != 0 {
			// A Community wreck-snap reclaim is resolved inside orderSelected.
			issued = b.orderSelected(code, mx, my, pointerModifiers.Shift)
		}
		// Return latch to Normal after dispatch unless shift-queuing keeps it
		// [07 §9][P0-I14]. A click the shape gate refused issued nothing, and
		// [07 R-CAM-01 §14] step 3 not being taken means nothing happens at
		// all — so the latch stays armed and the player can aim again.
		if issued {
			if pointerModifiers.Shift {
				b.battleState().Input.ShiftLatchSticky = true
			} else {
				b.resetOrderLatch()
			}
		}
		b.battleState().Input.PlaceCaptured = true
		return
	}
	leftHeld := mouse.Held(input.MouseButtonLeft)
	additive := pointerModifiers.Shift
	if mouse.Pressed(input.MouseButtonLeft) && !b.battleState().Input.DragActive {
		state := &b.battleState().Input
		state.DragPressClock = b.inputScaledClock()
		// Both endpoints are whole three-component world points; the height is
		// kept because the projection shears the vertical coordinate by each
		// endpoint's OWN height [07 §9 "Drag-rectangle conversion is closed"].
		state.DragStartWorldX, state.DragStartWorldY, state.DragStartWorldZ = dragEndpointWorld(b.cursorWorld(mx, my))
		state.DragEndWorldX, state.DragEndWorldY, state.DragEndWorldZ = state.DragStartWorldX, state.DragStartWorldY, state.DragStartWorldZ
		state.DragActive = true
	} else if leftHeld && b.battleState().Input.DragActive {
		// The moving endpoint follows the pointer: its world point is re-picked
		// every held pass, and the band is projected from it [07 §9].
		state := &b.battleState().Input
		state.DragEndWorldX, state.DragEndWorldY, state.DragEndWorldZ = dragEndpointWorld(b.cursorWorld(mx, my))
	} else if !leftHeld && b.battleState().Input.DragActive {
		// The release point was resolved while the drag still selected the
		// viewport branch. Preserve that region through the click adapter
		// before retiring the presentation capture [07 R-CAM-01 §11][07 R-CAM-01 §14].
		defer func() { b.battleState().Input.DragActive = false }()
		// The band is the two recorded world endpoints projected with the camera
		// of this moment, which is the frame the unit points are built in too
		// [07 §9 "Drag-rectangle conversion is closed"]. The endpoints themselves
		// are not refreshed here: the click classification below compares the
		// stored world pair [07 R-CAM-01 §14].
		band := b.selectionBand(b.battleState().Input)
		if idleDragIsClick(b.battleState().Input, b.inputScaledClock()) {
			// Idle viewport release classification [07 R-CAM-01 §14].
			// Uses the immutable committed-frame picker so fog, radius, strict tie,
			// and viewer rules are shared by selection and targeting [07 §9][03 §3.2].

			if b.beginResourceClick(cl, mx, my, pointerModifiers) {
				return
			}
			// With Type 1 an idle left click remains the selection/drag button;
			// an empty click deselects. Type 0 retains its contextual left-click
			// branch [07 R-CAM-01 §5].
			f, ok := b.currentSnapshot()
			if !ok {
				return
			}
			viewer := visibility.PlayerID(f.ViewingPlayer)
			var bh pool.Handle
			var bu frame.UnitView
			var hit bool
			// The framebuffer composer already rebases the projected world point
			// from the beam origin before drawing it. Mouse coordinates are in that
			// same logical framebuffer, so do not subtract the HUD viewport origin
			// a second time [03 §2.5][07 §8].
			shellX, shellY := mx, my
			bh, bu, hit = b.pickPresentedUnit(f, shellX, shellY, uint8(viewer))
			// Branch 2 of the world-click handler is cursor kind `0x0F`,
			// "the resolver's select answer: latch idle and the hovered unit
			// is an own SELECTABLE unit (own slot, selectable bit,
			// remaining-build fraction `0.0`, post-capture grace zero,
			// carrier null or itself a visible carrier)"
			// [07 R-CAM-01 §14 step 2]. That list is the shared eligibility
			// predicate `E(u)` of [07 R-WGT-01 §9][07 R-WGT-01 §10], and
			// ownSelectableUnit is this build's one copy of it.
			//
			// The test used to be ownership alone, so a click on an own
			// nanoframe selected it — and, because the select branch runs
			// before the order branch, ate the click a builder meant as an
			// assist. Failing `E(u)` here is what lets the click reach
			// branch 3, where the contextual code resolves "nano-reach
			// passes and the target is unfinished → code 8" into HelpBuild
			// [07 R-CAM-01 §14 step 3][04 R-ORD-02 §1].
			hitOwn := hit && bh != 0 && b.ownSelectableUnit(f, bu)
			if hitOwn {
				if additive {
					_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionToggle, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{bh}}})
				} else {
					_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{bh}}})
				}
				playSelectionCue(b.sess, []pool.Handle{bh}) // [07 §9]
			} else if b.interfaceTypeRightClick() {
				// Type 1's idle empty-left branch deselects regardless of
				// Shift. Shift only modifies an eligible select or a drag
				// rectangle; it does not preserve this branch [07 R-CAM-01
				// §14 step 4].
				_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
			} else {
				if b.hasSelection() {
					// Type-0 left-click contextual order when a selection exists
					// and the click is not on an own unit [04 §3.4][07 §9].
					b.orderSelected(1, mx, my, additive)
				} else {
					// No selection and click not on own unit: clear if not additive, else preserve [07 §9] C6.
					if !additive {
						_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
					}
				}
			}
		} else {
			f, ok := b.currentSnapshot()
			if !ok {
				return
			}
			handles := b.eligibleHandlesInBand(f, band)
			kind := session.HumanSelectionReplace
			if additive {
				kind = session.HumanSelectionToggle
			}
			_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: handles}})
			playSelectionCue(b.sess, handles) // [07 §9]
		}
	}
	// Type 1's idle right-click order is handled before captures and cancels;
	// every armed row still uses left and right cancels it [07 R-CAM-01 §5].
}

// handleBattleShortcuts reports whether Escape owns the remaining input pass.
// The developer film table runs second over the same residual token [07 R-CAM-01 §9].
func (b *battleSession) handleBattleShortcuts(in *input.State, cl *client.Client) bool {
	defer b.handleDeveloperShortcuts(in, cl)
	// Ctrl composition belongs to the selected token. The decoder gives the
	// letter, digit and function-key arms that identity even if live Ctrl has
	// since changed [07 R-CAM-01 §2].
	kbd := battleShortcutKeyboard(in)
	ctrlHeld := kbd.KeyHeld(input.KeyCtrl)
	if !ctrlHeld {
		// `n` (0x6E) cycles the next unvisited own unit. `N` (0x4E) is a
		// separate character token and the dispatcher has no case for it, so
		// Shift+n does nothing: the stockpile round is enqueued only by the
		// palette's `MAKENUKE`/`MAKEANTI` gadgets [07 R-CAM-01 §14 item 3]
		// [07 §6]. The Shift+N stand-in that used to enqueue a round here is
		// gone; DispatchStockpileGadget keeps the one authored caller.
		if kbd.KeyDown(input.KeyN) && !kbd.HasShift() {
			b.cycleNextUnvisitedUnit()
		}
	}
	// The "label every unit" bit. Retail's dispatcher has a case for each of
	// five character tokens — `!` `#` `*` `` ` `` `~` — and every one of them
	// flips interface-flags bit 0 and writes all settings back
	// [07 R-CAM-01 §2][07 R-HUD-03 §7]. Both backquote tokens are the same
	// physical key, shifted and unshifted, so one key edge covers them.
	//
	// The other three are the shifted digits: the window procedure pushes the
	// translated *character*, so Shift+1/3/8 on the US layout the retail
	// install assumes are `!` `#` `*` and never the digit token
	// [07 R-CAM-01 §14 "the key-token producer"]. The digit case of §2 is
	// reached by an unshifted digit character or by Alt+digit, so the label
	// toggle and group recall never meet — the apparent contradiction this
	// site used to record was two different tokens. The shifted-digit arm
	// lives in the digit loop below, where the same key edge is classified.
	if kbd.KeyDown(input.KeyBackquote) {
		b.toggleDamageBars()
	}
	// Game-speed and pause keys [07 §2][07 §11]: +/- clamp Requested 1..20
	// with localized messages; Pause toggles pause with the retail message.
	if kbd.KeyDown(input.KeyPause) {
		b.togglePause()
	}
	if !b.developer.film && (kbd.KeyDown(input.KeyEqual) || kbd.KeyDown(input.KeyNumpadAdd)) {
		b.adjustGameSpeed(1)
	}
	if !b.developer.film && (kbd.KeyDown(input.KeyMinus) || kbd.KeyDown(input.KeyNumpadSubtract)) {
		b.adjustGameSpeed(-1)
	}
	// F9 and F10 are Nanolathe bindings, not retail's: retail's dispatcher has
	// no case for either key (DESIGN_GPU_RENDERER §14.6). F9 toggles the view
	// scale about the viewport centre; F10 asks the window adapter to swap
	// executors. Both are presentation-only — the simulation cannot tell which
	// scale or which executor is active [I6].
	if !ctrlHeld && kbd.KeyDown(input.KeyF9) {
		// The modern executor cycles the same three factors as animated zoom
		// targets; the classic one steps the record scale as it always did
		// (DESIGN_GPU_RENDERER §16.8).
		b.toggleViewScale(cl.Enhanced())
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF10) {
		requestRendererToggle(cl)
	}
	// Digit routing uses the established SwitchAlt gate [07 R-CAM-01 §4].
	// Ctrl+digit is assignment and plays `CreateSquad`; the non-page branch is
	// group recall with Shift as preserve / toggle and plays `SelectSquad`
	// [07 R-CAM-01 §2][07 §9] C9-C10. Ctrl+0 has no case.
	//
	// Which physical edges reach the digit case is the key-token producer of
	// [07 R-CAM-01 §14]. Ctrl composes the token itself, so Ctrl+Shift+digit
	// still assigns. Without Ctrl the digit case is reached by an *unshifted*
	// digit character or by Alt+digit — Alt's system key-down pushes the raw
	// digit value and produces no character message. Shift with Alt therefore
	// keeps the digit token and the Shift argument recall reads is live only
	// for Shift+Alt+digit; Shift without Alt yields the shifted character
	// instead, and only `!` `#` `*` (Shift+1/3/8) have a case — the label
	// toggle. The other six shifted digits do nothing. Under the default
	// SwitchAlt = 0 that makes additive recall Shift+Alt+digit; with
	// SwitchAlt = 1 a plain digit recalls and additive recall is unreachable
	// from the keyboard, which is retail's behaviour and not a gap to patch.
	for d := 1; d <= 9; d++ {
		var key input.Key
		switch d {
		case 1:
			key = input.Key1
		case 2:
			key = input.Key2
		case 3:
			key = input.Key3
		case 4:
			key = input.Key4
		case 5:
			key = input.Key5
		case 6:
			key = input.Key6
		case 7:
			key = input.Key7
		case 8:
			key = input.Key8
		case 9:
			key = input.Key9
		}
		if !kbd.KeyDown(key) {
			continue
		}
		altHeld := kbd.KeyHeld(input.KeyAlt)
		switch {
		case ctrlHeld:
			if b.DispatchGroupAssign(d) == nil {
				b.playUICue(cl, "CreateSquad") // [07 R-CAM-01 §2]
			}
		case !in.ShortcutTokenMode && kbd.HasShift() && !altHeld:
			// The shifted-digit character tokens. `!` `#` `*` flip the label
			// bit; the other six have no case [07 R-CAM-01 §14 item 2].
			if d == 1 || d == 3 || d == 8 {
				b.toggleDamageBars()
			}
		default:
			b.routeDigit(d, altHeld, kbd.HasShift(), cl)
		}
	}
	// Page next/prev data-driven with guard [R-P0-03][07 §9] C10: no hardcoding.
	// `,` is the previous page and `.` the next, both with the `nextbuildmenu`
	// cue [07 R-CAM-01 §2]; PageUp/PageDown and the shifted arrows are this
	// build's extra bindings and keep working.
	if kbd.KeyDown(input.KeyPrior) || kbd.KeyDown(input.KeyRight) && kbd.HasShift() {
		b.nextBuildPage()
	}
	if kbd.KeyDown(input.KeyNext) || kbd.KeyDown(input.KeyLeft) && kbd.HasShift() {
		b.prevBuildPage()
	}
	// `.` and `,` are the next/previous build page of [07 R-CAM-01 §2]. The
	// `nextbuildmenu` cue that row names belongs to the page-switch routine
	// itself [07 §9 "Page encoding is closed"], so it is raised inside the page
	// helpers below and not a second time here.
	if !ctrlHeld && kbd.KeyDown(input.KeyPeriod) {
		b.nextBuildPage()
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyComma) {
		b.prevBuildPage()
	}
	// The follow camera. `t` tracks the next selected unit after the current
	// tracked object in slot order and `T` (Shift held) the previous, wrapping
	// within the local slot range; with nothing selected the tracked object
	// becomes null. Neither moves the camera itself — the follow step does
	// [07 R-CAM-01 §2][07 R-CAM-01 §12].
	if !ctrlHeld && kbd.KeyDown(input.KeyT) {
		b.cycleFollowTarget(kbd.HasShift())
	}
	if ctrlHeld {
		b.dispatchCtrlLetters(kbd)
	}
	// Camera bookmarks: Ctrl+F5..F8 store the current origin into slot 0..3 and
	// F5..F8 recall it, both with the `SelectSquad` cue [07 R-CAM-01 §2]
	// [07 R-CAM-01 §12].
	for slot, key := range [camera.BookmarkSlots]input.Key{input.KeyF5, input.KeyF6, input.KeyF7, input.KeyF8} {
		if !kbd.KeyDown(key) {
			continue
		}
		if ctrlHeld {
			if b.cam.StoreBookmark(slot) {
				b.playUICue(cl, "SelectSquad")
			}
			continue
		}
		if b.cam.RecallBookmark(slot) {
			if b.deferFollowInput {
				b.pendingFollowInput = func() { b.cam.RecallBookmark(slot) }
			}
			b.playUICue(cl, "SelectSquad")
		}
	}
	if !ctrlHeld && !kbd.HasShift() && kbd.KeyDown(input.KeyF1) {
		b.openUnitInfo()
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF3) {
		b.glideToMessageSource()
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF4) {
		// Interface-flags bit 0x80 has exactly two readers, and F4 pins both
		// [07 R-CAM-01 §14 "F4 pins the score panel open and arms the kill/loss
		// flash"]. The score panel shows while the bit is set as if Space were
		// held ([07 R-HUD-04 §1]), and the kill-credit finalize arms the
		// crediting slot's kill flash and the victim slot's loss flash to 30
		// **only** while it is set — with F4 off the arrays are never armed and
		// a Space-held panel shows steady numbers. Both readers are wired; the
		// bit is not a term of the rail slide. The bit's user-facing name is
		// recorded Unknown in [07 §2] — no string in the image names it — and
		// it is a naming curiosity, not an open behavioral question: both
		// readers are closed and nothing here or downstream reads a name.
		b.panelHoldFlag = !b.panelHoldFlag
	}
	if !ctrlHeld && kbd.KeyDown(input.KeyF12) {
		b.messageRing().Clear() // [07 R-CAM-01 §2]
	}
	// Escape: an armed latch or placement returns to idle; an idle latch
	// deselects everything [07 R-CAM-01 §2]. viewerStep intercepts the same
	// edge ahead of this dispatcher, so both copies apply the one arm.
	if kbd.KeyDown(input.KeyEscape) {
		if b.battleState().Input.Latch == input.LatchNormal && !b.battleState().PlacementArmed() {
			_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		}
		b.disarmPlacement()
		b.resetOrderLatch()
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		return true
	}
	return false
}

func (b *battleSession) routeDigit(digit int, altHeld, shiftHeld bool, cl *client.Client) {
	// The gate is `switchAlt == alt` [07 R-CAM-01 §4]: by default digits pick
	// build pages and Alt+digit recalls groups; with the persistent `SwitchAlt`
	// option set the two swap. The cached bit is installed at battle entry, so
	// this hotkey path has no settings I/O [07 R-CAM-01 §4].
	mode := byte(0)
	if b.switchAlt {
		mode = 1
	}
	if hud.RoutesToPage(mode, altHeld) {
		b.switchBuildPage(digit)
		return
	}
	if b.DispatchGroupRecall(digit, shiftHeld) == nil {
		b.playUICue(cl, "SelectSquad") // [07 R-CAM-01 §2]
	}
}

// isTalkGUIActive reports whether TALK.GUI suppresses held-arrow movement [07 §10].
// ownsFrame remains set through a commit/cancel frame so the closing Enter or
// Escape cannot expose held arrow state to the camera pass.
func (b *battleSession) isTalkGUIActive() bool { // [07 §10]
	return b != nil && (b.chat.active || b.chat.ownsFrame)
}

// dispatchCtrlLetters is the Ctrl+letter column of [07 R-CAM-01 §2]: Ctrl+A,
// Ctrl+C, Ctrl+D, Ctrl+S, Ctrl+Z, and the `CTRL_%c` category selects on
// Ctrl+B and Ctrl+E..R and Ctrl+T..Y. Ctrl+A..Z reach retail as tokens
// 0xAA..0xC3, so exactly one arm runs per press.
func (b *battleSession) dispatchCtrlLetters(kbd *input.KeyboardState) {
	shift := kbd.HasShift()
	switch {
	case kbd.KeyDown(input.KeyA):
		// Select every own selectable unit, additive over the current
		// selection, and clear the current build-menu unit [07 R-CAM-01 §2].
		// This build recomputes the command page's builder from the selection
		// at every publication boundary, so clearing the build-menu unit here
		// means dropping the armed placement.
		b.commitSelection(b.ownSelectableHandles(nil), true)
		b.disarmPlacement()
		return
	case kbd.KeyDown(input.KeyD):
		b.selfDestructSelection()
		return
	case b.communitySelectionEnabled() && !shift && kbd.KeyDown(input.KeyB):
		b.cycleCommunityIdle(communityCycleBuilder)
		return
	case b.communitySelectionEnabled() && !shift && kbd.KeyDown(input.KeyF):
		b.cycleCommunityIdle(communityCycleFactory)
		return
	case kbd.KeyDown(input.KeyS):
		if b.communitySelectionEnabled() && !shift {
			b.selectCommunityOnScreenWeapons()
			return
		}
		// The on-screen list, replacing the selection [07 R-CAM-01 §2][07 §8].
		b.commitSelection(b.ownSelectableHandles(b.onScreenUnit), false)
		return
	case kbd.KeyDown(input.KeyZ):
		// Every own selectable unit whose definition matches any currently
		// selected unit's definition [07 R-CAM-01 §2].
		f, ok := b.currentSnapshot()
		if !ok {
			return
		}
		mask := b.selectedDefinitionMask(f)
		if mask.IsZero() {
			return
		}
		b.commitSelection(b.ownSelectableHandles(func(v frame.UnitView) bool {
			return b.inCategory(v, mask)
		}), false)
		return
	}
	// The category letters. Ctrl+C additionally sets the follow-camera tracked
	// object to the last own unit in the `Commander` category set
	// [07 R-CAM-01 §2][07 R-CAM-01 §12].
	for _, letter := range ctrlCategoryLetters {
		if !kbd.KeyDown(letter.key) {
			continue
		}
		// Missing authored categories resolve to an empty membership set.
		// The replacement still runs: without Shift it deselects the previous
		// selection, while Shift preserves it [07 R-CAM-01 §2].
		mask, _ := b.categoryMask("CTRL_" + string(letter.name))
		b.commitSelection(b.ownSelectableHandles(func(v frame.UnitView) bool {
			return b.inCategory(v, mask)
		}), shift)
		if letter.name == 'C' {
			b.followCommander()
		}
		return
	}
}

// ctrlCategoryLetters is the census's `CTRL_%c` domain: Ctrl+B (0xAB),
// Ctrl+C (0xAC), Ctrl+E..Ctrl+R (0xAE..0xBB) and Ctrl+T..Ctrl+Y (0xBD..0xC2).
// Ctrl+A, Ctrl+D, Ctrl+S and Ctrl+Z have their own cases and are absent here
// [07 R-CAM-01 §2]. Stock content authors CTRL_B, CTRL_C, CTRL_F, CTRL_M,
// CTRL_P, CTRL_R, CTRL_V and CTRL_W; every other letter selects nothing, which
// the catalog lookup produces on its own.
var ctrlCategoryLetters = []struct {
	key  input.Key
	name byte
}{
	{input.KeyB, 'B'}, {input.KeyC, 'C'},
	{input.KeyE, 'E'}, {input.KeyF, 'F'}, {input.KeyG, 'G'}, {input.KeyH, 'H'},
	{input.KeyI, 'I'}, {input.KeyJ, 'J'}, {input.KeyK, 'K'}, {input.KeyL, 'L'},
	{input.KeyM, 'M'}, {input.KeyN, 'N'}, {input.KeyO, 'O'}, {input.KeyP, 'P'},
	{input.KeyQ, 'Q'}, {input.KeyR, 'R'},
	{input.KeyT, 'T'}, {input.KeyU, 'U'}, {input.KeyV, 'V'}, {input.KeyW, 'W'},
	{input.KeyX, 'X'}, {input.KeyY, 'Y'},
}

// The live scaled wall clock wraps the multiplication before division;
// queued input timestamps do not participate [07 R-CAM-01 §14].
func (b *battleSession) inputScaledClock() uint32 {
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	return (b.millisSource.Millis32() * uint32(30)) / 1000
}

func idleDragIsClick(s ui.BattleInputState, now uint32) bool {
	dx := int64(s.DragEndWorldX) - int64(s.DragStartWorldX)
	dz := int64(s.DragEndWorldZ) - int64(s.DragStartWorldZ)
	// Stored whole-world endpoints, strict signed deadline and dimensions
	// [07 R-CAM-01 §14]. The release pointer is used only after admission.
	return int32(now) < int32(s.DragPressClock+25) && dx > -32 && dx < 32 && dz > -32 && dz < 32
}
