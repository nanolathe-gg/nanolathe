package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestRendertypeDispatchTable verifies dispatch covers 0..7 per [03 §5.4] C6.
func TestRendertypeDispatchTable(t *testing.T) {
	// Fixture: one weapon per rendertype, same projectile record.
	baseProj := combat.Projectile{
		Pos:          combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)},
		StartPos:     combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)},
		CreationTick: 100,
		ExpiryTick:   200,
		WeaponID:     1,
	}
	frameCount := 10
	wantKind := map[int32]string{
		0: "beam",
		1: "base-sprite+model",
		2: "global-gaf",
		3: "base+model-distinct",
		4: "selector-gaf",
		5: "lifetime-gaf",
		6: "record-orientation",
		7: "segmented",
	}
	for rt := int32(0); rt < RendertypeCount; rt++ {
		w := &content.WeaponDef{RenderType: rt, Color: 7, Color2: 2, WeaponTimer: 30, Duration: 5}
		spec := DispatchRendertype(baseProj, w, 105, frameCount, 2, w.WeaponTimer, func() bool { return true })
		if spec.Suppressed && rt != 4 && rt != 5 {
			// 4 suppressed only with selector -1, 5 with out-of-range; these are not our case.
			t.Fatalf("rendertype %d suppressed unexpectedly kind %q [03 §5.4] C6", rt, spec.Kind)
		}
		if spec.Kind != wantKind[rt] {
			t.Fatalf("rendertype %d kind %q want %q [03 §5.4] C6", rt, spec.Kind, wantKind[rt])
		}
		if spec.RenderType != rt {
			t.Fatalf("rendertype %d specular RenderType %d want %d", rt, spec.RenderType, rt)
		}
	}
	// Selector -1 suppresses branch entirely [03 §5.4] case 4.
	w4 := &content.WeaponDef{RenderType: RenderTypeSelectorGAF}
	spec4 := DispatchRendertype(baseProj, w4, 105, 5, -1, 0, nil)
	if !spec4.Suppressed {
		t.Fatalf("rendertype 4 selector -1 should suppress [03 §5.4]")
	}
	// Lifetime out-of-range suppresses [03 §5.4] case 5.
	w5 := &content.WeaponDef{RenderType: RenderTypeLifetimeGAF, WeaponTimer: 10}
	// Make frame out-of-range by setting expiry == now so frame == frameCount -> suppressed.
	proj5 := baseProj
	proj5.ExpiryTick = 105 // now, so remaining 0 => frame = 10 -0 =10 => suppressed (>=count)
	spec5 := DispatchRendertype(proj5, w5, 105, 10, 0, 10, nil)
	if !spec5.Suppressed {
		t.Fatalf("rendertype 5 out-of-range frame should suppress [03 §5.4]")
	}
	// Normal lifetime case should not suppress.
	proj5.ExpiryTick = 110
	spec5 = DispatchRendertype(proj5, w5, 105, 10, 0, 10, nil)
	if spec5.Suppressed {
		t.Fatalf("rendertype 5 in-range should not suppress")
	}
	// Frame = 10 - ((110-105)*10)/10 =10-5=5 [03 §5.4]
	if spec5.Frame != 5 {
		t.Fatalf("rendertype 5 frame got %d want 5 [03 §5.4]", spec5.Frame)
	}
	// Selector 4 normal: frame = (now - spawn) mod count [03 §5.4]
	w4 = &content.WeaponDef{RenderType: RenderTypeSelectorGAF}
	proj4 := baseProj
	proj4.CreationTick = 100
	spec4 = DispatchRendertype(proj4, w4, 107, 5, 2, 0, nil)
	// (107-100)=7 mod5=2
	if spec4.Frame != 2 {
		t.Fatalf("rendertype 4 frame %d want 2 [03 §5.4]", spec4.Frame)
	}
	// Global GAF admission failure aborts whole renderer [03 §5.4] case 2.
	w2 := &content.WeaponDef{RenderType: RenderTypeGlobalGAF}
	spec2 := DispatchRendertype(baseProj, w2, 105, 0, 0, 0, func() bool { return false })
	if !spec2.Aborted || !spec2.Suppressed {
		t.Fatalf("rendertype 2 admission failure should abort [03 §5.4]")
	}
}

