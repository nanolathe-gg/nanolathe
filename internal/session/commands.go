package session

import (
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
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

const HumanGameplay HumanCommandKind = 255

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
	HumanSetLogo
	HumanView
	HumanGive
	HumanMakeSelectable
	HumanVisibility
	HumanDoubleShot
	HumanHalfShot
	HumanMeteor
	HumanBigBrother
	HumanShiftState
	HumanCancelQueuedMove
	HumanSpawn
)

// HumanSpawnCommand is the Modern testing command's captured world point.
// See DESIGN_INTERFACE_HUD_INPUT "Modern spawn command".
type HumanSpawnCommand struct {
	Unit    string
	X, Y, Z numeric.Fixed
}

type HumanSelectionCommand struct{ Handles []pool.Handle }

// HumanSelfDestructCommand carries the selection Ctrl+D acts on. The descriptor
// is the front-segment `SelfDestructFG`, which [04 R-ORD-01 §2] names as the
// button's own; the rear-segment `SelfDestruct` is the kamikaze/mine spawn.
type HumanSelfDestructCommand struct{ Handles []pool.Handle }

// HumanOrderTarget is one captured target in an area work list. Unit handles
// are revalidated at the input boundary; feature goals use Position.
type HumanOrderTarget struct {
	Target   pool.Handle
	Position orders.ResolvePos
}

type HumanOrderCommand struct {
	Handles  []pool.Handle
	Code     int
	Target   pool.Handle
	Position orders.ResolvePos
	Queued   bool
	// AssignedPosition is an explicit per-actor destination from a drag
	// formation (DESIGN_INTERFACE_HUD_INPUT §3.11). Ordinary clicks leave it
	// false so the retail selection offsets apply [04 R-STANCE-01 §5].
	AssignedPosition bool
	// TrackQueuedMove gives a queued targetless ground/air move a transient
	// receipt and bypasses the repeat-click toggle. This is explicit Enhanced
	// gesture policy, not retail behavior (DESIGN_INTERFACE_HUD_INPUT §3.10).
	TrackQueuedMove bool
	// Nonempty Targets explicitly requests an area batch under
	// DESIGN_INTERFACE_HUD_INPUT §3.11. Empty Targets retains ordinary clicks.
	Targets []HumanOrderTarget
}
type HumanStopCommand struct{ Handles []pool.Handle }

// HumanCancelQueuedMoveCommand names the first click's move and captured actors
// for the Enhanced construction gesture (DESIGN_INTERFACE_HUD_INPUT §3.10).
type HumanCancelQueuedMoveCommand struct {
	Sequence uint64
	Handles  []pool.Handle
}
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
	// AppendOnly preserves existing orders even at a repeated site. This is
	// explicit command intent for the modern resource shortcut, not a renderer
	// dependency or a change to ordinary Shift placement (DESIGN_INTERFACE_HUD_INPUT §3.10).
	AppendOnly bool
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
type HumanSetLogoCommand struct {
	Player int
	Logo   uint8
}
type HumanViewCommand struct{ Player uint8 }
type HumanGiveCommand struct {
	Player   int
	Resource economy.Res
	Amount   float32
}
type HumanVisibilityCommand struct {
	ToggleMask visibility.Mode
	ClearMask  visibility.Mode
}
type HumanMeteorCommand struct {
	ArgumentPresent bool
	Enabled         bool
}

// HumanStockpileCommand is one MAKENUKE/MAKEANTI click. Count is the click's
// signed count, the same counted producer every build-page toy reaches: +1 for
// a plain left click, +5 for Shift+left, -1 for a plain right click and -5 for
// Shift+right [07 R-P0-11 §1]. Shift scales the count; it is not a queue mode,
// which is why this command carries no queued flag. A zero Count is a caller
// that named no count and enqueues a single round.
type HumanStockpileCommand struct {
	Unit  pool.Handle
	Count int
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
	Mask     [hud.CategoryMaskBytes]byte
}

// HumanCommand is an immutable-at-boundary command value. EnqueueHumanCommand
// copies handle slices and strings so callers may reuse their input buffers.
type HumanCommand struct {
	Gameplay gameplay.Mode
	Spawn    HumanSpawnCommand
	// Sequence and DueTick are session-owned metadata. Callers leave both zero;
	// EnqueueHumanCommand assigns them when the value enters the session queue.
	Sequence         uint64
	DueTick          uint32
	Kind             HumanCommandKind
	Selection        HumanSelectionCommand
	Order            HumanOrderCommand
	Stop             HumanStopCommand
	CancelQueuedMove HumanCancelQueuedMoveCommand
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
	SetLogo          HumanSetLogoCommand
	View             HumanViewCommand
	Give             HumanGiveCommand
	Visibility       HumanVisibilityCommand
	Meteor           HumanMeteorCommand
	ShiftHeld        bool
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
	c.Order.Targets = append([]HumanOrderTarget(nil), c.Order.Targets...)
	c.Stop.Handles = cloneHumanHandles(c.Stop.Handles)
	c.CancelQueuedMove.Handles = cloneHumanHandles(c.CancelQueuedMove.Handles)
	c.SelfDestruct.Handles = cloneHumanHandles(c.SelfDestruct.Handles)
	return c
}

