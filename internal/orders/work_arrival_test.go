package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Neither feature row consults build distance. Its height draw and work begin
// only after the rectangle outcome [04 R-PATH-01 §12][04 R-ORD-01 §5].
func TestFeatureWorkInsideBuildRangeWaitsForMovementOutcome(t *testing.T) {
	for _, name := range []string{"Reclaim", "Resurrect"} {
		for _, outcome := range []uint32{gateArrived, gateNoRoute} {
			t.Run(name+workArrivalOutcomeName(outcome), func(t *testing.T) {
				tree, _ := retailShapedTree()
				tree.Height = 20
				f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 9, 11)
				f.builder.Def.BuildDistance = 200
				f.builder.InBuildStance = false
				sim := rng.NewSimulation(7)
				f.q.Binding().SimRNG = &sim
				bx, bz := featureBoxCentre(9, 11, tree)
				if !inBuildRange(f.builder, bx, bz, tree.FootprintX, tree.FootprintZ) {
					t.Fatal("fixture must be within build range but away from the rectangle border")
				}
				f.q.Push(Lookup(name), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(9), GoalZ: world.CellToWorld(11), GoalSupplied: true})
				head := f.q.Head()
				before := sim
				for tick := uint32(1); tick <= 5; tick++ {
					f.q.Pump(f.builder, tick)
				}
				if head.Phase != 1 || head.DynamicGate != gateMoveOutcomes || len(f.rects) != 1 {
					t.Fatalf("approach = phase %d gate %#x rectangles %d", head.Phase, head.DynamicGate, len(f.rects))
				}
				if sim != before || head.Param1 != 0 || head.Flags&FlagStopBuildingPending != 0 || f.segments != 0 {
					t.Fatal("work effects ran before the movement outcome")
				}
				head.Satisfied |= outcome
				f.q.Pump(f.builder, 6)
				if outcome == gateNoRoute {
					if f.q.LenPrimary() != 0 || sim != before {
						t.Fatal("no route must abandon without a height draw")
					}
				} else if head.Phase != 2 || sim.Draws() != before.Draws()+1 {
					t.Fatalf("arrival = phase %d, draws %d; want stance wait after one height draw", head.Phase, sim.Draws()-before.Draws())
				}
				if _, _, _, found := features.FeatureAt(f.econ.Terrain, world.CellToWorld(9), world.CellToWorld(11)); !found {
					t.Fatal("approach removed the feature")
				}
				if buckets := f.econ.UnitBuckets(f.builder.Handle); buckets[economy.Metal].Production != 0 || buckets[economy.Energy].Production != 0 {
					t.Fatal("approach credited feature resources")
				}
			})
		}
	}
}

func workArrivalOutcomeName(outcome uint32) string {
	if outcome == gateArrived {
		return "/arrival"
	}
	return "/no-route"
}

// HelpBuild waits on its annulus; the air twin waits on its strict, unpadded
// build-distance marker [04 R-ORD-01 §5][04 R-ORD-01 §7].
func TestAssistInsideGroundReachWaitsForMovementOutcome(t *testing.T) {
	for _, air := range []bool{false, true} {
		for _, outcome := range []uint32{gateArrived, gateNoRoute} {
			name := "HelpBuild"
			if air {
				name = "VTOL_HelpBuild"
			}
			t.Run(name+workArrivalOutcomeName(outcome), func(t *testing.T) {
				var q *Queue
				var builder, target *units.Unit
				if air {
					q, builder, target = vtolWorkFixture()
				} else {
					q, builder, target = workFixture()
				}
				builder.Def.BuildDistance = 100
				target.X = builder.X + numeric.FixedFromInt(120)
				target.Remaining = 1
				if !inBuildRangeOf(builder, target) {
					t.Fatal("fixture must be inside ground padded reach")
				}
				calls := 0
				q.Binding().Work.Assist = func(*units.Unit, *Node, uint32) bool { calls++; return true }
				var marker AirGoalRequest
				q.Binding().Movement.InstallAir = func(req AirGoalRequest) bool { marker = req; return true }
				phase, waiting, gate := uint8(0), uint8(1), uint32(gateWorkApproach)
				if air {
					phase, waiting, gate = 1, 2, gateMoveOutcomes
				}
				q.Push(Lookup(name), Node{Owner: builder.Handle, Target: target.Handle, Phase: phase})
				head := q.Head()
				for tick := uint32(1); tick <= 5; tick++ {
					q.Pump(builder, tick)
				}
				if head.Phase != waiting || head.DynamicGate != gate || calls != 0 || head.Flags&FlagStopBuildingPending != 0 {
					t.Fatalf("approach = phase %d gate %#x work calls %d", head.Phase, head.DynamicGate, calls)
				}
				if air && (marker.Radius != 100 || marker.X != target.X || marker.Node != head) {
					t.Fatalf("air marker = %+v", marker)
				}
				head.Satisfied |= outcome
				q.Pump(builder, 6)
				if outcome == gateNoRoute {
					if q.LenPrimary() != 0 || calls != 0 {
						t.Fatal("no route must abandon without work")
					}
				} else if calls != 1 {
					t.Fatalf("arrival delivered %d work calls, want one", calls)
				}
			})
		}
	}
}
