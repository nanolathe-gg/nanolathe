// Package movement — the air path marker and the air executors that install it
// [04 R-AIR-01 §4][04 R-AIR-01 §6][04 R-AIR-01 §7][04 §10.2][04 R-ORD-02 §2].
//
// [04 R-AIR-01 §1] establishes that there is exactly one command supply shared
// by every air order, and that the orders differ only in which goal payload
// they install. This file holds the payload — the 0x36-byte path marker of
// [04 R-AIR-01 §4] — and the executor legs that build one. The producer that
// consumes it is flightcommand.go; the integrator that consumes the producer's
// output is flight.go. Neither reads an order record.
package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Air path marker flag bits, exactly the table of [04 R-AIR-01 §4].
const (
	airMarkerFollow         uint16 = 0x01 // follow the target unit
	airMarkerRadial         uint16 = 0x02 // offset the goal radially about the target's heading
	airMarkerHeadingMatch   uint16 = 0x04 // arrival additionally requires equal headings
	airMarkerExplicitAlt    uint16 = 0x08 // an explicit altitude offset is present
	airMarkerExplicitRadius uint16 = 0x10 // an explicit horizontal arrival radius is present
	airMarkerTerrainAlt     uint16 = 0x20 // the goal Y is terrain-derived
	airMarkerExplicitHead   uint16 = 0x40 // an explicit heading is present
	airMarkerFreeze         uint16 = 0x80 // skip the follow branch of the goal update
)

// The satisfied bits the payload installer clears on every non-null install,
// and the bit it raises on the record whose payload it replaces
// [04 R-AIR-01 §4].
const (
	airGoalInstallClearMask uint32 = 0x20 | 0x40 | 0x80 | 0x100 | 0x200
)

// airLegGate is the dynamic gate every air executor leg arms when it installs
// a marker: the five movement-service satisfied bits the record re-dispatches
// on [04 R-AIR-01 §6][R-UNIT-06 §3].
const airLegGate uint32 = 0xE0

// airGoalCeiling is the 0x1FF0000 clamp the marker's altitude setter and its
// goal update both apply — about 511 world units. The per-tick producer rule of
// [04 R-AIR-01 §1] step 4 does NOT apply it, and does not go through here.
const airGoalCeiling = 0x1FF0000

// defaultFollowRadial is the radial offset a follow marker takes when the
// target is `canfly` and the follower's first weapon slot has zero `Range`:
// 0x640000, one hundred world units [04 R-AIR-01 §4].
const defaultFollowRadial numeric.Fixed = 0x640000

// airMarker is the air half of the goal-payload class family [04 R-AIR-01 §4]:
// a flags word, a horizontal arrival radius word, a signed altitude offset, a
// heading, an attach piece index, the owning unit, a weak target handle, a
// 16.16 goal triple and a 16.16 radial offset distance.
//
// The record's byte layout is retail's identity, not ours [I13]; the fields
// below are the same logical fields under Go names.
type airMarker struct {
	sys         *System
	flags       uint16
	radius      uint16
	altOffset   int16
	heading     uint16
	attachPiece uint16
	unit        *units.Unit
	target      pool.Handle
	goal        Vec3
	radial      numeric.Fixed
}

// newPointMarker is the **point** constructor: flags 0x20, goal a supplied
// triple [04 R-AIR-01 §4].
func (s *System) newPointMarker(u *units.Unit, goal Vec3) *airMarker {
	return &airMarker{sys: s, flags: airMarkerTerrainAlt, unit: u, goal: goal}
}

// newFrozenTerrainPointMarker is the **frozen terrain point** constructor:
// flags 0xA3, goal a supplied triple [04 R-AIR-01 §4].
func (s *System) newFrozenTerrainPointMarker(u *units.Unit, goal Vec3) *airMarker {
	return &airMarker{
		sys:   s,
		flags: airMarkerFollow | airMarkerRadial | airMarkerTerrainAlt | airMarkerFreeze,
		unit:  u,
		goal:  goal,
	}
}

// newFollowUnitMarker is the **follow-unit** constructor: flags 0x01, or 0x07
// with the radial offset set to the unit's first weapon slot's `Range` in 16.16
// — or 0x640000 when that `Range` is zero — whenever the **target** is `canfly`
// [04 R-AIR-01 §4].
func (s *System) newFollowUnitMarker(u *units.Unit, target pool.Handle) *airMarker {
	m := &airMarker{sys: s, flags: airMarkerFollow, unit: u, target: target}
	if t := s.unitFor(target); t != nil && t.Def != nil && t.Def.CanFly {
		m.flags = airMarkerFollow | airMarkerRadial | airMarkerHeadingMatch
		m.radial = numeric.Fixed(int64(firstWeaponRange(u)) << 16)
		if m.radial == 0 {
			m.radial = defaultFollowRadial
		}
	}
	return m
}

