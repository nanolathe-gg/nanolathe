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

// DET-01 architecture guards (parity-spine W1). Three ratchets pin who may
// touch the two retail random streams [01 §7.1][01 §7.2][INVARIANTS I4]:
//
//   (a) presentation packages must not import internal/sim/rng — presentation
//       consumes published values or private presentation copies, never the
//       retail stream types. Anything not convertible this round sits in the
//       explicit, commented, shrink-only allowlist below.
//   (b) rng.Global symbol references are allowed only in internal/sim/rng
//       itself, internal/session bootstrap, cmd/, and _test.go files.
//   (c) only internal/sim/rng and internal/session may CONSTRUCT
//       rng.Simulation/rng.CRT in non-test production code.

// presentationDirs are the presentation-layer packages guarded by (a).
var presentationDirs = []string{
	"internal/client",
	"internal/render",
	"internal/audio",
	"internal/hud",
	"internal/gui",
	"internal/ui",
	"internal/camera",
}

// presentationRNGAllowlist is the (a) exception list: files that still import
// internal/sim/rng. Entries must carry a reason and the list only shrinks.
var presentationRNGAllowlist = map[string]string{
	// The queue keeps the session CRT handoff signature SetCRTRandom(*rng.CRT)
	// but copies the stream STATE; its draws advance only private copies.
	"internal/audio/queue.go": "signature and private state copy; draws never reach the session stream",
	// Same SetCRTRandom handoff signature as the queue; music draws a private
	// presentationCRT (approved divergence, see DET-01 note there).
	"internal/audio/music.go": "SetCRTRandom signature only; draws use the private presentationCRT",
	// Passes the session CRT handoff to queue and music; retains no stream.
	"internal/audio/service.go": "SetCRTRandom pass-through signature only",
	// SetPresentationCRT copies the stream state into the client's private
	// presentation copy and never retains the session stream (DET-01/DET-04).
	"internal/client/audio.go": "SetPresentationCRT copies state into a private presentation copy",
	// Holds the private presentation copy feeding the segmented-projectile
	// presentation pass; approved divergence, see the field comment (DET-01).
	"internal/client/client.go": "private presentation CRT copy for segmented-projectile presentation",
}

func TestPresentationPackagesDoNotImportRNG(t *testing.T) {
	root := repositoryRoot(t)
	var violations []string
	for _, dir := range presentationDirs {
		abs := filepath.Join(root, filepath.FromSlash(dir))
		if _, err := os.Stat(abs); err != nil {
			continue // package not present in this worktree
		}
		err := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			importsRNG := false
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if p == "github.com/nanolathe/nanolathe/internal/sim/rng" {
					importsRNG = true
				}
			}
			if !importsRNG {
				return nil
			}
			reason, ok := presentationRNGAllowlist[relSlash]
			if !ok {
				pos := fset.Position(f.Package)
				violations = append(violations, formatViolation(root, path, pos.Line, "sim/rng import (no allowlist entry)"))
			} else {
				_ = reason
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("presentation package imports internal/sim/rng (DET-01 (a)): %s", strings.Join(violations, "; "))
	}
}

// TestGlobalReferencesAllowedOnlyInSessionAndRNG pins (b): the rng.Global
// symbol may be referenced only in internal/sim/rng, internal/session
// bootstrap, cmd/, and test files.
func TestGlobalReferencesAllowedOnlyInSessionAndRNG(t *testing.T) {
	root := repositoryRoot(t)
	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasSuffix(path, ".git") || strings.Contains(path, ".claude") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		if strings.HasPrefix(relSlash, "internal/sim/rng/") ||
			strings.HasPrefix(relSlash, "internal/session/") || // bootstrap handoff only
			strings.HasPrefix(relSlash, "cmd/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Global" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "rng" {
				return true
			}
			found = true
			return false
		})
		if found {
			violations = append(violations, relSlash)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("rng.Global referenced outside internal/sim/rng, internal/session bootstrap, cmd/, tests (DET-01 (b)): %s", strings.Join(violations, "; "))
	}
}

// constructionAllowlist is the (c) exception list: non-test production files
// outside internal/sim/rng and internal/session that may construct
// rng.Simulation/rng.CRT. Entries must carry a reason; the list only shrinks.
var constructionAllowlist = map[string]string{
	// The queue's private fallback/seed streams are presentation-only; its
	// SetCRTRandom copies state so session draws never advance here.
	"internal/audio/queue.go": "private presentation-only fallback streams",
}

// TestOnlySessionAndRNGMayConstruct pins (c): retail stream construction in
// non-test production code is confined to internal/sim/rng and the session
// bootstrap, plus the explicit allowlist above.
func TestOnlySessionAndRNGMayConstruct(t *testing.T) {
	root := repositoryRoot(t)
	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasSuffix(path, ".git") || strings.Contains(path, ".claude") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		if strings.HasPrefix(relSlash, "internal/sim/rng/") || strings.HasPrefix(relSlash, "internal/session/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "NewSimulation" && sel.Sel.Name != "NewCRT" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "rng" {
				return true
			}
			if _, allow := constructionAllowlist[relSlash]; allow {
				return true
			}
			pos := fset.Position(call.Pos())
			violations = append(violations, formatViolation(root, path, pos.Line, "rng."+sel.Sel.Name+" construction"))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("rng.Simulation/rng.CRT constructed outside internal/sim/rng, internal/session, allowlist (DET-01 (c)): %s", strings.Join(violations, "; "))
	}
}
