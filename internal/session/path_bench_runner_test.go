//go:build pathbench && retail

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const pbSchema = 1

type pbObjective struct {
	LongestCellStill                                                               int
	Actor                                                                          int
	Cohort, Key                                                                    string
	IssuedTick                                                                     int
	Goal                                                                           [2]numeric.Fixed
	Radius                                                                         int32
	FirstNearTick, FirstNoMoveHeadTick                                             int
	End                                                                            string
	LongestStill, BlockedTicks, PendingTicks, MaxPendingAge, RequestPolls, Results int
	TravelRaw                                                                      int64 // sum of max(abs(dx),abs(dz)), explicitly a lattice-distance proxy
}
type pbActorOutcome struct {
	Carrier                uint16
	MoveMode               uint8
	Remaining              float32
	Handle                 uint16
	Key, Cohort            string
	Alive, ExpectedRemoval bool
	Owner                  uint8
	EndRaw                 [2]numeric.Fixed
	Order                  string
}
type pbVisit struct {
	Actor                               int
	Region, Cohort                      string
	FirstTick, Ticks, Entries, LastTick int
}
type pbKnowledgeSample struct {
	Tick    int
	Learned [10]int
	Mapped  [10]int
}

type pbOutcome struct {
	Knowledge                                           []pbKnowledgeSample
	LearnedBlocksByOwner                                [10]int
	InitialLive, PeakLive, FinalLive                    int
	CreatedByPlayer                                     [10]uint32
	UnitsEnd                                            []pbActorOutcome
	Objectives                                          []pbObjective
	Actors                                              []pbActorOutcome
	Visits                                              []pbVisit
	SearchStarts, SearchDone, SearchSetup, SearchPopped int
	FinalPartialHash, TrajectoryHash                    string
	Inputs                                              []string
}
type pbCost struct {
	TickNS                              []int64
	TickCPUNS                           []int64 // thread CPU time per tick (research; robust to host load)
	P50NS, P95NS, P99NS, MaxNS          int64
	TotalTickNS, EventNS                int64
	AllocBytes, Allocs                  uint64
	HeapBefore, HeapAfter, HeapRetained uint64
	GCs                                 uint32
}
type pbReport struct {
	Schema                                     int
	Case, Family, Description, Rules, BaseMode string
	Size, Ticks, Repeat                        int
	CatalogHash, AssetManifest, InputHash      string
	InitialPartialHash                         string
	Scene                                      any
	Outcome                                    pbOutcome
	Cost                                       pbCost
}
type pbWatch struct {
	identity       *units.Unit
	cellStill      int
	objective      int
	lastX, lastZ   numeric.Fixed
	still, pending int
	lastPoll       uint32
	lastResult     uint32
	lastActivation uint64
}
type pbKernel struct {
	inner path.Kernel
	out   *pbOutcome
}
type pbSearch struct {
	path.Search
	out    *pbOutcome
	popped int
	done   bool
}

