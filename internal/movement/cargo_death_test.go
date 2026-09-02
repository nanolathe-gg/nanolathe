package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestCarrierDeathCascadeCreditsTheCarriersKiller locks the attribution edge of
// [06 §12.1]: "cargo killed by carrier death credits the carrier's killer ...
// the cascade applies its 30000 damage per cargo with the attacker argument set
// to the carrier's killer". The attacker argument is what the death handler's
// row writes into each cargo's recorded-attacker link [04 R-UNIT-06 §5], so the
// link is the observable — and it is the carrier's killer, never the carrier.
//
// The killer handle was already a parameter of the cascade and was discarded
// before WU-19-49b, so every cargo died attributed to nobody.
func TestCarrierDeathCascadeCreditsTheCarriersKiller(t *testing.T) {
	newCarrierWithCargo := func(t *testing.T) (*units.World, *System, *units.Unit, *units.Unit, *units.Unit) {
		t.Helper()
		terrain := syntheticTerrainFlat()
		system := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
		w := newMovementFixtureWorld(8)
		def := &content.UnitDef{UnitName: "cargofixture", FootprintX: 1, FootprintZ: 1, MaxDamage: 100, Limit: -1}
		at := func(owner uint8, cx, cz int32) *units.Unit {
			h, err := w.Create(def, owner, world.CellToWorld(cx), numeric.FixedFromInt(10), world.CellToWorld(cz))
			if err != nil {
				t.Fatalf("create unit: %v", err)
			}
			u := w.Unit(h)
			u.Health, u.MaxHealth = 100, 100
			return u
		}
		carrier, cargo, killer := at(0, 2, 2), at(0, 3, 3), at(1, 5, 5)
		system.BindWorld(w)
		if !AttachCargo(w, carrier.Handle, cargo.Handle, 0) {
			t.Fatal("attach failed")
		}
		return w, system, carrier, cargo, killer
	}

	t.Run("KillerIsCredited", func(t *testing.T) {
		w, system, carrier, cargo, killer := newCarrierWithCargo(t)
		system.HandleDeath(w, carrier.Handle, 0x30, killer.Handle)
		if !cargo.Dying {
			t.Fatalf("the cascade's 30000 did not kill the cargo: health %d", cargo.Health)
		}
		if cargo.EngagementTarget != killer.Handle {
			t.Fatalf("cargo recorded attacker = %d, want the CARRIER's killer %d [06 §12.1][04 R-UNIT-06 §5]",
				cargo.EngagementTarget, killer.Handle)
		}
		if cargo.EngagementTarget == carrier.Handle {
			t.Fatal("the cascade credited the carrier itself; the attacker argument is the carrier's killer [06 §12.1]")
		}
	})

	t.Run("NoKillerPassesTheNullThrough", func(t *testing.T) {
		w, system, carrier, cargo, killer := newCarrierWithCargo(t)
		cargo.EngagementTarget = killer.Handle // an earlier attacker of the cargo itself
		system.HandleDeath(w, carrier.Handle, 0x30, 0)
		if !cargo.Dying {
			t.Fatalf("the cascade's 30000 did not kill the cargo: health %d", cargo.Health)
		}
		if cargo.EngagementTarget != 0 {
			t.Fatalf("cargo recorded attacker = %d after a killerless carrier death, want null: the death row writes the packet's attacker unconditionally [04 R-UNIT-06 §5]",
				cargo.EngagementTarget)
		}
	})
}
