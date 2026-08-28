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
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

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
	"internal/clock/clock.go":      "I2 clock budget product (delta × speed + carry float64, carry float32) and clock float seconds [01 §4.2]",
	"internal/economy/ledger.go":   "I2 economy cumulative totals and waste counters [05 \"Stocks, counters, and waste\"]",
	"internal/movement/flight.go":  "I2 flight brake integration temporaries, narrowed at the named fixed-point stores [04 §10.1]",
	"internal/combat/aim.go":       "I2 ballistic discriminant, acos, sqrt [06 §3.3]",
	"internal/sim/numeric/trig.go": "I2 simulation trig-table construction, float64 transient [04 §5.1]",
	"internal/save/boxes.go":       "I2/I13 save float boxes: the game-time save box and account doubles are byte-layout contracts",
	"internal/save/bank.go":        "I13 HAPIBANK account record doubles are a byte-layout contract",
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
	"internal/ai/placement.go":              4,
	"internal/ai/selection.go":              2,
	"internal/ai/strategic.go":              6,
	"internal/cob/ports.go":                 12,
	"internal/combat/impact.go":             6,
	"internal/combat/meteor.go":             10,
	"internal/combat/motion.go":             12,
	"internal/combat/service.go":            2,
	"internal/combat/stockpile.go":          5,
	"internal/construction/capture.go":      5,
	"internal/construction/factory.go":      2,
	"internal/construction/resurrection.go": 3,
	"internal/construction/reverse.go":      4,
	"internal/economy/maker.go":             11,
	"internal/economy/tick.go":              2,
	"internal/mission/initial_mission.go":   8,
	"internal/mission/mission_globals.go":   6,
	"internal/mission/placement.go":         7,
	"internal/movement/altitude.go":         6,
	"internal/orders/pump.go":               4,
	"internal/session/progression.go":       5,
	"internal/session/session.go":           1,
	"internal/session/step.go":              1,
	"internal/units/units.go":               1,
	"internal/world/terrain.go":             1,
	"internal/world/wind.go":                2,
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

// --- Guard 3: debt markers ---

// debtMarkerTokens are the audit's debt markers, matched case-insensitively as
// substrings of declared identifier names and of comment text. TODO(question)
// is the INVARIANTS I9 marker for a local gap with the question written out;
// the others are the audit's Wave-5 cleanup vocabulary (retail-cleanup
// findings PROC-04/PROC-07 direction).
var debtMarkerTokens = []string{
	"legacy",
	"compatibility",
	"fallback",
	"guess",
	"plausible",
	"todo(question)",
}

// debtMarkerTotals records the baseline total occurrences per token.
// debtMarkerFileCounts records the baseline occurrences per file per token;
// a file absent from the map has baseline zero.
//
// Ratchet: totals may decrease; any increase fails naming the file and line.
// The gate is the per-file count (the strictest form the count-only baseline
// supports): with per-file counts, a token total can only grow if some file
// exceeds its baseline row, and that file's new sites are exactly what gets
// reported. Moving a marker between files therefore also requires a reviewed
// baseline-table edit. The totals exist to document the audit scale and to
// keep the two tables consistent; TestAuthoritativeDebtMarkersDoNotGrow fails
// on a table that disagrees with itself.
var debtMarkerTotals = map[string]int{
	"legacy":         65,
	"compatibility":  33,
	"fallback":       211,
	"guess":          8,
	"plausible":      1,
	"todo(question)": 341,
}

