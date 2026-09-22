package main

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type communityOrderDragKind uint8

const (
	communityDragNone communityOrderDragKind = iota
	communityDragQueuedOrder
	communityDragManualKickout
)

// communityOrderDragState owns a whole sourced press/release gesture. Its
// identity comes only from the committed frame and its destination preview is
// host-only; authoritative queue mutation remains at the typed session-command
// boundary [community patch engine behavior §5.11].
type communityOrderDragState struct {
	kind       communityOrderDragKind
	unit       pool.Handle
	instanceID uint64
	receipt    orders.CommunityOrderDragReceipt
	pressX     int32
	pressY     int32
	moved      bool
	cancelled  bool
	preview    communityOrderDragPreview
}

type communityOrderDragPreview struct {
	visible              bool
	build, valid         bool
	position             orders.CommunityOrderDragDestination
	cellX, cellZ         int32
	footX, footZ, height int32
}

func (b *battleSession) serviceCommunityOrderDrag(in *input.State, cl *client.Client, queuedEnabled bool) bool {
	if b == nil || in == nil || in.Mouse == nil || in.Kbd == nil || b.sess == nil || b.cam == nil {
		return false
	}
	mouse, modifiers := publishedPointer(in)
	mx, my := int32(mouse.X), int32(mouse.Y)
	state := &b.communityOrderDrag
	if state.kind != communityDragNone {
		return b.continueCommunityOrderDrag(in, cl, mouse, mx, my)
	}
	if !mouse.Pressed(input.MouseButtonLeft) || b.palettePointerOwned || b.classifyPointer(mx, my) != battlePointerViewport || cl != nil && !cl.IsFocused() {
		return false
	}
	override := communityModifierHeld(in.Kbd, b.hostPreferences().ClickSnapOverrideKey)
	// ConstructionKickout receives the window message before the queued-order
	// handler in the extension. The explicit owned-unit gesture therefore wins
	// even when Shift and queued dragging are also enabled.
	if override && b.sess.Build != nil && b.sess.Community.ConstructionKickout {
		f, ok := b.currentSnapshot()
		if ok {
			h, v, hit := b.pickPresentedUnit(f, mx, my, f.ViewingPlayer)
			if hit && h != 0 && v.Owner == b.sess.LocalOwner {
				*state = communityOrderDragState{kind: communityDragManualKickout, unit: h, instanceID: v.InstanceID, pressX: mx, pressY: my}
				return true
			}
		}
	}
	if !queuedEnabled || !modifiers.Shift || override || b.battleState().Input.Latch != input.LatchNormal || cl != nil && cl.StrategicViewActive() {
		return false
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return false
	}
	wx, wy, wz := b.cursorWorld(mx, my)
	view, unit, instanceID, found := communityOrderAtPoint(f, b.cat, wx, wy, wz)
	if !found {
		return false
	}
	*state = communityOrderDragState{
		kind: communityDragQueuedOrder, unit: unit, instanceID: instanceID, pressX: mx, pressY: my,
		receipt: communityOrderReceipt(view),
	}
	// Let the ordinary idle press establish its selection capture. If this
	// never becomes a drag, the extension replays that same click on release.
	return false
}

