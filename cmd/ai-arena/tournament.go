package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
)

// TournamentSpec lists the matches to play. Every pair plays every map and
// seed, once in each slot order when SwapSides.
type TournamentSpec struct {
	Name string `json:"name"`
	// Protocol names the evaluation protocol the spec follows
	// (docs/MODERN_AI_RESEARCH.md §5); it is recorded, not interpreted.
	Protocol  string     `json:"protocol,omitempty"`
	Maps      []string   `json:"maps"`
	Seeds     []uint     `json:"seeds"`
	Ticks     uint       `json:"ticks"`
	Pairs     [][]string `json:"pairs"`
	Roster    []string   `json:"roster"` // round robin when Pairs is empty
	SwapSides bool       `json:"swap_sides"`
	// RawSeeds plays each seed as the battle seed on every map instead of
	// the map-mixed derivation (match -raw-seed). Only for reproducing runs
	// made before map-mixed seeds.
	RawSeeds bool `json:"raw_seeds,omitempty"`
	// Starts is "slot" (default: slot i at start i), "random" (the retail
	// randomized assignment, a per-game coin with two players) or "both"
	// (every order plays once at slot starts and once swapped, two players
	// only), so start asymmetry can be told apart from slot order.
	Starts string   `json:"starts,omitempty"`
	Extra  []string `json:"extra"` // extra match flags
	// Sequential plays the seeds in blocks and stops each pair at a planned
	// look once its verdict is clear (docs/MODERN_AI_RESEARCH.md §5.3).
	Sequential *SequentialSpec `json:"sequential,omitempty"`
}

type matchJob struct {
	id      string
	args    []string
	players []string
	pair    int     // index into the spec's pairs
	seed    int     // index into the spec's seeds
	mapName string  // as the spec names it
	aSlot   int     // the slot the pair's first contestant plays
	look    int     // the sequential look that first includes the seed
	cost    float64 // expected wall seconds (estimateSeconds)
}

func runTournament(args []string) error {
	fs := flag.NewFlagSet("tournament", flag.ContinueOnError)
	specPath := fs.String("spec", "", "tournament spec JSON")
	out := fs.String("out", "", "output directory")
	// Three niced processes is the protocol's cap on a shared host
	// (docs/MODERN_AI_RESEARCH.md §5); raise it only on a dedicated one.
	jobsFlag := fs.String("jobs", strconv.Itoa(min(3, runtime.NumCPU())), "parallel match processes, or auto: the host's free cores, at most 4")
	order := fs.String("order", "longest", "play order: longest (the games expected to take longest first, by map and length) or spec")
	slots := fs.String("slots", "auto", "auto: share the host's match slots (half the logical CPUs) with every other tournament; dir:n shares n slots with tournaments given the same dir (lock files); off: no sharing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	jobs, how, err := parseJobs(*jobsFlag)
	if err != nil {
		return err
	}
	pool, err := parseSlots(*slots)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*specPath)
	if err != nil {
		return err
	}
	var spec TournamentSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(*out, "matches"), 0o755); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	queue, replays, err := buildQueue(spec, filepath.Join(*out, "matches"))
	if err != nil {
		return err
	}
	var plan *seqPlan
	if spec.Sequential != nil {
		if plan, err = newSeqPlan(spec.Sequential, len(spec.Seeds)); err != nil {
			return err
		}
		if !spec.SwapSides {
			return fmt.Errorf("sequential: needs swap_sides, so every block plays both slot orders")
		}
		for i := range queue {
			if len(queue[i].players) != 2 {
				return fmt.Errorf("sequential: needs two-player pairs, have %v", queue[i].players)
			}
			queue[i].look = plan.lookOf(queue[i].seed)
		}
	}
	switch *order {
	case "longest":
		orderQueue(queue, true, plan != nil)
	case "spec":
		orderQueue(queue, false, plan != nil)
	default:
		return fmt.Errorf("unknown -order %q (have longest, spec)", *order)
	}
	if how != "" {
		how = " (" + how + ")"
	}
	fmt.Fprintf(os.Stderr, "tournament %s: %d matches on %d workers%s (%d mirror replays skipped)\n", spec.Name, len(queue), jobs, how, replays)
	t := newTourney(spec, queue, plan, *out)
	t.run(self, jobs, pool)
	// Collect: every result except a sequential pair's games past its last look.
	excluded := t.excluded()
	var all []headless.ArenaResult
	byID := slices.Clone(t.queue)
	slices.SortFunc(byID, func(a, b matchJob) int { return strings.Compare(a.id, b.id) })
	for _, j := range byID {
		if slices.Contains(excluded, j.id) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(*out, "matches", j.id+".json"))
		if err != nil {
			continue
		}
		var r headless.ArenaResult
		if json.Unmarshal(raw, &r) == nil {
			for i := range r.Players {
				r.Players[i].Series = nil
			}
			all = append(all, r)
		}
	}
	if plan != nil {
		if err := writeJSON(filepath.Join(*out, "sequential.json"), t.report(excluded)); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(*out, "summary.json"), map[string]any{"spec": spec, "results": all, "wall_seconds": time.Since(t.started).Seconds()})
}

