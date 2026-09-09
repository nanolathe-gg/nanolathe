package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A particle's mark is two by two, because retail's rectangle filler is
// inclusive on both edges [03 §5.5]. At one pixel the spray reads as a dotted
// line, which is the defect this locks against.
func TestNanolatheParticleMarkIsTwoByTwo(t *testing.T) {
	c := &Client{width: 64, height: 64, indexed: make([]uint8, 64*64), cam: &camera.Camera{ViewW: 64, ViewH: 64}}
	vis := frame.VisibilityView{Valid: true, W: 8, H: 8, CoverageBytes: true, Visible: make([]byte, 64)}
	for i := range vis.Visible {
		vis.Visible[i] = 1
	}
	c.drawStripBarrier(&frame.Frame{Tick: 1, Visibility: vis, Strips: []frame.StripView{{
		Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa3,
		X: numeric.Fixed(20) << 16, Z: numeric.Fixed(20) << 16,
	}}}, 6)
	c.replayForTest()
	painted := 0
	for _, px := range c.indexed {
		if px == 0xa3 {
			painted++
		}
	}
	if painted != stripParticleSize*stripParticleSize {
		t.Fatalf("one particle painted %d pixels, want %d",
			painted, stripParticleSize*stripParticleSize)
	}
}

// fullVisibility admits every tile so a draw-order test measures paint order
// alone and never the coverage gate.
func fullVisibility() frame.VisibilityView {
	vis := frame.VisibilityView{Valid: true, W: 64, H: 64, CoverageBytes: true, Visible: make([]byte, 64*64)}
	for i := range vis.Visible {
		vis.Visible[i] = 1
	}
	return vis
}

// parkedNanoView models one already-published strip-6 particle. The client
// paints this immutable copy and owns no particle lifetime state [03 §5.5][I6].
func parkedNanoView(x, z numeric.Fixed, colour uint8) frame.StripView {
	return frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: colour, X: x, Z: z}
}

// TestNanolatheSprayPaintsOverTheUnitBeingBuilt locks the draw-order half of
// the nanolathe contract, which is where playtest defect PT3-01 lived.
//
// Strip 6 is drawn after the first unit pass and before the second
// [03 §1][03 R-RAST-01 §7], so which pass the built unit falls into decides
// whether the spray is visible at all. The pass predicate is the unit record's
// flags-word mode mirror, and every spawn — a nanoframe laid by a construction
// unit included — writes that mirror as 1, so a structure and a nanoframe are
// both pass A and the spray paints over them. Only an airborne aircraft (2)
// and a unit attached to a carrier (0) are pass B and paint over the spray.
// See the 2026-08-30 correction under [03 R-RAST-01 §7]: the previous text put
// structures in pass B, which hid every construction spray behind the very
// building it was completing.
func TestNanolatheSprayPaintsOverTheUnitBeingBuilt(t *testing.T) {
	// The mark this scene measures: a nano particle at world (200, 160) fills
	// its two-by-two rectangle inside the flat model anchored at the same cell.
	const particleColour = 0xa3
	probe := func(c *Client) uint8 {
		sx, sy := c.cam.WorldToScreen(px(200), 0, px(160))
		x := int(sx - camera.OriginX)
		y := int(sy - camera.OriginY)
		return c.indexed[y*c.width+x]
	}

	// The published particle's colour is one of the raw green-ramp bytes
	// 0xa1..0xa7 [03 §5.5].
	onNanoRamp := func(v uint8) bool {
		return v >= 0xa1 && v <= 0xa7
	}
	cases := []struct {
		name      string
		moverMode uint8
		wantSpray bool
	}{
		// A structure or a nanoframe: mirror 1, pass A, spray over it.
		{name: "structure mirror 1 is pass A", moverMode: 1, wantSpray: true},
		// An airborne aircraft: mirror 2, pass B, aircraft over the spray.
		{name: "airborne mirror 2 is pass B", moverMode: 2, wantSpray: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			flatModel(c, "m_built", 60, 20)
			built := frame.UnitView{Slot: 1, Owner: 0, X: px(200), Z: px(160), MoverMode: tc.moverMode, Model: "m_built"}
			cur := &frame.Frame{
				Tick:       1,
				Visibility: fullVisibility(),
				Selection:  frame.SelectionView{LocalPlayer: 0},
				Units:      []frame.UnitView{built},
			}
			clearIndexed(c)
			cur.Strips = []frame.StripView{parkedNanoView(px(200), px(160), particleColour)}

			// The production order of drawCommittedFrame, with the terrain,
			// feature and label stages left out: pass A, the projectile and
			// effect strips (the committed strip particle paints inside drawEffects), pass B.
			c.drawWorldPass(cur, true)
			c.drawProjectiles(cur)
			c.drawEffects(cur)
			c.drawWorldPassB(cur, true)
			c.replayForTest() // one recording pass, one replay, in production order (WU-1.8)

			got := probe(c)
			if onNanoRamp(got) != tc.wantSpray {
				if tc.wantSpray {
					t.Fatalf("pixel where the spray meets the unit = %#x; want a nanolathe ramp colour — the unit painted over the spray", got)
				}
				t.Fatalf("pixel where the spray meets the unit = %#x; want the unit's own colour — the spray painted over an airborne unit", got)
			}
			if !tc.wantSpray && got != 20 {
				t.Fatalf("pixel where the spray meets the unit = %#x, want the model's 20", got)
			}
		})
	}
}
