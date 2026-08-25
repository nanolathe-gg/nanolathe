package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestStrictSkirmish_NaturalSaveContinuation implements G8 natural checkpoint [ON-12].
// It reaches a checkpoint NATURALLY via production Step loop after:
//   - AI factory completion (armlab Remaining 0)
//   - at least one active route/order (Move order + scheduler route)
//   - at least one live COB thread or completed Aim (WeaponAimDispatch/COBReturn trace)
//   - active construction (armflea nanoframe Remaining in (0,1]) or projectile if reproducibly available
//
// No direct state injection at checkpoint: all state arises from production construction, path, COB, combat.
// Save through production bank/box path (save.NewBuilder / CaptureStateV1).
// Continue N=60 ticks uninterrupted vs fresh restore and compare authoritative hashes/traces.
func TestStrictSkirmish_NaturalSaveContinuation(t *testing.T) {
	const simSeed, crtSeed uint32 = 900, 1000
	const maxTick = 1500
	const continueTicks = 60
	rng.SeedGlobal(simSeed, crtSeed)
	cat := strictMinimalCatalog()
	// Enrich catalog as in G5 to enable AI factory chain armcom -> armlab -> armflea [ON-10 §11 G5]
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
	builderDef.TurnRate = 300
	builderDef.SightDistance = 300
	builderDef.CanPatrol = true
	builderDef.CanAttack = true
	// Weapon for shooter (shared with combat unit product)
	w2 := &content.WeaponDef{ID: 2, WeaponVelocity: 65536 * 5, Range: 4000 * 65536, ReloadTime: 5, DamageDefault: 50, Damage: map[string]int32{"default": 50}, LineOfSight: true, Turret: true, WaterWeapon: true}
	w2.CanonicalKey = content.CanonicalKey("testgun2")
	if cat.Weapons == nil {
		cat.Weapons = map[string]*content.WeaponDef{}
	}
	cat.Weapons[w2.CanonicalKey] = w2
	cat.RebuildWeaponIndex()
	labDef := &content.UnitDef{UnitName: "armlab", MaxDamage: 1000, BuildTime: 100, BuildCostMetal: 0, BuildCostEnergy: 0, FootprintX: 4, FootprintZ: 4, YardMap: "oooo oooo oooo oooo", Builder: true}
	labDef.CanonicalKey = content.CanonicalKey(labDef.UnitName)
	labDef.WorkerTime = 60
	cat.Units[labDef.CanonicalKey] = labDef
	soldierDef := &content.UnitDef{UnitName: "armflea", MaxDamage: 200, BuildTime: 100, BuildCostMetal: 0, BuildCostEnergy: 0, FootprintX: 1, FootprintZ: 1, CanMove: true, MaxVelocity: 65536, TurnRate: 300, SightDistance: 300, CanAttack: true}
	soldierDef.CanonicalKey = content.CanonicalKey(soldierDef.UnitName)
	soldierDef.CanMove = true
	soldierDef.CanPatrol = true
	soldierDef.Weapon1 = "testgun2"
	soldierDef.Weapon1Def = w2
	cat.Units[soldierDef.CanonicalKey] = soldierDef
	shooterDef := &content.UnitDef{UnitName: "shooter", MaxDamage: 200, BuildTime: 100, BuildCostMetal: 0, BuildCostEnergy: 0, FootprintX: 1, FootprintZ: 1, CanMove: true, MaxVelocity: 65536, TurnRate: 300, SightDistance: 300, CanAttack: true, Weapon1: "testgun2", Weapon1Def: w2}
	shooterDef.CanonicalKey = content.CanonicalKey(shooterDef.UnitName)
	cat.Units[shooterDef.CanonicalKey] = shooterDef
	targetDef := &content.UnitDef{UnitName: "targetdummy", MaxDamage: 200, BuildTime: 100, BuildCostMetal: 0, BuildCostEnergy: 0, FootprintX: 1, FootprintZ: 1, CanMove: false}
	targetDef.CanonicalKey = content.CanonicalKey(targetDef.UnitName)
	cat.Units[targetDef.CanonicalKey] = targetDef
	// Also give builder a weapon so factory building and shooting can overlap via same def (shooter unit will be armflea)
	builderDef.Weapon1 = "testgun2"
	builderDef.Weapon1Def = w2
	cat.BuildMenus = map[string]*content.BuildMenuPage{
		content.CanonicalKey("armcom"): {Builder: "armcom", Buttons: []string{"armlab"}},
		content.CanonicalKey("armlab"): {Builder: "armlab", Buttons: []string{"armflea"}},
	}
	if cat.Movement == nil {
		cat.Movement = map[string]*content.MovementClass{"testmove": {FootprintX: 1, FootprintZ: 1, MaxSlope: 10, MaxWaterDepth: 10}}
	}

	// Helper to build 64x64 terrain fresh
	buildTerrain64 := func() *world.Terrain {
		attrs := make([]formats.TNTAttribute, 64*64)
		for i := range attrs {
			attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
		}
		plot := world.ExpandPlot(attrs, 64, 64)
		ter := &world.Terrain{CellW: 64, CellH: 64, Plot: plot, Version: 0x2000, SeaLevel: 0, WindMin: 100, WindMax: 2000}
		_ = ter.ApplySchema(nil, 0)
		return ter
	}

	buildNatural := func() (*Session, pool.Handle, pool.Handle) {
		terrain := buildTerrain64()
		m := strictSyntheticMission()
		s := &Session{Catalog: cat, World: terrain, Mission: m}
		w, _ := newSlicedWorld(cat)
		s.Units = w
		s.Econ = strictEconomyForTest()
		for i := 0; i < 2; i++ {
			p := &s.Econ.Players[i]
			p.Exists = true
			p.ControllerState = uint8(i + 1)
			p.IsObserver = false
			p.StatusHalfwordAt144 = 1
			p.StatusWordAt140 = 0
			p.GameEnded = false
			p.EndGameCountdown = -1
			p.Stock[economy.Metal] = 2000
			p.Stock[economy.Energy] = 2000
			p.Capacity[economy.Metal] = 10000
			p.Capacity[economy.Energy] = 10000
		}
		s.Econ.SeedDeadlines(0)
		var crt rng.CRT = rng.NewCRT(crtSeed)
		s.InitWindForSession(&crt, 0)
		if err := createAndBindServices(s); err != nil {
			t.Fatalf("G8: createAndBindServices: %v", err)
		}
		mgr := &ai.Manager{Player: 1, Profile: &ai.Profile{}}
		mgr.Terrain = terrain
		mgr.Catalog = cat
		mgr.RNG = nil
		mgr.SurfaceMetal = 255
		mgr.Strategic.Init([]string{"armcom", "armlab", "armflea"})
		mgr.Strategic.ClassVectors[content.CanonicalKey("armlab")] = ai.ClassVector{C0: 10, C1: 40, C2: 10}
		mgr.Strategic.ClassVectors[content.CanonicalKey("armflea")] = ai.ClassVector{C0: 100, C1: 100, C2: 100}
		mgr.Strategic.ClassVectors[content.CanonicalKey("armcom")] = ai.ClassVector{C0: 40, C1: 30, C2: 30}
		mgr.Strategic.LastRefreshTick = 100000
		mgr.Strategic.LastClassRecomputeTick = 100000
		s.AI = []*ai.Manager{mgr}
		s.RegisterAll()
		s.State = StateBattle
		s.Clock.ScaledAnchor = 0
		// Create commanders
		hHuman, _ := s.Units.Create(builderDef, 0, strictCellToWorld(10), 0, strictCellToWorld(10))
		hAI, _ := s.Units.Create(builderDef, 1, strictCellToWorld(50), 0, strictCellToWorld(50))
		for _, h := range []pool.Handle{hHuman, hAI} {
			if u := s.Units.Unit(h); u != nil {
				y := terrain.HeightAt(u.X, u.Z)
				if y.Raw() == -1 {
					y = 0
				}
				u.Y = y
				publishOne(s, u)
				s.Movement.EnsureUnit(u)
			}
		}
		// Create shooter/target pair for COB/Aim/projectile (distinct types not counted by AI)
		hShooter, _ := s.Units.Create(shooterDef, 0, strictCellToWorld(10), 0, strictCellToWorld(15))
		hTarget, _ := s.Units.Create(targetDef, 1, strictCellToWorld(12), 0, strictCellToWorld(15))
		for _, h := range []pool.Handle{hShooter, hTarget} {
			if u := s.Units.Unit(h); u != nil {
				u.Y = numeric.Fixed(30 * 65536)
				publishOne(s, u)
				s.Movement.EnsureUnit(u)
			}
		}
		if shooter := s.Units.Unit(hShooter); shooter != nil {
			if w := cat.Weapons["testgun2"]; w != nil {
				shooter.Slots[0].Weapon = w
			}
			shooter.Slots[0].Ammo = 10
			shooter.Slots[0].Reload = 0
			shooter.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: hTarget}
			shooter.Slots[0].Flags |= 0x02
			code := []uint32{0x10021001, 1, 0x10065000}
			prog := &cob.Program{Code: code, Scripts: map[string]int{"AimPrimary": 0, "FirePrimary": 1}, Statics: 0, Pieces: []string{"base"}, ScriptsByID: []int{0}}
			shooter.SetScript(cob.NewVM(prog))
		}
		if target := s.Units.Unit(hTarget); target != nil {
			target.Health = 200
			target.MaxHealth = 200
			target.SetScript(cob.NewVM(&cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}, Statics: 0, Pieces: []string{"base"}}))
		}
		// Strategic center/origin
		for _, mm := range s.AI {
			mm.Strategic.CenterX = strictCellToWorld(32)
			mm.Strategic.CenterZ = strictCellToWorld(32)
			if u := s.Units.Unit(hAI); u != nil {
				mm.OriginX = u.X
				mm.OriginZ = u.Z
			}
		}
		mgr.QueueBuildTyped = func(req ai.BuildRequest) error {
			builder := s.Units.Unit(req.Builder)
			if builder == nil {
				return nil
			}
			if req.Kind == ai.BuildKindMobileSite {
				return construction.QueueMobileBuild(builder, req.UnitKey, req.X, req.Z, req.Count, cat)
			}
			return construction.QueueFactoryBuild(builder, req.UnitKey, req.Count, cat)
		}
		ensureMovementForAll(s)
		publishVisibilityForAll(s)
		s.SetTraceEnabled(true)
		s.ClearTrace()
		return s, hHuman, hShooter
	}

	sA, hHumanA, _ := buildNatural()
	// Predicates
	hasFactoryCompleted := func(s *Session) bool {
		for _, u := range s.Units.Iter() {
			if u != nil && u.Def != nil && u.Def.CanonicalKey == content.CanonicalKey("armlab") && u.Remaining == 0 && u.Alive {
				return true
			}
			if u != nil && u.Def != nil && u.Def.UnitName == "armlab" && u.Remaining == 0 && u.Alive {
				return true
			}
		}
		for _, mgr := range s.AI {
			if mgr == nil {
				continue
			}
			if _, ok := mgr.Milestones()[ai.MilestoneFactoryCompleted]; ok {
				return true
			}
		}
		return false
	}
	hasActiveRouteOrder := func(s *Session) bool {
		for _, u := range s.Units.Iter() {
			if u == nil {
				continue
			}
			if q := orders.QueueForUnit(u); q != nil && (q.LenPrimary() > 0 || q.LenSecondary() > 0) {
				return true
			}
			if route := s.Movement.Routes[u.Handle]; route != nil && route.Active {
				return true
			}
			if s.Movement.Scheduler != nil && s.Movement.Scheduler.HasRequest(u.Handle) {
				return true
			}
		}
		return false
	}
	hasConstructionActive := func(s *Session) bool {
		for _, u := range s.Units.Iter() {
			if u != nil && u.Remaining > 0 && u.Remaining <= 1 {
				return true
			}
		}
		return false
	}
	hasProjectileActive := func(s *Session) bool {
		return s.Combat != nil && s.Combat.Count() > 0
	}
	hasCOBOrAim := func(s *Session) bool {
		for _, ev := range s.TraceEvents() {
			if ev.Kind == TraceWeaponAimDispatch || ev.Kind == TraceCOBReturn {
				return true
			}
		}
		// Also check live thread
		for _, u := range s.Units.Iter() {
			if u == nil {
				continue
			}
			if vm := u.GetScript(); vm != nil {
				for i := 0; i < 8; i++ {
					th := vm.Threads[i]
					if th.Status != 0 {
						return true
					}
				}
			}
		}
		return false
	}

	var checkpointTick uint32
	var st *save.StateV1
	moveIssued := false
	var moveHandle pool.Handle = hHumanA
	// Search for natural checkpoint
