// Nanoframe allocation: placement rules and validation, the allocator itself,
// the frame's initial state, and the success epilogue that hands the product
// its queue [05 "Unit creation and limits"][04 R-FAC-02 §4].
//
// Moved out of factory.go by CL-5, which split that file by concern; the code
// is unchanged.

package construction

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func placementRules(s *Service, def *content.UnitDef) (world.PlacementRules, error) {
	rules, err := world.PlacementRulesForUnit(nil, def)
	if s != nil {
		rules, err = world.PlacementRulesForUnit(s.Catalog, def)
	}
	if err != nil {
		return world.PlacementRules{}, fmt.Errorf("construction: %w", err)
	}
	return rules, nil
}

// validatePlacement runs the shared placement legality query for construction.
// self is the identity exempted from occupancy rejection — the producing
// factory at a factory exit or the walking builder at its own site ([05
// "Factory production lifecycle"], [04 §6.4] "a nonzero occupant other than
// the passed self identity rejects"). skipAggregates is false for both the
// factory exit and the chosen site — retail passes mode 1 at every allocator
// call site [04 R-FAC-02 §4] (see PlacementQuery.SkipTerrainAggregates).
// Completed buildings retain their yard-selected ground words, so this shared
// query sees them without a second rectangle registry [04 R-COLL-01 §3].
func (s *Service) validatePlacement(self pool.Handle, rect world.FootprintRect, def *content.UnitDef, yard []world.YardCell, skipAggregates bool) (world.PlacementResult, error) {
	if s == nil || s.Terrain == nil {
		return world.PlacementResult{}, fmt.Errorf("construction: placement terrain unavailable")
	}
	// One rule, one identity, both halves of Nanolathe's split ground word.
	// Retail has a single occupancy word per cell, written by ground movers and
	// by building-class units alike, and every one of the validator's placement
	// callers passes a NULL self identity — the census finds no exemption for a
	// producer, a builder, or a product [04 R-COLL-01 §2][04 R-COLL-01 §6]
	// [04 R-FAC-02 §5]. What makes a legal factory exit legal is the producer no
	// longer holding the cells its open yard released, not an identity
	// exemption; and what stops a mobile builder stamping a nanoframe onto the
	// cells it is itself standing on is that same null identity applied to the
	// mover half of the word, which mobileOccupancy supplies (approach.go).
	// The `self` argument is retained in the signature for callers and
	// diagnostics; it is deliberately not an exemption.
	rules, err := placementRules(s, def)
	if err != nil {
		return world.PlacementResult{}, err
	}
	return s.Terrain.CheckPlacement(world.PlacementQuery{
		Rect:                  rect,
		Yard:                  yard,
		Rules:                 rules,
		Self:                  0,
		Mobile:                def != nil && def.BMCode != 0,
		SkipTerrainAggregates: skipAggregates,
	})
}

// ---------------------------------------------------------------------------
// Allocation and success epilogue [05 C18].
// ---------------------------------------------------------------------------

