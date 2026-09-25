package main

// Modern drag commands are requested input policy, not retail gestures.
// DESIGN_INTERFACE_HUD_INPUT §3.11 owns the geometry and interaction choices.

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	committedframe "github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const commandDragPixels = 6
const commandDragPathLimit = 2048

type dragBuildSite struct {
	cell   dragPoint
	height int32
	valid  bool
}

type battleCommandDrag struct {
	button         input.MouseButton
	latch          input.Latch
	product        string
	facing         units.StructureFacing
	builder        pool.Handle
	selection      []pool.Handle
	start, end     dragPoint
	pressX, pressY int32
	dragged, grid  bool
	path           []dragPoint
	sites          []dragBuildSite
}

func (b *battleSession) beginCommandDrag(cl *client.Client, mouse input.MouseState, modifiers input.Modifiers) bool {
	// Most host samples have no down edge. Avoid scanning selected actors on
	// idle frames; eligibility only matters when a gesture can begin.
	if !mouse.Pressed(input.MouseButtonLeft) && !mouse.Pressed(input.MouseButtonRight) {
		return false
	}
	if cl == nil || !cl.Enhanced() || !cl.IsFocused() || b.cam == nil || b.cat == nil || b.sess == nil {
		return false
	}
	state := &b.battleState().Input
	mx, my := int32(mouse.X), int32(mouse.Y)
	if state.HUDCaptured || state.PlaceCaptured || state.DragActive || b.palettePointerOwned || b.classifyPointer(mx, my) != battlePointerViewport {
		return false
	}
	f, ok := b.currentSnapshot()
	if !ok || len(f.Selection.Handles) == 0 {
		return false
	}
	button := input.MouseButtonLeft
	switch state.Latch {
	case input.LatchMobileBuild:
		if !b.battleState().PlacementArmed() {
			return false
		}
	case input.LatchRepair, input.LatchReclaim, input.LatchMove:
	case input.LatchNormal:
		if modifiers.Ctrl {
			return false
		}
		// Alt-left explicitly requests movement in either interface mode. Armed
		// commands above keep their own gestures, including Alt construction grids.
		if !(modifiers.Alt && mouse.Pressed(input.MouseButtonLeft)) {
			if !b.interfaceTypeRightClick() || !mouse.Pressed(input.MouseButtonRight) {
				return false
			}
			target, _, pos := b.pickTarget(mx, my)
			if target != 0 || pos == nil || pos.HasFeature {
				return false
			}
			button = input.MouseButtonRight
		}
		if len(b.dragMoveActors(f)) == 0 {
			return false
		}
	default:
		return false
	}
	if !mouse.Pressed(button) {
		return false
	}
	wx, _, wz := b.cursorWorld(mx, my)
	p := dragPoint{int32(wx.Floor()), int32(wz.Floor())}
	d := &battleCommandDrag{button: button, latch: state.Latch, product: state.BuildDef, builder: f.CommandPage.Builder,
		selection: slices.Clone(f.Selection.Handles), start: p, end: p, pressX: mx, pressY: my, path: []dragPoint{p}, grid: modifiers.Alt}
	if state.Latch == input.LatchMobileBuild {
		b.updatePlacement(mx, my)
		if def, ok := b.cat.Unit(d.product); ok {
			d.facing = b.communityPlacementFacing(def)
		}
		d.start = dragPoint{state.BuildCellX, state.BuildCellZ}
		d.end = d.start
	}
	b.resourceClick = nil
	b.modernDrag = d
	b.updateCommandDrag(cl, mx, my, modifiers)
	return true
}

