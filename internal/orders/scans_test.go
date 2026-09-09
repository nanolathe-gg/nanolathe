package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestPatrolScansKeepSlotOrderAndDrawOnlyAfterGates(t *testing.T) {
	def := &content.UnitDef{UnitDefID: 9, MaxDamage: 100, SightDistance: 300}
	actor := &units.Unit{Handle: 1, Owner: 0, Def: def, Alive: true, X: numeric.Fixed(0), Z: numeric.Fixed(0)}
	friendly := &units.Unit{Handle: 2, Owner: 0, Def: def, Alive: true, X: numeric.Fixed(20 << 16), Z: numeric.Fixed(0), Health: 50}
	hostile := &units.Unit{Handle: 3, Owner: 1, Def: def, Alive: true, X: numeric.Fixed(40 << 16), Z: numeric.Fixed(0), Health: 50}
	wrongDef := &units.Unit{Handle: 4, Owner: 1, Def: &content.UnitDef{UnitDefID: 10, MaxDamage: 100}, Alive: true, X: numeric.Fixed(60 << 16), Health: 50}
	dead := &units.Unit{Handle: 5, Owner: 1, Def: def, Alive: false, X: numeric.Fixed(80 << 16), Health: 50}
	friendly.Move.Mode, hostile.Move.Mode, wrongDef.Move.Mode, dead.Move.Mode = 1, 1, 1, 1
	order := []pool.Handle{actor.Handle, friendly.Handle, hostile.Handle, wrongDef.Handle, dead.Handle}
	seen := make([]pool.Handle, 0, len(order))
	sim := rng.SimulationFromState(1)
	q := &Queue{binding: &QueueBinding{
		SimRNG:    simPtr(&sim),
		Hostility: func(_, candidate *units.Unit) bool { return candidate.Owner == 1 },
		World: &WorldQueryAdapter{ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			for _, h := range order {
				seen = append(seen, h)
				var candidate *units.Unit
				switch h {
				case actor.Handle:
					candidate = actor
				case friendly.Handle:
					candidate = friendly
				case hostile.Handle:
					candidate = hostile
				case wrongDef.Handle:
					candidate = wrongDef
				case dead.Handle:
					candidate = dead
				}
				// The binding's enumerator stops when a visitor returns TRUE
				// (QueueBinding.ForEachUnit, and the session adapter that
				// composes it): this fake must answer the same question the
				// production one does, or a scan that walks the whole pool in
				// the test truncates to its first slot in a battle.
				if visit(h, candidate) {
					break
				}
			}
		}},
	}}
	BindQueue(actor, q)

	if got := scanAttackUType(actor, def.UnitDefID); got != hostile {
		t.Fatalf("typed attack winner = %v, want hostile candidate", got)
	}
	if !reflect.DeepEqual(seen, order) {
		t.Fatalf("visitor order = %v, want %v", seen, order)
	}
	if got := sim.Draws(); got != 1 {
		t.Fatalf("typed attack draws = %d, want one draw for the one passing candidate", got)
	}

	sim = rng.SimulationFromState(1)
	seen = seen[:0]
	if got := scanRepairCandidates(actor, def.SightDistance); len(got) != 1 {
		t.Fatalf("repair candidates with hostility gate = %v, want only the friendly candidate", got)
	}
	if got := sim.Draws(); got != 0 {
		t.Fatalf("repair rejection draws = %d, want zero", got)
	}
	hostile.Owner = 0
	hostile.LastDamageSide = actor.Owner
	hostile.LastDamageCause = 5
	if got := scanRepairCandidates(actor, def.SightDistance); len(got) != 1 {
		t.Fatalf("repair candidates during reclaim = %d, want one", len(got))
	}
	hostile.LastDamageCause = 1
	if got := scanRepairCandidates(actor, def.SightDistance); len(got) != 2 {
		t.Fatalf("repair candidates after diplomacy gate = %d, want two", len(got))
	}
	if got := pickRepairCandidate(actor, []*units.Unit{friendly, hostile}); got == nil {
		t.Fatal("repair candidate pick returned nil")
	}
	if got := sim.Draws(); got != 1 {
		t.Fatalf("repair candidate pick draws = %d, want one bounded pick", got)
	}
}