// newFollowPieceMarker is the **follow-unit-piece** constructor: flags 0x05,
// with the piece index [04 R-AIR-01 §4].
func (s *System) newFollowPieceMarker(u *units.Unit, target pool.Handle, piece uint16) *airMarker {
	return &airMarker{
		sys:         s,
		flags:       airMarkerFollow | airMarkerHeadingMatch,
		unit:        u,
		target:      target,
		attachPiece: piece,
	}
}

// setAltitudeOffset is the terrain-derived altitude setter [04 R-AIR-01 §4]:
// it always sets flag 0x08 and stores the signed word, and when flag 0x20 is
// already set it also computes the goal Y immediately as
// (max(seaLevelByte, terrainHeight(goalXZ)) + offset) << 16, clamped to
// 0x1FF0000. CruiseAltitudeForOffset is that expression [04 §10.1].
func (m *airMarker) setAltitudeOffset(offset int16) {
	if m == nil {
		return
	}
	m.flags |= airMarkerExplicitAlt
	m.altOffset = offset
	if m.flags&airMarkerTerrainAlt != 0 {
		m.goal.Y = CruiseAltitudeForOffset(m.terrain(), m.goal.X, m.goal.Z, int32(offset))
	}
}

// setArrivalRadius sets flag 0x10 and stores the word [04 R-AIR-01 §4]. The
// radius is a plain 16-bit word each executor leg writes for the leg it is
// starting, not a lookup into a family of enumerated values.
func (m *airMarker) setArrivalRadius(r uint16) {
	if m == nil {
		return
	}
	m.flags |= airMarkerExplicitRadius
	m.radius = r
}

// setHeading stores an explicit heading and raises flag 0x40 [04 R-AIR-01 §4].
func (m *airMarker) setHeading(h uint16) {
	if m == nil {
		return
	}
	m.flags |= airMarkerExplicitHead
	m.heading = h
}

// UpdateGoal is the marker's goal update [04 R-AIR-01 §4].
//
// With flag 0x01 clear **or** flag 0x80 set, and only when flag 0x08 is clear,
// the marker rewrites its goal Y by the same sector-height rule the per-tick
// producer uses ([04 R-AIR-01 §1] step 4). Otherwise, with a live follow: a
// dead target or a target whose sector link is the out-of-map sentinel makes
// the update decline, leaving the command position at last tick's value; else
// the goal becomes the target's attach-piece world position, plus the radial
// offset when flag 0x02 is set, and then the altitude offset shifted into
// 16.16. The result is clamped to 0x1FF0000 in both branches.
func (m *airMarker) UpdateGoal(u *units.Unit, dst *Vec3) {
	if m == nil || dst == nil {
		return
	}
	if m.flags&airMarkerFollow == 0 || m.flags&airMarkerFreeze != 0 {
		if m.flags&airMarkerExplicitAlt == 0 {
			if h, linked := m.sys.airSectorHeight(u); linked && u != nil && u.Def != nil {
				m.goal.Y = clampGoalCeiling(numeric.Fixed((int64(u.Def.CruiseAlt) + int64(h)) << 16))
			}
		}
		*dst = m.goal
		return
	}
	t := m.sys.unitFor(m.target)
	if t == nil || !t.Alive {
		return // decline: the command position keeps last tick's value
	}
	if _, linked := m.sys.airSectorHeight(t); !linked {
		return // the target's sector link is the out-of-map sentinel
	}
	goal := m.targetAttachPosition(t)
	if m.flags&airMarkerRadial != 0 {
		heading := t.Move.Heading
		if m.flags&airMarkerExplicitHead != 0 {
			heading = t.Move.Heading + m.heading
		}
		ox, oz := offsetAtBearing(heading, m.radial)
		goal.X += ox
		goal.Z += oz
	}
	goal.Y += numeric.Fixed(int64(m.altOffset) << 16)
	goal.Y = clampGoalCeiling(goal.Y)
	m.goal = goal
	*dst = m.goal
}

// Arrived is the marker's arrival test [04 R-AIR-01 §4].
//
// In the explicit-radius case (flag 0x10) the test is strict and horizontal
// only: hypot(unitX − goalX, unitZ − goalZ) / 65536 < (int16)arrivalRadius,
// evaluated in double precision on the raw fixed-point differences — the
// AirArrival helper of altitude.go, which is where this package's I2 float
// boundary lives. Otherwise
// the default test is hypot(...) / 65536 <= 0.5, and then, in order: flag 0x01
// additionally requires a live target; flag 0x04 additionally requires the
// unit's heading to equal the target's exactly; flag 0x08 additionally requires
// |unitY − goalY| < 0x10001.
func (m *airMarker) Arrived(u *units.Unit) bool {
	if m == nil || u == nil {
		return false
	}
	if m.flags&airMarkerExplicitRadius != 0 {
		r := int32(int16(m.radius))
		if r <= 0 {
			return false // a strict "< 0" is never true, and "< 0" never arrives
		}
		return AirArrival(u.X, u.Z, m.goal.X, m.goal.Z, r, 0, 0, false)
	}
	if !AirArrival(u.X, u.Z, m.goal.X, m.goal.Z, 0, 0, 0, false) {
		return false
	}
	var t *units.Unit
	if m.flags&airMarkerFollow != 0 {
		t = m.sys.unitFor(m.target)
		if t == nil || !t.Alive {
			return false
		}
	}
	if m.flags&airMarkerHeadingMatch != 0 {
		if t == nil || u.Move.Heading != t.Move.Heading {
			return false
		}
	}
	if m.flags&airMarkerExplicitAlt != 0 && !AltitudesEqual(u.Y, m.goal.Y) {
		return false
	}
	return true
}

