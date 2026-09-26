package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
)

// TestOBFConstants locks the sequential boundaries to the published
// O'Brien–Fleming constants for equally spaced looks (Jennison and Turnbull,
// "Group Sequential Methods", table 2.3): one look is the fixed-sample test,
// and more looks raise the last look's bar only slightly.
func TestOBFConstants(t *testing.T) {
	for _, c := range []struct {
		looks int
		alpha float64
		want  float64
	}{
		{1, 0.10, 1.645}, {1, 0.05, 1.960},
		{2, 0.10, 1.678}, {2, 0.05, 1.977},
		{4, 0.10, 1.733}, {4, 0.05, 2.024},
		{5, 0.05, 2.040},
	} {
		fracs := make([]float64, c.looks)
		for k := range fracs {
			fracs[k] = float64(k+1) / float64(c.looks)
		}
		if got := obfConstant(fracs, c.alpha); math.Abs(got-c.want) > 0.002 {
			t.Errorf("%d looks, alpha %.2f: c = %.4f, want %.3f", c.looks, c.alpha, got, c.want)
		}
	}
}

// TestStudentT checks the t distribution against tabulated quantiles.
func TestStudentT(t *testing.T) {
	for _, c := range []struct {
		p    float64
		df   int
		want float64
	}{
		{0.975, 5, 2.5706}, {0.95, 11, 1.7959}, {0.995, 3, 5.8409}, {0.5, 7, 0}, {0.05, 5, -2.0150},
	} {
		if got := tQuantile(c.p, c.df); math.Abs(got-c.want) > 1e-3 {
			t.Errorf("t quantile %.3f with %d df = %.4f, want %.4f", c.p, c.df, got, c.want)
		}
	}
	if math.Abs(normQuantile(0.95)-1.6449) > 1e-4 || math.Abs(normCDF(1.96)-0.975) > 1e-4 {
		t.Error("normal distribution off")
	}
}

// seqGames builds a pair's games: per map, per seed, both slot orders, with
// A's points from pts(map, seed, order).
func seqGames(maps, seeds int, pts func(m, s, o int) float64) []seqGame {
	var out []seqGame
	for m := range maps {
		for s := range seeds {
			for o := range 2 {
				out = append(out, seqGame{mapName: string(rune('a' + m)), seed: s, points: pts(m, s, o)})
			}
		}
	}
	return out
}

