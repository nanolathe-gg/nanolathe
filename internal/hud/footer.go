package hud

// The ordinary battle footer [07 R-HUD-03 §1–§3].
//
// This file is pure snapshot -> text: it reads the committed frame, the
// immutable compiled catalog, and the presentation-only pointer record, and
// returns the strings, colours and anchor placements the composer paints. It
// holds no session, pool, economy or order-queue pointer [I6].
//
// The one rule that is easy to get backwards: the footer NEVER reads the
// selection. Its three sources are the hovered gadget, the hovered world unit
// and the hovered feature, in that order, and the first present one wins
// outright [07 R-HUD-03 §1].

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// FooterTextColor is the raw palette index every footer field uses unless a
// rule names a colour-map entry instead [07 R-HUD-03 §1].
const FooterTextColor uint8 = 83

// FooterBarRemainder is the colour-map entry the damage bar's unfilled
// remainder takes, from fill+1 to x2 [07 R-HUD-03 §2]. The filled part uses
// PaletteProduction; there is no threshold colouring here — that is the world
// bar's rule [03 R-FX-01 §6].
const FooterBarRemainder uint8 = 4

// UnidentifiedObject is the caption drawn, centred at UNITNAME and alone,
// when the hovered unit fails the viewing player's direct-visibility
// predicate [07 R-HUD-03 §2][03 R-VIS-01 §4].
const UnidentifiedObject = "Unidentified object"

// StockpileWord is the secondary-field caption for a unit with a nonzero
// stockpile percentage [07 R-HUD-03 §2].
const StockpileWord = "Weapon"

// VeteranWord is the single experience word the kills line appends above four
// credited kills; the finer tiers of doc 04 are not shown [07 R-HUD-03 §2].
const VeteranWord = "Veteran"

// buildCardExcludedName is the one literal gadget name the build card refuses
// even when it resolves to a definition [07 R-HUD-03 §3]. The test is a plain
// case-insensitive name compare and has no other reader.
const buildCardExcludedName = "CORBUILD"

// FooterColor is a footer foreground: either a raw palette index (the §1
// default) or a colour-map entry the presentation layer resolves through the
// window's GUI colour block.
type FooterColor struct {
	Value   uint8
	Logical bool // true: Value is a dcb[] entry, not a raw palette index
}

// Raw and dcb constructors keep the call sites readable.
func rawColor(v uint8) FooterColor { return FooterColor{Value: v} }
func dcb(v uint8) FooterColor      { return FooterColor{Value: v, Logical: true} }

// FooterText is one string placed against an anchor. The composer applies
// dy and, for a centred field, the measured glyph-advance sum, because only
// it owns the side font [07 R-HUD-03 §1].
type FooterText struct {
	Anchor   int // index into Anchors
	Text     string
	Color    FooterColor
	Centered bool  // x = anchor.X1 - trunc(textWidth/2)
	FromY2   bool  // y is anchor.Y2 + OffsetY rather than anchor.Y1 + OffsetY
	OffsetY  int32 // added after the anchor's y and before dy
}

// FooterBar is one two-part inclusive fill at a bar anchor.
type FooterBar struct {
	Anchor int
	HP     int32
	Max    int32
}

// FooterLogo is the owner-logo blit at LOGO2 [07 R-HUD-03 §2].
type FooterLogo struct {
	Anchor int
	Frame  int // logos GAF frame index = the owner's lobby colour index
}

// Footer is everything one footer repaint draws over the backdrop. An empty
// Footer means the backdrop stamp alone, which is how "no target clears" is
// true by construction [07 R-HUD-03 §1].
type Footer struct {
	Texts []FooterText
	Bars  []FooterBar
	Logos []FooterLogo
}

// Empty reports whether the footer draws nothing but its backdrop.
func (f Footer) Empty() bool { return len(f.Texts) == 0 && len(f.Bars) == 0 && len(f.Logos) == 0 }

