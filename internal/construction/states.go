// The factory and mobile-build state machine: the five states of [05 "Factory
// production lifecycle"] and the mobile row's state 2, plus the mobile success
// epilogue.
//
// Moved out of factory.go by CL-5, which split that file by concern; the code
// is unchanged.

package construction

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func (s *Service) handleState0(factory *units.Unit, node *orders.Node, tick uint32) {
	// Mobile builds skip presentation clear of Goal (site is authoritative) [P0-I05]
	if isMobileBuild(node.ID) {
		// Mobile builds bypass the activate/yard-door handshake [P0-I05] and
		// enter their APPROACH phase, State1. That phase installs the rectangle
		// goal and arms `0xE0`; the placement phase, State2, is reached by the
		// phase advance the movement outcome drives [05 R-WORK-01 §13]. Carrying
		// the approach in the phase byte is what makes it save state: byte `0x09`
		// of the order record is the handler-private phase [08 R-SAVE-ORDER-01].
		node.Phase = uint8(State1)
		node.DynamicGate = 0
		node.Deadline = -1
		// The handler's setup path zeroes the blocked-area retry counter on
		// every (re)arm [04 §3.2][R-ORDER-02 §1].
		node.Param3 = 0
		return
	}
	// State 0 clears presentation payload [05].
	node.GoalX = 0
	node.GoalY = 0
	node.GoalZ = 0
	// Building class is the runtime status bit the allocator initializer sets
	// from the authored bmcode — not a yard-map, footprint or immobility
	// heuristic [05 "Factory production lifecycle"]. The previous predicate
	// guessed at all three and could disagree with retail on any definition
	// whose yard map, footprint and mobility did not line up with its bmcode.
	if factory.Flags&units.BuildingClassStatus == 0 {
		// Non-building contexts return the cancel-all result code; the handler
		// itself never touches the queue. Routing this through the queue's own
		// cancel path also keeps the queue's service bindings, which the
		// previous rebind-a-fresh-queue implementation silently dropped
		// [04 §3.3][05 "Queue subtraction"].
		if q := s.queueForUnit(factory); q != nil {
			q.CancelAll()
		}
		return
	}
	if int32(node.Param2) > 0 {
		// Positive count raises the activate edge and returns the advance
		// result: the phase increments and the pump restarts at the head in
		// the SAME pass, so state 1 runs immediately. There is no deadline and
		// no wake bit in state 0 — the previous one-tick deadline plus wake
		// bit 2 was invented, and it delayed every factory product by a tick
		// and armed a gate retail never arms [05 "Factory production
		// lifecycle"].
		//
		// The raise has to be a real edge, because the yard-door handshake is
		// entirely script-owned: the engine raises Activate and waits, and
		// nothing but the `Activate` script writes the in-build-stance bit
		// state 1 tests [05 "Factory production lifecycle"][R-P0-10]. A factory
		// authors neither `onoffable` nor `activatewhenbuilt`, so it is created
		// inactive [04 R-SPEC-01 §12] and stays there between queues — the
		// non-positive branch below lowers the edge once a queue has drained.
		// This raise is therefore already a true rising edge on a factory's
		// first product, and already suppressed on the same-pass restart from
		// state 4, which is what retail does [04 R-UNIT-06 §2].
		//
		s.activate(factory)
		node.Phase = uint8(State1)
		node.DynamicGate = 0
		node.Deadline = -1
		s.handleState1(factory, node, tick)
		return
	}
	// Non-positive count lowers the edge and frees the node.
	s.deactivate(factory)
	s.removeHead(factory, node)
}

// handleState1 advances only when script has set in-build-stance bit, otherwise waits with wake bit 2 [05].
func (s *Service) handleState1(factory *units.Unit, node *orders.Node, tick uint32) {
	if isMobileBuild(node.ID) {
		// Mobile builds skip the yard-door handshake [P0-I05]: State1 is their
		// APPROACH phase instead (approach.go). A builder with no approach term
		// falls straight through into State2 in this same visit.
		s.handleMobileApproach(factory, node, tick)
		return
	}
	// The ordinary order pump clears this gate only after a script event or
	// cancellation wakes the record. StepUnit must not bypass that wait by
	// polling its level on every visit [04 §3.3][04 R-COB-06].
	if node.DynamicGate != 0 {
		return
	}
	// State 1 is deliberately a level test with no timeout [05][R-P0-10].
	if factory.InBuildStance {
		node.Phase = uint8(State2)
		node.DynamicGate = 0
		node.Deadline = -1
		// The primary pump consumes an advance result immediately. Once the
		// authored stance handshake is already high, state 2 therefore runs in
		// this same pass; this is also what permits a counted successor to be
		// allocated without an invented idle tick [04 §4.7][R-FAC-01R].
		s.handleState2(factory, node, tick)
		return
	}
	// INBUILDSTANCE with cancel-current as its extra bit, and no deadline
	// [04 R-ORD-01 §1][04 R-ORD-01 §5].
	node.DynamicGate = InterruptCancel | units.PendingScriptTouched
	node.Deadline = -1
}

