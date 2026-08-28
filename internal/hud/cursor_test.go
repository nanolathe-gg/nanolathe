package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/units"
)

// unit builds a fixture unit owned by owner with the given definition.
func unit(owner uint8, def *content.UnitDef) *units.Unit {
	return &units.Unit{Owner: owner, Def: def, Alive: true, Health: 100, MaxHealth: 100}
}

// TestChooseCursorHoverTable locks the hover/armed shape table [07 §8] C12.
func TestChooseCursorHoverTable(t *testing.T) {
	builder := &content.UnitDef{UnitName: "ARMCV", CanMove: true, Builder: true,
		CanReclamate: true, CanPatrol: true, CanGuard: true}
	soldier := &content.UnitDef{UnitName: "ARMPW", CanMove: true, CanAttack: true,
		CanPatrol: true, CanGuard: true}
	bomber := &content.UnitDef{UnitName: "ARMTHUND", CanMove: true, CanAttack: true, CanFly: true,
		// The ID is non-zero: ID 0 is the record-0 inactive sentinel and would
		// not count as a weapon [02 §5 R-CONTENT-02].
		Weapon1Def: &content.WeaponDef{ID: 1, Dropped: true}}
	turret := &content.UnitDef{UnitName: "ARMLLT", CanAttack: true}
	wreck := &content.FeatureDef{Reclaimable: true}
	rock := &content.FeatureDef{}

	ownIdle := unit(0, soldier)
	enemy := unit(1, soldier)
	hurtFriend := unit(0, soldier)
	hurtFriend.Health = 40

	sel := func(us ...*units.Unit) CursorSelection {
		return CursorSelection{Viewer: 0, Units: us}
	}

	cases := []struct {
		name  string
		latch input.Latch
		sel   CursorSelection
		hover CursorHover
		want  int
	}{
		{"chrome forces idle", input.LatchAttack, sel(unit(0, soldier)),
			CursorHover{OverWorld: false, Target: enemy}, render.CursorNormal},
		{"empty selection over ground", input.LatchNormal, sel(),
			CursorHover{OverWorld: true}, render.CursorNormal},
		{"empty selection over own unit inspects", input.LatchNormal, sel(),
			CursorHover{OverWorld: true, Target: ownIdle}, render.CursorSelect},
		{"idle mover over ground", input.LatchNormal, sel(unit(0, soldier)),
			CursorHover{OverWorld: true}, render.CursorMove},
		{"idle over enemy attacks", input.LatchNormal, sel(unit(0, soldier)),
			CursorHover{OverWorld: true, Target: enemy}, render.CursorAttack},
		{"idle bomber over enemy shows airstrike", input.LatchNormal, sel(unit(0, bomber)),
			CursorHover{OverWorld: true, Target: enemy}, render.CursorAirstrike},
		{"idle builder over damaged friend repairs", input.LatchNormal, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Target: hurtFriend}, render.CursorRepair},
		{"idle builder over wreck reclaims", input.LatchNormal, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Feature: wreck}, render.CursorReclamate},
		{"idle builder over plain feature moves", input.LatchNormal, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Feature: rock}, render.CursorMove},
		{"idle immobile turret over ground stays idle", input.LatchNormal, sel(unit(0, turret)),
			CursorHover{OverWorld: true}, render.CursorNormal},
		{"armed patrol", input.LatchPatrol, sel(unit(0, soldier)),
			CursorHover{OverWorld: true}, render.CursorPatrol},
		{"armed patrol on a unit that cannot patrol", input.LatchPatrol, sel(unit(0, turret)),
			CursorHover{OverWorld: true}, render.CursorNormal},
		{"armed guard over ally", input.LatchFollow, sel(unit(0, soldier)),
			CursorHover{OverWorld: true, Target: hurtFriend}, render.CursorDefend},
		{"armed guard over enemy refuses", input.LatchFollow, sel(unit(0, soldier)),
			CursorHover{OverWorld: true, Target: enemy}, render.CursorNormal},
		{"armed capture without the capability", input.LatchCapture, sel(unit(0, soldier)),
			CursorHover{OverWorld: true, Target: enemy}, render.CursorNormal},
		{"armed teleport", input.LatchTeleport, sel(unit(0, soldier)),
			CursorHover{OverWorld: true}, render.CursorTeleport},
		{"placement valid", input.LatchMobileBuild, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Placing: true, PlacementValid: true}, render.CursorFindSite},
		{"placement invalid", input.LatchMobileBuild, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Placing: true}, render.CursorTooFar},
	}
	for _, tc := range cases {
		if got := ChooseCursor(tc.latch, tc.sel, tc.hover); got != tc.want {
			t.Errorf("%s: got %d (%s) want %d (%s) [07 §8]",
				tc.name, got, render.CursorName(got), tc.want, render.CursorName(tc.want))
		}
	}
}

// TestChooseCursorLowestIndexWins locks the reduction across a mixed selection
// [07 §8]: the index table's numbering is its shape priority order.
func TestChooseCursorLowestIndexWins(t *testing.T) {
	soldier := &content.UnitDef{UnitName: "ARMPW", CanMove: true, CanAttack: true}
	mover := &content.UnitDef{UnitName: "ARMFLASH", CanMove: true}
	enemy := unit(1, soldier)

	sel := CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, mover), unit(0, soldier)}}
	h := CursorHover{OverWorld: true, Target: enemy}
	// cursorattack (1) outranks cursormove (14) regardless of pool order.
	if got := ChooseCursor(input.LatchNormal, sel, h); got != render.CursorAttack {
		t.Fatalf("mixed selection got %d (%s) want cursorattack [07 §8]", got, render.CursorName(got))
	}
	sel.Units[0], sel.Units[1] = sel.Units[1], sel.Units[0]
	if got := ChooseCursor(input.LatchNormal, sel, h); got != render.CursorAttack {
		t.Fatalf("reversed order changed the answer: got %d [07 §8]", got)
	}
	// With no attacker in the selection the move shape survives.
	only := CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, mover)}}
	if got := ChooseCursor(input.LatchNormal, only, h); got != render.CursorMove {
		t.Fatalf("mover-only got %d (%s) want cursormove", got, render.CursorName(got))
	}
}

// TestCommandFireAffordability locks the BLAST shape gate [07 §8]: command fire
// shows cursortoofar when the stocks do not cover energypershot/metalpershot.
func TestCommandFireAffordability(t *testing.T) {
	// Non-zero IDs: ID 0 is the record-0 inactive sentinel and would not
	// count as a weapon [02 §5 R-CONTENT-02].
	dgun := &content.WeaponDef{ID: 2, CommandFire: true, EnergyPerShot: 500, MetalPerShot: 0}

	com := &content.UnitDef{UnitName: "ARMCOM", CanMove: true, CanAttack: true, CanDGun: true,
		Weapon1Def: &content.WeaponDef{ID: 1}, Weapon2Def: dgun}
	h := CursorHover{OverWorld: true}

	rich := CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, com)}, Energy: 900}
	if got := ChooseCursor(input.LatchBlast, rich, h); got != render.CursorAttack {
		t.Fatalf("affordable got %d (%s) want cursorattack [07 §8]", got, render.CursorName(got))
	}
	poor := CursorSelection{Viewer: 0, Units: []*units.Unit{unit(0, com)}, Energy: 100}
	if got := ChooseCursor(input.LatchBlast, poor, h); got != render.CursorTooFar {
		t.Fatalf("unaffordable got %d (%s) want cursortoofar [07 §8]", got, render.CursorName(got))
	}
}
