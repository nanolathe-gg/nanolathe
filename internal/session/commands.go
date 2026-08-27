package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// HumanCommandKind identifies a typed local-player command. Payloads contain
// handles and values only; they never retain pointers into simulation pools.
type HumanCommandKind uint8

const (
	HumanSelectionReplace HumanCommandKind = iota + 1
	HumanSelectionToggle
	HumanSelectionClear
	HumanOrder
	HumanStop
	HumanActivation
	HumanMobileBuild
	HumanFactoryBuild
	HumanCancelProduction
	HumanStockpile
	HumanBuildPage
	HumanGroupAssign
	HumanGroupRecall
)

type HumanSelectionCommand struct{ Handles []pool.Handle }
type HumanOrderCommand struct {
	Handles  []pool.Handle
	Code     int
	Target   pool.Handle
	Position orders.ResolvePos
	Queued   bool
}
type HumanStopCommand struct{ Handles []pool.Handle }
type HumanActivationCommand struct {
	Unit             pool.Handle
	Activate, Queued bool
}
type HumanMobileBuildCommand struct {
	Builder pool.Handle
	Product string
	WX, WZ  numeric.Fixed
	WY      numeric.Fixed // validated site height in world fixed units [07 §9]
	Queued  bool
}
type HumanFactoryBuildCommand struct {
	Builder pool.Handle
	Product string
	Count   int
	// Queued remains for old callers that only supplied the pre-count command.
	Queued bool
}
type HumanCancelProductionCommand struct{ Unit pool.Handle }
type HumanStockpileCommand struct {
	Unit   pool.Handle
	Queued bool
}

// HumanBuildPageCommand selects one authored build page for a selected builder.
// Page is an absolute zero-based page; presentation resolves digit/next/prev
// into this value from the immutable frame, while the authoritative boundary
// validates the builder and clamps against the compiled CANBUILD page count
// [07 §9].
type HumanBuildPageCommand struct {
	Builder pool.Handle
	Page    int
}

// HumanGroupCommand carries the established Ctrl+digit assignment or digit
// recall operation. Preserve is the Shift-held toggle/preserve argument on
// recall [07 §9]. Mask is the authored CTRL_F filter when that state is
// available; an all-zero value means that the presentation boundary has not
// published a CTRL_F mask yet (the no-filter path).
type HumanGroupCommand struct {
	Group    int
	Preserve bool
	Mask     [32]byte
}

// HumanCommand is an immutable-at-boundary command value. EnqueueHumanCommand
// copies handle slices and strings so callers may reuse their input buffers.
type HumanCommand struct {
	// Sequence and DueTick are session-owned metadata. Callers leave both zero;
	// EnqueueHumanCommand assigns them when the value enters the session queue.
	Sequence         uint64
	DueTick          uint32
	Kind             HumanCommandKind
	Selection        HumanSelectionCommand
	Order            HumanOrderCommand
	Stop             HumanStopCommand
	Activation       HumanActivationCommand
	MobileBuild      HumanMobileBuildCommand
	FactoryBuild     HumanFactoryBuildCommand
	CancelProduction HumanCancelProductionCommand
	Stockpile        HumanStockpileCommand
	BuildPage        HumanBuildPageCommand
	Group            HumanGroupCommand
}

func cloneHumanHandles(in []pool.Handle) []pool.Handle {
	if len(in) == 0 {
		return nil
	}
	out := make([]pool.Handle, len(in))
	copy(out, in)
	return out
}

func cloneHumanCommand(c HumanCommand) HumanCommand {
	c.Selection.Handles = cloneHumanHandles(c.Selection.Handles)
	c.Order.Handles = cloneHumanHandles(c.Order.Handles)
	c.Stop.Handles = cloneHumanHandles(c.Stop.Handles)
	return c
}

