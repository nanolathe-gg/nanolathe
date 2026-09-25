package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// protaRuleCases pairs each reserved rule layer with a table value. Strict
// ignores the table; Community and Modern read it (DESIGN_COMMUNITY_PATCH §4.7).
var protaRuleCases = []struct {
	name    string
	rules   Rules
	enabled bool
	applies bool
}{
	{"strict-ignores-on", StrictRules{}, true, false},
	{"community-off", CommunityRules{}, false, false},
	{"community-on", CommunityRules{}, true, true},
	{"modern-on", &ModernRules{}, true, true},
}

// Prior slot state for the work tests: slot 0 autonomous with an acquired
// target, slot 1 held by an earlier order with its own target, slot 2
// autonomous and idle.
func primeWorkSlots(u *units.Unit) {
	for idx := 0; idx < units.NumSlots; idx++ {
		u.Slots[idx] = units.Slot{Flags: units.SlotFlagEnabled | units.SlotFlagAutonomous}
	}
	u.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: 9}
	u.Slots[1].Flags &^= units.SlotFlagAutonomous
	u.Slots[1].Target = units.Target{Kind: units.TargetUnit, Unit: 8}
}

// TestProTAWorkingWeaponsSwapTheVerbAtFourWorkSites locks the ProTA 4.8
// working-weapons patch at the four sites this package owns: the same call
// with the inhibit verb, so held slots come back (target cleared) and
// autonomous slots keep their acquired targets. Retail's release takes every
// autonomous slot and leaves the held one alone [04 R-ORD-01 §5]
// [04 R-ORD-01 §7] (research/extensions/prota-engine.md "Weapons acquire
// targets while working"). `RepairUnitNoMove` and the VTOL preamble keep the
// release under every rule set.
func TestProTAWorkingWeaponsSwapTheVerbAtFourWorkSites(t *testing.T) {
	sites := []struct {
		name    string
		patched bool
		visit   func(u, target *units.Unit) Code
	}{
		{"RepairUnit phase 1", true, func(u, target *units.Unit) Code {
			return repairUnitHandler(u, &Node{ID: Lookup("RepairUnit"), Owner: u.Handle, Target: target.Handle, Phase: 1}, 0, 1)
		}},
		{"HelpBuild phase 1", true, func(u, target *units.Unit) Code {
			target.Remaining = 0.5
			return helpBuildHandler(u, &Node{ID: Lookup("HelpBuild"), Owner: u.Handle, Target: target.Handle, Phase: 1}, 0, 1)
		}},
		{"Capture phase 0", true, func(u, target *units.Unit) Code {
			return captureHandler(u, &Node{ID: Lookup("Capture"), Owner: u.Handle, Target: target.Handle}, 0, 1)
		}},
		{"ReclaimUnit phase 0", true, func(u, target *units.Unit) Code {
			return GroundUnitReclaimSetup(u, &Node{ID: Lookup("ReclaimUnit"), Owner: u.Handle, Target: target.Handle}, 0, 1)
		}},
		{"RepairUnitNoMove phase 0", false, func(u, target *units.Unit) Code {
			return repairUnitNoMoveHandler(u, &Node{ID: Lookup("RepairUnitNoMove"), Owner: u.Handle, Target: target.Handle}, 0, 1)
		}},
		{"VTOL work preamble", false, func(u, _ *units.Unit) Code {
			u.Def.CanFly = true
			return airWorkPreamble(u, &Node{ID: Lookup("VTOL_Reclaim"), Owner: u.Handle}, "Reclaiming")
		}},
	}
	for _, site := range sites {
		for _, tc := range protaRuleCases {
			t.Run(site.name+"/"+tc.name, func(t *testing.T) {
				q, builder, target := workFixture()
				q.Binding().Rules = tc.rules
				q.Binding().Community = community.Features{WorkingWeaponsAutonomous: tc.enabled}
				primeWorkSlots(builder)
				if code := site.visit(builder, target); code != 1 {
					t.Fatalf("visit returned %d, want advance", code)
				}
				s0, s1, s2 := builder.Slots[0], builder.Slots[1], builder.Slots[2]
				if site.patched && tc.applies {
					if !s0.IsAutonomous() || s0.Target.Unit != 9 || !s2.IsAutonomous() {
						t.Fatalf("autonomous slots were taken: %+v %+v", s0, s2)
					}
					if !s1.IsAutonomous() || s1.Target.Kind != units.TargetNone {
						t.Fatalf("held slot not handed back with its target cleared: %+v", s1)
					}
					return
				}
				if s0.IsAutonomous() || s0.Target.Kind != units.TargetNone || s2.IsAutonomous() {
					t.Fatalf("retail release left slots autonomous: %+v %+v", s0, s2)
				}
				if s1.IsAutonomous() || s1.Target.Unit != 8 {
					t.Fatalf("retail release touched the held slot: %+v", s1)
				}
			})
		}
	}
}

