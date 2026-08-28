package mission

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestP0I16_MissionCatalogIsolation(t *testing.T) {
	catA := initialMissionCatalog(false)
	catB := initialMissionCatalog(true)
	catA.Units[content.CanonicalKey("known")] = &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("known")}, UnitName: "known", MaxDamage: 100}
	catB.Units[content.CanonicalKey("known")] = &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("known")}, UnitName: "known", MaxDamage: 100}
	wA := units.New(5, catA)
	wB := units.New(5, catB)
	def := &content.UnitDef{UnitName: "known", MaxDamage: 100, CanMove: true, CanAttack: true}
	hA, _ := wA.Create(def, 0, 0, 0, 0)
	uA := wA.Unit(hA)
	uA.Flags |= 1 << 5
	uA.PlacementIdx = 0
	hB, _ := wB.Create(def, 0, 0, 0, 0)
	uB := wB.Unit(hB)
	uB.Flags |= 1 << 5
	uB.PlacementIdx = 0

	mA := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "known", Ident: "u0", InitialMission: "a unknown_type"}}}
	mB := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "known", Ident: "u0", InitialMission: "a unknown_type"}}}

	RunInitialMissionsWithCatalog(mA, wA, catA)
	RunInitialMissionsWithCatalog(mB, wB, catB)

	// mA should not have AttackUType because unknown_type is considered not exists
	if hasOrderP0I16(uA, "AttackUType") {
		t.Fatalf("catalog A should block unknown_type, but got AttackUType")
	}
	if !hasOrderP0I16(uB, "AttackUType") {
		t.Fatalf("catalog B should allow unknown_type")
	}
	// Interleaved second run: ensure no cross contamination
	wA2 := units.New(5, catA)
	hA2, _ := wA2.Create(def, 0, 0, 0, 0)
	uA2 := wA2.Unit(hA2)
	uA2.Flags |= 1 << 5
	uA2.PlacementIdx = 0
	mA2 := &Mission{Type: TypeCampaign, Units: []UnitPlacement{{UnitName: "known", Ident: "u0", InitialMission: "a unknown_type"}}}
	RunInitialMissionsWithCatalog(mA2, wA2, catA)
	if hasOrderP0I16(uA2, "AttackUType") {
		t.Fatalf("interleaved catalog A should still block")
	}

	_ = orders.QueueForUnit
}

func hasOrderP0I16(u *units.Unit, name string) bool {
	id := orders.Lookup(name)
	if id == 0 {
		return false
	}
	q := orders.QueueForUnit(u)
	for _, n := range q.Primary() {
		if n.ID == id {
			return true
		}
	}
	for _, n := range q.Secondary() {
		if n.ID == id {
			return true
		}
	}
	return false
}
