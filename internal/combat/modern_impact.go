package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern danger response".
// A victim can observe an impact without seeing its source. Session decides
// whether that observation carries a visible contact or only the hit bearing.
func (*ModernRules) ObserveImpact(s *Service, victim, attacker *units.Unit, in DamageInput, tick uint32) {
	if s == nil || s.ImpactNotice == nil || victim == nil || attacker == nil ||
		victim == attacker || !victim.Alive || victim.Dying || victim.Def == nil || attacker.Def == nil ||
		victim.Owner == attacker.Owner || s.Reaction == nil || s.Reaction.Allied == nil ||
		s.Reaction.Allied(victim.Owner, attacker.Owner) {
		return
	}
	// Reverse the observed incoming motion, not the post-motion impact point:
	// a direct contact can land past the victim's center. Without a horizontal
	// motion cue, use the packet's quantized direction toward the impact.
	bearing := numeric.Angle((uint16(in.Direction) << 8) + victim.Move.Heading)
	if in.ImpactVelocityX != 0 || in.ImpactVelocityZ != 0 {
		bearing = numeric.AngleFromAtan2(-in.ImpactVelocityX.Raw(), -in.ImpactVelocityZ.Raw())
	}
	s.ImpactNotice(victim, attacker, bearing, tick)
}
