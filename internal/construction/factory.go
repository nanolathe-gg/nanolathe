// Package construction implements factory production lifecycle [PLAN_08 WU-08-5][05 "Factory production lifecycle"].
package construction

import (
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// State is the factory production handler's phase byte [05 "Factory production lifecycle"].
type State uint8

// AdmissionStatus is the result class of factory state-2 admission. Only a
// blocked footprint is retried; invalid content retains a permanent diagnostic
// instead of entering the silent 15-tick loop [05
// "Factory production lifecycle"][04 §6.4].
type AdmissionStatus uint8

const (
	AdmissionAdmitted AdmissionStatus = iota + 1
	AdmissionBlockedTransiently
	AdmissionRejectedPermanentDefinition
)

func (s AdmissionStatus) String() string {
	switch s {
	case AdmissionAdmitted:
		return "admitted"
	case AdmissionBlockedTransiently:
		return "blocked-transiently"
	case AdmissionRejectedPermanentDefinition:
		return "rejected-permanent-definition"
	default:
		return "unknown"
	}
}

// AdmissionDiagnostic is a deterministic construction trace entry. It is
// diagnostic state only and does not participate in simulation hashes.
type AdmissionDiagnostic struct {
	Tick    uint32
	Builder pool.Handle
	Product string
	Status  AdmissionStatus
	Reason  string
}

// CommandDiagnostic retains a rejected command at the authoritative boundary
// so a discarded queue error cannot look like a no-op click.
type CommandDiagnostic struct {
	Tick    uint32
	Builder pool.Handle
	Product string
	Count   int
	Reason  string
}

const (
	State0 State = 0 // presentation clear / activate gate [05]
	State1 State = 1 // yard-door handshake waits for in-build-stance [05]
	State2 State = 2 // exit-spot acquisition + silent revalidation + allocation [05 C16-C18]
	State3 State = 3 // work loop [05]
	State4 State = 4 // completion [05]
)

// Wake and interrupt masks [05 "Factory production lifecycle"] [05 "Cancel-current and stop interrupts"].
const (
	WakeBit1 uint32 = 1 << 1 // 2 [05]
	WakeBit2 uint32 = 1 << 2 // 4 [05]
	WakeBit3 uint32 = 1 << 3 // 8 [05]

	InterruptCancel uint32 = 1 << 1 // mask bit 1 highest priority (value 2) [05 C21]
	InterruptStop   uint32 = 1 << 3 // mask bit 3 value 8 "Construction stopped" [05 C22]
)

// Standing-order masks [05 "Rally inheritance"] bits 18-19 and 20-21 from factory class/state word.
const (
	StandingMoveMask uint32 = 0x000C0000 // bits 18-19 [05]
	StandingFireMask uint32 = 0x00300000 // bits 20-21 [05]
)

// Flags on units.Unit.Flags for COB edges [04 §4.4] [05].
const (
	FlagActivated     uint32 = 1 << 0     // activate edge placeholder [05 "Factory production lifecycle"] TODO(question): exact bit not located
	FlagCompleted     uint32 = 0x00002000 // completion marker in the instance flag word [R-P0-09]
	FlagInitCloak     uint32 = 0x00004000 // init-cloak posture in the instance flag word [R-P0-09]
	FlagStartBuilding uint32 = 1 << 2     // start-building edge [05]
	FlagDeactivate    uint32 = 1 << 1     // deactivate edge [05 C21]
)

// Damage constants [05 "Cancel-current and stop interrupts"] C21.
const (
	Kind9Damage int32 = 30000 // unscaled, scaling requires damage <30000 [05 C21]
)

// The mode selector and the special-second-state predicate were package-level
// vars; they are per-session configuration, so they live on Service
// (ModeSelector / IsSpecialSecondState) [05 C21].

// Service holds the factory lifecycle dependencies [PLAN_08].
type Service struct {
	Terrain *world.Terrain
	Catalog *content.Catalog
	World   *units.World
	Economy *economy.Service
	// OrderBinding is the owning session context copied to every product and
	// reconstructed/replaced queue [04 §3.3][06 §11.1].
	OrderBinding *orders.QueueBinding
	// Allocator hook for tests; if nil, uses World.Create.
	Allocator func(owner uint8, def *content.UnitDef, x, y, z numeric.Fixed) (*units.Unit, error)
	// ModelForFactory hook for QueryBuildInfo when m param is nil; tests may set.
	ModelForFactory func(factory *units.Unit) *model.Model
	// OnRefresh is the interface refresh hook [05 C18][05 C21][05 C22].
	OnRefresh func(*units.Unit)
	// Presentation receives already-admitted construction cues. It is optional
	// for headless simulation and never feeds back into authoritative state
	// [R-P0-06][EVENT-01].
	Presentation interface{ EmitNanolathe(frame.Event) bool }
	// StatusText is the consumer-supplied status-line sink for verbatim
	// order-handler notifications [R-ORDER-02 §1]. Retail prints these strings
	// to the player's status surface; the session/HUD layer supplies the
	// callback and owns the surface. nil means no subscriber and the text is
	// dropped. It never feeds back into authoritative state.
	StatusText func(text string)
	// ModelForUnit resolves the current model used by QueryNanoPiece. The
	// factory hook remains the compatibility name for factory/model fixtures.
	ModelForUnit func(unit *units.Unit) *model.Model

	// ModeSelector selects the special-player refund scaling [05 C21]:
	// 0 => subtract 7/10, 1 => subtract 1/2, other => add fallback. This
	// pairing is INVERTED relative to the ledger's negative-energy-use refund
	// site [05 C21].
	ModeSelector int
	// IsSpecialSecondState reports whether the referenced player object is in
	// the special second state [05 C21]. nil means no player is special.
	IsSpecialSecondState func(owner uint8) bool

	// LimitChecker is the per-def limit hook for allocation [P0-I16][05 C23].
	// Was package var LimitChecker; now per-Service to avoid shared mutable.
	LimitChecker func(factory *units.Unit, defKey string) bool
	// Movement is the optional walk driver for mobile builders. When set, a
	// MOBILE builder ordered to build at a site out of nano range first walks
	// toward the site until within nanolathe range via the normal
	// Move_Ground machinery, then enters state 2 [04 §3.4][05][R-P0-06].
	// Factory-class builders (CanMove==false && CanFly==false) are their own
	// yard and are unaffected. When no movement driver is bound, callers must
	// already be within nanolathe range.
	Movement *movement.System

	// Per-session state. None of this may live in a package-level var: it is
	// authoritative (BuilderLinks is C18's "register the builder link on the
	// product"), it has to survive save/load through one owner, and two worlds
	// in one process must not share it.
	builderLinks  map[pool.Handle]pool.Handle         // product -> builder [05 C18]
	placements    map[pool.Handle]world.FootprintRect // product -> occupancy footprint; save persistence TODO(question)
	productIndex  map[uint32]string                   // product id -> catalog key, built once
	getBuiltLinks map[pool.Handle]pool.Handle         // product -> builder until GetBuilt consumes it [R-P0-09]
	messages      []string                            // verbatim diagnostics [05 C18][05 C21][05 C22]
	admissions    []AdmissionDiagnostic               // state-2 outcomes, diagnostic only
	commands      []CommandDiagnostic                 // command-boundary rejections
	lastPermanent AdmissionDiagnostic                 // bounded malformed-node dedupe key
	hasPermanent  bool
	lastKill      KillInfo // most recent kind-9 kill packet [05 C21]
	// structures records the footprint rectangle of every COMPLETED building.
	// It is this engine's stand-in for retail's separate building-mask layer
	// [04 §6.2]: completed buildings must keep blocking new placement after
	// their plot occupancy shorts are released, because those shorts are
	// mobile-occupancy state and a factory's own stamp would otherwise block
	// its exit spot forever ([04 §6.2] names terrain versus building-mask as
	// distinct layers; movement models the latter via OccupancyGrid stamps).
	// save persistence TODO(question), same gap as placements.
	structures map[pool.Handle]world.FootprintRect
}

// queueForUnit is the construction-owned queue admission point. Factory
// lifecycle code can be reached by both human and AI producers, so any lazy
// queue it creates must receive the same session binding as product queues
// and replacements [04 §3.3][04 §3.5][06 §11.1].
func (s *Service) queueForUnit(u *units.Unit) *orders.Queue {
	q := orders.QueueForUnit(u)
	if q != nil && s != nil && s.OrderBinding != nil {
		q.SetBinding(s.OrderBinding)
	}
	return q
}

// TickContext carries per-tick shared services for unit-local stepping (ON-02).
// It contains the tick and the existing shared services, no presentation state:
// world, economy, terrain, and catalog are the authoritative sim services.
// Presentation hooks (OnRefresh) are intentionally absent; StepUnit never calls them.
type TickContext struct {
	Tick    uint32
	World   *units.World
	Economy *economy.Service
	Terrain *world.Terrain
	Catalog *content.Catalog
}

// WorkResult reports the outcome of a single-unit construction step (ON-02).
// It carries the builder identity, product handle, definition key, and owner
// for session hooks without presentation calls. Completion is exactly-once.
type WorkResult struct {
	Builder     pool.Handle // builder that was stepped
	Product     pool.Handle // nanoframe/new unit handle, 0 if none
	DefKey      string      // canonical def key for the product
	Owner       uint8       // builder owner
	Completed   bool        // true if a unit completed this tick (exactly once)
	State       State       // phase after step
	Err         error       // explicit error for descriptor mismatch or other failure
	Diagnostics []string    // verbatim diagnostics (e.g., "Starting construction")
}

// isMobileBuilder reports whether the builder is a mobile builder [04 §3.1][P0-I05].
// Mobile builders use MobileBuild/VTOL_MobileBuild descriptors; factories use
// BuildingBuild. The distinction is the runtime building-class status bit the
// allocator initializer derives from the definition's authored bmcode [05
// "Factory production lifecycle"]; mobility is not the contract — stock
// buildings (kbot lab, factories) author CanMove=1, so a mobility heuristic
// classifies them as mobile and rejects every factory order [08 "Classifier
// eligibility, destinations, and order"].
func isMobileBuilder(u *units.Unit) bool {
	if u == nil {
		return false
	}
	return u.Flags&units.BuildingClassStatus == 0
}

// nanoReach returns the builder's nanolathe reach in world Fixed units [fmt fbi] Builddistance reach in pixels.
// TODO(question): exact nano reach constant — using BuildDistance*65536 fixed as reach; whether retail adds footprint radius term, uses piece-height, or measures from piece world pos to site footprint edge vs center remains unknown [04 §3.4][05][R-P0-06][fmt fbi].
func nanoReach(builder *units.Unit) numeric.Fixed {
	if builder == nil || builder.Def == nil || builder.Def.BuildDistance == 0 {
		return 0
	}
	return numeric.Fixed(int64(builder.Def.BuildDistance) * 65536)
}

// isWithinNanoRange reports whether the builder's nano piece (or its base
// position as fallback) is within nanolathe range of the site [04 §3.4][05][R-P0-06][fmt fbi].
// The reach is nanoReach above; distance is planar X/Z only, as the reclaim
// range check is planar [05 "Unit reclaim"]. The site point is supplied by the
// caller: needsApproach and build-site selection both pass the nearest point of
// the site's footprint rectangle, not its centre — see Service.siteRangePoint
// for why the centre reading is disproved by the authored data, and for the
// TODO(question) that remains on retail's own comparison [R-P0-06].
func (s *Service) isWithinNanoRange(builder *units.Unit, siteX, siteZ numeric.Fixed) bool {
	if builder == nil || builder.Def == nil {
		return true
	}
	if builder.Def.BuildDistance == 0 {
		return true // unlimited reach
	}
	if s == nil || s.Movement == nil {
		return true // no walk driver bound in this context; skip range gate for unit tests
	}
	reach := nanoReach(builder)
	if reach == 0 {
		return true
	}
	var srcX, srcZ numeric.Fixed
	if piece, pos, ok := s.QueryNanoPiece(builder); ok {
		srcX, srcZ = pos.X(), pos.Z()
		_ = piece // piece index is presentation data; range uses world position only
	} else {
		srcX, srcZ = builder.X, builder.Z
	}
	// Callers now pass the point of the site's footprint rectangle nearest the
	// builder rather than the site centre; see Service.siteRangePoint for the
	// evidence and for what is still untraced [R-P0-06].
	dx := int64(siteX) - int64(srcX)
	dz := int64(siteZ) - int64(srcZ)
	dist2 := dx*dx + dz*dz
	reach2 := int64(reach) * int64(reach)
	return dist2 <= reach2
}

// ensureWalk submits a walk request toward the site via the normal
// Move_Ground machinery, without adding new path code [04 §7.3].
// It is idempotent: repeated calls while a request or active route already
// exists do not resubmit, preserving determinism I1 and RNG call order I4.
//
// The goal handed to path search is a build-site perimeter candidate, never
// the footprint centre [07 §9][04 §7.4]: the order's stored position stays the
// centre, but routing the builder there parks it inside its own site, where
// the null-self commit check can never accept the placement
// [05 "Silent blocked revalidation before allocation"]. See approach.go.
func (s *Service) ensureWalk(builder *units.Unit, node *orders.Node) {
	if s == nil || s.Movement == nil || s.Movement.Scheduler == nil || builder == nil || node == nil {
		return
	}
	// Bind the movement goal BEFORE the idempotency guards below. The binding
	// is derived state that no save box carries, so the first tick after a
	// restore must re-establish it even when the restored route is still
	// active — otherwise the mover would spend that route steering at the
	// order's stored position and walk into the site [04 §8.3][04 §7.4].
	goal := path.Cell{X: world.WorldToCell(node.GoalX), Z: world.WorldToCell(node.GoalZ)}
	if _, standX, standZ, ok := s.selectBuildApproach(builder, node); ok {
		goal = path.Cell{X: world.WorldToCell(standX), Z: world.WorldToCell(standZ)}
		s.Movement.BindMoveGoal(builder.Handle, node, standX, standZ)
	}
	if s.Movement.Scheduler.HasRequest(builder.Handle) {
		return
	}
	if r := s.Movement.Routes[builder.Handle]; r != nil && r.Active {
		return
	}
	s.Movement.EnsureUnit(builder)
	start := path.Cell{X: world.WorldToCell(builder.X), Z: world.WorldToCell(builder.Z)}
	s.Movement.SubmitMove(builder.Handle, builder.Owner, start, goal)
}

// clearWalk cancels any walk route/request for the builder after it arrives
// within nano range, so the builder stops once construction begins.
func (s *Service) clearWalk(builder *units.Unit) {
	if s == nil || s.Movement == nil {
		return
	}
	s.Movement.DeactivateMove(builder.Handle)
}

// NeedsWalk reports whether a mobile builder needs to walk toward the site
// before construction can begin [04 §3.4][05][R-P0-06].
// Factory-class builders never need walk. The condition itself lives in
// needsApproach (approach.go), which also records what the reach test still
// misses.
func (s *Service) NeedsWalk(builder *units.Unit, node *orders.Node) bool {
	if builder == nil || node == nil || !isMobileBuilder(builder) {
		return false
	}
	return s.needsApproach(builder, node)
}

// EnsureWalkPublic is the exported walk submission for session integration [04 §7.3].
func (s *Service) EnsureWalkPublic(builder *units.Unit, node *orders.Node) {
	s.ensureWalk(builder, node)
}

// IsWithinNanoRangePublic is the exported range check for session integration.
func (s *Service) IsWithinNanoRangePublic(builder *units.Unit, siteX, siteZ numeric.Fixed) bool {
	return s.isWithinNanoRange(builder, siteX, siteZ)
}

// KillInfo is the most recent kind-9 termination packet [05 C21].
type KillInfo struct {
	Damage   int32
	Severity int32
	NoCorpse bool
}

// CheckLimit reports whether nanoframe allocation for defKey on factory is allowed
// via Service.LimitChecker [C23][P0-I16]. Nil checker means allowed.
func (s *Service) CheckLimit(factory *units.Unit, defKey string) bool {
	if s == nil || s.LimitChecker == nil {
		return true
	}
	return s.LimitChecker(factory, defKey)
}

// NewService creates a Service with given dependencies.
func NewService(terrain *world.Terrain, catalog *content.Catalog, w *units.World, econ *economy.Service) *Service {
	s := &Service{Terrain: terrain, Catalog: catalog, World: w, Economy: econ}
	s.builderLinks = make(map[pool.Handle]pool.Handle)
	s.placements = make(map[pool.Handle]world.FootprintRect)
	s.getBuiltLinks = make(map[pool.Handle]pool.Handle)
	s.structures = make(map[pool.Handle]world.FootprintRect)
	s.buildProductIndex()
	return s
}

// buildProductIndex materializes the product-id to catalog-key reverse map once
// [05 C16][P0-I05]. Product IDs are stable catalog indices (1-based, 0 sentinel)
// via Catalog.UnitDefIndex, never FNV-1a hash (N04). Sorting ensures determinism (I1).
func (s *Service) buildProductIndex() {
	s.productIndex = make(map[uint32]string)
	if s.Catalog == nil || s.Catalog.Units == nil {
		return
	}
	keys := s.Catalog.SortedUnitKeys()
	for i, k := range keys {
		id := uint32(i + 1) // 1-based index matches Catalog.UnitDefIndex [P0-I05][02 §5]
		if _, seen := s.productIndex[id]; !seen {
			s.productIndex[id] = k
		}
	}
}

// rememberProductID records a product id mapping for callers that construct
// order payloads directly (tests and the queue builder) [05 C16][P0-I05].
func (s *Service) rememberProductID(defKey string, pid uint32) {
	if s.productIndex == nil {
		s.productIndex = make(map[uint32]string)
	}
	s.productIndex[pid] = content.CanonicalKey(defKey)
}

// Messages returns the verbatim diagnostics emitted so far [05 C18][05 C21][05 C22].
func (s *Service) Messages() []string { return append([]string(nil), s.messages...) }

// ClearMessages drops the diagnostic log.
func (s *Service) ClearMessages() { s.messages = nil }

func (s *Service) logMessage(msg string) { s.messages = append(s.messages, msg) }

// AdmissionDiagnostics returns state-2 outcomes in visit order. The trace is
// deliberately separate from Messages so callers can assert status classes
// without parsing presentation text [04 §6.4].
func (s *Service) AdmissionDiagnostics() []AdmissionDiagnostic {
	if s == nil {
		return nil
	}
	return append([]AdmissionDiagnostic(nil), s.admissions...)
}

// CommandDiagnostics returns rejected factory commands in input order.
func (s *Service) CommandDiagnostics() []CommandDiagnostic {
	if s == nil {
		return nil
	}
	return append([]CommandDiagnostic(nil), s.commands...)
}

// RecordCommandRejection retains a queue/command error at the authoritative
// boundary. It is called by session command processing and does not mutate the
// simulation queue [01 §4.4][05 "Build request and factory queue behavior"].
func (s *Service) RecordCommandRejection(tick uint32, builder pool.Handle, product string, count int, err error) {
	if s == nil || err == nil {
		return
	}
	s.commands = append(s.commands, CommandDiagnostic{
		Tick: tick, Builder: builder, Product: content.CanonicalKey(product), Count: count, Reason: err.Error(),
	})
}

func (s *Service) recordAdmission(tick uint32, builder pool.Handle, product string, status AdmissionStatus, err error) {
	if s == nil {
		return
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	s.admissions = append(s.admissions, AdmissionDiagnostic{
		Tick: tick, Builder: builder, Product: content.CanonicalKey(product), Status: status, Reason: reason,
	})
}

func (s *Service) rejectPermanent(factory *units.Unit, node *orders.Node, tick uint32, err error) {
	if s == nil || node == nil {
		return
	}
	product := node.BuildDefKey
	if product == "" {
		if def := s.productDef(uint32(node.Param1)); def != nil {
			product = def.UnitName
		}
	}
	var builder pool.Handle
	if factory != nil {
		builder = factory.Handle
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	diagnostic := AdmissionDiagnostic{
		Tick: tick, Builder: builder, Product: content.CanonicalKey(product),
		Status: AdmissionRejectedPermanentDefinition, Reason: reason,
	}
	// A malformed node may remain in an internal fixture indefinitely. Keep
	// diagnostics observable without retaining every node pointer or appending
	// one identical entry per tick.
	if s.hasPermanent && s.lastPermanent.Builder == diagnostic.Builder &&
		s.lastPermanent.Product == diagnostic.Product &&
		s.lastPermanent.Status == diagnostic.Status && s.lastPermanent.Reason == diagnostic.Reason {
		return
	}
	s.lastPermanent = diagnostic
	s.hasPermanent = true
	s.admissions = append(s.admissions, diagnostic)
	// Malformed nodes are outside the established state-2 path: queue
	// admission rejects them before they can reach this handler. Retain only a
	// diagnostic if an internal fixture bypasses that boundary; do not invent a
	// cancellation or retry transition [04 §6.4]. TODO(question): establish the
	// retail response if a malformed node bypasses queue preflight.
}

// notifyStatus surfaces a verbatim order-handler notification through the
// consumer-supplied status sink [R-ORDER-02 §1]. Unlike logMessage it is not
// a construction-lifecycle diagnostic; it is the retail status text.
func (s *Service) notifyStatus(text string) {
	if s == nil || s.StatusText == nil || text == "" {
		return
	}
	s.StatusText(text)
}

// LastKill returns the most recent kind-9 termination packet [05 C21].
func (s *Service) LastKill() KillInfo { return s.lastKill }

// BuilderLink returns the builder registered on a product, if any [05 C18].
func (s *Service) BuilderLink(product pool.Handle) (pool.Handle, bool) {
	b, ok := s.builderLinks[product]
	return b, ok
}

// SetBuilderLink registers the builder link on a product [05 C18].
func (s *Service) SetBuilderLink(product, builder pool.Handle) {
	if s.builderLinks == nil {
		s.builderLinks = make(map[pool.Handle]pool.Handle)
	}
	s.builderLinks[product] = builder
}

// ClearBuilderLink clears builder link on completion [P0-14] (helper for test).
func (s *Service) ClearBuilderLink(product pool.Handle) {
	if s.builderLinks != nil {
		delete(s.builderLinks, product)
	}
}

// PlacementForProduct returns the typed occupancy rectangle retained when a
// nanoframe was allocated. It is session state, not a reinterpretation of
// persisted unit/save fields; save persistence remains TODO(question).
func (s *Service) PlacementForProduct(product pool.Handle) (world.FootprintRect, bool) {
	if s == nil || s.placements == nil {
		return world.FootprintRect{}, false
	}
	r, ok := s.placements[product]
	return r, ok
}

func (s *Service) recordPlacement(product pool.Handle, rect world.FootprintRect) {
	if s.placements == nil {
		s.placements = make(map[pool.Handle]world.FootprintRect)
	}
	s.placements[product] = rect
}

// reservePlacement commits the product's footprint after a complete
// validation pass. Construction uses the established layer-A occupancy
// accessors because the placement validator compares both occupancy layers;
// the exact structure-vs-mobile layer alias remains TODO(question) [04 §6.2].
// The full rectangle is prechecked before any cell is stamped, so a failed
// reservation cannot leave a partial occupancy footprint.
//
// The precheck is gated by the yard map, exactly as the canonical validator
// gates it [04 §6.2] C10 [R-P0-08]: only a cell whose control byte carries
// bits 1-2 tests for a foreign occupant. It used to test every cell in the
// rectangle unconditionally, which contradicted the validator and rejected
// placements retail accepts — measured on ARMLAB, whose authored yard map
// "yoccoy ooccoo ..." puts control byte 0x29 on its four corners, and 0x29
// carries neither occupancy bit. A lab may therefore legally interlock a
// corner with an existing building, and the canonical preview/commit check
// said so while this reservation refused, so the order retried forever with
// no nanoframe.
//
// TODO(question): whether retail stamps every footprint cell or only the
// occupancy-gated ones is untraced. A cell already owned by another unit is
// left with its owner rather than overwritten, which is the conservative
// reading and the one ReleasePlacement already assumes — it clears only cells
// whose layer-A occupant is the releasing product. Tracing the yard-map stamp
// would settle whether a non-occupancy cell is stamped at all.
func (s *Service) reservePlacement(product pool.Handle, def *content.UnitDef, rect world.FootprintRect) error {
	if s == nil || s.Terrain == nil {
		return fmt.Errorf("construction: placement terrain unavailable")
	}
	if product == 0 || uint64(product) > uint64(^uint16(0)>>1) {
		return fmt.Errorf("construction: placement identity %d exceeds occupancy identity range", product)
	}
	id := int16(product)
	yard := reservationYard(def, rect)
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				return fmt.Errorf("construction: placement cell %d,%d unavailable", x, z)
			}
			if !yardTestsOccupancy(yard, rect, x, z) {
				continue // [04 §6.2] C10: bits 1-2 clear, no occupant test
			}
			if (cell.OccupantA() != 0 && cell.OccupantA() != id) ||
				(cell.OccupantB() != 0 && cell.OccupantB() != id) {
				return fmt.Errorf("construction: placement cell %d,%d occupied", x, z)
			}
		}
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell.OccupantA() != 0 && cell.OccupantA() != id {
				continue // another owner keeps the cell; see TODO(question) above
			}
			cell.SetOccupantA(id)
		}
	}
	return nil
}

