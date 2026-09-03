// Package orders implements order records and the queue pump [04 §3.2, §3.3][05][GAP T3].
package orders

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Code is the handler result code [04 §3.3].
type Code uint8

const FlagActive uint32 = 0x1000 // active marker – exactly one primary node carries it [04 §3.3][plan C9]

const (
	FlagAutoOp uint32 = 1 << iota // existence established [04 §3.3][05 "Queue subtraction"]; numeric values not established
	FlagPurgeSurvivor
	FlagTombstone
	// FlagRetryMark is the pump's code-9 completion flag
	// [04 §3.3][R-ORDER-02 §2]. Nothing in this package reads it: no pump,
	// cleanup or handler arm branches on it.
	//
	// Corrected 2026-09-02 (WU-19-107). This comment used to end "write-only
	// state — no reader may be invented"; that is now wrong, and it stood
	// against the reader research has since located. [04 R-PATH-01 §8] step 5.3
	// gates the ground goal installer's synthetic straight-line fallback on
	// "the unit has a current order record and that record's retiring flag is
	// clear", and [05 R-EGRESS-02] names that flag as exactly this one — the
	// fallback is suppressed for records the pump has already declared
	// complete. That is what leaves a re-armed move standing still against a
	// goal it cannot occupy instead of lurching at it once per re-arm. The
	// reader belongs to internal/movement's goal installer, not to this file.
	FlagRetryMark
	// FlagStopBuildingPending marks a record whose StartBuilding emitter ran
	// (EmitStartBuilding, the flag's only writer [R-ORDER-02 §2]); cleanup
	// emits the StopBuilding counterpart on every removal path.
	FlagStopBuildingPending
)

const (
	MoveNone    uint8 = 0
	MoveEnRoute uint8 = 1
	MoveArrived uint8 = 2
	MoveBlocked uint8 = 3
)

// Node is an 86-byte retail order record identity [04 §3.2] C5 (I13).
type Node struct {
	ID           ID            // descriptor identity [04 §3.2]
	Phase        uint8         // handler-private phase byte [04 §3.2]
	DynamicGate  uint32        // dynamic gate mask [04 §3.2]
	Deadline     int32         // deadline tick, -1 for none [04 §3.2]
	Owner        pool.Handle   // owning unit [04 §3.2]
	Target       pool.Handle   // target smart-reference [04 §3.2]
	GoalX        numeric.Fixed // goal position three 16.16 [04 §3.2]
	GoalY        numeric.Fixed
	GoalZ        numeric.Fixed
	GuardX       int16 // guard/fight anchor [04 §3.2]
	GuardY       int16
	CachedX      int16 // cached target position [04 §3.2]
	CachedY      int16
	Param1       uint32 // three general parameters [04 §3.2]
	Param2       uint32
	Param3       uint32 // build progress; for a mobile build the blocked-area retry counter [04 §3.2][R-ORDER-02 §1]
	StaticGate   uint32 // copy of descriptor static gate [04 §3.2]
	CreationTick uint32 // creation-tick snapshot [04 §3.2]
	Satisfied    uint32 // accumulated satisfied-gate bits [04 §3.2]
	Flags        uint32 // flag bits [04 §3.3][05]
	// Nanolathe path status extension [P0-I03][04 §7][04 §3.5]: published back from
	// the movement scheduler/route lifecycle so the pump and HUD can observe
	// en route / arrived / blocked without re-reading the movement grid.
	MoveState  uint8  // 0 none, 1 en route, 2 arrived, 3 blocked [P0-I03]
	PathStatus uint32 // copy of path.Status (0 success, 0x100 already, 0x200 rejected) [04 §7.2]
	// P0-I05 authoritative construction payloads [05 "Factory production lifecycle"][05 "Construction arithmetic"].
	// BuildDefKey is the canonical catalog key for factory/mobile products; it
	// survives save/load and maps to a stable catalog index in Param1 via
	// Catalog.UnitDefIndex. Using string+index avoids FNV-1a collisions (N04)
	// and provides the established name→index table at load [P0-I05].
	// Factory product: BuildDefKey+Param1(index)+Param2(count)+Phase progress [05].
	// Mobile build: BuildDefKey+Param1(index)+GoalX/Z site; Param3 is the
	// blocked-area retry counter [04 §3.2][R-ORDER-02 §1].
	// Assist/repair/reclaim/capture/resurrection: Target + operation-specific progress in Param2/3 [05].
	BuildDefKey string // canonical unit key for build products [P0-I05][02 §5]
	// RetailSubtypeCode and RetailSubtype preserve the optional handler payload
	// attached to a saved order.  The payload is deliberately opaque here: its
	// owning handler performs any typed fix-up, while this node keeps every word
	// available across a catalog/session restore [08 R-SAVE-02 §10].
	RetailSubtypeCode    uint32
	RetailSubtype        []byte
	RetailSubtypeUnitA   pool.Handle
	RetailSubtypeUnitB   pool.Handle
	RetailSubtypeWords16 []uint16
	RetailSubtypeWords32 []uint32
	// CaptionPending is the ONE-SHOT caption-pending flag of [04 §3.2] — one
	// of the two runtime bits the record's static-mask copy carries that no
	// static descriptor mask sets. The shared caption clear tests it, clears
	// it, and only then emits status kind 5 (`ok`) [04 R-ORD-01 §1]. Without
	// it a record that re-arms forever re-emits the acknowledgement voice on
	// every phase-0 re-entry, which [R-PATH-01 §14]'s composition (item 4)
	// states the steady state must NOT do: "silent and unbounded ... no
	// motion, no engine cue".
	//
	// It is a field rather than a bit of StaticGate/Flags because retail's bit
	// value is not established and the retail save word is `StaticGate |
	// Flags`, so inventing a position there could collide with a real static
	// bit and corrupt a restored record.
	//
	// TODO(question): [04 §3.2] and [04 R-ORD-01 §1] name the flag's tester
	// and its clearer but not its WRITER — nothing in the corpus says which
	// site arms it. This build arms it at record insertion (newNode), which
	// reproduces the observable contract: one acknowledgement per issued
	// order, silence on every later visit to the same record. Tracing the
	// arming site would settle whether some issuers leave it clear.
	CaptionPending bool
}

// Queue holds the two segments [04 §3.2] C5.
type Queue struct {
	primary   []*Node
	secondary []*Node

	// diagnostics records dispatch failures for this unit's queue. It is per
	// queue rather than package-global so two worlds in one process cannot
	// interleave their logs and so a queue's diagnostics die with it
	// [AGENTS.md §Diagnostics].
	diagnostics []string

	// secondaryTick is the per-queue tick published by the secondary walk. It
	// remains queue-owned state for handlers that need the most recent rear
	// segment visit; callers obtain session inputs from binding instead of
	// mirrored queue fields [06 §11.1][RS-P0-018].
	secondaryTick uint32

	binding *QueueBinding

	// lastPumpTick is the tick this queue was last pumped at. It is the tick a
	// handler invoked OUTSIDE a pump visit is given — the cancel notification
	// of [R-ORDER-02 §2], which cleanupNode delivers through the record's own
	// handler at removal time. Retail's handler bodies read the engine's
	// current tick [04 R-ORD-01 §1]; a removal reaches this package either
	// from inside a pump (where this is that pump's tick) or from a command
	// that ran in the same tick as the unit's last pump, so it is the current
	// tick in both. It is deliberately NOT secondaryTick: that state is the
	// queue's rear-walk marker
	// state the construction service transfers across a queue rebind, and
	// writing it from the primary walk would destroy that transfer.
	lastPumpTick uint32

	// getBuiltHandler is supplied by the construction service that owns the
	// product lifecycle. Keeping it on the queue preserves the ordinary ordered
	// primary walk without introducing package-global session state
	// [04 R-FAC-02 §4][I16]. Its signature is the descriptor Handler's: the
	// satisfied set is an argument because `GetBuilt`'s phase-2 body reads it —
	// the `0x8000` arm holds, the bit-0 arm decays [04 R-ORD-01 §11].
	getBuiltHandler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code
}

// SetGetBuiltHandler binds the construction-owned GetBuilt lifecycle to this
// queue. The queue pump remains the sole dispatcher and therefore preserves
// BeCarried/GetBuilt composition timing [04 R-FAC-02 §4].
func (q *Queue) SetGetBuiltHandler(handler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code) {
	if q != nil {
		q.getBuiltHandler = handler
	}
}

// QueueBinding is the concrete session-owned context every authoritative
// queue carries. Keeping these inputs together makes queue replacement and
// reconstruction an explicit value transfer instead of a collection of
// package-level fallbacks [04 §3.3][04 §3.4][06 §11.1].
type QueueBinding struct {
	Economy interface {
		UnitBuckets(pool.Handle) *[2]economy.Bucket
	}
	Lookup      func(pool.Handle) *units.Unit
	Hostility   func(actor *units.Unit, target *units.Unit) bool
	SimRNG      *rng.Simulation
	CurrentTick func() uint32

	// The following adapters are the session-owned runtime seam for the order
	// families. They are deliberately data-shaped rather than package globals:
	// the order package depends on request/result primitives, while session
	// composition supplies the concrete movement, world, work, combat, and
	// presentation owners [P0-00 A][04 R-ORD-01 §1]. The O0 composition gate
	// checks the adapters and operations needed by the binding/lifecycle seam;
	// family-specific callbacks remain nil until their owning O1/O3/O4 work
	// lands and must be checked by those callers before invocation.
	Movement     *MovementGoalAdapter
	World        *WorldQueryAdapter
	Work         *WorkAdapter
	Weapons      *WeaponAdapter
	Presentation *PresentationAdapter
	// Resources supplies the owning player's current stock and storage. It is
	// read by repair-patrol admission only; the economy service remains the
	// owner of these values [04 R-ORD-01 §4][05 "Player slot"].
	Resources func(uint8) (ResourceView, bool)

	// ReclaimFeature settles a finished feature reclaim at an anchor cell: it
	// reports the pools to credit and rewrites the cell [05 R-WORK-01 §5]. The
	// session binds it to the feature service, whose transition plays a
	// `seqnamereclamate` sequence out before the successor is stamped
	// [05 R-FEAT-01 §5]; the order package holds no service handle, so this is
	// a query like the two above. With none bound the payout falls back to the
	// terrain-only transition, which is what it used before the service grew
	// one — see finishFeatureReclaim.
	ReclaimFeature func(cx, cz int) (metal, energy float32, ok bool)

	// BuildList reports whether a definition's compiled build list holds at
	// least one entry. It is command code 14's whole gate: "the definition's
	// build list is non-empty and a live mover exists" [04 R-ORD-02 §1], NOT
	// the authored `builder` key. The list is the `CANBUILD` page of
	// gamedata/sidedata.tdf, compiled into content.Catalog.BuildMenus
	// [02 "Build-menu catalog keys"]; the order package holds no catalog
	// handle, so the session supplies the query.
	BuildList func(*content.UnitDef) bool

	// TransportAdmission is the carriable test — §10.2's nine-reject transport
	// admission for a (carrier, candidate) pair [04 §10.2][04 R-ORD-02 §1] —
	// which internal/movement owns. Five of the nine rejects read state the
	// order package does not own (the carrier's live cargo list, the
	// candidate's mover reference and committed mover mode, the map's sea
	// level), so the resolver asks the owner rather than re-deriving a second
	// copy of the ladder that could disagree with it.
	TransportAdmission func(carrier, candidate *units.Unit) bool

	// There is deliberately no separate alliance/diplomacy query here.
	// [04 R-ORD-02 §1] settles hostility as the acting PLAYER's diplomacy byte
	// toward the target's side — row A of [05 R-SHARE-01 §1], indexed by the
	// target's slot — and Hostility above is exactly that row as the session
	// composes it. A second field reading the same byte would be two sources of
	// truth for one value.
}

// ResourceView is the value-only economy snapshot needed by repair patrol's
// twenty-percent admission gates [04 R-ORD-01 §4]. Index 0 is metal and index
// 1 is energy, matching economy.Res [05 "Player slot"].
type ResourceView struct {
	Stock    [2]float32
	Capacity [2]float32
}

