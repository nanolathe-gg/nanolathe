package audiobackend

import (
	"errors"
	"io"
	"testing"
	"time"

	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
)

type staticPlayer struct {
	reader      *panReader
	playing     bool
	position    time.Duration
	volume      float64
	starts      int
	stops       int
	rewindPans  []float64
	rewindError []error
}

func (p *staticPlayer) Play()                   { p.playing = true; p.starts++ }
func (p *staticPlayer) IsPlaying() bool         { return p.playing }
func (p *staticPlayer) Position() time.Duration { return p.position }
func (p *staticPlayer) SetVolume(v float64)     { p.volume = v }
func (p *staticPlayer) PauseAndStopReading()    { p.playing = false; p.stops++ }
func (p *staticPlayer) Rewind() error {
	p.rewindPans = append(p.rewindPans, p.reader.pan)
	p.position = 0
	if len(p.rewindError) == 0 {
		return nil
	}
	err := p.rewindError[0]
	p.rewindError = p.rewindError[1:]
	return err
}

func newStaticBackend() (*Backend, *[]*staticPlayer) {
	b := New()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 32})
	made := new([]*staticPlayer)
	b.createPlayer = func(source io.Reader) (outputPlayer, error) {
		p := &staticPlayer{reader: source.(*panReader)}
		*made = append(*made, p)
		return p, nil
	}
	return b, made
}

func staticTestSample() *retailaudio.Sample {
	return &retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{128, 192}}
}

