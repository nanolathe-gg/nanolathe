package movement

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// RestoreHeadGoal installs a saved payload already held by the primary queue
// head. It is deliberately a follower operation: loading must not run an
// order handler, progress a queue, draw randomness, or invoke scripts [08
// R-SAVE-02 §10, §11]. A head without one of the five saved payload classes
// leaves the freshly-built follower unchanged.
func (s *System) RestoreHeadGoal(u *units.Unit) error {
	if s == nil || u == nil {
		return nil
	}
	q := orders.QueueOfUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return nil
	}
	head := q.Primary()[0]
	if head == nil || head.RetailSubtypeCode == 0 {
		return nil
	}
	word := func(i int) uint32 { return head.RetailSubtypeWords32[i] }
	cell := func(v uint32) (int32, int32) { return int32(int16(v)), int32(int16(v >> 16)) }
	switch head.RetailSubtypeCode {
	case 2:
		if len(head.RetailSubtypeWords16) != 5 || len(head.RetailSubtypeWords32) != 4 {
			return fmt.Errorf("movement: retail restore head: malformed air path marker")
		}
		m := &airMarker{
			sys: s, unit: u, target: head.RetailSubtypeUnitB,
			flags: head.RetailSubtypeWords16[0], radius: head.RetailSubtypeWords16[1],
			altOffset:   int16(head.RetailSubtypeWords16[2]),
			heading:     head.RetailSubtypeWords16[3],
			attachPiece: head.RetailSubtypeWords16[4],
			goal:        Vec3{X: numeric.Fixed(int64(int32(word(0)))), Y: numeric.Fixed(int64(int32(word(1)))), Z: numeric.Fixed(int64(int32(word(2))))},
			radial:      numeric.Fixed(int64(int32(word(3)))),
		}
		savedSatisfied := head.Satisfied
		s.installAirGoal(u, head, m)
		head.Satisfied = savedSatisfied
		st := s.airStateFor(u, head)
		st.order = head
		if c := s.FlightCommandFor(u.Handle, u); c != nil {
			c.Flags |= flightCommandDirty
		}
	case 3:
		if len(head.RetailSubtypeWords16) != 4 || len(head.RetailSubtypeWords32) != 6 {
			return fmt.Errorf("movement: retail restore head: malformed air velocity marker")
		}
		m := &airVelocityMarker{
			sys: s, unit: u,
			pos:       Vec3{X: numeric.Fixed(int64(int32(word(0)))), Y: numeric.Fixed(int64(int32(word(1)))), Z: numeric.Fixed(int64(int32(word(2))))},
			vel:       Vec3{X: numeric.Fixed(int64(int32(word(3)))), Y: numeric.Fixed(int64(int32(word(4)))), Z: numeric.Fixed(int64(int32(word(5))))},
			steer:     head.RetailSubtypeWords16[0]&1 != 0,
			commanded: head.RetailSubtypeWords16[2],
		}
		// TODO(question): code-3 retains a zeroed auxiliary u16 and a trailing
		// padding word after the saved steer flag and commanded heading. Their
		// runtime meaning is unknown; preserving the staged words avoids
		// assigning either a marker field without a reader.
		savedSatisfied := head.Satisfied
		s.installAirPayload(u, head, m)
		head.Satisfied = savedSatisfied
		if c := s.FlightCommandFor(u.Handle, u); c != nil {
			c.Flags |= flightCommandDirty
		}
	case 4:
		if len(head.RetailSubtypeWords32) != 3 {
			return fmt.Errorf("movement: retail restore head: malformed point goal")
		}
		x, z := cell(word(0))
		savedSatisfied := head.Satisfied
		if !s.installGroundPayload(u.Handle, head, path.PointGoalRestored(path.Cell{X: x, Z: z}, int32(word(1)), int32(word(2))), worldCellCenter(x), worldCellCenter(z)) {
			return fmt.Errorf("movement: retail restore head: point goal rejected")
		}
		head.Satisfied = savedSatisfied
	case 5:
		if len(head.RetailSubtypeWords32) != 5 {
			return fmt.Errorf("movement: retail restore head: malformed annulus goal")
		}
		x, z := cell(word(0))
		// Code 5 writes inner, outer, inner-squared, outer-squared in that
		// order. The path family takes the inward bound first [08 R-SAVE-02
		// §10][04 R-PATH-01 §9].
		savedSatisfied := head.Satisfied
		if !s.installGroundPayload(u.Handle, head, path.AnnulusGoalRestored(path.Cell{X: x, Z: z}, int32(word(1)), int32(word(2)), int32(word(3)), int32(word(4))), worldCellCenter(x), worldCellCenter(z)) {
			return fmt.Errorf("movement: retail restore head: annulus goal rejected")
		}
		head.Satisfied = savedSatisfied
	case 6:
		if len(head.RetailSubtypeWords32) != 4 {
			return fmt.Errorf("movement: retail restore head: malformed rectangle goal")
		}
		x1, x2, z1, z2 := int32(word(0)), int32(word(1)), int32(word(2)), int32(word(3))
		savedSatisfied := head.Satisfied
		if !s.installGroundPayload(u.Handle, head, path.RectPerimeterGoal(path.Rect{Min: path.Cell{X: x1, Z: z1}, Max: path.Cell{X: x2, Z: z2}}), worldCellCenter((x1+x2)/2), worldCellCenter(z2)) {
			return fmt.Errorf("movement: retail restore head: rectangle goal rejected")
		}
		head.Satisfied = savedSatisfied
	default:
		return fmt.Errorf("movement: retail restore head: unsupported subtype %d", head.RetailSubtypeCode)
	}
	return nil
}

