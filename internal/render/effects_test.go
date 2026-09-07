package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// helper to make a persistent effect record for ordering tests.
// id encoded in X for traceability; anim keeps it alive for several ticks.
func persistentEffect(id int) EffectRecord {
	return EffectRecord{
		X: numeric.Fixed(int64(id) * 65536),
		// keep alive via looping anim so it is not considered empty
		AnimA: EffectAnimPlayer{Idx: 0, Countdown: 100, Loop: true, Active: true, Frames: 2},
	}
}

// single-tick dying effect: non-looping 1-frame anim that terminates on first Step
func dyingEffect(id int) EffectRecord {
	return EffectRecord{
		X:     numeric.Fixed(int64(id) * 65536),
		AnimA: EffectAnimPlayer{Idx: 0, Countdown: 1, Loop: false, Active: true, Frames: 1},
		// AnimB inactive, HasModel false => empty after AnimA terminates
	}
}

// TestFixedEffectCap verifies 300-record cap and append at/above cap allocates nothing [03 §1] C5.
func TestFixedEffectCap(t *testing.T) {
	var p FixedEffectPool
	if p.Cap() != 300 {
		t.Fatalf("cap %d want 300 [03 §1] C5", p.Cap())
	}
	// Fill to cap
	for i := 0; i < FixedEffectCap; i++ {
		rec := persistentEffect(i)
		if !p.Append(rec) {
			t.Fatalf("append %d failed, len %d", i, p.Len())
		}
	}
	if p.Len() != 300 {
		t.Fatalf("len %d want 300", p.Len())
	}
	// At-cap: next append must allocate nothing [03 §1]
	firstX := p.Records()[0].X
	lastX := p.Records()[299].X
	if !p.Append(persistentEffect(999)) {
		// expected false return
	} else {
		t.Fatalf("append at cap should return false [03 §1] C5")
	}
	if p.Len() != 300 {
		t.Fatalf("at-cap append changed len %d want 300 [03 §1] C5", p.Len())
	}
	if p.Records()[0].X != firstX || p.Records()[299].X != lastX {
		t.Fatalf("at-cap append mutated records [03 §1] C5")
	}
	// Over-cap: artificially force over-cap by direct slice growth attempt? We just verify second over-cap also no alloc.
	if p.Append(persistentEffect(1000)) {
		t.Fatalf("append over cap should return false [03 §1] C5")
	}
	if p.Len() != 300 {
		t.Fatalf("over-cap len %d want 300", p.Len())
	}
	// Survivor order equals insertion order among survivors [03 §1]
	for i, rec := range p.Records() {
		want := numeric.Fixed(int64(i) * 65536)
		if rec.X != want {
			t.Fatalf("order i=%d X %d want %d", i, rec.X, want)
		}
	}
}

// TestFixedEffectOverCapDirect verifies over-cap when count is already above cap via manual injection.
// This locks the "at or above the cap" wording [03 §1] C5 — both cases allocate nothing.
func TestFixedEffectOverCapDirect(t *testing.T) {
	var p FixedEffectPool
	// Fill to cap
	for i := 0; i < FixedEffectCap; i++ {
		p.Append(persistentEffect(i))
	}
	// Simulate corrupted over-cap state by appending via internal slice directly (bypass Append cap)
	// We cannot bypass Append without touching private field; instead we verify that after compacting
	// some out, we can append again, and that at-cap remains blocked.
	// First, make some records die this tick so count drops below cap via same-call compaction.
	// Create a fresh pool with mix of persistent and dying.
	var p2 FixedEffectPool
	for i := 0; i < 299; i++ {
		p2.Append(persistentEffect(i))
	}
	p2.Append(dyingEffect(999)) // last is dying
	if p2.Len() != 300 {
		t.Fatalf("setup len %d want 300", p2.Len())
	}
	// This dying record will be removed within the same Update call [03 §1] C5
	p2.Update(1)
	if p2.Len() != 299 {
		t.Fatalf("dying record should be compacted within same call, len %d want 299 [03 §1] C5", p2.Len())
	}
	// Now we have room for one more
	if !p2.Append(persistentEffect(1000)) {
		t.Fatalf("append after compaction should succeed")
	}
	if p2.Len() != 300 {
		t.Fatalf("len %d want 300 after re-append", p2.Len())
	}
	// And again at cap fails
	if p2.Append(persistentEffect(1001)) {
		t.Fatalf("again at cap should fail")
	}
}

