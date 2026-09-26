package headless

import (
	"fmt"
	"hash/fnv"
	"io"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ArenaPlayer is one contestant. A nil Brain plays the bound retail step.
type ArenaPlayer struct {
	Label   string
	Brain   aikit.Brain
	Persona aikit.Persona
	Side    int // 0 ARM, 1 CORE
}

// ArenaRequest describes one displayless AI-versus-AI match.
type ArenaRequest struct {
	Root     string
	Roots    []string
	Map      string
	Gameplay gameplay.Mode // a registered set whose planner is aikit.HostPlanner
	Seed     uint32
	// MapSeed plays the battle on ArenaMapSeed(Seed, Map) instead of Seed.
	// A Modern brain draws its style and jitter from the battle seed and its
	// slot, so a tournament that reuses one seed list on every map would
	// otherwise sample the same few draws on every map. False plays Seed
	// itself, which reproduces runs made before the derivation existed.
	MapSeed   bool
	TickLimit uint32
	Players   []ArenaPlayer
	// Starts selects which of the map's start positions each slot takes, so
	// start asymmetry can be separated from slot order.
	Starts ArenaStarts
	// Score selects the score a timeout is adjudicated on (default
	// ScoreDefault). Both scores are recorded either way.
	Score ArenaScore
	// SampleEvery is the metric sample interval in ticks (default 150).
	SampleEvery uint32
	// TraceEvery > 0 records unit positions every that many ticks and each
	// brain's Explain every ExplainEvery ticks, for the replay viewer.
	TraceEvery   uint32
	ExplainEvery uint32
	// Level is the battle's difficulty word (default hard). The retail
	// planner's plan gates read it, and under a set with the retail income
	// discount (-income retail) so does every computer player's income; a
	// Modern brain's persona is set by its player spec, not by the level.
	Level       ArenaLevel
	StartMetal  int
	StartEnergy int
	Log         io.Writer
	// PaceTPS > 0 paces the match to that many ticks per wall second (30 is
	// real time), so asynchronous thinking is measured with real reaction
	// windows. Host-side sleep only; the simulation never reads a clock.
	PaceTPS int
	// MeasureAllocs turns on per-think allocation accounting. The runtime's
	// counters are process-wide, so the figures mean something only for
	// synchronous personas and one match per process.
	MeasureAllocs bool
	// Publish keeps the session's committed-frame publication. The arena has
	// no presentation consumer, and nothing authoritative reads a published
	// frame [I6], so by default a match drops the frame buffer after
	// composition and the session skips publication: the same game in a
	// little over half the simulation time (docs/MODERN_AI_RESEARCH.md §5.3).
	// Publish restores it, to check that the two still agree.
	Publish bool
	// Adjudicate ends a match early once its winner is clear. The zero value
	// plays every match to a commander kill or the tick limit.
	Adjudicate ArenaAdjudication
}

// ArenaAdjudication ends a match early, as a win for the leader (reason
// "adjudicated"), once one live player's score has been at least RatioPct/100
// times every other live player's at every metric sample of the last Window
// ticks, at a tick no earlier than From. Scores are the request's score kind
// read from the samples; a score below zero counts as zero and the leader's
// must be positive. The rule and its validation against full-length games
// are docs/MODERN_AI_RESEARCH.md §5.3.
type ArenaAdjudication struct {
	RatioPct uint32 `json:"ratio_pct"` // 200: twice the runner-up
	Window   uint32 `json:"window"`    // ticks the lead must hold
	From     uint32 `json:"from"`      // earliest tick
}

// adjudicator follows one match's lead from sample to sample.
type adjudicator struct {
	rule   ArenaAdjudication
	leader int    // the player holding the lead, -1 for none
	since  uint32 // the first sample of the current lead
}

// observe reads one sample tick's scores (index by player; dead players are
// ignored) and returns the adjudicated winner, or -1 to play on.
func (a *adjudicator) observe(tick uint32, scores []int64, alive []bool) int {
	lead, best, second := -1, int64(0), int64(0)
	for i, s := range scores {
		if !alive[i] {
			continue
		}
		s = max(s, 0)
		switch {
		case lead < 0 || s > best:
			second = max(second, best)
			lead, best = i, s
		case s > second:
			second = s
		}
	}
	// A lone survivor also counts as leading; the decisive check ends such a
	// match first.
	if lead < 0 || best <= 0 || best*100 < second*int64(a.rule.RatioPct) {
		a.leader = -1
		return -1
	}
	if lead != a.leader {
		a.leader, a.since = lead, tick
	}
	if tick >= a.rule.From && tick-a.since >= a.rule.Window {
		return lead
	}
	return -1
}

// sampleScore is a sample's score of the given kind, as the tick limit scores
// a survivor.
func sampleScore(s ArenaSample, kind ArenaScore) int64 {
	v := int64(s.ArmyValue) + int64(s.EcoValue) + s.ValueKilled - s.ValueLost/2
	if kind == ScoreInvested {
		v += int64(s.BuilderValue) + int64(s.FrameValue)
	}
	return v
}

// ArenaStarts selects how the arena's slots take the map's start positions.
// Slot order is not neutral on its own (think stagger, the order players are
// stepped, the per-slot random stream), so a tournament that always puts slot
// i at start i cannot tell a start advantage from a slot advantage.
type ArenaStarts string

const (
	// StartsSlot is identity placement: slot i takes the start position
	// whose stored number is i (the lobby's fixed-location setting).
	StartsSlot ArenaStarts = "slot"
	// StartsRandom is the retail randomized assignment, drawn from the
	// battle's CRT seed [08 "Randomization for skirmish starts"]: with two
	// players, a coin decides whether they exchange starts, so across seeds
	// the start is independent of the slot. The result records who got which.
	StartsRandom ArenaStarts = "random"
	// StartsSwap (two players only) puts slot 0 at start 1 and slot 1 at
	// start 0. The session offers no explicit assignment, so the arena plays
	// the randomized assignment with the first CRT seed, counting up from the
	// battle seed, whose coin exchanges the two starts, and checks the
	// placement before the first tick. The simulation seed is unchanged.
	StartsSwap ArenaStarts = "swap"
)

// ArenaLevel is the battle's difficulty word by name.
type ArenaLevel string

const (
	LevelEasy   ArenaLevel = "easy"
	LevelMedium ArenaLevel = "medium"
	LevelHard   ArenaLevel = "hard"
)

// word is the difficulty word the level names: 0 easy, 1 medium, 2 hard
// (the default).
func (l ArenaLevel) word() (int, error) {
	switch l {
	case "", LevelHard:
		return 2, nil
	case LevelMedium:
		return 1, nil
	case LevelEasy:
		return 0, nil
	}
	return 0, fmt.Errorf("arena: unknown level %q (have easy, medium, hard)", l)
}

// startSwapSeed returns the first CRT seed from seed upward whose retail
// randomized start assignment exchanges two players' starts. With fewer
// than three eligible slots that assignment first draws a CRT gate,
// (rand·2)/0x8000, and runs its single exchange only when the gate is 1
// [08 "Randomization for skirmish starts"].
func startSwapSeed(seed uint32) (uint32, error) {
	for k := uint32(0); k < 1<<20; k++ {
		c := rng.NewCRT(seed + k)
		if int(c.Rand())*2/0x8000 != 0 {
			return seed + k, nil
		}
	}
	return 0, fmt.Errorf("arena: no CRT seed within 2^20 of %d exchanges the starts", seed)
}

// ArenaScore names the score a game that reaches its tick limit is
// adjudicated on: the higher score wins on points when it is at least 1.3
// times the other, otherwise the game is a draw. Formulas are in
// docs/MODERN_AI_RESEARCH.md §5.
type ArenaScore string

const (
	// ScoreDefault is army value + economy value + value killed − value
	// lost / 2, from the last sample. Builders, the commander, nanoframes and
	// stock count nothing.
	ScoreDefault ArenaScore = "default"
	// ScoreInvested adds the value of finished builders other than the
	// commander and the built fraction of every nanoframe to ScoreDefault:
	// value invested, not only value fielded, for tests (such as a growth
	// switch) whose treatment spends on constructors and unfinished work.
	// Stock and the commander still count nothing.
	ScoreInvested ArenaScore = "invested"
)

// ArenaSample is one row of a player's time series.
type ArenaSample struct {
	Tick         uint32 `json:"t"`
	MetalIncome  int32  `json:"mi"`
	EnergyIncome int32  `json:"ei"`
	MetalStock   int32  `json:"ms"`
	EnergyStock  int32  `json:"es"`
	MetalWaste   int32  `json:"mw"` // cumulative overflow
	ArmyValue    int32  `json:"av"`
	EcoValue     int32  `json:"ev"`
	Units        int32  `json:"u"`
	Builders     int32  `json:"b"`
	IdleBuilders int32  `json:"ib"`
	Factories    int32  `json:"f"`
	IdleFactory  int32  `json:"if"`
	ValueLost    int64  `json:"vl"`
	ValueKilled  int64  `json:"vk"`
	Extractors   int32  `json:"mx"`
	// BuilderValue is the value of finished builders other than the
	// commander; FrameValue is the built fraction of every nanoframe's value.
	// Only ScoreInvested counts them.
	BuilderValue int32 `json:"bv"`
	FrameValue   int32 `json:"nv"`
	// Trapped counts ground combat units that have stayed within
	// trapRadius of where they were first seen for at least trapAge ticks
	// while their current order's goal lies beyond trapRadius: a unit told
	// to go somewhere that never leaves is boxed in by buildings, features
	// or wrecks. Units the army holds at home (no far goal) do not count.
	Trapped int32 `json:"tr"`
}

// ArenaCost is the measured AI cost for one player.
type ArenaCost struct {
	Steps          int     `json:"steps"`
	StepMeanUS     float64 `json:"step_mean_us"`
	StepP99US      float64 `json:"step_p99_us"`
	StepMaxUS      float64 `json:"step_max_us"`
	Thinks         int     `json:"thinks"`
	ThinkMeanUS    float64 `json:"think_mean_us"`
	ThinkP99US     float64 `json:"think_p99_us"`
	ThinkMaxUS     float64 `json:"think_max_us"`
	ThinkAllocB    float64 `json:"think_alloc_bytes_mean"`
	ThinkAllocObjs float64 `json:"think_alloc_objs_mean"`
	// StepAllocB and StepAllocObjs are the mean allocations of a whole host
	// step on the simulation thread (the observation, the command apply, a
	// synchronous think and the engine upkeep), measured like the think's.
	StepAllocB    float64 `json:"step_alloc_bytes_mean,omitempty"`
	StepAllocObjs float64 `json:"step_alloc_objs_mean,omitempty"`
	// Parts attributes the step's simulation-thread time (aikit.PartProbe),
	// one entry per part that ran.
	Parts []ArenaPartCost `json:"parts,omitempty"`
	// JoinWaits counts the joins that found the worker still busy and the
	// simulation thread waited for it (its time is the "join" part). An
	// unpaced match (PaceTPS 0) reaches a deadline far sooner than real time,
	// so only a paced match measures what a player would wait.
	JoinWaits int `json:"join_waits"`
	// PrepUS is the preparation's time on its worker (the map analysis and
	// the brain's Init), which must finish before the first batch is due.
	PrepUS float64 `json:"prep_us,omitempty"`
}

// ArenaPartCost is one part of the host step (aikit.StepPart): the steps
// that ran it and its time per occurrence.
type ArenaPartCost struct {
	Part   string  `json:"part"`
	Count  int     `json:"n"`
	MeanUS float64 `json:"mean_us"`
	P99US  float64 `json:"p99_us"`
	MaxUS  float64 `json:"max_us"`
}

// ArenaHostTiming is the match's host-side cost: whole ticks, the AI's share
// of each tick and the collector. Every field is a measurement of the host,
// never of the game, so two runs of one game differ here and nowhere else.
type ArenaHostTiming struct {
	TickMaxUS float64 `json:"tick_max_us"`
	// AITick* summarize, per tick, the sum of every player's host step: the
	// simulation-thread time the computer players take in that tick.
	AITickMeanUS float64 `json:"ai_tick_mean_us"`
	AITickP99US  float64 `json:"ai_tick_p99_us"`
	AITickMaxUS  float64 `json:"ai_tick_max_us"`
	// AITicksOver1ms and AITicksOver4ms count the ticks whose AI sum exceeded
	// 1 ms and 4 ms.
	AITicksOver1ms int     `json:"ai_ticks_over_1ms"`
	AITicksOver4ms int     `json:"ai_ticks_over_4ms"`
	GC             ArenaGC `json:"gc"`
	// ProcessCPUSeconds is the match process's user and system CPU time,
	// content loading included, when the host fills it in (ai-arena does):
	// what the game would take on a core of its own.
	ProcessCPUSeconds float64 `json:"process_cpu_seconds,omitempty"`
	// Ticks and AITicks are the per-tick series (µs×10) the figures above
	// summarize, for host tools that compare distributions (a per-tick
	// minimum over repeated runs of one deterministic match removes most of
	// what a loaded host adds). Not written to the result file.
	Ticks   []int32 `json:"-"`
	AITicks []int32 `json:"-"`
	// PartTicks is AITicks split by the part of the step (aikit.StepPart),
	// summed over the players; empty for steps without a part probe.
	PartTicks [aikit.NumStepParts][]int32 `json:"-"`
}

// ArenaGC is the process's collector activity over the match: deltas between
// two reads taken as the match starts and ends. The counters are
// process-wide, so they describe one match only with one match per process.
type ArenaGC struct {
	Cycles       uint32  `json:"cycles"`
	PauseTotalUS float64 `json:"pause_total_us"`
	PauseMaxUS   float64 `json:"pause_max_us"`
	AllocMB      float64 `json:"alloc_mb"`
	AllocObjects uint64  `json:"alloc_objects"`
	CPUSeconds   float64 `json:"cpu_seconds"`
}

// ArenaPlayerResult is one contestant's outcome.
type ArenaPlayerResult struct {
	Slot    int    `json:"slot"`
	Label   string `json:"label"`
	Brain   string `json:"brain"`
	Persona string `json:"persona"`
	Side    int    `json:"side"`
	// Start is the stored number of the start position the slot took (0 is
	// the map's StartPos1), or -1 when the commander stood on none.
	Start    int    `json:"start"`
	Alive    bool   `json:"alive"`
	DiedTick uint32 `json:"died_tick,omitempty"`
	// Score is the score the game was adjudicated on (ArenaResult.ScoreKind);
	// ScoreDefault and ScoreInvested are both variants. All three are -1 for
	// a player that did not survive.
	Score         int64            `json:"score"`
	ScoreDefault  int64            `json:"score_default"`
	ScoreInvested int64            `json:"score_invested"`
	Kills         int32            `json:"kills"`
	Losses        int32            `json:"losses"`
	ValueKilled   int64            `json:"value_killed"`
	ValueLost     int64            `json:"value_lost"`
	Commands      aikit.ApplyStats `json:"commands"`
	Cost          ArenaCost        `json:"cost"`
	Series        []ArenaSample    `json:"series"`
	// FirstAttack is the tick this player first destroyed a finished unit
	// of another player (credited as in ValueKilled, to the side that
	// damaged it last): the human benchmark's "first kill" milestone, for
	// every brain alike. Absent when it never did.
	FirstAttack uint32             `json:"first_attack_tick,omitempty"`
	Built       map[string]int     `json:"built,omitempty"`
	RoleBuilt   map[string]int     `json:"role_built,omitempty"`
	Extra       map[string]float64 `json:"extra,omitempty"`
	// LostByClass is the finished value lost by unit class (the replay
	// trace's role classes).
	LostByClass map[string]int64 `json:"lost_by_class,omitempty"`
	// TrappedMax and TrappedMean summarize ArenaSample.Trapped.
	TrappedMax  int32   `json:"trapped_max"`
	TrappedMean float64 `json:"trapped_mean"`
}

// ArenaResult is the outcome of one match.
type ArenaResult struct {
	Map  string `json:"map"`
	Seed uint32 `json:"seed"` // the requested seed
	// BattleSeed is the seed both battle streams started from: Seed, or
	// ArenaMapSeed(Seed, Map) when the request asked for map mixing.
	BattleSeed uint32 `json:"battle_seed"`
	// CRTSeed is the CRT stream's seed: the battle seed, except under
	// StartsSwap. Starts is the requested start assignment.
	CRTSeed     uint32              `json:"crt_seed"`
	Starts      ArenaStarts         `json:"starts"`
	ScoreKind   ArenaScore          `json:"score_kind"`
	Gameplay    string              `json:"gameplay"`
	Level       ArenaLevel          `json:"level,omitempty"`
	RuleSet     string              `json:"rule_set,omitempty"` // the registered set the request named
	Ticks       uint32              `json:"ticks"`
	Winner      int                 `json:"winner"` // player index in Players, -1 draw
	Reason      string              `json:"reason"` // decisive, points, timeout or adjudicated
	WallSeconds float64             `json:"wall_seconds"`
	TickMeanUS  float64             `json:"tick_mean_us"`
	TickP99US   float64             `json:"tick_p99_us"`
	Host        ArenaHostTiming     `json:"host_timing"`
	Players     []ArenaPlayerResult `json:"players"`
	Trace       *ArenaTrace         `json:"-"`
	// Adjudication is the early-end rule the match played under, if any.
	Adjudication *ArenaAdjudication `json:"adjudication,omitempty"`
}

// ArenaTrace is replay data for the viewer.
type ArenaTrace struct {
	Map      string          `json:"map"`
	WorldW   int32           `json:"world_w"`
	WorldH   int32           `json:"world_h"`
	SectorW  int32           `json:"sector_w"`
	SectorH  int32           `json:"sector_h"`
	Labels   []string        `json:"labels"`
	Frames   []ArenaFrame    `json:"frames"`
	Explains []ArenaExplain  `json:"explains"`
	Spots    [][3]int32      `json:"spots"`
	Starts   [][2]int32      `json:"starts"`
	Events   []ArenaEvent    `json:"events"`
	Series   [][]ArenaSample `json:"series"`
}

// ArenaFrame packs every live unit as [player, role, x, z, hp%, built].
type ArenaFrame struct {
	Tick  uint32     `json:"t"`
	Units [][6]int32 `json:"u"`
}

// ArenaExplain is one brain self-description.
type ArenaExplain struct {
	Tick   uint32        `json:"t"`
	Player int           `json:"p"`
	X      aikit.Explain `json:"x"`
}

// ArenaEvent is a notable moment (death of a commander, first attack).
type ArenaEvent struct {
	Tick   uint32 `json:"t"`
	Player int    `json:"p"`
	Kind   string `json:"k"`
	X      int32  `json:"x"`
	Z      int32  `json:"z"`
}

// Role codes for trace frames.
const (
	traceCommander = iota
	traceBuilder
	traceFactory
	traceEco
	traceDefense
	traceGround
	traceAir
	traceNaval
	traceOther
)

// traceRoleNames names the trace role classes in constant order.
var traceRoleNames = [traceOther + 1]string{"commander", "builder", "factory", "eco", "defense", "ground", "air", "naval", "other"}

func traceRole(info *aikit.UnitInfo) int32 {
	switch {
	case info == nil:
		return traceOther
	case info.Role.Has(aikit.RoleCommander):
		return traceCommander
	case info.Role.Has(aikit.RoleBuilder):
		return traceBuilder
	case info.Role.Has(aikit.RoleFactory):
		return traceFactory
	case info.Role.Any(aikit.RoleExtractor | aikit.RoleEnergy | aikit.RoleMetalMaker | aikit.RoleStorage):
		return traceEco
	case info.Role.Has(aikit.RoleDefense):
		return traceDefense
	case info.Role.Has(aikit.RoleAir):
		return traceAir
	case info.Role.Has(aikit.RoleNaval):
		return traceNaval
	case info.Role.Has(aikit.RoleCombat):
		return traceGround
	}
	return traceOther
}

// arenaProbe times host steps and thinks. It is host-side and never
// reached by the simulation's own decisions [I6]. One probe serves one
// match, and every field is indexed by player: an asynchronous persona calls
// ThinkBegin/ThinkEnd on its own worker goroutine, and the preparation calls
// PrepBegin/PrepEnd on it, so two players never share a buffer (the metric
// read buffers included) and a worker's fields are never the simulation
// thread's.
type arenaProbe struct {
	stepStart  [10]time.Time
	thinkStart [10]time.Time
	steps      [10][]int32 // µs×10
	thinks     [10][]int32
	allocB     [10]uint64
	allocO     [10]uint64
	allocStart [10][2]uint64
	samples    [10][2]metrics.Sample
	measure    bool

	// Simulation thread: step allocations, parts, and the tick's AI sum.
	stepAllocB     [10]uint64
	stepAllocO     [10]uint64
	stepAllocStart [10][2]uint64
	stepSamples    [10][2]metrics.Sample
	partMark       [10]time.Time
	parts          [10][aikit.NumStepParts][]int32 // µs×10 per occurrence
	stepPart       [10][aikit.NumStepParts]int32   // this step's parts (µs×10), until StepEnd
	stepRan        [10][aikit.NumStepParts]bool
	tickAI         int64                       // ns, this tick's steps
	tickParts      [aikit.NumStepParts]int64   // ns, this tick's parts
	partTicks      [aikit.NumStepParts][]int32 // µs×10 per tick, every player's part
	// Worker: the preparation.
	prepStart [10]time.Time
	prepNS    [10]int64
}

func newArenaProbe(measure bool) *arenaProbe {
	p := &arenaProbe{measure: measure}
	for i := range p.samples {
		p.samples[i] = [2]metrics.Sample{{Name: "/gc/heap/allocs:bytes"}, {Name: "/gc/heap/allocs:objects"}}
		p.stepSamples[i] = [2]metrics.Sample{{Name: "/gc/heap/allocs:bytes"}, {Name: "/gc/heap/allocs:objects"}}
	}
	return p
}

// allocs reads the process-wide allocation counters into buf.
// The counters are process-wide, so a measurement is only meaningful with a
// synchronous persona and one match per process.
func allocs(buf *[2]metrics.Sample) (uint64, uint64) {
	s := buf[:]
	metrics.Read(s)
	return s[0].Value.Uint64(), s[1].Value.Uint64()
}

func (p *arenaProbe) StepBegin(player uint8, tick uint32) {
	if p.measure {
		p.stepAllocStart[player][0], p.stepAllocStart[player][1] = allocs(&p.stepSamples[player])
	}
	now := time.Now()
	p.stepStart[player] = now
	p.partMark[player] = now
}
func (p *arenaProbe) StepEnd(player uint8, tick uint32) {
	d := time.Since(p.stepStart[player]).Nanoseconds()
	// The step's allocations are read before the probe's own bookkeeping
	// (the series appends) allocates.
	if p.measure {
		b, o := allocs(&p.stepSamples[player])
		p.stepAllocB[player] += b - p.stepAllocStart[player][0]
		p.stepAllocO[player] += o - p.stepAllocStart[player][1]
	}
	p.steps[player] = append(p.steps[player], int32(d/100))
	p.tickAI += d
	for part, ran := range p.stepRan[player] {
		if ran {
			p.parts[player][part] = append(p.parts[player][part], p.stepPart[player][part])
			p.stepRan[player][part] = false
		}
	}
}
func (p *arenaProbe) ThinkBegin(player uint8, tick uint32) {
	if p.measure {
		p.allocStart[player][0], p.allocStart[player][1] = allocs(&p.samples[player])
	}
	p.thinkStart[player] = time.Now()
}
func (p *arenaProbe) ThinkEnd(player uint8, tick uint32) {
	p.thinks[player] = append(p.thinks[player], int32(time.Since(p.thinkStart[player]).Nanoseconds()/100))
	if p.measure {
		b, o := allocs(&p.samples[player])
		p.allocB[player] += b - p.allocStart[player][0]
		p.allocO[player] += o - p.allocStart[player][1]
	}
}

// Part implements aikit.PartProbe.
func (p *arenaProbe) Part(player uint8, tick uint32, part aikit.StepPart) {
	now := time.Now()
	d := now.Sub(p.partMark[player]).Nanoseconds()
	// Kept until StepEnd, which appends it to the series outside the
	// step's allocation window.
	p.stepPart[player][part], p.stepRan[player][part] = int32(d/100), true
	p.tickParts[part] += d
	p.partMark[player] = now
}

// PrepBegin implements aikit.PartProbe.
func (p *arenaProbe) PrepBegin(player uint8, tick uint32) { p.prepStart[player] = time.Now() }

// PrepEnd implements aikit.PartProbe.
func (p *arenaProbe) PrepEnd(player uint8, tick uint32) {
	p.prepNS[player] = time.Since(p.prepStart[player]).Nanoseconds()
}

// endTick closes the tick's AI sum and returns it (µs×10).
func (p *arenaProbe) endTick() int32 {
	v := int32(p.tickAI / 100)
	p.tickAI = 0
	for i := range p.tickParts {
		p.partTicks[i] = append(p.partTicks[i], int32(p.tickParts[i]/100))
		p.tickParts[i] = 0
	}
	return v
}

var stepPartNames = [aikit.NumStepParts]string{"begin", "join", "apply", "observe", "think", "upkeep"}

// gcRead is one read of the collector's counters.
type gcRead struct {
	ms  runtime.MemStats
	cpu [1]metrics.Sample
}

func (g *gcRead) read() {
	runtime.ReadMemStats(&g.ms)
	g.cpu[0].Name = "/cpu/classes/gc/total:cpu-seconds"
	metrics.Read(g.cpu[:])
}

// gcDelta is the collector's activity between two reads.
func gcDelta(a, b *gcRead) ArenaGC {
	out := ArenaGC{
		Cycles:       b.ms.NumGC - a.ms.NumGC,
		PauseTotalUS: float64(b.ms.PauseTotalNs-a.ms.PauseTotalNs) / 1000,
		AllocMB:      float64(b.ms.TotalAlloc-a.ms.TotalAlloc) / (1 << 20),
		AllocObjects: b.ms.Mallocs - a.ms.Mallocs,
	}
	if a.cpu[0].Value.Kind() == metrics.KindFloat64 && b.cpu[0].Value.Kind() == metrics.KindFloat64 {
		out.CPUSeconds = b.cpu[0].Value.Float64() - a.cpu[0].Value.Float64()
	}
	// The pause ring holds the last 256 cycles.
	n := min(out.Cycles, uint32(len(b.ms.PauseNs)))
	for i := uint32(0); i < n; i++ {
		v := float64(b.ms.PauseNs[(b.ms.NumGC-1-i)%uint32(len(b.ms.PauseNs))]) / 1000
		out.PauseMaxUS = max(out.PauseMaxUS, v)
	}
	return out
}

func percentiles(v []int32) (mean, p99, max float64) {
	if len(v) == 0 {
		return 0, 0, 0
	}
	s := append([]int32(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var sum int64
	for _, x := range s {
		sum += int64(x)
	}
	mean = float64(sum) / float64(len(s)) / 10
	p99 = float64(s[(len(s)-1)*99/100]) / 10
	max = float64(s[len(s)-1]) / 10
	return
}

// arenaConfig lays out an AI-only skirmish: every contestant is a computer
// player in its own ally group. The lobby's "at least one human" rule is a
// front-end rule; the arena records the first contestant as the local slot.
func arenaConfig(req ArenaRequest) session.SkirmishConfig {
	cfg := session.SkirmishConfig{MapName: req.Map, NumPlayers: len(req.Players)}
	for i := range cfg.Players {
		cfg.Players[i] = session.SkirmishPlayer{}
	}
	metal, energy := req.StartMetal, req.StartEnergy
	if metal == 0 {
		metal = session.SkirmishDefaultMetal
	}
	if energy == 0 {
		energy = session.SkirmishDefaultEnergy
	}
	for i, p := range req.Players {
		cfg.Players[i].Controller = session.SkirmishControllerComputer
		cfg.Players[i].AllyGroup = i
		cfg.Players[i].Color = i
		cfg.Players[i].Side = p.Side
		cfg.Players[i].Metal = metal
		cfg.Players[i].Energy = energy
	}
	cfg.Location = 1
	cfg.ApplyDefaults()
	for i, p := range req.Players {
		cfg.Players[i].Side = p.Side
	}
	return cfg
}

// ArenaMapSeed mixes a map name into a tournament seed, so one seed list
// samples different random draws on every map. The map name is trimmed and
// lower-cased (so "Great Divide" and "great divide" agree), hashed with 32-bit
// FNV-1a, and placed in the low word under the seed in the high word; the
// 64-bit value is finished with the SplitMix64 output function and its low 32
// bits are the battle seed. The derivation is part of the evaluation
// protocol (docs/MODERN_AI_RESEARCH.md §5): changing it changes every
// recorded game.
func ArenaMapSeed(seed uint32, mapName string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(mapName))))
	z := uint64(seed)<<32 | uint64(h.Sum32())
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return uint32(z ^ (z >> 31))
}

