package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

// The slide arithmetic of [07 R-HUD-04 §1]: a quarter of the remaining
// distance with a one-pixel floor, 18 composed frames each way, and the two
// detent cues.
func TestScoreSlideConvergesInEighteenFramesEachWay(t *testing.T) {
	var s ScoreSlide
	visible, cue := s.Step(true)
	if !visible || cue != ScoreCuePanel {
		t.Fatalf("first showing step: visible=%v cue=%q, want true/%q", visible, cue, ScoreCuePanel)
	}
	if s != 31 {
		t.Fatalf("first showing step left s=%d, want 31 (125/4)", s)
	}
	frames := 1
	for s != ScorePanelWidth {
		_, cue = s.Step(true)
		frames++
		if frames > 64 {
			t.Fatal("showing slide did not converge")
		}
	}
	if frames != 18 {
		t.Errorf("closed to open took %d composed frames, want 18", frames)
	}
	if cue != ScoreCueOptions {
		t.Errorf("arriving at the open detent played %q, want %q", cue, ScoreCueOptions)
	}

	_, cue = s.Step(false)
	if cue != ScoreCuePanel {
		t.Errorf("leaving the open detent played %q, want %q", cue, ScoreCuePanel)
	}
	if s != 94 {
		t.Errorf("first hiding step left s=%d, want 94", s)
	}
	frames = 1
	for s != 0 {
		_, cue = s.Step(false)
		frames++
		if frames > 64 {
			t.Fatal("hiding slide did not converge")
		}
	}
	if frames != 18 {
		t.Errorf("open to closed took %d composed frames, want 18", frames)
	}
	if cue != ScoreCueOptions {
		t.Errorf("arriving at the closed detent played %q, want %q", cue, ScoreCueOptions)
	}
	if visible, cue := s.Step(false); visible || cue != "" {
		t.Errorf("a retracted hidden panel stepped to visible=%v cue=%q, want false/empty", visible, cue)
	}
}

// A showing panel already at the open detent draws without stepping or
// playing a cue [07 R-HUD-04 §1].
func TestScoreSlideAtOpenDetentDrawsWithoutCue(t *testing.T) {
	s := ScoreSlide(ScorePanelWidth)
	visible, cue := s.Step(true)
	if !visible || cue != "" || s != ScorePanelWidth {
		t.Fatalf("open detent step: visible=%v cue=%q s=%d, want true/empty/125", visible, cue, s)
	}
}

func TestScoreSessionKindGateAndShowPolarity(t *testing.T) {
	for kind, want := range map[uint8]bool{1: false, 2: true, 3: true, 0: false} {
		if got := ScoreSessionKindDraws(kind); got != want {
			t.Errorf("kind %d draws=%v, want %v", kind, got, want)
		}
	}
	// F4's interface bit shows the panel on its own; Space shows it unless a
	// text editor has the focus.
	cases := []struct {
		bit, space, editor, want bool
	}{
		{false, false, false, false},
		{false, true, false, true},
		{false, true, true, false},
		{true, false, true, true},
	}
	for _, c := range cases {
		if got := ScoreShowing(c.bit, c.space, c.editor); got != c.want {
			t.Errorf("ScoreShowing(%v,%v,%v)=%v, want %v", c.bit, c.space, c.editor, got, c.want)
		}
	}
}

func TestScorePanelGeometryHangsOffTheRightEdge(t *testing.T) {
	r := ScorePanelGeometry(640, 40, 3)
	if r.X0 != 600 || r.X1 != 725 || r.Y0 != 32 || r.Y1 != 166 {
		t.Fatalf("geometry = %+v, want x0=600 x1=725 y0=32 y1=166", r)
	}
	if ScoreRowTop(0) != 47 || ScoreRowTop(1) != 87 || ScoreRowTop(2) != 127 {
		t.Errorf("row tops = %d/%d/%d, want 47/87/127", ScoreRowTop(0), ScoreRowTop(1), ScoreRowTop(2))
	}
}

func TestScoreSlotRowFilter(t *testing.T) {
	base := ScoreSlot{Present: true, Controller: 1, Side: 0, LiveUnits: 4}
	if !base.Qualifies() {
		t.Fatal("a present, controller-1, live slot must qualify")
	}
	absent := base
	absent.Present = false
	if absent.Qualifies() {
		t.Error("an absent record must not qualify")
	}
	controller := base
	controller.Controller = 4
	if controller.Qualifies() {
		t.Error("controller byte outside 1..3 must not qualify")
	}
	side := base
	side.Side = ScoreSideExcluded
	if side.Qualifies() {
		t.Error("side byte 10 must not qualify")
	}
	watcher := base
	watcher.Watcher = true
	if watcher.Qualifies() {
		t.Error("the watcher bit must reject the row")
	}
	// "live-unit count != 0 OR the auxiliary word == 0": a dead slot still
	// qualifies while its auxiliary word is zero.
	dead := base
	dead.LiveUnits = 0
	if !dead.Qualifies() {
		t.Error("a dead slot with auxiliary 0 must still qualify")
	}
	dead.Auxiliary = 1
	if dead.Qualifies() {
		t.Error("a dead slot with a nonzero auxiliary word must not qualify")
	}
	alive := base
	alive.Auxiliary = 1
	if !alive.Qualifies() {
		t.Error("a live slot qualifies whatever the auxiliary word is")
	}
}

