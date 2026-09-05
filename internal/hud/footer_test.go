package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// footerCatalog is an authored-shaped fixture: the values stand in for FBI
// keys, they are not retail numbers. The test locks the format, not the data.
func footerCatalog() *content.Catalog {
	solar := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testsolar"},
		UnitName:         "TESTSOLAR",
		Name:             "Test Collector",
		Description:      "Produces Energy",
		BuildCostMetal:   135,
		BuildCostEnergy:  1200,
	}
	return &content.Catalog{Units: map[string]*content.UnitDef{"testsolar": solar}}
}

func footerTextAt(f Footer, anchor int) (FooterText, bool) {
	for _, t := range f.Texts {
		if t.Anchor == anchor {
			return t, true
		}
	}
	return FooterText{}, false
}

// The build card is "%s  M:%d E:%d" — two spaces, both costs from the
// definition [07 R-HUD-03 §3].
func TestFooterBuildCardFormatsNameAndCosts(t *testing.T) {
	got := BuildFooter(&frame.Frame{}, footerCatalog(), 0,
		FooterHover{Gadget: 3, GadgetName: "TESTSOLAR"}, false)
	name, ok := footerTextAt(got, AnchorName)
	if !ok {
		t.Fatal("build card drew no NAME line")
	}
	if want := "Test Collector  M:135 E:1200"; name.Text != want {
		t.Errorf("NAME = %q, want %q", name.Text, want)
	}
	if name.Color != (FooterColor{Value: FooterTextColor}) {
		t.Errorf("NAME colour = %+v, want raw index 83", name.Color)
	}
	desc, ok := footerTextAt(got, AnchorDescription)
	if !ok || desc.Text != "Produces Energy" {
		t.Errorf("DESCRIPTION = %q/%v, want the definition's description verbatim", desc.Text, ok)
	}
	// A hovered gadget wins outright: a hovered unit behind it draws nothing.
	if _, ok := footerTextAt(got, AnchorUnitName); ok {
		t.Error("build card must replace the unit readout, not supplement it")
	}
}

// An idle own unit shows the literal signs of the four archived slots
// [07 R-HUD-03 §2][05 R-ECO-01 §5].
func TestFooterIdleOwnUnitShowsSignedZeroRates(t *testing.T) {
	f := &frame.Frame{Units: []frame.UnitView{{
		Slot: 7, Owner: 2, DefName: "testsolar", Health: 100, MaxHealth: 100,
	}}}
	got := BuildFooter(f, footerCatalog(), 2, FooterHover{Gadget: NoGadget, Unit: 7}, false)
	for _, tc := range []struct {
		anchor int
		want   string
		color  FooterColor
	}{
		{AnchorUnitMetalMake, "+0.0", FooterColor{Value: PaletteProduction, Logical: true}},
		{AnchorUnitEnergyMake, "+0", FooterColor{Value: PaletteProduction, Logical: true}},
		{AnchorUnitMetalUse, "-0.0", FooterColor{Value: PaletteConsumption, Logical: true}},
		{AnchorUnitEnergyUse, "-0", FooterColor{Value: PaletteConsumption, Logical: true}},
	} {
		text, ok := footerTextAt(got, tc.anchor)
		if !ok {
			t.Errorf("anchor %s drew nothing", AnchorNames[tc.anchor])
			continue
		}
		if text.Text != tc.want {
			t.Errorf("anchor %s = %q, want %q", AnchorNames[tc.anchor], text.Text, tc.want)
		}
		if text.Color != tc.color {
			t.Errorf("anchor %s colour = %+v, want %+v", AnchorNames[tc.anchor], text.Color, tc.color)
		}
	}
	// A negative archived slot clamps below at zero rather than showing a sign
	// twice: max(v, 0) before formatting.
	f.Units[0].ArchivedMetalMake = -3
	got = BuildFooter(f, footerCatalog(), 2, FooterHover{Gadget: NoGadget, Unit: 7}, false)
	if text, _ := footerTextAt(got, AnchorUnitMetalMake); text.Text != "+0.0" {
		t.Errorf("clamped metal production = %q, want %q", text.Text, "+0.0")
	}
}

