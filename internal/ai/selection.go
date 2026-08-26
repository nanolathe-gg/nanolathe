package ai

import (
	"fmt"
	"os"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// aiDebug reports whether AI decision diagnostics are enabled [OX P2].
// Presentation-only stderr logging; never touches sim state [I6].
func aiDebug() bool { return os.Getenv("NANOLATHE_AI_DEBUG") == "1" }

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

// P0-I16: Authoritative hooks moved onto Manager. Immutable tables remain package-level.
// The previous package globals AICatalog, CandidateSource, MissionGateFlag, Gate241Candidates
// are now fields on Manager (CandidateSource, Catalog, MissionGateFlag, GateCandidates).
// hasGate241 now takes per-manager state [P0-I16].

func getCandidateSource(m Selector) func(builder *units.Unit) []string {
	if m == nil {
		return nil
	}
	if cs, ok := m.(interface {
		GetCandidateSource() func(*units.Unit) []string
	}); ok {
		return cs.GetCandidateSource()
	}
	return nil
}

func getCatalog(m Selector) *content.Catalog {
	if m == nil {
		return nil
	}
	if gc, ok := m.(interface{ GetCatalog() *content.Catalog }); ok {
		return gc.GetCatalog()
	}
	return nil
}

func getGateFlag(m Selector) int32 {
	if m == nil {
		return 0
	}
	if gf, ok := m.(interface{ GetMissionGateFlag() int32 }); ok {
		return gf.GetMissionGateFlag()
	}
	return 0
}

func getGateCandidates(m Selector) map[string]struct{} {
	if m == nil {
		return nil
	}
	if gc, ok := m.(interface{ GetGateCandidates() map[string]struct{} }); ok {
		return gc.GetGateCandidates()
	}
	return nil
}

func getSelectorRNG(m Selector) *rng.Simulation {
	// Single global simulation stream per I4 and RS-02 [08] — no per-manager RNG.
	return rng.Global.Sim
}

// isWaterOnlyExtractor reports whether def extracts metal and authors a water
// depth floor, i.e. it can only ever be placed in water (coruwmex.fbi
// minwaterdepth=10 vs cormex.fbi none) [02 "Unit record"][P0-03].
func isWaterOnlyExtractor(def *content.UnitDef) bool {
	return def != nil && def.ExtractsMetal != 0 && def.MinWaterDepth > 0
}

// filterWaterExtractors demotes water-only extractor candidates while any land
// extractor remains, so the planner expands on land first and does not pin the
// only builder on a slow shore site [OX P3][P0-03]. TODO(question): the exact
// retail mechanism that deprioritizes water extractors early is not located in
// p0-03; this encodes the observed land-first behavior with authored FBI data.
func filterWaterExtractors(m Selector, candidates []string) []string {
	cat := getCatalog(m)
	if cat == nil || len(candidates) == 0 {
		return candidates
	}
	hasLand := false
	for _, key := range candidates {
		def, ok := cat.Unit(key)
		if !ok || def == nil {
			continue
		}
		if def.ExtractsMetal != 0 && !isWaterOnlyExtractor(def) {
			hasLand = true
			break
		}
	}
	if !hasLand {
		return candidates
	}
	out := make([]string, 0, len(candidates))
	for _, key := range candidates {
		def, ok := cat.Unit(key)
		if ok && def != nil && isWaterOnlyExtractor(def) {
			continue
		}
		out = append(out, key)
	}
	return out
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(T25): exact definition field identity and mission-mode interaction is blocked; placeholder logic: flag==1 && key in candidates [P0-I16].
func hasGate241(candidateKey string, m Selector) bool {
	flag := getGateFlag(m)
	if flag != 1 {
		return false
	}
	cands := getGateCandidates(m)
	if cands == nil {
		return false
	}
	_, ok := cands[canonicalKey(candidateKey)]
	return ok
}

// ScoreInputsFromEconomy derives ScoreInputs from an economy.Service snapshot for player [08 "Established AI-facing data and rooted planner"] [PLAN 11 C6] [05 "Player slot"].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func ScoreInputsFromEconomy(econ *economy.Service, player uint8) ScoreInputs {
	if econ == nil || int(player) >= len(econ.Players) {
		return ScoreInputs{}
	}
	p := &econ.Players[player]
	curE := p.Stock[economy.Energy]
	capE := p.Capacity[economy.Energy]
	curM := p.Stock[economy.Metal]
	capM := p.Capacity[economy.Metal]
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Direct PassProduced alone is per-pass production without leftover and stays <3 for metal, keeping metalMix at 100 [P2].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	prodEInclusive := p.Stock[economy.Energy] + p.PassProduced[economy.Energy]
	prodMInclusive := p.Stock[economy.Metal] + p.PassProduced[economy.Metal]
	prodE := prodEInclusive
	prodM := prodMInclusive
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

func buildOptionsForBuilder(m Selector, builder *units.Unit) []string {
	cs := getCandidateSource(m)
	if cs != nil {
		out := cs(builder)
		cp := make([]string, len(out))
		copy(cp, out)
		return cp
	}
	cat := getCatalog(m)
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
			if page, ok := cat.BuildMenus[builder.Def.CanonicalKey]; ok && page != nil {
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
	cands := filterWaterExtractors(m, candidates)

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
		if hasGate241(ck, m) {
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
		var hit bool
		if strat != nil && strat.ClassVectors != nil {
			if v, ok := strat.ClassVectors[ck]; ok {
				cv = v
				hit = true
			} else {
				cv = ClassVector{C0: 40, C1: 0, C2: 0}
			}
		} else {
			cv = ClassVector{C0: 40, C1: 0, C2: 0}
		}
		if !hit && os.Getenv("NANOLATHE_AI_DEBUG") != "" {
			// Debug logging behind env to diagnose vector hit misses [REVIEW_OX_ALPHA P2].
			// Ensure vector lookups hit real entries; fallback indicates catalog vs strategic key mismatch.
			fmt.Fprintf(os.Stderr, "ai: class-vector miss ck=%q candidate=%q builder=%q vectors=%d\n", ck, candKeyRaw, builderKey, len(strat.ClassVectors))
		}
		var weight int32 = 100
		if profile != nil {
			weight = profile.WeightFor(ck)
		}
		score := ComputeScore(in, cv, weight)
		if aiDebug() {
			fmt.Printf("ai-debug: player=%d builder=%s cand=%s cv=%+v weight=%d score=%d in=%+v\n",
				player, builderKey, ck, cv, weight, score, in)
		}
		if score <= 0 {
			continue
		}
		positives = append(positives, scored{key: candKeyRaw, score: score})
		total += score
	}

	// C9 bound census: cumulative total is the only variable bound in this file [PLAN 11 C9] [08].
	// C7: cumulative weighted reservoir: ONE global RNG draw of RNG(cumulative) yielding score/finalTotal; positive scores only [PLAN 11 C7] [08] [I4] RS-02.
	if len(positives) == 0 || total <= 0 {
		return Candidate{}, false
	}
	// Bounds below 2 do not advance the stream per [01 §7.1] [INVARIANTS I4]; RNG(cumulative) is 0 without draw.
	if total < 2 {
		return Candidate{DefKey: positives[0].key, Score: positives[0].score}, true
	}
	rngStream := getSelectorRNG(m)
	if rngStream == nil {
		// Global simulation stream not seeded (should not happen in production); deterministic fallback without draw.
		return Candidate{DefKey: positives[0].key, Score: positives[0].score}, true
	}
	// Single draw [PLAN 11 C7] single global stream [I4][RS-02].
	draw := rngStream.Uint32n(uint32(total)) // I4 call order is behavior [01 §7.1] [INVARIANTS I4]
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
// It resolves candidates via Manager.Catalog/ CandidateSource and then delegates to SelectWithCandidates [P0-I16].
// The Selector interface decouples from the concrete Manager type while manager.go lands concurrently.
func Select(m Selector, builder *units.Unit, econ *economy.Service) (Candidate, bool) {
	cands := buildOptionsForBuilder(m, builder)
	return SelectWithCandidates(m, builder, econ, cands)
}
