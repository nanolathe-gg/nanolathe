package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// A particle's mark is two by two, because retail's rectangle filler is
// inclusive on both edges [03 §5.5]. At one pixel the spray reads as a dotted
// line, which is the defect this locks against.
func TestNanolatheParticleMarkIsTwoByTwo(t *testing.T) {
	c := &Client{width: 64, height: 64, indexed: make([]uint8, 64*64), cam: &camera.Camera{ViewW: 64, ViewH: 64}}
	c.nano.Records = []render.NanoRecord{{Particles: []render.NanoParticle{
		{X: numeric.Fixed(20) << 16, Z: numeric.Fixed(20) << 16, Color: 0xa3},
	}}}
	vis := frame.VisibilityView{Valid: true, W: 8, H: 8, CoverageBytes: true, Visible: make([]byte, 64)}
	for i := range vis.Visible {
		vis.Visible[i] = 1
	}
	c.drawNanolathe(&frame.Frame{Tick: 1, Visibility: vis})
	painted := 0
	for _, px := range c.indexed {
		if px == 0xa3 {
			painted++
		}
	}
	if painted != render.NanoParticleSize*render.NanoParticleSize {
		t.Fatalf("one particle painted %d pixels, want %d",
			painted, render.NanoParticleSize*render.NanoParticleSize)
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

// parkedNano pins one live particle at a world point with no velocity and an
// expiry far past the frame under test, and closes the record's spawn window,
// so the field survives the committed-tick advance unchanged and the frame's
// only variable is paint order.
func parkedNano(c *Client, x, z numeric.Fixed, colour uint8) {
	c.nano.Records = []render.NanoRecord{{
		EndTick:   0,
		NextSpawn: 1,
		Particles: []render.NanoParticle{{X: x, Z: z, Color: colour, ExpiryTick: 1 << 20}},
	}}
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

	// The committed-tick advance recolours a live particle one step up the
	// green ramp before it paints, so the spray's own pixel is any of
	// 0xa1..0xa7 rather than the colour it was seeded with [03 §5.5].
	onNanoRamp := func(v uint8) bool {
		return v > render.NanoColorBase && v <= render.NanoColorBase+render.NanoColorSpan
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
			parkedNano(c, px(200), px(160), particleColour)

			// The production order of drawCommittedFrame, with the terrain,
			// feature and label stages left out: pass A, the projectile and
			// effect strips (the nano field paints inside drawEffects), pass B.
			c.drawWorldPass(cur, true)
			c.drawProjectiles(cur)
			c.drawEffects(cur)
			c.drawWorldPassB(cur, true)

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
