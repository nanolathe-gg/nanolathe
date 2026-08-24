// Package combat — stockpile and interceptors per [06 §11] C29.
//
// WU-09-9 owns stockpile.go. Other combat files own pool, slots, fire, etc.
// Determinism: stable pool order, no map iteration (I1), fixed-point 16.16 (I2),
// truncation toward zero where retail does (I3), pool.Projectiles is sole
// count/dead authority (I5).
package combat

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// ---------------------------------------------------------------------------
// Stockpile constants [06 §11.1]
// ---------------------------------------------------------------------------

// StockpileProgressStep is the per-visit progress advance, capped at build time [06 §11.1].
const StockpileProgressStep = 5 // [06 §11.1] each stockpile work visit advances per-node progress by five

// StockpileRetryRejected is the retry delay when two-resource admission is rejected [06 §11.1].
const StockpileRetryRejected = 10 // [06 §11.1] failed admission schedules a ten-tick retry deadline

// StockpileRetryAccepted is the retry delay when admission accepted but work incomplete [06 §11.1].
const StockpileRetryAccepted = 5 // [06 §11.1] accepted but incomplete work schedules a five-tick retry

// StockpileRetryBlocked is the wait when slot byte is already greater than 199 [06 §11.1].
const StockpileRetryBlocked = 300 // [06 §11.1] a new round is blocked with a 300-tick wait when the slot byte is already greater than 199

// StockpileMaxAmmo is the ordinary path limit for completed rounds [06 §11.1].
// Ordinary path can reach 200 but does not start a round beyond it.
const StockpileMaxAmmo = 200 // [06 §11.1]

// ---------------------------------------------------------------------------
// Stockpile queue state [06 §11.1]
// ---------------------------------------------------------------------------

// StockpileEntry models one secondary BUILDWEAPON queue node per [06 §11.1].
// Stockpile weapons use the secondary weapon-production queue;
// non-stockpile entries use the ordinary build queue [06 §11.1].
// Queue increments coalesce matching weapon/type entries at the tail;
// decrements reduce or unlink [06 §11.1] (orders.Queue.CoalesceTail).
// Two distinct values are kept: the linked node's signed requested count and
// the slot's byte-sized completed-round remainder [06 §11.1].
type StockpileEntry struct {
	Weapon   *content.WeaponDef // selected weapon; BuildTime is Weapon.ReloadTime [06 §11.1]
	Count    int32              // signed requested count [06 §11.1]
	Progress int32              // per-node progress 0..BuildTime, step 5 capped [06 §11.1]
}

// BuildTime returns the compiled reload-time used as stockpile build time [06 §11.1].
func StockpileBuildTime(w *content.WeaponDef) int32 {
	if w == nil {
		return 0
	}
	return w.ReloadTime // [02 "Weapon record"] reloadtime*30, used as build time per [06 §11.1]
}

// CanStartStockpileRound reports whether a new round may start given the slot's
// byte-sized completed-round remainder [06 §11.1].
// A new round is blocked when the slot byte is already greater than 199 [06 §11.1].
func CanStartStockpileRound(ammo int32) bool {
	return ammo <= 199 // [06 §11.1] slot byte >199 blocked; ordinary path can reach 200 but does not start beyond it
}

// StockpileCostDelta computes the admitted delta for one resource for this visit
// per [06 §11.1]:
//
//	energyDelta = trunc(next*energyCost/buildTime) - trunc(old*energyCost/buildTime)
//	metalDelta  = trunc(next*metalCost/buildTime) - trunc(old*metalCost/buildTime)
//
// Both are independently truncated toward zero [01 §8] I3 [06 §11.1].
// TODO(question): exact float truncation for fractional costs remains untraced beyond ordinary positive stock inputs; using truncate toward zero per [01 §8].
func StockpileCostDelta(oldProg, newProg int32, cost float64, buildTime int32) float32 {
	if buildTime <= 0 {
		// TODO(question): [06 §11.1] build time <=0 (zero authored reload) with cost>0: divide behavior untraced; return full cost as placeholder until probe.
		return float32(cost)
	}
	oldTrunc := int32(float64(oldProg) * cost / float64(buildTime)) // trunc toward zero [01 §8]
	newTrunc := int32(float64(newProg) * cost / float64(buildTime))
	return float32(newTrunc - oldTrunc) // delta per [06 §11.1]
}

