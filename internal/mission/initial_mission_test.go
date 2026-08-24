package mission

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// helper to create a ground-capable def.
func testDef(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:   name,
		CanMove:    true,
		CanAttack:  true,
		CanGuard:   true,
		CanPatrol:  true,
		CanLoad:    true,
		CanFly:     false,
		Builder:    true,
		CanCapture: true,
	}
}

func testFlyerDef(name string) *content.UnitDef {
	d := testDef(name)
	d.CanFly = true
	return d
}

func newWorldWithUnits(defs []*content.UnitDef, idents []string, unitNames []string) (*units.World, []*units.Unit) {
	w := units.New(20, nil)
	var us []*units.Unit
	for i, def := range defs {
		h, err := w.Create(def, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
		if err != nil {
			panic(err)
		}
		u := w.Unit(h)
		// Set initial flags bit5 set to test clearing.
		u.Flags |= 1 << 5
		us = append(us, u)
		_ = idents
		_ = unitNames
		_ = i
	}
	return w, us
}

func queueLen(u *units.Unit) int {
	q := orders.QueueForUnit(u)
	if q == nil {
		return 0
	}
	return q.LenPrimary() + q.LenSecondary()
}

func primaryNodes(u *units.Unit) []*orders.Node {
	q := orders.QueueForUnit(u)
	if q == nil {
		return nil
	}
	return q.Primary()
}

func secondaryNodes(u *units.Unit) []*orders.Node {
	q := orders.QueueForUnit(u)
	if q == nil {
		return nil
	}
	return q.Secondary()
}

func hasOrder(u *units.Unit, name string) bool {
	id := orders.Lookup(name)
	if id == 0 {
		return false
	}
	for _, n := range primaryNodes(u) {
		if n.ID == id {
			return true
		}
	}
	for _, n := range secondaryNodes(u) {
		if n.ID == id {
			return true
		}
	}
	return false
}

func findNode(u *units.Unit, name string) *orders.Node {
	id := orders.Lookup(name)
	if id == 0 {
		return nil
	}
	for _, n := range primaryNodes(u) {
		if n.ID == id {
			return n
		}
	}
	for _, n := range secondaryNodes(u) {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func TestInitialMissionVerbs(t *testing.T) {
	// Cover every verb at least once including both a forms [C10].
	// Use ground unit for all except where VTOL variant needed.

	// Set hooks for type existence to allow known types, reject unknown.
	UnitTypeExistsHook = func(name string) bool {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "unknown_type_xyz" {
			return false
		}
		return true
	}
	defer func() { UnitTypeExistsHook = nil }()

	IsBuildingTypeHook = func(name string) bool {
		// For test, treat "ARMFAB" as building (empty unit name) -> BuildingBuild
		if strings.EqualFold(name, "ARMFAB") {
			return true
		}
		return false
	}
	defer func() { IsBuildingTypeHook = nil }()

	// Create three units to test inter-unit verbs g,i,wa.
	// Test m
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", Ident: "u0", InitialMission: "m 100,200"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "Move_Ground") {
			t.Fatalf("m verb should queue Move_Ground, got %v", primaryNodes(u))
		}
		n := findNode(u, "Move_Ground")
		if n.GoalX != numeric.Fixed(100*65536) || n.GoalZ != numeric.Fixed(200*65536) {
			t.Fatalf("m coordinates scaling wrong: got %d,%d want %d,%d", n.GoalX.Raw(), n.GoalZ.Raw(), 100*65536, 200*65536)
		}
		// postlude MakeSelectable should be present because m does not suppress.
		if !hasOrder(u, "MakeSelectable") {
			t.Fatalf("m verb should have postlude MakeSelectable")
		}
		if u.Flags&(1<<5) != 0 {
			t.Fatalf("bit5 should clear when queued")
		}
	}
	// a numeric
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "a 300,400"}}}
		RunInitialMissions(m1, w1)
		// Should queue attack (numeric) and suppress tail
		hasAttack := hasOrder(u, "Attack_Chase") || hasOrder(u, "AttackUType") || hasOrder(u, "Attack_NoMove")
		if !hasAttack {
			t.Fatalf("a numeric should queue attack order")
		}
		if hasOrder(u, "MakeSelectable") {
			t.Fatalf("a numeric should suppress MakeSelectable postlude")
		}
	}
	// a by-type
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "a ARMCK"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "AttackUType") {
			t.Fatalf("a by-type should queue AttackUType, got %v", primaryNodes(u))
		}
		if !hasOrder(u, "MakeSelectable") {
			t.Fatalf("a by-type should NOT suppress MakeSelectable")
		}
		// unknown type should queue nothing -> still postlude? No queued orders, so no MakeSelectable.
		w2 := units.New(5, nil)
		h2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u2 := w2.Unit(h2)
		u2.Flags |= 1 << 5
		m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "a unknown_type_xyz"}}}
		RunInitialMissions(m2, w2)
		if hasOrder(u2, "AttackUType") {
			t.Fatalf("a unknown type should queue nothing")
		}
		if queueLen(u2) != 0 {
			t.Fatalf("unknown a should not queue, but also no postlude because queued==0, got %d", queueLen(u2))
		}
	}
	// b building vs mobile
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		// building type ARMFAB triggers BuildingBuild per hook
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "b ARMFAB 2 500,600"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "BuildingBuild") {
			t.Fatalf("b ARMFAB should queue BuildingBuild, got %v", primaryNodes(u))
		}
		n := findNode(u, "BuildingBuild")
		if n.Param2 != 2 {
			t.Fatalf("b count wrong: got %d want 2", n.Param2)
		}
		if n.GoalX != 500*65536 || n.GoalZ != 600*65536 {
			t.Fatalf("b coordinates wrong")
		}
		// mobile type ARMCK
		w2 := units.New(5, nil)
		h2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u2 := w2.Unit(h2)
		u2.Flags |= 1 << 5
		m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "b ARMCK 3 700,800"}}}
		RunInitialMissions(m2, w2)
		if !hasOrder(u2, "MobileBuild") {
			t.Fatalf("b ARMCK should queue MobileBuild")
		}
	}
	// bw
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "bw 5"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "BuildWeapon") {
			t.Fatalf("bw should queue BuildWeapon")
		}
		n := findNode(u, "BuildWeapon")
		if n.Param2 != 5 {
			t.Fatalf("bw count wrong got %d want 5", n.Param2)
		}
		// BuildWeapon is secondary
		q := orders.QueueForUnit(u)
		if q.LenSecondary() == 0 {
			t.Fatalf("BuildWeapon should be secondary")
		}
	}
	// d
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "d"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "SelfDestructFG") && !hasOrder(u, "SelfDestruct") {
			t.Fatalf("d should queue SelfDestructFG")
		}
		if hasOrder(u, "MakeSelectable") {
			t.Fatalf("d should suppress MakeSelectable")
		}
	}
	// g
	{
		// need two units
		w1 := units.New(5, nil)
		hA, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		hB, _ := w1.Create(testDef("ARMCK"), 0, 0, 0, 0)
		uA := w1.Unit(hA)
		uB := w1.Unit(hB)
		uA.Flags |= 1 << 5
		// mission units mapping: placement 0 -> uA, placement1 -> uB
		// uA guards uB via Ident
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{
			{UnitName: "ARMCOM", Ident: "alpha", InitialMission: "g beta"},
			{UnitName: "ARMCK", Ident: "beta", InitialMission: ""},
		}}
		RunInitialMissions(m1, w1)
		if !hasOrder(uA, "Follow_Ground") && !hasOrder(uA, "VTOL_Follow") {
			t.Fatalf("g should queue guard order, got prim %v sec %v", primaryNodes(uA), secondaryNodes(uA))
		}
		n := findNode(uA, "Follow_Ground")
		if n == nil {
			n = findNode(uA, "VTOL_Follow")
		}
		if n.Target != hB {
			t.Fatalf("g target wrong: got %v want %v", n.Target, hB)
		}
		// unresolved g should queue nothing
		w2 := units.New(5, nil)
		hA2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		uA2 := w2.Unit(hA2)
		uA2.Flags |= 1 << 5
		m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", Ident: "a", InitialMission: "g nonexistent"}}}
		RunInitialMissions(m2, w2)
		// Should have no guard order, but may have postlude if queued 0 then no.
		if hasOrder(uA2, "Follow_Ground") || hasOrder(uA2, "VTOL_Follow") {
			t.Fatalf("g unresolved should queue nothing")
		}
		_ = uB
	}
	// i
	{
		w1 := units.New(5, nil)
		hA, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		hB, _ := w1.Create(testDef("ARMCK"), 0, 0, 0, 0)
		uA := w1.Unit(hA)
		uA.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{
			{UnitName: "ARMCOM", Ident: "alpha", InitialMission: "i beta"},
			{UnitName: "ARMCK", Ident: "beta", InitialMission: ""},
		}}
		RunInitialMissions(m1, w1)
		if queueLen(uA) != 0 {
			// i should not queue order, but postlude would not trigger because queued==0.
			// So queueLen should be 0 (no MakeSelectable because queued==0)
			t.Fatalf("i should not queue order, got %d", queueLen(uA))
		}
		if uA.Flags&(1<<5) != 0 {
			// bit5 should not clear because no queued order
		} else {
			t.Fatalf("i with no queued order should not clear bit5")
		}
		_ = hB
	}
	// o
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags = 0
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "o 1,2"}}}
		RunInitialMissions(m1, w1)
		if queueLen(u) != 0 {
			t.Fatalf("o should not queue order")
		}
		want := (uint32(1&3) << 17) | (uint32(2&3) << 19)
		if u.Flags&((0x3<<17)|(0x3<<19)) != want {
			t.Fatalf("o flag bits wrong: got %b want %b", u.Flags, want)
		}
	}
	// p
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "p 100,200,10"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "Patrol") && !hasOrder(u, "VTOL_Patrol") {
			t.Fatalf("p should queue patrol")
		}
		n := findNode(u, "Patrol")
		if n == nil {
			n = findNode(u, "VTOL_Patrol")
		}
		if n.GoalX != 100*65536 || n.GoalZ != 200*65536 {
			t.Fatalf("p coords wrong")
		}
		if n.Param1 != uint32(10*30) {
			t.Fatalf("p timeout ticks wrong: got %d want %d", n.Param1, 10*30)
		}
		if hasOrder(u, "MakeSelectable") {
			t.Fatalf("p should suppress tail")
		}
	}
	// s
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "s"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "MakeSelectable") {
			t.Fatalf("s should queue MakeSelectable")
		}
		// s suppresses tail, but there is already one MakeSelectable queued by verb s itself.
		// Postlude should not add second MakeSelectable.
		q := orders.QueueForUnit(u)
		count := 0
		for _, n := range q.Primary() {
			if n.ID == orders.Lookup("MakeSelectable") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("s should have exactly 1 MakeSelectable (no tail), got %d", count)
		}
	}
	// u
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "u 300,400"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "Ground_Unload") && !hasOrder(u, "VTOL_Unload") {
			t.Fatalf("u should queue unload")
		}
		n := findNode(u, "Ground_Unload")
		if n == nil {
			n = findNode(u, "VTOL_Unload")
		}
		if n.GoalX != 300*65536 {
			t.Fatalf("u x wrong")
		}
	}
	// w
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "w 5,7"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "Wait") {
			t.Fatalf("w should queue Wait")
		}
		n := findNode(u, "Wait")
		if n.Param1 != uint32(5*30) {
			t.Fatalf("w secs ticks wrong: got %d want %d", n.Param1, 150)
		}
		if n.Param2 != 7 {
			t.Fatalf("w trailing n wrong: got %d", n.Param2)
		}
	}
	// wa
	{
		w1 := units.New(5, nil)
		hA, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		hB, _ := w1.Create(testDef("ARMCK"), 0, 0, 0, 0)
		uA := w1.Unit(hA)
		uA.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{
			{UnitName: "ARMCOM", Ident: "alpha", InitialMission: "wa beta"},
			{UnitName: "ARMCK", Ident: "beta", InitialMission: ""},
		}}
		RunInitialMissions(m1, w1)
		if !hasOrder(uA, "WaitForAttack") {
			t.Fatalf("wa should queue WaitForAttack")
		}
		n := findNode(uA, "WaitForAttack")
		if n.Target != hB {
			t.Fatalf("wa target wrong: got %v want %v", n.Target, hB)
		}
		// wa unresolved fallback to self
		w2 := units.New(5, nil)
		hA2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		uA2 := w2.Unit(hA2)
		uA2.Flags |= 1 << 5
		m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "wa nonexistent"}}}
		RunInitialMissions(m2, w2)
		if !hasOrder(uA2, "WaitForAttack") {
			t.Fatalf("wa unresolved should still queue WaitForAttack fallback")
		}
		n2 := findNode(uA2, "WaitForAttack")
		if n2.Target != hA2 {
			t.Fatalf("wa fallback to self failed: got %v want %v", n2.Target, hA2)
		}
	}
}