// isMobileBuild reports whether id is a mobile build descriptor [P0-I05][04 §3.1].
func isMobileBuild(id orders.ID) bool {
	name := orders.DescriptorFor(id).Name
	return name == MobileBuildOrder || name == VTOLMobileBuildOrder
}

// handleState2 implements C16-C18 [05][P0-I05].
// Factory products use exit-spot QueryBuildInfo [05 C16]; mobile products use
// the authoritative site anchor stored in Node.GoalX/Z [P0-I05] via QueueMobileBuild.
func (s *Service) handleState2(factory *units.Unit, node *orders.Node, tick uint32) {
	// Mobile build branch: site is authoritative Goal from QueueMobileBuild [P0-I05].
	if isMobileBuild(node.ID) {
		s.handleMobileState2(factory, node, tick)
		return
	}
	// An armed wait is a visit boundary. The blocked exit "schedules a retry in
	// exactly 15 ticks, sets wake bit 2, and stays"; the allocator refusal takes
	// the distinct 300-tick wait [05 "Factory production lifecycle"]. Both were
	// written to the node and then ignored, because this handler re-ran the
	// whole exit-spot query on every visit: the probe saw an admission attempt
	// on 1517, 1518, 1519 … instead of one every fifteenth tick. Consuming the
	// deadline here is the same visit boundary handleMobileState2 already
	// applies for its 30-tick blocked-area waits; the pump's wake semantics are
	// untouched [04 §3.3].
	if node.Deadline >= 0 {
		if tick < uint32(node.Deadline) {
			return
		}
		node.DynamicGate = 0
		node.Deadline = -1
	}
	// Resolve and classify before arming a retry. Missing definitions,
	// unresolved movement profiles, and malformed extents are permanent content
	// failures; only a valid footprint rejected by occupancy/terrain is the
	// established silent 15-tick retry [04 §6.4][05 C17].
	def := s.getProductDefForNode(node)
	if def == nil {
		s.rejectPermanent(factory, node, tick,
			fmt.Errorf("%w: product %q", world.ErrMissingPlacementDefinition, node.BuildDefKey))
		return
	}
	if _, err := placementRules(s, def); err != nil {
		s.rejectPermanent(factory, node, tick, err)
		return
	}
	// Exit-spot acquisition exact order [05 C16] is performed via QueryBuildInfo path for factory.
	// For handler we already have stored cell via QueryBuildInfo; but we need to compute again per tick?
	// Use lastService for catalog lookup.

	// Determine model for factory.
	var m *model.Model
	if s.ModelForFactory != nil {
		m = s.ModelForFactory(factory)
	}
	if m == nil {
		if binding := factory.COBBinding(); binding != nil {
			m = binding.Model
		}
	}
	// A missing current model is a production composition failure [R-P0-09].
	footX, footZ := int(def.FootprintX), int(def.FootprintZ)
	if footX <= 0 || footZ <= 0 {
		s.rejectPermanent(factory, node, tick,
			fmt.Errorf("%w: product %q has malformed footprint %dx%d", world.ErrMissingPlacementDefinition, def.UnitName, footX, footZ))
		return
	}
	extent, err := world.NewFootprintExtent(int32(footX), int32(footZ))
	if err != nil {
		s.rejectPermanent(factory, node, tick, err)
		return
	}
	buildPiece := -1
	var modelPosition world.ModelWorldPosition
	var ok bool
	if m != nil {
		buildPiece, modelPosition, ok = s.queryBuildPiecePosition(factory, m)
	} else {
		ok = false
	}
	if !ok {
		s.rejectPermanent(factory, node, tick,
			fmt.Errorf("factory QueryBuildInfo did not resolve an exit piece"))
		return
	}
	factoryPlacement, err := world.SnapFactoryPlacement(modelPosition, extent)
	if err != nil {
		s.rejectPermanent(factory, node, tick, err)
		return
	}
	cell := factoryPlacement.Anchor().Cell()
	// Store position on order node [05 C16] — already done in success epilogue storage but also store now.
	// For factory, Goal is overwritten with exit spot cell origin [05 C16].
	node.GoalX = world.CellToWorld(cell.X)
	node.GoalZ = world.CellToWorld(cell.Z)

	// Load product definition and attempt silent blocked revalidation [05 C17].
	var yard []world.YardCell
	if def.YardMap != "" {
		y, err := world.ParseYardMap(def.YardMap, footX, footZ)
		if err != nil {
			s.rejectPermanent(factory, node, tick, err)
			return
		}
		yard = y
	} else {
		// No yardmap for mobile products => use nil yard (inline terrain loop) but factory mode still checks occupancy.
		// For mobiles, the validator is inline terrain loop only when mode requests terrain checking; other modes accept immediately.
		// Factory production state2 passes its own class/state flag pair as mode and null self identity, so any foreign occupant rejects [05 C17].
		// For mobiles, we can still validate via occupancy check: if terrain occupied, fail.
		// Use empty yard to trigger occupancy check via ValidatePlacement's bits 1-2? But empty yard has no bits, so it would pass.
		// So for mobile without yard, we need to perform area occupancy check manually.
		// We will treat nil yard as mobile inline check: validate rectangle occupancy via terrain.
		yard = make([]world.YardCell, footX*footZ)
		// Fill with occupancy-checking bits: bits 1-2 set to reject any nonzero occupant.
		for i := range yard {
			yard[i] = 0x06 // bits 1-2 set [04 §6.2]
		}
	}
	// The exit-spot query runs in the inline terrain-check mode (mode value 1 at
	// every allocator call site), so the per-cell depth/slope gates apply at the
	// exit exactly as at a chosen site [04 R-FAC-02 §4][04 §6.4].
	if _, err := s.validatePlacement(factory.Handle, factoryPlacement.Rect(), def, yard, false); err != nil {
		s.recordAdmission(tick, factory, def.UnitName, factoryPlacement.Rect(), AdmissionBlockedTransiently, err)
		s.yieldFactoryExit(factory, factoryPlacement.Rect(), nil, tick)
		// Silent blocked revalidation: retry in exactly 15 ticks, stays — no
		// message/sound/allocation; repeats every 15 while obstructed; NO
		// timeout [05 C17]. Wake mask is bits {1,2}: schedule(node,15) sets
		// bit 1 + deadline and the caller adds bit 2
		// [05 "Factory production lifecycle"].
		node.DynamicGate = WakeBit1 | WakeBit2
		node.Deadline = int32(tick + 15)
		// No message, no allocation — silent.
		//
		// This silent retry, the yard-close admission gate (YardOpenTransaction)
		// and the ordinary blocked mover are the WHOLE of retail's policy for a
		// crowded factory exit [04 R-FAC-02 §5][04 R-FAC-02 §6]. Nothing here
		// asks the units in the way to move: COB port 19 (`BUGGER_OFF`) has no
		// engine reader at all — a census of the second state byte's bit 3 finds
		// only the get/set port arms, the creation clear and the save writer
		// [04 R-COB-05]. Do not add a scatter, push or crowd-avoidance rule
		// keyed on that flag.
		return
	}
	s.recordAdmission(tick, factory, def.UnitName, factoryPlacement.Rect(), AdmissionAdmitted, nil)

	// On validation success, allocator creates unit AT exit spot [05 C18].
	product, err := s.allocateNanoframe(factory, def, factoryPlacement.Rect(), factoryPlacement.ModelPosition())
	if err != nil {
		// Allocator refusal prints verbatim "Unable to create any more units", retries in exactly 300 ticks (not randomized), stays state2 [05 C18].
		s.logMessage(fmt.Sprintf("construction: allocation refused (%v)", err))
		s.logMessage(ErrLimitMessage)
		// "not created → status 7 `Unable to create any more units`, deadline
		// 300, hold" [04 R-ORD-01 §5][05 "the build-order caption census"].
		s.raiseStatus(factory, statusCant, ErrLimitMessage)
		node.DynamicGate = WakeBit2 // F6b: the allocator refusal wakes on bit 2 [05 "Factory production lifecycle"]
		node.Deadline = int32(tick + 300)
		// Stay in state2.
		return
	}
	// Success epilogue [05 C18].
	if err := s.successEpilogue(factory, node, product, cell, buildPiece, tick); err != nil {
		// successEpilogue's own comment already calls its failure "an explicit
		// rejected allocation" [04 R-FAC-02 §1]; a rejected allocation is a
		// product that never existed [04 R-FAC-02 §3], so it is freed here, not
		// killed. The Destroy this replaces filed a kill record and a death cause
		// for a unit retail never created.
		s.freeNeverExistedProduct(product)
		s.rejectPermanent(factory, node, tick, err)
	}
}

