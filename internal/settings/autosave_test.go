package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The periodic autosave interval is a host preference in minutes
// (DESIGN_SESSIONS_AI_SAVE §5, "Autosave, quick save and quick load"): a file
// written before the key existed keeps the ten minutes of issue #146, an
// explicit 0 stays Off, and only values the game cannot use are repaired.
func TestAutosaveMinutesLoadAndRoundTrip(t *testing.T) {
	if DefaultPresentation().AutosaveMinutes != 10 || Defaults().Presentation.AutosaveMinutes != 10 {
		t.Fatal("the autosave interval default must remain ten minutes")
	}
	for _, tc := range []struct {
		name, block string
		want        int
	}{
		{"old file", "", 10},
		{"omitted key", `,"presentation":{"fps":120}`, 10},
		{"off", `,"presentation":{"autosaveMinutes":0}`, AutosaveOff},
		{"five", `,"presentation":{"autosaveMinutes":5}`, 5},
		{"hand-edited", `,"presentation":{"autosaveMinutes":20}`, 20},
		{"negative", `,"presentation":{"autosaveMinutes":-3}`, 10},
		{"too long", `,"presentation":{"autosaveMinutes":100000}`, MaxAutosaveMinutes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			body := `{"version":` + strconv.Itoa(FileVersion) + tc.block + `}`
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := loaded.Presentation.AutosaveMinutes; got != tc.want {
				t.Fatalf("loaded interval = %d, want %d", got, tc.want)
			}
			if err := loaded.SaveTo(path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var stored struct {
				Presentation map[string]json.RawMessage `json:"presentation"`
			}
			if err := json.Unmarshal(data, &stored); err != nil {
				t.Fatal(err)
			}
			if got := string(stored.Presentation["autosaveMinutes"]); got != strconv.Itoa(tc.want) {
				t.Fatalf("stored interval = %q, want %d", got, tc.want)
			}
		})
	}
}
