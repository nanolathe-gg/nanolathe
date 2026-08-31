// Package movement — the mover-mode setter and the shared air takeoff preamble
// [04 R-AIR-01 §3][04 R-AIR-01 §6][04 R-AIR-02].
//
// The mover mode is the occupancy *plane*, not a moving/stopped flag: mode 1 is
// grounded, mode 2 airborne, mode 0 attached/parked [04 R-AIR-01 §3]. Only the
// air executors and the save-restore path change it, and only through the one
// setter reproduced here.
package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// climbArrivalWindow is the air marker's altitude arrival test when an explicit
// altitude offset is present (marker flag 0x08): |unitY − goalY| < 0x10001
// [04 R-AIR-01 §4].
const climbArrivalWindow = 0x10001

// SetMoverMode is retail's committed-mover-mode setter [04 R-AIR-01 §3].
//
// It does nothing when the current low two bits already equal the request.
// Otherwise, for a requested mode 1 (grounded) it zeroes the three velocity
// components and the scalar speed and clears the unit state byte's activation
// bit — raising `Deactivate` and notification 4 on the falling edge; for any
// other requested mode it sets that bit, raising `Activate` and notification 3.
// Then it writes the request's low two bits into the committed mode.
//
// The 16-bit turn residual is deliberately NOT zeroed here: only the
// integrator's own inactive-mode branch zeroes it [04 R-AIR-01 §3].
//
// The occupancy consequence is [04 R-COLL-01 §4]: the ground word is written by
// mode-1 movers and by the building class, the air word by mode-2 movers, and
// modes 0 and 3 stamp nothing. Nanolathe models only the ground plane — nothing
// reads an air word — so leaving mode 1 releases the mover's ground cells and
// entering mode 1 re-stamps them. That release is the event [04 R-FAC-02 §6]
// names for an aircraft product clearing its factory's exit, and the same cells
// are what the yard-close admission gate of [04 R-FAC-02 §5] tests.
func (s *System) SetMoverMode(u *units.Unit, mode uint8) bool {
	if s == nil || u == nil {
		return false
	}
	mode &= 0x3
	prev := u.Move.Mode & 0x3
	if prev == mode {
		return false // nothing when the low two bits already equal the request
	}
	if mode == 1 {
		if fl := s.Flights[u.Handle]; fl != nil {
			fl.VX, fl.VY, fl.VZ = 0, 0, 0
			fl.Speed = 0
		}
		u.Move.Speed = 0
		// The setter also runs the bank/pitch routine with a zero delta, which
		// decays the lean accumulator of [04 R-AIR-01 §2] by its 0xF333 factor
		// exactly once and recomputes bank and pitch from the decayed
		// accumulator. They are levelled, not snapped to zero.
		s.levelFlightLean(u)
		u.SetActivationEdge(false)
	} else {
		u.SetActivationEdge(true)
	}
	u.Move.Mode = mode
	if fl := s.Flights[u.Handle]; fl != nil {
		fl.Mode = mode
	}
	s.applyOccupancyPlane(u, prev, mode)
	return true
}

// applyOccupancyPlane moves a mover between Nanolathe's single (ground)
// occupancy plane and no plane at all, per the mode rule of [04 R-COLL-01 §4].
// A building-class unit is selected by its class, not by its mode, and is never
// touched here.
func (s *System) applyOccupancyPlane(u *units.Unit, prev, mode uint8) {
	if s == nil || s.Grid == nil || u == nil {
		return
	}
	coll := s.Collisions[u.Handle]
	if coll == nil {
		return
	}
	switch {
	case prev == 1 && mode != 1:
		if s.Grid.Clear(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, coll.ID) {
			s.noteOccupancyCommit(u.Handle, s.tick)
		}
	case prev != 1 && mode == 1:
		if s.Grid.Stamp(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, coll.ID) {
			s.noteOccupancyCommit(u.Handle, s.tick)
		}
	}
	coll.Mode = mode
	coll.CachedMode = mode
}

// takeoffPreamble is the five-step routine every air executor that must get the
// unit off the ground runs [04 R-AIR-01 §6], in this order:
//
//  1. release the manual-target latch on all three weapon slots — owned by the
//     weapon layer, not by this package, and therefore not performed here;
//  2. if the unit has a carrier, detach it requesting mover mode 2;
//  3. set the state byte's activation bit, raising `Activate` — the engine's
//     takeoff script hook;
//  4. only if the committed mode is 1 (grounded): set mode 2, build a point
//     marker on the unit's own X/Y/Z whose altitude offset is `cruisealt / 2`
//     (signed, truncating toward zero), install it as the record's goal payload
//     and OR 0xE0 into the record's gate;
//  5. advance the phase.
//
// It reports whether step 4 built a marker. When the unit is already airborne no
// marker is built and the phase still advances, so a mid-air order does not
// reset the aircraft's climb goal [04 R-AIR-01 §6].
//
// Step 4's marker is an initial *climb* goal only: its explicit altitude offset
// makes its arrival test `|unitY − goalY| < 0x10001` on top of a horizontal test
// the marker satisfies the instant it is built, so the record's 0xE0 gate holds
// until the aircraft has climbed [04 R-AIR-01 §4][04 R-AIR-02].
func (s *System) takeoffPreamble(u *units.Unit, rec *orders.Node) bool {
	if s == nil || u == nil || u.Def == nil || !u.Def.CanFly {
		return false
	}
	// Step 2 — the self-detach requests mode 2, so the mode write below is the
	// one that runs for a unit that was carried [04 R-AIR-01 §3][04 R-AIR-01 §6].
	if u.Attachment.Carrier != 0 {
		DetachCargo(s.world, u.Handle)
	}
	// Step 3 — the takeoff script hook. Step 4's setter raises the same edge for
	// any non-grounded mode, so this is a no-op edge whenever step 4 runs
	// [04 R-AIR-01 §3].
	u.SetActivationEdge(true)
	// Step 4 — grounded only.
	if u.Move.Mode&0x3 != 1 {
		return false
	}
	s.SetMoverMode(u, 2)
	m := s.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
	m.setAltitudeOffset(halfCruiseAlt(u.Def.CruiseAlt))
	s.installAirGoal(u, rec, m)
	if rec != nil {
		rec.DynamicGate |= airLegGate
	}
	return true
}

// halfCruiseAlt is the preamble's `cruisealt / 2`: a signed 16-bit halving with
// C division, truncating toward zero [04 R-AIR-01 §6][I3].
func halfCruiseAlt(cruiseAlt int32) int16 {
	v := int16(cruiseAlt)
	if v >= 0 {
		return v / 2
	}
	return -((-v) / 2)
}

// ClimbTargetFor reports the commanded altitude of an outstanding initial-climb
// marker, for tests and diagnostics. ok is false once no marker with an explicit
// altitude offset is installed [04 R-AIR-01 §6].
func (s *System) ClimbTargetFor(h pool.Handle) (numeric.Fixed, bool) {
	if s == nil {
		return 0, false
	}
	fl := s.Flights[h]
	if fl == nil || fl.Command == nil {
		return 0, false
	}
	m, ok := fl.Command.Payload.(*airMarker)
	if !ok || m == nil || m.flags&airMarkerExplicitAlt == 0 {
		return 0, false
	}
	return m.goal.Y, true
}