// PointGoalRequest identifies one order-node-owned point payload. Keeping the
// node pointer in every request prevents a late arrival from satisfying a
// successor that replaced the queue head [P0-00 B][04 R-ORD-01 §0].
type PointGoalRequest struct {
	Owner   pool.Handle
	Node    *Node
	X, Y, Z numeric.Fixed
	Radius  int32
}

// AnnulusGoalRequest identifies an annulus payload. Outer and inner radii are
// separate because the researched installer carries both values [04 R-ORD-01
// §1].
type AnnulusGoalRequest struct {
	Owner       pool.Handle
	Node        *Node
	X, Y, Z     numeric.Fixed
	OuterRadius int32
	InnerRadius int32
}

// RectangleGoalRequest identifies a snapped footprint rectangle payload
// [04 R-ORD-01 §1].
type RectangleGoalRequest struct {
	Owner        pool.Handle
	Node         *Node
	CellX, CellZ int32
	Width, Depth int32
}

// AirGoalRequest is the primitive air payload description. The movement
// package owns the marker implementation; orders only supplies its stable
// node identity and authored scalar inputs [P0-00 B][04 R-AIR-01 §4].
type AirGoalRequest struct {
	Owner   pool.Handle
	Node    *Node
	Target  pool.Handle
	X, Y, Z numeric.Fixed
	Radius  int32
	Flags   uint16
}

// MovementGoalAdapter is the narrow goal/release port used by order handlers.
// Each callback returns false when the owner cannot accept the request. The
// callback itself is responsible for publishing the pending word at the
// movement boundary; the order package does not duplicate that state machine.
type MovementGoalAdapter struct {
	Ready            func() bool
	InstallPoint     func(PointGoalRequest) bool
	InstallAnnulus   func(AnnulusGoalRequest) bool
	InstallRectangle func(RectangleGoalRequest) bool
	InstallAir       func(AirGoalRequest) bool
	Release          func(*Node) bool
	// RunAir is the queue-local air executor. Keeping it on the binding avoids
	// a process-global runner when more than one session exists [04 §3.3].
	RunAir AirLegRunner

	// AirBases returns the ally group's row of the per-side target registry's
	// third list — the damaged-aircraft base candidates of
	// [06 §3.1 "the third list"] and [04 R-AIR-01 §11]. internal/movement holds
	// the list and refills it on the registry's own 30-tick cadence; the two
	// patrol rows that seek a pad ask the owner for it rather than keeping a
	// second enumeration that could disagree, for the reason
	// TransportAdmission gives above. The returned slice is the holder's
	// storage and is read-only to this package; callers filter it with
	// combat.ScanAirBaseList.
	AirBases func(allyGroup uint8) []pool.Handle
}

// FeatureView is the value-only feature identity exposed to order scans. It
// intentionally avoids importing the feature runtime into orders (which would
// create a package cycle) and is traversed in the feature service's established
// stable anchor order [01 §6.2][05 "Feature instance and terrain cell"].
type FeatureView struct {
	ID      uint16
	CX, CZ  int32
	X, Y, Z numeric.Fixed
	// Footprint and Height are the authored feature-box dimensions used by
	// nanolathe presentation [05 R-WORK-01 §8]. They remain value-only here so
	// orders does not import the feature runtime.
	FootprintX      int32
	FootprintZ      int32
	Height          int32
	DefinitionKey   string
	Metal           int32
	Energy          int32
	Reclaimable     bool
	Autoreclaimable bool
}

// WorldQueryAdapter owns deterministic target/feature lookup and geometry
// queries. ForEachUnit and ForEachFeature must invoke callbacks in retail pool
// order; callers must not replace them with map traversal [P0-00 A,D][I1].
type WorldQueryAdapter struct {
	LookupUnit     func(pool.Handle) *units.Unit
	Hostile        func(*units.Unit, *units.Unit) bool
	ForEachUnit    func(func(pool.Handle, *units.Unit) bool)
	LookupFeature  func(int32, int32) (FeatureView, bool)
	ForEachFeature func(func(FeatureView) bool)
	TerrainHeight  func(numeric.Fixed, numeric.Fixed) (numeric.Fixed, bool)
	SeaLevel       func() uint8
	ModelBounds    func(pool.Handle) (int32, int32, bool)
	// DeclaresAlliance is the one-directional row read of [05 R-SHARE-01 §1]:
	// row A of `from` indexed by `toward`. `Hostile` above answers the
	// symmetric question the command resolver asks [04 R-ORD-02 §1]; this
	// answers the single-row question the guard's combat join asks
	// [04 R-UNIT-06 §1]. Nil when the binding has no player rows, in which case
	// the caller falls back.
	DeclaresAlliance func(from, toward uint8) bool

	// MappingWord reads one word of the per-player mapping word grid
	// [03 R-LAYER §1]: one 16-bit word per 2x2-cell tile, bits 0..9 one per
	// player slot, ORed by the phase-5 LOS stamp sweep and never decremented.
	// The arguments are TILE coordinates, already carrying whatever offset the
	// reader's own index arithmetic adds; the binding forms the grid's flat
	// index with its own stride, so a tile column past the stride wraps into
	// the next row exactly as retail's flat index does. ok is false only when
	// no grid is bound or the flat index falls outside the allocation.
	//
	// The one simulation reader in this tree is the aircraft landing test's
	// coarse early accept [04 R-AIR-01 §6a] as corrected by
	// [04 R-AIR-01 §14.2]. internal/movement reaches it through this port
	// because the grid belongs to the visibility service, which neither that
	// package nor this one holds a handle to. Nil when the composition has no
	// visibility service, in which case the caller runs its full test.
	MappingWord func(tileX, tileZ int32) (uint16, bool)
}

// WorkAdapter is the construction/repair/ownership port. The result is kept
// as a bool at this seam; concrete work services own their detailed progress,
// economy, packet, and callback state [P0-00 C].
type WorkAdapter struct {
	Ready          func() bool
	Assist         func(*units.Unit, *Node, uint32) bool
	Repair         func(builder, patient *units.Unit, node *Node, tick uint32) bool
	Capture        func(*units.Unit, *Node, uint32) bool
	ReclaimFeature func(*units.Unit, *Node, uint32) bool
	ReclaimUnit    func(*units.Unit, *Node, uint32) bool
	Resurrect      func(*units.Unit, *Node, uint32) bool
	Refresh        func(*units.Unit)
	// CancelNotice is the receiver for the cleanup cancel notification of
	// [R-ORDER-02 §2] on behalf of the records this package does not hold a
	// handler for. cleanupNode's guard — the record's dynamic gate still holding
	// bit 1 at removal — is unchanged; the notification simply has somewhere to
	// go for the three construction rows, which are handler-less because another
	// package runs them from its own state machine (drivenDescriptors). It is
	// how interrupt mask 2 reaches the factory's cancel-current body
	// [05 "Build request and factory queue behavior"][05 C21]. It reports
	// whether it accepted the notice; the return is advisory, since the record
	// is already being freed.
	CancelNotice func(owner *units.Unit, n *Node, tick uint32) bool
}

// WeaponAdapter is the order-facing combat slot port. Slot operations remain
// callbacks so combat remains the sole owner of authoritative weapon state
// [P0-00 E][06 §1.2].
type WeaponAdapter struct {
	Ready           func() bool
	ReleaseSlot     func(*units.Unit, int) bool
	InhibitSlot     func(*units.Unit, int) bool
	SetManualTarget func(*units.Unit, int, pool.Handle) bool
	FireTarget      func(*units.Unit, int, pool.Handle, uint32) bool
	FirePoint       func(*units.Unit, int, numeric.Fixed, numeric.Fixed, uint32) bool
	StopFiring      func(*units.Unit, int) bool
	Acquire         func(*units.Unit, int, uint32) (pool.Handle, bool)
	Engaged         func(*units.Unit, int) bool
	// CanEngage is the shot-admission gate of [04 R-ORD-01 §7]: given a
	// shooter, a candidate target and a slot index, may that slot be bound to
	// that target right now. `Attack_Chase` phases 1 and 3 branch on it
	// [04 R-ORD-01 §3]. It is distinct from Engaged, which asks the same
	// question about the target a slot has ALREADY been bound to.
	CanEngage func(*units.Unit, pool.Handle, int) bool
}

// PresentationAdapter is the committed-frame event port. It carries semantic
// status and nanolathe events without allowing the order pump to mutate client
// state [P0-00 F][03 §1].
type PresentationAdapter struct {
	Ready            func() bool
	Status           func(*units.Unit, uint8, string) bool
	Nanolathe        func(*units.Unit, *Node, uint32) bool
	NanolatheFeature func(*units.Unit, *Node, FeatureView, uint32) bool
}

// Validate reports whether the binding is complete enough to run a battle.
//
// It runs once, when the session composes the binding, and not per pump: by
// the time a record is dispatched the services it needs are either all present
// or the session never started. Family-specific effects still check their own
// operation callback before use.
func (b *QueueBinding) Validate() error {
	if b == nil {
		return fmt.Errorf("orders: missing queue binding")
	}
	if b.SimRNG == nil {
		return fmt.Errorf("orders: missing simulation RNG")
	}
	if b.Economy == nil || b.Lookup == nil || b.Hostility == nil || b.Resources == nil {
		return fmt.Errorf("orders: incomplete base queue services")
	}
	if b.Movement == nil || b.World == nil || b.Work == nil || b.Weapons == nil || b.Presentation == nil {
		return fmt.Errorf("orders: incomplete single-player queue services")
	}
	if b.Movement.Ready == nil || !b.Movement.Ready() || b.Movement.InstallPoint == nil || b.Movement.Release == nil || b.Movement.RunAir == nil {
		return fmt.Errorf("orders: incomplete movement goal service")
	}
	if b.Work.Ready == nil || !b.Work.Ready() || b.Weapons.Ready == nil || !b.Weapons.Ready() || b.Presentation.Ready == nil || !b.Presentation.Ready() {
		return fmt.Errorf("orders: incomplete single-player subsystem service")
	}
	if b.World.LookupUnit == nil || b.World.Hostile == nil || b.World.ForEachUnit == nil || b.World.ForEachFeature == nil || b.World.LookupFeature == nil || b.World.TerrainHeight == nil || b.World.SeaLevel == nil {
		return fmt.Errorf("orders: incomplete world query service")
	}
	// Command resolution's two owned-elsewhere gates: code 14's build list and
	// the carriable test [04 R-ORD-02 §1][04 §10.2]. Both fail closed when
	// absent, so a battle that started without them would silently refuse
	// mobile build and every pickup.
	if b.BuildList == nil || b.TransportAdmission == nil {
		return fmt.Errorf("orders: incomplete command resolution service")
	}
	return nil
}

// ForEachUnit visits live units through the composed world adapter. The
// adapter, rather than a queue handler, owns the retail slot order; returning
// true from the visitor stops further callbacks [01 §4.4][01 §6.2][I1].
func (b *QueueBinding) ForEachUnit(visit func(pool.Handle, *units.Unit) bool) {
	if b == nil || b.World == nil || b.World.ForEachUnit == nil || visit == nil {
		return
	}
	b.World.ForEachUnit(visit)
}

// ForEachFeature visits live features through the composed world adapter. The
// feature service supplies stable anchor order; this helper never ranges a
// feature map [01 §6.2][05 "Feature instance and terrain cell"][I1].
func (b *QueueBinding) ForEachFeature(visit func(FeatureView) bool) {
	if b == nil || b.World == nil || b.World.ForEachFeature == nil || visit == nil {
		return
	}
	b.World.ForEachFeature(visit)
}

// LookupFeature resolves an anchored live feature through the same world
// adapter used by traversal. A missing feature is represented by ok=false,
// not by a fabricated definition [P0-00 D].
func (b *QueueBinding) LookupFeature(cx, cz int32) (FeatureView, bool) {
	if b == nil || b.World == nil || b.World.LookupFeature == nil {
		return FeatureView{}, false
	}
	return b.World.LookupFeature(cx, cz)
}

