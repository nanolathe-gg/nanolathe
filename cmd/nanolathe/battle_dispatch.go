package main

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
)

func snapshotUnitByHandle(f *snapshot.Frame, h pool.Handle) (snapshot.UnitView, bool) {
	if f == nil {
		return snapshot.UnitView{}, false
	}
	for i := range f.Units {
		if f.Units[i].Slot == h {
			return f.Units[i], true
		}
	}
	return snapshot.UnitView{}, false
}

// battleCommandKind identifies the authoritative mutation requested by one
// production input event. Keeping the command typed prevents screen-space
// coordinates and UI proxy bits from leaking across the battle/application
// boundary [07 §9].
type battleCommandKind uint8

const (
	battleCommandSelectionReplace battleCommandKind = iota + 1
	battleCommandSelectionToggle
	battleCommandSelectionClear
	battleCommandOrder
	battleCommandStop
	battleCommandActivation
	battleCommandMobileBuild
	battleCommandFactoryBuild
	battleCommandCancelProduction
	battleCommandStockpile
	battleCommandBuildPage
	battleCommandGroupAssign
	battleCommandGroupRecall
)

// battleCommand is the narrow, typed input-to-session payload. Exactly one
// payload branch is populated for each command kind; no command carries a
// guessed descriptor or a screen coordinate.
type battleCommand struct {
	Kind             battleCommandKind
	Order            battleOrderCommand
	Activation       battleActivationCommand
	MobileBuild      battleMobileBuildCommand
	FactoryBuild     battleFactoryBuildCommand
	CancelProduction battleCancelProductionCommand
	Stockpile        battleStockpileCommand
	BuildPage        battleBuildPageCommand
	Group            battleGroupCommand
	Selection        battleSelectionCommand
}

type battleSelectionCommand struct{ Handles []pool.Handle }

type battleOrderCommand struct {
	Latch    input.Latch
	Handles  []pool.Handle
	Target   pool.Handle
	Position orders.ResolvePos
	Queued   bool
}

type battleActivationCommand struct {
	Unit     pool.Handle
	Activate bool
	Queued   bool
}

type battleMobileBuildCommand struct {
	Builder    pool.Handle
	Product    string
	WX, WY, WZ numeric.Fixed
	Queued     bool
}

type battleFactoryBuildCommand struct {
	Builder pool.Handle
	Product string
	Queued  bool
}

type battleCancelProductionCommand struct{ Unit pool.Handle }

type battleStockpileCommand struct {
	Unit   pool.Handle
	Queued bool
}

type battleBuildPageCommand struct {
	Builder pool.Handle
	Page    int
}

type battleGroupCommand struct {
	Group    int
	Preserve bool
	Mask     [32]byte
}

// battleDispatch is the narrow injection surface the HUD and battle input
// controller use to issue authoritative commands without importing session
// internals directly [R-P0-03][07 §9]. Cycle ON-09/session must bind these to
// the real construction/order pumps.
//
// A future composition layer may bind the typed command records to
// session-owned pumps. The current battle application uses the direct typed
// fallback below:
//   - mobileBuild: construction.QueueMobileBuild(builder, product, wx, wz, 1, cat)
//   - factoryBuild: construction.QueueFactoryBuild(factory, product, 1, cat)
//   - order/stop/activation/stockpile/cancel: orders.Resolve or the canonical
//     descriptor transition, then Queue.Push via session
//
// The present battleSession implements this interface via its methods; headless
// tests can supply recording doubles without a live Session.
//
// Injection points session must bind (short):
//
//	mobileBuildFn(product string, wx, wz numeric.Fixed, queued bool) error
//	factoryBuildFn(product string, queued bool) error
//	orderDispatchFn(latch input.Latch, x, y int32, queued bool)
//
// All are func fields on battleSession; when nil the fallback construction/
// order paths run. Tests inject recording funcs to verify coordinates and
// product names data-driven from BuildMenus without needing a live Session.
type battleDispatch interface {
	DispatchMobileBuild(product string, wx, wz numeric.Fixed, queued bool) error
	DispatchFactoryBuild(product string, queued bool) error
	DispatchOrderLatch(latch input.Latch, x, y int32, queued bool)
	DispatchOrderCommand(cmd battleOrderCommand) error
	DispatchActivation(cmd battleActivationCommand) error
	DispatchCancelProduction(unit pool.Handle) error
	DispatchStockpile(unit pool.Handle, queued bool) error
	DispatchGroupAssign(group int) error
	DispatchGroupRecall(group int, preserve bool) error
}

