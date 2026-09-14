package main

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type tacticalRangeKind uint8

const (
	tacticalWeapon tacticalRangeKind = iota
	tacticalRadar
	tacticalSonar
	tacticalRadarJam
	tacticalSonarJam
	tacticalBuild
	tacticalIntercept
)

// These colors and dash patterns are Enhanced UI choices, not retail rules
// (GPU design §20). Labels make the tactical categories explicit.
var tacticalRangeStyles = [...]struct {
	label  string
	rgb    [3]uint8
	dashed bool
}{
	{"Weapon", [3]uint8{255, 150, 40}, false},
	{"Radar", [3]uint8{40, 230, 230}, false},
	{"Sonar", [3]uint8{90, 150, 255}, true},
	{"Radar jammer", [3]uint8{225, 100, 255}, true},
	{"Sonar jammer", [3]uint8{255, 120, 185}, true},
	{"Build", [3]uint8{100, 240, 100}, true},
	{"Interceptor guide", [3]uint8{255, 230, 100}, true},
}

type tacticalRange struct {
	kind     tacticalRangeKind
	radius   int32
	inactive bool
}
type tacticalRangeSet struct {
	values [8]tacticalRange
	n      int
}

func (s *tacticalRangeSet) add(kind tacticalRangeKind, radius int32, inactive bool) {
	if radius <= 0 {
		return
	}
	for _, r := range s.values[:s.n] {
		if r.kind == kind && r.radius == radius {
			return
		}
	}
	if s.n < len(s.values) {
		s.values[s.n] = tacticalRange{kind, radius, inactive}
		s.n++
	}
}

// Radii are authored planning guides, not firing solutions [06 §3.3]. Sensor
// and build field widths follow the existing queue overlay [07 R-P0-11 §3].
func tacticalRanges(d *content.UnitDef, enabled [3]bool, preview, activated bool) tacticalRangeSet {
	var out tacticalRangeSet
	if d == nil {
		return out
	}
	for i, w := range [3]*content.WeaponDef{d.Weapon1Def, d.Weapon2Def, d.Weapon3Def} {
		// Missing slots can resolve to NOWEAPON with a nonzero authored range.
		if content.IsWeaponInactive(w) || (!preview && !enabled[i]) {
			continue
		}
		if w.Interceptor {
			// Retail acquisition tests a square about the stored aim point; this
			// circle is only the authored coverage guide [06 §11.2].
			out.add(tacticalIntercept, w.Coverage, false)
		} else {
			out.add(tacticalWeapon, w.Range, false)
		}
	}
	inactive := !preview && d.OnOffable && !activated
	out.add(tacticalRadar, int32(int16(d.RadarDistance)), inactive)
	out.add(tacticalSonar, int32(int16(d.SonarDistance)), inactive)
	out.add(tacticalRadarJam, int32(int16(d.RadarDistanceJam)), inactive)
	out.add(tacticalSonarJam, int32(int16(d.SonarDistanceJam)), inactive)
	if d.Builder {
		out.add(tacticalBuild, int32(uint16(d.BuildDistance)), false)
	}
	return out
}

func (b *battleSession) updateTacticalRangeInput(in *input.State, focused bool) {
	b.tacticalRangesHeld = focused && in != nil && in.Kbd != nil && in.Kbd.KeyHeld(input.KeyShift)
}

func (b *battleSession) tacticalRangesActive(c *client.Client) bool {
	return b != nil && b.modernDrag == nil && c != nil && c.TacticalRangesAvailable() && b.tacticalRangesHeld && b.cat != nil && b.battleState().Modal() == ui.BattleModalClosed && !b.isResultVisible() && !b.isTalkGUIActive()
}

