package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A Shift-click that removes a queued build site must not move the queue's
// insertion point, and must not touch any surviving order's anchor.
//
// Both halves regressed together and were reported from a play-test: after
// removing a queued order, the next queued order landed near the FRONT of the
// remaining queue, and a surviving order's overlay marker jumped vertically.
//
//   - Ordering. [04 §3.3]'s runtime-bit census gives the active marker exactly
//     one writer, the producer insertion's after-marker branch, so removing the
//     record that held it leaves the segment unmarked and [04 §3.1]'s insertion
//     rule appends the next record "at the tail". The removal used to hand the
//     marker to the head instead, which put the next order at index 1.
//   - Anchors. The record a producer creates is only the tail when the marker
//     was on the tail, so stamping the click's site height onto the tail wrote
//     one queued site's height onto a different queued order — the vertical
//     jump. The overlay projects the build marker from the order's own GoalY
//     [07 R-P0-11 §3], so a clobbered GoalY is directly a moved marker.
//
// The removal itself is [07 R-P0-11 §6]'s queued-order toggle: a queued click
// within one cell of an already-queued site of the same order kind removes it
// and issues nothing.
func TestQueuedBuildRemovalKeepsInsertionPointAndAnchors(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	product := &content.UnitDef{UnitName: "solar", MaxDamage: 10}
	product.CanonicalKey = "solar"
	cat.Units[product.CanonicalKey] = product
	bdef := &content.UnitDef{UnitName: "builder", Builder: true, CanMove: true, MaxDamage: 10}
	bdef.CanonicalKey = "builder"
	cat.Units[bdef.CanonicalKey] = bdef
	w := newSessionFixtureWorld(8, cat)
	hb, _ := w.Create(bdef, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}

	fx := func(v int) numeric.Fixed { return numeric.Fixed(v) << 16 }
	// Distinct heights per site: the height is what the overlay marker's screen
	// Y is derived from, so a swapped or dropped one is visible here.
	click := func(cx, cy, cz, atTick int) {
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
			Builder: hb, Product: "solar", Queued: true,
			WX: fx(cx), WY: fx(cy), WZ: fx(cz),
		}}); err != nil {
			t.Fatal(err)
		}
		s.applyHumanCommands(uint32(atTick))
	}
	type site struct{ x, y, z int }
	sites := func() []site {
		q := orders.QueueForUnit(w.Unit(hb))
		out := make([]site, 0, q.LenPrimary())
		for _, n := range q.Primary() {
			out = append(out, site{int(n.GoalX >> 16), int(n.GoalY >> 16), int(n.GoalZ >> 16)})
		}
		return out
	}
	same := func(got []site, want ...site) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	a, b, c, d := site{100, 11, 100}, site{200, 22, 200}, site{300, 33, 300}, site{400, 44, 400}
	click(a.x, a.y, a.z, 10)
	click(b.x, b.y, b.z, 11)
	click(c.x, c.y, c.z, 12)
	if got := sites(); !same(got, a, b, c) {
		t.Fatalf("three queued sites = %v, want %v", got, []site{a, b, c})
	}

	// Repeat the last click: [07 R-P0-11 §6]'s toggle removes it and issues
	// nothing. The survivors keep their own anchors.
	click(c.x, c.y, c.z, 13)
	if got := sites(); !same(got, a, b) {
		t.Fatalf("after removing the last queued site = %v, want %v", got, []site{a, b})
	}

	// The next queued order goes to the END, and carries its OWN site height.
	click(d.x, d.y, d.z, 14)
	if got := sites(); !same(got, a, b, d) {
		t.Fatalf("after re-queueing = %v, want %v (a removal must not move the insertion point or an anchor)", got, []site{a, b, d})
	}

	// Removing a middle order leaves the marker where it was, so the queue
	// still appends behind its last record.
	click(b.x, b.y, b.z, 15)
	if got := sites(); !same(got, a, d) {
		t.Fatalf("after removing the middle site = %v, want %v", got, []site{a, d})
	}
	prim := orders.QueueForUnit(w.Unit(hb)).Primary()
	marked := 0
	for _, n := range prim {
		if n.Flags&orders.FlagActive != 0 {
			marked++
		}
	}
	if marked > 1 {
		t.Fatalf("%d records carry the active marker; at most one may [04 §3.3]", marked)
	}

	// A queue whose marker is NOT on its last record still stamps the right
	// record. The pump's tail-rotate (primary result code 6) produces exactly
	// this shape — it moves the head behind a marked record — and there the
	// producer inserts mid-segment [04 §3.3], so the record it created is not
	// the tail. Move the marker by hand rather than driving a rotate: the point
	// under test is the stamp, not the rotate.
	for i, n := range prim {
		if i == 0 {
			n.Flags |= orders.FlagActive
		} else {
			n.Flags &^= orders.FlagActive
		}
	}
	f := site{600, 66, 600}
	click(f.x, f.y, f.z, 16)
	if got := sites(); !same(got, a, f, d) {
		t.Fatalf("insertion after a marker that is not the tail = %v, want %v (the click's site height belongs to the record the producer created)", got, []site{a, f, d})
	}
}
