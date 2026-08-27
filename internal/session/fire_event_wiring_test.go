package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestCombatStartEventsPublishOnceInOrderWithoutStateHashFeedback(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 17, 19)
	if s == nil || s.Combat == nil || s.Presentation == nil {
		t.Fatal("strict session did not compose combat presentation")
	}
	before := HashState(s)
	pos := combat.Vec3{X: numeric.FixedFromInt(12), Y: numeric.FixedFromInt(3), Z: numeric.FixedFromInt(18)}
	s.Combat.Events(combat.Event{Kind: combat.EventStartSound, Tick: 7, Source: 4, Position: pos, Sound: "sound/start.wav"})
	s.Combat.Events(combat.Event{Kind: combat.EventStartSmoke, Tick: 7, Source: 4, Target: 9, Position: pos})
	events := s.Presentation.Events()
	if len(events) != 2 {
		t.Fatalf("presentation events %d, want exactly 2: %+v", len(events), events)
	}
	if events[0].Kind != presentation.KindSound || events[0].Alias != "sound/start.wav" {
		t.Fatalf("start sound event %+v, want first and authored alias", events[0])
	}
	if events[1].Kind != presentation.KindSmokeStart || events[1].EffectID != uint32(pool.Handle(9)) {
		t.Fatalf("start smoke event %+v, want second and projectile handle 9", events[1])
	}
	if events[0].Sequence >= events[1].Sequence {
		t.Fatalf("event sequence %d then %d is not researched order", events[0].Sequence, events[1].Sequence)
	}
	if got := HashState(s); got != before {
		t.Fatalf("presentation callbacks changed authoritative state hash %s -> %s", before, got)
	}
}