var _ battleDispatch = (*battleSession)(nil)

// These stubs are overridden in battle.go with real logic; they exist here
// only to document the injected contract for ON-09.
func (b *battleSession) DispatchMobileBuild(product string, wx, wz numeric.Fixed, queued bool) error {
	var builder pool.Handle
	if f, ok := b.currentSnapshot(); ok {
		builder = f.CommandPage.Builder
		if v, found := snapshotUnitByHandle(f, builder); !found || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
			builder = 0
		}
	}
	if b.requireCommandDispatch && builder == 0 {
		return fmt.Errorf("battle: production build has no snapshot builder")
	}
	if builder == 0 && !b.requireCommandDispatch {
		if u := b.selectedBuilder(); u != nil {
			builder = u.Handle
		}
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandMobileBuild, MobileBuild: battleMobileBuildCommand{
		Builder: builder, Product: product, WX: wx, WZ: wz, Queued: queued,
	}})
}
func (b *battleSession) DispatchFactoryBuild(product string, queued bool) error {
	var builder pool.Handle
	if f, ok := b.currentSnapshot(); ok {
		builder = f.CommandPage.Builder
		if v, found := snapshotUnitByHandle(f, builder); !found || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
			builder = 0
		}
	}
	if b.requireCommandDispatch && builder == 0 {
		return fmt.Errorf("battle: production factory has no snapshot builder")
	}
	if builder == 0 && !b.requireCommandDispatch {
		if u := b.selectedFactory(); u != nil {
			builder = u.Handle
		}
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandFactoryBuild, FactoryBuild: battleFactoryBuildCommand{
		Builder: builder, Product: product, Queued: queued,
	}})
}

func (b *battleSession) snapshotBuilder(v snapshot.UnitView) bool {
	if b == nil || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(v.DefName)
	return ok && def != nil && def.Builder
}
func (b *battleSession) DispatchOrderLatch(latch input.Latch, x, y int32, queued bool) {
	// This compatibility entry point is retained for HUD integrations that
	// still provide a screen point. Production world dispatch uses
	// DispatchOrderCommand below, after picking has produced a typed payload.
	if b.orderDispatchFn != nil {
		b.orderDispatchFn(latch, x, y, queued)
	}
}

// DispatchOrderCommand submits a picked, typed order payload. The command
// contains no screen-space origin, so an order cannot be preceded by an
// accidental contextual action at (0,0) [04 §3.4][07 §9].
func (b *battleSession) DispatchOrderCommand(cmd battleOrderCommand) error {
	if b.requireCommandDispatch {
		if _, ok := b.currentSnapshot(); !ok {
			return fmt.Errorf("battle: production order has no current snapshot")
		}
		// Empty handles mean resolve the authoritative selection at the input
		// boundary, after any earlier queued selection command.
		cmd.Handles = nil
	}
	if len(cmd.Handles) == 0 && !b.requireCommandDispatch {
		cmd.Handles = b.selectedHandles()
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandOrder, Order: cmd})
}

func (b *battleSession) dispatchStopCommand() error {
	return b.submitBattleCommand(battleCommand{Kind: battleCommandStop})
}

func (b *battleSession) DispatchActivation(cmd battleActivationCommand) error {
	return b.submitBattleCommand(battleCommand{Kind: battleCommandActivation, Activation: cmd})
}