// SupplyHeading is the marker's heading-supply method [04 R-AIR-01 §4], whose
// four cases are tested in this order: with neither flag 0x04 nor flag 0x80
// set, or with no live target, it writes the marker's own heading and returns
// true if flag 0x40 is set, and otherwise reports no suggestion; with a live
// target, flag 0x02 writes bearing(unitPos, targetPos), else flag 0x40 writes
// the marker's own heading, else it writes the target's heading.
//
// The no-suggestion arm leaves dst untouched, because [04 R-AIR-01 §1] step 5
// says that inside 16 world units with no suggestion "the command heading is
// left completely unchanged" — a write here would contradict that.
func (m *airMarker) SupplyHeading(u *units.Unit, dst *uint16) bool {
	if m == nil || dst == nil {
		return false
	}
	t := m.sys.unitFor(m.target)
	if t != nil && !t.Alive {
		t = nil
	}
	if (m.flags&(airMarkerHeadingMatch|airMarkerFreeze) == 0) || t == nil {
		if m.flags&airMarkerExplicitHead != 0 {
			*dst = m.heading
			return true
		}
		return false
	}
	switch {
	case m.flags&airMarkerRadial != 0:
		*dst = bearing(u.X, u.Z, t.X, t.Z)
	case m.flags&airMarkerExplicitHead != 0:
		*dst = m.heading
	default:
		*dst = t.Move.Heading
	}
	return true
}

// Persistent returns true exactly when flag 0x01 is set and the target is live,
// so a follow marker survives arrival and a point marker is released on it
// [04 R-AIR-01 §4].
func (m *airMarker) Persistent() bool {
	if m == nil || m.flags&airMarkerFollow == 0 {
		return false
	}
	t := m.sys.unitFor(m.target)
	return t != nil && t.Alive
}

// Release drops the marker's own references [04 R-AIR-01 §1] step 6.
func (m *airMarker) Release() {
	if m == nil {
		return
	}
	m.unit = nil
	m.target = 0
}

// targetAttachPosition is the goal a follow marker takes from its target.
//
// TODO(question): [04 R-AIR-01 §4] says the goal becomes "the target's
// attach-piece world position (the exit-piece locator transform of [R-REV-02],
// including the unit-origin addition)", but this package has no compiled piece
// transform to evaluate. Placeholder: the target's own origin, which is that
// transform's unit-origin term with a zero piece offset. What would settle it is
// the model piece world-transform surface reaching internal/movement.
func (m *airMarker) targetAttachPosition(t *units.Unit) Vec3 {
	return Vec3{X: t.X, Y: t.Y, Z: t.Z}
}

func (m *airMarker) terrain() *world.Terrain {
	if m == nil || m.sys == nil {
		return nil
	}
	return m.sys.Terrain
}

// clampGoalCeiling applies the marker's 0x1FF0000 ceiling [04 R-AIR-01 §4].
func clampGoalCeiling(y numeric.Fixed) numeric.Fixed {
	if int64(y) > airGoalCeiling {
		return numeric.Fixed(airGoalCeiling)
	}
	return y
}

// offsetAtBearing is the sine/cosine pair every air executor's "offset at a
// bearing and radius" leg builds, in this codebase's heading convention:
// heading 0 is +Z and a vector at heading t is (r·sin t, r·cos t) [04 §5.1].
func offsetAtBearing(heading uint16, radius numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	sin := int64(numeric.Sin(numeric.Angle(heading)))
	cos := int64(numeric.Cos(numeric.Angle(heading)))
	return numeric.Fixed((int64(radius) * sin) >> 13), numeric.Fixed((int64(radius) * cos) >> 13)
}

// firstWeaponRange is the unit's first weapon slot's `Range` in whole world
// units, the quantity several air legs use as a radius [04 R-AIR-01 §4]
// [04 R-AIR-01 §7].
func firstWeaponRange(u *units.Unit) int32 {
	if u == nil {
		return 0
	}
	slot := u.SlotAt(0)
	if slot == nil || slot.Weapon == nil {
		return 0
	}
	return slot.Weapon.Range
}

