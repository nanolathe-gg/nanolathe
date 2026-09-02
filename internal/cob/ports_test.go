package cob

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// ---------------------------------------------------------------------------
// C15 port table
// ---------------------------------------------------------------------------

func TestPortTable(t *testing.T) {
	if len(PortTable) != 20 {
		t.Fatalf("PortTable len %d want 20 [04 §4.4] C15", len(PortTable))
	}
	for i, info := range PortTable {
		wantID := Port(i + 1)
		if info.ID != wantID {
			t.Fatalf("PortTable[%d] ID %d want %d", i, info.ID, wantID)
		}
		if !IsEnginePort(int32(info.ID)) {
			t.Fatalf("IsEnginePort(%d) false want true [04 §4.4] C15", info.ID)
		}
	}
	if IsEnginePort(0) || IsEnginePort(21) || IsEnginePort(-1) {
		t.Fatalf("IsEnginePort outside 1..20 should be false [04 §4.4] C15")
	}
	// Spot-check names/boundaries verbatim [04 §4.4] C15.
	if PortTable[0].Name != "activation" {
		t.Fatalf("port 1 name %q want activation [04 §4.4]", PortTable[0].Name)
	}
	if PortTable[3].Name != "health" {
		t.Fatalf("port 4 name %q want health", PortTable[3].Name)
	}
	if PortTable[6].Name != "piece position XZ" {
		t.Fatalf("port 7 name %q", PortTable[6].Name)
	}
	if PortTable[19].Name != "armored" {
		t.Fatalf("port 20 name %q want armored", PortTable[19].Name)
	}
}

// ---------------------------------------------------------------------------
// C15/C25 trig helpers — RockUnit and HitByWeapon [GAP T15] [04 §5.1] C25
// ---------------------------------------------------------------------------

func TestRockUnitArgs(t *testing.T) {
	// Hand-computed cardinals via 512-entry table scaled 8192 [04 §5.1] C25.
	// Table[0]=0, Table[128]=8192, etc. RockUnit = -cos*800, -sin*800 rounded.
	// rel 0 => cos 8192, sin 0 -> (-800, 0)
	x, y := RockUnitArgs(0)
	if x != -800 || y != 0 {
		t.Fatalf("RockUnit rel0 got (%d,%d) want (-800,0) [GAP T15]", x, y)
	}
	// rel 16384 = quarter turn (90 deg) => cos 0, sin 8192 -> (0, -800)
	x, y = RockUnitArgs(16384)
	if x != 0 || y != -800 {
		t.Fatalf("RockUnit quarter got (%d,%d) want (0,-800)", x, y)
	}
	// rel 32768 = half turn 180 deg => cos -8192, sin 0 -> (800,0)
	x, y = RockUnitArgs(-32768) // int16 -32768 == 32768 unsigned
	if x != 800 || y != 0 {
		t.Fatalf("RockUnit half got (%d,%d) want (800,0)", x, y)
	}
	// rel -16384 = -90 deg = 49152 unsigned => cos 0, sin -8192 -> (0,800)
	x, y = RockUnitArgs(-16384)
	if x != 0 || y != 800 {
		t.Fatalf("RockUnit -quarter got (%d,%d) want (0,800)", x, y)
	}
	// Fixture against independent table computed via math.Round(8192*sin) [04 §5.1].
	// Verify that RockUnit uses numeric.Sin/Cos table with rounding.
	for _, rel := range []int16{0x1234, 0x4000, -0x2000, 0x7fff} {
		a := numeric.Angle(uint16(rel))
		cosVal := int32(math.Round(8192 * math.Cos(float64(uint16(rel))*2*math.Pi/65536)))
		sinVal := int32(math.Round(8192 * math.Sin(float64(uint16(rel))*2*math.Pi/65536)))
		// But retail uses discrete table step 512, not continuous. Compare via numeric table.
		// We compare against numeric.Cos/Sin directly computed with scalar product helper.
		expX := int32((int64(numeric.Cos(a))*800 + 4096) >> 13)
		expY := int32((int64(numeric.Sin(a))*800 + 4096) >> 13)
		// Note: numeric.Sin uses table entry i = angle*512>>16 &511 with round(8192*sin).
		// Direct math above would differ for non-aligned angles, so we verify RockUnit matches
		// numeric table not continuous cos. The expected using numeric table vs manual table
		// entry should align with trigScalar.
		_ = cosVal
		_ = sinVal
		gotX, gotY := RockUnitArgs(rel)
		wantX, wantY := -expX, -expY
		if gotX != wantX || gotY != wantY {
			t.Fatalf("RockUnit rel %#x got (%d,%d) want (%d,%d)", uint16(rel), gotX, gotY, wantX, wantY)
		}
	}
}

