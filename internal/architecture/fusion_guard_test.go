package architecture

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Go specification lets an implementation "combine multiple floating-point
// operations into a single fused operation, possibly across statements, and
// produce a result that differs from the value obtained by executing and
// rounding the instructions individually". The gc arm64 backend does exactly
// that; so does amd64 under GOAMD64=v3, for the `a + b*c` shape only; default
// amd64 does not fuse at all. The same source therefore has three possible
// answers, which is a determinism hazard on its own, and none of the fused ones
// is what retail computes: retail's floating point rounds every product to the
// working precision before it is added, because it has no fused multiply-add.
//
// Several I2 rows are written in those terms — "narrowed by one store at the
// end", "combined and truncated once" — and a fused build does not satisfy
// them. The escape the same paragraph of the specification gives is an explicit
// floating-point conversion of the product, which forces the intermediate
// rounding. Authoritative expressions carry that conversion; this guard proves
// they still do, by reading the generated assembly rather than the source,
// because the backend fuses across statements and through inlining and a
// source-level shape check cannot see either.
//
// Presentation is out of scope BY PACKAGE, not by site: internal/render,
// internal/client, internal/platform/gpurender, internal/upscale,
// internal/camera, internal/audio, internal/hud, internal/gui, internal/input,
// internal/drawlist and cmd/nanolathe are all free to fuse, and most of the
// repository's fused instructions live there. The guard scans authoritativeDirs
// and nothing else, so adding a package to that list is what brings it in.
//
// internal/render/fragment.go's shatter normal is the one presentation site
// worth naming here: it runs inside an RNG-consuming admission path, and it is
// safe for the same reason the allowances below are — every operand is a
// float32, so each product is at most 48 significand bits and exact in
// binary64. Fusing an exact product changes nothing on any host.

// fusionAllowance is one declaration whose fused instructions are provably
// harmless. `sites` is the number of distinct source lines that fuse inside the
// declaration, which is stable under inlining; the instruction count is not,
// because the same line is compiled into every caller it is inlined into.
//
// Shrink-only: a new key needs the exactness argument in `reason`, and the
// count must match exactly, so removing a fusion also means editing the list.
type fusionAllowance struct {
	sites  int
	reason string
}

// fusionAllowances names every remaining fused multiply-add in the
// authoritative packages. Each one multiplies by an exact value, so the single
// rounding a fused instruction performs is the same value two roundings give.
var fusionAllowances = map[string]fusionAllowance{
	"internal/ai/strategic.go func *Strategic.refreshCountsAndCenter": {
		3, "the weighted-centre accumulators scale by the inverse of fixed one, an exact power of two; the position product is rounded by its own multiply first [08 R-P0-05 §10]",
	},
	"internal/economy/admission.go func *Service.settleOneResource": {
		4, "the settlement apply-back multiplies two float32 fields, so the product carries at most 48 significand bits and is exact in binary64 [05 R-ECO-01 §5]",
	},
	"internal/movement/flight.go func IntegrateFlight": {
		2, "the brake excess and the doubled acceleration fuse a multiply by the inverse of fixed one, an exact power of two [04 §10.1] C28",
	},
}

// fusedARM64 are the arm64 fused multiply-add mnemonics; fusedAMD64 are the
// GOAMD64=v3 ones. The amd64 backend only emits the `a + b*c` form, which is
// why its site set is a subset of arm64's today.
var (
	fusedARM64 = map[string]bool{
		"FMADDS": true, "FMADDD": true, "FMSUBS": true, "FMSUBD": true,
		"FNMADDS": true, "FNMADDD": true, "FNMSUBS": true, "FNMSUBD": true,
	}
	fusedAMD64 = regexp.MustCompile(`^VF(N?)M(ADD|SUB)[0-9]*(SS|SD|PS|PD)$`)
)

// assemblyLine matches one instruction in a `-gcflags=-S` dump: an offset, a
// program counter, the source position in parentheses, then the mnemonic.
var assemblyLine = regexp.MustCompile(`^\s+0x[0-9a-f]+\s+\d+\s+\(([^()]+\.go):(\d+)\)\s+([A-Z][A-Z0-9]*)`)