// TickStockpile advances one stockpile work visit for entry and slot per [06 §11.1].
// It advances per-node progress by five, capped at build time, admits the delta pair
// through the ordinary two-resource helper (admit func), and handles retry, blocking,
// and completion.
//
// Contract per [06 §11.1]:
//   - each visit advances progress by 5 capped at buildTime [06 §11.1]
//   - admitted demands are independently truncated cumulative differences [06 §11.1]
//   - progress not advanced when admission rejected, but both requested amounts retained [06 §11.1]
//   - failed admission schedules 10-tick retry [06 §11.1]
//   - accepted but incomplete work schedules 5-tick retry [06 §11.1]
//   - completion increments slot byte, decrements signed queue count, requests interface refresh [06 §11.1]
//   - new round blocked with 300 wait when slot byte >199 [06 §11.1]
//   - ordinary path can reach 200 but does not start beyond it [06 §11.1]
//   - assets whose buildTime <=5 can complete multiple queued rounds in one visit while admission remains open [06 §11.1]
//   - launch is checked before production in the same unit slot, so a round completed by secondary queue cannot launch until next tick [06 §11.1] C29
//
// admit is the two-resource admission helper; it should return true when both
// carries are non-positive (both accepted), false otherwise [05 "Two-resource admission"].
// When admit is nil, admission is assumed always accepted (tests without economy).
//
// Returns nextTick (0 when no further work), refreshed (interface refresh requested), and completedRounds.
func TickStockpile(entry *StockpileEntry, slot *Slot, tick uint32, admit func(energy, metal float32) bool) (nextTick uint32, refreshed bool, completedRounds int) {
	if entry == nil || slot == nil || entry.Weapon == nil {
		return 0, false, 0
	}
	if entry.Count <= 0 {
		return 0, false, 0
	}
	w := entry.Weapon
	buildTime := StockpileBuildTime(w) // [06 §11.1]
	if buildTime <= 0 {
		// TODO(question): [06 §11.1] zero buildTime malformed: spec notes assets whose build time <=5 can multi-complete; zero would divide by zero in cost delta.
		// Placeholder: treat one step as one completion without progress, if admission succeeds.
		if !CanStartStockpileRound(slot.Ammo) {
			return tick + StockpileRetryBlocked, false, 0 // [06 §11.1] blocked >199
		}
		eCost := float32(w.EnergyPerShot)
		mCost := float32(w.MetalPerShot)
		// No proportional split; admit whole costs.
		accepted := true
		if admit != nil {
			accepted = admit(eCost, mCost)
		}
		if !accepted {
			return tick + StockpileRetryRejected, false, 0 // [06 §11.1] rejected 10
		}
		// Completion
		slot.Ammo++ // [06 §11.1] byte-sized completed rounds increment
		// Clamp to byte range 0..255? Retail byte wraps? Unknown, TODO(question) byte overflow or wrap for malformed preexisting values [06 §11.1] missing/unknown.
		if slot.Ammo > 255 {
			slot.Ammo = 255 // placeholder clamp
		}
		entry.Count-- // signed queue count decrement [06 §11.1]
		entry.Progress = 0
		refreshed = true // selected-unit interface refresh [06 §11.1]
		completedRounds = 1
		if entry.Count > 0 && buildTime <= 5 {
			// Continue loop for multi-completion in one visit while admission open [06 §11.1]
			// For zero buildTime, multi-completion mirrors <=5 case: keep consuming while queued.
			// Avoid infinite loop when admit always true: limit iterations to remaining count.
			for entry.Count > 0 && CanStartStockpileRound(slot.Ammo) {
				if admit != nil && !admit(eCost, mCost) {
					return tick + StockpileRetryRejected, true, completedRounds
				}
				slot.Ammo++
				if slot.Ammo > 255 {
					slot.Ammo = 255
				}
				entry.Count--
				completedRounds++
				// For zero buildTime, each iteration is one round; still need to break after one? Spec says <=5 can multi-complete, zero qualifies.
				// Continue while queued and admit open.
				if entry.Count == 0 {
					break
				}
			}
		}
		if entry.Count > 0 {
			if !CanStartStockpileRound(slot.Ammo) {
				return tick + StockpileRetryBlocked, true, completedRounds
			}
			return tick + StockpileRetryAccepted, true, completedRounds
		}
		return 0, true, completedRounds
	}
	// Normal path with buildTime >0
	completedRounds = 0
	refreshed = false
	// If slot blocked at entry, wait 300 [06 §11.1]
	if !CanStartStockpileRound(slot.Ammo) {
		return tick + StockpileRetryBlocked, false, 0 // [06 §11.1]
	}
	// Multi-completion loop for assets whose buildTime <=5 [06 §11.1]
	for {
		if entry.Count <= 0 {
			break
		}
		if !CanStartStockpileRound(slot.Ammo) {
			// Blocked after completing some rounds in this same visit
			if completedRounds > 0 {
				return tick + StockpileRetryBlocked, true, completedRounds
			}
			return tick + StockpileRetryBlocked, false, 0
		}
		oldProg := entry.Progress
		newProg := oldProg + StockpileProgressStep // [06 §11.1] advances by five
		if newProg > buildTime {
			newProg = buildTime // capped at buildTime [06 §11.1]
		}
		// Compute deltas per [06 §11.1] independently truncated
		eDelta := StockpileCostDelta(oldProg, newProg, w.EnergyPerShot, buildTime)
		mDelta := StockpileCostDelta(oldProg, newProg, w.MetalPerShot, buildTime)
		accepted := true
		if admit != nil {
			accepted = admit(eDelta, mDelta)
		}
		if !accepted {
			// Progress not advanced when admission rejected [06 §11.1]; retry 10
			// Counts retained, both requested amounts retained (caller retains admit amounts via economy buckets).
			if completedRounds > 0 {
				// Some rounds already completed in this visit before this rejection
				return tick + StockpileRetryRejected, true, completedRounds
			}
			return tick + StockpileRetryRejected, false, 0 // [06 §11.1]
		}
		// Admission accepted: advance progress
		entry.Progress = newProg
		if entry.Progress < buildTime {
			// Accepted but incomplete work schedules 5-tick retry [06 §11.1]
			if completedRounds > 0 {
				// Lost: we already completed some rounds in this same tick, but still incomplete for next round.
				// Spec says assets with buildTime <=5 can multi-complete; for larger buildTime, incomplete should schedule 5 and not loop.
				return tick + StockpileRetryAccepted, true, completedRounds
			}
			return tick + StockpileRetryAccepted, false, 0 // [06 §11.1]
		}
		// Completion: increment slot byte, decrement signed queue count, request refresh [06 §11.1]
		slot.Ammo++ // byte-sized remainder increment [06 §11.1]
		if slot.Ammo > 255 {
			slot.Ammo = 255 // TODO(question): byte overflow wrap untraced [06 §11.1] unknown
		}
		entry.Count--      // signed queue count decrement [06 §11.1]
		entry.Progress = 0 // new round progress resets
		completedRounds++
		refreshed = true // [06 §11.1] refresh helper itself does not mutate values and does not clamp [06 §11.1]
		if entry.Count == 0 {
			// Queue empty: no further retry
			break
		}
		// More queued rounds exist
		if buildTime <= 5 {
			// Assets whose build time <=5 can complete multiple queued rounds in one visit while admission remains open [06 §11.1]
			// Continue loop without returning, attempting next round immediately in same tick.
			// Admission remains open is checked at top of next iteration via admit result.
			continue
		}
		// For buildTime >5, completion waits for next tick even if more queued; schedule accepted-incomplete boundary? Spec suggests completed round does not immediately start next round in same tick unless buildTime <=5.
		// Return with accepted incomplete scheduling? Actually completed round just finished, next round not started; next work visit schedules 5? But spec says failed admission 10, accepted incomplete 5. Completed case not explicitly retried, but loop will start next round on next tick.
		// Return with 5? To match spec's "accepted but incomplete work schedules 5", after completion the next round is at 0, so incomplete? But completion already consumed one round. For >5 case, we stop after one completion and schedule next tick with 5? Conservative: return 5 to schedule next round's first step.
		return tick + StockpileRetryAccepted, true, completedRounds // schedule next round's work
	}
	if completedRounds > 0 {
		if entry.Count > 0 {
			// More rounds queued but not completed due to loop break (blocked or single completion with >5 buildTime)
			if !CanStartStockpileRound(slot.Ammo) {
				return tick + StockpileRetryBlocked, true, completedRounds
			}
			return tick + StockpileRetryAccepted, true, completedRounds
		}
		return 0, true, completedRounds
	}
	return 0, false, 0
}