func (s *Service) allocateNanoframe(factory *units.Unit, def *content.UnitDef, rect world.FootprintRect, position world.ModelWorldPosition) (*units.Unit, error) {
	if def == nil {
		return nil, fmt.Errorf("construction: nil product def")
	}
	// Enforce per-def limit ONLY at allocation [05 C23][05 "Unit creation and limits"].
	// Per-def limit -1 is the unlimited sentinel [P0-15][P0-16]; a 0 is treated
	// as unlimited too, because in a single-player build it can only be Go's
	// zero value — [05 R-SHARE-01 §9] establishes the definition parser writes
	// -1 into every definition and that the one writer of 0 is the multiplayer
	// restriction apply step, which a skirmish or campaign battle never runs.
	// See perDefLimit for the unimplemented half.
	if lim, limited := perDefLimit(def); limited && lim > 0 {
		cnt := 0
		if s.World != nil {
			for _, u := range s.World.Iter() {
				if u != nil && u.Alive && u.Def != nil && int(u.Owner) == int(factory.Owner) && u.Def.UnitName == def.UnitName {
					cnt++
				}
			}
		}
		if int32(cnt) >= lim {
			return nil, ExhaustionError()
		}
	}
	if !s.CheckLimit(factory, def.UnitName) {
		return nil, ExhaustionError() // verbatim [05 C18] via hook [P0-I16]
	}
	if s.Allocator != nil {
		// Hook for tests: create at the authored model/world exit position.
		prod, err := s.Allocator(factory.Owner, def, position.X(), position.Y(), position.Z())
		if err != nil {
			return nil, err
		}
		if prod == nil || prod.Handle == 0 {
			return nil, fmt.Errorf("construction: allocator returned invalid unit handle")
		}
		if _, exists := s.placements[prod.Handle]; exists {
			return nil, fmt.Errorf("construction: allocator reused reserved unit handle %d", prod.Handle)
		}
		if s.World != nil {
			if existing := s.World.Unit(prod.Handle); existing != nil && existing != prod {
				return nil, fmt.Errorf("construction: allocator reused live unit handle %d", prod.Handle)
			}
		}
		initializeNanoframe(prod, def)
		if prod != nil {
			if err := s.reservePlacement(prod.Handle, def, rect); err != nil {
				// Same never-existed unwind as the world path below
				// [04 R-FAC-02 §3]. The bare `Alive = false` this replaces left
				// the pool slot allocated and both counters bumped, so the
				// product went on being counted by the per-definition census.
				s.freeNeverExistedProduct(prod)
				return nil, err
			}
			s.recordPlacement(prod.Handle, def, rect)
		}
		return prod, nil
	}
	if s.World == nil {
		return nil, fmt.Errorf("construction: no world/allocator")
	}
	// Create at the authored exit model/world position. The product is a
	// nanoframe, not an already-built unit, so it takes the creation service's
	// unbuilt form and `activatewhenbuilt` does not raise its activation edge
	// here — completion does [04 R-SPEC-01 §12].
	h, err := s.World.CreateNanoframe(def, factory.Owner, position.X(), position.Y(), position.Z())
	if err != nil {
		return nil, err
	}
	prod := s.World.Unit(h)
	if prod == nil {
		return nil, fmt.Errorf("construction: failed to get product")
	}
	if err := s.reservePlacement(prod.Handle, def, rect); err != nil {
		// The refused product never existed [04 R-FAC-02 §3]: it is freed, not
		// killed. A Destroy here filed a death with no damage packet behind it —
		// a kill record, a death cause and a decremented live count against a
		// units-ever-created the allocation had already bumped.
		s.freeNeverExistedProduct(prod)
		return nil, err
	}
	s.recordPlacement(prod.Handle, def, rect)
	initializeNanoframe(prod, def)
	return prod, nil
}

// freeNeverExistedProduct unwinds a nanoframe allocation this service completed
// but could not admit.
//
// [04 R-FAC-02 §3] lists the abnormal ends of factory production. Cancel-current
// and a dying factory are real deaths — the first kills the product with damage
// cause 9 ([05 R-WORK-01 §1]), the second kills every unit on the cargo list.
// The third is not: "A product freed by pool exhaustion or limit never existed."
// No death, no kill record, no death cause, no counters — the slot goes back to
// the pool, because retail never got past the allocator's refusal at all
// ([05 R-SHARE-01 §8] steps 1-4 return the null unit; only step 5 counts).
//
// The order is release then free: the ground words and yard marks this identity
// stamped come off first, since the slot is lowest-free reusable in the same
// tick and a stale stamp would be read against the next occupant
// [04 R-COLL-01 §4][P0-16 §6.3]. units.FreeNeverCreated owns the counter half.
func (s *Service) freeNeverExistedProduct(prod *units.Unit) {
	if prod == nil {
		return
	}
	handle := prod.Handle
	s.ReleasePlacement(handle)
	s.ClearBuilderLink(handle)
	if s.getBuiltLinks != nil {
		delete(s.getBuiltLinks, handle)
	}
	if s.World != nil && s.World.Unit(handle) == prod {
		s.World.FreeNeverCreated(handle)
		return
	}
	// A caller-supplied Allocator hook may hand back a record the world does not
	// own (the package's synthetic fixtures do). There is no slot to return then;
	// clearing the alive bit is the whole of the unwind.
	prod.Alive = false
}