// FooterHover is the presentation-only pointer record of [07 R-HUD-03 §1].
// Every field is owned by the composer's per-frame pointer pass, never by the
// simulation.
type FooterHover struct {
	// Gadget is the battle window tree's hovered-gadget index, -1 when none,
	// and GadgetName its authored name.
	Gadget     int
	GadgetName string
	// Unit is the pointer record's unit word. It is rewritten only inside the
	// view or over the minimap and otherwise keeps its previous value, so a
	// unit hovered on the way to the panel stays in the footer.
	Unit pool.Handle
	// Visible is the viewing player's direct-visibility predicate
	// [03 R-VIS-01 §4]. The composer supplies it because the projection from
	// world position to the committed mask is its own; a nil predicate admits
	// own units only, which is the one case the predicate always passes.
	Visible func(*frame.UnitView) bool
	// Feature is the canonical key of the feature occupying the ground cell
	// under the pointer, empty when none. It is recomputed every frame.
	Feature string
	// StockpilePercent is the hovered unit's stockpile percentage,
	// progress * 100 / reloadtime of the first stockpiling weapon, 0 when none
	// [06 §11.1]. The composer computes it because the reload time lives on the
	// weapon record the queue node's slot index selects.
	StockpilePercent int32
}

// NoGadget is the hovered-gadget index for "no gadget under the pointer".
const NoGadget = -1

// BuildFooter applies the three sources and their fixed priority. viewer is
// the viewing player's slot; overlay is the F11 developer overlay, which
// widens the own-unit block and the feature line [07 R-HUD-03 §1–§3].
func BuildFooter(f *frame.Frame, cat *content.Catalog, viewer uint8, hover FooterHover, overlay bool) Footer {
	var out Footer
	// 1 — hovered gadget. A gadget that is not a product button draws nothing,
	// and that is still a win: the lower sources do not get a turn.
	if hover.Gadget != NoGadget {
		buildCard(&out, cat, hover.GadgetName)
		return out
	}
	// 2 — hovered world unit.
	if hover.Unit != 0 {
		unitReadout(&out, f, cat, viewer, hover, overlay)
		return out
	}
	// 3 — hovered feature.
	if hover.Feature != "" {
		featureLine(&out, cat, hover.Feature, overlay)
	}
	return out
}

// buildCard draws the hovered product button's name/cost line and the
// definition's description [07 R-HUD-03 §3]. A name that does not resolve
// draws nothing.
func buildCard(out *Footer, cat *content.Catalog, gadgetName string) {
	if cat == nil || gadgetName == "" {
		return
	}
	if strings.EqualFold(gadgetName, buildCardExcludedName) {
		return
	}
	def, ok := cat.Unit(content.CanonicalKey(gadgetName))
	if !ok || def == nil {
		return
	}
	// Two spaces between the display name and the costs; both costs are the
	// authored integers, truncated toward zero at compile time [07 R-HUD-03 §3].
	out.Texts = append(out.Texts,
		FooterText{Anchor: AnchorName, Text: fmt.Sprintf("%s  M:%d E:%d", def.Name, def.BuildCostMetal, def.BuildCostEnergy), Color: rawColor(FooterTextColor)},
		FooterText{Anchor: AnchorDescription, Text: def.Description, Color: rawColor(FooterTextColor)},
	)
}

// featureLine draws the hovered feature's description with its authored metal
// and energy amounts [07 R-HUD-03 §3].
func featureLine(out *Footer, cat *content.Catalog, key string, overlay bool) {
	if cat == nil {
		return
	}
	def := cat.Features[content.CanonicalKey(key)]
	if def == nil {
		return
	}
	if def.NoDisplayInfo && !overlay {
		return
	}
	text := def.Description
	if overlay {
		text = def.CanonicalKey
	}
	// An indestructible feature shows the name alone. Otherwise the line is
	// "%s %s%s" whose second and third arguments are themselves " M:%d" when
	// the authored metal is nonzero (empty otherwise) and " E:%d" when the
	// energy is nonzero, both truncated toward zero from the stored singles
	// [07 R-HUD-03 §3]. It is built here exactly as that format rather than by
	// appending, because the separator DOUBLES: one space comes from the
	// format and one from whichever argument is present, so a metal-only or an
	// energy-only feature reads "Rock  M:50" / "Rock  E:20" with two spaces,
	// and a feature with both reads "Rock  M:50 E:20". The doubling is
	// emergent here, which is why the section annotates it only on the build
	// card, where the same two spaces are visible in that literal
	// "%s  M:%d E:%d". A retired play-test plan restated this line with a single
	// space; research owns behavior and that restatement was the imprecise one,
	// so do not "fix" this back to one space. The composer itself is
	// DESIGN_INTERFACE_HUD_INPUT "The footer".
	if !def.Indestructible {
		metalField, energyField := "", ""
		if def.Metal != 0 {
			metalField = fmt.Sprintf(" M:%d", def.Metal)
		}
		if def.Energy != 0 {
			energyField = fmt.Sprintf(" E:%d", def.Energy)
		}
		text = fmt.Sprintf("%s %s%s", text, metalField, energyField)
	}
	// A string of nothing but the format's separators paints no glyphs; skip
	// the draw rather than issuing a no-op.
	if strings.TrimSpace(text) == "" {
		return
	}
	out.Texts = append(out.Texts, FooterText{Anchor: AnchorName, Text: text, Color: rawColor(FooterTextColor)})
}

