package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestInitializeBattleAIPrecedesUnitDraws(t *testing.T) {
	terrain := &world.Terrain{CellW: 4, CellH: 2, Plot: make([]world.PlotCell, 8)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i][7] = 44
	}
	terrain.Tidal = numeric.Fixed(16384) // 0.25 map tidal strength.
	ota, err := formats.LoadOTA([]byte("[GlobalHeader]{SurfaceMetal=300;}"))
	if err != nil {
		t.Fatal(err)
	}
	wind := world.NewWind(100, 200)
	econ := &economy.Service{Wind: wind, Terrain: terrain}
	windDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "windgen"},
		UnitName:         "windgen",
		WindGenerator:    8,
	}
	tidalDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tidalgen"},
		UnitName:         "tidalgen",
		TidalGenerator:   8,
	}
	s := &Session{
		World:   terrain,
		Catalog: &content.Catalog{Units: map[string]*content.UnitDef{"windgen": windDef, "tidalgen": tidalDef}},
		Econ:    econ,
		Wind:    wind,
		Mission: &mission.Mission{OTA: ota},
	}
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.Players[1].Allies[1] = true
	s.SeedSessionRNG(0x1234, 0x5678)

	if err := initializeBattleAI(s, 1, &ai.Profile{}); err != nil {
		t.Fatalf("initializeBattleAI: %v", err)
	}
	mgr := s.AI[1]
	if mgr == nil {
		t.Fatal("manager not installed")
	}
	if got := s.SimRNG().Draws(); got != 8 {
		t.Fatalf("manager constructor draws = %d, want exact pre-unit count 8 [08 R-ENTRY-01 §3 step 24]", got)
	}
	if wind.Scalar != 0 || wind.Strength != 0 || wind.LastChange != 0 {
		t.Fatalf("tick-zero wind state changed during AI binding: %+v", wind)
	}
	if got := mgr.Strategic.ClassVectors["windgen"].C2; got != 0 {
		t.Fatalf("tick-zero wind-generator coefficient = %d, want zero before first wind chain", got)
	}
	if got := mgr.Strategic.ClassVectors["tidalgen"].C2; got != 10 {
		t.Fatalf("tick-zero tidal-generator coefficient = %d, want 10 from bound map strength", got)
	}
	if mgr.SurfaceMetal != 300 || terrain.Plot[0].Metal() != 44 {
		t.Fatalf("SurfaceMetal = %d and plot byte = %d, want distinct raw word 300 and narrowed seed 44", mgr.SurfaceMetal, terrain.Plot[0].Metal())
	}
	if mgr.Strategic.CenterX != 0 || mgr.Strategic.CenterZ != 0 || mgr.OriginX != 0 || mgr.OriginZ != 0 {
		t.Fatalf("strategic/origin state was approximated after construction: center=(%d,%d) origin=(%d,%d)", mgr.Strategic.CenterX, mgr.Strategic.CenterZ, mgr.OriginX, mgr.OriginZ)
	}
	if mgr.RallyVisible == nil {
		t.Fatal("ordinary rally visibility binding is nil")
	}
	if mgr.RallyProbeKnown != nil {
		t.Fatal("the unknown rally explored/current option identity must fail closed")
	}
	// The rally member gate is bound now that [08 R-AI-01 §19] names it: the
	// slot-1 shot-time physical gate of [06 §3.3], taken from the combat
	// service rather than re-implemented in the planner.
	if mgr.RallyShotTimeAdmits == nil {
		t.Fatal("the rally slot-1 shot-time gate binding is nil")
	}
	if mgr.InitializeBattleState(terrain, ai.RallyBattleBindings{}) {
		t.Fatal("battle state initialized more than once")
	}
	if !mgr.IsAlliance(1, 1) || mgr.IsAlliance(1, 0) || mgr.IsAlliance(10, 1) {
		t.Fatal("session-owned alliance binding did not validate active player rows")
	}
}

