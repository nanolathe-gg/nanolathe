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
	// Reconciled against the authoritative-source census after the merged
	// cleanups. These are shrink-only counts; changing a marker's location or
	// adding one still requires an explicit reviewed baseline update.
	"legacy": 36,
	// 17 -> 18: PT3 added one in internal/orders/pump.go. It is a comment that
	// NAMES an existing compatibility synthesis rather than introducing one —
	// Queue.Binding() has always read the legacy value-fixture fields without
	// caching, and the new sentence records that it survives only while
	// construction and older fixture callers write Queue.Lookup/Hostility/
	// StockpileEconomy directly. Reviewed and accepted as a documentation
	// gain: the debt was already there and unlabelled.
	"compatibility": 18,
	// O3 records three explicit fallback notes at the sites where the cited
	// research leaves the AirToAir interrupt mask unresolved.
	"fallback":  186,
	"guess":     10,
	"plausible": 1,
	// 323 at the point both units branched. WU-17-11 moved one marker out of
	// the scanned authoritative dirs (into internal/render, which this ratchet
	// does not scan), and WU-17-14 added two in internal/orders. Composed:
	// 323 − 1 + 2 = 324. WU-18-0 then retired one in internal/orders/pump.go:
	// the pump's phase-0 gate clear carried "TODO(question) exact gate
	// semantics", asking what the descriptor's static mask meant as a wait.
	// [04 §3.1] and [04 §3.3] answer it — the static mask is insertion
	// metadata and the dynamic gate is the wait — so the clear and its question
	// both went with the record constructor's correction. Composed: 324 − 1.
	// WU-18-4 then added five in the new internal/orders/combat.go — the five
	// questions [04 R-ORD-01 §3] and [04 R-AIR-01 §8] leave open at the sites
	// that depend on them, enumerated in that file's baseline row below.
	// Composed: 323 + 5 = 328. Three family units then landed in parallel and
	// each composed against 328 without seeing the others: WU-18-1 added three
	// (two in standing.go, one in selfdestruct.go) and WU-18-2 three more in
	// work.go. Both branches wrote 331 by their own arithmetic; composed the
	// total is 323 + 5 + 3 + 3 = 334, and every file row below survives.
	// WU-18-3 then added two in the new internal/orders/vtolwork.go — the two
	// questions [04 R-ORD-01 §7] and [05 R-WORK-01 §8] leave open, enumerated
	// in that file's baseline row below. Composed: 334 + 2 = 336.
	// WU-18-5 then added six in internal/movement/airorders.go — the six
	// questions [04 R-AIR-01 §8] and [04 R-ORD-02 §3, §4] leave open at the
	// sites the seven pump-driven air executors depend on, enumerated in that
	// file's baseline row below. Its own new file, internal/orders/vtolair.go,
	// adds none: every open question its handlers depend on is one WU-18-4
	// already recorded in combat.go. Composed: 336 + 6 = 342.
	// WU-18-8 branched from the same 336 and did not see WU-18-5: it added one
	// in the new internal/orders/patrol.go and retired two in
	// internal/orders/resolve.go with the second pursuit-leash helper (both
	// rows below carry the reasoning). Composed across both: 336 + 6 + 1 − 2 =
	// 341, and every file row below survives.
	// The nanoframe-decay gate then added one in
	// internal/construction/factory.go: [04 R-ORD-01 §5]'s measurement closes
	// whether the decay runs while a builder works, but not the producer or
	// encoding of the `0x8000` wake it stands in for. The first-selection
	// build-page default added one in internal/session/commands.go: the
	// behaviour is a direct retail observation recorded under
	// [07 R-HUD-03 §6], but no traced writer sets status bit 22 at unit
	// creation. Composed: 341 + 1 + 1 = 343.
	// The PT3 playtest round then retired three, one per file, each because the
	// question was answered rather than moved: internal/movement/airorders.go
	// 10 -> 9 (VTOL_LandIfCan's altitude offset — [04 R-AIR-01] had the two
	// branches reversed, and the correction closes the contradiction the
	// question flagged); internal/movement/integrate.go 5 -> 4 and
	// internal/orders/pump.go 5 -> 4 (the route acceptance rule's entry point
	// and the synthetic-fallback suppressor — [04 R-PATH-01 §8]'s corrected
	// text and its closed TODO(question), the record completion flag the
	// primary pump's code-9 arm sets). Composed: 343 − 3 = 340.
	// B1 then closed three result/skirmish questions, retained one pre-existing
	// movement marker, and added two explicit commander-death gaps: net 339.
	// O3 adds two net markers: the explicit AirToAir mask and omitted
	// control-byte/save questions, less the retired combat placeholder. B2
	// then closes one result question while adding two exact Unknown sites
	// for commander save keys and cargo provenance: net 342.
	"todo(question)": 342,
}

