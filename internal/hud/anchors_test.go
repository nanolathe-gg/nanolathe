package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func makeFullSide(name string) *content.SideDef {
	anchors := make(map[string]content.Rect, len(AnchorNames))
	for i, an := range AnchorNames {
		// Deterministic fixture values: use i to make each rect distinct but valid.
		// x1=i*10, y1=i*5, x2=x1+100, y2=y1+20
		x1 := int32(i * 10)
		y1 := int32(i * 5)
		anchors[content.CanonicalKey(an)] = content.Rect{X1: x1, Y1: y1, X2: x1 + 100, Y2: y1 + 20}
	}
	return &content.SideDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("SIDE0")},
		Name:             name,
		Anchors:          anchors,
	}
}

func TestAnchorsAll30Present(t *testing.T) {
	side := makeFullSide("ARM")
	a, err := AnchorsFromSide(side)
	if err != nil {
		t.Fatalf("AnchorsFromSide: %v", err)
	}
	if len(a) != 30 {
		t.Fatalf("len Anchors = %d, want 30", len(a))
	}
	for i, name := range AnchorNames {
		got := a[i]
		want, ok := side.Anchor(name)
		if !ok {
			t.Fatalf("side missing %s", name)
		}
		if got.X1 != want.X1 || got.Y1 != want.Y1 || got.X2 != want.X2 || got.Y2 != want.Y2 {
			t.Fatalf("anchor %s mismatch: got %v want %v", name, got, want)
		}
		// Also via ByName
		byName, ok := a.ByName(name)
		if !ok {
			t.Fatalf("ByName %s not found", name)
		}
		if byName != got {
			t.Fatalf("ByName %s mismatch: %v vs %v", name, byName, got)
		}
	}
}

func TestAnchorsMissingIsFatal(t *testing.T) {
	side := makeFullSide("CORE")
	// Remove one mandatory anchor.
	delete(side.Anchors, content.CanonicalKey("ENERGYBAR"))
	_, err := AnchorsFromSide(side)
	if err == nil {
		t.Fatalf("expected error for missing anchor, got nil")
	}
	// Also missing LOGO2
	side2 := makeFullSide("CORE2")
	delete(side2.Anchors, content.CanonicalKey("DAMAGEBAR2"))
	if _, err := AnchorsFromSide(side2); err == nil {
		t.Fatalf("expected error for missing DAMAGEBAR2")
	}
	// Nil side
	if _, err := AnchorsFromSide(nil); err == nil {
		t.Fatalf("expected error for nil SideDef")
	}
	// The first anchor in file order is equally mandatory [02 §6] C8.
	side3 := makeFullSide("ARM")
	delete(side3.Anchors, content.CanonicalKey("LOGO"))
	if _, err := AnchorsFromSide(side3); err == nil {
		t.Fatalf("expected error for missing LOGO")
	}
}

func TestAnchorsVerbatimNoNormalization(t *testing.T) {
	// Feed asymmetric corners where x2 < x1 and y2 < y1; assert byte-identical retention [02 §6] C8.
	side := makeFullSide("ARM")
	// Overwrite ENERGYBAR with inverted corners.
	inverted := content.Rect{X1: 100, Y1: 200, X2: 10, Y2: 20}
	side.Anchors[content.CanonicalKey("ENERGYBAR")] = inverted
	// Also make DAMAGEBAR inverted vertically.
	side.Anchors[content.CanonicalKey("DAMAGEBAR")] = content.Rect{X1: 5, Y1: 50, X2: 105, Y2: 10}

	a, err := AnchorsFromSide(side)
	if err != nil {
		t.Fatalf("AnchorsFromSide: %v", err)
	}
	energy, _ := a.ByName("ENERGYBAR")
	if energy.X1 != 100 || energy.Y1 != 200 || energy.X2 != 10 || energy.Y2 != 20 {
		t.Fatalf("verbatim retention failed for ENERGYBAR: got %+v want %+v", energy, inverted)
	}
	// Verify not normalized: width should be negative verbatim.
	if energy.Width() != -90 {
		t.Fatalf("Width verbatim expected -90, got %d", energy.Width())
	}
	if energy.Height() != -180 {
		t.Fatalf("Height verbatim expected -180, got %d", energy.Height())
	}
	dmg, _ := a.ByName("DAMAGEBAR")
	if dmg.X1 != 5 || dmg.Y1 != 50 || dmg.X2 != 105 || dmg.Y2 != 10 {
		t.Fatalf("verbatim retention failed for DAMAGEBAR: got %+v", dmg)
	}

	// Also test Anchors array direct index retains verbatim.
	if a[AnchorEnergyBar] != energy {
		t.Fatalf("index vs ByName mismatch")
	}
	// Case-insensitive lookup should still return verbatim.
	ci, ok := a.ByName("energybar")
	if !ok || ci != energy {
		t.Fatalf("case-insensitive ByName failed: %v %v", ci, ok)
	}
	ci2, ok := a.ByName("EnErGyBaR")
	if !ok || ci2 != energy {
		t.Fatalf("case-insensitive mixed case failed")
	}
}

func TestAnchorIndex(t *testing.T) {
	for i, name := range AnchorNames {
		idx, ok := AnchorIndex(name)
		if !ok {
			t.Fatalf("AnchorIndex %s not found", name)
		}
		if idx != i {
			t.Fatalf("AnchorIndex %s = %d want %d", name, idx, i)
		}
		// lower case
		idx2, ok := AnchorIndex(content.CanonicalKey(name))
		if !ok || idx2 != i {
			t.Fatalf("AnchorIndex lower %s = %d", name, idx2)
		}
	}
	if _, ok := AnchorIndex("NOT_AN_ANCHOR"); ok {
		t.Fatalf("expected not found for unknown anchor")
	}
}

func TestAnchorsOrderedHelper(t *testing.T) {
	r := Rect{X1: 100, Y1: 200, X2: 10, Y2: 20}
	left, top, right, bottom := r.Ordered()
	if left != 10 || right != 100 || top != 20 || bottom != 200 {
		t.Fatalf("Ordered failed: got %d %d %d %d", left, top, right, bottom)
	}
	// Normal rect unchanged
	r2 := Rect{X1: 10, Y1: 20, X2: 100, Y2: 200}
	l2, t2, r2v, b2 := r2.Ordered()
	if l2 != 10 || r2v != 100 || t2 != 20 || b2 != 200 {
		t.Fatalf("Ordered normal failed")
	}
}
