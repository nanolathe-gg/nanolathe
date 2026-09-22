package orders

import "github.com/nanolathe-gg/nanolathe/internal/units"

func (StrictRules) RejectStockpileOrder(*units.Unit, *Node) bool { return false }

// RejectStockpileOrder selects the corrupt-order result only for an invalid
// slot. A nil slot weapon is our readable no-weapon sentinel: its zero reload
// must retain the retail simulation path (community-patch-engine.md CP-DMG-5).
func (CommunityRules) RejectStockpileOrder(u *units.Unit, n *Node) bool {
	b := bindingOfUnit(u)
	if b == nil || !b.Community.BuildWeaponSlotGuard {
		return false
	}
	return n == nil || n.Param1 >= units.NumSlots
}
