package orders_test

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestReclaimBeyondNanolatheRangeWalksAndPays is WU-19-5's end-to-end lock, and
// it is deliberately the whole loop rather than a handler assertion: the defect
// it guards was a placeholder in `Reclaim`'s phase 0 that held the record at
// the head forever while the mover was never asked to walk, so a right-click on
// a distant rock or wreck produced no motion, no removal and no metal.
//
// It drives the real order pump, the real path search and the real movement
// integration inside a composed skirmish, exactly as the mobile-builder
// approach parity test drives its own service. The assertions are
// relationships, not a census: the builder must close most of the gap, the
// feature must leave the grid, and the pools must reach the builder's
// production accumulators [04 R-ORD-01 §5][05 R-WORK-01 §5].
//
// Extended 2026-09-01 (WU-19-24) to a BLOCKING one-cell feature — every stock
// tree and rock. That case is the one RWU-19-12 was filed for: with the bare
// footprint as the goal rectangle its only admissible cell was the feature's
// own, which the searched layer holds as blocked, so the request published an
// empty route and the row abandoned on `0x40`. [04 R-PATH-01 §12] establishes
// that the goal class grows the rectangle by the reclaimer's own footprint, so
// a one-cell feature and a one-cell commander give an eight-cell ring of
// approach anchors and the feature's cell is interior. A blocking subtest that
// walks and pays is the proof; before the growth it could not pass.
func TestReclaimBeyondNanolatheRangeWalksAndPays(t *testing.T) {
	for _, tc := range []struct {
		name     string
		blocking bool
	}{
		{"non-blocking", false},
		{"blocking one cell", true},
	} {
		t.Run(tc.name, func(t *testing.T) { reclaimBeyondRangeWalksAndPays(t, tc.blocking) })
	}
}