// EnqueueHumanCommand appends one command for the next authoritative input
// phase. It performs no simulation mutation.
func (s *Session) EnqueueHumanCommand(c HumanCommand) error {
	if s == nil {
		return fmt.Errorf("session: nil human-command owner")
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	// The retail input pass consumes the command at the next authoritative
	// boundary. No researched latency/network offset exists for local commands,
	// so the smallest truthful contract is the next session tick.
	// TODO(question): verify whether a networked input frame can target a later
	// frame; settle this from the future-frame receive probe before adding an
	// offset here.
	s.nextHumanSequence++
	if s.nextHumanSequence == 0 {
		// Sequence wrap is outside the established single-player lifetime. Keep
		// zero reserved as "not assigned" while preserving deterministic order.
		s.nextHumanSequence++
	}
	c.Sequence = s.nextHumanSequence
	if s.Clock == nil {
		c.DueTick = 1
	} else {
		c.DueTick = s.Clock.GlobalTick + 1
	}
	s.pendingHuman = append(s.pendingHuman, cloneHumanCommand(c))
	return nil
}

// PendingHumanCommands returns immutable command copies for diagnostics/tests.
func (s *Session) PendingHumanCommands() []HumanCommand {
	if s == nil {
		return nil
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	out := make([]HumanCommand, len(s.pendingHuman))
	for i := range s.pendingHuman {
		out[i] = cloneHumanCommand(s.pendingHuman[i])
	}
	return out
}

// applyHumanCommands is the sole production consumer of local input. Commands
// are applied in enqueue order, with canonical orders/construction APIs doing
// all descriptor and lifecycle decisions.
func (s *Session) applyHumanCommands(tick uint32) {
	if s == nil {
		return
	}
	s.humanMu.Lock()
	if len(s.pendingHuman) == 0 {
		s.humanMu.Unlock()
		return
	}
	// Keep future commands queued. Enqueue assigns monotonically increasing
	// sequence values, therefore the stable queue order is the authoritative
	// same-tick order and needs no map or sort [I1].
	cmds := make([]HumanCommand, 0, len(s.pendingHuman))
	future := make([]HumanCommand, 0, len(s.pendingHuman))
	for i := range s.pendingHuman {
		c := s.pendingHuman[i]
		if c.DueTick <= tick {
			cmds = append(cmds, c)
		} else {
			future = append(future, c)
		}
	}
	s.pendingHuman = future
	s.humanMu.Unlock()
	for _, c := range cmds {
		s.applyHumanCommand(c, tick)
	}
}

func (s *Session) humanUnit(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	u := s.Units.Unit(h)
	if u == nil || !u.Alive || u.Owner != s.LocalOwner {
		return nil
	}
	return u
}

func (s *Session) selectedHumanHandles() []pool.Handle {
	if s == nil || s.Units == nil {
		return nil
	}
	out := make([]pool.Handle, 0)
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive && u.Owner == s.LocalOwner && u.Flags&0x10 != 0 {
			out = append(out, u.Handle)
		}
	}
	return out
}

func (s *Session) selectedHumanBuilder(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	var selected *units.Unit
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != s.LocalOwner || u.Flags&0x10 == 0 {
			continue
		}
		// The retail page state is keyed by the single selected-builder
		// identity. A builder mixed with another selected unit has aggregate
		// command state, not a builder page [07 §9].
		if selected != nil {
			return nil
		}
		selected = u
	}
	if selected == nil || selected.Handle != h || selected.Def == nil || !selected.Def.Builder {
		return nil
	}
	return selected
}

func (s *Session) applyHumanBuildPage(c HumanBuildPageCommand) {
	u := s.selectedHumanBuilder(c.Builder)
	if u == nil || s.Catalog == nil {
		return
	}
	menu := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]
	if menu == nil || len(menu.Buttons) == 0 {
		return
	}
	pageCount := hud.PageCountFromButtons(len(menu.Buttons), hud.RetailBuildButtonsPerPage)
	defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || defID == 0 || defID > 0xffff {
		return
	}
	view := hud.SelectUnit{Flags: u.Flags, DefID: uint16(defID)}
	// SetBuildPage performs the retail identity/page-count guard and clamps
	// to the authored page byte. It mutates only at the input boundary [07 §9].
	hud.SetBuildPage(&view, c.Page, pageCount, nil)
	u.Flags = view.Flags
}

