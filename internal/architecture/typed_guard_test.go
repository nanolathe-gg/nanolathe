package architecture

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// typedPackage is one authoritative package checked with the same exported
// dependency information the Go tool uses. It lets the I1 guard identify a
// map through a local, selector, named type, or call expression.
type typedPackage struct {
	importPath string
	fset       *token.FileSet
	files      []*ast.File
	info       *types.Info
}

type listedPackage struct {
	ImportPath string
	Dir        string
	Export     string
	GoFiles    []string
}

func loadAuthoritativeTypedPackages(t *testing.T, root string) []typedPackage {
	t.Helper()
	args := []string{"list", "-deps", "-export", "-json"}
	for _, dir := range authoritativeDirs {
		args = append(args, "./"+dir+"/...")
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("list authoritative package exports: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	var listed []listedPackage
	exports := map[string]string{}
	for {
		var item listedPackage
		err := decoder.Decode(&item)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode authoritative package exports: %v", err)
		}
		listed = append(listed, item)
		exports[item.ImportPath] = item.Export
	}

	fset := token.NewFileSet()
	lookup := func(path string) (io.ReadCloser, error) {
		export, ok := exports[path]
		if !ok || export == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(export)
	}
	imp := importer.ForCompiler(fset, "gc", lookup)
	var packages []typedPackage
	for _, item := range listed {
		if !isAuthoritativeImportPath(item.ImportPath) {
			continue
		}
		files := make([]*ast.File, 0, len(item.GoFiles))
		for _, name := range item.GoFiles {
			file, err := parser.ParseFile(fset, filepath.Join(item.Dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parse %s/%s: %v", item.ImportPath, name, err)
			}
			files = append(files, file)
		}
		info := &types.Info{
			Defs:  map[*ast.Ident]types.Object{},
			Types: map[ast.Expr]types.TypeAndValue{},
		}
		if _, err := (&types.Config{Importer: imp}).Check(item.ImportPath, fset, files, info); err != nil {
			t.Fatalf("type-check %s: %v", item.ImportPath, err)
		}
		packages = append(packages, typedPackage{importPath: item.ImportPath, fset: fset, files: files, info: info})
	}
	return packages
}

func isAuthoritativeImportPath(path string) bool {
	const prefix = "github.com/nanolathe-gg/nanolathe/"
	for _, dir := range authoritativeDirs {
		base := prefix + dir
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

type typedMapRange struct {
	path         string
	function     string
	rangeOrdinal int
	rangeHash    string
	functionHash string
	body         string
	line         int
}

func typedMapRanges(root string, packages []typedPackage) []typedMapRange {
	var ranges []typedMapRange
	for _, pkg := range packages {
		for _, file := range pkg.files {
			path := pkg.fset.Position(file.Pos()).Filename
			relative, err := filepath.Rel(root, path)
			if err != nil {
				relative = path
			}
			appendRanges := func(name string, functionNode ast.Node, body *ast.BlockStmt) {
				rangeOrdinal := 0
				functionBody := normalizedNode(pkg.fset, functionNode)
				functionDigest := sha256.Sum256([]byte(functionBody))
				ast.Inspect(body, func(node ast.Node) bool {
					rangeStmt, ok := node.(*ast.RangeStmt)
					if !ok || !isMapType(pkg.info.TypeOf(rangeStmt.X)) {
						return true
					}
					rangeOrdinal++
					body := normalizedNode(pkg.fset, rangeStmt)
					rangeDigest := sha256.Sum256([]byte(body))
					ranges = append(ranges, typedMapRange{
						path:         filepath.ToSlash(relative),
						function:     name,
						rangeOrdinal: rangeOrdinal,
						rangeHash:    fmt.Sprintf("%x", rangeDigest[:]),
						functionHash: fmt.Sprintf("%x", functionDigest[:]),
						body:         body,
						line:         pkg.fset.Position(rangeStmt.Pos()).Line,
					})
					return true
				})
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				appendRanges(functionName(pkg.fset, fn), fn, fn.Body)
			}
			for _, decl := range file.Decls {
				if _, isFunction := decl.(*ast.FuncDecl); isFunction {
					continue
				}
				ast.Inspect(decl, func(node ast.Node) bool {
					literal, ok := node.(*ast.FuncLit)
					if !ok {
						return true
					}
					name := "package-init closure@" + strconv.Itoa(pkg.fset.Position(literal.Pos()).Line)
					appendRanges(name, literal, literal.Body)
					return false
				})
			}
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].key() < ranges[j].key() })
	return ranges
}

func isMapType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	_, ok := typ.Underlying().(*types.Map)
	return ok
}

func functionName(fset *token.FileSet, fn *ast.FuncDecl) string {
	if fn.Recv == nil {
		return fn.Name.Name
	}
	return normalizedNode(fset, fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func normalizedNode(fset *token.FileSet, node ast.Node) string {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, fset, node); err != nil {
		panic(err)
	}
	return strings.TrimSpace(buffer.String())
}

func (site typedMapRange) key() string {
	return site.path + " " + site.function + " " + site.rangeHash
}

func (site typedMapRange) functionKey() string {
	return site.path + " " + site.function
}

type floatScope struct {
	path  string
	name  string
	count int
	sites []occurrence
}

func float64Scopes(t *testing.T) []floatScope {
	t.Helper()
	var scopes []floatScope
	scanAuthoritativeSources(t, func(path string, fset *token.FileSet, file *ast.File, lineText func(int) string) {
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
				for _, spec := range gen.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, isStruct := typeSpec.Type.(*ast.StructType); isStruct {
						// Struct fields use resolved object identities below: a token
						// count cannot distinguish `a, b float64` from one field.
						continue
					}
					if scope, ok := float64ScopeForNode(path, fset, lineText, "type "+typeSpec.Name.Name, typeSpec); ok {
						scopes = append(scopes, scope)
					}
				}
				continue
			}
			name := floatScopeName(fset, decl)
			if name == "" {
				continue
			}
			if scope, ok := float64ScopeForNode(path, fset, lineText, name, decl); ok {
				scopes = append(scopes, scope)
			}
		}
	})
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].key() < scopes[j].key() })
	return scopes
}

