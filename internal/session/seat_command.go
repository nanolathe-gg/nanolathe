package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The explicit seat-command value of docs/DESIGN_MULTIPLAYER.md §7.4.4. These
// are Nanolathe protocol types, not retail records: the kind numbers, field
// widths and bounds are the §7.4.1–§7.4.2 tables, and the session applies a
// stamped value at phase 1 through the same payload implementation the local
// adapter (EnqueueHumanCommand) reaches. The wire codec is
// seat_command_codec.go; nothing here encodes or decodes bytes.

// CommandContext is the admitted session's decoding and authorization
// context. The session's admitted kind chooses it; no caller and no payload
// can (§7.4.1, §7.4.4).
type CommandContext uint8

// The two contexts of command schema version 1. The numbers are protocol
// constants.
const (
	OnlineCommand      CommandContext = 1
	SinglePlayerReplay CommandContext = 2
)

// SeatCommandKind is a command's kind byte. Every number is written out
// rather than derived from iota or Go layout, because the numbers are a wire
// schema (§7.4.2). Numbers 1..34, 46 and 255 match HumanCommandKind for
// recognition; a test locks that, nothing derives one from the other.
type SeatCommandKind uint8

// The kind numbers of §7.4.2. Every unlisted number is invalid.
const (
	SeatSelectionReplace   SeatCommandKind = 1  // L
	SeatSelectionToggle    SeatCommandKind = 2  // L
	SeatSelectionClear     SeatCommandKind = 3  // L
	SeatOrder              SeatCommandKind = 4  // S
	SeatStop               SeatCommandKind = 5  // S
	SeatActivation         SeatCommandKind = 6  // S
	SeatMobileBuild        SeatCommandKind = 7  // D: known-site admission needs M5
	SeatFactoryBuild       SeatCommandKind = 8  // S
	SeatCancelProduction   SeatCommandKind = 9  // S
	SeatStockpile          SeatCommandKind = 10 // S
	SeatBuildPage          SeatCommandKind = 11 // L
	SeatGroupAssign        SeatCommandKind = 12 // S
	SeatGroupRecall        SeatCommandKind = 13 // L
	SeatStance             SeatCommandKind = 14 // S
	SeatCloak              SeatCommandKind = 15 // S
	SeatSelfDestruct       SeatCommandKind = 16 // S
	SeatNoShake            SeatCommandKind = 17 // R
	SeatATM                SeatCommandKind = 18 // S, cheat
	SeatSetResource        SeatCommandKind = 19 // S, cheat
	SeatSetLogo            SeatCommandKind = 20 // R
	SeatView               SeatCommandKind = 21 // D, cheat
	SeatGive               SeatCommandKind = 22 // S; cheat for any amount but a positive whole one
	SeatMakeSelectable     SeatCommandKind = 23 // S, cheat
	SeatVisibility         SeatCommandKind = 24 // D, cheat
	SeatDoubleShot         SeatCommandKind = 25 // D, cheat
	SeatHalfShot           SeatCommandKind = 26 // D, cheat
	SeatMeteor             SeatCommandKind = 27 // S, cheat
	SeatBigBrother         SeatCommandKind = 28 // L
	SeatShiftState         SeatCommandKind = 29 // L
	SeatCancelQueuedMove   SeatCommandKind = 30 // S
	SeatSpawn              SeatCommandKind = 31 // S, cheat
	SeatBuilderOptions     SeatCommandKind = 32 // S
	SeatCommunityOrderDrag SeatCommandKind = 33 // D: known-site admission needs M5
	SeatCommunityKickout   SeatCommandKind = 34 // S
	SeatShareMetal         SeatCommandKind = 35 // D, reserved: no v1 payload
	SeatShareEnergy        SeatCommandKind = 36 // D, reserved
	SeatShareMapping       SeatCommandKind = 37 // D, reserved
	SeatShareRadar         SeatCommandKind = 38 // D, reserved
	SeatShareAll           SeatCommandKind = 39 // D, reserved
	SeatSetShareMetal      SeatCommandKind = 40 // D, reserved
	SeatSetShareEnergy     SeatCommandKind = 41 // D, reserved
	SeatShareGift          SeatCommandKind = 42 // D, reserved
	SeatDeclareAlliance    SeatCommandKind = 43 // D, reserved
	SeatSharedVictory      SeatCommandKind = 44 // D, reserved
	SeatShootAll           SeatCommandKind = 45 // D, cheat, reserved
	SeatDeveloperSpawn     SeatCommandKind = 46 // R: authorized local developer submission
	SeatGameplay           SeatCommandKind = 255
)

