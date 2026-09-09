package main

// The battle cursor and the footer hover it shares: what the pointer is over
// and what that makes the cursor and the footer show [07 R-HUD-03 §1].

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// updateCursor resolves the software-cursor shape for this frame [07 §8].
// The pointer region, the armed latch, the selection, and the world pick under
// the pointer are the whole input; the chooser owns the truth table.
func (b *battleSession) updateCursor(cl *client.Client) {
	cursors := cl.Cursors()
	if cursors == nil || b.sess == nil {
		return
	}
	mouse, _ := cl.Input().PointerSample()
	mx, my := int32(mouse.X), int32(mouse.Y)
	region := b.classifyPointer(mx, my)
	// The footer's pointer record is written by the same per-frame pointer
	// pass, and by nothing else [07 R-HUD-03 §1].
	b.updateFooterHover(mx, my)
	hover := hud.CursorHover{
		OverWorld:      region != battlePointerChrome,
		Placing:        b.battleState().Input.BuildDef != "",
		PlacementValid: b.battleState().Input.BuildOK,
	}
	if hover.OverWorld && !hover.Placing {
		_, hover.Target, _ = b.pickTarget(mx, my)
		hover.Feature = b.hoverFeature(mx, my)
	}
	sel := hud.CursorSelection{Viewer: b.sess.LocalOwner, Hostile: b.hostile}
	if b.interfaceTypeRightClick() {
		sel.InterfaceType = hud.InterfaceTypeRightClick
	}
	if f, ok := b.currentSnapshot(); ok {
		for _, handle := range f.Selection.Handles {
			for i := range f.Units {
				v := f.Units[i]
				if v.Slot == handle && v.Owner == b.sess.LocalOwner {
					sel.Units = append(sel.Units, b.snapshotUnitCopy(v))
					break
				}
			}
		}
	}
	if f, ok := b.currentSnapshot(); ok {
		for _, ev := range f.Economy {
			if ev.Player == b.sess.LocalOwner {
				sel.Metal, sel.Energy = ev.Metal, ev.Energy
				break
			}
		}
	}
	cursors.SetIndex(hud.ChooseCursor(b.battleState().Input.Latch, sel, hover))
}

// overWorld reports whether a pointer position lies in the world viewport
// rather than on the HUD chrome; chrome forces the idle cursor shape [07 §8].
// It uses the same drawn-chrome layout as the click path, so the shape and
// click destination cannot disagree [C-3][07 §6][07 §8].
func (b *battleSession) overWorld(x, y int32) bool {
	// "When an active modal covers the pointer, world picking and camera edge
	// behavior are gated out" [07 §3]. The unit-information screen is the one
	// battle child window this shell draws over the view, and without this test
	// F1 pressed with the pointer over the open screen would resolve a unit
	// behind it and rebuild the screen [07 R-HUD-03 §8].
	if unitInfoCovers(x, y) {
		return false
	}
	if b.hud != nil {
		return b.hud.overWorld(x, y)
	}
	// When no authored HUD layout is available, use the viewport transform's
	// drawn-chrome rectangle at the negotiated surface [C-3][07 §8]
	// [07 R-HUD-05].
	screenW, screenH := b.surfaceSize()
	vt := client.NewViewportTransform(b.cam, nil, screenW, screenH)
	return vt.Viewport.Contains(x, y)
}

// hoverFeature returns the definition of the feature occupying the cell under
// the pointer, or nil [07 §8][05 "Feature instance and terrain cell"].
func (b *battleSession) hoverFeature(sx, sy int32) *content.FeatureDef {
	if b.cam == nil {
		return nil
	}
	wx, _, wz := b.cursorWorld(sx, sy)
	cx, cz := world.WorldToCell(wx), world.WorldToCell(wz)
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	for _, v := range f.Features {
		footX, footZ := int32(v.FootX), int32(v.FootZ)
		if footX <= 0 {
			footX = 1
		}
		if footZ <= 0 {
			footZ = 1
		}
		if cx < v.CX || cx >= v.CX+footX || cz < v.CZ || cz >= v.CZ+footZ || !snapshotFeatureVisible(f, v, f.ViewingPlayer) {
			continue
		}
		return &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: v.DefName}, FootprintX: int32(v.FootX), FootprintZ: int32(v.FootZ), Height: v.Height, Reclaimable: v.Reclaimable, Geothermal: v.Geothermal, Blocking: v.Blocking}
	}
	return nil
}

