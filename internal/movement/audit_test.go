package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/world"
)

func auditFeature(name string, blocking bool, fx, fz int32) *content.FeatureDef {
	return &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		Blocking:         blocking,
		FootprintX:       fx,
		FootprintZ:       fz,
	}
}

func stampAuditFeature(t *testing.T, terrain *world.Terrain, def *content.FeatureDef, x, z int32) {
	t.Helper()
	terrain.FeatureDefs = append(terrain.FeatureDefs, def)
	idx := uint16(len(terrain.FeatureDefs) - 1)
	if err := terrain.StampFeatureRect(x, z, idx, def.FootprintX, def.FootprintZ); err != nil {
		t.Fatalf("stamp feature: %v", err)
	}
}

func TestAuditCellBlockingFeatureAndFringe(t *testing.T) {
	terrain := syntheticTerrain(8, 8, 0)
	def := auditFeature("tree1", true, 2, 2)
	def.Filename, def.SeqName = "trees.gaf", "tree1"
	stampAuditFeature(t, terrain, def, 2, 2)

	p := Template()
	a := AuditCell(terrain, p, Cell{X: 2, Z: 2}, AuditContext{
		StaticLayerValue:    LayerClear,
		HasStaticLayerValue: true,
	})
	if a.Profile.Verdict != AuditBlocked || !a.Feature.Resolved || !a.Feature.DefinitionResolved || !a.Feature.Blocking {
		t.Fatalf("anchor audit = %#v, want resolved blocking cell", a)
	}
	if a.Feature.Anchor != (Cell{X: 2, Z: 2}) || a.Feature.FootprintX != 2 || a.Feature.FootprintZ != 2 {
		t.Fatalf("feature geometry = %#v", a.Feature)
	}
	if a.Finding != FindingStampMismatch {
		t.Fatalf("finding = %v, want stamp mismatch", a.Finding)
	}

	fringe := AuditCell(terrain, p, Cell{X: 3, Z: 3}, AuditContext{})
	if !fringe.Feature.Resolved || fringe.Feature.ResolvedRef != a.Feature.ResolvedRef || fringe.Feature.Anchor != a.Feature.Anchor {
		t.Fatalf("fringe audit = %#v, want anchor resolution", fringe.Feature)
	}
}

func TestAuditCellNonblockingSpriteAndRenderedBounds(t *testing.T) {
	terrain := syntheticTerrain(5, 5, 0)
	def := auditFeature("decorative", false, 1, 1)
	def.Filename, def.SeqName = "scenery.gaf", "decor"
	stampAuditFeature(t, terrain, def, 1, 1)
	a := AuditCell(terrain, Template(), Cell{X: 1, Z: 1}, AuditContext{
		RenderedAnchor:    Cell{X: 1, Z: 1},
		HasRenderedAnchor: true,
		RenderedBounds:    AuditBounds{MinX: -1, MinZ: -1, MaxX: 3, MaxZ: 3, Valid: true},
		HasRenderedBounds: true,
	})
	if a.Profile.Verdict != AuditPass || a.Finding != FindingAuthoredNonblocking {
		t.Fatalf("audit verdict/finding = %v/%v", a.Profile.Verdict, a.Finding)
	}
	if !a.Feature.Sprite || a.Feature.SpriteAsset != "scenery.gaf:decor" {
		t.Fatalf("sprite evidence = %#v", a.Feature)
	}
	if !a.HasRenderedBounds || !a.RenderedBounds.Valid || a.RenderedBounds.MaxX != 3 {
		t.Fatalf("rendered evidence = %#v", a)
	}
	if a.Feature.FootprintX != 1 || a.RenderedBounds.MaxX-a.RenderedBounds.MinX == a.Feature.FootprintX {
		t.Fatalf("sprite bounds must remain distinct from blocking footprint: %#v", a)
	}
}

func TestAuditRuntimeWreckReclaim(t *testing.T) {
	terrain := syntheticTerrain(8, 8, 0)
	wreck := auditFeature("wreck", true, 2, 1)
	wreck.Reclaimable = true
	smudge := auditFeature("wreck-reclaimed", false, 1, 1)
	wreck.FeatureReclamateDef = smudge
	terrain.FeatureDefs = []*content.FeatureDef{wreck, smudge}
	svc := features.NewService(terrain, nil, nil, nil)
	inst := svc.PlaceAt(3, 3, wreck)
	if inst == nil {
		t.Fatal("runtime wreck was not placed")
	}
	before := AuditCell(terrain, Template(), Cell{X: 4, Z: 3}, AuditContext{})
	if !before.Feature.Resolved || before.Feature.Definition != "wreck" || !before.Feature.Blocking {
		t.Fatalf("wreck fringe audit = %#v", before)
	}
	svc.Reclaim(nil, inst, 1)
	after := AuditCell(terrain, Template(), Cell{X: 3, Z: 3}, AuditContext{})
	if !after.Feature.Resolved || after.Feature.Definition != "wreck-reclaimed" || after.Feature.Blocking {
		t.Fatalf("reclaimed successor audit = %#v", after)
	}
	if after.Profile.Verdict != AuditPass {
		t.Fatalf("reclaimed successor should be passable, got %v", after.Profile.Verdict)
	}
}