// CommandPosition is a captured world click: the point, the Interface Type
// polarity that selects the contextual resolution branch (0 left, 1 right)
// and whether the click named a feature (§7.4.1 `position`). HasFeature is
// captured intent, not a claim that the feature exists or is visible.
type CommandPosition struct {
	X, Y, Z       numeric.Fixed
	InterfaceType uint8
	HasFeature    bool
}

// CommandPoint is a plain world point (§7.4.1 `point`).
type CommandPoint struct {
	X, Y, Z numeric.Fixed
}

// CommandTarget is one area-list entry: a nullable unit target, which may
// belong to any player, and its captured position (§7.4.1).
type CommandTarget struct {
	Target   pool.UnitRef
	Position CommandPosition
}

// OrderPayload is kind 4. Ordinary-order actors are a set in ascending handle
// order; an area order keeps its actors' and its entries' captured order
// (§7.4.3).
type OrderPayload struct {
	Actors           []pool.UnitRef
	Code             uint8
	Target           pool.UnitRef
	Position         CommandPosition
	Queued           bool
	AssignedPosition bool
	TrackQueuedMove  bool
	Targets          []CommandTarget
}

// StopPayload is kind 5.
type StopPayload struct {
	Actors []pool.UnitRef
}

// ActivationPayload is kind 6.
type ActivationPayload struct {
	Unit     pool.UnitRef
	Activate bool
	Queued   bool
}

// MobileBuildPayload is kind 7. Position.Y is the site height the sender
// computed; an online receiver must derive and check it (§7.4.3), which is
// M5's known-site work.
type MobileBuildPayload struct {
	Builder    pool.UnitRef
	Product    string
	Position   CommandPoint
	Facing     uint8
	Queued     bool
	AppendOnly bool
}

// FactoryBuildPayload is kind 8.
type FactoryBuildPayload struct {
	Builder pool.UnitRef
	Product string
	Count   int32
}

// CancelProductionPayload is kind 9.
type CancelProductionPayload struct {
	Unit pool.UnitRef
}

// StockpilePayload is kind 10.
type StockpilePayload struct {
	Unit  pool.UnitRef
	Count int32
}

// GroupAssignPayload is kind 12: the group's complete new membership among
// the issuer's units, the empty set included (§7.1, §7.4.3).
type GroupAssignPayload struct {
	Group   uint8
	Members []pool.UnitRef
}

// StancePayload is kind 14.
type StancePayload struct {
	Actors []pool.UnitRef
	Fire   bool
	Value  uint8
}

// CloakPayload is kind 15.
type CloakPayload struct {
	Actors []pool.UnitRef
	Cloak  bool
}

// SelfDestructPayload is kind 16.
type SelfDestructPayload struct {
	Actors []pool.UnitRef
	Queued bool
}

// SetResourcePayload is kind 19. Player is the single-player replay field;
// online it must be zero and the issuing seat's own stock is written.
type SetResourcePayload struct {
	Player   uint8
	Resource economy.Res
	Amount   float32
}

// SetLogoPayload is kind 20, a single-player replay record.
type SetLogoPayload struct {
	Player uint8
	Logo   uint8
}

// ViewPayload is kind 21.
type ViewPayload struct {
	Player uint8
}

