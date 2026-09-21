package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestArmedReclaimRow locks the armed RECLAIM row (latch 0xC) of [07 §8]
// against the retail contract, which reads no diplomacy row at all: the
// feature test first, then the unit test, and the unit test is the admission
// predicate `ReclaimUnit`'s phase 0 applies — the actor's `canreclamate`, the
// target's mover mode not airborne, and the target's definition WITHOUT
// `cancapture` [04 §3 `ReclaimUnit`][05 R-WORK-01 §4].
//
// The row used to read "a reclaimable feature OR a hostile target". Hovering
// one's own unit therefore showed the plain arrow while the click reclaimed it
// — the disagreement between the advertised and the performed action that §8
// says this table exists to prevent.
func TestArmedReclaimRow(t *testing.T) {
	reclaimerDef := &content.UnitDef{UnitName: "ARMCV", CanMove: true, Builder: true, CanReclamate: true}
	plainDef := &content.UnitDef{UnitName: "ARMPW", CanMove: true, CanAttack: true}
	buildingDef := &content.UnitDef{UnitName: "ARMSOLAR"}
	// A commander is the `cancapture` definition of stock content, and the
	// admission predicate reads that same key on the TARGET.
	commanderDef := &content.UnitDef{UnitName: "ARMCOM", CanMove: true, Builder: true, CanReclamate: true, CanCapture: true}
	flyerDef := &content.UnitDef{UnitName: "ARMATLAS", CanMove: true, CanFly: true}

	building := unit(0, buildingDef)
	building.Flags |= units.BuildingClassStatus
	nanoframe := unit(0, buildingDef)
	nanoframe.Flags |= units.BuildingClassStatus
	nanoframe.Remaining = 0.5
	ownSoldier := unit(0, plainDef)
	commander := unit(0, commanderDef)
	hostileSoldier := unit(1, plainDef)
	hostileCommander := unit(1, commanderDef)
	grounded := unit(1, flyerDef)
	grounded.Move.Mode = 1
	airborne := unit(1, flyerDef)
	airborne.Move.Mode = 2

	actor := unit(0, reclaimerDef)
	actor.Handle = 7
	// The selected actor itself under the pointer: the arm never evaluates a
	// selected unit as its own target [07 §8].
	self := unit(0, reclaimerDef)
	self.Handle = 7

	sel := CursorSelection{Viewer: 0, Units: []*units.Unit{actor},
		Hostile: func(_, target *units.Unit) bool { return target.Owner != 0 }}
	wreck := &content.FeatureDef{Reclaimable: true}
	rock := &content.FeatureDef{}

	cases := []struct {
		name  string
		hover CursorHover
		want  int
	}{
		{"own completed building", CursorHover{OverWorld: true, Target: building}, render.CursorReclamate},
		{"own nanoframe", CursorHover{OverWorld: true, Target: nanoframe}, render.CursorReclamate},
		{"own mobile unit", CursorHover{OverWorld: true, Target: ownSoldier}, render.CursorReclamate},
		{"hostile ground unit", CursorHover{OverWorld: true, Target: hostileSoldier}, render.CursorReclamate},
		{"own commander", CursorHover{OverWorld: true, Target: commander}, render.CursorNormal},
		{"hostile commander", CursorHover{OverWorld: true, Target: hostileCommander}, render.CursorNormal},
		{"landed aircraft", CursorHover{OverWorld: true, Target: grounded}, render.CursorReclamate},
		{"airborne aircraft", CursorHover{OverWorld: true, Target: airborne}, render.CursorNormal},
		{"the selected actor itself", CursorHover{OverWorld: true, Target: self}, render.CursorNormal},
		{"reclaimable feature", CursorHover{OverWorld: true, Feature: wreck}, render.CursorReclamate},
		{"plain feature", CursorHover{OverWorld: true, Feature: rock}, render.CursorNormal},
		{"bare ground", CursorHover{OverWorld: true}, render.CursorNormal},
	}
	for _, tc := range cases {
		if got := ChooseCursor(input.LatchReclaim, sel, tc.hover); got != tc.want {
			t.Errorf("armed reclaim over %s: got %d (%s) want %d (%s) [07 §8][04 §3 `ReclaimUnit`]",
				tc.name, got, render.CursorName(got), tc.want, render.CursorName(tc.want))
		}
	}

	// The row is silent for an actor without the capability, whichever test
	// would otherwise fire.
	plain := CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, plainDef)},
		Hostile: sel.Hostile}
	for _, h := range []CursorHover{
		{OverWorld: true, Feature: wreck},
		{OverWorld: true, Target: hostileSoldier},
	} {
		if got := ChooseCursor(input.LatchReclaim, plain, h); got != render.CursorNormal {
			t.Errorf("armed reclaim without canreclamate: got %d (%s), want cursornormal [07 §8]",
				got, render.CursorName(got))
		}
	}
}

