package ai

import (
	"math"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type testSelector struct {
	player      uint8
	profile     *Profile
	strategic   *Strategic
	catalog     *content.Catalog
	rng         *rng.Simulation
	missionMode int32
}

func (s *testSelector) GetPlayer() uint8             { return s.player }
func (s *testSelector) GetProfile() *Profile         { return s.profile }
func (s *testSelector) GetStrategic() *Strategic     { return s.strategic }
func (s *testSelector) GetCatalog() *content.Catalog { return s.catalog }
func (s *testSelector) GetMissionGateFlag() int32    { return s.missionMode }
func (s *testSelector) GetRNG() *rng.Simulation {
	// DET-01: tests seed the global streams; return the CURRENT stream so
	// reseeds between sub-cases are observed, exactly like an injected
	// session stream would be.
	if s.rng != nil {
		return s.rng
	}
	return rng.Global.Sim
}

func testBuilder(defKey string) *units.Unit {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(defKey)},
		UnitName:         defKey,
	}
	return &units.Unit{Def: def, Owner: 1, Alive: true}
}

func testEcon(player uint8, curEnergy, capEnergy, curMetal, capMetal float32, prodE, prodM, consE, consM float32) *economy.Service {
	var svc economy.Service
	// Ensure player slot exists. The slot is a computer player: the profile
	// grammar's `limit` directive applies only to a slot whose control byte is
	// 2 [08 R-AI-01 §12] [05 R-SHARE-01 §1].
	svc.Players[player].ControllerState = 2
	svc.Players[player].Stock[economy.Energy] = curEnergy
	svc.Players[player].Capacity[economy.Energy] = capEnergy
	svc.Players[player].Stock[economy.Metal] = curMetal
	svc.Players[player].Capacity[economy.Metal] = capMetal
	svc.Players[player].AIProduction[economy.Energy] = prodE
	svc.Players[player].AIProduction[economy.Metal] = prodM
	svc.Players[player].AIConsumption[economy.Energy] = consE
	svc.Players[player].AIConsumption[economy.Metal] = consM
	return &svc
}

// TestScoreFormula locks the worked case from [08 "Established AI-facing data and rooted planner"] and [PLAN 11 Tests].
// zero energy, cap 1000, zero production ⇒ energyRaw = 125 +20+100, then clamps and final truncation.
func TestScoreFormula(t *testing.T) {
	// Inputs producing starved case: both rams 245 as in openta-go/starved: energyRaw 245 metalRaw 245 via zero prod and net<1.
	in := ScoreInputs{
		CurEnergy: 0, CapEnergy: 1000, NetEnergy: 0, ProdEnergy: 0,
		CurMetal: 0, CapMetal: 500, NetMetal: 0, ProdMetal: 0,
	}
	if got := EnergyRaw(in); got != 245 {
		t.Fatalf("energyRaw starved = %d want 245 (125+20+100) [PLAN 11 Tests]", got)
	}
	if got := MetalRaw(in); got != 245 {
		t.Fatalf("metalRaw starved = %d want 245 (125+20+100) [PLAN 11 Tests]", got)
	}
	mMix, eMix, oMix := ComputeMix(in)
	if mMix != 100 || eMix != 100 || oMix != 0 {
		t.Fatalf("mix starved = %d/%d/%d want 100/100/0", mMix, eMix, oMix)
	}
	// calm case from openta-go: energy 800 cap1000 metal400 cap500 net5 prod 300/10
	inCalm := ScoreInputs{CurEnergy: 800, CapEnergy: 1000, CurMetal: 400, CapMetal: 500, NetEnergy: 5, NetMetal: 5, ProdEnergy: 300, ProdMetal: 10}
	if got := EnergyRaw(inCalm); got != 25 {
		t.Fatalf("energyRaw calm = %d want 25", got)
	}
	if got := MetalRaw(inCalm); got != 25 {
		t.Fatalf("metalRaw calm = %d want 25", got)
	}
	mMix, eMix, oMix = ComputeMix(inCalm)
	if mMix != 25 || eMix != 0 || oMix != 75 {
		t.Fatalf("mix calm = %d/%d/%d want 25/0/75", mMix, eMix, oMix)
	}
	// Final truncation check: with starved mix and class 40,30,30 weight100 => 60 as in openta-go
	cv := ClassVector{C0: 40, C1: 30, C2: 30}
	score := ComputeScore(in, cv, 100)
	if score != 60 {
		t.Fatalf("score starved 40,30,30 weight100 = %d want 60", score)
	}
	// Half-weight starved with mix 25/75/0 => total 40*25+30*75=3250 *50/10000=16
	inMid := ScoreInputs{CurEnergy: 100, CapEnergy: 500, CurMetal: 200, CapMetal: 500, NetEnergy: 1, NetMetal: 2, ProdEnergy: 120, ProdMetal: 8}
	// This corresponds to openta-go mid case: metal 75 energy 0 other25 but we check generic
	_ = inMid
	// Zero weight => score 0
	if s := ComputeScore(in, cv, 0); s != 0 {
		t.Fatalf("zero weight score %d want 0", s)
	}
	// Negative coefficient
	cvNeg := ClassVector{C0: -40, C1: 30, C2: 30}
	inNeg := inCalm // mix 25/0/75 => -40*75+30*25 = -3000+750=-2250 /10000*100 = -22
	if s := ComputeScore(inNeg, cvNeg, 100); s != -22 {
		t.Fatalf("negative coefficient score %d want -22", s)
	}
}

