package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
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
	// HumanCloak is the side rail's CLOAK gadget [04 R-STANCE-01 §2]. It needs
	// its own kind because the cloak arm is a selection broadcast of one of two
	// named descriptors, not a per-unit toggle the way HumanActivation is: one
	// press resolves `Cloak_On` or `Cloak_Off` from the published panel pair and
	// sends that one descriptor to the whole selection.
	HumanCloak
	// HumanSelfDestruct is the Ctrl+D row of [07 R-CAM-01 §2]. It needs its own
	// kind because that row's action is "resolve the SELFDESTRUCT order
	// descriptor" by name, which HumanOrder cannot express: its 1..14 codes are
	// the latch bytes of [07 §9] and none of them is self-destruct.
	HumanSelfDestruct
	// These local typed-command mutations cross the same authoritative input
	// boundary as ordinary battle commands rather than changing the live
	// session from presentation code [07 R-CAM-01 §6].
	HumanNoShake
	HumanATM
	HumanSetResource
	HumanMakeSelectable
	HumanVisibility
	HumanDoubleShot
	HumanHalfShot
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

// HumanCloakCommand is one press of the side panel's CLOAK gadget
// [04 R-STANCE-01 §2]. Presentation reads the published two-bit cloak pair and
// decides the direction with that section's test; this boundary owns the
// broadcast. There is no queue flag: the arm takes no Shift argument, exactly
// as the stance arm beside it does not.
type HumanCloakCommand struct {
	// Cloak selects `Cloak_On`; otherwise `Cloak_Off`.
	Cloak bool
}

type HumanCancelProductionCommand struct{ Unit pool.Handle }
type HumanSetResourceCommand struct {
	Player   int
	Resource economy.Res
	Amount   float32
}
type HumanVisibilityCommand struct {
	ToggleMask visibility.Mode
	ClearMask  visibility.Mode
}
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
	Cloak            HumanCloakCommand
	SelfDestruct     HumanSelfDestructCommand
	SetResource      HumanSetResourceCommand
	Visibility       HumanVisibilityCommand
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
	// boundary, and no offset belongs here. Established [08 "Soft pacing — no
	// per-tick input barrier"]: "no fixed input-delay constant exists", bounded
	// over the packet registry, the custom send/receive chain and both
	// schedulers. The only future window in the image is on the RECEIVE side and
	// is applied locally: the receiver tags each decoded record with its own
	// simulation tick at parse time and withholds entries tagged 1..30 ticks
	// ahead [08 "Receive buffering"] — the sender never names a target frame, and
	// that 31-tick window "is not proof of a universal 30-tick input delay". So
	// the next session tick is the contract, not a placeholder for one. The
	// receive path itself is multiplayer-only and out of scope [08 R-OOS-01 §3].
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
		// The retail page state is keyed by the single selected unit's
		// identity. A unit mixed with another selected one has aggregate
		// command state, not a page [07 §9].
		if selected != nil {
			return nil
		}
		selected = u
	}
	if selected == nil || selected.Handle != h || selected.Def == nil {
		return nil
	}
	return selected
}