// A hovered unit that fails the visibility predicate draws the caption alone
// [07 R-HUD-03 §2][03 R-VIS-01 §4].
func TestFooterNonVisibleEnemyIsUnidentified(t *testing.T) {
	f := &frame.Frame{Units: []frame.UnitView{{
		Slot: 9, Owner: 5, DefName: "testsolar", Health: 40, MaxHealth: 100,
	}}}
	got := BuildFooter(f, footerCatalog(), 2, FooterHover{Gadget: NoGadget, Unit: 9}, false)
	name, ok := footerTextAt(got, AnchorUnitName)
	if !ok || name.Text != UnidentifiedObject {
		t.Fatalf("UNITNAME = %q/%v, want %q", name.Text, ok, UnidentifiedObject)
	}
	if !name.Centered {
		t.Error("Unidentified object is centred at UNITNAME")
	}
	if len(got.Texts) != 1 || len(got.Bars) != 0 || len(got.Logos) != 0 {
		t.Errorf("unidentified readout drew %d texts, %d bars, %d logos; want the caption and nothing else",
			len(got.Texts), len(got.Bars), len(got.Logos))
	}
	// The same unit passes the predicate once the composer's mask admits it.
	visible := func(*frame.UnitView) bool { return true }
	got = BuildFooter(f, footerCatalog(), 2, FooterHover{Gadget: NoGadget, Unit: 9, Visible: visible}, false)
	if name, _ := footerTextAt(got, AnchorUnitName); name.Text != "Test Collector" {
		t.Errorf("identified UNITNAME = %q, want the definition's leading name", name.Text)
	}
	// An enemy readout stops after the bar and logo: no rates, no caption.
	if _, ok := footerTextAt(got, AnchorUnitMetalMake); ok {
		t.Error("the four rate fields are own-unit only")
	}
}

// The feature line's separator is TWO spaces before the metal field, not one.
// [07 R-HUD-03 §3] gives the line as "%s %s%s" whose second and third
// arguments are " M:%d" and " E:%d", so the format's space and the argument's
// space add up. The build card's "%s  M:%d E:%d" in the same section carries
// the two spaces visibly in its literal; here the doubling is emergent, which
// is why only the card is annotated. A retired play-test plan restated the
// feature line with a single space — research owns behavior and that
// restatement was the imprecise one. This case exists so the next reader
// does not "correct" the second space away.
func TestFooterFeatureLineSeparatorIsTwoSpaces(t *testing.T) {
	cat := footerCatalog()
	cat.Features = map[string]*content.FeatureDef{
		"rockmetal": {
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: "rockmetal"},
			Description:      "Rock", Metal: 50,
		},
		"geovent": {
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: "geovent"},
			Description:      "Geothermal Vent", Energy: 20,
		},
		"rockboth": {
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: "rockboth"},
			Description:      "Rock", Metal: 50, Energy: 20,
		},
		"cliff": {
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: "cliff"},
			Description:      "Cliff", Metal: 50, Indestructible: true,
		},
	}
	for _, tc := range []struct{ key, want string }{
		{"rockmetal", "Rock  M:50"},
		{"geovent", "Geothermal Vent  E:20"},
		{"rockboth", "Rock  M:50 E:20"},
		// An indestructible feature shows the name alone [07 R-HUD-03 §3].
		{"cliff", "Cliff"},
	} {
		got := BuildFooter(&frame.Frame{}, cat, 0, FooterHover{Gadget: NoGadget, Feature: tc.key}, false)
		line, ok := footerTextAt(got, AnchorName)
		if !ok {
			t.Errorf("feature %q drew no NAME line", tc.key)
			continue
		}
		if line.Text != tc.want {
			t.Errorf("feature %q line = %q, want %q", tc.key, line.Text, tc.want)
		}
		if line.Color != (FooterColor{Value: FooterTextColor}) {
			t.Errorf("feature %q colour = %+v, want raw index 83", tc.key, line.Color)
		}
	}
}

// The footer never reads the selection: with no hovered gadget, no hovered
// world unit and no hovered feature it draws nothing but its backdrop, even
// when the frame carries a full selection whose primary is a live own unit
// [07 R-HUD-03 §1] ("The footer never reads the selection: neither the primary
// selected unit nor the group is a footer source").
//
// This case exists because a `--shot` capture has no pointer, so it composes
// exactly this state, and an empty bottom strip beside a selected commander
// reads as a rendering regression rather than as the contract. It is not one:
// only a hover fills the strip. If a future change makes a selected unit paint
// the readout, that is the defect, and this test is where it is caught.
func TestFooterIgnoresSelectionWithoutHover(t *testing.T) {
	f := &frame.Frame{
		Units: []frame.UnitView{{
			Slot: 7, Owner: 2, DefName: "testsolar", Health: 100, MaxHealth: 100,
		}},
		Selection: frame.SelectionView{LocalPlayer: 2, Handles: []pool.Handle{7}, Primary: 7},
	}
	got := BuildFooter(f, footerCatalog(), 2, FooterHover{Gadget: NoGadget}, false)
	if !got.Empty() {
		t.Errorf("a selection with no hover drew %d texts, %d bars, %d logos; want the backdrop alone",
			len(got.Texts), len(got.Bars), len(got.Logos))
	}
	// The same frame with the same unit hovered is the readout state, so the
	// emptiness above is the missing hover and not a missing catalog or owner.
	got = BuildFooter(f, footerCatalog(), 2, FooterHover{Gadget: NoGadget, Unit: 7}, false)
	if name, ok := footerTextAt(got, AnchorUnitName); !ok || name.Text != "Test Collector" {
		t.Errorf("hovered UNITNAME = %q/%v, want the definition's leading name", name.Text, ok)
	}
}
