package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/audiobackend"
	"github.com/nanolathe/nanolathe/internal/settings"
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
