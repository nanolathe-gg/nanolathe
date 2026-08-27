package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestCombatStartEventsPublishOnceInOrderWithoutStateHashFeedback(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 17, 19)
	if s == nil || s.Combat == nil || s.publication == nil || s.publication.events == nil {
		t.Fatal("strict session did not compose combat presentation")
	}
	before := HashState(s)
	pos := combat.Vec3{X: numeric.FixedFromInt(12), Y: numeric.FixedFromInt(3), Z: numeric.FixedFromInt(18)}
	s.Combat.Events(combat.Event{Kind: combat.EventStartSound, Tick: 7, Source: 4, Position: pos, Sound: "sound/start.wav"})
	s.Combat.Events(combat.Event{Kind: combat.EventStartSmoke, Tick: 7, Source: 4, Target: 9, Position: pos})
	events := s.publication.events.Events()
	if len(events) != 2 {
		t.Fatalf("presentation events %d, want ordered audio and visual cues: %+v", len(events), events)
	}
	if events[0].Kind != frame.KindAudio || events[0].Sound != "sound/start.wav" || !events[0].AudioPositional {
		t.Fatalf("start audio event %+v, want positional sound", events[0])
	}
	if events[1].Kind != frame.KindSmokeStart || events[1].EffectID != uint32(pool.Handle(9)) {
		t.Fatalf("start smoke event %+v, want visual smoke and projectile handle 9", events[1])
	}
	if got := HashState(s); got != before {
		t.Fatalf("presentation callbacks changed authoritative state hash %s -> %s", before, got)
	}
}