func (b *battleSession) continueCommunityOrderDrag(in *input.State, cl *client.Client, mouse input.MouseState, mx, my int32) bool {
	state := &b.communityOrderDrag
	overrideHeld := communityModifierHeld(in.Kbd, b.hostPreferences().ClickSnapOverrideKey)
	requiredHeld := overrideHeld
	if state.kind == communityDragQueuedOrder {
		requiredHeld = in.Kbd.HasShift()
	}
	strategic := cl != nil && cl.StrategicViewActive()
	queuedContextLost := state.kind == communityDragQueuedOrder && communityQueuedOrderDragContextLost(b.battleState().Input.Latch, overrideHeld, strategic)
	if !requiredHeld || queuedContextLost || cl != nil && !cl.IsFocused() || b.palettePointerOwned || battleShortcutKeyboard(in).KeyDown(input.KeyEscape) || mouse.Pressed(input.MouseButtonRight) {
		state.cancelled = true
		state.preview.visible = false
	}
	state.moved = state.moved || mx != state.pressX || my != state.pressY
	if state.kind == communityDragQueuedOrder && !state.cancelled {
		f, ok := b.currentSnapshot()
		if !ok || !communityOrderGestureStillPublished(f, state.receipt, state.instanceID) {
			state.cancelled = true
			state.preview.visible = false
		} else if state.moved {
			b.updateCommunityOrderDragPreview(mx, my)
		}
	}
	if mouse.Held(input.MouseButtonLeft) {
		return true
	}
	if !mouse.Released(input.MouseButtonLeft) {
		wasQueued := state.kind == communityDragQueuedOrder
		*state = communityOrderDragState{}
		if wasQueued {
			b.battleState().Input.DragActive = false
		}
		return true
	}
	gesture := *state
	*state = communityOrderDragState{}
	if gesture.kind == communityDragQueuedOrder && !gesture.cancelled && !gesture.moved {
		return false
	}
	if gesture.kind == communityDragQueuedOrder {
		b.battleState().Input.DragActive = false
	}
	if gesture.cancelled {
		return true
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return true
	}
	v, found := snapshotUnitByHandle(f, gesture.unit)
	if !found || v.Owner != b.sess.LocalOwner || v.InstanceID != gesture.instanceID {
		return true
	}
	wx, wy, wz := b.cursorWorld(mx, my)
	if gesture.kind == communityDragManualKickout {
		_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanCommunityKickout, CommunityKickout: session.HumanCommunityKickoutCommand{
			Unit: gesture.unit, InstanceID: gesture.instanceID, X: wx, Y: wy, Z: wz,
		}})
		return true
	}
	if !communityOrderGestureStillPublished(f, gesture.receipt, gesture.instanceID) {
		return true
	}
	_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanCommunityOrderDrag, CommunityOrderDrag: session.HumanCommunityOrderDragCommand{
		InstanceID: gesture.instanceID, Receipt: gesture.receipt,
		Position: orders.CommunityOrderDragDestination{X: wx, Y: wy, Z: wz},
	}})
	return true
}

func communityQueuedOrderDragContextLost(latch input.Latch, overrideHeld, strategic bool) bool {
	return latch != input.LatchNormal || overrideHeld || strategic
}

func communityOrderAtPoint(f *frame.Frame, cat *content.Catalog, x, y, z numeric.Fixed) (frame.OrderView, pool.Handle, uint64, bool) {
	if f == nil {
		return frame.OrderView{}, 0, 0, false
	}
	for _, unit := range f.Selection.Handles {
		uv, ok := snapshotUnitByHandle(f, unit)
		if !ok || uv.Owner != f.Selection.LocalPlayer {
			continue
		}
		for _, queue := range f.OrderQueues {
			if queue.Unit != unit {
				continue
			}
			for _, order := range queue.Primary {
				if !communityPublishedOrderDraggable(order) || !communityOrderHit(order, cat, x, y, z) {
					continue
				}
				return order, unit, uv.InstanceID, true
			}
			break
		}
	}
	return frame.OrderView{}, 0, 0, false
}

func communityPublishedOrderDraggable(order frame.OrderView) bool {
	if order.Target != 0 {
		return false
	}
	if order.BuildProduct != "" && (order.Kind == "MobileBuild" || order.Kind == "VTOL_MobileBuild") {
		return true
	}
	switch order.Kind {
	case "Move_Ground", "VTOL_Move", "QMove",
		"Patrol", "VTOL_Patrol", "QPatrol", "RepairPatrol", "VTOL_RepairPatrol",
		"Ground_Unload", "VTOL_Unload":
		return true
	default:
		return false
	}
}

func communityOrderHit(order frame.OrderView, cat *content.Catalog, x, y, z numeric.Fixed) bool {
	footX, footZ := int64(1), int64(1)
	if order.BuildProduct != "" {
		if cat == nil {
			return false
		}
		def, ok := cat.Unit(order.BuildProduct)
		if !ok || def == nil || def.FootprintX <= 0 || def.FootprintZ <= 0 {
			return false
		}
		// The source's picker reads the definition's ordinary authored
		// extents. Rotation is applied later, when the drag destination is
		// previewed and validated; using OrderView's oriented display fields
		// here would silently widen a different axis.
		footX, footZ = int64(def.FootprintX), int64(def.FootprintZ)
	}
	halfX := numeric.FixedFromInt(8 * footX)
	halfZ := numeric.FixedFromInt(8 * footZ)
	orderScreenZ := order.GoalZ - order.GoalY/2
	cursorScreenZ := z - y/2
	return x >= order.GoalX-halfX && x < order.GoalX+halfX && cursorScreenZ >= orderScreenZ-halfZ && cursorScreenZ < orderScreenZ+halfZ
}

