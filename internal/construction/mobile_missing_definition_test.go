package construction

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// TestMobileBuildUnresolvableProductFailsLikeTheFactoryTwin locks the mobile
// build row to the same answer its factory twin gives for a product the
// catalog cannot resolve.
//
// Retail has no arm to copy: the record carries a product definition INDEX and
// the `MobileBuild` body indexes the definition table with it and no bounds
// test, so the load cannot yield nothing [04 R-ORD-01 §5][04 R-ORD-01 §18].
// The only handler-level refusals that row describes are the blocked-area
// budget [R-ORDER-02 §1] and the allocator's `Unable to create any more units`
// hold. A definition guard is therefore Nanolathe's own, and handleState2
// already settled its shape: retain a permanent-definition admission
// diagnostic and invent no queue transition. The mobile path used to arm a
// silent 15-tick retry instead, so a catalog mismatch span the record forever
// with nothing recorded anywhere.
func TestMobileBuildUnresolvableProductFailsLikeTheFactoryTwin(t *testing.T) {
	for _, name := range []string{MobileBuildOrder, VTOLMobileBuildOrder} {
		t.Run(name, func(t *testing.T) {
			svc, builder, node := approachFixture(t, 12, 12)
			node.ID = orders.Lookup(name)
			node.Phase, node.DynamicGate, node.Deadline = uint8(State2), 0, -1
			builder.Def.CanFly = name == VTOLMobileBuildOrder
			// Name a product no catalog row resolves, and clear the index
			// fallback so getProductDefForNode really returns nothing.
			node.BuildDefKey, node.Param1 = "nosuchproduct", 0

			svc.handleMobileState2(builder, node, 100)

			diagnostics := svc.AdmissionDiagnostics()
			if len(diagnostics) != 1 {
				t.Fatalf("admission diagnostics = %d, want the one permanent-definition rejection", len(diagnostics))
			}
			got := diagnostics[0]
			if got.Status != AdmissionRejectedPermanentDefinition {
				t.Fatalf("admission status = %v, want AdmissionRejectedPermanentDefinition", got.Status)
			}
			if got.Builder != builder.Handle || got.Product != "nosuchproduct" {
				t.Fatalf("admission builder=%d product=%q, want %d/%q", got.Builder, got.Product, builder.Handle, "nosuchproduct")
			}
			if !strings.Contains(got.Reason, "placement definition unavailable") {
				t.Fatalf("admission reason = %q, want the missing-definition error", got.Reason)
			}

			// No invented transition: the record neither arms a retry nor
			// leaves the state it was visited in.
			if node.Deadline != -1 || node.DynamicGate != 0 {
				t.Fatalf("unresolvable product armed a retry: deadline=%d gate=%d", node.Deadline, node.DynamicGate)
			}
			if node.Phase != uint8(State2) {
				t.Fatalf("unresolvable product moved the record to phase %d, want state 2", node.Phase)
			}
			if q := orders.QueueForUnit(builder); q.LenPrimary() != 1 {
				t.Fatalf("unresolvable product changed the queue length to %d", q.LenPrimary())
			}
			// It is a definition failure, not a placement one: no caption and
			// no lifecycle message belongs to it.
			if msgs := svc.Messages(); len(msgs) != 0 {
				t.Fatalf("unresolvable product logged %v", msgs)
			}
		})
	}
}
