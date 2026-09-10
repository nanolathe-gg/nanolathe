package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Repair patrol's issuer has different stance arms from combat: hold position
// still assists with a sight-distance leash, maneuver uses the unsigned leash,
// roam has no return, and stance 3 refuses [04 R-STANCE-01 §4].
func TestRepairPatrolIssuerRetainsReturnAction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		move      uint32
		sight     int32
		leash     int32
		wantIssue bool
		wantPost  bool
		wantLeash uint32
	}{
		{"hold position", 0, 96, 31, true, true, 96},
		{"signed sight distance", 0, 65534, 31, true, true, 0xfffffffe},
		{"maneuver", 1, 31, 65536 + 96, true, true, 96},
		{"roam", 2, 96, 96, true, false, 0},
		{"stance three", 3, 96, 96, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor, target := repairAdmissionPair(t, 50)
			actor.Def.SightDistance, actor.Def.ManeuverLeashLength = tc.sight, tc.leash
			actor.Flags = actor.Flags&^(stanceFieldMask<<stanceMoveShift) | tc.move<<stanceMoveShift
			actor.X, actor.Z = 70<<16|1234, -90<<16|4321
			q := QueueOfUnit(actor)
			q.binding.Lookup = func(h pool.Handle) *units.Unit {
				if h == target.Handle {
					return target
				}
				return nil
			}
			if got := spawnPatrolRepair(actor, target, 7); got != tc.wantIssue {
				t.Fatalf("issued = %v, want %v", got, tc.wantIssue)
			}
			if !tc.wantIssue {
				if q.LenPrimary() != 0 {
					t.Fatal("refused repair inserted an action")
				}
				return
			}
			assist := q.Primary()[0]
			if assist.ID != Lookup("RepairUnit") || assist.Target != target.Handle || assist.Param3 != tc.wantLeash || assist.CreationTick != 7 {
				t.Fatalf("repair identity/target/leash/tick differ: %+v", assist)
			}
			if !tc.wantPost {
				if hasSuccessor(actor, assist) || assist.GuardX != 0 || assist.GuardY != 0 {
					t.Fatal("roaming assist acquired a return move or anchor")
				}
				return
			}
			if !hasSuccessor(actor, assist) || assist.GuardX != 70 || assist.GuardY != -90 {
				t.Fatal("repair lost its return move or whole-position anchor")
			}
			post := q.Primary()[1]
			if post.ID != Lookup("Move_Ground") || post.Target != 0 || post.GoalX != actor.X || post.GoalY != actor.Y || post.GoalZ != actor.Z || post.Param3 != 0 || post.CreationTick != 7 {
				t.Fatal("return move did not retain the departure position and tick")
			}
			if tc.wantLeash == 96 {
				actor.X = 166 << 16
				resumed := false
				q.SetOwnedHandler(post.ID, func(_ *units.Unit, n *Node, _ uint32, _ uint32) (Code, bool) {
					resumed = n == post
					return 0, false
				})
				q.Pump(actor, 8)
				if !resumed || q.Primary()[0] != post {
					t.Fatal("repair leash completion did not resume its return action")
				}
			}
		})
	}
}
