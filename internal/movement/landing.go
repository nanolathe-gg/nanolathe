// Package movement — landing pads and air repair [04 §10.2].
//
// Landing pads: QueryLandingPad is synchronous four-output query on target script; candidates tried order 0..3 and first piece
// that is not carried and not already assigned to another unit (any unit whose attach-piece field equals candidate) wins [04 §10.2].
// With no pad the loiter/spiral heading step is used; no free pad keeps order alive for next-tick retry or 30+rand(15) delayed retry [04 §10.2].
// Air repair (pad heals) when VTOL on pad.
//
// IsAirBase via definition bit isairbase [02 "Unit record"][04 §10.2].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// IsLandingPad reports whether u is a landing pad via IsAirBase [02 "Unit record"][04 §10.2].
// TODO(question): full pad detection via QueryLandingPad script query [04 §10.2]; IsAirBase is stub.
func IsLandingPad(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.IsAirBase // [02 "Unit record"] [04 §10.2]
}

// FindFreePad finds a free landing pad for seeker [04 §10.2].
// Tries candidates in order 0..3 conceptually, but in world terms scans all IsAirBase units in deterministic order [I1]
// and returns first that is not carried and not already assigned to another unit (any unit whose attach-piece equals candidate) [04 §10.2].
// For simplified world where each pad is a unit (one piece), we treat pad unit handle as candidate index and check if any other unit's AttachPiece equals that handle's slot? Since AttachPiece is per-cargo piece index on carrier, not pad assignment.
// For pads, assignment is any unit whose attach-piece field equals candidate piece index? Spec: "any unit whose attach-piece field equals the candidate" [04 §10.2].
// In our model, attach-piece field is cargo.Attachment.AttachPiece; pad not carrier, so checking attach piece equality not applicable.
// Instead we approximate free pad as not carried and not already hosting a landed VTOL (we track landed VTOL via PadOccupant).
// For minimal vertical slice, free means pad not carried and pad's own cargo list empty? Actually pad may be ground building, not carrier, so no cargo.
// Simpler: free means pad not already assigned as landing target for another VTOL this tick (we don't track reservations).
// For headless test, any IsAirBase unit not carried wins.
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
			continue // carried pad not available [04 §10.2] "not carried"
		}
		// Check not already assigned to another unit: scan all units for attach-piece field equals candidate index
		// In retail, candidate is piece index 0..3 on target script; our pad is one unit = one candidate.
		// We approximate assignment as any other air unit already landed on this pad (distance < radius)?
		// For test, if any other unit's current position within pad footprint and not seeker, treat as occupied.
		occupied := false
		for _, other := range w.IterSliced() {
			if other == nil || other == seeker || other == u {
				continue
			}
			if other.Attachment.Carrier != 0 {
				continue // carried not count
			}
			if other.Def != nil && other.Def.CanFly {
				// Check if other is landed on this pad (heuristic: close distance < 32 wu)
				dx := int64(other.X) - int64(u.X)
				dz := int64(other.Z) - int64(u.Z)
				// Use 32 world units ~ two cells
				if dx*dx+dz*dz < int64(32*65536)*int64(32*65536) {
					// If other is stationary (speed 0) maybe landed?
					// Assume any nearby VTOL occupies pad.
					occupied = true
					break
				}
			}
		}
		if occupied {
			continue
		}
		return u
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
	vtol.Move.Mode = 1 // parked [04 §9.1] 1 stopped/parked
	if fl, ok := s.Flights[vtolHandle]; ok {
		fl.X = int32(vtol.X.Raw())
		fl.Z = int32(vtol.Z.Raw())
		fl.Y = int32(vtol.Y.Raw())
		fl.VX = 0
		fl.VY = 0
		fl.VZ = 0
		fl.Speed = 0
		fl.Mode = 1
	}
	if st, ok := s.Steers[vtolHandle]; ok {
		st.X = int32(vtol.X.Raw())
		st.Z = int32(vtol.Z.Raw())
	}
	if coll, ok := s.Collisions[vtolHandle]; ok {
		coll.X = int32(vtol.X.Raw())
		coll.Z = int32(vtol.Z.Raw())
		coll.Y = int32(vtol.Y.Raw())
	}
	// Occupancy stamp landed footprint
	if s.Grid != nil {
		if coll, ok := s.Collisions[vtolHandle]; ok {
			anchor := coll.ProposedAnchor(1)
			s.Grid.Stamp(anchor, coll.FootPrintX, coll.FootPrintZ, coll.ID)
		}
	}
	return true
}