// EnqueueHumanCommand appends one command for the next authoritative input
// phase. It performs no simulation mutation.
func (s *Session) EnqueueHumanCommand(c HumanCommand) error {
	_, err := s.EnqueueHumanCommandWithSequence(c)
	return err
}

// EnqueueHumanCommandWithSequence also returns the session-owned command receipt.
// Enhanced input uses it to replace only its first click's queued move with a
// build at a later input boundary (DESIGN_INTERFACE_HUD_INPUT §3.10).
func (s *Session) EnqueueHumanCommandWithSequence(c HumanCommand) (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("session: nil human-command owner")
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
	// that 30-tick window "is not proof of a universal 30-tick input delay". An
	// entry tagged for the current tick is delivered, not withheld, so the
	// window counts the thirty future deltas only. So
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
	return c.Sequence, nil
}

// PendingHumanCommands returns immutable command copies for diagnostics/tests
// and presentation of input intent awaiting the next authoritative tick.
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

// pausedInputApplicable reports whether a queued command may be applied at the
// paused-input boundary of DESIGN_INTERFACE_HUD_INPUT §3.12, where no sub-tick
// runs. A kind is refused here only when applying it would be simulation work
// rather than input bookkeeping: a random draw on either authoritative stream,
// or a new world object. Retail's pause "suppresses simulation progress"
// [07 §11], and both refusals are chat-console commands, not battle input.
//
// Refusing is not skipping. The drain stops at the first refused command and
// leaves it and everything behind it queued, so enqueue order is preserved
// exactly and the next real tick applies the remainder in sequence.
func pausedInputApplicable(c HumanCommand) bool {
	switch c.Kind {
	case HumanSpawn:
		// The Modern spawn command allocates a unit, which consumes creation
		// draws (see spawn_command.go).
		return false
	case HumanMeteor:
		// The argument-free form enters the storm-arm body, which spends four
		// CRT scheduling draws and starts a strike window [06 §6.5]. The
		// argument form only writes the enabled bit.
		return c.Meteor.ArgumentPresent
	}
	return true
}