func initializeNanoframe(prod *units.Unit, def *content.UnitDef) {
	if prod == nil || def == nil {
		return
	}
	// [05 "Nanoframe allocation"]: every allocation path publishes the same
	// unfinished instance before builder/product linking.
	prod.Remaining = 1
	prod.Health = 0
	prod.MaxHealth = int32(def.MaxDamage)
	prod.InBuildStance = false
	prod.Alive = true
	// A nanoframe is INACTIVE. The world's own allocation path no longer raises
	// the edge for a frame (World.CreateNanoframe above), so for that path this
	// is a no-op. It is kept because the Service.Allocator hook is
	// caller-supplied and the fixtures behind it allocate through the
	// already-built World.Create, which does raise; without a lowering here such
	// a frame would reach completion already active and completion's raise would
	// not be an edge, so `Activate` would never start [04 R-UNIT-06 §2].
	//
	// It goes through the edge setter, not a direct write. A raise that already
	// happened has already started the unit's `Activate` script, and a stock
	// extractor's `Activate` spins its arms until `Deactivate` stops it: only a
	// real falling edge runs `Deactivate` and stops the animation. Clearing the
	// bit by hand leaves the script running, which is the defect this replaces.
	prod.SetActivationEdge(false)
}

// productRecord stamps the two order-record fields that every ordinary issuer
// writes and that a bare `orders.Node{}` literal leaves at zero, for a record
// this package pushes onto a PRODUCT's own queue.
//
// The owning unit is one of the order record's own fields [04 §3.2]; a handler
// body is handed it alongside the record [04 R-ORD-01 §1]; and the movement
// controller's single goal slot is addressed BY it [04 R-ORD-01 §9]. Every
// record this package puts on a product is the PRODUCT's: [04 R-FAC-02 §4]
// states the product's first order is `BeCarried`, its second is `GetBuilt`,
// and that `GetBuilt` itself resolves the builder's `QMove`/`QPatrol` records
// "against the product ... and inserted queued on the product", with `Park`
// inserted in their place when nothing was. The factory is the record's TARGET
// or its source, never its owner.
//
// Left null, every such record on every product in the battle named the same
// controller slot at handle 0, and `Queue.ownerUnit` could not resolve the
// record's unit at all, so every owner-side step of the record destructor was
// a no-op. The measured one is the slot return: [04 R-UNIT-06 §5 part 3] has
// the destructor hand all three weapon slots back — targets cleared, autonomy
// bit raised — for every removed record whose static-mask copy lacks bit 16,
// and with a null owner a product's `BeCarried`/`GetBuilt`/rally record
// returned nothing. Two further owner-side steps fail the same way and were
// simply not exercised on the scenarios measured for this unit: the cancel
// notification and the `StopBuilding` counterpart. So does the air installer,
// which refuses an install whose resolved unit does not carry `canfly` and can
// resolve no unit from a null owner. This is the same defect WU-19-69 fixed
// for the mission-script interpreter, in the same shape.
//
// CreationTick is the creation-tick snapshot [04 §3.2]: the tick current at
// the handler visit that pushes the record. Unlike the mission interpreter's,
// which runs once before the first tick is stepped, these visits are ordinary
// pumped ones, so the caller passes its own tick.
//
// It writes nothing else: the goal triple, the target smart-reference and the
// parameter words stay the pushing site's.
func productRecord(product *units.Unit, tick uint32, n orders.Node) orders.Node {
	if product == nil {
		return n
	}
	n.Owner = product.Handle
	n.CreationTick = tick
	return n
}