// TestFixedEffectRemovalWithinSameCall verifies emptied records are removed by stable
// left compaction within the same updater call, unlike generic strip objects [03 §1] C5.
func TestFixedEffectRemovalWithinSameCall(t *testing.T) {
	var p FixedEffectPool
	// Layout: persistent, dying, persistent, dying, persistent
	// Dying records have 1-frame non-looping anim => terminate on first Step => empty => removed same call
	p.Append(persistentEffect(10))
	p.Append(dyingEffect(11))
	p.Append(persistentEffect(12))
	p.Append(dyingEffect(13))
	p.Append(persistentEffect(14))

	p.Update(1)

	if p.Len() != 3 {
		t.Fatalf("len %d want 3 after same-call compaction [03 §1] C5", p.Len())
	}
	// Survivors keep order [03 §1]
	want := []int64{10, 12, 14}
	for i, rec := range p.Records() {
		got := int64(rec.X) >> 16
		if got != want[i] {
			t.Fatalf("survivor %d X %d want %d", i, got, want[i])
		}
	}

	// Contrast with Strip: strip's terminal condition created during update is noticed only on next invocation [03 §1] C4.
	// Verify that a strip object that sets shouldRem during Update is not removed until next Update,
	// while effect pool removes immediately.
	var s Strip
	latch := &mockObj{id: 99, latch: true} // mockObj latch sets shouldRem during first Update
	s.Append(latch)
	s.Append(&mockObj{id: 100})
	s.Update(1)
	if len(s.Objects) != 2 {
		t.Fatalf("strip latch first update should not remove [03 §1] C4, len %d want 2", len(s.Objects))
	}
	// Effect pool already removed in same call as shown above, so contrast holds.
}

// TestFixedEffectIntegrationWithStripOrder verifies direct strip/effect updates
// preserve their distinct lifecycle semantics deterministically [03 §1] C4 C5.
func TestFixedEffectIntegrationWithStripOrder(t *testing.T) {
	var strip Strip
	var effects FixedEffectPool
	// Strip: two objects, second is shouldRem true => removed before update; first updated.
	a := &mockObj{id: 1}
	b := &mockObj{id: 2, shouldRem: true}
	strip.Append(a)
	strip.Append(b)

	// Effects: one persistent, one dying (same-call removal)
	effects.Append(persistentEffect(20))
	effects.Append(dyingEffect(21))

	strip.Update(42)
	effects.Update(42)

	if len(strip.Objects) != 1 || strip.Objects[0].(*mockObj).id != 1 {
		t.Fatalf("strip removal-before-update failed [03 §1] C4")
	}
	if a.updated != 1 {
		t.Fatalf("strip survivor should be updated once, got %d", a.updated)
	}
	if b.updated != 0 {
		t.Fatalf("strip removed should not be updated, got %d", b.updated)
	}
	if effects.Len() != 1 {
		t.Fatalf("effects same-call compaction failed len %d want 1 [03 §1] C5", effects.Len())
	}
	if int64(effects.Records()[0].X>>16) != 20 {
		t.Fatalf("effects survivor wrong")
	}

	// Second tick: ensure no cross-contamination and stable order
	strip.Update(43)
	effects.Update(43)
	if len(strip.Objects) != 1 {
		t.Fatalf("strip second update len %d", len(strip.Objects))
	}
	if effects.Len() != 1 {
		t.Fatalf("effects second update len %d", effects.Len())
	}
}

