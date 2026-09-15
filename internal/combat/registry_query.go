package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TargetsInRadius enumerates the cached target registry for Wait and the
// stationary guard [04 R-SPEC-01 §8][06 §3.1]. Entries retain registry order;
// only distance and current liveness are checked. Secondary contacts are used
// only when the primary query is empty and the owner's upgrade gate is open.
// The returned slice belongs to the caller. This query consumes no RNG.
func (s *Service) TargetsInRadius(owner uint8, x, z numeric.Fixed, radius int32, w *units.World) []pool.Handle {
	if s == nil || w == nil || owner >= combatPlayerSlots {
		return nil
	}
	var out []pool.Handle
	appendInRange := func(list []pool.Handle) {
		for _, h := range list {
			u := w.Unit(h)
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			// The shared squared metric takes signed-32 coordinate deltas,
			// squares before truncating each axis, and adds at 32 bits. The
			// radius product and comparison are signed-32 too [06 §3.1].
			dx := numeric.Fixed(int32(x - u.X))
			dz := numeric.Fixed(int32(z - u.Z))
			if int32(rngBoundForCandidate(dx, dz)) <= radius*radius {
				out = append(out, h)
			}
		}
	}
	appendInRange(s.targets.primaryList(owner))
	if len(out) == 0 && s.targetingUpgradeGateFor(owner) {
		appendInRange(s.targets.secondaryList(owner))
	}
	return out
}
