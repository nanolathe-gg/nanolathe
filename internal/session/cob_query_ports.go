package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// bindQueryPorts installs engine ports 7 through 15 — the QUERY half of the
// twenty-wide port switch [04 §4.4][04 R-COB-03 §2].
//
// Every one of them is a pure read of state that already exists: a piece world
// point, another unit's position or definition height, or arithmetic over the
// packed coordinate word. None carries per-unit state of its own, which is why
// they are bound here beside port 16 rather than in the instance port table
// internal/units builds for the six state-bearing write ports (1/5/6/18/19/20)
// — those need the D+wake ordering guarantee relative to Create and these do
// not.
//
// Until this binding existed the VM's readPortDefault answered ZERO for all
// nine. Zero is a meaningful and wrong answer to every one of them: a packed
// (0,0) position, a distance of nothing, a bearing of due north. In the retail
// script corpus the readers are the four transports — the ARM and CORE hover
// and sea transports — which read ports 7, 8, 9, 10, 11 and 13 to place their
// cargo; with the ports unbound they placed it at the world origin.
func (s *Session) bindQueryPorts(binding *cob.Binding, u *units.Unit) {
	if s == nil || binding == nil || binding.VM == nil || u == nil {
		return
	}
	vm := binding.VM

	// Ports 7 and 8 — the piece world point. The locator returns the world
	// OFFSET (x, y, −z) and every consumer adds it to the unit position with no
	// further sign change [03 R-RAST-01 §8]. An out-of-range piece index, or a
	// unit with no render table, contributes a zero offset, so the port answers
	// the unit's own position [04 §4.4]; that is exactly what a declined
	// ComposePiece means here, so the decline is a fallback rather than an
	// error.
	pieceWorld := cob.PieceWorldPoint(func(piece int32) [3]numeric.Fixed {
		unitPos := [3]numeric.Fixed{u.X, u.Y, u.Z}
		if piece < 0 {
			return unitPos // zero offset [04 §4.4]
		}
		offset, ok := binding.ComposePiece(int(piece), u.Move.Heading, u.Move.Pitch, u.Move.Bank)
		if !ok {
			return unitPos // out-of-range index or no render table [04 §4.4]
		}
		return [3]numeric.Fixed{
			u.X.Add(offset[0]),
			u.Y.Add(offset[1]),
			u.Z.Add(offset[2]),
		}
	})
	vm.BindPort(cob.Port(7), cob.PiecePositionXZPortFunc(pieceWorld))
	vm.BindPort(cob.Port(8), cob.PiecePositionYPortFunc(pieceWorld))

	// Ports 9, 10 and 11 — another unit, selected by the identifier in the
	// first argument slot. The identifier is masked to sixteen bits; a zero
	// identifier, or a slot whose alive bit is clear, reads zero [04 §4.4].
	//
	// Retail applies NO upper-bound check against the pool capacity, so an
	// identifier past the end of the pool is an out-of-bounds read whose
	// admission depends on whatever the alive bit reads at that address —
	// undefined behavior, not a defined value [04 §4.4]. units.World.Unit
	// rejects such a handle instead, which is the bounds-check exception of
	// [I11]: rejecting data retail would (unpredictably) accept. No shipped
	// script reaches it — the six readers all pass an identifier the engine
	// handed them.
	lookup := cob.UnitPortLookup(func(id int32) ([3]numeric.Fixed, int32, bool) {
		masked := uint16(id) // masked to sixteen bits [04 R-COB-03 §2]
		if masked == 0 {
			return [3]numeric.Fixed{}, 0, false // zero identifier reads zero [04 §4.4]
		}
		target := s.Units.Unit(pool.Handle(masked)) // alive gate [04 R-COB-03 §2]
		if target == nil {
			return [3]numeric.Fixed{}, 0, false
		}
		var height int32
		if target.Def != nil {
			// Port 11's "definition height" is the definition's model
			// bounding-box maximum Y in 16.16 [04 R-MOV-03 §5] — the same word
			// the transport hand-off argument carries and the repair water
			// clause compares against sea level.
			height = target.Def.ModelTopFixed
		}
		return [3]numeric.Fixed{target.X, target.Y, target.Z}, height, true
	})
	vm.BindPort(cob.Port(9), cob.UnitPositionXZPortFunc(lookup))
	vm.BindPort(cob.Port(10), cob.UnitPositionYPortFunc(lookup))
	vm.BindPort(cob.Port(11), cob.UnitHeightPortFunc(lookup))

	// Ports 12 to 15 — the trig arm. Port 12 is the only one of the four that
	// reads unit state, and it reads it at CALL time: the heading moves every
	// tick and the port subtracts the heading the reading unit carries now
	// [04 §4.4].
	vm.BindPort(cob.Port(12), cob.RelativeBearingPortFunc(func() uint16 { return u.Move.Heading }))
	vm.BindPort(cob.Port(13), cob.DistancePortFunc())
	vm.BindPort(cob.Port(14), cob.AtanPortFunc())
	vm.BindPort(cob.Port(15), cob.HypotPortFunc())
}
