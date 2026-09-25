package session

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// GroundPointAt gives a map-pixel ground position its terrain height, or sea
// level where the terrain has no height sample (its out-of-range sentinel).
// It is the megamap pointer's world point, which deliberately does not search
// along the half-height shear the way CursorToWorld does
// ([draw-engine-interface "Map scale"](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap))
// (DESIGN_INTERFACE_HUD_INPUT §3.15). The query is read-only [I6].
func (s *Session) GroundPointAt(x, z int32) (numeric.Fixed, numeric.Fixed, numeric.Fixed, bool) {
	if s == nil || s.World == nil {
		return 0, 0, 0, false
	}
	wx, wz := numeric.Fixed(x)<<16, numeric.Fixed(z)<<16
	h := s.World.HeightAt(wx, wz)
	if h < 0 {
		h = s.World.SeaLevelWorld()
	} else {
		h = h >> 16 << 16
	}
	return wx, h, wz, true
}
