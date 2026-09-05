// Capture, resurrection and reverse [05 R-WORK-01 §6].

package construction

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// CaptureDeathCause is the damage-kind byte the ownership transfer's kill
// packet carries [06 §12.1]: "cause 4 — capture/owner replacement". Its credit
// branch is none, and the finalizer's corpse/explosion path skips the Killed
// query for it [04 §5.1].
const CaptureDeathCause uint8 = 4

// Capture timer constants [05 R-WORK-01 §6].
//
// CORRECTION (AU-3). [05 R-WORK-01 §6] renders the base sum with three
// constants — `0.015`, `0.2142857142857` and `150.0` — and calls them "all
// three float32 constants". Re-tracing the capture executor this session shows
// that rendering is a decompiler's simplification: NEITHER 0.015 nor
// 0.2142857142857 exists anywhere in the image, as a float32 or as a double.
// What the executor actually holds is four single-precision constants, and it
// forms each cost term with TWO multiplies:
//
//	energy term = buildcostenergy × 30       × 0.0005
//	metal term  = buildcostmetal  × 30       × (−1/140)
//	base_f      = (energy term − metal term) − (−150)
//
// so the metal term is carried NEGATIVE and the bias is carried negative, and
// both are turned around by subtractions. The reconstructed coefficients are
// the products of the pairs, and they are NOT the float32 nearest the decimals
// §6 prints: 30 × float32(0.0005) is 0.0150000007…, a shade ABOVE 0.015, while
// float32(0.015) is 0.0149999997, a shade below.
//
// Getting that backwards is not academic — it was this unit's first attempt.
// Writing the sum with a float32 0.015 and evaluating at working precision
// moves `base` DOWN by one at every energy cost that is a multiple of 200 (an
// energy cost of 200 gives 152 instead of 153), and stock energy costs are
// dense in multiples of 200. Written as retail writes it, the base agrees with
// the float64 decimals over the whole authored range — swept exhaustively for
// every cost pair that lands below the 1800 clamp — so the width was never the
// behavioral half of this contract. The clamps below are.
//
// The names below are the operands, not the reconstructed coefficients,
// because the reconstruction is what invites the wrong rounding back in.
const (
	captureCostScale  float32 = 30.0                   // both cost terms are scaled by this first
	captureEnergyUnit float32 = 0.0005                 // then the energy term by this
	captureMetalUnit  float32 = -0.0071428571827709675 // and the metal term by this (negative, −1/140)
	captureBias       float32 = -150.0                 // subtracted, so it adds 150
	captureClampMax   int32   = 1800
)

