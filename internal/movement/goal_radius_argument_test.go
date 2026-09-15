package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// nodeNamed builds a bare record of the named descriptor. The radius helper
// reads only the record's identity and its first parameter word, so nothing
// else has to be filled in.
func nodeNamed(t *testing.T, name string, param1 uint32) *orders.Node {
	t.Helper()
	id := orders.Lookup(name)
	if id == 0 {
		t.Fatalf("descriptor %q is not in the table", name)
	}
	return &orders.Node{ID: id, Param1: param1}
}

// TestGroundMoveRadiusFollowsArgumentWord locks the Move_Ground phase-0
// binding of [04 R-ORD-01 §4]: the point goal's radius reads the full signed
// 32-bit first parameter and adds 4 with 32-bit wraparound. VTOL_Move instead
// binds max(KamikazeDistance, 16), independently of the parameter.
func TestGroundMoveRadiusFollowsArgumentWord(t *testing.T) {
	def := &content.UnitDef{UnitName: "armflea"}

	for _, arg := range []uint32{0, 160, 1, 0x7FFF} {
		got := goalRadiusParamFor(def, nodeNamed(t, "Move_Ground", arg))
		want := int32(arg) + 4
		if got != want {
			t.Fatalf("Move_Ground radius for argument %d = %d, want %d [04 R-ORD-01 §4]", arg, got, want)
		}
	}

	// Ordinary interface point moves leave the parameter 0, which is the
	// familiar radius 4 and handle threshold floor(4/16)² = 0 — arrival on the
	// exact goal cell only.
	if got := goalRadiusParamFor(def, nodeNamed(t, "Move_Ground", 0)); got != 4 || ThresholdSqFromRadius(got) != 0 {
		t.Fatalf("argument 0 = radius %d threshold %d, want 4 and 0 [04 R-ORD-01 §4][R-P0-01]", got, ThresholdSqFromRadius(got))
	}
	// A wave gather forwards 160, so its arrival radius is strictly wider than
	// a plain move's and its threshold is no longer the exact goal cell.
	gather := goalRadiusParamFor(def, nodeNamed(t, "Move_Ground", 160))
	plain := goalRadiusParamFor(def, nodeNamed(t, "Move_Ground", 0))
	if gather != 164 || gather <= plain || ThresholdSqFromRadius(gather) <= ThresholdSqFromRadius(plain) {
		t.Fatalf("gather radius %d (threshold %d) must exceed plain radius %d (threshold %d) [08 R-AI-01 §19][04 R-ORD-01 §4]",
			gather, ThresholdSqFromRadius(gather), plain, ThresholdSqFromRadius(plain))
	}

	// The high half participates in both the value and its sign. Bit 15
	// alone does not make the full parameter negative.
	for _, tc := range []struct {
		argument uint32
		radius   int32
	}{
		{0xFF60, 65380},
		{0xFFFFFF60, -156},
		{0xDEAD0000, -559087612},
		{0x7FFFFFFF, -2147483645},
	} {
		if got := goalRadiusParamFor(def, nodeNamed(t, "Move_Ground", tc.argument)); got != tc.radius {
			t.Fatalf("argument %x = radius %d, want %d [04 R-ORD-01 §4]", tc.argument, got, tc.radius)
		}
	}

	// VTOL_Move takes no argument: it binds max(KamikazeDistance, 16).
	air := &content.UnitDef{UnitName: "armatlas", KamikazeDistance: 40}
	if got := goalRadiusParamFor(air, nodeNamed(t, "VTOL_Move", 160)); got != 40 {
		t.Fatalf("VTOL_Move radius = %d, want 40 (kamikaze distance, argument ignored) [R-P0-01]", got)
	}
	if got := goalRadiusParamFor(def, nodeNamed(t, "VTOL_Move", 160)); got != 16 {
		t.Fatalf("VTOL_Move floor = %d, want 16 [R-P0-01]", got)
	}
	// Every other member of the arrival-handle family keeps the radius-4
	// default; the argument word means something else on those records (a
	// catalog index on the work rows), so it must not reach the radius.
	if got := goalRadiusParamFor(def, nodeNamed(t, "HelpBuild", 160)); got != 4 {
		t.Fatalf("HelpBuild radius = %d, want 4 — the work rows' argument word is a catalog index [04 R-ORD-01 §5]", got)
	}
}

