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

// HumanDeveloperSpawn is the retail developer default handler, separate from
// the Modern exact-name command and the reserved online kinds 35..45.
const HumanDeveloperSpawn HumanCommandKind = 46

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
	HumanBuilderOptions
	HumanCommunityOrderDrag
	HumanCommunityKickout
)

// HumanSpawnCommand is the Modern testing command's captured world point.
// See DESIGN_INTERFACE_HUD_INPUT "Modern spawn command".
type HumanSpawnCommand struct {
	Unit    string
	X, Y, Z numeric.Fixed
}

// HumanDeveloperSpawnCommand captures an authorized developer submission.
// Authorization belongs to the local producer; replay stores the accepted
// request rather than depending on unrecorded host access [07 R-CAM-01 §6].
type HumanDeveloperSpawnCommand struct {
	Pattern string
	Owner   uint8
	X, Y, Z numeric.Fixed
}

type HumanSelectionCommand struct{ Handles []pool.Handle }

// HumanSelfDestructCommand carries the selection Ctrl+D acts on, and whether
// Shift was held, which makes an issue queued [07 R-CAM-01 §2]. The descriptor
// is the rear-segment `SelfDestruct`. The front-segment `SelfDestructFG` is
// only the mission `d` token's [04 §3.6].
type HumanSelfDestructCommand struct {
	Handles []pool.Handle
	Queued  bool
}

// HumanOrderTarget is one captured target in an area work list. Unit handles
// are revalidated at the input boundary; feature goals use Position.
type HumanOrderTarget struct {
	Target   pool.Handle
	Position orders.ResolvePos
}