// handleMobileState2 implements mobile build placement at the authoritative site anchor [P0-I05][05 "Factory production lifecycle"].
// Mobile payload carries site in Node.GoalX/Z (world coords) via QueueMobileBuild [P0-I05].
// Validation uses the product's yard at the snapped site, not the factory exit spot.
func (s *Service) handleMobileState2(builder *units.Unit, node *orders.Node, tick uint32) {
	switch s.mobilePlacementVisit(builder, node, tick) {
	case 1:
		node.Phase = uint8(State3)
	case 8:
		s.removeHead(builder, node)
	}
}

// mobilePlacementVisit shares the ground and air placement effects while
// leaving advancement/removal to the owning dispatcher [04 R-ORD-02 §2].
func (s *Service) mobilePlacementVisit(builder *units.Unit, node *orders.Node, tick uint32) orders.Code {
	// Armed waits are visit boundaries: while the record's deadline is in the
	// future the handler is not visited, and on arrival the wait is consumed
	// (the pump's deadline rule — deadline arrived clears it and re-dispatches
	// — [04 §3.3]). The blocked-area budget depends on this spacing: its waits
	// are EXACTLY 30 ticks [R-ORDER-02 §1].
	if node.Deadline >= 0 {
		if tick < uint32(node.Deadline) {
			return 2
		}
		node.DynamicGate = 0
		node.Deadline = -1
	}
	// The approach is behind this phase, not inside it. State1 owns the walk and
	// the `0xE0` wait; a record only reaches State2 once the movement outcome has
	// retired it [05 R-WORK-01 §13] (approach.go). What survives here is the
	// clear-the-site term, which is a placement question rather than a reach one:
	// the builder's own footprint may still cover a cell of the site it is about
	// to stamp [04 R-COLL-01 §2]. Factory-class builders are their own yard and
	// are unaffected.
	if isMobileBuilder(builder) && s.Movement != nil && builder.Def != nil && builder.Def.BuildDistance != 0 {
		if !s.mustClearSite(builder, node) {
			// Only stop moving once the builder's own footprint no longer
			// covers the site; otherwise the walk installed above stays live
			// while the validator below rejects on the builder's own
			// occupancy and the blocked-area budget runs [R-ORDER-02 §1]
			// [04 R-COLL-01 §2].
			s.clearWalk(builder)
			node.MoveState = orders.MoveArrived
		}
	}
	// Resolve and classify before arming any retry, exactly as the factory twin
	// in handleState2 does. A product the catalog cannot resolve is a content
	// failure, not a crowded site: retail has no arm to copy here, because its
	// record carries a product definition INDEX and the `MobileBuild` body
	// indexes the definition table with it without a bounds test, so the load
	// cannot yield nothing [04 R-ORD-01 §5][04 R-ORD-01 §18]. The only
	// handler-level refusals that row describes are the blocked-area budget
	// ([R-ORDER-02 §1]) and the allocator's `Unable to create any more units`
	// hold — neither is this case. So the retention is the whole of the
	// response: a permanent-definition admission diagnostic, no caption, and no
	// invented queue transition. The 15-tick retry this replaces was an
	// invented transition AND silent, which left a catalog mismatch spinning
	// the record forever with nothing for the player or a log to see.
	def := s.getProductDefForNode(node)
	if def == nil {
		s.rejectPermanent(builder, node, tick,
			fmt.Errorf("%w: product %q", world.ErrMissingPlacementDefinition, node.BuildDefKey))
		return 2
	}
	footX, footZ := int(def.FootprintX), int(def.FootprintZ)
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	// Site anchor is authoritative Goal from QueueMobileBuild [P0-I05].
	extent, err := world.NewFootprintExtent(int32(footX), int32(footZ))
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit2, int32(tick+15)
		return 2
	}
	anchor, err := world.SnapFootprintAnchor(node.GoalX, node.GoalZ, extent)
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit1|WakeBit2, int32(tick+15)
		return 2
	}
	rect, err := world.NewFootprintRect(anchor, extent)
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit1|WakeBit2, int32(tick+15)
		return 2
	}
	cell := anchor.Cell()
	// Do not overwrite Goal: keep original clicked site for determinism and tests that assert Goal equals clicked site [P0-I05].
	// Validation at snapped cell [05 C17] with null self identity (mobile builders place at site).
	var yard []world.YardCell
	if def.YardMap != "" {
		y, err := world.ParseYardMap(def.YardMap, footX, footZ)
		if err == nil {
			yard = y
		}
	} else {
		yard = make([]world.YardCell, footX*footZ)
		for i := range yard {
			yard[i] = 0x06 // bits 1-2 reject any occupant [04 §6.2]
		}
	}
	result, err := s.validatePlacement(builder.Handle, rect, def, yard, false)
	if err != nil {
		// [R-ORDER-02 §1] Blocked-area budget, replacing the factory-style
		// silent 15-tick retry: each blocked visit notifies "Waiting for
		// target area to clear", increments the record's third parameter
		// ([04 §3.2] assigns it to the retry counter), and waits EXACTLY 30
		// ticks with no random draw while the counter is at most 10; the
		// first blocked visit with the counter above 10 notifies "Target
		// area was blocked" and abandons the order (code 8, remove).
		// Do not request another clearance move on the terminal give-up visit.
		if node.Param3 <= 10 {
			s.yieldConstructionSite(builder, rect, tick)
		}
		text, code := orders.MobileBuildBlockedVisit(node, tick)
		// The same two captions are status kind 7 on the builder
		// [04 R-ORD-01 §5][05 "the build-order caption census"]. Only the first
		// blocked visit and the over-limit one carry text; a silent visit
		// raises nothing, because an empty kind-7 status would resolve to the
		// slot's static speech and replay the cue every retry.
		if text != "" {
			s.notifyStatus(text)
			s.raiseStatus(builder, statusCant, text)
		}
		return code
	}
	siteY := builder.Y
	if s.Terrain != nil {
		siteY = numeric.Fixed(int64(result.SiteHeight) * numeric.FractionOne)
	}
	mobilePlacement, err := world.SnapMobilePlacement(node.GoalX, siteY, node.GoalZ, extent)
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit1|WakeBit2, int32(tick+15)
		return 2
	}
	product, err := s.allocateNanoframe(builder, def, mobilePlacement.Rect(), mobilePlacement.ModelPosition())
	if err != nil {
		s.logMessage(ErrLimitMessage)
		s.raiseStatus(builder, statusCant, ErrLimitMessage) // [04 R-ORD-01 §5]
		// The air row abandons on allocation refusal; only the ground row
		// holds for 300 ticks [04 R-ORD-02 §2][04 R-ORD-01 §5].
		if node.ID == vtolMobileBuildRow {
			return 8
		}

		node.DynamicGate = WakeBit2
		node.Deadline = int32(tick + 300)
		return 2
	}
	// For mobile, success epilogue reuses factory helper but with builder as factory and cell as site cell.
	// It stores cell origin as Goal? We preserve original Goal for site authoritative test, so store snapshot separately?
	// Keep Goal as site, but successEpilogue will overwrite Goal with cell origin. Preserve site in a separate snapshot?
	// Instead call mobile-specific epilogue that keeps Goal as site and uses cell for product creation.
	s.successEpilogueMobile(builder, node, product, cell, tick)
	return 1
}