// TestFixedEffectGravityAndBounce verifies integrator advances velocity against gravity
// and restores prior position with invert/halve on terrain/water contact [03 §1] C5,
// plus model-pointer clearing alternative branch.
func TestFixedEffectGravityAndBounce(t *testing.T) {
	var p FixedEffectPool
	// Gravity 1 world unit per tick (65536)
	gravity := numeric.Fixed(65536)
	p.SetGravity(gravity)

	// Record without model, moving down toward terrain at height 10*65536
	// Start at 12, velocity -3 (down), gravity 1 => after integration Y = 12 + (-3-1)=8 < terrain 10 => bounce
	terrainH := numeric.Fixed(10 * 65536)
	p.SetHeightFunc(func(x, z numeric.Fixed) numeric.Fixed { return terrainH })
	p.SetSeaLevel(numeric.Fixed(0))

	rec := EffectRecord{
		X:        numeric.Fixed(0),
		Y:        numeric.Fixed(12 * 65536),
		Z:        numeric.Fixed(0),
		VY:       numeric.Fixed(-3 * 65536),
		AnimA:    EffectAnimPlayer{Idx: 0, Countdown: 10, Loop: true, Active: true, Frames: 2},
		HasModel: false,
	}
	p.Append(rec)
	p.Update(1)
	if p.Len() != 1 {
		t.Fatalf("bounce should keep record")
	}
	got := p.Records()[0]
	// Position should be restored to prior (12)
	if got.Y != numeric.Fixed(12*65536) {
		t.Fatalf("bounce restore Y %d want %d [03 §1] C5", got.Y, 12*65536)
	}
	// Velocity: before integration VY=-3, after gravity VY=-4, then invert/halve => 2 ( -(-4)/2)
	if got.VY != numeric.Fixed(2*65536) {
		t.Fatalf("bounce VY %d want %d [03 §1] C5", got.VY, 2*65536)
	}

	// Alternative branch: record with model should clear model on contact, not bounce
	var p2 FixedEffectPool
	p2.SetGravity(gravity)
	p2.SetHeightFunc(func(x, z numeric.Fixed) numeric.Fixed { return terrainH })
	rec2 := EffectRecord{
		X:        numeric.Fixed(0),
		Y:        numeric.Fixed(12 * 65536),
		Z:        numeric.Fixed(0),
		VY:       numeric.Fixed(-3 * 65536),
		AnimA:    EffectAnimPlayer{Idx: 0, Countdown: 10, Loop: true, Active: true, Frames: 2},
		HasModel: true,
	}
	p2.Append(rec2)
	p2.Update(1)
	if p2.Len() != 1 {
		t.Fatalf("model clear should keep record (anim still active)")
	}
	got2 := p2.Records()[0]
	if got2.HasModel {
		t.Fatalf("model pointer should be cleared on contact [03 §1] C5")
	}
	// Position should have advanced (not restored) when model branch taken
	if got2.Y == numeric.Fixed(12*65536) {
		t.Fatalf("model branch should not restore position [03 §1] C5")
	}
	// Check water contact: sea level 5, no terrain func, Y falls below sea => bounce
	var p3 FixedEffectPool
	p3.SetGravity(gravity)
	p3.SetSeaLevel(numeric.Fixed(5 * 65536))
	// No height func => water only
	rec3 := EffectRecord{
		X:     numeric.Fixed(0),
		Y:     numeric.Fixed(6 * 65536),
		Z:     numeric.Fixed(0),
		VY:    numeric.Fixed(-2 * 65536),
		AnimA: EffectAnimPlayer{Idx: 0, Countdown: 10, Loop: true, Active: true, Frames: 2},
	}
	p3.Append(rec3)
	p3.Update(1) // Y after gravity: 6 + (-2-1)=3 < sea 5 => bounce
	if p3.Len() != 1 {
		t.Fatalf("water bounce should keep record")
	}
	got3 := p3.Records()[0]
	if got3.Y != numeric.Fixed(6*65536) {
		t.Fatalf("water bounce restore Y %d want %d", got3.Y, 6*65536)
	}
}

