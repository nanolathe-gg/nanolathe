// The mover-mode setter and the shared air takeoff preamble
// [04 R-AIR-01 §3][04 R-AIR-01 §6][04 R-AIR-02].
//
// The mover mode is the occupancy *plane*, not a moving/stopped flag: mode 1 is
// grounded, mode 2 airborne, mode 0 attached/parked [04 R-AIR-01 §3]. Only the
// air executors and the save-restore path change it, and only through the one
// setter reproduced here.

package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

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
// modes 0 and 3 stamp nothing. Leaving mode 1 therefore *moves* the mover's
// stamp from the ground word to the air word rather than dropping it, and
// landing moves it back. Releasing the ground cells is the event
// [04 R-FAC-02 §6] names for an aircraft product clearing its factory's exit,
// and the same cells are what the yard-close admission gate of
// [04 R-FAC-02 §5] tests; the air word the mover takes instead is what the
// projectile contact test of [06 R-DMG-01 §7] reads to hit an aircraft.
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
		u.Move.VelX, u.Move.VelY, u.Move.VelZ = 0, 0, 0
		// The setter also runs the bank/pitch routine with a zero delta, which
		// decays the lean accumulator of [04 R-AIR-01 §2] by its 0xF333 factor
		// exactly once and recomputes bank and pitch from the decayed
		// accumulator. They are levelled, not snapped to zero.
		s.levelFlightLean(u)
		if fl := s.Flights[u.Handle]; fl != nil {
			// Deactivate is raised immediately below. Its callback can observe
			// mover save words before another integrator visit, so publish the
			// zero triple and this call's decayed lean first. Do not call the
			// full flight commit here: its transform may be from the prior tick.
			if coll := s.Collisions[u.Handle]; coll != nil {
				coll.VX, coll.VY, coll.VZ, coll.Speed = 0, 0, 0, 0
				coll.LeanX, coll.LeanY, coll.LeanZ = fl.LeanX, fl.LeanY, fl.LeanZ
				coll.TurnResidual = fl.TurnResidual
			}
		}
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

// applyOccupancyPlane moves a mover between the two occupancy planes on a mode
// change, per the mode rule of [04 R-COLL-01 §4]: mode 1 the ground word, mode 2
// the air word, modes 0 and 3 neither. A building-class unit is selected by its
// class, not by its mode, and is never touched here.
func (s *System) applyOccupancyPlane(u *units.Unit, prev, mode uint8) {
	if s == nil || s.Grid == nil || u == nil {
		return
	}
	coll := s.Collisions[u.Handle]
	if coll == nil {
		return
	}
	coll.Mode = mode
	coll.CachedMode = mode
	s.syncMoverStamp(u)
}

// syncMoverStamp reconciles a mover's occupancy with its committed cached pair
// and mode. The identity holds exactly one plane's cells — ground for mode 1,
// air for mode 2, none for modes 0 and 3 — at exactly the cached pair, because
// "every writer stamps at the unit's cached pair" [04 R-COLL-01 §4]. When the
// pair or the plane has changed it clears the rectangle that was actually
// stamped and stamps the new one, which is the clear-then-stamp order of the
// commit's success branch [04 R-COLL-01 §1].
//
// The occupant-age clock ([04 §6.1 R-DOC04-B]) is the mover's last-stamp tick,
// and the stamp writes it UNCONDITIONALLY as its first action, guarded only on
// the mover existing: it precedes the bounds test, so even a stamp that writes
// no cell because the rectangle is off map advances it, and the plane the stamp
// writes is not part of the condition [04 R-COLL-01 §4 "stamp, in order"]
// [04 R-PATH-01 §14 "writers of the mover's commit tick"].
//
// Correction (WU-19-123): this used to write the clock only when the GROUND
// plane was touched, on the reasoning that the occupant-age gate reads the
// ground word alone and an airborne restamp has nothing to age. The reasoning
// picks the wrong side of the contract — the clock is the mover's, not the
// cell's — and the gap it left is observable: an aircraft that restamps in the
// air keeps a clock frozen at its takeoff, so the first classification after it
// touches down can read cells whose occupant the watermark has already passed.
func (s *System) syncMoverStamp(u *units.Unit) {
	if s == nil || u == nil {
		return
	}
	coll := s.Collisions[u.Handle]
	if coll == nil || coll.Building {
		return
	}
	if s.Grid == nil {
		s.syncStampedAirSector(u, coll)
		return
	}
	plane, stamps := planeForMode(u.Move.Mode)
	stamped := false
	clearedGround := false
	clearedAnchor := coll.StampedAnchor
	if coll.HasStamp && (!stamps || coll.StampedPlane != plane || coll.StampedAnchor != coll.CachedAnchor) {
		if s.Grid.ClearPlane(coll.StampedPlane, coll.StampedAnchor, coll.FootPrintX, coll.FootPrintZ, coll.ID) {
			clearedGround = clearedGround || coll.StampedPlane == PlaneGround
		}
		coll.HasStamp = false
	}
	if stamps && !coll.HasStamp {
		s.Grid.StampPlane(plane, coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, coll.ID)
		stamped = true
		coll.StampedAnchor = coll.CachedAnchor
		coll.StampedPlane = plane
		coll.HasStamp = s.Grid.RectOnMap(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ)
	}
	if clearedGround {
		// The clear's class-layer maintenance [04 R-COLL-01 §4], on the
		// rectangle that was actually released. This is the takeoff, the
		// transport pickup and the carried-position setter's move: each of them
		// hands the ground plane back, and a layer that had baked the unit in
		// through the occupant-age gate would otherwise keep the wall
		// [04 R-PATH-01 §14]. Only a ground release is reclassified, because
		// only the ground word is read by the gate [04 R-COLL-01 §2]. The clock
		// write below is NOT conditioned the same way — see the doc comment.
		s.noteFootprintClear(u.Handle, clearedAnchor, coll.FootPrintX, coll.FootPrintZ, true)
	}
	if stamped {
		s.noteOccupancyCommit(u.Handle, s.tick)
	}
	s.syncStampedAirSector(u, coll)
}

// takeoffPreamble is the five-step routine every air executor that must get the
// unit off the ground runs [04 R-AIR-01 §6], in this order:
//
//  1. release the manual-target latch on all three weapon slots through the
//     owning queue's weapon adapter;
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
	// Step 1 — each air takeoff releases all three manual-target latches in
	// numeric slot order. The queue binding owns the combat state; a missing
	// adapter is an unbound fixture and leaves that state untouched.
	if q := orders.QueueOfUnit(u); q != nil && q.Binding() != nil && q.Binding().Weapons != nil && q.Binding().Weapons.ReleaseSlot != nil {
		for idx := 0; idx < units.NumSlots; idx++ {
			q.Binding().Weapons.ReleaseSlot(u, idx)
		}
	}
	// Step 2 — the self-detach requests mode 2 directly: the detach's apply
	// step writes the request's low two bits straight into the committed
	// mover-mode pair [04 R-AIR-01 §3][04 R-AIR-01 §9], so a unit that was
	// carried is airborne the instant this runs. Step 4's mode-1 gate below
	// never fires for this caller — the committed mode is 2, not 1 — which is
	// why no initial climb marker is built for a unit taking off from its
	// carrier [04 R-AIR-01 §6].
	if u.Attachment.Carrier != 0 {
		DetachTakeoff(s.world, u.Handle)
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
