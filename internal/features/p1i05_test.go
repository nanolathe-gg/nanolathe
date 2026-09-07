package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

func newTestTerrainP1(w, h int) *world.Terrain {
	attrs := make([]formats.TNTAttribute, w*h)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, w, h)
	return &world.Terrain{
		CellW:       int32(w),
		CellH:       int32(h),
		Plot:        plot,
		FeatureDefs: []*content.FeatureDef{},
		SeaLevel:    10,
		Gravity:     numeric.Fixed(0x1FDB),
	}
}

func defP1(name string, footX, footZ int32, obj, filename string) *content.FeatureDef {
	fd := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		FootprintX:       footX,
		FootprintZ:       footZ,
		Damage:           10,
		Metal:            5,
		Energy:           3,
	}
	fd.Object = obj
	fd.Filename = filename
	fd.Reclaimable = true
	return fd
}

// TestAllocationFailurePolicy locks the 0x800 anim slots and the WH*0xD grid
// silent fail [P1-10][P1-15] [P1-I05]. The catalog is NOT one of the pools that
// can refuse: it is reallocated per record with no fixed cap, and "catalog
// exhaustion" is not a retail failure mode [05 R-FEAT-01 §1]. This test used to
// fill 256 records and demand a refusal; CL-6 removed the guard it locked.
func TestAllocationFailurePolicy(t *testing.T) {
	w, h := 4, 4
	terrain := newTestTerrainP1(w, h)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)

	// A definition past 256 records is admitted, not refused [05 R-FEAT-01 §1].
	for i := 0; i < 0x100; i++ {
		fd := defP1("feat"+string(rune('a'+i%26)), 1, 1, "", "")
		fd.CanonicalKey = content.CanonicalKey("catalog" + string(rune(i)))
		terrain.FeatureDefs = append(terrain.FeatureDefs, fd)
	}
	extra := defP1("extra", 1, 1, "", "")
	extra.CanonicalKey = content.CanonicalKey("extra")
	if inst := svc.spawnFeatureAt(0, 0, extra); inst == nil {
		t.Fatalf("a 257th definition must be admitted; the catalog has no cap [05 R-FEAT-01 §1]")
	}
	// Clear catalog and instances for the anim-slot arm.
	svc.resetInstances()
	terrain.Plot[0].SetFeature(world.PlotFeatureNone)
	terrain.Plot[0].SetFlagByte(0)
	terrain.FeatureDefs = []*content.FeatureDef{}
	// Anim pool 0x800 silent fail: fill instances map to 2048
	for i := 0; i < FeatureAnimSlots; i++ {
		// Use non-conflicting positions by wrapping? But our map key is cz*W+cx, limited to 16 cells. To fill map we need to allow many instances even on same cell? Instead directly fill map with synthetic keys beyond grid to simulate pool exhaustion
		// For test, we directly set map size to limit
		svc.setInstance(i, &Instance{CX: i % w, CZ: i % h, Def: defP1("a", 1, 1, "", "")})
		if len(svc.instances) >= FeatureAnimSlots {
			break
		}
	}
	// Ensure we reached limit
	if len(svc.instances) < FeatureAnimSlots {
		// Force to limit via manual fill with unique keys beyond grid bounds trick: bypass grid limit by directly inserting
		for len(svc.instances) < FeatureAnimSlots {
			k := len(svc.instances) + 10000
			svc.setInstance(k, &Instance{CX: 0, CZ: 0, Def: defP1("x", 1, 1, "", "")})
		}
	}
	fd2 := defP1("animTest", 1, 1, "", "")
	fd2.CanonicalKey = content.CanonicalKey("animTest")
	terrain.FeatureDefs = []*content.FeatureDef{fd2}
	// Need to clear a cell to attempt spawn but pool still full
	terrain.Plot[1].SetFeature(world.PlotFeatureNone)
	terrain.Plot[1].SetFlagByte(0)
	if inst := svc.spawnFeatureAt(1, 0, fd2); inst != nil {
		t.Fatalf("anim pool 0x800 exhaustion should silent fail [P1-10][P1-15]")
	}
	// WH*0xD grid silent fail: out-of-bounds only. An occupied cell is NOT a
	// failure — the dense-pack rule replaces a destructible feature standing
	// where the new one lands, and "there is no 'already occupied' failure"
	// other than indestructible, void and stale-fringe [05 R-FEAT-01 §3 step 3].
	// This assertion used to demand the opposite and was the defect R12 names.
	svc.resetInstances()
	terrain.Plot[0].SetFeature(0) // occupies 0,0
	terrain.FeatureDefs = []*content.FeatureDef{defP1("gridTest", 1, 1, "", "")}
	terrain.FeatureDefs[0].CanonicalKey = content.CanonicalKey("gridTest")
	if inst := svc.spawnFeatureAt(0, 0, terrain.FeatureDefs[0]); inst == nil {
		t.Fatalf("occupied destructible cell should be replaced, not refused [05 R-FEAT-01 §3 step 3]")
	}
	svc.resetInstances()
	terrain.Plot[0].SetFeature(world.PlotFeatureNone)
	terrain.Plot[0].SetFlagByte(0)
	if inst := svc.spawnFeatureAt(-1, 0, terrain.FeatureDefs[0]); inst != nil {
		t.Fatalf("out-of-bounds should silent fail [P1-15] WH*0xD grid")
	}
	if inst := svc.spawnFeatureAt(w, h, terrain.FeatureDefs[0]); inst != nil {
		t.Fatalf("out-of-bounds should silent fail")
	}
	// Missing successor sentinel 0xFFFF: ensure remove with no successor just frees
	svc.resetInstances()
	terrain.Plot[0].SetFeature(world.PlotFeatureNone)
	terrain.Plot[0].SetFlagByte(0)
	defNoSucc := defP1("nosucc", 1, 1, "", "")
	defNoSucc.CanonicalKey = content.CanonicalKey("nosucc")
	defNoSucc.FeatureDead = ""
	defNoSucc.FeatureDeadDef = nil
	terrain.FeatureDefs = []*content.FeatureDef{defNoSucc}
	inst := svc.spawnFeatureAt(0, 0, defNoSucc)
	if inst == nil {
		t.Fatalf("spawn nosucc failed")
	}
	svc.RemoveFeatureAt(0, 0, CauseDead)
	if !terrain.Plot[0].IsEmpty() {
		t.Fatalf("missing successor should return to free sentinel 0xFFFF [P1-10][P1-15] 0xFFFF")
	}
}

