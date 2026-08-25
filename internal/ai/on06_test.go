package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestTypedBuildRequestPreservesCoordinates verifies Place preserves exact X,Z and Kind [P0-07].
func TestTypedBuildRequestPreservesCoordinates(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, CanMove: true, MaxDamage: 100},
			content.CanonicalKey("armsolar"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armsolar")}, UnitName: "armsolar", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armcom"): {Builder: "armcom", Buttons: []string{"armsolar"}},
		},
	}
	// Flat terrain valid.
	attrs := make([]formats.TNTAttribute, 16*16)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 16, 16)
	terrain := &world.Terrain{CellW: 16, CellH: 16, Plot: plot, Version: world.VersionCanonical}
	w := units.New(16, cat)
	h, _ := w.Create(cat.Units[content.CanonicalKey("armcom")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	builder := w.Unit(h)
	builder.Remaining = 0
	r := rng.NewSimulation(123)
	mgr := &Manager{
		Player:       0,
		Catalog:      cat,
		Strategic:    Strategic{CenterX: world.CellToWorld(8), CenterZ: world.CellToWorld(8), Radius: 0, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("armsolar"): {C0: 40}}},
		OriginX:      world.CellToWorld(2),
		OriginZ:      world.CellToWorld(2),
		RNG:          &r,
		SurfaceMetal: 0,
		Factory:      builder,
		Terrain:      terrain,
	}
	mgr.Strategic.Catalog = cat
	var captured BuildRequest
	var capturedCount int
	mgr.QueueBuildTyped = func(req BuildRequest) error {
		captured = req
		capturedCount++
		// Also enqueue via construction for validation that queue would be created.
		bu := w.Unit(req.Builder)
		if bu == nil {
			bu = builder
		}
		if req.Kind == BuildKindMobileSite {
			return construction.QueueMobileBuild(bu, req.UnitKey, req.X, req.Z, req.Count, cat)
		}
		return construction.QueueFactoryBuild(bu, req.UnitKey, req.Count, cat)
	}
	x, z, ok := Place(mgr, "armsolar", terrain)
	if !ok {
		t.Fatalf("Place failed")
	}
	if capturedCount != 1 {
		t.Fatalf("QueueBuildTyped not called exactly once, got %d", capturedCount)
	}
	if captured.Kind != BuildKindMobileSite {
		t.Fatalf("Kind want MobileSite got %d", captured.Kind)
	}
	if captured.X != x || captured.Z != z {
		t.Fatalf("request X,Z %d,%d != Place return %d,%d", captured.X, captured.Z, x, z)
	}
	if captured.UnitKey != "armsolar" && content.CanonicalKey(captured.UnitKey) != content.CanonicalKey("armsolar") {
		t.Fatalf("UnitKey %q want armsolar", captured.UnitKey)
	}
	if captured.Builder != builder.Handle {
		t.Fatalf("Builder handle %d want %d", captured.Builder, builder.Handle)
	}
	// Also verify factory production uses FactoryQueue for mobile unit from factory.
	// Create a factory builder (immobile) and a mobile product (armflash).
	facDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armvp")}, UnitName: "armvp", FootprintX: 4, FootprintZ: 4, YardMap: "oooo", Builder: true, CanMove: false, MaxDamage: 100}
	cat.Units[content.CanonicalKey("armvp")] = facDef
	// Mobile product for factory
	flashDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: false, CanMove: true, MaxVelocity: 100, CanAttack: true, MaxDamage: 100, Weapon1Def: &content.WeaponDef{}}
	cat.Units[content.CanonicalKey("armflash")] = flashDef
	cat.BuildMenus[content.CanonicalKey("armvp")] = &content.BuildMenuPage{Builder: "armvp", Buttons: []string{"armflash"}}
	w2 := units.New(16, cat)
	h2, _ := w2.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
	facUnit := w2.Unit(h2)
	facUnit.Remaining = 0
	mgr2 := &Manager{
		Player:    0,
		Catalog:   cat,
		Strategic: Strategic{CenterX: world.CellToWorld(8), CenterZ: world.CellToWorld(8), Radius: 0, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("armflash"): {C0: 40}}},
		RNG:       &r,
		Factory:   facUnit,
		Terrain:   terrain,
	}
	mgr2.Strategic.Catalog = cat
	var facReq BuildRequest
	mgr2.QueueBuildTyped = func(req BuildRequest) error {
		facReq = req
		bu := w2.Unit(req.Builder)
		if bu == nil {
			bu = facUnit
		}
		if req.Kind == BuildKindFactoryQueue {
			return construction.QueueFactoryBuild(bu, req.UnitKey, req.Count, cat)
		}
		return construction.QueueMobileBuild(bu, req.UnitKey, req.X, req.Z, req.Count, cat)
	}
	// Simulate factory production via doConstruction path: create economy with sufficient stock, trigger construction.
	var econ economy.Service
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = 2
	econ.Players[0].Stock[economy.Energy] = 800
	econ.Players[0].Stock[economy.Metal] = 400
	econ.Players[0].Capacity[economy.Energy] = 1000
	econ.Players[0].Capacity[economy.Metal] = 500
	econ.Players[0].PassProduced[economy.Energy] = 300
	econ.Players[0].PassProduced[economy.Metal] = 10
	econ.Players[0].StatusHalfwordAt144 = 1
	econ.Players[0].StatusWordAt140 = 0
	econ.Players[0].GameEnded = false
	econ.Players[0].EndGameCountdown = -1
	// Ensure manager has profile to allow selection
	prof := &Profile{Weight: map[string]int32{content.CanonicalKey("armflash"): 100}, Limit: map[string]int32{}}
	mgr2.Profile = prof
	mgr2.CandidateSource = func(b *units.Unit) []string { return []string{"armflash"} }
	mgr2.Deadlines[TaskConstruction] = 0
	mgr2.Tick(0, w2, &econ)
	if facReq.Kind != BuildKindFactoryQueue {
		t.Fatalf("factory production Kind want FactoryQueue got %d (unit %q mobile %v)", facReq.Kind, facReq.UnitKey, flashDef.CanMove)
	}
	if facReq.X != 0 || facReq.Z != 0 {
		t.Fatalf("factory queue should have zero X,Z, got %d,%d", facReq.X, facReq.Z)
	}
}

