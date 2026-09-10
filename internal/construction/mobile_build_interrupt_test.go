package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// Both mobile build rows abandon on target removal and complete on cancellation;
// only BuildingBuild restarts/refunds/kills [04 R-ORD-01 §5][04 R-ORD-02 §2].
func TestMobileBuildInterruptsDoNotUseFactoryEpilogues(t *testing.T) {
	for _, name := range []string{MobileBuildOrder, VTOLMobileBuildOrder} {
		for _, cancel := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/removed", true: "/canceled"}[cancel], func(t *testing.T) {
				svc, builder, product, node := mobileBuildCompletionFixture(t, 3)
				node.ID = orders.Lookup(name)
				node.Phase, node.DynamicGate = uint8(State3), InterruptCancel|InterruptStop
				product.Remaining = 0.5
				q := orders.QueueForUnit(builder)
				svc.RegisterOrderHandlers(q)
				q.Binding().Work = &orders.WorkAdapter{CancelNotice: svc.DeliverCancelNotice}
				var statuses []string
				q.Binding().Presentation = &orders.PresentationAdapter{Status: func(_ *units.Unit, _ uint8, text string) bool { statuses = append(statuses, text); return true }}
				if cancel {
					q.PurgeUnprotected()
				} else {
					if !svc.NotifyProductRemoved(product.Handle) {
						t.Fatal("missing removal notice")
					}
					q.Pump(builder, 100)
				}
				if q.LenPrimary() != 0 || node.Param2 != 3 {
					t.Fatalf("interrupt kept/recounted mobile build: length=%d count=%d", q.LenPrimary(), node.Param2)
				}
				if product.Dying || product.Remaining != 0.5 || svc.LastKill().Damage != 0 {
					t.Fatalf("interrupt applied factory completion/kill: dying=%v remaining=%v packet=%+v", product.Dying, product.Remaining, svc.LastKill())
				}
				if buckets := svc.Economy.UnitBuckets(builder.Handle); buckets != nil && buckets[economy.Metal].Production != 0 {
					t.Fatalf("mobile cancel refunded metal: %v", buckets[economy.Metal].Production)
				}
				if !cancel && (len(statuses) != 1 || statuses[0] != "Construction terminated") {
					t.Fatalf("removed status=%v", statuses)
				}
			})
		}
	}
}