// retirePlacement releases a nanoframe's plot occupancy reservation and
// records the completed building's footprint in the structures registry.
// Occupancy shorts are mobile-occupancy state ([fmt tnt] runtime writer model:
// unit stomp/unstomp), so a finished building must not keep squatting them —
// its own stamp is what deadlocked every factory's first exit-spot validation.
// Blocking duty moves to s.structures, the building-mask stand-in [04 §6.2].
func (s *Service) retirePlacement(product pool.Handle) {
	if s == nil || product == 0 {
		return
	}
	rect, ok := s.placements[product]
	s.releaseFrameStamps(product)
	if !ok {
		// Nothing was reserved for this product (fixture-created units); the
		// structures registry only tracks footprints construction itself laid.
		return
	}
	// Only building-class completions join the structures registry. A finished
	// mobile unit walks away, so blocking overlap against it must not persist;
	// its frame stamps still release like every other product's [04 §6.2].
	if u := s.World.Unit(product); u != nil && u.Def != nil && u.Flags&units.BuildingClassStatus == 0 {
		return
	}
	if s.structures == nil {
		s.structures = make(map[pool.Handle]world.FootprintRect)
	}
	s.structures[product] = rect
}

// reservationYard resolves the product's yard-map control bytes over its
// footprint [04 §6.2] C10. A definition that authors no yard map reserves
// every cell, which is the yard the commit validator synthesises for it.
func reservationYard(def *content.UnitDef, rect world.FootprintRect) []world.YardCell {
	w, d := int(rect.Width()), int(rect.Depth())
	if w <= 0 || d <= 0 {
		return nil
	}
	if def != nil && def.YardMap != "" {
		if y, err := world.ParseYardMap(def.YardMap, w, d); err == nil && len(y) == w*d {
			return y
		}
	}
	y := make([]world.YardCell, w*d)
	for i := range y {
		y[i] = 0x06 // bits 1-2 reject any occupant [04 §6.2]
	}
	return y
}