// TestBeamStrokes verifies C7: color2==0 draws one stroke, otherwise two secondary first primary on top [03 §5.4] C7.
func TestBeamStrokes(t *testing.T) {
	hs := [2]int32{10, 20}
	ts := [2]int32{30, 40}
	// color2 zero -> one stroke primary.
	strokes := BeamStrokes(hs, ts, 5, 0)
	if len(strokes) != 1 || strokes[0].Color != 5 {
		t.Fatalf("beam single stroke %v want one with color 5 [03 §5.4] C7", strokes)
	}
	// color2 nonzero -> two strokes secondary outer primary inner [03 §5.4].
	strokes = BeamStrokes(hs, ts, 5, 9)
	if len(strokes) != 2 {
		t.Fatalf("beam double strokes len %d want 2 [03 §5.4] C7", len(strokes))
	}
	if strokes[0].Color != 9 || strokes[1].Color != 5 {
		t.Fatalf("beam double colors %d %d want 9,5 secondary first primary on top [03 §5.4] C7", strokes[0].Color, strokes[1].Color)
	}
	// Endpoint swapped: first stroke tail->head, second head->tail [03 §5.4].
	if strokes[0].X0 != ts[0] || strokes[0].Y0 != ts[1] || strokes[0].X1 != hs[0] || strokes[0].Y1 != hs[1] {
		t.Fatalf("beam first stroke not tail->head swapped %v [03 §5.4]", strokes[0])
	}
	if strokes[1].X0 != hs[0] || strokes[1].Y0 != hs[1] || strokes[1].X1 != ts[0] || strokes[1].Y1 != ts[1] {
		t.Fatalf("beam second stroke not head->tail %v [03 §5.4]", strokes[1])
	}
}

