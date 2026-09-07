package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The stamp's SECOND optional pointer [05 "Feature instance and terrain cell"]:
// the corpse placement passes the dying unit's bank/heading/pitch and the
// instance stores it verbatim, while every other placement passes none and
// stores three zeros. The transplant of [05 R-WORK-01 §7] reads what this
// stores, so the two halves have to agree on both arms.
func TestOnlyTheCorpsePlacementStoresAnOrientation(t *testing.T) {
	terrain := newTestTerrainP1(8, 8)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)

	wreck := defP1("orientwreck", 1, 1, "orientwreck.3do", "")
	fell := Orientation{Bank: 0x1234, Heading: 0xC000, Pitch: 0x0456}
	corpse := svc.PlaceCorpse([3]numeric.Fixed{
		world.CellToWorld(2),
		numeric.Fixed(11 * 65536),
		world.CellToWorld(3),
	}, fell, wreck, false)
	if corpse == nil {
		t.Fatal("corpse refused")
	}
	if corpse.Orientation != fell {
		t.Fatalf("corpse orientation %+v, want the dying unit's triple %+v", corpse.Orientation, fell)
	}

	// A map-authored / mission / successor placement supplies no triple.
	tree := defP1("orienttree", 1, 1, "", "trees")
	plain := svc.PlaceAt(5, 5, tree)
	if plain == nil {
		t.Fatal("plain placement refused")
	}
	if plain.Orientation != (Orientation{}) {
		t.Fatalf("non-corpse placement orientation %+v, want three zeros", plain.Orientation)
	}
}

// The successor hop does NOT carry the triple: a replacement stores zeros like
// every other non-corpse placement [05 "Feature instance and terrain cell"].
func TestSuccessorReplacementDropsTheWrecksOrientation(t *testing.T) {
	terrain := newTestTerrainP1(8, 8)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)

	successor := defP1("orientheap", 1, 1, "orientheap.3do", "")
	wreck := defP1("orientwreck2", 1, 1, "orientwreck2.3do", "")
	wreck.FeatureDeadDef = successor

	fell := Orientation{Bank: 1, Heading: 2, Pitch: 3}
	corpse := svc.PlaceCorpse([3]numeric.Fixed{
		world.CellToWorld(4),
		numeric.Fixed(11 * 65536),
		world.CellToWorld(4),
	}, fell, wreck, true)
	if corpse == nil || corpse.Orientation != fell {
		t.Fatalf("corpse orientation %+v, want %+v", corpse, fell)
	}
	svc.RemoveFeatureAt(4, 4, CauseDead)
	heap := svc.InstanceAt(4, 4)
	if heap == nil || heap.Def == nil || heap.Def.CanonicalKey != successor.CanonicalKey {
		t.Fatalf("successor not stamped: %+v", heap)
	}
	if heap.Orientation != (Orientation{}) {
		t.Fatalf("successor orientation %+v, want three zeros — the triple must not cross the hop", heap.Orientation)
	}
}