// An active capture runs before minimap and HUD routing, so crossing chrome
// cannot turn the release into a different command. Lost ownership cancels it.
func (b *battleSession) serviceCommandDrag(in *input.State, cl *client.Client, mouse input.MouseState, modifiers input.Modifiers) bool {
	d := b.modernDrag
	if d == nil {
		return false
	}
	state := &b.battleState().Input
	f, ok := b.currentSnapshot()
	cancelled := !ok || cl == nil || !cl.Enhanced() || !cl.IsFocused() || b.palettePointerOwned ||
		state.Latch != d.latch || state.BuildDef != d.product || !slices.Equal(f.Selection.Handles, d.selection) ||
		(d.product != "" && f.CommandPage.Builder != d.builder)
	kbd := battleShortcutKeyboard(in)
	if kbd.KeyDown(input.KeyEscape) || (d.button == input.MouseButtonLeft && mouse.Pressed(input.MouseButtonRight)) {
		b.modernDrag = nil
		b.disarmPlacement()
		b.resetOrderLatch()
		state.PlaceCaptured = mouse.Held(input.MouseButtonLeft)
		return true
	}
	if cancelled {
		b.modernDrag = nil
		return true
	}
	if in.ShortcutTokenMode && in.ShortcutToken.Kind != input.TokenNone {
		b.modernDrag = nil
		state.PlaceCaptured = d.button == input.MouseButtonLeft && mouse.Held(d.button)
		return false
	}
	for key := input.Key(1); key < input.KeyCount; key++ {
		if key != input.KeyShift && key != input.KeyAlt && key != input.KeyCtrl && in.Kbd.KeyDown(key) {
			b.modernDrag = nil
			state.PlaceCaptured = d.button == input.MouseButtonLeft && mouse.Held(d.button)
			return false // The new command owns this sample, including its hotkey.
		}
	}
	mx, my := int32(mouse.X), int32(mouse.Y)
	state.PointerX, state.PointerY = mx, my
	state.ShiftHeld = modifiers.Shift
	b.updateCommandDrag(cl, mx, my, modifiers)
	if mouse.Held(d.button) {
		return true
	}
	defer func() { b.modernDrag = nil }()
	// A missing release edge (e.g. a focus transition) never commits stale work.
	if !mouse.Released(d.button) {
		return true
	}
	// A refused short release issued nothing, and the armed latch survives it
	// exactly as it does on the retail click path: [07 R-CAM-01 §14] step 3
	// not being taken means nothing happens, and a latch returning to idle
	// would be something happening. The dragged branches below either
	// dispatch or return early, so they keep the plain retire.
	issued := true
	if !d.dragged {
		if d.product != "" {
			b.updatePlacement(mx, my)
			if !state.BuildOK {
				b.playUICue(cl, "notoktobuild")
				return true
			}
			if b.commitBuild(modifiers.Shift) {
				b.playUICue(cl, "oktobuild")
			} else {
				b.disarmPlacement()
				return true
			}
		} else {
			code := hud.LatchToCode(d.latch)
			if d.latch == input.LatchNormal && d.button == input.MouseButtonLeft {
				// A short Alt gesture is an explicit point move, even over a target.
				code = hud.LatchToCode(input.LatchMove)
			}
			issued = b.orderSelected(code, mx, my, modifiers.Shift)
		}
	} else {
		switch d.latch {
		case input.LatchMobileBuild:
			accepted := false
			for _, site := range d.sites {
				if !site.valid {
					continue
				}
				wx, wz := world.PlacementCenter(site.cell.x, site.cell.z, state.BuildFootX, state.BuildFootZ)
				if b.dispatchMobileBuildFacing(d.product, wx, numeric.FixedFromInt(int64(site.height)), wz, modifiers.Shift || accepted, true, d.facing) == nil {
					accepted = true
				}
			}
			if !accepted {
				b.playUICue(cl, "notoktobuild")
				return true
			}
			b.playUICue(cl, "oktobuild")
		case input.LatchRepair, input.LatchReclaim:
			targets := b.dragAreaTargets(f, d)
			if len(targets) == 0 {
				return true
			}
			_ = b.DispatchOrderCommand(session.HumanOrderCommand{Handles: b.selectedHandlesInSlotOrder(), Code: hud.LatchToCode(d.latch), Targets: targets, Queued: modifiers.Shift})
		default:
			actors := b.dragMoveActors(f)
			points := make([]dragPoint, len(actors))
			for i, h := range actors {
				v, _ := snapshotUnitByHandle(f, h)
				points[i] = dragPoint{int32(v.X.Floor()), int32(v.Z.Floor())}
			}
			goals := dragAssignDestinations(points, dragSamplePath(d.path, len(actors)))
			for i, p := range goals {
				pos := dragGroundPosition(cl, p)
				_ = b.DispatchOrderCommand(session.HumanOrderCommand{Handles: []pool.Handle{actors[i]}, Code: hud.LatchToCode(input.LatchMove), Position: pos, Queued: modifiers.Shift, AssignedPosition: true})
			}
		}
	}
	if d.product != "" {
		if modifiers.Shift {
			state.BuildSticky = true
		} else {
			b.disarmPlacement()
		}
	} else if d.latch != input.LatchNormal && issued {
		if modifiers.Shift {
			state.ShiftLatchSticky = true
		} else {
			b.resetOrderLatch()
		}
	}
	return true
}

