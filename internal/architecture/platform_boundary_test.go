package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

// platformOnly are the packages allowed to reach Ebitengine: the concrete
// window/loop/device adapter, the PCM device boundary, and the desktop binary
// that links them.
var platformOnly = map[string]bool{
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp": true,
	"github.com/nanolathe/nanolathe/internal/audiobackend":       true,
	"github.com/nanolathe/nanolathe/cmd/nanolathe":               true,
}

// TestOnlyThePlatformAdapterReachesEbitengine keeps the displayless boundary
// honest: every other package, including its test binary, must stand up with
// no window and no audio device.
//
// This is a dependency-closure guard rather than a count, so it does not
// obstruct cleanup: it fails only when a package acquires a device dependency
// it did not have.
func TestOnlyThePlatformAdapterReachesEbitengine(t *testing.T) {
	root := repositoryRoot(t)

	// One `go list` pass over the module, not one per package. `-test` adds
	// the test-augmented package and the test binary to the listing, which is
	// where the leak into internal/session lived: session's tests imported the
	// client, and the client owned the window loop and built the audio device.
	// `.Deps` is already the full transitive closure, so the per-package
	// `-deps` invocation — which re-loaded the module graph once per package —
	// bought nothing but wall time.
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
		if platformOnly[owningPackage(name)] {
			continue
		}
		for _, dep := range strings.Fields(deps) {
			if strings.HasPrefix(dep, "github.com/hajimehoshi/ebiten/v2") ||
				strings.HasPrefix(dep, "github.com/ebitengine/") {
				t.Errorf("%s reaches the platform dependency %s", name, dep)
				break
			}
		}
	}
}

// owningPackage reduces the four shapes `go list -test` prints for one
// package — `P`, `P [P.test]`, `P_test [P.test]` and `P.test` — to P, the
// package a leak would have to be fixed in and the key platformOnly is
// written in.
func owningPackage(importPath string) string {
	name := importPath
	if i := strings.Index(name, " ["); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSuffix(name, ".test")
	return strings.TrimSuffix(name, "_test")
}
