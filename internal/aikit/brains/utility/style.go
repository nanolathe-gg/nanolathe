package utility

import (
	"fmt"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Variety and ambition. Both are expressed only as perturbations of
// Params, so every consideration keeps its meaning and a perturbed brain is
// the same brain with different weights:
//
//   - A style is an opening archetype: multiplicative perturbations applied
//     once, before the shared model derives its per-definition tables, and
//     an opening lead (how many energy buildings precede the first
//     extractor). The default is balanced, the tuned brain; style=random
//     draws one from the player's private generator (Kit.Rand) at the
//     frequency human players use it, and style=<name> plays that one.
//   - Jitter varies the opening a little within the style, redraws the
//     attack sizing for every wave, and draws the personality's traits
//     (personality.go), so the same brain does not open, attack or lean
//     identically twice.
//   - Ambition (the persona's 1..100) is the percent of the top human tier's
//     plan the brain attempts. Below 100 it caps extractors, constructors
//     and factories, and holds the army to a share of the top tier's, each
//     on the tier's measured curve over time, by zeroing for one think the
//     weight that would add one more (limit). Metal that would bank instead
//     goes to towers and then to the army.
//
// style=balanced with jitter off at ambition 100 is exactly the
// deterministic brain, and draws nothing (the default personality draws
// only with jitter).
//
// Human data: the recorded-game benchmark (tools/ai-human-bench, report
// sections 3 and 9): six opening archetypes of 5,013 ProTA 1v1 openings, and
// per-skill-tier linear fits of counts over minutes 3–15.

// perturb scales one parameter by pct percent.
type perturb struct {
	name string
	pct  int32
}

// Style is one strategic character.
type Style struct {
	Name string
	// Weight is the relative probability of a random draw: the archetype's
	// share of human openings, in percent.
	Weight int32
	Doc    string
	mods   []perturb
	// lead weighs 0..4 energy buildings before the first extractor (the
	// first three human builds are EMM 35%, EME 22%, EEE 18%, EEM 13%, MEE
	// and MEM 7%; the eco archetypes open EEEE). Zero weights: no rule.
	// Without jitter the most likely lead is played.
	lead [maxLead + 1]int32
	// attLag is how many seconds later than the whole corpus the archetype
	// fields its first combat unit (negative: earlier); the attack value
	// reads the army tier curve that much later in the game, so the style
	// waits for the army a top player holds that much later (attackValue).
	attLag int32
	// famTilt scales the stock first-factory family shares (percent, by
	// family; open_fam, econ_family.go): the archetype's first-factory
	// family share over the whole corpus's, where the archetype's row
	// lists the family; a zero row is no tilt.
	famTilt [famShip + 1]int32
}

const maxLead = 4

// Opening leads: most archetypes open with one energy building, a few with
// two or none; the eco archetypes more often with two or three. Humans on
// the eco archetypes often open with four (EEEE, ProTA), but in the stock
// game every energy building before the first extractor cost util+tac early
// income and points against the baseline: eco with a lead of four scored
// 44% jitter off, with two 35%, with one 48% (24 games each), and 25–32% in
// 14 random draws that mixed two to four. So the eco archetypes mostly lead
// with one and sometimes with two or three.
var (
	leadOne = [maxLead + 1]int32{9, 83, 8, 0, 0}
	leadEco = [maxLead + 1]int32{0, 70, 30, 0, 0}
	leadAll = [maxLead + 1]int32{0, 40, 40, 20, 0}
)

// Styles lists every style; a random draw picks by Weight. The archetype
// perturbations are the benchmark's: each parameter scales with the
// archetype's median of the matching quantity relative to the whole corpus
// (fac_time with the first factory's time; w_factory, w_cons, w_metal,
// w_energy, w_defense with the counts built by minute 10; w_army with army
// value built by minute 10; w_tech with the share reaching tech 2 by minute
// 15; w_air with the share owning an air plant by minute 10). tech_time
// carries the inverse of w_tech, because the timed tech-2 transition reads
// it rather than w_tech. balanced is never drawn: it is the deterministic
// baseline.
var Styles = [...]Style{
	{Name: "balanced", Weight: 0, Doc: "the tuned defaults (never drawn; the deterministic baseline)"},
	{Name: "expand", Weight: 30, Doc: "O1: factory at build 6, then constructors (human share 30%, wins 52%)",
		mods: []perturb{{"fac_time", 95}, {"w_cons", 110}, {"w_defense", 115}, {"w_tech", 115}, {"tech_time", 85}},
		lead: leadOne, attLag: 43, famTilt: famTiltExpand},
	{Name: "eco", Weight: 25, Doc: "O2: energy and extractors first, factory at build 9 (25%, 52%)",
		mods: []perturb{{"fac_time", 115}, {"w_factory", 135}, {"w_cons", 120}, {"w_metal", 145}, {"w_energy", 140},
			{"w_defense", 145}, {"w_army", 105}, {"w_tech", 125}, {"tech_time", 80}, {"w_air", 90}},
		lead: leadEco, attLag: 12, famTilt: famTiltEco},
	{Name: "units", Weight: 23, Doc: "O3: factory at build 6, combat units first (23%, 48%)",
		mods: []perturb{{"fac_time", 90}, {"w_cons", 65}, {"w_metal", 85}, {"w_energy", 75}, {"w_defense", 55},
			{"w_army", 110}, {"w_tech", 55}, {"tech_time", 180}, {"w_air", 95}},
		lead: leadOne, attLag: -64, famTilt: famTiltUnits},
	{Name: "tower", Weight: 15, Doc: "O4: factory at build 6, an early tower (15%, 46%)",
		mods: []perturb{{"fac_time", 90}, {"w_metal", 85}, {"w_energy", 95}, {"w_defense", 130}, {"w_army", 85},
			{"w_tech", 90}, {"tech_time", 110}, {"w_air", 105}},
		lead: leadOne, attLag: 36, famTilt: famTiltTower},
	{Name: "twofac", Weight: 4, Doc: "O5: factory at build 5, a second soon, often air (4%, 49%)",
		mods: []perturb{{"fac_time", 80}, {"w_factory", 135}, {"w_cons", 90}, {"w_metal", 85}, {"w_energy", 80},
			{"w_defense", 70}, {"w_army", 90}, {"w_tech", 90}, {"tech_time", 110}, {"w_air", 195}},
		lead: leadOne, attLag: 9, famTilt: famTiltTwofac},
	// O6 at full strength (fac_time 155, w_factory 135, w_cons 210, w_metal
	// 205, w_energy 180, w_defense 155, w_army 80, w_tech 155, w_air 75)
	// scored 34% against the baseline over 48 games (its army at minute 10
	// is half the baseline's); it plays at half strength (the geometric
	// mean of the human perturbation and none).
	{Name: "greedy", Weight: 3, Doc: "O6: no factory in the first twelve builds (3%, 52%), at half strength",
		mods: []perturb{{"fac_time", 125}, {"w_factory", 115}, {"w_cons", 145}, {"w_metal", 145}, {"w_energy", 135},
			{"w_defense", 125}, {"w_army", 90}, {"w_tech", 125}, {"tech_time", 80}, {"w_air", 85}},
		lead: leadAll, attLag: 63},
}

// modalLead is the most likely lead of a style (used without jitter).
func (st *Style) modalLead() int32 {
	best := int32(0)
	for i := range st.lead {
		if st.lead[i] > st.lead[best] {
			best = int32(i)
		}
	}
	return best
}

// ambitionScale: at ambition 0 a parameter is low percent of its value,
// rising linearly to 100% at ambition 100 (quadratically for quad).
type ambitionScale struct {
	name string
	low  int32
	quad bool
}

// ambitionScales are the weights ambition scales once; the counts are held
// by the tier curves below.
var ambitionScales = [...]ambitionScale{
	{name: "w_tech", low: 0, quad: true},
	{name: "tech_time", low: 250},
	{name: "travel_half", low: 60},
	{name: "com_radius", low: 70},
	{name: "att_min", low: 50},
	{name: "att_grow", low: 40},
}

// tierCurve is a count (milli) growing linearly with game time: a + b per
// minute. The top human tier's fits (report section 9, ProTA 1v1, minutes
// 3–15, counts built) are converted to the stock unit set's alive counts by
// the ratio of original-TA winners' alive counts to ProTA winners' built
// counts at minutes 10 and 15 (extractors 0.58, constructors 0.63,
// factories 0.88, defenses 0.70, army value 1.08):
//
//	extractors   -1.7 + 1.74/min × 0.58
//	constructors -2.7 + 1.31/min × 0.63
//	factories    -0.02 + 0.27/min × 0.88
//	defenses     -8.0 + 1.81/min × 0.70
//	army value   -753 + 222/min × 1.08 (arena value, fit of the medians)
//
// A persona's cap is its ambition percent of the curve: 100 is the top
// tier (and no cap); the lower tiers sit at 77% (low) to 87% (mid) of the
// top tier's income and extractors at minute 10, so medium's 80 is the
// low-to-mid tiers and easy's 35 about half the low tier.
type tierCurve struct{ a, b int64 }

var (
	topExtractors   = tierCurve{-990, 1010}
	topConstructors = tierCurve{-1700, 830}
	topFactories    = tierCurve{-20, 236}
	topDefenses     = tierCurve{-5600, 1270}
	topArmy         = tierCurve{-814000, 239000}
)

// ambition is the persona's ambition as the brain reads it, 1..100: zero
// (unset) is the full plan, as aikit.Persona normalizes it, so a kit
// built by hand plays the same plan as one from a host.
func ambition(per *aikit.Persona) int32 {
	if a := per.Ambition; a > 0 && a < 100 {
		return a
	}
	return 100
}

// at is the curve's value (milli) at tick scaled by ambition percent.
func (c tierCurve) at(ambition int32, tick uint32) int64 {
	return (c.a + c.b*int64(tick)/1800) * int64(ambition) / 100
}

// The attack value (core.Posture.AttackValue) is the army value at which the
// tactics army may launch an offensive; production also doubles the army
// share while the army is below it. It follows the persona's army tier
// curve — topArmy scaled by ambition, the curve the ambition caps hold the
// army to — so every persona's own army can reach it:
//
//	waveShare % × topArmy.at(ambition, now + the style's attLag), at least waveFloor
//
// and at least att_ratio % of the estimated enemy army (strategy.go); each
// wave is then jittered (wave). The style's lag is the archetype's median
// minute of its first combat unit less the corpus median (2.96 min; human
// benchmark report sections 3 and 4): expand (O1) 3.68, eco (O2) 3.17,
// units (O3) 1.90, tower (O4) 3.56, twofac (O5) 3.12, greedy (O6) 5.05 at
// half strength like its other perturbations. waveFloor is the tactics
// army's smallest launch (three units worth 300); it also keeps the value
// positive, which production reads as "no army target" at zero.
const (
	waveShare = 45
	waveFloor = 300
)

// buildValue is the army size production pushes toward, before the enemy
// estimate and the wave jitter: the tuned att_min + att_grow per minute
// (scaled by ambition), which was the attack value before it followed the
// tier curve (strategy.go keeps production on it).
func buildValue(s *shared, p *Params) int64 {
	return int64(p.AttackMin) + int64(p.AttackGrow)*int64(s.minutes)
}

// attackValue is this think's launch value before the enemy estimate and
// the wave jitter. With AttLinear it is the build value, as before the
// launch followed the tier curve.
func (vr *variety) attackValue(s *shared, p *Params) int64 {
	if vr.v.AttLinear {
		return buildValue(s, p)
	}
	share, floor := int64(waveShare), int64(waveFloor)
	if vr.v.AttShare > 0 {
		share = int64(vr.v.AttShare)
	}
	if vr.v.AttFloor > 0 {
		floor = int64(vr.v.AttFloor)
	}
	// The personality's aggression moves the launch share (personality.go).
	share = share * vr.pers.launchPct() / 100
	t := int64(s.tick)
	if !vr.v.AttNoLag {
		t += int64(Styles[vr.style].attLag) * 30
	}
	if t < 0 {
		t = 0
	}
	a := int64(ambition(&s.k.Persona))
	v := (topArmy.a + topArmy.b*t/1800) * a * share / (100 * 100 * 1000)
	return max64(v, floor)
}

// Floors: every persona may own one factory and one extractor, and an army
// worth 200 before the curve allows more.
const (
	floorFactories  = 1000
	floorExtractors = 1000
	floorArmy       = 200000
)

// bankPercent: a metal store at least this full while income covers
// expense is banking; the capped plan then spends it on towers and army.
const bankPercent = 60

// openingTicks bounds the opening lead: after three minutes extractors are
// never held back for energy.
const openingTicks = 5400

// openingJitter is the ± percent each named parameter varies per game when
// jitter is on.
var openingJitter = [...]perturb{
	{"fac_time", 25}, {"eco_early", 8}, {"w_cons", 15}, {"w_energy", 10}, {"w_metal", 10},
	{"v_ref", 30}, {"w_mix", 20}, {"w_defense", 20}, {"eco_ramp", 15},
}

// Wave jitter: each wave's attack sizing is this percent range of the
// strategy's value.
const (
	waveJitLo = 80
	waveJitHi = 125
)

// Variety selects the style and whether the brain jitters, and holds the
// attack-value switches (attackValue). The zero value of each attack switch
// is the default.
type Variety struct {
	// Style is a style name, or "random" (or empty) for a weighted draw;
	// DefaultVariety plays "balanced".
	Style  string
	Jitter bool
	// AttLinear restores the attack value before it followed the army tier
	// curve: att_min + att_grow per minute, both scaled by ambition
	// (att_curve=0).
	AttLinear bool
	// AttShare overrides the share of the tier curve (att_share=, percent)
	// and AttFloor the smallest attack value (att_floor=); 0 keeps the
	// default.
	AttShare, AttFloor int32
	// AttNoLag drops the style's lag from the attack value (att_lag=0).
	AttNoLag bool
	// AttNoBuild lets production push the army only up to the launch
	// value (att_build=0) instead of the larger build value (strategy.go).
	AttNoBuild bool
	// NoTowerTime and NoWideBase turn off the second round of front rules
	// (README §12.4; layout_base.go): tower timing (tower_time=0, the
	// default; tower_time=1 turns it on) and the wider base (wide_base=0).
	// Both act only with def_plan and layout on.
	NoTowerTime, NoWideBase bool
	// Opening switches (opening.go, README §13.13). OpenReclaim is the early
	// reclaim's weight in percent (open_reclaim=0..400; 0 turns it off);
	// OpenReclaimHi (open_reclaim_hi=, permille) is the metal store at
	// which its gate shuts, and OpenReclaimEnd (open_reclaim_end=,
	// minutes) when it ends, full to half of it (0 keeps each default).
	OpenReclaim                   int32
	OpenReclaimHi, OpenReclaimEnd int32
	// OpenFam draws the first factory's family per game from the stock
	// human shares tilted by the style (open_fam=1; econ_family.go). An
	// explicit fac_first wins.
	OpenFam bool
	// OpenArmy is the early army's parts (open_army=0..3; opening.go).
	OpenArmy int32
	// OpenFollow is the factory after the first (open_follow=0..2;
	// econ_family.go).
	OpenFollow int32
	// Personality is the per-game character (personality.go, README
	// §13.15): "random" draws each trait around neutral (DefaultVariety),
	// an archetype name plays that archetype, and "off" none. The empty
	// zero value is none, so a Variety literal plays no personality.
	Personality string
	// Traits pins a trait (-100..100) where Pinned has its bit (1 << the
	// Trait), with or without a personality (trait_<name>=).
	Traits [numTraits]int32
	Pinned uint16
}

// DefaultVariety plays the balanced style, jitters and draws a personality
// around it per game (README §13.15): the drawn styles lost to balanced
// (expand and tower, the most drawn, took 44% of the points in 40-minute
// games), so a style is played only when configured (style=<name|random>).
// Tower timing is off by default: on the integrated tip it still cost
// points against the same brain without it (README §12.4); tower_time=1
// turns it on. The early reclaim (at weight openReclaimW) and both
// early-army parts are on (README §13.13); the first-factory family draw
// is off: it held the 20-minute gate, but its kbot openings lose about two
// games in three to the same brain (open_fam=1 turns it on).
func DefaultVariety() Variety {
	return Variety{Style: "balanced", Jitter: true, NoTowerTime: true,
		OpenReclaim: openReclaimW, OpenArmy: armyScouts | armyAfterCons, Personality: persRandom}
}

// VarietyFrom reads player-spec switches style=<name|random>, jitter=0|1,
// the attack-value switches att_curve=0|1, att_share=<percent>,
// att_floor=<value>, att_lag=0|1 and att_build=0|1, the front-rule
// switches tower_time=0|1 and wide_base=0|1, the opening switches
// open_reclaim=<weight>, open_reclaim_hi=<permille>,
// open_reclaim_end=<minutes>, open_army=<parts>, open_fam=0|1 and
// open_follow=0..2, and the personality personality=<name|random|off> and
// its traits trait_<name>=-100..100 (TraitKeys) over the default; other
// keys are ignored.
func VarietyFrom(kv map[string]string) (Variety, error) {
	v := DefaultVariety()
	if s, ok := kv["style"]; ok {
		if s != "random" && styleIndex(s) < 0 {
			return v, fmt.Errorf("utility: unknown style %q", s)
		}
		v.Style = s
	}
	if s, ok := kv["personality"]; ok {
		if s == "" || !validPersonality(s) {
			return v, fmt.Errorf("utility: unknown personality %q", s)
		}
		v.Personality = s
	}
	for i, key := range TraitKeys {
		if s, ok := kv[key]; ok {
			t, err := traitFrom(key, s)
			if err != nil {
				return v, err
			}
			v.Traits[i] = t
			v.Pinned |= 1 << i
		}
	}
	if s, ok := kv["jitter"]; ok {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 1 {
			return v, fmt.Errorf("utility: jitter=%q is not 0 or 1", s)
		}
		v.Jitter = n == 1
	}
	for _, sw := range [...]struct {
		key string
		dst *bool
	}{{"att_curve", &v.AttLinear}, {"att_lag", &v.AttNoLag}, {"att_build", &v.AttNoBuild},
		{"tower_time", &v.NoTowerTime}, {"wide_base", &v.NoWideBase}} {
		if s, ok := kv[sw.key]; ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 || n > 1 {
				return v, fmt.Errorf("utility: %s=%q is not 0 or 1", sw.key, s)
			}
			*sw.dst = n == 0
		}
	}
	for _, sw := range [...]struct {
		key string
		dst *bool
	}{{"open_fam", &v.OpenFam}} {
		if s, ok := kv[sw.key]; ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 || n > 1 {
				return v, fmt.Errorf("utility: %s=%q is not 0 or 1", sw.key, s)
			}
			*sw.dst = n == 1
		}
	}
	for _, sw := range [...]struct {
		key    string
		dst    *int32
		lo, hi int
	}{{"att_share", &v.AttShare, 1, 400}, {"att_floor", &v.AttFloor, 1, 20000}, {"open_reclaim", &v.OpenReclaim, 0, 400}, {"open_reclaim_hi", &v.OpenReclaimHi, 1, 1000}, {"open_reclaim_end", &v.OpenReclaimEnd, 1, 60}, {"open_army", &v.OpenArmy, 0, 3}, {"open_follow", &v.OpenFollow, 0, 2}} {
		if s, ok := kv[sw.key]; ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < sw.lo || n > sw.hi {
				return v, fmt.Errorf("utility: %s=%q is not in %d..%d", sw.key, s, sw.lo, sw.hi)
			}
			*sw.dst = int32(n)
		}
	}
	return v, nil
}

