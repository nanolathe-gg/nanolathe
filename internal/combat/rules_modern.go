package combat

import "github.com/nanolathe-gg/nanolathe/internal/units"

// ModernRules is the approved Nanolathe Modern rule set of the combat service:
// the terrain preflight of docs/DESIGN_WEAPONS_PROJECTILES.md §2.3.1 and the
// Hold Fire suppression of §2.6.1. Both are user-authorized departures from the
// retail firing pipeline, not parity defects [I11].
//
// It carries no state, so one value serves a whole session. It is used by
// pointer so a rule set may embed it and override a single answer without
// copying the policy.
type ModernRules struct{}

// AdmitShot runs the §2.3.1 preview and records the verdict on the query. The
// preview uses the actual launch and motion kernels, so the answer is the one
// the real shot would produce for the geometry known this tick; an exhausted
// work budget or unpreviewable family admits, because incomplete prediction
// proves nothing.
func (*ModernRules) AdmitShot(q *ShotQuery) bool {
	if q == nil {
		return true
	}
	q.Blocked = modernTerrainAdmission(q.Launch, q.Muzzle, q.Aim, q.Tick, q.Terrain, q.Target, q.Wind) == terrainShotBlocked
	return !q.Blocked
}

// HoldsFire suppresses new weapon work for a shooter whose standing fire field
// is zero. It adds no liveness or generation check to retail shooter
// references: a stale reference answers from the flags word it finds, exactly
// as the retail readers do.
func (*ModernRules) HoldsFire(u *units.Unit) bool {
	return u != nil && u.Flags>>units.StandingFireShift&units.StandingFieldMask == 0
}

// previewsShot asks the spawner for the speculative spread of shotPreviewer:
// the §2.3.1 preview reads the spread-adjusted angles, and a refusal must
// leave both the slot and the shared stream untouched.
func (*ModernRules) previewsShot() bool { return true }
