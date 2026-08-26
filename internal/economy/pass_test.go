package economy

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Fixtures for the assembled settlement pass (REVIEW.md WU-R2-2).
//
// These lock the composed pass rather than the arithmetic helpers. Every one of
// them passed vacuously before the pass was assembled, because TickPlayer
// settled a player mirror and none of the other steps ran at all.

func settleTestWorld(t *testing.T, n int) (*units.World, []pool.Handle) {
	t.Helper()
	w := units.New(10, nil)
	hs := make([]pool.Handle, 0, n)
	for i := 0; i < n; i++ {
		def := &content.UnitDef{}
		def.MaxDamage = 100
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("create unit %d: %v", i, err)
		}
		hs = append(hs, h)
	}
	return w, hs
}

// settlingPlayer prepares slot p so TickPlayer's gate chain admits it, with
// capacity high enough that the post-settlement clamp is a no-op unless the
// fixture says otherwise [05 "Authoritative settlement order"] C3 C4.
func settlingPlayer(s *Service, p int) *Player {
	pl := &s.Players[p]
	InitPlayer(pl)
	activePlayer(pl)
	pl.Capacity[Metal] = 1e6
	pl.Capacity[Energy] = 1e6
	return pl
}

// TestTickPlayerSettlesPerUnitState is the F-6 regression guard. TickPlayer
// used to call a mirror-only Settle and drop the world, so per-unit debt never
// entered the sums and both ratios were computed from the mirror alone.
func TestTickPlayerSettlesPerUnitState(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	pl.Stock[Energy] = 10
	w, hs := settleTestWorld(t, 1)

	// One unit carrying 20 energy of debt against a pool of 10: debtRatio 0.5,
	// so half the debt is paid and half is carried.
	svc.UnitBuckets(hs[0])[Energy].Carry = 20

	svc.TickPlayer(0, 0, w, nil)

	if got := svc.UnitBuckets(hs[0])[Energy].Carry; got != 10 {
		t.Fatalf("unit carry after settlement = %v want 10; the world is not "+
			"reaching the settlement (per-unit debt excluded from the sums)", got)
	}
	if got := pl.Stock[Energy]; got != 0 {
		t.Fatalf("closing stock = %v want 0 (pool 10 fully spent on debt)", got)
	}
}

// TestSettledAIAggregatesIncludeUnitAndMirror locks the fields consumed by the
// strategic score: settlement folds the fixed-order unit buckets and player
// mirror, rather than publishing the mirror-only reporting counters [R-P0-05].
func TestSettledAIAggregatesIncludeUnitAndMirror(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	w, hs := settleTestWorld(t, 1)
	unit := svc.UnitBuckets(hs[0])
	unit[Energy].Production = 3
	unit[Energy].Requested = 4
	unit[Metal].Production = 5
	unit[Metal].Requested = 6
	pl.Mirror[Energy].Production = 7
	pl.Mirror[Energy].Requested = 8
	pl.Mirror[Metal].Production = 9
	pl.Mirror[Metal].Requested = 10

	svc.Settle(0, 0, w)
	if got, want := pl.AIProduction[Energy], float32(10); got != want {
		t.Fatalf("AI energy production = %v, want %v", got, want)
	}
	if got, want := pl.AIConsumption[Energy], float32(12); got != want {
		t.Fatalf("AI energy consumption = %v, want %v", got, want)
	}
	if got, want := pl.AIProduction[Metal], float32(14); got != want {
		t.Fatalf("AI metal production = %v, want %v", got, want)
	}
	if got, want := pl.AIConsumption[Metal], float32(16); got != want {
		t.Fatalf("AI metal consumption = %v, want %v", got, want)
	}
}

// TestCarrySurvivesThePass locks steps 6 and 8 of [05 "Authoritative settlement
// order"]: carry is written back and survives; only the pass inputs clear.
// Zeroing the whole subrecord makes oldCarry permanently zero and the debt
// stage of the two-stage algorithm dead.
func TestCarrySurvivesThePass(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	pl.Stock[Energy] = 4
	w, hs := settleTestWorld(t, 1)
	b := svc.UnitBuckets(hs[0])
	b[Energy].Carry = 8
	b[Energy].Production = 3
	b[Energy].Requested = 5

	svc.Settle(0, 0, w)

	after := svc.UnitBuckets(hs[0])
	if after[Energy].Carry == 0 {
		t.Fatal("carry was cleared with the pass inputs; it is cross-pass state")
	}
	if after[Energy].Production != 0 || after[Energy].Requested != 0 || after[Energy].Accepted != 0 {
		t.Fatalf("pass inputs not cleared: %+v", after[Energy])
	}
	if svc.Players[0].Mirror[Energy].Production != 0 {
		t.Fatalf("mirror pass inputs not cleared: %+v", svc.Players[0].Mirror[Energy])
	}
}

// TestCloakDebitRunsInSlotOrderDuringPass locks C6/C13 through the assembled
// pass: the debit is sequential and an earlier slot can starve a later one.
func TestCloakDebitRunsInSlotOrderDuringPass(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	pl.Stock[Energy] = 10
	w, _ := settleTestWorld(t, 2)
	svc.CloakCost = func(u *units.Unit) float32 { return 6 }

	svc.Settle(0, 0, w)

	// Slot 1 debits 6 of 10; slot 2 then finds 4 and cannot pay.
	if got := pl.ArchivedMirror[Energy].Requested; got != 6 {
		t.Fatalf("cloak requested = %v want 6 (only the earlier slot pays)", got)
	}
}

// TestCapacityClampAndWaste locks C10: stock clamps to the rebuilt capacity and
// the overflow lands in cumulative waste with its fractional part intact.
func TestCapacityClampAndWaste(t *testing.T) {
	var svc Service
	pl := settlingPlayer(&svc, 0)
	pl.Capacity[Metal] = 0 // rebuilt below from the world
	pl.Stock[Metal] = 0
	pl.Mirror[Metal].Production = 10.5

	w, _ := settleTestWorld(t, 1)
	// One completed unit with 4 metal storage: capacity rebuilds to 4.
	for _, u := range w.Iter() {
		if u != nil && u.Def != nil {
			u.Def.MetalStorage = 4
		}
	}

	svc.Settle(0, 0, w)

	if pl.Capacity[Metal] != 4 {
		t.Fatalf("capacity = %v want 4 (rebuilt from the world each pass)", pl.Capacity[Metal])
	}
	if pl.Stock[Metal] != 4 {
		t.Fatalf("stock = %v want 4 (clamped to capacity)", pl.Stock[Metal])
	}
	if got := pl.Waste[Metal]; math.Abs(got-6.5) > 1e-6 {
		t.Fatalf("waste = %v want 6.5 with the fraction preserved", got)
	}
}