// Suppress capability differences that could identify a commander decoy. The
// icon layer intentionally gives the entire commander-looking group shared art.
func tacticalCommanderAppearance(d *content.UnitDef) bool {
	if d.Commander {
		return true
	}
	for _, t := range strings.Fields(d.Category) {
		if strings.EqualFold(t, "COMMANDER") {
			return true
		}
	}
	for key, value := range d.Unknown {
		if strings.EqualFold(key, "TEDClass") && strings.EqualFold(strings.TrimSpace(value), "COMMANDER") {
			return true
		}
	}
	return false
}

func (b *battleSession) visitTacticalRanges(c *client.Client, f *frame.Frame, visit func(numeric.Fixed, numeric.Fixed, numeric.Fixed, tacticalRange)) {
	if !b.tacticalRangesActive(c) || f == nil {
		return
	}
	state := b.battleState().Input
	overWorld := b.overWorld(state.PointerX, state.PointerY)
	c.VisitTacticalUnits(f, func(v frame.UnitView) {
		if !(v.Owner == f.ViewingPlayer && v.Flags&hud.SelectionFlag != 0) && !(overWorld && !state.DragActive && v.Slot == b.footerHoverUnit) {
			return
		}
		d, ok := b.cat.Unit(v.DefName)
		if !ok || d == nil || (v.Owner != f.ViewingPlayer && tacticalCommanderAppearance(d)) {
			return
		}
		ranges := tacticalRanges(d, v.EnabledWeaponSlots, false, v.Activated)
		for _, r := range ranges.values[:ranges.n] {
			visit(v.X, v.Y, v.Z, r)
		}
	})
	if state.BuildDef == "" || !overWorld {
		return
	}
	d, ok := b.cat.Unit(state.BuildDef)
	if !ok || d == nil {
		return
	}
	x, z := world.PlacementCenter(state.BuildCellX, state.BuildCellZ, state.BuildFootX, state.BuildFootZ)
	y := numeric.Fixed(int64(state.BuildSiteH) << 16)
	ranges := tacticalRanges(d, [3]bool{}, true, true)
	for _, r := range ranges.values[:ranges.n] {
		visit(x, y, z, r)
	}
}

func (s battleHUDUIStage) DrawTacticalOverlay(c *client.Client, f *frame.Frame) {
	if !s.battle.tacticalRangesActive(c) {
		return
	}
	var colors [len(tacticalRangeStyles)]uint8
	for i, style := range tacticalRangeStyles {
		colors[i] = c.TacticalRangeColor(style.rgb)
	}
	s.battle.visitTacticalRanges(c, f, func(x, y, z numeric.Fixed, r tacticalRange) {
		c.DrawTacticalRange(x, y, z, r.radius, colors[r.kind], r.inactive || tacticalRangeStyles[r.kind].dashed)
	})
}

func (b *battleSession) drawTacticalRangeLegend(c *client.Client, f *frame.Frame) {
	if !b.tacticalRangesActive(c) || b.hud == nil || b.hud.console == nil {
		return
	}
	var kinds uint8
	b.visitTacticalRanges(c, f, func(_, _, _ numeric.Fixed, r tacticalRange) { kinds |= 1 << r.kind })
	if kinds == 0 {
		return
	}
	width, height := c.Size()
	legendWidth := 0
	const gap = 12
	for i, style := range tacticalRangeStyles {
		if kinds&(1<<i) != 0 {
			legendWidth += client.MeasureText(b.hud.console, style.label) + gap
		}
	}
	legendWidth -= gap
	lineHeight := max(14, int(b.hud.console.Height)+2)
	x, y := 140, height-40-lineHeight
	c.UIFillRect(x-4, y-3, min(width-x-4, legendWidth+8), lineHeight+6, c.TacticalRangeColor([3]uint8{12, 18, 24}))
	for i, style := range tacticalRangeStyles {
		if kinds&(1<<i) == 0 {
			continue
		}
		if x >= width-8 {
			break
		}
		c.UITextWidth(b.hud.console, style.label, x, y, width-x-8, c.TacticalRangeColor(style.rgb))
		x += client.MeasureText(b.hud.console, style.label) + gap
	}
}
