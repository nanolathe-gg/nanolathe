// Package combat — targeting and acquisition per [06 §3] P0-10 (WU-09-1).
//
// Acquisition scan rules, range vs coverage distinction, candidate selection
// order, and hysteresis are established per [06 §3] P0-10. Coverage drives overlay
// only [06 §3.3]; engagement uses Range. Candidate traversal excludes features
// because they are in a separate system [06 §3.1]. Iteration is deterministic
// (pool slot asc) [06 §1.2] (I1); no map iteration.
package combat

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TargetKind distinguishes how a slot's target is encoded [06 §3.2] P0-10 (I13).
// Retail stores unit vs ground via a sentinel value in the weapon slot
// record's target field [06 §1.2] P0-10.
// Low-level setters do not validate alliance, category, sensor, range or ballistic feasibility [06 §3.2] P0-10.
type TargetKind uint8

const (
	TargetNone  TargetKind = iota // no target
	TargetUnit                    // unit target (pool handle), identified by the slot record's sentinel value [06 §1.2] P0-10
	TargetPoint                   // world position ground point x/z words <<16, sentinel absent [06 §1.2] P0-10
)

// SentinelUnit is the retail sentinel for the unit latch in the weapon slot
// record's target field [06 §1.2] P0-10.
const SentinelUnit int16 = -0x8000 // 0x8000

// Target is the per-slot encoded target state [06 §1.2] [06 §3.2] P0-10 (I13).
// The weapon slot record's target field pairs a signed 16-bit unit index
// (0=null) with a signed 16-bit sentinel [06 §1.2] P0-10.
// Ground point: x/z words <<16 plus Y resolved through the terrain height query [06 §1.2] P0-10.
type Target struct {
	Kind TargetKind    // [06 §3.2] P0-10 (I13)
	Unit pool.Handle   // valid when Kind==TargetUnit; 0 null sentinel, no generation token [06 §5.1] (I13) [01 §6.1]
	X, Z numeric.Fixed // valid when Kind==TargetPoint or for fallback point when unit target lost [06 §3.2] P0-10 (I13)
	Y    numeric.Fixed // height when point-target via terrain query [06 §3.2] P0-10
}

// IsUnitTarget reports whether the target names a unit slot [06 §3.2] P0-10.
func (t Target) IsUnitTarget() bool { return t.Kind == TargetUnit && t.Unit != 0 }

// IsPointTarget reports whether the target is a world point [06 §3.2] P0-10.
func (t Target) IsPointTarget() bool { return t.Kind == TargetPoint }

// ---------------------------------------------------------------------------
// Range vs coverage distinction [06 §3.3] [06 §2.1] [06 §11.2] P0-10
// ---------------------------------------------------------------------------

// WithinRange reports whether planar distance from shooter to candidate is within weapon Range [06 §3.3] P0-10 [06 §2.1].
// Ordinary fire range uses horizontal distance against the weapon range [06 §3.3] P0-10.
// Tests dist² vs range² via 64-bit __allmul then >>? inclusive JLE; range0 only self-cell admits [06 §3.3] P0-10.
// Coverage is a separate scalar for projectile-target/interceptor behavior and is NOT the ordinary ground-target fire radius [06 §3.3] P0-10 (coverage drives overlay only).
// This helper intentionally uses Range, not Coverage (I10).
func WithinRange(shooterX, shooterZ, candX, candZ numeric.Fixed, weaponRange int32) bool {
	if weaponRange < 0 {
		return false // negative range admits none [06 §2.1] P0-10
	}
	dx := int64(candX.Sub(shooterX).Raw()) // [06 §3.3] planar delta, raw 16.16
	dz := int64(candZ.Sub(shooterZ).Raw())
	// [06 §3.3]: `a = (int32)(((int64)dx * dx) >> 32)`, and the same for dz —
	// the raw deltas are squared FIRST in 64 bits and the product is then
	// shifted down by a whole 32, which is the squared distance in whole world
	// units with the fraction discarded from the SQUARE.
	//
	// Corrected 2026-08-31. This truncated each delta to whole world units
	// first and squared the truncations, which is a different number: at a
	// separation of 1.5 world units on one axis retail contributes 2 and the
	// old form contributed 1. It shortened every weapon's effective reach by
	// up to a world unit per axis and disagreed with the order-side
	// shot-admission gate of [04 R-ORD-01 §7], which squares before shifting —
	// so a chase could bind a slot the firing step then refused.
	dist2 := ((dx * dx) >> 32) + ((dz * dz) >> 32)
	r2 := int64(weaponRange) * int64(weaponRange)
	return dist2 <= r2 // inclusive JLE [06 §3.3]
}

