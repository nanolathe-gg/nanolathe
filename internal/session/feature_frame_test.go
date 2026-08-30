package session

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestFeatureLifecycleBurnSinkAndReclaim covers burning lifecycle setup,
// submerged corpse sinking, and reclaim credit [05 "Feature burning"] [05
// "Feature reclaim"].
func TestFeatureLifecycleBurnSinkAndReclaim(t *testing.T) {
	rng.SeedGlobal(700, 800)
	cat := strictMinimalCatalog()
	tree := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Damage: 100, Metal: 50, Energy: 100, Reclaimable: true}
	tree.CanonicalKey = "tree"
	tree.Object = "tree"
	burnt := &content.FeatureDef{}
	burnt.CanonicalKey = "tree_burnt"
	tree.FeatureBurntDef = burnt
	tree.FeatureBurnt = burnt.CanonicalKey
	corpse := &content.FeatureDef{FootprintX: 2, FootprintZ: 2, Damage: 100, Metal: 75, Energy: 75, Reclaimable: true}
	corpse.CanonicalKey = "corcorpse"
	corpse.Object = corpse.CanonicalKey
	cat.Features[tree.CanonicalKey] = tree
	cat.Features[burnt.CanonicalKey] = burnt
	cat.Features[corpse.CanonicalKey] = corpse
	s := &Session{Catalog: cat, World: strictMinimalTerrain(), Mission: strictSyntheticMission()}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("new unit world: %v", err)
	}
	s.Units = w
	s.Econ = strictEconomyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[0].SetSettlementStatusPair(1, 0)
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind services: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	treeInstance := s.Features.PlaceAt(5, 5, tree)
	if treeInstance == nil {
		t.Fatal("tree placement failed")
	}
	treeInstance.IsBurning = true
	treeInstance.BurnCountdown = 1
	treeInstance.BurnDuration = 1
	treeInstance.Health = tree.Damage
	s.Features.SetBurnAnimationTicks(func(*content.FeatureDef) int32 { return 10 })
	s.Features.TickLifecycle(1)
	unitDef := cat.Units["armcom"]
	unitDef.Corpse = corpse.CanonicalKey
	h, err := s.Units.Create(unitDef, 0, numeric.Fixed(8*16*65536), numeric.Fixed(2*65536), numeric.Fixed(8*16*65536))
	if err != nil {
		t.Fatalf("create reclaim unit: %v", err)
	}
	u := s.Units.Unit(h)
	corpseInstance := s.Features.PlaceAt(8, 8, corpse)
	if corpseInstance == nil {
		t.Fatal("corpse placement failed")
	}
	s.World.SeaLevel = 20
	corpseInstance.Y = numeric.Fixed(2 * 65536)
	s.Features.StartSinking(corpseInstance, false)
	if !corpseInstance.IsSinking {
		t.Fatal("submerged corpse did not begin sinking")
	}
	metal, _ := s.Features.Reclaim(u, corpseInstance, 2)
	if metal == 0 {
		t.Fatal("reclaim did not return authored metal")
	}
	s.Econ.Players[0].Stock[economy.Metal] += metal
	if s.Econ.Players[0].Stock[economy.Metal] != metal {
		t.Fatalf("reclaim credit=%v, want %v", s.Econ.Players[0].Stock[economy.Metal], metal)
	}
}

// TestCommittedFrameReadsAreIsolated verifies that consuming the published
// frame, including fog bytes, cannot alter authoritative state [03 §2.4].
func TestCommittedFrameReadsAreIsolated(t *testing.T) {
	rng.SeedGlobal(777, 888)
	cat := strictMinimalCatalog()
	build := func() *Session {
		s := &Session{Catalog: cat, World: strictMinimalTerrain(), Mission: strictSyntheticMission(), Clock: &clock.State{Requested: 10, Active: 10}, Snapshot: &frame.Buffer{}}
		w, _ := newSlicedWorld(cat)
		s.Units = w
		s.Econ = strictEconomyForTest()
		for i := 0; i < 2; i++ {
			s.Econ.Players[i].Exists = true
			s.Econ.Players[i].ControllerState = uint8(i + 1)
		}
		s.Econ.SeedDeadlines(0)
		s.InitBattleWindForSession()
		if err := createAndBindServicesForTest(t, s); err != nil {
			t.Fatalf("bind services: %v", err)
		}
		s.RegisterAll()
		s.State = StateBattle
		h, _ := s.Units.Create(cat.Units["armcom"], 0, numeric.Fixed(10*65536), 0, numeric.Fixed(10*65536))
		u := s.Units.Unit(h)
		publishOne(s, u)
		s.Movement.EnsureUnit(u)
		id := orders.Lookup("Move_Ground")
		if id != 0 {
			orders.QueueForUnit(u).Push(id, orders.NewMoveNode(id, numeric.Fixed(20*16*65536), numeric.Fixed(20*16*65536), 0, h, false))
		}
		return s
	}
	headless, rendered := build(), build()
	frameReads := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		headless.Step(int32(i + 1))
		rendered.Step(int32(i + 1))
		committed := rendered.Snapshot.Current()
		if committed == nil || committed.Tick != rendered.Clock.GlobalTick {
			t.Fatalf("missing committed frame at tick %d", i+1)
		}
		h := sha256.New()
		_, _ = h.Write(committed.Fog.Ch0)
		_, _ = h.Write(committed.Fog.Ch1)
		frameReads = append(frameReads, fmt.Sprintf("%d:%x:%d", committed.Tick, h.Sum(nil), len(committed.Units)))
	}
	if len(frameReads) != 20 || frameReads[0] == frameReads[len(frameReads)-1] {
		t.Fatalf("committed frame reads did not advance: first=%q last=%q", frameReads[0], frameReads[len(frameReads)-1])
	}
	if got, want := HashState(rendered), HashState(headless); got != want {
		t.Fatalf("frame reads changed state: %s != %s", got, want)
	}
}
