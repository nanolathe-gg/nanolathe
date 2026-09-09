package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// newCarrierWithCargo builds a carrier holding one cargo unit plus a separate
// killer owned by another player, all on flat synthetic terrain.
func newCarrierWithCargo(t *testing.T) (*units.World, *System, *units.Unit, *units.Unit, *units.Unit) {
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
	bindCargoDamageFixture(system, w)
	if !AttachCargo(w, carrier.Handle, cargo.Handle, 0) {
		t.Fatal("attach failed")
	}
	return w, system, carrier, cargo, killer
}

// TestCarrierDeathCascadeCause locks the cause half of the carrier-finalisation
// row: a carrier that dies "kills every unit on its cargo list with 30000
// damage (cause 3 when the death record's kind nibble is 3, else cause 6)"
// [04 R-FAC-02 §3], which [06 §12.1] states as "the cause passed is 3 when the
// carrier's own cause nibble is 3 and 6 otherwise" and its producer list gives
// as cause 3 propagating to cargo while "any other carrier cause cascades its
// cargo as cause 6".
//
// The kind nibble is the damage-kind byte recorded at damage time [06 §12.1],
// which is Unit.LastDamageCause here — the same field internal/combat's intake
// stamps and internal/session reads back for death statistics. Before this
// unit the cascade passed DeathKilled unconditionally and stamped no cause at
// all, so a self-destructing carrier's cargo took the full-credit path that
// [06 §12.1] reserves for causes 1 and 6.
func TestCarrierDeathCascadeCause(t *testing.T) {
	cases := []struct {
		name       string
		carrier    combat.Cause
		wantCause  combat.Cause
		wantRecord units.DeathCause
	}{
		// The one branch that propagates: self-destruct, cause 3 [06 §12.1].
		{"SelfDestructPropagates", combat.CauseSelfDestruct, combat.CauseSelfDestruct, units.DeathSelfDestruct},
		// Every other carrier cause takes the default cargo cascade, cause 6.
		{"OrdinaryWeapon", combat.CauseOrdinary, combat.CauseCargo, units.DeathKilled},
		{"Reclaim", combat.CauseReclaim, combat.CauseCargo, units.DeathKilled},
		{"Deconstruction", combat.CauseDeconstruction, combat.CauseCargo, units.DeathKilled},
		{"Teardown", combat.CauseTeardown, combat.CauseCargo, units.DeathKilled},
		{"WaterDamage", combat.CauseWaterDamage, combat.CauseCargo, units.DeathKilled},
		// A carrier that reached finalisation with no recorded packet at all.
		{"NoRecordedPacket", 0, combat.CauseCargo, units.DeathKilled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, system, carrier, cargo, killer := newCarrierWithCargo(t)
			carrier.LastDamageCause = uint8(tc.carrier)
			system.HandleDeath(w, carrier.Handle, killer.Handle, 0)
			if !cargo.Dying {
				t.Fatalf("the cascade's 30000 did not kill the cargo: health %d", cargo.Health)
			}
			if got := combat.Cause(cargo.LastDamageCause); got != tc.wantCause {
				t.Fatalf("carrier kind nibble %d cascaded cause %d, want %d [04 R-FAC-02 §3][06 §12.1]",
					tc.carrier, got, tc.wantCause)
			}
			if cargo.DeathCause != tc.wantRecord {
				t.Fatalf("carrier kind nibble %d recorded death cause %v, want %v",
					tc.carrier, cargo.DeathCause, tc.wantRecord)
			}
			// The attacker stays the carrier's killer whichever cause is
			// cascaded [06 §12.1][04 R-UNIT-06 §5], and the side snapshot
			// beside it is that attacker's owner byte [06 §9.1].
			if cargo.EngagementTarget != killer.Handle {
				t.Fatalf("cargo recorded attacker = %d, want the carrier's killer %d", cargo.EngagementTarget, killer.Handle)
			}
			if cargo.LastDamageSide != killer.Owner {
				t.Fatalf("cargo attacker-side snapshot = %d, want the killer's owner %d [06 §9.1]", cargo.LastDamageSide, killer.Owner)
			}
			if IsCarried(w, cargo.Handle) {
				t.Fatal("cargo still attached after the carrier's finalisation [04 R-FAC-02 §3]")
			}
		})
	}
}

// TestCarrierDeathCascadeNeutralSideWithoutKiller locks the null-attacker half
// of the side snapshot: a packet with no attacker carries the neutral side 10,
// so no kill clause of [06 §12.1] can credit player 0 for a killerless
// carrier's cargo.
func TestCarrierDeathCascadeNeutralSideWithoutKiller(t *testing.T) {
	w, system, carrier, cargo, _ := newCarrierWithCargo(t)
	carrier.LastDamageCause = uint8(combat.CauseOrdinary)
	system.HandleDeath(w, carrier.Handle, 0, 0)
	if cargo.LastDamageSide != units.NeutralAttackerSide {
		t.Fatalf("cargo attacker-side snapshot = %d after a killerless carrier death, want the neutral side %d [06 R-WPN-04 §2]",
			cargo.LastDamageSide, units.NeutralAttackerSide)
	}
	if got := combat.Cause(cargo.LastDamageCause); got != combat.CauseCargo {
		t.Fatalf("cascaded cause %d, want 6 [06 §12.1]", got)
	}
}

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
	t.Run("KillerIsCredited", func(t *testing.T) {
		w, system, carrier, cargo, killer := newCarrierWithCargo(t)
		carrier.LastDamageCause = uint8(combat.CauseOrdinary)
		system.HandleDeath(w, carrier.Handle, killer.Handle, 0)
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
		carrier.LastDamageCause = uint8(combat.CauseOrdinary)
		system.HandleDeath(w, carrier.Handle, 0, 0)
		if !cargo.Dying {
			t.Fatalf("the cascade's 30000 did not kill the cargo: health %d", cargo.Health)
		}
		if cargo.EngagementTarget != 0 {
			t.Fatalf("cargo recorded attacker = %d after a killerless carrier death, want null: the death row writes the packet's attacker unconditionally [04 R-UNIT-06 §5]",
				cargo.EngagementTarget)
		}
	})
}

func bindCargoDamageFixture(system *System, w *units.World) *combat.Service {
	service := &combat.Service{ControlByte: func(uint8) uint8 { return combat.ControlByteHuman }}
	system.Damage = func(tick uint32, input combat.DamageInput) combat.DamageResult {
		return service.AcceptDamage(w, tick, input)
	}
	return service
}