func (s *Session) applyHumanGroup(c HumanGroupCommand, assign bool) {
	if s == nil || s.Units == nil || s.Catalog == nil || c.Group < 1 || c.Group > 9 {
		return
	}
	views := make([]*hud.SelectUnit, 0)
	unitsByView := make([]*units.Unit, 0)
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != s.LocalOwner || u.Def == nil {
			continue
		}
		defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
		if !ok || defID == 0 || defID > 0xffff {
			continue
		}
		views = append(views, &hud.SelectUnit{Flags: u.Flags, Group: u.Group, DefID: uint16(defID)})
		unitsByView = append(unitsByView, u)
	}
	if assign {
		hud.AssignGroup(views, c.Group, nil)
	} else {
		mask := c.Mask
		if mask == [32]byte{} {
			// No CTRL_F mask producer is part of the current immutable frame.
			// Treat absent filter state as no filter; the authored mask producer
			// remains an explicit TODO rather than a guessed category mask [07 §9].
			for i := range mask {
				mask[i] = 0xff
			}
		}
		hud.RecallGroup(views, c.Group, c.Preserve, mask, nil)
	}
	for i, view := range views {
		unitsByView[i].Flags = view.Flags
		unitsByView[i].Group = view.Group
	}
}

func (s *Session) normalizeSelectedBuilderPages() {
	if s == nil || s.Units == nil || s.Catalog == nil {
		return
	}
	var u *units.Unit
	for _, candidate := range s.Units.Iter() {
		if candidate == nil || !candidate.Alive || candidate.Owner != s.LocalOwner || candidate.Flags&0x10 == 0 {
			continue
		}
		if u != nil {
			return // mixed/multiple selection has no single page owner [07 §9]
		}
		u = candidate
	}
	if u == nil || u.Def == nil || !u.Def.Builder {
		return
	}
	menu := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]
	if menu == nil || len(menu.Buttons) == 0 {
		return
	}
	defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || defID == 0 || defID > 0xffff {
		return
	}
	view := hud.SelectUnit{Flags: u.Flags, DefID: uint16(defID)}
	page := 0
	if hud.IsPaged(view.Flags) {
		page = hud.DecodePage(view.Flags)
	}
	hud.SetBuildPage(&view, page, hud.PageCountFromButtons(len(menu.Buttons), hud.RetailBuildButtonsPerPage), nil)
	u.Flags = view.Flags
}

func stampHumanBuild(u *units.Unit, product string, tick uint32, queued bool, goalY numeric.Fixed) {
	if u == nil {
		return
	}
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	prim := q.Primary()
	tail := prim[len(prim)-1]
	if tail == nil || tail.BuildDefKey != content.CanonicalKey(product) {
		return
	}
	tail.Owner = u.Handle
	tail.CreationTick = tick
	tail.GoalY = goalY
	if queued {
		tail.Flags |= orders.FlagPurgeSurvivor
	} else {
		tail.Flags &^= orders.FlagPurgeSurvivor
	}
}

