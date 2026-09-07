package economy

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestSettlementIterationSkipsLaterFreedCloaker locks the liveness boundary
// shared by the direct owner walk and the assembled cloak pass. A callback on
// an earlier slot may remove a later slot; the removed unit neither consumes
// energy nor receives the transition [05 "Cloak debit"][04 §1.1].
func TestSettlementIterationSkipsLaterFreedCloaker(t *testing.T) {
	var svc Service
	svc.Players[0].Stock[Energy] = 10
	w := units.NewSliced(3, nil)
	def := economyFixtureDef(&content.UnitDef{UnitName: "iter-cloak", MaxDamage: 1, Limit: -1})
	h1, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	svc.CloakDue = func(u *units.Unit) bool {
		if u.Handle == h1 {
			w.FreeImmediate(h2)
		}
		return true
	}

	ApplyCloakDebits(&svc, w, 0, func(*units.Unit) float32 { return 6 }, nil, nil)
	if got, want := svc.Players[0].Stock[Energy], float32(4); got != want {
		t.Fatalf("energy after earlier-slot debit and later free = %v, want %v", got, want)
	}
	if got := svc.UnitBuckets(h1)[Energy].Requested; got != 6 {
		t.Fatalf("first unit requested = %v, want 6", got)
	}
	if got := svc.UnitBuckets(h2)[Energy].Requested; got != 0 {
		t.Fatalf("freed later unit requested = %v, want 0", got)
	}
	if w.Unit(h2) != nil {
		t.Fatal("freed later unit remains live")
	}
}

// TestForEachUnitOrderedOwnerResult checks the visible owner-slice result in
// slot order [05 "Authoritative settlement order"] C6. It does not claim to
// prove the absence of an unrelated scan; the paired allocation benchmark is
// the evidence for that implementation property.
func TestForEachUnitOrderedOwnerResult(t *testing.T) {
	w := units.NewSliced(2, nil)
	def := economyFixtureDef(&content.UnitDef{UnitName: "iter-owner", MaxDamage: 1, Limit: -1})
	h0, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	var got []pool.Handle
	ForEachUnitOrdered(w, 0, func(u *units.Unit) { got = append(got, u.Handle) })
	if len(got) != 1 || got[0] != h0 {
		t.Fatalf("owner 0 order = %v, want [%d]", got, h0)
	}
}

// TestSettlementIterationTrace is a full assembled-pass trace with energy and
// metal shortages plus three sequential cloak payments. It records every
// player and per-unit ledger field settlement writes, both cloak outcomes and
// the global simulation RNG state. The same trace is run against the baseline
// revision as an EC-P1 comparison; it is deliberately not a partial session
// fingerprint.
func TestSettlementIterationTrace(t *testing.T) {
	got := settlementIterationTrace(t)
	const want = "player-E=00000000,00000000,00000000,00000000/00000000,00000000 player-M=00000000,00000000,00000000,00000000/00000000,00000000 stock=40400000,40000000 cap=43960000,43960000 pass=40400000,40400000,41200000,40400000 total=4008000000000000,4008000000000000,4024000000000000,4008000000000000 waste=0000000000000000,0000000000000000 unit=1 hidden=true requested=true E=00000000,00000000,00000000,00000000/3f800000,40800000 M=00000000,00000000,00000000,00000000/3f800000,00000000 unit=2 hidden=true requested=true E=00000000,00000000,00000000,00000000/3f800000,40a00000 M=00000000,00000000,00000000,00000000/3f800000,00000000 unit=3 hidden=false requested=true E=00000000,00000000,00000000,00000000/3f800000,3f800000 M=00000000,00000000,00000000,00000000/3f800000,40400000 rng=222213584/3"
	if got != want {
		t.Fatalf("settlement trace = %s", got)
	}
	t.Logf("settlement trace: %s", got)
}

