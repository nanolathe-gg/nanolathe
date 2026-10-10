package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func uiScaleSettingsScreen(mod *modlibrary.Mod, file settings.Settings) (*gameShell, *nlScreen) {
	// The real flag default preserves saved preferences; zero Options would
	// instead be an explicit command-line Auto override.
	g := &gameShell{cs: &contentSet{mod: mod}, opts: Options{UIScale: -1}}
	g.applySettings(file)
	s := newNLScreen(func() *gameShell { return g })
	if mod != nil {
		s.mods = []modlibrary.Mod{*mod}
	}
	s.draft = s.freshDraft(g)
	s.bindSource(g)
	return g, s
}

// Old settings and mod recommendations keep the same base/mod/player order,
// including Auto and locked values, through a save and restart
// (DESIGN_MODS_MUTATORS §4.6, DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func TestModSettingsLegacyUIScaleRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, player, lock string
		want               int
	}{
		{"recommendation overrides base", `{}`, ``, 2},
		{"player Auto overrides recommendation", `{"presentation":{"sidebarScale":0}}`, ``, 0},
		{"current player Auto wins", `{"presentation":{"sidebarScale":2,"uiScale":0}}`, ``, 0},
		{"lock suppresses legacy player", `{"presentation":{"sidebarScale":1}}`, `presentation.uiScale`, 2},
		{"legacy lock suppresses current player", `{"presentation":{"uiScale":1}}`, `presentation.sidebarScale`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			data := `{"version":1,"presentation":{"sidebarScale":1},"modSettings":{"scale-mod":` + tc.player + `}}`
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := settings.LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			locks := `[]`
			if tc.lock != "" {
				locks = `["` + tc.lock + `"]`
			}
			meta, err := modlibrary.ParseMetadata([]byte(`{"schema":2,"id":"scale-mod","name":"Scale mod","version":"1","settings":{"presentation":{"sidebarScale":2}},"locks":` + locks + `}`))
			if err != nil {
				t.Fatal(err)
			}
			mod := &modlibrary.Mod{Metadata: meta}
			g, _ := uiScaleSettingsScreen(mod, file)
			if g.presentation.UIScale != tc.want {
				t.Fatalf("effective scale = %d, want %d", g.presentation.UIScale, tc.want)
			}
			if err := g.captureSettings().SaveTo(path); err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(written, []byte(`"sidebarScale"`)) {
				t.Fatal("saving the base and active mod retained a retired scale key")
			}
			stored, err := settings.LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Presentation.UIScale != 1 {
				t.Fatalf("base took the mod's scale: %d", stored.Presentation.UIScale)
			}
			restarted, _ := uiScaleSettingsScreen(mod, stored)
			if restarted.presentation.UIScale != tc.want {
				t.Fatalf("restart scale = %d, want %d", restarted.presentation.UIScale, tc.want)
			}
			if tc.lock != "" {
				if mod.Config.Locks[0] != tc.lock {
					t.Fatal("resolving the lock changed the mod's config")
				}
				stored.ModLockOverrides = []string{mod.ID}
				restarted.applySettings(stored)
				if restarted.presentation.UIScale != 1 {
					t.Fatalf("unlock lost the player's stored scale: %d", restarted.presentation.UIScale)
				}
			}
		})
	}
}