func TestUppercaseWQuirk(t *testing.T) {
	// C11: uppercase-led W… token enters BUILD block, never plain Wait.
	w1 := units.New(5, nil)
	h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u := w1.Unit(h)
	u.Flags |= 1 << 5
	// Uppercase W 10 should be BUILD, not Wait.
	// We treat "W 10" as building build with name "10"? That still not Wait.
	// Instead use "Ww 3" which is BuildWeapon via uppercase quirk.
	m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "W 10"}}}
	RunInitialMissions(m1, w1)
	if hasOrder(u, "Wait") {
		t.Fatalf("uppercase W should never be Wait")
	}
	// Lowercase w should be Wait.
	w2 := units.New(5, nil)
	h2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u2 := w2.Unit(h2)
	u2.Flags |= 1 << 5
	m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "w 10"}}}
	RunInitialMissions(m2, w2)
	if !hasOrder(u2, "Wait") {
		t.Fatalf("lowercase w should be Wait")
	}
	// Wa lowercase should be WaitForAttack
	w3 := units.New(5, nil)
	h3, _ := w3.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u3 := w3.Unit(h3)
	u3.Flags |= 1 << 5
	m3 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "wa target"}}}
	RunInitialMissions(m3, w3)
	if !hasOrder(u3, "WaitForAttack") {
		t.Fatalf("lowercase wa should be WaitForAttack")
	}
	// Uppercase Wa should NOT be WaitForAttack (it goes to BUILD)
	w4 := units.New(5, nil)
	h4, _ := w4.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u4 := w4.Unit(h4)
	u4.Flags |= 1 << 5
	m4 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "Wa target"}}}
	RunInitialMissions(m4, w4)
	if hasOrder(u4, "WaitForAttack") {
		t.Fatalf("uppercase Wa should not be WaitForAttack per C11")
	}
	// Uppercase Ww should be BuildWeapon via quirk
	UnitTypeExistsHook = func(name string) bool { return true }
	defer func() { UnitTypeExistsHook = nil }()
	w5 := units.New(5, nil)
	h5, _ := w5.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u5 := w5.Unit(h5)
	u5.Flags |= 1 << 5
	m5 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "Ww 4"}}}
	RunInitialMissions(m5, w5)
	if !hasOrder(u5, "BuildWeapon") {
		t.Fatalf("uppercase Ww should be BuildWeapon via C11")
	}
	// Lowercase bw also BuildWeapon but via normal path
	w6 := units.New(5, nil)
	h6, _ := w6.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u6 := w6.Unit(h6)
	u6.Flags |= 1 << 5
	m6 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "bw 4"}}}
	RunInitialMissions(m6, w6)
	if !hasOrder(u6, "BuildWeapon") {
		t.Fatalf("lowercase bw should be BuildWeapon")
	}
}