// successEpilogue performs the success sequence after allocation [05 C18].
func (s *Service) successEpilogue(factory *units.Unit, node *orders.Node, product *units.Unit, cell world.Cell, buildPiece int, tick uint32) error {
	// A mobile product enters the shared carried representation before any
	// factory/product publication. Failure is therefore an explicit rejected
	// allocation, never a live partially accepted factory state
	// [04 R-FAC-02 §1].
	// TODO(question): trace the full factory attachment/queue lifecycle for
	// authored BMCode values above 1. Keep the established nonzero class
	// branch; do not manufacture a mover for these values [08 R-AI-03 §7.4].
	if product.Def != nil && product.Def.BMCode != 0 {
		if !movement.AttachFactoryProduct(s.World, factory.Handle, product.Handle, buildPiece) {
			return fmt.Errorf("construction: factory product attachment gates rejected allocation")
		}
		product.Move.Mode = 1 // grounded for ground and aircraft products [04 R-FAC-02 §1]
	}
	// Store position on order node already done via cell; also store world triple for presentation?
	node.GoalX = world.CellToWorld(cell.X)
	node.GoalZ = world.CellToWorld(cell.Z)
	// Link product handle into node payload Target for later states [05 C18].
	// This write is also the record's TARGET REFERENCE registration: the factory
	// record binds its product at the `Starting construction` visit, which is
	// what makes the unit-removal walk deliver the target-removed notice — mask
	// 8, construction stopped — to this factory when the product under
	// construction is destroyed [04 R-ORD-01 §6]
	// [05 "Build request and factory queue behavior"].
	productHandle := product.Handle
	node.Target = productHandle

	// Message "Starting construction" verbatim [05 C18]. `BuildingBuild` phase 2
	// emits it as status kind 9 (`build`) on the builder once the nanoframe was
	// created [04 R-ORD-01 §5][05 "the build-order caption census"].
	s.logMessage("Starting construction")
	s.raiseStatus(factory, statusBuild, "Starting construction")

	// Register builder link on product [05 C18].
	// The local builder link is retained for the product's GetBuilt lookup;
	// retail cleanup beyond that bounded handoff remains unresolved [R-FAC-01C].
	s.SetBuilderLink(productHandle, factory.Handle)
	if s.getBuiltLinks == nil {
		s.getBuiltLinks = make(map[pool.Handle]pool.Handle)
	}
	s.getBuiltLinks[productHandle] = factory.Handle

	// Initial standing-field merge has the same recovered class/auto guard as
	// GetBuilt. Do not copy order bits to a product whose flags do not prove the
	// standing-order capability [R-P0-09].
	s.copyStandingFlags(factory, product)

	// A mobile factory product is attached in the allocation visit. The shared
	// cargo representation is the only carried-state authority; structure-class
	// products remain standing at the allocated position [04 R-FAC-02 §1].
	if product.Def != nil && product.Def.BMCode != 0 {
		beCarriedID := orders.Lookup("BeCarried")
		if beCarriedID != 0 {
			pq := orders.BindQueueBinding(product, s.OrderBinding)
			s.registerGetBuilt(pq)
			s.RegisterOrderHandlers(pq)
			// `BeCarried` is not a producer insertion: it is a side effect of
			// the attach commit, which flushes the cargo's primary queue of
			// records lacking static bit 2 and HEAD-INSERTS the record
			// [04 R-FAC-02 §1] step 5. A freshly allocated product's queue is
			// empty, so the flush is a no-op and the head insert is the whole
			// step. It arms no caption and writes no active marker.
			pq.PushHead(beCarriedID, productRecord(product, tick, orders.Node{Target: factory.Handle}))
		}
	}

	// The attach commit inserted `BeCarried` at the head; `GetBuilt` follows as
	// a QUEUED producer insertion [04 R-FAC-02 §1][04 R-FAC-02 §4]. Because
	// `GetBuilt`'s static mask carries bit 5, that insertion takes the
	// head-insert branch [04 R-ORD-01 §13], so the product's primary queue at
	// the end of this visit is, front to back, `[GetBuilt, BeCarried]`. See the
	// 2026-09-04 correction under [04 R-FAC-02 §1]: the two sections that gave
	// the opposite order derived it from the after-marker path, before the
	// bit-5 branch was traced.
	getBuiltID := orders.Lookup("GetBuilt")
	if getBuiltID != 0 {
		pq := orders.BindQueueBinding(product, s.OrderBinding)
		s.registerGetBuilt(pq)
		s.RegisterOrderHandlers(pq)
		// Queued, count zero [05 C18].
		pq.Push(getBuiltID, productRecord(product, tick, orders.Node{Param2: 0, QueuedIssue: true}))
	}

	// The factory uses ONLY the edge form: it raises the building-bit edge here
	// in state 2, lowers it in state 4 and in cancel-current (together with the
	// activation bit), and never calls the order-record emission helper — so its
	// production record never carries the StopBuilding-pending flag, the
	// removal-time `StopBuilding` emission of [04 R-ORDER-02 §2] never fires for
	// it, and the factory's `StopBuilding` is the falling edge alone
	// [04 §3.8 correction 2026-09-02].
	s.startBuilding(factory)

	// Refresh builder interface [05 C18].
	if s != nil && s.Economy != nil {
		// Placeholder: interface refresh is presentation; no op but keep hook.
		if s.OnRefresh != nil {
			s.OnRefresh(factory)
		}
	}
	// Advance to state 3 [05 C18].
	node.Phase = uint8(State3)
	return nil
}

