package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestLatchToCodeMapping(t *testing.T) {
	// Full latch→code mapping table [GAP T22][07 §9] C11.
	cases := []struct {
		latch input.Latch
		code  int
		name  string
	}{
		{input.LatchNormal, 1, "Normal"},
		{input.LatchMove, 2, "Move"},
		{input.LatchAttack, 3, "Attack"},
		{input.LatchBlast, 4, "Blast"},
		{input.LatchUnload, 5, "Unload"},
		{input.LatchPickup, 6, "Pickup"},
		{input.LatchFollow, 7, "Follow"},
		{input.LatchRepair, 8, "Repair"},
		{input.LatchPatrol, 9, "Patrol"},
		{input.LatchTeleport, 11, "Teleport"},
		{input.LatchReclaim, 12, "Reclaim"},
		{input.LatchCapture, 13, "Capture"},
		{input.LatchMobileBuild, 14, "MobileBuild"},
	}
	for _, tc := range cases {
		got := LatchToCode(tc.latch)
		if got != tc.code {
			t.Errorf("LatchToCode(%s %d) = %d want %d", tc.name, tc.latch, got, tc.code)
		}
		// Also verify via Dispatch path: LatchToCode underlies Dispatch switch key [GAP T22] C11.
		if !tc.latch.IsValid() {
			t.Errorf("latch %s should be valid", tc.name)
		}
		if got != int(tc.latch) {
			t.Errorf("latch byte should equal code for valid latch %s: latch %d code %d", tc.name, tc.latch, got)
		}
	}
	invalid := []input.Latch{0, 10, 0xA, 0x0F, 0xFF, 42}
	for _, l := range invalid {
		if got := LatchToCode(l); got != 0 {
			t.Errorf("LatchToCode invalid %d = %d want 0", l, got)
		}
		if l.IsValid() {
			t.Errorf("latch %d should be invalid", l)
		}
	}
}

func TestParseButtonLatchChain(t *testing.T) {
	// Button parse chain precedence: STOP → ATTACK → BLAST → DEFEND → REPAIR →
	// PATROL → RECLAIM → CAPTURE → UNLOAD → LOAD → MOVE [07 §9] C11.
	tests := []struct {
		name string
		gate uint32
		want input.Latch
	}{
		{"STOP", 1, input.LatchNormal},
		{"attack", 1, input.LatchAttack}, // case-insensitive
		{"BLAST", 1, input.LatchBlast},
		{"defend", 1, input.LatchFollow},
		{"Repair", 1, input.LatchRepair},
		{"patrol", 1, input.LatchPatrol},
		{"reclaim", 1, input.LatchReclaim},
		{"CAPTURE", 1, input.LatchCapture},
		{"UNLOAD", 1, input.LatchUnload},
		{"LOAD", 1, input.LatchPickup},
		{"pickup", 1, input.LatchPickup}, // LOAD/PICKUP alias [07 §9]
		{"move", 1, input.LatchMove},
		// Default when no predicate matches.
		{"BUILD_ARM", 1, input.LatchMove},
		{"", 1, input.LatchMove},
		// Gate zero forces Normal regardless of name [07 §9].
		{"ATTACK", 0, input.LatchNormal},
		{"MOVE", 0, input.LatchNormal},
		{"UNLOAD", 0, input.LatchNormal},
	}
	for _, tc := range tests {
		got := ParseButtonLatch(tc.name, tc.gate)
		if got != tc.want {
			t.Errorf("ParseButtonLatch(%q gate %d) = %d (%s) want %d (%s)", tc.name, tc.gate, got, got.String(), tc.want, tc.want.String())
		}
	}
}

func TestParseButtonLatchPrecedence(t *testing.T) {
	// A button satisfying multiple predicates picks per chain order [07 §9] C11.
	precedence := []struct {
		name string
		want input.Latch
		desc string
	}{
		{"STOPATTACKBLASTDEFENDREPAIRPATROLRECLAIMCAPTUREUNLOADLOADMOVE", input.LatchNormal, "STOP first"},
		{"ATTACKBLAST", input.LatchAttack, "ATTACK before BLAST"},
		{"ATTACKMOVE", input.LatchAttack, "ATTACK before MOVE"},
		{"BLASTMOVE", input.LatchBlast, "BLAST before MOVE"},
		{"DEFENDREPAIR", input.LatchFollow, "DEFEND before REPAIR"},
		{"REPAIRPATROL", input.LatchRepair, "REPAIR before PATROL"},
		{"RECLAIMCAPTURE", input.LatchReclaim, "RECLAIM before CAPTURE"},
		{"UNLOAD", input.LatchUnload, "UNLOAD before LOAD substring"},
		{"UNLOAD_LOAD", input.LatchUnload, "UNLOAD vs LOAD contains"},
		{"RECLAIM_UNLOAD", input.LatchReclaim, "RECLAIM before UNLOAD"},
		{"CAPTURE_UNLOAD", input.LatchCapture, "CAPTURE before UNLOAD"},
		{"PICKUP", input.LatchPickup, "PICKUP alias for LOAD"},
		{"LOADPICKUP", input.LatchPickup, "LOAD alias pickup"},
		{"MOVE", input.LatchMove, "default MOVE"},
	}
	for _, tc := range precedence {
		got := ParseButtonLatch(tc.name, 1)
		if got != tc.want {
			t.Errorf("ParseButtonLatch precedence %q (%s) = %s want %s", tc.name, tc.desc, got.String(), tc.want.String())
		}
	}
	// Extra: button containing both ATTACK and BLAST with earlier STOP should still pick STOP.
	if got := ParseButtonLatch("STOP_ATTACK_BLAST", 1); got != input.LatchNormal {
		t.Errorf("STOP should outrank ATTACK/BLAST: got %s", got.String())
	}
	// UNLOAD contains LOAD — must not be mistaken for LOAD/PICKUP.
	if got := ParseButtonLatch("UNLOAD", 1); got != input.LatchUnload {
		t.Errorf("UNLOAD substring test: got %s want Unload", got.String())
	}
	if got := ParseButtonLatch("LOAD", 1); got != input.LatchPickup {
		t.Errorf("LOAD -> Pickup: got %s", got.String())
	}
}

