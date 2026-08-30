package audiobackend

import "testing"

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
