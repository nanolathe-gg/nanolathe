package main

// The optional megamap overview: its settings, the Tab and wheel toggles, the
// pointer it owns while shown and the world point it resolves
// (DESIGN_INTERFACE_HUD_INPUT §3.15). It is host presentation modelled on the
// community draw engine's shipped ProTA 4.8 megamap
// ([draw-engine-interface](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap));
// every gesture it takes becomes an ordinary typed command [I6].

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// megamapHoverBox is the shipped build's `MaxIconWidth` × `MaxIconHeight`
// default, the hover search box in pixels [draw-engine-interface
// "Availability", "Hover"].
const megamapHoverBox = 22

// megamapMinBox is the smallest screen extent, on both axes, at which a left
// release selects by box [draw-engine-interface "Input while shown"].
const megamapMinBox = 9

// battleMegamap is the host state of the overview. Nothing here is saved or
// read by the simulation.
type battleMegamap struct {
	shown      bool
	tabPending bool
	wheelAccum float32

	// The gesture the view owns: a left or right press that began on it, the
	// box it started, and the position of the last double-click, whose
	// matching release is not a click.
	pressOwned          [2]bool
	boxActive           bool
	boxX0, boxY0        int32 // image-local
	boxX1, boxY1        int32
	lastDoubleX         int32
	lastDoubleY         int32
	lastDoubleValid     bool
	composition         megamapComposition
	iconsLoaded         bool
	icons               *client.MegamapIconBank
	slotScratch         []int
	slotScratchForFrame *frame.Frame
}

// megamapOptions is the resolved presentation block.
type megamapOptions struct {
	enabled, wheel, wheelMove, doubleClickMove, flash bool
	thresholds                                        render.MegamapRingThresholds
	dots                                              [10]byte
	// colors are the eight `Megamap*Color` settings in key order; -1 keeps
	// the ring's research default.
	colors [8]int
}

func (b *battleSession) megamapOptions() megamapOptions {
	p := b.hostPreferences()
	o := megamapOptions{
		enabled: p.Overview == settings.OverviewMegamap,
		wheel:   p.MegamapWheel&1 != 0, wheelMove: p.MegamapWheelMove&1 != 0,
		doubleClickMove: p.MegamapDoubleClickMove&1 != 0, flash: p.MegamapFlash&1 != 0,
		thresholds: render.MegamapRingThresholds{
			Radar: int32(p.MegamapRadarMinimum), Sonar: int32(p.MegamapSonarMinimum),
			SonarJam: int32(p.MegamapSonarJamMinimum), AntiNuke: int32(p.MegamapAntiNukeMinimum),
		},
	}
	for i, v := range p.PlayerDotColors {
		o.dots[i] = byte(v)
	}
	for i, v := range p.MegamapRingColors() {
		o.colors[i] = *v
	}
	return o
}

// megamapMode reports whether the Megamap overview is selected. Tab and the
// wheel keep their ordinary meanings otherwise.
func (b *battleSession) megamapMode() bool {
	return b != nil && b.megamapOptions().enabled
}

// megamapShown reports whether the view is up. Switching the overview off
// while it is shown closes it on the next frame.
func (b *battleSession) megamapShown() bool {
	return b != nil && b.megamap.shown
}

// megamapLens fits the map into the battle viewport `(129,32)..(W−1,H−33)`,
// the world region the chrome leaves [03 §4.1][07 R-HUD-05].
func (b *battleSession) megamapLens() camera.MegamapLens {
	if b == nil || b.sess == nil || b.sess.World == nil {
		return camera.MegamapLens{}
	}
	w, h := b.surfaceSize()
	extentW, extentH := camera.MegamapExtent(b.sess.World.CellW, b.sess.World.CellH)
	return camera.LayoutMegamap(camera.OriginX+1, camera.OriginY, w-camera.OriginX-1, h-2*camera.OriginY, extentW, extentH)
}

