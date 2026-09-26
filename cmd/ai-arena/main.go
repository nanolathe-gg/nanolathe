// Command ai-arena plays displayless computer-versus-computer skirmishes to
// compare Modern AI prototypes (internal/aikit) with each other and with the
// retail planner.
//
//	ai-arena match -map "Painted Desert" -seed 3 -p retail -p scripted:hard:1 -out r.json
//	ai-arena tournament -spec spec.json -out DIR -jobs 3
//
// A player is brain[:persona[:side[:params]]]; brain "retail" is the retail
// planner. Matches run in separate processes so a tournament uses every core
// without sharing any session state.
//
// The battle seed is headless.ArenaMapSeed(seed, map) unless -raw-seed (or
// a spec's "raw_seeds") asks for the seed itself: a brain's style and jitter
// draws depend on the battle seed and slot, so an unmixed seed list replays
// the same draws on every map. The evaluation protocol is
// docs/MODERN_AI_RESEARCH.md §5.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"

	_ "github.com/nanolathe-gg/nanolathe/mods"
	aikitmod "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ai-arena match|tournament|maps ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "match":
		err = runMatch(os.Args[2:])
	case "tournament":
		err = runTournament(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type playerFlags []string

func (p *playerFlags) String() string     { return strings.Join(*p, ",") }
func (p *playerFlags) Set(v string) error { *p = append(*p, v); return nil }

// parsePlayer reads brain[:persona[:side[:k=v,k=v]]].
func parsePlayer(spec string, slot int) (headless.ArenaPlayer, error) {
	parts := strings.SplitN(spec, ":", 4)
	brainName := parts[0]
	personaName := "medium"
	side := slot & 1
	params := map[string]string{}
	if len(parts) > 1 && parts[1] != "" {
		personaName = parts[1]
	}
	if len(parts) > 2 && parts[2] != "" {
		v, err := strconv.Atoi(parts[2])
		if err != nil {
			return headless.ArenaPlayer{}, fmt.Errorf("player %q: bad side", spec)
		}
		side = v
	}
	if len(parts) > 3 && parts[3] != "" {
		for _, kv := range strings.Split(parts[3], ",") {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return headless.ArenaPlayer{}, fmt.Errorf("player %q: bad param %q", spec, kv)
			}
			params[k] = v
		}
	}
	persona, ok := aikit.PersonaByName(personaName)
	if !ok {
		return headless.ArenaPlayer{}, fmt.Errorf("player %q: unknown persona %q", spec, personaName)
	}
	if v, ok := params["async"]; ok {
		persona.Async = v == "1" || v == "true"
	}
	if v, ok := params["omniscient"]; ok {
		persona.Omniscient = v == "1" || v == "true"
	}
	if v, ok := params["think"]; ok {
		n, _ := strconv.Atoi(v)
		persona.ThinkEvery = uint32(n)
	}
	if v, ok := params["react"]; ok {
		n, _ := strconv.Atoi(v)
		persona.Reaction = uint32(n)
	}
	if v, ok := params["apm"]; ok {
		n, _ := strconv.Atoi(v)
		persona.APM = int32(n)
	}
	if v, ok := params["burst"]; ok {
		n, _ := strconv.Atoi(v)
		persona.Burst = int32(n)
	}
	if v, ok := params["attention"]; ok {
		n, _ := strconv.Atoi(v)
		persona.Attention = int32(n)
	}
	if v, ok := params["skill"]; ok {
		n, _ := strconv.Atoi(v)
		persona.Skill = int32(n)
	}
	if v, ok := params["ambition"]; ok {
		n, _ := strconv.Atoi(v)
		persona.Ambition = int32(max(n, 1)) // 0 would mean unset (full plan)
	}
	brain, err := newBrain(brainName, params)
	if err != nil {
		return headless.ArenaPlayer{}, err
	}
	label := brainName
	if brain != nil {
		label = brainName + "/" + persona.Name
	}
	if l, ok := params["label"]; ok {
		label = l
	}
	return headless.ArenaPlayer{Label: label, Brain: brain, Persona: persona, Side: side}, nil
}

