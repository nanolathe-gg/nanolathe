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

// needsApproach reports whether a mobile builder's build record is still in its
// approach phase [04 §3.4][05 R-WORK-01 §2][05 R-WORK-01 §12][05 R-WORK-01 §13].
// It is the single gate the walk submission and the state-2 handler share.
//
// The reach test it runs, on the one visit it runs it, is [05 R-WORK-01 §2]'s,
// exactly: planar in X and Z, from the
// BUILDER's origin to the SITE's snapped centre, with the builder instance's
// half-footprint diagonal and the PRODUCT definition's half-footprint diagonal
// both subtracted, compared inclusively and with a signed compare against the
// builder's `builddistance` [05 R-WORK-01 §12] point 1. Y is ignored, no nano
// piece is resolved and no model radius appears — point 3 of §12 retires all
// three.
//
// WHERE it is consulted (WU-19-218, retiring the T25 placeholder that stood
// here — the consultation now runs where research says it runs).
// [05 R-WORK-01 §12] point 2 and [05 R-WORK-01 §13] establish that retail asks
// this expression ONLY on the approach phase's arrival-failure wake, satisfied
// bit `0x40`. Phase 0 arms the record's dynamic gate to `0xE0`, so phase 1 is
// dispatched on the three movement outcomes of [04 R-ORD-01 §0] and on nothing
// else; the body tests its satisfied argument for `0x40` and only under it forms
// the centre-to-centre distance. A visit carrying `0x20` (the follower reached
// the rectangle goal) or `0x80` (a goal object was released) without `0x40`
// skips the expression entirely — standing on the footprint's border IS the
// reach [04 §7.2][04 R-PATH-01 §12] — and the work phase has no range test at
// all. There is no other range term in the row.
//
// needsApproach below is therefore no longer the reach expression, and no
// longer a wake consumer either: it is the question "is this record still IN
// the approach phase", and the record's own phase byte is the answer.
//
// WU-19-225 collapsed the seam that stood between those two sentences. The
// approach used to park the record on a private wake bit with a one-tick
// deadline and pull the movement outcome out of the accumulating words through
// an orders.DeliverApproachWake helper, keeping the "phase 1 has already run"
// mark in a Go-only Node.ApproachRetired field. Both are gone:
//
//   - the approach phase arms `0xE0` on the record's own dynamic gate, so the
//     pump's step 3 computes the satisfied set, clears the delivered bits out of
//     both words and hands them to this package's registered handler as the
//     ordinary `satisfied` argument [04 §3.3][05 R-WORK-01 §13];
//   - the retirement is a PHASE ADVANCE, State1 to State2, exactly as retail's
//     phase 0 to phase 1 to work is. That is where retail keeps it and where
//     the save box carries it: byte `0x09` of the 58-byte order record is the
//     "handler-private phase/state byte ... handlers own its interpretation"
//     [08 R-SAVE-ORDER-01]. A builder saved mid-approach now restores at
//     State1 and re-installs its goal; one saved past the approach restores at
//     State2 and does not walk again.
//
// The three arms of [05 R-WORK-01 §13] live in mobileBuildWakeVisit below, and
// the phase they advance is this predicate's whole state. The reach expression
// is consulted on exactly one visit of the record's life — the first `0x40`
// that reaches State1 — because every later visit is at State2, which is what
// "the work phase has no range test at all" means for a machine visited per
// tick [05 R-WORK-01 §12] point 2.
//
// Standing on the site is a separate question answered by mustClearSite below.
// Folding it in here made the state-2 handler ("a true result means return now,
// validate nothing") hang forever whenever the mover's halt snapped its anchor
// one cell off; the overlap therefore drives the WALK and the commit keeps
// falling through to the validator, whose rejection runs the bounded
// blocked-area budget of [R-ORDER-02 §1].
func (s *Service) needsApproach(builder *units.Unit, node *orders.Node) bool {
	if !s.approachArmed(builder, node) {
		return false
	}
	// State0 counts as approaching. It is the mobile row's pre-approach phase —
	// handleState0 turns it into State1 on the record's first construction visit
	// and nothing else lives there — so a record that has just been issued, or
	// one the stop interrupt has restarted, is in its approach from the tick it
	// reaches the head, not from the tick after. The session's activation
	// boundary reads this predicate BEFORE that first construction visit, so
	// excluding State0 would deactivate the mover for exactly one tick and delay
	// every mobile build by that tick.
	return State(node.Phase) < State2
}

