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

// The capture timer of [05 R-WORK-01 §6] used to have a second, CALLER-LESS
// copy here under the name `CaptureTimer`. The live implementation is
// `captureBudget` in internal/orders/work.go — the capture executor's own — and
// AU-7 deleted this copy rather than let two renderings of one traced section
// drift apart again (they already had, on the constant form of the base sum).
// `internal/construction` imports `internal/orders`, so the surviving copy is on
// the side that can be shared.

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
	// The ordinary creator receives the victim's original two-bit mover mode.
	// Its wrapper runs COB Create with the grounded default, then installs this
	// argument before its activation edge [05 R-WORK-01 §15].
	h, err := s.World.CreateWithMoverMode(victim.Def, newOwner, victim.X, victim.Y, victim.Z, victim.Move.Mode)
	if err != nil {
		return nil, false
	}
	repl := s.World.Unit(h)
	if repl == nil {
		return nil, false
	}
	// The replacement begins with neither definition-seeded standing order.
	// Capture clears both complete two-bit fields after the ordinary creator
	// initializes them [05 R-WORK-01 §15][04 R-STANCE-01 §6].
	repl.Flags &^= (units.StandingFieldMask << units.StandingMoveShift) |
		(units.StandingFieldMask << units.StandingFireShift)
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
	operationalState := victim.OperationalState()
	victim.LastDamageCause = uint8(CaptureDeathCause)
	victim.LastDamageSide = units.NeutralAttackerSide
	s.World.Destroy(victim.Handle, units.DeathKilled) // null attacker [06 §12.1]
	// The capture transfer replays the old operational edge byte only after the
	// old record has died. This is deliberately not a field copy: the state-edge
	// service replays the whole modeled operational byte in its set then clear
	// passes, retaining callback, cue, and visibility side effects. The request
	// bit is not operational and is not transferred [05 R-WORK-01 §15]
	// [05 R-ECO-01 §8][05 R-ECO-01 §9].
	repl.ReplayOperationalState(operationalState)
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
