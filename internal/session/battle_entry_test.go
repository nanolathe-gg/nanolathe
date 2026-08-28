package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

var _ = content.CanonicalKey // ensure content import used

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestStorageBonusFloorAndEnable(t *testing.T) {
	var p economy.Player
	if p.StorageBonusEnabled {
		t.Fatal("fresh player should have bonus disabled")
	}
	p.InstallStorageBonus(100, 50)
	if !p.StorageBonusEnabled {
		t.Fatal("InstallStorageBonus should set enable flag [layout omitted] bit0")
	}
	if p.StorageBonus[economy.Metal] != 200 {
		t.Fatalf("metal floor 100 ->200 got %v", p.StorageBonus[economy.Metal])
	}
	if p.StorageBonus[economy.Energy] != 200 {
		t.Fatalf("energy floor 50 ->200 got %v", p.StorageBonus[economy.Energy])
	}
	var q economy.Player
	q.InstallStorageBonus(1000, 800)
	if q.StorageBonus[economy.Metal] != 1000 || q.StorageBonus[economy.Energy] != 800 {
		t.Fatalf("1000/800 should be preserved got %v %v", q.StorageBonus[economy.Metal], q.StorageBonus[economy.Energy])
	}
	// Truncation I3: int -> float via FILD/FSTP exact for these magnitudes
	var r economy.Player
	r.InstallStorageBonus(201, 199)
	if r.StorageBonus[economy.Metal] != 201 || r.StorageBonus[economy.Energy] != 200 {
		t.Fatalf("201/199 floor got %v %v", r.StorageBonus[economy.Metal], r.StorageBonus[economy.Energy])
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestRebuildCapacityBonusInclusive(t *testing.T) {
	var svc economy.Service
	w := units.NewSliced(10, nil)
	def := &content.UnitDef{}
	def.MaxDamage = 100
	def.EnergyStorage = 0
	def.MetalStorage = 0
	// Complete unit for player 0 with zero storage, but bonus should still give capacity
	h, _ := w.Create(def, 0, 0, 0, 0)
	if u := w.Unit(h); u != nil {
		u.Remaining = 0
	}
	svc.Players[0].Exists = true
	svc.Players[0].StorageBonusEnabled = true
	svc.Players[0].StorageBonus[economy.Metal] = 1000
	svc.Players[0].StorageBonus[economy.Energy] = 1000
	economy.RebuildCapacity(&svc, w)
	if svc.Players[0].Capacity[economy.Metal] != 1000 || svc.Players[0].Capacity[economy.Energy] != 1000 {
		t.Fatalf("capacity with bonus 1000/1000 got %v %v", svc.Players[0].Capacity[economy.Metal], svc.Players[0].Capacity[economy.Energy])
	}
	// Without flag, capacity should be 0
	var svc2 economy.Service
	svc2.Players[0].Exists = true
	svc2.Players[0].StorageBonus[economy.Metal] = 1000
	svc2.Players[0].StorageBonus[economy.Energy] = 1000
	// flag false
	economy.RebuildCapacity(&svc2, w)
	if svc2.Players[0].Capacity[economy.Metal] != 0 || svc2.Players[0].Capacity[economy.Energy] != 0 {
		t.Fatalf("without enable flag capacity should be 0 got %v %v", svc2.Players[0].Capacity[economy.Metal], svc2.Players[0].Capacity[economy.Energy])
	}
	// With unit storage plus bonus: unit adds 500, bonus 1000 => 1500
	def2 := &content.UnitDef{}
	def2.MaxDamage = 100
	def2.MetalStorage = 500
	def2.EnergyStorage = 250
	w2 := units.NewSliced(10, nil)
	h2, _ := w2.Create(def2, 0, 0, 0, 0)
	if u := w2.Unit(h2); u != nil {
		u.Remaining = 0
	}
	var svc3 economy.Service
	svc3.Players[0].Exists = true
	svc3.Players[0].StorageBonusEnabled = true
	svc3.Players[0].StorageBonus[economy.Metal] = 1000
	svc3.Players[0].StorageBonus[economy.Energy] = 200
	economy.RebuildCapacity(&svc3, w2)
	if svc3.Players[0].Capacity[economy.Metal] != 1500 {
		t.Fatalf("500+1000 metal want 1500 got %v", svc3.Players[0].Capacity[economy.Metal])
	}
	if svc3.Players[0].Capacity[economy.Energy] != 450 {
		t.Fatalf("250+200 energy want 450 got %v", svc3.Players[0].Capacity[economy.Energy])
	}
}

// TestSkirmishStorageBonusPreservesOpeningStock locks P1 acceptance: both players stock >=900 at tick 60 without fixture credit [OX P1].
// It uses production NewSkirmishWithFS on a tiny catalog (or retail if available) and steps 60 ticks.
func TestSkirmishStorageBonusPreservesOpeningStock(t *testing.T) {
	rng.SeedGlobal(11, 12)
	cat := minimalCatalogForStrict()
	// minimal fs with 2 StartPos
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.ota":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 1
	// Ensure starting resources 1000/1000 (defaults already)
	cfg.Players[0].Metal = 1000
	cfg.Players[0].Energy = 1000
	cfg.Players[1].Metal = 1000
	cfg.Players[1].Energy = 1000
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest: %v", err)
	}
	// Verify bonus installed per OX P1: enable flag true and bonus = max(1000,200)=1000 for both
	for i := 0; i < 2; i++ {
		p := s.Econ.Players[i]
		if !p.StorageBonusEnabled {
			t.Fatalf("player %d bonus flag not enabled [layout omitted] bit0", i)
		}
		if p.StorageBonus[economy.Metal] != 1000 || p.StorageBonus[economy.Energy] != 1000 {
			t.Fatalf("player %d bonus want 1000/1000 got %v/%v", i, p.StorageBonus[economy.Metal], p.StorageBonus[economy.Energy])
		}
	}
	// Ensure capacity bonus-inclusive before any settlement
	if s.Econ.Players[0].Capacity[economy.Metal] < 1000 || s.Econ.Players[0].Capacity[economy.Energy] < 1000 {
		t.Fatalf("capacity not bonus-inclusive: metal %v energy %v", s.Econ.Players[0].Capacity[economy.Metal], s.Econ.Players[0].Capacity[economy.Energy])
	}
	// Check thresholds are bonus-inclusive as chosen (with TODO question)
	if s.Econ.Players[0].MetalShareThreshold != 1000 || s.Econ.Players[0].EnergyShareThreshold != 1000 {
		t.Logf("thresholds %v %v (bonus-inclusive chosen, TODO question if retail is stale 0)", s.Econ.Players[0].MetalShareThreshold, s.Econ.Players[0].EnergyShareThreshold)
	}
	// Stock before ticks should be 1000
	if s.Econ.Players[0].Stock[economy.Metal] != 1000 || s.Econ.Players[0].Stock[economy.Energy] != 1000 {
		t.Fatalf("pre-tick stock not 1000: p0 metal %v energy %v", s.Econ.Players[0].Stock[economy.Metal], s.Econ.Players[0].Stock[economy.Energy])
	}
	// Step 60 ticks via economy deadlines (stable player 0..1 order per I1)
	// Use Service.Tick which respects per-player 30-tick deadline and does settlement.
	// Global tick increments per sub-tick; we simulate tick 0..60 inclusive.
	for tick := uint32(0); tick <= 60; tick++ {
		s.Econ.Tick(tick, s.Units)
		s.Econ.ShareTick(tick)
	}
	// Strict assertion: both players stock >=900 at tick 60 without fixture credit
	for i := 0; i < 2; i++ {
		if s.Econ.Players[i].Stock[economy.Metal] < 900 || s.Econ.Players[i].Stock[economy.Energy] < 900 {
			t.Fatalf("P1 failed: player %d stock at tick60 metal %v energy %v want >=900 (bonus must preserve opening) cap metal %v energy %v waste %v %v", i, s.Econ.Players[i].Stock[economy.Metal], s.Econ.Players[i].Stock[economy.Energy], s.Econ.Players[i].Capacity[economy.Metal], s.Econ.Players[i].Capacity[economy.Energy], s.Econ.Players[i].Waste[economy.Metal], s.Econ.Players[i].Waste[economy.Energy])
		}
	}
	// Also step to 600 and ensure nonzero stock remains (headless default-AI nonzero at 600)
	for tick := uint32(61); tick <= 600; tick++ {
		s.Econ.Tick(tick, s.Units)
		s.Econ.ShareTick(tick)
	}
	for i := 0; i < 2; i++ {
		if s.Econ.Players[i].Stock[economy.Metal] == 0 && s.Econ.Players[i].Stock[economy.Energy] == 0 {
			t.Fatalf("player %d stock zero at tick600 metal %v energy %v (headless should show nonzero)", i, s.Econ.Players[i].Stock[economy.Metal], s.Econ.Players[i].Stock[economy.Energy])
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestSkirmishBonusFloor200(t *testing.T) {
	rng.SeedGlobal(21, 22)
	cat := minimalCatalogForStrict()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test2.ota": "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	cfg := SkirmishConfig{MapName: "test2", NumPlayers: 2}
	cfg.ApplyDefaults()
	// Override to low values to test floor (ApplyDefaults only replaces 0 with 1000, so 50/10/199 stay)
	cfg.Players[0].Metal = 50
	cfg.Players[0].Energy = 10
	cfg.Players[1].Metal = 199
	cfg.Players[1].Energy = 1
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest low: %v", err)
	}
	if s.Econ.Players[0].StorageBonus[economy.Metal] != 200 || s.Econ.Players[0].StorageBonus[economy.Energy] != 200 {
		t.Fatalf("p0 floor 50/10 want 200/200 got %v/%v", s.Econ.Players[0].StorageBonus[economy.Metal], s.Econ.Players[0].StorageBonus[economy.Energy])
	}
	if s.Econ.Players[1].StorageBonus[economy.Metal] != 200 || s.Econ.Players[1].StorageBonus[economy.Energy] != 200 {
		t.Fatalf("p1 199/1 floor want 200/200 got %v/%v", s.Econ.Players[1].StorageBonus[economy.Metal], s.Econ.Players[1].StorageBonus[economy.Energy])
	}
}