// RestoreMover applies the fixed mover extension from a saved unit.  The
// route, follower, and proposal objects are derived state and are cleared;
// only the words the save format defines as live mover state are copied
// [08 R-SAVE-02 §§6-8].
func (s *System) RestoreMover(h pool.Handle, data []byte) error {
	if s == nil || s.world == nil {
		return fmt.Errorf("movement: retail restore mover: unbound world")
	}
	if len(data) != 35 {
		return fmt.Errorf("movement: retail restore mover: size %d, want 35", len(data))
	}
	u := s.world.Unit(h)
	if u == nil || !u.Alive {
		return fmt.Errorf("movement: retail restore mover: unit %d unavailable", h)
	}
	s.EnsureUnit(u)
	c := s.Collisions[h]
	if c == nil {
		return fmt.Errorf("movement: retail restore mover: unit %d has no collision state", h)
	}
	word := func(off int) int32 { return int32(binary.LittleEndian.Uint32(data[off : off+4])) }
	c.VX, c.VY, c.VZ = word(0), word(4), word(8)
	c.LeanX, c.LeanY, c.LeanZ = word(12), word(16), word(20)
	c.Speed = word(24)
	c.TurnResidual = int16(binary.LittleEndian.Uint16(data[28:]))
	c.LastStampTick = binary.LittleEndian.Uint32(data[30:])
	state := data[34]
	mode := state & 3
	blocked := state&4 != 0
	c.Mode, c.CachedMode, c.Blocked, c.SavedStateByte = mode, mode, blocked, state
	// The mover's mode mirror and the packed unit-status mirror are separate
	// save fields; the mover byte must not overwrite the unit's mirror.
	if fl := s.Flights[h]; fl != nil {
		fl.Mode = mode
		fl.VX, fl.VY, fl.VZ = c.VX, c.VY, c.VZ
		fl.Speed = c.Speed
		fl.TurnResidual = c.TurnResidual
		fl.LeanX, fl.LeanY, fl.LeanZ = c.LeanX, c.LeanY, c.LeanZ
		s.commitFlightState(u, fl)
	} else {
		// A ground mover has no FlightState commit. The velocity triple the save
		// carries is still live before the next mover tick, including the weapon
		// lead reader [06 §3.3][08 R-SAVE-02 §8].
		u.Move.VelX = numeric.Fixed(int64(c.VX))
		u.Move.VelY = numeric.Fixed(int64(c.VY))
		u.Move.VelZ = numeric.Fixed(int64(c.VZ))
	}
	if st := s.Steers[h]; st != nil {
		st.Speed = c.Speed
	}
	// The saved route/follower/proposal records are not present in a standard
	// battle save.  Ensure no pre-restore request or stale route survives.
	s.CancelPathRequest(h)
	s.Routes[h] = &Route{}
	if int(h) < len(s.sessions) {
		s.sessions[int(h)] = nil
	}
	delete(s.activeOrders, h)
	delete(s.moveGoals, h)
	delete(s.arrivalHandles, h)
	c.LastProposalTick = 0
	return nil
}

// RestoreOccupancy re-anchors a mover using the saved mover mode. The unit's
// packed status mirror is deliberately not consulted: it is a separate saved
// word, while the mover byte decides the occupancy plane [08 R-SAVE-02 §8].
func (s *System) RestoreOccupancy(h pool.Handle, anchorX, anchorZ int16) error {
	if s == nil || s.Grid == nil {
		return fmt.Errorf("movement: retail restore occupancy: no grid")
	}
	c := s.Collisions[h]
	if c == nil {
		return fmt.Errorf("movement: retail restore occupancy: unit %d has no collision state", h)
	}
	anchor := Cell{X: int32(anchorX), Z: int32(anchorZ)}
	if c.Building {
		s.clearBuildingGrid(c.CachedAnchor, c.FootPrintX, c.FootPrintZ, c.Yard, c.YardOpen, c.ID)
		s.stampBuildingGrid(anchor, c.FootPrintX, c.FootPrintZ, c.Yard, c.YardOpen, c.ID)
	} else {
		plane, stamps := planeForMode(c.Mode)
		if c.HasStamp {
			s.Grid.ClearPlane(c.StampedPlane, c.StampedAnchor, c.FootPrintX, c.FootPrintZ, c.ID)
			c.HasStamp = false
		}
		if stamps {
			s.Grid.StampPlane(plane, anchor, c.FootPrintX, c.FootPrintZ, c.ID)
			c.StampedAnchor, c.StampedPlane = anchor, plane
			c.HasStamp = s.Grid.RectOnMap(anchor, c.FootPrintX, c.FootPrintZ)
		}
	}
	c.CachedAnchor, c.OldAnchor = anchor, anchor
	c.CachedMode = c.Mode & 0x3
	c.Dirty = false
	s.noteOccupancyCommit(h, c.LastStampTick)
	if s.world != nil {
		s.syncStampedAirSector(s.world.Unit(h), c)
	}
	return nil
}