func (b *battleSession) DispatchCancelProduction(unit pool.Handle) error {
	return b.submitBattleCommand(battleCommand{Kind: battleCommandCancelProduction, CancelProduction: battleCancelProductionCommand{Unit: unit}})
}

func (b *battleSession) DispatchStockpile(unit pool.Handle, queued bool) error {
	return b.submitBattleCommand(battleCommand{Kind: battleCommandStockpile, Stockpile: battleStockpileCommand{Unit: unit, Queued: queued}})
}

// DispatchBuildPage submits an absolute authored page selected by the
// presentation controls. Production resolves the builder from the immutable
// CommandPage and the session validates it again at the input boundary [07 §9].
func (b *battleSession) DispatchBuildPage(page int) error {
	var builder pool.Handle
	if f, ok := b.currentSnapshot(); ok {
		builder = f.CommandPage.Builder
		if builder == 0 || page < 0 || page >= int(f.CommandPage.PageCount) {
			return fmt.Errorf("battle: invalid build page %d", page)
		}
	} else if !b.requireCommandDispatch {
		if u := b.selectedBuilder(); u != nil {
			builder = u.Handle
		}
	}
	if b.requireCommandDispatch && builder == 0 {
		return fmt.Errorf("battle: build page has no snapshot builder")
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandBuildPage, BuildPage: battleBuildPageCommand{Builder: builder, Page: page}})
}

func (b *battleSession) DispatchGroupAssign(group int) error {
	if group < 1 || group > 9 {
		return fmt.Errorf("battle: invalid control group %d", group)
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandGroupAssign, Group: battleGroupCommand{Group: group}})
}

func (b *battleSession) DispatchGroupRecall(group int, preserve bool) error {
	if group < 1 || group > 9 {
		return fmt.Errorf("battle: invalid control group %d", group)
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandGroupRecall, Group: battleGroupCommand{Group: group, Preserve: preserve}})
}

func (b *battleSession) submitBattleCommand(cmd battleCommand) error {
	if b == nil {
		return nil
	}
	if b.commandDispatchFn != nil {
		return b.commandDispatchFn(cmd)
	}
	if b.requireCommandDispatch {
		return fmt.Errorf("battle: production command dispatch is unbound")
	}
	switch cmd.Kind {
	case battleCommandOrder:
		b.dispatchOrderFallback(cmd.Order)
		return nil
	case battleCommandStop:
		b.dispatchStopFallback()
		return nil
	case battleCommandActivation:
		b.dispatchActivationFallback(cmd.Activation)
		return nil
	case battleCommandMobileBuild:
		if b.mobileBuildFn != nil {
			return b.mobileBuildFn(cmd.MobileBuild.Product, cmd.MobileBuild.WX, cmd.MobileBuild.WZ, cmd.MobileBuild.Queued)
		}
		return b.dispatchMobileBuildFallback(cmd.MobileBuild.Product, cmd.MobileBuild.WX, cmd.MobileBuild.WZ, cmd.MobileBuild.Queued)
	case battleCommandFactoryBuild:
		if b.factoryBuildFn != nil {
			return b.factoryBuildFn(cmd.FactoryBuild.Product, cmd.FactoryBuild.Queued)
		}
		return b.dispatchFactoryBuildFallback(cmd.FactoryBuild.Product, cmd.FactoryBuild.Queued)
	case battleCommandCancelProduction:
		b.dispatchCancelProductionFallback(cmd.CancelProduction.Unit)
		return nil
	case battleCommandStockpile:
		b.dispatchStockpileFallback(cmd.Stockpile)
		return nil
	case battleCommandBuildPage:
		if !b.requireCommandDispatch {
			// Fixture fallback still mutates only through the same unit flag
			// encoding; production always takes the session queue branch.
			if u := b.selectedBuilder(); u != nil && b.cat != nil {
				if pm := b.cat.BuildMenus[u.Def.CanonicalKey]; pm != nil {
					count := hud.PageCountFromButtons(len(pm.Buttons), hud.RetailBuildButtonsPerPage)
					view := hud.SelectUnit{Flags: u.Flags, DefID: b.catalogDefID(u)}
					hud.SetBuildPage(&view, cmd.BuildPage.Page, count, nil)
					u.Flags = view.Flags
				}
			}
		}
		return nil
	case battleCommandGroupAssign:
		if b.requireCommandDispatch {
			return fmt.Errorf("battle: production group assignment requires session dispatch")
		}
		b.dispatchGroupFallback(cmd.Group, true)
		return nil
	case battleCommandGroupRecall:
		if b.requireCommandDispatch {
			return fmt.Errorf("battle: production group recall requires session dispatch")
		}
		b.dispatchGroupFallback(cmd.Group, false)
		return nil
	case battleCommandSelectionReplace:
		if b.sess != nil && b.sess.Units != nil {
			for _, u := range b.sess.Units.Iter() {
				if u != nil && u.Owner == b.sess.LocalOwner {
					u.Flags &^= client.SelectionFlag
				}
			}
			for _, h := range cmd.Selection.Handles {
				if u := b.sess.Units.Unit(h); u != nil && u.Owner == b.sess.LocalOwner {
					u.Flags |= client.SelectionFlag
				}
			}
		}
		return nil
	case battleCommandSelectionToggle:
		if b.sess != nil && b.sess.Units != nil {
			for _, h := range cmd.Selection.Handles {
				if u := b.sess.Units.Unit(h); u != nil && u.Owner == b.sess.LocalOwner {
					u.Flags ^= client.SelectionFlag
				}
			}
		}
		return nil
	case battleCommandSelectionClear:
		if b.sess != nil && b.sess.Units != nil {
			for _, u := range b.sess.Units.Iter() {
				if u != nil && u.Owner == b.sess.LocalOwner {
					u.Flags &^= client.SelectionFlag
				}
			}
		}
		return nil
	default:
		return nil
	}
}

