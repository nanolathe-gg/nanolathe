// Guard: exported objects that nothing in the module ever names.
//
// deadcode answers "can a binary reach this function". It says nothing about a
// constant, a variable, a type or a struct field, and nothing at all about an
// object no code mentions — an exported name with zero uses is invisible to
// every reachability tool, so a whole exported surface can accrete without a
// gate going red. This guard closes that hole with a shrink-only allowlist:
// unreferenced_exported_allow.txt records the objects that were unreferenced
// when the guard landed, a new one fails, and an entry that acquires a
// reference must be dropped from the list in the same commit.
//
// The scan is deliberately NAME-based rather than type-based. It counts
// *ast.Ident occurrences across the whole module — every build tag, tests
// included, generated files included as reference sources — and calls a
// declaration referenced when its name occurs anywhere but its own declaring
// identifier. A same-named identifier in an unrelated package therefore counts
// as a use. That is the conservative direction for a gate: it under-reports
// rather than accusing live code, and it needs no type checker, so the whole
// tree parses in a second or two [I11].
//
// Methods are out of scope: a method reached through fmt, encoding or
// interface satisfaction is never named at the call site, so "nobody names it"
// would not mean "nobody calls it".

package architecture

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// unreferencedAllowFile is the committed allowlist, relative to this package.
const unreferencedAllowFile = "unreferenced_exported_allow.txt"

// unreferencedUpdateEnv regenerates the allowlist instead of enforcing it.
const unreferencedUpdateEnv = "NANOLATHE_UPDATE_ALLOWLIST"

// generatedHeader is the go generate convention: a line of this shape before
// the package clause marks the file as machine-written. Such a file is not a
// declaration source — nobody edits it, so listing its names would be noise —
// but its identifier uses still count as references.
var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// exportedDecl is one candidate object: a package-level const, var, type or
// func, or an exported field of an exported package-level struct type.
type exportedDecl struct {
	entry    string // "<dir>.<Name>", or "<dir>.<Type>.<Field>" for a field
	name     string // the identifier the reference count is keyed by
	owner    string // for a field, its struct type name; empty otherwise
	tagged   bool   // for a field, it carries a struct tag
	position string // "<file>:<line>", for the failure message only
}

// exportedSurface is one pass over the module's source.
type exportedSurface struct {
	decls []exportedDecl
	// identUses counts every identifier occurrence in the module, including
	// the declaring occurrence itself, so a count of one means "declared and
	// never mentioned again".
	identUses map[string]int
	// unkeyedLiteral holds type names built by at least one positional
	// composite literal. Such a literal writes every field without naming
	// one, so none of that type's fields can be called unused.
	unkeyedLiteral map[string]bool
	// embedded holds type names embedded in another struct, whose fields are
	// therefore reachable by promotion through the outer type.
	embedded map[string]bool
}

// TestNoUnreferencedExported fails when an exported package-level object, or
// an exported field of an exported package-level struct, is named nowhere in
// the module outside its own declaration and is not on the allowlist.
func TestNoUnreferencedExported(t *testing.T) {
	root := repositoryRoot(t)
	surface := scanExportedSurface(t, root)

	current := make(map[string]exportedDecl)
	for _, decl := range surface.decls {
		if surface.identUses[decl.name] > 1 {
			continue
		}
		if decl.owner != "" {
			if decl.tagged || surface.unkeyedLiteral[decl.owner] || surface.embedded[decl.owner] {
				continue
			}
		}
		current[decl.entry] = decl
	}

	allowPath := filepath.Join(root, "internal", "architecture", unreferencedAllowFile)
	if os.Getenv(unreferencedUpdateEnv) == "1" {
		writeUnreferencedAllowlist(t, allowPath, current)
		return
	}

	allowed := readUnreferencedAllowlist(t, allowPath)
	regenerate := unreferencedUpdateEnv + "=1 go test ./internal/architecture -run TestNoUnreferencedExported"

	var added []string
	for entry, decl := range current {
		if _, ok := allowed[entry]; !ok {
			added = append(added, fmt.Sprintf("%s (%s)", entry, decl.position))
		}
	}
	if len(added) != 0 {
		sort.Strings(added)
		t.Errorf("exported object referenced nowhere in the module: delete it, reference it, or add it to internal/architecture/%s with a trailing `# reason`:\n  + %s\nregenerate with: %s",
			unreferencedAllowFile, strings.Join(added, "\n  + "), regenerate)
	}

	var stale []string
	for entry := range allowed {
		if _, ok := current[entry]; !ok {
			stale = append(stale, entry)
		}
	}
	if len(stale) != 0 {
		sort.Strings(stale)
		t.Errorf("stale allowlist entry: these are referenced or gone, so drop them from internal/architecture/%s in this commit (the list only shrinks):\n  - %s\nregenerate with: %s",
			unreferencedAllowFile, strings.Join(stale, "\n  - "), regenerate)
	}
}