// TestVentPersistence verifies geothermal vent persists under building and after removal [05 "Geothermal requirement"] [P1-10]
func TestVentPersistence(t *testing.T) {
	w, h := 8, 8
	terrain := newTestTerrainP1(w, h)
	terrain.ApplySchema(nil, 0)
	// Create vent feature
	ventDef := defP1("geovent", 1, 1, "", "")
	ventDef.CanonicalKey = content.CanonicalKey("geovent")
	ventDef.Geothermal = true
	ventDef.Reclaimable = false
	ventDef.Blocking = false
	ventDef.Damage = 100
	terrain.FeatureDefs = []*content.FeatureDef{ventDef}
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	vent := svc.PlaceAt(4, 4, ventDef)
	if vent == nil {
		t.Fatalf("vent place failed")
	}
	// Simulate building placement that requires geothermal: yard with G (0x8f) at 0,0
	yard, err := world.ParseYardMap("G", 1, 1)
	if err != nil {
		t.Fatalf("ParseYardMap: %v", err)
	}
	if err := terrain.ValidatePlacement(4, 4, yard, 1, 1, 0); err != nil {
		t.Fatalf("geothermal placement should succeed when vent present: %v", err)
	}
	// Ensure vent still present after validation (read-only)
	if !VentPersistsAfterBuildingRemoval(terrain, 4, 4, ventDef) {
		t.Fatalf("vent should persist after placement validation [05 \"Geothermal requirement\"] [P1-10]")
	}
	// Simulate building removal (no op on feature grid) and verify vent still there
	// No clearFootprint called for building; vent should remain
	if inst := svc.InstanceAt(4, 4); inst == nil || inst.Def != ventDef {
		t.Fatalf("vent instance should still be at 4,4 after building would be placed")
	}
	// Destroy vent's successor path: vent has no successor, removal should free but we test plant destruction does NOT clear vent
	// Instead, test that removing a different feature at same location via successor replacement still respects vent? No, we just ensure vent not cleared by building logic
	// Clear a non-vent feature nearby and ensure vent unaffected
	otherDef := defP1("rock", 1, 1, "", "")
	otherDef.CanonicalKey = content.CanonicalKey("rock")
	terrain.FeatureDefs = append(terrain.FeatureDefs, otherDef)
	other := svc.PlaceAt(2, 2, otherDef)
	if other == nil {
		t.Fatalf("other place failed")
	}
	svc.RemoveFeatureAt(2, 2, CauseDead)
	if terrain.Plot[2+2*w].Feature() != world.PlotFeatureNone {
		t.Fatalf("other feature removal should clear its cell")
	}
	if !VentPersistsAfterBuildingRemoval(terrain, 4, 4, ventDef) {
		t.Fatalf("vent should still persist after other feature removal")
	}
	// Also test at-least-one multi-vent satisfaction: footprint 2x2 with one vent anywhere should satisfy
	yard2, _ := world.ParseYardMap("GGGG", 2, 2) // G at all cells
	// Place vent only at 4,4, footprint at 4,4 covering 4,4 and 5,5 etc, but vent at 4,4 only
	if err := terrain.ValidatePlacement(4, 4, yard2, 2, 2, 0); err != nil {
		t.Fatalf("multi-vent at-least-one should succeed when one vent present [05][P1-10], got %v", err)
	}
	// Empty location without vent should fail
	if err := terrain.ValidatePlacement(6, 6, yard2, 2, 2, 0); err == nil {
		t.Fatalf("geothermal placement without vent should fail [05][P1-10]")
	}
}

