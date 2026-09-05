package economy

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestMakerStall locks the positive-carry maker gate [R-PROD-01 §5].
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
	if got := MakerProduction(8, 0); got != 8 {
		t.Fatalf("maker not stalled should produce maker byte")
	}
	// Integration with PerUnitProductionFills
	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	svc.Players[0].EndGameCountdown = -1
	// Both generator fixtures author `activatewhenbuilt`, as every stock maker
	// and extractor in the reference install does. That is what activates them:
	// the building branch of [R-ECO-01 §2] runs only on the engine-state
	// activation bit, and a definition authoring neither `activatewhenbuilt`
	// nor `onoffable` "never activates and never runs any generator"
	// [05 R-PROD-01 §2]. Already-built creation raises the edge, per
	// [04 R-SPEC-01 §12] site 1.
	def := economyFixtureDef(&content.UnitDef{UnitName: "armmakr", MakesMetal: 1, ExtractsMetal: 0, EnergyMake: 0, MetalMake: 0, BuildTime: 100, MaxDamage: 100, ActivateWhenBuilt: true})
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
	def2 := economyFixtureDef(&content.UnitDef{UnitName: "armmex", ExtractsMetal: 0.5, BuildTime: 100, MaxDamage: 100, ActivateWhenBuilt: true})
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

func TestGeneratorAndMobileBranchGates(t *testing.T) {
	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	wind := float32(0.25)
	svc.Wind = &world.Wind{Scalar: wind}
	def := economyFixtureDef(&content.UnitDef{UnitName: "wind", WindGenerator: 8, EnergyUse: 4, BuildTime: 1, MaxDamage: 1})
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Activated = true
	b := svc.UnitBuckets(h)
	b[Energy].Carry = 1
	svc.PerUnitProductionFills(0, w)
	if b[Energy].Production != wind*8 {
		t.Fatalf("wind should ignore upkeep admission, got %v", b[Energy].Production)
	}

	def.BMCode = true
	b[Energy] = Bucket{}
	u.Move.Mode = 1
	svc.PerUnitProductionFills(0, w)
	if b[Energy].Production != 0 {
		t.Fatalf("mobile unit must not reach generator branch, got %v", b[Energy].Production)
	}
}

func TestUpkeepAdmissionControlsExtractor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		energyUse  float64
		carry      float32
		wantEnergy float32
	}{
		{name: "negative-refund", energyUse: -1, wantEnergy: 1},
		{name: "zero-positive-carry", energyUse: 0, carry: 1},
		{name: "negative-zero-positive-carry", energyUse: math.Copysign(0, -1), carry: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := units.NewSliced(10, &content.Catalog{})
			svc := &Service{}
			svc.Players[0].Exists = true
			def := economyFixtureDef(&content.UnitDef{UnitName: tc.name, ExtractsMetal: 1, EnergyUse: tc.energyUse, BuildTime: 1, MaxDamage: 1})
			h, err := w.Create(def, 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			u := w.Unit(h)
			u.Activated = true
			u.SpotMetal = 9
			svc.UnitBuckets(h)[Energy].Carry = tc.carry
			svc.PerUnitProductionFills(0, w)
			b := svc.UnitBuckets(h)
			if b[Energy].Production != tc.wantEnergy {
				t.Fatalf("energy production = %v, want %v", b[Energy].Production, tc.wantEnergy)
			}
			if b[Metal].Production != 0 {
				t.Fatalf("extractor production admitted for energyUse=%v and carry=%v: %v", tc.energyUse, tc.carry, b[Metal].Production)
			}
		})
	}
}

// refundProduction runs one activated building authoring `energyUse` for a
// player with the given control byte and difficulty selector, and reports the
// energy production accumulator afterwards. `seed` pre-loads the accumulator so
// the test can observe whether the refund reaches it at working precision or as
// an already-narrowed float32.
func refundProduction(t *testing.T, energyUse float64, controller uint8, selector int, seed float32) float32 {
	t.Helper()
	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = controller
	svc.SetEconomySelector(selector)
	def := economyFixtureDef(&content.UnitDef{UnitName: "refund", EnergyUse: energyUse, BuildTime: 1, MaxDamage: 1})
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Activated = true
	b := svc.UnitBuckets(h)
	b[Energy].Production = seed
	svc.PerUnitProductionFills(0, w)
	return b[Energy].Production
}