func settlementIterationTrace(t *testing.T) string {
	t.Helper()
	var s Service
	p := settlingPlayer(&s, 0)
	p.Stock[Energy] = 10
	p.Stock[Metal] = 2

	w := units.NewSliced(3, nil)
	sim := rng.NewSimulation(73)
	w.SetSimulationRNG(&sim)
	makeDef := func(name string, cloak int32) *content.UnitDef {
		return economyFixtureDef(&content.UnitDef{
			UnitName: name, Limit: -1, MaxDamage: 1,
			EnergyMake: 1, MetalMake: 1,
			EnergyStorage: 100, MetalStorage: 100,
			CloakCost: cloak, CloakCostMoving: cloak,
		})
	}
	h1, err := w.Create(makeDef("trace-one", 4), 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := w.Create(makeDef("trace-two", 5), 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := w.Create(makeDef("trace-three", 4), 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []pool.Handle{h1, h2, h3} {
		w.Unit(h).SetCloaked(true)
	}

	b1, b2, b3 := s.UnitBuckets(h1), s.UnitBuckets(h2), s.UnitBuckets(h3)
	b1[Energy] = Bucket{Carry: 4, Requested: 5, Accepted: 5}
	b2[Energy] = Bucket{Carry: 2, Requested: 3, Accepted: 3}
	b3[Energy] = Bucket{Requested: 1, Accepted: 1}
	b1[Metal] = Bucket{Carry: 1, Requested: 5, Accepted: 5}
	b2[Metal] = Bucket{Carry: 1, Requested: 2, Accepted: 2}
	b3[Metal] = Bucket{Requested: 3, Accepted: 3}
	s.CloakCost = func(u *units.Unit) float32 { return u.CloakCost() }
	s.CloakDue = func(u *units.Unit) bool { return u != nil && u.IsCloaked && u.RevealDeadline == 0 }

	drawsBefore := sim.Draws()
	s.Settle(0, 0, w)
	if got := sim.Draws(); got != drawsBefore {
		t.Fatalf("settlement changed simulation RNG draws: %d -> %d", drawsBefore, got)
	}

	var out strings.Builder
	writeBucketTrace(&out, "player-E", p.Mirror[Energy], p.ArchivedMirror[Energy])
	writeBucketTrace(&out, "player-M", p.Mirror[Metal], p.ArchivedMirror[Metal])
	fmt.Fprintf(&out, "stock=%08x,%08x cap=%08x,%08x pass=%08x,%08x,%08x,%08x total=%016x,%016x,%016x,%016x waste=%016x,%016x ",
		math.Float32bits(p.Stock[Energy]), math.Float32bits(p.Stock[Metal]),
		math.Float32bits(p.Capacity[Energy]), math.Float32bits(p.Capacity[Metal]),
		math.Float32bits(p.PassProduced[Energy]), math.Float32bits(p.PassProduced[Metal]),
		math.Float32bits(p.PassConsumed[Energy]), math.Float32bits(p.PassConsumed[Metal]),
		math.Float64bits(p.TotalProduced[Energy]), math.Float64bits(p.TotalProduced[Metal]),
		math.Float64bits(p.TotalConsumed[Energy]), math.Float64bits(p.TotalConsumed[Metal]),
		math.Float64bits(p.Waste[Energy]), math.Float64bits(p.Waste[Metal]))
	for _, h := range []pool.Handle{h1, h2, h3} {
		u := w.Unit(h)
		ue := s.unitBuckets[h]
		fmt.Fprintf(&out, "unit=%d hidden=%t requested=%t ", h, u.Hidden, u.IsCloaked)
		writeBucketTrace(&out, "E", ue.Buckets[Energy], ue.Archived[Energy])
		writeBucketTrace(&out, "M", ue.Buckets[Metal], ue.Archived[Metal])
	}
	fmt.Fprintf(&out, "rng=%d/%d", sim.State, sim.Draws())
	return strings.TrimSpace(out.String())
}

func writeBucketTrace(out *strings.Builder, name string, live Bucket, archived ArchivedBucket) {
	fmt.Fprintf(out, "%s=%08x,%08x,%08x,%08x/%08x,%08x ", name,
		math.Float32bits(live.Production), math.Float32bits(live.Requested),
		math.Float32bits(live.Accepted), math.Float32bits(live.Carry),
		math.Float32bits(archived.Production), math.Float32bits(archived.Requested))
}
