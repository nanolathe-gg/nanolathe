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
	if !s.installOffMapRecoveryMarker(u, head) {
		return false
	}
	st.waiting = true
	return true
}

// installOffMapRecoveryMarker is that same leg without the stepAir-driven
// executor's own bookkeeping, so the pump-driven executors below can run it as
// their first act too [04 R-AIR-01 §5][04 R-AIR-01 §8] step 4.
func (s *System) installOffMapRecoveryMarker(u *units.Unit, head *orders.Node) bool {
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
	// The pump-driven air executors below reach the order pump through this
	// binding; rebinding is a pointer write, so doing it here costs nothing and
	// needs no state on System (integrate.go owns that type).
	s.BindAirOrderLegs()

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

// ---------------------------------------------------------------------------
// The pump-driven air executors [04 R-AIR-01 §7][04 R-AIR-01 §8][04 R-ORD-02 §3]
// ---------------------------------------------------------------------------
//
// `VTOL_Move`, `VTOL_LandIfCan` and `VTOL_Standby` above run from the mover
// tick because this engine's pump had no handler for them. The seven executors
// below are the opposite arrangement and the faithful one: internal/orders has
// a handler for each, that handler runs the record-side entry sequence, and it
// then calls into this package for the leg. The pump therefore stays the sole
// dispatcher — it clears the record's dynamic gate before every dispatch and
// applies the leg's own result code afterwards [04 §3.3] — while the legs stay
// where the air marker family of [04 R-AIR-01 §4] lives.
//
// A leg reads and writes the record directly: the phase byte is the pump's
// (code 1 advances it, code 0 resets it), the dynamic gate and the deadline are
// the leg's own, and the arrival bits it waits on are the ones the flight
// command producer raises on that record [04 R-AIR-01 §1] step 6.

// BindAirOrderLegs binds this system's air executor legs to the order pump.
func (s *System) BindAirOrderLegs() {
	if s == nil {
		return
	}
	orders.SetAirLegRunner(s.runAirOrderLeg)
}

// runAirOrderLeg is the orders.AirLegRunner this system registers. It declines
// a record whose unit is not one of ours, so two systems sharing the binding
// cannot drive each other's aircraft.
func (s *System) runAirOrderLeg(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
	if s == nil || u == nil || n == nil || s.world == nil || s.world.Unit(u.Handle) != u {
		return 0, false
	}
	switch orders.DescriptorFor(n.ID).Name {
	case "VTOL_Evade":
		return s.legVTOLEvade(u, n, tick), true
	case "VTOL_SeekAttack":
		return s.legVTOLSeekAttack(u, n, satisfied, tick), true
	case "VTOL_SeekGuard":
		return s.legVTOLSeekGuard(u, n, satisfied, tick), true
	case "AirStrike":
		return s.legAirStrike(u, n, satisfied, tick), true
	case "AirToGround":
		return s.legAirToGround(u, n, tick), true
	case "AirToGroundHover":
		return s.legAirToGroundHover(u, n, tick), true
	case "AirToAir":
		return s.legAirToAir(u, n, satisfied, tick), true
	}
	return 0, false
}

// --- shared leg vocabulary ---

// The gates the attack-run legs arm, named for the leg that arms them. Their
// bits are [04 R-ORD-01 §0]'s: `0x2` cancel-current, `0x8` interrupt/abandon,
// `0x10` guard re-arm, `0xE0` the three movement outcomes, `0x1000` one of the
// attack family's engage/disengage pair, `0x10000` the slot-clear/interrupt bit
// the combat pre-checks also treat as a reason to end the order.
const (
	airLegGateStrike     uint32 = 0x100E8 // 0x10000 | 0xE0 | 0x8
	airLegGateReposition uint32 = 0xE2    // 0xE0 | 0x2
	airLegGateStrafe     uint32 = 0x100EA // airLegGateStrike | 0x2
	airLegGateHoverMiss  uint32 = 0x110E8 // airLegGateStrike | 0x1000
	airLegGateOrbit      uint32 = 0xF8    // 0xE0 | 0x10 | 0x8
)

// airOrbitRadiusBonus is the 0xA0 world units the search and guard orbits add
// to the slot-0 weapon `Range` for their orbit radius [04 R-AIR-01 §7]
// [04 R-ORD-02 §3]; airOverflyBonus is the 0x3C0 `AirStrike` phase 5 adds to
// `attackrunlength` for the overfly distance [04 R-AIR-01 §8].
const (
	airOrbitRadiusBonus = 0xA0
	airOverflyBonus     = 0x3C0
)

// airMoverReady is the "a live mover and `canfly`" clause every air phase 0
// opens with [04 R-AIR-01 §7][04 R-ORD-02 §2][04 R-ORD-02 §3].
func (s *System) airMoverReady(u *units.Unit) bool {
	return s != nil && u != nil && u.Def != nil && u.Def.CanFly && s.Flights[u.Handle] != nil
}

// airDeadline is the deadline setter of [04 R-ORD-01 §1]: it stores
// `current tick + n` and ORs bit 0 into the dynamic gate.
func airDeadline(n *orders.Node, tick uint32, delay uint32) {
	n.Deadline = int32(tick + delay)
	n.DynamicGate |= 1
}

// airPlanarDistance is the `hypot(a − b)` the attack-run legs measure in 16.16
// [04 R-AIR-01 §8].
//
// Retail forms it on the double-precision stack and truncates toward zero.
// I2 has no row for a float temporary in this file, and none is needed: over an
// exact integer radicand `floor(sqrt(x))` and `trunc(hypot)` are the same value,
// so the integer square root of `dx² + dz²` reproduces it without leaving the
// fixed-point world. The squared sum of two raw 16.16 map coordinates is at most
// about 6e17 and cannot overflow int64.
func airPlanarDistance(ax, az, bx, bz numeric.Fixed) int64 {
	dx := int64(ax) - int64(bx)
	dz := int64(az) - int64(bz)
	return int64(isqrt(uint64(dx*dx + dz*dz)))
}

// airBelowThreeQuarters is the health test five air legs share: health strictly
// below `(MaxDamage >> 2) · 3`, computed unsigned [04 R-AIR-01 §7]
// [04 R-AIR-01 §8][04 R-ORD-02 §3].
func airBelowThreeQuarters(u *units.Unit) bool {
	if u == nil {
		return false
	}
	max := int32(0)
	if u.Def != nil {
		max = u.Def.MaxDamage
	}
	if max <= 0 {
		max = u.MaxHealth
	}
	if max <= 0 {
		return false
	}
	health := u.Health
	if health < 0 {
		health = 0
	}
	return uint32(health) < uint32(max>>2)*3
}

// airBaseCandidates is the "collect the base candidates within `0xF00` for my
// side" scan that `VTOL_SeekAttack` phase 1, `VTOL_SeekGuard` phase 1,
// `AirStrike` phase 6 and `AirToGround` phase 4 all run when the aircraft is
// below three quarters health, and whose non-empty result pushes a
// `VTOL_Landing` order at a randomly drawn candidate.
//
// TODO(question): the scan's admission predicate is not established. Four
// sections name the list — [04 R-AIR-01 §7], [04 R-AIR-01 §8] and
// [04 R-ORD-02 §3] all say "the base candidates within `0xF00`" or "the
// nearby-unit candidate list within `0xF00` for the unit's ally group" — but
// [04 R-ORD-02 §4], which is the section that enumerates the scan visitors,
// defines only the repair-candidate filter and the guard-candidate visitor and
// does not define this one. Whether a candidate is any allied unit, any unit
// with the `isairbase` capability, or any unit with a free pad is therefore
// unstated, and so is what "for my side" means against the diplomacy byte.
// Choosing one would decide where damaged aircraft go to land, so nothing is
// chosen: the list is reported empty, and every caller falls through to its own
// "no candidates" arm, which each of the four sections states. A trace of that
// visitor's admission test settles it.
func (s *System) airBaseCandidates(_ *units.Unit) []pool.Handle { return nil }

// airSpawnAtHead inserts a freshly allocated record at the front of the unit's
// primary segment, which is the head insert of [04 R-ORD-01 §1].
func airSpawnAtHead(u *units.Unit, name string, target pool.Handle, goal Vec3, tick uint32) {
	q := orders.QueueForUnit(u)
	if q == nil {
		return
	}
	id := orders.Lookup(name)
	if id == 0 {
		return
	}
	q.PushHead(id, orders.NewNodeForOrder(id, target, goal.X, goal.Y, goal.Z, tick, u.Handle, false))
}

// --- VTOL_Evade [04 R-AIR-01 §8] ---

// legVTOLEvade is the random 90-degree break:
//
//	Phase 0 requires a live mover and `canfly`, draws `random below 2` into a
//	record scratch word, and forms `h = unitHeading + (draw == 0 ? 0x4000 :
//	0xC000)` — a random 90-degree break left or right — then builds a point
//	marker at `unitPos − offset(h, Range)` with horizontal arrival radius `0x80`
//	and gate `0x100E8`. Phase 1 repeats the same break on the same side (the
//	scratch word is re-read, not re-drawn) at twice the radius. Phase 2 returns
//	5. Exactly one random draw per evasion.
//
// TODO(question): [04 R-AIR-01 §8] says "a record scratch word" without saying
// which of the record's three general parameters holds it. p1 is used, matching
// [04 R-ORD-02 §3], which puts the one drawn value of `VTOL_Follow` and
// `VTOL_SeekGuard` phase 0 in p1 and its derived bit in p2. The choice is
// unobservable in flight — the same side is re-read in phase 1 either way — but
// it is visible in a record dump, so it is recorded rather than assumed.
// A trace of the store settles it.
func (s *System) legVTOLEvade(u *units.Unit, n *orders.Node, tick uint32) orders.Code {
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all*, the clause's stated failure [04 R-ORD-02 §3]
		}
		sim := s.simRNG(u)
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		n.Param1 = sim.Uint32n(2)
		s.installEvadeBreak(u, n, 1)
		return 1
	case 1:
		s.installEvadeBreak(u, n, 2)
		return 1
	default:
		return 5
	}
}