// TestReservoirDrawsPerPositiveCandidate compares the selector with an
// independently authored running-total reference [08 R-AI-01 §8].
func TestReservoirDrawsPerPositiveCandidate(t *testing.T) {
	seed := uint32(12345)
	rng.SeedGlobal(seed, 0)
	sel := &testSelector{
		player: 1,
		profile: &Profile{
			Weight: map[string]int32{"armfav": 100, "corfav": 100, "armship": 100},
			Limit:  map[string]int32{},
		},
		strategic: &Strategic{
			Counts:       map[string]int32{},
			ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}, "corfav": {C0: 40, C1: 30, C2: 30}, "armship": {C0: 40, C1: 30, C2: 30}},
		},
	}
	builder := testBuilder("armcom")
	econ := testEcon(1, 800, 1000, 400, 500, 300, 10, 0, 0) // use settled runtime aggregates for positive scores

	// Use explicit candidates that will have positive scores
	cands := []string{"armfav", "corfav", "armship"}
	// Ensure deterministic scores: we use economy inputs that yield positive via starved mix
	// Overwrite economy to starved-like but with curEnergy >=50, curMetal >=25 to pass C5 gates
	// diff still large: 1000-50=950 =>118+20+100=238
	econStarved := testEcon(1, 50, 1000, 25, 500, 0, 0, 0, 0)
	// Need to set AIConsumption zero so the settled net remains production.
	rng.SeedGlobal(seed, 0)
	before := rng.Global.Sim.Draws()
	got, ok := SelectWithCandidates(sel, builder, econStarved, cands)
	if !ok {
		t.Fatalf("selection should succeed")
	}
	after := rng.Global.Sim.Draws()
	if after-before != uint64(len(cands)) {
		t.Fatalf("reservoir draws: %d -> %d want +%d positive candidates", before, after, len(cands))
	}
	// The reference deliberately duplicates the stated loop instead of calling
	// the production selector: add one score, draw at that total, then compare
	// the signed result with that candidate's own score.
	probe := rng.NewSimulation(seed)
	var want Candidate
	var total int32
	for _, key := range cands {
		score := ComputeScore(ScoreInputsFromEconomy(econStarved, 1), sel.strategic.ClassVectors[key], sel.profile.WeightFor(key))
		if score <= 0 {
			continue
		}
		total += score
		if int32(probe.Uint32n(uint32(total))) < score {
			want = Candidate{DefKey: key, Score: score}
		}
	}
	if got != want || rng.Global.Sim.State != probe.State || rng.Global.Sim.Draws() != probe.Draws() {
		t.Fatalf("reservoir got candidate=%+v state/draws=%d/%d, want %+v %d/%d", got, rng.Global.Sim.State, rng.Global.Sim.Draws(), want, probe.State, probe.Draws())
	}

	// Two positive candidates consume two draws.
	rng.SeedGlobal(seed, 0)
	cands2 := []string{"armfav", "corfav"}
	before = rng.Global.Sim.Draws()
	_, _ = SelectWithCandidates(sel, builder, econStarved, cands2)
	after = rng.Global.Sim.Draws()
	if after-before != 2 {
		t.Fatalf("reservoir draws with 2 candidates: %d want +2", after-before)
	}
	// Total <2 should consume no draw per [01 §7.1]
	// Create a scenario where only one positive remains and total==1 => need special cands with weight to produce score 1
	// For determinism use weight 1 and class that yields 1
	rng.SeedGlobal(seed, 0)
	selOne := &testSelector{
		player:  1,
		profile: &Profile{Weight: map[string]int32{"armfav": 1}, Limit: map[string]int32{}},
		strategic: &Strategic{
			Counts:       map[string]int32{},
			ClassVectors: map[string]ClassVector{"armfav": {C0: 100, C1: 0, C2: 0}},
		},
	}
	// Need score 1: choose mix that yields total 100*? Let's craft inputs that give oMix 100, metal0 energy0 => total 100*100=10000 *1 /10000=1? Actually C0 100 * other 100 =10000 *1/10000=1
	// inputs starved with 245 gave mix metal100 energy100 other0 -> total 0 => not.
	// Use calm inputs: other75 metal25 energy0 => total C0*75=7500 *1/10000=0 -> not 1.
	// Test total==1 via weight tuning: with C0=100 and an all-other mix,
	// 100*100*1/10000 yields one.
	// To get 1, need total*weight ==10000. Choose cv {C0:100} other100 =>10000*1/10000=1.
	// So need mix other100.
	// Achieve other100 requires metalMix 0 energyMix 0 => metalRaw 0 energyRaw 0.
	// That requires prod >=200/5 etc and diff 0. Let's just brute: curEnergy 1000 cap1000 => diff0 => scaled0 => +0 net>=1 +0 prod>=200 =>0. So other100.
	econOne := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0) // use high prod to avoid bumps, net 300>1 etc so 0 raw

	// Set net to 2 (>1) to avoid +20, prod 300 to avoid +100 => raw 0
	econOne.Players[1].AIProduction[economy.Energy] = 300
	econOne.Players[1].AIProduction[economy.Metal] = 10
	econOne.Players[1].AIConsumption[economy.Energy] = 0 // net 300
	econOne.Players[1].AIConsumption[economy.Metal] = 0  // net 10

	// But our ScoreInputsFromEconomy net = prod - cons =300,10 both >1 so no +20, raw 0. Good.
	before = rng.Global.Sim.Draws()
	_, _ = SelectWithCandidates(selOne, builder, econOne, []string{"armfav"})
	after = rng.Global.Sim.Draws()
	if after-before != 0 {
		t.Fatalf("total<2 should not advance RNG [01 §7.1], draws %d want 0", after-before)
	}
	// Determinism: same seed gives same choice and stream state.
	rng.SeedGlobal(seed, 0)
	c1, _ := SelectWithCandidates(sel, builder, econStarved, cands)
	state1 := rng.Global.Sim.State
	rng.SeedGlobal(seed, 0)
	c2, _ := SelectWithCandidates(sel, builder, econStarved, cands)
	if c1 != c2 || state1 != rng.Global.Sim.State {
		t.Fatalf("reservoir deterministic: %+v/%d vs %+v/%d", c1, state1, c2, rng.Global.Sim.State)
	}
	_ = econ // keep
}

