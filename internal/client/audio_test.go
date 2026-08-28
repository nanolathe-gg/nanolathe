package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

type audioOutputSpy struct {
	plays int
}

func (s *audioOutputSpy) PlaySample(*audio.Sample, float64, float64) error {
	s.plays++
	return nil
}

func TestPlayPositionalRequiresVisibilityPredicate(t *testing.T) {
	_, err := New(Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	service := audio.NewService(nil)
	if _, err := service.Cache.Put("pos_alias", []byte{128, 128}); err != nil {
		t.Fatal(err)
	}
	service.SetViewport(audio.Viewport{})
	spy := &audioOutputSpy{}
	old := audio.GlobalOutput()
	audio.SetGlobalOutput(spy)
	defer audio.SetGlobalOutput(old)

	pos := [3]numeric.Fixed{}
	if _, _, ok := service.PlayPositional("pos_alias", pos, nil); ok {
		t.Fatal("positional audio should fail closed without a visibility predicate")
	}
	if spy.plays != 0 {
		t.Fatalf("positional audio without a visibility predicate played %d times", spy.plays)
	}

	// Other positional contracts may opt into an explicit always-audible test
	// predicate; this must continue to exercise pan/attenuation and playback.
	if _, _, ok := service.PlayPositional("pos_alias", pos, func([3]numeric.Fixed) bool { return true }); !ok {
		t.Fatal("explicit visibility predicate should admit positional audio")
	}
	if spy.plays != 1 {
		t.Fatalf("explicitly audible positional audio played %d times, want 1", spy.plays)
	}
}
