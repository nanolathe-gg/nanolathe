package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// Removing a HelpBuild record delivers cancel-current before any phase body;
// the cancellation cannot contribute work or replay the completion cue
// [04 R-ORD-01 §5][04 R-ORDER-02 §2].
func TestHelpBuildCancellationSkipsWorkAndCompletion(t *testing.T) {
	for _, phase := range []uint8{2, 3, 4} {
		t.Run(string(rune('0'+phase)), func(t *testing.T) {
			q, builder, target := workFixture()
			target.Remaining = 0.5
			builder.InBuildStance = false
			q.Push(Lookup("HelpBuild"), Node{Owner: builder.Handle, Target: target.Handle})
			n := q.Primary()[0]
			n.Phase, n.DynamicGate, n.Deadline = phase, gateCancelCurrent, -1
			var work, spray, cues int
			q.binding.Work.Assist = func(*units.Unit, *Node, uint32) bool { work++; target.Remaining = 0; return true }
			q.binding.Presentation = &PresentationAdapter{
				Nanolathe: func(*units.Unit, *Node, uint32) bool { spray++; return true },
				Status:    func(*units.Unit, uint8, string) bool { cues++; return true },
			}
			if code := helpBuildHandler(builder, n, gateCancelCurrent, 100); code != 5 {
				t.Errorf("cancel result=%d, want complete", code)
			}
			q.PurgeUnprotected()
			if q.LenPrimary() != 0 || work != 0 || spray != 0 || cues != 0 || target.Remaining != 0.5 || builder.RevealDeadline != 0 {
				t.Fatalf("cancel replayed phase %d: queue=%d work=%d spray=%d cues=%d remaining=%v reveal=%d", phase, q.LenPrimary(), work, spray, cues, target.Remaining, builder.RevealDeadline)
			}
		})
	}
}