// WithinCoverageSquare reports whether the candidate's stored aim point lies within the interceptor's coverage square [06 §11.2] P0-10.
// The scan uses an inclusive axis-aligned coverage square, not a radius circle [06 §11.2] [06 §3.3] P0-10.
// Coverage is NOT ordinary fire range [06 §3.3] — this helper is for interceptor acquisition only [06 §11.2].
func WithinCoverageSquare(shooterX, shooterZ, candX, candZ numeric.Fixed, coverage int32) bool {
	if coverage < 0 {
		return false
	}
	dx := candX.Sub(shooterX)
	dz := candZ.Sub(shooterZ)
	dxI := dx.Int()
	if dxI < 0 {
		dxI = -dxI
	}
	dzI := dz.Int()
	if dzI < 0 {
		dzI = -dzI
	}
	return dxI <= int64(coverage) && dzI <= int64(coverage) // inclusive bounds per [06 §11.2] P0-10
}

// ---------------------------------------------------------------------------
// Candidate selection order per [06 §3.1] [06 §3.2] P0-10 (I1, I4)
// ---------------------------------------------------------------------------

// Candidate is one automatic candidate for a weapon slot's acquisition scan [06 §3.1] P0-10 [06 §3.2] P0-10.
// The input set for ordinary acquisition comes from per-player lists rebuilt on a cadence of at least 30 ticks,
// primary list requires hostility and a direct-visibility predicate that accepts own-side units, rejects cloaked,
// rejects underwater without dedicated status bit 0x200, and samples multiple target-bounds points [06 §3.1] P0-10.
// Secondary status list is consulted only when primary in-radius set is empty and targeting-upgrade aggregate is active [06 §3.1] P0-10.
type Candidate struct {
	Handle   pool.Handle   // pool slot index, 0 null sentinel; iteration order is slot asc [06 §1.2] P0-10 (I1) [01 §6.1]
	X, Z     numeric.Fixed // current world X/Z [06 §3.2] P0-10
	Y        numeric.Fixed // reference/top height, compared against sea level via Y>sea<<16 [06 §3.1] P0-10 (I13)
	Category uint32        // decoded category bitset for this candidate [06 §3.1] P0-10
	// CategoryMask is the compiled unit-ID membership mask. It is authoritative
	// whenever CategoryMaskResolved is true; Category is retained only for old
	// fixture callers and is never populated from authored text at runtime
	// [R-P0-03].
	CategoryMask         content.CategoryMask
	CategoryMaskResolved bool
	Hostile              bool // hostility for primary list [06 §3.1] P0-10

	// OwnSide marks a candidate belonging to the viewing player. The
	// direct-visibility predicate accepts own-side units outright, without
	// consulting the visibility state [06 §3.1] P0-10.
	OwnSide bool
	// Cloaked units are rejected by the direct-visibility predicate [06 §3.1] P0-10.
	Cloaked bool
	// Underwater units are rejected unless their dedicated status bit 0x200 is set
	// [06 §3.1] P0-10. The bit is the sensor phase's 0x200 underwater-exempt marking (FriendlyMask alias).
	Underwater     bool
	UnderwaterSeen bool // 0x200 alias [06 §3.1] P0-10 [03 §3.4] P0-11
	// AirTarget is the to-air target-status class, enforced when the slot
	// requests it [06 §3.1] P0-10.
	AirTarget bool
}

// IsPreferredCategory reports whether candidate's category is clear of the slot's bad-target-category mask,
// entering the preferred bucket [06 §3.1] P0-10. A matching candidate (category & badMask !=0) enters the fallback bucket [06 §3.1] P0-10.
// Any preferred result wins over fallback [06 §3.1] P0-10.
func IsPreferredCategory(cat, badMask uint32) bool { return cat&badMask == 0 } // [06 §3.1] P0-10