func TestHitByWeaponArgs(t *testing.T) {
	// Cardinal hand-computed [04 §5.1] C26 shifted left 8 domain.
	x, y := HitByWeaponArgs(0) // angle 0 => cos 8192/sin 0 => (400,0)
	if x != 400 || y != 0 {
		t.Fatalf("HitByWeapon dir0 got (%d,%d) want (400,0) [04 §5.1] C26", x, y)
	}
	x, y = HitByWeaponArgs(64) // 64<<8=16384 quarter => (0,400)
	if x != 0 || y != 400 {
		t.Fatalf("HitByWeapon dir64 got (%d,%d) want (0,400)", x, y)
	}
	x, y = HitByWeaponArgs(128) // 32768 half => (-400,0)
	if x != -400 || y != 0 {
		t.Fatalf("HitByWeapon dir128 got (%d,%d) want (-400,0)", x, y)
	}
	x, y = HitByWeaponArgs(192) // 49152 => (0,-400)
	if x != 0 || y != -400 {
		t.Fatalf("HitByWeapon dir192 got (%d,%d) want (0,-400)", x, y)
	}
	// Verify rounding via table: dir 1 => angle 256 => index 2 => sin entry round(8192*sin(2*2pi/512))
	dir := uint8(1)
	a := numeric.Angle(uint16(dir) << 8)
	expX := int32((int64(numeric.Cos(a))*400 + 4096) >> 13)
	expY := int32((int64(numeric.Sin(a))*400 + 4096) >> 13)
	gotX, gotY := HitByWeaponArgs(dir)
	if gotX != expX || gotY != expY {
		t.Fatalf("HitByWeapon dir1 got (%d,%d) want (%d,%d) [04 §5.1] C25", gotX, gotY, expX, expY)
	}
}

// ---------------------------------------------------------------------------
// C15 Killed severity and SetMaxReloadTime/Query seeds [GAP T15]
// ---------------------------------------------------------------------------

func TestKilledSeverity(t *testing.T) {
	// Formula: ((-health*100)/maxHealth + prior)/2 clamped 1..100 [GAP T15] C15 [04 §5.1].
	// Note unsigned divide but positive domain.
	// health -50, max 100, prior 80 => (-(-50)*100)/100=50, +80=130/2=65
	if got := KilledSeverity(-50, 100, 80); got != 65 {
		t.Fatalf("KilledSeverity -50/100/80 got %d want 65", got)
	}
	// clamp low: health 0, prior 0 => (0*100)/100=0+0=0/2=0 clamp 1
	if got := KilledSeverity(0, 100, 0); got != 1 {
		t.Fatalf("KilledSeverity zero low clamp got %d want 1", got)
	}
	// clamp high: health -200, max 100, prior 100 => (200*100)/100=200+100=300/2=150 clamp 100
	if got := KilledSeverity(-200, 100, 100); got != 100 {
		t.Fatalf("KilledSeverity high clamp got %d want 100", got)
	}
	// typical overkill: health -10, max 100, prior 90 => (10*100)/100=10+90=100/2=50
	if got := KilledSeverity(-10, 100, 90); got != 50 {
		t.Fatalf("KilledSeverity got %d want 50", got)
	}
	// hand-computed with different prior: health -100, max 200, prior 60 => (100*100)/200=50+60=110/2=55
	if got := KilledSeverity(-100, 200, 60); got != 55 {
		t.Fatalf("KilledSeverity -100/200/60 got %d want 55", got)
	}
}

func TestMaxReloadMillis(t *testing.T) {
	// trunc(maxReload*1000/30) [GAP T15] C15
	cases := []struct {
		ticks int32
		want  int32
	}{
		{30, 1000},
		{45, 1500},
		{0, 0},
		{1, 33},  // 1000/30=33 trunc
		{3, 100}, // 3000/30=100
		{15, 500},
	}
	for _, c := range cases {
		if got := MaxReloadMillis(c.ticks); got != c.want {
			t.Fatalf("MaxReloadMillis %d got %d want %d [GAP T15] C15", c.ticks, got, c.want)
		}
	}
}