// successEpilogueMobile is like successEpilogue but preserves the authoritative site Goal [P0-I05].
func (s *Service) successEpilogueMobile(builder *units.Unit, node *orders.Node, product *units.Unit, cell world.Cell, tick uint32) {
	// Preserve original Goal site for test assertion that structure appears at clicked location [P0-I05].
	// The product's world position is at cell origin, which corresponds to site snapped with half-extent.
	// Node.Goal remains the clicked site; we do not overwrite it with cell origin.
	// The same target-reference registration as the factory epilogue: the record
	// binds its product here and the removal walk delivers mask 8 through it
	// [04 R-ORD-01 §6][05 "Build request and factory queue behavior"].
	productHandle := product.Handle
	node.BindTarget(productHandle)
	// `MobileBuild` phase 1's created arm: "status 9 with `Starting
	// construction`" [04 R-ORD-01 §5].
	s.logMessage("Starting construction")
	s.raiseStatus(builder, statusBuild, "Starting construction")
	s.SetBuilderLink(productHandle, builder.Handle)
	// Mobile placement keeps the product's authored standing orders. Only
	// factory production copies them at allocation [04 R-STANCE-01 §6].
	getBuiltID := orders.Lookup("GetBuilt")
	if getBuiltID != 0 {
		pq := orders.BindQueueBinding(product, s.OrderBinding)
		// The product can reach the order sweep before its construction visit.
		// Install its lifecycle now, as the factory epilogue does, so that first
		// dispatch follows GetBuilt rather than the missing-handler fallback.
		s.registerGetBuilt(pq)
		s.RegisterOrderHandlers(pq)
		// Queued, like the factory's [04 R-FAC-02 §1]; the record's bit 5 puts
		// it at the head of the nanoframe's own queue [04 R-ORD-01 §13].
		pq.Push(getBuiltID, productRecord(product, tick, orders.Node{Target: builder.Handle, Param2: 0, QueuedIssue: true}))
	}
	// No heading snap here. The question this site used to record — whether
	// retail rotates the unit or leaves the turn to the script — is answered:
	// "A mobile builder's heading toward the selected build goal is produced by
	// ordinary movement steering" [04 §2.3b], and the script turns its torso
	// with the relative bearing the emitter below passes as the first
	// StartBuilding argument [04 R-CB-01 §3]. Writing Move.Heading straight
	// from the site delta bypassed the turn clamp and, once the mover's records
	// carry the allocated heading, would also fight the steering that owns it.
	//
	// The MobileBuild/VTOL_MobileBuild handler's StartBuilding emission is the
	// order-record emitter, one of the nine nanolathe/assist sites [R-ORDER-02
	// §2]: it arranges the name-form StartBuilding and sets the record's
	// StopBuilding-pending flag so cleanup emits the counterpart on every
	// removal path. The slot-form heading variant is the construction-command
	// producer, is not among the nine call sites, and writes no flag
	// (corrected [04 §5.3]) — so it is not used here.
	orders.EmitStartBuilding(builder, node)
	if s != nil && s.OnRefresh != nil {
		s.OnRefresh(builder)
	}
	// Keep cell for product creation already done; no need to store again.
	_ = cell
}

