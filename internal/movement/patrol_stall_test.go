package movement_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestPatrollingSquadDoesNotFreezeWhileWalking locks the liveness half of the
// blocked-mover contract: a blocked ground mover jostles "for at most a
// throttle period per stage" [04 R-PATH-01 §14 item 2], where the throttle is
// the follower's 60 ticks [04 R-MOV-01 §7]. A mover that is stopped dead while
// its speed word is still nonzero — walk callbacks still firing, position not
// changing — for many multiples of that period is not jostling, it is stuck.
//
// The three ways this regressed, all found together and all invisible to a
// single-unit fixture because they need contention for the one global working
// set [04 R-PATH-01 §6]:
//
//   - The scheduler ended its whole call after one 100-pop slice instead of
//     spending the player's accumulator, so throughput was one slice a tick and
//     followers waited hundreds of ticks for a route.
//   - The candidate poll advanced its cursor past the request that removal had
//     just shifted down, skipping one waiting follower per poll.
//   - Re-accepting a held route left the route's static-obstacle revision at
//     the publication that produced it, so the static-replan gate re-armed
//     every tick and cancelled its own in-flight request forever.
//
// The thresholds are deliberately loose — this is a relationship assertion, not
// a census. Before the fixes this scenario reported a worst frozen run of 376
// ticks and 6469 frozen unit-ticks; after, 195 and 1816. The same staging on
// `ashap plateau` reported runs of 800-950 ticks that never ended at all,
// which is the static-revision livelock.
func TestPatrollingSquadDoesNotFreezeWhileWalking(t *testing.T) {
	root := testsupport.RetailRoot(t)
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail install: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cat, err := content.Compile(fs)
	if err != nil {
		t.Skipf("catalog compile: %v", err)
	}

	rng.SeedGlobal(100, 200)
	cfg := session.SkirmishConfig{MapName: "coast to coast", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0] = session.SkirmishPlayer{Nickname: "human", Controller: 0, Metal: 1000, Energy: 1000}
	cfg.Players[1] = session.SkirmishPlayer{Nickname: "cpu", Controller: 1, Side: 1, Color: 1, AllyGroup: 1, Metal: 1000, Energy: 1000}
	sess, err := session.NewSkirmishWithFS(fs, cat, cfg)
	if err != nil {
		t.Skipf("skirmish: %v", err)
	}
	now := sess.Clock.ScaledAnchor
	for i := 0; i < 10 && sess.State != session.StateBattle; i++ {
		now++
		sess.Step(now)
	}
	if sess.State != session.StateBattle {
		t.Fatalf("not in battle: %v", sess.State)
	}

	var mineX, mineZ, theirsX, theirsZ numeric.Fixed
	var haveMine, haveTheirs bool
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil || !u.Def.Commander {
			continue
		}
		if u.Owner == 0 && !haveMine {
			mineX, mineZ, haveMine = u.X, u.Z, true
		}
		if u.Owner == 1 && !haveTheirs {
			theirsX, theirsZ, haveTheirs = u.X, u.Z, true
		}
	}
	if !haveMine || !haveTheirs {
		t.Skip("both commanders are needed to stage the patrol")
	}
	kbot, okK := cat.Unit("ARMPW")
	solar, okS := cat.Unit("ARMSOLAR")
	if !okK || !okS {
		t.Skip("ARMPW/ARMSOLAR absent from this install")
	}

	cellW := numeric.Fixed(int64(world.CellToWorld(1)))
	spawn := func(def *content.UnitDef, owner uint8, x, z numeric.Fixed) pool.Handle {
		h, err := sess.Units.Create(def, owner, x, sess.World.HeightAt(x, z), z)
		if err != nil {
			return 0
		}
		if u := sess.Units.Unit(h); u != nil {
			sess.Movement.EnsureUnit(u)
		}
		return h
	}

	// A block of enemy buildings around the enemy commander: the base the
	// patrol legs have to thread, and the contention that makes several movers
	// want a route at once.
	for gz := int32(-4); gz <= 4; gz += 2 {
		for gx := int32(-4); gx <= 4; gx += 2 {
			if gx == 0 && gz == 0 {
				continue
			}
			spawn(solar, 1, theirsX+numeric.Fixed(int64(gx)*int64(cellW)), theirsZ+numeric.Fixed(int64(gz)*int64(cellW)))
		}
	}

	// Placement has to be legal or the test measures its own staging: a mover
	// spawned on ground its class cannot traverse never had a route to lose.
	probeH := spawn(kbot, 0, mineX+8*cellW, mineZ+8*cellW)
	if probeH == 0 {
		t.Skip("no room to stage the squad")
	}
	comCellX, comCellZ := world.WorldToCell(mineX), world.WorldToCell(mineZ)
	squad := []pool.Handle{}
	for r := int32(4); r <= 20 && len(squad) < 12; r++ {
		for dz := -r; dz <= r && len(squad) < 12; dz += 3 {
			for dx := -r; dx <= r && len(squad) < 12; dx += 3 {
				cx, cz := comCellX+dx, comCellZ+dz
				if !sess.Movement.IsGoalCellPassable(probeH, path.Cell{X: cx, Z: cz}) {
					continue
				}
				if sess.Movement.Grid != nil && sess.Movement.Grid.FootprintOccupied(movement.Cell{X: cx, Z: cz}, 3, 3, 0) {
					continue
				}
				if h := spawn(kbot, 0, world.CellToWorld(cx), world.CellToWorld(cz)); h != 0 {
					squad = append(squad, h)
				}
			}
		}
	}
	if len(squad) < 8 {
		t.Skipf("only %d squad members could be staged on legal ground", len(squad))
	}

	if err := sess.EnqueueHumanCommand(session.HumanCommand{
		Kind:      session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: squad},
	}); err != nil {
		t.Fatal(err)
	}
	// Code 9 is the patrol resolver [04 R-ORD-02 §1]; the four legs box the
	// enemy base so every lap crosses it.
	legs := [][2]numeric.Fixed{
		{theirsX - 6*cellW, theirsZ - 6*cellW},
		{theirsX + 6*cellW, theirsZ - 6*cellW},
		{theirsX + 6*cellW, theirsZ + 6*cellW},
		{theirsX - 6*cellW, theirsZ + 6*cellW},
	}
	for i, leg := range legs {
		if err := sess.EnqueueHumanCommand(session.HumanCommand{
			Kind: session.HumanOrder,
			Order: session.HumanOrderCommand{
				Handles:  squad,
				Code:     9,
				Position: orders.ResolvePos{X: leg[0], Y: sess.World.HeightAt(leg[0], leg[1]), Z: leg[1]},
				Queued:   i > 0,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	type sample struct {
		x, z     numeric.Fixed
		stillFor int
	}
	last := map[pool.Handle]sample{}
	longest, frozenTicks := 0, 0
	for i := 0; i < 4000; i++ {
		now++
		sess.Step(now)
		for _, h := range squad {
			u := sess.Units.Unit(h)
			if u == nil || !u.Alive {
				continue
			}
			prev := last[h]
			cur := sample{x: u.X, z: u.Z}
			if prev.x == u.X && prev.z == u.Z {
				cur.stillFor = prev.stillFor + 1
			}
			// A mover that has braked to a stop is idle, not stuck; only a
			// nonzero speed word means the walk script is still running
			// [04 §5.2].
			if u.Move.Speed == 0 {
				cur.stillFor = 0
			}
			last[h] = cur
			if cur.stillFor > longest {
				longest = cur.stillFor
			}
			// A stall is a run of at least a throttle period's worth of ticks
			// with no displacement at all; shorter pauses are ordinary turning
			// and braking.
			if cur.stillFor == 0 && prev.stillFor >= 30 {
				frozenTicks += prev.stillFor
			} else if i == 3999 && cur.stillFor >= 30 {
				frozenTicks += cur.stillFor
			}
		}
	}

	// Sanity: the squad has to have gone somewhere, or the thresholds below
	// pass for the wrong reason.
	moved := 0
	for _, h := range squad {
		if u := sess.Units.Unit(h); u != nil && u.Alive {
			if abs64(int64(u.X-mineX)>>16)+abs64(int64(u.Z-mineZ)>>16) > 400 {
				moved++
			}
		}
	}
	if moved < len(squad)/2 {
		t.Fatalf("only %d of %d patrolling kbots travelled past 400 world units; the scenario no longer exercises the follower", moved, len(squad))
	}

	t.Logf("%d patrolling kbots over 4000 ticks: longest frozen-while-walking run %d ticks, %d unit-ticks inside stalls of 30+ ticks",
		len(squad), longest, frozenTicks)
	if longest > 600 {
		t.Fatalf("a patrolling kbot stood still for %d consecutive ticks with a nonzero speed word: it is not jostling, it is stuck. A blocked mover re-arms a request every 60 ticks and jostles for at most a throttle period per stage [04 R-PATH-01 §14][04 R-MOV-01 §7]", longest)
	}
	if frozenTicks > 3200 {
		t.Fatalf("the squad spent %d unit-ticks inside 30-tick-plus stalls with a nonzero speed word over 4000 ticks; before the scheduler, poll and static-revision fixes this was 6469 and after it 1816", frozenTicks)
	}
}