// sessionHumanCommand converts the UI value into the session-owned command
// value. Slices are copied by Session.EnqueueHumanCommand; no live pointers
// cross the presentation boundary.
func (b *battleSession) sessionHumanCommand(c battleCommand) (session.HumanCommand, bool) {
	if b == nil {
		return session.HumanCommand{}, false
	}
	switch c.Kind {
	case battleCommandSelectionReplace:
		return session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: c.Selection.Handles}}, true
	case battleCommandSelectionToggle:
		return session.HumanCommand{Kind: session.HumanSelectionToggle, Selection: session.HumanSelectionCommand{Handles: c.Selection.Handles}}, true
	case battleCommandSelectionClear:
		return session.HumanCommand{Kind: session.HumanSelectionClear}, true
	case battleCommandOrder:
		return session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: c.Order.Handles, Code: latchCode(c.Order.Latch), Target: c.Order.Target, Position: c.Order.Position, Queued: c.Order.Queued}}, true
	case battleCommandStop:
		var handles []pool.Handle
		if !b.requireCommandDispatch {
			handles = b.selectedHandles()
		}
		return session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: handles}}, true
	case battleCommandActivation:
		return session.HumanCommand{Kind: session.HumanActivation, Activation: session.HumanActivationCommand{Unit: c.Activation.Unit, Activate: c.Activation.Activate, Queued: c.Activation.Queued}}, true
	case battleCommandMobileBuild:
		return session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{Builder: c.MobileBuild.Builder, Product: c.MobileBuild.Product, WX: c.MobileBuild.WX, WY: c.MobileBuild.WY, WZ: c.MobileBuild.WZ, Queued: c.MobileBuild.Queued}}, true
	case battleCommandFactoryBuild:
		return session.HumanCommand{Kind: session.HumanFactoryBuild, FactoryBuild: session.HumanFactoryBuildCommand{Builder: c.FactoryBuild.Builder, Product: c.FactoryBuild.Product, Queued: c.FactoryBuild.Queued}}, true
	case battleCommandCancelProduction:
		return session.HumanCommand{Kind: session.HumanCancelProduction, CancelProduction: session.HumanCancelProductionCommand{Unit: c.CancelProduction.Unit}}, true
	case battleCommandStockpile:
		return session.HumanCommand{Kind: session.HumanStockpile, Stockpile: session.HumanStockpileCommand{Unit: c.Stockpile.Unit, Queued: c.Stockpile.Queued}}, true
	case battleCommandBuildPage:
		return session.HumanCommand{Kind: session.HumanBuildPage, BuildPage: session.HumanBuildPageCommand{Builder: c.BuildPage.Builder, Page: c.BuildPage.Page}}, true
	case battleCommandGroupAssign:
		return session.HumanCommand{Kind: session.HumanGroupAssign, Group: session.HumanGroupCommand{Group: c.Group.Group, Preserve: c.Group.Preserve, Mask: c.Group.Mask}}, true
	case battleCommandGroupRecall:
		return session.HumanCommand{Kind: session.HumanGroupRecall, Group: session.HumanGroupCommand{Group: c.Group.Group, Preserve: c.Group.Preserve, Mask: c.Group.Mask}}, true
	}
	return session.HumanCommand{}, false
}

