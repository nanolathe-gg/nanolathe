package drawlist

// WaterSurface captures the Enhanced water animation operands (GPU design §26).
// The terrain record owns these values; replay never reads a live session.
type WaterSurface struct {
	Enabled                  bool
	Tick                     uint32
	Fraction16               int32
	WindHeading              uint16
	WindStrength             int32
	DriftX, DriftZ, Energy   float32
	TidalDriftX, TidalDriftZ float32 // Presentation current, independent of wind strength.
}

// SurfaceWake carries hover dust or stationary building foam (GPU design §26).
// Centre and half-vectors are recording pixels; Age and Alpha are in [0,1].
type SurfaceWake struct {
	X, Y, AxisX, AxisY, CrossX, CrossY, Age, Alpha float32
	Dust                                           bool
	Foam                                           bool
}

type SurfaceWakes struct{ Marks []SurfaceWake }

type SurfaceWakeSink interface{ SurfaceWakes(SurfaceWakes) }

// RecordSurfaceWakes borrows the marks until Reset. Clone owns its copy.
func (l *List) RecordSurfaceWakes(c SurfaceWakes) {
	l.order = append(l.order, tag{familySurfaceWakes, len(l.surfaceWakes)})
	l.surfaceWakes = append(l.surfaceWakes, c)
}
