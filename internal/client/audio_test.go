package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

type audioOutputSpy struct {
	plays int
}

type spatialAudioOutputSpy struct {
	audioOutputSpy
	mode audio.SoundMode
}

func (s *spatialAudioOutputSpy) SoundMode() audio.SoundMode { return s.mode }

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

func TestUpdateAudioViewportUsesBattleBeamAndSelectedMode(t *testing.T) {
	c, err := New(Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetAudioService(audio.NewService(nil))
	c.SetCamera(&camera.Camera{X: 10, Z: 20, ViewW: 640, ViewH: 480, MapW: 2048, MapH: 1024})
	old := audio.GlobalOutput()
	audio.SetGlobalOutput(&spatialAudioOutputSpy{mode: audio.SoundMode3D})
	t.Cleanup(func() { audio.SetGlobalOutput(old) })

	c.UpdateAudioViewportFromCamera()
	v := c.AudioViewport()
	if v.Left != 138 || v.Top != 52 || v.Width != 32 || v.Height != 26 {
		t.Fatalf("audio beam = %+v; want origin (138,52), cells 32x26", v)
	}
	if v.MapW != 128 || v.MapH != 64 || v.SoundMode != audio.SoundMode3D {
		t.Fatalf("audio map/mode = %+v; want map 128x64 and 3D", v)
	}
}
