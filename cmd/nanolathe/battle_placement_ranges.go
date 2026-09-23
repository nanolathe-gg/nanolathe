package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func (b *battleSession) placementRangesActive(c *client.Client) bool {
	if b == nil || b.modernDrag != nil || c == nil || !c.Enhanced() || !c.IsFocused() || b.cam == nil || b.cat == nil || b.battleState().Modal() != ui.BattleModalClosed || b.isResultVisible() || b.isTalkGUIActive() {
		return false
	}
	state := b.battleState().Input
	enabled := b.rangePreferences.PlacementWeaponRanges == nil || *b.rangePreferences.PlacementWeaponRanges
	return state.BuildDef != "" && b.overWorld(state.PointerX, state.PointerY) && (enabled || state.ShiftHeld && b.rangesShown())
}

// Placement contributes weapon primitives to the ordinary queue-overlay pass,
// sharing +showranges projection, terrain chords, colours, labels and rasterizer.
// It reads the prospective product and snapped site, never live units or queues
// (Modern presentation policy, DESIGN_GPU_RENDERER §20).
func (b *battleSession) placementRangeOverlay(c *client.Client, opt hud.QueueOverlayOptions) []hud.QueuePrimitive {
	if !b.placementRangesActive(c) {
		return nil
	}
	state := b.battleState().Input
	d, ok := b.cat.Unit(state.BuildDef)
	if !ok || d == nil {
		return nil
	}
	var weapons [3]hud.RangeWeapon
	for slot, w := range [3]*content.WeaponDef{d.Weapon1Def, d.Weapon2Def, d.Weapon3Def} {
		if !content.IsWeaponInactive(w) {
			weapons[slot] = hud.RangeWeapon{Enabled: true, Range: w.Range}
		}
	}
	x, z := world.PlacementCenter(state.BuildCellX, state.BuildCellZ, state.BuildFootX, state.BuildFootZ)
	center := hud.QueueWorldPoint{X: x, Y: numeric.Fixed(int64(state.BuildSiteH) << 16), Z: z}
	return hud.WeaponRangeOverlay(center, weapons, opt)
}