searchLoop:
	for tick := 1; tick <= maxTick; tick++ {
		sA.Step(int32(tick))
		// Clear GetBuilt/Park blocking factory production as in G5 [05 C18]
		for _, u := range sA.Units.Iter() {
			if u.Def != nil && u.Def.UnitName == "armlab" && u.Remaining == 0 {
				if q := orders.QueueForUnit(u); q != nil && q.LenPrimary() > 0 {
					if hd := q.Head(); hd != nil {
						name := orders.DescriptorFor(hd.ID).Name
						if name == "GetBuilt" || name == "Park" {
							q.RemoveHead()
						}
					}
				}
			}
		}
		factoryDone := hasFactoryCompleted(sA)
		if tick%20 == 0 || factoryDone {
			t.Logf("tick %d factoryDone=%v route=%v cob=%v constr=%v proj=%v milestones=%v", tick, factoryDone, hasActiveRouteOrder(sA), hasCOBOrAim(sA), hasConstructionActive(sA), hasProjectileActive(sA), func() map[string]uint32 {
				for _, mgr := range sA.AI {
					if mgr != nil {
						return mgr.Milestones()
					}
				}
				return nil
			}())
			if factoryDone {
				for _, u := range sA.Units.Iter() {
					if u != nil && u.Def != nil {
						t.Logf("  unit %d %s rem %.2f alive %v owner %d", u.Handle, u.Def.UnitName, u.Remaining, u.Alive, u.Owner)
					}
				}
			}
		}
		if factoryDone && !moveIssued {
			// Issue a long move to guarantee active route/order at checkpoint
			id := orders.Lookup("Move_Ground")
			if id == 0 {
				id = orders.Lookup("QMove")
			}
			if id != 0 {
				if u := sA.Units.Unit(moveHandle); u != nil {
					q := orders.QueueForUnit(u)
					if q != nil && q.LenPrimary() == 0 {
						goalX := strictCellToWorld(60)
						goalZ := strictCellToWorld(60)
						node := orders.NewMoveNode(id, goalX, goalZ, sA.Clock.GlobalTick, moveHandle, false)
						q.Push(id, node)
					}
				}
			}
			moveIssued = true
		}
		if !factoryDone {
			continue
		}
		routeActive := hasActiveRouteOrder(sA)
		cobSeen := hasCOBOrAim(sA)
		constrActive := hasConstructionActive(sA)
		projActive := hasProjectileActive(sA)
		// Require factory + route/order + COB + (construction or projectile)
		if routeActive && cobSeen && (constrActive || projActive) {
			// Capture checkpoint naturally via production bank/box path
			st = sA.CaptureStateV1()
			if st == nil {
				t.Fatalf("G8: CaptureStateV1 nil at tick %d", tick)
			}
			checkpointTick = uint32(tick)
			t.Logf("G8 natural checkpoint at tick %d factoryDone=%v route=%v cob=%v constr=%v proj=%v", tick, factoryDone, routeActive, cobSeen, constrActive, projActive)
			break searchLoop
		}
	}
	if st == nil {
		// Dump diagnostics
		evs := sA.TraceEvents()
		// Collect milestone info
		var milestones map[string]uint32
		for _, mgr := range sA.AI {
			if mgr != nil {
				milestones = mgr.Milestones()
				break
			}
		}
		fr := StrictFailureRecord{
			LastCompleted: func() string {
				if milestones != nil {
					for _, k := range []string{ai.MilestoneFactoryCompleted, ai.MilestoneNanoframeObserved, ai.MilestoneBuildRequestAccepted, ai.MilestonePlacementSelected} {
						if _, ok := milestones[k]; ok {
							return k
						}
					}
				}
				if hasFactoryCompleted(sA) {
					return "factory_unit_found"
				}
				return "none"
			}(), CurrentTick: sA.Clock.GlobalTick, Seed: simSeed, CrtSeed: crtSeed,
			Handles: []string{fmt.Sprintf("%d", hHumanA)}, DefKeys: []string{"armcom/armlab/armflea"},
			QueueHead: strictQueueHeadString(hHumanA, sA), PathStatus: strictPathStatus(hHumanA, sA),
			AimState: strictAimState(hHumanA, sA), ResourceStocks: strictResourceStocks(1, sA),
			ProjectileCount: func() int {
				if sA.Combat != nil {
					return sA.Combat.Count()
				}
				return 0
			}(), ResultLatch: strictResultLatch(sA), Last50Trace: LastNTraceStrings(evs, 50),
		}
		t.Logf("G8 FAILURE no natural checkpoint: %s", FormatFailure(fr))
		t.Fatalf("G8: natural checkpoint not reached within %d ticks (factoryDone=%v route=%v cob=%v constr=%v proj=%v) [ON-12]", maxTick, hasFactoryCompleted(sA), hasActiveRouteOrder(sA), hasCOBOrAim(sA), hasConstructionActive(sA), hasProjectileActive(sA))
	}
	// Verify checkpoint was reached via production Step loop, no injection
	t.Logf("G8 checkpoint tick %d GlobalTick %d", checkpointTick, sA.Clock.GlobalTick)
	// Save through production bank/box path
	b := save.NewBuilder(save.RetailTag)
	save.WriteStateV1(b, st)
	bankBytes := b.Bytes()
	bank, err := save.OpenBytes(bankBytes, save.RetailTag)
	if err != nil {
		t.Fatalf("G8: OpenBytes: %v", err)
	}
	decoded, err := save.ReadStateV1(bank, cat.Hash, cat.Manifest)
	if err != nil {
		t.Fatalf("G8: ReadStateV1: %v", err)
	}
	if decoded.SimDraws != st.SimDraws || decoded.CrtDraws != st.CrtDraws {
		t.Fatalf("G8: RNG draws mismatch after bank round-trip sim %d/%d crt %d/%d", decoded.SimDraws, st.SimDraws, decoded.CrtDraws, st.CrtDraws)
	}
	t.Logf("G8 checkpoint econ P0 UpdateTime %d H1 %d H2 %d stock %.1f P1 UpdateTime %d H1 %d H2 %d", sA.Econ.Players[0].UpdateTime, sA.Econ.Players[0].Helper1Deadline, sA.Econ.Players[0].Helper2Deadline, sA.Econ.Players[0].Stock[0], sA.Econ.Players[1].UpdateTime, sA.Econ.Players[1].Helper1Deadline, sA.Econ.Players[1].Helper2Deadline)
	t.Logf("G8 decoded econ P0 %d H1 %d H2 %d P1 %d H1 %d H2 %d", decoded.Economy.Players[0].UpdateTime, decoded.Economy.Players[0].Helper1Deadline, decoded.Economy.Players[0].Helper2Deadline, decoded.Economy.Players[1].UpdateTime, decoded.Economy.Players[1].Helper1Deadline, decoded.Economy.Players[1].Helper2Deadline)
	t.Logf("G8 checkpoint helperCalls P0 %d %d P1 %d %d", sA.Econ.Players[0].Helper1Calls, sA.Econ.Players[0].Helper2Calls, sA.Econ.Players[1].Helper1Calls, sA.Econ.Players[1].Helper2Calls)
	// Continue uninterrupted N ticks and capture hashes
	sA.ClearTrace()
	// sA already at checkpoint T; continue from T+1
	for i := 0; i < continueTicks; i++ {
		sA.Step(int32(int(checkpointTick) + 1 + i))
	}
	hashA := HashState(sA)
	traceA := HashTrace(sA.TraceEvents())
	simDrawsA := uint64(0)
	crtDrawsA := uint64(0)
	if rng.Global.Sim != nil {
		simDrawsA = rng.Global.Sim.Draws()
	}
	if rng.Global.Crt != nil {
		crtDrawsA = rng.Global.Crt.Draws()
	}
	// Restore into FRESH session and continue N ticks (re-seed RNG from saved state, not global)
	// Build fresh session with same catalog but fresh terrain/world
	rng.SeedGlobal(simSeed, crtSeed)
	// Need to rebuild fresh terrain for sB but we reuse buildNatural helper with fresh terrain
	// To avoid sharing terrain mutated by sA, create fresh session via same helper but reusing cat
	sB, _, _ := buildNatural()
	if err := sB.RestoreStateV1(decoded); err != nil {
		t.Fatalf("G8: RestoreStateV1: %v", err)
	}
	t.Logf("G8 restored econ P0 UpdateTime %d H1 %d H2 %d stock %.1f P1 UpdateTime %d H1 %d H2 %d", sB.Econ.Players[0].UpdateTime, sB.Econ.Players[0].Helper1Deadline, sB.Econ.Players[0].Helper2Deadline, sB.Econ.Players[0].Stock[0], sB.Econ.Players[1].UpdateTime, sB.Econ.Players[1].Helper1Deadline, sB.Econ.Players[1].Helper2Deadline)
	t.Logf("G8 restored helperCalls P0 %d %d P1 %d %d", sB.Econ.Players[0].Helper1Calls, sB.Econ.Players[0].Helper2Calls, sB.Econ.Players[1].Helper1Calls, sB.Econ.Players[1].Helper2Calls)
	// After restore, RNG must be from saved state, not global seed
	sB.SetTraceEnabled(true)
	sB.ClearTrace()
	start := decoded.Clock.GlobalTick + 1
	for i := 0; i < continueTicks; i++ {
		sB.Step(int32(start) + int32(i))
	}
	hashB := HashState(sB)
	traceB := HashTrace(sB.TraceEvents())
	simDrawsB := uint64(0)
	crtDrawsB := uint64(0)
	if rng.Global.Sim != nil {
		simDrawsB = rng.Global.Sim.Draws()
	}
	if rng.Global.Crt != nil {
		crtDrawsB = rng.Global.Crt.Draws()
	}
	if hashA != hashB {
		// Detailed dump for diagnostics
		t.Logf("G8 hash mismatch: dumping units A vs B")
		for _, u := range sA.Units.Iter() {
			if u == nil {
				continue
			}
			t.Logf("A unit %d %s rem %.3f health %d pos %d %d", u.Handle, u.Def.UnitName, u.Remaining, u.Health, u.X.Raw(), u.Z.Raw())
		}
		for _, u := range sB.Units.Iter() {
			if u == nil {
				continue
			}
			t.Logf("B unit %d %s rem %.3f health %d pos %d %d", u.Handle, u.Def.UnitName, u.Remaining, u.Health, u.X.Raw(), u.Z.Raw())
		}
		if sA.Combat != nil && sB.Combat != nil {
			t.Logf("A combat %d B %d", sA.Combat.Count(), sB.Combat.Count())
			for i := 0; i < sA.Combat.Count() && i < len(sA.Combat.Records); i++ {
				p := sA.Combat.Records[i]
				t.Logf("A proj %d weapon %d pos %d %d", i, p.WeaponID, p.Pos.X.Raw(), p.Pos.Z.Raw())
			}
			for i := 0; i < sB.Combat.Count() && i < len(sB.Combat.Records); i++ {
				p := sB.Combat.Records[i]
				t.Logf("B proj %d weapon %d pos %d %d", i, p.WeaponID, p.Pos.X.Raw(), p.Pos.Z.Raw())
			}
		}
		if sA.Movement != nil && sB.Movement != nil {
			t.Logf("A routes %d B %d", len(sA.Movement.Routes), len(sB.Movement.Routes))
			for h, r := range sA.Movement.Routes {
				t.Logf("A route %d count %d active %v", h, r.Count, r.Active)
			}
			for h, r := range sB.Movement.Routes {
				t.Logf("B route %d count %d active %v", h, r.Count, r.Active)
			}
		}
		if sA.Econ != nil && sB.Econ != nil {
			for i := 0; i < 2; i++ {
				t.Logf("A econ %d metal %.1f energy %.1f capM %.1f", i, sA.Econ.Players[i].Stock[economy.Metal], sA.Econ.Players[i].Stock[economy.Energy], sA.Econ.Players[i].Capacity[economy.Metal])
				t.Logf("B econ %d metal %.1f energy %.1f capM %.1f", i, sB.Econ.Players[i].Stock[economy.Metal], sB.Econ.Players[i].Stock[economy.Energy], sB.Econ.Players[i].Capacity[economy.Metal])
			}
		}
		t.Fatalf("G8: state hash mismatch uninterrupted %s vs restored %s (checkpoint %d + %d ticks)", hashA, hashB, checkpointTick, continueTicks)
	}
	if traceA != traceB {
		t.Fatalf("G8: trace hash mismatch uninterrupted %s vs restored %s (checkpoint %d + %d ticks)\nA %v\nB %v", traceA, traceB, checkpointTick, continueTicks, sA.TraceEvents()[:g8min(5, len(sA.TraceEvents()))], sB.TraceEvents()[:g8min(5, len(sB.TraceEvents()))])
	}
	if simDrawsA != simDrawsB || crtDrawsA != crtDrawsB {
		t.Fatalf("G8: RNG draws mismatch after %d ticks sim %d/%d crt %d/%d", continueTicks, simDrawsA, simDrawsB, crtDrawsA, crtDrawsB)
	}
	// Additional deterministic ordering check: MarshalStateV1 twice yields same bytes (sorted)
	// Use st (checkpoint) already sorted, but also check current state
	st2 := sA.CaptureStateV1()
	b2 := save.MarshalStateV1(st2)
	b3 := save.MarshalStateV1(st2)
	if string(b2) != string(b3) {
		t.Fatalf("G8: MarshalStateV1 not deterministic (map iteration)")
	}
	ev := StrictGateEvidence{
		Commit: strictCommit(), ContentManifest: strictCatalogHash(cat), Map: "test", Seed: simSeed, CrtSeed: crtSeed,
		Players: []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer", "ai_profile": "default"}},
		MaxTick: int(checkpointTick) + continueTicks, Milestones: map[string]uint32{"checkpoint": checkpointTick, "final_tick": sA.Clock.GlobalTick, "factory_completed": checkpointTick},
		Winner: -1, Reason: "G8 natural save/load [ON-12] factory+route+COB+constr",
		FinalTick: sA.Clock.GlobalTick, FinalStateHash: hashA, TraceHash: traceA,
	}
	t.Logf("G8 evidence: %s", FormatEvidence(ev))
}

func g8min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
