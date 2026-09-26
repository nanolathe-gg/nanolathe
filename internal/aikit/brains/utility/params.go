package utility

import (
	"fmt"
	"sort"
	"strconv"
)

// Params are the brain's tunable weights. Every field is an integer with a
// documented range so an evolutionary search can mutate them blindly;
// ParseParams clamps into range. Weights are percentages of a nominal
// consideration (100 = neutral); thresholds are in the units named.
type Params struct {
	Horizon     int32 // h: projection horizon, seconds
	DemandBP    int32 // demand_bp: permille of build-power spend capacity counted as demand
	ERatio      int32 // e_ratio: planned energy spent per metal, ×10
	WMetal      int32 // w_metal: extractor weight
	WEnergy     int32 // w_energy: energy producer weight
	WFactory    int32 // w_factory: factory weight
	WDefense    int32 // w_defense: static defense weight
	DefPlan     int32 // def_plan: 1 = proactive defense plan (defense.go), 0 = reactive only
	WRadar      int32 // w_radar: radar weight
	WMaker      int32 // w_maker: metal maker weight
	WStorage    int32 // w_storage: storage weight
	WAssist     int32 // w_assist: assist/guard weight
	WTech       int32 // w_tech: tech-2 factory desire
	WCons       int32 // w_cons: constructor production weight
	WArmy       int32 // w_army: combat production weight
	WAA         int32 // w_aa: anti-air counter strength
	WRange      int32 // w_range: long-range counter to static defenses
	WMix        int32 // w_mix: army variety pressure
	WScout      int32 // w_scout: scout production weight
	VRef        int32 // v_ref: efficiency blend (small = favor cheap units by square law)
	TravelHalf  int32 // travel_half: seconds of travel that halve a site's score
	ThreatHalf  int32 // threat_half: threat-grid level that halves a site's score
	Hysteresis  int32 // hysteresis: percent improvement needed to drop a commitment
	EcoEarly    int32 // eco_early: EcoShare at minute 0
	EcoLate     int32 // eco_late: EcoShare after the ramp
	EcoRamp     int32 // eco_ramp: minutes from early to late share
	AttackMin   int32 // att_min: minimum wave value
	AttackGrow  int32 // att_grow: wave value added per minute
	AttackRatio int32 // att_ratio: wave value as percent of the estimated enemy army
	ComRadius   int32 // com_radius: commander leash from home, world units
	SpotsPerCon int32 // spots_per_con: free safe spots per builder before more are wanted
	FacTime     int32 // fac_time: seconds by which the first factory is fully urgent
	WAir        int32 // w_air: air factory desire, percent of the anti-air-damped suitability
	Naval       int32 // naval: 1 plays water maps (reach, shipyards, water sites); 0 = land only
	Air         int32 // air: 1 builds air plants and aircraft when useful; 0 = never on purpose
	Tech        int32 // tech: 1 runs the timed tech-2 transition; 0 = the old income gate only
	TechTime    int32 // tech_time: minutes by which the first tech-2 factory is fully wanted
	WUpgrade    int32 // w_upgrade: extractor upgrade weight, percent of an extractor's
	Layout      int32 // layout: 1 lays the base out in streets and blocks, keeps factory exits open, reclaims
	WReclaim    int32 // w_reclaim: weight of reclaiming wrecks and clearing streets
	ReachMix    int32 // reach_mix: 1 = production and factory choice follow reach to the plausible enemy starts (§13.1)
	Fleet       int32 // fleet: 1 = assault fleets of destroyers and heavier hulls, a few light boats (§13.2)
	TidalField  int32 // tidal_field: 1 = water economy buildings in fields clear of the shipyards (§13.3)
	Growth      int32 // growth: human economy shape at full ambition, a sum of parts (§13.4)
	FacFirst    int32 // fac_first: first factory family 0 = none, 1 kbot, 2 vehicle, 3 air, 4 ship, 5 drawn per game (§13.5)
	WFacFirst   int32 // w_fac_first: other factories rate this percent below the chosen family's until one is owned
	Expand      int32 // expand: mid-game expansion, a sum of parts (§13.10)
	ExpandFrom  int32 // expand_from: minute from which expand's pace, builders and spend parts act
	Army        int32 // army: income into army and towers that pay, a sum of parts (§13.12)
	Metal       int32 // metal: metal use at every ambition, a sum of parts (README §13.14)
	FacBackoff  int32 // fac_backoff: a factory site search that found no site is not repeated at its request point for a while
}

