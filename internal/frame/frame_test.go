package frame

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestBufferFlipsSlotsAndKeepsCommittedStableDuringWrite(t *testing.T) {
	b := NewBuffer(Capacities{Units: 1})
	first := b.BeginWrite()
	first.Units = append(first.Units, UnitView{Slot: 7, X: numeric.Fixed(11)})
	if err := b.Publish(10); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	committed := b.Current()
	if committed == nil || committed.Tick != 10 || committed.Units[0].Slot != 7 {
		t.Fatalf("unexpected first committed frame: %#v", committed)
	}

	second := b.BeginWrite()
	if second == committed {
		t.Fatal("BeginWrite reused the committed slot")
	}
	second.Units = append(second.Units, UnitView{Slot: 8})
	if committed.Tick != 10 || len(committed.Units) != 1 || committed.Units[0].Slot != 7 {
		t.Fatalf("committed frame changed while alternate slot was written: %#v", committed)
	}
	if err := b.Publish(11); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if b.Current() != second || b.Current().Tick != 11 {
		t.Fatal("second slot was not committed")
	}
}

func TestBufferRejectsMissingAndNonMonotonicPublication(t *testing.T) {
	var b Buffer
	if err := b.Publish(1); !errors.Is(err, ErrPublishWithoutWrite) {
		t.Fatalf("publish without write error = %v", err)
	}
	b.BeginWrite()
	if err := b.Publish(4); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	b.BeginWrite()
	if err := b.Publish(4); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("duplicate tick error = %v", err)
	}
	// A rejected publication leaves the pending slot available, but a fresh
	// BeginWrite is used here to make the retry boundary explicit.
	b.BeginWrite()
	if err := b.Publish(3); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("retrograde tick error = %v", err)
	}
	b.BeginWrite()
	if err := b.Publish(5); err != nil {
		t.Fatalf("next monotonic publish: %v", err)
	}
	if tick, ok := b.PublishedTick(); !ok || tick != 5 {
		t.Fatalf("published tick = %d, %t", tick, ok)
	}
}

func TestFrameResetRetainsNestedCapacities(t *testing.T) {
	f := Frame{
		Units:       make([]UnitView, 1, 2),
		Effects:     make([]EffectView, 1, 2),
		OrderQueues: make([]OrderQueueView, 1, 2),
		Events:      make([]EventView, 1, 2),
		Selection:   SelectionView{Handles: make([]pool.Handle, 1, 3)},
		CommandPage: CommandPageView{ProductKeys: make([]string, 1, 3)},
		Visibility: VisibilityView{
			Visible:     make([]uint8, 1, 4),
			WordVisible: make([]uint16, 1, 4),
		},
		Fog: FogView{Ch0: make([]uint8, 1, 4), Ch1: make([]uint8, 1, 4)},
		Result: ResultView{
			Winners: make([]int, 1, 3),
			Losers:  make([]int, 1, 3),
			Scores:  make([]ResultScore, 1, 3),
		},
	}
	f.Units[0].Pieces = make([]PieceView, 1, 3)
	f.Effects[0].DurationsA = make([]int32, 1, 3)
	f.Effects[0].DurationsB = make([]int32, 1, 3)
	f.OrderQueues[0].Primary = make([]OrderView, 1, 3)
	f.OrderQueues[0].Secondary = make([]OrderView, 1, 3)
	f.OrderQueues[0].Primary[0].Route = make([]RoutePoint, 1, 3)
	f.OrderQueues[0].Secondary[0].Route = make([]RoutePoint, 1, 3)
	f.Events[0].DurationsA = make([]int32, 1, 3)
	f.Events[0].DurationsB = make([]int32, 1, 3)

	f.Tick = 99
	f.Paused = true
	f.Reset()
	if f.Tick != 0 || f.Paused || len(f.Units) != 0 || len(f.Effects) != 0 || len(f.OrderQueues) != 0 || len(f.Events) != 0 {
		t.Fatalf("frame scalar/top-level reset failed: %#v", f)
	}
	if got := f.Units[:1][0].Pieces; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("unit pieces len/cap = %d/%d", len(got), cap(got))
	}
	if got := f.Effects[:1][0].DurationsA; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("effect durations A len/cap = %d/%d", len(got), cap(got))
	}
	if got := f.OrderQueues[:1][0].Primary; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("primary queue len/cap = %d/%d", len(got), cap(got))
	}
	if got := f.OrderQueues[:1][0].Secondary; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("secondary queue len/cap = %d/%d", len(got), cap(got))
	}
	if got := f.OrderQueues[:1][0].Primary[:1][0].Route; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("primary route len/cap = %d/%d", len(got), cap(got))
	}
	if got := f.Events[:1][0].DurationsB; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("event durations B len/cap = %d/%d", len(got), cap(got))
	}
	if len(f.Selection.Handles) != 0 || cap(f.Selection.Handles) != 3 || len(f.Visibility.Visible) != 0 || cap(f.Visibility.Visible) != 4 || len(f.Fog.Ch1) != 0 || cap(f.Fog.Ch1) != 4 {
		t.Fatal("flat slice capacities were not retained")
	}
	if len(f.Result.Scores) != 0 || cap(f.Result.Scores) != 3 {
		t.Fatal("result slice capacity was not retained")
	}
}

