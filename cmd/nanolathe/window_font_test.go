package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

// The button painter's font walk selects the FNT the button's `fontnumber`
// picks from the page's kind-7 records, but every caption run goes through
// the GAF pen: with a GAF font in the slot the selected FNT is never
// consulted, and on the null-slot fallback it replaces the common font
// [03 R-FONT-01 §5][03 R-FONT-01 §6]. Locked here because the two outcomes
// are one silent swap apart — a side page's build count in `armbutt` instead
// of `hattfont12` would look plausible.
func TestProductButtonCaptionSelectedFNTIsOnlyTheNullSlotFallback(t *testing.T) {
	gaf := queueCountFontFixture()
	common := queueCountFNTFixture()
	selected := &formats.FNT{Height: 5}
	for _, code := range []byte{'+', '3'} {
		selected.Glyphs[code] = &formats.FNTGlyph{Width: 4, Height: 5, Bits: make([]byte, 3)}
	}
	gad := gui.Gadget{Kind: gui.KindButton, Attribs: 0x20, CommonAttribs: 4}
	r := gui.Rect{X: 8, Y: 12, W: 64, H: 64}
	const text = "+3"

	// Slot holds hattfont12: the GAF family, whatever the walk selected.
	h := &retailBattleHUD{modalFont: gaf, guiFont: common}
	_, _, family, fallback := h.productButtonCaptionLayoutSelected(gad, r, text, selected)
	if family != gaf || fallback != nil {
		t.Fatalf("with a GAF font in the slot the caption family = (%v, %v), want the GAF slot and no FNT [03 R-FONT-01 §6]", family != nil, fallback)
	}

	// Null slot: the FNT drawer with the selected record, else the common font.
	h = &retailBattleHUD{guiFont: common}
	_, y, family, fallback := h.productButtonCaptionLayoutSelected(gad, r, text, selected)
	if family != nil || fallback != selected {
		t.Fatalf("null slot with a selected record: family = %v, fallback = %v, want the selected FNT", family != nil, fallback)
	}
	_, commonY, _, commonFallback := h.productButtonCaptionLayoutSelected(gad, r, text, nil)
	if commonFallback != common {
		t.Fatalf("null slot with no record: fallback = %v, want the common font", commonFallback)
	}
	// The pen is placed by the family that draws: the two FNT heights differ,
	// so the two pens must too, or the metric was not read from the fallback.
	if y == commonY {
		t.Fatalf("pen Y %d is the same for the selected and the common FNT; the metric must come from the FNT that draws", y)
	}
}
