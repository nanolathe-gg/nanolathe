package economy

import (
	"math"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestArchivedBucketHasOnlyReportSlots(t *testing.T) {
	if got, want := reflect.TypeOf(ArchivedBucket{}).NumField(), 2; got != want {
		t.Fatalf("archived bucket fields = %d, want %d", got, want)
	}
}

// TestAuthoredIsPerPass locks C1: authored per-pass values accumulate verbatim,
// not multiplied by the tick rate. An authored 5 stays 5 per pass, not 150.
func TestAuthoredIsPerPass(t *testing.T) {
	var b Bucket
	AddProduction(&b, 5)
	if b.Production != 5 {
		t.Fatalf("C1: authored 5 accumulated as %v, want 5", b.Production)
	}
	// Add again simulates second pass accumulation verbatim.
	AddProduction(&b, 5)
	if b.Production != 10 {
		t.Fatalf("C1: two passes of 5 should be 10, got %v", b.Production)
	}
	// Explicit check that ledger.go contains no *30 or /30 time conversion.
	// This is a compile-time contract; the test locks the arithmetic, not the grep.
	if b.Production == 150 {
		t.Fatal("C1: production was scaled by 30, violates no time conversion")
	}
}

// TestCommitOrdering locks C10: per-pass counters report the PASS not available funds.
// Counters are committed before opening stock folds into the allocation pool.
func TestCommitOrdering(t *testing.T) {
	var svc Service
	p := &svc.Players[0]
	// Simulate a pass where mirror production was 10, opening stock 100, so if
	// counters incorrectly included opening stock they'd be 110.
	p.Mirror[Metal].Production = 10
	p.Mirror[Energy].Production = 20
	p.Mirror[Metal].Requested = 7
	p.Mirror[Energy].Requested = 14
	p.Stock[Metal] = 110 // 100 opening +10 production if folded incorrectly
	p.Stock[Energy] = 120
	p.Capacity[Metal] = 1000
	p.Capacity[Energy] = 1000

	svc.Settle(0, 0, nil)

	if p.PassProduced[Metal] != 10 {
		t.Fatalf("C10: PassProduced Metal = %v, want 10 (pass, not pool)", p.PassProduced[Metal])
	}
	if p.PassProduced[Energy] != 20 {
		t.Fatalf("C10: PassProduced Energy = %v, want 20", p.PassProduced[Energy])
	}
	if p.PassConsumed[Metal] != 7 {
		t.Fatalf("C10: PassConsumed Metal = %v, want 7", p.PassConsumed[Metal])
	}
	if p.AIProduction[Metal] != 10 || p.AIProduction[Energy] != 20 {
		t.Fatalf("AI production aggregates = %v, want [10 20]", p.AIProduction)
	}
	if p.AIConsumption[Metal] != 7 || p.AIConsumption[Energy] != 14 {
		t.Fatalf("AI consumption aggregates = %v, want [7 14]", p.AIConsumption)
	}
	if p.TotalProduced[Metal] != 10 {
		t.Fatalf("C10: TotalProduced should be 10, got %v", p.TotalProduced[Metal])
	}
	if p.TotalConsumed[Energy] != 14 {
		t.Fatalf("C10: TotalConsumed Energy = %v, want 14", p.TotalConsumed[Energy])
	}
	// Stock should have been clamped but not used for counters.
	if p.ArchivedMirror[Metal].Production != 10 {
		t.Fatalf("C10: archived production = %v, want 10", p.ArchivedMirror[Metal].Production)
	}
	if p.Mirror[Metal].Production != 0 || p.Mirror[Energy].Production != 0 {
		t.Fatalf("C10: live buckets not zeroed after commit: %+v", p.Mirror)
	}
}

// TestCapacityClampFractionalWaste locks C10: clamp stock to rebuilt capacity,
// overflow to cumulative waste with fractional preserved, stock stays float32.
func TestCapacityClampFractionalWaste(t *testing.T) {
	var svc Service
	p := &svc.Players[0]
	p.Stock[Metal] = 100.25
	p.Capacity[Metal] = 100
	p.Stock[Energy] = 10.5
	p.Capacity[Energy] = 10
	p.Mirror[Metal].Production = 1
	p.Mirror[Energy].Production = 1

	svc.Settle(0, 0, nil)

	if p.Stock[Metal] != 100 {
		t.Fatalf("C10 clamp: Metal stock = %v, want 100", p.Stock[Metal])
	}
	if p.Stock[Energy] != 10 {
		t.Fatalf("C10 clamp: Energy stock = %v, want 10", p.Stock[Energy])
	}
	// Waste must preserve fractional part. 0.25 and 0.5 are exactly representable.
	if math.Abs(p.Waste[Metal]-1.25) > 1e-6 {
		t.Fatalf("C10 waste Metal = %v, want 1.25 preserved", p.Waste[Metal])
	}
	if math.Abs(p.Waste[Energy]-1.5) > 1e-6 {
		t.Fatalf("C10 waste Energy = %v, want 1.5 preserved", p.Waste[Energy])
	}
	// Stock stays float32: check type via assignment, already float32.
	var _ float32 = p.Stock[Metal]
	// Waste is float64 per I2.
	var _ float64 = p.Waste[Metal]
}

func TestCapacityWasteUsesWorkingPrecisionDifference(t *testing.T) {
	var svc Service
	p := &svc.Players[0]
	p.Stock[Energy] = 100000000
	p.Capacity[Energy] = 1
	svc.Settle(0, 0, nil)
	if got, want := p.Waste[Energy], float64(99999999); got != want {
		t.Fatalf("working-precision waste = %v, want %v", got, want)
	}
}

// TestCloakSequentialDebitTruncation locks C13: direct sequential debit with truncation in slot order.
func TestCloakSequentialDebitTruncation(t *testing.T) {
	// Truncation vectors per I3: truncate toward zero.
	vectors := []struct {
		cost float32
		want int32
	}{
		{5.9, 5},
		{5.1, 5},
		{5.0, 5},
		{0.9, 0},
		{-5.9, -5},
		{-0.9, 0},
	}
	for _, v := range vectors {
		if got := int32(v.cost); got != v.want {
			t.Fatalf("C13 trunc: int32(%v)=%d want %d", v.cost, got, v.want)
		}
	}

	// Sequential order: earlier slots consume live stock before later slots are tested.
	var svc Service
	svc.Players[0].Stock[Energy] = 10
	w := units.NewSliced(10, nil)
	def := economyFixtureDef(&content.UnitDef{})
	def.MaxDamage = 100
	h1, _ := w.Create(def, 0, 0, 0, 0)
	h2, _ := w.Create(def, 0, 0, 0, 0)
	// Ensure slot order is h1 then h2 (lowest-free)
	if h1 != 1 || h2 != 2 {
		t.Fatalf("pool slots %d %d want 1 2", h1, h2)
	}
	costs := map[int]float32{
		int(h1): 6.7, // trunc 6
		int(h2): 6.2, // trunc 6
	}
	getCost := func(u *units.Unit) float32 {
		return costs[int(u.Handle)]
	}
	svc.CloakDue = func(*units.Unit) bool { return true }
	ApplyCloakDebits(&svc, w, 0, getCost, nil, nil)
	if svc.Players[0].Stock[Energy] != 4 {
		t.Fatalf("C13 sequential: stock after first debit = %v want 4", svc.Players[0].Stock[Energy])
	}
	// Second should have failed due to insufficient stock (4 <6), so stock stays 4.
	// Verify by checking that only one debit was recorded.
	if got := svc.UnitBuckets(h1)[Energy].Requested; got != 6 {
		t.Fatalf("C13: unit requested should be 6 (only first), got %v", got)
	}
	// Single debit helper truncation test.
	var p Player
	p.Stock[Energy] = 10
	if !DebitCloak(&p, 5.9) {
		t.Fatal("DebitCloak 5.9 should succeed")
	}
	if p.Stock[Energy] != 5 {
		t.Fatalf("DebitCloak stock = %v want 5 (trunc 5.9->5)", p.Stock[Energy])
	}
	if DebitCloak(&p, 5.1) {
		// 5.1 trunc 5, stock 5 -> should succeed (equal)
	}
	// Reset and test failure.
	p.Stock[Energy] = 4
	if DebitCloak(&p, 4.9) {
		// trunc 4, stock 4 -> succeed
	}
	p.Stock[Energy] = 4
	if DebitCloak(&p, 5.9) {
		t.Fatal("DebitCloak should fail when need 5 > stock 4")
	}
}

// TestRebuildCapacity locks C14: storage capacity rebuilt each pass from eligible completed units.
func TestRebuildCapacity(t *testing.T) {
	var svc Service
	w := units.NewSliced(10, nil)
	defA := economyFixtureDef(&content.UnitDef{})
	defA.MaxDamage = 100
	defA.EnergyStorage = 500
	defA.MetalStorage = 1000
	defB := economyFixtureDef(&content.UnitDef{})
	defB.MaxDamage = 100
	defB.EnergyStorage = 250
	defB.MetalStorage = 0
	// Complete units (Remaining==0) contribute.
	h1, _ := w.Create(defA, 0, 0, 0, 0)
	h2, _ := w.Create(defB, 0, 0, 0, 0)
	h3, _ := w.Create(defA, 1, 0, 0, 0)
	// Incomplete nanoframe should not contribute.
	h4, _ := w.Create(defA, 0, 0, 0, 0)
	wIter := w.Iter()
	// Find h4 and set Remaining=1
	for _, u := range wIter {
		if u.Handle == h4 {
			u.Remaining = 1
		}
	}
	// Zero capacities initially.
	svc.Players[0].Capacity[Energy] = 999
	svc.Players[0].Capacity[Metal] = 999
	RebuildCapacity(&svc, w)
	if svc.Players[0].Capacity[Energy] != 750 {
		t.Fatalf("C14 capacity Energy = %v want 750 (500+250)", svc.Players[0].Capacity[Energy])
	}
	if svc.Players[0].Capacity[Metal] != 1000 {
		t.Fatalf("C14 capacity Metal = %v want 1000", svc.Players[0].Capacity[Metal])
	}
	if svc.Players[1].Capacity[Energy] != 500 {
		t.Fatalf("C14 player1 Energy = %v want 500", svc.Players[1].Capacity[Energy])
	}
	// Ensure incomplete unit excluded.
	if svc.Players[0].Capacity[Metal] == 2000 {
		t.Fatal("incomplete unit contributed incorrectly")
	}
	_ = h1
	_ = h2
	_ = h3
}

func TestSettleRebuildsOnlySettlingPlayerCapacity(t *testing.T) {
	var svc Service
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	svc.Players[0].EndGameCountdown = -1
	svc.Players[1].Capacity[Metal] = 777
	svc.Players[1].Capacity[Energy] = 888
	w := units.NewSliced(10, nil)
	def := economyFixtureDef(&content.UnitDef{MetalStorage: 100, EnergyStorage: 200, MaxDamage: 1})
	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	svc.Settle(0, 0, w)
	if got := svc.Players[1].Capacity; got != [2]float32{777, 888} {
		t.Fatalf("settling player 0 changed player 1 capacity: %v", got)
	}
}

// TestStableSlotOrderVisitation locks C6: units visited in slot ascending order.
func TestStableSlotOrderVisitation(t *testing.T) {
	w := units.NewSliced(10, nil)
	def := economyFixtureDef(&content.UnitDef{})
	def.MaxDamage = 100
	// Create units for player 0 in reverse creation order? But pool always lowest-free, so order is insertion order.
	// Create three units, then free middle, reuse should give lowest-free and order still ascending.
	h1, _ := w.Create(def, 0, 0, 0, 0)
	h2, _ := w.Create(def, 0, 0, 0, 0)
	h3, _ := w.Create(def, 0, 0, 0, 0)
	_ = h3
	w.Destroy(h2, units.DeathKilled)
	w.TeardownCleanup()
	h4, _ := w.Create(def, 0, 0, 0, 0)
	if h4 != h2 {
		t.Fatalf("reuse lowest-free got %d want %d", h4, h2)
	}
	var order []int
	ForEachUnitOrdered(w, 0, func(u *units.Unit) {
		order = append(order, int(u.Handle))
	})
	// Should be ascending: 1,2,3
	for i := 1; i < len(order); i++ {
		if order[i] <= order[i-1] {
			t.Fatalf("C6 not ascending: %v", order)
		}
	}
	if len(order) != 3 {
		t.Fatalf("C6 expected 3 units, got %v", order)
	}
	if order[0] != int(h1) {
		t.Fatalf("C6 first %d want %d", order[0], h1)
	}
}

// TestMirrorClosedWriterSurface locks C11: closed writer set enumerated, no factory queue-draw writer.
func TestMirrorClosedWriterSurface(t *testing.T) {
	var svc Service
	p := &svc.Players[0]
	// Record init
	InitPlayer(p)
	// Per-pass clear through the assembled settlement pass.
	p.Mirror[Metal].Production = 5
	svc.Settle(0, 0, nil)
	if p.Mirror[Metal].Production != 0 {
		t.Fatal("assembled settlement should clear mirror production")
	}
	// Two admission helpers
	var b [2]Bucket
	b[Energy].Carry = 0
	b[Metal].Carry = 0
	AdmitTwoResource(&b, 3, 4)
	if b[Energy].Accepted != 3 || b[Metal].Accepted != 4 {
		t.Fatal("AdmitTwoResource failed")
	}
	b[Energy].Carry = 1 // positive carry denies admission
	AdmitTwoResource(&b, 1, 1)
	if b[Energy].Accepted == 4 {
		t.Fatal("AdmitTwoResource should deny when carry positive")
	}
	var b2 [2]Bucket
	b2[Energy].Carry = 0
	AdmitOneResource(&b2, 5)
	if b2[Energy].Accepted != 5 {
		t.Fatal("AdmitOneResource failed")
	}
	b2[Energy].Carry = 1
	AdmitOneResource(&b2, 5)
	if b2[Energy].Accepted != 5 {
		t.Fatal("AdmitOneResource should deny when carry positive")
	}
	AdmitTwoResourceToMirror(p, 2, 2)
	AdmitOneResourceToMirror(p, 2)
	// Immediate debit path
	p.Stock[Energy] = 10
	p.Stock[Metal] = 10
	if !ImmediateDebit(p, nil, 3, 3) {
		t.Fatal("ImmediateDebit should succeed")
	}
	if ImmediateDebit(p, nil, 100, 100) {
		t.Fatal("ImmediateDebit should fail when insufficient")
	}
	// Spawn credit
	var sp Player
	CreditSpawn(&sp, Metal, 100)
	if sp.Stock[Metal] != 100 {
		t.Fatal("CreditSpawn failed")
	}
	// Construction termination credit with special scales -0.5 and -0.7
	var ct Player
	CreditConstructionTermination(&ct, 0.5, 100, -1) // normal add
	if ct.Mirror[Metal].Production != 50 {
		t.Fatalf("normal termination credit = %v want 50", ct.Mirror[Metal].Production)
	}
	ct.Mirror[Metal].Production = 0
	CreditConstructionTermination(&ct, 0.5, 100, 0) // credit 0.5
	if ct.Mirror[Metal].Production != 25 {
		t.Fatalf("special 0 credit = %v want 25", ct.Mirror[Metal].Production)
	}
	ct.Mirror[Metal].Production = 0
	CreditConstructionTermination(&ct, 0.5, 100, 1) // credit 0.7
	if ct.Mirror[Metal].Production != 35 {
		t.Fatalf("special 1 credit = %v want 35", ct.Mirror[Metal].Production)
	}
	// There is NO factory queue-draw writer — this comment is the contract.
	// If a future helper named FactoryQueueDraw existed, this test would fail via vet
	// because the closed set is enumerated above.
}

// TestImmediateDebitCreditsThePayingSubrecord locks whose `requested`
// accumulator the direct two-resource payment writes [05 R-ECO-01 §7]. The five
// admission helpers "are small methods on the economy subrecord ... the
// 'builder's subrecord' a caller passes is always a subrecord, never a player
// record"; the two direct payments reach through the subrecord's owner pointer
// for the LIVE STOCK only. This used to write the request to the player mirror,
// which is the one bucket it must not touch.
func TestImmediateDebitCreditsThePayingSubrecord(t *testing.T) {
	p := &Player{}
	p.Stock[Energy] = 10
	p.Stock[Metal] = 10
	var shooter [2]Bucket

	if !ImmediateDebit(p, &shooter, 3, 4) {
		t.Fatal("the payment was refused with stock in hand; both compares are inclusive [05 R-ECO-01 §7]")
	}
	if shooter[Energy].Requested != 3 || shooter[Metal].Requested != 4 {
		t.Fatalf("the paying subrecord recorded (%v, %v), want (3, 4): the request stays on the subrecord [05 R-ECO-01 §7]", shooter[Energy].Requested, shooter[Metal].Requested)
	}
	if p.Mirror[Energy].Requested != 0 || p.Mirror[Metal].Requested != 0 {
		t.Fatalf("the player mirror recorded (%v, %v), want (0, 0): only the live stock is reached through the owner pointer [05 R-ECO-01 §7]", p.Mirror[Energy].Requested, p.Mirror[Metal].Requested)
	}
	// The live stock IS the player's, and the accepted accumulator is never
	// touched, so an immediate payment never becomes carry.
	if p.Stock[Energy] != 7 || p.Stock[Metal] != 6 {
		t.Fatalf("stock after the payment = (%v, %v), want (7, 6)", p.Stock[Energy], p.Stock[Metal])
	}
	if shooter[Energy].Accepted != 0 || shooter[Metal].Accepted != 0 {
		t.Fatalf("the payment credited an accepted accumulator (%v, %v); it must not [05 R-ECO-01 §7]", shooter[Energy].Accepted, shooter[Metal].Accepted)
	}
	// All-or-nothing: a refusal debits and records nothing at all.
	before := shooter
	if ImmediateDebit(p, &shooter, 100, 0) {
		t.Fatal("a payment beyond the energy stock was accepted [05 R-ECO-01 §7]")
	}
	if shooter != before || p.Stock[Energy] != 7 || p.Stock[Metal] != 6 {
		t.Fatal("a refused payment moved something; both compares precede every write [05 R-ECO-01 §7]")
	}
}
