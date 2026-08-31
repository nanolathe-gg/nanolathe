package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestP0I16_HostilityIsolation(t *testing.T) {
	// Two actors with same side but different queue hostility
	defA := &content.UnitDef{Side: "ARM"}
	defB := &content.UnitDef{Side: "ARM"}
	u1 := &units.Unit{Handle: 1, Owner: 0, Def: defA, Alive: true}
	u2 := &units.Unit{Handle: 2, Owner: 0, Def: defA, Alive: true}
	target := &units.Unit{Handle: 3, Owner: 0, Def: defB, Alive: true}
	// Queue for u1 says hostile always true, for u2 always false
	q1 := QueueForUnit(u1)
	q2 := QueueForUnit(u2)
	hostile := func(a, b *units.Unit) bool { return true }
	q1.SetBinding(&QueueBinding{Hostility: hostile})
	q2.SetBinding(&QueueBinding{Hostility: func(a, b *units.Unit) bool { return false }})

	if !isHostile(u1, target) {
		t.Fatalf("q1 hostility true should be hostile")
	}
	if isHostile(u2, target) {
		t.Fatalf("q2 hostility false should not be hostile")
	}
	// Interleaved
	if !isHostile(u1, target) || isHostile(u2, target) {
		t.Fatalf("interleaved hostility contaminated")
	}
	// Save/reload: copy hostility
	saved := q1.Binding().Hostility
	q1Dup := &Queue{}
	q1Dup.SetBinding(&QueueBinding{Hostility: func(a, b *units.Unit) bool { return false }})
	// Destroy q1Dup, reload
	reloadedQ1 := &Queue{}
	reloadedQ1.SetBinding(&QueueBinding{Hostility: saved})
	// Need to test via a unit that uses reloaded queue
	u1Dup := &units.Unit{Handle: 4, Owner: 0, Def: defA, Alive: true}
	BindQueue(u1Dup, reloadedQ1)
	if !isHostile(u1Dup, target) {
		t.Fatalf("reloaded hostility should still be true")
	}
	_ = pool.Handle(0)
}

func TestP0I16_TargetLookupIsolation(t *testing.T) {
	uA := &units.Unit{Handle: 10, Owner: 0, Alive: true}
	uB := &units.Unit{Handle: 11, Owner: 0, Alive: true}
	targetA := &units.Unit{Handle: 100, Owner: 1, Alive: true}
	targetB := &units.Unit{Handle: 200, Owner: 1, Alive: true}
	qA := QueueForUnit(uA)
	qB := QueueForUnit(uB)
	qA.SetBinding(&QueueBinding{Lookup: func(h pool.Handle) *units.Unit {
		if h == 100 {
			return targetA
		}
		return nil
	}})
	qB.SetBinding(&QueueBinding{Lookup: func(h pool.Handle) *units.Unit {
		if h == 200 {
			return targetB
		}
		return nil
	}})
	nA := &Node{Target: 100}
	nB := &Node{Target: 200}
	if got := getLookupForWard(nA, uA); got != targetA {
		t.Fatalf("qA lookup should return targetA")
	}
	if got := getLookupForWard(nB, uB); got != targetB {
		t.Fatalf("qB lookup should return targetB")
	}
	// Cross contamination check
	if got := getLookupForWard(nA, uB); got != nil {
		t.Fatalf("uB lookup for 100 should be nil, got %v", got)
	}
	if got := getLookupForWard(nB, uA); got != nil {
		t.Fatalf("uA lookup for 200 should be nil")
	}
}
