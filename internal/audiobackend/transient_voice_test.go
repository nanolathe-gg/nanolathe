package audiobackend

import (
	"errors"
	"io"
	"testing"
	"time"

	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
)

func transientSample() *retailaudio.Sample {
	return &retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{192}}
}

func newTransientBackend(t *testing.T, limit int) (*Backend, *[]*observedPlayer) {
	t.Helper()
	b := New()
	var made []*observedPlayer
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		player := &observedPlayer{}
		made = append(made, player)
		return player, nil
	}
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: limit})
	return b, &made
}

func fillTransientSlots(t *testing.T, b *Backend) {
	t.Helper()
	for range 8 {
		if err := b.PlaySample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTransientAdmissionDropsNinthBeforeCreationOrSteal(t *testing.T) {
	b, made := newTransientBackend(t, 8+1)
	fillTransientSlots(t, b)
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if got := len(*made); got != 8 {
		t.Fatalf("created players = %d, want %d", got, 8)
	}
	if got := len(b.players); got != 8 {
		t.Fatalf("tracked voices = %d, want %d", got, 8)
	}
	if got := len(b.transients); got != 8 {
		t.Fatalf("transient references = %d, want %d", got, 8)
	}
	for index, player := range *made {
		if !player.playing || player.stops != 0 {
			t.Fatalf("transient %d was changed by dropped ninth: playing=%t stops=%d", index, player.playing, player.stops)
		}
	}
}

func TestTransientCompletionAndStealFreeSlotOnNextTransientLoad(t *testing.T) {
	t.Run("completion", func(t *testing.T) {
		b, made := newTransientBackend(t, 8+1)
		fillTransientSlots(t, b)
		(*made)[0].playing = false
		if err := b.PlaySample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
		if len(*made) != 8+1 || len(b.transients) != 8 {
			t.Fatalf("completion did not make one transient slot reusable: made=%d transients=%d", len(*made), len(b.transients))
		}
	})

	t.Run("global steal", func(t *testing.T) {
		b, made := newTransientBackend(t, 8+1)
		fillTransientSlots(t, b)
		if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
		if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
		if (*made)[0].playing || (*made)[0].stops != 1 || len(b.transients) != 8 {
			t.Fatal("ordinary global steal did not leave the stopped transient for its next reap")
		}
		if err := b.PlaySample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
		if len(*made) != 8+3 || len(b.transients) != 8 {
			t.Fatalf("stolen transient was not reaped before the next mode-1 load: made=%d transients=%d", len(*made), len(b.transients))
		}
	})
}

func TestTransientSlotsDoNotBlockRegisteredOrStreamPlayback(t *testing.T) {
	b, made := newTransientBackend(t, 8+2)
	fillTransientSlots(t, b)
	if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PlayStream(transientSample(), 1); err != nil {
		t.Fatal(err)
	}
	if len(*made) != 8+2 || len(b.transients) != 8 || len(b.players) != 8+1 || len(b.streams) != 1 {
		t.Fatalf("registered or stream playback consumed a transient slot: made=%d transients=%d players=%d streams=%d", len(*made), len(b.transients), len(b.players), len(b.streams))
	}
}

func TestFailedTransientCreationDoesNotOccupySlot(t *testing.T) {
	b := New()
	attempts := 0
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("device rejected player")
		}
		return &observedPlayer{}, nil
	}
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 8 + 1})
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || len(b.transients) != 0 || len(b.players) != 0 {
		t.Fatalf("failed creation occupied playback state: attempts=%d transients=%d players=%d", attempts, len(b.transients), len(b.players))
	}
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(b.transients) != 1 || len(b.players) != 1 {
		t.Fatalf("later creation did not use the unoccupied transient slot: attempts=%d transients=%d players=%d", attempts, len(b.transients), len(b.players))
	}
}

type transientRewindFailPlayer struct {
	observedPlayer
	err error
}

func (p *transientRewindFailPlayer) Rewind() error { return p.err }

