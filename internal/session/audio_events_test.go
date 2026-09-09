package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type sessionAudioOutputSpy struct {
	plays int
}

func (s *sessionAudioOutputSpy) PlaySample(*audio.Sample, float64, float64) error {
	s.plays++
	return nil
}

func TestCommittedAudioSurvivesAudienceChangeBeforeDrain(t *testing.T) {
	s := &Session{
		Audio:       audio.NewService(nil),
		Clock:       &clock.State{GlobalTick: 7},
		Vis:         visibility.New(&world.Terrain{CellW: 2, CellH: 2}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled),
		publication: newPublicationState(frame.NewEventBuffer(frame.Limits{})),
	}
	cache := s.Audio.Cache
	if _, err := cache.Put("shot", []byte{128, 129}); err != nil {
		t.Fatal(err)
	}
	s.Audio.Registry.SetCache(cache)
	s.Vis.ByteGrid(0)[0] = 1
	if _, _, ok := s.EmitWeaponHit("shot", [3]numeric.Fixed{}, false); !ok {
		t.Fatal("audible event was not admitted")
	}
	s.Vis.ByteGrid(0)[0] = 0
	old := audio.GlobalOutput()
	spy := &sessionAudioOutputSpy{}
	audio.SetGlobalOutput(spy)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })
	s.Audio.DrainEvents(7, s.publication.events.SnapshotEvents())
	if spy.plays != 1 {
		t.Fatalf("admitted event was suppressed after audience changed: plays=%d", spy.plays)
	}
}
