package movement

import (
	"testing"
)

// TestCarrierDeathCascadeIgnoresAttackerVeterancy locks the amount half of the
// carrier-finalisation row against the truncation that makes it easy to regress
// silently.
//
// [06 §12.1]: each cargo unit receives a 30,000 damage packet "through the
// ordinary builder — so it is scaled by defender veterancy but not by the
// armored-state modifier, whose gate is a strict `< 30,000`". The builder is
// the defender-side stage: [06 §9.1] has the amount "computed by one shared
// routine (§9.2) and handed to the packet builder, which applies the
// defender-side scales", and §9.2's listing puts the attacker tier above its
// `-- packet builder, defender side --` line, gated on a damage record's
// shooter — a record the cascade never builds. §9.2 then calls this one of "the
// fixed 30,000 self-damage, cargo-cascade and refund packets".
//
// The regression this guards is not a rounding difference. The packet amount is
// truncated to sixteen bits (§9.2 step 7) and applied as a modular subtraction
// read back as signed (§9.1), so scaling 30,000 by a tier-2 attacker's
// 106%-per-tier ladder gives 33,600 — negative as an int16. The cargo would be
// HEALED to about 32,000 health by the death of its carrier, and would survive.
// A tier-2 killer is ten kills, which any veteran unit reaches.
func TestCarrierDeathCascadeIgnoresAttackerVeterancy(t *testing.T) {
	for _, kills := range []int32{0, 5, 10, 25, 100} {
		w, system, carrier, cargo, killer := newCarrierWithCargo(t)
		killer.Kills = kills
		system.HandleDeath(w, carrier.Handle, killer.Handle)
		if !cargo.Dying || cargo.Health > 0 {
			t.Fatalf("killer with %d kills: cargo survived the cascade with health %d; the 30000 packet "+
				"must not be scaled by attacker veterancy [06 §12.1][06 §9.1][06 §9.2]", kills, cargo.Health)
		}
	}
}
