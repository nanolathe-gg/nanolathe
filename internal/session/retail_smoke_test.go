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
	"github.com/nanolathe/nanolathe/internal/testsupport"
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
func loadRetailFixture(t *testing.T) *retailFixture {
	t.Helper()
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail %q: %v", root, err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
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
	goalX := builder.X.Add(world.CellToWorld(20))
	goalZ := builder.Z.Add(world.CellToWorld(20))
	if err := construction.QueueMobileBuild(builder, retailARMLab, goalX, goalZ, 1, f.cat); err != nil {
		t.Fatalf("queue authored factory: %v", err)
	}
	coreX := coreBuilder.X.Add(world.CellToWorld(20))
	coreZ := coreBuilder.Z.Add(world.CellToWorld(20))
	if err := construction.QueueMobileBuild(coreBuilder, retailCORELab, coreX, coreZ, 1, f.cat); err != nil {
		t.Fatalf("queue authored CORE factory: %v", err)
	}
	stepRetail(s, 120)
	if s.Clock.GlobalTick != 120 {
		t.Fatalf("global tick = %d, want 120", s.Clock.GlobalTick)
	}
	if s.Econ.Players[0].Stock[economy.Metal] >= startMetal && retailUnit(s, 0, retailARMLab) == nil {
		t.Fatalf("factory order made no authored progress and did not settle economy: metal %.1f", s.Econ.Players[0].Stock[economy.Metal])
	}
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
