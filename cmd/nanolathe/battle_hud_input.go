package main

// The HUD's pointer pass: hover tracking, hit testing and the click and
// right-click consumers that dispatch a command [07 §9].

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// hoveredGadgetSource reports the footer's first source: the hovered-gadget
// index and its authored name, or hud.NoGadget when the pointer is over no
// gadget [07 R-HUD-03 §1].
func (h *retailBattleHUD) hoveredGadgetSource() (int, string) {
	if h == nil || !h.hoveredGadgetOK {
		return hud.NoGadget, ""
	}
	return h.hoveredGadget, h.hoveredGadgetName
}

// updateHoveredGadget runs the gadget-tree pointer pass over the open command
// page: the hovered index is the gadget whose authored rectangle contains the
// pointer, and it is reset when no window is open [07 R-HUD-03 §1]. A greyed
// product slot is still hovered; its name simply does not resolve to a
// definition, so the card draws nothing [07 R-HUD-03 §3][07 R-HUD-03 §6].
func (h *retailBattleHUD) updateHoveredGadget(b *battleSession, f *frame.Frame, x, y int32) {
	if h == nil {
		return
	}
	h.hoveredGadget, h.hoveredGadgetName, h.hoveredGadgetOK = 0, "", false
	if b == nil {
		return
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return
	}
	if window == nil {
		return
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind != gui.KindButton {
			continue
		}
		// A command button the aggregate hides is deactivated, and hidden
		// gadgets are skipped before the hit test [07 R-HUD-03 §6]
		// [07 R-WGT-01 §1]. A greyed one is not: the hovered-gadget writer has
		// no grey test, so a greyed button still fills the footer's first
		// source [07 R-HUD-03 §1].
		if command, isCommand := commandGadgetVerdict(gad, f, paged); isCommand && command.hidden {
			continue
		}
		// The rail's gadget rectangles are fixed in authored coordinates and
		// the §6 slide never translates them [07 R-HUD-05] (WU-19-223).
		r := window.PlacedRect(i)
		if !guiRectContains(r, x, y) {
			continue
		}
		h.hoveredGadget, h.hoveredGadgetName, h.hoveredGadgetOK = i, gad.Name, true
		return
	}
}

// LastDispatchError returns the last factory-command enqueue failure observed
// by the HUD, if any. It is a diagnostic surface, not retail status text.
func (h *retailBattleHUD) LastDispatchError() error {
	if h == nil {
		return nil
	}
	return h.dispatchErr
}

// dispatchFactoryBuild is the HUD's single call into the command boundary for
// a factory product button [01 §4.4][07 §9]. It used to consult a function
// field on the HUD first, so a test could substitute its own dispatcher; the
// field's only writer was one diagnostic test, which now drives the production
// dispatcher into a refusal it raises itself.
func (h *retailBattleHUD) dispatchFactoryBuild(b *battleSession, product string, count int) error {
	return b.DispatchFactoryBuildDelta(product, count)
}

// consumeClick applies the retail order-button latch parser and data-driven build
// product binding to a visible authored side-panel button [R-P0-03][07 §9].
// Build products are validated against cat.BuildMenus (no invention) and
// dispatched via injected callbacks: mobile builders arm placement (definition
// retained, cursorfindsite [07 §8] 0xE), factories queue immediately [F-P1-008].
// Order buttons are bound via ParseButtonLatch and handleHudOrderButton which
// routes through the injected command dispatch [R-P0-03]. Any HUD gadget hit
// is consumed to prevent leak into world drag [07 §3][F-P0-003]. Page next/prev
// are data-driven with count guard [R-P0-03][07 §9] C10.
func (h *retailBattleHUD) consumeClick(b *battleSession, x, y int32) bool {
	shift := b != nil && b.battleState() != nil && b.battleState().Input.ShiftHeld
	return h.consumeClickDelta(b, x, y, false, shift)
}

func (h *retailBattleHUD) consumeClickWithShift(b *battleSession, x, y int32, shift bool) bool {
	return h.consumeClickDelta(b, x, y, false, shift)
}

// consumeRightClickWithShift handles the signed cancellation form of a factory
// product button. Other authored controls are consumed without an action;
// right-click remains deselect/cancel on the world [R-P0-11].
func (h *retailBattleHUD) consumeRightClickWithShift(b *battleSession, x, y int32, shift bool) bool {
	return h.consumeClickDelta(b, x, y, true, shift)
}

