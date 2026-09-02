// Package construction implements factory production lifecycle [PLAN_08 WU-08-5][05 "Factory production lifecycle"].
package construction

import (
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
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
	FlagCompleted     uint32 = 0x00002000 // completion marker in the instance flag word [R-P0-09]
	FlagStartBuilding uint32 = 1 << 2     // start-building edge [05]
)

// Damage constants [05 "Cancel-current and stop interrupts"] C21.
const (
	Kind9Damage int32 = 30000 // unscaled, scaling requires damage <30000 [05 C21]
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

// stampKind9Death writes the provenance pair the cause-9 packet carries,
// on both refund paths [06 §12.1].
//
// The two packets are not the same packet, and the closure of that question is
// [05 "Cancel-current and stop interrupts"]'s 2026-09-02 correction: the
// reverse arm's last line is `selfKill(target, target, 30000, kind 9)`
// [05 R-WORK-01 §1], while cancel-current sends `damage(attacker = the factory,
// victim = the product, 30000, kind 9, flag 0)`. Only the attacker HANDLE
// differs, and each call site passes its own; what this helper stamps is the
// side snapshot the ordinary intake stores beside the kind byte [06 §9.1] step
// 4, and a factory and its product always share an owner, so the side is the
// product's owner on both paths.
func stampKind9Death(product *units.Unit) {
	if product == nil {
		return
	}
	product.LastDamageCause = Kind9Cause
	product.LastDamageSide = product.Owner
}

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

	// Per-session state. None of this may live in a package-level var: it is
	// authoritative (BuilderLinks is C18's "register the builder link on the
	// product"), it has to survive save/load through one owner, and two worlds
	// in one process must not share it.
	builderLinks map[pool.Handle]pool.Handle // product -> builder [05 C18]
	// TODO(question): if an in-battle restore boundary is introduced, persist
	// placements together with the production node phase/count/target, unit
	// activation/building edges, COB sleep/wait threads, and piece interpolation
	// so an authored factory close resumes on the identical callback and tick.
	// The current codebase has no in-battle codec [I13]; do not invent a
	// factory-only format.
	placements    map[pool.Handle]placementRecord // product -> occupancy footprint and immutable definition
	productIndex  map[uint32]string               // product id -> catalog key, built once
	getBuiltLinks map[pool.Handle]pool.Handle     // product -> builder until GetBuilt consumes it [R-P0-09]
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
	// product silently never reached the session's completion hook — it got no
	// mover, so it never left the pad and never took a ground word there.
	completedInPump pool.Handle
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
		q.SetGetBuiltHandler(s.handleGetBuiltOrder)
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

// nanoIsqrt is the floor of the square root of a non-negative value. Retail
// forms the three magnitudes of [05 R-WORK-01 §2] on the double-precision stack
// and truncates each toward zero; for an exact integer radicand the two agree,
// and the integer form keeps authoritative state out of floating point (I2).
func nanoIsqrt(v int64) int64 {
	if v <= 0 {
		return 0
	}
	r := int64(1)
	for r*r <= v {
		r <<= 1
	}
	x := int64(0)
	for b := r; b > 0; b >>= 1 {
		t := x + b
		if t*t <= v {
			x = t
		}
	}
	return x
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
// rectangle, under a TODO(question) that asked whether retail measured to the
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
	distFixed := nanoIsqrt(dx*dx + dz*dz)
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
	s.getBuiltLinks = make(map[pool.Handle]pool.Handle)
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
	return r.rect, ok
}

func (s *Service) recordPlacement(product pool.Handle, def *content.UnitDef, rect world.FootprintRect) {
	if s.placements == nil {
		s.placements = make(map[pool.Handle]placementRecord)
	}
	s.placements[product] = placementRecord{rect: rect, def: def}
}

// reservePlacement commits the product's footprint after the canonical
// placement query has accepted it. Mobile products stamp the complete ground
// footprint. Building-class products stamp exactly the yard cells selected by
// their current port-18 state [04 R-COLL-01 §3–§4].
func (s *Service) reservePlacement(product pool.Handle, def *content.UnitDef, rect world.FootprintRect) error {
	if s == nil || s.Terrain == nil {
		return fmt.Errorf("construction: placement terrain unavailable")
	}
	if product == 0 || uint64(product) > uint64(^uint16(0)>>1) {
		return fmt.Errorf("construction: placement identity %d exceeds occupancy identity range", product)
	}
	id := int16(product)
	building := def != nil && !def.BMCode
	var yard []world.YardCell
	if building {
		var err error
		yard, err = buildingYard(def, rect)
		if err != nil {
			return err
		}
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				return fmt.Errorf("construction: placement cell %d,%d unavailable", x, z)
			}
			if building && !yard[int((z-rect.MinZ())*rect.Width()+(x-rect.MinX()))].TestsOccupancy() {
				continue // [04 §6.2] C10: bits 1-2 clear, no occupant test
			}
			// Ground word only [04 R-COLL-01 §2]: "the air word is never
			// consulted, so a landed or hovering airborne unit never blocks a
			// ground mover through this test". WU-19-20 gave mode-2 movers the
			// air word [04 R-COLL-01 §4]; testing it here would let an
			// aircraft parked over its own plant's exit refuse every later
			// product.
			if cell.OccupantA() != 0 && cell.OccupantA() != id {
				return fmt.Errorf("construction: placement cell %d,%d occupied", x, z)
			}
		}
	}
	if building {
		open := false
		if s.World != nil {
			if u := s.World.Unit(product); u != nil {
				open = u.YardOpen
			}
		}
		s.stampBuilding(product, placementRecord{rect: rect, def: def}, open)
		return nil
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell.OccupantA() == 0 || cell.OccupantA() == id {
				cell.SetOccupantA(id)
			}
		}
	}
	return nil
}

// retirePlacement releases a completed mobile product. A completed building
// keeps its placement record and its canonical yard-selected ground stamp, so
// Terrain.CheckPlacement remains the single blocker source [04 R-COLL-01 §3].
func (s *Service) retirePlacement(product pool.Handle) {
	if s == nil || product == 0 {
		return
	}
	record, ok := s.placements[product]
	if !ok {
		return
	}
	if record.def == nil || record.def.BMCode {
		s.ReleasePlacement(product)
		return
	}
	open := false
	if s.World != nil {
		if u := s.World.Unit(product); u != nil {
			open = u.YardOpen
		}
	}
	s.stampBuilding(product, record, open)
}

func buildingYard(def *content.UnitDef, rect world.FootprintRect) ([]world.YardCell, error) {
	w, d := int(rect.Width()), int(rect.Depth())
	if w <= 0 || d <= 0 {
		return nil, fmt.Errorf("construction: invalid building footprint %dx%d", w, d)
	}
	if def == nil || def.BMCode {
		return nil, fmt.Errorf("construction: building yard unavailable")
	}
	yard, err := world.ParseYardMap(def.YardMap, w, d)
	if err != nil {
		return nil, err
	}
	if len(yard) != w*d {
		return nil, fmt.Errorf("construction: building yard length %d != footprint %d", len(yard), w*d)
	}
	return yard, nil
}

