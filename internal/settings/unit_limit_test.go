package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestUnitLimitDefaultAndClamp locks the configured per-player unit limit:
// Nanolathe raises the missing value and upper bound by user request
// (DESIGN_CONTENT_VFS §5); settings still clamp once at startup.
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
		{501, 501},
		{1000, 1000},
		{2000, 2000},
		{3276, 3276},
		{3277, MaxUnitLimit},
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
	s.UnitLimit = 2000
	if err := s.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.UnitLimit != 2000 {
		t.Fatalf("UnitLimit = %d, want 2000", got.UnitLimit)
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
