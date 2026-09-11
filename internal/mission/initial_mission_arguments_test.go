package mission

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The parser stores each floating argument at single precision before the
// coordinate/time scaling; positional triples carry zero Y [08 R-ENTRY-01 §6].
func TestInitialMissionPositionAndTimeArguments(t *testing.T) {
	for _, tc := range []struct {
		script, order string
		time          bool
	}{
		{"m 2048.0001 4096.0001", "Move_Ground", false},
		{"a 2048.0001 4096.0001", "Suppress", false},
		{"b ARMFAB 1 2048.0001 4096.0001", "MobileBuild", false},
		{"u 2048.0001 4096.0001", "Ground_Unload", false},
		{"p 2048.0001 4096.0001 0.7", "Patrol", true},
		{"w 0.7", "Wait", true},
	} {
		t.Run(tc.order, func(t *testing.T) {
			def := testDef("ARMCOM")
			def.Weapon1Def = &content.WeaponDef{ID: 1, Range: 100}
			w := newMissionFixtureWorld(5, nil)
			h, err := w.Create(def, 0, 0, 77<<16, 0)
			if err != nil {
				t.Fatal(err)
			}
			u := atPlacement(w.Unit(h), 0)
			m := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: tc.script}}}
			RunInitialMissionsWithCatalog(m, w, testInitialCatalog)
			n := findNode(u, tc.order)
			if n == nil {
				t.Fatalf("missing %s: %v", tc.order, primaryNodes(u))
			}
			if tc.order != "Wait" && (n.GoalX != 2048<<16 || n.GoalZ != 4096<<16 || n.GoalY != 0) {
				t.Errorf("goal=(%d,%d,%d), want single-precision X/Z and zero Y", n.GoalX, n.GoalY, n.GoalZ)
			}
			// 0.7 narrows below seven tenths; scaling that stored value by 30
			// then truncating yields 20, not the exact-decimal result 21.
			if tc.time && n.Param1 != 20 {
				t.Errorf("duration=%d, want 20 ticks", n.Param1)
			}
		})
	}
}

// Only bmcode exactly 1 creates a mover. Every other byte selects the
// building-build verb, even when that byte is nonzero [04 §3.6].
func TestInitialMissionBuildRequiresExactMoverClass(t *testing.T) {
	def := testBuildingDef("ARMFAB")
	def.BMCode = 2
	w := newMissionFixtureWorld(5, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := atPlacement(w.Unit(h), 0)
	m := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMFAB", InitialMission: "b ARMCK 3 700 800"}}}
	RunInitialMissionsWithCatalog(m, w, testInitialCatalog)
	n := findNode(u, "BuildingBuild")
	if n == nil || findNode(u, "MobileBuild") != nil {
		t.Fatalf("mover-less class selected mobile build: %v", primaryNodes(u))
	}
	if n.GoalX != 0 || n.GoalY != 0 || n.GoalZ != 0 {
		t.Errorf("building build retained an authored position: (%d,%d,%d)", n.GoalX, n.GoalY, n.GoalZ)
	}
}