// vtolBuildVisit keeps the aircraft's six retail phases on the saved order
// record. In particular phase 1 consumes the climb outcome by installing the
// site marker; phase 2 alone consumes that marker's arrival before placement
// [04 R-ORD-02 §2][08 R-SAVE-ORDER-01]. The primary pump owns all advances.
func (s *Service) vtolBuildVisit(builder *units.Unit, node *orders.Node, satisfied, tick uint32) (orders.Code, bool) {
	switch node.Phase {
	case 0, 1:
		if s.Movement == nil {
			return 7, true
		}
		return s.Movement.VisitAirBuildApproach(builder, node, satisfied, tick), true
	case 2:
		if satisfied&approachWakeNoRoute != 0 {
			return 8, true
		}
		if s.getProductDefForNode(node) == nil {
			// The shared visit answers an unresolvable product with retention
			// and a hold that arms nothing, which is sound for the ground row
			// because its caller is the per-unit step. This row's caller is the
			// primary pump, where a hold with no gate reloads the same head
			// forever inside one tick [04 R-ORD-01 §10]. Report the same
			// diagnostic and tell the pump the record did not advance, so the
			// retention is one visit per tick here too.
			s.rejectPermanent(builder, node, tick,
				fmt.Errorf("%w: product %q", world.ErrMissingPlacementDefinition, node.BuildDefKey))
			return 0, false
		}
		return s.mobilePlacementVisit(builder, node, tick), true
	case 3, 4:
		if node.Phase == 3 && !builder.InBuildStance {
			// The air row discards the stance helper's result, retaining only
			// its gate write; work still runs on this visit [04 R-ORD-02 §2].
			node.DynamicGate = 0xE
		}
		product := s.World.Unit(node.Target)
		if product == nil || builder.Def == nil {
			return 7, true
		}
		if s.Movement != nil {
			s.Movement.VisitAirBuildWork(builder, node, tick)
		}
		if s.applyWorkStep(builder, product, tick) {
			s.emitAcceptedNano(tick, builder, product)
		}
		// Air work has no reveal-deadline stamp. The same zero test applies
		// after accepted, refused and already-complete work [05 R-WORK-01 §1].
		if product.Remaining == 0 {
			s.applyCompletionPosture(product)
			return 1, true
		}
		node.Deadline = int32(tick + 1)
		node.DynamicGate |= 0xB // deadline setter's bit 0, plus cancel/removed
		return 2, true
	case 5:
		s.raiseStatus(builder, statusComplete, "Building complete")
		delete(s.builderLinks, node.Target)
		if s.OnRefresh != nil {
			s.OnRefresh(builder)
		}
		return 5, true
	default:
		return 7, true
	}
}