// Tick returns the session's current authoritative tick when the binding
// supplies one. Handlers normally receive the pump tick directly; this seam is
// for callbacks reached during queue cleanup outside the normal walk [01
// §4.4][04 R-ORD-01 §1].
func (b *QueueBinding) Tick() uint32 {
	if b == nil || b.CurrentTick == nil {
		return 0
	}
	return b.CurrentTick()
}

// SetBinding installs all per-queue authoritative inputs as one value.
func (q *Queue) SetBinding(b *QueueBinding) {
	if q == nil {
		return
	}
	q.binding = b
}

// Binding returns this queue's concrete, session-owned binding. A queue has no
// implicit or synthesized runtime context: an unbound queue returns nil.
func (q *Queue) Binding() *QueueBinding {
	if q == nil {
		return nil
	}
	return q.binding
}

// BindQueueBinding ensures a lazily-created queue receives its owner's session
// context before any order can be pumped or resolved.
func BindQueueBinding(u *units.Unit, b *QueueBinding) *Queue {
	if u == nil {
		return nil
	}
	q := QueueForUnit(u)
	if b != nil {
		q.SetBinding(b)
	}
	return q
}

// [P2-03][P1-I09] Queue storage is dynamic, matching retail's heap-linked list
// (NEGATIVE-BOUNDED 3901 boundaries found no cap). The previous 64/32 caps
// were inside stock-reachable behavior: corpus measurement over 275 maps /
// 278 units / 175 campaign missions shows a retail InitialMission can queue
// 105 raw tokens (Silent Slayers carry1: g ms1,g ms2,m...w...) and would
// require >64 primary nodes uncapped; the capped run truncated to 64.
// The secondary max in corpus is 1, but 32 is an arbitrary divergence.
// Retail has no located cap, so Nanolathe uses dynamic slice growth with an
// OOM guard only at a very large threshold far outside stock (OOMGuardQueue
// below, applied at content admission in Push/PushSecondary/CoalesceTail).
//
// There is deliberately NO pump-iteration cap and NO queue-code guard that
// changes behavior mid-walk (ORD-02): retail can wedge on a tight
// script/order loop, and a defensive cap would alter queue state, RNG use,
// and later updates — reproducing the wedge is the contract [04 §3.3][I11].
// Memory safety belongs at admission, not in the running queue.
// Corpus: TestCorpusQueueCaps_Retail (internal/orders/corpus_caps_test.go)
// measures maxPrimary 105+ uncapped and maxSecondary 1.
// TODO(T23): exact allocator zero-fill byte count for order nodes (retail
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// used a different memset length but observable effect is zeroed.
const OOMGuardQueue = 10000

func (q *Queue) LenPrimary() int {
	if q == nil {
		return 0
	}
	return len(q.primary)
}
func (q *Queue) LenSecondary() int {
	if q == nil {
		return 0
	}
	return len(q.secondary)
}
func (q *Queue) Primary() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.primary))
	copy(out, q.primary)
	return out
}
func (q *Queue) Secondary() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.secondary))
	copy(out, q.secondary)
	return out
}

// SetPrimary replaces the primary segment [P0-I16][P0-I05].
// Exported accessor replaces reflect/unsafe inspection (ON-02).
func (q *Queue) SetPrimary(primary []*Node) {
	if q == nil {
		return
	}
	q.primary = primary
}

// SetSecondary replaces the secondary segment [P0-I16][04 §3.2].
// Exported accessor replaces reflect/unsafe inspection (ON-02).
func (q *Queue) SetSecondary(secondary []*Node) {
	if q == nil {
		return
	}
	q.secondary = secondary
}

// NewQueueWith constructs a queue with the given segments [04 §3.2] C5.
// Exported constructor for construction service to avoid reflect/unsafe (ON-02).
func NewQueueWith(primary []*Node, secondary []*Node) *Queue {
	return &Queue{primary: primary, secondary: secondary}
}

// Pump is the per-unit order pump, replacing PumpAll-style global sweeps [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
// It operates on a single handle per call to preserve worker/economy bucket isolation:
// stepping builder A does not advance builder B. The existing Queue.Pump remains
// for compatibility but is non-authoritative in new session code (ON-02).
type Pump struct {
	World *units.World // authoritative unit pool (fixed pools, slot 0 null) [01 §6.1][P0-16]
}

// PumpResult reports the outcome of a single-unit pump [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
type PumpResult struct {
	Handle       pool.Handle // requested handle
	Found        bool        // unit existed and was alive
	HadQueue     bool        // queue had at least one node before pumping
	PrimaryLen   int         // primary length after pump
	SecondaryLen int         // secondary length after pump
	Err          error       // explicit error for missing unit or other failure, nil on success
	Diagnostics  []string    // queue diagnostics captured during pump
}

// PumpUnit advances only the named unit's existing queue/work state [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
// It preserves the primary head-blocking restart-from-head and secondary skip-not-due contracts:
// primary restarts from the head after each dispatch, secondary scans front-to-back skipping not-due.
// Only the named unit's queue advances; other builders are untouched. An absent
// queue remains absent: allocation belongs to the command/order producer, not
// to the per-tick unit visit [04 §3.3][04 §3.5].
func (p *Pump) PumpUnit(handle pool.Handle, tick uint32) PumpResult {
	if p == nil || p.World == nil {
		return PumpResult{Handle: handle, Err: fmt.Errorf("orders: nil pump or world")}
	}
	u := p.World.Unit(handle)
	if u == nil {
		return PumpResult{Handle: handle, Found: false, Err: fmt.Errorf("orders: unit %d not found or dead", handle)}
	}
	q := QueueOfUnit(u)
	if q == nil {
		return PumpResult{Handle: handle, Found: true, HadQueue: false, PrimaryLen: 0, SecondaryLen: 0}
	}
	had := q.LenPrimary()+q.LenSecondary() > 0
	// Preserve existing Queue.Pump semantics exactly: primary head-blocking, secondary skip-not-due.
	q.Pump(u, tick)
	prim := q.LenPrimary()
	sec := q.LenSecondary()
	// No order-guard write. [07 R-WGT-01 §10]'s store census over the word the
	// eligibility sites compare finds no writer anywhere in the order subsystem
	// — "not the order-record constructor, not the primary or secondary pump,
	// not the handler return-code epilogue, not cancel-all, not the
	// single-record expiry helper, and not the idle-queue refill". The word is
	// the remaining-build fraction, which construction owns.
	diags := q.Diagnostics()
	return PumpResult{Handle: handle, Found: true, HadQueue: had, PrimaryLen: prim, SecondaryLen: sec, Diagnostics: append([]string(nil), diags...)}
}

func isSecondary(id ID) bool {
	return DescriptorFor(id).StaticGate&0x40000 != 0 // [04 §3.1] rear-segment selection flag
}

func (q *Queue) simForJitter() *rng.Simulation {
	if q != nil {
		if binding := q.Binding(); binding != nil {
			return binding.SimRNG
		}
	}
	return nil
}

func (q *Queue) randBelow15() uint32 {
	if q.simForJitter() == nil {
		panic("orders: simulation RNG not injected [DET-01]")
	}
	return q.simForJitter().Uint32n(15) // gameplay jitter uses simulation stream [I4][04 §3.3]
}

func (q *Queue) randBelow30() uint32 {
	if q.simForJitter() == nil {
		panic("orders: simulation RNG not injected [DET-01]")
	}
	return q.simForJitter().Uint32n(30) // [R-P0-01] code 9's last re-arm draws RNG(30), a distinct draw site from code 3's RNG(15)
}

// moveGroundGoalRadius is the arrival radius `Move_Ground` phase 0 binds with
// its point goal: "radius `(int16)payloadType + 4`" [04 R-ORD-01 §4], the
// record's first general parameter word read as a SIGNED 16-bit value.
//
// MoveGroundGoalRadius below is the read-back seam for the movement layer, the
// same shape PatrolGoalRadius and ParkGoalRect already give it: the handler
// authors the geometry and the movement layer reads it back rather than
// restating the arithmetic beside a descriptor name it does not own.
func moveGroundGoalRadius(n *Node) int32 {
	if n == nil {
		return 4
	}
	return int32(int16(uint16(n.Param1))) + 4
}

// MoveGroundGoalRadius reports the arrival radius `Move_Ground` binds, and
// whether n is that row [04 R-ORD-01 §4].
func MoveGroundGoalRadius(n *Node) (int32, bool) {
	if n == nil || DescriptorFor(n.ID).Name != "Move_Ground" {
		return 0, false
	}
	return moveGroundGoalRadius(n), true
}

// moveGroundHandler is `Move_Ground`, and since WU-18-8 that descriptor alone
// (see ensureMoveHandlers below for what else used to run this body and why it
// was wrong).
//
// Row [04 R-ORD-01 §4][R-P0-01]: phase 0: carried -> cancel-all; caption clear;
// point goal at the record's goal with radius `(int16)payloadType + 4`; gate =
// 0xE0; advance. Phase 1: satisfied 0x20 -> status 6 (`Arrived`), complete;
// else *re-arm* (9), which resets the phase and rebinds from phase 0 after
// 30..59 ticks. Other phase: cancel-all — a phase byte outside the machine
// cancels the whole queue [R-ORDER-02 §1].
//
// Corrected 2026-09-02 (WU-19-97). Phase 0 armed the gate and nothing else: it
// ran neither the row's caption clear nor its point-goal install, so the ONE
// row of the whole table that is the ordinary move was the one row that owned
// no goal payload. Everything downstream had to work around that. The
// controller's slot stayed empty for an ordinary move, so the follower's
// arrival step had no payload to ask [04 R-MOV-03 §2 step 1] and the arrival
// bit `0x20` this handler's phase 1 waits on had to be produced from a
// name-keyed handle instead; the follower's repath arm ([04 R-MOV-03 §2] step
// 3, "with a payload installed") could not be gated as the section writes it,
// because gating it would have stopped every ordinary move from re-pathing;
// and an install by any OTHER record could not displace this record's object
// from the slot, because there was none, so the `0x80` rebind raise of
// [04 R-ORD-01 §9] never reached a `Move_Ground` record.
//
// The install is the same helper every combat and work row already reaches —
// installPointGoal (combat.go) — so this row now clears pending `0x20`-`0x200`,
// releases its own previous object and binds the new one exactly as they do.
//
// The radius is the row's own: `(int16)payloadType + 4`, the record's first
// general parameter word read as a SIGNED 16-bit value [04 R-ORD-01 §4]. It is
// 0 for interface- and most AI-issued moves (radius 4, handle threshold
// floor(4/16)² = 0 — arrival on the exact goal cell) and 160 for the AI wave
// task's gather broadcast [08 R-AI-01 §19]. internal/movement's
// goalRadiusParamFor reads the same word for the fallback handle it builds
// before this handler has run.
func moveGroundHandler(u *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
	if u != nil && u.Attachment.Carrier != 0 {
		return 7 // reject while attached [R-P0-01]
	}
	if n.Phase == 0 {
		captionClear(u, n) // [04 R-ORD-01 §4] "caption clear" [04 R-ORD-01 §1]
		// "point goal at the record's goal with radius `(int16)payloadType + 4`"
		// [04 R-ORD-01 §4].
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, moveGroundGoalRadius(n))
		n.DynamicGate = 0xE0 // [R-P0-01] phase 0 arms gate 0xE0
		return 1
	}
	if n.Phase > 1 {
		return 7 // cancel-all: a phase outside the machine [R-ORDER-02 §1]
	}
	if satisfied&0x20 != 0 { // [R-P0-01] combined&0x20 -> ack + return 5
		// The acknowledgement is the row's "status 6 (`Arrived`)"
		// [04 R-ORD-01 §4]: the shared status emitter, whose three-clause
		// producer gate (owner is the local viewing player, alive bit set,
		// silenced bit clear) and default-caption substitution live in the
		// session-owned adapter behind workStatus [04 R-ORD-01 §1]
		// [03 R-AUD-01 §3]. It reaches presentation as a committed status
		// event, never as sim audio [I6]. This is the same call the two other
		// kind-6 raisers make — `Attack_Kamikaze` phase 1 (combat.go) and
		// `VTOL_Move` phase 2 (patrol.go).
		workStatus(u, statusArrived, "Arrived")
		return 5
	}
	return 9 // [R-P0-01] drop when further records else 30+RNG30 wait
}

