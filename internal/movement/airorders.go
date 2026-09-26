// The air path marker and the air executors that install it
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
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
// flags 0xA3, goal a supplied triple, and — corrected by [04 R-AIR-01 §4]
// (2026-09-04, WU-19-226) — the **target** the marker is frozen about.
//
// The target is what the 0xA3 flags are for. The freeze bit skips the follow
// branch of the goal update, so the goal never moves; but the follow bit and
// the radial bit still read the target in the two methods the freeze does not
// touch: heading supply writes `bearing(unitPos, targetPos)` while the target
// is live, and persistence keeps the marker through arrival while it is. That
// pair is how a `hoverattack` gunship holds its standoff facing the thing it
// is shooting [04 R-AIR-01 §8]. Built without a target the same flags supply
// no heading and release on arrival, which is a plain point marker with a
// slower name — and a gunship that faces its own line of flight, firing only
// on the ticks its turn happens to sweep the target through the drift gate.
func (s *System) newFrozenTerrainPointMarker(u *units.Unit, target pool.Handle, goal Vec3) *airMarker {
	return &airMarker{
		sys:    s,
		flags:  airMarkerFollow | airMarkerRadial | airMarkerTerrainAlt | airMarkerFreeze,
		unit:   u,
		target: target,
		goal:   goal,
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

// targetAttachPosition is the goal a follow marker takes from its target: the
// target's own origin.
//
// [04 R-AIR-01 §14.1] settles what §4's "target's attach-piece world position"
// resolves to. The follow branch asks the piece world-position locator of
// [04 R-REV-02] for the marker's stored attach-piece index on the target, and
// that locator answers a ZERO offset when the index is negative. The
// follow-unit constructor — flags 0x01/0x07, which is every user of this
// branch: `VTOL_Follow`, `AirStrike`'s bound-target marker, the guard seek —
// stores index −1, so a plain follow marker's goal is exactly the target's
// origin triple, with no piece arithmetic at all. The radial offset of flag
// 0x02 is added after, as §4 says.
//
// Only the follow-unit-piece constructor (flags 0x05, the transport pickup of
// [04 R-AIR-01 §9]) stores a real index, and that one does need the piece
// transform. When a compiled piece world-transform surface reaches this
// package, the pickup marker is the single call site to route through it; the
// contract for every other follow marker is unchanged [04 R-AIR-01 §14.1].
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

// offsetAtBearing is the shared component pair every air executor's "offset at
// a bearing and radius" leg builds: `(sin(h)·r, cos(h)·r)` at the trig table's
// 8192 scale, with retail's round-to-nearest before the shift [04 R-MOV-01 §4].
//
// The pair returned is the UN-negated one, and the sign belongs to the call
// site. The travel direction of a heading is `(−sin h, −cos h)`
// [04 R-MOV-01 §4], so `pos − offsetAtBearing(h, r)` moves r world units ALONG
// h and `pos + offsetAtBearing(h, r)` moves r world units OPPOSITE it. Both
// families exist among the air legs — the second is the marker's radial follow
// offset and the air construction orbit — and each call site below names which
// one it is [04 §10.3][04 R-AIR-01 §4].
func offsetAtBearing(heading uint16, radius numeric.Fixed) (numeric.Fixed, numeric.Fixed) {
	sin := int64(numeric.Sin(numeric.Angle(heading)))
	cos := int64(numeric.Cos(numeric.Angle(heading)))
	return numeric.Fixed((int64(radius)*sin + 0x1000) >> 13), numeric.Fixed((int64(radius)*cos + 0x1000) >> 13)
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

// airSectorHeight reads the sector retained by the completed footprint stamp.
// It deliberately does not re-query the unit coordinates: all air consumers
// observe the same prior-stamp identity and sentinel state [04 R-AIR-01 §5].
func (s *System) airSectorHeight(u *units.Unit) (uint8, bool) {
	if s == nil || u == nil {
		return 0, false
	}
	if coll := handleRow(s.Collisions, u.Handle); coll != nil && coll.airSector != nil && !coll.airOffMap {
		return coll.airSector.Smoothed, true
	}
	return 0, false
}

// installAirGoal installs a payload on the unit's flight command block
// [04 R-AIR-01 §4]: the installer raises the goal-replaced bit 0x80 on the
// record whose payload it replaces, then clears the record's satisfied bits
// 0x20, 0x40, 0x80, 0x100 and 0x200 for the record it installs on. Replacing
// another record leaves its release bit standing; replacing this record's own
// object clears the self-notification [04 R-ORD-01 §9].
func (s *System) installAirGoal(u *units.Unit, rec *orders.Node, m *airMarker) {
	if m != nil {
		s.installAirPayload(u, rec, m)
	}
}

// releaseAirGoal clears the installed payload without installing another. It is
// the "release the payload" step several air executors run before spawning a
// child record [04 R-AIR-01 §7].
func (s *System) releaseAirGoal(u *units.Unit) {
	if s == nil || u == nil {
		return
	}
	if owner := s.airPayloadOwner(u.Handle); owner != nil {
		s.ReleaseGoalPayload(owner)
		return
	}
	s.detachControllerGoal(u.Handle)
}

// --- the air executors ---

// simRNG returns the session stream bound to the owning order queue [I4].
func (s *System) simRNG(u *units.Unit) *rng.Simulation {
	q := orders.QueueForUnit(u)
	if q == nil || q.Binding() == nil {
		return nil
	}
	return q.Binding().SimRNG
}

// legVTOLLandIfCan is `VTOL_LandIfCan` [04 R-AIR-01 §6], the three-phase
// machine an idle aircraft with nowhere to park runs. It lands on terrain, not
// on a pad. The pump owns phase, scratch and gate, preserving the descent
// when a temporary Paralyze record takes the head [04 §2.4].
func (s *System) legVTOLLandIfCan(u *units.Unit, head *orders.Node, satisfied uint32, tick uint32) orders.Code {
	// A successor ends landing before either outcomes or recovery are handled
	// [04 R-AIR-01 §6]. The pump must first admit this visit through its gate.
	if q := orders.QueueOfUnit(u); q != nil {
		chain := q.Primary()
		for i, n := range chain {
			if n == head && i+1 < len(chain) {
				return 5
			}
		}
	}
	// The recovered ordinary producers supply arrival plus release together;
	// the non-arrival arms remain the handler's contract [04 R-AIR-01 §6].
	// Entry: a satisfied route-failure bit 0x40 completes; the off-map recovery
	// leg of [04 R-AIR-01 §5] pre-empts everything else.
	if satisfied&0x40 != 0 {
		return 5
	}
	if s.installOffMapRecoveryMarker(u, head) {
		return 2
	}
	sim := s.simRNG(u)
	switch head.Phase {
	case 0:
		if handleRow(s.Flights, u.Handle) == nil || u.Def == nil || !u.Def.CanFly {
			return 7
		}
		if head.GoalX == 0 && head.GoalY == 0 && head.GoalZ == 0 {
			head.GoalX, head.GoalY, head.GoalZ = u.X, u.Y, u.Z
		}
		if sim == nil {
			airDeadline(head, tick, 1)
			return 2 // unbound fixture: hold without inventing a random stream
		}
		draw := uint16(sim.Uint32n(0x10000))
		head.Param1 = uint32(draw)
		head.Param2 = uint32(draw & 1)
		s.takeoffPreamble(u, head)
		return 1
	case 1:
		if s.landable(u, u.X, u.Z) {
			if bridge := u.ScriptBridge(); bridge != nil {
				bridge.DeferredWake("EndTransport", nil, nil)
			}
			// The altitude offset is zero on dry land and `terrainHeight −
			// seaLevel` (a negative quantity) over water, so composed with the
			// setter's `max(seaLevel, terrainHeight) + offset` both branches
			// command exactly the terrain height and the aircraft settles ON the
			// surface [04 R-AIR-01 §6]. The doc carried these two branches the
			// other way round until the re-trace this unit ran; the correction
			// and its evidence are recorded there.
			offset := int16(0)
			if h, sea, ok := s.terrainAndSea(u.X, u.Z); ok && h <= sea {
				offset = int16(h - sea)
			}
			m := s.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
			m.setAltitudeOffset(offset)
			s.installAirGoal(u, head, m)
			head.DynamicGate = airLegGate
			// The landing script hook: clearing the state byte's activation bit
			// raises `Deactivate` and notification 4 [04 R-AIR-01 §6].
			u.SetActivationEdge(false)
			return 1
		}
		if sim == nil {
			airDeadline(head, tick, 1)
			return 2
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
			return 2
		}
		// All twelve failed. Any delivered movement outcome steps the search
		// bearing by −0x5555 — about −120 degrees — and take a marker at the
		// cached goal offset by it at radius 0xA0, arrival radius 0x40.
		if satisfied&airLegGate != 0 {
			head.Param1 -= 0x5555
		}
		// Along the search bearing, so the pair is subtracted [04 R-AIR-01 §6].
		ox, oz := offsetAtBearing(uint16(head.Param1), numeric.Fixed(0xA0<<16))
		m := s.newPointMarker(u, Vec3{X: head.GoalX - ox, Y: head.GoalY, Z: head.GoalZ - oz})
		m.setArrivalRadius(0x40)
		s.installAirGoal(u, head, m)
		head.DynamicGate |= airLegGate
		return 2
	case 2:
		if satisfied&0x20 == 0 {
			return 8
		}
		// Phase 2 completes the landing: the mode setter zeroes the velocity and
		// the scalar speed and levels bank and pitch [04 R-AIR-01 §3], and the
		// pending mode write reaches ordinary validation on the mover visit
		// [04 R-COLL-01 §1][04 R-AIR-01 §6 "Touchdown"].
		s.SetMoverMode(u, 1)
		return 5
	default:
		return 7
	}
}

// legVTOLStandby keeps its phase and integer post on the order record, so
// both survive load and temporary primary-head replacement [04 R-AIR-01 §7]
// [08 R-SAVE-ORDER-01]. The pump owns advance, wait and completion; no private
// executor phase or cached post is needed.
func (s *System) legVTOLStandby(u *units.Unit, n *orders.Node, tick uint32) orders.Code {
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7
		}
		if weapons := orderWeapons(u); weapons != nil && weapons.ReleaseSlot != nil {
			for slot := 0; slot < units.NumSlots; slot++ {
				weapons.ReleaseSlot(u, slot)
			}
		}
		n.DynamicGate |= 0x10000
		airDeadline(n, tick, 1)
		n.GuardX, n.GuardY = int16(u.X>>16), int16(u.Z>>16)
		return 1
	case 1:
		if orders.AutonomousAcquire(u) {
			n.DynamicGate = 0
			n.Phase = 0
			return 3
		}
		return 1
	case 2:
		sim := s.simRNG(u)
		if u.Def == nil || !u.Def.CanFly || u.Move.Mode&3 != 2 {
			if sim == nil {
				return airLegUnbound(n, tick)
			}
			n.DynamicGate |= 0x10000
			airDeadline(n, tick, 30+sim.Uint32n(30))
			n.Phase = 1
			return 2
		}
		if len(u.Attachment.Cargo) > 0 {
			if sim == nil {
				return airLegUnbound(n, tick)
			}
			// The three draws are bearing, radius, delay. The signed integer
			// post is widened before subtracting the fixed-point displacement
			// [04 R-AIR-01 §7][08 R-SAVE-ORDER-01].
			b := uint16(sim.Uint32n(0x10000))
			radius := numeric.Fixed(int64(8+sim.Uint32n(0x20)) << 16)
			ox, oz := offsetAtBearing(b, radius)
			m := s.newPointMarker(u, Vec3{X: numeric.Fixed(int64(n.GuardX)<<16) - ox, Z: numeric.Fixed(int64(n.GuardY)<<16) - oz})
			m.setAltitudeOffset(int16(u.Def.CruiseAlt))
			s.installAirGoal(u, n, m)
			airDeadline(n, tick, 30+sim.Uint32n(15))
			n.Phase = 1
			return 2
		}
		airSpawnAtHead(u, "VTOL_LandIfCan", 0, Vec3{X: n.GoalX, Y: n.GoalY, Z: n.GoalZ}, tick)
		return 5
	default:
		return 7
	}
}

// ---------------------------------------------------------------------------
// The air construction orbit [04 §10.3][04 R-ORD-02 §2][04 R-ORD-01 §7]
// ---------------------------------------------------------------------------

// Air construction orbit constants [04 §10.3].
const (
	// airOrbitPeriod is the recurrence cadence: the orbit marker is rebuilt on
	// every tick whose global tick number is an exact multiple of 150.
	airOrbitPeriod = 150
	// airOrbitStep is the signed angular step 0xDB6E (−9362), about −51.43
	// degrees. Seven of them are 65,534 of the 65,536-unit circle, which is why
	// the recurrence walks about seven stations per revolution — a consequence
	// of the step, not an authored station count.
	airOrbitStep = uint16(0xDB6E)
)

// VisitAirBuildApproach runs the two movement legs of VTOL_MobileBuild from
// the construction owner's order visit [04 R-ORD-02 §2]. The caller applies
// the returned pump result: 1 advances the same persisted Node.Phase, while 7
// cancels the invalid order. No parallel movement phase survives the visit.
func (s *System) VisitAirBuildApproach(u *units.Unit, n *orders.Node, _ uint32, _ uint32) orders.Code {
	if s == nil || u == nil || n == nil {
		return 7
	}
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7
		}
		orders.NotifyCaptionClear(u, n, "Building")
		s.takeoffPreamble(u, n)
		return 1
	case 1:
		n.Param3 = 0
		goalX, goalZ := n.GoalX, n.GoalZ
		// The marker is centered on the PRODUCT's footprint. The retained
		// order goal remains the authored request [04 R-ORD-02 §2]
		// [04 R-PATH-01 §13]. An unbound resolver preserves that request.
		if s.ProductFootprint != nil {
			if fx, fz, ok := s.ProductFootprint(n.Param1); ok {
				goalX, goalZ = snapToOwnFootprint(goalX, goalZ, fx, fz)
			}
		}
		m := s.newPointMarker(u, Vec3{X: goalX, Y: n.GoalY, Z: goalZ})
		m.setArrivalRadius(airBuildDistance(u))
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGate
		return 1
	default:
		return 7
	}
}

