package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// primeMobileBuildSlots stages the prior slot state: slot 0 autonomous with an
// acquired target, slot 1 held by an earlier order with its own target, slot 2
// autonomous and idle.
func primeMobileBuildSlots(u *units.Unit) {
	for idx := 0; idx < units.NumSlots; idx++ {
		u.Slots[idx] = units.Slot{Flags: units.SlotFlagEnabled | units.SlotFlagAutonomous}
	}
	u.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: 9}
	u.Slots[1].Flags &^= units.SlotFlagAutonomous
	u.Slots[1].Target = units.Target{Kind: units.TargetUnit, Unit: 8}
}

// TestMobileBuildPlacementTakesWeaponSlots locks `MobileBuild` phase 1's
// all-slot call on a legal placement [04 R-ORD-01 §5]. Retail releases all
// three slots, which clears the autonomy bit every acquisition reader tests
// [04 R-UNIT-06 §5][06 §3.2], so a Strict builder's weapons stop acquiring
// while it builds; the record destructor's all-slot return hands them back
// when the record ends [04 R-ORD-01 §7]. The ProTA 4.8 working-weapons switch
// swaps the verb at the same call, so autonomous slots keep their targets and
// a held slot is handed back (research/extensions/prota-engine.md "Weapons
// acquire targets while working"). Strict ignores the switch.
func TestMobileBuildPlacementTakesWeaponSlots(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   orders.Rules
		enabled bool
		inhibit bool
	}{
		{"strict-ignores-on", orders.StrictRules{}, true, false},
		{"community-off", orders.CommunityRules{}, false, false},
		{"community-on", orders.CommunityRules{}, true, true},
		{"modern-on", &orders.ModernRules{}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b, n := approachFixtureAt(t, 10, 10, world.CellToWorld(2), world.CellToWorld(2))
			q := orders.QueueForUnit(b)
			q.Binding().Rules = tc.rules
			q.Binding().Community = community.Features{WorkingWeaponsAutonomous: tc.enabled}
			n.Phase, n.DynamicGate, n.Deadline = uint8(State2), 0, -1
			primeMobileBuildSlots(b)

			if code := s.mobilePlacementVisit(b, n, 1); code != 1 || n.Target == 0 {
				t.Fatalf("legal placement returned %d with target %d, want advance with a nanoframe", code, n.Target)
			}
			s0, s1, s2 := b.Slots[0], b.Slots[1], b.Slots[2]
			if tc.inhibit {
				if !s0.IsAutonomous() || s0.Target.Unit != 9 || !s2.IsAutonomous() {
					t.Fatalf("autonomous slots were taken: %+v %+v", s0, s2)
				}
				if !s1.IsAutonomous() || s1.Target.Kind != units.TargetNone {
					t.Fatalf("held slot not handed back with its target cleared: %+v", s1)
				}
			} else {
				if s0.IsAutonomous() || s0.Target.Kind != units.TargetNone || s2.IsAutonomous() {
					t.Fatalf("release left slots acquiring while placing: %+v %+v", s0, s2)
				}
				if s1.IsAutonomous() || s1.Target.Unit != 8 {
					t.Fatalf("release touched the held slot: %+v", s1)
				}
			}

			// The record ends: the destructor returns every slot to autonomy.
			s.removeHead(b, n)
			if q.LenPrimary() != 0 {
				t.Fatalf("record not removed")
			}
			for idx := 0; idx < units.NumSlots; idx++ {
				if !b.Slots[idx].IsAutonomous() {
					t.Fatalf("slot %d not handed back after the record ended: %+v", idx, b.Slots[idx])
				}
			}
		})
	}
}

// TestMobileBuildSlotCallFollowsTheLegalPlacementCheck locks the call's
// position: an illegal placement takes the blocked ladder before the legal
// arm, so it makes no slot call [04 R-ORD-01 §5]. The aircraft row shares this
// visit but makes no placement-time call, because its takeoff preamble already
// released the slots [04 R-ORD-02 §2].
func TestMobileBuildSlotCallFollowsTheLegalPlacementCheck(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		s, b, _, n := siteYieldFixture(t)
		s.Rules = StrictRules{}
		primeMobileBuildSlots(b)
		want := b.Slots
		if code := s.mobilePlacementVisit(b, n, 1); code != 2 || n.Target != 0 {
			t.Fatalf("blocked placement returned %d with target %d, want a hold", code, n.Target)
		}
		if b.Slots != want {
			t.Fatalf("blocked placement changed slots: %+v, want %+v", b.Slots, want)
		}
	})
	t.Run("aircraft", func(t *testing.T) {
		s, b, n := approachFixtureAt(t, 10, 10, world.CellToWorld(2), world.CellToWorld(2))
		n.ID = vtolMobileBuildRow
		n.Phase, n.DynamicGate, n.Deadline = uint8(State2), 0, -1
		primeMobileBuildSlots(b)
		want := b.Slots
		if code := s.mobilePlacementVisit(b, n, 1); code != 1 || n.Target == 0 {
			t.Fatalf("legal placement returned %d with target %d, want advance", code, n.Target)
		}
		if b.Slots != want {
			t.Fatalf("aircraft placement changed slots: %+v, want %+v", b.Slots, want)
		}
	})
}