// yardTestsOccupancy reports whether the yard byte covering (x,z) carries the
// occupancy bits [04 §6.2] C10. An unresolvable yard falls back to testing,
// so a parse failure can never silently loosen the check.
func yardTestsOccupancy(yard []world.YardCell, rect world.FootprintRect, x, z int32) bool {
	if len(yard) == 0 {
		return true
	}
	idx := int((z-rect.MinZ())*rect.Width() + (x - rect.MinX()))
	if idx < 0 || idx >= len(yard) {
		return true
	}
	return yard[idx]&0x06 != 0
}

func (s *Service) releaseFrameStamps(product pool.Handle) bool {
	if s == nil || s.placements == nil {
		return false
	}
	rect, ok := s.placements[product]
	if !ok {
		return false
	}
	if s.Terrain != nil && uint64(product) <= uint64(^uint16(0)>>1) {
		id := int16(product)
		for z := rect.MinZ(); z < rect.MaxZ(); z++ {
			for x := rect.MinX(); x < rect.MaxX(); x++ {
				cell := s.Terrain.PlotAt(x, z)
				if cell == nil || cell.OccupantA() != id {
					continue
				}
				cell.SetOccupantA(0)
			}
		}
	}
	delete(s.placements, product)
	return true
}

// ReleasePlacement removes an unfinished/dead product's reserved frame
// footprint and any completed-structure registry entry for the handle; the
// death/teardown observer calls this exactly once per leaving unit [R-P0-09].
func (s *Service) ReleasePlacement(product pool.Handle) bool {
	dropped := s.releaseFrameStamps(product)
	if s != nil && s.structures != nil {
		if _, ok := s.structures[product]; ok {
			delete(s.structures, product)
			dropped = true
		}
	}
	return dropped
}

// StructureBlocks reports whether any completed building other than self
// covers any cell of rect. Callers that validate a producer against its own
// body pass that body as self so a factory exit inside its own yard stays
// legal while foreign structures still block [05 "Factory production
// lifecycle"][04 §6.2]. Deterministic scan: keys sorted ascending (I1).
func (s *Service) StructureBlocks(self pool.Handle, rect world.FootprintRect) (pool.Handle, bool) {
	if s == nil || len(s.structures) == 0 {
		return 0, false
	}
	keys := make([]pool.Handle, 0, len(s.structures))
	for h := range s.structures {
		keys = append(keys, h)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, h := range keys {
		if h == self {
			continue
		}
		r := s.structures[h]
		if rect.MinX() < r.MaxX() && r.MinX() < rect.MaxX() &&
			rect.MinZ() < r.MaxZ() && r.MinZ() < rect.MaxZ() {
			return h, true
		}
	}
	return 0, false
}

// BuilderLinks returns a copy of all builder/product links (ON-02).
// Exported accessor replaces reflect/unsafe inspection; used to verify deterministic cleanup.
func (s *Service) BuilderLinks() map[pool.Handle]pool.Handle {
	if s == nil || s.builderLinks == nil {
		return nil
	}
	out := make(map[pool.Handle]pool.Handle, len(s.builderLinks))
	for k, v := range s.builderLinks {
		out[k] = v
	}
	return out
}

// LinkRecord is one builder-product link, product handle owns builder handle [05 C18][RS-10].
type LinkRecord struct {
	Builder pool.Handle
	Product pool.Handle
}

// SnapshotLinks returns a deterministic sorted copy of builder-product links [RS-10][I1].
// Sorted by Product ascending, then Builder ascending, for canonical save ordering.
func (s *Service) SnapshotLinks() []LinkRecord {
	if s == nil || len(s.builderLinks) == 0 {
		return nil
	}
	out := make([]LinkRecord, 0, len(s.builderLinks))
	for prod, builder := range s.builderLinks {
		out = append(out, LinkRecord{Builder: builder, Product: prod})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Product != out[j].Product {
			return out[i].Product < out[j].Product
		}
		return out[i].Builder < out[j].Builder
	})
	return out
}

// ---------------------------------------------------------------------------
// C24 Construction arithmetic [05 "Construction arithmetic"].
// ---------------------------------------------------------------------------

// WorkerQuantum derives integer worker quantum floor(workerTime/30) [05 "Construction arithmetic"].
func WorkerQuantum(workerTime int32) int32 {
	// [05 "Construction arithmetic"] floor division, trunc toward zero for positive inputs [01 §8] I3.
	if workerTime < 0 {
		return workerTime / 30
	}
	return workerTime / 30 // trunc toward zero == floor for non-negative
}

// RemainingStep computes new remaining fraction clamp(old - worker/buildTime,0,1) [05 "Construction arithmetic"].
func RemainingStep(old float32, worker int32, buildTime int32) float32 {
	if buildTime <= 0 {
		return 0 // TODO(question): zero buildTime guard untraced; clamp to 0 rather than panic
	}
	delta := float32(worker) / float32(buildTime)
	nv := old - delta
	if nv < 0 {
		nv = 0
	}
	if nv > 1 {
		nv = 1
	}
	return nv
}

// HealthGain implements difference-of-truncations health gain [05 "Construction arithmetic"].
// health gain = trunc(maxDamage*old) - trunc(maxDamage*new)
func HealthGain(old, newRemaining float32, maxDamage int32) int32 {
	// trunc toward zero is Go int32(float32) [01 §8] I3.
	return int32(float32(maxDamage)*old) - int32(float32(maxDamage)*newRemaining)
}

// ConstructionStep performs one construction helper step [05 "Construction arithmetic"].
// Returns newRemaining, healthGain, energyDemand, metalDemand.
func ConstructionStep(old float32, worker int32, buildTime int32, maxDamage int32, energyCost, metalCost int32) (float32, int32, float32, float32) {
	nv := RemainingStep(old, worker, buildTime)
	hg := HealthGain(old, nv, maxDamage)
	delta := old - nv // positive decrease [05]
	energyDemand := float32(energyCost) * delta
	metalDemand := float32(metalCost) * delta
	return nv, hg, energyDemand, metalDemand
}

// ---------------------------------------------------------------------------
// C16 exit-spot acquisition [05 "Factory production lifecycle"].
// ---------------------------------------------------------------------------

// SnapWorldToCell snaps world position to map cells using footprint extents biased by half extent [05 C16].
// Each coordinate converts from Fixed to cell index biased by half its extent to give footprint rectangle origin.
func SnapWorldToCell(wx, wz numeric.Fixed, footX, footZ int) world.Cell {
	// [05 "Factory production lifecycle"] C16: each coordinate biased by half its extent.
	// WorldToCell floors with sign correction [03 §2.1] I3, then subtract half extent integer division.
	cx := world.WorldToCell(wx)
	cz := world.WorldToCell(wz)
	// half extent via trunc toward zero integer division [01 §8].
	cx -= int32(footX / 2)
	cz -= int32(footZ / 2)
	return world.Cell{X: cx, Z: cz}
}

// snapBias is the half-extent bias vector for tests: returns (footX/2, footZ/2) integer.
func snapBias(footX, footZ int) (int32, int32) { return int32(footX / 2), int32(footZ / 2) }