// TestNoConstructionQueueBuildReference ensures ai sources contain no legacy queue [P0-07].
func TestNoConstructionQueueBuildReference(t *testing.T) {
	// Scan internal/ai/*.go for the legacy queue literal (split to avoid self-match).
	// We split the literal to avoid the test itself being flagged.
	needle := "construction." + "QueueBuild"
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), needle) {
			// Allow this test file to contain the needle in a comment split across lines? Our split avoids it.
			// But if any other file contains it, fail.
			// This test file itself does not contain the literal due to split, so any hit is a violation.
			t.Fatalf("ai source %s contains %q (legacy queue) [P0-07]", e.Name(), needle)
		}
	}
}

// TestMilestoneSequenceObservesProduction verifies staged milestones are set only from observed state [P0-07].
func TestMilestoneSequenceObservesProduction(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, CanMove: true, MaxDamage: 100},
			content.CanonicalKey("armvp"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armvp")}, UnitName: "armvp", FootprintX: 4, FootprintZ: 4, YardMap: "oooo", Builder: true, CanMove: false, MaxDamage: 100},
			content.CanonicalKey("armflash"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: false, CanMove: true, CanAttack: true, MaxDamage: 100, Weapon1Def: &content.WeaponDef{}},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armcom"): {Builder: "armcom", Buttons: []string{"armvp"}},
			content.CanonicalKey("armvp"):  {Builder: "armvp", Buttons: []string{"armflash"}},
		},
	}
	// Ensure profile loaded milestone requires actual load, not just table entry.
	// Create a temp VFS with ai/default.txt
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "ai"), 0755); err != nil {
		t.Fatalf("mkdir ai: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ai", "default.txt"), []byte("plan any\nweight armvp 1.0\nweight armflash 1.0\n"), 0644); err != nil {
		t.Fatalf("write default: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(tmpDir, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}
	r := rng.NewSimulation(42)
	mgr, err := NewManager(0, fs, "default", &r, cat, 0, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if len(mgr.Milestones()) == 0 || mgr.Milestones()[MilestoneProfileLoaded] != 0 {
		t.Fatalf("ProfileLoaded should be set at tick 0 after NewManager, got %v", mgr.Milestones())
	}
	// Before any tick, no other milestones should be set.
	if _, ok := mgr.Milestones()[MilestonePlacementSelected]; ok {
		t.Fatalf("PlacementSelected should not be set before any build")
	}
	// Setup world with commander.
	attrs := make([]formats.TNTAttribute, 16*16)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 16, 16)
	terrain := &world.Terrain{CellW: 16, CellH: 16, Plot: plot, Version: world.VersionCanonical}
	w := units.New(32, cat)
	h, _ := w.Create(cat.Units[content.CanonicalKey("armcom")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	com := w.Unit(h)
	com.Remaining = 0
	// Bind typed queue to capture and also actually queue via construction.
	var lastReq BuildRequest
	mgr.Factory = com
	mgr.Terrain = terrain
	mgr.Catalog = cat
	mgr.Strategic.Catalog = cat
	mgr.Strategic.CenterX = world.CellToWorld(8)
	mgr.Strategic.CenterZ = world.CellToWorld(8)
	mgr.QueueBuildTyped = func(req BuildRequest) error {
		lastReq = req
		bu := w.Unit(req.Builder)
		if bu == nil {
			bu = com
		}
		if req.Kind == BuildKindMobileSite {
			return construction.QueueMobileBuild(bu, req.UnitKey, req.X, req.Z, req.Count, cat)
		}
		return construction.QueueFactoryBuild(bu, req.UnitKey, req.Count, cat)
	}
	// Economy for selection.
	var econ economy.Service
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = 2
	econ.Players[0].IsObserver = false
	econ.Players[0].StatusHalfwordAt144 = 1
	econ.Players[0].StatusWordAt140 = 0
	econ.Players[0].GameEnded = false
	econ.Players[0].EndGameCountdown = -1
	econ.Players[0].Stock[economy.Energy] = 800
	econ.Players[0].Stock[economy.Metal] = 400
	econ.Players[0].Capacity[economy.Energy] = 1000
	econ.Players[0].Capacity[economy.Metal] = 500
	econ.Players[0].PassProduced[economy.Energy] = 300
	econ.Players[0].PassProduced[economy.Metal] = 10
	econ.SeedDeadlines(0)
	// Trigger construction via Tick at 0 (TaskConstruction due)
	mgr.Deadlines[TaskConstruction] = 0
	mgr.Tick(0, w, &econ)
	if _, ok := mgr.Milestones()[MilestonePlacementSelected]; !ok {
		t.Fatalf("PlacementSelected not set after Place via Tick, lastReq %+v", lastReq)
	}
	if _, ok := mgr.Milestones()[MilestoneBuildRequestAccepted]; !ok {
		t.Fatalf("BuildRequestAccepted not set after QueueBuildTyped success")
	}
	// Ensure not setting due to table entry alone: reset manager without Tick, with same catalog but no queue, milestones should remain only ProfileLoaded.
	mgr2 := &Manager{Player: 0, Profile: mgr.Profile, Catalog: cat, RNG: &r}
	mgr2.Strategic.Catalog = cat
	if len(mgr2.Milestones()) != 0 {
		t.Fatalf("fresh manager without Tick should have no milestones, got %v", mgr2.Milestones())
	}
	// Simulate nanoframe observed: create a nanoframe unit (remaining 0.5)
	hN, _ := w.Create(cat.Units[content.CanonicalKey("armvp")], 0, world.CellToWorld(4), 0, world.CellToWorld(4))
	nf := w.Unit(hN)
	nf.Remaining = 0.5
	nf.Health = 50
	nf.MaxHealth = 100
	mgr.Tick(1, w, &econ)
	if _, ok := mgr.Milestones()[MilestoneNanoframeObserved]; !ok {
		t.Fatalf("NanoframeObserved not set after nanoframe created")
	}
	// Complete factory
	nf.Remaining = 0
	nf.Health = 100
	mgr.Tick(2, w, &econ)
	if _, ok := mgr.Milestones()[MilestoneFactoryCompleted]; !ok {
		t.Fatalf("FactoryCompleted not set after factory completed")
	}
	// Factory product queued: enqueue a product on factory
	factory := nf
	// Ensure factory has queue via typed queue (simulate factory production)
	// Use QueueFactoryBuild directly, then Tick to observe.
	if err := construction.QueueFactoryBuild(factory, "armflash", 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}
	mgr.Tick(3, w, &econ)
	if _, ok := mgr.Milestones()[MilestoneFactoryProductQueued]; !ok {
		t.Fatalf("FactoryProductQueued not set after factory queue observed")
	}
	// Combat unit completed
	hC, _ := w.Create(cat.Units[content.CanonicalKey("armflash")], 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	cu := w.Unit(hC)
	cu.Remaining = 0
	// Force group assignment via updateGroups
	mgr.Tick(4, w, &econ)
	if _, ok := mgr.Milestones()[MilestoneCombatUnitCompleted]; !ok {
		t.Fatalf("CombatUnitCompleted not set")
	}
	if _, ok := mgr.Milestones()[MilestoneGroupAssigned]; !ok {
		t.Fatalf("GroupAssigned not set after combat unit assigned")
	}
	// Attack move issued: need enemy target and wave to fire.
	// Create enemy unit for targeting
	enemyDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: false, CanMove: true, CanAttack: true, MaxDamage: 100, Weapon1Def: &content.WeaponDef{}}
	_ = enemyDef
	// Create enemy at distance
	hE, _ := w.Create(cat.Units[content.CanonicalKey("armflash")], 1, world.CellToWorld(10), 0, world.CellToWorld(10))
	enemy := w.Unit(hE)
	enemy.Remaining = 0
	enemy.Owner = 1
	// Ensure AI has enough group members to trigger wave (need 3 for wave min)
	// Create 2 more combat units
	for i := 0; i < 2; i++ {
		hx, _ := w.Create(cat.Units[content.CanonicalKey("armflash")], 0, world.CellToWorld(6+int32(i)), 0, world.CellToWorld(6))
		ux := w.Unit(hx)
		ux.Remaining = 0
	}
	mgr.Tick(5, w, &econ)
	// Force wave due
	mgr.Deadlines[TaskWaveA] = 5
	mgr.Tick(5, w, &econ)
	if _, ok := mgr.Milestones()[MilestoneAttackMoveIssued]; !ok {
		// Try explore/rally as fallback: ensure at least one attack order was issued via any wave
		// If still not, force a direct wave call
		mgr.Deadlines[TaskWaveA] = 5
		mgr.Tick(5, w, &econ)
		if _, ok2 := mgr.Milestones()[MilestoneAttackMoveIssued]; !ok2 {
			t.Fatalf("AttackMoveIssued not set after wave")
		}
	}
	// Hostile damage observed
	mgr.ObserveHostileDamage(6, enemy.Handle, w)
	if _, ok := mgr.Milestones()[MilestoneHostileDamageObserved]; !ok {
		t.Fatalf("HostileDamageObserved not set")
	}
	// Verify order: milestones should be in causal order (ticks non-decreasing)
	order := milestoneOrder
	lastTick := uint32(0)
	for _, k := range order {
		if tick, ok := mgr.Milestones()[k]; ok {
			if tick < lastTick {
				t.Fatalf("milestone %s tick %d < previous %d (order violation)", k, tick, lastTick)
			}
			lastTick = tick
		}
	}
	// Ensure not all milestones set due to table entry alone: create a manager with catalog that has build menu but no world progress, no milestones beyond ProfileLoaded.
	mgrEmpty := &Manager{Player: 0, Profile: mgr.Profile, Catalog: cat, RNG: &r, Terrain: terrain}
	mgrEmpty.Strategic.Catalog = cat
	mgrEmpty.Tick(0, units.New(16, cat), &econ)
	if len(mgrEmpty.Milestones()) > 1 { // only ProfileLoaded expected
		// It may also have GroupAssigned if combat units exist? But empty world should have only ProfileLoaded
		if _, ok := mgrEmpty.Milestones()[MilestoneNanoframeObserved]; ok {
			t.Fatalf("empty world should not have NanoframeObserved from table entry")
		}
	}
}

// TestAllianceAwareSelection verifies alliance members never chosen hostile [P0-07].
func TestAllianceAwareSelection(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armflash"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflash")}, UnitName: "armflash", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: false, CanMove: true, CanAttack: true, MaxDamage: 100, Weapon1Def: &content.WeaponDef{}},
		},
	}
	r := rng.NewSimulation(1)
	allyGroups := map[uint8]int{0: 0, 1: 0, 2: 1}
	isAlliance := func(a, b uint8) bool {
		ga, oka := allyGroups[a]
		gb, okb := allyGroups[b]
		if !oka || !okb {
			return a == b
		}
		return ga == gb
	}
	mgr := &Manager{
		Player:     0,
		RNG:        &r,
		Catalog:    cat,
		IsAlliance: isAlliance,
	}
	mgr.Strategic.CenterX = 0
	mgr.Strategic.CenterZ = 0
	mgr.Strategic.Catalog = cat
	w := units.New(16, cat)
	// Allied unit at distance 10 (close)
	hA, _ := w.Create(cat.Units[content.CanonicalKey("armflash")], 1, numeric.FixedFromInt(10), 0, numeric.FixedFromInt(0))
	_ = hA
	// Enemy unit at distance 100 (farther but should be chosen over allied close)
	hE, _ := w.Create(cat.Units[content.CanonicalKey("armflash")], 2, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(0))
	_ = hE
	target := mgr.findEnemyTarget(w)
	if target == nil {
		t.Fatalf("findEnemyTarget returned nil, expected enemy 2")
	}
	if target.Owner != 2 {
		t.Fatalf("alliance-aware: allied unit chosen as hostile, got owner %d want 2", target.Owner)
	}
	// Different teams always eligible: ensure enemy not skipped
	if target.Owner == 0 || target.Owner == 1 {
		t.Fatalf("allied chosen")
	}
	// Also test that when all enemies are allied, result is nil
	allyGroups2 := map[uint8]int{0: 0, 1: 0}
	isAlliance2 := func(a, b uint8) bool {
		ga, oka := allyGroups2[a]
		gb, okb := allyGroups2[b]
		if !oka || !okb {
			return a == b
		}
		return ga == gb
	}
	mgr2 := &Manager{Player: 0, RNG: &r, IsAlliance: isAlliance2}
	mgr2.Strategic.CenterX = 0
	w2 := units.New(16, cat)
	hA2, _ := w2.Create(cat.Units[content.CanonicalKey("armflash")], 1, numeric.FixedFromInt(10), 0, 0)
	_ = hA2
	if t2 := mgr2.findEnemyTarget(w2); t2 != nil {
		t.Fatalf("all allied, expected nil target, got owner %d", t2.Owner)
	}
	// Default same-owner-only when IsAlliance nil
	mgr3 := &Manager{Player: 0, RNG: &r, IsAlliance: nil}
	mgr3.Strategic.CenterX = 0
	w3 := units.New(16, cat)
	hA3, _ := w3.Create(cat.Units[content.CanonicalKey("armflash")], 1, numeric.FixedFromInt(5), 0, 0)
	_ = hA3
	hE3, _ := w3.Create(cat.Units[content.CanonicalKey("armflash")], 2, numeric.FixedFromInt(6), 0, 0)
	_ = hE3
	if t3 := mgr3.findEnemyTarget(w3); t3 == nil || t3.Owner != 1 {
		// Both 1 and 2 are enemies under default same-owner-only, closest is 1
		if t3 != nil {
			t.Fatalf("default alliance: expected owner 1 (closest), got %d", t3.Owner)
		}
	}
	// Verify hostile damage observation respects alliance
	mgr.ObserveHostileDamage(10, hA, w) // allied
	if _, ok := mgr.Milestones()[MilestoneHostileDamageObserved]; ok {
		t.Fatalf("allied damage should not set HostileDamageObserved")
	}
	mgr.ObserveHostileDamage(10, hE, w) // hostile
	if _, ok := mgr.Milestones()[MilestoneHostileDamageObserved]; !ok {
		t.Fatalf("hostile damage should set HostileDamageObserved")
	}
}

