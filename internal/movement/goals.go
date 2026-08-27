package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

// OW-3-P goal-families wiring [04 §7.2][04 §7.4][04 §3.5].
//
// Four families share the path.Goal interface [04 §7.2] C8:
//   PointGoal, AnnulusGoal (stand-off), RectPerimeterGoal, SavedGoal.
// Before this unit Annulus/Rect/Saved had zero callers; orbit/stand-off
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
//	Follow_Ground / VTOL_Follow / Guard_NoMove (guard stand-off) => AnnulusGoal
//	  centered on the ward (Target) when available, with Param1 as outer
//	  (fallback placeholderGuardDefaultRaw) and half as inner [04 §3.2][04 §3.5].
//	  Standoff radius Param1 is established [04 §3.2]; its unit conversion is
//	  still TODO(question) [M-4][04 §7.4].
//
// Unwired families [OW-3-P] with citation why:
//
//	RectPerimeterGoal has no established order-layer producer in bounded search;
//	  [04 §7.2][04 §7.4] defines the rectangle heuristic and enumeration, but no
//	  handler in [04 §3.5][04 §10.3] describes a patrol-around-rect target that
//	  constructs it. Forcing it would invent a producer; leave unwired.
//	SavedGoal has no established patrol-leg producer; the class is defined as
//	  "Base/restored-from-save goals have identically-zero heuristic and a null
//	  start predicate" [04 §7.2], used for save restore (GoalKind 3 in
//	  internal/save/boxes.go). Patrol legs (Patrol/QPatrol/VTOL_Patrol etc) are
//	  queued as sequential PointGoals via the ordinary order queue [04 §3.3];
//	  no bounded evidence shows patrol chaining via SavedGoal, so leave that
//	  path unwired rather than forcing it [04 §7.4] UNKNOWN frequency.
//
// All other orders => PointGoal(radius 0) [04 §7.2] C8.
func (s *System) GoalForOrder(goalCell path.Cell, n *orders.Node) path.Goal {
	return s.goalForOrder(goalCell, n)
}

func (s *System) goalForOrder(goalCell path.Cell, n *orders.Node) path.Goal {
	return s.goalForOrderInternal(goalCell, n)
}

func (s *System) goalForOrderInternal(goalCell path.Cell, n *orders.Node) path.Goal {
	if n == nil {
		return path.PointGoal(goalCell, 0)
	}
	name := orders.DescriptorFor(n.ID).Name
	switch name {
	case "Attack_Chase":
		// TODO(question): standoff radii source not located; orbit cadence 30+RNG placeholder [04 §3.5][M-4][04 §7.2][04 §7.4]
		// TODO(question): per-substate band variation (approach/halved/banded zero etc) is established as 8-state machine [04 §3.5] but per-substate radii remain TODO(question); wire single placeholder Annulus structure here, not per-substate radii.
		center := goalCell
		if n.Target != 0 && s != nil && s.world != nil {
			if tgt := s.world.Unit(n.Target); tgt != nil {
				center = path.Cell{X: world.WorldToCell(tgt.X), Z: world.WorldToCell(tgt.Z)}
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
				center = path.Cell{X: world.WorldToCell(ward.X), Z: world.WorldToCell(ward.Z)}
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
		// - SavedGoal: not forced for patrol legs; only save restore uses it via GoalKind 3, which remains unwired here by design [04 §7.2].
		return path.PointGoal(goalCell, 0)
	}
}

// The above helper is the sole new producers of AnnulusGoal; call sites in
// integrate.go (ActivateMove/ReplanMove) are the only consumers. Rect and Saved
// producers are intentionally NOT added here; see file header.

// HeadingFromDelta returns the world heading (uint16, 0..65535 per circle)
// that points along the planar delta (dx, dz) in 16.16 fixed units [04 §5.1].
// It is the same heading the ground mover computes toward a waypoint; the
// mobile-build success path uses it to turn the builder to face the build site
// before raising the slot-form StartBuilding [04 §5.3][cob A-7].
func HeadingFromDelta(dx, dz int64) uint16 {
	return headingFromDelta(dx, dz)
}