func (s *Session) applyHumanBuildPage(c HumanBuildPageCommand) {
	u := s.selectedHumanBuilder(c.Builder)
	if u == nil || s.Catalog == nil {
		return
	}
	// Which pages exist is the definition's page-count byte and nothing else
	// [07 R-HUD-03 §6][02 R-CAT-01 §5 step 5]. The CANBUILD membership test
	// that used to stand here, beside selectedHumanBuilder's FBI `Builder`
	// word, refused the ORDERS/BUILD toggle and the page keys on the eight
	// stockpile launchers — the units that author a page window and build
	// nothing [06 §11.1]. SetBuildPage below carries the count guard.
	pageCount := hud.BuilderPageCount(u.Def)
	if pageCount == 0 {
		return
	}
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

// Selection does not touch a builder's page state. The writer that puts a
// builder on its first build page is UNIT CREATION: the unit initializer seeds
// page field 1 with the paged bit set for every definition whose page-count
// byte is 2 or more, and clears both otherwise (internal/units' initial status
// flags builder). A
// selection only reads the bits; the BUILD/ORDERS clicks set or clear the
// paged bit, and the page field is written only by the page keys and gadgets
// [07 §9][07 R-HUD-04 §4 "First build page"]. The selection-time default that
// used to stand here re-applied the same seed and is gone.

// insertedBuildNode picks the record the construction producer just created,
// given the primary segment as it stood before the call.
//
// Correction (WU-19-227). stampHumanBuild used to assume the record was the
// primary TAIL. It is not: [04 §3.3]'s producer insertion links a new record
// "immediately after the currently active order" and only "appends at the tail
// when no record carries" the active marker. Any queue whose marker is not on
// its last record therefore receives the new build node in the middle, and the
// tail assumption then stamped the click's site height (GoalY) and creation
// tick onto a DIFFERENT queued building — which is what moved an unrelated
// queued site's overlay marker vertically after a Shift-click removal, while
// the record actually created kept GoalY 0 and drew at sea level.
//
// A nil answer means the producer coalesced into an existing record instead of
// allocating one ([05 "Queue insertion"]'s tail-only counted coalesce); the
// caller then falls back to the tail, which is the record that coalesce grew.
func insertedBuildNode(before, after []*orders.Node) *orders.Node {
	if len(after) <= len(before) {
		return nil
	}
	for _, n := range after {
		if n == nil {
			continue
		}
		known := false
		for _, b := range before {
			if b == n {
				known = true
				break
			}
		}
		if !known {
			return n
		}
	}
	return nil
}

func stampHumanBuild(u *units.Unit, before []*orders.Node, product string, tick uint32, queued bool, goalY numeric.Fixed) {
	if u == nil {
		return
	}
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	prim := q.Primary()
	node := insertedBuildNode(before, prim)
	if node == nil {
		node = prim[len(prim)-1]
	}
	if node == nil || node.BuildDefKey != content.CanonicalKey(product) {
		return
	}
	node.Owner = u.Handle
	node.CreationTick = tick
	node.GoalY = goalY
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

// removeQueuedWorldOrder is retail's queued-order duplicate test
// [07 R-P0-11 §6]. It is the FIRST act of the one producer every world order
// the interface issues goes through, and it runs only when the click's queue
// flag (the Shift bit) is set: walk the acting unit's primary queue from the
// front and remove the first node for which all of
//
//   - the node's order kind equals the kind being issued;
//   - the issued target handle is absent (zero) or equals the node's target;
//   - the issued goal lies within one map cell of the node's goal on X and on
//     Z independently
//
// hold. It reports whether a node went, in which case the caller issues
// nothing at all — a repeat Shift-click is a toggle that removes exactly one
// queued order, front-most match first, and produces no order of its own.
//
// One shared path, deliberately: retail has one producer, so a per-kind copy
// of this test would be a second contract to keep in step. Both world-click
// boundaries — the mobile-build placement click and the resolved order click
// — call this.
//
// Four properties are deliberate and are locked by tests, because each is the
// kind of thing a later reader would "correct":
//
//   - The product is not part of the match. Retail passes the product
//     definition id in a separate argument that the duplicate test never
//     reads, so a repeat click carrying a different product still removes
//     whatever building was queued at that spot.
//   - The tolerance is a whole cell, inclusive, per axis — a square, not a
//     radius. Sites one cell apart are within tolerance of each other.
//   - Y is not compared. The site height plays no part.
//   - The kinds are compared as resolved identities, so `MOBILEBUILD` and
//     `VTOL_MOBILEBUILD` — and equally the ground and air forms of a move or
//     an attack — are distinct and do not match each other.
//
// TODO(question): whether the world-click producer receives a goal point
// alongside a target handle. [07 R-P0-11 §6] states the match rule with both
// arguments optional but does not say which the click supplies for a
// target-click order; [07 §9] step 3 says the click issues "at the pointer's
// world point", so this boundary supplies both and the goal term therefore
// participates in a target-click match. If retail passes no goal there, a
// repeat Shift-attack-click on a target that has moved more than one cell
// since the order was queued would remove it where this build re-queues.
// Deciding it needs a trace of the world-click handler's call into the
// producer.
//
// TODO(question): whether the interface's non-world-click queued issues share
// this producer. [07 R-P0-11 §6] scopes the test to "every world order the
// interface issues", and the two world-click boundaries are the only callers
// here. The side panel's own buttons — Stop, the activation toggle, stockpile,
// Ctrl+D self-destruct, the two stance gadgets — issue no world point, and the
// section does not say whether a Shift-held press of one of them runs the test
// (which would make a second Shift-press cancel the first). Deciding it needs
// a trace of those button handlers' call into the producer.
func removeQueuedWorldOrder(actor *units.Unit, kind orders.ID, target pool.Handle, wx, wz numeric.Fixed) bool {
	if actor == nil || kind == 0 {
		return false
	}
	q := orders.QueueForUnit(actor)
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
		if n.ID != kind {
			return false
		}
		if target != 0 && n.Target != target {
			return false
		}
		return within(n.GoalX, wx) && within(n.GoalZ, wz)
	})
}

