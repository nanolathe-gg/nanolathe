package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredStrategicIconsReturnsBuiltInCatalogWithDiagnostic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.ini")
	if err := os.WriteFile(path, []byte("[Option]\nUseDefaultIcon=false\n[Icon]\nALL=missing.pcx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	icons, err := configuredStrategicIcons(nil, path)
	if icons == nil || err == nil {
		t.Fatalf("fallback/error = %v/%v", icons, err)
	}
	for _, text := range []string{"logical path " + path, "providers searched [filesystem]", "missing.pcx"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("diagnostic %q missing %q", err, text)
		}
	}
}
