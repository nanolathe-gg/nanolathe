package client

// The player's Enhanced effect switches (DESIGN_GPU_RENDERER §30). These are
// Nanolathe presentation rules, not retail behaviour: retail has none of these
// effects and the classic executor composes the same pixels either way.

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func withoutEffect(set func(*drawlist.Effects)) drawlist.Effects {
	e := drawlist.AllEffects()
	set(&e)
	return e
}

// A new client presents every effect; a changed selection retires the
// transient histories so a switch that was off leaves nothing stale behind.
func TestMarksSwitchGatesTrailHistory(t *testing.T) {
	c := trailScene(t)
	c.SetEnhanced(true)
	if c.Effects() != drawlist.AllEffects() {
		t.Fatalf("new client effects = %+v", c.Effects())
	}

	c.SetEffects(withoutEffect(func(e *drawlist.Effects) { e.Marks = false }))
	publishWalker(t, c, 1, 40)
	c.ObserveCommittedTick()
	publishWalker(t, c, 2, 65)
	c.ObserveCommittedTick()
	if len(c.trails.marks) != 0 {
		t.Fatalf("Marks off laid %d trail marks", len(c.trails.marks))
	}

	// Back on, the stride restarts from the current tick rather than replaying
	// the steps taken while the switch was off.
	c.SetEffects(drawlist.AllEffects())
	publishWalker(t, c, 3, 90)
	c.ObserveCommittedTick()
	publishWalker(t, c, 4, 115)
	c.ObserveCommittedTick()
	laid := len(c.trails.marks)
	if laid == 0 {
		t.Fatal("Marks on laid no trail marks")
	}

	// Turning it off retires the history instead of freezing it on screen.
	c.SetEffects(withoutEffect(func(e *drawlist.Effects) { e.Marks = false }))
	if len(c.trails.marks) != 0 {
		t.Fatalf("turning Marks off kept %d of %d marks", len(c.trails.marks), laid)
	}
}

// Water closes reflection admission and the authored water phase together, so
// no reflected geometry and no surface metadata reach the list at all.
func TestWaterSwitchClosesReflectionsAndSurface(t *testing.T) {
	c := reflectionScene(t)
	x, z := numeric.FixedFromInt(16), numeric.FixedFromInt(16)
	if !c.reflectionWaterAt(x, z) {
		t.Fatal("scene does not sit over reflective water")
	}
	if !c.waterSurfaceMetadata().Enabled {
		t.Fatal("scene records no water phase")
	}
	c.SetEffects(withoutEffect(func(e *drawlist.Effects) { e.Water = false }))
	if c.reflectionWaterAt(x, z) {
		t.Fatal("Water off still admitted a reflection site")
	}
	if c.waterSurfaceMetadata().Enabled {
		t.Fatal("Water off still recorded a water phase")
	}
}