// TestSequentialLooks locks the look statistic and its decisions: the share
// is the mean of per-map means of blocks, its error the spread of the map
// means (maps - 1 degrees of freedom), and early looks need a far larger
// statistic than the last.
func TestSequentialLooks(t *testing.T) {
	p, err := newSeqPlan(&SequentialSpec{Looks: []int{2, 4, 6, 8}}, 8)
	if err != nil {
		t.Fatal(err)
	}
	// Block and map arithmetic: map means 0.75, 0.5, 0.25, 1.0 on four maps.
	want := []float64{0.75, 0.5, 0.25, 1.0}
	g := seqGames(4, 2, func(m, s, o int) float64 {
		if o == 0 {
			return want[m]*2 - 0.5*float64(s%2) // blocks average to want[m] over the two seeds
		}
		return 0.5 * float64(s%2)
	})
	l := p.look(0, g)
	sd := math.Sqrt(((0.625-0.75)*(0.625-0.75) + (0.625-0.5)*(0.625-0.5) + (0.625-0.25)*(0.625-0.25) + (0.625-1)*(0.625-1)) / 3)
	if l.Maps != 4 || l.Games != 16 || l.DF != 3 || math.Abs(l.Share-0.625) > 1e-12 || math.Abs(l.SE-sd/2) > 1e-12 {
		t.Fatalf("look %+v, want share 0.625, se %.4f on 4 maps, 16 games", l, sd/2)
	}
	// The first of four looks stops only at the O'Brien–Fleming nominal
	// level for t = 1/4, read on t(3).
	nominal := normCDF(p.c / math.Sqrt(0.25))
	if math.Abs(l.Bound-tQuantile(nominal, 3)) > 1e-9 || l.Bound < 10 {
		t.Fatalf("first-look bound %.3f, want the t(3) quantile of %.6f", l.Bound, nominal)
	}
	// A clean sweep on every map decides at the first look, and its record
	// (an unbounded statistic) is still valid JSON.
	l = p.look(0, seqGames(6, 8, func(m, s, o int) float64 { return 1 }))
	if _, err := json.Marshal(l); l.Decision != verdictBetter || err != nil {
		t.Fatalf("a sweep: %+v, %v", l, err)
	}
	if l := p.look(0, seqGames(6, 8, func(m, s, o int) float64 { return 0 })); l.Decision != verdictWorse {
		t.Fatalf("a whitewash: %+v", l)
	}
	// Slot order decides every game (slot 0 wins): no difference, and with
	// no trend at all the first look already stops for futility (the chance
	// of a verdict at the last look is 2(1 - Phi(c / sqrt(3/4)))).
	l = p.look(0, seqGames(6, 8, func(m, s, o int) float64 { return float64(1 - o) }))
	if cp := 2 * (1 - normCDF(p.c/math.Sqrt(0.75))); l.Decision != verdictNone || math.Abs(l.Power-cp) > 1e-9 {
		t.Fatalf("slot-decided games: %+v", l)
	}
	// Games past the look's seeds are not read.
	g = seqGames(6, 8, func(m, s, o int) float64 {
		if s >= 2 {
			return 1
		}
		return float64(1 - o)
	})
	if l := p.look(0, g); l.Games != 24 || l.Share != 0.5 {
		t.Fatalf("look 1 read later seeds: %+v", l)
	}
	// A modest, consistent edge (60% on every map but one) is not enough at
	// look 1 but decides at the last look.
	edge := func(m, s, o int) float64 {
		if m == 0 {
			return 0.5
		}
		if (s+o)%5 < 3 {
			return 1
		}
		return 0
	}
	if l := p.look(0, seqGames(6, 8, edge)); l.Decision == verdictBetter {
		t.Fatalf("a modest edge decided at look 1: %+v", l)
	}
	if l := p.look(3, seqGames(6, 8, edge)); l.Decision != verdictBetter {
		t.Fatalf("a consistent edge undecided at the last look: %+v", l)
	}
	for _, bad := range []SequentialSpec{{}, {Looks: []int{2, 2, 8}}, {Looks: []int{2, 4}}, {Looks: []int{8}, Alpha: 1.5}} {
		if _, err := newSeqPlan(&bad, 8); err == nil {
			t.Errorf("spec %+v accepted", bad)
		}
	}
}

