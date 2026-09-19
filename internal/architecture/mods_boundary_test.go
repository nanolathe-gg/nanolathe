package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

// modPackagePrefix is the module path of the mod list and the sets it links.
const modPackagePrefix = "github.com/nanolathe-gg/nanolathe/mods"

// TestOnlyCommandsImportTheModList keeps the rule-set extension point pointing
// one way: cmd → mods → session → simulation.
//
// `mods` is the list of gameplay rule sets a build links, so anything that
// imports it inherits whichever sets happen to be in the tree. A command may —
// selecting a set by name is its job. A package under internal/ may not: its
// behavior would then depend on the mod list rather than on the set the
// session bound, and a set that composes a package's own implementations would
// close the loop into an import cycle
// (docs/DESIGN_GAMEPLAY_RULES.md §8, docs/ARCHITECTURE.md §3).
//
// Like the platform-boundary guard, this is a dependency-closure check over
// the whole module including test binaries, not a count, so it fails only when
// a package acquires the dependency.
func TestOnlyCommandsImportTheModList(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("go", "list", "-test", "-f", "{{.ImportPath}}|{{join .Deps \" \"}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -test ./...: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, deps, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		owner := owningPackage(name)
		if !strings.Contains(owner, "/internal/") && !strings.HasSuffix(owner, "/internal") {
			continue // a command, a mod, a probe or a tool may link the list
		}
		for _, dep := range strings.Fields(deps) {
			if dep == modPackagePrefix || strings.HasPrefix(dep, modPackagePrefix+"/") {
				t.Errorf("%s imports the mod list %s; only a command may", name, dep)
				break
			}
		}
	}
}
