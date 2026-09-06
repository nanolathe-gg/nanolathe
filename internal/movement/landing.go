// Landing pads [04 §10.2].
//
// Landing pads: QueryLandingPad is a synchronous four-output query on the
// target's script; the four cells are seeded −1, walked 0..3, and the first
// piece that passes the free-pad predicate wins — the pad is not itself carried
// and no unit already in its cargo list holds that attach-piece index
// [04 §10.2][04 R-AIR-01 §6]. With no free piece the machine loiters about the
// pad and re-runs the query when it arrives; [04 R-AIR-01 §6]'s phase table
// gives no separate delayed retry, which supersedes §10.2's "30+rand(15)"
// prose. The machine itself is execVTOLLanding in airorders.go, and its phase 6
// is the only producer of pad repair — it pushes a `SelfRepair` record on the
// lander it attaches [04 R-AIR-01 §6]. Nothing in this file heals.
//
// IsAirBase via definition bit isairbase [02 "Unit record"][04 §10.2].
//
// Water landing, and where a landed seaplane rests. Ground landing is
// `VTOL_LandIfCan` (execVTOLLandIfCan in airorders.go); which cells it will
// accept is the landing-legality predicate of [04 R-AIR-01 §6a], whose aircraft
// water rule raises the water floor to sea level for a `canfly` definition that
// is NOT `amphibious`. The eight stock seaplanes are exactly the `amphibious`
// aircraft, so water is landable ground for them and for nothing else that
// flies. That much is settled and is what this package implements.
//
// Where a landed seaplane rests is settled [04 R-AIR-01 §6 "Touchdown"]: on
// the seabed. Phase 1 commands the terrain height over water, the flight
// integrator's vertical control descends to the commanded Y with no sea-level
// term [04 §10.1], and on the touchdown tick the position commit — entered by
// the mode/mirror mismatch the mode setter just created, not by any position
// delta — raises the transform-dirty bit, so the post-move correction of
// [04 R-MOV-01 §5] runs once and its fourth branch conforms the integer
// height, pitch and roll to the raw terrain bytes under the ground plate. Sea
// level is read on that path only inside the `canhover` arm, which no aircraft
// takes. The question this comment used to carry — whether some producer puts
// the seaplane back on the surface — closed as a bounded negative over every
// reader of the sea-level byte and every writer of a unit's Y: there is none.
// The surface look is not presentation either: the compositor blits at the raw
// unit Y and the waterline pass of [03 R-REN-03A §8] tints (own) or erases
// (enemy without sonar) everything below the surface, so retail draws a landed
// seaplane the way it draws a submarine. The rule lives in applyAirPostMove in
// integrate.go, after the flight branch of the mover tick.

package movement

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// IsLandingPad reports whether u is a landing pad. The test is the authored
// `isairbase` key and nothing else — word A bit 9 of [04 R-SPEC-01 §0], the
// only pad test the command resolver makes [04 R-ORD-02 §1], and the same key
// `VTOL_Landing` phase 6 reads on the pad owner [04 R-AIR-01 §7].
//
// The marker retired here called this a stub for "full pad detection via the
// QueryLandingPad script query". The two are different questions: `isairbase`
// says whether a unit is a pad at all, while the script query names WHICH of a
// pad's four pieces a lander attaches to. The piece question is still open, and
// is marked at FindFreePad below where it belongs.
func IsLandingPad(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.IsAirBase // [02 "Unit record"] [04 §10.2]
}

// FindFreePad finds a pad with a free landing piece for seeker, scanning units
// in the deterministic sweep order [I1].
//
// The marker retired here said the four-output `QueryLandingPad` query "has no
// caller in this build" and that occupancy was approximated. Both statements
// are stale: the landing machine `VTOL_Landing` runs the real query at three of
// its phases (queryLandingPad in airorders.go), which seeds four cells to −1,
// walks them 0..3 and accepts the first that passes the free-pad predicate of
// [04 R-AIR-01 §6] — the pad is not itself carried and no unit already in its
// cargo list holds that attach-piece index. There is no separate reservation
// table [04 §7.4 correction, "pad reservation"].
//
// This helper is the surface for callers outside the order boundary, so it
// answers with the pad rather than the piece. What it must not do is invent a
// second occupancy rule: the proximity test that stood here — any `canfly` unit
// within 32 world units occupies the pad — contradicted the established
// predicate in both directions, calling a free pad occupied because an
// aircraft was flying over it and a full pad free because its guests park on
// pieces further out than that.
//
// A pad with no script bound has no query to run; retail has no such pad, and
// nothing but a fixture reaches this arm, so it falls back to the predicate's
// own terms — a pad carrying nothing has a free piece.
func (s *System) FindFreePad(w *units.World, seeker *units.Unit) *units.Unit {
	if w == nil || seeker == nil {
		return nil
	}
	// Deterministic iteration: player 0..9 asc, slot asc [I1]
	for _, u := range w.IterSliced() {
		if u == nil || u == seeker {
			continue
		}
		if !IsLandingPad(u) {
			continue
		}
		if u.Attachment.Carrier != 0 {
			continue // carried pad not available [04 R-AIR-01 §6]
		}
		if u.ScriptBridge() == nil {
			if len(u.Attachment.Cargo) == 0 {
				return u
			}
			continue
		}
		if _, ok := s.queryLandingPad(u); ok {
			return u
		}
	}
	return nil
}