// handleState3 is the work loop [05].
func (s *Service) handleState3(factory *units.Unit, node *orders.Node, tick uint32) {
	// With product attached, shared work helper runs with floor(workerTime/30); else fall through to cancel-all.
	var product *units.Unit
	if node.Target != 0 && s.World != nil {
		product = s.World.Unit(node.Target)
	}
	if product == nil {
		// Node that has lost its product falls through to result 7 — losing product cancels ALL factory orders [05].
		//
		// A product DESTROYED mid-build no longer reaches here: the removal walk
		// delivers the target-removed notice (mask 8) and unlinks the reference,
		// and the pump tests that interrupt before the state machine, so the
		// established construction-stopped body runs instead — one count
		// decrement and a surviving node [04 R-ORD-01 §6][05 C22]. What is left
		// for this arm is a record that reaches state 3 with no reference at all.
		q := s.queueForUnit(factory)
		if q != nil {
			newQ := orders.NewQueueWith(nil, nil)
			newQ.SetBinding(q.Binding())
			orders.BindQueue(factory, newQ)
		}
		return
	}
	if factory.Def == nil {
		// No worker time.
		node.DynamicGate = WakeBit1 | WakeBit3
		node.Deadline = int32(tick + 1)
		return
	}
	committed := s.applyWorkStep(factory, product, tick)
	if committed {
		// Query and emit only after the two-resource admission and authoritative
		// state update have committed [R-P0-06]. Rejected work reaches no query.
		s.emitAcceptedNano(tick, factory, product)
		// "Beside the spray" the reveal stamp is written by ten handler sites, and
		// `MobileBuild`'s work phase is one of them at `tick + 300`; `BuildingBuild`
		// is named explicitly as one that never writes it, even though this same
		// loop is its phase-3 work visit too [04 R-ORD-01 §5 "The reveal stamp"].
		// The write is an outright store to the one shared reveal/cloak deadline
		// field, never a maximum [03 R-VIS-01 §6], and its only reader is the
		// cloak debit gate [05 R-ECO-01 §9].
		if isMobileBuild(node.ID) {
			factory.RevealDeadline = tick + 300
		}
	}
	// The stored fraction's zero test, not this step's committed return, is what
	// governs the completion transition: [05 R-WORK-01 §1] places it "on both
	// arms and also on the admission-refused path", after every exit of the
	// shared step. State 4 then repeats the transition idempotently
	// [04 §4.7][R-FAC-01R].
	//
	// Corrected (WU-19-132). The zero test used to sit behind an early return on
	// a non-committed step, and §1's FIRST line makes a step on an already-zero
	// fraction return not-committed. So when an ASSISTING builder's step stored
	// the zero — a commander guarding its own factory, the ordinary opening —
	// this node never saw the zero: it retried in state 3 for the rest of the
	// battle, the factory's building edge never fell, no successor product was
	// ever allocated, and the finished product never reached the session's
	// completion hook. That hook is what gives a new unit its mover state, so
	// the product stood inside the factory footprint holding no occupancy,
	// unable to walk out and unable to answer a Move order
	// [05 "Factory production lifecycle"][04 R-FAC-02 §3].
	if product.Remaining == 0 {
		s.applyCompletionPosture(product)
		node.Phase = uint8(State4)
		s.handleState4(factory, node, tick)
		return
	}
	// Denied work leaves the remaining fraction untouched, and an accepted step
	// that did not finish the product retries the same way: one tick later with
	// wake bits 1 and 3 [05 "Two-resource admission"][05].
	node.DynamicGate = WakeBit1 | WakeBit3
	node.Deadline = int32(tick + 1)
}

