// Package movement — per-unit integration glue [04 §8.1][04 §8.2][04 §10.1][04 §7.1][04 §7.3].
//
// Integrate.go is the per-unit integration glue that owns FlightState/SteerState/CollisionState
// surfaces for gate-2 and later phases. It bridges units.World, orders queues, the path.Scheduler,
// and the ground/flight integrators.
//
// The System type is the composition root that the kernel's movement window
// calls each tick. Flight integration remains available through the explicit
// per-unit helper below.
//
// Wiring contract [task]:
//   - Per selected unit issuing Move_Ground: submit path.Request via Scheduler (PointGoal at click cell, radius 0).
//   - Scheduler.Tick in the kernel's orders/path window each tick with injected SearchFunc bound to path.Search + profile passability.
//   - On publication: order node carries Route (Route.Publish); per tick follow it: Prune, SteerState.UpdateHeading + Integrate, CollisionState.TryFastPath/ApplyBlocked against OccupancyGrid.
//
// Citations: [04 §7.1] C1 lattice, [04 §7.2] C8 point goal, [04 §7.3] C14–C15, [04 §8.1] C20 C21, [04 §8.2] C23 C24.
package movement

import (
	"math/bits"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// System is the per-unit integration glue. It owns the three mover surfaces per unit
// plus the route, and the scheduler/grid/terrain it was bound to at construction.
// One System is created per authoritative session and is the sole writer
// of per-unit movement state for that world.
type System struct {
	Terrain *world.Terrain

	// Fallback is the profile used by a unit whose definition names no
	// movement class. Aircraft and buildings legitimately have none: they are
	// not classified against the ground lattice at all. It is NOT a permissive
	// stand-in for a class that failed to resolve — that is a load error, and
	// EnsureUnit records it in Unresolved.
	Fallback Profile

	// Classes is the compiled movement-class table, keyed as
	// content.CanonicalKey(name). Each unit resolves its own profile from it:
	// passability, path bias, collision footprint and occupancy stamps are
	// per-unit identity, not session-wide [04 §6.1] [04 §7.1].
	Classes map[string]*content.MovementClass

	// Unresolved names the movement classes a unit definition asked for and
	// the table did not have, for load diagnostics. Order is first-seen.
	Unresolved []string

	Grid         *OccupancyGrid
	Scheduler    *path.Scheduler
	Routes       map[pool.Handle]*Route
	Steers       map[pool.Handle]*SteerState
	Collisions   map[pool.Handle]*CollisionState
	Flights      map[pool.Handle]*FlightState
	profiles     map[pool.Handle]Profile // per-unit resolved movement profile [04 §6.1]
	profileNames map[pool.Handle]string  // per-unit canonical class key the profile resolved from [04 §6.1]; lookup-only [I1]
	sessions     []*path.Session         // deterministic slice indexed by handle [04 §7.3] C11 C12 budget-honoring sessions
	prevMoveTier map[pool.Handle]int     // cached mover tier per unit for MoveRate edge emission [04 §5.2][GAP T15] C18
	prevSFXBand  map[pool.Handle]int     // cached setSFXoccupy band per unit for edge emission [04 §5.2][GAP T15] C17 C18

	// world is the units world bound via BindWorld (or via Tick for legacy path).
	// StepUnit needs it to fetch the *units.Unit for a handle without passing
	// the world on every per-unit call, so the caller can invoke StepUnit
	// inside its own slot visit [04 §1.1] sweep order.
	world *units.World

	// layerRegistry is the per-class stamped passability layer registry
	// [04 §6.1 R-DOC04-B]. It is constructed from the terrain, the occupancy
	// grid, the bound unit world and this System's own committed-anchor
	// adapter, and is created at first use rather than in NewSystem because
	// the request revision pass needs the unit world [04 §6.1 R-DOC04-B],
	// which production binds via BindWorld before the first search or
	// occupancy commit.
	layerRegistry *ClassLayers

	// per-tick shared indexing built deterministically ONCE in BeginTick [04 §8.2] C22.
	// StepUnit consumes it; EndTick clears it.
	tickStarted bool
	tick        uint32
	tickCarried map[pool.Handle]struct{}

	pathFailures map[pool.Handle]PathFailure // last non-success publish per handle [04 §7.3] C12 [P0-08][P0-12]

	// activeOrders is the single path activation boundary.  A route belongs to
	// the order node that was active when its request was submitted, not merely
	// to a unit handle.  Queue heads are stable pointers for their lifetime;
	// keeping that identity here lets publication reject a result for a stale
	// head after a replace/purge in the same tick.  Direct movement callers do
	// not bind an order and retain the legacy SubmitMove surface used by the
	// movement package fixtures.
	activeOrders   map[pool.Handle]*activeMove
	nextActivation uint64
	arrivalHandles map[pool.Handle]*arrivalHandle // per-unit Move_Ground arrival handle [R-P0-01]
	moveGoals      map[pool.Handle]*moveGoal      // per-unit movement-goal handle [04 §8.3][04 §7.4]
}

type activeMove struct {
	order *orders.Node
	token uint64
}

type arrivalHandle struct {
	order    *orders.Node
	goalX    int32 // goal cells (bias-corrected) [R-P0-01]
	goalZ    int32
	threshSq int32 // floor(radiusParam/16)² inclusive [R-P0-01]; 0 for ground moves
}

// localSteeringThresholdSquared is only the near-waypoint brake/steering
// threshold. It is not final Move_Ground completion; that tolerance is recovered
// in R-P0-01 and lives in the arrival handle (threshold²) below. [04 §3.5][R-P0-01]
const localSteeringThresholdSquared uint64 = uint64(2*65536) * uint64(2*65536)

// [R-P0-01] Move_Ground arrival handshake constants.
const (
	arrivalSatisfiedBit uint32 = 0x20 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	arrivalGateMask     uint32 = 0xE0 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
)

// These are explicit Nanolathe land-skirmish retry policy values. Retail's
// dynamic-blocker retry cadence/count remain unresolved [R-P1-10]; they are not
// presented as recovered executable constants.
const (
	landPathFailureRetryInterval = 30
	landPathFailureMaxRetries    = 1
)

type PathFailure struct {
	Status    path.Status
	Tick      uint32
	Retries   int
	NextRetry uint32
}

// thresholdSqFromRadius computes the goal-handle threshold² = floor(radiusParam/16)² [R-P0-01].
// The movement-goal handle stores radiusParam and precomputes floor(radiusParam/16)²
// (signed arithmetic shift with sign correction, then square). Negative radii clamp
// the shift to zero.
func thresholdSqFromRadius(radiusParam int32) int32 {
	rp := int64(radiusParam)
	t := rp / 16 // arithmetic floor for non-negative; radii are non-negative in practice
	return int32(t * t)
}

// goalRadiusParamFor returns the movement-goal handle radius parameter for a
// move-family order head [R-P0-01 corrected]. The traced Move_Ground handler
// binds the goal handle with the node's radius field plus 4; the field is 0
// for HUD/AI-issued point moves, so the ground arrival radius is 4 and the
// handle threshold is floor(4/16)² = 0 — the order completes only when the
// committed tile equals the goal cell. The VTOL_Move handler instead passes
// the definition's kamikaze distance clamped to at least 16. Patrol
// substates bind other radii (halved/zero); their exact per-substate values are
// not recovered, so the patrol family keeps the ground default.
func goalRadiusParamFor(name string, def *content.UnitDef) int32 {
	if name == "VTOL_Move" {
		rp := int32(16)
		if def != nil && def.KamikazeDistance > rp {
			rp = def.KamikazeDistance
		}
		return rp
	}
	return 4 // radius field (0 at order creation) + 4 [R-P0-01 corrected]
}

// goalCellForWorld computes goal cells (goal − bias·0x80000 + 0x80000) >>20 [R-P0-01].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// the offset shifts by foot*halfCell. Using foot*halfCell offset makes tile and goal domains
// consistent and inclusive compare planar in cell domain [R-P0-01].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// gives identical domains for tile and goal for 1×1 and preserves inclusive distance semantics.
func goalCellForWorld(goal numeric.Fixed, foot int32) int32 {
	if foot <= 0 {
		foot = 1
	}
	half := int64(0x80000) // 1<<19 half cell [R-P0-01][03 §2.1]
	cell := int64(1 << 20) // 0x100000 one cell [03 §2.1]
	v := int64(goal) + int64(foot)*half
	return int32(floorDiv(v, cell))
}

// ThresholdSqFromRadius is exported helper for tests [R-P0-01].
func ThresholdSqFromRadius(radiusParam int32) int32 { return thresholdSqFromRadius(radiusParam) }

// ArrivalHandleFor returns the cached arrival handle for a unit, if any [R-P0-01].
func (s *System) ArrivalHandleFor(h pool.Handle) (goalX, goalZ int32, threshSq int32, ok bool) {
	if s == nil || s.arrivalHandles == nil {
		return 0, 0, 0, false
	}
	ah, ok := s.arrivalHandles[h]
	if !ok || ah == nil {
		return 0, 0, 0, false
	}
	return ah.goalX, ah.goalZ, ah.threshSq, true
}

// StepResult is the per-unit movement result for the slot visit [04 §8.1][04 §8.2].
// Arrived is true only when the unit was dispatched with an active route and is now
// within the unresolved final order tolerance (not merely "route became
// active") [R-P0-01]. DistToGoal is a publication-only integer-sqrt
// diagnostic in Fixed 16.16 units.
type StepResult struct {
	Handle     pool.Handle   // the stepped handle
	Arrived    bool          // final completion remains false until R-P0-01 closes
	DistToGoal numeric.Fixed // diagnostic distance after step; sentinel when no goal
	HasRoute   bool          // route.Active after step (pruning may have cleared it)
	Moved      bool          // position changed this tick
	Blocked    bool          // collision blocked this tick [04 §8.2] C24
	EmptyRoute bool          // true when no active route at entry (empty/failed) [task]
}

// NewSystem creates a System bound to terrain/profile/grid. It allocates the per-unit
// maps and a path.Scheduler whose SearchFunc is bound to path.Search with profile
// passability over the supplied terrain, and whose PublishFunc stores into Routes via
// Route.Publish [04 §7.3] C14.
func NewSystem(terrain *world.Terrain, fallback Profile, grid *OccupancyGrid) *System {
	s := &System{
		Terrain:        terrain,
		Fallback:       fallback,
		Grid:           grid,
		Routes:         make(map[pool.Handle]*Route),
		Steers:         make(map[pool.Handle]*SteerState),
		Collisions:     make(map[pool.Handle]*CollisionState),
		Flights:        make(map[pool.Handle]*FlightState),
		profiles:       make(map[pool.Handle]Profile),
		profileNames:   make(map[pool.Handle]string),
		prevMoveTier:   make(map[pool.Handle]int),
		prevSFXBand:    make(map[pool.Handle]int),
		pathFailures:   make(map[pool.Handle]PathFailure),
		activeOrders:   make(map[pool.Handle]*activeMove),
		arrivalHandles: make(map[pool.Handle]*arrivalHandle),
		moveGoals:      make(map[pool.Handle]*moveGoal),
	}
	sched := path.NewScheduler(s.searchFunc, s.publishFunc)
	// Use DefaultBase unless overridden [P0-I16]; no longer reads mutable global.
	sched.SetBase(path.DefaultBase)
	s.Scheduler = sched
	return s
}

// SetClasses binds the compiled movement-class table. Call it before the first
// EnsureUnit; units already initialized keep the profile they resolved.
func (s *System) SetClasses(classes map[string]*content.MovementClass) {
	if s == nil {
		return
	}
	s.Classes = classes
}

// BindWorld binds the units world for per-unit stepping [04 §1.1].
// The world is needed to fetch the *units.Unit for a handle inside StepUnit
// so the caller can drive movement from its own slot visit without passing the
// world on every call. Tick also binds it for legacy callers.
// This is the minimal additive interface for ON-03; it does not change
// internal/path or internal/orders.
func (s *System) BindWorld(w *units.World) {
	if s == nil {
		return
	}
	s.world = w
	// The layer registry must capture the bound unit world for the request
	// revision pass [04 §6.1 R-DOC04-B]; production binds the world in the
	// unit-sweep phase before the first search or occupancy commit. The lazy
	// ensureLayerRegistry covers the Tick entry point, which binds the world
	// itself without a BindWorld call.
	if s.layerRegistry == nil {
		s.layerRegistry = s.newLayerRegistry()
	}
}

// newLayerRegistry constructs the per-class layer registry over the terrain,
// the occupancy grid, the bound unit world and this System's committed-anchor
// adapter [04 §6.1 R-DOC04-B].
func (s *System) newLayerRegistry() *ClassLayers {
	return NewClassLayers(s.Terrain, s.Grid, s.world, s)
}

// ensureLayerRegistry returns the layer registry, creating it at first use
// for wiring sites reached without a BindWorld call (System.Tick binds the
// world itself).
func (s *System) ensureLayerRegistry() *ClassLayers {
	if s == nil {
		return nil
	}
	if s.layerRegistry == nil {
		s.layerRegistry = s.newLayerRegistry()
	}
	return s.layerRegistry
}

// World returns the bound units world, if any.
func (s *System) World() *units.World {
	if s == nil {
		return nil
	}
	return s.world
}

// isqrt returns floor(sqrt(n)) with integer arithmetic. It is used only for a
// diagnostic distance; authoritative completion uses the squared domain.
func isqrt(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	// Start at a power-of-two ceiling for sqrt(n). bits.Len64 avoids the
	// 1<<32 multiplication overflow that made the old initialization opaque.
	bit := bits.Len64(n) - 1
	x := uint64(1) << uint((bit+2)/2)
	for {
		y := (x + n/x) >> 1
		if y >= x {
			return x
		}
		x = y
	}
}

const maxUint64 = ^uint64(0)
const maxInt64 = int64(^uint64(0) >> 1)
const minInt64 = -maxInt64 - 1

func addSignedSaturating(a, b int64) int64 {
	if b > 0 && a > maxInt64-b {
		return maxInt64
	}
	if b < 0 && a < minInt64-b {
		return minInt64
	}
	return a + b
}

// absDiffUnsigned computes |a-b| without overflowing signed subtraction.
func absDiffUnsigned(a, b int64) uint64 {
	if a >= b {
		return uint64(a) - uint64(b)
	}
	return uint64(b) - uint64(a)
}

func squareSaturating(v uint64) uint64 {
	if v != 0 && v > maxUint64/v {
		return maxUint64
	}
	return v * v
}

func addSaturating(a, b uint64) uint64 {
	if maxUint64-a < b {
		return maxUint64
	}
	return a + b
}

// squaredDistanceFixed is the authoritative integer distance domain. Each
// axis is widened before subtraction and each product/sum saturates, so an
// extreme coordinate cannot wrap into the near-steering threshold.
func squaredDistanceFixed(ax, az, bx, bz int64) uint64 {
	x := squareSaturating(absDiffUnsigned(ax, bx))
	z := squareSaturating(absDiffUnsigned(az, bz))
	return addSaturating(x, z)
}

// distSqToGoal computes the squared fixed-point world distance without a
// floating-point decision path. The bool is false when no goal exists, so a
// diagnostic sentinel can never enter a steering decision.
func (s *System) distSqToGoal(u *units.Unit) (uint64, bool) {
	if u == nil {
		return 0, false
	}
	// The movement-goal handle is the authority, not the order's stored
	// position: for a build order the two differ [04 §8.3][04 §7.4].
	if gx, gz, ok := s.moveGoalForUnit(u); ok {
		return squaredDistanceFixed(int64(gx), int64(gz), int64(u.X), int64(u.Z)), true
	}
	route := s.Routes[u.Handle]
	if route != nil && route.Active && route.Count > 0 {
		last := route.Points[route.Count-1]
		wpX := addSignedSaturating(int64(world.CellToWorld(last.X)), 524288)
		wpZ := addSignedSaturating(int64(world.CellToWorld(last.Z)), 524288)
		return squaredDistanceFixed(wpX, wpZ, int64(u.X), int64(u.Z)), true
	}
	return 0, false
}

// distToGoal is a deterministic integer-sqrt diagnostic, never a completion
// predicate [I2].
func (s *System) distToGoal(u *units.Unit) numeric.Fixed {
	d2, ok := s.distSqToGoal(u)
	if !ok {
		// Publication-only sentinel; callers must use distSqToGoal's bool for
		// decisions. This is intentionally not a square.
		return numeric.Fixed(1 << 30)
	}
	return numeric.Fixed(int64(isqrt(d2)))
}

// finalGoalReached implements the recovered Move_Ground arrival predicate [R-P0-01].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// cell in the cell domain, planar only, inclusive: dx*dx+dz*dz <= threshold².
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// speed or blocked term participates. Route pruning (<=25 whole units) is separate [R-P0-01].
func (s *System) finalGoalReached(u *units.Unit, hadRoute bool) bool {
	if u == nil {
		return false
	}
	// Arrival is defined only for an order that has an arrival handle bound [R-P0-01].
	// hadRoute gates the diagnostic Arrived flag but the satisfied bit is still
	// set via the handle when within threshold even if route already pruned [R-P0-01].
	ah, ok := s.arrivalHandles[u.Handle]
	if !ok || ah == nil || ah.order == nil {
		return false
	}
	// Verify handle still belongs to the active head; stale handles after a head
	// replacement must not signal [R-P0-01][04 §7.3].
	if q := orders.QueueForUnit(u); q == nil || q.Head() != ah.order {
		return false
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	var tileX, tileZ int32
	if coll, ok := s.Collisions[u.Handle]; ok && coll != nil {
		tileX = coll.CachedAnchor.X
		tileZ = coll.CachedAnchor.Z
	} else {
		// Fallback before first stamp: use world-to-cell floor with same bias domain
		// for determinism. For 1x1 this is WorldToCell; for larger footprints the
		// bias offset is the same foot*half used for goal cells.
		tileX = world.WorldToCell(u.X)
		tileZ = world.WorldToCell(u.Z)
	}
	dx := int64(tileX) - int64(ah.goalX)
	dz := int64(tileZ) - int64(ah.goalZ)
	// Signed 32-bit squares, pure planar inclusive compare [R-P0-01] setle.
	if dx*dx+dz*dz <= int64(ah.threshSq) {
		ah.order.Satisfied |= arrivalSatisfiedBit // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// Also reflect hadRoute gating for diagnostic Arrived flag: only report
		// Arrived when we had a route at entry, preserving prior contract that
		// EmptyRoute paths do not count as arrived [R-P0-01][task].
		if hadRoute {
			return true
		}
		// Still signal the bit even when hadRoute false so pump can complete
		// a direct-walk goal without a published route [R-P0-01]. Whether a
		// direct arrival without a route should complete is Unknown for the
		// compact controller class — the open questions are consolidated in
		// the block directly below this function. Keep bit set but
		// diagnostic false.
		return false
	}
	return false
}

// TODO(question): How does the compact ground controller class (0x1c alloc, vt 0x4fd488,
// selected for owner type byte 3) signal ground-order arrival, given its vt+8 hook is a
// plain ret? And what advances the VTOL_MOVE handler phase 1→2? Both remain
// unrecovered; do not guess them. [R-P0-01] Related: whether a direct arrival
// without a published route should complete the order — the bit is kept set
// with the diagnostic false in finalGoalReached above.

// resolveProfile derives a unit's movement profile from its definition's
// movement class [02 "Unit record"] [04 §6.1].
//
// A definition naming no class gets the fallback: aircraft and buildings are
// not classified against the ground lattice. A definition naming a class the
// table does not hold is a content error, recorded in Unresolved so a load can
// report it rather than silently pathing a ship like a scout.
func (s *System) resolveProfile(u *units.Unit) Profile {
	if s == nil || u == nil || u.Def == nil {
		return Profile{}
	}
	name := u.Def.MovementClass
	if strings.TrimSpace(name) == "" {
		return s.Fallback
	}
	mc := s.Classes[content.CanonicalKey(name)]
	if mc == nil {
		s.noteUnresolved(name)
		return s.Fallback
	}
	return NewProfile(mc)
}

// noteUnresolved records a missing movement class once, in first-seen order.
func (s *System) noteUnresolved(name string) {
	for _, have := range s.Unresolved {
		if have == name {
			return
		}
	}
	s.Unresolved = append(s.Unresolved, name)
}

// classKeyOf returns the compiled class-table key a unit's movement profile
// resolves from, or "" when the definition names no class or an unresolved
// one — both carry the shared scratch profile [04 §6.1 R-DOC04-A]. It
// mirrors resolveProfile's resolution so the layer registry keys a unit's
// layer by the same canonical class name the profile came from [04 §6.1
// R-DOC04-B: all requests of one class share one record and layer].
func (s *System) classKeyOf(u *units.Unit) string {
	if s == nil || u == nil || u.Def == nil {
		return ""
	}
	name := u.Def.MovementClass
	if strings.TrimSpace(name) == "" {
		return ""
	}
	key := content.CanonicalKey(name)
	if s.Classes[key] == nil {
		return ""
	}
	return key
}

// classKeyFor returns the recorded class key of a handle; "" for a unit
// without an initialized surface, which shares the single scratch-profile
// layer keyed by the empty name.
func (s *System) classKeyFor(h pool.Handle) string {
	if s == nil || s.profileNames == nil {
		return ""
	}
	return s.profileNames[h]
}

// noteOccupancyCommit records a unit's occupancy-commit tick on every
// allocated class layer [04 §6.1 R-DOC04-B]. The commit tick is one field of
// the unit record, read by every class's per-cell classifier and request
// revision pass; the frozen registry represents it as a per-layer map, so the
// tick is noted on each. Names() is the deterministic allocation-order slice,
// never a map range [I1]. A layer allocated later misses pre-allocation
// ticks; the unit's next commit refreshes it.
func (s *System) noteOccupancyCommit(h pool.Handle, tick uint32) {
	if s == nil || h == 0 {
		return
	}
	reg := s.ensureLayerRegistry()
	if reg == nil {
		return
	}
	for _, name := range reg.Names() {
		reg.For(name, Profile{}).NoteCommit(h, tick)
	}
}

// ProfileFor returns the profile resolved for a unit handle. A handle with no
// initialized surfaces falls back, which is the correct answer for a path
// request that outlived its unit.
func (s *System) ProfileFor(h pool.Handle) Profile {
	if s == nil {
		return Profile{}
	}
	if p, ok := s.profiles[h]; ok {
		return p
	}
	return s.Fallback
}

func (s *System) recordPathFailure(h pool.Handle, status path.Status, tick uint32) {
	if s == nil {
		return
	}
	if s.pathFailures == nil {
		s.pathFailures = make(map[pool.Handle]PathFailure)
	}
	if rec, ok := s.pathFailures[h]; ok {
		rec.Status = status
		rec.Tick = tick
		rec.Retries++
		rec.NextRetry = tick + landPathFailureRetryInterval
		s.pathFailures[h] = rec
		return
	}
	s.pathFailures[h] = PathFailure{Status: status, Tick: tick, Retries: 0, NextRetry: tick + landPathFailureRetryInterval}
}

func (s *System) HasPathFailure(h pool.Handle) bool {
	if s == nil || s.pathFailures == nil {
		return false
	}
	_, ok := s.pathFailures[h]
	return ok
}

func (s *System) PathFailure(h pool.Handle) (path.Status, uint32, bool) {
	if s == nil || s.pathFailures == nil {
		return 0, 0, false
	}
	rec, ok := s.pathFailures[h]
	if !ok {
		return 0, 0, false
	}
	return rec.Status, rec.Tick, true
}

func (s *System) PathFailureRecord(h pool.Handle) (PathFailure, bool) {
	if s == nil || s.pathFailures == nil {
		return PathFailure{}, false
	}
	rec, ok := s.pathFailures[h]
	return rec, ok
}

func (s *System) ClearPathFailure(h pool.Handle) {
	if s == nil || s.pathFailures == nil {
		return
	}
	delete(s.pathFailures, h)
}

func (s *System) NextRetryTick(h pool.Handle) uint32 {
	if s == nil || s.pathFailures == nil {
		return 0
	}
	if rec, ok := s.pathFailures[h]; ok {
		return rec.NextRetry
	}
	return 0
}

func (s *System) RetryCount(h pool.Handle) int {
	if s == nil || s.pathFailures == nil {
		return 0
	}
	if rec, ok := s.pathFailures[h]; ok {
		return rec.Retries
	}
	return 0
}

func (s *System) IncrementPathFailureRetry(h pool.Handle, nextTick uint32) {
	if s == nil || s.pathFailures == nil {
		return
	}
	rec, ok := s.pathFailures[h]
	if !ok {
		return
	}
	rec.Retries++
	rec.NextRetry = nextTick
	s.pathFailures[h] = rec
}

func (s *System) IsGoalCellPassable(h pool.Handle, cell path.Cell) bool {
	if s == nil {
		return true
	}
	if s.Terrain == nil {
		return true
	}
	profile := s.ProfileFor(h)
	return profile.IsPassableFootprint(s.Terrain, cell.X, cell.Z)
}

// EnsureUnit initializes per-unit surfaces for u if not already present. It stamps
// the occupancy grid at the unit's current quantized anchor [04 §8.2] C22.
func (s *System) EnsureUnit(u *units.Unit) {
	if s == nil || u == nil || u.Def == nil {
		return
	}
	h := u.Handle
	if _, ok := s.Routes[h]; ok {
		return
	}
	s.Routes[h] = &Route{}
	// Resolve this unit's own movement profile once; every later passability,
	// bias, footprint and occupancy decision for it reads this one [04 §6.1].
	profile := s.resolveProfile(u)
	s.profiles[h] = profile
	s.profileNames[h] = s.classKeyOf(u)
	// SteerState [M2][M3] with pitch accumulator and accel/brake plumbing
	steer := &SteerState{
		X:              int32(u.X.Raw()),
		Z:              int32(u.Z.Raw()),
		Heading:        0,
		PendingHeading: 0,
		Dirty:          false,
		Speed:          0,
		MaxVelocity:    int32(u.Def.MaxVelocity),
		TurnRate:       int32(u.Def.TurnRate),
		HeightWord:     int16(u.Y.Raw() >> 16),
		SeaLevel:       0,
		DefFlags:       0,
		Pitch:          0,
		Bank:           0,
		PitchScale:     int32(u.Def.PitchScale),
		BankScale:      int32(u.Def.BankScale),
		Acceleration:   int32(u.Def.Acceleration),
		BrakeRate:      int32(u.Def.BrakeRate),
		ResidualX:      0,
		ResidualY:      0,
		ResidualZ:      0,
	}
	if s.Terrain != nil {
		steer.SeaLevel = s.Terrain.SeaLevel
	}
	// Derive flags from unit def
	var flags uint32
	if u.Def.CanHover {
		flags |= 0x1000
	}
	if u.Def.Floater {
		flags |= 0x80000
	}
	steer.DefFlags = flags
	s.Steers[h] = steer

	// CollisionState
	footX := profile.FootPrintX
	footZ := profile.FootPrintZ
	if footX <= 0 {
		footX = int16(u.Def.FootprintX)
		if footX <= 0 {
			footX = 1
		}
	}
	if footZ <= 0 {
		footZ = int16(u.Def.FootprintZ)
		if footZ <= 0 {
			footZ = 1
		}
	}
	coll := &CollisionState{
		ID:          int(h),
		X:           int32(u.X.Raw()),
		Z:           int32(u.Z.Raw()),
		Y:           int32(u.Y.Raw()),
		VX:          0,
		VZ:          0,
		Speed:       0,
		Heading:     0,
		MaxVelocity: int32(u.Def.MaxVelocity),
		FootPrintX:  footX,
		FootPrintZ:  footZ,
		Mode:        1,
		Blocked:     false,
		Dirty:       false,
		BlockerID:   -1,
	}
	// Quantized anchor via half bias [04 §8.2] C23
	// Use footprint-derived half bias if not set
	bx, bz := coll.HalfBias()
	_ = bx
	_ = bz
	anchor := coll.ProposedAnchor(coll.Mode) // uses current X,Z,VX=0
	// Actually for initial stamp we want anchor from current X,Z without velocity
	// ProposedAnchor adds VX,VZ (0) then quantizes, so it's current cell
	coll.CachedAnchor = anchor
	coll.OldAnchor = anchor
	coll.CachedMode = 1
	s.Collisions[h] = coll
	if s.Grid != nil {
		// The creation-time stamp writes no occupancy-commit tick: commit
		// ticks are noted at the movement commit site (StepUnit) [04 §6.1
		// R-DOC04-B]. Whether retail also writes the unit record's
		// commit-tick field at the creation-time stamp is untraced and is
		// recorded with the write-site questions at that commit site.
		s.Grid.Stamp(anchor, footX, footZ, coll.ID)
	}
	// Init move mode to parked [04 §9.1] 1 stopped/parked; TakeOff/Sumbit will set 2 active
	u.Move.Mode = 1
	// FlightState for can-fly units
	if u.Def.CanFly {
		flight := &FlightState{
			Mode:                 1,
			X:                    int32(u.X.Raw()),
			Y:                    int32(u.Y.Raw()),
			Z:                    int32(u.Z.Raw()),
			VX:                   0,
			VY:                   0,
			VZ:                   0,
			Speed:                0,
			Heading:              0,
			TargetHeading:        0,
			TurnResidual:         0,
			MaxVelocity:          int32(u.Def.MaxVelocity),
			Acceleration:         int32(u.Def.Acceleration),
			BrakeRate:            int32(u.Def.BrakeRate),
			TurnRate:             int32(u.Def.TurnRate),
			TargetY:              int32(u.Y.Raw()),
			VerticalHoldSentinel: false,
			Dirty:                false,
		}
		// Authored zeros stay zero. The previous 65536/16384/65536 substitutes
		// were invented constants on an authoritative path (I6): a unit whose
		// FBI really does author zero acceleration would silently fly with an
		// acceleration nobody wrote, and no retail source gives those values.
		// A stationary aircraft is a visible content bug; a fabricated one is
		// not.
		s.Flights[h] = flight
	}
}

// SubmitMove submits a path request for handle owned by player from start to goal cell
// via the scheduler with a PointGoal of radius 0 [04 §7.2] C8. The caller keeps the
// orders queue as authority by also pushing a Move_Ground node; this method only
// enqueues the path request.
func (s *System) SubmitMove(handle pool.Handle, player uint8, start, goal path.Cell) {
	if s == nil || s.Scheduler == nil {
		return
	}
	s.submitMove(handle, player, start, goal, 0)
}

func (s *System) submitMove(handle pool.Handle, player uint8, start, goal path.Cell, activation uint64) {
	goalObj := path.PointGoal(goal, 0) // radius 0 [task] — legacy direct path (fixtures, tests) remains PointGoal [04 §7.2] C8
	req := path.Request{
		Unit:       handle,
		Player:     player,
		Start:      start,
		Goal:       goalObj,
		Activation: activation,
	}
	s.Scheduler.Submit(req)
}

func (s *System) submitMoveForOrder(u *units.Unit, head *orders.Node, start, goal path.Cell, activation uint64) {
	// OW-3-P: select Goal family per order [04 §7.2][04 §7.4][04 §3.5] — Annulus for attack/guard stand-off where retail establishes it, Point otherwise.
	// RectPerimeterGoal and SavedGoal remain unwired (no established producer) per goals.go header [04 §7.2][04 §7.4][M-4].
	goalObj := s.goalForOrder(goal, head)
	req := path.Request{
		Unit:       u.Handle,
		Player:     u.Owner,
		Start:      start,
		Goal:       goalObj,
		Activation: activation,
	}
	s.Scheduler.Submit(req)
}

func (s *System) staticObstacleRevision() uint64 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return s.Terrain.StaticObstacleRevision()
}

// ActivateMove binds one path request to the current primary order head and
// submits it exactly once.  The queue head is the authority: a repeated call
// for the same node is a no-op, while a new node cancels the old request and
// invalidates its route before submitting the replacement.  This closes the
// activation/submission boundary used by session's authoritative loop [04
// §3.3][04 §7.3].
//
// The caller must have resolved a target's current position into head.GoalX/Z
// before calling this method.  Target tracking is deliberately kept at the
// order boundary; a path request never captures a mutable *units.Unit.
func (s *System) ActivateMove(u *units.Unit, head *orders.Node) bool {
	if s == nil || u == nil || head == nil || s.Scheduler == nil {
		return false
	}
	if s.activeOrders == nil {
		s.activeOrders = make(map[pool.Handle]*activeMove)
	}
	if old, ok := s.activeOrders[u.Handle]; ok && old.order == head {
		return false // exactly one submission per active order
	}
	if _, wasBound := s.activeOrders[u.Handle]; wasBound {
		s.Scheduler.Cancel(u.Handle)
		if route := s.Routes[u.Handle]; route != nil {
			route.Active = false
			route.Dirty = true
		}
	}
	s.nextActivation++
	if s.nextActivation == 0 { // reserve zero for unbound/direct requests
		s.nextActivation++
	}
	token := s.nextActivation
	s.activeOrders[u.Handle] = &activeMove{order: head, token: token}
	start := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	// Path search is aimed at the goal handle, so a replan after a dynamic
	// block re-paths to the same point the mover was already steering at —
	// for a build order that is the selected perimeter candidate, not the
	// site centre [04 §8.3][04 §7.4].
	goalX, goalZ, _ := s.moveGoalFor(u.Handle, head)
	goal := path.Cell{X: world.WorldToCell(goalX), Z: world.WorldToCell(goalZ)}
	s.submitMoveForOrder(u, head, start, goal, token)
	s.bindArrivalHandle(u, head)
	return true
}

// ReplanMove replaces the pending path for the currently active order. It
// retains the order identity, so the resulting publication is still attached
// only to that head. Collision handling does not call this surface: the
// collision commit remains the final authority [04 §8.2][R-MOV-02A].
func (s *System) ReplanMove(u *units.Unit, head *orders.Node) bool {
	if s == nil || u == nil || head == nil || s.Scheduler == nil {
		return false
	}
	if s.activeOrders == nil || s.activeOrders[u.Handle] == nil || s.activeOrders[u.Handle].order != head {
		return s.ActivateMove(u, head)
	}
	s.Scheduler.Cancel(u.Handle)
	if route := s.Routes[u.Handle]; route != nil {
		route.Active = false
		route.Dirty = true
	}
	s.nextActivation++
	if s.nextActivation == 0 {
		s.nextActivation++
	}
	token := s.nextActivation
	s.activeOrders[u.Handle].token = token
	start := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	// Path search is aimed at the goal handle, so a refresh re-paths to the
	// same point the mover was already steering at — for a build order that is
	// the selected perimeter candidate, not the site centre [04 §8.3][04 §7.4].
	goalX, goalZ, _ := s.moveGoalFor(u.Handle, head)
	goal := path.Cell{X: world.WorldToCell(goalX), Z: world.WorldToCell(goalZ)}
	s.submitMoveForOrder(u, head, start, goal, token)
	s.bindArrivalHandle(u, head)
	return true
}

// DeactivateMove drops the path binding for a unit whose active order is no
// longer path-backed.  It is intentionally idempotent so every queue/head
// transition can pass through the same boundary.
func (s *System) DeactivateMove(handle pool.Handle) {
	if s == nil {
		return
	}
	if s.Scheduler != nil {
		s.Scheduler.Cancel(handle)
	}
	if s.activeOrders != nil {
		delete(s.activeOrders, handle)
	}
	if route := s.Routes[handle]; route != nil && route.Active {
		route.Active = false
		route.Dirty = true
	}
	if s.arrivalHandles != nil {
		delete(s.arrivalHandles, handle)
	}
}

func (s *System) bindArrivalHandle(u *units.Unit, head *orders.Node) {
	if s == nil || u == nil || head == nil {
		return
	}
	if s.arrivalHandles == nil {
		s.arrivalHandles = make(map[pool.Handle]*arrivalHandle)
	}
	// Only bind for Move_Ground-family orders [R-P0-01]; other orders not arrival-tracked.
	name := orders.DescriptorFor(head.ID).Name
	switch name {
	case "Move_Ground", "VTOL_Move", "QMove", "Patrol", "QPatrol", "VTOL_Patrol", "RepairPatrol", "VTOL_RepairPatrol":
	default:
		// Not a ground-move family order: ensure no stale handle remains.
		delete(s.arrivalHandles, u.Handle)
		return
	}
	profile := s.ProfileFor(u.Handle)
	footX := profile.FootPrintX
	footZ := profile.FootPrintZ
	if footX <= 0 {
		if u.Def != nil && u.Def.FootprintX > 0 {
			footX = int16(u.Def.FootprintX)
		} else {
			footX = 1
		}
	}
	if footZ <= 0 {
		if u.Def != nil && u.Def.FootprintZ > 0 {
			footZ = int16(u.Def.FootprintZ)
		} else {
			footZ = 1
		}
	}
	// The handle's cell comes from the same accessor the mover steers by, so
	// steering target and arrival test can never disagree [04 §8.3].
	goalWorldX, goalWorldZ, _ := s.moveGoalFor(u.Handle, head)
	goalX := goalCellForWorld(goalWorldX, int32(footX))
	goalZ := goalCellForWorld(goalWorldZ, int32(footZ))
	// [R-P0-01 corrected] radiusParam for the goal handle: ground move-family
	// binds the node's radius field plus 4, and the field is 0 at order
	// creation (threshold 0 — exact goal cell); VTOL_Move binds
	// max(KamikazeDistance,16). The sight-derived radius previously used here
	// was a misattribution: the sight reads in the traced handlers feed
	// range/acquire paths, never the goal handle.
	radiusParam := goalRadiusParamFor(name, u.Def)
	threshSq := thresholdSqFromRadius(radiusParam) // [R-P0-01] floor(radiusParam/16)²
	s.arrivalHandles[u.Handle] = &arrivalHandle{order: head, goalX: goalX, goalZ: goalZ, threshSq: threshSq}
	// [R-P0-01] initial gate must be 0 so phase 0 handler can arm 0xE0; otherwise static 0x402 would block.
	if head.Phase == 0 && head.DynamicGate != 0 {
		// Only clear initial static gate; preserve armed 0xE0 for re-binds after a replan where Phase already 1
		head.DynamicGate = 0
		head.Deadline = -1
	}
}

// searchFunc is the injected SearchFunc bound to path.Search with profile passability
// over System.Terrain and occupancy. It honors the 100-pops-per-request-per-call
// budget and full-or-empty publication [04 §7.3] C11 C12 via a resumable Session
// per unit held in deterministic slice storage indexed by handle [I1].
func (s *System) searchFunc(r path.Request, scale int32, budget int) ([]path.Point, path.Status, bool) {
	if s == nil {
		return nil, path.StatusRejected, true
	}
	idx := int(r.Unit)
	// Grow sessions slice to cover handle deterministically [I1].
	if idx >= len(s.sessions) {
		need := idx + 1
		if cap(s.sessions) < need {
			ns := make([]*path.Session, need)
			copy(ns, s.sessions)
			s.sessions = ns
		} else {
			s.sessions = s.sessions[:need]
		}
	}
	sess := s.sessions[idx]
	needsNew := sess == nil || sess.Start() != r.Start || sess.Goal() != r.Goal
	if needsNew {
		// The REQUESTING unit's profile decides passability and bias. Sharing
		// one profile across the world paths a ship, a hover and a Krogoth as
		// the same 1x1 ground scout [04 §6.1] [04 §7.1].
		profile := s.ProfileFor(r.Unit)
		bias := path.Point{X: int32(profile.FootPrintX / 2), Z: int32(profile.FootPrintZ / 2)}
		var hasBounds bool
		var bounds path.Rect
		if s.Terrain != nil && s.Terrain.CellW > 0 && s.Terrain.CellH > 0 {
			hasBounds = true
			bounds = path.Rect{Min: path.Cell{X: 0, Z: 0}, Max: path.Cell{X: s.Terrain.CellW - 1, Z: s.Terrain.CellH - 1}}
		}
		var cfg path.SearchConfig
		if s.Terrain != nil {
			// The requesting unit's class layer is the search passability
			// source [04 §6.1 R-DOC04-B]: one record and one stamped layer
			// per movement class, shared by reference by every request of
			// the class. This is the SC22 static layer — terrain and static
			// features per the class stamp; mobile occupancy is not an A*
			// wall, and the only dynamic channel into the layer is the
			// occupant-age gate fed by the request revision pass
			// [docs/SPEC_CONFLICTS SC22][04 §8.2].
			//
			// PassableValue binds the packed terrain stamp, not the full
			// Passable consumer: the owner/building-mask write sites are
			// Supported inference with no located retail writer (the
			// unwired-write-site questions are recorded at the
			// occupancy-commit site in StepUnit), and Passable's bit-miss
			// value 2 short-circuits the terrain value, so consulting an
			// unwired mask would bypass terrain blocking entirely. With the
			// terrain binding the bit-miss value 2 never occurs in
			// production — matching the pre-layer terrain-only search
			// [04 §6.1 R-DOC04-B].
			//
			// The per-request revision pass runs at request init before any
			// expansion [04 §6.1 R-DOC04-B][04 §7.3]; path.Session.init
			// invokes cfg.Revise first.
			reg := s.ensureLayerRegistry()
			cls := s.classKeyFor(r.Unit)
			layer := reg.For(cls, profile)
			requester := r.Unit
			revTick := s.tick
			cfg = path.SearchConfig{
				Start:     r.Start,
				Goal:      r.Goal,
				Scale:     scale,
				Bias:      bias,
				HasBounds: hasBounds,
				Bounds:    bounds,
				PassableValue: func(c path.Cell) uint8 {
					return layer.Value(c.X, c.Z)
				},
				Revise: func() {
					reg.ReviseFor(cls, profile, requester, revTick)
				},
			}
		} else {
			// No terrain: nothing to classify; every cell passes except the
			// bounds check (HasBounds is false above) [04 §7.1] C10.
			cfg = path.SearchConfig{
				Start:      r.Start,
				Goal:       r.Goal,
				Scale:      scale,
				Bias:       bias,
				HasBounds:  hasBounds,
				Bounds:     bounds,
				IsPassable: func(path.Cell) bool { return true },
			}
		}
		sess = path.NewSession(cfg)
		s.sessions[idx] = sess
	}
	points, status, done := sess.Resume(budget) // [04 §7.3] C11 budget, C12 full-or-empty
	if done {
		s.sessions[idx] = nil
	}
	return points, status, done
}

// publishFunc stores the published points into the per-unit Route via Route.Publish
// [04 §7.3] C14 and leaves the order node as authority (caller keeps orders queue).
// It surfaces non-success status via Route.Status and pathFailures for loop failure handling [04 §7.3] C12 [P0-08][P0-12].
func (s *System) publishFunc(r path.Request, points []path.Point, status path.Status) {
	if s == nil {
		return
	}
	// A scheduler callback can finish after the queue head has changed (for
	// example, a replace/purge in the order pump).  Publication belongs only to
	// the node that activated this request.  Leave the current route untouched
	// when identity no longer matches; the next active head will submit through
	// ActivateMove.
	if binding, bound := s.activeOrders[r.Unit]; bound {
		if binding == nil || binding.token != r.Activation {
			return
		}
		if s.world != nil {
			u := s.world.Unit(r.Unit)
			q := orders.QueueForUnit(u)
			if u == nil || q == nil || q.Head() != binding.order {
				return
			}
		}
	}
	route := s.Routes[r.Unit]
	if route == nil {
		route = &Route{}
		s.Routes[r.Unit] = route
	}
	mPoints := make([]Point, len(points))
	for i, p := range points {
		mPoints[i] = Point{X: p.X, Z: p.Z}
	}
	revision := s.staticObstacleRevision()
	if s.world != nil {
		if u := s.world.Unit(r.Unit); u != nil && u.Def != nil && u.Def.CanFly {
			revision = 0 // aircraft do not consume the ground static layer
		}
	}
	route.PublishAtRevision(mPoints, revision)
	// Overnight land policy: Kbots may use aggressive legality-only line of
	// sight smoothing; vehicles retain conservative forward-only waypoints until
	// authored turning/braking feasibility is fully recovered [plan §3.1].
	// The complete footprint predicate is used for every ray cell, so a shortcut
	// cannot cut a diagonal corner. Exact bad-slope speed/cost remains TODO.
	if s.world != nil {
		if u := s.world.Unit(r.Unit); u != nil && u.Def != nil && !u.Def.CanFly {
			reg := s.ensureLayerRegistry()
			layer := reg.For(s.classKeyFor(r.Unit), s.ProfileFor(r.Unit))
			smoothLandRouteWithLayer(route, u.Def, s.ProfileFor(r.Unit), layer)
		}
	}
	route.Status = status
	if status == path.StatusRejected {
		s.recordPathFailure(r.Unit, status, s.tick)
	} else {
		s.ClearPathFailure(r.Unit)
	}
}

func aggressiveLandSmoothing(def *content.UnitDef) bool {
	return def != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(def.MovementClass)), "kbot")
}

