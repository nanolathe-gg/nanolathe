package construction

import (
	"math"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const (
	kickoutPi        = 3.141592654
	kickoutQuarterPi = 0.785398163
)

type kickRecord struct {
	x, y, z numeric.Fixed
	valid   bool
}

// YieldObstruction implements enabled CP-CON-1 only at the urgent blocked-site
// branch. Factory exits and refused yard closes keep the retail answer. Modern
// overrides this method with its existing deterministic clearance policy
// [DESIGN_COMMUNITY_PATCH §4.3, §11].
func (CommunityRules) YieldObstruction(s *Service, requester *units.Unit, clear world.FootprintRect, _ []world.YardCell, tick uint32, urgent bool) {
	if !urgent || s == nil || s.World == nil || s.Terrain == nil || s.CRTRandom == nil || requester == nil || !s.Community.ConstructionKickout {
		return
	}
	for _, h := range s.kickoutOccupants(clear) {
		u := s.World.Unit(h)
		if !s.AdmitSiteOccupant(requester, u) {
			continue
		}
		// The approved per-candidate cadence draws even before protected-work
		// rejection (DESIGN_COMMUNITY_PATCH §11 Q3); source draws at search entry.
		randomDirection := float64(s.CRTRandom(360)) / 57.0
		if !s.shouldKickout(u, clear) {
			continue
		}
		x, z, ok := s.findKickoutDestination(u, clear, randomDirection)
		if !ok {
			continue
		}
		s.kickoutMove(u, x, u.Y, z, tick)
	}
}

// KickoutMove is the manual CP-CON-1 move used by the host's Alt drag. It
// intentionally performs no search, placement validation or random draw; it
// applies the same sourced queue rewrite and records the issued destination.
func (s *Service) KickoutMove(u *units.Unit, x, y, z numeric.Fixed, tick uint32) bool {
	if s == nil || !s.rules().KickoutEnabled(s) {
		return false
	}
	return s.kickoutMove(u, x, y, z, tick)
}

func (s *Service) kickoutMove(u *units.Unit, x, y, z numeric.Fixed, tick uint32) bool {
	if u == nil {
		return false
	}
	already := s.isBeingKickedOut(u)
	if !orders.KickoutRewrite(u, x, y, z, tick, already) {
		return false
	}
	idx := int(u.Handle)
	if idx >= len(s.kickRecords) {
		s.kickRecords = append(s.kickRecords, make([]kickRecord, idx-len(s.kickRecords)+1)...)
	}
	s.kickRecords[idx] = kickRecord{x: x, y: y, z: z, valid: true}
	return true
}

func (s *Service) isBeingKickedOut(u *units.Unit) bool {
	if s == nil || u == nil || int(u.Handle) >= len(s.kickRecords) {
		return false
	}
	r := &s.kickRecords[int(u.Handle)]
	if !r.valid {
		return false
	}
	q := orders.QueueOfUnit(u)
	if q == nil || q.Head() == nil {
		return false
	}
	head := q.Head()
	name := orders.DescriptorFor(head.ID).Name
	if name != "Move_Ground" && name != "VTOL_Move" {
		return false
	}
	if head.GoalX == r.x && head.GoalY == r.y && head.GoalZ == r.z {
		return true
	}
	r.valid = false
	return false
}

func (s *Service) kickoutOccupants(clear world.FootprintRect) []pool.Handle {
	var result []pool.Handle
	add := func(raw int) {
		if raw <= 0 {
			return
		}
		h := pool.Handle(raw)
		if !slices.Contains(result, h) {
			result = append(result, h)
		}
	}
	for z := clear.MinZ(); z < clear.MaxZ(); z++ {
		for x := clear.MinX(); x < clear.MaxX(); x++ {
			if cell := s.Terrain.PlotAt(x, z); cell != nil {
				add(int(cell.OccupantA()))
			}
			if s.Terrain.Movers != nil {
				add(int(s.Terrain.Movers.CellOccupant(x, z)))
			}
		}
	}
	slices.Sort(result)
	return result
}

func (s *Service) shouldKickout(u *units.Unit, clear world.FootprintRect) bool {
	q := orders.QueueOfUnit(u)
	if q == nil || q.Head() == nil {
		return true
	}
	head := q.Head()
	name := orders.DescriptorFor(head.ID).Name
	if name == "Move_Ground" || name == "VTOL_Move" {
		return clear.Contains(world.WorldToCell(head.GoalX), world.WorldToCell(head.GoalZ))
	}
	if head.Target == 0 {
		return true
	}
	target := s.World.Unit(head.Target)
	if target == nil || target.Def == nil || target.Remaining <= 0 {
		return true
	}
	// CP-CON-1 source arithmetic is binary64 throughout; the strict < 600
	// comparison deliberately includes a target with zero invested energy.
	invested := float64(target.Def.BuildCostEnergy) * (1.0 - float64(target.Remaining))
	if invested >= 600.0 {
		return true
	}
	for _, other := range s.World.Iter() {
		if other == nil || other == u || !other.Alive || other.Dying || other.Owner != u.Owner {
			continue
		}
		otherQ := orders.QueueOfUnit(other)
		if otherQ != nil && otherQ.Head() != nil && otherQ.Head().Target == target.Handle && otherQ.Head().Phase >= 2 {
			return true
		}
	}
	return false
}

func (s *Service) findKickoutDestination(u *units.Unit, clear world.FootprintRect, randomDirection float64) (numeric.Fixed, numeric.Fixed, bool) {
	if u == nil || u.Def == nil || s.Terrain == nil {
		return 0, 0, false
	}
	centerX := int64((clear.MinX() + clear.MaxX()) * 8)
	centerZ := int64((clear.MinZ() + clear.MaxZ()) * 8)
	unitX, unitZ := u.X.Int(), u.Z.Int()
	direction := randomDirection
	if !s.isBeingKickedOut(u) {
		if q := orders.QueueOfUnit(u); q != nil && q.Head() != nil && q.Head().Target != 0 {
			target := s.World.Unit(q.Head().Target)
			if target != nil {
				targetDirection := math.Atan2(float64(target.Z.Int()-unitZ), float64(target.X.Int()-unitX))
				dxKick, dzKick := float64(centerX-unitX), float64(centerZ-unitZ)
				plus := float64(math.Cos(targetDirection+kickoutQuarterPi)*dxKick) + float64(math.Sin(targetDirection+kickoutQuarterPi)*dzKick)
				minus := float64(math.Cos(targetDirection-kickoutQuarterPi)*dxKick) + float64(math.Sin(targetDirection-kickoutQuarterPi)*dzKick)
				if plus < minus {
					direction = targetDirection + kickoutQuarterPi
				} else {
					direction = targetDirection - kickoutQuarterPi
				}
				nominal := float64(24 * clear.Width())
				if ix, iz, ok := forwardCircleIntersection(float64(centerX), float64(centerZ), nominal, float64(unitX), float64(unitZ), direction); ok &&
					ix > 16 && ix < float64(s.Terrain.CellW*16) && iz > 16 && iz < float64(s.Terrain.CellH*16) {
					direction = math.Atan2(iz-float64(centerZ), ix-float64(centerX))
				}
			}
		} else if unitX != centerX || unitZ != centerZ {
			direction = math.Atan2(float64(unitZ-centerZ), float64(unitX-centerX))
		}
	}

	nominal := int64(24) * int64(clear.Width())
	for radius := nominal; radius < 2*nominal; radius += 16 {
		r := float64(radius)
		for offset := 0.0; offset < kickoutPi; offset += 16.0 / r {
			for sign := -1.0; sign <= 1.0; sign += 2.0 {
				angle := direction + float64(sign*offset)
				xStep := float64(r * math.Cos(angle))
				zStep := float64(r * math.Sin(angle))
				x := centerX + int64(xStep)
				z := centerZ + int64(zStep)
				if s.kickoutCellClear(u, x, z) {
					return numeric.FixedFromInt(x), numeric.FixedFromInt(z), true
				}
			}
		}
	}
	return 0, 0, false
}

func forwardCircleIntersection(cx, cz, radius, x, z, direction float64) (float64, float64, bool) {
	tangent := math.Tan(direction)
	if math.Abs(tangent) <= 1.0 {
		u := tangent
		v := float64(z-cz) - float64(x*tangent)
		x1, x2, ok := kickoutQuadratic(float64(u*u)+1.0, 2.0*float64(float64(u*v)-cx), float64(float64(v*v)+float64(cx*cx))-float64(radius*radius))
		if !ok {
			return 0, 0, false
		}
		for _, candidateX := range [...]float64{x1, x2} {
			candidateZ := z + float64(float64(candidateX-x)*tangent)
			dx := candidateX - x
			dz := candidateZ - z
			forward := float64(dx*math.Cos(direction)) + float64(dz*math.Sin(direction))
			if forward >= 0.0 {
				return candidateX, candidateZ, true
			}
		}
		return 0, 0, false
	}

	u := 1.0 / tangent
	v := float64(x-cx) - float64(z/tangent)
	z1, z2, ok := kickoutQuadratic(float64(u*u)+1.0, 2.0*float64(float64(u*v)-cz), float64(float64(v*v)+float64(cz*cz))-float64(radius*radius))
	if !ok {
		return 0, 0, false
	}
	for _, candidateZ := range [...]float64{z1, z2} {
		candidateX := x + float64(float64(candidateZ-z)/tangent)
		dx := candidateX - x
		dz := candidateZ - z
		forward := float64(dx*math.Cos(direction)) + float64(dz*math.Sin(direction))
		if forward >= 0.0 {
			return candidateX, candidateZ, true
		}
	}
	return 0, 0, false
}

func kickoutQuadratic(a, b, c float64) (float64, float64, bool) {
	determinant := float64(b*b) - float64(float64(4.0*a)*c)
	if determinant < 0.0 {
		return 0, 0, false
	}
	root := math.Sqrt(determinant)
	return (-b + root) / a / 2.0, (-b - root) / a / 2.0, true
}

func (s *Service) kickoutCellClear(u *units.Unit, x, z int64) bool {
	if x < 16 || z < 16 || x >= int64(s.Terrain.CellW)*16 || z >= int64(s.Terrain.CellH)*16 {
		return false
	}
	cx, cz := int32(x/16), int32(z/16)
	if cx <= 0 || cz <= 0 || cx+1 >= s.Terrain.CellW || cz+1 >= s.Terrain.CellH {
		return false
	}
	cell := s.Terrain.PlotAt(cx, cz)
	if cell == nil || cell.OccupantA() != 0 || (s.Terrain.Movers != nil && s.Terrain.Movers.CellOccupant(cx, cz) != 0) {
		return false
	}
	if feature, ok := s.Terrain.FeatureDefAt(cell.Feature()); ok && feature.Height != 0 {
		return false
	}
	return int32(cell.MaxHeight())-int32(cell.MinHeight()) < u.Def.MaxSlope
}
