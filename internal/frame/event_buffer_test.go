package frame

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestEventBufferOrderingIDsAndDetachedTiming(t *testing.T) {
	durations := []int32{2, 3}
	c := NewEventBuffer(Limits{MaxEvents: 4, MaxEffectEvents: 4})
	if !c.EmitImpact(Event{Tick: 7, Graphic: "first", DurationsA: durations}) ||
		!c.EmitExplosion(Event{Tick: 7, Graphic: "second"}) {
		t.Fatal("admission failed")
	}
	durations[0] = 99
	got := c.SnapshotEvents()
	if len(got) != 2 || got[0].Sequence != 1 || got[1].Sequence != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("ordered identity = %+v", got)
	}
	if got[0].DurationsA[0] != 2 {
		t.Fatalf("admission timing aliased input: %+v", got[0].DurationsA)
	}
	got[0].DurationsA[0] = 88
	if c.SnapshotEvents()[0].DurationsA[0] != 2 {
		t.Fatal("snapshot timing aliased staging")
	}
}

func TestEventBufferBoundsResetAndDiagnostics(t *testing.T) {
	c := NewEventBuffer(Limits{MaxEvents: 1, MaxEffectEvents: 1})
	if !c.EmitImpact(Event{X: numeric.Fixed(1)}) || c.EmitExplosion(Event{}) {
		t.Fatal("expected one admitted event and one bounded drop")
	}
	if c.Dropped() != 1 || !c.Overflow() || len(c.Events()) != 1 {
		t.Fatalf("diagnostics dropped=%d overflow=%v events=%d", c.Dropped(), c.Overflow(), len(c.Events()))
	}
	c.Reset()
	if c.Dropped() != 0 || c.Overflow() || len(c.Events()) != 0 {
		t.Fatal("reset did not clear staging diagnostics")
	}
	if !c.EmitImpact(Event{}) || c.SnapshotEvents()[0].ID != 2 || c.SnapshotEvents()[0].Sequence != 2 {
		t.Fatalf("reset did not retain monotonic identity: %+v", c.SnapshotEvents())
	}
}

func TestEventBufferRoutesAndBuildsNanolatheSegments(t *testing.T) {
	if RouteForProducer(ProducerBeam) != StripBeam || RouteForProducer(ProducerUnknown) != StripUnknown {
		t.Fatal("strip routing contract changed")
	}
	e := Event{Mode: uint8(NanolatheBuild), Producer: ProducerBeam, X: numeric.Fixed(3), TargetX: numeric.Fixed(9)}
	if c := NewEventBuffer(Limits{}); c.EmitNanolatheSegments(e, 4) != 2 {
		t.Fatal("build nanolathe should emit two segments")
	} else {
		got := c.SnapshotEvents()
		if len(got) != 2 || got[0].Strip != int8(StripBeam) || got[0].NanolatheIndex != 0 || got[1].NanolatheIndex != 1 || got[1].NanolatheCount != 2 {
			t.Fatalf("nanolathe events = %+v", got)
		}
	}
}