// airBuildDistance is the builder's authored `builddistance` as the 16-bit
// horizontal arrival radius the air build legs write [04 R-ORD-02 §2]
// [04 R-ORD-01 §7], and as the orbit radius of [04 §10.3].
func airBuildDistance(u *units.Unit) uint16 {
	if u == nil || u.Def == nil {
		return 0
	}
	return uint16(u.Def.BuildDistance)
}

// VisitAirBuildWork installs the work body's due orbit station before its
// work quantum, including a visit that completes the product [04 §10.3].
// The owning order handler calls it only from its established work phases.
//
// The station is `builddistance` world units from the work target, at the
// builder's own angular position about that target advanced by `airOrbitStep`,
// and the marker carries that same angle as its explicit heading — which is the
// direction from the station back at the target, so the aircraft faces what it
// is building. The arithmetic is the target's position PLUS the un-negated
// component pair, which is the opposite sign from the approach and orbit legs
// of [04 R-AIR-01 §7] and is what makes the stations circle the target instead
// of crossing over it. There is no arrival radius and no altitude setter: the
// marker's own goal update keeps its Y at the cruise altitude over the sector
// the aircraft is in, every tick [04 R-AIR-01 §4].
func (s *System) VisitAirBuildWork(u *units.Unit, head *orders.Node, tick uint32) {
	if s == nil || u == nil || head == nil {
		return
	}
	if tick%airOrbitPeriod != 0 {
		return
	}
	targetX, targetY, targetZ := head.GoalX, head.GoalY, head.GoalZ
	if t := s.unitFor(head.Target); t != nil && t.Alive {
		targetX, targetY, targetZ = t.X, t.Y, t.Z
	}
	angle := bearing(u.X, u.Z, targetX, targetZ) + airOrbitStep
	r := numeric.Fixed(int64(airBuildDistance(u)) << 16)
	ox, oz := offsetAtBearing(angle, r)
	m := s.newPointMarker(u, Vec3{X: targetX + ox, Y: targetY, Z: targetZ + oz})
	m.setHeading(angle)
	s.installAirGoal(u, head, m)
}

// ---------------------------------------------------------------------------
// VTOL_Landing — the seven-phase pad-landing machine [04 R-AIR-01 §6]
// ---------------------------------------------------------------------------

// airPadCandidates is the candidate count `QueryLandingPad` offers: pieces 0
// through 3, tried strictly in index order, a −1 cell skipped rather than
// treated as end-of-list [04 R-AIR-01 §6].
const airPadCandidates = 4

// airNoPiece is the reserved no-piece index [04 R-UNIT-06 §3].
const airNoPiece = 0xFF

// queryLandingPad is the pad scan of [04 R-AIR-01 §6]: the first candidate
// piece 0..3 of the target that is free wins, where a pad piece is free exactly
// when the pad owner is not itself carried and no unit in the pad owner's cargo
// list records that same attach-piece index.
//
// The candidate piece indices come from a synchronous `QueryLandingPad` on the
// TARGET's script, with all four outputs pre-seeded −1, so a target whose script
// answers nothing offers no pad at all [04 R-AIR-01 §6][04 §5.3]. A −1 cell is
// skipped rather than treated as end-of-list.
//
// This previously offered all four indices unconditionally for any `isairbase`
// target, on the stated grounds that internal/movement had no COB surface. That
// was wrong twice over: this package already imports internal/cob, and the pad's
// own VM is reachable through the unit record. The placeholder was strictly more
// permissive than retail, so it could not deny a pad that retail grants — but it
// granted pads retail denies, and it made the pad piece a fiction, since the
// index it returned was a loop counter rather than anything the model authored.
func (s *System) queryLandingPad(pad *units.Unit) (uint16, bool) {
	if pad == nil || !pad.Alive || pad.Attachment.Carrier != 0 {
		return 0, false
	}
	if pad.Def == nil || !pad.Def.IsAirBase {
		return 0, false
	}
	bridge := pad.ScriptBridge()
	if bridge == nil {
		// No script bound at all. The seed is what retail would be left holding,
		// and every cell of it is −1, so there is no pad to offer.
		return 0, false
	}
	result := bridge.QueryLandingPad()
	for candidate := 0; candidate < airPadCandidates; candidate++ {
		piece := result.Values[candidate]
		if piece < 0 {
			continue
		}
		if s.padPieceFree(pad, uint16(piece)) {
			return uint16(piece), true
		}
	}
	return 0, false
}