// smoothLandRoute encodes the explicit overnight policy: only Kbots take the
// aggressive legality-only shortcut; vehicles retain their conservative route
// until authored forward turning/braking feasibility is established.
func smoothLandRoute(route *Route, def *content.UnitDef, profile Profile, terrain *world.Terrain) {
	if route == nil || !aggressiveLandSmoothing(def) {
		return
	}
	route.Smooth(func(p Point) bool {
		if terrain == nil {
			return true
		}
		return profile.IsPassableFootprint(terrain, p.X-int32(profile.FootPrintX)/2, p.Z-int32(profile.FootPrintZ)/2)
	})
}

// smoothLandRouteWithLayer uses the same stamped static source as A* so a
// legality shortcut cannot cross a cell rejected by search [04 §7.2][04 §7.5].
func smoothLandRouteWithLayer(route *Route, def *content.UnitDef, profile Profile, layer *ClassLayer) {
	if route == nil || !aggressiveLandSmoothing(def) || layer == nil {
		return
	}
	route.Smooth(func(p Point) bool {
		return layer.Value(p.X-int32(profile.FootPrintX)/2, p.Z-int32(profile.FootPrintZ)/2) != LayerBlocked
	})
}

// headingFromDelta computes a uint16 heading for a ground delta dx (east), dz (north)
// where heading 0 = north (+Z), 16384 = east (+X) [04 §5.1][04 §8.1] C20.
//
// Integer-only per I2: the angle is bisected against the simulation trig
// tables (the same 512-entry round(8192·sin) family PLAN_03 C17 sanctions),
// comparing the cross product of the delta with the candidate direction. The
// result is exact on axis/diagonal boundaries and within one table step
// (1/512 of a circle) elsewhere.
// TODO(question): [04 §8.1] does not name retail's arctan method; this
// bisection is our deterministic stand-in, not an attested sequence.
func headingFromDelta(dx, dz int64) uint16 {
	if dx == 0 && dz == 0 {
		return 0
	}
	lo, hi := int32(0), int32(65536)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		// Direction at heading mid is (sin, cos): 0 points north, +Z.
		cross := int64(numeric.Sin(numeric.Angle(mid)))*dz -
			int64(numeric.Cos(numeric.Angle(mid)))*dx
		if cross < 0 {
			lo = mid // candidate is short of the target direction
		} else {
			hi = mid
		}
	}
	return uint16(lo)
}

