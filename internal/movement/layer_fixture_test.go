package movement

import "github.com/nanolathe-gg/nanolathe/internal/world"

// NewClassLayer allocates the layer for one class over the terrain and stamps
// the whole map [04 §6.1]: the single-cell classifier form runs per attribute
// cell with the watermark zero, so occupants never block at load, then the
// two-direction contagion pass runs [04 §6.1 R-DOC04-B]. Bounds are explicit:
// the layer covers exactly the terrain's attribute-cell lattice.
func NewClassLayer(p Profile, t *world.Terrain, grid *OccupancyGrid) *ClassLayer {
	l := newUnstampedClassLayer(p, t, grid)
	l.stampAll()
	return l
}