// battleSeed is the seed the request's battle streams start from.
func (req ArenaRequest) battleSeed() uint32 {
	if req.MapSeed {
		return ArenaMapSeed(req.Seed, req.Map)
	}
	return req.Seed
}

// RunArenaMatch mounts the install, composes the match and plays it out.
func RunArenaMatch(req ArenaRequest) (ArenaResult, error) {
	fs, err := mountContentRoots(req.Root, req.Roots)
	if err != nil {
		return ArenaResult{}, err
	}
	defer fs.Close()
	view, profile, err := contentProfileView(fs, "")
	if err != nil {
		return ArenaResult{}, err
	}
	catalog, err := content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err != nil {
		return ArenaResult{}, diagnostic("catalog compile failed: "+err.Error(), req.Map, fs.ProviderIDs(), "a complete compiled catalog")
	}
	if req.TickLimit == 0 {
		req.TickLimit = 30 * 60 * 25
	}
	if req.SampleEvery == 0 {
		req.SampleEvery = 150
	}
	difficulty, err := req.Level.word()
	if err != nil {
		return ArenaResult{}, err
	}
	cfg := arenaConfig(req)
	seed := req.battleSeed()
	crtSeed := seed
	switch req.Starts {
	case "", StartsSlot:
		req.Starts = StartsSlot
	case StartsRandom:
		cfg.Location = 0
	case StartsSwap:
		if len(req.Players) != 2 {
			return ArenaResult{}, fmt.Errorf("arena: starts %q needs exactly two players, have %d", req.Starts, len(req.Players))
		}
		cfg.Location = 0
		if crtSeed, err = startSwapSeed(seed); err != nil {
			return ArenaResult{}, err
		}
	default:
		return ArenaResult{}, fmt.Errorf("arena: unknown starts %q (have slot, random, swap)", req.Starts)
	}
	switch req.Score {
	case "":
		req.Score = ScoreDefault
	case ScoreDefault, ScoreInvested:
	default:
		return ArenaResult{}, fmt.Errorf("arena: unknown score %q (have default, invested)", req.Score)
	}
	composed, err := ComposeFreshBattle(FreshBattleRequest{
		Gameplay:         req.Gameplay,
		CommunitySources: session.CommunitySources{Content: profile.GameplaySources()},
		Kind:             ScenarioDirectOTA, Map: req.Map, LocalOwner: -1,
		Difficulty: difficulty, Skirmish: cfg,
		SimulationSeed: seed, CRTSeed: crtSeed,
		FS: view, Catalog: catalog, AutomatedPlayers: true,
	})
	if err != nil {
		return ArenaResult{}, err
	}
	if !req.Publish {
		// With no buffer the session skips publication (and the arena its
		// event drain); the simulation never reads a published frame [I6].
		composed.Session.Snapshot = nil
	}
	return playArena(req, composed.Session)
}

