package combat

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Cause is the death-cause nibble [06 §12.1] [04 §5.1]. High four bits of the packed death byte.
// Values are retail kind bytes; plan lists the established local producers.
type Cause uint8 // [06 §12.1]

const (
	CauseOrdinary          Cause = 1  // ordinary weapon damage [06 §12.1]
	CauseParalyzer         Cause = 2  // paralyzer packet [06 §10]
	CauseSelfDestruct      Cause = 3  // self-destruct countdown [06 §12.1]
	CauseCapture           Cause = 4  // capture/owner replacement [06 §12.1]
	CauseReclaim           Cause = 5  // reclaim/build-complete pulse [06 §12.1]
	CauseCargo             Cause = 6  // default cargo cascade [06 §12.1]
	CauseFeatureConversion Cause = 7  // immediate feature conversion [06 §12.1]
	CauseTeardown          Cause = 8  // game teardown sweep [06 §12.1]
	CauseDeconstruction    Cause = 9  // construction-fraction deconstruction/refund [06 §12.1]
	CauseWaterDamage       Cause = 11 // mission water damage [06 §12.1]
)

// DeathSeverity computes death severity per [06 §12.1] C22 and [04 §5.1].
// clamp((floor((-health)*100 / maxDamage) + prior) /2, 1,100) truncating divide by two.
// prior is the prior-severity-sample byte: health percentage retained from
// the previous 30-tick sampling boundary, not the immediate pre-lethal
// percentage [06 §12.1][04 §5.1].
// Reuses cob.KilledSeverity (C15 of phase 6) [04 §5.1].
func DeathSeverity(health, maxHealth int32, priorSample uint8) int32 {
	return cob.KilledSeverity(health, maxHealth, priorSample) // [06 §12.1] C22 [04 §5.1]
}

// PackDeathByte packs severity-independent cause/variant byte per [04 §5.1]: (cause<<4)|(variant&0xF).
// Low four bits are corpse-chain depth [06 §12.1] C23; high four bits death cause.
func PackDeathByte(cause Cause, variant uint8) uint8 {
	return uint8(cause)<<4 | (variant & 0x0F) // [04 §5.1] [06 §12.1] C23
}

// UnpackDeathByte splits packed byte into cause and depth [06 §12.1] C23.
func UnpackDeathByte(packed uint8) (Cause, uint8) {
	return Cause(packed >> 4), packed & 0x0F // [06 §12.1] C23
}

// ResolveCorpse returns the corpse feature for depth [06 §12.1] C23.
// Depth is low four bits of packed death byte. Depth 0 => no corpse; depth1 => authored Corpse feature;
// larger depths follow featuredead exactly depth-1 times, stopping at no-feature sentinel.
func ResolveCorpse(unitDef *content.UnitDef, features map[string]*content.FeatureDef, depth uint8) *content.FeatureDef {
	depth &= 0x0F // low four bits [06 §12.1] C23
	if depth == 0 {
		return nil // [06 §12.1] C23 depth 0 places no corpse
	}
	if unitDef == nil {
		return nil
	}
	name := strings.TrimSpace(unitDef.Corpse)
	if name == "" {
		return nil
	}
	ck := content.CanonicalKey(name)
	var cur *content.FeatureDef
	if features != nil {
		cur = features[ck]
	}
	if cur == nil {
		return nil
	}
	if depth == 1 { // [06 §12.1] C23 selects authored Corpse
		return cur
	}
	for i := uint8(1); i < depth; i++ {
		if cur == nil {
			return nil
		}
		var nxt *content.FeatureDef
		if cur.FeatureDeadDef != nil {
			nxt = cur.FeatureDeadDef // resolved successor [02 "Feature record"]
		} else if strings.TrimSpace(cur.FeatureDead) != "" {
			ck2 := content.CanonicalKey(cur.FeatureDead)
			if features != nil {
				nxt = features[ck2]
			}
		} else {
			return nil // sentinel [06 §12.1] C23
		}
		if nxt == nil {
			return nil // no-feature sentinel [06 §12.1] C23
		}
		cur = nxt
	}
	return cur
}

// DeathContext is the input to the shared death path [06 §12.1][04 §5.1] C22-C25.
type DeathContext struct {
	Health            int32
	MaxHealth         int32
	PriorSample       uint8   // prior-severity-sample byte, previous 30-tick window percent [04 §5.1]
	Cause             Cause   // death packet kind [06 §12.1]
	RemainingFraction float32 // remaining-build-fraction/landed indicator; 0.0 when normal/grounded, nonzero while airborne or under construction [06 §12.1]
	UnitDef           *content.UnitDef
}