// TestCampaignSecondGrantWritesStocksAndStorageBonus locks the surviving grant
// at the campaign battle-entry boundary [08 R-ENTRY-01 §8 step 5]: on the
// mission kind it writes "stocks and bonus from the mission's authored words",
// and nothing else — ledgers, capacity and history fields stay as the tick-zero
// settlement left them.
//
// It previously asserted "stocks only". That read [08 R-ENTRY-01 §8 step 5]'s
// "overwrites whatever the tick-0 settlement produced" as covering the stocks
// alone and missed the same sentence's "and bonus", which [05 R-ECO-01 §4]
// states outright: the battle-initialisation starting-resource writer walks the
// ten slots and, on the mission kind, calls the bonus setter before writing the
// stocks. Without the bonus the opening stock has no capacity to sit in and the
// post-settlement clamp takes it straight back — Arm mission 2's empty
// treasury.
func TestCampaignSecondGrantWritesStocksAndStorageBonus(t *testing.T) {
	ota, err := formats.LoadOTA([]byte(`
[GlobalHeader]
{
    HumanMetal=100;
    HumanEnergy=200;
    ComputerMetal=300;
    ComputerEnergy=400;
}
`))
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Econ: &economy.Service{}}
	human := &s.Econ.Players[0]
	human.Exists = true
	human.ControllerState = 1
	human.Stock = [2]float32{17, 19}
	human.Capacity = [2]float32{501, 502}
	human.TotalProduced = [2]float64{31, 32}
	human.PassConsumed = [2]float32{41, 42}
	computer := &s.Econ.Players[1]
	computer.Exists = true
	computer.ControllerState = 2
	computer.Stock = [2]float32{23, 29}
	computer.Capacity = [2]float32{601, 602}
	computer.TotalProduced = [2]float64{51, 52}
	computer.PassConsumed = [2]float32{61, 62}

	// The bonus setter floors each operand at 200 [05 R-ECO-01 §4], so the
	// human's 100/200 both store 200 and the computer's 300/400 store as
	// authored.
	wantHuman := *human
	wantHuman.Stock = [2]float32{100, 200}
	wantHuman.StorageBonusEnabled = true
	wantHuman.StorageBonus = [2]float32{200, 200}
	wantComputer := *computer
	wantComputer.Stock = [2]float32{300, 400}
	wantComputer.StorageBonusEnabled = true
	wantComputer.StorageBonus = [2]float32{300, 400}
	if err := overwriteCampaignResources(s, &mission.Mission{OTA: ota}); err != nil {
		t.Fatalf("overwriteCampaignResources: %v", err)
	}
	if !reflect.DeepEqual(*human, wantHuman) {
		t.Fatalf("human second grant added to tick-zero stock or changed history:\n got %#v\nwant %#v", *human, wantHuman)
	}
	if !reflect.DeepEqual(*computer, wantComputer) {
		t.Fatalf("computer second grant added to tick-zero stock or changed history:\n got %#v\nwant %#v", *computer, wantComputer)
	}
}

