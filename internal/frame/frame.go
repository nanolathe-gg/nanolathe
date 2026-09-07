package frame

import (
	"errors"
	"sync/atomic"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

var (
	// ErrPublishWithoutWrite means BeginWrite was not called for this slot.
	ErrPublishWithoutWrite = errors.New("frame: publish without BeginWrite")
	// ErrNonMonotonicTick rejects duplicate and out-of-order publication.
	ErrNonMonotonicTick = errors.New("frame: non-monotonic tick publication")
)

// Capacities controls the top-level preallocation performed by NewBuffer.
// Zero means that the corresponding slice starts empty and grows only if the
// caller appends to it.  These are storage choices, not simulation limits;
// fixed pool limits remain owned by their simulation pools [01 §6.1].
type Capacities struct {
	Units           int
	Projectiles     int
	Features        int
	Effects         int
	OrderQueues     int
	Builds          int
	Cues            int
	Selection       int
	CommandProducts int
	Visibility      int
	RadarContacts   int
	Fog             int
}

// PieceView carries one committed COB piece transform [03 §2.4].
type PieceView struct {
	Index            int
	Name             string
	RotX, RotY, RotZ uint16
	Tx, Ty, Tz       numeric.Fixed
	DontShade        bool
	Hidden           bool
	DontShadow       bool
}

// UnitView is the committed presentation copy of one live unit.  InstanceID
// remains for the current client identity adapter; it is not a pool
// generation.  The eventual publisher may omit it when all consumers use
// pool slots directly.
type UnitView struct {
	InstanceID           uint64
	Slot                 pool.Handle
	DefID                uint16
	Owner                uint8
	X, Y, Z              numeric.Fixed
	Heading, Pitch, Bank uint16
	Health, MaxHealth    int32
	BuildRemaining       float32
	Flags                uint32
	DefName              string
	Model                string
	FootX, FootZ         int8
	Pieces               []PieceView
	BMCode               bool // authored model-shading class gate [R-RND-02A]
	// ZBuffer is the authored FBI key that gives the unit's composition image
	// a per-pixel height plane. 276 of the 278 stock units author it
	// [R-REN-03A §2].
	ZBuffer bool
	// NoShadow, CanHover and Floater are the three authored keys the model
	// shadow gate reads [R-REN-03D §1].
	NoShadow bool
	CanHover bool
	Floater  bool
	// Digger raises the height key by 75 and clips the buried half of the
	// model away [R-REN-03A §8].
	Digger     bool
	IsBuilding bool
	// Activated is the committed on/off state used by UI command dispatch.
	// Presentation must not rehydrate a selected unit from the live pool [I6].
	Activated bool
	// Cloaked and Decloaking are step 2 of the visibility gate [03 §3.2],
	// published so presentation can evaluate it without reconstructing cloak
	// state from the instance flag word.  Cloaked is the INSTANCE cloak bit
	// [R-VIS-01 §4]; Decloaking is runtime status bit 12, the decloak timer of
	// [03 §3.4].  A cloaked unit is hidden from a non-owner unless the timer is
	// running.
	//
	// Definition `stealth` is deliberately NOT folded in: it is the contact
	// callback's third reject, so it suppresses radar and sonar detection but
	// never line of sight [R-VIS-01 §5].
	Cloaked    bool
	Decloaking bool
	// Kills is the credited-kill counter the footer's kills line reads
	// [07 R-HUD-03 §2].
	Kills int32
	// MoverMode is the committed low two bits of the unit record's flags-word
	// mode mirror: 1 is on the ground or on the surface — which includes every
	// structure, nanoframe or complete — 2 is airborne, 0 is attached to a
	// carrier or parked on a pad, and 3 reaches a unit only through a save file
	// [04 R-MOV-01 §8].  It is the unit painter's pass selector: pass A draws
	// the mirror-1 units interleaved with that row's tall features, pass B
	// draws everything else after the projectile and effect strips
	// [03 R-RAST-01 §7].  Structures are pass A; the earlier reading that put
	// them in pass B read this word as the mover object's mode rather than the
	// record's own mirror and was retracted on 2026-08-30.
	MoverMode uint8
	// Carrier is the slot of the unit this one is attached to, or 0 when it is
	// not carried, and CarriedPiece is the carrier piece it hangs from with a
	// negative value for the piece-less carry of [04 R-UNIT-06 §3].  The unit
	// painter needs the link because retail's per-unit present runs "for the
	// unit and then each attached child that is not carried piece-less"
	// [03 R-RAST-01 §7]: a factory's nanoframe is painted with the factory, not
	// only as its own entry in its own Z row.
	Carrier      pool.Handle
	CarriedPiece int16
	// Group is the unit's one stored control-group value [07 §9].  The
	// health-bar pass draws the digit '0'+Group beside the bar of a unit whose
	// group number is nonzero [03 R-FX-01 §6].
	Group uint8
	// OwnerColor is the owning player's lobby colour index: the frame selector
	// for the owner logo the footer blits at LOGO2 [07 R-HUD-03 §2].  It is
	// carried per unit because the committed frame holds no player roster.
	// OwnerColorKnown is false for a slot with no lobby record.
	OwnerColor      uint8
	OwnerColorKnown bool
	// The four archived economy slots of [05 R-ECO-01 §5]: the production and
	// requested totals of the most recent settlement pass, rewritten there
	// every pass while the live buckets are cleared.  They are the only source
	// the footer's four rate fields read [07 R-HUD-03 §2] — not the definition
	// constants, and with no smoothing or other cadence.
	ArchivedMetalMake  float32
	ArchivedEnergyMake float32
	ArchivedMetalUse   float32
	ArchivedEnergyUse  float32
	// HullXExtent, HullYExtent and HullZExtent are the compiled definition's
	// three extent words in 16.16 world units — the sample offsets of the
	// gameplay visibility gate's four-point hull [03 §3.2] step 5.  They are
	// three separate definition words: the height decrement is neither the Z
	// extent nor half the unit's height.  Their writers are traced in
	// [07 R-REV-01 §7] — the unit-record compiler writes the horizontal pair
	// from the authored footprint keys and the catalog loader rewrites the
	// vertical one as the model's total height once the 3DO is loaded.
	//
	// They are published because presentation may not read a live definition
	// [I6], and because the gate is not reproducible from FootX/FootZ alone:
	// the vertical word comes from the model, not from the FBI record.
	HullXExtent numeric.Fixed
	HullYExtent numeric.Fixed
	HullZExtent numeric.Fixed
	// UnderwaterExempt is the runtime status bit that exempts a unit from the
	// gate's below-sea-level rejection [03 §3.2] step 3.  The sensor phase
	// sets it on owned and allied units [03 §3.4], which is why those are
	// never rejected for depth.  It is published for the same reason Cloaked
	// is: presentation must not reconstruct sensor state from the instance
	// flag word [R-VIS-01 §4].
	UnderwaterExempt bool
}

// ProjectileView is the committed copy of one projectile draw record
// [06 §5.1].  Projectile allocation and lifetime authority remains in the
// simulation pool; this value is only a read-only draw input.
type ProjectileView struct {
	PresentationID            uint64
	Handle                    pool.Handle
	Owner                     uint8
	OwnerKnown                bool
	X, Y, Z                   numeric.Fixed
	WeaponID                  int32
	Shooter                   pool.Handle
	Model                     string
	Yaw                       uint16 // retail's yaw word, (-sin a, -cos a) names the direction [06 R-WPN-05 §11]
	Pitch                     uint16
	Flags                     uint32
	Family                    int32
	RenderType                int32
	Selector                  int32
	StartX, StartY, StartZ    numeric.Fixed
	TailX, TailY, TailZ       numeric.Fixed
	VX, VY, VZ                numeric.Fixed
	CreationTick              uint32
	ExpiryTick                uint32
	Lifetime                  int32
	BurstRemaining            int32
	MuzzlePiece               int32
	Target                    pool.Handle
	TargetX, TargetY, TargetZ numeric.Fixed
	Graphic                   string
	SmokeTrail                bool
	TrailFrame                int32
	PaletteRow                int16
	AssetID                   string
	BaseAssetID               string
	SelectorSequence          int32
	FrameCount                int32
	PrimaryColor              uint8
	SecondaryColor            uint8
	HasPrimaryColor           bool
	HasSecondaryColor         bool
	OrientationLow            uint16
	OrientationHigh           uint16
	HasDirectOrientation      bool
	SecondaryModel            string
	SecondaryModelUntil       uint32
	// FloorHeight is the record's cached average floor height in whole world
	// units: over the plot cell of the record's post-motion point,
	// `(cell.maxHeight + cell.minHeight) / 2` as an unsigned division of two
	// height bytes [06 §8.1] step 2.  The collision gate writes it on every
	// in-map tick and no gameplay test reads it — the projectile draw pass is
	// its only consumer, and the shared ground `shadow` sprite of render types
	// 1, 3, 4 and 6 is anchored against half of it rather than against the
	// projectile's own Y [03 §5.4].
	//
	// FloorHeightValid is false when the point resolved to no plot cell, which
	// is the off-map case the gate retires without sampling terrain.
	FloorHeight      int16
	FloorHeightValid bool
}

// FeatureView is the committed copy of one live feature [05 "Feature
// instance and terrain cell"].
type FeatureView struct {
	InstanceID uint64
	// Owner is the plot's placer selector. Map-authored features use the
	// non-player selector 10; corpse/runtime features carry their owner's
	// player slot [03 §3.3][03 §3.9].
	Owner      uint8
	OwnerKnown bool
	CX, CZ     int32
	X, Y, Z    numeric.Fixed
	// Bank, Heading and Pitch are the live record's orientation triple, in the
	// unit record's own order and units (65536 per circle) [05 "Feature
	// instance and terrain cell"]. Retail draws a 3DO feature as a pseudo-unit
	// filled with the model pointer, the position and the slot's orientation
	// words [03 R-RAST-01 §6], so the feature model pass needs them exactly as
	// the unit pass needs a unit's. They are zero for every placement but a
	// corpse.
	Bank         uint16
	Heading      uint16
	Pitch        uint16
	DefName      string
	Model        string
	Health       int32
	MaxHealth    int32
	Status       uint32
	IsBurning    bool
	IsSinking    bool
	BurnTicks    int32
	FootX, FootZ int8
	Filename     string
	SeqName      string
	SeqNameShad  string
	// EventSeqName and EventSeqNameShad are the sequence the instance's OWN
	// cursor is running — the burn, death or reclaim animation — and its
	// shadow twin. They are empty for every feature at rest. A cell with a
	// live instance blits the instance's cursor frames; a cell with none blits
	// the definition's rest cursor `seqname`/`seqnameshad`
	// [03 R-RAST-01 §6][05 R-FEAT-01 §10].
	EventSeqName     string
	EventSeqNameShad string
	// EventSeqVisit is that cursor's visit count: frame i of the entry holds
	// for max(delay, 1) visits [05 R-FEAT-01 §10].
	EventSeqVisit      int32
	Animating          bool
	AnimationStartTick uint32
	AnimTrans          bool
	ShadTrans          bool
	Blocking           bool
	Reclaimable        bool
	// NoDrawUnderGray is the authored nodrawundergray gate. It is copied into
	// the committed frame so the feature passes can apply the memory/LOS
	// predicate without consulting the mutable catalog [02 "Feature record"]
	// [03 §5.1.5].
	NoDrawUnderGray bool
	Height          int32
	Geothermal      bool
}

// SFXClass is the typed COB sound/effect class carried by a cue.
type SFXClass uint8

const (
	SFXVector SFXClass = iota + 1
	SFXWhiteSmoke
	SFXBlackSmoke
	SFXSubBubbles
)

// EffectView is a committed view of an active fixed effect or strip object
// [03 §1].  Effect lifecycle remains presentation-owned, but the renderer
// consumes the tick-end result through this immutable value.
type EffectView struct {
	PresentationID uint64
	ID             uint32
	EventSeq       uint64
	Source         pool.Handle
	Target         pool.Handle
	EffectID       uint32
	Piece          int32
	SFXType        int32
	SFXClass       SFXClass
	Mode           uint8
	StartTick      uint32
	ExpiryTick     uint32
	Lifetime       int32
	X, Y, Z        numeric.Fixed
	TargetX        numeric.Fixed
	TargetY        numeric.Fixed
	TargetZ        numeric.Fixed
	VX, VY, VZ     numeric.Fixed
	Gravity        numeric.Fixed
	Kind           string
	HasModel       bool
	SeqA           int32
	SeqB           int32
	// ActiveA and ActiveB are the two embedded animation players' published
	// liveness: whether this record's PRIMARY (named art) and SECONDARY
	// (calculated flash) layer is still to be drawn [03 §1].
	//
	// Rendering walks the pool once per animation category, and a category
	// whose sequence pointer was cleared at termination draws nothing for that
	// record [03 §1]. The record outlives whichever player finishes first — it
	// is retired only when both are inactive — so the draw pass must not infer
	// liveness from the durations, from the art name, or from the cursor
	// index: frame 0 is a valid live frame and is exactly the index a
	// terminated player leaves behind [03 §4.4]. The fixed effect pool is the
	// sole producer of these views and publishes the fact instead.
	ActiveA bool
	ActiveB bool
	// HasCalculatedFlash and CalculatedTable carry the explosion pool's
	// SECONDARY cursor over a procedurally generated disc [06 R-WFX-01 §2].
	// SeqB is that cursor's frame; the table index selects which of the three
	// generated tables it indexes. The flag is separate so a zero value cannot
	// read as table 0.
	HasCalculatedFlash bool
	CalculatedTable    uint8
	// StripFill is a mirrored strip sub-record's fill colour. The per-sub-record
	// draw of [03 R-STRIP-01 §2] blits "either a GAF frame or a two-by-two
	// filled rectangle"; a view with a Graphic takes the first form and one with
	// a nonzero StripFill takes the second. Zero is never one of the authored
	// fill colours — the nano ramp 0xa1..0xa7 and the sprinkle pair 0x61/0x67 —
	// so zero unambiguously means "not a fill".
	StripFill               uint8
	Graphic                 string
	PaletteRow              int16
	Light                   bool
	Shake                   int32
	AssetID                 string
	SequenceID              string
	DurationsA              []int32
	DurationsB              []int32
	LoopA                   bool
	LoopB                   bool
	FlashRadius             int32
	FlashLevel              int32
	HasFlashDisc            bool
	Strip                   int8
	NanolatheIndex          int32
	NanolatheCount          int32
	NanolatheGeometryKnown  bool
	NanolatheTargetBoxKnown bool
	NanolatheTargetMin      [3]numeric.Fixed
	NanolatheTargetMax      [3]numeric.Fixed
	// NanolatheBoxAtSource: the box above sits at the SOURCE end of the
	// segment and the published target point is the destination — the
	// reversed direction of unit reclaim, capture and feature reclaim
	// [05 R-WORK-01 §8].
	NanolatheBoxAtSource bool
}

// RoutePoint is one fixed-point point in an order's committed route.
type RoutePoint struct {
	X, Y, Z numeric.Fixed
	Flags   uint8
}

// OrderView is one committed order node used by selection and queue overlays
// [04 §3].
type OrderView struct {
	Unit                   pool.Handle
	Target                 pool.Handle
	GoalX, GoalY, GoalZ    numeric.Fixed
	Kind                   string
	State                  uint8
	StateLabel             string
	MoveState              uint8
	List                   uint8
	Index                  uint16
	DescriptorID           int32
	Phase                  uint8
	CreationTick           uint32
	Flags                  uint32
	DynamicGate            uint32
	Deadline               int32
	Satisfied              uint32
	PathStatus             uint32
	Param1, Param2, Param3 uint32
	BuildProduct           string
	BuildCount             uint32
	FootX, FootZ           int8
	Route                  []RoutePoint
	RouteTruncated         bool
}

// OrderQueueView carries primary and secondary queues in producer order.  It
// replaces the obsolete Frame.Orders duplicate.
type OrderQueueView struct {
	Unit               pool.Handle
	Primary            []OrderView
	Secondary          []OrderView
	PrimaryTruncated   bool
	SecondaryTruncated bool
}

// SelectionView is committed local selection state [07 §9].
type SelectionView struct {
	LocalPlayer uint8
	Handles     []pool.Handle
	Primary     pool.Handle
	Count       uint16
	CommandMask uint32
}

// CommandPageView describes the selected builder's authored command page and
// carries the selection-aggregate command state the side panel stages and
// greys its command buttons from [07 §9][07 R-HUD-03 §6].
//
// Every aggregate field below is folded over the local player's selected units
// in ascending pool order — the order [07 §9] fixes for every selection walk —
// so the value is a pure function of committed state and is recomputed each
// tick.  The interface's own latch, the local write a stance or on/off click
// makes to stage the button before the next refresh, stays presentation-owned
// [04 R-STANCE-01 §2].
type CommandPageView struct {
	Builder     pool.Handle
	Page        uint16
	PageCount   uint16
	ProductKeys []string
	// GeneratedProducts retains the explicit product-to-slot records authored
	// by download/*.tdf for this visible page. ProductKeys remains the
	// canonical membership union used by dispatch; this slice preserves sparse
	// and conflicting BUTTON claims for generated GUI assembly [02 R-CAT-01
	// §8][07 §9].
	GeneratedProducts []GeneratedProductPlacement

	// MoveStance and FireStance are the two three-bit standing-order
	// aggregates [04 R-STANCE-01 §1].  0, 1 and 2 are the three stances, 3
	// means the selected units that accept the stance disagree, and 4 — the
	// value the fold starts from — means no selected unit accepts it, which is
	// the value that greys MOVEORD/FIREORD [07 R-HUD-03 §6].  A unit joins the
	// fold only when its definition authors the matching accept key:
	// mobilestandorders for the move field, firestandorders for the fire one
	// [04 R-STANCE-01 §5].
	MoveStance uint8
	FireStance uint8

	// CloakState and OnOffState are the two-bit cloak and on/off aggregates
	// CLOAK and ONOFF are staged from, greyed when the value is 3
	// [07 R-HUD-03 §6].  0 and 1 are the agreed states of the units that carry
	// the capability, and 3 — the value the fold starts from — means no
	// selected unit carries it.
	//
	// A disagreeing selection folds to 2, not to 3.  The aggregate refresh
	// starts each pair at 3, lets the first capable unit replace it with that
	// unit's state, and moves it to 2 from there; 3 therefore survives only a
	// walk that folded nothing.
	//
	// The two-bit folds, their sentinel and disagreement values, and the
	// on/off-vs-cloak asymmetry are [07 R-HUD-03 §13], which also corrects
	// §6's gloss of the greying value 3 as "mixed": 3 is the not-applicable
	// sentinel and 2 is the disagreement value.  §6's greying condition
	// (grey at 3) is unaffected.
	CloakState uint8
	OnOffState uint8

	// Stockpile is the held-round byte the count-label writer's `commonattribs`
	// bit 0x08 branch prints on a MAKENUKE/MAKEANTI toy: "a byte on the builder
	// unit — the stockpile count", cleared and unprinted when zero, with the
	// pending BUILDWEAPON total appended after it [07 R-P0-11 §2]. It is the
	// page unit's slot-0 completed-round remainder, which is where the order
	// alias's build type of zero puts every round and where every shipped
	// stockpile weapon lives [06 §11.1][06 R-WPN-05 §2].
	//
	// The pending half is not published beside it: the committed order queues
	// already carry the secondary BUILDWEAPON nodes the writer sums.
	Stockpile int32

	// The capability aggregates the stage/grey table of [07 R-HUD-03 §6]
	// reads: each button is greyed when its aggregate bit is clear.  Every
	// selected unit contributes its definition's authored capability key
	// [02 "Unit record"]: canmove, canstop, canattack, canguard (DEFEND),
	// canpatrol, canreclamate (RECLAIM), cancapture, canload (the transport
	// bit) and candgun (the blast bit).
	//
	// Repair is a separate aggregate bit fed by the parser's derived copy of
	// canreclamate, so it always agrees with Reclaim [02 R-KEYS-01 §1]; it is
	// published separately because §6's table greys REPAIR from its own bit.
	// IsTransport additionally hides BLAST when it is set and hides LOAD when
	// it is clear [07 R-HUD-03 §6].
	//
	// The fold is a disjunction: the aggregate bit is set once any selected
	// unit's definition carries the key, so a button is greyed only when no
	// selected unit can perform the command.
	//
	// The disjunction is [07 R-HUD-03 §13].  [04 §3.7] previously stated the
	// conjunction — "enabled only when every selected unit carries the bit" —
	// and is corrected there and in place.
	CanMove     bool
	CanStop     bool
	CanAttack   bool
	CanDefend   bool
	CanPatrol   bool
	CanReclaim  bool
	CanCapture  bool
	CanRepair   bool
	IsTransport bool
	CanBlast    bool
}

// GeneratedProductPlacement is one committed download-menu patch. Button is
// the authored zero-based slot byte and is deliberately not normalized or
// clamped at publication time [02 R-CAT-01 §8][fmt tdf].
type GeneratedProductPlacement struct {
	ProductKey string
	Button     uint8
}

// BuildProgressView carries construction progress in its authored float32
// domain [05 "Construction target state"].
type BuildProgressView struct {
	Builder      pool.Handle
	Product      pool.Handle
	ProductKey   string
	Remaining    float32
	AcceptedWork float32
	Health       int32
	MaxHealth    int32
	QueueIndex   int32
	Factory      bool
	Stalled      bool
	FootX, FootZ int8
}

// EconomyView is the HUD-facing per-player stock and ledger copy [05 "Player
// slot"].  It replaces the obsolete Frame.Resources duplicate.
type EconomyView struct {
	Player         uint8
	Metal          float32
	Energy         float32
	MetalCapacity  float32
	EnergyCapacity float32
	MetalProduced  float32
	MetalConsumed  float32
	EnergyProduced float32
	EnergyConsumed float32
	Active         bool
}

// VisibilityView contains the masks consumed by current-frame picking and
// fog presentation.  Unused radar/explored aliases are intentionally absent.
type VisibilityView struct {
	W, H          int32
	Visible       []uint8
	WordVisible   []uint16
	CoverageBytes bool
	Valid         bool
	// SeaLevel is the map header's sea-level byte scaled to 16.16 world units,
	// which is the value the gameplay visibility gate's step 3 compares a
	// unit's base height against — the comparison is against that scaled byte
	// and never against zero [03 §3.2][03 §2.2].  It rides the visibility
	// channel because it is only ever read beside the masks; presentation must
	// not reach into the mutable terrain for it [I6].
	SeaLevel numeric.Fixed
}

// RadarContactKind identifies the source record represented by a minimap
// contact.  The kind is part of the committed payload so presentation does
// not inspect the mutable projectile/feature pools [03 §3.9].
type RadarContactKind uint8

const (
	RadarContactUnit RadarContactKind = iota
	RadarContactProjectile
	RadarContactFeature
)

// RadarRingView carries one weapon-range ring's authored flags.  A unit owns
// three weapon slots; rings retain that slot order at the frame boundary
// [03 §3.9].
type RadarRingView struct {
	Enabled   bool
	Dashed    bool
	Range     int32
	Intercept bool
}

// RadarContactView is the immutable contact input used by minimap
// presentation. Coordinates remain authoritative 16.16 values until the
// renderer performs the documented signed narrowing [03 §3.9].
type RadarContactView struct {
	Kind          RadarContactKind
	Handle        pool.Handle
	Owner         uint8
	OwnerKnown    bool
	X, Y, Z       numeric.Fixed
	Status        uint32
	Hidden        bool
	Stealth       bool
	Active        bool
	OnOffable     bool
	Selected      bool
	RangeStatus   bool
	BlinkSuppress uint8
	Seen          bool
	Friendly      bool
	Commander     bool
	// Palette is the owner-player frame selector for authored radar/feature
	// art. PaletteKnown distinguishes a published selector of zero from an
	// unresolved owner or neutral contact; presentation must not recover an
	// unknown selector from live session state [03 §3.9][I6].
	Palette       uint8
	PaletteKnown  bool
	Visible       bool
	RadarDistance int32
	SonarDistance int32
	RadarJam      int32
	SonarJam      int32
	Graphic       string
	AssetID       string
	Rings         []RadarRingView
}

// RadarView is the committed radar/contact payload. It is rebuilt at every
// completed simulation tick and owns all nested slices [03 §3.6].
type RadarView struct {
	// Contacts is the whole contacts-pass input: blips, commander markers,
	// sensor circles and weapon rings all come from these records. There is no
	// second circle list — [03 §3.10]'s 2026-08-29 correction establishes the
	// contacts pass as the sole circle producer and retracts the reading that
	// gave the sensor phase a callback surface of its own.
	Contacts []RadarContactView
	// BlinkPhase is the committed bit-0 radar phase. It carries no countdown
	// or surface dirty flags; presentation consumes only this scalar
	// [R-CORE-03][03 §3.6].
	BlinkPhase uint8
	// MarkerMode is the authoritative minimap composer mode. Zero is the
	// explicit mode-off value until a simulation-owned source is available;
	// presentation must not force the viewport marker on [03 §3.12][I6].
	MarkerMode uint8
}

// EventKind identifies an ordered transient presentation cue.  Cues are not
// authoritative state and must not be used to drive simulation decisions.
type EventKind uint8

const (
	EventKindInvalid EventKind = iota
	EventKindCOBSFX
	EventKindNanolathe
	EventKindMuzzleFlash
	EventKindSmokeStart
	EventKindSmokeEnd
	EventKindProjectileTrail
	EventKindImpact
	EventKindWaterImpact
	EventKindExplosion
	EventKindLHTFlash
	EventKindShake
	EventKindCorpse
	EventKindAudio
	EventKindStatus
	// EventKindAnnounce is a battle message-line announcement: a finished line
	// of text, the ring class it is posted under and the slot it is attributed
	// to. It is a different thing from EventKindStatus, which is a unit
	// caption/voice request keyed by an audio slot and arbitrated by the audio
	// queue; an announcement has no audio slot and goes straight to the
	// message ring [07 R-HUD-03 §14.3][08 R-CAMP-01 §9].
	EventKindAnnounce
)

// String names the event kind for diagnostics; an out-of-range value reads as
// "invalid".
func (k EventKind) String() string {
	names := [...]string{"invalid", "cob_sfx", "nanolathe", "muzzle_flash", "smoke_start", "smoke_end", "projectile_trail", "impact", "water_impact", "explosion", "lht_flash", "shake", "corpse", "audio", "status", "announce"}
	if int(k) >= len(names) {
		return names[0]
	}
	return names[k]
}

// EventView is an ordered transient cue admitted during one authoritative
// tick. Sequence preserves producer order for presentation playback.
type EventView struct {
	ID                        uint32
	Sequence                  uint64
	Tick                      uint32
	Kind                      EventKind
	Source                    pool.Handle
	Target                    pool.Handle
	EffectID                  uint32
	Piece                     int32
	SFXType                   int32
	SFXClass                  SFXClass
	Graphic                   string
	X, Y, Z                   numeric.Fixed
	TargetX, TargetY, TargetZ numeric.Fixed
	Lifetime                  int32
	ExpiryTick                uint32
	Mode                      uint8
	Team                      uint8
	PaletteRow                int16
	Magnitude                 int32
	AssetID                   string
	SequenceID                string
	DurationsA                []int32
	DurationsB                []int32
	LoopA                     bool
	LoopB                     bool
	FlashRadius               int32
	FlashLevel                int32
	HasFlashDisc              bool
	Strip                     int8
	NanolatheIndex            int32
	NanolatheCount            int32
	NanolatheGeometryKnown    bool
	// NanolatheActiveUntil is the committed unit-caption/work highlight stamp;
	// zero means the producer supplied no stamp [04 R-ORD-01 §1].
	NanolatheActiveUntil    uint32
	NanolatheTargetBoxKnown bool
	NanolatheTargetMin      [3]numeric.Fixed
	NanolatheTargetMax      [3]numeric.Fixed
	// NanolatheBoxAtSource: the box above sits at the SOURCE end of the
	// segment and the published target point is the destination
	// [05 R-WORK-01 §8].
	NanolatheBoxAtSource bool
	Sound                string
	AudioPositional      bool
	AudioWater           bool
	AudioAudible         bool
	// Status events are semantic unit-caption requests. They are consumed by
	// the presentation edge, never by authoritative simulation [03 §8.3][07
	// R-HUD-03 §14].
	StatusKind  uint8
	StatusText  string
	StatusClass uint8
	// AnnounceSlot is the player slot an EventKindAnnounce line is attributed
	// to — the ring's speaker byte, which selects the logo and colour the
	// message column draws beside the line. Sentinel 10 is "no speaker"
	// [07 R-HUD-03 §14.3][08 R-CAMP-01 §9].
	AnnounceSlot uint8
}

// ResultScore is one player's committed result statistic.
type ResultScore struct {
	Player           int
	Team             int
	Name             string
	Logo             uint8
	Kills            int
	Losses           int
	EnergyProduced   int
	MetalProduced    int
	EnergyConsumed   int
	MetalConsumed    int
	EnergyWasted     int
	MetalWasted      int
	CommandersKilled int
	CommandersLost   int
	Score            int
	Kind             string
}

// ResultView is the committed terminal result shown by the frontend.
type ResultView struct {
	Ended      bool
	Kind       string
	WinnerTeam int
	Winners    []int
	Losers     []int
	Reason     string
	Tick       uint32
	ArmedTick  uint32
	Countdown  int16
	Draw       bool
	Scores     []ResultScore
	// ColumnMaxima are the seven ENDMSN bar maxima in Kills, Losses,
	// EProduced, MProduced, EWasted, MWasted, Score order. They are part of
	// the committed result so presentation never derives denominators from the
	// live world [08 R-CAMP-01 §7].
	ColumnMaxima [7]int
}

// PlayerRowSlots is the number of player slots published every tick. Retail
// scans the same ten slots in slot order for the score panel's rows and for
// the result rows [07 R-HUD-04 §1][08 R-CAMP-01 §7][I1].
const PlayerRowSlots = 10

// PlayerRow is one player slot's live per-tick state, published every tick so
// the Space-held Kills/Losses panel has the row filter's six terms, the two
// counter pairs and the rank byte without reaching into the session
// [07 R-HUD-04 §1][I6].
//
// It is deliberately separate from ResultScore: ResultScore is the latched
// end-of-battle statistic row of [08 R-CAMP-01 §7] (economy totals, score,
// win/lose kind) and exists only once the result is collected, while this row
// exists on every tick of a live battle and carries only what the panel's scan
// reads.
type PlayerRow struct {
	// Present is the record-exists term of the row filter.
	Present bool
	// Name is the player name written at (x0+9, y+6).
	Name string
	// Logo is the lobby record's logo byte, the frame number of the side-logo
	// GAF entry the row's logo is drawn from.
	Logo uint8
	// Kills and Losses are the slot's two 16-bit counters, widened. They are
	// the same words the result rows and the kill-lead line read
	// [08 R-CAMP-01 §7][08 R-CAMP-01 §9].
	Kills  int
	Losses int
	// CommandersKilled and CommandersLost are the pair the panel prints
	// instead when the commander-death option word is 2 (Deathmatch)
	// [07 R-HUD-04 §1][07 R-FE-01 §7].
	CommandersKilled int
	CommandersLost   int
	// Controller is the slot's controller byte; the row filter admits 1, 2
	// and 3 (human, local, remote — [08 R-SKIR-01 §1]).
	Controller uint8
	// Side is the slot's side byte; the neutral side 10 is excluded.
	Side uint8
	// Watcher is the lobby record's watcher bit (0x40); a watcher gets no row.
	Watcher bool
	// LiveUnits is the slot's live-unit count. The filter admits the slot when
	// this is nonzero or the auxiliary word is zero.
	LiveUnits int
	// Auxiliary is the per-slot word doc 08 leaves unnamed, the second half of
	// the live-unit term [07 R-HUD-04 §1][08 R-CAMP-01 §7].
	Auxiliary uint32
	// Rank is the slot's rank byte: the panel emits rows in rank order and
	// compacts a vacated rank in the same frame [07 R-HUD-04 §1], and a
	// credited kill moves the rank up the ladder [08 R-CAMP-01 §9].
	Rank uint8
}

// FogView is the committed two-channel fog cache [03 §3.3].
type FogView struct {
	W, H             int32
	OriginX, OriginZ int32
	Ch0, Ch1         []uint8
	Valid            bool
}

// Frame is one committed tick-end presentation payload.  It owns all slices;
// after Publish succeeds the writer must treat the frame as immutable until
// the next permitted BeginWrite reuse.
type Frame struct {
	Tick uint32
	// Paused is the scheduler state captured at this completed tick-end
	// publication. A pause transition can take effect synchronously without
	// another completed tick; UI keeps its own canonical truth for that interval
	// [01 §4.3][07 §11].
	Paused      bool
	Units       []UnitView
	Projectiles []ProjectileView
	Features    []FeatureView
	Effects     []EffectView
	// Strips is every live strip-object sub-record, in the composer's walk
	// order: strips ascending, objects in insertion order, sub-records in
	// vector order [03 §1][03 R-STRIP-01 §2]. Strip objects are authoritative
	// simulation state swept in phase 11, so presentation cannot read them
	// directly; this is the committed copy it draws from [I6].
	Strips      []StripView
	OrderQueues []OrderQueueView
	Economy     []EconomyView
	Selection   SelectionView
	CommandPage CommandPageView
	Builds      []BuildProgressView
	Visibility  VisibilityView
	Radar       RadarView
	Events      []EventView
	Fog         FogView
	Result      ResultView
	// Players is the ten player slots' live per-tick rows, indexed by slot,
	// slot 0..9 ascending [07 R-HUD-04 §1][I1]. Every slot is written every
	// tick; an absent record publishes its zero value with Present false.
	Players [PlayerRowSlots]PlayerRow
	// Shake is the authoritative camera jitter offset produced at phase 10
	// [03 §5.6][01 §4.4]. The session advances the shake driver with CRT draws
	// and publishes the cumulative offset; presentation only applies it.
	ShakeOffsetX   int32
	ShakeOffsetY   int32
	ShakeActive    bool
	ShakeDuration  int32
	ShakeRemaining int32
	ShakeAmpX      int32
	ShakeAmpY      int32
	// Strip is the Space-held bottom slide strip's three readouts
	// [07 §6][07 R-HUD-04 §4]. They are scheduling and lobby scalars, not
	// simulation state, and the strip is the only consumer.
	Strip StripReadout
}

// StripReadout carries what the §6 slide strip prints: the game time is the
// frame's own tick, so only the unit limit and the two speed words need
// publishing [07 §6][07 R-HUD-04 §4].
type StripReadout struct {
	// UnitLimit is the session's per-player unit limit — the `(Max %d)` half
	// of `Total Units: %d (Max %d)`. The count half is the viewing slot's live
	// unit count, which Players already carries [07 R-HUD-03 §12].
	UnitLimit int32
	// ActiveSpeed and RequestedSpeed are the scheduler's two speed words. The
	// strip prints the active one and appends a `(+/-n)` suffix when the
	// requested value differs [07 §6].
	ActiveSpeed    int32
	RequestedSpeed int32
}

// Reserve preallocates top-level slices. It preserves existing values and
// capacities; call it before the first publication when stable allocation
// behavior is required.
func (f *Frame) Reserve(c Capacities) {
	if f == nil {
		return
	}
	f.Units = reserve(f.Units, c.Units)
	f.Projectiles = reserve(f.Projectiles, c.Projectiles)
	f.Features = reserve(f.Features, c.Features)
	f.Effects = reserve(f.Effects, c.Effects)
	f.OrderQueues = reserve(f.OrderQueues, c.OrderQueues)
	f.Builds = reserve(f.Builds, c.Builds)
	f.Events = reserve(f.Events, c.Cues)
	f.Selection.Handles = reserve(f.Selection.Handles, c.Selection)
	f.CommandPage.ProductKeys = reserve(f.CommandPage.ProductKeys, c.CommandProducts)
	f.CommandPage.GeneratedProducts = reserve(f.CommandPage.GeneratedProducts, c.CommandProducts)
	f.Visibility.Visible = reserve(f.Visibility.Visible, c.Visibility)
	f.Radar.Contacts = reserve(f.Radar.Contacts, c.RadarContacts)
	f.Fog.Ch0 = reserve(f.Fog.Ch0, c.Fog)
	f.Fog.Ch1 = reserve(f.Fog.Ch1, c.Fog)
}

func reserve[T any](s []T, n int) []T {
	if n <= cap(s) {
		return s
	}
	dst := make([]T, len(s), n)
	copy(dst, s)
	return dst
}

// Reset clears the frame in place while retaining every slice capacity,
// including nested routes, piece transforms, effect/cue durations, result
// lists, visibility words, and fog channels.
func (f *Frame) Reset() {
	if f == nil {
		return
	}
	for i := range f.Units {
		pieces := f.Units[i].Pieces
		clear(pieces)
		f.Units[i] = UnitView{Pieces: pieces[:0]}
	}
	for i := range f.Effects {
		a, b := f.Effects[i].DurationsA, f.Effects[i].DurationsB
		clear(a)
		clear(b)
		f.Effects[i] = EffectView{DurationsA: a[:0], DurationsB: b[:0]}
	}
	for i := range f.OrderQueues {
		primary, secondary := f.OrderQueues[i].Primary, f.OrderQueues[i].Secondary
		resetOrders(primary)
		resetOrders(secondary)
		f.OrderQueues[i] = OrderQueueView{Primary: primary[:0], Secondary: secondary[:0]}
	}
	for i := range f.Events {
		a, b := f.Events[i].DurationsA, f.Events[i].DurationsB
		clear(a)
		clear(b)
		f.Events[i] = EventView{DurationsA: a[:0], DurationsB: b[:0]}
	}
	clear(f.Selection.Handles)
	f.Selection.Handles = f.Selection.Handles[:0]
	clear(f.CommandPage.ProductKeys)
	f.CommandPage.ProductKeys = f.CommandPage.ProductKeys[:0]
	clear(f.CommandPage.GeneratedProducts)
	f.CommandPage.GeneratedProducts = f.CommandPage.GeneratedProducts[:0]
	clear(f.Visibility.Visible)
	f.Visibility.Visible = f.Visibility.Visible[:0]
	clear(f.Visibility.WordVisible)
	f.Visibility.WordVisible = f.Visibility.WordVisible[:0]
	clear(f.Fog.Ch0)
	clear(f.Fog.Ch1)
	f.Fog.Ch0 = f.Fog.Ch0[:0]
	f.Fog.Ch1 = f.Fog.Ch1[:0]
	clear(f.Result.Winners)
	clear(f.Result.Losers)
	clear(f.Result.Scores)
	f.Result.Winners = f.Result.Winners[:0]
	f.Result.Losers = f.Result.Losers[:0]
	f.Result.Scores = f.Result.Scores[:0]
	clear(f.Projectiles)
	clear(f.Features)
	clear(f.Economy)
	clear(f.Builds)
	clear(f.Strips)
	f.Units = f.Units[:0]
	f.Projectiles = f.Projectiles[:0]
	f.Features = f.Features[:0]
	f.Effects = f.Effects[:0]
	f.Strips = f.Strips[:0]
	f.OrderQueues = f.OrderQueues[:0]
	f.Economy = f.Economy[:0]
	f.Builds = f.Builds[:0]
	f.Events = f.Events[:0]
	f.Tick = 0
	f.Paused = false
	f.ShakeOffsetX = 0
	f.ShakeOffsetY = 0
	f.ShakeActive = false
	f.ShakeDuration = 0
	f.ShakeRemaining = 0
	f.ShakeAmpX = 0
	f.ShakeAmpY = 0
	f.Selection = SelectionView{Handles: f.Selection.Handles}
	f.CommandPage = CommandPageView{
		ProductKeys:       f.CommandPage.ProductKeys,
		GeneratedProducts: f.CommandPage.GeneratedProducts,
	}
	f.Visibility = VisibilityView{Visible: f.Visibility.Visible, WordVisible: f.Visibility.WordVisible}
	for i := range f.Radar.Contacts {
		clear(f.Radar.Contacts[i].Rings)
		f.Radar.Contacts[i] = RadarContactView{Rings: f.Radar.Contacts[i].Rings[:0]}
	}
	f.Radar.Contacts = f.Radar.Contacts[:0]
	f.Radar = RadarView{Contacts: f.Radar.Contacts}
	f.Fog = FogView{Ch0: f.Fog.Ch0, Ch1: f.Fog.Ch1}
	f.Result = ResultView{Winners: f.Result.Winners, Losers: f.Result.Losers, Scores: f.Result.Scores, ColumnMaxima: f.Result.ColumnMaxima}
	f.Players = [PlayerRowSlots]PlayerRow{}
}

func resetOrders(s []OrderView) {
	for i := range s {
		route := s[i].Route
		clear(route)
		s[i] = OrderView{Route: route[:0]}
	}
}

// Buffer is a preallocated two-slot committed-frame buffer. The committed
// index is atomic solely to publish the writer's completed slot to a reader;
// the caller still owns the documented lifetime boundary above.
type Buffer struct {
	slots     [2]Frame
	committed atomic.Uint32 // zero means no publication; otherwise slot+1
	writeSlot uint8
	writing   bool
	lastTick  uint32
	published bool
	// Retained committed events, drained by the presentation consumer. See
	// event_retention.go: the two slots carry current STATE, which the next
	// publication legitimately supersedes, while events are one-shot
	// occurrences that must survive until they are applied
	// [03 R-AUD-01 §7][03 §2.4][I6].
	pendingEvents   []EventView
	pendingDropped  uint64
	pendingOverflow bool
}

// NewBuffer constructs a two-slot buffer and reserves the requested top-level
// capacities in both slots. With no argument, the zero-value capacities are
// used; callers may call Frame.Reserve before the first write.
func NewBuffer(capacities ...Capacities) *Buffer {
	b := &Buffer{}
	if len(capacities) != 0 {
		b.slots[0].Reserve(capacities[0])
		b.slots[1].Reserve(capacities[0])
	}
	return b
}

// BeginWrite returns the noncommitted slot after resetting it. Only one
// simulation writer may call this method. Calling it again before Publish
// restarts the same pending write.
func (b *Buffer) BeginWrite() *Frame {
	if b == nil {
		return nil
	}
	idx := uint8(0)
	if committed := b.committed.Load(); committed != 0 {
		idx = uint8((committed - 1) ^ 1)
	}
	b.writeSlot = idx
	b.writing = true
	f := &b.slots[idx]
	f.Reset()
	return f
}

// Publish commits the pending write at tick. Tick values must increase
// strictly, matching the one publication after each completed authoritative
// tick required by [01 §4.4]. A failed publication leaves the pending write
// available for correction and retry.
func (b *Buffer) Publish(tick uint32) error {
	if b == nil || !b.writing {
		return ErrPublishWithoutWrite
	}
	if b.published && tick <= b.lastTick {
		return ErrNonMonotonicTick
	}
	f := &b.slots[b.writeSlot]
	f.Tick = tick
	// Every committed tick's events join the retained queue exactly once, in
	// raise order, before the slot becomes readable. A publication cadence
	// faster than the presentation drain therefore supersedes state but never
	// discards an occurrence [03 R-AUD-01 §7][I6].
	b.retainCommittedEvents(f.Events)
	b.committed.Store(uint32(b.writeSlot) + 1)
	b.lastTick = tick
	b.published = true
	b.writing = false
	return nil
}

// Current returns the committed frame, or nil before the first successful
// publication. The returned frame is immutable until the next BeginWrite
// permitted by the documented reader lifetime contract.
func (b *Buffer) Current() *Frame {
	if b == nil {
		return nil
	}
	committed := b.committed.Load()
	if committed == 0 {
		return nil
	}
	return &b.slots[committed-1]
}

// PublishedTick reports the last committed tick.
func (b *Buffer) PublishedTick() (uint32, bool) {
	if b == nil || !b.published {
		return 0, false
	}
	return b.lastTick, true
}
