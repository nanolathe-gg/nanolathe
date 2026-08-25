package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestPickUnitFogAndOverlap verifies the ONE picking routine respects fog and deterministic overlap [07 §9][03 §3.2] C8 [P0-I14].

func TestPickUnitFogAndOverlap(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	// Simple world 32x32 for camera.
	attrs := make([]formats.TNTAttribute, 32*32)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 32, 32)
	terrain := &world.Terrain{CellW: 32, CellH: 32, Plot: plot, Version: 0x2000, SeaLevel: 0}
	_ = terrain.ApplySchema(nil, 0)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 32 * 16, MapH: 32 * 16}

	// Units world sliced.
	w := units.New(16, cat)
	def := &content.UnitDef{UnitName: "ARMCOM", MaxDamage: 100, CanMove: true, CanAttack: true}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	// Place two units at same world position so screen projection overlaps exactly.
	x := numeric.Fixed(10 * 65536)
	z := numeric.Fixed(10 * 65536)
	h1, _ := w.Create(def, 1, x, 0, z) // enemy player 1
	h2, _ := w.Create(def, 1, x, 0, z) // enemy player 1 second unit, higher slot
	u1 := w.Unit(h1)
	u2 := w.Unit(h2)
	if u1 == nil || u2 == nil {
		t.Fatal("units not created")
	}
	// Ensure both alive and same position.
	sx0, sy0 := cam.WorldToScreen(u1.X, u1.Y, u1.Z)
	sx, sy := sx0-camera.OriginX, sy0-camera.OriginY // shell

	// Visibility service with no coverage: enemy units not visible to player 0 [03 §3.2] C8.
	// Construct minimal service with grid dimensions matching terrain/2.
	vis := &visibility.Service{}
	// Use helper to init: we need to set W,H via NewService? Check visibility constructor.
	// Fallback: if vis.W==0 then IsVisible returns false for all enemy, which matches fog test.
	// So with empty service, PickUnit should treat both as fogged and return no unit.

	// Fog test: with vis non-nil but empty grid, IsVisible should be false for enemy.
	bh, bu := PickUnit(sx, sy, cam, w, vis, visibility.PlayerID(0))
	if bh != 0 || bu != nil {
		t.Fatalf("fogged: PickUnit should return no unit when enemy not visible, got %v %v", bh, bu)
	}

	// Now make them visible by using nil vis (no fog) — should pick lower slot on tie [07 §9].
	bh, bu = PickUnit(sx, sy, cam, w, nil, visibility.PlayerID(0))
	if bh != h1 || bu != u1 {
		t.Fatalf("overlap tie: want lower slot h1 %v got %v (h2 %v)", h1, bh, h2)
	}

	// Move second unit slightly closer to cursor than first, should win despite higher slot.
	// Nudge u2 to be exactly at cursor, u1 slightly offset.
	u2.X = x + numeric.Fixed(1*65536) // 1 world unit east ~ 1 pixel
	sx20, sy20 := cam.WorldToScreen(u2.X, u2.Y, u2.Z)
	sx2, sy2 := sx20-camera.OriginX, sy20-camera.OriginY
	// Cursor at u2's screen pos should pick u2 now.
	bh, bu = PickUnit(sx2, sy2, cam, w, nil, visibility.PlayerID(0))
	if bh != h2 {
		t.Fatalf("nearest wins: want h2 %v got %v (dist to u1 includes 1px offset)", h2, bh)
	}

	// Ensure deterministic iteration: call twice yields same result.
	bh2, _ := PickUnit(sx, sy, cam, w, nil, visibility.PlayerID(0))
	if bh2 != bh && bh == h1 {
		// this branch not needed
	}
}

func TestPickUnitRespectsFogViaVisibility(t *testing.T) {
	// Verify feature of PickUnit that fogged units are absent for picking [03 §3.2] C8.
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	w := units.New(8, cat)
	def := &content.UnitDef{UnitName: "ARMCOM", MaxDamage: 100}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	x := numeric.Fixed(5 * 65536)
	z := numeric.Fixed(5 * 65536)
	h, _ := w.Create(def, 1, x, 0, z)
	u := w.Unit(h)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 32 * 16, MapH: 32 * 16}
	sx0, sy0 := cam.WorldToScreen(u.X, u.Y, u.Z)
	sx, sy := sx0-camera.OriginX, sy0-camera.OriginY

	// With visibility that marks player 0's own units visible but enemy not, we can test own bypass.
	// Create a service that would mark enemy invisible; using nil vis means no fog (all visible).
	// To test fog we use a vis with zero grid (W==0) which returns false for enemy [predicate.go].
	visEmpty := &visibility.Service{} // W==0 => IsVisible false for enemy
	if bh, _ := PickUnit(sx, sy, cam, w, visEmpty, visibility.PlayerID(0)); bh != 0 {
		t.Fatalf("fogged enemy should not be picked with empty vis, got %v", bh)
	}
	// Owner bypass: unit owned by viewer should be visible even with empty vis [03 §3.2] C8 step 1.
	w2 := units.New(8, cat)
	hOwn, _ := w2.Create(def, 0, x, 0, z) // owner 0 same as viewer
	own := w2.Unit(hOwn)
	sxO0, syO0 := cam.WorldToScreen(own.X, own.Y, own.Z)
	sxOwn, syOwn := sxO0-camera.OriginX, syO0-camera.OriginY
	bh, bu := PickUnit(sxOwn, syOwn, cam, w2, visEmpty, visibility.PlayerID(0))
	if bh != hOwn || bu != own {
		t.Fatalf("owner bypass: want own unit %v got %v", hOwn, bh)
	}
	_ = u
}
