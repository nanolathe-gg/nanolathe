package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const goalPendingMask uint32 = 0x20 | 0x40 | 0x80 | 0x100 | 0x200

// raiseEvictedGoalRelease is the controller-slot half of an install
// [04 R-ORD-01 §9]: handing the movement controller a goal — null or new —
// makes it raise `0x80` on the record that owns the object the slot held, and
// the raise follows the OBJECT to its owner, which need not be the record being
// installed for.
//
// The slot is moveGoals for the ground follower and FlightCommand for the
// air controller; each installer clears the other side. An
// installer calls this BEFORE its own closing clear of `0x20`-`0x200`, so a
// record that displaces its own previous object has the self-raise cancelled
// and a record that displaces another's leaves the bit standing on that other
// record — the "goal-handle detach or rebind" producer of the movement
// families' outcome table.
//
// Retired 2026-09-02 (WU-19-71): a null-owner guard stood here, skipping the
// raise when `owner` was zero. Its text said "a NULL owner handle names no
// controller ... raising `0x80` there would carry the bit BETWEEN UNITS", and
// it named its live producers — the mission-script interpreter of [04 §3.6]
// (retired by WU-19-69), then `internal/construction`'s factory product
// records. Both are stamped now. A sweep of every `orders.Node` composite
// literal and every queue-insertion call site in the tree found no producer
// left that leaves the field zero, and instrumenting both install paths over a
// seed-7 skirmish to 50000 ticks and MISSION0 to 18000 counted zero writes to
// the handle-0 entry, so the guard has nothing to guard: nothing is stored
// there, and a lookup there finds no record to raise the bit on. It was never
// a behavioural rule of §9 — it was cover for our own defect — so it goes
// rather than standing as a permanent exception to an unconditional raise.
func (s *System) raiseEvictedGoalRelease(owner pool.Handle) {
	if s == nil {
		return
	}
	// Unconditional, as §9 states it: the raise lands on the owner of whatever
	// object the slot held, whether or not that is the installing record, and
	// the installer's own closing clear of `0x20`-`0x200` cancels the self case.
	if g := handleRow(s.moveGoals, owner); g != nil && g.order != nil {
		g.order.Satisfied |= goalReleasedPending
	}
	if n := s.airPayloadOwner(owner); n != nil {
		n.Satisfied |= goalReleasedPending
	}
}

// ReleaseGoal is the queue cleanup seam for a record's retained payload
// [04 R-ORD-01 §9]. A record with no object has nothing to release.
func (s *System) ReleaseGoal(n *orders.Node) bool {
	if s == nil || n == nil {
		return false
	}
	s.deleteRecordGoal(n, false)
	return true
}

