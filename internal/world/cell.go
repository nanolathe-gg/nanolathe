// Cell is the lattice coordinate for factory exit-spot snapping [05 "Factory production lifecycle"] I3.

package world

// Cell is a lattice coordinate on the attribute-cell grid [03 §2.1] [04 §7.1].
// It is the same domain as path.Cell but owned here for the construction API
// that returns world.Cell per PLAN_08. X and Z are cell indices.
type Cell struct {
	X int32
	Z int32
}
