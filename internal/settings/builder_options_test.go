package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuilderOptionsDefaultsNormalizationAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		body string
		want BuilderOptions
	}{
		{`{"version":1}`, DefaultBuilderOptions()},
		{`{"version":1,"builderOptions":{"guard":[0,2,9],"patrol":[2,0,-1]}}`, BuilderOptions{Guard: [3]int{0, 2, 1}, Patrol: [3]int{2, 0, 1}}},
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.BuilderOptions != tc.want {
			t.Fatalf("loaded options %+v, want %+v", s.BuilderOptions, tc.want)
		}
		if err := s.SaveTo(path); err != nil {
			t.Fatal(err)
		}
		restored, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if restored.BuilderOptions != tc.want {
			t.Fatal("saved options changed")
		}
	}
}
