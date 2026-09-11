package mission

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Established: positional InitialMission verbs enter the command resolver
// [04 §3.6]. Its capability gates, mover test and repair-patrol choice remain
// observable at mission entry, before the ordinary queue pump takes over
// [04 R-ORD-02 §1].
func TestInitialMissionResolvesPositionalCommands(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		configure          func(*content.UnitDef)
		hostileTarget      bool
	}{
		{name: "factory move becomes rally", script: "m 100 200", want: "QMove", configure: func(d *content.UnitDef) { d.BMCode = 0 }},
		{name: "factory patrol becomes rally", script: "p 100 200 5", want: "QPatrol", configure: func(d *content.UnitDef) { d.BMCode = 0 }},
		{name: "repair patrol", script: "p 100 200 5", want: "RepairPatrol", configure: func(d *content.UnitDef) { d.CanReclamate = true }},
		{name: "air repair patrol", script: "p 100 200 5", want: "VTOL_RepairPatrol", configure: func(d *content.UnitDef) { d.CanFly, d.CanReclamate = true, true }},
		{name: "move capability", script: "m 100 200", configure: func(d *content.UnitDef) { d.CanMove = false }},
		{name: "patrol capability", script: "p 100 200 5", configure: func(d *content.UnitDef) { d.CanPatrol = false }},
		{name: "unload capability", script: "u 100 200", configure: func(d *content.UnitDef) { d.CanLoad = false }},
		{name: "guard capability", script: "g target", configure: func(d *content.UnitDef) { d.CanGuard = false }},
		{name: "hostile guard", script: "g target", hostileTarget: true},
		{name: "unarmed attack", script: "a 100 200"},
		{name: "attack capability", script: "a 100 200", configure: func(d *content.UnitDef) {
			d.CanAttack = false
			d.Weapon1Def = &content.WeaponDef{ID: 1}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := testDef("ARMCOM")
			if tc.configure != nil {
				tc.configure(def)
			}
			w := newMissionFixtureWorld(5, nil)
			h, err := w.Create(def, 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			u := atPlacement(w.Unit(h), 0)
			u.Flags |= units.ClassifierEligibleStatus
			var owner uint8
			if tc.hostileTarget {
				owner = 1
			}
			target, err := w.Create(testDef("ARMCK"), owner, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			atPlacement(w.Unit(target), 1)
			m := &Mission{Type: TypeCampaign, Units: []UnitPlacement{
				{UnitName: "ARMCOM", InitialMission: tc.script},
				{UnitName: "ARMCK", Ident: "target"},
			}}
			RunInitialMissionsWithCatalog(m, w, testInitialCatalog)
			if tc.want == "" {
				if queueLen(u) != 0 || u.Flags&units.ClassifierEligibleStatus == 0 {
					t.Fatalf("rejected script changed queue or selection eligibility: queue=%v flags=%#x", primaryNodes(u), u.Flags)
				}
				return
			}
			n := findNode(u, tc.want)
			if n == nil || n.Owner != h {
				t.Fatalf("script did not queue %s for its owner: %v", tc.want, primaryNodes(u))
			}
			if n.GoalX.Floor() != 100 || n.GoalZ.Floor() != 200 {
				t.Fatalf("resolved command lost its authored point: (%v, %v)", n.GoalX, n.GoalZ)
			}
		})
	}
}