// unitReadout draws the hovered unit's name, damage bar, owner logo and — for
// an own unit — its rates, kills line, order caption and secondary field
// [07 R-HUD-03 §2].
func unitReadout(out *Footer, f *frame.Frame, cat *content.Catalog, viewer uint8, hover FooterHover, overlay bool) {
	view := FooterUnit(f, hover.Unit)
	if view == nil {
		// Admission: the hovered unit must be alive. A stale pointer word whose
		// unit is gone draws nothing at all [07 R-HUD-03 §2].
		return
	}
	own := view.Owner == viewer
	if !hover.visible(view, viewer) {
		out.Texts = append(out.Texts, FooterText{Anchor: AnchorUnitName, Text: UnidentifiedObject, Color: rawColor(FooterTextColor), Centered: true})
		return
	}
	def := footerUnitDef(cat, view)
	// The name is the definition record's leading name field, drawn verbatim
	// with no second localization lookup. Retail substitutes the owning
	// player's lobby name only in a multiplayer session (kind 3), which is out
	// of Nanolathe's scope [07 R-HUD-03 §2][08 R-OOS-01].
	if def != nil && def.Name != "" {
		out.Texts = append(out.Texts, FooterText{Anchor: AnchorUnitName, Text: def.Name, Color: rawColor(FooterTextColor), Centered: true})
	}
	// The bar is drawn for an own unit or a definition that does not author
	// hidedamage; allies see no bar either [07 R-HUD-03 §2][04 R-SPEC-01 §6].
	if own || def == nil || !def.HideDamage {
		out.Bars = append(out.Bars, FooterBar{Anchor: AnchorDamageBar, HP: view.Health, Max: view.MaxHealth})
	}
	if view.OwnerColorKnown {
		out.Logos = append(out.Logos, FooterLogo{Anchor: AnchorLogo2, Frame: int(view.OwnerColor)})
	}
	if !own && !overlay {
		return
	}
	// The four archived rate fields, in anchor order, each clamped below at
	// zero before formatting. The sign characters are literal, so an idle unit
	// shows +0.0, +0, -0.0, -0 [07 R-HUD-03 §2][05 R-ECO-01 §5].
	out.Texts = append(out.Texts,
		FooterText{Anchor: AnchorUnitMetalMake, Text: fmt.Sprintf("+%.1f", float64(clampRate(view.ArchivedMetalMake))), Color: dcb(PaletteProduction)},
		FooterText{Anchor: AnchorUnitEnergyMake, Text: fmt.Sprintf("+%.0f", float64(clampRate(view.ArchivedEnergyMake))), Color: dcb(PaletteProduction)},
		FooterText{Anchor: AnchorUnitMetalUse, Text: fmt.Sprintf("-%.1f", float64(clampRate(view.ArchivedMetalUse))), Color: dcb(PaletteConsumption)},
		FooterText{Anchor: AnchorUnitEnergyUse, Text: fmt.Sprintf("-%.0f", float64(clampRate(view.ArchivedEnergyUse))), Color: dcb(PaletteConsumption)},
	)
	if text := KillsLine(view.Flags, view.Kills); text != "" {
		out.Texts = append(out.Texts, FooterText{Anchor: AnchorDamageBar, Text: text, Color: dcb(PaletteNormal), FromY2: true, OffsetY: 2})
	}
	// The order caption is the caption column of the order-kind table for the
	// unit's current order, or the table's row-0 caption — the empty sentinel
	// name — when it has none [07 R-HUD-03 §2][04 R-ORD-01 §1].
	order := footerCurrentOrder(f, hover.Unit)
	if order != nil && order.StateLabel != "" {
		out.Texts = append(out.Texts, FooterText{Anchor: AnchorMissionText, Text: order.StateLabel, Color: rawColor(FooterTextColor), Centered: true})
	}
	secondaryField(out, f, cat, viewer, hover, order, own)
}