// ensureMoveHandlers installs moveGroundHandler on the one descriptor whose row
// it is.
//
// Correction (WU-18-8). This installer used to name all eight members of the
// move family — `Move_Ground`, `VTOL_Move`, `QMove`, `Patrol`, `QPatrol`,
// `VTOL_Patrol`, `RepairPatrol`, `VTOL_RepairPatrol` — and give every one of
// them the ground move's body. Only the first IS that body. [04 R-ORD-01 §4]
// gives `Patrol` and `RepairPatrol` their own multi-phase machines built on the
// patrol-chain setup; [04 R-ORD-01 §2] gives the queued-move pair a single row
// of its own ("Deadline 60, *rotate*") that owns no goal and reads no target;
// [04 R-ORD-02 §2] gives the two air forms bodies that end differently from the
// ground move; and [04 R-ORD-01 §7] gives `VTOL_RepairPatrol` a body vtolwork.go
// had already written. The over-claim was not inert: a family installer assigns
// only where a descriptor's handler is still nil and this list runs first, so
// claiming the seven other names took them out of the reach of the installers
// that owned them. A `Patrol` walked to its first waypoint and completed, a
// `RepairPatrol` neither patrolled nor repaired, and `VTOL_RepairPatrol`'s
// written and tested body could never install. The seven rows now live in
// patrol.go and vtolwork.go.
func ensureMoveHandlers() {
	id := Lookup("Move_Ground")
	if id != 0 && int(id) < len(table) && table[int(id)].Handler == nil {
		table[int(id)].Handler = moveGroundHandler
	}
}

func init() {
	// Attempt early install; if table not yet built (init order) the lazy ensure will retry on first pump.
	ensureMoveHandlers()
}

func findActive(q *Queue) int {
	if q == nil {
		return -1
	}
	for i, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			return i
		}
	}
	return -1
}

// ensureSingleActive keeps the active-marker invariant: exactly one primary
// node carries FlagActive [04 §3.3]. When none does the head takes it; when
// several do (possible while the marker travels) later duplicates are cleared.
// Insertion moves the mark to the inserted node and removal hands it to the
// removed node's successor [04 §3.3][05 "Queue insertion"].
func (q *Queue) ensureSingleActive() {
	if q == nil || len(q.primary) == 0 {
		return
	}
	first := -1
	for i, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			if first == -1 {
				first = i
			} else {
				n.Flags &^= FlagActive
			}
		}
	}
	if first == -1 {
		q.primary[0].Flags |= FlagActive
	}
}

// newNode is the record constructor every insertion path goes through.
//
// Correction (WU-18-0). This function used to seed the record's DYNAMIC gate
// from the descriptor's STATIC mask:
//
//	if nn.DynamicGate == 0 { nn.DynamicGate = desc.StaticGate }
//
// The two are different fields with different meanings and must not be
// conflated. The static mask is insertion metadata: [04 §3.1]'s census names
// bit 9 (0x200) "constructed without a target unit clears it", bit 10 (0x400)
// the same for a goal position, bit 18 (0x40000) rear-segment selection, and
// bit 20 (0x100000) the nanolathe/build-site class; every other static bit has
// no located reader and is stored opaque. Not one of them is a thing to wait
// for. The dynamic gate is the opposite field: [04 §3.3] step 2 intersects it
// with the record's own satisfied bits and the unit's capability word, and
// step 3 stops the whole walk when the gate is nonzero and nothing in it is
// satisfied. Seeding it with insertion metadata therefore made a record ask to
// be woken by bits nothing raises — a `Capture` or `Reclaim` record parked at
// the head of its unit's primary queue forever, taking every order behind it
// down with it, and never reaching the pump's missing-handler diagnostic.
//
// The contract is explicit: "the record constructor zeroes the dynamic gate and
// the pending word, so a freshly inserted record is dispatched on its very next
// pump visit with an empty satisfied set" [04 R-ORD-01 §1]. A record waits only
// for what a handler asks it to wait for; the copy of the static mask is kept,
// because [04 §3.2] gives the record a static-mask copy field of its own.
// staticPurgeSurvivor is bit 2 of a descriptor's static gate mask. It is the
// purge-survivor bit: the keep-survivors purge a non-queued (Replace) issue
// runs removes every front-segment record whose static-mask copy lacks it
// [04 §3.3][04 R-MOV-03 §6]. The nine descriptors that carry it are
// `MakeSelectable`, `Wait`, `AttackUType`, `WaitForAttack`, `GetBuilt`,
// `BeCarried`, `Paralyze`, `SelfRepair` and `BuildingBuild` [04 §3.1].
const staticPurgeSurvivor uint32 = 0x4

func newNode(id ID, n Node) *Node {
	desc := DescriptorFor(id)
	nn := n
	nn.ID = id
	if nn.StaticGate == 0 {
		nn.StaticGate = desc.StaticGate
	}
	// [04 §3.1]'s constructor clear: static bit 9 (0x200, staticTargetObserver
	// — "this record was issued against a target") is cleared on the record's
	// own static-mask copy when no target unit was supplied to the
	// constructor, closed by [04 R-MOV-03 §7] ("clears 0x200 from the
	// static-mask copy when no target was supplied"). Target zero (pool.Handle's
	// null) is this build's "no target supplied", matching every existing
	// target-presence test in the package [04 §3.2]. This is what lets a
	// reader distinguish "issued without a target" from "issued against a
	// target that has since gone" by the bit alone, e.g. the shared air-attack
	// entry's step 2 [04 R-AIR-01 §16].
	//
	// The same established fact lists a second constructor clear — bit 10
	// (0x400) when no goal position was supplied — but this build's Node has
	// no field distinguishing "no goal was supplied" from "goal supplied at
	// the fixed-point origin", and every caller that constructs a record
	// setting GoalX/Y/Z lives outside this file (combat.go, work.go,
	// resolve.go, vtolwork.go, transport.go, park.go, patrol.go, standing.go
	// — none owned by this work unit). Applying it here would mean guessing a
	// "supplied" signal this build does not track, which is the invented
	// behavior CLAUDE.md rule 1 forbids. Left open for whichever unit adds a
	// goal-supplied signal at the construction call sites.
	if nn.Target == 0 {
		nn.StaticGate &^= staticTargetObserver
	}
	// Survivorship is a property of the record's descriptor, not of the queue
	// modifier that inserted it. This used to be set from the caller's
	// queued/non-queued flag instead, which had the two halves of [04 §3.3]
	// backwards: a shift-queued `Move_Ground` survived a later Replace it
	// should not have, and a factory's `BuildingBuild` — which carries bit 2
	// — was purged by the first plain move order the player gave the factory,
	// which is what stopped a factory with a rally point from producing.
	if nn.StaticGate&staticPurgeSurvivor != 0 {
		nn.Flags |= FlagPurgeSurvivor
	}
	if nn.Deadline == 0 {
		nn.Deadline = -1
	}
	// Arm the one-shot caption-pending flag [04 §3.2]. Insertion is where the
	// static-mask copy is taken, so it is where the runtime bits the copy
	// carries are armed; see the TODO(question) on Node.CaptionPending.
	nn.CaptionPending = true
	node := &Node{}
	*node = nn
	return node
}

// Diagnostics returns this queue's dispatch failures.
func (q *Queue) Diagnostics() []string {
	if q == nil {
		return nil
	}
	return append([]string(nil), q.diagnostics...)
}

// ClearDiagnostics drops the recorded dispatch failures.
func (q *Queue) ClearDiagnostics() {
	if q != nil {
		q.diagnostics = nil
	}
}

func (q *Queue) recordDiagnostic(msg string) {
	if q == nil {
		return
	}
	//  bound diagnostics to 256 entries to prevent per-tick unbounded growth when descriptors have nil handlers by design.
	const maxDiagnostics = 256
	if len(q.diagnostics) >= maxDiagnostics {
		copy(q.diagnostics, q.diagnostics[1:])
		q.diagnostics = q.diagnostics[:maxDiagnostics-1]
	}
	q.diagnostics = append(q.diagnostics, msg)
}

// cancelAll frees every record on both segments [04 §3.3] result code 7 and
// [05 "Queue pumping and result codes"]. Non-head primary records and every
// secondary record are tombstoned, which is what suppresses their
// weapon-target-clear notification [05 "Queue subtraction"].
func (q *Queue) cancelAll() {
	for i, n := range q.primary {
		if i != 0 {
			n.Flags |= FlagTombstone
		}
		q.cleanupNode(n)
	}
	for _, n := range q.secondary {
		n.Flags |= FlagTombstone
		q.cleanupNode(n)
	}
	q.primary = nil
	q.secondary = nil // via the pair-removal helper [05]
}

// ownerUnit resolves a record's owning unit through the queue's binding
// lookup. It returns nil when no lookup is installed (bare fixtures), which
// leaves every callback arrange a no-op.
func (q *Queue) ownerUnit(n *Node) *units.Unit {
	if q == nil || n == nil || n.Owner == 0 {
		return nil
	}
	binding := q.Binding()
	if binding == nil || binding.Lookup == nil {
		return nil
	}
	return binding.Lookup(n.Owner)
}

// cleanupNode runs the strict record-removal cleanup order [R-ORDER-02 §2]:
//
//  1. restore the record identity — records are named Go fields (I13),
//     nothing to restore;
//  2. when the record's dynamic gate mask — the same field the pump consumes
//     — still holds bit 1 (value 2) at removal, invoke the operation handler
//     with that cancel-notification mask: a record removed while waiting on
//     that bit delivers the cancel-current notification through its own
//     handler. The return code is ignored; the record is already being freed.
//     This step runs regardless of the tombstone;
//  3. emit the StopBuilding counterpart when the record carries the pending
//     flag — on every removal path and NOT tombstone-gated;
//  4. release the presentation payload — records carry none today, and the
//     owner's displayed-payload latch has no record payload to point at;
//  5. ONLY for a non-tombstoned record, run the weapon-target-clear helper
//     (TargetCleared). The tombstone is set at removal time on every freed
//     record except the primary segment's front head at that moment; the
//     comparison is always against the front anchor regardless of which
//     segment the record occupied, so rear-segment records are always
//     tombstoned and never emit it.
func (q *Queue) cleanupNode(n *Node) {
	if n == nil {
		return
	}
	u := q.ownerUnit(n)
	if n.DynamicGate&2 != 0 && u != nil { // cancel-notification guard: dynamic gate bit 1 (value 2) [R-ORDER-02 §2]
		if h := DescriptorFor(n.ID).Handler; h != nil {
			_ = h(u, n, 2, q.lastPumpTick)
		} else if q.binding != nil && q.binding.Work != nil && q.binding.Work.CancelNotice != nil {
			// A record another package's state machine runs has no descriptor
			// handler here, so the notification goes to that package's receiver
			// instead. Retail draws no distinction: it invokes the operation
			// handler compiled into the descriptor, and for the three
			// construction rows that handler IS the factory production machine.
			_ = q.binding.Work.CancelNotice(u, n, q.lastPumpTick)
		}
	}
	emitStopBuilding(u, n)
	if q.binding != nil && q.binding.Movement != nil && q.binding.Movement.Release != nil {
		q.binding.Movement.Release(n)
	}
	// "The record destructor returns all three slots (with their targets
	// cleared) for every removed record whose static-mask copy lacks bit 16"
	// [04 R-UNIT-06 §5 part 3] — which is what hands a completed or purged
	// attack's slots back to autonomous acquisition. Bit 16 excludes the rows
	// that never took a slot in the first place: `Activate`, `Deactivate`, the
	// two cloak toggles, the two standing-order rows and `BuildingBuild`, whose
	// removal must not wipe the weapon targets an acquisition put there.
	if n.Flags&FlagTombstone == 0 && n.StaticGate&staticSlotKeeper == 0 {
		clearWeaponBuildTargets(u)
	}
}

