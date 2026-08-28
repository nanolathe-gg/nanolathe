package triggers

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Fixtures for the poll/notification split (REVIEW.md WU-R2-3).

func triggerWorld(t *testing.T) *units.World {
	t.Helper()
	return units.NewSliced(16, nil)
}

// spawn adds a live unit at integer world coordinates.
func spawn(t *testing.T, w *units.World, name string, owner uint8, x, z int32) *units.Unit {
	t.Helper()
	def := &content.UnitDef{}
	def.UnitName = name
	def.MaxDamage = 100
	def.CanMove = true
	h, err := w.Create(def, owner, numeric.FixedFromInt(int64(x)), 0, numeric.FixedFromInt(int64(z)))
	if err != nil {
		t.Fatalf("spawn %s: %v", name, err)
	}
	return w.Unit(h)
}

func pollCtx(w *units.World, tick uint32) PollContext {
	return PollContext{Tick: tick, World: w, LocalOwner: 0, EnemyOwner: 1}
}

// TestPollDoesNotAdvanceCountdowns is the F-8.1 regression guard. The countdown
// used to decrement on a plain tick poll, so "kill 5 of type X" completed in
// five ticks with nothing killed [08 "Evaluation"].
func TestPollDoesNotAdvanceCountdowns(t *testing.T) {
	w := triggerWorld(t)
	spawn(t, w, "CORLAB", 1, 0, 0)
	tr := New(KindKillUnitType, "CORLAB", 3)

	for i := 0; i < 100; i++ {
		if tr.Poll(pollCtx(w, uint32(i))) {
			t.Fatalf("countdown completed from polling alone at tick %d", i)
		}
	}
	if tr.Args[0] != 3 {
		t.Fatalf("poll decremented the countdown to %d; it advances from the "+
			"unit-died notification, not from the passage of time", tr.Args[0])
	}
}

// TestCountdownAdvancesOnNotification locks the notification slot [08 "Evaluation"] C17.
func TestCountdownAdvancesOnNotification(t *testing.T) {
	w := triggerWorld(t)
	victim := spawn(t, w, "CORLAB", 1, 0, 0)
	tr := New(KindKillUnitType, "CORLAB", 2)
	c := pollCtx(w, 0)

	if tr.Notify(c, NotifyUnitDied, victim) {
		t.Fatal("should not complete with one kill of two")
	}
	if tr.Args[0] != 1 {
		t.Fatalf("count %d want 1", tr.Args[0])
	}
	if !tr.Notify(c, NotifyUnitDied, victim) {
		t.Fatal("second kill should complete the countdown")
	}
	// A completed trigger stays completed and stops counting.
	before := tr.Args[0]
	tr.Notify(c, NotifyUnitDied, victim)
	if tr.Args[0] != before {
		t.Fatal("completed trigger kept counting")
	}
	// Wrong type does not count.
	other := spawn(t, w, "ARMCOM", 1, 0, 0)
	tr2 := New(KindKillUnitType, "CORLAB", 1)
	if tr2.Notify(c, NotifyUnitDied, other) {
		t.Fatal("non-matching type counted")
	}
	// A capture notification is not a kill.
	tr3 := New(KindKillUnitType, "CORLAB", 1)
	if tr3.Notify(c, NotifyUnitCaptured, victim) {
		t.Fatal("capture notification advanced a kill countdown")
	}
}

// TestKillUnitTypeOwnerGate locks the per-kind owner gating [08 "Evaluation"]:
// KillUnitType is a victory condition and counts only enemy losses, while
// UnitTypeKilled is a defeat condition and takes any owner.
func TestKillUnitTypeOwnerGate(t *testing.T) {
	w := triggerWorld(t)
	mine := spawn(t, w, "ARMLAB", 0, 0, 0)
	theirs := spawn(t, w, "ARMLAB", 1, 0, 0)
	c := pollCtx(w, 0)

	victory := New(KindKillUnitType, "ARMLAB", 1)
	if victory.Notify(c, NotifyUnitDied, mine) {
		t.Fatal("victory countdown counted the local player's own loss")
	}
	if !victory.Notify(c, NotifyUnitDied, theirs) {
		t.Fatal("victory countdown ignored an enemy loss")
	}

	defeat := New(KindUnitTypeKilled, "ARMLAB", 1)
	if !defeat.Notify(c, NotifyUnitDied, mine) {
		t.Fatal("defeat countdown should take any owner")
	}
}

