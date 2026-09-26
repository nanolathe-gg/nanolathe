package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The saved modernAI block becomes the session's three layers, each checked
// against the brain's keys and spelled canonically; its difficulty keys are
// the three words and its player keys the lobby's 1-based slot numbers
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Configuration").
func TestModernAISettingsBecomeTheSessionLayers(t *testing.T) {
	block := settings.ModernAI{
		All:        settings.AIParams{"style": "eco", "jitter": "0"},
		Difficulty: map[string]settings.AIParams{"hard": {"w_army": "120"}},
		Players:    map[string]settings.AIParams{"2": {"style": "tower"}, "10": {"micro": "0"}},
	}
	got, err := aiOverridesFromSettings(block, "settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var want session.AIOverrides
	want.All = "jitter=0,style=eco"
	want.Difficulty[2] = "w_army=120"
	want.Players[1] = "style=tower"
	want.Players[9] = "micro=0"
	if got != want {
		t.Fatalf("layers %+v, want %+v", got, want)
	}

	for _, tc := range []struct {
		block   settings.ModernAI
		logical string
	}{
		{settings.ModernAI{All: settings.AIParams{"w_amry": "1"}}, "modernAI.all"},
		{settings.ModernAI{Players: map[string]settings.AIParams{"2": {"style": "rush"}}}, "modernAI.players.2"},
		{settings.ModernAI{Difficulty: map[string]settings.AIParams{"brutal": {"style": "eco"}}}, "modernAI.difficulty.brutal"},
		{settings.ModernAI{Players: map[string]settings.AIParams{"0": {"style": "eco"}}}, "modernAI.players.0"},
		{settings.ModernAI{Players: map[string]settings.AIParams{"11": {"style": "eco"}}}, "modernAI.players.11"},
		{settings.ModernAI{Players: map[string]settings.AIParams{"02": {"style": "eco"}}}, "modernAI.players.02"},
	} {
		_, err := aiOverridesFromSettings(tc.block, "settings.json")
		if err == nil || !strings.Contains(err.Error(), "logical path "+tc.logical+" in settings.json") || !strings.HasPrefix(err.Error(), "nanolathe: ") {
			t.Fatalf("%+v: error %v, want a diagnostic naming %s", tc.block, err, tc.logical)
		}
	}
}

// --ai sets every computer player's parameters and wins over the saved
// block; the window otherwise reads the block, a bad entry in it stops the
// start, and captures and displayless runs never read it.
func TestTheAIFlagWinsOverTheSavedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	saved := settings.Defaults()
	saved.ModernAI.Players = map[string]settings.AIParams{"3": {"style": "greedy"}}
	if err := saved.SaveTo(path); err != nil {
		t.Fatal(err)
	}

	opts, err := parseFlags([]string{"--ai", "style=eco,w_army=90", "--ai", "style=units"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveStartupAIOverrides(opts)
	if err != nil || got != (session.AIOverrides{All: "style=units,w_army=90"}) {
		t.Fatalf("flag layers %+v (%v)", got, err)
	}
	if _, err := parseFlags([]string{"--ai", "w_amry=90"}, io.Discard); err == nil || !strings.Contains(err.Error(), "logical path --ai w_amry=90") {
		t.Fatalf("an unknown --ai key parsed: %v", err)
	}

	got, err = resolveStartupAIOverrides(Options{})
	if err != nil || got.Players[2] != "style=greedy" || got.All != "" {
		t.Fatalf("saved layers %+v (%v)", got, err)
	}
	if got, err := resolveStartupAIOverrides(Options{Headless: true}); err != nil || !got.IsZero() {
		t.Fatalf("a displayless run read the saved block: %+v (%v)", got, err)
	}

	saved.ModernAI.Players["3"] = settings.AIParams{"style": "rush"}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := saved.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveStartupAIOverrides(Options{}); err == nil {
		t.Fatal("an unknown style in the saved block did not stop the start")
	}
}

// No screen edits the block, so the shell writes back exactly what it
// read.
func TestTheModernAIBlockRidesThroughTheShell(t *testing.T) {
	maps := []string{"Painted Desert"}
	src := &gameShell{maps: maps}
	src.setup = newSkirmishMenuConfig(maps[0])
	blob := src.captureSettings()
	blob.ModernAI = settings.ModernAI{All: settings.AIParams{"style": "eco"}, Players: map[string]settings.AIParams{"2": {"jitter": "0"}}}
	dst := &gameShell{maps: maps}
	dst.setup = newSkirmishMenuConfig(maps[0])
	dst.applySettings(blob)
	if got := dst.captureSettings().ModernAI; !reflect.DeepEqual(got, blob.ModernAI) {
		t.Fatalf("captured %+v, want %+v", got, blob.ModernAI)
	}
}

// A save whose record names a parameter this build's brain does not read is
// refused, as an unknown mutator is, rather than loaded with the defaults.
func TestASaveWithUnknownAIParametersIsRefused(t *testing.T) {
	good := &session.AIControllers{Seed: 3, Overrides: []session.AIPlayerOverrides{{Player: 1, Params: "jitter=0,style=eco"}}}
	if err := validateRecordedAIOverrides(good); err != nil {
		t.Fatal(err)
	}
	if err := validateRecordedAIOverrides(nil); err != nil {
		t.Fatal(err)
	}
	bad := &session.AIControllers{Seed: 3, Overrides: []session.AIPlayerOverrides{{Player: 1, Params: "w_retired=1"}}}
	err := validateRecordedAIOverrides(bad)
	var refusal *saveLoadRefusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.message, "player 2") {
		t.Fatalf("refusal %v", err)
	}
}
