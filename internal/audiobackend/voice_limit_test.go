package audiobackend

import (
	"fmt"
	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
	"io"
	"testing"
)

// Admission uses the configured limit independently of the fixed tracking
// capacity [03 R-AUD-01 §1 steps 2,7]. Only the host player is substituted.
func TestConfiguredVoiceLimitAndTracking(t *testing.T) {
	for _, limit := range []int{1, 3, 32, 33} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			b := New()
			var made []*observedPlayer
			b.createPlayer = func(io.Reader) (outputPlayer, error) { p := &observedPlayer{}; made = append(made, p); return p, nil }
			b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: limit})
			sample := &retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{192}}
			for i := 0; i < limit+1; i++ {
				if err := b.PlayRegisteredSample(sample, 1, 0); err != nil {
					t.Fatal(err)
				}
			}
			if limit <= 32 {
				if len(b.players) != limit || made[0].playing || made[0].stops != 1 || !made[1].playing {
					t.Fatal("configured limit did not steal oldest live voice")
				}
			} else {
				if len(b.players) != 32 || !made[0].playing || !made[32].playing || !made[33].playing {
					t.Fatal("tracking capacity incorrectly became a playback limit")
				}
				b.SetEffectsVolume(0.25)
				if made[32].volume != 0.25 || made[33].volume != 0.25 {
					t.Fatal("shared output gain missed untracked voices")
				}
				b.SetMasterEnabled(false)
				if made[0].playing || !made[32].playing || !made[33].playing {
					t.Fatal("tracked stop-all crossed into untracked voices")
				}
				made[32].playing = false
				b.reapLocked()
				if len(b.untracked) != 1 || b.untracked[0].player != made[33] {
					t.Fatal("completed untracked host reference retained")
				}
				b.Close()
				if made[33].playing {
					t.Fatal("application close retained untracked playback")
				}
			}
		})
	}
}

func TestVoiceLimitReapsAndAppliesReductionOnNextPlay(t *testing.T) {
	b := New()
	var made []*observedPlayer
	b.createPlayer = func(io.Reader) (outputPlayer, error) { p := &observedPlayer{}; made = append(made, p); return p, nil }
	config := retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 3}
	b.ConfigureOutput(config)
	sample := &retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{192}}
	play := func() {
		t.Helper()
		if err := b.PlaySample(sample, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	play()
	play()
	play()
	made[1].playing = false
	play()
	if made[0].stops != 0 || len(b.players) != 3 {
		t.Fatal("finished voice did not free capacity before stealing")
	}
	config.MixingBuffers = 1
	b.ConfigureOutput(config)
	if len(b.players) != 3 || !made[0].playing {
		t.Fatal("setting change stole before next admission")
	}
	play()
	if len(b.players) != 1 || b.players[0].player != made[4] || made[0].playing || made[2].playing || made[3].playing {
		t.Fatal("lowered limit did not release old voices on next admission")
	}
}