// TestDefaultTriggersResolve is the F-8.2 regression guard. The injected
// defaults used to return false unconditionally, so no mission could be won or
// lost [08 "Default triggers"] C16.
func TestDefaultTriggersResolve(t *testing.T) {
	w := triggerWorld(t)
	vic, def := EnsureDefaults(nil, nil)

	// DestroyAllUnits is self-satisfied from the first poll: its counter has no
	// writer anywhere in the image [08 "Evaluation"].
	vDone, dDone := Evaluate(vic, def, pollCtx(w, 0))
	if !vDone {
		t.Fatal("the default victory condition never resolves")
	}
	_ = dDone

	// The default defeat condition resolves when the local player is wiped out.
	_, def2 := EnsureDefaults(nil, nil)
	spawn(t, w, "ARMCOM", 0, 0, 0)
	if _, d := Evaluate(nil, def2, pollCtx(w, 0)); d {
		t.Fatal("all-units-killed fired while the local player still has units")
	}
	for _, u := range w.Iter() {
		if u != nil {
			u.Alive = false
		}
	}
	if _, d := Evaluate(nil, def2, pollCtx(w, 0)); !d {
		t.Fatal("all-units-killed did not fire with no local units left")
	}
}

// TestBuildUnitTypeScansTheWorld locks the poll-time scan [08 "Evaluation"].
func TestBuildUnitTypeScansTheWorld(t *testing.T) {
	w := triggerWorld(t)
	tr := New(KindBuildUnitType, "ARMSY", 2)
	c := pollCtx(w, 0)

	if tr.Poll(c) {
		t.Fatal("completed with nothing built")
	}
	spawn(t, w, "ARMSY", 0, 0, 0)
	if tr.Poll(c) {
		t.Fatal("completed with one of two built")
	}
	// An enemy unit of the same type does not count toward the local build.
	spawn(t, w, "ARMSY", 1, 0, 0)
	if tr.Poll(c) {
		t.Fatal("an enemy unit satisfied a local build condition")
	}
	// A nanoframe is not a completed unit.
	frame := spawn(t, w, "ARMSY", 0, 0, 0)
	frame.Remaining = 1
	if tr.Poll(c) {
		t.Fatal("an unfinished nanoframe counted as built")
	}
	frame.Remaining = 0
	if !tr.Poll(c) {
		t.Fatal("two completed units should satisfy the condition")
	}
}

// TestCommanderConditionsGateOnOwner locks the split between the victory and
// defeat commander conditions [08 "Evaluation"].
func TestCommanderConditionsGateOnOwner(t *testing.T) {
	w := triggerWorld(t)
	mine := spawn(t, w, "ARMCOM", 0, 0, 0)
	mine.Def.Commander = true
	theirs := spawn(t, w, "CORCOM", 1, 0, 0)
	theirs.Def.Commander = true
	c := pollCtx(w, 0)

	kill := New(KindKillEnemyCommander, "")
	lose := New(KindCommanderKilled, "")
	if kill.Poll(c) || lose.Poll(c) {
		t.Fatal("commander conditions fired with both commanders alive")
	}
	theirs.Alive = false
	if !kill.Poll(c) {
		t.Fatal("KillEnemyCommander did not fire with the enemy commander dead")
	}
	if lose.Poll(c) {
		t.Fatal("CommanderKilled fired for the enemy's commander")
	}
	mine.Alive = false
	if !lose.Poll(c) {
		t.Fatal("CommanderKilled did not fire with the local commander dead")
	}
}

// TestBoundaryTolerance locks C17's ±2 window over the poll-time scan
// with the promoted retail contract: threshold stored after arithmetic >>4 and
// poll compares (worldPixel>>4) vs threshold with abs <3 [08 "Evaluation"].
func TestBoundaryTolerance(t *testing.T) {
	// Threshold 100 is already authored>>4 (authored 1600). Positions are
	// worldPixel values; 1600>>4=100, 1632>>4=102 etc. Tolerance is <3 on the
	// shifted values, i.e. ±2 world units after >>4 (32 worldPixel).
	cases := []struct {
		pos      int32 // worldPixel X
		thresh   int32 // already shifted (authored>>4)
		wantPass bool
	}{
		{1600, 100, true},  // diff 0
		{1632, 100, true},  // 1632>>4=102 diff 2
		{1568, 100, true},  // 98 diff 2
		{1648, 100, false}, // 103 diff 3
		{1552, 100, false}, // 97 diff 3
	}
	for _, tc := range cases {
		w := triggerWorld(t)
		spawn(t, w, "ARMFAV", 0, tc.pos, 0)
		tr := New(KindAnyUnitPassesX, "", tc.thresh)
		if got := tr.Poll(pollCtx(w, 0)); got != tc.wantPass {
			t.Fatalf("AnyUnitPassesX pos %d (>>4=%d) thresh %d: got %v want %v",
				tc.pos, tc.pos>>4, tc.thresh, got, tc.wantPass)
		}
	}
	// Also verify negative thresholds with arithmetic shift: -1600>>4 = -100.
	{
		w := triggerWorld(t)
		spawn(t, w, "ARMFAV", 0, -1600, 0) // -100
		tr := New(KindAnyUnitPassesX, "", -100)
		if !tr.Poll(pollCtx(w, 0)) {
			t.Fatal("negative boundary should pass at diff 0")
		}
	}
	{
		w := triggerWorld(t)
		spawn(t, w, "ARMFAV", 0, -1552, 0) // -97 diff 3 should fail
		tr2 := New(KindAnyUnitPassesX, "", -100)
		if tr2.Poll(pollCtx(w, 0)) {
			t.Fatal("negative boundary diff 3 should fail")
		}
	}
	// The type-gated variant respects the authored type and ANYTYPE.
	// WorldPixel 1000 -> 62 after >>4, so threshold 62 matches.
	w := triggerWorld(t)
	spawn(t, w, "ARMCOM", 0, 1000, 0) // 62
	if New(KindUnitTypePassesX, "CORCOM", 62).Poll(pollCtx(w, 0)) {
		t.Fatal("type mismatch crossed the boundary")
	}
	if !New(KindUnitTypePassesX, "ARMCOM", 62).Poll(pollCtx(w, 0)) {
		t.Fatal("matching type did not cross the boundary")
	}
	if !New(KindUnitTypePassesX, "ANYTYPE", 62).Poll(pollCtx(w, 0)) {
		t.Fatal("ANYTYPE did not cross the boundary")
	}
}

