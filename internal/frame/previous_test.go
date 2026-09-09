package frame

import "testing"

// The Enhanced presentation blends the two most recent committed ticks, so the
// buffer must report the older one exactly while it exists: never before the
// second publication, and never while the writer is refilling that same slot
// (docs/DESIGN_GPU_RENDERER.md §13.5).
func TestPreviousFollowsPublicationSequence(t *testing.T) {
	b := NewBuffer()
	if b.Previous() != nil {
		t.Fatal("a buffer with no publication reported a previous frame")
	}

	b.BeginWrite()
	if err := b.Publish(1); err != nil {
		t.Fatalf("publish tick 1: %v", err)
	}
	if b.Previous() != nil {
		t.Fatal("the first publication reported a previous frame")
	}

	b.BeginWrite()
	if err := b.Publish(2); err != nil {
		t.Fatalf("publish tick 2: %v", err)
	}
	prev := b.Previous()
	if prev == nil || prev.Tick != 1 {
		t.Fatalf("previous after the second publication = %v, want tick 1", prev)
	}
	if cur := b.Current(); cur == nil || cur.Tick != 2 {
		t.Fatalf("current after the second publication = %v, want tick 2", cur)
	}
	if prev == b.Current() {
		t.Fatal("previous and current name the same slot")
	}

	b.BeginWrite()
	if b.Previous() != nil {
		t.Fatal("a pending write left the previous slot readable")
	}
	if err := b.Publish(3); err != nil {
		t.Fatalf("publish tick 3: %v", err)
	}
	prev = b.Previous()
	if prev == nil || prev.Tick != 2 {
		t.Fatalf("previous after the third publication = %v, want tick 2", prev)
	}
}

// A rejected publication leaves the pending write open, so the older tick stays
// unavailable until the writer succeeds.
func TestPreviousStaysUnavailableAfterRejectedPublish(t *testing.T) {
	b := NewBuffer()
	b.BeginWrite()
	if err := b.Publish(1); err != nil {
		t.Fatalf("publish tick 1: %v", err)
	}
	b.BeginWrite()
	if err := b.Publish(2); err != nil {
		t.Fatalf("publish tick 2: %v", err)
	}
	b.BeginWrite()
	if err := b.Publish(2); err == nil {
		t.Fatal("a non-monotonic publication was accepted")
	}
	if b.Previous() != nil {
		t.Fatal("a rejected publication left the previous slot readable")
	}
}