func TestRepairFeaturePairingUsesLatticeOrderAndTournaments(t *testing.T) {
	def := &content.UnitDef{MaxDamage: 100, SightDistance: 96}
	actor := &units.Unit{Handle: 1, Owner: 0, Def: def, Alive: true}
	sim := rng.SimulationFromState(1)
	binding := &QueueBinding{SimRNG: &sim, Resources: func(uint8) (ResourceView, bool) {
		return ResourceView{Stock: [2]float32{0, 0}, Capacity: [2]float32{100, 100}}, true
	}}
	lookupCount := 0
	binding.World = &WorldQueryAdapter{
		LookupFeature: func(int32, int32) (FeatureView, bool) {
			lookupCount++
			return FeatureView{Energy: int32(lookupCount), Metal: int32(lookupCount), Reclaimable: true, Autoreclaimable: true}, true
		},
		TerrainHeight: func(x, z numeric.Fixed) (numeric.Fixed, bool) { return x + z, true },
	}
	q := &Queue{binding: binding}
	BindQueue(actor, q)
	energy, metal := scanFeatureLists(actor, 96)
	if lookupCount != 9 || len(energy) != 9 || len(metal) != 9 {
		t.Fatalf("feature lattice calls/lists = %d/%d/%d, want 9/9/9", lookupCount, len(energy), len(metal))
	}
	for i, feature := range energy {
		offX := int32(-48 + (i/3)*48)
		offZ := int32(-48 + (i%3)*48)
		wantX := numeric.Fixed(int64(offX) << 16)
		wantZ := numeric.Fixed(int64(offZ) << 16)
		if feature.X != wantX || feature.Z != wantZ || feature.Y != wantX+wantZ {
			t.Fatalf("sample %d = (%v,%v,%v), want (%v,%v,%v)", i, feature.X, feature.Z, feature.Y, wantX, wantZ, wantX+wantZ)
		}
	}
	if _, ok := pickFeatureTournament(actor, energy, false); !ok {
		t.Fatal("energy tournament returned no candidate")
	}
	if _, ok := pickFeatureTournament(actor, metal, true); !ok {
		t.Fatal("metal tournament returned no candidate")
	}
	if got := sim.Draws(); got != 6 {
		t.Fatalf("feature tournament draws = %d, want six", got)
	}
}

func simPtr(s *rng.Simulation) *rng.Simulation { return s }

// TestLiveUnitEnumeratorAnswersTheStopQuestion locks the enumerator's visitor
// contract (I1). QueueBinding.ForEachUnit ends the walk when a visitor returns
// TRUE and takes the next slot when it returns false; the session composes it
// that way. The adapter below is written in the production shape — a `stopped`
// latch set from the visitor's own answer — so a scan that reads the answer the
// other way round is caught here rather than in a battle, where it truncated
// every scan to the pool's first live slot.
//
// The whole-pool half used to be the air-base pad scan. WU-19-66 moved that one
// off the enumerator — its candidates are the target registry's third list, not
// a live-unit walk [04 R-AIR-01 §11] — so the repair-candidate scan, which
// still walks every slot, stands in its place.
func TestLiveUnitEnumeratorAnswersTheStopQuestion(t *testing.T) {
	plain := &content.UnitDef{MaxDamage: 100}
	actor := &units.Unit{Handle: 1, Owner: 0, Def: plain, Alive: true}
	first := &units.Unit{Handle: 2, Owner: 0, Def: plain, Alive: true, X: numeric.Fixed(10 << 16), Health: 50}
	second := &units.Unit{Handle: 3, Owner: 0, Def: plain, Alive: true, X: numeric.Fixed(20 << 16), Health: 50}
	last := &units.Unit{Handle: 4, Owner: 0, Def: plain, Alive: true, X: numeric.Fixed(30 << 16), Health: 50}
	for _, u := range []*units.Unit{first, second, last} {
		u.Move.Mode = 1 // the repair filter's committed mover mode [04 R-ORD-02 §4]
	}
	pool4 := []*units.Unit{actor, first, second, last}

	var visited int
	sim := rng.SimulationFromState(1)
	q := &Queue{binding: &QueueBinding{
		SimRNG:    &sim,
		Hostility: func(_, _ *units.Unit) bool { return false },
		World: &WorldQueryAdapter{ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			stopped := false
			for _, candidate := range pool4 {
				if stopped {
					return
				}
				visited++
				if visit(candidate.Handle, candidate) {
					stopped = true
				}
			}
		}},
	}}
	BindQueue(actor, q)

	visited = 0
	damaged := scanRepairCandidates(actor, 0xF00)
	if len(damaged) != 3 || damaged[2] != last {
		t.Fatalf("repair scan = %v, want all three damaged movers with the last slot last", damaged)
	}
	if visited != len(pool4) {
		t.Fatalf("repair scan visited %d slots, want the whole pool (%d)", visited, len(pool4))
	}

	visited = 0
	if got := scanRadiusTarget(actor, 15, false); got != first {
		t.Fatalf("radius scan = %v, want the first in-range candidate", got)
	}
	if visited != 2 {
		t.Fatalf("radius scan visited %d slots, want it to stop on its hit (2)", visited)
	}
	if got := sim.Draws(); got != 0 {
		t.Fatalf("enumerator draws = %d, want none: neither scan draws", got)
	}
}