func TestReservoirNonpositiveCandidateBoundary(t *testing.T) {
	// A nonpositive candidate is skipped before the total or RNG helper. The
	// remaining score of one still enters the reservoir, but its bound also
	// consumes no draw.
	builder := testBuilder("armcom")
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)
	sim := rng.NewSimulation(91)
	before := sim.State
	sel := &testSelector{
		player: 1,
		profile: &Profile{Weight: map[string]int32{
			"negative": 100,
			"one":      1,
		}, Limit: map[string]int32{}},
		strategic: &Strategic{
			Counts: map[string]int32{},
			ClassVectors: map[string]ClassVector{
				"negative": {C0: -100},
				"one":      {C0: 100},
			},
		},
		rng: &sim,
	}
	got, ok := SelectWithCandidates(sel, builder, econ, []string{"negative", "one"})
	if !ok || got != (Candidate{DefKey: "one", Score: 1}) {
		t.Fatalf("nonpositive skip selection=%+v ok=%v, want one with score one", got, ok)
	}
	if sim.Draws() != 0 || sim.State != before {
		t.Fatalf("nonpositive/one-bound candidates advanced RNG: draws=%d state=%d want state=%d", sim.Draws(), sim.State, before)
	}
}

func TestSelectedSideMismatchConsumesPerPositiveDrawsWithoutRedraw(t *testing.T) {
	cross := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "cross"}, UnitName: "cross", Side: "CORE"}
	runnerUp := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "runner"}, UnitName: "runner", Side: "ARM"}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{"cross": cross, "runner": runnerUp},
		BuildMenus: map[string]*content.BuildMenuPage{
			"armcom": {Buttons: []string{"cross", "runner"}},
		},
	}
	strat := &Strategic{
		Catalog:      cat,
		Counts:       map[string]int32{"cross": 0, "runner": 0},
		ClassVectors: map[string]ClassVector{"cross": {C0: 100}, "runner": {C0: 100}},
	}
	profile := &Profile{Weight: map[string]int32{"cross": 100, "runner": 100}, Limit: map[string]int32{}}
	builder := testBuilder("armcom")
	builder.Def.Side = "ARM"
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)

	// Both candidates score 100. The first positive candidate always becomes
	// the tentative result. Choose a seed whose second draw does not replace
	// it, leaving the cross-side candidate selected.
	seed := uint32(1)
	for {
		probe := rng.NewSimulation(seed)
		_ = probe.Uint32n(100)
		if probe.Uint32n(200) >= 100 {
			break
		}
		seed++
	}
	sim := rng.NewSimulation(seed)
	sel := &testSelector{player: 1, profile: profile, strategic: strat, catalog: cat, rng: &sim}
	if got, ok := Select(sel, builder, econ); ok || got != (Candidate{}) {
		t.Fatalf("cross-side reservoir winner was not discarded: got=%+v ok=%v", got, ok)
	}
	if got := sim.Draws(); got != 2 {
		t.Fatalf("side mismatch draw count=%d, want one per positive candidate and no redraw", got)
	}
}

