package construction

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These bounded search limits are Modern policy, not retail constants:
// DESIGN_ECONOMY_CONSTRUCTION, "Modern factory-exit yielding" and "Modern construction-site yielding". Clearance
// issues ordinary orders; it never writes transforms, occupancy, resources or RNG.
const (
	factoryYieldRadius   = 8
	factoryYieldBudget   = 256
	factoryYieldBlockers = 8
)

func (s *Service) yieldClosingYard(factory *units.Unit, requested bool, rect world.FootprintRect, yard []world.YardCell, tick uint32) {
	if !requested {
		s.yieldFactoryExit(factory, rect, yard, tick)
	}
}

func (s *Service) yieldFactoryExit(factory *units.Unit, clear world.FootprintRect, closingYard []world.YardCell, tick uint32) {
	if factory == nil || factory.Def == nil || factory.Def.BMCode != 0 || !factory.Def.Builder {
		return
	}
	s.yieldObstruction(factory, clear, closingYard, tick, false)
}

// yieldConstructionSite gives an idle blocker's local route priority over the
// global search backlog, without extending the builder's blocked-site budget.
// Nanolathe Modern policy: DESIGN_ECONOMY_CONSTRUCTION, "Modern construction-site yielding".
func (s *Service) yieldConstructionSite(builder *units.Unit, clear world.FootprintRect, tick uint32) {
	s.yieldObstruction(builder, clear, nil, tick, true)
}

func (s *Service) yieldObstruction(requester *units.Unit, clear world.FootprintRect, closingYard []world.YardCell, tick uint32, urgent bool) {
	if s == nil || !s.ModernConstructionClearance || s.World == nil || s.Movement == nil || s.Terrain == nil || requester == nil {
		return
	}
	var blockers []pool.Handle
	add := func(id int) {
		if id > 0 && pool.Handle(id) != requester.Handle && !slices.Contains(blockers, pool.Handle(id)) {
			blockers = append(blockers, pool.Handle(id))
		}
	}
	for z := clear.MinZ(); z < clear.MaxZ(); z++ {
		for x := clear.MinX(); x < clear.MaxX(); x++ {
			if closingYard != nil && closingYard[(z-clear.MinZ())*clear.Width()+x-clear.MinX()]&0x04 == 0 {
				continue
			}
			if cell := s.Terrain.PlotAt(x, z); cell != nil {
				add(int(cell.OccupantA()))
			}
			if id, held := s.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z}); held {
				add(id)
			}
		}
	}
	slices.Sort(blockers)
	eligible := blockers[:0]
	for _, h := range blockers {
		if s.factoryYieldEligible(requester, s.World.Unit(h)) {
			eligible = append(eligible, h)
			if len(eligible) == factoryYieldBlockers {
				break
			}
		}
	}
	if len(eligible) == 0 {
		return
	}
	// Avoid neighboring factory yards even when open and currently unstamped.
	// The ordered unit walk avoids iterating the placement lookup map [I1].
	forbidden := []world.FootprintRect{clear}
	for _, u := range s.World.Iter() {
		if u.Def != nil && u.Def.BMCode == 0 && u.Def.Builder {
			if r, ok := s.PlacementForProduct(u.Handle); ok {
				forbidden = append(forbidden, r)
			}
		}
	}
	for _, h := range eligible {
		u := s.World.Unit(h)
		destination, route, ok := s.factoryYieldDestination(u, forbidden, urgent)
		if !ok {
			continue
		}
		// Point goals use the movement footprint's half-extent bias [04 §7.1].
		x := world.CellToWorld(destination.MinX()) + numeric.Fixed(int64(destination.Width())*8<<16)
		z := world.CellToWorld(destination.MinZ()) + numeric.Fixed(int64(destination.Depth())*8<<16)
		q := s.queueForUnit(u)
		id := orders.Lookup("Move_Ground")
		q.Push(id, orders.NewMoveNode(id, x, z, tick, u.Handle, true))
		if urgent {
			s.Movement.StageModernClearance(u, q.Head(), route)
		}
		forbidden = append(forbidden, destination)
	}
}