// stampBuilding normalizes one building to the exact ground cells selected by
// its current yard state. The global clear pass precedes the global stamp pass
// so an accepted yard transition preserves retail's clear-then-restamp order
// [04 R-COLL-01 §4].
//
// Retail writes one ground word per cell. Nanolathe splits that plane in two —
// the terrain plot cell read by the placement validator, and the movement
// occupancy grid read by the mover commit and the path search — so both must
// follow the yard state together. A building that released its `c`/`C` pad in
// the plot alone still held it in the grid, and the exit-spot query of
// [04 R-FAC-02 §5] (null self identity, so the producer's own stamp blocks it)
// rejected every product forever.
func (s *Service) stampBuilding(product pool.Handle, record placementRecord, open bool) {
	if s == nil || s.Terrain == nil || product == 0 || uint64(product) > uint64(^uint16(0)>>1) {
		return
	}
	yard, err := buildingYard(record.def, record.rect)
	if err != nil {
		return
	}
	id := int16(product)
	var grid *movement.OccupancyGrid
	if s.Movement != nil {
		grid = s.Movement.Grid // nil-safe: every OccupancyGrid method tolerates a nil receiver
	}
	gridID := int(product)
	if current, ok := s.placements[product]; ok {
		current.yardOpen = open
		s.placements[product] = current
	}
	// Keep movement's teardown state in lockstep with this accepted yard state.
	if s.Movement != nil {
		s.Movement.SetBuildingYardState(product, open)
	}
	// First release every self-owned cell no longer selected by the new state.
	for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
		for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				continue
			}
			y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
			if !y.Selects(open) {
				if cell.OccupantA() == id {
					cell.SetOccupantA(0)
				}
				// Clear only touches cells this identity holds, which is the
				// plot's self-owned test in the other layer [04 R-COLL-01 §4].
				grid.Clear(movement.Cell{X: x, Z: z}, 1, 1, gridID)
			}
		}
	}
	// Only after the complete clear pass, stamp every cell selected by the new
	// state and set the structure-yard mark on every yard-bit-0 cell.
	for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
		for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				continue
			}
			y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
			if y.Selects(open) {
				// The building class takes the same per-cell overlap protocol
				// as every mover stamp [04 R-COLL-01 §4]: the grid arbitrates
				// the cell as it is visited, raises the host/intruder bits on
				// both units, and writes the plot word itself when a terrain
				// is bound to it. When it is not (a grid-less or plot-less
				// fixture), the same arbitration decides the word here; the
				// verdict is deterministic, so asking twice cannot disagree
				// and the bit raises are idempotent.
				grid.Stamp(movement.Cell{X: x, Z: z}, 1, 1, gridID)
				if cell.OccupantA() != id && grid.ArbitrateOverlap(int(cell.OccupantA()), gridID) {
					cell.SetOccupantA(id)
				}
			}
			if y&0x01 != 0 {
				cell.SetStructureYard(true)
			}
		}
	}
	// The overlap protocol itself — host/intruder bits, the displacement of an
	// occupant whose owner is in the eliminated player state, and the clear's
	// overlap scan and restamp — now runs inside the occupancy layer for this
	// stamp exactly as it does for a mover [04 R-COLL-01 §4].
	//
	// TODO(question): what this write pair still does not do is the rest of
	// the section's stamp and clear order for the building class — the derived
	// min/max height recompute over the grown rectangle and the reclassifica-
	// tion of the rectangle in every active class layer. Both need shared APIs
	// this service does not own.
}

// RegisterBuildingPlacement records and stamps a building at the exact
// footprint derived by session composition before strict COB Create. This is
// the unit-creation stamp writer of [04 R-COLL-01 §4], not a reservation or a
// whole-rectangle approximation.
func (s *Service) RegisterBuildingPlacement(u *units.Unit) error {
	if s == nil || u == nil || u.Def == nil || u.Def.BMCode {
		return nil
	}
	extent, err := world.NewFootprintExtent(int32(u.Def.FootprintX), int32(u.Def.FootprintZ))
	if err != nil {
		return err
	}
	placement, err := world.SnapMobilePlacement(u.X, u.Y, u.Z, extent)
	if err != nil {
		return err
	}
	record := placementRecord{rect: placement.Rect(), def: u.Def}
	if _, err := buildingYard(record.def, record.rect); err != nil {
		return err
	}
	s.recordPlacement(u.Handle, u.Def, record.rect)
	s.stampBuilding(u.Handle, record, u.YardOpen)
	return nil
}

// YardOpenTransaction performs port 18's admission and accepted restamp as
// one ordered operation. It returns false on silent denial [04 §4.7 port 18]
// [04 R-COLL-01 §4][04 R-FAC-02 §5].
func (s *Service) YardOpenTransaction(u *units.Unit, requested bool) bool {
	if s == nil || s.Terrain == nil || u == nil || u.Handle == 0 || uint64(u.Handle) > uint64(^uint16(0)>>1) {
		return false
	}
	record, ok := s.placements[u.Handle]
	if !ok {
		// TODO(question): if a port-18 write can precede the unit-creation
		// placement record, establish whether retail preserves the requested
		// bit for its later initial stamp. Decider: trace creation's cached-pair
		// write versus synchronous COB Create. Fail closed meanwhile [04 §4.7].
		return false
	}
	yard, err := buildingYard(record.def, record.rect)
	if err != nil {
		return false
	}
	// The admission bounds are the mobile validator's exact building bounds:
	// positive cached pair and the final map row/column excluded [04 R-COLL-01
	// §2][04 R-FAC-02 §5].
	if record.rect.MinX() <= 0 || record.rect.MinZ() <= 0 ||
		record.rect.MaxX() >= s.Terrain.CellW || record.rect.MaxZ() >= s.Terrain.CellH {
		return false
	}
	id := int16(u.Handle)
	// Retail reads one ground word per cell here, so a factory cannot close its
	// yard while a released product still stands on a `c`/`C` cell, and cannot
	// open it while a foreign unit stands on an `O` cell [04 R-FAC-02 §5].
	// Nanolathe splits that plane in two — the terrain plot cell and the
	// movement occupancy grid — and stampBuilding already writes both. The
	// admission test has to read both for the same reason: construction's plot
	// stamp is released when a mobile product completes, after which the grid is
	// the only layer still holding the pad, so a plot-only test admitted the
	// close with the product still standing in the yard and closed the doors on
	// it. Whether the script retries the refused write is authored behavior
	// [04 R-FAC-02 §5].
	var grid *movement.OccupancyGrid
	if s.Movement != nil {
		grid = s.Movement.Grid // nil-safe: every OccupancyGrid method tolerates a nil receiver
	}
	gridID := int(u.Handle)
	for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
		for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
			y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
			checked := y&0x04 != 0
			if requested {
				checked = y&(0x02|0x08) != 0
			}
			if !checked {
				continue
			}
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil || (cell.OccupantA() != 0 && cell.OccupantA() != id) {
				return false
			}
			if occ, held := grid.OccupantAt(movement.Cell{X: x, Z: z}); held && occ != 0 && occ != gridID {
				return false
			}
		}
	}

	// The authoritative bit commits before the clear/stamp pass [04 R-COLL-01 §4].
	u.YardOpen = requested
	s.stampBuilding(u.Handle, record, requested)
	return true
}