func (s *Service) handleState4(factory *units.Unit, node *orders.Node, tick uint32) {
	var product *units.Unit
	if node.Target != 0 && s.World != nil {
		product = s.World.Unit(node.Target)
	}
	// State 3's zero-remaining helper has already completed the product (and may
	// have raised its Activate edge). State 4 now lowers the factory building
	// edge before repeating the product transition idempotently [R-FAC-01R].
	// The repeated transition observes that the first transition already
	// detached the mobile product and performs no position write
	// [04 R-FAC-02 §3].
	// Trigger BuildUnitType only on local 30-tick deadline [P0-14].
	//
	// SETTLED (WU-19-166): "Interrupt masks 2/8 bodies known, producers
	// unlocated [P0-14]" stood here as an accepted-placeholder marker. Both producers have since been located and
	// both are wired in this package. Mask 2 (cancel-current) is delivered by
	// the removal paths themselves: "node cleanup invokes the handler with mask
	// 2 whenever the removed record's state-mask byte still has bit 1 set, so
	// the producers of the cancel notification are exactly the removal paths
	// (counted cancel, non-queued purge, pump removals, death/capture
	// teardown)" [04 §3.3][R-ORDER-02 §2]. Mask 8 is *target removed*: a
	// record's target smart-reference raises `0x8` into the record's pending
	// word when the referenced unit is destroyed, and the reference is then
	// unlinked [04 R-ORD-01 §6] — which is Service.NotifyProductRemoved, the
	// path TestTargetRemovedNoticeReachesTheFactoryInterrupt locks. [04 §3.3]'s
	// older paragraph, which read the construction-stopped wake as having no
	// located producer because no instruction ORs the bit directly, now carries
	// that supersession in place: the raise goes through the reference method,
	// and for a factory record the target reference IS the product.
	// Phase 4's first act is the completion status, ahead of the falling
	// StartBuilding edge and the completion transition: `BuildingBuild` emits
	// status 8 with no text, so the slot's default caption `Nanolathe Complete`
	// stands, and `MobileBuild` emits status 8 with `Building complete`
	// [04 R-ORD-01 §5][03 §8.3]. This is the factory's "unit ready" voice.
	if isMobileBuild(node.ID) {
		s.raiseStatus(factory, statusComplete, "Building complete")
	} else {
		s.raiseStatus(factory, statusComplete, "")
	}
	s.stopBuilding(factory)
	if product != nil {
		// The second transition must not create a duplicate Activate callback.
		s.applyCompletionPosture(product)
		// Clear the construction presentation payload before completion bookkeeping.
		node.GoalX, node.GoalY, node.GoalZ = 0, 0, 0
		// Only BuildingBuild repeats through its count. MobileBuild and
		// VTOL_MobileBuild are ordinary one-shot completions even when a
		// coalesced record carries Param2 greater than one [04 R-ORD-01 §5].
		if !isMobileBuild(node.ID) && node.Param2 > 0 {
			node.Param2--
		}
		// Clear builder/product link on completion [P0-14]; the death/capture
		// teardown does not walk these links, so they leak there instead.
		if product != nil {
			delete(s.builderLinks, product.Handle)
		}
		// The order-panel refresh keys on the BUILDER's identity: the compare
		// is against the builder's identity word and the panel refreshed is the
		// builder's — for a factory, its build page, which is the same step
		// [04 R-FAC-02 §3] calls the queue-count label refresh
		// [04 R-SPEC-01 §12]. The product refresh that stood beside it was a
		// second presentation call retail never makes.
		if s != nil && s.OnRefresh != nil {
			s.OnRefresh(factory)
		}
		// BuildingBuild result 0 restarts state 0 in this same primary-pump
		// pass. The count test there is authoritative: an empty count lowers
		// activation after StopBuilding, while a successor remains active and
		// can allocate immediately through the already-high stance gate
		// [R-FAC-01R].
		node.Phase = uint8(State0)
		node.DynamicGate = 0
		node.Deadline = -1
		node.BindTarget(0)
		if isMobileBuild(node.ID) {
			// Phase-4 MobileBuild completion is an ordinary completion result:
			// cleanup and remove the record once, independent of Param2
			// [04 R-ORD-01 §5].
			s.removeHead(factory, node)
		} else {
			s.handleState0(factory, node, tick)
		}
	} else {
		if !isMobileBuild(node.ID) && node.Param2 > 0 {
			node.Param2--
		}
		node.Phase = uint8(State0)
		node.DynamicGate = 0
		node.Deadline = -1
		node.BindTarget(0)
		if isMobileBuild(node.ID) {
			s.removeHead(factory, node)
		} else {
			s.handleState0(factory, node, tick)
		}
	}
}

