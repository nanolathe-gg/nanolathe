package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The `clock` DWORD defaults clear, retains only its low bit at the settings
// boundary, and survives the common write/read cycle [02 "Settings"]
// [07 R-CAM-01 §6].
func TestClockLowBitPersistence(t *testing.T) {
	if s := Defaults(); s.Clock != 0 || s.ClockEnabled() {
		t.Fatalf("clock default = %d (enabled %t), want clear", s.Clock, s.ClockEnabled())
	}

	path := filepath.Join(t.TempDir(), "settings.json")
	s := Defaults()
	s.Clock = 3
	if err := s.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored Settings
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Clock != 1 {
		t.Fatalf("stored clock = %d, want low bit 1", stored.Clock)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Clock != 1 || !loaded.ClockEnabled() {
		t.Fatalf("loaded clock = %d (enabled %t), want set", loaded.Clock, loaded.ClockEnabled())
	}

	loaded.Clock = 2
	loaded.Normalize()
	if loaded.Clock != 0 || loaded.ClockEnabled() {
		t.Fatalf("normalized even clock = %d (enabled %t), want clear", loaded.Clock, loaded.ClockEnabled())
	}
}