func TestAuditWreckAfterActiveRouteAndReclaimWhileWaiting(t *testing.T) {
	terrain := syntheticTerrain(8, 8, 0)
	wreck := auditFeature("route-wreck", true, 2, 1)
	wreck.Reclaimable = true
	reclaimed := auditFeature("route-wreck-smudge", false, 1, 1)
	wreck.FeatureReclamateDef = reclaimed
	terrain.FeatureDefs = []*content.FeatureDef{wreck, reclaimed}
	svc := features.NewService(terrain, nil, nil, nil)
	var route Route
	route.Publish([]Point{{X: 1, Z: 3}, {X: 6, Z: 3}})
	if !route.Active {
		t.Fatal("fixture route is not active")
	}

	// The route is already active when the wreck appears. A current route
	// cannot be silently declared repaired by this audit; the occupied cell is
	// expected to route around after a later publication [04 §7.3].
	inst := svc.PlaceAt(3, 3, wreck)
	if inst == nil {
		t.Fatal("runtime wreck was not placed after route publication")
	}
	blocked := AuditCell(terrain, Template(), Cell{X: 3, Z: 3}, AuditContext{
		RouteRevision:     7,
		HasRouteRevision:  true,
		StaticRevision:    8,
		HasStaticRevision: true,
	})
	if blocked.Profile.Verdict != AuditBlocked || blocked.Finding != FindingStaleRoute {
		t.Fatalf("active-route wreck audit = %#v, want blocked stale route", blocked)
	}

	// Reclaim occurs while the route remains active. The successor is authored
	// nonblocking, so the route may walk through this cell; a large sprite may
	// still overlap visually, which is why rendered geometry is separate.
	svc.Reclaim(nil, inst, 2)
	open := AuditCell(terrain, Template(), Cell{X: 3, Z: 3}, AuditContext{})
	if open.Profile.Verdict != AuditPass || open.Finding != FindingAuthoredNonblocking {
		t.Fatalf("waiting-route reclaimed audit = %#v, want pass/nonblocking", open)
	}
}

func TestAuditFootprintOpeningAndOccupancyEvidence(t *testing.T) {
	terrain := syntheticTerrain(7, 5, 0)
	wall := auditFeature("wall", true, 1, 1)
	for z := int32(0); z < 5; z++ {
		if z == 2 { // one-cell opening in the x=3 barrier
			continue
		}
		stampAuditFeature(t, terrain, wall, 3, z)
	}
	cell := Cell{X: 3, Z: 2}
	single := AuditCell(terrain, Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 255, MaxWaterSlope: 255, BadWaterSlope: 255}, cell, AuditContext{
		CompletedStructureBlocked: true,
		HasStructureVerdict:       true,
	})
	wideProfile := Template()
	wideProfile.FootPrintX, wideProfile.FootPrintZ = 2, 2
	wide := AuditCell(terrain, wideProfile, Cell{X: 2, Z: 1}, AuditContext{})
	if single.Profile.Verdict != AuditPass || wide.Profile.Verdict != AuditBlocked {
		t.Fatalf("opening single/wide verdict = %v/%v", single.Profile.Verdict, wide.Profile.Verdict)
	}
	if !single.HasStructureVerdict || !single.CompletedStructureBlocked {
		t.Fatalf("structure evidence = %#v", single)
	}
	terrain.PlotAt(cell.X, cell.Z).SetOccupantA(17)
	occupied := AuditCell(terrain, Template(), cell, AuditContext{})
	if !occupied.HasMobileOccupant || occupied.MobileOccupantA != 17 {
		t.Fatalf("mobile occupancy evidence = %#v", occupied)
	}
}

func TestAuditUnboundFeatureNeverLooksNonblocking(t *testing.T) {
	terrain := syntheticTerrain(3, 3, 0)
	terrain.PlotAt(1, 1).SetFeature(0)
	a := AuditCell(terrain, Template(), Cell{X: 1, Z: 1}, AuditContext{})
	if !a.Feature.Resolved || a.Feature.DefinitionResolved || a.Profile.Verdict != AuditBlocked {
		t.Fatalf("unbound feature audit = %#v", a)
	}
	if a.Finding == FindingAuthoredNonblocking {
		t.Fatalf("unbound feature was misclassified as nonblocking: %#v", a)
	}
}

func TestAuditRouteEvidenceAndBoundedSink(t *testing.T) {
	terrain := syntheticTerrain(3, 3, 0)
	collector := &AuditCollector{Limit: 1}
	cells := []Cell{{X: 0, Z: 0}, {X: 1, Z: 0}}
	AuditRouteFailure(collector, terrain, Template(), cells, AuditContext{
		RouteRevision:     3,
		HasRouteRevision:  true,
		StaticRevision:    4,
		HasStaticRevision: true,
		RayPassable:       false,
		RayAccepted:       true,
		HasRayVerdict:     true,
		Finding:           FindingPlanner,
	})
	if len(collector.Records) != 1 || collector.Records[0].Cell != cells[0] {
		t.Fatalf("bounded records = %#v", collector.Records)
	}
	a := collector.Records[0]
	if !a.HasRayVerdict || a.RayPassable || !a.RayAccepted || a.Finding != FindingPlanner {
		t.Fatalf("route evidence = %#v", a)
	}
}
