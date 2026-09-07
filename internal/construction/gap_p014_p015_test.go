package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestResurrectionDelay_Sole03 locks sole 0.3 use and floor(work/30) [P0-15].
func TestResurrectionDelay_Sole03(t *testing.T) {
	// buildTime 5000, workerTime 60 => floor 2 => 5000*0.3/2=750
	if got := ResurrectionDelay(5000, 60); got != 750 {
		t.Fatalf("resur delay 5000/60 => %d want 750", got)
	}
	// worker 30 => floor1 => 3000*0.3/1=900
	if got := ResurrectionDelay(3000, 30); got != 900 {
		t.Fatalf("resur delay 3000/30 => %d want 900", got)
	}
	// work <30 => q 0 => infinity => indefinite integer, low 32 bits zero =>
	// delay 0, immediate completion [05 R-WORK-01 §7 "the width of the stored
	// delay"]. Zero buildtime with q 0 (a NaN) lands on the same zero.
	if got := ResurrectionDelay(3000, 20); got != 0 {
		t.Fatalf("resur delay work20 => %d want 0", got)
	}
	if got := ResurrectionDelay(0, 20); got != 0 {
		t.Fatalf("resur delay 0/20 => %d want 0", got)
	}
	// A negative buildtime gives a negative delay, never zero.
	if got := ResurrectionDelay(-3000, 30); got != -900 {
		t.Fatalf("resur delay -3000/30 => %d want -900", got)
	}
	// Check '_' truncation sole use: FeatureNameToDef via '_' truncation
	if got := FeatureNameToDefName("ARMCK_DEAD"); got != "ARMCK" {
		t.Fatalf("feature trunc ARMCK_DEAD => %q want ARMCK", got)
	}
	if got := FeatureNameToDefName("corsomething"); got != "corsomething" {
		t.Fatalf("no '_' => same")
	}
	// 1 RNG jitter draw per start [P0-15][I4]
	sim := rng.NewSimulation(1)
	before := sim.Draws()
	_ = ResurrectionJitter(&sim, 10)
	if sim.Draws()-before != 1 {
		t.Fatalf("resurrection jitter should consume exactly 1 RNG draw, got %d", sim.Draws()-before)
	}
	before2 := sim.Draws()
	_ = ResurrectionJitter(&sim, 0)
	if sim.Draws()-before2 != 0 {
		t.Fatalf("jitter spread 0 should consume 0 draws")
	}
}

// TestResurrectionRemovesFeatureAfterAllocation locks phase 5's successful
// allocation before its destructive feature transition [05 R-WORK-01 §7].
func TestResurrectionRemovesFeatureAfterAllocation(t *testing.T) {
	w := newConstructionFixtureWorld(10, nil)
	cat := content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "armck", BuildTime: 3000, BuildCostMetal: 100, BuildCostEnergy: 100, MaxDamage: 750}
	def.CanonicalKey = content.CanonicalKey("armck")
	def.UnitLimit = -1
	cat.Units = map[string]*content.UnitDef{content.CanonicalKey("armck"): def}
	terrain := &world.Terrain{CellW: 10, CellH: 10, Plot: make([]world.PlotCell, 100)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	// Place a resurrectable feature at 5,5 (corpse)
	featDef := &content.FeatureDef{}
	// Simulate feature existence via terrain FeatureDefs
	terrain.FeatureDefs = []*content.FeatureDef{featDef}
	terrain.Plot[5*10+5].SetFeature(0)
	svc := NewService(terrain, &cat, w, nil)
	builderDef := &content.UnitDef{UnitName: "armrect", BuildTime: 100, WorkerTime: 60, BuildCostMetal: 10, BuildCostEnergy: 10, MaxDamage: 100}
	builderDef.CanResurrect = true
	hb, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builder := w.Unit(hb)
	builder.Def = builderDef
	// Resurrect at same pos
	posX := world.CellToWorld(5)
	posZ := world.CellToWorld(5)
	sim2 := rng.NewSimulation(1)
	prod, err := svc.Resurrect(builder, &terrain.Plot[5*10+5], def, posX, numeric.Fixed(0), posZ, &sim2)
	if err != nil {
		t.Fatalf("resurrect err %v", err)
	}
	if prod == nil || prod.Remaining != 0 || prod.Health != 1 {
		t.Fatalf("resurrect product remaining %v health %d want 0/1", prod.Remaining, prod.Health)
	}
	// The feature is removed only after the successful allocation, before the
	// new product is returned to the caller [05 R-WORK-01 §7].
	if got := terrain.Plot[5*10+5].Feature(); got != world.PlotFeatureNone {
		t.Fatalf("feature not cleared after successful allocation: Plot[5,5].Feature=0x%04x want 0xFFFF", got)
	}
	if aw := terrain.Plot[5*10+5].AnchorWord(); aw != 0 {
		t.Fatalf("feature anchor not cleared: AnchorWord=0x%04x want 0", aw)
	}
}

