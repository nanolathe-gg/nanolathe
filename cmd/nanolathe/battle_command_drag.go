package main

// Modern drag commands are requested input policy, not retail gestures.
// DESIGN_INTERFACE_HUD_INPUT §3.11 owns the geometry and interaction choices.

import (
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
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
	// cancel marks a Shift row site that removes an existing queued build
	// instead of adding one; cancelled is that build's footprint and height,
	// which the preview crosses out (§3.11).
	cancel          bool
	cancelled       resourceRect
	cancelledHeight int32
}

// dragQueuedPointTolerance is the queued-order toggle's match distance: one
// map cell on each of X and Z, inclusive [07 R-P0-11 §6]. The session applies
// the same test when the toggling command arrives.
const dragQueuedPointTolerance = numeric.Fixed(16 << 16)

// dragQueuedBuild is one primary-queue record of the drag's builder, in queue
// order, as the release's toggling commands will find it.
type dragQueuedBuild struct {
	goalX, goalY, goalZ numeric.Fixed
	footX, footZ        int32
	product             string
	build               bool // this builder's own mobile-build order kind
	taken               bool // an earlier site of this gesture cancels it
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
		// Rows and grids are the player's choice; off, the click places one
		// site through the ordinary path.
		if !b.battleState().PlacementArmed() || b.hostPreferences().BuildDrag == 0 {
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
			// An admitted hostile radar dot owns the contextual attack press;
			// it is not empty ground for a move drag (interface design
			// "Modern radar dots"). Explicit Alt-left movement stays above.
			if h, _ := b.pickRadarAttackTarget(mx, my, input.LatchNormal); h != 0 {
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
	if d.product != "" {
		b.stepBuildDragGap(mouse.ZoomScrollY, mouse.PanY)
	}
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
				if !site.valid && !site.cancel {
					continue
				}
				// A new site appends without the repeated-site toggle; a cancel
				// site sends exactly the queued command a Shift-click there
				// sends, whose toggle removes the queued build [07 R-P0-11 §6].
				queued, appendOnly := modifiers.Shift || accepted, true
				if site.cancel {
					queued, appendOnly = true, false
				}
				wx, wz := world.PlacementCenter(site.cell.x, site.cell.z, state.BuildFootX, state.BuildFootZ)
				if b.dispatchMobileBuildFacing(d.product, wx, numeric.FixedFromInt(int64(site.height)), wz, queued, appendOnly, d.facing) == nil {
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
			b.dispatchAreaWork(f, hud.LatchToCode(d.latch), targets, modifiers.Shift)
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
		cells := dragBuildCells(d.start, end, state.BuildFootX, state.BuildFootZ, b.buildDragGap, d.grid)
		reserved, complete := b.resourceReservations()
		def, _ := b.cat.Unit(d.product)
		d.facing = b.communityPlacementFacing(def)
		// Only a queued (Shift) release runs the toggle, as with the click.
		var queue []dragQueuedBuild
		if modifiers.Shift {
			if f, ok := b.currentSnapshot(); ok {
				queue = b.dragCancelQueue(f, d.builder)
			}
		}
		d.sites = d.sites[:0]
		for _, cell := range cells {
			result, err := b.checkProductPlacement(cell.x, cell.z, def, state.BuildFootX, state.BuildFootZ, uint16(d.builder))
			if err == nil && len(queue) != 0 {
				// The click admits a site only when placement accepts it,
				// and so does its cancellation. Before the gesture becomes a
				// drag it is still that click, whose match ignores the product.
				wx, wz := world.PlacementCenter(cell.x, cell.z, state.BuildFootX, state.BuildFootZ)
				if i := dragCancelMatch(queue, wx, wz, d.product, !d.dragged); i >= 0 {
					n := &queue[i]
					n.taken = true
					site := dragBuildSite{cell: cell, height: result.SiteHeight, cancel: true, cancelledHeight: int32(n.goalY.Floor())}
					site.cancelled = resourceRect{cell.x, cell.z, state.BuildFootX, state.BuildFootZ}
					if n.footX > 0 && n.footZ > 0 {
						x, z := world.PlacementAnchor(n.goalX, n.goalZ, n.footX, n.footZ)
						site.cancelled = resourceRect{x, z, n.footX, n.footZ}
					}
					d.sites = append(d.sites, site)
					continue
				}
			}
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

// buildDragWheelOwned reports whether the wheel sets the build-drag gap rather
// than zooming: for as long as a build row or grid holds the pointer (§3.11).
func (b *battleSession) buildDragWheelOwned() bool {
	return b != nil && b.modernDrag != nil && b.modernDrag.product != ""
}

// stepBuildDragGap spends the wheel on the build-drag gap, one free cell per
// whole notch, wheel up widening. Precise scrolling (a trackpad or a Magic
// Mouse) carries no notch count, so its vertical travel counts one notch per
// dragBuildGapPanStep logical pixels. Fractions accumulate until they make a
// notch. The gap stays for the rest of the battle.
func (b *battleSession) stepBuildDragGap(wheel float32, panY float64) {
	if wheel == 0 && panY == 0 {
		return
	}
	b.buildDragWheel += wheel + float32(panY/dragBuildGapPanStep)
	steps := int32(b.buildDragWheel)
	if steps == 0 {
		return
	}
	b.buildDragWheel -= float32(steps)
	b.buildDragGap = min(max(b.buildDragGap+steps, 0), dragBuildGapMax)
}

// dragCancelQueue copies the drag builder's committed primary queue for the
// cancellation preview (§3.11). It returns nil — nothing can be cancelled —
// when the committed queue cannot prove what the release's commands will
// meet: a truncated published queue, or any typed command still awaiting its
// tick, since that command may edit the queue first. Both clear within a tick.
func (b *battleSession) dragCancelQueue(f *committedframe.Frame, builder pool.Handle) []dragQueuedBuild {
	if f == nil || b.cat == nil || b.sess == nil || len(b.sess.PendingHumanCommands()) != 0 {
		return nil
	}
	v, ok := snapshotUnitByHandle(f, builder)
	if !ok {
		return nil
	}
	def, ok := b.cat.Unit(v.DefName)
	if !ok || def == nil {
		return nil
	}
	// The session issues the flying builder's own kind; the toggle matches
	// only records of the kind being issued [07 R-P0-11 §6].
	kind := construction.MobileBuildOrder
	if def.CanFly {
		kind = construction.VTOLMobileBuildOrder
	}
	for _, q := range f.OrderQueues {
		if q.Unit != builder {
			continue
		}
		if q.PrimaryTruncated {
			return nil
		}
		out := make([]dragQueuedBuild, 0, len(q.Primary))
		for _, o := range q.Primary {
			out = append(out, dragQueuedBuild{goalX: o.GoalX, goalY: o.GoalY, goalZ: o.GoalZ, footX: int32(o.FootX), footZ: int32(o.FootZ), product: o.BuildProduct, build: o.Kind == kind})
		}
		return out
	}
	return nil
}

// dragCancelMatch finds the record the queued toggle at (wx, wz) removes: the
// front-most untaken record of the builder's own build kind whose goal lies
// within one cell on X and on Z [07 R-P0-11 §6]. A drag row cancels it only
// when it is the dragged product; otherwise the toggle would remove another
// building, so the row reports no cancellation (-1) there.
func dragCancelMatch(queue []dragQueuedBuild, wx, wz numeric.Fixed, product string, anyProduct bool) int {
	within := func(a, b numeric.Fixed) bool {
		d := a - b
		if d < 0 {
			d = -d
		}
		return d <= dragQueuedPointTolerance
	}
	for i := range queue {
		n := &queue[i]
		if n.taken || !n.build || !within(n.goalX, wx) || !within(n.goalZ, wz) {
			continue
		}
		if anyProduct || content.CanonicalKey(n.product) == content.CanonicalKey(product) {
			return i
		}
		return -1
	}
	return -1
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

// dispatchAreaWork orders an area's work list nearest-first for the selected
// workers (DESIGN_INTERFACE_HUD_INPUT §3.11). Workers are the `canreclamate`
// definitions repair and reclaim resolve for [04 R-ORD-02 §1]. Each worker,
// or each group of nearby workers when the budget below is short, gets one
// command whose copy of the list is a nearest-next walk from where it stands,
// so it works through the area instead of crossing it in publication order.
// Every actor already works through an area list on its own, so splitting the
// command changes only the visiting order. The other selected actors, which
// queue nothing, and any actor the frame cannot place or name ride in the
// first command; with no worker they share one command in publication order.
func (b *battleSession) dispatchAreaWork(f *committedframe.Frame, code int, targets []session.HumanOrderTarget, queued bool) {
	selected := b.selectedHandlesInSlotOrder()
	var workers []int // indices into selected
	var positions []dragPoint
	for i, h := range selected {
		v, found := snapshotUnitByHandle(f, h)
		var def *content.UnitDef
		if found && b.cat != nil {
			def, found = b.cat.Unit(v.DefName)
		}
		if found && def != nil && def.CanReclamate {
			workers = append(workers, i)
			positions = append(positions, dragPoint{int32(v.X.Floor()), int32(v.Z.Floor())})
		}
	}
	if len(workers) == 0 {
		if len(selected) != 0 {
			_ = b.DispatchOrderCommand(session.HumanOrderCommand{Handles: selected, Code: code, Targets: targets, Queued: queued})
		}
		return
	}
	points := make([]dragPoint, len(targets))
	for i := range targets {
		points[i] = dragPoint{int32(targets[i].Position.X.Floor()), int32(targets[i].Position.Z.Floor())}
	}
	// Every selected actor belongs to exactly one command, in selection order;
	// non-workers join the first group's.
	group := make([]int, len(selected))
	groups, starts := dragAreaGroups(positions, len(targets), b.areaCommandLimit())
	for g, members := range groups {
		for _, w := range members {
			group[workers[w]] = g
		}
	}
	for g := range groups {
		walk := dragNearestChain(starts[g], points)
		ordered := make([]session.HumanOrderTarget, len(walk))
		for i, j := range walk {
			ordered[i] = targets[j]
		}
		var handles []pool.Handle
		for i, h := range selected {
			if group[i] == g {
				handles = append(handles, h)
			}
		}
		_ = b.DispatchOrderCommand(session.HumanOrderCommand{Handles: handles, Code: code, Targets: ordered, Queued: queued})
	}
}

// areaCommandLimit is how many commands one area gesture may send: the
// single-player fan-out budget, or one online, where the relay's pending
// budget is shared by every seat (DESIGN_INTERFACE_HUD_INPUT §3.11).
func (b *battleSession) areaCommandLimit() int {
	if b.onlineBattle() {
		return 1
	}
	return dragAreaCommandLimit
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
			if site.cancel {
				// A crossed-out red box over the queued build the release removes.
				c := site.cancelled
				l, t, r, bt := b.siteRectToScreen(c.x*16, c.z*16, (c.x+c.w)*16, (c.z+c.h)*16, site.cancelledHeight)
				cl.UIFrameRect(int(l), int(t), int(r-l), int(bt-t), red)
				cl.UIFrameRect(int(l)+1, int(t)+1, int(r-l)-2, int(bt-t)-2, red)
				cl.UIWorldLine(l, t, r-1, bt-1, red)
				cl.UIWorldLine(r-1, t, l, bt-1, red)
				continue
			}
			color := red
			if site.valid {
				color = green
			}
			l, t, r, bt := b.siteRectToScreen(site.cell.x*16, site.cell.z*16, (site.cell.x+state.BuildFootX)*16, (site.cell.z+state.BuildFootZ)*16, site.height)
			cl.UIFrameRect(int(l), int(t), int(r-l), int(bt-t), color)
			cl.UIFrameRect(int(l)+1, int(t)+1, int(r-l)-2, int(bt-t)-2, color)
		}
		if d.dragged && b.hud != nil && b.hud.console != nil {
			// The wheel's gap beside the pointer, so a row's spacing is read
			// before it is released.
			cl.UIText(b.hud.console, fmt.Sprintf("gap %d", b.buildDragGap), int(state.PointerX)+14, int(state.PointerY)+14, green)
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
