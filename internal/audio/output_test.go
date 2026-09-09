package audio

import "testing"

type outputPlay struct {
	sample *Sample
	volume float64
	pan    float64
}

type outputSpy struct {
	plays []outputPlay
}

func (s *outputSpy) PlaySample(sample *Sample, volume, pan float64) error {
	s.plays = append(s.plays, outputPlay{sample: sample, volume: volume, pan: pan})
	return nil
}

type configuredOutputSpy struct {
	outputSpy
	configs []OutputConfig
}

func (s *configuredOutputSpy) ConfigureOutput(config OutputConfig) {
	s.configs = append(s.configs, config)
}

func TestConfigureOutputRetainsStateUntilOutputInstallation(t *testing.T) {
	previous := GlobalOutput()
	defer func() {
		ConfigureOutput(OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: SoundModeMono})
		SetGlobalOutput(previous)
	}()
	SetGlobalOutput(nil)
	want := OutputConfig{MasterEnabled: true, EffectsVolume: 0.5, SoundMode: SoundMode3D, MixingBuffers: 3}
	ConfigureOutput(want)
	spy := &configuredOutputSpy{}
	SetGlobalOutput(spy)
	if len(spy.configs) != 1 || spy.configs[0] != want {
		t.Fatalf("delayed output config = %#v, want %#v", spy.configs, want)
	}
	updated := OutputConfig{MasterEnabled: false, EffectsVolume: 0, SoundMode: SoundModeMono, MixingBuffers: 32}
	ConfigureOutput(updated)
	if len(spy.configs) != 2 || spy.configs[1] != updated {
		t.Fatalf("live output config = %#v, want %#v", spy.configs, updated)
	}
}

func TestToggleOutput3DPreservesRetainedConfiguration(t *testing.T) {
	previous := GlobalOutput()
	defer func() {
		ConfigureOutput(OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: SoundModeMono, MixingBuffers: 8})
		SetGlobalOutput(previous)
	}()
	spy := &configuredOutputSpy{}
	SetGlobalOutput(spy)
	want := OutputConfig{MasterEnabled: false, EffectsVolume: 0.25, SoundMode: SoundModeMono, MixingBuffers: 3}
	ConfigureOutput(want)

	want.SoundMode = SoundMode3D
	if got := ToggleOutput3D(); got != SoundMode3D || spy.configs[len(spy.configs)-1] != want {
		t.Fatalf("3D toggle = %v/%#v, want %#v", got, spy.configs[len(spy.configs)-1], want)
	}
	want.SoundMode = SoundModeMono
	if got := ToggleOutput3D(); got != SoundModeMono || spy.configs[len(spy.configs)-1] != want {
		t.Fatalf("mono toggle = %v/%#v, want %#v", got, spy.configs[len(spy.configs)-1], want)
	}
}

type streamOutputSpy struct {
	outputSpy
	streamPlays  []*Sample
	streamVolume []float64
	streamStops  int
}

func (s *streamOutputSpy) PlayStream(sample *Sample, volume float64) error {
	s.streamPlays = append(s.streamPlays, sample)
	s.streamVolume = append(s.streamVolume, volume)
	return nil
}

func (s *streamOutputSpy) StopStream() { s.streamStops++ }