func (b *battleSession) updateCommandDrag(cl *client.Client, mx, my int32, modifiers input.Modifiers) {
	d := b.modernDrag
	if d == nil {
		return
	}
	dx, dy := int64(mx-d.pressX), int64(my-d.pressY)
	d.dragged = d.dragged || dx*dx+dy*dy >= commandDragPixels*commandDragPixels
	wx, _, wz := b.cursorWorld(mx, my)
	p := dragPoint{int32(wx.Floor()), int32(wz.Floor())}
	d.end = p
	d.grid = modifiers.Alt
	if d.product != "" {
		state := &b.battleState().Input
		d.end.x, d.end.z = world.PlacementAnchor(wx, wz, state.BuildFootX, state.BuildFootZ)
		end := d.end
		if !d.dragged {
			end = d.start
		}
		cells := dragBuildCells(d.start, end, state.BuildFootX, state.BuildFootZ, d.grid)
		reserved, complete := b.resourceReservations()
		def, _ := b.cat.Unit(d.product)
		d.facing = b.communityPlacementFacing(def)
		d.sites = d.sites[:0]
		for _, cell := range cells {
			result, err := b.checkProductPlacement(cell.x, cell.z, def, state.BuildFootX, state.BuildFootZ, uint16(d.builder))
			valid := err == nil && complete
			rect := resourceRect{cell.x, cell.z, state.BuildFootX, state.BuildFootZ}
			for _, r := range reserved {
				if rect.overlaps(r) {
					valid = false
					break
				}
			}
			d.sites = append(d.sites, dragBuildSite{cell: cell, height: result.SiteHeight, valid: valid})
		}
		return
	}
	if d.latch == input.LatchMove || d.latch == input.LatchNormal {
		if d.path[len(d.path)-1] != p {
			if len(d.path) < commandDragPathLimit {
				d.path = append(d.path, p)
			} else {
				d.path[len(d.path)-1] = p
			}
		}
	}
}

// dragMoveActors and dragAreaTargets read the committed frame they are given:
// the host step passes the newest publication, the drag preview the one the
// pass presents (presentedSnapshot). The local player is the frame's own, so
// a preview drawn while the simulation goroutine runs reads nothing live.
func (b *battleSession) dragMoveActors(f *committedframe.Frame) []pool.Handle {
	if f == nil || b.cat == nil {
		return nil
	}
	var out []pool.Handle
	for _, h := range b.selectedHandlesInSlotOrder() {
		v, found := snapshotUnitByHandle(f, h)
		if !found || v.Owner != f.Selection.LocalPlayer || v.BuildRemaining != 0 {
			continue
		}
		def, found := b.cat.Unit(v.DefName)
		if found && def != nil && def.CanMove && def.BMCode != 0 {
			out = append(out, h)
		}
	}
	return out
}

