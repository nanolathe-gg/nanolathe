package drawlist

// Arrival is an authored, presentation-only skirmish opening (GPU design §36).
// Coordinates and Scale are in recording pixels; the world transform applies
// once in the executor. Zero Active leaves ordinary rendering untouched.
type Arrival struct {
	Active       bool
	Seconds      float32
	X, Y         float32
	GridX, GridY float32
	Scale        float32
}

// ArrivalImpactSeconds and ArrivalDurationSeconds are artistic prototype
// timings, not retail behavior or simulation time.
const ArrivalImpactSeconds float32 = 0.8
const ArrivalDurationSeconds float32 = 3.2
