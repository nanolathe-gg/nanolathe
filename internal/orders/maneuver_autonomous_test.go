package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The maneuver issuer saves a return move beneath its attack, with a narrowed
// leash and whole-position anchor. The forced guard join bypasses both the
// stance refusal and the two-record form [04 R-STANCE-01 §3][04 R-STANCE-01 §4].
func TestAutonomousManeuverRetainsReturnAction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		move      uint32
		fire      uint32
		force     bool
		wantIssue bool
		wantPost  bool
	}{
		{"maneuver fire at will", 1, 2, false, true, true},
		{"maneuver return fire", 1, 1, false, true, true},
		{"roam", 2, 2, false, true, false},
		{"hold position", 0, 2, false, false, false},
		{"hold fire", 1, 0, false, false, false},
		{"forced maneuver guard", 1, 2, true, true, false},
		{"forced hold guard", 0, 0, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, actor := standingFixture(&content.UnitDef{BMCode: 1, CanMove: true, CanAttack: true, ManeuverLeashLength: 65536 + 96})
			actor.X, actor.Y, actor.Z = 70<<16|1234, 40<<16, -90<<16|4321
			actor.Flags = units.ArmedStatus | tc.move<<stanceMoveShift | tc.fire<<stanceFireShift
			target := &units.Unit{Handle: 2, Alive: true, Def: &content.UnitDef{}, X: 120 << 16, Z: 90 << 16}
			q.binding.Lookup = func(h pool.Handle) *units.Unit {
				if h == target.Handle {
					return target
				}
				return nil
			}
			if got := autoEngage(actor, target, tc.force); got != tc.wantIssue {
				t.Fatalf("issued = %v, want %v", got, tc.wantIssue)
			}
			if !tc.wantIssue {
				if q.LenPrimary() != 0 {
					t.Fatal("refused engagement inserted an action")
				}
				return
			}
			attack := q.Primary()[0]
			if attack.ID != Lookup("Attack_Chase") || attack.Target != target.Handle {
				t.Fatal("engagement did not put the target attack first")
			}
			if !tc.wantPost {
				if hasSuccessor(actor, attack) || attack.Param3 != 0 || attack.GuardX != 0 || attack.GuardY != 0 {
					t.Fatal("single unlimited attack acquired a return move or leash")
				}
				return
			}
			if !hasSuccessor(actor, attack) || attack.Param3 != 96 || attack.GuardX != 70 || attack.GuardY != -90 {
				t.Fatalf("maneuver lost its return action or narrowed leash/anchor: %+v", attack)
			}
			post := q.Primary()[1]
			if post.ID != Lookup("Move_Ground") || post.Target != 0 || post.GoalX != actor.X || post.GoalY != actor.Y || post.GoalZ != actor.Z || post.Param3 != 0 {
				t.Fatal("return move did not retain the exact departure position")
			}
			// The attack is exactly at its inclusive leash boundary. Its ordinary
			// completion exposes the return move during the same pump cascade.
			actor.X = 166 << 16
			resumed := false
			q.SetOwnedHandler(post.ID, func(_ *units.Unit, n *Node, _ uint32, _ uint32) (Code, bool) {
				resumed = n == post
				return 0, false
			})
			q.Pump(actor, 1)
			if !resumed || q.Primary()[0] != post {
				t.Fatal("leash completion did not resume the saved return action")
			}
		})
	}
}