func (s *System) unitFor(h pool.Handle) *units.Unit {
	if s == nil || s.world == nil || h == 0 {
		return nil
	}
	return s.world.Unit(h)
}

// airSectorHeight is the sector-grid read every cruise-altitude rule performs,
// and the sentinel test six air executors run [04 R-AIR-01 §5]. A false second
// result is the out-of-bounds sector record.
func (s *System) airSectorHeight(u *units.Unit) (uint8, bool) {
	if s == nil || u == nil {
		return 0, false
	}
	return s.AirSectors.SectorHeightAt(u.X, u.Z)
}

// installAirGoal installs a payload on the unit's flight command block
// [04 R-AIR-01 §4]: the installer raises the goal-replaced bit 0x80 on the
// record whose payload it replaces, then clears the record's satisfied bits
// 0x20, 0x40, 0x80, 0x100 and 0x200 for the record it installs on. Both are
// the same record here, so the clear is what survives — which is exactly why
// [R-UNIT-06 §3] says "every rebind starts with a clean satisfied word".
func (s *System) installAirGoal(u *units.Unit, rec *orders.Node, m *airMarker) {
	if s == nil || u == nil || m == nil {
		return
	}
	c := s.FlightCommandFor(u.Handle, u)
	if c == nil {
		return
	}
	if c.Payload != nil {
		if rec != nil {
			rec.Satisfied |= airGoalReleasedBit
		}
		c.Payload.Release()
	}
	if rec != nil {
		rec.Satisfied &^= airGoalInstallClearMask
	}
	c.Payload = m
}

// releaseAirGoal clears the installed payload without installing another. It is
// the "release the payload" step several air executors run before spawning a
// child record [04 R-AIR-01 §7].
func (s *System) releaseAirGoal(u *units.Unit) {
	if s == nil || u == nil {
		return
	}
	fl := s.Flights[u.Handle]
	if fl == nil || fl.Command == nil || fl.Command.Payload == nil {
		return
	}
	fl.Command.Payload.Release()
	fl.Command.Payload = nil
}

// --- the air executors ---

// airOrderState is the movement-side dispatch state for one air order record.
//
// Retail keeps the phase byte, the gate word and the scratch words on the order
// record itself and re-dispatches the executor from the order pump. Nanolathe's
// pump has no air handlers — a handler-less descriptor stalls its record — so
// the air executors run from the mover tick instead, and this is where their
// per-record state lives. The record still receives the gate and satisfied-bit
// writes the contracts specify, so the pump completes an air move exactly as it
// completes a ground one.
type airOrderState struct {
	order   *orders.Node
	phase   uint8
	waiting bool   // a marker with gate 0xE0 is outstanding
	arrived bool   // the producer reported arrival last tick
	bearing uint16 // the search/loiter bearing scratch word
	low     uint8  // the low bit of the drawn bearing, the second scratch word
	goal    Vec3   // the record's cached goal
	post    Vec3   // VTOL_Standby's recorded post
	done    bool   // the executor reported completion
}

// airStateFor returns the executor state for the unit's current head record,
// resetting it when the head changes.
func (s *System) airStateFor(u *units.Unit, head *orders.Node) *airOrderState {
	if s.airOrders == nil {
		s.airOrders = make(map[pool.Handle]*airOrderState)
	}
	st := s.airOrders[u.Handle]
	if st == nil || st.order != head {
		st = &airOrderState{order: head}
		s.airOrders[u.Handle] = st
	}
	return st
}

// simRNG is the simulation stream the air executors draw from. It is the
// stream the unit's own order queue was bound with, so a session that seeds one
// stream per battle draws in one deterministic order [I4].
func (s *System) simRNG(u *units.Unit) *rng.Simulation {
	q := orders.QueueForUnit(u)
	if q == nil {
		return nil
	}
	b := q.Binding()
	if b == nil {
		return nil
	}
	return b.SimRNG
}

// runAirExecutor dispatches the head record's air executor. It runs before the
// per-tick command producer, which is the position [04 R-AIR-01 §1] gives the
// order layer's work relative to the mover tick.
func (s *System) runAirExecutor(u *units.Unit, head *orders.Node, st *airOrderState) {
	if head == nil || st.done {
		return
	}
	// A leg that armed gate 0xE0 holds the record until its marker reports
	// arrival; that is the gate, not a poll [04 R-AIR-01 §6].
	if st.waiting {
		if !st.arrived {
			return
		}
		st.waiting = false
		st.arrived = false
	}
	switch orders.DescriptorFor(head.ID).Name {
	case "VTOL_Move":
		s.execVTOLMove(u, head, st)
	case "VTOL_LandIfCan":
		s.execVTOLLandIfCan(u, head, st)
	case "VTOL_Standby":
		s.execVTOLStandby(u, head, st)
	default:
		// Every other head leaves the command block alone. With a null payload
		// the producer does nothing at all and the aircraft continues on its
		// last command [04 R-AIR-01 §1].
		//
		// TODO(T25): `Stop` spawns `VTOL_LandIfCan` (target none, goal = own
		// position, p1..p3 = 0) at the head for an airborne `canfly` unit
		// [04 R-ORD-01 §2], and `VTOL_Standby` phase 2 spawns the same record
		// for an idle unloaded aircraft [04 R-AIR-01 §7]. Both spawns are order
		// -record insertions, which belong to internal/orders — a package this
		// unit does not own, and one where `Stop` currently has no handler at
		// all. Placeholder: the executors below are reachable only from a
		// record another layer pushes.
	}
}