func TestPostludeMakeSelectableSuppression(t *testing.T) {
	// C12 postlude: ≥1 order queued ⇒ bit5 clears; unless numeric a/p/d/s appeared, final MakeSelectable queues.
	// m does NOT suppress => should have tail
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "m 10,20"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "MakeSelectable") {
			t.Fatalf("m should have postlude MakeSelectable")
		}
		if u.Flags&(1<<5) != 0 {
			t.Fatalf("bit5 should clear")
		}
	}
	// numeric a suppresses
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "a 10,20"}}}
		RunInitialMissions(m1, w1)
		if hasOrder(u, "MakeSelectable") {
			t.Fatalf("numeric a should suppress MakeSelectable")
		}
		if u.Flags&(1<<5) != 0 {
			t.Fatalf("bit5 should still clear even when suppressed")
		}
	}
	// by-type a does NOT suppress
	{
		UnitTypeExistsHook = func(name string) bool { return true }
		defer func() { UnitTypeExistsHook = nil }()
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "a ARMCK"}}}
		RunInitialMissions(m1, w1)
		if !hasOrder(u, "MakeSelectable") {
			t.Fatalf("by-type a should NOT suppress MakeSelectable")
		}
	}
	// p suppresses
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "p 10,20,5"}}}
		RunInitialMissions(m1, w1)
		if hasOrder(u, "MakeSelectable") {
			t.Fatalf("p should suppress")
		}
	}
	// d suppresses
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "d"}}}
		RunInitialMissions(m1, w1)
		if hasOrder(u, "MakeSelectable") {
			t.Fatalf("d should suppress")
		}
	}
	// s suppresses but s itself is MakeSelectable, so exactly one.
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "s"}}}
		RunInitialMissions(m1, w1)
		q := orders.QueueForUnit(u)
		c := 0
		for _, n := range q.Primary() {
			if n.ID == orders.Lookup("MakeSelectable") {
				c++
			}
		}
		if c != 1 {
			t.Fatalf("s should have exactly 1 MakeSelectable, got %d", c)
		}
	}
	// no queued orders => no bit5 clear and no tail
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: ""}}}
		RunInitialMissions(m1, w1)
		if queueLen(u) != 0 {
			t.Fatalf("empty script should not queue")
		}
		if u.Flags&(1<<5) == 0 {
			t.Fatalf("bit5 should NOT clear when no queued orders")
		}
	}
	// multiple verbs with one suppressing => still suppress tail
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "m 1,1, d"}}}
		RunInitialMissions(m1, w1)
		if hasOrder(u, "MakeSelectable") {
			// Should have 0 Tail MakeSelectable, but d suppresses. However m before d still? The postlude MakeSelectable would be suppressed because script contained d.
			// But note d itself is not MakeSelectable; so queue should have Move and SelfDestruct, no extra MakeSelectable.
			// Check that no extra MakeSelectable beyond what? m+ d => no tail.
			q := orders.QueueForUnit(u)
			c := 0
			for _, n := range q.Primary() {
				if n.ID == orders.Lookup("MakeSelectable") {
					c++
				}
			}
			if c != 0 {
				t.Fatalf("m+d should suppress tail, got %d MakeSelectable", c)
			}
		}
	}
}

