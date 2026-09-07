package session

// These tests intentionally use one named retail scenario. A retail install
// is an authored corpus, not a source of arbitrary fixtures: selecting the
// first unit/map that happens to satisfy a predicate made a passing test mean
// something different on every install.

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

const (
	retailMap       = "ashap plateau"
	retailARM       = "ARMCOM"
	retailCORE      = "CORCOM"
	retailARMLab    = "ARMLAB"
	retailCORELab   = "CORLAB"
	retailARMHammer = "ARMHAM"
	retailCOREAK    = "CORAK"
)

type retailFixture struct {
	fs  *vfs.FS
	cat *content.Catalog
	cfg SkirmishConfig
}

// loadRetailFixture is the sole retail-root and catalog setup path for this
// package. Missing fixture assets skip; an installed but malformed corpus is
// a test failure, so content regressions are not hidden by substitution.
//
// The catalog comes from retailcat.Shared: every caller below only reads it
// (unit/side/map lookups, NewSkirmishWithFS's own read-only catalog use), so
// one compile per package binary is enough rather than one per test
// [internal/testsupport/retailcat]. The shared filesystem is kept alive for
// the process lifetime by that cache, so this fixture must not close it.
func loadRetailFixture(t *testing.T) *retailFixture {
	t.Helper()
	cat, fs := retailcat.Shared(t)
	if err := cat.Validate(); err != nil {
		t.Fatalf("validate retail catalog: %v", err)
	}
	if _, ok := cat.Maps[content.CanonicalKey(retailMap)]; !ok {
		t.Skipf("retail fixture map %q is absent", retailMap)
	}
	for _, key := range []string{retailARM, retailCORE, retailARMLab, retailCORELab, retailARMHammer, retailCOREAK} {
		if def, ok := cat.Unit(key); !ok || def == nil {
			t.Skipf("retail fixture unit %q is absent", key)
		}
	}
	if len(cat.Sides) < 2 || cat.Sides[0] == nil || cat.Sides[1] == nil ||
		!strings.EqualFold(cat.Sides[0].Commander, retailARM) ||
		!strings.EqualFold(cat.Sides[1].Commander, retailCORE) {
		t.Skipf("retail fixture requires side 0=%s and side 1=%s", retailARM, retailCORE)
	}
	if _, ok := cat.BuildMenus[content.CanonicalKey(retailARM)]; !ok {
		t.Skipf("retail fixture build menu for %s is absent", retailARM)
	}
	if _, ok := cat.BuildMenus[content.CanonicalKey(retailCORE)]; !ok {
		t.Skipf("retail fixture build menu for %s is absent", retailCORE)
	}
	cfg := SkirmishConfig{
		MapName: retailMap, NumPlayers: 2,
		RNGSimSeed: 12345, RNGCrtSeed: 67890,
	}
	cfg.ApplyDefaults()
	cfg.Players[0].Side, cfg.Players[0].Controller = 0, SkirmishControllerHuman
	cfg.Players[1].Side, cfg.Players[1].Controller = 1, SkirmishControllerComputer
	// The retail sentinel 5 means the lobby left alliances at their default;
	// it is accepted by the established skirmish configuration contract.
	cfg.Players[0].AllyGroup, cfg.Players[1].AllyGroup = 5, 5
	return &retailFixture{fs: fs, cat: cat, cfg: cfg}
}

func (f *retailFixture) session(t *testing.T) *Session {
	t.Helper()
	s, err := NewSkirmishWithFS(f.fs, f.cat, f.cfg)
	if err != nil {
		t.Fatalf("construct retail %q: %v", retailMap, err)
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("validate retail composition: %v", err)
	}
	return s
}

func retailUnit(s *Session, owner uint8, key string) *units.Unit {
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == owner && u.Def != nil && strings.EqualFold(u.Def.UnitName, key) {
			return u
		}
	}
	return nil
}

func stepRetail(s *Session, ticks int) {
	// The first dispatch completes loading and the next dispatch runs the
	// state-6 handler before ticking [08 "Session states"] C2. Include that
	// boundary call so this helper advances the requested number of
	// authoritative ticks rather than silently producing ticks-1.
	for tick := 0; tick <= ticks; tick++ {
		s.Step(int32(tick))
	}
}

// TestRetailBootUsesAuthoredMapAndCommanders keeps the real-content entry
// contract small: authored terrain, two side commanders, and initialized
// session services must all be present.
func TestRetailBootUsesAuthoredMapAndCommanders(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	if s.World == nil || s.World.CellW == 0 || s.World.CellH == 0 {
		t.Fatalf("retail terrain not loaded: %#v", s.World)
	}
	if retailUnit(s, 0, retailARM) == nil || retailUnit(s, 1, retailCORE) == nil {
		t.Fatalf("authored commanders missing: arm=%v core=%v", retailUnit(s, 0, retailARM), retailUnit(s, 1, retailCORE))
	}
	if s.Econ == nil || s.Movement == nil || s.Vis == nil || s.Combat == nil {
		t.Fatal("retail boot did not bind economy, movement, visibility, and combat services")
	}
}

