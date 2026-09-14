package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestLensShaderCompiles(t *testing.T) {
	skipAfterDeviceLoop(t)
	if _, err := newLensShader(); err != nil {
		t.Fatal(err)
	}
}

func TestLensGeometryAppliesWorldTransformAndFramebufferClip(t *testing.T) {
	r := &Renderer{w: 40, h: 40}
	r.sched.setWorld(1, 2)
	l := drawlist.Lens{X: 40, Y: 40, Scale: camera.ViewScaleDetail, Clip: drawlist.Rect{X: 20, Y: 20, W: 20, H: 20}}
	r.lens.read.reset()
	r.appendLens(l)
	if len(r.lens.verts) == 0 {
		t.Fatal("lens emitted no clipped geometry")
	}
	c := r.lens.read
	if !c.any || c.x0 < 20 || c.y0 < 20 || c.x1 > 22 || c.y1 > 22 {
		t.Fatalf("inward capture bounds=%d,%d..%d,%d", c.x0, c.y0, c.x1, c.y1)
	}
	for _, v := range r.lens.verts {
		if v.DstX < 20 || v.DstY < 20 || v.DstX > 23 || v.DstY > 23 {
			t.Fatalf("world transform or fixed clip: %+v", v)
		}
	}
}
