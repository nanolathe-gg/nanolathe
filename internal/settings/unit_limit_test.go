package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestUnitLimitDefaultAndClamp locks the configured per-player unit limit:
// missing installs 250, and a stored value outside 20..500 is pulled to the
// nearer bound, which is retail's own start-up clamp rather than a schema
// repair [02 "Unit limit"][08 R-SKIR-01 §6].
func TestUnitLimitDefaultAndClamp(t *testing.T) {
	if got := Defaults().UnitLimit; got != DefaultUnitLimit {
		t.Fatalf("Defaults().UnitLimit = %d, want %d", got, DefaultUnitLimit)
	}
	for _, tc := range []struct{ in, want int }{
		{0, DefaultUnitLimit},
		{-1, MinUnitLimit},
		{19, MinUnitLimit},
		{20, 20},
		{500, 500},
		{501, MaxUnitLimit},
	} {
		s := Defaults()
		s.UnitLimit = tc.in
		s.Normalize()
		if s.UnitLimit != tc.want {
			t.Errorf("Normalize(UnitLimit %d) = %d, want %d", tc.in, s.UnitLimit, tc.want)
		}
	}
}

// TestUnitLimitSurvivesRoundTrip proves the value reaches the session rather
// than being reset on every save: a stored limit is written, read back
// unchanged, and a file that omits the key still gets the default.
func TestUnitLimitSurvivesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Defaults()
	s.UnitLimit = 320
	if err := s.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.UnitLimit != 320 {
		t.Fatalf("UnitLimit = %d, want 320", got.UnitLimit)
	}

	// A hand-written file with no `unitLimit` key takes the default, the way
	// the retail reader installs a default per absent value.
	body, err := json.Marshal(map[string]any{"version": FileVersion, "difficulty": 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bare := filepath.Join(t.TempDir(), "bare.json")
	if err := os.WriteFile(bare, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = LoadFrom(bare)
	if err != nil {
		t.Fatalf("LoadFrom(bare): %v", err)
	}
	if got.UnitLimit != DefaultUnitLimit {
		t.Fatalf("absent key UnitLimit = %d, want %d", got.UnitLimit, DefaultUnitLimit)
	}
}