// CaptureTimer computes the capture timer of [05 R-WORK-01 §6], in the form the
// capture executor computes it:
//
//	base_f       = (energyCost*30*0.0005) - (metalCost*30*(-1/140)) - (-150)
//	base         = trunc(base_f); if (base >= 1800) base = 1800    // UPPER clamp only
//	healthScaled = (uint32)( ((int32)(int16)health + (int32)maxDamage) * base )
//	             / (uint32)( 2 * maxDamage )                       // UNSIGNED divide
//	killsFactor  = (int32)(uint16)kills / 5
//	timer        = ((killsFactor + 10) * healthScaled * 10) / 100  // signed
//
// Kills is the target's kill count (runtime experience field), health int16,
// maxDamage u32. The two cost terms are formed and combined at x87 working
// precision with a single truncation at the end; the float64 intermediates
// below are that working precision, never stored (I2).
//
// Four corrections against the shape this used to have:
//
//  1. THE COST TERMS. See the constant block above: the two-multiply form is
//     what the executor holds, and neither reconstructed coefficient is the
//     float32 nearest the decimal [05 R-WORK-01 §6] prints. Over the authored
//     range this agrees with the float64 decimals the code used to carry, so
//     the change is one of provenance, not of value — but writing those
//     decimals as float32 instead, which is the obvious "fix", is off by one at
//     every energy cost that is a multiple of 200.
//
//  2. NO LOWER CLAMPS. "There is no lower clamp: the comparison is a single
//     signed test against 1800 and nothing bounds the value below." The
//     `base < 0 → 0` and `timer < 0 → 0` clamps are gone, and they were not
//     harmless: a negative authored cost drives `base` negative, and the
//     unsigned division below then yields an ENORMOUS healthScaled — precisely
//     what [05 R-WORK-01 §6] says retail produces. The lower clamp turned that
//     into a small number instead.
//
//  3. THE MIDDLE STEP IS AN UNSIGNED DIVIDE OF A SIGNED 32-BIT PRODUCT. The
//     numerator wraps at 32 bits and is re-read unsigned; the old int64
//     arithmetic could not wrap and divided signed.
//
//  4. THE KILL COUNT IS READ AS uint16 before the signed divide by five.
//
// Corrections 2 to 4 bring this into line with `captureBudget` in
// internal/orders/work.go, which is the LIVE implementation of the same section
// — this function has no caller. Correction 1 does not: that copy still carries
// the three decimals as float32 literals, which happens to agree over the
// authored range but is the rounding trap described above.
//
// DIVERGENCE (I11): the `maxDamage <= 0` guard below is ours. Retail divides by
// `2 * maxdamage` with no zero test, so a definition authoring `maxdamage 0`
// faults there; [05 R-WORK-01 §6] does not describe what the fault produces, so
// there is no behavior to clone. Substituting 1 keeps the caller alive and is
// unreachable on stock content, which authors no zero `maxdamage`. The live
// copy in internal/orders/work.go reproduces the fault instead.
func CaptureTimer(energyCost, metalCost float32, health int32, maxDamage int32, kills int32) int {
	if maxDamage <= 0 {
		maxDamage = 1 // DIVERGENCE (I11): retail faults on the divide; see above.
	}
	// Each cost term is scaled twice, the metal term is carried negative and the
	// bias is carried negative, so both fold in through subtractions. One
	// truncation toward zero at the end [01 §8] (I3).
	energyTerm := float64(energyCost) * float64(captureCostScale) * float64(captureEnergyUnit)
	metalTerm := float64(metalCost) * float64(captureCostScale) * float64(captureMetalUnit)
	base := int32((energyTerm - metalTerm) - float64(captureBias)) // __ftol [01 §8]
	if base >= captureClampMax {
		base = captureClampMax // the only clamp: signed, upper, and `>=`
	}
	// A signed 32-bit product re-read unsigned, then divided unsigned. Go's
	// signed arithmetic wraps, so int32 here IS the retail multiply.
	numerator := uint32((int32(int16(health)) + maxDamage) * base)
	healthScaled := int32(numerator / uint32(2*maxDamage))
	killsFactor := int32(uint16(kills)) / 5 // signed, truncating, no cap
	timer := ((killsFactor + 10) * healthScaled * 10) / 100
	return int(timer)
}

// CaptureEligible is the capture executor's phase-0 admission ladder
// [05 R-WORK-01 §6]. It has exactly five predicates, tested in this order and
// stopping at the first failure:
//
//  1. the order's target handle is non-null — `Capture failed`;
//  2. the builder is still linked — a silent terminal;
//  3. the BUILDER's definition carries `cancapture` — a silent terminal;
//  4. the TARGET's definition does NOT carry `cancapture` —
//     `That unit cannot be captured`. The same bit gates both ends, so
//     anything that can capture cannot be captured;
//  5. the target's remaining construction fraction compares equal to zero —
//     `That unit is a cloud of vapor and cannot be captured`.
//
// Predicate 5 is a float32 compare against a literal zero whose only accepted
// outcome is equal [05 R-WORK-01 §10]: negative zero is accepted, every other
// value including NaN is rejected. Go's `== 0` on a float32 has exactly those
// semantics, so `Remaining == 0` is the retail test itself, not a proxy for an
// idleness sentinel — there is no idleness, health or order-state test here,
// and a moving or firing finished unit is captured normally.
//
// Predicate 4 is what an earlier reading of this function could not locate and
// called "victim immunity": it is the target's own `cancapture` bit
// [05 R-WORK-01 §10]. The same bit also blocks reclaim [05 R-WORK-01 §4].
//
// The same-owner and dying-victim rejects this function used to add are NOT in
// the ladder — [05 R-WORK-01 §10] states it has exactly these five — so they
// are gone. [05 R-WORK-01 §15] says where each one does live, and the answer is
// two different layers:
//
//   - the SAME-OWNER exclusion is the command resolver's code 13, whose whole
//     test is "the actor's `cancapture`, a target, and the target's owner
//     record differing from the actor's — a same-owner target never becomes a
//     `Capture` order" [05 R-WORK-01 §15][04 R-ORD-02 §1]. It is implemented
//     there, in internal/orders/resolve.go's code-13 arm;
//   - the DEATH LATCH is the ownership transfer's own entry gate, and only
//     there. The resolver rejects a target lacking the alive bit but "does not
//     read the death latch", and neither does the issue helper that queues the
//     resolved order, so a target killed this tick — latch set, alive bit still
//     set until the next sweep's finalizer — passes both and passes this
//     five-predicate ladder, which tests none of owner, latch or health. It is
//     refused at TransferOwnership below, silently.
func CaptureEligible(builder *units.Unit, victim *units.Unit) bool {
	if victim == nil { // 1: the target handle is non-null
		return false
	}
	if builder == nil || builder.Def == nil { // 2: the builder is still linked
		return false
	}
	if !builder.Def.CanCapture { // 3: the builder can capture
		return false
	}
	if victim.Def == nil || victim.Def.CanCapture { // 4: the target cannot
		return false
	}
	// 5: the remaining-build fraction compares equal to literal zero
	// [05 R-WORK-01 §10]. A nanoframe carries 1.0 and is rejected.
	return victim.Remaining == 0
}

