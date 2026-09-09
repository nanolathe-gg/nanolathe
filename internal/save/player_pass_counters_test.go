package save

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

// TestPlayerAccountCarriesNoPassAggregates locks the bounded-negative census of
// the `Player%i` account: it carries the two stocks, the six cumulative
// doubles, the two storage floats and the integer scalars, and it carries no
// per-pass income or expense rate [08 "Player records", WU-19-158 addendum].
//
// The four floats the HUD resource bar samples (PassProduced/PassConsumed) and
// the settled pair the planner scores with (AIProduction/AIConsumption) are
// therefore neither projected out nor restored in, and a restored slot reads
// them as the world rebuild left them — zero — until its next settlement pass
// rewrites all six from the pass's own gather [05 R-ECO-01 §6]
// [08 R-ENTRY-01 §3 step 24]. This test exists so that "the HUD reads zero
// right after Load" is not later "fixed" by inventing a save item.
func TestPlayerAccountCarriesNoPassAggregates(t *testing.T) {
	var src economy.Player
	src.Stock[economy.Energy], src.Stock[economy.Metal] = 900, 90
	src.TotalProduced[economy.Energy], src.TotalProduced[economy.Metal] = 5000, 500
	src.UpdateTime = 1234
	src.ControllerState = 2
	// The aggregates a live settlement would have written on the saved side.
	src.PassProduced[economy.Energy], src.PassProduced[economy.Metal] = 111, 22
	src.PassConsumed[economy.Energy], src.PassConsumed[economy.Metal] = 33, 4
	src.AIProduction[economy.Energy], src.AIProduction[economy.Metal] = 111, 22
	src.AIConsumption[economy.Energy], src.AIConsumption[economy.Metal] = 33, 4

	slot := PlayerSlotFromEconomy(1, src)

	// A freshly reset destination, as the load path's world rebuild leaves it.
	var dst economy.Player
	economy.InitPlayer(&dst)
	slot.ApplyToEconomy(&dst)

	// What the account does carry comes back.
	if dst.Stock[economy.Energy] != src.Stock[economy.Energy] || dst.UpdateTime != src.UpdateTime {
		t.Fatalf("stock/deadline not restored: stock %v deadline %d", dst.Stock, dst.UpdateTime)
	}
	// What it does not carry stays where the reset left it.
	for _, r := range [...]economy.Res{economy.Energy, economy.Metal} {
		if dst.PassProduced[r] != 0 || dst.PassConsumed[r] != 0 {
			t.Fatalf("resource %d: HUD per-pass rates restored (%v/%v); the account has no such item [08 \"Player records\"]",
				r, dst.PassProduced[r], dst.PassConsumed[r])
		}
		if dst.AIProduction[r] != 0 || dst.AIConsumption[r] != 0 {
			t.Fatalf("resource %d: planner aggregates restored (%v/%v); the account has no such item [08 \"Player records\"]",
				r, dst.AIProduction[r], dst.AIConsumption[r])
		}
	}
}