func (s *Service) releaseFrameStamps(product pool.Handle) bool {
	if s == nil || s.placements == nil {
		return false
	}
	record, ok := s.placements[product]
	if !ok {
		return false
	}
	if s.Terrain != nil && uint64(product) <= uint64(^uint16(0)>>1) {
		id := int16(product)
		// The leaving identity releases both halves of Nanolathe's split ground
		// plane, exactly as it took them in stampBuilding [04 R-COLL-01 §4].
		var grid *movement.OccupancyGrid
		if s.Movement != nil {
			grid = s.Movement.Grid // nil-safe: every OccupancyGrid method tolerates a nil receiver
		}
		gridID := int(product)
		var yard []world.YardCell
		if record.def != nil && !record.def.BMCode {
			yard, _ = buildingYard(record.def, record.rect)
		}
		for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
			for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
				cell := s.Terrain.PlotAt(x, z)
				if cell == nil {
					continue
				}
				if len(yard) != 0 {
					y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
					if !y.Selects(record.yardOpen) {
						if y&0x01 != 0 {
							cell.SetStructureYard(false)
						}
						continue
					}
				}
				if cell.OccupantA() == id {
					cell.SetOccupantA(0)
				}
				grid.Clear(movement.Cell{X: x, Z: z}, 1, 1, gridID)
				if len(yard) != 0 {
					y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
					if y&0x01 != 0 {
						cell.SetStructureYard(false)
					}
				}
			}
		}
	}
	delete(s.placements, product)
	return true
}

// ReleasePlacement clears only ground words equal to the leaving identity,
// clears its structure-yard marks, and deletes its placement record. The
// death/teardown observer calls this exactly once [04 R-COLL-01 §4][R-P0-09].
func (s *Service) ReleasePlacement(product pool.Handle) bool {
	return s.releaseFrameStamps(product)
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
	// The definition word is read unsigned before the integer division [05
	// "Construction arithmetic"].
	return int32(uint16(workerTime) / 30)
}