var debtMarkerFileCounts = map[string]map[string]int{
	"legacy": {
		"internal/cob/vm.go":                  4,
		"internal/combat/service.go":          2,
		"internal/construction/factory.go":    2,
		"internal/construction/queue.go":      1,
		"internal/mission/initial_mission.go": 2,
		"internal/mission/load.go":            3,
		"internal/movement/integrate.go":      5,
		"internal/movement/steer.go":          2,
		"internal/orders/pump.go":             2,
		"internal/orders/resolve.go":          10,
		"internal/path/queue.go":              2,
		"internal/pool/pool.go":               6,
		"internal/session/scheduling.go":      1,
		"internal/session/step.go":            2,
		"internal/session/wind.go":            1,
		"internal/units/units.go":             5,
		"internal/world/coords.go":            4,
		"internal/world/placement.go":         4,
		"internal/world/plot.go":              1,
		"internal/world/terrain.go":           6,
	},
	"compatibility": {
		"internal/ai/manager.go":              2,
		"internal/ai/strategic.go":            1,
		"internal/cob/vm.go":                  1,
		"internal/combat/service.go":          2,
		"internal/construction/factory.go":    5,
		"internal/construction/queue.go":      1,
		"internal/mission/initial_mission.go": 2,
		"internal/movement/admission.go":      1,
		"internal/movement/integrate.go":      2,
		"internal/orders/pump.go":             3,
		"internal/orders/resolve.go":          1,
		"internal/orders/zbuildweapon.go":     2,
		"internal/path/queue.go":              2,
		"internal/pool/pool.go":               1,
		"internal/save/bank.go":               2,
		"internal/session/commands.go":        1,
		"internal/units/units.go":             1,
		"internal/visibility/fog.go":          1,
		"internal/world/placement.go":         2,
	},
	"fallback": {
		"internal/ai/groups.go":                  1,
		"internal/ai/manager.go":                 3,
		"internal/ai/placement.go":               6,
		"internal/ai/profile.go":                 7,
		"internal/ai/selection.go":               4,
		"internal/cob/binding.go":                1,
		"internal/cob/bridge.go":                 2,
		"internal/cob/load.go":                   1,
		"internal/cob/ports.go":                  2,
		"internal/cob/vm.go":                     11,
		"internal/combat/death.go":               1,
		"internal/combat/impact.go":              2,
		"internal/combat/meteor.go":              7,
		"internal/combat/motion.go":              1,
		"internal/combat/service.go":             6,
		"internal/combat/target.go":              9,
		"internal/construction/approach.go":      1,
		"internal/construction/factory.go":       10,
		"internal/construction/queue.go":         10,
		"internal/economy/ledger.go":             1,
		"internal/features/reproduce.go":         1,
		"internal/features/service.go":           3,
		"internal/features/sink.go":              1,
		"internal/mission/catalog.go":            3,
		"internal/mission/initial_mission.go":    12,
		"internal/mission/load.go":               15,
		"internal/mission/mission_globals.go":    13,
		"internal/mission/placement.go":          1,
		"internal/mission/schema.go":             1,
		"internal/movement/admission.go":         5,
		"internal/movement/cargo.go":             1,
		"internal/movement/collision.go":         2,
		"internal/movement/goals.go":             4,
		"internal/movement/integrate.go":         7,
		"internal/movement/profile.go":           2,
		"internal/movement/profile_footprint.go": 1,
		"internal/movement/steer.go":             1,
		"internal/movement/transport.go":         3,
		"internal/orders/pump.go":                2,
		"internal/orders/resolve.go":             6,
		"internal/orders/transport.go":           10,
		"internal/orders/zbuildweapon.go":        3,
		"internal/path/search.go":                1,
		"internal/pool/pool.go":                  1,
		"internal/session/audio.go":              1,
		"internal/session/composition.go":        1,
		"internal/session/mission.go":            1,
		"internal/session/publish.go":            2,
		"internal/session/result.go":             2,
		"internal/session/session.go":            3,
		"internal/session/skirmish.go":           1,
		"internal/session/step.go":               2,
		"internal/session/stockpile.go":          1,
		"internal/units/cob_binding.go":          1,
		"internal/units/units.go":                3,
		"internal/world/placement.go":            3,
		"internal/world/terrain.go":              5,
	},
	"guess": {
		"internal/combat/pool.go":          1,
		"internal/construction/factory.go": 1,
		"internal/construction/reclaim.go": 1,
		"internal/movement/integrate.go":   1,
		"internal/session/commands.go":     1,
		"internal/session/publish.go":      1,
		"internal/units/cob_binding.go":    1,
		"internal/world/placement.go":      1,
	},
	"plausible": {
		"internal/world/placement.go": 1,
	},
	"todo(question)": {
		"internal/ai/manager.go":                   2,
		"internal/ai/placement.go":                 4,
		"internal/ai/profile.go":                   5,
		"internal/ai/selection.go":                 4,
		"internal/ai/strategic.go":                 6,
		"internal/cob/load.go":                     2,
		"internal/cob/ports.go":                    19,
		"internal/cob/vm.go":                       7,
		"internal/combat/death.go":                 6,
		"internal/combat/fire.go":                  3,
		"internal/combat/impact.go":                1,
		"internal/combat/meteor.go":                2,
		"internal/combat/motion.go":                2,
		"internal/combat/service.go":               2,
		"internal/combat/slots.go":                 1,
		"internal/combat/stockpile.go":             12,
		"internal/combat/target.go":                3,
		"internal/construction/approach.go":        6,
		"internal/construction/capture.go":         4,
		"internal/construction/factory.go":         17,
		"internal/construction/reclaim.go":         1,
		"internal/construction/reverse.go":         1,
		"internal/economy/ledger.go":               6,
		"internal/economy/maker.go":                1,
		"internal/economy/tick.go":                 4,
		"internal/features/burn.go":                1,
		"internal/mission/campaign_progression.go": 3,
		"internal/mission/initial_mission.go":      4,
		"internal/mission/load.go":                 2,
		"internal/mission/mission_globals.go":      4,
		"internal/movement/admission.go":           4,
		"internal/movement/altitude.go":            1,
		"internal/movement/cargo.go":               3,
		"internal/movement/collision.go":           2,
		"internal/movement/flight.go":              3,
		"internal/movement/goals.go":               16,
		"internal/movement/integrate.go":           5,
		"internal/movement/landing.go":             1,
		"internal/movement/movegoal.go":            1,
		"internal/movement/profile.go":             3,
		"internal/movement/route.go":               3,
		"internal/movement/transport.go":           14,
		"internal/orders/pump.go":                  13,
		"internal/orders/resolve.go":               37,
		"internal/orders/table.go":                 1,
		"internal/orders/transport.go":             25,
		"internal/path/goals.go":                   4,
		"internal/path/queue.go":                   6,
		"internal/path/search.go":                  5,
		"internal/pool/pool.go":                    3,
		"internal/save/bank.go":                    3,
		"internal/session/commands.go":             1,
		"internal/session/mission.go":              3,
		"internal/session/progression.go":          4,
		"internal/session/result.go":               5,
		"internal/session/retail_load.go":          1,
		"internal/session/session.go":              3,
		"internal/session/skirmish.go":             8,
		"internal/session/state.go":                1,
		"internal/session/step.go":                 4,
		"internal/sim/numeric/numeric.go":          1,
		"internal/sim/numeric/trig.go":             1,
		"internal/sim/rng/rng.go":                  1,
		"internal/units/pipeline.go":               2,
		"internal/units/types.go":                  1,
		"internal/units/units.go":                  5,
		"internal/visibility/fog.go":               1,
		"internal/world/feature_stamp.go":          1,
		"internal/world/placement.go":              4,
		"internal/world/plot.go":                   3,
		"internal/world/terrain.go":                1,
		"internal/world/wind.go":                   2,
	},
}

