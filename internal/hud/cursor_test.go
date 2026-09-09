package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/units"
)

// unit builds a fixture unit owned by owner with the given definition. The
// status word carries the active-state bit `0x20`, because the allocator
// initializer sets it on every unit it creates [R-P0-04 "Runtime eligibility
// bit lifecycle"] and the inspect predicate of [07 §8] tests it.
func unit(owner uint8, def *content.UnitDef) *units.Unit {
	return &units.Unit{Owner: owner, Def: def, Alive: true, Health: 100, MaxHealth: 100,
		Flags: units.ClassifierEligibleStatus}
}

// TestInspectableGates locks the clauses of [07 §8]'s own-unit inspect
// predicate one at a time. [07 R-WGT-01 §10] corrects that section's "own,
// active, finished, UNTASKED unit": the predicate reads no order state, and
// what §8 called the empty-current-task field is the remaining-build fraction
// the "still building" case already covers. Both halves of SC16 are closed.
func TestInspectableGates(t *testing.T) {
	def := &content.UnitDef{UnitName: "ARMPW", CanMove: true}
	cases := []struct {
		name string
		mut  func(*units.Unit)
		want bool
	}{
		{"own active finished untasked", func(*units.Unit) {}, true},
		{"another player's unit", func(u *units.Unit) { u.Owner = 1 }, false},
		{"dead", func(u *units.Unit) { u.Alive = false }, false},
		{"still building", func(u *units.Unit) { u.Remaining = 0.5 }, false},
		{"active-state bit clear", func(u *units.Unit) { u.Flags &^= units.ClassifierEligibleStatus }, false},
		// There is no separate "untasked" gate to implement: [07 R-WGT-01 §10]
		// identifies §8's empty-current-task field as the remaining-build
		// fraction, which the "still building" row above already covers. An
		// idle unit holding its `defaultmissiontype` standing record is
		// inspectable, which is the case the retired order-guard word broke.
	}
	for _, tc := range cases {
		u := unit(0, def)
		tc.mut(u)
		if got := isInspectable(u, 0); got != tc.want {
			t.Errorf("%s: isInspectable = %v, want %v [07 §8][07 §9]", tc.name, got, tc.want)
		}
	}
	if isInspectable(nil, 0) {
		t.Errorf("nil target inspects [07 §8]")
	}
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
	hurtFrame := unit(0, soldier)
	hurtFrame.Health = 40
	hurtFrame.Remaining = 0.5

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
		// The default interface type never turns a click on a damaged COMPLETE
		// friendly into a repair [04 R-ORD-02 §1]; the click reaches the
		// own-unit reject, and the shape that reject shows is `cursorselect`
		// [07 §8][07 R-CAM-01 §14].
		{"idle builder over damaged complete friend inspects", input.LatchNormal, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Target: hurtFriend}, render.CursorSelect},
		{"idle builder over damaged UNFINISHED friend repairs", input.LatchNormal, sel(unit(0, builder)),
			CursorHover{OverWorld: true, Target: hurtFrame}, render.CursorRepair},
		// The MOVE latch is code 2, which does repair a damaged complete
		// friendly [04 R-ORD-02 §1] — the one row `needsWork` still serves.
		{"armed move over damaged complete friend repairs", input.LatchMove, sel(unit(0, builder)),
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

// TestTypeOneIdleCursorLocks the alternate idle column independently of the
// default action-shaped column. Armed rows retain their existing table
// regardless of Interface Type [07 R-CAM-01 §5][07 §8].
func TestTypeOneIdleCursorLocks(t *testing.T) {
	mover := &content.UnitDef{UnitName: "ARMPW", CanMove: true}
	reclaimer := &content.UnitDef{UnitName: "ARMCV", CanMove: true, CanReclamate: true}
	attacker := &content.UnitDef{UnitName: "CORAK", CanMove: true, CanAttack: true}
	actor := unit(0, mover)
	own := unit(0, mover)
	hostile := unit(1, attacker)
	other := unit(2, mover)
	wreck := &content.FeatureDef{Reclaimable: true}

	sel := func(u *units.Unit) CursorSelection {
		return CursorSelection{Viewer: 0, Units: []*units.Unit{u}, InterfaceType: InterfaceTypeRightClick,
			Hostile: func(_, target *units.Unit) bool { return target.Owner == 1 }}
	}
	cases := []struct {
		name  string
		sel   CursorSelection
		hover CursorHover
		want  int
	}{
		{"own eligible unit selects", sel(actor), CursorHover{OverWorld: true, Target: own}, render.CursorSelect},
		{"hostile is red without attack capability", sel(actor), CursorHover{OverWorld: true, Target: hostile}, render.CursorRed},
		{"other unit is green", sel(actor), CursorHover{OverWorld: true, Target: other}, render.CursorGrn},
		{"reclaimable feature is normal without capability", sel(actor), CursorHover{OverWorld: true, Feature: wreck}, render.CursorNormal},
		{"reclaimable feature is green with reclaim capability", sel(unit(0, reclaimer)), CursorHover{OverWorld: true, Feature: wreck}, render.CursorGrn},
		{"empty selection retains inspect fallback", CursorSelection{Viewer: 0, InterfaceType: InterfaceTypeRightClick}, CursorHover{OverWorld: true, Target: own}, render.CursorSelect},
	}
	for _, tc := range cases {
		if got := ChooseCursor(input.LatchNormal, tc.sel, tc.hover); got != tc.want {
			t.Errorf("%s: cursor = %d (%s), want %d (%s) [07 R-CAM-01 §5][07 §8]", tc.name, got, render.CursorName(got), tc.want, render.CursorName(tc.want))
		}
	}
	// Type 1 changes no armed row: the attack answer remains action-shaped.
	if got := ChooseCursor(input.LatchAttack, sel(actor), CursorHover{OverWorld: true, Target: hostile}); got != render.CursorNormal {
		t.Fatalf("armed attack without capability = %d, want cursornormal [07 R-CAM-01 §5]", got)
	}
	if got := ChooseCursor(input.LatchAttack, sel(unit(0, attacker)), CursorHover{OverWorld: true, Target: hostile}); got != render.CursorAttack {
		t.Fatalf("armed attack with capability = %d, want cursorattack [07 R-CAM-01 §5]", got)
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

// TestIdleUnitWithAStandingRecordIsInspectable is the case the retired
// order-guard word broke. [07 R-WGT-01 §10]: "a finished unit is eligible
// whatever its queue contains", and an idle stock mobile unit's queue holds the
// `Standby`-family record its `defaultmissiontype` authored [04 §3.3] — 122 of
// the reference install's 278 definitions author `Standby` alone. A predicate
// deriving eligibility from queue emptiness would gate `cursorselect` off for
// every one of them.
func TestIdleUnitWithAStandingRecordIsInspectable(t *testing.T) {
	def := &content.UnitDef{UnitName: "ARMPW", CanMove: true, DefaultMissionType: "Standby"}
	u := unit(0, def)
	if !isInspectable(u, 0) {
		t.Fatal("an idle own unit holding its standing record must be inspectable [07 R-WGT-01 §10]")
	}
}

// TestIdleLatchRepairsAnUnfinishedFriendly confirms the row [07 R-WGT-01 §10]
// places immediately before the inspect test on the empty-candidate path: a
// hovered friendly the repair predicate accepts and whose remaining-build
// fraction is nonzero yields `cursorrepair`, not `cursorselect`. It is the same
// word the inspect gate reads, in the opposite sense.
func TestIdleLatchRepairsAnUnfinishedFriendly(t *testing.T) {
	// A construction vehicle authors both `builder` and `canreclamate`; the
	// resolver's assistance admission reads the second [04 R-ORD-01 §7].
	builderDef := &content.UnitDef{UnitName: "ARMCV", CanMove: true, Builder: true, CanReclamate: true}
	builder := unit(0, builderDef)

	target := unit(0, &content.UnitDef{UnitName: "ARMSOLAR"})
	target.Remaining = 0.5 // a nanoframe: nonzero fraction

	sel := CursorSelection{Viewer: 0, Units: []*units.Unit{builder},
		Hostile: func(_, _ *units.Unit) bool { return false }}
	h := CursorHover{OverWorld: true, Target: target}
	if got := ChooseCursor(input.LatchNormal, sel, h); got != render.CursorRepair {
		t.Fatalf("a hovered nanoframe must give cursorrepair, got %v [07 R-WGT-01 §10]", got)
	}

	// Finished and undamaged, the same hover falls through to the inspect row.
	target.Remaining = 0
	if got := ChooseCursor(input.LatchNormal, sel, h); got != render.CursorSelect {
		t.Fatalf("a finished own unit must give cursorselect, got %v [07 §8]", got)
	}
}

// TestIdleCursorMatchesTheDefaultResolverRows is the cursor half of the
// contextual code's default variant [04 R-ORD-02 §1] code 1: the idle-latch
// shape must promise what the resolver would issue, which is [07 §8]'s "gated on
// the same authored capability flags the order predicate reads". Each row names
// the resolver step it mirrors.
func TestIdleCursorMatchesTheDefaultResolverRows(t *testing.T) {
	builderDef := &content.UnitDef{UnitName: "ARMCV", CanMove: true, Builder: true,
		CanReclamate: true, CanGuard: true}
	// A factory: the authored `builder` key without the `canreclamate` mirror
	// bit the assistance admission reads [04 R-ORD-01 §7].
	factoryDef := &content.UnitDef{UnitName: "ARMVP", CanMove: true, Builder: true}
	enemyDef := &content.UnitDef{UnitName: "CORAK", CanMove: true, CanAttack: true}

	builder := unit(0, builderDef)
	factory := unit(0, factoryDef)

	frame := unit(0, &content.UnitDef{UnitName: "ARMSOLAR"})
	frame.Remaining = 0.5
	damagedComplete := unit(0, &content.UnitDef{UnitName: "ARMSOLAR"})
	damagedComplete.Health = 40
	enemy := unit(1, enemyDef)

	sel := func(us ...*units.Unit) CursorSelection {
		return CursorSelection{Viewer: 0, Units: us,
			Hostile: func(_, target *units.Unit) bool { return target.Owner != 0 }}
	}
	cases := []struct {
		name  string
		sel   CursorSelection
		hover CursorHover
		want  int
	}{
		// Step 1: hostile and `canattack`.
		{"hostile unit, attacker", sel(unit(0, enemyDef)),
			CursorHover{OverWorld: true, Target: enemy}, render.CursorAttack},
		// Step 2: hostile and `canreclamate`, no attack capability.
		{"hostile unit, reclaimer", sel(builder),
			CursorHover{OverWorld: true, Target: enemy}, render.CursorReclamate},
		// Step 3 first half: an unfinished friendly is the only repair-shaped row.
		{"unfinished friendly, reclaimer", sel(builder),
			CursorHover{OverWorld: true, Target: frame}, render.CursorRepair},
		// ... and only for an actor carrying the flag the admission reads.
		{"unfinished friendly, factory without the mirror bit", sel(factory),
			CursorHover{OverWorld: true, Target: frame}, render.CursorMove},
		// Step 3 second half: the own-unit reject shows the select shape.
		{"damaged complete own unit", sel(builder),
			CursorHover{OverWorld: true, Target: damagedComplete}, render.CursorSelect},
		// Step 5 and step 6.
		{"reclaimable feature", sel(builder),
			CursorHover{OverWorld: true, Feature: &content.FeatureDef{Reclaimable: true}},
			render.CursorReclamate},
		{"open ground", sel(builder), CursorHover{OverWorld: true}, render.CursorMove},
	}
	for _, tc := range cases {
		if got := ChooseCursor(input.LatchNormal, tc.sel, tc.hover); got != tc.want {
			t.Errorf("%s: got %d (%s) want %d (%s) [07 §8][04 R-ORD-02 §1]",
				tc.name, got, render.CursorName(got), tc.want, render.CursorName(tc.want))
		}
	}
}