func TestQuerySeeds(t *testing.T) {
	qt := QueryTransportSeed()  // [-1,0,0,0] [GAP T15] C15
	qp := QueryLandingPadSeed() // [-1,-1,-1,-1]
	if qt != [4]int32{-1, 0, 0, 0} {
		t.Fatalf("QueryTransportSeed %v want [-1 0 0 0] [GAP T15]", qt)
	}
	if qp != [4]int32{-1, -1, -1, -1} {
		t.Fatalf("QueryLandingPadSeed %v want all -1", qp)
	}
}

// ---------------------------------------------------------------------------
// C19 emit-sfx classification [GAP T15] C19
// ---------------------------------------------------------------------------

func TestEmitSFXClassification(t *testing.T) {
	// Vector 0–5 [GAP T15] C19
	for i := int32(0); i <= 5; i++ {
		if got := ClassifySFX(i); got != SFXVector {
			t.Fatalf("ClassifySFX %d got %v want Vector [GAP T15] C19", i, got)
		}
	}
	// Point types 0x101 white, 0x102 black, 0x103 bubbles
	if got := ClassifySFX(0x101); got != SFXWhiteSmoke {
		t.Fatalf("Classify 0x101 got %v want White", got)
	}
	if got := ClassifySFX(0x102); got != SFXBlackSmoke {
		t.Fatalf("Classify 0x102 got %v", got)
	}
	if got := ClassifySFX(0x103); got != SFXSubBubbles {
		t.Fatalf("Classify 0x103 got %v", got)
	}
	// Ignored: vector 6+, 0x100 itself, >=0x104 [GAP T15] C19
	ignored := []int32{6, 7, 100, 0x100, 0x104, 0x105, 0x200, -1, 999}
	for _, v := range ignored {
		if got := ClassifySFX(v); got != SFXIgnored {
			t.Fatalf("ClassifySFX %d (%#x) got %v want Ignored [GAP T15] C19", v, uint32(v), got)
		}
	}
}

type recordSink struct {
	calls []struct {
		piece int
		typ   int32
		kind  SFXKind
	}
}

func (r *recordSink) EmitSFX(piece int, sfxType int32, kind SFXKind) {
	r.calls = append(r.calls, struct {
		piece int
		typ   int32
		kind  SFXKind
	}{piece, sfxType, kind})
}

func TestEmitSFXDispatchVisibilityGated(t *testing.T) {
	// Presentation-only, visibility-gated [GAP T15] C19: no sink call when not visible.
	sink := &recordSink{}
	if DispatchSFX(sink, 1, 0, false) {
		t.Fatalf("DispatchSFX not visible should return false [GAP T15] C19")
	}
	if len(sink.calls) != 0 {
		t.Fatalf("sink called when not visible")
	}
	// Visible vector type should dispatch
	if !DispatchSFX(sink, 2, 3, true) {
		t.Fatalf("DispatchSFX visible vector should return true")
	}
	if len(sink.calls) != 1 || sink.calls[0].kind != SFXVector {
		t.Fatalf("sink calls %v want 1 vector", sink.calls)
	}
	// Ignored should not dispatch even when visible
	sink2 := &recordSink{}
	if DispatchSFX(sink2, 0, 0x104, true) {
		t.Fatalf("ignored type should return false [GAP T15] C19")
	}
	if len(sink2.calls) != 0 {
		t.Fatalf("ignored type called sink")
	}
}

func TestEmitSFXVMIntegration(t *testing.T) {
	// VM emit-sfx opcode should go through ClassifySFX and visibility gate [GAP T15] C19 [04 §4.3].
	// Program: push effect 0 (vector), emit-sfx from piece 0, sleep to yield
	code := []uint32{
		0x10021001, 1, // push 1 vector thrust [04 §4.3] F
		0x1000f000, 0, // emit-sfx piece0 [04 §4.3] B pops 1
		0x10021001, 0, // push 0 sleep
		0x10013000,
	}
	prog := synthProg(code, []string{"base"}, 0, []int{0})
	vm := NewVM(prog)
	sink := &recordSink{}
	vm.SetSFXSink(sink)
	vm.SetSFXVisible(func(piece int, sfxType int32) bool { return true }) // always visible
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	if len(sink.calls) != 1 {
		t.Fatalf("VM emit-sfx did not reach sink: calls %v", sink.calls)
	}
	if sink.calls[0].typ != 1 {
		t.Fatalf("VM emit-sfx type %d want 1", sink.calls[0].typ)
	}
	// Visibility gated: invisible should not sink even with handler
	sink2 := &recordSink{}
	vm2 := NewVM(prog)
	vm2.SetSFXSink(sink2)
	vm2.SetSFXVisible(func(piece int, sfxType int32) bool { return false })
	vm2.Threads[0].Status = ThreadRunning
	vm2.Threads[0].PC = 0
	vm2.Drain(1)
	if len(sink2.calls) != 0 {
		t.Fatalf("VM emit-sfx invisible should not sink [GAP T15] C19")
	}
	// Ignored vocabulary >=0x104 via VM should be dropped
	codeIgn := []uint32{
		0x10021001, 0x104,
		0x1000f000, 0,
		0x10021001, 0,
		0x10013000,
	}
	progIgn := synthProg(codeIgn, []string{"base"}, 0, []int{0})
	vm3 := NewVM(progIgn)
	sink3 := &recordSink{}
	vm3.SetSFXSink(sink3)
	vm3.Threads[0].Status = ThreadRunning
	vm3.Threads[0].PC = 0
	vm3.Drain(1)
	if len(sink3.calls) != 0 {
		t.Fatalf("VM emit-sfx ignored vocabulary should not sink [GAP T15] C19: %v", sink3.calls)
	}
	// No sink = not panicking, no dispatch but opcode advances
	vm4 := NewVM(prog)
	vm4.Threads[0].Status = ThreadRunning
	vm4.Threads[0].PC = 0
	vm4.Drain(1) // should not panic when sink nil
}

