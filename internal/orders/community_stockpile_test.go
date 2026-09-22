package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
)

func TestCommunityStockpileCorruptExitPreservesOtherOrders(t *testing.T) {
	for _, tc := range []struct {
		name             string
		rules            Rules
		enabled, removes bool
	}{
		{"strict", StrictRules{}, true, false},
		{"disabled", CommunityRules{}, false, false},
		{"community", CommunityRules{}, true, true},
		{"modern", &ModernRules{}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, q, _ := armsFixture(t)
			q.Binding().Rules = tc.rules
			q.Binding().Community = community.Features{BuildWeaponSlotGuard: tc.enabled}
			q.Push(Lookup("Move"), Node{})
			bad := q.appendTail(Lookup("BuildWeapon"), Node{Param1: ^uint32(0), Param2: 1})
			next := q.appendTail(Lookup("BuildWeapon"), Node{Param1: 1, Param2: 1})
			code := buildWeaponHandler(u, bad, 0, 10)
			if tc.removes {
				if code != 7 || q.applySecondaryResultCode(bad, code, 10) {
					t.Fatal("corrupt exit must remove the record and stop this pass")
				}
				if q.LenPrimary() != 1 || q.LenSecondary() != 1 || q.Secondary()[0] != next || next.Param2 != 1 {
					t.Fatal("corrupt exit altered another order")
				}
			} else if code != 2 || q.LenSecondary() != 2 {
				t.Fatal("retail malformed-slot hold changed")
			}
		})
	}
}

func TestCommunityStockpileSentinelKeepsFreeRound(t *testing.T) {
	u, q, econ := armsFixture(t)
	q.Binding().Rules = CommunityRules{}
	q.Binding().Community.BuildWeaponSlotGuard = true
	q.CoalesceTail(Lookup("BuildWeapon"), Node{Param2: 3})
	q.Pump(u, 0)
	if u.Slots[0].Ammo != 3 || q.LenSecondary() != 0 {
		t.Fatal("readable zero-divisor sentinel was rejected")
	}
	for _, bucket := range econ.UnitBuckets(u.Handle) {
		if bucket.Requested != 0 || bucket.Accepted != 0 {
			t.Fatal("sentinel round consumed resources")
		}
	}
}
