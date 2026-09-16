// Factory and mobile build requests, and the queue insertion and subtraction
// they go through [05 "Queue insertion"][05 "Queue subtraction"].

package construction

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Architectural: factory and mobile products live as TYPED PAYLOADS on orders.Node in the PRIMARY segment.
// This package does NOT define a second node or queue type. Construction calls phase 6's
// queue/coalesce/cancel API (Queue.Push/CoalesceTail/CancelTailMost) [PLAN_08 C15][GAP T3].
// The secondary segment is exclusively BuildWeapon/SelfDestruct [GAP T3] — factory/mobile products
// never use it.
//
// Payload encoding on orders.Node fields per [04 §3.2] build family meanings [P0-I05]:
//   BuildDefKey = canonical defKey (stable string for save/load remapping, replaces FNV-1a N04)
//   Owner     = owning unit (the producing factory or builder) [04 §3.2]; record
//   cleanup resolves its StopBuilding counterpart through it [R-ORDER-02 §2]
//   Param1 = stable catalog index (1-based, 0 sentinel) via Catalog.UnitDefIndex, never FNV hash [P0-I05][02 §5]
//   Param2 = remaining build count [05 "Build request and factory queue behavior"]
//   Param3 = blocked-area retry counter for mobile builds [04 §3.2]
//   GoalX/Z = site world anchor for mobile builds [P0-I05]; factory ignores Goal (exit spot via QueryBuildInfo)
//   ID     = BuildingBuild for factory [04 §3.1][GAP T3], MobileBuild/VTOL_MobileBuild for mobile [04 §3.1]
// Primary segment only; no second queue type exists.

// FactoryBuildOrder is the primary descriptor for factory products [04 §3.1][GAP T3].
// Retail dump shows only BuildWeapon (0xc0140) and SelfDestruct (0x40040) carry bit 0x40000
// (secondary selection), so factory/mobile products are primary. BuildingBuild is the canonical
// factory production descriptor.
const FactoryBuildOrder = "BuildingBuild"

// Mobile build descriptors [04 §3.1].
// GetBuiltOrder is the product-side lifecycle row of [04 R-FAC-02 §4].
const GetBuiltOrder = "GetBuilt"

// The two mobile build rows: a ground builder takes the first, a `canfly`
// builder the second [04 §3.1][04 R-ORD-01 §7].
const MobileBuildOrder = "MobileBuild"
const VTOLMobileBuildOrder = "VTOL_MobileBuild"

// ErrLimitMessage is the verbatim exhaustion message produced at nanoframe allocation
// when per-def limits or pool exhaustion refuses creation [05 "Unit creation and limits"].
const ErrLimitMessage = "Unable to create any more units"

// The command-boundary rejections. They are returned to the producer that
// asked for a build; none of them is a retail diagnostic.
var (
	ErrNilFactory             = errors.New("construction: nil factory")
	ErrEmptyDef               = errors.New("construction: empty defKey")
	ErrBadCount               = errors.New("construction: count must be > 0")
	ErrNoQueue                = errors.New("construction: no queue")
	ErrNoBuildOrder           = errors.New("construction: build order descriptor not found")
	ErrUnknownProduct         = errors.New("construction: product definition unavailable")
	ErrMissingMovementProfile = errors.New("construction: product movement profile unavailable")
	// The exhaustion sentinel carries retail's verbatim text
	// [05 "Unit creation and limits"][05 C18].
	//lint:ignore ST1005 retail text
	ErrLimit = errors.New(ErrLimitMessage)
)

// P0-I16: LimitChecker moved onto Service as authoritative session-owned hook.
// See Service.LimitChecker and Service.CheckLimit.

// ExhaustionError returns the verbatim limit exhaustion error [05 "Unit creation and limits"].
func ExhaustionError() error {
	//lint:ignore ST1005 retail text: `Unable to create any more units` is reproduced verbatim [05 C18].
	return errors.New(ErrLimitMessage)
}

// defIndex maps defKey to the stable catalog index the build record carries in
// its first parameter [P0-I05][02 §5]. A missing catalog or an unknown key
// keeps the catalog's zero reject sentinel — the same rule orders.NewNodeForOrder's
// payload constructors follow, and for the same reason: no runtime identity is
// invented for an unresolved definition.
//
// This used to hand out synthetic indices from a package-global counter and map
// (base 100000, guarded by a mutex) whenever the catalog could not answer. They
// were a second product identity: an index minted that way could never equal
// the catalog index a queued record actually carries, which is what the two
// cancel paths' index-then-key double pass existed to paper over. Every
// production caller has the session catalog, and the record's canonical
// BuildDefKey is the identity everything else compares.
func defIndex(cat *content.Catalog, defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	if ck == "" || cat == nil {
		return 0
	}
	if idx, ok := cat.UnitDefIndex(ck); ok {
		return idx
	}
	return 0
}