// CanLandOn checks if seeker can land on pad [04 §10.2] IsAirBase.
func CanLandOn(seeker, pad *units.Unit) bool {
	if seeker == nil || pad == nil {
		return false
	}
	if seeker.Def != nil && !seeker.Def.CanFly {
		return false // only VTOL seeks pad [04 §10.2]
	}
	return IsLandingPad(pad)
}

// Land attempts to land VTOL on pad [04 §10.2].
// Sets VTOL position to pad, heading to pad if needed, mode to parked (1), and Y to terrain height (landed).
// Returns true on success. No free pad among those tried keeps order alive for next-tick retry [04 §10.2].
func (s *System) Land(w *units.World, vtolHandle pool.Handle, pad *units.Unit) bool {
	if w == nil || pad == nil {
		return false
	}
	vtol := w.Unit(vtolHandle)
	if vtol == nil {
		return false
	}
	if !CanLandOn(vtol, pad) {
		return false
	}
	// Move to pad center
	vtol.X = pad.X
	vtol.Z = pad.Z
	if s.Terrain != nil {
		vtol.Y = s.Terrain.HeightAt(vtol.X, vtol.Z)
	} else {
		vtol.Y = pad.Y
	}
	if fl, ok := s.Flights[vtolHandle]; ok {
		fl.X = int32(vtol.X.Raw())
		fl.Z = int32(vtol.Z.Raw())
		fl.Y = int32(vtol.Y.Raw())
		fl.VX = 0
		fl.VY = 0
		fl.VZ = 0
		fl.Speed = 0
	}
	if st, ok := s.Steers[vtolHandle]; ok {
		st.X = int32(vtol.X.Raw())
		st.Z = int32(vtol.Z.Raw())
	}
	// The pad footprint is the anchor the ground-plane re-stamp below uses, so
	// the cached pair has to name the touchdown cell before the mode write.
	if coll, ok := s.Collisions[vtolHandle]; ok {
		coll.X = int32(vtol.X.Raw())
		coll.Z = int32(vtol.Z.Raw())
		coll.Y = int32(vtol.Y.Raw())
		coll.VX, coll.VZ = 0, 0
		anchor := coll.ProposedAnchor(1)
		coll.CachedAnchor = anchor
		coll.OldAnchor = anchor
	}
	// Touchdown goes through the one mover-mode setter: mode 1 is grounded, so
	// the setter zeroes the velocity components and the scalar speed and lowers
	// the activation edge (`Deactivate`, the landing script hook)
	// [04 R-AIR-01 §3], and moves the stamp from the air word back to the
	// ground word of the touchdown rectangle [04 R-COLL-01 §4].
	// Any outstanding goal payload is superseded: the touchdown is the end of
	// the leg that produced it [04 R-AIR-01 §1] step 6.
	s.releaseAirGoal(vtol)
	if !s.SetMoverMode(vtol, 1) {
		// Already grounded: the mode write is a no-op, so reconcile the stamp
		// against the new pad rectangle directly — the clear runs at the
		// rectangle that was stamped, not at the pad [04 R-COLL-01 §4].
		s.syncMoverStamp(vtol)
	}
	if fl, ok := s.Flights[vtolHandle]; ok {
		fl.Mode = 1
	}
	return true
}

// There is no pad-side healing helper here. The `AirRepair` routine retired at
// this site (WU-19-206) took an `amount` its caller supplied as a bare 5 with
// no citation and added it straight to a nearby aircraft's health. Retail has
// no healing producer of that shape at all: `VTOL_Landing` phase 6 pushes a
// `SelfRepair` record on the lander it attaches [04 R-AIR-01 §6], and every
// health point after that comes from the shared repair helper's kind-10 packet,
// one per admitted work visit, after the pad owner's one-resource energy
// admission accepts the visit's `buildcostenergy` term [05 R-WORK-01 §3]. The
// producer lives in execVTOLLanding; the work lives in internal/orders and
// internal/construction.

// LandingFailedMessage verbatim for no free pad [04 §10.2] distinct from "Landing aborted - all pads are occupied".
const LandingFailedMessage = "Landing failed"
const LandingAbortedMessage = "Landing aborted - all pads are occupied"

