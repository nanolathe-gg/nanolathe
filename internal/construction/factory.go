// The factory production service [05 "Factory production lifecycle"]: the
// service record, its diagnostics and the per-unit step the session drives it
// through.

package construction

import (
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// State is the factory production handler's phase byte [05 "Factory production lifecycle"].
type State uint8

// AdmissionStatus is the result class of factory state-2 admission. Only a
// blocked footprint is retried; invalid content retains a permanent diagnostic
// instead of entering the silent 15-tick loop [05
// "Factory production lifecycle"][04 §6.4].
type AdmissionStatus uint8

// The three outcomes of a state-2 admission attempt: the product was created,
// the attempt was blocked by something that may pass (a busy site, a refused
// resource admission), or the definition can never be built and the record is
// rejected outright rather than retried [05 "Factory production lifecycle"]
// [04 §6.4].
const (
	AdmissionAdmitted AdmissionStatus = iota + 1
	AdmissionBlockedTransiently
	AdmissionRejectedPermanentDefinition
)

// String names the status for a diagnostic.
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

// FlagCompleted belongs to units.Unit.Flags [R-P0-09]. Operational callbacks
// use Unit's one-byte state-edge service [05 R-ECO-01 §8].
const (
	// The activation edge has no flag here. Bits 0 and 1 of this word were a
	// placeholder second copy of the activated bit that drifted from the
	// authoritative one; retail keeps a single engine-state byte written
	// through a single edge machine [04 R-UNIT-06 §2], which nanolathe models
	// as units.Unit.Activated with units.Unit.SetActivationEdge as its one
	// writer — the same bit the economy branch gate reads [05 R-PROD-01 §2].
	// There is no init-cloak flag here either. A FlagInitCloak naming bit 14 of
	// the instance flag word was raised by the completion transition on an
	// `init_cloaked` product, from doc 04 §3.8's earlier mislabel of the
	// transition's capability-bit-24 arm. Bit 24 is `isfeature` and bit 14 is
	// the death latch, not a cloak posture [04 R-SPEC-01 §12][03 R-VIS-01 §6];
	// the constant and its write are gone (RWU-19-26).
	FlagCompleted uint32 = 0x00002000 // completion marker in the instance flag word [R-P0-09]
)

// Damage constants [05 "Cancel-current and stop interrupts"] C21.
const (
	// The fixed nominal bypasses the armor branch, whose comparison is strictly
	// below 30000. Defender veterancy still scales the accepted packet [06 §9.2].
	Kind9Damage int32 = 30000
	// Kind9Cause is the damage-kind byte the two refund paths' packet carries
	// [06 §12.1]: "cause 9 — construction-fraction deconstruction/refund:
	// packet builder invoked with 30000 from the two refund paths. Credit
	// branch: none." The death finalizer reads this byte, not the coarse
	// death label, to select the corpse chain and the explosion — and cause 9
	// is one of the three that skip the Killed query outright, so a frame
	// stamped with anything else vanishes with a wreck it should not leave
	// [04 §5.1][05 C21].
	Kind9Cause uint8 = 9
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
	Combat  *combat.Service
	// OrderBinding is the owning session context copied to every product and
	// reconstructed/replaced queue [04 §3.3][06 §11.1].
	OrderBinding *orders.QueueBinding
	// Allocator hook for tests; if nil, uses World.Create.
	Allocator func(owner uint8, def *content.UnitDef, x, y, z numeric.Fixed) (*units.Unit, error)
	// ModelForFactory hook for QueryBuildInfo when m param is nil; tests may set.
	ModelForFactory func(factory *units.Unit) *model.Model
	// OnRefresh is the interface refresh hook [05 C18][05 C21][05 C22].
	OnRefresh func(*units.Unit)
	// Presentation receives already-admitted construction cues: one call per
	// accepted work step, after the authoritative update and the nano-piece
	// query [R-P0-06 §6]. It is optional for headless simulation and its
	// verdict never feeds back into authoritative state [EVENT-01].
	//
	// It is NOT a pure frame-event sink. Every producer in [R-P0-06 §1]'s
	// table also appends the strip-6 emitter whose first five particles spend
	// thirty CRT draws [03 R-STRIP-01 §3], and the CRT stream is authoritative
	// [01 §7.5]. The session binds an adapter that does both halves; binding a
	// bare frame event buffer here — as the session used to — publishes the
	// cue and silently drops the draws. That is why this seam stays one method
	// wide and carries the geometry on the event: the adapter needs no builder,
	// target or piece of its own.
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

	// ModeSelector is the difficulty word that selects the computer player's
	// production scaling: 0 credits a HALF, 1 credits seven tenths, any other
	// value credits the whole amount [05 R-ECO-01 §3][05 R-ECO-01 §11].
	//
	// Correction (PT3-05 follow-up). This used to read "0 => subtract 7/10,
	// 1 => subtract 1/2 ... This pairing is INVERTED relative to the ledger's
	// negative-energy-use refund site". Both halves were wrong; see the
	// cancel-refund arm below for the trace that retires them.
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

	// Per-session derived index for progress publication and the construction
	// removal helper. Saved producer order targets rebuild it; retail's carrier
	// attachment is a separate relation [08 R-SAVE-02 §11].
	builderLinks map[pool.Handle]pool.Handle // product -> builder
	// SETTLED (WU-19-166), retiring an open-question marker that read "if an
	// in-battle restore boundary is introduced, persist placements together
	// with the production node phase/count/target ...". Placements must NOT be
	// persisted: retail's load is "reconstruction, not pointer restoration",
	// and its step 10 rebuilds "derived occupancy, registrations, lists, and
	// presentation caches" after the units and their queues have been recreated
	// in stable slots (steps 7–9) [08 "Load process"]. A placement record is
	// exactly derived occupancy — the footprint rectangle a live unit's
	// position and definition already determine — so a restore rebuilds this
	// map by re-registering each live building, the same way a goal payload is
	// re-installed rather than saved (Service.installApproachGoal). What a save
	// does carry for this service is the order record itself (phase, count,
	// target), which internal/orders owns. There is no alternate Nanolathe save
	// codec [I13]; do not invent a factory-only format.
	placements    map[pool.Handle]placementRecord // product -> occupancy footprint and immutable definition
	productIndex  map[uint32]string               // product id -> catalog key, built once
	messages      []string                        // verbatim diagnostics [05 C18][05 C21][05 C22]
	admissions    []AdmissionDiagnostic           // state-2 outcomes, diagnostic only
	commands      []CommandDiagnostic             // command-boundary rejections
	lastPermanent AdmissionDiagnostic             // bounded malformed-node dedupe key
	hasPermanent  bool
	lastKill      KillInfo // most recent kind-9 kill packet [05 C21]
	// completedInPump is the product the completion transition ran on during the
	// pump StepUnit is currently driving, or 0. It exists because the factory
	// state machine "restarts at state 0 within the same pump pass, so coalesced
	// counts build back-to-back with no gap" [05 "Factory production lifecycle"]:
	// by the time StepUnit inspects the node again its Target is the SUCCESSOR's
	// nanoframe, so deriving the completed handle from the post-pump head
	// reported completion only for the last product of a run. Every earlier
	// product silently never reached the session's completion hook, so its
	// post-completion session services were skipped.
	completedInPump pool.Handle

	// Registration operands, resolved once per service. queueForUnit runs for
	// every stepped builder every tick, and re-resolving the row names through
	// the descriptor table and rebuilding the two bound handler values there
	// was a string lookup and three closure allocations per builder per tick.
	// The values are derived from the immutable descriptor table and from this
	// service; nothing here is per-queue or per-tick state.
	rowsResolved          bool
	stepDrivenRows        []orders.ID
	mobileWakeRows        []orders.ID
	getBuiltRow           orders.ID
	boundConstructionWake orders.OwnedHandler
	// Only StepUnit may execute the reclaim row body; earlier queue visits
	// preserve their delivered events on the same node until this window.
	reclaimStepNode *orders.Node
	// Air builds use the ordinary primary pump inside the construction visit.
	// This is only the current call's owner; all progress lives on the node.
	vtolBuildStepOwner *units.Unit
	boundGetBuilt      orders.OwnedHandler
}

// ensureRegistrationRows resolves this service's row ids and binds its two
// handler values, once.
func (s *Service) ensureRegistrationRows() {
	if s == nil || s.rowsResolved {
		return
	}
	s.rowsResolved = true
	for _, name := range buildRowsDrivenByStepUnit {
		if id := orders.Lookup(name); id != 0 {
			s.stepDrivenRows = append(s.stepDrivenRows, id)
		}
	}
	for _, name := range []string{MobileBuildOrder, VTOLMobileBuildOrder} {
		if id := orders.Lookup(name); id != 0 {
			s.mobileWakeRows = append(s.mobileWakeRows, id)
		}
	}
	s.getBuiltRow = orders.Lookup(GetBuiltOrder)
	s.boundConstructionWake = s.constructionWakeVisit
	// The same adaptation Queue.SetGetBuiltHandler performs — a GetBuilt visit
	// always advances the record — bound once instead of per registration.
	s.boundGetBuilt = func(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
		return s.handleGetBuiltOrder(u, n, satisfied, tick), true
	}
}

// registerGetBuilt binds the construction-owned `GetBuilt` lifecycle to q
// [04 R-FAC-02 §4], through the same owned-handler seam
// Queue.SetGetBuiltHandler writes.
func (s *Service) registerGetBuilt(q *orders.Queue) {
	if s == nil || q == nil {
		return
	}
	s.ensureRegistrationRows()
	q.SetOwnedHandler(s.getBuiltRow, s.boundGetBuilt)
}

type placementRecord struct {
	rect     world.FootprintRect
	def      *content.UnitDef
	yardOpen bool
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
	if q != nil && s != nil {
		s.registerGetBuilt(q)
	}
	s.RegisterOrderHandlers(q)
	return q
}

// buildRowsDrivenByStepUnit are the order rows this service advances from its
// own per-unit step (StepUnit) rather than from the order pump: the factory and
// mobile-build lifecycle [05 "Factory production lifecycle"][04 R-FAC-02 §4]
// and the unit-reclaim machine [05 "Unit reclaim"]. Build records own their
// continuing phase, gate and deadline. Reclaim uses the ordinary pump epilogue
// only inside StepUnit's construction window.
//
// The slice is fixed and ordered, never a map: registration walks it in source
// order (I1).
var buildRowsDrivenByStepUnit = []string{
	FactoryBuildOrder,    // BuildingBuild
	MobileBuildOrder,     // MobileBuild
	VTOLMobileBuildOrder, // VTOL_MobileBuild
	"ReclaimUnit",
	"VTOL_ReclaimUnit",
}

// RegisterOrderHandlers declares this service's ownership of the build rows on
// q, through the order package's per-queue registration seam. It is the
// statement that replaced the descriptor table's DriverExternalMachine value:
// the pump stays the sole dispatcher and learns from the owner, not from a
// table lookup, that it must not write a result code over a live state machine
// [04 §3.3].
//
// It is called wherever this service binds a queue, and the session composition
// calls it for queues that reach the pump without passing through here. It is
// idempotent: the registration is a fixed function value per row.
func (s *Service) RegisterOrderHandlers(q *orders.Queue) {
	if s == nil || q == nil {
		return
	}
	s.ensureRegistrationRows()
	for _, id := range s.stepDrivenRows {
		q.SetOwnedHandler(id, s.boundConstructionWake)
	}
	// The two mobile-build rows use the same handler so their stop/cancel
	// notifications are delivered before their phase-1 movement wake. Their
	// approach parks on retail's `0xE0` gate, so the pump's satisfied set IS the
	// wake and arrives as the ordinary handler argument [04 §3.3][05 R-WORK-01
	// §13]. StepUnit still owns all continuing state-machine arms.
	for _, id := range s.mobileWakeRows {
		q.SetOwnedHandler(id, s.boundConstructionWake)
	}
}

// constructionWakeVisit receives an ordinary pump delivery for a row whose
// state machine StepUnit otherwise advances. The pump has already consumed the
// delivered bits from the record and unit words, so construction must act on
// this argument while it is valid. In particular, a product-removal notice
// reaches the stopped-build arm here rather than being discarded before
// StepUnit can inspect Pending [04 §3.3][04 R-ORD-01 §6][05 C22].
//
// The handler does not apply a result code for the continuing arms: StepUnit
// remains the owner of their phase, gate and deadline. Mobile-build phase 1
// retains its movement-wake body. Reclaim forwards an early delivery onto its
// exact record and applies result codes only inside StepUnit's work window.
func (s *Service) constructionWakeVisit(builder *units.Unit, node *orders.Node, satisfied uint32, tick uint32) (orders.Code, bool) {
	if s == nil || builder == nil || node == nil {
		return 0, false
	}
	if isReclaimUnitNode(node) {
		if s.reclaimStepNode != node {
			// The ordinary pump consumed these bits before calling us. Forward
			// that exact delivery on this record until StepUnit enters its work
			// window; no event or deadline is synthesized [04 §3.3].
			node.Satisfied |= satisfied
			node.DynamicGate |= satisfied
			return 0, false
		}
		return s.unitReclaimVisit(builder, node, satisfied, tick), true
	}
	if isMobileBuild(node.ID) {
		if code, handled := s.mobileBuildInterrupt(builder, node, satisfied); handled {
			return code, true
		}
	}
	if node.ID == vtolMobileBuildRow {
		if s.vtolBuildStepOwner != builder {
			// Preserve the actual earlier delivery until the unit's construction
			// window, without inventing a second phase or movement wake.
			node.Satisfied |= satisfied
			node.DynamicGate |= satisfied
			return 0, false
		}
		return s.vtolBuildVisit(builder, node, satisfied, tick)
	}
	if isBuildOrderID(node.ID) {
		if satisfied&InterruptCancel != 0 {
			s.handleCancelCurrent(builder, node, tick)
			// Cancel-current applies completion posture before its kind-9 packet,
			// but its dying product is not an ordinary completed product for the
			// session hook. This ordinary-pump arm must leave no completion token
			// for a later builder's StepUnit visit.
			s.completedInPump = 0
			return 0, false
		}
		if satisfied&InterruptStop != 0 {
			s.handleStop(builder, node, tick)
			return 0, false
		}
	}
	if isMobileBuild(node.ID) {
		return s.mobileBuildWakeVisit(builder, node, satisfied, tick)
	}
	return 0, false
}

// mobileBuildInterrupt keeps the ground and air mobile rows' terminal wakes
// separate from the factory refund/kill and counted restart bodies
// [04 R-ORD-01 §5][04 R-ORD-02 §2]. Their abandoned nanoframes remain for GetBuilt.
func (s *Service) mobileBuildInterrupt(builder *units.Unit, node *orders.Node, satisfied uint32) (orders.Code, bool) {
	code := orders.Code(5)
	if satisfied&InterruptCancel == 0 {
		if satisfied&InterruptStop == 0 {
			return 0, false
		}
		s.raiseStatus(builder, statusCant, "Construction terminated")
		code = 8
	}
	if s.OnRefresh != nil {
		s.OnRefresh(builder)
	}
	delete(s.builderLinks, node.Target)
	return code, true
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
	Builder   pool.Handle // builder that was stepped
	Product   pool.Handle // nanoframe/new unit handle, 0 if none
	DefKey    string      // canonical def key for the product
	Owner     uint8       // builder owner
	Completed bool        // true if a unit completed this tick (exactly once)
	State     State       // phase after step
	Err       error       // explicit error for descriptor mismatch or other failure
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

// nanoIsqrt is the floor of the square root of a non-negative value. Retail
// forms the three magnitudes of [05 R-WORK-01 §2] on the double-precision stack
// and truncates each toward zero; for an exact integer radicand the two agree,
// and the integer form keeps authoritative state out of floating point (I2).
//
// This is the classic bitwise digit-by-digit method (review finding R04): it
// starts from the highest power of four that fits in 63 bits and refines one
// base-4 digit of the result per iteration using only addition, subtraction
// and shifts, never a squaring multiply. That makes it exact and terminating
// for every 0 <= v <= math.MaxInt64 — unlike the previous doubling search,
// which grew a candidate `r` by repeated `r*r` until the square overflowed
// signed 64-bit and wrapped to a value the loop's own condition could never
// escape (reachable at v >= 2^62, i.e. a builder-to-site separation of about
// 32768 world units per axis; see isWithinNanoRange below).
func nanoIsqrt(v int64) int64 {
	if v <= 0 {
		return 0
	}
	// digit starts at the largest power of four not exceeding v; math.MaxInt64
	// is just under 2^63, so 2^62 (the largest power of two below 2^63 that is
	// also a power of four) is a safe, constant starting point.
	digit := int64(1) << 62
	for digit > v {
		digit >>= 2
	}
	root := int64(0)
	for digit != 0 {
		if v >= root+digit {
			v -= root + digit
			root = (root >> 1) + digit
		} else {
			root >>= 1
		}
		digit >>= 2
	}
	return root
}

// nanoRadicand forms `dx*dx + dz*dz` for isWithinNanoRange without risking a
// signed 64-bit wrap in the multiply or the add (review finding R04).
//
// Bound established for the caller: the largest map extent this research
// corpus documents is research/formats/ota.md's `size` example, `36 x 16` in
// 512-world-unit squares — 18432x8192 world units. A worst-case diagonal
// separation at that scale gives a radicand near 2.9e18: under 2^62
// (4.61e18) and an order of magnitude under math.MaxInt64 (9.22e18), so no
// known or documented retail/community map reaches the danger zone. But
// research/formats/tnt.md's Width/Height header fields are raw u32s, and
// neither the format nor internal/content/compile_map.go's loader enforces a
// maximum on them — so an unusually large or malformed map is not ruled out
// by the code itself. The sum first overflows once |dx| and |dz| both
// approach roughly 2^31 in 16.16 (about 32768 world units of separation on
// each axis) — the same separation at which isWithinNanoRange's own signed
// 16-bit high-word read already goes negative (see the comment below), so
// saturating here changes nothing for any map where that read still means
// anything, and only prevents a wrap for inputs already past it.
func nanoRadicand(dx, dz int64) int64 {
	sq := func(a int64) int64 {
		if a == math.MinInt64 {
			// -a would itself overflow (two's complement has no positive
			// counterpart for MinInt64); its magnitude already saturates.
			return math.MaxInt64
		}
		if a < 0 {
			a = -a
		}
		if a != 0 && a > math.MaxInt64/a {
			return math.MaxInt64
		}
		return a * a
	}
	dx2, dz2 := sq(dx), sq(dz)
	if dx2 > math.MaxInt64-dz2 {
		return math.MaxInt64
	}
	return dx2 + dz2
}

// nanoFootprintPad is one end's half-footprint diagonal in whole world units:
// `trunc(8 · hypot(footX, footZ))` [05 R-WORK-01 §2]. Eight is half of the
// sixteen world units a footprint cell spans, and `8·sqrt(n)` is `sqrt(64n)`.
// Both ends' pads subtract, because retail forms the target's with a NEGATIVE
// eight and then adds it.
func nanoFootprintPad(footX, footZ int32) int32 {
	r := int64(footX)*int64(footX) + int64(footZ)*int64(footZ)
	return int32(nanoIsqrt(64 * r))
}

// isWithinNanoRange is the build-distance reach test of [05 R-WORK-01 §2],
// exactly:
//
//	distWorld  = (int16)(trunc(hypot(dx, dz)) >> 16)   // the signed high word
//	builderPad = trunc( 8 · hypot(builderFootX, builderFootZ))
//	targetPad  = trunc(-8 · hypot(targetFootX,  targetFootZ))
//	inRange    = (distWorld - builderPad + targetPad) <= (uint16)builddistance
//
// It is two-dimensional in X and Z and ignores Y entirely; the distance is
// CENTRE to CENTRE — the builder's origin to the site's snapped footprint
// centre — with each end's half-footprint diagonal taken off. The target end's
// footprint pair is passed by value so a construction SITE can substitute the
// PRODUCT definition's footprint, which is what §2 says the mobile builder's
// approach does. The comparison is inclusive and SIGNED [05 R-WORK-01 §12]
// point 1: a builder standing inside the target's half-diagonal makes the left
// side negative and passes.
//
// CORRECTION (WU-19-94). This used to compare `builddistance` in 16.16 against
// the squared planar distance to the NEAREST POINT of the site's footprint
// rectangle, under an open-question marker that asked whether retail measured to the
// centre, the edge or the bounds, and whether it added a footprint radius term.
// [05 R-WORK-01 §12] retires both markers: "this retires a reach of
// `builddistance` in 16.16 compared against the nearest point of the site's
// footprint rectangle: retail's test is centre-to-centre with both
// half-diagonals subtracted". Point 3 of the same section retires the rest of
// the old marker's list — there is no nano piece, no piece height, no Y term
// and no model radius; the `(Xextent + Zextent)/3` radius belongs to unit
// reclaim's squared form alone.
//
// Kept from the previous correction, because it is still the contract: the
// reach never resolves a nano piece. `QueryNanoPiece` is presentation-side and
// runs strictly AFTER admitted work [R-P0-06 §2][R-P0-06 §4][R-P0-06 §6];
// calling it from this predicate spent two script calls per tick where the
// emitter spends one, and the stock two-emitter scripts alternate their piece
// per call, so only one of ARMACK's two nano guns ever sprayed.
//
// Where this runs is recorded at Service.needsApproach: §12 point 2 establishes
// that retail consults the expression only on the approach phase's
// arrival-failure wake.
func (s *Service) isWithinNanoRange(builder *units.Unit, siteX, siteZ numeric.Fixed, siteFootX, siteFootZ int32) bool {
	if builder == nil || builder.Def == nil {
		return true
	}
	if builder.Def.BuildDistance == 0 {
		return true // no authored reach term; the approach gate is not armed
	}
	if s == nil || s.Movement == nil {
		return true // no walk driver bound in this context; skip range gate for unit tests
	}
	dx := int64(builder.X) - int64(siteX)
	dz := int64(builder.Z) - int64(siteZ)
	distFixed := nanoIsqrt(nanoRadicand(dx, dz))
	// The high word is read as a signed 16-bit quantity out of a 32-bit
	// register, not as a shift of the whole value: a separation of 32768 world
	// units or more reads negative [05 R-WORK-01 §2].
	distWorld := int32(int16(uint32(distFixed) >> 16))
	builderPad := nanoFootprintPad(builder.Def.FootprintX, builder.Def.FootprintZ)
	targetPad := nanoFootprintPad(siteFootX, siteFootZ)
	return distWorld-builderPad-targetPad <= int32(uint16(builder.Def.BuildDistance))
}

// ensureWalk activates a walk toward the site through movement's current-head
// boundary [04 R-MOV-01 §3][04 R-PATH-01 §8]. ActivateMove owns exactly-once
// submission and restored-route adoption; repeated visits for the same node do
// not resubmit, preserving determinism I1 and RNG call order I4.
//
// The goal handed to path search is the RECTANGLE goal on the product
// footprint, never the footprint centre and never a hand-picked perimeter
// point [04 R-PATH-01 §13][04 R-PATH-01 §12]: the order's stored position stays
// the centre, but routing the builder there parks it inside its own site, where
// the null-self commit check can never accept the placement
// [05 "Silent blocked revalidation before allocation"]. The rectangle's border
// is the candidate set and the search picks from it. See approach.go.
func (s *Service) ensureWalk(builder *units.Unit, node *orders.Node) {
	if s == nil || s.Movement == nil || s.Movement.Scheduler == nil || builder == nil || node == nil {
		return
	}
	// Install the goal BEFORE the idempotency guards below. The payload is
	// derived state that no save box carries, so the first tick after a restore
	// must re-establish it even when the restored route is still active —
	// otherwise the mover would spend that route steering at the order's stored
	// position and walk into the site [04 §8.3][04 R-PATH-01 §13].
	s.installApproachGoal(builder, node)
	s.Movement.EnsureUnit(builder)
	s.Movement.ActivateMove(builder, node)
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
	// A construction aircraft has neither term. `VTOL_MobileBuild` phase 1
	// installs a POINT marker at the site with horizontal arrival radius
	// `builddistance` and the work body then orbits; the row ends "there is no
	// nanolathe-active stamp and no reach test after arrival: an aircraft that
	// reached its builddistance marker builds from wherever the 150-tick orbit
	// leaves it" [04 R-ORD-02 §2]. Both the reach term and the clear-the-site
	// term belong to the ground twin's phase-0 RECTANGLE goal on the product
	// footprint [04 R-ORD-01 §5], and an aircraft installs no such goal.
	//
	// needsApproach already returned false for `canfly`, but mustClearSite did
	// not, so an aircraft hovering over the site it was told to build — which is
	// exactly where its own arrival marker puts it, a cruise altitude above the
	// footprint — answered yes here. The session's walk arm then emitted status
	// 7 `I can't reach the construction site` and abandoned the record, which is
	// why a construction aircraft could not build on open flat ground.
	if builder.Def != nil && builder.Def.CanFly {
		return false
	}
	// A ground builder standing inside its own site walks out of it, exactly as
	// retail's phase-0 rectangle goal on the product footprint requires
	// [R-ORD-01 §5][04 §7.2]. The commit does not share this term — see
	// needsApproach and mustClearSite (approach.go).
	return s.needsApproach(builder, node) || s.mustClearSite(builder, node)
}

// EnsureWalkPublic is the exported walk submission for session integration [04 §7.3].
func (s *Service) EnsureWalkPublic(builder *units.Unit, node *orders.Node) {
	s.ensureWalk(builder, node)
}

// IsWithinNanoRangePublic is the exported range check for session integration.
// The site's own footprint pair is part of the test [05 R-WORK-01 §2], so
// callers pass it alongside the centre.
func (s *Service) IsWithinNanoRangePublic(builder *units.Unit, siteX, siteZ numeric.Fixed, siteFootX, siteFootZ int32) bool {
	return s.isWithinNanoRange(builder, siteX, siteZ, siteFootX, siteFootZ)
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
	s.placements = make(map[pool.Handle]placementRecord)
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
	// Retain a diagnostic and nothing else: do not invent a cancellation or
	// retry transition [04 §6.4].
	//
	// REWRITTEN (WU-19-166). The marker here read "Malformed nodes are outside
	// the established state-2 path: queue admission rejects them before they
	// can reach this handler", and asked for the retail response when a
	// malformed node bypasses queue preflight. The premise was false in both
	// halves. Nothing is bypassing a boundary: this function's callers are
	// ordinary content-integrity failures that a real catalog can produce — no
	// definition for the node's product, a definition whose placement profile
	// does not resolve, a non-positive or unbuildable footprint, and a factory
	// whose current model resolves no exit piece. And retail has no
	// corresponding arm to copy, because it has no corresponding step: the
	// record carries a product definition INDEX ([04 R-ORD-01 §5], "p1 =
	// product definition index") and the `MobileBuild` and `BuildingBuild`
	// bodies index the definition table with it. The only handler-level refusals
	// those rows describe are the placement-illegal retry (deadline 15, or the
	// blocked-area budget of [R-ORDER-02 §1]) and the allocation refusal
	// (`Unable to create any more units`, deadline 300) — neither is this case.
	//
	// Closed (RWU-19-197, [04 R-ORD-01 §18]): neither handler bounds-checks
	// the index. The definition is formed as table base + index × record size
	// and used at once, so the load cannot yield nothing; the only "nothing"
	// the rows handle is the CREATOR's null (per-type limit reached, or no free
	// slot), which is the `Unable to create any more units` arm — deadline
	// 300, hold. A missing or unresolvable definition is therefore a guard
	// retail lacks, with no retail caption or queue transition to borrow: the
	// diagnostic-only retention is the whole of it and must not grow either.
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

// Status kinds the build rows this service owns emit [04 R-ORD-01 §1]'s table.
const (
	statusCant     uint8 = 7 // `cant` — every construction rejection
	statusComplete uint8 = 8 // `unitcomplete`
	statusBuild    uint8 = 9 // `build`
)

// raiseStatus is the shared status emitter of [04 R-ORD-01 §1], reached from
// the `BuildingBuild` and `MobileBuild` handler bodies. Those two rows live in
// this package rather than in internal/orders [04 R-ORD-01 §5], so their status
// sites raise from here through the order queue's presentation adapter — the
// same seam orders.NotifyStatus gives every other handler.
//
// It is presentation: the emitter's own gate (local player, live unit) is
// applied by the adapter, the request is staged as a committed-frame event, and
// nothing here reads or writes simulation state or either RNG stream
// [03 §8.3][I4][I6]. logMessage beside these calls stays what it is — a
// construction-lifecycle diagnostic, not the player-facing status line.
func (s *Service) raiseStatus(u *units.Unit, kind uint8, text string) {
	if s == nil || u == nil {
		return
	}
	orders.NotifyStatus(u, kind, text)
}

// LastKill returns the most recent kind-9 termination packet [05 C21].
func (s *Service) LastKill() KillInfo { return s.lastKill }

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

// NotifyProductRemoved delivers the target-removed notice for a unit that has
// just been destroyed: when the removed unit is the product some factory record
// still holds as its target reference, the owning builder's pending word takes
// bit 3 — `InterruptStop` — and the reference is unlinked, in that order
// [04 R-ORD-01 §6]. The next pump visit tests the interrupt before the state
// machine and runs the established construction-stopped body: the verbatim
// "Construction stopped", one count decrement, an interface refresh, and the
// node surviving at state 0 [05 C22].
//
// This helper uses the derived product→producer index to find the actual
// order reference. Completion, cancel-current and the never-existed unwind
// remove the index entry. The session also delivers the generic order-removal
// walk, so notification does not depend on this index [04 R-ORD-01 §6].
//
// It reports whether a notice was delivered.
func (s *Service) NotifyProductRemoved(product pool.Handle) bool {
	if s == nil || product == 0 || s.builderLinks == nil {
		return false
	}
	builderHandle, ok := s.builderLinks[product]
	if !ok || builderHandle == 0 || s.World == nil {
		return false
	}
	builder := s.World.Unit(builderHandle)
	if builder == nil {
		return false
	}
	node := s.recordTargeting(builder, product)
	if node == nil {
		return false
	}
	node.Satisfied |= InterruptStop // the notice belongs to this observing record [04 R-ORD-01 §6]
	node.BindTarget(0)              // "then unlinks the reference"
	return true
}

// recordTargeting returns the builder's construction record that currently
// holds handle as its target reference, or nil. Both segments are walked in
// order (I1); only construction records can carry a product reference, so a
// same-handle attack or guard record in the queue is not a false positive.
func (s *Service) recordTargeting(builder *units.Unit, handle pool.Handle) *orders.Node {
	q := s.queueForUnit(builder)
	if q == nil {
		return nil
	}
	for _, segment := range [][]*orders.Node{q.Primary(), q.Secondary()} {
		for _, n := range segment {
			if n == nil || n.Target != handle {
				continue
			}
			if !isBuildOrderID(n.ID) {
				continue
			}
			return n
		}
	}
	return nil
}

// DeliverCancelNotice is the construction handler's receiver for the cleanup
// cancel notification of [04 R-ORDER-02 §2]: "when the record's dynamic gate
// mask — the same field the pump consumes — still holds bit 1 (value 2) at
// removal, invoke the operation handler with that cancel-notification mask".
// The guard is the caller's (orders' cleanup runs it for every removal path);
// this is the body, which is the same cancel-current body the interrupt test
// reaches — refund `trunc((1 - remaining) * metalBuildCost)` and the cause-9
// kill [05 C21].
//
// Re-entry is the one thing the receiver has to get right. Cancel-current's own
// epilogue removes the head node, which re-enters cleanup; the body therefore
// releases the record's gate and its product reference BEFORE that removal, so
// the second pass sees a record no longer waiting on bit 1 and returns.
func (s *Service) DeliverCancelNotice(owner *units.Unit, node *orders.Node, tick uint32) bool {
	if s == nil || owner == nil || node == nil {
		return false
	}
	if node.DynamicGate&InterruptCancel == 0 {
		return false // the cancel-notification guard [04 R-ORDER-02 §2]
	}
	if !isBuildOrderID(node.ID) {
		return false
	}
	if isMobileBuild(node.ID) {
		_, handled := s.mobileBuildInterrupt(owner, node, InterruptCancel)
		return handled
	}
	s.handleCancelCurrent(owner, node, tick)
	return true
}

// The rows the two per-unit predicates below test, resolved once.
// orders.Lookup is a case-insensitive binary search over the descriptor
// table's names — the right shape for the interface's spelling-tolerant
// transmission, the wrong shape for a predicate called once per unit per tick.
// The table is immutable after package initialization, so each answer is a
// constant; Go initializes an imported package before the importing package's
// variables, so these resolve after orders.buildTable has run.
//
// The two predicates together were resolving up to eight names per unit per
// tick and measured 7.3% of authoritative tick time
// (docs/SIM_BENCHMARK.md).
var (
	factoryBuildRow    = orders.Lookup(FactoryBuildOrder)
	mobileBuildRow     = orders.Lookup(MobileBuildOrder)
	vtolMobileBuildRow = orders.Lookup(VTOLMobileBuildOrder)
	getBuiltRowID      = orders.Lookup(GetBuiltOrder)
	// The standing/auto and rally rows of isStandingOpID, in its own test
	// order. A zero id means the table names no such row, which the original
	// spelled as an explicit `!= 0` guard on every arm; a zero row can never
	// equal a live node's id, so the guard is preserved by construction.
	standingOpRows = [...]orders.ID{
		orders.Lookup("BeCarried"),
		getBuiltRowID,
		orders.Lookup("Park"),
		orders.Lookup("QMove"),
		orders.Lookup("QPatrol"),
	}
)

// isBuildOrderID reports whether id is one of the three rows this service
// drives: the factory row and the two mobile ones [04 §3.1].
func isBuildOrderID(id orders.ID) bool {
	if id == factoryBuildRow {
		return true
	}
	if id == mobileBuildRow || id == vtolMobileBuildRow {
		return true
	}
	// Also treat generic build via BuildingBuild fallback (already FactoryBuildOrder) – no other IDs are construction builds.
	return false
}

// Pump advances one builder's front record through the factory production
// machine, interrupts first [05 "Factory production lifecycle"]
// [05 "Cancel-current and stop interrupts"]. The build rows live on the primary
// segment — bit 18 selects the rear segment and only `BuildWeapon` and
// `SelfDestruct` carry it [04 §3.1] — so this walk never looks at the rear one.
//
// StepUnit is the entry the session's unit phase calls, and it calls this.
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
	if head.ID == vtolMobileBuildRow {
		previous := s.vtolBuildStepOwner
		s.vtolBuildStepOwner = factory
		defer func() { s.vtolBuildStepOwner = previous }()
		orders.ContinuePrimaryWork(factory, head, tick)
		return
	}
	// Interrupt masks tested before state machine with cancel-current first [05].
	if isMobileBuild(head.ID) && factory.Pending&(InterruptCancel|InterruptStop) != 0 {
		satisfied := factory.Pending & (InterruptCancel | InterruptStop)
		factory.Pending &^= satisfied
		s.mobileBuildInterrupt(factory, head, satisfied)
		head.DynamicGate = 0
		s.removeHead(factory, head)
		return
	}
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
	if id == 0 {
		return false
	}
	for _, row := range standingOpRows {
		if id == row {
			return true
		}
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

// handleGetBuiltOrder is the construction-owned handler invoked only by the
// ordered primary queue walk [04 R-FAC-02 §4].
func (s *Service) handleGetBuiltOrder(product *units.Unit, node *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if s == nil || product == nil || node == nil {
		return 5
	}
	// The satisfied set is `(record pending | unit pending) & gate`, computed
	// and cleared out of both words by the pump before it dispatched this
	// record. With `GetBuilt`'s gate at `0x8001`, bit 15 in the set means some
	// builder ran a forward step on this product since the last dispatch
	// [04 R-ORD-01 §10][04 R-ORD-01 §11].
	worked := satisfied&pendingUnderConstructionWake != 0
	if product.Remaining > 0 {
		switch State(node.Phase) {
		case State0:
			// Phases 0 and 1 do not test the satisfied set — a raised bit merely
			// advances them early [04 R-ORD-01 §11].
			node.Phase = uint8(State1)
			node.DynamicGate = 0x8001
			node.Deadline = int32(tick + 300)
		case State1:
			node.Phase = uint8(State2)
			node.DynamicGate = 0x8001
			node.Deadline = int32(tick + getBuiltWorkedPeriod)
		case State2:
			// The `0x8000` arm: deadline 30, hold, no decay. The deadline-expiry
			// arm below (satisfied bit 0 alone) is reached only when a whole
			// deadline passes with no forward step on the product
			// [04 R-ORD-01 §11].
			//
			// Retired with the producer's closure: the local defer stamp this
			// arm used to test (a next-decay tick on the record, written by
			// every admitted step) reproduced the suppression with an
			// eleven-tick window and only for an admitted step. §11 gives the
			// window as the full 30-tick re-arm and the raise as unconditional
			// on the forward arm, so a refused admission and a sub-30
			// `workertime` defer too.
			if worked {
				node.DynamicGate = 0x8001
				node.Deadline = int32(tick + getBuiltWorkedPeriod)
				return 2
			}
			// The decay quantum is `−((float)(buildtime × 11) / buildcostenergy)`:
			// a 32-bit signed product converted to float, divided by the
			// single-precision cost, negated, and passed as float32. It is formed
			// here exactly as the wrapper forms it so that a zero
			// `buildcostenergy` reaches the step as −∞ (reverse arm, clamp to
			// 1.0, full metal refund, health floor 0, clamp-kill on this first
			// decay visit) and a zero `buildtime` with it as a NaN (both compares
			// unordered — the step writes nothing and raises no wake bit). A
			// `buildcostenergy > 0` guard around the decay is not retail
			// [05 R-WORK-01 §11].
			if product.Def != nil {
				quantum := -(float32(product.Def.BuildTime*11) / product.Def.BuildCostEnergy)
				// The decay is the self form: the frame is both builder and
				// target, so the refund lands in its own owner's bucket and the
				// clamp-kill names it as its own attacker [05 R-WORK-01 §1].
				s.sharedStep(product, product, quantum, tick)
			}
			node.Phase = uint8(State2)
			node.DynamicGate = 0x8001
			node.Deadline = int32(tick + getBuiltDecayPeriod)
		default:
			// Only states 0, 1, and 2 are established for GetBuilt
			// [04 R-P0-09]. Normalize malformed fixture/save state without
			// introducing another retail phase.
			node.Phase = uint8(State2)
			node.DynamicGate = 0x8001
			node.Deadline = int32(tick + getBuiltDecayPeriod)
		}
		return 2
	}
	// Completion is observed only on GetBuilt's own due visit. Rally/park is
	// appended behind this record; code 5 lets the pump unlink it and continue
	// in the same pass [04 R-FAC-02 §4].
	builderHandle := node.Target
	if builderHandle != 0 && s.World != nil {
		if builder := s.World.Unit(builderHandle); builder != nil {
			if s.OnRefresh != nil {
				s.OnRefresh(builder)
			}
			if product.Def != nil && product.Def.BMCode != 0 {
				s.rallyInheritance(builder, product, tick)
			}
		}
	}
	return 5
}

// killDecayedNanoframe sends the reverse arm's termination packet for a frame
// whose remaining fraction the decay has just clamped to one: a kind-9 packet
// with nominal damage exactly 30000 [05 "Reverse and deconstruction"][05 C21].
// A locally controlled lethal result takes the severity-zero, no-corpse,
// no-explosion path. The shared receiver may instead leave a veteran survivor
// or a remotely controlled zero-health unit, so this helper does not assume a
// death latch. It is the same packet
// cancel-current sends, minus cancel-current's refund and completion
// transition — the reverse arm has already paid its own metal back through
// ReverseRefund, and the completion transition runs only on a remaining
// fraction of zero, which this is the opposite of.
//
// Releasing the placement here is what unblocks whatever the frame was sitting
// on. The frame is not a completed building, so it keeps no reservation.
func (s *Service) killDecayedNanoframe(product *units.Unit, tick uint32) {
	if s == nil || product == nil || !product.Alive {
		return
	}
	s.lastKill = KillInfo{Damage: Kind9Damage, Severity: 0, NoCorpse: true}
	if s.World != nil && s.World.Unit(product.Handle) != nil {
		// Reverse construction is the self form: target is also the raw attacker.
		// The receiver owns health, provenance, reaction, and delayed death marking
		// [05 R-WORK-01 §1][06 §9.1][06 §9.2].
		s.Combat.AcceptDamage(s.World, tick, combat.DamageInput{
			Victim: product.Handle, Attacker: product.Handle, Nominal: Kind9Damage,
			Kind: Kind9Cause,
		})
	}
	// If the common intake latches death, the record stays Alive for the phase-2
	// finalizer, which performs OnDeath, pool free, and the live-unit decrement
	// [01 §4.4]. Survivors and remote-controller results retain their ordinary
	// combat state.
	s.ReleasePlacement(product.Handle)
	delete(s.builderLinks, product.Handle)
}

// StepUnit is this service's per-unit step: the one entry the session's unit
// phase calls for a builder. The pump dispatches their construction wake
// handler, while the state machine below owns every continuing record's phase,
// gate and deadline [04 §3.3][05 "Factory production lifecycle"].
//
// It reports what the visit did through WorkResult, which the session's
// diagnostics and the AI read; the authoritative effects are on the world.
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
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0}
	}
	prim := q.Primary()
	// A standalone GetBuilt head is advanced through the real queue pump. This
	// keeps unit-local construction stepping composable without the old
	// direct per-tick resolver; a preceding BeCarried record still exclusively
	// controls when the walk can reach GetBuilt [04 R-FAC-02 §4].
	if len(prim) > 0 && prim[0] != nil && prim[0].ID == getBuiltRowID {
		q.Pump(builder, tick)
		prim = q.Primary()
	}
	if len(prim) == 0 {
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0}
	}
	// Work discovery skips standing ops (GetBuilt pending resolution, Park)
	// so a completed factory keeps producing [RX-05][05 "Queue insertion"].
	head := firstWorkNode(prim)
	if head == nil {
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0}
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
		return WorkResult{Builder: handle, Product: head.Target, DefKey: head.BuildDefKey, Owner: builder.Owner, State: State(head.Phase)}
	}
	// Distinct descriptor check [P0-I05][04 §3.1]: factory BuildingBuild vs mobile MobileBuild/VTOL_MobileBuild.
	factoryID := factoryBuildRow
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
		return WorkResult{Builder: handle, Product: head.Target, DefKey: head.BuildDefKey, Owner: builder.Owner, State: State(head.Phase), Err: mismatch}
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
	s.completedInPump = 0
	s.Pump(builder, tick)
	completedInPump := s.completedInPump
	s.completedInPump = 0
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
			// Detect the stored remaining-fraction transition exactly once.
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
	// The completion transition is authoritative over every handle derived from
	// the post-pump head: a same-pass successor allocation has already replaced
	// the node's target [05 "Factory production lifecycle"].
	if completedInPump != 0 {
		completed = true
		productHandle = completedInPump
		if prod := w.Unit(completedInPump); prod != nil && prod.Def != nil {
			defKey = prod.Def.CanonicalKey
		}
	}
	if defKey == "" {
		defKey = beforeKey
	}
	return WorkResult{
		Builder:   handle,
		Product:   productHandle,
		DefKey:    defKey,
		Owner:     beforeOwner,
		Completed: completed,
		State:     afterState,
	}
}

