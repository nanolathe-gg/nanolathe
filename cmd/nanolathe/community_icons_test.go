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

func TestConfiguredStrategicIconsDiscoversOneAuthoredPackageConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "iCoN")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ICONCFG.INI")
	if err := os.WriteFile(path, []byte("[Option]\nUseDefaultIcon=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	icons, err := configuredStrategicIcons(nil, root)
	if icons == nil || err != nil {
		t.Fatalf("directory selection = %v/%v", icons, err)
	}
}

func TestConfiguredStrategicIconsDirectoryMustBeUnambiguous(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "iconcfg.ini"), filepath.Join(root, "ZIcon", "iconcfg.ini")}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("[Option]\nUseDefaultIcon=true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	icons, err := configuredStrategicIcons(nil, root)
	if icons == nil || err == nil {
		t.Fatalf("ambiguous directory = %v/%v", icons, err)
	}
	for _, want := range []string{"logical path " + root, "exactly one iconcfg.ini", paths[0], paths[1]} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q missing %q", err, want)
		}
	}

	// An exact file remains an exact user choice even beside another package.
	icons, err = configuredStrategicIcons(nil, paths[0])
	if icons == nil || err != nil {
		t.Fatalf("explicit file selection = %v/%v", icons, err)
	}
}

func TestConfiguredStrategicIconsDirectoryReportsMissingConfig(t *testing.T) {
	root := t.TempDir()
	icons, err := configuredStrategicIcons(nil, root)
	if icons == nil || err == nil {
		t.Fatalf("empty directory = %v/%v", icons, err)
	}
	for _, want := range []string{"logical path " + root, "providers searched [filesystem]", "none found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q missing %q", err, want)
		}
	}
}