func (s *Session) applyHumanCommand(c HumanCommand, tick uint32) {
	if s == nil {
		return
	}
	switch c.Kind {
	case HumanNoShake:
		s.ToggleNoShake()
		return
	case HumanATM:
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			return
		}
		if s.Econ == nil || int(s.LocalOwner) >= len(s.Econ.Players) {
			return
		}
		p := &s.Econ.Players[s.LocalOwner]
		if !p.Exists {
			return
		}
		economy.CreditSpawn(p, economy.Metal, 1000)
		economy.CreditSpawn(p, economy.Energy, 1000)
		return
	case HumanSetResource:
		if s.Econ == nil || c.SetResource.Player < 0 || c.SetResource.Player >= len(s.Econ.Players) {
			return
		}
		if c.SetResource.Resource != economy.Metal && c.SetResource.Resource != economy.Energy {
			return
		}
		p := &s.Econ.Players[c.SetResource.Player]
		if !p.Exists || p.ControllerState < 1 || p.ControllerState > 3 || p.Side == 10 {
			return
		}
		p.Stock[c.SetResource.Resource] = c.SetResource.Amount
		return
	case HumanVisibility:
		if s.Vis == nil {
			return
		}
		const mask2 = visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled
		if c.Visibility.ToggleMask&mask2 != 0 || c.Visibility.ClearMask&mask2 != 0 {
			if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
				return
			}
		}
		const semantic = mask2 | visibility.ModeTerrainRay
		mode := s.Vis.Mode()
		mode ^= c.Visibility.ToggleMask & semantic
		mode &^= c.Visibility.ClearMask & semantic
		s.Vis.SetMode(mode)
		return
	case HumanDoubleShot, HumanHalfShot:
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			return
		}
		if s.Combat == nil {
			return
		}
		if c.Kind == HumanDoubleShot {
			s.Combat.ToggleDoubleShot()
		} else {
			s.Combat.ToggleHalfShot()
		}
		return
	}
	if s.Units == nil {
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
	case HumanSelectionToggle:
		for _, h := range c.Selection.Handles {
			if u := s.humanUnit(h); u != nil {
				u.Flags ^= 0x10
			}
		}
	case HumanSelectionClear:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive && u.Owner == s.LocalOwner {
				u.Flags &^= 0x10
			}
		}
	case HumanMakeSelectable:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive {
				u.Flags |= units.ClassifierEligibleStatus
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
	case HumanCloak:
		// The cloak arm of the same battle-panel handler as the two stance
		// gadgets [04 R-STANCE-01 §2]: it resolves one of the two named
		// descriptors and transmits it through the ordinary selection broadcast
		// [04 R-STANCE-01 §5], with the general parameter left at zero.
		//
		// Unlike the stance arm, the broadcast applies no definition gate of its
		// own here — §5's skip tests name only the two standing descriptors — so
		// every selected unit receives the record and the capability test is the
		// order handler's own: `Cloak_On`/`Cloak_Off` set or clear the
		// cloak-requested bit only when the definition is cloak-capable, derived
		// as `cloakcost > 0`, and complete either way [04 R-ORD-01 §2].
		//
		// Neither the leader exclusion nor the centroid arm applies: both cloak
		// descriptors carry static gate mask 0x10060, which has no
		// target-required bit, and the command carries no ground position.
		name := "Cloak_Off"
		if c.Cloak.Cloak {
			name = "Cloak_On"
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
			s.bindOrderQueue(u)
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			// The handler returns code 5, so the record is consumed on its
			// single visit and the displaced head resumes behind it
			// [04 R-ORD-01 §2]. A cloak toggle is not a new mission and must
			// not purge the queue the way Stop does — the same reading the
			// stance arm above applies.
			q.PushHead(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false))
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
			if removeQueuedWorldOrder(u, mobileBuildKind(u), 0, c.MobileBuild.WX, c.MobileBuild.WZ) {
				return
			}
		} else {
			if q := orders.QueueForUnit(u); q != nil {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
		}
		// The segment as it stands before the producer runs is what identifies
		// the record the producer creates; it is not the tail. See
		// insertedBuildNode.
		beforeMobile := orders.QueueForUnit(u).Primary()
		if err := construction.QueueMobileBuild(u, c.MobileBuild.Product, c.MobileBuild.WX, c.MobileBuild.WZ, 1, s.Catalog); err == nil {
			stampHumanBuild(u, beforeMobile, c.MobileBuild.Product, tick, c.MobileBuild.Queued, c.MobileBuild.WY)
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
		beforeFactory := orders.QueueForUnit(u).Primary()
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
			// A counted factory node is a no-purge command: the count IS the
			// queue modifier, so this producer never issues a Replace.
			stampHumanBuild(u, beforeFactory, c.FactoryBuild.Product, tick, false, 0)
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
		// The UI alias path always supplies zero, which is where shipped
		// stockpile weapons live [06 §11.1]; the node constructor then stores
		// that build-type argument verbatim, and the handler selects the slot
		// with it and no search [06 R-WPN-05 §2]. The slot hunt that used to
		// stand here — "find the first slot carrying a `stockpile` weapon" —
		// named a slot the alias never names.
		//
		// The refusal is the enqueue guard's: a node whose named slot holds
		// weapon record 0 or a weapon without `stockpile` would complete every
		// queued round free in one visit and, if it outlived the visit, fault
		// the build page's percentage on a divide by zero [06 R-WPN-05 §2]. No
		// shipped click reaches it — a MAKENUKE/MAKEANTI button is authored
		// only where slot 0 holds a stockpile weapon — so refusing is both safe
		// and indistinguishable from retail here.
		const stockpileAliasSlot = 0 // [06 §11.1] the alias's build-type argument
		if !orders.StockpileSlotAcceptsBuildWeapon(u, stockpileAliasSlot) {
			return
		}
		s.bindOrderQueue(u)
		n := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, c.Stockpile.Queued)
		n.Param1, n.Param2 = uint32(stockpileAliasSlot), 1
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
			if c.Order.Queued {
				// The producer's first act, once per acting unit: a queued
				// click that repeats an already-queued order of this kind at
				// (or within one cell of) the same point removes it and issues
				// nothing [07 R-P0-11 §6]. It runs ONLY in queued mode; a plain
				// click falls through to the Replace below without testing.
				if removeQueuedWorldOrder(u, id, c.Order.Target, gx, gz) {
					continue
				}
			} else {
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
