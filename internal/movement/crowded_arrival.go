package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// CrowdedMoveBlocked is a bounded, read-only local query. It uses current
// footprint occupancy, not stale route bytes or a search rejection as proof
// of global reachability. Nanolathe Modern policy: DESIGN_MOVEMENT_PATH
// "Modern crowded arrival". Orders owns its 96-unit bound and 90-tick dwell.
func (s *System) CrowdedMoveBlocked(u *units.Unit, n *orders.Node) (int32, int32, bool) {
	if s == nil || s.world == nil || s.Terrain == nil || s.Grid == nil || u == nil || n == nil {
		return 0, 0, false
	}
	c := handleRow(s.Collisions, u.Handle)
	binding := handleRow(s.activeOrders, u.Handle)
	if c == nil || !c.HasStamp || c.StampedPlane != PlaneGround || c.Building || c.Speed != 0 || binding == nil || binding.order != n {
		return 0, 0, false
	}
	start := c.CachedAnchor
	no := func() (int32, int32, bool) { return start.X, start.Z, false }
	if r := handleRow(s.Routes, u.Handle); r != nil && r.Active && r.Count > 1 {
		return no()
	}
	bx, bz := c.HalfBias()
	goal := QuantizedAnchor(int32(n.GoalX.Raw()), int32(n.GoalZ.Raw()), bx, bz)
	dx, dz := goal.X-start.X, goal.Z-start.Z
	sx, sz := int32(1), int32(1)
	if dx < 0 {
		dx, sx = -dx, -1
	}
	if dz < 0 {
		dz, sz = -dz, -1
	}
	if dx > 6 || dz > 6 || dx+dz == 0 {
		return no()
	}
	fx, fz := c.FootPrintX, c.FootPrintZ
	if fx < 1 || fz < 1 {
		return no()
	}
	profile := s.ProfileFor(u.Handle)
	// Admitted anchors are statically clear. Every overlapping occupant must
	// be a stationary mobile of the same owner; no structure/enemy is relaxed.
	probe := func(at Cell) (staticClear, occupied bool) {
		if !commitRectInBounds(s.Terrain, at, fx, fz) || !profile.IsPassableFootprint(s.Terrain, at.X, at.Z) {
			return false, false
		}
		for zz := int32(0); zz < int32(fz); zz++ {
			for xx := int32(0); xx < int32(fx); xx++ {
				id, present := s.Grid.OccupantAtPlane(PlaneGround, Cell{X: at.X + xx, Z: at.Z + zz})
				if !present || id == int(u.Handle) {
					continue
				}
				other := s.world.Unit(pool.Handle(id))
				if other == nil || !other.Alive || other.Dying || other.Owner != u.Owner || other.Def == nil || other.Def.BMCode != 1 || !other.Def.CanMove || other.Def.CanFly || other.Remaining != 0 || other.Attachment.Carrier != 0 || other.Move.Speed != 0 {
					return false, false
				}
				oc := handleRow(s.Collisions, other.Handle)
				if oc == nil || !oc.HasStamp || oc.StampedPlane != PlaneGround || oc.Building || oc.Speed != 0 {
					return false, false
				}
				occupied = true
			}
		}
		return true, occupied
	}
	clear, occupied := probe(goal)
	if !clear || !occupied {
		return no()
	}
	// Supercover the short static corridor, allowing only the friendly crowd.
	at := start
	for ix, iz := int32(0), int32(0); ix < dx || iz < dz; {
		a, b := (1+2*ix)*dz, (1+2*iz)*dx
		switch {
		case a == b:
			if ok, _ := probe(Cell{X: at.X + sx, Z: at.Z}); !ok {
				return no()
			}
			if ok, _ := probe(Cell{X: at.X, Z: at.Z + sz}); !ok {
				return no()
			}
			at.X, at.Z = at.X+sx, at.Z+sz
			ix++
			iz++
		case a < b:
			at.X += sx
			ix++
		default:
			at.Z += sz
			iz++
		}
		if ok, _ := probe(at); !ok {
			return no()
		}
	}
	distance := func(at Cell) int64 { a, b := int64(at.X-goal.X), int64(at.Z-goal.Z); return a*a + b*b }
	current := distance(start)
	for _, dir := range [8]Cell{{X: 1}, {X: 1, Z: 1}, {Z: 1}, {X: -1, Z: 1}, {X: -1}, {X: -1, Z: -1}, {Z: -1}, {X: 1, Z: -1}} {
		at := Cell{X: start.X + dir.X, Z: start.Z + dir.Z}
		if distance(at) >= current {
			continue
		}
		clear, occupied := probe(at)
		if !clear || occupied {
			continue
		}
		if dir.X != 0 && dir.Z != 0 {
			xClear, xOccupied := probe(Cell{X: start.X + dir.X, Z: start.Z})
			zClear, zOccupied := probe(Cell{X: start.X, Z: start.Z + dir.Z})
			if !xClear || xOccupied || !zClear || zOccupied {
				continue
			}
		}
		return no()
	}
	return start.X, start.Z, true
}