// installEvadeBreak builds one break leg at `multiple × Range`.
func (s *System) installEvadeBreak(u *units.Unit, n *orders.Node, multiple int64) {
	turn := uint16(0x4000)
	if n.Param1 != 0 {
		turn = 0xC000
	}
	h := u.Move.Heading + turn
	r := numeric.Fixed(int64(firstWeaponRange(u)) * multiple << 16)
	ox, oz := offsetAtBearing(h, r)
	m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
	m.setArrivalRadius(0x80)
	s.installAirGoal(u, n, m)
	n.DynamicGate |= airLegGateStrike
}

// airLegUnbound is the answer a leg gives when the simulation stream it must
// draw from is not bound to the unit's queue.
//
// TODO(T25): [04 R-AIR-01 §7] and [§8] give every one of these draws as
// unconditional, so there is no retail arm for "no stream". Placeholder: the
// one-tick deadline hold of [04 R-ORD-01 §1], which keeps the record at the
// head and re-dispatches it rather than consuming a draw that does not exist or
// completing an order the player still owns.
func airLegUnbound(n *orders.Node, tick uint32) orders.Code {
	airDeadline(n, tick, 1)
	return 2
}

// --- VTOL_SeekAttack [04 R-AIR-01 §7] ---

// legVTOLSeekAttack is the randomized search orbit an aircraft with no bound
// target flies while looking for one.
//
//	Phase 0 requires a live mover and `canfly`; with a target already bound it
//	tries to latch it and, on success, clears the gate word and returns 0; with
//	no target it defaults the cached goal to the unit's position if that goal is
//	exactly (0,0,0), draws one full-circle bearing (random below 0x10000),
//	stores it and its low bit, and runs the shared takeoff preamble.
//
//	Phase 1, in this order: set the manual-target latch on all three slots; if
//	health is below three quarters of MaxDamage, collect the nearby-unit
//	candidate list within 0xF00 and, if it is non-empty, clear the goal payload,
//	draw one random index, push a VTOL_Landing order at that candidate, clear
//	the gate word and return 0; then ask the ordinary acquisition for a target
//	and return 5 if one is latched; then, if the arrival bits 0xE0 are set,
//	advance the search bearing by −(0x5555 + random below 0x2000); finally build
//	a point marker at the cached goal offset by the search bearing at radius
//	firstWeaponRange + 0xA0, horizontal arrival radius 0x80, install, set the
//	deadline to the current tick plus 30 + random below 30, OR 0xE0 into the
//	gate word, and return 2.
//
// TODO(T25): three steps belong to the weapon layer and are not reachable from
// internal/movement — phase 0's latch attempt on an already-bound target, phase
// 1's manual-target latch on all three slots, and phase 1's autonomous
// acquisition. Placeholder: the latch attempt does not succeed and the
// acquisition finds nothing, so phase 0 always takes the search-setup arm and
// phase 1 always reaches the orbit step. Both are the sections' own "otherwise"
// arms, so the search still runs; what is missing is the shortcut that ends it
// the moment a target is latched.
func (s *System) legVTOLSeekAttack(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if s.installOffMapRecoveryMarker(u, n) {
		return 2 // the recovery leg pre-empts and holds [04 R-AIR-01 §5]
	}
	sim := s.simRNG(u)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 R-ORD-02 §3]
		}
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		if n.Target == 0 && n.GoalX == 0 && n.GoalY == 0 && n.GoalZ == 0 {
			n.GoalX, n.GoalY, n.GoalZ = u.X, u.Y, u.Z
		}
		draw := sim.Uint32n(0x10000)
		n.Param1 = draw
		n.Param2 = draw & 1
		s.takeoffPreamble(u, n)
		// §7 does not name phase 0's result code; every sibling phase 0 in
		// [04 R-ORD-02 §2] and [04 R-ORD-02 §3] advances, and phase 1 below is
		// only reachable that way.
		return 1
	case 1:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		if airBelowThreeQuarters(u) {
			if bases := s.airBaseCandidates(u); len(bases) > 0 {
				s.releaseAirGoal(u)
				pick := bases[sim.Uint32n(uint32(len(bases)))]
				airSpawnAtHead(u, "VTOL_Landing", pick, Vec3{}, tick)
				n.DynamicGate = 0
				return 0 // *restart*
			}
		}
		if satisfied&airLegGate != 0 {
			// The step is subtractive and never additive: about −120 degrees
			// plus up to 45 degrees more, so the search visits three points per
			// revolution before the jitter [04 R-AIR-01 §7].
			n.Param1 = uint32(uint16(n.Param1) - uint16(0x5555+sim.Uint32n(0x2000)))
		}
		r := numeric.Fixed(int64(firstWeaponRange(u)+airOrbitRadiusBonus) << 16)
		// [04 R-AIR-01 §7] writes "the cached goal offset by the search
		// bearing"; the sign is [04 R-ORD-02 §3]'s, which gives the same orbit
		// step for `VTOL_Follow` and `VTOL_SeekGuard` explicitly as
		// `wardPos − offset(p1, r)`.
		ox, oz := offsetAtBearing(uint16(n.Param1), r)
		m := s.newPointMarker(u, Vec3{X: n.GoalX - ox, Y: n.GoalY, Z: n.GoalZ - oz})
		m.setArrivalRadius(0x80)
		s.installAirGoal(u, n, m)
		airDeadline(n, tick, 30+sim.Uint32n(30))
		n.DynamicGate |= airLegGate
		return 2
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// --- VTOL_SeekGuard [04 R-ORD-02 §3] ---

