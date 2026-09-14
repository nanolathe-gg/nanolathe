package camera

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