// emitMovementCallbacks emits StartMoving/StopMoving/MoveRateN and setSFXoccupy per [04 §5.2][GAP T15] C17 C18.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Must be called after steering/flight integration but before the next slot's clear/commit so the VM sees the walk loops [04 §1.1][01 §4.4].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// We map def MoveRate1/2 via content.UnitDef.MoveRate1/2 (defaults twice MaxVelocity) [02 "Unit record"] [04 §5.2].
func (s *System) emitMovementCallbacks(u *units.Unit, speed int32) {
	if s == nil || u == nil {
		return
	}
	vm := u.GetScript()
	if vm == nil {
		return
	}
	if s.prevMoveTier == nil {
		s.prevMoveTier = make(map[pool.Handle]int)
	}
	if s.prevSFXBand == nil {
		s.prevSFXBand = make(map[pool.Handle]int)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	inhibit := false
	attached := u.Attachment.Carrier != 0
	// Magnitude is speed scalar (ground) or 3-D flight speed; ground uses scalar Speed [04 §5.2] C18.
	// We pass speed for magA and 0 for magZ so both-zero gate is Speed==0 [04 §5.2][GAP T15] C18.
	magA := speed
	magZ := int32(0)
	rate1 := int32(0)
	rate2 := int32(0)
	if u.Def != nil {
		rate1 = u.Def.MoveRate1
		rate2 = u.Def.MoveRate2
		// Defaults: twice MaxVelocity when not authored [02 "Unit record"] [04 §5.2].
		if rate1 == 0 && rate2 == 0 && u.Def.MaxVelocity != 0 {
			rate1 = u.Def.MaxVelocity * 2
			rate2 = u.Def.MaxVelocity * 2
		} else if rate1 == 0 {
			rate1 = rate2
		} else if rate2 == 0 {
			rate2 = rate1
		}
	}
	cat := cob.MoveRateCategory(inhibit, attached, magA, magZ, rate1, rate2) // [04 §5.2][GAP T15] C18
	prev := s.prevMoveTier[u.Handle]
	if prev != cat {
		kinds := cob.MoveRateTransition(prev, cat) // [GAP T15] C18
		for _, k := range kinds {
			var name string
			switch k {
			case cob.CallbackStartMoving:
				name = "StartMoving"
			case cob.CallbackStopMoving:
				name = "StopMoving"
			case cob.CallbackMoveRate1:
				name = "MoveRate1"
			case cob.CallbackMoveRate2:
				name = "MoveRate2"
			case cob.CallbackMoveRate3:
				name = "MoveRate3"
			default:
				continue
			}
			cob.StartWithImmediateBarrier(vm, name, nil) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		}
		s.prevMoveTier[u.Handle] = cat
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	band := MediumBand(s.Terrain, u) // simplified mapping [04 §9.1]; exact overwrite 1→2→3 via wy/wt/wl/mb is TODO(question) for hover
	prevBand := s.prevSFXBand[u.Handle]
	if band != prevBand {
		cob.StartWithImmediateBarrier(vm, "setSFXoccupy", []int32{int32(band)}) // I [GAP T15] C17 exact spelling lower-case s
		s.prevSFXBand[u.Handle] = band
	}
}

// BeginTick builds per-tick shared indexing deterministically ONCE per tick [04 §8.2] C22.
// The cargo set (units whose Attachment.Carrier != 0) is captured here so all
// StepUnit calls in this tick observe the same cargo membership [04 §10.2].
// The occupancy grid itself is synchronous; clear-then-stamp finishes before the
// next slot [04 §8.2] C22, so later StepUnit calls immediately observe earlier
// commits without needing a separate grid copy. BeginTick must be called once
// before any StepUnit in the tick; the world must have been bound via BindWorld
// (or via Tick's legacy path).
func (s *System) BeginTick(tick uint32) {
	if s == nil {
		return
	}
	s.tick = tick
	s.tickStarted = true
	// Deterministic cargo indexing [I1][04 §10.2]: player 0..9 asc then slot asc
	// via IterSliced yields that order [P0-16]. Build once; StepUnit consumes.
	s.tickCarried = make(map[pool.Handle]struct{}, 8)
	w := s.world
	if w != nil {
		for _, u := range w.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			if u.Attachment.Carrier != 0 {
				s.tickCarried[u.Handle] = struct{}{}
			}
		}
	}
}

// EndTick clears per-tick shared indexing and performs post-sweep work that
// must happen once after all carriers have moved: cargo slaving and air-pad
// repair [04 §10.2]. It must be called after the per-unit StepUnit loop.
func (s *System) EndTick(tick uint32) {
	if s == nil {
		return
	}
	_ = tick
	w := s.world
	if w != nil {
		s.SyncCarriedMotion(w) // [04 §10.2] cargo slaved to carrier, no occupancy stamp
		// Air repair on pads for landed VTOLs [04 §10.2] VTOL_GetRepaired
		for _, u := range w.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			if u.Def != nil && u.Def.CanFly && u.Move.Mode == 1 {
				for _, pad := range w.IterSliced() {
					if pad == nil || pad == u {
						continue
					}
					if !IsLandingPad(pad) {
						continue
					}
					dx := int64(u.X) - int64(pad.X)
					dz := int64(u.Z) - int64(pad.Z)
					if dx*dx+dz*dz <= int64(16*65536)*int64(16*65536) {
						AirRepair(w, u.Handle, pad, 5)
						break
					}
				}
			}
		}
	}
	s.tickCarried = nil
	s.tickStarted = false
}

