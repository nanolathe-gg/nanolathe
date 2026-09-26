package utility

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// Personality: a per-game character drawn around the tuned balanced play
// (README §13.15). Nanolathe Modern AI policy, the brain's own play; no
// retail behaviour is claimed.
//
// A personality is how a game is played: how readily the army attacks, how
// much it raids, how many towers go up, how fast the economy spreads, when
// tech 2 comes, what the army is made of and how much it scouts. Each of
// those is a trait, an integer from -100 to 100 (0 neutral). By default
// every game plays the balanced style and draws each trait on its own, so
// the same brain raids, builds towers and expands in every game, only at
// rates that differ a little from game to game (user direction 2026-09-25:
// "just weights between them changing a bit. So some raids still happen,
// some towers still get built, etc, just at different rates"). A draw is
// the sum of two uniform draws in -50..50: most games stay near neutral,
// a few lean hard on a trait. The named archetypes (Personalities) pull
// several traits together; they are played only when configured.
//
// A trait acts only through parameters the brain already has, so every
// consideration keeps its meaning: a trait at +100 or -100 moves each of its
// parameters to the percent its row lists (between, linearly from none at
// 0), and the aggression and raid traits also give the tactics army its
// temper (ArmyTemper). No trait switches anything off. The rows' extremes
// are the measured strength-neutral ranges: each trait alone at +100 and at
// -100 was played against the brain without a personality on the 20-minute
// land pool, and a range whose extreme lost more than about three points was
// narrowed (README §13.15).
//
// Draws come from Kit.Rand in Init only, after the style, lead and jitter
// draws (variety.begin) and before the first-factory family draw (index), so
// turning the personality off leaves every earlier draw as it was. Like the
// opening jitter, the draws need jitter on; their number is fixed by the
// switches alone: two per trait for the default draw, one per trait for a
// named archetype's variation about its centres — also for a pinned trait,
// whose drawn value is then replaced, so pinning one trait leaves the others
// as drawn. jitter=0 draws nothing: the default then plays every trait
// neutral and a named archetype its centres.

// Trait indexes a personality trait.
type Trait int

const (
	TraitAggression Trait = iota // attack launch value and engage margin
	TraitRaids                   // raid squad and harass raid sizes
	TraitTowers                  // defense weight
	TraitExpansion               // constructors and extractors
	TraitTech                    // tech-2 timing and desire
	TraitHeavy                   // unit weight class and range counter
	TraitAir                     // air plant desire
	TraitScouting                // scout production
	numTraits
)

// TraitKeys are the player-spec keys that pin the traits (VarietyFrom), in
// trait order; a trait's short name is its key without the prefix.
var TraitKeys = [numTraits]string{"trait_aggression", "trait_raids", "trait_towers", "trait_expansion",
	"trait_tech", "trait_heavy", "trait_air", "trait_scouting"}

// traitName is a trait's short name, for the explain notes.
func traitName(i int) string { return strings.TrimPrefix(TraitKeys[i], "trait_") }

// traitMax bounds a trait's value either side of neutral.
const traitMax = 100

// traitHalf is the range of each of the two uniform draws whose sum is a
// default trait: triangular on -100..100, |t| above 60 in 16% of draws and
// above 80 in 4%.
const traitHalf = 50

// traitNoise is the ± range each trait varies about a named archetype's
// centre when jitter is on.
const traitNoise = 25

// traitWords name a trait's lean, below and above neutral; a drawn
// personality is named by its strongest lean (name).
var traitWords = [numTraits][2]string{
	{"patient", "aggressive"}, {"quiet", "raider"}, {"open", "turtle"}, {"compact", "expander"},
	{"late-tech", "early-tech"}, {"light", "heavy"}, {"grounded", "flyer"}, {"unscouted", "scout"},
}

// steadyLean is the strongest lean below which a drawn personality is
// named "steady".
const steadyLean = 30

// Archetype is a named personality, played only when configured
// (personality=<name>): its trait centres.
type Archetype struct {
	Name   string
	Doc    string
	centre [numTraits]int32
}