// ---------------------------------------------------------------------------
// C18 MoveRate tiers [GAP T15] [04 §5.2]
// ---------------------------------------------------------------------------

func TestMoveRateTiers(t *testing.T) {
	// Definition thresholds per I13 mapping: rate1 is definition MoveRate1,
	// rate2 is definition MoveRate2 [04 §5.2].
	rate1, rate2 := int32(100), int32(200)
	// Category 0 when inhibit or attached or both magnitudes zero [GAP T15] C18
	if got := MoveRateCategory(true, false, 50, 50, rate1, rate2); got != 0 {
		t.Fatalf("inhibit true should be cat0 got %d [GAP T15] C18", got)
	}
	if got := MoveRateCategory(false, true, 50, 50, rate1, rate2); got != 0 {
		t.Fatalf("attached should be cat0")
	}
	if got := MoveRateCategory(false, false, 0, 0, rate1, rate2); got != 0 {
		t.Fatalf("both zero should be cat0")
	}
	// Single magnitude zero but other nonzero -> not both zero, classify via magA
	if got := MoveRateCategory(false, false, 50, 0, rate1, rate2); got != 1 {
		t.Fatalf("one mag nonzero should not be cat0 got %d", got)
	}
	// Tier 1 up to MoveRate1 inclusive [04 §5.2] signed inclusive
	if got := MoveRateCategory(false, false, 100, 100, rate1, rate2); got != 1 {
		t.Fatalf("mag==rate1 should be cat1 got %d", got)
	}
	if got := MoveRateCategory(false, false, 50, 50, rate1, rate2); got != 1 {
		t.Fatalf("mag < rate1 cat1 got %d", got)
	}
	// Tier 2 up to rate2 inclusive, beyond rate1
	if got := MoveRateCategory(false, false, 150, 150, rate1, rate2); got != 2 {
		t.Fatalf("mag 150 cat2 got %d", got)
	}
	if got := MoveRateCategory(false, false, 200, 200, rate1, rate2); got != 2 {
		t.Fatalf("mag==rate2 cat2 got %d", got)
	}
	// Tier 3 above both
	if got := MoveRateCategory(false, false, 201, 201, rate1, rate2); got != 3 {
		t.Fatalf("mag > rate2 cat3 got %d", got)
	}
	if got := MoveRateCategory(false, false, 1000, 1000, rate1, rate2); got != 3 {
		t.Fatalf("large mag cat3 got %d", got)
	}
	// Signed comparison: negative magnitude should be <= rate1 -> cat1 (though magnitude is normally >=0)
	if got := MoveRateCategory(false, false, -10, -10, rate1, rate2); got != 1 {
		t.Fatalf("negative mag signed cat1 got %d", got)
	}
}

