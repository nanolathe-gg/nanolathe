package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/audiobackend"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestApplyRetailAudioOptionsPushesSelectedSoundMode(t *testing.T) {
	previous := audio.GlobalOutput()
	t.Cleanup(func() {
		audio.ConfigureOutput(audio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: audio.SoundModeMono})
		audio.SetGlobalOutput(previous)
	})
	audio.SetGlobalOutput(nil)

	stored := settings.Defaults()
	stored.Audio.SoundMode = settings.SoundMode3D
	// The startup settings read completes before the platform creates the PCM
	// device. Installing the backend afterwards must receive the saved choice.
	shell := &gameShell{setup: newSkirmishMenuConfig("")}
	shell.applySettings(stored)
	backend := audiobackend.New()
	audio.SetGlobalOutput(backend)
	if got := backend.SoundMode(); got != audio.SoundMode3D {
		t.Fatalf("saved 3D mode after delayed output install = %v, want 3D", got)
	}
	shell.audioPrefs.SoundMode = settings.SoundModeMono
	shell.applyRetailAudioOptions()
	if got := backend.SoundMode(); got != audio.SoundModeMono {
		t.Fatalf("mode switch back = %v, want Mono", got)
	}
}

func TestDirectBattleAudioOptionsRetainStored3DBeforeOutputInstall(t *testing.T) {
	previous := audio.GlobalOutput()
	t.Cleanup(func() {
		audio.ConfigureOutput(audio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: audio.SoundModeMono})
		audio.SetGlobalOutput(previous)
	})
	audio.SetGlobalOutput(nil)

	stored := settings.DefaultAudio()
	stored.SoundMode = settings.SoundMode3D
	applyBattleAudioOptions(&battleSession{}, settings.Settings{Audio: stored})
	backend := audiobackend.New()
	audio.SetGlobalOutput(backend)
	if got := backend.SoundMode(); got != audio.SoundMode3D {
		t.Fatalf("direct battle stored mode after delayed output install = %v, want 3D", got)
	}
}

type audioLimitConfigSpy struct{ config audio.OutputConfig }

func (*audioLimitConfigSpy) PlaySample(*audio.Sample, float64, float64) error { return nil }
func (s *audioLimitConfigSpy) ConfigureOutput(c audio.OutputConfig)           { s.config = c }

func TestStoredMixingBuffersReachDelayedAndLiveOutput(t *testing.T) {
	previous := audio.GlobalOutput()
	t.Cleanup(func() {
		audio.ConfigureOutput(audio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: audio.SoundModeMono, MixingBuffers: settings.DefaultMixingBuffers})
		audio.SetGlobalOutput(previous)
	})
	audio.SetGlobalOutput(nil)
	stored := settings.Defaults()
	stored.Audio.MixingBuffers = 3
	shell := &gameShell{setup: newSkirmishMenuConfig("")}
	shell.applySettings(stored)
	spy := &audioLimitConfigSpy{}
	audio.SetGlobalOutput(spy)
	if spy.config.MixingBuffers != 3 {
		t.Fatalf("delayed limit=%d, want3", spy.config.MixingBuffers)
	}
	shell.audioPrefs.MixingBuffers = 32
	shell.applyRetailAudioOptions()
	if spy.config.MixingBuffers != 32 {
		t.Fatalf("live limit=%d, want32", spy.config.MixingBuffers)
	}
	stored.Audio.MixingBuffers = 33
	applyBattleAudioOptions(&battleSession{}, stored)
	if spy.config.MixingBuffers != 33 {
		t.Fatalf("direct battle limit=%d, want33 without upper clamp", spy.config.MixingBuffers)
	}
}

// retailSoundFlags composes the packed sound-flags byte from the stored block:
// bits 0..2 `Sound Mode`, bit 3 `RestoreVolume`, bit 4 `ackfx`, bit 5
// `buildfx`, bit 6 `speechfx` [03 R-AUD-01 §2].
func TestRetailSoundFlagsBitLayout(t *testing.T) {
	a := settings.DefaultAudio() // mode 1, ackfx, buildfx, speechfx set
	if got := retailSoundFlags(a); got != 0x71 {
		t.Fatalf("default flags = %#02x, want 0x71", got)
	}
	a.SpeechFX = 0
	if got := retailSoundFlags(a); got != 0x31 {
		t.Fatalf("speechfx clear = %#02x, want 0x31", got)
	}
	a.SpeechFX = 1
	a.RestoreVolume = 1
	a.SoundMode = settings.SoundModeOff
	if got := retailSoundFlags(a); got != 0x78 {
		t.Fatalf("restorevolume set, mode off = %#02x, want 0x78", got)
	}
}

// The `SPEECH` gadget's two halves have to reach the voice queue: with bit 6
// clear no unit voice line plays, and captions are unaffected
// [03 R-AUD-01 §2][03 §8.3]. Before this wiring existed the queue kept its
// construction defaults and `SPEECH` `Off` still spoke.
func TestApplyRetailVoiceGatesCarriesTheSpeechPreference(t *testing.T) {
	play := func(a settings.Audio, unitChatText int) (plays, captions int) {
		svc := audio.NewService(nil)
		cat := &audio.Category{Name: "test"}
		cat.Rows[2].Variants = []string{"warn"}
		cat.Rows[2].Captions = []string{""}
		svc.Queue.Register(1, cat, "Peewee", true)
		applyRetailVoiceGates(svc, a, unitChatText)
		svc.Queue.OnPlay(func(string, audio.Slot, pool.Handle) { plays++ })
		svc.Queue.OnCaption(func(string, audio.Slot, pool.Handle) { captions++ })
		svc.Queue.InsertAt(100, 2, 1, "")
		svc.Queue.Drain(100)
		return plays, captions
	}

	full := settings.DefaultAudio() // `SPEECH` at `Full`: bit 6 set, level 10
	if plays, captions := play(full, settings.DefaultUnitChatText); plays != 1 || captions != 1 {
		t.Fatalf("SPEECH Full: plays=%d captions=%d, want 1 and 1", plays, captions)
	}

	off := settings.DefaultAudio() // `SPEECH` at `Off`: bit 6 clear, level 0
	off.SpeechFX = 0
	off.UnitChat = 0
	plays, captions := play(off, settings.DefaultUnitChatText)
	if plays != 0 {
		t.Fatalf("SPEECH Off still played %d voice lines", plays)
	}
	if captions != 1 {
		t.Fatalf("SPEECH Off suppressed the caption: captions=%d", captions)
	}

	// The caption gate is the other level, and `UNITCHAT` `Off` closes it while
	// the voice line still plays [07 R-CAM-01 §7].
	if plays, captions := play(full, 0); plays != 1 || captions != 0 {
		t.Fatalf("UNITCHAT Off: plays=%d captions=%d, want 1 and 0", plays, captions)
	}
}
