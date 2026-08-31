package movement

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/pool"
)

// RetailMoverImage returns the detached 35-byte u%04xmob image. Route,
// follower, proposal, and last-proposal state are intentionally absent from
// the retail save boundary [08 R-SAVE-02 §8].
func (s *System) RetailMoverImage(h pool.Handle) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("movement: retail save mover: nil system")
	}
	c := s.Collisions[h]
	if c == nil {
		return nil, fmt.Errorf("movement: retail save mover: unit %d has no collision state", h)
	}
	data := make([]byte, 35)
	values := [...]int32{c.VX, c.VY, c.VZ, c.LeanX, c.LeanY, c.LeanZ, c.Speed}
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], uint32(value))
	}
	binary.LittleEndian.PutUint16(data[28:], uint16(c.TurnResidual))
	binary.LittleEndian.PutUint32(data[30:], c.LastStampTick)
	// Bits 3..7 are writer scratch. SavedStateByte retains them after restore;
	// fresh movers have deterministic zero scratch while named mode/blocked
	// state always comes from the live fields [08 R-SAVE-02 §8].
	state := c.SavedStateByte &^ 7
	state |= c.Mode & 3
	if c.Blocked {
		state |= 4
	}
	data[34] = state
	return data, nil
}