// TestCarrierScoreNeverPositiveWithoutMetalExtraction locks [08 R-AI-04 §2]'s
// "carriers are not built" finding: the class routine zeroes the other-mix
// coefficient for every `canload` definition outright, and its metal and
// energy coefficients (`clamp(100*extractsMetal - 0.02*buildCostMetal -
// 25*makesMetal)` and `clamp(-0.0025*buildCostEnergy - 5*Classify)`) carry no
// positive term when the definition does not extract metal — so for a
// `canload` definition that does not extract metal, all three coefficients
// sit at or below zero and the candidate score is at or below zero under
// every economy mix, regardless of profile weight.
func TestCarrierScoreNeverPositiveWithoutMetalExtraction(t *testing.T) {
	carrier := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("carrier")},
		UnitName:         "carrier",
		CanLoad:          true, // the zeroing key [08 R-AI-04 §2]
		CanAttack:        true, // would otherwise contribute +21 to the other-mix accumulator
		BuildCostMetal:   1000,
		BuildCostEnergy:  8000,
		// ExtractsMetal and MakesMetal both left at zero: "does not extract
		// metal" [08 R-AI-04 §2].
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{carrier.CanonicalKey: carrier}}
	strat := &Strategic{
		Catalog:       cat,
		Counts:        map[string]int32{carrier.CanonicalKey: 0},
		ClassVectors:  map[string]ClassVector{carrier.CanonicalKey: {}},
		SingleVectors: map[string]int8{carrier.CanonicalKey: 0},
	}
	strat.recomputeClassVectors()
	cv := strat.ClassVectors[carrier.CanonicalKey]
	if cv.C0 > 0 || cv.C1 > 0 || cv.C2 > 0 {
		t.Fatalf("carrier class vector = %+v, want every coefficient at or below zero [08 R-AI-04 §2]", cv)
	}

	// Every corner of the economy mix (metal-dominant, blended, other-
	// dominant) at maximum weight still scores at or below zero: nonpositive
	// coefficients dotted with a nonnegative mix can never turn positive.
	mixes := []ScoreInputs{
		{CurEnergy: 0, CapEnergy: 1000, NetEnergy: 0, ProdEnergy: 0, CurMetal: 0, CapMetal: 500, NetMetal: 0, ProdMetal: 0},         // starved: mix 100/100/0
		{CurEnergy: 800, CapEnergy: 1000, CurMetal: 400, CapMetal: 500, NetEnergy: 5, NetMetal: 5, ProdEnergy: 300, ProdMetal: 10},  // calm: mix 25/0/75
		{CurEnergy: 1000, CapEnergy: 1000, NetEnergy: 5, ProdEnergy: 300, CurMetal: 500, CapMetal: 500, NetMetal: 5, ProdMetal: 10}, // settled: mix 0/0/100
	}
	for _, in := range mixes {
		if s := ComputeScore(in, cv, 100); s > 0 {
			t.Fatalf("carrier score under mix %+v = %d, want at or below zero [08 R-AI-04 §2]", in, s)
		}
	}

	// The full selection pipeline: with no positive candidate, the reservoir
	// is never reached and the RNG stream is never drawn from — "skipped
	// without drawing" [08 R-AI-04 §2].
	rng.SeedGlobal(9, 0)
	sel := &testSelector{
		player:    1,
		profile:   &Profile{Weight: map[string]int32{carrier.CanonicalKey: 100}, Limit: map[string]int32{}},
		strategic: strat,
	}
	builder := testBuilder("armcom")
	econStarved := testEcon(1, 50, 1000, 25, 500, 0, 0, 0, 0) // clears the C5 gates; mix is otherwise irrelevant given the vector above
	before := rng.Global.Sim.Draws()
	if _, ok := SelectWithCandidates(sel, builder, econStarved, []string{carrier.CanonicalKey}); ok {
		t.Fatalf("carrier definition was selected despite a nonpositive score")
	}
	if after := rng.Global.Sim.Draws(); after-before != 0 {
		t.Fatalf("carrier-only selection drew %d RNG values, want zero [08 R-AI-04 §2]", after-before)
	}
}

