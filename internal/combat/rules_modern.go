package combat

import "github.com/nanolathe-gg/nanolathe/internal/units"

// ModernRules is the approved Nanolathe Modern rule set of the combat service:
// the terrain preflight of docs/DESIGN_WEAPONS_PROJECTILES.md §2.3.1 and the
// Hold Fire suppression of §2.6.1, and the threat/incoming-fire prototype.
// These are user-authorized departures from the
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
	q.Covered, q.Blocked = false, false
	if q.Service != nil && q.World != nil && q.Target != nil && q.Shooter != nil {
		preview := Projectile{Shooter: q.Shooter.Handle, ShooterSide: q.Shooter.Owner}
		if modernReliableWeapon(q.Launch.Weapon) {
			InitOrdinary(&preview, q.Launch.Weapon, q.Tick, q.Muzzle, q.Aim, q.Target.Handle)
			eta := q.Service.proposedShotWindow(preview, q.Launch.Weapon, q.Target, q.World, q.Terrain, q.Tick)
			if eta > 0 && q.Service.incomingDamage(q.Shooter, q.Target, q.World, q.Terrain, q.Tick, eta) >= int64(q.Target.Health) && q.Target.Health > 0 {
				q.Covered = true
				return false
			}
		}
	}
	q.Blocked = modernTerrainAdmission(q.Launch, q.Muzzle, q.Aim, q.Tick, q.Terrain, q.Target, q.Wind) == terrainShotBlocked
	return !q.Blocked
}

// HoldsFire suppresses AUTONOMOUS weapon work for a shooter whose standing fire
// field is zero, and never ordered work: Hold Fire stops a unit firing on its
// own, not firing when told to. A slot an order holds aims and launches
// through the ordinary reload, aim, resource and ammunition gates, which is
// also what retail does with it [04 R-STANCE-01 §3]; what Modern adds is the
// suppression of every slot the unit still owns itself.
//
// It adds no liveness or generation check to retail shooter references: a
// stale reference answers from the flags word it finds, exactly as the retail
// readers do.
// Nanolathe Modern policy: docs/DESIGN_WEAPONS_PROJECTILES.md §2.6.1.
func (*ModernRules) HoldsFire(u *units.Unit, ordered bool) bool {
	return !ordered && u != nil && u.Flags>>units.StandingFireShift&units.StandingFieldMask == 0
}

// previewsShot asks the spawner for the speculative spread of shotPreviewer:
// the §2.3.1 preview reads the spread-adjusted angles, and a refusal must
// leave both the slot and the shared stream untouched.
func (*ModernRules) previewsShot() bool { return true }