// updateFooterHover runs the battle pointer handler's per-frame pass over the
// footer's two world sources [07 R-HUD-03 §1]. The hovered-unit word is
// rewritten only inside the view with no drag-selection rectangle armed, or
// over the minimap; everywhere else it is left alone. The hovered-feature
// word is the feature under the pointer's ground cell and is recomputed every
// frame wherever the pointer is. Neither reads the selection.
func (b *battleSession) updateFooterHover(mx, my int32) {
	if b == nil || b.sess == nil {
		return
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	switch {
	case b.classifyPointer(mx, my) == battlePointerMinimap:
		b.footerHoverUnit = b.minimapHoverUnit(f, mx, my)
	case b.overWorld(mx, my) && !b.battleState().Input.DragActive:
		handle, _, _ := client.PickSnapshotUnit(f, mx, my, b.cam, f.ViewingPlayer)
		b.footerHoverUnit = handle
	}
	b.footerHoverFeature = ""
	if def := b.hoverFeature(mx, my); def != nil {
		b.footerHoverFeature = def.CanonicalKey
	}
}

// footerHover assembles the footer's pointer record for this frame. The
// gadget index comes from the HUD's own hit test; the visibility predicate is
// the committed mask, which only the composer can project into
// [07 R-HUD-03 §1][03 R-VIS-01 §4][I6].
func (b *battleSession) footerHover(f *frame.Frame) hud.FooterHover {
	out := hud.FooterHover{Gadget: hud.NoGadget}
	if b == nil {
		return out
	}
	out.Unit = b.footerHoverUnit
	out.Feature = b.footerHoverFeature
	out.Visible = func(v *frame.UnitView) bool {
		return v != nil && client.SnapshotVisible(f, *v, f.ViewingPlayer)
	}
	out.Gadget, out.GadgetName = b.hud.hoveredGadgetSource()
	out.StockpilePercent = footerStockpilePercent(f, b.cat, out.Unit)
	return out
}

// footerStockpilePercent is the build-page percentage of a stockpiling
// weapon: the BuildWeapon node's progress times 100 over the compiled reload
// time of the weapon its slot index selects, an integer divide [06 §11.1].
// Stockpile nodes live in the unit's secondary queue.
func footerStockpilePercent(f *frame.Frame, cat *content.Catalog, handle pool.Handle) int32 {
	if f == nil || cat == nil || handle == 0 {
		return 0
	}
	view := hud.FooterUnit(f, handle)
	if view == nil || view.DefName == "" {
		return 0
	}
	def, ok := cat.Unit(view.DefName)
	if !ok || def == nil {
		return 0
	}
	for i := range f.OrderQueues {
		q := &f.OrderQueues[i]
		if q.Unit != handle {
			continue
		}
		for _, node := range q.Secondary {
			if node.Kind != "BuildWeapon" {
				continue
			}
			weapon := stockpileWeaponForSlot(def, int(node.Param1))
			if weapon == nil || weapon.ReloadTime <= 0 {
				continue
			}
			return int32(int64(node.Param3) * 100 / int64(weapon.ReloadTime))
		}
		break
	}
	return 0
}

func stockpileWeaponForSlot(def *content.UnitDef, slot int) *content.WeaponDef {
	slots := [3]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def}
	if slot < 0 || slot >= len(slots) {
		return nil
	}
	weapon := slots[slot]
	if content.IsWeaponInactive(weapon) || !weapon.Stockpile {
		return nil
	}
	return weapon
}