func (s *Service) factoryYieldEligible(factory, u *units.Unit) bool {
	if u == nil || !u.Alive || u.Dying || u.Def == nil || u.Owner != factory.Owner || u.Remaining != 0 || u.Attachment.Carrier != 0 || u.Def.BMCode != 1 || !u.Def.CanMove || u.Def.CanFly || u.Def.MaxVelocity <= 0 || !s.Movement.HasMover(u.Handle) || u.ParalyzeExpire != 0 {
		return false
	}
	if (u.Flags>>units.StandingMoveShift)&units.StandingFieldMask == 0 || u.Move.Mode != 1 || u.Move.Speed != 0 || u.Move.VelX != 0 || u.Move.VelY != 0 || u.Move.VelZ != 0 || s.Movement.HasPathRequest(u.Handle) {
		return false
	}
	if int(u.Handle) < len(s.Movement.Routes) {
		if r := s.Movement.Routes[u.Handle]; r != nil && (r.Active || r.WantsRepath) {
			return false
		}
	}
	q := orders.QueueOfUnit(u)
	if q == nil {
		return true
	}
	if q.LenSecondary() != 0 || q.LenPrimary() > 1 {
		return false
	}
	head := q.Head()
	return head == nil || (head.Flags&orders.FlagAutoOp != 0 && head.ID == orders.Lookup("Standby"))
}

func yieldOverlap(a, b world.FootprintRect) bool {
	return a.MinX() < b.MaxX() && b.MinX() < a.MaxX() && a.MinZ() < b.MaxZ() && b.MinZ() < a.MaxZ()
}

func (s *Service) factoryYieldDestination(u *units.Unit, forbidden []world.FootprintRect, urgent bool) (world.FootprintRect, []movement.Cell, bool) {
	start, fx, fz, ok := s.Movement.CommittedFootprint(u.Handle)
	if !ok || fx <= 0 || fz <= 0 {
		return world.FootprintRect{}, nil, false
	}
	extent, err := world.NewFootprintExtent(int32(fx), int32(fz))
	if err != nil {
		return world.FootprintRect{}, nil, false
	}
	const width = 2*factoryYieldRadius + 1
	var visited [width * width]bool
	var queue [width * width]movement.Cell
	var parents [width * width]int
	var depths [width * width]int
	queue[0] = start
	visited[factoryYieldRadius*width+factoryYieldRadius] = true
	tail := 1
	profile := s.Movement.ProfileFor(u.Handle)
	directions := [...]movement.Cell{{X: 0, Z: -1}, {X: -1, Z: 0}, {X: 0, Z: 1}, {X: 1, Z: 0}}
	for head := 0; head < tail && head < factoryYieldBudget; head++ {
		cell := queue[head]
		anchor := world.NewFootprintAnchor(cell.X, cell.Z)
		rect, err := world.NewFootprintRect(anchor, extent)
		if err != nil {
			continue
		}
		if head != 0 {
			if !s.Movement.IsGoalCellPassable(u.Handle, path.Cell{X: cell.X, Z: cell.Z}) || !s.factoryYieldClear(u.Handle, rect, profile) {
				continue
			}
			legal := true
			for _, other := range forbidden {
				if yieldOverlap(rect, other) {
					legal = false
					break
				}
			}
			if legal {
				var route []movement.Cell
				if urgent {
					route = make([]movement.Cell, depths[head]+1)
					for i, at := len(route)-1, head; i >= 0; i, at = i-1, parents[at] {
						route[i] = queue[at]
					}
				}
				return rect, route, true
			}
		}
		// Preserve every bend in the priority route within the follower's
		// existing 20-point capacity; never truncate a route before clearance.
		if urgent && depths[head] >= 19 {
			continue
		}
		for _, delta := range directions {
			next := movement.Cell{X: cell.X + delta.X, Z: cell.Z + delta.Z}
			dx, dz := next.X-start.X+factoryYieldRadius, next.Z-start.Z+factoryYieldRadius
			if dx < 0 || dz < 0 || dx >= width || dz >= width {
				continue
			}
			index := dz*width + dx
			if visited[index] {
				continue
			}
			visited[index] = true
			queue[tail] = next
			parents[tail] = head
			depths[tail] = depths[head] + 1
			tail++
		}
	}
	return world.FootprintRect{}, nil, false
}

func (s *Service) factoryYieldClear(self pool.Handle, rect world.FootprintRect, profile movement.Profile) bool {
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			if !profile.IsPassableCommitCell(s.Terrain, x, z) {
				return false
			}
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil || (cell.OccupantA() != 0 && pool.Handle(cell.OccupantA()) != self) {
				return false
			}
			if id, held := s.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z}); held && id != 0 && pool.Handle(id) != self {
				return false
			}
		}
	}
	return true
}
