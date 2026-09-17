package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A builder-flagged definition whose compiled build menu holds no entries is a
// shipped configuration, not a corner case: the air repair pads, the carriers,
// the decoy commanders, the Fark and the Necro all carry `builder` for assist,
// repair and reclaim and compile zero build options. Retail's manager splits on
// two DIFFERENT keys — the authored flag and the compiled entry COUNT — so such
// a definition is admitted everywhere the flag (or the list's existence) is the
// key and skipped exactly where the count is [08 R-AI-01 §3][08 R-AI-01 §16]
// [08 R-P0-04][08 R-P0-05 §5][08 R-P0-05 §9].
//
// These tests lock that split at every site, because reading one key for the
// other is invisible until a Fark stalls a Commander's whole build programme.

const emptyMenuBuilderKey = "empty-menu-builder"
const stockedMenuBuilderKey = "stocked-menu-builder"

// emptyMenuBuilderDef is a mobile builder-flagged definition. Its build menu is
// supplied by the fixtures below and is what varies between the two directions.
func emptyMenuBuilderDef(key string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: key},
		UnitName:         key,
		Side:             "ARM",
		Builder:          true,
		BMCode:           1, // mobile, so the initialization pass adds no building term
		CanMove:          true,
		CanPatrol:        true, // pass two's reposition resolves the patrol intent
		MaxVelocity:      100,
		MaxDamage:        100,
		MinWaterDepth:    -1,
	}
}

// emptyMenuConstructionFixture builds two mobile builders that differ only in
// their compiled build menu: one page with no entries, one page with a single
// entry. Both are classifier-eligible and completed.
func emptyMenuConstructionFixture(t *testing.T) (*Manager, *units.World, *units.Unit, *units.Unit, *economy.Service, *rng.Simulation, *[]pool.Handle) {
	t.Helper()
	emptyDef := emptyMenuBuilderDef(emptyMenuBuilderKey)
	stockedDef := emptyMenuBuilderDef(stockedMenuBuilderKey)
	productDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "empty-menu-product"},
		UnitName:         "empty-menu-product",
		Side:             "ARM",
		FootprintX:       2,
		FootprintZ:       2,
		YardMap:          "o",
		MaxDamage:        100,
		MaxSlope:         255,
		MaxWaterSlope:    255,
		MaxWaterDepth:    10000,
		MinWaterDepth:    -10000,
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			emptyDef.CanonicalKey:   emptyDef,
			stockedDef.CanonicalKey: stockedDef,
			productDef.CanonicalKey: productDef,
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			// The page EXISTS and holds no entries — the shipped shape.
			emptyDef.CanonicalKey:   {},
			stockedDef.CanonicalKey: {Buttons: []string{productDef.CanonicalKey}},
		},
	}
	w := newAIFixtureWorld(4, cat)
	place := func(def *content.UnitDef, cell int32) *units.Unit {
		h, err := w.Create(def, 1, world.CellToWorld(cell), 0, world.CellToWorld(32))
		if err != nil {
			t.Fatal(err)
		}
		u := w.Unit(h)
		u.Remaining = 0
		u.Flags |= units.ClassifierEligibleStatus
		return u
	}
	empty := place(emptyDef, 32)
	stocked := place(stockedDef, 40)

	sim := rng.NewSimulation(7)
	var submitted []pool.Handle
	m := &Manager{
		Player:       1,
		Profile:      &Profile{Weight: map[string]int32{productDef.CanonicalKey: 100}, Limit: map[string]int32{}},
		Catalog:      cat,
		Terrain:      placementTerrain(64, 64, 0),
		OrderBinding: aiFixtureOrderBinding(cat, &sim),
		RNG:          &sim,
		QueueBuildTyped: func(req BuildRequest) error {
			submitted = append(submitted, req.Builder)
			return nil
		},
	}
	m.Strategic = Strategic{
		CenterX:         empty.X,
		CenterZ:         empty.Z,
		Radius:          37,
		Counts:          map[string]int32{productDef.CanonicalKey: 0},
		ClassVectors:    map[string]ClassVector{productDef.CanonicalKey: {C0: 100}},
		Catalog:         cat,
		LandRegion:      PlacementRegion{CellW: 20, CellH: 20},
		WaterRegion:     PlacementRegion{CellW: 20, CellH: 20},
		setupDrawsReady: true,
	}
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)
	return m, w, empty, stocked, econ, &sim, &submitted
}