// tourney plays a queue of matches on a pool of worker processes and, for a
// sequential spec, stops each pair at the first look that decides it.
type tourney struct {
	spec   TournamentSpec
	queue  []matchJob
	plan   *seqPlan
	out    string
	byPair [][]int // queue indices of each pair's games

	mu       sync.Mutex
	next     int
	done     []bool // by queue index: finished, failed or resumed
	running  map[int]context.CancelFunc
	finished int
	started  time.Time
	// Per pair: looks evaluated, the next look, and the seeds a decided pair
	// used (0 while it plays on).
	looks    [][]seqLook
	nextLook []int
	stopAt   []int
}

func newTourney(spec TournamentSpec, queue []matchJob, plan *seqPlan, out string) *tourney {
	np := len(spec.Pairs)
	if np == 0 {
		np = len(spec.Roster) * (len(spec.Roster) - 1) / 2
	}
	t := &tourney{spec: spec, queue: queue, plan: plan, out: out, byPair: make([][]int, np),
		done: make([]bool, len(queue)), running: map[int]context.CancelFunc{},
		looks: make([][]seqLook, np), nextLook: make([]int, np), stopAt: make([]int, np)}
	for i, j := range queue {
		t.byPair[j.pair] = append(t.byPair[j.pair], i)
	}
	return t
}

func (t *tourney) resultPath(j matchJob) string {
	return filepath.Join(t.out, "matches", j.id+".json")
}

// run plays the queue. Games already on disk count as played (resume), and
// a sequential pair's looks are re-read from them, so a resumed tournament
// reaches the decisions an uninterrupted one would.
func (t *tourney) run(self string, jobs int, pool *slotPool) {
	t.started = time.Now()
	t.mu.Lock()
	for i, j := range t.queue {
		if _, err := os.Stat(t.resultPath(j)); err == nil {
			t.done[i] = true
			t.finished++
		}
	}
	for p := range t.byPair {
		t.evaluate(p)
	}
	t.mu.Unlock()
	var wg sync.WaitGroup
	for range max(jobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i, ctx, ok := t.take()
				if !ok {
					return
				}
				err := t.play(ctx, self, t.queue[i], pool)
				t.finish(i, err, ctx.Err() != nil)
			}
		}()
	}
	wg.Wait()
}

