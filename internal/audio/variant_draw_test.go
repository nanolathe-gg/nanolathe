package audio

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

// TestEmptyRowStillConsumesVariantDraw locks the ordering of [03 §8.3] step 2:
// the variant draw happens on EVERY resolve, before any gate and before the
// variant count is consulted, so the CRT stream advances with queue pops rather
// than with audible successes. A row with no variants produces no pick but
// still costs its draw.
//
// This is worth a test because skipping the draw is invisible locally and
// shifts every later variant pick by one — and because the empty row is the
// common case: the reference install authors no `load`, `unload` or `repair`
// variant in any of its categories, so a queue that skipped the draw would
// desynchronize from retail within the first few cues of a battle.
func TestEmptyRowStillConsumesVariantDraw(t *testing.T) {
	cat := &Category{Name: "emptyrow"}
	cat.Rows[1].Variants = []string{"sel_a", "sel_b"} // select: two variants
	// Slot 10 (`repair`) deliberately has no variants at all.

	q := NewQueue()
	q.Register(1, cat, "U", true)
	q.Seed(4242)

	draws := func() uint64 {
		if r := q.CRTRandom(); r != nil {
			return r.Draws()
		}
		t.Fatal("queue has no CRT stream bound")
		return 0
	}

	var played []string
	q.OnPlay(func(alias string, _ Slot, _ pool.Handle) { played = append(played, alias) })

	before := draws()
	q.InsertAt(0, 10, 1, "")
	q.Drain(30)
	if got := draws() - before; got != 1 {
		t.Fatalf("resolving an empty row consumed %d draws, want 1 [03 §8.3]", got)
	}
	if len(played) != 0 {
		t.Fatalf("an empty row must produce no pick, played %v [03 §8.3]", played)
	}
}
