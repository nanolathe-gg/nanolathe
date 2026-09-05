package architecture

// PROC-03 parity-drift ratchets (docs/PLAN_03_CORE_DETERMINISM.md, "Parity
// audit 2026-08-27 findings", row PROC-03/05 "remainder"): three shrink-only
// ratchets that keep existing parity drift from growing while the Wave-5
// cleanup (PROC-04/PROC-07 direction) works it off incrementally.
//
// Ratchet semantics, shared by all three guards: each guard scans non-test Go
// files in the authoritative packages and records occurrences per file in a
// committed baseline taken from the tree as of this commit. Current counts may
// decrease freely (a decrease means the baseline row should be tightened in
// the same commit); any increase fails and names the offending file and line.
// The per-file gate is deliberately strict: moving an occurrence between files
// is a baseline-table change that must be reviewed, not a silent side effect.
//
// The guards are source-inspecting AST scans and are independent of runtime
// wiring, like the other guards in this package. They enforce "no new debt";
// they do not certify the existing occurrences as correct.

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
		if dependency == "github.com/nanolathe/nanolathe/internal/client" ||
			dependency == "github.com/nanolathe/nanolathe/internal/audiobackend" ||
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

// failRatchet aborts the test when a guard's ratchet is exceeded.
func failRatchet(t *testing.T, guard string, failures []string) {
	t.Helper()
	if len(failures) != 0 {
		t.Fatalf("%s ratchet exceeded (PROC-03): %d offending site(s), counts may only decrease:\n%s",
			guard, len(failures), strings.Join(failures, "\n"))
	}
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

// --- Guard 1: map iteration (INVARIANTS I1) ---

// mapIterationBaseline records literal map-typed range occurrences in
// non-test authoritative files as of the baseline commit.
//
// Ratchet: the count per file may decrease; any increase fails. INVARIANTS I1
// forbids iterating a Go map in anything that can affect simulation state,
// because map iteration order is randomized per run and retail's order is the
// behavior. The scan is deliberately syntactic: it counts a range statement
// whose expression is literally a map type (a map type expression, including
// through parentheses, or a map composite literal). Ranging over a variable
// of map type, or over a call result, is out of scope here — it is caught by
// review against I1 and by the existing determinism wiring tests.
//
// Baseline is empty today: the authoritative packages currently contain no
// literal map-typed ranges, so this is a pure forward ratchet.
var mapIterationBaseline = map[string]int{}

// TestAuthoritativeMapIterationDoesNotGrow enforces the PROC-03 map-iteration
// ratchet (INVARIANTS I1): no new literal map-typed range may appear in the
// authoritative packages.
func TestAuthoritativeMapIterationDoesNotGrow(t *testing.T) {
	current := map[string]int{}
	sites := map[string][]occurrence{}
	scanAuthoritativeSources(t, func(relPath string, fset *token.FileSet, file *ast.File, lineText func(int) string) {
		perLine := map[int]string{}
		ast.Inspect(file, func(n ast.Node) bool {
			rangeStmt, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			expr := rangeStmt.X
			for {
				paren, ok := expr.(*ast.ParenExpr)
				if !ok {
					break
				}
				expr = paren.X
			}
			literalMap := false
			switch typed := expr.(type) {
			case *ast.MapType:
				literalMap = true
			case *ast.CompositeLit:
				_, literalMap = typed.Type.(*ast.MapType)
			}
			if !literalMap {
				return true
			}
			line := fset.Position(rangeStmt.Pos()).Line
			current[relPath]++
			perLine[line] = lineText(line)
			return true
		})
		if len(perLine) != 0 {
			sites[relPath] = siteList(perLine)
		}
	})
	failRatchet(t, "map iteration (I1)", enforceRatchet(t, "map iteration (I1)", mapIterationBaseline, current, sites))
}

// --- Guard 2: float64 occurrences (INVARIANTS I2) ---

// float64ExemptFiles mirrors the INVARIANTS I2 exhaustive float allowlist for
// the authoritative packages: files whose float64 use is named by an I2 row.
// Each entry carries the row it mirrors; the list only shrinks.
var float64ExemptFiles = map[string]string{
	"internal/clock/clock.go":              "I2 clock budget product (delta × speed + carry float64, carry float32) and clock float seconds [01 §4.2]",
	"internal/economy/admission.go":        "I2 economy settlement working-precision intermediates narrowed at named float32 stores [05 R-ECO-01 §1][05 R-ECO-01 §5]",
	"internal/economy/ledger.go":           "I2 economy cumulative totals and waste counters [05 \"Stocks, counters, and waste\"]",
	"internal/economy/maker.go":            "I2 economy production contributions and difficulty discounts use double working precision before named float32 stores; authored stockpile costs narrow at their documented boundary [05 R-ECO-01 §1][05 R-ECO-01 §3][05 \"Construction arithmetic\"]",
	"internal/economy/p28_parity_trace.go": "opt-in trace copy of ledger.go's I2 cumulative totals and waste counters (same I2 row; P28-OBS-00C)",
	"internal/movement/airorders.go":       "I2 AirStrike release lead sqrt((2·cruisealt)/gravity) · 30 · speedInteger, narrowed by truncation toward zero [04 R-AIR-01 §8]",
	"internal/movement/flight.go":          "I2 flight brake integration temporaries [04 §10.1], the shared air bearing [04 R-AIR-01 §1], and the lean accumulator's atan2 and rotation [04 R-AIR-01 §2] — all narrowed at the named fixed-point and uint16 angle stores",
	"internal/movement/integrate.go":       "I2 ground follower goal-point bearing and route-distance/lookahead hypot temporaries [04 R-MOV-01 §2][04 R-MOV-01 §3][04 R-MOV-03 §2][04 R-PATH-01 §8]",
	"internal/combat/aim.go":               "I2 ballistic discriminant, acos, sqrt [06 §3.3]",
	"internal/combat/impact.go":            "I2 area-damage range sqrt, float64 transient truncated to int32 [06 §9.3]",
	"internal/combat/damage.go":            "I2 area-damage amount product: the promoted base damage times the stored float32 falloff, truncated toward zero [06 §9.2]",
	"internal/sim/numeric/trig.go":         "I2 simulation trig-table construction, float64 transient [04 §5.1]",
	"internal/save/boxes.go":               "I2/I13 save float boxes: the game-time save box and account doubles are byte-layout contracts",
	"internal/save/bank.go":                "I13 HAPIBANK account record doubles are a byte-layout contract",
	"internal/session/strips.go":           "I2 nanolathe particle travel distance (sqrt, truncated to the tick count), float64 temporary never stored [03 §5.5]",
}

// float64Baseline records float64 occurrences per remaining (non-exempt)
// non-test authoritative file as of the baseline commit. An occurrence is a
// float64 type reference (var/const declaration, field, func parameter or
// result, conversion) or a floating-point basic literal (an untyped float
// constant defaults to float64).
//
// Ratchet: the count per file may decrease; any increase fails. INVARIANTS I2
// fixes the world as 16.16 fixed point with an exhaustive float allowlist;
// new float64 sites outside that list are parity drift.
var float64Baseline = map[string]int{
	"internal/ai/placement.go":              1,
	"internal/ai/selection.go":              2,
	"internal/ai/strategic.go":              5,
	"internal/cob/ports.go":                 6,
	"internal/combat/meteor.go":             10,
	"internal/combat/motion.go":             4,
	"internal/combat/service.go":            2,
	"internal/combat/stockpile.go":          5,
	"internal/construction/capture.go":      5,
	"internal/construction/factory.go":      18,
	"internal/construction/resurrection.go": 3,
	"internal/construction/reverse.go":      4,
	"internal/economy/tick.go":              2,
	"internal/mission/initial_mission.go":   8,
	"internal/mission/mission_globals.go":   6,
	"internal/mission/placement.go":         7,
	"internal/movement/altitude.go":         6,
	"internal/orders/pump.go":               4,
	// WU-18-2, the work handlers. Two sites, both retail's own floating point
	// and both narrowed immediately to the integer the record stores: the
	// capture budget's three float32 constants
	// (0.015·buildcostenergy + 0.2142857142857·buildcostmetal + 150.0,
	// [05 R-WORK-01 §6]) and the resurrection delay's stored double
	// (0.3·buildtime / (workertime/30), [05 R-WORK-01 §7], "the 0.3 belongs to
	// this state alone"). docs/INVARIANTS.md I2 has no row for either; WU-18-2
	// reports them as rows the allowlist needs rather than editing a file it
	// does not own. Reproducing them in rationals would change which values
	// truncate, so the arithmetic stays as retail computes it.
	"internal/orders/work.go":         6,
	"internal/session/progression.go": 5,
	"internal/session/session.go":     1,
	"internal/session/step.go":        1,
	"internal/units/units.go":         1,
	"internal/world/terrain.go":       1,
	"internal/world/wind.go":          2,
}

// TestAuthoritativeFloat64DoesNotGrow enforces the PROC-03 float ratchet
// (INVARIANTS I2): no new float64 occurrence may appear in the authoritative
// packages outside the I2-derived file exemptions above.
func TestAuthoritativeFloat64DoesNotGrow(t *testing.T) {
	current := map[string]int{}
	sites := map[string][]occurrence{}
	countExempt := map[string]int{}
	scanAuthoritativeSources(t, func(relPath string, fset *token.FileSet, file *ast.File, lineText func(int) string) {
		perLine := map[int]string{}
		count := 0
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				if node.Name == "float64" {
					count++
					perLine[fset.Position(node.Pos()).Line] = lineText(fset.Position(node.Pos()).Line)
				}
			case *ast.BasicLit:
				if node.Kind == token.FLOAT {
					count++
					perLine[fset.Position(node.Pos()).Line] = lineText(fset.Position(node.Pos()).Line)
				}
			}
			return true
		})
		if _, exempt := float64ExemptFiles[relPath]; exempt {
			countExempt[relPath] = count
			return
		}
		if count != 0 {
			current[relPath] = count
			sites[relPath] = siteList(perLine)
		}
	})
	for path := range float64ExemptFiles {
		if _, seen := countExempt[path]; !seen {
			t.Errorf("stale float64 exemption: %s is allowlisted but was not scanned", path)
		} else if countExempt[path] == 0 {
			t.Errorf("stale float64 exemption: %s no longer contains float64; shrink the I2 allowlist entry", path)
		}
	}
	failRatchet(t, "float64 (I2)", enforceRatchet(t, "float64 (I2)", float64Baseline, current, sites))
}