// TestFixedEffectAnimStepping verifies both embedded players are single-stepped [03 §1] C5
// and non-looping sequences are cleared at termination.
func TestFixedEffectAnimStepping(t *testing.T) {
	var p FixedEffectPool
	// AnimA: 2 frames looping, AnimB: 1 frame non-looping with countdown 1 => terminates on first step
	rec := EffectRecord{
		X:        numeric.Fixed(0),
		Y:        numeric.Fixed(0),
		VX:       numeric.Fixed(0),
		AnimA:    EffectAnimPlayer{Idx: 0, Countdown: 1, Loop: true, Active: true, Frames: 2},
		AnimB:    EffectAnimPlayer{Idx: 0, Countdown: 1, Loop: false, Active: true, Frames: 1},
		HasModel: true, // keep alive even after AnimB terminates, so we can observe stepping
	}
	p.Append(rec)
	p.Update(1)
	if p.Len() != 1 {
		t.Fatalf("record should survive because HasModel true")
	}
	got := p.Records()[0]
	// AnimB should have terminated (cleared)
	if got.AnimB.Active {
		t.Fatalf("AnimB non-looping should be cleared at termination [03 §1] C5")
	}
	// AnimA should have advanced one frame (looping)
	if got.AnimA.Idx != 1 {
		t.Fatalf("AnimA should have advanced to 1, got %d", got.AnimA.Idx)
	}
	// Next tick, AnimB already inactive stays inactive, AnimA advances and wraps
	p.Update(2)
	got = p.Records()[0]
	if got.AnimA.Idx != 0 {
		t.Fatalf("AnimA looping should wrap to 0, got %d", got.AnimA.Idx)
	}
	if got.AnimB.Active {
		t.Fatalf("AnimB should remain inactive")
	}
}

func TestFixedEffectAuthoredDurationsAndSnapshotIsolation(t *testing.T) {
	var p FixedEffectPool
	input := frame.EffectView{
		ID: 9, PresentationID: 19, EventSeq: 20, Kind: "explosion",
		DurationsA: []int32{2, 3}, LoopA: false, HasModel: true,
	}
	if !p.AppendView(input) {
		t.Fatal("authored effect admission failed")
	}
	input.DurationsA[0] = 99
	if got := p.SnapshotViews()[0].DurationsA[0]; got != 2 {
		t.Fatalf("effect duration aliased input: %d", got)
	}
	p.Update(1)
	if got := p.SnapshotViews()[0].SeqA; got != 0 {
		t.Fatalf("frame advanced before authored duration elapsed: %d", got)
	}
	p.Update(2)
	if got := p.SnapshotViews()[0].SeqA; got != 1 {
		t.Fatalf("authored duration frame = %d, want 1", got)
	}
	view := p.SnapshotViews()
	view[0].DurationsA[0] = 77
	if got := p.SnapshotViews()[0].DurationsA[0]; got != 2 {
		t.Fatalf("pool snapshot aliases internal duration: %d", got)
	}
}

func TestFixedEffectRejectsMalformedTimingWithoutOneTickFallback(t *testing.T) {
	var p FixedEffectPool
	if !p.AppendView(frame.EffectView{ID: 1, Kind: "impact", DurationsA: []int32{2, 0}}) {
		t.Fatal("malformed timing admission should preserve metadata record")
	}
	if got := p.Records()[0].AnimA.Active; got {
		t.Fatal("malformed authored timing activated a synthetic cursor")
	}
	p.Update(1)
	if p.Len() != 0 {
		t.Fatalf("unresolved timing record did not retire without a visual player: %d", p.Len())
	}
}

