package settings

import (
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"os"
	"path/filepath"
	"testing"
)

func TestGameplayPreferenceMigrationAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, text := range []string{`{"version":1}`, `{"version":1,"gameplay":"unknown"}`} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.Gameplay != gameplay.Modern {
			t.Fatalf("legacy/unknown mode %q", s.Gameplay)
		}
	}
	s := Defaults()
	s.Gameplay = gameplay.Strict31
	if err := s.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gameplay != gameplay.Strict31 {
		t.Fatal("strict mode lost on restart")
	}
}
