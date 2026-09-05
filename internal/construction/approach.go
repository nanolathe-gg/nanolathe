package construction

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The mobile-build approach [04 R-PATH-01 §13][04 R-ORD-01 §5].
//
// CORRECTION (WU-19-94), superseding every previous version of this file. The
// text that stood here read: "What research establishes and this file
// implements: perimeter enumeration around the footprint, a build-distance
// filter, a placement-validation filter, a sorted bounded candidate list, and a
// single selected point goal." That sentence traced back to [04 §7.4]'s
// unanchored "Build-site generation enumerates perimeter candidates around a
// footprint, filters by range and placement validation, sorts a bounded list of
// candidates, and passes a selected point goal into path search", which
// [04 R-PATH-01 §13] has since STRUCK: it "carries no anchor and describes no
// mechanism on the mobile-build approach path". There is no candidate
// generator, no range filter, no sort, no bounded list and no point goal.
//
// What the handler's approach phase does, and nothing more [04 R-PATH-01 §13]:
// read the product definition's footprint pair; snap the record's X and Z to
// that footprint's centre; zero the leash word; install the RECTANGLE goal of
// [04 R-PATH-01 §12] with the product's anchor cell and footprint as its origin
// and size; set the gate word to `0xE0`; advance.
//
// The candidate set is therefore the SEARCH's: the grown rectangle's border
// cells, enumerated in the order [04 R-MOV-03 §9] gives, and the "selection" is
// the border cell the search closes first by path cost, with the enumeration
// order as the tie among equal keys. The builder halts when its committed
// anchor lies on that border [04 R-PATH-01 §12]. Because the rectangle grows
// the product footprint by the MOVER's own footprint on the west and north and
// by one cell on the east and south, the product's own cells are interior of
// the goal — never enumerated — so a builder that arrives is standing clear of
// the site it is about to stamp. That is what the removed offset-1 ring was
// approximating by hand; installing the goal itself makes the approximation
// unnecessary and drops the untraced rings, sort key and bounded list with it.
//
// RNG (I5/I4): the removed generator drew nothing. Ring enumeration, the
// distance sort and the placement filter were all deterministic integer work
// and consumed no simulation or CRT draw, so retiring them moves no draw and
// preserves call order; [04 R-PATH-01 §13] likewise names no draw on this path.
//
// The four open-question markers this header carried are retired by
// [04 R-PATH-01 §13] (rings beyond offset 1; the sort key, tie-break and
// bounded-list size — all three answered "no such mechanism exists") and by
// [05 R-WORK-01 §2] with [05 R-WORK-01 §12] (the reach test's origin and form —
// see needsApproach).
//
// Kept from the previous version, because it is still the contract: retail
// keeps ONE ground word per cell, written by movers and buildings alike, and
// the footprint validator rejects any nonzero occupant other than the passed
// self identity, with a null self identity at the mobile-build site
// [04 R-COLL-01 §2][04 R-COLL-01 §4][04 R-COLL-01 §6]. A builder standing on
// its own site therefore blocks it, and the order takes the blocked-area budget
// of [R-ORDER-02 §1] rather than stamping a nanoframe over the builder.
// Nanolathe splits that word into the terrain plot cell and the mover occupancy
// lattice; Service.mobileOccupancy is the second half. mustClearSite below is
// what keeps the walk installed until the builder is off its own site.

// siteAnchorCell resolves the north-west footprint cell and extent of the site
// stored on a MOBILEBUILD node [07 §9]. The node's Goal is the footprint
// centre; the anchor is the same snap the commit path performs, and it is the
// origin argument the rectangle-goal installer takes [04 R-PATH-01 §12].
func (s *Service) siteAnchorCell(node *orders.Node) (anchorX, anchorZ, footX, footZ int32, ok bool) {
	if s == nil || node == nil {
		return 0, 0, 0, 0, false
	}
	def := s.getProductDefForNode(node)
	if def == nil {
		return 0, 0, 0, 0, false
	}
	footX, footZ = world.FootprintForUnit(s.Catalog, def)
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	anchor, err := world.SnapFootprintAnchor(node.GoalX, node.GoalZ, extent)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	cell := anchor.Cell()
	return cell.X, cell.Z, footX, footZ, true
}

// siteCentre returns the site's SNAPPED footprint centre and the product's
// footprint pair — the two quantities the reach test of [05 R-WORK-01 §2] needs
// at the target end.
//
// The centre is `(foot + 2·cell)·2^19` per axis, which is the record's own goal
// after phase 0's snap [04 R-PATH-01 §13][05 R-WORK-01 §12]: "the handler then
// measures from its own position to the record's goal — the site centre snapped
// to the product footprint in phase 0". Recomputing it from the anchor rather
// than reading node.GoalX/GoalZ keeps this measurement on the same cell the
// goal installer and the commit validator use, whatever the click resolved to.
func (s *Service) siteCentre(node *orders.Node) (x, z numeric.Fixed, footX, footZ int32, ok bool) {
	anchorX, anchorZ, fx, fz, resolved := s.siteAnchorCell(node)
	if !resolved {
		return 0, 0, 0, 0, false
	}
	extent, err := world.NewFootprintExtent(fx, fz)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	centre, err := world.CenterForFootprint(world.NewFootprintAnchor(anchorX, anchorZ), extent)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return centre.X(), centre.Z(), fx, fz, true
}