// execVTOLMove is `VTOL_Move` [04 R-ORD-02 §2], the executor a factory-built
// aircraft also reaches, because `Park` re-identifies itself as `VTOL_Move`
// with a restart for a `canfly` product [04 R-FAC-02 §4][04 R-AIR-02].
//
// Phase 0: a live mover and `canfly`; the takeoff preamble; advance. Phase 1:
// snap the record's goal X and Z onto the unit's own footprint and build a
// point marker there with no altitude or radius setter — arrival is the default
// hypot <= 0.5 world units at the terrain-derived Y; install; gate 0xE0;
// advance. Phase 2: the arrival completes the order; the pump's move handler
// sees the producer's 0x20 and unlinks the record.
func (s *System) execVTOLMove(u *units.Unit, head *orders.Node, st *airOrderState) {
	switch st.phase {
	case 0:
		if s.Flights[u.Handle] == nil || u.Def == nil || !u.Def.CanFly {
			st.done = true
			return
		}
		st.waiting = s.takeoffPreamble(u, head)
		st.phase = 1
	case 1:
		goalX, goalZ := head.GoalX, head.GoalZ
		if gx, gz, ok := s.moveGoalFor(u.Handle, head); ok {
			goalX, goalZ = gx, gz
		}
		fx, fz := s.pathFootprint(u)
		goalX, goalZ = snapToOwnFootprint(goalX, goalZ, fx, fz)
		m := s.newPointMarker(u, Vec3{X: goalX, Y: head.GoalY, Z: goalZ})
		s.installAirGoal(u, head, m)
		head.DynamicGate = airLegGate
		st.waiting = true
		st.phase = 2
	default:
		// Arrival is the record's business: the producer has already raised the
		// satisfied bit, and the move handler completes on it.
		st.done = true
	}
}