func (k pbKernel) NewSession(c path.SearchConfig) path.Search {
	s := k.inner.NewSession(c)
	k.out.SearchStarts++
	k.out.SearchSetup += s.SetupSteps()
	return &pbSearch{Search: s, out: k.out}
}
func (s *pbSearch) Resume(budget int) ([]path.Point, path.Status, bool) {
	points, status, done := s.Search.Resume(budget)
	n := s.Search.Popped()
	s.out.SearchPopped += n - s.popped
	s.popped = n
	if done && !s.done {
		s.done = true
		s.out.SearchDone++
	}
	return points, status, done
}
func pbJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
func pbDigest(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func pbFingerprint(t *testing.T, s *Session) string {
	t.Helper()
	h, e := s.PartialStateFingerprint()
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func pbSceneDescription(t *testing.T, sc *pbScene) any {
	actors := make([]any, 0, len(sc.Actors))
	for _, a := range sc.Actors {
		u := sc.S.Units.Unit(a.Handle)
		actors = append(actors, struct {
			Actor                                pbActor
			Owner                                uint8
			DefinitionHash                       string
			Profile                              any
			MaxVelocity, Acceleration, BrakeRate int32
			TurnRate                             int32
		}{*a, u.Owner, u.Def.Hash, sc.S.Movement.ProfileFor(a.Handle), u.Def.MaxVelocity, u.Def.Acceleration, u.Def.BrakeRate, u.Def.TurnRate})
	}
	events := make([]string, len(sc.Events))
	for i, e := range sc.Events {
		events[i] = fmt.Sprintf("tick=%d %s", e.Tick, e.Label)
	}
	return struct {
		Width, Height         int32
		Sea                   uint8
		TerrainHash           string
		Actors                []any
		Events, Inputs, Notes []string
		Regions               []pbRegion
		Features              any
		VisibilityMode        uint8
		LocalOwner            uint8
		Seeds                 [2]uint32
		UnitLimit             int
	}{sc.S.World.CellW, sc.S.World.CellH, sc.S.World.SeaLevel, pbDigest(t, sc.S.World.Plot), actors, events, append([]string(nil), sc.Inputs...), sc.Notes, sc.Regions, sc.S.Community, uint8(sc.S.Vis.Mode()), sc.S.LocalOwner, [2]uint32{7, 11}, 500}
}
func pbAbs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func pbNear(a *pbActor, x, z numeric.Fixed) bool {
	dx, dz := int64(x-a.Goal[0]), int64(z-a.Goal[1])
	r := int64(numeric.FixedFromInt(int64(a.Radius)))
	return dx*dx+dz*dz <= r*r
}
func pbOrderName(s *Session, a *pbActor) string {
	u := s.Units.Unit(a.Handle)
	if u == nil {
		return ""
	}
	q := orders.QueueOfUnit(u)
	if q == nil || q.Head() == nil {
		return ""
	}
	return orders.DescriptorFor(q.Head().ID).Name
}
func pbObserve(sc *pbScene, tick int, out *pbOutcome, watches *[]pbWatch) {
	for len(*watches) < len(sc.Actors) {
		a := sc.Actors[len(*watches)]
		u := sc.S.Units.Unit(a.Handle)
		w := pbWatch{objective: -1, identity: u}
		if u != nil {
			w.lastX, w.lastZ = u.X, u.Z
		}
		*watches = append(*watches, w)
	}
	for i, a := range sc.Actors {
		w := &(*watches)[i]
		u := sc.S.Units.Unit(a.Handle)
		if u != w.identity {
			u = nil
		}
		if a.GoalActive && (w.objective < 0 || out.Objectives[w.objective].IssuedTick != a.GoalTick) {
			if w.objective >= 0 && out.Objectives[w.objective].End == "pending" {
				out.Objectives[w.objective].End = "superseded"
			}
			w.objective = len(out.Objectives)
			w.still = 0
			w.cellStill = 0
			w.pending = 0
			out.Objectives = append(out.Objectives, pbObjective{Actor: i, Cohort: a.Cohort, Key: a.Key, IssuedTick: a.GoalTick, Goal: a.Goal, Radius: a.Radius, End: "pending"})
		}
		if u == nil || !u.Alive {
			if w.objective >= 0 {
				o := &out.Objectives[w.objective]
				if !a.GoalActive && o.End == "pending" {
					o.End = "cancelled"
				}
				if o.End == "pending" {
					o.End = "removed"
				}
			}
			continue
		}
		for _, region := range sc.Regions {
			cx, cz := world.WorldToCell(u.X), world.WorldToCell(u.Z)
			if cx < region.X0 || cx >= region.X1 || cz < region.Z0 || cz >= region.Z1 {
				continue
			}
			found := -1
			for k := range out.Visits {
				if out.Visits[k].Actor == i && out.Visits[k].Region == region.Name {
					found = k
					break
				}
			}
			if found < 0 {
				out.Visits = append(out.Visits, pbVisit{Actor: i, Region: region.Name, Cohort: a.Cohort, FirstTick: tick})
				found = len(out.Visits) - 1
			}
			if out.Visits[found].LastTick != tick-1 || out.Visits[found].Ticks == 0 {
				out.Visits[found].Entries++
			}
			out.Visits[found].Ticks++
			out.Visits[found].LastTick = tick
		}
		if w.objective < 0 {
			w.lastX, w.lastZ = u.X, u.Z
			continue
		}
		o := &out.Objectives[w.objective]
		if !a.GoalActive && o.End == "pending" {
			o.End = "cancelled"
		}
		if tick < a.GoalTick {
			continue
		}
		if a.CaptureGoal {
			q := orders.QueueOfUnit(u)
			if q != nil {
				// The newly issued node may be queued behind an active record.
				// Capture its group-adjusted goal, not an older head's destination.
				for _, node := range q.Primary() {
					if int(node.CreationTick) == a.GoalTick && isHumanMoveOrder(node.ID) {
						a.Goal = [2]numeric.Fixed{node.GoalX, node.GoalZ}
						o.Goal = a.Goal
					}
				}
			}
			a.CaptureGoal = false
		}
		if o.FirstNearTick == 0 && pbNear(a, u.X, u.Z) {
			o.FirstNearTick = tick
		}
		active := strings.Contains(pbOrderName(sc.S, a), "Move")
		if !active && o.FirstNoMoveHeadTick == 0 {
			o.FirstNoMoveHeadTick = tick
		}
		// Goal proximity and order exit remain separate observations: proximity
		// alone cannot prove a repair, build, patrol or moving-target order finished.
		if o.End == "pending" && o.FirstNearTick != 0 {
			o.End = "near_goal"
		}
		if o.End == "pending" {
			if u.X == w.lastX && u.Z == w.lastZ {
				w.still++
			} else {
				w.still = 0
			}
			o.LongestStill = max(o.LongestStill, w.still)
			if world.WorldToCell(u.X) == world.WorldToCell(w.lastX) && world.WorldToCell(u.Z) == world.WorldToCell(w.lastZ) {
				w.cellStill++
			} else {
				w.cellStill = 0
			}
			o.LongestCellStill = max(o.LongestCellStill, w.cellStill)
			o.TravelRaw += max(pbAbs(int64(u.X-w.lastX)), pbAbs(int64(u.Z-w.lastZ)))
			if c := sc.S.Movement.Collisions[a.Handle]; c != nil && c.Blocked {
				o.BlockedTicks++
			}
			if sc.S.Movement.HasPathRequest(a.Handle) {
				w.pending++
				o.PendingTicks++
				o.MaxPendingAge = max(o.MaxPendingAge, w.pending)
			} else {
				w.pending = 0
			}
			if r := sc.S.Movement.Routes[a.Handle]; r != nil && r.LastRequestTick != w.lastPoll {
				w.lastPoll = r.LastRequestTick
				o.RequestPolls++
			}
		}
		_, r := sc.S.Path.TraceFor(a.Handle)
		if r != nil && r.Done && (r.Tick != w.lastResult || r.Request.Activation != w.lastActivation) {
			w.lastResult = r.Tick
			w.lastActivation = r.Request.Activation
			o.Results++
		}
		w.lastX, w.lastZ = u.X, u.Z
	}
}

// No allocation, clocks, tracing or decisions: a partial trajectory digest
// checks that diagnostic observation did not alter the paired execution.
func pbTrajectory(sc *pbScene, h uint64) uint64 {
	mix := func(v uint64) { h ^= v; h *= 1099511628211 }
	mix(uint64(sc.S.Clock.GlobalTick))
	mix(uint64(len(sc.Actors)))
	for player := 0; player < 10; player++ {
		mix(uint64(sc.S.Units.CreatedCountForPlayer(player)))
		sc.S.Units.ForEachPlayerSliceLive(player, func(u *units.Unit) {
			mix(uint64(u.Handle))
			mix(uint64(u.X))
			mix(uint64(u.Y))
			mix(uint64(u.Z))
			mix(uint64(u.Owner))
			mix(uint64(u.Move.Heading))
			if q := orders.QueueOfUnit(u); q != nil && q.Head() != nil {
				mix(uint64(q.Head().ID))
				mix(uint64(q.Head().GoalX))
				mix(uint64(q.Head().GoalZ))
			}
		})
	}

	return h
}
func pbRun(t *testing.T, c pbCase, rules string, n, ticks int, diagnostic bool, profile string) (*pbScene, pbOutcome, pbCost, string, any) {
	t.Helper()
	sc := c.Build(t, rules, n)
	sort.SliceStable(sc.Events, func(i, j int) bool { return sc.Events[i].Tick < sc.Events[j].Tick })
	for _, e := range sc.Events {
		if e.Tick < 1 || e.Tick > ticks {
			if ticks == c.Ticks {
				t.Fatalf("%s event out of window: %v", c.ID, e)
			}
		}
	}
	initial := pbFingerprint(t, sc.S)
	scene := pbSceneDescription(t, sc)
	var out pbOutcome
	out.InitialLive = pbLive(sc)
	out.PeakLive = out.InitialLive
	if diagnostic {
		set := sc.S.Rules
		set.Path = pbKernel{inner: set.Path, out: &out}
		sc.S.BindRules(set)
		sc.S.Path.EnableTrace()
	}
	var watches []pbWatch
	var tracer *pbTracer
	if diagnostic {
		tracer = pbTraceOpen(sc, c.ID, rules, n)
	}
	cost := pbCost{TickNS: make([]int64, ticks), TickCPUNS: make([]int64, ticks)}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Initialize observers before the window; first displacement is retained.
	if diagnostic {
		pbObserve(sc, 0, &out, &watches)
	}
	if profile != "" {
		f, e := os.Create(profile + ".cpu.pprof")
		if e != nil {
			t.Fatal(e)
		}
		if e = pprof.StartCPUProfile(f); e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		defer pprof.StopCPUProfile()
	}
	runtime.GC()
	if profile != "" {
		pbAllocProfile(t, profile+".alloc-before.pprof")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	hash := uint64(14695981039346656037)
	event := 0
	for tick := 1; tick <= ticks; tick++ {
		if event < len(sc.Events) && sc.Events[event].Tick == tick {
			if diagnostic {
				out.Knowledge = append(out.Knowledge, pbKnowledgeAt(sc, tick-1))
			}
			begin := time.Now()
			for event < len(sc.Events) && sc.Events[event].Tick == tick {
				sc.Events[event].Apply(t, sc)
				event++
			}
			cost.EventNS += time.Since(begin).Nanoseconds()
		}
		cpu0 := pbThreadCPU()
		start := time.Now()
		got := sc.S.Clock.BeginSubTick()
		sc.S.stepAuthoritativePhases(got)
		cost.TickNS[tick-1] = time.Since(start).Nanoseconds()
		cost.TickCPUNS[tick-1] = pbThreadCPU() - cpu0
		hash = pbTrajectory(sc, hash)
		if diagnostic {
			pbObserve(sc, tick, &out, &watches)
			out.PeakLive = max(out.PeakLive, pbLive(sc))
			tracer.frame(sc, tick)
		}
	}
	tracer.close(sc)
	runtime.ReadMemStats(&after)
	if profile != "" {
		pbAllocProfile(t, profile+".alloc-after.pprof")
	}
	cost.AllocBytes = after.TotalAlloc - before.TotalAlloc
	cost.Allocs = after.Mallocs - before.Mallocs
	cost.HeapBefore = before.HeapAlloc
	cost.HeapAfter = after.HeapAlloc
	cost.GCs = after.NumGC - before.NumGC
	runtime.GC()
	runtime.ReadMemStats(&after)
	cost.HeapRetained = after.HeapAlloc
	runtime.KeepAlive(sc)
	out.FinalPartialHash = pbFingerprint(t, sc.S)
	out.TrajectoryHash = fmt.Sprintf("%016x", hash)
	out.Inputs = sc.Inputs
	if diagnostic {
		finalKnowledge := pbKnowledgeAt(sc, ticks)
		out.Knowledge = append(out.Knowledge, finalKnowledge)
		out.LearnedBlocksByOwner = finalKnowledge.Learned
		out.FinalLive = pbLive(sc)
		for i := range out.CreatedByPlayer {
			out.CreatedByPlayer[i] = sc.S.Units.CreatedCountForPlayer(i)
		}
		for _, u := range sc.S.Units.IterSliced() {
			out.UnitsEnd = append(out.UnitsEnd, pbActorOutcome{Handle: uint16(u.Handle), Key: u.Def.CanonicalKey, Owner: u.Owner, Alive: u.Alive, Carrier: uint16(u.Attachment.Carrier), MoveMode: u.Move.ModeMirror, Remaining: u.Remaining, EndRaw: [2]numeric.Fixed{u.X, u.Z}})
		}
		for i, a := range sc.Actors {
			u := sc.S.Units.Unit(a.Handle)
			if i >= len(watches) || u != watches[i].identity {
				u = nil
			}
			r := pbActorOutcome{Handle: uint16(a.Handle), Key: a.Key, Cohort: a.Cohort, ExpectedRemoval: a.ExpectedRemoval}
			if u != nil {
				r.Carrier = uint16(u.Attachment.Carrier)
				r.MoveMode = u.Move.ModeMirror
				r.Remaining = u.Remaining
				r.Alive = u.Alive
				r.Owner = u.Owner
				r.EndRaw = [2]numeric.Fixed{u.X, u.Z}
				r.Order = pbOrderName(sc.S, a)
			}
			out.Actors = append(out.Actors, r)
		}
		for i := range out.Objectives {
			o := &out.Objectives[i]
			if o.End == "pending" && o.FirstNoMoveHeadTick != 0 && o.FirstNearTick == 0 {
				o.End = "away_no_move_head"
			}
		}
	}
	sorted := append([]int64(nil), cost.TickNS...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	percentile := func(p int) int64 { return sorted[(len(sorted)*p+99)/100-1] }
	cost.P50NS = percentile(50)
	cost.P95NS = percentile(95)
	cost.P99NS = percentile(99)
	cost.MaxNS = sorted[len(sorted)-1]
	for _, v := range cost.TickNS {
		cost.TotalTickNS += v
	}
	return sc, out, cost, initial, scene
}
func pbEnvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 {
		t.Fatalf("invalid %s=%q", key, s)
	}
	return n
}
func TestPathBenchmark(t *testing.T) {
	dir := os.Getenv("NANOLATHE_PATH_BENCH_OUT")
	if dir == "" {
		t.Skip("use tools/path-bench")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("benchmark output must be absolute")
	}
	filter, e := regexp.Compile(os.Getenv("NANOLATHE_PATH_BENCH_FILTER"))
	if e != nil {
		t.Fatal(e)
	}
	sort.Slice(pbCases, func(i, j int) bool { return pbCases[i].ID < pbCases[j].ID })
	seen := map[string]bool{}
	var selected []pbCase
	for _, c := range pbCases {
		if seen[c.ID] {
			t.Fatalf("duplicate case %s", c.ID)
		}
		seen[c.ID] = true
		if filter.MatchString(c.ID) {
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 {
		t.Fatal("no matching path benchmark cases")
	}
	type listing struct {
		ID, Family, Description string
		Sizes                   []int
		Ticks                   int
	}
	catalog := make([]listing, 0, len(selected))
	for _, c := range selected {
		catalog = append(catalog, listing{c.ID, c.Family, c.Description, c.Sizes, c.Ticks})
	}
	pbJSON(t, filepath.Join(dir, "cases.json"), catalog)
	if os.Getenv("NANOLATHE_PATH_BENCH_LIST") == "1" {
		return
	}
	modes := strings.Split(os.Getenv("NANOLATHE_PATH_BENCH_RULES"), ",")
	if modes[0] == "" {
		modes = []string{"modern", "strict-3.1", "community-3.9"}
	}
	repeats := pbEnvInt(t, "NANOLATHE_PATH_BENCH_REPEATS", 3)
	sizeFilter := os.Getenv("NANOLATHE_PATH_BENCH_SIZES")
	runs := 0
	for _, c := range selected {
		for _, n := range c.Sizes {
			if sizeFilter != "" && !strings.Contains(","+sizeFilter+",", ","+strconv.Itoa(n)+",") {
				continue
			}
			ticks := pbEnvInt(t, "NANOLATHE_PATH_BENCH_TICKS", c.Ticks)
			for _, mode := range modes {
				label := fmt.Sprintf("%s/%s/n%d", c.ID, mode, n)
				t.Run(label, func(t *testing.T) {
					sc, diagnostic, _, initial, scene := pbRun(t, c, mode, n, ticks, true, "")
					input := pbDigest(t, struct {
						Scene  any
						Inputs []string
						Ticks  int
					}{scene, diagnostic.Inputs, ticks})
					catalogHash, manifest, base := sc.S.Catalog.Hash, sc.S.Catalog.Manifest, string(sc.S.Gameplay)
					for repeat := 1; repeat <= repeats; repeat++ {
						_, plain, cost, plainInitial, plainScene := pbRun(t, c, mode, n, ticks, false, "")
						if initial != plainInitial || pbDigest(t, scene) != pbDigest(t, plainScene) || pbDigest(t, diagnostic.Inputs) != pbDigest(t, plain.Inputs) || diagnostic.TrajectoryHash != plain.TrajectoryHash || diagnostic.FinalPartialHash != plain.FinalPartialHash {
							t.Fatalf("diagnostic/timed determinism mismatch for %s repeat %d", label, repeat)
						}
						report := pbReport{Schema: pbSchema, Case: c.ID, Family: c.Family, Description: c.Description, Rules: mode, BaseMode: base, Size: n, Ticks: ticks, Repeat: repeat, CatalogHash: catalogHash, AssetManifest: manifest, InputHash: input, InitialPartialHash: initial, Scene: scene, Outcome: diagnostic, Cost: cost}
						name := fmt.Sprintf("%s__%s__n%d__r%d", url.PathEscape(c.ID), url.PathEscape(mode), n, repeat)
						pbJSON(t, filepath.Join(dir, name+".json"), report)
						runs++
						t.Logf("%s p50=%dus p95=%dus p99=%dus bytes/tick=%d searches=%d", name, cost.P50NS/1000, cost.P95NS/1000, cost.P99NS/1000, cost.AllocBytes/uint64(ticks), diagnostic.SearchStarts)
					}
					if os.Getenv("NANOLATHE_PATH_BENCH_PROFILES") == "1" {
						_, profiled, _, _, _ := pbRun(t, c, mode, n, ticks, false, filepath.Join(dir, fmt.Sprintf("%s__%s__n%d", url.PathEscape(c.ID), url.PathEscape(mode), n)))
						if profiled.TrajectoryHash != diagnostic.TrajectoryHash || profiled.FinalPartialHash != diagnostic.FinalPartialHash {
							t.Fatal("profile pass changed observed trajectory")
						}
					}
				})
			}
		}
	}
	if runs == 0 {
		t.Fatal("no selected sizes ran (or retail assets missing)")
	}
	pbJSON(t, filepath.Join(dir, "complete.json"), struct{ Schema, Runs int }{pbSchema, runs})
}

func pbAllocProfile(t *testing.T, name string) {
	t.Helper()
	f, e := os.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = pprof.Lookup("allocs").WriteTo(f, 0); e != nil {
		t.Fatal(e)
	}
}

func pbLive(sc *pbScene) int {
	total := 0
	for i := 0; i < 10; i++ {
		total += sc.S.Units.LiveCountForPlayer(i)
	}
	return total
}

// The cost observer must neither manufacture idle order queues nor add its
// own allocations to the measured window. This is a measurement guard.
func TestPathBenchObserver(t *testing.T) {
	if os.Getenv("NANOLATHE_PATH_BENCH_SMOKE") == "" {
		t.Skip("opt-in observer check")
	}
	sc := pbNew(t, "modern", pbTerrain(t, 64, 64, 20, 0))
	a := pbAdd(t, sc, "armflea", "passive", 0, 8, 8)
	before := pbFingerprint(t, sc.S)
	q := orders.QueueOfUnit(sc.S.Units.Unit(a.Handle))
	if n := testing.AllocsPerRun(4, func() { _ = pbTrajectory(sc, 1) }); n != 0 {
		t.Fatalf("trajectory observer allocated %g times", n)
	}
	_ = pbOrderName(sc.S, a)
	if before != pbFingerprint(t, sc.S) || q != orders.QueueOfUnit(sc.S.Units.Unit(a.Handle)) {
		t.Fatal("observation changed passive-unit state")
	}
}

func pbKnowledgeAt(sc *pbScene, tick int) pbKnowledgeSample {
	sample := pbKnowledgeSample{Tick: tick}
	for _, word := range sc.S.Vis.WordMask() {
		for owner := 0; owner < 10; owner++ {
			if word&(1<<owner) != 0 {
				sample.Mapped[owner]++
			}
		}
	}
	if learned := sc.S.Movement.Rules.LearnedTerrain(sc.S.Movement); learned != nil {
		w, h := sc.S.Vis.GridDimensions()
		for owner := uint8(0); owner < 10; owner++ {
			for z := int32(0); z < h; z++ {
				for x := int32(0); x < w; x++ {
					if learned.Known(x, z, owner) {
						sample.Learned[owner]++
					}
				}
			}
		}
	}
	return sample
}