// legVTOLSeekGuard is the guard-seeking orbit.
//
//	Phase 0: mover and `canfly` (else cancel-all); a goal of exactly (0,0,0) →
//	goal = own position; draw RNG(0x10000) into p1 and its low bit into p2;
//	advance — no takeoff preamble: a grounded seeker stays grounded until
//	something else lifts it. Phase 1, in order: health below three quarters →
//	collect the base candidates within 0xF00; any → release the payload,
//	RNG(count), spawn VTOL_Landing at the pick at the head, gate = 0, restart.
//	Then enumerate the units within `sightdistance` through the guard-candidate
//	visitor; non-empty → release the payload, resolve code 7 against the first
//	listed unit and spawn the result at the head whatever it is, gate = 0, wait.
//	Else the orbit step of `VTOL_Follow` leg 4 with r = Range + 0xA0 read from
//	slot 0 unconditionally, about the record's goal; hold. Other phase:
//	cancel-all.
//
// TODO(question): the guard-candidate visitor "admits `u` when `u`'s owner's
// diplomacy byte toward my side is nonzero, `u` is not `canfly`, and `u` is not
// the seeker" [04 R-ORD-02 §4]. The polarity of that first clause is not
// settled by the section: the same phrase admits candidates for the *repair*
// filter in the same list, where the units wanted are friendly, and here the
// units wanted are the ones this aircraft would guard — also friendly — yet
// "nonzero diplomacy" reads naturally as the not-allied side of the byte. The
// only relation this package can reach is the queue binding's hostility
// predicate, which answers the opposite question. Implementing either reading
// would decide whether a seeking guard attaches itself to friends or to enemies
// — an inversion, not an imprecision — so the enumeration is reported empty and
// the leg falls to its own stated "else" arm, the orbit. A trace of that
// visitor's diplomacy compare settles it.
func (s *System) legVTOLSeekGuard(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if s.installOffMapRecoveryMarker(u, n) {
		return 2 // the recovery leg pre-empts and holds [04 R-AIR-01 §5]
	}
	sim := s.simRNG(u)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 R-ORD-02 §3]
		}
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		if n.GoalX == 0 && n.GoalY == 0 && n.GoalZ == 0 {
			n.GoalX, n.GoalY, n.GoalZ = u.X, u.Y, u.Z
		}
		draw := sim.Uint32n(0x10000)
		n.Param1 = draw
		n.Param2 = draw & 1
		return 1
	case 1:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		if airBelowThreeQuarters(u) {
			if bases := s.airBaseCandidates(u); len(bases) > 0 {
				s.releaseAirGoal(u)
				pick := bases[sim.Uint32n(uint32(len(bases)))]
				airSpawnAtHead(u, "VTOL_Landing", pick, Vec3{}, tick)
				n.DynamicGate = 0
				return 0 // *restart*
			}
		}
		// The guard-candidate enumeration is the open question recorded above;
		// with an
		// empty list the leg is the orbit step of `VTOL_Follow` leg 4, about
		// the record's goal rather than a ward, with the radius read from slot
		// 0 unconditionally [04 R-ORD-02 §3].
		if satisfied&airLegGate != 0 {
			n.Param1 = uint32(uint16(n.Param1) - uint16(0x4000+sim.Uint32n(0x2000)))
		}
		r := numeric.Fixed(int64(firstWeaponRange(u)+airOrbitRadiusBonus) << 16)
		ox, oz := offsetAtBearing(uint16(n.Param1), r)
		m := s.newPointMarker(u, Vec3{X: n.GoalX - ox, Y: n.GoalY, Z: n.GoalZ - oz})
		m.setArrivalRadius(0x80)
		s.installAirGoal(u, n, m)
		airDeadline(n, tick, 30)
		n.DynamicGate |= airLegGateOrbit
		return 2
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// --- AirStrike [04 R-AIR-01 §8] ---