func (q *Queue) PurgeUnprotected() {
	if q == nil {
		return
	}
	// [05 "Queue insertion"] non-queued issue purges primary nodes lacking the protected flag
	kept := q.primary[:0]
	for _, n := range q.primary {
		if n.Flags&FlagPurgeSurvivor != 0 {
			kept = append(kept, n)
		} else {
			if n != nil {
				// non-head gets tombstone per [04 §3.3]; secondary always tombstoned via primary-anchor test
				// For purge, use primary head test
				isHead := n == q.primary[0]
				if !isHead {
					n.Flags |= FlagTombstone
				}
				q.cleanupNode(n)
			}
		}
	}
	q.primary = kept
	if len(q.primary) > 0 {
		q.primary[0].Flags |= FlagActive
		for i := 1; i < len(q.primary); i++ {
			q.primary[i].Flags &^= FlagActive
		}
	}
}

// hasLeadingAutoOp reports whether either segment leads with an auto/default
// record. Push tests it first because DropLeadingAutoOps ends by moving the
// active marker back to the front record, which is only correct when the drop
// actually removed the record the marker sat on; an unconditional call would
// reset the insertion point of every ordinary queued add [04 §3.3].
func (q *Queue) hasLeadingAutoOp() bool {
	if q == nil {
		return false
	}
	if len(q.primary) > 0 && q.primary[0].Flags&FlagAutoOp != 0 {
		return true
	}
	return len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0
}

func (q *Queue) DropLeadingAutoOps() {
	if q == nil {
		return
	}
	// [05 "Queue insertion"] issuing any primary order drops leading auto/default-op nodes – leading RUN at front of each segment
	for len(q.primary) > 0 && q.primary[0].Flags&FlagAutoOp != 0 {
		n := q.primary[0]
		q.cleanupNode(n) // head not tombstoned
		q.primary = q.primary[1:]
	}
	for len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0 {
		n := q.secondary[0]
		n.Flags |= FlagTombstone // secondary always tombstoned [04 §3.3]
		q.cleanupNode(n)
		q.secondary = q.secondary[1:]
	}
	if len(q.primary) > 0 {
		q.primary[0].Flags |= FlagActive
		for i := 1; i < len(q.primary); i++ {
			q.primary[i].Flags &^= FlagActive
		}
	}
}

func (q *Queue) Push(id ID, n Node) {
	if q == nil {
		return
	}
	// [P1-I09] dynamic storage: retail has no cap (NEGATIVE-BOUNDED); previous
	// 64/32 caps were inside stock (corpus max 105 raw tokens -> 64 truncated).
	// Now unbounded with OOM guard far outside stock (10000 >> 105).
	if len(q.primary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: primary queue OOM guard (%d), dropping %s", len(q.primary), DescriptorFor(id).Name))
		return
	}
	// "Issuing a front-segment record drops leading auto/default records (those
	// carrying the auto-op flag) wherever they live" [04 §3.3]. Push is the
	// common insertion path every producer enters through, so the drop belongs
	// here and not at each producer: without it the pump's own idle refill
	// (refillIdle below) parks a standing `Standby` / `VTOL_Standby` record at
	// the head, and every later order queues behind a record whose gate no
	// producer in this build can satisfy.
	if q.hasLeadingAutoOp() {
		q.DropLeadingAutoOps()
	}
	node := newNode(id, n) // [04 §3.3][05 "Queue insertion"] C9
	act := findActive(q)
	if act >= 0 {
		pos := act + 1
		q.primary = append(q.primary, nil)
		copy(q.primary[pos+1:], q.primary[pos:])
		q.primary[pos] = node
		// The marker moves to the inserted node, so repeated interface adds
		// queue FIFO directly behind the running order [04 §3.3][05 "Queue
		// insertion"]; the decompile confirms the mark relocates
		// (notes/construction/04_factory_lifecycle.md).
		q.primary[act].Flags &^= FlagActive
		node.Flags |= FlagActive
	} else {
		q.primary = append(q.primary, node)
		if len(q.primary) == 1 {
			node.Flags |= FlagActive
		}
	}
}

// PushHead is the handler-side head insert [04 R-ORD-01 §1]: a record a
// handler spawns goes to the FRONT of the primary segment, so the spawned
// order runs before the spawning one resumes, and the displaced head's
// auto/default-operation flag is inherited by the new head.
//
// It is deliberately not Push. Push is the interface insertion, which places a
// new record immediately AFTER the active marker so that repeated player adds
// queue first-in-first-out behind the running order [04 §3.1]. A spawn is the
// other shape: `Stop`'s `VTOL_LandIfCan`, the kamikaze arrival's
// `SelfDestruct`, the guard's auto-engage attack — all of them run before the
// record that asked for them [04 R-ORD-01 §2, §3].
//
// The spawned record's dynamic gate is the caller's value verbatim. A freshly
// allocated record awaits nothing [04 R-ORD-01 §1], and every documented spawn
// site that does wait on something states its own gate ("gate = 0",
// "gate |= 0xE0"). This used to be a difference from Push, whose records took
// the descriptor's static mask; since WU-18-0 corrected newNode both insertion
// paths produce a record with an empty gate unless the caller asks for one.
//
// TODO(question): the active marker's behavior at a head insert is not
// established — [04 R-ORD-01 §1] describes the link and the auto-flag
// inheritance and says nothing about the insertion-point marker. The marker is
// left on the displaced record here, so a later interface Append still queues
// behind the order that spawned this one rather than between the two. A trace
// of the head-insert helper's writes to the marker word would settle it.
func (q *Queue) PushHead(id ID, n Node) *Node {
	if q == nil {
		return nil
	}
	if len(q.primary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: primary queue OOM guard (%d), dropping spawned %s", len(q.primary), DescriptorFor(id).Name))
		return nil
	}
	node := newNode(id, n)
	node.DynamicGate = n.DynamicGate // verbatim: the descriptor's static mask is insertion metadata, not a wait [04 §3.1]
	node.Flags &^= FlagActive
	if len(q.primary) > 0 {
		node.Flags |= q.primary[0].Flags & FlagAutoOp // inherit the displaced head's auto flag [04 R-ORD-01 §1]
	}
	q.primary = append([]*Node{node}, q.primary...)
	q.ensureSingleActive() // an empty segment's new head takes the marker [04 §3.3]
	return node
}

// appendTail is the patrol-chain append. Unlike Push, it does not insert
// behind the active marker: patrol setup walks its selected segment and adds
// the return waypoint at that segment's tail [04 R-ORD-01 §4][04 R-ORD-02 §4].
// The new record receives no active marker, preserving the current head.
func (q *Queue) appendTail(id ID, n Node) *Node {
	if q == nil {
		return nil
	}
	segment := &q.primary
	if isSecondary(id) {
		segment = &q.secondary
	}
	if len(*segment) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: queue OOM guard (%d), dropping patrol waypoint %s", len(*segment), DescriptorFor(id).Name))
		return nil
	}
	node := newNode(id, n)
	node.Flags &^= FlagActive
	*segment = append(*segment, node)
	if segment == &q.primary && len(*segment) == 1 {
		node.Flags |= FlagActive
	}
	return node
}

func (q *Queue) PushSecondary(id ID, n Node) {
	if q == nil {
		return
	}
	// [P1-I09] dynamic: OOM guard far outside stock (maxSecondary 1 in corpus >> 32 old cap not hit but dynamic is correct retail).
	if len(q.secondary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: secondary queue OOM guard (%d), dropping %s", len(q.secondary), DescriptorFor(id).Name))
		return
	}
	node := newNode(id, n)
	if len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0 {
		node.Flags |= FlagAutoOp // [05 "Queue insertion"] inherit old head's auto flag
	}
	q.secondary = append([]*Node{node}, q.secondary...)
}

func (q *Queue) CoalesceTail(id ID, n Node) {
	if q == nil {
		return
	}
	if isSecondary(id) {
		if len(q.secondary) > 0 {
			tail := q.secondary[len(q.secondary)-1]
			if tail.ID == id && tail.Param1 == n.Param1 { // [04 §3.3][05 "Queue insertion"] tail-only
				add := n.Param2
				if add == 0 {
					add = 1
				}
				// [P2-03] arithmetic overflow: tail Param2 wraps int32 low32 like retail add/sub.
				tail.Param2 += add
				return
			}
		}
		if len(q.secondary) >= OOMGuardQueue {
			q.recordDiagnostic(fmt.Sprintf("orders: secondary coalesce OOM guard (%d), dropping %s", len(q.secondary), DescriptorFor(id).Name))
			return
		}
		q.PushSecondary(id, n)
		return
	}
	if len(q.primary) > 0 {
		tail := q.primary[len(q.primary)-1]
		if tail.ID == id && tail.Param1 == n.Param1 {
			add := n.Param2
			if add == 0 {
				add = 1
			}
			tail.Param2 += add
			return
		}
	}
	// tail-only fallback append [04 §3.3][05 "Queue insertion"] [P1-I09] dynamic with OOM guard
	if len(q.primary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: primary coalesce OOM guard (%d), dropping %s", len(q.primary), DescriptorFor(id).Name))
		return
	}
	node := newNode(id, n)
	q.primary = append(q.primary, node)
	if len(q.primary) == 1 {
		node.Flags |= FlagActive
	}
}

// CancelFrontMost removes the first matching node walking the primary queue
// from the front, and reports whether one went [07 R-P0-11 §6].
//
// This is the queued-order duplicate removal, not a counted subtraction, and
// it differs from CancelTailMost in both directions that matter:
//
//   - It scans front to back. Retail's producer walks the acting unit's
//     primary chain from the head and returns on the first match, so the
//     oldest queued order at a point is the one that goes.
//   - It unlinks the whole node. There is no count decrement here: the
//     traced path frees the matched node outright whatever its count field
//     says. The decrement belongs to the factory producer's negative-count
//     path [R-P0-11 §1], which is a different routine.
//
// The scan is over the primary segment only, which is what retail walks; the
// secondary segment is not searched. Tombstoning follows the same rule as
// every other removal: the node is tombstoned unless it is the list head
// [04 §3.3].
func (q *Queue) CancelFrontMost(match func(Node) bool) bool {
	if q == nil || match == nil {
		return false
	}
	for i := 0; i < len(q.primary); i++ {
		if !match(*q.primary[i]) {
			continue
		}
		n := q.primary[i]
		if i != 0 {
			n.Flags |= FlagTombstone // [04 §3.3]
		}
		q.cleanupNode(n) // [05 "Queue subtraction"]
		copy(q.primary[i:], q.primary[i+1:])
		q.primary = q.primary[:len(q.primary)-1]
		q.ensureSingleActive() // mark moves to the successor [04 §3.3]
		return true
	}
	return false
}