// StepUnit advances ONLY the unit identified by handle through the same
// integration path Tick uses today [04 §8.1][04 §8.2][04 §10.1]. The per-unit
// body is extracted so Tick becomes BeginTick+loop(StepUnit)+EndTick wrapper,
// kept for compatibility and documented non-authoritative so the future central
// loop replaces it. Shared per-tick indexing from BeginTick is consumed;
// published routes are consumed without duplicate submission; route/goal
// completion uses goal tolerance (arrival) and is exposed via StepResult.
// Ground, air, landing, transport states keep working [04 §9.1][04 §10.2].
// No presentation/camera state enters movement [I6]. Deterministic.
func (s *System) StepUnit(handle pool.Handle, tick uint32) StepResult {
	if s == nil {
		return StepResult{Handle: handle, EmptyRoute: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	w := s.world
	if w == nil {
		return StepResult{Handle: handle, EmptyRoute: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	u := w.Unit(handle)
	if u == nil || !u.Alive {
		return StepResult{Handle: handle, EmptyRoute: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	// Cargo check via per-tick indexing [04 §10.2]. If BeginTick was not called
	// we fall back to direct carrier check for backward compat (still deterministic).
	if s.tickCarried != nil {
		if _, isCarried := s.tickCarried[handle]; isCarried {
			d := s.distToGoal(u)
			s.emitMovementCallbacks(u, 0) // carried cargo does not drive own mover [04 §10.2]
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
		}
	} else if u.Attachment.Carrier != 0 {
		d := s.distToGoal(u)
		s.emitMovementCallbacks(u, 0)
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
	}
	// Keep orders queue as authority: only follow route if primary order is Move_Ground-class [task]
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		// Also try alternate accessor for session-bound queues
		if q == nil || (q.LenPrimary() == 0 && q.Head() == nil) {
			d := s.distToGoal(u)
			s.emitMovementCallbacks(u, 0) // no order => tier 0 [04 §5.2][GAP T15] C18 ensure StopMoving if was moving
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
		}
	}
	var head *orders.Node
	if q.LenPrimary() > 0 {
		head = q.Primary()[0]
	} else {
		head = q.Head()
	}
	if head == nil {
		d := s.distToGoal(u)
		s.emitMovementCallbacks(u, 0)
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
	}
	name := orders.DescriptorFor(head.ID).Name
	if name != "Move_Ground" && name != "VTOL_Move" && name != "QMove" && name != "VTOL_MobileBuild" && name != "MobileBuild" && name != "VTOL_Patrol" && name != "Patrol" {
		if head.GoalX == 0 && head.GoalZ == 0 && head.GoalY == 0 {
			d := s.distToGoal(u)
			s.emitMovementCallbacks(u, 0)
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
		}
	}
	route := s.Routes[handle]
	hadRoute := route != nil && route.Active && route.Count > 0
	if hadRoute && (u.Def == nil || !u.Def.CanFly) && route.NeedsStaticReplan(s.staticObstacleRevision()) {
		// A static mutation invalidates the published route before its next
		// waypoint is consumed. Replanning retains the order identity; with no
		// bound order the route remains inactive and the caller can resubmit it.
		route.Active = false
		route.Dirty = true
		if head != nil {
			s.ReplanMove(u, head)
		}
		d := s.distToGoal(u)
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
	}
	_ = s.resolveProfile(u) // retained for profile revision side-effects if any; outer profile not needed for pitch path [M2]
	var directGoal bool
	var directX, directZ numeric.Fixed
	if !hadRoute {
		// [R-P0-01] still test arrival even without an active route: handle is
		// bound at order activation, and the inclusive cell-domain predicate may
		// already be satisfied before a route publishes (e.g., start within threshold).
		_ = s.finalGoalReached(u, hadRoute)
		d := s.distToGoal(u)
		d2, hasGoal := s.distSqToGoal(u)
		if hasGoal && d2 <= localSteeringThresholdSquared {
			s.emitMovementCallbacks(u, 0)
			// Route absence is not proof of final order completion via local
			// threshold; completion is via the arrival handle above [R-P0-01].
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false, Arrived: false}
		}
		// No active route (failed search, or route pruned out last tick): the
		// mover still drives straight at the goal handle — retail keeps steering
		// at the goal handle once the route is exhausted, and the arrival
		// predicate completes the order when the tile lands on the goal cell.
		// The handle, not the order's stored position, is the target: a build
		// order stores the site centre but is walked to a perimeter candidate
		// [04 §8.3][04 §7.4]. A head that reports MoveArrived has completed its
		// approach (mobile build sets it once the builder is within nanolathe
		// reach of the site), so the mover stops rather than keep steering at
		// the goal handle [04 §7.4][04 §3.5].
		if head != nil && head.MoveState != orders.MoveArrived {
			if gx, gz, okGoal := s.moveGoalFor(handle, head); okGoal {
				directGoal = true
				directX = gx
				directZ = gz
			} else {
				s.emitMovementCallbacks(u, 0)
				return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false, Arrived: false}
			}
		} else {
			s.emitMovementCallbacks(u, 0)
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false, Arrived: false}
		}
	} else {
		// Prune(mover pos) [04 §7.3] C15. The stored points carry the half-footprint bias,
		// so the mover's position is compared in the same biased domain [04 §7.1] C1.
		profile := s.resolveProfile(u)
		moverPt := Point{
			X: world.WorldToCell(u.X) + int32(profile.FootPrintX/2),
			Z: world.WorldToCell(u.Z) + int32(profile.FootPrintZ/2),
		}
		route.Prune(moverPt)
		if !route.Active || route.Count == 0 {
			// No waypoint left this tick: check only the local steering threshold,
			// then fall through to direct goal movement. This is not completion.
			d := s.distToGoal(u)
			d2, hasGoal := s.distSqToGoal(u)
			if hasGoal && d2 <= localSteeringThresholdSquared {
				arrived := s.finalGoalReached(u, hadRoute)
				s.emitMovementCallbacks(u, 0) // arrived => tier 0 [04 §5.2][GAP T15] C18 ensure StopMoving
				return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: false, Moved: false, Arrived: arrived}
			}
			// Still far: direct move to the goal handle [04 §8.3][04 §7.4].
			// A head that reports MoveArrived has completed its approach (mobile
			// build), so stop instead of steering at the goal handle.
			if head != nil && head.MoveState != orders.MoveArrived {
				if gx, gz, okGoal := s.moveGoalFor(handle, head); okGoal {
					directGoal = true
					directX = gx
					directZ = gz
				} else {
					arrived := s.finalGoalReached(u, hadRoute)
					s.emitMovementCallbacks(u, 0)
					return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: false, Moved: false, Arrived: arrived}
				}
			} else {
				arrived := s.finalGoalReached(u, hadRoute)
				s.emitMovementCallbacks(u, 0)
				return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: false, Moved: false, Arrived: arrived}
			}
		}
	}
	// Current waypoint or direct goal
	var wp Point
	var wpWorldX, wpWorldZ numeric.Fixed
	var dx, dz int64
	if directGoal {
		wpWorldX = directX
		wpWorldZ = directZ
		dx = int64(wpWorldX) - int64(u.X)
		dz = int64(wpWorldZ) - int64(u.Z)
		// For direct goal, keep wp as goal cell for pitch delta fallback (use goal cell)
		wp = Point{X: world.WorldToCell(directX), Z: world.WorldToCell(directZ)}
	} else {
		// Current waypoint: index 1 if available else 0 [task]
		if route.Count > 1 {
			wp = route.Points[1]
		} else {
			wp = route.Points[0]
		}
		wpWorldX = world.CellToWorld(wp.X)
		wpWorldZ = world.CellToWorld(wp.Z)
		wpWorldX = numeric.Fixed(int64(wpWorldX) + 524288) // 0.5 cell = 524288 = 1<<19 [03 §2.1]
		wpWorldZ = numeric.Fixed(int64(wpWorldZ) + 524288)
		dx = int64(wpWorldX) - int64(u.X)
		dz = int64(wpWorldZ) - int64(u.Z)
	}
	if dx == 0 && dz == 0 {
		d := s.distToGoal(u)
		arrived := s.finalGoalReached(u, hadRoute)
		s.emitMovementCallbacks(u, 0) // no delta => tier 0 [04 §5.2][GAP T15] C18
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: true, EmptyRoute: false, Moved: false, Arrived: arrived}
	}
	desired := headingFromDelta(dx, dz)
	oldXRaw := int64(u.X)
	oldZRaw := int64(u.Z)
	var moved bool
	var blocked bool
	// Ground vs air branch [04 §10.1] C26
	if u.Def != nil && u.Def.CanFly {
		flight := s.Flights[handle]
		if flight == nil {
			d := s.distToGoal(u)
			s.emitMovementCallbacks(u, 0)
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: true, EmptyRoute: false, Moved: false, Arrived: s.finalGoalReached(u, hadRoute)}
		}
		flight.X = int32(u.X.Raw())
		flight.Y = int32(u.Y.Raw())
		flight.Z = int32(u.Z.Raw())
		flight.TargetX = int32(wpWorldX.Raw())
		flight.TargetZ = int32(wpWorldZ.Raw())
		flight.TargetHeading = desired
		if s.Terrain != nil && u.Def != nil {
			targetY := CruiseAltitudeForOffset(s.Terrain, wpWorldX, wpWorldZ, u.Def.CruiseAlt)
			flight.TargetY = int32(targetY.Raw())
		} else {
			flight.TargetY = int32(u.Y.Raw())
		}
		if flight.MaxVelocity == 0 && u.Def.MaxVelocity != 0 {
			flight.MaxVelocity = int32(u.Def.MaxVelocity)
		}
		if flight.Acceleration == 0 && u.Def.Acceleration != 0 {
			flight.Acceleration = int32(u.Def.Acceleration)
		}
		if flight.BrakeRate == 0 && u.Def.BrakeRate != 0 {
			flight.BrakeRate = int32(u.Def.BrakeRate)
		}
		flight.TurnRate = int32(u.Def.TurnRate)
		IntegrateFlight(flight) // [04 §10.1] C26–C30, arithmetic preserved
		u.X = numeric.Fixed(int64(flight.X))
		u.Y = numeric.Fixed(int64(flight.Y))
		u.Z = numeric.Fixed(int64(flight.Z))
		u.Move.Heading = flight.Heading
		u.Move.Speed = numeric.Fixed(int64(flight.Speed))
		s.emitMovementCallbacks(u, flight.Speed) // [04 §5.2][GAP T15] C18 flight path also uses MoveRate tiers with same thresholds
		moved = int64(u.X) != oldXRaw || int64(u.Z) != oldZRaw
		blocked = false
	} else {
		steer := s.Steers[handle]
		coll := s.Collisions[handle]
		if steer == nil || coll == nil {
			d := s.distToGoal(u)
			s.emitMovementCallbacks(u, 0)
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: true, EmptyRoute: false, Moved: false, Arrived: s.finalGoalReached(u, hadRoute)}
		}
		steer.X = int32(u.X.Raw())
		steer.Z = int32(u.Z.Raw())
		steer.HeightWord = int16(u.Y.Raw() >> 16)
		if s.Terrain != nil {
			steer.SeaLevel = s.Terrain.SeaLevel
		}
		if u.Def != nil {
			steer.MaxVelocity = int32(u.Def.MaxVelocity)
			steer.TurnRate = int32(u.Def.TurnRate)
			var f uint32
			if u.Def.CanHover {
				f |= 0x1000
			}
			if u.Def.Floater {
				f |= 0x80000
			}
			steer.DefFlags = f
			steer.PitchScale = int32(u.Def.PitchScale)
			steer.BankScale = int32(u.Def.BankScale)
			steer.Acceleration = int32(u.Def.Acceleration)
			steer.BrakeRate = int32(u.Def.BrakeRate)
		}
		steer.UpdateHeading(desired) // [04 §8.1] C20
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		steer.UpdatePitch(steer.PendingHeading) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		cap := steer.SpeedCapFromPitch() // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		hasWaypoint := true // hadRoute true implies waypoint (directGoal fallback also true) [04 §7.3] C14
		blockedPrev := coll != nil && coll.Blocked
		distRaw := int32(s.distToGoal(u).Raw())                              // 16.16 trunc toward zero [I3][01 §8]
		steer.UpdateSpeedWithBraking(cap, hasWaypoint, distRaw, blockedPrev) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		steer.Integrate()                                                    // [04 §8.1] C20: heading commit + fixed trig position step
		oldX := int64(u.X)
		oldZ := int64(u.Z)
		newX := int64(steer.X)
		newZ := int64(steer.Z)
		coll.X = int32(oldX)
		coll.Z = int32(oldZ)
		coll.Y = int32(u.Y.Raw())
		coll.VX = int32(newX - oldX)
		coll.VZ = int32(newZ - oldZ)
		coll.Speed = steer.Speed
		coll.Heading = steer.Heading
		if u.Def != nil {
			coll.MaxVelocity = int32(u.Def.MaxVelocity)
		}
		moverProfile := s.ProfileFor(handle) // [04 §6.1][04 §8.2] per-unit profile
		blockerID := -1
		perCell := func(c Cell) bool {
			if s.Grid != nil {
				if occ, ok := s.Grid.OccupantAt(c); ok && occ != coll.ID {
					blockerID = occ
					return false
				}
			}
			return true
		}
		aggregate := func() bool {
			if s.Terrain == nil {
				return true
			}
			bx, bz := coll.HalfBias()
			anchor := QuantizedAnchor(coll.X+coll.VX, coll.Z+coll.VZ, bx, bz)
			return moverProfile.IsPassableFootprint(s.Terrain, anchor.X, anchor.Z)
		}
		fastPath, isBlocked := coll.CommitOne(s.Grid, coll.Mode, perCell, aggregate) // [04 §8.2] C23 C24: sync clear-then-stamp before next slot
		blocked = isBlocked
		// Occupancy was committed (clear/commit/stamp, [04 §8.2] C22): record
		// the unit's occupancy-commit tick so the request revision pass of
		// [04 §6.1 R-DOC04-B] sees it. The same-cell fast path commits the
		// transform without restamping occupancy [04 §8.2] C23, so it writes
		// no commit tick.
		// TODO(question): the occupancy-commit WRITE SITES are only partially
		// established. (a) The owner/building-mask writers (ClassLayer
		// SetOwnerRect/ClearOwnerRect) are Supported inference with no located
		// retail writer; a traced building-commit writer would settle where
		// retail sets and clears the requester's mask bits. (b) Whether the
		// creation-time occupancy stamp (EnsureUnit) also writes the unit
		// record's commit-tick field is untraced. Until the mask writers are
		// wired the mask stays all-zero and the search binds the terrain stamp
		// only (see searchFunc), so the bit-miss value 2 never occurs in
		// production — matching the pre-layer terrain-only search.
		if !isBlocked && !fastPath {
			s.noteOccupancyCommit(handle, tick)
		}
		coll.BlockerID = blockerID
		u.X = numeric.Fixed(int64(coll.X))
		u.Z = numeric.Fixed(int64(coll.Z))
		if s.Terrain != nil {
			u.Y = s.Terrain.HeightAt(u.X, u.Z)
			if u.Y == numeric.Fixed(-1) {
				u.Y = numeric.Fixed(int64(coll.Y))
			}
		}
		steer.X = coll.X
		steer.Z = coll.Z
		steer.Heading = coll.Heading
		steer.Speed = coll.Speed
		u.Move.Heading = coll.Heading
		u.Move.Speed = numeric.Fixed(coll.Speed)
		// Emit StartMoving/StopMoving/MoveRateN and setSFXoccupy per [04 §5.2][GAP T15] C17 C18 via immediate barrier [GAP T15] C18.
		// Must run after speed commit so tier reflects current capped speed [04 §5.2][GAP T15] C18.
		s.emitMovementCallbacks(u, coll.Speed)
		moved = int64(u.X) != oldXRaw || int64(u.Z) != oldZRaw
	}
	// Arrival via goal tolerance, not merely route active [task]
	d2 := s.distToGoal(u)
	arrived := s.finalGoalReached(u, hadRoute)
	// Published routes consumed without duplicate submission: StepUnit does not
	// call SubmitMove; the scheduler's HasRequest gate in session path-submit
	// remains authority [04 §7.3] C11 C12.
	hasRouteAfter := route != nil && route.Active
	// EmptyRoute reports route absence at entry: with no route the mover still
	// steers straight at the order goal, so the flag and movement are orthogonal.
	return StepResult{Handle: handle, DistToGoal: d2, HasRoute: hasRouteAfter, EmptyRoute: !hadRoute, Moved: moved, Blocked: blocked, Arrived: arrived}
}