func styleIndex(name string) int {
	for i := range Styles {
		if Styles[i].Name == name {
			return i
		}
	}
	return -1
}

// paramIndex resolves a parameter name to its slot; the tables above are
// checked against Specs by the tests.
func paramIndex(name string) int {
	for i := range Specs {
		if Specs[i].Name == name {
			return i
		}
	}
	return -1
}

// scaleParam multiplies slot i by pct percent (rounded) and clamps it into
// its documented range.
func scaleParam(p *Params, i int, pct int64) {
	if i < 0 {
		return
	}
	s := p.slots()
	sp := &Specs[i]
	v := (int64(*s[i])*pct + 50) / 100
	if v < int64(sp.Min) {
		v = int64(sp.Min)
	}
	if v > int64(sp.Max) {
		v = int64(sp.Max)
	}
	*s[i] = int32(v)
}

// applyAmbition scales Params by the persona's ambition (1..100; see
// ambition).
func applyAmbition(p *Params, amb int32) {
	if amb >= 100 || amb <= 0 {
		return
	}
	a := int64(amb)
	for _, m := range ambitionScales {
		f := a
		if m.quad {
			f = a * a / 100
		}
		pct := int64(m.low) + (100-int64(m.low))*f/100
		scaleParam(p, paramIndex(m.name), pct)
	}
}

