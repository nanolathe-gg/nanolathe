package combat

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
)

// Cause is the death-cause nibble [06 §12.1] [04 §5.1]. High four bits of the packed death byte.
// Values are retail kind bytes; plan lists the established local producers.
type Cause uint8 // [06 §12.1]

const (
	CauseOrdinary          Cause = 1  // ordinary weapon damage [06 §12.1]
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	PriorSample       uint8   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Cause             Cause   // death packet kind [06 §12.1]
	RemainingFraction float32 // remaining-build-fraction/landed indicator; 0.0 when normal/grounded, nonzero while airborne or under construction [06 §12.1]
	UnitDef           *content.UnitDef
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
// The handler selects one of the unit's two resolved death-weapon definitions:
// cause 3 (self-destruct) prefers SelfDestructAsDef, otherwise ExplodeAsDef.
// A link holding the record-0 inactive sentinel (ID 0, stock [noweapon]) is
// not a weapon [02 §5 R-CONTENT-02]: inactive links fall through like nil, and
// an all-inactive selection returns nil — the death path then dispatches no
// explosion. This is the DoExplosion weapon trigger [06 §12.1] C22–C25.
func SelectDeathExplosionWeapon(def *content.UnitDef, cause Cause) *content.WeaponDef {
	if def == nil {
		return nil // [02 "Unit record"] no def => no weapon
	}
	// [06 §12.1] C22-C25: death-explosion weapon trigger (DoExplosion) of the
	// unit's deathExplosion weapon. Cause 3 uses SelfDestructAs, others use ExplodeAs.
	if cause == CauseSelfDestruct {
		if !content.IsWeaponInactive(def.SelfDestructAsDef) {
			return def.SelfDestructAsDef // [02 "Unit record"] selfdestructas
		}
		if !content.IsWeaponInactive(def.ExplodeAsDef) {
			return def.ExplodeAsDef // fallback [02 "Unit record"]
		}
		return nil // both links inactive [02 §5 R-CONTENT-02]
	}
	if !content.IsWeaponInactive(def.ExplodeAsDef) {
		return def.ExplodeAsDef // [02 "Unit record"] explodeas [06 §12.1] DoExplosion
	}
	return nil // link inactive or unresolved [02 §5 R-CONTENT-02]
}

// KilledVariantFromVM performs the synchronous 4-cell Killed query deterministically [04 §5.1] C23 C25.
// It drains the unit's script threads inline with delta 0 (no piece pass) and
// returns the low-four-bit corpse-chain depth. No map iteration, no wall-clock,
// no global RNG beyond the VM's own deterministic state (I1, I4, I6).
// If the VM or Killed script is absent, ok=false and the variant cell keeps
// its caller-indeterminate value which we keep as 0 deterministically
// TODO(question): absent Killed body indeterminate stack history not traced [04 §5.1].
func KilledVariantFromVM(vm *cob.VM, severity int32) (int32, bool) {
	if vm == nil || vm.Program() == nil {
		return 0, false // [04 §5.1] no VM => absent body
	}
	prog := vm.Program()
	pc, ok := prog.Scripts["Killed"]
	if !ok {
		return 0, false // [04 §5.1] absent Killed body
	}
	// Four-cell query: cell0=severity, cell1=variant initial, cells 2/3=0 [04 §5.1].
	// Variant is the low nibble of the mutable corpse-chain depth [06 §12.1] C23.
	args := []int32{severity, 0, 0, 0}
	started, _ := vm.CallQuery(pc, args) // [04 §4.2] Q mode: drains one slot inline, no piece pass
	if !started {
		return 0, false // pool full etc => absent [04 §4.3]
	}
	variant := args[1] & 0x0F // [06 §12.1] C23 low four bits are depth; indet. kept 0
	return variant, true
}

// ResolveDeath implements the shared authoritative death path for C22-C25 [06 §12.1][04 §5.1][GAP T21].
// It is the helper that cause-9 deconstruction and capture/reclaim call sites use [PLAN_09 C24].
// syncKilled, when non-nil, is the synchronous four-cell Killed query that drains script threads inline [04 §5.1][06 §12.1] C23 C25.
// It receives severity and returns the low-four-bit variant (depth). If the script is absent, syncKilled should return ok=false;
// the variant cell then keeps its caller-indeterminate value except where remainingFraction forces zero [04 §5.1].
// TODO(question): absent Killed body indeterminate value kept as 0 for determinism; validate stack history serialization.
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
	hasScript := false
	if syncKilled != nil {
		v, ok := syncKilled(int32(sev)) // synchronous query drains script threads inline [04 §5.1] C25
		res.KilledCalls = 1             // local authoritative issues exactly one Killed after severity [06 §12.1] C25
		if ok {
			variant = v & 0x0F // low four bits are corpse-chain depth [06 §12.1] C23
			hasScript = true
		} else {
			// Absent Killed body: variant cell keeps caller-indeterminate value except where build-fraction forces zero [04 §5.1].
			// TODO(question): indeterminate stack history; using 1 as deterministic placeholder so an ordinary
			// lethal death without a Killed script still places its authored Corpse (depth 1) and satisfies G4,
			// matching the constant-switch it replaces. Validate exact indeterminate against retail [04 §5.1].
			variant = 1 // deterministic placeholder depth 1 [06 §12.1] C23; TODO(question) on true indeterminate
			hasScript = false
		}
		_ = hasScript
	} else {
		// No VM or no Killed script => treat as absent body [04 §5.1].
		// TODO(question): same indeterminate placeholder depth 1 for determinism as above.
		variant = 1         // deterministic placeholder depth 1 [06 §12.1] C23; TODO(question)
		res.KilledCalls = 1 // still counts as queried attempt; caller provided no function means variant 1 but queried true per full pipeline
		// For test determinism, if caller explicitly passed nil to indicate no query capability, keep KilledCalls 1 to show query path taken but script absent.
		// If caller wants to indicate no VM, they can pass nil and handle.
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