// IsPreferredCategoryMask tests compiled unit-definition membership masks.
// Category tokens are registry keys whose values are unit-ID sets; no runtime
// token hashing participates [R-P0-03][06 §3.1].
func IsPreferredCategoryMask(cat, badMask content.CategoryMask) bool {
	return !cat.Intersects(badMask)
}

// rngBoundForCandidate computes the shared-RNG bound for a candidate as the sum of the high 32-bit halves
// of its squared fixed-point X and Z deltas [06 §3.2] P0-10. Bound = (dxRaw²>>32)+(dzRaw²>>32) via __allmul.
// A bound below two uses the RNG helper's zero/no-advance path [01 §7.1] (I4) P0-10.
func rngBoundForCandidate(dx, dz numeric.Fixed) uint32 {
	dxRaw := int64(dx.Raw()) // 16.16 raw [03 §2.1] I2 P0-10
	dzRaw := int64(dz.Raw())
	dx2 := dxRaw * dxRaw // 32.32
	dz2 := dzRaw * dzRaw
	highDx := uint32(uint64(dx2) >> 32)
	highDz := uint32(uint64(dz2) >> 32)
	bound := highDx + highDz // sum of high halves [06 §3.2] P0-10
	return bound
}

// selectPreferredWinner selects the winner from one bucket (preferred or fallback) using shared-RNG scores per [06 §3.2] P0-10.
// Each candidate receives a shared-RNG score bounded by the sum of high halves of squared fixed deltas [06 §3.2] P0-10.
// Bound <2 uses RNG zero/no-advance path [01 §7.1] (I4) P0-10. Strictly lower score wins, equal preserves first sampled candidate in that bucket [06 §3.2] P0-10.
// Determinism: iteration is bucket order as provided (which must be stable per I1); no map iteration; RNG draw order is authoritative [06 §3.2] P0-10 (I4) (I1).
func selectPreferredWinner(bucket []Candidate, shooterX, shooterZ numeric.Fixed, rng *rng.Simulation) (Candidate, bool) {
	if len(bucket) == 0 {
		return Candidate{}, false
	}
	bestIdx := -1
	var bestScore uint32
	for i, c := range bucket {
		dx := c.X.Sub(shooterX)
		dz := c.Z.Sub(shooterZ)
		bound := rngBoundForCandidate(dx, dz) // [06 §3.2] P0-10
		var score uint32
		if rng != nil {
			score = rng.Uint32n(bound) // [01 §7.1] I4: bound<2 returns 0 without advance P0-10
		} else {
			score = 0
		}
		if bestIdx == -1 || score < bestScore { // strictly lower wins [06 §3.2] P0-10
			bestIdx = i
			bestScore = score
		}
		// Equal score preserves first sampled candidate [06 §3.2] P0-10 (do not update on equality)
	}
	return bucket[bestIdx], true
}