func TestTransientFailureOrderingPreservesOrReleasesCapacity(t *testing.T) {
	b := New()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 1})
	old := &observedPlayer{}
	b.createPlayer = func(io.Reader) (outputPlayer, error) { return old, nil }
	if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	b.createPlayer = func(io.Reader) (outputPlayer, error) { return nil, errors.New("create") }
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if !old.playing || old.stops != 0 || len(b.players) != 1 || len(b.transients) != 0 {
		t.Fatal("mode-1 create failure stole or retained playback state")
	}

	failing := &transientRewindFailPlayer{err: errors.New("rewind")}
	b.createPlayer = func(io.Reader) (outputPlayer, error) { return failing, nil }
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if old.playing || old.stops != 1 || failing.playing || failing.stops != 1 || len(b.players) != 0 || len(b.transients) != 0 {
		t.Fatal("post-steal mode-1 rewind failure retained gain, playback, or references")
	}
}

type transientOrderPlayer struct {
	events  *[]string
	playing bool
}

func (p *transientOrderPlayer) Play()                 { p.playing = true; *p.events = append(*p.events, "play") }
func (p *transientOrderPlayer) IsPlaying() bool       { return p.playing }
func (*transientOrderPlayer) Position() time.Duration { return 0 }
func (p *transientOrderPlayer) Rewind() error         { *p.events = append(*p.events, "rewind"); return nil }
func (p *transientOrderPlayer) SetVolume(float64)     { *p.events = append(*p.events, "gain") }
func (p *transientOrderPlayer) PauseAndStopReading() {
	p.playing = false
	*p.events = append(*p.events, "stop")
}

func TestTransientSuccessCreatesThenStealsRewindsGainsAndTracks(t *testing.T) {
	b := New()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 1})
	events := []string{}
	old := &transientOrderPlayer{events: &events}
	newPlayer := &transientOrderPlayer{events: &events}
	created := 0
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		created++
		if created == 1 {
			return old, nil
		}
		events = append(events, "create")
		return newPlayer, nil
	}
	if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	events = nil
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "stop", "rewind", "gain", "play"}
	if len(events) != len(want) {
		t.Fatalf("mode-1 ordering = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("mode-1 ordering = %v, want %v", events, want)
		}
	}
	if len(b.players) != 1 || b.players[0].player != newPlayer || len(b.transients) != 1 || b.transients[0].player != newPlayer {
		t.Fatal("successful mode-1 request did not track and retain the new voice")
	}
}

func TestPumpReapsCompletedTransientReferencesAtItsExistingCadence(t *testing.T) {
	b, made := newTransientBackend(t, 8+1)
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	b.Pump(now)
	(*made)[0].playing = false
	b.Pump(now.Add(99 * time.Millisecond))
	if len(b.transients) != 1 {
		t.Fatal("pump reaped a transient before its 100 ms cadence")
	}
	b.Pump(now.Add(100 * time.Millisecond))
	if len(b.transients) != 0 || len(b.players) != 0 || (*made)[0].stops != 1 {
		t.Fatalf("pump did not release the completed transient: transients=%d players=%d stops=%d", len(b.transients), len(b.players), (*made)[0].stops)
	}
}

// A full ordinary table distinguishes the loader's drop from mixer stealing.
// Literal counts independently lock the eight-transient contract [03 R-AUD-01 §1].
func TestNinthTransientDropsBeforeFullMixerSteal(t *testing.T) {
	b, made := newTransientBackend(t, 32)
	for range 24 {
		if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	fillTransientSlots(t, b)
	if len(*made) != 32 || len(b.players) != 32 {
		t.Fatal("fixture did not fill global mixer")
	}
	if err := b.PlaySample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(*made) != 32 || len(b.players) != 32 || len(b.transients) != 8 {
		t.Fatal("ninth transient entered full mixer")
	}
	for _, p := range *made {
		if !p.playing || p.stops != 0 {
			t.Fatal("dropped transient stole a live mixer voice")
		}
	}
}