// getBuiltDecayPeriod is the eleven-tick rearm of `GetBuilt`'s deadline-expiry
// arm — the visit that takes the decay [04 R-ORD-01 §11][04 R-FAC-02 §4].
const getBuiltDecayPeriod = 11

// getBuiltWorkedPeriod is the thirty-tick rearm the `0x8000` arm installs, and
// the arm phase 1 installs on its way into phase 2 [04 R-ORD-01 §11].
const getBuiltWorkedPeriod = 30

// pendingUnderConstructionWake is bit 15 of the unit's ORDER-EVENT WORD — "the
// under-construction wait" of [04 R-ORD-01 §0], consumed by `GetBuilt`'s gate
// `0x8001` [04 R-ORD-01 §11]. Retail's store is a byte store into the word's
// high byte; the word is one Go field here (I13), so the byte store is an OR of
// this mask.
const pendingUnderConstructionWake uint32 = 0x8000

// raiseUnderConstructionWake is the shared step's wake store: on every call
// whose quantum is not negative — an admitted step, a step the two-resource
// admission refuses, and a zero quantum alike — it ORs `0x8000` into the
// TARGET's pending word, before the zero-quantum test and before the admission
// [04 R-ORD-01 §11].
//
// What the bit buys is the whole of the decay contract: `GetBuilt` leaves its
// gate at `0x8001` after every arm, the primary pump's satisfied set is
// `(record pending | unit pending) & gate` [04 R-ORD-01 §10], so a raised bit
// dispatches the record before its deadline and the phase-2 body takes the
// `0x8000` arm — deadline 30, hold, no decay. A nanoframe therefore decays only
// after a whole deadline in which no builder's forward step touched it, and a
// builder stalled on metal keeps its own site alive by standing at it
// [04 R-ORD-01 §11]. This supersedes the local eleven-tick defer stamp that
// stood here while the producer was unlocated; that stamp reproduced the effect
// with the wrong window and only for a record this package could find.
//
// This store is the whole of the raise. WU-19-95 additionally mirrored the bit
// onto the `GetBuilt` record's scratch word because the bound handler's
// signature carried only the tick; `Queue.SetGetBuiltHandler` now takes the
// descriptor Handler shape, so the handler reads the pump's own satisfied set
// and the mirror is gone — one writer, one word, exactly as §11 describes.
func raiseUnderConstructionWake(product *units.Unit) {
	if product == nil {
		return
	}
	product.Pending |= pendingUnderConstructionWake
}