func float64ScopeForNode(path string, fset *token.FileSet, lineText func(int) string, name string, node ast.Node) (floatScope, bool) {
	perLine := map[int]string{}
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Ident:
			if node.Name == "float64" {
				count++
				line := fset.Position(node.Pos()).Line
				perLine[line] = lineText(line)
			}
		case *ast.BasicLit:
			if node.Kind == token.FLOAT {
				count++
				line := fset.Position(node.Pos()).Line
				perLine[line] = lineText(line)
			}
		}
		return true
	})
	if count == 0 {
		return floatScope{}, false
	}
	return floatScope{path: path, name: name, count: count, sites: siteList(perLine)}, true
}

func floatScopeName(fset *token.FileSet, decl ast.Decl) string {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		return "func " + functionName(fset, decl)
	case *ast.GenDecl:
		var names []string
		for _, spec := range decl.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, spec.Name.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
			}
		}
		if len(names) != 0 {
			return decl.Tok.String() + " " + strings.Join(names, ",")
		}
	}
	return ""
}

func (scope floatScope) key() string {
	return scope.path + " " + scope.name
}

type typedFloat64Field struct {
	path string
	key  string
	line int
}

func typedFloat64Fields(root string, packages []typedPackage) []typedFloat64Field {
	var fields []typedFloat64Field
	for _, pkg := range packages {
		for _, file := range pkg.files {
			filename := pkg.fset.Position(file.Pos()).Filename
			relative, err := filepath.Rel(root, filename)
			if err != nil {
				relative = filename
			}
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					structType, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					path := filepath.ToSlash(relative)
					collectFloat64Fields(pkg, path, typeSpec.Name.Name, structType, &fields)
				}
			}
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })
	return fields
}

func collectFloat64Fields(pkg typedPackage, path, owner string, structType *ast.StructType, fields *[]typedFloat64Field) {
	for _, field := range structType.Fields.List {
		if nested, ok := field.Type.(*ast.StructType); ok {
			for _, name := range field.Names {
				collectFloat64Fields(pkg, path, owner+"."+name.Name, nested, fields)
			}
			continue
		}
		for _, name := range fieldNames(pkg.info, pkg.fset, field) {
			if !containsFloat64(name.typ) {
				continue
			}
			*fields = append(*fields, typedFloat64Field{
				path: path,
				key:  path + " type " + owner + "." + name.name + " " + normalizedNode(pkg.fset, field.Type),
				line: name.line,
			})
		}
	}
}

type namedField struct {
	name string
	typ  types.Type
	line int
}

func fieldNames(info *types.Info, fset *token.FileSet, field *ast.Field) []namedField {
	if len(field.Names) == 0 {
		return []namedField{{
			name: normalizedNode(fset, field.Type),
			typ:  info.TypeOf(field.Type),
			line: fset.Position(field.Pos()).Line,
		}}
	}
	fields := make([]namedField, 0, len(field.Names))
	for _, ident := range field.Names {
		object, _ := info.Defs[ident].(*types.Var)
		typ := info.TypeOf(field.Type)
		if object != nil {
			typ = object.Type()
		}
		fields = append(fields, namedField{
			name: ident.Name,
			typ:  typ,
			line: fset.Position(ident.Pos()).Line,
		})
	}
	return fields
}

// containsFloat64 inspects numeric storage within a field without promoting
// another named struct's fields into this owner's allowance. Named structs
// have their own declaration audit; signatures do not store numeric state.
func containsFloat64(typ types.Type) bool {
	seen := make(map[types.Type]bool)
	var visit func(types.Type) bool
	visit = func(typ types.Type) bool {
		if typ == nil || seen[typ] {
			return false
		}
		seen[typ] = true
		typ = types.Unalias(typ)
		if named, ok := typ.(*types.Named); ok {
			if _, ownsFields := named.Underlying().(*types.Struct); ownsFields {
				return false
			}
		}
		switch typ := typ.Underlying().(type) {
		case *types.Basic:
			return typ.Kind() == types.Float64
		case *types.Array:
			return visit(typ.Elem())
		case *types.Slice:
			return visit(typ.Elem())
		case *types.Pointer:
			return visit(typ.Elem())
		case *types.Map:
			return visit(typ.Key()) || visit(typ.Elem())
		case *types.Chan:
			return visit(typ.Elem())
		case *types.Struct:
			for i := 0; i < typ.NumFields(); i++ {
				if visit(typ.Field(i).Type()) {
					return true
				}
			}
		}
		return false
	}
	return visit(typ)
}