// TransferOwnership performs the central narrow ownership transfer
// [05 R-WORK-01 §15]. Its exact copy list is in that section; perDefLimit below
// returns the effective per-definition limit and whether one applies.
func perDefLimit(def *content.UnitDef) (int32, bool) {
	if def == nil {
		return -1, false
	}
	if def.LimitEnabled {
		// [05 R-SHARE-01 §9]: the definition parser writes -1 into every
		// definition's limit field, so a written field is authoritative. Any
		// negative value is the unlimited sentinel; a written 0 is a genuine
		// zero allowance — the multiplayer restriction apply step is its only
		// writer, and it means the definition may not be created at all. The
		// compiled catalog now carries the parser's -1 default, so a written 0
		// is no longer indistinguishable from a fixture's Go zero value.
		if def.Limit < 0 {
			return -1, false
		}
		return def.Limit, true
	}
	if def.UnitLimit == UnitLimitUnlimited || def.UnitLimit == 0 {
		return -1, false
	}
	if def.UnitLimit <= 0 {
		return -1, false
	}
	return def.UnitLimit, true
}

// TransferOwnership is the local branch of the central ownership transfer
// [05 R-WORK-01 §15]. Its ENTRY GATE is three tests — `owner ≠ new owner`, the
// alive bit set, and the DEATH LATCH CLEAR — and "a refusal there is silent
// (the executor still raises cue slot 16 with no text)". The latch test is the
// one that has no counterpart anywhere earlier: neither the command resolver
// nor the issue helper reads it, so a victim killed this tick reaches here with
// its latch set and its alive bit still standing, and this is where it is
// turned away. Nanolathe's Dying flag IS that latch — Destroy sets it and the
// phase-2 slot finalizer clears the slot [04 §2.3][04 §2.4].
//
// The per-definition limit is the -1 unlimited sentinel for every definition a
// single-player battle sees; a written 0 admits no unit of that definition at
// all [05 R-SHARE-01 §9].
func (s *Service) TransferOwnership(victim *units.Unit, newOwner uint8) (*units.Unit, bool) {
	if s == nil || s.World == nil || victim == nil || victim.Def == nil {
		return nil, false
	}
	// The entry gate of [05 R-WORK-01 §15], in its order. Every refusal here is
	// silent: no caption, no diagnostic.
	if victim.Owner == newOwner {
		return nil, false
	}
	if !victim.Alive {
		return nil, false
	}
	if victim.Dying {
		// The death latch. This is also the first-lethal rule for multiple
		// captors: each captor runs its own node and timer, and the first to
		// reach lethal progress transfers; the second arrives to find the latch
		// its own kill packet set and is refused here
		// [05 "Capture"].
		return nil, false
	}
	if lim, ok := perDefLimit(victim.Def); ok {
		cnt := 0
		for _, u := range s.World.Iter() {
			if u != nil && u.Alive && u.Def != nil && u.Def.UnitName == victim.Def.UnitName && u.Owner == newOwner {
				cnt++
			}
		}
		if int32(cnt) >= lim {
			return nil, false
		}
	}
	// Pool check: try alloc via World.Create at victim pos.
	h, err := s.World.Create(victim.Def, newOwner, victim.X, victim.Y, victim.Z)
	if err != nil {
		return nil, false
	}
	repl := s.World.Unit(h)
	if repl == nil {
		return nil, false
	}
	// THE COPY LIST, in [05 R-WORK-01 §15]'s order and nothing beyond it: the
	// 16-bit health, the remaining fraction, the orientation triple (bank,
	// heading, pitch), and — per weapon slot, only where the REPLACEMENT's slot
	// control byte carries its enabled bit — that slot's stockpiled-round byte.
	// "Nothing else is copied."
	repl.Health = victim.Health
	repl.Remaining = victim.Remaining
	repl.MaxHealth = victim.MaxHealth
	repl.Move.Bank = victim.Move.Bank
	repl.Move.Heading = victim.Move.Heading
	repl.Move.Pitch = victim.Move.Pitch
	// CORRECTION (WU-19-94), on two counts.
	//
	// The kill count is NOT carried. The line here read `repl.Kills =
	// victim.Kills`, on [05 "Capture"]'s "health, remaining fraction, veteran
	// experience, and visual piece and facing fields are carried".
	// [05 R-WORK-01 §15] corrects that paragraph in place: "The kill count is
	// not — the replacement is a fresh record with zero kills, so 'veteran
	// experience' is not carried and the next capture's kills factor restarts
	// from zero". CaptureTimer's killsFactor term therefore reads 0 for a
	// freshly captured unit, which makes recapturing one cheaper, not dearer.
	//
	// The conditional copy is the STOCKPILE, not the metal spot. The line below
	// read `repl.SpotMetal = victim.SpotMetal` under an open-question marker asking
	// "which cargo predicate retail tests", with SpotMetal named in the comment
	// as "a placeholder proxy". The gated per-slot stockpiled-round byte "is the
	// whole of the 'cargo copied conditionally'" [05 R-WORK-01 §15], so the
	// marker is retired and the placeholder is gone. SpotMetal is not copied:
	// the replacement is made by the ordinary creator, which samples the
	// placement-time metal sum for itself at the same position
	// [05 R-PROD-01 §6].
	//
	// The gate is the REPLACEMENT's control byte, not the victim's — a slot the
	// new record does not have enabled receives nothing. Slot.Ammo is retail's
	// completed-ammunition byte of [05 "Stockpile production"].
	for i := range repl.Slots {
		if !repl.Slots[i].IsEnabled() {
			continue
		}
		repl.Slots[i].Ammo = victim.Slots[i].Ammo
	}
	// The transported-cargo list, alliances, orders and groups are not carried
	// either [05 R-WORK-01 §15]; the victim's queues leak on capture exactly as
	// they do on death [05 "Factory product heading"].
	//
	// The old unit is then killed with a cause-4 packet and a null attacker:
	// [06 §12.1] gives cause 4 as "capture/owner replacement: packet builder
	// invoked with a NULL attacker at both of its call sites. Credit branch:
	// none." The intake's side snapshot for a null-attacker packet is the
	// neutral side, never the captor's [06 §9.1] step 4 — a captured record must
	// not read back as a kill for the capturing player. Cause 4 is also one of
	// the three that skip the Killed query, so the old record vanishes without a
	// wreck or an explosion [04 §5.1]; stamping it DeathKilled alone left it
	// exploding as ordinary weapon damage.
	//
	// The kill is unconditional here because the entry gate above already
	// refused a latched victim, which is where the first-lethal rule now lives.
	victim.LastDamageCause = uint8(CaptureDeathCause)
	victim.LastDamageSide = units.NeutralAttackerSide
	s.World.Destroy(victim.Handle, units.DeathKilled) // null attacker [06 §12.1]
	// New unit's building flag etc already via Create; remaining already copied.
	// Note: victim's queues leak on capture, same as on death [05 "Factory
	// product heading", "Established fact — link lifetime and completion
	// order"] — do not walk victim Orders.
	return repl, true
}

