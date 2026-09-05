// Stockpile and interceptors per [06 §11] C29.
//
// WU-09-9 owns stockpile.go. Other combat files own pool, slots, fire, etc.
// Determinism: stable pool order, no map iteration (I1), fixed-point 16.16 (I2),
// truncation toward zero where retail does (I3), pool.Projectiles is sole
// count/dead authority (I5).

package combat

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
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
// The node carries the fields [06 §11.1] establishes: a signed requested
// count, an integer progress value, and a slot index that is the
// caller-supplied build-type argument stored verbatim — there is no
// weapon-id-to-slot translation anywhere in the queue path [06 §11.1]. That
// slot index selects the unit's weapon slot directly, and the handler tests
// neither that the slot's weapon carries `stockpile` nor that it is a real
// weapon [06 R-WPN-05 §2]. Save restoration preserves these node fields plus
// the three per-unit slot bytes [06 §11.1] [08 "Save-file organization"]. A
// malformed slot index outside 0..2 aliases past the unit object in retail;
// this implementation treats it as no weapon / wait instead [06 §11.1]
// [06 R-WPN-05 §2].
type StockpileEntry struct {
	Weapon   *content.WeaponDef // selected weapon; BuildTime is Weapon.ReloadTime [06 §11.1]
	Count    int32              // signed requested count [06 §11.1]
	Progress int32              // per-node progress 0..BuildTime, step 5 capped [06 §11.1]
	SlotIdx  int32              // slot index selecting the unit's weapon slot directly, stored verbatim [06 §11.1]
}

// StockpileNodeSize is the retail queue node size 0x56=86 bytes [P1-09 §2.1].
const StockpileNodeSize = 0x56 // [P1-09 §2.1]

// StockpileSlotByteCap is the >199 block threshold [P1-09 §2.2] [06 §11.1].
//
// The byte can neither underflow nor pass 200 through the engine's own paths
// [06 R-WPN-05 §2]: phase 0 refuses to start a round above 199, so the +1 of
// phase 2 tops out at 200, and the launch decrement sits behind a nonzero test.
// A value of 200..255 can arrive only from a save, and it then blocks every new
// round forever — the phase-0 hold, re-armed every 300 ticks — without ever
// wrapping. There is no wrap to model.
const StockpileSlotByteCap = 199 // [P1-09 §2.2]

// BuildTime returns the compiled reload-time used as stockpile build time [06 §11.1].
func StockpileBuildTime(w *content.WeaponDef) int32 {
	if w == nil {
		return 0
	}
	return w.ReloadTime // [02 "Weapon record"] reloadtime*30, used as build time per [06 §11.1]
}

// CanStartStockpileRound reports whether a new round may start given the slot's
// byte-sized completed-round remainder [06 §11.1].
// A new round is blocked when the slot byte is already greater than 199 [06 §11.1] [P1-09 §2.2].
// Malformed slot byte 200..255 all block; byte inc wraps 255→0 [P1-09 §2.3].
func CanStartStockpileRound(ammo int32) bool {
	return ammo <= StockpileSlotByteCap // [P1-09 §2.2] >199 blocked
}

// IsValidStockpileSlotIdx reports whether slotIdx is in 0..2 for the three
// weapon slots [P1-09 §2.3]. Malformed ≥3 or negative aliases past the unit object
// and must be treated as no weapon / wait without corrupting memory [P1-09 §2.3].
func IsValidStockpileSlotIdx(slotIdx int32) bool {
	return slotIdx >= 0 && slotIdx < NumSlots // 0..2 [P1-09 §2.3]
}

// QueueProducers names the four producers that push a BUILDWEAPON entry onto a
// unit's secondary production queue [06 §11.1][P1-09 §2.1]. Cancelling a
// partially built round unlinks the node and leaves its fractional carry in the
// economy buckets rather than refunding it [P1-09 §5].
//
// The names are the producers, not their addresses: the first two literals used
// to carry executable addresses.
var QueueProducers = []string{
	"HUD MAKENUKE/MAKEANTI order alias", // [06 §11.1] the two order aliases
	"initial-mission Bw verb parser",    // [P1-09 §2.1]
	"network build decoder 0x12",        // [P1-09 §2.1]
	"network completion decoder 0x2C",   // [P1-09 §2.1]
}