// setMegamapShown enters or leaves the view. Entering plays `Options`, clears
// the hovered-unit word and any box, and clears the placement site-valid bit;
// leaving plays `Previous` [draw-engine-interface "Entering and leaving"].
// Nothing pauses the simulation.
func (b *battleSession) setMegamapShown(show bool, cl *client.Client) {
	if b == nil || b.megamap.shown == show {
		return
	}
	b.megamap.shown = show
	b.megamap.boxActive = false
	b.megamap.pressOwned = [2]bool{}
	b.megamap.lastDoubleValid = false
	if show {
		// The pictures load once, here on the input side, so drawing only
		// reads them.
		b.megamapIconBank()
		b.playUICue(cl, "Options")
		b.footerHoverUnit = 0
		state := &b.battleState().Input
		state.DragActive = false
		state.BuildOK = false
		b.modernDrag = nil
		b.resourceClick = nil
		b.resourceQueueFeedback = nil
		return
	}
	b.playUICue(cl, "Previous")
}

// serviceMegamapTab consumes the Tab press and toggles on its release. The
// press edge arrives as the residual token; the release is the first battle
// frame afterwards whose held state no longer has Tab, which also covers a
// press and release inside one host frame [draw-engine-interface "Entering and
// leaving"]. It reports whether this frame's token was Tab and so consumed.
func (b *battleSession) serviceMegamapTab(pressed bool, in *input.State, cl *client.Client) bool {
	if b == nil {
		return false
	}
	if !b.megamapMode() {
		b.megamap.tabPending = false
		if b.megamap.shown {
			b.setMegamapShown(false, cl)
		}
		return false
	}
	if pressed {
		b.megamap.tabPending = true
	}
	if b.megamap.tabPending && (in == nil || in.Kbd == nil || !in.Kbd.KeyHeld(input.KeyTab)) {
		b.megamap.tabPending = false
		// Leaving by key never moves the camera.
		b.setMegamapShown(!b.megamap.shown, cl)
	}
	return pressed
}

// megamapTakesWheel reports whether the modern smooth-zoom wheel steps aside:
// in Megamap mode with the wheel option on the same notch would otherwise both
// zoom and toggle, and while the view is shown the camera it would zoom is
// hidden (DESIGN_INTERFACE_HUD_INPUT §3.15, host choice).
func (b *battleSession) megamapTakesWheel() bool {
	if b == nil {
		return false
	}
	o := b.megamapOptions()
	return o.enabled && o.wheel || b.megamap.shown
}

// serviceMegamapWheel is `WheelZoom`: a notch back enters, a notch forward
// leaves, clearing follow and — with `WheelMoveMegaMap` — centring the camera
// on the pointer's map point. The pointer position does not matter and the
// wheel is not consumed [draw-engine-interface "Entering and leaving"]. A
// notch is one Ebitengine wheel unit of the zoom wheel, which already excludes
// precise trackpad scrolling; fractions bank until they reach one.
func (b *battleSession) serviceMegamapWheel(mouse *input.MouseState, cl *client.Client) {
	if b == nil || mouse == nil {
		return
	}
	o := b.megamapOptions()
	dy := mouse.ZoomScrollY
	if !o.enabled || !o.wheel || dy == 0 {
		if !o.enabled || !o.wheel {
			b.megamap.wheelAccum = 0
		}
		return
	}
	if (dy < 0) != (b.megamap.wheelAccum < 0) {
		b.megamap.wheelAccum = 0
	}
	b.megamap.wheelAccum += dy
	switch {
	case b.megamap.wheelAccum <= -1:
		b.megamap.wheelAccum = 0
		if !b.megamap.shown {
			b.setMegamapShown(true, cl)
		}
	case b.megamap.wheelAccum >= 1:
		b.megamap.wheelAccum = 0
		if b.megamap.shown {
			b.cam.ClearFollow()
			b.pendingFollowInput = nil
			if o.wheelMove {
				b.megamapCentreCamera(int32(mouse.X), int32(mouse.Y))
			}
			b.setMegamapShown(false, cl)
		}
	}
}

