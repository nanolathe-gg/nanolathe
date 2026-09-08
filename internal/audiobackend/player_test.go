package audiobackend

import (
	"testing"

	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
)

func TestBackendIsLazy(t *testing.T) {
	b := New()
	if b == nil || b.SampleRate() != 44100 {
		t.Fatalf("backend sample rate = %v, want 44100", b)
	}
	if b.ctx != nil {
		t.Fatal("backend should create its device lazily")
	}
	if !b.Capabilities().Device || !b.Capabilities().Stereo {
		t.Fatal("real backend should advertise device and stereo output")
	}
	if got := b.SoundMode(); got != retailaudio.SoundModeMono {
		t.Fatalf("default sound mode = %v, want Mono", got)
	}
	b.SetSoundMode(retailaudio.SoundMode3D)
	if got := b.SoundMode(); got != retailaudio.SoundMode3D {
		t.Fatalf("set sound mode = %v, want 3D", got)
	}
}

// TestBackendWarmUpInstallsDevice locks that WarmUp actually reaches the
// point of creating the host device context, rather than being a no-op that
// leaves the same lazy-until-first-cue behavior TestBackendIsLazy checks
// above. This is a functional check only (no timing assertion, which would
// be flaky across hosts and CI sandboxes without real audio hardware); the
// measured latency this closes is reported in the WU-19-224 commit message,
// not locked here [platform work, not a retail contract].
func TestBackendWarmUpInstallsDevice(t *testing.T) {
	b := New()
	b.WarmUp()
	if b.ctx == nil {
		t.Fatal("WarmUp did not install the host audio context")
	}
}

func TestClampPlayback(t *testing.T) {
	tests := []struct {
		volume, pan float64
		wantVolume  float64
		wantPan     float64
	}{
		{volume: -0.25, pan: -2, wantVolume: 0, wantPan: -1},
		{volume: 0.5, pan: 0.25, wantVolume: 0.5, wantPan: 0.25},
		{volume: 1.5, pan: 2, wantVolume: 1, wantPan: 1},
	}
	for _, test := range tests {
		volume, pan := clampPlayback(test.volume, test.pan)
		if volume != test.wantVolume || pan != test.wantPan {
			t.Fatalf("clampPlayback(%v, %v) = %v, %v; want %v, %v", test.volume, test.pan, volume, pan, test.wantVolume, test.wantPan)
		}
	}
}
