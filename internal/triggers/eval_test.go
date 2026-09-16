package triggers

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Fixtures for the poll/notification split (REVIEW.md WU-R2-3).

func triggerWorld(t *testing.T) *units.World {
	t.Helper()
	return newTriggersFixtureWorld(16, nil)
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
	return PollContext{
		Tick: tick, World: w, MissionArmed: true,
		StampedCell: func(u *units.Unit) (int16, int16, bool) {
			return int16(u.X.Int() >> 4), int16(u.Z.Int() >> 4), true
		},
		Deproject:   func(x, z int32) (int32, int32, int32) { return x << 16, 0, z << 16 },
		IsCommander: func(u *units.Unit) bool { return u != nil && u.Def != nil && u.Def.Commander },
	}
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
	celebrations := 0
	c := pollCtx(w, 0)
	c.Celebrate = func() { celebrations++ }

	// DestroyAllUnits reads slot 1's live-unit counter [08 R-TRIG-01 §4].
	vDone, dDone := EvaluateOwned(&vic, &def, c)
	if !vDone {
		t.Fatal("the default victory condition never resolves")
	}
	_ = dDone
	if vDone, _ = EvaluateOwned(&vic, &def, c); !vDone || celebrations != 1 || !vic[0].Celebrated {
		t.Fatalf("persistent default cue state = trigger=%+v cues=%d", vic[0], celebrations)
	}

	// The default defeat condition resolves when the local player is wiped out.
	_, def2 := EnsureDefaults(nil, nil)
	spawn(t, w, "ARMCOM", 0, 0, 0)
	blockingVictory := []*Trigger{New(KindVictoryTimerRunsOut, "", SecondsToTicks(100))}
	if _, d := EvaluateOwned(&blockingVictory, &def2, pollCtx(w, 0)); d {
		t.Fatal("all-units-killed fired while the local player still has units")
	}
	for _, u := range w.Iter() {
		if u != nil {
			u.Alive = false
		}
	}
	if _, d := EvaluateOwned(&blockingVictory, &def2, pollCtx(w, 0)); !d {
		t.Fatal("all-units-killed did not fire with no local units left")
	}
}

func TestEvaluateOwnedInstallsPersistentDefaults(t *testing.T) {
	victory, defeat := []*Trigger(nil), []*Trigger(nil)
	c := pollCtx(triggerWorld(t), 0)
	celebrations := 0
	c.Celebrate = func() { celebrations++ }
	if v, d := EvaluateOwned(&victory, &defeat, c); !v || d {
		t.Fatalf("owned defaults returned victory=%v defeat=%v", v, d)
	}
	if len(victory) != 1 || len(defeat) != 1 || !victory[0].Celebrated || celebrations != 1 {
		t.Fatalf("owned defaults were not retained: victory=%+v defeat=%+v cues=%d", victory, defeat, celebrations)
	}
	if v, _ := EvaluateOwned(&victory, &defeat, c); !v || celebrations != 1 {
		t.Fatalf("persistent default repeated cue: victory=%+v cues=%d", victory, celebrations)
	}
}

// TestBuildUnitTypeScansTheWorld locks the poll-time scan [08 "Evaluation"].
func TestBuildUnitTypeScansTheWorld(t *testing.T) {
	w := triggerWorld(t)
	tr := New(KindBuildUnitType, "ARMSY")
	c := pollCtx(w, 0)

	if tr.Poll(c) {
		t.Fatal("completed with nothing built")
	}
	spawn(t, w, "ARMSY", 0, 0, 0)
	if !tr.Poll(c) {
		t.Fatal("the first finished local unit did not satisfy the name-only condition")
	}
	// An enemy unit of the same type does not count toward a fresh local condition.
	spawn(t, w, "ARMSY", 1, 0, 0)
	tr = New(KindBuildUnitType, "OTHER")
	if tr.Poll(c) {
		t.Fatal("an enemy unit satisfied a local build condition")
	}
	// A nanoframe is not a completed unit.
	frame := spawn(t, w, "OTHER", 0, 0, 0)
	frame.Remaining = 1
	if tr.Poll(c) {
		t.Fatal("an unfinished nanoframe counted as built")
	}
	frame.Remaining = 0
	if !tr.Poll(c) {
		t.Fatal("a finished local unit should satisfy the condition")
	}
}

