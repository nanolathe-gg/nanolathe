package movement

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/pool"
)

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

// RestoreOccupancy re-anchors a mover using the normal clear/stamp paths.
// Raw grid writes are intentionally unavailable to the restore caller
// [08 R-SAVE-02 §11].
func (s *System) RestoreOccupancy(h pool.Handle, anchorX, anchorZ int16) error {
	if s == nil || s.Grid == nil {
		return fmt.Errorf("movement: retail restore occupancy: no grid")
	}
	c := s.Collisions[h]
	if c == nil {
		return fmt.Errorf("movement: retail restore occupancy: unit %d has no collision state", h)
	}
	s.Grid.Clear(c.CachedAnchor, c.FootPrintX, c.FootPrintZ, c.ID)
	anchor := Cell{X: int32(anchorX), Z: int32(anchorZ)}
	var stamped bool
	if c.Building {
		stamped = s.stampBuildingGrid(anchor, c.FootPrintX, c.FootPrintZ, c.Yard, c.YardOpen, c.ID)
	} else {
		stamped = s.Grid.Stamp(anchor, c.FootPrintX, c.FootPrintZ, c.ID)
	}
	if !stamped {
		return fmt.Errorf("movement: retail restore occupancy: unit %d anchor (%d,%d) rejected", h, anchorX, anchorZ)
	}
	c.CachedAnchor, c.OldAnchor = anchor, anchor
	c.Dirty = false
	s.noteOccupancyCommit(h, c.LastStampTick)
	return nil
}