// airRadiusWord truncates a computed horizontal arrival radius to the 16-bit
// word the marker stores [04 R-AIR-01 §4].
func airRadiusWord(v int64) uint16 { return uint16(int16(v)) }

// airGravityWord recovers the runtime gravity word `AirStrike` phase 4 reads:
// "the runtime word the map loader fills from the OTA `gravity` key, defaulting
// to `0x1FDB` when neither the OTA nor the TNT header supplies one"
// [04 R-AIR-01 §8]. This build stores that word already converted to a per-tick
// 16.16 acceleration by `authored × 65536 / 900` [03 §2.2][fmt ota], so the
// authored word is recovered by the inverse; the conversion round-trips for
// every authored value a map can carry.
func (s *System) airGravityWord() int64 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return int64(s.Terrain.Gravity) * 900 / 65536
}

// airReleaseLead is the bombing run's ballistic release lead
// [04 R-AIR-01 §8] phase 4: `t = sqrt((2 · cruisealt) / gravity)`,
// `lead = trunc(t · 30.0 · speedInteger)`, where `speedInteger` is the signed
// 16-bit integer part of the mover's scalar speed word. The `30.0` converts the
// free-fall time from seconds to ticks against a per-tick speed, so the whole
// expression is `speed_per_tick · 30 · sqrt(2·cruisealt/gravity)` world units.
//
// Retail evaluates the square root and the product in double precision. This
// build forms the same value in fixed point instead — `q = (2·cruisealt << 32)
// / gravity` makes `isqrt(q)` equal `sqrt(2·cruisealt/gravity) << 16`, and the
// final shift takes the product back down — so I2 needs no row here, because
// there is no float temporary to allow.
//
// That is a deliberate, closed choice, not a deferred one. The two forms agree
// except in the last bit of a truncation, the result is a bomb-release lead in
// whole world units where one unit is imperceptible, and competitive desync
// hashes are explicitly out of scope for this project (AGENTS.md "Not now"), so
// nothing downstream can observe the difference. Matching retail's double here
// would buy exactness in a quantity nothing compares, at the cost of a float
// site in a simulation package.
func airReleaseLead(u *units.Unit, gravity int64) int64 {
	if u == nil || u.Def == nil || gravity == 0 {
		return 0
	}
	num := int64(u.Def.CruiseAlt) * 2
	if num <= 0 {
		return 0
	}
	root := int64(isqrt(uint64((num << 32) / gravity))) // sqrt(2c/g) in 16.16
	speed := int64(int16(u.Move.Speed.Raw() >> 16))
	return (30 * speed * root) >> 16
}

