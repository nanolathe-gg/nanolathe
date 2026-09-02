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
	HumanStance
	// HumanSelfDestruct is the Ctrl+D row of [07 R-CAM-01 §2]. It needs its own
	// kind because that row's action is "resolve the SELFDESTRUCT order
	// descriptor" by name, which HumanOrder cannot express: its 1..14 codes are
	// the latch bytes of [07 §9] and none of them is self-destruct.
	HumanSelfDestruct
)

type HumanSelectionCommand struct{ Handles []pool.Handle }

// HumanSelfDestructCommand carries the selection Ctrl+D acts on. The descriptor
// is the front-segment `SelfDestructFG`, which [04 R-ORD-01 §2] names as the
// button's own; the rear-segment `SelfDestruct` is the kamikaze/mine spawn.
type HumanSelfDestructCommand struct{ Handles []pool.Handle }
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

// HumanStanceCommand is one press of the side panel's MOVEORD or FIREORD
// gadget [04 R-STANCE-01 §2]. Presentation computes the next value from the
// published three-bit panel field with that section's cycle (0→1, 1→2, 2→0,
// 3→0; 4 matches no arm and presses nothing) and transmits it here; this
// boundary owns the broadcast and the definition gate.
type HumanStanceCommand struct {
	// Fire selects `Standing_FireOrder`; otherwise `Standing_MoveOrder`.
	Fire bool
	// Value is the new stance, 0..2 as the panel sends it.
	Value int32
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
	Stance           HumanStanceCommand
	SelfDestruct     HumanSelfDestructCommand
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
	c.SelfDestruct.Handles = cloneHumanHandles(c.SelfDestruct.Handles)
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
	pageCount := hud.BuilderPageCount(u.Def)
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
	switch {
	case hud.IsPaged(view.Flags):
		// The unit is on a build page: keep it there.
		page = hud.DecodePage(view.Flags)
	case hud.RememberedPage(view.Flags) == 0:
		// The unit has never had a page selected — neither the page-shown bit
		// nor the remembered page field of [07 §9] has ever been written — so
		// this is its first selection. A builder opens on its first build page
		// rather than on the orders state.
		//
		// Established by manual retail observation (2026-08-30 playtest report,
		// recorded under [07 R-HUD-03 §6]): selecting a builder in retail shows
		// its build menu, not its order palette. The page state itself is
		// per-unit and persistent, so this fires once: selecting ORDERS clears
		// the page-shown bit but leaves the page field alone, which is exactly
		// what keeps that choice from being undone by the next selection.
		page = 1
	}
	// SetBuildPage clamps against the page-count byte, so a builder whose
	// definition authors no page window stays on the orders state [07 §9].
	hud.SetBuildPage(&view, page, hud.BuilderPageCount(u.Def), nil)
	u.Flags = view.Flags
}

// TODO(question): which retail writer puts a builder on its first build page.
// The behaviour is observed and the page bits are established [07 §9], but no
// traced site sets the page-shown bit at unit creation, so the default is
// applied here, at the selection boundary that already normalizes the page,
// and keyed on "never paged" so it cannot overwrite a remembered choice.
// Decider: a static trace of the writers of unit status bit 22.

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
	// The queue modifier is applied by the caller (purge or not before the
	// insert); it is not stamped onto the record. Purge survivorship is the
	// descriptor's static gate bit 2 and the insertion path already wrote it
	// [04 §3.3][04 R-MOV-03 §6] — rewriting it here is what let a plain move
	// order purge a factory's BuildingBuild node and stop production.
	_ = queued
}

// queuedPointTolerance is the duplicate test's per-axis window: one map cell,
// sixteen world units, in 16.16 [07 R-P0-11 §6]. Retail compares X and Z
// independently and inclusively at the boundary, which makes the accepted
// region a square of side two cells centred on the queued goal — not a radius,
// and not an exact site match.
const queuedPointTolerance = numeric.Fixed(16 << 16)

// mobileBuildKind is the order identity a mobile-build click issues for this
// builder: the VTOL variant for a flyer, the ground variant otherwise. It
// mirrors the choice construction.QueueMobileBuild makes for the same unit, so
// the duplicate test below compares a queued node against the identity that
// created it. A zero here (neither descriptor registered) matches no node, so
// the click falls through to an ordinary enqueue rather than removing the
// wrong one.
func mobileBuildKind(builder *units.Unit) orders.ID {
	if builder != nil && builder.Def != nil && builder.Def.CanFly {
		if id := orders.Lookup(construction.VTOLMobileBuildOrder); id != 0 {
			return id
		}
	}
	return orders.Lookup(construction.MobileBuildOrder)
}

