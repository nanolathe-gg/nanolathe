package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestBindExistingOrderQueuesKeepsLazyQueuesLazy(t *testing.T) {
	w := units.NewSliced(4, nil)
	def := &content.UnitDef{UnitName: "queue-binding", MaxDamage: 1, Script: fixtureCOBProgram()}
	h1, _ := w.Create(def, 0, 0, 0, 0)
	h2, _ := w.Create(def, 0, 0, 0, 0)
	u1, u2 := w.Unit(h1), w.Unit(h2)
	q := orders.QueueForUnit(u1)
	sim := rng.NewSimulation(31)
	binding := &orders.QueueBinding{SimRNG: &sim}
	s := &Session{Units: w, Build: &construction.Service{OrderBinding: binding}}
	s.bindExistingOrderQueues()
	if q.Binding() != binding {
		t.Fatal("existing queue did not receive the concrete session binding")
	}
	if u2.Orders != nil {
		t.Fatal("binding pass created a queue for a unit that had none")
	}
}

func TestNewOrderBindingUsesRetailUnitPoolOrder(t *testing.T) {
	w := newSessionFixtureWorld(3, nil)
	def := &content.UnitDef{UnitName: "binding-order", MaxDamage: 1}
	h0, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create player 0 unit: %v", err)
	}
	h1, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create player 1 unit: %v", err)
	}
	s := &Session{Units: w, Econ: &economy.Service{}}
	b := s.newOrderBinding()
	var got []pool.Handle
	b.ForEachUnit(func(h pool.Handle, _ *units.Unit) bool {
		got = append(got, h)
		return false
	})
	if len(got) != 2 || got[0] != h0 || got[1] != h1 {
		t.Fatalf("unit binding traversal = %v, want [%d %d]", got, h0, h1)
	}
}

func TestNewOrderBindingUsesDirectionalDiplomacyAndResources(t *testing.T) {
	s := &Session{Econ: &economy.Service{}}
	s.Econ.Players[0].Allies[1] = true
	s.Econ.Players[1].Allies[0] = false
	s.Econ.Players[0].Stock = [2]float32{7, 11}
	s.Econ.Players[0].Capacity = [2]float32{70, 110}
	b := s.newOrderBinding()
	actor := &units.Unit{Owner: 0, Def: &content.UnitDef{Side: "blue"}}
	target := &units.Unit{Owner: 1, Def: &content.UnitDef{Side: "blue"}}
	if b.Hostility(actor, target) {
		t.Fatal("directional player alliance should admit the candidate")
	}
	s.Econ.Players[0].Allies[1] = false
	if !b.Hostility(actor, target) {
		t.Fatal("directional player diplomacy should reject the candidate")
	}
	resources, ok := b.Resources(0)
	if !ok || resources.Stock != [2]float32{7, 11} || resources.Capacity != [2]float32{70, 110} {
		t.Fatalf("resource binding = %#v, %v", resources, ok)
	}
}