func (h *retailBattleHUD) consumeClickDelta(b *battleSession, x, y int32, rightClick, shift bool) bool {
	if h == nil || b == nil {
		return false
	}
	// The unit information screen is a child window on top of the battle: it
	// services the release before the command page does, and a release inside
	// it never reaches a side-panel control [07 §3][07 R-WGT-01 §1].
	if h.unitInfoConsumeClick(x, y) {
		return true
	}
	// A committed frame is required for every HUD action [I6]. A frame with a
	// non-empty selection but no command-page builder still exposes the authored
	// general/order controls; with an empty selection the command windows are
	// closed to the root and there is nothing above it to click [07 §6].
	f, ok := b.currentSnapshot()
	if !ok {
		return false
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return false
	}
	if window == nil {
		return false
	}
	ctx, ok := h.paletteContext(b)
	if !ok {
		return false
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut&1 != 0 || gad.Kind != gui.KindButton {
			continue
		}
		command, isCommand := commandGadgetVerdict(gad, f, ctx.paged)
		if isCommand && command.hidden {
			continue
		}
		r := window.PlacedRect(i)
		if !guiRectContains(r, x, y) {
			continue
		}
		if h.activatePaletteGadget(b, ctx, i, rightClick, shift) {
			return true
		}
		// Greyed command records are transparent to a later overlapping
		// record, as in the retained generic service [07 R-WGT-01 §§1,3].
		if isCommand && command.grey {
			continue
		}
		return true
	}
	return false
}

// hitTestFor is the session-aware hit test used by battleSession handleInput [F-P0-003].
func (h *retailBattleHUD) hitTestFor(b *battleSession, x, y int32) bool {
	if h == nil || b == nil {
		return false
	}
	// An open modal that covers the pointer owns the press: world picking and
	// camera edge behavior are gated out under it [07 §3].
	if unitInfoCovers(x, y) {
		return true
	}
	var f *frame.Frame
	if b.sess != nil && b.sess.Snapshot != nil {
		f = b.sess.Snapshot.Current()
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return false
	}
	if window == nil {
		return false
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut&1 != 0 || gad.Kind != gui.KindButton {
			continue
		}
		// Neither a greyed nor a hidden command button is an activation target
		// [07 R-WGT-01 §3][07 R-HUD-03 §6], so neither reports a hit here — the
		// same reading the authored-grey test above already applies.
		if command, isCommand := commandGadgetVerdict(gad, f, paged); isCommand && (command.grey || command.hidden) {
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail gadget
		// rectangle, so the hit test never follows it [07 R-HUD-05]
		// (WU-19-223).
		r := window.PlacedRect(i)
		if guiRectContains(r, x, y) {
			return true
		}
	}
	return false
}

func (h *retailBattleHUD) buttonAt(b *battleSession, x, y int32) int {
	if h == nil || b == nil {
		return -1
	}
	var f *frame.Frame
	if b.sess != nil && b.sess.Snapshot != nil {
		f = b.sess.Snapshot.Current()
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return -1
	}
	if window == nil {
		return -1
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut&1 != 0 || gad.Kind != gui.KindButton {
			continue
		}
		// A greyed button takes no capture and a hidden one is skipped before
		// the hit test [07 R-WGT-01 §3][07 R-WGT-01 §1], so neither can be the
		// gadget a press and its release identify.
		if command, isCommand := commandGadgetVerdict(gad, f, paged); isCommand && (command.grey || command.hidden) {
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail gadget
		// rectangle, so the hit test never follows it [07 R-HUD-05]
		// (WU-19-223).
		r := window.PlacedRect(i)
		if guiRectContains(r, x, y) {
			return i
		}
	}
	return -1
}

func (h *retailBattleHUD) sameButton(b *battleSession, x0, y0, x1, y1 int32) bool {
	// While the unit information screen is up it is the active window: the
	// press/release pair is identified against it, not against the command
	// page underneath [07 §3][07 R-WGT-01 §1].
	if unitInfoOpen() {
		return unitInfoCovers(x0, y0) && unitInfoCovers(x1, y1)
	}
	pressed := h.buttonAt(b, x0, y0)
	return pressed >= 0 && pressed == h.buttonAt(b, x1, y1)
}

func guiRectContains(r gui.Rect, x, y int32) bool {
	left, top, right, bottom := r.X, r.Y, r.X+r.W, r.Y+r.H
	return x >= left && x <= right && y >= top && y <= bottom
}

// overWorld reports whether a pointer position lies in the world viewport
// rather than on the HUD chrome [07 §8]. The rail boundary is the authored
// 129-pixel column; the top and bottom strips are as tall as their frames.
// A pointer on the chrome forces the idle cursor shape [07 §8].
//
// The minimap is on the rail and is consumed before this world-region test;
// pointers over it therefore remain chrome [07 §8][07 §10].
func (h *retailBattleHUD) overWorld(x, y int32) bool {
	if h == nil {
		return true
	}
	const railX = 129 // authored rail boundary, matching the PANELSIDE blit
	if x < railX {
		return false
	}
	top := int32(0)
	if h.panelTop != nil {
		top = int32(h.panelTop.Height)
	}
	// The world viewport's last row is `H-33` inclusive at every display
	// mode, one row above the bottom strip's origin [03 §4.1][07 R-HUD-05].
	screenH := h.screenH
	if screenH <= 0 {
		screenH = retailScreenH
	}
	bottom := hud.BottomStripY(screenH)
	return y >= top && y < bottom
}
