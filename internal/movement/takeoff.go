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

// usesTakeoffPreamble reports whether an order descriptor name is one of the air
// executors established to run the shared takeoff preamble. `VTOL_Move`,
// `VTOL_Patrol` and `VTOL_MobileBuild` are named by [04 R-ORD-02 §2];
// `VTOL_Landing` and `VTOL_LandIfCan` run it in their own phase 0
// [04 R-AIR-01 §6]. Nothing else in the table is established to run it, and an
// unlisted name must not be added on resemblance.
func usesTakeoffPreamble(name string) bool {
	switch name {
	case "VTOL_Move", "VTOL_Patrol", "VTOL_MobileBuild", "VTOL_Landing", "VTOL_LandIfCan":
		return true
	}
	return false
}

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
		// levels the lean accumulator of [04 R-AIR-01 §2]. That accumulator is
		// not represented on FlightState, so the level-out has nothing to write
		// here; it belongs to whoever wires [04 R-AIR-01 §2], not to this call.
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
//  4. only if the committed mode is 1 (grounded): set mode 2 and install a
//     point marker on the unit's own X/Y/Z whose altitude offset is
//     `cruisealt / 2` (signed, truncating toward zero);
//  5. advance the phase.
//
// Step 4's marker is an initial *climb* goal only: its explicit altitude offset
// makes its arrival test `|unitY − goalY| < 0x10001` on top of a horizontal test
// the marker satisfies the instant it is built, so the record's 0xE0 gate holds
// until the aircraft has climbed [04 R-AIR-01 §4][04 R-AIR-02]. When the unit is
// already airborne no marker is built and the phase still advances, so a mid-air
// order does not reset the climb goal [04 R-AIR-01 §6].
func (s *System) takeoffPreamble(u *units.Unit) {
	if s == nil || u == nil || u.Def == nil || !u.Def.CanFly {
		return
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
		return
	}
	climbY := CruiseAltitudeForCarrier(s.Terrain, u.X, u.Z, u, true)
	s.SetMoverMode(u, 2)
	if s.takeoffClimb == nil {
		s.takeoffClimb = make(map[pool.Handle]int32)
	}
	s.takeoffClimb[u.Handle] = int32(climbY.Raw())
	if fl := s.Flights[u.Handle]; fl != nil {
		fl.TargetX = int32(u.X.Raw())
		fl.TargetZ = int32(u.Z.Raw())
		fl.TargetY = int32(climbY.Raw())
	}
}

// activateTakeoff runs the preamble for an order head whose executor is
// established to carry it. It is the phase-0 half of that executor: the
// activation boundary visits a fresh head exactly once, and step 4 is itself
// guarded on the committed mode, so a repeat visit is inert.
func (s *System) activateTakeoff(u *units.Unit, head *orders.Node) {
	if s == nil || u == nil || head == nil || u.Def == nil || !u.Def.CanFly {
		return
	}
	if !usesTakeoffPreamble(orders.DescriptorFor(head.ID).Name) {
		return
	}
	s.takeoffPreamble(u)
}

// stepTakeoffClimb flies the initial climb leg installed by the preamble. It
// returns done=true when no climb is outstanding — either none was installed or
// the marker's altitude window has been entered — in which case the caller
// proceeds with the ordinary air step. While the climb is outstanding the
// aircraft's commanded X and Z are its own position, so the §10.1 integrator
// produces vertical motion only, and the caller must not test arrival: the
// record's gate is still waiting on this marker [04 R-AIR-01 §4][04 R-AIR-02].
func (s *System) stepTakeoffClimb(u *units.Unit) (StepResult, bool) {
	if s == nil || u == nil || s.takeoffClimb == nil {
		return StepResult{}, true
	}
	target, pending := s.takeoffClimb[u.Handle]
	if !pending {
		return StepResult{}, true
	}
	fl := s.Flights[u.Handle]
	if fl == nil {
		delete(s.takeoffClimb, u.Handle)
		return StepResult{}, true
	}
	dy := int64(u.Y.Raw()) - int64(target)
	if dy < 0 {
		dy = -dy
	}
	if dy < climbArrivalWindow {
		delete(s.takeoffClimb, u.Handle)
		return StepResult{}, true
	}
	fl.X = int32(u.X.Raw())
	fl.Y = int32(u.Y.Raw())
	fl.Z = int32(u.Z.Raw())
	fl.TargetX = fl.X
	fl.TargetZ = fl.Z
	fl.TargetY = target
	fl.TargetHeading = u.Move.Heading
	if fl.MaxVelocity == 0 && u.Def != nil && u.Def.MaxVelocity != 0 {
		fl.MaxVelocity = int32(u.Def.MaxVelocity)
	}
	if fl.Acceleration == 0 && u.Def != nil && u.Def.Acceleration != 0 {
		fl.Acceleration = int32(u.Def.Acceleration)
	}
	if fl.BrakeRate == 0 && u.Def != nil && u.Def.BrakeRate != 0 {
		fl.BrakeRate = int32(u.Def.BrakeRate)
	}
	if u.Def != nil {
		fl.TurnRate = int32(u.Def.TurnRate)
	}
	oldY := int64(u.Y)
	IntegrateFlight(fl) // [04 §10.1] C26–C30
	u.X = numeric.Fixed(int64(fl.X))
	u.Y = numeric.Fixed(int64(fl.Y))
	u.Z = numeric.Fixed(int64(fl.Z))
	u.Move.Heading = fl.Heading
	u.Move.Speed = numeric.Fixed(int64(fl.Speed))
	s.emitMovementCallbacks(u, fl.Speed) // [04 §5.2] tiers apply to the flight path too
	return StepResult{
		Handle:     u.Handle,
		DistToGoal: s.distToGoal(u),
		HasRoute:   false,
		EmptyRoute: true,
		Moved:      int64(u.Y) != oldY,
	}, false
}

// ClimbTargetFor exposes the outstanding initial-climb altitude for a handle,
// for tests and diagnostics. ok is false once the marker's altitude window has
// been entered and the record's gate has been released.
func (s *System) ClimbTargetFor(h pool.Handle) (numeric.Fixed, bool) {
	if s == nil || s.takeoffClimb == nil {
		return 0, false
	}
	y, ok := s.takeoffClimb[h]
	return numeric.Fixed(int64(y)), ok
}