type arenaTracker struct {
	lostClass   [10][traceOther + 1]int64 // value lost by trace role
	valueLost   [10]int64
	valueKilled [10]int64
	commander   [10]pool.Handle
	diedTick    [10]uint32
	built       [10]map[string]int
	table       *aikit.Table
	events      []ArenaEvent
	firstAttack [10]uint32
	anchors     map[pool.Handle]trapAnchor
}

// trapAnchor is where a ground combat unit was first seen, and whether it
// has ever left trapRadius of that point.
type trapAnchor struct {
	x, z int32
	tick uint32
	left bool
}

const (
	trapRadius = 320
	trapAge    = 3600 // two minutes
)

func playArena(req ArenaRequest, sess *session.Session) (ArenaResult, error) {
	log := req.Log
	if log == nil {
		log = io.Discard
	}
	n := len(req.Players)
	probe := newArenaProbe(req.MeasureAllocs)
	var drained []frame.EventView
	table := aikit.BuildTable(sess.Catalog, sess.Rules.Construction)
	tr := &arenaTracker{table: table, anchors: map[pool.Handle]trapAnchor{}}
	hosts := make([]*aikit.Host, n)
	for i, p := range req.Players {
		m := sess.AI[i]
		if m == nil {
			return ArenaResult{}, fmt.Errorf("arena: slot %d has no computer manager", i)
		}
		tr.built[i] = map[string]int{}
		if p.Brain == nil {
			m.Ext = &aikit.RetailTimer{Probe: probe}
			continue
		}
		h := aikit.NewHost(m, p.Brain, p.Persona)
		h.Probe = probe
		m.Ext = h
		hosts[i] = h
	}
	// Commanders at entry, and value accounting on death.
	starts := make([]int, n)
	for i := range starts {
		starts[i] = -1
	}
	for _, u := range sess.Units.IterSliced() {
		if u.Def != nil && u.Def.Commander && int(u.Owner) < n {
			tr.commander[u.Owner] = u.Handle
			starts[u.Owner] = arenaStartOf(sess, u)
		}
	}
	if req.Starts == StartsSwap && (starts[0] != 1 || starts[1] != 0) {
		return ArenaResult{}, fmt.Errorf("arena: starts %q placed slots at starts %v, want [1 0]; the session's randomized assignment no longer follows [08 \"Randomization for skirmish starts\"]", req.Starts, starts)
	}
	prevDeath := sess.Units.OnDeath
	sess.Units.OnDeath = func(handle pool.Handle, cause units.DeathCause, u *units.Unit) {
		if u != nil && int(u.Owner) < n && u.Def != nil {
			info := table.Of(u.Def)
			if info != nil && u.Remaining == 0 {
				tr.valueLost[u.Owner] += int64(info.Value)
				tr.lostClass[u.Owner][traceRole(info)] += int64(info.Value)
				killer := u.LastDamageSide
				if int(killer) < n && killer != u.Owner {
					tr.valueKilled[killer] += int64(info.Value)
					if tr.firstAttack[killer] == 0 && sess.Clock != nil {
						tr.firstAttack[killer] = sess.Clock.GlobalTick
					}
				}
			}
			if u.Def.Commander && tr.diedTick[u.Owner] == 0 && sess.Clock != nil {
				tr.diedTick[u.Owner] = sess.Clock.GlobalTick
				tr.events = append(tr.events, ArenaEvent{Tick: sess.Clock.GlobalTick, Player: int(u.Owner), Kind: "commander_death", X: int32(int64(u.X) >> 16), Z: int32(int64(u.Z) >> 16)})
			}
		}
		if prevDeath != nil {
			prevDeath(handle, cause, u)
		}
	}
	prevCreate := sess.Units.OnCreate
	sess.Units.OnCreate = func(handle pool.Handle, u *units.Unit) {
		if u != nil && int(u.Owner) < n && u.Def != nil {
			tr.built[u.Owner][u.Def.UnitName]++
		}
		if prevCreate != nil {
			prevCreate(handle, u)
		}
	}

	result := ArenaResult{Map: req.Map, Seed: req.Seed, BattleSeed: sess.RNGSimSeed, CRTSeed: sess.RNGCrtSeed, Starts: req.Starts, ScoreKind: req.Score, Gameplay: string(sess.Gameplay), Level: req.Level, RuleSet: string(req.Gameplay), Winner: -1}
	var adj *adjudicator
	var adjScores []int64
	var adjAlive []bool
	if req.Adjudicate.RatioPct > 0 {
		rule := req.Adjudicate
		result.Adjudication = &rule
		adj = &adjudicator{rule: rule, leader: -1}
		adjScores, adjAlive = make([]int64, n), make([]bool, n)
	}
	series := make([][]ArenaSample, n)
	var trace *ArenaTrace
	if req.TraceEvery > 0 {
		trace = &ArenaTrace{Map: req.Map, WorldW: sess.World.CellW * 16, WorldH: sess.World.CellH * 16}
		for _, p := range req.Players {
			trace.Labels = append(trace.Labels, p.Label)
		}
	}
	var tickDur, aiTick []int32
	var gc0 gcRead
	gc0.read()
	started := time.Now()
	var walk []*units.Unit
	classesByID := orderClasses()
	var paceNext time.Time
	if req.PaceTPS > 0 {
		paceNext = time.Now()
	}
	for sess.State != session.StatePostBattle && sess.Clock.GlobalTick < req.TickLimit {
		if req.PaceTPS > 0 {
			paceNext = paceNext.Add(time.Second / time.Duration(req.PaceTPS))
			if d := time.Until(paceNext); d > 0 {
				time.Sleep(d)
			}
		}
		t0 := time.Now()
		drained = arenaStep(sess, drained)
		tickDur = append(tickDur, int32(time.Since(t0).Nanoseconds()/100))
		aiTick = append(aiTick, probe.endTick())
		tick := sess.Clock.GlobalTick
		if tick%req.SampleEvery == 0 {
			walk = sess.Units.AppendLiveSliced(walk[:0])
			for i := 0; i < n; i++ {
				series[i] = append(series[i], sampleArena(sess, walk, table, tr, i, tick, classesByID))
			}
		}
		if trace != nil && tick%req.TraceEvery == 0 {
			walk = sess.Units.AppendLiveSliced(walk[:0])
			trace.Frames = append(trace.Frames, traceFrame(walk, table, n, tick))
		}
		if trace != nil && req.ExplainEvery > 0 && tick%req.ExplainEvery == 0 {
			for i, h := range hosts {
				if h == nil {
					continue
				}
				if ex, ok := h.Brain().(aikit.Explainer); ok {
					h.Join()
					var x aikit.Explain
					ex.Explain(&x)
					trace.Explains = append(trace.Explains, ArenaExplain{Tick: tick, Player: i, X: x})
				}
			}
		}
		if tick%30 == 0 {
			alive := 0
			last := -1
			for i := 0; i < n; i++ {
				if arenaAlive(sess, tr, i) {
					alive++
					last = i
				}
			}
			if alive <= 1 {
				result.Winner = last
				result.Reason = "decisive"
				break
			}
		}
		if adj != nil && tick%req.SampleEvery == 0 {
			for i := 0; i < n; i++ {
				adjScores[i] = sampleScore(series[i][len(series[i])-1], req.Score)
				adjAlive[i] = arenaAlive(sess, tr, i)
			}
			if w := adj.observe(tick, adjScores, adjAlive); w >= 0 {
				result.Winner = w
				result.Reason = "adjudicated"
				break
			}
		}
	}
	for _, h := range hosts {
		if h != nil {
			h.Close()
		}
	}
	result.Ticks = sess.Clock.GlobalTick
	result.WallSeconds = time.Since(started).Seconds()
	var gc1 gcRead
	gc1.read()
	result.Host.GC = gcDelta(&gc0, &gc1)
	result.TickMeanUS, result.TickP99US, result.Host.TickMaxUS = percentiles(tickDur)
	result.Host.AITickMeanUS, result.Host.AITickP99US, result.Host.AITickMaxUS = percentiles(aiTick)
	result.Host.Ticks, result.Host.AITicks, result.Host.PartTicks = tickDur, aiTick, probe.partTicks
	for _, v := range aiTick {
		if v > 10000 {
			result.Host.AITicksOver1ms++
		}
		if v > 40000 {
			result.Host.AITicksOver4ms++
		}
	}
	if result.Reason == "" {
		result.Reason = "timeout"
	}
	var best, second int64 = -1, -1
	bestI := -1
	for i, p := range req.Players {
		pr := ArenaPlayerResult{Slot: i, Label: p.Label, Persona: p.Persona.Name, Side: p.Side, Start: starts[i], Series: series[i]}
		pr.Brain = "retail"
		if p.Brain != nil {
			pr.Brain = p.Brain.Name()
		}
		if p.Brain == nil {
			pr.Persona = "retail"
		}
		pr.Alive = arenaAlive(sess, tr, i)
		pr.DiedTick = tr.diedTick[i]
		pr.ValueKilled, pr.ValueLost = tr.valueKilled[i], tr.valueLost[i]
		if i < len(sess.Econ.Players) {
			pr.Kills = int32(sess.Econ.Players[i].Kills)
			pr.Losses = int32(sess.Econ.Players[i].Losses)
		}
		if len(series[i]) > 0 {
			s := series[i][len(series[i])-1]
			pr.ScoreDefault = int64(s.ArmyValue) + int64(s.EcoValue) + pr.ValueKilled - pr.ValueLost/2
			pr.ScoreInvested = pr.ScoreDefault + int64(s.BuilderValue) + int64(s.FrameValue)
		}
		if !pr.Alive {
			pr.ScoreDefault, pr.ScoreInvested = -1, -1
		}
		pr.Score = pr.ScoreDefault
		if req.Score == ScoreInvested {
			pr.Score = pr.ScoreInvested
		}
		if hosts[i] != nil {
			pr.Commands = hosts[i].Stats()
		}
		c := &pr.Cost
		c.Steps = len(probe.steps[i])
		c.StepMeanUS, c.StepP99US, c.StepMaxUS = percentiles(probe.steps[i])
		c.Thinks = len(probe.thinks[i])
		c.ThinkMeanUS, c.ThinkP99US, c.ThinkMaxUS = percentiles(probe.thinks[i])
		if c.Thinks > 0 && probe.measure {
			c.ThinkAllocB = float64(probe.allocB[i]) / float64(c.Thinks)
			c.ThinkAllocObjs = float64(probe.allocO[i]) / float64(c.Thinks)
		}
		if c.Steps > 0 && probe.measure {
			c.StepAllocB = float64(probe.stepAllocB[i]) / float64(c.Steps)
			c.StepAllocObjs = float64(probe.stepAllocO[i]) / float64(c.Steps)
		}
		for part, v := range probe.parts[i] {
			if len(v) == 0 {
				continue
			}
			pc := ArenaPartCost{Part: stepPartNames[part], Count: len(v)}
			pc.MeanUS, pc.P99US, pc.MaxUS = percentiles(v)
			c.Parts = append(c.Parts, pc)
		}
		c.JoinWaits = len(probe.parts[i][aikit.PartJoin])
		c.PrepUS = float64(probe.prepNS[i]) / 1000
		pr.Built = tr.built[i]
		var trappedSum int64
		for _, smp := range series[i] {
			trappedSum += int64(smp.Trapped)
			if smp.Trapped > pr.TrappedMax {
				pr.TrappedMax = smp.Trapped
			}
		}
		if len(series[i]) > 0 {
			pr.TrappedMean = float64(trappedSum) / float64(len(series[i]))
		}
		if hosts[i] != nil {
			if rep, ok := hosts[i].Brain().(aikit.Reporter); ok {
				pr.Extra = map[string]float64{}
				rep.Report(func(name string, v int64) { pr.Extra[name] = float64(v) })
			}
		}
		pr.FirstAttack = tr.firstAttack[i]
		pr.LostByClass = map[string]int64{}
		for k, name := range traceRoleNames {
			if v := tr.lostClass[i][k]; v != 0 {
				pr.LostByClass[name] = v
			}
		}
		result.Players = append(result.Players, pr)
		if pr.Score > best {
			second, best, bestI = best, pr.Score, i
		} else if pr.Score > second {
			second = pr.Score
		}
	}
	if result.Reason == "timeout" && bestI >= 0 {
		// Adjudicate on points only with a clear margin; otherwise a draw.
		if second < 0 || best*10 >= second*13 {
			result.Winner = bestI
			result.Reason = "points"
		}
	}
	if trace != nil {
		trace.Events = tr.events
		trace.Series = series
		if hosts[0] != nil || len(hosts) > 1 {
			for _, h := range hosts {
				if h != nil && h.Kit().Map != nil {
					mi := h.Kit().Map
					trace.SectorW, trace.SectorH = mi.SectorW, mi.SectorH
					trace.Starts = mi.Starts
					for _, s := range mi.Spots {
						trace.Spots = append(trace.Spots, [3]int32{s.X, s.Z, s.Metal})
					}
					break
				}
			}
		}
		result.Trace = trace
	}
	fmt.Fprintf(log, "arena: %s seed %d (battle %d): %d ticks in %.1fs, winner %d (%s)\n", req.Map, req.Seed, result.BattleSeed, result.Ticks, result.WallSeconds, result.Winner, result.Reason)
	return result, nil
}