// SiteCentrePublic exposes siteCentre for wiring tests and session diagnostics
// [05 R-WORK-01 §2].
func (s *Service) SiteCentrePublic(node *orders.Node) (x, z numeric.Fixed, footX, footZ int32, ok bool) {
	return s.siteCentre(node)
}

// installApproachGoal is the approach phase's whole goal mechanism
// [04 R-PATH-01 §13]: the RECTANGLE goal of [04 R-PATH-01 §12] installed with
// the product's anchor cell as its origin and the product's footprint as its
// size. internal/movement's constructor grows that argument rectangle by the
// BUILDER's own footprint, so the border it enumerates is exactly the ring of
// anchor cells at which the builder stands flush against the site.
//
// Installation is once per record, not once per visit. Retail installs the goal
// in the approach phase and advances behind gate `0xE0`; handing the movement
// controller a goal evicts whatever it held and drops the mover's active-order
// binding [04 R-ORD-01 §9], so re-installing on every visit would re-submit the
// path request every tick. HasGroundGoal is the identity-checked "this record
// already owns the mover's payload" test, and a record that lost the slot to
// another installs again — which is the same rebind the restore path needs,
// since a goal payload is derived state that no save box carries.
func (s *Service) installApproachGoal(builder *units.Unit, node *orders.Node) bool {
	if s == nil || s.Movement == nil || builder == nil || node == nil {
		return false
	}
	if s.Movement.HasGroundGoal(builder.Handle, node) {
		return true
	}
	anchorX, anchorZ, footX, footZ, ok := s.siteAnchorCell(node)
	if !ok {
		return false
	}
	return s.Movement.InstallRectangleGoal(orders.RectangleGoalRequest{
		Owner: builder.Handle,
		Node:  node,
		CellX: anchorX,
		CellZ: anchorZ,
		Width: footX,
		Depth: footZ,
	})
}

// InstallApproachGoalPublic exposes installApproachGoal for the approach
// regression test and for session diagnostics [04 R-PATH-01 §13].
func (s *Service) InstallApproachGoalPublic(builder *units.Unit, node *orders.Node) bool {
	return s.installApproachGoal(builder, node)
}

// rectsOverlap reports whether two half-open cell rectangles intersect.
func rectsOverlap(aMinX, aMinZ, aMaxX, aMaxZ, bMinX, bMinZ, bMaxX, bMaxZ int32) bool {
	return aMinX < bMaxX && bMinX < aMaxX && aMinZ < bMaxZ && bMinZ < aMaxZ
}

// unitFootprintAnchor returns the cell a unit's footprint anchors at when its
// centre stands at the world point (x,z), using the same snap the occupancy
// commit uses [04 §8.2] and the same quantity the rectangle goal's arrival test
// reads [04 R-PATH-01 §12]. It is asked of the builder by mustClearSite and of
// the TARGET by unit reclaim's approach, which installs its rectangle goal on
// the target's own footprint [04 R-ORD-01 §5].
func (s *Service) unitFootprintAnchor(u *units.Unit, x, z numeric.Fixed) (cellX, cellZ int32, ok bool) {
	if s == nil || u == nil || u.Def == nil {
		return 0, 0, false
	}
	bx, bz := world.FootprintForUnit(s.Catalog, u.Def)
	extent, err := world.NewFootprintExtent(bx, bz)
	if err != nil {
		return 0, 0, false
	}
	anchor, err := world.SnapFootprintAnchor(x, z, extent)
	if err != nil {
		return 0, 0, false
	}
	c := anchor.Cell()
	return c.X, c.Z, true
}