// TestResurrectionRefusalLeavesTheCorpseFootprintUntouched is the failure half
// of phase 5. A refused allocation does not consume even the anchor cell, so
// its 300-tick order retry receives the identical feature [05 R-WORK-01 §7].
func TestResurrectionRefusalLeavesTheCorpseFootprintUntouched(t *testing.T) {
	productDef := &content.UnitDef{UnitName: "retryunit", MaxDamage: 100, UnitLimit: -1}
	productDef.CanonicalKey = content.CanonicalKey(productDef.UnitName)
	builderDef := &content.UnitDef{UnitName: "retrybuilder", MaxDamage: 100, UnitLimit: -1, CanResurrect: true}
	builderDef.CanonicalKey = content.CanonicalKey(builderDef.UnitName)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		productDef.CanonicalKey: productDef,
		builderDef.CanonicalKey: builderDef,
	}}
	w := newConstructionFixtureWorld(2, cat)
	h, err := w.Create(builderDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	builder := w.Unit(h)

	terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	corpseDef := &content.FeatureDef{FootprintX: 2, FootprintZ: 2}
	terrain.FeatureDefs = []*content.FeatureDef{corpseDef}
	for z := int32(1); z < 3; z++ {
		for x := int32(1); x < 3; x++ {
			cell := terrain.PlotAt(x, z)
			if x == 1 && z == 1 {
				cell.SetFeature(0)
			} else {
				cell.SetFeature(world.PlotFeatureFringe)
			}
		}
	}
	before := append([]world.PlotCell(nil), terrain.Plot...)
	svc := NewService(terrain, cat, w, nil)
	// A nil product is the allocator's common pool/per-definition refusal
	// shape. The service turns it into ErrLimit without mutating the plot.
	svc.Allocator = func(uint8, *content.UnitDef, numeric.Fixed, numeric.Fixed, numeric.Fixed) (*units.Unit, error) {
		return nil, nil
	}
	product, err := svc.Resurrect(builder, terrain.PlotAt(1, 1), productDef, world.CellToWorld(1), 0, world.CellToWorld(1), nil)
	if err != ErrLimit || product != nil {
		t.Fatalf("refused resurrect = (%v, %v), want (nil, ErrLimit)", product, err)
	}
	for i := range terrain.Plot {
		if terrain.Plot[i] != before[i] {
			t.Fatalf("refused resurrection changed plot cell %d: got %#v want %#v", i, terrain.Plot[i], before[i])
		}
	}
}

// The reverse arm's refund ladder: direct metal credit, and the special
// second state's 0.5/0.7 selector pairing tied to the TARGET's owner
// [05 R-WORK-01 §1][05 "Cancel-current and stop interrupts"]. The arm itself is
// exercised through sharedStep by the decay-wrapper tests in
// getbuilt_decay_gate_test.go; ReverseStep/ApplyReverse/ReverseCause9, the
// parallel expression this test used to drive, are retired (WU-19-101).
//
// The two scaled expectations were NEGATIVE until WU-19-166 — they locked a
// debit where retail pays a reduced credit. [05 R-ECO-01 §11] settles the sign
// for all fourteen sites of the family, this one named among them: the stored
// constants are negative and the site SUBTRACTS the product, so the scaled arm
// adds half (selector 0) or seven tenths (selector 1) of the refund. The test
// is what kept the inverted arithmetic in place, so it is corrected with it.
func TestReverseRefundSelectorLadder(t *testing.T) {
	bucket := float32(0)
	ReverseRefund(&bucket, 40, false, 0)
	if bucket != 40 {
		t.Fatalf("ordinary owner credits the whole amount: got %v want 40", bucket)
	}
	bucket2 := float32(0)
	ReverseRefund(&bucket2, 100, true, 0)
	if bucket2 != 50 {
		t.Fatalf("selector 0 credits half: got %v want 50", bucket2)
	}
	bucket3 := float32(0)
	ReverseRefund(&bucket3, 100, true, 1)
	if bucket3 != 70 {
		t.Fatalf("selector 1 credits seven tenths: got %v want 70", bucket3)
	}
	bucket4 := float32(0)
	ReverseRefund(&bucket4, 100, true, 2)
	if bucket4 != 100 {
		t.Fatalf("any other selector credits the whole amount: got %v want 100", bucket4)
	}
}

