package orders

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
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
				if !visit(h, candidate) {
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