// scanExportedSurface parses every Go file of this module once, collecting the
// candidate declarations and the module-wide identifier census together.
func scanExportedSurface(t *testing.T, root string) exportedSurface {
	t.Helper()
	surface := exportedSurface{
		identUses:      make(map[string]int, 1<<15),
		unkeyedLiteral: make(map[string]bool),
		embedded:       make(map[string]bool),
	}
	err := guardWalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		// Comments are dropped: the generated-file marker is found in the raw
		// bytes below, and no other part of this guard reads a comment.
		file, err := parser.ParseFile(fset, path, source, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(relative))

		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.Ident:
				surface.identUses[typed.Name]++
			case *ast.CompositeLit:
				if typed.Type != nil {
					markPositionalLiteral(typed, typed.Type, surface.unkeyedLiteral)
				}
			}
			return true
		})

		// Test files are reference sources but not declaration sources. Their
		// exported surface is the framework's: Test/Benchmark/Fuzz/Example
		// functions are called by name from no Go code at all, and a helper a
		// test file exports is already confined to that test binary.
		if strings.HasSuffix(entry.Name(), "_test.go") || generatedSource(source) {
			return nil
		}
		collectExportedDecls(&surface, fset, file, filepath.ToSlash(relative), dir)
		return nil
	})
	if err != nil {
		t.Fatalf("scan module source: %v", err)
	}
	return surface
}

// generatedSource reports whether the file carries the go generate marker
// before its package clause.
func generatedSource(source []byte) bool {
	head := source
	if index := bytes.Index(source, []byte("\npackage ")); index >= 0 {
		head = source[:index]
	}
	return generatedHeader.Match(head)
}

// collectExportedDecls records this file's package-level exported objects and
// the struct-shape facts the field rules need.
func collectExportedDecls(surface *exportedSurface, fset *token.FileSet, file *ast.File, relative, dir string) {
	position := func(pos token.Pos) string {
		at := fset.Position(pos)
		return fmt.Sprintf("%s:%d", relative, at.Line)
	}
	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			// Methods are excluded; see the file comment.
			if typed.Recv != nil || !typed.Name.IsExported() {
				continue
			}
			surface.decls = append(surface.decls, exportedDecl{
				entry:    dir + "." + typed.Name.Name,
				name:     typed.Name.Name,
				position: position(typed.Name.Pos()),
			})
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				switch specified := spec.(type) {
				case *ast.ValueSpec:
					for _, name := range specified.Names {
						if !name.IsExported() {
							continue
						}
						surface.decls = append(surface.decls, exportedDecl{
							entry:    dir + "." + name.Name,
							name:     name.Name,
							position: position(name.Pos()),
						})
					}
				case *ast.TypeSpec:
					structure, isStruct := specified.Type.(*ast.StructType)
					if isStruct {
						noteStructShape(surface, structure)
					}
					if !specified.Name.IsExported() {
						continue
					}
					surface.decls = append(surface.decls, exportedDecl{
						entry:    dir + "." + specified.Name.Name,
						name:     specified.Name.Name,
						position: position(specified.Name.Pos()),
					})
					if isStruct {
						collectExportedFields(surface, structure, specified.Name.Name, dir, position)
					}
				}
			}
		}
	}
}

// noteStructShape records the types this struct embeds. A promoted field is
// read through the outer type, so an embedded type's fields are never dead.
func noteStructShape(surface *exportedSurface, structure *ast.StructType) {
	for _, field := range structure.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		if name := literalTypeName(field.Type); name != "" {
			surface.embedded[name] = true
		}
	}
}