// TryStockpileLaunch attempts a stockpile launch per [06 §11.1] [06 §4.2].
// Launch requires the weapon's stockpile flag and a nonzero slot remainder [06 §11.1].
// A successful spawner decrements the slot byte and requests the same selected-unit interface refresh [06 §11.1].
// An empty remainder prevents projectile allocation [06 §11.1].
// Stockpile launch bypasses the ordinary per-launch energy/metal debit; its resource cost belongs to production path [06 §11.1] [06 §4.2].
// Launch is checked before production in the same unit slot [06 §11.1] C29, so caller must invoke this before TickStockpile.
// Only successful spawn decrements ammunition [06 §11.1].
//
// This helper mirrors fire.TryFire's stockpile branch but is explicit for C29 lifecycle tests:
// it validates ammo >0, attempts svc.Reserve, on success decrements slot.Ammo, sets projectile fields minimally,
// and returns handle. On failure (empty ammo or pool full) it performs no ammo/reload/resource mutation
// matching the ordinary slot pipeline's failure path [06 §11.1] [06 §4.1] C4.
func TryStockpileLaunch(svc *Service, slot *Slot, slotIdx int, tgt Target, tick uint32) (pool.Handle, bool) {
	if svc == nil || slot == nil || slot.Weapon == nil {
		return 0, false
	}
	if !slot.Weapon.Stockpile {
		return 0, false // launch requires stockpile flag [06 §11.1]
	}
	if slot.Ammo <= 0 {
		return 0, false // empty remainder prevents allocation [06 §11.1]
	}
	// Pool-full check before allocation would fail; retain no ammo change [06 §11.1] [06 §4.1] C4.
	// Attempt reserve at tail [06 §5.1]
	h, ok := svc.Reserve()
	if !ok {
		return 0, false // no ammo, reload, firing-state, or resource mutation on pool-full [06 §11.1] C29
	}
	// Success: decrement slot byte [06 §11.1] and request interface refresh (diagnostic flag)
	slot.Ammo-- // [06 §11.1]
	if slot.Ammo < 0 {
		slot.Ammo = 0 // TODO(question): byte underflow for malformed preexisting values [06 §11.1] unknown
	}
	idx := int(h) - 1
	p := &svc.Records[idx]
	p.WeaponID = slot.Weapon.ID
	p.CreationTick = tick
	p.MuzzlePiece = int16(slot.MuzzlePiece) // preserve muzzle identity [06 §4.1] C3
	// Minimal common initialization: copy muzzle point? Use zero Vec3 placeholder.
	p.Pos = Vec3{}
	p.StartPos = Vec3{}
	if tgt.Kind == TargetPoint {
		p.TargetPos = Vec3{X: tgt.X, Y: tgt.Y, Z: tgt.Z}
	} else if tgt.Kind == TargetUnit {
		p.TargetUnit = tgt.Unit
	}
	// Stockpile launch does not write reload [06 §4.2] C7 performed by caller
	_ = slotIdx
	return h, true
}

