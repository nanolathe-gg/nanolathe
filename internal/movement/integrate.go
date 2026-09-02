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
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"

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

	// Fallback is retained for callers that ask for a handle before its unit
	// surface exists. Initialized units always use either their resolved class
	// record or their own FBI scratch profile; it is never shared by unresolved
	// definitions [02 §5 "Movement class record"][04 §6.1].
	Fallback Profile

	// Classes is the compiled movement-class table, keyed as
	// content.CanonicalKey(name). Each unit resolves its own profile from it:
	// passability, path bias, collision footprint and occupancy stamps are
	// per-unit identity, not session-wide [04 §6.1] [04 §7.1].
	Classes map[string]*content.MovementClass

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
	sessions                []*pathWorkingSet       // deterministic slice indexed by handle [04 §7.3] C11 C12 budget-honoring sessions
	prevMoveTier            map[pool.Handle]int     // cached mover tier per unit for MoveRate edge emission [04 §5.2][GAP T15] C18
	prevSFXBand             map[pool.Handle]int     // cached setSFXoccupy band per unit for edge emission [04 §5.2][GAP T15] C17 C18

	// world is the units world bound via BindWorld for the phase-2 transaction.
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

	// airBases is the per-side target registry's third list — the
	// damaged-aircraft base candidates of [06 §3.1 "the third list"] and
	// [04 R-AIR-01 §11]. Unlike tickCarried it is NOT per-tick: it is refilled
	// on the registry's own 30-tick cadence and deliberately read stale in
	// between, which is the behavior. BeginTick drives the rebuild so the
	// snapshot is taken at a tick boundary rather than at whichever aircraft
	// happens to scan first.
	airBases combat.AirBaseRegistry

	// pathFailures is a publication diagnostic only. It never gates, counts, or
	// schedules recovery; WantsRepath/LastRequestTick on Route own that state
	// [04 R-MOV-01 §7][04 R-PATH-01 §8].
	pathFailures map[pool.Handle]PathFailure

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
	// payload is the goal object the record installed. Retail's follower does not
	// re-derive an arrival predicate: it asks the payload "has the unit arrived",
	// forwarding the mover's committed cell to that class's own start predicate
	// [04 R-MOV-03 §2][04 R-PATH-01 §9]. The work family's annulus and rectangle
	// payloads are carried here so the arrival band is the same object the search
	// was aimed at; with none set, the point and border tests below apply.
	payload path.Goal
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
	arrivalSatisfiedBit uint32 = 0x20 // ORed into node.satisfied by the arrival-bit setter [R-P0-01]
	arrivalGateMask     uint32 = 0xE0 // gate mask armed by the handler's phase-0 gate arm [R-P0-01]
)

type PathFailure struct {
	Status path.Status
	Tick   uint32
}