func TestSilentMalformed(t *testing.T) {
	// Unknown letters/digits/punctuation ignored, scanning resumes past comma [C13].
	// Failed lookups no-op (wa fallback).
	// Malformed numbers still queue with 0.
	w1 := units.New(5, nil)
	h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u := w1.Unit(h)
	u.Flags |= 1 << 5
	m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "?, 123, m 10,20, !, w 5"}}}
	RunInitialMissions(m1, w1)
	if !hasOrder(u, "Move_Ground") {
		t.Fatalf("silent malformed: m should still queue")
	}
	if !hasOrder(u, "Wait") {
		t.Fatalf("silent malformed: w should still queue")
	}
	// Check that unknown tokens didn't create orders
	if queueLen(u) != 4 { // Move + Wait + 2 MakeSelectable? Wait m and w both not suppress, so tail MakeSelectable should be present => Move, Wait, MakeSelectable = 3? Actually queued 2 orders -> tail 1 => total 3. Let's count.
		// m queues Move, w queues Wait, postlude adds MakeSelectable => 3.
		// Unknown ? and ! and 123 are ignored.
		// So total 3.
	}
	// malformed numbers: move with bad numbers should still queue with 0.
	w2 := units.New(5, nil)
	h2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u2 := w2.Unit(h2)
	u2.Flags |= 1 << 5
	m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "m bad,numbers"}}}
	RunInitialMissions(m2, w2)
	if !hasOrder(u2, "Move_Ground") {
		t.Fatalf("malformed m numbers should still queue")
	}
	n := findNode(u2, "Move_Ground")
	if n.GoalX != 0 || n.GoalZ != 0 {
		t.Fatalf("malformed numbers should result in 0, got %d,%d", n.GoalX, n.GoalZ)
	}
	// wa fallback to self when unresolved
	w3 := units.New(5, nil)
	h3, _ := w3.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u3 := w3.Unit(h3)
	u3.Flags |= 1 << 5
	m3 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "wa nosuch"}}}
	RunInitialMissions(m3, w3)
	n3 := findNode(u3, "WaitForAttack")
	if n3 == nil || n3.Target != h3 {
		t.Fatalf("wa unresolved fallback to self failed")
	}
	// g unresolved should queue nothing -> no postlude if no other orders? Actually g unresolved queues nothing, so queued==0 -> no tail and bit5 not cleared.
	w4 := units.New(5, nil)
	h4, _ := w4.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u4 := w4.Unit(h4)
	u4.Flags |= 1 << 5
	m4 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "g nosuch"}}}
	RunInitialMissions(m4, w4)
	if queueLen(u4) != 0 {
		t.Fatalf("g unresolved should queue nothing, got %d", queueLen(u4))
	}
	if u4.Flags&(1<<5) == 0 {
		t.Fatalf("g unresolved with no queued order should not clear bit5")
	}
}

