package render

import (
	"slices"
	"testing"
)

// TestEffectServiceResolvesPrimaryTimingBesideACalculatedFlash locks the
// per-player timing lookup [06 R-WFX-01 §2][03 §1].
//
// An impact record carries two players: the weapon's named art as the primary,
// and the procedurally generated flash table as the secondary, whose holds the
// producer publishes as the secondary durations. Admission used to consult the
// authored-timing resolver only when NEITHER player had timing, so the flash's
// presence suppressed the art's lookup and every impact reached the pool with
// no primary player at all — art that could only ever be shown as a static
// frame 0, for as long as the flash kept the record alive. Each player's timing
// is now resolved on its own.
func TestEffectServiceResolvesPrimaryTimingBesideACalculatedFlash(t *testing.T) {
	pool := &FixedEffectPool{}
	s := NewEffectServiceWithPool(EffectCapacity, pool)
	resolved := 0
	s.SetTimingResolver(func(e Event) (FrameTiming, bool) {
		resolved++
		if e.Graphic != "art" {
			return FrameTiming{}, false
		}
		// The stock effect entries hold 2 or 3 ticks a frame [06 R-WFX-01 §1].
		return FrameTiming{Durations: []int32{2, 2, 2, 2}}, true
	})
	// The production impact shape: named art, and the calculated table's holds
	// already published as the secondary timing.
	s.Advance(1, []Event{{
		Kind: KindExplosion, Tick: 1, Sequence: 1,
		Graphic: "art", AssetID: "fx",
		HasCalculatedFlash: true, CalculatedTable: 0,
		DurationsB: FlashFrameDurations(0),
	}})
	if resolved != 1 {
		t.Fatalf("the resolver ran %d times; a flash on the secondary must not suppress the primary lookup", resolved)
	}
	if pool.Len() != 1 {
		t.Fatalf("admission produced %d records", pool.Len())
	}
	rec := pool.Records()[0]
	if !rec.AnimA.Active || rec.AnimA.Frames != 4 {
		t.Fatalf("the named art owns no primary player: %+v", rec.AnimA)
	}
	if !rec.AnimB.Active || rec.AnimB.Frames != len(FlashFrameDurations(0)) {
		t.Fatalf("the calculated flash lost its secondary player: %+v", rec.AnimB)
	}
	views := pool.SnapshotViews()
	if len(views) != 1 || !views[0].ActiveA || !views[0].ActiveB {
		t.Fatalf("both players are running but liveness says otherwise: %+v", views)
	}

	// An entry the resolver cannot find stays unresolved: no player, no
	// invented lifetime, and the layer publishes dead [I9].
	miss := &FixedEffectPool{}
	m := NewEffectServiceWithPool(EffectCapacity, miss)
	m.SetTimingResolver(func(Event) (FrameTiming, bool) { return FrameTiming{}, false })
	m.Advance(1, []Event{{
		Kind: KindExplosion, Tick: 1, Sequence: 1,
		Graphic: "no-such-entry", AssetID: "fx",
		HasCalculatedFlash: true, CalculatedTable: 0,
		DurationsB: FlashFrameDurations(0),
	}})
	if views = miss.SnapshotViews(); len(views) != 1 || views[0].ActiveA {
		t.Fatalf("an unresolvable entry published a live primary player: %+v", views)
	}
}

// TestEffectServiceBindsAlreadyAdmittedPrimaryTiming locks the Create-before-
// composition sequence. COB can append named art while the session has its
// canonical pool but before the client installs the GAF timing resolver; that
// binding must activate the existing primary player without re-admitting,
// reordering, or changing its calculated secondary [03 §1][06 R-WFX-01 §2].
func TestEffectServiceBindsAlreadyAdmittedPrimaryTiming(t *testing.T) {
	pool := &FixedEffectPool{}
	s := NewEffectServiceWithPool(EffectCapacity, pool)
	s.Advance(1, []Event{{
		ID: 91, Kind: KindExplosion, Tick: 1, Sequence: 1,
		Graphic: "art", AssetID: "fx",
		HasCalculatedFlash: true, CalculatedTable: 2,
		DurationsB: FlashFrameDurations(2),
	}})
	before := pool.Records()
	if len(before) != 1 || before[0].AnimA.Active || !before[0].AnimB.Active {
		t.Fatalf("pre-binding record = %+v, want unresolved primary and live calculated secondary", before)
	}
	id := before[0].ID
	secondary := before[0].AnimB
	secondaryDurations := append([]int32(nil), secondary.Durations...)
	resolved := 0
	s.SetTimingResolver(func(e Event) (FrameTiming, bool) {
		resolved++
		if e.Graphic != "art" || e.AssetID != "fx" {
			return FrameTiming{}, false
		}
		return FrameTiming{Durations: []int32{2, 3}}, true
	})
	after := pool.Records()
	if resolved != 1 || len(after) != 1 || after[0].ID != id {
		t.Fatalf("binding changed record admission: calls=%d records=%+v", resolved, after)
	}
	if got := after[0].AnimA; !got.Active || got.Idx != 0 || got.Countdown != 2 || got.Frames != 2 || len(got.Durations) != 2 {
		t.Fatalf("binding primary = %+v, want active authored player", got)
	}
	if got := after[0].AnimB; got.Idx != secondary.Idx || got.Countdown != secondary.Countdown ||
		got.Loop != secondary.Loop || got.Active != secondary.Active || got.Frames != secondary.Frames ||
		!slices.Equal(got.Durations, secondaryDurations) {
		t.Fatalf("binding changed calculated secondary: got %+v, want %+v", got, secondary)
	}
	// Rebinding does not restart an already resolved primary player.
	s.SetTimingResolver(func(Event) (FrameTiming, bool) {
		t.Fatal("rebind resolved an already active primary")
		return FrameTiming{}, false
	})
}

// TestEffectServicePendingViewsCarryLiveness covers the fixture fallback that
// runs with no pool bound. The pool is the normal publisher of per-player
// liveness, so the fallback mirrors its admission rule; otherwise a pending
// view would be published with both players dead and silently draw nothing
// [03 §1].
func TestEffectServicePendingViewsCarryLiveness(t *testing.T) {
	s := newEffectService(0)
	s.Advance(1, []Event{
		{Kind: KindExplosion, Tick: 1, Sequence: 1, Graphic: "art", DurationsA: []int32{2, 2}},
		{Kind: KindExplosion, Tick: 1, Sequence: 2, Graphic: "art"},
	})
	views := s.Snapshot()
	if len(views) != 2 {
		t.Fatalf("pending admission produced %d views", len(views))
	}
	if !views[0].ActiveA {
		t.Fatal("a pending view with authored timing published a dead player")
	}
	if views[1].ActiveA {
		t.Fatal("a pending view with no authored timing published a live player")
	}
}
