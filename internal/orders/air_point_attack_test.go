package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Ground and feature attacks have a position, not a removed unit reference.
// The issued-against-unit bit alone selects the lost-target fallback
// [04 R-AIR-01 §8][04 R-AIR-01 §16][04 R-MOV-03 §7].
func TestAirPointAttackKeepsGoal(t *testing.T) {
	for _, name := range []string{"AirStrike", "AirToGround"} {
		t.Run(name, func(t *testing.T) {
			q, u := gateFixture()
			x, y, z := numeric.FixedFromInt(123), numeric.FixedFromInt(17), numeric.FixedFromInt(456)
			q.Push(Lookup(name), Node{Owner: u.Handle, GoalX: x, GoalY: y, GoalZ: z})
			n := q.Head()
			if n.Target != 0 || n.StaticGate&staticTargetObserver != 0 {
				t.Fatal("point attack acquired a unit observer")
			}
			before := *q.binding.SimRNG
			if code, done := airEntry(u, n, 0, airInterruptMask(n.ID)); done {
				t.Fatalf("point attack completed before its flight leg: code=%d", code)
			}
			if *q.binding.SimRNG != before {
				t.Fatal("point admission consumed RNG")
			}
			if n.GoalX != x || n.GoalY != y || n.GoalZ != z {
				t.Fatal("point attack lost its authored goal")
			}
		})
	}
}

// Only bomber and strafer entries track the unit's current position in the
// cached goal; hover and dogfight retain it at entry [04 R-AIR-01 §8].
func TestAirEntryGoalRefreshByExecutor(t *testing.T) {
	for _, name := range []string{"AirStrike", "AirToGround", "AirToGroundHover", "AirToAir"} {
		t.Run(name, func(t *testing.T) {
			q, u := gateFixture()
			q.binding.Lookup = func(pool.Handle) *units.Unit { return u }
			q.Push(Lookup(name), Node{Owner: u.Handle, Target: 7})
			n := q.Head()
			if _, done := airEntry(u, n, 0, airInterruptMask(n.ID)); done {
				t.Fatal("live target completed attack")
			}
			wantX, wantY, wantZ := numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0)
			if name == "AirStrike" || name == "AirToGround" {
				wantX, wantY, wantZ = u.X, u.Y, u.Z
			}
			if n.GoalX != wantX || n.GoalY != wantY || n.GoalZ != wantZ {
				t.Fatal("entry changed the wrong cached goal")
			}
		})
	}
}