// TestUnitLossThrottleIsControllerTwoOnly locks the construction throttle of
// [08 R-AI-01 §11] at the boundary that owns it. Relocated by WU-19-26: the
// throttle is armed from DAMAGE to a `cancapture` unit whose owning player's
// control byte is 2 — one of the four parts of the damage-intake reaction
// routine of [06 §9.1] step 4 — and NOT from death finalization, where this
// build used to arm it. Death is a different event with a different cadence.
func TestUnitLossThrottleIsControllerTwoOnly(t *testing.T) {
	cat := minimalCatalogForStrict()
	// `cancapture` is the throttle's definition gate [08 R-AI-01 §11].
	cat.Units["armcom"].CanCapture = true
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		Catalog: cat,
		World:   minimalTerrain(),
		Units:   w,
		Econ:    &economy.Service{},
		Clock:   &clock.State{},
		Combat:  &combat.Service{},
	}
	for owner, controller := range []uint8{1, 2} {
		p := &s.Econ.Players[owner]
		p.Exists = true
		p.ControllerState = controller
	}
	s.Combat.ControlByte = func(owner uint8) uint8 {
		if int(owner) >= len(s.Econ.Players) || !s.Econ.Players[owner].Exists {
			return combat.ControlByteAbsent
		}
		return s.Econ.Players[owner].ControllerState
	}
	s.bindDamageReaction()
	s.SeedSessionRNG(123, 456)
	s.Clock.GlobalTick = 17
	humanManager := &ai.Manager{Player: 0, RNG: s.SimRNG()}
	computerManager := &ai.Manager{Player: 1, RNG: s.SimRNG()}
	s.AI[0], s.AI[1] = humanManager, computerManager
	s.RegisterAll()

	def := cat.Units["armcom"]
	humanHandle, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	human := w.Unit(humanHandle)
	computerHandle, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	computer := w.Unit(computerHandle)

	// Damage to a human-owned `cancapture` unit arms nothing: the throttle is
	// the computer player's alone [08 R-AI-01 §11].
	before := s.SimRNG().Draws()
	s.Combat.ReactToDamage(w, human, computer, s.Clock.GlobalTick)
	if got := s.SimRNG().Draws(); got != before {
		t.Fatalf("human reaction consumed %d draws, want zero [08 R-AI-01 §11]", got-before)
	}
	if got := humanManager.UnitLossDeadline(); got != 0 {
		t.Fatalf("human manager armed unit-loss deadline %d, want zero", got)
	}

	// Damage to the computer's own `cancapture` unit draws once, bound 300, and
	// writes tick + 30 + draw [08 R-AI-01 §11].
	probe := rng.NewSimulation(123)
	wantDeadline := uint32(17 + 30 + probe.Uint32n(300))
	s.Combat.ReactToDamage(w, computer, human, s.Clock.GlobalTick)
	if got := s.SimRNG().Draws(); got != before+1 {
		t.Fatalf("computer reaction consumed %d draws, want one", got-before)
	}
	if got := computerManager.UnitLossDeadline(); got != wantDeadline {
		t.Fatalf("computer unit-loss deadline = %d, want %d", got, wantDeadline)
	}

	// Death finalization no longer arms it, and draws nothing.
	before = s.SimRNG().Draws()
	computer.Dying = true
	computer.DeathCause = 1
	s.phaseUnits(2)
	if got := s.SimRNG().Draws(); got != before {
		t.Fatalf("death finalization consumed %d draws, want zero: the throttle moved to the damage boundary [08 R-AI-01 §11]", got-before)
	}
}

func TestFinishBattleEntryPrimeOverwriteThenMetalVectorOnce(t *testing.T) {
	terrain := &world.Terrain{
		CellW: 3,
		CellH: 2,
		Plot:  make([]world.PlotCell, 6),
		FeatureDefs: []*content.FeatureDef{
			{Metal: 11, Indestructible: true},
			{Metal: 22, Indestructible: true},
			{Metal: 33, Indestructible: false},
		},
	}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.PlotAt(2, 0).SetFeature(1)
	terrain.PlotAt(0, 1).SetFeature(0)
	terrain.PlotAt(1, 1).SetFeature(world.PlotFeatureFringe)
	terrain.PlotAt(2, 1).SetFeature(2)

	s := &Session{World: terrain, Econ: &economy.Service{}}
	p := &s.Econ.Players[0]
	p.Exists = true
	p.ControllerState = 1
	p.EndGameCountdown = -1
	p.UpdateTime = 0
	p.Stock[economy.Metal] = 91
	p.Stock[economy.Energy] = 92
	mgr := &ai.Manager{Player: 1}
	grantCalls := 0
	if err := finishBattleEntry(s, func() error {
		grantCalls++
		if p.UpdateTime != 30 {
			t.Fatalf("second grant ran before tick-0 player prime: UpdateTime=%d", p.UpdateTime)
		}
		if len(mgr.Strategic.MetalSpots) != 0 {
			t.Fatal("metal vector built before second resource grant")
		}
		clearLiveResourceStocks(s)
		p.Stock[economy.Metal] = 1000
		p.Stock[economy.Energy] = 2000
		s.AI[1] = mgr
		return nil
	}); err != nil {
		t.Fatalf("finishBattleEntry: %v", err)
	}
	if grantCalls != 1 || p.Stock[economy.Metal] != 1000 || p.Stock[economy.Energy] != 2000 {
		t.Fatalf("second grant did not overwrite stocks once: calls=%d stocks=%v", grantCalls, p.Stock)
	}
	want := []ai.MetalSpot{{CellX: 2, CellZ: 0, Metal: 22}, {CellX: 0, CellZ: 1, Metal: 11}}
	if got := mgr.Strategic.MetalSpots; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("row-major battle metal vector = %#v, want %#v", got, want)
	}

	// The direct/fixture seam can safely encounter the common tail without
	// double-priming, re-granting, or rebuilding its retained vector.
	mgr.Strategic.MetalSpots[0].Metal = 99
	if err := finishBattleEntry(s, func() error { grantCalls++; return nil }); err != nil {
		t.Fatalf("second finishBattleEntry: %v", err)
	}
	if grantCalls != 1 || p.UpdateTime != 30 || mgr.Strategic.MetalSpots[0].Metal != 99 {
		t.Fatalf("tail repeated: calls=%d update=%d spots=%#v", grantCalls, p.UpdateTime, mgr.Strategic.MetalSpots)
	}
}