// megamapCentreCamera clamps the pointer onto the image, converts it and
// centres the game view on that point within the scroll limits.
func (b *battleSession) megamapCentreCamera(x, y int32) {
	lens := b.megamapLens()
	if b.cam == nil || !lens.Valid() {
		return
	}
	wx, wz := lens.ScreenToWorld(x, y)
	b.cam.ClearFollow()
	b.pendingFollowInput = nil
	b.cam.JumpToBattleViewCenter(wx, wz)
}

// megamapOwnsPointer reports whether a framebuffer point belongs to the view:
// it is shown and the point lies in the battle viewport it covers, not under a
// battle child window [draw-engine-interface "Input while shown"].
func (b *battleSession) megamapOwnsPointer(x, y int32) bool {
	if b == nil || !b.megamap.shown || b.battleState().HasOptionsLayer() || unitInfoCovers(x, y) {
		return false
	}
	return b.megamapLens().ContainsView(x, y) && b.overWorld(x, y)
}

// megamapCursorWorld is the view's branch of the pointer's world point:
// the image point divided by the float scale and truncated, with no shear
// correction, given the terrain height there or sea level
// [draw-engine-interface "Map scale"]. A pointer in the margin is clamped onto
// the image so no hidden-world point is ever resolved.
func (b *battleSession) megamapCursorWorld(x, y int32) (numeric.Fixed, numeric.Fixed, numeric.Fixed, bool) {
	if !b.megamapOwnsPointer(x, y) {
		return 0, 0, 0, false
	}
	lens := b.megamapLens()
	if !lens.Valid() {
		return 0, 0, 0, false
	}
	wx, wz := lens.ScreenToWorld(x, y)
	if b.sess != nil {
		if fx, fy, fz, ok := b.sess.GroundPointAt(wx, wz); ok {
			return fx, fy, fz, true
		}
	}
	return numeric.Fixed(wx) << 16, 0, numeric.Fixed(wz) << 16, true
}

// megamapPickTarget is pickTarget's unit word over the view: the hovered unit
// below, never a hidden-world hull.
func (b *battleSession) megamapPickTarget(f *frame.Frame, x, y int32) (pool.Handle, *units.Unit, bool) {
	if f == nil || !b.megamapOwnsPointer(x, y) {
		return 0, nil, false
	}
	handle := b.megamapHoverUnit(f, x, y)
	if handle == 0 {
		return 0, nil, true
	}
	v, ok := snapshotUnitByHandle(f, handle)
	if !ok {
		return 0, nil, true
	}
	hit := &units.Unit{Handle: handle, Owner: v.Owner, X: v.X, Y: v.Y, Z: v.Z, Flags: v.Flags, Health: v.Health, MaxHealth: v.MaxHealth, Remaining: v.BuildRemaining, Alive: true}
	if b.cat != nil && v.DefName != "" {
		hit.Def, _ = b.cat.Unit(v.DefName)
	}
	return handle, hit, true
}

// megamapUnitSlots indexes the committed units by pool slot, rebuilt once per
// published frame.
func (b *battleSession) megamapUnitSlots(f *frame.Frame) []int {
	if b.megamap.slotScratchForFrame == f && f != nil {
		return b.megamap.slotScratch
	}
	largest := 0
	for i := range f.Units {
		largest = max(largest, int(f.Units[i].Slot))
	}
	slots := b.megamap.slotScratch
	if cap(slots) < largest+1 {
		slots = make([]int, largest+1)
	} else {
		slots = slots[:largest+1]
		clear(slots)
	}
	for i := range f.Units {
		if f.Units[i].Slot != 0 {
			slots[int(f.Units[i].Slot)] = i + 1
		}
	}
	b.megamap.slotScratch, b.megamap.slotScratchForFrame = slots, f
	return slots
}

func megamapUnitView(f *frame.Frame, slots []int, h pool.Handle) (*frame.UnitView, bool) {
	if h == 0 || int(h) >= len(slots) || slots[int(h)] == 0 {
		return nil, false
	}
	return &f.Units[slots[int(h)]-1], true
}