// TestPatrolPadSeekOffersAlliedPads locks the correction WU-19-66 made to both
// patrol rows' pad seek. The candidate set is the target registry's third list,
// which the registry files by ALLY GROUP — a pad an ally owns is on this
// aircraft's row whenever the pad owner's alliance declaration toward this
// group is set [05 R-SHARE-01 §1][06 §3.1] — and the filter re-tests the three
// admission flags but not liveness [04 R-AIR-01 §11]. The old walk over live
// units with `candidate.Owner != u.Owner` could express neither.
func TestPatrolPadSeekOffersAlliedPads(t *testing.T) {
	padDef := &content.UnitDef{MaxDamage: 100, Builder: true, IsAirBase: true}
	flierDef := &content.UnitDef{MaxDamage: 100, CanFly: true, BMCode: 1}
	flier := &units.Unit{Handle: 1, Owner: 0, Def: flierDef, Alive: true, Health: 50}

	own := &units.Unit{Handle: 5, Owner: 0, Def: padDef, Alive: true, Activated: true}
	ally := &units.Unit{Handle: 6, Owner: 1, Def: padDef, Alive: true, Activated: true}
	// Death-latched since the last rebuild: still on the row, still offered.
	dead := &units.Unit{Handle: 7, Owner: 1, Def: padDef, Alive: true, Dying: true, Activated: true}
	// Deactivated since the last rebuild: the flags ARE re-tested, so it goes.
	off := &units.Unit{Handle: 8, Owner: 1, Def: padDef, Alive: true, Activated: false}

	slots := map[pool.Handle]*units.Unit{5: own, 6: ally, 7: dead, 8: off}
	q := &Queue{binding: &QueueBinding{
		Lookup: func(h pool.Handle) *units.Unit { return slots[h] },
		Movement: &MovementGoalAdapter{
			// The row the registry filed for ally group 0, in unit-array order.
			AirBases: func(group uint8) []pool.Handle {
				if group != 0 {
					return nil
				}
				return []pool.Handle{5, 6, 7, 8}
			},
		},
	}}
	BindQueue(flier, q)

	pads := airBasePads(flier)
	want := []*units.Unit{own, ally, dead}
	if len(pads) != len(want) {
		t.Fatalf("pad seek offered %d pads, want %d (own, allied, death-latched) [04 R-AIR-01 §11]", len(pads), len(want))
	}
	for i := range want {
		if pads[i] != want[i] {
			t.Fatalf("pad seek offered %v, want own/allied/death-latched in list order [04 R-AIR-01 §11]", pads)
		}
	}

	// No port composed: the seek offers nothing rather than re-deriving a
	// second enumeration.
	q.binding.Movement = nil
	if got := airBasePads(flier); got != nil {
		t.Fatalf("with no movement port the seek offered %v, want nothing", got)
	}
}