func collectExportedFields(surface *exportedSurface, structure *ast.StructType, owner, dir string, position func(token.Pos) string) {
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			surface.decls = append(surface.decls, exportedDecl{
				entry:    dir + "." + owner + "." + name.Name,
				name:     name.Name,
				owner:    owner,
				tagged:   field.Tag != nil,
				position: position(name.Pos()),
			})
		}
	}
}

// markPositionalLiteral records the named type of any composite literal that
// sets a field by position. typeExpr is the literal's type, which for an
// element of a slice or map literal is supplied by the caller because Go
// allows it to be elided.
func markPositionalLiteral(literal *ast.CompositeLit, typeExpr ast.Expr, unkeyed map[string]bool) {
	if name := literalTypeName(typeExpr); name != "" {
		for _, element := range literal.Elts {
			if _, keyed := element.(*ast.KeyValueExpr); !keyed {
				unkeyed[name] = true
				return
			}
		}
		return
	}
	element := literalElementType(typeExpr)
	if element == nil {
		return
	}
	for _, entry := range literal.Elts {
		if pair, isPair := entry.(*ast.KeyValueExpr); isPair {
			entry = pair.Value
		}
		if inner, isLiteral := entry.(*ast.CompositeLit); isLiteral && inner.Type == nil {
			markPositionalLiteral(inner, element, unkeyed)
		}
	}
}

// literalTypeName returns the bare name of a named type expression, or "" for
// a container or an unnamed type.
func literalTypeName(typeExpr ast.Expr) string {
	switch typed := typeExpr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.StarExpr:
		return literalTypeName(typed.X)
	case *ast.IndexExpr:
		return literalTypeName(typed.X)
	case *ast.IndexListExpr:
		return literalTypeName(typed.X)
	case *ast.ParenExpr:
		return literalTypeName(typed.X)
	}
	return ""
}

// literalElementType returns the element type of an array, slice or map type
// expression, which is the type a nested literal may elide.
func literalElementType(typeExpr ast.Expr) ast.Expr {
	switch typed := typeExpr.(type) {
	case *ast.ArrayType:
		return typed.Elt
	case *ast.MapType:
		return typed.Value
	case *ast.ParenExpr:
		return literalElementType(typed.X)
	}
	return nil
}

// readUnreferencedAllowlist returns the allowed entries mapped to the trailing
// reason each carries, if any.
func readUnreferencedAllowlist(t *testing.T, path string) map[string]string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read allowlist: %v", err)
	}
	allowed := make(map[string]string)
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		reason := ""
		if index := strings.Index(trimmed, "#"); index >= 0 {
			reason = strings.TrimSpace(trimmed[index+1:])
			trimmed = strings.TrimSpace(trimmed[:index])
		}
		allowed[trimmed] = reason
	}
	return allowed
}

// writeUnreferencedAllowlist rewrites the allowlist, carrying every surviving
// entry's recorded reason across so regeneration never silently discards one.
func writeUnreferencedAllowlist(t *testing.T, path string, current map[string]exportedDecl) {
	t.Helper()
	previous := readUnreferencedAllowlist(t, path)
	entries := make([]string, 0, len(current))
	for entry := range current {
		entries = append(entries, entry)
	}
	sort.Strings(entries)

	var out strings.Builder
	out.WriteString("# Exported objects whose name occurs nowhere in the module outside their\n")
	out.WriteString("# own declaration, as found by TestNoUnreferencedExported.\n")
	out.WriteString("#\n")
	out.WriteString("# Shrink-only: a new unreferenced object fails the guard, and an entry that\n")
	out.WriteString("# gains a reference or is deleted must be removed here in the same commit.\n")
	out.WriteString("# An entry kept on purpose carries its reason after a `#` — an iota anchor\n")
	out.WriteString("# whose removal would renumber its siblings, or a constant that is the\n")
	out.WriteString("# code's only record of a retail table.\n")
	out.WriteString("#\n")
	out.WriteString("# Regenerate with:\n")
	out.WriteString("#   " + unreferencedUpdateEnv + "=1 go test ./internal/architecture -run TestNoUnreferencedExported\n")
	for _, entry := range entries {
		out.WriteString(entry)
		if reason := previous[entry]; reason != "" {
			out.WriteString("  # " + reason)
		}
		out.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}
	t.Logf("wrote %s (%d entries)", path, len(entries))
}
