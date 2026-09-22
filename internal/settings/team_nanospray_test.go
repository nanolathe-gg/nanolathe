package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// This renderer preference preserves green for older settings files and keeps
// explicit choices through the write-all settings transaction (GPU design §30).
func TestTeamNanosprayPreferenceLoadAndRoundTrip(t *testing.T) {
	if Defaults().Presentation.TeamNanospray != 0 {
		t.Fatal("default nanospray must stay green")
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"older file", `{"version":1}`, 0},
		{"missing choice", `{"version":1,"presentation":{}}`, 0},
		{"green", `{"version":1,"presentation":{"teamNanospray":0}}`, 0},
		{"team", `{"version":1,"presentation":{"teamNanospray":1}}`, 1},
		{"positive choice", `{"version":1,"presentation":{"teamNanospray":2}}`, 2},
		{"negative repaired", `{"version":1,"presentation":{"teamNanospray":-1}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Presentation.TeamNanospray != tc.want {
				t.Fatalf("loaded nanospray %d, want %d", s.Presentation.TeamNanospray, tc.want)
			}
			if err := s.SaveTo(path); err != nil {
				t.Fatal(err)
			}
			s, err = LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Presentation.TeamNanospray != tc.want {
				t.Fatalf("saved nanospray %d, want %d", s.Presentation.TeamNanospray, tc.want)
			}
		})
	}
}
