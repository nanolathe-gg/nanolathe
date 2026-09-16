// Test-only seams over production combat entries.
//
// Each wrapper below has no shipped caller: the engine reaches the same
// production body by another route, and these forms exist so a fixture can ask
// the question directly. They live in a test file so production source carries
// one entry per contract.

package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// InitProjectile reconstructs a projectile event using the event creator
// ladder [06 §6.2] C15. Live unit fire carries its executor-selected family.
// Returns the creation family used; nil weapon returns CreationNone.
// slotDistance and gravity are the ballistic creator's two extra operands, and
// dropperHeading/dropperSpeed are the dropped creator's two; each pair is
// ignored by every other family [06 §6.4].
func InitProjectile(p *Projectile, w *content.WeaponDef, now uint32, muzzle, target Vec3, targetUnit pool.Handle, yaw, pitch numeric.Angle, meteorVel *Vec3, slotDistance int32, gravity numeric.Fixed, dropperHeading numeric.Angle, dropperSpeed numeric.Fixed) CreationFamily {
	fam := CreationFamilyForWeapon(w)
	initProjectileFamily(fam, p, w, now, muzzle, target, targetUnit, yaw, pitch, meteorVel, slotDistance, gravity, dropperHeading, dropperSpeed)
	return fam
}

// FindInterceptorTarget scans the current packed projectile prefix in increasing
// order for the first unclaimed enemy-owned targetable record whose stored
// aim point lies within the inclusive coverage square [06 §11.2] C29.
//
// Criteria per [06 §11.2]:
//   - nonzero slot ammunition is checked by caller (interceptor slot must have ammo) [06 §11.2]
//   - owner byte differs (alliance not consulted) [06 §11.2]
//   - candidate's weapon is targetable [06 §11.2]
//   - stored X and Z coordinates each inside inclusive coverage bounds [06 §11.2]
//   - not already referenced by any projectile's reservation link [06 §11.2]
//
// The slot store written at acquisition packs the candidate's current position
// into the interceptor unit's fixed target words [06 §11.2]; rescanning immediately
// before firing and storing the authoritative reservation link at spawn is the
// vertical-launch executor's, through TryFire's InterceptorRescan port
// [06 §11.2][06 §6.6].
//
// Returns the candidate handle, its current position (to be stored in slot's
// fixed target words), and whether a candidate was found.
// Determinism: prefix ascending, first unclaimed wins, not nearest [06 §11.2] I1.
func FindInterceptorTarget(svc *Service, interceptorPos Vec3, interceptorSide uint8, coverage int32, weapons map[int32]*content.WeaponDef) (pool.Handle, Vec3, bool) {
	return findInterceptorTarget(svc, interceptorPos, interceptorSide, coverage, func(id int32) (*content.WeaponDef, bool) {
		w, ok := weapons[id]
		return w, ok
	})
}

// AcquireTarget queries the cached registry, chooses a primary/secondary
// population, then samples and scores each pick in order [06 §3.1][06 §3.2].
// Service materialization owns the live/death check. This query preserves the
// registry order and does not re-test hostility or visibility after rebuild.
func AcquireTarget(candidates []Candidate, a Acquisition) (pool.Handle, bool) {
	filtered := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if WithinRange(a.ShooterX, a.ShooterZ, c.X, c.Z, a.Range) {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 && a.HasUpgrade {
		for _, c := range a.Secondary {
			if WithinRange(a.ShooterX, a.ShooterZ, c.X, c.Z, a.Range) {
				filtered = append(filtered, c)
			}
		}
	}
	return acquireFilteredTarget(filtered, a)
}

// directlyVisible is the primary list's direct-visibility predicate
// [06 §3.1] P0-10 [03 §3.2] P0-11. It accepts own-side units, rejects cloaked units, rejects
// underwater units without their dedicated status bit 0x200, and samples multiple
// target-bounds points via Visible (4-point hull) [03 §3.2] P0-11.
//
// Acquisition does NOT call it: the registry rebuild owns this predicate now
// (directlyVisibleAtRebuild in service.go is the same three clauses plus the
// same probe, for an observing player rather than a built candidate record).
// The one caller left is IsValidAcquisitionCandidate, which the damage path's
// reaction offer asks about a single candidate it did not get from a list
// [06 R-WPN-04 §2 part 3].
func (a *Acquisition) directlyVisible(c Candidate) bool {
	if c.OwnSide {
		return true // accepted outright, before any other test [06 §3.1] P0-10
	}
	if c.Cloaked {
		return false // cloaked reject [06 §3.1] P0-10 P0-11
	}
	if c.Underwater && !c.UnderwaterSeen {
		return false // underwater without 0x200 alias reject [06 §3.1] P0-10 P0-11
	}
	if a.Visible == nil {
		return false // hostile acquisition requires the direct-visibility predicate [06 §3.1]
	}
	return a.Visible(c) // 4-point hull sampling [03 §3.2] P0-11
}

// IsValidAcquisitionCandidate reports whether a candidate passes the primary
// list gate and acquisition-time physical admission for the given slot
// [06 §3.1] [06 §3.3] P0-10. It is the same pair of predicates AcquireTarget filters
// on, exposed for callers that want to test one candidate.
func IsValidAcquisitionCandidate(c Candidate, a Acquisition) bool {
	if !c.Hostile || !a.directlyVisible(c) {
		return false
	}
	return a.admits(c)
}