// installGroundPayload publishes n's new goal payload [04 R-ORD-01 §1].
//
// TWO LEVELS, ONE BINDING [04 R-ORD-01 §9]. Every order record has its own
// payload field and owns the object in it, but the unit's movement controller —
// the ground route follower here, the flight block on the air side — has ONE
// payload slot. Installing for a record hands the controller a goal, and
// handing the controller any goal, null or new, makes it raise `0x80` on THE
// RECORD THAT OWNS THE OBJECT CURRENTLY IN ITS SLOT, then replace the slot. The
// raise goes through the object: each goal object carries a reference to the
// record that created it, and the bit is ORed into that record's pending word.
//
// So two records on one mover can each hold a payload object, but only one is
// BOUND. The bound object alone is asked for arrival, alone arms the repath
// bit, and alone is the search's goal; a displaced object stays allocated and
// referenced by its record's field but is inert. When record A installs while
// the controller holds record B's object, B's pending word receives `0x80`;
// when A installs over its own previous object, the same raise lands on A and
// is cancelled by the closing clear below — which is why the bit is never
// observable from an installer acting on itself.
//
// Corrected by [04 R-ORD-01 §9] (WU-19-68). The comment that stood here said
// an installer "writes `0x80` into the record it is installing for, and into no
// other record's pending word", on the argument that the payload is a field OF
// the record and a different record's field is out of reach. The installer does
// not reach the other record's field: it reaches the CONTROLLER'S SLOT, and the
// raise follows the object in that slot to its owner. The behavioural argument
// that comment offered — that a patrol chain would retire every leg on the tick
// it was armed, because the pump walks past a record stalled at gate `0xE0` to
// the records behind it — does not hold either: [04 §3.3] step 3 stops the walk
// AT a gated record with nothing satisfied, so the record behind a stalled leg
// is never pumped and never installs. The 37,196 raises measured on 2026-08-31
// were measured on this reimplementation's pump, not retail's.
//
// The ground goal row and flight command together are that single controller
// slot: a unit is ground or air, and each installer clears the other side.
func (s *System) installGroundPayload(owner pool.Handle, n *orders.Node, goal path.Goal, x, z numeric.Fixed) bool {
	if s == nil || n == nil {
		return false
	}
	// Handing the controller a goal raises `0x80` on the record that owns what
	// the slot held [04 R-ORD-01 §9]. When that is n itself the closing clear
	// below cancels it, which is the "never observable from an installer" case.
	s.releaseRecordGoal(n)
	s.displaceControllerGoal(owner)
	n.Satisfied &^= goalPendingMask
	g := &moveGoal{order: n, x: x, z: z, goal: goal}
	s.storeRecordGoal(owner, recordGoal{node: n, ground: g})
	setHandleRow(&s.moveGoals, owner, g)
	// An install REPLACES the record's goal, so whatever route the mover is
	// following is now aimed at the wrong place. Dropping the active-order
	// binding is what makes the session's mover boundary re-submit against the
	// payload just installed; ActivateMove admits exactly one submission per
	// active order, so without this a record that installs a second goal — a
	// `Move_Ground` re-arm, and every `Attack_Chase` maneuver substate
	// [04 R-ORD-01 §3] — kept walking to its first one. An ordered attacker
	// therefore reached the spot its target had been standing on when the order
	// was given, stopped, and never followed [04 R-ORD-01 §1][04 R-PATH-01 §8].
	// Detaching the binding from its record — rather than deleting it — is what
	// routes the next activation down ActivateMove's own re-activation arm: it
	// cancels the outstanding request and clears the path state before
	// submitting, and it does not let the stale route be adopted as if it were
	// still aimed at this goal.
	if prior := handleRow(s.activeOrders, owner); prior != nil {
		prior.order = nil
	}
	return true
}

// InstallPointGoal binds a point payload and its arrival radius to n.
func (s *System) InstallPointGoal(req orders.PointGoalRequest) bool {
	if s == nil || req.Node == nil {
		return false
	}
	u := s.unitFor(req.Owner)
	if u != nil && u.Def != nil && u.Def.CanFly {
		return false
	}
	fx, fz := s.pathFootprint(u)
	center := path.Cell{X: goalCellForWorld(req.X, fx), Z: goalCellForWorld(req.Z, fz)}
	return s.installGroundPayload(req.Owner, req.Node, path.PointGoal(center, req.Radius), req.X, req.Z)
}

// InstallAnnulusGoal binds a stand-off payload to n.
func (s *System) InstallAnnulusGoal(req orders.AnnulusGoalRequest) bool {
	if s == nil || req.Node == nil {
		return false
	}
	u := s.unitFor(req.Owner)
	if u != nil && u.Def != nil && u.Def.CanFly {
		return false
	}
	fx, fz := s.pathFootprint(u)
	center := path.Cell{X: goalCellForWorld(req.X, fx), Z: goalCellForWorld(req.Z, fz)}
	return s.installGroundPayload(req.Owner, req.Node, path.AnnulusGoal(center, req.InnerRadius, req.OuterRadius), req.X, req.Z)
}

