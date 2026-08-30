// Package orders implements the 68-order descriptor table [04 §3.1][R-DOC04-C] [PLAN_06 WU-06-2].
package orders

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/units"
)

// ID is the index into the sorted descriptor table; 0 is the reject sentinel [04 §3.1] C4.
type ID uint8

// PresentationHelper is the optional presentation helper a descriptor carries,
// run during command resolution [04 §3.1]. The field takes exactly four
// identities across all 68 descriptors [R-DOC04-C]; it is an identity the
// presentation layer branches on, never per-descriptor behavior.
type PresentationHelper uint8

const (
	// HelperNone: the descriptor carries no presentation helper.
	HelperNone PresentationHelper = iota
	// HelperGoalResolveAck runs goal resolution with acknowledgement text and
	// rings — the attack, suppress, capture, pickup, unload, teleport, and
	// help-build families [R-DOC04-C]. Per-record resolution: the attack
	// family is Attack_NoMove, Attack_Chase, Attack_Kamikaze, AttackSpecial,
	// AirStrike, AirToAir, AirToGround, and AirToGroundHover — AttackUType
	// carries no helper despite its name; VTOL_Landing belongs with the
	// unload family; RepairUnitNoMove carries acknowledgement without path
	// markers [R-DOC04-C].
	HelperGoalResolveAck
	// HelperGoalResolveAckPathMarkers is HelperGoalResolveAck plus moving path
	// markers — the move (incl. the queued variant QMove), patrol (incl.
	// QPatrol), repair-patrol, follow, repair-unit, reclaim (the point order
	// Reclaim alongside ReclaimUnit), and resurrect families [R-DOC04-C].
	HelperGoalResolveAckPathMarkers
	// HelperBuildFootprint draws the build-footprint marker; carried by
	// MobileBuild and VTOL_MobileBuild only [R-DOC04-C].
	HelperBuildFootprint
)

// Descriptor is one order descriptor [04 §3.1] C4.
type Descriptor struct {
	Name       string                                              // canonical, the sort key and binary-search key [04 §3.1]
	StateLabel string                                              // state label the interface uses for a unit running this order [04 §3.1]
	Class      uint8                                               // small class parameter [04 §3.1] TODO(question) [P0-07]: bounded census over function boundaries found no reader — stored opaque, never branched on
	AckGroup   uint8                                               // acknowledgement group index [04 §3.1]
	StaticGate uint32                                              // 32-bit static gate mask; see the census note below
	Handler    func(u *units.Unit, n *Node, satisfied uint32) Code // [04 §3.1] called with the owning unit, the order record, and the bits satisfied this tick
	// Presentation is the descriptor's goal-resolution presentation-helper
	// identity, one of the four PresentationHelper values [04 §3.1][R-DOC04-C].
	Presentation PresentationHelper
}

// StaticGate census [04 §3.1][R-DOC04-C]. Named readers: bit 9 (0x200) is
// cleared when the order is constructed without a target unit; bit 10 (0x400)
// is cleared when constructed without a goal position; bit 18 (0x40000) marks
// a record that belongs in the rear queue segment; bit 20 (0x100000) marks
// the nanolathe/build-site class, read by the guard-assist branch. Bits 14
// and 21 exist only at runtime (tail-record inheritance and the cached target
// position) and appear in no static mask. The remaining static bits (1-8, 11,
// 16, 17, 19, 24) have no located reader in the [R-DOC04-C] census: the raw
// mask is stored verbatim and never interpreted — do not add readers, do not
// add or drop bits, without a new research finding.

// The four static batches below are transcribed verbatim in their compiled
// registration order [R-DOC04-C]. buildTable appends them batch by batch and
// re-sorts the whole table after every batch with a case-sensitive byte
// comparison, so an order's identity is its index in the final sorted table
// [04 §3.1] C4.

