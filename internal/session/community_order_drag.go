package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// publishedHumanUnit validates both authoritative ownership and the immutable
// frame generation captured by the host. Pool handles intentionally have no
// generation bits, so the publication identity closes the slot-reuse window.
func (s *Session) publishedHumanUnit(h pool.Handle, instanceID uint64) *units.Unit {
	if s == nil || s.publication == nil {
		return nil
	}
	u := s.humanUnit(h)
	if u == nil || instanceID == 0 || int(h) >= len(s.publication.unitIdentities) {
		return nil
	}
	id := s.publication.unitIdentities[int(h)]
	if id.unit != u || id.id != instanceID {
		return nil
	}
	return u
}

func (s *Session) applyCommunityKickout(c HumanCommunityKickoutCommand, tick uint32) {
	u := s.publishedHumanUnit(c.Unit, c.InstanceID)
	if u == nil || s.Build == nil {
		return
	}
	s.bindOrderQueue(u)
	s.Build.KickoutMove(u, c.X, c.Y, c.Z, tick)
}

func (s *Session) applyCommunityOrderDrag(c HumanCommunityOrderDragCommand) {
	u := s.publishedHumanUnit(c.Receipt.Unit, c.InstanceID)
	if u == nil {
		return
	}
	q := orders.QueueOfUnit(u)
	if q == nil {
		return
	}
	orders.DragCommunityOrder(q, c.Receipt, c.Position, func(n *orders.Node, raw orders.CommunityOrderDragDestination) (orders.CommunityOrderDragDestination, bool) {
		if !orders.IsMobileBuild(n.ID) {
			return raw, true
		}
		return s.communityDraggedBuildPosition(u, n, raw)
	})
}

func (s *Session) communityDraggedBuildPosition(builder *units.Unit, n *orders.Node, raw orders.CommunityOrderDragDestination) (orders.CommunityOrderDragDestination, bool) {
	if s == nil || s.Build == nil || s.Catalog == nil || builder == nil || n == nil {
		return orders.CommunityOrderDragDestination{}, false
	}
	def, ok := s.Catalog.Unit(n.BuildDefKey)
	if !ok || def == nil {
		return orders.CommunityOrderDragDestination{}, false
	}
	cx, cz, footX, footZ, ok := communityDraggedBuildAnchor(s.Build, def, n.BuildFacing, raw.X, raw.Z)
	if !ok {
		return orders.CommunityOrderDragDestination{}, false
	}
	result, err := s.PreviewPlacementForCursor(cx, cz, def, footX, footZ, builder.Handle, n.BuildFacing)
	if err != nil {
		return orders.CommunityOrderDragDestination{}, false
	}
	x, z := world.PlacementCenter(cx, cz, footX, footZ)
	return orders.CommunityOrderDragDestination{X: x, Y: numeric.FixedFromInt(int64(result.SiteHeight)), Z: z}, true
}

// communityDraggedBuildAnchor applies the queued order's retained facing
// before snapping the cursor to a placement anchor. Kept separate so the
// transposition and centre arithmetic can be locked without a terrain fixture.
func communityDraggedBuildAnchor(build *construction.Service, def *content.UnitDef, facing units.StructureFacing, x, z numeric.Fixed) (cx, cz, footX, footZ int32, ok bool) {
	geometry, err := build.StructureGeometry(def, facing)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	footX, footZ = geometry.FootprintX, geometry.FootprintZ
	cx, cz = world.PlacementAnchor(x, z, footX, footZ)
	return cx, cz, footX, footZ, true
}