func TestDispatchToResolveIntegration(t *testing.T) {
	// Wire-through only: latch dispatch resolves to orders.Resolve codes 1..14 [GAP T22][04 §3.4] C11.
	orders.EnsureHandlers()

	// Stub defs.
	moverDef := &content.UnitDef{UnitName: "mover", CanMove: true, MaxDamage: 100}
	attackerDef := &content.UnitDef{UnitName: "attacker", CanMove: true, CanAttack: true, MaxDamage: 100}
	guardDef := &content.UnitDef{UnitName: "guard", CanGuard: true, MaxDamage: 100}
	builderDef := &content.UnitDef{UnitName: "builder", CanMove: true, Builder: true, MaxDamage: 100}
	repairDef := &content.UnitDef{UnitName: "repairer", Builder: true, MaxDamage: 100}
	patrolDef := &content.UnitDef{UnitName: "patroller", CanPatrol: true, MaxDamage: 100}
	reclaimDef := &content.UnitDef{UnitName: "reclaimer", CanReclamate: true, MaxDamage: 100}
	captureDef := &content.UnitDef{UnitName: "capturer", CanCapture: true, MaxDamage: 100}
	loadDef := &content.UnitDef{UnitName: "loader", CanLoad: true, MaxDamage: 100}
	dgunDef := &content.UnitDef{UnitName: "dgun", CanDGun: true, MaxDamage: 100}
	vaultDef := &content.UnitDef{UnitName: "unloadable", CanMove: false, MaxDamage: 100}

	actorMover := &units.Unit{Def: moverDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorMover.Handle = 1
	actorAttacker := &units.Unit{Def: attackerDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorAttacker.Handle = 1
	actorGuard := &units.Unit{Def: guardDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorGuard.Handle = 1
	actorBuilder := &units.Unit{Def: builderDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorBuilder.Handle = 1
	actorRepair := &units.Unit{Def: repairDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorRepair.Handle = 1
	actorPatrol := &units.Unit{Def: patrolDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorPatrol.Handle = 1
	actorReclaim := &units.Unit{Def: reclaimDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorReclaim.Handle = 1
	actorCapture := &units.Unit{Def: captureDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorCapture.Handle = 1
	actorLoad := &units.Unit{Def: loadDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorLoad.Handle = 1
	actorDGun := &units.Unit{Def: dgunDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	actorDGun.Handle = 1
	_ = vaultDef
	_ = actorGuard
	_ = actorBuilder
	_ = actorRepair
	_ = actorMover

	// Hostile and friendly targets.
	hostile := &units.Unit{Def: &content.UnitDef{UnitName: "target", CanMove: true, Side: "CORE", MaxDamage: 100}, Alive: true, MaxHealth: 100, Health: 100, Owner: 1}
	hostile.Handle = 2
	hostile.Def.Side = "CORE"
	actorAttacker.Def.Side = "ARM"
	friendly := &units.Unit{Def: &content.UnitDef{UnitName: "friend", CanMove: true, Side: "ARM", MaxDamage: 100}, Alive: true, MaxHealth: 100, Health: 50, Owner: 0}
	friendly.Handle = 3
	friendly.Def.Side = "ARM"
	carriable := &units.Unit{Def: &content.UnitDef{UnitName: "carriable", MaxDamage: 100}, Alive: true, MaxHealth: 100, Health: 100, Owner: 1}
	carriable.Handle = 4
	// carriable must not be CantBeTransported.

	cases := []struct {
		latch input.Latch
		actor *units.Unit
		targ  *units.Unit
		pos   *orders.ResolvePos
		want  string // expected order name; empty means expect 0
	}{
		{input.LatchMove, actorMover, nil, nil, "Move_Ground"},
		{input.LatchAttack, actorAttacker, hostile, nil, "Attack_Chase"},
		{input.LatchBlast, actorDGun, nil, nil, "AttackSpecial"},
		{input.LatchPickup, actorLoad, carriable, nil, "Ground_Pickup"},
		{input.LatchFollow, actorGuard, friendly, nil, "Follow_Ground"},
		{input.LatchRepair, actorBuilder, friendly, nil, "RepairUnit"},
		{input.LatchPatrol, actorPatrol, nil, nil, "QPatrol"},
		{input.LatchReclaim, actorReclaim, nil, &orders.ResolvePos{HasFeature: true, IsWreck: false}, "Reclaim"},
		{input.LatchCapture, actorCapture, hostile, nil, "Capture"},
		{input.LatchMobileBuild, actorBuilder, nil, nil, "MobileBuild"},
		// Invalid capability should yield 0.
		{input.LatchAttack, actorMover, hostile, nil, ""},
		{input.LatchCapture, actorMover, hostile, nil, ""},
		// Invalid latch.
		{input.Latch(0), actorMover, nil, nil, ""},
		{input.Latch(0xA), actorMover, nil, nil, ""},
	}
	for i, tc := range cases {
		gotID := Dispatch(tc.latch, tc.actor, tc.targ, tc.pos)
		var wantID orders.ID
		if tc.want != "" {
			wantID = orders.Lookup(tc.want)
			if wantID == 0 {
				t.Fatalf("case %d latch %s: Lookup(%q) returned 0 — test def wrong", i, tc.latch.String(), tc.want)
			}
		}
		if gotID != wantID {
			t.Errorf("case %d latch %s actor %s target %v want %q (%d) got %d (%q)", i, tc.latch.String(), tc.actor.Def.UnitName, tc.targ, tc.want, wantID, gotID, orders.DescriptorFor(gotID).Name)
		}
		// Cross-check via direct orders.Resolve code path.
		code := LatchToCode(tc.latch)
		direct := orders.Resolve(code, tc.actor, tc.targ, tc.pos)
		if direct != gotID {
			t.Errorf("case %d: Dispatch(%s) = %d but Resolve(%d) = %d", i, tc.latch.String(), gotID, code, direct)
		}
	}
}

func TestDispatchSelectionStableIteration(t *testing.T) {
	orders.EnsureHandlers()
	moverDef := &content.UnitDef{UnitName: "mover", CanMove: true, MaxDamage: 100}
	// Create selection out of order: handles 5, 2, 9.
	u5 := &units.Unit{Def: moverDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	u5.Handle = 5
	u2 := &units.Unit{Def: moverDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	u2.Handle = 2
	u9 := &units.Unit{Def: moverDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	u9.Handle = 9
	// Non-mover that should fail gate.
	nonMoverDef := &content.UnitDef{UnitName: "static", CanMove: false, MaxDamage: 100}
	u7 := &units.Unit{Def: nonMoverDef, Alive: true, MaxHealth: 100, Health: 100, Owner: 0}
	u7.Handle = 7

	selected := []*units.Unit{u5, u2, u9, u7}
	ids := DispatchSelection(input.LatchMove, selected, nil, nil)
	if len(ids) != 4 {
		t.Fatalf("DispatchSelection len = %d want 4", len(ids))
	}
	// Expected order sorted ascending handle: 2,5,7,9.
	// 2->Move_Ground, 5->Move_Ground, 7->fail (0), 9->Move_Ground
	wantName := []string{"Move_Ground", "Move_Ground", "", "Move_Ground"}
	// Sorted handles are 2,5,7,9 so indices align as above.
	for i, want := range wantName {
		var wantID orders.ID
		if want != "" {
			wantID = orders.Lookup(want)
		}
		if ids[i] != wantID {
			t.Errorf("DispatchSelection[%d] = %d (%q) want %d (%q)", i, ids[i], orders.DescriptorFor(ids[i]).Name, wantID, want)
		}
	}
	// Also verify empty selection.
	if got := DispatchSelection(input.LatchMove, nil, nil, nil); got != nil {
		t.Errorf("nil selection should return nil")
	}
	if got := DispatchSelection(input.LatchMove, []*units.Unit{}, nil, nil); got != nil {
		t.Errorf("empty selection should return nil")
	}
	// Invalid latch returns zeros but still sorted length.
	idsInvalid := DispatchSelection(input.Latch(0), selected, nil, nil)
	for i, id := range idsInvalid {
		if id != 0 {
			t.Errorf("invalid latch DispatchSelection[%d] = %d want 0", i, id)
		}
	}
}

func TestParseButtonLatchGateForcesNormal(t *testing.T) {
	// Gate zero forces normal regardless of parse chain [07 §9].
	names := []string{"ATTACK", "BLAST", "MOVE", "UNLOAD", "LOAD", "REPAIR", "STOP"}
	for _, n := range names {
		if got := ParseButtonLatch(n, 0); got != input.LatchNormal {
			t.Errorf("ParseButtonLatch(%q, 0) = %s want Normal", n, got.String())
		}
	}
}
