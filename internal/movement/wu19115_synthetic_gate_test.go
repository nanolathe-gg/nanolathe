package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestSyntheticFallbackGateReadsTheRetiringFlag locks the polarity of the goal
// installer's third acceptance gate, [04 R-PATH-01 §8] step 5.3: the synthetic
// straight line runs only "if the unit has a current order record and that
// record's retiring flag is clear". [05 R-EGRESS-02] names the flag as the
// pump's code-9 completion flag, `orders.FlagRetryMark`.
//
// The polarity is the whole point of the gate, so it is asserted in both
// directions and for the no-record case. Reading it the other way round (or
// passing an unconditional true, which is what WU-19-107 found at the two
// install sites) hands a mover a fresh straight line at its goal on every one
// of the 30-59-tick re-arms of [04 R-ORDER-02 §1], which is the "never settle
// down, keep shifting around and playing sounds" report.
func TestSyntheticFallbackGateReadsTheRetiringFlag(t *testing.T) {
	cases := []struct {
		name string
		node *orders.Node
		want bool
	}{
		{"no current order record", nil, false},
		{"retiring flag clear", &orders.Node{}, true},
		{"retiring flag set", &orders.Node{Flags: orders.FlagRetryMark}, false},
		{"retiring flag set beside others", &orders.Node{Flags: orders.FlagRetryMark | orders.FlagTombstone}, false},
	}
	for _, c := range cases {
		if got := allowSyntheticFor(c.node); got != c.want {
			t.Errorf("%s: allowSyntheticFor = %v, want %v [04 R-PATH-01 §8 step 5.3][05 R-EGRESS-02]",
				c.name, got, c.want)
		}
	}
}

// TestRetiredRecordGetsNoSyntheticRoute is the same contract at the installer:
// a two-point route can never pass either point-count gate, so a record whose
// retiring flag is clear is rewritten with the straight line and a record whose
// flag is set is left with no active route at all — the follower then has fewer
// than two points, clears has-waypoint and brakes [04 R-MOV-01 §3], which is
// how a blocked mover "idles in place" [04 R-EGRESS-01].
func TestRetiredRecordGetsNoSyntheticRoute(t *testing.T) {
	unit := &units.Unit{X: 0, Z: 0}
	goal := path.PointGoal(path.Cell{X: 20, Z: 0}, 0)
	goalX := numeric.Fixed(320 << 16)

	live := &Route{}
	live.PublishAtRevision([]Point{{X: 0}, {X: 300}}, 7)
	installGroundGoal(live, unit, goal, goalX, 0, true, allowSyntheticFor(&orders.Node{}), 7, 1)
	if !live.Active || live.Count != 2 || live.Points[1] != (Point{X: 320}) {
		t.Fatalf("a record the pump has not retired keeps its straight line: route=%+v", live)
	}

	retired := &Route{}
	retired.PublishAtRevision([]Point{{X: 0}, {X: 300}}, 7)
	installGroundGoal(retired, unit, goal, goalX, 0, true,
		allowSyntheticFor(&orders.Node{Flags: orders.FlagRetryMark}), 7, 1)
	if retired.Active {
		t.Fatalf("a retired record must not be handed a synthetic straight line: route=%+v", retired)
	}
	if !retired.WantsRepath {
		t.Fatalf("the installer arms wants-repath whatever the gate decides [04 R-PATH-01 §8 step 5]: route=%+v", retired)
	}
}