// TestCommanderConditionsGateOnOwner locks the notification and literal owner
// predicates [08 R-TRIG-01 §3, §4].
func TestCommanderConditionsGateOnOwner(t *testing.T) {
	w := triggerWorld(t)
	mine := spawn(t, w, "ARMCOM", 0, 0, 0)
	mine.Def.Commander = true
	theirs := spawn(t, w, "CORCOM", 1, 0, 0)
	theirs.Def.Commander = true
	c := pollCtx(w, 0)

	kill := New(KindKillEnemyCommander, "")
	lose := New(KindCommanderKilled, "")
	if kill.Notify(c, NotifyUnitDied, mine) {
		t.Fatal("enemy-commander victory counted the local commander")
	}
	if !kill.Notify(c, NotifyUnitDied, theirs) {
		t.Fatal("KillEnemyCommander ignored the enemy commander removal")
	}
	if lose.Notify(c, NotifyUnitDied, theirs) {
		t.Fatal("CommanderKilled fired for the enemy's commander")
	}
	if !lose.Notify(c, NotifyUnitDied, mine) {
		t.Fatal("CommanderKilled ignored the local commander removal")
	}
}

// TestBoundaryTolerance locks the strict ±2-cell window over the committed
// footprint anchor [08 R-TRIG-01 §3].
func TestBoundaryTolerance(t *testing.T) {
	// Threshold 100 is already authored>>4 (authored 1600). Positions are
	// worldPixel values; 1600>>4=100, 1632>>4=102 etc. Tolerance is <3 on the
	// stamped-cell values, i.e. ±2 cells after >>4 (32 world pixels).
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
		spawn(t, w, "ARMFAV", 1, tc.pos, 0)
		tr := New(KindAnyUnitPassesX, "", tc.thresh)
		if got := tr.Poll(pollCtx(w, 0)); got != tc.wantPass {
			t.Fatalf("AnyUnitPassesX pos %d (>>4=%d) thresh %d: got %v want %v",
				tc.pos, tc.pos>>4, tc.thresh, got, tc.wantPass)
		}
	}
	// Also verify negative thresholds with arithmetic shift: -1600>>4 = -100.
	{
		w := triggerWorld(t)
		spawn(t, w, "ARMFAV", 1, -1600, 0) // -100
		tr := New(KindAnyUnitPassesX, "", -100)
		if !tr.Poll(pollCtx(w, 0)) {
			t.Fatal("negative boundary should pass at diff 0")
		}
	}
	{
		w := triggerWorld(t)
		spawn(t, w, "ARMFAV", 1, -1552, 0) // -97 diff 3 should fail
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

func TestMoveRadiusSignedCoordinateSubtraction(t *testing.T) {
	const maxInt32 = int32(1<<31 - 1)
	const minInt32 = int32(-1 << 31)
	if got := wrappedDelta(int64(maxInt32), minInt32); got != -1 {
		t.Fatalf("max-min wrapped delta = %d want -1", got)
	}
	if got := wrappedDelta(int64(minInt32), maxInt32); got != 1 {
		t.Fatalf("min-max wrapped delta = %d want 1", got)
	}
	if got := wrappedDelta(int64(minInt32), 0); got*got != int64(1)<<62 {
		t.Fatalf("widest signed delta square = %d want %d", got*got, int64(1)<<62)
	}

	w := triggerWorld(t)
	u := spawn(t, w, "ARMCOM", 0, 0, 0)
	u.X = numeric.Fixed(maxInt32)
	c := pollCtx(w, 0)
	c.Deproject = func(_, _ int32) (int32, int32, int32) {
		return minInt32 + 65535, 0, 0
	}
	if New(KindMoveUnitToRadius, "ARMCOM", 0, 0, 0).Poll(c) {
		t.Fatal("wrapped one-pixel delta satisfied a zero-radius condition")
	}
}

// TestTimerSecondsToTicks locks the seconds×30 deadline [08 "Evaluation"] C17.
func TestTimerSecondsToTicks(t *testing.T) {
	w := triggerWorld(t)
	tr := New(KindVictoryTimerRunsOut, "", SecondsToTicks(10))
	if tr.Args[0] != 300 {
		t.Fatalf("seconds 10 -> ticks %d want 300", tr.Args[0])
	}
	if tr.Poll(pollCtx(w, 299)) {
		t.Fatal("tick 299 should not fire a 300 deadline")
	}
	if !tr.Poll(pollCtx(w, 300)) {
		t.Fatal("tick 300 should fire")
	}
	// Timer predicates never store Satisfied; an earlier tick is false again.
	if tr.Poll(pollCtx(w, 0)) {
		t.Fatal("timer predicate latched instead of being recomputed")
	}
	if got := New(KindDeathTimerRunsOut, "", SecondsToTicks(1200)).Args[0]; got != 36000 {
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
	if v, _ := EvaluateOwned(&vic, &def, c); v {
		t.Fatal("victory fired with one member incomplete")
	}
	vic[1].Completed = true
	v, d := EvaluateOwned(&vic, &def, c)
	if !v || d {
		t.Fatalf("all victory members complete: got victory=%v defeat=%v", v, d)
	}
	// Simultaneous resolves as a victory.
	def[0].Completed = true
	if v, d := EvaluateOwned(&vic, &def, c); !v || d {
		t.Fatalf("simultaneous should be a victory: victory=%v defeat=%v", v, d)
	}
	// Defeat is an OR.
	vic[1].Completed = false
	if _, d := EvaluateOwned(&vic, &def, c); !d {
		t.Fatal("defeat OR did not fire with one member complete")
	}
	// An empty queue gets a default record rather than retaining the empty-AND
	// identity [08 "Default triggers"].
	spawn(t, w, "enemy", 1, 0, 0)
	var emptyVictory []*Trigger
	blockingDefeat := []*Trigger{New(KindDeathTimerRunsOut, "", SecondsToTicks(1))}
	if v, d := EvaluateOwned(&emptyVictory, &blockingDefeat, c); v || d {
		t.Fatalf("default polling returned victory=%v defeat=%v with a live enemy", v, d)
	}
}

func TestEvaluateShortCircuitAndMissionArmed(t *testing.T) {
	w := triggerWorld(t)
	c := pollCtx(w, 0)
	late := New(KindVictoryTimerRunsOut, "", SecondsToTicks(10))
	notPolledVictory := New(KindAllUnitsKilled, "")
	firstDefeat := New(KindAllUnitsKilled, "")
	notPolledDefeat := New(KindAllUnitsKilled, "")

	shortVictory := []*Trigger{late, notPolledVictory}
	shortDefeat := []*Trigger{firstDefeat, notPolledDefeat}
	if v, d := EvaluateOwned(&shortVictory, &shortDefeat, c); v || !d {
		t.Fatalf("want false victory and first-true defeat, got v=%v d=%v", v, d)
	}
	if notPolledVictory.Completed || notPolledDefeat.Completed {
		t.Fatalf("queue short-circuit polled later records: victory=%v defeat=%v", notPolledVictory.Completed, notPolledDefeat.Completed)
	}

	celebrations := 0
	c.Celebrate = func() { celebrations++ }
	c.MissionArmed = false
	pure := New(KindDestroyAllUnits, "")
	unarmedVictory := []*Trigger{pure}
	var unarmedDefeat []*Trigger
	if v, d := EvaluateOwned(&unarmedVictory, &unarmedDefeat, c); v || d || celebrations != 0 || pure.Celebrated {
		t.Fatalf("unarmed mission evaluated queues: v=%v d=%v cues=%d trigger=%+v", v, d, celebrations, pure)
	}
}

func TestVictoryCelebrationIsOncePerRecord(t *testing.T) {
	c := pollCtx(triggerWorld(t), 0)
	celebrations := 0
	c.Celebrate = func() { celebrations++ }
	pure := New(KindDestroyAllUnits, "")
	// Two SEPARATE polls, not one predicate written twice: the record is
	// polled again after it has already celebrated, and must not celebrate a
	// second time.
	firstPoll := pure.Poll(c)
	secondPoll := pure.Poll(c)
	if !firstPoll || !secondPoll || pure.Completed || !pure.Celebrated || celebrations != 1 {
		t.Fatalf("pure victory predicate cue state got trigger=%+v cues=%d", pure, celebrations)
	}
}

func TestPureAnnihilationPredicatesCanBecomeFalseAgain(t *testing.T) {
	w := triggerWorld(t)
	c := pollCtx(w, 0)
	celebrations := 0
	c.Celebrate = func() { celebrations++ }
	destroy := New(KindDestroyAllUnits, "")
	if !destroy.Poll(c) || celebrations != 1 || destroy.Completed {
		t.Fatalf("initial destroy predicate = %+v cues=%d", destroy, celebrations)
	}
	spawn(t, w, "CORCOM", 1, 0, 0)
	if destroy.Poll(c) || celebrations != 1 || destroy.Completed || !destroy.Celebrated {
		t.Fatalf("reinforcement did not clear pure destroy predicate: %+v cues=%d", destroy, celebrations)
	}

	w2 := triggerWorld(t)
	allKilled := New(KindAllUnitsKilled, "")
	if !allKilled.Poll(pollCtx(w2, 0)) || allKilled.Celebrated {
		t.Fatal("empty slot-0 slice should satisfy AllUnitsKilled")
	}
	spawn(t, w2, "ARMCOM", 0, 0, 0)
	if allKilled.Poll(pollCtx(w2, 0)) || allKilled.Completed || allKilled.Celebrated {
		t.Fatalf("new eligible local unit did not clear AllUnitsKilled: %+v", allKilled)
	}
}

func TestNotificationSubjectIncludedCounts(t *testing.T) {
	t.Run("mobile", func(t *testing.T) {
		w := triggerWorld(t)
		subject := spawn(t, w, "CORAK", 1, 0, 0)
		subject.Def.BMCode = 1
		tr := New(KindKillAllMobileUnits, "")
		if !tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) {
			t.Fatal("sole occupied mobile subject should complete")
		}

		w = triggerWorld(t)
		subject = spawn(t, w, "CORAK", 1, 0, 0)
		subject.Def.BMCode = 1
		other := spawn(t, w, "CORFAV", 1, 0, 0)
		other.Def.BMCode = 1
		tr = New(KindKillAllMobileUnits, "")
		if tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) {
			t.Fatal("second occupied mobile enemy was not counted")
		}
	})

	t.Run("all-of-type", func(t *testing.T) {
		w := triggerWorld(t)
		subject := spawn(t, w, "CORAK", 1, 0, 0)
		tr := New(KindKillAllOfType, "CORAK")
		if !tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) {
			t.Fatal("sole occupied typed subject should complete")
		}

		w = triggerWorld(t)
		subject = spawn(t, w, "CORAK", 1, 0, 0)
		spawn(t, w, "CORAK", 1, 0, 0)
		tr = New(KindKillAllOfType, "CORAK")
		if tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) {
			t.Fatal("second occupied typed enemy was not counted")
		}
	})

	t.Run("both-primary-slots", func(t *testing.T) {
		w := triggerWorld(t)
		subject := spawn(t, w, "ARMFAV", 0, 0, 0)
		other := spawn(t, w, "ARMFAV", 1, 0, 0)
		tr := New(KindAllUnitsKilledOfType, "ARMFAV")
		if tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) {
			t.Fatal("matching records across slots 0 and 1 did not carry the count")
		}
		other.Alive = false
		if !tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) {
			t.Fatal("sole occupied subject across primary slots should complete")
		}
	})

	t.Run("defeat-count-continues", func(t *testing.T) {
		w := triggerWorld(t)
		subject := spawn(t, w, "ARMFAV", 0, 0, 0)
		tr := New(KindUnitTypeKilled, "ARMFAV", 1)
		if !tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) || tr.Args[0] != 0 {
			t.Fatalf("first notification = %+v", tr)
		}
		if !tr.Notify(pollCtx(w, 0), NotifyUnitDied, subject) || tr.Args[0] != -1 {
			t.Fatalf("completed defeat countdown did not continue below zero: %+v", tr)
		}
	})
}

// TestPollMutatesOnlyCompleted locks C17's purity for the scanning kinds.
func TestPollMutatesOnlyCompleted(t *testing.T) {
	w := triggerWorld(t)
	// 8032>>4=502, 8000>>4=500 diff 2 after >>4 — should cross.
	spawn(t, w, "ARMFAV", 1, 8032, 0)
	tr := New(KindAnyUnitPassesX, "", 500)
	args, typ := tr.Args, tr.Type
	if !tr.Poll(pollCtx(w, 0)) {
		t.Fatal("should cross at a difference of two after >>4")
	}
	if tr.Args != args || tr.Type != typ {
		t.Fatal("poll mutated fields other than Completed")
	}
}