// TestProTAAttackChaseTakesOneSlot locks `Attack_Chase` phase 1's take: retail
// releases slots 0 and 2 before binding slot p1; the ProTA 4.8 package releases
// only slot 2 when p1 > 1 (signed), else slot 0 [04 R-ORD-01 §3]
// (research/extensions/prota-engine.md, the two related weapon-slot patches).
// Phase 3 keeps retail's pair in every mode.
func TestProTAAttackChaseTakesOneSlot(t *testing.T) {
	for _, tc := range protaRuleCases {
		for _, p1 := range []uint32{0, 1, 2, 0xFFFFFFFF} {
			q, u, target := workFixture()
			q.Binding().Rules = tc.rules
			q.Binding().Community = community.Features{AttackSingleSlotTake: tc.enabled}
			q.Binding().Weapons = &WeaponAdapter{CanEngage: func(*units.Unit, pool.Handle, int) bool { return true }}
			for idx := 0; idx < units.NumSlots; idx++ {
				u.Slots[idx] = units.Slot{Flags: units.SlotFlagEnabled | units.SlotFlagAutonomous, Target: units.Target{Kind: units.TargetUnit, Unit: 9}}
			}
			n := &Node{ID: Lookup("Attack_Chase"), Owner: u.Handle, Target: target.Handle, Phase: 1, Param1: p1}
			if code := attackChaseHandler(u, n, 0, 1); code != 2 {
				t.Fatalf("%s p1=%d: phase 1 returned %d, want hold", tc.name, p1, code)
			}
			taken := [units.NumSlots]bool{true, false, true}
			if tc.applies {
				// 0xFFFFFFFF is -1 signed, so it takes slot 0.
				taken = [units.NumSlots]bool{p1 != 2, false, p1 == 2}
			}
			for idx := 0; idx < units.NumSlots; idx++ {
				if got := !u.Slots[idx].IsAutonomous(); got != taken[idx] {
					t.Fatalf("%s p1=%d: slot %d taken=%v, want %v", tc.name, p1, idx, got, taken[idx])
				}
			}
			if bound := int(p1); bound < units.NumSlots && u.Slots[bound].Target.Unit != target.Handle {
				t.Fatalf("%s p1=%d: slot p1 not bound to the target: %+v", tc.name, p1, u.Slots[bound])
			}

			// Phase 3's shot-gate arm releases slots 0 and 2 whatever the rule.
			for idx := 0; idx < units.NumSlots; idx++ {
				u.Slots[idx].Flags |= units.SlotFlagAutonomous
			}
			n.Phase = 3
			attackChaseHandler(u, n, 0, 2)
			if u.Slots[0].IsAutonomous() || !u.Slots[1].IsAutonomous() || u.Slots[2].IsAutonomous() {
				t.Fatalf("%s p1=%d: phase 3 changed retail's slots 0 and 2 take", tc.name, p1)
			}
		}
	}
}

// TestProTASuppressDGunTakesSlotTwoOnly locks `Suppress` phase 1: with p1 = 2
// retail releases all three slots, the ProTA 4.8 package only slot 2; the
// p1 ≠ 2 arm releases slots 0 and 1 in every mode [04 R-ORD-01 §3].
func TestProTASuppressDGunTakesSlotTwoOnly(t *testing.T) {
	for _, tc := range protaRuleCases {
		for _, p1 := range []uint32{0, 2} {
			q, u, _ := workFixture()
			q.Binding().Rules = tc.rules
			q.Binding().Community = community.Features{AttackSingleSlotTake: tc.enabled}
			for idx := 0; idx < units.NumSlots; idx++ {
				u.Slots[idx] = units.Slot{Flags: units.SlotFlagEnabled | units.SlotFlagAutonomous}
			}
			n := &Node{ID: Lookup("Suppress"), Owner: u.Handle, Phase: 1, Param1: p1, GoalX: u.X, GoalZ: u.Z}
			if code := suppressHandler(u, n, 0, 1); code != 1 {
				t.Fatalf("%s p1=%d: phase 1 returned %d, want advance", tc.name, p1, code)
			}
			taken := [units.NumSlots]bool{true, true, false}
			if p1 == 2 {
				taken = [units.NumSlots]bool{!tc.applies, !tc.applies, true}
			}
			for idx := 0; idx < units.NumSlots; idx++ {
				if got := !u.Slots[idx].IsAutonomous(); got != taken[idx] {
					t.Fatalf("%s p1=%d: slot %d taken=%v, want %v", tc.name, p1, idx, got, taken[idx])
				}
			}
		}
	}
}

// TestProTAResurrectionFailureText locks the phase-3 caption: retail's
// doubled-s `Ressurection failed` unless the ProTA 4.8 text switch applies,
// when both failure arms read `Resurrection failed` with the same status and
// abandon [04 R-ORD-01 §5] (research/extensions/prota-engine.md "Resurrection
// failure text").
func TestProTAResurrectionFailureText(t *testing.T) {
	corpse := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "notaunit_dead"},
		Height:           20, FootprintX: 1, FootprintZ: 1, Reclaimable: true,
	}
	for _, tc := range protaRuleCases {
		f := newFeatureWorkFixture(t, []*content.FeatureDef{corpse}, 4, 5)
		f.builder.X, f.builder.Z = world.CellToWorld(4), world.CellToWorld(5)
		bindResurrectSeam(f, nil, nil) // the catalogue resolves nothing
		f.q.Binding().Rules = tc.rules
		f.q.Binding().Community = community.Features{ResurrectionTextFix: tc.enabled}
		var captions []string
		var statuses []uint8
		f.q.Binding().Presentation = &PresentationAdapter{
			Status: func(_ *units.Unit, status uint8, text string) bool {
				captions, statuses = append(captions, text), append(statuses, status)
				return true
			},
		}
		f.q.Push(Lookup("Resurrect"), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(5), GoalSupplied: true})
		f.q.Pump(f.builder, 0)
		f.q.Head().Satisfied |= gateArrived
		for tick := uint32(1); tick <= 50 && f.q.LenPrimary() > 0; tick++ {
			f.q.Pump(f.builder, tick)
		}
		want := "Ressurection failed"
		if tc.applies {
			want = "Resurrection failed"
		}
		if f.q.LenPrimary() != 0 || len(captions) == 0 || captions[len(captions)-1] != want || statuses[len(statuses)-1] != statusCant {
			t.Fatalf("%s: queue %d, captions %q statuses %v; want abandon with status %d %q", tc.name, f.q.LenPrimary(), captions, statuses, statusCant, want)
		}
	}
}
