// The session-owned context a queue carries, and the adapter views the
// handlers reach the rest of the engine through [04 §3.3][04 §3.4][06 §11.1].
// Moved out of pump.go by CL-5 with no other change.

package orders

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

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
	// Damage delivers locally produced packets to the session's common intake [06 §9.1].
	Damage func(uint32, combat.DamageInput) combat.DamageResult

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

// PlaceRequest names one live unit and the world position a handler is
// committing it to, for the direct position commit of [04 R-COLL-01 §4]. It
// carries no radius, no node and no mode: the setter writes the position it is
// given and derives the cell pair and plane from the unit's own committed
// mover mode.
type PlaceRequest struct {
	Unit    pool.Handle
	X, Y, Z numeric.Fixed
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

	// PlaceUnit is the direct position commit — retail's "carried-position
	// setter", the occupancy commit's success branch without the validator
	// [04 R-COLL-01 §4]. Same cell and mode writes XYZ only; otherwise it
	// clears the old footprint, writes XYZ, the cell pair and the mode, stamps
	// the new footprint under the overlap protocol, and publishes LOS.
	//
	// It belongs on THIS adapter rather than on WorldQueryAdapter because the
	// two things the setter touches — the occupancy planes' cached cell pair
	// and the class-layer restamp family [04 R-MOV-03 §3] — are owned by the
	// same package this adapter already fronts. WorldQueryAdapter is a
	// read-only query port: nothing on it writes simulation state, and routing
	// an occupancy write through it would give the world queries a second
	// owner.
	//
	// The one order-facing caller is the `Teleport` row's per-unit placement
	// [04 R-ORD-01 §2]. Like every callback here it returns false when the
	// owner cannot accept, and like every callback here it — not the handler —
	// owns whatever the movement boundary must republish afterwards.
	PlaceUnit func(PlaceRequest) bool
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

	// Teleport is the `Teleport` row's per-moved-unit effect: the strip-5
	// flame-stream container spawned at the moved unit's OLD position, laying
	// one animated segment every 10 ticks between the old position and the
	// displaced one for its 30-tick life [04 R-ORD-01 §2][03 R-LAYER §4].
	//
	// The row emits it once per moved unit and BEFORE that unit's position
	// commit, which is why the endpoints are arguments rather than something
	// the presentation edge could re-derive from the unit afterwards.
	//
	// [03 R-LAYER §4] is also the retraction of the older reading that made
	// strip 5 a "flame-weapon area scan": the teleport handler is strip 5's
	// only producer besides burning-feature smoke, so no combat path competes
	// for this callback.
	Teleport func(moved *units.Unit, fromX, fromY, fromZ, toX, toY, toZ numeric.Fixed) bool
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
	if b.Damage == nil {
		return fmt.Errorf("orders: incomplete damage intake service")
	}
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