// ---------------------------------------------------------------------------
// Interceptor coverage square [06 §11.2] C29
// ---------------------------------------------------------------------------

// InterceptorCoverageHalfExtent returns the half-extent of the interceptor
// coverage square in fixed-point raw units per [06 §11.2].
// The axis-aligned square has side twice coverage<<16 around the stored aim
// point [06 §11.2] C29 (prompt scope). Coverage is the authored integer weapon
// coverage; shift left 16 converts to 16.16 fixed per [03 §2.1] I2.
func InterceptorCoverageHalfExtent(coverage int32) int64 {
	if coverage < 0 {
		return 0 // TODO(question): negative coverage untraced [06 §11.2]
	}
	return int64(coverage) << 16 // coverage<<16 [06 §11.2] C29
}

// InterceptorCoverageSide returns the full side length of the square
// (twice coverage<<16) per [06 §11.2] C29 for sizing tests.
func InterceptorCoverageSide(coverage int32) int64 {
	return InterceptorCoverageHalfExtent(coverage) * 2 // side twice coverage<<16 [06 §11.2]
}

// WithinInterceptorCoverage tests whether the candidate's stored aim point lies
// within the interceptor's inclusive axis-aligned coverage square per [06 §11.2].
// The scan measures an axis-aligned square of side twice coverage<<16 around the
// incoming projectile's stored aim point ([06 §4.x] consumed-linking semantics)
// per C29 prompt, and per [06 §11.2] coverage is not ordinary fire range.
//
// Both axes are tested with inclusive bounds and with wrapped unsigned
// comparison semantics around coverage in fixed-point units [06 §11.2].
// For ordinary nonnegative coverage without overflow this is absolute distance
// [06 §11.2]. Malformed overflowed coverage remains TODO(question) [06 §11.2] unknown.
// TODO(question): wrapped unsigned handling for overflow not modeled; using absolute compare placeholder.
func WithinInterceptorCoverage(candidateAim Vec3, interceptorPos Vec3, coverage int32) bool {
	if coverage < 0 {
		return false // TODO(question): negative coverage untraced [06 §11.2]
	}
	half := InterceptorCoverageHalfExtent(coverage) // [06 §11.2]
	dx := candidateAim.X.Raw() - interceptorPos.X.Raw()
	if dx < 0 {
		dx = -dx
	}
	dz := candidateAim.Z.Raw() - interceptorPos.Z.Raw()
	if dz < 0 {
		dz = -dz
	}
	// Inclusive bounds [06 §11.2]
	return dx <= half && dz <= half // axis-aligned square, not radius circle [06 §11.2]
}

