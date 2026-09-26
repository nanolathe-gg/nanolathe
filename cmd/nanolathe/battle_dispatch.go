package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func snapshotUnitByHandle(f *frame.Frame, h pool.Handle) (frame.UnitView, bool) {
	if f == nil {
		return frame.UnitView{}, false
	}
	for i := range f.Units {
		if f.Units[i].Slot == h {
			return f.Units[i], true
		}
	}
	return frame.UnitView{}, false
}

func (b *battleSession) snapshotBuilder(v frame.UnitView) bool {
	if b == nil || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(v.DefName)
	return ok && def != nil && def.Builder
}

// StockpileGadget reports whether an authored side-panel gadget is the
// MAKENUKE/MAKEANTI stockpile toy, the only producer of a BUILDWEAPON round in
// shipped content [06 §11.1][07 R-CAM-01 §14 item 3]. The count-label writer
// identifies the same toys by `commonattribs` bit 0x08 [07 R-P0-11 §2], which
// stock content authors on exactly the eight stockpile buttons and on nothing
// else, so that bit is the test; the name suffix is the fallback for a page
// whose byte is unset.
func StockpileGadget(gad gui.Gadget) bool {
	if gad.Kind != gui.KindButton {
		return false
	}
	if gad.CommonAttribs&guiAttribStockpileToy != 0 {
		return true
	}
	upper := strings.ToUpper(gad.Name)
	return strings.HasSuffix(upper, "MAKENUKE") || strings.HasSuffix(upper, "MAKEANTI")
}

// guiAttribStockpileToy is the `commonattribs` bit the count-label writer tests
// after 0x04 to format a stockpile button's caption [07 R-P0-11 §2].
const guiAttribStockpileToy = 0x08

// DispatchStockpileGadget is the click body of a MAKENUKE/MAKEANTI toy: it
// adds or subtracts count BUILDWEAPON rounds on the unit the committed command
// page names. A stockpile toy reaches the same counted producer every other
// build-page toy does — MAKENUKE/MAKEANTI only route it to the BUILDWEAPON
// descriptor instead of a build order — so the count is signed (+1/+5/-1/-5)
// and the producer never purges [07 R-P0-11 §1]. The alias supplies slot 0 and
// the session's enqueue guard refuses a slot that holds no `stockpile` weapon
// [06 §11.1][06 R-WPN-05 §2].
//
// The order alias is the button's whole behavior — there is no placement, no
// latch and no hotkey — so the caller needs only to recognise the gadget and
// call this [07 R-CAM-01 §14 item 3].
func (b *battleSession) DispatchStockpileGadget(count int) error {
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("nanolathe: stockpile round not dispatched: no committed frame")
	}
	unit := f.CommandPage.Builder
	if v, found := snapshotUnitByHandle(f, unit); !found || b.sess == nil || v.Owner != b.sess.LocalOwner {
		unit = 0
	}
	if unit == 0 {
		return fmt.Errorf("nanolathe: stockpile round not dispatched: the committed command page names no unit the local player owns")
	}
	return b.DispatchStockpile(unit, count)
}

// enqueueHumanCommand is the only battle-to-session mutation path. The UI
// constructs the session-owned value directly; EnqueueHumanCommand assigns
// sequence and due-tick metadata and performs no simulation mutation
// [01 §4.4][07 §9].
func (b *battleSession) enqueueHumanCommand(c session.HumanCommand) error {
	if b == nil || b.sess == nil {
		return fmt.Errorf("nanolathe: battle command not enqueued: no session")
	}
	return b.sess.EnqueueHumanCommand(c)
}

func (b *battleSession) DispatchMobileBuild(product string, wx, wy, wz numeric.Fixed, queued bool) error {
	return b.dispatchMobileBuild(product, wx, wy, wz, queued, false)
}

func (b *battleSession) DispatchMobileBuildFacing(product string, wx, wy, wz numeric.Fixed, queued bool, facing units.StructureFacing) error {
	return b.dispatchMobileBuildFacing(product, wx, wy, wz, queued, false, facing)
}

func (b *battleSession) dispatchMobileBuild(product string, wx, wy, wz numeric.Fixed, queued, appendOnly bool) error {
	return b.dispatchMobileBuildFacing(product, wx, wy, wz, queued, appendOnly, units.FacingSouth)
}