type HumanOrderCommand struct {
	// StagedCount, when it exceeds the number of acting units, is the size
	// of the selection the order was given to. Only a replay's staging sets
	// it (Session.StageGroupMove): a recorded group can have had members
	// the replay does not stage, and the formation cutoff counts them all
	// [04 R-STANCE-01 §5]. The command boundary leaves it zero.
	StagedCount int32
	Handles     []pool.Handle
	Code        int
	Target      pool.Handle
	Position    orders.ResolvePos
	Queued      bool
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

// HumanCommunityOrderDragCommand carries a committed queue receipt and the
// final cursor point. InstanceID prevents a recycled unit slot from accepting
// a delayed host gesture [community patch engine behavior §5.11].
type HumanCommunityOrderDragCommand struct {
	InstanceID uint64
	Receipt    orders.CommunityOrderDragReceipt
	Position   orders.CommunityOrderDragDestination
}

// HumanCommunityKickoutCommand is CP-CON-1's manual override gesture. The
// destination deliberately has no placement-validation bit: the sourced path
// rewrites the unit's orders directly.
type HumanCommunityKickoutCommand struct {
	Unit       pool.Handle
	InstanceID uint64
	X, Y, Z    numeric.Fixed
}
type HumanActivationCommand struct {
	Unit             pool.Handle
	Activate, Queued bool
}
type HumanMobileBuildCommand struct {
	Facing  units.StructureFacing // CP-CON-5, clamped through the construction rule at issue.
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
	// Handles is the selection the press broadcasts to, in the ascending
	// order of the former selection scan; the client resolves it from its own
	// selection when the press is sent (DESIGN_MULTIPLAYER §7.3, §7.4.3).
	Handles []pool.Handle
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
	// Handles is the selection the press broadcasts to, as for a stance.
	Handles []pool.Handle
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
// into this value from the frame [07 §9]. The build page is local interface
// state (DESIGN_MULTIPLAYER §7.3): the host applies this value to its own
// local state and the session refuses it.
type HumanBuildPageCommand struct {
	Builder pool.Handle
	Page    int
}

// HumanGroupCommand carries the established Ctrl+digit assignment or digit
// recall operation [07 §9]. Assignment is a seat command: Handles is its
// complete new membership, the client's selection when the keys are pressed,
// because the group number is also computer-player state
// (DESIGN_MULTIPLAYER §7.1). Recall changes only the local selection, so the
// host applies it to its own local state and the session refuses it; Preserve
// is recall's Shift-held toggle/preserve argument, and Mask the authored
// CTRL_F filter when that state is available (an all-zero value is the
// no-filter path).
type HumanGroupCommand struct {
	Handles  []pool.Handle
	Group    int
	Preserve bool
	Mask     [hud.CategoryMaskBytes]byte
}

// HumanCommand is an immutable-at-boundary command value. EnqueueHumanCommand
// copies handle slices and strings so callers may reuse their input buffers.
type HumanCommand struct {
	BuilderOptions HumanBuilderOptionsCommand
	Gameplay       gameplay.Mode
	Spawn          HumanSpawnCommand
	DeveloperSpawn HumanDeveloperSpawnCommand
	// Sequence and DueTick are session-owned metadata. Callers leave both zero;
	// EnqueueHumanCommand assigns them when the value enters the session queue.
	Sequence           uint64
	DueTick            uint32
	Kind               HumanCommandKind
	Selection          HumanSelectionCommand
	Order              HumanOrderCommand
	Stop               HumanStopCommand
	CancelQueuedMove   HumanCancelQueuedMoveCommand
	CommunityOrderDrag HumanCommunityOrderDragCommand
	CommunityKickout   HumanCommunityKickoutCommand
	Activation         HumanActivationCommand
	MobileBuild        HumanMobileBuildCommand
	FactoryBuild       HumanFactoryBuildCommand
	CancelProduction   HumanCancelProductionCommand
	Stockpile          HumanStockpileCommand
	BuildPage          HumanBuildPageCommand
	Group              HumanGroupCommand
	Stance             HumanStanceCommand
	Cloak              HumanCloakCommand
	SelfDestruct       HumanSelfDestructCommand
	SetResource        HumanSetResourceCommand
	SetLogo            HumanSetLogoCommand
	View               HumanViewCommand
	Give               HumanGiveCommand
	Visibility         HumanVisibilityCommand
	Meteor             HumanMeteorCommand
	ShiftHeld          bool

	// seat is a stamped stream entry riding this queue (EnqueueSeatCommand);
	// nil for every local command. One queue keeps stamped and local entries
	// in one order through one drain (DESIGN_MULTIPLAYER §7.4.4).
	seat *seatQueued
	// refs is the local adapter's capture of the command's explicit unit
	// handles as allocation references (DESIGN_MULTIPLAYER §7.4.4).
	refs localRefs
}

// localRefs holds the explicit handles of a local command as allocation
// references, captured when the command is submitted. There are no other
// actors: selection is client-side local state (DESIGN_MULTIPLAYER §7.3), and
// the client resolves every order's units from its own selection when it
// sends the order, so a selection made earlier in the same input batch is
// already in the handles a later command carries.
type localRefs struct {
	captured bool
	actors   []pool.UnitRef // Order, Stop, SelfDestruct, CancelQueuedMove, Stance, Cloak, GroupAssign handles
	target   pool.UnitRef   // Order.Target
	targets  []pool.UnitRef // Order.Targets[i].Target, parallel
	unit     pool.UnitRef   // the singular actor of the unit-addressed kinds
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
	c.Stance.Handles = cloneHumanHandles(c.Stance.Handles)
	c.Cloak.Handles = cloneHumanHandles(c.Cloak.Handles)
	c.Group.Handles = cloneHumanHandles(c.Group.Handles)
	c.refs.actors = cloneRefs(c.refs.actors)
	c.refs.targets = cloneRefs(c.refs.targets)
	return c
}

// captureRef names the allocation now occupying a handle's slot. The handle
// is kept even when its unit has already died: the slot's raw record
// (World.RawUnitRecord) still shows a dying unit's serial, and a freed or
// never-allocated slot shows zero, which no lookup accepts. Either way the
// reference is stale rather than null, so an ordinary order to a dead target
// keeps the single-player result §7.4.3 says the local adapter preserves —
// a ground order built with the dead handle. A local reference with a zero
// serial never leaves the session; it has no wire form.
func (s *Session) captureRef(h pool.Handle) pool.UnitRef {
	if h == 0 {
		return pool.UnitRef{}
	}
	var serial uint64
	if s != nil && s.Units != nil {
		if u := s.Units.RawUnitRecord(h); u != nil {
			serial = u.AllocationSerial
		}
	}
	return pool.UnitRef{Handle: h, Serial: serial}
}

func (s *Session) captureRefs(hs []pool.Handle) []pool.UnitRef {
	if len(hs) == 0 {
		return nil
	}
	out := make([]pool.UnitRef, len(hs))
	for i, h := range hs {
		out[i] = s.captureRef(h)
	}
	return out
}

// captureLocalRefs records the command's explicit handles as references. It
// runs at submission; a command handed to phase 1 without passing through
// EnqueueHumanCommand (replay staging, tests) is captured when applied, which
// is the same instant for the queue's purposes because nothing runs between.
func (s *Session) captureLocalRefs(c *HumanCommand) {
	if c.refs.captured {
		return
	}
	c.refs = localRefs{captured: true}
	switch c.Kind {
	case HumanOrder:
		c.refs.actors = s.captureRefs(c.Order.Handles)
		c.refs.target = s.captureRef(c.Order.Target)
		if len(c.Order.Targets) != 0 {
			c.refs.targets = make([]pool.UnitRef, len(c.Order.Targets))
			for i := range c.Order.Targets {
				c.refs.targets[i] = s.captureRef(c.Order.Targets[i].Target)
			}
		}
	case HumanStop:
		c.refs.actors = s.captureRefs(c.Stop.Handles)
	case HumanSelfDestruct:
		c.refs.actors = s.captureRefs(c.SelfDestruct.Handles)
	case HumanCancelQueuedMove:
		c.refs.actors = s.captureRefs(c.CancelQueuedMove.Handles)
	case HumanStance:
		c.refs.actors = s.captureRefs(c.Stance.Handles)
	case HumanCloak:
		c.refs.actors = s.captureRefs(c.Cloak.Handles)
	case HumanGroupAssign:
		c.refs.actors = s.captureRefs(c.Group.Handles)
	case HumanActivation:
		c.refs.unit = s.captureRef(c.Activation.Unit)
	case HumanMobileBuild:
		c.refs.unit = s.captureRef(c.MobileBuild.Builder)
	case HumanFactoryBuild:
		c.refs.unit = s.captureRef(c.FactoryBuild.Builder)
	case HumanCancelProduction:
		c.refs.unit = s.captureRef(c.CancelProduction.Unit)
	case HumanStockpile:
		c.refs.unit = s.captureRef(c.Stockpile.Unit)
	}
}

// LocalInterfaceKind reports the local-only (L) kinds of DESIGN_MULTIPLAYER
// §7.1: selection, build pages, group recall, BigBrother and Shift. They are
// client-side local interface state (§7.3), applied by the host to its own
// local state; the session holds none of it and refuses them. Their numbers
// stay reserved so the kind numbering of §7.4.2 does not move.
func LocalInterfaceKind(k HumanCommandKind) bool {
	switch k {
	case HumanSelectionReplace, HumanSelectionToggle, HumanSelectionClear, HumanBuildPage, HumanGroupRecall, HumanBigBrother, HumanShiftState:
		return true
	}
	return false
}

// OnlineCommandContext reports whether the session runs the online command
// context, where every command that changes the world must arrive stamped and
// the replay-only kinds (NoShake, SetLogo) are local presentation preferences
// of the issuing client (DESIGN_MULTIPLAYER §7.1).
func (s *Session) OnlineCommandContext() bool {
	if s == nil {
		return false
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	return s.seatCommands.online != nil
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
//
// It is the single-player compatibility adapter of DESIGN_MULTIPLAYER
// §7.4.4: it captures the command's explicit handles as allocation
// references here, at submission, and phase 1 hands the command to the same
// payload implementation a stamped seat command reaches (applyBound). Every
// actor list is explicit: the client resolves selection-derived actors from
// its own selection before it submits (§7.3). The local interface kinds never
// reach the session (LocalInterfaceKind). In an online session it admits
// nothing at all: every command that changes the world must arrive stamped
// through EnqueueSeatCommand, so no kind can fall through to this path
// unauthorized (§16.2 M2-C2).
func (s *Session) EnqueueHumanCommandWithSequence(c HumanCommand) (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("session: nil human-command owner")
	}
	if LocalInterfaceKind(c.Kind) {
		return 0, fmt.Errorf("nanolathe: local command refused: logical path human command kind %d, providers searched [session], expected the client's local interface state (DESIGN_MULTIPLAYER §7.3)", c.Kind)
	}
	s.humanMu.Lock()
	online := s.seatCommands.online != nil
	s.humanMu.Unlock()
	if online {
		return 0, fmt.Errorf("nanolathe: local command refused: logical path human command kind %d, providers searched [session], expected a stamped seat command in an online session", c.Kind)
	}
	// A caller cannot supply the capture or a stamped entry.
	c.seat = nil
	c.refs = localRefs{}
	s.captureLocalRefs(&c)
	if c.Kind == HumanBuilderOptions {
		if err := s.validateBuilderOptions(c.BuilderOptions); err != nil {
			return 0, err
		}
	}
	if c.Kind == HumanGameplay {
		if _, err := ResolveCommunity(c.Gameplay.Normalize(), s.CommunitySources); err != nil {
			return 0, err
		}
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

// HasPendingHumanCommand reports whether a command of this kind waits for the
// next authoritative boundary. A host that runs the sub-ticks on their own
// goroutine asks before handing a pump over (docs/DESIGN_GPU_RENDERER.md
// §13.13).
func (s *Session) HasPendingHumanCommand(kind HumanCommandKind) bool {
	if s == nil {
		return false
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	for i := range s.pendingHuman {
		c := &s.pendingHuman[i]
		if c.seat != nil {
			// A stamped rule-set switch (single-player replay) reassigns the
			// same HUD-read rule state as a local one, so the host must see it.
			if kind == HumanGameplay && c.seat.command.Kind == SeatGameplay {
				return true
			}
			continue
		}
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// PendingHumanCommands returns immutable command copies for diagnostics/tests
// and presentation of input intent awaiting the next authoritative tick.
// Stamped seat entries are not human commands and are not listed.
func (s *Session) PendingHumanCommands() []HumanCommand {
	if s == nil {
		return nil
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	out := make([]HumanCommand, 0, len(s.pendingHuman))
	for i := range s.pendingHuman {
		if s.pendingHuman[i].seat != nil {
			continue
		}
		out = append(out, cloneHumanCommand(s.pendingHuman[i]))
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
		s.checkpointConsumedInput()
		s.applyAndRecordHumanCommand(c, tick)
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
	if c.seat != nil {
		// The paused boundary is the single-player local adapter's alone: a
		// stamped entry waits for phase 1 of its own tick (DESIGN_MULTIPLAYER
		// §4.2, §7.4.4), and it holds everything queued behind it.
		return false
	}
	switch c.Kind {
	case HumanSpawn, HumanDeveloperSpawn:
		// A spawn command allocates a unit, which consumes creation
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
		s.checkpointConsumedInput()
		s.applyAndRecordHumanCommand(c, tick)
	}
	return n
}

// humanUnit admits a live unit of the seat the command being applied acts for:
// the entry's seat while phase 1 applies a stamped seat command, otherwise the
// local own/controlling slot (DESIGN_MULTIPLAYER §7.2). The issuer is command
// attribution, not a perspective; nothing here reads or moves a viewing slot.
func (s *Session) humanUnit(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	u := s.Units.Unit(h)
	if u == nil || !u.Alive || u.Owner != s.commandIssuer() {
		return nil
	}
	return u
}

// commandIssuer is the seat humanUnit admits units of.
func (s *Session) commandIssuer() uint8 {
	if s.seatCommands.issuing {
		return s.seatCommands.issuer
	}
	return s.LocalOwner
}

// commandActor resolves an actor reference: a live unit with that allocation
// serial, owned by the issuer. A serial mismatch is a stale actor, never the
// slot's new occupant (§16.2 M2-C3).
func (s *Session) commandActor(issuer uint8, r pool.UnitRef) *units.Unit {
	if s == nil || s.Units == nil {
		return nil
	}
	u := s.Units.LookupReference(r)
	if u == nil || !u.Alive || u.Owner != issuer {
		return nil
	}
	return u
}

// commandTarget resolves a target reference, which may name any player's unit.
func (s *Session) commandTarget(r pool.UnitRef) *units.Unit {
	if s == nil || s.Units == nil {
		return nil
	}
	u := s.Units.LookupReference(r)
	if u == nil || !u.Alive {
		return nil
	}
	return u
}

// groupViews lists an owner's live units that carry a nonzero catalog
// definition id, in ascending pool order, as the group scanner sees them
// [07 §9]. The explicit membership of the assignment stands in for the
// selection bit, which is the client's (DESIGN_MULTIPLAYER §7.3): selected
// sets it on exactly the members.
func (s *Session) groupViews(owner uint8, selected func(pool.Handle) bool) ([]*hud.SelectUnit, []*units.Unit) {
	views := make([]*hud.SelectUnit, 0)
	unitsByView := make([]*units.Unit, 0)
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != owner || u.Def == nil {
			continue
		}
		defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
		if !ok || defID == 0 || defID > 0xffff {
			continue
		}
		flags := u.Flags &^ hud.SelectionFlag
		if selected(u.Handle) {
			flags |= hud.SelectionFlag
		}
		views = append(views, &hud.SelectUnit{Flags: flags, Group: u.Group, DefID: uint16(defID)})
		unitsByView = append(unitsByView, u)
	}
	return views, unitsByView
}

// applyBoundGroupAssign is Ctrl+digit assignment over explicit membership:
// every live member of the issuer takes the group, and every other unit of
// the issuer carrying it loses it, through the same scanner rule as before
// [07 §9]. Only the group number is written; selection is the client's
// (DESIGN_MULTIPLAYER §7.1, §7.4.3).
func (s *Session) applyBoundGroupAssign(b *boundCommand) {
	if s.Catalog == nil || b.c.Group.Group < 1 || b.c.Group.Group > 9 {
		return
	}
	members := s.boundActors(b)
	var mark []bool
	for _, h := range members {
		if int(h) >= len(mark) {
			mark = append(mark, make([]bool, int(h)+1-len(mark))...)
		}
		mark[h] = true
	}
	views, unitsByView := s.groupViews(b.issuer, func(h pool.Handle) bool {
		return int(h) < len(mark) && mark[h]
	})
	hud.AssignGroup(views, b.c.Group.Group, nil)
	for i, view := range views {
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

func stampHumanBuild(u *units.Unit, before []*orders.Node, product string, tick uint32, queued bool, goalY numeric.Fixed, facing ...units.StructureFacing) {
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
	if len(facing) != 0 {
		node.BuildFacing = facing[0]
	}
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
	within := func(stored, issued numeric.Fixed) bool {
		// The biased window is evaluated in the raw coordinate word, including
		// wrap at its signed boundary [04 R-MOV-03 §6].
		d := uint32(issued) - uint32(stored) + uint32(queuedPointTolerance)
		return d <= 2*uint32(queuedPointTolerance)
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

// applyHumanCommand is phase 1's one entry for a queued element, in queue
// order. A stamped seat entry is authorized and yields a receipt
// (applyQueuedSeatCommand). A local command, bound to references and to the
// own/controlling slot at drain time, goes to the shared payload
// implementation (applyBound) that stamped commands reach too
// (DESIGN_MULTIPLAYER §7.4.4). A local interface kind applies nothing: that
// state is the client's (§7.3), and the queue never admits one.
func (s *Session) applyHumanCommand(c HumanCommand, tick uint32) {
	if s == nil {
		return
	}
	if c.seat != nil {
		s.applyQueuedSeatCommand(c.seat, tick)
		return
	}
	if LocalInterfaceKind(c.Kind) {
		return
	}
	b := s.bindLocalCommand(c)
	s.applyBound(&b, tick)
}

// boundCommand is one command as the shared payload implementation takes it:
// the payload at the local record's width, the issuing seat, and every actor
// and target as an allocation reference in processing order. Both adapters
// produce it — the local one from a HumanCommand (bindLocalCommand), the
// stamped one from a SeatCommand (bindSeatCommand) — so one body applies
// both (DESIGN_MULTIPLAYER §7.4.4).
type boundCommand struct {
	c HumanCommand
	// issuer owns the actors and is the source of every seat-owned mutation:
	// the stamped seat online, the own/controlling slot at drain time in
	// single-player [07 R-CAM-01 §6].
	issuer uint8
	// online applies the online stale-target contract of §7.4.3; the local
	// adapter and the single-player replay context keep the single-player
	// result.
	online bool
	// stamped commands name Community actors by allocation reference; the
	// local adapter keeps its publication-identity check.
	stamped bool
	// sequence is the tracked-move receipt: the stream position of a stamped
	// entry, the local sequence of a local one.
	sequence uint64
	actors   []pool.UnitRef
	target   pool.UnitRef
	targets  []pool.UnitRef // parallel to c.Order.Targets
	unit     pool.UnitRef
}

// sortedUniqueRefs is the ordinary order's captured selection as a set,
// visited in pool order [I1]. One handle captured twice carries one serial.
func sortedUniqueRefs(in []pool.UnitRef) []pool.UnitRef {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b pool.UnitRef) int { return int(a.Handle) - int(b.Handle) })
	return slices.CompactFunc(out, func(a, b pool.UnitRef) bool { return a.Handle == b.Handle })
}

// bindLocalCommand is the local adapter's half of phase 1. Every actor was
// captured at submission from the command's explicit handles. There is no
// selection fallback: the client resolved the order's units from its own
// selection when it sent the order (DESIGN_MULTIPLAYER §7.3), so a command
// that names no units has none and does nothing, as an empty stamped actor
// list does (§7.4.3).
func (s *Session) bindLocalCommand(c HumanCommand) boundCommand {
	s.captureLocalRefs(&c)
	b := boundCommand{c: c, issuer: s.LocalOwner, sequence: c.Sequence, target: c.refs.target, targets: c.refs.targets, unit: c.refs.unit}
	switch c.Kind {
	case HumanOrder:
		if len(c.Order.Targets) == 0 {
			b.actors = sortedUniqueRefs(c.refs.actors)
		} else {
			b.actors = c.refs.actors
		}
	case HumanStop, HumanSelfDestruct, HumanCancelQueuedMove, HumanStance, HumanCloak, HumanGroupAssign:
		// The stance and cloak arms broadcast to the captured selection in
		// the ascending order of the former selection scan
		// [04 R-STANCE-01 §5]; assignment takes it as the group's complete
		// membership [07 §9].
		b.actors = c.refs.actors
	}
	return b
}

// boundActors resolves the actor list to the issuer's live units, keeping the
// list's order and any repeat the local adapter carried. Stale and foreign
// references drop out; an online foreign actor was already refused whole.
func (s *Session) boundActors(b *boundCommand) []pool.Handle {
	out := make([]pool.Handle, 0, len(b.actors))
	for _, r := range b.actors {
		if s.commandActor(b.issuer, r) != nil {
			out = append(out, r.Handle)
		}
	}
	return out
}

// boundActorList is boundActors with §7.4.3's outcome: an empty or entirely
// stale list does nothing.
func (s *Session) boundActorList(b *boundCommand) ([]pool.Handle, CommandOutcome) {
	handles := s.boundActors(b)
	if len(handles) == 0 {
		return nil, CommandNoOp
	}
	return handles, CommandApplied
}

// applyBound is the one phase-1 payload implementation. Its bodies are the
// local applier's, with actors and targets resolved through allocation
// references and seat-owned mutations taken from the bound issuer. The
// outcome is for a stamped entry's receipt; the local adapter ignores it.
func (s *Session) applyBound(b *boundCommand, tick uint32) CommandOutcome {
	if s == nil {
		return CommandNoOp
	}
	if b.issuer != s.LocalOwner {
		prevIssuer, prevIssuing := s.seatCommands.issuer, s.seatCommands.issuing
		s.seatCommands.issuer, s.seatCommands.issuing = b.issuer, true
		defer func() { s.seatCommands.issuer, s.seatCommands.issuing = prevIssuer, prevIssuing }()
	}
	if s.applyBoundPlayerCommand(b, tick) {
		return CommandApplied
	}
	if s.Units == nil {
		return CommandNoOp
	}
	c := &b.c
	switch c.Kind {
	case HumanMakeSelectable:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive {
				u.Flags |= units.ClassifierEligibleStatus
			}
		}
	case HumanStop:
		id := orders.Lookup("Stop")
		if id == 0 {
			return CommandApplied
		}
		handles, outcome := s.boundActorList(b)
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
		return outcome
	case HumanActivation:
		u := s.commandActor(b.issuer, b.unit)
		if u == nil {
			return CommandNoOp
		}
		if u.Def == nil || !u.Def.OnOffable {
			return CommandApplied
		}
		name := "Deactivate"
		if c.Activation.Activate {
			name = "Activate"
		}
		id := orders.Lookup(name)
		if id == 0 {
			return CommandApplied
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
		// The selection broadcast of [04 R-STANCE-01 §5]: walk the issuer's
		// units in ascending pool order, submit to every one carrying the
		// selection bit, and skip a unit whose definition lacks the matching
		// accept flag. Neither the leader exclusion nor the centroid arm is
		// active for a standing order — both standing descriptors carry
		// static mask 0x10060, which has no target-required bit, and a
		// standing order carries no ground position. A stamped command names
		// the units it was captured from, in that same order (§7.4.3).
		name := "Standing_MoveOrder"
		if c.Stance.Fire {
			name = "Standing_FireOrder"
		}
		id := orders.Lookup(name)
		if id == 0 {
			return CommandApplied
		}
		handles, outcome := s.boundActorList(b)
		for _, h := range handles {
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
		return outcome
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
			return CommandApplied
		}
		handles, outcome := s.boundActorList(b)
		for _, h := range handles {
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
		return outcome
	case HumanMobileBuild:
		u := s.commandActor(b.issuer, b.unit)
		if u == nil {
			return CommandNoOp
		}
		if s.Catalog == nil {
			return CommandApplied
		}
		def, ok := s.Catalog.Unit(c.MobileBuild.Product)
		if !ok {
			return CommandApplied
		}
		facing := s.Build.ResolveStructureFacing(def, c.MobileBuild.Facing)
		s.bindOrderQueue(u)
		if c.MobileBuild.Queued {
			// A queued click on a point that already carries a queued order of
			// this kind removes that order and issues nothing [07 R-P0-11 §6].
			// The test runs only in queued mode; a non-queued click purges and
			// re-issues as before.
			if !c.MobileBuild.AppendOnly && removeQueuedWorldOrder(u, mobileBuildKind(u), 0, c.MobileBuild.WX, c.MobileBuild.WZ) {
				return CommandApplied
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
			n.GoalY, n.BuildFacing = c.MobileBuild.WY, facing
			q := orders.QueueForUnit(u)
			// Preserve the modern shortcut's existing same-site work without
			// converting it into counted production. Mobile completion consumes
			// the whole site order [04 R-ORD-01 §5].
			if c.MobileBuild.AppendOnly && q.LenPrimary() != 0 {
				tail := q.Primary()[q.LenPrimary()-1]
				if tail.ID == id && tail.BuildDefKey == n.BuildDefKey && tail.GoalX == n.GoalX && tail.GoalZ == n.GoalZ && tail.BuildFacing == n.BuildFacing {
					tail.CreationTick, tail.GoalY = tick, n.GoalY
					return CommandApplied
				}
			}
			q.Push(id, n)
		}
	case HumanFactoryBuild:
		u := s.commandActor(b.issuer, b.unit)
		if u == nil {
			return CommandNoOp
		}
		if s.Catalog == nil {
			return CommandApplied
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
		u := s.commandActor(b.issuer, b.unit)
		if u == nil {
			return CommandNoOp
		}
		s.bindOrderQueue(u)
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			return CommandApplied
		}
		prim := q.Primary()
		tail := prim[len(prim)-1]
		if tail == nil || tail.BuildDefKey == "" {
			return CommandApplied
		}
		if orders.IsMobileBuild(tail.ID) {
			_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
		} else {
			_ = construction.CancelTailMost(u, tail.BuildDefKey)
		}
	case HumanStockpile:
		u := s.commandActor(b.issuer, b.unit)
		if u == nil {
			return CommandNoOp
		}
		s.applyBoundStockpile(u, c.Stockpile.Count, tick)
	case HumanGroupAssign:
		s.applyBoundGroupAssign(b)
	case HumanSelfDestruct:
		// Ctrl+D is a toggle [07 R-CAM-01 §2]. Its name lookup is
		// case-insensitive and exact, so it resolves the rear-segment
		// `SelfDestruct`, never `SelfDestructFG` (the mission `d` token's).
		// Every selected unit already holding one has its first record
		// removed, and that removal says `Self destruct terminated`. Only when
		// no selected unit held one does the press issue the order to the
		// whole selection. The record's handler owns the countdown, the
		// announcements and the 30000 self-damage [04 R-SPEC-01 §13].
		id := orders.Lookup("SelfDestruct")
		if id == 0 {
			return CommandApplied
		}
		handles, outcome := s.boundActorList(b)
		cancelled := false
		for _, h := range handles {
			u := s.humanUnit(h)
			if u == nil {
				continue
			}
			s.bindOrderQueue(u)
			if orders.QueueForUnit(u).CancelFirstOf(id) {
				cancelled = true
			}
		}
		if cancelled {
			return outcome
		}
		for _, h := range handles {
			u := s.humanUnit(h)
			if u == nil {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			// No target and no goal; Shift makes it a queued issue.
			q.Push(id, orders.Node{Owner: u.Handle, CreationTick: tick, QueuedIssue: c.SelfDestruct.Queued})
		}
		return outcome
	case HumanCancelQueuedMove:
		return s.applyBoundCancelQueuedMove(b)
	case HumanCommunityOrderDrag:
		if !b.stamped {
			s.applyCommunityOrderDrag(c.CommunityOrderDrag)
			return CommandApplied
		}
		return s.applyStampedCommunityOrderDrag(b)
	case HumanCommunityKickout:
		if !b.stamped {
			s.applyCommunityKickout(c.CommunityKickout, tick)
			return CommandApplied
		}
		u := s.commandActor(b.issuer, b.unit)
		if u == nil {
			return CommandNoOp
		}
		if s.Build == nil {
			return CommandApplied
		}
		s.bindOrderQueue(u)
		s.Build.KickoutMove(u, c.CommunityKickout.X, c.CommunityKickout.Y, c.CommunityKickout.Z, tick)
	case HumanOrder:
		return s.applyBoundOrder(b, tick)
	}
	return CommandApplied
}

// applyBoundPlayerCommand applies the kinds that act on player records and
// battle-wide state rather than units, and reports whether c was one.
func (s *Session) applyBoundPlayerCommand(b *boundCommand, tick uint32) bool {
	c := &b.c
	switch c.Kind {
	case HumanBuilderOptions:
		if s.validateBuilderOptionsFor(c.BuilderOptions, b.issuer) == nil {
			s.playerBuilderOptions[c.BuilderOptions.Owner] = c.BuilderOptions.Options
		}
	case HumanGameplay:
		s.SetGameplay(c.Gameplay)
	case HumanNoShake:
		s.ToggleNoShake()
	case HumanSpawn:
		s.applySpawnCommand(c.Spawn, b.issuer, tick)
	case HumanDeveloperSpawn:
		if !b.online {
			s.applyDeveloperSpawnCommand(c.DeveloperSpawn)
		}
	case HumanATM:
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			return true
		}
		if s.Econ == nil || int(b.issuer) >= len(s.Econ.Players) {
			return true
		}
		p := &s.Econ.Players[b.issuer]
		if !p.Exists {
			return true
		}
		economy.CreditSpawn(p, economy.Metal, 1000)
		economy.CreditSpawn(p, economy.Energy, 1000)
	case HumanSetResource:
		if s.Econ == nil || c.SetResource.Player < 0 || c.SetResource.Player >= len(s.Econ.Players) {
			return true
		}
		if c.SetResource.Resource != economy.Metal && c.SetResource.Resource != economy.Energy {
			return true
		}
		p := &s.Econ.Players[c.SetResource.Player]
		if !p.Exists || p.ControllerState < 1 || p.ControllerState > 3 || p.Side == 10 {
			return true
		}
		p.Stock[c.SetResource.Resource] = c.SetResource.Amount
	case HumanSetLogo:
		if s.Econ == nil || c.SetLogo.Player < 0 || c.SetLogo.Player >= len(s.Econ.Players) {
			return true
		}
		p := &s.Econ.Players[c.SetLogo.Player]
		if !p.Exists || p.ControllerState < 1 || p.ControllerState > 3 || p.Side == 10 {
			return true
		}
		p.Logo = c.SetLogo.Logo
	case HumanView:
		if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
			s.SetViewingOwner(c.View.Player)
		}
	case HumanGive:
		p := s.playerRecord(c.Give.Player)
		if p == nil || !p.Exists || p.ControllerState < 1 || p.ControllerState > 3 || p.Side == 10 {
			return true
		}
		// The source is the issuer: the stamped seat online, and in
		// single-player the own/controlling slot — LocalOwner at drain time,
		// the slot retail's developer `Control` command moves — never the
		// viewing slot. `View` writes only the viewing slot, so a View earlier
		// in the same input batch changes presentation but not whose stock
		// Give debits [07 R-CAM-01 §6][05 R-SHARE-01 §2].
		s.Econ.Transfer(b.issuer, uint8(c.Give.Player), c.Give.Resource, c.Give.Amount)
	case HumanVisibility:
		if s.Vis == nil {
			return true
		}
		const mask2 = visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled
		if c.Visibility.ToggleMask&mask2 != 0 || c.Visibility.ClearMask&mask2 != 0 {
			if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
				return true
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
	case HumanDoubleShot, HumanHalfShot:
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			return true
		}
		if s.Combat == nil {
			return true
		}
		if c.Kind == HumanDoubleShot {
			s.Combat.ToggleDoubleShot()
		} else {
			s.Combat.ToggleHalfShot()
		}
	case HumanMeteor:
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			return true
		}
		if c.Meteor.ArgumentPresent {
			s.Meteor.Enabled = c.Meteor.Enabled
			return true
		}
		// The command-only form enters the same storm-arm body as a due
		// schedule, but deliberately bypasses the enabled-bit test [07
		// R-CAM-01 §6][06 §6.5].
		s.armMeteor(tick)
	default:
		return false
	}
	return true
}

// applyBoundStockpile is one MAKENUKE/MAKEANTI click on a resolved launcher.
func (s *Session) applyBoundStockpile(u *units.Unit, count int, tick uint32) {
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
	s.queueStockpileRounds(u, count, tick)
}

// applyBoundOrder is the world order. An explicit target that has died keeps
// the single-player result for the local adapter and the replay context: the
// order resolves as a ground order at the captured position and its node
// still carries the dead handle. Online, a stale ordinary target makes the
// whole order a no-op and never a ground click (§7.4.3).
func (s *Session) applyBoundOrder(b *boundCommand, tick uint32) CommandOutcome {
	c := &b.c
	if len(c.Order.Targets) != 0 {
		return s.applyBoundOrderBatch(b, tick)
	}
	targetHandle := b.target.Handle
	var target *units.Unit
	if targetHandle != 0 {
		target = s.commandTarget(b.target)
		if target == nil && b.online {
			return CommandNoOp
		}
	}
	handles, outcome := s.boundActorList(b)
	if outcome != CommandApplied {
		return outcome
	}
	var excluded pool.Handle
	if !c.Order.AssignedPosition && c.Order.Code != 5 && c.Order.Code != 10 && c.Order.Code != 14 && target != nil {
		excluded = target.Handle // numeric broadcast target exclusion [04 R-STANCE-01 §5]
	}
	var center orders.ResolvePos
	var count int32
	if !c.Order.AssignedPosition {
		center, count = s.humanOrderCentroid(handles, excluded)
		if c.Order.StagedCount > count && count != 0 {
			count = c.Order.StagedCount
		}
	}
	slots := s.groupDestinationSlots(c.Order, handles, excluded, target, center, count)
	// The units this command gives a ground move, for the traffic
	// policy's arrival places (movement.Pilot); nothing reads it under
	// Strict 3.1 or Community 3.9.
	var moved []pool.Handle
	var movedOwner uint8
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
		// The hovered target and cursor-ground triple are independent inputs;
		// the constructor keeps the supplied point [04 R-ORD-01 §13].
		gx, gy, gz := c.Order.Position.X, c.Order.Position.Y, c.Order.Position.Z
		if !c.Order.AssignedPosition && orders.DescriptorFor(id).StaticGate&2 != 0 && count != 0 {
			goal := humanFormationGoal(c.Order.Position, u, center, count)
			gx, gy, gz = goal.X, goal.Y, goal.Z
			if d, ok := slots.lookup(h); ok {
				gx, gz = d.X, d.Z
			}
		}
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		trackedMove := c.Order.TrackQueuedMove && c.Order.Queued && targetHandle == 0 && isHumanMoveOrder(id)
		if c.Order.Queued {
			// The producer's first act, once per acting unit: a queued
			// click that repeats an already-queued order of this kind at
			// (or within one cell of) the same point removes it and issues
			// nothing [07 R-P0-11 §6]. It runs ONLY in queued mode; a plain
			// click falls through to the Replace below without testing.
			if !trackedMove && removeQueuedWorldOrder(u, id, targetHandle, gx, gz) {
				continue
			}
		} else {
			q.PurgeUnprotected()
			q.DropLeadingAutoOps()
		}
		n := orders.NewNodeForOrder(id, targetHandle, gx, gy, gz, tick, u.Handle, c.Order.Queued)
		if trackedMove {
			// The tracked-move receipt: a stamped entry's stream position,
			// so every client stamps the same sequence (§7.2).
			n.HumanMoveSequence = b.sequence
		}
		q.Push(id, n)
		if target == nil && !c.Order.Queued && id == orders.Lookup("Move_Ground") {
			moved, movedOwner = append(moved, h), u.Owner
		}
	}
	if len(moved) > 1 && s.Movement != nil {
		s.Movement.NoteGroupOrder(movedOwner, moved, c.Order.Position.X, c.Order.Position.Z, tick)
	}
	return CommandApplied
}

// applyBoundOrderBatch preserves the captured actor and target order.
// The area gesture is an explicit extension (DESIGN_INTERFACE_HUD_INPUT §3.11):
// the first admitted target replaces once unless queued, then all others append
// without the ordinary repeat-click toggle [07 R-P0-11 §6]. A stale explicit
// target drops only its own entry and never becomes a ground order; targetless
// feature entries keep their place (§7.4.3).
func (s *Session) applyBoundOrderBatch(b *boundCommand, tick uint32) CommandOutcome {
	c := &b.c
	handles, outcome := s.boundActorList(b)
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
		queued := c.Order.Queued
		for i, goal := range c.Order.Targets {
			var target *units.Unit
			if goal.Target != 0 {
				target = s.commandTarget(b.targets[i])
				if target == nil {
					// A vanished captured unit must not turn into a ground
					// order. Existing targets use [04 R-ORD-02 §1]'s gates.
					continue
				}
			}
			id := orders.Resolve(c.Order.Code, u, target, &goal.Position)
			if id == 0 {
				continue
			}
			gx, gy, gz := goal.Position.X, goal.Position.Y, goal.Position.Z
			if !queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, goal.Target, gx, gy, gz, tick, u.Handle, queued))
			queued = true
		}
	}
	return outcome
}

// applyBoundCancelQueuedMove removes the tracked queued moves the named
// receipt stamped into the actors' queues (DESIGN_INTERFACE_HUD_INPUT §3.10).
func (s *Session) applyBoundCancelQueuedMove(b *boundCommand) CommandOutcome {
	sequence := b.c.CancelQueuedMove.Sequence
	if sequence == 0 {
		return CommandApplied
	}
	handles, outcome := s.boundActorList(b)
	for _, h := range handles {
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
				if n != nil && n.HumanMoveSequence == sequence && isHumanMoveOrder(n.ID) {
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
	return outcome
}

// applyStampedCommunityOrderDrag is the stamped form of the Community queue
// drag: the unit is named by allocation reference, and the receipt's every
// field must still match the queued record (§7.4.3). A changed receipt does
// nothing.
func (s *Session) applyStampedCommunityOrderDrag(b *boundCommand) CommandOutcome {
	u := s.commandActor(b.issuer, b.unit)
	if u == nil {
		return CommandNoOp
	}
	q := orders.QueueOfUnit(u)
	if q == nil {
		return CommandNoOp
	}
	ok := orders.DragCommunityOrder(q, b.c.CommunityOrderDrag.Receipt, b.c.CommunityOrderDrag.Position, func(n *orders.Node, raw orders.CommunityOrderDragDestination) (orders.CommunityOrderDragDestination, bool) {
		if !orders.IsMobileBuild(n.ID) {
			return raw, true
		}
		return s.communityDraggedBuildPosition(u, n, raw)
	})
	if !ok {
		return CommandNoOp
	}
	return CommandApplied
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
