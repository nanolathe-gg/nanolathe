//go:build retail

package headless

import (
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/tactics"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utility"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// TestArenaMatchesShareNoProcessState plays the same match twice at once in
// one process, with asynchronous thinkers, and requires identical results.
// Every piece of a match's host state (the event drain, the probe's metric
// buffers, the allocation switch) must belong to that match: under -race a
// shared buffer is a reported race, and without it a shared buffer can leak
// one match's reads into the other's.
func TestArenaMatchesShareNoProcessState(t *testing.T) {
	root := testsupport.RetailRoot(t)
	persona, ok := aikit.PersonaByName("hard")
	if !ok {
		t.Fatal("no hard persona")
	}
	persona.Async = true
	req := func() ArenaRequest {
		players := make([]ArenaPlayer, 2)
		for i := range players {
			st, ec, pr := utility.Policies(utility.DefaultParams())
			brain := core.New("util+tac", st, ec, tactics.New(tactics.DefaultParams()), pr)
			players[i] = ArenaPlayer{Label: "util+tac", Brain: brain, Persona: persona, Side: i}
		}
		return ArenaRequest{Root: root, Map: "the pass", Seed: 3, MapSeed: true, TickLimit: 900,
			Gameplay: gameplay.Mode(aikitmod.ArenaSet), Players: players, MeasureAllocs: true}
	}
	var results [2]ArenaResult
	var errs [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = RunArenaMatch(req())
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("match %d: %v", i, err)
		}
	}
	a, b := results[0], results[1]
	if a.BattleSeed != ArenaMapSeed(3, "the pass") || a.Ticks != b.Ticks || a.Winner != b.Winner {
		t.Fatalf("matches differ: battle seed %d, ticks %d/%d, winner %d/%d", a.BattleSeed, a.Ticks, b.Ticks, a.Winner, b.Winner)
	}
	for i := range a.Players {
		pa, pb := a.Players[i], b.Players[i]
		if pa.ScoreDefault != pb.ScoreDefault || pa.ScoreInvested != pb.ScoreInvested || len(pa.Series) != len(pb.Series) {
			t.Fatalf("slot %d differs: scores %d/%d and %d/%d", i, pa.ScoreDefault, pb.ScoreDefault, pa.ScoreInvested, pb.ScoreInvested)
		}
		if pa.Alive && pa.ScoreInvested < pa.ScoreDefault {
			t.Fatalf("slot %d: invested score %d below default %d", i, pa.ScoreInvested, pa.ScoreDefault)
		}
		if pa.Start != i {
			t.Fatalf("slot %d took start %d under slot starts", i, pa.Start)
		}
	}
}

// TestArenaSkipsPublicationAndAdjudicates locks two tournament-speed
// shortcuts (docs/MODERN_AI_RESEARCH.md §5.3). Dropping the frame buffer
// must not change the game: nothing authoritative reads a published frame
// [I6], so a match with and without publication records the same result
// apart from its timings. An adjudicated match is the same game cut short:
// it ends at the first sample where the rule holds, as the leader's win, and
// its samples are the full game's up to there.
func TestArenaSkipsPublicationAndAdjudicates(t *testing.T) {
	root := testsupport.RetailRoot(t)
	persona, ok := aikit.PersonaByName("hard")
	if !ok {
		t.Fatal("no hard persona")
	}
	req := func() ArenaRequest {
		st, ec, pr := utility.Policies(utility.DefaultParams())
		brain := core.New("util+tac", st, ec, tactics.New(tactics.DefaultParams()), pr)
		return ArenaRequest{Root: root, Map: "the pass", Seed: 5, MapSeed: true, TickLimit: 1800,
			Gameplay: gameplay.Mode(aikitmod.ArenaSet), Players: []ArenaPlayer{
				{Label: "util+tac", Brain: brain, Persona: persona, Side: 0},
				{Label: "retail", Side: 1},
			}}
	}
	untimed := func(r ArenaResult) ArenaResult {
		r.WallSeconds, r.TickMeanUS, r.TickP99US, r.Host = 0, 0, 0, ArenaHostTiming{}
		r.Players = slices.Clone(r.Players)
		for i := range r.Players {
			r.Players[i].Cost = ArenaCost{}
		}
		return r
	}
	published := req()
	published.Publish = true
	withFrames, err := RunArenaMatch(published)
	if err != nil {
		t.Fatal(err)
	}
	without, err := RunArenaMatch(req())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(untimed(withFrames), untimed(without)) {
		t.Fatalf("publication changed the game: ticks %d/%d, winner %d/%d, scores %d:%d / %d:%d",
			withFrames.Ticks, without.Ticks, withFrames.Winner, without.Winner,
			withFrames.Players[0].Score, withFrames.Players[1].Score, without.Players[0].Score, without.Players[1].Score)
	}
	if without.Reason == "decisive" {
		t.Skip("the reference match ended before the adjudication check")
	}
	cut := req()
	cut.Adjudicate = ArenaAdjudication{RatioPct: 101, From: 1200}
	adj, err := RunArenaMatch(cut)
	if err != nil {
		t.Fatal(err)
	}
	full := without.Players
	lead := 0
	for i := range full {
		if sampleScore(full[i].Series[8], ScoreDefault) > sampleScore(full[lead].Series[8], ScoreDefault) {
			lead = i
		}
	}
	if adj.Reason != "adjudicated" || adj.Ticks != 1200 || adj.Winner != lead || adj.Adjudication == nil {
		t.Fatalf("adjudicated match: reason %q at tick %d, winner %d; want adjudicated at 1200 for %d", adj.Reason, adj.Ticks, adj.Winner, lead)
	}
	for i, p := range adj.Players {
		if !reflect.DeepEqual(p.Series, full[i].Series[:len(p.Series)]) || len(p.Series) != 9 {
			t.Fatalf("slot %d: the adjudicated match's %d samples are not the full game's prefix", i, len(p.Series))
		}
	}
}