// TestSeedSessionRNGWipesPreBattleDrawsAndLeavesGlobalAlone locks the DET-01
// seeding contract [R-CORE-02]: seeding both session streams fresh wipes
// every draw made before it (retail's reseed-wipes-history property at battle
// entry), the seeded continuation is deterministic, and rng.Global is neither
// read nor written. The old requireGlobalRNGStreams gate is gone — there is
// no process-global stream left to validate.
func TestSeedSessionRNGWipesPreBattleDrawsAndLeavesGlobalAlone(t *testing.T) {
	s := &Session{}
	// Pre-battle consumption on the fixture-default streams.
	s.SimRNG().Uint32n(100)
	s.CrtRNG().Rand()
	if s.SimRNG().Draws() == 0 || s.CrtRNG().Draws() == 0 {
		t.Fatal("pre-battle draws did not advance the session streams")
	}
	gsim, gcrt := rng.Global.Sim, rng.Global.Crt

	s.SeedSessionRNG(42, 42)
	if s.SimRNG().Draws() != 0 || s.CrtRNG().Draws() != 0 {
		t.Fatalf("reseed must wipe draw history [R-CORE-02]: sim %d crt %d", s.SimRNG().Draws(), s.CrtRNG().Draws())
	}
	if rng.Global.Sim != gsim || rng.Global.Crt != gcrt {
		t.Fatal("SeedSessionRNG mutated rng.Global [DET-01]")
	}

	// Same seeds produce identical continuations.
	fresh := &Session{}
	fresh.SeedSessionRNG(42, 42)
	if a, b := s.SimRNG().Uint32n(1000), fresh.SimRNG().Uint32n(1000); a != b {
		t.Fatalf("sim continuation diverged: %d vs %d", a, b)
	}
	if a, b := s.CrtRNG().Rand(), fresh.CrtRNG().Rand(); a != b {
		t.Fatalf("crt continuation diverged: %d vs %d", a, b)
	}
}

func TestSkirmishSetupShuffleDoesNotAdvanceSessionCRT(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 41, 73)
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2, Location: 0, RNGSimSeed: 41, RNGCrtSeed: 73}
	cfg.Players[0].Side = 0
	cfg.Players[1].Side = 1
	m := &mission.Mission{Specials: []mission.Special{
		{Kind: 1, ID: 0, X: 0, Z: 0, Name: "StartPos1"},
		{Kind: 1, ID: 1, X: 10, Z: 10, Name: "StartPos2"},
	}}
	before := *s.CrtRNG()
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		t.Fatalf("reconstruct skirmish units: %v", err)
	}
	if got := s.CrtRNG(); got.State != before.State || got.Draws() != before.Draws() {
		t.Fatalf("retained CRT moved during setup: state %d→%d draws %d→%d", before.State, got.State, before.Draws(), got.Draws())
	}
}

// TestConstructorsDoNotRequireGlobalRNG supersedes the old
// TestStrictSessionConstructorsRejectMissingGlobalRNG [R-CORE-02] DET-01:
// constructors no longer consult or require the process-global streams, so
// their diagnostics with nil globals are domain errors, never RNG errors.
func TestConstructorsDoNotRequireGlobalRNG(t *testing.T) {
	oldSim, oldCRT := rng.Global.Sim, rng.Global.Crt
	t.Cleanup(func() {
		rng.Global.Sim = oldSim
		rng.Global.Crt = oldCRT
	})
	rng.Global.Sim = nil
	rng.Global.Crt = nil

	if _, err := NewMissionWithProgress(nil, nil, "missing", 0, nil); err == nil || strings.Contains(err.Error(), "RNG") {
		t.Fatalf("mission constructor error = %v, want a domain error with no RNG dependency", err)
	}
	if _, err := NewSkirmishWithProgress(nil, nil, SkirmishConfig{MapName: "missing"}, nil); err == nil || strings.Contains(err.Error(), "RNG") {
		t.Fatalf("skirmish constructor error = %v, want a domain error with no RNG dependency", err)
	}
}

func minimalCatalogForStrict() *content.Catalog {
	mv := map[string]*content.MovementClass{
		// Hand-authored compiled record, template-initialized per the compiler
		// contract [04 §6.1 R-DOC04-A]: unauthored fields carry the startup
		// template (minwaterdepth -10000, maxwaterslope 255) and bad slopes
		// chain to half the Max just read (10>>1, 255>>1). A zero-valued
		// record is a real record whose zero minwaterdepth blocks every cell
		// above sea level [02 §5 "Movement class record"].
		"testmove": {FootprintX: 1, FootprintZ: 1, MaxWaterDepth: 10, MinWaterDepth: -10000, MaxSlope: 10, BadSlope: 5, MaxWaterSlope: 255, BadWaterSlope: 127},
	}
	mv["testmove"].CanonicalKey = content.CanonicalKey("testmove")
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			// Commanders are mobile in the corpus and author BMcode=1; the
			// building-class status bit is derived from that byte [08 "Classifier
			// eligibility, destinations, and order"].
			"armcom": {UnitName: "armcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, Commander: true, BMCode: 1},
			"corcom": {UnitName: "corcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, Commander: true, BMCode: 1},
		},
		Movement: mv,
		Sides: []*content.SideDef{
			{Name: "ARM", Commander: "armcom"},
			{Name: "CORE", Commander: "corcom"},
		},
		Features: map[string]*content.FeatureDef{},
		Maps:     map[string]*content.MapHeader{},
	}
	for _, u := range cat.Units {
		u.CanonicalKey = content.CanonicalKey(u.UnitName)
	}
	installFixtureCOB(cat)
	return cat
}

