package snapshot

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestPublishedFramesAreStable locks the immutability that lets Read hand out
// stored pointers: a later Publish must replace the pointers, never rewrite a
// frame the renderer is already holding.
func TestPublishedFramesAreStable(t *testing.T) {
	var b Buffer
	b.Publish(&Frame{Tick: 1, Units: []UnitView{{DefID: 10}}})
	prev1, cur1, ok := b.Read()
	if !ok {
		t.Fatal("first publish produced no frame")
	}
	if prev1.Tick != 1 || cur1.Tick != 1 {
		t.Fatalf("first publish = prev %d cur %d, want 1/1", prev1.Tick, cur1.Tick)
	}

	b.Publish(&Frame{Tick: 2, Units: []UnitView{{DefID: 20}}})
	if cur1.Tick != 1 || cur1.Units[0].DefID != 10 {
		t.Fatal("a held frame changed under the reader")
	}
	prev2, cur2, _ := b.Read()
	if prev2.Tick != 1 || cur2.Tick != 2 {
		t.Fatalf("second publish = prev %d cur %d, want 1/2", prev2.Tick, cur2.Tick)
	}
}

// TestPublishCopiesInput: the caller may reuse the slices it passed.
func TestPublishCopiesInput(t *testing.T) {
	var b Buffer
	units := []UnitView{{DefID: 7}}
	b.Publish(&Frame{Tick: 1, Units: units})
	units[0].DefID = 99
	_, cur, _ := b.Read()
	if cur.Units[0].DefID != 7 {
		t.Fatal("Publish did not copy its input")
	}
}

func TestPublishCopiesExtendedFrameAndAppliesPresentationBounds(t *testing.T) {
	projectiles := make([]ProjectileView, MaxSnapshotProjectiles+1)
	projectiles[0].WeaponID = 7
	projectiles[len(projectiles)-1].WeaponID = 99
	effects := make([]EffectView, MaxSnapshotEffects+1)
	effects[0].ID = 11
	effects[len(effects)-1].ID = 22
	route := make([]RoutePoint, MaxSnapshotRoutePoints+1)
	route[0].X = 13
	route[len(route)-1].X = 26
	primary := []OrderView{{Index: 4, Route: route}}
	secondary := []OrderView{{Index: 9}}
	queues := []OrderQueueView{{Primary: primary, Secondary: secondary}}
	units := []UnitView{{Pieces: []PieceView{{Index: 2}}}}
	events := make([]EventView, MaxSnapshotEvents+1)
	events[0].Sequence = 31
	events[len(events)-1].Sequence = 62
	selection := SelectionView{Handles: []pool.Handle{pool.Handle(3)}}
	page := CommandPageView{ProductKeys: []string{"armcom", "kbot"}}
	visibility := VisibilityView{Version: 5, Explored: []byte{1, 2, 3}, Visible: []byte{4}, Radar: []byte{5}}

	frame := Frame{
		Units:       units,
		Projectiles: projectiles,
		Effects:     effects,
		Orders:      []OrderView{{Route: route}},
		OrderQueues: queues,
		Events:      events,
		Selection:   selection,
		CommandPage: page,
		Visibility:  visibility,
		Sounds:      []SoundEvent{{Alias: "fire", X: 17}},
	}
	var b Buffer
	b.Publish(&frame)

	// Reuse/mutate every nested input after Publish. The published copy must
	// retain values in producer order and must not expose a live slice.
	projectiles[0].WeaponID = 70
	effects[0].ID = 110
	route[0].X = 130
	primary[0].Index = 40
	events[0].Sequence = 310
	selection.Handles[0] = pool.Handle(30)
	page.ProductKeys[0] = "changed"
	visibility.Explored[0] = 9
	visibility.Visible[0] = 9
	visibility.Radar[0] = 9
	units[0].Pieces[0].Index = 20
	frame.Orders[0].Route[0].X = 1300
	frame.Sounds[0].Alias = "changed"

	_, got, ok := b.Read()
	if !ok {
		t.Fatal("extended frame was not published")
	}
	if len(got.Projectiles) != MaxSnapshotProjectiles || got.Projectiles[0].WeaponID != 7 || got.Projectiles[len(got.Projectiles)-1].WeaponID == 99 {
		t.Fatalf("projectile copy/bound = len %d first %d last %d", len(got.Projectiles), got.Projectiles[0].WeaponID, got.Projectiles[len(got.Projectiles)-1].WeaponID)
	}
	if !got.ProjectilesTruncated || !got.EffectsTruncated || !got.EventsTruncated {
		t.Fatal("bounded collections did not expose truncation")
	}
	if len(got.Effects) != MaxSnapshotEffects || got.Effects[0].ID != 11 {
		t.Fatalf("effect copy/bound = len %d first %d", len(got.Effects), got.Effects[0].ID)
	}
	if got.Units[0].Pieces[0].Index != 2 || got.Orders[0].Route[0].X != 13 {
		t.Fatal("unit pieces or top-level order route aliases input")
	}
	if got.OrderQueues[0].Primary[0].Index != 4 || len(got.OrderQueues[0].Primary[0].Route) != MaxSnapshotRoutePoints || got.OrderQueues[0].Primary[0].Route[0].X != 13 {
		t.Fatalf("queue/route copy = index %d route len %d first %d", got.OrderQueues[0].Primary[0].Index, len(got.OrderQueues[0].Primary[0].Route), got.OrderQueues[0].Primary[0].Route[0].X)
	}
	if !got.OrderQueues[0].Primary[0].RouteTruncated || got.OrderQueues[0].PrimaryTruncated || got.OrderQueuesTruncated || !got.Orders[0].RouteTruncated {
		t.Fatal("route/queue truncation signal was incorrect")
	}
	if len(got.OrderQueues[0].Secondary) != 1 || got.OrderQueues[0].Secondary[0].Index != 9 {
		t.Fatal("secondary queue was not preserved")
	}
	if len(got.Events) != MaxSnapshotEvents || got.Events[0].Sequence != 31 || got.Events[len(got.Events)-1].Sequence == 62 {
		t.Fatalf("event copy/bound = len %d first %d last %d", len(got.Events), got.Events[0].Sequence, got.Events[len(got.Events)-1].Sequence)
	}
	if got.Selection.Handles[0] != pool.Handle(3) || got.CommandPage.ProductKeys[0] != "armcom" || got.Visibility.Explored[0] != 1 || got.Visibility.Visible[0] != 4 || got.Visibility.Radar[0] != 5 || got.Sounds[0].Alias != "fire" {
		t.Fatal("nested selection, command, visibility, or sound state aliases input")
	}
}