// Personalities lists the archetypes. Trait order: aggression, raids,
// towers, expansion, tech, heavy, air, scouting.
var Personalities = [...]Archetype{
	{Name: "rusher", Doc: "attacks early and raids; few towers, late tech, light units",
		centre: [numTraits]int32{70, 40, -50, -30, -50, -40, 0, 20}},
	{Name: "raider", Doc: "raids and scouts with light, fast units",
		centre: [numTraits]int32{20, 80, -20, 0, -20, -60, 20, 60}},
	{Name: "turtle", Doc: "towers and a held army; heavier units, earlier tech",
		centre: [numTraits]int32{-60, -50, 80, -20, 30, 40, -20, -30}},
	{Name: "boomer", Doc: "spreads its economy fast and attacks late",
		centre: [numTraits]int32{-40, -20, -30, 80, 20, 0, 0, 0}},
	{Name: "tech", Doc: "early tech 2 and heavy units",
		centre: [numTraits]int32{-20, -30, 20, 20, 80, 60, 30, 0}},
	{Name: "flyer", Doc: "air plants where air is useful",
		centre: [numTraits]int32{0, 20, -20, 0, 20, 0, 90, 30}},
}

// traitParam is one parameter a trait moves: percent of its value at trait
// -100 (lo) and +100 (hi).
type traitParam struct {
	trait  Trait
	name   string
	lo, hi int32
}

// traitParams are the parameters the traits move, applied in this order
// after the style and the opening jitter; each result is clamped into the
// parameter's range.
var traitParams = [...]traitParam{
	{TraitTowers, "w_defense", 60, 150},
	{TraitExpansion, "w_cons", 80, 125},
	{TraitExpansion, "w_metal", 85, 120},
	{TraitTech, "tech_time", 130, 70},
	{TraitTech, "w_tech", 60, 150},
	{TraitHeavy, "v_ref", 60, 180},
	{TraitHeavy, "w_range", 50, 200},
	{TraitAir, "w_air", 50, 140},
	{TraitScouting, "w_scout", 30, 200},
}

// Aggression and raids act outside Params: aggression scales the launch
// share of the attack value (attackValue; percent at -100 and +100) and
// lowers the army's engage margin (and its retreat margin by half as much)
// by up to aggrEngage permille; raids scale the value at which the tactics
// army splits off its raid squad and the harass raid's least value
// (percent). A lower launch share attacks earlier with a smaller army; the
// production target (armyTarget) does not follow it.
const (
	aggrLaunchLo, aggrLaunchHi = 133, 67
	aggrEngage                 = 80
	raidSplitLo, raidSplitHi   = 140, 80
	raidHarassLo, raidHarassHi = 150, 90
)

// traitPct is a trait's percent along its row: 100 at 0, lo at -100, hi at
// +100.
func traitPct(t, lo, hi int32) int64 {
	if t >= 0 {
		return 100 + int64(hi-100)*int64(t)/traitMax
	}
	return 100 + int64(lo-100)*int64(-t)/traitMax
}

// personality is the strategy's drawn personality. The zero value is
// none.
type personality struct {
	mode  int32 // persNone, persDrawn or persNamed
	arch  int   // with persNamed, the index into Personalities
	trait [numTraits]int32
}

// Personality modes, as reported (personality_mode).
const (
	persNone  = 0 // personality=off: every trait neutral unless pinned
	persDrawn = 1 // the default: each trait drawn around neutral
	persNamed = 2 // a named archetype
)

// personalityIndex resolves an archetype name, or -1.
func personalityIndex(name string) int {
	for i := range Personalities {
		if Personalities[i].Name == name {
			return i
		}
	}
	return -1
}

// validPersonality reports whether a Variety.Personality value is one the
// strategy plays.
func validPersonality(s string) bool {
	return s == "" || s == persOff || s == persRandom || personalityIndex(s) >= 0
}

// The Variety.Personality words besides the archetype names.
const (
	persOff    = "off"
	persRandom = "random"
)

