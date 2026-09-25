package main

// The megamap's selection and order overlay: the selection box, the placement
// ghost and the Shift-held queued-order overlay, drawn over the composed
// image from the committed frame (DESIGN_INTERFACE_HUD_INPUT §3.15). The
// contracts are the shipped ProTA 4.8 megamap's
// ([draw-engine-interface "Selection and order overlay"](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap));
// positions use the megamap's one shared frame, and colour-map entries go
// through the logical-to-physical map. `Megamap*Color` does not apply here.
// Nothing here reads the live simulation [I6].

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The overlay's fixed colour-map entries.
const (
	megamapBoxEntry         = 15 // the selection box
	megamapGhostValidEntry  = 10 // the ghost over a valid site, a selected unit's queued site
	megamapGhostRefuseEntry = 4  // the ghost over a refused site
	megamapSiteOtherEntry   = 1  // an unselected unit's queued site
	megamapCloakEntry       = 15 // a cloaked focus unit's minimum-cloak circle
)

// megamapDashPixels is the dash chain's spacing in image pixels on each axis
// and its phase period in ticks [draw-engine-interface "Selection and order
// overlay"].
const megamapDashPixels = 20

// The order descriptor's draw-mask bits the megamap reads.
const (
	megamapMaskSite  = 0x01
	megamapMaskChain = 0x02
	megamapMaskIcon  = 0x08
	megamapMaskCloak = 0x10
)

// drawMegamapOverlay draws the box, the placement ghost and, while Shift is
// held, the queued-order overlay into the composed image.
func (b *battleSession) drawMegamapOverlay(cur *frame.Frame, lens camera.MegamapLens, key megamapComposeKey) {
	m := &b.megamap.composition
	w, h := int(lens.W), int(lens.H)
	// 1. One outline in entry 15 joining the press point and the clamped
	// pointer, only while both extents exceed eight pixels. There is no
	// inner frame, unlike the world drag rectangle [03 R-SEL-02A].
	if key.box && render.MegamapBoxOutlineShown(key.bx0, key.by0, key.bx1, key.by1) {
		render.StrokeMegamapRect(m.image, w, h, int(key.bx0), int(key.by0), int(key.bx1), int(key.by1), b.hud.paletteIndex(megamapBoxEntry))
	}
	// 2. The placement ghost: the armed footprint centred on the projection
	// of the pointer's world point, with the half-height shear, in entry 10
	// over a valid site and entry 4 over a refused one.
	//
	// TODO(question): the shipped build's row-building mode draws each queued
	// row position instead, valid positions in raw palette index 240 — or 234
	// under an engine code-byte condition the audit did not identify — and
	// refused ones in 214. Nanolathe has no row-building mode over the
	// megamap (the Modern command drag cannot start there), so no row ghost is
	// drawn; one added later would draw valid positions in 240, the one colour
	// the research ties to no unidentified condition. A trace of that
	// condition would settle 234 [draw-engine-interface "Selection and order
	// overlay"].
	if g := key.ghost; g.shown {
		if wx, wy, wz, ok := b.megamapCursorWorld(g.x, g.y); ok {
			cx, cy := lens.Project(int32(wx>>16), int32(wy>>16), int32(wz>>16))
			entry := byte(megamapGhostRefuseEntry)
			if g.valid {
				entry = megamapGhostValidEntry
			}
			x0, y0, x1, y1 := render.MegamapFootprintRect(cx, cy, g.footX, g.footZ, lens.ScaleX(), lens.ScaleY(), lens.W, lens.H)
			render.StrokeMegamapRect(m.image, w, h, int(x0), int(y0), int(x1), int(y1), b.hud.paletteIndex(entry))
		}
	}
	if key.shift {
		b.drawMegamapQueuedOrders(cur, lens, key)
	}
}

// megamapPoint is a world point in whole units with its height, the form
// every overlay anchor takes before the shear is applied.
type megamapPoint struct{ x, y, z int32 }

func megamapPointOf(x, y, z numeric.Fixed) megamapPoint {
	return megamapPoint{radarMapPixel(x), radarMapPixel(y), radarMapPixel(z)}
}