func (b *battleSession) dragAreaTargets(f *committedframe.Frame, d *battleCommandDrag) []session.HumanOrderTarget {
	if f == nil {
		return nil
	}
	inside := func(x, z numeric.Fixed) bool {
		return x.Floor() >= int64(min(d.start.x, d.end.x)) && x.Floor() <= int64(max(d.start.x, d.end.x)) && z.Floor() >= int64(min(d.start.z, d.end.z)) && z.Floor() <= int64(max(d.start.z, d.end.z))
	}
	var out []session.HumanOrderTarget
	if d.latch == input.LatchRepair {
		for _, v := range f.Units {
			if v.Owner != f.Selection.LocalPlayer || (v.Health >= v.MaxHealth && v.BuildRemaining == 0) || !inside(v.X, v.Z) || !client.SnapshotVisible(f, v, f.ViewingPlayer) {
				continue
			}
			out = append(out, session.HumanOrderTarget{Target: v.Slot, Position: orders.ResolvePos{X: v.X, Y: v.Y, Z: v.Z}})
		}
	} else {
		for _, v := range f.Features {
			// The authored blocking flag covers movement and building placement
			// [05 R-FEAT-01 §6]; drag reclaim clears only these obstacles (§3.11).
			if !v.Reclaimable || !v.Blocking || !inside(v.X, v.Z) || !snapshotFeatureVisible(f, v, f.ViewingPlayer) {
				continue
			}
			wreck := b.isCorpseName(v.DefName)
			out = append(out, session.HumanOrderTarget{Position: orders.ResolvePos{X: v.X, Y: v.Y, Z: v.Z, HasFeature: true, IsWreck: wreck, FeatureResurrectable: wreck}})
		}
	}
	return out
}

func dragGroundPosition(cl *client.Client, p dragPoint) orders.ResolvePos {
	x, z := numeric.FixedFromInt(int64(p.x)), numeric.FixedFromInt(int64(p.z))
	return orders.ResolvePos{X: x, Y: cl.GroundHeightAt(x, z), Z: z}
}

// All markers use the same world overlay transform as queued orders. Keeping
// pointer coordinates out of this bracket preserves detail and strategic zoom.
func (b *battleSession) drawCommandDrag(cl *client.Client) {
	d := b.modernDrag
	if d == nil || cl == nil || !cl.Enhanced() || b.cam == nil {
		return
	}
	green, red := cl.GUIColor(hud.GhostColorLegal), cl.GUIColor(hud.GhostColorIllegal)
	if d.product != "" {
		state := &b.battleState().Input
		for _, site := range d.sites {
			color := red
			if site.valid {
				color = green
			}
			l, t, r, bt := b.siteRectToScreen(site.cell.x*16, site.cell.z*16, (site.cell.x+state.BuildFootX)*16, (site.cell.z+state.BuildFootZ)*16, site.height)
			cl.UIFrameRect(int(l), int(t), int(r-l), int(bt-t), color)
			cl.UIFrameRect(int(l)+1, int(t)+1, int(r-l)-2, int(bt-t)-2, color)
		}
		return
	}
	if !d.dragged {
		return
	}
	presented, _ := b.presentedSnapshot(cl)
	project := func(pos orders.ResolvePos) hud.QueuePoint {
		x, y := cl.WorldToScreenPx(pos.X, pos.Y, pos.Z)
		return hud.QueuePoint{X: x - camera.OriginX, Y: y - camera.OriginY}
	}
	mark := func(pos orders.ResolvePos) { p := project(pos); cl.UIFrameRect(int(p.X)-3, int(p.Y)-3, 7, 7, green) }
	line := func(a, z dragPoint) {
		p, q := project(dragGroundPosition(cl, a)), project(dragGroundPosition(cl, z))
		cl.UIWorldLine(p.X, p.Y, q.X, q.Y, green)
	}
	if d.latch == input.LatchRepair || d.latch == input.LatchReclaim {
		a, z := d.start, d.end
		line(a, dragPoint{z.x, a.z})
		line(dragPoint{z.x, a.z}, z)
		line(z, dragPoint{a.x, z.z})
		line(dragPoint{a.x, z.z}, a)
		for _, target := range b.dragAreaTargets(presented, d) {
			mark(target.Position)
		}
	} else {
		for i := 1; i < len(d.path); i++ {
			line(d.path[i-1], d.path[i])
		}
		for _, p := range dragSamplePath(d.path, len(b.dragMoveActors(presented))) {
			mark(dragGroundPosition(cl, p))
		}
	}
}