// DeathCreditInput is the identity snapshot retained by the death packet's
// accounting boundary. AttackerSide is deliberately a side value rather than
// a live-unit lookup: retail credits from the stored packet snapshot and does
// not validate the reconstructed attacker pointer [06 §12.1].
type DeathCreditInput struct {
	Cause             Cause
	VictimOwner       uint8
	AttackerSide      uint8
	AttackerPresent   bool
	VictimCommander   bool
	RemainingFraction float32
	// Cause3LossEligible is supplied by the owner/alliance gate. The retail
	// trace establishes the gate but not the reduction of a non-local alliance
	// aggregate, so callers must not infer it from side equality [06 §12.1].
	Cause3LossEligible bool
}

// DeathCredit is the player-level accounting decision for one finalized unit
// death. It contains no mutation; the session/economy owner applies the four
// increments at the event site [06 §12.1][08 R-CAMP-01 §7].
type DeathCredit struct {
	VictimLoss            bool
	AttackerKill          bool
	VictimCommanderLoss   bool
	AttackerCommanderKill bool
}

// ComputeDeathCredit applies the exact cause and identity gates for result
// statistics. Ordinary weapon (1) and cargo cascade (6) deaths use the full
// path; self-destruct (3) is loss-only when its alliance gate allows it;
// reclaim (5) uses the full path only for a non-neutral, non-self attacker.
// Construction, capture, feature conversion, teardown and water deaths do
// not credit player statistics [06 §12.1].
func ComputeDeathCredit(in DeathCreditInput) DeathCredit {
	full := in.Cause == CauseOrdinary || in.Cause == CauseCargo
	if in.Cause == CauseReclaim {
		full = in.AttackerPresent && in.AttackerSide != 10 && in.AttackerSide != in.VictimOwner
	}
	loss := full || (in.Cause == CauseSelfDestruct && in.Cause3LossEligible)
	// Ordinary unit kills use all three packet gates. Commander credit is a
	// separate full-path branch: its only attacker identity gate is the
	// non-neutral side test. Consequently a self-killed or incomplete commander
	// still contributes commander-kill credit when the packet has an attacker
	// side, while it contributes no ordinary unit kill [06 §12.1].
	kill := full && in.AttackerPresent && in.AttackerSide != 10 &&
		in.AttackerSide != in.VictimOwner && in.RemainingFraction == 0
	commanderLoss := loss && in.VictimCommander
	commanderKill := full && in.AttackerPresent && in.VictimCommander && in.AttackerSide != 10
	return DeathCredit{
		VictimLoss:            loss,
		AttackerKill:          kill,
		VictimCommanderLoss:   commanderLoss,
		AttackerCommanderKill: commanderKill,
	}
}

// DeathResolution is the result of the shared death path [06 §12.1] C22-C25.
type DeathResolution struct {
	Severity    uint8               // 0 when query bypassed else 1..100 [06 §12.1] C22 C24
	Variant     uint8               // low nibble depth 0..15 after gating [06 §12.1] C23 C24
	Packed      uint8               // (cause<<4)|(variant&0xF) [04 §5.1]
	Queried     bool                // whether synchronous Killed query was issued [06 §12.1] C24
	Corpse      *content.FeatureDef // resolved chain or nil [06 §12.1] C23
	DoCorpse    bool                // depth>0 and not gated [06 §12.1]
	DoExplosion bool                // gated by remainingFraction==0 and severity>0 [06 §12.1]
	KilledCalls int                 // number of synchronous Killed invocations (0 or 1 for local) [06 §12.1] C25
}

// ShouldDispatchReplayKilled reports whether received-network replay mode should dispatch
// Killed asynchronously. Only when signed packet severity is positive; return is ignored [06 §12.1] C25 [04 §5.1].
func ShouldDispatchReplayKilled(packetSeverity int8) bool {
	return packetSeverity > 0 // [06 §12.1] C25 [04 §5.1]
}

// SelectDeathExplosionWeapon selects the death-explosion weapon per [06 §12.1][02 "Unit record"].
// Cause 3 selects SelfDestructAsDef; every other cause selects ExplodeAsDef.
// Record 0 is still delivered to central impact: its calculated flash and
// dust puff survive the absence of named art [06 §12.2][06 R-DMG-01 §5]
// [06 R-WFX-01 §2]. Weapon-slot activity is not a death-explosion gate.
func SelectDeathExplosionWeapon(def *content.UnitDef, cause Cause) *content.WeaponDef {
	if def == nil {
		return nil // [02 "Unit record"] no def => no weapon
	}
	if cause == CauseSelfDestruct {
		return def.SelfDestructAsDef
	}
	return def.ExplodeAsDef
}

