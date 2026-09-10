package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"testing"
)

// Ground mobile allocation refusal retries after 300 ticks, while its air
// twin abandons and releases the queue [04 R-ORD-01 §5][04 R-ORD-02 §2].
func TestMobileAllocationRefusalUsesTheRowsOwnResult(t *testing.T) {
	for _, name := range []string{MobileBuildOrder, VTOLMobileBuildOrder} {
		t.Run(name, func(t *testing.T) {
			svc, builder, node := approachFixture(t, 12, 12)
			node.ID = orders.Lookup(name)
			node.Phase, node.DynamicGate = uint8(State2), 0
			builder.Def.CanFly = name == VTOLMobileBuildOrder
			def := svc.getProductDefForNode(node)
			def.LimitEnabled, def.Limit = true, 0
			svc.handleMobileState2(builder, node, 100)
			q := orders.QueueForUnit(builder)
			if got := svc.Messages(); len(got) != 1 || got[0] != ErrLimitMessage {
				t.Fatalf("allocation refusal messages=%v", got)
			}
			if name == VTOLMobileBuildOrder {
				if q.LenPrimary() != 0 {
					t.Fatalf("air allocation refusal kept queue=%d deadline=%d", q.LenPrimary(), node.Deadline)
				}
			} else if q.LenPrimary() != 1 || node.Deadline != 400 {
				t.Fatalf("ground allocation retry queue=%d deadline=%d", q.LenPrimary(), node.Deadline)
			}
		})
	}
}
