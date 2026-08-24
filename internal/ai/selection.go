package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): AI resource-score expressions (energyRaw/metalRaw) are float32 temporaries per I2
// with exact x87 spill retention unknown; this file evaluates the named energyRaw and metalRaw
// expressions in float32 and narrows at the shown truncations per [08 "Established AI-facing data and rooted planner"] [PLAN 11 C6] [INVARIANTS I2].

// Candidate is the selected build target [PLAN 11 Public API].
type Candidate struct {
	DefKey string
	Score  int32
}

// Selector is the minimal Manager view needed for candidate selection [PLAN 11 WU-11-4].
// It is a local interface to avoid a hard import cycle while manager.go (WU-11-2) lands concurrently.
// Mapping for WU-14 unification:
//
//	Selector.GetPlayer()      ↔ Manager.Player      (uint8, 0..9)
//	Selector.GetProfile()     ↔ Manager.Profile     (*Profile)
//	Selector.GetStrategic()   ↔ &Manager.Strategic  (*Strategic)
//
// Manager will implement these three methods (small wrappers returning the fields) so that
// func Select(m Selector, ...) can be called as Select(manager, ...) where manager is *Manager.
type Selector interface {
	GetPlayer() uint8
	GetProfile() *Profile
	GetStrategic() *Strategic
}

// ScoreInputs carries the economy inputs for the C6 formula in float32 [08 "Established AI-facing data and rooted planner"] [PLAN 11 C6] [INVARIANTS I2].
type ScoreInputs struct {
	CurEnergy  float32
	CapEnergy  float32
	CurMetal   float32
	CapMetal   float32
	NetEnergy  float32
	NetMetal   float32
	ProdEnergy float32
	ProdMetal  float32
}

// AICatalog is the content catalog used to enumerate candidate build options for a builder.
// Set by session init (WU-14). When nil, Select falls back to CandidateSource or to an empty list.
// TODO(question): wiring of catalog into AI is not closed in [08]; this indirection keeps selection pure for tests.
var AICatalog *content.Catalog

// CandidateSource overrides build-option enumeration for tests. When non-nil it is called instead of AICatalog.
var CandidateSource func(builder *units.Unit) []string

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// 0 = inactive (retail non-mission), 1 = active. Used with Gate241Candidates below.
var MissionGateFlag int32

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
var Gate241Candidates map[string]struct{}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(T25): exact definition field identity and mission-mode interaction is blocked; placeholder logic: MissionGateFlag==1 && key in Gate241Candidates.
func hasGate241(candidateKey string) bool {
	if MissionGateFlag != 1 {
		return false
	}
	if Gate241Candidates == nil {
		return false
	}
	_, ok := Gate241Candidates[canonicalKey(candidateKey)]
	return ok
}

// ScoreInputsFromEconomy derives ScoreInputs from an economy.Service snapshot for player [08 "Established AI-facing data and rooted planner"] [PLAN 11 C6] [05 "Player slot"].
// Cur/Cap are Stock/Capacity [05 "Player slot"] [INVARIANTS I2]; Prod/Net are per-pass production values.
// TODO(question): [08] does not name the exact ledger fields for prod/net; retail sums unit tables (openta-go/view uses unit EnergyMake etc)
// while this implementation reads PassProduced/PassConsumed. Gate and score fixtures remain valid because they inject ScoreInputs directly;
// the economy-derived path is documented as one plausible wiring and stays replaceable.
func ScoreInputsFromEconomy(econ *economy.Service, player uint8) ScoreInputs {
	if econ == nil || int(player) >= len(econ.Players) {
		return ScoreInputs{}
	}
	p := &econ.Players[player]
	curE := p.Stock[economy.Energy]
	capE := p.Capacity[economy.Energy]
	curM := p.Stock[economy.Metal]
	capM := p.Capacity[economy.Metal]
	prodE := p.PassProduced[economy.Energy]
	prodM := p.PassProduced[economy.Metal]
	// Net as production minus consumption for the pass.
	netE := prodE - p.PassConsumed[economy.Energy]
	netM := prodM - p.PassConsumed[economy.Metal]
	return ScoreInputs{
		CurEnergy:  curE,
		CapEnergy:  capE,
		CurMetal:   curM,
		CapMetal:   capM,
		ProdEnergy: prodE,
		ProdMetal:  prodM,
		NetEnergy:  netE,
		NetMetal:   netM,
	}
}

