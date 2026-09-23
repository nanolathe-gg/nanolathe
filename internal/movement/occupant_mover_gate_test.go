package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// stubMovers answers the occupant-age gate's mover question for the handles it
// lists; everything else has no mover, which is what a building is.
type stubMovers map[pool.Handle]bool

func (m stubMovers) HasMover(h pool.Handle) bool { return m[h] }

// TestOccupantAgeGateBuildingBlocksAtEveryWatermark locks the corrected gate of
// [04 R-PATH-01 §14]: the occupancy-commit tick is a word on the occupant's
// MOVER, so the test is `mover == null || mover.commitTick < watermark`. A
// building has no mover and therefore hard-blocks unconditionally — from the
// first classification that finds it in the occupant word, whatever the
// watermark and whatever tick its own creation stamp wrote. A mobile occupant
// keeps the tick rule [R-DOC04-B] states.
//
// This corrects [R-DOC04-B]'s rider that a building's frozen commit tick is
// eventually passed by the watermark: the outcome (buildings hard-block) is
// unchanged, but it is immediate and does not wait for a request revision.
func TestOccupantAgeGateBuildingBlocksAtEveryWatermark(t *testing.T) {
	const mover = pool.Handle(7)
	const building = pool.Handle(9)

	tr := layerTerrain(16, 16, 20)
	grid := NewOccupancyGrid()
	l := NewClassLayer(kbotsSS2, tr, grid)
	l.movers = stubMovers{mover: true}
	grid.Stamp(Cell{X: 3, Z: 3}, 1, 1, int(mover))
	grid.Stamp(Cell{X: 9, Z: 9}, 1, 1, int(building))
	// Both stamped their commit tick at creation [04 R-PATH-01 §14].
	l.NoteCommit(mover, 5)
	l.NoteCommit(building, 5)

	// Watermark zero, the map-load stamp: the mover does not block, the
	// building already does.
	if got := l.classify(3, 3); got != LayerClear {
		t.Fatalf("watermark 0 mover occupant = %d, want clear", got)
	}
	if got := l.classify(9, 9); got != LayerBlocked {
		t.Fatalf("watermark 0 building occupant = %d, want blocked", got)
	}

	// Watermark past both ticks: the mover now blocks on age.
	l.watermark = 20
	if got := l.classify(3, 3); got != LayerBlocked {
		t.Fatalf("stale mover occupant = %d, want blocked", got)
	}
	if got := l.classify(9, 9); got != LayerBlocked {
		t.Fatalf("armed-watermark building occupant = %d, want blocked", got)
	}

	// A fresh commit tick clears the mover; it cannot clear the building,
	// because the building never reaches the tick compare at all.
	l.NoteCommit(mover, 25)
	l.NoteCommit(building, 25)
	if got := l.classify(3, 3); got != LayerClear {
		t.Fatalf("fresh mover occupant = %d, want clear", got)
	}
	if got := l.classify(9, 9); got != LayerBlocked {
		t.Fatalf("building with a fresh tick = %d, want blocked — a building has no mover", got)
	}
}

// TestCreationStampWritesTheCommitTick locks the second half of
// [04 R-PATH-01 §14]: unit creation is one of the footprint stamp's callers —
// the creator stamps the new unit's footprint after the initializer returns —
// and the stamp writes the mover's commit tick as its first action. EnsureUnit
// is Nanolathe's creation stamp, so it must note the tick in scope.
func TestCreationStampWritesTheCommitTick(t *testing.T) {
	terrain := syntheticTerrainFlat()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(8)
	sys.BindWorld(w)
	layer := sys.ensureLayerRegistry().For("", wiringProfile)

	sys.BeginTick(41)
	at := world.CellToWorld(6)
	h, err := w.Create(wiringDef(), 0, at, terrain.HeightAt(at, at), at)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, ok := layer.CommitTick(h); ok {
		t.Fatalf("allocation alone noted a commit tick %d; the stamp is what writes it", got)
	}
	sys.EnsureUnit(w.Unit(h))
	if got, ok := layer.CommitTick(h); !ok || got != 41 {
		t.Fatalf("creation-stamp commit tick = %d/%v, want 41/true", got, ok)
	}

	// A building's creation stamp writes the tick too; it is simply never
	// what makes the building block [04 R-PATH-01 §14].
	sys.BeginTick(42)
	def := &content.UnitDef{UnitName: "stationary", MaxDamage: 100, BMCode: 0, FootprintX: 2, FootprintZ: 2}
	at2 := world.CellToWorld(12)
	hb, err := w.Create(def, 0, at2, terrain.HeightAt(at2, at2), at2)
	if err != nil {
		t.Fatalf("create building: %v", err)
	}
	sys.EnsureUnit(w.Unit(hb))
	if got, ok := layer.CommitTick(hb); !ok || got != 42 {
		t.Fatalf("building creation-stamp commit tick = %d/%v, want 42/true", got, ok)
	}
	if sys.HasMover(hb) {
		t.Fatal("a building must report no mover [04 R-COLL-01 §1]")
	}
	if !sys.HasMover(h) {
		t.Fatal("a mobile unit must report a mover")
	}
}
