package movement

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
)

// RetailOrderPayload projects the record's retained object, whether bound or
// displaced. Staged bytes on the node are not live state [08 R-SAVE-02 §6, §10]
// [04 R-ORD-01 §9]. Empty and explicitly released records have no subtype.
func (s *System) RetailOrderPayload(n *orders.Node) (orders.RetailOrderPayload, error) {
	if s == nil || n == nil {
		return orders.RetailOrderPayload{}, nil
	}
	for _, owned := range handleRow(s.recordGoals, n.Owner) {
		if owned.node != n {
			continue
		}
		out := orders.RetailOrderPayload{}
		put16 := func(off int, v uint16) { binary.LittleEndian.PutUint16(out.Data[off:], v) }
		put32 := func(off int, v uint32) { binary.LittleEndian.PutUint32(out.Data[off:], v) }
		switch m := owned.air.(type) {
		case *airMarker:
			out.Code, out.Data, out.UnitA, out.UnitB = 2, make([]byte, 0x36), n.Owner, m.target
			put16(0x1c, m.flags)
			put16(0x1e, m.radius)
			put16(0x20, uint16(m.altOffset))
			put16(0x22, m.heading)
			put16(0x24, m.attachPiece)
			put32(0x26, uint32(m.goal.X))
			put32(0x2a, uint32(m.goal.Y))
			put32(0x2e, uint32(m.goal.Z))
			put32(0x32, uint32(m.radial))
		case *airVelocityMarker:
			out.Code, out.Data, out.UnitA = 3, make([]byte, 0x2a), n.Owner
			flags := m.savedFlags &^ 1
			if m.steer {
				flags |= 1
			}
			put16(0xa, flags)
			put16(0x24, m.savedAux)
			put16(0x26, m.commanded)
			put16(0x28, m.savedTrailing)
			for i, v := range []uint32{uint32(m.pos.X), uint32(m.pos.Y), uint32(m.pos.Z), uint32(m.vel.X), uint32(m.vel.Y), uint32(m.vel.Z)} {
				put32(0xc+4*i, v)
			}
		case nil:
			if owned.ground == nil || owned.ground.goal == nil {
				return out, nil
			} // derived steering points have no separate saved object
			code, words, ok := path.RetailGoalWords(owned.ground.goal)
			if !ok {
				return out, fmt.Errorf("nanolathe: retail goal save failed: logical path save/Units/u%04x, providers searched [movement], expected supported ground goal", n.Owner)
			}
			out.Code, out.Data = code, make([]byte, 4+4*len(words))
			for i, v := range words {
				put32(4+i*4, v)
			}
		default:
			return out, fmt.Errorf("nanolathe: retail goal save failed: logical path save/Units/u%04x, providers searched [movement], expected supported air goal", n.Owner)
		}
		// Leaked native prefixes and embedded link storage are discarded on load;
		// the host writer uses zero scratch, never executable pointer bytes.
		return out, nil
	}
	return orders.RetailOrderPayload{}, nil
}