// StockpileCostDelta computes the admitted delta for one resource for this visit
// per [06 §11.1] [P1-09 §2.4]:
//
//	energyDelta = trunc(next*energyCost/buildTime) - trunc(old*energyCost/buildTime)
//	metalDelta  = trunc(next*metalCost/buildTime) - trunc(old*metalCost/buildTime)
//
// Both are independently truncated toward zero [01 §8] I3 [06 §11.1].
// Fractional truncated-cumulative carry is preserved across visits without
// explicit carry variable; after admission failure progress not advanced but
// both requested amounts retained in economy buckets [P1-09 §4].
// On cancel after partial progress the fractional carry remains in buckets
// (not refunded) — cancellation just unlinks the node [P1-09 §5].
func StockpileCostDelta(oldProg, newProg int32, cost float64, buildTime int32) float32 {
	if buildTime <= 0 {
		// A build time of zero is the unarmed slot's weapon record 0, the
		// `[noweapon]` sentinel every empty slot points at [06 R-DMG-01 §5].
		// Phase 1 forms `next = min(progress + 5, 0) = 0`, so BOTH quotients
		// are `0·cost/0` — an invalid operation whose truncation is the same
		// indefinite integer on either side — and the delta is exactly 0
		// [06 R-WPN-05 §2]. Returning the full cost, as this used to, charged
		// for a round retail completes free.
		return 0
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
	// Slot→node map malformed check: slotIdx out of 0..2 treated as no weapon / wait [P1-09 §2.3]
	if entry.SlotIdx != 0 && !IsValidStockpileSlotIdx(entry.SlotIdx) {
		return tick + StockpileRetryBlocked, false, 0 // malformed alias past object -> wait [P1-09 §2.3]
	}
	w := entry.Weapon
	buildTime := StockpileBuildTime(w) // [06 §11.1]
	// A build time of zero is NOT a special case. The handler tests neither
	// that the slot's weapon carries `stockpile` nor that it is a real weapon,
	// and an unarmed slot points at weapon record 0 — reload 0, both per-shot
	// costs 0 [06 R-WPN-05 §2][06 R-DMG-01 §5]. The ordinary path below carries
	// it: `next = min(progress + 5, 0)` is 0, both deltas are 0
	// (StockpileCostDelta), the admission accepts a zero request unless the
	// unit's buckets already carry debt, `0 < 0` is false so the round
	// completes, and the pump's re-dispatch of the head — the multi-completion
	// loop here — empties the whole queue in this one visit, free, until the
	// count reaches 0 or the byte passes 199. The special branch that used to
	// stand here charged the weapon's whole per-shot cost per round instead.
	completedRounds = 0
	refreshed = false
	// If slot blocked at entry, wait 300 [06 §11.1] [P1-09 §2.2]
	if !CanStartStockpileRound(slot.Ammo) {
		return tick + StockpileRetryBlocked, false, 0 // [06 §11.1] >199 block [P1-09 §2.2]
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
		// Completion: increment slot byte with wrap 255→0 [P1-09 §2.3], decrement signed queue count, request refresh [06 §11.1]
		// Phase 2's increment: no cap and no wrap test [06 R-WPN-05 §2]. The
		// >199 gate at the top of this loop is what bounds it, so the byte
		// tops out at 200 and the old mask was dead.
		slot.Ammo++
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

// There is no separate stockpile launcher. WU-19-185 deleted the one that used
// to stand here: it had no production caller and it duplicated the record
// creation of TryFire behind four operands that never reached it (the firing
// unit, the muzzle query, a target-position resolver and the map's gravity).
//
// The live path is the ordinary per-slot pipeline. For a stockpile weapon the
// slot's fire gate reads "slot byte nonzero" IN PLACE OF the per-shot cost
// test and otherwise runs the SAME executor [06 §11.1][06 R-WPN-05 §2], so the
// spawner is TryFire and the creator is whichever one the weapon's own flags
// select [06 §6.2] — `vlaunch` for all eight stockpile-flagged weapons in the
// retail corpus (I14), which is the vertical-launch creator of [06 §6.6]. The
// gate, the launch-before-production ordering, the post-spawn decrement of the
// slot byte and the skipped reload store all belong to the pipeline
// (StepWeaponsForUnit and TickSlot), not to the spawner [06 §4.1] C1
// [06 §4.2] C7 [06 §11.1].

// ---------------------------------------------------------------------------
// Interceptor coverage square [06 §11.2] C29
// ---------------------------------------------------------------------------

// InterceptorCoverageHalfExtent returns the `C << 16` bias term the coverage
// compare adds to each axis delta, in raw 16.16 units [06 R-WPN-05 §10].
// For an ordinary nonnegative coverage below 32,768 this is the half-extent of
// the axis-aligned square in 16.16 units, which is what the name says; the
// shift is formed in 32-bit arithmetic, so larger or negative authored values
// wrap and the term stops being a half-extent. That wrap is the contract, not
// an error case, and there is no "negative means none" guard
// [06 R-WPN-05 §10].
func InterceptorCoverageHalfExtent(coverage int32) int64 {
	return int64(int32(uint32(coverage) << 16)) // C<<16, 32-bit [06 R-WPN-05 §10]
}

// InterceptorCoverageSide returns the `C << 17` limit the coverage compare
// tests against, in raw 16.16 units [06 R-WPN-05 §10]. For ordinary coverage
// it is the full side of the square; like the bias it is formed in 32-bit
// arithmetic and wraps at or above 32,768, which is why it is not simply twice
// the half-extent computed in a wider type [06 R-WPN-05 §10].
func InterceptorCoverageSide(coverage int32) int64 {
	return int64(int32(uint32(coverage) << 17)) // C<<17, 32-bit [06 R-WPN-05 §10]
}

// WithinInterceptorCoverage tests the interceptor scan's coverage compare on
// the candidate's stored aim point [06 §11.2][06 R-WPN-05 §10].
//
// The compare is 32-bit unsigned, per axis, X first and then Z, never Y, and
// the subtraction is the interceptor *unit's* position minus the candidate's
// stored aim point [06 R-WPN-05 §10]:
//
//	accept axis iff uint32((unit.axis - candidate.storedAim.axis) + (C<<16))
//	                <= uint32(C<<17)
//
// The formula in uint32 is the whole contract — there is no separate rule for
// malformed values [06 R-WPN-05 §10]. For ordinary nonnegative coverage below
// 32,768 it is |delta| <= C world units, inclusive at the boundary, and an
// axis-aligned square rather than a radius circle [06 §11.2]. A negative
// coverage -k accepts exactly the complement of the open square of half-side
// k: a candidate at or beyond k world units on both axes is accepted and one
// inside is rejected. A coverage at or above 32,768 wraps C<<17 and accepts
// whatever set the formula then gives [06 R-WPN-05 §10].
func WithinInterceptorCoverage(candidateAim Vec3, interceptorPos Vec3, coverage int32) bool {
	bias := uint32(coverage) << 16  // C<<16 [06 R-WPN-05 §10]
	limit := uint32(coverage) << 17 // C<<17 [06 R-WPN-05 §10]
	// The deltas are 32-bit subtractions of the 16.16 values [06 R-WPN-05 §10].
	dx := uint32(int32(interceptorPos.X.Raw() - candidateAim.X.Raw()))
	if dx+bias > limit {
		return false // X is tested first [06 R-WPN-05 §10]
	}
	dz := uint32(int32(interceptorPos.Z.Raw() - candidateAim.Z.Raw()))
	return dz+bias <= limit // then Z; Y is never tested [06 R-WPN-05 §10]
}

// IsProjectileClaimed reports whether any projectile already references
// candidate via its reservation link (TargetProjectile) per [06 §11.2].
// Scans the current packed projectile prefix in increasing order [06 §11.2] I1.
// Alliance is not consulted for this dedup; only the link matters [06 §11.2].
// This is the claim pass, and like the candidate pass it does not test the dead
// bit — a dead-but-uncompacted record's link still claims its candidate
// [06 §11.2].
func IsProjectileClaimed(svc *Service, candidate pool.Handle) bool {
	if svc == nil || candidate == 0 {
		return false
	}
	// Scan current prefix ascending, as interceptor acquisition does [06 §11.2] I1.
	cnt := svc.Count()
	for i := 0; i < cnt; i++ {
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

// findInterceptorTarget is FindInterceptorTarget over a weapon LOOKUP rather
// than a map. The production caller resolves through the catalog's
// once-compiled slot index, which resolves colliding ids in the documented
// order [02 "Weapon record"] and allocates nothing; the exported map form above
// is the fixture seam its tests were written against.
func findInterceptorTarget(svc *Service, interceptorPos Vec3, interceptorSide uint8, coverage int32, weaponByID func(int32) (*content.WeaponDef, bool)) (pool.Handle, Vec3, bool) {
	if svc == nil || weaponByID == nil {
		return 0, Vec3{}, false
	}
	cnt := svc.Count() // capture at entry [01 §6.2] [06 §5.2] projectile phase capture
	for i := 0; i < cnt; i++ {
		h := pool.Handle(i + 1)
		// No dead-bit test: neither interceptor scan filters on liveness, so a
		// dead-but-uncompacted targetable enemy record inside coverage and
		// unclaimed is still selected [06 §11.2][06 R-WPN-05 §10].
		rec := &svc.Records[i]
		if rec.ShooterSide == interceptorSide {
			continue // owner byte differs required, alliance not consulted [06 §11.2]
		}
		// Weapon is targetable check [06 §11.2]
		w, okW := weaponByID(rec.WeaponID)
		if !okW || w == nil || !w.Targetable {
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

// ---------------------------------------------------------------------------
// Interceptor detonation and victim signature [06 §11.2] [06 §9.3] C29
// ---------------------------------------------------------------------------

// VictimSig is the signature packet published for each qualifying victim per
// [06 §11.2]. It contains the victim's stored target position and weapon index
// byte [06 §11.2].
// The weapon byte is the one the weapon loader stamps into every record with
// that record's own slot index, 0..255. Because an authored `ID` selects the
// record slot ([02 "Weapon record"]), the byte is the low byte of the authored
// ID for every record an ID selects, so `uint8(WeaponID & 0xFF)` is exact
// [06 R-WPN-05 §10]. The signature exists to replicate the removal to other
// endpoints; in a single-process game the local impact-selector call is the
// whole effect and the byte has no consumer [06 R-WPN-05 §10].
type VictimSig struct {
	TargetPos Vec3  // stored target position [06 §11.2]
	WeaponIdx uint8 // weapon record slot index [06 R-WPN-05 §10]
}

// PublishVictimSig builds the signature for a projectile per [06 §11.2].
func PublishVictimSig(rec Projectile) VictimSig {
	return VictimSig{
		TargetPos: rec.TargetPos,              // stored target position [06 §11.2]
		WeaponIdx: uint8(rec.WeaponID & 0xFF), // record slot index [06 R-WPN-05 §10]
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
// interceptor's unhalved areaofeffect per [06 §11.2][06 §9.3][06 R-WPN-05 §10].
//
// The metric is the sum of three truncated squares. With d.axis the raw 16.16
// difference of the two records' current points [06 R-WPN-05 §10]:
//
//	((dx·dx)>>32) + ((dy·dy)>>32) + ((dz·dz)>>32)  <  area × area
//
// each square a 64-bit signed product arithmetically shifted right by 32 —
// whole world units squared, truncated per axis — summed as a signed 32-bit
// integer and compared strictly against the square of the *unhalved* 16-bit
// areaofeffect. Squaring the whole 16.16 delta and truncating afterwards is not
// the same as truncating the delta and squaring it: a delta of 1.9 world units
// contributes 3, not 1 [06 R-WPN-05 §10]. That is why the halved/unhalved and
// the truncation order both matter to the sizing.
//
// The Y term is the two projectile records' current heights in the same 16.16
// domain as X and Z — no terrain sample, no separate scale; the "victim height"
// is simply the victim record's current Y [06 R-WPN-05 §10]. The comparison is
// widened to 64 bits so the 16-bit area's square is exact.
func ProjectileInInterceptorBlast(victimPos, exploderPos Vec3, unhalvedArea int32) bool {
	dx := exploderPos.X.Raw() - victimPos.X.Raw() // raw 16.16 deltas [06 R-WPN-05 §10]
	dy := exploderPos.Y.Raw() - victimPos.Y.Raw()
	dz := exploderPos.Z.Raw() - victimPos.Z.Raw()
	// 64-bit signed squares, arithmetic shift right 32, summed as int32
	// [06 R-WPN-05 §10].
	sum := int32((dx*dx)>>32) + int32((dy*dy)>>32) + int32((dz*dz)>>32)
	area := int64(uint16(unhalvedArea)) // unhalved 16-bit areaofeffect [06 R-WPN-05 §10]
	return int64(sum) < area*area       // strict < [06 §9.3][06 R-WPN-05 §10]
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
// The automatic interceptor scan and the fire-time rescan [06 §11.2] C29
// ---------------------------------------------------------------------------

// interceptorScanCandidate is the scan both interceptor passes run [06 §11.2]:
// the aim-time automatic scan, which "runs from the same per-slot position in
// the autonomous scan that ordinary acquisition runs from (§3.2) and is chosen
// by the slot weapon's interceptor flag", and the fire-time rescan the
// vertical-launch executor performs immediately before firing.
//
// The gates, in the section's order: the slot's ammunition byte is nonzero;
// then the pool prefix walk, which accepts the first candidate whose owner side
// differs from the interceptor unit's (the alliance matrix is NOT consulted),
// whose weapon carries `targetable`, whose STORED AIM POINT lies inside the
// inclusive coverage square measured from the interceptor UNIT's position, and
// which no pool record's reservation link already claims. Neither pass tests
// the dead bit.
//
// The returned Vec3 is the candidate's CURRENT position, which is what the slot
// store takes — [06 §11.2] is explicit that the two positions play different
// roles and must not be conflated: the scan metric is the stored aim point (so
// interceptors defend the aimed-at ground point) and the slot store is the
// current position.
//
// The coverage operand is the weapon's own `coverage` scalar, which is separate
// from ordinary fire range [06 §11.2]; it is passed through verbatim, negative
// and oversized values included [06 R-WPN-05 §10].
func interceptorScanCandidate(svc *Service, u *units.Unit, slot *units.Slot, catalog *content.Catalog) (pool.Handle, Vec3, bool) {
	if svc == nil || u == nil || slot == nil || slot.Weapon == nil || catalog == nil {
		return 0, Vec3{}, false
	}
	if !slot.Weapon.Interceptor {
		return 0, Vec3{}, false
	}
	if slot.Ammo <= 0 {
		return 0, Vec3{}, false // requires a nonzero slot ammunition byte [06 §11.2]
	}
	// The subtraction is the interceptor UNIT's position, not its weapon piece
	// [06 R-WPN-05 §10].
	pos := Vec3{X: u.X, Y: u.Y, Z: u.Z}
	// The owner-side operand is the unit's owning-player byte [06 §11.2]. The
	// catalog's once-compiled slot index resolves the candidate weapon; ranging
	// a map here would resolve colliding ids in Go's randomised order (I1).
	return findInterceptorTarget(svc, pos, u.Owner, slot.Weapon.Coverage, catalog.WeaponByID)
}