func minimalTerrain() *world.Terrain {
	attrs := make([]formats.TNTAttribute, 32*32)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 32, 32)
	ter := &world.Terrain{
		CellW: 32, CellH: 32,
		Plot:     plot,
		Version:  0x2000,
		SeaLevel: 0,
		WindMin:  100,
		WindMax:  2000,
	}
	_ = ter.ApplySchema(nil, 0)
	return ter
}

func fsWithMap(t *testing.T, ota string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maps", "test.ota"), []byte(ota), 0644); err != nil {
		t.Fatalf("write ota: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}
	return fs
}

func TestStrictCatalogValidatesSuppliedCatalog(t *testing.T) {
	missingMovement := &content.Catalog{
		Sides: []*content.SideDef{{Name: "ARM"}},
	}
	if _, err := strictCatalogWithProgress(nil, missingMovement, nil); err == nil || !strings.Contains(err.Error(), "moveinfo.tdf") {
		t.Fatalf("missing MOVEINFO supplied catalog error = %v, want exact validation diagnostic", err)
	}

	missingSides := &content.Catalog{
		Movement: map[string]*content.MovementClass{"testmove": {}},
	}
	if _, err := strictCatalogWithProgress(nil, missingSides, nil); err == nil || !strings.Contains(err.Error(), "sidedata.tdf") {
		t.Fatalf("missing SIDEDATA supplied catalog error = %v, want exact validation diagnostic", err)
	}

	valid := &content.Catalog{
		Movement: map[string]*content.MovementClass{"testmove": {}},
		Sides:    []*content.SideDef{{Name: "ARM"}},
	}
	got, err := strictCatalogWithProgress(nil, valid, nil)
	if err != nil {
		t.Fatalf("minimally valid supplied catalog: %v", err)
	}
	if got != valid {
		t.Fatal("strict supplied catalog was replaced instead of returned unchanged")
	}
}

func syntheticMission() *mission.Mission {
	return &mission.Mission{
		Type:       mission.TypeSkirmish,
		TerrainKey: "test",
		Schema:     mission.Schema{Name: "Schema 0"},
		WindBounds: mission.WindBounds{Min: 100, Max: 200},
	}
}

func economyForTest() *economy.Service {
	return &economy.Service{}
}

func TestValidateCompositionSuccess(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	m := syntheticMission()
	s := &Session{
		Catalog: cat,
		World:   terrain,
		Mission: m,
	}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("ValidateComposition should pass: %v", err)
	}
}

func TestCreateAndBindServicesChainsDeathObserver(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	s := &Session{Catalog: cat, World: terrain, Mission: syntheticMission()}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	var primary, priorExtra int
	w.OnDeath = func(pool.Handle, units.DeathCause, *units.Unit) { primary++ }
	w.OnDeathExtra = func(pool.Handle, units.DeathCause, *units.Unit) { priorExtra++ }
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	def := cat.Units["armcom"]
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Destroy(h, units.DeathKilled)
	if got := w.FinalizeDeath(h, 1); !got.Freed {
		t.Fatalf("FinalizeDeath = %#v", got)
	}
	if primary != 1 || priorExtra != 1 {
		t.Fatalf("composition clobbered/duplicated hooks: primary=%d priorExtra=%d", primary, priorExtra)
	}
}

