// Construction arithmetic and the work step [05 "Construction arithmetic"][05
// R-WORK-01 §1][05 R-WORK-01 §3]: the quanta, the remaining-fraction step, and
// the assist/repair visits that spend them.
//
// Moved out of factory.go by CL-5, which split that file by concern; the code
// is unchanged.

package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// WorkerQuantum derives integer worker quantum floor(workerTime/30) [05 "Construction arithmetic"].
func WorkerQuantum(workerTime int32) int32 {
	// The definition word is read unsigned before the integer division [05
	// "Construction arithmetic"].
	return int32(uint16(workerTime) / 30)
}

// HealQuantum derives the worker quantum the `healtime` self-repair path hands
// to Repair: `((uint16)healtime × 8) / 30` [05 R-WORK-01 §3, "`healtime`, the
// only consumer"].
//
// The eight is the CADENCE, not a rate multiplier. The self-repair branch runs
// once every eight ticks (`tick & 7 == 0`), so multiplying by eight before the
// same divide-by-thirty that WorkerQuantum performs expresses `healtime` on the
// identical per-30-tick scale as `workertime`: a definition wanting the same
// work per second as a `workertime` builder authors the same number.
//
// The definition word is signed 16-bit but is read UNSIGNED here, exactly as
// `workertime` is, so the low sixteen bits are zero-extended before the
// multiply. The multiply and the divide are 32-bit, so a large authored value
// does not wrap in the eight.
//
// Because both of Repair's terms are clamped to exactly one whenever positive,
// any `healtime` big enough to make this quotient non-zero (`healtime >= 4`)
// yields the same observable rate — one health point and one energy unit per
// eight ticks — and a `healtime` of 1..3 yields a zero quantum, hence zero
// terms, hence no healing and no charge [05 R-WORK-01 §3].
func HealQuantum(healTime int32) int32 {
	return int32(uint16(healTime)) * 8 / 30
}

// wideConstructionStepQuantum performs one construction helper step
// [05 "Construction arithmetic"], returning newRemaining, healthGain,
// energyDemand and metalDemand.
//
// The quantum is in its retail type. Every ordinary caller's quantum is an
// integer converted to float, but the decay wrapper's is not, and its
// infinities and NaNs have to survive the division and the clamp exactly as
// x87 leaves them [05 R-WORK-01 §1][05 R-WORK-01 §11].
func wideConstructionStepQuantum(old float32, quantum float32, buildTime int32, maxDamage int32, energyCost, metalCost float32) (float32, int32, float32, float32) {
	old80 := float64(old)
	new80 := old80 - float64(quantum)/float64(buildTime)
	if new80 <= 0 {
		new80 = 0
	}
	if new80 >= 1 {
		new80 = 1
	}
	newStored := float32(new80)
	delta32 := float32(old80 - float64(newStored))
	energy := float32(float64(energyCost) * float64(delta32))
	metal := float32(float64(metalCost) * float64(delta32))
	max80 := float64(uint32(maxDamage))
	// Each product uses the shared signed-64 truncation and low-word result;
	// subtraction then wraps in the health word [01 R-DET-01 §1][05 R-WORK-01 §1].
	healthGain := numeric.TruncateFloat64ToLow32(max80*old80) - numeric.TruncateFloat64ToLow32(max80*float64(newStored))
	return newStored, healthGain, energy, metal
}

func repairTerms(maxDamage int32, energyCost float32, worker, buildTime int32) (int32, int32) {
	// The shared low-word conversion also handles zero build time and other
	// exceptional results [05 R-WORK-01 §3][01 R-DET-01 §1].
	heal := numeric.TruncateFloat64ToLow32(1 + (float64(maxDamage)*float64(worker)-1)/float64(buildTime))
	energy := numeric.TruncateFloat64ToLow32(1 + (float64(energyCost)*float64(worker)-1)/float64(buildTime))
	if heal >= 1 {
		heal = 1
	}
	if energy >= 1 {
		energy = 1
	}
	return heal, energy
}

// Assist applies one ordinary construction work step to a live nanoframe.
// It is the session-bound entry point for HelpBuild: admission, progress,
// health, and decay deferral remain owned here just as they are for factory
// products [05 R-WORK-01 §1].
func (s *Service) Assist(builder, target *units.Unit, tick uint32) bool {
	committed := s.applyWorkStep(builder, target, tick)
	// The stored fraction's zero test, not this step's committed return, is what
	// governs the completion transition: [05 R-WORK-01 §1] places it "on both
	// arms and also on the admission-refused path", after EVERY exit of the
	// shared step. This is the same correction WU-19-132 made to the factory's
	// state-3 body, applied to the sibling site — §1's FIRST line makes a step
	// on an already-zero fraction return not-committed, so gating the zero test
	// on the committed return is how a completion gets dropped.
	//
	// Assist is the owning boundary for a mobile helper's final increment: the
	// step that stores the zero is the step that runs the transition, and its
	// builder is whichever builder called it — the helper, not the frame's
	// original builder.
	if target != nil && target.Remaining == 0 {
		s.applyCompletionPosture(target)
	}
	return committed
}

// applyWorkStep is the ordinary forward entry to the shared step: it derives
// the integer worker quantum from the builder's own definition and hands it to
// sharedStep as a float32 [05 "Construction arithmetic"][05 R-WORK-01 §1].
//
// The tick is carried through to the reverse arm's terminal packet. Decay
// suppression itself is the pending word's `0x8000`, not a tick stamp the step
// computes [04 R-ORD-01 §11].
func (s *Service) applyWorkStep(builder, target *units.Unit, tick uint32) bool {
	if s == nil || builder == nil || builder.Def == nil {
		return false
	}
	// `(uint16)workertime / 30` is an integer division performed BEFORE the
	// conversion to float [05 "Construction arithmetic"].
	return s.sharedStep(builder, target, float32(WorkerQuantum(builder.Def.WorkerTime)), tick)
}

