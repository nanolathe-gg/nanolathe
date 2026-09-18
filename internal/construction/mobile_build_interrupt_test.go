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

// TestMobileBuildPreCheckBitOrder locks the order of the two pre-check bits in
// the two mobile build handlers [04 §5][04 R-ORD-02 §2]: `MobileBuild` tests
// bit 3 first and bit 1 second — the opposite of `BuildingBuild`'s per-visit
// tree — so a visit carrying both bits abandons with `Construction terminated`
// (code 8) and never reaches the cancel-current arm; `VTOL_MobileBuild`
// follows `BuildingBuild` instead, so the same visit completes silently
// (code 5).
func TestMobileBuildPreCheckBitOrder(t *testing.T) {
	cases := []struct {
		name      string
		row       string
		satisfied uint32
		wantCode  orders.Code
		wantText  []string
	}{
		{"ground/stop", MobileBuildOrder, InterruptStop, 8, []string{"Construction terminated"}},
		{"ground/cancel", MobileBuildOrder, InterruptCancel, 5, nil},
		{"ground/both", MobileBuildOrder, InterruptCancel | InterruptStop, 8, []string{"Construction terminated"}},
		{"air/stop", VTOLMobileBuildOrder, InterruptStop, 8, []string{"Construction terminated"}},
		{"air/cancel", VTOLMobileBuildOrder, InterruptCancel, 5, nil},
		{"air/both", VTOLMobileBuildOrder, InterruptCancel | InterruptStop, 5, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, builder, _, node := mobileBuildCompletionFixture(t, 1)
			node.ID = orders.Lookup(tc.row)
			q := orders.QueueForUnit(builder)
			var statuses []string
			q.Binding().Presentation = &orders.PresentationAdapter{Status: func(_ *units.Unit, _ uint8, text string) bool {
				statuses = append(statuses, text)
				return true
			}}
			code, handled := svc.mobileBuildInterrupt(builder, node, tc.satisfied)
			if !handled || code != tc.wantCode {
				t.Fatalf("%s satisfied %#x: code=%d handled=%v, want %d/true", tc.row, tc.satisfied, code, handled, tc.wantCode)
			}
			if len(statuses) != len(tc.wantText) {
				t.Fatalf("%s satisfied %#x: statuses %v, want %v", tc.row, tc.satisfied, statuses, tc.wantText)
			}
			for i, want := range tc.wantText {
				if statuses[i] != want {
					t.Fatalf("%s satisfied %#x: status %d = %q, want %q", tc.row, tc.satisfied, i, statuses[i], want)
				}
			}
		})
	}
	// Neither bit: the row is not this handler's to terminate.
	svc, builder, _, node := mobileBuildCompletionFixture(t, 1)
	if code, handled := svc.mobileBuildInterrupt(builder, node, 0); handled || code != 0 {
		t.Fatalf("no pre-check bit: code=%d handled=%v, want 0/false", code, handled)
	}
}