// batch1 is registration batch 1: 23 records [R-DOC04-C].
var batch1 = []Descriptor{
	{Name: "Stop", StateLabel: "Stopping", Class: 0x00, AckGroup: 19, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "Attack_NoMove", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x280, Presentation: HelperGoalResolveAck},
	{Name: "Activate", StateLabel: "Activate", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone, Handler: activateHandler},
	{Name: "Deactivate", StateLabel: "Deactivate", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone, Handler: deactivateHandler},
	{Name: "Cloak_On", StateLabel: "Cloaking", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "Cloak_Off", StateLabel: "Decloaking", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "Standing_MoveOrder", StateLabel: "Acknowledged", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "Standing_FireOrder", StateLabel: "Acknowledged", Class: 0x00, AckGroup: 19, StaticGate: 0x10060, Presentation: HelperNone},
	{Name: "BuildingBuild", StateLabel: "Nanolathing", Class: 0x00, AckGroup: 19, StaticGate: 0x10010c, Presentation: HelperNone},
	{Name: "BuildWeapon", StateLabel: "Nanolathing", Class: 0x00, AckGroup: 19, StaticGate: 0xc0140, Presentation: HelperNone},
	{Name: "SelfDestruct", StateLabel: "SELF DESTRUCT ENGAGED", Class: 0x00, AckGroup: 19, StaticGate: 0x40040, Presentation: HelperNone},
	{Name: "SelfDestructFG", StateLabel: "SELF DESTRUCT ENGAGED", Class: 0x00, AckGroup: 19, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "Paralyze", StateLabel: "Paralyzed", Class: 0x00, AckGroup: 19, StaticGate: 0x24, Presentation: HelperNone},
	{Name: "GetBuilt", StateLabel: "Under construction", Class: 0x00, AckGroup: 19, StaticGate: 0x224, Presentation: HelperNone},
	{Name: "BeCarried", StateLabel: "Being transported", Class: 0x00, AckGroup: 19, StaticGate: 0x24, Presentation: HelperNone},
	{Name: "MakeSelectable", StateLabel: "Unit is available", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Presentation: HelperNone, Handler: makeSelectableHandler},
	{Name: "Wait", StateLabel: "Waiting", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Presentation: HelperNone},
	{Name: "WaitForAttack", StateLabel: "Waiting for attack", Class: 0x00, AckGroup: 19, StaticGate: 0x204, Presentation: HelperNone},
	{Name: "AttackUType", StateLabel: "Attacking", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Presentation: HelperNone},
	{Name: "Guard_NoMove", StateLabel: "Ready", Class: 0x00, AckGroup: 19, StaticGate: 0x20, Presentation: HelperNone},
	{Name: "SelfRepair", StateLabel: "Repairing", Class: 0x00, AckGroup: 19, StaticGate: 0x1000204, Presentation: HelperNone},
	{Name: "QMove", StateLabel: "Ready with orders", Class: 0x02, AckGroup: 14, StaticGate: 0x400, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "QPatrol", StateLabel: "Ready with orders", Class: 0x02, AckGroup: 7, StaticGate: 0x400, Presentation: HelperGoalResolveAckPathMarkers},
}