// legAirStrike is the bombing run, with the ballistic release lead of
// [04 R-AIR-01 §8]'s seven-row table. Phase 6 either loops back to phase 3 for
// another run or breaks off to land.
//
// TODO(T25): every weapon-layer step of the table is out of this package's
// reach — phase 0's `Attacking` status caption, phase 1's manual-target latch
// on all three slots and its release on slot 0, phase 5's "order the weapons to
// fire at the cached goal position", and phase 6's "stop firing". Placeholder:
// the flight legs run and the guns do not, so an aircraft flies the whole run
// and drops nothing. internal/combat owns the slots; the seam that would let an
// order reach them does not exist yet.
func (s *System) legAirStrike(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	// Step 4 of the shared entry sequence: `AirStrike` "tests the sentinel
	// nowhere at all", so there is no off-map recovery on this executor
	// [04 R-AIR-01 §8].
	sim := s.simRNG(u)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 R-ORD-02 §3]
		}
		s.takeoffPreamble(u, n)
		return 1
	case 1:
		// The test only decides whether a repositioning marker is installed;
		// both branches return 1, so the phase advances either way.
		if airPlanarDistance(n.GoalX, n.GoalZ, u.X, u.Z) < 0x1E00000 {
			b := bearing(u.X, u.Z, n.GoalX, n.GoalZ)
			ox, oz := offsetAtBearing(b, numeric.Fixed(0x8C00000)) // 2240 world units
			m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
			m.setArrivalRadius(0x3C0)
			s.installAirGoal(u, n, m)
			n.DynamicGate |= airLegGateReposition
		}
		return 1
	case 2:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		d := airPlanarDistance(n.GoalX, n.GoalZ, u.X, u.Z)
		h := bearing(u.X, u.Z, n.GoalX, n.GoalZ)
		jittered := h + uint16(sim.Uint32n(0x4000)) - 0x2000 // uniform ±45 degrees
		ox, oz := offsetAtBearing(jittered, numeric.Fixed(d/2))
		m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
		m.setArrivalRadius(0x1E0)
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateStrike
		return 1
	case 3:
		return 1 // no work
	case 4:
		if satisfied&airLegGate != 0 {
			return 1 // arrived: advance and do nothing else
		}
		gravity := s.airGravityWord()
		if gravity == 0 {
			return 7 // *cancel-all* of the whole queue [04 R-AIR-01 §8]
		}
		lead := airReleaseLead(u, gravity)
		var m *airMarker
		if n.Target != 0 {
			m = s.newFollowUnitMarker(u, n.Target)
		} else {
			m = s.newPointMarker(u, Vec3{X: n.GoalX, Y: n.GoalY, Z: n.GoalZ})
		}
		runLength := int64(0)
		if u.Def != nil {
			runLength = int64(u.Def.AttackRunLength)
		}
		m.setArrivalRadius(airRadiusWord(lead + 1 + runLength))
		s.installAirGoal(u, n, m)
		airDeadline(n, tick, 1)
		n.DynamicGate |= airLegGateStrike
		return 2
	case 5:
		runLength := int64(0)
		if u.Def != nil {
			runLength = int64(u.Def.AttackRunLength)
		}
		b := bearing(u.X, u.Z, n.GoalX, n.GoalZ)
		ox, oz := offsetAtBearing(b, numeric.Fixed((runLength+airOverflyBonus)<<16))
		m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
		m.setArrivalRadius(0x3C0)
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateReposition
		return 1
	case 6:
		ox, oz := offsetAtBearing(u.Move.Heading, numeric.Fixed(0x5A00000)) // 1440 world units
		m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
		m.setArrivalRadius(0x80)
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateReposition
		if !airBelowThreeQuarters(u) {
			n.Phase = 3
			return 2 // fly another run
		}
		if sim != nil {
			if bases := s.airBaseCandidates(u); len(bases) > 0 {
				s.releaseAirGoal(u)
				pick := bases[sim.Uint32n(uint32(len(bases)))]
				airSpawnAtHead(u, "VTOL_Landing", pick, Vec3{}, tick)
				n.DynamicGate = 0
			}
		}
		return 0 // *restart*, with or without candidates
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// --- AirToGround and AirToGroundHover [04 R-AIR-01 §8] ---

// airFindBaseAndLand is the "find a base and land" branch `AirStrike` phase 6
// states and `AirToGround` phase 4, `AirToGroundHover` phase 3,
// `VTOL_SeekAttack` phase 1 and `VTOL_SeekGuard` phase 1 all reuse: collect the
// candidates within 0xF00, and if any exist clear the payload, draw one random
// index, push a `VTOL_Landing` order at that candidate and clear the gate word.
// It reports whether it took the branch.
func (s *System) airFindBaseAndLand(u *units.Unit, n *orders.Node, sim *rng.Simulation, tick uint32) bool {
	if sim == nil {
		return false
	}
	bases := s.airBaseCandidates(u)
	if len(bases) == 0 {
		return false
	}
	s.releaseAirGoal(u)
	pick := bases[sim.Uint32n(uint32(len(bases)))]
	airSpawnAtHead(u, "VTOL_Landing", pick, Vec3{}, tick)
	n.DynamicGate = 0
	return true
}

// airJitteredApproach is the leg `AirStrike` phase 2 and `AirToGround` phase 1
// share: a uniform ±45-degree jitter about the bearing to the cached goal, at
// half the current distance [04 R-AIR-01 §8].
func (s *System) airJitteredApproach(u *units.Unit, n *orders.Node, sim *rng.Simulation, radius uint16) {
	d := airPlanarDistance(n.GoalX, n.GoalZ, u.X, u.Z)
	h := bearing(u.X, u.Z, n.GoalX, n.GoalZ)
	jittered := h + uint16(sim.Uint32n(0x4000)) - 0x2000
	ox, oz := offsetAtBearing(jittered, numeric.Fixed(d/2))
	m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
	m.setArrivalRadius(radius)
	s.installAirGoal(u, n, m)
}