// execVTOLLandIfCan is `VTOL_LandIfCan` [04 R-AIR-01 §6], the three-phase
// machine an idle aircraft with nowhere to park runs. It lands on terrain, not
// on a pad.
func (s *System) execVTOLLandIfCan(u *units.Unit, head *orders.Node, st *airOrderState) {
	// Entry: a satisfied goal-release bit 0x40 completes; the off-map recovery
	// leg of [04 R-AIR-01 §5] pre-empts everything else.
	if head.Satisfied&0x40 != 0 {
		st.done = true
		return
	}
	if s.airOffMapRecovery(u, head, st) {
		return
	}
	sim := s.simRNG(u)
	switch st.phase {
	case 0:
		if s.Flights[u.Handle] == nil || u.Def == nil || !u.Def.CanFly {
			st.done = true
			return
		}
		if head.GoalX == 0 && head.GoalY == 0 && head.GoalZ == 0 {
			head.GoalX, head.GoalY, head.GoalZ = u.X, u.Y, u.Z
		}
		st.goal = Vec3{X: head.GoalX, Y: head.GoalY, Z: head.GoalZ}
		if sim == nil {
			return // no stream, no draw: the machine holds rather than inventing one
		}
		draw := uint16(sim.Uint32n(0x10000))
		st.bearing = draw
		st.low = uint8(draw & 1)
		st.waiting = s.takeoffPreamble(u, head)
		st.phase = 1
	case 1:
		if s.landable(u, u.X, u.Z) {
			// TODO(question): [04 R-AIR-01 §6] gives phase 1's altitude offset as
			// "0 when the terrain height there is at or below sea level and
			// terrainHeight - seaLevel otherwise", and glosses both branches as
			// placing "the marker's commanded Y at exactly the terrain height,
			// because the marker's terrain-derived altitude rule adds the offset
			// to max(seaLevel, terrainHeight)". The gloss does not follow from
			// [04 R-AIR-01 §4]'s Established setter expression: with terrain above
			// sea level that expression yields terrain + (terrain - seaLevel), and
			// with terrain at or below it, seaLevel. Only a setter whose base were
			// seaLevel alone would satisfy the gloss for the second branch. The
			// offsets below are the ones §6 states, run through §4's expression,
			// so an aircraft settles a little above the surface on high ground.
			// What would settle it is a re-trace of the setter's base term against
			// this executor's leg.
			offset := int16(0)
			if h, sea, ok := s.terrainAndSea(u.X, u.Z); ok && h > sea {
				offset = int16(h - sea)
			}
			m := s.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
			m.setAltitudeOffset(offset)
			s.installAirGoal(u, head, m)
			head.DynamicGate = airLegGate
			// The landing script hook: clearing the state byte's activation bit
			// raises `Deactivate` and notification 4 [04 R-AIR-01 §6].
			u.SetActivationEdge(false)
			st.waiting = true
			st.phase = 2
			return
		}
		if sim == nil {
			return
		}
		// The search: twelve iterations, two draws each in the order X then Z,
		// each candidate snapped to the unit's footprint half-cell anchor
		// [04 R-AIR-01 §6].
		for k := int64(0); k < 12; k++ {
			span := uint32(0x81 + 0x20*k)
			half := numeric.Fixed((0x40 + 0x10*k) << 16)
			dx := numeric.Fixed(int64(sim.Uint32n(span)) << 16)
			dz := numeric.Fixed(int64(sim.Uint32n(span)) << 16)
			fx, fz := s.pathFootprint(u)
			cx, cz := snapToOwnFootprint(u.X+dx-half, u.Z+dz-half, fx, fz)
			if !s.landable(u, cx, cz) {
				continue
			}
			m := s.newPointMarker(u, Vec3{X: cx, Y: u.Y, Z: cz})
			s.installAirGoal(u, head, m)
			head.DynamicGate = airLegGate
			st.waiting = true
			return
		}
		// All twelve failed. When the arrival bits are set, step the search
		// bearing by −0x5555 — about −120 degrees — and take a marker at the
		// cached goal offset by it at radius 0xA0, arrival radius 0x40.
		if head.Satisfied&airLegGate == airLegGate {
			st.bearing -= 0x5555
		}
		ox, oz := offsetAtBearing(st.bearing, numeric.Fixed(0xA0<<16))
		m := s.newPointMarker(u, Vec3{X: st.goal.X + ox, Y: st.goal.Y, Z: st.goal.Z + oz})
		m.setArrivalRadius(0x40)
		s.installAirGoal(u, head, m)
		head.DynamicGate |= airLegGate
		st.waiting = true
	default:
		// Phase 2 completes the landing: the mode setter zeroes the velocity and
		// the scalar speed and levels bank and pitch [04 R-AIR-01 §3], and the
		// mode write re-stamps the ground plane [04 R-COLL-01 §4].
		s.SetMoverMode(u, 1)
		st.done = true
	}
}

// execVTOLStandby is `VTOL_Standby` [04 R-AIR-01 §7], the decision between
// parking and circling.
func (s *System) execVTOLStandby(u *units.Unit, head *orders.Node, st *airOrderState) {
	sim := s.simRNG(u)
	switch st.phase {
	case 0:
		if s.Flights[u.Handle] == nil || u.Def == nil || !u.Def.CanFly {
			st.done = true
			return
		}
		// Releasing the manual-target latch on all three weapon slots is the
		// weapon layer's, not this package's; the record's own writes follow.
		head.DynamicGate |= 0x10000
		head.Deadline = int32(s.tick + 1)
		st.post = Vec3{X: u.X, Y: u.Y, Z: u.Z}
		st.phase = 1
	case 1:
		// TODO(question): phase 1 "asks the ordinary autonomous acquisition for
		// a target and, if one is found and accepted, clears the gate word,
		// resets the phase to zero and returns 3" [04 R-AIR-01 §7]. Autonomous
		// acquisition is the combat layer's and is not reachable from this
		// package. Placeholder: the no-target arm, which advances.
		st.phase = 2
	case 2:
		if u.Def == nil || !u.Def.CanFly || u.Move.Mode&0x3 != 2 {
			head.DynamicGate |= 0x10000
			if sim != nil {
				head.Deadline = int32(s.tick + 30 + sim.Uint32n(30))
			}
			st.phase = 1
			return
		}
		if len(u.Attachment.Cargo) > 0 {
			if sim == nil {
				return
			}
			// Three simulation draws per visit, in the order bearing, radius,
			// delay. This is the whole of retail's aircraft "circling": a fresh
			// uniformly random bearing and an 8-to-39 world-unit radius about a
			// fixed post, redrawn every 30 to 44 ticks [04 R-AIR-01 §7].
			b := uint16(sim.Uint32n(0x10000))
			radius := numeric.Fixed(int64(8+sim.Uint32n(0x20)) << 16)
			delay := sim.Uint32n(15)
			ox, oz := offsetAtBearing(b, radius)
			m := s.newPointMarker(u, Vec3{X: st.post.X + ox, Y: st.post.Y, Z: st.post.Z + oz})
			if u.Def != nil {
				m.setAltitudeOffset(int16(u.Def.CruiseAlt))
			}
			s.installAirGoal(u, head, m)
			head.Deadline = int32(s.tick + 30 + delay)
			st.phase = 1
			return
		}
		// TODO(T25): the no-cargo arm allocates a `VTOL_LandIfCan` record
		// carrying this record's cached goal and pushes it on the unit
		// [04 R-AIR-01 §7]. Record insertion belongs to internal/orders, which
		// this unit does not own. Placeholder: hold, so an unloaded idle
		// aircraft keeps its last command instead of landing.
		st.done = true
	default:
		st.done = true
	}
}