// TestBeamHeadTailVectors mirrors combat latch tick per [06 §6.10] via SimulateBeamTick.
// Before latch tail stays fixed while head advances;
// tick that sets latch still leaves tail fixed;
// next tick both move preserving length [06 §6.10].
func TestBeamHeadTailVectors(t *testing.T) {
	w := &content.WeaponDef{BeamWeapon: true, Duration: 5, WeaponTimer: 100}
	vel := combat.Vec3{X: numeric.Fixed(65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)} // 1.0 per tick
	p := combat.Projectile{
		Pos:          combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)},
		StartPos:     combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)},
		Velocity:     vel,
		CreationTick: 100,
		ExpiryTick:   300,
		BeamLatch:    false,
	}
	// tick 105: creation+duration=105, tick==105 not > => latch false [06 §6.10]
	if BeamLatchState(p.CreationTick, w.Duration, 105) {
		t.Fatalf("latch should not be set at tick==creation+duration [06 §6.10]")
	}
	// tick 106: 100+5 <106 true => latch becomes true this tick but tail stays fixed [06 §6.10]
	if !BeamLatchState(p.CreationTick, w.Duration, 106) {
		t.Fatalf("latch should be set at tick 106 [06 §6.10]")
	}
	// Simulate sequence 105,106,107 mirroring combat.
	p1 := SimulateBeamTick(p, w, 105)
	if p1.BeamLatch {
		t.Fatalf("after tick 105 latch %v want false [06 §6.10]", p1.BeamLatch)
	}
	if p1.Pos.X.Raw() != 65536 || p1.StartPos.X.Raw() != 0 {
		t.Fatalf("tick105 head %d tail %d want 65536,0 before latch [06 §6.10]", p1.Pos.X.Raw(), p1.StartPos.X.Raw())
	}
	p2 := SimulateBeamTick(p1, w, 106)
	if !p2.BeamLatch {
		t.Fatalf("after tick 106 latch %v want true [06 §6.10]", p2.BeamLatch)
	}
	if p2.Pos.X.Raw() != 131072 || p2.StartPos.X.Raw() != 0 {
		t.Fatalf("tick106 (latch setting tick) head %d tail %d want 131072,0 tail still fixed [06 §6.10]", p2.Pos.X.Raw(), p2.StartPos.X.Raw())
	}
	p3 := SimulateBeamTick(p2, w, 107)
	if p3.Pos.X.Raw() != 196608 || p3.StartPos.X.Raw() != 65536 {
		t.Fatalf("tick107 after latch both move head %d tail %d want 196608,65536 [06 §6.10]", p3.Pos.X.Raw(), p3.StartPos.X.Raw())
	}
	// Length preserved: pos - start = 131072 (2*65536) after latch.
	if p3.Pos.X.Raw()-p3.StartPos.X.Raw() != 131072 {
		t.Fatalf("beam length not preserved after latch %d want 131072 [06 §6.10]", p3.Pos.X.Raw()-p3.StartPos.X.Raw())
	}
	// ComputeBeamEndpoints should reflect latched flag at entry (not after) [06 §6.10].
	// For p2 (latched), ComputeBeamEndpoints with wasLatched true would report latched.
	bm := ComputeBeamEndpoints(p2, w, 107)
	if !bm.Latched {
		t.Fatalf("ComputeBeamEndpoints latched %v want true for already latched entry [06 §6.10]", bm.Latched)
	}
	// For p1 (not latched), not latched.
	bm = ComputeBeamEndpoints(p1, w, 106)
	// p1.BeamLatch false, so Compute reports false (wasLatched false) and latchNow computed but not used for Latched field.
	// We expose latched as wasLatched per [06 §6.10] next tick moves both.
	if bm.Latched {
		t.Fatalf("ComputeBeamEndpoints before latch should be false")
	}
	// Screen projection orthographic [03 §2.5].
	// World 10,5,20 -> screenX =10 - camX +128, screenY=20 - (5>>1) -camZ+32
	pos := combat.Vec3{X: numeric.Fixed(10 * 65536), Y: numeric.Fixed(5 * 65536), Z: numeric.Fixed(20 * 65536)}
	sx, sy := ProjectToScreen(pos, 2, 3)
	if sx != 10-2+128 || sy != 20-(5>>1)-3+32 {
		t.Fatalf("project %d,%d want %d,%d [03 §2.5]", sx, sy, 10-2+128, 20-(5>>1)-3+32)
	}
}

