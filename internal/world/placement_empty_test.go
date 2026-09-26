package world

import (
	"errors"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Empty mobile loops accept after strict bounds and never sample an occupied
// cell, including when only one dimension is zero [04 R-P0-08-C].
func TestEmptyMobilePlacement(t *testing.T) {
	for _, pair := range [][2]int32{{0, 0}, {0, 2}, {2, 0}} {
		ext, err := NewFootprintExtent(pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		def := &content.UnitDef{BMCode: 1, FootprintX: pair[0], FootprintZ: pair[1]}
		rules, err := PlacementRulesForUnit(nil, def)
		if err != nil {
			t.Fatal(err)
		}
		if rules.ProfileResolved || rules.Domain != content.MobilityUnknown {
			t.Fatalf("empty footprint invented a profile: %+v", rules)
		}
		// No plot is needed: this path must not touch even the anchor cell.
		terrain := &Terrain{CellW: 8, CellH: 8}
		for _, origin := range [][2]int32{{0, 0}, {7 - pair[0], 7 - pair[1]}} {
			rect, err := NewFootprintRect(NewFootprintAnchor(origin[0], origin[1]), ext)
			if err != nil {
				t.Fatal(err)
			}
			q := PlacementQuery{Rect: rect, Mobile: true, Rules: rules}
			if _, err := terrain.CheckPlacement(q); err != nil || !terrain.PlacementLegal(q) {
				t.Fatalf("empty %v at %v rejected: %v", pair, origin, err)
			}
			q.Mobile = false
			if _, err := terrain.CheckPlacement(q); !errors.Is(err, ErrInvalidFootprint) {
				t.Fatalf("empty building accepted: %v", err)
			}
		}
		for _, origin := range [][2]int32{{-1, 0}, {0, -1}, {8 - pair[0], 0}, {0, 8 - pair[1]}} {
			rect, _ := NewFootprintRect(NewFootprintAnchor(origin[0], origin[1]), ext)
			if _, err := terrain.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err == nil {
				t.Fatalf("empty %v at out-of-bounds %v accepted", pair, origin)
			}
		}
	}
}

func TestEmptyFactorySnapRetainsAuthoredTransform(t *testing.T) {
	for _, pair := range [][2]int32{{0, 0}, {0, 2}, {2, 0}} {
		ext, _ := NewFootprintExtent(pair[0], pair[1])
		// One fixed unit below/at/above the half-cell transition on each axis.
		for _, delta := range []numeric.Fixed{-1, 0, 1} {
			x := numeric.Fixed(8<<16) + delta
			pos := NewModelWorldPosition(x, 12345, x)
			p, err := SnapFactoryPlacement(pos, ext)
			if err != nil {
				t.Fatal(err)
			}
			wantX := int32((int64(x) + (int64(8-pair[0]*8) << 16)) >> 20)
			wantZ := int32((int64(x) + (int64(8-pair[1]*8) << 16)) >> 20)
			if p.Anchor().CellX() != wantX || p.Anchor().CellZ() != wantZ || p.ModelPosition() != pos {
				t.Fatalf("empty %v delta %d: got %v; want %d,%d with original transform", pair, delta, p, wantX, wantZ)
			}
			if p.Rect().Contains(wantX, wantZ) {
				t.Fatal("empty rectangle contains a cell")
			}
		}
	}
}

// The loader selects a resolved class before copying the definition's size
// and terrain fields. FBI zero is not an empty-footprint override, and class
// zero is not a request to fall back to FBI [04 R-P0-08-C].
func TestEmptyMobileFootprintResolvesClassFirst(t *testing.T) {
	def := &content.UnitDef{BMCode: 1, MovementClass: "selected"}
	mc := &content.MovementClass{FootprintX: 2, FootprintZ: 3, MaxSlope: 17, MaxWaterDepth: 23}
	cat := &content.Catalog{Movement: map[string]*content.MovementClass{"selected": mc}}
	fx, fz := FootprintForUnit(cat, def)
	if fx != 2 || fz != 3 {
		t.Fatalf("FBI zeros overrode class: %dx%d", fx, fz)
	}
	rules, err := PlacementRulesForUnit(cat, def)
	if err != nil || !rules.ProfileResolved || rules.MaxSlope != 17 || rules.MaxWaterDepth != 23 {
		t.Fatalf("FBI zeros bypassed class limits: %+v, %v", rules, err)
	}
	// A class with one zero dimension similarly overrides nonzero FBI values.
	def.FootprintX, def.FootprintZ = 4, 5
	mc.FootprintX = 0
	fx, fz = FootprintForUnit(cat, def)
	if fx != 0 || fz != 3 {
		t.Fatalf("class empty pair fell back to FBI: %dx%d", fx, fz)
	}
	// An absent class keeps the complete FBI pair, including its empty axis.
	def.FootprintX = 0
	delete(cat.Movement, "selected")
	fx, fz = FootprintForUnit(cat, def)
	if fx != 0 || fz != 5 {
		t.Fatalf("unresolved class lost FBI pair: %dx%d", fx, fz)
	}
	if _, err = PlacementRulesForUnit(cat, def); err != nil {
		t.Fatalf("unresolved class refused empty FBI fallback: %v", err)
	}
}
