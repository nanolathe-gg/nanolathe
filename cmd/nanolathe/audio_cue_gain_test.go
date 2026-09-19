package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type cueGainSpy struct{ gains []float64 }

func (s *cueGainSpy) PlaySample(_ *audio.Sample, gain, _ float64) error {
	s.gains = append(s.gains, gain)
	return nil
}

// The four ordinary producers share the base attenuation [03 R-AUD-01 §1].
// FX gain belongs to the output stage, so changing its admission input must
// not scale the base a second time in the producer or voice queue.
func TestOrdinaryCueProducersShareBaseGain(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sounds"), 0755); err != nil {
		t.Fatal(err)
	}
	// Independently authored raw unsigned PCM [fmt wav].
	if err := os.WriteFile(filepath.Join(root, "sounds", "explode.wav"), []byte{128, 129, 127, 128}, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	previous := audio.GlobalOutput()
	t.Cleanup(func() { audio.SetGlobalOutput(previous) })
	for _, fx := range []float32{1, 0.5} {
		svc := audio.NewService(fs)
		svc.Registry.RegisterPath("click", "sounds/explode.wav")
		svc.Queue.Register(1, &audio.Category{Rows: [24]audio.Row{1: {Variants: []string{"explode"}}}}, "unit", true)
		svc.Queue.ConfigureBackendGates(fx, 0x47, true)
		spy := &cueGainSpy{}
		audio.SetGlobalOutput(spy)
		if !svc.PlayUICue("click") {
			t.Fatal("UI cue was not loaded")
		}
		if !svc.Emit(30, audio.SlotSelect, 1, "") {
			t.Fatal("voice cue was not admitted")
		}
		svc.DrainEvents(30, nil)
		prefs := settings.DefaultAudio()
		prefs.FXVol = int(fx * 64)
		shell := &gameShell{cs: testContentSet(fs), audioOwner: svc, frontendAliasesBound: true, audioPrefs: prefs}
		shell.playRetailSoundTest()
		svc.SetViewport(audio.Viewport{Width: 40, Height: 30})
		pos := [3]numeric.Fixed{320 * 65536, 0, 240 * 65536}
		if _, _, ok := svc.PlayPositional("click", pos, func([3]numeric.Fixed) bool { return true }); !ok {
			t.Fatal("centered world cue was not admitted")
		}
		if len(spy.gains) != 4 {
			t.Fatalf("FX=%g: got %d producers, want UI, voice, TEST, world", fx, len(spy.gains))
		}
		want := math.Pow(10, -5.85/20)
		for i, gain := range spy.gains {
			if math.Abs(gain-want) > 1e-12 {
				t.Errorf("FX=%g producer %s base gain=%g, want %g", fx, []string{"UI", "voice", "TEST", "world"}[i], gain, want)
			}
		}
	}
}