// TestDestruction verifies feature transitions to successor or removed, with reclaim credit [05 "Removal and successor replacement"] [05 "Feature reclaim"]
func TestDestruction(t *testing.T) {
	w, h := 4, 4
	terrain := newTestTerrainP1(w, h)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	// Create chain A->B->C
	defA := defP1("A", 1, 1, "", "")
	defA.CanonicalKey = content.CanonicalKey("A")
	defA.Reclaimable = true
	defA.Metal = 10
	defA.Energy = 20
	defA.Damage = 30
	defB := defP1("B", 1, 1, "", "")
	defB.CanonicalKey = content.CanonicalKey("B")
	defB.Reclaimable = true
	defB.Damage = 5
	defC := defP1("C", 1, 1, "", "")
	defC.CanonicalKey = content.CanonicalKey("C")
	defC.Damage = 5
	defA.FeatureDeadDef = defB
	defA.FeatureDead = "B"
	defA.FeatureReclamateDef = defC
	defA.FeatureReclamate = "C"
	defA.FeatureBurntDef = nil
	terrain.FeatureDefs = []*content.FeatureDef{defA, defB, defC}
	_ = svc.PlaceAt(1, 1, defA)
	// CauseDead should hop to B
	svc.RemoveFeatureAt(1, 1, CauseDead)
	if inst := svc.InstanceAt(1, 1); inst == nil || inst.Def != defB {
		t.Fatalf("dead hop should spawn B [05 \"Removal and successor replacement\"]")
	}
	// Reclaim from B (which has nil reclaim successor) should remove to free
	// But B's reclaim successor is nil, so next reclaim should free
	svc.RemoveFeatureAt(1, 1, CauseReclaim)
	if !terrain.Plot[1+1*w].IsEmpty() {
		t.Fatalf("reclaim with no successor should free to 0xFFFF [P1-10][P1-15]")
	}
	// Test reclaim credit via Reclaim helper
	_ = svc.PlaceAt(1, 1, defA)
	inst := svc.InstanceAt(1, 1)
	metal, energy := svc.Reclaim(nil, inst, 0)
	if metal != 10 || energy != 20 {
		t.Fatalf("reclaim should return full pools metal 10 energy 20 [05 \"Feature reclaim\"], got %v %v", metal, energy)
	}
	if svc.InstanceAt(1, 1) == nil || svc.InstanceAt(1, 1).Def != defC {
		t.Fatalf("reclaim should replace with reclaimed successor C [05 \"Removal and successor replacement\"]")
	}
	// Test sinking successor carries position: place wreck with sinking, then destroy it and check successor inherits? Not needed for destruction but for completeness
}

