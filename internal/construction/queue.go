// Package construction implements factory build requests and queue [PLAN_08 WU-08-4][05].
package construction

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Architectural: factory products live as TYPED PAYLOADS on orders.Node in the PRIMARY segment.
// This package does NOT define a second node or queue type. Construction calls phase 6's
// queue/coalesce/cancel API (Queue.Push/CoalesceTail/CancelTailMost) [PLAN_08 C15][GAP T3].
// The secondary segment is exclusively BuildWeapon/SelfDestruct [GAP T3] — factory products
// never use it.
//
// Payload encoding on orders.Node fields per [04 §3.2] build family meanings:
//   Param1 = product definition index / template id — deterministic hash of canonical defKey
//           (CanonicalKey lowercases per [02 §5]; FNV-1a over that key gives uint32)
//   Param2 = remaining build count [05 "Build request and factory queue behavior"]
//   Param3 = unused for factory products
//   ID     = primary build descriptor (BuildingBuild preferred, MobileBuild fallback) [04 §3.1]
// Primary segment only; no second queue type exists.

// FactoryBuildOrder is the primary descriptor for factory products [04 §3.1][GAP T3].
// Retail dump shows only BuildWeapon (0xc0140) and SelfDestruct (0x40040) carry bit 0x40000
// (secondary selection), so factory products are primary. BuildingBuild is the canonical
// factory production descriptor; MobileBuild is the fallback for fixtures [04 §3.1].
const FactoryBuildOrder = "BuildingBuild"
const fallbackBuildOrder = "MobileBuild"

// ErrLimitMessage is the verbatim exhaustion message produced at nanoframe allocation
// when per-def limits or pool exhaustion refuses creation [05 "Unit creation and limits"].
// Exact string verified in research: handler prints "Unable to create any more units",
// schedules retry in exactly 300 ticks (not randomized) and stays in state 2 [05].
const ErrLimitMessage = "Unable to create any more units"

var (
	ErrNilFactory   = errors.New("construction: nil factory")
	ErrEmptyDef     = errors.New("construction: empty defKey")
	ErrBadCount     = errors.New("construction: count must be > 0")
	ErrNoQueue      = errors.New("construction: no queue")
	ErrNoBuildOrder = errors.New("construction: build order descriptor not found")
)

// LimitChecker is the hook for WU-08-5 nanoframe allocation per-def limit check [05 "Unit creation and limits"] C23.
// It is called ONLY at allocation time (factory lifecycle state 2), not at queue time —
// queued factory products hold no reservation: a full queue simply fails each allocation
// attempt and retries [05 "Unit creation and limits"].
// Return true if creation allowed, false if limit exhausted (or pool full).
// When false, producer prints ErrLimitMessage and retries in 300 ticks [05].
// Default nil means unlimited (allow all) — stock defaults sentinel -1 unlimited [05].
// WU-08-5 sets this to enforce counts within owning player's slice only [C23].
var LimitChecker func(factory *units.Unit, defKey string) bool

// CheckLimit reports whether nanoframe allocation for defKey on factory is allowed
// via LimitChecker [C23]. Nil checker means allowed.
func CheckLimit(factory *units.Unit, defKey string) bool {
	if LimitChecker == nil {
		return true
	}
	return LimitChecker(factory, defKey)
}

// ExhaustionError returns the verbatim limit exhaustion error [05 "Unit creation and limits"].
func ExhaustionError() error { return errors.New(ErrLimitMessage) }

// productID maps defKey to uint32 for Node.Param1 per [04 §3.2] build family.
// Uses FNV-1a over CanonicalKey (case-insensitive per [02 §5]) for deterministic mapping.
func productID(defKey string) uint32 {
	ck := content.CanonicalKey(defKey)
	h := fnv.New32a()
	_, _ = h.Write([]byte(ck))
	return h.Sum32()
}

// buildOrderID resolves the primary build descriptor for factory products [04 §3.1][GAP T3].
func buildOrderID() orders.ID {
	id := orders.Lookup(FactoryBuildOrder)
	if id != 0 {
		return id
	}
	return orders.Lookup(fallbackBuildOrder)
}

// QueueBuild enqueues count copies of defKey on factory's PRIMARY queue [C15][C20][C23].
//
//   - Payload is typed on orders.Node (Param1 = productID, Param2 = count) [04 §3.2] C15.
//   - Counted adds coalesce TAIL-ONLY via orders.Queue.CoalesceTail [C20]:
//     distinct products never merged; identical product separated from tail never merged
//     because only the tail is tested [04 §3.3][05 "Queue insertion"].
//   - Insertion after the active marker 0x1000 uses orders.Queue.Push [C20][05 "Queue insertion"].
//     The fallback path (no coalesce) inserts after the single active-order marker.
//   - No repeat flag exists — repetition is count plus same-pass state-0 restart [05 "Factory production lifecycle"][GAP T3].
//   - Per-def limits are NOT checked here; they are enforced only at nanoframe allocation
//     via LimitChecker with no queue reservation [C23][05 "Unit creation and limits"].
func QueueBuild(factory *units.Unit, defKey string, count int) error {
	if factory == nil {
		return ErrNilFactory
	}
	if strings.TrimSpace(defKey) == "" {
		return ErrEmptyDef
	}
	if count <= 0 {
		return ErrBadCount
	}
	pid := productID(defKey)
	q := orders.QueueForUnit(factory)
	if q == nil {
		return ErrNoQueue
	}
	bid := buildOrderID()
	if bid == 0 {
		return ErrNoBuildOrder
	}
	// Tail-only coalesce: only the tail is examined [05 "Queue insertion"].
	// If tail matches operation and product type, add count and return.
	prim := q.Primary()
	if len(prim) > 0 {
		tail := prim[len(prim)-1]
		if tail.ID == bid && tail.Param1 == uint32(pid) {
			q.CoalesceTail(bid, orders.Node{Param1: uint32(pid), Param2: uint32(count)})
			return nil
		}
	} else {
		// Empty queue: coalesce trivially creates head with active flag via CoalesceTail tail path.
		// Use CoalesceTail to keep path uniform (it sets FlagActive when len==1).
		q.CoalesceTail(bid, orders.Node{Param1: uint32(pid), Param2: uint32(count)})
		return nil
	}
	// No coalesce — insert after active marker via Push [C20][05 "Queue insertion"].
	q.Push(bid, orders.Node{Param1: uint32(pid), Param2: uint32(count)})
	return nil
}

// CancelTailMost cancels the tail-most matching build for defKey on factory's queue [C20].
// It matches the tail-most node with the factory build ID and productID, tombstones non-head
// nodes so cleanup skips TargetCleared, and leaves secondary always tombstoned [04 §3.3][05 "Queue subtraction"].
//
// Cancellation walks tail-most and reduces Param2 when >1, otherwise unlinks the node
// and runs cleanup in strict order [05 "Queue subtraction"] — tombstone observable via Node.Flags.
func CancelTailMost(factory *units.Unit, defKey string) error {
	if factory == nil {
		return ErrNilFactory
	}
	if strings.TrimSpace(defKey) == "" {
		return ErrEmptyDef
	}
	pid := productID(defKey)
	q := orders.QueueForUnit(factory)
	if q == nil {
		return ErrNoQueue
	}
	bid := buildOrderID()
	if bid == 0 {
		return ErrNoBuildOrder
	}
	matched := q.CancelTailMost(func(n orders.Node) bool {
		return n.ID == bid && n.Param1 == uint32(pid)
	})
	if !matched {
		return fmt.Errorf("construction: no matching build %q to cancel", defKey)
	}
	return nil
}
