// Package orders implements the 68-order descriptor table [04 §3.1] [PLAN_06 WU-06-2].
package orders

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/units"
)

// ID is the index into the sorted descriptor table; 0 is the reject sentinel [04 §3.1] C4.
type ID uint8

// Descriptor is one order descriptor [04 §3.1] C4.
type Descriptor struct {
	Name       string                                              // canonical, sort key, binary-search key [04 §3.1]
	StateLabel string                                              // state label the interface uses [04 §3.1]
	Class      uint8                                               // small class parameter [04 §3.1] TODO(question): no located consumer
	AckGroup   uint8                                               // acknowledgement group [04 §3.1]
	StaticGate uint32                                              // 32-bit static gate mask [04 §3.1]
	Handler    func(u *units.Unit, n *Node, satisfied uint32) Code // [04 §3.1] handler, called with owning unit, order record, and satisfied bits
}

var table []Descriptor
var byName map[string]ID

func init() { buildTable() }

// buildTable builds the 68-entry table from four static batches of 23,22,22,1
// records; after every batch the whole table re-sorts ascending by canonical
// name using case-sensitive byte comparison [04 §3.1] C4.
// The final sorted order is the documented 67 named plus "" at 0.
func buildTable() {
	// Batch 1: 23
	b1 := []Descriptor{
		{Name: "Activate", StateLabel: "Activate", Class: 0x00, AckGroup: 19, StaticGate: 0x10060},
		{Name: "AirStrike", StateLabel: "Airstrike", Class: 0x08, AckGroup: 2, StaticGate: 0x600},
		{Name: "AirToAir", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200},
		{Name: "AirToGround", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200},
		{Name: "AirToGroundHover", StateLabel: "Engaging target", Class: 0x08, AckGroup: 1, StaticGate: 0x200},
		{Name: "AttackSpecial", StateLabel: "Annihilating", Class: 0x08, AckGroup: 1, StaticGate: 0x680},
		{Name: "AttackUType", StateLabel: "Attacking", Class: 0x00, AckGroup: 19, StaticGate: 0x4},
		{Name: "Attack_Chase", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x280},
		{Name: "Attack_Kamikaze", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x600},
		{Name: "Attack_NoMove", StateLabel: "Attacking", Class: 0x08, AckGroup: 1, StaticGate: 0x280},
		{Name: "BeCarried", StateLabel: "Being transported", Class: 0x00, AckGroup: 19, StaticGate: 0x24},
		{Name: "BuildWeapon", StateLabel: "Nanolathing", Class: 0x00, AckGroup: 19, StaticGate: 0xc0140},
		{Name: "BuildingBuild", StateLabel: "Nanolathing", Class: 0x00, AckGroup: 19, StaticGate: 0x10010c},
		{Name: "Capture", StateLabel: "Capturing", Class: 0x08, AckGroup: 4, StaticGate: 0x200},
		{Name: "Cloak_Off", StateLabel: "Decloaking", Class: 0x00, AckGroup: 19, StaticGate: 0x10060},
		{Name: "Cloak_On", StateLabel: "Cloaking", Class: 0x00, AckGroup: 19, StaticGate: 0x10060},
		{Name: "Deactivate", StateLabel: "Deactivate", Class: 0x00, AckGroup: 19, StaticGate: 0x10060},
		{Name: "Follow_Ground", StateLabel: "Guarding", Class: 0x12, AckGroup: 5, StaticGate: 0x200},
		{Name: "GetBuilt", StateLabel: "Under construction", Class: 0x00, AckGroup: 19, StaticGate: 0x224},
		{Name: "Ground_Pickup", StateLabel: "Loading", Class: 0x08, AckGroup: 12, StaticGate: 0x200},
		{Name: "Ground_Unload", StateLabel: "Unloading", Class: 0x08, AckGroup: 13, StaticGate: 0x400},
		{Name: "Guard_NoMove", StateLabel: "Ready", Class: 0x00, AckGroup: 19, StaticGate: 0x20},
		{Name: "HelpBuild", StateLabel: "Nanolathing", Class: 0x18, AckGroup: 6, StaticGate: 0x100208},
	}
	// Batch 2: 22
	b2 := []Descriptor{
		{Name: "MakeSelectable", StateLabel: "Unit is available", Class: 0x00, AckGroup: 19, StaticGate: 0x4, Handler: makeSelectableHandler},
		{Name: "MobileBuild", StateLabel: "Nanolathing", Class: 0x13, AckGroup: 0, StaticGate: 0x100508},
		{Name: "Move_Ground", StateLabel: "Moving", Class: 0x12, AckGroup: 14, StaticGate: 0x402},
		{Name: "Paralyze", StateLabel: "Paralyzed", Class: 0x00, AckGroup: 19, StaticGate: 0x24},
		{Name: "Park", StateLabel: "Parking", Class: 0x00, AckGroup: 14, StaticGate: 0x0},
		{Name: "Patrol", StateLabel: "Patrolling", Class: 0x12, AckGroup: 7, StaticGate: 0x412},
		{Name: "QMove", StateLabel: "Ready with orders", Class: 0x02, AckGroup: 14, StaticGate: 0x400},
		{Name: "QPatrol", StateLabel: "Ready with orders", Class: 0x02, AckGroup: 7, StaticGate: 0x400},
		{Name: "Reclaim", StateLabel: "Reclaiming", Class: 0x12, AckGroup: 11, StaticGate: 0x100800},
		{Name: "ReclaimUnit", StateLabel: "Reclaiming", Class: 0x12, AckGroup: 11, StaticGate: 0x100200},
		{Name: "RepairPatrol", StateLabel: "Repair patrol", Class: 0x12, AckGroup: 7, StaticGate: 0x412},
		{Name: "RepairUnit", StateLabel: "Repairing", Class: 0x12, AckGroup: 6, StaticGate: 0x100200},
		{Name: "RepairUnitNoMove", StateLabel: "Repairing", Class: 0x18, AckGroup: 6, StaticGate: 0x200},
		{Name: "Resurrect", StateLabel: "Resurrecting", Class: 0x12, AckGroup: 11, StaticGate: 0x200},
		{Name: "SelfDestruct", StateLabel: "SELF DESTRUCT ENGAGED", Class: 0x00, AckGroup: 19, StaticGate: 0x40040},
		{Name: "SelfDestructFG", StateLabel: "SELF DESTRUCT ENGAGED", Class: 0x00, AckGroup: 19, StaticGate: 0x0},
		{Name: "SelfRepair", StateLabel: "Repairing", Class: 0x00, AckGroup: 19, StaticGate: 0x1000204},
		{Name: "Standby", StateLabel: "Standby", Class: 0x10, AckGroup: 15, StaticGate: 0x20000},
		{Name: "Standby_Mine", StateLabel: "Standby", Class: 0x10, AckGroup: 15, StaticGate: 0x1020000},
		{Name: "Standing_FireOrder", StateLabel: "Acknowledged", Class: 0x00, AckGroup: 19, StaticGate: 0x10060},
		{Name: "Standing_MoveOrder", StateLabel: "Acknowledged", Class: 0x00, AckGroup: 19, StaticGate: 0x10060},
		{Name: "Stop", StateLabel: "Stopping", Class: 0x00, AckGroup: 19, StaticGate: 0x0},
	}
	// Batch 3: 22
	b3 := []Descriptor{
		{Name: "Suppress", StateLabel: "Suppressing fire", Class: 0x08, AckGroup: 1, StaticGate: 0x410},
		{Name: "Teleport", StateLabel: "Teleporting", Class: 0x08, AckGroup: 9, StaticGate: 0x600},
		{Name: "VTOL_Evade", StateLabel: "Evading", Class: 0x00, AckGroup: 19, StaticGate: 0x0},
		{Name: "VTOL_Follow", StateLabel: "Guarding", Class: 0x02, AckGroup: 5, StaticGate: 0x200},
		{Name: "VTOL_GetRepaired", StateLabel: "Under repair", Class: 0x00, AckGroup: 19, StaticGate: 0x200},
		{Name: "VTOL_HelpBuild", StateLabel: "Nanolathing", Class: 0x08, AckGroup: 6, StaticGate: 0x100208},
		{Name: "VTOL_LandIfCan", StateLabel: "Seeking to land", Class: 0x00, AckGroup: 19, StaticGate: 0x400},
		{Name: "VTOL_Landing", StateLabel: "Landing", Class: 0x08, AckGroup: 14, StaticGate: 0x600},
		{Name: "VTOL_MobileBuild", StateLabel: "Nanolathing", Class: 0x03, AckGroup: 0, StaticGate: 0x100508},
		{Name: "VTOL_Move", StateLabel: "Moving", Class: 0x02, AckGroup: 14, StaticGate: 0x402},
		{Name: "VTOL_Patrol", StateLabel: "Patrolling", Class: 0x02, AckGroup: 7, StaticGate: 0x412},
		{Name: "VTOL_Pickup", StateLabel: "Loading", Class: 0x08, AckGroup: 8, StaticGate: 0x200},
		{Name: "VTOL_Reclaim", StateLabel: "Reclaiming", Class: 0x02, AckGroup: 11, StaticGate: 0x100800},
		{Name: "VTOL_ReclaimUnit", StateLabel: "Reclaiming", Class: 0x02, AckGroup: 11, StaticGate: 0x100200},
		{Name: "VTOL_RepairPatrol", StateLabel: "Repair patrol", Class: 0x02, AckGroup: 7, StaticGate: 0x412},
		{Name: "VTOL_RepairUnit", StateLabel: "Repairing", Class: 0x02, AckGroup: 6, StaticGate: 0x100200},
		{Name: "VTOL_SeekAttack", StateLabel: "Seeking to attack", Class: 0x00, AckGroup: 19, StaticGate: 0x600},
		{Name: "VTOL_SeekGuard", StateLabel: "Seeking to guard", Class: 0x00, AckGroup: 19, StaticGate: 0x600},
		{Name: "VTOL_Standby", StateLabel: "Standby", Class: 0x00, AckGroup: 15, StaticGate: 0x20000},
		{Name: "VTOL_Unload", StateLabel: "Unloading", Class: 0x08, AckGroup: 9, StaticGate: 0x400},
		{Name: "Wait", StateLabel: "Waiting", Class: 0x00, AckGroup: 19, StaticGate: 0x4},
		{Name: "WaitForAttack", StateLabel: "Waiting for attack", Class: 0x00, AckGroup: 19, StaticGate: 0x204},
	}
	// Batch 4: 1 empty sentinel
	b4 := []Descriptor{
		{Name: "", StateLabel: "", Class: 0, AckGroup: 0, StaticGate: 0},
	}
	all := make([]Descriptor, 0, 68)
	batches := [][]Descriptor{b1, b2, b3, b4}
	for _, batch := range batches {
		all = append(all, batch...)
		sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name }) // case-sensitive byte compare [04 §3.1]
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
