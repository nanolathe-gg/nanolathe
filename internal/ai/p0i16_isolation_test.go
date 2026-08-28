package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestSelectionPerManagerProfileAndVectorIsolation proves that concrete
// catalog, profile, and strategic-vector state remain manager-local when
// selections interleave in one process [P0-I16].
func TestSelectionPerManagerProfileAndVectorIsolation(t *testing.T) {
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
	rng.SeedGlobal(42, 0)
	mgrA.RNG = rng.Global.Sim
	mgrB.RNG = rng.Global.Sim

	builderA := &units.Unit{Def: catA.Units[content.CanonicalKey("a_builder")], Owner: 0, Alive: true}
	builderB := &units.Unit{Def: catB.Units[content.CanonicalKey("b_builder")], Owner: 0, Alive: true}
	var econA, econB economy.Service
	econA.Players[0].Stock[economy.Energy] = 800
	econA.Players[0].Stock[economy.Metal] = 400
	econA.Players[0].Capacity[economy.Energy] = 1000
	econA.Players[0].Capacity[economy.Metal] = 500
	econA.Players[0].AIProduction[economy.Energy] = 300
	econA.Players[0].AIProduction[economy.Metal] = 10
	econB = econA

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
}