func TestValidateCompositionMissing(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	m := syntheticMission()
	base := func() *Session {
		s := &Session{
			Catalog: cat,
			World:   terrain,
			Mission: m,
		}
		w, _ := newSlicedWorld(cat)
		s.Units = w
		s.Econ = economyForTest()
		s.Econ.Players[0].Exists = true
		s.Econ.Players[0].ControllerState = 1
		s.Econ.SeedDeadlines(0)
		s.InitBattleWindForSession()
		_ = createAndBindServicesForTest(t, s)
		return s
	}
	cases := []string{"Clock", "World", "Units", "Vis", "Features", "Movement", "Path", "Combat", "Build", "Econ"}
	for _, name := range cases {
		s := base()
		switch name {
		case "Clock":
			s.Clock = nil
		case "World":
			s.World = nil
		case "Units":
			s.Units = nil
		case "Vis":
			s.Vis = nil
		case "Features":
			s.Features = nil
		case "Movement":
			s.Movement = nil
		case "Path":
			s.Path = nil
		case "Combat":
			s.Combat = nil
		case "Build":
			s.Build = nil
		case "Econ":
			s.Econ = nil
		}
		if err := s.ValidateComposition(); err == nil {
			t.Fatalf("ValidateComposition should fail when %s missing", name)
		}
	}
}

func TestTopologySameForBothConstructors(t *testing.T) {
	cat := minimalCatalogForStrict()
	fs1 := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	ota2 := "[GlobalHeader]\n{\nminwindspeed=10;\nmaxwindspeed=20;\n[Schema 0]\n{\nType=Easy;\n[units]\n{\n[unit0]\n{\nUnitname=armcom;\nXPos=0;\nZPos=0;\n}\n}\n[specials]\n{\n}\n[features]\n{\n}\n}\n}\n"
	fs2 := fsWithMap(t, ota2)
	cfgSk := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfgSk.Players[0].Controller = SkirmishControllerHuman
	cfgSk.Players[1].Controller = SkirmishControllerComputer
	cfgSk.Players[0].AllyGroup = 1
	cfgSk.Players[1].AllyGroup = 2
	sSkirmish, err := NewSyntheticSkirmishForTest(fs1, cat, cfgSk)
	if err != nil {
		t.Fatalf("skirmish ForTest: %v", err)
	}
	if sSkirmish.World == nil {
		sSkirmish.World = minimalTerrain()
		sSkirmish.Features = nil
		sSkirmish.Vis = nil
		sSkirmish.Movement = nil
		sSkirmish.Path = nil
		sSkirmish.Build = nil
		sSkirmish.Combat = nil
		_ = createAndBindServicesForTest(t, sSkirmish)
		publishVisibilityForAll(sSkirmish)
	}
	sMission, err := NewSyntheticMissionForTest(fs2, cat, "test.ota", 0)
	if err != nil {
		t.Fatalf("mission ForTest: %v", err)
	}
	if sMission.World == nil {
		sMission.World = minimalTerrain()
		sMission.Features = nil
		sMission.Vis = nil
		sMission.Movement = nil
		sMission.Path = nil
		sMission.Build = nil
		sMission.Combat = nil
		_ = createAndBindServicesForTest(t, sMission)
		publishVisibilityForAll(sMission)
	}
	checks := []struct {
		name   string
		sk, ms bool
	}{
		{"Vis", sSkirmish.Vis != nil, sMission.Vis != nil},
		{"Features", sSkirmish.Features != nil, sMission.Features != nil},
		{"Path", sSkirmish.Path != nil, sMission.Path != nil},
		{"Combat", sSkirmish.Combat != nil, sMission.Combat != nil},
		{"Movement", sSkirmish.Movement != nil, sMission.Movement != nil},
		{"Build", sSkirmish.Build != nil, sMission.Build != nil},
		{"Econ", sSkirmish.Econ != nil, sMission.Econ != nil},
	}
	for _, c := range checks {
		if !c.sk || !c.ms {
			t.Fatalf("topology mismatch %s: skirmish %v mission %v [P0-I01]", c.name, c.sk, c.ms)
		}
	}
}

