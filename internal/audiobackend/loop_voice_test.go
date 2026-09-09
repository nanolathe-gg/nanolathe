package audiobackend

import (
	"io"
	"testing"
	"time"

	retailaudio "github.com/nanolathe-gg/nanolathe/internal/audio"
)

func TestLoopingRegisteredVoiceIsExclusiveAndProtectedFromSteal(t *testing.T) {
	b := New()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 2})
	var made []*observedPlayer
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		p := &observedPlayer{}
		made = append(made, p)
		return p, nil
	}
	loop, first, second := transientSample(), transientSample(), transientSample()
	if err := b.PlayLoopingRegisteredSample(loop, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PlayLoopingRegisteredSample(loop, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(made) != 1 || made[0].starts != 1 || len(b.players) != 1 {
		t.Fatal("exclusive loop reissued a player, restart, or mixer reference")
	}
	if err := b.PlayRegisteredSample(first, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PlayRegisteredSample(second, 1, 0); err != nil {
		t.Fatal(err)
	}
	if !made[0].playing || made[0].stops != 0 || made[1].playing || made[1].stops != 1 || !made[2].playing {
		t.Fatal("capacity did not retain old loop and steal oldest non-loop")
	}
	if len(b.players) != 2 || !b.players[0].loop || b.players[1].loop {
		t.Fatalf("tracked loop flags = %#v", b.players)
	}
	b.SetMasterEnabled(false)
	if made[0].playing || len(b.players) != 0 {
		t.Fatal("MODE Off did not stop looping voice")
	}
}

func TestOnlyLoopCapacityDropsAndPumpReapsLoop(t *testing.T) {
	b := New()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 1})
	var made []*observedPlayer
	b.createPlayer = func(io.Reader) (outputPlayer, error) {
		p := &observedPlayer{}
		made = append(made, p)
		return p, nil
	}
	if err := b.PlayLoopingRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PlayRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(made) != 1 || len(b.players) != 1 || !b.players[0].loop || !made[0].playing {
		t.Fatal("only-loop capacity selected a host-invented victim")
	}
	now := time.Unix(100, 0)
	b.Pump(now)
	b.Pump(now.Add(100 * time.Millisecond))
	if len(b.players) != 1 || !made[0].playing {
		t.Fatal("pump reaped a live loop")
	}
	made[0].playing = false
	b.Pump(now.Add(200 * time.Millisecond))
	if len(b.players) != 0 || made[0].stops != 1 {
		t.Fatal("pump did not release completed loop")
	}
}

func TestUntrackedLoopRemainsOutsideModeOffAndClosesWithBackend(t *testing.T) {
	b := New()
	b.ConfigureOutput(retailaudio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, MixingBuffers: 33})
	for range trackedVoiceSlots {
		b.players = append(b.players, voice{player: &observedPlayer{playing: true}})
	}
	loop := &observedPlayer{}
	b.createPlayer = func(io.Reader) (outputPlayer, error) { return loop, nil }
	if err := b.PlayLoopingRegisteredSample(transientSample(), 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(b.untracked) != 1 || !b.untracked[0].loop || !loop.playing {
		t.Fatal("fixture did not place loop beyond the tracked table")
	}
	b.SetMasterEnabled(false)
	if !loop.playing || loop.stops != 0 || len(b.untracked) != 1 {
		t.Fatal("MODE Off reached outside the fixed tracking table")
	}
	b.Close()
	if loop.playing || len(b.untracked) != 0 {
		t.Fatal("backend shutdown retained its host-only loop reference")
	}
}

func TestRegisteredLoopReaderCanBecomeOneShot(t *testing.T) {
	b, made := newStaticBackend()
	sample := staticTestSample()
	if err := b.PlayLoopingRegisteredSample(sample, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(*made) != 1 || !(*made)[0].reader.loop {
		t.Fatal("looping registered request did not arm static reader")
	}
	(*made)[0].playing = false
	if err := b.PlayRegisteredSample(sample, 1, 0); err != nil {
		t.Fatal(err)
	}
	if (*made)[0].reader.loop {
		t.Fatal("one-shot reuse retained prior looping reader state")
	}
}
