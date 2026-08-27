package presentation

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestEstablishedStripRoutesRequireProducerIdentity(t *testing.T) {
	cases := []struct {
		producer StripProducer
		want     Strip
	}{
		{ProducerShockwave, StripShockwave}, {ProducerCrater, StripCrater},
		{ProducerBeam, StripBeam}, {ProducerLightning, StripLightning},
		{ProducerSmoke, StripSmoke},
	}
	for _, tc := range cases {
		if got := RouteForProducer(tc.producer); got != tc.want {
			t.Errorf("route %v = %d, want %d", tc.producer, got, tc.want)
		}
	}
	for _, kind := range []Kind{KindImpact, KindExplosion, KindCorpse, KindCOBSFX} {
		if got := RouteForKind(kind); got != StripUnknown {
			t.Fatalf("generic kind %v route = %d, want unknown", kind, got)
		}
	}
}

func TestRouteEventUsesOnlyExplicitProducer(t *testing.T) {
	unknown := Event{Kind: KindExplosion}
	RouteEvent(&unknown)
	if unknown.Strip != -1 {
		t.Fatalf("generic event route = %d, want unresolved", unknown.Strip)
	}
	explicit := Event{Kind: KindExplosion, Producer: ProducerShockwave}
	RouteEvent(&explicit)
	if explicit.Strip != int8(StripShockwave) {
		t.Fatalf("explicit producer route = %d, want %d", explicit.Strip, StripShockwave)
	}
	preResolved := Event{Kind: KindImpact, Producer: ProducerSmoke, Strip: int8(StripBeam)}
	RouteEvent(&preResolved)
	if preResolved.Strip != int8(StripBeam) {
		t.Fatalf("pre-resolved route changed to %d", preResolved.Strip)
	}
}

func TestNanolatheCadenceAndAuthoritativeGeometry(t *testing.T) {
	if got := NanolatheSegmentCount(NanolatheBuild, 1); got != 2 {
		t.Fatalf("build count = %d, want 2", got)
	}
	if got := NanolatheSegmentCount(NanolatheReclaim, 0); got != 1 {
		t.Fatalf("reclaim even count = %d, want 1", got)
	}
	if got := NanolatheSegmentCount(NanolatheCapture, 1); got != 0 {
		t.Fatalf("capture odd count = %d, want 0", got)
	}
	e := Event{Mode: uint8(NanolatheBuild), X: numeric.Fixed(3 << 16), TargetX: numeric.Fixed(9 << 16), TargetZ: numeric.Fixed(2 << 16)}
	segments := BuildNanolatheSegments(e, 4)
	if len(segments) != 2 || segments[1].Color != 6 {
		t.Fatalf("segments = %+v, want two palette-6 segments", segments)
	}
	if segments[0].FromX != e.X || segments[0].ToX != e.TargetX || segments[0].ToZ != e.TargetZ {
		t.Fatal("producer request geometry was not preserved as metadata")
	}
}

func TestCollectorDetachesAuthoredTimingAndRoutes(t *testing.T) {
	durations := []int32{2, 3}
	c := NewCollector(Limits{MaxEvents: 2, MaxEffectEvents: 2, MaxSounds: 2})
	if !c.EmitExplosion(Event{Tick: 1, DurationsA: durations}) {
		t.Fatal("event rejected")
	}
	durations[0] = 99
	got := c.SnapshotEvents()
	if got[0].Strip != -1 || got[0].DurationsA[0] != 2 {
		t.Fatalf("snapshot metadata = %+v", got[0])
	}
	got[0].DurationsA[0] = 88
	if c.SnapshotEvents()[0].DurationsA[0] != 2 {
		t.Fatal("snapshot timing aliases collector storage")
	}
}
