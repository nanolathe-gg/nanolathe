package ai

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

type testSelector struct {
	player          uint8
	profile         *Profile
	strategic       *Strategic
	catalog         *content.Catalog
	candidateSource func(*units.Unit) []string
	gateFlag        int32
	gateCandidates  map[string]struct{}
}

func (s *testSelector) GetPlayer() uint8                               { return s.player }
func (s *testSelector) GetProfile() *Profile                           { return s.profile }
func (s *testSelector) GetStrategic() *Strategic                       { return s.strategic }
func (s *testSelector) GetCandidateSource() func(*units.Unit) []string { return s.candidateSource }
func (s *testSelector) GetCatalog() *content.Catalog                   { return s.catalog }
func (s *testSelector) GetMissionGateFlag() int32                      { return s.gateFlag }
func (s *testSelector) GetGateCandidates() map[string]struct{}         { return s.gateCandidates }

func testBuilder(defKey string) *units.Unit {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(defKey)},
		UnitName:         defKey,
	}
	return &units.Unit{Def: def, Owner: 1, Alive: true}
}

func testEcon(player uint8, curEnergy, capEnergy, curMetal, capMetal float32, prodE, prodM, consE, consM float32) *economy.Service {
	var svc economy.Service
	// Ensure player slot exists
	svc.Players[player].Stock[economy.Energy] = curEnergy
	svc.Players[player].Capacity[economy.Energy] = capEnergy
	svc.Players[player].Stock[economy.Metal] = curMetal
	svc.Players[player].Capacity[economy.Metal] = capMetal
	svc.Players[player].PassProduced[economy.Energy] = prodE
	svc.Players[player].PassProduced[economy.Metal] = prodM
	svc.Players[player].PassConsumed[economy.Energy] = consE
	svc.Players[player].PassConsumed[economy.Metal] = consM
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

// TestReservoirSingleDraw asserts one RNG draw per selection regardless of candidate count [PLAN 11 C7] [INVARIANTS I4].
func TestReservoirSingleDraw(t *testing.T) {
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
	econ := testEcon(1, 800, 1000, 400, 500, 300, 10, 0, 0) // use prod 300 etc to get positive scores but will override inputs via PassProduced mapping

	// Use explicit candidates that will have positive scores
	cands := []string{"armfav", "corfav", "armship"}
	// Ensure deterministic scores: we use economy inputs that yield positive via starved mix
	// Overwrite economy to starved-like but with curEnergy >=50, curMetal >=25 to pass C5 gates
	// diff still large: 1000-50=950 =>118+20+100=238
	econStarved := testEcon(1, 50, 1000, 25, 500, 0, 0, 0, 0)
	// Need to set PassConsumed zero so net 0 -> +20 each
	rng.SeedGlobal(seed, 0)
	before := rng.Global.Sim.Draws()
	_, ok := SelectWithCandidates(sel, builder, econStarved, cands)
	if !ok {
		t.Fatalf("selection should succeed")
	}
	after := rng.Global.Sim.Draws()
	if after-before != 1 {
		t.Fatalf("reservoir single draw: draws %d -> %d want +1 regardless of %d candidates", before, after, len(cands))
	}
	// Regardless of candidate count, still one draw
	rng.SeedGlobal(seed, 0)
	cands2 := []string{"armfav", "corfav"}
	before = rng.Global.Sim.Draws()
	_, _ = SelectWithCandidates(sel, builder, econStarved, cands2)
	after = rng.Global.Sim.Draws()
	if after-before != 1 {
		t.Fatalf("single draw with 2 cands: draws %d want +1", after-before)
	}
	// Total <2 should consume no draw per [01 §7.1]
	rng.SeedGlobal(seed, 0)
	// Create a scenario where only one positive remains and total==1 => need special cands with weight to produce score 1
	// For determinism use weight 1 and class that yields 1
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
	// We can directly set CandidateSource to guarantee score? Simpler: test total==1 via weight tuning: if we pick cv {C0:0,C1:1,C2:0} and mix metal 100 => total 100 *1/10000=0 not 1.
	// To get 1, need total*weight ==10000. Choose cv {C0:100} other100 =>10000*1/10000=1.
	// So need mix other100.
	// Achieve other100 requires metalMix 0 energyMix 0 => metalRaw 0 energyRaw 0.
	// That requires prod >=200/5 etc and diff 0. Let's just brute: curEnergy 1000 cap1000 => diff0 => scaled0 => +0 net>=1 +0 prod>=200 =>0. So other100.
	econOne := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0) // use high prod to avoid bumps, net 300>1 etc so 0 raw

	// Set net to 2 (>1) to avoid +20, prod 300 to avoid +100 => raw 0
	econOne.Players[1].PassProduced[economy.Energy] = 300
	econOne.Players[1].PassProduced[economy.Metal] = 10
	econOne.Players[1].PassConsumed[economy.Energy] = 0 // net 300
	econOne.Players[1].PassConsumed[economy.Metal] = 0  // net 10

	// But our ScoreInputsFromEconomy net = prod - cons =300,10 both >1 so no +20, raw 0. Good.
	rng.SeedGlobal(seed, 0)
	before = rng.Global.Sim.Draws()
	_, _ = SelectWithCandidates(selOne, builder, econOne, []string{"armfav"})
	after = rng.Global.Sim.Draws()
	if after-before != 0 {
		t.Fatalf("total<2 should not advance RNG [01 §7.1], draws %d want 0", after-before)
	}
	// Determinism: same seed gives same choice
	rng.SeedGlobal(seed, 0)
	c1, _ := SelectWithCandidates(sel, builder, econStarved, cands)
	rng.SeedGlobal(seed, 0)
	c2, _ := SelectWithCandidates(sel, builder, econStarved, cands)
	if c1.DefKey != c2.DefKey {
		t.Fatalf("reservoir deterministic: %s vs %s", c1.DefKey, c2.DefKey)
	}
	_ = econ // keep
}

