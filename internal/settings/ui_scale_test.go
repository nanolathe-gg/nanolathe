package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A size stored under the sidebar-only key carries over to the UI scale, and a
// size above the maximum reads as the maximum
// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func TestUIScaleNormalize(t *testing.T) {
	p := DefaultPresentation()
	if err := json.Unmarshal([]byte(`{"sidebarScale":2}`), &p); err != nil {
		t.Fatal(err)
	}
	p.Normalize()
	if p.UIScale != 2 {
		t.Fatalf("legacy 2 normalized to UIScale %d", p.UIScale)
	}
	p = Presentation{UIScale: 3}
	p.Normalize()
	if p.UIScale != MaxChromeScale {
		t.Fatalf("UIScale 3 normalized to %d, want %d", p.UIScale, MaxChromeScale)
	}
}

// Migration is local to each incoming document, before settings layers choose
// precedence. Auto is an explicit choice, including beside the retired key
// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale", DESIGN_MODS_MUTATORS §4.6).
func TestUIScalePersistedLayerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, base, mod, player string
		want                    int
	}{
		{"legacy mod overrides legacy base", `"sidebarScale":1`, `"sidebarScale":2`, ``, 2},
		{"legacy mod overrides current base", `"uiScale":1`, `"sidebarScale":2`, ``, 2},
		{"legacy player overrides mod", `"sidebarScale":1`, `"uiScale":1`, `"sidebarScale":2`, 2},
		{"legacy Auto overrides fixed", `"sidebarScale":2`, `"sidebarScale":1`, `"sidebarScale":0`, 0},
		{"current Auto overrides legacy", `"sidebarScale":2`, `"sidebarScale":2`, `"uiScale":0`, 0},
		{"same document current Auto wins", `"sidebarScale":2,"uiScale":0`, ``, ``, 0},
		{"same layer current Auto wins", `"sidebarScale":1`, ``, `"sidebarScale":2,"uiScale":0`, 0},
		{"later legacy overrides current Auto", `"uiScale":0`, `"sidebarScale":2`, ``, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			data := `{"version":1,"presentation":{` + tc.base + `},"modSettings":{"scale-mod":{"presentation":{` + tc.player + `}}}}`
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			mod := json.RawMessage(`{"presentation":{` + tc.mod + `}}`)
			effective, err := Layer(file, mod, file.ModSettings["scale-mod"])
			if err != nil {
				t.Fatal(err)
			}
			if effective.Presentation.UIScale != tc.want {
				t.Fatalf("layered UI scale = %d, want %d", effective.Presentation.UIScale, tc.want)
			}
		})
	}
}

func TestUIScalePatchesMigrateBeforeComposition(t *testing.T) {
	legacy := json.RawMessage(`{"presentation":{"sidebarScale":2,"glint":0}}`)
	auto := json.RawMessage(`{"presentation":{"uiScale":0}}`)
	both := json.RawMessage(`{"presentation":{"sidebarScale":2,"uiScale":0,"glint":0}}`)
	for _, tc := range []struct {
		name string
		run  func() (json.RawMessage, error)
		want string
	}{
		{"legacy over Auto", func() (json.RawMessage, error) { return Merge(auto, legacy) }, `{"presentation":{"glint":0,"uiScale":2}}`},
		{"Auto over legacy", func() (json.RawMessage, error) { return Merge(legacy, auto) }, `{"presentation":{"glint":0,"uiScale":0}}`},
		{"same layer current wins", func() (json.RawMessage, error) { return Merge(legacy, both) }, `{"presentation":{"glint":0,"uiScale":0}}`},
		{"only incoming layer", func() (json.RawMessage, error) { return Merge(nil, legacy) }, `{"presentation":{"glint":0,"uiScale":2}}`},
		{"only lower layer", func() (json.RawMessage, error) { return Merge(legacy, nil) }, `{"presentation":{"glint":0,"uiScale":2}}`},
		{"scope legacy", func() (json.RawMessage, error) { return Restrict(legacy, []string{"presentation.uiScale"}) }, `{"presentation":{"uiScale":2}}`},
		{"scope Auto", func() (json.RawMessage, error) { return Restrict(both, []string{"presentation.uiScale"}) }, `{"presentation":{"uiScale":0}}`},
		{"locked legacy", func() (json.RawMessage, error) { return Without(legacy, []string{"presentation.uiScale"}) }, `{"presentation":{"glint":0}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.run()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("patch = %s, want %s", got, tc.want)
			}
		})
	}
}