func runMatch(args []string) error {
	fs := flag.NewFlagSet("match", flag.ContinueOnError)
	var players playerFlags
	mapName := fs.String("map", "", "map name")
	seed := fs.Uint("seed", 1, "tournament seed; the battle plays a map-mixed derivation of it (see -raw-seed)")
	rawSeed := fs.Bool("raw-seed", false, "play -seed itself as the battle seed, without mixing in the map name (reproduces runs made before map-mixed seeds)")
	ticks := fs.Uint("ticks", 30*60*20, "tick limit")
	score := fs.String("score", "default", "score a timeout is adjudicated on: default (army + economy + killed - lost/2) or invested (also finished builders and nanoframes)")
	starts := fs.String("starts", "slot", "start positions: slot (slot i at start i), random (the retail randomized assignment from the CRT seed), swap (two players: reversed)")
	out := fs.String("out", "", "result JSON path (default stdout)")
	tracePath := fs.String("trace", "", "write replay trace JSON here")
	traceEvery := fs.Uint("trace-every", 30, "trace frame interval (ticks)")
	explainEvery := fs.Uint("explain-every", 150, "explain interval (ticks)")
	allocs := fs.Bool("allocs", false, "measure per-think allocations (use synchronous personas)")
	level := fs.String("level", "hard", "the battle's difficulty word: easy, medium or hard (the retail planner's plan gates; with -income retail, every computer player's income)")
	income := fs.String("income", "full", "computer income: full (every computer player credited in full) or retail (the 3.1 difficulty discount on every computer player)")
	metal := fs.Int("metal", 0, "starting metal (default skirmish 1000)")
	energy := fs.Int("energy", 0, "starting energy (default skirmish 1000)")
	root := fs.String("root", "", "content root")
	cpuProfile := fs.String("cpuprofile", "", "write a CPU profile of the match")
	pace := fs.Int("pace", 0, "pace to this many ticks per wall second (30 = real time)")
	memProfile := fs.String("memprofile", "", "write an allocation profile after the match")
	publish := fs.Bool("publish", false, "keep the session's frame publication (the arena drops it by default: same game, less time; docs/MODERN_AI_RESEARCH.md §5.3)")
	adjudicate := fs.String("adjudicate", "", "end the match early as a win for a clear leader: ratio,minutes,from (e.g. 2,3,10: twice the other's score at every sample of 3 minutes, from minute 10); empty plays to the limit")
	fs.Var(&players, "p", "player spec brain[:persona[:side[:k=v,...]]] (repeat)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(players) < 2 {
		return fmt.Errorf("need at least two -p players")
	}
	req := headless.ArenaRequest{Map: *mapName, Seed: uint32(*seed), MapSeed: !*rawSeed, Starts: headless.ArenaStarts(*starts), Score: headless.ArenaScore(*score), TickLimit: uint32(*ticks), Gameplay: gameplay.Mode(aikitmod.ArenaSet), StartMetal: *metal, StartEnergy: *energy, Log: os.Stderr}
	if *root != "" {
		req.Root = *root
	}
	req.Level = headless.ArenaLevel(*level)
	switch *income {
	case "full":
	case "retail":
		req.Gameplay = gameplay.Mode(aikitmod.ArenaRetailIncomeSet)
	default:
		return fmt.Errorf("unknown -income %q (have full, retail)", *income)
	}
	req.PaceTPS = *pace
	for i, spec := range players {
		p, err := parsePlayer(spec, i)
		if err != nil {
			return err
		}
		req.Players = append(req.Players, p)
	}
	if *tracePath != "" {
		req.TraceEvery = uint32(*traceEvery)
		req.ExplainEvery = uint32(*explainEvery)
	}
	req.MeasureAllocs = *allocs
	req.Publish = *publish
	adj, err := parseAdjudicate(*adjudicate)
	if err != nil {
		return err
	}
	req.Adjudicate = adj
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}
	res, err := headless.RunArenaMatch(req)
	if err != nil {
		return err
	}
	res.Host.ProcessCPUSeconds = processCPUSeconds()
	if *memProfile != "" {
		f, err := os.Create(*memProfile)
		if err != nil {
			return err
		}
		pprof.Lookup("allocs").WriteTo(f, 0)
		f.Close()
	}
	if *tracePath != "" && res.Trace != nil {
		if err := writeJSON(*tracePath, res.Trace); err != nil {
			return err
		}
	}
	if *out == "" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", " ")
		return enc.Encode(res)
	}
	return writeJSON(*out, res)
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(v); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