// GivePayload is kind 22. Player is the recipient; the source is the issuing
// seat online and the own/controlling slot at drain time in single-player
// [07 R-CAM-01 §6].
type GivePayload struct {
	Player   uint8
	Resource economy.Res
	Amount   float32
}

// VisibilityPayload is kind 24.
type VisibilityPayload struct {
	ToggleMask uint8
	ClearMask  uint8
}

// MeteorPayload is kind 27.
type MeteorPayload struct {
	ArgumentPresent bool
	Enabled         bool
}

// CancelQueuedMovePayload is kind 30. Sequence is the stream position the
// tracked move's receipt reported, never an unacknowledged local sequence
// (§7.4.4).
type CancelQueuedMovePayload struct {
	Sequence uint64
	Actors   []pool.UnitRef
}

// SpawnPayload is kind 31.
type SpawnPayload struct {
	Unit     string
	Position CommandPoint
}

// BuilderOptionsPayload is kind 32. Owner is the single-player replay field;
// online it must be zero and the issuing seat's options change.
type BuilderOptionsPayload struct {
	Owner  uint8
	Guard  [3]uint8
	Patrol [3]uint8
}

// CommunityOrderDragPayload is kind 33: the committed queue receipt, with an
// allocation reference in place of the publication identity, and the new
// destination.
type CommunityOrderDragPayload struct {
	Unit         pool.UnitRef
	Index        uint16
	DescriptorID int32
	CreationTick uint32
	Target       pool.UnitRef
	Goal         CommandPoint
	BuildProduct string
	BuildFacing  uint8
	Destination  CommandPoint
}

// CommunityKickoutPayload is kind 34.
type CommunityKickoutPayload struct {
	Unit        pool.UnitRef
	Destination CommandPoint
}

// DeveloperSpawnPayload is kind 46, a single-player replay record. Pattern
// is a canonical key with '*'/'?' interpreted by the retail default handler.
// Owner is the typed integer's low byte, including values the allocator refuses.
type DeveloperSpawnPayload struct {
	Pattern  string
	Owner    uint8
	Position CommandPoint
}

// GameplayPayload is kind 255, a single-player replay record; online it is
// lobby-only.
type GameplayPayload struct {
	Mode gameplay.Mode
}

// SeatCommand is the tagged seat-command value. Kind selects one payload
// record; every other record must be zero, so no field can be silently
// discarded (§7.4.4). Fieldless kinds have no record.
type SeatCommand struct {
	Kind               SeatCommandKind
	Order              OrderPayload
	Stop               StopPayload
	Activation         ActivationPayload
	MobileBuild        MobileBuildPayload
	FactoryBuild       FactoryBuildPayload
	CancelProduction   CancelProductionPayload
	Stockpile          StockpilePayload
	GroupAssign        GroupAssignPayload
	Stance             StancePayload
	Cloak              CloakPayload
	SelfDestruct       SelfDestructPayload
	SetResource        SetResourcePayload
	SetLogo            SetLogoPayload
	View               ViewPayload
	Give               GivePayload
	Visibility         VisibilityPayload
	Meteor             MeteorPayload
	CancelQueuedMove   CancelQueuedMovePayload
	Spawn              SpawnPayload
	DeveloperSpawn     DeveloperSpawnPayload
	BuilderOptions     BuilderOptionsPayload
	CommunityOrderDrag CommunityOrderDragPayload
	CommunityKickout   CommunityKickoutPayload
	Gameplay           GameplayPayload
}