// applyPausedHumanCommands drains the longest due, paused-applicable PREFIX of
// the input queue through the same applyHumanCommand path phase 1 uses, and
// reports how many commands were applied. before runs once, after the prefix
// has been taken and before the first application, so a caller can reproduce
// phase 1's own leading work in phase 1's order.
//
// tick is the tick the commands are due for — the tick that has not run — so
// every creation stamp and deadline is the one the unpaused run would have
// written. Exactly-once follows from the queue: a drained command is gone
// before the clock resumes, so phase 1 of that tick applies it no second time
// [01 §4.4].
func (s *Session) applyPausedHumanCommands(tick uint32, before func()) int {
	if s == nil {
		return 0
	}
	s.humanMu.Lock()
	n := 0
	for n < len(s.pendingHuman) {
		c := &s.pendingHuman[n]
		if c.DueTick > tick || !pausedInputApplicable(*c) {
			break
		}
		n++
	}
	if n == 0 {
		s.humanMu.Unlock()
		return 0
	}
	cmds := make([]HumanCommand, n)
	copy(cmds, s.pendingHuman[:n])
	s.pendingHuman = append(s.pendingHuman[:0], s.pendingHuman[n:]...)
	s.humanMu.Unlock()
	if before != nil {
		before()
	}
	for _, c := range cmds {
		s.applyHumanCommand(c, tick)
	}
	return n
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

// PendingBuildPage projects accepted page commands over the committed page for
// the same builder, so repeated host input before a tick preserves enqueue
// order without copying the command queue [07 R-HUD-03 §6][I6].
func (s *Session) PendingBuildPage(builder pool.Handle, page int) int {
	if s == nil {
		return page
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	for i := range s.pendingHuman {
		c := &s.pendingHuman[i]
		if c.Kind == HumanBuildPage && c.BuildPage.Builder == builder {
			page = c.BuildPage.Page
		}
	}
	return page
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
		if mask == ([hud.CategoryMaskBytes]byte{}) {
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
// The world click supplies both terms (Established): outside the MOBILEBUILD
// placement arm, which has its own commit, the world-click commit hands the
// duplicate-testing producer the resolved ground point under the pointer
// alongside the target handle for every latch. The goal term therefore
// participates in a target-click match, exactly as this build assumes
// [07 §9][07 R-P0-11 §6].
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
	case HumanGameplay:
		s.SetGameplay(c.Gameplay)
		return
	case HumanBigBrother:
		s.bigBrother.enabled = !s.bigBrother.enabled
		if s.bigBrother.enabled {
			s.bigBrother.countdown = 1
		} else {
			s.bigBrother.cancelFollow = true
		}
		return
	case HumanShiftState:
		s.bigBrother.shiftHeld = c.ShiftHeld
		return
	case HumanNoShake:
		s.ToggleNoShake()
		return
	case HumanSpawn:
		s.applySpawnCommand(c.Spawn, tick)
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
	case HumanSetLogo:
		if s.Econ == nil || c.SetLogo.Player < 0 || c.SetLogo.Player >= len(s.Econ.Players) {
			return
		}
		p := &s.Econ.Players[c.SetLogo.Player]
		if !p.Exists || p.ControllerState < 1 || p.ControllerState > 3 || p.Side == 10 {
			return
		}
		p.Logo = c.SetLogo.Logo
		return
	case HumanView:
		if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
			s.SetViewingOwner(c.View.Player)
		}
		return
	case HumanGive:
		p := s.playerRecord(c.Give.Player)
		if p == nil || !p.Exists || p.ControllerState < 1 || p.ControllerState > 3 || p.Side == 10 {
			return
		}
		// Resolve the source at drain time so a preceding View in the same
		// input batch takes effect [07 R-CAM-01 §6][05 R-SHARE-01 §2].
		s.Econ.Transfer(s.ViewingOwner, uint8(c.Give.Player), c.Give.Resource, c.Give.Amount)
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
		// Mapping and NowISee carry the bulk refresh's history-reset argument.
		// The latter still resets history when bits are already clear; the command
		// itself, rather than a detected mode transition, selects that argument.
		resetHistory := c.Visibility.ToggleMask&visibility.ModeHistoryEnabled != 0 ||
			c.Visibility.ClearMask&visibility.ModeHistoryEnabled != 0
		eligible, observers := visibilityModeRefreshInputs(s, mode)
		s.Vis.RefreshMode(mode, resetHistory, eligible, observers)
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
	case HumanMeteor:
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			return
		}
		if c.Meteor.ArgumentPresent {
			s.Meteor.Enabled = c.Meteor.Enabled
			return
		}
		// The command-only form enters the same storm-arm body as a due
		// schedule, but deliberately bypasses the enabled-bit test [07
		// R-CAM-01 §6][06 §6.5].
		s.armMeteor(tick)
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
			// Both activation descriptors carry the preserve-queue bit, so
			// even a nonqueued toggle skips the replacement purge. Push still
			// drops leading auto orders and inserts at the head
			// [04 R-ORD-01 §13].
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
			node := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false)
			node.Param1 = uint32(c.Stance.Value)
			if c.Stance.Fire {
				// Keep the existing fire-stance boundary: Modern Hold Fire
				// preserves withdrawal/wait responses and retires automatic
				// attacks at the stance write (DESIGN_UNITS_ORDERS_COB
				// "Modern Hold Fire"). Generic command cleanup would end both.
				q.PushHead(id, node)
			} else {
				// Preserve-queue skips only the replacement purge. Ordinary
				// producer insertion still drops leading auto records and arms
				// the caption before head insertion [04 R-ORD-01 §13]. It also
				// lets Modern supersede danger while retaining the assignment.
				q.Push(id, node)
			}
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
			// Both cloak descriptors preserve the mission without bypassing
			// producer bookkeeping or Modern command precedence. Push inserts
			// them at the head after the leading-auto drop [04 R-ORD-01 §13].
			q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false))
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
			if !c.MobileBuild.AppendOnly && removeQueuedWorldOrder(u, mobileBuildKind(u), 0, c.MobileBuild.WX, c.MobileBuild.WZ) {
				return
			}
		} else {
			if q := orders.QueueForUnit(u); q != nil {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
		}
		// Site placement carries zero production count and uses ordinary
		// insertion, not the counted factory producer [07 R-P0-11 §2].
		// Otherwise each queued structure contributes a spurious +1 caption.
		id := mobileBuildKind(u)
		if id != 0 && content.CanonicalKey(c.MobileBuild.Product) != "" {
			n := orders.NewMobileBuildNode(s.Catalog, c.MobileBuild.Product, c.MobileBuild.WX, c.MobileBuild.WZ, 0, 0, tick, u.Handle, c.MobileBuild.Queued)
			n.GoalY = c.MobileBuild.WY
			q := orders.QueueForUnit(u)
			// Preserve the modern shortcut's existing same-site work without
			// converting it into counted production. Mobile completion consumes
			// the whole site order [04 R-ORD-01 §5].
			if c.MobileBuild.AppendOnly && q.LenPrimary() != 0 {
				tail := q.Primary()[q.LenPrimary()-1]
				if tail.ID == id && tail.BuildDefKey == n.BuildDefKey && tail.GoalX == n.GoalX && tail.GoalZ == n.GoalZ {
					tail.CreationTick, tail.GoalY = tick, n.GoalY
					return
				}
			}
			q.Push(id, n)
		}
	case HumanFactoryBuild:
		u := s.humanUnit(c.FactoryBuild.Builder)
		if u == nil || s.Catalog == nil {
			return
		}
		// Factory products come from the installed GUI name, independently of
		// CANBUILD membership [07 §9][07 R-P0-11 §1].
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
		// The stockpile toy is a counted producer like every other build-page
		// toy: the click's signed count adds or subtracts rounds against the
		// BUILDWEAPON record, and the producer never purges [07 R-P0-11 §1].
		// A caller that named no count asks for one round.
		count := c.Stockpile.Count
		if count == 0 {
			count = 1
		}
		if count < 0 {
			// Negative count: the scan does not stop at the first match, so
			// the TAIL-most matching record is consumed first; a record
			// holding more than the remaining magnitude is subtracted in
			// place, otherwise it is unlinked and the scan repeats with the
			// reduced remainder [07 R-P0-11 §1]. CancelTailMost is that step
			// for a magnitude of one — it decrements a record holding more
			// than one and unlinks it otherwise — so the loop below reaches
			// the same state the single scan does. BUILDWEAPON lives on the
			// REAR segment [04 §3.1], which CancelTailMost searches after the
			// primary one; the match is the descriptor plus the record's
			// build-type operand, the only id a BUILDWEAPON record carries.
			// Nothing matching means nothing changes: the click is already
			// audible, because the cue precedes the routing.
			s.bindOrderQueue(u)
			q := orders.QueueForUnit(u)
			if q == nil {
				return
			}
			matchRound := func(n orders.Node) bool {
				return n.ID == id && n.Param1 == uint32(stockpileAliasSlot)
			}
			for i := 0; i < -count; i++ {
				if !q.CancelTailMost(matchRound) {
					break
				}
			}
			return
		}
		if !orders.StockpileSlotAcceptsBuildWeapon(u, stockpileAliasSlot) {
			return
		}
		s.bindOrderQueue(u)
		// The queued/non-queued argument is NOT the click's Shift bit: the
		// world-order shift chain does not participate on the counted path
		// [07 R-P0-11 §1], and this producer issues no Replace, so it never
		// purges. The argument is inert for a rear-segment record in any case
		// — the caption clear is never called for BUILDWEAPON [04 R-ORD-01 §1].
		n := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false)
		n.Param1, n.Param2 = uint32(stockpileAliasSlot), uint32(count)
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
	case HumanCancelQueuedMove:
		s.applyHumanCancelQueuedMove(c.CancelQueuedMove)
	case HumanOrder:
		if len(c.Order.Targets) != 0 {
			s.applyHumanOrderBatch(c.Order, tick)
			return
		}
		var target *units.Unit
		if c.Order.Target != 0 {
			target = s.humanTarget(c.Order.Target)
		}
		handles := c.Order.Handles
		if len(handles) == 0 {
			handles = s.selectedHumanHandles()
		} else {
			// A captured selection remains a set visited in pool order [I1].
			handles = slices.Clone(handles)
			slices.Sort(handles)
			handles = slices.Compact(handles)
		}
		var excluded pool.Handle
		if !c.Order.AssignedPosition && c.Order.Code != 5 && c.Order.Code != 10 && c.Order.Code != 14 && target != nil {
			excluded = target.Handle // numeric broadcast target exclusion [04 R-STANCE-01 §5]
		}
		var center orders.ResolvePos
		var count int32
		if !c.Order.AssignedPosition {
			center, count = s.humanOrderCentroid(handles, excluded)
		}
		for _, h := range handles {
			u := s.humanUnit(h)
			if u == nil || h == excluded {
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
			if !c.Order.AssignedPosition && orders.DescriptorFor(id).StaticGate&2 != 0 && count != 0 {
				goal := humanFormationGoal(c.Order.Position, u, center, count)
				gx, gy, gz = goal.X, goal.Y, goal.Z
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			trackedMove := c.Order.TrackQueuedMove && c.Order.Queued && c.Order.Target == 0 && isHumanMoveOrder(id)
			if c.Order.Queued {
				// The producer's first act, once per acting unit: a queued
				// click that repeats an already-queued order of this kind at
				// (or within one cell of) the same point removes it and issues
				// nothing [07 R-P0-11 §6]. It runs ONLY in queued mode; a plain
				// click falls through to the Replace below without testing.
				if !trackedMove && removeQueuedWorldOrder(u, id, c.Order.Target, gx, gz) {
					continue
				}
			} else {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			n := orders.NewNodeForOrder(id, c.Order.Target, gx, gy, gz, tick, u.Handle, c.Order.Queued)
			if trackedMove {
				n.HumanMoveSequence = c.Sequence
			}
			q.Push(id, n)
		}
	}
}

// humanOrderCentroid counts the selection before per-actor command admission.
// Whole coordinates are summed before the truncating average; rejected actors
// still contribute [04 R-STANCE-01 §5]. This is integer-only and draws no RNG.
func (s *Session) humanOrderCentroid(handles []pool.Handle, excluded pool.Handle) (orders.ResolvePos, int32) {
	var x, z, count int32
	for _, h := range handles {
		if u := s.humanUnit(h); u != nil && h != excluded {
			x += int32(u.X) >> 16
			z += int32(u.Z) >> 16
			count++
		}
	}
	if count == 0 {
		return orders.ResolvePos{}, 0
	}
	return orders.ResolvePos{X: numeric.Fixed((x / count) << 16), Z: numeric.Fixed((z / count) << 16)}, count
}

// humanFormationGoal preserves nearby actors' offsets; distant outliers keep
// the clicked point. Each square is truncated separately, and equality passes
// the cutoff. Height and first general parameter are unchanged [04 R-STANCE-01 §5].
func humanFormationGoal(goal orders.ResolvePos, u *units.Unit, center orders.ResolvePos, count int32) orders.ResolvePos {
	dx, dz := int32(u.X-center.X), int32(u.Z-center.Z)
	distance := int32((int64(dx)*int64(dx))>>32) + int32((int64(dz)*int64(dz))>>32)
	if distance <= 3000*count {
		goal.X += numeric.Fixed(dx)
		goal.Z += numeric.Fixed(dz)
	}
	return goal
}

func isHumanMoveOrder(id orders.ID) bool {
	return id != 0 && (id == orders.Lookup("Move_Ground") || id == orders.Lookup("VTOL_Move"))
}

func (s *Session) applyHumanCancelQueuedMove(c HumanCancelQueuedMoveCommand) {
	if c.Sequence == 0 {
		return
	}
	for _, h := range c.Handles {
		u := s.humanUnit(h)
		if u == nil {
			continue
		}
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		for {
			var found *orders.Node
			for _, n := range q.Primary() {
				if n != nil && n.HumanMoveSequence == c.Sequence && isHumanMoveOrder(n.ID) {
					found = n
					break
				}
			}
			if found == nil {
				break
			}
			// Use a currently linked pointer: the removal helper's fallback
			// for stale pointers could otherwise remove unrelated work. Reload
			// after cleanup, which can itself mutate the queue [04 §3.3].
			q.RemovePrimaryNode(found, false)
		}
	}
}

// applyHumanOrderBatch preserves the captured target order for each actor.
// The area gesture is an explicit extension (DESIGN_INTERFACE_HUD_INPUT §3.11):
// the first admitted target replaces once unless queued, then all others append
// without the ordinary repeat-click toggle [07 R-P0-11 §6].
func (s *Session) applyHumanOrderBatch(c HumanOrderCommand, tick uint32) {
	handles := c.Handles
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
		queued := c.Queued
		for _, goal := range c.Targets {
			var target *units.Unit
			if goal.Target != 0 {
				target = s.humanTarget(goal.Target)
				if target == nil {
					// A vanished captured unit must not turn into a ground
					// order. Existing targets use [04 R-ORD-02 §1]'s gates.
					continue
				}
			}
			id := orders.Resolve(c.Code, u, target, &goal.Position)
			if id == 0 {
				continue
			}
			gx, gy, gz := goal.Position.X, goal.Position.Y, goal.Position.Z
			if target != nil {
				gx, gy, gz = target.X, target.Y, target.Z
			}
			if !queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, goal.Target, gx, gy, gz, tick, u.Handle, queued))
			queued = true
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