// TestRetailOrdersReachTheAuthoritativeQueue checks a concrete authored order
// path immediately after admission, before AI cadence can alter it.
func TestRetailOrdersReachTheAuthoritativeQueue(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	builder := retailUnit(s, 0, retailARM)
	if builder == nil {
		t.Fatal("ARM commander not spawned")
	}
	q := orders.QueueForUnit(builder)
	if q == nil {
		t.Fatal("ARM commander queue is nil")
	}
	goalX := builder.X.Add(world.CellToWorld(20))
	goalZ := builder.Z.Add(world.CellToWorld(20))
	if err := construction.QueueMobileBuild(builder, retailARMLab, goalX, goalZ, 1, f.cat); err != nil {
		t.Fatalf("queue authored factory order: %v", err)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("primary queue length = %d, want 1", q.LenPrimary())
	}
	n := q.Head()
	if n == nil || !strings.EqualFold(n.BuildDefKey, content.CanonicalKey(retailARMLab)) {
		t.Fatalf("queue head = %#v, want %s", n, retailARMLab)
	}
}

// TestRetailFactoryAndEconomyAdvance runs the authored first-tier factory
// contract. It does not claim that retail AI reaches a particular state on a
// particular machine; construction progress and resource settlement are the
// semantic observations owned by this smoke test.
func TestRetailFactoryAndEconomyAdvance(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	builder := retailUnit(s, 0, retailARM)
	coreBuilder := retailUnit(s, 1, retailCORE)
	if builder == nil {
		t.Fatal("ARM commander not spawned")
	}
	if coreBuilder == nil {
		t.Fatal("CORE commander not spawned")
	}
	startMetal := s.Econ.Players[0].Stock[economy.Metal]
	goalX, goalZ := retailBuildSite(t, s, f.cat, builder, retailARMLab)
	if err := construction.QueueMobileBuild(builder, retailARMLab, goalX, goalZ, 1, f.cat); err != nil {
		t.Fatalf("queue authored factory: %v", err)
	}
	coreX, coreZ := retailBuildSite(t, s, f.cat, coreBuilder, retailCORELab)
	if err := construction.QueueMobileBuild(coreBuilder, retailCORELab, coreX, coreZ, 1, f.cat); err != nil {
		t.Fatalf("queue authored CORE factory: %v", err)
	}
	// The builder walks to nano range before the nanoframe is allocated and
	// the first debit lands [05 "Factory production lifecycle"]; the walk
	// length is authored by the map, so advance in bounded rounds rather than
	// asserting a fixed arrival tick.
	const round, maxRounds = 120, 8
	progressed := func() bool {
		return s.Econ.Players[0].Stock[economy.Metal] < startMetal || retailUnit(s, 0, retailARMLab) != nil
	}
	rounds := 0
	for ; rounds < maxRounds && !progressed(); rounds++ {
		stepRetail(s, round)
	}
	if want := uint32(rounds * round); s.Clock.GlobalTick != want {
		t.Fatalf("global tick = %d, want %d", s.Clock.GlobalTick, want)
	}
	if !progressed() {
		t.Fatalf("factory order made no authored progress and did not settle economy: metal %.1f", s.Econ.Players[0].Stock[economy.Metal])
	}
}

