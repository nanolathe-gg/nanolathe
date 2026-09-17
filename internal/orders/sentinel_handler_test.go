package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Descriptor row 0 carries a handler, and its body is "return complete (5),
// touch nothing, draw nothing" [04 R-ORD-01 §12].
//
// No production path builds a row-0 record — every producer tests the resolved
// identity and skips on 0 — so this locks the guard rather than a reachable
// behavior. What it rules out is the dispatch falling to the pump's no-handler
// arm, which parks the record for 30..44 ticks and draws RNG(15) to do it: a
// simulation-stream draw retail never makes.
func TestSentinelRowCompletesWithoutDrawing(t *testing.T) {
	if DescriptorFor(0).Handler == nil {
		t.Fatal("row 0 carries no handler; the pump would park a sentinel record and draw RNG(15)")
	}
	q, u := standingFixture(&content.UnitDef{})
	// A record ahead of it proves the sentinel is completed on the head reload
	// of the pass that reaches it, not merely on the pass it was inserted in.
	lead := q.PushHead(0, Node{Owner: u.Handle, Deadline: -1})
	head := q.PushHead(Lookup("Wait"), Node{Owner: u.Handle, Phase: 5, Deadline: -1})
	q.SetOwnedHandler(head.ID, func(_ *units.Unit, rec *Node, _ uint32, _ uint32) (Code, bool) {
		if rec != head {
			t.Fatalf("the sentinel reached the head under the wrong handler")
		}
		return 5, true // complete, exposing the sentinel behind it in the same pass
	})
	before := q.binding.SimRNG.Draws()
	q.Pump(u, 100)
	if len(q.primary) != 0 {
		t.Fatalf("primary segment holds %d records, want the sentinel freed in the same pass", len(q.primary))
	}
	if q.indexOfPrimary(lead) >= 0 {
		t.Fatal("the sentinel record survived its dispatch")
	}
	if got := q.binding.SimRNG.Draws() - before; got != 0 {
		t.Errorf("simulation draws = %d, want 0: the sentinel completes without arming a wait", got)
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics = %v, want none: row 0 is handled, not unimplemented", diags)
	}
}
