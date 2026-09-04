// Package testsupport holds helpers shared by asset-gated tests.
package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RetailAssetsEnv is the variable that opts a run in to the retail corpus.
// RetailAssetsEnvLegacy is the older spelling, still honoured.
const (
	RetailAssetsEnv       = "NANOLATHE_RETAIL_ASSETS"
	RetailAssetsEnvLegacy = "NANOLATHE_TA_ROOT"
)

// RetailRoot returns the retail install root, SKIPPING the test unless a run
// has explicitly opted in by setting $NANOLATHE_RETAIL_ASSETS (or the legacy
// $NANOLATHE_TA_ROOT) to the install directory.
//
// The opt-in is the point. This helper used to fall back to
// ~/TotalAnnihilation whenever neither variable was set, which meant that on
// any developer machine with the game installed EVERY asset-gated test ran on
// every `go test ./...` — including the whole-corpus catalog compiles and the
// multi-thousand-tick headless sessions. Those dominate both wall time and
// memory: `go test` runs one package binary per CPU, and a dozen concurrent
// binaries each holding a compiled retail catalog exhausted a 16 GB machine.
// The fallback also made the local suite silently different from CI, which has
// no game install and therefore always skipped them.
//
// The two tiers this creates:
//
//   - default (and CI): no variable set, retail tests skip, the suite is the
//     small synthetic-fixture tier and stays fast enough to run on every edit;
//   - integration (pre-merge): the variable set, the full corpus runs. See
//     tools/check-retail.
//
// Callers that only need a catalog should use
// internal/testsupport/retailcat.Shared rather than compiling their own.
func RetailRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv(RetailAssetsEnv)
	if root == "" {
		root = os.Getenv(RetailAssetsEnvLegacy)
	}
	if root == "" {
		t.Skipf("retail assets are opt-in: set %s to the install root (see tools/check-retail)", RetailAssetsEnv)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		// An explicitly requested root that cannot be read is a broken
		// invocation, not an absent install: fail rather than skip, so a
		// typo in the variable does not look like a green run.
		t.Fatalf("%s=%q cannot be read: %v", RetailAssetsEnv, root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".hpi") {
			return root
		}
	}
	t.Fatalf("%s=%q holds no HPI archives", RetailAssetsEnv, root)
	return ""
}

// RetailRootIfPresent is RetailRoot's non-testing form for helpers that must
// decide without a *testing.T. It reports the root and whether the run opted in.
func RetailRootIfPresent() (string, bool) {
	root := os.Getenv(RetailAssetsEnv)
	if root == "" {
		root = os.Getenv(RetailAssetsEnvLegacy)
	}
	return root, root != ""
}
