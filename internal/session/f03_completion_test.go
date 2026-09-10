package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// Allocation has no completion cue; only the builder's terminal order phase
// raises it, through the viewing-owner gate [05 "The build-order caption census"].
func TestAllocationDoesNotAnnounceCompletion(t *testing.T) {
	for _, owner := range []uint8{0, 1} {
		s := newLoopTestSession(t, 0)
		s.Audio = audio.NewService(nil)
		s.Clock.GlobalTick = 41
		def := s.Catalog.Units["armcom"]
		product, err := s.Units.CreateNanoframe(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if s.Units.Unit(product).Remaining == 0 {
			t.Fatal("fixture did not create an unfinished product")
		}
		if s.Audio.Queue.Count != 0 || len(statusEvents(s, uint8(audio.SlotUnitComplete))) != 0 {
			t.Fatalf("owner %d allocation announced completion", owner)
		}

		builder, err := s.Units.Create(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if s.Audio.Queue.Count != 0 {
			t.Fatal("already-built allocation announced completion")
		}
		u := s.Units.Unit(builder)
		s.bindOrderQueue(u)
		orders.NotifyStatus(u, uint8(audio.SlotUnitComplete), "Building complete")
		events := statusEvents(s, uint8(audio.SlotUnitComplete))
		if owner == s.ViewingOwner {
			if len(events) != 1 || events[0].Source != builder {
				t.Fatalf("terminal cue must identify the builder: %v", events)
			}
		} else if len(events) != 0 {
			t.Fatal("enemy completion bypassed the viewing-owner gate")
		}
	}
}
