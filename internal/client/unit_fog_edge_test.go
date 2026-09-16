package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The unit list uses the hull visibility predicate [03 R-RAST-01 §7].
// A ground-anchor fog cell cannot veto a visible hull: projection shears
// height, and a large model can reach sight before its anchor [03 §3.2].
func TestUnitFogEdgeAdmitsVisibleHullAndThenCompositesFog(t *testing.T) {
	for _, raised := range []bool{false, true} {
		name := "footprint reaches sight before anchor"
		if raised {
			name = "height projects body ahead of ground anchor"
		}
		for _, bytes := range []bool{false, true} {
			source := "/word"
			if bytes {
				source = "/byte"
			}
			t.Run(name+source, func(t *testing.T) {
				c := newTestClient(t)
				c.models["edge"] = syntheticModel(
					[]pieceInfo{{name: "root", parent: -1}},
					[]syntheticTri{makeTriangle(0, "root", [3][3]float64{{-64, 0, 0}, {32, 0, 0}, {-64, 0, -96}}, 20, 0)}, 0,
				)
				u := frame.UnitView{Slot: 1, Owner: 1, Model: "edge", IsBuilding: true, MoverMode: 1,
					X: px(264), Z: px(160), HullOffsetX: px(-64), HullXExtent: px(96), HullZExtent: px(96)}
				if raised {
					u.Y, u.Z = px(128), px(224)
				}
				vis := frame.VisibilityView{Valid: true, W: 32, H: 32, CoverageBytes: bytes,
					Visible: make([]byte, 32*32), WordVisible: make([]uint16, 32*32)}
				fog := frame.FogView{Valid: true, W: 32, H: 32, Ch0: make([]byte, 32*32), Ch1: make([]byte, 32*32)}
				for z := 0; z < 32; z++ {
					for x := 0; x < 32; x++ {
						i := z*32 + x
						dark := x >= 8
						if raised {
							dark = z >= 7
						}
						if dark {
							fog.Ch0[i] = 15
						} else {
							vis.Visible[i], vis.WordVisible[i] = 1, 1
						}
					}
				}
				cur := &frame.Frame{ViewingPlayer: 0, Visibility: vis, Fog: fog, Units: []frame.UnitView{u}}
				if !SnapshotVisible(cur, u, 0) {
					t.Fatal("fixture hull must be in sight")
				}
				// A particle lands inside the visible part of the building. The
				// old anchor gate left this spray floating over an absent body.
				cur.Strips = []frame.StripView{parkedNanoView(px(210), px(170), 0xa3)}
				clearIndexed(c)
				c.drawWorldPass(cur, true)
				c.drawEffects(cur)
				c.drawWorldPassB(cur, true)
				c.replayForTest()
				if got := c.indexed[165*c.width+205]; got != 20 {
					t.Fatalf("visible building missing at sight edge: pixel %d, want 20", got)
				}
				if got := c.indexed[170*c.width+210]; got != 0xa3 {
					t.Fatalf("spray no longer paints above visible building: pixel %d", got)
				}
				if !strategicUnitVisible(cur, u, 0, nil) {
					t.Fatal("strategic icon disagrees with visible body")
				}
				// The fog overlay still obscures the part beyond the sight edge.
				// Cell rectangles start at their half-tile offset [03 §3.3].
				coveredX, coveredY := 275, 165
				if raised {
					coveredX, coveredY = 205, 242
				}
				if got := c.indexed[coveredY*c.width+coveredX]; got != 20 {
					t.Fatalf("fixture has no body beneath the fog: pixel %d", got)
				}
				c.drawFog(cur)
				c.replayForTest()
				if got := c.indexed[coveredY*c.width+coveredX]; got != 0 {
					t.Fatalf("unexplored fog did not cover the body: pixel %d", got)
				}
				if got := c.indexed[165*c.width+205]; got != 20 {
					t.Fatalf("fog erased the visible part of the body: pixel %d", got)
				}
				cur.Units[0].Cloaked = true
				c.selectionChrome = nil
				c.drawWorldPass(cur, true)
				if len(c.selectionChrome) != 0 || strategicUnitVisible(cur, cur.Units[0], 0, nil) {
					t.Fatal("cloaked enemy passed hull admission")
				}
				cur.Units[0].Cloaked = false
				// Losing sight must still remove the foreign unit, even though
				// the presentation fog snapshot has not changed.
				clear(cur.Visibility.Visible)
				clear(cur.Visibility.WordVisible)
				c.selectionChrome = nil
				c.drawWorldPass(cur, true)
				if len(c.selectionChrome) != 0 || strategicUnitVisible(cur, u, 0, nil) {
					t.Fatal("foreign unit survived loss of sight")
				}
			})
		}
	}
}
