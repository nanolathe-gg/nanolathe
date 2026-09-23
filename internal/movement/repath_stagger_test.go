package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Strict 3.1 and Community keep retail's inclusive 60-tick throttle for every
// unit and admission tick, and an unbound System answers as Strict
// [04 R-MOV-01 §7].
func TestStrictRepathDelayIsRetailsSixty(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		for slot := 0; slot < 600; slot += 7 {
			for _, last := range []uint32{0, 1, 59, 60, 61, 1234, 0xffff_ff00} {
				if got := rules.RepathDelay(nil, slot, last); got != 60 {
					t.Fatalf("%T delay(slot %d, last %d) = %d, want 60", rules, slot, last, got)
				}
			}
		}
	}
	var s System
	route := &Route{WantsRepath: true, LastRequestTick: 300}
	if s.repathDue(route, 5, 359) || !s.repathDue(route, 5, 360) {
		t.Fatal("unbound System: want refused at last+59 and admitted at last+60")
	}
	route.WantsRepath = false
	if s.repathDue(route, 5, 1000) {
		t.Fatal("an unarmed follower is never due")
	}
}

// Modern's delay is 60..67, a pure function of slot and admission tick, and
// every one of the eight values occurs (DESIGN_MOVEMENT_PATH "Modern re-route
// staggering").
func TestModernRepathDelayBoundsAndDeterminism(t *testing.T) {
	m := &ModernRules{}
	var seen [modernRepathSpread]int
	for slot := 1; slot < 512; slot++ {
		d := m.RepathDelay(nil, slot, 60)
		if d < 60 || d >= 60+modernRepathSpread {
			t.Fatalf("slot %d delay %d outside [60, %d)", slot, d, 60+modernRepathSpread)
		}
		if again := (&ModernRules{}).RepathDelay(nil, slot, 60); again != d {
			t.Fatalf("slot %d delay %d then %d: not deterministic", slot, d, again)
		}
		seen[d-60]++
	}
	for off, n := range seen {
		// 511 slots over eight values: each should hold roughly 64.
		if n < 32 || n > 96 {
			t.Fatalf("offset %d taken by %d of 511 slots, want a roughly even share: %v", off, n, seen)
		}
	}
}

// A cohort admitted on one tick that keeps re-arming stays in step for ever
// under Strict. Modern splits it at the first re-admission and keeps
// separating units that share an admission tick, so the largest same-tick
// burst keeps falling instead of settling at one eighth of the cohort. This is
// the property the per-admission mix was chosen for over a fixed per-slot
// phase.
func TestModernRepathStaggerSeparatesACohort(t *testing.T) {
	const units = 240
	burst := func(rules Rules, cycles int) int {
		last := make([]uint32, units)
		for i := range last {
			last[i] = 60
		}
		worst := 0
		for c := 0; c < cycles; c++ {
			counts := make([]int, 60+(cycles+1)*(60+modernRepathSpread))
			worst = 0
			for i := range last {
				last[i] += rules.RepathDelay(nil, i+1, last[i])
				counts[last[i]]++
				worst = max(worst, counts[last[i]])
			}
		}
		return worst
	}
	if got := burst(StrictRules{}, 10); got != units {
		t.Fatalf("Strict cohort burst after 10 cycles = %d, want the whole cohort %d", got, units)
	}
	first, later := burst(&ModernRules{}, 1), burst(&ModernRules{}, 10)
	t.Logf("Modern largest same-tick burst of %d: %d after one re-admission, %d after ten", units, first, later)
	if first > units/4 {
		t.Fatalf("Modern first re-admission burst = %d of %d, want the cohort split", first, units)
	}
	if later >= first || later > units/12 {
		t.Fatalf("Modern burst after 10 cycles = %d (first %d), want it still falling below %d", later, first, units/12)
	}
}

// The scheduler poll uses the same due test as staging: under Modern a staged
// follower is refused one tick before its delay and admitted on it.
func TestModernPollHonoursTheRepathDelay(t *testing.T) {
	s := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	s.Rules = &ModernRules{}
	w := newMovementFixtureWorld(4)
	s.BindWorld(w)
	start, _, ok := w.SliceForPlayer(0)
	if !ok {
		t.Fatal("fixture: no player-0 slice")
	}
	h, err := w.CreateWithForcedSlot(wiringDef(), 0, 0, 0, 0, pool.Handle(start))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	s.EnsureUnit(w.Unit(h))
	s.SubmitMove(h, 0, path.Cell{X: 1}, path.Cell{X: 7})
	route := handleRow(s.Routes, h)
	delay := s.rules().RepathDelay(s, int(h), route.LastRequestTick)
	p := s.pathProvider
	// Four physical slots: eight polls visit the mover twice at each tick.
	p.SetPathTick(delay - 1)
	for range 8 {
		if _, result := p.Poll(0); result == path.PollRequest {
			t.Fatalf("admitted at tick %d, one before the Modern delay", delay-1)
		}
	}
	p.SetPathTick(delay)
	for range 8 {
		if got, result := p.Poll(0); result == path.PollRequest {
			if got.Unit != h || route.LastRequestTick != delay {
				t.Fatalf("admitted %d stamped %d, want %d stamped %d", got.Unit, route.LastRequestTick, h, delay)
			}
			return
		}
	}
	t.Fatalf("not admitted at the Modern delay tick %d", delay)
}

func TestRepathDueDoesNotAllocate(t *testing.T) {
	route := &Route{WantsRepath: true, LastRequestTick: 60}
	for _, rules := range []Rules{nil, &ModernRules{}} {
		s := &System{Rules: rules}
		if allocs := testing.AllocsPerRun(200, func() {
			learnedRulesSink = s.repathDue(route, 9, 125)
		}); allocs != 0 {
			t.Fatalf("rules %T allocated %v per due test", rules, allocs)
		}
	}
}