// mapRangeExceptions pins every currently justified map traversal by its
// package path, containing function, and normalized range body. The companion
// mapFunctionHashes binds the whole containing operation, including its sort
// or other ordering dependency, to the same I1 review.
var mapRangeExceptions = map[string]string{
	"internal/cob/debug_capture.go *VM.DebugSnapshot 7d182462d9e252dafb59da673acdcd673414aec86225c680a2c9710ae0bc1fa0":             "host-only read copies script names into detached storage and sorts them before returning; it neither invokes scripts nor changes VM state",
	"internal/features/debug_capture.go *Service.DebugSnapshot 0de7ab9b2ba8a95d0ba037338b670b09c17a342ce8e777aad0040ba750b1ddb8":   "host-only read gathers private keys and sorts them before copying feature values; it does not refresh the live key cache or change feature state",
	"internal/cob/binding.go BindStrict 86205b5f7e2c831877392e584559b68dedd881a5fa3fae0d7563637ac5276a20":                          "installs distinct handlers by port key; it does not invoke them",
	"internal/cob/binding.go BindStrict 4a375074326b6563496b3c5ce6f1f0f10af85196815ffe7d2b50a5eb84ba217c":                          "installs distinct bindings by port key; it does not invoke them",
	"internal/units/cob_binding.go bindUnitPortHandlers a4d09a5b9e3de58069318e1f534101f632a5819ec42cc2a8410811523b9fdb89":          "installs bindings only; no handler is run during this map walk",
	"internal/units/cob_binding.go bindCOBWithPortsAndVisibility 8cdbf0ee30106974865028c642794614f67004c7467ebdd5035d1da25e593c4f": "installs bindings only; no handler is run during this map walk",
	"internal/features/service.go *Service.sortedInstanceKeys 0de7ab9b2ba8a95d0ba037338b670b09c17a342ce8e777aad0040ba750b1ddb8":    "gathers keys and sorts them before every consumer sees the slice",
	"internal/save/battle_image.go validateCarrierReferenceGraph 452bad5001b060b81ff4f96a4cf3a6dc90e812f0a597899aedfa32b66dd65c1a": "each traversal changes only local DFS colours; the verdict and diagnostic are order-independent",
	"internal/ai/manager.go *Manager.EnsureStrategicInitialized df04ad0a0adf5788480cb3a77e4461881c9681b4a6ede1f7a59e35a29969fa7d":  "gathers catalog keys and sorts them before strategic initialization",
	"internal/ai/manager.go *Manager.EnsureStrategicInitialized 49821edd23bb8678aec944823c688db25591c31d8633a8e6b3070fee6b4f91cf":  "copies independent class-vector values before the maps are rebuilt",
	"internal/ai/manager.go *Manager.EnsureStrategicInitialized 143e739971a876a6dec004cfb0c1eec87a450131b1a8cb5ebf864e7134f914ea":  "restores independent class-vector values by key after deterministic initialization",
	"internal/ai/profile.go cloneWeightTable 3438197ca1a62ee230169a7fdbd1767c419c250ec3ab2636216d11af93b8a1b8":                     "gathers keys and sorts them before copying values",
	"internal/ai/profile.go *Profile.Difficulties 121fcfb90c9967581c05938dd3cedb5d32a90b6dfa7160edb251609c9e38faa9":                "builds a set; later traversal is sorted",
	"internal/ai/profile.go *Profile.Difficulties 89476ddf7b72a0802b9c9b8aceb51d93fa413ec90af9de1b2267d4f60358f64f":                "builds a set; later traversal is sorted",
	"internal/ai/profile.go *Profile.Difficulties acc21e1e3de22ada7c695b0abd96cd6add30d9b0c3abcdea42c236271e260b94":                "gathers difficulty values and sorts them before returning",
	"internal/ai/profile.go LoadProfile 26cc4132c8787df22a9b7f051f9f99e2fea6b5af920a835ac7b9d696de1dcbca":                          "compiled plan keys are canonical one of any/easy/medium/hard; each write is independent by key",
	"internal/ai/profile.go LoadProfile 01b396ee0fe5fb3b2d2beeb2c50472b8b4bd5d667d7c456c2dfe9dfa6e6d5a55":                          "copies independent weight entries from the compiled plan table",
	"internal/ai/profile.go LoadProfile 890604c92b3b039d202cc05f9110eb14b6a39653d7d5eaa62d97de16df76745b":                          "copies independent limit entries from the compiled plan table",
	"internal/ai/profile.go LoadProfile f37ac58ee86dc1f1482c347f6798c1a96f100112a61384f8ad973dc70d05c7fa":                          "gathers fallback candidates and sorts them before choosing",
	"internal/ai/profile.go LoadProfile 0280b14a954a8c2a978ced97a35ae44f5a46001ea4c2be131ed7f087410383e2":                          "gathers fallback candidates and sorts them before choosing",
	"internal/ai/strategic.go *Strategic.InitClassVectors 29779bffdb49550306ca56874236ae6dad68fa86200247860924a2a074100a30":        "builds a key union; all later state changes use its sorted slice",
	"internal/ai/strategic.go *Strategic.InitClassVectors 8fcd02e17c7dda3b0d0567c71d54830e90c9fd280ef197ebe5b1da2051e87e93":        "builds a key union; all later state changes use its sorted slice",
	"internal/ai/strategic.go *Strategic.InitClassVectors e79578d327977829fe121c11424ea67b57c82eac29b688bab842f58aff975676":        "builds a key union; all later state changes use its sorted slice",
	"internal/ai/strategic.go *Strategic.InitClassVectors 18908f679e53e3c7fe9e577e28acb6299128b38a3a14cacbd77646bff7b9c5e7":        "gathers a key union and sorts it before class-vector work",
	"internal/ai/strategic.go *Strategic.refreshCountsAndCenter a051d045678d378db6571dac1ce6ecf5c01d4d653a78570d6dcbdfe0488ef0dc":  "resets independent per-key counters to one constant",
	"internal/ai/strategic.go *Strategic.refreshCountsAndCenter 19c9bf5a91895121468b9dbc6ab2055bb4893f34da93ee33a3dfe2371bb623ea":  "adds absent keys with one constant before ordered unit traversal",
	"internal/ai/strategic.go *Strategic.recomputeClassVectors 8fcd02e17c7dda3b0d0567c71d54830e90c9fd280ef197ebe5b1da2051e87e93":   "builds a key union; all class-vector work uses its sorted slice",
	"internal/ai/strategic.go *Strategic.recomputeClassVectors 9590d1c8637fdc22b9f638f74db726374c799501b11248f120d070556227dd92":   "builds a key union; all class-vector work uses its sorted slice",
	"internal/ai/strategic.go *Strategic.recomputeClassVectors e79578d327977829fe121c11424ea67b57c82eac29b688bab842f58aff975676":   "builds a key union; all class-vector work uses its sorted slice",
	"internal/ai/strategic.go *Strategic.recomputeClassVectors 29779bffdb49550306ca56874236ae6dad68fa86200247860924a2a074100a30":   "builds a key union; all class-vector work uses its sorted slice",
	"internal/ai/strategic.go *Strategic.recomputeClassVectors 18908f679e53e3c7fe9e577e28acb6299128b38a3a14cacbd77646bff7b9c5e7":   "gathers a key union and sorts it before class-vector work",
	"internal/construction/placement.go *Service.BuilderLinks 8466aaa44621ca02916ab416518e813fc183fba49d5412297e13e5a9d8a35fe9":    "returns an independent map copy; it performs no simulation update",
	"internal/construction/placement.go *Service.SnapshotLinks 25a0e243eae77988d06fc0ea4c7529adb42fc0c4bc4a0a0b640eee53edbee946":   "gathers records and sorts product then builder before returning",
	"internal/session/ai_entry.go initializeBattleAI ccc09334e9842e8b751ea2fb6be83c568675169ad3be0e46d82e3aa365396b71":             "gathers catalog keys and sorts them before strategic initialization",
	"internal/session/mission.go pruneRestrictedBuildMenus 7e59e489ce3fe77412e3b63fa6daf9a84cea740f31e48ffa7cc12a8ce2812e4f":       "gathers builder keys and sorts them before rewriting the catalog",
}