func (b *battleSession) dispatchMobileBuildFacing(product string, wx, wy, wz numeric.Fixed, queued, appendOnly bool, facing units.StructureFacing) error {
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("nanolathe: mobile build not dispatched: no committed frame")
	}
	builder := f.CommandPage.Builder
	if v, found := snapshotUnitByHandle(f, builder); !found || b.sess == nil || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
		builder = 0
	}
	if builder == 0 {
		return fmt.Errorf("nanolathe: mobile build not dispatched: the committed command page names no builder the local player owns")
	}
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{
		Facing: facing, Builder: builder, Product: product, WX: wx, WY: wy, WZ: wz, Queued: queued, AppendOnly: appendOnly,
	}})
}

func (b *battleSession) DispatchFactoryBuild(product string, queued bool) error {
	return b.DispatchFactoryBuildDelta(product, 1)
}

func (b *battleSession) DispatchFactoryBuildDelta(product string, count int) error {
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("nanolathe: factory build not dispatched: no committed frame")
	}
	builder := f.CommandPage.Builder
	// Counted products come from the selected unit's named GUI gadget;
	// the actor's Builder flag is not a gate [07 R-P0-11 §1]. This also
	// admits a building's authored mobile upgrade without changing its FBI.
	if v, found := snapshotUnitByHandle(f, builder); !found || b.sess == nil || v.Owner != b.sess.LocalOwner {
		builder = 0
	}
	if builder == 0 {
		return fmt.Errorf("nanolathe: factory build not dispatched: the committed command page names no unit the local player owns")
	}
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanFactoryBuild, FactoryBuild: session.HumanFactoryBuildCommand{
		Builder: builder, Product: product, Count: count,
	}})
}

// DispatchOrderCommand submits a picked, typed order payload. The command
// contains no screen-space origin, so an order cannot be preceded by an
// accidental contextual action at (0,0) [04 §3.4][07 §9].
func (b *battleSession) DispatchOrderCommand(cmd session.HumanOrderCommand) error {
	if _, ok := b.currentSnapshot(); !ok {
		return fmt.Errorf("nanolathe: order not dispatched: no committed frame")
	}
	if b.interfaceTypeRightClick() {
		cmd.Position.InterfaceType = orders.InterfaceTypeRightClick
	} else {
		cmd.Position.InterfaceType = orders.InterfaceTypeLeftClick
	}
	for i := range cmd.Targets {
		cmd.Targets[i].Position.InterfaceType = cmd.Position.InterfaceType
	}
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: cmd})
}

func (b *battleSession) dispatchStopCommand() error {
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanStop})
}

func (b *battleSession) DispatchActivation(cmd session.HumanActivationCommand) error {
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanActivation, Activation: cmd})
}

func (b *battleSession) DispatchCancelProduction(unit pool.Handle) error {
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanCancelProduction, CancelProduction: session.HumanCancelProductionCommand{Unit: unit}})
}

func (b *battleSession) DispatchStockpile(unit pool.Handle, count int) error {
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanStockpile, Stockpile: session.HumanStockpileCommand{Unit: unit, Count: count}})
}

// DispatchBuildPage submits an absolute authored page selected by the
// presentation controls. Production resolves the builder from the immutable
// CommandPage and the session validates it again at the input boundary [07 §9].
func (b *battleSession) DispatchBuildPage(page int) error {
	f, ok := b.currentSnapshot()
	if !ok {
		return fmt.Errorf("nanolathe: build page not dispatched: no committed frame")
	}
	builder := f.CommandPage.Builder
	if builder == 0 || page < 0 || page >= int(f.CommandPage.PageCount) {
		return fmt.Errorf("nanolathe: build page not dispatched: page %d is outside the committed page count", page)
	}
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanBuildPage, BuildPage: session.HumanBuildPageCommand{Builder: builder, Page: page}})
}

func (b *battleSession) DispatchGroupAssign(group int) error {
	if group < 1 || group > 9 {
		return fmt.Errorf("nanolathe: control group assign not dispatched: group %d is outside 1..9", group)
	}
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanGroupAssign, Group: session.HumanGroupCommand{Group: group}})
}

func (b *battleSession) DispatchGroupRecall(group int, preserve bool) error {
	if group < 1 || group > 9 {
		return fmt.Errorf("nanolathe: control group recall not dispatched: group %d is outside 1..9", group)
	}
	return b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanGroupRecall, Group: session.HumanGroupCommand{Group: group, Preserve: preserve}})
}

func (b *battleSession) CancelPlacement() {
	if b == nil {
		return
	}
	b.disarmPlacement()
}

func (b *battleSession) IsPlacementArmed() bool {
	return b != nil && b.battleState().Input.BuildDef != ""
}

func (b *battleSession) PlacementProduct() string {
	if b == nil {
		return ""
	}
	return b.battleState().Input.BuildDef
}