// debtMarkerOccurrences returns, for one file, the number of occurrences of
// each debt-marker token: declared identifier names (type, value, field, and
// function declaration names) and comment text, each matched
// case-insensitively as a substring. A multi-line block comment counts once,
// at its opening line; line comments count one per line.
func debtMarkerOccurrences(fset *token.FileSet, file *ast.File, lineText func(int) string) (map[string]int, map[int]string) {
	counts := map[string]int{}
	perLine := map[int]string{}
	line := func(pos token.Pos) int { return fset.Position(pos).Line }
	record := func(kind, name string, at int) {
		lower := strings.ToLower(name)
		for _, marker := range debtMarkerTokens {
			if strings.Contains(lower, marker) {
				counts[marker]++
				perLine[at] = kind + " " + lineText(at)
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			record("decl", node.Name.Name, line(node.Name.Pos()))
		case *ast.TypeSpec:
			record("decl", node.Name.Name, line(node.Name.Pos()))
		case *ast.ValueSpec:
			for _, name := range node.Names {
				record("decl", name.Name, line(name.Pos()))
			}
		case *ast.Field:
			for _, name := range node.Names {
				record("decl", name.Name, line(name.Pos()))
			}
		}
		return true
	})
	for _, group := range file.Comments {
		for _, comment := range group.List {
			record("comment", comment.Text, line(comment.Pos()))
		}
	}
	return counts, perLine
}

// TestAuthoritativeDebtMarkersDoNotGrow enforces the PROC-03 debt-marker
// ratchet (audit Wave-5 gate, PROC-04/PROC-07 direction): the debt-marker
// vocabulary may only shrink in the authoritative packages. It complements
// INVARIANTS I9 (unknowns stay unknown in TODO markers) and I11 (one
// behavior, no compatibility paths) by keeping the existing vocabulary from
// growing while the cleanup works it off.
func TestAuthoritativeDebtMarkersDoNotGrow(t *testing.T) {
	// Baseline tables must agree with themselves before they gate anything.
	for _, marker := range debtMarkerTokens {
		sum := 0
		for _, count := range debtMarkerFileCounts[marker] {
			sum += count
		}
		if sum != debtMarkerTotals[marker] {
			t.Fatalf("debt-marker baseline inconsistent for %q: file counts sum to %d, total says %d", marker, sum, debtMarkerTotals[marker])
		}
	}

	current := map[string]map[string]int{} // marker -> file -> count
	sites := map[string]map[string][]occurrence{}
	scanAuthoritativeSources(t, func(relPath string, fset *token.FileSet, file *ast.File, lineText func(int) string) {
		counts, perLine := debtMarkerOccurrences(fset, file, lineText)
		for _, marker := range debtMarkerTokens {
			if counts[marker] == 0 {
				continue
			}
			if current[marker] == nil {
				current[marker] = map[string]int{}
				sites[marker] = map[string][]occurrence{}
			}
			current[marker][relPath] = counts[marker]
			sites[marker][relPath] = siteList(perLine)
		}
	})
	var debtFailures []string
	for _, marker := range debtMarkerTokens {
		failures := enforceRatchet(t, "debt marker "+marker+" (PROC-03)", debtMarkerFileCounts[marker], current[marker], sites[marker])
		for _, failure := range failures {
			debtFailures = append(debtFailures, marker+": "+failure)
		}
	}
	failRatchet(t, "debt markers (PROC-03)", debtFailures)
}