// legAirToGround is the strafing run, six phases [04 R-AIR-01 §8]:
//
//	0 — status caption `Attacking`; shared takeoff preamble.
//	1 — set the manual-target latch on all three slots; the same ±0x2000 random
//	    jitter about the bearing to the goal at half the current distance,
//	    horizontal arrival radius 0x80; gate = 0x100E8.
//	2 — release the slot-0 latch; order the weapons at the target if one is
//	    bound, else at the cached goal point; build a point marker on the cached
//	    goal whose horizontal arrival radius is the unit's first weapon slot's
//	    `Range`; gate = 0x100E8.
//	3 — the fly-through: `h = atan2(unitX − goalX, unitZ − goalZ)`; point marker
//	    at `goalPos − offset(h, Range · 3)` with horizontal arrival radius
//	    `0x80 + random below 0x80`; gate = 0x100EA.
//	4 — health below three quarters → the find-a-base-and-land branch; otherwise
//	    draw `random below 2` and add `0xC000` on 0 or `0x4000` otherwise
//	    from the unit's own heading, at radius `Range`, horizontal arrival radius
//	    0x80; gate = 0x100EA.
//	5 — set the phase to 2 and return 2, closing the loop.
//
// TODO(T25): the weapon-layer steps — the phase 0 caption, phase 1's latch,
// phase 2's release and fire order — are internal/combat's and are not
// reachable here. Placeholder: the flight legs run and the guns do not.
func (s *System) legAirToGround(u *units.Unit, n *orders.Node, tick uint32) orders.Code {
	if s.installOffMapRecoveryMarker(u, n) {
		return 2 // step 4: `AirToGround` "takes the recovery leg" [04 R-AIR-01 §8]
	}
	sim := s.simRNG(u)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 R-ORD-02 §3]
		}
		s.takeoffPreamble(u, n)
		return 1
	case 1:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		s.airJitteredApproach(u, n, sim, 0x80)
		n.DynamicGate = airLegGateStrike
		return 1
	case 2:
		m := s.newPointMarker(u, Vec3{X: n.GoalX, Y: n.GoalY, Z: n.GoalZ})
		m.setArrivalRadius(airRadiusWord(int64(firstWeaponRange(u))))
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateStrike
		return 1
	case 3:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		h := bearing(u.X, u.Z, n.GoalX, n.GoalZ)
		ox, oz := offsetAtBearing(h, numeric.Fixed(int64(firstWeaponRange(u))*3<<16))
		m := s.newPointMarker(u, Vec3{X: n.GoalX - ox, Y: n.GoalY, Z: n.GoalZ - oz})
		m.setArrivalRadius(uint16(0x80 + sim.Uint32n(0x80)))
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateStrafe
		return 1
	case 4:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		if airBelowThreeQuarters(u) {
			s.airFindBaseAndLand(u, n, sim, tick)
			return 0 // *restart*, with or without candidates [04 R-AIR-01 §8]
		}
		turn := uint16(0x4000)
		if sim.Uint32n(2) == 0 {
			turn = 0xC000
		}
		// The break is taken from the unit's own heading, at the leg's radius,
		// in the `unitPos − offset(…)` form every break leg of §8 uses.
		ox, oz := offsetAtBearing(u.Move.Heading+turn, numeric.Fixed(int64(firstWeaponRange(u))<<16))
		m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
		m.setArrivalRadius(0x80)
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateStrafe
		return 1
	case 5:
		n.Phase = 2
		return 2 // closing the loop
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// legAirToGroundHover is the `hoverattack` standoff [04 R-AIR-01 §8]. Phases 0
// and 1 match `AirToGround`. Phase 2 releases the slot-0 latch, aims at the
// target, builds a point marker on the target's current position with
// horizontal arrival radius equal to the first weapon slot's `Range`, and
// zeroes two record scratch words (a side flag and a miss counter). Phase 3 is
// the orbit: a deterministic ±45-degree left/right alternation about the
// heading to the target, on a frozen terrain-relative marker at
// `targetPos + offset(h', (Range · 2) / 3)` — note the plus — with horizontal
// arrival radius 0x10; and, when the unit has failed to engage more than once,
// a random full-circle reposition at `targetPos − offset(bearing, Range)`
// instead.
//
// TODO(T25): "ask the weapon layer whether the unit can engage the target; if
// it cannot, increment the miss counter" is internal/combat's question and this
// package cannot ask it. Placeholder: the unit is treated as able to engage, so
// the miss counter never rises and the leg always takes the deterministic
// alternating-sides orbit — the arm that is the executor's actual standoff
// behavior. The random reposition arm is therefore unreachable until the seam
// exists; it is written out so that wiring the question is a one-line change.
// The phase 0 caption, phase 2's latch release and its aim are the same
// unreachable weapon layer.
//
// TODO(question): [04 R-AIR-01 §8] does not name which record scratch words
// phase 2 zeroes; p1 (the side flag) and p2 (the miss counter) are used, in the
// order the section lists them. See legVTOLEvade's note on the same question.
func (s *System) legAirToGroundHover(u *units.Unit, n *orders.Node, tick uint32) orders.Code {
	if s.installOffMapRecoveryMarker(u, n) {
		return 2 // step 4: `AirToGroundHover` takes the recovery leg
	}
	sim := s.simRNG(u)
	targetX, targetY, targetZ := n.GoalX, n.GoalY, n.GoalZ
	if t := s.unitFor(n.Target); t != nil && t.Alive {
		targetX, targetY, targetZ = t.X, t.Y, t.Z
	}
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 R-ORD-02 §3]
		}
		s.takeoffPreamble(u, n)
		return 1
	case 1:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		s.airJitteredApproach(u, n, sim, 0x80)
		n.DynamicGate = airLegGateStrike
		return 1
	case 2:
		m := s.newPointMarker(u, Vec3{X: targetX, Y: targetY, Z: targetZ})
		m.setArrivalRadius(airRadiusWord(int64(firstWeaponRange(u))))
		s.installAirGoal(u, n, m)
		n.Param1 = 0 // the side flag
		n.Param2 = 0 // the miss counter
		n.DynamicGate = airLegGateStrike
		return 1
	case 3:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		rangeUnits := int64(firstWeaponRange(u))
		if n.Param2 > 1 {
			n.Param2 = 0
			b := uint16(sim.Uint32n(0x10000))
			ox, oz := offsetAtBearing(b, numeric.Fixed(rangeUnits<<16))
			m := s.newPointMarker(u, Vec3{X: targetX - ox, Y: targetY, Z: targetZ - oz})
			m.setArrivalRadius(0x80)
			s.installAirGoal(u, n, m)
			n.DynamicGate |= airLegGateHoverMiss
			return 2
		}
		h := bearing(u.X, u.Z, targetX, targetZ)
		var side uint16
		if n.Param1 == 0 {
			side = h - 0x2000
			n.Param1 = 1
		} else {
			side = h + 0x2000
			n.Param1 = 0
		}
		ox, oz := offsetAtBearing(side, numeric.Fixed((rangeUnits*2/3)<<16))
		m := s.newFrozenTerrainPointMarker(u, Vec3{X: targetX + ox, Y: targetY, Z: targetZ + oz})
		if u.Def != nil {
			m.setAltitudeOffset(int16(u.Def.CruiseAlt))
		}
		m.setArrivalRadius(0x10)
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateStrike
		if airBelowThreeQuarters(u) && s.airFindBaseAndLand(u, n, sim, tick) {
			return 0 // *restart* behind the spawned landing order
		}
		return 2
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// --- AirToAir and its velocity-carrying payload [04 R-AIR-01 §8] ---