// padPieceFree is the free-pad predicate of [04 R-AIR-01 §6]: the pad owner is
// not itself carried and no unit in its cargo list holds this attach piece.
func (s *System) padPieceFree(pad *units.Unit, piece uint16) bool {
	if pad == nil || pad.Attachment.Carrier != 0 {
		return false
	}
	for _, h := range pad.Attachment.Cargo {
		guest := s.unitFor(h)
		if guest == nil {
			continue
		}
		if guest.Attachment.AttachPiece == int(piece) {
			return false
		}
	}
	return true
}

// legVTOLLanding runs the pad-landing row from the order pump. Its gate,
// deadline and satisfied set belong to the record, so target removal and the
// timed pad recheck remain runnable while a marker has not arrived
// [04 §3.3][04 R-AIR-01 §6].
func (s *System) legVTOLLanding(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if s.rules().RepairPadQueue(s) && len(u.Attachment.Cargo) == 0 {
		return s.legQueuedRepairLanding(u, n, satisfied, tick)
	}
	return s.legPadLanding(u, n, satisfied, tick, nil)
}

// A Modern reservation changes admission, not the flight or repair machinery.
// A nil reservation is the unmodified retail landing sequence.
func (s *System) legPadLanding(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32, reservation *repairLanding) orders.Code {
	pad := s.unitFor(n.Target)
	if pad == nil || !pad.Alive {
		orders.NotifyStatus(u, 7, "Landing aborted")
		return 8
	}
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7
		}
		orders.NotifyCaptionClear(u, n, "Landing")
		s.takeoffPreamble(u, n)
		if sim := s.simRNG(u); sim != nil {
			n.Param1 = sim.Uint32n(0x10000)
		}
		return 1
	case 1:
		piece, ok := s.landingPiece(pad, reservation)
		if !ok || !s.padPieceFree(pad, piece) {
			_, ok = s.landingPiece(pad, reservation)
		}
		if ok {
			n.Phase = 2
			return 2
		}
		ox, oz := offsetAtBearing(uint16(n.Param1), numeric.Fixed(int64(firstWeaponRange(u))<<16))
		m := s.newPointMarker(u, Vec3{X: pad.X - ox, Y: pad.Y, Z: pad.Z - oz})
		m.setArrivalRadius(0x80)
		s.installAirGoal(u, n, m)
		n.DynamicGate = 0xE8
		n.Param1 = uint32(uint16(n.Param1) + uint16(0x4000))
		return 2
	case 2:
		m := s.newFollowPieceMarker(u, n.Target, airNoPiece)
		m.setArrivalRadius(0xA0)
		s.installAirGoal(u, n, m)
		n.DynamicGate = 0xE8
		return 1
	case 3:
		piece, ok := s.landingPiece(pad, reservation)
		if !ok {
			orders.NotifyStatus(u, 7, "Landing failed")
			return 0
		}
		n.Param1 = uint32(piece)
		m := s.newFollowPieceMarker(u, n.Target, piece)
		m.setArrivalRadius(0x30)
		s.installAirGoal(u, n, m)
		n.DynamicGate = 0xE8
		return 1
	case 4:
		return 1
	case 5:
		if satisfied&0x20 != 0 {
			return 1
		}
		piece := uint16(n.Param1)
		if !s.padPieceFree(pad, piece) {
			var ok bool
			piece, ok = s.landingPiece(pad, reservation)
			if !ok {
				orders.NotifyStatus(u, 7, "Landing aborted: all pads are occupied")
				return 0
			}
			n.Param1 = uint32(piece)
		}
		m := s.newFollowPieceMarker(u, n.Target, piece)
		offset := int16(0)
		if len(u.Attachment.Cargo) != 0 {
			if cargo := s.unitFor(u.Attachment.Cargo[0]); cargo != nil && cargo.Def != nil {
				offset = int16(cargo.Def.ModelTopFixed >> 16)
			}
		}
		m.setAltitudeOffset(offset)
		if bridge := u.ScriptBridge(); bridge != nil {
			bridge.DeferredWake("EndTransport", nil, nil)
		}
		s.installAirGoal(u, n, m)
		airDeadline(n, tick, 15)
		n.DynamicGate |= 0xE8
		return 2
	case 6:
		if satisfied&0x40 != 0 {
			return 8
		}
		piece := uint16(n.Param1)
		if !s.padPieceFree(pad, piece) {
			orders.NotifyStatus(u, 7, "Landing aborted: no pads available")
			return 0
		}
		if len(u.Attachment.Cargo) == 0 {
			AttachCargo(s.world, n.Target, u.Handle, int(piece))
			if reservation != nil && padRepairsLander(u, pad) {
				// Leave through the ordinary move executor after SelfRepair,
				// including when there was no suspended order to free the pad.
				goal := s.repairHoldingPoint(reservation)
				airSpawnAtHead(u, "VTOL_Move", 0, goal, tick)
			}
			if padRepairsLander(u, pad) {
				s.ReleaseGoalPayload(n)
				airSpawnAtHead(u, "SelfRepair", n.Target, Vec3{X: u.X, Y: u.Y, Z: u.Z}, tick)
			}
		} else {
			if bridge := u.ScriptBridge(); bridge != nil {
				bridge.Deferred("EndTransport", nil, nil)
			}
			AttachCargo(s.world, n.Target, u.Attachment.Cargo[0], int(piece))
		}
		return 5
	default:
		return 7
	}
}

// padRepairsLander is `VTOL_Landing` phase 6's three-clause repair test
// [04 R-AIR-01 §6]: the lander is below its definition's `MaxDamage`, the pad
// owner's definition carries both `isairbase` and `builder`, and the pad owner
// is not under construction.
//
// The health compare is the definition word against the unit's own health, the
// same pairing every other pad-side test in [04 R-AIR-01 §11] makes; a lander
// already at or above full health gets no record, and the repair helper would
// refuse it anyway on its own first compare [05 R-WORK-01 §3].
func padRepairsLander(lander, pad *units.Unit) bool {
	if lander == nil || lander.Def == nil || pad == nil || pad.Def == nil {
		return false
	}
	if int32(lander.Health) >= lander.Def.MaxDamage {
		return false
	}
	if !pad.Def.IsAirBase || !pad.Def.Builder {
		return false
	}
	return pad.Remaining == 0 // not under construction [05 "Construction target state"]
}

