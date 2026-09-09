package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestRectangleSelectionGatesOnEligibility locks [07 R-WGT-01 §10]'s statement
// of the drag walk: "for each record with `E(u)` true, apply the inclusive
// rectangle test... A record failing `E(u)` is neither written, toggled, nor
// counted, regardless of position."
//
// Before WU-19-45 the walk returned every VISIBLE unit inside the rectangle, so
// a drag across a battle line selected the enemy's units and half-built
// nanoframes with one's own. The three ineligible units below sit inside the
// rectangle and must not appear; the two eligible ones must, in ascending slot
// order.
func TestRectangleSelectionGatesOnEligibility(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	b := &battleSession{
		sess: &session.Session{LocalOwner: 0},
		cat:  cat,
		cam:  &camera.Camera{},
	}
	at := func(slot pool.Handle, owner uint8, mut func(*frame.UnitView)) frame.UnitView {
		v := frame.UnitView{
			Slot: slot, Owner: owner, DefName: "u",
			Flags: units.ClassifierEligibleStatus,
			X:     numeric.FixedFromInt(64), Y: 0, Z: numeric.FixedFromInt(64),
		}
		if mut != nil {
			mut(&v)
		}
		return v
	}
	f := &frame.Frame{Units: []frame.UnitView{
		at(1, 0, nil), // eligible
		at(2, 1, nil), // another player's
		at(3, 0, func(v *frame.UnitView) { v.BuildRemaining = 0.5 }), // nanoframe
		at(4, 0, func(v *frame.UnitView) { v.Flags = 0 }),            // status bit clear
		at(5, 0, nil), // eligible
	}}

	// A rectangle covering the whole surface, so membership is decided by the
	// eligibility filter alone.
	rect := client.Rect{MinX: -1 << 20, MinY: -1 << 20, MaxX: 1 << 20, MaxY: 1 << 20}
	got := b.eligibleHandlesInRect(f, rect)

	want := []pool.Handle{1, 5}
	if len(got) != len(want) {
		t.Fatalf("rectangle selection = %v, want %v [07 R-WGT-01 §10]", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rectangle selection = %v, want %v in ascending slot order [I1]", got, want)
		}
	}

	// An empty rectangle selects nothing rather than everything.
	if h := b.eligibleHandlesInRect(f, client.Rect{}); len(h) != 0 {
		t.Fatalf("an empty rectangle selected %v", h)
	}
}
