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
	"github.com/nanolathe/nanolathe/internal/world"
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

// PointTargetHeight is the height half of the per-slot target-point resolver
// [06 R-WPN-04 §1]. For a point target the resolver promotes the slot's two
// stored words to 16.16 for X and Z and answers
//
//	Y = max(bilinearTerrainHeight(X, Z), seaLevelByte) << 16
//
// — the four-corner bilinear query of [03 §2.3], floored at the map's sea-level
// byte so a ground point below the water plane is aimed at the SURFACE rather
// than at the sea bed. No lead is ever applied to a point target.
//
// The bilinear query answers the raw −1 sentinel on the map's last row and
// column [03 §2.3]; the sentinel loses the maximum to sea level for free, which
// is how the cursor's own ground query treats it [07 §8].
//
// The height used to be left at zero for every point target, which put the aim
// point at the map's zero plane instead of on the ground the order named: the
// solved pitch dived tens of world units below the click, and the shot-time
// range gate measured to that same wrong point.
func PointTargetHeight(terrain *world.Terrain, x, z numeric.Fixed) numeric.Fixed {
	if terrain == nil {
		return 0
	}
	h := int32(terrain.HeightAt(x, z) >> 16) // whole world units; −1 is the sentinel
	if sea := int32(terrain.SeaLevel); h < sea {
		h = sea
	}
	return numeric.Fixed(int64(h) << 16)
}

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
// The secondary (seen-bit) list is consulted only when the primary walk produced nothing and the
// registry's targeting-upgrade gate is set, and it receives no visibility re-test [06 §3.1] P0-10.
type Candidate struct {
	Handle   pool.Handle   // pool slot index, 0 null sentinel; iteration order is slot asc [06 §1.2] P0-10 (I1) [01 §6.1]
	X, Z     numeric.Fixed // current world X/Z [06 §3.2] P0-10
	Y        numeric.Fixed // current world Y; the height clauses take its whole-unit word plus ModelTop [06 §3.1] P0-10 (I13)
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
	// ModelTop is the candidate definition's model total-height whole-unit
	// word, the addend of the target height clause [06 §3.1][03 R-P0-18-A §1].
	ModelTop int32
	// MoverMode is the candidate's committed mover mode, the operand of the
	// `toairweapon` clause: it must read exactly 2 [06 R-WPN-05 §1] clause 3
	// [04 R-MOV-01 §8]. The `AirTarget` field that stood here carried the
	// definition's `canfly` instead, which is a different question — a landed
	// aircraft still carries `canfly` — and [02 R-KEYS-01 §2] recorded the
	// operand as the one inference of `toairweapon`'s reader census until
	// [06 R-WPN-05 §1] closed it on the mover mode.
	MoverMode uint8
	// Floater and CanHover are the candidate definition's capability-word A
	// bits 19 and 12, read only by the water branch [04 R-SPEC-01 §0][06 §3.1].
	Floater, CanHover bool
	// Stunned carries the candidate's stunned mark. Exactly one gate reads it —
	// a paralyzer weapon rejects a candidate already carrying it, check 5 of the
	// picked-candidate order [06 §3.2] — and nothing else does: the mark is a
	// note ON the victim for other units' scans and disables nothing on the
	// victim itself [06 R-DMG-01 §11].
	Stunned bool
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
	// ShooterY is the shooter's world Y. The non-water branch tests its
	// whole-unit word plus ShooterModelTop against the sea-level byte, strictly
	// greater [06 §3.1] [06 R-WPN-05 §1].
	ShooterY numeric.Fixed
	// ShooterModelTop is the shooter definition's model total-height whole-unit
	// word, the addend of that clause [03 R-P0-18-A §1].
	ShooterModelTop int32
	// SeaLevel is the map's sea level in world units [03 §2.2] P0-10; the gate
	// compares against its whole-unit byte.
	SeaLevel numeric.Fixed
	// Range is the weapon's ordinary fire range. Coverage is a SEPARATE scalar
	// for projectile-target/interceptor behavior and is not this radius
	// [06 §3.3] [06 §2.1] P0-10.
	Range         int32
	BadMask       uint32
	BadTargetMask content.CategoryMask
	MaskResolved  bool

	// Paralyzer marks the slot's weapon as a paralyzer, which is the only thing
	// that makes a candidate's stunned mark matter: "a paralyzer weapon rejects
	// a candidate already carrying the stunned bit", check 5 of the
	// picked-candidate order [06 §3.2]. An ordinary weapon ignores the mark
	// [06 R-DMG-01 §11].
	Paralyzer bool

	// WaterWeapon takes the water branch: the target's `floater`/`canhover`
	// pair and planar range, with no shooter-side, air or ballistic clause
	// [06 §3.1] [06 R-WPN-05 §1] clause 1.
	//
	// The selecting bit is settled [06 R-WPN-05 §7]: BOTH admission gates —
	// this acquisition-time one and the shot-time one of [06 §3.3] — branch on
	// bit 16 of the weapon definition's flag word, the bit the parser writes
	// for the authored key `waterweapon` [02 R-KEYS-01]. `noautorange` (bit 27)
	// is read only by the expiry rule [06 §6.3][06 §7.3] and plays no part in
	// admission.
	//
	// The pair used to reach this gate only through an optional `WaterAdmit`
	// closure, which nothing installed: every water weapon therefore admitted
	// every candidate at any depth. The clauses are part of the gate proper now
	// and read the candidate's own Floater/CanHover fields.
	WaterWeapon bool

	// ToAir is the weapon's `toairweapon` flag [06 §3.1] P0-10: the candidate's
	// committed mover mode must read exactly 2 [06 R-WPN-05 §1] clause 3.
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
	//
	// AcquireTarget does not read it. The predicate belongs to the registry
	// rebuild, which files the primary list up to thirty ticks before an
	// acquisition filters it [06 §3.1]; this field serves the single-candidate
	// question IsValidAcquisitionCandidate answers for the reaction offer.
	Visible func(c Candidate) bool

	RNG *rng.Simulation

	// Secondary is the registry's SECONDARY candidate list, consulted only when
	// the primary walk produced nothing and the gate below is set [06 §3.1].
	//
	// It is the *seen*-bit list: the registry rebuild files a hostile candidate
	// here when its runtime seen status bit is set, independently of whether the
	// direct-visibility predicate put it on the primary list, so a unit can be
	// on both lists, either, or neither [06 §3.1]. The seen bit is the sensor
	// phase's, recomputed every tick from the LOCAL player's point of view
	// [03 §3.2][R-VIS-01 §4], which is why a computer opponent's fallback
	// acquisition inherits the human's sensors.
	//
	// The list receives NO visibility re-test at acquisition: "the secondary
	// list was populated from the *seen* bit at rebuild, and that is the only
	// sensor test it ever receives" [06 §3.1] (refinement of 2026-09-02).
	// Service.acquireTargetForSlotRange fills it from the registry.
	Secondary []Candidate
	// HasUpgrade is the registry's secondary-list gate [06 §3.1] (refinement
	// of 2026-09-02) [04 R-SPEC-01 §8]. It is a BOOLEAN, not a sum: the
	// rebuild writes 1 when any unit the scanning player itself owns — the
	// same player slot, allies excluded — is alive, not dying, complete and
	// activated and carries `istargetingupgrade`. The shooter's own definition
	// plays no part. TargetingUpgradeGate computes it over a unit array;
	// Service.rebuildTargetRegistry computes it on the registry's cadence and
	// Service.acquireTargetForSlotRange hands it here.
	HasUpgrade bool
}