// pathWorkingSet is one admitted path request's resumable working set: the
// search session plus the identity of the request that owns it.
//
// The start cell is deliberately NOT part of that identity. Request setup
// copies the requesting unit's cached committed cell as the start cell at
// ADMISSION [04 R-PATH-01 §4] step 1, and every later budget slice resumes the
// same working set [04 R-PATH-01 §6]; re-reading the live cell on each slice
// would restart the search under a moving unit and it would never finish. The
// owning request is therefore identified by its goal object and its activation
// token, which is what a replan or a queue-head replacement changes.
type pathWorkingSet struct {
	session    *path.Session
	goal       path.Goal
	activation uint64
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
// move-family order head [R-P0-01 corrected][04 R-ORD-01 §4].
//
// `Move_Ground` phase 0 binds its point goal with radius
// `(int16)argument + 4`, where the argument is the record's first parameter
// word read as a SIGNED 16-bit value. Interface-issued moves and the AI's
// regroup/explore broadcasts leave that word 0, giving the familiar radius 4
// and handle threshold floor(4/16)² = 0 — arrival only on the exact goal cell.
// The AI wave task's gather broadcast forwards 160 through the same word
// [08 R-AI-01 §19], so a gather is a move with a 164-world-unit arrival radius.
// Passing the word instead of hardcoding 4 was the missing half of that
// contract: the AI filled the word (WU-19-74) but this helper never read it.
//
// The VTOL_Move handler does not read the word at all; it passes the
// definition's kamikaze distance clamped to at least 16.
//
// The ground patrol family binds radii of its own, and they are NOT the ground
// default: §4 gives `Patrol` phase 1 radius 0 and `RepairPatrol` phase 1
// radius 16. This helper does not model the patrol substate machine and must
// not guess those values from the descriptor name, so it reads back what the
// handler bound — orders.PatrolGoalRadius, the same read-back shape
// orders.ParkGoalRect already gives this file for the rectangle `Park`
// installs. Binding 4 for both rows left `RepairPatrol` on threshold
// floor(4/16)² = 0 where the row's radius 16 gives floor(16/16)² = 1, a real
// difference in the arrival predicate, and stated `Patrol`'s radius wrongly
// even though its threshold happened to agree.
func goalRadiusParamFor(def *content.UnitDef, head *orders.Node) int32 {
	if head == nil {
		return 4 // radius field (0 at order creation) + 4 [R-P0-01 corrected]
	}
	switch orders.DescriptorFor(head.ID).Name {
	case "VTOL_Move":
		rp := int32(16)
		if def != nil && def.KamikazeDistance > rp {
			rp = def.KamikazeDistance
		}
		return rp
	}
	if radius, ok := orders.MoveGroundGoalRadius(head); ok {
		// Signed 16-bit read of the argument word, then +4 [04 R-ORD-01 §4],
		// read back from the handler that binds it rather than restated here.
		// Since WU-19-97 that handler installs the point goal itself, so this
		// is only the fallback handle built before its phase 0 has run — a
		// bound payload outranks the radius at the end of bindArrivalHandle.
		return radius
	}
	if radius, ok := orders.PatrolGoalRadius(head); ok {
		return radius // [04 R-ORD-01 §4], authored beside the handler that binds it
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
	Handle pool.Handle // the stepped handle
	// LifecycleError reports that StepUnit was called outside the active
	// BeginTick/EndTick transaction. The production session owns that
	// transaction; callers must not fall back to direct unit state [01 §4.4].
	LifecycleError bool
	Arrived        bool          // final completion remains false until R-P0-01 closes
	DistToGoal     numeric.Fixed // diagnostic distance after step; sentinel when no goal
	HasRoute       bool          // route.Active after step (pruning may have cleared it)
	Moved          bool          // position changed this tick
	Blocked        bool          // collision blocked this tick [04 §8.2] C24
	EmptyRoute     bool          // true when no active route at entry (empty/failed) [task]
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
	// The terrain owns the mover half of retail's single ground-occupancy word
	// for the rest of the battle, so every placement check reaches both halves
	// without its caller electing to pass one [04 R-COLL-01 §2].
	if terrain != nil {
		terrain.Movers = gridOccupancy{grid: grid}
		// Retail's mobile occupancy IS the plot cell's first two words
		// [03 §2.2][04 R-COLL-01 §4]; binding them here makes every grid stamp
		// and clear write the word of the same plane in the same call, so the
		// grid and the words are one store (the authority note at the head of
		// collision.go).
		grid.AttachPlot(terrain)
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
// so the session can drive movement from its own slot visit without passing
// the world on every call. This is the minimal additive interface for ON-03;
// it does not change internal/path or internal/orders.
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

// mappingWordSource resolves the view of the visibility publisher's per-player
// mapping word grid that the search's coarse test reads [04 R-PATH-01 §2]
// [04 R-PATH-01 §14]. internal/movement holds no visibility handle, so it
// reaches the grid through the requester's order-queue binding — the same route
// the aircraft landing test's coarse early accept takes [04 R-AIR-01 §14.2].
// Nil when the unit carries no binding or the composition has no visibility
// service, which leaves Passable on its terrain-only fallback.
func (s *System) mappingWordSource(h pool.Handle) MappingWordSource {
	if s == nil || s.world == nil {
		return nil
	}
	u := s.world.Unit(h)
	if u == nil {
		return nil
	}
	b := airBinding(u)
	if b == nil || b.World == nil || b.World.MappingWord == nil {
		return nil
	}
	return b.World.MappingWord
}

// ensureLayerRegistry returns the layer registry, creating it at first use for
// wiring sites reached without a prior BindWorld call.
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
// selection-primitive conform is applied by applyGroundPostMove below
// [04 R-MOV-01 §5].
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

const unitTransformDirty uint32 = 1 << 16

// applyGroundPostMove is the sole ordinary ground writer for Y, pitch, and
// bank. Its four-corner path follows the root selection primitive, while the
// upright and floater paths deliberately leave the angle words unchanged
// [04 R-MOV-01 §5a].
func applyGroundPostMove(t *world.Terrain, u *units.Unit, dirty bool, mode uint8, bob *hoverBob) {
	if t == nil || u == nil || u.Def == nil {
		return
	}
	if !dirty && !u.Def.CanHover {
		return
	}
	// The transform-dirty bit is consumed before the mode branch. The local
	// mover dirty signals are the movement package's equivalent until the unit
	// flag is populated by the session writer.
	u.Flags &^= unitTransformDirty
	if mode&3 != 1 {
		return
	}

	if u.Def.Upright {
		if y, ok := groundPostMoveHeight(t, u); ok {
			u.Y = y
		}
		return
	}
	if u.Def.Floater {
		u.Y = numeric.Fixed((int64(t.SeaLevel) - int64(u.Def.Waterline)) << 16)
		return
	}
	// A canhover definition that is neither upright nor floater takes the
	// fourth branch like any other ground mover; its medium changes only the
	// per-corner floor and bob inside the conform [04 R-MOV-01 §8a]. This
	// previously returned here, which left every hovercraft with no post-move
	// Y write at all.
	applyGroundConform(t, u, bob)
}

// hoverAnimationRate is the animation counter's advance per simulation tick.
//
// Retail forms that counter as GetTickCount() scaled by a configured rate and
// divided by 1000 — a wall clock shared with the presentation layer [01 §7.4].
// Nanolathe deliberately does not clone it. [04 R-MOV-01 §5b] settles what the
// leak costs: the pairwise truncating average leaves a one-unit residue that
// the antipodal corner pairs do not cancel, and because section 9.1's band-2
// test is an equality against sea level, the ten stock canhover definitions
// that author waterline 0 alternate between bands 2 and 1 on elapsed real
// time. That classifier is edge-triggered, so a wall clock here would re-fire
// an occupancy callback on the unit's script at a wall-clock cadence. The
// counter is therefore derived from the tick, which keeps the rest of §5 exact
// and confines the divergence to which of {0, -1} the offset takes.
//
// Both inputs the previous marker here deferred are now traced
// [04 R-MOV-01 §5c]. The rate field has exactly one writer — the boot-time
// timebase installer stores 30 into it, once — so retail's counter is
// floor(GetTickCount() * 30 / 1000), the same 30-per-second wall-clock scale
// that budgets the tick [01 §4.1]. One step per tick is what "30 per second at
// 30 ticks per second" reduces to when no budget lag exists, so the rate stays
// 1 and the only remaining divergence is the one [R-MOV-01 §5b] bounds: which
// of {0, -1} a hovering unit's height offset takes on a tick where the wall
// clock and the tick disagree. The per-unit phase word's writer is the unit
// initializer's full-domain allocator draw, carried on units.Unit.BobPhase and
// read by component below [04 R-MOV-01 §5c].
const hoverAnimationRate = 1

// hoverBob carries the canhover inputs of the four-corner conform's per-corner
// height [04 R-MOV-01 §5]. A nil *hoverBob selects the ordinary terrain plate,
// which is what every non-hovering ground mover gets.
type hoverBob struct {
	counter int32 // the animation counter; only its low five bits are read
	amp     int32 // 0, 1 or 2 after the speed term and the age fade
	phase   int16 // the unit's allocator-drawn bob phase word [04 R-MOV-01 §5c]
}

// newHoverBob builds the bob inputs, or returns nil when this unit does not
// take the hover branch [04 R-MOV-01 §5].
func newHoverBob(u *units.Unit, speed int32, tick, lastProposal uint32) *hoverBob {
	if u == nil || u.Def == nil || !u.Def.CanHover || !u.Alive || u.Dying {
		return nil
	}
	// The sea-level floor and the bob are one branch, gated together on the
	// definition and the live/death bits [04 R-MOV-01 §5][04 R-MOV-01 §8a]. A
	// faded or speed-cancelled amplitude still takes the floor, so this returns
	// a zero-amplitude bob rather than nil — nil means "not a hovering unit"
	// and selects the ordinary terrain plate.
	bob := &hoverBob{counter: int32(tick) * hoverAnimationRate, phase: u.BobPhase}
	half := u.Def.MaxVelocity / 2
	if half <= 0 {
		// MaxVelocity/2 is an unguarded divisor in retail: a canhover
		// definition below 2 faults there [04 R-MOV-01 §5]. Declining to fault
		// cannot diverge on authored content — the slowest canhover definition
		// in this install authors 98304 [04 R-MOV-01 §5b].
		return bob
	}
	v := speed
	if v > half {
		v = half
	}
	// amp = 2 - (((v << 16) / half) * 2 >> 16), so 2 at rest and 0 at half
	// MaxVelocity, then faded linearly to zero over the 60 ticks after the
	// mover last proposed a position [04 R-MOV-01 §5].
	amp := 2 - int32(((int64(v)<<16)/int64(half))*2>>16)
	age := tick - lastProposal // unsigned delta, as the age is read
	if age > 60 {
		age = 60
	}
	amp -= amp * int32(age) / 60
	if amp < 0 {
		amp = 0
	}
	bob.amp = amp
	return bob
}

// component is the per-corner offset for corner i. The four corners sit a
// quarter circle apart, so the unit rocks rather than heaves [04 R-MOV-01 §5].
func (b *hoverBob) component(i int32) int32 {
	// The per-unit phase word the allocator drew at creation, added to the
	// corner angle so hovercraft rock out of phase with one another
	// [04 R-MOV-01 §5][04 R-MOV-01 §5c]. The sum wraps in 16 bits, as the
	// angle does.
	angle := int16(((b.counter&0x1f + 8*i) << 11) + int32(b.phase))
	// The shared table biases the angle by 0x20 before selecting one of its 512
	// entries, and the product rounds to nearest at the table's 8192 scale
	// [04 R-MOV-01 §4][04 R-MOV-01 §5].
	return numeric.MulRound(numeric.Sin(numeric.Angle(uint16(angle))+0x20), b.amp)
}

// applyGroundConform samples the four vertices of the root selection plate.
// Invalid geometry and any out-of-map corner abandon the complete correction,
// preserving the previous Y and orientation [04 R-MOV-01 §5a].
func applyGroundConform(t *world.Terrain, u *units.Unit, bob *hoverBob) {
	binding := u.COBBinding()
	if binding == nil || binding.Model == nil {
		return
	}
	m := binding.Model
	if t.CellW <= 1 || t.CellH <= 1 || int64(t.CellW)*int64(t.CellH) > int64(len(t.Plot)) {
		return
	}
	if m.Root < 0 || m.Root >= len(m.Pieces) {
		return
	}
	piece := m.Pieces[m.Root]
	if !piece.Selection || len(piece.Primitives) == 0 {
		return
	}
	primitive := piece.Primitives[0]
	if len(primitive.VertexIndices) < 4 {
		return
	}

	var worldX, worldZ [4]int16
	var height [4]int32
	for i := 0; i < 4; i++ {
		index := primitive.VertexIndices[i]
		if int(index) >= len(piece.Vertices) {
			return
		}
		vertex := piece.Vertices[index]
		rx, rz := rotateGroundPair(vertex[0], vertex[2], u.Move.Heading)
		worldX[i] = int16((rx + int64(u.X)) >> 16)
		worldZ[i] = int16((int64(u.Z) - rz) >> 16)
		if uint32(worldX[i]>>4) >= uint32(t.CellW-1) || uint32(worldZ[i]>>4) >= uint32(t.CellH-1) {
			return
		}
		height[i] = conformHeight(t, worldX[i], worldZ[i])
		if bob != nil {
			// canhover raises the conform's floor to sea level, so a hovercraft
			// rides the surface over water and the terrain over land in one
			// expression [04 R-MOV-01 §8a].
			if sea := int32(t.SeaLevel); height[i] <= sea {
				height[i] = sea
			}
			height[i] += bob.component(int32(i))
		}
	}

	a := (height[0] + height[1]) / 2
	b := (height[2] + height[3]) / 2
	integerHeight := (a + b) / 2
	u.Y = numeric.Fixed((int64(integerHeight) << 16) | (int64(u.Y) & 0xffff))
	// Each run is the unrotated model-space span of the very corners whose
	// heights the matching numerator differences, so both angles are a genuine
	// rise over run [04 R-MOV-01 §5a]. Pitch differences the two Z-edge pair
	// averages and corners 0 and 3 sit on opposite Z edges; roll differences
	// corner 0 against corner 1, so its run is the span between those two.
	//
	// Correction. The roll run was written as the corner 1 to corner 2 span.
	// Stock plates are authored as a ring — (+x,+z), (-x,+z), (-x,-z), (+x,-z)
	// — so corners 1 and 2 share an X and that span is zero for 522 of the 538
	// stock models that carry a root selection primitive. A zero run makes the
	// arc tangent a quarter circle for any non-zero cross-slope, which laid
	// every ground vehicle on its side the first time it crossed one. The
	// corner 0 to corner 1 span is non-zero for all but the 16 models whose
	// plate is degenerate on both axes anyway.
	modelZRun := int16(conformAbsFixed(vertexZ(piece, primitive.VertexIndices[0])-vertexZ(piece, primitive.VertexIndices[3])) >> 16)
	modelXRun := int16(conformAbsFixed(vertexX(piece, primitive.VertexIndices[0])-vertexX(piece, primitive.VertexIndices[1])) >> 16)
	u.Move.Pitch = numeric.AngleFromAtan2(int64(b-a), int64(modelZRun)).Raw()
	u.Move.Bank = numeric.AngleFromAtan2(int64(height[0]-height[1]), int64(modelXRun)).Raw()
}

func vertexX(piece model.Piece, index uint16) numeric.Fixed {
	if int(index) >= len(piece.Vertices) {
		return 0
	}
	return piece.Vertices[index][0]
}

func vertexZ(piece model.Piece, index uint16) numeric.Fixed {
	if int(index) >= len(piece.Vertices) {
		return 0
	}
	return piece.Vertices[index][2]
}

func conformAbsFixed(value numeric.Fixed) int64 {
	v := int64(value)
	if v < 0 {
		return -v
	}
	return v
}

func rotateGroundPair(x, z numeric.Fixed, heading uint16) (int64, int64) {
	// Ground conform and flight lean use the same body-to-world pair rotation
	// and rounding closure [01 R-DET-01 §2][04 R-MOV-01 §5a]. Model vertices
	// originate as signed 32-bit 16.16 values, so the established helper's
	// narrow inputs and outputs preserve the authored domain exactly.
	rx, rz := rotateLeanPair(int32(x), int32(z), heading)
	return int64(rx), int64(rz)
}

func conformHeight(t *world.Terrain, x, z int16) int32 {
	cx, cz := int32(x>>4), int32(z>>4)
	fx, fz := int32(x&15), int32(z&15)
	w := t.CellW
	h := func(px, pz int32) int32 {
		return int32(t.Plot[int(pz*w+px)].Height())
	}
	h00, h10 := h(cx, cz), h(cx+1, cz)
	h01, h11 := h(cx, cz+1), h(cx+1, cz+1)
	top := h00 + (h10-h00)*fx/16
	bottom := h01 + (h11-h01)*fx/16
	return top + (bottom-top)*fz/16
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
// It compares the mover's cached occupancy tile (CollisionState.CachedAnchor)
// against the goal handle's cell in the cell domain, planar only, inclusive:
// dx*dx+dz*dz <= threshold².
// On success it ORs 0x20 into node.satisfied through the arrival-bit setter
// [R-P0-01]. No y, heading,
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

// raiseArrival is the whole of the follower's arrival step: "on arrival raise
// pending `0x20` on the owning record, ask the payload whether it is
// persistent, and if not release it through the follower's owner"
// [04 R-MOV-03 §2 "The follower's per-tick service"].
//
// The persistence answer is not a question a ground follower has to ask: the
// *does this route persist after arrival* query "returns 0 for all three
// A\*-facing classes, so arrival always detaches the route", and the `0x20` is
// ORed in "before detaching" [04 R-PATH-01 §8]. The three A\*-facing classes
// are the point, annulus and rectangle goals — the classes whose
// is-a-search-goal flag reads 1 [04 R-MOV-03 §2] — which is every goal a
// ground follower can hold. The two air classes carry their own persistence
// rule and their own producer, which runs this same arrival/persistence/release
// step for the flight block [04 R-AIR-01 §1] step 6, so an aircraft is left
// alone here: "aircraft never enter this scheduler" [04 R-PATH-01 §8].
func (s *System) raiseArrival(u *units.Unit, ah *arrivalHandle) {
	if ah == nil || ah.order == nil {
		return
	}
	ah.order.Satisfied |= arrivalSatisfiedBit // [R-P0-01][04 R-PATH-01 §8] `0x20` before the detach
	s.detachOnArrival(u, ah)
}

// detachOnArrival is the detach half of the step above.
//
// The release is the release form of the record-level install/release helper:
// cancel the in-flight search, OR `0x80` into the pending word of the record
// that owns the object in the controller's slot, clear has-waypoint and
// wants-repath, and drop the slot [04 R-ORD-01 §1][04 R-ORD-01 §9]. That
// ordering — arrival's `0x20` first, the release's `0x80` second — is the same
// one the air producer already runs [04 R-AIR-01 §1] step 6, and no handler
// reads `0x80` ahead of `0x20`: `Move_Ground` phase 1 and `Attack_Kamikaze`
// phase 1 test `0x20` first, and the patrol and work rows test the whole
// `0xE0` gate [04 R-ORD-01 §3][04 R-ORD-01 §4][04 R-ORD-01 §5].
//
// A row whose handler installs no payload object in this build still detaches
// its route. §8's rule is about arrival, not about which record happens to own
// the object; leaving an active route standing behind an arrival would keep the
// mover consuming waypoints through a goal it has already reached.
//
// The handle goes with the payload. The follower asks the arrival question only
// "with a payload installed" [04 R-MOV-03 §2]; with the slot null there is
// nothing left to ask until an installer binds a new object, and
// bindArrivalHandle builds a fresh handle at the next activation or replan.
func (s *System) detachOnArrival(u *units.Unit, ah *arrivalHandle) {
	if s == nil || u == nil || ah == nil || ah.order == nil {
		return
	}
	if u.Def != nil && u.Def.CanFly {
		return // the flight block owns its own arrival, persistence and release
	}
	if !s.ReleaseGoalPayload(ah.order) {
		s.CancelPathRequest(u.Handle)
		if route := s.Routes[u.Handle]; route != nil {
			route.Active = false
			route.WantsRepath = false
		}
	}
	delete(s.arrivalHandles, u.Handle)
}

// finalGoalReached raises the ground arrival bit 0x20 from the goal payload's
// OWN arrival test — the tile-versus-goal-cell predicate — which carries no
// route, point-count or search-status term. A mover that reaches the goal cell
// by direct walking while its path request is still pending, or was never
// published, completes the order exactly as one that consumed a route does
// [04 R-P0-01 "Closed — compact ground controller", clarification 2026-09-02].
// hadRoute is therefore not consulted: the parameter is retained for the
// caller's own bookkeeping.
func (s *System) finalGoalReached(u *units.Unit, hadRoute bool) bool {
	_ = hadRoute
	if u == nil {
		return false
	}
	// Arrival is defined only for an order that has an arrival handle bound [R-P0-01].
	ah, ok := s.arrivalHandles[u.Handle]
	if !ok || ah == nil || ah.order == nil {
		return false
	}
	// Verify handle still belongs to the active head; stale handles after a head
	// replacement must not signal [R-P0-01][04 §7.3].
	if q := orders.QueueForUnit(u); q == nil || q.Head() != ah.order {
		return false
	}
	// Cached tile from occupancy commit (CollisionState.CachedAnchor) [R-P0-01][04 §8.2].
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
	// A payload answers the arrival question itself: the satisfied-from-unit
	// adapter forwards the committed cell pair to the class's start predicate
	// [04 R-MOV-03 §2]. The annulus band and the work rectangle both arrive
	// beside their target, never on its own anchor cell [04 R-PATH-01 §9].
	if ah.payload != nil {
		if ah.payload.StartSatisfied(path.Cell{X: tileX, Z: tileZ}) {
			s.raiseArrival(u, ah) // [R-P0-01] OR 0x20, then detach [04 R-PATH-01 §8]
			return true
		}
		return false
	}
	// A rectangle-perimeter goal arrives on membership of the border, not on
	// proximity to one cell [04 §7.2][04 R-FAC-02 §4].
	if ah.border != nil {
		if onRectBorder(*ah.border, tileX, tileZ) {
			s.raiseArrival(u, ah) // [R-P0-01] OR 0x20, then detach [04 R-PATH-01 §8]
			return true
		}
		return false
	}
	dx := int64(tileX) - int64(ah.goalX)
	dz := int64(tileZ) - int64(ah.goalZ)
	// Signed 32-bit squares, pure planar inclusive compare [R-P0-01] setle.
	if dx*dx+dz*dz <= int64(ah.threshSq) {
		// The arrival-bit setter's OR of 0x20, then the detach [04 R-PATH-01 §8].
		s.raiseArrival(u, ah) // [R-P0-01]
		return true
	}
	return false
}

// Closed 2026-09-02 (RWU-19-30, [04 R-P0-01]): this site used to ask how the
// compact ground locomotion controller signals ground-order arrival when its
// arrival-notify slot is a plain return, and whether a direct arrival with no
// published route should complete the order. Both are answered. The mechanism
// is not a hook at all: the per-unit sweep gates the order pumps and the
// movement tick on the owner's state byte being 1 or 2, so a state-3
// (defeated/watching) owner's units are inert because the whole pump+mover
// block is skipped. And arrival needs no published route — the bit is raised
// from the goal payload's own tile-versus-goal-cell predicate, which has no
// route, point-count or search-status term, so finalGoalReached above returns
// true on a direct walk-in.
//
// Closed 2026-09-01 (WU-19-16): this marker also asked what advances the
// `VTOL_Move` handler from phase 1 to phase 2. [04 R-ORD-02 §2] answers it —
// phase 1 installs the destination marker, sets the gate to `0xE0` and advances
// unconditionally; phase 2 is dispatched on any of the three arrival bits — and
// internal/orders/patrol.go's vtolMoveHandler implements exactly that.

// resolveProfile derives a unit's movement profile from its definition's
// movement class [02 "Unit record"] [04 §6.1]. Resolved names use the catalog
// record. Blank and unresolved names use the complete unit-local FBI scratch
// record; retail keeps this degraded path alive and does not report a missing
// class as a content error [02 §5 "Movement class record"].
func (s *System) resolveProfile(u *units.Unit) Profile {
	if s == nil || u == nil || u.Def == nil {
		return Profile{}
	}
	name := u.Def.MovementClass
	if strings.TrimSpace(name) == "" {
		return s.scratchProfile(u.Def)
	}
	mc := s.Classes[content.CanonicalKey(name)]
	if mc == nil {
		return s.scratchProfile(u.Def)
	}
	return NewProfile(mc)
}

// scratchProfile returns the unit's retained FBI movement record. The compiler
// owns the startup-template defaults, so this remains unit-local even when a
// movement class name is blank or cannot be resolved [02 §5][04 §6.1].
func (s *System) scratchProfile(d *content.UnitDef) Profile {
	return NewScratchProfile(d)
}

// classKeyOf returns the deterministic layer identity for a unit's movement
// profile. Resolved names share their catalog class layer. Blank and
// unresolved names use a profile-derived key, allowing distinct FBI scratch
// profiles to retain distinct stamped layers instead of aliasing on an empty
// class name [04 §6.1 R-DOC04-B].
func (s *System) classKeyOf(u *units.Unit) string {
	if s == nil || u == nil || u.Def == nil {
		return ""
	}
	name := u.Def.MovementClass
	if strings.TrimSpace(name) == "" {
		return scratchLayerKey(NewScratchProfile(u.Def))
	}
	key := content.CanonicalKey(name)
	if s.Classes[key] != nil {
		return key
	}
	return scratchLayerKey(NewScratchProfile(u.Def))
}

// scratchLayerKey encodes every stored profile field in a stable lookup key.
// The prefix keeps it disjoint from ordinary canonical class names, while the
// profile fields ensure two distinct unit-local records cannot alias merely
// because movementclass is blank or unresolved [I1].
func scratchLayerKey(p Profile) string {
	buf := make([]byte, 0, 80)
	// Authored canonical names originate in NUL-terminated content strings, so
	// a leading NUL makes this namespace structurally disjoint from every
	// catalog key rather than merely relying on a conventional prefix.
	buf = append(buf, 0)
	buf = append(buf, "scratch/"...)
	for _, v := range []int64{
		int64(p.FootPrintX), int64(p.FootPrintZ), int64(p.MaxWaterDepth),
		int64(p.MinWaterDepth), int64(p.MaxSlope), int64(p.BadSlope),
		int64(p.MaxWaterSlope), int64(p.BadWaterSlope),
	} {
		buf = strconv.AppendInt(buf, v, 10)
		buf = append(buf, '/')
	}
	return string(buf)
}

// classKeyFor returns the recorded class/layer key of a handle; "" for a unit
// without an initialized surface.
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
	s.pathFailures[h] = PathFailure{Status: status, Tick: tick}
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
		Acceleration:   int32(u.Def.Acceleration),
		BrakeRate:      int32(u.Def.BrakeRate),
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

	// CollisionState. Building-class units use their authored FBI rectangle and
	// yard bytes; mobile units retain the resolved movement-class rectangle and
	// full rectangular stamp [04 R-COLL-01 §4].
	building := !u.Def.BMCode
	var yard []world.YardCell
	footX := profile.FootPrintX
	footZ := profile.FootPrintZ
	if building {
		footX = int16(u.Def.FootprintX)
		footZ = int16(u.Def.FootprintZ)
		if footX <= 0 {
			footX = 1
		}
		if footZ <= 0 {
			footZ = 1
		}
		var err error
		yard, err = world.ParseYardMap(u.Def.YardMap, int(footX), int(footZ))
		if err != nil {
			// ParseYardMap accepts every normalized building extent, so this is
			// unreachable after the local extent normalization. Keep the map
			// empty only if a future parser adds a data error; never substitute
			// a mobile whole-rectangle stamp for a building.
			yard = nil
		}
	}
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
		Building:    building,
		Yard:        yard,
		YardOpen:    u.YardOpen,
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
		// Every successful stamp writes the occupant-age clock first, and unit
		// creation is one of the stamp's writers: the creator stamps the new
		// unit's footprint after the initializer returns, so the creation-time
		// stamp does write the mover's commit tick [04 R-PATH-01 §14]
		// [04 R-COLL-01 §4][04 §6.1 R-DOC04-B]. A building's tick is written
		// here too and then never advances again, but that is no longer what
		// makes it block the search: with no mover it takes the occupant-age
		// gate's null arm unconditionally [04 R-PATH-01 §14].
		stamped := false
		if coll.Building {
			stamped = s.stampBuildingGrid(anchor, footX, footZ, coll.Yard, coll.YardOpen, coll.ID)
		} else {
			// Creation is one of the stamp's writers and it stamps the plane of
			// the mover's mode — 1 here, so the ground word of every cell of the
			// rectangle [04 §2.3][04 R-COLL-01 §4].
			plane, stamps := planeForMode(coll.Mode)
			if stamps {
				stamped = s.Grid.StampPlane(plane, anchor, footX, footZ, coll.ID)
				coll.StampedAnchor = anchor
				coll.StampedPlane = plane
				coll.HasStamp = s.Grid.RectOnMap(anchor, footX, footZ)
			}
		}
		if stamped {
			s.noteOccupancyCommit(h, s.tick)
		}
	}
	// Init move mode to parked [04 §9.1] 1 stopped/parked; a retail restore
	// already copied the unit-side packed mirror and it must survive this
	// bootstrap allocation until the mover pass applies its own state [08
	// R-SAVE-02 §6].
	if !u.RestoredMoveMode {
		u.Move.Mode = 1
	}
	// FlightState for can-fly units
	if u.Def.CanFly {
		flight := &FlightState{
			Mode:          1,
			X:             int32(u.X.Raw()),
			Y:             int32(u.Y.Raw()),
			Z:             int32(u.Z.Raw()),
			VX:            0,
			VY:            0,
			VZ:            0,
			Speed:         0,
			Heading:       u.Move.Heading, // allocated `buildangle` heading [04 §2.3b]
			TargetHeading: u.Move.Heading,
			TurnResidual:  0,
			MaxVelocity:   int32(u.Def.MaxVelocity),
			Acceleration:  int32(u.Def.Acceleration),
			BrakeRate:     int32(u.Def.BrakeRate),
			TurnRate:      int32(u.Def.TurnRate),
			TargetY:       int32(u.Y.Raw()),
			OffMap:        false,
			Dirty:         false,
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

// stampBuildingGrid applies the shared yard selector to movement occupancy.
// The caller has already performed the terrain/foreign-occupant admission;
// each selected cell is still stamped separately so the grid preserves its
// self-identity and foreign-occupant denial semantics [04 R-COLL-01 §4].
func (s *System) stampBuildingGrid(anchor Cell, footX, footZ int16, yard []world.YardCell, open bool, id int) bool {
	if s == nil || s.Grid == nil || len(yard) != int(footX)*int(footZ) {
		return false
	}
	if footX <= 0 || footZ <= 0 {
		return false
	}
	stamped := false
	for dz := int32(0); dz < int32(footZ); dz++ {
		for dx := int32(0); dx < int32(footX); dx++ {
			if !yard[int(dz)*int(footX)+int(dx)].Selects(open) {
				continue
			}
			if s.Grid.Stamp(Cell{X: anchor.X + dx, Z: anchor.Z + dz}, 1, 1, id) {
				stamped = true
			}
		}
	}
	return stamped
}

// clearBuildingGrid removes only cells selected by the building's current
// yard state. The parsed yard remains attached to the collision state so
// teardown uses the same selector as creation and open/close [04 R-COLL-01
// §4].
func (s *System) clearBuildingGrid(anchor Cell, footX, footZ int16, yard []world.YardCell, open bool, id int) bool {
	if s == nil || s.Grid == nil || len(yard) != int(footX)*int(footZ) {
		return false
	}
	if footX <= 0 || footZ <= 0 {
		return false
	}
	cleared := false
	for dz := int32(0); dz < int32(footZ); dz++ {
		for dx := int32(0); dx < int32(footX); dx++ {
			if !yard[int(dz)*int(footX)+int(dx)].Selects(open) {
				continue
			}
			if s.Grid.Clear(Cell{X: anchor.X + dx, Z: anchor.Z + dz}, 1, 1, id) {
				cleared = true
			}
		}
	}
	return cleared
}

// SetBuildingYardState updates the movement-side state used by destruction
// cleanup. Construction owns admission and calls this after an accepted
// port-18 transaction [04 R-COLL-01 §4].
func (s *System) SetBuildingYardState(h pool.Handle, open bool) {
	if s == nil {
		return
	}
	if coll := s.Collisions[h]; coll != nil && coll.Building {
		coll.YardOpen = open
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
	goalObj := s.goalForOrderWithFootprint(u, goal, head, fx, fz)
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
// isPrimaryHead reports whether head is the record the unit's primary queue
// walk would reach first — the only record whose handler can be running, and
// therefore the only one that may own the mover's goal payload [04 §3.3]
// [04 R-PATH-01 §8]. A unit with no queue, or with an empty primary segment,
// has no competing record, so a bare fixture is unaffected.
func isPrimaryHead(u *units.Unit, head *orders.Node) bool {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return true
	}
	prim := q.Primary()
	if len(prim) == 0 {
		return true
	}
	return prim[0] == head
}

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
	if !isPrimaryHead(u, head) {
		// Only the record the pump is servicing may own the mover. The pump
		// walks from the front head and stops at the first record whose gate is
		// nonzero and unsatisfied, so no record behind a blocked head ever runs
		// its handler [04 §3.3] step 3, and only a handler that ran can install
		// a goal payload [04 R-ORD-01 §1]; the follower holds exactly one goal
		// object at a time [04 R-PATH-01 §8]. Honouring a bind for a record
		// behind the head let a second record steal the mover — a construction
		// walk stepped past a blocked `Park` head cancelled that head's path
		// request and deleted its arrival handle on every visit, so the head
		// waited on an arrival bit nothing could raise and the 30-tick re-arm
		// that lives behind its gate never ran [04 R-EGRESS-01].
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
		s.clearPathState(u.Handle)
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
	goalObj := s.goalForOrderWithFootprint(u, goal, head, fx, fz)
	s.bindRectSteeringGoal(u, head, goalObj, fx, fz)
	if route := s.Routes[u.Handle]; route != nil && !selectedPoint {
		goalPointX, goalPointZ, haveGoalPoint := groundGoalPoint(goalObj, u, fx, fz)
		installGroundGoal(route, u, goalObj, goalPointX, goalPointZ, haveGoalPoint, allowSyntheticFor(head), s.staticObstacleRevision(), s.tick)
		route.LastRequestTick = s.tick
	}
	s.submitGoalForOrder(u, start, goalObj, token)
	s.bindArrivalHandle(u, head)
	return true
}

// allowSyntheticFor is the goal installer's gate on the synthetic straight-line
// fallback, written as [04 R-PATH-01 §8] step 5.3 writes it: the fallback runs
// only "if the unit has a current order record and that record's retiring flag
// is clear". [05 R-EGRESS-02] names the retiring flag — it is the pump's code-9
// completion flag, `orders.FlagRetryMark`, set on every record the pump re-arms
// (and on the ones it frees) before it resets the phase [04 R-ORDER-02 §1].
//
// So the fallback is suppressed for exactly the records the pump has already
// declared complete. That is what keeps a blocked mover still: the 30-59-tick
// re-arm loop of [04 R-EGRESS-01] reinstalls the goal on every cycle, and
// without this gate each reinstall handed the mover a fresh two-point straight
// line at a goal it cannot occupy — it lurched, emitted StartMoving/MoveRate
// and stopped again, once per cycle, forever. Retail's blocked followers
// "re-arm every 30 ticks against a goal they cannot occupy and idle in place".
func allowSyntheticFor(head *orders.Node) bool {
	return head != nil && head.Flags&orders.FlagRetryMark == 0
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
	s.clearPathState(u.Handle)
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
	goalObj := s.goalForOrderWithFootprint(u, goal, head, fx, fz)
	s.bindRectSteeringGoal(u, head, goalObj, fx, fz)
	if route := s.Routes[u.Handle]; route != nil && !selectedPoint {
		goalPointX, goalPointZ, haveGoalPoint := groundGoalPoint(goalObj, u, fx, fz)
		installGroundGoal(route, u, goalObj, goalPointX, goalPointZ, haveGoalPoint, allowSyntheticFor(head), s.staticObstacleRevision(), s.tick)
		route.LastRequestTick = s.tick
	}
	s.submitGoalForOrder(u, start, goalObj, token)
	s.bindArrivalHandle(u, head)
	return true
}

// hasControllerGoal reports whether the ground route follower's single payload
// slot holds an object for this mover [04 R-ORD-01 §9]. It is the "with a
// payload installed" condition of the follower's per-tick service
// [04 R-MOV-03 §2], and it asks about the SLOT, not about which record owns
// what is in it: HasGroundGoal next door is the identity-checked question a
// caller asks when it needs to know that one particular record is the bound
// one.
func (s *System) hasControllerGoal(h pool.Handle) bool {
	if s == nil || s.moveGoals == nil {
		return false
	}
	return s.moveGoals[h] != nil
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
	// Step 3 of the per-tick service, with the condition the section opens it
	// with: "**With a payload installed**, when the mover's blocked bit is set
	// **or** fewer than two points remain, arm the repath bit"
	// [04 R-MOV-03 §2 "The follower's per-tick service"][04 R-MOV-01 §7].
	//
	// "A payload installed" is the CONTROLLER's slot, not the record's field:
	// the controller holds one object, the object most recently installed by
	// any record of this unit, and only that bound object arms the repath bit
	// [04 R-ORD-01 §9]. A record whose object has been displaced from the slot
	// is inert — no arrival, no route, no bits — so the test is slot occupancy,
	// never record identity.
	//
	// WU-19-91 left this arm unconditional and said why: the one row that could
	// not satisfy the condition was the ordinary ground move, whose handler
	// installed no payload at all in this build. WU-19-97 gave `Move_Ground`
	// phase 0 its point-goal install, so the condition is now true exactly
	// where retail's is and the gate can be written as the section writes it.
	if s.hasControllerGoal(u.Handle) && (blocked || !route.Active || route.Count < 2) {
		route.WantsRepath = true
	}
	// The follower owns both the wants-repath arm and the exact inclusive
	// admission boundary. Session only invokes this once in the unit sweep;
	// it never submits a second copy [04 R-MOV-01 §7][04 R-PATH-01 §6].
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

// clearPathState is the single lifecycle reset for follower-owned path state.
// Cancelling the request separately is not enough: a route with wants-repath
// left armed could resurrect a removed order on a later unit visit. The order
// binding, request cancellation, and follower state are therefore cleared at
// each head/death/transport/completion boundary [04 R-PATH-01 §8].
func (s *System) clearPathState(handle pool.Handle) {
	if s == nil {
		return
	}
	if route := s.Routes[handle]; route != nil {
		route.Active = false
		route.WantsRepath = false
		route.LastRequestTick = 0
		route.Status = 0
		route.Dirty = true
	}
	s.ClearPathFailure(handle)
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
	s.clearPathState(handle)
	if s.activeOrders != nil {
		delete(s.activeOrders, handle)
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
	// The names below are the rows whose arrival this handle serves even when
	// the record's own goal payload is the implicit one derived from its stored
	// position [R-P0-01]. Park joins the family: its phase 1 completes on the
	// arrival bit this handle sets [04 R-ORD-01 §2][04 R-FAC-02 §4]. The ground
	// work rows join it for the same reason: they install a movement goal in
	// phase 0 or 1 and then wait behind 0xE0/0xE8 for the follower's verdict
	// [04 R-ORD-01 §5], so without a handle their approach gate had no producer
	// at all and the record parked at the head of its queue for the rest of the
	// game.
	name := orders.DescriptorFor(head.ID).Name
	switch name {
	case "Move_Ground", "VTOL_Move", "QMove", "Patrol", "QPatrol", "VTOL_Patrol", "RepairPatrol", "VTOL_RepairPatrol", "Park",
		"HelpBuild", "RepairUnit", "Capture", "Reclaim", "Resurrect":
	default:
		// The name list is not the retail condition, and cannot be. The
		// follower's per-tick service runs its first step "with a payload
		// installed": it asks THAT payload whether the unit has arrived and, on
		// arrival, raises pending `0x20` on the record the payload belongs to
		// [04 R-MOV-03 §1 "The follower's per-tick service"][04 R-ORD-01 §0].
		// Owning an installed payload is the record's own statement that it is
		// waiting on a movement outcome — the same test the session's mover
		// boundary already uses to decide who drives the mover, and a narrower
		// one than membership of a list of names.
		//
		// `Attack_Chase` is the row that needed it. Its phase 2 installs a
		// point goal at the target with the slot's engagement distance as the
		// radius and advances to phase 3, which returns to phase 1 only on
		// satisfied ∩ `0x40E0` [04 R-ORD-01 §3]. When the mover is already
		// inside that radius when the next request is admitted, the search's
		// start predicate answers yes, so request setup notifies `0x100`,
		// publishes empty and returns [04 R-PATH-01 §4 step 6]; the empty
		// publication asks the goal "is the unit already at the goal", gets yes,
		// and therefore does NOT raise `0x40` [04 R-PATH-01 §7]. `0x100` is
		// masked out of every satisfied set [04 R-ORD-01 §0], so the follower's
		// arrival `0x20` is the only outcome the composition leaves — and with
		// no handle bound there was no producer for it. Phase 3 then re-armed
		// its thirty-tick deadline forever and the chase never re-aimed at a
		// target that had moved on (WU-19-87's PT6 stall).
		if !s.HasGroundGoal(u.Handle, head) {
			// No installed payload: no arrival question to ask, and any handle
			// left from a previous record must not signal.
			delete(s.arrivalHandles, u.Handle)
			return
		}
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
	// [R-P0-01 corrected][04 R-ORD-01 §4] radiusParam for the goal handle:
	// Move_Ground binds `(int16)argument + 4` from the record's first parameter
	// word, which is 0 for interface- and most AI-issued moves (threshold 0 —
	// exact goal cell) and 160 for an AI wave gather; VTOL_Move binds
	// max(KamikazeDistance,16); the two ground patrol rows bind the radii they
	// author themselves. The sight-derived radius previously used here was a
	// misattribution: the sight reads in the traced handlers feed
	// range/acquire paths, never the goal handle.
	radiusParam := goalRadiusParamFor(u.Def, head)
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
	//
	// The rectangle tested here must be the SAME one the search is aimed at.
	// ParkGoalRect reports the handler's `(origin, 8s × 6s)` arguments; the goal
	// class grows them by this mover's own footprint [04 R-PATH-01 §12], and
	// goalForOrderWithFootprint builds the search goal that way. Testing arrival
	// against the bare argument rectangle instead leaves a product that reached
	// the grown border unable to raise `0x20`, so its `Park` record re-arms its
	// thirty-tick deadline for the rest of the battle.
	if minX, minZ, maxX, maxZ, ok := orders.ParkGoalRect(head); ok {
		grown := grownGoalRect(minX, minZ, maxX-minX+1, maxZ-minZ+1, int32(footX), int32(footZ))
		ah.border = &grown
	}
	// The work rows' payload is the object the search is aimed at, built once
	// here so steering target, search goal and arrival test can never disagree
	// [04 R-ORD-01 §5][04 R-MOV-03 §2].
	// `Reclaim` and `Resurrect` install the same rectangle on the FEATURE's
	// footprint as `RepairUnit` and `Capture` do on a unit's [04 R-ORD-01 §5].
	// The marker that stood here said this layer could not read a feature record
	// so the two rows fell back to a point goal; WU-19-5 landed the accessor —
	// goals.go's featureRectForGoal resolves the anchor and authored footprint
	// through the queue binding's world adapter — and workApproachGoal builds
	// the grown rectangle for both rows.
	if payload, ok := s.workApproachGoal(u, path.Cell{X: goalX, Z: goalZ}, head, int32(footX), int32(footZ)); ok {
		ah.payload = payload
	}
	// A payload the record INSTALLED for itself outranks both of the above, in
	// the same precedence goalForOrderWithFootprint applies when it builds the
	// search goal — that is what keeps the arrival predicate and the search
	// goal the same object. `Attack_Chase` is the family that needs it: its
	// phase 2 installs a point goal of the slot's engagement distance or a
	// banded goal around the target [04 R-ORD-01 §3], and with the name-keyed
	// radius above the handle tested for arrival on the exact goal cell
	// instead. The mover then stopped inside its own standoff without ever
	// raising `0x20`, so the record's phase 3 never learned the leg was over
	// and never re-aimed at a target that had moved on.
	if payload := s.moveGoalPayload(u.Handle, head); payload != nil {
		ah.payload = payload
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
			ns := make([]*pathWorkingSet, need)
			copy(ns, s.sessions)
			s.sessions = ns
		} else {
			s.sessions = s.sessions[:need]
		}
	}
	ws := s.sessions[idx]
	needsNew := ws == nil || ws.session == nil || ws.goal != r.Goal || ws.activation != r.Activation
	var sess *path.Session
	if !needsNew {
		sess = ws.session
	}
	if needsNew {
		// [04 R-PATH-01 §4] step 1: request setup copies the requesting unit's
		// CACHED COMMITTED CELL — the anchor the occupancy commit last wrote,
		// read at ADMISSION — as the start cell. The cell the follower held when
		// it submitted the request is not that value: a request waits in the
		// provider until the single global working set is free, and the mover
		// keeps walking meanwhile, so the submitted cell can be many cells
		// behind by the time setup runs. Consuming it made the early-exit
		// ladder judge a stale position: a request submitted while the mover
		// stood on its own goal took step 6's already-satisfied exit and
		// published an empty route, and [04 R-PATH-01 §7]'s count-zero branch
		// then asked the LIVE position, disagreed, and raised `0x40` — the
		// "cannot get there" bit — for a goal that was plainly reachable. The
		// work and mobile-build rows abandon their record on that bit
		// [04 R-ORD-01 §5], which is what surfaced as a unit refusing a goal
		// that an identical second order reached.
		start := r.Start
		if s.world != nil {
			if u := s.world.Unit(r.Unit); u != nil && u.Handle == r.Unit {
				start = s.pathStartCell(u)
			}
		}
		r.Start = start
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
			// PassableValue binds the full four-step consumer of
			// [04 R-PATH-01 §2], mapping-word gate included. The gate reads
			// the visibility publisher's per-player grid through the
			// MappingWord port, which is the only array retail has: the
			// mapping grid's complete writer set is the map-load fill, the
			// bulk rebuild and the phase-5 LOS stamp, and no occupancy commit
			// writes it, so a movement-side copy would stay all-zero and the
			// unmapped value 2 would never occur [04 R-PATH-01 §14]
			// [03 R-LAYER §1]. With the real grid bound, ground the requesting
			// player has never mapped returns 2 and expands without the
			// terrain layer being read at all — retail's optimistic pathing
			// through fog.
			//
			// The per-request revision pass runs at request init before any
			// expansion [04 §6.1 R-DOC04-B][04 §7.3]; path.Session.init
			// invokes cfg.Revise first.
			reg := s.ensureLayerRegistry()
			reg.BindMappingWord(s.mappingWordSource(r.Unit))
			cls := s.classKeyFor(r.Unit)
			layer := reg.For(cls, profile)
			requester := r.Unit
			revTick := s.tick
			footX, footZ := profile.FootPrintX, profile.FootPrintZ
			owner := uint8(0)
			if s.world != nil {
				if u := s.world.Unit(r.Unit); u != nil {
					owner = u.Owner
				}
			}
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
					return layer.Passable(c.X, c.Z, footX, footZ, owner)
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
		s.sessions[idx] = &pathWorkingSet{session: sess, goal: r.Goal, activation: r.Activation}
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

// installGroundRoute is the search's publication and nothing else.
//
// Correction (2026-08-31, [05 R-EGRESS-02]). This used to run the three
// acceptance gates of [04 R-PATH-01 §8] over every publication, on that
// section's parenthetical "when a newly published route (or a new goal object)
// is installed". The parenthetical is inverted: the gates belong to the GOAL
// INSTALLER alone (installGroundGoal below). The publisher itself — the only
// writer the search uses — clamps the count to 20, writes the count and the
// points, sets has-waypoint and dirty, and clears wants-repath, with no
// point-count test, no goal-point query, no terminal-cell test, no
// half-distance test and no synthetic rewrite; [04 R-PATH-01 §7]'s publication
// contract states exactly those five effects and Route.PublishAtRevision
// already implements them.
//
// The consequence of the inversion was a liveness bug, not a cosmetic one:
// collinear removal collapses a straight or diagonal A* run to exactly TWO
// points, both point-count gates require three or more, so a perfectly good
// two-point route around an obstacle was rejected and overwritten by the
// synthetic straight line at the goal — aiming the mover back into whatever it
// had just routed around, on every republication, forever. The follower kept
// believing it held a route, so nothing ever reported blocked.
func installGroundRoute(route *Route, points []Point, revision uint64) {
	if route == nil {
		return
	}
	route.PublishAtRevision(points, revision)
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
	// One publisher for ground and air alike: a published route is adopted
	// verbatim [04 R-PATH-01 §7][05 R-EGRESS-02]. The acceptance gates run only
	// where a goal object is installed (installGroundGoal).
	installGroundRoute(route, mPoints, revision)
	route.Status = status
	if status == path.StatusRejected {
		s.recordPathFailure(r.Unit, status, s.tick)
	} else {
		s.ClearPathFailure(r.Unit)
	}
}

// emitMovementCallbacks emits StartMoving/StopMoving/MoveRateN and setSFXoccupy per [04 §5.2][GAP T15] C17 C18.
// It is the per-unit movement-window immediate-start emission (I) through the
// movement-window immediate-start emitter, with the delta-0 barrier [GAP T15] C18.
// Must be called after steering/flight integration but before the next slot's clear/commit so the VM sees the walk loops [04 §1.1][01 §4.4].
// Retail classification: category 0 when blocked/inhibited/attached/both mags
// zero, else 1..3 via signed definition thresholds [04 R-COLL-01 §5][04 §5.2].
// We map def MoveRate1/2 via content.UnitDef.MoveRate1/2 (defaults twice MaxVelocity) [02 "Unit record"] [04 §5.2].
func (s *System) emitMovementCallbacks(u *units.Unit, speed int32) {
	if s == nil || u == nil {
		return
	}
	if s.prevMoveTier == nil {
		s.prevMoveTier = make(map[pool.Handle]int)
	}
	if s.prevSFXBand == nil {
		s.prevSFXBand = make(map[pool.Handle]int)
	}
	// The tier-0 override's first term is the mover's PERSISTED blocked flag.
	// [04 §5.2]'s "mover inhibit bit (bit 2 of the mover's state byte)" and
	// [04 R-COLL-01 §5]'s blocked flag are one and the same bit — the state byte
	// carries the mover mode in bits 0–1 and the blocked flag in bit 2, and there
	// is no separate inhibit latch. So the classifier reads the flag the commit's
	// validation gate last wrote, which happens only on a cross-cell or
	// mode-changing proposal: between verdicts the flag is stale by construction
	// and a unit that was rejected still classifies as tier 0 while it moves
	// inside its own cell [04 R-COLL-01 §5][04 R-MOV-01 §6].
	blocked := false
	// The adjacent word tested with the scalar speed is the signed 16-bit turn
	// residual, not a second speed component [04 §5.2][04 R-MOV-01 §6]. The
	// ground steering step does not yet drive a turn residual word, so this reads
	// zero except after a save restore; the flight branch keeps its residual on
	// the flight state and never mirrors it here.
	turnResidual := int32(0)
	if coll := s.Collisions[u.Handle]; coll != nil {
		blocked = coll.Blocked
		turnResidual = int32(coll.TurnResidual)
	}
	attached := u.Attachment.Carrier != 0
	// Magnitude is the mover's scalar speed word; ground uses scalar Speed and the
	// flight branch passes its own [04 §5.2][04 R-MOV-01 §6] C18.
	magA := speed
	magZ := turnResidual
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
	cat := cob.MoveRateCategory(blocked, attached, magA, magZ, rate1, rate2) // [04 §5.2][04 R-MOV-01 §6] C18
	// The cache update is the classifier's final write [04 §5.2], and it is
	// engine state rather than a script start: it happens whether or not this
	// unit carries a script, and it is what the weapon drift gate reads
	// [06 R-WPN-03 §2]. Writing it unconditionally (an unchanged category still
	// rewrites the same two bits) keeps the gate reading the last verdict.
	u.MoveTier = uint8(cat)
	vm := u.GetScript()
	if vm == nil {
		return
	}
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
	// setSFXoccupy band 0..4 [04 §9.1] — the classifier starts from this unit's
	// cached band and the one-argument callback is edge-triggered, emitted only
	// when the band changes [GAP T15] C17 C18.
	prevBand := s.prevSFXBand[u.Handle]
	band := MediumBand(s.Terrain, u, prevBand)
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
// before any StepUnit in the tick; the world must have been bound via BindWorld.
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
	// The target registry's third list, on its own cadence — the call is made
	// every tick and Rebuild itself applies the 30-tick throttle, so the
	// snapshot lands on a tick boundary [06 §3.1][04 R-AIR-01 §11].
	if w != nil {
		s.airBases.Rebuild(tick, w.Iter(), s.diplomacyRows())
	}
}

// diplomacyRows is the one-directional alliance row read the registry rebuild
// needs [05 R-SHARE-01 §1]. A session composes exactly one order binding and
// installs it on every unit's queue, so the first queue that carries one is the
// session's; the movement system holds no economy handle of its own. Nil when
// no binding is composed yet, in which case the rebuild treats only the ally
// group's own units as friendly.
func (s *System) diplomacyRows() func(from, toward uint8) bool {
	if s == nil || s.world == nil {
		return nil
	}
	for _, u := range s.world.Iter() {
		b := airBinding(u)
		if b == nil || b.World == nil || b.World.DeclaresAlliance == nil {
			continue
		}
		return b.World.DeclaresAlliance
	}
	return nil
}

// AirBaseList is the read side of the third list, for the ally group's own
// index [06 §3.1][04 R-AIR-01 §11]. internal/orders reaches it through the
// movement goal port rather than keeping a second enumeration that could
// disagree with this one — the same reason TransportAdmission is a port.
func (s *System) AirBaseList(allyGroup uint8) []pool.Handle {
	if s == nil {
		return nil
	}
	return s.airBases.List(allyGroup)
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

// StepUnit advances ONLY the unit identified by handle through the phase-2
// integration path [04 §8.1][04 §8.2][04 §10.1]. The session composes the
// BeginTick+ascending StepUnit loop+EndTick transaction. Shared per-tick indexing
// from BeginTick is consumed;
// published routes are consumed without duplicate submission; route/goal
// completion uses goal tolerance (arrival) and is exposed via StepResult.
// Ground, air, landing, transport states keep working [04 §9.1][04 §10.2].
// No presentation/camera state enters movement [I6]. Deterministic.
func (s *System) StepUnit(handle pool.Handle, tick uint32) StepResult {
	if s == nil {
		return StepResult{Handle: handle, EmptyRoute: true, LifecycleError: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	// Phase 2 is one explicit transaction. A direct StepUnit call cannot
	// reconstruct its per-tick cargo snapshot safely, so report the lifecycle
	// violation and leave gameplay state untouched [01 §4.4][04 §8.2].
	if !s.tickStarted || s.tick != tick {
		return StepResult{Handle: handle, EmptyRoute: true, LifecycleError: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	w := s.world
	if w == nil {
		return StepResult{Handle: handle, EmptyRoute: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	u := w.Unit(handle)
	if u == nil || !u.Alive {
		return StepResult{Handle: handle, EmptyRoute: true, DistToGoal: numeric.Fixed(1 << 30)}
	}
	// Cargo membership is read only from the snapshot captured by BeginTick
	// [04 §10.2]. The active lifecycle check above makes a direct carrier read
	// unnecessary and prevents StepUnit from becoming a second transaction owner.
	if _, isCarried := s.tickCarried[handle]; isCarried {
		// Transport removes the mover from the ground route scheduler. Keep
		// the queue record intact for the eventual unload, but clear all
		// follower state so a carried unit cannot re-arm an old request
		// [04 §10.2][04 R-PATH-01 §8].
		s.DeactivateMove(handle)
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
		res := s.stepAir(u, tick)
		// The flight commit writes the new cached cell pair; the stamp follows
		// it, so an airborne mover holds the air word of the rectangle it is
		// actually over and a landed one holds the ground word
		// [04 R-COLL-01 §1] step (4) [04 R-COLL-01 §4]. A rectangle that left
		// the map writes no cell.
		s.syncMoverStamp(u)
		return res
	}
	// The queue selects the STEERING TARGET; it does not gate the mover tick.
	// The sweep runs the mover tick and then the post-move correction for every
	// live unit that has a mover, gated on the owner's control byte and never on
	// an order record [04 R-MOV-03 §1] step 9. The control-byte gate belongs to
	// the session's phase-2 sweep, which owns the player and unit iteration;
	// this function is only the per-unit body it calls. The sweep now applies
	// that gate: the player gate admits control bytes 1, 2 and 3, and the inner
	// block that holds the mover tick admits 1 and 2 only.
	// A unit with no order, or one whose
	// head is neither a movement record nor carries a goal, therefore still
	// reaches the mover: it is exactly the follower's no-waypoint case, which
	// brakes without turning and never touches the heading [04 R-MOV-01 §3], its
	// commit takes the stationary early return or the same-cell fast path
	// [04 R-COLL-01 §1], and the post-move correction still owns its Y
	// [04 R-MOV-01 §5]. Before this, an orderless mover returned here and kept
	// whatever Y and occupancy it was spawned or released with.
	orderless := false
	q := orders.QueueForUnit(u)
	if q == nil || (q.LenPrimary() == 0 && q.Head() == nil) {
		orderless = true
	}
	var head *orders.Node
	if !orderless {
		if q.LenPrimary() > 0 {
			head = q.Primary()[0]
		} else {
			head = q.Head()
		}
		if head == nil {
			orderless = true
		}
	}
	if head != nil {
		name := orders.DescriptorFor(head.ID).Name
		if name != "Move_Ground" && name != "VTOL_Move" && name != "QMove" && name != "VTOL_MobileBuild" && name != "MobileBuild" && name != "VTOL_Patrol" && name != "Patrol" && name != "Park" {
			if head.GoalX == 0 && head.GoalZ == 0 && head.GoalY == 0 {
				// A goal-less non-movement head steers nothing, so the follower
				// has no payload. The mover tick below still runs.
				orderless = true
				head = nil
			}
		}
	}
	// A building has no mover, so the sweep's mover tick and post-move
	// correction do not run for it [04 R-MOV-03 §1] step 9. Neither does a unit
	// movement never admitted, which has no mover record to tick.
	steer := s.Steers[handle]
	coll := s.Collisions[handle]
	if steer == nil || coll == nil || coll.Building {
		d := s.distToGoal(u)
		s.emitMovementCallbacks(u, 0)
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false}
	}
	// With no order there is no installed route: the follower's service has no
	// payload to revalidate, no points to prune and nothing to arm
	// [04 R-MOV-03 §1] "The follower's per-tick service".
	var route *Route
	if !orderless {
		route = s.Routes[handle]
		s.serviceGroundFollower(u, head, route, tick)
	}
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
	if orderless {
		// No payload, so no arrival test and no goal distance to consult: the
		// no-waypoint follower zeroes its turn residual and asks the speed
		// update for `-BrakeRate` [04 R-MOV-01 §3]. Everything after this point
		// is the mover tick retail runs whether or not a record exists.
		brakingOnly = true
	} else if !hadRoute {
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
		// `steer` and `coll` were resolved at the mover gate above; a unit
		// without both never reaches here.
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
			steer.Acceleration = int32(u.Def.Acceleration)
			steer.BrakeRate = int32(u.Def.BrakeRate)
		}
		steer.UpdateHeading(desired) // [04 §8.1] C20
		// Speed capping consumes the authoritative unit pitch word [04 R-MOV-01 §4].
		cap := steer.SpeedCapForPitch(int16(u.Move.Pitch))
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
		// The stationary early return of [04 R-COLL-01 §1]: when the proposal
		// equals the current position on all three axes AND the proposed mode
		// equals the committed mode mirror, the commit step returns with nothing
		// written — no transform-dirty bit, no validator, no stamp, and the
		// blocked flag untouched. A unit at rest never revalidates. The vertical
		// axis is always equal here because the ground speed update writes
		// vertical velocity as a literal zero [04 R-MOV-01 §4]. Without this,
		// every parked mover took the same-cell fast path, which DOES set dirty,
		// and so ran the post-move correction every tick — the very thing
		// `canhover` alone is supposed to force [04 R-MOV-01 §5].
		stationary := coll.VX == 0 && coll.VZ == 0 && coll.Mode&0x3 == coll.CachedMode&0x3
		fastPath, isBlocked := true, false
		if !stationary {
			// On any non-stationary proposal the last-proposal tick is written
			// FIRST, before the cell test and before the validator, so it records
			// the last tick on which the unit TRIED to change position or mode —
			// blocked ticks included [04 R-COLL-01 §1]. It is the age term the
			// hover bob of [04 R-MOV-01 §5] reads; with no writer at all every
			// hovercraft's bob faded to zero once the tick passed 60.
			coll.LastProposalTick = tick
			fastPath, isBlocked = coll.CommitOne(s.Grid, coll.Mode, perCell, nil) // [04 §8.2] C23 C24: sync clear-then-stamp before next slot
		}
		blocked = isBlocked
		// Occupancy was committed (clear/commit/stamp, [04 §8.2] C22): record
		// the unit's occupancy-commit tick so the request revision pass of
		// [04 §6.1 R-DOC04-B] sees it. The same-cell fast path commits the
		// transform without restamping occupancy [04 §8.2] C23, so it writes
		// no commit tick.
		// Both halves of the write-site question this site used to carry are
		// closed by [04 R-PATH-01 §14]. (a) There is no owner/building-mask
		// array and so no mask writer: the word the search tests is the
		// visibility publisher's mapping grid, whose complete writer set is the
		// map-load fill, the bulk rebuild and the phase-5 LOS stamp — the
		// occupancy commit, the footprint stamp and clear, unit creation and
		// building completion never reference it. searchFunc binds a view of
		// that grid. (b) Unit creation DOES write the commit tick: the creator
		// stamps the new unit's footprint after the initializer returns, and
		// the footprint stamp writes the tick as its first action; EnsureUnit
		// does the same.
		if !isBlocked && !fastPath {
			s.noteOccupancyCommit(handle, tick)
		}
		coll.BlockerID = blockerID
		u.X = numeric.Fixed(int64(coll.X))
		u.Z = numeric.Fixed(int64(coll.Z))
		groundDirty := u.Flags&unitTransformDirty != 0 || steer.Dirty || coll.Dirty || (u.Def != nil && u.Def.CanHover)
		steer.X = coll.X
		steer.Z = coll.Z
		steer.Heading = coll.Heading
		steer.Speed = coll.Speed
		u.Move.Heading = coll.Heading
		u.Move.Speed = numeric.Fixed(coll.Speed)
		// Emit StartMoving/StopMoving/MoveRateN and setSFXoccupy per [04 §5.2][GAP T15] C17 C18 via immediate barrier [GAP T15] C18.
		// Must run after speed commit so tier reflects current capped speed [04 §5.2][GAP T15] C18.
		// The scalar speed is passed as committed. A blocked mover retains a
		// capped scalar speed for its next proposal [04 R-COLL-01 §5]; it reaches
		// tier 0 through the classifier's blocked term, which now reads the
		// persisted flag directly, so this site no longer substitutes a zero speed
		// to force the same category.
		s.emitMovementCallbacks(u, coll.Speed)
		// This follows the complete mover tick, including its callbacks, and is
		// the only ordinary ground pose writer [04 R-MOV-01 §5a].
		applyGroundPostMove(s.Terrain, u, groundDirty, coll.Mode, newHoverBob(u, coll.Speed, s.tick, coll.LastProposalTick))
		if groundDirty {
			u.Flags &^= unitTransformDirty
			steer.Dirty = false
			coll.Dirty = false
		}
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

// gridOccupancy adapts the mover occupancy lattice to the placement
// validator's occupant test. Retail reads one ground word; this is the half of
// it that ground movers write [04 R-COLL-01 §4].
type gridOccupancy struct{ grid *OccupancyGrid }

// CellOccupant returns the identity holding the cell, or 0 when it is free.
func (g gridOccupancy) CellOccupant(cellX, cellZ int32) uint16 {
	if g.grid == nil {
		return 0
	}
	id, held := g.grid.OccupantAt(Cell{X: cellX, Z: cellZ})
	if !held || id == 0 {
		return 0
	}
	if id < 0 || id > int(^uint16(0)) {
		// An identity that does not fit the occupancy word is still an
		// occupant; reporting it free would admit a placement over a live
		// unit. The placement identity range is bounded well below this
		// elsewhere (reservePlacement refuses a wider handle), so this is a
		// bounds guard, not a behavior [I11].
		return ^uint16(0)
	}
	return uint16(id)
}