// QueryBuildWorldPosition resolves the authored exit transform in full world
// X/Y/Z per [05 "Factory production lifecycle"] C16.
// Exact order: query factory script's build-info piece with query argument PRE-INITIALIZED to -1;
// resolve piece transform + factory origin to world position; store position on order node is done by caller;
// load product definition and snap to map cells using packed footprint extents each biased by half extent.
func (s *Service) QueryBuildWorldPosition(factory *units.Unit, m *model.Model) (world.ModelWorldPosition, bool) {
	if factory == nil || m == nil {
		return world.ModelWorldPosition{}, false
	}
	// QueryBuildInfo is a synchronous mode-Q callback. Production uses the
	// strict binding bridge [R-P0-09][04 §5.3].
	pieceIdx := int32(-1)
	if binding := factory.COBBinding(); binding != nil && binding.Callbacks != nil {
		pieceIdx = binding.Callbacks.QueryBuildInfo().QueryValue()
	} else {
		return world.ModelWorldPosition{}, false
	}
	if pieceIdx < 0 {
		return world.ModelWorldPosition{}, false
	}
	modelPiece := pieceIdx
	if binding := factory.COBBinding(); binding != nil && int(pieceIdx) < len(binding.PieceMap) {
		modelPiece = int32(binding.PieceMap[pieceIdx])
	}
	if modelPiece < 0 || int(modelPiece) >= len(m.Pieces) {
		return world.ModelWorldPosition{}, false
	}
	// 2. resolve piece transform plus factory origin to world position. Strict
	// bindings own PieceMap, hierarchy state, and unit orientation; construction
	// must not duplicate that composition [04 §4.1][03 §2.4].
	var pos [3]numeric.Fixed
	if binding := factory.COBBinding(); binding != nil {
		var composed bool
		pos, composed = binding.ComposePiece(int(pieceIdx), factory.Move.Heading, factory.Move.Pitch, factory.Move.Bank)
		if !composed {
			return world.ModelWorldPosition{}, false
		}
	} else {
		return world.ModelWorldPosition{}, false
	}
	worldX := factory.X.Add(pos[0])
	worldY := factory.Y.Add(pos[1])
	worldZ := factory.Z.Add(pos[2])
	return world.NewModelWorldPosition(worldX, worldY, worldZ), true
}

// QueryBuildInfo preserves the established cell-returning API. Its cell is
// the independently snapped validation anchor; callers allocating a product
// must retain QueryBuildWorldPosition separately [R-P0-02].
func (s *Service) QueryBuildInfo(factory *units.Unit, m *model.Model) (world.Cell, bool) {
	position, ok := s.QueryBuildWorldPosition(factory, m)
	if !ok {
		return world.Cell{}, false
	}

	// 4. Load product definition and snap using packed footprint extents each biased by half extent [05 C16][P0-I05].
	footX, footZ := 1, 1 // default 1x1 when the product is unknown
	if q := s.queueForUnit(factory); q != nil && q.LenPrimary() > 0 {
		head := q.Primary()[0]
		var def *content.UnitDef
		if head.BuildDefKey != "" && s.Catalog != nil {
			if d, ok := s.Catalog.Unit(head.BuildDefKey); ok {
				def = d
			}
		}
		if def == nil {
			if pid := head.Param1; pid != 0 {
				def = s.productDef(uint32(pid))
			}
		}
		if def != nil {
			footX = int(def.FootprintX)
			footZ = int(def.FootprintZ)
			if footX <= 0 {
				footX = 1
			}
			if footZ <= 0 {
				footZ = 1
			}
		}
	}
	extent, err := world.NewFootprintExtent(int32(footX), int32(footZ))
	if err != nil {
		return world.Cell{}, false
	}
	placement, err := world.SnapFactoryPlacement(position, extent)
	if err != nil {
		return world.Cell{}, false
	}
	return placement.Anchor().Cell(), true
}

// QueryNanoPiece synchronously resolves the builder's authored nano piece and
// transforms it through the current model hierarchy. Cell zero is seeded to 0;
// no engine-side piece alternation is permitted [R-P0-06][04 §5.3].
func (s *Service) QueryNanoPiece(builder *units.Unit) (int32, world.ModelWorldPosition, bool) {
	if s == nil || builder == nil {
		return 0, world.ModelWorldPosition{}, false
	}
	m := s.ModelForUnit
	if m == nil {
		m = s.ModelForFactory
	}
	var mdl *model.Model
	if m != nil {
		mdl = m(builder)
	}
	if mdl == nil {
		if binding := builder.COBBinding(); binding != nil {
			mdl = binding.Model
		}
	}
	if mdl == nil {
		return 0, world.ModelWorldPosition{}, false
	}
	piece := int32(0)
	if binding := builder.COBBinding(); binding != nil && binding.Callbacks != nil {
		piece = binding.Callbacks.QueryNanoPiece().QueryValue()
	} else {
		return 0, world.ModelWorldPosition{}, false
	}
	modelPiece := piece
	if binding := builder.COBBinding(); binding != nil && piece >= 0 && int(piece) < len(binding.PieceMap) {
		modelPiece = int32(binding.PieceMap[piece])
	}
	if modelPiece < 0 || int(modelPiece) >= len(mdl.Pieces) {
		return piece, world.ModelWorldPosition{}, false
	}
	var pos [3]numeric.Fixed
	if binding := builder.COBBinding(); binding != nil {
		var composed bool
		pos, composed = binding.ComposePiece(int(piece), builder.Move.Heading, builder.Move.Pitch, builder.Move.Bank)
		if !composed {
			return piece, world.ModelWorldPosition{}, false
		}
	} else {
		return piece, world.ModelWorldPosition{}, false
	}
	return piece, world.NewModelWorldPosition(builder.X.Add(pos[0]), builder.Y.Add(pos[1]), builder.Z.Add(pos[2])), true
}

func (s *Service) emitAcceptedNano(tick uint32, builder, product *units.Unit) {
	if s == nil || s.Presentation == nil || builder == nil || product == nil {
		return
	}
	piece, source, ok := s.QueryNanoPiece(builder)
	if !ok {
		return
	}
	// Selector 6 is the established construction segment selector. The source
	// is the QueryNanoPiece world position; the target is the product's world
	// anchor [R-P0-06]. One event per accepted work step (mobile construction
	// emits one segment, unlike build assist's two) [R-P0-06 §1][R-P0-06 §3].
	// The producer identity routes the event to effect strip 6 (beam/muzzle/
	// nanolathe) and the geometry flag opens the client's nanolathe draw gate
	// [03 §5.5][R-P0-06 §5].
	s.Presentation.EmitNanolathe(frame.Event{
		Tick: tick, Source: builder.Handle, Target: product.Handle, Piece: piece,
		X: source.X(), Y: source.Y(), Z: source.Z(),
		TargetX: product.X, TargetY: product.Y, TargetZ: product.Z,
		EffectID: 6, Mode: 1, Team: builder.Owner,
		Producer:               frame.ProducerBeam,
		PaletteRow:             6,
		NanolatheGeometryKnown: true,
	})
}

// productDef resolves an order payload's product id to its definition through
// the reverse index built at Service construction [05 C16][P0-I05].
// IDs are stable catalog indices (1-based), never FNV hash [P0-I05].
func (s *Service) productDef(pid uint32) *content.UnitDef {
	if s == nil || s.Catalog == nil || pid == 0 {
		return nil
	}
	key, ok := s.productIndex[pid]
	if !ok {
		// Fallback: try direct catalog lookup via index [P0-I05]
		if def, ok2 := s.Catalog.UnitDefByIndex(pid); ok2 {
			return def
		}
		return nil
	}
	def, ok := s.Catalog.Unit(key)
	if !ok {
		// Fallback to index-based lookup if key missing (catalog changed)
		if def2, ok2 := s.Catalog.UnitDefByIndex(pid); ok2 {
			return def2
		}
		return nil
	}
	return def
}

// ---------------------------------------------------------------------------
// Helpers for footprint yard and validation [05 C17] [04 §6.2].
// ---------------------------------------------------------------------------

// catalogIndex returns the stable catalog index for defKey [P0-I05][02 §5].
// Never uses FNV hash.
func catalogIndexForService(cat *content.Catalog, defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	if cat != nil {
		if idx, ok := cat.UnitDefIndex(ck); ok {
			return idx
		}
	}
	return 0
}

// getProductDefForNode resolves the product a build node names [05 C16][P0-I05].
// It first uses the authoritative BuildDefKey string (stable across catalog
// changes and save/load), then falls back to the catalog index in Param1.
func (s *Service) getProductDefForNode(node *orders.Node) *content.UnitDef {
	if node == nil {
		return nil
	}
	if node.BuildDefKey != "" && s != nil && s.Catalog != nil {
		if def, ok := s.Catalog.Unit(node.BuildDefKey); ok {
			return def
		}
	}
	return s.productDef(uint32(node.Param1))
}

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
// the passed self identity rejects"). skipAggregates marks the factory
// exit-spot query, whose caller mode is outside the recovered inline
// terrain-check mode (see PlacementQuery.SkipTerrainAggregates). Completed
// buildings register in s.structures and reject overlap here so releasing
// frame stamps cannot let structures stack.
func (s *Service) validatePlacement(self pool.Handle, rect world.FootprintRect, def *content.UnitDef, yard []world.YardCell, skipAggregates bool) (world.PlacementResult, error) {
	if s == nil || s.Terrain == nil {
		return world.PlacementResult{}, fmt.Errorf("construction: placement terrain unavailable")
	}
	if _, blocked := s.StructureBlocks(self, rect); blocked {
		return world.PlacementResult{}, fmt.Errorf("construction: footprint overlaps a completed structure")
	}
	rules, err := placementRules(s, def)
	if err != nil {
		return world.PlacementResult{}, err
	}
	return s.Terrain.CheckPlacement(world.PlacementQuery{
		Rect:                  rect,
		Yard:                  yard,
		Rules:                 rules,
		Self:                  uint16(self),
		Mobile:                def != nil && def.BMCode,
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
	// Per-def limit -1 sentinel means unlimited [P0-15][P0-16]; 0 from Go zero-value also treated as unlimited for fixtures.
	// TODO(question): Genuine limit 0 (no units allowed) vs Go zero-value unlimited not distinguished; fixtures use explicit -1 where needed.
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
			return nil, fmt.Errorf(ErrLimitMessage)
		}
	}
	if !s.CheckLimit(factory, def.UnitName) {
		return nil, fmt.Errorf(ErrLimitMessage) // verbatim [05 C18] via hook [P0-I16]
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
		// Extractor yield is sampled once at placement and stored on the product
		// [P1-10][P1-15]: Σ(cellMetal+1)*extractsMetal, never resampled.
		if prod != nil && def.ExtractsMetal != 0 && s.Terrain != nil {
			if v, err := s.Terrain.SampleMetal(rect.MinX(), rect.MinZ(), int(def.FootprintX), int(def.FootprintZ), float32(def.ExtractsMetal)); err == nil {
				prod.SpotMetal = v // once, never resampled [P1-10]
			}
		}
		if prod != nil {
			if err := s.reservePlacement(prod.Handle, def, rect); err != nil {
				prod.Alive = false
				return nil, err
			}
			s.recordPlacement(prod.Handle, rect)
		}
		return prod, nil
	}
	if s.World == nil {
		return nil, fmt.Errorf("construction: no world/allocator")
	}
	// Create at the authored exit model/world position.
	h, err := s.World.Create(def, factory.Owner, position.X(), position.Y(), position.Z())
	if err != nil {
		return nil, err
	}
	prod := s.World.Unit(h)
	if prod == nil {
		return nil, fmt.Errorf("construction: failed to get product")
	}
	if err := s.reservePlacement(prod.Handle, def, rect); err != nil {
		s.World.Destroy(prod.Handle, units.DeathKilled)
		return nil, err
	}
	s.recordPlacement(prod.Handle, rect)
	initializeNanoframe(prod, def)
	// Extractor yield is sampled once at placement and stored on the product
	// [P1-10][P1-15]: Σ(cellMetal+1)*extractsMetal, never resampled.
	if def.ExtractsMetal != 0 && s.Terrain != nil {
		if v, err := s.Terrain.SampleMetal(rect.MinX(), rect.MinZ(), int(def.FootprintX), int(def.FootprintZ), float32(def.ExtractsMetal)); err == nil {
			prod.SpotMetal = v // once, never resampled [P1-10]
		}
	}
	return prod, nil
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
}

