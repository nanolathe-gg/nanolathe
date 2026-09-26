package main

// The minimap as an input surface: its layout, the pointer conversions and
// the camera latch, click order and hover it produces [03 §3.9] [07 §9].

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// minimapLayout is the sole production adapter for radar geometry. PlayRight
// and PlayBottom are authored by map loading; the rail destination is the
// battle composer's fixed 126-pixel canvas origin [07 §6][07 §10].
func (b *battleSession) minimapLayout() (camera.Minimap, hud.Rect, bool) {
	if b == nil || b.sess == nil || b.hud == nil {
		return camera.Minimap{}, hud.Rect{}, false
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return camera.Minimap{}, hud.Rect{}, false
	}
	dst, ok := b.hud.minimapRect()
	if !ok {
		return camera.Minimap{}, hud.Rect{}, false
	}
	return camera.LayoutMinimap(playW, playH), dst, true
}

// isOverMinimap identifies the authored radar interaction region [07 §10].
func (b *battleSession) isOverMinimap(x, y int32) bool {
	_, dst, ok := b.minimapLayout()
	return ok && dst.Contains(x, y)
}

// isOnRadar distinguishes the fitted radar rectangle from its 126-pixel
// canvas letterbox. The canvas remains the interaction capture region, while
// only the fitted rectangle selects the direct lens branch [07 §10].
func (b *battleSession) isOnRadar(x, y int32) bool {
	m, dst, ok := b.minimapLayout()
	if !ok {
		return false
	}
	dl, dt, dr, db := dst.Ordered()
	if x < dl || x > dr || y < dt || y > db {
		return false
	}
	cx, cy, _ := m.DisplayToCanvas(x, y, dl, dt, dr-dl+1, db-dt+1)
	return m.HitTest(cx, cy)
}

// battlePointerRegion is the pointer record's usable-region classification.
// The minimap and viewport are the two sources that may supply a world point;
// all chrome, including the minimap letterbox bars, is inert. Command and
// cursor consumers call this same classifier so feedback cannot advertise a
// different destination from a click [07 R-CAM-01 §11][07 §8].
type battlePointerRegion uint8

const (
	battlePointerChrome battlePointerRegion = iota
	battlePointerViewport
	battlePointerMinimap
)

func (b *battleSession) classifyPointer(x, y int32) battlePointerRegion {
	if b == nil {
		return battlePointerChrome
	}
	// A covering battle child window owns the pointer record. In particular the
	// unit-information window can cover the radar, so its underlying contact
	// must not reach the cursor, footer, or command classifier [07 §3].
	if unitInfoCovers(x, y) || b.battleState().HasOptionsLayer() {
		return battlePointerChrome
	}
	if b.modernDrag == nil && !b.battleState().Input.DragActive && b.isOnRadar(x, y) {
		return battlePointerMinimap
	}
	if b.overWorld(x, y) {
		return battlePointerViewport
	}
	return battlePointerChrome
}

// minimapCameraButton and minimapOrderButton use the same in-memory Interface
// Type owner as the world-click path. Shell battles observe a live options
// change; direct battles retain the entry-time persisted setting without a
// per-frame settings read [07 R-CAM-01 §5][07 R-CAM-01 §7].
func (b *battleSession) minimapCameraButton() input.MouseButton {
	if b.interfaceTypeRightClick() {
		return input.MouseButtonLeft
	}
	return input.MouseButtonRight
}

func (b *battleSession) minimapOrderButton() input.MouseButton {
	if b.minimapCameraButton() == input.MouseButtonLeft {
		return input.MouseButtonRight
	}
	return input.MouseButtonLeft
}

// minimapPointerWorld is the minimap branch of step 1's pointer
// classification: the lens conversion of [07 R-CAM-01 §11], with no
// half-viewport term. It yields map pixels, which the ground resolver then
// turns into a world point exactly as it does for the view branch [03 §3.11].
//
// The branch is taken only when the pointer is inside the minimap rectangle
// **and no drag rectangle is active**, so a selection drag begun in the view
// keeps resolving against the view even as the pointer crosses the rail
// [07 R-CAM-01 §11].
func (b *battleSession) minimapPointerWorld(x, y int32) (int32, int32, bool) {
	if b == nil || b.sess == nil || b.modernDrag != nil || b.battleState().Input.DragActive {
		return 0, 0, false
	}
	m, dst, ok := b.minimapLayout()
	if !ok {
		return 0, 0, false
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return 0, 0, false
	}
	return client.MinimapPointerWorld(m, dst, playW, playH, x, y)
}

