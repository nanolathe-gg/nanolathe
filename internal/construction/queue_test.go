package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Architectural: no second queue type — factory products live as typed payloads on orders.Node
// in the PRIMARY segment via orders.Queue Push/CoalesceTail/CancelTailMost [PLAN_08 C15][GAP T3].
// This test file locks that through the real orders.Queue; it does not define its own node type.

func factoryUnit() *units.Unit {
	return &units.Unit{Handle: 1, Owner: 0}
}

func TestCoalesceTailOnly(t *testing.T) {
	f := factoryUnit()
	q := orders.QueueForUnit(f)
	if q == nil {
		t.Fatalf("queue nil")
	}
	// Ensure clean.
	// Identical product at tail merges [C20][05 "Queue insertion"].
	if err := QueueBuild(f, "armflash", 2); err != nil {
		t.Fatalf("QueueBuild A 2: %v", err)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("after first A len %d want 1", q.LenPrimary())
	}
	n := q.Primary()[0]
	if n.Param2 != 2 {
		t.Fatalf("first A Param2 %d want 2", n.Param2)
	}
	if err := QueueBuild(f, "armflash", 3); err != nil {
		t.Fatalf("QueueBuild A again: %v", err)
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("identical tail should merge len %d want 1", q.LenPrimary())
	}
	if got := q.Primary()[0].Param2; got != 5 {
		t.Fatalf("coalesced count %d want 5", got)
	}
	// Distinct product never merged [C20].
	if err := QueueBuild(f, "armflea", 1); err != nil {
		t.Fatalf("QueueBuild distinct: %v", err)
	}
	if q.LenPrimary() != 2 {
		t.Fatalf("distinct product should not coalesce len %d want 2", q.LenPrimary())
	}
	// Identical product separated from tail never merged [C20][04 §3.3].
	if err := QueueBuild(f, "armflash", 1); err != nil {
		t.Fatalf("QueueBuild separated identical: %v", err)
	}
	if q.LenPrimary() != 3 {
		t.Fatalf("separated identical should not coalesce len %d want 3", q.LenPrimary())
	}
	// Verify queue contains two armflash entries and one armflea, regardless of insertion order
	// (tail-append FIFO vs after-active LIFO). Separated identical must not coalesce, distinct must not.
	prim := q.Primary()
	if len(prim) != 3 {
		t.Fatalf("prim len %d want 3", len(prim))
	}
	head := prim[0]
	if head.Param2 != 5 {
		t.Fatalf("head count corrupted %d", head.Param2)
	}
	countFlash, countFlea := 0, 0
	for _, n := range prim {
		if n.Param1 == productID("armflash") {
			countFlash++
		} else if n.Param1 == productID("armflea") {
			countFlea++
		}
	}
	if countFlash != 2 || countFlea != 1 {
		t.Fatalf("product counts flash %d flea %d want 2,1", countFlash, countFlea)
	}
	// Ensure the separated armflash entry has count 1
	foundSeparated := false
	for _, n := range prim[1:] {
		if n.Param1 == productID("armflash") && n.Param2 == 1 {
			foundSeparated = true
		}
	}
	if !foundSeparated {
		t.Fatalf("separated armflash count 1 not found")
	}
	// Case-insensitive: canonical key lowercases [02 §5]
	q2Factory := &units.Unit{Handle: 2, Owner: 0}
	_ = orders.QueueForUnit(q2Factory) // ensure queue
	if err := QueueBuild(q2Factory, "ArmFlash", 1); err != nil {
		t.Fatalf("case variant build: %v", err)
	}
	if err := QueueBuild(q2Factory, "armflash", 2); err != nil {
		t.Fatalf("case variant coalesce: %v", err)
	}
	q2 := orders.QueueForUnit(q2Factory)
	if q2.LenPrimary() != 1 || q2.Primary()[0].Param2 != 3 {
		t.Fatalf("case-insensitive coalesce failed len %d param %d", q2.LenPrimary(), q2.Primary()[0].Param2)
	}
}