// megamapIdentified is the LOS helper's identification: own units, and units
// the painter's visibility predicate shows the viewer. Anything else admitted
// is a contact drawn with the `nothing` picture.
func megamapIdentified(f *frame.Frame, u *frame.UnitView) bool {
	return u != nil && (u.Owner == f.ViewingPlayer || client.SnapshotVisible(f, *u, f.ViewingPlayer))
}

// megamapAllied is the renderer's ally test against the viewing player.
func megamapAllied(f *frame.Frame, owner uint8) bool {
	viewer := f.ViewingPlayer
	if owner == viewer {
		return true
	}
	return int(viewer) < len(f.Players) && int(owner) < len(f.Players[viewer].Allies) && f.Players[viewer].Allies[owner]
}

// megamapHoverUnit is the hovered unit: the first admitted contact, in list
// order, whose reference point — its position **plus** half its footprint —
// lies strictly inside the 22×22-pixel search box around the pointer's world
// point and then strictly inside a box its current picture's size, each
// converted to world units. The hit area is therefore offset from the drawn
// icon by one footprint, as in the shipped build [draw-engine-interface
// "Hover"]. There is no hover while a box drag is in progress.
func (b *battleSession) megamapHoverUnit(f *frame.Frame, x, y int32) pool.Handle {
	lens := b.megamapLens()
	if f == nil || !b.megamap.shown || b.megamap.boxActive || !lens.ContainsScreen(x, y) {
		return 0
	}
	wx, wz := lens.Unproject(x-lens.X, y-lens.Y)
	sx, sy := lens.ScaleX(), lens.ScaleY()
	searchW, searchH := float64(megamapHoverBox)/2/sx, float64(megamapHoverBox)/2/sy
	slots := b.megamapUnitSlots(f)
	for i := range f.Radar.Contacts {
		p := &f.Radar.Contacts[i]
		if !b.radarUnitContactAdmitted(f, p) {
			continue
		}
		u, ok := megamapUnitView(f, slots, p.Handle)
		if !ok || u.DefID == 0 && u.DefName == "" {
			continue
		}
		refX := float64(radarMapPixel(p.X)) + float64(u.FootX)*8
		refZ := float64(radarMapPixel(p.Z)) + float64(u.FootZ)*8
		dx, dz := refX-float64(wx), refZ-float64(wz)
		if dx <= -searchW || dx >= searchW || dz <= -searchH || dz >= searchH {
			continue
		}
		pw, ph := b.megamapPictureSize(f, p, u)
		halfW, halfH := float64(pw)/2/sx, float64(ph)/2/sy
		if dx <= -halfW || dx >= halfW || dz <= -halfH || dz >= halfH {
			continue
		}
		return p.Handle
	}
	return 0
}