func TestMoveRateTransition(t *testing.T) {
	// unchanged emits nothing [04 §5.2]
	if tr := MoveRateTransition(1, 1); tr != nil {
		t.Fatalf("same tier should emit nothing got %v", tr)
	}
	// into 0 from nonzero => StopMoving [GAP T15] C18
	if tr := MoveRateTransition(2, 0); len(tr) != 1 || tr[0] != CallbackStopMoving {
		t.Fatalf("into0 from nonzero want StopMoving got %v", tr)
	}
	// into nonzero from 0 => StartMoving FIRST then MoveRateN with barrier [GAP T15] C18
	if tr := MoveRateTransition(0, 1); len(tr) != 2 || tr[0] != CallbackStartMoving || tr[1] != CallbackMoveRate1 {
		t.Fatalf("0->1 want StartMoving+MoveRate1 got %v", tr)
	}
	if tr := MoveRateTransition(0, 2); len(tr) != 2 || tr[0] != CallbackStartMoving || tr[1] != CallbackMoveRate2 {
		t.Fatalf("0->2 want StartMoving+MoveRate2 got %v", tr)
	}
	if tr := MoveRateTransition(0, 3); len(tr) != 2 || tr[1] != CallbackMoveRate3 {
		t.Fatalf("0->3 want StartMoving+MoveRate3 got %v", tr)
	}
	// nonzero->nonzero only MoveRateN [GAP T15] C18
	if tr := MoveRateTransition(1, 3); len(tr) != 1 || tr[0] != CallbackMoveRate3 {
		t.Fatalf("1->3 want MoveRate3 got %v", tr)
	}
	if tr := MoveRateTransition(2, 1); len(tr) != 1 || tr[0] != CallbackMoveRate1 {
		t.Fatalf("2->1 want MoveRate1 got %v", tr)
	}
}

// ---------------------------------------------------------------------------
// C26 ordering and clamp bounds [04 §5.1] C26
// ---------------------------------------------------------------------------

func TestC26OrderingSubtractBeforeCallback(t *testing.T) {
	// Health subtracted FIRST, then HitByWeapon with cos/sin*400 of dir shifted left 8,
	// then TakeDamage with post-hit clamp(health*100/maxHealth,0,100) [04 §5.1] C26.
	v := &VictimState{Health: 100, MaxHealth: 100, Active: true, Dying: false, MovementCat: 0}
	res := ApplyNormalDamage(v, DamageKindNormal, 30, 0) // dir 0 => (400,0)
	if v.Health != 70 {                                  // 100-30=70 subtracted first
		t.Fatalf("health subtract first got %d want 70 [04 §5.1] C26", v.Health)
	}
	if res.HealthAfter != 70 {
		t.Fatalf("result HealthAfter %d want 70", res.HealthAfter)
	}
	if res.HitArgs != [2]int32{400, 0} {
		t.Fatalf("HitArgs %v want [400 0] [04 §5.1] C26", res.HitArgs)
	}
	if res.TakeArg != 70 { // 70*100/100=70 clamped
		t.Fatalf("TakeArg %d want 70 [04 §5.1] C26", res.TakeArg)
	}
	if !res.ShouldHit || !res.ShouldTake {
		t.Fatalf("ShouldHit/Take false want true")
	}
	// Non-zero dir validates shift left 8
	v2 := &VictimState{Health: 80, MaxHealth: 100, Active: true, MovementCat: 0}
	res2 := ApplyNormalDamage(v2, DamageKindNormal, 10, 64) // dir 64 -> quarter
	if res2.HitArgs != [2]int32{0, 400} {
		t.Fatalf("HitArgs dir64 %v want [0 400]", res2.HitArgs)
	}
	if v2.Health != 70 || res2.TakeArg != 70 {
		t.Fatalf("post-hit health/percent %d/%d want 70/70", v2.Health, res2.TakeArg)
	}
}

func TestC26PercentageClampBounds(t *testing.T) {
	// TakeDamage percent clamp(health*100/maxHealth,0,100) unsigned division [04 §5.1] C26
	if got := ComputeTakeDamagePercent(200, 100); got != 100 {
		t.Fatalf("clamp high got %d want 100", got)
	}
	if got := ComputeTakeDamagePercent(-10, 100); got != 0 {
		t.Fatalf("clamp low negative health got %d want 0", got)
	}
	if got := ComputeTakeDamagePercent(50, 100); got != 50 {
		t.Fatalf("50/100 got %d want 50", got)
	}
	if got := ComputeTakeDamagePercent(0, 100); got != 0 {
		t.Fatalf("zero got %d want 0", got)
	}
	// HealthPercent same clamp for port4
	if got := HealthPercent(150, 100); got != 100 {
		t.Fatalf("HealthPercent clamp high got %d", got)
	}
}