// buildOrderID resolves the primary build descriptor for factory products [04 §3.1][GAP T3].
// A missing descriptor is a missing descriptor: the caller returns
// ErrNoBuildOrder rather than substituting a different handler. `BuildingBuild`
// and `MobileBuild` are separate rows of the descriptor table with separate
// bodies [04 §3.1][04 R-ORD-01 §5], so a factory product driven by the mobile
// row would take the site-bound machine instead of the factory one.
func buildOrderID() orders.ID { return orders.Lookup(FactoryBuildOrder) }

// mobileBuildOrderID resolves the mobile build descriptor for the builder [04 §3.1][P0-I05].
// VTOL builders use VTOL_MobileBuild, others use MobileBuild [04 §3.4] code 14.
func mobileBuildOrderID(builder *units.Unit) orders.ID {
	if builder != nil && builder.Def != nil && builder.Def.CanFly {
		if id := orders.Lookup(VTOLMobileBuildOrder); id != 0 {
			return id
		}
	}
	return orders.Lookup(MobileBuildOrder)
}

// QueueFactoryBuild enqueues count copies of defKey on factory's PRIMARY queue [C15][C20][C23][P0-I05].
// Factory payload: catalog index in Param1, count in Param2, BuildDefKey canonical, Phase holds factory state [05].
// Distinct handler from mobile: uses BuildingBuild descriptor [P0-I05].
func QueueFactoryBuild(factory *units.Unit, defKey string, count int, cat *content.Catalog) error {
	if factory == nil {
		return ErrNilFactory
	}
	if strings.TrimSpace(defKey) == "" {
		return ErrEmptyDef
	}
	if count <= 0 {
		return ErrBadCount
	}
	ck := content.CanonicalKey(defKey)
	if err := validateFactoryProduct(cat, ck); err != nil {
		return err
	}
	pid := defIndex(cat, defKey)
	q := orders.QueueForUnit(factory)
	if q == nil {
		return ErrNoQueue
	}
	bid := buildOrderID()
	if bid == 0 {
		return ErrNoBuildOrder
	}
	// Positive insertion, once: CoalesceTail owns both halves of
	// [05 "Queue insertion"] — add to a matching tail, otherwise construct and
	// insert through the producer path. The three call sites this replaced
	// (matching tail, empty list, everything else) each reached a different
	// helper, and the empty-list one reached a private tail append.
	q.CoalesceTail(bid, orders.Node{Owner: factory.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck})
	return nil
}

// validateFactoryProduct is the command-boundary preflight for products that
// have a catalog. Fixed definitions and canfly aircraft do not need a ground
// movement profile. Other mobile products must resolve one before entering a
// queue, preventing permanent content failures from becoming state-2 retries
// [04 §6.4][05 "Unit creation and limits"]. Both production callers pass the
// session catalog; a nil catalog is a fixture that has none, and defers
// resolution to the handler.
func validateFactoryProduct(cat *content.Catalog, key string) error {
	if cat == nil {
		return nil
	}
	def, ok := cat.Unit(key)
	if !ok || def == nil {
		return fmt.Errorf("%w: %q", ErrUnknownProduct, key)
	}
	domain := def.MobilityDomain
	// Definitions assembled directly by older callers predate the compiled
	// field. Apply the same established class split as the compiler adapter;
	// once present, the compiled domain is authoritative for admission.
	if domain == content.MobilityUnknown {
		switch {
		case def.BMCode == 0:
			domain = content.MobilityFixed
		case def.CanFly:
			domain = content.MobilityAircraft
		case def.MovementClass != "":
			domain = content.MobilityGround
		}
	}
	switch domain {
	case content.MobilityFixed, content.MobilityAircraft:
		// Aircraft admission is independent of any authored ground class. The
		// class may be present for another consumer, but it is not a required
		// aircraft ground profile [04 §6.4].
		return nil
	case content.MobilityGround:
		if def.MovementClass == "" {
			return fmt.Errorf("%w: ground product %q has no movement class", ErrMissingMovementProfile, key)
		}
		if _, ok := cat.Movement[content.CanonicalKey(def.MovementClass)]; !ok {
			return fmt.Errorf("%w %q for product %q", ErrMissingMovementProfile, def.MovementClass, key)
		}
		return nil
	default:
		// Unknown mobile products cannot be admitted without inventing a
		// placement domain or terrain profile [04 §6.4].
		return fmt.Errorf("%w: class-less mobile product %q", ErrMissingMovementProfile, key)
	}
}