// serviceMegamapPointer runs the view's pointer handler for this host frame
// and returns the sample the ordinary battle controller receives. Every record
// inside the viewport is the view's, so the returned sample carries no button
// state there; a release whose press began outside the view is passed through
// so the HUD capture it belongs to can retire [draw-engine-interface "Input
// while shown"].
func (b *battleSession) serviceMegamapPointer(in *input.State, sample input.Sample, cl *client.Client) input.Sample {
	if b == nil || !b.megamap.shown || in == nil {
		return sample
	}
	lens := b.megamapLens()
	if !lens.Valid() {
		return sample
	}
	mouse, mods := publishedPointer(in)
	mx, my := int32(mouse.X), int32(mouse.Y)
	event, _ := in.CurrentPointer()
	owned := b.megamapOwnsPointer(mx, my)
	if b.megamap.boxActive {
		b.megamap.boxX1, b.megamap.boxY1 = lens.ClampScreen(mx, my)
	}
	consumed := owned
	switch event.Kind {
	case input.LeftDown:
		if owned {
			b.megamap.pressOwned[0] = true
			state := b.battleState().Input
			if lens.ContainsScreen(mx, my) && state.Latch == input.LatchNormal && !b.battleState().PlacementArmed() {
				b.megamap.boxActive = true
				b.megamap.boxX0, b.megamap.boxY0 = lens.ClampScreen(mx, my)
				b.megamap.boxX1, b.megamap.boxY1 = b.megamap.boxX0, b.megamap.boxY0
			}
		}
	case input.LeftDoubleClick:
		if owned {
			b.megamap.pressOwned[0] = true
			b.megamapDoubleClick(cl, mx, my)
		}
	case input.RightDown, input.RightDoubleClick:
		// Right press and right double-click do nothing.
		if owned {
			b.megamap.pressOwned[1] = true
		}
	case input.LeftUp:
		if b.megamap.pressOwned[0] {
			consumed = true
			b.megamap.pressOwned[0] = false
			b.megamapLeftRelease(cl, lens, mx, my, mods.Shift, in.Kbd)
		} else {
			consumed = false
		}
	case input.RightUp:
		if b.megamap.pressOwned[1] {
			consumed = true
			b.megamap.pressOwned[1] = false
			b.megamapRightRelease(cl, mx, my, mods.Shift)
		} else {
			consumed = false
		}
	}
	if !consumed && !b.megamap.pressOwned[0] && !b.megamap.pressOwned[1] {
		return sample
	}
	// The view owns this record: the world-click path must see no press,
	// release or held button.
	sample.Buttons = input.MouseButtons{}
	sample.PressedButtons = [4]bool{}
	sample.ReleasedButtons = [4]bool{}
	sample.Pointer.Kind = input.PointerEventNone
	sample.Pointer.Buttons = input.MouseButtons{}
	return sample
}

// megamapLeftRelease is the left-release branch order: a box of at least nine
// pixels on both axes selects; a prepared order goes to the world-click
// handler; any other release is a click [draw-engine-interface "Input while
// shown"].
func (b *battleSession) megamapLeftRelease(cl *client.Client, lens camera.MegamapLens, mx, my int32, shift bool, kbd *input.KeyboardState) {
	box := b.megamap.boxActive
	b.megamap.boxActive = false
	if b.megamap.lastDoubleValid && b.megamap.lastDoubleX == mx && b.megamap.lastDoubleY == my {
		// The release that completes a double-click is not a click.
		b.megamap.lastDoubleValid = false
		return
	}
	b.megamap.lastDoubleValid = false
	if box {
		w := b.megamap.boxX1 - b.megamap.boxX0
		h := b.megamap.boxY1 - b.megamap.boxY0
		if w < 0 {
			w = -w
		}
		if h < 0 {
			h = -h
		}
		if w >= megamapMinBox && h >= megamapMinBox {
			b.megamapBoxSelect(lens, shift, b.zeroDragFilter(kbd))
			return
		}
	}
	state := b.battleState().Input
	prepared := state.Latch != input.LatchNormal || b.battleState().PlacementArmed()
	if prepared {
		// A prepared order is issued only from the image with a selection;
		// the click branch below is reached with the order still neutral.
		if lens.ContainsScreen(mx, my) && b.hasSelection() {
			b.megamapWorldClick(cl, mx, my, shift)
		}
		return
	}
	b.megamapClick(mx, my, shift)
}

// megamapWorldClick hands a release with a prepared order to the world-click
// handler at the view's pointer point, with Shift. A build reads the
// site-valid bit as last written: set, each selected builder gets the
// mobile-build order and `oktobuild` plays, the placement staying armed only
// with Shift; clear, `notoktobuild` plays, no order is issued and the
// placement stays armed [draw-engine-interface "Build placement from the
// megamap"][07 §9].
//
// TODO(question): the shipped build revalidates the site only through the
// engine's per-frame preview, which needs the engine's own pointer record
// inside the game view. That record is a Supported inference: it would stay
// frozen at the last position the game saw, so after the pointer has crossed
// the build menu every megamap build click would play `notoktobuild` until
// the view closes. Nanolathe keeps its ordinary preview revalidating at the
// megamap point instead (host choice, DESIGN_INTERFACE_HUD_INPUT §3.15), so a
// building chosen from the build menu can still be placed. A manual ProTA 4.8
// test settles it: open the megamap with the pointer over the battlefield,
// choose a building by hotkey and click a legal site (predicted to build),
// then choose one from the build menu and click a legal site (predicted
// `notoktobuild`) [draw-engine-interface "Build placement from the megamap"].
func (b *battleSession) megamapWorldClick(cl *client.Client, mx, my int32, shift bool) {
	// The extensions' mex and wreck click snapping is not taken from the
	// megamap: whether its search finds a site from the megamap's point is
	// Unknown [draw-engine-interface "Click snapping on the megamap"].
	b.communityPlacement.reclaimSnapArmed = false
	if b.battleState().PlacementArmed() {
		if !b.battleState().Input.BuildOK {
			b.playUICue(cl, "notoktobuild")
			return
		}
		b.siteBuildAtMinimapPoint(cl, mx, my, shift)
		return
	}
	code := hud.LatchToCode(b.battleState().Input.Latch)
	if code == 0 {
		return
	}
	if b.orderSelected(code, mx, my, shift) {
		if shift {
			b.battleState().Input.ShiftLatchSticky = true
		} else {
			b.resetOrderLatch()
		}
	}
}

