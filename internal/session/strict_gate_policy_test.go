package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RX-06 gate policy meta-test [ON-10 §11]: synthetic strict gates must FAIL,
// never skip, when a stage is missing. Skipping is reserved for retail-asset
// absence guards. This scan keeps it that way.
func TestStrict_NoSyntheticSkips(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	found := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "strict_g") || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "strict_gate_policy_test.go" {
			continue // this file mentions the skip API in its own scanner
		}
		found++
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "t.Skip(") && !strings.Contains(line, "t.Skipf(") {
				continue
			}
			// Allow asset-absence guards only: the skip line or its neighbors
			// must reference the retail asset guard.
			ctx := strings.Join(lines[maxInt(0, i-3):minInt(len(lines), i+2)], "\n")
			lower := strings.ToLower(ctx)
			if strings.Contains(lower, "retail") ||
				strings.Contains(lower, "totalannihilation") ||
				strings.Contains(lower, "asset") ||
				strings.Contains(lower, "nanolathe_ta_root") ||
				strings.Contains(lower, "catalog compile") ||
				strings.Contains(lower, "no network map") {
				continue
			}
			t.Errorf("%s:%d: synthetic strict gate skips instead of failing: %s", name, i+1, strings.TrimSpace(line))
		}
	}
	if found == 0 {
		t.Fatalf("no strict gate files found; scan ran in wrong directory")
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
