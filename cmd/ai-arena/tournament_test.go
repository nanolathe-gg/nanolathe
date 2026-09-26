package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestBuildQueuePlaysEachMirrorGameOnce locks the mirror dedupe: with
// swap_sides, a pair of identical specs resolves to the same player
// arguments in both slot orders, which is the same deterministic game, so
// only one order is queued. A pair that differs keeps both orders.
func TestBuildQueuePlaysEachMirrorGameOnce(t *testing.T) {
	spec := TournamentSpec{
		Maps: []string{"the pass", "sherwood"}, Seeds: []uint{1, 2}, Ticks: 30, SwapSides: true,
		Pairs: [][]string{{"util+tac:hard", "util+tac:hard"}, {"util+tac:hard", "retail"}},
	}
	queue, replays := mustQueue(t, spec)
	mirror, mixed := 0, 0
	for _, j := range queue {
		if j.players[0] == j.players[1] {
			mirror++
		} else {
			mixed++
		}
	}
	if mirror != 4 || mixed != 8 || replays != 4 {
		t.Fatalf("mirror %d mixed %d replays %d, want 4 8 4", mirror, mixed, replays)
	}
	// A mirror that names the same side for both is still one game.
	spec.Pairs = [][]string{{"util+tac:hard:0", "util+tac:hard:0"}}
	if queue, replays = mustQueue(t, spec); len(queue) != 4 || replays != 4 {
		t.Fatalf("same-side mirror: %d jobs, %d replays; want 4, 4", len(queue), replays)
	}
	// Named sides that differ put a different faction in each slot, so both
	// orders are distinct games.
	spec.Pairs = [][]string{{"util+tac:hard:0", "util+tac:hard:1"}}
	if queue, replays = mustQueue(t, spec); len(queue) != 8 || replays != 0 {
		t.Fatalf("side-named pair: %d jobs, %d replays; want 8, 0", len(queue), replays)
	}
	if !slices.Contains(queue[0].args, "util+tac:hard:0") || slices.Contains(queue[0].args, "-raw-seed") {
		t.Fatalf("unexpected args %v", queue[0].args)
	}
}

// TestBuildQueueStarts locks the start modes: "both" plays every order at slot
// and swapped starts, and a mirror still plays each distinct game once.
func TestBuildQueueStarts(t *testing.T) {
	spec := TournamentSpec{
		Maps: []string{"the pass"}, Seeds: []uint{1}, Ticks: 30, SwapSides: true, Starts: "both",
		Pairs: [][]string{{"util+tac:hard", "retail"}, {"util+tac:hard", "util+tac:hard"}},
	}
	queue, replays := mustQueue(t, spec)
	swaps := 0
	for _, j := range queue {
		if slices.Contains(j.args, "swap") {
			swaps++
		}
	}
	if len(queue) != 6 || swaps != 3 || replays != 2 {
		t.Fatalf("%d jobs (%d swapped), %d replays; want 6 (3), 2", len(queue), swaps, replays)
	}
	spec.Starts = "sideways"
	if _, _, err := buildQueue(spec, "out"); err == nil {
		t.Fatal("an unknown starts mode must be rejected")
	}
}

func mustQueue(t *testing.T, spec TournamentSpec) ([]matchJob, int) {
	t.Helper()
	q, r, err := buildQueue(spec, "out")
	if err != nil {
		t.Fatal(err)
	}
	return q, r
}

// TestProtocolV2Specs checks the protocol v2 tournament specs parse and
// expand to the documented game counts (docs/MODERN_AI_RESEARCH.md §5): eight
// map-mixed seeds, both slot orders, the mirror played once, random starts.
func TestProtocolV2Specs(t *testing.T) {
	want := map[string]int{"v2-land": 384, "v2-water": 256, "v2-ladder": 768, "v2-long": 144}
	for name, games := range want {
		raw, err := os.ReadFile(filepath.Join("testdata", "protocol-v2", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var spec TournamentSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		queue, _ := mustQueue(t, spec)
		if spec.Protocol != "v2" || len(spec.Seeds) < 8 || spec.RawSeeds || len(queue) != games {
			t.Fatalf("%s: protocol %q, %d seeds, raw %v, %d games; want v2, >=8, false, %d", name, spec.Protocol, len(spec.Seeds), spec.RawSeeds, len(queue), games)
		}
		for _, j := range queue {
			if !slices.Contains(j.args, "random") {
				t.Fatalf("%s: %v does not ask for random starts", name, j.args)
			}
		}
	}
}
