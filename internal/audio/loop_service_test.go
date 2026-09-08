package audio

import "testing"

type loopingRegisteredOutputSpy struct {
	registeredOutputSpy
	loops []outputPlay
}

func (s *loopingRegisteredOutputSpy) PlayLoopingRegisteredSample(sample *Sample, volume, pan float64) error {
	s.loops = append(s.loops, outputPlay{sample: sample, volume: volume, pan: pan})
	return nil
}

func TestServiceLoopingUICueRequiresLoopingRegisteredOutput(t *testing.T) {
	s := NewService(testAudioFS(t, "bgm"))
	old := GlobalOutput()
	t.Cleanup(func() { SetGlobalOutput(old) })

	plain := &registeredOutputSpy{}
	SetGlobalOutput(plain)
	if s.PlayLoopingUICue("bgm") || len(plain.registered) != 0 || len(plain.plays) != 0 {
		t.Fatal("looping cue fell back through a non-looping output")
	}

	looping := &loopingRegisteredOutputSpy{}
	SetGlobalOutput(looping)
	if !s.PlayLoopingUICue("bgm") {
		t.Fatal("looping cue did not resolve through looping output")
	}
	if len(looping.loops) != 1 || len(looping.registered) != 0 || len(looping.plays) != 0 {
		t.Fatalf("loop dispatch = loops=%d registered=%d ordinary=%d, want 1/0/0", len(looping.loops), len(looping.registered), len(looping.plays))
	}
}