// installOffMapRecoveryMarker is the shared off-map recovery leg run before
// the executor's phase switch [04 R-AIR-01 §5]: a point marker 800 world units
// toward the map centre, with horizontal arrival radius 0x80 and gate 0xE0.
func (s *System) installOffMapRecoveryMarker(u *units.Unit, head *orders.Node) bool {
	if !s.airOffMap(u) {
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
// candidate position [04 R-AIR-01 §6a].
//
// It is a dedicated routine, not the mover's commit validator reused: one
// strict slope maximum with no second water-slope tier, plus an occupancy rule
// and an aircraft water rule the commit validator does not carry.
//
// The coarse early accept runs before the walk: when the mapping word for the
// tile under the anchor-plus-quarter-footprint point does NOT carry the
// aircraft owner's slot bit, the position is landable outright, with no feature,
// yard, occupancy, depth or slope test at all [04 R-AIR-01 §6a] as corrected by
// [04 R-AIR-01 §14.2] — the grid is the per-player mapping word grid of
// [03 R-LAYER §1], not a movement-class blocking map, and the bit is the
// owner's slot bit, not a class shift. An aircraft sent over ground its owner
// has never had sight of therefore lands blind.
//
// The grid is bound as of WU-19-100 — the order queue binding's world adapter
// carries the visibility service's word grid as MappingWord, and
// landingMappingWord below reads it. A unit whose queue carries no binding, or
// a composition with no visibility service, still answers "no grid" and runs
// the full walk; that fallback is stricter than retail (retail accepts on the
// coarse bit alone, including on a cell another unit occupies) and bounded — a
// refusal only makes the caller keep searching, and the ground-landing machine
// has its repeated-failure fallback.
func (s *System) landable(u *units.Unit, x, z numeric.Fixed) bool {
	if s == nil || u == nil || u.Def == nil || s.Terrain == nil {
		return false
	}
	coll := handleRow(s.Collisions, u.Handle)
	if coll == nil {
		return false
	}
	fx, fz := coll.FootPrintX, coll.FootPrintZ
	anchorX, anchorZ := world.PlacementAnchor(x, z, int32(fx), int32(fz))
	anchor := Cell{X: anchorX, Z: anchorZ}
	if anchor.X < 0 || anchor.Z < 0 {
		return false
	}
	if anchor.X+int32(fx) >= s.Terrain.CellW || anchor.Z+int32(fz) >= s.Terrain.CellH {
		return false
	}
	// The coarse early accept, before any per-cell work
	// [04 R-AIR-01 §6a][04 R-AIR-01 §14.2].
	if word, ok := s.landingMappingWord(u, anchor.X, anchor.Z, fx); ok && !airMappingBitSet(word, u.Owner) {
		return true
	}

	profile := s.ProfileFor(u.Handle)
	sea := int32(s.Terrain.SeaLevel)
	depthFloor := sea - profile.MaxWaterDepth
	depthCeil := sea - profile.MinWaterDepth
	// The aircraft water rule: for a can-fly definition that is not amphibious
	// the floor is raised to sea level, so every cell under the footprint must
	// be at or above it. A non-amphibious aircraft cannot set down on water
	// however shallow, whatever its authored movement class allows
	// [04 R-AIR-01 §6a]. This is the rule that governs where an idle aircraft
	// may park, and the placeholder predicate had nothing like it.
	if depthFloor < sea && u.Def.CanFly && !u.Def.Amphibious {
		depthFloor = sea
	}

	for dz := int16(0); dz < fz; dz++ {
		for dx := int16(0); dx < fx; dx++ {
			cx, cz := anchor.X+int32(dx), anchor.Z+int32(dz)
			if isFeatureBlocked(s.Terrain, cx, cz) {
				return false
			}
			cell := s.Terrain.PlotAt(cx, cz)
			if cell == nil {
				return false
			}
			// A finished building's yard is not landable ground
			// [04 R-COLL-01 §4].
			if cell.StructureYard() {
				return false
			}
			// Landing reads both planes: another aircraft over this cell must
			// make us search elsewhere before descent, even though it holds no
			// ground word yet. Self never blocks either plane [04 R-AIR-01 §6a].
			if s.Grid != nil {
				for _, plane := range [...]Plane{PlaneGround, PlaneAir} {
					if occ, ok := s.Grid.OccupantAtPlane(plane, Cell{X: cx, Z: cz}); ok && occ != coll.ID {
						return false
					}
				}
			}
			lo, hi := int32(cell.MinHeight()), int32(cell.MaxHeight())
			if lo < depthFloor || hi > depthCeil {
				return false
			}
			if hi-lo > int32(profile.MaxSlope) {
				return false
			}
		}
	}
	return true
}

// airMappingBitSet reports whether a mapping word carries a player slot's bit.
// The grid holds one 16-bit word per 2×2-cell tile with bits 0..9 one per
// player slot [03 R-LAYER §1]; the landing test reads the AIRCRAFT OWNER's bit
// [04 R-AIR-01 §14.2], the same byte [04 R-ORD-02 §1]'s own-unit test compares
// with the local slot.
func airMappingBitSet(word uint16, owner uint8) bool {
	if owner > 9 {
		return false // only ten usable bits exist [03 R-LAYER §1]
	}
	return word&(1<<owner) != 0
}

// airMappingTile is the tile the landing test's index arithmetic names in the
// mapping word grid [04 R-AIR-01 §6a]:
//
//	i = (cellX >> 1) + (fx >> 2) + ((cellZ >> 1) + (fx >> 2)) · stride
//
// with stride the grid's tile width, so the tile pair is
// `((cellX >> 1) + (fx >> 2), (cellZ >> 1) + (fx >> 2))`. Note that BOTH terms
// add `fx >> 2`: the Z term does not use `fz`. That asymmetry is what the
// routine computes, not a transcription slip [04 R-AIR-01 §6a].
//
// The stride multiply belongs to whoever holds the grid, so the pair is what
// crosses the port; the holder forms the same flat index, which keeps retail's
// row wrap for a tile column past the stride.
func airMappingTile(cellX, cellZ int32, fx int16) (tileX, tileZ int32) {
	q := int32(fx >> 2)
	return cellX>>1 + q, cellZ>>1 + q
}

// landingMappingWord is the mapping-word-grid read the coarse early accept of
// [04 R-AIR-01 §6a] performs, at the tile airMappingTile gives.
//
// The grid is the visibility service's per-player mapping word grid
// ([03 R-LAYER §1]; [04 R-AIR-01 §14.2] identifies it as the one the landing
// test reads). internal/movement holds no visibility handle, so it reads the
// grid through the order queue binding's world adapter — the same route
// diplomacyRows takes to the alliance rows. It is deliberately NOT the class
// layer's owner/building mask: that in-package word array has no production
// writer at all, so reading it would early-accept everywhere and let an
// aircraft park on water — the exact rule [04 R-AIR-01 §6a] calls its
// substantive finding.
//
// "No grid" — a unit whose queue carries no binding, or a composition with no
// visibility service — makes landable run its full per-cell walk, the stricter
// and bounded fallback recorded on landable itself.
func (s *System) landingMappingWord(u *units.Unit, cellX, cellZ int32, fx int16) (uint16, bool) {
	if s == nil || u == nil {
		return 0, false
	}
	b := airBinding(u)
	if b == nil || b.World == nil || b.World.MappingWord == nil {
		return 0, false
	}
	tileX, tileZ := airMappingTile(cellX, cellZ, fx)
	return b.World.MappingWord(tileX, tileZ)
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

// stepAir follows the order-pump visit with the controller's per-tick hook,
// flight integration, position commit and movement-rate cache. Order phases
// and work markers are already committed by their owning visit [04 R-AIR-01 §1].
//
// The ground route follower is not on this path. It never was retail's — the
// flight integrator reads only the flight command block — and it was the source
// of the reported sideways and backwards flight, because it supplies no command
// heading at all.
func (s *System) stepAir(u *units.Unit, tick uint32) StepResult {
	// The pump-driven air executors below reach the order pump through the
	// queue binding, and the queue binding is NOT this function's business.
	// Session composition installs it: internal/session's bindOrderQueue at
	// creation, bindExistingOrderQueues at battle entry, and
	// bindExistingOrderQueue immediately before every PumpUnit call in phase 5
	// — the only place the binding is read. This used to call
	// BindAirOrderLegs here on the belief that "rebinding is a pointer write,
	// so doing it here costs nothing". It is not a pointer write: it is a
	// sweep of the whole unit pool, once per aircraft per tick, and it was 53%
	// of authoritative tick time and half of all bytes allocated
	// (docs/SIM_BENCHMARK.md).
	handle := u.Handle
	fl := handleRow(s.Flights, handle)
	if fl == nil {
		d := s.distToGoal(u)
		s.emitMovementCallbacks(u, 0)
		return StepResult{Handle: handle, DistToGoal: d, EmptyRoute: true}
	}

	// Call 1 — the controller's per-tick hook: the six-step producer and the
	// integrator's single input fetch [04 R-AIR-01 §1].
	s.StepFlightCommand(u, nil, s.AirSectors)

	// Arrival belongs to the record's payload. This returned diagnostic is a
	// same-visit sample, never a second latch that can restart an executor.
	arrived := false
	if c := fl.Command; c != nil {
		arrived = c.Payload == nil || c.Payload.Arrived(u)
	}

	// Call 2 — the flight integrator. Its arithmetic is [04 §10.1] and is not
	// touched here; only its inputs are.
	fl.Mode = u.Move.Mode & 0x3
	fl.X = int32(u.X.Raw())
	fl.Y = int32(u.Y.Raw())
	fl.Z = int32(u.Z.Raw())
	fl.Heading = u.Move.Heading
	if u.Def != nil {
		fl.MaxVelocity = int32(u.Def.MaxVelocity)
		fl.Acceleration = int32(u.Def.Acceleration)
		fl.BrakeRate = int32(u.Def.BrakeRate)
		fl.TurnRate = int32(u.Def.TurnRate)
	}
	oldX, oldZ := int64(u.X), int64(u.Z)
	IntegrateFlight(fl) // [04 §10.1] C26–C30

	// Call 3 — the ordinary position/mode commit validates the integrated
	// proposal before publishing the mirror and occupancy [04 R-COLL-01 §1].
	s.commitFlightState(u, fl)
	blocked := false
	if coll := handleRow(s.Collisions, handle); coll != nil {
		blocked = coll.Blocked
	}
	// The mover's VELOCITY TRIPLE [04 R-MOV-01 §1]. The flight integrator owns
	// all three components on this path — the decay, the brake shaping, the
	// vertical clamp and the horizontal acceleration each write them — and it
	// assigns an exact zero triple for any mode other than airborne
	// [04 §10.1][04 R-AIR-01 §1], so a landed aircraft publishes zero here on
	// its very next tick without needing a second writer.

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
	route := handleRow(s.Routes, handle)
	hasRoute := route != nil && route.Active
	return StepResult{
		Handle:     handle,
		DistToGoal: s.distToGoal(u),
		HasRoute:   hasRoute,
		EmptyRoute: !hasRoute,
		Moved:      int64(u.X) != oldX || int64(u.Z) != oldZ,
		Arrived:    arrived,
		Blocked:    blocked,
	}
}

// commitFlightState is the single flight commit owner for shared mover words.
// It publishes the live integrator triple, scalar speed, residual and lean
// record before callbacks or save readers can observe the unit [04 §10.1]
// [04 R-AIR-01 §1][08 R-SAVE-02 §8].
func (s *System) commitFlightState(u *units.Unit, fl *FlightState) {
	if s == nil || u == nil || fl == nil {
		return
	}
	if coll := handleRow(s.Collisions, u.Handle); coll != nil {
		// Flight has already integrated XYZ. Reconstruct the old horizontal
		// position so CommitOne consumes that proposal exactly once; its Y
		// input is the proposed Y, retained even on refusal [04 R-COLL-01 §1].
		coll.X, coll.Y, coll.Z = fl.X-fl.VX, fl.Y, fl.Z-fl.VZ
		coll.VX, coll.VY, coll.VZ = fl.VX, fl.VY, fl.VZ
		coll.Speed, coll.Heading = fl.Speed, fl.Heading
		coll.Mode = u.Move.Mode & 3
		coll.MaxVelocity = fl.MaxVelocity
		coll.LeanX, coll.LeanY, coll.LeanZ = fl.LeanX, fl.LeanY, fl.LeanZ
		coll.TurnResidual = fl.TurnResidual
		if fl.VX != 0 || fl.VY != 0 || fl.VZ != 0 || coll.Mode != coll.CachedMode {
			coll.LastProposalTick = s.tick
			anchor := coll.ProposedAnchor(coll.Mode)
			inBounds := commitRectInBounds(s.Terrain, anchor, coll.FootPrintX, coll.FootPrintZ)
			profile := s.ProfileFor(u.Handle)
			blockerID := -1
			perCell := func(c Cell) bool {
				// Bounds precede the mode dispatch. Only airborne proposals
				// pass off map; on map only mode 1 scans [04 R-COLL-01 §2].
				if !inBounds {
					return coll.Mode == 2
				}
				if coll.Mode != 1 {
					return true
				}
				if s.Terrain != nil && !profile.IsPassableCommitCell(s.Terrain, c.X, c.Z) {
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
			clearedAnchor, clearedMode := coll.CachedAnchor, coll.CachedMode
			fast, blocked := coll.CommitOne(s.Grid, coll.Mode, perCell, nil)
			coll.BlockerID = blockerID
			if !fast && !blocked {
				if clearedMode == 1 {
					s.noteFootprintClear(u.Handle, clearedAnchor, coll.FootPrintX, coll.FootPrintZ, true)
				}
				s.noteOccupancyCommit(u.Handle, s.tick)
			}
		}
		fl.X, fl.Y, fl.Z = coll.X, coll.Y, coll.Z
		fl.VX, fl.VY, fl.VZ, fl.Speed = coll.VX, coll.VY, coll.VZ, coll.Speed
		fl.ModeMirror = coll.CachedMode
		fl.Dirty = fl.Dirty || coll.Dirty
		u.Move.ModeMirror = coll.CachedMode
	}
	u.X, u.Y, u.Z = numeric.Fixed(int64(fl.X)), numeric.Fixed(int64(fl.Y)), numeric.Fixed(int64(fl.Z))
	u.Move.Heading = fl.Heading
	u.Move.Speed = numeric.Fixed(int64(fl.Speed))
	u.Move.VelX, u.Move.VelY, u.Move.VelZ = numeric.Fixed(int64(fl.VX)), numeric.Fixed(int64(fl.VY)), numeric.Fixed(int64(fl.VZ))
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
// Air executors, including move, standby and both landing families, run
// through the order pump. Construction calls its approach legs from the
// same persisted order-phase owner.
// internal/orders has a handler for each, that handler runs the record-side entry sequence, and it
// then calls into this package for the leg. The pump therefore stays the sole
// dispatcher — it clears the record's dynamic gate before every dispatch and
// applies the leg's own result code afterwards [04 §3.3] — while the legs stay
// where the air marker family of [04 R-AIR-01 §4] lives.
//
// A leg reads and writes the record directly: the phase byte is the pump's
// (code 1 advances it, code 0 resets it), the dynamic gate and the deadline are
// the leg's own, and the arrival bits it waits on are the ones the flight
// command producer raises on that record [04 R-AIR-01 §1] step 6.

// BindAirOrderLegs is retained for callers that explicitly initialize movement
// before session composition. It installs this system's runner on the existing
// unit queues only; the runner remains queue-local and there is no process-wide
// dispatch state [04 §3.3]. Session composition performs the same binding for
// queues created after this initialization point.
func (s *System) BindAirOrderLegs() {
	if s == nil || s.world == nil {
		return
	}
	runner := s.AirLegRunner()
	for _, u := range s.world.Iter() {
		q := orders.QueueOfUnit(u)
		if q == nil {
			continue
		}
		binding := q.Binding()
		if binding == nil {
			binding = &orders.QueueBinding{}
			q.SetBinding(binding)
		}
		if binding.Movement == nil {
			binding.Movement = &orders.MovementGoalAdapter{}
		}
		binding.Movement.RunAir = runner
		s.RegisterOrderHandlers(q)
	}
}

// Standby's body belongs to movement because it constructs air markers, but
// its record lifecycle belongs to the ordinary queue pump [04 R-AIR-01 §7]
// [04 §3.3]. Resolve its immutable descriptor once for queue registration.
var airStandbyRowID = orders.Lookup("VTOL_Standby")

// RegisterOrderHandlers binds movement-owned record bodies before each queue
// can be pumped, including a standby newly created by the idle refill.
func (s *System) RegisterOrderHandlers(q *orders.Queue) {
	if s == nil || q == nil {
		return
	}
	if s.airLegHandler == nil {
		s.airLegHandler = s.runAirOrderLeg
	}
	q.SetOwnedHandler(airStandbyRowID, s.airLegHandler)
}

// AirLegRunner returns this system's executor for installation on a
// session-owned queue binding.
func (s *System) AirLegRunner() orders.AirLegRunner {
	if s == nil {
		return nil
	}
	return s.runAirOrderLeg
}

// runAirOrderLeg is the orders.AirLegRunner this system registers. It declines
// a record whose unit is not one of ours, so two systems sharing the binding
// cannot drive each other's aircraft.
func (s *System) runAirOrderLeg(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
	if s == nil || u == nil || n == nil || s.world == nil || s.world.Unit(u.Handle) != u {
		return 0, false
	}
	switch orders.DescriptorFor(n.ID).Name {
	case "VTOL_Move":
		// The order handler owns captions, slot inhibition and completion;
		// these two phase-local movement writes share its persisted phase
		// [04 R-ORD-02 §2][08 R-SAVE-ORDER-01].
		switch n.Phase {
		case 0:
			if !s.airMoverReady(u) {
				return 7, true
			}
			s.takeoffPreamble(u, n)
			return 1, true
		case 1:
			fx, fz := s.pathFootprint(u)
			x, z := snapToOwnFootprint(n.GoalX, n.GoalZ, fx, fz)
			s.installAirGoal(u, n, s.newPointMarker(u, Vec3{X: x, Y: n.GoalY, Z: z}))
			n.DynamicGate = airLegGate
			return 1, true
		}
		return 0, false
	case "VTOL_HelpBuild":
		if n.Phase == 3 {
			s.VisitAirBuildWork(u, n, tick)
		}
		return 0, false // the order handler continues with its work quantum
	case "VTOL_Standby":
		return s.legVTOLStandby(u, n, tick), true
	case "VTOL_Landing":
		return s.legVTOLLanding(u, n, satisfied, tick), true
	case "VTOL_LandIfCan":
		return s.legVTOLLandIfCan(u, n, satisfied, tick), true
	case "VTOL_Evade":
		return s.legVTOLEvade(u, n, tick), true
	case "VTOL_SeekAttack":
		return s.legVTOLSeekAttack(u, n, satisfied, tick), true
	case "VTOL_SeekGuard":
		return s.legVTOLSeekGuard(u, n, satisfied, tick), true
	case "VTOL_Follow":
		// The air guard is the one executor of the six that carries the off-map
		// recovery of [04 R-AIR-01 §5] whose remaining phases do NOT live here:
		// [04 R-ORD-02 §3]'s phases are the order package's guard handler, which
		// shares its four legs with `Follow_Ground`. Only the recovery marker
		// belongs to this package, so this case runs that leg alone and
		// otherwise DECLINES, letting the handler carry on with its own phase
		// switch — which is what "run it before the phase switch and return
		// from it immediately" needs and nothing more.
		if s.installOffMapRecoveryMarker(u, n) {
			return 2, true // *hold* — result code 2 [04 R-AIR-01 §5]
		}
		return 0, false
	case "AirStrike":
		return s.legAirStrike(u, n, satisfied, tick), true
	case "AirToGround":
		return s.legAirToGround(u, n, tick), true
	case "AirToGroundHover":
		return s.legAirToGroundHover(u, n, tick), true
	case "AirToAir":
		return s.legAirToAir(u, n, satisfied, tick), true
	case "VTOL_Pickup":
		// The air transport pair [04 §10.2]. Both legs live in transport.go
		// beside the cargo helpers they call, and reach the pump through this
		// one runner like every other pump-driven air executor, because every
		// command their phase tables queue is an air path marker of
		// [04 R-AIR-01 §4].
		return s.legVTOLPickup(u, n, satisfied, tick), true
	case "VTOL_Unload":
		return s.legVTOLUnload(u, n, satisfied, tick), true
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
	return s != nil && u != nil && u.Def != nil && u.Def.CanFly && handleRow(s.Flights, u.Handle) != nil
}

// orderWeapons returns the session-owned weapon port for this aircraft. Air
// legs run after the order pump, while the combat pipeline runs at the next
// unit phase; these calls therefore only latch authoritative slot state and
// never allocate a presentation-only shot [04 R-AIR-01 §8][06 §3.3].
func orderWeapons(u *units.Unit) *orders.WeaponAdapter {
	if u == nil {
		return nil
	}
	q := orders.QueueOfUnit(u)
	if q == nil || q.Binding() == nil {
		return nil
	}
	return q.Binding().Weapons
}

// Attack preparation inhibits slots without installing a target. Binding is a
// later, primary-slot operation [04 R-AIR-01 §8][04 R-ORD-01 §7].
func inhibitAirWeapons(u *units.Unit) {
	w := orderWeapons(u)
	if w == nil || w.InhibitSlot == nil {
		return
	}
	for idx := 0; idx < units.NumSlots; idx++ {
		w.InhibitSlot(u, idx)
	}
}

func releaseWeapon(u *units.Unit, idx int) bool {
	w := orderWeapons(u)
	return w != nil && w.ReleaseSlot != nil && w.ReleaseSlot(u, idx)
}

func firePrimaryTarget(u *units.Unit, target pool.Handle, tick uint32) bool {
	w := orderWeapons(u)
	return w != nil && w.FireTarget != nil && target != 0 && w.FireTarget(u, 0, target, tick)
}

func firePrimaryPoint(u *units.Unit, x, z numeric.Fixed, tick uint32) bool {
	w := orderWeapons(u)
	return w != nil && w.FirePoint != nil && w.FirePoint(u, 0, x, z, tick)
}

func stopPrimaryWeapon(u *units.Unit) {
	if w := orderWeapons(u); w != nil && w.StopFiring != nil {
		w.StopFiring(u, 0)
	}
}

// weaponCanEngage is the shot-admission gate of [04 R-ORD-01 §7] asked
// through the queue's weapon adapter: may slot idx be pointed at target right
// now. `AirToGroundHover` phase 3 counts its refusals [04 R-AIR-01 §8].
func weaponCanEngage(u *units.Unit, target pool.Handle, idx int) bool {
	w := orderWeapons(u)
	return w != nil && w.CanEngage != nil && target != 0 && w.CanEngage(u, target, idx)
}

// airOffMap reports whether the unit's air-sector link is the off-map sentinel
// record [04 R-AIR-01 §5], the condition every executor's step-4 arm tests.
// With no terrain bound there is no grid to be off, and the condition is
// false — the same answer installOffMapRecoveryMarker gives.
func (s *System) airOffMap(u *units.Unit) bool {
	if s == nil || s.Terrain == nil {
		return false
	}
	_, linked := s.airSectorHeight(u)
	return !linked
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

// airBelowThreeQuarters is the health test the six base-seeking air legs share
// [04 R-AIR-01 §7][04 R-AIR-01 §8][04 R-ORD-02 §3]. The expression itself lives
// in internal/combat beside the list it gates, so the air legs and the two
// patrol rows of [04 R-ORD-02 §2] and [04 R-ORD-01 §7] share one spelling of it
// [04 R-AIR-01 §11].
func airBelowThreeQuarters(u *units.Unit) bool { return combat.AirBelowThreeQuarters(u) }

// airBaseCandidates is the "collect the base candidates within `0xF00` for my
// side" scan of [04 R-AIR-01 §11], which `VTOL_SeekAttack` phase 1,
// `VTOL_SeekGuard` phase 1, `AirStrike` phase 6, `AirToGroundHover` phase 3,
// `VTOL_Patrol` phase 2, `VTOL_RepairPatrol` phase 1 and `AirToGround` phase 4
// run when the aircraft is below three quarters health, and whose non-empty
// result pushes a `VTOL_Landing` order at one candidate drawn from it.
//
// It is not a sector visitor: the candidate set is the per-side target
// registry's **third list** [06 §3.1 "the third list"], held on this system and
// refilled on the registry's 30-tick cadence by BeginTick. This is its filter —
// re-test the three admission flags but not liveness, admit at planar
// `(dx² >> 32) + (dz² >> 32) <= 0xF00²` inclusive, push in list order.
//
// Reading the snapshot rather than the live world is the behavior, not a
// shortcut: a pad that died inside the window is still offered here and is
// rejected by the landing order's own pad query [04 R-AIR-01 §6], and a pad
// that finished building inside the window is not offered until the next
// rebuild.
func (s *System) airBaseCandidates(u *units.Unit) []pool.Handle {
	if s == nil || u == nil {
		return nil
	}
	list := s.airBases.List(u.Owner)
	if len(list) == 0 {
		return nil
	}
	return combat.ScanAirBaseList(u.X, u.Z, list, s.airUnitLookup(u))
}

// airUnitLookup resolves a third-list handle back to its unit for the scan's
// flag and distance re-tests. The bound units world is the direct source; a
// system driven only through the order binding falls back to the binding's own
// lookup.
func (s *System) airUnitLookup(u *units.Unit) func(pool.Handle) *units.Unit {
	if s != nil && s.world != nil {
		return s.world.Unit
	}
	b := airBinding(u)
	if b == nil || b.Lookup == nil {
		return nil
	}
	return b.Lookup
}

// airBinding is the session-owned order binding for this aircraft, the same
// seam orderWeapons and simRNG reach through.
func airBinding(u *units.Unit) *orders.QueueBinding {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return nil
	}
	return q.Binding()
}

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

// airSpawnIDAtHead is airSpawnAtHead for a record the caller has already
// resolved to an ID — the guard seek spawns "the result whatever it is"
// [04 R-ORD-02 §3], so it never names the order.
func airSpawnIDAtHead(u *units.Unit, id orders.ID, target pool.Handle, goal Vec3, tick uint32) {
	q := orders.QueueForUnit(u)
	if q == nil || id == 0 {
		return
	}
	q.PushHead(id, orders.NewNodeForOrder(id, target, goal.X, goal.Y, goal.Z, tick, u.Handle, false))
}

// airGuardVisitorAdmits is the guard-candidate visitor of [04 R-ORD-02 §4],
// with the polarity [04 R-AIR-01 §14.4] established. It admits `cand` when all
// three clauses hold:
//
//   - the candidate OWNER's alliance row A, indexed by the SEEKER's owner slot,
//     is nonzero — row A is that owner's own one-directional declaration
//     ([05 R-SHARE-01 §1]), nonzero for a declared ally and for the owner
//     itself, whose self entry is seeded to one;
//   - the candidate is not `canfly`;
//   - the candidate is not the seeker.
//
// `declares` is the row read; with none bound only the seeker's own side is
// admitted, which is the self entry alone.
func airGuardVisitorAdmits(seeker, cand *units.Unit, declares func(from, toward uint8) bool) bool {
	if seeker == nil || cand == nil || cand.Def == nil {
		return false
	}
	if cand == seeker || cand.Handle == seeker.Handle {
		return false // not the seeker [04 R-ORD-02 §4]
	}
	if cand.Def.CanFly {
		return false // not `canfly` [04 R-ORD-02 §4]
	}
	if cand.Owner == seeker.Owner {
		return true // the row's self entry is seeded to one [04 R-AIR-01 §14.4]
	}
	if declares == nil {
		return false
	}
	return declares(cand.Owner, seeker.Owner)
}

// airGuardCandidate is `VTOL_SeekGuard` phase 1's enumeration: the units within
// the seeker's `sightdistance`, filtered by the visitor, of which only the
// first listed is used [04 R-ORD-02 §3]. The pool is walked slot-ascending, the
// project's one deterministic unit order (I1), and the range test is the
// established truncated whole-world-unit planar metric [06 §3.1].
func (s *System) airGuardCandidate(u *units.Unit) *units.Unit {
	if s == nil || u == nil || u.Def == nil || s.world == nil {
		return nil
	}
	declares := s.diplomacyRows()
	for _, cand := range s.world.Iter() {
		if !airGuardVisitorAdmits(u, cand, declares) {
			continue
		}
		if !combat.WithinRange(u.X, u.Z, cand.X, cand.Z, u.Def.SightDistance) {
			continue
		}
		return cand
	}
	return nil
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
// The scratch word §8 leaves unnamed is p1: [04 R-AIR-01 §14.3]'s census over
// the four combat executors and this one has `VTOL_Evade` drawing `random
// below 2` into p1 and re-reading p1 in phase 1, with p2 untouched.
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
// This is not an open retail question and carries no marker: [04 R-AIR-01 §7]
// and [§8] give every one of these draws as unconditional, so retail has no
// "no stream" arm at all — the stream is the session's, and a bound queue
// always has it. The arm exists only because simRNG can answer nil for a queue
// this build has not bound yet. It holds the record at the head for one tick
// [04 R-ORD-01 §1] rather than consuming a draw that does not exist or
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
//	issues autonomous attack work and, on success, clears the gate and returns
//	0; refusal advances without orbit initialization or takeoff. With
//	no target it defaults the cached goal to the unit's position if that goal is
//	exactly (0,0,0), draws one full-circle bearing (random below 0x10000),
//	stores it and its low bit, and runs the shared takeoff preamble.
//
//	Phase 1, in this order: inhibit all three weapon slots; if
//	health is below three quarters of MaxDamage, collect the nearby-unit
//	candidate list within 0xF00 and, if it is non-empty, clear the goal payload,
//	draw one random index, push a VTOL_Landing order at that candidate, clear
//	the gate word and return 0; then ask the ordinary acquisition for a target
//	and return 5 if an attack is issued; then, if the arrival bits 0xE0 are set,
//	advance the search bearing by −(0x5555 + random below 0x2000); finally build
//	a point marker at the cached goal offset by the search bearing at radius
//	firstWeaponRange + 0xA0, horizontal arrival radius 0x80, install, set the
//	deadline to the current tick plus 30 + random below 30, OR 0xE0 into the
//	gate word, and return 2.
//
// Weapon-facing latch and acquisition calls use the queue binding; the fallback
// arms below remain the established no-target search behavior.
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
		if n.Target != 0 {
			if orders.AutonomousEngage(u, s.unitFor(n.Target)) {
				n.DynamicGate = 0
				return 0 // restart at the inserted attack [04 R-AIR-01 §7]
			}
			return 1 // refusal skips the targetless initializer [04 R-AIR-01 §7]
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
		return 1
	case 1:
		if sim == nil {
			return airLegUnbound(n, tick)
		}
		// Inhibit before repair and acquisition; the slot-control verb owns
		// the guarded target clear [04 R-AIR-01 §7][04 R-ORD-01 §7].
		if weapons := orderWeapons(u); weapons != nil && weapons.InhibitSlot != nil {
			for slot := 0; slot < units.NumSlots; slot++ {
				weapons.InhibitSlot(u, slot)
			}
		}
		if airBelowThreeQuarters(u) && s.airFindBaseAndLand(u, n, sim, tick) {
			return 0 // *restart* [04 R-AIR-01 §7][04 R-AIR-01 §11]
		}
		if orders.AutonomousAcquire(u) {
			return 5 // acquisition succeeds and the seek order completes
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
// The guard-candidate visitor's diplomacy clause is settled by
// [04 R-AIR-01 §14.4]: the byte is the CANDIDATE owner's alliance row A
// ([05 R-SHARE-01 §1], that owner's own declaration) indexed by the SEEKER's
// owner slot, and the visitor admits on nonzero — the same shape
// [04 R-ORD-01 §3]'s combat-join correction established for the ground guard,
// read from the candidate's side. A seeking guard therefore attaches itself to
// its own side's units and its allies', never to an enemy. See
// airGuardVisitorAdmits.
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
		if airBelowThreeQuarters(u) && s.airFindBaseAndLand(u, n, sim, tick) {
			return 0 // *restart* [04 R-ORD-02 §3][04 R-AIR-01 §11]
		}
		// The guard-candidate enumeration: non-empty → release the payload,
		// resolve code 7 against the FIRST listed unit and spawn the result at
		// the head whatever it is, gate = 0, wait [04 R-ORD-02 §3].
		if cand := s.airGuardCandidate(u); cand != nil {
			s.ReleaseGoalPayload(n)
			if id := orders.Resolve(7, u, cand, nil); id != 0 {
				airSpawnIDAtHead(u, id, cand.Handle, Vec3{X: cand.X, Y: cand.Y, Z: cand.Z}, tick)
			}
			n.DynamicGate = 0
			return 3 // *wait* — the pump's `30 + random below 15` [04 R-ORD-02 §3]
		}
		// With an empty list the leg is the orbit step of `VTOL_Follow` leg 4,
		// about the record's goal rather than a ward, with the radius read from
		// slot 0 unconditionally [04 R-ORD-02 §3].
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

// airGravityWord reads the original OTA integer [04 R-AIR-01 §8]. Reversing
// the terrain acceleration's truncated conversion loses precision, and the
// terrain's legacy/negative fallback does not apply to this separate reader.
func (s *System) airGravityWord() int64 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return int64(s.Terrain.OTAGravity)
}

// airReleaseLead is the bombing run's ballistic release lead
// [04 R-AIR-01 §8] phase 4: `t = sqrt((2 · cruisealt) / gravity)`,
// `lead = trunc(t · 30.0 · speedInteger)`, where `speedInteger` is the signed
// 16-bit integer part of the mover's scalar speed word. The `30.0` converts the
// free-fall time from seconds to ticks against a per-tick speed, so the whole
// expression is `speed_per_tick · 30 · sqrt(2·cruisealt/gravity)` world units.
//
// Retail evaluates the square root and product in double precision; the final
// conversion truncates toward zero [01 §8][I3].
func airReleaseLead(u *units.Unit, gravity int64) int64 {
	if u == nil || u.Def == nil || gravity == 0 {
		return 0
	}
	cruise := float64(u.Def.CruiseAlt)
	speed := float64(int16(u.Move.Speed.Raw() >> 16))
	return int64(numeric.TruncateFloat64ToLow32(math.Sqrt((2.0*cruise)/float64(gravity)) * 30.0 * speed))
}

// legAirStrike is the bombing run, with the ballistic release lead of
// [04 R-AIR-01 §8]'s seven-row table. Phase 6 either loops back to phase 3 for
// another run or breaks off to land.
//
// Weapon slot operations are issued through the queue binding; combat consumes
// the resulting armed targets in its ordinary next unit phase.
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
		inhibitAirWeapons(u)
		releaseWeapon(u, 0)
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
		// Release slot 0's latch before issuing the fire-at-point command. The
		// combat unit phase consumes this armed target on its next visit.
		releaseWeapon(u, 0)
		// The bomber's release row always orders a point shot at the cached
		// goal, even when that cache came from a live target [04 R-AIR-01 §8].
		firePrimaryPoint(u, n.GoalX, n.GoalZ, tick)
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
		stopPrimaryWeapon(u)
		ox, oz := offsetAtBearing(u.Move.Heading, numeric.Fixed(0x5A00000)) // 1440 world units
		m := s.newPointMarker(u, Vec3{X: u.X - ox, Y: u.Y, Z: u.Z - oz})
		m.setArrivalRadius(0x80)
		s.installAirGoal(u, n, m)
		n.DynamicGate = airLegGateReposition
		if !airBelowThreeQuarters(u) {
			n.Phase = 3
			return 2 // fly another run
		}
		s.airFindBaseAndLand(u, n, sim, tick)
		return 0 // *restart*, with or without candidates [04 R-AIR-01 §8][04 R-AIR-01 §11]
	default:
		return 7 // *cancel-all* [04 R-ORD-02 §3]
	}
}

// --- AirToGround and AirToGroundHover [04 R-AIR-01 §8] ---

// airFindBaseAndLand is the "find a base and land" branch `AirStrike` phase 6,
// `AirToGround` phase 4, `AirToGroundHover` phase 3, `VTOL_SeekAttack` phase 1
// and `VTOL_SeekGuard` phase 1 share: collect the third-list candidates within
// 0xF00, and if any
// exist clear the goal payload, draw one random index, push a `VTOL_Landing`
// order at that candidate and clear the gate word. It reports whether it took
// the branch [04 R-AIR-01 §11].
//
// The draw is one simulation `RNG(count)` and is taken **only** on a non-empty
// list, so an aircraft with no pad in reach advances no random state; a count
// of one draws nothing at all [01 §7.1][I4]. A caller whose list is empty
// continues in the same visit — none of them treats "no pad" as an outcome.
//
// The payload clear is the record-level release helper of [04 R-ORD-01 §1],
// ReleaseGoalPayload — the seam WU-19-62 landed — so the release runs the
// identity test and raises pending `0x80` on the record that owned the payload,
// exactly as the arrival and teardown releases do.
func (s *System) airFindBaseAndLand(u *units.Unit, n *orders.Node, sim *rng.Simulation, tick uint32) bool {
	if sim == nil {
		return false
	}
	bases := s.airBaseCandidates(u)
	if len(bases) == 0 {
		return false
	}
	s.ReleaseGoalPayload(n)
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
//	4 — health below three quarters → the find-a-base-and-land branch, which
//	    *restarts* only when it found a pad; with no pad in reach, and when the
//	    aircraft is healthy, the break leg:
//	    draw `random below 2` and add `0xC000` on 0 or `0x4000` otherwise
//	    from the unit's own heading, at radius `Range`, horizontal arrival radius
//	    0x80; gate = 0x100EA.
//	5 — set the phase to 2 and return 2, closing the loop.
//
// The phase 1 latch and phase 2 release/fire order are issued through the queue
// binding; combat owns the subsequent admission and fire.
func (s *System) legAirToGround(u *units.Unit, n *orders.Node, tick uint32) orders.Code {
	// Step 4 of the shared entry sequence, the `AirToGround` arm: this
	// executor "does NOT build a recovery marker: it sets the record's
	// deadline to the current tick plus 30, forces its phase to 2, and falls
	// through into its ordinary phase switch" [04 R-AIR-01 §5][04 R-AIR-01
	// §8]. Phase 2 then re-aims and installs the Range-radius marker on the
	// cached goal, so a strafer that overran the map edge on its fly-through
	// turns straight back onto the target instead of detouring 800 world
	// units toward the map centre.
	//
	// Corrected 2026-09-04 (WU-19-226): this took the six-executor recovery
	// leg — a marker toward the map centre, gate 0xE0, hold — which the
	// research assigns to `AirToAir` and `AirToGroundHover` and denies to
	// this one. With a fly-through of three weapon ranges beyond the target
	// [04 R-AIR-01 §8] a fighter leaves most maps on most runs, and each
	// detour was several hundred ticks in which it neither faced nor fired
	// at the target; the play-test read it as "won't attack the ground".
	if s.airOffMap(u) {
		airDeadline(n, tick, 30)
		n.Phase = 2
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
		inhibitAirWeapons(u)
		s.airJitteredApproach(u, n, sim, 0x80)
		n.DynamicGate = airLegGateStrike
		return 1
	case 2:
		releaseWeapon(u, 0)
		if n.Target != 0 {
			firePrimaryTarget(u, n.Target, tick)
		} else {
			firePrimaryPoint(u, n.GoalX, n.GoalZ, tick)
		}
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
		// Below three quarters health this leg is an ordinary pad-seeking
		// caller, exactly like `AirStrike` phase 6 and `VTOL_Patrol` phase 2:
		// it collects the base candidates within 0xF00 and, when any exist,
		// releases the goal payload, draws one bounded value over the count,
		// head-inserts a `VTOL_Landing` at the drawn pad, clears the gate word
		// and *restarts*. With NO candidates it falls into the break leg below
		// and returns 1 — the health test alone never ends the visit
		// [04 R-AIR-01 §8][04 R-AIR-01 §11].
		if airBelowThreeQuarters(u) && s.airFindBaseAndLand(u, n, sim, tick) {
			return 0 // *restart* behind the spawned landing order
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
// which retail builds and then never installs, so it commands nothing.
//
// The weapon layer supplies the engagement result; a failed query increments
// the miss counter before selecting the recovery orbit.
//
// The two scratch words §8 leaves unnamed are p1 and p2, and
// [04 R-AIR-01 §14.3] names which is which: phase 2 zeroes BOTH, phase 3 uses
// p2 as the miss counter (incremented on a refused engagement, reset to 0 when
// it exceeds 1) and p1 as the side flag (0 → subtract the quarter turn and
// write 1; 1 → add it and write 0). That is the assignment used below.
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
		inhibitAirWeapons(u)
		s.airJitteredApproach(u, n, sim, 0x80)
		n.DynamicGate = airLegGateStrike
		return 1
	case 2:
		releaseWeapon(u, 0)
		if n.Target != 0 {
			firePrimaryTarget(u, n.Target, tick)
		} else {
			firePrimaryPoint(u, targetX, targetZ, tick)
		}
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
		// "Ask the weapon layer whether the unit can engage the target"
		// [04 R-AIR-01 §8]: the question is the unit-to-unit shot-admission
		// gate of [06 §3.1] — the one the attack and guard handlers ask
		// before binding a slot [04 R-ORD-01 §7] — put to the RECORD's target
		// on slot 0. It is not a planar range test on whatever the slot
		// happens to hold. Corrected 2026-09-04 (WU-19-226).
		if !weaponCanEngage(u, n.Target, 0) {
			n.Param2++
		}
		if n.Param2 > 1 {
			n.Param2 = 0
			// Retail allocates a point marker at `targetPos − offset(bearing,
			// Range)` and sets its arrival radius, then returns without ever
			// INSTALLING it: this arm has no payload release and no install
			// call, where the alternation arm below has both. The marker is
			// dropped, so the aircraft keeps whatever payload it was already
			// bound to and is not sent anywhere new — only the draw, the
			// counter reset and the gate OR outlive the visit
			// [04 R-AIR-01 §8]. The draw is still spent, so the stream is
			// unaffected either way (I4); it is the only part of the arm the
			// dropped marker's arithmetic can be observed through.
			_ = sim.Uint32n(0x10000)
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
		// The marker is frozen ABOUT THE TARGET: while the target lives it
		// supplies `bearing(unit, target)` as the command heading inside the
		// producer's 320-world-unit consultation range and persists through
		// arrival [04 R-AIR-01 §4], which is what keeps the gunship facing
		// the target as it slides between its two standoff points.
		m := s.newFrozenTerrainPointMarker(u, n.Target, Vec3{X: targetX + ox, Y: targetY, Z: targetZ + oz})
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
	// Saved uninterpreted words remain attached to this object, not a later
	// replacement marker [08 R-SAVE-02 §10].
	savedFlags, savedAux, savedTrailing uint16
	sys                                 *System
	unit                                *units.Unit
	pos                                 Vec3
	vel                                 Vec3
	commanded                           uint16
	steer                               bool
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
	s.releaseRecordGoal(rec)
	s.detachControllerGoal(u.Handle)
	if rec != nil {
		rec.Satisfied &^= airGoalInstallClearMask
		s.storeRecordGoal(u.Handle, recordGoal{node: rec, air: p})
	}
	c.Payload = p
	c.payloadOwner = rec
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
//     intercept: position `targetPos + targetVelocity · 45`, velocity
//     `targetVelocity` plus the direction of the target's heading at half the
//     target's `MaxVelocity` — componentwise on X and Z, with Y the target's
//     own velocity Y; deadline `tick + 45`; gate `|= 0x100E8`; hold.
//   - otherwise the leg gives up: [04 R-ORD-02 §5] corrects §8's "re-issues a
//     seek order" — the traced arm releases the payload, spawns `VTOL_Evade`
//     with the same target at the head, zeroes the counter and the gate, and
//     returns *restart*.
//
// The payload's "steer to a commanded heading" flag stays CLEAR on both legs,
// and that is retail's own state, not a placeholder: [04 R-AIR-01 §14.5] finds
// the flag's single setter has no caller anywhere in the image — no executor
// leg, no constructor (the constructor zeroes the flag word), and no stream
// path, since the serializer emits the commanded heading only when the flag is
// set. The flag-gated branches §8 describes are therefore dead in play: the
// goal advances by its velocity unrotated and arrival is the 48-world-unit test
// alone. The machinery is kept because §8 documents it, not because anything
// arms it.
//
// The arm §8 omits — arrival bits clear, counter below 0x5A, range to the
// target AT OR BELOW 0xA0 — is the lead intercept's own tail minus the marker
// [04 R-AIR-01 §14.5]: no new payload is installed, whatever is bound stays
// bound; deadline `tick + 45`; gate `|= 0x100E8`; hold. The give-up arm of
// [04 R-ORD-02 §5] is reached from exactly two states, arrival bits set with a
// non-positive dot, or arrival bits clear with the counter at or above 0x5A.
//
// Target aiming is issued through the queue binding and consumed by combat's
// ordinary slot pipeline.
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
		// Dogfight preparation releases and binds only the primary slot after
		// inhibiting all slots [04 R-AIR-01 §8].
		inhibitAirWeapons(u)
		releaseWeapon(u, 0)
		firePrimaryTarget(u, n.Target, tick)
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
				tvx, tvy, tvz := airUnitVelocity(t)
				tHeading := uint16(0)
				tMax := int64(0)
				if t != nil {
					tHeading = t.Move.Heading
					if t.Def != nil {
						tMax = int64(t.Def.MaxVelocity)
					}
				}
				vx, vz := offsetAtBearing(tHeading, numeric.Fixed(tMax/2))
				// The commanded velocity is the TARGET'S OWN velocity triple
				// plus the direction of its heading at half its `MaxVelocity`:
				// the horizontal terms are summed componentwise and the
				// vertical one is the target's velocity Y, untouched by the
				// heading term. The marker advances its goal by this velocity
				// every tick, so dropping the target's own velocity made the
				// commanded lead point drift at the wrong rate whenever the
				// target was moving [04 R-AIR-01 §8]. The halving truncates
				// toward zero, as the signed halving in the image does.
				m := &airVelocityMarker{
					sys:  s,
					unit: u,
					pos:  Vec3{X: targetX + tvx*45, Y: targetY, Z: targetZ + tvz*45},
					vel:  Vec3{X: tvx - vx, Y: tvy, Z: tvz - vz},
				}
				s.installAirPayload(u, n, m)
				airDeadline(n, tick, 45)
				n.DynamicGate |= airLegGateStrike
				return 2
			}
			// Range at or below 0xA0: the lead intercept's tail without the
			// marker — no new payload, deadline tick+45, gate |= 0x100E8, hold
			// [04 R-AIR-01 §14.5].
			airDeadline(n, tick, 45)
			n.DynamicGate |= airLegGateStrike
			return 2
		}
		s.ReleaseGoalPayload(n)
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

// airUnitVelocity is the mover's VELOCITY TRIPLE [04 R-MOV-01 §1] — the words
// the lead intercept reads for both its position and its velocity term, not a
// reconstruction from heading and scalar speed. The two agree on the ground
// path, where the triple is exactly `−offset(heading, speed)` with a zero Y,
// and part company in flight, where the integrator writes all three
// [04 §10.1][04 R-AIR-01 §1].
func airUnitVelocity(t *units.Unit) (x, y, z numeric.Fixed) {
	if t == nil {
		return 0, 0, 0
	}
	return t.Move.VelX, t.Move.VelY, t.Move.VelZ
}