func (q *Queue) CancelTailMost(match func(Node) bool) bool {
	if q == nil || match == nil {
		return false
	}
	for i := len(q.primary) - 1; i >= 0; i-- {
		if match(*q.primary[i]) {
			n := q.primary[i]
			if n.Param2 > 1 {
				n.Param2--
				return true
			}
			isHead := i == 0
			if !isHead {
				n.Flags |= FlagTombstone // [04 §3.3]
			}
			q.cleanupNode(n) // [05 "Queue subtraction"]
			copy(q.primary[i:], q.primary[i+1:])
			q.primary = q.primary[:len(q.primary)-1]
			q.ensureSingleActive() // mark moves to the successor [04 §3.3]
			return true
		}
	}
	for i := len(q.secondary) - 1; i >= 0; i-- {
		if match(*q.secondary[i]) {
			n := q.secondary[i]
			if n.Param2 > 1 {
				n.Param2--
				return true
			}
			n.Flags |= FlagTombstone // secondary always effectively tombstoned [04 §3.3]
			q.cleanupNode(n)
			copy(q.secondary[i:], q.secondary[i+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
			return true
		}
	}
	return false
}

// Pump is the legacy per-queue pump for a single unit [04 §3.3][05 "Queue pumping and result codes"].
// Non-authoritative compatibility wrapper (ON-02): new code should use Pump.PumpUnit per handle.
func (q *Queue) Pump(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	q.lastPumpTick = tick
	// No order-guard write here either; see PumpUnit above and
	// [07 R-WGT-01 §10].
	q.pumpPrimary(u, tick)
	if len(q.primary) > 0 {
		head := q.primary[0]
		sat := (head.Satisfied | u.Pending) & head.DynamicGate // [04 §3.3]
		if head.DynamicGate != 0 && sat == 0 {
			return // blocked primary front prevents ALL secondary dispatch [04 §3.3] C6
		}
	}
	q.pumpSecondary(u, tick)
}

// IdleRefillMission resolves the standing task the primary pump creates for
// an idle unit [04 §3.3, "Closed — the idle-queue refill from
// `defaultmissiontype`"][02 R-KEYS-01 §1].
//
// Established, re-verified 2026-08-31: the definition's 100-byte
// `defaultmissiontype` string is converted through the ORDER DESCRIPTOR
// registry's own case-insensitive name lookup — the same binary search over the
// 25-byte descriptor records that §3.1 sorts, returning the record's table
// index as a byte, and 0 (the reject sentinel) for an empty or unrecognised
// name. The pump's condition is three terms: the front list is empty, the
// owner's controller state is 1 or 2, and the code is non-zero.
//
// The controller state is passed in because this package cannot see the player
// ledger. Values 1 and 2 are the two ACTIVE player states — 1 human, 2 computer
// ([04 R-SPEC-01 §5] identifies 2 as the computer player) — not, as §3.3 used to
// say, "one of the two computer-player states": the same 1-or-2 test gates the
// whole per-unit sweep that runs this pump, with 3 the eliminated/watch state
// whose units are skipped entirely ([04 §8.3, "Closed — compact ground
// controller"]). A human player's idle aircraft is therefore refilled exactly
// like a computer player's, which is what makes the stock
// `defaultmissiontype = VTOL_Standby` reachable at all.
func IdleRefillMission(u *units.Unit, controllerState uint8) (ID, bool) {
	if u == nil || u.Def == nil {
		return 0, false
	}
	if controllerState != 1 && controllerState != 2 {
		return 0, false
	}
	name := u.Def.DefaultMissionType
	if name == "" {
		return 0, false
	}
	id := Lookup(name)
	if id == 0 {
		// The registry comparator is case-insensitive [04 §3.1], so an authored
		// name that differs only in case still resolves.
		for i := range table {
			if i != 0 && strings.EqualFold(table[i].Name, name) {
				id = ID(i)
				break
			}
		}
	}
	if id == 0 {
		return 0, false
	}
	return id, true
}

// controllerStateOf reads the owner's controller state through the queue's
// economy binding. The binding carries the economy service under its stockpile
// name; with none bound there is no ledger and no refill.
func (q *Queue) controllerStateOf(u *units.Unit) uint8 {
	if q == nil || u == nil {
		return 0
	}
	if q.binding == nil {
		return 0
	}
	svc, ok := q.binding.Economy.(*economy.Service)
	if !ok || svc == nil {
		return 0
	}
	owner := int(u.Owner)
	if owner < 0 || owner >= len(svc.Players) {
		return 0
	}
	return svc.Players[owner].ControllerState
}

// refillIdle is the pump's own record creation for an idle unit [04 §3.3]:
// "Idle default-operation records are created by the primary pump itself —
// never by insertion — only when its list is empty ... such a node is allocated
// in non-queued mode, constructed with the auto flag, and head-inserted into
// the list the op's descriptor selects (a secondary-class default op therefore
// lands in the rear segment)." The pump returns after the insert; the record is
// dispatched on the next visit.
//
// This is the seam the standby → `VTOL_LandIfCan` → landing chain hangs from:
// every stock aircraft authors `defaultmissiontype = VTOL_Standby`, so with no
// refill no `VTOL_Standby` record ever existed and a plane that finished a move
// hovered where it stopped forever.
func (q *Queue) refillIdle(u *units.Unit) bool {
	if q == nil {
		return false
	}
	return q.refillIdleWithState(u, q.controllerStateOf(u))
}

// refillIdleWithState is refillIdle with the controller state supplied, so the
// insertion half can be exercised without an economy ledger behind the queue.
func (q *Queue) refillIdleWithState(u *units.Unit, controllerState uint8) bool {
	if q == nil || u == nil || len(q.primary) != 0 {
		return false
	}
	id, ok := IdleRefillMission(u, controllerState)
	if !ok {
		return false
	}
	n := Node{Owner: u.Handle, Deadline: -1}
	if isSecondary(id) {
		q.PushSecondary(id, n)
		if len(q.secondary) > 0 {
			q.secondary[0].Flags |= FlagAutoOp
		}
		return true
	}
	node := q.PushHead(id, n)
	if node == nil {
		return false
	}
	node.Flags |= FlagAutoOp // "constructed with the auto flag" [04 §3.3]
	return true
}

func (q *Queue) pumpPrimary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	q.lastPumpTick = tick
	// No iteration cap here (ORD-02): a handler looping through the continue
	// codes wedges exactly as retail's does [04 §3.3][I11].
	//
	// The walk is HEAD-ONLY [04 R-ORD-01 §10]: after every non-returning result
	// code the loop reloads the front head and applies steps 1 to 4 to it.
	// There is no cursor, because "continue walking" never means "visit the
	// record behind this one" — a record behind the head is reached in a pass
	// only when the head is unlinked (codes 5, 8, 9-not-last, the above-9
	// helper), rotated to the tail (code 6), or replaced by a handler's head
	// insert ([04 R-ORD-01 §1]).
	//
	// Retired (WU-19-73): this loop used to carry `for cursor := 0; cursor <
	// len(q.primary);` with `cursor++` on a hold and `cursor = 0` on every
	// other continuing code. The cursor was WU-19-4's generalisation of
	// [04 R-FAC-02 §4]'s "a *hold* (code 2) does NOT stop the walk — the next
	// record is visited in the same pass", which §4's own 2026-09-02 correction
	// withdraws: the primary pump reloads the head after every code, so a
	// code-2 hold re-runs whatever is at the head, never the record behind it.
	for {
		if len(q.primary) == 0 {
			// §10's first line: "if rec is null: (auto-order spawn for an idle
			// mover, §3.4a) return". The idle refill from
			// `defaultmissiontype` [04 §3.3]. A unit whose primary segment has
			// emptied is handed its standing auto-op record — for every stock
			// aircraft that is `VTOL_Standby`, whose no-cargo arm pushes
			// `VTOL_LandIfCan`, which is how an idle aircraft comes home.
			//
			// This was written, exported and tested but deliberately not called,
			// because the landing-legality predicate `VTOL_LandIfCan` depends on was
			// a placeholder and a factory's first aircraft product landed on its own
			// plant, stalling the plant's build-stance handshake forever. That
			// predicate is now traced and implemented [04 R-AIR-01 §6a], and it
			// refuses a finished building's yard cells, so the product no longer
			// parks on the plant that made it.
			//
			// Corrected (WU-19-73): the refill used to fall through into the
			// walk, so a refilled record was dispatched in the same pass. The
			// refill closure of [04 §3.3] states the opposite outright — "It
			// returns immediately after the insert — the record is dispatched
			// on the unit's next pump visit, never in the same one" — and §10's
			// loop returns on the null head for the same reason.
			q.refillIdle(u)
			return
		}
		n := q.primary[0]
		if n.Deadline != -1 && tick >= uint32(n.Deadline) {
			n.Deadline = -1
			n.Satisfied |= 1 // ordinary deadline expiry raises only bit 0 [04 R-ORD-01 §0]
		}
		desc := DescriptorFor(n.ID)
		// Removed (WU-18-0): a phase-0 pre-dispatch clear used to stand here.
		// When the record was at phase 0 and its dynamic gate still equalled
		// the descriptor's static mask, it wiped the gate, the pending word and
		// the deadline so that the first dispatch was not blocked. It existed
		// only to compensate for newNode seeding the dynamic gate from the
		// static mask (see the correction there), and it is not a clear retail
		// performs. [04 §3.3] gives the pump exactly one gate clear — step 4's,
		// on the record it is about to dispatch, below — and reaches the
		// dispatch-on-first-visit property a different way: the record
		// constructor zeroes the gate and the pending word [04 R-ORD-01 §1], so
		// step 3's block test passes on a fresh record without any pump help.
		// With the constructor corrected the block is unreachable for a fresh
		// record (its gate is 0, so the outer test fails), and for any record
		// that reaches phase 0 with a gate a handler armed — result code 0
		// resets the phase while leaving the gate and deadline standing — it
		// would be actively wrong: it would discard a wait, its deadline, and
		// the pending bits [04 §3.3] steps 1 and 2 require to survive into the
		// satisfied intersection.
		satisfied := (n.Satisfied | u.Pending) & n.DynamicGate // [04 §3.3] C6
		if n.DynamicGate != 0 && satisfied == 0 {
			return // blocked head stalls [04 §3.3] C6
		}
		n.Satisfied &^= satisfied
		u.Pending &^= satisfied
		n.DynamicGate = 0
		handler := desc.Handler
		if (desc.Name != "GetBuilt" && handler == nil) || (desc.Name == "GetBuilt" && q.getBuiltHandler == nil) {
			// What advances a handler-less record is the descriptor's own
			// Driver field, not its spelling.
			switch desc.Driver {
			case DriverMovementRoute:
				// The movement scheduler owns the route lifecycle [04 §7].
				// Synthesize a wait so the pump does not spin and the record is
				// re-dispatched after 30+rand15 [04 §3.3] C3, while the loop's
				// path-submit and movement-integrate drive the route.
				n.DynamicGate = 1
				n.Deadline = int32(tick + 30 + q.randBelow15()) // [04 §3.3][I4]
				n.MoveState = MoveEnRoute
				return
			case DriverExternalMachine:
				// Another subsystem runs this record from its own per-unit step
				// and owns its phase, gate and deadline. The pump must leave
				// every one of those fields alone: writing a result code over
				// them is writing over a live state machine.
				return
			}
			// Retired (WU-19-4): this arm carried an accepted-blocked marker for descriptors
			// that had no handler and no driver. The census is now zero —
			// TestHandlersAreInstalledBeforeTheFirstPump walks the whole table
			// and fails on any named descriptor that is neither `GetBuilt`
			// (bound per queue by the construction service, [04 R-FAC-02 §4])
			// nor driven by another subsystem — so this is a guard against a
			// table that regresses, not a placeholder for behavior we owe.
			//
			// It stays because the alternative to a guard is a jam. Retail has
			// a handler for every named descriptor, so there is no retail
			// behavior to clone here; what the pump owes is an outcome that is
			// bounded and visible. The record is parked with the contract's own
			// wait, code 3 — lowest gate bit, deadline `tick + 30 + random
			// below 15` [04 §3.3] — which stops the walk, keeps the record the
			// player still owns, and re-diagnoses once per wait instead of once
			// per tick. The alternatives are all worse: codes 0 and 1
			// re-dispatch the record from the head and spin, corrupting the
			// phase on the way; 2, 4 and 6 walk past it every tick with no
			// diagnostic; and 5, 7, 8 and 9 free it, turning a missing handler
			// into a silently dropped order.
			q.recordDiagnostic(fmt.Sprintf("orders: no handler for %s, parked for 30..44 ticks", desc.Name))
			q.applyPrimaryResultCode(n, 3, tick)
			return
		}
		var code Code
		if desc.Name == "GetBuilt" && q.getBuiltHandler != nil {
			// GetBuilt is not a descriptor handler: the construction service
			// that owns the product lifecycle binds it per queue
			// [04 R-FAC-02 §4]. Its signature is the descriptor Handler's, and
			// the satisfied set it receives is the same one computed above —
			// `GetBuilt`'s gate is `0x8001` and its phase-2 body reads the set
			// to choose its arm: `0x8000` holds for another 30 ticks, bit 0
			// alone decays [04 R-ORD-01 §11]. The result-code handling below is
			// its own, so this stays a named case.
			code = q.getBuiltHandler(u, n, satisfied, tick)
		} else {
			// Removed (WU-18-7): three by-name cases stood here, calling
			// `beCarriedHandlerAtTick`, `stopHandlerAtTick` and
			// `parkHandlerAtTick` so those three bodies could see the tick that
			// the Handler signature did not carry. The tick is now the
			// handler's fourth argument, so every descriptor gets it the same
			// way — which is what [04 R-ORD-01 §1] describes: the deadline
			// setter stores "current tick + n", and any row with a deadline
			// needs the tick to form one.
			code = handler(u, n, satisfied, tick)
		}
		// [04 §3.3] codes 2 and 4 "continue walking unchanged". Continuing is
		// the head reload of [04 R-ORD-01 §10], so a hold re-runs the record
		// that is at the head *after* the handler returned — the same record
		// when the handler only armed a gate (the reload then finds it blocked
		// and the pass ends), or the record a handler head-inserted or
		// re-identified in its place.
		//
		// Corrected (WU-19-73). WU-19-4 made this arm `cursor++` for every row,
		// on [04 R-FAC-02 §4]'s "The primary pump stops its walk at the first
		// record whose gate is non-zero and whose satisfied set is empty; a
		// *hold* (code 2) does NOT stop the walk — the next record is visited
		// in the same pass." That sentence is withdrawn by §4's own 2026-09-02
		// correction — it was the reasoning behind the retracted `t0 + 301` pad
		// dwell — and [04 R-ORD-01 §10] gives the loop in full: nothing ever
		// resumes at the *next* record. §10 also names the invariant this arm
		// now relies on: every handler returning 2 or 4 has first armed a gate
		// on the record or changed the segment head, or the loop would not
		// terminate. `BeCarried` phase 1 (deadline 10, code 2, every visit) is
		// its canonical example.
		if code == 2 || code == 4 {
			continue
		}
		if desc.Name == "GetBuilt" {
			// The construction service's own result handling. Under the
			// head-only walk this record is always the front head, so the
			// removal never tombstones ([R-ORDER-02 §2]) and the pass always
			// continues into the record the unlink exposed — which is how the
			// rally or `Park` that `handleGetBuiltOrder` appended is dispatched
			// in the same pass [04 R-FAC-02 §4].
			if code == 5 || code == 8 {
				q.RemovePrimaryNode(n, false)
				continue
			}
			return
		}
		if !q.applyPrimaryResultCode(n, code, tick) {
			return
		}
	}
}

// indexOfPrimary locates a record in the primary segment by identity, or -1.
func (q *Queue) indexOfPrimary(n *Node) int {
	for i, p := range q.primary {
		if p == n {
			return i
		}
	}
	return -1
}

// spliceOutPrimary unlinks n from the primary segment by identity and reports
// whether it was still linked. It takes no index from the caller, and that is
// the point: it is called AFTER the removal cleanup, which can re-enter the
// queue.
//
// Every removal path runs cleanupNode, and cleanupNode sends the cancel
// notification of [R-ORDER-02 §2] — "when the record's dynamic gate mask still
// holds bit 1 (value 2) at removal, invoke the operation handler with that
// cancel-notification mask". That handler is arbitrary work: for the three
// construction rows it is the production machine, whose cancel-current epilogue
// removes the head itself; for a descriptor row it may head-insert a spawned
// record ([04 R-ORD-01 §1]) or cancel the whole queue (code 7). So the segment
// the caller measured before the cleanup is not the segment that exists after
// it, and an index taken before is stale.
//
// In retail the queue is a linked list and a record's destructor unlinks the
// node itself — unlinking a node that the notification already unlinked is a
// no-op there, because its links are gone. Our segment is a slice, which has no
// such property: the same double removal drops whichever record has since taken
// slot 0, or runs off the end of an emptied segment. Re-locating the record is
// how the slice keeps the linked list's semantics; it is not a bounds guard,
// and it removes exactly the record the caller named or nothing at all.
func (q *Queue) spliceOutPrimary(n *Node) bool {
	idx := q.indexOfPrimary(n)
	if idx < 0 {
		return false // the cancel notification already unlinked it
	}
	copy(q.primary[idx:], q.primary[idx+1:])
	q.primary = q.primary[:len(q.primary)-1]
	return true
}

// unlinkPrimary removes the dispatched record from the primary segment and
// runs the removal cleanup [05 "Queue subtraction"]. The record is found by
// identity rather than assumed to be at the front: a handler that head-inserts
// a spawned record [04 R-ORD-01 §1] is no longer the front record when its own
// result code is applied, and freeing slot 0 there would free the spawned
// order instead of the one that finished.
//
// The tombstone follows [R-ORDER-02 §2]: it is set on every freed record
// except the one that is the primary segment's front head at that moment, and
// it is what suppresses that record's weapon-target-clear notification. The
// tombstone decision is made before the cleanup, because it is about where the
// record stood when the removal was decided; the unlink is made after it, by
// identity, because the cleanup's cancel notification can move or remove
// records (see spliceOutPrimary).
func (q *Queue) unlinkPrimary(n *Node) {
	idx := q.indexOfPrimary(n)
	if idx < 0 {
		return
	}
	if idx != 0 {
		n.Flags |= FlagTombstone
	}
	q.cleanupNode(n)
	q.spliceOutPrimary(n)
	q.ensureSingleActive() // mark moves to the successor [04 §3.3]
}

// applyPrimaryResultCode is the PRIMARY result-code table [04 §3.3] C7: it
// maps the handler's return code to queue effects for the head record n.
// Deliberately distinct from applySecondaryResultCode — the segments share
// the code values but not the effects, and re-merging them reintroduces the
// ORD-03 mismatches (secondary code 9 would re-arm, secondary 6/7 would
// tail-yield or cancel-all). Primary specifics here: code 6 rotates to the
// segment tail, code 7 is the exclusive whole-queue cancel, code 9's
// last-record arm re-arms with RNG(30) [R-P0-01].
//
// Returns false when the walk stops for this pump. §3.3 names exactly one stop
// — step 3's gate test, applied to the head the loop reloads
// ([04 R-ORD-01 §10]) — and the codes below that return false do so because
// the section's consequence list says the pass ends there: code 7 and the
// above-9 helper return outright, and code 9's last-record arm and code 3 arm a
// wait on the head, which the reload's gate test then refuses. Returning false
// on those two is the reload written out.
func (q *Queue) applyPrimaryResultCode(n *Node, code Code, tick uint32) bool {
	switch code {
	case 0:
		n.Phase = 0 // [04 §3.3]
	case 1:
		n.Phase++ // [04 §3.3]
	case 2, 4:
		// [04 §3.3] continue walking unchanged
	case 3:
		n.DynamicGate = 1                               // [04 §3.3] lowest gate bit
		n.Deadline = int32(tick + 30 + q.randBelow15()) // [04 §3.3][I4] wait 30..44; the only arm drawing RNG(15)
		// Closed (WU-19-73) by [04 R-ORD-01 §10], which settles the question
		// this arm carried as a TODO: the loop reloads the FRONT HEAD after
		// every non-returning code and applies the gate test to it. This arm
		// just armed the head's own gate with a deadline 30 or more ticks out,
		// so the reload finds it blocked and the pass ends. `return false` is
		// therefore not merely outcome-preserving — it is the rule, and the
		// pump reaches it one step earlier than retail does (retail reloads and
		// re-gates; we return, which is the same observable pass).
		return false
	case 5, 8:
		q.unlinkPrimary(n) // [04 §3.3][05 "Queue subtraction"]
	case 6:
		idx := q.indexOfPrimary(n)
		if idx < 0 {
			return false
		}
		copy(q.primary[idx:], q.primary[idx+1:])
		q.primary = q.primary[:len(q.primary)-1]
		n.Flags &^= FlagActive
		q.primary = append(q.primary, n) // move to segment tail and continue [04 §3.3]
		q.ensureSingleActive()           // exactly one marker remains
	case 7:
		q.cancelAll() // [04 §3.3] free every record on both segments and return; whole-queue cancel is exclusively primary code 7
		return false
	case 9:
		n.Flags |= FlagRetryMark // [04 §3.3][R-ORDER-02 §2] completion flag; its reader is the goal installer of [04 R-PATH-01 §8] step 5.3
		// "Last" is having no record after it in the segment, which is not the
		// same as being the only record: a handler that head-inserts a spawned
		// record [04 R-ORD-01 §1] leaves itself behind that record and can
		// still be the tail.
		if idx := q.indexOfPrimary(n); idx >= 0 && idx == len(q.primary)-1 {
			// [R-P0-01][04 §3.3] last record re-arms: phase reset, wait
			// 30..59 — the distinct RNG(30) arm, not code 3's RNG(15).
			n.Phase = 0
			n.DynamicGate = 1
			n.Deadline = int32(tick + 30 + q.randBelow30())
			return false
		}
		q.unlinkPrimary(n) // [04 §3.3] otherwise unlink and free
	default:
		if code > 9 {
			// [04 §3.3] above 9: single-node expiry helper — unlink, clean,
			// free, and return; no draw, no whole-queue cancel [P0-08].
			// Whole-queue cancel is exclusively code 7 [P0-08] A09.
			q.unlinkPrimary(n)
			return false
		}
		return false
	}
	return true
}

func (q *Queue) pumpSecondary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	q.lastPumpTick = tick
	for idx := 0; idx < len(q.secondary); {
		n := q.secondary[idx]
		// Removed (WU-18-0): a per-name normalization stood here, clearing the
		// dynamic gate of a deadline-less `BuildWeapon` record because fresh
		// ones were born carrying their descriptor's static mask (0xc0140) and
		// a rear record with a gate never dispatches. It was the rear-segment
		// half of the same conflation newNode has now dropped, and it was
		// narrower than the defect and wider than the fix: it named one
		// descriptor, and it would equally have cleared a gate the stockpile
		// handler armed without a deadline. A fresh rear record now arrives
		// with an empty gate [04 R-ORD-01 §1], so it is ready by construction.
		//
		// [R-ORDER-02 §1] A rear-segment record is dispatched only when its
		// gate mask is empty or its deadline has arrived; the deadline compare
		// is unsigned, so the -1 sentinel (0xffffffff) reads as not-due.
		deadlineArrived := uint32(n.Deadline) <= tick
		shouldDispatch := n.DynamicGate == 0 || deadlineArrived
		if !shouldDispatch {
			idx++
			continue
		}
		if deadlineArrived {
			n.Deadline = -1 // clear; no expiry bit is set — an arrived deadline satisfies nothing here
		}
		n.DynamicGate = 0
		// [R-ORDER-02 §1] The secondary pump never delivers satisfied bits:
		// the handler is invoked with an EMPTY satisfied set — no expiry bit,
		// no satisfied-word read, and no capability-word consumption. Rear
		// records run purely on their own deadlines; movement or wake bits can
		// never drive them.
		// The per-queue published tick [RS-P0-018]. Every handler now takes the
		// tick as its fourth argument (WU-18-7), so no handler reads this field
		// for its deadlines any more; it stays because it is the queue's own
		// record of the tick it was last pumped at, and the construction service
		// transfers it across a queue rebind [04 R-FAC-02 §4].
		q.secondaryTick = tick
		handler := DescriptorFor(n.ID).Handler
		if handler == nil {
			// The same missing-handler guard as the primary walk above (whose
			// comment carries the reasoning and the census), through the
			// secondary table's code 3 — which parks this record for 30..44
			// ticks and, unlike the primary, continues the walk, so one
			// unrunnable rear-segment record does not hide the records behind
			// it [04 §3.3].
			q.recordDiagnostic(fmt.Sprintf("orders: nil handler for secondary %s, parked for 30..44 ticks", DescriptorFor(n.ID).Name))
			advance, walking := q.applySecondaryResultCode(n, 3, tick)
			if !walking {
				return
			}
			idx += advance
			continue
		}
		code := handler(u, n, 0, tick)
		advance, walking := q.applySecondaryResultCode(n, code, tick)
		if !walking {
			return // codes 6 and 7: remove the single record and return [04 §3.3] C8
		}
		idx += advance
	}
}