// energyRaw computes the energyRaw term exactly as written [PLAN 11 C6] [08 "Established AI-facing data and rooted planner"].
// Evaluate in float32, trunc toward zero [01 §8] [INVARIANTS I3]; TODO(question) on x87 spill retention.
func energyRaw(in ScoreInputs) int32 {
	capped := in.CapEnergy
	if capped > 1000 {
		capped = float32(1000) // [08] min(cap,1000) [PLAN 11 C6]
	}
	diff := capped - in.CurEnergy   // float32
	scaled := diff * float32(0.125) // float32
	if scaled < 0 {
		scaled = 0
	}
	raw := int32(scaled) // trunc toward zero [01 §8] [INVARIANTS I3]
	if in.NetEnergy < 1 {
		raw += 20
	}
	if in.ProdEnergy < 50 {
		raw += 100
	} else if in.ProdEnergy < 200 {
		raw += 10
	}
	return raw
}

// metalRaw computes the metalRaw term exactly as written [PLAN 11 C6] [08].
func metalRaw(in ScoreInputs) int32 {
	capped := in.CapMetal
	if capped > 500 {
		capped = float32(500) // [08] min(cap,500) [PLAN 11 C6]
	}
	diff := capped - in.CurMetal
	scaled := diff * float32(0.25)
	if scaled < 0 {
		scaled = 0
	}
	raw := int32(scaled)
	if in.NetMetal < 1 {
		raw += 20
	}
	if in.ProdMetal < 3 {
		raw += 100
	} else if in.ProdMetal < 5 {
		raw += 20
	}
	return raw
}

// ComputeMix computes the three-way mix from raw values per [PLAN 11 C6] [08].
func ComputeMix(in ScoreInputs) (metalMix, energyMix, otherMix int32) {
	mRaw := metalRaw(in)
	eRaw := energyRaw(in)
	// metalMix = clamp(metalRaw,0,100) [PLAN 11 C6]
	mMix := mRaw
	if mMix < 0 {
		mMix = 0
	}
	if mMix > 100 {
		mMix = 100
	}
	// energyMix = clamp(energyRaw - metalMix,0,100) [PLAN 11 C6]
	eMix := eRaw - mMix
	if eMix < 0 {
		eMix = 0
	}
	if eMix > 100 {
		eMix = 100
	}
	// otherMix = max(0,100-metalMix-energyMix) [PLAN 11 C6]
	oMix := int32(100) - mMix - eMix
	if oMix < 0 {
		oMix = 0
	}
	return mMix, eMix, oMix
}

// ComputeScore computes the C6 score exactly as the plan block quotes [PLAN 11 C6] [08] with truncation as written.
// Resource inputs are float32 temporaries with TODO(question) on x87 spills [INVARIANTS I2]; final trunc is integer division trunc toward zero [01 §8] [INVARIANTS I3].
func ComputeScore(in ScoreInputs, cv ClassVector, weight int32) int32 {
	// Clamp weight [0,100] per [08] [PLAN 11 C4]
	if weight < 0 {
		weight = 0
	}
	if weight > 100 {
		weight = 100
	}
	mMix, eMix, oMix := ComputeMix(in)
	total := int32(cv.C0)*oMix + int32(cv.C1)*mMix + int32(cv.C2)*eMix
	// score = trunc((class0*otherMix + class1*metalMix + class2*energyMix) * weight / 10000) [PLAN 11 C6]
	// Integer trunc toward zero via Go's /.
	return total * weight / 10000
}

// EnergyRaw is an exported accessor for TestScoreFormula to verify the worked intermediate [PLAN 11 Tests].
func EnergyRaw(in ScoreInputs) int32 { return energyRaw(in) }

// MetalRaw is an exported accessor for tests.
func MetalRaw(in ScoreInputs) int32 { return metalRaw(in) }

func buildOptionsForBuilder(builder *units.Unit) []string {
	if CandidateSource != nil {
		out := CandidateSource(builder)
		cp := make([]string, len(out))
		copy(cp, out)
		return cp
	}
	if AICatalog != nil && AICatalog.BuildMenus != nil && builder != nil && builder.Def != nil {
		key := canonicalKey(builder.Def.UnitName)
		if key == "" {
			key = canonicalKey(builder.Def.CanonicalKey)
		}
		if page, ok := AICatalog.BuildMenus[key]; ok && page != nil {
			out := make([]string, len(page.Buttons))
			copy(out, page.Buttons)
			return out
		}
		if builder.Def.CanonicalKey != "" {
			if page, ok := AICatalog.BuildMenus[builder.Def.CanonicalKey]; ok && page != nil {
				out := make([]string, len(page.Buttons))
				copy(out, page.Buttons)
				return out
			}
		}
	}
	return nil
}

