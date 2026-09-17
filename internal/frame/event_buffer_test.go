package frame

import (
	"reflect"
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
	// Feature reclaim is the only two-segment producer [05 R-WORK-01 §8].
	e := Event{Mode: uint8(NanolatheFeatureReclaim), Producer: ProducerBeam, X: numeric.Fixed(3), TargetX: numeric.Fixed(9)}
	if c := NewEventBuffer(Limits{}); c.EmitNanolatheSegments(e, 4) != 2 {
		t.Fatal("feature reclaim should emit two segments")
	} else {
		got := c.SnapshotEvents()
		if len(got) != 2 || got[0].Strip != int8(StripBeam) || got[0].NanolatheIndex != 0 || got[1].NanolatheIndex != 1 || got[1].NanolatheCount != 2 {
			t.Fatalf("nanolathe events = %+v", got)
		}
	}
}

// TestNanolatheSegmentCountMatchesProducerCensus locks the census of
// [05 R-WORK-01 §8]: every ordinary producer submits ONE segment per accepted
// work visit and feature reclaim is the only two-segment producer. The count
// is not a function of the tick — the per-producer retry interval belongs to
// the executor that decides whether a visit happens, not to the emission — so
// the same mode must give the same count on an even tick and an odd one.
func TestNanolatheSegmentCountMatchesProducerCensus(t *testing.T) {
	cases := []struct {
		mode NanolatheMode
		want int
	}{
		{NanolatheBuild, 1},
		{NanolatheReclaim, 1},
		{NanolatheCapture, 1},
		{NanolatheFeatureReclaim, 2},
		{NanolatheMode(0), 0},
		{NanolatheMode(9), 0},
	}
	for _, tc := range cases {
		if got := NanolatheSegmentCount(tc.mode); got != tc.want {
			t.Errorf("NanolatheSegmentCount(%d) = %d, want %d", tc.mode, got, tc.want)
		}
		// The built segments follow the count, on any tick.
		for _, tick := range []uint32{0, 1, 2, 7} {
			e := Event{Mode: uint8(tc.mode), Producer: ProducerBeam}
			if got := len(BuildNanolatheSegments(e)); got != tc.want {
				t.Errorf("mode %d tick %d built %d segments, want %d", tc.mode, tick, got, tc.want)
			}
		}
	}
}

// fillDistinct gives every settable field of v a distinct non-zero value, so a
// field the snapshot forgets to copy shows up as a zero on the far side.
func fillDistinct(v reflect.Value, seed *int) {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*seed++
		// int8 fields (Strip) must stay in range and must not be the
		// unresolved sentinel the router rewrites.
		v.SetInt(int64(*seed%100 + 1))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*seed++
		v.SetUint(uint64(*seed%100 + 1))
	case reflect.String:
		*seed++
		v.SetString("field-" + string(rune('a'+*seed%26)))
	case reflect.Slice:
		e := reflect.New(v.Type().Elem()).Elem()
		fillDistinct(e, seed)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), e))
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillDistinct(v.Index(i), seed)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fillDistinct(v.Field(i), seed)
			}
		}
	}
}

// TestSnapshotCopiesEveryEventViewField locks the committed event payload
// against a silently dropped field: EventView is the publication boundary's
// copy of a staged Event [I6], so every field it declares must come from the
// event the producer admitted. The test drives it by reflection rather than by
// a hand-written field list so that ADDING a field to EventView and forgetting
// to copy it fails here — which is how HasCalculatedFlash and CalculatedTable,
// populated by the COB-explosion and debris producers, came to be missing from
// the committed payload while the client happened to read the separate
// EffectView pool instead.
func TestSnapshotCopiesEveryEventViewField(t *testing.T) {
	seed := 0
	var e Event
	fillDistinct(reflect.ValueOf(&e).Elem(), &seed)
	// Admission rejects an invalid kind or a negative lifetime, and assigns
	// ID and Sequence itself; everything else must survive untouched.
	e.Kind = KindExplosion
	e.Lifetime = 42

	c := NewEventBuffer(Limits{})
	if !c.Admit(e) {
		t.Fatal("fully populated event was refused")
	}
	got := c.SnapshotEvents()
	if len(got) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(got))
	}
	view := reflect.ValueOf(got[0])
	source := reflect.ValueOf(e)
	assigned := map[string]bool{"ID": true, "Sequence": true}
	for i := 0; i < view.NumField(); i++ {
		name := view.Type().Field(i).Name
		field := view.Field(i)
		if field.IsZero() {
			t.Errorf("EventView.%s is zero after the round trip: the snapshot does not copy it", name)
			continue
		}
		if assigned[name] {
			continue // EventBuffer owns these two [I6]
		}
		src := source.FieldByName(name)
		if !src.IsValid() {
			t.Errorf("EventView.%s has no same-named Event field to copy from", name)
			continue
		}
		if !reflect.DeepEqual(field.Interface(), src.Interface()) {
			t.Errorf("EventView.%s = %v, staged event had %v", name, field.Interface(), src.Interface())
		}
	}
}
