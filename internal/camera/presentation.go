package camera

import "math"

// PresentationView describes the continuous Enhanced projection in world
// pixels. It is never simulation state (DESIGN_GPU_RENDERER §16.5).
type PresentationView struct {
	X, Z, Factor float64
}

// PresentationView retains a zoom anchor's fractional origin while the
// integer camera remains at the corresponding zoom step. Other camera writers
// invalidate it by changing that origin or factor; a jump cannot reuse an old
// fractional anchor.
func (c *Camera) PresentationView() PresentationView {
	if c == nil {
		return PresentationView{Factor: 1}
	}
	if c.zoomView.Factor > 0 && c.X == c.zoomViewX && c.Z == c.zoomViewZ && c.EffectiveZoom() == c.zoomViewFactor {
		return c.zoomView
	}
	return PresentationView{X: float64(c.X), Z: float64(c.Z), Factor: c.EffectiveZoom().Float()}
}

// SetPresentationView installs an exact continuous view: the integer camera
// takes its floor and the fractional remainder is retained the way a zoom
// anchor's is, so a sampler sees the view it was given rather than the nearest
// whole world pixel. A scripted camera needs this: through the integer jump
// each sample lands up to a world pixel off its path — two screen pixels at
// 2x — and a slow push reads as a side-to-side shimmer. A clamp takes
// precedence in its own axis. Presentation state only [I6].
func (c *Camera) SetPresentationView(v PresentationView) {
	if c == nil {
		return
	}
	z := Zoom(math.Round(v.Factor * float64(ZoomUnit))).Norm()
	// The request is kept unfloored: a sub-native request the map cannot
	// reach is what selects the full strategic view at the floor (§16.7).
	c.requestedZoom = z
	if minZ := c.MinZoom(); z < minZ {
		// The origin was derived for the requested factor; re-anchor it about
		// the viewport centre for the factor the map allows.
		w, h := float64(c.ViewW+OriginX)/2, float64(c.ViewH)/2
		v.X += w * (1/v.Factor - 1/minZ.Float())
		v.Z += h * (1/v.Factor - 1/minZ.Float())
		z, v.Factor = minZ, minZ.Float()
	}
	if z == ZoomMax && v.Factor > ZoomMax.Float() {
		v.Factor = ZoomMax.Float()
	}
	c.Zoom, c.Scale = z, z.Step()
	c.X, c.Z = int32(math.Floor(v.X)), int32(math.Floor(v.Z))
	beforeX, beforeZ := c.X, c.Z
	c.Clamp()
	if c.X != beforeX {
		v.X = float64(c.X)
	}
	if c.Z != beforeZ {
		v.Z = float64(c.Z)
	}
	c.zoomView, c.zoomViewX, c.zoomViewZ, c.zoomViewFactor = v, c.X, c.Z, z
}