// applyStyle scales Params by a style's perturbations.
func applyStyle(p *Params, st *Style) {
	for _, m := range st.mods {
		scaleParam(p, paramIndex(m.name), int64(m.pct))
	}
}

// variety is the strategy's per-game state for styles, jitter and
// ambition.
type variety struct {
	v       Variety
	style   int // index into Styles
	waveJit int64
	ready   bool // the army reached the current wave's size
	waves   int32
	labels  [numLabels]string

	// planned is Params after ambition, style and jitter; each think starts
	// from it before the opening lead and the ambition caps apply.
	planned Params
	lead    int32 // energy buildings before the first extractor (0 = no rule)
	pers    personality
	// Definitions the caps count (UnitInfo.Index).
	facIdx, mexIdx, consIdx, energyIdx, defIdx []int32
	// Thinks each rule bound: factory, extractor, constructor and army caps,
	// banking (towers or army instead), the opening lead.
	capped [6]int32

	// Instrumentation for Report (never read by a decision).
	open      [openLen]*aikit.UnitInfo
	openN     int
	seen      []seenUnit // by handle: the unit last seen there
	firstPush uint32
}

// seenUnit identifies the unit a slot held (definition and instance).
type seenUnit struct {
	def *aikit.UnitInfo
	gen uint32
}

