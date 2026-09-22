package main

// Community build-model preview host policy. The pose comes from the host's
// armed placement state; selected builders, player rows, alliances, candidate
// units and visibility all come from one committed frame. This path never reads
// the live unit or visibility services [I6] (DESIGN_COMMUNITY_PATCH §7,
// CP-UD-2).

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// drawCommunityBuildPreview records the optional model preview beside the
// existing placement rectangle. The caller invokes it while the world-overlay
// region is open, after drawBuildGhost has established the ordinary armed and
// viewport gates.
func (b *battleSession) drawCommunityBuildPreview(c *client.Client) {
	if b == nil || c == nil || b.cam == nil || !b.battleState().PlacementArmed() {
		return
	}
	if !b.overWorld(b.battleState().Input.PointerX, b.battleState().Input.PointerY) {
		return
	}
	style := client.CommunityPreviewStyle(b.hostPreferences().NanoframePreview)
	if style != client.CommunityPreviewFull && style != client.CommunityPreviewWireframe {
		return
	}
	if b.cat == nil {
		return
	}
	def, ok := b.cat.Unit(b.battleState().Input.BuildDef)
	if !ok || def == nil || def.ObjectName == "" {
		return
	}
	cur, ok := b.currentSnapshot()
	if !ok {
		return
	}
	input := &b.battleState().Input
	owner := cur.Selection.LocalPlayer
	var color uint8
	known := int(owner) < len(cur.Players) && cur.Players[owner].Present
	if known {
		color = cur.Players[owner].Logo
	}
	drawSite := func(cx, cz, height int32, chosen units.StructureFacing) {
		x, z := world.PlacementCenter(cx, cz, input.BuildFootX, input.BuildFootZ)
		facing := communityPreviewFacing(cur, b.cat, def, chosen, x, z)
		c.DrawCommunityBuildPreview(client.CommunityPreviewOptions{
			Definition: def, Facing: uint8(facing), Heading: units.HeadingWithFacing(32768, facing),
			Owner: owner, OwnerColor: color, ColorKnown: known,
			X: x, Y: numeric.FixedFromInt(int64(height)), Z: z, Style: style,
		})
	}
	if drag := b.modernDrag; drag != nil {
		if drag.product == input.BuildDef {
			for _, site := range drag.sites {
				drawSite(site.cell.x, site.cell.z, site.height, drag.facing)
			}
		}
		return
	}
	drawSite(input.BuildCellX, input.BuildCellZ, input.BuildSiteH, b.communityPlacementFacing(def))
}

// communityPreviewFacing applies PreviewFaceOpponent's fog-safe host gate.
// It deliberately returns an authored-disallowed facing when an eligible
// visible opponent exists: the key declares that the future unit script owns
// its heading (community patch engine, CP-UD-2).
func communityPreviewFacing(cur *frame.Frame, cat *content.Catalog, def *content.UnitDef, chosen units.StructureFacing, x, z numeric.Fixed) units.StructureFacing {
	if cur == nil || cat == nil || def == nil || !def.PreviewFaceOpponent {
		return chosen
	}
	local := cur.Selection.LocalPlayer
	if local >= frame.PlayerRowSlots || !cur.Players[local].Present || !communityPreviewNearSelectedBuilder(cur, cat, local, x, z) {
		return chosen
	}
	var (
		found        bool
		bestDistance uint64
		bestDX       int64
		bestDZ       int64
	)
	for player := uint8(0); player < frame.PlayerRowSlots; player++ {
		if player == local {
			continue
		}
		row := cur.Players[player]
		if !row.Present || row.Watcher || cur.Players[local].Allies[player] {
			continue
		}
		candidate, ok := communityPreviewUnitByHandle(cur.Units, row.UnitSlotStart)
		if !ok || !client.SnapshotVisible(cur, candidate, cur.ViewingPlayer) {
			continue
		}
		dx := int64(candidate.X - x)
		dz := int64(candidate.Z - z)
		distance := communityPreviewDistanceSquared(dx, dz)
		if !found || distance < bestDistance {
			found, bestDistance, bestDX, bestDZ = true, distance, dx, dz
		}
	}
	if !found {
		return chosen
	}
	if absInt64(bestDX) > absInt64(bestDZ) {
		if bestDX > 0 {
			return units.FacingEast
		}
		return units.FacingWest
	}
	if bestDZ > 0 {
		return units.FacingSouth
	}
	return units.FacingNorth
}

func communityPreviewNearSelectedBuilder(cur *frame.Frame, cat *content.Catalog, local uint8, x, z numeric.Fixed) bool {
	for _, handle := range cur.Selection.Handles {
		view, ok := communityPreviewUnitByHandle(cur.Units, handle)
		if !ok || view.Owner != local || view.BuildRemaining != 0 {
			continue
		}
		builder, ok := cat.UnitDefByIndex(uint32(view.DefID))
		if !ok {
			builder, ok = cat.Unit(view.DefName)
		}
		if !ok || builder == nil || builder.BuildDistance <= 0 {
			continue
		}
		radius := uint64(builder.BuildDistance) << 16
		dx := uint64(absInt64(int64(view.X - x)))
		dz := uint64(absInt64(int64(view.Z - z)))
		// Test dz² <= r²-dx² after the axis bounds. This is the same
		// inclusive circle without overflowing at the largest authored uint16
		// build distance.
		if dx <= radius && dz <= radius && dz*dz <= radius*radius-dx*dx {
			return true
		}
	}
	return false
}

func communityPreviewUnitByHandle(views []frame.UnitView, handle pool.Handle) (frame.UnitView, bool) {
	for _, view := range views {
		if view.Slot == handle {
			return view, true
		}
	}
	return frame.UnitView{}, false
}

func communityPreviewDistanceSquared(dx, dz int64) uint64 {
	x, z := uint64(absInt64(dx)), uint64(absInt64(dz))
	if x > math.MaxUint32 || z > math.MaxUint32 {
		return math.MaxUint64
	}
	x2, z2 := x*x, z*z
	if math.MaxUint64-x2 < z2 {
		return math.MaxUint64
	}
	return x2 + z2
}

func absInt64(v int64) int64 {
	if v < 0 {
		if v == math.MinInt64 {
			return math.MaxInt64
		}
		return -v
	}
	return v
}
