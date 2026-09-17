package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// repairPatrolRefusalFixture arms one repair-patrol visit whose candidate scan
// finds two nonhostile damaged units — the bounded pick of [04 R-ORD-01 §4]
// skips its draw on a one-element list, so two is the smallest gather that
// spends a simulation value — and whose surroundings are thick with qualifying
// reclaim features. The metal store sits below the 20 % gate and the energy
// store above it, so a visit that leaves the paragraph's repair sentence
// reaches the feature pairing rather than the "both stores are healthy" hold.
// That is what makes the draw counts below discriminating.
func repairPatrolRefusalFixture(t *testing.T, air bool) (*units.Unit, []*units.Unit, *rng.Simulation, *Queue) {
	t.Helper()
	actorDef := &content.UnitDef{
		UnitDefID: 9, MaxDamage: 100, SightDistance: 96,
		CanReclamate: true, CanMove: true, CanFly: air, BMCode: 1, MaxWaterDepth: 12,
	}
	candidateDef := &content.UnitDef{UnitDefID: 10, MaxDamage: 100, BMCode: 1}

	actor := &units.Unit{Handle: 1, Owner: 0, Def: actorDef, Alive: true, Health: actorDef.MaxDamage}
	// Mode 2 keeps the air twin past its own preamble; the low-health pad seek
	// of step 3 stays shut because the actor is at full health.
	actor.Move.Mode, actor.Move.ModeMirror = 1, 1
	if air {
		actor.Move.Mode, actor.Move.ModeMirror = 2, 2
	}

	candidates := make([]*units.Unit, 0, 2)
	for i := range 2 {
		c := &units.Unit{
			Handle: pool.Handle(2 + i), Owner: 0, Def: candidateDef, Alive: true, Health: 50,
			X: numeric.Fixed(int64(20+8*i) << 16),
			Y: numeric.Fixed(30 << 16), // top 30, clear of sea level 20
		}
		c.Move.Mode, c.Move.ModeMirror = 1, 1
		candidates = append(candidates, c)
	}

	sim := rng.SimulationFromState(1)
	binding := &QueueBinding{
		SimRNG:    &sim,
		Hostility: func(_, _ *units.Unit) bool { return false },
		Resources: func(uint8) (ResourceView, bool) {
			// Energy at 100 % of storage opens the repair scan; metal at 0 %
			// keeps the "both stores healthy" hold shut.
			return ResourceView{Stock: [2]float32{0, 100}, Capacity: [2]float32{100, 100}}, true
		},
		Movement: &MovementGoalAdapter{
			InstallPoint: func(PointGoalRequest) bool { return true },
			InstallAir:   func(AirGoalRequest) bool { return true },
			Release:      func(*Node) bool { return true },
		},
		World: &WorldQueryAdapter{
			ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
				if visit(actor.Handle, actor) {
					return
				}
				for _, c := range candidates {
					if visit(c.Handle, c) {
						return
					}
				}
			},
			LookupUnit: func(h pool.Handle) *units.Unit {
				for _, c := range candidates {
					if c.Handle == h {
						return c
					}
				}
				return nil
			},
			// Every lattice sample qualifies, so both tournaments of
			// [04 R-ORD-01 §4] run if the visit ever reaches the pairing.
			LookupFeature: func(int32, int32) (FeatureView, bool) {
				return FeatureView{Energy: 10, Metal: 10, Reclaimable: true, Autoreclaimable: true}, true
			},
			TerrainHeight: func(numeric.Fixed, numeric.Fixed) (numeric.Fixed, bool) { return 0, true },
			SeaLevel:      func() uint8 { return 20 },
		},
	}
	q := &Queue{binding: binding}
	BindQueue(actor, q)
	return actor, candidates, &sim, q
}