// ParamSpec documents one parameter.
type ParamSpec struct {
	Name              string
	Default, Min, Max int32
	Doc               string
}

// Specs lists every parameter in Params field order.
var Specs = [...]ParamSpec{
	{"h", 45, 10, 180, "projection horizon (s): stock counts as stock/h of supply"},
	{"demand_bp", 600, 100, 1000, "permille of build-power spend capacity assumed as demand"},
	{"e_ratio", 110, 40, 300, "planned energy per metal ×10 (prices energy, sets E:M balance)"},
	{"w_metal", 100, 0, 400, "extractor weight"},
	{"w_energy", 100, 0, 400, "energy producer weight"},
	{"w_factory", 100, 0, 400, "factory weight"},
	{"w_defense", 60, 0, 400, "static defense weight"},
	{"def_plan", 1, 0, 1, "1 = proactive defense plan, 0 = reactive defenses only"},
	{"w_radar", 40, 0, 400, "radar weight"},
	{"w_maker", 60, 0, 400, "metal maker weight"},
	{"w_storage", 15, 0, 400, "storage weight"},
	{"w_assist", 50, 0, 400, "assist nanoframe / guard factory weight"},
	{"w_tech", 60, 0, 400, "tech-2 factory desire"},
	{"w_cons", 100, 0, 400, "constructor production weight"},
	{"w_army", 100, 0, 400, "combat production weight"},
	{"w_aa", 100, 0, 400, "anti-air counter strength"},
	{"w_range", 60, 0, 400, "range counter to enemy static defense"},
	{"w_mix", 100, 0, 400, "army variety pressure"},
	{"w_scout", 60, 0, 400, "scout production weight"},
	{"v_ref", 150, 20, 1000, "efficiency blend: DPS*HP/(V*(V+v_ref))"},
	{"travel_half", 35, 5, 300, "travel seconds that halve a site's score"},
	{"threat_half", 20, 2, 400, "threat level that halves a site's score"},
	{"hysteresis", 50, 0, 300, "percent better an option must be to drop an assist"},
	{"eco_early", 75, 20, 95, "EcoShare at minute 0"},
	{"eco_late", 40, 10, 80, "EcoShare after the ramp"},
	{"eco_ramp", 12, 2, 30, "minutes from eco_early to eco_late"},
	{"att_min", 800, 100, 6000, "minimum wave value"},
	{"att_grow", 150, 0, 1000, "wave value added per minute"},
	{"att_ratio", 130, 50, 400, "wave value as percent of estimated enemy army"},
	{"com_radius", 900, 300, 3000, "commander leash from home (world units)"},
	{"spots_per_con", 3, 1, 12, "free safe spots per builder before more builders are wanted"},
	{"fac_time", 100, 20, 600, "seconds by which the first factory is fully urgent"},
	{"w_air", 100, 0, 1000, "air factory desire (percent of the anti-air-damped suitability)"},
	{"naval", 1, 0, 1, "switch: water-map play (reach analysis, shipyards, water sites, naval units)"},
	{"air", 1, 0, 1, "switch: air plants and aircraft when useful"},
	{"tech", 1, 0, 1, "switch: timed tech-2 transition (factory, advanced constructors, moho, fusion)"},
	{"tech_time", 16, 4, 40, "minutes by which the first tech-2 factory is fully wanted (with income)"},
	{"w_upgrade", 300, 0, 1000, "extractor upgrade (moho over an extractor) weight, percent of a new extractor's"},
	{"layout", 1, 0, 1, "switch: rows and zones as people build, factory exits kept open (and reopened), wrecks and lane features reclaimed"},
	{"w_reclaim", 60, 0, 400, "weight of reclaiming wrecks near the base and clearing factory lanes"},
	{"reach_mix", 1, 0, 1, "switch: reach judged over the plausible enemy starts; stranded factories make a home guard only, and no more of them"},
	{"fleet", 1, 0, 1, "switch: against an enemy navy, assault fleets of destroyers and heavier hulls, light boats capped to a few"},
	{"tidal_field", 1, 0, 1, "switch: tidal generators and other water economy in fields clear of the shipyards' exits"},
	{"growth", 16, 0, 31, "human economy shape at full ambition, a sum of parts: 1 expansion, 2 tech-2 timeline, 4 constructors per extractor, 8 factories per income, 16 constructor floor"},
	{"fac_first", 0, 0, 5, "first factory family: 0 none, 1 kbot lab, 2 vehicle plant, 3 air plant, 4 shipyard, 5 drawn per game"},
	{"w_fac_first", 300, 100, 1000, "percent by which other factories rate below the first-factory family's until one of it is owned"},
	{"expand", 0, 0, 31, "mid-game expansion, a sum of parts: 1 extractors on the expansion pace, 2 constructors follow the expansion, 4 failed spots held until seen empty, 8 contested spots behind our army, 16 factories follow the income while metal banks (§13.10)"},
	{"expand_from", 15, 4, 30, "minute from which expand's pace, builders and spend parts act (§13.10)"},
	{"army", 21, 0, 127, "income into army and towers that pay, a sum of parts: 1 towers yield to production while metal banks, 2 factories follow banked income and replace a walled-in one, 4 tower types by the square law and direct fire where ground attacks came, 8 towers where attacks came, 16 a tower value ceiling, 32 the commander's zone covered, 64 towers from surplus (§13.12)"},
	{"metal", 7, 0, 7, "metal use at every ambition, a sum of parts: 1 no metal maker while a free safe extractor spot stands open, 2 no energy building or energy storage while the energy store is nine tenths full and not draining, 4 spots near home stay in the plan when the ambition extractor cap binds (README §13.14)"},
	{"fac_backoff", 2, 0, 2, "factory site back-off: after a factory's placement search finds no site, for 20 s 1 = that factory is not asked for at that request point again, 2 = no factory is (docs/MODERN_AI_RESEARCH.md §5.1)"},
}

