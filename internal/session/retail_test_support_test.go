package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// HashState returns a compact deterministic digest for the authoritative
// session state used by replay-isolation tests. It deliberately walks the
// engine's ordered slices rather than serializing test metadata.
func HashState(s *Session) string {
	if s == nil {
		return ""
	}
	h := sha256.New()
	if s.Units != nil {
		for player := 0; player < 10; player++ {
			fmt.Fprintf(h, "C%d:%d|", player, s.Units.CreatedCountForPlayer(player))
		}
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			// Group is authoritative manager/control-group state. It is hashed
			// separately from the tactical vectors below so an unlisted unit's
			// group transition cannot alias an otherwise identical state.
			fmt.Fprintf(h, "U%d:%d:%d:%d:%.2f:%d:%d:%t:%t:%t:%t:%t|", u.Handle, int64(u.X.Raw()), int64(u.Z.Raw()), u.Health, u.Remaining, u.Flags, u.Group, u.InBuildStance, u.Busy, u.YardOpen, u.BuggerOff, u.Armored)
		}
	}
	if s.Combat != nil {
		for i := 0; i < s.Combat.Count(); i++ {
			if i >= len(s.Combat.Records) {
				break
			}
			p := s.Combat.Records[i]
			if s.Combat.IsDead(pool.Handle(i + 1)) {
				continue
			}
			fmt.Fprintf(h, "P%d:%d:%d:%d|", i, int64(p.Pos.X.Raw()), int64(p.Pos.Y.Raw()), int64(p.Pos.Z.Raw()))
		}
	}
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := &s.Econ.Players[i]
			if !p.Exists {
				continue
			}
			// Hash the exact float32 payloads used by the authoritative economy,
			// rather than a rounded display representation. The latter aliases
			// distinct stocks and can make a resumed state appear equivalent.
			fmt.Fprintf(h, "E%d:%08x:%08x|", i,
				math.Float32bits(p.Stock[economy.Metal]),
				math.Float32bits(p.Stock[economy.Energy]))
			// The strategic score consumes the four settled aggregates, not the
			// mirror-only pass counters. Hash raw float32 payloads so every
			// authoritative bit (including signed zero/NaN payloads) is covered
			// deterministically in player then resource order [01 §4.4].
			fmt.Fprintf(h, "A%d:%08x:%08x:%08x:%08x|", i,
				math.Float32bits(p.AIProduction[economy.Metal]),
				math.Float32bits(p.AIProduction[economy.Energy]),
				math.Float32bits(p.AIConsumption[economy.Metal]),
				math.Float32bits(p.AIConsumption[economy.Energy]))
		}
	}
	// Manager tactical vectors affect future AI admissions and task choices;
	// include their exact recovered slot order in the authoritative hash
	// [08 "AI group vectors"]. Handle sequence is meaningful because vector insertion uses
	// pool order and wave merge uses replace-with-last removal.
	for player, m := range s.AI {
		if m == nil {
			continue
		}
		fmt.Fprintf(h, "G%d:", player)
		groups := [][]pool.Handle{
			m.GroupResource, m.GroupWaveA, m.GroupRegroupA,
			m.GroupConstruction, m.GroupNull, m.GroupWaveB,
			m.GroupRegroupB, m.GroupExplore, m.GroupRally,
		}
		for slot, members := range groups {
			fmt.Fprintf(h, "%d[", slot+1)
			for _, handle := range members {
				fmt.Fprintf(h, "%d,", handle)
			}
			fmt.Fprint(h, "];")
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func strictMinimalCatalog() *content.Catalog {
	mv := map[string]*content.MovementClass{
		// Hand-authored compiled record, template-initialized per the compiler
		// contract [04 §6.1 R-DOC04-A]: unauthored fields carry the startup
		// template (minwaterdepth -10000, maxwaterslope 255) and bad slopes
		// chain to half the Max just read [02 §5 "Movement class record"].
		"testmove": {FootprintX: 1, FootprintZ: 1, MaxWaterDepth: 10, MinWaterDepth: -10000, MaxSlope: 10, BadSlope: 5, MaxWaterSlope: 255, BadWaterSlope: 127},
	}
	mv["testmove"].CanonicalKey = content.CanonicalKey("testmove")
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			// BMCode is what makes a definition mobile rather than a
			// building; every stock mobile unit authors it, and a fixture
			// modelling a commander must too, or the runtime building-class
			// status bit sends it down the structure branches
			// [08 "Classifier eligibility, destinations, and order"].
			"armcom": {UnitName: "armcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, BuildTime: 100, WorkerTime: 30, CanMove: true, MaxVelocity: 2000, TurnRate: 1000, Builder: true, BMCode: 1},
			"corcom": {UnitName: "corcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, BuildTime: 100, WorkerTime: 30, CanMove: true, MaxVelocity: 2000, TurnRate: 1000, Builder: true, BMCode: 1},
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
		u.MovementClass = "testmove"
	}
	installFixtureCOB(cat)
	return cat
}

func strictMinimalTerrain() *world.Terrain {
	attrs := make([]formats.TNTAttribute, 32*32)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 32, 32)
	ter := &world.Terrain{
		CellW: 32, CellH: 32,
		Plot: plot, Version: 0x2000, SeaLevel: 0, WindMin: 100, WindMax: 2000,
	}
	_ = ter.ApplySchema(nil, 0)
	return ter
}

func strictSyntheticMission() *mission.Mission {
	return &mission.Mission{
		Type: mission.TypeSkirmish, TerrainKey: "test",
		Schema:     mission.Schema{Name: "Schema 0"},
		WindBounds: mission.WindBounds{Min: 100, Max: 200},
	}
}

func strictEconomyForTest() *economy.Service {
	return &economy.Service{}
}

// strictNewSessionWithUnits builds a deterministic session fixture for N units
// distributed across players 0 and 1.
func strictNewSessionWithUnits(t *testing.T, nUnits int, simSeed, crtSeed uint32) *Session {
	t.Helper()
	cat := strictMinimalCatalog()
	terrain := strictMinimalTerrain()
	m := strictSyntheticMission()
	s := &Session{
		Catalog: cat, World: terrain, Mission: m,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Snapshot: &frame.Buffer{},
	}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.IsObserver = false
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)
	s.SeedSessionRNG(simSeed, crtSeed)
	s.InitBattleWindForSession()
	_ = createAndBindServicesForTest(t, s)
	s.RegisterAll()
	s.State = StateBattle
	def := cat.Units["armcom"]
	for i := 0; i < nUnits; i++ {
		owner := uint8(i % 2)
		x := numeric.Fixed(int64((10 + i*5) * 65536))
		z := numeric.Fixed(int64((10 + i*5) * 65536))
		y := terrain.HeightAt(x, z)
		if y == numeric.Fixed(-1) {
			y = 0
		}
		_, _ = s.Units.Create(def, owner, x, y, z)
	}
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	return s
}