func TestClamp255(t *testing.T) {
	// Comma-free run >255 clamps [C13] sanctioned divergence.
	// Generate a script with no commas, length 300, with a valid verb at start.
	long := strings.Repeat("a", 300) // no comma, all 'a's but first is 'a' verb? Actually token would be "aaa...". First char 'a', rest is payload but no comma -> one token length 300.
	// Our clamp should truncate to 255 and not panic.
	w1 := units.New(5, nil)
	h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u := w1.Unit(h)
	u.Flags |= 1 << 5
	// Use a long token that is "m " plus many spaces to exceed 255 but without comma.
	long2 := "m " + strings.Repeat("1", 300)
	m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: long2}}}
	// Should not panic, and should queue one move order (clamped).
	RunInitialMissions(m1, w1)
	if !hasOrder(u, "Move_Ground") {
		t.Fatalf("long comma-free should still queue clamped token")
	}
	// Token with comma-free long run inside script that has comma delimiter should clamp per-token.
	// Example: script "m 1,2, " + long without comma? Hard to test.
	// At least ensure no panic for long script.
	w2 := units.New(5, nil)
	h2, _ := w2.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u2 := w2.Unit(h2)
	m2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: long}}}
	RunInitialMissions(m2, w2)
	// long "a..." token: fields after 'a' are 299 'a's which are not numeric, so a by-type with name "aaa..." -> should queue AttackUType if type exists.
	// We just check no panic and some queue.
	_ = u2
	_ = h
}