// airVelocityArrival is the velocity marker's hard-coded arrival distance:
// 48 world units of horizontal distance [04 R-AIR-01 §8].
const airVelocityArrival = 48

// airVelocityMarker is the second goal-payload class the dogfight legs command
// [04 R-AIR-01 §8]: "they command a *position and a velocity*, through a second
// payload class whose per-tick goal update advances its own position by its own
// velocity vector each tick (X and Z only; Y is not advanced) and, when its
// 'steer to a commanded heading' flag is set, rotates the velocity's horizontal
// pair toward the commanded heading by at most `TurnRate >> 3` per tick,
// zeroing the vertical component whenever it does."
//
// Its heading supply is [04 R-ORD-02 §4]'s: it "writes `bearing(unitPos →
// markerGoal)` and returns 1 unconditionally".
type airVelocityMarker struct {
	sys       *System
	unit      *units.Unit
	pos       Vec3
	vel       Vec3
	commanded uint16
	steer     bool
}

// UpdateGoal advances the marker's own position by its own velocity, then
// steers the velocity when the flag is set [04 R-AIR-01 §8].
func (m *airVelocityMarker) UpdateGoal(u *units.Unit, dst *Vec3) {
	if m == nil || dst == nil {
		return
	}
	m.pos.X += m.vel.X
	m.pos.Z += m.vel.Z
	if m.steer {
		m.steerVelocity(u)
	}
	*dst = m.pos
}

// steerVelocity rotates the horizontal velocity pair toward the commanded
// heading by at most `TurnRate >> 3`, and zeroes the vertical component
// [04 R-AIR-01 §8].
func (m *airVelocityMarker) steerVelocity(u *units.Unit) {
	m.vel.Y = 0
	step := uint16(0)
	if u != nil && u.Def != nil && u.Def.TurnRate > 0 {
		step = uint16(u.Def.TurnRate >> 3)
	}
	current := airVelocityHeading(m.vel)
	delta := m.commanded - current
	switch {
	case delta == 0:
		return
	case delta <= 0x8000:
		if uint32(delta) > uint32(step) {
			delta = step
		}
		current += delta
	default:
		back := uint16(0x10000 - uint32(delta))
		if uint32(back) > uint32(step) {
			back = step
		}
		current -= back
	}
	speed := int64(isqrt(uint64(int64(m.vel.X)*int64(m.vel.X) + int64(m.vel.Z)*int64(m.vel.Z))))
	ox, oz := offsetAtBearing(current, numeric.Fixed(speed))
	m.vel.X, m.vel.Z = -ox, -oz
}

// airVelocityHeading is the heading a velocity vector is travelling on: the
// position step of [04 R-MOV-01 §4] moves along (−sin h, −cos h), so the
// heading of a velocity is the bearing of its negation.
func airVelocityHeading(v Vec3) uint16 { return bearing(0, 0, v.X, v.Z) }

// Arrived is the marker's hard-coded 48-world-unit horizontal test, with the
// additional requirement — when the steer flag is set — that the velocity's
// bearing equal the commanded heading exactly [04 R-AIR-01 §8].
func (m *airVelocityMarker) Arrived(u *units.Unit) bool {
	if m == nil || u == nil {
		return false
	}
	if !AirArrival(u.X, u.Z, m.pos.X, m.pos.Z, airVelocityArrival, 0, 0, false) {
		return false
	}
	if m.steer && airVelocityHeading(m.vel) != m.commanded {
		return false
	}
	return true
}

// SupplyHeading writes bearing(unitPos → markerGoal) and reports a suggestion
// unconditionally [04 R-ORD-02 §4].
func (m *airVelocityMarker) SupplyHeading(u *units.Unit, dst *uint16) bool {
	if m == nil || u == nil || dst == nil {
		return false
	}
	*dst = bearing(u.X, u.Z, m.pos.X, m.pos.Z)
	return true
}

// Persistent reports false: the velocity marker is not a follow marker, so the
// producer releases it on arrival [04 R-AIR-01 §4].
func (m *airVelocityMarker) Persistent() bool { return false }

// Release drops the marker's own references [04 R-AIR-01 §1] step 6.
func (m *airVelocityMarker) Release() {
	if m != nil {
		m.unit = nil
	}
}