// TestAuthoritativeArithmeticIsNotFused compiles the simulation packages for
// arm64 — whatever the host is, so an Intel run catches an Apple Silicon
// regression and the reverse — and fails on any fused multiply-add that is not
// allowed above.
func TestAuthoritativeArithmeticIsNotFused(t *testing.T) {
	root := repositoryRoot(t)
	declarations := authoritativeDeclarations(t)

	arm := fusionSites(t, root, "arm64", "", func(mnemonic string) bool { return fusedARM64[mnemonic] })
	counts := attributeFusion(t, arm, declarations)

	var failures []string
	for key, allowance := range fusionAllowances {
		if _, ok := counts[key]; !ok {
			failures = append(failures, fmt.Sprintf("stale fusion allowance %q (%s): nothing fuses there any more, shrink the list", key, allowance.reason))
		}
	}
	for key, sites := range counts {
		allowance, ok := fusionAllowances[key]
		if !ok {
			failures = append(failures, fmt.Sprintf("fused multiply-add in %q at %s: round the product with an explicit conversion, or allow it with the argument that the product is exact", key, formatFusionSites(sites)))
			continue
		}
		if len(sites) != allowance.sites {
			failures = append(failures, fmt.Sprintf("fusion allowance %q changed %d -> %d (%s); sites %s", key, allowance.sites, len(sites), allowance.reason, formatFusionSites(sites)))
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("fused multiply-add guard failed (arm64):\n%s", strings.Join(failures, "\n"))
	}
}

// TestAuthoritativeArithmeticIsNotFusedOnAMD64V3 covers the third answer. The
// v3 site set is a subset of arm64's today, which is a measurement rather than
// a guarantee, so it is asserted instead of assumed.
func TestAuthoritativeArithmeticIsNotFusedOnAMD64V3(t *testing.T) {
	root := repositoryRoot(t)
	declarations := authoritativeDeclarations(t)

	sites := fusionSites(t, root, "amd64", "v3", func(mnemonic string) bool { return fusedAMD64.MatchString(mnemonic) })
	counts := attributeFusion(t, sites, declarations)

	var failures []string
	for key, found := range counts {
		allowance, ok := fusionAllowances[key]
		if !ok {
			failures = append(failures, fmt.Sprintf("fused multiply-add in %q at %s (GOAMD64=v3): round the product with an explicit conversion", key, formatFusionSites(found)))
			continue
		}
		if len(found) > allowance.sites {
			failures = append(failures, fmt.Sprintf("fusion allowance %q allows %d sites, GOAMD64=v3 fuses %d (%s); sites %s", key, allowance.sites, len(found), allowance.reason, formatFusionSites(found)))
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("fused multiply-add guard failed (amd64 v3):\n%s", strings.Join(failures, "\n"))
	}
}

// fusionSites compiles every authoritative package with the assembly listing
// enabled and returns the repository-relative `file:line` of each fused
// instruction. stderr is streamed, never buffered: the dumps run to tens of
// megabytes.
func fusionSites(t *testing.T, root, arch, amd64Level string, fused func(string) bool) map[string]int {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH; fusion guard needs the compiler's assembly listing")
	}
	module := modulePath(t, root)
	// The package-qualified form is required: a bare -S can replay a cached
	// build without reprinting the listing.
	// No -o: every authoritative package is a library, so the build writes
	// nothing at all. -o would need a directory and a main package to fill it.
	args := []string{"build", "-gcflags=" + module + "/internal/...=-S"}
	for _, dir := range authoritativeDirs {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			continue // package not present in this worktree
		}
		args = append(args, "./"+dir+"/...")
	}
	command := exec.Command("go", args...)
	command.Dir = root
	level := amd64Level
	if level == "" {
		level = "v1" // the default; it has no effect on an arm64 build
	}
	command.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+arch, "GOAMD64="+level, "CGO_ENABLED=0")
	listing, err := command.StderrPipe()
	if err != nil {
		t.Fatalf("assembly listing pipe: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start %s build: %v", arch, err)
	}
	sites := map[string]int{}
	scanner := bufio.NewScanner(listing)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		match := assemblyLine.FindStringSubmatch(scanner.Text())
		if match == nil || !fused(match[3]) {
			continue
		}
		path := filepath.ToSlash(match[1])
		if relative, err := filepath.Rel(root, match[1]); err == nil && !strings.HasPrefix(relative, "..") {
			path = filepath.ToSlash(relative)
		} else if trimmed, ok := strings.CutPrefix(path, module+"/"); ok {
			// -trimpath (tools/go-budget) reports module paths, not files.
			path = trimmed
		}
		sites[path+":"+match[2]]++
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if scanErr != nil {
		t.Fatalf("read %s assembly listing: %v", arch, scanErr)
	}
	if waitErr != nil {
		t.Fatalf("%s build failed: %v", arch, waitErr)
	}
	return sites
}

// fusionDeclaration is one top-level function and the line span it covers.
type fusionDeclaration struct {
	path       string
	name       string
	first, end int
}

func authoritativeDeclarations(t *testing.T) map[string][]fusionDeclaration {
	t.Helper()
	byPath := map[string][]fusionDeclaration{}
	scanAuthoritativeSources(t, func(path string, fset *token.FileSet, file *ast.File, _ func(int) string) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			byPath[path] = append(byPath[path], fusionDeclaration{
				path:  path,
				name:  "func " + functionName(fset, fn),
				first: fset.Position(fn.Pos()).Line,
				end:   fset.Position(fn.End()).Line,
			})
		}
	})
	return byPath
}

// attributeFusion maps each fused instruction back to the declaration that
// CONTAINS the source line, not to the symbol the instruction was emitted in.
// Inlining puts one source line in many symbols; the declaration is stable.
// Sites outside authoritativeDirs are ignored — the package-qualified -S flag
// also dumps presentation dependencies, which are out of scope.
func attributeFusion(t *testing.T, sites map[string]int, declarations map[string][]fusionDeclaration) map[string][]string {
	t.Helper()
	counts := map[string][]string{}
	for site := range sites {
		separator := strings.LastIndex(site, ":")
		path := site[:separator]
		decls, ok := declarations[path]
		if !ok {
			continue // presentation or generated dependency
		}
		line, err := strconv.Atoi(site[separator+1:])
		if err != nil {
			t.Fatalf("assembly listing gave an unparseable line %q", site)
		}
		key := path + " (file scope)"
		for _, decl := range decls {
			if line >= decl.first && line <= decl.end {
				key = decl.path + " " + decl.name
				break
			}
		}
		counts[key] = append(counts[key], site)
	}
	return counts
}

func formatFusionSites(sites []string) string {
	sorted := append([]string(nil), sites...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}

func modulePath(t *testing.T, root string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(path)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}