func reclaimBeyondRangeWalksAndPays(t *testing.T, blocking bool) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets not mountable at %s: %v", root, err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSkirmish, Map: "Great Divide", LocalOwner: 0,
		SimulationSeed: 1, CRTSeed: 1, FS: fs,
	})
	if err != nil {
		t.Skipf("Great Divide unavailable: %v", err)
	}
	sess := composed.Session
	scaled := sess.Clock.ScaledAnchor
	step := func() {
		scaled += 5
		sess.Step(scaled)
	}
	for i := 0; i < 8; i++ {
		step()
	}

	// The local commander: a ground builder that can reclaim.
	var builder *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		if u.Owner == composed.LocalOwner && u.Def.BMCode != 0 && u.Def.CanReclamate && !u.Def.CanFly {
			builder = u
			break
		}
	}
	if builder == nil {
		t.Skip("the composed skirmish has no local ground reclaimer")
	}

	// A reclaimable feature well outside nanolathe range but close enough to
	// walk to inside the tick budget. Instances() is the feature service's own
	// stable anchor order, so the pick is deterministic (I1).
	//
	// The blocking leg additionally requires a ONE-CELL footprint: that is the
	// shape whose bare-footprint rectangle had a single admissible cell, and it
	// is the shape every stock tree and rock has [04 R-PATH-01 §12].
	reach := int64(builder.Def.BuildDistance)
	var (
		anchorX, anchorZ     int
		originalKey          string
		poolMetal, poolEnerg int32
		startGap             int64
	)
	found := false
	for _, inst := range sess.Features.Instances() {
		if inst == nil || inst.Def == nil || !inst.Def.Reclaimable || inst.Def.Blocking != blocking {
			continue
		}
		if blocking && (inst.Def.FootprintX != 1 || inst.Def.FootprintZ != 1) {
			continue
		}
		dx := int64(builder.X-inst.X) >> 16
		dz := int64(builder.Z-inst.Z) >> 16
		gap := dx*dx + dz*dz
		if gap <= (reach+96)*(reach+96) || gap > 900*900 {
			continue
		}
		// Away from the map edge, where the outermost cells are impassable and
		// the approach has nothing to do with this row.
		if int32(inst.CX) < 5 || int32(inst.CZ) < 5 || int32(inst.CX) > sess.World.CellW-6 || int32(inst.CZ) > sess.World.CellH-6 {
			continue
		}
		anchorX, anchorZ = inst.CX, inst.CZ
		originalKey = inst.Def.CanonicalKey
		poolMetal, poolEnerg = inst.Def.Metal, inst.Def.Energy
		startGap = gap
		found = true
		break
	}
	if !found {
		t.Skipf("no reclaimable feature with blocking=%v stands between nanolathe range and 900 world units of the commander", blocking)
	}
	if poolMetal == 0 && poolEnerg == 0 {
		t.Skip("the chosen feature authors no pools, so the credit is unobservable")
	}

	startX, startZ := builder.X, builder.Z
	q := orders.QueueForUnit(builder)
	q.Push(orders.Lookup("Reclaim"), orders.Node{
		Owner: builder.Handle,
		GoalX: world.CellToWorld(int32(anchorX)),
		GoalY: builder.Y,
		GoalZ: world.CellToWorld(int32(anchorZ)),
	})
	record := q.Primary()[0]
	lastPhase := record.Phase

	// The credit is a one-time addition to the builder's production
	// accumulators, which the settlement pass drains into the archived report,
	// so sample both every tick and keep the peak [05 R-WORK-01 §5]
	// [05 R-ECO-01 §2].
	peak := func(res economy.Res) float32 {
		live := sess.Econ.UnitBuckets(builder.Handle)
		got := float32(0)
		if live != nil {
			got = live[res].Production
		}
		if arch := sess.Econ.UnitArchived(builder.Handle)[res].Production; arch > got {
			got = arch
		}
		return got
	}
	peakMetal, peakEnergy := float32(0), float32(0)
	cleared := false
	for i := 0; i < 3000; i++ {
		step()
		if v := peak(economy.Metal); v > peakMetal {
			peakMetal = v
		}
		if v := peak(economy.Energy); v > peakEnergy {
			peakEnergy = v
		}
		// The transition is a REPLACEMENT, not a removal: every reclaimable
		// stock feature names `smudge01` as its `featurereclamate` successor,
		// so the test is that the ORIGINAL definition has left the anchor
		// [05 R-WORK-01 §5-A].
		if inst := sess.Features.InstanceAt(anchorX, anchorZ); inst == nil || inst.Def == nil || inst.Def.CanonicalKey != originalKey {
			cleared = true
		}
		if cleared {
			break
		}
		if !builder.Alive {
			t.Skip("the commander died before the reclaim finished")
		}
		if q.LenPrimary() > 0 && q.Primary()[0] == record {
			lastPhase = record.Phase
			continue
		}
		// The record is gone. A sprite feature that names a reclaim sequence
		// does not vanish on the payout visit: the transition attaches an
		// animation instance and the FEATURE phase drives it to completion
		// before the replacement stamps `smudge01` [05 R-FEAT-01 §5 step 5].
		// The credit already landed on the visit that started it [05
		// R-WORK-01 §5-A]. Keep stepping while that animation runs; only a
		// departed record over a feature that is neither cleared nor
		// animating means the approach never reached the footprint.
		if inst := sess.Features.InstanceAt(anchorX, anchorZ); inst != nil && inst.IsAnimating {
			continue
		}
		t.Fatalf("the reclaim record left the queue at phase %d after %d ticks without clearing the feature: the approach did not carry it to the footprint [04 R-ORD-01 §5]", lastPhase, i)
	}

	dx := int64(builder.X-startX) >> 16
	dz := int64(builder.Z-startZ) >> 16
	moved := dx*dx + dz*dz
	if moved < 100*100 {
		t.Fatalf("the builder moved %d world units squared toward a feature %d away: the approach never happened [04 R-ORD-01 §5]", moved, startGap)
	}
	if !cleared {
		t.Fatalf("%s is still standing at (%d,%d) after 3000 ticks; the builder moved %d units squared", originalKey, anchorX, anchorZ, moved)
	}
	// The credit is unconditional and lands whole, but a computer-player
	// discount would halve it [05 R-WORK-01 §5 step 4]; the local slot is a
	// human, so the whole pool is the bar.
	if poolMetal > 0 && peakMetal < float32(poolMetal) {
		t.Fatalf("metal production peaked at %v for a feature carrying %d: the payout did not reach the owner", peakMetal, poolMetal)
	}
	if poolEnerg > 0 && peakEnergy < float32(poolEnerg) {
		t.Fatalf("energy production peaked at %v for a feature carrying %d: the payout did not reach the owner", peakEnergy, poolEnerg)
	}

}
