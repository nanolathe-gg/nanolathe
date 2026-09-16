// Targeting and acquisition per [06 §3] P0-10 (WU-09-1).
//
// Acquisition scan rules, range vs coverage distinction, candidate selection
// order, and hysteresis are established per [06 §3] P0-10. Coverage drives overlay
// only [06 §3.3]; engagement uses Range. Candidate traversal excludes features
// because they are in a separate system [06 §3.1]. Iteration is deterministic
// for registry walks (pool slot asc) and sampled picks (shared RNG) [06 §3.2];
// no map iteration.

package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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

// PreFireLeadGate reports whether the five gates of the pre-fire lead
// [06 §3.3] all pass for this shot. The lead runs when ALL of:
//
//   - the slot's armed bit is set — bit 1 of the slot control byte, the one
//     the slot initializer writes for every slot whose weapon link resolved
//     [06 R-WPN-05 §3];
//   - the weapon is NOT `cruise` — cruise suppresses the only lead in the
//     whole weapon pipeline [06 §6.7];
//   - the target unit has a MOVEMENT RECORD. Buildings are exactly the units
//     with no mover [04 R-COLL-01 §1], and this build's mover records are
//     created for the non-building units, i.e. the ones whose definition
//     authors `bmcode` [04 R-PATH-01 §14];
//   - the shooter's credited-kill count is STRICTLY GREATER THAN FIVE. The
//     count is an unsigned 16-bit field and the lead gate is the only
//     `>`-form consumer of it in the whole engine; every other one divides
//     [06 R-DMG-01 §8]. A unit therefore starts leading on its sixth kill,
//     one kill after the panel stops printing a number;
//   - `weaponvelocity` is nonzero — it is the divisor of the flight time.
//
// The target's motion enters the firing solution here and nowhere else: no
// spread term, no drift gate and no in-flight guidance reads it
// [06 R-WPN-03 §3][06 §6.7].
func PreFireLeadGate(shooter, target *units.Unit, slot *units.Slot, w *content.WeaponDef) bool {
	if shooter == nil || target == nil || slot == nil || w == nil {
		return false
	}
	if !slot.IsEnabled() {
		return false // the armed bit [06 §3.3][06 R-WPN-05 §3]
	}
	if w.Cruise {
		return false // `cruise` suppresses the lead [06 §3.3][06 §6.7]
	}
	if target.Def == nil || target.Def.BMCode != 1 {
		return false // no movement record [06 §3.3][04 R-COLL-01 §1]
	}
	// Unsigned, strict [06 §3.3][06 R-DMG-01 §8]. The field is a 16-bit
	// unsigned counter that wraps at 65,536, so the comparison is made on the
	// low sixteen bits and a wrapped count is small again.
	if uint16(shooter.Kills) <= 5 {
		return false
	}
	return w.WeaponVelocity != 0
}

// PreFireLeadPoint applies the pre-fire lead of [06 §3.3] to an already
// resolved target point and answers the led point. It is the ONLY lead in the
// weapon pipeline: projectile guidance is pure pursuit and adds no
// target-velocity term in flight [06 §6.7].
//
// With `s` the SHOOTER's own world point (not the muzzle — the lead runs at
// target-point resolution, before the aim origin is queried) and `point` the
// resolved target point, both raw 16.16:
//
//	D  = trunc(sqrt((dX*dX + dY*dY) + dZ*dZ))    ; dX = s.X - point.X, etc.
//	T  = (int64(D) << 16) / weaponvelocity       ; signed 64-bit divide
//	T2 = (int64(T) * 0xcccc) >> 16               ; 52,428/65,536 = 0.79998779…
//	point.axis += int32((int64(velocity.axis) * T2) >> 16)
//
// The 0.8 factor and the six-kill threshold are read from the image, not
// chosen [06 §3.3]. Note that this distance is THREE-dimensional while the
// range test of the same section is planar — the two are different quantities
// and neither may be substituted for the other.
//
// `weaponvelocity` reaches the record already scaled to 16.16 per tick
// [02 "Weapon record"][I8], so `T` is a flight time in ticks expressed in
// 16.16, and the per-axis product converts it back against a per-tick
// velocity. The addend is narrowed to a signed 32-bit word before it is added,
// which is retail's store width for a coordinate.
func PreFireLeadPoint(shooter, target *units.Unit, slot *units.Slot, w *content.WeaponDef, point Vec3) Vec3 {
	if !PreFireLeadGate(shooter, target, slot, w) {
		return point
	}
	// Raw 16.16 deltas, wrapping as signed 32-bit before the conversion —
	// the same domain the ballistic solver's deltas take [06 §3.3].
	dx := int32(shooter.X.Sub(point.X).Raw())
	dy := int32(shooter.Y.Sub(point.Y).Raw())
	dz := int32(shooter.Z.Sub(point.Z).Raw())
	// The square root is taken at working precision in the stated association
	// order and truncated toward zero into `D` [06 §3.3][01 §8] I3. `D` stays
	// a raw 16.16 distance; nothing shifts it down to whole world units. The
	// shared form is distance3DRaw, which [06 §6.8]'s cruise helper writes
	// identically.
	d := distance3DRaw(dx, dy, dz)
	t := (d << 16) / int64(w.WeaponVelocity)
	// 0xcccc / 65,536 = 0.79998779…, an arithmetic shift, so it floors
	// [06 §3.3] I3.
	t2 := (t * 0xcccc) >> 16
	lead := func(v numeric.Fixed) numeric.Fixed {
		return numeric.Fixed(int32((v.Raw() * t2) >> 16))
	}
	point.X = point.X.Add(lead(target.Move.VelX))
	point.Y = point.Y.Add(lead(target.Move.VelY))
	point.Z = point.Z.Add(lead(target.Move.VelZ))
	return point
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
	Hostile              bool // reaction-offer hostility; cached queries trust registry membership [06 §3.1]

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

// Acquisition carries query and physical-gate operands plus the shared stream
// for one slot acquisition [06 §3.1][06 §3.2].
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
	// The acquisition query itself does not read it. The predicate belongs to
	// the registry rebuild, which files the primary list up to thirty ticks
	// before an acquisition filters it [06 §3.1]; this field serves the
	// single-candidate form a fixture asks about a candidate it did not get
	// from a list [06 R-WPN-04 §2 part 3].
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
	// plays no part. Service.rebuildTargetRegistry computes it on the
	// registry's cadence, one unit at a time through
	// unitOpensTargetingUpgradeGate, and Service.acquireTargetForSlotRange
	// hands it here.
	HasUpgrade bool
}

