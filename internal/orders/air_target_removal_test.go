package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// AirStrike's overfly gate excludes target removal. The observer still clears
// its target, and movement arrival later reaches the null-target fallback;
// the earlier legs instead consume removal and keep the cached goal
// [04 R-AIR-01 §8][04 R-AIR-01 §16][04 §3.3].
func TestAirStrikeTargetRemovalAcrossMovementGates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gate       uint32
		stance     uint32
		wantSeek   bool
		wantOwnPos bool
	}{
		{"overfly fire at will", 0xE2, 2, true, true},
		{"overfly hold fire", 0xE2, 0, true, true},
		{"swing wide fire at will", 0x100E8, 2, true, false},
		{"swing wide hold fire", 0x100E8, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, actor, target := observerFixture(t)
			actor.X, actor.Y, actor.Z = 70<<16, 40<<16, 90<<16
			actor.Flags = actor.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | tc.stance<<units.StandingFireShift
			q := QueueForUnit(actor)
			q.SetBinding(&QueueBinding{Lookup: w.Unit})
			q.Push(Lookup("AirStrike"), Node{Owner: actor.Handle, Target: target.Handle, GoalX: 11 << 16, GoalY: 12 << 16, GoalZ: 13 << 16})
			n := q.Primary()[0]
			n.Phase, n.DynamicGate = 5, tc.gate
			// Leave the resulting seek available for inspection; its air runner
			// is outside this observer-to-order transition.
			q.SetOwnedHandler(Lookup("VTOL_SeekAttack"), func(*units.Unit, *Node, uint32, uint32) (Code, bool) { return 0, false })
			TargetRemoved(w, target.Handle)
			if n.Target != 0 || n.StaticGate&staticTargetObserver == 0 {
				t.Fatal("removal must clear the reference and retain issued-target metadata")
			}
			q.Pump(actor, 1)
			if tc.gate&pendTargetRemoved == 0 {
				if len(q.Primary()) == 0 || q.Primary()[0] != n {
					t.Fatal("removal outside the movement gate woke the attack")
				}
				n.Satisfied |= 0x20
				q.Pump(actor, 2)
			}
			seek := seekAtHead(q)
			if (seek != nil) != tc.wantSeek {
				t.Fatalf("seek spawned = %v, want %v", seek != nil, tc.wantSeek)
			}
			if seek == nil {
				return
			}
			wantX, wantY, wantZ := n.GoalX, n.GoalY, n.GoalZ
			if tc.wantOwnPos {
				wantX, wantY, wantZ = actor.X, actor.Y, actor.Z
			}
			if seek.Target != 0 || seek.GoalX != wantX || seek.GoalY != wantY || seek.GoalZ != wantZ {
				t.Fatalf("seek target/goal = %d/(%d,%d,%d), want 0/(%d,%d,%d)", seek.Target, seek.GoalX, seek.GoalY, seek.GoalZ, wantX, wantY, wantZ)
			}
		})
	}
}
