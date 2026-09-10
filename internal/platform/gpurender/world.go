package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The modern executor's half of smooth zoom (docs/DESIGN_GPU_RENDERER.md §16).
//
// One affine transform does the whole of it. The recorder emits the world at an
// integer step; between the world region's two boundary markers the scheduler
// scales every placed rectangle and every appended vertex by the live factor
// over that step, about the surface origin, and clips against the record extent
// rather than against the framebuffer. Outside the region — the chrome, the
// cursor, the strategic markers — nothing changes, which is why the HUD stays
// at its authored pixel size at every factor.
//
// At a rest factor the scale is one, the record extent IS the framebuffer, and
// every device call this package makes is the one it made before §16. That is
// what keeps the §6 parity gate exact at 1x and 2x.

// World opens or closes the recorded world region. It is the drawlist.WorldSink
// method; the classic executor does not implement it (§16.3).
//
// It is a barrier in the same sense the clear is: the transform changes what a
// compiled rectangle means, so everything pending is submitted before it
// changes. A frame has exactly two of them, so the cost is two submissions.
func (r *Renderer) World(w drawlist.WorldSpace) {
	if r == nil {
		return
	}
	r.submitSchedule()
	if !w.Begin {
		r.worldW, r.worldH = r.w, r.h
		r.sched.clearWorld()
		return
	}
	r.worldW, r.worldH = r.w, r.h
	if int(w.RecordW) > r.worldW {
		r.worldW = int(w.RecordW)
	}
	if int(w.RecordH) > r.worldH {
		r.worldH = int(w.RecordH)
	}
	// The two factors are exact small integers in the same units, so the scale
	// is an exact rational and a rest step disarms the transform outright.
	r.sched.setWorld(int32(w.Zoom), int32(camera.ZoomOf(w.Step)))
}

// clipW and clipH are the extent every family clips a world command against:
// the record extent inside the world region, the framebuffer everywhere else.
// Outside the region they are r.w and r.h exactly, so an interface family reads
// the number it always read.
func (r *Renderer) clipW() int {
	if r.worldW <= 0 {
		return r.w
	}
	return r.worldW
}

func (r *Renderer) clipH() int {
	if r.worldH <= 0 {
		return r.h
	}
	return r.worldH
}

// Markers draws the strategic marker layer: one alpha-blended square per unit,
// plus a one-pixel selection outline (§16.11). It is the drawlist.MarkerSink
// method; the classic executor does not implement it.
//
// The markers are recorded OUTSIDE the world region and already positioned
// through the live factor, so they compile with the transform disarmed and keep
// their authored pixel size at every zoom. They ride the destination pass so
// the fade composites over the terrain under them.
func (r *Renderer) Markers(m drawlist.Markers) {
	if r == nil || r.sceneDest == nil || r.tables.atlas == nil || len(m.Marks) == 0 {
		return
	}
	imgs := [4]*ebiten.Image{1: r.tables.atlas}
	for i := range m.Marks {
		mk := &m.Marks[i]
		if mk.Size <= 0 || mk.Alpha == 0 {
			continue
		}
		half := mk.Size / 2
		x0, y0 := int(mk.X-half), int(mk.Y-half)
		x1, y1 := x0+int(mk.Size), y0+int(mk.Size)
		if mk.HasClip {
			x0, y0 = maxInt(x0, int(mk.Clip.X)), maxInt(y0, int(mk.Clip.Y))
			x1 = minInt(x1, int(mk.Clip.X+mk.Clip.W))
			y1 = minInt(y1, int(mk.Clip.Y+mk.Clip.H))
		}
		x0, y0 = maxInt(x0, 0), maxInt(y0, 0)
		x1, y1 = minInt(x1, r.w), minInt(y1, r.h)
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		if !r.sched.begin(schedDest, x0, y0, x1, y1, imgs) {
			continue
		}
		alpha := float32(mk.Alpha) / 255
		r.sched.quad(schedDest,
			float32(x0), float32(y0), float32(x1), float32(y1), 0, 0, 0, 0,
			[4]float32{float32(mk.Index), alpha, 0, 0},
			[4]float32{0, 0, 0, destOpMarker})
		if !mk.Selected {
			continue
		}
		// The outline is the same op on the square's four one-pixel edges, so it
		// fades with the layer instead of appearing at full strength first.
		outline := [4]float32{float32(mk.Outline), alpha, 0, 0}
		custom := [4]float32{0, 0, 0, destOpMarker}
		fx0, fy0 := float32(x0), float32(y0)
		fx1, fy1 := float32(x1), float32(y1)
		r.sched.quad(schedDest, fx0, fy0, fx1, fy0+1, 0, 0, 0, 0, outline, custom)
		r.sched.quad(schedDest, fx0, fy1-1, fx1, fy1, 0, 0, 0, 0, outline, custom)
		r.sched.quad(schedDest, fx0, fy0, fx0+1, fy1, 0, 0, 0, 0, outline, custom)
		r.sched.quad(schedDest, fx1-1, fy0, fx1, fy1, 0, 0, 0, 0, outline, custom)
	}
}