// TestEmptyBuildMenuBuilderIsClassifiedButNeverPlaces locks the three
// construction-task sites at once: the classifier keys on the AUTHORED flag, so
// an empty-menu builder joins the construction group; pass one keys on the
// compiled ENTRY COUNT, so it never reaches selection or placement; pass two
// tests neither, so it is still repositioned [08 R-P0-04][08 R-AI-01 §3].
func TestEmptyBuildMenuBuilderIsClassifiedButNeverPlaces(t *testing.T) {
	m, w, empty, stocked, econ, sim, submitted := emptyMenuConstructionFixture(t)

	m.classifyGroups(w)
	if empty.Group != 4 {
		t.Fatalf("empty-menu builder group = %d, want the construction record 4 [08 R-P0-04]", empty.Group)
	}
	if len(m.GroupConstruction) != 2 {
		t.Fatalf("construction vector = %v, want both builders [08 R-P0-04]", m.GroupConstruction)
	}

	// Pass one: the empty-menu builder is skipped before selection, so it draws
	// no RNG and submits nothing; its sibling, identical but for one menu
	// entry, does both.
	before := sim.Draws()
	m.constructionPlacePass(90, w, econ, m.Strategic.CenterX, m.Strategic.CenterZ, 0)
	if len(*submitted) != 1 || (*submitted)[0] != stocked.Handle {
		t.Fatalf("pass-one submissions = %v, want exactly the stocked builder %d [08 R-AI-01 §3]", *submitted, stocked.Handle)
	}
	if q := orders.QueueOfUnit(empty); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("skipped builder received %d pass-one orders [08 R-AI-01 §3]", q.LenPrimary())
	}
	if sim.Draws() == before {
		t.Fatal("neither builder reached selection; the fixture proves nothing")
	}

	// Pass two reads no build-option key at all.
	m.constructionRepositionPass(90, w, m.Strategic.CenterX, m.Strategic.CenterY, m.Strategic.CenterZ, 0)
	q := orders.QueueOfUnit(empty)
	if q == nil || q.LenPrimary() == 0 {
		t.Fatal("empty-menu builder was not repositioned by pass two [08 R-AI-01 §3]")
	}
}

// TestEmptyBuildMenuBuilderIsSkippedByTheFactoryBranch locks the eco task's
// factory branch, which reads the same compiled entry count as construction
// pass one [08 R-AI-01 §2].
func TestEmptyBuildMenuBuilderIsSkippedByTheFactoryBranch(t *testing.T) {
	m, w, empty, stocked, econ, _, submitted := emptyMenuConstructionFixture(t)
	// The branch requires a building that makes no metal. Both fixtures become
	// buildings here; nothing else about them changes.
	for _, u := range []*units.Unit{empty, stocked} {
		u.Flags |= units.BuildingClassStatus
		m.GroupResource = append(m.GroupResource, u.Handle)
	}

	m.doResource(120, w, econ)

	if len(*submitted) != 1 || (*submitted)[0] != stocked.Handle {
		t.Fatalf("factory-branch submissions = %v, want exactly the stocked builder %d [08 R-AI-01 §2]", *submitted, stocked.Handle)
	}
}

// TestEmptyBuildMenuBuilderKeepsEveryFlagKeyedTerm locks the three sites that
// key on the authored flag (equivalently, on the compiled list EXISTING) rather
// than on its entry count: the 30-tick refresh's build-capable counter, the
// initialization pass's +20 weight term and the class routine's +30 other-mix
// term. Counting only non-empty menus there would let five Farks stop being
// five builders, which is what gates a `cancapture` builder's placement
// [08 R-AI-01 §16][08 R-P0-05 §9][08 R-P0-05 §5].
func TestEmptyBuildMenuBuilderKeepsEveryFlagKeyedTerm(t *testing.T) {
	def := emptyMenuBuilderDef(emptyMenuBuilderKey)
	cat := &content.Catalog{
		Units:      map[string]*content.UnitDef{def.CanonicalKey: def},
		BuildMenus: map[string]*content.BuildMenuPage{def.CanonicalKey: {}},
	}
	w := newAIFixtureWorld(2, cat)
	h, err := w.Create(def, 1, world.CellToWorld(8), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Remaining = 0

	s := &Strategic{Catalog: cat}
	s.BindEnergyEnvironment(func() (float32, float32) { return 0, 0 })
	s.Init([]string{def.CanonicalKey})

	// +20 and no building term: a mobile builder initializes to exactly 20.
	if got := s.InitVectors[def.CanonicalKey]; got != 20 {
		t.Errorf("initialization weight = %d, want 20 [08 R-P0-05 §9]", got)
	}
	// The other-mix accumulator starts at 1, gains 30 while fewer than three
	// are owned, and the zero-count multiplier is four: (1+30)*4 = 124, which
	// the routine's clamp caps at 100. Without the term the same definition
	// yields 1*4 = 4, which is what the control below pins.
	if got := s.ClassVectors[def.CanonicalKey].C0; got != 100 {
		t.Errorf("other-mix coefficient = %d, want the clamped (1+30)*4 [08 R-P0-05 §5]", got)
	}
	plain := emptyMenuBuilderDef("empty-menu-non-builder")
	plain.Builder = false
	if _, cv := classVectorFor(t, plain); cv.C0 != 4 {
		t.Errorf("non-builder control coefficient = %d, want 4 = 1*4 [08 R-P0-05 §5]", cv.C0)
	}

	s.refreshCountsAndCenter(1, w)
	if s.BuildCapable != 1 {
		t.Errorf("build-capable count = %d, want 1 [08 R-AI-01 §16]", s.BuildCapable)
	}
}