// serviceMinimapCameraLatch runs before this host frame's new clicks. The
// admitted down edge only sets the capture, so the first camera jump is the
// following service pass. Once captured it uses the current pointer record
// even outside the radar rectangle; the signed lens arithmetic runs on that
// record before the camera's normal clamp [07 R-CAM-01 §11].
func (b *battleSession) serviceMinimapCameraLatch(mx, my int32, mouse *input.MouseState) bool {
	if b == nil || b.cam == nil || mouse == nil || !b.minimapCameraCaptured {
		return false
	}
	m, dst, ok := b.minimapLayout()
	if !ok {
		if mouse.Released(b.minimapCameraCaptureButton) {
			b.minimapCameraCaptured = false
		}
		return true
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		if mouse.Released(b.minimapCameraCaptureButton) {
			b.minimapCameraCaptured = false
		}
		return true
	}
	intent, ok := client.MinimapCameraCaptureIntent(m, dst, playW, playH, mx, my)
	if !ok {
		if mouse.Released(b.minimapCameraCaptureButton) {
			b.minimapCameraCaptured = false
		}
		return true
	}
	// The minimap latch jumps, and a minimap jump cancels the follow triple
	// [07 R-CAM-01 §12].
	b.cam.ClearFollow()
	b.pendingFollowInput = nil
	b.cam.JumpToBattleViewCenter(intent.X, intent.Z)
	// The already-set capture is serviced before this frame's queued up edge.
	// Only that captured button's release clears it; a missing held sample is
	// not a substitute for the event [07 R-CAM-01 §11][07 §2].
	if mouse.Released(b.minimapCameraCaptureButton) {
		b.minimapCameraCaptured = false
	}
	return true
}

// beginMinimapCameraLatch records only a qualifying down edge. It never jumps
// on the press frame, and a held-only sample cannot acquire the capture [07
// R-CAM-01 §5][07 R-CAM-01 §11].
func (b *battleSession) beginMinimapCameraLatch(mx, my int32, mouse *input.MouseState) bool {
	if b == nil || mouse == nil || !mouse.Pressed(b.minimapCameraButton()) || b.classifyPointer(mx, my) != battlePointerMinimap {
		return false
	}
	// The alternate camera button is only an idle gesture. In both interface
	// modes an armed order continues to fire on left, and a build placement is
	// likewise an armed world action [07 R-CAM-01 §5].
	in := &b.battleState().Input
	if in.Latch != input.LatchNormal || in.BuildDef != "" {
		return false
	}
	b.minimapCameraCaptured = true
	b.minimapCameraCaptureButton = b.minimapCameraButton()
	return true
}

// minimapClickOrder is retail's minimap order-button path. Under Interface
// Type 0 that button is left; under Type 1 it is right. The world-click
// handler is **region-agnostic**: with the
// armed-order latch not idle the frame handler routes a left-down to it
// whatever the region, and with the latch idle a left-down over the minimap
// reaches it at once — no box drag starts there. Its branches run in the order
// below [07 R-CAM-01 §14 "the world-click handler is region-agnostic, and its
// branch order"].
//
// Orders route through orderSelected, the single order producer. pickTarget
// and cursorWorld already take the minimap branch of the pointer
// classification, so the view and the minimap differ only in how the pointer's
// world point and unit word are resolved [07 R-CAM-01 §11][07 R-HUD-03 §1].
func (b *battleSession) minimapClickOrder(cl *client.Client, mx, my int32, additive bool) {
	if b == nil || !b.isOnRadar(mx, my) {
		return
	}
	// Branch 1: the MOBILEBUILD latch. The site-valid bit has one writer — the
	// in-view placement preview, which the frame handler runs only over the
	// view — so over the minimap the bit still holds the verdict of the last
	// in-view hover. A click while placement is armed therefore sites the
	// building at the minimap-resolved world point when that verdict was OK,
	// and plays `notoktobuild` when it was not. Dropping the click, as this
	// path used to, is the one thing retail does not do [07 R-CAM-01 §14].
	if b.battleState().PlacementArmed() {
		if !b.battleState().Input.BuildOK {
			b.playUICue(cl, "notoktobuild")
			return
		}
		b.siteBuildAtMinimapPoint(cl, mx, my, additive)
		return
	}
	if b.battleState().Input.Latch != input.LatchNormal {
		issued := false
		code := hud.LatchToCode(b.battleState().Input.Latch)
		if code != 0 {
			issued = b.orderSelected(code, mx, my, additive)
		}
		// The latch retires after dispatch unless Shift keeps it, as it does
		// for a world click [07 §9][P0-I14] — and, as there, only when the
		// click issued something: the handler is region-agnostic, so a minimap
		// click the shape gate refused leaves the latch armed too
		// [07 R-CAM-01 §14].
		if issued {
			if additive {
				b.battleState().Input.ShiftLatchSticky = true
			} else {
				b.resetOrderLatch()
			}
		}
		return
	}
	// Branch 2: cursor kind 0x0F, the resolver's "select" answer. With the
	// latch idle and the hovered unit an own selectable unit the click selects
	// it — Shift toggles the selected bit, otherwise the selection is replaced.
	// The hovered unit over the minimap is the nearest HOT RADAR candidate
	// within squared pixel distance 4, so clicking a blip selects that unit
	// [07 R-CAM-01 §14 step 2][07 R-HUD-03 §1]. This branch used to be missing
	// here, which made an own blip unclickable.
	if f, ok := b.currentSnapshot(); ok {
		if h := b.minimapHoverUnit(f, mx, my); h != 0 {
			// The branch is cursor kind `0x0F`, so its admission is the shared
			// eligibility predicate `E(u)`, not ownership alone: a blip whose
			// remaining-build fraction is nonzero is not selectable
			// [07 R-CAM-01 §14 step 2][07 R-WGT-01 §10].
			if v, found := snapshotUnitByHandle(f, h); found && b.ownSelectableUnit(f, v) {
				kind := session.HumanSelectionReplace
				if additive {
					kind = session.HumanSelectionToggle
				}
				_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{h}}})
				playSelectionCue(b.sess, []pool.Handle{h}) // [07 §9]
				return
			}
		}
	}
	// Branch 3: the contextual order, which orders.Resolve specialises from the
	// target and the point [04 §3.4][07 §9].
	if b.hasSelection() {
		b.orderSelected(1, mx, my, additive)
	}
}

