package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestCarrierDeathCascadeRunsAtTheFinalizer locks the wiring of the cargo
// cascade into the composed session's death boundary. [06 §12.1] runs it
// inside the central death handler — after the fixed teardown helpers, after
// the victim's own carrier detach, before the death explosion and the corpse —
// and [04 R-FAC-02 §3] gives the same row from the factory side: a carrier
// that dies "kills every unit on its cargo list with 30000 damage (cause 3
// when the death record's kind nibble is 3, else cause 6) and detaches each".
//
// The observable at this boundary is the cargo's own state after the carrier's
// phase-2 finalizer runs: dead, detached, stamped with the cascade's cause and
// the carrier's killer.
func TestCarrierDeathCascadeRunsAtTheFinalizer(t *testing.T) {
	load := func(t *testing.T, s *Session) (carrier, cargo, killer *units.Unit) {
		t.Helper()
		all := s.Units.IterSliced()
		if len(all) < 3 {
			t.Fatalf("fixture produced %d units, need three", len(all))
		}
		carrier, cargo, killer = all[0], all[1], all[2]
		for _, u := range []*units.Unit{carrier, cargo, killer} {
			u.Health, u.MaxHealth = 100, 100
		}
		if !movement.AttachCargo(s.Units, carrier.Handle, cargo.Handle, 0) {
			t.Fatal("attach failed")
		}
		return carrier, cargo, killer
	}

	t.Run("OrdinaryCarrierDeathCascadesCauseSix", func(t *testing.T) {
		s := newLoopTestSession(t, 3)
		carrier, cargo, killer := load(t, s)
		// The carrier died to ordinary weapon damage: kind nibble 1.
		carrier.LastDamageCause = uint8(combat.CauseOrdinary)
		s.Units.DestroyBy(carrier.Handle, units.DeathKilled, killer.Handle)

		s.stepUnitPhase(1)

		if s.Units.Unit(carrier.Handle) != nil {
			t.Fatal("carrier was not finalized at its phase-2 slot")
		}
		if !cargo.Dying {
			t.Fatalf("cargo survived its carrier's finalisation: health %d [04 R-FAC-02 §3]", cargo.Health)
		}
		if got := combat.Cause(cargo.LastDamageCause); got != combat.CauseCargo {
			t.Fatalf("cargo cause = %d, want 6, the default cargo cascade [06 §12.1]", got)
		}
		if cargo.DeathCause != units.DeathKilled {
			t.Fatalf("cargo death label = %v, want DeathKilled", cargo.DeathCause)
		}
		if cargo.EngagementTarget != killer.Handle {
			t.Fatalf("cargo recorded attacker = %d, want the CARRIER's killer %d [06 §12.1][04 R-UNIT-06 §5]",
				cargo.EngagementTarget, killer.Handle)
		}
		if cargo.LastDamageSide != killer.Owner {
			t.Fatalf("cargo attacker side = %d, want the killer's owner %d [06 §9.1]", cargo.LastDamageSide, killer.Owner)
		}
		if cargo.Attachment.Carrier != 0 {
			t.Fatal("cargo was not detached by the cascade [04 R-FAC-02 §3]")
		}
	})

	t.Run("SelfDestructingCarrierPropagatesCauseThree", func(t *testing.T) {
		s := newLoopTestSession(t, 3)
		carrier, cargo, killer := load(t, s)
		// A carrier that blew itself up: kind nibble 3, the one value the
		// cascade propagates rather than replacing [06 §12.1].
		carrier.LastDamageCause = uint8(combat.CauseSelfDestruct)
		s.Units.DestroyBy(carrier.Handle, units.DeathSelfDestruct, killer.Handle)

		s.stepUnitPhase(1)

		if !cargo.Dying {
			t.Fatalf("cargo survived its carrier's self-destruct: health %d", cargo.Health)
		}
		if got := combat.Cause(cargo.LastDamageCause); got != combat.CauseSelfDestruct {
			t.Fatalf("cargo cause = %d, want 3 propagated from the carrier [06 §12.1]", got)
		}
		if cargo.DeathCause != units.DeathSelfDestruct {
			t.Fatalf("cargo death label = %v, want DeathSelfDestruct", cargo.DeathCause)
		}
		if cargo.EngagementTarget != killer.Handle {
			t.Fatalf("cargo recorded attacker = %d, want the carrier's killer %d", cargo.EngagementTarget, killer.Handle)
		}
	})

	t.Run("UnloadedCarrierDeathTouchesNothing", func(t *testing.T) {
		s := newLoopTestSession(t, 3)
		all := s.Units.IterSliced()
		carrier, bystander := all[0], all[1]
		bystander.Health, bystander.MaxHealth = 100, 100
		carrier.LastDamageCause = uint8(combat.CauseOrdinary)
		s.Units.DestroyBy(carrier.Handle, units.DeathKilled, 0)

		s.stepUnitPhase(1)

		if bystander.Dying || bystander.Health != 100 {
			t.Fatalf("a carrier with no cargo damaged an unrelated unit: dying=%v health=%d", bystander.Dying, bystander.Health)
		}
	})
}

// TestDeathCauseForResolutionPrefersTheRecordedKind locks the finalizer's cause
// selection: retail keeps one cause, the damage-kind byte recorded at damage
// time, and every branch of the death handler reads it [06 §12.1]. The coarse
// label is only the fallback for a death that retained no packet.
func TestDeathCauseForResolutionPrefersTheRecordedKind(t *testing.T) {
	cases := []struct {
		name  string
		label units.DeathCause
		kind  uint8
		want  combat.Cause
	}{
		// The cases the old label-only mapping collapsed to cause 1.
		{"CargoCascade", units.DeathKilled, uint8(combat.CauseCargo), combat.CauseCargo},
		{"WaterDamage", units.DeathKilled, uint8(combat.CauseWaterDamage), combat.CauseWaterDamage},
		{"Deconstruction", units.DeathKilled, uint8(combat.CauseDeconstruction), combat.CauseDeconstruction},
		{"FeatureConversion", units.DeathKilled, uint8(combat.CauseFeatureConversion), combat.CauseFeatureConversion},
		// The kind byte wins over a disagreeing label.
		{"KindOverridesLabel", units.DeathKilled, uint8(combat.CauseReclaim), combat.CauseReclaim},
		// No packet: the label is the fallback.
		{"PacketlessKilled", units.DeathKilled, 0, combat.CauseOrdinary},
		{"PacketlessSelfDestruct", units.DeathSelfDestruct, 0, combat.CauseSelfDestruct},
		{"PacketlessReclaim", units.DeathReclaimed, 0, combat.CauseReclaim},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &units.Unit{LastDamageCause: tc.kind}
			if got := deathCauseForResolution(tc.label, u); got != tc.want {
				t.Fatalf("cause = %d, want %d [06 §12.1]", got, tc.want)
			}
		})
	}
}
