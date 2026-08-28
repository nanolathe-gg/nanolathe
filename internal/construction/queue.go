// Package construction implements factory and mobile build requests and queue [PLAN_08 WU-08-4][05][P0-I05].
package construction

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
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
const MobileBuildOrder = "MobileBuild"
const VTOLMobileBuildOrder = "VTOL_MobileBuild"

const fallbackBuildOrder = "MobileBuild"

// ErrLimitMessage is the verbatim exhaustion message produced at nanoframe allocation
// when per-def limits or pool exhaustion refuses creation [05 "Unit creation and limits"].
const ErrLimitMessage = "Unable to create any more units"

var (
	ErrNilFactory             = errors.New("construction: nil factory")
	ErrEmptyDef               = errors.New("construction: empty defKey")
	ErrBadCount               = errors.New("construction: count must be > 0")
	ErrNoQueue                = errors.New("construction: no queue")
	ErrNoBuildOrder           = errors.New("construction: build order descriptor not found")
	ErrUnknownProduct         = errors.New("construction: product definition unavailable")
	ErrMissingMovementProfile = errors.New("construction: product movement profile unavailable")
	ErrLimit                  = errors.New(ErrLimitMessage)
)

// P0-I16: LimitChecker moved onto Service as authoritative session-owned hook.
// See Service.LimitChecker and Service.CheckLimit.

// ExhaustionError returns the verbatim limit exhaustion error [05 "Unit creation and limits"].
func ExhaustionError() error { return errors.New(ErrLimitMessage) }

// defIndex maps defKey to stable catalog index [P0-I05][02 §5].
// Uses catalog when available, otherwise a collision-free sequential fallback (not FNV hash).
var (
	fallbackIndexMap         = make(map[string]uint32)
	fallbackNextIndex uint32 = 100000 // high to avoid overlapping real 1..N
	fallbackMu        sync.Mutex
)

func defIndex(cat *content.Catalog, defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	if ck == "" {
		return 0
	}
	if cat != nil {
		if idx, ok := cat.UnitDefIndex(ck); ok {
			return idx
		}
	}
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	if id, ok := fallbackIndexMap[ck]; ok {
		return id
	}
	id := fallbackNextIndex
	fallbackNextIndex++
	fallbackIndexMap[ck] = id
	return id
}

// buildOrderID resolves the primary build descriptor for factory products [04 §3.1][GAP T3].
func buildOrderID() orders.ID {
	id := orders.Lookup(FactoryBuildOrder)
	if id != 0 {
		return id
	}
	return orders.Lookup(fallbackBuildOrder)
}

// mobileBuildOrderID resolves the mobile build descriptor for the builder [04 §3.1][P0-I05].
// VTOL builders use VTOL_MobileBuild, others use MobileBuild [04 §3.4] code 14.
func mobileBuildOrderID(builder *units.Unit) orders.ID {
	if builder != nil && builder.Def != nil && builder.Def.CanFly {
		if id := orders.Lookup(VTOLMobileBuildOrder); id != 0 {
			return id
		}
	}
	if id := orders.Lookup(MobileBuildOrder); id != 0 {
		return id
	}
	return buildOrderID()
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
	prim := q.Primary()
	if len(prim) > 0 {
		tail := prim[len(prim)-1]
		if tail.ID == bid && tail.Param1 == pid && tail.BuildDefKey == ck {
			// Tail-only coalesce [05 "Queue insertion"].
			q.CoalesceTail(bid, orders.Node{Owner: factory.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck})
			// CoalesceTail compares Param1 only; ensure BuildDefKey also matches by re-checking last tail after?
			// Since fallback indices are unique per ck, Param1 equality already implies same product.
			return nil
		}
	} else {
		q.CoalesceTail(bid, orders.Node{Owner: factory.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck})
		return nil
	}
	q.Push(bid, orders.Node{Owner: factory.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck})
	return nil
}