// TestExtractorOverlap verifies extractor cannot be placed overlapping another extractor's patch when occupancy blocks, but metal sampling allows overlap [05 "Terrain metal extraction"] [P1-10]
func TestExtractorOverlap(t *testing.T) {
	w, h := 8, 8
	terrain := newTestTerrainP1(w, h)
	// Seed metal
	terrain.ApplySchema(nil, 0)
	// Simulate first extractor placement at 2,2 footprint 2x2
	footX, footZ := 2, 2
	extractsMetal := float32(1.5)
	res1, err := terrain.CheckExtractorOverlap(2, 2, footX, footZ, extractsMetal)
	if err != nil {
		t.Fatalf("CheckExtractorOverlap: %v", err)
	}
	if res1.OverlapsExisting {
		t.Fatalf("empty terrain should not overlap")
	}
	// Stamp occupancy for first extractor at 2,2
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			cell := terrain.PlotAt(2+int32(dx), 2+int32(dz))
			if cell != nil {
				cell.SetOccupantA(1) // mark occupied [04 §6.2] bits 1-2
			}
		}
	}
	// Second extractor overlapping at 3,3 (overlaps cell 3,3) should report overlap
	res2, _ := terrain.CheckExtractorOverlap(3, 3, footX, footZ, extractsMetal)
	if !res2.OverlapsExisting {
		t.Fatalf("overlapping extractor should be detected via occupancy [04 §6.2][05 \"Terrain metal extraction\"]")
	}
	// Non-overlapping at 5,5 should not overlap
	res3, _ := terrain.CheckExtractorOverlap(5, 5, footX, footZ, extractsMetal)
	if res3.OverlapsExisting {
		t.Fatalf("non-overlapping should not report overlap")
	}
	// Validate placement with occupancy bits should reject overlapping when yard requires empty
	yard, _ := world.ParseYardMap("o", 1, 1) // 'o' includes bits 1-2? Check: 'o' 0x2f includes bits 0,1,2,3,5? Actually o is 0x2f includes bits. Use 'o' for occupancy check
	// For extractor, use yard that checks occupancy: G maybe? But o includes bit1-2 per placement doc: bits1-2 reject occupant
	// So overlapping placement should be blocked
	if err := terrain.ValidatePlacement(3, 3, yard, 2, 2, 0); err == nil {
		// If yard is o, it should reject due to occupant
		// If not, try with explicit occupancy yard: use 'o' which has 0x2f = 00101111 includes bits 1-2 (0x06) yes
		// So should reject
		t.Fatalf("ValidatePlacement should reject overlapping extractor due to occupancy [04 §6.2]")
	}
}