// draw chooses the personality from v (Init only; see the header for the
// draws it makes) and applies its parameter moves to p.
func (ps *personality) draw(k *aikit.Kit, v *Variety, p *Params) {
	*ps = personality{}
	switch v.Personality {
	case "", persOff:
	case persRandom:
		ps.mode = persDrawn
		if v.Jitter {
			for i := range ps.trait {
				ps.trait[i] = k.Rand.Range(-traitHalf, traitHalf) + k.Rand.Range(-traitHalf, traitHalf)
			}
		}
	default:
		ps.mode, ps.arch = persNamed, personalityIndex(v.Personality)
		ps.trait = Personalities[ps.arch].centre
		if v.Jitter {
			for i := range ps.trait {
				t := ps.trait[i] + k.Rand.Range(-traitNoise, traitNoise)
				ps.trait[i] = int32(clamp(int64(t), -traitMax, traitMax))
			}
		}
	}
	for i := range ps.trait {
		if v.Pinned&(1<<i) != 0 {
			ps.trait[i] = v.Traits[i]
		}
	}
	for _, m := range traitParams {
		if t := ps.trait[m.trait]; t != 0 {
			scaleParam(p, paramIndex(m.name), traitPct(t, m.lo, m.hi))
		}
	}
}

// launchPct is the percent of the attack value's launch share.
func (ps *personality) launchPct() int64 {
	return traitPct(ps.trait[TraitAggression], aggrLaunchLo, aggrLaunchHi)
}

// ArmyTemper is what the personality asks of a tactical army: Engage lowers
// its engage margin (permille; negative raises it) and its retreat margin by
// half as much; RaidPct and HarassPct are the percent of the value at which
// its raid squad splits off and of the harass raid's least value. The
// neutral temper is {0, 100, 100}.
type ArmyTemper struct {
	Engage, RaidPct, HarassPct int64
}

// ArmyTemper reports the drawn personality's temper. It is valid once the
// strategy's Init has run (the army's Init runs after it).
func (st *Strategy) ArmyTemper() ArmyTemper {
	ps := &st.vr.pers
	a, r := int64(ps.trait[TraitAggression]), ps.trait[TraitRaids]
	return ArmyTemper{
		Engage:    aggrEngage * a / traitMax,
		RaidPct:   traitPct(r, raidSplitLo, raidSplitHi),
		HarassPct: traitPct(r, raidHarassLo, raidHarassHi),
	}
}

// name is the personality's short name: a named archetype's name; for a
// drawn one its strongest lean (the first trait in order on a tie), or
// "steady" when no trait leans steadyLean or more; "none" without one.
func (ps *personality) name() string {
	switch ps.mode {
	case persNamed:
		return Personalities[ps.arch].Name
	case persDrawn:
		best, lean := -1, int32(steadyLean-1)
		for i, t := range ps.trait {
			if t > lean || -t > lean {
				best, lean = i, max(t, -t)
			}
		}
		if best < 0 {
			return "steady"
		}
		if ps.trait[best] > 0 {
			return traitWords[best][1]
		}
		return traitWords[best][0]
	}
	return "none"
}

// String is the short name and every trait, for the explain notes.
func (ps *personality) String() string {
	var sb strings.Builder
	sb.WriteString("personality ")
	sb.WriteString(ps.name())
	switch ps.mode {
	case persDrawn:
		sb.WriteString(" (drawn)")
	case persNamed:
		sb.WriteString(" (configured)")
	}
	for i, t := range ps.trait {
		sb.WriteString(" ")
		sb.WriteString(traitName(i))
		sb.WriteString(" ")
		if t > 0 {
			sb.WriteString("+")
		}
		sb.WriteString(strconv.Itoa(int(t)))
	}
	return sb.String()
}

// report publishes the personality to the arena: its mode (0 none, 1
// drawn, 2 a named archetype), its short name and every trait.
func (ps *personality) report(add func(name string, value int64)) {
	add("personality_mode", int64(ps.mode))
	add("personality_"+ps.name(), 1)
	for i, t := range ps.trait {
		add(TraitKeys[i], int64(t))
	}
}

// traitFrom reads a pinned trait value.
func traitFrom(key, s string) (int32, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < -traitMax || n > traitMax {
		return 0, fmt.Errorf("utility: %s=%q is not in %d..%d", key, s, -traitMax, traitMax)
	}
	return int32(n), nil
}