// airOffMapRecovery is the shared off-map recovery leg six air executors run
// before their phase switch, returning from it immediately [04 R-AIR-01 §5]:
// a point marker at unitPos + offset, where offset is the negated sine/cosine
// pair of bearing(unitPos, mapCentre) at radius 0x3200000 (800 world units),
// with horizontal arrival radius 0x80 and gate 0xE0.
func (s *System) airOffMapRecovery(u *units.Unit, head *orders.Node, st *airOrderState) bool {
	if _, linked := s.airSectorHeight(u); linked || s.Terrain == nil {
		return false
	}
	centreX := numeric.Fixed(int64(s.Terrain.CellW*16/2) << 16)
	centreZ := numeric.Fixed(int64(s.Terrain.CellH*16/2) << 16)
	b := bearing(u.X, u.Z, centreX, centreZ)
	ox, oz := offsetAtBearing(b, numeric.Fixed(0x3200000))
	m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
	m.setArrivalRadius(0x80)
	s.installAirGoal(u, head, m)
	head.DynamicGate |= airLegGate
	st.waiting = true
	return true
}

// landable is the landing-legality test `VTOL_LandIfCan` phase 1 asks about a
// candidate position.
//
// TODO(question): [04 R-AIR-01 §6] names the test — "ask the landing-legality
// test whether the unit's current position is landable" — but gives neither its
// predicate nor its citation, and no section in this unit's reading list
// defines it. Placeholder: the predicate the mover's own occupancy commit
// applies at that anchor — every footprint cell in bounds, passable for this
// unit's movement profile, and unoccupied by another unit
// [04 R-COLL-01 §4][04 §8.2]. What would settle it is a trace of that test's
// body, which §6 calls but does not describe.
func (s *System) landable(u *units.Unit, x, z numeric.Fixed) bool {
	if s == nil || u == nil {
		return false
	}
	coll := s.Collisions[u.Handle]
	if coll == nil || s.Terrain == nil {
		return false
	}
	fx, fz := coll.FootPrintX, coll.FootPrintZ
	anchorX, anchorZ := world.PlacementAnchor(x, z, int32(fx), int32(fz))
	anchor := Cell{X: anchorX, Z: anchorZ}
	if !commitRectInBounds(s.Terrain, anchor, fx, fz) {
		return false
	}
	profile := s.ProfileFor(u.Handle)
	for dz := int16(0); dz < fz; dz++ {
		for dx := int16(0); dx < fx; dx++ {
			c := Cell{X: anchor.X + int32(dx), Z: anchor.Z + int32(dz)}
			if !profile.IsPassableCommitCell(s.Terrain, c.X, c.Z) {
				return false
			}
			if s.Grid != nil {
				if occ, ok := s.Grid.OccupantAt(c); ok && occ != coll.ID {
					return false
				}
			}
		}
	}
	return true
}

// terrainAndSea returns the terrain height byte at a position and the map's sea
// level byte, for the landing marker's altitude offset [04 R-AIR-01 §6].
func (s *System) terrainAndSea(x, z numeric.Fixed) (h, sea int32, ok bool) {
	if s == nil || s.Terrain == nil {
		return 0, 0, false
	}
	sample := s.Terrain.HeightAt(x, z)
	if sample == numeric.Fixed(-1) {
		return 0, int32(s.Terrain.SeaLevel), true
	}
	return int32(int64(sample) >> 16), int32(s.Terrain.SeaLevel), true
}

// snapToOwnFootprint re-centres a goal on the cell the unit would occupy there,
// which is the snap/reverse pair `VTOL_Move` phase 1 applies to the record's
// goal X and Z [04 R-ORD-02 §2][04 R-ORD-01 §1].
func snapToOwnFootprint(x, z numeric.Fixed, fx, fz int32) (numeric.Fixed, numeric.Fixed) {
	cellX := goalCellForWorld(x, fx)
	cellZ := goalCellForWorld(z, fz)
	return world.PlacementCenter(cellX, cellZ, fx, fz)
}

