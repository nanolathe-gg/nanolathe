package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func minimalWorld() *world.Terrain {
	attrs := make([]formats.TNTAttribute, 32*32)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 32, 32)
	ter := &world.Terrain{CellW: 32, CellH: 32, Plot: plot, Version: 0x2000, SeaLevel: 0, WindMin: 100, WindMax: 2000}
	_ = ter.ApplySchema(nil, 0)
	return ter
}

func TestKillUnitTypeOnlyOnDeath(t *testing.T) {
	// KillUnitType should not advance from poll alone, only on death notification.
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, Features: map[string]*content.FeatureDef{}}
	w := newSessionFixtureWorld(16, cat)
	def := &content.UnitDef{UnitName: "CORLAB", MaxDamage: 100, CanMove: true}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	h, _ := w.Create(def, 1, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(0))
	u := w.Unit(h)
	tr := triggers.New(triggers.KindKillUnitType, "CORLAB", 2)
	ctx := triggers.PollContext{Tick: 0, World: w, LocalOwner: 0, EnemyOwner: 1}
	// Poll 100 times should not advance
	for i := 0; i < 100; i++ {
		tr.Poll(ctx)
	}
	if tr.Args[0] != 2 {
		t.Fatalf("poll advanced kill countdown to %d", tr.Args[0])
	}
	// Notify death should advance
	if tr.Notify(ctx, triggers.NotifyUnitDied, u) {
		t.Fatal("should not complete after one kill")
	}
	if tr.Args[0] != 1 {
		t.Fatalf("after one kill count %d want 1", tr.Args[0])
	}
	if !tr.Notify(ctx, triggers.NotifyUnitDied, u) {
		t.Fatal("second kill should complete")
	}
	if !tr.Completed {
		t.Fatal("trigger should be completed")
	}
}

func TestCaptureUnitTypeOnlyOnTransfer(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	w := newSessionFixtureWorld(16, cat)
	def := &content.UnitDef{UnitName: "CORLAB", MaxDamage: 100}
	h, _ := w.Create(def, 1, 0, 0, 0)
	u := w.Unit(h)
	tr := triggers.New(triggers.KindCaptureUnitType, "CORLAB", 1)
	ctx := triggers.PollContext{Tick: 0, World: w, LocalOwner: 0, EnemyOwner: 1}
	if tr.Notify(ctx, triggers.NotifyUnitDied, u) {
		t.Fatal("capture should not advance on death")
	}
	if tr.Poll(ctx) {
		t.Fatal("capture should not advance on poll")
	}
	if !tr.Notify(ctx, triggers.NotifyUnitCaptured, u) {
		t.Fatal("capture should advance on capture notification")
	}
	if !tr.Completed {
		t.Fatal("capture trigger should be completed after transfer")
	}
}

func TestBuildUnitTypePoll(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	w := newSessionFixtureWorld(16, cat)
	def := &content.UnitDef{UnitName: "ARMSY", MaxDamage: 100}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	tr := triggers.New(triggers.KindBuildUnitType, "ARMSY", 1)
	ctx := triggers.PollContext{Tick: 0, World: w, LocalOwner: 0, EnemyOwner: 1}
	if tr.Poll(ctx) {
		t.Fatal("should not complete with nothing built")
	}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	u.Remaining = 1 // nanoframe not completed
	if tr.Poll(ctx) {
		t.Fatal("nanoframe should not count")
	}
	u.Remaining = 0
	if !tr.Poll(ctx) {
		t.Fatal("completed unit should satisfy build condition at next poll")
	}
}

func TestSimultaneousVictoryDefeatResolvesVictory(t *testing.T) {
	w := units.NewSliced(16, nil)
	ctx := triggers.PollContext{Tick: 0, World: w, LocalOwner: 0, EnemyOwner: 1}
	vic := []*triggers.Trigger{triggers.New(triggers.KindDestroyAllUnits, "")} // always true on poll
	// defeat AllUnitsKilled: need no local units, so make enemy still have units but local empty.
	// With no local units, AllUnitsKilled true.
	def := []*triggers.Trigger{triggers.New(triggers.KindAllUnitsKilled, "")}
	// Ensure defeat would be true: no local units in w
	vDone, dDone := triggers.Evaluate(vic, def, ctx)
	if !vDone || dDone {
		t.Fatalf("simultaneous should be victory: v=%v d=%v", vDone, dDone)
	}
}

func TestLatchTimingEconomyFreezeNotImmediate(t *testing.T) {
	// Latch should arm to 4 without immediate ending, and settlement should be frozen via Countdown>=0
	// but IsEnding false until 5 deadline ticks later (150 ticks).
	latch := NewEndLatch()
	if latch.IsEnding() {
		t.Fatal("initial latch should not be ending")
	}
	if latch.SettlementFrozen() {
		t.Fatal("initial settlement not frozen")
	}
	// Simulate poll at tick 0 due
	latch.AdvanceWin(true)
	if latch.Countdown != 4 {
		t.Fatalf("after arm Countdown %d want 4", latch.Countdown)
	}
	if latch.IsEnding() {
		t.Fatal("after arm IsEnding should be false (Bits not yet set)")
	}
	if !latch.SettlementFrozen() {
		t.Fatal("after arm settlement should be frozen via Countdown>=0")
	}
	if latch.IsWin() {
		t.Fatal("IsWin should be false until latch (pending win not yet Bits)")
	}
	// 4 more advances (ticks 30,60,90,120) should decrement 4->3->2->1->0 still not latch
	for i := 0; i < 4; i++ {
		latched := latch.AdvanceWin(true)
		if latched {
			t.Fatalf("should not latch at iteration %d", i)
		}
		if latch.IsEnding() {
			t.Fatal("should not be ending before final decrement")
		}
	}
	if latch.Countdown != 0 {
		t.Fatalf("after 4 decrements Countdown %d want 0", latch.Countdown)
	}
	// final decrement to -1 should latch
	if !latch.AdvanceWin(true) {
		t.Fatal("final decrement should latch")
	}
	if !latch.IsEnding() {
		t.Fatal("after final latch IsEnding should be true")
	}
	if !latch.IsWin() {
		t.Fatal("after final latch IsWin should be true")
	}
	if latch.Countdown != -1 {
		t.Fatalf("after latch Countdown %d want -1", latch.Countdown)
	}
}

func TestSaveLoadPreservesCountdown(t *testing.T) {
	latch := NewEndLatch()
	latch.AdvanceWin(true) // arm to 4
	// Simulate one decrement
	latch.AdvanceWin(true) // 4->3
	savedCountdown := latch.Countdown
	savedBits := latch.Bits
	savedPending := latch.Pending
	// Simulate save/load by copying struct
	copyLatch := latch
	// mutate original to ensure copy independent
	latch.AdvanceWin(true)
	if copyLatch.Countdown != savedCountdown || copyLatch.Bits != savedBits || copyLatch.Pending != savedPending {
		t.Fatalf("save copy mismatch")
	}
	// After load, continue countdown and ensure latch still works
	restored := copyLatch
	// Advance remaining 3 ticks to reach 0 then latch
	for i := 0; i < 3; i++ {
		restored.AdvanceWin(true)
	}
	if restored.Countdown != 0 {
		t.Fatalf("restored countdown %d want 0", restored.Countdown)
	}
	if !restored.AdvanceWin(true) {
		t.Fatal("restored final latch should succeed")
	}
	if !restored.IsEnding() || !restored.IsWin() {
		t.Fatal("restored latch should be win ending")
	}
}
