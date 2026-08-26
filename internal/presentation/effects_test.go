package presentation

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestEffectServiceAdmissionOrderAndExplicitExpiry(t *testing.T) {
	svc := NewEffectService(4)
	events := []Event{
		{ID: 7, Sequence: 11, Tick: 10, Kind: KindNanolathe, Lifetime: 3, Graphic: "nano"},
		{ID: 8, Sequence: 12, Tick: 10, Kind: KindImpact, Graphic: "impact"},
	}
	svc.Advance(10, events)
	got := svc.Snapshot()
	if len(got) != 2 || got[0].ID != 7 || got[1].ID != 8 || got[0].EventSeq != 11 {
		t.Fatalf("admission order = %+v", got)
	}
	if got[0].ExpiryTick != 13 || got[0].Lifetime != 3 {
		t.Fatalf("explicit lifetime = %+v", got[0])
	}
	if got[1].ExpiryTick != 0 || got[1].Lifetime != 0 {
		t.Fatalf("unknown lifetime was guessed = %+v", got[1])
	}
	svc.Advance(12, nil)
	if len(svc.Snapshot()) != 2 {
		t.Fatal("effect expired before its authored deadline")
	}
	svc.Advance(13, nil)
	got = svc.Snapshot()
	if len(got) != 1 || got[0].ID != 8 {
		t.Fatalf("expiry/compaction = %+v", got)
	}
	// Replaying an already published event window cannot duplicate an effect.
	svc.Advance(14, events)
	if len(svc.Snapshot()) != 1 || svc.Snapshot()[0].ID != 8 {
		t.Fatalf("duplicate event admission = %+v", svc.Snapshot())
	}
}

func TestEffectServiceAuthoredFrameTiming(t *testing.T) {
	svc := NewEffectService(4)
	svc.SetTimingResolver(func(e Event) (FrameTiming, bool) {
		if e.Graphic != "authored" {
			return FrameTiming{}, false
		}
		return FrameTiming{Durations: []int32{2, 3}}, true
	})
	svc.Advance(10, []Event{{ID: 1, Sequence: 1, Tick: 10, Kind: KindExplosion, Graphic: "authored"}})
	if got := svc.Snapshot()[0].SeqA; got != 0 {
		t.Fatalf("initial authored frame = %d", got)
	}
	svc.Advance(11, nil)
	if got := svc.Snapshot()[0].SeqA; got != 0 {
		t.Fatalf("frame advanced too soon = %d", got)
	}
	svc.Advance(12, nil)
	if got := svc.Snapshot()[0].SeqA; got != 1 {
		t.Fatalf("authored frame cadence = %d, want 1", got)
	}
	svc.Advance(15, nil)
	if len(svc.Snapshot()) != 0 {
		t.Fatalf("non-looping authored effect survived terminal frame: %+v", svc.Snapshot())
	}
}

func TestEffectServiceSmokeEndAndBound(t *testing.T) {
	svc := NewEffectService(1)
	start := Event{ID: 4, Sequence: 4, Tick: 1, Kind: KindSmokeStart, Source: 3, Target: 9, Graphic: "smoke"}
	svc.Advance(1, []Event{start})
	svc.Advance(2, []Event{{ID: 5, Sequence: 5, Tick: 2, Kind: KindSmokeStart, Source: 4, Graphic: "second"}})
	if len(svc.Snapshot()) != 1 || svc.Dropped() != 1 {
		t.Fatalf("fixed bound = effects=%+v dropped=%d", svc.Snapshot(), svc.Dropped())
	}
	svc.Advance(3, []Event{{ID: 6, Sequence: 6, Tick: 3, Kind: KindSmokeEnd, Source: 3, Target: 9}})
	if len(svc.Snapshot()) != 0 {
		t.Fatalf("smoke end did not remove matching effect: %+v", svc.Snapshot())
	}
	// A full-pool rejection belongs only to that publication window. Once the
	// matching smoke is removed, the next window is not permanently marked
	// truncated.
	svc.Advance(4, []Event{{ID: 7, Sequence: 7, Tick: 4, Kind: KindImpact}})
	if svc.Dropped() != 0 || len(svc.Snapshot()) != 1 {
		t.Fatalf("stale admission diagnostic or failed reuse: dropped=%d effects=%+v", svc.Dropped(), svc.Snapshot())
	}
}

func TestEffectServiceIncludesCOBSFXAndPreservesSelector(t *testing.T) {
	svc := NewEffectService(2)
	svc.Advance(3, []Event{{ID: 1, Sequence: 1, Tick: 3, Kind: KindCOBSFX, EffectID: 5, Piece: 2, SFXType: 3, X: 10, TargetX: 20}})
	got := svc.Snapshot()
	if len(got) != 1 || got[0].Kind != snapshot.EventKindCOBSFX.String() || got[0].EffectID != 5 || got[0].Piece != 2 || got[0].TargetX != 20 {
		t.Fatalf("COB SFX effect publication = %+v", got)
	}
}

func TestEffectServiceSnapshotIsDetachedAndPresentationOnly(t *testing.T) {
	e := Event{ID: 9, Sequence: 9, Tick: 2, Kind: KindLHTFlash, X: 11, Y: 12, Z: 13}
	svc := NewEffectService(2)
	svc.Advance(2, []Event{e})
	got := svc.Snapshot()
	got[0].X = 99
	if svc.Snapshot()[0].X != 11 {
		t.Fatal("snapshot mutation changed service state")
	}
	if svc.Snapshot()[0].Kind != snapshot.EventKindLHTFlash.String() || !svc.Snapshot()[0].Light {
		t.Fatalf("LHT publication = %+v", svc.Snapshot()[0])
	}
	if e.X != 11 || e.Y != 12 || e.Z != 13 {
		t.Fatalf("event payload mutated by presentation publication: %+v", e)
	}
}