const openLen = 10

// Posture labels, indexed.
const (
	lblPressure = iota
	lblDefend
	lblOpening
	lblBuildUp
	lblExpand
	numLabels
)

var labelNames = [numLabels]string{"pressure", "defend", "opening", "build-up", "expand"}

// SetVariety selects the style and jitter; it must be called before the
// brain's first think.
func (st *Strategy) SetVariety(v Variety) error {
	if v.Style != "" && v.Style != "random" && styleIndex(v.Style) < 0 {
		return fmt.Errorf("utility: unknown style %q", v.Style)
	}
	if !validPersonality(v.Personality) {
		return fmt.Errorf("utility: unknown personality %q", v.Personality)
	}
	for i := range v.Traits {
		if t := v.Traits[i]; v.Pinned&(1<<i) != 0 && (t < -traitMax || t > traitMax) {
			return fmt.Errorf("utility: %s=%d is not in %d..%d", TraitKeys[i], t, -traitMax, traitMax)
		}
	}
	st.vr.v = v
	return nil
}

// begin runs once from Init, before the shared model derives its tables:
// ambition, the style (named or drawn), its opening lead and the opening
// jitter.
func (vr *variety) begin(k *aikit.Kit, p *Params) {
	applyAmbition(p, ambition(&k.Persona))
	vr.style = styleIndex(vr.v.Style)
	if vr.style < 0 {
		var w [len(Styles)]int32
		for i := range Styles {
			w[i] = Styles[i].Weight
		}
		vr.style = k.Rand.Pick(w[:])
		if vr.style < 0 {
			vr.style = 0
		}
	}
	st := &Styles[vr.style]
	applyStyle(p, st)
	vr.lead = st.modalLead()
	vr.waveJit = 100
	if vr.v.Jitter {
		if l := k.Rand.Pick(st.lead[:]); l >= 0 {
			vr.lead = int32(l)
		}
		for _, m := range openingJitter {
			j := int64(k.Rand.Range(-m.pct, m.pct))
			scaleParam(p, paramIndex(m.name), 100+j)
		}
		vr.waveJit = int64(k.Rand.Range(waveJitLo, waveJitHi))
	}
	// The personality draws after every draw above (personality.go).
	vr.pers.draw(k, &vr.v, p)
	for i := range vr.labels {
		if vr.style == 0 {
			vr.labels[i] = labelNames[i]
		} else {
			vr.labels[i] = st.Name + " " + labelNames[i]
		}
	}
}

