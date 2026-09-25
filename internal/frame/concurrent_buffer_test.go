package frame

import (
	"sync"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func publishTick(t *testing.T, b *Buffer, tick uint32, units int) {
	t.Helper()
	f := b.BeginWrite()
	for i := 0; i < units; i++ {
		f.Units = append(f.Units, UnitView{Slot: pool.Handle(tick)})
	}
	if err := b.Publish(tick); err != nil {
		t.Fatalf("publish %d: %v", tick, err)
	}
}

// A pinned publication survives any number of later publications, and the
// writer keeps rotating through the slots nobody holds.
func TestPinnedPublicationSurvivesRotation(t *testing.T) {
	b := NewBuffer()
	b.SetConcurrentReaders()
	for tick := uint32(1); tick <= 5; tick++ {
		publishTick(t, b, tick, 3)
	}
	cur, prev, ok := b.PinTick(4)
	if !ok || cur.Tick != 4 || prev == nil || prev.Tick != 3 {
		t.Fatalf("PinTick(4) = %v %v %v", cur, prev, ok)
	}
	for tick := uint32(6); tick <= 40; tick++ {
		publishTick(t, b, tick, 3)
	}
	if cur.Tick != 4 || prev.Tick != 3 || cur.Units[0].Slot != 4 || prev.Units[0].Slot != 3 {
		t.Fatalf("pinned pair was rewritten: cur %d/%d prev %d/%d", cur.Tick, cur.Units[0].Slot, prev.Tick, prev.Units[0].Slot)
	}
	b.Unpin(cur)
	b.Unpin(prev)
	// Released slots hold the oldest publications, so they are the writer's
	// next choices; a full rotation later the old ticks are gone.
	for tick := uint32(41); tick <= 41+concurrentBufferSlots; tick++ {
		publishTick(t, b, tick, 3)
	}
	if latest := b.Current(); latest == nil || latest.Tick != 41+concurrentBufferSlots {
		t.Fatalf("current = %v, want tick %d", latest, 41+concurrentBufferSlots)
	}
	if _, _, ok := b.PinTick(4); ok {
		t.Fatal("a released old tick survived a full rotation")
	}
}

// The paused-input boundary republishes a tick; the pinned pair then has no
// blendable previous frame, exactly as the unpinned Previous reports.
func TestPinAfterRepublishHasNoPrevious(t *testing.T) {
	b := NewBuffer()
	b.SetConcurrentReaders()
	publishTick(t, b, 1, 1)
	publishTick(t, b, 2, 1)
	b.BeginWrite()
	if err := b.Republish(2); err != nil {
		t.Fatalf("republish: %v", err)
	}
	cur, prev := b.PinLatest()
	if cur == nil || cur.Tick != 2 || prev != nil {
		t.Fatalf("PinLatest after republish = %v %v, want tick 2 and no previous", cur, prev)
	}
	if b.Previous() != nil {
		t.Fatal("unpinned Previous paired a republished tick with itself")
	}
	b.Unpin(cur)
}

// A host that joins a writer observes every publication it made, in order,
// while the rotation still holds them.
func TestPublicationsSinceVisitsInOrder(t *testing.T) {
	b := NewBuffer()
	b.SetConcurrentReaders()
	publishTick(t, b, 1, 1)
	seen := b.PublicationSeq()
	for tick := uint32(2); tick <= 6; tick++ {
		publishTick(t, b, tick, 1)
	}
	var ticks []uint32
	seen = b.PublicationsSince(seen, func(f *Frame) { ticks = append(ticks, f.Tick) })
	if len(ticks) != 5 || ticks[0] != 2 || ticks[4] != 6 || seen != b.PublicationSeq() {
		t.Fatalf("observed %v (seq %d), want ticks 2..6", ticks, seen)
	}
	if n := b.PublicationsSince(seen, func(*Frame) { t.Fatal("revisited a publication") }); n != seen {
		t.Fatalf("second observation advanced to %d", n)
	}
}

// A reader pinning beside a writer on another goroutine always reads one
// consistent publication. Run with -race.
func TestPinnedReaderBesideWriter(t *testing.T) {
	b := NewBuffer()
	b.SetConcurrentReaders()
	publishTick(t, b, 1, 64)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for tick := uint32(2); tick <= 3000; tick++ {
			f := b.BeginWrite()
			for i := 0; i < 64; i++ {
				f.Units = append(f.Units, UnitView{Slot: pool.Handle(tick)})
			}
			if err := b.Publish(tick); err != nil {
				t.Errorf("publish %d: %v", tick, err)
				return
			}
		}
	}()
	for i := 0; i < 3000; i++ {
		cur, prev := b.PinLatest()
		for _, u := range cur.Units {
			if uint32(u.Slot) != cur.Tick {
				t.Fatalf("pinned frame %d holds a unit from tick %d", cur.Tick, u.Slot)
			}
		}
		if prev != nil {
			for _, u := range prev.Units {
				if uint32(u.Slot) != prev.Tick {
					t.Fatalf("pinned previous %d holds a unit from tick %d", prev.Tick, u.Slot)
				}
			}
		}
		b.Unpin(cur)
		b.Unpin(prev)
	}
	wg.Wait()
}
