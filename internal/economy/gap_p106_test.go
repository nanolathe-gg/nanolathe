package economy

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestMakerStall(t *testing.T) {
	if !MakerStall(0.1) {
		t.Fatalf("energyCarry 0.1 should stall")
	}
	if MakerStall(0) {
		t.Fatalf("energyCarry 0 should not stall (inclusive)")
	}
	if MakerStall(-1) {
		t.Fatalf("negative carry should not stall")
	}
	if got := ExtractorProduction(10, 0.1); got != 0 {
		t.Fatalf("extractor stalled should be 0 got %v", got)
	}
	if got := ExtractorProduction(10, 0); got != 10 {
		t.Fatalf("extractor not stalled should be 10 got %v", got)
	}
	if got := MakerProduction(1, 0.1); got != 0 {
		t.Fatalf("maker stalled")
	}
	if got := MakerProduction(1, 0); got != 1.0 {
		t.Fatalf("maker not stalled should be 1")
	}
	// Integration with PerUnitProductionFills
	w := units.New(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	svc.Players[0].StatusHalfwordAt144 = 1
	svc.Players[0].EndGameCountdown = -1
	def := &content.UnitDef{UnitName: "armmakr", MakesMetal: 1, ExtractsMetal: 0, EnergyMake: 0, MetalMake: 0, BuildTime: 100, MaxDamage: 100}
	def.CanonicalKey = content.CanonicalKey("armmakr")
	// Create unit with energy carry >0
	h, _ := w.Create(def, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	u := w.Unit(h)
	u.Def = def
	// Set energy carry via bucket
	b := svc.UnitBuckets(h)
	b[Energy].Carry = 10 // stall
	svc.PerUnitProductionFills(0, w)
	if b[Metal].Production != 0 {
		t.Fatalf("maker should stall when energyCarry>0, production %v", b[Metal].Production)
	}
	// Reset carry 0 should produce
	b[Metal].Production = 0
	b[Energy].Carry = 0
	svc.PerUnitProductionFills(0, w)
	if b[Metal].Production != 1.0 {
		t.Fatalf("maker not stalled should produce 1, got %v", b[Metal].Production)
	}
	// Extractor with spotMetal
	def2 := &content.UnitDef{UnitName: "armmex", ExtractsMetal: 0.5, BuildTime: 100, MaxDamage: 100}
	def2.CanonicalKey = content.CanonicalKey("armmex")
	h2, _ := w.Create(def2, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	u2 := w.Unit(h2)
	u2.Def = def2
	u2.SpotMetal = 5 // sampled Σ(cell+1)*extractsMetal
	b2 := svc.UnitBuckets(h2)
	b2[Energy].Carry = 5 // stall
	svc.PerUnitProductionFills(0, w)
	// Need to clear previous production? Actually previous b already had 1, but b2 should be stalled 0
	if b2[Metal].Production != 0 {
		t.Fatalf("extractor stall expected 0 got %v", b2[Metal].Production)
	}
	b2[Energy].Carry = 0
	b2[Metal].Production = 0
	svc.PerUnitProductionFills(0, w)
	if b2[Metal].Production != 5 {
		t.Fatalf("extractor not stalled should be 5 got %v", b2[Metal].Production)
	}
}

// TestNegativeEnergyUseRefund locks 0.5/0.7 discount when controller==2 [P1-06].
func TestNegativeEnergyUseRefund(t *testing.T) {
	// Plain add when controller !=2
	if got := NegativeEnergyUseRefund(-10, 1, 0); got != 10 {
		t.Fatalf("negative energyUse controller1 plain 10 got %v", got)
	}
	// Controller 2 selector 0 => -0.5
	if got := NegativeEnergyUseRefund(-10, 2, 0); got != -5 {
		t.Fatalf("controller2 sel0 => -5 got %v", got)
	}
	// Controller 2 selector 1 => -0.7
	if got := NegativeEnergyUseRefund(-10, 2, 1); got != -7 {
		t.Fatalf("sel1 => -7 got %v", got)
	}
	// Other selector plain
	if got := NegativeEnergyUseRefund(-10, 2, 2); got != 10 {
		t.Fatalf("sel2 fallback plain 10 got %v", got)
	}
	// Positive energyUse not refund
	if got := NegativeEnergyUseRefund(10, 2, 0); got != 0 {
		t.Fatalf("positive should be 0 got %v", got)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestThresholdsDistinct(t *testing.T) {
	p := &Player{}
	p.Capacity[Metal] = 1000
	p.Capacity[Energy] = 800
	InitShareThresholds(p)
	if p.MetalShareThreshold != 1000 || p.EnergyShareThreshold != 800 {
		t.Fatalf("thresholds should copy capacity initially, got %v %v", p.MetalShareThreshold, p.EnergyShareThreshold)
	}
	// Modify capacity should not affect thresholds (distinct fields)
	p.Capacity[Metal] = 500
	if p.MetalShareThreshold != 1000 {
		t.Fatalf("threshold distinct from capacity, got %v want 1000", p.MetalShareThreshold)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestRefillMinGap(t *testing.T) {
	var svc Service
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	svc.Players[0].StatusHalfwordAt144 = 1
	svc.Players[0].EndGameCountdown = -1
	svc.Players[0].AutoShareMetal = true
	svc.Players[0].AutoShareEnergy = true
	svc.Players[0].Capacity[Metal] = 1000
	svc.Players[0].Capacity[Energy] = 1000
	svc.Players[0].MetalShareThreshold = 200
	svc.Players[0].EnergyShareThreshold = 200
	svc.Players[0].Stock[Metal] = 800
	svc.Players[0].Stock[Energy] = 800
	svc.Players[0].Allies[1] = true
	svc.Players[1].Exists = true
	svc.Players[1].ControllerState = 1
	svc.Players[1].StatusHalfwordAt144 = 1
	svc.Players[1].EndGameCountdown = -1
	svc.Players[1].Capacity[Metal] = 1000
	svc.Players[1].Capacity[Energy] = 1000
	svc.Players[1].Stock[Metal] = 100
	svc.Players[1].Stock[Energy] = 100
	svc.ReferencePlayer = 0
	// Metal: excess 600 *0.333=200, gap 900 => transfer 200
	svc.ShareTick(60)
	if svc.Players[0].Stock[Metal] != 600 || svc.Players[1].Stock[Metal] != 300 {
		t.Fatalf("metal refill min gap failed stock %v %v", svc.Players[0].Stock[Metal], svc.Players[1].Stock[Metal])
	}
	// Reset for energy: excess 600*0.5=300 gap 900 => 300
	svc.Players[0].Stock[Energy] = 800
	svc.Players[1].Stock[Energy] = 100
	// Over-cap gap clamp: src high, dst near cap but still lower than src, gap small => transfer clamped to gap [P1-06]
	svc.Players[0].Stock[Metal] = 1000
	svc.Players[0].MetalShareThreshold = 200 // excess 800*0.333=266
	svc.Players[1].Stock[Metal] = 999
	svc.Players[1].Capacity[Metal] = 1000 // gap 1
	svc.Players[0].Stock[Energy] = 800
	svc.Players[1].Stock[Energy] = 100
	svc.ReferencePlayer = 0
	svc.ShareTick(120) // 120%60==0
	if svc.Players[1].Stock[Metal] != 1000 {
		t.Fatalf("over-cap gap clamp should clamp to 1000 got %v", svc.Players[1].Stock[Metal])
	}
	if svc.Players[0].Stock[Metal] != 999 {
		t.Fatalf("src after over-cap transfer should be 999 got %v", svc.Players[0].Stock[Metal])
	}
}

// TestLastWinsAlliances locks last-wins 0..9 [P1-06].
func TestLastWinsAlliances(t *testing.T) {
	var svc Service
	for i := 0; i < 10; i++ {
		svc.Players[i].Exists = true
		svc.Players[i].ControllerState = 1
		svc.Players[i].StatusHalfwordAt144 = 1
		svc.Players[i].EndGameCountdown = -1
		svc.Players[i].Capacity[Metal] = 1000
		svc.Players[i].Stock[Metal] = 500
	}
	svc.Players[0].Stock[Metal] = 800
	svc.Players[0].MetalShareThreshold = 200
	svc.Players[0].AutoShareMetal = true
	svc.Players[0].Allies[1] = true
	svc.Players[0].Allies[2] = true
	svc.Players[0].Allies[3] = true
	svc.Players[1].Stock[Metal] = 100
	svc.Players[2].Stock[Metal] = 50
	svc.Players[3].Stock[Metal] = 200
	svc.ReferencePlayer = 0
	svc.ShareTick(60)
	// All three lower than src (800), last qualifying wins => dest should be 3 (last index)
	if svc.Players[3].Stock[Metal] == 200 {
		t.Fatalf("last-wins should transfer to player 3, stock still 200")
	}
	if svc.Players[1].Stock[Metal] != 100 || svc.Players[2].Stock[Metal] != 50 {
		t.Fatalf("earlier candidates should not receive, got %v %v", svc.Players[1].Stock[Metal], svc.Players[2].Stock[Metal])
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestStatusPairPredicate(t *testing.T) {
	if !statusPairPredicate(1, 0) {
		t.Fatalf("1,0 should pass")
	}
	if !statusPairPredicate(1, 1234) {
		t.Fatalf("1,nonzero should pass via half")
	}
	if !statusPairPredicate(0, 0) {
		t.Fatalf("0,0 should pass via word zero")
	}
	if statusPairPredicate(0, 1) {
		t.Fatalf("0,1 should fail")
	}
	if !statusPairPredicate(-1, 999) {
		t.Fatalf("-1 nonzero should pass")
	}
}

// TestPacketOverwriteSync locks packet 0x16 overwrite-sync no threshold abort [P1-06].
func TestPacketOverwriteSync(t *testing.T) {
	var svc Service
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	svc.Players[0].StatusHalfwordAt144 = 1
	svc.Players[0].EndGameCountdown = -1
	svc.Players[0].Capacity[Metal] = 1000
	svc.Players[0].Stock[Metal] = 500
	svc.Players[1].Exists = true
	svc.Players[1].ControllerState = 1
	svc.Players[1].StatusHalfwordAt144 = 1
	svc.Players[1].EndGameCountdown = -1
	svc.Players[1].Capacity[Metal] = 1000
	svc.Players[1].Stock[Metal] = 500
	ApplySharePacket(&svc, 1, 100, 0, 1)
	if svc.Players[0].Stock[Metal] != 400 || svc.Players[1].Stock[Metal] != 600 {
		t.Fatalf("packet overwrite-sync failed %v %v", svc.Players[0].Stock[Metal], svc.Players[1].Stock[Metal])
	}
	// Over-cap clamp via packet
	svc.Players[1].Stock[Metal] = 995
	svc.Players[1].Capacity[Metal] = 1000
	ApplySharePacket(&svc, 1, 100, 0, 1)
	if svc.Players[1].Stock[Metal] != 1000 {
		t.Fatalf("packet over-cap gap clamp failed %v", svc.Players[1].Stock[Metal])
	}
}

// TestPreGameSpawnOutsideLedger locks pre-game one pass then spawn outside ledger, single ADD 30 catch-up [P1-06].
func TestPreGameSpawnOutsideLedger(t *testing.T) {
	var svc Service
	for i := 0; i < 2; i++ {
		svc.Players[i].Exists = true
		svc.Players[i].ControllerState = 1
		svc.Players[i].StatusHalfwordAt144 = 1
		svc.Players[i].EndGameCountdown = -1
	}
	svc.Players[0].Mirror[Metal].Production = 2
	svc.Players[1].Mirror[Metal].Production = 3
	svc.SeedDeadlines(0)
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	w := units.New(10, nil)
	svc.Tick(0, w)
	if svc.Players[0].UpdateTime != 30 || svc.Players[1].UpdateTime != 30 {
		t.Fatalf("pre-game one pass should settle active players and advance deadlines")
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[1].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 2 || svc.Players[1].PassProduced[Metal] != 3 {
		t.Fatalf("pre-game one pass must consume/archive each player's production")
	}
	producedBeforeSpawn := svc.Players[0].TotalProduced[Metal]
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	CreditSpawn(&svc.Players[0], Metal, 1000)
	if svc.Players[0].Stock[Metal] != 1000 {
		t.Fatalf("spawn credit outside ledger")
	}
	// Waste not incremented via spawn
	if svc.Players[0].TotalProduced[Metal] != producedBeforeSpawn {
		t.Fatalf("spawn should not affect TotalProduced: before=%v after=%v", producedBeforeSpawn, svc.Players[0].TotalProduced[Metal])
	}
	// Single ADD 30 catch-up: deadline advanced exactly 30 before settlement, not loop
	svc2 := Service{}
	svc2.Players[0].Exists = true
	svc2.Players[0].ControllerState = 1
	svc2.Players[0].StatusHalfwordAt144 = 1
	svc2.Players[0].EndGameCountdown = -1
	svc2.Players[0].UpdateTime = 0
	svc2.Players[0].Helper1Deadline = 1 << 31
	svc2.Players[0].Helper2Deadline = 1 << 31
	svc2.Players[0].Mirror[Metal].Production = 4
	// Tick 100 with deadline 0 => 100 behind, should settle once per tick, not loop to catch all at once
	svc2.TickPlayer(0, 100, w, nil)
	if svc2.Players[0].UpdateTime != 30 {
		t.Fatalf("deadline should be 30 after one catch-up, got %d", svc2.Players[0].UpdateTime)
	}
	if svc2.Players[0].Mirror[Metal].Production != 0 || svc2.Players[0].PassProduced[Metal] != 4 {
		t.Fatalf("single ADD 30 catch-up must execute one concrete settlement pass")
	}
}