// stepAir is the aircraft's whole mover tick [04 R-AIR-01 §1]: the air
// executor's leg for the head record, then the controller's per-tick hook (the
// command producer and the integrator's single input fetch), then the flight
// integrator, then the position commit and the movement-rate cache.
//
// The ground route follower is not on this path. It never was retail's — the
// flight integrator reads only the flight command block — and it was the source
// of the reported sideways and backwards flight, because it supplies no command
// heading at all.
func (s *System) stepAir(u *units.Unit, tick uint32) StepResult {
	handle := u.Handle
	fl := s.Flights[handle]
	if fl == nil {
		d := s.distToGoal(u)
		s.emitMovementCallbacks(u, 0)
		return StepResult{Handle: handle, DistToGoal: d, EmptyRoute: true}
	}

	head := airHeadFor(u)
	st := s.airStateFor(u, head)
	s.runAirExecutor(u, head, st)

	// Call 1 — the controller's per-tick hook: the six-step producer and the
	// integrator's single input fetch [04 R-AIR-01 §1].
	s.StepFlightCommand(u, head, s.AirSectors)

	// The executor's gate is released by the payload's own arrival test, which
	// the producer has just run. Observing it here rather than through the
	// record's satisfied word keeps the leg sequence intact even though this
	// engine's order pump has no air handler to re-dispatch [04 R-AIR-01 §6].
	if c := fl.Command; c != nil {
		// Recomputed, never latched: a leg that installed a fresh marker this
		// tick is answered about that marker, and a stale arrival from the
		// payload a previous record left behind cannot release the next gate.
		st.arrived = c.Payload == nil || c.Payload.Arrived(u)
	} else {
		st.arrived = false
	}

	// Call 2 — the flight integrator. Its arithmetic is [04 §10.1] and is not
	// touched here; only its inputs are.
	fl.Mode = u.Move.Mode & 0x3
	fl.X = int32(u.X.Raw())
	fl.Y = int32(u.Y.Raw())
	fl.Z = int32(u.Z.Raw())
	fl.Heading = u.Move.Heading
	if u.Def != nil {
		if fl.MaxVelocity == 0 && u.Def.MaxVelocity != 0 {
			fl.MaxVelocity = int32(u.Def.MaxVelocity)
		}
		if fl.Acceleration == 0 && u.Def.Acceleration != 0 {
			fl.Acceleration = int32(u.Def.Acceleration)
		}
		if fl.BrakeRate == 0 && u.Def.BrakeRate != 0 {
			fl.BrakeRate = int32(u.Def.BrakeRate)
		}
		fl.TurnRate = int32(u.Def.TurnRate)
	}
	oldX, oldZ := int64(u.X), int64(u.Z)
	IntegrateFlight(fl) // [04 §10.1] C26–C30

	// Call 3 — the commit. An airborne mover holds no ground cells, so there is
	// no occupancy stamp here; the mode setter moved the stamp when the aircraft
	// left the ground and puts it back when it lands [04 R-COLL-01 §4].
	u.X = numeric.Fixed(int64(fl.X))
	u.Y = numeric.Fixed(int64(fl.Y))
	u.Z = numeric.Fixed(int64(fl.Z))
	u.Move.Heading = fl.Heading
	u.Move.Speed = numeric.Fixed(int64(fl.Speed))
	if coll := s.Collisions[handle]; coll != nil {
		coll.X = int32(fl.X)
		coll.Y = int32(fl.Y)
		coll.Z = int32(fl.Z)
		coll.Heading = fl.Heading
		coll.Speed = fl.Speed
		anchor := coll.ProposedAnchor(u.Move.Mode & 0x3)
		coll.CachedAnchor = anchor
		coll.OldAnchor = anchor
	}

	// Call 4 — the movement-rate cache [04 §5.2].
	s.emitMovementCallbacks(u, fl.Speed)

	// An aircraft consumes no published route — the command block is its only
	// input — but a route left bound to it is still reported, because the ground
	// static layer must not invalidate one [04 R-MOV-01 §3].
	//
	// Arrival is the payload's own test and nothing else. The ground arrival
	// handle's cell-proximity predicate is not consulted here: an air arrival is
	// the marker's horizontal hypot, with the leg's own radius or the default
	// half world unit [04 R-AIR-01 §4], and running both would give an air move
	// two disagreeing completion rules [I11].
	route := s.Routes[handle]
	hasRoute := route != nil && route.Active
	return StepResult{
		Handle:     handle,
		DistToGoal: s.distToGoal(u),
		HasRoute:   hasRoute,
		EmptyRoute: !hasRoute,
		Moved:      int64(u.X) != oldX || int64(u.Z) != oldZ,
		Arrived:    st.arrived,
	}
}

// airHeadFor is the record the air executor dispatches on: the unit's active
// primary head, or nil when the queue is empty. A nil head leaves the command
// block alone, which is [04 R-AIR-01 §1]'s null-payload case.
func airHeadFor(u *units.Unit) *orders.Node {
	q := orders.QueueForUnit(u)
	if q == nil {
		return nil
	}
	if q.LenPrimary() > 0 {
		return q.Primary()[0]
	}
	return q.Head()
}