// IsProjectileClaimed reports whether any live projectile already references
// candidate via its reservation link (TargetProjectile) per [06 §11.2].
// Scans the current packed projectile prefix in increasing order [06 §11.2] I1.
// Alliance is not consulted for this dedup; only the link matters [06 §11.2].
func IsProjectileClaimed(svc *Service, candidate pool.Handle) bool {
	if svc == nil || candidate == 0 {
		return false
	}
	// Scan current prefix ascending, as interceptor acquisition does [06 §11.2] I1.
	// Use dynamic Count each iteration? Count stable during scan per phase capture [01 §6.2], but reservation link scan must see all live links.
	// Iterate over current count at call time [06 §11.2].
	cnt := svc.Count()
	for i := 0; i < cnt; i++ {
		h := pool.Handle(i + 1)
		if !svc.Alive(h) {
			continue
		}
		if svc.Records[i].TargetProjectile == candidate {
			return true // already referenced by any projectile's reservation link [06 §11.2]
		}
	}
	return false
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
// before firing and storing the authoritative reservation link at spawn is handled
// by AcquireInterceptorTargetForSpawn [06 §11.2].
//
// Returns the candidate handle, its current position (to be stored in slot's
// fixed target words), and whether a candidate was found.
// Determinism: prefix ascending, first unclaimed wins, not nearest [06 §11.2] I1.
func FindInterceptorTarget(svc *Service, interceptorPos Vec3, interceptorSide uint8, coverage int32, weapons map[int32]*content.WeaponDef) (pool.Handle, Vec3, bool) {
	if svc == nil {
		return 0, Vec3{}, false
	}
	cnt := svc.Count() // capture at entry [01 §6.2] [06 §5.2] projectile phase capture
	for i := 0; i < cnt; i++ {
		h := pool.Handle(i + 1)
		if !svc.Alive(h) {
			continue
		}
		rec := &svc.Records[i]
		if rec.ShooterSide == interceptorSide {
			continue // owner byte differs required, alliance not consulted [06 §11.2]
		}
		// Weapon is targetable check [06 §11.2]
		w := weapons[rec.WeaponID]
		if w == nil || !w.Targetable {
			continue // not targetable [06 §11.2]
		}
		// Stored aim point vs coverage square [06 §11.2]
		if !WithinInterceptorCoverage(rec.TargetPos, interceptorPos, coverage) {
			continue
		}
		// Not already referenced by any reservation link [06 §11.2]
		if IsProjectileClaimed(svc, h) {
			continue
		}
		// First unclaimed in pool order wins, not nearest [06 §11.2]
		curPos := rec.Pos // slot stores candidate's current position [06 §11.2]
		return h, curPos, true
	}
	return 0, Vec3{}, false
}

// AcquireInterceptorTargetForSpawn rescans immediately before firing and reserves
// the authoritative link per [06 §11.2].
// The interceptor spawner rescans immediately before firing; the final candidate's
// record pointer stored in the new interceptor's reservation-link field at spawn
// time is the authoritative reservation, and that store is what later scans test
// when rejecting candidates already claimed [06 §11.2].
//
// On success, it reserves a new interceptor projectile, writes the reservation
// link to the candidate, copies muzzle/current positions minimally, and returns
// the new handle and candidate handle. If no candidate found, it performs no
// ammunition/reload/resource mutation per [06 §11.2] C29 (pending shot, no mutation).
func AcquireInterceptorTargetForSpawn(svc *Service, interceptorPos Vec3, interceptorSide uint8, coverage int32, interceptorWeapon *content.WeaponDef, slot *Slot, muzzlePos Vec3, tick uint32, weapons map[int32]*content.WeaponDef) (newHandle pool.Handle, candidate pool.Handle, ok bool) {
	if svc == nil || interceptorWeapon == nil || slot == nil {
		return 0, 0, false
	}
	if !interceptorWeapon.Interceptor {
		return 0, 0, false // only interceptor-flagged weapon spawner does this rescan [06 §11.2]
	}
	if slot.Ammo <= 0 {
		return 0, 0, false // requires nonzero slot ammunition [06 §11.2]
	}
	// Rescan immediately before firing [06 §11.2]
	candHandle, candCurPos, found := FindInterceptorTarget(svc, interceptorPos, interceptorSide, coverage, weapons)
	if !found {
		// No candidate before firing leaves shot pending; no ammo/reload/resource mutation [06 §11.2] C29
		return 0, 0, false
	}
	// Reserve interceptor projectile at tail [06 §5.1] [06 §11.2]
	h, okReserve := svc.Reserve()
	if !okReserve {
		// Pool-full: no candidate reservation, no mutation per [06 §11.2] C29 and [06 §4.1] C4
		return 0, 0, false
	}
	idx := int(h) - 1
	p := &svc.Records[idx]
	p.WeaponID = interceptorWeapon.ID
	p.CreationTick = tick
	p.Pos = muzzlePos
	p.StartPos = muzzlePos
	p.TargetPos = candCurPos // stored? Actually slot stores candidate current position [06 §11.2]; interceptor's navigation later uses link, not this point.
	p.ShooterSide = interceptorSide
	p.TargetProjectile = candHandle // authoritative reservation link [06 §11.2]
	// Stockpile interceptor weapons may also be stockpile-flagged; interceptor launch still decrements ammo and skips reload per stockpile path [06 §11.1]?
	// If interceptor is stockpile, decrement ammo only on successful spawn per [06 §11.1] C29
	if interceptorWeapon.Stockpile {
		slot.Ammo-- // [06 §11.1] only successful spawn decrements ammunition
		if slot.Ammo < 0 {
			slot.Ammo = 0
		}
	}
	// TODO(question): dead-candidate behavior between aim and fire scans remains open [06 §11.2] unknown
	// TODO(question): multiplayer reconstruction index-versus-pointer anomaly remains open [06 §11.2] unknown
	return h, candHandle, true
}

// ---------------------------------------------------------------------------
// Interceptor detonation and victim signature [06 §11.2] [06 §9.3] C29
// ---------------------------------------------------------------------------

// VictimSig is the signature packet published for each qualifying victim per
// [06 §11.2]. It contains the victim's stored target position and weapon index
// byte [06 §11.2].
type VictimSig struct {
	TargetPos Vec3  // stored target position [06 §11.2]
	WeaponIdx uint8 // weapon index byte [06 §11.2] low byte of weapon ID
}

// PublishVictimSig builds the signature for a projectile per [06 §11.2].
func PublishVictimSig(rec Projectile) VictimSig {
	return VictimSig{
		TargetPos: rec.TargetPos,              // stored target position [06 §11.2]
		WeaponIdx: uint8(rec.WeaponID & 0xFF), // weapon index byte [06 §11.2] TODO(question): mapping of retail weapon index vs ID low byte remains untraced; low byte as placeholder
	}
}

// PublishExploderSig builds the exploding projectile's own signature per [06 §11.2].
// It is published once per qualifying victim [06 §11.2].
func PublishExploderSig(rec Projectile) VictimSig {
	return VictimSig{
		TargetPos: rec.TargetPos,
		WeaponIdx: uint8(rec.WeaponID & 0xFF),
	}
}

// VictimSigEqual reports exact-match equality of two signatures per [06 §11.2]
// C29: the packet receiver scans pool order and impacts the first record whose
// stored target position and weapon index byte all match [06 §11.2]. All fields
// must match exactly; non-match survives.
func VictimSigEqual(a, b VictimSig) bool {
	return a.WeaponIdx == b.WeaponIdx &&
		a.TargetPos.X.Raw() == b.TargetPos.X.Raw() &&
		a.TargetPos.Y.Raw() == b.TargetPos.Y.Raw() &&
		a.TargetPos.Z.Raw() == b.TargetPos.Z.Raw()
}

// FindVictimBySignature scans pool order ascending for the first record whose
// stored target position and weapon index byte all match the signature [06 §11.2] C29.
// It is the packet receiver's scan [06 §11.2].
// Returns handle and true on exact match; false otherwise (non-match survival) [06 §11.2].
func FindVictimBySignature(svc *Service, sig VictimSig) (pool.Handle, bool) {
	if svc == nil {
		return 0, false
	}
	cnt := svc.Count()
	for i := 0; i < cnt; i++ {
		h := pool.Handle(i + 1)
		if !svc.Alive(h) {
			continue
		}
		rec := &svc.Records[i]
		candSig := VictimSig{
			TargetPos: rec.TargetPos,
			WeaponIdx: uint8(rec.WeaponID & 0xFF),
		}
		if VictimSigEqual(candSig, sig) {
			return h, true // exact match [06 §11.2]
		}
	}
	return 0, false // non-match survival [06 §11.2]
}

// ProjectileInInterceptorBlast tests whether a projectile lies within the
// interceptor's unhalved areaofeffect radius per [06 §11.2] [06 §9.3] C29.
// Uses the unhalved authored area value (no >>1) [06 §11.2] and strict
// three-axis distance test (< area²) per [06 §8.1] projectile-link proximity
// and [06 §9.3] interceptor-chain flag strict test [06 §11.2].
//
// Distance is computed in integer world units (truncate toward zero [01 §8] I3)
// from three-dimensional current-point positions [06 §8.1].
// Halved vs unhalved distinction is critical for sizing test [06 §9.3] C29.
// TODO(question): exact scale of victim height vs terrain/explosion center height comparison remains untraced beyond ordinary samples.
func ProjectileInInterceptorBlast(victimPos, exploderPos Vec3, unhalvedArea int32) bool {
	if unhalvedArea <= 0 {
		return false
	}
	// Convert fixed to integer world units trunc toward zero [01 §8] I3
	dx := victimPos.X.Int() - exploderPos.X.Int()
	dy := victimPos.Y.Int() - exploderPos.Y.Int()
	dz := victimPos.Z.Int() - exploderPos.Z.Int()
	// Strict three-axis squared test [06 §8.1] [06 §9.3]
	// Compare squared distance < radius² [06 §8.1]
	dist2 := dx*dx + dy*dy + dz*dz
	area2 := int64(unhalvedArea) * int64(unhalvedArea)
	return int64(dist2) < area2 // strict < [06 §9.3] [06 §8.1] [06 §11.2]
}

// CollectInterceptorVictims scans live non-self projectile records in pool
// order using the unhalved authored area value per [06 §11.2] C29.
// It is called after ordinary unit and feature area enumeration [06 §9.3]
// [06 §11.2] C29, and does not filter by side, alliance, or targetable state
// [06 §11.2] — friendly projectiles can be removed.
// Each qualifying victim is forced through ordinary impact (modeled here as
// collection for the caller to impact). The loop reloads live pool count so
// records appended while scanning can be reached [06 §9.3].
//
// exploder is the interceptor-flagged exploding projectile handle (skipped as self) [06 §11.2].
func CollectInterceptorVictims(svc *Service, exploder pool.Handle, exploderPos Vec3, weapon *content.WeaponDef) []pool.Handle {
	if svc == nil || weapon == nil {
		return nil
	}
	if !weapon.Interceptor {
		return nil // only interceptor-flagged weapons do this sweep [06 §11.2]
	}
	unhalved := weapon.AreaOfEffect // unhalved authored area value [06 §11.2] C29
	if unhalved <= 0 {
		return nil
	}
	var victims []pool.Handle
	// Reload live pool count during scan per [06 §9.3] C29
	for i := 0; i < svc.Count(); i++ {
		h := pool.Handle(i + 1)
		if h == exploder {
			continue // non-self skip [06 §11.2]
		}
		if !svc.Alive(h) {
			continue // alive only [06 §11.2]
		}
		rec := &svc.Records[i]
		if ProjectileInInterceptorBlast(rec.Pos, exploderPos, unhalved) {
			victims = append(victims, h) // each accepted projectile forced through impact [06 §11.2]
		}
	}
	return victims
}

// ApplyInterceptorExplosion performs the interceptor-flagged explosion ordering
// per [06 §11.2] C29 and [06 §9.3] C29:
// ordinary area enumeration (unit+feature) happens first, then the interceptor
// force-detonation sweep of alive non-self projectiles inside its UNHALVED
// areaofeffect [06 §11.2] [06 §9.3] C29.
// The victim signature publication for exact-match removal is modeled via
// Publish/Find helpers [06 §11.2] C29.
// The helper takes callbacks so tests can verify ordering.
//
// ordinary enumerates units/features (broad phase plus box distance) and calls
// perUnit/perFeature in that order [06 §9.3]. For stockpile interceptor burst,
// the exact box enumeration is elided; only ordering (ordinary before projectile
// sweep) matters for the C29 fixture.
//
// interceptorVictims is the projectile sweep; each victim is also resolved via
// exact-match signature scan to demonstrate signature publication [06 §11.2].
// The exploding projectile's own signature is published once per qualifying
// victim [06 §11.2] (caller may record it).
func ApplyInterceptorExplosion(svc *Service, exploder pool.Handle, exploderPos Vec3, weapon *content.WeaponDef, ordinary func(), onVictims func([]pool.Handle, []VictimSig, []VictimSig)) {
	if weapon == nil || !weapon.Interceptor {
		if ordinary != nil {
			ordinary()
		}
		return
	}
	// Ordinary area enumeration first [06 §9.3] [06 §11.2] C29
	if ordinary != nil {
		ordinary()
	}
	// Then interceptor force-detonation sweep using unhalved area [06 §11.2] C29
	victims := CollectInterceptorVictims(svc, exploder, exploderPos, weapon)
	// Victim signature publication for exact-match removal [06 §11.2] C29
	var victimSigs []VictimSig
	var exploderSigs []VictimSig
	if len(victims) > 0 {
		// Need exploder record for its signature if alive
		var exploderRec Projectile
		if exploder != 0 {
			idx := int(exploder) - 1
			if idx >= 0 && idx < len(svc.Records) && idx < svc.Count() {
				exploderRec = svc.Records[idx]
			}
		}
		for _, vh := range victims {
			idx := int(vh) - 1
			if idx < 0 || idx >= len(svc.Records) {
				continue
			}
			vsig := PublishVictimSig(svc.Records[idx]) // [06 §11.2]
			victimSigs = append(victimSigs, vsig)
			esig := PublishExploderSig(exploderRec) // [06 §11.2] once per qualifying victim
			exploderSigs = append(exploderSigs, esig)
		}
	}
	if onVictims != nil {
		onVictims(victims, victimSigs, exploderSigs)
	}
}

// ---------------------------------------------------------------------------
// Firestarter [06 §13.1] C29 scope: single-site nonzero test if it belongs here
// ---------------------------------------------------------------------------

// IsFirestarter reports whether the weapon's firestarter value is nonzero per
// [06 §13.1] — radial feature damage can ignite only when the weapon firestarter
// value is nonzero [06 §13.1] (among other gates: feature fire globally enabled,
// feature flammable). This is a single-site nonzero test (field !=0) per C29
// prompt, included in stockpile.go because interceptor area enumeration and
// feature fire handling are adjacent weapon-driven feature effects.
// TODO(question): whether firestarter belongs strictly to stockpile.go vs impact.go remains open; placed here as C29 asks to include the nonzero test if it belongs here.
func IsFirestarter(w *content.WeaponDef) bool {
	if w == nil {
		return false
	}
	return w.Firestarter != 0 // [06 §13.1] nonzero enables ignition
}

// ShouldIgniteFeatureGate tests the collaborative gate for feature fire
// ignition via weapon firestarter per [06 §13.1] C29.
// Returns true only when global feature fire enabled, feature is flammable,
// and weapon firestarter is nonzero [06 §13.1].
func ShouldIgniteFeatureGate(globalFeatureFireEnabled bool, featureFlammable bool, w *content.WeaponDef) bool {
	if !globalFeatureFireEnabled {
		return false
	}
	if !featureFlammable {
		return false
	}
	return IsFirestarter(w) // [06 §13.1] single-site nonzero test
}

// Ensure imports used: math for sqrt alternative (not needed but keep for future malformed cases)
var _ = math.Sqrt