var debtMarkerFileCounts = map[string]map[string]int{
	"legacy": {
		"internal/cob/vm.go":                  3,
		"internal/construction/queue.go":      2,
		"internal/mission/initial_mission.go": 1,
		"internal/mission/load.go":            3,
		"internal/movement/integrate.go":      5,
		"internal/movement/steer.go":          2,
		"internal/orders/pump.go":             2,
		"internal/orders/resolve.go":          1,
		"internal/session/scheduling.go":      1,
		"internal/session/step.go":            1,
		"internal/world/coords.go":            4,
		"internal/world/placement.go":         4,
		"internal/world/plot.go":              1,
		"internal/world/terrain.go":           6,
	},
	"compatibility": {
		"internal/combat/service.go":          1,
		"internal/construction/factory.go":    3,
		"internal/construction/queue.go":      1,
		"internal/mission/initial_mission.go": 1,
		"internal/movement/admission.go":      1,
		"internal/movement/goals.go":          1,
		"internal/movement/integrate.go":      2,
		// pump.go 2 -> 3: the Binding() compatibility-synthesis note; see the
		// totals row above.
		"internal/orders/pump.go":      3,
		"internal/save/bank.go":        2,
		"internal/session/commands.go": 1,
		"internal/visibility/fog.go":   1,
		"internal/world/placement.go":  1,
	},
	"fallback": {
		"internal/ai/groups.go":                  1,
		"internal/ai/profile.go":                 4,
		"internal/cob/binding.go":                1,
		"internal/cob/bridge.go":                 2,
		"internal/cob/load.go":                   1,
		"internal/cob/snapshot.go":               1,
		"internal/cob/ports.go":                  2,
		"internal/cob/vm.go":                     14,
		"internal/combat/death.go":               1,
		"internal/combat/meteor.go":              7,
		"internal/combat/motion.go":              1,
		"internal/combat/service.go":             4,
		"internal/combat/target.go":              9,
		"internal/construction/approach.go":      1,
		"internal/construction/factory.go":       5,
		"internal/construction/queue.go":         10,
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
		"internal/movement/airorders.go":         1,
		"internal/orders/vtolair.go":             2,
		"internal/movement/cargo.go":             1,
		"internal/movement/collision.go":         2,
		"internal/movement/goals.go":             4,
		"internal/movement/integrate.go":         6,
		"internal/movement/profile.go":           2,
		"internal/movement/profile_footprint.go": 1,
		"internal/movement/steer.go":             1,
		"internal/movement/transport.go":         3,
		"internal/orders/pump.go":                2,
		"internal/orders/resolve.go":             4,
		"internal/orders/transport.go":           10,
		"internal/orders/zbuildweapon.go":        3,
		"internal/session/audio.go":              1,
		"internal/session/composition.go":        1,
		"internal/session/mission.go":            1,
		"internal/session/publish.go":            2,
		"internal/session/result.go":             2,
		"internal/session/session.go":            3,
		"internal/session/skirmish.go":           1,
		"internal/session/step.go":               2,
		"internal/session/stockpile.go":          1,
		"internal/units/units.go":                4,
		"internal/units/cob_binding.go":          1,
		"internal/world/placement.go":            1,
		"internal/world/terrain.go":              5,
	},
	"guess": {
		"internal/combat/pool.go":               1,
		"internal/construction/factory.go":      1,
		"internal/construction/reclaim.go":      1,
		"internal/movement/integrate.go":        1,
		"internal/movement/p28_parity_trace.go": 1,
		"internal/path/queue.go":                1,
		"internal/path/search.go":               1,
		"internal/session/commands.go":          1,
		"internal/session/publish.go":           1,
		"internal/world/placement.go":           1,
	},
	"plausible": {
		"internal/world/placement.go": 1,
	},
	"todo(question)": {
		"internal/ai/manager.go":            4,
		"internal/ai/placement.go":          3,
		"internal/ai/profile.go":            4,
		"internal/ai/selection.go":          4,
		"internal/ai/strategic.go":          5,
		"internal/cob/load.go":              2,
		"internal/cob/ports.go":             5,
		"internal/cob/vm.go":                3,
		"internal/combat/death.go":          6,
		"internal/combat/damage.go":         5,
		"internal/combat/fire.go":           3,
		"internal/combat/impact.go":         1,
		"internal/combat/meteor.go":         2,
		"internal/combat/motion.go":         2,
		"internal/combat/service.go":        2,
		"internal/combat/slots.go":          1,
		"internal/combat/stockpile.go":      12,
		"internal/combat/target.go":         3,
		"internal/construction/approach.go": 6,
		"internal/construction/capture.go":  4,
		// +1: the nanoframe-decay gate of [04 R-ORD-01 §5] leaves the producer
		// and encoding of `GetBuilt`'s wake bit `0x8000` open at the site that
		// reproduces its measured effect. The behaviour half of that Unknown is
		// closed in doc 04; the bit itself is not.
		"internal/construction/factory.go":         17, // -1 then +1: the retired FlagActivated placeholder took its TODO with it (PLAN_16 WU-16-3); the GetBuilt decay gate added one
		"internal/construction/reclaim.go":         1,
		"internal/construction/reverse.go":         1,
		"internal/economy/ledger.go":               3,
		"internal/economy/tick.go":                 3,
		"internal/features/burn.go":                2,
		"internal/mission/campaign_progression.go": 3,
		"internal/mission/initial_mission.go":      4,
		"internal/mission/load.go":                 2,
		"internal/mission/mission_globals.go":      4,
		"internal/movement/admission.go":           4,
		"internal/movement/altitude.go":            1,
		"internal/movement/cargo.go":               1,
		"internal/movement/collision.go":           2,
		// flight.go +1 (3 -> 4) and the new flightcommand.go row: WU-17-2 records
		// the two gaps [04 R-AIR-01] leaves open — the shared coordinate-pair
		// rotation's sign convention, and the command block's flags byte, whose
		// reader, clearing site and initial value the section does not name. Both
		// are honest gaps written at their site per I9.
		"internal/movement/flight.go":        4,
		"internal/movement/flightcommand.go": 1,
		// airorders.go 0 -> 4: WU-17-3 records the four gaps the air sections
		// leave open at the sites that depend on them. (a) [04 R-AIR-01 §4] says
		// a follow marker's goal is the target's *attach-piece* world position,
		// and this package has no compiled piece transform to evaluate, so the
		// marker takes the target's origin — that transform's unit-origin term
		// with a zero piece offset. (b) [04 R-AIR-01 §7] has VTOL_Standby phase 1
		// ask the ordinary autonomous acquisition for a target; acquisition is
		// the combat layer's and is not reachable here, so the no-target arm
		// stands. (c) [04 R-AIR-01 §6] calls a landing-legality test in
		// VTOL_LandIfCan phase 1 but neither states its predicate nor cites a
		// section that does. All three are written at their site with the
		// question and its decider, per I9. (d) [04 R-AIR-01 §6]'s gloss that
		// VTOL_LandIfCan phase 1 commands "exactly the terrain height" does not
		// follow from [04 R-AIR-01 §4]'s Established setter expression; the code
		// follows the Established expression and records the disagreement.
		// airorders.go 4 -> 10: WU-18-5 landed the seven pump-driven air
		// executors in the same file and recorded six more questions the air
		// sections leave open at the sites that depend on them. (e) The
		// base-candidate scan four sections invoke ("the base candidates within
		// 0xF00 for my side") has no admission predicate: [04 R-ORD-02 §4]
		// enumerates the scan visitors and defines only two others, so the list
		// is reported empty rather than chosen. (f) [04 R-ORD-02 §4]'s
		// guard-candidate visitor turns on a diplomacy-byte polarity the section
		// states ambiguously; implementing either reading would invert who a
		// seeking guard attaches itself to. (g) and (h) [04 R-AIR-01 §8] says
		// VTOL_Evade and AirToGroundHover use "record scratch words" without
		// naming which of p1..p3 holds them. (i) Neither AirToAir leg is said to
		// set the velocity payload's steer-to-heading flag, which changes that
		// payload's arrival test. (j) §8 gives no arm for AirToAir phase 1 with
		// the arrival bits clear, the counter below 0x5A, and the range to the
		// target at or below 0xA0.
		// airorders.go 10 -> 9 and integrate.go 5 -> 4: PT3 closed one question
		// in each (the landing altitude offset, and the route acceptance rule's
		// entry point). See the totals row above.
		"internal/movement/airorders.go": 9,
		"internal/movement/goals.go":     16,
		"internal/movement/integrate.go": 5,
		"internal/movement/landing.go":   1,
		"internal/movement/movegoal.go":  1,
		"internal/movement/profile.go":   3,
		"internal/movement/route.go":     2,
		"internal/movement/transport.go": 14,
		// pump.go 5 -> 6 and stop.go 0 -> 1: WU-17-14 implements the `Stop`
		// handler of [04 R-ORD-01 §2] and records the two gaps it ran into.
		// (a) [04 R-ORD-01 §1] gives the handler-side head insert as a link
		// change plus the displaced head's auto-flag inheritance and says
		// nothing about the insertion (active) marker, so PushHead states which
		// way it leaves the marker and what would settle it. (b) [R-ORDER-02
		// §2] separates the three weapon-target-clear entry points only by
		// their latch guard, so what the unconditional entry the `Stop` row
		// calls does to the slot control byte is unstated. Both are written at
		// their site with the question and its decider, per I9.
		// 6 -> 5: WU-18-0 retired the phase-0 gate clear's question (see the
		// totals row above). 5 -> 4: PT3 closed [04 R-PATH-01 §8]'s question on
		// the record flag that suppresses the synthetic fallback — it is the
		// completion flag the primary pump's code-9 arm sets.
		"internal/orders/pump.go": 4,
		// combat.go 0 -> 5: WU-18-4 records the five questions its rows leave
		// open, each at the site that depends on it. (a) [R-ORDER-02 §2]'s
		// weapon-target-clear guard reads a slot control byte — bit 1 assigned,
		// bit 4 inhibit — that this build's units.Slot does not have, and
		// [04 §3.9]'s own missing list still carries that byte's bit 4 as
		// unlocated, so release/inhibit reproduce only the empty test.
		// (b) [04 R-ORD-01 §3] defers `Suppress`'s engagement distance to doc 06,
		// which does not define it, and [04 §3.9] lists the same helper's value
		// as inference; no distance is chosen, which leaves phase 2 on its own
		// `p2 < 1` arm. (c) [04 R-ORD-01 §2]'s `AttackSpecial` row describes only
		// the resolving case and not what the handler does when command code 3
		// rejects. (d) [04 R-AIR-01 §8]'s entry step 1 gates its seek-attack
		// replacement on a record successor marker, two status-word bits and a
		// "cached goal valid" bit, none of which the section locates in a named
		// field. (e) the same section gives step 1's interrupt mask for three of
		// the four air executors and omits `AirToAir`, whose mask therefore
		// remains unstated. All five are written at their site with the question
		// and its decider, per I9.
		"internal/orders/combat.go": 4,
		// patrol.go 0 -> 1 and resolve.go 36 -> 34: WU-18-8. The one new
		// question is the successor test the patrol cycle turns on —
		// [04 R-ORD-01 §4] words it "a next patrol record exists" and
		// [04 R-ORD-02 §2] words the air move's as "no successor", and neither
		// says whether the patrol form additionally filters the successor by
		// the chain-member mask bit; the readings differ only for a patrol with
		// an unrelated order queued behind it, and the site states which it
		// takes and what would settle it (I9). The two retired in resolve.go
		// went with `leashExceeded`: they asked what units the pursuit leash
		// and its anchor pair are in, which [R-STANCE-01 §4] answers — whole
		// world units on both sides, distance truncated toward zero before an
		// inclusive compare — so that helper folded into combat.go's
		// `leashBroken` and its questions were answered, not moved.
		"internal/orders/patrol.go":  1,
		"internal/orders/resolve.go": 34,
		// selfdestruct.go 0 -> 1 and standing.go 0 -> 2: WU-18-1 implements the
		// trivial, standing, wait, cloak and standby handlers of
		// [04 R-ORD-01 §2] plus the self-destruct pair, and records the three
		// gaps those rows leave open. (a) [04 R-ORD-01 §1] gives *release slot*
		// and *inhibit slot* one line each and puts only the `TargetCleared`
		// notification "under the guard of [R-ORDER-02 §2]", so whether the
		// target clear itself is guarded too is unstated. (b) `Standby_Mine`'s
		// row adds a status-word bit-29 test to a body whose inherited first
		// clause cancels on a missing mover reference, and the two cannot both
		// hold — a bit-29 unit is building-class and a building-class unit owns
		// no mover [04 R-FAC-02 §5]; every stock mine in the reference install
		// is `bmcode 0` (I14), so the site states which reading it takes and
		// what would settle it. (c) [04 R-ORD-01 §2] locates the self-destruct
		// initialisation marker in p2's high nibble without giving its value.
		// All three are written at their site with the question and its
		// decider, per I9.
		"internal/orders/selfdestruct.go": 1,
		"internal/orders/standing.go":     2,
		"internal/orders/stop.go":         1,
		"internal/orders/table.go":        1,
		"internal/orders/transport.go":    24,
		// WU-18-2, the work handlers: three questions research does not settle,
		// each written at its site with its decider (I9). (1) `SelfRepair`'s
		// phase-0 admission — [04 R-ORD-01 §2] puts "complete and activated" on
		// the order's target while [05 R-WORK-01 §3] splits the pair between
		// repairer and patient. (2) The "target state-word bits 2-3" arm of
		// `RepairUnit` and `Capture` — units.Unit mirrors only the low two
		// status bits, and no section names bits 2 and 3. (3) The assist
		// approach radius — [04 R-ORD-01 §5] takes its footprint from the
		// builder, [05 R-WORK-01 §2] from the target, over identical
		// arithmetic. None is a value this unit picked.
		"internal/orders/work.go": 3,
		// WU-18-3, the VTOL work twins: two questions [04 R-ORD-01 §7] and
		// [05 R-WORK-01 §8] leave open, each written at its site with its
		// decider (I9). (1) `VTOL_HelpBuild`'s phase 0 adds "the definition's
		// builder-specific script slot must be present" and no section names
		// that slot; this build caches no script-function indices on a
		// definition, so the clause is not evaluated rather than guessed at.
		// (2) [05 R-WORK-01 §8] records as Unknown which of the two model-box
		// forms the four VTOL work executors build for their spray, where both
		// ground forms are Established. Neither is a value this unit picked.
		"internal/orders/vtolair.go":  1,
		"internal/orders/vtolwork.go": 2,
		"internal/path/goals.go":      4,
		"internal/path/queue.go":      1,
		"internal/path/search.go":     1,
		"internal/pool/pool.go":       1,
		"internal/save/bank.go":       3,
		// +1: the first-selection build-page default of [07 R-HUD-03 §6] is a
		// direct retail observation with no traced writer for status bit 22.
		"internal/session/commands.go":        2,
		"internal/session/composition.go":     2,
		"internal/session/mission.go":         3,
		"internal/session/progression.go":     4,
		"internal/session/post_loop.go":       1,
		"internal/session/result.go":          1,
		"internal/session/stats.go":           1,
		"internal/save/boxes.go":              1,
		"internal/session/retail_load.go":     1,
		"internal/session/session.go":         3,
		"internal/session/skirmish.go":        5,
		"internal/session/commander_death.go": 2,
		"internal/session/state.go":           1,
		"internal/session/step.go":            2,
		"internal/session/strips.go":          15,
		"internal/session/trigger_adapter.go": 1,
		"internal/sim/numeric/numeric.go":     1,
		"internal/sim/numeric/trig.go":        1,
		"internal/sim/rng/rng.go":             1,
		"internal/combat/weapon_adapter.go":   1,
		"internal/units/types.go":             2,
		"internal/units/units.go":             8, // +1: the activation edge has no status-cue sink [04 R-UNIT-06 §2] (PLAN_16 WU-16-3)
		"internal/visibility/fog.go":          1,
		"internal/visibility/publish.go":      1, // LOS group-0 record content is Unknown [03 R-COMP-02 §1]
		"internal/visibility/sensors.go":      1, // five stock sensor definitions have no traced activation writer [03 §3.4 R-VIS-01 §4]. -1 (PLAN_17 WU-17-11): the untraced global-options-word bit 9 of the blip gate [03 §3.9] is NOT retired — it moved with its gate. The sensor phase no longer emits minimap circles ([03 §3.10] correction of 2026-08-29 makes the contacts pass the sole producer), so the blip gate left this file with the circle walk; the marker now sits on internal/render/minimap.go's MinimapContact.Options, which this guard does not scan.
		"internal/world/feature_stamp.go":     1,
		"internal/world/placement.go":         3,
		"internal/world/plot.go":              3,
		"internal/world/terrain.go":           2,
		"internal/world/wind.go":              2,
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