func TestPublishPreservesProducerTruncationAndAdmissionDrops(t *testing.T) {
	frame := Frame{
		OrderQueues: []OrderQueueView{{
			Primary:          []OrderView{{Route: []RoutePoint{{X: 4}}, RouteTruncated: true}},
			PrimaryTruncated: true,
		}},
		Events:                 []EventView{{Sequence: 1}},
		EventsTruncated:        true,
		EventAdmissionsDropped: 7,
	}
	var b Buffer
	b.Publish(&frame)
	_, got, ok := b.Read()
	if !ok {
		t.Fatal("pre-truncated frame was not published")
	}
	if !got.EventsTruncated || got.EventAdmissionsDropped != 7 {
		t.Fatalf("event admission status = truncated %v dropped %d", got.EventsTruncated, got.EventAdmissionsDropped)
	}
	if !got.OrderQueues[0].PrimaryTruncated || !got.OrderQueues[0].Primary[0].RouteTruncated {
		t.Fatal("producer queue/route truncation was lost during Publish")
	}
}

// TestReadDoesNotAllocate is the R17 regression. Read used to deep-copy both
// frames on every call, so a 60 fps render loop allocated two full frames per
// rendered frame purely to avoid a race that immutability already prevents.
func TestReadDoesNotAllocate(t *testing.T) {
	var b Buffer
	b.Publish(&Frame{Tick: 1, Units: make([]UnitView, 256)})
	b.Publish(&Frame{Tick: 2, Units: make([]UnitView, 256)})
	if got := testing.AllocsPerRun(100, func() { b.Read() }); got != 0 {
		t.Fatalf("Read allocates %v times per call, want 0", got)
	}
}

// TestReadBeforePublish reports not-ok rather than a zero frame.
func TestReadBeforePublish(t *testing.T) {
	var b Buffer
	if prev, cur, ok := b.Read(); ok || prev != nil || cur != nil {
		t.Fatal("an unpublished buffer returned a frame")
	}
}

// TestLerpNeverExtrapolates locks C15/C16: alpha is clamped at both ends and
// NaN degrades to the previous frame.
func TestLerpNeverExtrapolates(t *testing.T) {
	prev, cur := numeric.Fixed(0), numeric.Fixed(100)
	if got := Lerp(prev, cur, -1); got != prev {
		t.Fatalf("alpha -1 = %d, want %d", got, prev)
	}
	if got := Lerp(prev, cur, 2); got != cur {
		t.Fatalf("alpha 2 = %d, want %d", got, cur)
	}
	if got := Lerp(prev, cur, 0.5); got != 50 {
		t.Fatalf("alpha 0.5 = %d, want 50", got)
	}
	nan := float32(0)
	nan = nan / nan
	if got := Lerp(prev, cur, nan); got != prev {
		t.Fatalf("NaN alpha = %d, want %d", got, prev)
	}
}