// TargetingUpgradeGate computes the registry's secondary-list gate for the
// player owning slot `owner` [06 §3.1] [04 R-SPEC-01 §8]: true when at least
// one unit in `list` is alive, not dying, owned by that same player (allied
// players' units do not count), complete (remaining build fraction exactly
// zero) and activated (paralysis does not clear the bit), and whose
// definition carries `istargetingupgrade`. Nothing is counted or summed.
// Callers pass units in slot order; the result is order-independent (I1).
func TargetingUpgradeGate(list []*units.Unit, owner uint8) bool {
	for _, u := range list {
		if unitOpensTargetingUpgradeGate(u, owner) {
			return true
		}
	}
	return false
}

// unitOpensTargetingUpgradeGate is that predicate for one unit, so the
// registry's single rebuild walk and the exported whole-array form share one
// body [06 §3.1]. "Friendly" here means the SAME PLAYER, not the same ally
// group: the rebuild's counting branch is entered only when the candidate's
// owner slot equals the registry owner's, so an ally's targeting-upgrade unit
// never opens this gate for you (refinement of 2026-09-02, point 2).
func unitOpensTargetingUpgradeGate(u *units.Unit, owner uint8) bool {
	if u == nil || !u.Alive || u.Dying || u.Owner != owner {
		return false
	}
	if u.Remaining != 0 || !u.Activated || u.Def == nil {
		return false // complete (build fraction exactly zero) and activated
	}
	return u.Def.IsTargetingUpgrade // word A bit 10 [04 R-SPEC-01 §0][06 R-WPN-05 §7]
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

// admits runs acquisition-time physical admission — the unit-to-unit gate of
// [06 §3.1], which [06 R-WPN-05 §1] and [06 R-WPN-05 §9] give as one routine
// with these clauses in this order: the water branch's `floater`/`canhover`
// pair, or the non-water branch's two whole-unit height clauses and the
// `toairweapon` mover-mode test; then the ballistic solution; then planar range
// LAST, inclusive dist² <= range².
//
// The height clauses used to be bare 16.16 compares of the raw positions
// against sea level (`a.ShooterY <= a.SeaLevel || c.Y <= a.SeaLevel`). That is
// a different predicate: retail truncates each end to its whole-unit word and
// adds the definition's model top-height word before comparing against the
// sea-level BYTE, so a hull sitting at or under the waterline is still admitted
// whenever its model reaches above it, and a unit whose Y is one fraction above
// sea level but whose model top is zero is not.
//
// unitToUnitAdmitsBeforeRange is shared with CanEngageSlotTarget so the gate
// has exactly one body [06 R-WPN-05 §9].
func (a *Acquisition) admits(c Candidate) bool {
	shooter := unitGateEnd{Y: wholeYWord(a.ShooterY), ModelTop: a.ShooterModelTop}
	target := unitGateEnd{
		Y:         wholeYWord(c.Y),
		ModelTop:  c.ModelTop,
		MoverMode: c.MoverMode,
		Floater:   c.Floater,
		CanHover:  c.CanHover,
	}
	if !unitToUnitAdmitsBeforeRange(shooter, target, wholeYWord(a.SeaLevel), a.WaterWeapon, a.ToAir) {
		return false
	}
	if !a.WaterWeapon && a.Ballistic {
		// The water branch tests no ballistic feasibility [06 §3.1].
		if a.BallisticFeasible == nil || !a.BallisticFeasible(c) {
			return false // discriminant vs 0 exactly, pi/4, trunc per [06 §3.3] P0-10
		}
	}
	return WithinRange(a.ShooterX, a.ShooterZ, c.X, c.Z, a.Range) // LAST clause [06 §3.1] P0-10 inclusive
}

// wholeYWord is retail's `(int16)(Y >> 16)` on a 16.16 world value — the same
// truncation wholeY applies to a unit, for the operands this gate carries as
// plain Fixed values [06 §3.1]. The map's sea level is stored as a byte and
// reaches here as byte<<16, so the same conversion recovers it exactly.
func wholeYWord(v numeric.Fixed) int32 { return int32(int16(v.Raw() >> 16)) }

// rejectsStunned is check 5 of the picked-candidate order [06 §3.2]: "a
// paralyzer weapon rejects a candidate already carrying the stunned bit". It is
// paralyzer-only — an ordinary weapon happily re-targets a stunned unit, and
// nothing else in the engine reads the mark [06 R-DMG-01 §11].
//
// The check sits after the physical gate here because that is its position in
// the section's list; this build applies the picked-candidate checks to the
// whole set before sampling rather than to each pick, which is a pre-existing
// difference from [06 §3.2] and not this predicate's.
func (a *Acquisition) rejectsStunned(c Candidate) bool {
	return a.Paralyzer && c.Stunned
}

// AcquireTarget performs ordinary automatic target acquisition for one weapon
// slot per [06 §3.1] [06 §3.2] [06 §3.3] P0-10.
//
// The candidates are the registry's PRIMARY list as the caller materialized it,
// in unit-array order (I1); this function does not sort via maps. Steps, in the
// order [06 §3] P0-10 gives them:
//
//  1. The per-attempt filter and acquisition-time physical admission: heights
//     Y>sea, toAir, ballistic, planar range. NO visibility test — the
//     direct-visibility predicate ran at the registry rebuild, up to thirty
//     ticks ago [06 §3.1].
//  2. Randomly sample and remove at most 50 candidates from the input set via swap-remove RNG(remaining.len) [06 §3.2] P0-10.
//     len≤50 no sampling draw, >50 swap-remove 50x.
//  3. Partition into preferred (category clear of BadMask) and fallback
//     buckets; any preferred result wins over fallback [06 §3.1] P0-10.
//  4. Within each bucket, each candidate receives a shared-RNG score bounded
//     by high halves >>32 sum; bound<2→0 no-advance, strictly lower wins [06 §3.2] P0-10.
//
// The secondary (seen-bit) list is consulted only when the primary walk filtered empty and
// HasUpgrade is set; it gets the same distance/liveness test and NO visibility re-test [06 §3.1].
// Shot-gate never tests radar/cloak/jammer (NEGATIVE-BOUNDED) [06 §3.3] P0-10.
//
// Determinism: scan order is fixed (pool slot asc) (I1); no map iteration; the
// RNG draw order is authoritative (I4) P0-10.
func AcquireTarget(candidates []Candidate, a Acquisition) (pool.Handle, bool) {
	// Step 1: the per-attempt filter over the registry's primary list, then
	// physical admission [06 §3.1][06 §3.3].
	//
	// There is no visibility test here, and there must not be one: the
	// direct-visibility predicate ran when the registry filed these candidates,
	// and "no visibility, category, sensor, medium, alliance or range test
	// happens at this point" [06 §3.1]. Running it again would make a listed
	// candidate that has since gone dark unshootable and would erase the
	// staleness the section makes a contract. Service.primaryCandidates applies
	// the liveness and hostility half of the filter while materializing the
	// list; the distance test is a.admits' last clause, shared with the
	// secondary walk below.
	filtered := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.Hostile {
			continue // both lists are built hostile-only at rebuild [06 §3.1]
		}
		if !a.admits(c) {
			continue
		}
		if a.rejectsStunned(c) {
			continue // check 5: a paralyzer rejects an already-stunned candidate [06 §3.2]
		}
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		// Secondary status list consulted only when primary empty && upgrade [06 §3.1] P0-11
		// Not retried if primary existed but failed scoring [06 §3.1] P0-10.
		if a.HasUpgrade && len(a.Secondary) > 0 {
			// The secondary walk is the primary walk MINUS the visibility
			// predicate. [06 §3.1] (refinement of 2026-09-02): the filter
			// "applies to it the **same** test as the primary walk — planar
			// d² ≤ r² on the truncated whole-unit metric, alive bit set, death
			// latch clear — with no visibility re-test: the secondary list was
			// populated from the *seen* bit at rebuild, and that is the only
			// sensor test it ever receives."
			//
			// directlyVisible used to run here as well, which made the fallback
			// list a second copy of the primary list: every entry it could admit
			// the primary walk had already admitted, so the gate could never
			// produce a target the primary walk had not.
			//
			// Liveness is re-tested by the caller when it materializes these
			// candidates from the registry's stored handles, because the list is
			// up to thirty ticks stale [06 §3.1].
			secFiltered := make([]Candidate, 0, len(a.Secondary))
			for _, c := range a.Secondary {
				if !c.Hostile {
					continue // both lists are built hostile-only at rebuild [06 §3.1]
				}
				if !a.admits(c) {
					continue
				}
				if a.rejectsStunned(c) {
					continue // check 5 again on the secondary list [06 §3.2]
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
// Retention per [06 §3.2] P0-10 (I1)
// ---------------------------------------------------------------------------
//
// Removed (WU-19-80): `ShouldRetain` and `ShouldRetainMask` stood here as the
// retention predicate. Neither had a production caller — the scan's one
// retention site is in service.go's per-slot loop — and both rejected a stunned
// target for EVERY weapon, where [06 §3.2] rejects it only when the slot's
// weapon is a paralyzer. Two exported helpers that answer the question wrongly
// are worse than none: the live site now applies the paralyzer clause itself,
// with the drop written where the stale/dead drop already lives.

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
// AirBaseRegistryPeriod ticks; AirBaseRegistry is the holder that applies that
// cadence.
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
// The three admission flags are re-tested; **liveness is not**. A pad whose
// death latch was set since the rebuild is still offered, and the landing
// order's own pad query rejects it later [04 R-AIR-01 §6][04 R-AIR-01 §11].
// Nothing is scored or sorted; entries are pushed in list order, and the
// caller's single RNG draw over the count is what picks one.
//
// Retail's aliasing window is reproduced on BOTH halves, and both markers that
// used to stand here are retired.
//
// The third list holds raw record addresses into the fixed unit-record array,
// and the scan loads the definition pointer, the activation byte and the two
// position words straight off the stored address with no identity or liveness
// check. Every player's slice of that array is bounded once at session entry
// and never resized, so a stale entry always addresses a real record — it is
// never unmapped — and what it reads is whatever occupies that record now. The
// death path frees the record at slot-end of the tick the latch set [04 §5.4],
// so a pad's address can be re-occupied on any of the up-to-29 ticks left in
// the rebuild window.
//
// Reused half: `lookup` is the world's ordinary accessor, so a handle whose
// slot has been reallocated resolves to the new occupant, per I5's
// no-generation-tags rule, and the flags are re-tested against that occupant
// exactly as retail re-tests them against the record.
//
// Freed-but-not-reused half: retail's record release overwrites the record's
// definition pointer with the reserved `None` definition, which carries neither
// `builder` nor `isairbase`, so the first admission test rejects the entry
// before the stale activation byte or the stale position words are reached
// [04 R-AIR-01 §11 "The record release, and what a freed pad reads back as"].
// `lookup` returning nil drops the same entry. Same outcome by a different
// mechanism, so the earlier reading — that this was a narrowing needing a raw
// slot accessor from internal/units — is withdrawn: nothing is owed upstream.
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

// AirBaseRegistry holds the third list per ally group across ticks, so that a
// scan between rebuilds reads the snapshot the last rebuild left
// [06 §3.1][04 R-AIR-01 §11].
//
// Staleness is the point, not an artefact. Because the list is cleared and
// refilled only on the cadence, and because the scan re-tests the three
// admission flags but never liveness:
//
//   - a pad that died inside the window is still offered, and the landing
//     order's own pad query rejects it later [04 R-AIR-01 §6];
//   - a pad that finished building inside the window is not offered until the
//     next rebuild.
//
// The rows are indexed by ally group, and a session has ten player slots
// [05 "Player slot"], so ten rows cover every group. Iteration is over that
// fixed index range, never a map (I1).
type AirBaseRegistry struct {
	lists [10][]pool.Handle
}

// Rebuild refills every ally group's list from the unit array, but only on the
// registry's cadence: `globalTick % AirBaseRegistryPeriod == 0` [06 §3.1], the
// same throttle expression the severity sampler uses for the same period
// [04 §9.2]. On every other tick it does nothing, which is what leaves the
// snapshot stale. Before the first rebuild every list is empty, as retail's
// registry is until its first walk.
//
// One rebuild walks the array once, in slot order, and files each member into
// every ally group whose row admits it (I1) — the row read is the candidate
// owner's, indexed by the group [05 R-SHARE-01 §1].
func (r *AirBaseRegistry) Rebuild(tick uint32, list []*units.Unit, declares func(from, toward uint8) bool) {
	if !r.RebuildDue(tick) {
		return
	}
	for group := range r.lists {
		r.lists[group] = r.lists[group][:0] // cleared with the other two lists [06 §3.1]
	}
	for _, u := range list {
		if !IsAirBaseListMember(u) {
			continue
		}
		for group := range r.lists {
			friendly := int(u.Owner) == group
			if !friendly && declares != nil {
				friendly = declares(u.Owner, uint8(group))
			}
			if friendly {
				r.lists[group] = append(r.lists[group], u.Handle) // unit-array order [06 §3.1]
			}
		}
	}
}

// RebuildDue reports whether tick falls on the registry's rebuild cadence
// [06 §3.1] [04 R-AIR-01 §11]. Rebuild applies the same test itself; this is
// the predicate a caller uses so it need not materialise the unit list and the
// alliance row on the twenty-nine ticks in thirty where Rebuild reads neither.
func (r *AirBaseRegistry) RebuildDue(tick uint32) bool {
	return r != nil && tick%AirBaseRegistryPeriod == 0
}

// List returns one ally group's third list as the last rebuild left it. The
// slice is the registry's own storage; callers filter it into a fresh vector
// with ScanAirBaseList and never write through it.
func (r *AirBaseRegistry) List(allyGroup uint8) []pool.Handle {
	if r == nil || int(allyGroup) >= len(r.lists) {
		return nil
	}
	return r.lists[allyGroup]
}

// AirBelowThreeQuarters is the health test every damaged-aircraft base seek
// shares, written out by [04 R-AIR-01 §11] as
//
//	(uint)(int16)health < (MaxDamage >> 2) * 3
//
// — the 16-bit health field sign-extended and compared unsigned against three
// quarters of the definition's `MaxDamage`, the quarter formed by a truncating
// shift, strict. It lives here beside the list it gates so the six air legs and
// the two patrol rows share one expression rather than three spellings of it
// [04 R-AIR-01 §7][04 R-AIR-01 §8][04 R-ORD-01 §7][04 R-ORD-02 §2]
// [04 R-ORD-02 §3].
//
// An overkilled aircraft's negative health sign-extends to a very large
// unsigned value and is therefore NOT below three quarters. With no definition
// word there is no threshold.
func AirBelowThreeQuarters(u *units.Unit) bool {
	if u == nil || u.Def == nil || u.Def.MaxDamage <= 0 {
		return false
	}
	return uint32(int32(int16(u.Health))) < uint32((u.Def.MaxDamage>>2)*3)
}
