package main

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
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
	Count   int
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
// The composition layer binds these typed command records to session-owned
// pumps. The battleSession methods below perform validation and submit one
// command through commandDispatchFn.
type battleDispatch interface {
	DispatchMobileBuild(product string, wx, wz numeric.Fixed, queued bool) error
	DispatchFactoryBuildDelta(product string, count int) error
	DispatchFactoryBuild(product string, queued bool) error
	DispatchOrderCommand(cmd battleOrderCommand) error
	DispatchActivation(cmd battleActivationCommand) error
	DispatchCancelProduction(unit pool.Handle) error
	DispatchStockpile(unit pool.Handle, queued bool) error
	DispatchGroupAssign(group int) error
	DispatchGroupRecall(group int, preserve bool) error
}

var _ battleDispatch = (*battleSession)(nil)

// These methods validate typed input and submit it through the session command
// boundary for the authoritative input phase [01 §4.4][07 §9].
func (b *battleSession) DispatchMobileBuild(product string, wx, wz numeric.Fixed, queued bool) error {
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("battle: production build has no current snapshot")
	}
	var builder pool.Handle
	builder = f.CommandPage.Builder
	if v, found := snapshotUnitByHandle(f, builder); !found || b.sess == nil || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
		builder = 0
	}
	if builder == 0 {
		return fmt.Errorf("battle: production build has no snapshot builder")
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandMobileBuild, MobileBuild: battleMobileBuildCommand{
		Builder: builder, Product: product, WX: wx, WZ: wz, Queued: queued,
	}})
}
func (b *battleSession) DispatchFactoryBuild(product string, queued bool) error {
	return b.DispatchFactoryBuildDelta(product, 1)
}

func (b *battleSession) DispatchFactoryBuildDelta(product string, count int) error {
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("battle: production factory has no current snapshot")
	}
	var builder pool.Handle
	builder = f.CommandPage.Builder
	if v, found := snapshotUnitByHandle(f, builder); !found || b.sess == nil || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
		builder = 0
	}
	if builder == 0 {
		return fmt.Errorf("battle: production factory has no snapshot builder")
	}
	return b.submitBattleCommand(battleCommand{Kind: battleCommandFactoryBuild, FactoryBuild: battleFactoryBuildCommand{
		Builder: builder, Product: product, Count: count,
	}})
}

func (b *battleSession) snapshotBuilder(v snapshot.UnitView) bool {
	if b == nil || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(v.DefName)
	return ok && def != nil && def.Builder
}

// DispatchOrderCommand submits a picked, typed order payload. The command
// contains no screen-space origin, so an order cannot be preceded by an
// accidental contextual action at (0,0) [04 §3.4][07 §9].
func (b *battleSession) DispatchOrderCommand(cmd battleOrderCommand) error {
	if _, ok := b.currentSnapshot(); !ok {
		return fmt.Errorf("battle: production order has no current snapshot")
	}
	// Empty handles mean resolve the authoritative selection at the input
	// boundary, after any earlier queued selection command.
	cmd.Handles = nil
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
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("battle: build page has no current snapshot")
	}
	var builder pool.Handle
	builder = f.CommandPage.Builder
	if builder == 0 || page < 0 || page >= int(f.CommandPage.PageCount) {
		return fmt.Errorf("battle: invalid build page %d", page)
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
	if b == nil || b.commandDispatchFn == nil {
		return fmt.Errorf("battle: production command dispatch is unbound")
	}
	return b.commandDispatchFn(cmd)
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
		return session.HumanCommand{Kind: session.HumanStop}, true
	case battleCommandActivation:
		return session.HumanCommand{Kind: session.HumanActivation, Activation: session.HumanActivationCommand{Unit: c.Activation.Unit, Activate: c.Activation.Activate, Queued: c.Activation.Queued}}, true
	case battleCommandMobileBuild:
		return session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{Builder: c.MobileBuild.Builder, Product: c.MobileBuild.Product, WX: c.MobileBuild.WX, WY: c.MobileBuild.WY, WZ: c.MobileBuild.WZ, Queued: c.MobileBuild.Queued}}, true
	case battleCommandFactoryBuild:
		count := c.FactoryBuild.Count
		if count == 0 {
			count = 1
		}
		return session.HumanCommand{Kind: session.HumanFactoryBuild, FactoryBuild: session.HumanFactoryBuildCommand{Builder: c.FactoryBuild.Builder, Product: c.FactoryBuild.Product, Count: count}}, true
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
