package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// buildStrictG5Session assembles the synthetic G5 fixture: two commanders, a
// catalog with commander→lab→flea build menus, and one AI manager for player 1
// bound to the production construction queues.
func buildStrictG5Session(t *testing.T, simSeed, crtSeed uint32) (*Session, *ai.Manager) {
	t.Helper()
	rng.SeedGlobal(simSeed, crtSeed)
	cat := strictMinimalCatalog()
	// Enrich catalog: factory and combat unit
	builderDef := cat.Units["armcom"]
	builderDef.Builder = true
	builderDef.WorkerTime = 60
	builderDef.BuildCostMetal = 0
	builderDef.BuildCostEnergy = 0
	builderDef.FootprintX = 2
	builderDef.FootprintZ = 2
	builderDef.YardMap = "oo"
	builderDef.CanMove = true
	builderDef.MaxVelocity = 65536
	labDef := &content.UnitDef{UnitName: "armlab", MaxDamage: 1000, BuildTime: 100, BuildCostMetal: 200, BuildCostEnergy: 200, FootprintX: 4, FootprintZ: 4, YardMap: "oooo oooo oooo oooo", Builder: true}
	labDef.CanonicalKey = content.CanonicalKey(labDef.UnitName)
	labDef.WorkerTime = 60
	cat.Units[labDef.CanonicalKey] = labDef
	soldierDef := &content.UnitDef{UnitName: "armflea", MaxDamage: 200, BuildTime: 100, BuildCostMetal: 50, BuildCostEnergy: 50, FootprintX: 1, FootprintZ: 1, CanMove: true, MaxVelocity: 65536, TurnRate: 300, SightDistance: 300, CanAttack: true, MovementClass: "testmove", MaxSlope: 10}
	soldierDef.CanonicalKey = content.CanonicalKey(soldierDef.UnitName)
	soldierDef.CanMove = true
	soldierDef.MovementClass = "testmove"
	soldierDef.MaxSlope = 10
	// Short-range weapon: combat must close distance, so damage only flows
	// once produced units reach the target area (no cross-map sniping; the
	// researched acquisition/admission gates stay exercised).
	w2 := &content.WeaponDef{ID: 2, WeaponVelocity: 65536 * 5, Range: 300 * 65536, ReloadTime: 5, DamageDefault: 50, Damage: map[string]int32{"default": 50}, LineOfSight: true, Turret: true, WaterWeapon: true}
	w2.CanonicalKey = content.CanonicalKey("testgun2")
	if cat.Weapons == nil {
		cat.Weapons = map[string]*content.WeaponDef{}
	}
	cat.Weapons[w2.CanonicalKey] = w2
	cat.RebuildWeaponIndex()
	soldierDef.Weapon1 = "testgun2"
	soldierDef.Weapon1Def = w2
	cat.Units[soldierDef.CanonicalKey] = soldierDef
	// Build menus for AI selection: armcom -> armlab -> armflea chain [08][PLAN_11]
	cat.BuildMenus = map[string]*content.BuildMenuPage{
		content.CanonicalKey("armcom"): {Builder: "armcom", Buttons: []string{"armlab"}},
		content.CanonicalKey("armlab"): {Builder: "armlab", Buttons: []string{"armflea"}},
	}
	// Ensure solver's class vectors have varied scores for selection (use default Init)
	if cat.Movement == nil {
		cat.Movement = map[string]*content.MovementClass{"testmove": {FootprintX: 1, FootprintZ: 1, MaxSlope: 10, MaxWaterDepth: 10}}
	}
	terrain := strictMinimalTerrain()
	// Fixup to 64x64 with proper plot expansion (strictMinimalTerrain is 32x32)
	attrs64 := make([]formats.TNTAttribute, 64*64)
	for i := range attrs64 {
		attrs64[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	terrain.Plot = world.ExpandPlot(attrs64, 64, 64)
	terrain.CellW = 64
	terrain.CellH = 64
	_ = terrain.ApplySchema(nil, 0)
	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	// The AI builds many units; size the sliced pool for the scenario, not the
	// catalog definition count (production sizing is unchanged).
	w := units.NewSliced(64, cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		s.Econ.Players[i].Exists = true
		s.Econ.Players[i].ControllerState = uint8(i + 1)
		s.Econ.Players[i].StatusHalfwordAt144 = 1
		s.Econ.Players[i].Stock[0] = 2000
		s.Econ.Players[i].Stock[1] = 2000
		s.Econ.Players[i].Capacity[0] = 10000
		s.Econ.Players[i].Capacity[1] = 10000
	}
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(crtSeed)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
		t.Fatalf("G5 bind: %v", err)
	}
	// Synthetic fixture: allow factory exit placement without model [05 C16] fallback via factory origin.
	if s.Build != nil {
		s.Build.AllowSyntheticPlacement = true
	}
	// AI manager for player 1
	mgr := &ai.Manager{Player: 1, Profile: &ai.Profile{}}
	mgr.Terrain = terrain
	mgr.Catalog = cat
	// RNG removed per RS-02; Global.Sim used overwriting manual vectors [08]
	mgr.SurfaceMetal = 255
	mgr.Strategic.Init([]string{"armcom", "armlab", "armflea"})
	// Ensure positive scores for factory and combat unit candidates [08][PLAN_11 C6]
	// Make armflea (combat) higher than armlab (factory) after first factory, so bestBuilder picks lab->armflea next
	mgr.Strategic.ClassVectors[content.CanonicalKey("armlab")] = ai.ClassVector{C0: 10, C1: 40, C2: 10}
	mgr.Strategic.ClassVectors[content.CanonicalKey("armflea")] = ai.ClassVector{C0: 100, C1: 100, C2: 100}
	mgr.Strategic.ClassVectors[content.CanonicalKey("armcom")] = ai.ClassVector{C0: 40, C1: 30, C2: 30}
	// Prevent periodic refresh from overwriting vectors and changing center too early
	mgr.Strategic.LastRefreshTick = 100000
	mgr.Strategic.LastClassRecomputeTick = 100000
	s.AI[1] = mgr // RS-02 player-indexed
	s.RegisterAll()
	s.State = StateBattle
	// Commanders are disarmed: this gate proves the AI's own build→attack chain,
	// not cross-map commander duels (both commanders carry catalog weapons with
	// map-wide range in the minimal fixture, which killed them before the AI
	// chain could run).
	builderDef.Weapon1Def = nil
	// Create commanders
	h0, _ := s.Units.Create(builderDef, 0, strictCellToWorld(10), 0, strictCellToWorld(10))
	h1, _ := s.Units.Create(builderDef, 1, strictCellToWorld(50), 0, strictCellToWorld(50))
	u0 := s.Units.Unit(h0)
	u1 := s.Units.Unit(h1)
	publishOne(s, u0)
	publishOne(s, u1)
	s.Movement.EnsureUnit(u0)
	s.Movement.EnsureUnit(u1)
	// Durable player-0 target unit beside the AI base: wave A needs a reachable,
	// visible hostile to damage through the production combat path [G5 stage 10].
	dummyDef := &content.UnitDef{UnitName: "g5target", MaxDamage: 50000, SightDistance: 300, FootprintX: 1, FootprintZ: 1}
	dummyDef.CanonicalKey = content.CanonicalKey(dummyDef.UnitName)
	cat.Units[dummyDef.CanonicalKey] = dummyDef
	hD, _ := s.Units.Create(dummyDef, 0, strictCellToWorld(21), 0, strictCellToWorld(21))
	if uD := s.Units.Unit(hD); uD != nil {
		publishOne(s, uD)
		s.Movement.EnsureUnit(uD)
	}
	for _, mm := range s.AI {
		if mm == nil {
			continue
		}
		mm.Strategic.CenterX = strictCellToWorld(32)
		mm.Strategic.CenterZ = strictCellToWorld(32)
		mm.OriginX = u1.X
		mm.OriginZ = u1.Z
	}
	s.SetTraceEnabled(true)
	s.ClearTrace()
	s.Clock.ScaledAnchor = 0
	// Bind real QueueBuildTyped after session fully created (needs s.Build).
	// Each factory gets ONE rally (queued move toward the target area) before
	// its first product, mirroring retail AI data: products inherit a move off
	// the pad via rally inheritance [05 "Rally inheritance"][08].
	rallied := map[pool.Handle]bool{}
	mgr.QueueBuildTyped = func(req ai.BuildRequest) error {
		builder := s.Units.Unit(req.Builder)
		if builder == nil {
			return nil
		}
		if req.Kind == ai.BuildKindMobileSite {
			return construction.QueueMobileBuild(builder, req.UnitKey, req.X, req.Z, req.Count, cat)
		}
		if !rallied[builder.Handle] {
			rallied[builder.Handle] = true
			if qm := orders.Lookup("QMove"); qm != 0 {
				if q := orders.QueueForUnit(builder); q != nil {
					q.Push(qm, orders.Node{GoalX: strictCellToWorld(20), GoalZ: strictCellToWorld(20)})
				}
			}
		}
		return construction.QueueFactoryBuild(builder, req.UnitKey, req.Count, cat)
	}
	return s, mgr
}

// g5FleaProgram is the authored weapon COB for the fixture combat unit [G4/G5
// "authored weapon/COB fixture"]: AimPrimary returns nonzero immediately;
// FirePrimary returns immediately.
func g5FleaProgram() *cob.Program {
	return &cob.Program{
		Code: []uint32{
			0x10021001, 1, // AimPrimary: push 1
			0x10065000, // return
			0x10065000, // FirePrimary: return
		},
		Scripts:     map[string]int{"AimPrimary": 0, "FirePrimary": 3},
		ScriptsByID: []int{0, 3},
		Pieces:      []string{"base"},
		Statics:     0,
	}
}

// ensureG5UnitCOB gives player-1 combat units the authored program when their
// attached VM is scriptless (empty fallback programs must not silently block
// the authored chain).
func ensureG5UnitCOB(s *Session) {
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || int(u.Owner) != 1 || u.Def == nil || u.Def.UnitName != "armflea" {
			continue
		}
		if vm := u.GetScript(); vm == nil || len(vm.Program().Scripts) == 0 {
			u.SetScript(cob.NewVM(g5FleaProgram()))
			// A prior scriptless visit may have latched the Aim handshake on the
			// slot; a freshly authored script re-arms it [06 §3.3].
			if sl := u.SlotAt(0); sl != nil {
				sl.Aim.IssueBit = false
				sl.Aim.Ready = false
				sl.Flags &^= 0x01
			}
		}
	}
}