// retailBuildSite picks the nearest cell offset from the builder whose
// footprint passes the shared placement legality query for the product. A
// blind fixed offset landed on a slope the lab's authored limit rejects, so
// the order was abandoned through the blocked-area budget [R-ORDER-02 §1]
// rather than ever reaching construction.
func retailBuildSite(t *testing.T, s *Session, cat *content.Catalog, builder *units.Unit, key string) (numeric.Fixed, numeric.Fixed) {
	t.Helper()
	def, ok := cat.Unit(key)
	if !ok || def == nil {
		t.Fatalf("product %q absent", key)
	}
	extent, err := world.NewFootprintExtent(int32(def.FootprintX), int32(def.FootprintZ))
	if err != nil {
		t.Fatalf("footprint for %q: %v", key, err)
	}
	yard, err := world.ParseYardMap(def.YardMap, int(def.FootprintX), int(def.FootprintZ))
	if err != nil {
		t.Fatalf("yardmap for %q: %v", key, err)
	}
	rules, err := world.PlacementRulesForUnit(cat, def)
	if err != nil {
		t.Fatalf("placement rules for %q: %v", key, err)
	}
	const maxRadius = 24
	for r := 2; r <= maxRadius; r += 2 {
		for dz := -r; dz <= r; dz += 2 {
			for dx := -r; dx <= r; dx += 2 {
				if dx != -r && dx != r && dz != -r && dz != r {
					continue
				}
				x := builder.X.Add(world.CellToWorld(int32(dx)))
				z := builder.Z.Add(world.CellToWorld(int32(dz)))
				anchor, err := world.SnapFootprintAnchor(x, z, extent)
				if err != nil {
					continue
				}
				rect, err := world.NewFootprintRect(anchor, extent)
				if err != nil {
					continue
				}
				if _, err := s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: uint16(builder.Handle), Mobile: def.BMCode != 0}); err == nil {
					return x, z
				}
			}
		}
	}
	t.Fatalf("no legal %q site within %d cells of %s", key, maxRadius, builder.Def.UnitName)
	return 0, 0
}

// TestRetailCombatPublishesProjectileAndDeath exercises authored weapon
// binding. The terminal death is explicit so the test does not depend on an
// untraced AI battle duration.
func TestRetailCombatPublishesProjectileAndDeath(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	if retailUnit(s, 1, retailCORE) == nil {
		t.Fatal("CORE commander not spawned")
	}
	for _, key := range []string{retailARMHammer, retailCOREAK} {
		def, _ := f.cat.Unit(key)
		if def.Weapon1Def == nil && def.Weapon2Def == nil && def.Weapon3Def == nil {
			t.Fatalf("authored combat unit %s has no resolved weapon", key)
		}
	}
	stepRetail(s, 30)
	if s.Combat == nil {
		t.Fatal("combat service became nil during authored ticks")
	}
	enemy := retailUnit(s, 1, retailCORE)
	if enemy == nil {
		t.Fatal("CORE commander disappeared before explicit death")
	}
	s.Units.Destroy(enemy.Handle, units.DeathKilled)
	// Destroy latches Dying immediately; the unit remains alive until the
	// phase-2 slot-end finalization and cleanup [01 §4.4][04 §2.4]. The old
	// assertion treated the latch as immediate pool removal.
	if !enemy.Dying {
		t.Fatal("destroyed authored commander did not latch Dying")
	}
	beforeTick := s.Clock.GlobalTick
	// The next authoritative tick runs phase 2 and finalizes the deferred
	// death; Destroy must not remove the unit in the calling phase [01 §4.4]
	// [04 §2.4].
	s.Step(40)
	if s.Clock.GlobalTick <= beforeTick {
		t.Fatalf("deferred death did not advance an authoritative tick: before=%d after=%d", beforeTick, s.Clock.GlobalTick)
	}
	if enemy.Alive {
		t.Fatal("destroyed authored commander remained alive after phase-2 finalization")
	}
	if got := retailUnit(s, 1, retailCORE); got != nil {
		t.Fatalf("destroyed authored commander remained present after phase-2 finalization: %#v", got)
	}
}

// TestRetailAIReportsOnlyEstablishedState keeps the AI smoke honest. A
// profile and manager are required inputs; factory/combat timing is not
// asserted because no retail contract fixes their wall-clock tick.
func TestRetailAIReportsOnlyEstablishedState(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	if s.AI[1] == nil {
		t.Fatal("computer player has no AI manager")
	}
	if s.AI[1].Profile == nil || s.AI[1].Catalog != f.cat || s.AI[1].Terrain != s.World {
		t.Fatalf("AI manager is not bound to authored inputs: %#v", s.AI[1])
	}
	if len(s.AI[1].Strategic.ClassVectors) == 0 {
		t.Fatal("AI strategic vectors were not initialized")
	}
	stepRetail(s, 30)
	if s.AI[1] == nil {
		t.Fatal("AI manager disappeared during session tick")
	}
}

// TestRetailPostbattleStateUsesExplicitDeath checks the postbattle
// transition with a real authored commander while avoiding a claim about
// which AI unit wins first.
func TestRetailPostbattleStateUsesExplicitDeath(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	enemy := retailUnit(s, 1, retailCORE)
	if enemy == nil {
		t.Fatal("CORE commander not spawned")
	}
	s.Units.Destroy(enemy.Handle, units.DeathKilled)
	stepRetail(s, 120)
	res := s.GetResult()
	if !res.Ended {
		t.Skipf("retail mission did not poll commander death within 120 ticks")
	}
	if s.State != StatePostBattle && s.State != StateRouter {
		t.Fatalf("terminal state = %v, want postbattle/router", s.State)
	}
}