// QueueMobileBuild enqueues a mobile build order with site anchor on builder's PRIMARY queue [P0-I05].
// Mobile payload: catalog index in Param1, count in Param2, site world anchor in GoalX/Z,
// blocked-area retry counter (zeroed) in Param3 [04 §3.2][R-ORDER-02 §1],
// BuildDefKey canonical, builder relation is Owner [05][P0-I05].
// Uses MobileBuild or VTOL_MobileBuild descriptor, distinct from factory's BuildingBuild [P0-I05].
func QueueMobileBuild(builder *units.Unit, defKey string, siteX, siteZ numeric.Fixed, count int, cat *content.Catalog) error {
	if builder == nil {
		return ErrNilFactory
	}
	if strings.TrimSpace(defKey) == "" {
		return ErrEmptyDef
	}
	if count <= 0 {
		return ErrBadCount
	}
	ck := content.CanonicalKey(defKey)
	pid := defIndex(cat, defKey)
	q := orders.QueueForUnit(builder)
	if q == nil {
		return ErrNoQueue
	}
	bid := mobileBuildOrderID(builder)
	if bid == 0 {
		return ErrNoBuildOrder
	}
	// Mobile builds do not coalesce across different sites: the tail must carry
	// the same product AND the same site, which is CoalesceTail's rule
	// [05 "Queue insertion"][P0-I05].
	q.CoalesceTail(bid, orders.Node{Owner: builder.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck, GoalX: siteX, GoalZ: siteZ, GoalSupplied: true})
	return nil
}

// CancelTailMost cancels the tail-most matching factory build for defKey [C20][P0-I05].
func CancelTailMost(factory *units.Unit, defKey string) error {
	if factory == nil {
		return ErrNilFactory
	}
	if strings.TrimSpace(defKey) == "" {
		return ErrEmptyDef
	}
	ck := content.CanonicalKey(defKey)
	q := orders.QueueForUnit(factory)
	if q == nil {
		return ErrNoQueue
	}
	bid := buildOrderID()
	if bid == 0 {
		return ErrNoBuildOrder
	}
	// "Find the last (tail-most) node matching operation byte and product
	// type" [05 "Queue subtraction"]. The operation byte is the descriptor and
	// the product type is the record's canonical key, which every insertion
	// path writes from the same catalog. This used to run twice — once
	// comparing a synthetic index that could not match, then again on the key
	// alone — which made the first pass dead weight and the second pass the
	// only one that ever cancelled anything.
	if !q.CancelTailMost(func(n orders.Node) bool { return n.ID == bid && n.BuildDefKey == ck }) {
		return fmt.Errorf("construction: no matching build %q to cancel", defKey)
	}
	return nil
}

// CancelProductCount subtracts count units from the tail-most matching
// factory product. Each subtraction uses the queue's existing tombstone,
// unlink, and Param2 decrement behavior [05 "Queue subtraction"] [R-P0-11].
// A partial cancellation is valid: retail removes as many matching units as
// remain and stops when no matching node is left.
func CancelProductCount(factory *units.Unit, defKey string, count int) error {
	if count <= 0 {
		return ErrBadCount
	}
	for i := 0; i < count; i++ {
		if err := CancelTailMost(factory, defKey); err != nil {
			if i == 0 {
				return err
			}
			break
		}
	}
	return nil
}

// CancelMobileTailMost cancels the tail-most matching mobile build for defKey and site [P0-I05].
func CancelMobileTailMost(builder *units.Unit, defKey string, siteX, siteZ numeric.Fixed) error {
	if builder == nil {
		return ErrNilFactory
	}
	if strings.TrimSpace(defKey) == "" {
		return ErrEmptyDef
	}
	ck := content.CanonicalKey(defKey)
	q := orders.QueueForUnit(builder)
	if q == nil {
		return ErrNoQueue
	}
	bid := mobileBuildOrderID(builder)
	if bid == 0 {
		return ErrNoBuildOrder
	}
	// The mobile row's product type carries its site too: distinct sites are
	// distinct orders [P0-I05]. One comparison, for the reason above.
	if !q.CancelTailMost(func(n orders.Node) bool {
		return n.ID == bid && n.BuildDefKey == ck && n.GoalX == siteX && n.GoalZ == siteZ
	}) {
		return fmt.Errorf("construction: no matching mobile build %q to cancel", defKey)
	}
	return nil
}