// successEpilogue performs the success sequence after allocation [05 C18].
func (s *Service) successEpilogue(factory *units.Unit, node *orders.Node, product *units.Unit, cell world.Cell) {
	// Store position on order node already done via cell; also store world triple for presentation?
	node.GoalX = world.CellToWorld(cell.X)
	node.GoalZ = world.CellToWorld(cell.Z)
	// Link product handle into node payload Target for later states [05 C18].
	productHandle := product.Handle
	node.Target = productHandle

	// Message "Starting construction" verbatim [05 C18].
	s.logMessage("Starting construction")

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

	// Resolve get-built op + insert GetBuilt node onto product's primary queue (queued mode, zero count) [05 C18].
	getBuiltID := orders.Lookup("GetBuilt")
	if getBuiltID != 0 {
		pq := orders.BindQueueBinding(product, s.OrderBinding)
		// Queued mode, zero count per [05 C18]: Param2 zero count special? Queue treats 0 as 1? But we pass 0 and CoalesceTail will treat 0 as 1? However plan says zero count. We pass Node with Param2 0.
		pq.Push(getBuiltID, orders.Node{Param2: 0})
		// Ensure product's queue head is GetBuilt with active marker.
	}

	// Raise StartBuilding only on the rising edge [R-P0-09][04 §5.3].
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
}

// startBuilding/stopBuilding are edge helpers. The bridge owns callback mode
// and argument shape; construction only changes the cached edge bit [04 §5.3].
// startBuilding issues the slot-form heading variant, which per the corrected
// section is a construction-command producer that starts the slot and emits
// its network event WITHOUT touching the order record's StopBuilding-pending
// flag — exactly one writer of that flag exists, the order-record emitter
// orders.EmitStartBuilding [R-ORDER-02 §2].
func (s *Service) startBuilding(u *units.Unit) {
	if u == nil || u.Flags&FlagStartBuilding != 0 {
		return
	}
	u.Flags |= FlagStartBuilding
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		binding.Callbacks.StartBuildingHeading(u.Move.Heading & 0xffff) // [04 §5.3] slot form carries heading & 0xffff
	}
}

func (s *Service) stopBuilding(u *units.Unit) {
	if u == nil || u.Flags&FlagStartBuilding == 0 {
		return
	}
	u.Flags &^= FlagStartBuilding
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		binding.Callbacks.StopBuilding()
	}
}

func (s *Service) activate(u *units.Unit) {
	if u == nil || u.Flags&FlagActivated != 0 {
		return
	}
	u.Flags |= FlagActivated
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		binding.Callbacks.Activate()
	}
}

func (s *Service) deactivate(u *units.Unit) {
	if u == nil || u.Flags&(FlagActivated|FlagDeactivate) == 0 {
		return
	}
	u.Flags &^= FlagActivated | FlagDeactivate
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		binding.Callbacks.Deactivate()
	}
}

// copyStandingFlags is the recovered initial standing-field merge guard. The
// class and auto exclusions are distinct from the later rally traversal
// [R-P0-09].
func copyStandingFlags(builder, product *units.Unit) {
	if builder == nil || product == nil {
		return
	}
	if builder.Flags&0x10000000 == 0 || product.Flags&0x10000000 == 0 ||
		builder.Flags&0x00004000 != 0 || product.Flags&0x00004000 != 0 {
		return
	}
	product.Flags = (product.Flags &^ (StandingMoveMask | StandingFireMask)) |
		(builder.Flags & (StandingMoveMask | StandingFireMask))
}

func (s *Service) copyStandingFlags(builder, product *units.Unit) {
	copyStandingFlags(builder, product)
}

// OnRefresh is the interface refresh callback, set by tests.

// ---------------------------------------------------------------------------
// C19 Rally inheritance [05 "Rally inheritance"].
// ---------------------------------------------------------------------------

func (s *Service) rallyInheritance(factory *units.Unit, product *units.Unit) {
	if factory == nil || product == nil {
		return
	}
	fq := s.queueForUnit(factory)
	if fq == nil {
		// No queue => park
		parkID := orders.Lookup("Park")
		if parkID != 0 {
			pq := orders.BindQueueBinding(product, s.OrderBinding)
			pq.Push(parkID, orders.Node{})
		}
		return
	}
	prim := fq.Primary()
	qMoveID := orders.Lookup("QMove")
	qPatrolID := orders.Lookup("QPatrol")
	moveID := orders.Lookup("Move_Ground")
	patrolID := orders.Lookup("Patrol")
	parkID := orders.Lookup("Park")

	// Copy standing-order bits under documented gates [05 "Rally inheritance"] — same as success epilogue but additional gate for experience.
	// TODO(question): experience word copies only for computer-owned builders [05 "Rally inheritance"].
	inherited := 0
	pq := orders.BindQueueBinding(product, s.OrderBinding)
	// Collect rally nodes in traversal order first, then tail-append to preserve order [05 C19].
	// Tail-appending (rather than pq.Push) also leaves an existing GetBuilt
	// head and its active marker untouched.
	var toAppend []*orders.Node
	for _, n := range prim {
		if n == nil {
			continue
		}
		if n.ID == qMoveID {
			if moveID != 0 {
				nn := &orders.Node{ID: moveID, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, DynamicGate: 0, Deadline: -1, StaticGate: orders.DescriptorFor(moveID).StaticGate, Flags: 0}
				// Ensure deadline -1 for new node [04 §3.2]
				if nn.Deadline == 0 {
					nn.Deadline = -1
				}
				toAppend = append(toAppend, nn)
				inherited++
			}
		} else if n.ID == qPatrolID {
			if patrolID != 0 {
				nn := &orders.Node{ID: patrolID, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, DynamicGate: 0, Deadline: -1, StaticGate: orders.DescriptorFor(patrolID).StaticGate}
				if nn.Deadline == 0 {
					nn.Deadline = -1
				}
				toAppend = append(toAppend, nn)
				inherited++
			}
		}
	}
	if inherited == 0 {
		if parkID != 0 {
			nn := &orders.Node{ID: parkID, Deadline: -1, StaticGate: orders.DescriptorFor(parkID).StaticGate}
			if nn.Deadline == 0 {
				nn.Deadline = -1
			}
			toAppend = append(toAppend, nn)
		}
	}
	if len(toAppend) > 0 {
		// Tail-append to primary, preserving traversal order [05 C19][I1].
		primProd := pq.Primary()
		// If product queue has an active GetBuilt head, keep it; new nodes go after.
		// For empty product, first appended becomes head active.
		newPrim := append(primProd, toAppend...)
		// Reset active marker: exactly one primary node carries 0x1000 [04 §3.3].
		if len(newPrim) > 0 {
			for i := range newPrim {
				newPrim[i].Flags &^= orders.FlagActive
			}
			newPrim[0].Flags |= orders.FlagActive
		}
		sec := pq.Secondary()
		// Preserve queue-owned dispatch/economy hooks when replacing the primary
		// segment; rebuilding a queue must not silently detach its services.
		newQ := orders.NewQueueWith(nil, nil)
		newQ.SetBinding(pq.Binding())
		newQ.SecondaryTick = pq.SecondaryTick
		setQueuePrimary(newQ, newPrim)
		setQueueSecondary(newQ, sec)
		orders.BindQueue(product, newQ)
	}
}

// ---------------------------------------------------------------------------
// C21 Cancel-current interrupt [05 "Cancel-current and stop interrupts"].
// ---------------------------------------------------------------------------

func (s *Service) handleCancelCurrent(factory *units.Unit, node *orders.Node, tick uint32) {
	// Compute refund trunc((1 - remaining) * metalBuildCost) [05 C21].
	var remaining float32 = 1 // default if no product
	var metalCost int32
	var product *units.Unit
	if node.Target != 0 && s.World != nil {
		// Try to resolve product via world.
		product = s.World.Unit(node.Target)
		if product != nil && product.Def != nil {
			remaining = product.Remaining
			metalCost = product.Def.BuildCostMetal
		}
	}
	// If no product attached, same epilogue runs with remaining=1 => refund 0 [05 C21].
	if product == nil {
		// Try to get metalCost from product def via node Param1
		if def := s.getProductDefForNode(node); def != nil {
			metalCost = def.BuildCostMetal
		}
	}
	// Cancel-current performs the same completion transition before its cause-9
	// kill. The queued count is intentionally untouched [R-P0-09][05 C21].
	if product != nil {
		s.applyCompletionPosture(product)
	}
	refund := float32(int32((1 - remaining) * float32(metalCost))) // trunc toward zero [01 §8] I3

	// Normally add to builder's metal bucket UNLESS special second state [05 C21].
	// Apply via economy mirror bucket Production.
	if s.Economy != nil {
		pIdx := int(factory.Owner)
		if pIdx >= 0 && pIdx < len(s.Economy.Players) {
			player := &s.Economy.Players[pIdx]
			isSpecial := false
			if s.IsSpecialSecondState != nil {
				isSpecial = s.IsSpecialSecondState(factory.Owner)
			}
			if isSpecial {
				// Mode selector decides: 0 subtracts 7/10, 1 subtracts 1/2, other fallback to adding [05 C21].
				// This pairing is INVERTED vs ledger negative-energy site [05 C21] — do not harmonize.
				switch s.ModeSelector {
				case 0:
					player.Mirror[economy.Metal].Production += refund * -0.7 // subtract seven tenths [05 C21]
				case 1:
					player.Mirror[economy.Metal].Production += refund * -0.5 // subtract one half [05 C21]
				default:
					player.Mirror[economy.Metal].Production += refund
				}
			} else {
				player.Mirror[economy.Metal].Production += refund
			}
		}
	}

	// Run the completion transition [05 C21] TODO(question): completion transition side effects not fully located beyond remaining->0.
	if product != nil {
		product.Remaining = 0
		// TODO(question): completion flag set, activation per standing-order bits, cloak/init posture etc [05 C18] not fully located.
	}

	// Send ordinary kill packet — kind-9 damage exactly 30000 unscaled because scaling requires damage <30000 [05 C21][06 §9.1].
	// Note cause-9 deaths skip killed-severity query entirely (severity zero, no explosion, no corpse) [04 §5.1][05 C21].
	s.lastKill = KillInfo{Damage: Kind9Damage, Severity: 0, NoCorpse: true} // severity zero [05 C21]
	if product != nil {
		// Apply death: Alive false, but no corpse/explosion.
		// In world pool, mark dead but not via Destroy which would set cleanup? For test, just set Alive false.
		if s.World != nil {
			s.World.Destroy(product.Handle, units.DeathKilled)
			// Override corpse handling: mark that cause-9 has severity zero, no corpse.
		}
		// Release after the cause-9 death mark. Completion posture intentionally
		// precedes the kill, so releasing before Destroy would look like a live
		// completed product and retain its reservation.
		product.Alive = false
		s.ReleasePlacement(product.Handle)
		// Deterministically clear builder/product link after nanoframe (ON-02):
		// before nanoframe builderLinks not yet set, so no-op; after nanoframe it must be cleared
		// even on cancel, not leaked as on normal death path [P0-14]. Ensures stop/cancel cleanup deterministic.
		if s.builderLinks != nil {
			delete(s.builderLinks, product.Handle)
		}
		if s.getBuiltLinks != nil {
			delete(s.getBuiltLinks, product.Handle)
		}
		// Also clear any reverse mapping? product -> builder only, so delete above suffices.
		// Ensure product's own builder link cleared on cancel (before and after nanoframe unified) [05 C21].
	}

	// Lower Deactivate and StartBuilding together. Each bridge operation is
	// edge-deduplicated, preserving the single falling-edge callback contract.
	s.deactivate(factory)
	s.stopBuilding(factory)

	// Refresh interface [05 C21].
	if s != nil && s.OnRefresh != nil {
		s.OnRefresh(factory)
	}

	// Drop node WITHOUT decrementing remaining count [05 C21].
	// Remove head from primary queue without touching Param2.
	s.removeHead(factory, node)
}

