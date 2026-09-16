package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These tests lock the requested modern spacing policy, not retail placement.
func TestResourceRepeatedSolarPacksWithoutOverlap(t *testing.T) {
	for _, publish := range []bool{false, true} {
		t.Run(fmt.Sprintf("publish=%v", publish), func(t *testing.T) {
			b, cl, ms, builder := resourceFixture(t, false)
			// Keep the work queued while ticks drain input and publish orders.
			// Otherwise a nanoframe under the pointer becomes a unit target.
			// A deadline gate holds the head until its tick [04 §3.3].
			orders.QueueForUnit(b.sess.Units.Unit(builder)).Push(orders.Lookup("Wait"), orders.Node{
				Owner: builder, DynamicGate: 1, Deadline: 1000,
			})
			x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(320), 0, numeric.FixedFromInt(320))
			var placed []session.HumanMobileBuildCommand
			for i := 0; i < 9; i++ {
				resourceClickAt(b, cl, x, y, true)
				ms.ms += 100
				resourceClickAt(b, cl, x, y, true)
				cmds := resourceBuildCommands(b.sess)
				want := i + 1
				if publish {
					want = 1
				}
				if len(cmds) != want {
					t.Fatalf("gesture %d queued %d builds, want %d", i, len(cmds), want)
				}
				c := cmds[len(cmds)-1]
				if !c.Queued || !c.AppendOnly || c.Product != "armsolar" {
					t.Fatalf("changed command intent: %+v", c)
				}
				touching := i == 0
				for _, earlier := range placed {
					dx, dz := numeric.Abs(int32((c.WX-earlier.WX)>>16)), numeric.Abs(int32((c.WZ-earlier.WZ)>>16))
					if dx < 32 && dz < 32 {
						t.Fatalf("2x2 solar footprints overlap: %+v and %+v", earlier, c)
					}
					if (dx == 32 && dz < 32) || (dz == 32 && dx < 32) {
						touching = true
					}
				}
				if !touching {
					t.Fatalf("new solar was not placed against an existing edge: %+v", c)
				}
				placed = append(placed, c)
				if publish {
					b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
				}
			}
			// Equal-distance edges consistently choose north before west/east/south.
			if placed[1].WX != placed[0].WX || placed[1].WZ != placed[0].WZ-numeric.FixedFromInt(32) {
				t.Fatalf("second solar did not choose the nearest edge: %+v", placed[:2])
			}
		})
	}
}

func TestResourceSpacingReservesUnselectedBuildersOnlyForLocalPlayer(t *testing.T) {
	for _, owner := range []uint8{0, 1} {
		t.Run(fmt.Sprint(owner), func(t *testing.T) {
			b, cl, ms, _ := resourceFixture(t, false)
			def, _ := b.cat.Unit("armcons")
			h, err := b.sess.Units.Create(def, owner, numeric.FixedFromInt(128), 0, numeric.FixedFromInt(128))
			if err != nil {
				t.Fatal(err)
			}
			b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
			node := orders.NewMobileBuildNode(b.cat, "advanced", numeric.FixedFromInt(328), numeric.FixedFromInt(328), 0, 1, 0, h, true)
			cur, _ := b.currentSnapshot()
			written := b.sess.Snapshot.BeginWrite()
			*written = *cur
			written.OrderQueues = []frame.OrderQueueView{{Unit: h, Secondary: []frame.OrderView{{
				DescriptorID: int32(node.ID), BuildProduct: "advanced", GoalX: numeric.FixedFromInt(328), GoalZ: numeric.FixedFromInt(328),
			}}}}
			if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
				t.Fatal(err)
			}
			x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(320), 0, numeric.FixedFromInt(320))
			resourceClickAt(b, cl, x, y, true)
			ms.ms += 100
			resourceClickAt(b, cl, x, y, true)
			cmds := resourceBuildCommands(b.sess)
			if len(cmds) != 1 {
				t.Fatalf("commands %+v", cmds)
			}
			cx, cz := world.PlacementAnchor(cmds[0].WX, cmds[0].WZ, 2, 2)
			if owner == 0 {
				// Queued 5x5 structure occupies cells [18,23) on both axes.
				if cx < 23 && cx+2 > 18 && cz < 23 && cz+2 > 18 {
					t.Fatalf("overlaps another local builder's larger footprint: %+v", cmds[0])
				}
			} else if cmds[0].WX != numeric.FixedFromInt(320) || cmds[0].WZ != numeric.FixedFromInt(320) {
				t.Fatal("foreign queue changed the local placement")
			}
		})
	}
}

func TestResourceMexSpacingStaysOnDeposit(t *testing.T) {
	for _, covered := range []bool{false, true} {
		t.Run(fmt.Sprintf("covered=%v", covered), func(t *testing.T) {
			b, cl, ms, builder := resourceFixture(t, true)
			// A small solar at one edge leaves some metal available. A centered
			// advanced extractor reserves the entire deposit and must refuse.
			product, center := "armsolar", int32(320)
			if covered {
				product, center = "advanced", 344
			}
			if err := b.DispatchMobileBuild(product, numeric.FixedFromInt(int64(center)), 0, numeric.FixedFromInt(int64(center)), true); err != nil {
				t.Fatal(err)
			}
			x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(366), 0, numeric.FixedFromInt(366))
			resourceClickAt(b, cl, x, y, true)
			ms.ms += 100
			resourceClickAt(b, cl, x, y, true)
			cmds := resourceBuildCommands(b.sess)
			if covered {
				if len(cmds) != 1 || b.resourceQueueFeedback != nil || b.resourceClick != nil {
					t.Fatal("fully reserved deposit should refuse without a new build or feedback")
				}
				return
			}
			if len(cmds) != 2 || cmds[1].Builder != builder || cmds[1].Product != "advanced" {
				t.Fatalf("commands %+v", cmds)
			}
			cx, cz := world.PlacementAnchor(cmds[1].WX, cmds[1].WZ, 5, 5)
			if cx < 21 && cx+5 > 19 && cz < 21 && cz+5 > 19 {
				t.Fatal("mex overlaps queued solar")
			}
			if cx >= 23 || cx+5 <= 20 || cz >= 23 || cz+5 <= 20 {
				t.Fatal("mex was moved off its deposit")
			}
		})
	}
}

func TestResourceSpacingSkipsIllegalEdge(t *testing.T) {
	b, cl, ms, _ := resourceFixture(t, false)
	if err := b.DispatchMobileBuild("armsolar", numeric.FixedFromInt(320), 0, numeric.FixedFromInt(320), true); err != nil {
		t.Fatal(err)
	}
	// The equally near north edge is void; west is the next legal edge.
	b.sess.World.PlotAt(19, 17).SetFeature(world.PlotFeatureVoid)
	x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(320), 0, numeric.FixedFromInt(320))
	resourceClickAt(b, cl, x, y, true)
	ms.ms += 100
	resourceClickAt(b, cl, x, y, true)
	cmds := resourceBuildCommands(b.sess)
	if len(cmds) != 2 || cmds[1].WX != numeric.FixedFromInt(288) || cmds[1].WZ != numeric.FixedFromInt(320) {
		t.Fatalf("did not skip the invalid north edge: %+v", cmds)
	}
}
