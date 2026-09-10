package architecture

// PROC-03 parity-drift ratchets, defined by docs/DESIGN_RUNTIME_DETERMINISM.md
// §3.3 "Determinism tokens": type-checked map ranges [I1] and float64
// occurrences [I2] keep existing parity drift from growing during cleanup.
// Map traversals pin the audited range and containing function. Numeric
// storage fields have individual allowances; existing I2 operations in
// formerly exempt files have declaration-scoped counts. Other float sites
// retain shrink-only per-file counts, tightened when an occurrence disappears.
//
// These source guards require review for new or moved occurrences. They do
// not certify existing arithmetic or runtime wiring. The adjacent boundary
// guard keeps displayless commands independent of client/device packages.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestHeadlessCommandHasNoDesktopDependency locks the SP-REV-00 process
// topology: the displayless command may enter internal/session, but its import
// closure cannot reach the Ebitengine device packages or internal/client [I6].
func TestHeadlessCommandHasNoDesktopDependency(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./cmd/nanolathe-headless")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("list headless dependencies: %v", err)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == "github.com/nanolathe-gg/nanolathe/internal/client" ||
			dependency == "github.com/nanolathe-gg/nanolathe/internal/audiobackend" ||
			strings.HasPrefix(dependency, "github.com/hajimehoshi/ebiten/v2") {
			t.Fatalf("displayless command imports desktop dependency %s", dependency)
		}
	}
}

// occurrence is one counted site, kept only for failure messages.
type occurrence struct {
	line int
	text string
}

// scanAuthoritativeSources walks the authoritative packages and visits every
// non-test Go file with comments preserved. lineText returns the trimmed
// source line for failure messages.
func scanAuthoritativeSources(t *testing.T, visit func(relPath string, fset *token.FileSet, file *ast.File, lineText func(int) string)) {
	t.Helper()
	root := repositoryRoot(t)
	for _, relativeDir := range authoritativeDirs {
		dir := filepath.Join(root, filepath.FromSlash(relativeDir))
		if _, err := os.Stat(dir); err != nil {
			continue // package not present in this worktree
		}
		err := guardWalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			// ParseComments is required for the debt-marker guard; without
			// it the parser drops every comment and f.Comments stays empty.
			file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			lines := strings.Split(string(source), "\n")
			lineText := func(line int) string {
				if line < 1 || line > len(lines) {
					return ""
				}
				snippet := strings.TrimSpace(lines[line-1])
				if len(snippet) > 120 {
					snippet = snippet[:117] + "..."
				}
				return snippet
			}
			visit(filepath.ToSlash(relative), fset, file, lineText)
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", relativeDir, err)
		}
	}
}

// enforceRatchet compares current per-file counts against the committed
// baseline. Counts may decrease; a decrease logs a hint to tighten the
// baseline row in the same commit. It returns one offending site line for
// every file over its baseline; the caller decides how to report them.
func enforceRatchet(t *testing.T, guard string, baseline map[string]int, current map[string]int, sites map[string][]occurrence) []string {
	t.Helper()
	var failures []string
	for path, count := range current {
		allowed := baseline[path]
		if count < allowed {
			t.Logf("%s: %s shrank %d -> %d; tighten its baseline row in this commit", guard, path, allowed, count)
		}
		if count <= allowed {
			continue
		}
		for _, site := range sites[path] {
			failures = append(failures, fmt.Sprintf("%s:%d: %s", path, site.line, site.text))
		}
	}
	for path, allowed := range baseline {
		if _, present := current[path]; !present && allowed > 0 {
			t.Logf("%s: %s has no occurrences left (baseline %d); tighten its baseline row in this commit", guard, path, allowed)
		}
	}
	sort.Strings(failures)
	return failures
}

// siteList converts per-line sites into a sorted slice for failure messages.
func siteList(perLine map[int]string) []occurrence {
	lines := make([]int, 0, len(perLine))
	for line := range perLine {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	out := make([]occurrence, 0, len(lines))
	for _, line := range lines {
		out = append(out, occurrence{line: line, text: perLine[line]})
	}
	return out
}

// TestAuthoritativeMapIterationDoesNotGrow enforces the PROC-03 map-iteration
// guard (INVARIANTS I1). Go type information makes local maps, selector maps,
// named map types and map-returning calls equally visible to the check.
func TestAuthoritativeMapIterationDoesNotGrow(t *testing.T) {
	checkAuthoritativeTypedMapRanges(t)
}

// --- Guard 2: float64 occurrences (INVARIANTS I2) ---

// float64Baseline records float64 occurrences per non-test authoritative file
// whose sites are not separately authorized by a declaration-scoped record.
// An occurrence is a
// float64 type reference (var/const declaration, field, func parameter or
// result, conversion) or a floating-point basic literal (an untyped float
// constant defaults to float64).
//
// Ratchet: the count per file may decrease; any increase fails. INVARIANTS I2
// fixes the world as 16.16 fixed point with an exhaustive float allowlist;
// new float64 sites outside that list are parity drift.
var float64Baseline = map[string]int{
	"internal/ai/placement.go":     1,
	"internal/ai/selection.go":     2,
	"internal/ai/strategic.go":     5,
	"internal/cob/ports.go":        6,
	"internal/combat/motion.go":    4,
	"internal/combat/service.go":   2,
	"internal/combat/stockpile.go": 5,
	// Construction arithmetic follows [05 "Construction arithmetic"] and
	// [05 R-WORK-01 §3]. The refund sites share the final-store discount in
	// reverse.go [05 R-ECO-01 §3][05 R-ECO-01 §11].
	"internal/construction/arithmetic.go":   16,
	"internal/construction/inheritance.go":  0,
	"internal/construction/resurrection.go": 3,
	"internal/construction/reverse.go":      6, // shared refund final-store precision [05 R-ECO-01 §3][05 R-ECO-01 §11]
	"internal/economy/tick.go":              2,
	"internal/mission/initial_mission.go":   8,
	"internal/movement/altitude.go":         6,
	"internal/world/terrain.go":             2, // two single-precision tidal defaults [03 R-TERR-01 §6]
	"internal/world/wind.go":                2,
}

// TestAuthoritativeFloat64DoesNotGrow enforces the PROC-03 float ratchet
// (INVARIANTS I2). Existing I2 operations in formerly exempt files are
// declaration-scoped, so unrelated new floating-point state cannot pass.
func TestAuthoritativeFloat64DoesNotGrow(t *testing.T) {
	checkAuthoritativeFloat64(t)
}
