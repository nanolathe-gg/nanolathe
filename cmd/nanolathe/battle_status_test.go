package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// TestSpeedMessageLifetimeFollowsSharedRingTextScroll locks that the
// game-speed announcement ages out on the shared message ring's own
// TextScroll — the field the INTERFACE page's TXTSCROL slider writes through
// ConfigureMessageLines — rather than on a second ring's default
// [07 R-HUD-03 §14.3][07 R-CAM-01 §3]. The bound is (textscroll + 1) x 30
// ticks after the stored tick and the expiry compare is strict.
func TestSpeedMessageLifetimeFollowsSharedRingTextScroll(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	if b.cl == nil {
		t.Fatal("fixture battle has no installed presentation client")
	}
	// A non-default textscroll (5, not the ring's own default of 10)
	// distinguishes "read from the configured shared ring" from "read from a
	// hardcoded value or an unconfigured second ring".
	b.cl.ConfigureMessageLines(10, 5)
	ring := b.messageRing()
	if ring.TextScroll != 5 {
		t.Fatalf("messageRing().TextScroll = %d, want 5 (the client's ring, shared)", ring.TextScroll)
	}

	b.sess.Snapshot = frame.NewBuffer()
	b.sess.Snapshot.BeginWrite()
	if err := b.sess.Snapshot.Publish(300); err != nil {
		t.Fatalf("publish fixture frame: %v", err)
	}

	b.sess.Clock.Requested, b.sess.Clock.Active = 10, 10
	b.setGameSpeed(1)
	if got := ring.Visible(); len(got) != 1 || got[0].StoredTick != 300 {
		t.Fatalf("visible ring after the announcement = %+v, want one line stored at tick 300", got)
	}

	deadline := uint32(300 + (5+1)*30)
	ring.Expire(deadline)
	if len(ring.Visible()) != 1 {
		t.Fatalf("announcement expired at its own deadline %d; the bound is inclusive", deadline)
	}
	ring.Expire(deadline + 1)
	if len(ring.Visible()) != 0 {
		t.Fatalf("announcement still visible one tick past deadline %d with textscroll 5", deadline)
	}
}

// newestRingText is the newest visible line of the shared message ring, or ""
// when the ring shows nothing. The announcement has no other representation.
func newestRingText(b *battleSession) string {
	lines := b.messageRing().Visible()
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1].Text
}
