package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// TestStockpileClicksAreACountedProducer locks the stockpile arm of the counted
// build-page producer [07 R-P0-11 §1]. A positive count coalesces into the
// matching rear-segment BUILDWEAPON record's count field; a negative count
// consumes the tail-most matching record, subtracting in place while it holds
// more than the remaining magnitude and unlinking it otherwise; subtraction
// past zero stops at an empty queue rather than leaving a negative count; and a
// subtraction with no record at all changes nothing. The producer never purges,
// so no click removes an unrelated record.
func TestStockpileClicksAreACountedProducer(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "silo", MaxDamage: 100}
	def.CanonicalKey = "silo"
	cat.Units[def.CanonicalKey] = def
	w := newSessionFixtureWorld(8, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	u.SlotAt(0).Weapon = &content.WeaponDef{ID: 1, ReloadTime: 30, Stockpile: true}

	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	tick := uint32(1)
	click := func(count int) {
		t.Helper()
		if err := s.EnqueueHumanCommand(HumanCommand{
			Kind:      HumanStockpile,
			Stockpile: HumanStockpileCommand{Unit: h, Count: count},
		}); err != nil {
			t.Fatalf("enqueue %+d: %v", count, err)
		}
		s.applyHumanCommands(tick)
		tick++
	}
	// The rear segment holds at most this one BUILDWEAPON record throughout:
	// the count is the queue modifier, so a second click coalesces rather than
	// stacking a second record.
	rounds := func(what string) int {
		t.Helper()
		q := orders.QueueForUnit(u)
		if q == nil || q.LenSecondary() == 0 {
			return 0
		}
		if q.LenSecondary() != 1 {
			t.Fatalf("%s: rear segment holds %d records, want one coalesced BUILDWEAPON", what, q.LenSecondary())
		}
		n := q.Secondary()[0]
		if orders.DescriptorFor(n.ID).Name != "BuildWeapon" {
			t.Fatalf("%s: rear-segment record is %q, want BuildWeapon [04 §3.1]", what, orders.DescriptorFor(n.ID).Name)
		}
		if n.Param1 != 0 {
			t.Fatalf("%s: record slot = %d, want the alias's 0 [06 §11.1]", what, n.Param1)
		}
		return int(n.Param2)
	}

	click(5) // Shift+left
	if got := rounds("Shift+left"); got != 5 {
		t.Fatalf("Shift+left queued %d rounds, want 5", got)
	}
	click(5) // a second Shift+left coalesces into the same record
	if got := rounds("second Shift+left"); got != 10 {
		t.Fatalf("a second Shift+left left %d rounds, want 10 coalesced", got)
	}
	click(-1) // right click
	if got := rounds("right click"); got != 9 {
		t.Fatalf("right click left %d rounds, want 9", got)
	}
	click(-5) // Shift+right
	if got := rounds("Shift+right"); got != 4 {
		t.Fatalf("Shift+right left %d rounds, want 4", got)
	}
	// Past zero: the record is unlinked with the remainder discarded. The
	// count field never goes negative and the queue is simply empty.
	click(-5)
	if got := rounds("subtraction past zero"); got != 0 {
		t.Fatalf("subtraction past zero left %d rounds, want an empty rear segment", got)
	}
	// With no record to consume, a right click changes nothing. (It is still
	// audible: the cue precedes the routing, which is the click site's half of
	// the contract.)
	click(-1)
	if got := rounds("subtraction with no record"); got != 0 {
		t.Fatalf("subtraction with no record left %d rounds, want none", got)
	}
	// And the producer resumes from empty.
	click(1)
	if got := rounds("left click from empty"); got != 1 {
		t.Fatalf("left click from empty queued %d rounds, want 1", got)
	}
}

// TestStockpileSubtractionLeavesOtherRearRecords proves the negative arm is a
// match on the BUILDWEAPON descriptor and its build-type operand, not a blanket
// rear-segment cancel: the producer never purges [07 R-P0-11 §1].
func TestStockpileSubtractionLeavesOtherRearRecords(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "silo", MaxDamage: 100}
	def.CanonicalKey = "silo"
	cat.Units[def.CanonicalKey] = def
	w := newSessionFixtureWorld(8, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	u.SlotAt(0).Weapon = &content.WeaponDef{ID: 1, ReloadTime: 30, Stockpile: true}

	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	if err := s.EnqueueHumanCommand(HumanCommand{
		Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: h, Count: 1},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	s.applyHumanCommands(1)

	// SelfDestruct is the other rear-segment descriptor [04 §3.1]; a stockpile
	// right click must leave it alone.
	sd := orders.Lookup("SelfDestruct") // the rear-segment row, not the front-segment variant
	if sd == 0 {
		t.Skip("no SelfDestruct descriptor in this table")
	}
	q := orders.QueueForUnit(u)
	if q == nil {
		t.Fatal("the enqueue bound no queue")
	}
	q.Push(sd, orders.NewNodeForOrder(sd, 0, 0, 0, 0, 2, u.Handle, true))
	before := q.LenSecondary()

	if err := s.EnqueueHumanCommand(HumanCommand{
		Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: h, Count: -5},
	}); err != nil {
		t.Fatalf("enqueue subtraction: %v", err)
	}
	s.applyHumanCommands(3)

	if got := q.LenSecondary(); got != before-1 {
		t.Fatalf("rear segment holds %d records after the subtraction, want %d", got, before-1)
	}
	for _, n := range q.Secondary() {
		if orders.DescriptorFor(n.ID).Name == "BuildWeapon" {
			t.Fatal("the subtraction left a BUILDWEAPON record behind")
		}
	}
	found := false
	for _, n := range q.Secondary() {
		if n.ID == sd {
			found = true
		}
	}
	if !found {
		t.Fatal("the stockpile subtraction consumed the SelfDestruct record; the counted producer never purges [07 R-P0-11 §1]")
	}
}