// IntegrateFlight is the can-fly branch wrapper [04 §10.1] C26–C30.
func IntegrateFlightForUnit(u *units.Unit, w *world.Terrain) {
	if u == nil || w == nil {
		return
	}
	// Transient flight state from unit def
	f := &FlightState{
		Mode:         2,
		X:            int32(u.X.Raw()),
		Y:            int32(u.Y.Raw()),
		Z:            int32(u.Z.Raw()),
		Speed:        0,
		Heading:      0,
		MaxVelocity:  int32(u.Def.MaxVelocity),
		Acceleration: int32(u.Def.Acceleration),
		BrakeRate:    int32(u.Def.BrakeRate),
		TurnRate:     int32(u.Def.TurnRate),
		TargetY:      int32(u.Y.Raw()),
	}
	if f.MaxVelocity == 0 {
		f.MaxVelocity = 65536
	}
	if f.Acceleration == 0 {
		f.Acceleration = 16384
	}
	// Drive toward order goal if present
	q := orders.QueueForUnit(u)
	if q != nil && q.LenPrimary() > 0 {
		head := q.Primary()[0]
		if head != nil {
			dx := int64(head.GoalX) - int64(u.X)
			dz := int64(head.GoalZ) - int64(u.Z)
			if dx != 0 || dz != 0 {
				f.TargetX = int32(head.GoalX.Raw())
				f.TargetZ = int32(head.GoalZ.Raw())
				f.TargetHeading = headingFromDelta(dx, dz)
			}
		}
	}
	IntegrateFlight(f)
	u.X = numeric.Fixed(int64(f.X))
	u.Y = numeric.Fixed(int64(f.Y))
	u.Z = numeric.Fixed(int64(f.Z))
}