func TestFrameRadarResetRetainsContactsAndCircleStorage(t *testing.T) {
	f := Frame{
		Radar: RadarView{
			Contacts:   make([]RadarContactView, 1, 2),
			Circles:    make([]RadarCircleView, 1, 2),
			BlinkPhase: 1,
		},
	}
	f.Radar.Contacts[0].Rings = make([]RadarRingView, 1, 3)
	f.Reset()
	if f.Radar.BlinkPhase != 0 {
		t.Fatalf("radar blink phase reset = %d, want 0", f.Radar.BlinkPhase)
	}
	if len(f.Radar.Contacts) != 0 || cap(f.Radar.Contacts) != 2 || len(f.Radar.Circles) != 0 || cap(f.Radar.Circles) != 2 {
		t.Fatalf("radar top-level storage len/cap = %d/%d contacts, %d/%d circles", len(f.Radar.Contacts), cap(f.Radar.Contacts), len(f.Radar.Circles), cap(f.Radar.Circles))
	}
	if got := f.Radar.Contacts[:1][0].Rings; len(got) != 0 || cap(got) != 3 {
		t.Fatalf("radar nested ring storage len/cap = %d/%d", len(got), cap(got))
	}
}

func TestFrameRadarPhaseCopiesAsScalar(t *testing.T) {
	f := Frame{Radar: RadarView{
		BlinkPhase: 1,
		Contacts:   []RadarContactView{{Rings: []RadarRingView{{Enabled: true}}}},
	}}
	copy := f
	if copy.Radar.BlinkPhase != 1 || len(copy.Radar.Contacts) != 1 || len(copy.Radar.Contacts[0].Rings) != 1 {
		t.Fatalf("radar copy lost phase or nested storage: %+v", copy.Radar)
	}
	copy.Radar.BlinkPhase = 0
	if f.Radar.BlinkPhase != 1 {
		t.Fatal("radar phase copy aliases scalar state")
	}
}

func TestBufferWarmPublishAllocationsAfterWarmup(t *testing.T) {
	b := NewBuffer(Capacities{
		Units: 1, Projectiles: 1, Features: 1, Effects: 1,
		OrderQueues: 1, Builds: 1, Cues: 1, Selection: 1,
		CommandProducts: 1, Visibility: 4, Fog: 4,
	})
	for tick := uint32(1); tick <= 2; tick++ {
		f := b.BeginWrite()
		f.Units = append(f.Units, UnitView{Slot: pool.Handle(tick)})
		f.Projectiles = append(f.Projectiles, ProjectileView{Handle: pool.Handle(tick)})
		f.Visibility.Visible = append(f.Visibility.Visible, 1)
		if err := b.Publish(tick); err != nil {
			t.Fatalf("warm publish %d: %v", tick, err)
		}
	}
	nextTick := uint32(3)
	allocs := testing.AllocsPerRun(100, func() {
		f := b.BeginWrite()
		f.Units = append(f.Units, UnitView{Slot: pool.Handle(nextTick)})
		f.Projectiles = append(f.Projectiles, ProjectileView{Handle: pool.Handle(nextTick)})
		f.Visibility.Visible = append(f.Visibility.Visible, 1)
		if err := b.Publish(nextTick); err != nil {
			t.Fatal(err)
		}
		nextTick++
	})
	if allocs != 0 {
		t.Fatalf("warm BeginWrite/Publish allocated %v times", allocs)
	}
}