// megamapSelectCursor reports the select-cursor case and the hovered unit.
// The test is the cursor shape the ordinary chooser gives over the megamap's
// hovered unit with the prepared latch, not an ownership test
// [draw-engine-interface "What a megamap send becomes"][07 §8].
func (b *battleSession) megamapSelectCursor(f *frame.Frame, mx, my int32) (pool.Handle, bool) {
	if f == nil || b.cursorShapeAt(b.battleState().Input.Latch, mx, my) != render.CursorSelect {
		return 0, false
	}
	return b.megamapHoverUnit(f, mx, my), true
}

// megamapClick is an ordinary left release: select the own hovered unit, or,
// with a selection, clear it (right-click interface) or send the neutral
// order to the pointer's world point (left-click interface).
func (b *battleSession) megamapClick(mx, my int32, shift bool) {
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	if h, ok := b.megamapSelectCursor(f, mx, my); ok {
		// Only an own selectable, completed hovered unit changes.
		if v, found := snapshotUnitByHandle(f, h); h == 0 || !found || !b.ownSelectableUnit(f, v) {
			return
		}
		kind := session.HumanSelectionReplace
		if shift {
			kind = session.HumanSelectionToggle
		}
		_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{h}}})
		playSelectionCue(b.sess, []pool.Handle{h})
		return
	}
	if !b.hasSelection() {
		return
	}
	if b.interfaceTypeRightClick() {
		_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		return
	}
	b.megamapNeutralOrder(mx, my, shift)
}

// megamapNeutralOrder sends the neutral prepared order at the pointer's world
// point: command code 1 through the ordinary selection broadcast, with the
// hovered unit as target and Shift as the queue flag. Each selected unit
// resolves it separately by the Interface Type contextual rule, and positioned
// move and patrol results keep their nearby offsets
// [draw-engine-interface "What a megamap send becomes"][04 R-ORD-02 §1]
// [04 R-STANCE-01 §5].
func (b *battleSession) megamapNeutralOrder(mx, my int32, shift bool) {
	b.megamapSend(hud.LatchToCode(input.LatchNormal), mx, my, shift)
}

// megamapSend is an order the megamap sends itself: the numeric code, the
// hovered unit as target, the pointer's world point and Shift as the queue
// flag, straight into the selection broadcast. It is not a world click, so
// the armed-click shape gate [07 R-CAM-01 §14] does not apply
// [draw-engine-interface "What a megamap send becomes"].
func (b *battleSession) megamapSend(code int, mx, my int32, shift bool) {
	target, _, pos := b.pickTarget(mx, my)
	if pos == nil {
		return
	}
	_ = b.DispatchOrderCommand(session.HumanOrderCommand{Code: code, Target: target, Position: *pos, Queued: shift})
}