// approachArmed reports whether this builder and record carry an approach phase
// at all — the precondition for arming `0xE0` and for consulting the reach
// expression [05 R-WORK-01 §13].
//
// A construction aircraft has no approach term. [04 R-ORD-02 §2] closes the
// VTOL_MobileBuild work body with "there is no nanolathe-active stamp and no
// reach test after arrival: an aircraft that reached its builddistance marker
// builds from wherever the 150-tick orbit leaves it." Arrival is the air leg's
// own marker, installed in the order's phase 1 at radius builddistance; the
// orbit of [04 §10.3] then keeps moving the aircraft, and every station it flies
// to is a legal place to build from.
//
// Applying the ground reach test to an aircraft answered yes on nearly every
// visit — the orbit sits AT builddistance and swings beyond it between stations,
// and the builder is a cruise altitude above the site besides — so the record
// parked in its approach, never created its product, and was abandoned by the
// blocked-area budget about 400 ticks later. It also submitted a GROUND path
// request for an aircraft.
func (s *Service) approachArmed(builder *units.Unit, node *orders.Node) bool {
	if s == nil || s.Movement == nil || builder == nil || node == nil {
		return false
	}
	if builder.Def == nil || builder.Def.BuildDistance == 0 {
		return false
	}
	return !builder.Def.CanFly
}

// handleMobileApproach is the mobile-build row's approach phase, State1
// [04 R-PATH-01 §13][05 R-WORK-01 §13]. It is what the file header describes:
// install the rectangle goal on the product footprint, arm the record's dynamic
// gate to `0xE0`, and wait. There is no deadline — the three movement outcomes
// are the only thing that dispatches phase 1, and the follower owns the
// re-request cadence with no retry ceiling [04 R-MOV-01 §7].
//
// A builder with no approach term (an aircraft, or a definition with no
// `builddistance`) advances into the placement phase in this same visit, which
// is the timing the machine had when State0 wired straight to State2.
//
// What the gate replaces is worth naming, because it is the one observable
// change WU-19-225 makes to a running game: the approach used to arm this
// build's own `WakeBit2` (`0x4`) with a one-tick deadline. `0x4` is not a free
// bit — it is the SCRIPT-TOUCHED MARKER of [04 R-ORD-01 §0]
// (units.PendingScriptTouched, wired by WU-19-148) — so a builder walking to a
// site swallowed that notification out of its own pending word on every visit,
// and the expiring deadline left bit 0 standing in the record's satisfied word.
// Arming `0xE0` leaves both alone. Mobile-build timing is unchanged: a 24000-tick
// skirmish stamps the same products on the same ticks.
func (s *Service) handleMobileApproach(builder *units.Unit, node *orders.Node, tick uint32) {
	if s == nil || builder == nil || node == nil {
		return
	}
	if !isMobileBuilder(builder) || !s.approachArmed(builder, node) {
		node.Phase = uint8(State2)
		node.DynamicGate = 0
		node.Deadline = -1
		s.handleMobileState2(builder, node, tick)
		return
	}
	s.ensureWalk(builder, node)
	node.DynamicGate = orders.ApproachWakeGate
	node.Deadline = -1
	node.MoveState = orders.MoveEnRoute
}