// RemainingStep computes new remaining fraction clamp(old - worker/buildTime,0,1) [05 "Construction arithmetic"].
func RemainingStep(old float32, worker int32, buildTime int32) float32 {
	if buildTime <= 0 {
		// A guard that returns 0 for `buildtime = 0` is retail-exact: the
		// division is `+∞`, `old − ∞` is `−∞`, and the clamp's first test stores
		// `0.0f` — the whole remaining cost is demanded in one admission and the
		// product completes on that call [05 R-WORK-01 §11]. A NEGATIVE
		// `buildtime` instead inverts the step in retail (the fraction rises and
		// pins at 1.0, a frame that never completes); that case is malformed,
		// unshipped, and deliberately not reproduced by this `<= 0` guard.
		return 0
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
	return wideConstructionStep(old, worker, buildTime, maxDamage, energyCost, metalCost)
}

func wideConstructionStep(old float32, worker int32, buildTime int32, maxDamage int32, energyCost, metalCost int32) (float32, int32, float32, float32) {
	return wideConstructionStepQuantum(old, float32(worker), buildTime, maxDamage, energyCost, metalCost)
}

// wideConstructionStepQuantum is the same arithmetic with the quantum in its
// retail type. Every ordinary caller's quantum is an integer converted to
// float, but the decay wrapper's is not, and its infinities and NaNs have to
// survive the division and the clamp exactly as x87 leaves them
// [05 R-WORK-01 §1][05 R-WORK-01 §11].
func wideConstructionStepQuantum(old float32, quantum float32, buildTime int32, maxDamage int32, energyCost, metalCost int32) (float32, int32, float32, float32) {
	old80 := float64(old)
	new80 := old80 - float64(quantum)/float64(buildTime)
	if new80 <= 0 {
		new80 = 0
	}
	if new80 >= 1 {
		new80 = 1
	}
	newStored := float32(new80)
	delta32 := float32(old80 - float64(newStored))
	energy := float32(float64(energyCost) * float64(delta32))
	metal := float32(float64(metalCost) * float64(delta32))
	max80 := float64(uint32(maxDamage))
	healthGain := int32(max80*old80) - int32(max80*float64(newStored))
	return newStored, healthGain, energy, metal
}

func repairTerms(maxDamage, energyCost, worker, buildTime int32) (int32, int32) {
	if buildTime == 0 {
		return 0, 0
	}
	heal := int32(1 + (float64(maxDamage)*float64(worker)-1)/float64(buildTime))
	energy := int32(1 + (float64(energyCost)*float64(worker)-1)/float64(buildTime))
	if heal >= 1 {
		heal = 1
	}
	if energy >= 1 {
		energy = 1
	}
	return heal, energy
}

// Assist applies one ordinary construction work step to a live nanoframe.
// It is the session-bound entry point for HelpBuild: admission, progress,
// health, and decay deferral remain owned here just as they are for factory
// products [05 R-WORK-01 §1].
func (s *Service) Assist(builder, target *units.Unit, tick uint32) bool {
	if !s.applyWorkStep(builder, target, tick) {
		return false
	}
	if target.Remaining == 0 {
		// Assist is the owning boundary for a mobile helper's final increment.
		s.applyCompletionPosture(target)
	}
	return true
}

// applyWorkStep is the ordinary forward entry to the shared step: it derives
// the integer worker quantum from the builder's own definition and hands it to
// sharedStep as a float32 [05 "Construction arithmetic"][05 R-WORK-01 §1].
//
// The tick is no longer read: the decay suppression is the pending word's
// `0x8000`, not a tick stamp the step had to compute [04 R-ORD-01 §11]. It
// stays in the signature because the callers' own contract carries it.
func (s *Service) applyWorkStep(builder, target *units.Unit, _ uint32) bool {
	if s == nil || builder == nil || builder.Def == nil {
		return false
	}
	// `(uint16)workertime / 30` is an integer division performed BEFORE the
	// conversion to float [05 "Construction arithmetic"].
	return s.sharedStep(builder, target, float32(WorkerQuantum(builder.Def.WorkerTime)))
}

// sharedStep is the one construction helper every build, assist, factory-
// product and deconstruction step runs, written out in [05 R-WORK-01 §1]. It
// takes a builder, a target and a single-precision worker quantum and reports
// whether work was committed. Both arms live here because retail has one
// helper: the sign of the quantum picks the arm, and those same entry compares
// are what settle a malformed definition [05 R-WORK-01 §11].
//
// The quantum is float32 because retail's is: the wrapper that forms the decay
// quantum divides by a single-precision cost, so a zero `buildcostenergy`
// reaches the compares as −∞ and a zero `buildtime` with it as a NaN. Both are
// x87 compares, and an unordered operand reads as negative to the first and as
// equal to zero to the second, which is why a NaN quantum writes nothing and
// raises no wake bit [05 R-WORK-01 §11]. Go's own `<` and `==` are false for a
// NaN, so the unordered arm is spelled out.
//
// I2 allows the float32: the row is "Construction remaining fraction and its
// proportional cost/health intermediates" [05 "Construction arithmetic"], and
// §11 establishes that the quantum itself is single precision in retail.
func (s *Service) sharedStep(builder, target *units.Unit, quantum float32) bool {
	if s == nil || target == nil || target.Def == nil {
		return false
	}
	// The exact float compare on the stored fraction [05 R-WORK-01 §1].
	if target.Remaining == 0 {
		return false
	}
	// `if (worker >= 0.0f) target.pendingWord |= 0x8000` — the wake store runs
	// before the zero-quantum test and before the admission, so a refused step
	// and a zero quantum both defer the decay [04 R-ORD-01 §11].
	unordered := quantum != quantum
	forward := quantum >= 0 && !unordered
	if forward {
		raiseUnderConstructionWake(target)
	}
	// `if (worker == 0.0f) return notCommitted`, the compare an unordered
	// quantum also takes [05 R-WORK-01 §11].
	if quantum == 0 || unordered {
		return false
	}
	def := target.Def
	old := target.Remaining
	newStored, healthGain, energy, metal := wideConstructionStepQuantum(old, quantum, def.BuildTime, def.MaxDamage, def.BuildCostEnergy, def.BuildCostMetal)
	if forward {
		if builder == nil || s.Economy == nil {
			return false
		}
		if !economy.AdmitTwoResource(s.Economy.UnitBuckets(builder.Handle), energy, metal) {
			return false
		}
		health := target.Health + healthGain
		// The maximum-health cap is an UNSIGNED comparison, so a health that
		// went negative is clamped up to maxdamage [05 R-WORK-01 §1].
		if uint32(health) >= uint32(def.MaxDamage) {
			health = def.MaxDamage
		}
		target.Health = int32(int16(health))
		target.Remaining = newStored
		return true
	}
	// Reverse arm: metal-only, credited direct to the TARGET's bucket through
	// the special-player selector, health floored at zero with no maxdamage cap,
	// and the clamp to 1.0 killing the frame [05 R-WORK-01 §1].
	refund := -metal
	var bucket *float32
	if s.Economy != nil {
		if buckets := s.Economy.UnitBuckets(target.Handle); buckets != nil {
			bucket = &buckets[economy.Metal].Production
		}
	}
	// The special-player selector credits 0.5 or 0.7 of the amount and any other
	// value the whole of it, tied to the TARGET's owner [05 R-WORK-01 §1]
	// [05 "Cancel-current and stop interrupts"]. With no ledger there is no
	// bucket to credit and the refund is simply not paid; it is never diverted
	// to another field.
	special := s.IsSpecialSecondState != nil && s.IsSpecialSecondState(target.Owner)
	if bucket != nil {
		ReverseRefund(bucket, refund, special, s.ModeSelector)
	}
	health := target.Health + healthGain
	if health < 1 {
		health = 0
	}
	target.Health = int32(int16(health))
	target.Remaining = newStored
	if newStored >= 1 {
		// `selfKill(target, target, 30000, kind 9)` — the reverse arm's own
		// last line, and the only thing that removes an abandoned frame
		// [05 R-WORK-01 §1][05 R-WORK-01 §9].
		s.killDecayedNanoframe(target)
	}
	return true
}

// Repair applies one accepted repair packet. The packet is formed at this
// service boundary so repair shares combat's kind-10 early-heal path [05
// R-WORK-01 §3][06 §9.1].
func (s *Service) Repair(builder, target *units.Unit, worker int32) bool {
	if s == nil || builder == nil || target == nil || target.Def == nil {
		return false
	}
	def := target.Def
	if def.MaxDamage <= int32(int16(target.Health)) {
		return false
	}
	heal, energy := repairTerms(def.MaxDamage, def.BuildCostEnergy, worker, def.BuildTime)
	if s.Economy == nil {
		return false
	}
	buckets := s.Economy.UnitBuckets(builder.Handle)
	admitted := buckets != nil && buckets[economy.Energy].Carry <= 0
	economy.AdmitOneResource(buckets, float32(energy))
	if !admitted {
		return false
	}
	if heal < 0 {
		heal = 0
	}
	if s.Combat == nil || s.World == nil {
		return false
	}
	return s.Combat.DispatchHealingPacket(s.World, combat.Packet{
		Victim: uint16(target.Handle), Attacker: uint16(builder.Handle),
		Amount: uint16(heal), Kind: combat.KindHeal,
	})
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
	_, position, ok := s.queryBuildPiecePosition(factory, m)
	return position, ok
}

// queryBuildPiecePosition performs the synchronous QueryBuildInfo once and
// retains both values state 2 consumes: the signed-byte cargo piece and its
// composed position [04 R-FAC-02 §1][04 R-REV-02].
func (s *Service) queryBuildPiecePosition(factory *units.Unit, m *model.Model) (int, world.ModelWorldPosition, bool) {
	if factory == nil || m == nil {
		return -1, world.ModelWorldPosition{}, false
	}
	// QueryBuildInfo is a synchronous mode-Q callback. Production uses the
	// strict binding bridge [R-P0-09][04 §5.3].
	pieceIdx := int32(-1)
	if binding := factory.COBBinding(); binding != nil && binding.Callbacks != nil {
		pieceIdx = binding.Callbacks.QueryBuildInfo().QueryValue()
	} else {
		return -1, world.ModelWorldPosition{}, false
	}
	if pieceIdx < 0 {
		return -1, world.ModelWorldPosition{}, false
	}
	modelPiece := pieceIdx
	if binding := factory.COBBinding(); binding != nil && int(pieceIdx) < len(binding.PieceMap) {
		modelPiece = int32(binding.PieceMap[pieceIdx])
	}
	if modelPiece < 0 || int(modelPiece) >= len(m.Pieces) {
		return -1, world.ModelWorldPosition{}, false
	}
	// 2. resolve piece transform plus factory origin to world position. Strict
	// bindings own PieceMap, hierarchy state, and unit orientation; construction
	// must not duplicate that composition [04 §4.1][03 §2.4].
	var pos [3]numeric.Fixed
	if binding := factory.COBBinding(); binding != nil {
		var composed bool
		pos, composed = binding.ComposePiece(int(pieceIdx), factory.Move.Heading, factory.Move.Pitch, factory.Move.Bank)
		if !composed {
			return -1, world.ModelWorldPosition{}, false
		}
	} else {
		return -1, world.ModelWorldPosition{}, false
	}
	// Composed coordinates are MODEL space, and model space is mirrored in Z
	// against world space: the projection narrows a model-relative vertex as
	// hi16(-vz) while a unit's own position enters the blit unnegated
	// [03 R-RAST-01 §2]. A consumer that adds a composed offset to a unit's
	// world position therefore owes the Z negation, which model.Transform's
	// note records and leaves to each call site.
	//
	// That note holds the heading-zero nose mapping as a supported inference
	// with a probe still pending, and asks not to flip a sign on the note
	// alone. This site is settled by authored data instead. The exit footprint
	// has to land on cells the yard releases when it opens — the 'c'/'C'
	// region, stamped only while closed [04 R-COLL-01 §4] — because the state-2
	// area test runs with a null self identity and any non-zero ground word
	// blocks it [04 R-FAC-02 §5]. Measured over the six stock factories at
	// their authored build angles: unnegated, ARMAP, CORVP and CORAP put the
	// exit footprint on always-stamped `o` cells, where no product could ever
	// validate; negated, all six land inside their own released corridor. Only
	// one sign choice lets the stock models and the stock yard maps agree.
	worldX := factory.X.Add(pos[0])
	worldY := factory.Y.Add(pos[1])
	worldZ := factory.Z.Sub(pos[2])
	return int(pieceIdx), world.NewModelWorldPosition(worldX, worldY, worldZ), true
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
// no engine-side piece alternation is permitted [R-P0-06 §2][04 §5.3]. Stock
// multi-emitter builders alternate their spray piece from inside the script —
// ARMAP returns beam1/beam2 and ARMACK rnanospray/lnanospray on successive
// calls — so the caller must issue exactly one query per accepted work step
// and never a speculative one: an extra call per tick rotates the script past
// the emitter the work step would have used and pins the spray to one piece
// [R-P0-06 §4][R-P0-06 §6].
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
	// Composed coordinates are MODEL space, and model space is mirrored in Z
	// against world space: the projection narrows a model-relative vertex as
	// hi16(-vz) while a unit's own position enters the blit unnegated
	// [03 R-RAST-01 §2]. A consumer that turns a composed offset into a world
	// point therefore owes the Z negation, exactly as the build-plate query
	// above already does.
	//
	// This site previously added the composed Z. That mirrored the emitter
	// about the builder's own centre, and because the screen ordinate is
	// `Z - Y/2`, a Z error of twice the piece's depth offset moves the spray
	// origin by that many whole pixels down the screen — for the Arm aircraft
	// plant's beam pieces roughly seventy, which is how a nano piece authored
	// on top of the building came out spraying from the ground. The negated
	// form is the only one that puts the origin where the model pass actually
	// draws that piece: the model path composes the same offset and emits it at
	// `hi16(-vz)` relative to the unit's blit anchor, and
	// `WorldToScreen(unit + (x, y, -z))` is precisely that pixel [03 §2.4]
	// [03 §2.5]. It also agrees with the build-plate sign that the stock yard
	// maps settled independently.
	return piece, world.NewModelWorldPosition(builder.X.Add(pos[0]), builder.Y.Add(pos[1]), builder.Z.Sub(pos[2])), true
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
		NanolatheActiveUntil:   tick + 300,
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
	if product.Def != nil && product.Def.BMCode {
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

	// A mobile factory product is attached in the allocation visit. The shared
	// cargo representation is the only carried-state authority; structure-class
	// products remain standing at the allocated position [04 R-FAC-02 §1].
	if product.Def != nil && product.Def.BMCode {
		beCarriedID := orders.Lookup("BeCarried")
		if beCarriedID != 0 {
			pq := orders.BindQueueBinding(product, s.OrderBinding)
			pq.SetGetBuiltHandler(s.handleGetBuiltOrder)
			pq.Push(beCarriedID, productRecord(product, tick, orders.Node{Target: factory.Handle}))
		}
	}

	// Attach inserts BeCarried first; GetBuilt is queued behind it
	// [04 R-FAC-02 §1][04 R-FAC-02 §4].
	getBuiltID := orders.Lookup("GetBuilt")
	if getBuiltID != 0 {
		pq := orders.BindQueueBinding(product, s.OrderBinding)
		pq.SetGetBuiltHandler(s.handleGetBuiltOrder)
		// Queued mode, zero count per [05 C18]: Param2 zero count special? Queue treats 0 as 1? But we pass 0 and CoalesceTail will treat 0 as 1? However plan says zero count. We pass Node with Param2 0.
		pq.Push(getBuiltID, productRecord(product, tick, orders.Node{Param2: 0}))
		// Ensure product's queue head is GetBuilt with active marker.
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
// startBuilding issues the slot-form heading variant: the construction-command
// producer, which carries the PRODUCER'S OWN current heading as the script's
// first argument [04 §2.3b] rather than the relative bearing to a work target
// that the order-record emitter passes [04 R-CB-01 §3]. Those are two
// different arguments and this site keeps its own.
//
// The order-record emitter orders.EmitStartBuilding stays the only writer of
// the StopBuilding-pending flag here [R-ORDER-02 §2]. Note that
// [04 R-CB-01 §3] correction 2 withdrew the reading that made the slot form a
// separate function from the emission helper — retail's producer census finds
// one function, and its last act is to OR the pending flag into the order
// record. This helper has no order record to flag, so it cannot mirror that
// half yet.
// TODO(question): whether a Nanolathe construction-command start should route
// through the order record and set the pending flag, given [04 R-CB-01 §3]
// correction 2 collapses the two variants into one retail function.
func (s *Service) startBuilding(u *units.Unit) {
	if u == nil || u.Flags&FlagStartBuilding != 0 {
		return
	}
	u.Flags |= FlagStartBuilding
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		// The argument-less deferred edge form. Corrected (2026-09-02): this
		// called the argument-carrying variant with `Move.Heading & 0xffff`,
		// on §3.8's earlier "carries the producer heading". That sentence is
		// withdrawn — the argument-carrying form is the order-record emission
		// helper of the nine mobile work handlers and its one argument is the
		// relative bearing from builder to work target; no variant carries a
		// producer's own heading, and the factory uses only this edge form
		// [04 §3.8 correction 2026-09-02][04 R-CB-01 §3].
		binding.Callbacks.StartBuilding()
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

// standingMergeAdmits is the double guard both standing-field merges share:
// the copy is allowed only when BOTH units carry the state word's alive bit
// (bit 28) and NEITHER carries bit 14, the death latch the kill service sets
// beside the cause byte and the completion transition sets for an `isfeature`
// product [04 §3.8][04 R-SPEC-01 §12].
//
// Retail keeps both bits in one status word; Nanolathe keeps each as its own
// named field, which is what I13 requires — the alive bit is units.Unit.Alive
// ("slot valid; cleared by the phase-2 finalizer" [04 §2.4]) and the death
// latch is units.Unit.Dying, the mark World.Destroy sets. Reading them off the
// instance flag word instead, as this guard used to, tested two literals no
// live code path ever sets: the allocator's initial status word carries
// neither, so the state-2 merge below was a silent no-op in every battle.
func standingMergeAdmits(builder, product *units.Unit) bool {
	if builder == nil || product == nil {
		return false
	}
	return builder.Alive && product.Alive && !builder.Dying && !product.Dying
}

// copyStandingFlags is the recovered initial standing-field merge guard. The
// class and auto exclusions are distinct from the later rally traversal
// [R-P0-09]. This is the state-2 epilogue's copy — the initial product-state
// merge — and is a distinct stage from the post-build gate in
// inheritStandingFields; the product's initial flags are not proof that
// `GetBuilt` has run [04 §3.8].
func copyStandingFlags(builder, product *units.Unit) {
	if !standingMergeAdmits(builder, product) {
		return
	}
	product.Flags = (product.Flags &^ (StandingMoveMask | StandingFireMask)) |
		(builder.Flags & (StandingMoveMask | StandingFireMask))
}

func (s *Service) copyStandingFlags(builder, product *units.Unit) {
	copyStandingFlags(builder, product)
}

// controlByteComputer is the player slot's control byte for a computer player:
// `1` is a locally controlled human, `2` a computer player, `3` a remote peer
// [05 R-SHARE-01 §1].
//
// [04 §3.8] parenthesises the experience-word gate as "owner player state byte
// value 1". That parenthetical is the same mislabel [04 §3.6]'s 2026-08-31
// correction retired for the idle-queue refill — it read the pair {1,2} as two
// computer-player states — and three Established traces disagree with it:
// [05 R-SHARE-01 §1] (skirmish setup writes 1 for the human seat and 2 for each
// computer seat), [05 R-ECO-01 §3] (the difficulty discount runs for control
// byte 2), and [04 R-SPEC-01 §5] ("the searching unit's owning player has
// controller type 2 (a computer player)"). The gate is control byte 2.
const controlByteComputer uint8 = 2

// ownerControlByte reads the owning player row's control byte through the
// economy ledger, which is where the session writes it [05 R-SHARE-01 §1]. A
// row this service cannot see reads as 0 — not a control-byte value, so it
// never satisfies the computer-player gate.
func (s *Service) ownerControlByte(owner uint8) uint8 {
	if s == nil || s.Economy == nil || int(owner) >= len(s.Economy.Players) {
		return 0
	}
	return s.Economy.Players[owner].ControllerState
}

// inheritStandingFields is `GetBuilt`'s post-build standing merge, the second
// of the two stages [04 §3.8] keeps distinct. Under the same alive/death-latch
// guard as the state-2 copy it moves standing-move bits 18-19 and standing-fire
// bits 20-21 from builder to product, and the experience word rides the same
// guarded block under one further gate — the OWNER's control byte reading as a
// computer player [04 §3.8][04 R-FAC-02 §4].
//
// `units.Unit.Kills` is the experience word: it is the field the capture timer's
// divide-by-five reads and the field the account record saves [05 "Unit
// capture"][08 R-SAVE-02 §6]. A product and its builder always share an owner,
// so the control byte is read once, off the builder.
func (s *Service) inheritStandingFields(builder, product *units.Unit) {
	if !standingMergeAdmits(builder, product) {
		return
	}
	product.Flags = (product.Flags &^ (StandingMoveMask | StandingFireMask)) |
		(builder.Flags & (StandingMoveMask | StandingFireMask))
	if s.ownerControlByte(builder.Owner) == controlByteComputer {
		product.Kills = builder.Kills
	}
}

// OnRefresh is the interface refresh callback, set by tests.

// ---------------------------------------------------------------------------
// C19 Rally inheritance [05 "Rally inheritance"].
// ---------------------------------------------------------------------------

// rallyInheritance is `GetBuilt`'s completion arm: it walks the builder's
// primary queue and inserts the resolved rally records — or `Park` when there
// were none — QUEUED ON THE PRODUCT [04 R-FAC-02 §4]. Every record it makes is
// therefore the product's own, and is stamped through productRecord; tick is
// the GetBuilt visit's, which is when these records come into being
// [04 §3.2].
func (s *Service) rallyInheritance(factory *units.Unit, product *units.Unit, tick uint32) {
	if factory == nil || product == nil {
		return
	}
	fq := s.queueForUnit(factory)
	if fq == nil {
		// No queue => nothing to inherit, so the standing merge still runs and
		// then Park. Retail's builder always has a queue object; an empty walk
		// and a missing one reach the same two steps [04 R-FAC-02 §4].
		s.inheritStandingFields(factory, product)
		parkID := orders.Lookup("Park")
		if parkID != 0 {
			pq := orders.BindQueueBinding(product, s.OrderBinding)
			pq.Push(parkID, productRecord(product, tick, orders.Node{}))
		}
		return
	}
	prim := fq.Primary()
	qMoveID := orders.Lookup("QMove")
	qPatrolID := orders.Lookup("QPatrol")
	parkID := orders.Lookup("Park")
	// [04 R-FAC-02 §4]: "a record whose name is `QMove` is RESOLVED AS COMMAND
	// 2 (move) and one named `QPatrol` as command 9 (patrol) AGAINST THE
	// PRODUCT with the record's goal triple". The descriptor a rally record
	// becomes is therefore the product's own resolution, not a constant.
	//
	// Corrected 2026-09-02 (WU-19-107, playtest report 4: "planes are not
	// moving off the factory properly and are piling up, making it impossible
	// to build more until manually moving them"). This walk used to hard-code
	// `Move_Ground` and `Patrol`, so an aircraft product of a factory carrying
	// a rally point received the GROUND move handler. Nothing recovers from
	// that: an aircraft is never admitted to the ground path scheduler
	// ([04 R-PATH-01 §9]), so no movement bit ever satisfies the record's
	// `0xE0` gate and it stalls at the head forever; and because the record is
	// not an air one it never runs the takeoff preamble, so the product's mover
	// mode stays 1 and its stamp stays on the GROUND plane — which is exactly
	// the occupancy the next product's state-2 test and the yard-close
	// admission gate wait on ([04 R-AIR-02] step 3, [04 R-FAC-02 §5],
	// [04 R-FAC-02 §6]). The plant stops after its first product until the
	// player moves the aircraft by hand, which issues the same command 2 and
	// resolves the air executor the rally should have.
	//
	// Codes 2 and 9 read no position and no target ([04 R-ORD-02 §1]), so the
	// goal triple rides on the record rather than through the resolver.
	moveID := orders.Resolve(2, product, nil, nil)
	patrolID := orders.Resolve(9, product, nil, nil)

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
				rec := productRecord(product, tick, orders.Node{ID: moveID, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, DynamicGate: 0, Deadline: -1, StaticGate: orders.DescriptorFor(moveID).StaticGate, Flags: 0})
				nn := &rec
				// Ensure deadline -1 for new node [04 §3.2]
				if nn.Deadline == 0 {
					nn.Deadline = -1
				}
				toAppend = append(toAppend, nn)
				inherited++
			}
		} else if n.ID == qPatrolID {
			if patrolID != 0 {
				rec := productRecord(product, tick, orders.Node{ID: patrolID, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, DynamicGate: 0, Deadline: -1, StaticGate: orders.DescriptorFor(patrolID).StaticGate})
				nn := &rec
				if nn.Deadline == 0 {
					nn.Deadline = -1
				}
				toAppend = append(toAppend, nn)
				inherited++
			}
		}
	}
	// [04 R-FAC-02 §4] fixes the order inside the completion arm: the resolved
	// rally records are walked and inserted first, THEN the standing-bit copy
	// under the §3.8 guard (with the experience word for a computer-owned
	// builder), THEN `Park` if nothing was inserted.
	s.inheritStandingFields(factory, product)
	if inherited == 0 {
		if parkID != 0 {
			rec := productRecord(product, tick, orders.Node{ID: parkID, Deadline: -1, StaticGate: orders.DescriptorFor(parkID).StaticGate})
			nn := &rec
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
		pq.SetPrimary(newPrim)
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
				// The computer player's difficulty scaling. This site is one of
				// the fourteen members of that family, and every one of them
				// pairs the constants the same way: selector 0 credits a HALF,
				// selector 1 seven tenths, any other selector the whole amount
				// [05 R-ECO-01 §3][05 R-ECO-01 §11].
				//
				// Correction (PT3-05 follow-up). This arm used to read
				// `case 0: += refund * -0.7` and `case 1: += refund * -0.5`,
				// under a comment stating the pairing was inverted relative to
				// the ledger's negative-`energyuse` site and instructing that it
				// must not be harmonized. Both halves were wrong, and the
				// executable settles both: the pairing is uniform across all
				// fourteen sites, this one included, and the scaled arm is a
				// REDUCED CREDIT rather than a debit — the site forms
				// `accumulator - refund * (-0.5)`, which ADDS half the refund.
				// The old arm subtracted seven tenths of it, so cancelling a
				// build CHARGED a computer player metal where retail pays it
				// back at a discount, and charged it the wrong fraction. The
				// "do not harmonize" instruction is retired with the reading it
				// defended.
				//
				// The fraction is applied to the float32 refund rather than
				// through internal/economy's single-narrowing helper: retail
				// forms the product and the subtraction at working precision and
				// narrows once [05 R-ECO-01 §3], where this rounds the product
				// first. The refund is an integer-valued float32 (truncated
				// above), so the two agree at every stock magnitude; the
				// residual is the same class as the one locked in
				// internal/economy's reclaim-credit tests, and closing it means
				// moving this site onto that helper.
				switch s.ModeSelector {
				case 0:
					player.Mirror[economy.Metal].Production += refund * 0.5 // credit one half [05 R-ECO-01 §11]
				case 1:
					player.Mirror[economy.Metal].Production += refund * 0.7 // credit seven tenths [05 R-ECO-01 §11]
				default:
					player.Mirror[economy.Metal].Production += refund
				}
			} else {
				player.Mirror[economy.Metal].Production += refund
			}
		}
	}

	// Step 3, after the refund and before the kill: the SAME completion
	// transition every other completion runs — not a bare `remaining = 0`
	// [05 "Cancel-current and stop interrupts"][04 R-FAC-02 §3]. The builder is
	// the factory, so a product with `activatewhenbuilt` receives its `Activate`
	// edge here and dies in step 4 of the same call, and the order-panel refresh
	// the transition owes keys on the BUILDER's identity — this arm's own
	// `OnRefresh(factory)` below, never the product's [04 R-SPEC-01 §12].
	// The queued count is intentionally untouched [R-P0-09][05 C21].
	if product != nil {
		s.applyCompletionPosture(product)
	}

	// Send ordinary kill packet — kind-9 damage exactly 30000 unscaled because scaling requires damage <30000 [05 C21][06 §9.1].
	// Note cause-9 deaths skip killed-severity query entirely (severity zero, no explosion, no corpse) [04 §5.1][05 C21].
	s.lastKill = KillInfo{Damage: Kind9Damage, Severity: 0, NoCorpse: true} // severity zero [05 C21]
	if product != nil {
		// Apply death: Alive false, but no corpse/explosion.
		if s.World != nil {
			stampKind9Death(product)
			// Corrected (2026-09-02, this unit): the packet cancel-current sends
			// is `damage(attacker = the factory, victim = the product, 30000,
			// kind 9, flag 0)` — the FACTORY is the attacker. The self form
			// belongs to the shared step's reverse arm alone; same kind and
			// amount, not the same packet. Nothing on the credit side turns on
			// it (cause 9 has no credit branch), but the recorded-attacker link
			// and the death row hold the factory's identity for a cancelled
			// product [05 "Cancel-current and stop interrupts"][06 §12.1].
			s.World.DestroyBy(product.Handle, units.DeathKilled, factory.Handle)
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

	// Release the record's target reference and its dynamic gate before the
	// removal. The reference release is the "releases the reference at
	// completion or cancel" half of the target-removed binding
	// [05 "Build request and factory queue behavior"], and dropping gate bit 1
	// is what stops the removal below from re-delivering the cancel notice of
	// [04 R-ORDER-02 §2] into this same body: the guard is "the dynamic gate
	// still holds bit 1 AT REMOVAL", and by then this record is no longer
	// waiting on it.
	node.Target = 0
	node.DynamicGate = 0

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
	// Record the completion for StepUnit's caller; the transition is idempotent
	// and the second invocation names the same product [04 R-FAC-02 §3].
	s.completedInPump = product.Handle
	// Completed units become eligible for AI classification (group 4 construction) [R-P0-04][08].
	// Nanoframes are created with Flags without 0x20 (initializeNanoframe clears it); completion must restore it.
	product.Flags |= units.ClassifierEligibleStatus
	// Completion detaches through the shared cargo commit. Detach is idempotent
	// and performs no position write or re-stamp [04 R-FAC-02 §3].
	if product.Def != nil && product.Def.BMCode && product.Attachment.Carrier != 0 {
		movement.DetachCargo(s.World, product.Handle)
	}
	if product.Def != nil && product.Def.ActivateWhenBuilt {
		s.activate(product)
	}
	// Capability bit 24 is `isfeature`, and its completion arm marks the product
	// a feature stand-in: a DIRECT store of death-cause byte 7 plus the death
	// latch, not a damage packet [04 R-SPEC-01 §12][06 §12.1]. It runs here,
	// immediately after the `activatewhenbuilt` edge, which is where §12 places
	// it in the completion order.
	//
	// Correction (RWU-19-26): this arm read `init_cloaked` and wrote
	// `FlagInitCloak` plus the cloak-requested bit, on doc 04 §3.8's earlier
	// mislabel of bit 24 as "the cloak/initial-posture handling". Bit 24 is
	// `isfeature`; §3.8 is corrected in place. The completion transition never
	// reads `init_cloaked` and never writes either cloak bit — `init_cloaked` is
	// consumed once, by the unit constructor, which seeds the cloak-requested
	// bit [05 R-ECO-01 §9][03 R-VIS-01 §6][units.Unit.InitEconomyState].
	//
	// Cause 7's credit branch is none and its attacker is not a packet field, so
	// nothing writes LastDamageSide here; the finalizer's credit path does not
	// read it for this cause [06 §12.1]. Severity is forced zero and the corpse
	// nibble forced one, so the product becomes its authored Corpse.
	if product.Def != nil && product.Def.IsFeature && s.World != nil {
		product.LastDamageCause = uint8(combat.CauseFeatureConversion)
		s.World.Destroy(product.Handle, units.DeathKilled) // direct latch, null attacker [06 §12.1]
	}
	product.Health = product.MaxHealth
	// Completion releases mobile products but retains building-class products
	// on exactly the cells selected by the current yard state [04 R-COLL-01 §4].
	s.retirePlacement(product.Handle)
}

// removeHead removes the head node from factory's primary queue without
// decrement [05 C21]. Removal happens in place through the queue's own
// subtraction path so queue identity and every queue-owned service binding
// (the concrete binding and diagnostics) survive.
// The previous implementation rebuilt the segment into a fresh orders.Queue
// and rebound it, which dropped those hooks: after the first factory product
// completed, successor target orders lost target lookup and hostility, and
// secondary stockpile admission no longer saw the economy buckets.
//
// The tombstone bit is set on every freed record EXCEPT one that is the
// primary segment's front head at that moment, so "the front record of the
// primary queue is never tombstoned" [04 R-ORDER-02 §2]. Every removal this
// helper performs is a removal of that head — cancel-current's drop and
// `MobileBuild`'s phase-4 completion both act on the record the pump is
// dispatching — so the flag is false and RemovePrimaryNode's own index test
// keeps the exemption honest if a caller ever hands it a rear record.
func (s *Service) removeHead(factory *units.Unit, node *orders.Node) {
	q := s.queueForUnit(factory)
	if q == nil {
		return
	}
	q.RemovePrimaryNode(node, false)
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
		// The clear below is consequently dead on every path a factory reaches
		// here through. It was a workaround for a creation-time pinning in
		// units.InitEconomyState that set the bit true for any definition
		// authoring neither key and so swallowed this raise; that pinning has
		// been removed. The clear is retained only because
		// TestStateZeroActivateIsARealEdge constructs the pinned state by hand
		// and would fail without it. It is also a second writer of the
		// engine-state bit, which retail does not have — the edge machine is
		// the only writer [04 R-UNIT-06 §2]. Deleting both this clear and that
		// test's `factory.Activated = true` setup line is the follow-up.
		if !factory.InBuildStance {
			factory.Activated = false
		}
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
		// The primary pump consumes an advance result immediately. Once the
		// authored stance handshake is already high, state 2 therefore runs in
		// this same pass; this is also what permits a counted successor to be
		// allocated without an invented idle tick [04 §4.7][R-FAC-01R].
		s.handleState2(factory, node, tick)
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
		s.recordAdmission(tick, factory.Handle, def.UnitName, AdmissionBlockedTransiently, err)
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
	if isMobileBuilder(builder) && s.Movement != nil && builder.Def != nil && builder.Def.BuildDistance != 0 {
		if s.needsApproach(builder, node) {
			s.ensureWalk(builder, node)
			node.DynamicGate = WakeBit2
			node.Deadline = int32(tick + 1)
			node.MoveState = orders.MoveEnRoute
			return
		}
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
	s.successEpilogueMobile(builder, node, product, cell, tick)
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
		pq.Push(getBuiltID, productRecord(product, tick, orders.Node{Param2: 0}))
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
	if !s.applyWorkStep(factory, product, tick) {
		// Denied work leaves the remaining fraction untouched and retries on the
		// next tick [05 "Two-resource admission"].
		node.DynamicGate = WakeBit1 | WakeBit3
		node.Deadline = int32(tick + 1)
		return
	}
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
	if product.Remaining == 0 {
		// The shared work helper owns the first completion transition. It runs
		// synchronously when the admitted increment stores zero, before the
		// factory's building edge falls in state 4. State 4 deliberately repeats
		// this transition idempotently [04 §4.7][R-FAC-01R].
		s.applyCompletionPosture(product)
		node.Phase = uint8(State4)
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
	// State 3's zero-remaining helper has already completed the product (and may
	// have raised its Activate edge). State 4 now lowers the factory building
	// edge before repeating the product transition idempotently [R-FAC-01R].
	// The repeated transition observes that the first transition already
	// detached the mobile product and performs no position write
	// [04 R-FAC-02 §3].
	// Trigger BuildUnitType only on local 30-tick deadline [P0-14].
	// Interrupt masks 2/8 bodies known, producers TODO(T25) [P0-14].
	// Engine prints no text, lowers start-building edge, runs completion transition [05].
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
		node.Target = 0
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
		node.Target = 0
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

// NotifyProductRemoved delivers the target-removed notice for a unit that has
// just been destroyed: when the removed unit is the product some factory record
// still holds as its target reference, the owning builder's pending word takes
// bit 3 — `InterruptStop` — and the reference is unlinked, in that order
// [04 R-ORD-01 §6]. The next pump visit tests the interrupt before the state
// machine and runs the established construction-stopped body: the verbatim
// "Construction stopped", one count decrement, an interface refresh, and the
// node surviving at state 0 [05 C22].
//
// The reference this reads is `builderLinks`, the product→builder registration
// [05 C18] made in the same epilogue that writes `node.Target`. Its lifetime is
// the reference's lifetime: completion, cancel-current and the never-existed
// unwind all delete the entry before the product's death can reach here, which
// is the "releases the reference at completion or cancel" half of the contract
// and is what keeps those three paths silent.
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
	builder.Pending |= InterruptStop // the notice's event code IS a pending bit [04 R-ORD-01 §6]
	node.Target = 0                  // "then unlinks the reference"
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
	s.handleCancelCurrent(owner, node, tick)
	return true
}

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
	if bc := orders.Lookup("BeCarried"); bc != 0 && id == bc {
		return true
	}
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
				quantum := -(float32(product.Def.BuildTime*11) / float32(product.Def.BuildCostEnergy))
				// The decay is the self form: the frame is both builder and
				// target, so the refund lands in its own owner's bucket and the
				// clamp-kill names it as its own attacker [05 R-WORK-01 §1].
				s.sharedStep(product, product, quantum)
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
	builderHandle, ok := s.getBuiltLinks[product.Handle]
	if ok && builderHandle != 0 && s.World != nil {
		if builder := s.World.Unit(builderHandle); builder != nil {
			if s.OnRefresh != nil {
				s.OnRefresh(builder)
			}
			if product.Def != nil && product.Def.BMCode {
				s.rallyInheritance(builder, product, tick)
			}
		}
	}
	delete(s.getBuiltLinks, product.Handle)
	return 5
}

// killDecayedNanoframe sends the reverse arm's termination packet for a frame
// whose remaining fraction the decay has just clamped to one: kind-9 damage of
// exactly 30000, severity zero, no corpse and no explosion
// [05 "Reverse and deconstruction"][05 C21]. It is the same packet
// cancel-current sends, minus cancel-current's refund and completion
// transition — the reverse arm has already paid its own metal back through
// ReverseRefund, and the completion transition runs only on a remaining
// fraction of zero, which this is the opposite of.
//
// Releasing the placement here is what unblocks whatever the frame was sitting
// on. The frame is not a completed building, so it keeps no reservation.
func (s *Service) killDecayedNanoframe(product *units.Unit) {
	if s == nil || product == nil || !product.Alive {
		return
	}
	s.lastKill = KillInfo{Damage: Kind9Damage, Severity: 0, NoCorpse: true}
	if s.World != nil && s.World.Unit(product.Handle) != nil {
		stampKind9Death(product)
		s.World.DestroyBy(product.Handle, units.DeathKilled, product.Handle)
	}
	product.Alive = false
	s.ReleasePlacement(product.Handle)
	delete(s.builderLinks, product.Handle)
	delete(s.getBuiltLinks, product.Handle)
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
	// A standalone GetBuilt head is advanced through the real queue pump. This
	// keeps unit-local construction stepping composable without the old
	// direct per-tick resolver; a preceding BeCarried record still exclusively
	// controls when the walk can reach GetBuilt [04 R-FAC-02 §4].
	if len(prim) > 0 && prim[0] != nil && prim[0].ID == orders.Lookup("GetBuilt") {
		q.Pump(builder, tick)
		prim = q.Primary()
	}
	if len(prim) == 0 {
		return WorkResult{Builder: handle, Owner: builder.Owner, State: State0, Diagnostics: append([]string(nil), s.messages...)}
	}
	head := prim[0]
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
		Builder:     handle,
		Product:     productHandle,
		DefKey:      defKey,
		Owner:       beforeOwner,
		Completed:   completed,
		State:       afterState,
		Diagnostics: append([]string(nil), s.messages...),
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
