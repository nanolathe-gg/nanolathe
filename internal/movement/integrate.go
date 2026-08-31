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
	"math"
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

	Grid       *OccupancyGrid
	Scheduler  *path.Scheduler
	Routes     map[pool.Handle]*Route
	Steers     map[pool.Handle]*SteerState
	Collisions map[pool.Handle]*CollisionState
	// collisionHistory is allocated only for an opted-in parity capture. It is
	// appended after the complete movement sweep, never during diagnostic reads.
	collisionHistory        []collisionHistoryEntry
	collisionTraceEnabled   bool
	collisionHistoryLimit   int
	collisionHistoryDropped bool
	Flights                 map[pool.Handle]*FlightState
	profiles                map[pool.Handle]Profile // per-unit resolved movement profile [04 §6.1]
	profileNames            map[pool.Handle]string  // per-unit canonical class key the profile resolved from [04 §6.1]; lookup-only [I1]
	sessions                []*path.Session         // deterministic slice indexed by handle [04 §7.3] C11 C12 budget-honoring sessions
	prevMoveTier            map[pool.Handle]int     // cached mover tier per unit for MoveRate edge emission [04 §5.2][GAP T15] C18
	prevSFXBand             map[pool.Handle]int     // cached setSFXoccupy band per unit for edge emission [04 §5.2][GAP T15] C17 C18

	// world is the units world bound via BindWorld (or via Tick for legacy path).
	// StepUnit needs it to fetch the *units.Unit for a handle without passing
	// the world on every per-unit call, so the caller can invoke StepUnit
	// inside its own slot visit [04 §1.1] sweep order.
	world *units.World

	// layerRegistry is the per-class stamped passability layer registry
	// [04 §6.1 R-DOC04-B]. It is constructed from the terrain, the occupancy
	// grid and this System's own committed-anchor adapter. It may be created by
	// an occupancy commit before the unit world is bound; every BindWorld call
	// refreshes its revision-pass world [04 §6.1 R-DOC04-B].
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
	pathProvider   *pathProvider
	// AirSectors is the coarse second grid the map loader builds after the
	// terrain is decoded: 128-world-unit cells whose smoothed byte is the
	// maximum terrain height over the 3x3 block of sectors around each one
	// [04 R-AIR-01 §5]. It is the source the per-tick cruise-altitude rule of
	// [04 R-AIR-01 §1] step 4 reads — explicitly NOT the four-corner terrain
	// query — and the sentinel test the off-map recovery legs run. It is built
	// once, with the terrain, and never rebuilt.
	AirSectors *AirSectorGrid
	// airOrders is the movement-side dispatch state of each aircraft's current
	// air order record. It replaces the takeoffClimb map: the initial climb is
	// now a goal payload on the flight command block, not a separate altitude
	// cache [04 R-AIR-01 §1][04 R-AIR-01 §6]. Lookup-only; never ranged over
	// [I1].
	airOrders map[pool.Handle]*airOrderState
	// These are session-owned lobby values. Zero keeps path scheduling inert
	// until the session supplies explicit limits [04 R-PATH-01 §6].
	PathPlayers   int
	PathUnitLimit int32
}

// pathProvider is the movement-owned candidate surface. Submit/Cancel only
// mutate this stable-slot source; path itself owns no compatibility queue.
// The per-route follower state arms submissions through serviceGroundFollower
// at the established 60-tick cadence [04 R-MOV-01 §3][04 R-MOV-01 §7].
type pathProvider struct {
	requests [10][]path.Request
	cursor   [10]int
	players  int
	limit    int32
}

func (s *System) CancelPathRequest(h pool.Handle) bool {
	if s == nil || s.Scheduler == nil {
		return false
	}
	canceled := s.Scheduler.Cancel(h)
	if int(h) < len(s.sessions) {
		s.sessions[int(h)] = nil
	}
	return canceled
}
func (s *System) HasPathRequest(h pool.Handle) bool {
	if s == nil || s.Scheduler == nil {
		return false
	}
	return s.Scheduler.HasRequest(h)
}

