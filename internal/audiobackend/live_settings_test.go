package audiobackend

import (
	"encoding/binary"
	"math"
	"testing"

	retailaudio "github.com/nanolathe/nanolathe/internal/audio"
)

type observedPlayer struct {
	data          []byte
	volume        float64
	playing       bool
	starts, stops int
}

func (p *observedPlayer) Play()                { p.playing = true; p.starts++ }
func (p *observedPlayer) IsPlaying() bool      { return p.playing }
func (p *observedPlayer) SetVolume(v float64)  { p.volume = v }
func (p *observedPlayer) PauseAndStopReading() { p.playing = false; p.stops++ }
func (p *observedPlayer) firstOutput() float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(p.data))) * p.volume
}

// Settings enter through the actual PlaySample/PlayStream path; only the host
// device is replaced. The authored long cue remains playing across a level
// change, distinguishing live output gain from gain baked into future PCM.
func TestLiveEffectsAndModeOff(t *testing.T) {
	b := New()
	var players []*observedPlayer
	b.createPlayer = func(data []byte) (outputPlayer, error) {
		p := &observedPlayer{data: data}
		players = append(players, p)
		return p, nil
	}
	sample := &retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: make([]byte, 11025)}
	for i := range sample.Data {
		sample.Data[i] = 192
	}
	b.SetEffectsVolume(0.5)
	if err := b.PlaySample(sample, 0.5, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PlayStream(sample, 1); err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 {
		t.Fatalf("players = %d", len(players))
	}
	cue, narration := players[0], players[1]
	assertOutput := func(wantCue, wantNarration float64) {
		t.Helper()
		if cue.firstOutput() != wantCue || narration.firstOutput() != wantNarration {
			t.Fatalf("cue/narration output = %g/%g, want %g/%g", cue.firstOutput(), narration.firstOutput(), wantCue, wantNarration)
		}
	}
	assertOutput(0.125, 0.25)
	b.SetEffectsVolume(0.25)
	assertOutput(0.0625, 0.125)
	b.SetEffectsVolume(0)
	assertOutput(0, 0)
	if err := b.PlaySample(sample, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 {
		t.Fatal("zero FX admitted a new ordinary cue")
	}
	b.SetEffectsVolume(1)
	assertOutput(0.25, 0.5)
	if cue.starts != 1 || narration.starts != 1 || !cue.playing || !narration.playing {
		t.Fatal("FX changes restarted or stopped an active buffer")
	}
	b.SetMasterEnabled(false)
	if cue.playing || cue.stops != 1 || len(b.players) != 0 {
		t.Fatal("MODE Off did not stop and clear ordinary voices")
	}
	if !narration.playing || narration.stops != 0 {
		t.Fatal("ordinary stop-all tore down the separate narration stream")
	}
	if err := b.PlaySample(sample, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 {
		t.Fatal("MODE Off admitted a new ordinary cue")
	}
	b.SetMasterEnabled(true)
	if cue.playing || cue.starts != 1 {
		t.Fatal("MODE On resurrected a stopped cue")
	}
	b.StopStream()
	if narration.playing || narration.stops != 1 {
		t.Fatal("StopStream did not release narration")
	}
	b.Close()
}

// The stream opener is independent of the ordinary MODE play gate. Its own
// stop operation and the shared output gain remain effective [03 R-AUD-02 §1].
func TestStreamStartsThroughMutedOrdinaryOutput(t *testing.T) {
	b := New()
	p := &observedPlayer{}
	b.createPlayer = func(data []byte) (outputPlayer, error) { p.data = data; return p, nil }
	b.SetMasterEnabled(false)
	b.SetEffectsVolume(0)
	if err := b.PlayStream(&retailaudio.Sample{Channels: 1, SampleRate: 11025, BitsPerSample: 8, Data: []byte{192}}, 1); err != nil {
		t.Fatal(err)
	}
	if !p.playing || p.volume != 0 {
		t.Fatal("muted stream was dropped or audible")
	}
	b.SetEffectsVolume(0.5)
	if p.firstOutput() != 0.25 || p.starts != 1 {
		t.Fatal("live stream gain did not recover without a restart")
	}
	b.Close()
	if p.playing {
		t.Fatal("Close retained a stream")
	}
}