// index lists the definitions the opening lead and the ambition caps
// count; it runs once the shared model is set up.
func (vr *variety) index(s *shared) {
	vr.planned = s.p
	s.open.rec.w = vr.v.OpenReclaim
	s.open.rec.hi, s.open.rec.end = vr.v.OpenReclaimHi, vr.v.OpenReclaimEnd
	s.open.army = vr.v.OpenArmy
	if vr.v.OpenFam && s.p.FacFirst == famNone {
		// The family draw comes after the style, lead and jitter draws, so
		// turning it on leaves those as they were; without jitter the most
		// likely family is played and nothing is drawn.
		s.labelFamilies(s.k)
		if w := styleShares(&Styles[vr.style]); vr.v.Jitter {
			s.firstFam = s.drawFamily(s.k, w)
		} else {
			s.firstFam = s.modalFamily(s.k, w)
		}
		s.keepFamily(s.k)
	}
	if s.facFam != nil {
		s.setFollow(vr.v.OpenFollow)
	}
	front := s.p.DefPlan != 0 && s.p.Layout != 0
	s.zones.f2.towers = front && !vr.v.NoTowerTime
	s.zones.f2.wide = front && !vr.v.NoWideBase
	if ambition(&s.k.Persona) >= 100 && vr.lead == 0 && !s.zones.f2.towers {
		return // nothing counts (tower timing's banking rule counts the towers)
	}
	for i, u := range s.k.Table.Units {
		r := u.Role
		switch {
		case r.Has(aikit.RoleFactory):
			vr.facIdx = append(vr.facIdx, int32(i))
		case r.Has(aikit.RoleExtractor):
			vr.mexIdx = append(vr.mexIdx, int32(i))
		case r.Has(aikit.RoleEnergy) && !r.Has(aikit.RoleMobile):
			vr.energyIdx = append(vr.energyIdx, int32(i))
		case r.Has(aikit.RoleDefense):
			vr.defIdx = append(vr.defIdx, int32(i))
		case s.info[i].canEco && !r.Has(aikit.RoleCommander):
			vr.consIdx = append(vr.consIdx, int32(i))
		}
	}
}

