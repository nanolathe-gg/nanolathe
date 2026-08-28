// Package frame is the committed simulation-to-presentation boundary.
//
// A Frame is assembled by the single simulation writer and becomes immutable
// when Buffer.Publish succeeds.  Presentation samples the frame committed for
// the current tick; it does not interpolate between ticks [03 §2.4].  The
// buffer deliberately does not retain a previous frame or clone on publish.
//
// The caller must finish reading Buffer.Current before the next BeginWrite.
// BeginWrite reuses the slot that is not committed, so retaining a pointer to
// an older frame across the next write is a data race.  This is the explicit
// single simulation-writer/presentation-reader lifetime contract; this package
// does not add speculative locking or a third historical slot.
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
	RadarCircles    int
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
	IsBuilding           bool
	// Activated is the committed on/off state used by UI command dispatch.
	// Presentation must not rehydrate a selected unit from the live pool [I6].
	Activated bool
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
	Yaw                       uint16
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
}

// FeatureView is the committed copy of one live feature [05 "Feature
// instance and terrain cell"].
type FeatureView struct {
	InstanceID uint64
	// Owner is the plot's placer selector. Map-authored features use the
	// non-player selector 10; corpse/runtime features carry their owner's
	// player slot [03 §3.3][03 §3.9].
	Owner              uint8
	OwnerKnown         bool
	CX, CZ             int32
	X, Y, Z            numeric.Fixed
	DefName            string
	Model              string
	Health             int32
	MaxHealth          int32
	Status             uint32
	IsBurning          bool
	IsSinking          bool
	BurnTicks          int32
	FootX, FootZ       int8
	Filename           string
	SeqName            string
	SeqNameShad        string
	Animating          bool
	AnimationStartTick uint32
	AnimTrans          bool
	ShadTrans          bool
	Blocking           bool
	Reclaimable        bool
	Height             int32
	Geothermal         bool
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
	PresentationID         uint64
	ID                     uint32
	EventSeq               uint64
	Source                 pool.Handle
	Target                 pool.Handle
	EffectID               uint32
	Piece                  int32
	SFXType                int32
	SFXClass               SFXClass
	Mode                   uint8
	StartTick              uint32
	ExpiryTick             uint32
	Lifetime               int32
	X, Y, Z                numeric.Fixed
	TargetX                numeric.Fixed
	TargetY                numeric.Fixed
	TargetZ                numeric.Fixed
	VX, VY, VZ             numeric.Fixed
	Gravity                numeric.Fixed
	Kind                   string
	HasModel               bool
	SeqA                   int32
	SeqB                   int32
	Graphic                string
	PaletteRow             int16
	Light                  bool
	Shake                  int32
	AssetID                string
	SequenceID             string
	DurationsA             []int32
	DurationsB             []int32
	LoopA                  bool
	LoopB                  bool
	FlashRadius            int32
	FlashLevel             int32
	HasFlashDisc           bool
	Strip                  int8
	NanolatheIndex         int32
	NanolatheCount         int32
	NanolatheGeometryKnown bool
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

// CommandPageView describes the selected builder's authored command page.
type CommandPageView struct {
	Builder     pool.Handle
	Page        uint16
	PageCount   uint16
	ProductKeys []string
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
	Palette       uint8
	Visible       bool
	RadarDistance int32
	SonarDistance int32
	RadarJam      int32
	SonarJam      int32
	Graphic       string
	AssetID       string
	Rings         []RadarRingView
}

// RadarCircleView is one callback result from the completed sensor pass.
// U/V are the 128-world-unit callback coordinates and Kind preserves callback
// table order (outer, radar jammer, sonar jammer) [03 §3.4].
type RadarCircleView struct {
	// SourceID identifies the live unit that emitted this callback. It is
	// retained so cleanup cannot leave an orphaned circle in a committed frame.
	SourceID uint16
	U, V     int32
	Radius   int32
	Kind     uint8
}

// RadarView is the committed radar/contact payload. It is rebuilt at every
// completed simulation tick and owns all nested slices [03 §3.6].
type RadarView struct {
	Contacts []RadarContactView
	Circles  []RadarCircleView
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
)

func (k EventKind) String() string {
	names := [...]string{"invalid", "cob_sfx", "nanolathe", "muzzle_flash", "smoke_start", "smoke_end", "projectile_trail", "impact", "water_impact", "explosion", "lht_flash", "shake", "corpse", "audio"}
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
	Sound                     string
	AudioPositional           bool
	AudioWater                bool
	AudioAudible              bool
}

// ResultScore is one player's committed result statistic.
type ResultScore struct {
	Player int
	Team   int
	Kills  int
	Losses int
	Score  int
	Kind   string
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
	Tick        uint32
	Paused      bool
	Units       []UnitView
	Projectiles []ProjectileView
	Features    []FeatureView
	Effects     []EffectView
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
	f.Visibility.Visible = reserve(f.Visibility.Visible, c.Visibility)
	f.Radar.Contacts = reserve(f.Radar.Contacts, c.RadarContacts)
	f.Radar.Circles = reserve(f.Radar.Circles, c.RadarCircles)
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
	f.Units = f.Units[:0]
	f.Projectiles = f.Projectiles[:0]
	f.Features = f.Features[:0]
	f.Effects = f.Effects[:0]
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
	f.CommandPage = CommandPageView{ProductKeys: f.CommandPage.ProductKeys}
	f.Visibility = VisibilityView{Visible: f.Visibility.Visible, WordVisible: f.Visibility.WordVisible}
	for i := range f.Radar.Contacts {
		clear(f.Radar.Contacts[i].Rings)
		f.Radar.Contacts[i] = RadarContactView{Rings: f.Radar.Contacts[i].Rings[:0]}
	}
	clear(f.Radar.Circles)
	f.Radar.Contacts = f.Radar.Contacts[:0]
	f.Radar.Circles = f.Radar.Circles[:0]
	f.Radar = RadarView{Contacts: f.Radar.Contacts, Circles: f.Radar.Circles}
	f.Fog = FogView{Ch0: f.Fog.Ch0, Ch1: f.Fog.Ch1}
	f.Result = ResultView{Winners: f.Result.Winners, Losers: f.Result.Losers, Scores: f.Result.Scores}
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