// unitOpensTargetingUpgradeGate is the registry's secondary-list gate for one
// unit [06 §3.1] [04 R-SPEC-01 §8]: true when the unit is alive, not dying,
// owned by the scanning player itself, complete (remaining build fraction
// exactly zero) and activated (paralysis does not clear the bit), and its
// definition carries `istargetingupgrade`. Nothing is counted or summed; the
// rebuild walk sets the boolean on the first unit that opens it.
// "Friendly" here means the SAME PLAYER, not the same ally
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
func (a *Acquisition) rejectsStunned(c Candidate) bool {
	return a.Paralyzer && c.Stunned
}

// acquireFilteredTarget samples an already materialized query population. Its
// caller owns candidates: swap-last removal deliberately mutates the slice, so
// the caller must pass a snapshot it does not need afterwards. Service builds
// that short-lived snapshot per attempt and passes it directly here
// [06 §3.1][06 §3.2].
func acquireFilteredTarget(candidates []Candidate, a Acquisition) (pool.Handle, bool) {
	// The two score minima are independent, but draws follow pick order,
	// including fallback scores when a preferred winner already exists.
	best := [2]pool.Handle{}
	bestScore := [2]uint32{0x7fffffff, 0x7fffffff}
	for picked := 0; picked < 50 && len(candidates) > 0; picked++ {
		index := 0
		if a.RNG != nil {
			index = int(a.RNG.Uint32n(uint32(len(candidates))))
		}
		c := candidates[index]
		candidates[index] = candidates[len(candidates)-1]
		candidates = candidates[:len(candidates)-1]
		// Rejected picks still spent their sampling draw and count toward
		// the fifty-pick limit. They cannot open secondary fallback.
		if !a.admits(c) || a.rejectsStunned(c) {
			continue
		}
		preferred := IsPreferredCategory(c.Category, a.BadMask)
		if a.MaskResolved && c.CategoryMaskResolved {
			preferred = IsPreferredCategoryMask(c.CategoryMask, a.BadTargetMask)
		}
		bucket := 1
		if preferred {
			bucket = 0
		}
		var score uint32
		if a.RNG != nil {
			score = a.RNG.Uint32n(rngBoundForCandidate(c.X.Sub(a.ShooterX), c.Z.Sub(a.ShooterZ)))
		}
		if score < bestScore[bucket] {
			bestScore[bucket] = score
			best[bucket] = c.Handle
		}
	}
	if best[0] != 0 {
		return best[0], true
	}
	return best[1], best[1] != 0
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

// UnitTargetPoint is the live-unit half of the per-slot target-point resolver
// [06 R-WPN-04 §1]: the point every consumer of a unit target aims at — the
// aim-time yaw/pitch solve, the shot-time range and ballistic clauses, and the
// creator's own muzzle-to-target solve [06 §3.3][06 §6.3].
//
// The resolver dispatches `SweetSpot` synchronously on the TARGET's script
// with cell 0 seeded zero [06 R-WPN-03 §6][04 §5.3]; a script without the
// entry leaves the seed, so the piece is 0. The returned piece selects a piece
// of the target's loaded model, and the point is the target's position plus
// the centre of that piece's own vertex bounding box, seeded at the piece
// origin rather than the first vertex:
//
//	min = max = 0 ; per vertex: min = min(min, v), max = max(max, v)
//	point = target.position + (max + min) / 2      (per axis, signed, truncating)
//
// Three things this deliberately does NOT do, each Established
// [06 R-WPN-04 §1]: it applies neither the piece's parent offset nor its
// current script state (turn, move, hide) — the offset is the piece's own
// vertex cloud about its own origin; it does not go through the piece locator,
// so the model-space triple is added AS-IS, with no `(x, y, −z)` output
// negation; and a piece with no vertices yields the unit position exactly.
// The muzzle-side transform of [06 §3.4], which walks the hierarchy and
// negates Z once on output, is a different routine and is not reused here.
//
// Aiming at the bare unit position instead — which this build used to do —
// put every shot at the target's ground point: a Peewee's pellets went for a
// solar collector's footprint rather than its body, and a tall target's base.
//
// A piece index outside the model's table is an out-of-bounds read in retail;
// this build answers the unit position for it, the bounds-check exception of
// [I11]. A target with no binding at all (a synthetic fixture; every retail
// definition carries a script) takes the same answer.
func UnitTargetPoint(target *units.Unit) Vec3 {
	if target == nil {
		return Vec3{}
	}
	pos := Vec3{X: target.X, Y: target.Y, Z: target.Z}
	binding := target.COBBinding()
	if binding == nil {
		return pos
	}
	var piece int32 // cell 0 seeded zero [06 R-WPN-03 §6]
	if binding.Callbacks != nil {
		piece = binding.Callbacks.SweetSpot().QueryValue()
	}
	centre, ok := pieceVertexBoxCentre(binding, piece)
	if !ok {
		return pos
	}
	return Vec3{X: pos.X.Add(centre[0]), Y: pos.Y.Add(centre[1]), Z: pos.Z.Add(centre[2])}
}

// boxCentreKey identifies one piece of one loaded model for the box-centre
// memo. Models are shared immutable catalog objects, so the pointer is the
// identity.
type boxCentreKey struct {
	model *model.Model
	piece int
}

type boxCentreEntry struct {
	centre [3]numeric.Fixed
	ok     bool
}

// unitTargetPoint is UnitTargetPoint with the piece box centre memoised on the
// service. Every weapon slot with a unit target asks for the point every tick,
// and the vertex scan was a measurable share of weapon service; the memo
// returns exactly what pieceVertexBoxCentre computes for the same model and
// piece, and UnitTargetPoint stays the definition the tests lock.
func (s *Service) unitTargetPoint(target *units.Unit) Vec3 {
	if s == nil || target == nil {
		return UnitTargetPoint(target)
	}
	pos := Vec3{X: target.X, Y: target.Y, Z: target.Z}
	binding := target.COBBinding()
	if binding == nil {
		return pos
	}
	var piece int32 // cell 0 seeded zero [06 R-WPN-03 §6]
	if binding.Callbacks != nil {
		piece = binding.Callbacks.SweetSpot().QueryValue()
	}
	if binding.Model == nil || piece < 0 || int(piece) >= len(binding.PieceMap) {
		return pos
	}
	key := boxCentreKey{model: binding.Model, piece: binding.PieceMap[piece]}
	entry, hit := s.boxCentres[key]
	if !hit {
		entry.centre, entry.ok = pieceVertexBoxCentre(binding, piece)
		if s.boxCentres == nil {
			s.boxCentres = make(map[boxCentreKey]boxCentreEntry)
		}
		s.boxCentres[key] = entry
	}
	if !entry.ok {
		return pos
	}
	return Vec3{X: pos.X.Add(entry.centre[0]), Y: pos.Y.Add(entry.centre[1]), Z: pos.Z.Add(entry.centre[2])}
}

// pieceVertexBoxCentre is the `SweetSpot` piece-to-offset transform of
// [06 R-WPN-04 §1]: the vertex bounding box of one piece of the bound model,
// seeded at the origin, halved per axis with truncation toward zero. The COB
// piece index is mapped to the model piece through the binder's piece map,
// exactly as the locator maps it — both index the same piece table [03 §2.4]
// [04 §4.1]. The vertices are the model's as loaded, which already carry the
// load-time half-turn of [03 §2.4]; no further sign change is applied.
func pieceVertexBoxCentre(b *cob.Binding, cobPiece int32) ([3]numeric.Fixed, bool) {
	if b == nil || b.Model == nil || cobPiece < 0 || int(cobPiece) >= len(b.PieceMap) {
		return [3]numeric.Fixed{}, false
	}
	modelPiece := b.PieceMap[cobPiece]
	if modelPiece < 0 || modelPiece >= len(b.Model.Pieces) {
		return [3]numeric.Fixed{}, false
	}
	var lo, hi [3]int64 // seeded at the piece origin, NOT the first vertex [06 R-WPN-04 §1]
	for _, v := range b.Model.Pieces[modelPiece].Vertices {
		for axis := 0; axis < 3; axis++ {
			raw := v[axis].Raw()
			if raw < lo[axis] {
				lo[axis] = raw
			}
			if raw > hi[axis] {
				hi[axis] = raw
			}
		}
	}
	var centre [3]numeric.Fixed
	for axis := 0; axis < 3; axis++ {
		centre[axis] = numeric.Fixed((hi[axis] + lo[axis]) / 2) // signed, truncating halving [I3]
	}
	return centre, true
}