func (vr *variety) countOf(s *shared, idx []int32) int64 {
	var n int64
	for _, i := range idx {
		n += int64(s.count[i])
	}
	return n
}

// limit applies the opening lead and the ambition caps to this think's
// Params (reset from the plan first by the strategy). Counts include
// nanoframes, builders walking to a committed site and constructors queued
// at a factory.
func (vr *variety) limit(b *core.Board, s *shared, pr *Production) {
	s.metalThink(b) // econ_metal.go
	p := &s.p
	if vr.lead > 0 && s.tick < openingTicks && vr.countOf(s, vr.energyIdx) < int64(vr.lead) {
		// The opening lead: energy before the first extractors (and before
		// the factory, which humans place fifth to eighth).
		p.WMetal, p.WMaker, p.WFactory = 0, 0, 0
		vr.capped[5]++
		s.mt.lead = true
	}
	a := ambition(&s.k.Persona)
	if a >= 100 {
		if s.zones.f2.towers && vr.banking(s) {
			// Tower timing (defense_time.go): the banking rule's towers at
			// full ambition too.
			vr.bankTowers(s, a)
		}
		return
	}
	t := s.tick
	if vr.countOf(s, vr.facIdx)*1000 >= max64(topFactories.at(a, t), floorFactories) {
		p.WFactory = 0
		vr.capped[0]++
	}
	if vr.countOf(s, vr.mexIdx)*1000 >= max64(topExtractors.at(a, t), floorExtractors) {
		s.capExtractors() // metal: spots near home stay in the plan (econ_metal.go)
		vr.capped[1]++
	}
	n := vr.countOf(s, vr.consIdx)
	var queued int32
	if pr != nil {
		for _, fi := range b.Factories {
			f := &b.O.Own[fi]
			if int(f.H) >= len(pr.reg) {
				continue
			}
			r := &pr.reg[f.H]
			if r.def == f.Info && r.gen == f.Gen && r.prod != nil && f.QueueLen >= 1 && s.tick-r.tick < 1800 && s.info[r.prod.Index].canEco {
				queued++
			}
		}
	}
	if queued > s.consPending {
		n += int64(queued - s.consPending)
	}
	if n*1000 >= topConstructors.at(a, t) {
		p.WCons = 0
		vr.capped[2]++
	}
	// Banking: the capped plan has nothing left to buy, so the metal goes
	// to towers (up to the tier's defense curve) and the army cap lifts.
	if vr.banking(s) {
		vr.bankTowers(s, a)
		return
	}
	if b.NearHomeThreat == 0 && int64(b.ArmyValue)*1000 >= max64(topArmy.at(a, t), floorArmy) {
		p.WArmy = 0
		vr.capped[3]++
	}
}

