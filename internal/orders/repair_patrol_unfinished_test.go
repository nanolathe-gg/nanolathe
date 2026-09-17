package orders

import "testing"

// TestVTOLRepairPatrolUnfinishedTargetSpawnsHelpBuildDirectly locks the second
// half of [04 R-ORD-01 §7]'s step-4 sentence: "An unfinished `u` releases the
// payload, explicitly spawns `VTOL_HelpBuild` on `u` at the head, gate = 0, and
// returns *wait*."
//
// The arm is a different route from the complete-target one, which is what the
// stance rows prove. The issue helper for command code 8 refuses outright at
// stance 3 and inserts a return move beneath the assist at stances 0 and 1
// ([04 R-STANCE-01 §4]); the row's unfinished arm names its record itself, so
// none of that applies and the outcome is identical at every stance — one
// `VTOL_HelpBuild` at the head, no return move, and *wait*.
//
// The draw assertion is the I4 half: the bounded candidate pick is the only
// simulation value this visit spends, and the arm neither adds one nor falls
// through to the feature pairing's six [01 §7.5].
func TestVTOLRepairPatrolUnfinishedTargetSpawnsHelpBuildDirectly(t *testing.T) {
	// A fixed slice, not a map: the rows run in one order (I1).
	for _, row := range []struct {
		name  string
		move  uint32
		would string
	}{
		{"hold position", 0, "would insert a return move beneath the assist"},
		{"maneuver", 1, "would insert a return move beneath the assist"},
		{"roam", 2, "issues the assist alone"},
		{"stance 3", 3, "would refuse the code-8 issue outright"},
	} {
		t.Run(row.name, func(t *testing.T) {
			actor, candidates, sim, q := repairPatrolRefusalFixture(t, true)
			actor.Flags = actor.Flags&^(stanceFieldMask<<stanceMoveShift) | row.move<<stanceMoveShift
			for _, c := range candidates {
				c.Remaining = 1            // unfinished: a nanoframe under construction
				c.Health = c.Def.MaxDamage // and undamaged, so only `Remaining` selects the arm
			}
			released := 0
			q.binding.Movement.Release = func(*Node) bool { released++; return true }
			n := &Node{Owner: actor.Handle, Phase: 1}

			got := vtolRepairPatrolHandler(actor, n, 0, 100)

			if got != 3 {
				t.Fatalf("%s: unfinished target returned %d, want 3 (*wait*)", row.name, got)
			}
			if released != 1 {
				t.Fatalf("%s: payload released %d times, want 1 — the row's arm opens with the release", row.name, released)
			}
			if n.DynamicGate != 0 {
				t.Fatalf("%s: gate is %#x after the spawn, want 0 [04 R-ORD-01 §7]", row.name, n.DynamicGate)
			}
			if q.LenPrimary() != 1 {
				t.Fatalf("%s: inserted %d records, want 1; the row spawns the assist alone and the issue helper %s [04 R-STANCE-01 §4]",
					row.name, q.LenPrimary(), row.would)
			}
			head := q.Primary()[0]
			if head.ID != Lookup("VTOL_HelpBuild") {
				t.Fatalf("%s: head record is %q, want VTOL_HelpBuild", row.name, DescriptorFor(head.ID).Name)
			}
			if head.Target != candidates[0].Handle && head.Target != candidates[1].Handle {
				t.Fatalf("%s: spawned assist targets handle %d, want one of the two scanned candidates", row.name, head.Target)
			}
			if draws := sim.Draws(); draws != 1 {
				t.Fatalf("%s: unfinished arm drew %d simulation values, want 1 (the candidate pick alone) [01 §7.5][I4]", row.name, draws)
			}
		})
	}
}
