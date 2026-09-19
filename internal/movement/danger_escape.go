package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// DangerStepFeasible checks a short escape corridor without submitting a path,
// changing occupancy or spending RNG. The orders rule owns whether to retreat;
// the ordinary mover still owns the eventual route and collision decisions.
// Nanolathe Modern policy: DESIGN_MOVEMENT_PATH "Modern danger escape".
func (s *System) DangerStepFeasible(u *units.Unit, x, z numeric.Fixed) bool {
	if s == nil || s.Terrain == nil || s.Grid == nil || u == nil || u.Def == nil ||
		!u.Alive || u.Dying || u.Def.BMCode != 1 {
		return false
	}
	c := handleRow(s.Collisions, u.Handle)
	if c == nil {
		return false
	}
	bx, bz := c.HalfBias()
	start := QuantizedAnchor(int32(u.X.Raw()), int32(u.Z.Raw()), bx, bz)
	end := QuantizedAnchor(int32(x.Raw()), int32(z.Raw()), bx, bz)
	dx, dz := end.X-start.X, end.Z-start.Z
	sx, sz := int32(1), int32(1)
	if dx < 0 {
		dx, sx = -dx, -1
	}
	if dz < 0 {
		dz, sz = -dz, -1
	}
	// This is a bounded local escape probe, not another global path search.
	// Sixteen cells is the prototype's maximum displacement along either axis.
	if dx > 16 || dz > 16 || dx+dz == 0 {
		return false
	}
	fx, fz := c.FootPrintX, c.FootPrintZ
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	profile := s.ProfileFor(u.Handle)
	plane := PlaneGround
	if u.Def.CanFly {
		plane = PlaneAir
	}
	clear := func(at Cell) bool {
		if !commitRectInBounds(s.Terrain, at, fx, fz) {
			return false
		}
		if plane == PlaneGround && !profile.IsPassableFootprint(s.Terrain, at.X, at.Z) {
			return false
		}
		for zz := int32(0); zz < int32(fz); zz++ {
			for xx := int32(0); xx < int32(fx); xx++ {
				if id, occupied := s.Grid.OccupantAtPlane(plane, Cell{X: at.X + xx, Z: at.Z + zz}); occupied && id != int(u.Handle) {
					return false
				}
			}
		}
		return true
	}
	// Supercover traversal checks both adjacent anchors on a diagonal crossing,
	// so a clear destination cannot justify moving through a blocked corner.
	at := start
	for ix, iz := int32(0), int32(0); ix < dx || iz < dz; {
		xCross, zCross := (1+2*ix)*dz, (1+2*iz)*dx
		switch {
		case xCross == zCross:
			if !clear(Cell{X: at.X + sx, Z: at.Z}) || !clear(Cell{X: at.X, Z: at.Z + sz}) {
				return false
			}
			at.X, at.Z = at.X+sx, at.Z+sz
			ix, iz = ix+1, iz+1
		case xCross < zCross:
			at.X += sx
			ix++
		default:
			at.Z += sz
			iz++
		}
		if !clear(at) {
			return false
		}
	}
	return true
}

// DangerRouteFeasible preserves direct corridor admission, then considers a
// bounded local detour for a ground mover. This is Modern candidate admission;
// it neither constructs a route nor replaces the ordinary movement scheduler.
// Orders still owns danger scoring, stance, leash and the 30-tick decision rate.
func (s *System) DangerRouteFeasible(u *units.Unit, x, z numeric.Fixed) bool {
	if s.DangerStepFeasible(u, x, z) {
		return true
	}
	if s == nil || u == nil || u.Def == nil || u.Def.CanFly || u.Move.Mode != 1 ||
		s.Terrain == nil || s.Grid == nil || u.Def.BMCode != 1 || !u.Alive || u.Dying {
		return false
	}
	dx, dz := int64(x-u.X), int64(z-u.Z)
	const radius = int64(64 << 16) // Modern prototype local destination bound
	if dx < -radius || dx > radius || dz < -radius || dz > radius || dx*dx+dz*dz > radius*radius || dx == 0 && dz == 0 {
		return false
	}
	// The radius-four integer disk contains 49 points including the origin.
	// Five cardinal 16-unit edges plus a final corridor (at most 16 per axis)
	// cover the captured crowd's south-first escape without a general search.
	type node struct{ x, z, depth int8 }
	var queue [49]node
	var visited [9][9]bool
	visited[4][4] = true
	count := 1
	probe := *u
	for head := 0; head < count; head++ {
		at := queue[head]
		probe.X = u.X + numeric.Fixed(int64(at.x)*16<<16)
		probe.Z = u.Z + numeric.Fixed(int64(at.z)*16<<16)
		tailX, tailZ := int64(x-probe.X), int64(z-probe.Z)
		if at.depth != 0 && tailX >= -16<<16 && tailX <= 16<<16 && tailZ >= -16<<16 && tailZ <= 16<<16 &&
			s.DangerStepFeasible(&probe, x, z) {
			return true
		}
		if at.depth == 5 {
			continue
		}
		for _, dir := range [4][2]int8{{1, 0}, {0, 1}, {-1, 0}, {0, -1}} {
			nx, nz := at.x+dir[0], at.z+dir[1]
			if nx < -4 || nx > 4 || nz < -4 || nz > 4 || int(nx)*int(nx)+int(nz)*int(nz) > 16 || visited[nx+4][nz+4] {
				continue
			}
			cx, cz := u.X+numeric.Fixed(int64(nx)*16<<16), u.Z+numeric.Fixed(int64(nz)*16<<16)
			if !s.DangerStepFeasible(&probe, cx, cz) {
				continue
			}
			if cx == x && cz == z {
				return true
			}
			visited[nx+4][nz+4] = true
			queue[count] = node{nx, nz, at.depth + 1}
			count++
		}
	}
	return false
}