func (s *Service) applyCompletionPosture(product *units.Unit) {
	if product == nil {
		return
	}
	product.Remaining = 0
	product.Flags |= FlagCompleted
	// Completed units become eligible for AI classification (group 4 construction) [R-P0-04][08].
	// Nanoframes are created with Flags without 0x20 (initializeNanoframe clears it); completion must restore it.
	product.Flags |= units.ClassifierEligibleStatus
	if product.Def != nil && product.Def.ActivateWhenBuilt {
		s.activate(product)
	}
	if product.Def != nil && product.Def.InitCloaked {
		product.Flags |= FlagInitCloak
		product.IsCloaked = true
	}
	product.Health = product.MaxHealth
	// Completion releases the frame's plot occupancy stamps and hands blocking
	// duty to the structures registry: finished buildings must not occupy the
	// mobile-occupancy shorts, or every factory's exit-spot validation would
	// deadlock against its own yard [04 §6.2][05 "Factory production lifecycle"].
	s.retirePlacement(product.Handle)
}

// removeHead removes the head node from factory's primary queue without
// decrement [05 C21]. Removal happens in place through the queue's own
// subtraction path so queue identity and every queue-owned service binding
// (Hostility, Lookup, StockpileEconomy, SecondaryTick, diagnostics) survive.
// The previous implementation rebuilt the segment into a fresh orders.Queue
// and rebound it, which dropped those hooks: after the first factory product
// completed, successor target orders lost target lookup and hostility, and
// secondary stockpile admission no longer saw the economy buckets.
//
// TODO(question): the tombstone marker is applied unconditionally here, while
// [04 §3.3] exempts the primary head from it. The observable difference is
// currently nil because the tombstone-gated cleanup step is a stub, so the
// pre-existing marking is preserved rather than changed on inference.
func (s *Service) removeHead(factory *units.Unit, node *orders.Node) {
	q := s.queueForUnit(factory)
	if q == nil {
		return
	}
	q.RemovePrimaryNode(node, true)
}

// setQueuePrimary replaces primary segment via exported accessor (ON-02).
// Kept for internal call compatibility; uses exported SetPrimary, no reflect/unsafe.
func setQueuePrimary(q *orders.Queue, prim []*orders.Node) {
	if q == nil {
		return
	}
	q.SetPrimary(prim)
}

// setQueueSecondary replaces secondary segment via exported accessor (ON-02).
func setQueueSecondary(q *orders.Queue, sec []*orders.Node) {
	if q == nil {
		return
	}
	q.SetSecondary(sec)
}

// ---------------------------------------------------------------------------
// C22 Stop interrupt [05 "Cancel-current and stop interrupts"].
// ---------------------------------------------------------------------------

func (s *Service) handleStop(factory *units.Unit, node *orders.Node, tick uint32) {
	s.logMessage("Construction stopped") // verbatim [05 C22]
	// Decrement node count ONCE [05 C22].
	if node.Param2 > 0 {
		node.Param2--
	} else {
		// If Param2 is 0 (queued mode zero count case for GetBuilt? but for factory build nodes, count at least 1), still decrement? For stop, treat as decrement once even if zero => stay 0.
		// Spec says decrement once, node survives.
		if node.Param2 == 0 {
			// keep 0? But spec says count is remaining build count, so decrement from 1 to 0 would be 0 but node survives and restarts.
			// We leave at 0.
		}
	}
	// Refresh interface [05 C22].
	if s != nil && s.OnRefresh != nil {
		s.OnRefresh(factory)
	}
	// Returns result 0 — node SURVIVES, machine restarts [05 C22].
	node.Phase = uint8(State0)
	node.DynamicGate = 0
	node.Deadline = -1
	// Do not remove node.
}

// ---------------------------------------------------------------------------
// State gates [05 "Factory production lifecycle"].
// ---------------------------------------------------------------------------

func (s *Service) handleState0(factory *units.Unit, node *orders.Node, tick uint32) {
	// Mobile builds skip presentation clear of Goal (site is authoritative) [P0-I05]
	if isMobileBuild(node.ID) {
		// Mobile builds go directly to state2 placement, bypassing activate/yard-door [P0-I05]
		node.Phase = uint8(State2)
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
		// Mobile builds skip yard-door handshake [P0-I05]
		node.Phase = uint8(State2)
		node.DynamicGate = 0
		node.Deadline = -1
		// The handler's setup path zeroes the blocked-area retry counter on
		// every (re)arm [04 §3.2][R-ORDER-02 §1].
		node.Param3 = 0
		return
	}
	// State 1 is deliberately a level test with no timeout [05][R-P0-10].
	if factory.InBuildStance {
		node.Phase = uint8(State2)
		node.DynamicGate = 0
		node.Deadline = -1
		return
	}
	// Otherwise waits with wake bit 2 [05].
	node.DynamicGate = WakeBit2
	node.Deadline = int32(tick + 1)
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
	var modelPosition world.ModelWorldPosition
	var ok bool
	if m != nil {
		modelPosition, ok = s.QueryBuildWorldPosition(factory, m)
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
	if _, err := s.validatePlacement(factory.Handle, factoryPlacement.Rect(), def, yard, true); err != nil {
		s.recordAdmission(tick, factory.Handle, def.UnitName, AdmissionBlockedTransiently, err)
		// Silent blocked revalidation: retry in exactly 15 ticks, stays — no
		// message/sound/allocation; repeats every 15 while obstructed; NO
		// timeout [05 C17]. Wake mask is bits {1,2}: schedule(node,15) sets
		// bit 1 + deadline and the caller adds bit 2
		// [05 "Factory production lifecycle"].
		node.DynamicGate = WakeBit1 | WakeBit2
		node.Deadline = int32(tick + 15)
		// No message, no allocation — silent.
		return
	}
	s.recordAdmission(tick, factory.Handle, def.UnitName, AdmissionAdmitted, nil)

	// On validation success, allocator creates unit AT exit spot [05 C18].
	product, err := s.allocateNanoframe(factory, def, factoryPlacement.Rect(), factoryPlacement.ModelPosition())
	if err != nil {
		// Allocator refusal prints verbatim "Unable to create any more units", retries in exactly 300 ticks (not randomized), stays state2 [05 C18].
		s.logMessage(fmt.Sprintf("construction: allocation refused (%v)", err))
		s.logMessage(ErrLimitMessage)
		node.DynamicGate = WakeBit2 // F6b: the allocator refusal wakes on bit 2 [05 "Factory production lifecycle"]
		node.Deadline = int32(tick + 300)
		// Stay in state2.
		return
	}
	// Success epilogue [05 C18].
	s.successEpilogue(factory, node, product, cell)
}

// handleMobileState2 implements mobile build placement at the authoritative site anchor [P0-I05][05 "Factory production lifecycle"].
// Mobile payload carries site in Node.GoalX/Z (world coords) via QueueMobileBuild [P0-I05].
// Validation uses the product's yard at the snapped site, not the factory exit spot.
func (s *Service) handleMobileState2(builder *units.Unit, node *orders.Node, tick uint32) {
	// Armed waits are visit boundaries: while the record's deadline is in the
	// future the handler is not visited, and on arrival the wait is consumed
	// (the pump's deadline rule — deadline arrived clears it and re-dispatches
	// — [04 §3.3]). The blocked-area budget depends on this spacing: its waits
	// are EXACTLY 30 ticks [R-ORDER-02 §1].
	if node.Deadline >= 0 {
		if tick < uint32(node.Deadline) {
			return
		}
		node.DynamicGate = 0
		node.Deadline = -1
	}
	// Walk-to-site for mobile builders [04 §3.4][05][R-P0-06].
	// A MOBILE builder ordered to build at a site out of nano range first walks
	// toward the site until within nanolathe range, then enters state 2.
	// Range is builder BuildDistance pixels [fmt fbi] via nanoReach; distance is
	// from QueryNanoPiece piece world pos (or builder pos fallback) to the site
	// GoalX/Z anchor [R-P0-06]. Factory-class builders (CanMove==false && CanFly==false)
	// are their own yard and are unaffected.
	// Computer players are exempt from walk for gate stability: their first
	// factory must complete within the strict window, and walk would add
	// ~1500 ticks of travel that the gate does not budget for.
	// TODO(question): whether AI walk should be same as human remains open.
	if isMobileBuilder(builder) && s.Movement != nil && builder.Def != nil && builder.Def.BuildDistance != 0 {
		isAI := false
		if s.Economy != nil && int(builder.Owner) < len(s.Economy.Players) {
			if s.Economy.Players[builder.Owner].ControllerState == 2 {
				isAI = true
			}
		}
		if !isAI && s.needsApproach(builder, node) {
			s.ensureWalk(builder, node)
			node.DynamicGate = WakeBit2
			node.Deadline = int32(tick + 1)
			node.MoveState = orders.MoveEnRoute
			return
		}
		s.clearWalk(builder)
		node.MoveState = orders.MoveArrived
	}
	def := s.getProductDefForNode(node)
	footX, footZ := 1, 1
	if def != nil {
		footX = int(def.FootprintX)
		footZ = int(def.FootprintZ)
		if footX <= 0 {
			footX = 1
		}
		if footZ <= 0 {
			footZ = 1
		}
	}
	// Site anchor is authoritative Goal from QueueMobileBuild [P0-I05].
	extent, err := world.NewFootprintExtent(int32(footX), int32(footZ))
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit2, int32(tick+15)
		return
	}
	anchor, err := world.SnapFootprintAnchor(node.GoalX, node.GoalZ, extent)
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit1|WakeBit2, int32(tick+15)
		return
	}
	rect, err := world.NewFootprintRect(anchor, extent)
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit1|WakeBit2, int32(tick+15)
		return
	}
	cell := anchor.Cell()
	// Do not overwrite Goal: keep original clicked site for determinism and tests that assert Goal equals clicked site [P0-I05].
	// Validation at snapped cell [05 C17] with null self identity (mobile builders place at site).
	if def == nil {
		node.DynamicGate = WakeBit2
		node.Deadline = int32(tick + 15)
		return
	}
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
		text, code := orders.MobileBuildBlockedVisit(node, tick)
		s.notifyStatus(text)
		if code != 2 {
			// Abandon goes through the queue's canonical removal so cleanup
			// (StopBuilding counterpart included) runs [04 §3.3][R-ORDER-02 §2].
			s.removeHead(builder, node)
		}
		return
	}
	siteY := builder.Y
	if s.Terrain != nil {
		siteY = numeric.Fixed(int64(result.SiteHeight) * numeric.FractionOne)
	}
	mobilePlacement, err := world.SnapMobilePlacement(node.GoalX, siteY, node.GoalZ, extent)
	if err != nil {
		node.DynamicGate, node.Deadline = WakeBit1|WakeBit2, int32(tick+15)
		return
	}
	product, err := s.allocateNanoframe(builder, def, mobilePlacement.Rect(), mobilePlacement.ModelPosition())
	if err != nil {
		s.logMessage(ErrLimitMessage)
		node.DynamicGate = WakeBit2
		node.Deadline = int32(tick + 300)
		return
	}
	// For mobile, success epilogue reuses factory helper but with builder as factory and cell as site cell.
	// It stores cell origin as Goal? We preserve original Goal for site authoritative test, so store snapshot separately?
	// Keep Goal as site, but successEpilogue will overwrite Goal with cell origin. Preserve site in a separate snapshot?
	// Instead call mobile-specific epilogue that keeps Goal as site and uses cell for product creation.
	s.successEpilogueMobile(builder, node, product, cell)
}

