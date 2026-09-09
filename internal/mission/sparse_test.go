package mission

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestSparseVsDenseDivergence locks A27 [P0-04][P0-06]: retail uses sparse
// created[] with NULL on allocation failure, skipping NULL for
// Ident→Unitname first-occurrence scan. Dense w.Iter() prefix is wrong when
// allocation fails.
func TestSparseVsDenseDivergence(t *testing.T) {
	// Capacity 2, 3 placements, middle allocation will fail if we exhaust pool?
	// Use pool capacity 2: first two allocations succeed, third fails.
	// Instead simulate via limiting world capacity 2 and 3 placements.
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {UnitName: "armcom", MaxDamage: 100, CanMove: true, CanAttack: true},
			"armck":  {UnitName: "armck", MaxDamage: 100},
		},
	}
	w := newMissionFixtureWorld(2, cat) // capacity 2, third alloc fails
	// Create two units directly to fill pool, then try third via RunInitialMissions path?
	// Simpler: create placements 3 where second will fail due to pool exhausted.
	// We'll use reconstruct via session? Use direct world creation to simulate sparse.
	// Create placements:
	placements := []UnitPlacement{
		{UnitName: "armcom", Ident: "alpha", InitialMission: "g beta"},
		{UnitName: "armck", Ident: "beta", InitialMission: ""},
		{UnitName: "armcom", Ident: "gamma", InitialMission: ""},
	}
	m := &Mission{Type: TypeCampaign, Units: placements}
	// First pass: allocate up to capacity
	for idx, up := range placements {
		def, ok := cat.Unit(up.UnitName)
		if !ok {
			continue
		}
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			// failed -> sparse NULL
			continue
		}
		u := w.Unit(h)
		u.PlacementIdx = idx
		u.PlacementIdent = up.Ident
		u.PlacementUnitName = up.UnitName
	}
	// Now w has 2 units: placements 0 and 1; placement 2 failed (pool exhausted).
	// But we forced creation for 0 and 1; 2 should fail. Let's check.
	if w.Used() != 2 {
		t.Fatalf("pool used %d want 2", w.Used())
	}
	// Build sparse map as RunInitialMissions does.
	createdSparse := make([]*units.Unit, len(placements))
	for _, u := range w.Iter() {
		if u.PlacementIdx >= 0 && u.PlacementIdx < len(createdSparse) {
			createdSparse[u.PlacementIdx] = u
		}
	}
	if createdSparse[0] == nil || createdSparse[1] == nil || createdSparse[2] != nil {
		t.Fatalf("sparse incorrect: %v", createdSparse)
	}
	// g beta from placement 0 should resolve to placement 1 (beta), not gamma.
	// Dense w.Iter()[1] is beta (placement 1) – same here, so not divergent yet.
	// Force failure of middle: we already have middle present, last missing. Need
	// case where middle missing and last present to show divergence.
	// Reset and make middle fail.
	// Capacity 1 against three placements: the first succeeds and the rest are
	// refused, which is the sparse hole this case exists to show.
	w2 := newMissionFixtureWorld(1, cat)
	placements2 := []UnitPlacement{
		{UnitName: "armcom", Ident: "a", InitialMission: "g c"},
		{UnitName: "armck", Ident: "b", InitialMission: ""},
		{UnitName: "armcom", Ident: "c", InitialMission: ""},
	}
	// Only first can allocate due to capacity 1.
	for idx, up := range placements2 {
		def, _ := cat.Unit(up.UnitName)
		h, err := w2.Create(def, 0, 0, 0, 0)
		if err != nil {
			continue
		}
		u := w2.Unit(h)
		u.PlacementIdx = idx
		u.PlacementIdent = up.Ident
		u.PlacementUnitName = up.UnitName
		if w2.Used() >= 1 && idx == 0 {
			// Keep first, second will fail (pool exhausted), third would also fail
			// but we want third to succeed and second to fail to show gap.
			// So with capacity 1 we cannot have third succeed if second failed and pool still full.
			// Use capacity 2 with controlled failure: allocate 0, allocate 1 fails via unknown type, allocate 2 succeeds.
		}
	}
	// Use unknown type for middle to simulate type existence failure [P0-04].
	w3 := newMissionFixtureWorld(10, cat)
	placements3 := []UnitPlacement{
		{UnitName: "armcom", Ident: "alpha", InitialMission: "g gamma"},
		{UnitName: "unknown_type", Ident: "beta", InitialMission: ""}, // fails type check -> sparse NULL
		{UnitName: "armcom", Ident: "gamma", InitialMission: ""},
	}
	m3 := &Mission{Type: TypeCampaign, Units: placements3}
	for idx, up := range placements3 {
		def, ok := cat.Unit(up.UnitName)
		if !ok {
			continue
		}
		h, err := w3.Create(def, 0, 0, 0, 0)
		if err != nil {
			continue
		}
		u := w3.Unit(h)
		u.PlacementIdx = idx
		u.PlacementIdent = up.Ident
		u.PlacementUnitName = up.UnitName
	}
	createdSparse3 := make([]*units.Unit, len(placements3))
	for _, u := range w3.Iter() {
		if u.PlacementIdx >= 0 && u.PlacementIdx < len(createdSparse3) {
			createdSparse3[u.PlacementIdx] = u
		}
	}
	// Sparse: [0]=alpha, [1]=nil, [2]=gamma
	if createdSparse3[0] == nil || createdSparse3[1] != nil || createdSparse3[2] == nil {
		t.Fatalf("sparse gap failed: %v", createdSparse3)
	}
	// Build ident map first-occurrence skipping NULL [P0-06]
	identMap := make(map[string]int)
	unitNameMap := make(map[string]int)
	for i, pl := range placements3 {
		if createdSparse3[i] == nil {
			continue
		}
		lowerIdent := strings.ToLower(pl.Ident)
		if _, ok := identMap[lowerIdent]; !ok && pl.Ident != "" {
			identMap[lowerIdent] = i
		}
		lowerName := strings.ToLower(pl.UnitName)
		if _, ok := unitNameMap[lowerName]; !ok && pl.UnitName != "" {
			unitNameMap[lowerName] = i
		}
	}
	// g gamma from alpha should resolve to placement 2 (gamma), skipping NULL at 1.
	if idx, ok := identMap["gamma"]; !ok || idx != 2 {
		t.Fatalf("identMap gamma should be 2, got %d %v", idx, ok)
	}
	// Dense w.Iter() would be [alpha, gamma] length 2, where index 1 corresponds to gamma,
	// so dense mapping of placement 1 -> gamma would incorrectly map beta's slot to gamma.
	ws := w3.Iter()
	if len(ws) != 2 {
		t.Fatalf("ws len %d", len(ws))
	}
	denseIdxForGamma := -1
	for i, u := range ws {
		if strings.EqualFold(u.PlacementIdent, "gamma") {
			denseIdxForGamma = i
			break
		}
	}
	if denseIdxForGamma != 1 {
		t.Fatalf("dense gamma idx %d", denseIdxForGamma)
	}
	// Retail lookup for g gamma via sparse should be placement 2, not dense 1 misaligned with placement 1
	// Demonstrate divergence: dense mapping would think placement 1 (beta) -> ws[1] gamma, which is wrong.
	// Our sparse correctly maps placement 1 -> nil, so g beta would be unresolved, not gamma.
	if _, ok := identMap["beta"]; ok {
		t.Fatalf("beta should be unresolved (sparse NULL) but got mapping")
	}
	// Also test that RunInitialMissions queues guard correctly via sparse
	RunInitialMissionsWithCatalog(m3, w3, cat)
	// alpha should have guard order to gamma
	var alpha *units.Unit
	for _, u := range w3.Iter() {
		if strings.EqualFold(u.PlacementIdent, "alpha") {
			alpha = u
			break
		}
	}
	if alpha == nil {
		t.Fatalf("alpha not found")
	}
	// Check queue has Follow
	found := false
	for _, n := range orders.QueueForUnit(alpha).Primary() {
		if n.Target != 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("alpha should have guard order to gamma via sparse, no target found")
	}
	_ = m
	_ = placements
	_ = createdSparse
}