// PathRequestsSnapshot returns the provider's deterministic request order for
// construction diagnostics. The scheduler itself exposes no queue mutation or
// queue inspection facade.
func (s *System) PathRequestsSnapshot() []path.Request {
	if s == nil || s.pathProvider == nil {
		return nil
	}
	return s.pathProvider.allRequests()
}
func (p *pathProvider) PlayerCount() int { return p.players }
func (p *pathProvider) UnitLimit() int32 { return p.limit }
func (p *pathProvider) Eligible(player int) bool {
	// Eligibility is player-record existence, not queue non-emptiness. Retail
	// accrues and spends the equal share while polling that player's followers
	// even when none currently wants a route [04 R-PATH-01 §6].
	return player >= 0 && player < p.players
}
func (p *pathProvider) Poll(player int) (path.Request, path.PollResult) {
	if !p.Eligible(player) || len(p.requests[player]) == 0 {
		return path.Request{}, path.PollNoUnit
	}
	q := p.requests[player]
	i := p.cursor[player] % len(q)
	r := q[i]
	// A request is removed only when admitted; the cursor advances and wraps
	// on every visit, preserving stable follower polling [04 R-PATH-01 §6].
	p.cursor[player] = (i + 1) % len(q)
	p.requests[player] = append(q[:i], q[i+1:]...)
	if p.cursor[player] >= len(p.requests[player]) && len(p.requests[player]) > 0 {
		p.cursor[player] = 0
	}
	return r, path.PollRequest
}
func (p *pathProvider) Submit(r path.Request) {
	if int(r.Player) >= len(p.requests) {
		return
	}
	q := p.requests[r.Player]
	for player := range p.requests {
		for i := range p.requests[player] {
			if p.requests[player][i].Unit == r.Unit {
				p.requests[player] = append(p.requests[player][:i], p.requests[player][i+1:]...)
				break
			}
		}
	}
	q = p.requests[r.Player]
	q = append(q, r)
	p.requests[r.Player] = q
}
func (p *pathProvider) Cancel(unit pool.Handle) bool {
	for player := range p.requests {
		for i := range p.requests[player] {
			if p.requests[player][i].Unit == unit {
				p.requests[player] = append(p.requests[player][:i], p.requests[player][i+1:]...)
				return true
			}
		}
	}
	return false
}
func (p *pathProvider) HasRequest(unit pool.Handle) bool {
	for player := range p.requests {
		for _, r := range p.requests[player] {
			if r.Unit == unit {
				return true
			}
		}
	}
	return false
}
func (p *pathProvider) pending(player uint8) int { return len(p.requests[player]) }
func (p *pathProvider) allRequests() []path.Request {
	var out []path.Request
	for player := range p.requests {
		out = append(out, p.requests[player]...)
	}
	return out
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
	// border is the rectangle whose perimeter is the goal-cell enumeration for a
	// rectangle-perimeter goal. When set, arrival is membership of that border
	// and the point test above is not used: "enumerated goal cells are exactly
	// the rectangle border, where h is 0, and arrival requires lying on that
	// border" [04 §7.2], which [04 R-FAC-02 §4] names as the arrival rule for a
	// no-rally product's `Park`.
	border *path.Rect
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

// goalCellForWorld applies the same footprint-anchor quantisation as the
// movement commit: floor((goal + halfCell - footprint*halfCell) / cell)
// [04 R-COLL-01 §1][R-P0-01]. A placement centre therefore maps back to
// its rectangle anchor for every footprint size.
func goalCellForWorld(goal numeric.Fixed, foot int32) int32 {
	if foot <= 0 {
		foot = 1
	}
	half := int64(0x80000) // 1<<19 half cell [R-P0-01][03 §2.1]
	cell := int64(1 << 20) // 0x100000 one cell [03 §2.1]
	v := int64(goal) + half - int64(foot)*half
	return int32(floorDiv(v, cell))
}

func (s *System) pathFootprint(u *units.Unit) (int32, int32) {
	if s != nil && u != nil {
		if coll := s.Collisions[u.Handle]; coll != nil {
			return int32(coll.FootPrintX), int32(coll.FootPrintZ)
		}
		profile := s.ProfileFor(u.Handle)
		fx, fz := int32(profile.FootPrintX), int32(profile.FootPrintZ)
		if u.Def != nil {
			if fx <= 0 {
				fx = u.Def.FootprintX
			}
			if fz <= 0 {
				fz = u.Def.FootprintZ
			}
		}
		if fx <= 0 {
			fx = 1
		}
		if fz <= 0 {
			fz = 1
		}
		return fx, fz
	}
	return 1, 1
}

// pathStartCell returns the committed footprint anchor required by request
// setup. WorldToCell is not equivalent for footprints larger than one cell
// [04 R-PATH-01 §4][04 R-COLL-01 §1].
func (s *System) pathStartCell(u *units.Unit) path.Cell {
	if s != nil && u != nil {
		if coll := s.Collisions[u.Handle]; coll != nil {
			return path.Cell{X: coll.CachedAnchor.X, Z: coll.CachedAnchor.Z}
		}
		fx, fz := s.pathFootprint(u)
		return path.Cell{X: goalCellForWorld(u.X, fx), Z: goalCellForWorld(u.Z, fz)}
	}
	return path.Cell{}
}

// livePathOrder resolves the status sink captured by a path request. Search
// setup and publication can both finish after an order replacement, so all
// three identities must still agree before either boundary wakes an order:
// the live unit slot, the activation token, and the current queue-head node
// [04 R-PATH-01 §7][04 R-PATH-01 §9].
func (s *System) livePathOrder(r path.Request) (*units.Unit, *orders.Node, bool) {
	if s == nil || s.world == nil || r.Activation == 0 {
		return nil, nil, false
	}
	binding := s.activeOrders[r.Unit]
	if binding == nil || binding.order == nil || binding.token != r.Activation {
		return nil, nil, false
	}
	u := s.world.Unit(r.Unit)
	if u == nil || u.Handle != r.Unit {
		return nil, nil, false
	}
	q, ok := u.Orders.(*orders.Queue)
	if !ok || q == nil || q.Head() != binding.order {
		return nil, nil, false
	}
	return u, binding.order, true
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
		AirSectors:     NewAirSectorGrid(terrain), // built once at map load [04 R-AIR-01 §5]
		airOrders:      make(map[pool.Handle]*airOrderState),
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
		// This composition root is a single-player system. A session with a
		// lobby must overwrite these with its explicit values before ticking.
		PathPlayers:   1,
		PathUnitLimit: 1,
	}
	sched := path.NewScheduler(s.searchFunc, s.publishFunc)
	s.pathProvider = &pathProvider{players: s.PathPlayers, limit: s.PathUnitLimit}
	sched.SetCandidateProvider(s.pathProvider)
	// Use DefaultBase unless overridden [P0-I16]; no longer reads mutable global.
	sched.SetBase(path.DefaultBase)
	s.Scheduler = sched
	return s
}