// mobileBuildWakeVisit is the row's phase-1 body, dispatched by the order pump
// through this service's OwnedHandler registration (factory.go,
// RegisterOrderHandlers). Its `satisfied` argument is the set the pump computed
// and consumed for this visit [04 §3.3], which for a record parked on `0xE0` is
// one or more of the three movement outcomes of [04 R-ORD-01 §0].
//
// The three arms are [05 R-WORK-01 §13]'s three:
//
//   - `0x40` — "an empty route was published away from the goal". This is the
//     ONE consultation of the reach expression. Out of reach is status 7
//     `I can't reach the construction site` and abandon (code 8: the pump
//     unlinks and frees the record and continues its walk [04 §3.3]); in reach
//     retires the approach.
//   - `0x20`/`0x80` — arrival at the rectangle border, or a released goal
//     object. Retire with NO distance test: standing on the footprint's border
//     IS the reach [04 §7.2][04 R-PATH-01 §12]. This is the border case a
//     per-visit reach test got wrong — the builder is flush against the site
//     while the centre-minus-pads value may still exceed `builddistance` for a
//     large product.
//   - nothing gated — the record is not dispatched at all; the pump stalls on
//     the armed gate and this body never runs.
//
// It reports `(0, false)` on every arm but the abandon: this service advances
// its build records from its own per-unit step, and a result code applied to
// such a record would overwrite the deadline that step owns (OwnedHandler's
// second form).
func (s *Service) mobileBuildWakeVisit(builder *units.Unit, node *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
	if s == nil || builder == nil || node == nil || State(node.Phase) != State1 {
		return 0, false
	}
	if !isMobileBuilder(builder) || !s.approachArmed(builder, node) {
		return 0, false
	}
	wake := satisfied & orders.ApproachWakeGate
	if wake == 0 {
		return 0, false
	}
	if wake&approachWakeNoRoute != 0 {
		// The row's abandon arm, with its own two halves: the VISIT's `0x40` and
		// the reach verdict [04 R-ORD-01 §5][05 R-WORK-01 §2].
		if text, code := orders.MobileBuildUnreachableVisit(wake, s.outOfReach(builder, node)); code == 8 {
			s.raiseStatus(builder, statusCant, text)
			return code, true
		}
	}
	node.Phase = uint8(State2)
	node.DynamicGate = 0
	node.Deadline = -1
	return 0, false
}

// MobileBuildWakeVisitPublic exposes the phase-1 wake body for the approach
// regression tests and for session diagnostics [05 R-WORK-01 §13].
func (s *Service) MobileBuildWakeVisitPublic(builder *units.Unit, node *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
	return s.mobileBuildWakeVisit(builder, node, satisfied, tick)
}

// The three movement outcomes of [04 R-ORD-01 §0], named locally so the arms
// above read as the row does. orders.ApproachWakeGate is their union.
const (
	approachWakeArrived  uint32 = 0x20 // the follower reached the goal
	approachWakeNoRoute  uint32 = 0x40 // an empty route was published away from it
	approachWakeReleased uint32 = 0x80 // a goal object was released
)

// outOfReach is the reach expression of [05 R-WORK-01 §2] and nothing else: no
// wake gating, no state written. needsApproach above owns WHERE it is asked; the
// caller that runs the row's abandon arm asks it through OutOfReachPublic so
// that the arm's two halves — `satisfied & 0x40` and the reach verdict — are the
// row's own two halves [04 R-ORD-01 §5].
func (s *Service) outOfReach(builder *units.Unit, node *orders.Node) bool {
	if s == nil || builder == nil || builder.Def == nil || node == nil {
		return false
	}
	if builder.Def.BuildDistance == 0 || builder.Def.CanFly {
		return false
	}
	cx, cz, footX, footZ, ok := s.siteCentre(node)
	if !ok {
		cx, cz = node.GoalX, node.GoalZ
		footX, footZ = 1, 1
	}
	return !s.isWithinNanoRange(builder, cx, cz, footX, footZ)
}

// OutOfReachPublic exposes the reach expression for the session boundary that
// runs the mobile-build row's approach-failure arm [04 R-ORD-01 §5]
// [05 R-WORK-01 §2].
func (s *Service) OutOfReachPublic(builder *units.Unit, node *orders.Node) bool {
	return s.outOfReach(builder, node)
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