// Acquisition carries one weapon slot's acquisition-time gates [06 §3.1] P0-10.
//
// Acquisition-time physical admission is separate from retention and firing
// [06 §3.1] P0-10: a non-water weapon requires shooter and candidate reference
// heights above sea level via Y > sea<<16, enforces the to-air target-status class when
// requested, optionally requires a ballistic solution via disc vs 0 exactly, then planar range.
// A water weapon applies two candidate depth/type predicates and planar range, skipping height test.
// Shot-gate never tests radar/cloak/jammer per P0-10 NEGATIVE-BOUNDED.
type Acquisition struct {
	ShooterX, ShooterZ numeric.Fixed
	// ShooterY is the shooter's top/reference height, tested against sea level
	// on the non-water branch via Y > sea<<16 [06 §3.1] P0-10 [06 §3.3] P0-10.
	ShooterY numeric.Fixed
	// SeaLevel is the map's sea level in world units [03 §2.2] P0-10.
	SeaLevel numeric.Fixed
	// Range is the weapon's ordinary fire range. Coverage is a SEPARATE scalar
	// for projectile-target/interceptor behavior and is not this radius
	// [06 §3.3] [06 §2.1] P0-10.
	Range         int32
	BadMask       uint32
	BadTargetMask content.CategoryMask
	MaskResolved  bool

	// WaterWeapon takes the water branch: the depth/type predicates and planar
	// range, with no sea-level height requirement [06 §3.1] P0-10.
	WaterWeapon bool
	// WaterAdmit is the water branch's two candidate depth/type predicates.
	// Water weapons skip height test and instead apply two candidate depth and
	// type predicates via bands (wy/wt/wl/mb) per [04 §9.1] P0-10 P0-11.
	// TODO(question): the water branch is selected off one bit of the weapon
	// definition's flag word, and whether that bit is `noautorange` or
	// `waterweapon` is not proved — the gate keeps a neutral name until it is.
	// Decider: static trace of the flag word's writer against [02 R-KEYS-01]'s
	// authored-key mapping. P0-10/P0-11.
	WaterAdmit func(c Candidate) bool

	// ToAir requests the to-air target-status class [06 §3.1] P0-10: only candidates
	// carrying that class are admitted.
	ToAir bool

	// Ballistic makes a ballistic solution a requirement of admission
	// [06 §3.1] P0-10. It is a requirement, not a default pass: with no solver, a
	// ballistic slot admits nothing rather than admitting everything.
	// Discriminant vs 0 exactly, pi/4 upper gate, trunc(a*32768/pi) per [06 §3.3] P0-10.
	Ballistic         bool
	BallisticFeasible func(c Candidate) bool

	// Visible completes the direct-visibility predicate by sampling the
	// candidate's target-bounds points against the player's visibility state
	// [06 §3.1] P0-10 — 4-point hull sampling per [03 §3.2] P0-11. The own-side, cloak and
	// underwater parts of the predicate are decided here from the candidate's
	// own fields; only the sampling needs the visibility service.
	Visible func(c Candidate) bool

	RNG *rng.Simulation

	// Secondary candidates for radar-like list consulted only when primary filtered empty && upgrade !=0 [06 §3.1] P0-11.
	// TODO(question): the targeting-upgrade aggregate is read off the unit
	// definition's second capability word (word B of [04 R-SPEC-01 §0]), but
	// which bit of it is not proved; the aggregate keeps a neutral name.
	// Decider: static trace of that word's writer against [02 R-KEYS-01]. P0-11.
	Secondary  []Candidate
	HasUpgrade bool // targetingUpgradeAggregate != 0 [06 §3.1] P0-11; see the marker above
}

// directlyVisible is the primary list's direct-visibility predicate
// [06 §3.1] P0-10 [03 §3.2] P0-11. It accepts own-side units, rejects cloaked units, rejects
// underwater units without their dedicated status bit 0x200, and samples multiple
// target-bounds points via Visible (4-point hull) [03 §3.2] P0-11.
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

// admits runs acquisition-time physical admission in the established order
// [06 §3.1] P0-10: heights against sea level via Y>sea<<16, the to-air class, the ballistic
// solution via disc vs 0 exactly, then planar range inclusive dist²<=range².
func (a *Acquisition) admits(c Candidate) bool {
	if a.WaterWeapon {
		// The water branch skips the sea-level height requirement entirely [06 §3.1] P0-10 P0-11.
		if a.WaterAdmit != nil && !a.WaterAdmit(c) {
			return false // two depth/type predicates via bands [04 §9.1] P0-11
		}
		return WithinRange(a.ShooterX, a.ShooterZ, c.X, c.Z, a.Range) // planar range inclusive P0-10
	}
	// Non-water: both reference heights must be strictly above sea level Y > sea<<16 [06 §3.1] P0-10 [06 §3.3] P0-10.
	if a.ShooterY <= a.SeaLevel || c.Y <= a.SeaLevel {
		return false
	}
	if a.ToAir && !c.AirTarget {
		return false // to-air target-status class enforced when requested [06 §3.1] P0-10
	}
	if a.Ballistic {
		if a.BallisticFeasible == nil || !a.BallisticFeasible(c) {
			return false // discriminant vs 0 exactly, pi/4, trunc per [06 §3.3] P0-10
		}
	}
	return WithinRange(a.ShooterX, a.ShooterZ, c.X, c.Z, a.Range) // [06 §3.3] P0-10 inclusive
}

