package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestDebugCaptureQueueIsDetached(t *testing.T) {
	q := &Queue{primary: []*Node{{Param1: 17, Phase: 2}}, secondary: []*Node{{Param2: 3}}, diagnostics: []string{"pending"}, lastPumpTick: 31, secondaryTick: 29}
	d := q.DebugSnapshot(4)
	q.primary[0].Param1 = 19
	q.secondary[0].Param2 = 8
	q.diagnostics[0] = "changed"
	if d.Queue.Primary[0].Param1 != 17 || d.Queue.Secondary[0].Param2 != 3 || d.Diagnostics[0] != "pending" || d.LastPumpTick != 31 || d.SecondaryTick != 29 {
		t.Fatal("queue snapshot aliases live state")
	}
}

func TestDebugDangerCaptureDoesNotAgeOrAliasState(t *testing.T) {
	q, u, attacker := dangerFixture(true)
	n := &Node{ID: Lookup("Move_Ground"), Owner: u.Handle, GoalX: numeric.FixedFromInt(80)}
	q.primary = []*Node{n}
	q.danger.response = n
	q.danger.resume = &Node{ID: Lookup("Patrol")}
	q.danger.contacts[0] = dangerContact{unit: attacker, handle: attacker.Handle, tick: 1, failedUntil: 11, x: 23, z: 37}
	q.danger.impacts[0] = dangerImpact{valid: true, sector: 2, tick: 1, x: 41, z: 43}
	q.danger.nextDecision = 99
	q.Binding().CurrentTick = func() uint32 { return 1000 }
	before, random := q.danger, *q.Binding().SimRNG
	d := q.DebugSnapshot(u.Handle)
	if !reflect.DeepEqual(q.danger, before) || *q.Binding().SimRNG != random {
		t.Fatal("capture changed danger state or RNG")
	}
	if !d.Danger.Contacts[0].IdentityCurrent || !d.Danger.Contacts[0].VisibleNow || !d.Danger.Impacts[0].Valid || d.Danger.Response.PrimaryIndex != 0 || d.Danger.Resume.PrimaryIndex != -1 {
		t.Fatalf("missing retained danger evidence: %+v", d.Danger)
	}
	n.GoalX++
	q.danger.contacts[0].x++
	q.danger.impacts[0].x++
	if d.Danger.Response.GoalX != numeric.FixedFromInt(80) || d.Danger.Contacts[0].X != 23 || d.Danger.Impacts[0].X != 41 {
		t.Fatal("capture aliases live danger state")
	}
	q.Binding().Lookup = func(h pool.Handle) *units.Unit {
		if h == u.Handle {
			return u
		}
		return &units.Unit{Handle: attacker.Handle}
	}
	q.Binding().DangerVisible = func(*units.Unit, *units.Unit) bool {
		t.Fatal("capture queried visibility for stale identity")
		return false
	}
	stale := q.DebugSnapshot(u.Handle).Danger.Contacts[0]
	if !stale.IdentityChecked || stale.IdentityCurrent || stale.VisibilityChecked {
		t.Fatalf("stale slot presented as current: %+v", stale)
	}
}
