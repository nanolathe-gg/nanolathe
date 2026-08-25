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
	"strings"

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

	Grid       *OccupancyGrid
	Scheduler  *path.Scheduler
	Routes     map[pool.Handle]*Route
	Steers     map[pool.Handle]*SteerState
	Collisions map[pool.Handle]*CollisionState
	Flights    map[pool.Handle]*FlightState
	profiles   map[pool.Handle]Profile // per-unit resolved movement profile [04 §6.1]
	sessions   []*path.Session         // deterministic slice indexed by handle [04 §7.3] C11 C12 budget-honoring sessions
}

// NewSystem creates a System bound to terrain/profile/grid. It allocates the per-unit
// maps and a path.Scheduler whose SearchFunc is bound to path.Search with profile
// passability over the supplied terrain, and whose PublishFunc stores into Routes via
// Route.Publish [04 §7.3] C14.
func NewSystem(terrain *world.Terrain, fallback Profile, grid *OccupancyGrid) *System {
	s := &System{
		Terrain:    terrain,
		Fallback:   fallback,
		Grid:       grid,
		Routes:     make(map[pool.Handle]*Route),
		Steers:     make(map[pool.Handle]*SteerState),
		Collisions: make(map[pool.Handle]*CollisionState),
		Flights:    make(map[pool.Handle]*FlightState),
		profiles:   make(map[pool.Handle]Profile),
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
	// SteerState
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
				if !profile.IsPassable(s.Terrain, c.X, c.Z) {
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
	_ = status
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

// Tick runs the per-unit integration glue for all alive units in w. It must be
// called in the movement integration window after Scheduler.Tick [01 §4.4] I7.
// For each unit with an active route and a Move_Ground-class order, it does:
// Prune, UpdateHeading toward current waypoint, Integrate, then collision fast-path/blocked handling.
// Carried cargo is slaved to carrier and skips integration [04 §10.2]. Tick iterates
// player 0..9 asc then slot asc [I1].
func (s *System) Tick(tick uint32, w *units.World) {
	_ = tick
	if s == nil || w == nil {
		return
	}
	// Build set of carried handles to skip in main integration [04 §10.2].
	carried := make(map[pool.Handle]struct{}, 8)
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if u.Attachment.Carrier != 0 {
			carried[u.Handle] = struct{}{}
		}
	}
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if _, isCarried := carried[u.Handle]; isCarried {
			continue // cargo branch slaved after carrier moves [04 §10.2]
		}
		// Keep orders queue as authority: only follow route if primary order is Move_Ground-class [task]
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			continue
		}
		head := q.Primary()[0]
		if head == nil {
			continue
		}
		name := orders.DescriptorFor(head.ID).Name
		if name != "Move_Ground" && name != "VTOL_Move" && name != "QMove" && name != "VTOL_MobileBuild" && name != "MobileBuild" && name != "VTOL_Patrol" && name != "Patrol" {
			// Still allow generic move for demo
			if head.GoalX == 0 && head.GoalZ == 0 && head.GoalY == 0 {
				continue
			}
		}
		route := s.Routes[u.Handle]
		if route == nil || !route.Active {
			continue
		}
		// Prune(mover pos) [04 §7.3] C15. The stored points carry the
		// half-footprint bias added at publication (SearchConfig.Bias), so
		// the mover's position is compared in the same biased domain.
		profile := s.resolveProfile(u)
		moverPt := Point{
			X: world.WorldToCell(u.X) + int32(profile.FootPrintX/2),
			Z: world.WorldToCell(u.Z) + int32(profile.FootPrintZ/2),
		}
		route.Prune(moverPt)
		if !route.Active || route.Count == 0 {
			continue
		}
		// Current waypoint: index 1 if available else 0 [task]
		var wp Point
		if route.Count > 1 {
			wp = route.Points[1]
		} else {
			wp = route.Points[0]
		}
		// Convert waypoint cell+bias to world Fixed for heading [03 §2.1]
		wpWorldX := world.CellToWorld(wp.X)
		wpWorldZ := world.CellToWorld(wp.Z)
		// Aim at cell center [03 §2.1]
		wpWorldX = numeric.Fixed(int64(wpWorldX) + 524288) // 0.5 cell = 524288 = 1<<19
		wpWorldZ = numeric.Fixed(int64(wpWorldZ) + 524288)
		dx := int64(wpWorldX) - int64(u.X)
		dz := int64(wpWorldZ) - int64(u.Z)
		if dx == 0 && dz == 0 {
			continue
		}
		desired := headingFromDelta(dx, dz)

		// Ground vs air branch [04 §10.1] C26: can-fly units use flight integrator
		if u.Def != nil && u.Def.CanFly {
			flight := s.Flights[u.Handle]
			if flight == nil {
				continue
			}
			// Sync flight state from unit
			flight.X = int32(u.X.Raw())
			flight.Y = int32(u.Y.Raw())
			flight.Z = int32(u.Z.Raw())
			flight.TargetX = int32(wpWorldX.Raw())
			flight.TargetZ = int32(wpWorldZ.Raw())
			flight.TargetHeading = desired
			// Cruise altitude: targetY = max(sea level, terrain height at target XZ) + cruisealt [04 §10.1]
			// For waypoint following, use full cruisealt at waypoint.
			if s.Terrain != nil && u.Def != nil {
				targetY := CruiseAltitudeForOffset(s.Terrain, wpWorldX, wpWorldZ, u.Def.CruiseAlt)
				flight.TargetY = int32(targetY.Raw())
			} else {
				flight.TargetY = int32(u.Y.Raw())
			}
			// Keep MaxVelocity etc from def if zero
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
			IntegrateFlight(flight)
			u.X = numeric.Fixed(int64(flight.X))
			u.Y = numeric.Fixed(int64(flight.Y))
			u.Z = numeric.Fixed(int64(flight.Z))
			// Sync move heading/pitch/bank for snapshot [03 §2.4] C21
			u.Move.Heading = flight.Heading
			// Wake SFX band for air not needed; hover wake handled below for ground hover? Keep but air no wake.
			continue
		}

		steer := s.Steers[u.Handle]
		coll := s.Collisions[u.Handle]
		if steer == nil || coll == nil {
			continue
		}
		// Sync steer position and terrain state
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
		}
		// Update heading before integration [04 §8.1] C20
		steer.UpdateHeading(desired)
		// Pitch input: the signed height difference toward the waypoint cell.
		// TODO(question): [04 §8.1] does not name the exact operands feeding
		// the >>11 shift; the waypoint's coarse floor against the unit's Y is
		// the working reading (a one-height-byte step saturates the table).
		biasX := int32(profile.FootPrintX / 2) // strip the publication bias to get the waypoint's own cell [04 §7.1] C1
		biasZ := int32(profile.FootPrintZ / 2)
		pitchDelta := int32(s.Terrain.CoarseHeightAt(wp.X-biasX, wp.Z-biasZ).Raw() - int64(u.Y.Raw()))
		cap := steer.SpeedCap(pitchDelta)
		steer.UpdateSpeed(cap, pitchDelta)
		steer.Integrate()

		// Collision handling [04 §8.2] C23 C24
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
		// Collision admission uses the MOVING unit's own profile, so it agrees
		// with the passability the path was searched under [04 §6.1] [04 §8.2].
		moverProfile := s.ProfileFor(u.Handle)
		perCell := func(c Cell) bool {
			if s.Terrain != nil && !moverProfile.IsPassable(s.Terrain, c.X, c.Z) {
				return false
			}
			if s.Grid != nil {
				if occ, ok := s.Grid.OccupantAt(c); ok && occ != coll.ID {
					return false
				}
			}
			return true
		}
		aggregate := func() bool { return true }
		// TryFastPath is inside CommitOne; we call CommitOne which internally
		// does fast path then validator then blocked/success [04 §8.2] C23 C24.
		coll.CommitOne(s.Grid, coll.Mode, perCell, aggregate)
		u.X = numeric.Fixed(int64(coll.X))
		u.Z = numeric.Fixed(int64(coll.Z))
		if s.Terrain != nil {
			u.Y = s.Terrain.HeightAt(u.X, u.Z)
			if u.Y == numeric.Fixed(-1) {
				// OOB sentinel: keep previous Y
				u.Y = numeric.Fixed(int64(coll.Y))
			}
			// Hover/floater Y adjustments per [04 §9.2] waterline/floater clamp already in profile? Keep HeightAt.
		}
		// Sync steer to committed position
		steer.X = coll.X
		steer.Z = coll.Z
		steer.Heading = coll.Heading
		steer.Speed = coll.Speed
		// Sync move state for snapshot
		u.Move.Heading = coll.Heading
		u.Move.Speed = numeric.Fixed(coll.Speed)
		// Wake SFX for hover: band 2 or 3 emits wake types 2..5 [04 §9.1]
		// We record band in Move.Pitch/Bank? Not needed for physics, but keep for presentation via wake check.
		_ = ShouldEmitWake(s.Terrain, u)
	}
	// After all carriers moved, slave cargo [04 §10.2]
	s.SyncCarriedMotion(w)
	// Air repair on pads for landed VTOLs [04 §10.2] VTOL_GetRepaired
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if u.Def != nil && u.Def.CanFly && u.Move.Mode == 1 {
			// Check if on any IsAirBase pad
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
		X:           int32(u.X.Raw()),
		Z:           int32(u.Z.Raw()),
		Heading:     0,
		MaxVelocity: int32(u.Def.MaxVelocity),
		TurnRate:    int32(u.Def.TurnRate),
		HeightWord:  int16(u.Y.Raw() >> 16),
		SeaLevel:    w.SeaLevel,
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
	pitchDelta := int32(0)
	cap := steer.SpeedCap(pitchDelta)
	steer.UpdateSpeed(cap, pitchDelta)
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
