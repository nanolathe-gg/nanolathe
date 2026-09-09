package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// newEyeballSession builds a bound session with player 0 local (human) and
// player 1 remote, sprite-mask (Circular) LOS, and the authored shape fixture
// so the circular raster publishes real coverage.
func newEyeballSession(t *testing.T) *Session {
	t.Helper()
	cat := minimalCatalogForStrict()
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 0}
	terrain.Plot = make([]world.PlotCell, 64*64)
	_ = terrain.ApplySchema(nil, 0)
	s := &Session{Catalog: cat, World: terrain, Mission: syntheticMission()}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 2
	s.Econ.SeedDeadlines(0)
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}
	s.Vis.SetShapes(fixtureShapesForTest())
	s.Vis.SetMode(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled) // Circular
	s.RegisterAll()
	return s
}

func spawnEyeballVictim(t *testing.T, s *Session, owner uint8, tile int32) *units.Unit {
	t.Helper()
	def := &content.UnitDef{UnitName: fmt.Sprintf("victim%d", owner), MaxDamage: 100, SightDistance: 160, FootprintX: 1, FootprintZ: 1, Script: fixtureCOBProgram()}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	h, err := s.Units.Create(def, owner, numeric.Fixed(tile*32*65536), numeric.Fixed(30*65536), numeric.Fixed(tile*32*65536))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := s.Units.Unit(h)
	publishOne(s, u)
	return u
}

// kill runs the victim through the central death handler: mark dying, then
// finalize, which fires the session's OnDeath hook exactly once.
func kill(s *Session, u *units.Unit) {
	h := pool.Handle(u.Handle)
	s.Units.Destroy(h, units.DeathKilled)
	s.Units.FinalizeDeath(h, s.Clock.GlobalTick)
}

func coverageAt(s *Session, owner visibility.PlayerID, tile int32) uint8 {
	w, _ := s.Vis.GridDimensions()
	return s.Vis.ByteGrid(owner)[tile*w+tile]
}

// TestEyeballProducerGates locks [08 R-SESS-01 §3]: the central death handler
// appends one record for a locally owned victim under Circular LOS with the
// victim's sightdistance and expiry tick+60; a remote-owned victim, or any
// victim under Permanent LOS, appends nothing.
func TestEyeballProducerGates(t *testing.T) {
	s := newEyeballSession(t)
	s.Clock.GlobalTick = 100

	remote := spawnEyeballVictim(t, s, 1, 20)
	kill(s, remote)
	if got := s.EyeballCount(); got != 0 {
		t.Fatalf("remote-owned victim appended %d records, want 0", got)
	}

	local := spawnEyeballVictim(t, s, 0, 10)
	kill(s, local)
	if got := s.EyeballCount(); got != 1 {
		t.Fatalf("local victim appended %d records, want 1", got)
	}
	rec := s.postLoop.eyeballs.records[0]
	if rec.expiry != 160 || rec.sightDistance != 160 || rec.owner != 0 {
		t.Fatalf("record=%+v, want expiry 160 (tick+60), sightdistance 160, owner 0", rec)
	}

	// Permanent: mode bit 1 clear appends nothing.
	s.Vis.SetMode(visibility.ModeHistoryEnabled)
	perm := spawnEyeballVictim(t, s, 0, 12)
	kill(s, perm)
	if got := s.EyeballCount(); got != 1 {
		t.Fatalf("Permanent-mode death appended: count=%d, want 1", got)
	}
}

// TestEyeballCapacityDropsSilently locks the twenty-record block: the
// twenty-first append is dropped [01 R-PLAT-02 §5].
func TestEyeballCapacityDropsSilently(t *testing.T) {
	s := newEyeballSession(t)
	for i := 0; i < eyeballCapacity+1; i++ {
		s.Clock.GlobalTick = uint32(i + 1)
		kill(s, spawnEyeballVictim(t, s, 0, 10))
	}
	if got := s.EyeballCount(); got != eyeballCapacity {
		t.Fatalf("count=%d, want %d after %d deaths", got, eyeballCapacity, eyeballCapacity+1)
	}
	if last := s.postLoop.eyeballs.records[eyeballCapacity-1]; last.expiry != uint32(eyeballCapacity)+eyeballLifetimeTicks {
		t.Fatalf("last kept record expiry=%d; the 21st death must not displace an earlier record", last.expiry)
	}
}