// secondaryField fills UNITNAME2/DAMAGEBAR2 with the hovered unit's stockpile
// or, when there is none, its order target [07 R-HUD-03 §2]. Neither leaves
// the pair blank.
func secondaryField(out *Footer, f *frame.Frame, cat *content.Catalog, viewer uint8, hover FooterHover, order *frame.OrderView, own bool) {
	if own && hover.StockpilePercent != 0 {
		hp := hover.StockpilePercent
		if hp < 0 {
			hp = 0
		}
		if hp > 100 {
			hp = 100
		}
		out.Texts = append(out.Texts, FooterText{Anchor: AnchorUnitName2, Text: StockpileWord, Color: rawColor(FooterTextColor), Centered: true})
		out.Bars = append(out.Bars, FooterBar{Anchor: AnchorDamageBar2, HP: hp, Max: 100})
		return
	}
	// The target lookup runs only for own units.
	if !own || order == nil || order.Target == 0 {
		return
	}
	target := FooterUnit(f, order.Target)
	if target == nil {
		return
	}
	// The target must pass the same visibility predicate the hovered unit did.
	if !hover.visible(target, viewer) {
		return
	}
	def := footerUnitDef(cat, target)
	if def != nil && def.Name != "" {
		out.Texts = append(out.Texts, FooterText{Anchor: AnchorUnitName2, Text: def.Name, Color: rawColor(FooterTextColor), Centered: true})
	}
	if target.Owner == viewer || def == nil || !def.HideDamage {
		out.Bars = append(out.Bars, FooterBar{Anchor: AnchorDamageBar2, HP: target.Health, Max: target.MaxHealth})
	}
}

// KillsLine formats the credited-kill line, or "" when the unit is unarmed or
// has no kills. Above four kills the localized Veteran word is appended
// [07 R-HUD-03 §2].
func KillsLine(statusFlags uint32, kills int32) string {
	if statusFlags&units.ArmedStatus == 0 || kills == 0 {
		return ""
	}
	word := "kills"
	if kills == 1 {
		word = "kill"
	}
	if kills > 4 {
		return fmt.Sprintf("%d %s - %s", kills, word, VeteranWord)
	}
	return fmt.Sprintf("%d %s", kills, word)
}

// FooterBarFill returns the inclusive last filled column of a footer bar:
// x1 + (w * hp) / maxdamage with hp clamped into 0..maxdamage, a signed
// truncating divide. hp = 0 still paints the column at x1, and the remainder
// from fill+1 to x2 takes the second colour [07 R-HUD-03 §2].
func FooterBarFill(r Rect, hp, max int32) int32 {
	if max <= 0 {
		return r.X1
	}
	if hp < 0 {
		hp = 0
	}
	if hp > max {
		hp = max
	}
	w := r.X2 - r.X1
	return r.X1 + int32(int64(w)*int64(hp)/int64(max))
}

// FooterUnit returns the committed view of handle, or nil. A unit missing
// from the frame is not alive for footer purposes [07 R-HUD-03 §2].
func FooterUnit(f *frame.Frame, handle pool.Handle) *frame.UnitView {
	if f == nil || handle == 0 {
		return nil
	}
	for i := range f.Units {
		if f.Units[i].Slot == handle {
			return &f.Units[i]
		}
	}
	return nil
}

// visible applies the composer's direct-visibility predicate. Own units
// always pass it, so a nil predicate degrades to the own-unit test rather
// than to "everything is identified" [03 R-VIS-01 §4].
func (h FooterHover) visible(v *frame.UnitView, viewer uint8) bool {
	if v == nil {
		return false
	}
	if v.Owner == viewer {
		return true
	}
	return h.Visible != nil && h.Visible(v)
}

func footerUnitDef(cat *content.Catalog, v *frame.UnitView) *content.UnitDef {
	if cat == nil || v == nil {
		return nil
	}
	if v.DefName != "" {
		if def, ok := cat.Unit(v.DefName); ok {
			return def
		}
	}
	def, _ := cat.UnitDefByIndex(uint32(v.DefID))
	return def
}

// footerCurrentOrder returns the unit's current order record, primary queue
// first, or nil when it has none.
func footerCurrentOrder(f *frame.Frame, handle pool.Handle) *frame.OrderView {
	if f == nil || handle == 0 {
		return nil
	}
	for i := range f.OrderQueues {
		q := &f.OrderQueues[i]
		if q.Unit != handle {
			continue
		}
		if len(q.Primary) != 0 {
			return &q.Primary[0]
		}
		if len(q.Secondary) != 0 {
			return &q.Secondary[0]
		}
		return nil
	}
	return nil
}

// clampRate is the archived slot's clamp: the compare is "less-or-equal ->
// use 0", so a negative-zero slot formats as the positive-zero form
// [07 R-HUD-03 §2].
func clampRate(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	return v
}