// InstallRectangleGoal binds a footprint rectangle payload to n. The request
// carries the TARGET's anchor cell and footprint size; the rectangle the goal
// class stores is that footprint grown by the OWNING MOVER's own footprint
// [04 R-PATH-01 §12] — see grownGoalRect.
func (s *System) InstallRectangleGoal(req orders.RectangleGoalRequest) bool {
	if s == nil || req.Node == nil {
		return false
	}
	u := s.unitFor(req.Owner)
	if u != nil && u.Def != nil && u.Def.CanFly {
		return false
	}
	fx, fz := s.pathFootprint(u)
	goal := path.RectPerimeterGoal(grownGoalRect(req.CellX, req.CellZ, req.Width, req.Depth, fx, fz))
	return s.installGroundPayload(req.Owner, req.Node, goal, worldCellCenter(req.CellX), worldCellCenter(req.CellZ))
}

// grownGoalRect is the rectangle-goal constructor [04 R-PATH-01 §12]. The
// installer's arguments are the TARGET's anchor cell `(originX, originZ)` and
// its footprint size `(sizeX, sizeZ)`; the class stores, with `(fx, fz)` the
// owning MOVER's own footprint pair in cells — never the target's —
//
//	x1 = originX − fx    x2 = originX + sizeX
//	z1 = originZ − fz    z2 = originZ + sizeZ
//
// all four inclusive. Arrival is the mover's committed anchor cell lying on
// that border, which puts the mover's whole footprint edge- or corner-adjacent
// to the target with no gap; the target's own cells are interior, never
// enumerated, so a BLOCKING target is never a goal cell. Before this the build
// stored the bare footprint, whose only admissible cells for a one-cell feature
// were the feature's own — impassable in the searched layer — so every rock and
// tree reclaim published an empty route and abandoned on `0x40`.
//
// A footprint size below one cell is read as one: every definition's authored
// footprint is at least one cell, and the same clamp is applied wherever this
// package reads a footprint pair (pathFootprint).
func grownGoalRect(originX, originZ, sizeX, sizeZ, moverFootX, moverFootZ int32) path.Rect {
	if sizeX < 1 {
		sizeX = 1
	}
	if sizeZ < 1 {
		sizeZ = 1
	}
	if moverFootX < 1 {
		moverFootX = 1
	}
	if moverFootZ < 1 {
		moverFootZ = 1
	}
	return path.Rect{
		Min: path.Cell{X: originX - moverFootX, Z: originZ - moverFootZ},
		Max: path.Cell{X: originX + sizeX, Z: originZ + sizeZ},
	}
}

func worldCellCenter(c int32) numeric.Fixed { return numeric.Fixed(int64(c) << 20) }

// InstallAirGoal binds the existing flight marker family. Flags are marker
// flags supplied by the order seam; the constructor still establishes the
// required point/follow family bits before optional flags are added.
//
// It carries the same rule as installGroundPayload above: the flight block is
// the air controller and holds ONE payload slot, so installing displaces
// whatever object the slot held and raises `0x80` on THAT object's own record
// [04 R-ORD-01 §9]. When the displaced object is this record's own the closing
// clear cancels the raise.
func (s *System) InstallAirGoal(req orders.AirGoalRequest) bool {
	if s == nil || req.Node == nil {
		return false
	}
	u := s.unitFor(req.Owner)
	if u == nil || u.Def == nil || !u.Def.CanFly {
		return false
	}
	if handleRow(s.Flights, req.Owner) == nil {
		return false
	}
	var marker *airMarker
	if req.Target != 0 {
		marker = s.newFollowUnitMarker(u, req.Target)
	} else if req.Flags&airMarkerFreeze != 0 {
		marker = s.newFrozenTerrainPointMarker(u, 0, Vec3{X: req.X, Y: req.Y, Z: req.Z})
	} else {
		marker = s.newPointMarker(u, Vec3{X: req.X, Y: req.Y, Z: req.Z})
	}
	marker.flags |= req.Flags
	if req.Radius > 0 {
		marker.setArrivalRadius(uint16(req.Radius))
	}
	s.installAirGoal(u, req.Node, marker)
	return true
}