// TestTimerSecondsToTicks locks the seconds×30 deadline [08 "Evaluation"] C17.
func TestTimerSecondsToTicks(t *testing.T) {
	w := triggerWorld(t)
	tr := NewTimer(KindVictoryTimerRunsOut, 10)
	if tr.Args[0] != 300 {
		t.Fatalf("seconds 10 -> ticks %d want 300", tr.Args[0])
	}
	if tr.Poll(pollCtx(w, 299)) {
		t.Fatal("tick 299 should not fire a 300 deadline")
	}
	if !tr.Poll(pollCtx(w, 300)) {
		t.Fatal("tick 300 should fire")
	}
	// A completed trigger stays completed regardless of tick.
	if !tr.Poll(pollCtx(w, 0)) {
		t.Fatal("completed trigger should stay completed")
	}
	if got := NewTimer(KindDeathTimerRunsOut, 1200).Args[0]; got != 36000 {
		t.Fatalf("1200 sec -> %d want 36000", got)
	}
}

// TestMoveUnitToRadius locks the type-gated radius scan.
func TestMoveUnitToRadius(t *testing.T) {
	w := triggerWorld(t)
	spawn(t, w, "ARMCOM", 0, 105, 200)
	if !New(KindMoveUnitToRadius, "ARMCOM", 100, 200, 10).Poll(pollCtx(w, 0)) {
		t.Fatal("unit inside the radius should satisfy the condition")
	}
	if New(KindMoveUnitToRadius, "ARMCOM", 100, 200, 4).Poll(pollCtx(w, 0)) {
		t.Fatal("unit outside the radius satisfied the condition")
	}
	if New(KindMoveUnitToRadius, "CORCOM", 100, 200, 10).Poll(pollCtx(w, 0)) {
		t.Fatal("type mismatch satisfied the condition")
	}
}

// TestEvaluateCombination locks victory-AND, defeat-OR and victory-first
// [08 "Evaluation"].
func TestEvaluateCombination(t *testing.T) {
	w := triggerWorld(t)
	c := pollCtx(w, 0)

	vic := []*Trigger{New(KindAnyUnitPassesX, "", 100), New(KindAnyUnitPassesZ, "", 200)}
	def := []*Trigger{New(KindAnyUnitPassesX, "", 300)}

	// Victory is an AND: one incomplete member blocks it.
	vic[0].Completed = true
	if v, _ := Evaluate(vic, def, c); v {
		t.Fatal("victory fired with one member incomplete")
	}
	vic[1].Completed = true
	v, d := Evaluate(vic, def, c)
	if !v || d {
		t.Fatalf("all victory members complete: got victory=%v defeat=%v", v, d)
	}
	// Simultaneous resolves as a victory.
	def[0].Completed = true
	if v, d := Evaluate(vic, def, c); !v || d {
		t.Fatalf("simultaneous should be a victory: victory=%v defeat=%v", v, d)
	}
	// Defeat is an OR.
	vic[1].Completed = false
	if _, d := Evaluate(vic, def, c); !d {
		t.Fatal("defeat OR did not fire with one member complete")
	}
	// An empty victory queue never wins — the default is injected instead.
	if v, _ := Evaluate(nil, def, c); v {
		t.Fatal("an empty victory queue reported a win")
	}
}

// TestPollMutatesOnlyCompleted locks C17's purity for the scanning kinds.
func TestPollMutatesOnlyCompleted(t *testing.T) {
	w := triggerWorld(t)
	// 8032>>4=502, 8000>>4=500 diff 2 after >>4 — should cross.
	spawn(t, w, "ARMFAV", 0, 8032, 0)
	tr := New(KindAnyUnitPassesX, "", 500)
	args, typ := tr.Args, tr.Type
	if !tr.Poll(pollCtx(w, 0)) {
		t.Fatal("should cross at a difference of two after >>4")
	}
	if tr.Args != args || tr.Type != typ {
		t.Fatal("poll mutated fields other than Completed")
	}
}