const numParams = len(Specs)

func (p *Params) slots() [numParams]*int32 {
	return [numParams]*int32{
		&p.Horizon, &p.DemandBP, &p.ERatio, &p.WMetal, &p.WEnergy, &p.WFactory,
		&p.WDefense, &p.DefPlan, &p.WRadar, &p.WMaker, &p.WStorage, &p.WAssist, &p.WTech,
		&p.WCons, &p.WArmy, &p.WAA, &p.WRange, &p.WMix, &p.WScout, &p.VRef,
		&p.TravelHalf, &p.ThreatHalf, &p.Hysteresis, &p.EcoEarly, &p.EcoLate,
		&p.EcoRamp, &p.AttackMin, &p.AttackGrow, &p.AttackRatio, &p.ComRadius,
		&p.SpotsPerCon, &p.FacTime, &p.WAir, &p.Naval, &p.Air, &p.Tech, &p.TechTime, &p.WUpgrade, &p.Layout, &p.WReclaim,
		&p.ReachMix, &p.Fleet, &p.TidalField, &p.Growth, &p.FacFirst, &p.WFacFirst, &p.Expand, &p.ExpandFrom, &p.Army, &p.Metal, &p.FacBackoff,
	}
}

// clampAll clamps every parameter into its documented range.
func (p *Params) clampAll() {
	s := p.slots()
	for i := range Specs {
		*s[i] = min(max(*s[i], Specs[i].Min), Specs[i].Max)
	}
}

// DefaultParams returns the hand-set defaults.
func DefaultParams() Params {
	var p Params
	s := p.slots()
	for i := range Specs {
		*s[i] = Specs[i].Default
	}
	return p
}

// personaKeys are arena player parameters that belong to the persona or the
// label, not to this brain.
var personaKeys = map[string]bool{"async": true, "think": true, "react": true, "apm": true, "attention": true, "skill": true, "ambition": true, "label": true, "style": true, "jitter": true}

// ParseParams applies overrides (name=integer) over the defaults. Values are
// clamped into range; an unknown name or a non-integer value is an error so
// a typo in a search never silently runs the defaults.
func ParseParams(kv map[string]string) (Params, error) {
	p := DefaultParams()
	s := p.slots()
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if personaKeys[k] {
			continue
		}
		idx := -1
		for i := range Specs {
			if Specs[i].Name == k {
				idx = i
				break
			}
		}
		if idx < 0 {
			return p, fmt.Errorf("utility: unknown parameter %q", k)
		}
		v, err := strconv.Atoi(kv[k])
		if err != nil {
			return p, fmt.Errorf("utility: parameter %s=%q is not an integer", k, kv[k])
		}
		sp := &Specs[idx]
		if v < int(sp.Min) {
			v = int(sp.Min)
		}
		if v > int(sp.Max) {
			v = int(sp.Max)
		}
		*s[idx] = int32(v)
	}
	return p, nil
}