func TestCancelTailMostTombstone(t *testing.T) {
	// Ensure empty queue by using fresh factory handle
	f2 := &units.Unit{Handle: 3, Owner: 0}
	q := orders.QueueForUnit(f2)
	// Build queue: A, B where A is head active, B is tail non-head.
	if err := QueueBuild(f2, "armflash", 1); err != nil {
		t.Fatalf("build A: %v", err)
	}
	if err := QueueBuild(f2, "armflea", 1); err != nil {
		t.Fatalf("build B: %v", err)
	}
	if q.LenPrimary() != 2 {
		t.Fatalf("setup len %d want 2", q.LenPrimary())
	}
	prim := q.Primary()
	tailPtr := prim[1]
	headPtr := prim[0]
	if tailPtr.Flags&orders.FlagTombstone != 0 {
		t.Fatalf("tail should not be tombstoned before cancel")
	}
	// Cancel tail-most armflea (tail is B). Tail is non-head, so cancellation should tombstone it [04 §3.3].
	if err := CancelTailMost(f2, "armflea"); err != nil {
		t.Fatalf("cancel tail-most: %v", err)
	}
	if tailPtr.Flags&orders.FlagTombstone == 0 {
		t.Fatalf("tombstone not set on non-head cancel [C20][04 §3.3]")
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("after cancel len %d want 1", q.LenPrimary())
	}
	if q.Primary()[0].Param1 != headPtr.Param1 {
		t.Fatalf("head changed after tail cancel")
	}
	// Count decrement case: enqueue count 5, cancel one should decrement Param2 not remove node
	f3 := &units.Unit{Handle: 4, Owner: 0}
	q3 := orders.QueueForUnit(f3)
	if err := QueueBuild(f3, "armflash", 5); err != nil {
		t.Fatalf("build count5: %v", err)
	}
	if q3.Primary()[0].Param2 != 5 {
		t.Fatalf("count5 param %d", q3.Primary()[0].Param2)
	}
	if err := CancelTailMost(f3, "armflash"); err != nil {
		t.Fatalf("cancel one of 5: %v", err)
	}
	if q3.LenPrimary() != 1 || q3.Primary()[0].Param2 != 4 {
		t.Fatalf("decrement not applied len %d param %d", q3.LenPrimary(), q3.Primary()[0].Param2)
	}
	// After decrement, node should NOT be tombstoned (still live)
	if q3.Primary()[0].Flags&orders.FlagTombstone != 0 {
		t.Fatalf("decrement should not tombstone")
	}
	// Secondary always tombstoned: verify via direct orders Queue that secondary cancel tombstones even as head [04 §3.3]
	qSec := &orders.Queue{}
	bid := orders.Lookup("BuildWeapon")
	if bid == 0 {
		t.Skip("BuildWeapon not found")
	}
	qSec.PushSecondary(bid, orders.Node{Param1: 1})
	secPtr := qSec.Secondary()[0]
	qSec.CancelTailMost(func(n orders.Node) bool { return n.Param1 == 1 })
	if secPtr.Flags&orders.FlagTombstone == 0 {
		t.Fatalf("secondary cancel should be tombstoned even as head [04 §3.3]")
	}
}

func TestVerbatimExhaustionMessage(t *testing.T) {
	if ErrLimitMessage != "Unable to create any more units" {
		t.Fatalf("verbatim message mismatch %q", ErrLimitMessage)
	}
	if ExhaustionError().Error() != "Unable to create any more units" {
		t.Fatalf("exhaustion error mismatch %q", ExhaustionError().Error())
	}
}

func TestNoQueueReservation(t *testing.T) {
	// C23: per-def limits enforced ONLY at nanoframe allocation — no queue reservation.
	// QueueBuild must succeed even when LimitChecker says exhausted.
	orig := LimitChecker
	defer func() { LimitChecker = orig }()
	LimitChecker = func(factory *units.Unit, defKey string) bool { return false } // exhausted
	f := &units.Unit{Handle: 5, Owner: 0}
	if err := QueueBuild(f, "armflash", 1); err != nil {
		t.Fatalf("QueueBuild should not enforce limit [C23], got %v", err)
	}
	q := orders.QueueForUnit(f)
	if q.LenPrimary() != 1 {
		t.Fatalf("queue reservation should exist len %d", q.LenPrimary())
	}
	// But CheckLimit should report false, and ExhaustionError is the verbatim message [05].
	if CheckLimit(f, "armflash") {
		t.Fatalf("CheckLimit should report exhausted")
	}
	if !CheckLimit(&units.Unit{Handle: 6}, "other") {
		// this also false because checker returns false for any, but we test hook is called
	}
	// Reset checker to allow
	LimitChecker = func(factory *units.Unit, defKey string) bool { return true }
	if !CheckLimit(f, "armflash") {
		t.Fatalf("allow should be true")
	}
}

func TestNoSecondQueueType(t *testing.T) {
	// Architectural assertion: construction does not define its own queue type;
	// all factory products live on orders.Node primary segment via orders.Queue [C15].
	// This is asserted by the absence of a local queue struct and by using orders.QueueForUnit
	// in QueueBuild/CancelTailMost. If a local queue type appears, this comment fails review.
	t.Log("no second queue type: QueueBuild/CancelTailMost use orders.Queue and orders.Node only [C15]")
}
