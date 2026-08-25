package ai

import (
	"hash/fnv"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// productID replicates construction.productID for queue inspection.
func gateProductID(defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	h := fnv.New32a()
	_, _ = h.Write([]byte(ck))
	return h.Sum32()
}

func flatAttrs(cellW, cellH int32, height uint8) []formats.TNTAttribute {
	attrs := make([]formats.TNTAttribute, cellW*cellH)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: world.PlotFeatureNone}
	}
	return attrs
}

func makeGateCatalog() (*content.Catalog, *content.UnitDef, *content.UnitDef) {
	builderDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("gatebuilder")},
		UnitName:         "gatebuilder",
		FootprintX:       2,
		FootprintZ:       2,
		YardMap:          "oooo",
		Builder:          true,
		MaxDamage:        100,
		EnergyStorage:    1000,
		MetalStorage:     500,
	}
	faveeDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("gatefavee")},
		UnitName:         "gatefavee",
		FootprintX:       2,
		FootprintZ:       2,
		YardMap:          "oooo",
		ExtractsMetal:    0,
		MaxDamage:        100,
		EnergyStorage:    0,
		MetalStorage:     0,
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("gatebuilder"): builderDef,
			content.CanonicalKey("gatefavee"):   faveeDef,
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("gatebuilder"): {
				DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("gatebuilder")},
				Builder:          "gatebuilder",
				Buttons:          []string{"gatefavee"},
			},
		},
	}
	return cat, builderDef, faveeDef
}

func makeGateTerrain() *world.Terrain {
	const cw, ch int32 = 32, 32
	attrs := flatAttrs(cw, ch, 10)
	plot := world.ExpandPlot(attrs, int(cw), int(ch))
	return &world.Terrain{
		CellW:   cw,
		CellH:   ch,
		Plot:    plot,
		Version: world.VersionCanonical,
	}
}

func makeGateEconomy(player uint8) economy.Service {
	var svc economy.Service
	p := &svc.Players[player]
	p.Exists = true
	p.ControllerState = 2
	p.IsObserver = false
	p.StatusHalfwordAt144 = 1
	p.StatusWordAt140 = 0
	p.GameEnded = false
	p.EndGameCountdown = -1
	p.Stock[economy.Energy] = 800
	p.Stock[economy.Metal] = 400
	p.Capacity[economy.Energy] = 1000
	p.Capacity[economy.Metal] = 500
	p.PassProduced[economy.Energy] = 300
	p.PassProduced[economy.Metal] = 10
	p.PassConsumed[economy.Energy] = 0
	p.PassConsumed[economy.Metal] = 0
	// Seed deadlines to tick 0 phase-aligned.
	svc.SeedDeadlines(0)
	// SeedDeadlines overrides Stock/Capacity? It only touches deadlines, so restore our seeded stock/capacity.
	p = &svc.Players[player]
	p.Stock[economy.Energy] = 800
	p.Stock[economy.Metal] = 400
	p.Capacity[economy.Energy] = 1000
	p.Capacity[economy.Metal] = 500
	p.PassProduced[economy.Energy] = 300
	p.PassProduced[economy.Metal] = 10
	return svc
}