// arenaStartOf is the stored number of the map start position u stands on
// at battle entry, or -1. The skirmish stamp puts a commander exactly on its
// StartPos coordinates [08 R-ENTRY-01 §5].
func arenaStartOf(sess *session.Session, u *units.Unit) int {
	if sess.Mission == nil {
		return -1
	}
	x, z := int32(int64(u.X)>>16), int32(int64(u.Z)>>16)
	for _, sp := range sess.Mission.Specials {
		if sp.Kind == 1 && int32(sp.X) == x && int32(sp.Z) == z {
			return int(sp.ID)
		}
	}
	return -1
}

// arenaStep advances one authoritative tick, as the windowed host does, and
// drains the committed events into the match's own buffer, which it returns
// for reuse. The arena has no presentation consumer, so the events are
// discarded; the buffer belongs to one match so that two matches in one
// process share nothing (the simulation benchmark's step keeps a single
// package buffer, which is safe only for one run at a time).
func arenaStep(sess *session.Session, drained []frame.EventView) []frame.EventView {
	sess.Step(sess.Clock.ScaledAnchor + 1)
	if sess.Snapshot != nil {
		drained = sess.Snapshot.DrainCommittedEvents(drained)
	}
	return drained
}

func arenaAlive(sess *session.Session, tr *arenaTracker, i int) bool {
	if tr.diedTick[i] != 0 {
		return false
	}
	return sess.Units.LiveCountForPlayer(i) > 0
}

