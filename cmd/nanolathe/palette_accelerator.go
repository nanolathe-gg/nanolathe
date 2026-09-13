package main

// Command-page accelerators select the same visible gadget record as a pointer
// release. They are ordered token consumers, never held-key fallbacks
// [07 §2][07 §9][07 R-WGT-01 §3].

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

type paletteActivationContext struct {
	frame    *frame.Frame
	window   *gui.Window
	page     *formats.GAF
	paged    bool
	selected *content.UnitDef
}

func (h *retailBattleHUD) paletteContext(b *battleSession) (paletteActivationContext, bool) {
	if h == nil || b == nil {
		return paletteActivationContext{}, false
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return paletteActivationContext{}, false
	}
	w, page, err := h.windowForRequired(b, f)
	if err != nil {
		h.assetErr = err
		return paletteActivationContext{}, false
	}
	if w == nil {
		return paletteActivationContext{}, false
	}
	ctx := paletteActivationContext{frame: f, window: w, page: page, paged: commandPageIsPaged(f)}
	if f.CommandPage.Builder != 0 && f.CommandPage.PageCount != 0 && b.sess != nil && b.cat != nil {
		if view, found := snapshotUnitByHandle(f, f.CommandPage.Builder); found && view.Owner == b.sess.LocalOwner {
			if def, found := b.cat.Unit(view.DefName); found && def != nil && def.Builder {
				ctx.selected = def
			}
		}
	}
	return ctx, true
}

// palettePanel returns the one retained service state for this exact command
// window instance. A numbered/generated page is a distinct instance even when
// its controls share names with another page [07 R-WGT-01 §§1-3].
func (h *retailBattleHUD) palettePanel(window *gui.Window) *ui.Panel {
	if h == nil || window == nil {
		return nil
	}
	if h.palettePanels == nil {
		h.palettePanels = make(map[*gui.Window]*ui.Panel)
	}
	if p := h.palettePanels[window]; p != nil {
		return p
	}
	p := ui.NewPanel(window)
	h.palettePanels[window] = p
	return p
}

// preparePalettePanel makes the retained generic service see the current
// command-page visibility and capability verdict. The compiled GUI record is
// restored afterwards because the painter owns its own dynamic grey branch.
func (h *retailBattleHUD) preparePalettePanel(p *ui.Panel, ctx paletteActivationContext) func() {
	if p == nil || ctx.window == nil {
		return func() {}
	}
	grey := make([]int16, len(ctx.window.Gadgets))
	for i, gad := range ctx.window.Gadgets {
		grey[i] = gad.GrayedOut
		active := gad.Active != 0
		if command, isCommand := commandGadgetVerdict(gad, ctx.frame, ctx.paged); isCommand {
			active = active && !command.hidden
			if command.grey {
				ctx.window.Gadgets[i].GrayedOut |= 1
			}
		}
		p.SetActiveAt(i, active)
	}
	return func() {
		for i := range ctx.window.Gadgets {
			ctx.window.Gadgets[i].GrayedOut = grey[i]
		}
	}
}

// servicePaletteFrame is the command window's shared generic widget pass. Its
// production caller supplies tokens and pointer events together, preserving
// the first firing gadget in authored order. Direct pointer callers use the
// same retained panel, result index, capture and staged-button state [07 R-WGT-01 §§1-3,6-7][07 R-WGT-02 §5].
func (h *retailBattleHUD) servicePaletteFrame(b *battleSession, in *input.State, tokens bool) (ui.ServiceResult, bool) {
	result := ui.ServiceResult{FiredIndex: -1, HoverIndex: -1}
	if h == nil || b == nil || in == nil {
		return result, false
	}
	// Resolving/opening precedes PeekTokens: a command-window selection open
	// flushes the old ring before this new owner's first service pass.
	ctx, ok := h.paletteContext(b)
	if !ok {
		return result, false
	}
	p := h.palettePanel(ctx.window)
	if p == nil {
		return result, false
	}
	restore := h.preparePalettePanel(p, ctx)
	defer restore()

	frame := pointerFrame(in, nil, false)
	if tokens {
		// This is the zero-token-mode command palette: quickkeys may claim the
		// shared ring prefix, while the navigation/default matrix remains off.
		frame.Tokens = in.PeekTokens()
	}
	if in.Kbd != nil {
		frame.AltHeld = in.Kbd.KeyHeld(input.KeyAlt)
	}
	wasCaptured := p.CaptureIndex() >= 0
	result = p.ServiceFrame(frame, ui.WidgetHooks{ArtFrames: func(index int) int {
		if index < 0 || index >= len(ctx.window.Gadgets) {
			return 0
		}
		if entry := h.gadgetArtEntry(ctx.window.Gadgets[index], ctx.page); entry != nil {
			return len(entry.Frames)
		}
		return 0
	}})
	if tokens {
		in.DiscardTokens(result.ConsumedTokens)
	}
	if result.Fired {
		modifiers := input.Modifiers{Alt: frame.AltHeld}
		if in.Kbd != nil {
			modifiers.Shift = in.Kbd.HasShift()
		}
		h.activatePaletteGadget(b, ctx, result.FiredIndex, result.FiredButton == 2, modifiers)
	}
	return result, result.Fired && result.FiredButton != 0 || wasCaptured || p.CaptureIndex() >= 0
}

