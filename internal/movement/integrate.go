// Package movement — per-unit integration glue [04 §8.1][04 §8.2][04 §10.1][04 §7.1][04 §7.3].
//
// Integrate.go is the per-unit integration glue that owns FlightState/SteerState/CollisionState
// surfaces for gate-2 and later phases. It bridges units.World, orders queues, the path.Scheduler,
// and the ground/flight integrators.
//
// Public API per PLAN_07:
//
//	func Integrate(u *units.Unit, w *world.Terrain, tick uint32)
//	func IntegrateFlight(u *units.Unit, w *world.Terrain)
//
// Both are provided as thin wrappers that delegate to the System's per-unit state.
// The System type is the composition root that the kernel's movement window calls each tick.
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
// One System is created per gate2Session (or later per session) and is the sole writer
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
	sessions     []*path.Session         // deterministic slice indexed by handle [04 §7.3] C11 C12 budget-honoring sessions
	prevMoveTier map[pool.Handle]int     // cached mover tier per unit for MoveRate edge emission [04 §5.2][GAP T15] C18
	prevSFXBand  map[pool.Handle]int     // cached setSFXoccupy band per unit for edge emission [04 §5.2][GAP T15] C17 C18
	avoidNext    map[pool.Handle]uint32  // named deterministic land-avoidance cadence

	// world is the units world bound via BindWorld (or via Tick for legacy path).
	// StepUnit needs it to fetch the *units.Unit for a handle without passing
	// the world on every per-unit call, so the caller can invoke StepUnit
	// inside its own slot visit [04 §1.3] sweep order.
	world *units.World

	// per-tick shared indexing built deterministically ONCE in BeginTick [04 §8.2] C22.
	// StepUnit consumes it; EndTick clears it.
	tickStarted bool
	tick        uint32
	tickCarried map[pool.Handle]struct{}

	pathFailures map[pool.Handle]PathFailure // last non-success publish per handle [04 §7.3] C12 [P0-08][P0-12]
}

// localSteeringThresholdSquared is only the near-waypoint brake/steering
// threshold. It is not final Move_Ground completion; that tolerance remains an
// explicit R-P0-01 question. [04 §3.5]
const localSteeringThresholdSquared uint64 = uint64(2*65536) * uint64(2*65536)

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
		Terrain:      terrain,
		Fallback:     fallback,
		Grid:         grid,
		Routes:       make(map[pool.Handle]*Route),
		Steers:       make(map[pool.Handle]*SteerState),
		Collisions:   make(map[pool.Handle]*CollisionState),
		Flights:      make(map[pool.Handle]*FlightState),
		profiles:     make(map[pool.Handle]Profile),
		prevMoveTier: make(map[pool.Handle]int),
		prevSFXBand:  make(map[pool.Handle]int),
		avoidNext:    make(map[pool.Handle]uint32),
		pathFailures: make(map[pool.Handle]PathFailure),
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

// BindWorld binds the units world for per-unit stepping [04 §1.3].
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
	q := orders.QueueForUnit(u)
	if q != nil && q.LenPrimary() > 0 {
		head := q.Primary()[0]
		if head != nil && (head.GoalX != 0 || head.GoalZ != 0 || head.GoalY != 0 || head.Target != 0) {
			return squaredDistanceFixed(int64(head.GoalX), int64(head.GoalZ), int64(u.X), int64(u.Z)), true
		}
	}
	route := s.Routes[u.Handle]
	if route != nil && route.Active && route.Count > 0 {
		last := route.Points[route.Count-1]
		wpX := addSignedSaturating(int64(world.CellToWorld(last.X)), 524288)
		wpZ := addSignedSaturating(int64(world.CellToWorld(last.Z)), 524288)
		return squaredDistanceFixed(wpX, wpZ, int64(u.X), int64(u.Z)), true
	}
	if q != nil {
		if h := q.Head(); h != nil && (h.GoalX != 0 || h.GoalZ != 0) {
			return squaredDistanceFixed(int64(h.GoalX), int64(h.GoalZ), int64(u.X), int64(u.Z)), true
		}
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

// finalGoalReached deliberately does not turn route pruning into order
// completion. R-P0-01 did not establish the final Move_Ground tolerance or
// comparison domain; retaining the order active is the safe deterministic
// behavior until that question is answered.
func (s *System) finalGoalReached(u *units.Unit, hadRoute bool) bool {
	if !hadRoute || u == nil {
		return false
	}
	// TODO(question): exact final Move_Ground completion tolerance, units, and
	// inclusive comparison remain unresolved by R-P0-01. Do not fabricate a
	// five-cell or two-world-unit threshold here.
	return false
}

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
	goalObj := path.PointGoal(goal, 0) // radius 0 [task]
	req := path.Request{
		Unit:   handle,
		Player: player,
		Start:  start,
		Goal:   goalObj,
	}
	s.Scheduler.Submit(req)
}