// AcquireTarget performs ordinary automatic target acquisition for one weapon
// slot per [06 §3.1] [06 §3.2] [06 §3.3] P0-10.
//
// Candidates must arrive in deterministic pool-slot-ascending order (I1); this
// function does not sort via maps. Steps, in the order [06 §3] P0-10 gives them:
//
//  1. The primary list requires hostility and the direct-visibility predicate
//     [06 §3.1] P0-10, then acquisition-time physical admission: heights Y>sea, toAir, ballistic, planar range.
//  2. Randomly sample and remove at most 50 candidates from the input set via swap-remove RNG(remaining.len) [06 §3.2] P0-10.
//     len≤50 no sampling draw, >50 swap-remove 50x.
//  3. Partition into preferred (category clear of BadMask) and fallback
//     buckets; any preferred result wins over fallback [06 §3.1] P0-10.
//  4. Within each bucket, each candidate receives a shared-RNG score bounded
//     by high halves >>32 sum; bound<2→0 no-advance, strictly lower wins [06 §3.2] P0-10.
//
// Secondary radar-like list consulted only when primary filtered empty && HasUpgrade [06 §3.1] P0-11.
// Shot-gate never tests radar/cloak/jammer (NEGATIVE-BOUNDED) [06 §3.3] P0-10.
//
// Determinism: scan order is fixed (pool slot asc) (I1); no map iteration; the
// RNG draw order is authoritative (I4) P0-10.
func AcquireTarget(candidates []Candidate, a Acquisition) (pool.Handle, bool) {
	// Step 1: primary list gating and physical admission [06 §3.1] P0-10 [06 §3.3] P0-10.
	filtered := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.Hostile || !a.directlyVisible(c) {
			continue // primary list requires hostility + direct visibility [06 §3.1] P0-10
		}
		if !a.admits(c) {
			continue
		}
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		// Secondary status list consulted only when primary empty && upgrade [06 §3.1] P0-11
		// Not retried if primary existed but failed scoring [06 §3.1] P0-10.
		if a.HasUpgrade && len(a.Secondary) > 0 {
			// Build secondary filtered set with same gates
			secFiltered := make([]Candidate, 0, len(a.Secondary))
			for _, c := range a.Secondary {
				if !c.Hostile || !a.directlyVisible(c) {
					continue
				}
				if !a.admits(c) {
					continue
				}
				secFiltered = append(secFiltered, c)
			}
			if len(secFiltered) > 0 {
				filtered = secFiltered
			} else {
				return 0, false
			}
		} else {
			return 0, false
		}
	}

	// Step 2: random sample at most 50 candidates [06 §3.2] P0-10.
	var sampled []Candidate
	if len(filtered) <= 50 {
		// len≤50 no sampling draw P0-10
		sampled = filtered
		// Lock tie determinism to pool slot asc (I1) even if the caller supplied another order.
		sort.SliceStable(sampled, func(i, j int) bool { return sampled[i].Handle < sampled[j].Handle })
	} else {
		// >50 swap-remove RNG(remaining.len) 50x P0-10
		remaining := make([]Candidate, len(filtered))
		copy(remaining, filtered)
		sort.SliceStable(remaining, func(i, j int) bool { return remaining[i].Handle < remaining[j].Handle })
		sampled = make([]Candidate, 0, 50)
		for i := 0; i < 50 && len(remaining) > 0; i++ {
			var idx int
			if a.RNG != nil {
				idx = int(a.RNG.Uint32n(uint32(len(remaining)))) // bounded draw [01 §7.1] (I4) P0-10
			}
			sampled = append(sampled, remaining[idx])
			remaining[idx] = remaining[len(remaining)-1]
			remaining = remaining[:len(remaining)-1]
		}
		// Sampled order is now RNG-driven, not pool-asc; the scoring step's
		// first-sampled preservation is therefore RNG-influenced [06 §3.2] P0-10.
	}

	// Step 3: partition into preferred vs fallback [06 §3.1] P0-10.
	var preferred, fallback []Candidate
	for _, c := range sampled {
		isPreferred := IsPreferredCategory(c.Category, a.BadMask)
		if a.MaskResolved && c.CategoryMaskResolved {
			isPreferred = IsPreferredCategoryMask(c.CategoryMask, a.BadTargetMask)
		}
		if isPreferred {
			preferred = append(preferred, c)
		} else {
			fallback = append(fallback, c)
		}
	}

	// Step 4: scoring within each bucket; any preferred result wins over
	// fallback [06 §3.1] [06 §3.2] P0-10.
	if winner, ok := selectPreferredWinner(preferred, a.ShooterX, a.ShooterZ, a.RNG); ok {
		return winner.Handle, true
	}
	if winner, ok := selectPreferredWinner(fallback, a.ShooterX, a.ShooterZ, a.RNG); ok {
		return winner.Handle, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Retention / hysteresis per [06 §3.2] P0-10 (I1)
// ---------------------------------------------------------------------------

// ShouldRetain reports whether an automatic acquisition should retain its current live target
// without rerunning acquisition [06 §3.2] P0-10.
// Retention rechecks hostility, bad-target-category rejection, and paralyzer already-stunned exclusion [06 §3.2] P0-10.
// It otherwise keeps the current live target without re-running acquisition-time physical or sensor gates [06 §3.2] P0-10.
// Returns true to keep, false to drop and re-acquire.
func ShouldRetain(current Candidate, hostile bool, badMask uint32, stunned bool) bool {
	if current.Handle == 0 {
		return false // no target
	}
	if !hostile {
		return false // [06 §3.2] P0-10 rechecks hostility
	}
	if !IsPreferredCategory(current.Category, badMask) {
		return false // [06 §3.2] P0-10 bad-target-category rejection (retention stricter)
	}
	if stunned {
		return false // [06 §3.2] P0-10 paralyzer already-stunned exclusion
	}
	return true // retain without re-running physical/sensor gates [06 §3.2] P0-10
}

// ShouldRetainMask is the compiled-mask retention path [R-P0-03][06 §3.2].
func ShouldRetainMask(current Candidate, hostile bool, badMask content.CategoryMask, stunned bool) bool {
	if current.Handle == 0 || !hostile || stunned {
		return false
	}
	return IsPreferredCategoryMask(current.CategoryMask, badMask)
}

// ---------------------------------------------------------------------------
// The autonomous scan's per-slot admission [06 §3.2]
// ---------------------------------------------------------------------------

// AutonomousScanAdmitsSlot is the dropped and command-fire clauses of the
// autonomous target scan's per-slot admission [06 §3.2]: within a visited
// unit the three slots are processed in numeric order, and a slot is skipped
// unless (among the other clauses of that sentence) "its weapon is not
// `dropped`, and either the owning player's controller type is 2 (computer)
// or the weapon is **not** `commandfire`". A `dropped` weapon — one that
// falls under gravity rather than firing outward, e.g. a strafing bomb — is
// therefore never a candidate for autonomous acquisition, on any controller.
// The command-fire consequence is a separate contract: a human player's
// units never acquire autonomously with a command-fire weapon, and a
// computer player's do.
//
// ownerControlByte is the owning player row's control byte — 1 human, 2
// computer, 3 remote peer, 0 an unoccupied row [05 R-SHARE-01 §1] — read
// through Service.PlayerControlByteFor [06 R-DMG-01 §8]. Only the exact value 2
// opens the command-fire arm, so an unoccupied row (a fixture with no player
// table bound) reads as "not a computer" and a command-fire weapon there stays
// silent, which is the same answer a human's row gives.
//
// A human's command-fire weapon still fires: the manual attack path installs
// its target directly, and [06 §3.2] notes that forced/manual installation
// bypasses the autonomous visibility lists while still reaching the common
// shot-time physical gate. `AttackSpecial` is that path — it resolves command
// code 3 against its target, sets p1 = 2, and the resolved attack handler binds
// **slot 2** to the target from its next visit [04 R-ORD-01 §2][04 R-ORD-01 §3],
// which is a target install, not an acquisition, and is therefore not gated
// here. The same reasoning applies to a `dropped` weapon's manually installed
// target: this helper only gates the autonomous scan's own acquisition
// attempt, never a forced/manual installation reaching this slot by another
// path.
//
// The other clause of the same [06 §3.2] sentence — the two persisted slot
// flags — is a separate reader and is not this helper's business.
func AutonomousScanAdmitsSlot(weapon *content.WeaponDef, ownerControlByte uint8) bool {
	if weapon == nil {
		return false
	}
	if weapon.Dropped {
		return false // "its weapon is not `dropped`" [06 §3.2] — unconditional, any controller
	}
	if !weapon.CommandFire {
		return true // an ordinary weapon acquires for every controller [06 §3.2]
	}
	return ownerControlByte == ControlByteComputer // only controller type 2 [06 §3.2]
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

// ---------------------------------------------------------------------------
// The per-side target registry's third list — air bases
// [06 §3.1 "the third list"] [04 R-AIR-01 §11]
// ---------------------------------------------------------------------------

// AirBaseSeekRadius is the whole-world-unit radius the damaged-aircraft base
// seek admits candidates within, inclusive [04 R-AIR-01 §11]. It is compared as
// a square against the same truncated planar metric every other combat distance
// uses [06 §3.1].
const AirBaseSeekRadius int32 = 0xF00

// AirBaseRegistryPeriod is the target registry's rebuild cadence in ticks
// [06 §3.1]. The third list is cleared and refilled with the primary and
// secondary lists, so a scan can read a list up to this many ticks stale and
// can hold a unit that has since died or deactivated [04 R-AIR-01 §11].
const AirBaseRegistryPeriod uint32 = 30

// IsAirBaseListMember is the third list's membership test at rebuild
// [06 §3.1 "the third list"] [04 R-AIR-01 §11]: a fully built unit whose
// definition carries **both** `builder` **and** `isairbase` and whose
// activation bit is set. The rebuild classifies only units whose alive bit is
// set and whose death latch is clear [06 §3.1].
//
// Alliance is deliberately not part of this predicate: the registry's friendly
// branch reads the *candidate owner's* alliance row toward the registry's ally
// group, which is a one-directional read the caller owns
// [05 R-SHARE-01 §1][06 §3.1].
func IsAirBaseListMember(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	if !u.Alive || u.Dying {
		return false // alive bit set, death latch clear [06 §3.1]
	}
	if u.Remaining != 0 {
		return false // the friendly branch classifies fully built units only [06 §3.1]
	}
	return u.Def.Builder && u.Def.IsAirBase && u.Activated
}

// RebuildAirBaseList fills one ally group's third list from a unit array walked
// in slot order [06 §3.1] [04 R-AIR-01 §11] (I1). `declares` is the
// one-directional alliance row read of [05 R-SHARE-01 §1] — row A of the
// candidate's owner indexed by the registry's ally group — and is the friendly
// branch's own test; with none supplied only the ally group's own units are
// friendly, which is what a session with no player rows composes.
//
// The result is the list as of this instant. Retail refills it once every
// AirBaseRegistryPeriod ticks; see the caller for the staleness that cadence
// buys.
func RebuildAirBaseList(list []*units.Unit, allyGroup uint8, declares func(from, toward uint8) bool) []pool.Handle {
	var out []pool.Handle
	for _, u := range list {
		if !IsAirBaseListMember(u) {
			continue
		}
		friendly := u.Owner == allyGroup
		if !friendly && declares != nil {
			friendly = declares(u.Owner, allyGroup)
		}
		if !friendly {
			continue
		}
		out = append(out, u.Handle) // appended in unit-array order [06 §3.1]
	}
	return out
}

// ScanAirBaseList is the damaged-aircraft base seek's filter over one ally
// group's third list [04 R-AIR-01 §11]. It walks the list once, in list order,
// admitting an entry when its definition still carries `builder` and
// `isairbase`, its activation bit is still set, and the planar squared distance
// from (x, z) is at or below AirBaseSeekRadius squared — inclusive, on the
// truncated whole-world-unit metric of [06 §3.1].
//
// The three admission flags are re-tested; **liveness is not**. A pad destroyed
// since the rebuild is still offered, and the landing order's own pad query
// rejects it later [04 R-AIR-01 §6][04 R-AIR-01 §11]. Nothing is scored or
// sorted; entries are pushed in list order, and the caller's single RNG draw
// over the count is what picks one.
func ScanAirBaseList(x, z numeric.Fixed, list []pool.Handle, lookup func(pool.Handle) *units.Unit) []pool.Handle {
	if lookup == nil {
		return nil
	}
	var out []pool.Handle
	for _, h := range list {
		u := lookup(h)
		if u == nil || u.Def == nil {
			continue
		}
		if !u.Def.Builder || !u.Def.IsAirBase || !u.Activated {
			continue // the three flags are re-tested, liveness is not [04 R-AIR-01 §11]
		}
		if !WithinRange(x, z, u.X, u.Z, AirBaseSeekRadius) {
			continue // (dx² >> 32) + (dz² >> 32) <= 0xF00², inclusive [04 R-AIR-01 §11]
		}
		out = append(out, h)
	}
	return out
}