// TestPatrolFamilyGoalRadiiBySubstate locks the per-substate radii of
// [04 R-ORD-01 §4]: `Patrol` phase 1 binds its point goal with radius 0 and
// `RepairPatrol` phase 1 with radius 16, and the goal handle's threshold is
// floor(radius/16)² [R-P0-01 corrected].
//
// The relationship that regresses silently is the SEPARATION of the two rows.
// Before WU-19-85 the helper bound the ground default 4 for every member of the
// family, which left `RepairPatrol` on threshold 0 — the exact waypoint cell —
// where its row's radius 16 admits the ring one cell out as well. `Patrol`'s
// threshold agreed by accident, so only the repair row's arrival predicate
// actually moved; both are asserted because the accident is what hid the bug.
//
// The argument word is asserted not to reach either row: it is `Move_Ground`'s
// alone, and a patrol record's parameters carry other payloads.
func TestPatrolFamilyGoalRadiiBySubstate(t *testing.T) {
	def := &content.UnitDef{UnitName: "armflea"}

	for _, tc := range []struct {
		name      string
		radius    int32
		threshold int32
	}{
		{"Patrol", 0, 0},
		{"RepairPatrol", 16, 1},
	} {
		for _, arg := range []uint32{0, 160, 0xFF60} {
			got := goalRadiusParamFor(def, nodeNamed(t, tc.name, arg))
			if got != tc.radius {
				t.Fatalf("%s radius (argument %d) = %d, want %d [04 R-ORD-01 §4]", tc.name, arg, got, tc.radius)
			}
			if th := ThresholdSqFromRadius(got); th != tc.threshold {
				t.Fatalf("%s threshold = %d, want floor(%d/16)² = %d [R-P0-01 corrected]",
					tc.name, th, tc.radius, tc.threshold)
			}
		}
	}

	// The repair row's leg is the wider of the two: that ordering is the whole
	// behavioural content of the correction.
	patrol := goalRadiusParamFor(def, nodeNamed(t, "Patrol", 0))
	repair := goalRadiusParamFor(def, nodeNamed(t, "RepairPatrol", 0))
	if !(ThresholdSqFromRadius(repair) > ThresholdSqFromRadius(patrol)) {
		t.Fatalf("RepairPatrol threshold %d must exceed Patrol's %d [04 R-ORD-01 §4]",
			ThresholdSqFromRadius(repair), ThresholdSqFromRadius(patrol))
	}

	// The accessor is the single statement of both values, and it claims only
	// the two ground rows.
	if r, ok := orders.PatrolGoalRadius(nodeNamed(t, "Patrol", 0)); !ok || r != 0 {
		t.Fatalf("orders.PatrolGoalRadius(Patrol) = %d,%v, want 0,true [04 R-ORD-01 §4]", r, ok)
	}
	if r, ok := orders.PatrolGoalRadius(nodeNamed(t, "RepairPatrol", 0)); !ok || r != 16 {
		t.Fatalf("orders.PatrolGoalRadius(RepairPatrol) = %d,%v, want 16,true [04 R-ORD-01 §4]", r, ok)
	}
	for _, name := range []string{"Move_Ground", "VTOL_Patrol", "QPatrol"} {
		if _, ok := orders.PatrolGoalRadius(nodeNamed(t, name, 0)); ok {
			t.Fatalf("orders.PatrolGoalRadius claims %q; only the two ground patrol rows bind a ground point goal [04 R-ORD-01 §4][04 R-ORD-02 §2]", name)
		}
	}
}