// replanDynamicBlock applies the explicit land-skirmish avoidance policy from
// the overnight plan: stable lower pool slots have priority; a higher slot
// yields and submits a route from its current anchor to the original order
// goal. The one-tick cadence is a named Nanolathe policy because retail retry
// timing is not established [R-P1-10]. No reverse, push, crush, or teleport.
func (s *System) replanDynamicBlock(u *units.Unit, tick uint32, blockerID int) {
	if s == nil || u == nil || blockerID < 0 || int(u.Handle) <= blockerID || s.Scheduler == nil {
		return
	}
	if s.avoidNext == nil {
		s.avoidNext = make(map[pool.Handle]uint32)
	}
	if next := s.avoidNext[u.Handle]; tick < next {
		return
	}
	s.avoidNext[u.Handle] = tick + 1 // deterministic local policy cadence
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	head := q.Primary()[0]
	if head == nil || (head.GoalX == 0 && head.GoalZ == 0) {
		return
	}
	start := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	goal := path.Cell{X: world.WorldToCell(head.GoalX), Z: world.WorldToCell(head.GoalZ)}
	s.SubmitMove(u.Handle, u.Owner, start, goal)
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
		isPassable := func(c path.Cell) bool {
			if s.Terrain != nil {
				if !profile.IsPassableFootprint(s.Terrain, c.X, c.Z) {
					return false
				}
			}
			if s.Grid != nil {
				mc := Cell{X: c.X, Z: c.Z}
				if occ, ok := s.Grid.OccupantAt(mc); ok && occ != int(r.Unit) {
					return false
				}
			}
			return true
		}
		bias := path.Point{X: int32(profile.FootPrintX / 2), Z: int32(profile.FootPrintZ / 2)}
		var hasBounds bool
		var bounds path.Rect
		if s.Terrain != nil && s.Terrain.CellW > 0 && s.Terrain.CellH > 0 {
			hasBounds = true
			bounds = path.Rect{Min: path.Cell{X: 0, Z: 0}, Max: path.Cell{X: s.Terrain.CellW - 1, Z: s.Terrain.CellH - 1}}
		}
		cfg := path.SearchConfig{
			Start:      r.Start,
			Goal:       r.Goal,
			IsPassable: isPassable,
			Scale:      scale,
			Bias:       bias,
			HasBounds:  hasBounds,
			Bounds:     bounds,
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
	route := s.Routes[r.Unit]
	if route == nil {
		route = &Route{}
		s.Routes[r.Unit] = route
	}
	mPoints := make([]Point, len(points))
	for i, p := range points {
		mPoints[i] = Point{X: p.X, Z: p.Z}
	}
	route.Publish(mPoints)
	// Overnight land policy: Kbots may use aggressive legality-only line of
	// sight smoothing; vehicles retain conservative forward-only waypoints until
	// authored turning/braking feasibility is fully recovered [plan §3.1].
	// The complete footprint predicate is used for every ray cell, so a shortcut
	// cannot cut a diagonal corner. Exact bad-slope speed/cost remains TODO.
	if s.world != nil {
		if u := s.world.Unit(r.Unit); u != nil && u.Def != nil {
			smoothLandRoute(route, u.Def, s.ProfileFor(r.Unit), s.Terrain)
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
// Must be called after steering/flight integration but before the next slot's clear/commit so the VM sees the walk loops [04 §1.3][01 §4.4].
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
	_ = s.resolveProfile(u) // retained for profile revision side-effects if any; outer profile not needed for pitch path [M2]
	var directGoal bool
	var directX, directZ numeric.Fixed
	if !hadRoute {
		d := s.distToGoal(u)
		d2, hasGoal := s.distSqToGoal(u)
		if hasGoal && d2 <= localSteeringThresholdSquared {
			s.emitMovementCallbacks(u, 0)
			// Route absence is not proof of final order completion; the
			// order-layer tolerance remains unresolved [R-P0-01].
			return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false, Arrived: false}
		}
		s.emitMovementCallbacks(u, 0)
		return StepResult{Handle: handle, DistToGoal: d, HasRoute: false, EmptyRoute: true, Moved: false, Arrived: false}
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
			// Still far: direct move to goal.
			if head != nil && (head.GoalX != 0 || head.GoalZ != 0) {
				directGoal = true
				directX = head.GoalX
				directZ = head.GoalZ
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
		_, isBlocked := coll.CommitOne(s.Grid, coll.Mode, perCell, aggregate) // [04 §8.2] C23 C24: sync clear-then-stamp before next slot
		blocked = isBlocked
		coll.BlockerID = blockerID
		if blocked && blockerID >= 0 {
			// Lower slot wins the deterministic priority; higher slot yields
			// and requests a fresh route from its current anchor.
			s.replanDynamicBlock(u, tick, blockerID)
		}
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
	return StepResult{Handle: handle, DistToGoal: d2, HasRoute: hasRouteAfter, EmptyRoute: false, Moved: moved, Blocked: blocked, Arrived: arrived}
}

// Tick runs the per-unit integration glue for all alive units in w.
// It is kept for compatibility and documented non-authoritative so the future
// central loop (units.World sweep that calls BeginTick+loop(StepUnit)+EndTick)
// replaces it. The implementation is BeginTick + deterministic slot-asc loop of
// StepUnit + EndTick, preserving the same integration path Tick uses today [task].
// Mobile occupancy is committed synchronously in sweep order; clear-then-stamp
// finishes before next slot; vacated cell reusable same tick [04 §8.2] C22.
func (s *System) Tick(tick uint32, w *units.World) {
	if s == nil || w == nil {
		return
	}
	// Bind world for per-unit calls.
	s.world = w
	s.BeginTick(tick) // shared per-tick indexing built once [task]
	// Deterministic snapshot of handles: player 0..9 asc then slot asc [I1].
	// Iterate over snapshot so vacancy reuse same tick is visible via Grid but
	// iteration order is stable.
	units := w.IterSliced()
	for _, u := range units {
		if u == nil || !u.Alive {
			continue
		}
		// StepUnit advances ONLY that unit through the same integration path [task]
		_ = s.StepUnit(u.Handle, tick)
	}
	s.EndTick(tick) // cargo slaving + pad repair + clear per-tick state
}

// Integrate is the ground integrator wrapper required by PLAN_07 Public API.
// It delegates to the per-unit SteerState when a System has been bound to the unit
// via EnsureUnit, otherwise it no-ops. The world terrain is used for pitch/height.
func Integrate(u *units.Unit, w *world.Terrain, tick uint32) {
	_ = tick
	if u == nil || w == nil {
		return
	}
	// This wrapper is for external callers that don't hold a System. It creates a
	// transient SteerState from the unit's def and terrain, steps once toward the
	// order's goal if any, and writes back. It is not the scheduler-driven path;
	// the System.Tick path is preferred for gate2.
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	head := q.Primary()[0]
	if head == nil {
		return
	}
	goalX := head.GoalX
	goalZ := head.GoalZ
	dx := int64(goalX) - int64(u.X)
	dz := int64(goalZ) - int64(u.Z)
	if dx == 0 && dz == 0 {
		return
	}
	desired := headingFromDelta(dx, dz)
	steer := &SteerState{
		X:            int32(u.X.Raw()),
		Z:            int32(u.Z.Raw()),
		Heading:      0,
		MaxVelocity:  int32(u.Def.MaxVelocity),
		TurnRate:     int32(u.Def.TurnRate),
		HeightWord:   int16(u.Y.Raw() >> 16),
		SeaLevel:     w.SeaLevel,
		PitchScale:   int32(u.Def.PitchScale),
		BankScale:    int32(u.Def.BankScale),
		Acceleration: int32(u.Def.Acceleration),
		BrakeRate:    int32(u.Def.BrakeRate),
	}
	var f uint32
	if u.Def.CanHover {
		f |= 0x1000
	}
	if u.Def.Floater {
		f |= 0x80000
	}
	steer.DefFlags = f
	steer.UpdateHeading(desired)
	steer.UpdatePitch(steer.PendingHeading) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	cap := steer.SpeedCapFromPitch()        // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Transient wrapper: no history, treat as hasWaypoint true with large dist for accel [M3]
	steer.UpdateSpeedWithBraking(cap, true, 1<<30, false) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	steer.Integrate()
	u.X = numeric.Fixed(int64(steer.X))
	u.Z = numeric.Fixed(int64(steer.Z))
	u.Y = w.HeightAt(u.X, u.Z)
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
