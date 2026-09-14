package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// This is a Nanolathe preference: old files acquire the default, while an
// explicit Off must survive loading and the write-all settings transaction.
func TestExpandedSidebarMissingAndExplicitOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, tc := range []struct {
		text string
		want int
	}{
		{`{"version":1,"presentation":{}}`, 1},
		{`{"version":1,"presentation":{"expandedSidebar":0}}`, 0},
	} {
		if err := os.WriteFile(path, []byte(tc.text), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := LoadFrom(path)
		if err != nil || s.Presentation.ExpandedSidebar != tc.want {
			t.Fatalf("loaded sidebar %d, want %d: %v", s.Presentation.ExpandedSidebar, tc.want, err)
		}
		if err := s.SaveTo(path); err != nil {
			t.Fatal(err)
		}
		s, err = LoadFrom(path)
		if err != nil || s.Presentation.ExpandedSidebar != tc.want {
			t.Fatalf("saved sidebar %d, want %d: %v", s.Presentation.ExpandedSidebar, tc.want, err)
		}
	}
}
