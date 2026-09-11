package main

// Build placement: the armed product, its footprint check against the world,
// the ghost it draws and the order it commits [04 §6.2] [07 §9].

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func (b *battleSession) cursorWorld(sx, sy int32) (wx, wy, wz numeric.Fixed) {
	if b.cam == nil {
		return 0, 0, 0
	}
	// Step 1 of the host frame classifies the pointer before any ground
	// resolution. A pointer inside the minimap rectangle takes the lens
	// conversion, not the view's cursor-to-world projection — the two paths do
	// not share a routine in retail either — and the resulting map pixels then
	// go through the same ground resolver [07 R-CAM-01 §11][03 §3.11].
	if mpx, mpz, ok := b.minimapPointerWorld(sx, sy); ok {
		if b.sess != nil {
			if wx, wy, wz, ok := b.sess.CursorToWorld(mpx, mpz); ok {
				return wx, wy, wz
			}
		}
		return numeric.Fixed(mpx) << 16, 0, numeric.Fixed(mpz) << 16
	}
	// Clamp pointer into the battle viewport before ground resolution [07 §8]
	// step 1 [C-2]. The viewport is the drawn-chrome region of the **live**
	// surface, not of the authored 640×480 one: the battle loader rebuilds it
	// as `(128,32)..(W−1,H−33)` at every display mode, because the chrome
	// extends by rule rather than scaling [03 §4.1][07 R-HUD-05]. Fixing the
	// far edges at 639/447 folded every pointer beyond them back onto the
	// 640×480 corner, so at a larger mode a build site could not be picked
	// outside the authored area at all.
	screenW, screenH := b.surfaceSize()
	clampedX := sx
	clampedY := sy
	if clampedX < camera.OriginX+1 {
		clampedX = camera.OriginX + 1
	} else if clampedX > screenW-1 {
		clampedX = screenW - 1
	}
	if clampedY < camera.OriginY {
		clampedY = camera.OriginY
	} else if clampedY > screenH-camera.OriginY-1 {
		clampedY = screenH - camera.OriginY - 1
	}
	// The renderer stores world points at beam position minus OriginX/Y. Restore
	// those fixed offsets for the camera inverse [03 §2.5].
	fx, fz := b.cam.ScreenToWorld(clampedX+camera.OriginX, clampedY+camera.OriginY)
	if b.sess == nil {
		return fx, 0, fz
	}
	if wx, wy, wz, ok := b.sess.CursorToWorld(int32(fx>>16), int32(fz>>16)); ok {
		return wx, wy, wz
	}
	return fx, 0, fz
}

// armPlacement enters build-placement mode for a product [07 §9]. Retail arms
// the MOBILEBUILD latch, stores the product's definition id in a pending-build
// word and plays the `addbuild` cue; nothing is queued until the world click.
func (b *battleSession) armPlacement(def *content.UnitDef) {
	if def == nil {
		return
	}
	footX, footZ := footprintCellsForCatalog(b.cat, def)
	b.battleState().ArmPlacement(def.CanonicalKey, footX, footZ)
	b.battleState().Input.Latch = input.LatchMobileBuild
}

// disarmPlacement returns the battle screen to the idle latch after a placement
// ends, whether it ended in a click, a cancel, or shift being released [07 §9].
func (b *battleSession) disarmPlacement() {
	if b == nil {
		return
	}
	b.battleState().ClearPlacement()
}

// updatePlacement tracks the ghost under the cursor and validates it against
// the world [04 §6.2][PLAN_08 C17].
func (b *battleSession) updatePlacement(mx, my int32) {
	b.battleState().Input.BuildMX, b.battleState().Input.BuildMY = mx, my
	wx, _, wz := b.cursorWorld(mx, my)
	b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ = world.PlacementAnchor(wx, wz, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ)
	self := uint16(0)
	if frame, ok := b.currentSnapshot(); ok {
		self = uint16(frame.CommandPage.Builder)
	}
	footX, footZ := b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ
	var def *content.UnitDef
	if b.cat != nil {
		def, _ = b.cat.Unit(b.battleState().Input.BuildDef)
	}
	result, err := b.checkProductPlacement(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, def, footX, footZ, self)
	b.battleState().Input.BuildOK = err == nil
	if err == nil {
		b.battleState().Input.BuildSiteH = result.SiteHeight
	} else {
		// PreviewPlacement returns the same derived height on rejection; retaining
		// this value avoids a presentation-side terrain read or fallback rule.
		b.battleState().Input.BuildSiteH = result.SiteHeight
	}
}

