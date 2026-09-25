//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestProTAWorkingCommanderKeepsItsWeapons runs the ProTA 4.8 working-weapons
// switch end to end on authored content: a Core commander repairing or
// reclaiming an unarmed friendly constructor, with an unarmed Arm constructor
// inside laser range. Retail's work handlers release all three slots, so the
// commander's weapons stay silent while the order stands [04 R-ORD-01 §5];
// with the switch on, the same call hands them back and the ordinary
// autonomous scan acquires and fires [06 §3.2]
// (research/extensions/prota-engine.md "Weapons acquire targets while
// working"). The repair works in reach; the reclaim's verb is its phase 0, so
// the case does not need the approach to finish. `MobileBuild` is absent: the
// ground placement visit does not yet make retail's release call
// (DESIGN_COMMUNITY_PATCH §4.7).
func TestProTAWorkingCommanderKeepsItsWeapons(t *testing.T) {
	f := loadRetailFixture(t)
	for _, work := range []string{"RepairUnit", "ReclaimUnit"} {
		for _, on := range []bool{false, true} {
			name := work + "/off"
			if on {
				name = work + "/on"
			}
			t.Run(name, func(t *testing.T) {
				s := f.session(t)
				enabled := on
				s.CommunitySources = CommunitySources{Content: []community.Overrides{{WorkingWeaponsAutonomous: &enabled}}}
				if err := s.SetRules(CommunityRuleSetName); err != nil {
					t.Fatal(err)
				}
				stepRetail(s, 2)
				for player := range s.AI {
					if s.AI[player] != nil {
						for i := range s.AI[player].Deadlines {
							s.AI[player].Deadlines[i] = ^uint32(0)
						}
					}
				}
				// The authored commander, so its composition is the ordinary one.
				commander := retailUnit(s, 1, retailCORE)
				if commander == nil {
					t.Fatal("authored Core commander absent")
				}
				x, z := commander.X, commander.Z
				friend := placeCompleteRetailUnit(t, s, "CORCK", 1, x.Add(numeric.FixedFromInt(48)), z)
				enemy := placeCompleteRetailUnit(t, s, "ARMLAB", 0, x, z.Add(numeric.FixedFromInt(150)))
				// Keep the D-gun out of it, as the laser test does.
				commander.SlotAt(2).Flags &^= units.SlotFlagEnabled
				friend.Health = 1
				s.bindOrderQueue(commander)
				q := orders.QueueOfUnit(commander)
				q.Push(orders.Lookup(work), orders.Node{Owner: commander.Handle, Target: friend.Handle})
				id := orders.Lookup(work)
				before := enemy.Health
				working := 0
				for i := 0; i < 600; i++ {
					commander.Flags &^= units.StandingFieldMask << units.StandingMoveShift
					s.Econ.Players[1].Stock[economy.Energy] = 100
					friend.Health = 1 // keep the repair from finishing
					s.Step(s.Clock.ScaledAnchor + 1)
					if head := q.Head(); head == nil || head.ID != id {
						break
					}
					working++
				}
				if working < 300 {
					t.Fatalf("the %s order stood only %d ticks", work, working)
				}
				hit := enemy.Health < before
				if hit != on {
					t.Fatalf("enemy hit=%v while the commander worked, want %v (health %d/%d, laser %+v)", hit, on, enemy.Health, before, commander.SlotAt(0))
				}
			})
		}
	}
}