// CaptureTickRate is 2 ticks per progress step: the progress phase
// reschedules itself 2 ticks later on every qualifying visit [05 R-WORK-01 §6].
const CaptureTickRate = 2

// UnitLimitUnlimited is the sentinel -1 the definition parser writes for no
// limit [05 R-SHARE-01 §9].
const UnitLimitUnlimited int32 = -1

// CheckPerDefLimit reports whether creating another unit of def for owner would exceed limit [05 R-SHARE-01 §9].
func CheckPerDefLimit(w *units.World, owner uint8, def *content.UnitDef) bool {
	if def == nil {
		return true
	}
	lim, limited := perDefLimit(def)
	if !limited {
		return true
	}
	cnt := 0
	if w != nil {
		for _, u := range w.Iter() {
			if u != nil && u.Alive && u.Def != nil && u.Def.UnitName == def.UnitName && u.Owner == owner {
				cnt++
			}
		}
	}
	return int32(cnt) < lim
}

// Capture uses no decay/no cost [05 R-WORK-01 §6]: timer above, progress +2
// per 2 ticks, first-lethal gate above.
// Multiple captors run independent nodes; the first to reach lethal progress
// wins the transfer [05 "Capture"]
// — handled by TransferOwnership's Dying check.

// Ensure pool handle type imported for future use.
var _ = pool.Handle(0)
