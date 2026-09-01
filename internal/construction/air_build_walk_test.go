package construction

import (
	"testing"
)

// TestAConstructionAircraftNeverNeedsAWalk locks the whole air half of the
// walk predicate against [04 R-ORD-02 §2], whose `VTOL_MobileBuild` row ends:
// "There is no nanolathe-active stamp and no reach test after arrival: an
// aircraft that reached its builddistance marker builds from wherever the
// 150-tick orbit leaves it."
//
// Both terms of the predicate belong to the ground twin's phase-0 RECTANGLE
// goal on the product footprint [04 R-ORD-01 §5], and an aircraft installs a
// point marker instead. The reach term was already gated on `canfly`; the
// clear-the-site term was not, and an aircraft's own arrival marker puts it
// directly over the site — so it answered yes, the session's walk arm emitted
// status 7 `I can't reach the construction site`, and the record was abandoned
// on open flat ground.
//
// The fixture is the ground one that already proves a builder covering its own
// site must walk out of it, with the builder's definition flipped to an
// aircraft: the site overlap is identical, so a pass here is the `canfly` gate
// and nothing else.
func TestAConstructionAircraftNeverNeedsAWalk(t *testing.T) {
	svc, builder, node := approachFixture(t, 4, 4)

	if !svc.mustClearSite(builder, node) {
		t.Fatal("fixture is not exercising the defect: the builder does not cover its own site")
	}
	if !svc.NeedsWalk(builder, node) {
		t.Fatal("a GROUND builder standing inside its own site must still walk out of it [04 R-ORD-01 §5]")
	}

	builder.Def.CanFly = true
	if svc.NeedsWalk(builder, node) {
		t.Fatal("a construction aircraft has no reach test and no clear-the-site term [04 R-ORD-02 §2]")
	}
}