func orderClasses() []uint8 {
	table := orders.Table()
	out := make([]uint8, len(table))
	for i, d := range table {
		switch d.Name {
		case "MobileBuild", "VTOL_MobileBuild", "BuildingBuild", "HelpBuild", "VTOL_HelpBuild",
			"RepairUnit", "VTOL_RepairUnit", "Reclaim", "ReclaimUnit", "VTOL_Reclaim", "VTOL_ReclaimUnit",
			"RepairPatrol", "VTOL_RepairPatrol", "Resurrect", "Capture", "BuildWeapon",
			"Follow_Ground", "VTOL_Follow", "Guard_NoMove":
			out[i] = 1 // productive work
		}
	}
	return out
}

func unitWorking(u *units.Unit, classes []uint8) bool {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return false
	}
	for _, node := range q.Primary() {
		if node != nil && int(node.ID) < len(classes) && classes[node.ID] == 1 {
			return true
		}
	}
	return false
}

func sampleArena(sess *session.Session, walk []*units.Unit, table *aikit.Table, tr *arenaTracker, player int, tick uint32, classes []uint8) ArenaSample {
	s := ArenaSample{Tick: tick}
	if player < len(sess.Econ.Players) {
		p := &sess.Econ.Players[player]
		s.MetalIncome = int32(p.AIProduction[economy.Metal])
		s.EnergyIncome = int32(p.AIProduction[economy.Energy])
		s.MetalStock = int32(p.Stock[economy.Metal])
		s.EnergyStock = int32(p.Stock[economy.Energy])
		s.MetalWaste = int32(p.Waste[economy.Metal])
	}
	for _, u := range walk {
		if int(u.Owner) != player || u.Def == nil || u.Dying {
			continue
		}
		info := table.Of(u.Def)
		if info == nil {
			continue
		}
		s.Units++
		if u.Remaining != 0 {
			// Remaining runs 1 → 0 as the frame is built [04 §2.3].
			s.FrameValue += int32(float32(info.Value) * (1 - u.Remaining))
			continue
		}
		switch {
		case info.Role.Has(aikit.RoleFactory):
			s.Factories++
			s.EcoValue += info.Value
			if !unitWorking(u, classes) {
				s.IdleFactory++
			}
		case info.Role.Has(aikit.RoleBuilder) || info.Role.Has(aikit.RoleCommander):
			s.Builders++
			if !info.Role.Has(aikit.RoleCommander) {
				s.BuilderValue += info.Value
			}
			if !unitWorking(u, classes) {
				s.IdleBuilders++
			}
		case info.Role.Any(aikit.RoleCombat):
			s.ArmyValue += info.Value
			if info.Role.Has(aikit.RoleMobile) && !info.Role.Has(aikit.RoleAir) && tr.trapped(u, tick) {
				s.Trapped++
			}
		case info.Role.Has(aikit.RoleDefense):
			s.ArmyValue += info.Value / 2
			s.EcoValue += info.Value / 2
		default:
			s.EcoValue += info.Value
		}
		if info.Role.Has(aikit.RoleExtractor) {
			s.Extractors++
		}
		// First attack: an armed unit of this player inside another start area.
	}
	s.ValueLost = tr.valueLost[player]
	s.ValueKilled = tr.valueKilled[player]
	return s
}