// TestGate6EndToEnd is the Gate 6 lock: skirmish AI queues a real unit at a valid placement within 900 ticks.
func TestGate6EndToEnd(t *testing.T) {
	const seed uint32 = 0x12345678
	const player uint8 = 1

	rng.SeedGlobal(seed, 0)

	cat, builderDef, faveeDef := makeGateCatalog()
	terrain := makeGateTerrain()

	// P0-I16: selection state now per-manager, not package global.

	// Profile with weight for favee, no limit.
	prof := &Profile{
		Plan:   DifficultyAny,
		Weight: map[string]int32{content.CanonicalKey("gatefavee"): 100},
		Limit:  map[string]int32{},
	}

	w := units.New(64, cat)
	// Place builder at cell 5,5.
	bx := world.CellToWorld(5)
	bz := world.CellToWorld(5)
	h, err := w.Create(builderDef, player, bx, 0, bz)
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	builderUnit := w.Unit(h)
	if builderUnit == nil {
		t.Fatal("builder nil")
	}
	// Ensure world iteration finds it (alive, remaining 0 = completed).
	builderUnit.Remaining = 0

	svc := makeGateEconomy(player)

	strat := Strategic{
		CenterX:                world.CellToWorld(16),
		CenterZ:                world.CellToWorld(16),
		Radius:                 0,
		LastRefreshTick:        0,
		Counts:                 map[string]int32{},
		ClassVectors:           map[string]ClassVector{content.CanonicalKey("gatefavee"): {C0: 40, C1: 30, C2: 30}, content.CanonicalKey("gatebuilder"): {C0: 40}},
		LastClassRecomputeTick: 0,
	}

	mgr := &Manager{
		Player:       player,
		Strategic:    strat,
		Profile:      prof,
		OriginX:      world.CellToWorld(5),
		OriginZ:      world.CellToWorld(5),
		RNG:          rng.Global.Sim,
		SurfaceMetal: 0,
		Catalog:      cat,
		Factory:      builderUnit,
	}
	mgr.Strategic.Catalog = cat
	mgr.CandidateSource = nil
	mgr.MissionGateFlag = 0
	mgr.GateCandidates = nil
	for k := TaskKind(0); k < TaskKindCount; k++ {
		mgr.Deadlines[k] = 0
	}

	// Spy Place's ordinary path via per-manager QueueBuild [P0-I16].
	var placeSpyDef string
	var placeSpyCalled bool
	mgr.QueueBuild = func(f *units.Unit, defKey string, count int) error {
		placeSpyCalled = true
		placeSpyDef = defKey
		return construction.QueueBuild(f, defKey, count)
	}

	expectedPID := gateProductID("gatefavee")

	var successTick int = -1
	var successX, successZ numeric.Fixed
	var successValid bool

	runLoop := func() int {
		// Fresh world/factory needed for second determinism run; this closure captures current w,mgr,svc,terrain etc.
		// For the primary run we already have them, so just iterate.
		for tick := 0; tick < 900; tick++ {
			svc.TickPlayer(int(player), uint32(tick), w, func() {
				mgr.Tick(uint32(tick), w, &svc)
			})
			// After manager tick, inspect ordinary construction queue (via orders) for real second unit.
			q := orders.QueueForUnit(builderUnit)
			if q == nil {
				continue
			}
			found := false
			for _, n := range q.Primary() {
				if n == nil {
					continue
				}
				if n.Param1 == expectedPID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			// Queue happened — now verify Select + Place wiring with same manager fields.
			// Select should pick the real second def.
			cand, ok := Select(mgr, builderUnit, &svc)
			if !ok {
				t.Fatalf("tick %d: queue found but Select failed", tick)
			}
			if content.CanonicalKey(cand.DefKey) != content.CanonicalKey("gatefavee") {
				t.Fatalf("tick %d: Select picked %q want gatefavee", tick, cand.DefKey)
			}
			if cand.DefKey == builderDef.UnitName {
				t.Fatalf("tick %d: Select picked builder's own def (self-gate violated)", tick)
			}
			// Place should succeed at valid placement via manager's Origin/RNG/SurfaceMetal/Catalog/Factory.
			// Use terrain for validation; Place will move origin toward center and reset radius on success.
			// Save origin before Place to report.
			preX, preZ := mgr.OriginX, mgr.OriginZ
			// Reset spy flag for this Place call.
			placeSpyCalled = false
			placeSpyDef = ""
			x, z, ok := Place(mgr, cand.DefKey, terrain)
			if !ok {
				// Fallback: at least validate that the queued placement would be valid even if Place helper blocked.
				// This would be a seam: manager queues without validated placement.
				t.Fatalf("tick %d: Place(%q) failed even though construction queue succeeded; preOrigin %d,%d", tick, cand.DefKey, preX, preZ)
			}
			// ValidatePlacement at returned coordinates must pass.
			footX := int(faveeDef.FootprintX)
			footZ := int(faveeDef.FootprintZ)
			yard, perr := world.ParseYardMap(faveeDef.YardMap, footX, footZ)
			if perr != nil {
				t.Fatalf("yard parse: %v", perr)
			}
			cx := world.WorldToCell(x)
			cz := world.WorldToCell(z)
			if err := terrain.ValidatePlacement(cx, cz, yard, footX, footZ, 0); err != nil {
				t.Fatalf("tick %d: ValidatePlacement at Place %d,%d cell %d,%d failed: %v", tick, x, z, cx, cz, err)
			}
			if !placeSpyCalled || content.CanonicalKey(placeSpyDef) != content.CanonicalKey("gatefavee") {
				t.Fatalf("tick %d: Place did not issue queueBuild for gatefavee spy %v %q", tick, placeSpyCalled, placeSpyDef)
			}
			successTick = tick
			successX = x
			successZ = z
			successValid = true
			return tick
		}
		return -1
	}

	tick := runLoop()
	if tick == -1 || !successValid {
		t.Fatalf("no construction.QueueBuild for REAL second unit at VALID placement within 900 ticks (builder %q favee %q)", builderDef.UnitName, faveeDef.UnitName)
	}
	t.Logf("gate6: queued %q at tick %d placement %d,%d cell %d,%d valid", faveeDef.UnitName, successTick, successX, successZ, world.WorldToCell(successX), world.WorldToCell(successZ))

	// Determinism: rerun with same seed and assert same tick and placement.
	// Re-seed and rebuild minimal fresh state to avoid polluted queues [P0-I16].
	rng.SeedGlobal(seed, 0)
	cat2, builderDef2, faveeDef2 := makeGateCatalog()
	terrain2 := makeGateTerrain()
	w2 := units.New(64, cat2)
	bx2 := world.CellToWorld(5)
	bz2 := world.CellToWorld(5)
	h2, _ := w2.Create(builderDef2, player, bx2, 0, bz2)
	builderUnit2 := w2.Unit(h2)
	builderUnit2.Remaining = 0
	svc2 := makeGateEconomy(player)
	strat2 := Strategic{
		CenterX: world.CellToWorld(16),
		CenterZ: world.CellToWorld(16),
		Radius:  0,
		Counts:  map[string]int32{},
		ClassVectors: map[string]ClassVector{
			content.CanonicalKey("gatefavee"):   {C0: 40, C1: 30, C2: 30},
			content.CanonicalKey("gatebuilder"): {C0: 40},
		},
	}
	mgr2 := &Manager{
		Player:       player,
		Strategic:    strat2,
		Profile:      prof,
		OriginX:      world.CellToWorld(5),
		OriginZ:      world.CellToWorld(5),
		RNG:          rng.Global.Sim,
		SurfaceMetal: 0,
		Catalog:      cat2,
		Factory:      builderUnit2,
	}
	mgr2.Strategic.Catalog = cat2
	mgr2.CandidateSource = nil
	mgr2.MissionGateFlag = 0
	mgr2.GateCandidates = nil
	for k := TaskKind(0); k < TaskKindCount; k++ {
		mgr2.Deadlines[k] = 0
	}
	// Reset spy [P0-I16].
	placeSpyCalled = false
	placeSpyDef = ""
	mgr2.QueueBuild = func(f *units.Unit, defKey string, count int) error {
		placeSpyCalled = true
		placeSpyDef = defKey
		return construction.QueueBuild(f, defKey, count)
	}
	expectedPID2 := gateProductID("gatefavee")
	var tick2 = -1
	var x2, z2 numeric.Fixed
	for tick := 0; tick < 900; tick++ {
		svc2.TickPlayer(int(player), uint32(tick), w2, func() {
			mgr2.Tick(uint32(tick), w2, &svc2)
		})
		q := orders.QueueForUnit(builderUnit2)
		if q == nil {
			continue
		}
		found := false
		for _, n := range q.Primary() {
			if n != nil && n.Param1 == expectedPID2 {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		cand, ok := Select(mgr2, builderUnit2, &svc2)
		if !ok {
			t.Fatalf("determinism rerun: Select failed at %d", tick)
		}
		placeSpyCalled = false
		x, z, ok := Place(mgr2, cand.DefKey, terrain2)
		if !ok {
			t.Fatalf("determinism rerun: Place failed at %d", tick)
		}
		tick2 = tick
		x2, z2 = x, z
		_ = faveeDef2
		break
	}
	if tick2 != successTick {
		t.Fatalf("determinism failed: first run tick %d second run tick %d (seed %08x)", successTick, tick2, seed)
	}
	if x2 != successX || z2 != successZ {
		t.Fatalf("determinism placement mismatch: %d,%d vs %d,%d", successX, successZ, x2, z2)
	}
	t.Logf("gate6 determinism: tick %d placement %d,%d hash seed %08x", tick2, x2, z2, seed)
}