// Slots are filled 0,3,2,1; idle reuse wins before cursor comparison and a
// strict tie retains the earlier slot [03 R-AUD-01 §1].
func TestRegisteredStaticInstanceSelection(t *testing.T) {
	b, made := newStaticBackend()
	sample := staticTestSample()
	for range staticInstances {
		if err := b.PlayRegisteredSample(sample, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(*made) != staticInstances || b.statics[0].instances[0].player != (*made)[0] ||
		b.statics[0].instances[3].player != (*made)[1] || b.statics[0].instances[2].player != (*made)[2] ||
		b.statics[0].instances[1].player != (*made)[3] {
		t.Fatal("static instances did not fill 0,3,2,1")
	}
	// Slots 2 and 3 are both idle. The ascending scan must choose slot 2,
	// regardless of their reverse lazy-fill creation order [03 R-AUD-01 §1].
	(*made)[1].playing = false
	(*made)[2].playing = false
	if err := b.PlayRegisteredSample(sample, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(*made) != staticInstances || (*made)[2].starts != 2 || (*made)[1].starts != 1 {
		t.Fatal("lowest indexed idle static instance was not reused")
	}
	for _, p := range *made {
		p.playing = true
		p.position = 10 * time.Millisecond
	}
	if err := b.PlayRegisteredSample(sample, 1, 0); err != nil {
		t.Fatal(err)
	}
	if (*made)[0].starts != 2 || (*made)[1].starts != 1 || (*made)[2].starts != 2 || (*made)[3].starts != 1 {
		t.Fatal("cursor tie did not retain the earlier static slot")
	}
}

// Capacity stealing is earlier than lazy static-player creation. A failed new
// group therefore leaves the oldest tracked voice stopped and untracked
// [03 R-AUD-01 §1 steps 2,4].
func TestRegisteredStaticCapacityStealsBeforeLazyCreationFailure(t *testing.T) {
	b, made := newStaticBackend()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 1})
	if err := b.PlayRegisteredSample(staticTestSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(b.players) != 1 || len(b.statics) != 1 {
		t.Fatal("fixture did not admit its first registered sample")
	}
	b.createPlayer = func(io.Reader) (outputPlayer, error) { return nil, errors.New("create") }
	if err := b.PlayRegisteredSample(staticTestSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if (*made)[0].stops != 1 || len(b.players) != 0 || len(b.statics) != 1 || len(*made) != 1 {
		t.Fatal("lazy creation failure did not preserve capacity-before-instance ordering")
	}
}

func TestRegisteredStaticInstancesUseIdentityAndFurthestCursor(t *testing.T) {
	b, made := newStaticBackend()
	first, replacement := staticTestSample(), staticTestSample()
	if err := b.PlayRegisteredSample(first, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PlayRegisteredSample(replacement, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(b.statics) != 2 || len(*made) != 2 {
		t.Fatal("distinct sample identities shared a static instance group")
	}
	for range 3 {
		if err := b.PlayRegisteredSample(first, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	group := &b.statics[0]
	positions := [staticInstances]time.Duration{1, 2, 30, 10}
	for i := range group.instances {
		p := group.instances[i].player.(*staticPlayer)
		p.playing = true
		p.position = positions[i]
	}
	if err := b.PlayRegisteredSample(first, 1, 0); err != nil {
		t.Fatal(err)
	}
	if group.instances[2].player.(*staticPlayer).starts != 2 {
		t.Fatal("fifth request did not restart the furthest static instance")
	}
}

func TestRegisteredStaticResetOrderingAndFailure(t *testing.T) {
	b, made := newStaticBackend()
	sample := staticTestSample()
	for range staticInstances {
		if err := b.PlayRegisteredSample(sample, 1, -0.25); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range *made {
		p.playing = true
	}
	selected := (*made)[0]
	selected.rewindError = []error{errors.New("early"), nil}
	if err := b.PlayRegisteredSample(sample, 0.75, 0.5); err != nil {
		t.Fatal(err)
	}
	if selected.starts != 2 || len(selected.rewindPans) < 3 {
		t.Fatal("ignored early reset did not continue to playback")
	}
	last := selected.rewindPans[len(selected.rewindPans)-2:]
	if last[0] != -0.25 || last[1] != 0.5 {
		t.Fatalf("rewind pan order = %v, want [-0.25 0.5]", last)
	}
	selected.rewindError = []error{nil, errors.New("universal")}
	beforeStarts, beforeTracked := selected.starts, len(b.players)
	if err := b.PlayRegisteredSample(sample, 0.25, -0.5); err != nil {
		t.Fatal(err)
	}
	if selected.starts != beforeStarts || len(b.players) != beforeTracked || selected.volume != 0.75 {
		t.Fatal("universal reset failure played, tracked, or changed gain")
	}
}

func TestRegisteredStaticGainAndDuplicateTracking(t *testing.T) {
	b, made := newStaticBackend()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 33})
	sample := staticTestSample()
	for i := 0; i < 40; i++ {
		if err := b.PlayRegisteredSample(sample, float64(i+1)/100, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.players) != trackedVoiceSlots || len(b.untracked) != 1 {
		t.Fatal("fixture did not cross tracked/static duplicate boundary")
	}
	if (*made)[0].volume != 0.4 || b.players[0].baseGain != 0.4 || b.untracked[0].baseGain != 0.4 {
		t.Fatal("latest static gain did not replace older tracked and host-only references")
	}
	b.SetEffectsVolume(0.5)
	if (*made)[0].volume != 0.2 {
		t.Fatalf("older static reference overwrote latest gain: %g", (*made)[0].volume)
	}

	steal, stealMade := newStaticBackend()
	steal.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 5})
	for range 6 {
		if err := steal.PlayRegisteredSample(sample, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	if (*stealMade)[0].stops != 1 || (*stealMade)[0].starts != 3 || len(steal.players) != 5 {
		t.Fatal("steal did not clear one duplicate reference before static restart")
	}
}

func TestRegisteredStaticTerminalCleanup(t *testing.T) {
	b, made := newStaticBackend()
	if err := b.PlayRegisteredSample(staticTestSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	(*made)[0].playing = false
	b.reapLocked()
	if len(b.players) != 0 {
		t.Fatal("fixture did not reap ordinary reference")
	}
	b.Close()
	if len(b.statics) != 0 || (*made)[0].stops != 2 {
		t.Fatal("Close did not release retained static instance after reaping")
	}
}