// installAirPayload is installAirGoal for any member of the payload family, so
// the velocity marker reaches the command block by the same installer
// [04 R-AIR-01 §4].
func (s *System) installAirPayload(u *units.Unit, rec *orders.Node, p GoalPayload) {
	if s == nil || u == nil || p == nil {
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
	c.Payload = p
}

// legAirToAir is the dogfight [04 R-AIR-01 §8]. Phase 0 is the takeoff preamble
// plus a one-tick deadline. Phase 1 aims at the target and then:
//
//   - arrival bits set and the dot product of the unit→target bearing vector
//     and the unit's own facing vector (both taken at 20 world units) positive
//     → command "straight ahead": position `unitPos − offset(unitHeading,
//     MaxVelocity · 30)`, velocity `−offset(unitHeading, MaxVelocity)`;
//     deadline `tick + 60 + random below 30`; reset the scratch counter; hold.
//   - arrival bits clear and the scratch counter below 0x5A → recompute the
//     dot, add 0x2D to the counter if it is not positive and zero it otherwise;
//     then, with the range to the target above 0xA0 world units, command a lead
//     intercept: position `targetPos + targetVelocity · 45`, velocity derived
//     from the target's heading at half the target's `MaxVelocity`; deadline
//     `tick + 45`; gate `|= 0x100E8`; hold.
//   - otherwise the leg gives up: [04 R-ORD-02 §5] corrects §8's "re-issues a
//     seek order" — the traced arm releases the payload, spawns `VTOL_Evade`
//     with the same target at the head, zeroes the counter and the gate, and
//     returns *restart*.
//
// TODO(question): neither leg is said to set the payload's "steer to a
// commanded heading" flag, and [04 R-AIR-01 §8] describes that flag only in the
// abstract — it changes both the goal update (the velocity is rotated and its
// vertical component zeroed) and the arrival test (the velocity's bearing must
// equal the commanded heading exactly), so setting it on the wrong leg would
// change when a dogfight leg completes. Both legs leave it clear here, which is
// the payload's constructed state; the flag's machinery is implemented so that
// a trace naming its setter is a one-field change.
//
// TODO(question): §8 gives no arm for "arrival bits clear, counter below 0x5A,
// and the range to the target at or below 0xA0" — the lead intercept's own
// condition fails and the give-up arm is the counter's, not the range's.
// The one-tick deadline hold is taken there so the record neither parks nor
// invents a leg; a trace of that fall-through settles it.
//
// TODO(T25): "aims at the target" is internal/combat's and is not reachable
// from this package.
func (s *System) legAirToAir(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if s.installOffMapRecoveryMarker(u, n) {
		return 2 // step 4: `AirToAir` takes the recovery leg [04 R-AIR-01 §8]
	}
	sim := s.simRNG(u)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 R-ORD-02 §3]
		}
		s.takeoffPreamble(u, n)
		airDeadline(n, tick, 1)
		return 1
	case 1:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		targetX, targetY, targetZ := n.GoalX, n.GoalY, n.GoalZ
		t := s.unitFor(n.Target)
		if t != nil && t.Alive {
			targetX, targetY, targetZ = t.X, t.Y, t.Z
		}
		maxVel := int64(0)
		if u.Def != nil {
			maxVel = int64(u.Def.MaxVelocity)
		}
		if satisfied&airLegGate != 0 {
			if airFacingDot(u, targetX, targetZ) > 0 {
				ax, az := offsetAtBearing(u.Move.Heading, numeric.Fixed(maxVel*30))
				vx, vz := offsetAtBearing(u.Move.Heading, numeric.Fixed(maxVel))
				m := &airVelocityMarker{
					sys:  s,
					unit: u,
					pos:  Vec3{X: u.X - ax, Y: u.Y, Z: u.Z - az},
					vel:  Vec3{X: -vx, Z: -vz},
				}
				s.installAirPayload(u, n, m)
				airDeadline(n, tick, 60+sim.Uint32n(30))
				n.Param1 = 0
				return 2
			}
		} else if n.Param1 < 0x5A {
			if airFacingDot(u, targetX, targetZ) > 0 {
				n.Param1 = 0
			} else {
				n.Param1 += 0x2D
			}
			if airPlanarDistance(targetX, targetZ, u.X, u.Z) > int64(0xA0)<<16 {
				tvx, tvz := airUnitVelocity(t)
				tHeading := uint16(0)
				tMax := int64(0)
				if t != nil {
					tHeading = t.Move.Heading
					if t.Def != nil {
						tMax = int64(t.Def.MaxVelocity)
					}
				}
				vx, vz := offsetAtBearing(tHeading, numeric.Fixed(tMax/2))
				m := &airVelocityMarker{
					sys:  s,
					unit: u,
					pos:  Vec3{X: targetX + tvx*45, Y: targetY, Z: targetZ + tvz*45},
					vel:  Vec3{X: -vx, Z: -vz},
				}
				s.installAirPayload(u, n, m)
				airDeadline(n, tick, 45)
				n.DynamicGate |= airLegGateStrike
				return 2
			}
			return airLegUnbound(n, tick) // the unstated range arm, see the note above
		}
		s.releaseAirGoal(u)
		airSpawnAtHead(u, "VTOL_Evade", n.Target, Vec3{X: n.GoalX, Y: n.GoalY, Z: n.GoalZ}, tick)
		n.Param1 = 0
		n.DynamicGate = 0
		return 0 // *restart* [04 R-ORD-02 §5]
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// airFacingDot is the dot product of the unit→target bearing vector and the
// unit's own facing vector, both taken at 20 world units [04 R-AIR-01 §8].
// Only its sign is consulted, so the common negation of the two direction
// vectors ([04 R-MOV-01 §4]'s travel axis) cancels.
func airFacingDot(u *units.Unit, targetX, targetZ numeric.Fixed) int64 {
	const probe = numeric.Fixed(20 << 16)
	ax, az := offsetAtBearing(bearing(u.X, u.Z, targetX, targetZ), probe)
	bx, bz := offsetAtBearing(u.Move.Heading, probe)
	return (int64(ax)*int64(bx) + int64(az)*int64(bz)) >> 16
}

// airUnitVelocity is a unit's per-tick velocity vector: its scalar speed along
// the travel axis of [04 R-MOV-01 §4], which is `−offset(heading, speed)`.
func airUnitVelocity(t *units.Unit) (numeric.Fixed, numeric.Fixed) {
	if t == nil {
		return 0, 0
	}
	ox, oz := offsetAtBearing(t.Move.Heading, t.Move.Speed)
	return -ox, -oz
}