// TryLandOrRetry attempts landing; if no free pad, returns retry needed and appropriate message code [04 §10.2].
// No free pad among tried keeps order alive for next-tick retry or 30+rand(15) delayed retry [04 §10.2].
func (s *System) TryLandOrRetry(w *units.World, vtolHandle pool.Handle) (landed bool, needRetry bool, msg string) {
	vtol := w.Unit(vtolHandle)
	if vtol == nil {
		return false, false, LandingFailedMessage
	}
	pad := s.FindFreePad(w, vtol)
	if pad == nil {
		// Distinguish "no pad at all" vs "all occupied" via IsLandingPad existence
		anyPad := false
		for _, u := range w.IterSliced() {
			if IsLandingPad(u) {
				anyPad = true
				break
			}
		}
		if anyPad {
			return false, true, LandingAbortedMessage // [04 §10.2]
		}
		return false, true, LandingFailedMessage
	}
	ok := s.Land(w, vtolHandle, pad)
	if !ok {
		return false, true, LandingFailedMessage
	}
	return true, false, ""
}

// TakeOff runs the shared air takeoff preamble on a handle [04 R-AIR-01 §6]:
// detach from any carrier, raise the activation edge, and — only from the
// grounded mode 1 — set mode 2 and install the initial climb marker at the
// unit's own X/Z with altitude offset `cruisealt / 2`. It is the surface a
// caller outside the order boundary uses; the air executors themselves reach the
// same routine through the order activation boundary [04 R-AIR-02].
func (s *System) TakeOff(w *units.World, vtolHandle pool.Handle) bool {
	if w == nil {
		return false
	}
	vtol := w.Unit(vtolHandle)
	if vtol == nil || vtol.Def == nil || !vtol.Def.CanFly {
		return false
	}
	head := airHeadFor(vtol)
	s.takeoffPreamble(vtol, head)
	// Publish the command words from the freshly installed marker at once, so a
	// caller outside the mover tick sees the climb goal without waiting a tick
	// [04 R-AIR-01 §1].
	s.StepFlightCommand(vtol, head, s.AirSectors)
	return true
}

// SubmitAirMove publishes a direct air route for VTOL without lattice [04 §10.1] can-fly bypass.
// For air movers we bypass ordinary ground footprint checks [04 §10.2] supported inference.
// This publishes a 2-point route (current cell+bias to goal cell+bias) and sets cruise altitude target.
// If terrain nil, still publishes.
func (s *System) SubmitAirMove(vtolHandle pool.Handle, targetX, targetZ numeric.Fixed, w *units.World) {
	if s == nil || vtolHandle == 0 {
		return
	}
	vtol := w.Unit(vtolHandle)
	if vtol == nil {
		return
	}
	// Route points are signed integer world coordinates for every follower;
	// aircraft bypass the lattice but do not switch the point domain [04
	// R-MOV-01 §3].
	pts := []Point{
		{X: int32(vtol.X.Raw() >> 16), Z: int32(vtol.Z.Raw() >> 16)},
		{X: int32(targetX.Raw() >> 16), Z: int32(targetZ.Raw() >> 16)},
	}
	route := s.Routes[vtolHandle]
	if route == nil {
		route = &Route{}
		s.Routes[vtolHandle] = route
	}
	route.Publish(pts)
	// The command block is the only input the flight integrator has, so a direct
	// air move installs a point marker with the full `cruisealt` offset rather
	// than writing the mover's command words behind the producer's back
	// [04 R-AIR-01 §1][04 R-AIR-01 §4].
	m := s.newPointMarker(vtol, Vec3{X: targetX, Y: vtol.Y, Z: targetZ})
	if vtol.Def != nil {
		m.setAltitudeOffset(int16(vtol.Def.CruiseAlt))
	}
	head := airHeadFor(vtol)
	s.installAirGoal(vtol, head, m)
	// Airborne through the one mover-mode setter, so the ground plane the mover
	// held while grounded is released [04 R-AIR-01 §3][04 R-COLL-01 §4]. This
	// direct surface has no order record to hold a climb marker, so it commands
	// the goal altitude straight away.
	s.SetMoverMode(vtol, 2)
	if fl, ok := s.Flights[vtolHandle]; ok {
		fl.Mode = 2
	}
	s.StepFlightCommand(vtol, head, s.AirSectors)
}

// ValidateAirMoveSite validates air arrival without terrain blocking (air bypass) but still checks pad for landing [04 §10.2].
func (s *System) ValidateAirMoveSite(targetX, targetZ numeric.Fixed) bool {
	// Air bypass: always pass if within map bounds (if terrain exists)
	if s.Terrain != nil {
		cx := world.WorldToCell(targetX)
		cz := world.WorldToCell(targetZ)
		if cx < 0 || cz < 0 || cx >= s.Terrain.CellW || cz >= s.Terrain.CellH {
			return false
		}
	}
	return true
}