// trapped reports whether u has stayed within trapRadius of where it was
// first seen for trapAge ticks without ever leaving, while its current
// order's goal lies beyond trapRadius.
func (tr *arenaTracker) trapped(u *units.Unit, tick uint32) bool {
	x, z := int32(int64(u.X)>>16), int32(int64(u.Z)>>16)
	a, ok := tr.anchors[u.Handle]
	if !ok {
		tr.anchors[u.Handle] = trapAnchor{x: x, z: z, tick: tick}
		return false
	}
	if a.left {
		return false
	}
	dx, dz := int64(x-a.x), int64(z-a.z)
	if dx*dx+dz*dz > trapRadius*trapRadius {
		a.left = true
		tr.anchors[u.Handle] = a
		return false
	}
	if tick-a.tick < trapAge {
		return false
	}
	q := orders.QueueOfUnit(u)
	if q == nil {
		return false
	}
	for _, node := range q.Primary() {
		if node == nil {
			continue
		}
		gx, gz := int64(int64(node.GoalX)>>16), int64(int64(node.GoalZ)>>16)
		if gx == 0 && gz == 0 {
			return false
		}
		dx, dz = gx-int64(x), gz-int64(z)
		return dx*dx+dz*dz > trapRadius*trapRadius
	}
	return false
}

func traceFrame(walk []*units.Unit, table *aikit.Table, n int, tick uint32) ArenaFrame {
	f := ArenaFrame{Tick: tick}
	for _, u := range walk {
		if int(u.Owner) >= n || u.Def == nil || u.Dying {
			continue
		}
		info := table.Of(u.Def)
		hp := int32(100)
		if u.MaxHealth > 0 {
			hp = u.Health * 100 / u.MaxHealth
		}
		built := int32(1)
		if u.Remaining != 0 {
			built = 0
		}
		f.Units = append(f.Units, [6]int32{int32(u.Owner), traceRole(info), int32(int64(u.X) >> 16), int32(int64(u.Z) >> 16), hp, built})
	}
	return f
}
