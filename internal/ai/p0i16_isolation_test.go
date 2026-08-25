package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestP0I16_Isolation_TwoManagersInterleaved proves that two managers with different
// hooks do not contaminate each other when interleaved in one process [P0-I16].
func TestP0I16_Isolation_TwoManagersInterleaved(t *testing.T) {
	// Two catalogs with different build menus to prove per-manager Catalog isolation.
	catA := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("a_builder"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("a_builder")}, UnitName: "a_builder", Builder: true, MaxDamage: 100},
			content.CanonicalKey("a_unit"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("a_unit")}, UnitName: "a_unit", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("a_builder"): {Buttons: []string{"a_unit"}},
		},
	}
	catB := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("b_builder"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("b_builder")}, UnitName: "b_builder", Builder: true, MaxDamage: 100},
			content.CanonicalKey("b_unit"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("b_unit")}, UnitName: "b_unit", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("b_builder"): {Buttons: []string{"b_unit"}},
		},
	}

	mgrA := &Manager{Player: 0, Catalog: catA, Strategic: Strategic{Catalog: catA, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("a_unit"): {C0: 100}}}, Profile: &Profile{Weight: map[string]int32{content.CanonicalKey("a_unit"): 100}, Limit: map[string]int32{}}}
	mgrB := &Manager{Player: 0, Catalog: catB, Strategic: Strategic{Catalog: catB, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("b_unit"): {C0: 100}}}, Profile: &Profile{Weight: map[string]int32{content.CanonicalKey("b_unit"): 100}, Limit: map[string]int32{}}}

	// Different CandidateSource hooks
	mgrA.CandidateSource = func(b *units.Unit) []string { return []string{"a_unit"} }
	mgrB.CandidateSource = func(b *units.Unit) []string { return []string{"b_unit"} }

	// Different gate hooks – initially not blocking to allow interleaved success [P0-I16]
	mgrA.MissionGateFlag = 0
	mgrA.GateCandidates = nil
	mgrB.MissionGateFlag = 0
	mgrB.GateCandidates = nil

	builderA := &units.Unit{Def: catA.Units[content.CanonicalKey("a_builder")], Owner: 0, Alive: true}
	builderB := &units.Unit{Def: catB.Units[content.CanonicalKey("b_builder")], Owner: 0, Alive: true}
	var econA, econB economy.Service
	econA.Players[0].Stock[economy.Energy] = 800
	econA.Players[0].Stock[economy.Metal] = 400
	econA.Players[0].Capacity[economy.Energy] = 1000
	econA.Players[0].Capacity[economy.Metal] = 500
	econA.Players[0].PassProduced[economy.Energy] = 300
	econA.Players[0].PassProduced[economy.Metal] = 10
	econB = econA

	rng.SeedGlobal(42, 0)
	// Interleaved calls
	candA1, okA1 := Select(mgrA, builderA, &econA)
	candB1, okB1 := Select(mgrB, builderB, &econB)
	candA2, okA2 := Select(mgrA, builderA, &econA)
	candB2, okB2 := Select(mgrB, builderB, &econB)

	if !okA1 || !okB1 || !okA2 || !okB2 {
		t.Fatalf("selection should succeed for both managers")
	}
	if candA1.DefKey != "a_unit" || candA2.DefKey != "a_unit" {
		t.Fatalf("mgrA should always select a_unit, got %q %q", candA1.DefKey, candA2.DefKey)
	}
	if candB1.DefKey != "b_unit" || candB2.DefKey != "b_unit" {
		t.Fatalf("mgrB should always select b_unit, got %q %q", candB1.DefKey, candB2.DefKey)
	}
	// Gate isolation: mgrA will block a_unit, mgrB should not block b_unit
	mgrA.GateCandidates = map[string]struct{}{content.CanonicalKey("a_unit"): {}}
	mgrA.MissionGateFlag = 1
	rng.SeedGlobal(99, 0)
	if _, ok := Select(mgrA, builderA, &econA); ok {
		t.Fatalf("mgrA gate should block a_unit when flag=1 and candidate in set")
	}
	rng.SeedGlobal(99, 0)
	if _, ok := Select(mgrB, builderB, &econB); !ok {
		t.Fatalf("mgrB gate should not block b_unit when flag=0")
	}
	// Ensure mgrA's gate change didn't affect mgrB
	if mgrB.MissionGateFlag != 0 || len(mgrB.GateCandidates) != 0 {
		t.Fatalf("mgrB gate contaminated by mgrA")
	}
}

// TestP0I16_SaveReloadIsolation simulates save/destroy/reload isolation.
func TestP0I16_SaveReloadIsolation(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("builder"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("builder")}, UnitName: "builder", Builder: true, MaxDamage: 100},
			content.CanonicalKey("unitA"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("unitA")}, UnitName: "unitA", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("builder"): {Buttons: []string{"unitA"}},
		},
	}
	mgr := &Manager{Player: 1, Catalog: cat, Strategic: Strategic{Catalog: cat, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("unitA"): {C0: 100}}}, Profile: &Profile{Weight: map[string]int32{content.CanonicalKey("unitA"): 100}}}
	mgr.CandidateSource = func(b *units.Unit) []string { return []string{"unitA"} }
	mgr.MissionGateFlag = 1
	mgr.GateCandidates = map[string]struct{}{content.CanonicalKey("unitA"): {}}

	// Simulate save by copying fields
	savedCandidateSource := mgr.CandidateSource
	savedFlag := mgr.MissionGateFlag
	savedCandidates := mgr.GateCandidates
	savedCatalog := mgr.Catalog
	savedStrategicCatalog := mgr.Strategic.Catalog

	// Destroy mgr and create another with different hooks
	mgr2 := &Manager{Player: 2, Catalog: &content.Catalog{Units: map[string]*content.UnitDef{content.CanonicalKey("other"): {UnitName: "other"}}}, Strategic: Strategic{Catalog: &content.Catalog{}}}
	mgr2.CandidateSource = func(b *units.Unit) []string { return []string{"other"} }
	mgr2.MissionGateFlag = 0
	mgr2.GateCandidates = nil
	_ = mgr2

	// Reload first manager's saved state into new instance
	reloaded := &Manager{Player: 1, Catalog: savedCatalog, Strategic: Strategic{Catalog: savedStrategicCatalog, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("unitA"): {C0: 100}}}, Profile: mgr.Profile}
	reloaded.CandidateSource = savedCandidateSource
	reloaded.MissionGateFlag = savedFlag
	reloaded.GateCandidates = savedCandidates

	if reloaded.CandidateSource == nil {
		t.Fatalf("reloaded CandidateSource should be preserved")
	}
	builder := &units.Unit{Def: cat.Units[content.CanonicalKey("builder")], Owner: 1, Alive: true}
	var econ economy.Service
	econ.Players[1].Stock[economy.Energy] = 800
	econ.Players[1].Stock[economy.Metal] = 400
	econ.Players[1].Capacity[economy.Energy] = 1000
	econ.Players[1].Capacity[economy.Metal] = 500
	econ.Players[1].PassProduced[economy.Energy] = 300
	econ.Players[1].PassProduced[economy.Metal] = 10
	rng.SeedGlobal(1, 0)
	// Gate should still block because flag=1 and candidate in set
	if _, ok := Select(reloaded, builder, &econ); ok {
		t.Fatalf("reloaded gate should still block")
	}
	// Change flag to 0 and it should pass
	reloaded.MissionGateFlag = 0
	rng.SeedGlobal(1, 0)
	if _, ok := Select(reloaded, builder, &econ); !ok {
		t.Fatalf("reloaded with flag 0 should pass")
	}
}