// mapFunctionHashes bind the enclosing operation as well as its individual
// map range. This makes a sort, callback or other ordering dependency part of
// the audit rather than allowing it to change behind an unchanged range body.
var mapFunctionHashes = map[string]string{
	// On-demand diagnostic projections copy into local storage only. Pin their
	// sorts and full read operations so future edits require another I1 review.
	"internal/cob/debug_capture.go *VM.DebugSnapshot":            "f19b940e78dceab1d9c60fabb131113f547a21ce600853d5626368935c55d2f1",
	"internal/features/debug_capture.go *Service.DebugSnapshot":  "b88eeadbf744ab1a8e7d0580cf69be24c9dbbba5c567641de4968dc3aa80a6ad",
	"internal/ai/manager.go *Manager.EnsureStrategicInitialized": "cba316ea4945f9095fea37a058826bc5f346f0451711b017b426d5ce2eefa660",
	"internal/ai/profile.go cloneWeightTable":                    "c24030e7c8fcd13b14aeae9ef333f94c571fd0b0b98e59b14a3d7db12c86636e",
	"internal/ai/profile.go *Profile.Difficulties":               "6ebccbe5fdec25f142139476db16d2e8dc5a6ca6df55bbfbf4a21842b8fc10bd",
	"internal/ai/profile.go LoadProfile":                         "119b5500c827867e150450327bd13e543b91f5a0711b8b9df6f7a4eb7fc4708f",
	"internal/ai/strategic.go *Strategic.InitClassVectors":       "22eab29d4bc94cc0442920275dd3e5613c324a5349d32f9880093cf0b854f64f",
	"internal/ai/strategic.go *Strategic.refreshCountsAndCenter": "27025603520a216e18ea26c811992220b05173da9fe49ce7ec544913f7eb2b40",
	// Re-audited: the first pass now accumulates at retail's 53-bit working
	// precision [08 "Arithmetic and clamping"]. The key union, its sort and the
	// order every consumer sees are unchanged (I1).
	"internal/ai/strategic.go *Strategic.recomputeClassVectors": "5d879af01880139d273155d9a616fbd7256484085ee699ffb0d8e22e7f9bb9f3",
	"internal/cob/binding.go BindStrict":                        "833dd320728882053831252fe1cf9315c0a42c67042b3f2ae9d510bb912ef0f4",
	"internal/construction/placement.go *Service.BuilderLinks":  "ab64326b5234a8e82416673727052e72d47df694047af752dba5b34341f3c0bf",
	"internal/construction/placement.go *Service.SnapshotLinks": "c001891b664d5693829dc524e5c7bad19b8a14c4341d396557013d6a5c1b40ca",
	// Re-audited: the rebuild now also fills the parallel value row the two
	// per-tick walks read, so the map is walked once and hashed once instead
	// of being hashed again per key at every walk. The range body, the sort
	// and the order every consumer sees are unchanged (I1).
	"internal/features/service.go *Service.sortedInstanceKeys":    "039ab36d7a77f4e480107a0613c31452e218f0e58a6ccbfec64b6f5689119730",
	"internal/save/battle_image.go validateCarrierReferenceGraph": "3dd0048516ffdf5e47c88f0d813e8ea79da1990b598ab004ebc0fd0f6d031da0",
	"internal/session/ai_entry.go initializeBattleAI":             "dcfe11a8f7d0c19b5c30f5f0342d56c6785414e911fabe66e1d5dc473838890e",
	"internal/session/mission.go pruneRestrictedBuildMenus":       "83d3b5133ab38d04122d1387192dfc78679578f581c1a7abc6719332765b642e",
	"internal/units/cob_binding.go bindUnitPortHandlers":          "31dfa18e2683e5bec165669f55e4f5dad260588513831504622f3b1596ba5548",
	"internal/units/cob_binding.go bindCOBWithPortsAndVisibility": "68a25d9937cf032996d5feb8d96ce60224e51e9aa8e1bc13808fd2eff2ecfe75",
}

