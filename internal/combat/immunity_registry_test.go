package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
)

// The mission Immunity bit (units.ImmunityStatus, status-word bit 15) keeps a
// hostile off the target registry's PRIMARY list only; the secondary seen-bit
// list is unaffected, and MakeSelectable's clear of the bit makes the unit
// primary-list-eligible again starting with the next rebuild [06 §3.1 "the
// primary-list exclusion bit is the mission Immunity bit"].
func TestImmuneHostileIsExcludedFromPrimaryListOnly(t *testing.T) {
	f := newRegistryFixture(t, false) // plainly visible, not cloaked
	f.onProjectedGrid()
	f.enemy.Flags |= units.ImmunityStatus
	f.sensorTick(1)

	s := &Service{}
	s.stepTargetRegistries(targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)

	for _, h := range s.targets.primaryList(0) {
		if h == f.enemy.Handle {
			t.Fatal("an immune hostile must not be on the primary list [06 §3.1]")
		}
	}
	foundSecondary := false
	for _, h := range s.targets.secondaryList(0) {
		if h == f.enemy.Handle {
			foundSecondary = true
		}
	}
	if !foundSecondary {
		t.Fatal("the Immunity bit gates the primary list only; the seen-bit secondary list is unaffected [06 §3.1]")
	}

	// A MakeSelectable order clears the bit; the unit is not retroactively
	// filed on the current list — only the NEXT rebuild sees the change.
	f.enemy.MakeSelectable()
	for _, h := range s.targets.primaryList(0) {
		if h == f.enemy.Handle {
			t.Fatal("clearing the bit does not retroactively edit the list the last rebuild filed")
		}
	}

	s.stepTargetRegistries(2*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	found := false
	for _, h := range s.targets.primaryList(0) {
		if h == f.enemy.Handle {
			found = true
		}
	}
	if !found {
		t.Fatal("after MakeSelectable clears the Immunity bit, the next rebuild files the unit on the primary list")
	}
}