// drawMegamapQueuedOrders is the Shift-held order overlay. The walk covers
// the local player's units in slot order that are in play and not
// death-marked. Focus units are the hovered unit, the camera-tracked unit and
// the command-page subject; another unit is drawn only if it is selected, or
// if a builder context exists — some focus unit allied with the local player
// whose definition has a build list. Each node of the primary order list is
// drawn by its descriptor's draw-mask bits from a running anchor that starts
// at the unit [draw-engine-interface "Selection and order overlay"].
func (b *battleSession) drawMegamapQueuedOrders(cur *frame.Frame, lens camera.MegamapLens, key megamapComposeKey) {
	local := cur.Selection.LocalPlayer
	slots := b.megamapUnitSlots(cur)
	focus := [3]pool.Handle{key.hover, key.tracked, cur.CommandPage.Builder}
	builderContext := false
	for _, h := range focus {
		if u, ok := megamapUnitView(cur, slots, h); ok && megamapAlliedWith(cur, local, u.Owner) && b.snapshotBuilder(*u) {
			builderContext = true
			break
		}
	}
	selected := make([]bool, len(slots))
	for _, h := range cur.Selection.Handles {
		if int(h) < len(selected) {
			selected[h] = true
		}
	}
	queues := make([]int, len(slots))
	for i := range cur.OrderQueues {
		if h := int(cur.OrderQueues[i].Unit); h > 0 && h < len(queues) {
			queues[h] = i + 1
		}
	}
	d := megamapOverlayDraw{b: b, cur: cur, lens: lens, surf: &render.RadarSurface{W: int(lens.W), H: int(lens.H), Bits: b.megamap.composition.image}}
	for i := range cur.Units {
		u := &cur.Units[i]
		if u.Slot == 0 || u.Owner != local || u.Flags&units.DeathPendingStatus != 0 || int(u.Slot) >= len(queues) || queues[u.Slot] == 0 {
			continue
		}
		isFocus := false
		for _, h := range focus {
			isFocus = isFocus || h != 0 && h == u.Slot
		}
		isSelected := selected[u.Slot]
		if !isFocus && !isSelected && !builderContext {
			continue
		}
		d.unit(u, cur.OrderQueues[queues[u.Slot]-1].Primary, isFocus, isSelected, slots)
	}
}

// megamapAlliedWith is the alliance test against the local player.
func megamapAlliedWith(f *frame.Frame, local, owner uint8) bool {
	if owner == local {
		return true
	}
	return int(local) < len(f.Players) && int(owner) < len(f.Players[local].Allies) && f.Players[local].Allies[owner]
}

// megamapOverlayDraw carries one overlay pass: the icons already drawn, so an
// icon is drawn once per point per frame.
type megamapOverlayDraw struct {
	b     *battleSession
	cur   *frame.Frame
	lens  camera.MegamapLens
	surf  *render.RadarSurface
	icons []megamapPoint
}

// unit draws one unit's order list. Focus units draw everything, selected
// units draw build-site outlines and icons but no chains, and builder-context
// units draw only build-site outlines.
func (d *megamapOverlayDraw) unit(u *frame.UnitView, list []frame.OrderView, isFocus, isSelected bool, slots []int) {
	anchor := megamapPointOf(u.X, u.Y, u.Z)
	for i := range list {
		o := &list[i]
		desc := orders.DescriptorFor(orders.Lookup(o.Kind))
		mask := desc.Class
		goal := megamapPointOf(o.GoalX, o.GoalY, o.GoalZ)
		if mask&megamapMaskSite != 0 && o.FootX > 0 && o.FootZ > 0 {
			// Bit 1: the build site's outline with the ghost's geometry,
			// entry 10 for a selected unit and entry 1 otherwise; a focus
			// unit's chain runs to it, and the anchor becomes the site.
			entry := byte(megamapSiteOtherEntry)
			if isSelected {
				entry = megamapGhostValidEntry
			}
			cx, cy := d.lens.Project(goal.x, goal.y, goal.z)
			x0, y0, x1, y1 := render.MegamapFootprintRect(cx, cy, int32(o.FootX), int32(o.FootZ), d.lens.ScaleX(), d.lens.ScaleY(), d.lens.W, d.lens.H)
			render.StrokeMegamapRect(d.surf.Bits, d.surf.W, d.surf.H, int(x0), int(y0), int(x1), int(y1), d.b.hud.paletteIndex(entry))
			if isFocus {
				d.chain(anchor, goal, o.CreationTick)
			}
			anchor = goal
		}
		if !isFocus && !isSelected {
			continue
		}
		if mask&(megamapMaskChain|megamapMaskIcon) != 0 {
			at := d.orderAnchor(o, goal, slots)
			d.icon(desc.AckGroup, at)
			if mask&megamapMaskChain != 0 {
				// Bit 2: a focus unit's chain to the resolved anchor, which
				// then advances. Bit 8 alone draws the icon with no chain.
				if isFocus {
					d.chain(anchor, at, o.CreationTick)
				}
				anchor = at
			}
		}
		if mask&megamapMaskCloak != 0 && isFocus && u.Cloaked {
			d.cloakCircle(u)
		}
	}
}

// orderAnchor is a node's anchor: for an order with a target unit, the
// target's committed position while the local player can see it, otherwise
// the order's own position.
//
// TODO(question): the shipped build keeps a cached target position for a
// target the local player cannot see; Nanolathe does not publish that cache,
// so an unseen target's node falls back to the stored position (for a follow
// order that is an offset from the ward, not a map point). Publishing the
// order record's cached target position would settle it
// [draw-engine-interface "Selection and order overlay"][04 R-ORD-01 §13].
func (d *megamapOverlayDraw) orderAnchor(o *frame.OrderView, goal megamapPoint, slots []int) megamapPoint {
	if o.Target == 0 {
		return goal
	}
	t, ok := megamapUnitView(d.cur, slots, o.Target)
	if !ok || !(t.Owner == d.cur.Selection.LocalPlayer || client.SnapshotVisible(d.cur, *t, d.cur.Selection.LocalPlayer)) {
		return goal
	}
	return megamapPointOf(t.X, t.Y, t.Z)
}

