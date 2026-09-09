package triggers

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestCarrierClauseAdmitsCargoUnderAnAirbaseCarrier locks the write side
// (WU-19-81, [04 R-UNIT-06 §3]) meeting the read side (`eligibleMissionUnit`'s
// carrier clause, [08 R-TRIG-01 §3]) end to end: a local-player unit carried
// by a unit whose definition has `isairbase` counts as a live unit for
// AllUnitsKilled, while the same cargo under an ordinary (non-airbase)
// carrier does not.
//
// Before WU-19-81, nothing ever set the cargo-selectable mirror bit, so this
// clause rejected every carried unit regardless of its carrier's definition.
func TestCarrierClauseAdmitsCargoUnderAnAirbaseCarrier(t *testing.T) {
	t.Run("airbase carrier admits cargo", func(t *testing.T) {
		w := triggerWorld(t)
		cargo := spawn(t, w, "ARMCOM", 0, 0, 0)

		carrierDef := &content.UnitDef{}
		carrierDef.UnitName = "ARMAAS"
		carrierDef.MaxDamage = 100
		carrierDef.IsAirBase = true
		carrierHandle, err := w.Create(carrierDef, 1, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(0))
		if err != nil {
			t.Fatalf("create airbase carrier: %v", err)
		}
		cargo.Attachment.Carrier = carrierHandle

		tr := New(KindAllUnitsKilled, "")
		if tr.Poll(pollCtx(w, 0)) {
			t.Fatal("cargo carried by an isairbase carrier should count as a live unit, but AllUnitsKilled reported satisfied")
		}
	})

	t.Run("ordinary carrier does not admit cargo", func(t *testing.T) {
		w := triggerWorld(t)
		cargo := spawn(t, w, "ARMCOM", 0, 0, 0)

		carrierDef := &content.UnitDef{}
		carrierDef.UnitName = "ARMSHTRANS"
		carrierDef.MaxDamage = 100
		carrierHandle, err := w.Create(carrierDef, 1, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(0))
		if err != nil {
			t.Fatalf("create ordinary carrier: %v", err)
		}
		cargo.Attachment.Carrier = carrierHandle

		tr := New(KindAllUnitsKilled, "")
		if !tr.Poll(pollCtx(w, 0)) {
			t.Fatal("cargo carried by a non-airbase carrier should not count as a live unit, but AllUnitsKilled reported unsatisfied")
		}
	})
}