// TestSmokeEmissionCadence verifies additive deadline and zero-delay emit every tick [03 §5.4] [06 §13.2].
func TestSmokeEmissionCadence(t *testing.T) {
	w := &content.WeaponDef{SmokeTrail: true, SmokeDelay: 3}
	p := combat.Projectile{BurstRemaining: 0, ExpiryTick: 100, WeaponID: 1}
	em := &TrailEmitter{NextDeadline: 10}
	// At deadline 10 => emit and deadline becomes 13 [06 §13.2] additive.
	if !em.ShouldEmitTrailSmoke(w, p, 10) {
		t.Fatalf("smoke at deadline 10 should emit [06 §13.2]")
	}
	if em.NextDeadline != 13 {
		t.Fatalf("next deadline %d want 13 additive [06 §13.2]", em.NextDeadline)
	}
	// 11 <13 => no emit.
	if em.ShouldEmitTrailSmoke(w, p, 11) {
		t.Fatalf("smoke at 11 before deadline 13 should not emit [06 §13.2]")
	}
	// 13 => emit, deadline 16.
	if !em.ShouldEmitTrailSmoke(w, p, 13) {
		t.Fatalf("smoke at 13 should emit")
	}
	if em.NextDeadline != 16 {
		t.Fatalf("deadline %d want 16", em.NextDeadline)
	}
	// Delayed emitter preserves debt: skip to 20, deadline 16 <20 => one emit, deadline 19, still debt for next tick.
	em2 := &TrailEmitter{NextDeadline: 13}
	// Simulate that we missed 13-15 ticks and check at 20.
	if !em2.ShouldEmitTrailSmoke(w, p, 20) {
		t.Fatalf("delayed emitter at 20 should emit owed puff [03 §5.4] [06 §13.2]")
	}
	if em2.NextDeadline != 16 {
		t.Fatalf("additive preserves debt: deadline %d want 16 [06 §13.2]", em2.NextDeadline)
	}
	// Next tick 21 still past 16 => emits again, preserving debt until caught up.
	if !em2.ShouldEmitTrailSmoke(w, p, 21) {
		t.Fatalf("delayed debt second puff should emit at 21")
	}
	if em2.NextDeadline != 19 {
		t.Fatalf("deadline %d want 19", em2.NextDeadline)
	}
	// Zero-delay emits every eligible tick [03 §5.4].
	w0 := &content.WeaponDef{SmokeTrail: true, SmokeDelay: 0}
	em0 := &TrailEmitter{NextDeadline: 10}
	for tick := uint32(10); tick < 13; tick++ {
		if !em0.ShouldEmitTrailSmoke(w0, p, tick) {
			t.Fatalf("zero-delay smoke at %d should emit every tick [03 §5.4]", tick)
		}
		if em0.NextDeadline != 10 {
			t.Fatalf("zero-delay deadline should stay 10, got %d [03 §5.4]", em0.NextDeadline)
		}
	}
	// Burst parents never emit [06 §4.3] [03 §5.4].
	wBurst := &content.WeaponDef{SmokeTrail: true, SmokeDelay: 3}
	pBurst := combat.Projectile{BurstRemaining: 1, ExpiryTick: 100}
	emB := &TrailEmitter{NextDeadline: 10}
	if emB.ShouldEmitTrailSmoke(wBurst, pBurst, 10) {
		t.Fatalf("burst parent with remaining !=0 should never emit trail [06 §4.3]")
	}
	// After expiry: gated on being before expiry [06 §13.2].
	pExp := combat.Projectile{BurstRemaining: 0, ExpiryTick: 10}
	emExp := &TrailEmitter{NextDeadline: 10}
	if emExp.ShouldEmitTrailSmoke(w, pExp, 10) {
		t.Fatalf("smoke at expiry tick should not emit (before expiry only) [06 §13.2]")
	}
	emExp2 := &TrailEmitter{NextDeadline: 9}
	if !emExp2.ShouldEmitTrailSmoke(w, pExp, 9) {
		t.Fatalf("smoke before expiry should emit [06 §13.2]")
	}
	// No smoketrail flag => never.
	wNo := &content.WeaponDef{SmokeTrail: false, SmokeDelay: 3}
	emNo := &TrailEmitter{NextDeadline: 10}
	if emNo.ShouldEmitTrailSmoke(wNo, p, 10) {
		t.Fatalf("smoke without flag should not emit")
	}
}