func checkAuthoritativeTypedMapRanges(t *testing.T) {
	t.Helper()
	root := repositoryRoot(t)
	failures := mapRangeViolations(typedMapRanges(root, loadAuthoritativeTypedPackages(t, root)), mapRangeExceptions, mapFunctionHashes)
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("map iteration (I1) guard failed:\n%s", strings.Join(failures, "\n"))
	}
}

func mapRangeViolations(sites []typedMapRange, rangeExceptions, functionHashes map[string]string) []string {
	seenRanges := map[string]bool{}
	seenFunctions := map[string]bool{}
	var failures []string
	for _, site := range sites {
		key := site.key()
		if _, ok := rangeExceptions[key]; !ok {
			failures = append(failures, fmt.Sprintf("%s:%d: unreviewed map range in %s; key %q; body %s", site.path, site.line, site.function, key, site.body))
			continue
		}
		seenRanges[key] = true
		functionKey := site.functionKey()
		expected, ok := functionHashes[functionKey]
		if !ok {
			failures = append(failures, fmt.Sprintf("%s:%d: map range has no function audit for %q", site.path, site.line, functionKey))
			continue
		}
		seenFunctions[functionKey] = true
		if site.functionHash != expected {
			failures = append(failures, fmt.Sprintf("%s:%d: audited map function changed: %s", site.path, site.line, functionKey))
		}
	}
	for key := range rangeExceptions {
		if !seenRanges[key] {
			failures = append(failures, "stale map-range exception: "+key)
		}
	}
	for key := range functionHashes {
		if !seenFunctions[key] {
			failures = append(failures, "stale map-function exception: "+key)
		}
	}
	return failures
}

type float64Allowance struct {
	count  int
	reason string
}

// float64FieldAllowances names the binary64 storage fields found in package-level
// named struct declarations. The key retains the declaration and authored type syntax,
// so grouped fields and named or aliased float types cannot hide new state.
var float64FieldAllowances = map[string]string{
	// Audited model rotation coefficients: cached draw/admission transform trig,
	// never new simulation state; geometry narrows after each rotation [I2].
	"internal/model/model.go type xformNode.cx float64": "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type xformNode.sx float64": "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type xformNode.cy float64": "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type xformNode.sy float64": "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type xformNode.cz float64": "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type xformNode.sz float64": "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type trigNode.cx float64":  "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type trigNode.sx float64":  "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type trigNode.cy float64":  "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type trigNode.sy float64":  "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type trigNode.cz float64":  "I2 model piece rotation trig cache [03 §2.4]",
	"internal/model/model.go type trigNode.sz float64":  "I2 model piece rotation trig cache [03 §2.4]",

	"internal/economy/ledger.go type Player.Waste [2]float64":                        "I2 cumulative waste counters [05 \"Stocks, counters, and waste\"]",
	"internal/economy/ledger.go type Player.TotalProduced [2]float64":                "I2 cumulative totals [05 \"Stocks, counters, and waste\"]",
	"internal/economy/ledger.go type Player.TotalConsumed [2]float64":                "I2 cumulative totals [05 \"Stocks, counters, and waste\"]",
	"internal/economy/p28_parity_trace.go type PlayerTrace.Waste [2]float64":         "I2 opt-in trace copy of cumulative waste (P28-OBS-00C)",
	"internal/economy/p28_parity_trace.go type PlayerTrace.TotalProduced [2]float64": "I2 opt-in trace copy of cumulative totals (P28-OBS-00C)",
	"internal/economy/p28_parity_trace.go type PlayerTrace.TotalConsumed [2]float64": "I2 opt-in trace copy of cumulative totals (P28-OBS-00C)",

	"internal/mission/mission_globals.go type MissionGlobals.TidalStrength float64": "I2 immutable authored map-global value [02 map-global keys]",
	"internal/mission/mission_globals.go type MissionGlobals.KillMul float64":       "I2 immutable authored map-global value [02 map-global keys]",
	"internal/mission/mission_globals.go type MissionGlobals.TimeMul float64":       "I2 immutable authored map-global value [02 map-global keys]",
	"internal/save/bank.go type DoubleItem.Value float64":                           "I13 HAPIBANK account record double",
	"internal/save/boxes.go type PlayerSlot.TotalEnergyProduced float64":            "I2/I13 player save-box double",
	"internal/save/boxes.go type PlayerSlot.TotalMetalProduced float64":             "I2/I13 player save-box double",
	"internal/save/boxes.go type PlayerSlot.TotalEnergyConsumed float64":            "I2/I13 player save-box double",
	"internal/save/boxes.go type PlayerSlot.TotalMetalConsumed float64":             "I2/I13 player save-box double",
	"internal/save/boxes.go type PlayerSlot.EnergyWasted float64":                   "I2/I13 player save-box double",
	"internal/save/boxes.go type PlayerSlot.MetalWasted float64":                    "I2/I13 player save-box double",
}