// TestQueueOrderLongestFirst locks the scheduling order: looks first, then
// the longest expected games, then spec order; ids and arguments never change.
func TestQueueOrderLongestFirst(t *testing.T) {
	spec := TournamentSpec{Maps: []string{"sherwood", "plains and passes", "the pass", "no such map"}, Seeds: []uint{1, 2},
		Ticks: 72000, SwapSides: true, Pairs: [][]string{{"util+tac:hard", "retail"}}}
	queue, _ := mustQueue(t, spec)
	ids := map[string]string{}
	for _, j := range queue {
		ids[j.id] = j.args[2] + " " + j.args[4]
	}
	orderQueue(queue, true, false)
	if queue[0].mapName != "plains and passes" || queue[len(queue)-1].mapName != "sherwood" {
		t.Fatalf("order starts %q, ends %q", queue[0].mapName, queue[len(queue)-1].mapName)
	}
	for i := 1; i < len(queue); i++ {
		if queue[i].cost > queue[i-1].cost {
			t.Fatalf("job %d (%s) costs more than job %d (%s)", i, queue[i].mapName, i-1, queue[i-1].mapName)
		}
	}
	for _, j := range queue {
		if ids[j.id] != j.args[2]+" "+j.args[4] {
			t.Fatalf("%s changed arguments", j.id)
		}
	}
	// An unknown map takes the median; lengths scale the estimate.
	if estimateSeconds("no such map", 72000) <= estimateSeconds("sherwood", 72000) ||
		estimateSeconds("the pass", 36000) >= estimateSeconds("the pass", 72000) {
		t.Fatal("estimates out of order")
	}
	// Looks come before cost, and a sequential tournament plays pair by pair
	// within a look.
	spec.Pairs = append(spec.Pairs, []string{"util+tac:hard", "util+tac:medium"})
	queue, _ = mustQueue(t, spec)
	for i := range queue {
		queue[i].look = queue[i].seed
	}
	orderQueue(queue, true, true)
	for i := 1; i < len(queue); i++ {
		a, b := queue[i-1], queue[i]
		if b.seed < a.seed || (b.seed == a.seed && b.pair < a.pair) || (b.seed == a.seed && b.pair == a.pair && b.cost > a.cost) {
			t.Fatalf("job %d (seed %d, pair %d, %s) queued after job %d (seed %d, pair %d, %s)", i, b.seed, b.pair, b.mapName, i-1, a.seed, a.pair, a.mapName)
		}
	}
	if queue[0].aSlot != 0 && queue[1].aSlot != 0 {
		t.Fatal("no game plays the pair's first contestant in slot 0")
	}
}

// TestParseAdjudicate locks the -adjudicate rule's units: a ratio and two
// times in minutes.
func TestParseAdjudicate(t *testing.T) {
	got, err := parseAdjudicate("2,3,10")
	want := headless.ArenaAdjudication{RatioPct: 200, Window: 5400, From: 18000}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	if got, err := parseAdjudicate(""); err != nil || got.RatioPct != 0 {
		t.Fatalf("empty rule: %+v, %v", got, err)
	}
	for _, bad := range []string{"2,3", "1,3,10", "x,3,10", "2,-1,10"} {
		if _, err := parseAdjudicate(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestSlotPoolIsExclusive checks that two holders never share a slot and that
// releasing one frees it for the next.
func TestSlotPoolIsExclusive(t *testing.T) {
	pool, err := parseSlots(filepath.Join(t.TempDir(), "slots") + ":1")
	if err != nil {
		t.Fatal(err)
	}
	release, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := pool.acquire(ctx); err == nil {
		t.Fatal("a second holder took the only slot")
	}
	release()
	again, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again()
	if p, err := parseSlots("off"); p != nil || err != nil {
		t.Errorf("-slots off: pool %v, err %v", p, err)
	}
	for _, bad := range []string{"dir", "dir:0", ":2"} {
		if _, err := parseSlots(bad); err == nil {
			t.Errorf("-slots %q accepted", bad)
		}
	}
}

// TestProtocolV3Specs checks the protocol v3 templates
// (docs/MODERN_AI_RESEARCH.md §5.3) parse, validate as sequential designs and
// expand to the documented maximum game counts, with adjudication on exactly
// the 40-minute specs.
func TestProtocolV3Specs(t *testing.T) {
	want := map[string]int{"v3-gate-a": 192, "v3-gate-b": 96, "v3-ablation": 432}
	for name, games := range want {
		raw, err := os.ReadFile(filepath.Join("testdata", "protocol-v3", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var spec TournamentSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		queue, _ := mustQueue(t, spec)
		if spec.Protocol != "v3" || !spec.SwapSides || spec.Starts != "random" || len(queue) != games {
			t.Fatalf("%s: protocol %q, swap %v, starts %q, %d games; want v3, true, random, %d", name, spec.Protocol, spec.SwapSides, spec.Starts, len(queue), games)
		}
		if _, err := newSeqPlan(spec.Sequential, len(spec.Seeds)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if adj := slices.Contains(spec.Extra, "-adjudicate"); adj != (spec.Ticks == 72000) {
			t.Fatalf("%s: adjudication %v at %d ticks", name, adj, spec.Ticks)
		}
	}
}