func (b *battleSession) dispatchCancelProductionFallback(handle pool.Handle) {
	if b == nil || b.sess == nil || b.sess.Units == nil {
		return
	}
	u := b.sess.Units.Unit(handle)
	if u == nil || u.Def == nil {
		return
	}
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	prim := q.Primary()
	if len(prim) == 0 {
		return
	}
	tail := prim[len(prim)-1]
	if tail == nil || tail.BuildDefKey == "" {
		return
	}
	if orders.IsMobileBuild(tail.ID) {
		_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
	} else if orders.IsFactoryBuild(tail.ID) {
		_ = construction.CancelTailMost(u, tail.BuildDefKey)
	} else {
		if err := construction.CancelTailMost(u, tail.BuildDefKey); err != nil {
			_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
		}
	}
}

func (b *battleSession) dispatchStockpileFallback(cmd battleStockpileCommand) {
	if b == nil || b.sess == nil || b.sess.Units == nil {
		return
	}
	u := b.sess.Units.Unit(cmd.Unit)
	if u == nil || u.Def == nil {
		return
	}
	hasStockpile := false
	for i := 0; i < units.NumSlots; i++ {
		if slot := u.SlotAt(i); slot != nil && slot.Weapon != nil && slot.Weapon.Stockpile {
			hasStockpile = true
			break
		}
	}
	if !hasStockpile {
		if u.Def.Weapon1Def == nil || !u.Def.Weapon1Def.Stockpile {
			if u.Def.Weapon2Def == nil || !u.Def.Weapon2Def.Stockpile {
				if u.Def.Weapon3Def == nil || !u.Def.Weapon3Def.Stockpile {
					return
				}
			}
		}
	}
	buildWeaponID := orders.Lookup("BuildWeapon")
	if buildWeaponID == 0 {
		return
	}
	slotIdx := -1
	for i := 0; i < units.NumSlots; i++ {
		if slot := u.SlotAt(i); slot != nil && slot.Weapon != nil && slot.Weapon.Stockpile {
			slotIdx = i
			break
		}
	}
	if slotIdx < 0 {
		return
	}
	tick := uint32(0)
	if b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	node := orders.NewNodeForOrder(buildWeaponID, 0, 0, 0, 0, tick, u.Handle, cmd.Queued)
	node.Param1, node.Param2 = uint32(slotIdx), 1
	node.Param3 = 0
	if q := orders.QueueForUnit(u); q != nil {
		q.CoalesceTail(buildWeaponID, node)
	}
}