// float64ScopeAllowances is intentionally declaration-scoped. Each entry
// names the precise retail operation that needs binary64; all other float64
// occurrences still use the shrink-only per-file baseline.
var float64ScopeAllowances = map[string]float64Allowance{
	"internal/model/model.go func *xformNode.evaluateRotation": {3, "I2 per-axis model rotation trigonometry [03 §2.4]"},
	"internal/model/model.go func applyChain":                  {18, "I2 model vertex working precision; round after each axis then narrow geometry [03 §2.4]"},

	"internal/combat/meteor.go func MeteorDelay":               {2, "I2 meteor source float32, working quotient and signed64/low32 conversion [06 §6.5][01 R-DET-01 §1]"},
	"internal/combat/meteor.go func MeteorDurationTicks":       {2, "I2 meteor source float32, working product and signed64/low32 conversion [06 §6.5][01 R-DET-01 §1]"},
	"internal/combat/meteor.go func MeteorIntervalTicks":       {2, "I2 meteor source float32, working product and signed64/low32 conversion [06 §6.5][01 R-DET-01 §1]"},
	"internal/session/step.go func *Session.initMeteor":        {3, "I2 selected-schema meteor source stores enter the documented working-precision conversion helpers [06 §6.5][01 R-DET-01 §1]"},
	"internal/clock/clock.go func *State.budget":               {5, "I2 clock budget product and float32 carry [01 §4.2]"},
	"internal/clock/clock.go func *State.effectiveSpeedLocked": {3, "I2 clock speed multiplier [01 §4.2]"},
	"internal/clock/clock.go func lagThrottleFactor":           {6, "I2 retained multiplayer throttle expression [01 §4.2]"},
	"internal/clock/clock.go func decodeBox":                   {2, "I2 clock save-box float32 validation [01 §4.2]"},

	"internal/economy/admission.go func settlePure":                   {12, "I2 settlement working precision [05 R-ECO-01 §1][05 R-ECO-01 §5]"},
	"internal/economy/admission.go func *Service.settleOneResource":   {12, "I2 settlement working precision [05 R-ECO-01 §1][05 R-ECO-01 §5]"},
	"internal/economy/ledger.go func rebuildCapacityPlayer":           {4, "I2 economy capacity working precision [05 R-ECO-01 §1]"},
	"internal/economy/ledger.go func debitCloakToBucket":              {4, "I2 economy working precision [05 R-ECO-01 §1]"},
	"internal/economy/ledger.go func *Player.commitPassCounters":      {2, "I2 cumulative counters [05 \"Stocks, counters, and waste\"]"},
	"internal/economy/ledger.go func *Player.commitCapacityWaste":     {2, "I2 waste counter [05 \"Stocks, counters, and waste\"]"},
	"internal/economy/ledger.go func ImmediateDebit":                  {8, "I2 economy working precision [05 R-ECO-01 §1]"},
	"internal/economy/ledger.go func *Service.transfer":               {5, "I2 economy working precision [05 R-ECO-01 §1]"},
	"internal/economy/maker.go func addContribution":                  {9, "I2 production contributions and discounts [05 R-ECO-01 §1][05 R-ECO-01 §3]"},
	"internal/economy/maker.go func *Service.PerUnitProductionFills":  {9, "I2 production contributions [05 R-ECO-01 §1][05 R-ECO-01 §3]"},
	"internal/economy/maker.go func creditReclaimedMaterial":          {9, "I2 reclaimed-material accounting [05 R-ECO-01 §1]"},
	"internal/economy/maker.go func *Service.CreditFeatureReclaim":    {2, "I2 reclaimed-material accounting [05 R-ECO-01 §1]"},
	"internal/economy/maker.go func *Service.CreditUnitReclaimRefund": {2, "I2 construction refund [05 R-ECO-01 §11]"},

	"internal/movement/airorders.go func airReleaseLead":  {5, "I2 AirStrike release lead [04 R-AIR-01 §8]"},
	"internal/movement/flight.go func flightGoalDistance": {2, "I2 flight goal distance [04 R-AIR-01 §1]"},
	"internal/movement/flight.go func rotateLeanPair":     {10, "I2 lean rotation [04 R-AIR-01 §2]"},
	"internal/movement/flight.go func IntegrateFlight":    {34, "I2 flight braking and integration temporaries [04 §10.1]"},
	"internal/movement/integrate.go func groundHypotRaw":  {2, "I2 ground follower route distance [04 R-MOV-01 §3]"},

	"internal/combat/motion.go func InitOrdinary":            {2, "I2 ordinary creator stored planar distance [06 §6.3][06 §4.3]"},
	"internal/combat/aim.go func BallisticSolve":             {24, "I2 ballistic discriminant, acos and sqrt [06 §3.3]"},
	"internal/combat/aim.go func distance3DRaw":              {6, "I2 pre-fire lead distance [06 §3.3]"},
	"internal/combat/impact.go func DistanceToBox":           {6, "I2 area-damage range [06 §9.3]"},
	"internal/combat/damage.go func Falloff":                 {4, "I2 area-damage falloff [06 §9.3]"},
	"internal/combat/damage.go func weaponNominal":           {2, "I2 area-damage amount product [06 §9.2]"},
	"internal/construction/reclaim.go func UnitReclaimPulse": {2, "I2 unit-reclaim pulse divide [05 R-WORK-01 §4]"},

	"internal/sim/numeric/numeric.go func TruncateFloat32ToLow32": {1, "I2 exact widening of stored single precision into the shared I3 conversion [01 R-DET-01 §1]"},
	"internal/sim/numeric/numeric.go func TruncateFloat64ToLow32": {3, "I2 authored conversion and I3 signed-low-word narrowing [01 R-DET-01 §1]"},
	"internal/sim/numeric/trig.go const angleScale":               {1, "I2 simulation trig-table construction [04 §5.1]"},
	"internal/sim/numeric/trig.go func AngleFromAtan2":            {2, "I2 simulation trig-table construction [04 §5.1]"},
	"internal/sim/numeric/trig.go func init":                      {1, "I2 simulation trig-table construction [04 §5.1]"},

	"internal/save/boxes.go func WritePlayerSlot":   {4, "I2/I13 player save-box doubles"},
	"internal/save/boxes.go func ReadPlayerSlot":    {2, "I2/I13 player save-box doubles"},
	"internal/save/bank.go func *Account.SetDouble": {1, "I13 HAPIBANK account record double"},
	"internal/save/bank.go func *Account.Double":    {1, "I13 HAPIBANK account record double"},

	"internal/session/strips.go func nanoLifetimeTicks": {7, "I2 nanolathe particle travel distance [03 §5.5]"},
	"internal/session/strips.go func sprinkleStep":      {9, "I2 strip-object span [03 R-FX-01 §3]"},
	"internal/session/strips.go func flameSegLife":      {9, "I2 strip-object span [03 R-FX-02 §2]"},

	"internal/orders/work.go const captureCostScale,captureEnergyUnit,captureMetalUnit,captureBias,captureClampMax": {4, "I2 capture timer constants [05 R-WORK-01 §6]"},
	"internal/orders/work.go func captureBudget":     {9, "I2 capture timer base sum [05 R-WORK-01 §6]"},
	"internal/orders/work.go func resurrectionDelay": {3, "I2 resurrection delay [05 R-WORK-01 §7]"},
}