// siteBuildAtMinimapPoint is branch 1's issue arm: resolve MOBILEBUILD against
// the armed product and the pointer's world point snapped to the product's
// footprint grid, queued when Shift is held, play `oktobuild`, and keep the
// latch only while Shift is held [07 R-CAM-01 §14 step 1].
//
// It deliberately does not run updatePlacement: the site-valid bit's one
// writer is the in-view preview, and re-validating from the minimap point
// would overwrite the in-view verdict the branch has already consulted. The
// snap is the same PlacementAnchor the preview applies, and cursorWorld takes
// the minimap lens branch for a pointer over the radar rectangle
// [07 R-CAM-01 §11][03 §3.11].
func (b *battleSession) siteBuildAtMinimapPoint(cl *client.Client, mx, my int32, additive bool) {
	in := &b.battleState().Input
	wx, _, wz := b.cursorWorld(mx, my)
	in.BuildMX, in.BuildMY = mx, my
	in.BuildCellX, in.BuildCellZ = world.PlacementAnchor(wx, wz, in.BuildFootX, in.BuildFootZ)
	if !b.commitBuild(additive) {
		// A command rejection is a failed commit, not an armed placement state,
		// exactly as on the in-view path.
		b.disarmPlacement()
		return
	}
	b.playUICue(cl, "oktobuild")
	if additive {
		in.BuildSticky = true
	} else {
		b.disarmPlacement()
	}
}

// minimapHoverUnit is the minimap half of the pointer's unit word. The HOT
// RADAR list contains the admitted radar contacts, projected to radar pixels;
// the pointer admits squared pixel distance < 4, nearest first, else 0 [03 §3.9]
// [07 R-SEL-02B2]. Ties keep the lower pool slot so the result is stable [I1].
func (b *battleSession) minimapHoverUnit(f *frame.Frame, mx, my int32) pool.Handle {
	layout, dst, ok := b.minimapLayout()
	if !ok {
		return 0
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return 0
	}
	left, top, right, bottom := dst.Ordered()
	width, height := right-left+1, bottom-top+1
	best := pool.Handle(0)
	bestDist := int64(1 << 62)
	for i := range f.Radar.Contacts {
		v := &f.Radar.Contacts[i]
		// Contact admission precedes both projection and hover-list insertion.
		// A radar-only contact remains pickable, including during the off phase
		// of its damage blink, but an unknown enemy cannot leak through hover
		// [03 §3.9][07 R-SEL-02B2].
		if v.Handle == 0 || !b.radarUnitContactAdmitted(f, v) {
			continue
		}
		rx, ry := render.RadarProjection(radarMapPixel(v.X), radarMapPixel(v.Z), radarMapPixel(v.Y), playW, playH, layout)
		px, py, ok := layout.CanvasToDisplay(rx+layout.PadX, ry+layout.PadY, left, top, width, height)
		if !ok {
			continue
		}
		dx, dy := int64(px-mx), int64(py-my)
		d := dx*dx + dy*dy
		if d >= 4 {
			continue
		}
		if d < bestDist || (d == bestDist && (best == 0 || v.Handle < best)) {
			bestDist, best = d, v.Handle
		}
	}
	return best
}
