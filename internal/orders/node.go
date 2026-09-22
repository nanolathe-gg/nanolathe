package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// NewNodeForOrder is the single canonical command payload constructor [04 §3.2][04 §3.4][P0-I03].
// It writes target handle or point, GoalX/Y/Z in 16.16 world coords, queue modifier,
// command-specific fields via Param*, creation tick and owner. Every producer
// (HUD, AI, InitialMission, rally inheritance, tests) must route through it.
//
//   - id: canonical descriptor ID from Lookup (0 is reject)
//   - target: smart-reference handle of unit/feature target, 0 for ground point
//   - goalX/Y/Z: fixed-point world coords of the command's position payload
//   - tick: creation tick snapshot (Clock.GlobalTick)
//   - owner: owning unit handle
//   - queued: queue modifier — true is Append/Shift-queue (insert after active without purge),
//     false is Replace (purge unprotected + drop leading auto before insert) [04 §3.3][P0-08].
//
// The queued flag selects what the CALLER does to the queue before inserting, and it is
// also the producer insertion's own argument: Push arms the one-shot caption-pending bit
// only on a NON-QUEUED (Replace) issue, so a Shift-queued order is inserted silent
// [04 R-ORD-01 §13]. It is carried to Push in Node.QueuedIssue and written onto no stored
// record. Purge survivorship is the descriptor's static gate bit 2, and
// newNode applies it at insertion [04 §3.3][04 R-MOV-03 §6].
// StaticGate/DynamicGate/Deadline are filled later by newNode from the descriptor;
// caller may set Param1..3 for command-specific fields before Push.
//
// The goal triple is the command's position payload, so a record built here is
// constructed WITH a goal and GoalSupplied says so; newNode then keeps static
// bit 10 on the record's static-mask copy [04 §3.1][04 R-MOV-03 §7]. A producer
// whose command has no position payload passes zeros through this same door,
// and those commands — the activation, cloak, standing, wait, paralyze, pickup,
// carried, stockpile and factory-product rows — are exactly the rows whose
// authored mask carries no bit 10 to clear (table.go), so the flag is
// unobservable for them. The factory product is the clearest case: it leaves
// through the factory's own exit and `BuildingBuild` ignores the goal triple
// [05 "Factory production lifecycle"], and its authored mask carries no bit 10
// either, so the queue that builds that record passes zeros through this same
// door.
func NewNodeForOrder(id ID, target pool.Handle, goalX, goalY, goalZ numeric.Fixed, tick uint32, owner pool.Handle, queued bool) Node {
	n := Node{
		ID:           id,
		Target:       target,
		GoalX:        goalX,
		GoalY:        goalY,
		GoalZ:        goalZ,
		CreationTick: tick,
		Owner:        owner,
		GoalSupplied: true,
	}
	n.QueuedIssue = queued // the producer insertion's queued/non-queued argument [04 R-ORD-01 §13]
	n.GoalSupplied = true  // the position payload above is the constructor's goal argument [04 R-MOV-03 §7]
	return n
}

// NewMoveNode is a convenience for pure point moves (no target) [04 §3.4] code 2.
func NewMoveNode(id ID, goalX, goalZ numeric.Fixed, tick uint32, owner pool.Handle, queued bool) Node {
	return NewNodeForOrder(id, 0, goalX, 0, goalZ, tick, owner, queued)
}

// NewMobileBuildNode constructs a mobile build node with site anchor payload [05 "Construction arithmetic"][P0-I05].
// Mobile build payload: definition catalog index in Param1, site world anchor in GoalX/Z,
// builder relation is Owner, remaining count in Param2 [05][P0-I05].
// Param3 is the blocked-area retry counter [04 §3.2][R-ORDER-02 §1] and starts
// zeroed; the handler's setup path zeroes it on every (re)arm. ID is
// MobileBuild or VTOL_MobileBuild chosen by caller [04 §3.1].
// BuildFacing is a Nanolathe extension field outside the retail parameter
// words: [04 §3.2] keeps Param3 as the blocked-area retry counter. Only the
// player-facing producer supplies it; zero leaves AI and restored untagged
// orders facing south [community patch engine behavior, CP-CON-5].
func NewMobileBuildNode(cat *content.Catalog, defKey string, siteX, siteZ numeric.Fixed, orientation uint16, count uint32, tick uint32, owner pool.Handle, queued bool) Node {
	ck := content.CanonicalKey(defKey)
	idx, _ := catalogIndex(cat, ck)
	id := rowMobileBuild
	// Caller may override ID for VTOL; keep MobileBuild default if not VTOL.
	n := NewNodeForOrder(id, 0, siteX, 0, siteZ, tick, owner, queued)
	n.BuildDefKey = ck
	n.BuildFacing = units.StructureFacing(orientation & 3)
	n.Param1 = idx
	n.Param2 = count
	return n
}

// catalogIndex maps a canonical key to a stable catalog index [P0-I05][02 §5].
// A missing catalog or unknown key retains the catalog's zero reject sentinel;
// no runtime identity is invented for an unresolved definition.
func catalogIndex(cat *content.Catalog, canonicalKey string) (uint32, bool) {
	if canonicalKey == "" {
		return 0, false
	}
	if cat != nil {
		if idx, ok := cat.UnitDefIndex(canonicalKey); ok {
			return idx, true
		}
	}
	return 0, false
}

// IsMobileBuild reports whether id is a mobile build handler [P0-I05][04 §3.1].
func IsMobileBuild(id ID) bool {
	name := DescriptorFor(id).Name
	return name == "MobileBuild" || name == "VTOL_MobileBuild"
}