// needsApproach reports whether a mobile builder must still move before its
// build order can commit [04 §3.4][05 R-WORK-01 §2][05 R-WORK-01 §12]. It is
// the single gate the walk submission and the state-2 handler share.
//
// The reach test is [05 R-WORK-01 §2]'s, exactly: planar in X and Z, from the
// BUILDER's origin to the SITE's snapped centre, with the builder instance's
// half-footprint diagonal and the PRODUCT definition's half-footprint diagonal
// both subtracted, compared inclusively and with a signed compare against the
// builder's `builddistance` [05 R-WORK-01 §12] point 1. Y is ignored, no nano
// piece is resolved and no model radius appears — point 3 of §12 retires all
// three.
//
// PLACEMENT, recorded rather than implemented. §12 point 2 establishes that
// retail consults this expression ONLY on the approach phase's arrival-failure
// wake (satisfied bit `0x40`): a successful arrival on the rectangle border is
// itself the reach, and the work phase has no range test at all. Nanolathe's
// state-2 handler still consults it per visit, because the record's wake
// plumbing is owned by internal/session and internal/orders and this unit does
// not reach it. The expression is now the traced one either way; what remains
// approximate is where it is asked, and the difference is visible only for a
// builder that arrives on the border while the centre-minus-pads value still
// exceeds `builddistance` — which the rectangle's geometry makes rare rather
// than impossible.
//
// TODO(question): moving this consultation onto the `0x40` wake alone, so an
// arrival at the rectangle border retires the approach without a distance test
// [05 R-WORK-01 §12] point 2, needs a wake this handler can read. It cannot
// read the record's own satisfied word: the pump computes the satisfied set as
// `(record.satisfied | unit.pending) & record.gate` and then CLEARS the
// delivered bits from the record [04 §3.3], so by the time internal/session
// drives StepUnit the `0x40` is already consumed — construction sees a word
// that is zero on exactly the visit the bit was meant for. (Re-checked
// WU-19-166: the previous text here blamed "the approach phase's satisfied-word
// plumbing that internal/session owns", which named the wrong half. Session
// already acts on the bit — its activation boundary calls
// orders.MobileBuildUnreachableVisit(active.Satisfied, …) and runs the row's
// abandon arm; what is absent is a delivery of the same wake INTO this
// handler.) The seam is therefore in internal/orders or internal/session:
// either the pump dispatches construction's states from the satisfied set the
// way it dispatches a handler row, or the wake is latched on the record for the
// StepUnit visit that follows. Both are separate units; nothing here should
// invent a second reach rule in the meantime.
//
// Standing on the site is a separate question answered by mustClearSite below.
// Folding it in here made the state-2 handler ("a true result means return now,
// validate nothing") hang forever whenever the mover's halt snapped its anchor
// one cell off; the overlap therefore drives the WALK and the commit keeps
// falling through to the validator, whose rejection runs the bounded
// blocked-area budget of [R-ORDER-02 §1].
func (s *Service) needsApproach(builder *units.Unit, node *orders.Node) bool {
	if s == nil || s.Movement == nil || builder == nil || node == nil {
		return false
	}
	if builder.Def == nil || builder.Def.BuildDistance == 0 {
		return false
	}
	// A construction aircraft has no approach term at all. [04 R-ORD-02 §2]
	// closes the VTOL_MobileBuild work body with "there is no nanolathe-active
	// stamp and no reach test after arrival: an aircraft that reached its
	// builddistance marker builds from wherever the 150-tick orbit leaves it."
	// Arrival is the air leg's own marker, installed in the order's phase 1 at
	// radius builddistance; the orbit of [04 §10.3] then keeps moving the
	// aircraft, and every station it flies to is a legal place to build from.
	//
	// Applying the ground reach test here answered yes on nearly every visit —
	// the orbit sits AT builddistance and swings beyond it between stations, and
	// the builder is a cruise altitude above the site besides. State 2 treats a
	// true result as "return now, validate nothing", so the record parked at
	// phase 2, never created its product, and was abandoned by the blocked-area
	// budget about 400 ticks later. It also called ensureWalk, submitting a
	// GROUND path request for an aircraft.
	if builder.Def.CanFly {
		return false
	}
	cx, cz, footX, footZ, ok := s.siteCentre(node)
	if !ok {
		cx, cz = node.GoalX, node.GoalZ
		footX, footZ = 1, 1
	}
	return !s.isWithinNanoRange(builder, cx, cz, footX, footZ)
}

// mustClearSite reports whether the builder's own footprint still covers any
// cell of the site rectangle.
//
// The rectangle goal's "enumerated goal cells are exactly the rectangle border
// ... and arrival requires lying on that border", with a unit inside measuring
// `16 · min(distance to each edge)` back out [04 R-ORD-01 §5][04 §7.2]
// [04 R-PATH-01 §12]. A builder standing inside its own site is therefore not
// arrived and is steered out of it before the order's validation can accept
// anything. This predicate is the local form of that same statement: it keeps
// the walk installed and stops the state-2 handler cancelling it.
//
// It deliberately does not gate the commit: see needsApproach for why the
// commit must keep reaching the validator, and [R-ORDER-02 §1] for the budget
// that bounds it there.
func (s *Service) mustClearSite(builder *units.Unit, node *orders.Node) bool {
	if s == nil || builder == nil || builder.Def == nil || node == nil {
		return false
	}
	anchorX, anchorZ, footX, footZ, ok := s.siteAnchorCell(node)
	if !ok {
		return false
	}
	bx, bz := world.FootprintForUnit(s.Catalog, builder.Def)
	cellX, cellZ, ok := s.unitFootprintAnchor(builder, builder.X, builder.Z)
	if !ok {
		return false
	}
	return rectsOverlap(cellX, cellZ, cellX+bx, cellZ+bz, anchorX, anchorZ, anchorX+footX, anchorZ+footZ)
}

// MustClearSitePublic exposes mustClearSite for the walk-predicate regression
// test and for session diagnostics [04 §7.2][04 R-ORD-01 §5].
func (s *Service) MustClearSitePublic(builder *units.Unit, node *orders.Node) bool {
	return s.mustClearSite(builder, node)
}