// UnassignedKilledVariant is Nanolathe's single bounded substitute for the
// variant nibble retail leaves unspecified, and it is a **sanctioned
// divergence** [04 R-CB-01 §9][04 R-CB-01 §7].
//
// Retail keeps the variant in a local of the death handler that is written only
// by the two bypass paths; the synchronous `Killed` query seeds the script
// window from that local and copies the window back afterwards, so whenever the
// script does not assign its second parameter the local round-trips unchanged.
// Four sub-cases share that one path and one outcome — the unit has no VM, the
// program has no `Killed` body, the thread pool is full, and a `Killed` body
// that ignores its second parameter: the query writes nothing and the packed
// byte carries `(cause << 4) | (residue & 0xF)`, an unspecified four-bit
// residue that is deterministic within one retail process but bears no relation
// to the unit, the cause or the script and is not reproducible.
//
// [04 R-CB-01 §9] requires an implementation to pick one constant for all four
// sub-cases and record the choice. Nanolathe picks `1` — the value retail's own
// cause-7 bypass writes, and the depth that selects the definition's authored
// corpse [06 §12.1] C23 — so a stock wall or commander dying without a `Killed`
// body still leaves its authored wreck. The constant still yields to the
// remaining-work gate in ResolveDeath: a non-zero remaining-build fraction
// forces the variant to zero after any query.
const UnassignedKilledVariant int32 = 1

// KilledVariantFromVM performs the synchronous 4-cell Killed query deterministically [04 §5.1] C23 C25.
// It drains the unit's script threads inline with delta 0 (no piece pass) and
// returns the low-four-bit corpse-chain depth. No map iteration, no wall-clock,
// no global RNG beyond the VM's own deterministic state (I1, I4, I6).
//
// The query is attempted at most once. All three retail sub-cases in which the
// query writes nothing — no VM, no `Killed` body, thread pool full — return
// UnassignedKilledVariant with ok=false; the fourth, a body that ignores its
// second parameter, reaches the same value because the variant cell is *seeded*
// with the substitute and round-trips unchanged, exactly as retail's frame
// residue does [04 R-CB-01 §9].
func KilledVariantFromVM(vm *cob.VM, severity int32) (int32, bool) {
	if vm == nil || vm.Program() == nil {
		// A definition with no compiled script is a live unit with a null VM
		// that never reaches the query [04 R-COB-01 §3][04 R-CB-01 §9].
		return UnassignedKilledVariant, false
	}
	prog := vm.Program()
	pc, ok := prog.Scripts["Killed"]
	if !ok {
		// The name misses the program's script table, so the thread allocator
		// refuses before any window word is seeded [04 R-CB-01 §9].
		return UnassignedKilledVariant, false
	}
	// Four-cell query: cell0=severity, cell1=variant, cells 2/3=0 [04 §5.1].
	// Cell 1 is seeded with the substitute so a body that ignores its second
	// parameter copies it back unchanged [04 R-CB-01 §9]; a body that assigns
	// it yields the low nibble of the corpse-chain depth [06 §12.1] C23.
	args := []int32{severity, UnassignedKilledVariant, 0, 0}
	started, _ := vm.CallQuery(pc, args) // [04 §4.2] Q mode: drains one slot inline, no piece pass
	if !started {
		// Full eight-slot thread pool: the allocator refuses before seeding, so
		// nothing is copied back [04 R-CB-01 §9][04 §4.3].
		return UnassignedKilledVariant, false
	}
	variant := args[1] & 0x0F // [06 §12.1] C23 low four bits are depth
	return variant, true
}