// servicePalettePointer reuses the production pass verdict rather than servicing
// the same pointer record twice. Direct controller callers still service the
// retained panel here. A captured command control owns its release even
// when the pointer leaves it, so that release cannot reach a world command.
func (h *retailBattleHUD) servicePalettePointer(b *battleSession, in *input.State) bool {
	if b != nil && b.paletteFrameServiced {
		return b.palettePointerOwned
	}
	if unitInfoOpen() {
		return false
	}
	_, owned := h.servicePaletteFrame(b, in, false)
	return owned
}

// activatePaletteGadget returns whether the gadget was an admitted activation
// target. Hidden and greyed records are deliberately not admitted; callers
// then continue their ordered walk [07 R-WGT-01 §1][07 R-HUD-03 §6].
func (h *retailBattleHUD) activatePaletteGadget(b *battleSession, ctx paletteActivationContext, index int, rightClick bool, modifiers input.Modifiers) bool {
	if index <= 0 || index >= len(ctx.window.Gadgets) {
		return false
	}
	gad := ctx.window.Gadgets[index]
	if gad.Active == 0 || gad.Kind != gui.KindButton || gad.GrayedOut&1 != 0 {
		return false
	}
	command, isCommand := commandGadgetVerdict(gad, ctx.frame, ctx.paged)
	if isCommand && (command.hidden || command.grey) {
		return false
	}
	upperName, upperText := strings.ToUpper(gad.Name), strings.ToUpper(gad.Text)
	if isCommand {
		switch commandButtonName(gad.Name) {
		case "ORDERS":
			if rightClick {
				return true
			}
			b.playUICue(nil, ordersButtonCue)
			_ = b.DispatchBuildPage(0)
			return true
		case "BUILD":
			if rightClick {
				return true
			}
			b.playUICue(nil, buildButtonCue)
			_ = b.DispatchBuildPage(buildButtonPage(ctx.frame))
			return true
		}
	}
	next := hud.NextPageButton(int(ctx.frame.CommandPage.Page), int(ctx.frame.CommandPage.PageCount))
	prev := hud.PrevPageButton(int(ctx.frame.CommandPage.Page), int(ctx.frame.CommandPage.PageCount))
	if strings.Contains(upperName, "NEXTPAGE") || strings.Contains(upperName, "NEXT") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEDOWN") {
		if !rightClick {
			_ = b.dispatchBuildPageCued(next)
		}
		return true
	}
	if strings.Contains(upperName, "PREVPAGE") || strings.Contains(upperName, "PREV") && strings.Contains(upperName, "PAGE") || strings.Contains(upperName, "PAGEUP") {
		if !rightClick {
			_ = b.dispatchBuildPageCued(prev)
		}
		return true
	}
	if strings.Contains(upperName, "NEXT") || strings.Contains(upperText, "NEXT") {
		if !rightClick && ctx.frame.CommandPage.PageCount > 1 {
			_ = b.dispatchBuildPageCued(next)
		}
		return true
	}
	if strings.Contains(upperName, "PREV") || strings.Contains(upperText, "PREV") {
		if !rightClick && ctx.frame.CommandPage.PageCount > 1 {
			_ = b.dispatchBuildPageCued(prev)
		}
		return true
	}
	if StockpileGadget(gad) {
		if !rightClick {
			if err := b.DispatchStockpileGadget(modifiers.Shift); err != nil {
				h.dispatchErr = err
			}
		}
		return true
	}
	// The installed name is the product identity [07 §9]. Generated pages
	// patch that name at BUTTON+4; their published membership union is not
	// a slot list. Use the same name that supplies the art and hover card.
	if ctx.selected != nil && b.cat != nil {
		if product, found := b.cat.Unit(gad.Name); found && product != nil {
			if !hud.ProductArmsPlacement(product) {
				delta := factoryBuildDelta(modifiers, rightClick)
				b.playUICue(nil, countedBuildCue(delta))
				if err := h.dispatchFactoryBuild(b, product.CanonicalKey, delta); err != nil {
					h.dispatchErr = err
				}
				return true
			}
			if rightClick {
				return true
			}
			b.armPlacement(product)
			b.playUICue(nil, cueAddBuild)
			return true
		}
	}
	upper := strings.ToUpper(gad.Name)
	if strings.HasSuffix(upper, "ONOFF") {
		if !rightClick {
			b.toggleOnOffSelected(modifiers.Shift)
			b.playUICue(nil, cueSpecialOrders)
		}
		return true
	}
	switch commandButtonName(gad.Name) {
	case "CLOAK":
		if !rightClick {
			b.toggleCloakSelected()
		}
		return true
	case "MOVEORD":
		if !rightClick {
			b.cycleStance(false)
		}
		return true
	case "FIREORD":
		if !rightClick {
			b.cycleStance(true)
		}
		return true
	}
	if strings.Contains(upper, "MOVE") || strings.Contains(upper, "ATTACK") || strings.Contains(upper, "BLAST") || strings.Contains(upper, "DEFEND") || strings.Contains(upper, "REPAIR") || strings.Contains(upper, "PATROL") || strings.Contains(upper, "RECLAIM") || strings.Contains(upper, "CAPTURE") || strings.Contains(upper, "LOAD") || strings.Contains(upper, "UNLOAD") || strings.Contains(upper, "STOP") {
		if !rightClick {
			b.handleHudOrderButton(gad.Name)
		}
		return true
	}
	return true
}
