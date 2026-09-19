package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The selected track's type is an editable list entry, not the desired battle
// category [03 R-AUD-01 §4]. Mode changes must preserve that distinction.
func TestMusicOptionsModeKeepsBattleCategory(t *testing.T) {
	prior := optionsState
	t.Cleanup(func() { optionsState = prior })
	fs := vfs.New()
	defer fs.Close()
	svc := audio.NewService(fs)
	svc.Music.Open(16)
	svc.Music.Configure(audio.ModeSequential, 0)
	shell := &gameShell{cs: testContentSet(fs), audioOwner: svc, audioPrefs: settings.DefaultAudio()}
	optionsState = &retailOptionsState{track: 1}
	optionsState.categories[0] = 3
	shell.applyRetailMusicMode()
	if svc.Music.DesiredCategory() != 0 {
		t.Fatal("mode change replaced Building with selected track type")
	}
	if svc.Music.TrackCategory(1) != 3 {
		t.Fatal("category edits did not reach playback controller")
	}
}

func TestMusicPreferencesApplyToAnExistingFrontendOwner(t *testing.T) {
	defer audio.ConfigureOutput(audio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: audio.SoundModeMono})
	svc := audio.NewService(nil)
	svc.Music.Configure(audio.ModeCategoryShuffle, 4)
	shell := &gameShell{setup: newSkirmishMenuConfig(""), audioOwner: svc}
	stored := settings.Defaults()
	stored.Audio.MusicVol = 12
	stored.Audio.MusicMode = 0
	shell.applySettings(stored)
	if svc.Music.Volume() != 12 || svc.Music.IsEnabled() || svc.Music.DesiredCategory() != 4 {
		t.Fatal("saved music preferences lost after frontend audio initialization")
	}
}