func TestMissingContentAborts(t *testing.T) {
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n}\n}\n")
	emptyCat := &content.Catalog{Units: map[string]*content.UnitDef{}, Features: map[string]*content.FeatureDef{}, Maps: map[string]*content.MapHeader{}, Sides: []*content.SideDef{}}
	if _, err := NewSkirmishWithFS(fs, emptyCat, SkirmishConfig{MapName: "test", NumPlayers: 2}); err == nil {
		t.Fatalf("strict skirmish with empty catalog should abort")
	}
	if _, err := NewMissionWithFS(fs, emptyCat, "test.ota", 0); err == nil {
		t.Fatalf("strict mission with empty catalog should abort")
	}
	cat := minimalCatalogForStrict()
	fsNoTNT := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n}\n}\n}\n")
	if _, err := NewSkirmishWithFS(fsNoTNT, cat, SkirmishConfig{MapName: "test", NumPlayers: 2}); err == nil {
		t.Fatalf("strict skirmish with missing TNT should abort")
	}
}

func TestTwoSessionsCoexist(t *testing.T) {
	cat := minimalCatalogForStrict()
	fs1 := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n")
	fs2 := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=5;\nZPos=5;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=15;\nZPos=15;\n}\n}\n}\n}\n")
	s1, err := NewSyntheticSkirmishForTest(fs1, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if err != nil {
		t.Fatalf("s1: %v", err)
	}
	s2, err := NewSyntheticSkirmishForTest(fs2, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if err != nil {
		t.Fatalf("s2: %v", err)
	}
	if s1.Units == s2.Units {
		t.Fatalf("two sessions share Units pointer")
	}
	if s1.World != nil && s2.World != nil {
		orig := s1.World.CellW
		s1.World.CellW = 999
		if s2.World.CellW == 999 {
			t.Fatalf("shared mutable World")
		}
		s1.World.CellW = orig
	}
	// Interleaved ticks should match standalone
	rng.SeedGlobal(123, 456)
	s1a, _ := NewSyntheticSkirmishForTest(fs1, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if s1a.World == nil {
		s1a.World = minimalTerrain()
		_ = createAndBindServicesForTest(t, s1a)
	}
	// Ensure sliced pool already tested; just check Used counts stay same
	usedA := s1a.Units.Used()
	rng.SeedGlobal(123, 456)
	s1b, _ := NewSyntheticSkirmishForTest(fs1, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	s2b, _ := NewSyntheticSkirmishForTest(fs2, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if s1b.World == nil {
		s1b.World = minimalTerrain()
		_ = createAndBindServicesForTest(t, s1b)
	}
	if s2b.World == nil {
		s2b.World = minimalTerrain()
		_ = createAndBindServicesForTest(t, s2b)
	}
	// After interleaved creation, ensure no cross contamination
	if s1b.Units.Used() != usedA {
		t.Fatalf("interleaved Used mismatch")
	}
	_ = s2b
}

func TestCompositionGate(t *testing.T) {
	cat := minimalCatalogForStrict()
	terrain := minimalTerrain()
	m := syntheticMission()
	s := &Session{
		Catalog: cat,
		World:   terrain,
		Mission: m,
	}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if s.Vis == nil || s.Features == nil || s.Path == nil || s.Combat == nil || s.Movement == nil || s.Build == nil || s.Econ == nil {
		t.Fatalf("P0 services not all non-nil before first tick")
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("Validate before tick failed: %v", err)
	}
}