func TestParseCampaignMissionSelectorRejectsMalformedExplicitIdentity(t *testing.T) {
	path, idx, err := parseCampaignMissionSelector("camps/arm campaign.tdf:MISSION12")
	if err != nil || path != "camps/arm campaign.tdf" || idx != 12 {
		t.Fatalf("valid selector = (%q,%d,%v)", path, idx, err)
	}
	for _, selector := range []string{
		"camps/arm campaign.tdf:",
		"camps/arm campaign.tdf:mission",
		"camps/arm campaign.tdf:0",
		"camps/arm campaign.tdf:mission-1",
		"camps/arm campaign.tdf:mission0junk",
		":mission0",
		"camps/a:mission0:extra",
	} {
		_, _, err := parseCampaignMissionSelector(selector)
		if err == nil || !strings.Contains(err.Error(), selector) {
			t.Fatalf("selector %q error = %v, want useful rejection", selector, err)
		}
	}
}

func TestEqualBattleEntryProducesEqualAIStateRNGHashAndAllocationOrder(t *testing.T) {
	makeSession := func() *Session {
		t.Helper()
		cfg := DirectSkirmishConfig("test")
		cat := minimalCatalogForStrict()
		cat.Units["armcom"].Commander = true
		cat.Units["corcom"].Commander = true
		fs := fsFromMapSkirmish(t, map[string]string{
			"maps/test.ota":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n",
			"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
		})
		s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
		if err != nil {
			t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
		}
		return s
	}
	a, b := makeSession(), makeSession()
	if a.SimRNG().State != b.SimRNG().State || a.SimRNG().Draws() != b.SimRNG().Draws() ||
		a.CrtRNG().State != b.CrtRNG().State || a.CrtRNG().Draws() != b.CrtRNG().Draws() {
		t.Fatalf("equal entry RNG differs: sim=(%d/%d,%d/%d) crt=(%d/%d,%d/%d)",
			a.SimRNG().State, a.SimRNG().Draws(), b.SimRNG().State, b.SimRNG().Draws(),
			a.CrtRNG().State, a.CrtRNG().Draws(), b.CrtRNG().State, b.CrtRNG().Draws())
	}
	for i := range a.AI {
		am, bm := a.AI[i], b.AI[i]
		if (am == nil) != (bm == nil) {
			t.Fatalf("manager ownership differs at slot %d", i)
		}
		if am != nil && (!reflect.DeepEqual(am.Strategic.MetalSpots, bm.Strategic.MetalSpots) || am.Strategic.CenterX != bm.Strategic.CenterX || am.Strategic.CenterZ != bm.Strategic.CenterZ || am.OriginX != bm.OriginX || am.OriginZ != bm.OriginZ) {
			t.Fatalf("manager strategic state differs at slot %d", i)
		}
	}
	type allocated struct {
		handle int
		owner  uint8
		name   string
	}
	allocationOrder := func(s *Session) []allocated {
		out := make([]allocated, 0, s.Units.Used())
		for _, u := range s.Units.IterSliced() {
			if u != nil {
				name := ""
				if u.Def != nil {
					name = u.Def.UnitName
				}
				out = append(out, allocated{handle: int(u.Handle), owner: u.Owner, name: name})
			}
		}
		return out
	}
	if aa, ba := allocationOrder(a), allocationOrder(b); !reflect.DeepEqual(aa, ba) {
		t.Fatalf("allocation order differs: %#v / %#v", aa, ba)
	}
	ha, err := a.ParityAuthoritativeHash()
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	hb, err := b.ParityAuthoritativeHash()
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if ha != hb {
		t.Fatalf("equal initial authoritative hashes differ: %s / %s", ha, hb)
	}
}