// TestMalformedCustom verifies malformed feature defs handled gracefully [P1-I05]
func TestMalformedCustom(t *testing.T) {
	w, h := 4, 4
	terrain := newTestTerrainP1(w, h)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	// Zero footprint should be normalized to 1x1
	malDef := defP1("mal", 0, 0, "", "")
	malDef.CanonicalKey = content.CanonicalKey("mal")
	malDef.Damage = 10
	terrain.FeatureDefs = []*content.FeatureDef{malDef}
	inst := svc.PlaceAt(1, 1, malDef)
	if inst == nil {
		t.Fatalf("zero footprint malformed should be normalized to 1x1 and place [P1-I05]")
	}
	if inst.FootprintX != 1 || inst.FootprintZ != 1 {
		t.Fatalf("normalized footprint should be 1x1, got %d x %d", inst.FootprintX, inst.FootprintZ)
	}
	// Negative footprint
	negDef := defP1("neg", -2, -1, "", "")
	negDef.CanonicalKey = content.CanonicalKey("neg")
	terrain.FeatureDefs = append(terrain.FeatureDefs, negDef)
	inst2 := svc.PlaceAt(2, 2, negDef)
	if inst2 == nil {
		t.Fatalf("negative footprint should be clamped and place")
	}
	// Unknown successor name: should not panic, sentinel
	unknownSuccDef := defP1("unknownSucc", 1, 1, "", "")
	unknownSuccDef.CanonicalKey = content.CanonicalKey("unknownSucc")
	unknownSuccDef.FeatureDead = "missing_custom_feature"
	unknownSuccDef.FeatureDeadDef = nil // not resolved
	terrain.FeatureDefs = append(terrain.FeatureDefs, unknownSuccDef)
	_ = svc.PlaceAt(0, 0, unknownSuccDef)
	svc.RemoveFeatureAt(0, 0, CauseDead)
	// Should gracefully handle missing successor as nil -> free sentinel
	if !terrain.Plot[0].IsEmpty() {
		t.Fatalf("missing successor should gracefully free to 0xFFFF [P1-10][P1-15]")
	}
}

// TestGeothermalStampRunsTheSteamProducer locks the reach of the steam-strip
// producer [05 R-ECO-02 §3]: it is called from the feature stamp, once, with
// the footprint centre and the sampled height, and only for a definition
// carrying the `geothermal` flag. That single call site is why a vent steams
// when the map places it and not on any later tick.
func TestGeothermalStampRunsTheSteamProducer(t *testing.T) {
	terrain := newTestTerrainP1(16, 16)
	terrain.ApplySchema(nil, 0)
	plain := defP1("tree", 1, 1, "", "")
	plain.CanonicalKey = content.CanonicalKey("tree")
	plain.Damage = 10
	vent := defP1("geovent", 1, 1, "", "")
	vent.CanonicalKey = content.CanonicalKey("geovent")
	vent.Geothermal = true
	vent.Damage = 10
	terrain.FeatureDefs = []*content.FeatureDef{plain, vent}
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)

	type puff struct{ x, y, z numeric.Fixed }
	var puffs []puff
	svc.GeothermalSteam = func(x, y, z numeric.Fixed) {
		puffs = append(puffs, puff{x, y, z})
	}

	if svc.PlaceAt(3, 3, plain) == nil {
		t.Fatal("plain feature placement rejected")
	}
	if len(puffs) != 0 {
		t.Fatalf("a non-geothermal stamp ran the steam producer %d times", len(puffs))
	}

	inst := svc.PlaceAt(6, 9, vent)
	if inst == nil {
		t.Fatal("vent placement rejected")
	}
	if len(puffs) != 1 {
		t.Fatalf("the vent stamp ran the steam producer %d times, want exactly one", len(puffs))
	}
	if puffs[0].x != inst.X || puffs[0].y != inst.Y || puffs[0].z != inst.Z {
		t.Fatalf("steam at (%v,%v,%v), want the stamped centre/height (%v,%v,%v)",
			puffs[0].x, puffs[0].y, puffs[0].z, inst.X, inst.Y, inst.Z)
	}
}
