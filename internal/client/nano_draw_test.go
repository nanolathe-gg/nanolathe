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
