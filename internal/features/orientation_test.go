package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The stamp's SECOND optional pointer [05 "Feature instance and terrain cell"]:
// the corpse placement passes the dying unit's bank/heading/pitch and the
// instance stores it verbatim, while a bare placement passes none and
// stores three zeros. The transplant of [05 R-WORK-01 §7] reads what this
// stores, so the two halves have to agree on both arms.
func TestCorpseAndBarePlacementOrientation(t *testing.T) {
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

	// A map-authored / mission placement supplies no triple.
	tree := defP1("orienttree", 1, 1, "", "trees")
	plain := svc.PlaceAt(5, 5, tree)
	if plain == nil {
		t.Fatal("plain placement refused")
	}
	if plain.Orientation != (Orientation{}) {
		t.Fatalf("non-corpse placement orientation %+v, want three zeros", plain.Orientation)
	}
}

// Ordinary replacement passes only the attached predecessor's transform,
// while its accumulator and motion state are initialized by the stamp
// [05 R-FEAT-01 §5 step 6]. EC-G2 retains the zero-velocity boundary.
func TestReplacementRetainsAttachedTransform(t *testing.T) {
	for _, reclaim := range []bool{false, true} {
		t.Run(map[bool]string{false: "damage", true: "reclaim"}[reclaim], func(t *testing.T) {
			terrain := newTestTerrainP1(8, 8)
			svc := NewService(terrain, nil, nil, nil)
			successor := defP1("heap", 1, 1, "heap.3do", "")
			wreck := defP1("wreck", 1, 1, "wreck.3do", "")
			wreck.Damage, wreck.Reclaimable = 10, true
			wreck.FeatureDeadDef, wreck.FeatureReclamateDef = successor, successor
			pos := [3]numeric.Fixed{world.CellToWorld(4) + 123, numeric.Fixed(11*65536 + 456), world.CellToWorld(4) + 789}
			orient := Orientation{Bank: 1, Heading: 2, Pitch: 3}
			corpse := svc.PlaceCorpse(pos, orient, wreck, true)
			if corpse == nil {
				t.Fatal("corpse refused")
			}
			corpse.Vy = 12
			if reclaim {
				if _, _, ok := svc.ReclaimAt(4, 4); !ok {
					t.Fatal("reclaim refused")
				}
			} else {
				svc.Ignite(4, 4, 0, 10)
			}
			heap := svc.InstanceAt(4, 4)
			if heap == nil || heap.Def != successor {
				t.Fatalf("successor: %+v", heap)
			}
			if got := [3]numeric.Fixed{heap.X, heap.Y, heap.Z}; got != pos || heap.Orientation != orient {
				t.Fatalf("transform = %v %+v, want %v %+v", got, heap.Orientation, pos, orient)
			}
			if heap.DamageAccumulator != 0 || heap.Vy != 0 {
				t.Fatalf("unrelated state inherited: %+v", heap)
			}
		})
	}
}

// A resting sprite's lookup record supplies no transform, and burn completion
// explicitly passes neither transform [05 R-FEAT-01 §5, §10 pass 3c].
func TestReplacementNullTransformControls(t *testing.T) {
	for _, burn := range []bool{false, true} {
		terrain := newTestTerrainP1(8, 8)
		svc := NewService(terrain, nil, nil, nil)
		successor := defP1("heap", 1, 1, "heap.3do", "")
		source := defP1("tree", 1, 1, "", "trees")
		source.FeatureDeadDef, source.FeatureBurntDef = successor, successor
		inst := svc.PlaceAt(4, 4, source)
		inst.X, inst.Y, inst.Z = 1, 2, 3
		inst.Orientation = Orientation{Bank: 1, Heading: 2, Pitch: 3}
		if burn {
			startBurning(svc, inst, []int32{1}, 100)
			svc.TickLifecycle(1)
		} else {
			svc.RemoveFeatureAt(4, 4, CauseDead)
		}
		heap := svc.InstanceAt(4, 4)
		if heap == nil || heap.Def != successor {
			t.Fatalf("burn=%t successor: %+v", burn, heap)
		}
		wantX, wantZ := footprintCentreWorld(4, 1), footprintCentreWorld(4, 1)
		if heap.X != wantX || heap.Z != wantZ || heap.Y != terrain.HeightAt(wantX, wantZ) || heap.Orientation != (Orientation{}) {
			t.Fatalf("burn=%t retained lookup transform: %+v", burn, heap)
		}
	}
}
