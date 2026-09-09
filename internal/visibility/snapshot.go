package visibility

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// AudiblePoint answers the positional-audio audience gate for one world point
// [03 §8.3] "audience gating". It is the reduced one-point predicate of
// [03 §3.2] step 4 under the name the audio path asks for it by: each 16.16
// coordinate narrows to its signed map-pixel component, the point projects
// with the half-height shear (u = x>>5, v = (z - (y>>1))>>5), unsigned bounds
// reject an off-map point, and the mode word's bit 1 selects the local viewing
// player's explored byte grid or the LOS word mask at that player's bit alone
// — never an ally OR [03 §3.1].
//
// local is the local viewing player's slot; the audience gate has no other
// viewer. Height is part of the projection, so an aircraft or a hilltop weapon
// is gated at the cell it draws in, not at the ground cell beneath it.
//
// The answer is a scalar and nothing is copied. This replaces a GridSnapshot
// accessor that handed presentation a copy of the word mask and all ten player
// byte grids so the caller could read one cell out of them; the gate needs one
// cell, so the service answers with one bit and the grids stay private.
func (s *Service) AudiblePoint(local PlayerID, x, y, z numeric.Fixed) bool {
	return s.VisiblePoint(local, x, y, z)
}
