package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestCommunityDraggedBuildAnchorRetainsRotation(t *testing.T) {
	def := &content.UnitDef{UnitName: "lab", FootprintX: 2, FootprintZ: 3, Rotations: content.FacingSouth | content.FacingEast}
	build := &construction.Service{Rules: construction.CommunityRules{}, Community: community.Features{StructureRotation: true}}
	x, z := numeric.FixedFromInt(104), numeric.FixedFromInt(88)
	cx, cz, footX, footZ, ok := communityDraggedBuildAnchor(build, def, units.FacingEast, x, z)
	if !ok || footX != 3 || footZ != 2 {
		t.Fatalf("east anchor=%d,%d footprint=%dx%d ok=%v", cx, cz, footX, footZ, ok)
	}
	if sx, sz, _, _, _ := communityDraggedBuildAnchor(build, def, units.FacingSouth, x, z); sx == cx && sz == cz {
		t.Fatal("rotated and south footprints snapped the odd cursor to the same anchor")
	}
}

func TestCommunityOrderDragValidatesPublishedUnitGeneration(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "mover"}, UnitName: "mover", MaxDamage: 100, BMCode: 1, CanMove: true, Script: fixtureCOBProgram()}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"mover": def}}
	w := newSessionFixtureWorld(2, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	q := orders.QueueForUnit(u)
	id := orders.Lookup("Move_Ground")
	n := &orders.Node{ID: id, Owner: h, CreationTick: 2, GoalX: 10 << 16, Phase: 3}
	q.SetPrimary([]*orders.Node{n})
	s := &Session{Units: w, LocalOwner: 0, publication: &publicationState{}}
	instanceID := s.publication.unitIdentity(u)
	receipt := orders.CommunityOrderDragReceipt{Unit: h, DescriptorID: int32(id), CreationTick: 2, GoalX: 10 << 16}
	s.applyCommunityOrderDrag(HumanCommunityOrderDragCommand{InstanceID: instanceID + 1, Receipt: receipt, Position: orders.CommunityOrderDragDestination{X: 20 << 16}})
	if n.GoalX != 10<<16 || n.Phase != 3 {
		t.Fatal("stale published generation mutated the queue")
	}
	s.applyCommunityOrderDrag(HumanCommunityOrderDragCommand{InstanceID: instanceID, Receipt: receipt, Position: orders.CommunityOrderDragDestination{X: 20 << 16}})
	if n.GoalX != 20<<16 || n.Phase != 0 {
		t.Fatalf("valid generation left phase/goal %d/%d", n.Phase, n.GoalX)
	}
}
