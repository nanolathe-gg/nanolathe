package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// siteRectForNode resolves the product rectangle a MOBILEBUILD node names.
func siteRectForNode(t *testing.T, s *Service, node *orders.Node) world.FootprintRect {
	t.Helper()
	anchorX, anchorZ, footX, footZ, ok := s.siteAnchorCell(node)
	if !ok {
		t.Fatal("site anchor unresolved for a queued MOBILEBUILD node")
	}
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		t.Fatalf("extent: %v", err)
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(anchorX, anchorZ), extent)
	if err != nil {
		t.Fatalf("rect: %v", err)
	}
	return rect
}

func liveUnitCount(w *units.World) int {
	n := 0
	for _, u := range w.Iter() {
		if u != nil && u.Alive {
			n++
		}
	}
	return n
}

// TestMobileBuilderCannotStampOverItself is the PT5 lock. A mobile builder
// whose own footprint covers the site must not allocate a nanoframe there.
//
// Retail keeps ONE occupancy word per cell, written by ground movers and by
// building-class units alike [04 R-COLL-01 §4], and the footprint validator
// rejects "any nonzero occupant other than the passed self identity"
// [05 "control-byte bit roles in the footprint validator"][04 R-COLL-01 §2].
// Every placement caller passes a null self identity — the eleven-caller
// census finds no exemption for a builder [04 R-COLL-01 §6] — so the builder's
// own occupancy blocks its own site. The order then takes the blocked-area
// budget: the first blocked visit notifies `Waiting for target area to clear`,
// increments the record's third parameter and waits exactly 30 ticks
// [R-ORDER-02 §1][04 §3.2].
//
// Before this lock the builder was invisible to the validator (only
// construction wrote the plot half of the split word), so a queued field of
// solar collectors eventually stamped a structure onto the commander building
// it and the commander could no longer path out.
func TestMobileBuilderCannotStampOverItself(t *testing.T) {
	// Site anchor (1,1) with a 6x6 product; the 2x2 builder anchors at (1,1).
	svc, builder, node := approachFixture(t, 4, 4)

	if !svc.mustClearSite(builder, node) {
		t.Fatal("fixture is not exercising the defect: the builder does not cover its own site")
	}
	if !svc.NeedsWalk(builder, node) {
		t.Fatal("a builder standing inside its own site must still walk out of it [04 §7.2][R-ORD-01 §5]")
	}

	def := svc.getProductDefForNode(node)
	if def == nil {
		t.Fatal("product definition unresolved")
	}
	rect := siteRectForNode(t, svc, node)
	yard, err := world.ParseYardMap(def.YardMap, int(rect.Width()), int(rect.Depth()))
	if err != nil {
		t.Fatalf("yardmap: %v", err)
	}
	if _, err := svc.validatePlacement(builder.Handle, rect, def, yard, false); err == nil {
		t.Fatal("the validator accepted a site the builder itself occupies [04 R-COLL-01 §2]")
	}

	var texts []string
	svc.StatusText = func(text string) { texts = append(texts, text) }
	before := liveUnitCount(svc.World)
	node.Deadline = -1
	// The blocked-area budget belongs to the phase AFTER the approach: phase 1
	// is dispatched only on a movement outcome, and a visit carrying `0x20`
	// retires the approach with no distance test and falls straight into the
	// placement validator [05 R-WORK-01 §13]. Raise the arrival the follower
	// would have raised so this visit is that visit; without it the record is
	// still waiting for the mover and never reaches the validator.
	node.Satisfied |= 0x20
	svc.handleMobileState2(builder, node, 1)
	if got := liveUnitCount(svc.World); got != before {
		t.Fatalf("a nanoframe was allocated over the builder: %d live units, want %d", got, before)
	}
	if len(texts) != 1 || texts[0] != orders.MobileBuildWaitingText {
		t.Fatalf("blocked visit captions = %v, want exactly %q [R-ORDER-02 §1]", texts, orders.MobileBuildWaitingText)
	}
	if node.Param3 != 1 {
		t.Fatalf("blocked-area retry counter = %d, want 1 [04 §3.2]", node.Param3)
	}
	if node.Deadline != 31 {
		t.Fatalf("blocked visit deadline = %d, want tick 1 + exactly 30 [R-ORDER-02 §1]", node.Deadline)
	}
}

// TestClearSiteValidatesWhenTheBuilderStandsOff is the other half of the lock:
// the same fixture with the builder off the rectangle validates, so the
// rejection above is the occupant test and not a terrain or content failure.
func TestClearSiteValidatesWhenTheBuilderStandsOff(t *testing.T) {
	svc, builder, node := approachFixture(t, 12, 12)

	if svc.mustClearSite(builder, node) {
		t.Fatal("control fixture must place the site clear of the builder")
	}
	def := svc.getProductDefForNode(node)
	if def == nil {
		t.Fatal("product definition unresolved")
	}
	rect := siteRectForNode(t, svc, node)
	yard, err := world.ParseYardMap(def.YardMap, int(rect.Width()), int(rect.Depth()))
	if err != nil {
		t.Fatalf("yardmap: %v", err)
	}
	if _, err := svc.validatePlacement(builder.Handle, rect, def, yard, false); err != nil {
		t.Fatalf("a site clear of the builder must validate, got %v", err)
	}
}
