package frame

import "testing"

func publishEvents(t *testing.T, b *Buffer, tick uint32, ids ...uint32) {
	t.Helper()
	f := b.BeginWrite()
	for _, id := range ids {
		f.Events = append(f.Events, EventView{ID: id, Tick: tick, Kind: EventKindAudio})
	}
	if err := b.Publish(tick); err != nil {
		t.Fatalf("publish tick %d: %v", tick, err)
	}
}

// TestRetainedEventsSurviveSupersededPublications locks the retention contract
// of [03 R-AUD-01 §7]: the two slots carry state a later tick may supersede,
// but every committed tick's events reach the drain exactly once, oldest tick
// first and in raise order within a tick.
func TestRetainedEventsSurviveSupersededPublications(t *testing.T) {
	b := &Buffer{}
	publishEvents(t, b, 1, 10, 11)
	publishEvents(t, b, 2)
	publishEvents(t, b, 3, 12)
	if got := b.PendingCommittedEvents(); got != 3 {
		t.Fatalf("pending = %d, want the three events of ticks 1..3", got)
	}
	got := b.DrainCommittedEvents(nil)
	if len(got) != 3 || got[0].ID != 10 || got[1].ID != 11 || got[2].ID != 12 {
		t.Fatalf("drained %#v, want ids 10,11,12 in raise order", got)
	}
	if got[0].Tick != 1 || got[2].Tick != 3 {
		t.Fatalf("drained ticks = %d,%d, want each event to keep its own raise tick", got[0].Tick, got[2].Tick)
	}
	if again := b.DrainCommittedEvents(got); len(again) != 0 {
		t.Fatalf("second drain returned %d events, want none [03 R-AUD-01 §7]", len(again))
	}
}

// TestRetainedEventsDetachFromTheReusedSlot locks the copy: the frame slot an
// event was published from is reset and reused by the next BeginWrite, so a
// retained event that aliased it would be silently rewritten.
func TestRetainedEventsDetachFromTheReusedSlot(t *testing.T) {
	b := &Buffer{}
	f := b.BeginWrite()
	f.Events = append(f.Events, EventView{ID: 7, Tick: 1, Kind: EventKindImpact, DurationsA: []int32{3, 4}})
	if err := b.Publish(1); err != nil {
		t.Fatal(err)
	}
	publishEvents(t, b, 2)
	publishEvents(t, b, 3)
	got := b.DrainCommittedEvents(nil)
	if len(got) != 1 || got[0].ID != 7 || len(got[0].DurationsA) != 2 ||
		got[0].DurationsA[0] != 3 || got[0].DurationsA[1] != 4 {
		t.Fatalf("retained event = %#v, want an intact detached copy", got)
	}
}

// TestRetainedEventsAreBounded locks the presentation-only safety bound: an
// unconsumed queue cannot grow without limit, the overflow drops the newest
// submissions rather than rewriting the accepted history, and the drop is
// reported instead of being silent.
func TestRetainedEventsAreBounded(t *testing.T) {
	b := &Buffer{}
	tick := uint32(1)
	for b.PendingCommittedEvents() < retainedEventCapacity {
		publishEvents(t, b, tick, 1, 2, 3, 4, 5, 6, 7, 8)
		tick++
	}
	if dropped, overflow := b.RetainedEventsDropped(); dropped != 0 || overflow {
		t.Fatalf("dropped=%d overflow=%v before the bound was exceeded", dropped, overflow)
	}
	first := b.DrainCommittedEvents(nil)[0].Tick
	if first != 1 {
		t.Fatalf("oldest retained tick = %d, want 1", first)
	}
	for b.PendingCommittedEvents() < retainedEventCapacity {
		publishEvents(t, b, tick, 1, 2, 3, 4, 5, 6, 7, 8)
		tick++
	}
	publishEvents(t, b, tick, 99)
	if got := b.PendingCommittedEvents(); got != retainedEventCapacity {
		t.Fatalf("pending = %d, want the bound %d", got, retainedEventCapacity)
	}
	if dropped, overflow := b.RetainedEventsDropped(); dropped != 1 || !overflow {
		t.Fatalf("dropped=%d overflow=%v, want one reported drop", dropped, overflow)
	}
}
