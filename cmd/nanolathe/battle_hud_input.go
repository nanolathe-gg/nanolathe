package main

// The HUD's pointer pass: hover tracking, hit testing and the click and
// right-click consumers that dispatch a command [07 §9].

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
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

func (h *retailBattleHUD) dispatchFactoryBuild(b *battleSession, product string, count int) error {
	if h != nil && h.factoryDispatch != nil {
		return h.factoryDispatch(b, product, count)
	}
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
	return h.consumeClickDelta(b, x, y, false)
}

// consumeRightClick handles the signed cancellation form of a factory
// product button. Other authored controls are consumed without an action;
// right-click remains deselect/cancel on the world [R-P0-11].
func (h *retailBattleHUD) consumeRightClick(b *battleSession, x, y int32) bool {
	return h.consumeClickDelta(b, x, y, true)
}

func (h *retailBattleHUD) consumeClickDelta(b *battleSession, x, y int32, rightClick bool) bool {
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
	// Builder and product data are optional for general/order controls, but if
	// present they come only from the immutable CommandPage [I6].
	var selectedDef *content.UnitDef
	var snapshotProducts []string
	if f.CommandPage.Builder != 0 && f.CommandPage.PageCount != 0 && b.sess != nil && b.cat != nil {
		if builderView, found := snapshotUnitByHandle(f, f.CommandPage.Builder); found && builderView.Owner == b.sess.LocalOwner {
			if def, found := b.cat.Unit(builderView.DefName); found && def != nil && def.Builder {
				selectedDef = def
				snapshotProducts = f.CommandPage.ProductKeys
			}
		}
	}
	paged := commandPageIsPaged(f)
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
			continue
		}
		// The painter's verdict decides the click too. Greying a command button
		// from the selection aggregate [07 R-HUD-03 §6] does not touch the
		// authored gadget, so the authored-grey test above cannot see it, and a
		// button drawn greyed used to act on a click anyway.
		command, isCommand := commandGadgetVerdict(gad, f, paged)
		if isCommand && command.hidden {
			// A hidden command button is deactivated outright — LOAD without the
			// transport bit, BLAST with it [07 R-HUD-03 §6]. Hidden gadgets are
			// skipped before the hit test [07 R-WGT-01 §1], so one neither acts
			// nor shields whatever lies behind it.
			continue
		}
		// Fixed in authored coordinates: the §6 slide moves no rail gadget
		// rectangle, so the hit test never follows it [07 R-HUD-05]
		// (WU-19-223).
		r := window.PlacedRect(i)
		if !guiRectContains(r, x, y) {
			continue
		}
		if isCommand && command.grey {
			// "Greyed buttons ignore everything" [07 R-WGT-01 §3]: the grey test
			// runs before any activation effect [07 §3], so a greyed button takes
			// no capture and fires nothing. It is still hit-tested — only hidden
			// gadgets are skipped before that — so it does not fire and the pass
			// simply goes on to the gadgets after it [07 R-WGT-01 §1].
			continue
		}
		upperName := strings.ToUpper(gad.Name)
		upperText := strings.ToUpper(gad.Text)
		// BUILD and ORDERS are the two halves of the page-shown bit: they stage
		// from it and from its inverse [07 R-HUD-03 §6], and clicking one sets
		// the state its stage names. ORDERS selects page 0, which is what clears
		// the bit; BUILD selects a build page, which is what sets it.
		if isCommand {
			switch commandButtonName(gad.Name) {
			case "ORDERS":
				if rightClick {
					return true
				}
				// The gadget's own cue, played by the click handler before the
				// stage bit is consumed [07 R-HUD-04 §5]. It is not the page
				// cycle's `nextbuildmenu` — that belongs to `,`/`.` and to
				// NEXT/PREV [07 §9].
				b.playUICue(nil, ordersButtonCue)
				_ = b.DispatchBuildPage(0)
				return true
			case "BUILD":
				if rightClick {
					return true
				}
				b.playUICue(nil, buildButtonCue) // [07 R-HUD-04 §5]
				_ = b.DispatchBuildPage(buildButtonPage(f))
				return true
			}
		}
		// Page navigation. The NEXT and PREV gadgets are the two rows of the
		// page cycle that never return to page 0 [07 R-HUD-03 §6]; the target is
		// computed from the committed page and dispatched as an absolute page,
		// so the cycle's wrap lives in one place [I6].
		nextPage := hud.NextPageButton(int(f.CommandPage.Page), int(f.CommandPage.PageCount))
		prevPage := hud.PrevPageButton(int(f.CommandPage.Page), int(f.CommandPage.PageCount))
		if strings.Contains(upperName, "NEXTPAGE") || strings.Contains(upperName, "NEXT") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEDOWN") {
			if rightClick {
				return true
			}
			_ = b.dispatchBuildPageCued(nextPage)
			return true
		}
		if strings.Contains(upperName, "PREVPAGE") || strings.Contains(upperName, "PREV") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEUP") {
			if rightClick {
				return true
			}
			_ = b.dispatchBuildPageCued(prevPage)
			return true
		}
		if strings.Contains(upperName, "NEXT") || strings.Contains(upperText, "NEXT") {
			if rightClick {
				return true
			}
			// PREV and NEXT are deactivated outright after a page opens when the
			// page-count byte is below 2 [07 R-HUD-03 §6]. With page 0 counted,
			// a builder that has any authored page has a count of at least 2, so
			// the test only ever refuses a builder with no build page at all.
			if f.CommandPage.PageCount > 1 {
				_ = b.dispatchBuildPageCued(nextPage)
				return true
			}
		}
		if strings.Contains(upperName, "PREV") || strings.Contains(upperText, "PREV") {
			if rightClick {
				return true
			}
			if f.CommandPage.PageCount > 1 {
				_ = b.dispatchBuildPageCued(prevPage)
				return true
			}
		}
		// Build product binding data-driven [R-P0-03][02 "Build-menu catalog keys"].
		// GUI may not invent products absent from authored build list.
		if selectedDef != nil && b.cat != nil {
			candidates := []string{gad.Name, gad.Text}
			candidates = append(candidates, gad.Labels...)
			for _, cand := range candidates {
				if cand == "" {
					continue
				}
				// Direct canonical match or case-insensitive
				var pageProductKey string
				for _, key := range snapshotProducts {
					if strings.EqualFold(content.CanonicalKey(key), content.CanonicalKey(cand)) {
						pageProductKey = key
						break
					}
				}
				if pageProductKey == "" {
					continue
				}
				// Resolve canonical product name for dispatch
				prodKey := cand
				if pageProductKey != "" {
					// Production uses the immutable page key as identity; do not
					// recover an alias by consulting the live catalog menu.
					prodKey = pageProductKey
				}
				prodDef, _ := b.cat.Unit(prodKey)
				if prodDef == nil {
					// The committed page is the sole product identity source. An
					// unresolved key is consumed but cannot be classified or queued.
					return true
				}
				prodKey = prodDef.CanonicalKey
				// Retail branches on the product's BMcode, not on the builder
				// [07 §9]. Product BMcode determines queue versus placement.
				if !hud.ProductArmsPlacement(prodDef) {
					delta := factoryBuildDelta(b.battleState().Input.ShiftHeld, rightClick)
					// The counted-add routine's own cue runs before the
					// descriptor routing and before the queue coalesce, so a
					// click that ends up changing nothing is still audible
					// [07 R-P0-11 §1].
					b.playUICue(nil, countedBuildCue(delta))
					if err := h.dispatchFactoryBuild(b, prodKey, delta); err != nil {
						h.dispatchErr = err
					}
					return true
				}
				if rightClick {
					return true
				}
				// The build-button handler "arms the MOBILEBUILD latch (`0xE`),
				// stores the id in a pending-build word and plays the `addbuild`
				// cue — the click arms it, not the ghost show" [07 §9].
				b.armPlacement(prodDef)
				b.playUICue(nil, cueAddBuild)
				return true
			}
		}
		upper := strings.ToUpper(gad.Name)
		// The side-prefixed ONOFF gadget (ARMONOFF / CORONOFF) is the button
		// form of the on/off command [07 R-HUD-03 §6]; it issues exactly what
		// key `O` issues, for every selected onoffable unit, with Shift queuing
		// the record [04 R-ORD-01 §2].
		if strings.HasSuffix(upper, "ONOFF") {
			if rightClick {
				return true
			}
			b.toggleOnOffSelected(b.battleState().Input.ShiftHeld)
			// "the on/off and cloak arms of the same handler play the
			// already-documented `specialorders`" [07 §9]. The cue belongs to the
			// side-panel gadget arm, not to the on/off command itself, so the
			// hotkey path that shares toggleOnOffSelected does not raise it.
			b.playUICue(nil, cueSpecialOrders)
			return true
		}
		// The two stance gadgets are resolved by the same longest-suffix table
		// the stage and grey pass uses, so ARMMOVEORD reaches the stance arm
		// and never the MOVE substring arm below [04 R-STANCE-01 §2].
		switch commandButtonName(gad.Name) {
		case "MOVEORD":
			if rightClick {
				return true
			}
			b.cycleStance(false)
			return true
		case "FIREORD":
			if rightClick {
				return true
			}
			b.cycleStance(true)
			return true
		}
		if strings.Contains(upper, "MOVE") ||
			strings.Contains(upper, "ATTACK") || strings.Contains(upper, "BLAST") ||
			strings.Contains(upper, "DEFEND") || strings.Contains(upper, "REPAIR") ||
			strings.Contains(upper, "PATROL") || strings.Contains(upper, "RECLAIM") ||
			strings.Contains(upper, "CAPTURE") || strings.Contains(upper, "LOAD") ||
			strings.Contains(upper, "UNLOAD") || strings.Contains(upper, "STOP") {
			b.handleHudOrderButton(gad.Name)
			return true
		}
		// Any other GUI button still consumes the click to prevent world leak [07 §3]
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
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
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
		if i == 0 || gad.Active == 0 || gad.GrayedOut != 0 || gad.Kind != gui.KindButton {
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
