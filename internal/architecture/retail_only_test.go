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
var authoritativeDirs = []string{
	"internal/ai",
	"internal/clock",
	"internal/cob",
	"internal/combat",
	"internal/construction",
	"internal/economy",
	"internal/features",
	"internal/mission",
	"internal/movement",
	"internal/orders",
	"internal/path",
	"internal/pool",
	"internal/save",
	"internal/session",
	"internal/sim",
	"internal/units",
	"internal/visibility",
	"internal/world",
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

func forbiddenRuntimeImport(path string) bool {
	switch path {
	case "crypto/rand", "math/rand", "time":
		return true
	}
	for _, prefix := range []string{
		"github.com/hajimehoshi/ebiten/v2",
		"github.com/nanolathe/nanolathe/cmd/nanolathe",
		"github.com/nanolathe/nanolathe/internal/client",
		"github.com/nanolathe/nanolathe/internal/cleanroom",
		"github.com/nanolathe/nanolathe/internal/testsupport",
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