// TestRepairPatrolWaitsWhenTheRepairIssueIsRefused locks the third arm of
// [04 R-ORD-01 §4]'s repair sentence — "when resolvable and the issue helper
// accepts it → *rotate*, else *wait*" — and its air twin's restatement in
// [04 R-ORD-01 §7], "acceptance → *rotate*, refusal → *wait*". The refusal here
// is the issuer's own: stance 3 refuses outright [04 R-STANCE-01 §4], so
// command code 8 resolves and the helper still says no.
//
// The assertion that matters is the draw count, not the return code alone. A
// fall-through runs the feature pairing's two tournaments, three bounded picks
// each ([01 §7.5]), and those six extra simulation values shift the one
// authoritative stream for the rest of the session (I4).
func TestRepairPatrolWaitsWhenTheRepairIssueIsRefused(t *testing.T) {
	// A fixed slice, not a map: the two rows run in one order (I1).
	for _, row := range []struct {
		name string
		air  bool
	}{
		{"ground", false},
		{"air twin", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			actor, _, sim, q := repairPatrolRefusalFixture(t, row.air)
			actor.Flags = actor.Flags&^(stanceFieldMask<<stanceMoveShift) | 3<<stanceMoveShift
			handler := repairPatrolHandler
			if row.air {
				handler = vtolRepairPatrolHandler
			}
			n := &Node{Owner: actor.Handle, Phase: 1}

			got := handler(actor, n, 0, 100)

			if got != 3 {
				t.Fatalf("refused repair issue returned %d, want 3 (*wait*)", got)
			}
			if draws := sim.Draws(); draws != 1 {
				t.Fatalf("refused repair drew %d simulation values, want 1 (the candidate pick alone); "+
					"the feature tournament's six picks must not run [01 §7.5][I4]", draws)
			}
			if q.LenPrimary() != 0 {
				t.Fatalf("refused repair inserted %d records; retail issues none", q.LenPrimary())
			}
		})
	}
}

// TestRepairPatrolWaitsWhenCodeEightDoesNotResolve is the sentence's other
// refusal arm: the candidate passes the scan's filter and the ground twin's
// post-pick diplomacy recheck, but command code 8 does not resolve against it,
// so there is nothing for the issue helper to accept. A submerged candidate is
// the reachable case — the candidate filter carries no water term while
// nano-reach's water clause does ([04 R-ORD-01 §7]: "it will not repair a unit
// whose top is under water").
func TestRepairPatrolWaitsWhenCodeEightDoesNotResolve(t *testing.T) {
	actor, candidates, sim, q := repairPatrolRefusalFixture(t, false)
	actor.Def.MaxWaterDepth = 0 // wade half becomes sea 20 <= targetTop
	for _, c := range candidates {
		c.Y = numeric.Fixed(int64(-40) << 16) // top -40, well under sea level 20
	}
	n := &Node{Owner: actor.Handle, Phase: 1}

	if got := repairPatrolHandler(actor, n, 0, 100); got != 3 {
		t.Fatalf("unresolvable code 8 returned %d, want 3 (*wait*)", got)
	}
	if draws := sim.Draws(); draws != 1 {
		t.Fatalf("unresolvable code 8 drew %d simulation values, want 1 [01 §7.5][I4]", draws)
	}
	if q.LenPrimary() != 0 {
		t.Fatalf("unresolvable code 8 inserted %d records", q.LenPrimary())
	}
}

// TestRepairPatrolStillPairsFeaturesWithNoCandidate keeps the first arm honest.
// With nothing to pick, [04 R-ORD-01 §4]'s next sentence applies and the feature
// pairing runs with its six bounded picks; without this row the fix above could
// be "always wait" and still pass.
func TestRepairPatrolStillPairsFeaturesWithNoCandidate(t *testing.T) {
	actor, candidates, sim, q := repairPatrolRefusalFixture(t, false)
	for _, c := range candidates {
		c.Health = c.Def.MaxDamage // undamaged and finished: filtered out
	}
	n := &Node{Owner: actor.Handle, Phase: 1}

	if got := repairPatrolHandler(actor, n, 0, 100); got != 3 {
		t.Fatalf("empty candidate list returned %d, want 3 (the spawned reclaim waits)", got)
	}
	if draws := sim.Draws(); draws != 6 {
		t.Fatalf("feature pairing drew %d simulation values, want 6 (three per nonempty list) [01 §7.5]", draws)
	}
	if q.LenPrimary() != 1 || q.Primary()[0].ID != Lookup("Reclaim") {
		t.Fatalf("empty candidate list did not spawn the patrol reclaim: %d records", q.LenPrimary())
	}
}