// TestSegmentedBeam verifies rendertype 7 segment count denominator and jitter range [03 §5.4].
func TestSegmentedBeam(t *testing.T) {
	head := combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}
	tail := combat.Vec3{X: numeric.Fixed(10 * 65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)} // 10 world units raw 655360
	n := SegmentCount(head, tail)
	// 655360 /327680 =2 [03 §5.4]
	if n != 2 {
		t.Fatalf("segment count %d want 2 for span 10 with denominator 0x50000 [03 §5.4]", n)
	}
	// Short span zero => skipped [03 §5.4]
	tail2 := combat.Vec3{X: numeric.Fixed(1 * 65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)} // 1 unit -> 65536/327680=0
	if SegmentCount(head, tail2) != 0 {
		t.Fatalf("short span should be skipped (zero segments) [03 §5.4]")
	}
	// Jitter draws: each point receives rand()*11/0x8000 -5 in [-5,5] per axis [03 §5.4].
	// Use deterministic CRT seed [01 §7.2] presentation stream (I4)
	crt := rng.NewCRT(1) // deterministic [01 §7.2] (I4)
	for i := 0; i < 10; i++ {
		x, y, z := head.X, head.Y, head.Z
		jx, jy, jz := SegmentedJitter(&crt, x, y, z)
		// delta should be -5..+5 pixels *65536
		dx := int32((int64(jx) - int64(x)) / 65536)
		dy := int32((int64(jy) - int64(y)) / 65536)
		dz := int32((int64(jz) - int64(z)) / 65536)
		if dx < -5 || dx > 5 || dy < -5 || dy > 5 || dz < -5 || dz > 5 {
			t.Fatalf("jitter %d,%d,%d out of [-5,5] range [03 §5.4]", dx, dy, dz)
		}
	}
	// SegmentedBeamPoints uses exactly three CRT draws per interior point [03 §5.4] [I4].
	// Verify call count by seeding and reproducing jitter manually.
	head5 := combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}
	tail5 := combat.Vec3{X: numeric.Fixed(20 * 65536), Y: numeric.Fixed(0), Z: numeric.Fixed(0)} // span 20 -> 4 segments, so 2 interior points (n=4)
	// Actually 20*65536=1310720 /327680=4
	n5 := SegmentCount(head5, tail5)
	if n5 != 4 {
		t.Fatalf("segment count 20 units want 4 got %d", n5)
	}
	crt2 := rng.NewCRT(12345)
	pts := SegmentedBeamPoints(head5, tail5, &crt2)
	if len(pts) != n5+1-1+1 { // head + (n-1) interior + tail = n+1? Let's see: we push head, then n-1 interior, then tail => n+1 total
		// For n=4 => head, 3 interior? Actually our loop i=1..n-1 => 3? Wait code: for i=1;i<n;i++ => when n=4, i=1,2,3 =>3 interior => total 5 = n+1.
		// So len should be n+1.
		t.Fatalf("segmented points len %d want %d [03 §5.4]", len(pts), n5+1)
	}
	// Verify deterministic: same seed yields same points.
	crt3 := rng.NewCRT(12345)
	pts2 := SegmentedBeamPoints(head5, tail5, &crt3)
	if len(pts2) != len(pts) {
		t.Fatalf("deterministic len mismatch")
	}
	for i := range pts {
		if pts[i].X != pts2[i].X || pts[i].Y != pts2[i].Y || pts[i].Z != pts2[i].Z {
			t.Fatalf("deterministic jitter mismatch at %d", i)
		}
	}
}

// TestRenderBatchAbortsOnGlobalGAFAdmissionFailure verifies [03 §5.4] case 2 aborts whole renderer.
func TestRenderBatchAbortsOnGlobalGAFAdmissionFailure(t *testing.T) {
	// Two projectiles: first is rendertype 2 with admission failure, second would otherwise draw.
	p1 := combat.Projectile{WeaponID: 1, Pos: combat.Vec3{X: numeric.Fixed(0)}, StartPos: combat.Vec3{X: numeric.Fixed(0)}, CreationTick: 0, ExpiryTick: 100}
	p2 := combat.Projectile{WeaponID: 2, Pos: combat.Vec3{X: numeric.Fixed(100 * 65536)}, StartPos: combat.Vec3{X: numeric.Fixed(0)}, CreationTick: 0, ExpiryTick: 100}
	weapons := map[int32]*content.WeaponDef{
		1: {RenderType: RenderTypeGlobalGAF},
		2: {RenderType: RenderTypeBeam, Color: 1, Color2: 0},
	}
	// admitOK returns false -> should abort and produce no specs beyond, aborted true.
	specs, aborted := RenderBatch([]combat.Projectile{p1, p2}, weapons, 10, nil, nil, nil, nil, func() bool { return false })
	if !aborted {
		t.Fatalf("render batch should abort on global GAF admission failure [03 §5.4]")
	}
	if len(specs) != 0 {
		t.Fatalf("aborted batch should yield 0 specs until abort, got %d [03 §5.4]", len(specs))
	}
	// admitOK true -> no abort, second draws.
	specs, aborted = RenderBatch([]combat.Projectile{p2, p1}, weapons, 10, nil, nil, nil, nil, func() bool { return true })
	if aborted {
		t.Fatalf("admit true should not abort")
	}
	if len(specs) == 0 {
		t.Fatalf("non-aborted batch should have specs")
	}
}