// SelectWithCandidates is the testable core of candidate selection with explicit candidate list.
// It implements C5 gates, C6 scoring, C7 reservoir (single RNG(cumulative) draw), C9 bound census [PLAN 11].
func SelectWithCandidates(m Selector, builder *units.Unit, econ *economy.Service, candidates []string) (Candidate, bool) {
	if m == nil || builder == nil || builder.Def == nil || econ == nil {
		return Candidate{}, false
	}
	if len(candidates) == 0 {
		return Candidate{}, false
	}
	player := m.GetPlayer()
	if int(player) >= len(econ.Players) {
		return Candidate{}, false
	}
	profile := m.GetProfile()
	strat := m.GetStrategic()

	// Deterministic iteration: authored BuildMenu order (canbuild1..N ascending) is already stable via Catalog enumeration (I1 via sorted ReadDir [content]).
	// Preserve provided order for retail fidelity [08 "Established AI-facing data and rooted planner"]; no sorting here.
	// Caller must provide deterministically ordered slice; BuildMenus already does. This avoids map randomization.
	cands := candidates

	curEnergy := econ.Players[player].Stock[economy.Energy]
	curMetal := econ.Players[player].Stock[economy.Metal]
	builderKey := canonicalKey(builder.Def.UnitName)
	if builderKey == "" {
		builderKey = canonicalKey(builder.Def.CanonicalKey)
	}

	type scored struct {
		key   string
		score int32
	}
	var positives []scored
	var total int32 // cumulative [PLAN 11 C7] [PLAN 11 C9]

	// Inputs derived once per selection (economy-mixed, not per candidate varying) [08].
	in := ScoreInputsFromEconomy(econ, player)

	for _, candKeyRaw := range cands {
		candKey := candKeyRaw
		ck := canonicalKey(candKey)
		if ck == "" {
			continue
		}
		// C5: candidate == builder's own definition rejected [08] [PLAN 11 C5]
		if ck == builderKey {
			continue
		}
		// C5: currentEnergy <50 gate [08] [PLAN 11 C5]
		if curEnergy < 50 {
			continue
		}
		// C5: currentMetal <25 gate [08] [PLAN 11 C5]
		if curMetal < 25 {
			continue
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		if hasGate241(ck) {
			continue
		}
		// C5: profile limit (count < limit or -1) [08] [PLAN 11 C5]
		limit := int32(-1)
		if profile != nil {
			limit = profile.LimitFor(ck)
		}
		var count int32
		if strat != nil && strat.Counts != nil {
			count = strat.Counts[ck]
		}
		if limit != -1 && count >= limit {
			continue
		}
		// C6 scoring [PLAN 11 C6] [08]
		var cv ClassVector
		if strat != nil && strat.ClassVectors != nil {
			if v, ok := strat.ClassVectors[ck]; ok {
				cv = v
			} else {
				// Fallback placeholder: 40,0,0 per recomputeClassVectors placeholder [strategic.go]
				cv = ClassVector{C0: 40, C1: 0, C2: 0}
			}
		} else {
			cv = ClassVector{C0: 40, C1: 0, C2: 0}
		}
		var weight int32 = 100
		if profile != nil {
			weight = profile.WeightFor(ck)
		}
		score := ComputeScore(in, cv, weight)
		if score <= 0 {
			continue
		}
		positives = append(positives, scored{key: candKeyRaw, score: score})
		total += score
	}

	// C9 bound census: cumulative total is the only variable bound in this file [PLAN 11 C9] [08].
	// C7: cumulative weighted reservoir: ONE rng.Global.Sim draw of RNG(cumulative) yielding score/finalTotal; positive scores only [PLAN 11 C7] [08].
	if len(positives) == 0 || total <= 0 {
		return Candidate{}, false
	}
	// Bounds below 2 do not advance the stream per [01 §7.1] [INVARIANTS I4]; RNG(cumulative) is 0 without draw.
	if total < 2 {
		return Candidate{DefKey: positives[0].key, Score: positives[0].score}, true
	}
	if rng.Global.Sim == nil {
		// No global stream seeded; fall back to picking first positive (deterministic) to keep tests that do not seed from panicking.
		return Candidate{DefKey: positives[0].key, Score: positives[0].score}, true
	}
	// Single draw [PLAN 11 C7]
	draw := rng.Global.Sim.Uint32n(uint32(total)) // I4 call order is behavior [01 §7.1] [INVARIANTS I4]
	// Walk cumulative intervals
	var cum int32
	for _, p := range positives {
		cum += p.score
		if int32(draw) < cum {
			return Candidate{DefKey: p.key, Score: p.score}, true
		}
	}
	// Should not reach; fallback to last
	last := positives[len(positives)-1]
	return Candidate{DefKey: last.key, Score: last.score}, true
}

// Select is the public entry point per [PLAN 11 Public API] enumerated via build-option IDs.
// It resolves candidates via AICatalog/ CandidateSource and then delegates to SelectWithCandidates.
// The Selector interface decouples from the concrete Manager type while manager.go lands concurrently.
func Select(m Selector, builder *units.Unit, econ *economy.Service) (Candidate, bool) {
	cands := buildOptionsForBuilder(builder)
	return SelectWithCandidates(m, builder, econ, cands)
}