// airPayloadOwner reads the owner attached to the controller slot, never the
// current queue head [04 R-ORD-01 §9].
func (s *System) airPayloadOwner(owner pool.Handle) *orders.Node {
	if s != nil {
		if fl := handleRow(s.Flights, owner); fl != nil && fl.Command != nil && fl.Command.Payload != nil {
			return fl.Command.payloadOwner
		}
	}
	return nil
}

// OW-3-P goal-families wiring [04 §7.2][04 §7.4][04 §3.5].
//
// Four families share the path.Goal interface [04 §7.2] C8:
//   PointGoal, AnnulusGoal (stand-off), RectPerimeterGoal, and air-only goals.
// Before this unit Annulus/Rect had zero callers; orbit/stand-off
// degraded to PointGoal(0) [M-4]. This file wires the STRUCTURE and invents no
// constants: every radius it does not compute is the handler's own, installed
// through the record's payload installers. The placeholder radii this paragraph
// used to point at were deleted by WU-19-6 (see the note below), and no open
// marker remains in this file.

// Retired 2026-09-01 (WU-19-6): a placeholder-radius block stood here, holding
// `placeholderGuardDefaultRaw = 20` (the guard standoff when p1 was zero) and
// describing the chase's per-substate radii as the invented 32/64 pair. None
// of the three has a retail counterpart. [04 R-ORD-01 §8] gives the guard's
// radius as `(FootPrintX(me) + FootPrintX(ward) + 2) · 16` computed by the
// handler, halved for the goal's arrival radius, with no issuer input and no
// zero case; the chase's radii are `d`, `d/2`, `trunc(d/4)`, `0`, annulus
// `(d, d/2)` and annulus `(2d, d)` with `d` the weapon's authored `range`
// [06 R-WPN-05 §1], and the handler installs them itself through the record's
// own payload installers. The literals are deleted, not replaced.

// goalForOrder selects the path.Goal family for an order [04 §7.2][04 §7.4].
//
// Wired families [OW-3-P]:
//
//	Attack_Chase (orbit/stand-off) => whatever its own maneuver phase installed:
//	  a point goal for five of the six live substates and an annulus for the
//	  other two, every radius sized from the slot's weapon range
//	  [04 R-ORD-01 §3][06 R-WPN-05 §1]. The bound payload is consulted first, so
//	  this file supplies no chase goal of its own.
//	Park (a no-rally factory product's terminal record) => RectPerimeterGoal on
//	  the rectangle the handler installed. [04 R-FAC-02 §4] closes the producer
//	  this file previously recorded as missing: Park's phase 0 installs a
//	  rectangle goal centred on the product's own committed cell, and the ground
//	  search treats it as a perimeter goal whose admissible cells are exactly
//	  the border [04 §7.2]. The arithmetic lives in orders.ParkGoalRect; this
//	  case only reads it back.
//	Follow_Ground (the ground guard's follow) => PointGoal at the ward's
//	  position plus the record's stored anchor offset, arrival radius p1 / 2
//	  [04 R-ORD-01 §8]. `VTOL_Follow` and `Guard_NoMove` are not this family:
//	  the stationary guard installs nothing and the air twin circles in
//	  airspace [04 R-UNIT-06 §1].
//
// Unwired families [OW-3-P] with citation why:
//
//	The withdrawn saved-goal compatibility surface has no producer; air work
//	and moving goals now own the zero-heuristic, unsatisfied surface [04 R-PATH-01 §9].
//	  "Base/restored-from-save goals have identically-zero heuristic and a null
//	  start predicate" [04 §7.2], used for save restore (GoalKind 3 in
//	  internal/save/boxes.go). Patrol legs (Patrol/QPatrol/VTOL_Patrol etc) are
//	  queued as sequential PointGoals via the ordinary order queue [04 §3.3];
//	  no bounded evidence shows patrol chaining via an air-goal surface, so leave that
//	  path unwired rather than forcing it [04 §7.4] UNKNOWN frequency.
//
// All other orders => PointGoal(radius 0) [04 §7.2] C8.
func (s *System) goalForOrder(goalCell path.Cell, n *orders.Node) path.Goal {
	return s.goalForOrderWithFootprint(nil, goalCell, n, 1, 1)
}