// Row order is rank order; the first qualifying slot in slot order wins the
// rank [07 R-HUD-04 §1].
func TestScoreRowOrderIsRankOrderNotSlotOrder(t *testing.T) {
	slots := make([]ScoreSlot, ScorePanelSlots)
	slots[0] = ScoreSlot{Present: true, Controller: 1, LiveUnits: 1, Rank: 2}
	slots[3] = ScoreSlot{Present: true, Controller: 2, LiveUnits: 1, Rank: 0}
	slots[7] = ScoreSlot{Present: true, Controller: 2, LiveUnits: 1, Rank: 1}
	order, _ := ScoreRowOrder(slots, 3)
	want := []int{3, 7, 0}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// A vacated rank collapses in the same frame and every qualifying slot still
// draws a row [07 R-HUD-04 §1].
func TestScoreRowOrderCompactsAVacatedRankInTheSameFrame(t *testing.T) {
	slots := make([]ScoreSlot, ScorePanelSlots)
	slots[1] = ScoreSlot{Present: true, Controller: 1, LiveUnits: 1, Rank: 2}
	slots[2] = ScoreSlot{Present: true, Controller: 2, LiveUnits: 1, Rank: 3}
	order, compacted := ScoreRowOrder(slots, 2)
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("order = %v, want [1 2]: every qualifying slot draws a row", order)
	}
	if compacted[1].Rank != 0 || compacted[2].Rank != 1 {
		t.Errorf("compacted ranks = %d/%d, want 0/1", compacted[1].Rank, compacted[2].Rank)
	}
}

func TestScoreRowOrderIgnoresRanksHeldByUnqualifiedSlots(t *testing.T) {
	slots := make([]ScoreSlot, ScorePanelSlots)
	// A watcher holds rank 0 but never draws and is never compacted.
	slots[0] = ScoreSlot{Present: true, Controller: 1, LiveUnits: 1, Rank: 0, Watcher: true}
	slots[4] = ScoreSlot{Present: true, Controller: 1, LiveUnits: 1, Rank: 1}
	order, compacted := ScoreRowOrder(slots, 1)
	if len(order) != 1 || order[0] != 4 {
		t.Fatalf("order = %v, want [4]", order)
	}
	if compacted[0].Rank != 0 {
		t.Errorf("an unqualified slot's rank byte changed to %d, want 0", compacted[0].Rank)
	}
}

func TestScoreFlashArmsAtThirtyAndDecaysByTwo(t *testing.T) {
	var f ScoreFlash
	f.Credit(2, 5)
	if f.Kills[2] != 30 || f.Losses[5] != 30 {
		t.Fatalf("credit wrote kills[2]=%d losses[5]=%d, want 30/30", f.Kills[2], f.Losses[5])
	}
	steps := 0
	for f.Kills[2] != 0 {
		f.Decay()
		steps++
		if steps > 64 {
			t.Fatal("flash did not decay to zero")
		}
	}
	// 30 down by 2 per unit of the scaled timer is 15 units, half a second at
	// 30 units per second [07 R-HUD-04 §1][07 R-CAM-01 §10].
	if steps != 15 {
		t.Errorf("flash took %d timer units to fade, want 15", steps)
	}
	if f.Losses[5] != 0 {
		t.Errorf("loss flash = %d after the same decay, want 0", f.Losses[5])
	}
	f.Credit(0, 0)
	f.Reset()
	if f.Kills[0] != 0 || f.Losses[0] != 0 {
		t.Error("battle entry must zero both arrays")
	}
}

func TestScoreCountersSelectCommanderPairInDeathmatch(t *testing.T) {
	row := frame.PlayerRow{Kills: 7, Losses: 3, CommandersKilled: 2, CommandersLost: 1}
	if k, l := ScoreCounters(row, 0); k != 7 || l != 3 {
		t.Errorf("ordinary counters = %d/%d, want 7/3", k, l)
	}
	if k, l := ScoreCounters(row, ScoreDeathmatchOption); k != 2 || l != 1 {
		t.Errorf("deathmatch counters = %d/%d, want 2/1", k, l)
	}
}