// TestNegativeEnergyUseRefundFactors locks the computer-player refund factors on
// the folded call site [R-ECO-01 §2][R-ECO-01 §3]. The helper that used to
// pre-scale the refund into a float32 of its own is gone; the negated authored
// value now enters the shared discount ladder directly.
func TestNegativeEnergyUseRefundFactors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		energyUse  float64
		controller uint8
		selector   int
		want       float32
	}{
		{name: "human-plain", energyUse: -10, controller: 1, selector: 0, want: 10},
		{name: "computer-easy-half", energyUse: -10, controller: 2, selector: 0, want: 5},
		{name: "computer-medium-seven-tenths", energyUse: -10, controller: 2, selector: 1, want: 7},
		{name: "computer-hard-plain", energyUse: -10, controller: 2, selector: 2, want: 10},
		{name: "positive-use-no-refund", energyUse: 10, controller: 2, selector: 0, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refundProduction(t, tc.energyUse, tc.controller, tc.selector, 0); got != tc.want {
				t.Fatalf("energy production = %v, want %v [R-ECO-01 §3]", got, tc.want)
			}
		})
	}
}

// TestNegativeEnergyUseRefundNarrowsOnce is the E-5(b) regression guard.
//
// Retail's site forms `float32(production - amount * K)` in one expression, so
// the accumulator store is the only narrowing [R-ECO-01 §3]. The old code built
// `float32(amount * K)` first and added that float32 to production, narrowing
// twice — the factored form §3 warns "rounds differently". With a seeded
// accumulator the two forms land one single-precision bit apart:
//
//	seed   = 42.46375   (0x4229dae1)
//	amount = 68.682304  (0x42895d57), from an authored energyuse of -68.682304
//	two narrowings: 0x42b5152e        one narrowing: 0x42b5152d
//
// The fixture values are ours; only the arithmetic shape is retail's.
func TestNegativeEnergyUseRefundNarrowsOnce(t *testing.T) {
	const (
		seed         = float32(42.46375)
		twoRoundings = uint32(0x42b5152e)
		oneRounding  = uint32(0x42b5152d)
	)
	if math.Float32bits(seed) != 0x4229dae1 || math.Float32bits(float32(68.682304)) != 0x42895d57 {
		t.Fatalf("fixture constants do not carry their intended single-precision bit patterns")
	}
	got := refundProduction(t, -68.682304, 2, 1, seed)
	if bits := math.Float32bits(got); bits != oneRounding {
		if bits == twoRoundings {
			t.Fatalf("refund narrowed twice: production = %.17g (%08x); retail forms "+
				"float32(production - amount * -0.7) in one expression, expected %08x [R-ECO-01 §3]",
				float64(got), bits, oneRounding)
		}
		t.Fatalf("refund production = %.17g (%08x), want %08x [R-ECO-01 §3]", float64(got), bits, oneRounding)
	}
}

// TestWindProductNarrowsOnceForComputerPlayer is the E-5(a) regression guard.
//
// [R-ECO-01 §2] states the four generator products are "formed as one multiply
// and one add with no intermediate narrowing", and [R-ECO-01 §3] adds that
// "rounding the product to single first is not bit-identical". The old
// addContribution narrowed its argument on entry, so an AI-owned wind generator
// on medium difficulty rounded the discounted credit off the single-precision
// product instead of the working-precision one.
//
// Fixture (ours, chosen so the two forms differ in the last bit): wind scalar
// 0.06, windgenerator 3, empty accumulator, control byte 2, difficulty selector
// 1 (medium, factor -0.7).
//
//	product narrowed first: 0x3e010624 (0.12599998712539673)
//	product kept wide:      0x3e010625 (0.12600000202655792)
func TestWindProductNarrowsOnceForComputerPlayer(t *testing.T) {
	const (
		narrowedFirst = uint32(0x3e010624)
		keptWide      = uint32(0x3e010625)
	)
	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 2 // the computer player [R-ECO-01 §3]
	svc.SetEconomySelector(1)          // medium
	svc.Wind = &world.Wind{Scalar: 0.06}
	def := economyFixtureDef(&content.UnitDef{UnitName: "windbit", WindGenerator: 3, BuildTime: 1, MaxDamage: 1})
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Activated = true
	b := svc.UnitBuckets(h)
	svc.PerUnitProductionFills(0, w)

	got := b[Energy].Production
	if bits := math.Float32bits(got); bits != keptWide {
		if bits == narrowedFirst {
			t.Fatalf("wind product narrowed before the discount: production = %.17g (%08x); "+
				"[R-ECO-01 §2] forms the product with no intermediate narrowing, expected %08x",
				float64(got), bits, keptWide)
		}
		t.Fatalf("wind production = %.17g (%08x), want %08x [R-ECO-01 §2][R-ECO-01 §3]", float64(got), bits, keptWide)
	}
}