// checkProductPlacement is the battle-side adapter to the canonical
// preview/commit predicate. It resolves movement/FBI terrain rules and uses
// the product's compiled footprint, matching construction exactly [R-P0-08]
// [07 §9].
//
// The human build cursor is the one caller in the whole executable that binds
// the blocker's fourth argument to a PLAYER record rather than null
// [04 R-P0-08-B §1], so the ghost goes through PreviewPlacementForCursor: it
// runs the known-site gate — the footprint centre projected onto the
// 32-world-unit LOS grid, a site off that grid or one the local viewing slot
// cannot currently see rejected outright — before the mapping option decides
// whether the occupancy rejections apply. The null-player form this used to
// call accepts a site the local player cannot see.
func (b *battleSession) checkProductPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self uint16) (world.PlacementResult, error) {
	if b == nil || b.sess == nil {
		return world.PlacementResult{}, fmt.Errorf("nanolathe: build placement not previewed: the battle has no session")
	}
	return b.sess.PreviewPlacementForCursor(cx, cz, def, footX, footZ, pool.Handle(self))
}

// placementRect returns the armed site's footprint as a screen rectangle
// [07 §9]. Retail projects the two cell-aligned corners with the ordinary
// half-height shear, using the site height for both, so the ghost lies flat on
// the ground the building will stand on rather than following the cursor.
func (b *battleSession) placementRect() (left, top, right, bottom int32) {
	l := b.battleState().Input.BuildCellX * 16
	t := b.battleState().Input.BuildCellZ * 16
	r := l + b.battleState().Input.BuildFootX*16
	btm := t + b.battleState().Input.BuildFootZ*16
	return b.siteRectToScreen(l, t, r, btm, b.battleState().Input.BuildSiteH)
}

// siteRectToScreen projects a map-pixel footprint rectangle standing at height
// h (in map-pixel height units) into viewport-relative screen pixels [03 §2.5].
// Both corners take the same height, which is what flattens the marker onto the
// ground plane instead of tilting it.
func (b *battleSession) siteRectToScreen(l, t, r, btm, h int32) (left, top, right, bottom int32) {
	if b.cam == nil {
		return l, t, r, btm
	}
	px := func(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }
	x0, y0 := b.cam.WorldToScreen(px(l), px(h), px(t))
	x1, y1 := b.cam.WorldToScreen(px(r), px(h), px(btm))
	return x0 - camera.OriginX, y0 - camera.OriginY, x1 - camera.OriginX, y1 - camera.OriginY
}

