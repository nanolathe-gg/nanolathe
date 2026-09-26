// Repository-level guards for the retail runtime.
//
// These tests deliberately inspect production source instead of importing the
// packages under test.  That keeps the guard independent of runtime wiring and
// lets it catch an architectural dependency before a new feature exercises it.

package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// authoritativeDirs is the simulation side of the architecture boundary.
// Client, HUD, presentation, and audio are host/device edges and are
// intentionally outside this list.  The list follows the ownership boundaries
// in [01 §1] and the package graph in the repository architecture summary.
//
// internal/headless is deliberately NOT here even though it drives the session.
// It is a host/report edge: its report builder ranges a map to name packages,
// and the simulation benchmark's scene and timing records hold float64s that
// never reach simulation state.  Admitting it would mean allowlisting all of
// that, which buys a weaker guard than leaving the edge outside the boundary.
//
// internal/aikit and mods/aikit ARE here: a Modern computer player's
// controller and the rule set that binds it issue orders from phase 5, so
// every audit that guards the simulation — forbidden imports, float64, map
// order, fused arithmetic, goroutines — reads them too. The Modern AI
// exception of INVARIANTS I4 is a private generator the controller owns, and
// the asynchronous host's worker is the one justified goroutine carve-out
// (authoritativeGoroutines below); neither relaxes any audit for another
// package.
var authoritativeDirs = []string{
	"internal/ai",
	"internal/aikit",
	"internal/clock",
	"internal/cob",
	"internal/combat",
	"internal/construction",
	"internal/economy",
	"internal/features",
	"internal/frame",
	"internal/mission",
	"internal/model",
	"internal/movement",
	"internal/orders",
	"internal/path",
	"internal/pool",
	"internal/save",
	"internal/session",
	"internal/sim",
	"internal/survival",
	"internal/triggers",
	"internal/units",
	"internal/version",
	"internal/visibility",
	"internal/world",
	"mods/aikit",
}

// TestAuthoritativePackagesDoNotImportHostOrNondeterministicRuntime checks
// that simulation packages cannot acquire device/window APIs, wall-clock
// services, or an unrelated random stream.  It also keeps the executable,
// client, and test/clean-room scaffolding outside the authoritative graph.
// Ebitengine remains permitted at the client/audio edge only; simulation
// randomness is the two streams specified by [01 §7].
func TestAuthoritativePackagesDoNotImportHostOrNondeterministicRuntime(t *testing.T) {
	root := repositoryRoot(t)
	violations := scanAuthoritativeFiles(t, root, func(path string, file *ast.File, fset *token.FileSet) []string {
		var found []string
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if forbiddenRuntimeImport(importPath) {
				pos := fset.Position(spec.Pos())
				found = append(found, formatViolation(root, path, pos.Line, importPath))
			}
		}
		return found
	})
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("authoritative package imports host or nondeterministic runtime: %s", strings.Join(violations, "; "))
	}
}

// authoritativeGoroutines is the goroutine allowlist for the authoritative
// packages: the files that may contain a `go` statement, each with the reason
// its goroutine cannot make the simulation depend on scheduling (I1). It is
// shrink-only; an entry whose file no longer starts a goroutine is stale.
var authoritativeGoroutines = map[string]string{
	"internal/aikit/host.go": "the asynchronous host's worker runs one brain think on a copy the simulation thread built; the thread hands it the observation, joins it at the fixed reaction deadline and applies its commands there, so the tick the commands land on and their content are the synchronous host's (DESIGN_GAMEPLAY_RULES §5)",
}

// TestAuthoritativePackagesStartNoGoroutines keeps goroutine scheduling out of
// the simulation (I1): no authoritative file starts one unless it is named
// above with the argument that makes its result independent of scheduling.
func TestAuthoritativePackagesStartNoGoroutines(t *testing.T) {
	root := repositoryRoot(t)
	seen := map[string]bool{}
	violations := scanAuthoritativeFiles(t, root, func(path string, file *ast.File, fset *token.FileSet) []string {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			relative = path
		}
		relative = filepath.ToSlash(relative)
		var found []string
		ast.Inspect(file, func(node ast.Node) bool {
			statement, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}
			if _, allowed := authoritativeGoroutines[relative]; allowed {
				seen[relative] = true
				return true
			}
			found = append(found, formatViolation(root, path, fset.Position(statement.Pos()).Line, "go statement"))
			return true
		})
		return found
	})
	for path := range authoritativeGoroutines {
		if !seen[path] {
			violations = append(violations, path+" (stale goroutine allowance)")
		}
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("authoritative package starts a goroutine outside the allowlist (I1): %s", strings.Join(violations, "; "))
	}
}

func forbiddenRuntimeImport(path string) bool {
	switch path {
	case "crypto/rand", "math/rand", "time":
		return true
	}
	for _, prefix := range []string{
		"github.com/hajimehoshi/ebiten/v2",
		"github.com/nanolathe-gg/nanolathe/cmd/nanolathe",
		"github.com/nanolathe-gg/nanolathe/internal/client",
		"github.com/nanolathe-gg/nanolathe/internal/cleanroom",
		"github.com/nanolathe-gg/nanolathe/internal/testsupport",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

type fileVisitor func(path string, file *ast.File, fset *token.FileSet) []string

func scanAuthoritativeFiles(t *testing.T, root string, visit fileVisitor) []string {
	t.Helper()
	var violations []string
	for _, relativeDir := range authoritativeDirs {
		dir := filepath.Join(root, filepath.FromSlash(relativeDir))
		err := guardWalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			violations = append(violations, visit(path, file, fset)...)
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", relativeDir, err)
		}
	}
	return violations
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate repository go.mod")
		}
		directory = parent
	}
}

func formatViolation(root, path string, line int, subject string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		relative = path
	}
	return relative + ":" + strconv.Itoa(line) + " (" + subject + ")"
}