// ConfigurePath supplies the session topology that owns the scheduler's
// equal-share divisor and pressure tiers [04 R-PATH-01 §6]. Production calls
// this after the economy player records and sliced unit pool exist.
func (s *System) ConfigurePath(players int, unitLimit int32) {
	if s == nil || s.pathProvider == nil || s.Scheduler == nil {
		return
	}
	if players < 0 || players > 10 || unitLimit <= 0 {
		return
	}
	s.PathPlayers = players
	s.PathUnitLimit = unitLimit
	s.pathProvider.players = players
	s.pathProvider.limit = unitLimit
	s.Scheduler.SetCandidateProvider(s.pathProvider)
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
	// The layer registry must use the current bound unit world for the request
	// revision pass [04 §6.1 R-DOC04-B]. Occupancy publication can allocate it
	// before this bind, so refresh an existing registry as well as constructing
	// one for the Tick entry point.
	if s.layerRegistry == nil {
		s.layerRegistry = s.newLayerRegistry()
	}
	s.layerRegistry.BindWorld(w)
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

const routeLookaheadRaw = int64(80 << 16)

// groundHypotRaw reproduces the follower's double-precision hypot followed by
// truncation toward zero. The inputs and result remain raw 16.16 values; the
// float is only the established working-precision temporary [04 R-MOV-01 §3]
// [04 R-PATH-01 §8][I2].
func groundHypotRaw(dx, dz int64) int64 {
	return int64(math.Hypot(float64(dx), float64(dz)))
}

// routeTargets emits the follower's clamped T0/T1/T2 triples and applies the
// 80-world-unit pullback to T1 [04 R-MOV-01 §3].
func routeTargets(route *Route, unitX, unitZ int32) (t1x, t1z, t2x, t2z int32) {
	point := func(i int) Point {
		last := int(route.Count) - 1
		if i > last {
			i = last
		}
		return route.Points[i]
	}
	t0, t1, t2 := point(0), point(1), point(2)
	t0x, t0z := int64(t0.X)<<16, int64(t0.Z)<<16
	x1, z1 := int64(t1.X)<<16, int64(t1.Z)<<16
	dx1, dz1 := x1-int64(unitX), z1-int64(unitZ)
	d1 := groundHypotRaw(dx1, dz1)
	if d1 > routeLookaheadRaw {
		sx, sz := x1-t0x, z1-t0z
		length := groundHypotRaw(sx, sz)
		if length >= 1<<16 {
			ux, uz := (sx<<16)/length, (sz<<16)/length
			pull := d1 - routeLookaheadRaw
			if pull > length {
				pull = length
			}
			x1 -= (ux * pull) >> 16
			z1 -= (uz * pull) >> 16
		}
	}
	return int32(x1), int32(z1), int32(int64(t2.X) << 16), int32(int64(t2.Z) << 16)
}

// followerAccelerates evaluates the two strict distance gates which select
// +Acceleration instead of -BrakeRate [04 R-MOV-01 §4]. Valid mobile
// content authors non-zero TurnRate and BrakeRate; synthetic partial
// definitions retain their pre-existing forward-progress behavior.
func followerAccelerates(s *SteerState, desired uint16, unitX, unitZ, t1x, t1z, t2x, t2z int32) bool {
	if s == nil {
		return false
	}
	// Authored mobile units provide both divisors. Preserve Nanolathe's
	// existing bounded behavior for synthetic/partial definitions rather than
	// reproducing the retail divide fault [04 R-MOV-01 §4].
	if s.TurnRate == 0 || s.BrakeRate == 0 {
		return true
	}
	err := int64(headingDelta(s.Heading, desired))
	if err < 0 {
		err = -err
	}
	err &= 0xffff
	turnDist := (err * int64(s.Speed)) / int64(s.TurnRate)
	stopDist := (((int64(s.Speed) * int64(s.Speed)) >> 16) << 16) / (2 * int64(s.BrakeRate))
	dx1, dz1 := int64(t1x)-int64(unitX), int64(t1z)-int64(unitZ)
	dx2, dz2 := int64(t2x)-int64(unitX), int64(t2z)-int64(unitZ)
	a := ((dx1 * dx1) >> 32) + ((dz1 * dz1) >> 32)
	b := ((dx2 * dx2) >> 32) + ((dz2 * dz2) >> 32)
	turnGate := ((turnDist * turnDist) >> 32) * 4
	stopGate := (stopDist * stopDist) >> 32
	return a > turnGate && b > stopGate
}

// groundPostMoveHeight covers the model-independent post-move branches. The
// selection-primitive conform for other vehicles needs compiled model ground
// plate geometry and remains at its call site [04 R-MOV-01 §5][§9].
func groundPostMoveHeight(t *world.Terrain, u *units.Unit) (numeric.Fixed, bool) {
	if t == nil || u == nil {
		return 0, false
	}
	if u.Def != nil && !u.Def.Upright && u.Def.Floater {
		return numeric.Fixed((int64(t.SeaLevel) - int64(u.Def.Waterline)) << 16), true
	}
	terrainY := t.HeightAt(u.X, u.Z)
	if terrainY == numeric.Fixed(-1) {
		return 0, false
	}
	if u.Def != nil && u.Def.Upright && u.Def.CanHover {
		waterY := numeric.Fixed((int64(t.SeaLevel) - int64(u.Def.Waterline)) << 16)
		if terrainY < waterY {
			return waterY, true
		}
	}
	return terrainY, true
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
		wpX := int64(last.X) * 65536
		wpZ := int64(last.Z) * 65536
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
// onRectBorder reports whether cell (x, z) lies on rect's border — the exact
// enumeration a rectangle-perimeter goal admits, with h of 0 [04 §7.2]. Min and
// Max are inclusive, so a degenerate rectangle is its own border.
func onRectBorder(rect path.Rect, x, z int32) bool {
	if x < rect.Min.X || x > rect.Max.X || z < rect.Min.Z || z > rect.Max.Z {
		return false
	}
	return x == rect.Min.X || x == rect.Max.X || z == rect.Min.Z || z == rect.Max.Z
}

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
	// A rectangle-perimeter goal arrives on membership of the border, not on
	// proximity to one cell [04 §7.2][04 R-FAC-02 §4].
	if ah.border != nil {
		if onRectBorder(*ah.border, tileX, tileZ) {
			ah.order.Satisfied |= arrivalSatisfiedBit // [R-P0-01] OR 0x20
			return hadRoute
		}
		return false
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
	// SteerState [M2][M3] with pitch accumulator and accel/brake plumbing.
	//
	// The mover's records start from the heading the unit already carries, not
	// from zero. The allocator is the only heading writer in the common
	// creation path and it draws the initial heading from `buildangle`
	// [04 §2.3b]; a mover record seeded with 0 would have the first movement
	// step commit that 0 back over the allocated heading and silently discard
	// the build angle of every finished unit.
	steer := &SteerState{
		X:              int32(u.X.Raw()),
		Z:              int32(u.Z.Raw()),
		Heading:        u.Move.Heading,
		PendingHeading: u.Move.Heading,
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
		Heading:     u.Move.Heading, // allocated `buildangle` heading [04 §2.3b]
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
		// Every successful stamp writes the occupant-age clock first. Recording
		// the same tick here lets a later request revision cross a stationary
		// building into the shared class layer [04 R-COLL-01 §4]
		// [04 R-PATH-01 §2][04 §6.1 R-DOC04-B].
		if s.Grid.Stamp(anchor, footX, footZ, coll.ID) {
			s.noteOccupancyCommit(h, s.tick)
		}
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
			Heading:              u.Move.Heading, // allocated `buildangle` heading [04 §2.3b]
			TargetHeading:        u.Move.Heading,
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
	if s.pathProvider != nil {
		s.pathProvider.Submit(req)
	}
}

func (s *System) submitMoveForOrder(u *units.Unit, head *orders.Node, start, goal path.Cell, activation uint64) {
	// OW-3-P: select Goal family per order [04 §7.2][04 §7.4][04 §3.5] — Annulus for attack/guard stand-off where retail establishes it, Point otherwise.
	// RectPerimeterGoal remains unwired because no established order producer exists [04 §7.2][04 §7.4][M-4].
	fx, fz := s.pathFootprint(u)
	goalObj := s.goalForOrderWithFootprint(goal, head, fx, fz)
	s.submitGoalForOrder(u, start, goalObj, activation)
}

func (s *System) submitGoalForOrder(u *units.Unit, start path.Cell, goalObj path.Goal, activation uint64) {
	req := path.Request{
		Unit:       u.Handle,
		Player:     u.Owner,
		Start:      start,
		Goal:       goalObj,
		Activation: activation,
	}
	if s.pathProvider != nil {
		s.pathProvider.Submit(req)
	}
}

func (s *System) staticObstacleRevision() uint64 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return s.Terrain.StaticObstacleRevision()
}

// ActivateMove binds the current primary order head and, for a fresh route,
// submits one path request. The queue head is the authority: a repeated call
// for the same node is a no-op, while a new node cancels the old request and
// invalidates its route before submitting the replacement. A usable active
// route restored without derived bindings is adopted without another request;
// its follower retains the route's existing request-poll state [04 R-MOV-01
// §3][04 R-MOV-01 §7][04 R-PATH-01 §8].
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
	// The air executors' phase 0 is the shared takeoff preamble, but it does not
	// run here: it is the first leg the air executor of [04 R-AIR-01 §6] runs
	// from the mover tick, where it installs the climb marker as the record's
	// goal payload [04 R-AIR-01 §1]. Path activation is a ground concern.
	if old, ok := s.activeOrders[u.Handle]; ok && old.order == head {
		return false // exactly one submission per active order
	}
	_, wasBound := s.activeOrders[u.Handle]
	if wasBound {
		s.CancelPathRequest(u.Handle)
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
	binding := &activeMove{order: head, token: token}
	s.activeOrders[u.Handle] = binding
	if route := s.Routes[u.Handle]; !wasBound && usableActiveRoute(u, route) && !s.HasPathRequest(u.Handle) {
		// The route is persisted but its order/goal binding is derived. Adoption
		// does not stamp request-poll state; the wants-repath poll is the writer of
		// LastRequestTick [04 R-MOV-01 §7][04 R-PATH-01 §8].
		s.bindArrivalHandle(u, head)
		return true
	}
	start, goal, selectedPoint, _ := s.pathCellsForOrder(u, head)
	// Path search is aimed at the goal handle, so a replan after a dynamic
	// block re-paths to the same point the mover was already steering at —
	// for a build order that is the selected perimeter candidate, not the
	// site centre [04 §8.3][04 §7.4].
	fx, fz := s.pathFootprint(u)
	goalObj := s.goalForOrderWithFootprint(goal, head, fx, fz)
	s.bindRectSteeringGoal(u, head, goalObj, fx, fz)
	if route := s.Routes[u.Handle]; route != nil && !selectedPoint {
		goalPointX, goalPointZ, haveGoalPoint := groundGoalPoint(goalObj, u, fx, fz)
		installGroundGoal(route, u, goalObj, goalPointX, goalPointZ, haveGoalPoint, true, s.staticObstacleRevision(), s.tick)
		route.LastRequestTick = s.tick
	}
	s.submitGoalForOrder(u, start, goalObj, token)
	s.bindArrivalHandle(u, head)
	return true
}

// bindRectSteeringGoal is the rectangle-perimeter case of the goal-handle bind
// described in movegoal.go: a Park record's stored position is the rectangle's
// origin, not a cell the mover is supposed to stand on [04 R-ORD-01 §2]
// [04 R-FAC-02 §4]. Binding the perimeter point the follower already steers by
// keeps the steering target, the distance threshold and the arrival test on one
// cell, exactly as the build-order bind does for its selected candidate.
func (s *System) bindRectSteeringGoal(u *units.Unit, head *orders.Node, goalObj path.Goal, fx, fz int32) {
	if s == nil || u == nil || head == nil || goalObj == nil {
		return
	}
	if _, isRect := path.IsRectGoal(goalObj); !isRect {
		return
	}
	if gx, gz, ok := groundGoalPoint(goalObj, u, fx, fz); ok {
		s.BindMoveGoal(u.Handle, head, gx, gz)
	}
}

func (s *System) pathCellsForOrder(u *units.Unit, head *orders.Node) (start, goal path.Cell, selectedPoint, ok bool) {
	goalX, goalZ, ok := s.moveGoalFor(u.Handle, head)
	name := orders.DescriptorFor(head.ID).Name
	if name == "MobileBuild" || name == "VTOL_MobileBuild" {
		// The selected approach remains in the construction producer's whole-cell
		// domain, but every admitted request copies the mover's cached committed
		// cell as its start [04 R-PATH-01 §4 step 1].
		return s.pathStartCell(u),
			path.Cell{X: world.WorldToCell(goalX), Z: world.WorldToCell(goalZ)}, true, ok
	}
	fx, fz := s.pathFootprint(u)
	return s.pathStartCell(u), path.Cell{X: goalCellForWorld(goalX, fx), Z: goalCellForWorld(goalZ, fz)}, false, ok
}

func usableActiveRoute(u *units.Unit, route *Route) bool {
	if u == nil || route == nil || !route.Active {
		return false
	}
	if u.Def != nil && u.Def.CanFly {
		return route.Count > 0
	}
	return route.Count > 1
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
	s.CancelPathRequest(u.Handle)
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
	start, goal, selectedPoint, _ := s.pathCellsForOrder(u, head)
	// Path search is aimed at the goal handle, so a refresh re-paths to the
	// same point the mover was already steering at — for a build order that is
	// the selected perimeter candidate, not the site centre [04 §8.3][04 §7.4].
	fx, fz := s.pathFootprint(u)
	goalObj := s.goalForOrderWithFootprint(goal, head, fx, fz)
	s.bindRectSteeringGoal(u, head, goalObj, fx, fz)
	if route := s.Routes[u.Handle]; route != nil && !selectedPoint {
		goalPointX, goalPointZ, haveGoalPoint := groundGoalPoint(goalObj, u, fx, fz)
		installGroundGoal(route, u, goalObj, goalPointX, goalPointZ, haveGoalPoint, true, s.staticObstacleRevision(), s.tick)
		route.LastRequestTick = s.tick
	}
	s.submitGoalForOrder(u, start, goalObj, token)
	s.bindArrivalHandle(u, head)
	return true
}

// serviceGroundFollower runs the route follower's movement-tick service before
// steering. It consumes at most one reached waypoint, arms a repath when the
// previous commit was blocked or no waypoint remains, and submits at most once
// per 60 ticks without discarding a still-usable route [04 R-MOV-01 §3]
// [04 R-MOV-01 §7]. The scheduler runs earlier in the tick, so a request
// admitted here becomes eligible at the next scheduler boundary.
func (s *System) serviceGroundFollower(u *units.Unit, head *orders.Node, route *Route, tick uint32) {
	if s == nil || u == nil || head == nil || route == nil || (u.Def != nil && u.Def.CanFly) {
		return
	}
	if route.Active && route.Count > 1 {
		route.Prune(Point{X: int32(int64(u.X) >> 16), Z: int32(int64(u.Z) >> 16)})
	}
	blocked := false
	if coll := s.Collisions[u.Handle]; coll != nil {
		blocked = coll.Blocked
	}
	if blocked || !route.Active || route.Count < 2 {
		route.WantsRepath = true
	}
	if !route.WantsRepath || route.LastRequestTick+60 > tick || s.HasPathRequest(u.Handle) {
		return
	}
	binding := s.activeOrders[u.Handle]
	if binding == nil || binding.order != head {
		return
	}
	start, goal, _, ok := s.pathCellsForOrder(u, head)
	if !ok {
		return
	}
	s.submitMoveForOrder(u, head, start, goal, binding.token)
	route.LastRequestTick = tick
}

// DeactivateMove drops the path binding for a unit whose active order is no
// longer path-backed.  It is intentionally idempotent so every queue/head
// transition can pass through the same boundary.
func (s *System) DeactivateMove(handle pool.Handle) {
	if s == nil {
		return
	}
	if s.Scheduler != nil {
		s.CancelPathRequest(handle)
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
	// Park joins the family: its phase 1 completes on the arrival bit this
	// handle sets [04 R-ORD-01 §2][04 R-FAC-02 §4].
	case "Move_Ground", "VTOL_Move", "QMove", "Patrol", "QPatrol", "VTOL_Patrol", "RepairPatrol", "VTOL_RepairPatrol", "Park":
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
	ah := &arrivalHandle{order: head, goalX: goalX, goalZ: goalZ, threshSq: threshSq}
	// A rectangle-perimeter goal does not arrive at a point. Its enumerated goal
	// cells are exactly the rectangle border and "arrival requires lying on that
	// border" [04 §7.2] — the rule [04 R-FAC-02 §4] cites for the `Park` a
	// no-rally factory product receives. The steering point bound beside this
	// handle is one border cell chosen to steer at ([04 R-MOV-03 §2]'s far-edge
	// midpoint); testing arrival against that one cell made every OTHER border
	// cell a non-arrival, so a mover that reached the border anywhere else — the
	// A* endpoint whenever the published route is long enough to be accepted, or
	// a blocked mover clamped along the edge — never retired its record. The
	// column of products behind a factory is a separate, retail-faithful shape
	// [04 R-EGRESS-01]; this is the arrival predicate only.
	if minX, minZ, maxX, maxZ, ok := orders.ParkGoalRect(head); ok {
		ah.border = &path.Rect{Min: path.Cell{X: minX, Z: minZ}, Max: path.Cell{X: maxX, Z: maxZ}}
	}
	s.arrivalHandles[u.Handle] = ah
	// [R-P0-01] initial gate must be 0 so phase 0 handler can arm 0xE0; otherwise static 0x402 would block.
	if head.Phase == 0 && head.DynamicGate != 0 {
		// Only clear initial static gate; preserve armed 0xE0 for re-binds after a replan where Phase already 1
		head.DynamicGate = 0
		head.Deadline = -1
	}
}

func (s *System) headingFor(h pool.Handle) uint16 {
	if steer := s.Steers[h]; steer != nil {
		return steer.Heading
	}
	if s.world != nil {
		if u := s.world.Unit(h); u != nil {
			return u.Move.Heading
		}
	}
	return 0
}

// searchFunc is the injected SearchFunc bound to path.Search with profile passability
// over System.Terrain and occupancy. It honors the 100-pops-per-request-per-call
// budget and full-or-empty publication [04 §7.3] C11 C12 via a resumable Session
// per unit held in deterministic slice storage indexed by handle [I1].
func (s *System) searchFunc(r path.Request, scale int32, budget int) path.WorkResult {
	if s == nil {
		return path.WorkResult{Status: path.StatusRejected, Done: true}
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
				Start:      r.Start,
				Goal:       r.Goal,
				Scale:      scale,
				FootPrintX: int32(profile.FootPrintX),
				FootPrintZ: int32(profile.FootPrintZ),
				StartDir:   uint8((s.headingFor(r.Unit) + 0x1000) >> 13 & 7),
				HasBounds:  hasBounds,
				Bounds:     bounds,
				PassableValue: func(c path.Cell) uint8 {
					return layer.Value(c.X, c.Z)
				},
				Revise: func() {
					reg.ReviseFor(cls, profile, requester, revTick)
				},
			}
		} else {
			// No authored terrain layer is available; keep this request inert
			// rather than inventing permissive passability.
			cfg = path.SearchConfig{
				Start:         r.Start,
				Goal:          r.Goal,
				Scale:         scale,
				FootPrintX:    int32(profile.FootPrintX),
				FootPrintZ:    int32(profile.FootPrintZ),
				StartDir:      uint8((s.headingFor(r.Unit) + 0x1000) >> 13 & 7),
				HasBounds:     hasBounds,
				Bounds:        bounds,
				PassableValue: func(path.Cell) uint8 { return 0 },
			}
		}
		sess = path.NewSession(cfg)
		s.sessions[idx] = sess
		// Request setup reports its established 0x100/0x200 notification to
		// the goal object's owning order even when search work continues. These
		// bits are distinct from the final route diagnostic [04 R-PATH-01
		// §4][04 R-PATH-01 §9].
		if notified := sess.Notified(); notified != 0 {
			if _, order, live := s.livePathOrder(r); live {
				order.Satisfied |= uint32(notified)
			}
		}
	}
	before := sess.Popped()
	points, status, done := sess.Resume(budget) // [04 §7.3] C11 budget, C12 full-or-empty
	setup := 0
	if needsNew {
		setup = sess.SetupSteps()
	}
	pops := sess.Popped() - before
	if done {
		s.sessions[idx] = nil
	}
	return path.WorkResult{Points: points, Status: status, Done: done, SetupSteps: setup, Pops: pops}
}

// groundGoalPoint applies the three established goal-point queries. Point and
// annulus cells use the owning unit's footprint bias; rectangle goals use the
// middle X column and far Z edge [04 R-MOV-03 §2].
func groundGoalPoint(goal path.Goal, u *units.Unit, footX, footZ int32) (numeric.Fixed, numeric.Fixed, bool) {
	if goal == nil || u == nil {
		return 0, 0, false
	}
	trace := path.DescribeGoal(goal)
	if trace.Unknown {
		return 0, 0, false
	}
	cellWorld := func(cell, foot int32) numeric.Fixed {
		return numeric.Fixed((int64(cell)*16 + int64(foot)*8) << 16)
	}
	switch trace.Kind {
	case 1:
		return cellWorld(trace.Center.X, footX), cellWorld(trace.Center.Z, footZ), true
	case 2:
		centerX := cellWorld(trace.Center.X, footX)
		centerZ := cellWorld(trace.Center.Z, footZ)
		dx := int64(u.X) - int64(centerX)
		dz := int64(u.Z) - int64(centerZ)
		bearing := numeric.AngleFromAtan2(dx, dz)
		radius := int64(int32(trace.A+trace.B)/2) << 16
		// The shared table biases the angle by 0x20 before selecting one of its
		// 512 entries [04 R-MOV-01 §4].
		tableAngle := bearing + 0x20
		offsetX := (radius*int64(numeric.Sin(tableAngle)) + 0x1000) >> 13
		offsetZ := (radius*int64(numeric.Cos(tableAngle)) + 0x1000) >> 13
		return centerX + numeric.Fixed(offsetX), centerZ + numeric.Fixed(offsetZ), true
	case 3:
		midX := int32(trace.Rect.Min.X+trace.Rect.Max.X) / 2
		return cellWorld(midX, footX), cellWorld(trace.Rect.Max.Z, footZ), true
	default:
		return 0, 0, false
	}
}

// acceptGroundRoute applies the follower's acceptance gates to the currently
// held points. Terminal acceptance clears wants-repath; half-distance
// acceptance and the synthetic two-point route keep it armed [04 R-PATH-01 §8].
func acceptGroundRoute(route *Route, u *units.Unit, goal path.Goal, goalX, goalZ numeric.Fixed, haveGoalPoint, allowSynthetic bool, revision uint64, tick uint32) {
	if route == nil || goal == nil || u == nil {
		return
	}
	accepted := false
	if route.Count >= 3 {
		last := route.Points[route.Count-1]
		if goal.StartSatisfied(path.Cell{X: last.X >> 4, Z: last.Z >> 4}) {
			route.WantsRepath = false
			accepted = true
		} else if haveGoalPoint {
			dux, duz := int64(u.X)-int64(goalX), int64(u.Z)-int64(goalZ)
			dpx := (int64(last.X) << 16) - int64(goalX)
			dpz := (int64(last.Z) << 16) - int64(goalZ)
			du := groundHypotRaw(dux, duz)
			dp := groundHypotRaw(dpx, dpz)
			accepted = 2*dp < du
		}
	}
	if accepted {
		route.Active = true
	}
	if !accepted && haveGoalPoint && allowSynthetic {
		synthetic := []Point{
			{X: int32(u.X.Raw() >> 16), Z: int32(u.Z.Raw() >> 16)},
			{X: int32(goalX.Raw() >> 16), Z: int32(goalZ.Raw() >> 16)},
		}
		route.PublishAtRevision(synthetic, revision)
		route.WantsRepath = true
	} else if !accepted {
		route.Active = false
	}

	if tick-route.LastRequestTick > 10 {
		route.LastRequestTick = 0
	}
	route.Dirty = true
}

// installGroundGoal runs the same acceptance procedure when a new goal object
// is installed. With no held points this immediately installs the established
// two-point straight-line route, so steering need not wait for the
// asynchronous search publication [04 R-PATH-01 §8].
func installGroundGoal(route *Route, u *units.Unit, goal path.Goal, goalX, goalZ numeric.Fixed, haveGoalPoint, allowSynthetic bool, revision uint64, tick uint32) {
	if route == nil {
		return
	}
	route.Active = false
	route.WantsRepath = goal != nil
	acceptGroundRoute(route, u, goal, goalX, goalZ, haveGoalPoint, allowSynthetic, revision, tick)
}

// installGroundRoute publishes points, then applies the fixed acceptance
// order. Empty publications only clear wants-repath; the per-tick follower
// re-arms it and observes the 60-tick throttle [04 R-PATH-01 §8].
func installGroundRoute(route *Route, u *units.Unit, goal path.Goal, goalX, goalZ numeric.Fixed, haveGoalPoint, allowSynthetic bool, points []Point, revision uint64, tick uint32) {
	if route == nil {
		return
	}
	route.PublishAtRevision(points, revision)
	if len(points) == 0 {
		return
	}
	route.WantsRepath = true
	acceptGroundRoute(route, u, goal, goalX, goalZ, haveGoalPoint, allowSynthetic, revision, tick)
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
	boundUnit, boundOrder, liveBinding := s.livePathOrder(r)
	if _, bound := s.activeOrders[r.Unit]; bound && !liveBinding {
		return
	}
	if len(points) == 0 && liveBinding && r.Goal != nil && !r.Goal.StartSatisfied(s.pathStartCell(boundUnit)) {
		// Empty publication, not search rejection itself, is retail's
		// "cannot get there" notification [04 R-PATH-01 §7][04
		// R-PATH-01 §9][04 R-COLL-01 §6].
		boundOrder.Satisfied |= 0x40
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
	if s.world != nil {
		u := s.world.Unit(r.Unit)
		if u != nil && (u.Def == nil || !u.Def.CanFly) {
			goalX, goalZ, haveGoal := numeric.Fixed(0), numeric.Fixed(0), false
			allowSynthetic := false
			fx, fz := s.pathFootprint(u)
			goalX, goalZ, haveGoal = groundGoalPoint(r.Goal, u, fx, fz)
			if binding := s.activeOrders[r.Unit]; binding != nil && binding.order != nil {
				// TODO(question): the queue-pump flag that suppresses the synthetic route while
				// retiring an order is not represented on orders.Node. A publication
				// whose binding still matches the active head is treated as live [04
				// R-PATH-01 §8].
				allowSynthetic = haveGoal
			} else {
				q := orders.QueueForUnit(u)
				allowSynthetic = haveGoal && q != nil && q.Head() != nil
			}
			installGroundRoute(route, u, r.Goal, goalX, goalZ, haveGoal, allowSynthetic, mPoints, revision, s.tick)
		} else {
			route.PublishAtRevision(mPoints, revision)
		}
	} else {
		route.PublishAtRevision(mPoints, revision)
	}
	route.Status = status
	if status == path.StatusRejected {
		s.recordPathFailure(r.Unit, status, s.tick)
	} else {
		s.ClearPathFailure(r.Unit)
	}
}

// emitMovementCallbacks emits StartMoving/StopMoving/MoveRateN and setSFXoccupy per [04 §5.2][GAP T15] C17 C18.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Must be called after steering/flight integration but before the next slot's clear/commit so the VM sees the walk loops [04 §1.1][01 §4.4].
// Retail classification: category 0 when blocked/inhibited/attached/both mags
// zero, else 1..3 via signed definition thresholds [04 R-COLL-01 §5][04 §5.2].
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
			cob.StartDeferredWake(vm, name, nil) // [04 §5.2][GAP T15] deferred callback wake
		}
		s.prevMoveTier[u.Handle] = cat
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	band := MediumBand(s.Terrain, u) // simplified mapping [04 §9.1]; exact overwrite 1→2→3 via wy/wt/wl/mb is TODO(question) for hover
	prevBand := s.prevSFXBand[u.Handle]
	if band != prevBand {
		cob.StartDeferredWake(vm, "setSFXoccupy", []int32{int32(band)}) // [04 §9.1][GAP T15] deferred callback wake
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
	s.recordCollisionHistory(tick)
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
	// The mover tick is five calls and the flight branch is the second: the
	// controller's per-tick hook, then the flight integrator when the
	// definition's `canfly` bit is set [04 R-AIR-01 §1]. The flight integrator
	// never reads an order record — it reads only the flight command block — so
	// an aircraft takes this path whatever its queue holds, and an aircraft with
	// a released payload continues on its last command.
	if u.Def != nil && u.Def.CanFly {
		return s.stepAir(u, tick)
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
	if name != "Move_Ground" && name != "VTOL_Move" && name != "QMove" && name != "VTOL_MobileBuild" && name != "MobileBuild" && name != "VTOL_Patrol" && name != "Patrol" && name != "Park" {
		if head.GoalX == 0 && head.GoalZ == 0 && head.GoalY == 0 {
			d := s.distToGoal(u)
			s.emitMovementCallbacks(u, 0)
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
		}
	}
	route := s.Routes[handle]
	s.serviceGroundFollower(u, head, route, tick)
	isAircraft := u.Def != nil && u.Def.CanFly
	hadRoute := route != nil && route.Active && ((isAircraft && route.Count > 0) || (!isAircraft && route.Count > 1))
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
	var brakingOnly bool
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
		// A ground follower with no waypoint brakes without turning. Straight
		// goal motion is created only by the route-acceptance synthetic route; budget
		// delay, empty publication, and route exhaustion do not synthesize it
		// here [04 R-PATH-01 §8][04 R-MOV-01 §3]. Aircraft consume their goal
		// point directly and retain the existing direct branch.
		if u.Def == nil || !u.Def.CanFly {
			brakingOnly = true
		} else if head != nil && head.MoveState != orders.MoveArrived {
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
	}
	// Current waypoint or direct goal
	var wp Point
	var wpWorldX, wpWorldZ numeric.Fixed
	var dx, dz int64
	if brakingOnly {
		wpWorldX = u.X
		wpWorldZ = u.Z
		wp = Point{X: int32(int64(u.X) >> 16), Z: int32(int64(u.Z) >> 16)}
	} else if directGoal {
		wpWorldX = directX
		wpWorldZ = directZ
		dx = int64(wpWorldX) - int64(u.X)
		dz = int64(wpWorldZ) - int64(u.Z)
		// For direct goal, keep wp as goal cell for pitch delta fallback (use goal cell)
		wp = Point{X: world.WorldToCell(directX), Z: world.WorldToCell(directZ)}
	} else {
		if isAircraft {
			if route.Count > 1 {
				wp = route.Points[1]
			} else {
				wp = route.Points[0]
			}
			wpWorldX = numeric.Fixed(int64(wp.X) << 16)
			wpWorldZ = numeric.Fixed(int64(wp.Z) << 16)
		} else {
			t1x, t1z, _, _ := routeTargets(route, int32(u.X.Raw()), int32(u.Z.Raw()))
			wpWorldX, wpWorldZ = numeric.Fixed(t1x), numeric.Fixed(t1z)
		}
		dx = int64(wpWorldX) - int64(u.X)
		dz = int64(wpWorldZ) - int64(u.Z)
	}
	if !brakingOnly && dx == 0 && dz == 0 {
		d := s.distToGoal(u)
		arrived := s.finalGoalReached(u, hadRoute)
		s.emitMovementCallbacks(u, 0) // no delta => tier 0 [04 §5.2][GAP T15] C18
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: true, EmptyRoute: false, Moved: false, Arrived: arrived}
	}
	desired := u.Move.Heading
	if !brakingOnly {
		// The mover's desired heading uses the self-minus-target vector; the
		// ground velocity step negates its sine/cosine components, so this
		// operand reversal points the unit toward its waypoint [04 R-MOV-01
		// §2][04 R-MOV-01 §4].
		desired = numeric.AngleFromAtan2(-dx, -dz).Raw()
	}
	oldXRaw := int64(u.X)
	oldZRaw := int64(u.Z)
	var moved bool
	var blocked bool
	// Ground vs air branch [04 §10.1] C26. The air half of this branch is
	// unreachable: an aircraft returns at the mover tick's flight branch, above
	// [04 R-AIR-01 §1]. The guard remains so a future caller that arrives here
	// with an aircraft cannot fall into the ground route follower — the exact
	// path that left a construction aircraft's facing to the ground code and
	// made it fly sideways.
	if u.Def != nil && u.Def.CanFly {
		return s.stepAir(u, tick)
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
		// The follower selects acceleration only when both strict cornering and
		// stopping-distance gates pass [04 R-MOV-01 §4].
		hasWaypoint := !brakingOnly
		accelerate := false
		if hasWaypoint {
			t1x, t1z, t2x, t2z := routeTargets(route, int32(u.X.Raw()), int32(u.Z.Raw()))
			accelerate = followerAccelerates(steer, desired, int32(u.X.Raw()), int32(u.Z.Raw()), t1x, t1z, t2x, t2z)
		}
		steer.UpdateFollowerSpeed(cap, hasWaypoint, accelerate)
		steer.Integrate() // [04 §8.1] C20: heading commit + fixed trig position step
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
		proposedAnchor := coll.ProposedAnchor(coll.Mode)
		fx, fz := coll.FootPrintX, coll.FootPrintZ
		inBounds := commitRectInBounds(s.Terrain, proposedAnchor, fx, fz)
		blockerID := -1
		perCell := func(c Cell) bool {
			if !inBounds {
				return false
			}
			if s.Terrain != nil && !moverProfile.IsPassableCommitCell(s.Terrain, c.X, c.Z) {
				return false
			}
			if s.Grid != nil {
				if occ, ok := s.Grid.OccupantAt(c); ok && occ != coll.ID {
					blockerID = occ
					return false
				}
			}
			return true
		}
		fastPath, isBlocked := coll.CommitOne(s.Grid, coll.Mode, perCell, nil) // [04 §8.2] C23 C24: sync clear-then-stamp before next slot
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
		if y, ok := groundPostMoveHeight(s.Terrain, u); ok {
			u.Y = y
		} else {
			u.Y = numeric.Fixed(int64(coll.Y))
		}
		// TODO(R-MOV-01 selection primitive): non-upright, non-floater units
		// still need the compiled model ground plate to publish exact Y, pitch,
		// and roll; retain the established centre-height placeholder until that
		// geometry is available [04 R-MOV-01 §5].
		steer.X = coll.X
		steer.Z = coll.Z
		steer.Heading = coll.Heading
		steer.Speed = coll.Speed
		u.Move.Heading = coll.Heading
		u.Move.Speed = numeric.Fixed(coll.Speed)
		// Emit StartMoving/StopMoving/MoveRateN and setSFXoccupy per [04 §5.2][GAP T15] C17 C18 via immediate barrier [GAP T15] C18.
		// Must run after speed commit so tier reflects current capped speed [04 §5.2][GAP T15] C18.
		callbackSpeed := coll.Speed
		if coll.Blocked {
			// A blocked mover is movement tier zero even though collision retains
			// a capped scalar speed for its next proposal [04 R-COLL-01 §5].
			callbackSpeed = 0
		}
		s.emitMovementCallbacks(u, callbackSpeed)
		moved = int64(u.X) != oldXRaw || int64(u.Z) != oldZRaw
	}
	// Arrival via goal tolerance, not merely route active [task]
	d2 := s.distToGoal(u)
	arrived := s.finalGoalReached(u, hadRoute)
	// Published routes consumed without duplicate submission: StepUnit does not
	// call SubmitMove; the scheduler's HasRequest gate in session path-submit
	// remains authority [04 §7.3] C11 C12.
	hasRouteAfter := route != nil && route.Active
	// EmptyRoute reports route absence at entry. A ground mover brakes while
	// waiting for the follower's next eligible request [04 R-MOV-01 §3][§7].
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
				// This wrapper is an isolated flight fixture; keep the same
				// self-minus-target operand order as the live flight producer
				// [04 R-AIR-01 §1].
				f.TargetHeading = numeric.AngleFromAtan2(-dx, -dz).Raw()
			}
		}
	}
	IntegrateFlight(f)
	u.X = numeric.Fixed(int64(f.X))
	u.Y = numeric.Fixed(int64(f.Y))
	u.Z = numeric.Fixed(int64(f.Z))
}