// TestTidalProductNarrowsOnceForComputerPlayer is the tidal half of E-5(a). The
// tidal scalar is the map's tidal word over 65536, so the fixture picks a word
// whose scalar times the generator value straddles a single-precision boundary:
// 1294337/65536 = 19.750015, tidalgenerator 13, medium difficulty.
//
//	product narrowed first: 0x4333b9a2 (179.72512817382812)
//	product kept wide:      0x4333b9a3 (179.72514343261719)
func TestTidalProductNarrowsOnceForComputerPlayer(t *testing.T) {
	const (
		narrowedFirst = uint32(0x4333b9a2)
		keptWide      = uint32(0x4333b9a3)
	)
	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 2
	svc.SetEconomySelector(1)
	svc.Terrain = &world.Terrain{Tidal: 1294337}
	def := economyFixtureDef(&content.UnitDef{UnitName: "tidalbit", TidalGenerator: 13, BuildTime: 1, MaxDamage: 1})
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Activated = true
	b := svc.UnitBuckets(h)
	svc.PerUnitProductionFills(0, w)

	got := b[Energy].Production
	if bits := math.Float32bits(got); bits != keptWide {
		if bits == narrowedFirst {
			t.Fatalf("tidal product narrowed before the discount: production = %.17g (%08x), "+
				"expected %08x [R-ECO-01 §2]", float64(got), bits, keptWide)
		}
		t.Fatalf("tidal production = %.17g (%08x), want %08x [R-ECO-01 §2][R-ECO-01 §3]", float64(got), bits, keptWide)
	}
}

// TestThresholdsDistinct locks zero thresholds separate from capacity [R-SHARE-01 §3].
func TestThresholdsDistinct(t *testing.T) {
	p := &Player{}
	p.Capacity[Metal] = 1000
	p.Capacity[Energy] = 800
	InitShareThresholds(p)
	if p.MetalShareThreshold != 0 || p.EnergyShareThreshold != 0 {
		t.Fatalf("thresholds should initialize to zero, got %v %v", p.MetalShareThreshold, p.EnergyShareThreshold)
	}
	// Modify capacity should not affect thresholds (distinct fields)
	p.Capacity[Metal] = 500
	if p.MetalShareThreshold != 0 {
		t.Fatalf("threshold distinct from capacity, got %v want 0", p.MetalShareThreshold)
	}
}

// TestRefillMinGap locks automatic sharing's gap and ratio [R-SHARE-01 §3].
func TestRefillMinGap(t *testing.T) {
	var svc Service
	svc.Networked = true
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
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
	svc.Players[1].ControllerState = 3
	svc.Players[1].OptionKind = 1
	svc.Players[1].EndGameCountdown = -1
	svc.Players[1].Capacity[Metal] = 1000
	svc.Players[1].Capacity[Energy] = 1000
	svc.Players[1].Stock[Metal] = 100
	svc.Players[1].Stock[Energy] = 100
	svc.ReferencePlayer = 0
	// Metal: excess 600 *0.333=200, gap 900 => transfer 200
	svc.ShareTick(60, nil)
	if svc.Players[0].Stock[Metal] != 600 || svc.Players[1].Mirror[Metal].Production != 200 {
		t.Fatalf("metal refill min gap failed source=%v production=%v", svc.Players[0].Stock[Metal], svc.Players[1].Mirror[Metal].Production)
	}
	// Reset for energy: excess 600*0.5=300 gap 900 => 300
	svc.Players[0].Stock[Energy] = 800
	svc.Players[1].Stock[Energy] = 100
	// Source amount is capped by the destination capacity gap.
	svc.Players[0].Stock[Metal] = 1000
	svc.Players[0].MetalShareThreshold = 200 // excess 800*0.333=266
	svc.Players[1].Stock[Metal] = 999
	svc.Players[1].Capacity[Metal] = 1000 // gap 1
	svc.Players[1].Mirror[Metal].Production = 0
	svc.Players[0].Stock[Energy] = 800
	svc.Players[1].Stock[Energy] = 100
	svc.ReferencePlayer = 0
	svc.ShareTick(120, nil) // 120%60==0
	if svc.Players[1].Mirror[Metal].Production != 1 {
		t.Fatalf("automatic amount should be capped to the destination gap, production=%v", svc.Players[1].Mirror[Metal].Production)
	}
	if svc.Players[0].Stock[Metal] != 999 {
		t.Fatalf("src after over-cap transfer should be 999 got %v", svc.Players[0].Stock[Metal])
	}
}

