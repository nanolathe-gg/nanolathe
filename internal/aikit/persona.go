package aikit

// Persona is how a computer player executes, separate from what it decides.
// Difficulty lives here as human-like limits — how often it looks, how long
// it takes to react, how many actions it can take and how many operations it
// can attend to — and as ambition, how much it attempts, rather than as
// resource bonuses or deliberately bad play.
type Persona struct {
	Name string
	// ThinkEvery is the number of ticks between observations (30 = 1 s).
	ThinkEvery uint32
	// Reaction is the ticks between an observation and its commands taking
	// effect. It is also the window an asynchronous think has to finish.
	Reaction uint32
	// APM caps player-level actions per minute (0 = unlimited); Burst is how
	// many may be banked.
	APM, Burst int32
	// Attention is how many independent operations (squads, fronts, build
	// projects) the brain may run at once. Brains interpret it.
	Attention int32
	// Skill 0..100 is a brain-interpreted knob for decision refinements that
	// cost attention in a human (micro, focus fire, retreat discipline).
	Skill int32
	// Ambition 1..100 is how much the brain attempts — expansions,
	// factories, tech, defenses, the army it wants before it attacks — not
	// how well it makes each decision. 100 is the brain's full plan; a lower
	// value is a smaller plan executed as competently. The utility brain
	// reads it as the percent of the top human skill tier's build curves it
	// attempts (medium 80 ≈ the low-to-mid tiers, easy 35 ≈ half the low
	// tier). Brains interpret it.
	// Zero means unset and normalizes to 100, so a persona literal that
	// omits it keeps the full plan; the smallest plan is 1.
	Ambition int32
	// Async runs Think on a worker goroutine between observation and effect.
	// It never changes the game, only which core does the work.
	Async bool
	// Omniscient is a research-only switch that reports every hostile unit
	// as visible, to measure what the fairness boundary costs. No shipped
	// persona sets it.
	Omniscient bool
}

// Built-in personas. Numbers are prototype values for the arena, not tuned.
var (
	PersonaEasy = Persona{Name: "easy", ThinkEvery: 45, Reaction: 45, APM: 25, Burst: 4, Attention: 1, Skill: 20, Ambition: 35}
	PersonaMed  = Persona{Name: "medium", ThinkEvery: 30, Reaction: 15, APM: 60, Burst: 8, Attention: 2, Skill: 55, Ambition: 80}
	PersonaHard = Persona{Name: "hard", ThinkEvery: 15, Reaction: 6, APM: 150, Burst: 15, Attention: 4, Skill: 85, Ambition: 100}
	PersonaMax  = Persona{Name: "max", ThinkEvery: 10, Reaction: 3, APM: 0, Burst: 0, Attention: 8, Skill: 100, Ambition: 100}
)

// PersonaByName resolves a built-in persona.
func PersonaByName(name string) (Persona, bool) {
	for _, p := range [...]Persona{PersonaEasy, PersonaMed, PersonaHard, PersonaMax} {
		if p.Name == name {
			return p, true
		}
	}
	return Persona{}, false
}

func (p *Persona) normalize() {
	if p.ThinkEvery == 0 {
		p.ThinkEvery = 30
	}
	if p.Attention <= 0 {
		p.Attention = 1
	}
	if p.Reaction == 0 {
		p.Async = false
	}
	if p.Ambition <= 0 {
		p.Ambition = 100
	}
	if p.Ambition > 100 {
		p.Ambition = 100
	}
}