func (b *battleSession) dispatchGroupFallback(cmd battleGroupCommand, assign bool) {
	if b == nil || b.sess == nil || b.sess.Units == nil || b.cat == nil {
		return
	}
	views := make([]*hud.SelectUnit, 0)
	unitsByView := make([]*units.Unit, 0)
	for _, u := range b.sess.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != b.sess.LocalOwner || u.Def == nil {
			continue
		}
		id, ok := b.cat.UnitDefIndex(u.Def.CanonicalKey)
		if !ok || id == 0 || id > 0xffff {
			continue
		}
		views = append(views, &hud.SelectUnit{Flags: u.Flags, Group: u.Group, DefID: uint16(id)})
		unitsByView = append(unitsByView, u)
	}
	if assign {
		hud.AssignGroup(views, cmd.Group, nil)
	} else {
		mask := cmd.Mask
		if mask == [32]byte{} {
			for i := range mask {
				mask[i] = 0xff
			}
		}
		hud.RecallGroup(views, cmd.Group, cmd.Preserve, mask, nil)
	}
	for i, view := range views {
		unitsByView[i].Flags = view.Flags
		unitsByView[i].Group = view.Group
	}
}

func (b *battleSession) dispatchStopFallback() {
	if b == nil || b.sess == nil || b.sess.Units == nil {
		return
	}
	tick := uint32(0)
	if b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	stop := orders.Lookup("Stop")
	if stop == 0 {
		return
	}
	for _, u := range b.selectedUnits() {
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		q.Push(stop, orders.NewNodeForOrder(stop, 0, 0, 0, 0, tick, u.Handle, false))
	}
}

func (b *battleSession) dispatchActivationFallback(cmd battleActivationCommand) {
	if b == nil || b.sess == nil || b.sess.Units == nil {
		return
	}
	u := b.sess.Units.Unit(cmd.Unit)
	if u == nil || u.Def == nil || !u.Def.OnOffable {
		return
	}
	name := "Deactivate"
	if cmd.Activate {
		name = "Activate"
	}
	id := orders.Lookup(name)
	if id == 0 {
		return
	}
	tick := uint32(0)
	if b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	q := orders.QueueForUnit(u)
	if q == nil {
		return
	}
	if !cmd.Queued {
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
	}
	q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, cmd.Queued))
}

func (b *battleSession) dispatchOrderFallback(cmd battleOrderCommand) {
	if b == nil || b.sess == nil || b.sess.Units == nil {
		return
	}
	code := latchCode(cmd.Latch)
	if code == 0 {
		return
	}
	tick := uint32(0)
	if b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	var target *units.Unit
	if cmd.Target != 0 {
		// Resolve the smart-reference at application time. The typed command
		// carries only its stable handle; target position/validity therefore
		// cannot go stale while the input frame is waiting for application.
		target = b.sess.Units.Unit(cmd.Target)
	}
	for _, u := range b.selectedUnits() {
		id := orders.Resolve(code, u, target, &cmd.Position)
		if id == 0 {
			continue
		}
		gx, gy, gz := cmd.Position.X, cmd.Position.Y, cmd.Position.Z
		if cmd.Target != 0 && target != nil {
			gx, gy, gz = target.X, target.Y, target.Z
		}
		node := orders.NewNodeForOrder(id, cmd.Target, gx, gy, gz, tick, u.Handle, cmd.Queued)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		if !cmd.Queued {
			q.PurgeUnprotected()
			q.DropLeadingAutoOps()
		}
		q.Push(id, node)
	}
}

func latchCode(latch input.Latch) int {
	switch latch {
	case input.LatchNormal:
		return 1
	case input.LatchMove:
		return 2
	case input.LatchAttack:
		return 3
	case input.LatchBlast:
		return 4
	case input.LatchUnload:
		return 5
	case input.LatchPickup:
		return 6
	case input.LatchFollow:
		return 7
	case input.LatchRepair:
		return 8
	case input.LatchPatrol:
		return 9
	case input.LatchTeleport:
		return 11
	case input.LatchReclaim:
		return 12
	case input.LatchCapture:
		return 13
	case input.LatchMobileBuild:
		return 14
	default:
		return 0
	}
}
func (b *battleSession) CancelPlacement() {
	b.disarmPlacement()
}
func (b *battleSession) IsPlacementArmed() bool   { return b.buildDef != "" }
func (b *battleSession) PlacementProduct() string { return b.buildDef }