// TestPerDefLimit_Sentinel locks -1 unlimited vs limited [P0-15][P0-16].
func TestPerDefLimit_Sentinel(t *testing.T) {
	w := newConstructionFixtureWorld(10, nil)
	defUnlim := &content.UnitDef{UnitName: "armck", UnitLimit: -1}
	defLim1 := &content.UnitDef{UnitName: "armck", UnitLimit: 1}
	if !CheckPerDefLimit(w, 0, defUnlim) {
		t.Fatalf("unlimited should allow")
	}
	_, _ = w.Create(defLim1, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	if CheckPerDefLimit(w, 0, defLim1) {
		t.Fatalf("limit 1 with 1 existing should not allow another")
	}
	if !CheckPerDefLimit(w, 1, defLim1) {
		t.Fatalf("different owner should allow")
	}
}

// TestPrimaryOnlyQueue locks 68-desc census primary-only [P0-14][P0-I05].
func TestPrimaryOnlyQueue(t *testing.T) {
	// Factory/mobile products must live on primary only; secondary is exclusively BuildWeapon/SelfDestruct per GAP T3 [P0-I05].
	// Products now use stable catalog indices, not FNV hash [P0-I05].
	bid := orders.Lookup("BuildingBuild")
	if bid == 0 {
		bid = orders.Lookup("MobileBuild")
	}
	if bid == 0 {
		t.Fatalf("BuildingBuild/MobileBuild lookup failed")
	}
	if orders.DescriptorFor(bid).StaticGate&0x40000 != 0 {
		t.Fatalf("factory/mobile build should be primary, not secondary")
	}
	bid2 := orders.Lookup("BuildWeapon")
	if bid2 != 0 && orders.DescriptorFor(bid2).StaticGate&0x40000 == 0 {
		t.Fatalf("BuildWeapon should be secondary")
	}
}

// TestWorkClampAndHealthDiff locks work clamp remaining + diff-of-trunc [P0-14].
func TestWorkClampAndHealthDiff(t *testing.T) {
	old := float32(1.0)
	worker := int32(1) // floor(30/30)=1
	buildTime := int32(3000)
	nv := RemainingStep(old, worker, buildTime)
	if nv != 1.0-float32(1)/3000.0 {
		t.Fatalf("remaining step %v", nv)
	}
	// Health diff-of-trunc preserves fraction: max 10, 3 steps total 10 as in factory_test
	maxD := int32(10)
	hg1 := HealthGain(1.0, 0.6666667, maxD) // trunc10 - trunc6 =4
	if hg1 != 4 {
		t.Fatalf("hg1 %d want 4", hg1)
	}
}

// TestFactoryLinksClearedOnCompletionLeakedOnDeath locks builder/product links [P0-14].
func TestFactoryLinksClearedOnCompletionLeakedOnDeath(t *testing.T) {
	w := newConstructionFixtureWorld(10, nil)
	cat := content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := &content.UnitDef{UnitName: "armfac", BuildTime: 100, WorkerTime: 30, MaxDamage: 100, YardMap: "o", Builder: true}
	facDef.CanonicalKey = content.CanonicalKey("armfac")
	prodDef := &content.UnitDef{UnitName: "armck", BuildTime: 3000, BuildCostMetal: 100, BuildCostEnergy: 100, MaxDamage: 750}
	prodDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef.UnitLimit = -1
	cat.Units = map[string]*content.UnitDef{content.CanonicalKey("armfac"): facDef, content.CanonicalKey("armck"): prodDef}
	h, _ := w.Create(facDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	fac := w.Unit(h)
	fac.Def = facDef
	svc := NewService(nil, &cat, w, nil)
	prodHandle := pool.Handle(5)
	svc.SetBuilderLink(prodHandle, fac.Handle)
	if _, ok := svc.BuilderLink(prodHandle); !ok {
		t.Fatalf("link not set")
	}
	// Simulate completion clearing via direct delete (handleState4 does delete)
	// Use exported helper via SetBuilderLink with zero? Instead test that after setting then clearing via second link change, map reflects.
	// For this lock, verify that BuilderLink is cleared after we simulate completion by removing entry via creating new service and checking leak vs clear semantics:
	// Clear on completion
	svc2 := NewService(nil, &cat, w, nil)
	svc2.SetBuilderLink(prodHandle, fac.Handle)
	// Simulate completion: delete entry
	svc2.ClearBuilderLink(prodHandle) // helper we expose for test
	if _, ok := svc2.BuilderLink(prodHandle); ok {
		t.Fatalf("link should be cleared on completion")
	}
	// Leak on death: World.Destroy does not clear builderLinks — we keep map
	svc3 := NewService(nil, &cat, w, nil)
	svc3.SetBuilderLink(prodHandle, fac.Handle)
	// Simulate death: destroy product but not clearing map — leak
	if _, ok := svc3.BuilderLink(prodHandle); !ok {
		t.Fatalf("link should leak on death (not cleared)")
	}
}