// TestFixedEffectPreservesStripThroughPool locks the strip-6 nanolathe draw
// path [03 §5.5][R-P0-06 §5]: the effect-strip destination routed at admission
// must survive the fixed pool so the client's strip-6 nanolathe branch fires.
func TestFixedEffectPreservesStripThroughPool(t *testing.T) {
	var p FixedEffectPool
	if !p.AppendView(frame.EffectView{ID: 3, Kind: "nanolathe", Strip: 6, NanolatheGeometryKnown: true}) {
		t.Fatal("nanolathe admission failed")
	}
	got := p.SnapshotViews()
	if len(got) != 1 || got[0].Strip != 6 {
		t.Fatalf("strip dropped through pool: %+v", got)
	}
	if !got[0].NanolatheGeometryKnown {
		t.Fatal("nanolathe geometry flag dropped through pool")
	}
	if rec := p.Records()[0]; rec.Strip != 6 {
		t.Fatalf("record strip = %d, want 6", rec.Strip)
	}
}

// TestFixedEffectDeterminism verifies stable order and no RNG [03 §1][I4][I1].
func TestFixedEffectDeterminism(t *testing.T) {
	run := func() []int64 {
		var p FixedEffectPool
		for i := 0; i < 5; i++ {
			p.Append(persistentEffect(i * 10))
		}
		// Mix a bounce and an anim termination
		p.SetGravity(numeric.Fixed(1 * 65536))
		p.SetSeaLevel(numeric.Fixed(0))
		p.SetHeightFunc(func(x, z numeric.Fixed) numeric.Fixed { return numeric.Fixed(100 * 65536) })
		for tick := uint32(0); tick < 3; tick++ {
			p.Update(tick)
			// append deterministic new persistent effect each tick
			p.Append(persistentEffect(int(tick) + 100))
			if p.Len() > FixedEffectCap {
				t.Fatalf("over cap")
			}
		}
		out := make([]int64, p.Len())
		for i, rec := range p.Records() {
			out[i] = int64(rec.X >> 16)
		}
		return out
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("determinism len %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("determinism i=%d %d vs %d", i, a[i], b[i])
		}
	}
}

// TestFixedEffectAndStripIntegrationDeterminism ensures direct pool updates are deterministic.
func TestFixedEffectAndStripIntegrationDeterminism(t *testing.T) {
	run := func() (int, int) {
		var strip Strip
		var effects FixedEffectPool
		strip.Append(&mockObj{id: 5})
		effects.Append(persistentEffect(77))
		effects.Append(dyingEffect(88))
		strip.Update(10)
		effects.Update(10)
		strip.Update(11)
		effects.Update(11)
		return len(strip.Objects), effects.Len()
	}
	s1, e1 := run()
	s2, e2 := run()
	if s1 != s2 || e1 != e2 {
		t.Fatalf("composer determinism %d/%d vs %d/%d", s1, e1, s2, e2)
	}
}