// clone deep-copies the variable payload storage, so a caller may reuse its
// slices after EnqueueSeatCommand returns.
func (c SeatCommand) clone() SeatCommand {
	c.Order.Actors = cloneRefs(c.Order.Actors)
	if c.Order.Targets != nil {
		c.Order.Targets = append([]CommandTarget(nil), c.Order.Targets...)
	}
	c.Stop.Actors = cloneRefs(c.Stop.Actors)
	c.GroupAssign.Members = cloneRefs(c.GroupAssign.Members)
	c.Stance.Actors = cloneRefs(c.Stance.Actors)
	c.Cloak.Actors = cloneRefs(c.Cloak.Actors)
	c.SelfDestruct.Actors = cloneRefs(c.SelfDestruct.Actors)
	c.CancelQueuedMove.Actors = cloneRefs(c.CancelQueuedMove.Actors)
	return c
}

func cloneRefs(in []pool.UnitRef) []pool.UnitRef {
	if in == nil {
		return nil
	}
	return append([]pool.UnitRef(nil), in...)
}

// CommandStamp is the stream metadata the stream driver supplies beside a
// payload: the issuing seat, the tick the entry is bound to and its stream
// position (§4.2, §7.4.4). A payload never carries it.
type CommandStamp struct {
	Seat     uint8
	Tick     uint32
	Position uint64
}

// CommandOutcome is what phase 1 did with an admitted entry.
type CommandOutcome uint8

// The receipt outcomes of §7.4.3. A receipt does not claim that every actor
// achieved the requested order.
const (
	// CommandApplied: the entry was authorized and dispatched to the owning
	// gameplay services, which kept their own partial-work semantics.
	CommandApplied CommandOutcome = 1
	// CommandNoOp: the entry was authorized but its actors or explicit target
	// had gone stale, or its queue receipt had changed, so nothing was done.
	CommandNoOp CommandOutcome = 2
	// CommandRejected: the entry failed schema, role, permission, gameplay
	// availability or actor authorization before any mutation.
	CommandRejected CommandOutcome = 3
)

// CommandReceipt reports one stream entry's outcome. Diagnostic names the
// rejection, in the project's diagnostic shape, and is empty otherwise; it
// is presentation data, drained outside the tick, and is not part of the
// §7.4.4 contract's identity.
type CommandReceipt struct {
	Stamp      CommandStamp
	Outcome    CommandOutcome
	Diagnostic string
}

// seatQueued is one stamped entry riding the session's single input queue
// (pendingHuman), so stamped and local entries share one order and one drain.
// It is immutable once queued.
type seatQueued struct {
	stamp   CommandStamp
	command SeatCommand
	context CommandContext
}

// seatCommandState is the session's seat-command boundary state
// (DESIGN_MULTIPLAYER §7.4.4). lastPosition and receipts are guarded by
// Session.humanMu, because the host enqueues and drains outside the tick.
// online is written once, before the battle's first tick, and only read
// after. removed, issuer and issuing are phase-1 state on the simulation
// goroutine.
type seatCommandState struct {
	// online holds the admitted configuration's command-relevant fields when
	// the session runs the online context; nil is a single-player session,
	// whose context is SinglePlayerReplay.
	online *onlineCommandConfig
	// lastPosition is the highest stream position accepted so far, across
	// every seat. The stream is one append-only sequence (§4.2), so a later
	// entry of any seat must carry a higher position; gaps are legal.
	lastPosition uint64
	// receipts are phase-1 outcomes awaiting DrainCommandReceipts.
	receipts []CommandReceipt
	// removed marks a finally removed seat (§11.1). The relay-authored event
	// that sets it is M6's; until then only the unexported setter does.
	removed [10]bool
	// issuer is the seat the command being applied acts for; issuing is set
	// only while phase 1 applies one. Session.humanUnit admits units of this
	// seat — the entry's seat, never a viewer (§7.2).
	issuer  uint8
	issuing bool
	// replay is the single-player replay recorder that observes this boundary
	// and the presentation perspective of a playback (replay.go).
	replay replayState
}

// onlineCommandConfig is the part of an admitted EffectiveMatchConfig the
// receiver authorizes against (§8.6 fields 4, 6 and 13).
type onlineCommandConfig struct {
	roles         []MatchRole
	unitLimit     uint16
	cheatsAllowed bool
}