// batch2 is registration batch 2: 22 records [R-DOC04-C].
var batch2 = []Descriptor{
	{Name: "Standby", StateLabel: "Standby", Class: 0x10, AckGroup: 15, StaticGate: 0x20000, Presentation: HelperNone},
	{Name: "Standby_Mine", StateLabel: "Standby", Class: 0x10, AckGroup: 15, StaticGate: 0x1020000, Presentation: HelperNone},
	{Name: "Move_Ground", StateLabel: "Moving", Class: 0x12, AckGroup: 14, StaticGate: 0x402, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Follow_Ground", StateLabel: "Guarding", Class: 0x12, AckGroup: 5, StaticGate: 0x200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Suppress", StateLabel: "Suppressing fire", Class: 0x08, AckGroup: 1, StaticGate: 0x410, Presentation: HelperGoalResolveAck},
	{Name: "Attack_Chase", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x280, Presentation: HelperGoalResolveAck},
	{Name: "Attack_Kamikaze", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "AttackSpecial", StateLabel: "Annihilating", Class: 0x08, AckGroup: 1, StaticGate: 0x680, Presentation: HelperGoalResolveAck},
	{Name: "Park", StateLabel: "Parking", Class: 0x00, AckGroup: 14, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "Patrol", StateLabel: "Patrolling", Class: 0x12, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Ground_Pickup", StateLabel: "Loading", Class: 0x08, AckGroup: 12, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "Ground_Unload", StateLabel: "Unloading", Class: 0x08, AckGroup: 13, StaticGate: 0x400, Presentation: HelperGoalResolveAck},
	{Name: "Teleport", StateLabel: "Teleporting", Class: 0x08, AckGroup: 9, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "MobileBuild", StateLabel: "Nanolathing", Class: 0x13, AckGroup: 0, StaticGate: 0x100508, Presentation: HelperBuildFootprint},
	{Name: "HelpBuild", StateLabel: "Nanolathing", Class: 0x18, AckGroup: 6, StaticGate: 0x100208, Presentation: HelperGoalResolveAck},
	{Name: "RepairPatrol", StateLabel: "Repair patrol", Class: 0x12, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "RepairUnit", StateLabel: "Repairing", Class: 0x12, AckGroup: 6, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Capture", StateLabel: "Capturing", Class: 0x08, AckGroup: 4, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "Resurrect", StateLabel: "Resurrecting", Class: 0x12, AckGroup: 11, StaticGate: 0x200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "Reclaim", StateLabel: "Reclaiming", Class: 0x12, AckGroup: 11, StaticGate: 0x100800, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "ReclaimUnit", StateLabel: "Reclaiming", Class: 0x12, AckGroup: 11, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "RepairUnitNoMove", StateLabel: "Repairing", Class: 0x18, AckGroup: 6, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
}

// batch3 is registration batch 3: 22 records [R-DOC04-C].
var batch3 = []Descriptor{
	{Name: "VTOL_Standby", StateLabel: "Standby", Class: 0x00, AckGroup: 15, StaticGate: 0x20000, Presentation: HelperNone},
	{Name: "VTOL_Move", StateLabel: "Moving", Class: 0x02, AckGroup: 14, StaticGate: 0x402, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Landing", StateLabel: "Landing", Class: 0x08, AckGroup: 14, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_Pickup", StateLabel: "Loading", Class: 0x08, AckGroup: 8, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_Unload", StateLabel: "Unloading", Class: 0x08, AckGroup: 9, StaticGate: 0x400, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_Follow", StateLabel: "Guarding", Class: 0x02, AckGroup: 5, StaticGate: 0x200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Patrol", StateLabel: "Patrolling", Class: 0x02, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "AirStrike", StateLabel: "Airstrike", Class: 0x08, AckGroup: 2, StaticGate: 0x600, Presentation: HelperGoalResolveAck},
	{Name: "AirToAir", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "AirToGround", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "AirToGroundHover", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_MobileBuild", StateLabel: "Nanolathing", Class: 0x03, AckGroup: 0, StaticGate: 0x100508, Presentation: HelperBuildFootprint},
	{Name: "VTOL_HelpBuild", StateLabel: "Nanolathing", Class: 0x08, AckGroup: 6, StaticGate: 0x100208, Presentation: HelperGoalResolveAck},
	{Name: "VTOL_RepairPatrol", StateLabel: "Repair patrol", Class: 0x02, AckGroup: 7, StaticGate: 0x412, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_RepairUnit", StateLabel: "Repairing", Class: 0x02, AckGroup: 6, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Reclaim", StateLabel: "Reclaiming", Class: 0x02, AckGroup: 11, StaticGate: 0x100800, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_ReclaimUnit", StateLabel: "Reclaiming", Class: 0x02, AckGroup: 11, StaticGate: 0x100200, Presentation: HelperGoalResolveAckPathMarkers},
	{Name: "VTOL_Evade", StateLabel: "Evading", Class: 0x00, AckGroup: 19, StaticGate: 0x0, Presentation: HelperNone},
	{Name: "VTOL_SeekAttack", StateLabel: "Seeking to attack", Class: 0x00, AckGroup: 19, StaticGate: 0x600, Presentation: HelperNone},
	{Name: "VTOL_SeekGuard", StateLabel: "Seeking to guard", Class: 0x00, AckGroup: 19, StaticGate: 0x600, Presentation: HelperNone},
	{Name: "VTOL_GetRepaired", StateLabel: "Under repair", Class: 0x00, AckGroup: 19, StaticGate: 0x200, Presentation: HelperNone},
	{Name: "VTOL_LandIfCan", StateLabel: "Seeking to land", Class: 0x00, AckGroup: 19, StaticGate: 0x400, Presentation: HelperNone},
}

// batch4 is registration batch 4: the single empty-name record. It has no
// static image in the read-only data; that it is appended at registration
// rather than compiled in is Supported inference [R-DOC04-C]. Ordering is
// unaffected either way — every batch append re-sorts the whole table, and
// the empty name sorts to index 0, the reject sentinel [04 §3.1] C4.
var batch4 = []Descriptor{
	{Name: "", StateLabel: "", Class: 0x00, AckGroup: 0, StaticGate: 0x0, Presentation: HelperNone},
}

var table []Descriptor
var byName map[string]ID

func init() { buildTable() }

// buildTable builds the 68-entry table from the four static batches of 23,
// 22, 22, and 1 records [R-DOC04-C]; after every batch the whole table
// re-sorts ascending by canonical name using a case-sensitive byte comparison
// [04 §3.1] C4. The final sorted order is the 67 named commands plus the
// empty sentinel at index 0.
func buildTable() {
	all := make([]Descriptor, 0, 68)
	for _, batch := range [][]Descriptor{batch1, batch2, batch3, batch4} {
		all = append(all, batch...)
		sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name }) // case-sensitive byte compare [04 §3.1] C4
	}
	table = all
	byName = make(map[string]ID, len(table))
	for i, d := range table {
		byName[d.Name] = ID(i)
	}
}

// Table returns the 68-entry descriptor table, index 0 is "" sentinel [04 §3.1] C4.
func Table() []Descriptor { return table }

// Lookup returns the ID for name via binary search; miss returns 0 sentinel [04 §3.1] C4.
// Search is case-sensitive byte compare as retail sorts [04 §3.1].
func Lookup(name string) ID {
	if id, ok := byName[name]; ok {
		return id
	}
	// Binary search over sorted table for canonical match.
	i := sort.Search(len(table), func(i int) bool { return table[i].Name >= name })
	if i < len(table) && table[i].Name == name {
		return ID(i)
	}
	return 0
}

// DescriptorFor returns the descriptor for id, or the sentinel if out of range.
func DescriptorFor(id ID) Descriptor {
	if int(id) < len(table) {
		return table[int(id)]
	}
	return table[0]
}