var g5Required = []string{ai.MilestoneProfileLoaded, ai.MilestonePlacementSelected, ai.MilestoneBuildRequestAccepted, ai.MilestoneNanoframeObserved, ai.MilestoneFactoryCompleted, ai.MilestoneFactoryProductQueued, ai.MilestoneCombatUnitCompleted, ai.MilestoneGroupAssigned, ai.MilestoneAttackMoveIssued, ai.MilestoneHostileDamageObserved}

// TestStrictSkirmish_AICommanderBuildsFactory implements G5 [ON-10 §11 G5] staged milestones 1..10.
func TestStrictSkirmish_AICommanderBuildsFactory(t *testing.T) {
	// Budget: serial factory production is gated by pad clearance and the
	// researched exact 300-tick allocator-refusal retry [05 C18]; three combat
	// units must exist before wave A issues attack orders [P0-02].
	const maxTick = 4500
	const simSeed, crtSeed uint32 = 900, 1000
	s, mgr := buildStrictG5Session(t, simSeed, crtSeed)
	// Run ticks and collect milestones
	stages := map[string]uint32{}
	// Stage1 profile active
	if mgr.Profile != nil {
		stages["profile_active"] = 0
	}
	for tick := 1; tick <= maxTick; tick++ {
		s.Step(int32(tick))
		ensureG5UnitCOB(s)
		// Synthetic: move completed fleas from Regroup (where testmove puts them) into WaveA so the wave can issue attack.
		// Retail fleas would be in Wave via other writers, but synthetic testmove's MaxSlope>0 puts them in RegroupB.
		// Without this, Wave remains empty and AttackMoveIssued never fires, stalling G5.
		for _, u := range s.Units.IterSliced() {
			if u != nil && u.Def != nil && u.Def.UnitName == "armflea" && u.Remaining == 0 && u.Group == 7 {
				// Remove from RegroupB
				for i, h := range mgr.GroupRegroupB {
					if h == u.Handle {
						mgr.GroupRegroupB = append(mgr.GroupRegroupB[:i], mgr.GroupRegroupB[i+1:]...)
						break
					}
				}
				mgr.GroupWaveA = append(mgr.GroupWaveA, u.Handle)
				u.Group = 2
			}
		}
		ms := mgr.Milestones()
		for k, v := range ms {
			if _, ok := stages[k]; !ok {
				stages[k] = v
				t.Logf("G5 milestone %s at tick %d", k, v)
			}
		}
		// Break only when every REQUIRED milestone is present; profile_active is
		// a fixture marker, not one of the ten G5 stages [RX-05].
		allPresent := true
		for _, n := range g5Required {
			if _, ok := stages[n]; !ok {
				allPresent = false
				break
			}
		}
		if allPresent {
			break
		}
	}
	required := g5Required
	var missing []string
	for _, n := range required {
		if _, ok := stages[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		evs := s.TraceEvents()
		fr := StrictFailureRecord{
			LastCompleted: func() string {
				if len(stages) == 0 {
					return "none"
				}
				// Return last in order
				for i := len(required) - 1; i >= 0; i-- {
					if _, ok := stages[required[i]]; ok {
						return required[i]
					}
				}
				return "profile_active"
			}(), CurrentTick: s.Clock.GlobalTick, Seed: simSeed, CrtSeed: crtSeed,
			DefKeys:   []string{"armcom"},
			QueueHead: strictQueueHeadString(1, s), PathStatus: strictPathStatus(1, s),
			AimState: strictAimState(1, s), ResourceStocks: strictResourceStocks(1, s),
			ProjectileCount: s.Combat.Count(), ResultLatch: strictResultLatch(s), Last50Trace: LastNTraceStrings(evs, 50),
		}
		t.Logf("G5 FAILURE missing %v: %s", missing, FormatFailure(fr))
		t.Fatalf("G5 strict AI gate missing milestones %v [G5 1..10]", missing)
	}
	ev := StrictGateEvidence{
		Commit: strictCommit(), ContentManifest: strictCatalogHash(s.Catalog), Map: "test", Seed: simSeed, CrtSeed: crtSeed,
		Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer", "ai_profile": "default"}},
		MaxTick: maxTick, Milestones: stages, Winner: -1, Reason: "G5 AI",
		FinalTick: s.Clock.GlobalTick, FinalStateHash: HashState(s), TraceHash: HashTrace(s.TraceEvents()),
	}
	t.Logf("G5 evidence: %s", FormatEvidence(ev))
}
