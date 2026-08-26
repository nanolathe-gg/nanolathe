// Package testsupport holds helpers shared by asset-gated tests.
package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// RetailRoot returns the retail install root, skipping the test when the
// assets are absent. Every asset-dependent test guards itself this way so the
// suite passes on a machine with no game installed (AGENTS.md §Test policy).
// Retail assets are opt-in via $NANOLATHE_RETAIL_ASSETS or the legacy
// $NANOLATHE_TA_ROOT (either may be set, the former takes precedence).
func RetailRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("NANOLATHE_RETAIL_ASSETS")
	if root == "" {
		root = os.Getenv("NANOLATHE_TA_ROOT")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("retail assets not present: no home directory")
		}
		root = filepath.Join(home, "TotalAnnihilation")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("retail assets not present at %s", root)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".hpi" {
			return root
		}
	}
	t.Skipf("no HPI archives under %s", root)
	return ""
}
