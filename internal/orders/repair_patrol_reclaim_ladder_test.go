package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Feature identities for the ladder fixture: the lattice west of the patrol
// carries metal-only features and the rest carries energy-only ones, so the two
// lists are disjoint and the ladder's choice is observable.
const (
	ladderMetalFeature  = uint16(1)
	ladderEnergyFeature = uint16(2)
)

// reclaimLadderFixture arms one repair patrol on a lattice whose samples all
// resolve, so each non-empty list holds several entries and its tournament
// really spends its three bounded picks (a one-entry list would make the calls
// and advance nothing, [01 §7.5]).
//
// Both stores sit at half their storage: metal at or above 20 % keeps the first
// (low-metal) arm of [04 R-ORD-01 §4]'s ladder shut, and energy at or above
// 20 % puts the visit in the second arm, which is the arm under test. A feature
// value of 50 then fits under storage and a value of 60 does not. A value of
// zero keeps that feature off its list entirely.
func reclaimLadderFixture(t *testing.T, metalValue, energyValue int32) (*units.Unit, *rng.Simulation) {
	t.Helper()
	def := &content.UnitDef{UnitDefID: 9, MaxDamage: 100, SightDistance: 96, CanReclamate: true, CanMove: true, BMCode: 1}
	actor := &units.Unit{Handle: 1, Owner: 0, Def: def, Alive: true, Health: def.MaxDamage}
	actor.Move.Mode, actor.Move.ModeMirror = 1, 1

	sim := rng.SimulationFromState(1)
	binding := &QueueBinding{
		SimRNG: &sim,
		Resources: func(uint8) (ResourceView, bool) {
			return ResourceView{Stock: [2]float32{50, 50}, Capacity: [2]float32{100, 100}}, true
		},
		World: &WorldQueryAdapter{
			LookupFeature: func(cx, _ int32) (FeatureView, bool) {
				f := FeatureView{Reclaimable: true, Autoreclaimable: true}
				if cx < 0 {
					f.ID, f.Metal = ladderMetalFeature, metalValue
				} else {
					f.ID, f.Energy = ladderEnergyFeature, energyValue
				}
				return f, true
			},
			TerrainHeight: func(numeric.Fixed, numeric.Fixed) (numeric.Fixed, bool) { return 0, true },
			SeaLevel:      func() uint8 { return 20 },
		},
	}
	BindQueue(actor, &Queue{binding: binding})
	return actor, &sim
}

// TestRepairPatrolReclaimLadderSecondArm locks the second arm of the reclaim
// ladder of [04 R-ORD-01 §4]. Reached with metal at or above 20 % of its
// storage and either no energy feature or energy at or above 20 %, the arm
// reclaims the metal feature when one exists and its value fits under metal
// storage; otherwise it HOLDS only when there is no energy feature, holds when
// the energy value would overflow energy storage, and otherwise reclaims the
// energy feature. A missing metal feature is never itself a reason to hold —
// the row this test exists for is "no metal feature, fitting energy feature",
// which used to hold.
//
// The draw column is the other half of the contract: both tournaments run
// before the ladder is consulted, so the values spent depend only on which
// lists are non-empty, never on the decision the ladder reaches (I4).
func TestRepairPatrolReclaimLadderSecondArm(t *testing.T) {
	const (
		absent = int32(0)
		fits   = int32(50) // 50 + 50 <= 100, the inclusive fit test
		tooBig = int32(60) // 50 + 60 > 100
	)
	// A fixed slice, not a map: the rows run in one order (I1).
	for _, row := range []struct {
		name      string
		metal     int32
		energy    int32
		wantID    uint16 // 0 = hold
		wantDraws uint64
	}{
		{"no feature at all holds", absent, absent, 0, 0},
		{"no metal feature reclaims the fitting energy feature", absent, fits, ladderEnergyFeature, 3},
		{"no metal feature and an overflowing energy feature holds", absent, tooBig, 0, 3},
		{"a fitting metal feature wins with no energy feature", fits, absent, ladderMetalFeature, 3},
		{"a fitting metal feature wins over a fitting energy feature", fits, fits, ladderMetalFeature, 6},
		{"a fitting metal feature wins over an overflowing one", fits, tooBig, ladderMetalFeature, 6},
		{"an overflowing metal feature with no energy feature holds", tooBig, absent, 0, 3},
		{"an overflowing metal feature yields to the energy feature", tooBig, fits, ladderEnergyFeature, 6},
		{"both values overflow their storage, so the visit holds", tooBig, tooBig, 0, 6},
	} {
		t.Run(row.name, func(t *testing.T) {
			actor, sim := reclaimLadderFixture(t, row.metal, row.energy)

			feature, ok := chooseReclaimFeature(actor, actor.Def.SightDistance)

			if ok != (row.wantID != 0) {
				t.Fatalf("ladder decided ok=%v, want %v [04 R-ORD-01 §4]", ok, row.wantID != 0)
			}
			if ok && feature.ID != row.wantID {
				t.Fatalf("ladder chose feature %d, want %d [04 R-ORD-01 §4]", feature.ID, row.wantID)
			}
			if draws := sim.Draws(); draws != row.wantDraws {
				t.Fatalf("the ladder's visit drew %d simulation values, want %d (three per non-empty list, spent before the decision) [01 §7.5][I4]", draws, row.wantDraws)
			}
		})
	}
}
