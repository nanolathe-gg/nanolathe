package headless

import "testing"

// TestArenaMapSeedIsStable locks the evaluation protocol's seed derivation
// (docs/MODERN_AI_RESEARCH.md §5): recorded games are reproducible only while
// these values hold. The map name is case- and space-insensitive, and one
// seed list yields a different battle seed on every map.
func TestArenaMapSeedIsStable(t *testing.T) {
	if got := ArenaMapSeed(201, "great divide"); got != 3082589987 {
		t.Fatalf("ArenaMapSeed(201, great divide) = %d, want 3082589987", got)
	}
	if got := ArenaMapSeed(0, ""); got != 117504730 {
		t.Fatalf("ArenaMapSeed(0, \"\") = %d, want 117504730", got)
	}
	if ArenaMapSeed(201, " Great Divide ") != ArenaMapSeed(201, "great divide") {
		t.Fatal("map name case or surrounding space changed the battle seed")
	}
	maps := []string{"great divide", "the pass", "dark side", "red planet", "full moon", "sherwood",
		"metal heck", "comet catcher", "evad river confluence", "ashap plateau", "painted desert", "coast to coast"}
	seen := map[uint32]string{}
	for _, seed := range []uint32{201, 202} {
		for _, m := range maps {
			s := ArenaMapSeed(seed, m)
			if prev, dup := seen[s]; dup {
				t.Fatalf("seed %d on %q repeats %s's battle seed %d", seed, m, prev, s)
			}
			seen[s] = m
		}
	}
	if (ArenaRequest{Seed: 7, Map: "the pass"}).battleSeed() != 7 {
		t.Fatal("a request without MapSeed must play its seed unchanged")
	}
	if (ArenaRequest{Seed: 7, Map: "the pass", MapSeed: true}).battleSeed() != ArenaMapSeed(7, "the pass") {
		t.Fatal("a MapSeed request must play the mixed seed")
	}
}

// TestStartSwapSeedFindsTheExchangingGate locks how StartsSwap chooses its CRT
// seed: the battle seed itself when its first CRT draw already opens the
// two-player gate [08 "Randomization for skirmish starts"], otherwise the
// first seed counting up that does. The placement itself is checked against
// the commanders on every swapped match.
func TestStartSwapSeedFindsTheExchangingGate(t *testing.T) {
	for _, c := range []struct{ seed, want uint32 }{{1263233247, 1263233247}, {1560336160, 1560338069}} {
		got, err := startSwapSeed(c.seed)
		if err != nil || got != c.want {
			t.Fatalf("startSwapSeed(%d) = %d, %v; want %d", c.seed, got, err, c.want)
		}
	}
}

// TestAdjudicatorNeedsAHeldLead locks the early-end rule the protocol's
// validation measured (docs/MODERN_AI_RESEARCH.md §5.3): the leader must hold
// the ratio at every sample of the whole window (both ends included), the
// window restarts when the lead is lost or changes hands, the ratio test is
// inclusive, a score below zero counts as zero, the leader's must be
// positive, and no game ends before From.
func TestAdjudicatorNeedsAHeldLead(t *testing.T) {
	rule := ArenaAdjudication{RatioPct: 200, Window: 300, From: 600}
	alive := []bool{true, true}
	type step struct {
		tick uint32
		a, b int64
		want int
	}
	run := func(name string, steps []step) {
		t.Helper()
		adj := &adjudicator{rule: rule, leader: -1}
		for _, s := range steps {
			if got := adj.observe(s.tick, []int64{s.a, s.b}, alive); got != s.want {
				t.Fatalf("%s: tick %d scores %d/%d: got %d, want %d", name, s.tick, s.a, s.b, got, s.want)
			}
		}
	}
	run("held from the start ends at From", []step{
		{150, 400, 200, -1}, {300, 400, 200, -1}, {450, 400, 200, -1}, {600, 400, 200, 0},
	})
	run("a lead must span the window", []step{
		{450, 100, 100, -1}, {600, 100, 400, -1}, {750, 100, 400, -1}, {900, 100, 400, 1},
	})
	run("losing the ratio restarts the window", []step{
		{600, 400, 200, -1}, {750, 399, 200, -1}, {900, 400, 200, -1}, {1050, 400, 200, -1}, {1200, 400, 200, 0},
	})
	run("a change of leader restarts the window", []step{
		{600, 400, 200, -1}, {750, 200, 400, -1}, {900, 200, 400, -1}, {1050, 200, 400, 1},
	})
	run("negative scores count as zero, the leader's must be positive", []step{
		{600, 10, -50, -1}, {750, 10, -50, -1}, {900, 10, -50, 0},
	})
	run("no positive leader, no lead", []step{
		{600, 0, -50, -1}, {750, 0, 0, -1}, {900, 0, 0, -1}, {1050, -3, -2, -1},
	})
	// A dead player is not a rival; three players compare the leader with
	// the best of the rest.
	adj := &adjudicator{rule: ArenaAdjudication{RatioPct: 150, Window: 0}, leader: -1}
	if got := adj.observe(0, []int64{300, 900, 100}, []bool{true, false, true}); got != 0 {
		t.Fatalf("leader among the living: got %d, want 0", got)
	}
	adj = &adjudicator{rule: ArenaAdjudication{RatioPct: 150, Window: 0}, leader: -1}
	if got := adj.observe(0, []int64{300, 250, 100}, []bool{true, true, true}); got != -1 {
		t.Fatalf("a runner-up within the ratio must hold the game open, got %d", got)
	}
}

// TestSampleScoreIsTheLimitScore locks the adjudicator's reading of a sample
// to the tick-limit score formulas (docs/MODERN_AI_RESEARCH.md §5).
func TestSampleScoreIsTheLimitScore(t *testing.T) {
	s := ArenaSample{ArmyValue: 1000, EcoValue: 500, ValueKilled: 300, ValueLost: 201, BuilderValue: 40, FrameValue: 7}
	if got := sampleScore(s, ScoreDefault); got != 1000+500+300-100 {
		t.Fatalf("default score %d", got)
	}
	if got := sampleScore(s, ScoreInvested); got != 1000+500+300-100+40+7 {
		t.Fatalf("invested score %d", got)
	}
}