// AirRepair heals VTOL on pad [04 §10.2] VTOL_GetRepaired.
// Called each tick while VTOL is landed on IsAirBase pad; heals at heal rate.
// healRate default 5 per tick if Healtime? Use Def.HealTime? But for aircraft, heal maybe fixed.
// We use maxHealth/100 per tick approach? Simplify: + healAmount per tick capped at MaxHealth.
func AirRepair(w *units.World, vtolHandle pool.Handle, pad *units.Unit, amount int32) bool {
	if w == nil || pad == nil {
		return false
	}
	vtol := w.Unit(vtolHandle)
	if vtol == nil || pad == nil {
		return false
	}
	if !IsLandingPad(pad) {
		return false
	}
	// Check landed proximity
	dx := int64(vtol.X) - int64(pad.X)
	dz := int64(vtol.Z) - int64(pad.Z)
	if dx*dx+dz*dz > int64(32*65536)*int64(32*65536) {
		return false
	}
	if vtol.Health >= vtol.MaxHealth {
		return false
	}
	vtol.Health += amount
	if vtol.Health > vtol.MaxHealth {
		vtol.Health = vtol.MaxHealth
	}
	return true
}

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

// TakeOff transitions VTOL from landed (mode 1) to active (mode 2) and sets initial cruise altitude [04 §10.1][04 §10.2].
// Requires live mover and canfly; mode 1 -> 2 forced in load phase 0 [04 §10.2].
// For standalone gunship/bomber, use this to become airborne.
func (s *System) TakeOff(w *units.World, vtolHandle pool.Handle) bool {
	if w == nil {
		return false
	}
	vtol := w.Unit(vtolHandle)
	if vtol == nil || vtol.Def == nil || !vtol.Def.CanFly {
		return false
	}
	vtol.Move.Mode = 2 // active locomotion [04 §9.1]
	if fl, ok := s.Flights[vtolHandle]; ok {
		fl.Mode = 2
		// Initial climb target altitude cruisealt/2 at current XZ [04 §10.2] phase 0
		targetY := CruiseAltitudeForCarrier(s.Terrain, vtol.X, vtol.Z, vtol, true)
		fl.TargetY = int32(targetY.Raw())
		fl.TargetX = int32(vtol.X.Raw())
		fl.TargetZ = int32(vtol.Z.Raw())
	}
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
	// Convert to cells for route
	startCell := Cell{X: world.WorldToCell(vtol.X), Z: world.WorldToCell(vtol.Z)}
	goalCell := Cell{X: world.WorldToCell(targetX), Z: world.WorldToCell(targetZ)}
	prof := s.ProfileFor(vtolHandle)
	bx := int32(prof.FootPrintX / 2)
	bz := int32(prof.FootPrintZ / 2)
	// Direct 2-point air route (no occupancy block)
	pts := []Point{{X: startCell.X + bx, Z: startCell.Z + bz}, {X: goalCell.X + bx, Z: goalCell.Z + bz}}
	route := s.Routes[vtolHandle]
	if route == nil {
		route = &Route{}
		s.Routes[vtolHandle] = route
	}
	route.Publish(pts)
	// Set flight target altitude to full cruisealt at goal [04 §10.1]
	if fl, ok := s.Flights[vtolHandle]; ok {
		targetY := CruiseAltitudeForOffset(s.Terrain, targetX, targetZ, vtol.Def.CruiseAlt)
		fl.TargetY = int32(targetY.Raw())
		fl.TargetX = int32(targetX.Raw())
		fl.TargetZ = int32(targetZ.Raw())
	}
	// Ensure mode active
	if vtol.Move.Mode != 2 {
		vtol.Move.Mode = 2
	}
	if fl, ok := s.Flights[vtolHandle]; ok {
		fl.Mode = 2
	}
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