// applySecondaryResultCode is the SECONDARY result-code table [04 §3.3] C8.
// Deliberately distinct from applyPrimaryResultCode (ORD-03): codes 6 and 7
// remove the single record and return — no tail-yield, no cancel-all — while
// codes 5, 8, 9 and above 9 are plain unlink-and-free removals that continue
// the front-to-back walk. Secondary code 9 sets the completion flag and then
// plainly unlinks and frees with NO re-arm and NO draw, regardless of whether
// the record is last or first [04 §3.3] "Audit note — completion-wait
// ranges"; the above-9 expiry delegate never draws either.
// Returns the index advance (0 when the record was removed) and whether the
// walk continues.
func (q *Queue) applySecondaryResultCode(n *Node, code Code, tick uint32) (advance int, walking bool) {
	switch code {
	case 0:
		n.Phase = 0 // [04 §3.3]
		return 1, true
	case 1:
		n.Phase++ // [04 §3.3]
		return 1, true
	case 2, 4:
		return 1, true // [04 §3.3] continue unchanged
	case 3:
		n.DynamicGate = 1                               // [04 §3.3] lowest gate bit
		n.Deadline = int32(tick + 30 + q.randBelow15()) // [04 §3.3][I4] wait 30..44; the only arm drawing RNG(15)
		return 1, true
	case 5, 8:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 plain unlink+free, walk continues
		return 0, true
	case 6:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 remove the single record and return; no tail-yield
		return 0, false
	case 7:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 remove the single record and return; no cancel-all
		return 0, false
	case 9:
		n.Flags |= FlagRetryMark // [04 §3.3][R-ORDER-02 §2] completion flag; its reader is the goal installer of [04 R-PATH-01 §8] step 5.3
		// [04 §3.3] plain unlink+free — no re-arm, no draw, regardless of
		// last/first position (the primary-only last-record re-arm [R-P0-01]
		// does not apply to the secondary pump).
		q.removeSecondaryRecord(n)
		return 0, true
	default:
		if code > 9 {
			// [04 §3.3] C8 expiry delegate: plain unlink+free, no draw, and
			// the walk continues like the other plain removals.
			q.removeSecondaryRecord(n)
			return 0, true
		}
		return 0, false
	}
}