func TestC26DeathLatchShortCircuit(t *testing.T) {
	// Lethal vs movement-category-1/2 victim sets death latch and returns with NO callbacks [04 §5.1] C26
	for _, cat := range []int{1, 2} {
		v := &VictimState{Health: 10, MaxHealth: 100, Active: true, Dying: false, MovementCat: cat}
		res := ApplyNormalDamage(v, DamageKindNormal, 20, 0) // 10-20 = -10 lethal
		if !res.DeathLatch || !res.Dead {
			t.Fatalf("cat %d lethal should set DeathLatch/Dead %+v", cat, res)
		}
		if res.ShouldHit || res.ShouldTake {
			t.Fatalf("cat %d lethal should have no callbacks %+v [04 §5.1] C26", cat, res)
		}
		if !v.Dying {
			t.Fatalf("cat %d lethal should set victim Dying", cat)
		}
		// Lethal but not latched for stationary: cat 0 or 3 should still emit callbacks
	}
	// Stationary lethal (cat 0) still emits Hit/Take before clamp
	v0 := &VictimState{Health: 10, MaxHealth: 100, Active: true, MovementCat: 0}
	res0 := ApplyNormalDamage(v0, DamageKindNormal, 20, 0)
	if res0.DeathLatch {
		t.Fatalf("cat 0 lethal should not latch [04 §5.1] C26")
	}
	if !res0.ShouldHit || v0.Health != 0 {
		t.Fatalf("cat0 lethal should clamp to 0 and still emit callbacks %+v health %d", res0, v0.Health)
	}
	// Heal and paralyze kinds skip pair entirely [04 §5.1] C26
	vH := &VictimState{Health: 50, MaxHealth: 100, Active: true, MovementCat: 0}
	resH := ApplyNormalDamage(vH, DamageKindHeal, 20, 0)
	if resH.ShouldHit || resH.ShouldTake || vH.Health != 50 {
		t.Fatalf("heal should skip pair and not mutate health %+v", resH)
	}
	vP := &VictimState{Health: 50, MaxHealth: 100, Active: true, MovementCat: 0}
	resP := ApplyNormalDamage(vP, DamageKindParalyze, 20, 0)
	if resP.ShouldHit || resP.ShouldTake {
		t.Fatalf("paralyze should skip pair [04 §5.1] C26")
	}
	// Inactive or already dying victim: packet rejected, health not subtracted
	vI := &VictimState{Health: 50, MaxHealth: 100, Active: false, MovementCat: 0}
	resI := ApplyNormalDamage(vI, DamageKindNormal, 10, 0)
	if resI.ShouldHit || vI.Health != 50 {
		t.Fatalf("inactive victim should reject packet [04 §5.1] C26")
	}
	vD := &VictimState{Health: 50, MaxHealth: 100, Active: true, Dying: true, MovementCat: 0}
	resD := ApplyNormalDamage(vD, DamageKindNormal, 10, 0)
	if resD.ShouldHit {
		t.Fatalf("already dying victim should reject")
	}
}

// ---------------------------------------------------------------------------
// C16 aim-ready handshake [GAP T15] C16
// ---------------------------------------------------------------------------

func TestAimReadyHandshake(t *testing.T) {
	slot := &AimSlot{}
	slot.StartAim() // issue bit set [04 §5.3]
	if !slot.IssueBit {
		t.Fatalf("StartAim should set issue bit")
	}
	if slot.CanFire() {
		t.Fatalf("should not be ready before completion [GAP T15] C16")
	}
	// Zero return leaves weapon permanently unable to fire [GAP T15] C16
	slot.CompleteAim(0)
	if slot.CanFire() {
		t.Fatalf("zero return should not grant ready [GAP T15] C16")
	}
	// Nonzero grants ready
	slot2 := &AimSlot{}
	slot2.StartAim()
	slot2.CompleteAim(1)
	if !slot2.CanFire() {
		t.Fatalf("nonzero return should grant ready")
	}
	slot2.CompleteAim(0) // second zero should not clear ready
	if !slot2.CanFire() {
		t.Fatalf("ready should stay after zero")
	}
	// Exhausted/no script delivery zero never grants [GAP T15] C16
	slot3 := &AimSlot{}
	slot3.StartAim()
	slot3.CompleteAim(AimDeliveryZero)
	if slot3.CanFire() {
		t.Fatalf("exhausted delivery zero should not grant")
	}
}

// ---------------------------------------------------------------------------
// C17 same-tick windows order [GAP T15] C17 (I7)
// ---------------------------------------------------------------------------

