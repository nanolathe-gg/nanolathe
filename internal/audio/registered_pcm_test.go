package audio

import (
	"bytes"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestRegisteredPCMIsRateKeyedAndSampleOwned(t *testing.T) {
	first := &Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{128, 255}}
	canonical := first.RegisteredPCM(44100)
	if want := ConvertSample(first, 1, 0, 44100); !bytes.Equal(canonical, want) {
		t.Fatal("registered PCM differs from centred unity conversion")
	}
	if again := first.RegisteredPCM(44100); len(again) == 0 || &again[0] != &canonical[0] {
		t.Fatal("same sample and rate did not reuse canonical PCM")
	}
	if otherRate := first.RegisteredPCM(22050); len(otherRate) == 0 || len(otherRate) == len(canonical) {
		t.Fatal("output rate did not select distinct converted PCM")
	}

	// Replacing an alias installs a distinct Sample, so no old canonical bytes
	// can cross the cache ownership boundary.
	replacement := &Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{128, 0}}
	if bytes.Equal(canonical, replacement.RegisteredPCM(44100)) {
		t.Fatal("replacement sample reused prior sample PCM")
	}
}

type registeredOutputSpy struct {
	outputSpy
	registered []outputPlay
}

func (s *registeredOutputSpy) PlayRegisteredSample(sample *Sample, volume, pan float64) error {
	s.registered = append(s.registered, outputPlay{sample: sample, volume: volume, pan: pan})
	return nil
}

func TestServiceUsesRegisteredOutputOnlyForModeZeroPaths(t *testing.T) {
	s := NewService(testAudioFS(t, "shot"))
	old := GlobalOutput()
	spy := &registeredOutputSpy{}
	SetGlobalOutput(spy)
	t.Cleanup(func() { SetGlobalOutput(old) })

	if !s.PlayUICue("shot") {
		t.Fatal("UI cue did not resolve")
	}
	if _, _, ok := s.PlayPositional("shot", [3]numeric.Fixed{}, func([3]numeric.Fixed) bool { return true }); !ok {
		t.Fatal("positional cue did not admit")
	}
	if !s.PlayBriefing("shot", "", "", "") {
		t.Fatal("briefing alias did not resolve")
	}
	if len(spy.registered) != 3 || len(spy.plays) != 0 {
		t.Fatalf("mode-0 dispatch registered=%d ordinary=%d, want 3/0", len(spy.registered), len(spy.plays))
	}

	// Queue resolution is mode 1: it deliberately keeps the normal output path
	// even though the sample cache supplies its decoded input.
	s.Queue.Register(1, &Category{Rows: [24]Row{SlotSelect: {Variants: []string{"shot"}}}}, "unit", true)
	s.Emit(30, SlotSelect, 1, "")
	s.DrainEvents(30, nil)
	if len(spy.plays) != 1 || len(spy.registered) != 3 {
		t.Fatalf("mode-1 dispatch registered=%d ordinary=%d, want 3/1", len(spy.registered), len(spy.plays))
	}

}
