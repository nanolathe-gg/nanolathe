package movement_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestPatrollingGroundUnitsTravel locks the liveness half of [04 R-ORD-01 §0]
// "Established — whose pending word an installer's `0x80` lands in": a `Patrol`
// record chain must actually walk its legs.
//
// This is a relationship assertion, not a hash. The mechanism it guards is one
// bit reaching one wrong record: when a goal installer raised the release bit
// `0x80` on whichever OTHER record held the mover's payload, every patrol leg
// satisfied its own `0xE0` gate on the tick it was armed, and the whole army's
// largest displacement over a full mission run fell from ~1,700 world units to
// 20. Anything in that range separates the two behaviours by two orders of
// magnitude, so the thresholds below are deliberately loose.
func TestPatrollingGroundUnitsTravel(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets not mountable at %s: %v", root, err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind:           headless.ScenarioCampaign,
		Mission:        "camps/arm campaign.tdf:MISSION0",
		Difficulty:     1,
		LocalOwner:     -1,
		SimulationSeed: 7,
		CRTSeed:        7,
		FS:             fs,
	})
	if err != nil {
		t.Skipf("AC01 is not composable from this install: %v", err)
	}
	sess := composed.Session

	type point struct{ x, z numeric.Fixed }
	start := make(map[pool.Handle]point)
	patrolling := make(map[pool.Handle]bool)
	patrol := orders.Lookup("Patrol")
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		start[u.Handle] = point{u.X, u.Z}
	}

	scaled := sess.Clock.ScaledAnchor
	for i := 0; i < 150; i++ {
		scaled += 5
		sess.Step(scaled)
		// The mission's patrol orders are issued by its own script, so record
		// who has ever carried one rather than sampling a single tick.
		for _, u := range sess.Units.IterSliced() {
			if u == nil || !u.Alive || patrolling[u.Handle] {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			for _, rec := range q.Primary() {
				if rec != nil && rec.ID == patrol {
					patrolling[u.Handle] = true
					break
				}
			}
		}
	}

	travelled, furthest := 0, int64(0)
	patrolTravelled := 0
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		from, ok := start[u.Handle]
		if !ok {
			continue
		}
		d := abs64(int64(u.X-from.x)>>16) + abs64(int64(u.Z-from.z)>>16)
		if d > 200 {
			travelled++
			if patrolling[u.Handle] {
				patrolTravelled++
			}
		}
		if d > furthest {
			furthest = d
		}
	}
	if len(patrolling) == 0 {
		t.Fatalf("no unit carried a Patrol record in %d ticks; the scenario no longer exercises the chain", sess.Clock.GlobalTick)
	}
	if patrolTravelled == 0 {
		t.Fatalf("no patrolling unit travelled more than 200 world units in %d ticks (%d units patrolled, furthest displacement overall %d): patrol legs are retiring without motion", sess.Clock.GlobalTick, len(patrolling), furthest)
	}
	if travelled < 4 || furthest < 400 {
		t.Fatalf("ground movement stalled: %d units travelled past 200 world units, furthest %d, over %d ticks", travelled, furthest, sess.Clock.GlobalTick)
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