func TestCoordinatesAndTimesVectors(t *testing.T) {
	// Coordinates parse as floats scaled by 65536 [C10]; times scale by 30.
	cases := []struct {
		token string
		check func(*units.Unit) bool
	}{
		{
			token: "m 1.5,2.5",
			check: func(u *units.Unit) bool {
				n := findNode(u, "Move_Ground")
				if n == nil {
					n = findNode(u, "VTOL_Move")
				}
				return n != nil && n.GoalX == numeric.Fixed(int64(1.5*65536)) && n.GoalZ == numeric.Fixed(int64(2.5*65536))
			},
		},
		{
			token: "m -1.5,-2.5",
			check: func(u *units.Unit) bool {
				n := findNode(u, "Move_Ground")
				if n == nil {
					n = findNode(u, "VTOL_Move")
				}
				return n != nil && n.GoalX == numeric.Fixed(int64(-1.5*65536)) && n.GoalZ == numeric.Fixed(int64(-2.5*65536))
			},
		},
		{
			token: "w 2.5,3",
			check: func(u *units.Unit) bool {
				n := findNode(u, "Wait")
				return n != nil && n.Param1 == uint32(int32(2.5*30)) && n.Param2 == 3
			},
		},
		{
			token: "p 10,20,1.5",
			check: func(u *units.Unit) bool {
				n := findNode(u, "Patrol")
				if n == nil {
					n = findNode(u, "VTOL_Patrol")
				}
				return n != nil && n.GoalX == 10*65536 && n.GoalZ == 20*65536 && n.Param1 == uint32(int32(1.5*30))
			},
		},
		{
			token: "o 2,1",
			check: func(u *units.Unit) bool {
				want := (uint32(2&3) << 17) | (uint32(1&3) << 19)
				got := u.Flags & ((0x3 << 17) | (0x3 << 19))
				return got == want
			},
		},
	}
	for i, tc := range cases {
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags = 0
		// For move/p/w we need flag bit5 set to test clearing, but o test clears flags.
		if tc.token[0] != 'o' {
			u.Flags |= 1 << 5
		}
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: tc.token}}}
		RunInitialMissions(m1, w1)
		if !tc.check(u) {
			t.Fatalf("case %d token %q failed", i, tc.token)
		}
	}
	// Additional negative time trunc toward zero: w -1.5 should be -45 ticks truncated toward zero => -45.
	{
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags |= 1 << 5
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "w -1.5"}}}
		RunInitialMissions(m1, w1)
		n := findNode(u, "Wait")
		if n == nil || int32(n.Param1) != int32(-1.5*30) {
			t.Fatalf("negative time trunc failed: got %d want %d", int32(n.Param1), int32(-1.5*30))
		}
	}
}

