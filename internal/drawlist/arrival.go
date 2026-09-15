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
	// RevealRadius is the farthest on-screen explored chunk in world pixels.
	// Zero falls back to the viewport extent when fog metadata is unavailable.
	RevealRadius float32
	// DropHeight is the authored starting lift in world pixels.
	DropHeight float32
}

// ArrivalImpactSeconds and ArrivalDurationSeconds are artistic prototype
// timings, not retail behavior or simulation time.
const ArrivalLeadSeconds float32 = 0.5
const ArrivalDropSeconds float32 = 1.05
const ArrivalImpactSeconds float32 = 1.33
const ArrivalDurationSeconds float32 = 2.05

const ArrivalRevealSeconds float32 = 1.3
const ArrivalCoolingEndSeconds float32 = ArrivalImpactSeconds + 4
