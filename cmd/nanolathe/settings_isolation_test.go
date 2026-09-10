package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Command and input tests can reach the production write-all settings path.
// Keep the entire suite away from the player's preferences, including when
// the caller supplied NANOLATHE_SETTINGS. Individual tests may override this
// scratch path with t.Setenv when they need independent persisted state.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nanolathe-test-settings-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv(settings.EnvPath, filepath.Join(dir, "settings.json")); err != nil {
		os.RemoveAll(dir)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