// banking reports whether the metal store banks: at least bankPercent
// full while income covers expense.
func (vr *variety) banking(s *shared) bool {
	return s.mCap > 0 && s.mStock*100 >= s.mCap*bankPercent && s.mInc >= s.mExp
}

// bankTowers is the banking rule's towers: while the towers are below the
// tier's defense curve, w_defense is tripled (at least 180) for this think.
func (vr *variety) bankTowers(s *shared, a int32) {
	vr.capped[4]++
	if vr.countOf(s, vr.defIdx)*1000 < topDefenses.at(a, s.tick) {
		p := &s.p
		p.WDefense = max32(p.WDefense*3, 180)
	}
}

// wave sizes the next attack: a wave is ready when the army reaches the
// jittered value; once it has been spent (the army falls below half of it)
// the next wave draws a new factor.
func (vr *variety) wave(k *aikit.Kit, army, attack int64) int64 {
	target := attack * vr.waveJit / 100
	switch {
	case !vr.ready && army >= target:
		vr.ready = true
	case vr.ready && army*2 < target:
		vr.ready = false
		vr.waves++
		if vr.v.Jitter {
			vr.waveJit = int64(k.Rand.Range(waveJitLo, waveJitHi))
			target = attack * vr.waveJit / 100
		}
	}
	return target
}