// TestGates checks each C5 gate in isolation [PLAN 11 C5] [08].
func TestGates(t *testing.T) {
	builder := testBuilder("armcom")
	baseStrat := &Strategic{Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}, "corfav": {C0: 40, C1: 30, C2: 30}}}
	baseStrat.Catalog = &content.Catalog{Units: map[string]*content.UnitDef{
		content.CanonicalKey("armfav"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfav")}, UnitName: "armfav", Downloadable: true},
		content.CanonicalKey("corfav"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("corfav")}, UnitName: "corfav", Downloadable: false},
	}}
	baseProfile := &Profile{
		Weight: map[string]int32{"armfav": 100, "corfav": 100},
		Limit:  map[string]int32{},
	}
	sel := &testSelector{player: 1, profile: baseProfile, strategic: baseStrat}
	cands := []string{"armfav"}

	// Energy gate: curEnergy <50
	rng.SeedGlobal(1, 0)
	econLowEnergy := testEcon(1, 49, 1000, 100, 500, 0, 0, 0, 0)
	if _, ok := SelectWithCandidates(sel, builder, econLowEnergy, cands); ok {
		t.Fatalf("energy gate: currentEnergy 49 should reject")
	}
	// At threshold 50 should pass
	rng.SeedGlobal(1, 0)
	econAtEnergy := testEcon(1, 50, 1000, 100, 500, 0, 0, 0, 0)
	if _, ok := SelectWithCandidates(sel, builder, econAtEnergy, cands); !ok {
		t.Fatalf("energy gate: currentEnergy 50 should pass")
	}

	// Metal gate: <25
	rng.SeedGlobal(1, 0)
	econLowMetal := testEcon(1, 800, 1000, 24, 500, 0, 0, 0, 0)
	if _, ok := SelectWithCandidates(sel, builder, econLowMetal, cands); ok {
		t.Fatalf("metal gate: currentMetal 24 should reject")
	}
	rng.SeedGlobal(1, 0)
	econAtMetal := testEcon(1, 800, 1000, 25, 500, 0, 0, 0, 0)
	if _, ok := SelectWithCandidates(sel, builder, econAtMetal, cands); !ok {
		t.Fatalf("metal gate: currentMetal 25 should pass")
	}

	// Profile limit gate: count < limit or -1 [PLAN 11 C5]
	stratLimited := &Strategic{Counts: map[string]int32{"armfav": 2}, ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}}}
	profileLimit := &Profile{Weight: map[string]int32{"armfav": 100}, Limit: map[string]int32{"armfav": 2}}
	selLimited := &testSelector{player: 1, profile: profileLimit, strategic: stratLimited}
	econOK := testEcon(1, 800, 1000, 400, 500, 0, 0, 0, 0)
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(selLimited, builder, econOK, cands); ok {
		t.Fatalf("limit gate: count 2 limit 2 should reject (>=)")
	}
	stratLimited.Counts["armfav"] = 1
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(selLimited, builder, econOK, cands); !ok {
		t.Fatalf("limit gate: count 1 limit 2 should pass")
	}
	// Unlimited -1 always passes even at high count
	profileUnlimit := &Profile{Weight: map[string]int32{"armfav": 100}, Limit: map[string]int32{"armfav": -1}}
	selUnlimit := &testSelector{player: 1, profile: profileUnlimit, strategic: stratLimited}
	stratLimited.Counts["armfav"] = 99
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(selUnlimit, builder, econOK, cands); !ok {
		t.Fatalf("limit gate: -1 unlimited should pass regardless of count")
	}

	// The earlier builder-self rejection was disproved: reservoir admission is
	// unchanged when the selected definition is the builder's own definition.
	// The established consumer-side gate is the authored side comparison
	// [08 R-AI-01 §8].
	builderSelf := testBuilder("armfav")
	candsSelf := []string{"armfav", "corfav"}
	selSelf := &testSelector{player: 1, profile: baseProfile, strategic: baseStrat}
	rng.SeedGlobal(1, 0)
	got, ok := SelectWithCandidates(selSelf, builderSelf, econOK, candsSelf)
	if !ok {
		t.Fatalf("builder-own definition should remain reservoir-eligible")
	}
	if content.CanonicalKey(got.DefKey) == "" {
		t.Fatalf("builder-own selection returned an empty candidate")
	}

	// Mission mode 1 rejects a candidate carrying the authored downloadable
	// status bit (bit 5 in the compiled definition status word) [R-P0-05 §3].
	sel.missionMode = 1
	if _, ok := SelectWithCandidates(sel, builder, econOK, []string{"armfav"}); ok {
		t.Fatalf("mission mode 1 should reject downloadable candidate")
	}
	// The same mode admits a candidate without the status bit.
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(sel, builder, econOK, []string{"corfav"}); !ok {
		t.Fatalf("mission mode 1 should admit candidate without downloadable bit")
	}
	// Mode zero does not apply the definition-status gate.
	sel.missionMode = 0
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(sel, builder, econOK, []string{"armfav"}); !ok {
		t.Fatalf("mission mode 0 should admit downloadable candidate")
	}
}