// ---------------------------------------------------------------------------
// The two interrupt producers [05 "Build request and factory queue behavior"]
// ---------------------------------------------------------------------------
//
// Neither mask is raised by a UI or network command layer, which is where the
// superseded reading looked for them. Both are order-record NOTICES:
//
//   - mask 2, cancel-current, is the cleanup notice of [04 R-ORDER-02 §2]. It
//     is never raised into the pending word at all: the record-removal paths
//     invoke the operation handler with the mask when the record's DYNAMIC gate
//     still holds bit 1 at removal. That is why BuildingBuild's static mask
//     carries no bit 1 and the state machine arms it dynamically while a
//     product is attached (states 3 and 4's `WakeBit1`). DeliverCancelNotice
//     below is this handler's receiver.
//
//   - mask 8, construction stopped, is the target-removed notice of
//     [04 R-ORD-01 §6]. The record binds its product as its target reference at
//     creation (`node.Target = productHandle` in the two success epilogues) and
//     releases it at completion or cancel, so the unit-removal walk delivers
//     0x8 to the factory and unlinks the reference exactly when the product
//     under construction is destroyed. Nothing else raises bit 3 into the
//     pending word ([04 R-ORD-01 §0], [04 §3.3]). NotifyProductRemoved below is
//     that walk's construction-side arm.
//
// Both are Established.
