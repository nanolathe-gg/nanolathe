package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The construction aircraft's approach phase [04 R-ORD-02 §2]: `VTOL_MobileBuild`
// phase 1 installs a point marker at the site with horizontal arrival radius
// `builddistance` and sets the gate to `0xE0`; phase 2 — placement and the
// nanoframe — is dispatched by that marker's outcome and abandons on `0x40`.
// The marker and gate are the air executor's (internal/movement); this
// service's phase 1 holds the record open and advances on the wake.
//
// Before this phase existed an aircraft advanced straight into placement on
// its first visit and stamped its nanoframe wherever it stood (play-test PT4).
// The whole-session behavior — the aircraft flying to within `builddistance`
// before the frame exists — is locked on the authored corpus by
// TestRetailConstructionAircraftFliesToTheSiteBeforeBuilding in
// internal/movement; the three arms below are the ones a synthetic fixture can
// reach without a flight.

func airApproachFixture(t *testing.T) (*Service, *units.Unit, *orders.Node) {
	t.Helper()
	svc, builder, node := approachFixture(t, 10, 10)
	builder.Def.CanFly = true
	return svc, builder, node
}

// TestAirApproachHoldsWithoutAGroundGoal: an aircraft's phase 1 submits no
// ground rectangle goal and does not fall through into placement on its first
// visit — the record stays in its approach until the executor's marker reports.
func TestAirApproachHoldsWithoutAGroundGoal(t *testing.T) {
	svc, builder, node := airApproachFixture(t)
	node.DynamicGate = 0
	svc.handleMobileApproach(builder, node, 1)

	if State(node.Phase) != State1 {
		t.Fatalf("phase %d after the first approach visit, want State1: an aircraft waits for its site marker [04 R-ORD-02 §2]", node.Phase)
	}
	if node.Target != 0 {
		t.Fatal("an aircraft must not create a product before its marker arrives [04 R-ORD-02 §2]")
	}
	if svc.Movement.HasGroundGoal(builder.Handle, node) {
		t.Fatal("an aircraft's approach installed a ground rectangle goal; the air leg's marker is the goal [04 R-ORD-02 §2]")
	}
	if node.DynamicGate != 0 {
		t.Fatalf("gate %#x written by the construction service, want the executor's own arming left alone [04 R-AIR-01 §6]", node.DynamicGate)
	}
	if !svc.needsApproach(builder, node) {
		t.Fatal("an aircraft in its approach phase must report that it is approaching")
	}
}

// TestAirApproachIgnoresTheClimbWake: the takeoff preamble's climb marker
// reports arrival on the same `0xE0` gate before the site marker is installed
// [04 R-AIR-01 §6]. With no site leg installed the wake is not the site's, and
// the phase must not advance on it.
func TestAirApproachIgnoresTheClimbWake(t *testing.T) {
	svc, builder, node := airApproachFixture(t)
	if svc.Movement.AirBuildSiteLegInstalled(builder, node) {
		t.Fatal("fixture: no air executor has run, so no site leg can be installed")
	}
	code, applied := svc.mobileBuildWakeVisit(builder, node, 0x20, 1)
	if applied || code != 0 {
		t.Fatalf("climb wake returned (%d, %v), want the no-advance form", code, applied)
	}
	if State(node.Phase) != State1 {
		t.Fatalf("phase %d after the climb wake, want State1 [04 R-ORD-02 §2]", node.Phase)
	}
}

// TestAirApproachAbandonsOnNoRouteWithoutACaption: `VTOL_MobileBuild` phase 2
// "satisfied `0x40` → abandon" — result 8, no reach expression, no caption; the
// ground twin's status 7 `I can't reach the construction site` is not raised
// [04 R-ORD-02 §2].
func TestAirApproachAbandonsOnNoRouteWithoutACaption(t *testing.T) {
	svc, builder, node := airApproachFixture(t)
	var captions int
	q := orders.QueueOfUnit(builder)
	binding := q.Binding()
	binding.Presentation = &orders.PresentationAdapter{
		Status: func(*units.Unit, uint8, string) bool {
			captions++
			return true
		},
	}
	q.SetBinding(binding)

	code, applied := svc.mobileBuildWakeVisit(builder, node, 0x40, 1)
	if !applied || code != 8 {
		t.Fatalf("no-route wake returned (%d, %v), want (8, true): abandon [04 R-ORD-02 §2]", code, applied)
	}
	if captions != 0 {
		t.Fatalf("%d captions raised on the air abandon, want none [04 R-ORD-02 §2]", captions)
	}
	if node.Target != 0 {
		t.Fatal("an abandoned air build must not create a product")
	}
}