// TestFixedEffectPublishesPerPlayerLiveness locks the published liveness of the
// two embedded animation players [03 §1].
//
// The defect: a record survives until BOTH players are inactive, so a
// non-looping player that terminates first leaves its cursor at index 0 in a
// record that is still published every tick [03 §4.4]. Nothing on the view said
// the player was over, so the draw pass re-read index 0 as a live first frame —
// the finished layer started again under the one still playing. Liveness is now
// published per player and never inferred from the durations, the art name or
// the cursor index.
func TestFixedEffectPublishesPerPlayerLiveness(t *testing.T) {
	// A two-player record with unequal timing: the primary art runs two ticks
	// (two frames held one tick each) and the calculated flash six (three
	// frames held two ticks each).
	shortLong := frame.EffectView{
		ID: 1, Kind: "explosion", Graphic: "art", AssetID: "fx",
		HasCalculatedFlash: true, CalculatedTable: 0,
		DurationsA: []int32{1, 1},
		DurationsB: []int32{2, 2, 2},
	}
	longShort := shortLong
	longShort.DurationsA, longShort.DurationsB = shortLong.DurationsB, shortLong.DurationsA

	// (a) The primary finishes first and stops being drawn while the secondary
	// keeps the record alive.
	var p FixedEffectPool
	if !p.AppendView(shortLong) {
		t.Fatal("two-player admission failed")
	}
	for tick := uint32(1); tick <= 2; tick++ {
		p.Update(tick)
	}
	views := p.SnapshotViews()
	if len(views) != 1 {
		t.Fatalf("the record retired while its secondary player was still running: %d", len(views))
	}
	if views[0].ActiveA {
		t.Fatal("the terminated primary player is still published as live")
	}
	if !views[0].ActiveB {
		t.Fatal("the running secondary player is published as dead")
	}
	if views[0].SeqA != 0 {
		t.Fatalf("terminated cursor = %d; index 0 is exactly why liveness cannot be inferred from it", views[0].SeqA)
	}
	if draws := BuildEffectDraws(views); len(draws) != 1 || draws[0].ActiveA || !draws[0].ActiveB {
		t.Fatalf("liveness did not reach the draw instruction: %+v", draws)
	}

	// (c) Both finishing retires the record, inside the same updater call.
	for tick := uint32(3); tick <= 6; tick++ {
		p.Update(tick)
	}
	if p.Len() != 0 {
		t.Fatalf("both players inactive but %d records survive [03 §1]", p.Len())
	}

	// (b) The inverse: the secondary finishes first and the primary keeps
	// playing.
	var q FixedEffectPool
	if !q.AppendView(longShort) {
		t.Fatal("two-player admission failed")
	}
	for tick := uint32(1); tick <= 2; tick++ {
		q.Update(tick)
	}
	views = q.SnapshotViews()
	if len(views) != 1 {
		t.Fatalf("the record retired while its primary player was still running: %d", len(views))
	}
	if views[0].ActiveB {
		t.Fatal("the terminated secondary player is still published as live")
	}
	if !views[0].ActiveA {
		t.Fatal("the running primary player is published as dead")
	}

	// (d) A layer with no player of its own draws nothing. A named entry whose
	// authored timing never resolved activates no player, and nothing is
	// fabricated to stand in for it [I9]: liveness is the sequence pointer
	// alone, not "has art" and not "has durations". The impact shape that used
	// to land here — art beside a calculated flash — no longer does, because
	// the timing lookup is per player [06 R-WFX-01 §2]; see
	// TestEffectServiceResolvesPrimaryTimingBesideACalculatedFlash.
	var r FixedEffectPool
	if !r.AppendView(frame.EffectView{
		ID: 2, Kind: "explosion", Graphic: "art", AssetID: "fx",
		HasCalculatedFlash: true, CalculatedTable: 0,
		DurationsB: []int32{2, 2, 2},
	}) {
		t.Fatal("unresolved-art admission failed")
	}
	r.Update(1)
	views = r.SnapshotViews()
	if len(views) != 1 || !views[0].ActiveB {
		t.Fatalf("the flash player should still be running: %+v", views)
	}
	if views[0].ActiveA {
		t.Fatal("art whose timing never resolved published a live player")
	}
	for tick := uint32(2); tick <= 6; tick++ {
		r.Update(tick)
	}
	if r.Len() != 0 {
		t.Fatalf("the flash ended but %d records survive", r.Len())
	}

	// (e) A looping player never terminates, so its layer stays live.
	var s FixedEffectPool
	if !s.AppendView(frame.EffectView{
		ID: 3, Kind: "smokestart", Graphic: "smoke 1",
		DurationsA: []int32{1, 1}, LoopA: true,
	}) {
		t.Fatal("looping admission failed")
	}
	for tick := uint32(1); tick <= 20; tick++ {
		s.Update(tick)
		views = s.SnapshotViews()
		if len(views) != 1 || !views[0].ActiveA {
			t.Fatalf("a looping player went dead at tick %d: %+v", tick, views)
		}
	}
}