func TestSelectionRequiresConcreteProfileAndVectors(t *testing.T) {
	builder := testBuilder("armcom")
	econ := testEcon(1, 800, 1000, 400, 500, 0, 0, 0, 0)
	strat := &Strategic{ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}}}
	sel := &testSelector{player: 1, strategic: strat, rng: rng.Global.Sim}
	if _, ok := SelectWithCandidates(sel, builder, econ, []string{"armfav"}); ok {
		t.Fatalf("selection must reject an absent loaded profile")
	}
	sel.profile = &Profile{Weight: map[string]int32{"armfav": 100}, Limit: map[string]int32{}}
	sel.strategic = &Strategic{}
	if _, ok := SelectWithCandidates(sel, builder, econ, []string{"armfav"}); ok {
		t.Fatalf("selection must reject uninitialized class vectors")
	}
}

func TestSelectPreservesAuthoredBuildMenuOrder(t *testing.T) {
	builder := testBuilder("armcom")
	sel := &testSelector{
		catalog: &content.Catalog{BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armcom"): {Buttons: []string{"corfav", "armfav"}},
		}},
	}
	got := buildOptionsForBuilder(sel, builder)
	want := []string{"corfav", "armfav"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("authored build-menu order got %v want %v", got, want)
	}
}

// TestFloat32Narrowing locks that energyRaw/metalRaw are evaluated in float32 with truncation, not float64 narrowing [PLAN 11 C6] [INVARIANTS I2].
// Mirrors economy admission float32 fixture.
func TestFloat32Narrowing(t *testing.T) {
	// Deterministic divergence: cap 1000 cur 480.00003 (bits 43f00001) yields float32 trunc 65 vs float64 trunc 64 [INVARIANTS I2] [PLAN 11 C6].
	// Found via brute that cap - cur in float32 rounds to 520.0 exactly while float64 gives 519.999969 => scaled 65 vs 64.999 -> trunc 65 vs 64.
	curF32 := math.Float32frombits(0x43f00001) // 480.0000305175781
	capF32 := float32(1000)
	in := ScoreInputs{CurEnergy: curF32, CapEnergy: capF32, NetEnergy: 5, ProdEnergy: 300, CurMetal: 500, CapMetal: 500, NetMetal: 5, ProdMetal: 10}
	f32val := EnergyRaw(in)
	// float64 reference promoted from same float32 values but computed in float64
	diffF64 := float64(capF32) - float64(curF32)
	scaledF64 := diffF64 * 0.125
	if scaledF64 < 0 {
		scaledF64 = 0
	}
	f64val := int32(scaledF64)
	// With net>=1 prod>=200 no extra bumps, raw is just trunc
	if f32val == f64val {
		t.Fatalf("expected float32 vs float64 divergence but got same %d for cur %v cap %v diffF32 %v diffF64 %v", f32val, curF32, capF32, capF32-curF32, diffF64)
	}
	if f32val != 65 {
		t.Fatalf("float32 energyRaw expected 65 for divergence case, got %d (float64 would be %d)", f32val, f64val)
	}
	if f64val != 64 {
		t.Fatalf("float64 reference expected 64, got %d", f64val)
	}
	t.Logf("float32 narrowing divergence confirmed: cap %v cur bits %08x f32 %d f64 %d", capF32, math.Float32bits(curF32), f32val, f64val)
	// Additional check: ensure metalRaw also float32
	in2 := ScoreInputs{CurEnergy: 1000, CapEnergy: 1000, NetEnergy: 2, ProdEnergy: 300, CurMetal: 0, CapMetal: 500, NetMetal: 2, ProdMetal: 10}
	if got := MetalRaw(in2); got != int32(125) {
		t.Fatalf("metalRaw 500*0.25=125 got %d", got)
	}
	// Verify mixed: energyRaw/metalRaw use float32 narrow at trunc
	inStarved := ScoreInputs{CurEnergy: 0, CapEnergy: 1000, NetEnergy: 0, ProdEnergy: 0, CurMetal: 0, CapMetal: 500, NetMetal: 0, ProdMetal: 0}
	if got := EnergyRaw(inStarved); got != 245 {
		t.Fatalf("starved energyRaw via float32 should be 245 got %d", got)
	}
}

