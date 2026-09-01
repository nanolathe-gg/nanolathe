package airdiag

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestRenderedFacingFollowsSimHeading separates "the simulation does not turn"
// from "the simulation turns but presentation draws one facing". It builds the
// aircraft's real 3DO through the production draw builder at four headings a
// quarter circle apart and measures how far the composed model-space geometry
// actually rotates [03 §2.4] C24.
//
// It is a presentation observation, not a retail contract test: the only claim
// asserted is that the draw path is sensitive to the heading word at all.
func TestRenderedFacingFollowsSimHeading(t *testing.T) {
	h := newHarness(t)
	def, ok := h.Catalog.Unit(diagAircraft)
	if !ok || def == nil {
		t.Skipf("%s absent", diagAircraft)
	}
	m, err := model.Load(h.FS, "objects3d/"+def.ObjectName+".3do")
	if err != nil {
		t.Skipf("load model %q: %v", def.ObjectName, err)
	}

	type sample struct {
		heading uint16
		x, z    float64
	}
	var samples []sample
	for _, heading := range []uint16{0, 16384, 32768, 49152} {
		draw := render.BuildUnitDrawSimple(m, nil, heading, 0, 0, [3]numeric.Fixed{})
		if draw == nil || len(draw.Transforms) == 0 {
			t.Fatalf("draw at heading %d produced no transforms", heading)
		}
		// The farthest piece origin from the model origin is the most
		// heading-sensitive point available without naming an authored piece.
		var bx, bz float64
		var best float64 = -1
		for _, tr := range draw.Transforms {
			x := float64(int64(tr.Origin[0])) / 65536.0
			z := float64(int64(tr.Origin[2])) / 65536.0
			if d := math.Hypot(x, z); d > best {
				best, bx, bz = d, x, z
			}
		}
		samples = append(samples, sample{heading: heading, x: bx, z: bz})
		t.Logf("heading %5d -> farthest piece origin (%.3f, %.3f), radius %.3f", heading, bx, bz, best)
	}

	base := samples[0]
	if math.Hypot(base.x, base.z) < 0.001 {
		t.Skip("this model's piece origins are all on the model axis; no rotation is observable from them")
	}
	for _, s := range samples[1:] {
		if math.Abs(s.x-base.x) < 0.001 && math.Abs(s.z-base.z) < 0.001 {
			t.Errorf("the composed draw at heading %d is identical to heading %d: presentation is not following the heading word",
				s.heading, base.heading)
		}
	}
}