// removeQueuedOrderAtPoint is retail's queued-order duplicate test
// [07 R-P0-11 §6]: walk the unit's primary queue from the front and remove the
// first node whose order kind matches and whose goal lies within one map cell
// of the issued point on X and on Z independently. It reports whether a node
// went, in which case the caller issues nothing at all.
//
// Three properties are deliberate and are locked by tests, because each is the
// kind of thing a later reader would "correct":
//
//   - The product is not part of the match. Retail passes the product
//     definition id in a separate argument that the duplicate test never
//     reads, so a repeat click carrying a different product still removes
//     whatever building was queued at that spot.
//   - The tolerance is a whole cell, inclusive, per axis — a square, not a
//     radius. Sites one cell apart are within tolerance of each other.
//   - Y is not compared. The site height plays no part.
func removeQueuedOrderAtPoint(builder *units.Unit, kind orders.ID, wx, wz numeric.Fixed) bool {
	if builder == nil || kind == 0 {
		return false
	}
	q := orders.QueueForUnit(builder)
	if q == nil {
		return false
	}
	within := func(a, b numeric.Fixed) bool {
		d := a - b
		if d < 0 {
			d = -d
		}
		return d <= queuedPointTolerance // inclusive at the boundary [07 R-P0-11 §6]
	}
	return q.CancelFrontMost(func(n orders.Node) bool {
		return n.ID == kind && within(n.GoalX, wx) && within(n.GoalZ, wz)
	})
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
				s.bindOrderQueue(u)
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
		s.bindOrderQueue(u)
		if q := orders.QueueForUnit(u); q != nil {
			if !c.Activation.Queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, c.Activation.Queued))
		}
	case HumanStance:
		// The selection broadcast of [04 R-STANCE-01 §5]: walk the local
		// player's units in ascending pool order, submit to every one carrying
		// the selection bit, and skip a unit whose definition lacks the
		// matching accept flag. Neither the leader exclusion nor the centroid
		// arm is active for a standing order — both standing descriptors carry
		// static mask 0x10060, which has no target-required bit, and a standing
		// order carries no ground position.
		name := "Standing_MoveOrder"
		if c.Stance.Fire {
			name = "Standing_FireOrder"
		}
		id := orders.Lookup(name)
		if id == 0 {
			return
		}
		for _, h := range s.selectedHumanHandles() {
			u := s.humanUnit(h)
			if u == nil || u.Def == nil {
				continue
			}
			if c.Stance.Fire && !u.Def.FireStandOrders {
				continue
			}
			if !c.Stance.Fire && !u.Def.MobileStandOrders {
				continue
			}
			s.bindOrderQueue(u)
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			// The handler completes on its single visit and returns code 5, so
			// the record is consumed the tick it runs and the displaced head
			// resumes behind it [04 R-STANCE-01 §2]. A stance change is not a
			// new mission: it must not purge the queue the way Stop does.
			node := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false)
			node.Param1 = uint32(c.Stance.Value)
			q.PushHead(id, node)
		}
	case HumanMobileBuild:
		u := s.humanUnit(c.MobileBuild.Builder)
		if u == nil || s.Catalog == nil {
			return
		}
		s.bindOrderQueue(u)
		if c.MobileBuild.Queued {
			// A queued click on a point that already carries a queued order of
			// this kind removes that order and issues nothing [07 R-P0-11 §6].
			// The test runs only in queued mode; a non-queued click purges and
			// re-issues as before.
			if removeQueuedOrderAtPoint(u, mobileBuildKind(u), c.MobileBuild.WX, c.MobileBuild.WZ) {
				return
			}
		} else {
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
		s.bindOrderQueue(u)
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
		if err != nil && s.Build != nil {
			s.Build.RecordCommandRejection(tick, u.Handle, c.FactoryBuild.Product, count, err)
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
		s.bindOrderQueue(u)
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
		s.bindOrderQueue(u)
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
	case HumanSelfDestruct:
		// Ctrl+D resolves the SELFDESTRUCT descriptor and issues it for the
		// selection [07 R-CAM-01 §2]. The front-segment descriptor is the
		// button's [04 R-ORD-01 §2]; the record's own handler owns the
		// countdown, the announcement and the 30000 self-damage.
		id := orders.Lookup("SelfDestructFG")
		if id == 0 {
			id = orders.Lookup("SelfDestruct")
		}
		if id == 0 {
			return
		}
		handles := c.SelfDestruct.Handles
		if len(handles) == 0 {
			handles = s.selectedHumanHandles()
		}
		for _, h := range handles {
			u := s.humanUnit(h)
			if u == nil {
				continue
			}
			s.bindOrderQueue(u)
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			q.Push(id, orders.NewNodeForOrder(id, 0, u.X, u.Y, u.Z, tick, u.Handle, true))
		}
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
			s.bindOrderQueue(u)
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