// icon draws the order icon — the cursor-art entry the descriptor's icon byte
// names, 1 to 20, at frame `(tick / (2 × ticksPerFrame)) mod frames` — unless
// an icon was already drawn at exactly that point this frame.
func (d *megamapOverlayDraw) icon(iconByte uint8, at megamapPoint) {
	if iconByte < 1 || iconByte > 20 {
		return
	}
	for _, p := range d.icons {
		if p == at {
			return
		}
	}
	entry := queueIconEntry(d.b.fs, iconByte)
	index, ok := queueIconFrame(entry, d.cur.Tick)
	if !ok || int(index) >= len(entry.Frames) {
		return
	}
	d.icons = append(d.icons, at)
	x, y := d.lens.Project(at.x, at.y, at.z)
	blitRadarGAF(d.surf, x, y, entry.Frames[index].Frame)
}

// chain places the `pathicon` dash chain from one anchor to the next, both
// taken as (x, z − y/2) (render.MegamapDashChain).
func (d *megamapOverlayDraw) chain(from, to megamapPoint, born uint32) {
	entry := dashChainEntry(d.b.fs)
	if entry == nil || len(entry.Frames) == 0 {
		return
	}
	age := uint32(0)
	if d.cur.Tick > born {
		age = d.cur.Tick - born
	}
	sx, sy := d.lens.ScaleX(), d.lens.ScaleY()
	a := [2]float64{float64(from.x), float64(from.z) - float64(from.y)/2}
	b := [2]float64{float64(to.x), float64(to.z) - float64(to.y)/2}
	megamapDashChain(a, b, sx, sy, float64(d.lens.ExtentW), float64(d.lens.ExtentH), age, int(entry.Frames[0].Value), len(entry.Frames), func(frameIndex int, x, z float64) {
		blitRadarGAF(d.surf, int32(x*sx), int32(z*sy), entry.Frames[frameIndex].Frame)
	})
}

// cloakCircle is a cloaked focus unit's circle in entry 15, radius the
// definition's minimum cloak distance times the horizontal scale, truncated.
// It is drawn at every node whose mask carries bit 16.
//
// TODO(question): the research does not name the circle's centre; Nanolathe
// centres it on the unit's projected position, where retail's own overlay
// centres its range rings [07 R-P0-11 §3]. A trace of the megamap's circle
// call would settle it.
func (d *megamapOverlayDraw) cloakCircle(u *frame.UnitView) {
	if d.b.cat == nil {
		return
	}
	def, ok := d.b.cat.Unit(u.DefName)
	if !ok || def == nil {
		return
	}
	r := int(float64(int32(int16(def.MinCloakDistance))) * d.lens.ScaleX())
	if r <= 0 {
		return
	}
	cx, cy := d.lens.Project(radarMapPixel(u.X), radarMapPixel(u.Y), radarMapPixel(u.Z))
	render.DrawMegamapCircle(d.surf.Bits, d.surf.W, d.surf.H, int(cx), int(cy), r, d.b.hud.paletteIndex(megamapCloakEntry))
}

// megamapDashChain places the megamap's dash chain along one segment, in
// world units with the half-height shear already applied. The spacing is the
// world length of a 20×20 image-pixel diagonal,
// `√(trunc(20 / scaleX)² + trunc(20 / scaleY)²)`, and nothing is drawn unless
// the segment is longer than one spacing. The first sprite sits
// `(age mod 20) × spacing / 20` along the segment at frame
// `(age / ticksPerFrame) mod frames`, and each later sprite takes the next
// frame. The direction and length use the endpoints clamped to the map
// extent; positions start from the unclamped anchor [draw-engine-interface
// "Selection and order overlay"].
func megamapDashChain(a, b [2]float64, scaleX, scaleY, extentW, extentH float64, age uint32, ticksPerFrame, frames int, emit func(frame int, x, z float64)) {
	if frames <= 0 || scaleX <= 0 || scaleY <= 0 || emit == nil {
		return
	}
	ticksPerFrame = max(ticksPerFrame, 1)
	stepX, stepZ := math.Trunc(megamapDashPixels/scaleX), math.Trunc(megamapDashPixels/scaleY)
	spacing := math.Sqrt(stepX*stepX + stepZ*stepZ)
	clamp := func(p [2]float64) [2]float64 {
		return [2]float64{min(max(p[0], 0), extentW), min(max(p[1], 0), extentH)}
	}
	ca, cb := clamp(a), clamp(b)
	dx, dz := cb[0]-ca[0], cb[1]-ca[1]
	length := math.Sqrt(dx*dx + dz*dz)
	if spacing <= 0 || length <= spacing {
		return
	}
	ux, uz := dx/length, dz/length
	index := int(age/uint32(ticksPerFrame)) % frames
	for along := float64(age%megamapDashPixels) * spacing / megamapDashPixels; along < length; along += spacing {
		emit(index, a[0]+ux*along, a[1]+uz*along)
		index = (index + 1) % frames
	}
}