// take hands out the next game to play, skipping played games and games a
// decided pair no longer needs.
func (t *tourney) take() (int, context.Context, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.next < len(t.queue) {
		i := t.next
		t.next++
		if t.done[i] || t.decided(t.queue[i]) {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.running[i] = cancel
		return i, ctx, true
	}
	return 0, nil, false
}

func (t *tourney) decided(j matchJob) bool {
	return t.stopAt[j.pair] > 0 && j.seed >= t.stopAt[j.pair]
}

// play runs one match process; cancelling ctx kills it.
func (t *tourney) play(ctx context.Context, self string, j matchJob, pool *slotPool) error {
	if pool != nil {
		release, err := pool.acquire(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	cmd := exec.CommandContext(ctx, self, j.args...)
	logf, _ := os.Create(filepath.Join(t.out, "matches", j.id+".log"))
	defer logf.Close()
	cmd.Stderr = logf
	cmd.Stdout = logf
	return cmd.Run()
}

func (t *tourney) finish(i int, err error, cancelled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	j := t.queue[i]
	t.running[i]()
	delete(t.running, i)
	t.done[i] = true
	t.finished++
	switch {
	case cancelled:
		fmt.Fprintf(os.Stderr, "%s stopped: its pair was decided\n", j.id)
	case err != nil:
		fmt.Fprintf(os.Stderr, "%s failed: %v (%s)\n", j.id, err, strings.Join(j.players, " vs "))
	}
	if t.finished%10 == 0 || t.finished == len(t.queue) {
		fmt.Fprintf(os.Stderr, "  %d/%d done, %.0fs\n", t.finished, len(t.queue), time.Since(t.started).Seconds())
	}
	t.evaluate(j.pair)
}

// evaluate runs every look of pair p whose games have all finished, in
// order, and stops the pair at the first that decides it: its queued games
// are dropped and its running games past the look are killed.
func (t *tourney) evaluate(p int) {
	if t.plan == nil || t.stopAt[p] > 0 || len(t.byPair[p]) == 0 {
		return
	}
	if pair := t.queue[t.byPair[p][0]].players; pair[0] == pair[1] {
		return // a mirror has no second contestant to decide against
	}
	for t.nextLook[p] < len(t.plan.looks) {
		k := t.nextLook[p]
		n := t.plan.looks[k]
		var games []seqGame
		for _, qi := range t.byPair[p] {
			j := t.queue[qi]
			if j.seed >= n {
				continue
			}
			if !t.done[qi] {
				return
			}
			g, err := t.readGame(j)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: no result for look %d: %v\n", j.id, k+1, err)
				continue
			}
			games = append(games, g)
		}
		l := t.plan.look(k, games)
		t.looks[p] = append(t.looks[p], l)
		t.nextLook[p]++
		fmt.Fprintf(os.Stderr, "  pair %d look %d/%d (%d seeds, %d games): share %.3f, t %.2f (df %d, bound %.2f), power %.2f  %s\n",
			p+1, k+1, len(t.plan.looks), n, l.Games, l.Share, l.T, l.DF, l.Bound, l.Power, l.Decision)
		if l.Decision != "" {
			t.stopAt[p] = n
			for qi, cancel := range t.running {
				if t.decided(t.queue[qi]) {
					cancel()
				}
			}
			return
		}
	}
}

// readGame reads a finished game as a look sees it: A's points, where A is
// the pair's first contestant.
func (t *tourney) readGame(j matchJob) (seqGame, error) {
	raw, err := os.ReadFile(t.resultPath(j))
	if err != nil {
		return seqGame{}, err
	}
	var r struct {
		Winner int `json:"winner"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return seqGame{}, err
	}
	g := seqGame{mapName: j.mapName, seed: j.seed, points: 0.5}
	switch {
	case r.Winner == j.aSlot:
		g.points = 1
	case r.Winner >= 0:
		g.points = 0
	}
	return g, nil
}

// excluded lists the finished games of decided pairs past their last look,
// played while the look was pending; they stay on disk and out of the
// verdict and the summary.
func (t *tourney) excluded() []string {
	var out []string
	for _, j := range t.queue {
		if t.decided(j) {
			if _, err := os.Stat(t.resultPath(j)); err == nil {
				out = append(out, j.id)
			}
		}
	}
	slices.Sort(out)
	return out
}

// seqPairReport is one pair's sequential record in sequential.json.
type seqPairReport struct {
	Pair    []string  `json:"pair"`
	Looks   []seqLook `json:"looks"`
	Verdict string    `json:"verdict"` // A (the pair's first contestant) against B
	Seeds   int       `json:"seeds"`   // seeds the verdict used
}

func (t *tourney) report(excluded []string) map[string]any {
	var pairs []seqPairReport
	for p, idx := range t.byPair {
		if len(idx) == 0 {
			continue
		}
		j := t.queue[idx[0]]
		pair := slices.Clone(j.players)
		if j.aSlot != 0 {
			slices.Reverse(pair)
		}
		r := seqPairReport{Pair: pair, Looks: t.looks[p], Seeds: t.stopAt[p]}
		if n := len(r.Looks); n > 0 {
			r.Verdict = r.Looks[n-1].Decision
		}
		pairs = append(pairs, r)
	}
	return map[string]any{"looks": t.plan.looks, "alpha": t.plan.alpha, "futility": t.plan.futility,
		"obf_c": t.plan.c, "pairs": pairs, "excluded": excluded}
}

// buildQueue expands a spec into match jobs writing into dir. Each pair
// plays every map and seed, in both slot orders when SwapSides, and at each
// requested start assignment. An order whose
// resolved player arguments repeat one already queued for the same map and
// seed is skipped: when both contestants have identical specs the reversed
// order is the identical deterministic game, so it would only count one game
// twice. replays counts the skipped orders.
func buildQueue(spec TournamentSpec, dir string) (queue []matchJob, replays int, err error) {
	var startModes []string
	switch spec.Starts {
	case "", "slot":
		startModes = []string{"slot"}
	case "random":
		startModes = []string{"random"}
	case "both":
		startModes = []string{"slot", "swap"}
	default:
		return nil, 0, fmt.Errorf("tournament: unknown starts %q (have slot, random, both)", spec.Starts)
	}
	pairs := spec.Pairs
	if len(pairs) == 0 {
		for i := 0; i < len(spec.Roster); i++ {
			for j := i + 1; j < len(spec.Roster); j++ {
				pairs = append(pairs, []string{spec.Roster[i], spec.Roster[j]})
			}
		}
	}
	n := 0
	for pi, pair := range pairs {
		if spec.Starts == "both" && len(pair) != 2 {
			return nil, 0, fmt.Errorf("tournament: starts \"both\" needs two-player pairs, have %v", pair)
		}
		for _, m := range spec.Maps {
			for si, seed := range spec.Seeds {
				orders := [][]string{pair}
				if spec.SwapSides {
					rev := make([]string, len(pair))
					for i := range pair {
						rev[i] = pair[len(pair)-1-i]
					}
					orders = append(orders, rev)
				}
				var played []string
				for oi, ps := range orders {
					for _, starts := range startModes {
						var players []string
						for i, p := range ps {
							// Sides follow the slot unless the spec names them, so a
							// swapped pair also swaps factions.
							players = append(players, "-p", withSide(p, i))
						}
						key := starts + "\x00" + strings.Join(players, "\x00")
						if slices.Contains(played, key) {
							replays++
							continue
						}
						played = append(played, key)
						n++
						id := fmt.Sprintf("m%04d", n)
						a := []string{"match", "-map", m, "-seed", fmt.Sprint(seed), "-ticks", fmt.Sprint(spec.Ticks), "-out", filepath.Join(dir, id+".json")}
						if spec.RawSeeds {
							a = append(a, "-raw-seed")
						}
						if starts != "slot" {
							a = append(a, "-starts", starts)
						}
						a = append(a, players...)
						a = append(a, spec.Extra...)
						aSlot := 0
						if oi == 1 {
							aSlot = len(ps) - 1 // the reversed order
						}
						queue = append(queue, matchJob{id: id, args: a, players: ps, pair: pi, seed: si, mapName: m,
							aSlot: aSlot, cost: estimateSeconds(m, spec.Ticks)})
					}
				}
			}
		}
	}
	return queue, replays, nil
}

// withSide appends the slot's side when the spec does not name one.
func withSide(p string, slot int) string {
	parts := strings.SplitN(p, ":", 4)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	if parts[2] == "" {
		parts[2] = fmt.Sprint(slot & 1)
	}
	if len(parts) == 3 {
		return strings.Join(parts, ":")
	}
	return strings.Join(parts[:3], ":") + ":" + parts[3]
}