// ResolveDeath implements the shared authoritative death path for C22-C25 [06 §12.1][04 §5.1][GAP T21].
// It is the helper that cause-9 deconstruction and capture/reclaim call sites use [PLAN_09 C24].
// syncKilled, when non-nil, is the synchronous four-cell Killed query that drains script threads inline [04 §5.1][06 §12.1] C23 C25.
// It receives severity and returns the low-four-bit variant (depth). When the query writes nothing — no VM, no `Killed`
// body, or a full thread pool — syncKilled reports ok=false and the variant becomes UnassignedKilledVariant, the one
// sanctioned substitute for retail's unspecified four-bit residue [04 R-CB-01 §9]; the remaining-work gate below still
// forces zero afterwards. A nil syncKilled is the same case: the unit has no VM and the query is never attempted.
// Local authoritative death does NOT issue a second Killed callback after the synchronous query [06 §12.1] C25 — this function calls syncKilled at most once and never schedules an async second call.
func ResolveDeath(ctx DeathContext, features map[string]*content.FeatureDef, syncKilled func(severity int32) (variant int32, ok bool)) DeathResolution {
	var res DeathResolution
	cause := ctx.Cause
	// Cause overrides in evaluation order [04 §5.1][06 §12.1] C24.
	// Cause 7 yields severity 0 variant 1 and no query [06 §12.1] C24.
	if cause == CauseFeatureConversion {
		res.Severity = 0
		res.Variant = 1 // [06 §12.1] C24
		res.Packed = PackDeathByte(cause, 1)
		res.Queried = false
		res.KilledCalls = 0     // no query [06 §12.1] C24
		res.DoExplosion = false // severity 0 => no explosion even if remainingFraction==0
		res.DoCorpse = true
		res.Corpse = ResolveCorpse(ctx.UnitDef, features, 1) // [06 §12.1] C23 depth 1 selects authored Corpse
		return res                                           // [06 §12.1] C24 no query
	}
	// Causes 4,5,9 or positive health skip query with severity/variant 0 [06 §12.1] C24 [GAP T21].
	if cause == CauseCapture || cause == CauseReclaim || cause == CauseDeconstruction || ctx.Health > 0 {
		res.Severity = 0
		res.Variant = 0
		res.Packed = PackDeathByte(cause, 0) // [04 §5.1]
		res.Queried = false
		res.KilledCalls = 0
		res.DoExplosion = false // no severity => no explosion [06 §12.1]
		res.DoCorpse = false    // variant 0 => no corpse [06 §12.1] C23
		res.Corpse = nil
		return res // [06 §12.1] C24
	}
	// Full pipeline: compute severity then synchronous Killed query [06 §12.1] C22 C23 [04 §5.1].
	sev := DeathSeverity(ctx.Health, ctx.MaxHealth, ctx.PriorSample) // [06 §12.1] C22 clamp 1..100
	if sev < 1 {
		sev = 1
	}
	if sev > 100 {
		sev = 100
	}
	res.Severity = uint8(sev)
	res.Queried = true
	var variant int32
	if syncKilled != nil {
		v, ok := syncKilled(int32(sev)) // synchronous query drains script threads inline [04 §5.1] C25
		res.KilledCalls = 1             // local authoritative issues exactly one Killed after severity [06 §12.1] C25
		if ok {
			variant = v & 0x0F // low four bits are corpse-chain depth [06 §12.1] C23
		} else {
			// The query wrote nothing: no `Killed` body or a full thread pool.
			// One substitute serves every such sub-case [04 R-CB-01 §9].
			variant = UnassignedKilledVariant
		}
	} else {
		// No VM at all: retail never reaches the query, and the packed byte
		// carries the same unassigned residue [04 R-COB-01 §3][04 R-CB-01 §9].
		variant = UnassignedKilledVariant
		res.KilledCalls = 0 // no synchronous Killed was invoked [06 §12.1] C25
	}
	// After any query, nonzero remaining-build-fraction forces variant 0 [06 §12.1] C24 [GAP T21].
	// The same float gates death explosion [06 §12.1].
	if ctx.RemainingFraction != 0 { // equal to 0.0f gate [06 §12.1]
		variant = 0 // [06 §12.1] C24 [GAP T21]
	}
	res.Variant = uint8(variant & 0x0F)
	res.Packed = PackDeathByte(cause, res.Variant)          // [04 §5.1]
	res.DoExplosion = ctx.RemainingFraction == 0 && sev > 0 // gated by remainingFraction==0 [06 §12.1]
	res.DoCorpse = res.Variant != 0
	if res.DoCorpse {
		res.Corpse = ResolveCorpse(ctx.UnitDef, features, res.Variant) // [06 §12.1] C23
		if res.Corpse == nil {
			// Chain broke at sentinel before depth completed; no corpse placed, but DoCorpse remains depth-gated true for accounting.
			// Keep Corpse nil to signal placement failure [06 §12.1].
		}
	}
	// Local authoritative death does NOT issue a second Killed callback after synchronous query [06 §12.1] C25.
	// We have called syncKilled at most once and never schedule an async second call.
	return res
}