func checkAuthoritativeFloat64(t *testing.T) {
	t.Helper()
	checkAuthoritativeFloat64Fields(t)
	current := map[string]int{}
	sites := map[string][]occurrence{}
	seen := map[string]bool{}
	var failures []string
	for _, scope := range float64Scopes(t) {
		if allowance, ok := float64ScopeAllowances[scope.key()]; ok {
			seen[scope.key()] = true
			if violation := float64ScopeViolation(scope, allowance); violation != "" {
				failures = append(failures, violation)
			}
			continue
		}
		current[scope.path] += scope.count
		sites[scope.path] = append(sites[scope.path], scope.sites...)
	}
	for key, allowance := range float64ScopeAllowances {
		if !seen[key] {
			failures = append(failures, fmt.Sprintf("stale float64 scope allowance %q (%s)", key, allowance.reason))
		}
	}
	failures = append(failures, enforceRatchet(t, "float64 (I2)", float64Baseline, current, sites)...)
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("float64 (I2) guard failed:\n%s", strings.Join(failures, "\n"))
	}
}

func checkAuthoritativeFloat64Fields(t *testing.T) {
	t.Helper()
	root := repositoryRoot(t)
	failures := float64FieldViolations(typedFloat64Fields(root, loadAuthoritativeTypedPackages(t, root)), float64FieldAllowances)
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("float64 fields (I2) guard failed:\n%s", strings.Join(failures, "\n"))
	}
}

func float64FieldViolations(fields []typedFloat64Field, allowances map[string]string) []string {
	seen := map[string]bool{}
	var failures []string
	for _, field := range fields {
		if _, ok := allowances[field.key]; !ok {
			failures = append(failures, fmt.Sprintf("%s:%d: unreviewed float64 field %q", field.path, field.line, field.key))
			continue
		}
		seen[field.key] = true
	}
	for key := range allowances {
		if !seen[key] {
			failures = append(failures, "stale float64 field allowance: "+key)
		}
	}
	return failures
}

func float64ScopeViolation(scope floatScope, allowance float64Allowance) string {
	if scope.count == allowance.count {
		return ""
	}
	return fmt.Sprintf("float64 scope %q changed %d -> %d (%s); sites %s", scope.key(), allowance.count, scope.count, allowance.reason, formatSites(scope.sites))
}

func formatSites(sites []occurrence) string {
	parts := make([]string, 0, len(sites))
	for _, site := range sites {
		parts = append(parts, fmt.Sprintf("%d:%s", site.line, site.text))
	}
	return strings.Join(parts, "; ")
}

func TestTypedMapRangeDetectorFindsMapExpressions(t *testing.T) {
	root := t.TempDir()
	const source = `package fixture
type named map[int]int
type holder struct { values map[int]int }
func returned() map[int]int { return nil }
func ranges(h holder) {
	local := map[int]int{}
	for range local {}
	for range h.values {}
	var n named
	for range n {}
	for range returned() {}
	keys := []int{1, 2}
	for range keys {}
}`
	pkg := typedFixture(t, filepath.Join(root, "fixture.go"), source)
	ranges := typedMapRanges(root, []typedPackage{pkg})
	if len(ranges) != 4 {
		t.Fatalf("typed map ranges = %d, want 4: %#v", len(ranges), ranges)
	}
	for _, site := range ranges {
		if !strings.Contains(site.function, "ranges") {
			t.Fatalf("map range attributed to %q, want ranges", site.function)
		}
	}
}