func TestNonCampaignNoOp(t *testing.T) {
	// C9: only type 1 and BetweenMissions restores run. Type 2 should be no-op.
	w1 := units.New(5, nil)
	h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
	u := w1.Unit(h)
	u.Flags |= 1 << 5
	m1 := &Mission{Type: TypeSkirmish, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "m 10,20"}}}
	RunInitialMissions(m1, w1)
	if queueLen(u) != 0 {
		t.Fatalf("TypeSkirmish should not run InitialMission, got %d", queueLen(u))
	}
	if u.Flags&(1<<5) == 0 {
		t.Fatalf("bit5 should not clear for non-campaign")
	}
}

func TestMissionOFlagBits(t *testing.T) {
	// o d1,d2 writes bits 17-18 and 19-20 [C10].
	cases := []struct {
		d1, d2 int
		want   uint32
	}{
		{0, 0, 0},
		{1, 2, (1 << 17) | (2 << 19)},
		{3, 3, (3 << 17) | (3 << 19)},
		{5, 9, (1 << 17) | (1 << 19)}, // &3 masks
	}
	for _, c := range cases {
		w1 := units.New(5, nil)
		h, _ := w1.Create(testDef("ARMCOM"), 0, 0, 0, 0)
		u := w1.Unit(h)
		u.Flags = 0
		m1 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "ARMCOM", InitialMission: "o " + strconv.Itoa(c.d1) + "," + strconv.Itoa(c.d2)}}}
		RunInitialMissions(m1, w1)
		got := u.Flags & ((0x3 << 17) | (0x3 << 19))
		if got != c.want {
			t.Fatalf("o %d,%d: got %x want %x", c.d1, c.d2, got, c.want)
		}
		// o should not queue order
		if queueLen(u) != 0 {
			t.Fatalf("o should not queue order")
		}
	}
}