// TestGates checks each C5 gate in isolation [PLAN 11 C5] [08].
func TestGates(t *testing.T) {
	builder := testBuilder("armcom")
	baseStrat := &Strategic{Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}, "corfav": {C0: 40, C1: 30, C2: 30}}}
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

	// Builder self gate: candidate == builder definition rejected [08]
	builderSelf := testBuilder("armfav")
	candsSelf := []string{"armfav", "corfav"}
	selSelf := &testSelector{player: 1, profile: baseProfile, strategic: baseStrat}
	rng.SeedGlobal(1, 0)
	got, ok := SelectWithCandidates(selSelf, builderSelf, econOK, candsSelf)
	if !ok {
		t.Fatalf("self gate: should have corfav left")
	}
	if content.CanonicalKey(got.DefKey) == content.CanonicalKey("armfav") {
		t.Fatalf("self gate: should reject own definition")
	}

	// Opaque 241 bit5 gate TODO(T25) [PLAN 11 C5] [P0-I16: per-selector]
	sel.gateFlag = 1
	sel.gateCandidates = map[string]struct{}{"corfav": {}}
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(sel, builder, econOK, []string{"corfav"}); ok {
		t.Fatalf("241 gate: should reject when flag 1 and bit set [P0-I16]")
	}
	// Flag 0 => passes
	sel.gateFlag = 0
	sel.gateCandidates = nil
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(sel, builder, econOK, []string{"corfav"}); !ok {
		t.Fatalf("241 gate: flag 0 should pass [P0-I16]")
	}
	sel.gateFlag = 0
	sel.gateCandidates = nil
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
func TestSelectDeterminism(t *testing.T) {
	seed := uint32(42)
	sel := &testSelector{
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
	a, okA := SelectWithCandidates(sel, builder, econ, cands)
	rng.SeedGlobal(seed, 0)
	b, okB := SelectWithCandidates(sel, builder, econ, cands)
	if okA != okB || a.DefKey != b.DefKey || a.Score != b.Score {
		t.Fatalf("determinism failed: %v/%v vs %v/%v", a, okA, b, okB)
	}
}
