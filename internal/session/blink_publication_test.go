package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestBlinkByteReachesTheCommittedContact locks the publication half of the
// damage flash [06 R-WPN-04 §2][03 §3.9]. The sim record holds the byte signed
// (the sweep steps -16 up to zero); retail's minimap reads an unsigned byte and
// tests it against zero only, so the committed contact carries the same 240 the
// damage dispatcher wrote. Presentation must never have to recover the value
// from live session state [I6].
//
// This was the missing link: the frame field and the contacts pass's blink gate
// both existed, but nothing wrote the field, so no unit ever blinked.
func TestBlinkByteReachesTheCommittedContact(t *testing.T) {
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	s := visibilityFixture(t, true)
	s.Vis.SetLocal(visibility.PlayerID(1))
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	h, err := s.Units.Create(def, 1, cell(128), 0, cell(128))
	if err != nil {
		t.Fatalf("create local unit: %v", err)
	}
	publishVisibilityForAll(s)

	publishedByte := func(tick uint32) uint8 {
		t.Helper()
		s.publishSnapshot(tick)
		c, ok := radarContactFor(s.Snapshot.Current(), h)
		if !ok {
			t.Fatalf("tick %d: the unit publishes no radar contact", tick)
		}
		return c.BlinkSuppress
	}

	if got := publishedByte(1); got != 0 {
		t.Fatalf("an unhit unit publishes blink byte %d, want 0 [06 R-WPN-04 §2]", got)
	}
	// The value the damage dispatcher writes, read back through the frame as
	// retail's unsigned 240.
	s.Units.Unit(h).BlinkSuppress = -16
	if got := publishedByte(2); got != 240 {
		t.Fatalf("a freshly hit unit publishes blink byte %d, want 240 [06 R-WPN-04 §2]", got)
	}
	// One visit from the end of the blink the contact is still nonzero, and the
	// visit that lands on zero publishes zero — the gate is a zero test, so those
	// two states are the whole contract [03 §3.9].
	s.Units.Unit(h).BlinkSuppress = -1
	if got := publishedByte(3); got == 0 {
		t.Fatal("the last blinking visit published a zero blink byte [03 §3.9]")
	}
	s.Units.Unit(h).BlinkSuppress = 0
	if got := publishedByte(4); got != 0 {
		t.Fatalf("a spent blink publishes %d, want 0 [03 §3.9]", got)
	}
}

// Every completed catch-up sub-tick publishes its phase; an idle host call
// publishes nothing and cannot advance the countdown [01 R-CORE-03][I6].
func TestBlinkPhasePublishedAtCompletedSubticks(t *testing.T) {
	s := visibilityFixture(t, true)
	s.State = StateBattle
	s.resetRadarBlink()
	var ticks []uint32
	s.SetPublicationObserver(func(f *frame.Frame) {
		ticks = append(ticks, f.Tick)
		want := uint8((f.Tick / 8) & 1)
		if f.Radar.BlinkPhase != want || f.Radar.BlinkPhase != s.RadarBlinkPhase() {
			t.Fatalf("tick %d published phase=%d, want %d", f.Tick, f.Radar.BlinkPhase, want)
		}
	})
	// Four host calls each consume four sub-ticks, crossing both toggle edges.
	for now := int32(4); now <= 16; now += 4 {
		s.Step(now)
	}
	if len(ticks) != 16 || ticks[7] != 8 || ticks[15] != 16 {
		t.Fatalf("published ticks=%v", ticks)
	}
	before := s.Snapshot.Current()
	countdown := s.radarBlinkCountdown
	s.Step(16)
	if len(ticks) != 16 || s.Snapshot.Current() != before || s.radarBlinkCountdown != countdown {
		t.Fatal("idle host call advanced or republished blink")
	}
}
