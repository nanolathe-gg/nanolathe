package mission

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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

// Established prefix continuation and caller seeds [04 §3.6]. Coordinate
// zeros after failure assert the bounded host policy, not retail temporaries.
func TestInitialMissionArgumentContinuation(t *testing.T) {
	for _, tc := range []struct {
		script, order string
		x, z          numeric.Fixed
		p1, p2        uint32
	}{
		{script: "b ARMCK 2.5 7", order: "MobileBuild", x: 1 << 15, z: 7 << 16, p2: 2},
		{script: "b ARMCK-3 8 9", order: "MobileBuild", x: 8 << 16, z: 9 << 16, p2: 0xfffffffd},
		{script: "b ARMCK bad 8 9", order: "MobileBuild", p2: 1},
		{script: "bw bad", order: "BuildWeapon", p2: 1},
		{script: "bw 4294967297tail", order: "BuildWeapon", p2: 1},
		{script: "bw -4294967297", order: "BuildWeapon", p2: 0xffffffff},
		{script: "bw 2147483648", order: "BuildWeapon", p2: 0x80000000},
		{script: "bw 0x10", order: "BuildWeapon"},
		{script: "w bad 7", order: "Wait"},
		{script: "w 0.7-4294967297", order: "Wait", p1: 20, p2: 0xffffffff},
		{script: "w 2e+ 3", order: "Wait", p1: 60, p2: 3},
		{script: "w 2D2 3", order: "Wait", p1: 60},
		{script: "m 1.5.25", order: "Move_Ground", x: 3 << 15, z: 1 << 14},
		{script: "a 1.5.25", order: "Suppress", x: 3 << 15, z: 1 << 14},
		{script: "m bad 9", order: "Move_Ground"},
		{script: "u bad 9", order: "Ground_Unload"},
		{script: "p 4tail 9 8", order: "Patrol", x: 4 << 16},
	} {
		t.Run(tc.script, func(t *testing.T) {
			def := testDef("ARMCOM")
			def.Weapon1Def = &content.WeaponDef{ID: 1, Range: 100}
			w := newMissionFixtureWorld(5, nil)
			h, err := w.Create(def, 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			u := giveStockpileSlot(atPlacement(w.Unit(h), 0))
			m := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: tc.script}}}
			RunInitialMissionsWithCatalog(m, w, testInitialCatalog)
			n := findNode(u, tc.order)
			if n == nil {
				t.Fatalf("missing %s", tc.order)
			}
			if n.GoalX != tc.x || n.GoalZ != tc.z || n.Param2 != tc.p2 {
				t.Errorf("goal=(%v,%v) count=%d, want (%v,%v) count=%d", n.GoalX, n.GoalZ, n.Param2, tc.x, tc.z, tc.p2)
			}
			if tc.order == "MobileBuild" {
				if n.BuildDefKey != content.CanonicalKey("ARMCK") {
					t.Errorf("product=%q, want ARMCK", n.BuildDefKey)
				}
			} else if n.Param1 != tc.p1 {
				t.Errorf("parameter=%d, want %d", n.Param1, tc.p1)
			}
		})
	}
}

func TestInitialMissionStandingScanRetainsLaterSeed(t *testing.T) {
	for _, tc := range []struct {
		script string
		move   uint32
	}{
		{"o bad 0", 1},
		{"o 2.5 0", 2},
		{"o 4294967298tail 0", 2},
	} {
		u := &units.Unit{Flags: 1<<units.StandingMoveShift | 3<<units.StandingFireShift}
		dispatchToken(tc.script, &interpCtx{unit: u})
		move := u.Flags >> units.StandingMoveShift & units.StandingFieldMask
		fire := u.Flags >> units.StandingFireShift & units.StandingFieldMask
		if move != tc.move || fire != 3 {
			t.Errorf("%q: move/fire=(%d,%d), want (%d,3)", tc.script, move, fire, tc.move)
		}
	}
}

// Names stop at the excluded byte; only wa excludes underscore [fmt ota].
func TestInitialMissionNamePrefixes(t *testing.T) {
	for _, tc := range []struct {
		script, order string
		target        pool.Handle
	}{
		{"g target_suffix-extra", "Follow_Ground", 3},
		{"g target-extra", "Follow_Ground", 2},
		{"i target_suffix-extra", "", 3},
		{"wa target_suffix", "WaitForAttack", 2},
		{"wa _target", "WaitForAttack", 1},
		{"a ARMCK-extra", "AttackUType", 0},
	} {
		t.Run(tc.script, func(t *testing.T) {
			actor := &units.Unit{Handle: 1, Alive: true, Def: testDef("ARMCOM")}
			target := &units.Unit{Handle: 2, Alive: true, Def: testDef("ARMCK")}
			suffixed := &units.Unit{Handle: 3, Alive: true, Def: testDef("ARMCK")}
			var pairs []attachPair
			ctx := &interpCtx{
				unit: actor, catalog: testInitialCatalog,
				createdSparse: []*units.Unit{actor, target, suffixed},
				identMap:      map[string]int{"target": 1, "target_suffix": 2},
				attachPairs:   &pairs,
			}
			dispatchToken(tc.script, ctx)
			if tc.order == "" {
				if len(pairs) != 1 || pairs[0].carrierHandle != tc.target {
					t.Fatalf("attach=%v, want carrier %d", pairs, tc.target)
				}
				return
			}
			n := findNode(actor, tc.order)
			if n == nil || n.Target != tc.target {
				t.Fatalf("%s node=%v, want target %d", tc.order, n, tc.target)
			}
			if tc.order == "AttackUType" && n.BuildDefKey != content.CanonicalKey("ARMCK") {
				t.Fatalf("attack product=%q, want ARMCK", n.BuildDefKey)
			}
		})
	}
}

// These binary32 boundaries and incomplete-exponent rules are established;
// this does not claim every adversarial decimal rounds identically [fmt ota].
func TestInitialMissionFloatScanBoundary(t *testing.T) {
	for _, tc := range []struct {
		text string
		want float64
		ok   bool
	}{
		{"16777217", 16777216, true},
		{"2048.0001tail", 2048, true},
		{"1e1000", math.Inf(1), true},
		{"-1e1000", math.Inf(-1), true},
		{"2e+", 2, true},
		{"2D2", 2, true},
		{"inf", 7, false},
		{"nan", 7, false},
		{".e2", 7, false},
	} {
		scan := missionArgScanner{text: tc.text}
		value := float32(7)
		ok := scan.float(&value)
		if ok != tc.ok || float64(value) != tc.want {
			t.Errorf("%q: (%v,%v), want (%v,%v)", tc.text, value, ok, tc.want, tc.ok)
		}
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