// TestMoveRowReclaimUsesTheAdmissionPredicate locks the MOVE row's reclaim arm
// [07 §8]: hostility AND the same admission predicate, with the capture arm
// taking precedence for a `cancapture` actor. The row used to test the actor's
// `canreclamate` alone, so a move-click on a hostile commander advertised a
// strip the handler refuses.
func TestMoveRowReclaimUsesTheAdmissionPredicate(t *testing.T) {
	reclaimerDef := &content.UnitDef{UnitName: "ARMCV", CanMove: true, Builder: true, CanReclamate: true}
	captorDef := &content.UnitDef{UnitName: "ARMCOM", CanMove: true, Builder: true, CanReclamate: true, CanCapture: true}
	plainDef := &content.UnitDef{UnitName: "ARMPW", CanMove: true}
	flyerDef := &content.UnitDef{UnitName: "ARMATLAS", CanMove: true, CanFly: true}

	hostile := unit(1, plainDef)
	hostileCommander := unit(1, captorDef)
	hostileAirborne := unit(1, flyerDef)
	hostileAirborne.Move.Mode = 2

	sel := func(def *content.UnitDef) CursorSelection {
		return CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, def)},
			Hostile: func(_, target *units.Unit) bool { return target.Owner != 0 }}
	}
	cases := []struct {
		name   string
		actor  *content.UnitDef
		target *units.Unit
		want   int
	}{
		{"reclaimer over a hostile ground unit", reclaimerDef, hostile, render.CursorReclamate},
		{"reclaimer over a hostile commander", reclaimerDef, hostileCommander, render.CursorMove},
		{"reclaimer over a hostile airborne unit", reclaimerDef, hostileAirborne, render.CursorMove},
		// The capture arm sits ahead of the reclaim arm, so a `cancapture`
		// actor never shows the reclaim shape on this row.
		{"captor over a hostile ground unit", captorDef, hostile, render.CursorCapture},
	}
	for _, tc := range cases {
		h := CursorHover{OverWorld: true, Target: tc.target}
		if got := ChooseCursor(input.LatchMove, sel(tc.actor), h); got != tc.want {
			t.Errorf("%s: got %d (%s) want %d (%s) [07 §8]",
				tc.name, got, render.CursorName(got), tc.want, render.CursorName(tc.want))
		}
	}
}

// TestIdleReclaimRewriteAppliesTheAdmissionPredicate locks the idle Type-0
// rewrite [07 §8]: `canreclamate` plus hostility re-enters the table as the
// armed RECLAIM latch, so the admission predicate decides the shape there. An
// own unit never takes the rewrite at all — its idle shape is the inspect one.
func TestIdleReclaimRewriteAppliesTheAdmissionPredicate(t *testing.T) {
	reclaimerDef := &content.UnitDef{UnitName: "ARMCV", CanMove: true, Builder: true, CanReclamate: true}
	commanderDef := &content.UnitDef{UnitName: "ARMCOM", CanMove: true, CanCapture: true}
	plainDef := &content.UnitDef{UnitName: "ARMPW", CanMove: true}

	sel := CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, reclaimerDef)},
		Hostile: func(_, target *units.Unit) bool { return target.Owner != 0 }}
	cases := []struct {
		name   string
		target *units.Unit
		want   int
	}{
		{"hostile ground unit", unit(1, plainDef), render.CursorReclamate},
		// The rewrite is taken, the reclaim row refuses the target, and the
		// row's own fallback answers — not the contextual row's move.
		{"hostile commander", unit(1, commanderDef), render.CursorNormal},
		// Hostility is what gates the rewrite, so an own unit reaches the
		// contextual row's inspect test instead [07 §8].
		{"own finished unit", unit(0, plainDef), render.CursorSelect},
	}
	for _, tc := range cases {
		h := CursorHover{OverWorld: true, Target: tc.target}
		if got := ChooseCursor(input.LatchNormal, sel, h); got != tc.want {
			t.Errorf("idle over %s: got %d (%s) want %d (%s) [07 §8]",
				tc.name, got, render.CursorName(got), tc.want, render.CursorName(tc.want))
		}
	}
}
