package ai

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TODO(T23): the AI resource-score expressions (energyRaw/metalRaw) are
// float32 temporaries per I2. Doc 08 bounds the residual as platform class:
// narrowing to float32 at every helper invocation boundary is established, and
// only the control-word edge beyond those points is unknown
// [08 "What remains not established"]. This file therefore evaluates the named
// energyRaw and metalRaw expressions in float32 and narrows at the shown
// truncations [08 "Established AI-facing data and rooted planner"]
// [PLAN 11 C6] [INVARIANTS I2].

// Candidate is the selected build target [PLAN 11 Public API].
type Candidate struct {
	DefKey string
	Score  int32
}

// Selector is the documented manager state required by the pure selection
// core. The catalog and simulation RNG are optional interface extensions so
// existing callers that only exercise explicit candidate lists keep compiling;
// live selection requires both bindings [PLAN 11].
type Selector interface {
	GetPlayer() uint8
	GetProfile() *Profile
	GetStrategic() *Strategic
}

type selectorBindings interface {
	GetCatalog() *content.Catalog
	GetRNG() *rng.Simulation
}

// selectorMissionMode is implemented by Manager's concrete mission-mode
// binding. Selection cannot safely assume this mode is zero when the binding
// is absent because mode one activates the authored definition gate [R-P0-05
// §3].
type selectorMissionMode interface {
	GetMissionGateFlag() int32
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

// ScoreInputsFromEconomy derives ScoreInputs from the settled player record for
// player [08 "Established AI-facing data and rooted planner"] [R-P0-05].
// Cur/Cap are the player's settled stock and capacity fields [05 "Player
// slot"] [INVARIANTS I2]. Prod/Net read the four settled strategic runtime
// aggregates exposed by the economy adapter as AIProduction and
// AIConsumption. They are deliberately not reconstructed
// from stock or PassProduced: those are separate fields with separate
// settlement/reporting lifetimes [R-P0-05].
func ScoreInputsFromEconomy(econ *economy.Service, player uint8) ScoreInputs {
	if econ == nil || int(player) >= len(econ.Players) {
		return ScoreInputs{}
	}
	p := &econ.Players[player]
	curE := p.Stock[economy.Energy]
	capE := p.Capacity[economy.Energy]
	curM := p.Stock[economy.Metal]
	capM := p.Capacity[economy.Metal]
	prodE := p.AIProduction[economy.Energy]
	prodM := p.AIProduction[economy.Metal]
	consE := p.AIConsumption[economy.Energy]
	consM := p.AIConsumption[economy.Metal]
	netE := prodE - consE
	netM := prodM - consM
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
// Evaluate in float32, trunc toward zero [01 §8] [INVARIANTS I3]; the x87
// control-word residual is the file-level platform-residual marker.
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
	raw := numeric.TruncateFloat32ToLow32(scaled) // trunc toward zero [01 §8] [INVARIANTS I3]
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
	raw := numeric.TruncateFloat32ToLow32(scaled)
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
// Resource inputs are float32 temporaries carrying the file-level platform-residual
// x87 marker's uncertainty [INVARIANTS I2]; the final trunc is integer division truncating
// toward zero [01 §8] [INVARIANTS I3].
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

func buildOptionsForBuilder(m Selector, builder *units.Unit) []string {
	b, ok := m.(interface{ GetCatalog() *content.Catalog })
	if !ok || b == nil {
		return nil
	}
	cat := b.GetCatalog()
	if cat != nil && cat.BuildMenus != nil && builder != nil && builder.Def != nil {
		key := canonicalKey(builder.Def.UnitName)
		if key == "" {
			key = canonicalKey(builder.Def.CanonicalKey)
		}
		if page, ok := cat.BuildMenus[key]; ok && page != nil {
			out := make([]string, len(page.Buttons))
			copy(out, page.Buttons)
			return out
		}
		if builder.Def.CanonicalKey != "" {
			if page, ok := cat.BuildMenus[canonicalKey(builder.Def.CanonicalKey)]; ok && page != nil {
				out := make([]string, len(page.Buttons))
				copy(out, page.Buttons)
				return out
			}
		}
	}
	return nil
}

// computerPlayerCount is the number of player slots whose record exists and
// whose control byte is 2 [08 R-AI-01 §12], which is how many times the profile
// grammar's per-definition pass pair runs [08 R-AI-01 §18]. The rows are walked
// by index, never ranged (I1).
func computerPlayerCount(econ *economy.Service) int {
	if econ == nil {
		return 0
	}
	n := 0
	for i := 0; i < len(econ.Players); i++ {
		if econ.Players[i].Exists && econ.Players[i].ControllerState == controlByteComputer {
			n++
		}
	}
	return n
}

// SelectWithCandidates is the testable core of candidate selection with explicit candidate list.
// It implements C5 gates, C6 scoring, and C7's running-total reservoir
// [08 R-AI-01 §8].
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
	// A live manager is bound to one loaded profile and one initialized
	// strategic state. Missing state is a setup error, not an alternate policy
	// [08 "Established AI-facing data and rooted planner"].
	if profile == nil || strat == nil || strat.ClassVectors == nil {
		return Candidate{}, false
	}
	mission, ok := m.(selectorMissionMode)
	if !ok || mission == nil {
		return Candidate{}, false
	}
	missionMode := mission.GetMissionGateFlag()
	// The profile grammar needs the definition catalog for its name matcher and
	// its two lock vectors, so the whole of [08 R-AI-01 §12] is applied here,
	// before the first score is read; direct Manager fixtures and NewManager
	// therefore follow the same profile state. Profile-file precedence over a
	// per-definition `ai_weight` fragment is the lock vectors' job, and
	// `ai_limit` stays inert because the limit pass re-reads `ai_weight`.
	//
	// The two per-definition passes run once PER COMPUTER PLAYER and every run
	// writes every manager [08 R-AI-01 §18], so the count comes from the
	// authoritative player rows this function already reads — the same rows
	// whose control byte gates `limit` below. With k of them a category-naming
	// fragment multiplies its members' weights 2·k times; an exact naming
	// applies once and locks. The applier memoises on the catalog, so only the
	// first manager to score does the work and every later manager sees the
	// same table.
	if bindings, ok := m.(selectorBindings); ok && bindings != nil {
		profile.ApplyUnitDefinitionsForPlayers(bindings.GetCatalog(), computerPlayerCount(econ))
	}
	// `limit` applies only to slots whose control byte is 2 [08 R-AI-01 §12];
	// the weight table applies to every slot that has a manager.
	controlByte := econ.Players[player].ControllerState
	// The caller supplies the authored order; never rebuild it through a map.
	cands := candidates

	// Deterministic iteration: authored BuildMenu order (canbuild1..N ascending) is already stable via Catalog enumeration (I1 via sorted ReadDir [content]).
	// Preserve provided order for retail fidelity [08 "Established AI-facing data and rooted planner"]; no sorting here.
	// Caller must provide deterministically ordered slice; BuildMenus already does. This avoids map randomization.
	curEnergy := econ.Players[player].Stock[economy.Energy]
	curMetal := econ.Players[player].Stock[economy.Metal]
	var total int32
	var selected Candidate
	selectedOK := false
	var rngStream *rng.Simulation

	// Inputs derived once per selection (economy-mixed, not per candidate varying) [08].
	in := ScoreInputsFromEconomy(econ, player)

	for _, candKeyRaw := range cands {
		candKey := candKeyRaw
		ck := canonicalKey(candKey)
		if ck == "" {
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
		// C5: profile limit (count < limit or -1) [08] [PLAN 11 C5]
		def := strat.lookupDef(ck)
		if strat.Catalog != nil && def == nil {
			// The base-menu compiler retains unresolved names. Retail's invalid
			// type gate rejects only this candidate before the limit/vector reads
			// or reservoir draw [02 R-CAT-01 §5][08 R-AI-02 §2].
			continue
		}
		limit := profile.LimitForControl(controlByte, ck)
		if def != nil {
			limit = profile.LimitForDefinition(controlByte, def)
		}
		// TODO(question): strategic Counts/ClassVectors and Candidate.DefKey
		// still use names. Carry catalog indices through their producers before
		// selecting later equal-name records [02 R-CAT-01 §5][08 R-AI-01 §8].
		var count int32
		if strat != nil && strat.Counts != nil {
			count = strat.Counts[ck]
		}
		if limit != -1 && count >= limit {
			continue
		}
		if missionMode == 1 {
			def := strat.lookupDef(ck)
			if def == nil {
				// A live candidate must resolve through the concrete catalog before
				// the authored status gate can be evaluated [R-P0-05 §3].
				return Candidate{}, false
			}
			if def.Downloadable {
				continue
			}
		}
		// C6 scoring [PLAN 11 C6] [08]
		cv, ok := strat.ClassVectors[ck]
		if !ok {
			// Construction initializes a vector for every catalog type. A miss
			// means the strategic state is not ready [08].
			return Candidate{}, false
		}
		weight := profile.WeightFor(ck)
		if def != nil {
			weight = profile.WeightForDefinition(def)
		}
		score := ComputeScore(in, cv, weight)
		if score <= 0 {
			continue
		}

		// The total is a signed 32-bit accumulator. The bounded helper receives
		// its bit pattern and performs retail's signed below-two test itself, so
		// an overflowed or sub-two total does not advance the stream [01 §7.1].
		total += score
		if rngStream == nil {
			bindings, ok := m.(selectorBindings)
			if !ok || bindings == nil || bindings.GetRNG() == nil {
				return Candidate{}, false
			}
			rngStream = bindings.GetRNG()
		}
		draw := rngStream.Uint32n(uint32(total))
		// Each positive candidate gets one draw after joining the running total.
		// The signed comparison is against this candidate's score, not the total
		// or the previously selected score [08 R-AI-01 §8].
		if int32(draw) < score {
			selected = Candidate{DefKey: candKeyRaw, Score: score}
			selectedOK = true
		}
	}

	return selected, selectedOK
}

// selectedCandidateForBuilder applies the post-reservoir side filter. The
// selected definition is not filtered before the draws: a side mismatch wastes
// the completed reservoir selection and returns no candidate, with no re-draw or runner-up
// [08 R-AI-01 §8]. Authored side strings compare byte-for-byte and
// case-sensitively.
func selectedCandidateForBuilder(strat *Strategic, builder *units.Unit, key string, score int32) (Candidate, bool) {
	if strat == nil || builder == nil || builder.Def == nil {
		return Candidate{}, false
	}
	selected := strat.lookupDef(canonicalKey(key))
	if selected == nil || selected.Side != builder.Def.Side {
		return Candidate{}, false
	}
	return Candidate{DefKey: key, Score: score}, true
}

// Select is the public entry point per [PLAN 11 Public API]. It resolves the
// builder's authored build menu and preserves its button order [02 §5; 08
// "Established AI-facing data and rooted planner"].
func Select(m Selector, builder *units.Unit, econ *economy.Service) (Candidate, bool) {
	cands := buildOptionsForBuilder(m, builder)
	selected, ok := SelectWithCandidates(m, builder, econ, cands)
	if !ok {
		return Candidate{}, false
	}
	return selectedCandidateForBuilder(m.GetStrategic(), builder, selected.DefKey, selected.Score)
}