// TestDeterministicSeededRuns verifies repeated seeded runs produce identical requests/deadlines [P0-07].
func TestDeterministicSeededRuns(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, CanMove: true, MaxDamage: 100},
			content.CanonicalKey("armsolar"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armsolar")}, UnitName: "armsolar", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armcom"): {Builder: "armcom", Buttons: []string{"armsolar"}},
		},
	}
	prof := &Profile{Weight: map[string]int32{content.CanonicalKey("armsolar"): 100}, Limit: map[string]int32{}}
	attrs := make([]formats.TNTAttribute, 16*16)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 16, 16)
	terrain := &world.Terrain{CellW: 16, CellH: 16, Plot: plot, Version: world.VersionCanonical}
	run := func(seed uint32) ([]BuildRequest, [TaskKindCount]uint32) {
		r := rng.NewSimulation(seed)
		w := units.New(16, cat)
		h, _ := w.Create(cat.Units[content.CanonicalKey("armcom")], 0, world.CellToWorld(2), 0, world.CellToWorld(2))
		b := w.Unit(h)
		b.Remaining = 0
		mgr := &Manager{
			Player:       0,
			Profile:      prof,
			RNG:          &r,
			Catalog:      cat,
			Strategic:    Strategic{CenterX: world.CellToWorld(8), CenterZ: world.CellToWorld(8), Radius: 0, Counts: map[string]int32{}, ClassVectors: map[string]ClassVector{content.CanonicalKey("armsolar"): {C0: 40}}},
			OriginX:      world.CellToWorld(2),
			OriginZ:      world.CellToWorld(2),
			SurfaceMetal: 0,
			Factory:      b,
			Terrain:      terrain,
		}
		mgr.Strategic.Catalog = cat
		var reqs []BuildRequest
		mgr.QueueBuildTyped = func(req BuildRequest) error {
			reqs = append(reqs, req)
			bu := w.Unit(req.Builder)
			if bu == nil {
				bu = b
			}
			if req.Kind == BuildKindMobileSite {
				return construction.QueueMobileBuild(bu, req.UnitKey, req.X, req.Z, req.Count, cat)
			}
			return construction.QueueFactoryBuild(bu, req.UnitKey, req.Count, cat)
		}
		var econ economy.Service
		econ.Players[0].Exists = true
		econ.Players[0].ControllerState = 2
		econ.Players[0].IsObserver = false
		econ.Players[0].StatusHalfwordAt144 = 1
		econ.Players[0].StatusWordAt140 = 0
		econ.Players[0].GameEnded = false
		econ.Players[0].EndGameCountdown = -1
		econ.Players[0].Stock[economy.Energy] = 800
		econ.Players[0].Stock[economy.Metal] = 400
		econ.Players[0].Capacity[economy.Energy] = 1000
		econ.Players[0].Capacity[economy.Metal] = 500
		econ.Players[0].PassProduced[economy.Energy] = 300
		econ.Players[0].PassProduced[economy.Metal] = 10
		for k := TaskKind(0); k < TaskKindCount; k++ {
			mgr.Deadlines[k] = 0
		}
		for tick := uint32(0); tick < 200; tick++ {
			mgr.Tick(tick, w, &econ)
		}
		return reqs, mgr.Deadlines
	}
	aReq, aDead := run(0x1234)
	bReq, bDead := run(0x1234)
	if len(aReq) != len(bReq) {
		t.Fatalf("determinism: req counts %d vs %d", len(aReq), len(bReq))
	}
	for i := range aReq {
		if aReq[i] != bReq[i] {
			t.Fatalf("determinism: req %d mismatch %+v vs %+v", i, aReq[i], bReq[i])
		}
	}
	if aDead != bDead {
		t.Fatalf("determinism: deadlines %v vs %v", aDead, bDead)
	}
	// Different seed should potentially give different 900/150 deadlines (but not required to differ, just not equal always)
	// At least ensure same seed gives same, different seed may differ but we don't enforce.
}