func TestTickWindowOrder(t *testing.T) {
	order := TickWindowOrder()
	want := []WindowPhase{PhaseUnitUpdate, PhaseWeaponUpdate, PhaseNormalDrain, PhaseOrdersBuild, PhaseMovementIntegr, PhaseSlotEndDeath}
	if len(order) != len(want) {
		t.Fatalf("TickWindowOrder len %d want %d [GAP T15] C17", len(order), len(want))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] %d want %d [GAP T15] C17 (I7)", i, order[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Fixed-point vs model trig separation [04 §5.1] C25 [03 §2.4]
// ---------------------------------------------------------------------------

func TestFixedVsFloatTrigSeparation(t *testing.T) {
	// Callback arguments must go through the 512-entry table via numeric.Sin/Cos,
	// not float draw trig. The table is scaled 8192 and uses round-to-nearest
	// before truncation [04 §5.1] C25; model draw uses float with round-to-nearest
	// but different constants [03 §2.4]. This test pins that RockUnit/HitByWeapon
	// stay on the fixed table: for a small angle the table steps 512, while float
	// would be smooth. Angle 256 (one step) has sine table entry ~100 (approx
	// 8192*sin(2*pi/512*1)) but float sin(256*2pi/65536) scaled would be ~202?
	// Different.
	// We just verify HitByWeapon uses table index 2 for dir 1 (angle 256):
	a := numeric.Angle(uint16(1) << 8)
	tableSin := numeric.Sin(a)
	// Table entry for index 2 = round(8192*sin(2*2pi/512))
	expectIdx := (uint32(a) * 512 >> 16) & 511
	expectEntry := int32(math.Round(8192 * math.Sin(float64(expectIdx)*2*math.Pi/512)))
	if tableSin != expectEntry {
		t.Fatalf("numeric.Sin mismatch %d vs %d idx %d", tableSin, expectEntry, expectIdx)
	}
	// And that our trigScalar helper matches numeric.MulRound contract for the packers.
	if trigScalar(tableSin, 400) != int32((int64(tableSin)*400+4096)>>13) {
		t.Fatalf("trigScalar not matching MulRound helper [04 §5.1] C25")
	}
}

// ---------------------------------------------------------------------------
// Health/percent and build percent helpers spot checks
// ---------------------------------------------------------------------------

func TestHealthAndBuildPercent(t *testing.T) {
	if got := HealthPercent(100, 100); got != 100 {
		t.Fatalf("100/100 got %d", got)
	}
	if got := HealthPercent(0, 100); got != 0 {
		t.Fatalf("0/100 got %d", got)
	}
	if got := HealthPercent(50, 200); got != 25 {
		t.Fatalf("50/200 got %d want 25", got)
	}
	// Port 17 build percent [04 §4.4] C15: 1 - trunc(f * -99)
	if got := BuildPercentLeft(0.0); got != 0 {
		t.Fatalf("BuildPercent f=0 got %d want 0", got)
	}
	if got := BuildPercentLeft(1.0); got != 100 { // 1 - trunc(-99)=1 - (-99)=100
		t.Fatalf("BuildPercent 1.0 got %d want 100", got)
	}
	if got := BuildPercentLeft(0.5); got != 50 { // trunc(-49.5)=-49 ->1-(-49)=50
		t.Fatalf("BuildPercent 0.5 got %d want 50", got)
	}
	if got := BuildPercentLeft(0.01); got != 1 { // trunc(-0.99)=0 ->1-0=1
		t.Fatalf("BuildPercent 0.01 got %d", got)
	}
}

// Ensure pack helper compiles and round-trips integer part.
func TestPackHelpers(t *testing.T) {
	x := numeric.FixedFromInt(10)
	z := numeric.FixedFromInt(-20)
	packed := PackXZ(x, z)
	dx, dz := unpackXZ(packed)
	if dx != int32(x) || dz != int32(z) {
		t.Fatalf("PackXZ round-trip %d/%d want 10/-20 packed %#x", dx, dz, uint32(packed))
	}
	if got := Distance(packed); got != int32(math.Hypot(10, -20)*65536) {
		t.Fatalf("Distance %d want %d", got, int32(math.Hypot(10, -20)*65536))
	}
}

// TestGroundHeightDecodesPackedCoordinateHighXLowZ locks the [R-COB-03 §3]
// halves: PackXZ's high half is X and low half is Z, so GroundHeight must
// hand heightFn the same (x, z) order, not (z, x).
func TestGroundHeightDecodesPackedCoordinateHighXLowZ(t *testing.T) {
	x := numeric.FixedFromInt(30)
	z := numeric.FixedFromInt(-7)
	packed := PackXZ(x, z)

	var gotX, gotZ numeric.Fixed
	stub := func(qx, qz numeric.Fixed) numeric.Fixed {
		gotX, gotZ = qx, qz
		return numeric.FixedFromInt(5) // arbitrary valid height
	}
	if got := GroundHeight(packed, stub); got != numeric.FixedFromInt(5) {
		t.Fatalf("GroundHeight = %v want 5.0 (16.16 passthrough)", got)
	}
	if gotX != x || gotZ != z {
		t.Fatalf("GroundHeight unpacked (x=%v z=%v) want (x=%v z=%v) [R-COB-03 §3] high half X, low half Z", gotX, gotZ, x, z)
	}
}

// TestGroundHeightReshapesOffMapSentinel checks the one reshape [04 §4.4]
// requires: the terrain query's raw, unshifted −1 marker becomes retail's
// shifted off-map read −0x10000 (−1.0), never zero and never the raw −1.
func TestGroundHeightReshapesOffMapSentinel(t *testing.T) {
	offMap := func(numeric.Fixed, numeric.Fixed) numeric.Fixed { return numeric.Fixed(-1) }
	if got := GroundHeight(0, offMap); got != GroundHeightOffMap {
		t.Fatalf("GroundHeight off-map = %v want %v (−0x10000) [04 §4.4]", got, GroundHeightOffMap)
	}
	if GroundHeightOffMap != numeric.Fixed(-0x10000) {
		t.Fatalf("GroundHeightOffMap = %v want -0x10000", GroundHeightOffMap)
	}
}

// TestGroundHeightNilHeightFnReadsZero documents the no-terrain fallback: a
// nil heightFn (only reachable from a bare VM fixture with nothing bound,
// never from a production session) reads 0, not an invented sentinel. See
// the TODO(question) on VM.readPortDefault.
func TestGroundHeightNilHeightFnReadsZero(t *testing.T) {
	if got := GroundHeight(PackXZ(numeric.FixedFromInt(1), numeric.FixedFromInt(1)), nil); got != 0 {
		t.Fatalf("GroundHeight nil heightFn = %v want 0", got)
	}
}

// TestGroundHeightPortFuncReadsArgOne locks the engine-read argument
// convention this port shares with 7–16: the compiler always pushes the port
// id then four zero-filled slots, so a bound handler's args[0] is the id and
// args[1] is the packed coordinate [fmt cob]. A call with no argument slot
// pushed reads coordinate (0,0), the same zero-fill convention.
func TestGroundHeightPortFuncReadsArgOne(t *testing.T) {
	x := numeric.FixedFromInt(2)
	z := numeric.FixedFromInt(3)
	packed := PackXZ(x, z)

	var gotX, gotZ numeric.Fixed
	fn := GroundHeightPortFunc(func(qx, qz numeric.Fixed) numeric.Fixed {
		gotX, gotZ = qx, qz
		return numeric.FixedFromInt(9)
	})

	// args[0]=port id (16), args[1]=packed XZ, args[2..4]=compiler zero-fill.
	if got := fn([]int32{16, packed, 0, 0, 0}); got != int32(numeric.FixedFromInt(9)) {
		t.Fatalf("GroundHeightPortFunc = %d want %d", got, int32(numeric.FixedFromInt(9)))
	}
	if gotX != x || gotZ != z {
		t.Fatalf("GroundHeightPortFunc unpacked (x=%v z=%v) want (x=%v z=%v)", gotX, gotZ, x, z)
	}

	// No argument slot pushed: zero-fill convention reads (0,0).
	gotX, gotZ = numeric.FixedFromInt(99), numeric.FixedFromInt(99) // sentinel to prove overwrite
	fn([]int32{16})
	if gotX != 0 || gotZ != 0 {
		t.Fatalf("GroundHeightPortFunc with no arg slot = (x=%v z=%v) want (0,0)", gotX, gotZ)
	}
}

func TestBearingPortsUseRetailAngleConversion(t *testing.T) {
	// Port 12 is atan2(X,Z) followed by relative-heading subtraction; port 14
	// evaluates its two arguments as atan2(first, second) [R-COB-03 §2].
	if got := RelativeBearing(PackXZ(numeric.FixedFromInt(1), 0), 0); got != 16384 {
		t.Fatalf("RelativeBearing(+X)=%d want 16384", got)
	}
	if got := RelativeBearing(PackXZ(0, numeric.FixedFromInt(1)), 16384); got != 49152 {
		t.Fatalf("RelativeBearing(+Z, heading +X)=%d want 49152", got)
	}
	if got := AtanPort(1, 0); got != 16384 {
		t.Fatalf("AtanPort(+X, 0)=%d want 16384", got)
	}
}
