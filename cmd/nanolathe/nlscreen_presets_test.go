package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The unit restrictions are a match selection, not a preference: no preset
// part holds them, saving a preset never captures them and applying one never
// changes them (docs/DESIGN_MODS_MUTATORS.md §15.9).
func TestNLPresetsExcludeRestrictions(t *testing.T) {
	for _, scope := range nlPresetScopes {
		for _, p := range scope.paths() {
			if p == "restrictions" || strings.HasPrefix(p, "restrictions.") {
				t.Fatalf("preset part %q holds %s", scope.label, p)
			}
		}
	}
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	g := &gameShell{cs: restrictionTestContent(nil)}
	file := settings.Defaults()
	file.Restrictions = map[string]int{"armpw": 20}
	g.applySettings(file)
	g.settingsWritable = true
	s := newNLScreen(func() *gameShell { return g })
	s.draft = s.freshDraft(g)

	s.savePreset("Mine")
	if len(g.presets) != 1 {
		t.Fatalf("presets %+v", g.presets)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(g.presets[0].Settings, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["restrictions"]; ok {
		t.Fatalf("the saved preset holds the restrictions: %s", g.presets[0].Settings)
	}
	// A preset saved by an older build may hold a set: no part applies it.
	draft := s.draft.restrictions
	s.applyPresetToDraft(nlPresetEntry{name: "Old", patch: json.RawMessage(`{"restrictions":{"corak":0},"presentation":{"glint":0}}`)}, []bool{true, true, true})
	if s.draft.restrictions != draft || s.touched["restrictions"] || s.draft.pres.Glint != 0 {
		t.Fatalf("the preset moved the restrictions to %q (touched %v) or lost its glint", s.draft.restrictions, s.touched["restrictions"])
	}
	s.apply()
	if !maps.Equal(g.restrictions.saved, map[string]int{"armpw": 20}) || g.opts.Restrictions.String() != "armpw=20" {
		t.Fatalf("applying a preset changed the set: saved %v, battles %q", g.restrictions.saved, g.opts.Restrictions.String())
	}
}

// Scoped legacy presets migrate before filtering, and saving their pending
// draft writes the new key, including Auto, for the next apply and restart
// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale" and §3.17).
func TestNLPresetsLegacyUIScaleRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, scale string
		scopes      []bool
		want        int
	}{
		{"legacy controls", `"sidebarScale":2`, []bool{false, false, true}, 2},
		{"legacy all parts", `"sidebarScale":2`, []bool{true, true, true}, 2},
		{"legacy Auto", `"sidebarScale":0`, []bool{false, false, true}, 0},
		{"current Auto wins", `"sidebarScale":2,"uiScale":0`, []bool{false, false, true}, 0},
		{"graphics preserves scale", `"sidebarScale":2`, []bool{false, true, false}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			t.Setenv(settings.EnvPath, path)
			data := `{"version":1,"presentation":{"uiScale":1},"presets":[{"name":"Old","settings":{"presentation":{` + tc.scale + `,"glint":0},"switchAlt":1}}]}`
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := settings.Load()
			if err != nil {
				t.Fatal(err)
			}
			g, s := uiScaleSettingsScreen(nil, file)
			g.settingsWritable = true
			s.applyPresetToDraft(nlPresetEntry{name: "Old", patch: file.Presets[0].Settings}, tc.scopes)
			if s.draft.pres.UIScale != tc.want {
				t.Fatalf("draft scale = %d, want %d", s.draft.pres.UIScale, tc.want)
			}
			glint := settings.DefaultEffectSwitch
			if tc.scopes[1] {
				glint = 0
			}
			if s.draft.pres.Glint != glint || s.draft.switchAlt != tc.scopes[2] {
				t.Fatal("scale migration changed the preset's graphics/controls scope")
			}
			if g.presentation.UIScale != 1 {
				t.Fatal("applying a preset draft changed the live scale")
			}
			s.savePreset("Migrated")
			stored, err := settings.Load()
			if err != nil || len(stored.Presets) != 2 {
				t.Fatalf("saved presets: %v, %v", stored.Presets, err)
			}
			var saved struct {
				Presentation map[string]json.RawMessage `json:"presentation"`
			}
			if err := json.Unmarshal(stored.Presets[1].Settings, &saved); err != nil {
				t.Fatal(err)
			}
			var scale int
			value, ok := saved.Presentation["uiScale"]
			if err := json.Unmarshal(value, &scale); !ok || err != nil || scale != tc.want {
				t.Fatalf("saved preset lost explicit scale: %s", stored.Presets[1].Settings)
			}
			if _, ok := saved.Presentation["sidebarScale"]; ok {
				t.Fatal("saved preset retained the retired scale key")
			}
			s.apply()
			stored, err = settings.Load()
			if err != nil {
				t.Fatal(err)
			}
			restarted, reopened := uiScaleSettingsScreen(nil, stored)
			if restarted.presentation.UIScale != tc.want {
				t.Fatalf("restart scale = %d, want %d", restarted.presentation.UIScale, tc.want)
			}
			// A complete saved preset can also restore Auto over a fixed size.
			reopened.draft.pres.UIScale = 2
			reopened.touched["sidebar"] = true
			reopened.applyPresetToDraft(nlPresetEntry{name: "Migrated", patch: stored.Presets[1].Settings}, []bool{true, true, true})
			if reopened.draft.pres.UIScale != tc.want {
				t.Fatalf("saved preset reapplied scale = %d, want %d", reopened.draft.pres.UIScale, tc.want)
			}
		})
	}
}
