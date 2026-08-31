package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

const goalPendingMask uint32 = 0x20 | 0x40 | 0x80 | 0x100 | 0x200

func (s *System) releaseGoalNode(n *orders.Node) {
	if s == nil || n == nil {
		return
	}
	if g := s.moveGoals[n.Owner]; g != nil && g.order == n {
		n.Satisfied |= 0x80
		delete(s.moveGoals, n.Owner)
	}
	if st := s.airOrders[n.Owner]; st != nil && st.order == n {
		n.Satisfied |= 0x80
		s.releaseAirGoalForNode(n.Owner, n)
	}
}

// ReleaseGoal releases the payload owned by n. The node identity check keeps
// replacement/cancel cleanup from detaching a successor's payload.
func (s *System) ReleaseGoal(n *orders.Node) bool {
	if s == nil || n == nil {
		return false
	}
	s.releaseGoalNode(n)
	return true
}

func (s *System) installGroundPayload(owner pool.Handle, n *orders.Node, goal path.Goal, x, z numeric.Fixed) bool {
	if s == nil || n == nil {
		return false
	}
	if prior := s.moveGoals[owner]; prior != nil {
		if prior.order != n {
			prior.order.Satisfied |= 0x80
		}
		delete(s.moveGoals, owner)
	}
	if prior := s.airOrders[owner]; prior != nil {
		if prior.order != n {
			prior.order.Satisfied |= 0x80
		}
		s.releaseAirGoalForNode(owner, prior.order)
	}
	if s.moveGoals == nil {
		s.moveGoals = make(map[pool.Handle]*moveGoal)
	}
	n.Satisfied &^= goalPendingMask
	s.moveGoals[owner] = &moveGoal{order: n, x: x, z: z, goal: goal}
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

// InstallRectangleGoal binds a footprint rectangle payload to n.
func (s *System) InstallRectangleGoal(req orders.RectangleGoalRequest) bool {
	if s == nil || req.Node == nil {
		return false
	}
	if u := s.unitFor(req.Owner); u != nil && u.Def != nil && u.Def.CanFly {
		return false
	}
	goal := path.RectPerimeterGoal(path.Rect{Min: path.Cell{X: req.CellX, Z: req.CellZ}, Max: path.Cell{X: req.CellX + req.Width - 1, Z: req.CellZ + req.Depth - 1}})
	return s.installGroundPayload(req.Owner, req.Node, goal, worldCellCenter(req.CellX), worldCellCenter(req.CellZ))
}

func worldCellCenter(c int32) numeric.Fixed { return numeric.Fixed(int64(c) << 20) }

// InstallAirGoal binds the existing flight marker family. Flags are marker
// flags supplied by the order seam; the constructor still establishes the
// required point/follow family bits before optional flags are added.
func (s *System) InstallAirGoal(req orders.AirGoalRequest) bool {
	if s == nil || req.Node == nil {
		return false
	}
	u := s.unitFor(req.Owner)
	if u == nil || u.Def == nil || !u.Def.CanFly {
		return false
	}
	if s.Flights[req.Owner] == nil {
		return false
	}
	if prior := s.airOrders[req.Owner]; prior != nil {
		if prior.order != req.Node {
			prior.order.Satisfied |= 0x80
		}
		s.releaseAirGoalForNode(req.Owner, prior.order)
	}
	if prior := s.moveGoals[req.Owner]; prior != nil {
		if prior.order != req.Node {
			prior.order.Satisfied |= 0x80
		}
		delete(s.moveGoals, req.Owner)
	}
	var marker *airMarker
	if req.Target != 0 {
		marker = s.newFollowUnitMarker(u, req.Target)
	} else if req.Flags&airMarkerFreeze != 0 {
		marker = s.newFrozenTerrainPointMarker(u, Vec3{X: req.X, Y: req.Y, Z: req.Z})
	} else {
		marker = s.newPointMarker(u, Vec3{X: req.X, Y: req.Y, Z: req.Z})
	}
	marker.flags |= req.Flags
	if req.Radius > 0 {
		marker.setArrivalRadius(uint16(req.Radius))
	}
	s.installAirGoal(u, req.Node, marker)
	st := s.airStateFor(u, req.Node)
	st.order = req.Node
	return true
}

// releaseAirGoalForNode is the identity-aware wrapper around the existing air
// command release helper.
func (s *System) releaseAirGoalForNode(owner pool.Handle, n *orders.Node) {
	if s == nil || n == nil {
		return
	}
	if st := s.airOrders[owner]; st == nil || st.order != n {
		return
	}
	s.releaseAirGoal(s.unitFor(owner))
	delete(s.airOrders, owner)
}

// OW-3-P goal-families wiring [04 §7.2][04 §7.4][04 §3.5].
//
// Four families share the path.Goal interface [04 §7.2] C8:
//   PointGoal, AnnulusGoal (stand-off), RectPerimeterGoal, and air-only goals.
// Before this unit Annulus/Rect had zero callers; orbit/stand-off
// degraded to PointGoal(0) [M-4]. This file wires the STRUCTURE with
// placeholder values clearly marked where research does NOT establish a
// constant; it does NOT invent constants. See citations and TODO(question)
// markers below.

// Placeholder standoff radii [04 §3.5][04 §7.2][04 §7.4] TODO(question).
//
// Retail establishes the annulus V-shaped heuristic (raw radii in H, quantized
// radii in arrival [04 §7.4]) but the per-order radius source and the world-
// to-raw conversion remain unknown. The handler stubs in
// internal/orders/resolve.go use 64 world units with half/double banding and
// a 30+rand cadence [04 §3.5] as honest placeholders; we reuse those
// placeholders here for the Goal-structure wiring and keep the TODO(question)
// markers so the values are not mistaken for recovered constants. The orbit
// cadence (Code 3 wait 30+RNG) is also TODO(question) per M-4 (~473,502-526,606)
// and is not closed here.
//
// Units TODO(question): Goal radii are raw heuristic units compared to
// oct = 18*max+7*min [04 §7.2]; world units (Fixed 16.16, 1 cell = 16 world
// units = 0x100000) vs raw vs quantized >>4 radii mismatch [04 §7.4] is REAL
// and reproduced in path/goals.go, not fixed. Using world-derived 64 directly
// as raw is the structure placeholder, not a recovered conversion.

const (
	// placeholderAttackOuterRaw is the outer stand-off radius for Attack_Chase
	// orbit states 0/1/2/5/8 [04 §3.5] — stubStandoffWorld 64 world units [M-4]
	// used as raw heuristic placeholder. TODO(question) [04 §3.5][04 §7.2][04 §7.4]
	placeholderAttackOuterRaw int32 = 64
	// placeholderAttackInnerRaw is half stand-off for the banded states 1/2/5
	// etc. TODO(question) half/double band geometry not fully located beyond
	// "two banded-goal states (inner/outer radii at standoff/half and
	// double/half)" [04 §3.5][M-4]
	placeholderAttackInnerRaw int32 = 32
	// placeholderGuardDefaultRaw is the fallback guard standoff when Param1==0
	// [04 §3.2] "For a guard the first is the standoff radius" [04 §3.5](e)
	// TODO(question) guard standoff radius source for guard is Param1 fallback 20 world units [04 §3.2][04 §3.5][M-4]
	placeholderGuardDefaultRaw int32 = 20
)

// goalForOrder selects the path.Goal family for an order [04 §7.2][04 §7.4].
//
// Wired families [OW-3-P]:
//
//	Attack_Chase (orbit/stand-off) => AnnulusGoal with placeholder inner/outer
//	  per substate Param2 [04 §3.5] orbit cycle 0..8, using the placeholder
//	  radii above. Structure wired, constants remain TODO(question) [M-4].
//	Park (a no-rally factory product's terminal record) => RectPerimeterGoal on
//	  the rectangle the handler installed. [04 R-FAC-02 §4] closes the producer
//	  this file previously recorded as missing: Park's phase 0 installs a
//	  rectangle goal centred on the product's own committed cell, and the ground
//	  search treats it as a perimeter goal whose admissible cells are exactly
//	  the border [04 §7.2]. The arithmetic lives in orders.ParkGoalRect; this
//	  case only reads it back.
//	Follow_Ground / VTOL_Follow / Guard_NoMove (guard stand-off) => AnnulusGoal
//	  centered on the ward (Target) when available, with Param1 as outer
//	  (fallback placeholderGuardDefaultRaw) and half as inner [04 §3.2][04 §3.5].
//	  Standoff radius Param1 is established [04 §3.2]; its unit conversion is
//	  still TODO(question) [M-4][04 §7.4].
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
//     `half` is the assist approach term taken from the assistant's OWN
//     footprint (orders.AssistApproachHalf). The annulus class stores the
//     octile radii the heuristic clamps against and, separately, the squared
//     cell radii the arrival predicate compares — internal/path derives the
//     second pair by the >>4 quantisation [04 R-PATH-01 §9][04 R-MOV-03 §2].
//     The band is what lets an assistant arrive BESIDE its target: a point
//     goal on the target's own anchor cell can never be occupied, so the
//     search fails and the record abandons instead of working.
//   - `RepairUnit` phase 1 and `Capture` phase 0 install a RECTANGLE on the
//     target's footprint; its admissible cells are exactly the border and
//     arrival is lying on it [04 §7.2].
//
// `Reclaim` and `Resurrect` install that same rectangle on the FEATURE's
// footprint [04 R-ORD-01 §5], and this build has no feature resolver reachable
// from the order layer (the resolver placeholder on reclaimHandler,
// internal/orders/work.go). They keep the default point goal until a feature
// footprint is readable here; a trace is not what is missing, the plumbing is.
// The open marker for that gap stands at the payload bind in integrate.go.
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
		half := orders.AssistApproachHalf(mover.Def.FootprintX, mover.Def.FootprintZ)
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
		return path.RectPerimeterGoal(path.Rect{
			Min: path.Cell{X: anchorX, Z: anchorZ},
			Max: path.Cell{X: anchorX + tfx - 1, Z: anchorZ + tfz - 1},
		}), true
	}
	return nil, false
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
		if minX, minZ, maxX, maxZ, ok := orders.ParkGoalRect(n); ok {
			return path.RectPerimeterGoal(path.Rect{
				Min: path.Cell{X: minX, Z: minZ},
				Max: path.Cell{X: maxX, Z: maxZ},
			})
		}
		return path.PointGoal(goalCell, 0)
	case "Attack_Chase":
		// TODO(question): standoff radii source not located; orbit cadence 30+RNG placeholder [04 §3.5][M-4][04 §7.2][04 §7.4]
		// TODO(question): per-substate band variation (approach/halved/banded zero etc) is established as 8-state machine [04 §3.5] but per-substate radii remain TODO(question); wire single placeholder Annulus structure here, not per-substate radii.
		center := goalCell
		if n.Target != 0 && s != nil && s.world != nil {
			if tgt := s.world.Unit(n.Target); tgt != nil {
				center = path.Cell{X: goalCellForWorld(tgt.X, footX), Z: goalCellForWorld(tgt.Z, footZ)}
			}
		}
		// Wire AnnulusGoal structure with placeholder inner/outer; orbit cadence (Code 3 30+RNG) and vertical halve (Param2 substate) remain TODO(question) [04 §3.5][M-4][04 §7.2][04 §7.4]
		inner, outer := placeholderAttackInnerRaw, placeholderAttackOuterRaw
		return path.AnnulusGoal(center, inner, outer)
	case "Follow_Ground", "VTOL_Follow", "Guard_NoMove":
		// TODO(question): guard standoff radius source is Param1 [04 §3.2] fallback 20 world units [04 §3.5][M-4]; unit conversion TODO [04 §7.4]
		center := goalCell
		if n.Target != 0 && s != nil && s.world != nil {
			if ward := s.world.Unit(n.Target); ward != nil {
				center = path.Cell{X: goalCellForWorld(ward.X, footX), Z: goalCellForWorld(ward.Z, footZ)}
			}
		} else if n.Target != 0 {
			// Target not yet resolvable; keep goalCell as center placeholder.
		}
		outer := placeholderGuardDefaultRaw
		if n.Param1 != 0 {
			outer = int32(n.Param1) // TODO(question): using Param1 raw as placeholder raw radii; world-vs-raw mismatch [04 §7.2][04 §7.4]
		}
		inner := outer / 2 // TODO(question): banded-goal inner at half [04 §3.5] placeholder band
		if inner < 0 {
			inner = 0
		}
		if inner > outer {
			inner = outer
		}
		return path.AnnulusGoal(center, inner, outer)
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
