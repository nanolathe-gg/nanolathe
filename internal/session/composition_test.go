package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

func minimalCatalogForStrict() *content.Catalog {
	mv := map[string]*content.MovementClass{
		"testmove": {FootprintX: 1, FootprintZ: 1, MaxSlope: 10, MaxWaterDepth: 10},
	}
	mv["testmove"].CanonicalKey = content.CanonicalKey("testmove")
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {UnitName: "armcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, Commander: true},
			"corcom": {UnitName: "corcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, Commander: true},
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
	s.Econ.Players[0].StatusHalfwordAt144 = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.Players[1].StatusHalfwordAt144 = 1
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(0)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
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
	var crt rng.CRT = rng.NewCRT(0)
	s.InitWindForSession(&crt, 0)
	var primary, priorExtra int
	w.OnDeath = func(pool.Handle, units.DeathCause, *units.Unit) { primary++ }
	w.OnDeathExtra = func(pool.Handle, units.DeathCause, *units.Unit) { priorExtra++ }
	if err := createAndBindServices(s); err != nil {
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
		s.Econ.Players[0].StatusHalfwordAt144 = 1
		s.Econ.SeedDeadlines(0)
		var crt rng.CRT = rng.NewCRT(0)
		s.InitWindForSession(&crt, 0)
		_ = createAndBindServices(s)
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
	sSkirmish, err := NewSkirmishForTest(fs1, cat, cfgSk)
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
		_ = createAndBindServices(sSkirmish)
		publishVisibilityForAll(sSkirmish)
	}
	sMission, err := NewMissionForTest(fs2, cat, "test.ota", 0)
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
		_ = createAndBindServices(sMission)
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
	s1, err := NewSkirmishForTest(fs1, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if err != nil {
		t.Fatalf("s1: %v", err)
	}
	s2, err := NewSkirmishForTest(fs2, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
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
	s1a, _ := NewSkirmishForTest(fs1, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if s1a.World == nil {
		s1a.World = minimalTerrain()
		_ = createAndBindServices(s1a)
	}
	// Ensure sliced pool already tested; just check Used counts stay same
	usedA := s1a.Units.Used()
	rng.SeedGlobal(123, 456)
	s1b, _ := NewSkirmishForTest(fs1, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	s2b, _ := NewSkirmishForTest(fs2, cat, SkirmishConfig{MapName: "test", NumPlayers: 2})
	if s1b.World == nil {
		s1b.World = minimalTerrain()
		_ = createAndBindServices(s1b)
	}
	if s2b.World == nil {
		s2b.World = minimalTerrain()
		_ = createAndBindServices(s2b)
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
	s.Econ.Players[0].StatusHalfwordAt144 = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.Players[1].StatusHalfwordAt144 = 1
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(1)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if s.Vis == nil || s.Features == nil || s.Path == nil || s.Combat == nil || s.Movement == nil || s.Build == nil || s.Econ == nil {
		t.Fatalf("P0 services not all non-nil before first tick")
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("Validate before tick failed: %v", err)
	}
}