// TestLastWinsAlliances locks the ascending scan's last-wins rule [R-SHARE-01 §3].
func TestLastWinsAlliances(t *testing.T) {
	var svc Service
	svc.Networked = true
	for i := 0; i < 10; i++ {
		svc.Players[i].Exists = true
		svc.Players[i].ControllerState = 1
		svc.Players[i].EndGameCountdown = -1
		svc.Players[i].Capacity[Metal] = 1000
		svc.Players[i].Stock[Metal] = 500
	}
	for i := 1; i < 10; i++ {
		svc.Players[i].ControllerState = 3
		svc.Players[i].OptionKind = 1
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
	svc.ShareTick(60, nil)
	// All three lower than src (800), last qualifying wins => dest should be 3 (last index)
	if svc.Players[3].Mirror[Metal].Production == 0 {
		t.Fatalf("last-wins should transfer to player 3")
	}
	if svc.Players[1].Stock[Metal] != 100 || svc.Players[2].Stock[Metal] != 50 {
		t.Fatalf("earlier candidates should not receive, got %v %v", svc.Players[1].Stock[Metal], svc.Players[2].Stock[Metal])
	}
}

// eliminatedWorldForPlayerZero returns a world in which player 0 has created
// one unit and lost it, so its live count is zero and its ever-created count is
// one — the elimination state of [08 R-SKIR-01 §3] "Counters".
func eliminatedWorldForPlayerZero(t *testing.T) *units.World {
	t.Helper()
	w := units.NewSliced(10, &content.Catalog{})
	def := economyFixtureDef(&content.UnitDef{UnitName: "armcom", BuildTime: 100, MaxDamage: 100})
	def.CanonicalKey = content.CanonicalKey("armcom")
	h, err := w.Create(def, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w.Unit(h).Dying = true
	if res := w.FinalizeDeath(h, 30); !res.Freed {
		t.Fatalf("FinalizeDeath did not free the slot")
	}
	if w.LiveCountForPlayer(0) != 0 || w.CreatedCountForPlayer(0) == 0 {
		t.Fatalf("fixture is not the eliminated state: live=%d created=%d",
			w.LiveCountForPlayer(0), w.CreatedCountForPlayer(0))
	}
	return w
}

// TestSettlementGateIsTheEliminationTest locks the correction of
// [05 R-ECO-01 §12]: what stood as an unresolved literal "status pair" is the
// elimination test on the player record's two unit counters. An eliminated slot
// does not settle; a slot that has never created a unit does, which is what
// keeps a participating row alive through battle entry.
func TestSettlementGateIsTheEliminationTest(t *testing.T) {
	// Never created a unit: the second term holds, so the slot settles.
	fresh := units.NewSliced(10, &content.Catalog{})
	var svc Service
	p := &svc.Players[0]
	activePlayer(p)
	p.UpdateTime = 100
	p.Helper1Deadline = 200
	p.Helper2Deadline = 200
	p.Mirror[Metal].Production = 4
	svc.TickPlayer(0, 100, fresh, nil)
	if p.UpdateTime != 130 {
		t.Fatalf("deadline must advance to 130, got %d", p.UpdateTime)
	}
	if p.Mirror[Metal].Production != 0 || p.PassProduced[Metal] != 4 {
		t.Fatalf("never-created slot must settle, live=%v pass=%v",
			p.Mirror[Metal].Production, p.PassProduced[Metal])
	}

	// Live count zero with a non-zero ever-created count: eliminated. The
	// deadline still advances because the advance precedes the gate chain.
	dead := eliminatedWorldForPlayerZero(t)
	var svc2 Service
	q := &svc2.Players[0]
	activePlayer(q)
	q.UpdateTime = 100
	q.Helper1Deadline = 200
	q.Helper2Deadline = 200
	q.Mirror[Metal].Production = 4
	svc2.TickPlayer(0, 100, dead, nil)
	if q.UpdateTime != 130 {
		t.Fatalf("eliminated slot must still advance its deadline, got %d", q.UpdateTime)
	}
	if q.Mirror[Metal].Production != 4 || q.PassProduced[Metal] != 0 {
		t.Fatalf("eliminated slot must not settle, live=%v pass=%v",
			q.Mirror[Metal].Production, q.PassProduced[Metal])
	}
}

// TestPacketOverwriteSync locks receipt's no-recheck credit [R-SHARE-01 §4].
func TestPacketOverwriteSync(t *testing.T) {
	var svc Service
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1
	svc.Players[0].EndGameCountdown = -1
	svc.Players[0].Capacity[Metal] = 1000
	svc.Players[0].Stock[Metal] = 500
	svc.Players[1].Exists = true
	svc.Players[1].ControllerState = 3
	svc.Players[1].OptionKind = 1
	svc.Players[1].EndGameCountdown = -1
	svc.Players[1].Capacity[Metal] = 1000
	svc.Players[1].Stock[Metal] = 500
	ApplySharePacket(&svc, 2, 100, 0, 1)
	if svc.Players[0].Stock[Metal] != 500 || svc.Players[1].Mirror[Metal].Production != 100 {
		t.Fatalf("packet receipt failed stock=%v production=%v", svc.Players[0].Stock[Metal], svc.Players[1].Mirror[Metal].Production)
	}
	// Over-cap clamp via packet
	svc.Players[1].Stock[Metal] = 995
	svc.Players[1].Capacity[Metal] = 1000
	ApplySharePacket(&svc, 2, 100, 0, 1)
	if svc.Players[1].Mirror[Metal].Production != 200 {
		t.Fatalf("packet should defer capacity clamp, production=%v", svc.Players[1].Mirror[Metal].Production)
	}
}

func TestSharePacketSubtypeMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		res  Res
	}{
		{name: "energy", code: 1, res: Energy},
		{name: "metal", code: 2, res: Metal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svc Service
			ApplySharePacket(&svc, tc.code, 7, 0, 1)
			if got := svc.Players[1].Mirror[tc.res].Production; got != 7 {
				t.Fatalf("subtype %d credited resource %d production %v, want 7", tc.code, tc.res, got)
			}
			if got := svc.Players[1].Mirror[1-tc.res].Production; got != 0 {
				t.Fatalf("subtype %d also credited other resource %v", tc.code, got)
			}
		})
	}
}