// TestSelectDeterminism ensures two identical runs produce identical build orders [PLAN 11 Exit].
// RS-02: single global stream [08][I4].
func TestSelectDeterminism(t *testing.T) {
	seed := uint32(42)
	selA := &testSelector{
		player:  1,
		profile: &Profile{Weight: map[string]int32{"armfav": 100, "corfav": 50}, Limit: map[string]int32{}},
		strategic: &Strategic{
			Counts:       map[string]int32{},
			ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}, "corfav": {C0: 40, C1: 30, C2: 30}},
		},
	}
	selB := &testSelector{
		player:  1,
		profile: &Profile{Weight: map[string]int32{"armfav": 100, "corfav": 50}, Limit: map[string]int32{}},
		strategic: &Strategic{
			Counts:       map[string]int32{},
			ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}, "corfav": {C0: 40, C1: 30, C2: 30}},
		},
	}
	builder := testBuilder("armcom")
	econ := testEcon(1, 50, 1000, 25, 500, 0, 0, 0, 0)
	cands := []string{"armfav", "corfav"}
	rng.SeedGlobal(seed, 0)
	a, okA := SelectWithCandidates(selA, builder, econ, cands)
	rng.SeedGlobal(seed, 0)
	b, okB := SelectWithCandidates(selB, builder, econ, cands)
	if okA != okB || a.DefKey != b.DefKey || a.Score != b.Score {
		t.Fatalf("determinism failed: %v/%v vs %v/%v", a, okA, b, okB)
	}
}