// commitBuild queues a mobile-build order through the ordinary construction
// path [PLAN_08 C12/C23][P0-I05]; the session's construction pump drives the lifecycle.
// Site coordinates are passed at queue time and preserved on the queued node as GoalX/Y/Z [P0-I05]:
// the validated footprint center and site height carry the selected site via QueueMobileBuild.
// It uses catalog indices (not FNV hash) [P0-I05] and respects queue modifier (shift=queued) [04 §3.3][P0-I14].
// Every producer goes through one canonical payload constructor [P0-I03]: orders.NewMobileBuildNode / QueueMobileBuild.
// It is data-driven: product must be in builder's BuildMenus list; illegal placement queues nothing [R-P0-03].
func (b *battleSession) commitBuild(queued bool) bool {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 {
		return false
	}
	v, found := snapshotUnitByHandle(frame, frame.CommandPage.Builder)
	if !found || b.sess == nil || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
		return false
	}
	if b.cat != nil && !hud.ValidateBuildProduct(b.cat, v.DefName, b.battleState().Input.BuildDef) {
		return false // GUI may not invent products absent from authored list [R-P0-03]
	}
	if !b.battleState().Input.BuildOK {
		return false // illegal placement queues nothing [R-P0-03]
	}
	// The order carries the footprint's center and the site height, not the raw
	// cursor point: retail recomputes the same cell-aligned anchor the ghost was
	// drawn on and stores `((foot + 2*cell) << 19)` per axis with the validator's
	// site height as Y [07 §9]. Sending the cursor point instead would put the
	// building half a footprint off the box the player aimed with.
	wx, wz := world.PlacementCenter(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ)
	// A queued (Shift) click on a point that already carries a queued order of
	// this kind removes that order and issues nothing — one node, front-most
	// match, goal within one map cell on X and Z, product not part of the match
	// [07 R-P0-11 §6]. That test lives at the authoritative order-insertion
	// boundary, where the builder's live queue is, not here: the presentation
	// sends the same command either way and the session decides whether it adds
	// or removes. The Shift-gated overlay walks the live queues every frame, so
	// a removal stops drawing on the next published tick with no invalidation
	// of its own.
	//
	// Queue the typed command; the session applies it at the authoritative input
	// phase [01 §4.4][07 §9].
	wy := numeric.Fixed(int64(b.battleState().Input.BuildSiteH) << 16)
	if err := b.DispatchMobileBuild(b.battleState().Input.BuildDef, wx, wy, wz, queued); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: build %s: %v\n", b.battleState().Input.BuildDef, err)
		return false
	}
	return true
}

// worldOverlayArmed reports whether this frame's UI stage has a world-positioned
// overlay to draw — the armed build ghost, or the Shift-gated order-queue
// overlay — and so has to open a world region for it
// (DESIGN_GPU_RENDERER §16.3). It is a superset of what the two draw: an armed
// placement whose pointer has left the viewport, or a held Shift with nothing
// queued, opens a region that stays empty, which costs the executor two schedule
// submissions and no pixels.
func (b *battleSession) worldOverlayArmed(cur *frame.Frame) bool {
	if b == nil {
		return false
	}
	state := b.battleState()
	if state == nil {
		return false
	}
	return state.Input.BuildDef != "" || (cur != nil && state.Input.ShiftHeld)
}

// drawBuildGhost draws the armed build site the way retail does [07 §9].
//
// The ghost is two nested one-pixel outlines on the footprint's own cell-aligned
// rectangle — not a box centered on the cursor — and both are drawn in a single
// color chosen by site validity: logical palette entry 10 when the site is legal
// and 4 when it is not. Retail draws no text next to it; the product name and
// its cost live on the build button, and the cursor (`cursorfindsite` versus
// `cursortoofar`) carries the validity as well.
//
// Retail suppresses the ghost whenever the pointer leaves the world viewport,
// so it never appears over the side panel or the minimap.
func (b *battleSession) drawBuildGhost(c *client.Client) {
	if b.battleState().Input.BuildDef == "" || b.cam == nil {
		return
	}
	if !b.overWorld(b.battleState().Input.PointerX, b.battleState().Input.PointerY) {
		return
	}
	l, t, r, btm := b.placementRect()
	col := c.GUIColor(hud.GhostColorIllegal)
	if b.battleState().Input.BuildOK {
		col = c.GUIColor(hud.GhostColorLegal)
	}
	// Retail's adjacent strokes make one solid two-pixel border [07 §9].
	// Scale that entire border, not just the separation between its strokes:
	// ViewScale stores half steps, so casting it to pixels creates a gap even
	// at native zoom (DESIGN_GPU_RENDERER §14.2).
	thickness := int(viewScaleOf(b).Px(2))
	for inset := 0; inset < thickness; inset++ {
		c.UIFrameRect(int(l)+inset, int(t)+inset, int(r-l)-2*inset, int(btm-t)-2*inset, col)
	}
}

func footprintCellsForCatalog(cat *content.Catalog, def *content.UnitDef) (footX, footZ int32) {
	// HUD preview must resolve the same compiled movement/FBI footprint as the
	// sim [R-P0-08][07 §9] C-7: prefer the world helper so the two cannot
	// diverge.
	return world.FootprintForUnit(cat, def)
}