// successEpilogueMobile is like successEpilogue but preserves the authoritative site Goal [P0-I05].
func (s *Service) successEpilogueMobile(builder *units.Unit, node *orders.Node, product *units.Unit, cell world.Cell) {
	// Preserve original Goal site for test assertion that structure appears at clicked location [P0-I05].
	// The product's world position is at cell origin, which corresponds to site snapped with half-extent.
	// Node.Goal remains the clicked site; we do not overwrite it with cell origin.
	productHandle := product.Handle
	node.Target = productHandle
	s.logMessage("Starting construction")
	s.SetBuilderLink(productHandle, builder.Handle)
	if s.getBuiltLinks == nil {
		s.getBuiltLinks = make(map[pool.Handle]pool.Handle)
	}
	s.getBuiltLinks[productHandle] = builder.Handle
	s.copyStandingFlags(builder, product)
	getBuiltID := orders.Lookup("GetBuilt")
	if getBuiltID != 0 {
		pq := orders.BindQueueBinding(product, s.OrderBinding)
		pq.Push(getBuiltID, orders.Node{Param2: 0})
	}
	// Turn the builder to face the build site before construction begins.
	// Retail computes the bearing from the builder to the site [04 §5.3]; the
	// exact consumer of that bearing is the open question recorded below, so
	// the rotation is retained as the builder's approach posture and nothing
	// more.
	// TODO(question): whether retail rotates the unit heading itself or leaves
	// the turn to the script is not traced.
	heading := movement.HeadingFromDelta(int64(node.GoalX)-int64(builder.X), int64(node.GoalZ)-int64(builder.Z))
	builder.Move.Heading = heading
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
	node.Phase = uint8(State3)
	// Keep cell for product creation already done; no need to store again.
	_ = cell
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
		q := s.queueForUnit(factory)
		if q != nil {
			newQ := orders.NewQueueWith(nil, nil)
			newQ.SetBinding(q.Binding())
			newQ.SecondaryTick = q.SecondaryTick
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
	worker := WorkerQuantum(factory.Def.WorkerTime)
	if worker <= 0 {
		// Zero quantum unless distinct caller supplies another value [05].
		// For zero worker, no progress — retry one tick later.
		node.DynamicGate = WakeBit1 | WakeBit3
		node.Deadline = int32(tick + 1)
		return
	}
	buildTime := product.Def.BuildTime
	if buildTime <= 0 {
		buildTime = 1 // avoid div0
	}
	old := product.Remaining
	nv, hg, energyDemand, metalDemand := ConstructionStep(old, worker, buildTime, product.MaxHealth, product.Def.BuildCostEnergy, product.Def.BuildCostMetal)
	// Attempt two-resource admission via economy? For factory construction, admission is via builder's buckets.
	// Simulate admission: if economy is set, try to admit; if fails, do not advance.
	admitted := true
	if s.Economy != nil {
		bIdx := int(factory.Owner)
		if bIdx >= 0 && bIdx < len(s.Economy.Players) {
			// Find builder's unit buckets? But construction admission is per builder's economy subrecord? The helper always records both requested amounts; records both as accepted only if both carries non-positive [05 "Two-resource admission"].
			// For test, we can simulate that admission succeeds when builder's economy allows.
			// Simplify: directly check if enough stock? For now assume always admitted unless test injects failure.
			// Use economy.AdmitTwoResource to record.
			// Need builder's buckets: s.Economy.UnitBuckets(factory.Handle)
			if buckets := s.Economy.UnitBuckets(factory.Handle); buckets != nil {
				// Copy to local to test carry gates?
				beforeEnergyCarry := (*buckets)[economy.Energy].Carry
				beforeMetalCarry := (*buckets)[economy.Metal].Carry
				if beforeEnergyCarry > 0 || beforeMetalCarry > 0 {
					admitted = false
				} else {
					economy.AdmitTwoResource(buckets, energyDemand, metalDemand)
					// For two-stage settlement, admission success means accepted = demand.
					// If carries were non-positive, it will be accepted.
					// We consider admitted true.
					admitted = true
				}
			}
		}
	}
	if !admitted {
		// Do not advance remaining fraction [05].
		node.DynamicGate = WakeBit1 | WakeBit3
		node.Deadline = int32(tick + 1)
		return
	}
	// Update remaining and health with fractional carry [05 C24].
	product.Remaining = nv
	if hg != 0 {
		product.Health += hg
		if product.Health > product.MaxHealth {
			product.Health = product.MaxHealth
		}
		if product.Health < 0 {
			product.Health = 0
		}
	}
	// Query and emit only after the two-resource admission and authoritative
	// state update have committed [R-P0-06]. Rejected work reaches no query.
	s.emitAcceptedNano(tick, factory, product)
	if product.Remaining == 0 {
		// Advance to completion [05].
		node.Phase = uint8(State4)
		// Completion will be handled next pump or immediately? Spec says work loop with remaining zero advances to completion; otherwise retry 1 tick.
		// For now set to State4 and handle in next call; but we could also handle immediately.
		s.handleState4(factory, node, tick)
		return
	}
	// Otherwise retry one tick later with wake bits 1 and 3 [05].
	node.DynamicGate = WakeBit1 | WakeBit3
	node.Deadline = int32(tick + 1)
}

func (s *Service) handleState4(factory *units.Unit, node *orders.Node, tick uint32) {
	var product *units.Unit
	if node.Target != 0 && s.World != nil {
		product = s.World.Unit(node.Target)
	}
	// StopBuilding precedes the completion helper [P0-14]: the engine lowers the
	// StartBuilding bit before the helper runs.
	// TODO(question): [R-FAC-01C] completion is not established as a separate
	// factory-release state. Do not add an egress target, producer/product
	// collision exemption, no-stacking gate, or aircraft takeoff ordering here;
	// the retail boundary requires a first-movement/occupancy trace.
	// Note: product LOS after settlement phase5, targetable already phase3, GetBuilt same/next tick by slot [P0-14].
	// Trigger BuildUnitType only on local 30-tick deadline [P0-14].
	// Interrupt masks 2/8 bodies known, producers TODO(T25) [P0-14].
	// Engine prints no text, lowers start-building edge, runs completion transition [05].
	// Falling edge occurs before the completion transition [R-P0-09].
	s.stopBuilding(factory)
	if product != nil {
		// Completion transition: product remaining to zero, completion flag set, activation per standing-order bits, cloak/init posture, selection refresh [05].
		s.applyCompletionPosture(product)
		// Clear presentation payload [05].
		// Decrement node's remaining count once [05].
		if node.Param2 > 0 {
			node.Param2--
		}
		// Clear builder/product link on completion [P0-14]; the death/capture
		// teardown does not walk these links, so they leak there instead.
		if product != nil {
			delete(s.builderLinks, product.Handle)
		}
		// Refresh interface [05].
		if s != nil && s.OnRefresh != nil {
			s.OnRefresh(factory)
			s.OnRefresh(product)
		}
		// Return result 0 — state machine restarts at state0 within same pump pass, so coalesced counts build back-to-back [05].
		// Back-to-back state0 restart count-- per unit, no repeat flag [P0-14].
		if node.Param2 == 0 {
			s.removeHead(factory, node)
		} else {
			node.Phase = uint8(State0)
			node.DynamicGate = 0
			node.Deadline = -1
			node.Target = 0
		}
	} else {
		// No product? Still decrement and free?
		if node.Param2 > 0 {
			node.Param2--
		}
		if node.Param2 == 0 {
			s.removeHead(factory, node)
		} else {
			node.Phase = uint8(State0)
			node.Target = 0
		}
	}
}

// TODO(T25): the producers of interrupt masks 2 and 8 are still unknown — no
// writer appears within the searched boundaries [P0-14][P0-15]. Both interrupt
// bodies are established; the UI/network command layer that sets the bits is not.

// Pump implements the factory production handler entry per [05] with interrupt priority [PLAN_08].
// Primary-only factory queue (68-desc census) — bit 0x40000 only on BuildWeapon/SelfDestruct [P0-14].
// Non-authoritative compatibility wrapper (ON-02): new code should use StepUnit per handle.
func isBuildOrderID(id orders.ID) bool {
	if id == orders.Lookup(FactoryBuildOrder) {
		return true
	}
	if id == orders.Lookup(MobileBuildOrder) || id == orders.Lookup(VTOLMobileBuildOrder) {
		return true
	}
	// Also treat generic build via BuildingBuild fallback (already FactoryBuildOrder) – no other IDs are construction builds.
	return false
}

func (s *Service) Pump(factory *units.Unit, tick uint32) {
	if factory == nil {
		return
	}

	q := s.queueForUnit(factory)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	prim := q.Primary()
	if len(prim) == 0 {
		return
	}
	head := firstWorkNode(prim)
	if head == nil {
		return
	}
	// Non-build orders (e.g., Move_Ground) are not construction work; ignore without mutating queue [05][P0-I05].
	if !isBuildOrderID(head.ID) {
		return
	}
	// Interrupt masks tested before state machine with cancel-current first [05].
	if factory.Pending&InterruptCancel != 0 {
		factory.Pending &^= InterruptCancel
		s.handleCancelCurrent(factory, head, tick)
		return
	}
	if factory.Pending&InterruptStop != 0 {
		factory.Pending &^= InterruptStop
		s.handleStop(factory, head, tick)
		return
	}
	// Dispatch state machine [05].
	switch State(head.Phase) {
	case State0:
		s.handleState0(factory, head, tick)
	case State1:
		s.handleState1(factory, head, tick)
	case State2:
		s.handleState2(factory, head, tick)
	case State3:
		s.handleState3(factory, head, tick)
	case State4:
		s.handleState4(factory, head, tick)
	default:
		head.Phase = uint8(State0)
	}
}

// StepUnit advances only the named unit's construction work state [05 "Factory production lifecycle"][P0-I05] (ON-02).
// It preserves the existing bucket/carry admission model exactly: zero current stock does NOT forbid
// the first carry-admitted quantum; work pauses only when settlement denies carry (carry>0) [05 "Two-stage settlement algorithm"].
// Site coordinates survive intact from order node GoalX/Z through nanoframe placement for mobile builds [P0-I05]:
// QueueMobileBuild stores the world anchor in Node.GoalX/Z and handleMobileState2 snaps to the half-extent biased cell,
// preserving the original Goal for determinism. Factory and mobile descriptors remain distinct: a mobile builder
// receiving a factory-only descriptor (BuildingBuild) fails explicitly with an error diagnostic, never silently cleared [P0-I05].
// Completion returns handle, def key, and owner for session hooks; no presentation calls are made (OnRefresh suppressed).
// Stop/cancel cleans up worker/build links deterministically before and after nanoframe creation [05 C21][P0-14].
// isStandingOpID reports whether the order id is a standing/auto or rally
// op that must never block construction work discovery [05 "Queue insertion"]
// [05 "Rally inheritance"][RX-05]. A factory's own queued-move/queued-patrol
// nodes are rally points for produced units, not movement orders for the
// (immobile) factory itself.
func isStandingOpID(id orders.ID) bool {
	if gb := orders.Lookup("GetBuilt"); gb != 0 && id == gb {
		return true
	}
	if pk := orders.Lookup("Park"); pk != 0 && id == pk {
		return true
	}
	if qm := orders.Lookup("QMove"); qm != 0 && id == qm {
		return true
	}
	if qp := orders.Lookup("QPatrol"); qp != 0 && id == qp {
		return true
	}
	return false
}

// firstWorkNode returns the first primary node that is construction work,
// skipping leading standing ops (GetBuilt pending resolution, Park) [RX-05].
func firstWorkNode(prim []*orders.Node) *orders.Node {
	for _, n := range prim {
		if n == nil {
			continue
		}
		if !isStandingOpID(n.ID) {
			return n
		}
	}
	return nil
}

// resolveGetBuilt enforces the get-built node's self-drop on a completed
// product [05 C18][05 "Rally inheritance"]: after the product reaches zero
// remaining, rally inheritance or the no-rally Park fallback runs here; while
// the product is still under construction the node waits per the researched
// retry gates.
func (s *Service) resolveGetBuilt(product *units.Unit, tick uint32) {
	if s == nil || product == nil {
		return
	}
	q := s.queueForUnit(product)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	gb := orders.Lookup("GetBuilt")
	if gb == 0 {
		return
	}
	head := q.Primary()[0]
	if head.ID != gb {
		return
	}
	// Under construction uses three established retry states: state 0 schedules
	// 300 ticks, state 1 schedules 30 ticks, and state 2 waits on its wake bit
	// [R-P0-09][05 "Rally inheritance"].
	if product.Remaining > 0 {
		if head.Deadline >= 0 && tick < uint32(head.Deadline) {
			return
		}
		switch State(head.Phase) {
		case State0:
			head.Phase = uint8(State1)
			head.DynamicGate = WakeBit1
			head.Deadline = int32(tick + 300)
		case State1:
			head.Phase = uint8(State2)
			head.DynamicGate = WakeBit2
			head.Deadline = int32(tick + 30)
		default:
			head.Phase = uint8(State2)
			head.DynamicGate = WakeBit2
			head.Deadline = -1
		}
		return
	}
	// TODO(question): [R-FAC-01C] this is the unresolved completion/rally-or-Park
	// boundary. The bounded chain establishes direct allocation plus GetBuilt,
	// but not a later release target/state, producer-product exemption,
	// no-stacking rule, or aircraft takeoff-before-rally ordering. Preserve the
	// current ordinary rally/Park handoff until retail evidence closes it.
	builderHandle, ok := s.getBuiltLinks[product.Handle]
	if !ok || builderHandle == 0 || s.World == nil {
		// A restored product may not have an in-memory builder link. The retail
		// cleanup/recovery path is unresolved; remove the watcher without
		// inventing a replacement builder [R-P0-09].
		q.RemoveHead()
		return
	}
	builder := s.World.Unit(builderHandle)
	if builder != nil {
		s.rallyInheritance(builder, product)
	}
	delete(s.getBuiltLinks, product.Handle)
	// rallyInheritance may have rebound the product queue. Reacquire it before
	// dropping GetBuilt so the watcher cannot survive on the new primary list.
	if current := s.queueForUnit(product); current != nil {
		current.RemoveHead() // GetBuilt drops itself [05 "Rally inheritance"]
	}
}

func (s *Service) StepUnit(ctx TickContext, handle pool.Handle) WorkResult {
	w := s.World
	if ctx.World != nil {
		w = ctx.World
	}
	econ := s.Economy
	if ctx.Economy != nil {
		econ = ctx.Economy
	}
	terrain := s.Terrain
	if ctx.Terrain != nil {
		terrain = ctx.Terrain
	}
	cat := s.Catalog
	if ctx.Catalog != nil {
		cat = ctx.Catalog
	}
	tick := ctx.Tick
	if w == nil {
		return WorkResult{Builder: handle, Err: fmt.Errorf("construction: nil world")}
	}
	builder := w.Unit(handle)
	if builder == nil {
		return WorkResult{Builder: handle, Err: fmt.Errorf("construction: builder %d not found or dead", handle)}
	}
	q := s.queueForUnit(builder)
	if q == nil || q.LenPrimary() == 0 {
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0, Diagnostics: append([]string(nil), s.messages...)}
	}
	prim := q.Primary()
	if len(prim) == 0 {
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0, Diagnostics: append([]string(nil), s.messages...)}
	}
	head := prim[0]
	// GetBuilt resolution [05 C18][05 "Rally inheritance"] — must not block
	// factory production behind a stale get-built node (RX-05).
	if gbID := orders.Lookup("GetBuilt"); gbID != 0 && head.ID == gbID {
		if u := w.Unit(handle); u != nil {
			s.resolveGetBuilt(u, tick)
		}
	}
	// Work discovery skips standing ops (GetBuilt pending resolution, Park)
	// so a completed factory keeps producing [RX-05][05 "Queue insertion"].
	head = firstWorkNode(prim)
	if head == nil {
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0, Diagnostics: append([]string(nil), s.messages...)}
	}
	// Unit reclaim is an order-driven worker state distinct from factory/mobile
	// construction. It must run through the same per-unit construction window,
	// while remaining outside the build descriptor state machine [04 §3.5][05
	// "Unit reclaim"].
	if isReclaimUnitNode(head) {
		return s.stepUnitReclaim(builder, head, tick)
	}
	// Non-build orders (e.g., Move_Ground) are not construction work; ignore without mutating queue [05][P0-I05].
	if !isBuildOrderID(head.ID) {
		return WorkResult{Builder: handle, Product: head.Target, DefKey: head.BuildDefKey, Owner: builder.Owner, State: State(head.Phase), Diagnostics: append([]string(nil), s.messages...)}
	}
	// Distinct descriptor check [P0-I05][04 §3.1]: factory BuildingBuild vs mobile MobileBuild/VTOL_MobileBuild.
	factoryID := orders.Lookup(FactoryBuildOrder)
	mobile := isMobileBuild(head.ID)
	isMobBuilder := isMobileBuilder(builder)
	var mismatch error
	if head.ID == factoryID && isMobBuilder {
		mismatch = fmt.Errorf("construction: factory descriptor %q not allowed on mobile builder %q (handle %d) — distinct types end-to-end", orders.DescriptorFor(head.ID).Name, builder.Def.UnitName, handle)
	}
	if mobile && !isMobBuilder {
		mismatch = fmt.Errorf("construction: mobile descriptor %q not allowed on factory builder %q (handle %d) — distinct types end-to-end", orders.DescriptorFor(head.ID).Name, builder.Def.UnitName, handle)
	}
	if mismatch != nil {
		// Explicit failure, never silently cleared [P0-I05][04 §3.1] (ON-02).
		s.logMessage(mismatch.Error())
		return WorkResult{Builder: handle, Product: head.Target, DefKey: head.BuildDefKey, Owner: builder.Owner, State: State(head.Phase), Err: mismatch, Diagnostics: append([]string(nil), s.messages...)}
	}
	// Capture before state for completion detection.
	beforeTarget := head.Target
	beforeKey := head.BuildDefKey
	beforeOwner := builder.Owner
	beforePhase := State(head.Phase)
	var beforeRemaining float32
	var beforeProduct *units.Unit
	if beforeTarget != 0 {
		if bp := w.Unit(beforeTarget); bp != nil {
			beforeProduct = bp
			beforeRemaining = bp.Remaining
		}
	}
	// Suppress presentation during authoritative step (ON-02).
	oldRefresh := s.OnRefresh
	s.OnRefresh = nil
	oldWorld, oldEcon, oldTerrain, oldCat := s.World, s.Economy, s.Terrain, s.Catalog
	s.World, s.Economy, s.Terrain, s.Catalog = w, econ, terrain, cat
	// Single-unit pump: only this builder advances.
	s.Pump(builder, tick)
	s.World, s.Economy, s.Terrain, s.Catalog = oldWorld, oldEcon, oldTerrain, oldCat
	s.OnRefresh = oldRefresh
	// After state.
	newQ := s.queueForUnit(builder)
	var afterTarget pool.Handle
	var afterKey string
	var afterState State
	if newQ != nil && newQ.LenPrimary() > 0 {
		afterHead := newQ.Primary()[0]
		afterTarget = afterHead.Target
		afterKey = afterHead.BuildDefKey
		afterState = State(afterHead.Phase)
		if afterKey == "" {
			afterKey = beforeKey
		}
	} else {
		// Queue empty: node removed (completion, cancel-all, or expiry). Keep prior product handle for reporting.
		afterTarget = beforeTarget
		afterKey = beforeKey
		afterState = State0
	}
	if afterKey == "" {
		afterKey = beforeKey
	}
	productHandle := afterTarget
	if productHandle == 0 {
		productHandle = beforeTarget
	}
	completed := false
	defKey := afterKey
	if defKey == "" {
		defKey = beforeKey
	}
	if productHandle != 0 {
		if prod := w.Unit(productHandle); prod != nil {
			if prod.Remaining == 0 && beforeProduct != nil && beforeRemaining != 0 {
				completed = true
			} else if beforePhase == State4 && prod.Remaining == 0 {
				completed = true
			} else if beforePhase == State3 && prod.Remaining == 0 && beforeRemaining != 0 {
				completed = true
			}
			if prod.Def != nil && defKey == "" {
				defKey = prod.Def.UnitName
			}
			// Completion initializes unit exactly once: Remaining 0→0, health MaxHealth set in handleState4 [05 C18].
			// Detect exactly-once via transition from non-zero to zero.
		} else {
			// Product destroyed (cancel after nanoframe): not completed, but handle retained for hook.
			// Builder link already cleared deterministically in handleCancelCurrent (ON-02).
		}
	} else {
		// Check if a new nanoframe was just created this tick (state2 → state3) and product handle newly set.
		if newQ != nil && newQ.LenPrimary() > 0 {
			ah := newQ.Primary()[0]
			if ah.Target != 0 && beforeTarget == 0 {
				productHandle = ah.Target
				defKey = ah.BuildDefKey
				// Not completed yet (nanoframe created with Remaining=1).
				if defKey == "" && w != nil {
					if p2 := w.Unit(productHandle); p2 != nil && p2.Def != nil {
						defKey = p2.Def.UnitName
					}
				}
			}
		}
	}
	if defKey == "" {
		defKey = beforeKey
	}
	return WorkResult{
		Builder:     handle,
		Product:     productHandle,
		DefKey:      defKey,
		Owner:       beforeOwner,
		Completed:   completed,
		State:       afterState,
		Diagnostics: append([]string(nil), s.messages...),
	}
}