// workApproachGoal is the goal payload the ground work rows install
// [04 R-ORD-01 §5]. Two shapes cover the family, and both are stated per-row:
//
//   - `HelpBuild` phase 0 installs an ANNULUS at the target's position with
//     outer radius `builddistance + half` and inner radius `half`, where
//     `half` is the assist approach term over the TARGET's definition
//     footprint (orders.AssistApproachHalf) and `builddistance` alone is the
//     builder's own [04 R-ORD-01 §12][05 R-WORK-01 §2]. The annulus class stores the
//     octile radii the heuristic clamps against and, separately, the squared
//     cell radii the arrival predicate compares — internal/path derives the
//     second pair by the >>4 quantisation [04 R-PATH-01 §9][04 R-MOV-03 §2].
//     The band is what lets an assistant arrive BESIDE its target: a point
//     goal on the target's own anchor cell can never be occupied, so the
//     search fails and the record abandons instead of working.
//
//   - `RepairUnit` phase 1 and `Capture` phase 0 install a RECTANGLE from the
//     target's committed anchor cell and its copied footprint size; the goal
//     class grows that by this mover's own footprint and its admissible cells
//     are exactly the border of the GROWN rectangle [04 R-PATH-01 §12].
//
//   - `Reclaim` phase 0 and `Resurrect` phase 0 install that same rectangle from
//     the FEATURE's footprint — "origin cell, size" [04 R-ORD-01 §5]. The
//     origin is the anchor cell, with no half-footprint offset: a feature's
//     stamp writes the definition index on the anchor and the fringe sentinel
//     across the rest of the footprint, so the anchor already is the
//     constructor's origin [05 R-ECO-02 §2][05 R-FEAT-01 §3].
//
// Corrected 2026-09-01 (WU-19-24): all three arms built the BARE footprint
// rectangle, `[origin, origin + size − 1]`, on the reading that "arrival is
// lying on the footprint's own border". [04 R-PATH-01 §12] establishes the
// arithmetic between the installer's arguments and the class's stored fields:
// the constructor grows the argument rectangle by the owning mover's footprint,
// so the target's cells are interior and the border is the ring of anchor cells
// at which the mover stands flush against the target. Every rectangle goal in
// the engine is built that way — see grownGoalRect.
//
// Retired 2026-09-01 (WU-19-5): the two feature rows used to fall through to
// the default point goal, on the note that "this build has no feature resolver
// reachable from the order layer". There is one — the queue binding's world
// adapter resolves a cell to an anchored feature view carrying its footprint,
// which internal/session composes over the terrain's own fringe hop — so the
// plumbing that was missing is in place and the rows install what the row
// states. A point goal on a feature's own anchor cell can never be occupied by
// the reclaimer, so the search failed and the record abandoned rather than
// walking to a distant rock or wreck.
//
// This arm is the FALLBACK. The handler installs the rectangle itself through
// the record's own payload installer, and goalForOrderWithFootprint consults
// the bound payload first; this reproduces the same rectangle for a record
// whose payload is not bound — one restored from a save, or one re-activated
// after another record evicted this build's single per-mover slot.
func (s *System) workApproachGoal(mover *units.Unit, goalCell path.Cell, n *orders.Node, footX, footZ int32) (path.Goal, bool) {
	if s == nil || n == nil || mover == nil || mover.Def == nil {
		return nil, false
	}
	target := (*units.Unit)(nil)
	if n.Target != 0 && s.world != nil {
		target = s.world.Unit(n.Target)
	}
	switch orders.DescriptorFor(n.ID).Name {
	case "HelpBuild":
		center := goalCell
		if target != nil {
			center = path.Cell{X: goalCellForWorld(target.X, footX), Z: goalCellForWorld(target.Z, footZ)}
		}
		// The radicand reads the footprint pair from the TARGET's definition —
		// the unit being assisted — not the assistant's [04 R-ORD-01 §12],
		// which withdraws [04 R-ORD-01 §5]'s "from my own footprint" and
		// confirms [05 R-WORK-01 §2]. Only `builddistance`, the outer term, is
		// the builder's own. The builder's pair is the fallback for the one
		// case retail cannot reach and this fallback arm can: a record whose
		// payload is unbound and whose target no longer resolves.
		halfX, halfZ := mover.Def.FootprintX, mover.Def.FootprintZ
		if target != nil && target.Def != nil {
			halfX, halfZ = target.Def.FootprintX, target.Def.FootprintZ
		}
		half := orders.AssistApproachHalf(halfX, halfZ)
		outer := mover.Def.BuildDistance + half
		if outer < half {
			outer = half
		}
		return path.AnnulusGoal(center, half, outer), true
	case "RepairUnit", "Capture":
		if target == nil || target.Def == nil {
			return nil, false
		}
		tfx, tfz := target.Def.FootprintX, target.Def.FootprintZ
		if tfx <= 0 {
			tfx = 1
		}
		if tfz <= 0 {
			tfz = 1
		}
		anchorX := goalCellForWorld(target.X, tfx)
		anchorZ := goalCellForWorld(target.Z, tfz)
		// The target's anchor and size are the constructor's arguments; the
		// stored rectangle grows them by THIS mover's footprint
		// [04 R-PATH-01 §12].
		return path.RectPerimeterGoal(grownGoalRect(anchorX, anchorZ, tfx, tfz, footX, footZ)), true
	case "Reclaim", "Resurrect":
		anchorX, anchorZ, ffx, ffz, ok := featureRectForGoal(mover, n)
		if !ok {
			return nil, false
		}
		return path.RectPerimeterGoal(grownGoalRect(anchorX, anchorZ, ffx, ffz, footX, footZ)), true
	}
	return nil, false
}