// validateFactoryProduct is the command-boundary preflight for products that
// have a catalog. Fixed definitions and canfly aircraft do not need a ground
// movement profile. Other mobile products must resolve one before entering a
// queue, preventing permanent content failures from becoming state-2 retries
// [04 §6.4][05 "Unit creation and limits"]. A nil catalog remains the legacy
// AI/test adapter and defers resolution to the handler.
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
		case !def.BMCode:
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
	// Mobile builds do not coalesce across different sites; tail-only coalesce requires
	// same product AND same site (GoalX/Z). Distinct sites remain separate orders [P0-I05].
	prim := q.Primary()
	if len(prim) > 0 {
		tail := prim[len(prim)-1]
		if tail.ID == bid && tail.Param1 == pid && tail.BuildDefKey == ck && tail.GoalX == siteX && tail.GoalZ == siteZ {
			q.CoalesceTail(bid, orders.Node{Owner: builder.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck, GoalX: siteX, GoalZ: siteZ})
			return nil
		}
	} else {
		// Empty: use CoalesceTail path for uniform FlagActive handling, but need to set Goal after
		// since CoalesceTail creates node with Goal. Push with Goal directly instead.
		q.Push(bid, orders.Node{Owner: builder.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck, GoalX: siteX, GoalZ: siteZ})
		return nil
	}
	q.Push(bid, orders.Node{Owner: builder.Handle, Param1: pid, Param2: uint32(count), BuildDefKey: ck, GoalX: siteX, GoalZ: siteZ})
	return nil
}

// QueueBuild is the legacy factory entry for AI compatibility [P0-I16][P0-I05].
// It enqueues factory products via BuildingBuild using a nil-catalog fallback index.
// New code should use QueueFactoryBuild (with catalog) or QueueMobileBuild (with site).
// This wrapper preserves the original signature so AI's Manager.QueueBuild closure
// continues to compile without modifying internal/ai/* [P0-I16].
func QueueBuild(factory *units.Unit, defKey string, count int) error {
	return QueueFactoryBuild(factory, defKey, count, nil)
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
	// Use generic pid for comparison that includes fallback mapping consistent with QueueFactoryBuild nil-cat path.
	pid := defIndex(nil, defKey)
	q := orders.QueueForUnit(factory)
	if q == nil {
		return ErrNoQueue
	}
	bid := buildOrderID()
	if bid == 0 {
		return ErrNoBuildOrder
	}
	matched := q.CancelTailMost(func(n orders.Node) bool {
		// Compare both index and BuildDefKey for fallback safety (index 0 case).
		return n.ID == bid && n.Param1 == pid && (n.BuildDefKey == "" || n.BuildDefKey == ck)
	})
	if !matched {
		// Try with catalog-aware pid? If factory build was enqueued with catalog-provided index,
		// pid from nil fallback differs. Fall back to BuildDefKey string match alone.
		matched = q.CancelTailMost(func(n orders.Node) bool {
			return n.ID == bid && n.BuildDefKey == ck
		})
		if !matched {
			return fmt.Errorf("construction: no matching build %q to cancel", defKey)
		}
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
	pid := defIndex(nil, defKey)
	q := orders.QueueForUnit(builder)
	if q == nil {
		return ErrNoQueue
	}
	bid := mobileBuildOrderID(builder)
	if bid == 0 {
		return ErrNoBuildOrder
	}
	matched := q.CancelTailMost(func(n orders.Node) bool {
		return n.ID == bid && n.Param1 == pid && n.BuildDefKey == ck && n.GoalX == siteX && n.GoalZ == siteZ
	})
	if !matched {
		matched = q.CancelTailMost(func(n orders.Node) bool {
			return n.ID == bid && n.BuildDefKey == ck && n.GoalX == siteX && n.GoalZ == siteZ
		})
		if !matched {
			return fmt.Errorf("construction: no matching mobile build %q to cancel", defKey)
		}
	}
	return nil
}