// megamapRightRelease cancels a prepared order; otherwise the left-click
// interface clears a selection and the right-click interface sends the
// prepared order type — Guard when the select cursor shows, else neutral.
//
// Guard is command code 7 on the hovered unit: each selected unit with
// `canguard` gets the ground or air follow order and the others nothing.
// After the send the tidy-up performs the latch-to-idle side effects —
// clearing the Shift persistence bit and resetting the Stop radio group — and
// then restores the latch, so the prepared order stays Guard until the next
// right release cancels it, as the shipped build does
// [draw-engine-interface "What a megamap send becomes"].
func (b *battleSession) megamapRightRelease(cl *client.Client, mx, my int32, shift bool) {
	if b.battleState().PlacementArmed() {
		b.disarmPlacement()
		b.battleState().Input.HUDCaptured = false
		return
	}
	if b.battleState().Input.Latch != input.LatchNormal {
		b.resetOrderLatch()
		return
	}
	if !b.hasSelection() {
		return
	}
	if !b.interfaceTypeRightClick() {
		_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		return
	}
	if f, ok := b.currentSnapshot(); ok {
		if _, guard := b.megamapSelectCursor(f, mx, my); guard {
			b.megamapSend(hud.LatchToCode(input.LatchFollow), mx, my, shift)
			b.resetOrderLatch()
			b.battleState().SetLatch(input.LatchFollow)
			return
		}
	}
	b.megamapNeutralOrder(mx, my, shift)
}

// megamapDoubleClick: with the double-click selection switch on and an own
// hovered unit, select it and extend across the whole map to its definition
// (retail's Ctrl+Z set); the view stays open. A foreign hovered unit stops the
// left-click interface; otherwise `DoubleClickMoveMegamap` centres the camera
// and leaves [draw-engine-interface "Input while shown"].
func (b *battleSession) megamapDoubleClick(cl *client.Client, mx, my int32) {
	b.megamap.boxActive = false
	b.megamap.lastDoubleX, b.megamap.lastDoubleY, b.megamap.lastDoubleValid = mx, my, true
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	h := b.megamapHoverUnit(f, mx, my)
	if h != 0 {
		v, found := snapshotUnitByHandle(f, h)
		if found && v.Owner == f.ViewingPlayer && b.doubleClickSelectionEnabled() {
			if b.ownSelectableUnit(f, v) {
				handles := b.ownSelectableHandles(func(o frame.UnitView) bool { return o.DefID == v.DefID && o.DefName == v.DefName })
				_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: handles}})
				playSelectionCue(b.sess, handles)
			}
			return
		}
		if found && v.Owner != f.ViewingPlayer && !b.interfaceTypeRightClick() {
			return
		}
	}
	if b.megamapOptions().doubleClickMove && b.megamapLens().ContainsScreen(mx, my) {
		b.megamapCentreCamera(mx, my)
		b.setMegamapShown(false, cl)
	}
}

// megamapBoxSelect selects the local player's own completed selectable units
// whose reference point `(x, z − y/2)` lies strictly inside the converted
// rectangle. Without Shift the selection is replaced; with Shift each unit in
// the box is toggled. Retail/Community apply no game-view drag filter;
// Zero adds its host filter (DESIGN_INTERFACE_HUD_INPUT §3.13).
func (b *battleSession) megamapBoxSelect(lens camera.MegamapLens, shift bool, keep func(frame.UnitView) bool) {
	if _, ok := b.currentSnapshot(); !ok {
		return
	}
	x0, z0 := lens.Unproject(min(b.megamap.boxX0, b.megamap.boxX1), min(b.megamap.boxY0, b.megamap.boxY1))
	x1, z1 := lens.Unproject(max(b.megamap.boxX0, b.megamap.boxX1), max(b.megamap.boxY0, b.megamap.boxY1))
	handles := b.ownSelectableHandles(func(v frame.UnitView) bool {
		x := radarMapPixel(v.X)
		z := radarMapPixel(v.Z) - radarMapPixel(v.Y)/2
		return x > x0 && x < x1 && z > z0 && z < z1 && (keep == nil || keep(v))
	})
	kind := session.HumanSelectionReplace
	if shift {
		kind = session.HumanSelectionToggle
	}
	_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: handles}})
	playSelectionCue(b.sess, handles)
}