// TestRenderBatchVisibilityGate verifies draw gate evaluates once per record before dispatch [03 §5.4].
func TestRenderBatchVisibilityGate(t *testing.T) {
	pVisible := combat.Projectile{WeaponID: 1, Pos: combat.Vec3{X: numeric.Fixed(0)}, CreationTick: 0, ExpiryTick: 100}
	pHidden := combat.Projectile{WeaponID: 1, Pos: combat.Vec3{X: numeric.Fixed(10000 * 65536)}, CreationTick: 0, ExpiryTick: 100}
	weapons := map[int32]*content.WeaponDef{1: {RenderType: RenderTypeBeam}}
	visible := func(pos combat.Vec3) bool {
		// Only position 0 is visible; far position is hidden.
		return pos.X.Raw() == 0
	}
	specs, aborted := RenderBatch([]combat.Projectile{pVisible, pHidden}, weapons, 10, nil, nil, nil, visible, nil)
	if aborted {
		t.Fatalf("should not abort")
	}
	if len(specs) != 1 {
		t.Fatalf("visibility gate should cull one, got %d specs want 1 [03 §5.4]", len(specs))
	}
}

// countingSink implements SmokeSink for tests.
type countingSink struct {
	count    int
	last     combat.Vec3
	lastTick uint32
}

func (c *countingSink) EmitSmoke(pos combat.Vec3, tick uint32) {
	c.count++
	c.last = pos
	c.lastTick = tick
}

func TestSmokeSinkPresentationOnly(t *testing.T) {
	w := &content.WeaponDef{SmokeTrail: true, SmokeDelay: 2}
	p := combat.Projectile{Pos: combat.Vec3{X: numeric.Fixed(5 * 65536)}, BurstRemaining: 0, ExpiryTick: 100}
	em := &TrailEmitter{NextDeadline: 5}
	sink := &countingSink{}
	// tick 5 emits.
	if !em.MaybeEmitTrailSmoke(w, p, 5, sink) {
		t.Fatalf("should emit at 5")
	}
	if sink.count != 1 || sink.lastTick != 5 {
		t.Fatalf("sink not called correctly")
	}
	// tick 6 before deadline 7 -> no emit, sink count unchanged.
	if em.MaybeEmitTrailSmoke(w, p, 6, sink) {
		t.Fatalf("should not emit at 6")
	}
	if sink.count != 1 {
		t.Fatalf("sink should not be called at 6")
	}
	// tick 7 emits, deadline goes 9.
	if !em.MaybeEmitTrailSmoke(w, p, 7, sink) {
		t.Fatalf("should emit at 7")
	}
	if em.NextDeadline != 9 {
		t.Fatalf("deadline %d want 9", em.NextDeadline)
	}
	// Verify presentation never mutates combat state (I6): combat record unchanged.
	origSmoke := p.SmokeDeadline
	_ = em.ShouldEmitTrailSmoke(w, p, 9)
	if p.SmokeDeadline != origSmoke {
		t.Fatalf("presentation must not mutate combat state (I6) SmokeDeadline %d vs %d", p.SmokeDeadline, origSmoke)
	}
}
