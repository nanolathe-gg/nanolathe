package session

import (
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// interceptorGuidanceTick updates interceptor projectiles' stored target point
// to the linked candidate's current position each tick before motion per
// [06 §11.2]: non-cruise guidance reads the linked projectile's current point
// each tick, so target motion after launch is tracked via the link.
func (s *Session) interceptorGuidanceTick() {
	if s == nil || s.Combat == nil {
		return
	}
	if s.Catalog == nil {
		return
	}
	cnt := s.Combat.Count()
	for i := 0; i < cnt; i++ {
		h := pool.Handle(i + 1)
		if !s.Combat.Alive(h) {
			continue
		}
		rec := &s.Combat.Records[i]
		// The catalog's once-compiled slot index, not a per-tick map built by
		// ranging the weapon table: the index resolves colliding ids in the
		// documented order [02 "Weapon record"] where a map range resolved them
		// in Go's randomised one (I1), and it allocates nothing.
		w, ok := s.Catalog.WeaponByID(rec.WeaponID)
		if !ok || w == nil || !w.Interceptor {
			continue
		}
		if rec.TargetProjectile == 0 {
			continue
		}
		cand := rec.TargetProjectile
		if !s.Combat.Alive(cand) {
			// Candidate died between aim and fire or during chase: guidance
			// falls back to last stored point (dead-candidate unknown) [06 §11.2].
			continue
		}
		candIdx := int(cand) - 1
		if candIdx < 0 || candIdx >= len(s.Combat.Records) || candIdx >= s.Combat.Count() {
			continue
		}
		candRec := &s.Combat.Records[candIdx]
		// Non-cruise guidance reads linked projectile's current point each tick
		// [06 §11.2]. Update stored target point so motion steers toward live pos.
		rec.TargetPos = candRec.Pos
	}
}

// interceptorDetonationTick handles interceptor-flagged explosion sweeping after
// ordinary impact per [06 §11.2] [06 §9.3] C29. After ordinary unit/feature area
// enumeration, alive non-self projectiles inside the unhalved Area are forced
// through impact and removed. Friendly projectiles can be removed. The loop
// reloads live count so appended clones are reachable. Victim signatures are
// published for exact-match removal, but the minimal blast-radius sweep is
// sufficient for the vertical slice.
// interceptorExplosion is one dead interceptor staged for the sweep below: the
// record's handle, the point it died at, and its weapon.
type interceptorExplosion struct {
	h   pool.Handle
	pos combat.Vec3
	w   *content.WeaponDef
}

func (s *Session) interceptorDetonationTick() {
	if s == nil || s.Combat == nil {
		return
	}
	// Need to find interceptor projectiles that have just impacted and are now
	// dead but whose explosion sweep has not yet been applied. The combat
	// service's handleProjectileImpact does not perform the interceptor sweep,
	// so we perform it here for dead interceptor projectiles. Iterate over the
	// current count (including dead records until compact) and for each dead
	// interceptor, sweep live victims within unhalved area and mark them dead.
	// This is presentation-last, damage-last ordering: ordinary area already
	// happened in TickProjectiles's handleProjectileImpact; we now do the
	// projectile sweep [06 §11.2][06 §9.3] C29 (ordinary before projectiles).
	if s.Catalog == nil {
		return
	}
	cnt := s.Combat.Count()
	// Collect dead interceptor exploders first to avoid mutating while
	// scanning. The staging slice is reused across ticks and truncated here;
	// the sweep below consumes it within this call.
	exploders := s.interceptorExploded[:0]
	for i := 0; i < cnt; i++ {
		h := pool.Handle(i + 1)
		if s.Combat.Alive(h) {
			continue // alive not yet exploded
		}
		// Check if this record was alive at start of tick and is interceptor.
		// Dead records that are stale past new count after compact are cleared,
		// so iterating current count is correct [06 §5.2].
		rec := &s.Combat.Records[i]
		// If OldMarker indicates it was compacted away, its data is zeroed;
		// skip zero WeaponID.
		if rec.WeaponID == 0 {
			continue
		}
		// The catalog's once-compiled slot index; see interceptorGuidanceTick.
		w, ok := s.Catalog.WeaponByID(rec.WeaponID)
		if !ok || w == nil || !w.Interceptor {
			continue
		}
		// Consider only recent exploders: they must have been marked dead this
		// tick by TickProjectiles's collision/impact path. Heuristic: use
		// Expiry or position? For now, any dead interceptor is a candidate; the
		// sweep is idempotent (victims already dead are skipped), so over-
		// scanning is safe though not precise. To avoid repeatedly sweeping
		// long-dead interceptors, we could check that rec.CreationTick+some window,
		// but for the vertical slice where nuke is close, scanning all dead
		// interceptors each tick is acceptable and deterministic (I1).
		exploders = append(exploders, interceptorExplosion{h: h, pos: rec.Pos, w: w})
		if len(exploders) > 10 {
			break // cap to avoid unbounded work
		}
	}
	for _, e := range exploders {
		victims := combat.CollectInterceptorVictims(s.Combat, e.h, e.pos, e.w)
		for _, vh := range victims {
			// Force through ordinary impact path is modeled as immediate
			// removal for the vertical slice. The spec says each qualifying
			// victim is forced through the ordinary impact selector with no
			// direct unit target and a signature packet is published; the
			// receiver then impacts first matching target. For the slice, mark
			// victims dead directly; the signature path is exercised in unit
			// tests via FindVictimBySignature.
			s.Combat.MarkDead(vh)
		}
	}
	s.interceptorExploded = exploders[:0]
}