func (s *Session) applyHumanCommand(c HumanCommand, tick uint32) {
	if s == nil || s.Units == nil {
		return
	}
	switch c.Kind {
	case HumanSelectionReplace:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive && u.Owner == s.LocalOwner {
				u.Flags &^= 0x10
			}
		}
		for _, h := range c.Selection.Handles {
			if u := s.humanUnit(h); u != nil {
				u.Flags |= 0x10
			}
		}
		s.normalizeSelectedBuilderPages()
	case HumanSelectionToggle:
		for _, h := range c.Selection.Handles {
			if u := s.humanUnit(h); u != nil {
				u.Flags ^= 0x10
			}
		}
		s.normalizeSelectedBuilderPages()
	case HumanSelectionClear:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive && u.Owner == s.LocalOwner {
				u.Flags &^= 0x10
			}
		}
	case HumanStop:
		id := orders.Lookup("Stop")
		if id == 0 {
			return
		}
		handles := c.Stop.Handles
		if len(handles) == 0 {
			handles = s.selectedHumanHandles()
		}
		for _, h := range handles {
			if u := s.humanUnit(h); u != nil {
				if q := orders.QueueForUnit(u); q != nil {
					q.PurgeUnprotected()
					q.DropLeadingAutoOps()
					q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false))
				}
			}
		}
	case HumanActivation:
		u := s.humanUnit(c.Activation.Unit)
		if u == nil || u.Def == nil || !u.Def.OnOffable {
			return
		}
		name := "Deactivate"
		if c.Activation.Activate {
			name = "Activate"
		}
		id := orders.Lookup(name)
		if id == 0 {
			return
		}
		if q := orders.QueueForUnit(u); q != nil {
			if !c.Activation.Queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, c.Activation.Queued))
		}
	case HumanMobileBuild:
		u := s.humanUnit(c.MobileBuild.Builder)
		if u == nil || s.Catalog == nil {
			return
		}
		if !c.MobileBuild.Queued {
			if q := orders.QueueForUnit(u); q != nil {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
		}
		if err := construction.QueueMobileBuild(u, c.MobileBuild.Product, c.MobileBuild.WX, c.MobileBuild.WZ, 1, s.Catalog); err == nil {
			stampHumanBuild(u, c.MobileBuild.Product, tick, c.MobileBuild.Queued, c.MobileBuild.WY)
		}
	case HumanFactoryBuild:
		u := s.humanUnit(c.FactoryBuild.Builder)
		if u == nil || s.Catalog == nil {
			return
		}
		count := c.FactoryBuild.Count
		if count == 0 {
			count = 1
		}
		var err error
		if count > 0 {
			err = construction.QueueFactoryBuild(u, c.FactoryBuild.Product, count, s.Catalog)
		} else {
			err = construction.CancelProductCount(u, c.FactoryBuild.Product, -count)
		}
		if err == nil && count > 0 {
			// Counted factory nodes are no-purge commands. The old boolean is
			// retained only for source compatibility with pre-count callers.
			stampHumanBuild(u, c.FactoryBuild.Product, tick, false, 0)
		}
	case HumanCancelProduction:
		u := s.humanUnit(c.CancelProduction.Unit)
		if u == nil {
			return
		}
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			return
		}
		prim := q.Primary()
		tail := prim[len(prim)-1]
		if tail == nil || tail.BuildDefKey == "" {
			return
		}
		if orders.IsMobileBuild(tail.ID) {
			_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
		} else {
			_ = construction.CancelTailMost(u, tail.BuildDefKey)
		}
	case HumanStockpile:
		u := s.humanUnit(c.Stockpile.Unit)
		if u == nil {
			return
		}
		id := orders.Lookup("BuildWeapon")
		if id == 0 {
			return
		}
		slot := -1
		for i := 0; i < units.NumSlots; i++ {
			if sl := u.SlotAt(i); sl != nil && sl.Weapon != nil && sl.Weapon.Stockpile {
				slot = i
				break
			}
		}
		if slot < 0 {
			return
		}
		n := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, c.Stockpile.Queued)
		n.Param1, n.Param2 = uint32(slot), 1
		if q := orders.QueueForUnit(u); q != nil {
			q.CoalesceTail(id, n)
		}
	case HumanBuildPage:
		s.applyHumanBuildPage(c.BuildPage)
	case HumanGroupAssign:
		s.applyHumanGroup(c.Group, true)
	case HumanGroupRecall:
		s.applyHumanGroup(c.Group, false)
	case HumanOrder:
		var target *units.Unit
		if c.Order.Target != 0 {
			target = s.humanTarget(c.Order.Target)
		}
		handles := c.Order.Handles
		if len(handles) == 0 {
			handles = s.selectedHumanHandles()
		}
		for _, h := range handles {
			u := s.humanUnit(h)
			if u == nil {
				continue
			}
			id := orders.Resolve(c.Order.Code, u, target, &c.Order.Position)
			if id == 0 {
				continue
			}
			gx, gy, gz := c.Order.Position.X, c.Order.Position.Y, c.Order.Position.Z
			if target != nil {
				gx, gy, gz = target.X, target.Y, target.Z
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			if !c.Order.Queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, c.Order.Target, gx, gy, gz, tick, u.Handle, c.Order.Queued))
		}
	}
}

func (s *Session) humanTarget(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	u := s.Units.Unit(h)
	if u == nil || !u.Alive {
		return nil
	}
	return u
}