// watch records the opening (the first definitions built, in order) and
// the first push (three combat units well into the enemy's half) for the
// arena's report. It reads only the board and never draws.
func (vr *variety) watch(b *core.Board, s *shared) {
	s.watchOpen(b)
	o := b.O
	if vr.openN < openLen {
		for i := range o.Own {
			u := &o.Own[i]
			for len(vr.seen) <= int(u.H) {
				vr.seen = append(vr.seen, seenUnit{})
			}
			id := seenUnit{u.Info, u.Gen}
			if vr.seen[u.H] == id {
				continue
			}
			vr.seen[u.H] = id
			if u.Info.Role.Has(aikit.RoleCommander) || vr.openN >= openLen {
				continue
			}
			vr.open[vr.openN] = u.Info
			vr.openN++
		}
	}
	if vr.firstPush == 0 {
		n := 0
		for _, i := range b.Combat {
			u := &o.Own[i]
			if u.Info.Role.Has(aikit.RoleScout) || !u.Built {
				continue
			}
			if s.territory(b, u.X, u.Z) < 800 {
				n++
			}
		}
		if n >= 3 {
			vr.firstPush = b.Tick
		}
	}
}

// Report publishes the style, the opening and the first push to the arena
// (host-side, after the match).
func (st *Strategy) Report(add func(name string, value int64)) {
	vr := &st.vr
	add("style", int64(vr.style))
	add("style_"+Styles[vr.style].Name, 1)
	add("ambition", int64(ambition(&st.s.k.Persona)))
	add("waves", int64(vr.waves))
	add("first_push_tick", int64(vr.firstPush))
	add("capped_factory", int64(vr.capped[0]))
	add("capped_extractor", int64(vr.capped[1]))
	add("capped_cons", int64(vr.capped[2]))
	add("capped_army", int64(vr.capped[3]))
	add("banking", int64(vr.capped[4]))
	add("open_lead", int64(vr.lead))
	add("lead_thinks", int64(vr.capped[5]))
	vr.pers.report(add)
	if s := st.s; s.init {
		s.reportOpen(add)
		s.mt.report(add)
		if s.p.DefPlan != 0 {
			s.zones.audit.report(add)
		}
		if s.p.Layout != 0 {
			s.zones.laudit.report(add)
		}
	}
	var h uint64 = 1469598103934665603
	for i := 0; i < vr.openN; i++ {
		name := "?"
		if d := vr.open[i].Def; d != nil {
			name = d.UnitName
		}
		add(fmt.Sprintf("open_%02d_%s", i, name), 1)
		for j := 0; j < len(name); j++ {
			h = (h ^ uint64(name[j])) * 1099511628211
		}
		h = (h ^ '/') * 1099511628211
	}
	add("open_hash", int64(h>>12))
}