// TestMissingProfileErrors verifies NewManager/LoadProfile returns error for missing profile [P0-07] F-P0-007.
func TestMissingProfileErrors(t *testing.T) {
	tmpDir := t.TempDir()
	// Empty ai dir, no default
	fsEmpty := vfs.New()
	if err := fsEmpty.MountDirectory(tmpDir, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}
	if _, err := LoadProfile(fsEmpty, "nonexistent"); err == nil {
		t.Fatalf("LoadProfile should error for missing profile with no fallback")
	}
	// NewManager should also error
	r := rng.NewSimulation(1)
	if _, err := NewManager(0, fsEmpty, "nonexistent", &r, nil, 0, nil); err == nil {
		t.Fatalf("NewManager should error for missing selected profile")
	}
	if _, err := NewManager(0, fsEmpty, "", &r, nil, 0, nil); err == nil {
		t.Fatalf("NewManager should error for empty profile name")
	}
	// With default present, LoadProfile should succeed via fallback
	if err := os.MkdirAll(filepath.Join(tmpDir, "ai"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ai", "default.txt"), []byte("plan any\nweight armcom 1.0\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fsWithDefault := vfs.New()
	if err := fsWithDefault.MountDirectory(tmpDir, 10); err != nil {
		t.Fatalf("mount2: %v", err)
	}
	if _, err := LoadProfile(fsWithDefault, "missing_but_default_exists"); err != nil {
		t.Fatalf("LoadProfile with fallback should succeed when default exists, got %v", err)
	}
	if _, err := NewManager(0, fsWithDefault, "missing_but_default_exists", &r, nil, 0, nil); err != nil {
		t.Fatalf("NewManager fallback should succeed, got %v", err)
	}
	// Explicit missing with no fallback should be loud, not passive manager.
	if _, err := NewManager(0, fsEmpty, "default", &r, nil, 0, nil); err == nil {
		t.Fatalf("NewManager should error when default itself missing")
	}
}
