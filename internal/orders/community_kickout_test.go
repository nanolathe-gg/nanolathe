package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestCommunityBlockedSiteLimitRetainsStrictComparisonAndCadence(t *testing.T) {
	n := &Node{Deadline: -1, Param3: 20}
	text, code := MobileBuildBlockedVisitLimit(n, 100, 20)
	if text != "" || code != 2 || n.Param3 != 21 || n.Deadline != 130 {
		t.Fatalf("visit at limit = text %q code %d counter %d deadline %d", text, code, n.Param3, n.Deadline)
	}
	text, code = MobileBuildBlockedVisitLimit(n, 130, 20)
	if text != MobileBuildBlockedText || code != 8 || n.Param3 != 21 {
		t.Fatalf("visit above limit = text %q code %d counter %d", text, code, n.Param3)
	}
}

func TestKickoutRewriteResumesFreshBuildAndDropsPositionlessQueue(t *testing.T) {
	u := &units.Unit{Handle: 7, Def: &content.UnitDef{BMCode: 1, CanMove: true}, Alive: true}
	buildID, moveID := Lookup("MobileBuild"), Lookup("Move_Ground")
	build := &Node{ID: buildID, Owner: u.Handle, Phase: 4, Deadline: -1}
	tail := &Node{ID: Lookup("Patrol"), Owner: u.Handle, GoalX: numeric.FixedFromInt(80)}
	q := NewQueueWith([]*Node{build, tail}, nil)
	BindQueue(u, q)
	if !KickoutRewrite(u, numeric.FixedFromInt(32), 0, numeric.FixedFromInt(48), 9, false) {
		t.Fatal("fresh build rewrite refused")
	}
	got := q.Primary()
	if len(got) != 3 || got[0].ID != moveID || got[1] != build || got[2] != tail || build.Phase != 0 {
		t.Fatalf("fresh build queue = %#v phase=%d", got, build.Phase)
	}

	positionless := &Node{ID: Lookup("Stop"), Owner: u.Handle, Deadline: -1}
	dropped := &Node{ID: Lookup("Patrol"), Owner: u.Handle}
	q.SetPrimary([]*Node{positionless, dropped})
	if !KickoutRewrite(u, numeric.FixedFromInt(64), 0, numeric.FixedFromInt(80), 10, false) {
		t.Fatal("positionless rewrite refused")
	}
	got = q.Primary()
	if len(got) != 1 || got[0].ID != moveID || got[0].GoalX != numeric.FixedFromInt(64) {
		t.Fatalf("positionless rewrite retained queued work: %#v", got)
	}
}