// TestPreGameSpawnOutsideLedger locks setup settlement and direct spawn credit [05 "Authoritative settlement order"].
func TestPreGameSpawnOutsideLedger(t *testing.T) {
	var svc Service
	for i := 0; i < 2; i++ {
		svc.Players[i].Exists = true
		svc.Players[i].ControllerState = 1
		svc.Players[i].EndGameCountdown = -1
	}
	svc.Players[0].Mirror[Metal].Production = 2
	svc.Players[1].Mirror[Metal].Production = 3
	svc.SeedDeadlines(0)
	// Simulate one full setup player-phase pass.
	w := units.NewSliced(10, nil)
	svc.Tick(0, w)
	if svc.Players[0].UpdateTime != 30 || svc.Players[1].UpdateTime != 30 {
		t.Fatalf("pre-game one pass should settle active players and advance deadlines")
	}
	if svc.Players[0].Mirror[Metal].Production != 0 || svc.Players[1].Mirror[Metal].Production != 0 || svc.Players[0].PassProduced[Metal] != 2 || svc.Players[1].PassProduced[Metal] != 3 {
		t.Fatalf("pre-game one pass must consume/archive each player's production")
	}
	producedBeforeSpawn := svc.Players[0].TotalProduced[Metal]
	// Spawn credits outside ledger.
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