func (b *battleSession) updateCommunityOrderDragPreview(mx, my int32) {
	state := &b.communityOrderDrag
	if state.kind != communityDragQueuedOrder || state.cancelled {
		return
	}
	wx, wy, wz := b.cursorWorld(mx, my)
	preview := communityOrderDragPreview{visible: true, valid: true, position: orders.CommunityOrderDragDestination{X: wx, Y: wy, Z: wz}}
	if state.receipt.BuildProduct == "" {
		state.preview = preview
		return
	}
	preview.build = true
	if b.cat == nil || b.sess == nil || b.sess.Build == nil {
		preview.valid = false
		state.preview = preview
		return
	}
	def, ok := b.cat.Unit(state.receipt.BuildProduct)
	if !ok || def == nil {
		preview.valid = false
		state.preview = preview
		return
	}
	facing := units.StructureFacing(state.receipt.BuildFacing)
	geometry, err := b.sess.Build.StructureGeometry(def, facing)
	if err != nil {
		preview.valid = false
		state.preview = preview
		return
	}
	preview.footX, preview.footZ = geometry.FootprintX, geometry.FootprintZ
	preview.cellX, preview.cellZ = world.PlacementAnchor(wx, wz, preview.footX, preview.footZ)
	result, placementErr := b.sess.PreviewPlacementForCursor(preview.cellX, preview.cellZ, def, preview.footX, preview.footZ, state.unit, facing)
	preview.height = result.SiteHeight
	preview.valid = placementErr == nil
	x, z := world.PlacementCenter(preview.cellX, preview.cellZ, preview.footX, preview.footZ)
	preview.position = orders.CommunityOrderDragDestination{X: x, Y: numeric.FixedFromInt(int64(result.SiteHeight)), Z: z}
	state.preview = preview
}

func (b *battleSession) communityOrderDragPreviewActive() bool {
	return b != nil && b.communityOrderDrag.kind == communityDragQueuedOrder && b.communityOrderDrag.preview.visible
}

// drawCommunityOrderDrag draws the host-side feedback for the cursor point
// that will become one typed command on release. The extension mutates its
// retained record on every mouse move; Nanolathe keeps simulation mutation at
// the command boundary and presents the same destination from this transient
// state [community patch engine behavior §5.11].
func (b *battleSession) drawCommunityOrderDrag(cl *client.Client) {
	if cl == nil || b == nil || b.cam == nil || !b.communityOrderDragPreviewActive() {
		return
	}
	p := b.communityOrderDrag.preview
	color := cl.GUIColor(hud.GhostColorLegal)
	if !p.valid {
		color = cl.GUIColor(hud.GhostColorIllegal)
	}
	if p.build && p.footX > 0 && p.footZ > 0 {
		l, t, r, bt := b.siteRectToScreen(p.cellX*16, p.cellZ*16, (p.cellX+p.footX)*16, (p.cellZ+p.footZ)*16, p.height)
		cl.UIFrameRect(int(l), int(t), int(r-l), int(bt-t), color)
		cl.UIFrameRect(int(l)+1, int(t)+1, int(r-l)-2, int(bt-t)-2, color)
		return
	}
	sx, sy := cl.WorldToScreenPx(p.position.X, p.position.Y, p.position.Z)
	sx, sy = sx-camera.OriginX, sy-camera.OriginY
	cl.UIFrameRect(int(sx)-3, int(sy)-3, 7, 7, color)
}

func communityOrderReceipt(order frame.OrderView) orders.CommunityOrderDragReceipt {
	return orders.CommunityOrderDragReceipt{Unit: order.Unit, Index: order.Index, DescriptorID: order.DescriptorID,
		CreationTick: order.CreationTick, Target: order.Target, GoalX: order.GoalX, GoalY: order.GoalY, GoalZ: order.GoalZ,
		BuildProduct: order.BuildProduct, BuildFacing: order.BuildFacing}
}

func communityOrderReceiptStillPublished(f *frame.Frame, receipt orders.CommunityOrderDragReceipt, instanceID uint64) bool {
	v, ok := snapshotUnitByHandle(f, receipt.Unit)
	if !ok || v.InstanceID != instanceID {
		return false
	}
	for _, q := range f.OrderQueues {
		if q.Unit != receipt.Unit || int(receipt.Index) >= len(q.Primary) {
			continue
		}
		return communityOrderReceipt(q.Primary[receipt.Index]) == receipt
	}
	return false
}

func communityOrderGestureStillPublished(f *frame.Frame, receipt orders.CommunityOrderDragReceipt, instanceID uint64) bool {
	return f != nil && slices.Contains(f.Selection.Handles, receipt.Unit) && communityOrderReceiptStillPublished(f, receipt, instanceID)
}
