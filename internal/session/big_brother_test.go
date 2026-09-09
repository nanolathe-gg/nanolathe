package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Locks the sweep-tail ordering, pause and enable lifecycle established in
// [04 R-MOV-03 §1] and [07 R-CAM-01 §12].
func TestBigBrotherSelectionAndToggleLifecycle(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", MaxDamage: 10}
	w := newSessionFixtureWorld(8, cat)
	create := func(owner uint8) *units.Unit {
		t.Helper()
		h, err := w.Create(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		u := w.Unit(h)
		u.Flags |= units.ClassifierEligibleStatus
		return u
	}
	first, unfinished, last, foreign := create(0), create(0), create(0), create(1)
	unfinished.Remaining = 0.5
	first.Flags |= 0xD0
	foreign.Flags |= 0xD0
	s := &Session{Units: w}
	queue := func(c HumanCommand) {
		t.Helper()
		if err := s.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	queue(HumanCommand{Kind: HumanBigBrother})
	queue(HumanCommand{Kind: HumanShiftState, ShiftHeld: true})
	s.applyHumanCommands(1)
	s.stepBigBrother()
	if s.bigBrother.countdown != 1 || s.bigBrother.cycle {
		t.Fatal("held Shift did not pause the first cycle")
	}
	queue(HumanCommand{Kind: HumanShiftState})
	s.applyHumanCommands(2)
	s.stepBigBrother()
	if s.bigBrother.countdown != 90 || !s.bigBrother.cycle || !s.bigBrother.resetVisited || last.Flags&0x10 == 0 || first.Flags&0xD0 != 0 || foreign.Flags&0xD0 != 0 {
		t.Fatal("first cycle did not clear the whole pool then select the next ready unit")
	}
	s.resetBigBrotherEvents()
	s.stepBigBrother()
	if s.bigBrother.countdown != 89 || s.bigBrother.cycle || s.bigBrother.resetVisited {
		t.Fatal("ordinary tick did not decrement without cycling")
	}
	s.bigBrother.countdown = 1
	s.stepBigBrother()
	if first.Flags&0x10 == 0 || last.Flags&0x10 != 0 {
		t.Fatal("end of local slice did not wrap to first ready unit")
	}
	s.applyHumanCommand(HumanCommand{Kind: HumanBigBrother}, 3)
	if s.bigBrother.enabled || !s.bigBrother.cancelFollow || s.bigBrother.countdown != 90 {
		t.Fatal("disable did not preserve counter and cancel follow")
	}
	s.resetBigBrotherEvents()
	s.stepBigBrother()
	if s.bigBrother.countdown != 90 || s.bigBrother.cancelFollow {
		t.Fatal("disabled tick changed counter or retained event")
	}
	first.Flags &^= 0x10
	foreign.Flags |= 0xD0
	s.applyHumanCommand(HumanCommand{Kind: HumanBigBrother}, 4)
	s.stepBigBrother()
	if !s.bigBrother.cycle || s.bigBrother.resetVisited || first.Flags&0x10 == 0 || foreign.Flags&0xD0 != 0xD0 {
		t.Fatal("no selected ready unit must select first without whole-pool clear")
	}
	first.Remaining, last.Remaining = 1, 1
	s.resetBigBrotherEvents()
	s.bigBrother.countdown = 0
	s.stepBigBrother()
	if !s.bigBrother.cycle || s.bigBrother.resetVisited || s.bigBrother.countdown != 90 {
		t.Fatal("signed decrement through zero must notify even with no ready units")
	}
}
