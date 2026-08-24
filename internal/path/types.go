package path

// Cell is a lattice coordinate on the TNT attribute-cell grid.
// The lattice covers 16 map pixels per cell [04 §7.1].
type Cell struct {
	X int32
	Z int32
}

// Point is a published route point in world coordinates.
// Route publication converts each lattice cell to signed world
// coordinates using the profile's half-footprint bias [04 §7.1].
type Point struct {
	X int32
	Z int32
}

// Rect is an axis-aligned rectangle on the lattice, used by
// rectangle-perimeter goals. Min is inclusive, Max is inclusive.
type Rect struct {
	Min Cell
	Max Cell
}

// Status is the completion status notified by path search [04 §7.2].
type Status uint32

const (
	StatusAlreadySatisfied Status = 0x100 // start satisfies goal [04 §7.2]
	StatusRejected         Status = 0x200 // rejected / unreachable [04 §7.2]
)

// Goal is the per-search heuristic family [04 §7.2].
// It is supplied at search construction and interrogated during
// expansion. Enumerate returns goal cells, StartSatisfied is the
// early-exit predicate, and H is the pre-scale heuristic h.
//
// C8 families: point/radius (18*max+7*min), annulus (V-shaped zero band),
// rect perimeter (16*max+6*min outside), saved (identically zero) [04 §7.2].
type Goal interface {
	Enumerate(out []Cell) []Cell
	StartSatisfied(start Cell) bool
	H(c Cell) int32
}