// startBuilding/stopBuilding are edge helpers. The bridge owns callback mode
// and argument shape; construction only changes the cached edge bit [04 §5.3].
// The factory's production start is the building-bit edge machine and nothing
// else: a rising edge starts the argument-less deferred StartBuilding, a
// falling edge StopBuilding. It never enters the order-record emission helper
// of the nine mobile work handlers, never writes the StopBuilding-pending
// flag, and its production record has no such flag to carry — so the
// removal-time StopBuilding emission of [R-ORDER-02 §2] never fires for a
// factory record and the factory's StopBuilding is the falling edge alone
// [04 §3.8 correction 2026-09-02][04 R-CB-01 §3 scope note]. The collapse of
// "slot form" and "emission helper" into one function in [04 R-CB-01 §3]
// correction 2 concerns only the argument-carrying start; it does not reach
// this producer. (Settled 2026-09-02, RWU-19-40: this comment used to ask
// whether a construction-command start should route through an order record
// and set the pending flag. It should not.)
//
// orders.EmitStartBuilding, the order-record emitter, stays the only writer of
// the StopBuilding-pending flag [R-ORDER-02 §2].
func (s *Service) startBuilding(u *units.Unit) {
	if u != nil {
		u.SetBuildingEdge(true)
	}
}

func (s *Service) stopBuilding(u *units.Unit) {
	if u != nil {
		u.SetBuildingEdge(false)
	}
}

// activate and deactivate are construction's two producers of the activation
// edge. They hold no state of their own: retail keeps one engine-state byte
// written through one edge machine, and the change test in that machine is the
// only suppression of an unchanged value [04 R-UNIT-06 §2]. The former local
// FlagActivated/FlagDeactivate mirror of bit 0 was a second copy that drifted
// from units.Unit.Activated — the bit the economy branch gate actually reads
// [05 R-PROD-01 §2] — whenever an order, a script or the AI toggled the unit.
func (s *Service) activate(u *units.Unit) {
	u.SetActivationEdge(true)
}

func (s *Service) deactivate(u *units.Unit) {
	u.SetActivationEdge(false)
}