func TestMapRangeAuditRejectsChangedOrderingFunction(t *testing.T) {
	root := t.TempDir()
	const audited = `package fixture
func sortKeys([]int) {}
func sorted(m map[int]int) {
	keys := make([]int, 0, len(m))
	for key := range m { keys = append(keys, key) }
	sortKeys(keys)
}`
	const changed = `package fixture
func sortKeys([]int) {}
func sorted(m map[int]int) {
	keys := make([]int, 0, len(m))
	for key := range m { keys = append(keys, key) }
	_ = keys
}`
	baseline := typedMapRanges(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), audited)})
	if len(baseline) != 1 {
		t.Fatalf("audited fixture map ranges = %d, want 1", len(baseline))
	}
	allowedRanges := map[string]string{baseline[0].key(): "fixture sorts gathered keys"}
	allowedFunctions := map[string]string{baseline[0].functionKey(): baseline[0].functionHash}
	if got := mapRangeViolations(baseline, allowedRanges, allowedFunctions); len(got) != 0 {
		t.Fatalf("audited fixture failed: %s", strings.Join(got, "; "))
	}
	changedRanges := typedMapRanges(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), changed)})
	if got := mapRangeViolations(changedRanges, allowedRanges, allowedFunctions); len(got) == 0 {
		t.Fatal("removed sort silently passed map-range audit")
	}
}

func TestTypedMapRangeDetectorFindsPackageInitializerClosure(t *testing.T) {
	root := t.TempDir()
	const source = `package fixture
var initialized = func() int {
	values := map[int]int{1: 1}
	for range values {}
	return 0
}()
`
	ranges := typedMapRanges(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), source)})
	if len(ranges) != 1 || !strings.HasPrefix(ranges[0].function, "package-init closure@") {
		t.Fatalf("initializer map ranges = %#v, want one package-init closure", ranges)
	}
}

func TestFloat64FieldAuditRejectsGroupedAndNamedFields(t *testing.T) {
	root := t.TempDir()
	const before = `package fixture
type record struct { Allowed float64 }
`
	const after = `package fixture
type scalar float64
type record struct {
	Allowed, Unsafe float64
	Aliased scalar
	Values []float64
	Nested struct { Deep []float64 }
	scalar
}
`
	baseline := typedFloat64Fields(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), before)})
	if len(baseline) != 1 {
		t.Fatalf("fixture baseline fields = %d, want 1", len(baseline))
	}
	allowances := map[string]string{baseline[0].key: "fixture's one authorized field"}
	afterFields := typedFloat64Fields(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), after)})
	if got := float64FieldViolations(afterFields, allowances); len(got) != 5 {
		t.Fatalf("float field shape violations = %d, want 5: %s", len(got), strings.Join(got, "; "))
	}
}

func TestFloat64FieldAuditChecksContainersAndTerminatesCycles(t *testing.T) {
	root := t.TempDir()
	const source = `package fixture
type tree []tree
type leaves []struct { Next leaves; Value float64 }
type scalar float64
type record struct {
	Pointer *float64
	Map map[int]scalar
	Keys map[float64]int
	Nested []*struct { Value float64 }
	Channel chan scalar
	Recursive leaves
	Empty tree
	Callback func(float64) float64
}
`
	fields := typedFloat64Fields(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), source)})
	violations := float64FieldViolations(fields, nil)
	for _, name := range []string{"Pointer", "Map", "Keys", "Nested", "Channel", "Recursive"} {
		found := false
		for _, violation := range violations {
			found = found || strings.Contains(violation, "record."+name+" ")
		}
		if !found {
			t.Errorf("missing violation for %s: %v", name, violations)
		}
	}
	if len(violations) != 6 {
		t.Fatalf("container field violations = %v, want six numeric storage fields", violations)
	}
}

func TestFloat64FieldAuditDoesNotPromotePointerTargetFields(t *testing.T) {
	root := t.TempDir()
	const source = `package fixture
type owner struct { Value float64 }
type record struct { Owner *owner }
`
	fields := typedFloat64Fields(root, []typedPackage{typedFixture(t, filepath.Join(root, "fixture.go"), source)})
	if len(fields) != 1 {
		t.Fatalf("pointer fixture fields = %#v, want one owner field", fields)
	}
	if got, want := fields[0].key, "fixture.go type owner.Value float64"; got != want {
		t.Fatalf("pointer fixture key = %q, want %q", got, want)
	}
}

func TestFloat64ScopeAuditKeepsNonStructTypeDeclarations(t *testing.T) {
	root := t.TempDir()
	pkg := typedFixture(t, filepath.Join(root, "fixture.go"), `package fixture
type scalar float64
`)
	decl := pkg.files[0].Decls[0].(*ast.GenDecl)
	typeSpec := decl.Specs[0].(*ast.TypeSpec)
	scope, ok := float64ScopeForNode("fixture.go", pkg.fset, func(int) string { return "type scalar float64" }, "type scalar", typeSpec)
	if !ok || scope.count != 1 {
		t.Fatalf("non-struct type scope = %#v, present = %t; want one float64 token", scope, ok)
	}
}

func typedFixture(t *testing.T, filename, source string) typedPackage {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	info := &types.Info{
		Defs:  map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	if _, err := (&types.Config{}).Check("fixture", fset, []*ast.File{file}, info); err != nil {
		t.Fatalf("type-check fixture: %v", err)
	}
	return typedPackage{importPath: "fixture", fset: fset, files: []*ast.File{file}, info: info}
}
