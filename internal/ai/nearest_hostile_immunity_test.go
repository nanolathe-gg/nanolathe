package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// nearestHostileUnit's third and fourth candidate tests were corrected
// 2026-09-02 [08 R-AI-01 §9]: the third test reads status-word bit 15 (the
// mission Immunity bit, units.ImmunityStatus), not the death latch, and the
// fourth reads the runtime cloaked-INSTANCE bit this build carries as
// Unit.Hidden, not bit 2 of the status word.
func TestNearestHostileUnitSkipsImmuneAndCloakedInstance(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "enemy"}, UnitName: "enemy", MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"enemy": def}}
	w := newAIFixtureWorld(4, cat)

	// Dying-but-not-immune: closest, and dying alone must not exclude it.
	dying, err := w.Create(def, 1, numeric.FixedFromInt(10), 0, numeric.FixedFromInt(10))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(dying).Dying = true

	// Immune: closer still, but must be skipped regardless of Dying.
	immune, err := w.Create(def, 1, numeric.FixedFromInt(1), 0, numeric.FixedFromInt(1))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(immune).Flags |= units.ImmunityStatus

	// Cloaked-instance: closest of all, but must be skipped.
	cloaked, err := w.Create(def, 1, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(0))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(cloaked).Hidden = true

	e := runtimeEconomy(0, 2)
	e.Players[1].Exists, e.Players[1].ControllerState = true, 1
	m := &Manager{Player: 0, IsAlliance: func(uint8, uint8) bool { return false }}

	got := m.nearestHostileUnit(w, e, numeric.FixedFromInt(10), 0, numeric.FixedFromInt(10))
	if got == nil || got.Handle != dying {
		t.Fatalf("nearest hostile=%v, want the dying-but-not-immune unit %d (immune and cloaked-instance candidates must be skipped)", got, dying)
	}

	// With the dying candidate also removed, nothing qualifies.
	w.Unit(dying).Flags |= units.ImmunityStatus
	if got := m.nearestHostileUnit(w, e, numeric.FixedFromInt(10), 0, numeric.FixedFromInt(10)); got != nil {
		t.Fatalf("all three candidates excluded, got %v", got)
	}

	// Clearing Immunity through MakeSelectable (the retail writer) makes the
	// unit a candidate again.
	w.Unit(dying).MakeSelectable()
	w.Unit(dying).Dying = false
	if got := m.nearestHostileUnit(w, e, numeric.FixedFromInt(10), 0, numeric.FixedFromInt(10)); got == nil || got.Handle != dying {
		t.Fatalf("after MakeSelectable clears Immunity, nearest=%v, want %d", got, dying)
	}
}

// The 30-tick refresh's first vector is non-allied live units that pass
// visibility and whose mission Immunity bit is clear [08 R-AI-01 §16]. The
// bit was not tested here, so a campaign rally point drifted toward units the
// planner is forbidden to attack.
func TestRallyVectorSkipsMissionImmuneUnits(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "enemy"}, UnitName: "enemy", MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"enemy": def}}
	w := newAIFixtureWorld(4, cat)

	plain, err := w.Create(def, 1, numeric.FixedFromInt(20), 0, numeric.FixedFromInt(20))
	if err != nil {
		t.Fatal(err)
	}
	immune, err := w.Create(def, 1, numeric.FixedFromInt(10), 0, numeric.FixedFromInt(10))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(immune).Flags |= units.ImmunityStatus

	e := runtimeEconomy(0, 2)
	e.Players[1].Exists, e.Players[1].ControllerState = true, 1
	m := &Manager{
		Player:       0,
		IsAlliance:   func(uint8, uint8) bool { return false },
		RallyVisible: func(uint8, *units.Unit) bool { return true },
	}

	m.refreshRallyTargets(w, e)
	if len(m.rallyTargets) != 1 || m.rallyTargets[0] != plain {
		t.Fatalf("rally vector = %v, want only the non-immune unit %d", m.rallyTargets, plain)
	}

	// Clearing Immunity through MakeSelectable readmits it, and setting the
	// bit on the other one empties the vector.
	w.Unit(immune).MakeSelectable()
	w.Unit(plain).Flags |= units.ImmunityStatus
	m.refreshRallyTargets(w, e)
	if len(m.rallyTargets) != 1 || m.rallyTargets[0] != immune {
		t.Fatalf("rally vector = %v, want only the cleared unit %d", m.rallyTargets, immune)
	}
}
