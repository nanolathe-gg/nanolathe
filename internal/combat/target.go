// Package combat — targeting and acquisition per [06 §3] (WU-09-1).
//
// Acquisition scan rules, range vs coverage distinction, candidate selection
// order, and hysteresis are established per [06 §3]. Coverage drives overlay
// only [06 §3.3]; engagement uses Range. Candidate traversal excludes features
// because they are in a separate system [06 §3.1]. Iteration is deterministic
// (pool slot asc) [06 §1.2] (I1); no map iteration.
package combat

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TargetKind distinguishes how a slot's target is encoded [06 §3.2] (I13).
// Retail's low-level setters store either a unit target or a world position
// without validating alliance, category, sensor, range or ballistic feasibility [06 §3.2].
// TODO(question): exact manual unit/point encoding and command-fire replacement not fully closed [06 §3.2] missing/unknown.
type TargetKind uint8

const (
	TargetNone  TargetKind = iota // no target
	TargetUnit                    // unit target (pool handle) [06 §3.2]
	TargetPoint                   // world position [06 §3.2]
)

// Target is the per-slot encoded target state [06 §1.2] [06 §3.2] (I13).
// TODO(question): precise retail +offsets for encoded unit/point words remain untraced; role established per [06 §1.2].
type Target struct {
	Kind TargetKind    // [06 §3.2] (I13)
	Unit pool.Handle   // valid when Kind==TargetUnit; 0 null sentinel, no generation token [06 §5.1] (I13) [01 §6.1]
	X, Z numeric.Fixed // valid when Kind==TargetPoint or for fallback point when unit target lost [06 §3.2] (I13)
	Y    numeric.Fixed // height when point-target [06 §3.2] (I13) TODO(question): height encoding for point targets not fully closed
}

// IsUnitTarget reports whether the target names a unit slot [06 §3.2].
func (t Target) IsUnitTarget() bool { return t.Kind == TargetUnit && t.Unit != 0 }

// IsPointTarget reports whether the target is a world point [06 §3.2].
func (t Target) IsPointTarget() bool { return t.Kind == TargetPoint }

// ---------------------------------------------------------------------------
// Range vs coverage distinction [06 §3.3] [06 §2.1] [06 §11.2]
// ---------------------------------------------------------------------------

// WithinRange reports whether planar distance from shooter to candidate is within weapon Range [06 §3.3] [06 §2.1].
// Ordinary fire range uses horizontal distance against the weapon range [06 §3.3].
// Coverage is a separate scalar for projectile-target/interceptor behavior and is NOT the ordinary ground-target fire radius [06 §3.3] (coverage drives overlay only).
// This helper intentionally uses Range, not Coverage (I10).
//
// TODO(question): precise retail fixed-point scaling of range test (whether range is compared against trunc of hypot vs squared integer world units) is not fully closed; using squared integer truncation via Int() as deterministic placeholder.
func WithinRange(shooterX, shooterZ, candX, candZ numeric.Fixed, weaponRange int32) bool {
	if weaponRange < 0 {
		// TODO(question): negative range malformed state untraced [06 §2.1]; treating as no range.
		return false
	}
	dx := candX.Sub(shooterX) // [06 §3.3] planar delta
	dz := candZ.Sub(shooterZ)
	// Truncate toward zero to integer world units for range comparison [01 §8] I3. See I2/I3: world positions are Fixed 16.16.
	dxI := dx.Int()
	dzI := dz.Int()
	// Squared planar range test first at shot-time admission [06 §3.3].
	dist2 := dxI*dxI + dzI*dzI
	r2 := int64(weaponRange) * int64(weaponRange)
	return dist2 <= r2 // inclusive? TODO(question): inclusive vs exclusive bound not fully proved; using inclusive per typical range.
}

