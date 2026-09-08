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