// sharedStep is the one construction helper every build, assist, factory-
// product and deconstruction step runs, written out in [05 R-WORK-01 §1]. It
// takes a builder, a target and a single-precision worker quantum and reports
// whether work was committed. Both arms live here because retail has one
// helper: the sign of the quantum picks the arm, and those same entry compares
// are what settle a malformed definition [05 R-WORK-01 §11].
//
// The quantum is float32 because retail's is: the wrapper that forms the decay
// quantum divides by a single-precision cost, so a zero `buildcostenergy`
// reaches the compares as −∞ and a zero `buildtime` with it as a NaN. Both are
// x87 compares, and an unordered operand reads as negative to the first and as
// equal to zero to the second, which is why a NaN quantum writes nothing and
// raises no wake bit [05 R-WORK-01 §11]. Go's own `<` and `==` are false for a
// NaN, so the unordered arm is spelled out.
//
// I2 allows the float32: the row is "Construction remaining fraction and its
// proportional cost/health intermediates" [05 "Construction arithmetic"], and
// §11 establishes that the quantum itself is single precision in retail.
func (s *Service) sharedStep(builder, target *units.Unit, quantum float32, tick uint32) bool {
	if s == nil || target == nil || target.Def == nil {
		return false
	}
	// The exact float compare on the stored fraction [05 R-WORK-01 §1].
	if target.Remaining == 0 {
		return false
	}
	// `if (worker >= 0.0f) target.pendingWord |= 0x8000` — the wake store runs
	// before the zero-quantum test and before the admission, so a refused step
	// and a zero quantum both defer the decay [04 R-ORD-01 §11].
	unordered := quantum != quantum
	forward := quantum >= 0 && !unordered
	if forward {
		raiseUnderConstructionWake(target)
	}
	// `if (worker == 0.0f) return notCommitted`, the compare an unordered
	// quantum also takes [05 R-WORK-01 §11].
	if quantum == 0 || unordered {
		return false
	}
	def := target.Def
	old := target.Remaining
	newStored, healthGain, energy, metal := wideConstructionStepQuantum(old, quantum, def.BuildTime, def.MaxDamage, def.BuildCostEnergy, def.BuildCostMetal)
	if forward {
		if builder == nil || s.Economy == nil {
			return false
		}
		if !economy.AdmitTwoResource(s.Economy.UnitBuckets(builder.Handle), energy, metal) {
			return false
		}
		health := target.Health + healthGain
		// The maximum-health cap is an UNSIGNED comparison, so a health that
		// went negative is clamped up to maxdamage [05 R-WORK-01 §1].
		if uint32(health) >= uint32(def.MaxDamage) {
			health = def.MaxDamage
		}
		target.Health = int32(int16(health))
		target.Remaining = newStored
		return true
	}
	// Reverse arm: metal-only, credited direct to the TARGET's bucket through
	// the special-player selector, health floored at zero with no maxdamage cap,
	// and the clamp to 1.0 killing the frame [05 R-WORK-01 §1].
	refund := -metal
	var bucket *float32
	if s.Economy != nil {
		if buckets := s.Economy.UnitBuckets(target.Handle); buckets != nil {
			bucket = &buckets[economy.Metal].Production
		}
	}
	// The special-player selector credits 0.5 or 0.7 of the amount and any other
	// value the whole of it, tied to the TARGET's owner [05 R-WORK-01 §1]
	// [05 "Cancel-current and stop interrupts"]. With no ledger there is no
	// bucket to credit and the refund is simply not paid; it is never diverted
	// to another field.
	special := s.IsSpecialSecondState != nil && s.IsSpecialSecondState(target.Owner)
	if bucket != nil {
		ReverseRefund(bucket, refund, special, s.ModeSelector)
	}
	health := target.Health + healthGain
	if health < 1 {
		health = 0
	}
	target.Health = int32(int16(health))
	target.Remaining = newStored
	if newStored >= 1 {
		// `selfKill(target, target, 30000, kind 9)` — the reverse arm's own
		// last line, and the only thing that removes an abandoned frame
		// [05 R-WORK-01 §1][05 R-WORK-01 §9].
		s.killDecayedNanoframe(target, tick)
	}
	return true
}

// Repair applies one accepted repair packet. The packet is formed at this
// service boundary so repair shares combat's kind-10 early-heal path [05
// R-WORK-01 §3][06 §9.1].
func (s *Service) Repair(builder, target *units.Unit, worker int32) bool {
	if s == nil || builder == nil || target == nil || target.Def == nil {
		return false
	}
	def := target.Def
	if def.MaxDamage <= int32(int16(target.Health)) {
		return false
	}
	heal, energy := repairTerms(def.MaxDamage, def.BuildCostEnergy, worker, def.BuildTime)
	if s.Economy == nil {
		return false
	}
	buckets := s.Economy.UnitBuckets(builder.Handle)
	if !economy.AdmitOneResource(buckets, float32(energy)) {
		return false
	}
	if s.Combat == nil || s.World == nil {
		return false
	}
	return s.Combat.DispatchHealingPacket(s.World, combat.Packet{
		Victim: uint16(target.Handle), Attacker: uint16(builder.Handle),
		Amount: uint16(heal), Kind: combat.KindHeal,
	})
}

// ---------------------------------------------------------------------------
// C16 exit-spot acquisition [05 "Factory production lifecycle"].
// ---------------------------------------------------------------------------
