package session

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// RX-06 gate policy meta-test [ON-10 §11]: a release gate must FAIL, not skip,
// when a stage is missing. Skipping is reserved for two cases: a retail-asset
// absence guard, and a gate explicitly registered below as disabled with the
// stage it currently fails at.
//
// The previous scanner looked only at files named strict_g*_test.go inside
// this one package. That missed strict_o6_natural_ai_test.go,
// strict_o6_result_test.go and both cmd/nanolathe/production_*_test.go files,
// so every production acceptance gate could carry an unconditional t.Skip
// while `go test ./...` stayed green. The scan now walks the whole repository
// and covers every strict_*_test.go and production_*_test.go file.

// disabledGateMarker must appear in the skip message of any registered
// disabled release gate. It is the only way a gate escapes the fail-not-skip
// rule other than a retail-asset guard.
const disabledGateMarker = "RELEASE-GATE-DISABLED"

// disabledGates is the exhaustive registry of release gates that are known to
// fail and are therefore skipped. The value records the stage the gate
// actually reaches, measured by running it with the skip removed — not the
// reason someone once guessed.
//
// The registry is bidirectionally enforced: a gate skipped without an entry
// fails this test, and an entry with no matching skip fails it too. A gate
// may only leave this map by being fixed.
var disabledGates = map[string]string{
	"cmd/nanolathe/production_o3_test.go::TestProductionInputARMEconomyBuildReplay":             "site scan finds no legal production site for armsolar (5x5 footprint) on retail Ashap Plateau; fails before any picking or build command is issued",
	"internal/session/strict_o6_natural_ai_test.go::TestStrictSkirmish_NaturalAIRealAssets":     "natural AI stalls in placement, not selection: candidate selection succeeds from tick 0 (CORMEX, score ~97) but no legal site is found until tick ~4771, so FactoryCompleted never arrives within 12000 ticks. The synthetic G5 gate exercises the same production and attack-wave path end to end and passes, which localizes the remaining gap to AI site search",
	"internal/session/strict_o6_result_test.go::TestStrictSkirmish_NaturalCommanderDeathResult": "typed attack is admitted but the weapon handshake never completes: aim=false, cob_return=false, fire=false within 600 ticks",
}

// gateFile reports whether a file name registers as a release gate.
func gateFile(name string) bool {
	if !strings.HasSuffix(name, "_test.go") {
		return false
	}
	return strings.HasPrefix(name, "strict_") || strings.HasPrefix(name, "production_")
}

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repository root from %s", dir)
	return ""
}

// enclosingTest returns the name of the top-level test function containing the
// given file offset, or "" when the offset is outside every test function.
func enclosingTest(fset *token.FileSet, file *ast.File, pos token.Pos) string {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil {
			continue
		}
		if pos >= fn.Body.Pos() && pos <= fn.Body.End() {
			return fn.Name.Name
		}
	}
	return ""
}

// assetGuard reports whether the lines around a skip identify it as a
// retail-asset absence guard, which is the one legitimate skip.
func assetGuard(lines []string, i int) bool {
	lo := i - 3
	if lo < 0 {
		lo = 0
	}
	hi := i + 2
	if hi > len(lines) {
		hi = len(lines)
	}
	lower := strings.ToLower(strings.Join(lines[lo:hi], "\n"))
	for _, needle := range []string{
		"retail", "totalannihilation", "asset", "nanolathe_ta_root",
		"catalog compile", "no network map",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func TestStrict_NoSyntheticSkips(t *testing.T) {
	root := repoRoot(t)
	found := 0
	seenDisabled := map[string]string{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip dot directories: .git and the per-agent worktrees under
			// .worktrees/.claude hold other branches' copies of these files.
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !gateFile(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "internal/session/strict_gate_policy_test.go" {
			return nil // this file names the skip API in its own scanner
		}
		found++

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, data, 0)
		if parseErr != nil {
			t.Errorf("%s: parse: %v", rel, parseErr)
			return nil
		}
		lines := strings.Split(string(data), "\n")

		for i, line := range lines {
			if !strings.Contains(line, "t.Skip(") && !strings.Contains(line, "t.Skipf(") {
				continue
			}
			if strings.Contains(line, disabledGateMarker) {
				pos := fset.File(parsed.Pos()).LineStart(i + 1)
				name := enclosingTest(fset, parsed, pos)
				if name == "" {
					t.Errorf("%s:%d: disabled-gate skip is not inside a test function", rel, i+1)
					continue
				}
				key := rel + "::" + name
				if _, ok := disabledGates[key]; !ok {
					t.Errorf("%s:%d: %s is skipped but is not in the disabledGates registry; "+
						"register it with the stage it actually fails at, or remove the skip", rel, i+1, key)
					continue
				}
				if prev, dup := seenDisabled[key]; dup {
					t.Errorf("%s:%d: %s has more than one disabled-gate skip (also %s)", rel, i+1, key, prev)
					continue
				}
				seenDisabled[key] = fmt.Sprintf("%s:%d", rel, i+1)
				continue
			}
			if assetGuard(lines, i) {
				continue
			}
			// Triage lane OW-R-REDS: G8 natural checkpoint synthetic skip is
			// allowed with explicit TODO(question) [ON-12][05 C18].
			lo := i - 3
			if lo < 0 {
				lo = 0
			}
			hi := i + 2
			if hi > len(lines) {
				hi = len(lines)
			}
			ctx := strings.ToLower(strings.Join(lines[lo:hi], "\n"))
			if strings.Contains(ctx, "todo(question)") && strings.Contains(ctx, "g8") {
				continue
			}
			t.Errorf("%s:%d: release gate skips instead of failing: %s", rel, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if found == 0 {
		t.Fatalf("no release gate files found; scan ran in wrong directory")
	}

	// The registry may not outlive the skips it documents: a gate that starts
	// passing must be removed from it in the same change that removes the skip.
	for key := range disabledGates {
		if _, ok := seenDisabled[key]; !ok {
			t.Errorf("disabledGates registers %q but no matching %s skip was found; "+
				"if the gate now runs, delete its registry entry", key, disabledGateMarker)
		}
	}
}

// TestStrict_ReleaseGateSummary prints the disabled-gate roster so a CI log
// states plainly which acceptance gates did not run and where each stops.
// It is a report, not an assertion; the enforcement lives above.
func TestStrict_ReleaseGateSummary(t *testing.T) {
	if len(disabledGates) == 0 {
		t.Log("release gates: all enabled")
		return
	}
	keys := make([]string, 0, len(disabledGates))
	for k := range disabledGates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("release gates disabled: %d", len(keys))
	for _, k := range keys {
		t.Logf("  DISABLED %s\n    stops at: %s", k, disabledGates[k])
	}
}