// TestEyeballExpiryStrictAndStable locks [03 R-COMP-02 §2]: the tail pass
// removes only records with expiry strictly below the tick and compacts the
// survivors in their original order.
func TestEyeballExpiryStrictAndStable(t *testing.T) {
	s := newEyeballSession(t)
	var l eyeballList
	for _, e := range []uint32{12, 10, 11, 10, 12} {
		l.records = append(l.records, eyeballRecord{expiry: e})
	}
	l.expire(s.Vis, 10)
	if len(l.records) != 5 {
		t.Fatalf("expiry == tick was removed: survivors=%d, want 5", len(l.records))
	}
	l.expire(s.Vis, 11)
	got := make([]uint32, 0, len(l.records))
	for _, r := range l.records {
		got = append(got, r.expiry)
	}
	if want := []uint32{12, 11, 12}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("survivors=%v, want stable order %v", got, want)
	}
}

// TestEyeballCoverageLingersSixtyTicks locks the observable contract: the
// victim's sight coverage stays published through its expiry tick and is
// withdrawn by the executor tail on the tick after [03 R-COMP-02 §2].
func TestEyeballCoverageLingersSixtyTicks(t *testing.T) {
	s := newEyeballSession(t)
	s.Clock.GlobalTick = 100
	const tile = 10
	// Relationship, not census: the fixture may already cover the tile, so
	// assert against the pre-spawn refcount.
	base := coverageAt(s, 0, tile)
	victim := spawnEyeballVictim(t, s, 0, tile)
	if coverageAt(s, 0, tile) != base+1 {
		t.Fatal("fixture: live unit publishes no coverage at its own tile")
	}
	kill(s, victim)
	if coverageAt(s, 0, tile) != base+1 {
		t.Fatal("coverage withdrawn at death; the eyeball must keep it published")
	}
	for tick := uint32(101); tick <= 160; tick++ {
		s.Clock.GlobalTick = tick
		s.runRetailPostLoopTail(tick)
		if coverageAt(s, 0, tile) != base+1 {
			t.Fatalf("coverage withdrawn at tick %d, want it held through expiry tick 160", tick)
		}
	}
	s.Clock.GlobalTick = 161
	s.runRetailPostLoopTail(161)
	if got := coverageAt(s, 0, tile); got != base {
		t.Fatalf("coverage=%d after expiry, want %d", got, base)
	}
	if got := s.EyeballCount(); got != 0 {
		t.Fatalf("count=%d after expiry, want 0", got)
	}
}

// TestEyeballUsesObserverCellDispatch locks that the eyeball producer projects
// through the same mode-dispatched observer path every other publisher uses
// [08 R-SESS-01 §3], not the terrain-ray branch unconditionally. The victim is
// placed at the review's known-divergent position (X=320 Y=64 Z=384, model top
// 64: ray branch row 10, sprite branch row 11, [03 R-VIS-01 §2]); the session
// runs Circular (sprite-mask) LOS, so a correct record lands on row 11 and a
// record still using the ray shear would land on row 10.
func TestEyeballUsesObserverCellDispatch(t *testing.T) {
	s := newEyeballSession(t)
	s.Clock.GlobalTick = 100

	def := &content.UnitDef{UnitName: "eyeballdivergent", MaxDamage: 100, SightDistance: 160, FootprintX: 1, FootprintZ: 1, Script: fixtureCOBProgram(), ModelTop: 64}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	h, err := s.Units.Create(def, 0, worldUnits(320), worldUnits(64), worldUnits(384))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := s.Units.Unit(h)
	publishOne(s, u)
	kill(s, u)

	if got := s.EyeballCount(); got != 1 {
		t.Fatalf("count=%d, want 1", got)
	}
	rec := s.postLoop.eyeballs.records[0]
	if rec.cx != 10 || rec.cz != 11 {
		t.Fatalf("eyeball cell=(%d,%d), want (10,11): the sprite-mask projection did not run", rec.cx, rec.cz)
	}
	if rayX, rayZ := observerTile(u, rec.emitter); rayX == rec.cx && rayZ == rec.cz {
		t.Fatalf("eyeball cell matches the terrain-ray branch (%d,%d): dispatch collapsed to one branch", rayX, rayZ)
	}
}
