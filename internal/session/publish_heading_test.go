package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestPublishedHeadingIsTheUnitRecordsOwnWord locks the publication against
// [04 §8.1][R-MOV-01 §4]: there is exactly one heading word — the unit's own,
// "the one the steering step just turned" — and every mover surface beside it
// is a copy its own step maintains (I13).
//
// The publication used to prefer the ground steer record's copy. Every unit
// gets a steer record at EnsureUnit, aircraft included, and the air mover
// writes the unit record and the collision record but never the steer — so an
// aircraft published the heading its steer was seeded with at creation and
// never turned into the direction it was flying.
//
// The fixture is that divergence directly: the record has turned, the ground
// steer copy has not.
func TestPublishedHeadingIsTheUnitRecordsOwnWord(t *testing.T) {
	const stale, turned = uint16(0x0000), uint16(0x4000)

	unitsPool := newSessionFixtureWorld(4, nil)
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "flier"},
		MaxDamage:        1, CanFly: true, BMCode: 1, MaxVelocity: 65536, TurnRate: 500,
	}
	h, err := unitsPool.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := unitsPool.Unit(h)

	terrain := &world.Terrain{CellW: 16, CellH: 16, Plot: make([]world.PlotCell, 16*16)}
	sys := movement.NewSystem(terrain, movement.Template(), movement.NewOccupancyGrid())
	sys.BindWorld(unitsPool)
	sys.EnsureUnit(u)
	if st := sys.Steers[h]; st == nil {
		t.Fatal("fixture is not exercising the defect: an aircraft has no steer record to go stale")
	} else {
		st.Heading = stale
	}
	u.Move.Heading = turned

	s := &Session{Snapshot: frame.NewBuffer(), Units: unitsPool, Movement: sys, LocalOwner: 0}
	s.publishSnapshot(1)

	got := s.Snapshot.Current()
	if got == nil || len(got.Units) != 1 {
		t.Fatalf("publication produced %v", got)
	}
	if got.Units[0].Heading != turned {
		t.Fatalf("published heading = %d, want the unit record's own %d (the stale steer copy is %d)",
			got.Units[0].Heading, turned, stale)
	}
}
