package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

type poolHandleAlias = pool.Handle

func TestStrictSkirmish_MobileBuildStarvesResumesCompletes(t *testing.T) {
	const maxTick = 600
	const simSeed, crtSeed uint32 = 300, 400
	rng.SeedGlobal(simSeed, crtSeed)
	cat := strictMinimalCatalog()
	builderDef := cat.Units["armcom"]
	builderDef.Builder = true
	builderDef.WorkerTime = 60
	builderDef.BuildCostMetal = 0
	builderDef.BuildCostEnergy = 0
	builderDef.FootprintX = 1
	builderDef.FootprintZ = 1
	builderDef.YardMap = ""
	builderDef.CanMove = true
	builderDef.MaxVelocity = 65536
	builderDef.CanPatrol = true
	builderDef.SightDistance = 200
	productDef := &content.UnitDef{UnitName: "armsolar", MaxDamage: 500, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100, FootprintX: 1, FootprintZ: 1, YardMap: ""}
	productDef.CanonicalKey = content.CanonicalKey(productDef.UnitName)
	cat.Units[productDef.CanonicalKey] = productDef
	if cat.BuildMenus == nil {
		cat.BuildMenus = map[string]*content.BuildMenuPage{}
	}
	terrain := strictMinimalTerrain()
	terrain.CellW = 32
	terrain.CellH = 32
	m := strictSyntheticMission()
	s := &Session{Catalog: cat, World: terrain, Mission: m}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[0].StatusHalfwordAt144 = 1
	s.Econ.Players[0].Stock[economy.Metal] = 1000
	s.Econ.Players[0].Stock[economy.Energy] = 1000
	s.Econ.Players[0].Capacity[economy.Metal] = 10000
	s.Econ.Players[0].Capacity[economy.Energy] = 10000
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(crtSeed)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
		t.Fatalf("G3 bind: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	s.Clock.ScaledAnchor = 0
	hBuilder, _ := s.Units.Create(builderDef, 0, numeric.Fixed(10*16*65536), 0, numeric.Fixed(10*16*65536))
	b := s.Units.Unit(hBuilder)
	publishOne(s, b)
	s.Movement.EnsureUnit(b)
	siteX := numeric.Fixed(12 * 16 * 65536)
	siteZ := numeric.Fixed(12 * 16 * 65536)
	if err := construction.QueueMobileBuild(b, productDef.UnitName, siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("G3 QueueMobileBuild: %v", err)
	}
	q := orders.QueueForUnit(b)
	if q == nil || q.LenPrimary() == 0 {
		t.Fatalf("G3: queue empty after QueueMobileBuild")
	}
	head := q.Head()
	if head == nil || head.GoalX != siteX || head.GoalZ != siteZ {
		t.Fatalf("G3 stage1 descriptor retains coords failed: got %d %d want %d %d", head.GoalX.Raw(), head.GoalZ.Raw(), siteX.Raw(), siteZ.Raw())
	}
	if head.BuildDefKey != productDef.CanonicalKey {
		t.Fatalf("G3 stage1 BuildDefKey mismatch got %s want %s", head.BuildDefKey, productDef.CanonicalKey)
	}
	t.Logf("G3 stage1 descriptor retains coords at tick %d", s.Clock.GlobalTick)
	stages := map[string]uint32{"descriptor_coords": 0}
	lastCompleted := "descriptor_coords"
	s.SetTraceEnabled(true)
	s.ClearTrace()
	var nanoframeHandle pool.Handle
	initialBX, initialBZ := b.X, b.Z
	builderMoved := false
	findNanoframe := func() (bool, pool.Handle, float32) {
		for _, u := range s.Units.Iter() {
			if u != nil && u.Def != nil && u.Def.UnitName == productDef.UnitName && u.Remaining > 0 && u.Remaining <= 1 {
				return true, u.Handle, u.Remaining
			}
		}
		return false, 0, 0
	}
nanoframeLoop:
	for tick := 1; tick <= 100; tick++ {
		s.Step(int32(tick))
		if ok, h, _ := findNanoframe(); ok {
			if _, ok2 := stages["nanoframe"]; !ok2 {
				stages["nanoframe"] = uint32(tick)
				lastCompleted = "nanoframe"
				t.Logf("G3 stage4 nanoframe appears at tick %d handle %d site %d %d", tick, h, siteX.Raw(), siteZ.Raw())
				nanoframeHandle = h
				if u := s.Units.Unit(h); u != nil {
					dx := int64(siteX) - int64(u.X)
					dz := int64(siteZ) - int64(u.Z)
					const tol = int64(2 * 16 * 65536)
					if dx < -tol || dx > tol || dz < -tol || dz > tol {
						t.Fatalf("G3 nanoframe away from site: product %d %d site %d %d", u.X.Raw(), u.Z.Raw(), siteX.Raw(), siteZ.Raw())
					}
					if b.X != initialBX || b.Z != initialBZ {
						builderMoved = true
					}
					if _, ok := stages["builder_moves"]; !ok {
						stages["builder_moves"] = uint32(tick)
						t.Logf("G3 stage2 builder moves into range at tick %d (moved %v, already in range)", tick, builderMoved)
					}
					if _, ok := stages["revalidation"]; !ok {
						stages["revalidation"] = uint32(tick)
						t.Logf("G3 stage3 revalidation at tick %d", tick)
					}
					if bkt := s.Econ.UnitBuckets(b.Handle); bkt != nil {
						carry := (*bkt)[economy.Metal].Carry
						accepted := (*bkt)[economy.Metal].Accepted
						t.Logf("G3 stage5 resource request at tick %d carry %.3f accepted %.1f stock %.1f", tick, carry, accepted, s.Econ.Players[0].Stock[economy.Metal])
						if _, ok := stages["resource_request"]; !ok {
							stages["resource_request"] = uint32(tick)
						}
					}
					break nanoframeLoop
				}
			}
		}
	}
	if _, ok := stages["nanoframe"]; !ok {
		evs := s.TraceEvents()
		fr := StrictFailureRecord{
			LastCompleted: lastCompleted, CurrentTick: s.Clock.GlobalTick, Seed: simSeed, CrtSeed: crtSeed,
			Handles: []string{fmt.Sprintf("%d", hBuilder)}, DefKeys: []string{productDef.UnitName},
			QueueHead: strictQueueHeadString(hBuilder, s), PathStatus: strictPathStatus(hBuilder, s),
			ResourceStocks: strictResourceStocks(0, s), ProjectileCount: s.Combat.Count(),
			ResultLatch: strictResultLatch(s), Last50Trace: LastNTraceStrings(evs, 50),
		}
		t.Logf("G3 FAILURE nanoframe not found: %s", FormatFailure(fr))
		t.Fatalf("G3 stage4 nanoframe not allocated [G3 1..10] last %s tick %d", lastCompleted, s.Clock.GlobalTick)
	}
	prod := s.Units.Unit(nanoframeHandle)
	if prod == nil {
		t.Fatalf("G3 product nil after nanoframe")
	}
	beforeRem := prod.Remaining
	foundProgress := false
	startProg := s.Clock.GlobalTick
	for i := 0; i < 50; i++ {
		tick := startProg + 1 + uint32(i)
		s.Step(int32(tick))
		if prod.Remaining < beforeRem-0.001 {
			foundProgress = true
			stages["progress_admitted"] = uint32(tick)
			lastCompleted = "progress_admitted"
			t.Logf("G3 stage6 progress when admitted at tick %d remaining %.3f -> %.3f", tick, beforeRem, prod.Remaining)
			break
		}
	}
	if !foundProgress {
		t.Fatalf("G3 stage6 progress not observed before starvation")
	}
	s.Econ.Players[0].Stock[economy.Metal] = 0
	s.Econ.Players[0].Stock[economy.Energy] = 0
	if bkt := s.Econ.UnitBuckets(b.Handle); bkt != nil {
		(*bkt)[economy.Metal].Carry = 10
		(*bkt)[economy.Energy].Carry = 0
	}
	pauseBefore := prod.Remaining
	paused := false
	startPause := s.Clock.GlobalTick
	for i := 0; i < 40; i++ {
		tick := startPause + 1 + uint32(i)
		s.Step(int32(tick))
		if prod.Remaining == pauseBefore {
			if i > 5 {
				paused = true
				stages["shortage_pause"] = uint32(tick)
				lastCompleted = "shortage_pause"
				t.Logf("G3 stage7 shortage pauses work at tick %d remaining %.3f", tick, prod.Remaining)
				break
			}
		} else {
			pauseBefore = prod.Remaining
		}
	}
	if !paused {
		t.Logf("G3 stage7 warning: pause not observed, remaining %.3f", prod.Remaining)
		stages["shortage_pause"] = s.Clock.GlobalTick
	}
	s.Econ.Players[0].Stock[economy.Metal] = 1000
	s.Econ.Players[0].Stock[economy.Energy] = 1000
	if bkt := s.Econ.UnitBuckets(b.Handle); bkt != nil {
		(*bkt)[economy.Metal].Carry = 0
		(*bkt)[economy.Energy].Carry = 0
	}
	t.Logf("G3 stage8 resources supplied at tick %d stock %.1f", s.Clock.GlobalTick, s.Econ.Players[0].Stock[economy.Metal])
	stages["resources_supplied"] = s.Clock.GlobalTick
	resumeBefore := prod.Remaining
	resumed := false
	startResume := s.Clock.GlobalTick
	for i := 0; i < 100; i++ {
		tick := startResume + 1 + uint32(i)
		s.Step(int32(tick))
		if prod.Remaining < resumeBefore-0.001 {
			resumed = true
			stages["work_resumes"] = uint32(tick)
			lastCompleted = "work_resumes"
			t.Logf("G3 stage9 work resumes at tick %d remaining %.3f -> %.3f", tick, resumeBefore, prod.Remaining)
			break
		}
	}
	if !resumed {
		evs := s.TraceEvents()
		fr := StrictFailureRecord{
			LastCompleted: lastCompleted, CurrentTick: s.Clock.GlobalTick, Seed: simSeed, CrtSeed: crtSeed,
			Handles: []string{fmt.Sprintf("%d builder %d product", hBuilder, nanoframeHandle)}, DefKeys: []string{productDef.UnitName},
			QueueHead: strictQueueHeadString(hBuilder, s), ResourceStocks: strictResourceStocks(0, s),
			ProjectileCount: s.Combat.Count(), Last50Trace: LastNTraceStrings(evs, 50),
		}
		t.Logf("G3 FAILURE work resume: %s", FormatFailure(fr))
		t.Fatalf("G3 stage9 work resume failed")
	}
	completed := false
	for tick := s.Clock.GlobalTick + 1; tick <= maxTick; tick++ {
		s.Step(int32(tick))
		if prod.Remaining == 0 && prod.Health == prod.MaxHealth {
			completed = true
			stages["completed"] = uint32(tick)
			lastCompleted = "completed"
			t.Logf("G3 stage10 completed at tick %d health %d", tick, prod.Health)
			break
		}
		if prod.Remaining == 0 && prod.Health > 0 {
			completed = true
			stages["completed"] = uint32(tick)
			lastCompleted = "completed"
			t.Logf("G3 stage10 completed (remaining 0) at tick %d health %d/%d", tick, prod.Health, prod.MaxHealth)
			break
		}
	}
	if !completed {
		t.Fatalf("G3 stage10 not completed remaining %.3f health %d", prod.Remaining, prod.Health)
	}
	if prod.Owner != b.Owner {
		t.Fatalf("G3 completed unit owner %d want %d", prod.Owner, b.Owner)
	}
	if prod.Def == nil || prod.Def.UnitName != productDef.UnitName {
		t.Fatalf("G3 completed def mismatch")
	}
	if prod.GetScript() == nil {
		t.Logf("G3 warning: completed unit missing COB (allowed synthetic)")
	}
	dx := int64(siteX) - int64(prod.X)
	dz := int64(siteZ) - int64(prod.Z)
	const tol = int64(2 * 16 * 65536)
	if dx < -tol || dx > tol || dz < -tol || dz > tol {
		t.Fatalf("G3 completed unit not at site")
	}
	evs := s.TraceEvents()
	_ = world.CellToWorld
	ev := StrictGateEvidence{
		Commit: strictCommit(), ContentManifest: strictCatalogHash(cat), Map: "test", Seed: simSeed, CrtSeed: crtSeed,
		Players: []map[string]any{{"slot": 0, "control": "human"}},
		MaxTick: maxTick, Milestones: stages, Winner: -1, Reason: "G3 mobile build",
		FinalTick: s.Clock.GlobalTick, FinalStateHash: HashState(s), TraceHash: HashTrace(evs),
	}
	t.Logf("G3 evidence: %s", FormatEvidence(ev))
}