// removeSecondaryRecord unlinks and frees one secondary record [04 §3.3]
// [05 "Queue subtraction"]: the record is always tombstoned because the
// tombstone test compares against the front anchor regardless of segment, so
// BuildWeapon/SelfDestruct removals never emit the weapon-target-clear
// notification.
func (q *Queue) removeSecondaryRecord(n *Node) {
	n.Flags |= FlagTombstone
	q.cleanupNode(n)
	for i, m := range q.secondary {
		if m == n {
			copy(q.secondary[i:], q.secondary[i+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
			return
		}
	}
}

// Mobile-build blocked-area retry budget [R-ORDER-02 §1]. The record's third
// parameter is the blocked-area retry counter [04 §3.2] (the record's
// progress field, reused; the handler's setup path zeroes it, so a fresh or
// re-armed record starts the budget at zero). On a blocked approach visit the
// mobile-build handler notifies "Waiting for target area to clear",
// increments the counter, and waits EXACTLY 30 ticks — a fixed wait with no
// random draw — while the counter is at most 10; the first blocked visit
// whose counter is already above 10 notifies "Target area was blocked" and
// abandons (code 8, remove). Eleven 30-tick waits, then give-up on visit
// twelve.
const (
	// MobileBuildBlockedWaitTicks is the fixed blocked-visit wait; the traced
	// arm draws no random value, unlike the pump's code-3 wait.
	MobileBuildBlockedWaitTicks uint32 = 30 // [R-ORDER-02 §1]
	// MobileBuildBlockedGiveUpAbove is the counter value above which the next
	// blocked visit gives up: waits happen while the counter is at most 10.
	MobileBuildBlockedGiveUpAbove uint32 = 10 // [R-ORDER-02 §1]
)

// Retail notifies these strings verbatim as the blocked-area status text
// [R-ORDER-02 §1].
const (
	MobileBuildWaitingText = "Waiting for target area to clear"
	MobileBuildBlockedText = "Target area was blocked"
	// MobileBuildUnreachableText is the approach-failure text of the same row
	// [04 R-ORD-01 §5, the `MobileBuild` row].
	MobileBuildUnreachableText = "I can't reach the construction site"
)

// MobileBuildUnreachableVisit is the approach-failure arm of the mobile-build
// row [04 R-ORD-01 §5]: "Phase 1: when satisfied has `0x40`, run the reach test
// against the product footprint; out of reach → status 7 `I can't reach the
// construction site`, abandon." `0x40` is the route publisher's "cannot get
// there" notification — an empty publication raised while the mover is not at
// the goal [04 R-PATH-01 §7][04 R-COLL-01 §6] — and it is the ONLY thing that
// ends an approach the search cannot satisfy: the follower re-requests every 60
// ticks forever with no retry ceiling [04 R-MOV-01 §7], so a record that
// ignores the bit walks nowhere and waits for a wake that will never differ.
//
// The caller supplies the reach verdict, because the reach test measures
// against the product's footprint and internal/construction owns the product
// definition; it notifies the returned text through its own status surface and
// applies code 8 (abandon: unlink and free the single record, [04 §3.3]).
// `satisfied` is the record's accumulated pending word. With the bit absent, or
// the mover already in reach, the caller keeps its approach: code 2, continue
// unchanged.
func MobileBuildUnreachableVisit(satisfied uint32, outOfReach bool) (statusText string, code Code) {
	if satisfied&pendNoRoute == 0 || !outOfReach {
		return "", 2
	}
	return MobileBuildUnreachableText, 8
}

// MobileBuildBlockedVisit is one blocked-visit step of the mobile-build
// budget for the record n at tick. It returns the verbatim status text to
// notify and the pump result code the caller returns: code 2 (continue) with
// the wait armed — lowest gate bit plus deadline tick+30 exactly, the pump's
// blocked-head stall re-dispatching on deadline arrival — or code 8
// (abandon/remove) once the counter has passed its budget. The counter lives
// in n.Param3 [04 §3.2]; the caller notifies the returned text through its
// own status surface. A nil record gives up without touching anything.
func MobileBuildBlockedVisit(n *Node, tick uint32) (statusText string, code Code) {
	if n == nil {
		return MobileBuildBlockedText, 8
	}
	if n.Param3 > MobileBuildBlockedGiveUpAbove {
		return MobileBuildBlockedText, 8
	}
	n.Param3++
	// Arm the exact 30-tick wait: lowest gate bit stalls the head, and the
	// pump's deadline expiry sets that bit as satisfied on arrival [04 §3.3].
	// The wait draws no random value [R-ORDER-02 §1].
	n.DynamicGate = 1
	n.Deadline = int32(tick + MobileBuildBlockedWaitTicks)
	return MobileBuildWaitingText, 2
}

// RemoveHead removes the primary segment's front record with the ordinary
// removal cleanup.
//
// Fixed (WU-19-73): this used to drop slot 0 positionally — `q.primary =
// q.primary[1:]` — after the cleanup had already run. It crashed a `Coast to
// Coast` skirmish at tick 3865 with `slice bounds out of range [1:0]`: the head
// was a `MobileBuild` record carrying gate bit 1 (gate `0xa`), so the cleanup
// sent [R-ORDER-02 §2]'s cancel notification, which for a construction row is
// the production machine's cancel-current — and cancel-current's own epilogue
// removes the head. The segment was already empty when the positional re-slice
// ran. On a queue with a record behind the head the same double removal would
// have silently dropped that record instead of crashing. The unlink now goes
// through spliceOutPrimary, which re-locates the record by identity after the
// cleanup and removes nothing when the notification already removed it.
func (q *Queue) RemoveHead() *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	n := q.primary[0]
	q.cleanupNode(n)
	q.spliceOutPrimary(n)
	q.ensureSingleActive()
	if n != nil {
		n.MoveState = MoveArrived
	}
	return n
}

func (q *Queue) Head() *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	return q.primary[0]
}

func QueueForUnit(u *units.Unit) *Queue {
	if u == nil {
		return nil
	}
	if q, ok := u.Orders.(*Queue); ok && q != nil {
		return q
	}
	q := &Queue{}
	u.Orders = q
	return q
}

// QueueOfUnit returns the unit's existing order queue without creating one.
// Read-only paths such as frame publication must use this lookup so observing
// a unit cannot mutate its authoritative order state [03 §1][04 §3.3].
func QueueOfUnit(u *units.Unit) *Queue {
	if u == nil {
		return nil
	}
	q, _ := u.Orders.(*Queue)
	return q
}

func BindQueue(u *units.Unit, q *Queue) {
	if u == nil {
		return
	}
	if q != nil {
		prior := QueueOfUnit(u)
		if prior != nil && prior != q && prior.binding != nil {
			prior.releaseBoundGoals()
		}
	}
	if q != nil && q.binding == nil {
		// Queue replacement is a lifecycle boundary: preserve the session
		// context from the replaced queue unless the producer supplied a new
		// concrete binding explicitly [P0-00 A.1][04 §3.3].
		if prior := QueueOfUnit(u); prior != nil && prior.binding != nil {
			q.SetBinding(prior.binding)
		}
	}
	u.Orders = q
}

// releaseBoundGoals is the queue-replacement lifecycle edge. It only releases
// movement payloads; order-record cleanup remains the removal path's owner.
// Movement checks node identity, so a late cleanup cannot detach a successor.
func (q *Queue) releaseBoundGoals() {
	if q == nil || q.binding == nil || q.binding.Movement == nil || q.binding.Movement.Release == nil {
		return
	}
	for _, n := range q.primary {
		if n != nil {
			q.binding.Movement.Release(n)
		}
	}
	for _, n := range q.secondary {
		if n != nil {
			q.binding.Movement.Release(n)
		}
	}
}

// RemovePrimaryNode removes one primary node in place, preserving queue
// identity and the queue's concrete binding (including Economy), plus its
// secondary tick and diagnostics. Callers that rebuilt the segment into a
// fresh Queue silently dropped those hooks, so successor
// orders lost target lookup and stockpile admission after a construction
// removal.
//
// Removal follows the established subtraction order [04 §3.3][05 "Queue
// subtraction"]: the node is marked per tombstone rules, cleanup runs exactly
// once, the segment is spliced, and the active marker is handed to the
// successor.
//
// tombstone selects the marker applied before cleanup. Retail exempts the
// primary head from the tombstone [04 §3.3]; callers that must preserve an
// older unconditional marking pass true explicitly.
//
// The node is matched by pointer identity; when that fails the head is
// accepted if it carries the same order ID and first parameter. Returns the
// removed node, or nil when nothing matched.
func (q *Queue) RemovePrimaryNode(node *Node, tombstone bool) *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	idx := -1
	for i, n := range q.primary {
		if n == node {
			idx = i
			break
		}
	}
	if idx == -1 {
		head := q.primary[0]
		if node != nil && head != nil && head.ID == node.ID && head.Param1 == node.Param1 {
			idx = 0
		} else {
			return nil
		}
	}
	removed := q.primary[idx]
	if removed != nil {
		if tombstone || idx != 0 {
			removed.Flags |= FlagTombstone
		}
		removed.Flags &^= FlagActive
		q.cleanupNode(removed)
	}
	// By identity after the cleanup, not by the index taken before it: the
	// cleanup's cancel notification can unlink this record itself or insert
	// ahead of it (see spliceOutPrimary).
	q.spliceOutPrimary(removed)
	q.ensureSingleActive()
	return removed
}

// CancelAll is the exported entry to result code 7's whole-queue cancel
// [04 §3.3][05 "Queue pumping and result codes"]. It preserves queue identity
// and every queue-owned service binding; callers must never express a cancel
// by rebinding a fresh Queue to the unit.
func (q *Queue) CancelAll() {
	if q == nil {
		return
	}
	q.cancelAll()
}