// WithinCoverageSquare reports whether the candidate's stored aim point lies within the interceptor's coverage square [06 §11.2].
// The scan uses an inclusive axis-aligned coverage square, not a radius circle [06 §11.2] [06 §3.3].
// Coverage is NOT ordinary fire range [06 §3.3] — this helper is for interceptor acquisition only [06 §11.2].
// Two axes implemented as wrapped unsigned comparisons around coverage in fixed-point units; for ordinary nonnegative coverage without overflow this is absolute distance [06 §11.2].
// TODO(question): wrapped unsigned handling for overflowed coverage not modeled; using absolute integer compare as placeholder.
func WithinCoverageSquare(shooterX, shooterZ, candX, candZ numeric.Fixed, coverage int32) bool {
	if coverage < 0 {
		// TODO(question): negative coverage untraced; treat as none.
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
	return dxI <= int64(coverage) && dzI <= int64(coverage) // inclusive bounds per [06 §11.2]
}

// ---------------------------------------------------------------------------
// Candidate selection order per [06 §3.1] [06 §3.2] (I1, I4)
// ---------------------------------------------------------------------------

// Candidate is one automatic candidate for a weapon slot's acquisition scan [06 §3.1] [06 §3.2].
// The input set for ordinary acquisition comes from per-player lists rebuilt on a cadence of at least 30 ticks,
// primary list requires hostility and a direct-visibility predicate that accepts own-side units, rejects cloaked,
// rejects underwater without dedicated status bit, and samples multiple target-bounds points [06 §3.1].
// Secondary status list is consulted only when primary in-radius set is empty and targeting-upgrade aggregate is active [06 §3.1].
// Traversal excludes map features because features are stored separately [06 §3.1].
// For WU-09-1 we model a simplified candidate shape; sensor and medium gates beyond range are TODO(question).
type Candidate struct {
	Handle   pool.Handle   // pool slot index, 0 null sentinel; iteration order is slot asc [06 §1.2] (I1) [01 §6.1]
	X, Z     numeric.Fixed // current world X/Z [06 §3.2]
	Category uint32        // decoded category bitset for this candidate [06 §3.1] TODO(question): Category string parsing into bitset not in content catalog yet
	Hostile  bool          // hostility for primary list [06 §3.1]
	Visible  bool          // direct-visibility predicate [06 §3.1]
	// TODO(question): additional gates not modeled here: cloaked, underwater status, to-air class, ballistic feasibility, water weapon depth/type, sonar/jammer [06 §3.1] missing/unknown.
}

// IsPreferredCategory reports whether candidate's category is clear of the slot's bad-target-category mask,
// entering the preferred bucket [06 §3.1]. A matching candidate (category & badMask !=0) enters the fallback bucket [06 §3.1].
// Any preferred result wins over fallback [06 §3.1].
func IsPreferredCategory(cat, badMask uint32) bool { return cat&badMask == 0 } // [06 §3.1]

// rngBoundForCandidate computes the shared-RNG bound for a candidate as the sum of the high 32-bit halves
// of its squared fixed-point X and Z deltas [06 §3.2]. A bound below two uses the RNG helper's zero/no-advance path [01 §7.1] (I4).
// TODO(question): precise retail quantization of Fixed delta squaring (whether 16.16 raw squared >>32 vs Int square) not fully closed; using raw product high-half sum as established approximation per [06 §3.2] wording.
func rngBoundForCandidate(dx, dz numeric.Fixed) uint32 {
	dxRaw := int64(dx.Raw()) // 16.16 raw [03 §2.1] I2
	dzRaw := int64(dz.Raw())
	// Product of two 16.16 values is 32.32 in 64 bits; high 32 bits after shift is integer part of world^2 [06 §3.2].
	// For negative deltas product is positive; shift arithmetic on positive preserves value.
	dx2 := dxRaw * dxRaw // 32.32
	dz2 := dzRaw * dzRaw
	// High 32 bits via >>32; for signed positive product arithmetic shift equals logical.
	highDx := uint32(uint64(dx2) >> 32)
	highDz := uint32(uint64(dz2) >> 32)
	bound := highDx + highDz // sum of high halves [06 §3.2]
	return bound             // may be 0.. large; <2 path is special [01 §7.1] I4
}

// selectPreferredWinner selects the winner from one bucket (preferred or fallback) using shared-RNG scores per [06 §3.2].
// Each candidate receives a shared-RNG score bounded by the sum of high halves of squared fixed deltas [06 §3.2].
// Bound <2 uses RNG zero/no-advance path [01 §7.1] (I4). Strictly lower score wins, equal preserves first sampled candidate in that bucket [06 §3.2].
// Determinism: iteration is bucket order as provided (which must be stable per I1); no map iteration; RNG draw order is authoritative [06 §3.2] (I4) (I1).
func selectPreferredWinner(bucket []Candidate, shooterX, shooterZ numeric.Fixed, rng *rng.Simulation) (Candidate, bool) {
	if len(bucket) == 0 {
		return Candidate{}, false
	}
	bestIdx := -1
	var bestScore uint32
	for i, c := range bucket {
		dx := c.X.Sub(shooterX)
		dz := c.Z.Sub(shooterZ)
		bound := rngBoundForCandidate(dx, dz) // [06 §3.2]
		var score uint32
		if rng != nil {
			score = rng.Uint32n(bound) // [01 §7.1] I4: bound<2 returns 0 without advance
		} else {
			// Without RNG, deterministic fallback is 0 (like bound<2 path); preserves first on tie.
			score = 0
		}
		if bestIdx == -1 || score < bestScore { // strictly lower wins [06 §3.2]
			bestIdx = i
			bestScore = score
		}
		// Equal score preserves first sampled candidate [06 §3.2] (do not update on equality)
	}
	return bucket[bestIdx], true
}

// AcquireTarget performs ordinary automatic target acquisition for one weapon slot per [06 §3.1] [06 §3.2] [06 §3.3].
// Candidates must be provided in deterministic pool-slot-asc order (I1); this function does not sort via maps.
// Steps established per [06 §3]:
//
//  1. Physical admission at acquisition time: for ordinary weapons, test planar range (and, when established, shooter height, to-air, ballistic, water depth predicates).
//     For WU-09-1 only the planar range gate is implemented; other predicates are TODO(question) [06 §3.1] missing/unknown.
//  2. Randomly sample and remove at most 50 candidates from the input set [06 §3.2]. For candidate counts ≤50 we preserve input order (no sampling); for >50 we sample deterministically via Simulation RNG (I4).
//     TODO(question): precise retail removal order (swap-with-last vs. Fisher-Yates order) not fully closed; using swap-with-last per commentary.
//  3. Partition into preferred (category clear of badMask) and fallback (matching) buckets [06 §3.1]; any preferred result wins over fallback [06 §3.1].
//  4. Within each bucket, each candidate receives a shared-RNG score bounded by the sum of high halves of squared fixed deltas [06 §3.2]; bound<2 uses zero/no-advance [01 §7.1] I4; strictly lower wins, equal preserves first sampled [06 §3.2].
//  5. Coverage vs engagement: ordinary acquisition uses weapon Range, not Coverage; Coverage is a separate scalar for projectile-target/interceptor square and drives overlay only [06 §3.3] [06 §2.1].
//
// Unstated details are marked TODO(question) per WU-09-1 scope.
// Determinism: scan order fixed (pool slot asc) (I1); no map iteration; RNG draw order is authoritative (I4).
func AcquireTarget(candidates []Candidate, shooterX, shooterZ numeric.Fixed, weaponRange int32, badMask uint32, rng *rng.Simulation) (pool.Handle, bool) {
	// Step 1: filter by hostility and visibility (primary list gating) [06 §3.1] and planar range admission [06 §3.1] [06 §3.3].
	// For WU-09-1 minimal, we require Hostile && Visible && WithinRange; other gates TODO.
	filtered := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.Hostile || !c.Visible {
			continue // [06 §3.1] primary list requires hostility + direct-visibility predicate
		}
		if !WithinRange(shooterX, shooterZ, c.X, c.Z, weaponRange) { // [06 §3.3] range vs coverage established
			continue // acquisition-time planar range [06 §3.1]
		}
		// TODO(question): [06 §3.1] additional acquisition-time gates not modeled: non-water requires shooter+ candidate heights above sea level, to-air class, optional ballistic solution; water applies depth/type predicates; some branches bypass.
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		return 0, false
	}

	// Step 2: random sample at most 50 candidates [06 §3.2].
	// TODO(question): exact retail sampling algorithm (which RNG draws, removal swap order, whether sampling preserves original pool order vs sampled order) not fully closed; using deterministic swap-with-last sampling per I4/I1.
	var sampled []Candidate
	if len(filtered) <= 50 {
		// ≤50: keep all in deterministic pool-asc order (caller's order) — already stable per I1.
		sampled = filtered
	} else {
		// >50: randomly sample 50 without replacement via Simulation RNG [06 §3.2] (I4).
		// Copy to mutable remaining slice sorted asc (I1) and draw indices.
		remaining := make([]Candidate, len(filtered))
		copy(remaining, filtered)
		// Ensure deterministic iteration: sort remaining by Handle asc (stable) per I1 if caller violated order.
		sort.SliceStable(remaining, func(i, j int) bool { return remaining[i].Handle < remaining[j].Handle })
		sampled = make([]Candidate, 0, 50)
		for i := 0; i < 50 && len(remaining) > 0; i++ {
			var idx int
			if rng != nil {
				idx = int(rng.Uint32n(uint32(len(remaining)))) // bounded draw [01 §7.1] (I4)
			} else {
				idx = 0
			}
			sampled = append(sampled, remaining[idx])
			// Remove by swap-with-last to keep O(1) and determinism without preserving order of remaining.
			remaining[idx] = remaining[len(remaining)-1]
			remaining = remaining[:len(remaining)-1]
		}
		// Note: sampled order is now RNG-driven, not pool-asc; scoring's first-sampled preservation is therefore RNG-influenced [06 §3.2].
		// For small sets where determinism on tied candidates is tested, we stay in ≤50 path so order remains pool-asc.
	}

	// Ensure sampled is at least deterministic for ≤50: sort if we didn't sample? Already pool-asc from input.
	if len(filtered) <= 50 {
		// Sort sampled by Handle asc to lock tie determinism to pool slot asc (I1).
		// Input was promised asc but we enforce it so a test that shuffles still shows asc win.
		sort.SliceStable(sampled, func(i, j int) bool { return sampled[i].Handle < sampled[j].Handle })
	}

	// Step 3: partition into preferred vs fallback [06 §3.1].
	var preferred, fallback []Candidate
	for _, c := range sampled {
		if IsPreferredCategory(c.Category, badMask) { // [06 §3.1]
			preferred = append(preferred, c)
		} else {
			fallback = append(fallback, c)
		}
	}

	// Step 4: scoring within each bucket [06 §3.2].
	// Any preferred result wins over fallback [06 §3.1].
	if winner, ok := selectPreferredWinner(preferred, shooterX, shooterZ, rng); ok {
		return winner.Handle, true // [06 §3.1] preferred wins
	}
	if winner, ok := selectPreferredWinner(fallback, shooterX, shooterZ, rng); ok {
		return winner.Handle, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Retention / hysteresis per [06 §3.2] (I1)
// ---------------------------------------------------------------------------

// ShouldRetain reports whether an automatic acquisition should retain its current live target
// without rerunning acquisition [06 §3.2].
// Retention rechecks hostility, bad-target-category rejection, and paralyzer already-stunned exclusion [06 §3.2].
// It otherwise keeps the current live target without re-running acquisition-time physical or sensor gates [06 §3.2].
// Returns true to keep, false to drop and re-acquire.
// TODO(question): exact live/target validity beyond hostility/category/stun, and precise paralyzer stun flag store, not fully closed [06 §3.2] [06 §10].
func ShouldRetain(current Candidate, hostile bool, badMask uint32, stunned bool) bool {
	if current.Handle == 0 {
		return false // no target
	}
	if !hostile {
		return false // [06 §3.2] rechecks hostility
	}
	if !IsPreferredCategory(current.Category, badMask) {
		// Actually retention is stricter and rejects a retained unit whose category is in that bad-target mask [06 §3.1] [06 §3.2].
		// Any preferred result wins at acquisition, but retention rejects even fallback.
		// So if candidate now matches badMask (i.e., not preferred), retention fails.
		return false // [06 §3.2] bad-target-category rejection
	}
	if stunned {
		return false // [06 §3.2] paralyzer already-stunned exclusion
	}
	// TODO(question): [06 §3.2] visibility/sensor/range/medium/aircraft/ballistic not rechecked on retention; we model as kept.
	return true // retain without re-running physical/sensor gates [06 §3.2]
}

// IsValidAcquisitionCandidate is a test helper that mirrors the filter gate in AcquireTarget for unit tests.
// It reports whether candidate would pass the acquisition-time admission for range (and hostility/visibility).
func IsValidAcquisitionCandidate(c Candidate, shooterX, shooterZ numeric.Fixed, weaponRange int32) bool {
	if !c.Hostile || !c.Visible {
		return false
	}
	return WithinRange(shooterX, shooterZ, c.X, c.Z, weaponRange) // [06 §3.3] [06 §3.1]
}
