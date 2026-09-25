package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	nanolatheModule   = "github.com/nanolathe-gg/nanolathe"
	modFetchPackage   = nanolatheModule + "/internal/modfetch"
	modLibraryPackage = nanolatheModule + "/internal/modlibrary"
	desktopCommand    = nanolatheModule + "/cmd/nanolathe"
)

// TestNetworkStaysInTheModFetcher pins the mod library's network boundary
// (docs/DESIGN_MODS_MUTATORS.md §9 "Guards"):
//
//  1. internal/modfetch is the only package in the module that imports
//     net/http. This is a DIRECT-import check: Ebitengine's ebitenutil
//     already pulls net/http into the dependency closure of the platform
//     adapter and the desktop command, so a closure check could not hold.
//  2. Only cmd/nanolathe may depend on internal/modfetch, so the displayless
//     command and every internal package stay off the network (D5).
//  3. No authoritative package depends on internal/modlibrary or
//     internal/modfetch: mod selection happens before a session exists and
//     never reaches a tick.
//
// Like the platform guard, it runs `go list -test` over the whole module so a
// test binary cannot smuggle the dependency in either.
func TestNetworkStaysInTheModFetcher(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("go", "list", "-test", "-f", "{{.ImportPath}}|{{join .Imports \" \"}}|{{join .Deps \" \"}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -test ./...: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, "|", 3)
		if len(fields) != 3 {
			continue
		}
		name, imports, deps := fields[0], strings.Fields(fields[1]), strings.Fields(fields[2])
		owner := owningPackage(name)

		if owner != modFetchPackage {
			for _, imported := range imports {
				if imported == "net/http" || strings.HasPrefix(imported, "net/http/") {
					t.Errorf("%s imports %s; only %s may", name, imported, modFetchPackage)
					break
				}
			}
		}
		if owner != modFetchPackage && owner != desktopCommand {
			for _, dep := range deps {
				if dep == modFetchPackage {
					t.Errorf("%s depends on %s; only %s may", name, modFetchPackage, desktopCommand)
					break
				}
			}
		}
		if isAuthoritativePackage(owner) {
			for _, dep := range deps {
				if dep == modFetchPackage || dep == modLibraryPackage {
					t.Errorf("authoritative package %s depends on %s", name, dep)
					break
				}
			}
		}
	}
}

// isAuthoritativePackage reports whether an import path lies inside one of
// authoritativeDirs, the simulation side of the architecture boundary.
func isAuthoritativePackage(importPath string) bool {
	for _, dir := range authoritativeDirs {
		prefix := nanolatheModule + "/" + dir
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}