// featureRectForGoal resolves the feature under a record's stored goal position
// to its anchor cell and authored footprint, through the same session-composed
// seam the order layer reads [05 R-ECO-02 §2]. The lookup is the world
// adapter's, not this package's: the anchored resolution — the fringe hop to a
// multi-cell feature's origin, and the definition behind the cell's index —
// belongs to the feature runtime, and routing both the handler's payload
// install and this fallback through one seam keeps them on the same anchor.
//
// A mover with no bound queue, or a session with no world adapter, resolves
// nothing and the caller falls back to the ordinary point goal rather than
// inventing a rectangle.
func featureRectForGoal(mover *units.Unit, n *orders.Node) (cellX, cellZ, footX, footZ int32, ok bool) {
	q := orders.QueueOfUnit(mover)
	if q == nil || n == nil {
		return 0, 0, 0, 0, false
	}
	b := q.Binding()
	if b == nil {
		return 0, 0, 0, 0, false
	}
	view, found := b.LookupFeature(world.WorldToCell(n.GoalX), world.WorldToCell(n.GoalZ))
	if !found {
		return 0, 0, 0, 0, false
	}
	footX, footZ = view.FootprintX, view.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	return view.CX, view.CZ, footX, footZ, true
}

// goalForOrderWithFootprint uses the owning mover's footprint for target
// snapping. Annulus centres are constructed from target world positions with
// the same footprint formula as point goals [04 R-MOV-03 §2].
func (s *System) goalForOrderWithFootprint(mover *units.Unit, goalCell path.Cell, n *orders.Node, footX, footZ int32) path.Goal {
	if n == nil {
		return path.PointGoal(goalCell, 0)
	}
	if bound := s.moveGoalPayload(n.Owner, n); bound != nil {
		return bound
	}
	if g, ok := s.workApproachGoal(mover, goalCell, n, footX, footZ); ok {
		return g
	}
	name := orders.DescriptorFor(n.ID).Name
	switch name {
	case "Park":
		// The rectangle is authored by the Park handler [04 R-ORD-01 §2]
		// [04 R-FAC-02 §4]; a record that has not run its phase 0 yet has no
		// rectangle and falls back to the ordinary point goal.
		//
		// ParkGoalRect returns the handler's `(origin, 8s × 6s size)` as an
		// inclusive origin..origin+size−1 pair; the constructor grows that by
		// the product's own footprint exactly as it does for the other six
		// callers [04 R-PATH-01 §12].
		if minX, minZ, maxX, maxZ, ok := orders.ParkGoalRect(n); ok {
			return path.RectPerimeterGoal(grownGoalRect(minX, minZ, maxX-minX+1, maxZ-minZ+1, footX, footZ))
		}
		return path.PointGoal(goalCell, 0)
	// Retired 2026-08-31: an `Attack_Chase` case stood here forcing an
	// AnnulusGoal centred on the target with the placeholder radii 32 and 64,
	// under two open-question markers saying the per-substate radii were not
	// located. They are located — [04 R-ORD-01 §3] gives a point goal for five
	// of the six live substates and an annulus for the other two, all sized
	// from the slot's weapon range — and the handler now installs the right
	// payload itself through the record's own installers. The bound payload is
	// consulted above, so this case only ever overrode the handler's own
	// choice; without it a chase record with no payload yet falls to the
	// ordinary point goal.
	case "Follow_Ground":
		// The ground guard's payload is a POINT goal at the ward's position
		// plus the record's stored anchor offset, with arrival radius
		// `p1 / 2` [04 R-ORD-01 §8 point 3] — "the annulus installer and the
		// rectangle installer are not called anywhere in either guard
		// handler". `Follow_Ground`'s own maintenance leg installs exactly
		// this every 30 ticks, and the bound payload is consulted above, so
		// this arm is only reached for a record that has not run its phase 1
		// yet. It must still be the same goal: the record's goal triple is an
		// OFFSET from the ward, not a position [04 R-ORD-01 §8 point 2], so
		// falling through to the default point goal would aim the search at a
		// cell near the world origin.
		//
		// `VTOL_Follow` and `Guard_NoMove` used to share this arm. Neither
		// belongs to it: the stationary guard installs no goal of any kind
		// [04 R-ORD-01 §8 point 5], and the air twin's maintenance is the air
		// marker family's airspace circling, not a ground goal
		// [04 R-UNIT-06 §1].
		if n.Target != 0 && s != nil && s.world != nil {
			if ward := s.world.Unit(n.Target); ward != nil {
				x, _, z, radius := orders.GuardFollowPoint(n, ward.X, ward.Y, ward.Z)
				center := path.Cell{X: goalCellForWorld(x, footX), Z: goalCellForWorld(z, footZ)}
				return path.PointGoal(center, radius)
			}
		}
		// No resolvable ward: the offset has nothing to be added to, so there
		// is no point to aim at. The ordinary point goal on the passed cell is
		// what every other unresolved family gets.
		return path.PointGoal(goalCell, 0)
	default:
		// Explicitly unwired families:
		// - RectPerimeterGoal: no established producer [04 §7.2][04 §7.4] — leave as Point instead of inventing a patrol-rect order.
		// - Air work/moving goals: no ground-order producer exists [04 R-PATH-01 §9].
		return path.PointGoal(goalCell, 0)
	}
}

// The above helper is the sole producer of AnnulusGoal and of RectPerimeterGoal;
// call sites in integrate.go (ActivateMove/ReplanMove) are the only consumers.
// The saved-goal producer is intentionally NOT added here; see file header.

// HeadingFromDelta returns the world heading (uint16, 0..65535 per circle)
// whose position step of [04 R-MOV-01 §4] travels along the planar delta
// (dx, dz) in 16.16 fixed units. It is the heading the ground mover steers
// toward for a waypoint at that delta; heading 0 is -Z (up-screen).
//
// It is NOT a way to face a builder at a build site: a mobile builder's
// heading toward its build goal is produced by ordinary movement steering
// [04 §2.3b], and the script's torso turn comes from the relative bearing the
// StartBuilding emitter passes [04 R-CB-01 §3].
func HeadingFromDelta(dx, dz int64) uint16 {
	return numeric.AngleFromAtan2(-dx, -dz).Raw()
}
